# Plan: CLI AgentLoop 控制、断连恢复与远程观察协议升级

- 状态：代码实施与自动验证已完成；待用户按第 8.2 节完成真实环境联调验收
- 日期：2026-11-14
- 权威计划：本文件是本轮升级的唯一实施计划；后续进展、范围调整和验收结果只回本文件更新，不另建同事项计划。
- 当前项目：`/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli`
- 桌面参照：`../taiyiflow-electron`，功能分支 `feature/agent-loop-collab`，核查终点 `57bb29e9`；首页拆分分支不属于本轮协议基线。
- 后台真值：`../taiyiflow-dev`，分支 `feature/extplugins`，HEAD `124913e967`。
- CLI 基线：分支 `feature/extplugins`，HEAD `5f4582f`。

## 1. 核查结论

本轮不是给 provider 增加两个 HTTP 方法即可完成的小改动。CLI 当前没有新协议的执行模式、检查点、事件游标、恢复通知、用户控制态和持久化补偿，需要把普通命令行、TUI、子任务及协同 worker 接入同一套执行语义。

实施前已实证的现状：

1. `internal/provider/provider.go` 的 `StreamRequest` 没有 `execution_mode`、`execution_attempt_id`、`checkpoint_id`、`checkpoint_version`、`after_event_id`、`subscription_id` 和 `model`；`StreamEvent` 没有独立事件 ID；`SessionSummary` 没有远程执行元数据。
2. `internal/provider/taiyiflow/client.go` 的 SSE 与 WebSocket 已共用 `buildStreamRequestBody()`，适合统一补请求字段；但 SSE 解析丢弃 `id:`，WebSocket 与 SSE 尚没有统一的 AgentLoop 事件语义层。
3. 全 CLI Go 代码没有 `network_freezing`、`resumable`、`resume_requested`、`checkpoint_state`、`agent_loop_cancel_requested` 等新协议处理。
4. `cmd/root.go`、`cmd/tui.go`、`internal/runtime/subtask_tools.go` 分别直接调用 `Provider.Stream()`。只升级其中一个入口会造成主任务、TUI 和子任务行为分叉。
5. TUI 当前 Ctrl+C 只取消本地流；普通流失败重试仍可能按原输入重新发起执行。新协议下，用户停止必须补发服务端 cancel，已受理执行的重连不得再次以 `start` 重放用户输入。
6. `internal/agent/session.go` 只持久化基础会话和 run/message lineage，没有执行尝试、检查点、恢复意图、事件游标或待补报工具结果。
7. `internal/runtime/client_runtime.go` 的回调补偿只有进程内有限重试，最终失败即返回；没有跨断流、跨进程重启的持久化队列。
8. `internal/runtime/collaboration_worker.go` 尚未声明 `agent_loop.resume.v1`，也没有处理 `agent_loop.resume_requested`、`agent_loop.resume_notification_ack` 和 `agent_loop.checkpoint_state`。
9. 后台只允许 `collaboration_worker` 角色接收和确认恢复通知，`primary_cli` 不应另建一套通知消费者。primary 与 worker 必须通过活跃执行标记、恢复锁和后台 CAS 避免重复恢复。
10. CLI 已有 `task_stop`、协同任务取消和 Windows 进程树终止，不应误报为“完全缺失”。但非 Windows 原始命令、沙盒包装命令及后台重试路径尚未统一保证进程树终止。
11. CLI 的 Shell 高危检查覆盖凭证、编码绕过、反向连接、PID1/fork bomb 等桌面方案未完整覆盖的场景；桌面端 renderer schedule 直执旁路和 `skill_create/upload/delete` 历史问题不能直接套用到 CLI。
12. 当前 CLI 没有自定义模型选择配置或 `--model` 请求来源，不能为了对齐桌面端虚构模型 ID。provider 可预留可选 `model` 字段，但当前 start/resume 都应继续省略；后台 `124913e967` 的实际修复是恢复流拆分消息时继承原消息 source 中的模型标识。

## 2. 桌面计划与后台实现的差异处理

以后台当前代码为协议和状态机的最终验收依据，桌面端用于参考客户端交互和恢复编排。

### 2.1 pre-loop pause 不能照搬旧桌面计划

