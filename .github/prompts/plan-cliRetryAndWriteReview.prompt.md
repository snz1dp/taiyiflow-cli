## Plan: CLI重试与写入审查

这次建议分五条线一起做，但保持语义收敛：新增“流失败自动重试次数”配置替代当前硬编码的 1 次重试；新增只在失败态可见的 /retry 手动重发；收到 split_assistant_message 时让新一条 AI 消息在 TUI 与普通命令行输出中分行显示；专项复核 SSE 空闲超时逻辑，避免流总耗时超过 idleTimeout 时被误判为超时关闭；文件修改审查不再新增重复配置键，直接复用现有的 write_confirmation_policy，只调整 /config 文案和弹层尺寸/按钮表现。

**Steps**

1. 在 [taiyiflow-cli/internal/config/config.go](taiyiflow-cli/internal/config/config.go) 增加流失败自动重试次数字段，默认 0、最大 3，并补齐默认值、归一化与加载兜底。这个字段建议命名为 stream_retry_attempts。
2. 在 [taiyiflow-cli/cmd/config.go](taiyiflow-cli/cmd/config.go) 增加该字段的 set、unset、show 支持，确保命令行配置与 YAML 持久化一致。这个步骤依赖 1。
3. 在 [taiyiflow-cli/internal/tui/app.go](taiyiflow-cli/internal/tui/app.go) 的 ConfigFormValues、字段索引、字段规格、提交映射和选择器状态中接入新字段；同时把现有“写入确认策略”文案明确成“文件修改审查窗口”语义。这个步骤依赖 1。
4. 在 [taiyiflow-cli/cmd/tui.go](taiyiflow-cli/cmd/tui.go) 把 runner 中的 streamRetryCount 上限改为读取配置值，继续复用 shouldRetryStreamRequestError，只自动重试流连接异常，不扩大到服务端业务错误。这个步骤依赖 1。
5. 在 [taiyiflow-cli/internal/tui/app.go](taiyiflow-cli/internal/tui/app.go) 为 model 增加“最近一次可重发请求”和“最近一次失败状态”两个状态位，在发送入口记录请求，在失败分支置位，在成功完成或新请求开始时清除。这个步骤依赖 4。
6. 在 [taiyiflow-cli/internal/tui/app.go](taiyiflow-cli/internal/tui/app.go) 新增 /retry 指令，并只在最近一次请求失败后出现在 slash 命令提示里；执行时复用原始请求参数，但建议生成新的 RunID，保留原 SessionID。这个步骤依赖 5。
7. 在 [taiyiflow-cli/cmd/tui.go](taiyiflow-cli/cmd/tui.go) 与 [taiyiflow-cli/internal/tui/app.go](taiyiflow-cli/internal/tui/app.go) 接入 split_assistant_message 语义：从 SSE payload 透传拆分标记，并在收到该标记后让下一段 assistant chunk 强制落成新的消息条目，而不是继续拼接到上一条 assistant 输出。这个步骤可与 5、6 并行，但要与现有 assistant chunk 聚合逻辑一起回归。
8. 在 [taiyiflow-cli/cmd/root.go](taiyiflow-cli/cmd/root.go) 的普通命令行流式输出链路中处理 split_assistant_message：记录“下一段 assistant 输出需要换行分隔”的状态，并在下一段可见 assistant 文本输出前补一个空行，避免两条 AI 消息直接粘连。需要兼顾首段输出 trim、show-thinking 过滤以及纯标记消息无 chunk 的情况。这个步骤可与 7 并行设计。
9. 在 [taiyiflow-cli/internal/provider/taiyiflow/client.go](taiyiflow-cli/internal/provider/taiyiflow/client.go) 复核 SSE 空闲超时实现，重点检查 idleTimeoutReadCloser 是否真按“最近一次 SSE 活动后的空闲时长”计时，而不是因为整条流总耗时超过 idleTimeout 就关闭连接；实现时要显式核对 `Read` 成功返回数据后的 `timer.Reset` 是否足以把空闲计时重新起算、`Close` 路径里的 `timer.Stop` 是否会与超时回调竞争、以及超时回调触发底层 `reader.Close` 的时机是否可能把仍在持续产出数据的长流提前打断。必要时调整 timer reset/stop 时机、超时标记写入顺序或包装方式，并保持 stream_idle_timeout_seconds 的语义仍然是“空闲超时”而非“总时长上限”。这个步骤建议与 4 并行推进，因为它直接影响自动重试触发频率和长流稳定性。
10. 在 [taiyiflow-cli/internal/tui/app.go](taiyiflow-cli/internal/tui/app.go) 调大 confirm overlay 的宽度和预览换行宽度，让文件修改审查窗口更大；按钮区继续保留“确认本次写入 / 取消本次写入”，但增强按钮可见性和间距。这个步骤可与 5、6、7、8、9 并行。
11. 补测试，重点覆盖配置边界、配置表单提交、/retry 显隐与重发、split_assistant_message 分条显示、SSE 空闲超时不会因为总耗时超出 idleTimeout 而误关流，以及文件写入确认策略未回退。这个步骤依赖前述实现完成。

