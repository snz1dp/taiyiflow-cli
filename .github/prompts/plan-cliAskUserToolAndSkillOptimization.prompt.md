## Plan: CLI ask_user 工具与问答弹窗

在 taiyiflow-cli 内新增一个无副作用的本地 client tool ask_user，对齐参考 ask_user 的 questions 协议，并按你的要求扩展多选。实现上不改太乙智启后端协议：继续通过 client_mcp_servers 暴露工具，结果仍用 SubmitClientCallResult 回传。TUI 侧复用现有 confirm/login/config overlay 状态机，新增一类阻塞式问题表单消息；非 TUI 交互式 CLI 保留终端顺序提问回退。同时把 askuser 从当前孤立的内置目录补成真正可发现的 bundled skill，并改写为面向 CLI 的提示词。

**Steps**
1. 阶段一：定义 ask_user 工具契约与暴露范围。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go，在 clientToolDefinitionsForMode 中新增 ask_user 定义，协议沿用参考实现的 questions/name/label/input_format/options_list，并扩展 multi_select；同时更新 canonicalClientToolName、allowedClientToolSetForMode，并在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_capability.go 的 modeAllowsClientTool 中把 ask_user 作为无副作用工具放开给 ask、plan、agent 三种 request mode。建议返回结果按题目 key 直接输出 string 或 []string，取消时走错误回调而不是空结果。此步骤阻塞后续所有实现。
2. 阶段二：扩展 runtime 的问答执行抽象。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go，为 ClientRuntime 增加 AskUserQuestion、AskUserRequest、AskUserResponse 之类的内部结构和 SetAskUserPrompter 注入点；在 ExecuteClientToolPayload 中分发 ask_user；默认实现走 stdin/stdout 顺序提问，文本题读取单行输入，单选支持编号或原文输入，多选支持逗号分隔编号/值。此步骤依赖步骤 1。
3. 阶段三：为 TUI 增加阻塞式 ask_user overlay。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go，参照 ConfirmRequest、confirmRequestMsg、startStream 的 channel 往返机制，新增 askUserRequestMsg、AskUserFormState、AskUserFormSubmission 和对应 response channel；扩展 Runner 与 startStream 签名，使流执行期间可以像 confirm 一样阻塞等待结构化答案；在 renderOverlay、键盘处理、focus/scroll 管理中复用 login/config overlay 模式，文本题复用 textinput，单选复用现有单选渲染风格，多选沿用 KnowledgePicker 一类的上下移动 + toggle 交互习惯。此步骤依赖步骤 2。
4. 阶段四：在 TUI runner 中接入 ask_user 执行闭环。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go，在 runTUI 的 runner 闭包里给 runtimeClient 注入 ask_user handler，使 provider.EventClientTool -> runtimeClient.ExecuteClientToolPayload 时能把 ask_user 请求转成 TUI overlay，并在用户提交后继续后续流式执行；保留现有 confirm、skill review、sandbox escape 确认链路不变。此步骤依赖步骤 3。
5. 阶段五：优化并真正启用 askuser bundled skill。修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/bundled_registry.go，把 askuser 注册到 bundledSkillDefinitions；修改 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/builtin/askuser/SKILL.md，把当前偏“网页代理”的内容改成 CLI 场景的澄清与确认提示，明确何时批量提问、何时单题追问、何时用 ask_user 而不是猜测、何时对填写/提交/破坏性动作先向用户确认。必要时补 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/skills.go 相关测试或 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/skills.go 供应链验证，确保 askuser 不再只是孤立目录。此步骤可与步骤 2-4 并行，但合并前必须确认默认启用策略。
6. 阶段六：补测试与手工验收。为 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go 增加 ask_user schema、mode gate、stdin fallback、取消/多选解析测试；为 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go 增加 ask_user 阻塞消息、overlay 渲染、字段导航、提交/取消回传测试；为 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go 或 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go 增加 TUI runner 闭环与 askuser skill 出现在 RequestedSkillNames / ClientSkillPackages 的回归测试。最后做一轮 TUI 实测和一轮交互式 CLI 实测。此步骤依赖步骤 1-5。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go — ask_user 工具 schema、工具白名单、描述文本。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go — ClientRuntime 注入式 ask_user 执行器、默认终端回退、ExecuteClientToolPayload 分发。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go — ask_user 工具定义与 fallback 行为测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_capability.go — ask/plan/agent 三种模式的 client tool 可用性控制。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — Runner/startStream、阻塞消息、AskUserFormState、overlay 渲染与键盘处理。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go — ask_user 表单状态机与渲染回归测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — TUI runner 注入 ask_user handler，与 provider.EventClientTool 的执行闭环。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go — ask_user 在 TUI runner 和技能供应链中的回归测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go — 非 TUI 交互式 CLI 默认 runtime 注入点与普通流执行路径。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/bundled_registry.go — askuser bundled skill 注册点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/builtin/askuser/SKILL.md — askuser 技能提示词正文。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/skills.go — Discover / ClientPackages / EnabledSkillNames 行为复用点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/skills.go — loadSkillPayloadWithConfig，请求装配中的技能供应链入口。

**Verification**
1. 运行 go test ./internal/runtime，验证 ask_user schema、mode gate、stdin fallback、取消/多选解析。
2. 运行 go test ./internal/tui，验证 ask_user 阻塞消息、overlay 导航、提交/取消与渲染。
3. 运行 go test ./cmd，或至少覆盖新增的 cmd/tui_test.go 与 cmd/root_test.go 用例，确认 TUI runner 与技能上送闭环。
4. 运行 go build -o taiyiflow-cli .，确认 CLI 编译通过。
5. 手工验证 TUI：启动 taiyiflow-cli，在 ask、plan、agent 模式分别触发 ask_user，检查文本题、单选、多选、取消、继续流式执行。
6. 手工验证交互式 CLI：非 TUI 模式触发 ask_user，检查顺序提问、编号选择、多选输入与回传结果。
7. 手工验证技能：在 skills picker 或请求 payload 中确认 askuser 已进入 Discover、RequestedSkillNames 与 ClientSkillPackages，而不是继续处于未注册状态。

**Decisions**
- 包含范围：CLI client runtime、TUI overlay、交互式 CLI fallback、askuser bundled skill 注册与文案优化。
- 排除范围：不修改 taiyiflow 后端协议，不新增新的 stream event 类型，不改 sibling JS SDK 文件。
- 已确认：ask_user 需要覆盖 TUI 弹窗与交互式 CLI fallback；options 题允许扩展多选。
- 建议：ask_user 作为无副作用工具，暴露到 ask、plan、agent 三种 request mode；否则 askuser skill 在 plan/ask 模式下无法真正落地。
- 风险点：askuser 当前未进入 bundled registry，若只改 SKILL.md 不改注册，优化不会生效。

**Further Considerations**
1. 建议保持返回结构尽量扁平，题目 key 直接映射到 string 或 []string，避免再套一层 answers 增加模型使用负担。
2. 若要完全追平参考实现里“自动追加其它要求”，建议在 TUI/CLI 适配层统一追加一个可选自由文本题，并使用固定 key 避免与模型自定义问题冲突；若不做，需同步更新工具 description，避免误导模型。
