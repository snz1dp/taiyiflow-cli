## Plan: TUI 审查窗与 About/输入框

本次建议按“三条主线 + 一次回归收尾”推进：先修文件写入确认的控制链路和审查窗展示，再补 about 双入口，最后把输入框高度从固定 6 行改成按终端高度动态扩展。已确认 /about 需要同时覆盖顶层子命令和会话内斜杠命令；确认弹窗保留 Esc/q/N 作为取消，但界面上必须显式渲染确认/取消按钮。

**Steps**

1. 阶段一，修复文件写入确认的控制链路。围绕 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go 的 RequiresClientToolConfirmation、authorizeClientTool 与 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 的 EventClientTool 分支对齐，确保 write_confirmation_policy=direct 时 file_edit、write_file 不进入 TUI 审查弹窗；同时保留 approval_mode 和 permission_rules 对其它可变更工具的既有语义。这个步骤阻塞后续“窗体优化”验收，因为要先保证 direct 配置真的绕过审查窗。
2. 阶段二，重做文件编辑审查窗的展示语义。扩展 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 中 buildTUIClientToolConfirmRequest 与 buildWriteConfirmationPreview，把当前“路径 + 编辑前/编辑后摘要”改成更接近 unified diff 的文本预览，至少明确展示删除行、增加行和上下文；同时在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 的 renderConfirmOverlay 中保留明确的“确认本次写入 / 取消本次写入”按钮，并调整 Hint 文案，使“按钮可见、Esc/q/N 也可取消”的行为一致。
3. 阶段三，放大审查窗并优化滚动/换行。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 的 renderOverlay、renderConfirmOverlay 和 fitOverlayContent 使用的宽高上限，当前 pendingConfirm 宽度上限为 112 且预览换行宽度受 m.contentWidth-14 限制，需要放宽到更接近终端可用宽度，并给长 diff 预览更稳定的滚动空间。这个步骤可与阶段二并行，但要复用同一份 diff 预览文本。
4. 阶段四，收敛智能体自动续跑配置语义。把 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go 中 agent_auto_loop_max_rounds 的默认值改为 0、最大允许值改为 100，并将“0 表示禁用”做成唯一语义；同步移除 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 配置表单里的“智能体自动续跑默认开关”，以及 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 对 enabled/max_rounds 的双字段验证与展示，避免布尔开关和轮数上限出现双配置源。建议最终由 max_rounds 是否大于 0 派生请求侧启用状态。
5. 阶段五，实现 about 双入口并统一元数据来源。新增 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/about.go（或从 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/version.go 提炼共享 helper），让顶层 `taiyiflow-cli about` 与交互式 `/about`、TUI `/about` 复用同一份输出。元数据优先使用 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/buildinfo/buildinfo.go 的 version/commit/date，并补充固定的产品名、作者/组织、简介、仓库定位说明；同时把 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 的根命令 dispatch、printRootUsage、printInteractiveUsage 与 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 的 buildTUIHelpLines、localCommand switch、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 的 slashCommands 一起补齐。
6. 阶段六，输入框自适应高度。当前 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 已在 handleInputKey 中支持 shift+enter/alt+enter/ctrl+j 插入换行，真正缺口在 currentInputHeight 与 inputHeightForValue 只按内容计算并硬编码最多 6 行。需要把高度上限改成“终端总高度的 30% 与最小 1 行之间取较小值”，并在 syncLayout 中同步 history viewport 的剩余高度，确保长输入时不会压塌消息区。
7. 阶段七，补文案和回归测试。更新 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/README.md 与相关帮助文本，写清 direct 写入、about 入口、以及 auto_loop_max_rounds=0 的新语义；同时补齐 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config_test.go 的覆盖。

**Relevant files**

- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go — 文件写入审查的真值来源，当前 direct/confirm 逻辑就在 RequiresClientToolConfirmation 与 authorizeClientTool。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — TUI 对 EventClientTool 的确认分发、write preview 生成、TUI 本地斜杠命令入口。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — 确认弹窗渲染、slashCommands、输入框高度计算、配置表单字段定义和帮助提示。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go — agent_auto_loop_max_rounds 默认值、上限和归一化逻辑。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go — CLI 配置读写校验，尤其是 agent_auto_loop_max_rounds 与 write_confirmation_policy。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go — 顶层命令分发、交互式 `/about`、帮助输出和会话级配置命令。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/version.go — 现有版本输出，可提炼 about 共享 formatter。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/buildinfo/buildinfo.go — about/version 统一元数据源。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go — direct 写入绕过审查和 confirm 模式保留审查的回归锚点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go — 审查窗渲染、按钮显式性、输入框高度增长与 30% 上限测试锚点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go — TUI 配置保存、帮助列表、`/about` 本地命令行为。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go — 顶层 about 分发、交互式 `/about`、命令帮助输出。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/README.md — 运行时行为和 about 文档补充。

**Verification**

1. 在 taiyiflow-cli 根目录运行 `go test ./internal/runtime`，确认 direct 写入不再弹审查窗，confirm 模式仍保留审查。
2. 运行 `go test ./internal/tui`，确认审查窗 diff 预览、显式按钮和输入框自适应高度行为。
3. 运行 `go test ./cmd`，确认 `taiyiflow-cli about`、交互式 `/about`、TUI `/about` 与帮助文本同步。
4. 运行 `go build -o taiyiflow-cli .`，确认配置语义和 about 双入口没有破坏整体编译。
5. 手动烟测：把 write_confirmation_policy 设为 direct 后触发 file_edit/write_file，确认无审查窗；切回 confirm 后确认出现更大的 diff 弹窗并带明确按钮；输入多行内容时高度随内容增长且不超过终端 30%；分别验证 `taiyiflow-cli about`、交互式 `/about` 与 TUI `/about` 输出一致。

**Decisions**

- `/about` 同时覆盖顶层子命令和会话内斜杠命令，避免 CLI/TUI 信息源分叉。
- 确认弹窗保留 Esc/q/N 作为取消快捷操作，但界面必须显式渲染确认/取消按钮。
- `shift+enter` 当前已存在于 TUI 键处理，当前任务按“稳定化 + 高度自适应”处理，不从零重写输入行为。
- 自动续跑改为 `agent_auto_loop_max_rounds=0` 即禁用，默认值 0、最大值 100；移除独立默认开关，避免双配置源。
- 范围限定在 taiyiflow-cli 仓库，不扩展到 taiyiflow 前端或其它外部 UI。

**Further Considerations**

1. diff 预览建议使用 unified diff 风格而不是继续沿用“编辑前/编辑后”摘要，因为当前 confirm overlay 已支持多行滚动预览，只需要补统一的文本生成 helper。
2. 当前终端 TUI 代码里没有显式鼠标点击处理，因此“点击空白处关闭”这项在实现上等价为“不要依赖隐式关闭，必须有清晰按钮和明确取消快捷键提示”；计划不额外引入鼠标事件系统。
