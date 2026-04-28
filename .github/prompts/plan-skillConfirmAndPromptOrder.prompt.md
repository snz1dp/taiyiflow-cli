## Plan: 修复工具确认与提示顺序

修复 taiyiflow-cli 中两类相邻且相关的 TUI 问题：一是技能或客户端工具在弹窗确认“允许”后仍被 runtime 二次拒绝；二是技能、客户端工具、本地命令以及其他工具或命令的执行结果先于消息提示显示。推荐方案是复用现有 TUI 确认能力作为唯一交互确认入口，runtime 在收到 TUI 已确认的执行配置后不再重复询问；同时把各类工具或命令的提示事件与结果事件拆成稳定的先后顺序，统一保证“先提示、后结果”，并用单测固定该事件时序。

**Steps**
1. 阶段一：统一“是否需要确认”和“确认后是否允许执行”的判定。梳理 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 中 provider.EventClientTool 分支、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go 中 RequiresClientToolConfirmation 与 authorizeClientTool 的差异，明确 TUI 已确认场景下 runtime 应跳过交互式 confirmClientTool。这个步骤阻塞后续权限修复。
2. 阶段二：修复 runtime 授权逻辑。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go，使 ApprovalMode=auto 的 TUI 已确认执行在 ask 规则、技能审查、写入确认三类路径下都不会再次落入 stdin 确认；同时保持 deny 和 readonly 的拒绝语义不变，避免把真正的禁止规则误放行。这个步骤依赖步骤 1。
3. 阶段三：补权限回归测试。扩展 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go，新增至少两类测试：一类验证 skill/file_edit 在 TUI 已确认等价配置下不会再被 ask 规则或 SkillReviewPolicy=confirm 拒绝；另一类验证 deny/readonly 仍然拒绝，防止放宽过头。这个步骤依赖步骤 2。
4. 阶段四：固定 TUI 中所有工具与命令提示和结果的顺序，并同步规范用户可见文案。检查 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 中 provider.EventClientTool、provider.EventBuiltIn、provider.EventToolCall 等相关分支，以及 emitTUIClientToolResultEvents 和本地命令输出进入 TUI 的路径，保证“工具或命令调用提示/进度提示”先进入 TUI 事件流，再在执行完成后发出结果面板、结果消息或输出摘要；如果当前还有 stdout/stderr 直写、系统消息补写或 panel 抢占顺序不一致的路径，一并收敛为同一时序。与此同时，梳理该链路上的确认框标题、按钮文案、提示文本与结果摘要，尽量移除纯英文提示，确需保留英文标识时统一采用“中文(english)”格式。必要时补一个明确的 EventSystemNote 或 EventToolSummary 存根，避免任何工具或命令结果先于提示占位。这个步骤可与步骤 3 并行，但推荐在权限修复后一起验证。
5. 阶段五：补 TUI 事件序列测试。扩展 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go，沿用 TestUpdateEndToEndToolCallAndAssistantSplitChain 的事件驱动模式，新增覆盖客户端工具、本地命令和其他工具路径的顺序断言，至少固定“调用提示/进度提示 -> 结果面板或结果消息 -> 后续 assistant chunk”的时序；如需要，也在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go 补建构函数或 emit 序列的窄测试，并覆盖关键确认文案格式。这个步骤依赖步骤 4。
6. 阶段六：执行聚焦验证。先跑 taiyiflow-cli 工作区现有的 go test ./internal/runtime 与 go test ./internal/tui；如果 cmd 相关测试被改动，再补 go test ./cmd。最后人工验证一次 TUI：触发 skill 审查弹窗后选择“本次允许”，确认不再出现误拒绝；再分别触发客户端工具、本地命令和其他会产出结果的工具路径，确认所有结果面板、结果消息与输出摘要都在提示出现之后才显示；同时检查该链路上的用户可见提示已改为中文优先，必要英文采用“中文(english)”格式。这个步骤依赖步骤 3 和步骤 5。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go — 复用 RequiresClientToolConfirmation、authorizeClientTool、confirmClientTool，修正 TUI 已确认后的授权分支。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go — 复用 ExecuteClientToolPayload 入口，确认不需要新增第二套执行入口。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — 调整 provider.EventClientTool、provider.EventBuiltIn、provider.EventToolCall 等分支和结果事件发送顺序，确保所有工具与命令路径都是提示先于结果。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — 复用 applyEvent、EventClientToolCall、EventBuiltInCall、EventToolCall、EventToolPanel 的现有渲染模型，必要时只加最小消息存根，不改整体 overlay 设计。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go — 新增 runtime 授权回归测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go — 新增事件顺序与时间线稳定性测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go — 复用 buildTUIClientToolConfirmRequest、applyEffectiveSkillReviewForPayload 的现有测试模式，必要时补窄测试。

**Verification**
1. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli 运行 go test ./internal/runtime，确认权限策略相关单测通过。
2. 在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli 运行 go test ./internal/tui，确认事件顺序与 overlay 行为单测通过。
3. 如果修改了 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 或新增 cmd 层测试，再运行 go test ./cmd。
4. 手工在 TUI 中复现多条路径：技能弹窗选择“本次允许”后执行成功；客户端工具结果面板只在提示出现之后弹出；本地命令和其他工具执行结果也都只在对应提示出现之后展示。

**Decisions**
- 第二个问题的目标行为已确定为“保留现有结果展示形态，但所有工具与命令结果都必须等提示出现后再显示”，不在本次范围内把工具结果整体改造成普通消息时间线。
- 本次修复优先保持现有权限语义：deny 规则和 readonly 模式继续生效；只消除 TUI 已确认后仍被 runtime 二次询问/拒绝的不一致。
- 本次涉及的用户可见提示、标题、按钮与摘要默认使用中文；确需保留英文术语或标识时，统一采用“中文(english)”格式，避免裸英文提示。
- 优先做最小改动，不重构技能发现、面板渲染框架或 provider stream 协议。

**Further Considerations**
1. 如果后续发现非 TUI 交互模式也存在类似“双重确认”，可以在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 复用同一授权收敛策略，但不作为本次首要范围。