桌面文档 `agent-loop-preloop-pause-boundary-plan.md` 曾要求 `executing=true, run_status=running` 时 pause 返回 409。后台随后由提交 `6a17800200` 明确把 `running` 重新加入 `_PAUSABLE_EXECUTION_STATUSES`，当前 `control.py` 为：

```text
running / agent_loop_running / pause_requested / network_freezing
```

因此本轮 CLI 必须遵守以下规则：

- 不把“pre-loop pause 必须 409”写成验收条件。
- 客户端可以把“已受理”和“已形成暂停检查点”分开显示，但最终是否允许暂停由后台响应决定。
- `cancel_requested` 后 pause 仍应失败；cancel 优先于 pause。
- 联调时记录后台实际版本；若后台状态矩阵后续再变，先更新本计划和协议测试，再改 CLI。

### 2.2 恢复 ACK 的 `result` 目前只是桌面附加字段

桌面端会发送 `accepted`、`ignored_expired`、`ignored_stale`、`rejected_invalid_payload`、`ignored_stopped`、`already_running` 等 `result`。后台当前 `protocol.py` 只强制要求：

```text
notification_id / checkpoint_id / checkpoint_version
```

`service.py::_handle_resume_notification_ack()` 也只按通知 ID、检查点版本、来源终端和持久化状态判定，不读取 `result`。CLI 可以为协议兼容保留可选 `result`，但不能把结果字符串当成服务端 ACK 成功的证据，更不能等待服务端回送同样的结果枚举。

## 3. 已确认的协议契约

### 3.1 流式执行

统一使用现有流入口：

```text
POST /run/{flow_id}/stream
WS   /run/{flow_id}/websocket
```

`SimplifiedAPIRequest` 新字段：

```text
execution_mode: start | attach | resume
execution_attempt_id
checkpoint_id
checkpoint_version >= 1
after_event_id
subscription_id
model（可选）
```

角色约束：

- `start`：发起新执行；仅首次发起使用。
- `resume`：原发起端从指定检查点恢复，必须携带 `run_id + checkpoint_id + checkpoint_version`。
- `attach`：只读观察已有执行，可携带 `run_id + execution_attempt_id + after_event_id + subscription_id`；不是原发起端恢复手段。
- 普通 `ask/plan` 保持旧行为；CLI 新协议先限定在 `request_type=agent` 的本地客户端执行链路，不因这次升级改变普通问答的断连语义。

后台只有 `start/resume` 在异常断连时进入网络冻结；`attach` 保留生产 Loop 但不触发原端网络冻结。缺省 `execution_mode` 仍是老客户端行为，所以 Agent 请求必须显式传 `start`，否则无法获得本轮能力。

### 3.2 控制与恢复发现

```text
POST /run/sessions/{session_id}/pause
POST /run/sessions/{session_id}/cancel
GET  /run/checkpoints/resumable?client_id=...&device_id=...&flow_id=...&session_id=...
```

控制请求：

```json
{
  "run_id": "可选但已知时应携带",
  "request_id": "每次逻辑控制操作生成并复用",
  "client_id": "原发起客户端 ID",
  "device_id": "设备 ID"
}
```

控制响应至少包含：

```text
session_id / run_id / status / request_id / checkpoint_id
subtask_cancel_requested / subtask_cancel_pending / subtask_cancel_failed
```

约束：

- HTTP 202 只表示受理，不表示暂停检查点已生成，也不表示所有本地和远端副作用已结束。
- 403、409 和状态变化不得被显示成“已暂停”或“已停止”。
- 暂停发现只查询，不自动把用户暂停变成恢复。
- 自动恢复只接受 `pause_reason=network` 且 `requires_user_resume=false` 的检查点。
- `checkpoint_id + checkpoint_version` 是恢复互斥键，`notification_id` 是通知去重键。
- 已过期、旧版本、已停止、当前仍运行或来源不匹配的通知不得启动恢复。

### 3.3 协同通知

WebSocket：`/agents/client/websocket`，信封沿用：

```json
{"type":"collaboration","event":"<event>","payload":{}}
```

worker 连接查询参数在现有能力集合上追加：

```text
agent_loop.resume.v1
```

事件：

```text
agent_loop.resume_requested          server -> worker
agent_loop.resume_notification_ack   worker -> server
agent_loop.checkpoint_state          server -> worker
```

`resume_requested` 至少携带通知 ID、检查点 ID/版本、session/flow/run、恢复阶段和过期时间；`checkpoint_state` 额外携带状态、暂停原因和是否需要用户恢复。

