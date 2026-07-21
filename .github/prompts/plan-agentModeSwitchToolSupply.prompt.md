## Plan: Agent 模式切换后工具动态供应

### 问题背景

当前 CLI 在 ask/plan 模式下通过 `switch_to_agent` 工具切换到 agent 模式时，CLI 侧已正确完成两件事：
1. `clientToolDefinitionsForMode("agent")` 返回全量工具定义（含 `shell_exec`、`file_edit`、`write_file`、`run_subtask` 等）。
2. `modeAllowsClientTool("agent", toolName)` 对所有工具返回 `true`。

CLI 的 `switchToAgent()` 返回值中包含 `additional_tools`（完整的工具名、描述、inputSchema），并通过 `submitCallback` 将该结果提交给后端。

但后端在收到回调后，`resolve_callback()` 只做了 `future.set_result(result)`，没有解析 `additional_tools` 并动态更新当前会话的工具列表。后端 `tool_calls()` 仍然使用 `build_context()` 时按原始 `request_type`（ask/plan）构建的 `available_tools` 白名单和 `client_mcp_servers` 元数据缓存。这导致模型在同一轮次内尝试调用 `shell_exec` 等新增工具时，`load_client_mcp_tool_meta()` 找不到工具定义，返回 NOTFOUND。

**只有用户发送下一条消息时**，CLI 才会用新的 `sessionState.RequestMode = "agent"` 发起 Stream 请求，后端 `build_context()` 才以 `request_type=agent` 重新构建工具列表，工具才真正可用。

### 目标

在 `switch_to_agent` 回调到达后端时，后端动态解析 `additional_tools` 并注入当前会话的工具列表，使模型在同一轮次内即可调用新增工具，无需等待下一轮用户输入。

### 边界

- 仅影响当前 Stream 会话，不修改持久化配置。
- 不改变 `build_context()` 的初始构建逻辑，只在回调到达时做增量注入。
- 不影响下一次请求的正常 `build_context()` 流程。
- CLI 侧无需改动，`switchToAgent()` 返回值已包含后端所需的全部信息。

### 涉及文件

| 文件路径 | 仓库 | 改动类型 |
|---------|------|---------|
| `src/backend/base/taiyiflow/services/mcp_tool/service.py` | taiyiflow | 核心改动：回调结果解析与工具动态注入 |
| `src/backend/base/taiyiflow/services/agent/service.py` | taiyiflow | 辅助改动：`AgentContext` 工具列表更新方法 |
| `src/backend/base/taiyiflow/services/mcp_tool/utils.py` | taiyiflow | 可能扩展：客户端工具元数据缓存写入 |

### Steps

#### 1. 阶段一：后端回调结果解析（阻塞后续）

**文件**: `src/backend/base/taiyiflow/services/mcp_tool/service.py`

在 `resolve_callback()` 或 `tool_calls()` 处理客户端工具回调结果时，增加对 `additional_tools` 的解析逻辑。

需要确认的具体位置：
- `resolve_callback()` 方法当前只做 `future.set_result(result)` / `future.set_exception(error)`，回调结果被消费方（`tool_calls()` 中的 `await asyncio.wait_for(future, ...)`）取出后没有后续处理。
- 应在 `tool_calls()` 中客户端工具调用结果返回后（即 `await asyncio.wait_for(future, ...)` 之后），检查结果中是否包含 `additional_tools` 字段。

新增方法（伪代码）：

