## Plan: 服务端配置与能力对齐

把 taiyiflow-cli 的“默认流程（default flow）、知识库、模型上下文协议工具（MCP tools）、上传约束、语音能力”统一收敛到 provider/cmd 层：新增服务端配置接口 GET /config 的读取，扩展 metadata feature 归一化，按“本地配置 > 服务端 /config.default_flow_id > flows 列表首项”的顺序解析默认流程；终端用户界面（TUI）只消费归一化后的可渲染状态，不直接理解后端原始 JSON。后端源码当前已具备 /config 与 support_knowledge/support_mcp_tools 语义，本轮以 CLI 适配落地为主，不主动改 backend。

**Steps**

1. 阶段一：在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go 定义 ServiceConfig、ServiceFeatureFlags 及相关系统级配置结构，并扩展 Provider 接口增加 GetServiceConfig(ctx)。字段至少覆盖 max_file_size_upload、doc_exts、milvus_enabled、default_flow_id、feature_flags.text_to_speech、feature_flags.speech_to_text；同时扩展 ChatCapabilities，显式支持 support_knowledge、support_mcp_tools，并保留 unknown features。此步骤阻塞后续所有消费改造。
2. 阶段一：在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go 实现 GET /config 与 metadata 新 feature 归一化，沿用现有 API 前缀归一化与 httpClient。GetMetadata 继续保留原始 Features，同时把 support_knowledge、support_mcp_tools 写入归一化能力；GetServiceConfig 对缺失字段做零值降级，不让 /config 失败阻断主流程。此步骤依赖步骤 1。
3. 阶段二：调整 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/default_flow.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 的默认流程与快照装配逻辑。默认流程优先级改为：命令行显式 flow > 本地 config.default_flow > 服务端 /config.default_flow_id > 现有 ListFlows 首项兜底。buildTUISnapshot 与 buildInitialTUISnapshot 在读取 metadata 的同时补读 service config，并把系统级约束与 flow 级能力合并成 Snapshot；知识库入口的判定以 metadata.support_knowledge 为主，并对旧后端继续兼容 knowledge_search；知识库列表请求只在能力开启时触发。此步骤依赖步骤 2。
4. 阶段二：扩展 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 的 Snapshot，仅承载界面会消费的系统状态，例如上传大小上限、允许文档扩展名、知识库系统可用性、MCP 工具支持、文本转语音（TTS）支持、语音识别（STT）支持。若当前没有成熟的上传或语音交互入口，本轮至少把这些状态接入现有 /files 状态文本或等价摘要输出，避免 provider 新字段成为死数据。此步骤依赖步骤 3。
5. 阶段三：补齐测试与回归。更新 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client_test.go，覆盖 /config 解析、feature_flags、support_knowledge、support_mcp_tools 和缺失字段降级；更新 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go 与 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go，覆盖默认流程优先级、knowledge 支持判定、知识库列表是否按条件拉取、Snapshot 中系统配置映射；如 Snapshot 有新增渲染字段，再补 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go 的状态文本或帮助文案测试。此步骤依赖步骤 3，可与步骤 4 的渲染测试并行。

**Relevant files**

- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go — 新增 ServiceConfig / ServiceFeatureFlags 类型，扩展 ChatCapabilities 与 Provider 接口，是数据边界的主落点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go — 新增 /config client 方法，扩展 metadata feature canonical 映射，是协议适配的主落点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/default_flow.go — 把 default_flow_id 纳入默认流程选择优先级，替换“直接取第一个 flow”的现状。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go — 交互入口与非 TUI 流程需要共用新的默认流程解析。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — buildInitialTUISnapshot、buildTUISnapshot、applyMetadataToTUISnapshot、renderTUIChatFilesStatus 等位置负责把 service config + metadata 合成为 TUI 状态。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — Snapshot 结构与 /files 等本地命令文案的消费边界。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client_test.go — provider HTTP stub 测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go — snapshot 映射与知识库条件加载测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go — 默认流程回退顺序测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/endpoints.py — /config 与 metadata features 的真值来源，仅作联调参考。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/schemas.py — ConfigResponse 与 FlowMetaResponse 的真值来源，仅作联调参考。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/services/settings/feature_flags.py — feature_flags.text_to_speech 与 feature_flags.speech_to_text 的字段真值来源，仅作联调参考。

**Verification**

1. 在 taiyiflow-cli 运行 go test ./internal/provider/taiyiflow ./cmd ./internal/tui，重点观察 /config 解析、默认流程优先级、knowledge 能力判定相关用例。
2. 用 HTTP stub 场景分别验证四类返回：完整 /config；缺失 feature_flags；metadata 只含 knowledge_search；metadata 含 support_knowledge + support_mcp_tools，确认 CLI 兼容老新两类后端。
3. 手动验证未配置本地 default_flow 时，CLI/TUI 会优先落到服务端 default_flow_id；只有 /config 缺失时才退回首个 flow。
4. 手动验证 knowledge 相关 UI/命令只在支持时出现，且 knowledge bases 只在支持时加载；support_mcp_tools 至少能进入状态摘要或内部 Snapshot，而不会被丢失。
5. 手动验证 /files 或等价状态输出能反映 max_file_size_upload 与 doc_exts；若本轮未做上传入口，不对真实文件选择做拦截测试。

**Decisions**

- 本轮以 taiyiflow-cli 适配为主，不主动修改 taiyiflow backend；若联调发现部署环境 /config 返回与源码不一致，再单独开后端修正。
- knowledge 能力判定以 metadata 的 support_knowledge 为主，并兼容旧值 knowledge_search，避免新旧后端切换时 UI 退化。
- support_mcp_tools 先进入归一化能力模型与状态摘要，不在本轮新增独立 MCP 开关命令；运行时 client_mcp_servers 透传机制保持不变。
- max_file_size_upload、doc_exts、text_to_speech、speech_to_text 先做“取值 + 状态透传 + 摘要展示/后续校验预留”，不在本轮发明新的上传或语音交互协议。
- 本地 config.default_flow 仍保留最高的持久化优先级，服务端 default_flow_id 只作为未配置时的回退值，不覆盖用户本地显式设置。
