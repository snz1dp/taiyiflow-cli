## Plan: 对话能力元数据界面规划

基于 metadataResponse 新增的 SubHead、Questions、Features，在 taiyiflow-cli 里补齐“模型能力驱动的对话界面状态”。推荐方案是把能力解析与默认值收敛到 provider/cmd 层，把展示与交互收敛到 TUI Snapshot 和本地 slash 命令层，避免让 internal/tui 直接理解后端原始字符串。Questions 本次仅展示不交互；knowledge_search 采用真实后端知识库接口做选择，不做占位输入。

**Steps**

1. 阶段一：定义能力模型与状态边界。先在 internal/provider/provider.go 的 Metadata 周边新增明确的能力归一化表示，例如对 Features 做 canonical 映射，至少覆盖 internet_search、knowledge_search、image_understanding、audio_understanding、video_understanding、document_understanding，并约定 unknown feature 的保留策略。这里应复用 internal/provider/taiyiflow/client.go 已拿到的原始 Features，避免在 TUI 层散落字符串判断。
2. 阶段一：扩展 TUI 快照模型。为 internal/tui/app.go 的 Snapshot 增加与界面直接相关的字段：SubHead、Questions、能力开关状态、知识库选择状态、允许上传的文件能力集合，以及必要的“是否默认开启联网搜索”派生值。Snapshot 保持“可渲染状态”而非后端原始响应，避免在视图代码里重复推导。此步骤依赖步骤 1。
3. 阶段二：在 cmd/tui.go 的 buildTUISnapshot、buildInitialTUISnapshot、refreshSnapshot 闭包里，把 metadata 注入到 Snapshot。具体包括：欢迎区增加 SubHead 与 Questions；Features 决定默认的 InternetSearch 开关、知识库入口显隐、附件类型能力显隐。首次进入与 snapshotRefreshedMsg 刷新都要沿用 internal/tui/app.go 现有 buildInitialEntries / shouldReplaceEntriesOnSnapshotRefresh 机制，避免再次出现“首屏未重建”的回归。此步骤依赖步骤 2。
4. 阶段二：补齐 provider 层知识库数据访问。基于 taiyiflow 后端的 /agents/knowledge/bases 和 /agents/documents 接口，在 internal/provider/provider.go 定义知识库列表/文档列表数据结构与方法，在 internal/provider/taiyiflow/client.go 实现请求和响应映射，并沿用当前 API 前缀归一化规则。知识库列表只在 metadata.Features 包含 knowledge_search 时拉取，避免无意义请求。此步骤可与步骤 3 并行，但在命令实现前必须完成。
5. 阶段三：设计并实现对话命令。保持 slash command 风格与 internal/tui/app.go 的 slashCommands、visibleSlashCommands、handleLocalSlashCommand 一致，新增一个能力总入口，建议用 /chat 或 /capabilities 之一集中管理，而不是为每个 feature 新增顶层命令。推荐子命令集合如下：status 查看当前能力状态；internet on|off|status 管理联网搜索；knowledge 打开知识库选择面板或输出当前选中项；knowledge clear 清空知识库范围；files 查看当前允许上传的类型与已选文件。Questions 只在欢迎区展示，不进入 slash 命令交互。此步骤依赖步骤 2 与步骤 4。（默认开启联网搜索的智能体在 status 输出时要明确标注；knowledge 命令需要根据 metadata.Features 决定是否可见；knowledge 选择面板的选项来自步骤 4 的接口；files 命令的输出也要基于 metadata.Features 进行动态调整。）
6. 阶段三：实现知识库选择与状态回写。复用 cmd/local_commands.go 返回 LocalCommandResult 的模式，为知识库选择引入新的 picker state 或沿用现有 overlay 结构扩展一类 picker。选择结果回写到 Snapshot，并通过 internal/tui/app.go 的 applyLocalCommandResult 触发局部刷新，而不是直接操作输入框。需要支持清空、单选/多选策略、加载失败提示，以及在切换智能体后根据新 metadata 自动清理失效选择。此步骤依赖步骤 5。
7. 阶段三：实现附件能力约束。当前 internal/tui/app.go 的 Request 尚未承载 InputFiles、InternetSearch、KnowledgeScopes、KnowledgeDocs；需要先补齐请求对象，再由 cmd/tui.go 的 runner 映射到 provider.StreamRequest 已存在的对应字段。文件上传入口应只在 metadata.Features 声明相应理解能力时开放；没有对应 feature 时，在命令帮助、状态栏和错误提示里都要给出一致反馈。此步骤依赖步骤 2；真正的上传/解析命令若当前仓库尚未存在，可先在计划中限定为“约束与入口预留”，不扩展底层文件选择器以外的上传协议。
8. 阶段四：更新欢迎区与帮助文案。internal/tui/app.go 的 buildInitialEntries / buildWelcomeEntries 需要把 WelcomeMessage、SubHead、Questions 组合成稳定的欢迎块；cmd/tui.go 的 buildTUIHelpLines 和 slashCommands 描述也要同步反映新增对话命令及其可见条件。Questions 仅展示，不做回车填充或直接发送。
9. 阶段四：补齐测试。优先覆盖 internal/tui/app_test.go 的 slash command 可见性、欢迎区渲染、snapshot 刷新重建、能力关闭时的命令隐藏；覆盖 cmd/tui_test.go 的 metadata -> snapshot 映射、知识库接口失败降级、请求映射到 provider.StreamRequest 的字段；覆盖 internal/provider/taiyiflow/client.go 周边单测，验证 metadata features 解析与 /agents/knowledge/bases、/agents/documents 调用路径。

