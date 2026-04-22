## Plan: 沙箱越界询问与错误输出收敛

在保持当前运行时结构最小改动的前提下，新增“路径越界策略（deny/confirm/allow）”并支持全局配置与会话覆盖；当路径超出沙箱且策略为 confirm 时交互询问，非交互场景自动拒绝并给出明确提示。同时移除用户可见错误中的 [XXX] 前缀，保留内部错误类型用于程序判断。

**Steps**
1. 设计并落地配置模型扩展（阻塞后续步骤）
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go 的 Config 中新增全局策略字段（建议名：SandboxEscapePolicy，取值 deny/confirm/allow，默认 confirm）。
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/agent/session.go 的 SessionState 中新增会话级覆盖字段（建议名：SandboxEscapePolicyOverride，可空），并保持向后兼容（旧会话文件无该字段时回退默认）。
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go 扩展 config set/unset 键解析与校验，允许设置全局策略；同步 renderConfigSummary 文案。

1. 接入优先级解析与运行时传递（依赖步骤1）
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 与 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 增加“有效策略”解析函数，按已确认优先级执行：会话配置 > 命令行参数 > 全局配置 > 默认值。
1. 在 task 入口增加命令行参数（建议：--sandbox-escape-policy），仅影响当前进程；若未提供则继续使用会话/全局。
1. 在交互命令中补充会话级设置入口（建议通过 /config session set sandbox_escape_policy <value> 或等价命令），并写回 SessionStore。

1. 改造越界判定为“可询问”流程（依赖步骤2）
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/security_helpers.go 保留路径解析与敏感目录硬拦截逻辑；把“超出沙箱”错误从纯字符串升级为可识别错误类型（携带 resolved path 与触发原因），便于上层分支处理。
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go 与 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go 的路径入口点统一捕获该错误：
1. 策略 deny：直接拒绝。
1. 策略 allow：允许继续。
1. 策略 confirm：触发用户确认（CLI 读 stdin；TUI 走 ConfirmRequest 弹窗）；用户同意后仅本次调用放行。
1. 非交互模式（stdin 不可交互）下 confirm 自动拒绝，并返回“当前为 confirm 且无交互输入，请改用 allow 或会话覆盖”的可操作提示。

1. 去除用户可见 [XXX] 错误代码（可与步骤3并行）
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go 调整 toolError.Error() 输出为纯消息，不再拼接 [Code]。
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 的错误展示路径（EventErrorNote / errorText）补一层兜底清洗，防止历史或第三方错误文本含 [XXX] 前缀泄漏。
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/main.go 与 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 保持统一输出口径（执行失败仅打印可读消息）。

1. 补齐测试与回归验证（依赖步骤3、步骤4）
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go 新增越界策略测试矩阵：deny/allow/confirm（交互同意、交互拒绝、非交互）。
1. 新增配置与会话序列化测试（建议新增 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config_test.go 与 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/agent/session_test.go），验证新字段默认值、读写兼容与优先级解析。
1. 新增错误输出断言，确保不再出现 [PERMISSION_DENIED] 这类前缀。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go — 扩展全局策略字段与默认值。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go — 扩展 config set/unset 与摘要展示。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/agent/session.go — 扩展会话级策略覆盖字段。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go — 解析命令行参数、组装有效策略并传入流式执行。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — TUI 确认弹窗接入与会话级配置入口。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/security_helpers.go — 越界判定与可识别错误类型。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go — 工具错误输出格式、路径越界确认调用。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go — built_in 命令路径参数的越界策略联动。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go — 运行时回归测试。

**Verification**
1. 运行 go test ./...（至少覆盖 internal/runtime、internal/config、internal/agent）。
1. 手工验证 CLI 交互：在 confirm 策略下访问沙箱外路径，出现确认提示；拒绝后失败，允许后成功。
1. 手工验证非交互：echo "" | taiyiflow-cli task "..." 触发越界时自动拒绝并给出配置指引。
1. 手工验证会话优先级：同一全局配置下，通过会话覆盖切换策略，观察行为变化。
1. 全局 grep 验证：用户可见输出中不再含 [A-Z_]+ 风格错误码前缀。

**Decisions**
- 已确认默认策略为 confirm。
- 已确认非交互模式下 confirm 自动拒绝并提示如何放开。
- 已确认优先级为：会话配置 > 命令行参数 > 全局配置 > 默认值。
- 范围内：本地客户端工具与 built_in 本地命令的路径越界处理、CLI/TUI 用户可见错误格式。
- 范围外：后端协议变更、跨仓库服务端错误结构改造、敏感目录黑名单策略变更。

**Further Considerations**
1. 会话级命令语法建议优先复用 /config 命令族，避免新增过多顶层斜杠命令，降低学习成本。
2. 即使策略为 allow，仍建议保持敏感目录硬拦截（.ssh/.gnupg/Keychains），避免误放开高风险路径。