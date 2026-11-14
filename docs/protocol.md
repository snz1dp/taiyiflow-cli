# 传输协议参考

本页描述 CLI 与太乙智启后台之间的传输协议，真值来源为后台 `taiyiflow/api/v1/endpoints.py`、`schemas.py` 与 CLI `internal/provider/taiyiflow/` 实现。协议细节以后端代码为准，本页仅记录 CLI 实际使用的部分。

## 基础约定

- 默认后端：`https://snz1.cn`（`backend_base_url`）
- API 前缀：`/taiyi/composer/api/v1`（`backend_api_prefix`）
- 认证前缀：`/xeai`（`auth_api_prefix`）
- 认证方式：请求头携带访问令牌；令牌过期时用 `persistent_token` 自动续期
- 会话响应头：`X-TaiyiFlow-Session-id`（SSE 链路返回，CLI 据此绑定会话）

## 流式运行

### SSE 链路（默认）

```text
POST /run/{flow_id_or_name}/stream?session_id=<可选>
Content-Type: application/json
Accept: text/event-stream
```

- 响应为 SSE 事件流，逐条推送事件；
- 响应头 `X-TaiyiFlow-Session-id` 携带会话 ID（请求未带会话时由服务端分配）；
- 流空闲超时由 `stream_idle_timeout_seconds` 控制，超时触发重试或报错。

### WebSocket 链路

```text
WS /run/{flow_id_or_name}/websocket
```

协议流程：

1. 建立 WebSocket 连接（携带认证头）；
2. 连接成功后发送与 SSE 相同的请求体 JSON 作为第一条消息；
3. 服务端逐条推送 `{"event": "...", "data": {...}}` 格式的 JSON 消息；
4. 服务端推送 `event="close"` 后主动关闭连接。

与 SSE 的差异：WebSocket 握手响应**不携带** `X-TaiyiFlow-Session-id`，因此请求未带会话 ID 时，CLI 先预创建会话 ID 再写入请求体。

协议选择由 `stream_protocol` 配置控制：`auto`（默认，按服务端能力协商）/ `sse` / `websocket`。

### 请求体字段

SSE 与 WebSocket 共用同一请求体（SimplifiedAPIRequest）：

| 字段 | 说明 |
| --- | --- |
| `input_value` | 用户输入文本 |
| `input_type` | 输入类型，默认 `any` |
| `files` | 上传文件标识列表 |
| `session_id` / `run_id` / `message_id` | 会话、运行、消息标识 |
| `request_type` | 请求模式：`agent` / `ask` / `plan` |
| `client_type` | 客户端类型，CLI 固定 `cli` |
| `client_id` / `device_id` | 客户端与设备标识 |
| `client_context_dir` | 本地工作目录 |
| `command_execution_target` | 命令执行目标，默认 `client` |
| `internet_search` | 联网搜索开关 |
| `knowledge_scopes` / `knowledge_docs` | 知识库范围与文档 |
| `reasoning` | 思考模式开关 |
| `image_generation_enabed` | 图像生成开关（字段名对齐后端原拼写） |
| `requested_skill_names` | 显式请求的技能名 |
| `client_skill_packages` | 本地技能包（元数据 + 正文 + markdown） |
| `client_mcp_servers` | 本地 MCP 服务器供应 |
| `client_instructions` | 客户端指令上下文 |
| `available_tools` | 本地可用工具定义 |
| `agent_auto_loop_enabled` / `agent_auto_loop_max_rounds` | 自动续跑开关与轮次上限 |
| `cross_session_memory_enabled` | 跨会话记忆开关 |
| `max_iterations` / `agent_continue_windows` | 迭代与续跑窗口 |
| `execution_mode` | 执行模式：`start` / `resume` / `attach` |
| `execution_attempt_id` / `checkpoint_id` / `checkpoint_version` | 恢复执行相关标识 |
| `after_event_id` / `subscription_id` | 事件续接与订阅标识 |
| `master_flow_id` / `parent_id` | 主智能体与父任务标识（子任务场景） |
| `model` | 指定模型 |
| `tweaks` / `agent_inputs` / `output_type` / `output_component` | 其他扩展字段 |

## 流式事件类型

| 事件类型 | 说明 |
| --- | --- |
| `message` | 文本消息分片（智能体回答） |
| `error` | 错误事件 |
| `close` | 流结束 |
| `compression` / `compression_start` / `compression_end` | 上下文压缩进度 |
| `bytes` | 二进制内容分片 |
| `custom` | 自定义事件 |
| `tool_call` | 工具调用（通用） |
| `built_in` | 内置工具调用 |
| `client_tool` | 客户端本地工具调用（CLI 执行并回传结果） |
| `mcp_tool` | MCP 工具调用 |

每个事件携带 `event_id`（或数据内 `event_id` 字段），用于断点续接（`after_event_id`）。

## 工具结果回传

本地工具（`client_tool` / `mcp_tool`）执行完成后回传：

```text
POST /agents/client/call/result
```

请求体：

| 字段 | 说明 |
| --- | --- |
| `flow_id` | 智能体 ID |
| `session_id` | 会话 ID |
| `run_id` | 运行 ID |
| `call_id` | 工具调用 ID |
| `message_id` | 消息 ID（可选） |
| `success` | 是否成功 |
| `result` | 成功时的结果内容 |
| `error_message` | 失败时的错误信息 |

## 执行控制

```text
POST /run/sessions/{session_id}/pause     暂停
POST /run/sessions/{session_id}/cancel    停止
GET  /run/checkpoints/resumable           发现可恢复检查点
GET  /sessions/{session_id}               会话执行状态
```

详见[执行控制与恢复](/execution-control)。

## 技能接口

```text
POST /agents/flows/{flow}/sessions/{session}/skills/resolve   技能解析预览
GET  /agents/skills                                           服务端技能列表/搜索
GET  /agents/skills/{id}?format=raw                           技能详情
POST /agents/skills/upload                                    上传技能
DELETE /agents/skills/{id}                                    删除技能
GET  /agents/skills/installations                             已安装技能
POST /agents/skills/installations/{id}/uninstall              卸载
```

技能解析预览返回 `skills`（已解析技能）、`missing_skill_names`（缺失技能，触发自动补装）、`allowed_tools`（允许的工具）。

## 其他接口

```text
GET  /run/{flow}/metadata          智能体元信息（名称、欢迎语）
POST /run/{flow}                   非流式运行
GET  /version                      后端版本信息
GET  /config                       后端配置
```

上下文压缩轮询：

```text
/agents/flows/{flow}/sessions/{session}/run/{run}/compressions
```

## 认证与续期

- 登录接口位于认证前缀 `/xeai` 下（密码登录、扫码登录、验证码等）；
- 登录成功写入 `access_token`、`persistent_token`、`device_id`、`auth_updated_at`；
- 请求返回认证错误时，CLI 优先用 `persistent_token` 续期并重放原始请求；续期失败才提示重新登录；
- `get_sso_token` 工具返回的令牌仅用于认证请求（下载 URL 的 `__TOKEN__` 参数或 `Authorization: Bearer` 头），不得出现在生成内容中。