**Relevant files**

- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go — 扩展 Metadata、StreamRequest 附属类型，以及知识库列表接口定义。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go — 复用 metadataResponse，补充 Features 归一化辅助函数、知识库接口 client 方法、stream request 映射复核。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — buildTUISnapshot、buildInitialTUISnapshot、runner、buildTUIHelpLines、本地 slash 命令编排的主落点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_commands.go — 新增能力命令解析与知识库选择结果包装，沿用 LocalCommandResult / ToolPanel 模式。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — Snapshot、Request、slashCommands、welcome entries、overlay/picker、applyLocalCommandResult 的主要界面修改点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go — slash command、欢迎区与刷新行为测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go — metadata 映射与命令交互测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/schemas.py — 确认 stream 请求字段 knowledge_scopes、knowledge_docs、internet_search 的真值来源。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/processing/process.py — 确认知识库范围解析与 internet_search 禁止与上传文件并存的后端行为。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/agent_knowledge.py — 知识库列表与文档列表接口真值来源，用于 provider 适配。

**Verification**

1. 运行 taiyiflow-cli 的相关单测，至少覆盖 internal/tui 与 cmd 两层，确认新增 slash command 可见性、欢迎区渲染、snapshot 刷新不回退。
2. 为 provider 层补充 HTTP stub 测试，验证 metadata features、/agents/knowledge/bases、/agents/documents 的解析与 API 前缀拼接。
3. 手动验证四组场景：仅 internet_search；internet_search + knowledge_search；仅多模态文件能力；无任何 feature。确认界面显隐、默认值和错误提示一致。
4. 手动验证“带文件上传时 internet_search 自动关闭或不可选”的行为，需与 processing/process.py 中 input_request.internet_search = input_request.internet_search and len(input_request.input_files) == 0 保持一致。
5. 手动验证切换智能体、刷新 snapshot、重置会话后，知识库选择和能力状态不会残留到不支持的智能体上。
6. 做一次全量回归测试，确认本次改动没有引入其他功能的回归问题。

**Decisions**

- 本次只规划对话界面与 slash 命令，不扩展 Questions 的点击即发交互；Questions 仅作为欢迎区示例展示。
- knowledge_search 使用真实知识库接口，而不是手动输入 ID 的占位方案。
- 推荐把能力命令收敛为一个总入口，而不是拆成多个顶层 slash command，降低命令面噪音并便于后续继续扩展 feature。
- 文件理解能力与上传入口本次按“能力约束 + 请求字段打通 + UI 显隐”规划；如果仓库内尚无成熟文件选择器，上传交互细节可作为紧邻后续步骤，不在本轮计划里发明新协议。
- provider 请求地址必须沿用 /taiyi/composer/api/v1 的归一化规则，避免知识库新接口绕过既有前缀处理。

**Further Considerations**

1. 知识库选择建议先做知识库级别，再决定是否暴露文档级细分。Option A: 首版只选 knowledge_scopes，复杂度最低。 Option B: 同时支持 knowledge_docs，需再加二级 picker。推荐 Option A，文档级放第二步。
2. 附件能力建议先按文件大类控制可选扩展名。Option A: image/audio/video/document 四类映射到扩展名白名单。 Option B: 直接完全信任后端。推荐 Option A，能在本地更早给出反馈。