**Relevant files**

- [taiyiflow-cli/internal/config/config.go](taiyiflow-cli/internal/config/config.go) 负责配置结构、默认值、归一化。
- [taiyiflow-cli/internal/config/config_test.go](taiyiflow-cli/internal/config/config_test.go) 负责新配置默认值和边界测试。
- [taiyiflow-cli/cmd/config.go](taiyiflow-cli/cmd/config.go) 负责 config set/unset/show。
- [taiyiflow-cli/cmd/tui.go](taiyiflow-cli/cmd/tui.go) 负责 runner 自动重试和配置表单提交映射。
- [taiyiflow-cli/cmd/backend_health.go](taiyiflow-cli/cmd/backend_health.go) 提供 shouldRetryStreamRequestError，直接复用其错误筛选。
- [taiyiflow-cli/cmd/root.go](taiyiflow-cli/cmd/root.go) 已有 splitAssistantMessage 辅助函数，是普通命令行流式输出做“新消息换行分隔”的直接控制点。
- [taiyiflow-cli/internal/tui/app.go](taiyiflow-cli/internal/tui/app.go) 负责 slash 命令、失败态、ConfigForm 和确认弹层。
- [taiyiflow-cli/internal/provider/taiyiflow/client.go](taiyiflow-cli/internal/provider/taiyiflow/client.go) 中的 idleTimeoutReadCloser 与 streamIdleTimeout 是这次 SSE 空闲超时风险排查的主落点，尤其要看 `Read` 内的 `timer.Reset`、`Close` 内的 `timer.Stop` 与超时回调里的底层 `reader.Close` 时序。
- [taiyiflow-cli/internal/runtime/local_tools.go](taiyiflow-cli/internal/runtime/local_tools.go) 需要确认继续只由 write_confirmation_policy 控制文件写入审查。
- [taiyiflow-cli/internal/provider/taiyiflow/client_test.go](taiyiflow-cli/internal/provider/taiyiflow/client_test.go)、[taiyiflow-cli/cmd/tui_test.go](taiyiflow-cli/cmd/tui_test.go)、[taiyiflow-cli/internal/tui/app_test.go](taiyiflow-cli/internal/tui/app_test.go)、[taiyiflow-cli/cmd/root_test.go](taiyiflow-cli/cmd/root_test.go)、[taiyiflow-cli/internal/runtime/local_tools_test.go](taiyiflow-cli/internal/runtime/local_tools_test.go) 是主要测试落点。

**Verification**

1. 运行 `go test ./internal/config`，验证新配置默认值、范围和归一化。
2. 运行 `go test ./cmd`，验证 config 命令与 TUI 配置提交映射。
3. 运行现成任务 `test tui package` 或 `go test ./internal/tui`，验证 /retry 显隐与失败态重发，以及 split_assistant_message 在 TUI 中会强制分成新消息条目。
4. 运行 `go test ./cmd` 中与 [taiyiflow-cli/cmd/root_test.go](taiyiflow-cli/cmd/root_test.go) 相关的测试，确认普通命令行流式输出在收到 split_assistant_message 后会为下一条 AI 消息补一个空行。
5. 运行 `go test ./internal/provider/taiyiflow`，验证 SSE 空闲超时相关测试，尤其是“流总耗时超过 idleTimeout 但期间持续有数据到达时不应被关闭”的场景；建议补两类定向用例：一类覆盖多次 `Read` 命中 `timer.Reset` 后连接仍保持打开，另一类覆盖 `Close` 与超时回调并发时不会重复关闭底层 reader 或把正常 EOF 误报成 idle timeout。
6. 运行 `go test ./internal/runtime`，确认 write_confirmation_policy 复用后文件写入确认逻辑未回退。
7. 手工验证 /config 中的新字段保存、文件修改审查开关生效、制造一次流失败后 /retry 出现并可重发、成功后 /retry 消失，以及连接支持 split_assistant_message 的后端时，TUI 与普通命令行输出都会把新消息分开显示；另外补一个长时间 SSE 会话验证，确认只在真正空闲超过 idleTimeout 时才会断流。

**Decisions**

- 按你的选择，复用现有 write_confirmation_policy，不新增独立“文件修改审查窗口”配置键。
- /retry 只在最近一次请求失败后可见。
- split_assistant_message 的展示范围按现有协议落到 TUI 与普通命令行两条输出链路，不只限于 TUI。
- stream_idle_timeout_seconds 的目标语义保持为“空闲超时”，不是“流总时长上限”；若现实现有偏差，需要在本次一并修正。
- 自动重试只覆盖当前已有的流连接异常分支，不覆盖服务端正常返回的业务错误。

如果这个计划可以，就直接批准；下一位实现代理可以按这个分阶段落地。
