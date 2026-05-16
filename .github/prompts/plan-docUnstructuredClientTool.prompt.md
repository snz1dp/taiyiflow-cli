## Plan: 文档结构化客户端工具

在 taiyiflow-cli 内新增一个 provider-backed client_tool，用模型显式调用的方式完成“本地文件 -> 上传 -> 调用 doc_unstructured_flow_id -> 回调结果”闭环。推荐本轮不自动改写现有 --file、/upload、InputFiles 主流程；只通过全局扩展名配置和 tweak 提示控制模型何时调用该工具，这样改动集中在 provider、runtime、配置与状态摘要四处，验证面最小。

**Steps**
1. Phase 1: Provider 协议与最小 API 封装。修改 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go`，新增文档结构化调用所需的窄类型与 Provider 方法；优先暴露“文档结构化专用方法”，避免把 CLI 过早抽象成完全通用的非流式 run 层。`/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go` 内部可复用私有通用 helper，但对外只暴露本次需求需要的能力。请求体继续沿用现有 `streamRequestBody` 同构字段：`input_value`、`input_type`、`files`、`session_id`、`output_type`。此步骤阻塞后续实现。
2. Phase 2: Runtime 执行链支持 provider-backed client_tool。修改 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go`，新增带 provider 句柄的执行入口，推荐保留现有 `ExecuteClientToolPayload` 并新增 `ExecuteClientToolPayloadWithProvider(...)`，避免无关测试和本地命令调用全部跟着改签名。`HandleClientToolEvent`、CLI 流处理和 TUI 流处理切换到 provider-aware 入口；本地只读 slash 命令仍可走旧入口。此步骤依赖 1。
3. Phase 3: 实现文档结构化工具本体。新增或拆分到 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/` 下的文档工具实现文件，完成以下逻辑：从 payload 上下文补齐 `flow_id`/`session_id`；加载服务端 `/config` 并要求 `DocUnstructuredFlowID` 非空；读取本地全局扩展名配置并仅允许命中扩展名的文件进入流程；复用现有上传语义解析本地路径并逐个调用 `Provider.UploadFile`；使用新 provider 方法调用 `doc_unstructured_flow_id`；将返回结果归一化为稳定、JSON 可序列化的对象，例如 `count`、`items`、`session_id`、可选 `raw_outputs`。因为后端 `/agents/client/call/result` 会通过 `extract_mcp_call_result(...)` 把结果转成字符串，结果对象必须结构稳定、便于模型从 JSON 字符串继续消费。此步骤依赖 1-2。
4. Phase 4: 注册工具、模式放行与审批。修改 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go`，新增工具定义、canonical name、输入 schema 与预览文案；推荐工具只在 `agent` 模式暴露，不在 `ask`/`plan` 放开。同步修改 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_capability.go` 与相关确认逻辑，把该工具视为会上传本地文件内容到服务端的变更型工具，纳入现有 approval/permission 体系，而不是按只读工具放行。此步骤依赖 2-3。
5. Phase 5: 全局配置与模型提示。修改 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go` 和 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go`，新增一个按扩展名生效的全局配置键，推荐以 YAML 数组存储、以 `config set` 的逗号分隔字符串录入，并统一做 lower-case、去前导点、去重归一化。随后同步 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go`、`/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go` 的 ConfigFormValues、字段 key 映射、build/apply 提交流程。最后扩展 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go` 的 tweak 注入内容，让模型能在系统提示里看到“哪些扩展名应优先调用文档结构化工具”。此步骤在 2 的方法形状确定后可与 4 并行。
6. Phase 6: 共享 helper 与状态一致性。把 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go` 里的上传路径解析/上传复用点抽到可被 runtime 和现有命令共同使用的位置，避免 `--file`、`/upload` 与新工具各自维护路径归一化逻辑。同步更新 `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go` 的文件状态摘要，让用户看到服务端支持扩展名、已配置走文档结构化的扩展名，以及 `DocUnstructuredFlowID` 是否可用。此步骤依赖 3 和 5。
7. Phase 7: 测试与验证。补齐 provider、runtime、config、cmd/TUI 的单元测试，并以窄包测试为主验证实现。此步骤依赖 1-6。

**Relevant files**
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go` — 新增专用请求/响应类型与 Provider 方法签名。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go` — 实现上传后调用 `POST /run/{doc_unstructured_flow_id}` 的非流式方法，并解析 `session_id`/`outputs`。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client_test.go` — 覆盖新请求体、响应解析和错误分支。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go` — 增加 provider-aware client_tool 执行入口，并让 stream 事件链路走新入口。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go` — 注册工具定义、模式过滤、确认预览、变更型工具识别。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go` — 新工具定义、模式暴露、参数校验与执行 happy path/failure path。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go` — 新增扩展名配置字段与归一化 helper。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config_test.go` — 覆盖扩展名列表的 parse/normalize/reset 行为。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go` — `config set/show/unset` 接入新键。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go` — 复用上传 helper、扩展 tweak 注入提示。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go` — TUI ConfigForm 构建/提交、新状态摘要、必要的 snapshot 字段映射。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go` — ConfigFormValues、字段 key 映射与 snapshot 承载新增展示字段。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go` — Provider stub 若接口变更需同步；必要时补工具链路周边测试。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go` — 配置表单、文件状态摘要和 snapshot 映射测试。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/schemas.py` — 参考 `SimplifiedAPIRequest`、`RunResponse`、`MCPClientCallResult` 的真实协议形状。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/endpoints.py` — 参考 `POST /run/{flow_id_or_name}` 与 `/config` 的后端入口。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/agent_client.py` — 参考客户端回调结果如何落库与通知工具服务。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/utils/mcp.py` — 参考 client tool 回调结果会如何被字符串化。

**Verification**
1. 运行 `go test ./internal/provider/taiyiflow`，验证新非流式 run 封装的请求体与响应解析。
2. 运行 `go test ./internal/runtime`，验证工具定义、参数校验、provider-backed 执行和回调结果形状。
3. 运行 `go test ./cmd`，验证 config set/show/unset、TUI 入口、模式放行与状态摘要。
4. 如果修改了表单模型或 snapshot，补跑 `go test ./internal/tui`。
5. 手工验证：配置允许扩展名后，在 agent 模式让模型对本地 PDF/DOCX 发起工具调用；确认会先审批、再上传、再命中 `doc_unstructured_flow_id`，最终在工具轨迹中看到结构化文本结果回到服务端。

**Decisions**
- 本轮范围包含：显式 client_tool、服务端文档结构化调用、全局扩展名配置、模型提示和用户可见状态。
- 本轮范围不包含：自动改写现有 `--file`、`/upload`、`InputFiles` 主流程；如果后续需要“附件命中扩展名自动改走服务端结构化”，建议在当前共享 helper 落地后单独做第二阶段。
- 新工具仅在 `agent` 模式暴露；`ask`/`plan` 不自动放开，因为它会把本地文件内容上传到服务端。
- Provider 对外保持窄接口；内部允许复用通用 POST helper，但不把 CLI 这轮需求扩大成完全通用的非流式后端抽象层。
- 工具返回值以稳定 JSON 对象为准，而不是拼接自由文本；虽然服务端最终会把结果字符串化，但稳定 JSON 更利于模型继续解析。