### 3.4 attach 事件

```text
attach-flow / run_snapshot / resync_required / heartbeat / close
agent_loop_checkpoint_created / 其他普通活动事件
```

游标优先级：`data.event_id`、SSE `id:`、`run_snapshot.current_event_id`。`resync_required` 必须清游标并全量刷新；`close` 是明确终态。observer 收到任何客户端工具执行事件都应视为协议异常并拒绝执行，不能复用原端工具回调路径。

## 4. 目标与非目标

### 4.1 本期目标

- P0：完成 Agent `start/resume`、pause/cancel、网络冻结恢复、工具结果持久化补偿和 worker 恢复通知闭环。
- P1：完成 attach 只读观察、远程会话执行元数据、事件游标续接与轮询降级。
- P1：统一主任务、TUI、子任务、worker 的执行语义，并补齐停止时的本地进程树收尾。
- P2：有证据地选择性对齐本地审批与产物分类差异，不回退 CLI 现有安全能力。

### 4.2 明确不做

- 不移植 Electron 首页 Index 拆分、ASR/TTS、SVG/生信渲染、媒体键、浏览器视图等桌面专属功能。
- 不新增模型选择 UI；不从 token usage 的返回模型名反推下一次请求模型。
- 不把 attach 当作 resume，不在恢复失败后自动改用 `start` 重放用户输入。
- 不用一个 `paused` 布尔值同时表示用户暂停和网络冻结。
- 不把桌面的 pre-loop 409 旧矩阵或 ACK `result` 枚举当成后台验收真值。
- 不默认引入桌面 CPU 感知停顿。原桌面计划已明确 CLI 没有“给桌面交互让 CPU”的同一场景；如未来有服务端资源保护需求，另立独立评估。
- 不为兼容旧后台而在新 `start` 失败后静默退回 legacy 重试，避免重复执行。
- 不切换其他仓库分支，不修改桌面端或后台仓库，不提交或推送；本轮业务代码变更仅限当前 CLI 项目。

## 5. 总体实施结构

### 5.1 provider 只负责协议，不承载恢复策略

修改：

- `internal/provider/provider.go`
- `internal/provider/taiyiflow/client.go`
- `cmd/local_capability.go`
- 相关 provider stub 和测试

新增协议类型：

```text
AgentLoopControlRequest / AgentLoopControlResponse
AgentLoopCheckpoint
AgentLoopCheckpointQuery
SessionExecutionMetadata
```

扩展 `StreamRequest` 的可选执行字段和 `StreamEvent.ID`；为流错误增加可机器判断的类型/协议 payload，避免通过匹配中文错误字符串识别 `retry_after`、冲突或 `tool_call_paused`。

Provider 接口增加：

```text
GetSessionExecution(session_id)
PauseAgentLoop(session_id, request)
CancelAgentLoop(session_id, request)
ListResumableAgentLoopCheckpoints(query)
```

`callbackAuthRefreshProvider` 必须同步转发这些方法并保留现有认证刷新语义。所有 HTTP 方法覆盖路径转义、查询参数、202/403/404/409 和响应解码。

SSE 读取 `id:`，WebSocket 从统一 payload 读取 `event_id`；二者输出相同的 `StreamEvent`，上层不得只支持一种传输。

### 5.2 新增共享 AgentLoop 协调层

建议新增 `internal/agentloop` 小包，只依赖 provider、配置和持久化工具，不 import `internal/runtime`，通过回调接口注入本地工具执行和消息刷新，避免 import cycle。

职责限定为：

- 执行身份与状态机：start、active、pause_requested、cancel_requested、network_freezing、waiting_checkpoint、resuming、terminal。
- 原端恢复协调：发现检查点、恢复锁、退避、ready 判定、用户控制失效处理。
- 持久化：恢复意图、通知去重、observer 游标、待补报工具结果。
- attach 观察编排。
- 不直接渲染 TUI，不直接执行 Shell，不复制 provider HTTP 代码。

建议文件：

```text
internal/agentloop/controller.go
internal/agentloop/store.go
internal/agentloop/recovery.go
internal/agentloop/observer.go
internal/agentloop/*_test.go
```

若实施时发现小包边界与现有依赖不符，可以合并文件，但不能把完整状态机分别复制到 `root.go`、`tui.go` 和 worker。

