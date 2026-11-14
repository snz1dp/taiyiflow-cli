# 执行控制与恢复

智能体任务在后台以 AgentLoop 形式运行，CLI 提供暂停、停止、恢复与只读观察四种控制能力。控制的权威状态在服务端：CLI 发出请求后等待服务端确认，而不是本地直接终止。

## 暂停与停止

### TUI 内

| 命令 | 行为 |
| --- | --- |
| `/pause` | 请求暂停当前执行；服务端在合适时机生成检查点 |
| `/cancel` | 请求停止当前执行 |

请求受理后提示“暂停/停止请求已受理（status=...），等待服务端权威状态”；暂停时若尚未生成检查点会额外说明。

### 快捷键

任务执行中按 `Ctrl+C`：停止本地执行并请求服务端取消；若处于只读观察（attach）状态，则仅退出观察，远程执行不会被停止。

### 接口

```text
POST /run/sessions/{session_id}/pause
POST /run/sessions/{session_id}/cancel
```

请求体携带 `run_id`、`request_id`、`client_id`、`device_id`（均可选）；响应包含：

| 字段 | 说明 |
| --- | --- |
| `status` | 服务端权威状态 |
| `checkpoint_id` | 暂停生成的检查点 ID（可能为空，表示尚未生成） |
| `subtask_cancel_requested` / `subtask_cancel_pending` / `subtask_cancel_failed` | 子任务取消的请求数、待处理数与失败标记 |

## 检查点与恢复

暂停后服务端生成检查点（checkpoint），记录执行现场：

| 字段 | 说明 |
| --- | --- |
| `resume_stage` | 恢复阶段 |
| `pause_reason` | 暂停原因 |
| `requires_user_resume` | 是否需要用户显式恢复 |
| `waiting_call_ids` / `pending_call_ids` / `uncertain_call_ids` | 等待中、待处理、状态不确定的工具调用 |
| `expires_at` | 检查点过期时间 |

### 发现可恢复检查点

```text
GET /run/checkpoints/resumable?client_id=...&device_id=...&flow_id=...&session_id=...
```

`client_id` 必填，其余可选过滤。CLI 启动时会据此发现当前会话的可恢复检查点。

### 显式恢复

```bash
taiyiflow-cli task --resume
```

- 从当前会话的可恢复检查点恢复 AgentLoop，**不重复发送用户输入**；
- 不接受新的任务描述，不能携带 `--file`；
- 与 `--attach`、`--new` 互斥。

恢复请求以 `execution_mode=resume` 发起，服务端从检查点记录的阶段继续执行，处理等待中的工具调用。

## 只读观察（attach）

```bash
taiyiflow-cli task --attach
```

- 以只读方式观察当前会话的远程执行，**不执行本地工具**；
- 不接受任务输入，不能携带 `--file`，与 `--resume`、`--new` 互斥；
- 适合观察另一台设备发起的任务，或在不干预执行的情况下查看进度；
- 观察中按 `Ctrl+C` 仅退出观察，远程执行继续。

## 会话执行状态

```text
GET /sessions/{session_id}
```

返回会话的执行元数据：`executing`（是否执行中）、`executing_run_id`、`run_status`、`origin_client_id` / `origin_device_id`（发起方）、`current_event_id` / `current_event_seq`（当前事件位置）。CLI 用它判断会话是否被其他客户端占用、是否需要 attach。

## 执行模式小结

| 模式 | 入口 | 行为 |
| --- | --- | --- |
| `start` | `task "任务"` | 正常发起新执行 |
| `resume` | `task --resume` | 从检查点恢复，不重发输入 |
| `attach` | `task --attach` | 只读观察，不执行本地工具 |

## 流式中断与重试

- 流空闲超时由 `stream_idle_timeout_seconds` 控制（默认 1200 秒，0 禁用）；
- 连接中断/超时类异常会自动重试（`doctor` 输出中报告当前重试次数）；
- 长时间闲置后的首次请求优先用 `persistent_token` 自动续期并重放原始 stream 请求，续期失败才回落重新登录。