```python
def _maybe_apply_mode_switch_result(self, callback_result, *, agent_context, session_holder):
    """检查 switch_to_agent 的回调结果，动态更新当前会话的工具列表。"""
    if not isinstance(callback_result, dict):
        return
    additional_tools = callback_result.get("additional_tools")
    switched_to = callback_result.get("switched_to")
    if switched_to != "agent" or not additional_tools:
        return

    # 1. 将 additional_tools 的工具名加入 available_tools 白名单
    for tool_def in additional_tools:
        tool_name = tool_def.get("name", "")
        if tool_name and tool_name not in (agent_context.available_tools or []):
            agent_context.available_tools.append(tool_name)

    # 2. 将 additional_tools 的工具定义加入 client_mcp_servers 元数据缓存
    #    使后续 load_client_mcp_tool_meta() 能找到这些工具
    for tool_def in additional_tools:
        tool_name = tool_def.get("name", "")
        if tool_name:
            # 写入与 client_mcp_tool_to_openai_tools() 相同结构的元数据
            self._client_tool_metas[tool_name] = {
                "name": tool_name,
                "description": tool_def.get("description", ""),
                "inputSchema": tool_def.get("inputSchema", {}),
            }

    # 3. 同步更新 session_holder 中的 available_tools
    if session_holder is not None:
        session_holder.available_tools = list(agent_context.available_tools)
        session_holder.config.setdefault("agent_runtime", {})
        session_holder.config["agent_runtime"]["available_tools"] = list(agent_context.available_tools)
```

**验证方式**：
- 确认 `_maybe_apply_mode_switch_result` 在 `switch_to_agent` 回调到达时被调用。
- 确认非 `switch_to_agent` 的工具回调不会触发该逻辑。
- 确认 `additional_tools` 为空或缺失时不做任何变更。

#### 2. 阶段二：客户端工具元数据缓存写入（依赖步骤 1）

**文件**: `src/backend/base/taiyiflow/services/mcp_tool/service.py` 和 `src/backend/base/taiyiflow/services/mcp_tool/utils.py`

需要确认 `load_client_mcp_tool_meta()` 的数据来源和缓存结构，确保步骤 1 中写入的 `_client_tool_metas` 能被 `tool_calls()` 中的查找逻辑命中。

需要核对的关键逻辑：
- `load_client_mcp_tool_meta(client_mcp_servers, function_name)` 如何从 `client_mcp_servers` 中查找工具定义。
- `client_mcp_servers` 的数据结构（CLI 传入的 `ClientMCPServers` 字段格式）。
- 是否需要将 `additional_tools` 转换为与 `client_mcp_servers` 相同的结构后追加到当前会话的 `client_mcp_servers` 列表中。

可能的替代方案（如果直接操作元数据缓存有风险）：
- 将 `additional_tools` 转换为 `client_mcp_servers` 格式，追加到当前会话的 `client_mcp_servers` 变量中，使后续 `tool_calls()` 调用 `load_client_mcp_tool_meta()` 时能自然命中。

**验证方式**：
- 在 `switch_to_agent` 回调后，`tool_calls()` 中调用 `load_client_mcp_tool_meta(client_mcp_servers, "shell_exec")` 能返回非 None。
- 模型在同一轮次内调用 `shell_exec` 时不再返回 NOTFOUND。

#### 3. 阶段三：AgentContext 与 session 同步（依赖步骤 2）

**文件**: `src/backend/base/taiyiflow/services/agent/service.py`

确认 `AgentContext` 是否在 `build_context()` 后保持引用语义（即修改 `agent_context.available_tools` 后，后续 `tool_calls()` 中使用的 `available_tools` 变量能感知到变更）。

需要核对的关键逻辑：
- `tool_calls()` 方法中 `available_tools` 参数的来源：是从 `agent_context.available_tools` 传入的引用，还是 `build_context()` 时复制的快照。
- 如果是快照，需要在 `tool_calls()` 中改为从 `agent_context` 实时读取，或者在 `_maybe_apply_mode_switch_result` 中同时更新 `tool_calls()` 使用的局部变量。

**验证方式**：
- 在 `switch_to_agent` 回调后，`agent_context.available_tools` 包含 `shell_exec`、`file_edit` 等工具名。
- `session_holder.available_tools` 和 `session_holder.config["agent_runtime"]["available_tools"]` 同步更新。

#### 4. 阶段四：auto_loop 衔接确认（依赖步骤 3）