### 5.3 持久化布局与并发

在 `internal/config.DefaultRootDir()` 下增加独立 AgentLoop 状态目录，不把高频游标和工具结果塞进现有会话标题/配置文件。

至少保存：

```text
RecoveryIntent:
  flow_id / session_id / run_id
  checkpoint_id / checkpoint_version
  status / pause_reason / requires_user_resume
  awaiting_resume / invalidated_by_user
  model（仅原请求确实有值时）
  created_at / updated_at

PendingToolCallback:
  ClientCallResult 全部字段
  created_at / attempts / next_retry_at / last_error

ObserverCursor:
  session_id / run_id / execution_attempt_id
  subscription_id / after_event_id
```

要求：

- 临时文件写入、fsync/close 后原子 rename；状态文件权限 0600。
- 工具结果可能含敏感内容，不写调试日志，不进入普通会话消息文件。
- 保留工具结果原始 `created_at`，重试不能延长其生命周期。
- 同一进程使用 keyed mutex；跨 primary/worker 进程使用基于 `checkpoint_id + checkpoint_version` 的原子 claim 文件和过期租约。后台 CAS 是最后防线，但不能代替本地去重。
- 用户 pause/cancel 先持久化控制意图，再发 HTTP；worker 看到该 run 的用户停止意图后不得自动 resume。
- 正常终态、明确取消、无效过期检查点按规则清理；暂时性写盘失败必须阻止自动恢复并显式报错，不能声称恢复状态已可靠保存。

## 6. 分阶段实施步骤

### 阶段 A：协议模型与事件解析（P0，先行）

1. 扩展 provider 请求/响应、session execution metadata 和控制接口。
2. 在 `buildStreamRequestBody()` 统一序列化 execution/checkpoint/cursor/subscription/model 字段，所有可选零值保持 `omitempty`。
3. 为 SSE 增加事件 ID，为 WS 补齐同一字段；统一解析 `start-flow`、`resume-flow`、checkpoint、network freeze、pause/cancel、resync、close 等消息中的 `name/run_id/execution_attempt_id/event_id/retry_after`。
4. 保留原有普通消息事件映射，避免协议升级改变 ask/plan、token usage、title 和客户端工具现有处理。
5. 完成 provider 定向测试后再进入状态机实施。

完成标准：SSE 与 WS 对同一逻辑事件生成等价的上层事件；start/resume/attach 请求快照与后台 schema 一致。

### 阶段 B：共享执行状态机与主入口接入（P0）

1. `cmd/root.go`、`cmd/tui.go` 的 Agent 请求首次调用显式 `execution_mode=start`，在发起前持久化 session/run 基本身份。
2. 从 `start-flow`/`resume-flow` 首事件及后续权威事件更新 `run_id`、`execution_attempt_id`，不再只依赖客户端预生成 run ID。
3. `internal/runtime/subtask_tools.go` 的 Agent 子任务走同一 start 语义；保留 `parent_id`、child session/run 和协同回报，不把父会话恢复状态复制给子任务。
4. 主命令、TUI 和子任务共用事件分类与错误判定，展示层继续各自处理文本、工具和统计。
5. 改造现有流失败重试：
   - 未发起的新请求才允许 `start`。
   - 已受理执行发生断流时，保留恢复意图并进入检查点发现/恢复流程。
   - attach 只用于观察，不用于替代原端 resume。
   - 连接在首事件前失败且无法确认后台是否受理时，先查会话执行状态和检查点；有界对账后仍无证据则报告“提交结果不确定”，由用户显式重试，禁止自动再次 start。
6. 保留现有 `/retry` 作为“用户明确发起新执行”的入口，生成新 run ID，不与自动恢复混用。

完成标准：任何自动重连路径都不会以同一用户输入重复 `start`；主任务和子任务不会出现一套支持恢复、一套仍走旧断连清理的分叉。

### 阶段 C：pause/cancel 与本地停止语义（P0）

1. 实现 pause/cancel 控制方法及 `request_id` 幂等复用。
2. TUI 增加明确的暂停、恢复入口；Ctrl+C 对 AgentLoop 改为“用户停止”：
   - 先记录停止意图并阻止新客户端工具派发。
   - 使用带超时的 detached context 调用服务端 cancel。
   - 再关闭本地流，确保不是仅断流让后台进入网络冻结。
   - 202 后显示“停止已受理，等待结束”，直到权威终止事件或会话状态确认。
