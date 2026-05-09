## Plan: 跨平台环境提示与跨会话记忆开关落地

以“双通道”方式落地：一方面在本地工具描述中明确当前平台限制与跨平台命令策略，另一方面在每次 task 请求中注入环境摘要；同时新增系统配置项与 task 子命令参数 `cross_session_memory_enabled`，并按“命令行（CLI）参数优先于配置文件”生效，最终透传到后端 `cross_session_memory_enabled` 协议字段。

**Steps**
1. 协议与参数建模（Phase 1，阻塞后续）
1.1 在 `internal/provider/provider.go` 的 `StreamRequest` 中新增跨会话记忆字段（建议使用可区分显式设置的类型，避免后续默认值歧义）。
1.2 在 `internal/provider/taiyiflow/client.go` 的 `streamRequestBody` 与 `Stream(...)` 请求体映射中新增 `cross_session_memory_enabled`，并对齐后端 `SimplifiedAPIRequest` 字段语义。
1.3 在 `internal/provider/taiyiflow/client_test.go` 增加请求体断言用例，验证该字段会被正确序列化。

2. 系统配置接入（Phase 2，*depends on 1*）
2.1 在 `internal/config/config.go` 的 `Config` 结构体新增 `CrossSessionMemoryEnabled`（yaml key: `cross_session_memory_enabled`），并在 `Default()` 中给出默认值（false）。
2.2 在 `internal/config/config.go` 的 `Manager.Load()` 归一化流程中确保该字段稳定读取（布尔类型默认值保持与 `Default()` 一致）。
2.3 在 `cmd/config.go` 中补齐 `assignConfigValue`、`resetConfigValue`、`normalizeConfigKey`（含必要别名）与 `renderConfigSummary` 展示项。
2.4 在 `cmd/config_test.go`、`internal/config/config_test.go` 补充 set/unset/summary/default 相关测试。

3. task 子命令参数与优先级（Phase 3，*parallel with step 2.4*）
3.1 在 `cmd/root.go` 的 `rootOptions` 新增跨会话记忆参数与“是否显式设置”标记，新增 flag：`--cross-session-memory-enabled`。
3.2 延续现有 `stream-retry-attempts` 模式，通过 `flagSet.Visit(...)` 识别该 flag 是否传入，保证“参数优先于配置”。
3.3 在 `runTask`/`runInteractive`/`runTaskOnce` 到 `streamTaskWithSkillRetry` 的配置合并链路中注入最终生效值。
3.4 在 `cmd/root_test.go` 新增覆盖：默认继承配置、flag 显式 true 覆盖、flag 显式 false 覆盖。

4. 模型环境告知双通道（Phase 4，*depends on 3*）
4.1 在 `cmd/root.go`（或拆分 helper 文件）新增运行环境摘要构造：操作系统（OS）、架构（ARCH）、工作目录、命令执行目标、跨平台执行建议、`fork/exec ... not supported` 的判定与回退建议。
4.2 将环境摘要注入请求输入（task 文本前置系统提示块，保持简短稳定），确保模型在每轮任务都能看到当前执行环境。
4.3 在 `internal/runtime/local_tools.go` 的 `shell_exec` 工具描述追加平台兼容说明（优先推荐 `command + args` 结构化调用、必要时再用 `rawCommand`），并提示根据当前 OS 选择命令。
4.4 在 `cmd/root_test.go` 与 `internal/runtime/local_tools_test.go` 增加断言：请求输入含环境提示关键字段、工具描述含平台约束关键字。

5. 联调验证与回归（Phase 5，*depends on 2-4*）
5.1 运行 `go test ./cmd ./internal/config ./internal/provider/taiyiflow ./internal/runtime`，确认核心链路通过。
5.2 对关键场景做手动验证：
- 配置开关：`config set/unset/show` 的读写与展示。
- task 覆盖：同一配置下使用 `--cross-session-memory-enabled=true/false` 的行为差异。
- 请求透传：抓包或测试桩确认 `cross_session_memory_enabled` 与环境摘要进入请求。
5.3 回归检查：不设置新参数时，现有 task、会话与工具调用行为保持不变。

**Relevant files**
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go` — 扩展 `StreamRequest` 协议字段。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go` — 请求体结构与字段映射。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client_test.go` — 请求体序列化断言。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go` — 配置结构、默认值、加载归一化。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go` — config set/unset/key alias/summary。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config_test.go` — 配置命令回归测试。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config_test.go` — 配置默认与归一化测试。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go` — task flag、优先级合并、环境摘要注入、StreamRequest 组装。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go` — task 参数覆盖与请求构造断言。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go` — `shell_exec` 工具描述增强。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go` — 工具描述文本断言。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/schemas.py` — 协议真值参考（`cross_session_memory_enabled` 已定义）。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/processing/process.py` — 后端请求上下文真值参考（已消费 `cross_session_memory_enabled`）。

**Verification**
1. 单元测试：`go test ./cmd ./internal/config ./internal/provider/taiyiflow ./internal/runtime`。
2. 命令验证：`taiyiflow-cli config set cross_session_memory_enabled true`、`taiyiflow-cli config show`、`taiyiflow-cli config unset cross_session_memory_enabled`。
3. task 参数验证：`taiyiflow-cli task --cross-session-memory-enabled=true "..."` 与 `--cross-session-memory-enabled=false` 对比请求体字段。
4. 跨平台语义验证：在不同 OS（Windows/Linux/macOS）确认环境摘要内容与 `shell_exec` 描述中的平台提示一致。

**Decisions**
- 已确认采用“双通道”环境告知：工具描述 + 每次请求注入环境摘要。
- 已确认参数优先级：命令行（CLI）flag 覆盖系统配置。
- 包含范围：配置项、task 子命令、请求透传、环境提示与测试。
- 不包含范围：后端协议变更、TUI 新交互入口、历史会话数据迁移。

**Further Considerations**
1. 环境摘要文本长度控制：建议限制在 8-12 行，避免稀释用户任务主体。
2. `fork/exec` 兼容提示语气：建议使用“报错判定与替代建议”而不是绝对禁止，避免与可执行场景冲突。
3. 若后续需要更强可观测性，可在 `--show-stream-events` 下打印“已注入环境提示摘要”简短日志。