**文件**: `src/backend/base/taiyiflow/services/agent/service.py`

确认 agent 模式下 `auto_loop` 默认启用（`hermes` 策略），模型在收到 `switch_to_agent` 结果后会在同一轮次内继续推理。

需要核对的关键逻辑：
- `resolve_runtime_strategy()` 在 agent 模式下返回 `hermes`。
- `hermes` 策略下，工具回调结果返回后模型是否会自动继续推理（而非等待用户输入）。
- 如果 `auto_loop` 未启用，模型在收到 `switch_to_agent` 结果后可能不会继续调用工具，此时动态注入的工具列表不会在当前轮次被使用——这不是 bug，但需要明确行为预期。

**验证方式**：
- 在 `hermes` 策略下，`switch_to_agent` 回调后模型自动继续推理并调用 `shell_exec`。
- 如果 `auto_loop` 未启用，确认模型至少在下一次用户输入时能正常使用全量工具（这是已有行为，不应被破坏）。

#### 5. 阶段五：端到端验证（依赖步骤 4）

**测试场景**：

1. **ask 模式 → switch_to_agent → 同轮次调用工具**：
   - 在 ask 模式下发送需要执行命令的任务。
   - 模型调用 `switch_to_agent`。
   - 模型在同一轮次内调用 `shell_exec`。
   - 预期：`shell_exec` 正常执行，不返回 NOTFOUND。

2. **plan 模式 → switch_to_agent → 同轮次调用工具**：
   - 在 plan 模式下发送需要编辑文件的任务。
   - 模型调用 `switch_to_agent`。
   - 模型在同一轮次内调用 `file_edit`。
   - 预期：`file_edit` 正常执行，不返回 NOTFOUND。

3. **ask 模式不切换 → 调用受限工具应被拒绝**：
   - 在 ask 模式下直接尝试调用 `shell_exec`（不经过 `switch_to_agent`）。
   - 预期：返回 NOTFOUND，行为与改动前一致。

4. **下一轮次工具列表一致性**：
   - `switch_to_agent` 后，用户发送下一条消息。
   - 预期：`build_context()` 以 `request_type=agent` 正常构建全量工具列表，行为与改动前一致。

**验证方式**：
- 场景 1 和 2 通过：说明动态注入生效。
- 场景 3 通过：说明没有误放行非 agent 模式的工具调用。
- 场景 4 通过：说明没有破坏已有的下一轮次正常流程。

### 风险点

1. **线程安全**：`_pending_callbacks` 和 `available_tools` 的并发修改。后端是 asyncio 单线程模型，`resolve_callback()` 和 `tool_calls()` 在同一事件循环中，不存在真正的并发竞争，但需要确认 `future.set_result()` 后的回调链是否在同一个协程中执行。
2. **工具定义格式兼容**：CLI 的 `additional_tools` 中 `inputSchema` 使用 JSON Schema 格式，需要确认与后端 `load_client_mcp_tool_meta()` 期望的格式一致。如果不一致，需要做格式转换。
3. **多次 switch_to_agent**：如果模型在同一会话中多次调用 `switch_to_agent`，`_maybe_apply_mode_switch_result` 应做幂等处理（去重），避免重复追加。
4. **session 持久化影响**：动态注入的工具列表如果写入 `session_holder`，会在下一次请求时被 `build_context()` 读取。需要确认 `build_context()` 是否会以 `request_type` 为准重新过滤（如果是，则动态注入的工具不会残留到下一轮；如果否，则可能残留多余工具）。

### 不做的事

- 不修改 CLI 侧代码。CLI 的 `switchToAgent()` 返回值已包含后端所需的全部信息。
- 不修改 `build_context()` 的初始构建逻辑。初始构建仍按原始 `request_type` 过滤工具。
- 不修改 `switch_to_agent` 工具的定义和触发条件。
- 不增加新的 API 接口。动态注入在 `tool_calls()` 回调处理中完成，不需要客户端额外请求。