3. 非交互 root 的取消路径复用同一控制逻辑；信号处理不能在 context 已取消后直接放弃 HTTP cancel。
4. pause 采用“请求中/已受理/已生成检查点”分态；409/403 只结束本次 pending，不伪造成功。
5. cancel 优先于 pause；旧 run 的响应和事件不得清理新 run 的状态。
6. 暂停或停止后不再执行新的本地工具。网络断流不等于用户停止：已在执行的工具可完成并持久化结果，但不接新调用。
7. 本地停止意图与 worker 共享，防止 cancel 请求暂时失败时 worker 又把该 run 自动恢复。

完成标准：本地 Ctrl+C 不会把用户停止误变成网络自动恢复；202、checkpoint 和最终终止在 UI/日志中可区分。

### 阶段 D：工具结果持久化补偿与网络恢复（P0）

1. 改造 `ClientRuntime.SubmitClientToolCallback()`：结果形成后先入持久队列，再执行首次 HTTP 上报；成功后删除，临时失败保留并入队。
2. 保留原 `created_at`、递增 attempts，并使用可注入时钟和退避；进程启动、worker 重连及网络恢复时均可继续补报。
3. 后台明确表示 `tool_call_paused` 或等价“迟到结果不再接受”时删除待补报项并记录审计，不再无限重试；其他 4xx 配置/权限错误与临时网络错误分开处理。
4. 网络恢复顺序固定为：
   - 停止 observer 和普通远程轮询对会话状态的竞争写入。
   - 补报该 run 的持久化工具结果。
   - 查询并校验可恢复检查点。
   - 以原 `run_id + checkpoint_id + checkpoint_version` 发起 `execution_mode=resume`。
   - 收到有效 `resume-flow` 或其他明确 ready/流事件后才清理 awaiting 状态。
5. 再次收到 `network_freezing` 时保留恢复意图，优先采用服务端 `retry_after`；没有服务端提示时采用有界指数退避和抖动，不使用固定 3 秒死循环。
6. 只有 `pause_reason=network && requires_user_resume=false` 可自动恢复；用户暂停只能由显式 `/resume` 或非交互恢复命令触发。
7. 恢复时沿用原请求的必要能力快照；`model` 只有原请求确有值才回传，不从响应 usage 猜测。

完成标准：断流、进程重启和 worker 重连后，工具结果先对账、检查点后恢复；重复断网不会丢恢复意图，用户暂停永不自动恢复。

### 阶段 E：worker 通知、去重与归属（P0）

1. `buildCollaborationWorkerWebSocketURL()` 在保留现有 task 能力的同时追加 `agent_loop.resume.v1`。
2. 在 worker 事件循环处理 `resume_requested` 和 `checkpoint_state`，网络/HTTP 操作放到有界 goroutine，不阻塞 WebSocket 读循环。
3. 按 notification ID 去重，按检查点 ID/版本建立恢复 claim；处理过期、旧版、字段不全、来源不符、已停止、已运行和重复通知。
4. worker 是自动恢复通知的唯一消费者；primary 只维护活跃执行和用户控制状态，不订阅同一通知。
5. 恢复流 ready 后发送 ACK；ACK 必需字段只有通知 ID、检查点 ID/版本，可选 `result` 只作桌面兼容，不据此判断成败。
6. `connection.ready` 后除现有最近任务对账外，触发有界 resumable checkpoint 主动发现，作为通知丢失补偿。
7. `agent_loop.checkpoint_state` 只更新权威状态，不直接把 `ready` 当成恢复许可；仍需校验暂停原因和用户控制意图。
8. 限制并发恢复数并复用 worker 当前任务并发保护，避免重连时同时抢占大量检查点。

完成标准：同一检查点在通知、重连对账和主动发现同时到达时最多一个有效恢复流；primary 活跃时 worker 不重复接管。

### 阶段 F：attach 只读观察与轮询降级（P1）

1. provider 增加 `GET /sessions/{session_id}` 的执行元数据映射，至少覆盖：
   `executing/executing_run_id/execution_attempt_id/run_status/origin_client_id/origin_device_id/current_event_id/current_event_seq`。
2. observer 使用独立 client ID（原 ID 有界截断后追加 `:observer`），生成并复用 subscription ID。
3. 以 `execution_mode=attach` 接入，按事件 ID 推进游标；断线后使用 `after_event_id` 重连。
4. `run_snapshot` 和 `resync_required` 通过会话消息全量刷新校正展示；resync 时清游标，不去猜测缺失事件。
5. observer 不执行客户端工具、不提交工具结果、不自动 resume、不改变原端恢复意图。
6. clean close、连续失败、稳定连接时长和轮询降级分别计数；退避、降级阈值写成可测试常量，不照搬桌面魔法数而不测。
7. 用户显式 resume/pause/cancel 或原端网络恢复开始时暂停 observer，避免 observer 与恢复流同时成为会话状态写入源。
8. TUI 提供 attach/detach 和远程状态展示；非交互模式提供只读 attach 入口。命令帮助必须明确 attach 不会恢复执行。

完成标准：跨进程观察能续接、重同步和终止，且全程没有本地工具副作用。

### 阶段 G：本地 Runtime 停止与选择性安全对齐（P1/P2）

P1 必做：

1. 保留现有 `task_stop` 和 Windows `taskkill /F /T` 能力。
2. 为非 Windows 原始命令、沙盒命令、后台任务和 sandbox retry 统一配置独立进程组及进程组取消；不得只杀 `sh`、`sandbox-exec` 或 `bwrap` 的直接子进程。
3. Windows 沙盒包装路径也复核进程树终止，不假设 `buildRawCommand()` 的修复已覆盖所有构造器。
4. 父任务 pause/cancel 时，本地子任务 context、协同任务取消和后台进程停止按来源 run 关联；在途副作用不可撤回要如实记录。
5. 增加单项任务停止、重复停止、已退出停止和恢复后未附着任务的测试，保持现有“未附着不能停止”的明确错误。

P2 先补差距测试再决定改法：

1. 权限决策未知 action/decision fail-closed，用户确认后和执行前重新评估当前策略。
2. 评估规则聚合 `deny > ask > allow`，同时保留工具级显式 allow 的既有语义，不能只按数组首个命中偶然短路。
3. Shell 候选分析可增强 Bash/Sh/Zsh、CMD、PowerShell、静态 env 和复合命令识别，但必须保留 CLI 已有更广的高危检查，不能换成桌面子集。
4. Shell 文件变化事件与用户产物分类可复用统一分类器，但不得把普通源码变化全部当成用户下载产物。
5. CPU 自适应停顿不进入默认范围；确有 CLI 资源保护需求时另行立项。

## 7. 兼容、错误与回滚策略

1. `ask/plan` 继续省略 `execution_mode`；Agent 新协议不支持时明确报告后台版本不兼容，不静默改变为 legacy 自动重试。
2. pause/cancel/resumable 的 404 表示能力不可用；409 表示当前状态冲突；二者都不能触发自动 start 或自动 attach。
3. resume 失败只能按后台错误类型决定等待、重新发现或要求用户处理，禁止降级为新执行。
4. 所有控制请求使用原发起身份；observer 身份不得用于 pause。
5. 旧持久化文件缺失或为零值时按无恢复状态处理；新文件版本号缺失或高于当前支持版本时 fail-closed，不自动执行工具或恢复。
6. 回滚 CLI 前先用新版明确完成或 cancel 活跃 AgentLoop。旧版不会声明恢复能力，也不会读取新恢复状态；直接回滚不会自动清理后台冻结检查点。
7. 不新增功能开关来掩盖半套协议实现；按阶段完整提交，P0 未闭环前不发布为可恢复能力。

## 8. 测试计划

### 8.1 自动测试（实施代理执行）

Provider：

- start/resume/attach 请求字段、零值省略、checkpoint version 边界、model 原样透传。
- SSE `id:`、多行 data、WS event_id 和两种传输的事件等价性。
- pause/cancel/resumable/session execution 的路径、query、202/403/404/409 和响应字段。
- 结构化流错误中的 retry_after、冲突和迟到工具结果语义。

协调层：

- 用户暂停与网络冻结分离；用户停止后通知不能自动恢复。
- 通知 ID 去重、检查点版本锁、过期/旧版/已停止/已运行/字段不全。
- primary 与 worker 竞争、通知与主动发现竞争、claim 过期回收。
- 网络恢复先补工具结果再 resume，ready 前清状态失败可重试，二次 network freeze 保留意图。
- 工具结果创建时间不变、重试次数增加、tool_call_paused 删除、进程重启后补报。
- 首事件前连接失败不自动重复 start；已受理断流不重复 start；显式 `/retry` 才生成新 run。
- attach 游标续接、resync 清游标、close 终态、observer 工具事件 fail-closed。
- 持久化原子写、损坏文件、权限、版本不兼容和敏感结果不落日志。

Runtime：

- 主任务、TUI、子任务的 execution_mode 和权威 run lineage。
- Ctrl+C 发送服务端 cancel，202 不立即伪装终态，旧 run 响应不污染新 run。
- 各平台进程组/进程树取消、后台任务和 sandbox retry 使用同一取消语义。
- 现有审批、高危命令和任务管理测试不得回退。

建议命令：

```bash
go test ./internal/provider/taiyiflow ./internal/agentloop ./internal/runtime ./cmd ./internal/tui
go test -race ./internal/agentloop ./internal/runtime
go test ./...
go vet ./...
```

### 8.2 真实环境联调（用户在现有环境自测）

不额外搭建复杂 UI 或多端测试环境。实施完成后由用户按以下矩阵验收：

| 场景 | 操作 | 预期 |
|---|---|---|
| Agent 新执行 | SSE、WS 各发起一次 | 请求含 `start`，首事件保存权威 run/attempt |
| 普通 ask/plan | 发起并断流 | 保持旧语义，不误进 AgentLoop 恢复 |
| 启动期暂停 | 在首个 Loop 状态前后分别 pause | 以后台当前响应为准，不错误断言旧桌面 pre-loop 409 |
| 用户暂停 | 长工具或长输出中 pause | 202 只显示受理；checkpoint 后才显示已暂停；不自动恢复 |
| 显式恢复 | 对用户暂停执行 resume | 使用相同 run、checkpoint ID/version，继续原会话 |
| 用户停止 | Ctrl+C 或 stop | 服务端收到 cancel；不是单纯断流；worker 不自动恢复 |
| 断网恢复 | 原端断网，等待 checkpoint 后恢复网络 | 先补工具结果再 resume，不重复用户输入 |
| 连续断网 | resume 中再次断网 | 保留恢复意图，遵守 retry_after，不创建新 run |
| 工具迟到结果 | 暂停/停止后补报 | 明确拒绝后不再重试，不形成死循环 |
| 进程重启 | 工具结果待补报时退出并重开 | 队列仍在，原 created_at 不变 |
| 通知竞争 | worker 通知、主动发现、primary 活跃同时出现 | 同一 checkpoint 只有一个有效恢复流 |
| attach 观察 | 另一终端观察运行 | 不执行工具；可续接；resync 后全量校正；close 后结束 |
| 子任务停止 | 父任务 pause/cancel，含本地及远程子任务 | 协同状态与本地进程收尾一致，无孤儿进程 |
| 模型保持 | 后台自定义模型场景恢复 | 不新增 CLI 模型猜测；消息模型标识与后台修复一致 |
| 旧后台 | 新 CLI 调新控制能力 | 明确不支持，不静默 start 重放或自动降级 resume |

## 9. 风险与控制点

| 风险 | 控制 |
|---|---|
| 自动重试重复执行用户任务 | start 仅首次使用；提交不确定时有界对账后交用户决定 |
| 用户停止被后台当成网络断连 | 先持久化停止意图并补发 cancel，再关闭本地流 |
| 用户暂停被 worker 自动恢复 | 同时校验 pause_reason、requires_user_resume 和本地用户控制意图 |
| 多入口各自实现导致行为漂移 | 共享 `internal/agentloop`，provider SSE/WS 统一事件模型 |
| primary 与 worker 重复恢复 | worker 独占通知、活跃标记、跨进程 claim、后台 CAS 四层约束 |
| 工具结果永久重试或丢失 | 持久队列、原创建时间、分类错误、迟到拒绝即删除 |
| observer 产生副作用 | 独立身份、只读事件流、工具事件 fail-closed |
| 旧桌面文档与后台不一致 | 后台 `../taiyiflow-dev` 当前代码和契约为准，差异记录在本文件第 2 节 |
| 安全对齐造成能力回退 | 先写差距测试，保留 CLI 现有高危检查，不整套替换桌面规则 |
| 敏感工具结果泄露 | 0600、原子写、不记录结果正文日志、按终态清理 |

## 10. 阶段验收与实施顺序

严格按以下依赖实施：

```text
A 协议模型
  -> B start/身份/主入口
  -> C 用户控制 + D 回调补偿/网络恢复
  -> E worker 通知闭环
  -> F attach 观察
  -> G Runtime 停止与选择性安全对齐
```

- P0 完成定义：A–E 自动测试通过，用户完成核心断网、暂停、停止、工具补报和通知竞争联调。
- P1 完成定义：attach 无工具副作用且可续接/重同步，本地任务与进程树停止通过目标平台验证。
- P2 完成定义：仅合入有回归测试证明的本地差距，不包含默认 CPU 限流。
- 每阶段完成后更新本文件的状态和实测结果；若协议字段或后台状态机变化，先记录证据并修订本文件，再实施代码。

## 11. 实施结果与验收记录（2026-11-14）

### 11.1 阶段完成情况

| 阶段 | 代码实施 | 主要落地结果 |
|---|---|---|
| A 协议模型与事件解析 | 已完成 | start/resume/attach 请求字段、执行/检查点元数据、控制 API、SSE `id:` 与 WS `event_id` 统一解析；普通 ask/plan 保持旧语义 |
| B 共享状态机与主入口 | 已完成 | 新增 `internal/agentloop`；root、TUI、子任务和 worker 复用执行/恢复协调；自动重连不再重复 start，显式 retry 才创建新 run |
| C 用户控制与本地停止 | 已完成 | pause/cancel 持久化意图、稳定 request ID、cancel 优先和 detached cancel；忙碌时 `/pause`、`/cancel`、`/resume`、`/detach` 不排队，`/cancel` 同时取消本地流 |
| D 工具补偿与网络恢复 | 已完成 | 工具结果先落 0600 持久队列再上报，保留 created_at、attempts 和退避；迟到拒绝删除；恢复前先补报并校验网络检查点 |
| E worker 恢复闭环 | 已完成 | 声明 `agent_loop.resume.v1`，通知去重、ready ACK、检查点状态、主动发现、primary 避让、跨进程 claim 和共享并发限制 |
| F attach 只读观察 | 已完成 | 独立 observer 身份与游标、快照/续接/resync/close、工具事件 fail-closed、退避与轮询降级；提供非交互 attach 和 TUI `/attach`、`/detach` |
| G Runtime 停止与安全 | 已完成 | Unix 进程组与 Windows 进程树取消覆盖原始命令、沙盒包装和后台重试；未知权限动作 fail-closed，执行前重新评估策略，保留既有高危检查 |

### 11.2 已执行的自动验证

以下命令均已在当前工作树实际执行并通过：

```bash
/usr/local/go/bin/go test ./internal/provider/taiyiflow ./internal/agentloop ./internal/runtime ./cmd ./internal/tui -count=1 -timeout=240s
/usr/local/go/bin/go test -race ./internal/agentloop ./internal/runtime -count=1 -timeout=300s
/usr/local/go/bin/go test -race ./internal/tui -count=1 -timeout=240s
GOOS=windows /usr/local/go/bin/go test -c -o /dev/null ./internal/runtime
/usr/local/go/bin/go test ./... -count=1 -timeout=300s
/usr/local/go/bin/go vet ./...
git diff --check
```

补充验证结果：

- Unix 进程组取消测试确认直接进程及其后台子进程均被终止；Windows Runtime 测试包交叉编译通过。
- Runtime 后台任务测试等待最终持久化完成，消除了 TempDir 清理竞态；竞态测试无数据竞争报告。
- TUI 定向测试覆盖 attach/detach 本地取消、observer 上显式 resume，以及忙碌 `/cancel` 同时停止本地流且不进入输入队列。
- 全量测试首次发现内嵌插件 schema 与桌面端共享文件不一致，已按仓库 `make sync-plugin-schema` 目标同步当前 CLI 内嵌副本，随后 `internal/plugins` 和全量测试通过；未修改桌面端文件。

### 11.3 尚待用户验收

代码实施和自动测试任务已闭环，但不以此替代真实环境验收。第 8.2 节的真实 SSE/WS 多端 UI、真实断网、长模型/长工具、跨进程通知竞争、迟到工具结果及远程子任务场景仍由用户在现有环境自测；发现问题时只在本文件追加实测证据和修复记录，不另建同事项计划。

本轮未创建 Git commit，未推送远端。
