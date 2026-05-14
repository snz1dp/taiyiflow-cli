## Plan: CLI 上传与思考检索流体验

围绕现有太乙智启后台协议做 CLI 侧增量改造，不新增顶层 upload 子命令，也不改后端 schema。先补齐 provider 的上传、历史元数据与检索来源识别能力，再接到单次 task、交互式 slash 命令和 TUI 渲染；流内负责上传前置、实时思考块、检索中状态和生成中动画，检索结果与历史思考详情依赖消息落库后的会话回刷。

**Steps**
1. Phase 1：补齐 provider 数据面。扩展 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go 中的 Provider、SessionMessage，并新增 CLI 侧 SearchResult DTO；在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go 中实现基于现有 POST /upload/{flow} 的 UploadFile，扩展 sessionMessageResponse 与 ListSessionMessages 以透传 reason_text、search_results、reasoning_enabled、search_keyword 等历史元数据，同时保留 tool_call 事件中的 name 以便后续区分 internet_search_context、knowledge_search_context、long_term_memory_search。此步阻塞 2-6。
2. Phase 2：接入命令入口。命令行单次任务继续走 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 中的 task 入口，新增单文件参数，发送流请求前先上传本地文件，拿到后端返回的 file_path 再注入 StreamRequest.InputFiles。交互式会话与 TUI 同步移除 /files，改为 /upload；/upload 无参时展示当前附件与上传限制，有参时上传单文件并写回当前会话快照。此步依赖 1；root 与 tui 部分可并行。
3. Phase 3：补齐历史与实时思考链路。扩展 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 的 loadRecentSessionHistory 和 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 的 Snapshot、HistoryEntry、buildInitialEntries、renderEntry，使历史 assistant 消息仅显示“含思考”提示而不展开具体 reason_text；当前流式响应则继续利用现有 think 标签分段，在 TUI 中新增独立高亮的 thinking 区块并实时刷新。同步在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 和 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 添加 /think 命令，支持 latest 或消息序号查看历史思考详情；TUI 优先通过结果面板或覆盖层展示，避免把完整思考正文回灌到普通历史消息流。此步依赖 1；root 与 tui 可并行。
4. Phase 4：补齐检索状态与引用转换。基于 Phase 1 透传的 tool_call name，在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 的 applyEvent、renderTransientStatus、renderStatusBar 中新增“联网搜索中”“知识检索中”“历史记忆检索中”状态；由于流里当前没有结构化 search_results 事件，检索完成后的结果展示应通过 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 在当前轮结束后回刷最后一条消息元数据，再把 search_results 交给 TUI 和 CLI 渲染。引用转换按 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/services/mcp_tool/retrieval_context.py 的规则实现：1.x=联网、2.x=共享知识、3.x=个人知识；共享与个人的细分以 search_results.source 为准，因为知识检索开始态只能拿到统一的 knowledge_search_context。此步依赖 1；TUI 与 CLI 渲染可并行。
5. Phase 5：补齐生成中动画。为 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 和 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 增加轻量周期 tick 与最近流活动时间跟踪，在请求活跃且流未 close、error、idle timeout 时，为当前输出尾部追加动态 ...；一旦收到 close、error、idle timeout 或当前轮完成，立即清除动画。此步依赖 2-4 中的流状态建模；TUI 与 CLI 可并行。
6. Phase 6：测试与联调。先在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client_test.go 覆盖上传请求、消息历史 reason_text 与 search_results 解析、tool_call name 透传；再在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go 增补 /upload、/think、历史思考提示、检索状态、引用转换和动态 ... 回归；最后用 go test ./internal/provider/taiyiflow、go test ./internal/tui、go test ./cmd、go build -o taiyiflow-cli . 做收口验证。此步依赖 1-5。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go — 扩展 Provider、SessionMessage、SearchResult 等 CLI 侧协议模型。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go — 实现 UploadFile，扩展 ListSessionMessages，保留 tool_call name，并继续复用 streamRequestBody.Files。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go — task 文件上传入口、交互式 /upload 与 /think、CLI 思考与引用输出、动态 ... 输出。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — /files 到 /upload 的命令分发替换、/think 命令、上传前置、历史回刷和 stream event 到 TUI Event 的映射。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — Snapshot 和 HistoryEntry 扩展、thinking 高亮块、检索状态、引用渲染、tick 动画。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client_test.go — provider 适配层协议回归。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go — 单次任务与交互式 CLI 回归。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go — slash 命令、帮助文案、会话回刷回归。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go — 渲染、状态栏、历史与实时 thinking、动画回归。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/agent_file.py — 上传协议真值来源，默认不计划改动。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/schemas.py — files、reasoning、upload response schema 真值来源，默认不计划改动。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/schema/message.py — reason_text、search_results 等历史字段真值来源，默认不计划改动。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/services/mcp_tool/retrieval_context.py — 1.x、2.x、3.x 引用规则真值来源，默认不计划改动。

**Verification**
1. 在 provider 层用 httptest 覆盖上传成功、上传失败、消息历史解析 reason_text 与 search_results、tool_call name 保留和 streamRequestBody.Files 回归。
2. 在 cmd 与 TUI 层验证 /files 帮助文案完全替换为 /upload，/upload 无参查看附件，有参上传后能进入下一次请求的 InputFiles。
3. 验证当前轮 think 内容在流式过程中单独高亮且持续刷新，历史消息仅显示“含思考”提示，/think latest 与 /think 指定序号都能查看详情。
4. 验证检索中状态能区分联网搜索、知识检索、历史记忆检索；检索完成后基于回刷到的 search_results 展示结果与引用转换，1.x、2.x、3.x 与 source 字段一致。
5. 验证动态 ... 仅在请求活跃时出现，close、error、idle timeout、用户中断和正常完成后都能立即消失。
6. 执行 go test ./internal/provider/taiyiflow、go test ./internal/tui、go test ./cmd、go build -o taiyiflow-cli . 作为最终收口。

**Decisions**
- 单次命令入口保持在 task，采用 task 参数接入单文件上传，不新增顶层 upload 子命令。
- 会话内命令将 /files 替换为 /upload，历史思考查看命令采用 /think。
- 搜索状态覆盖全部检索来源，但开始态只保证区分联网搜索、知识检索、历史记忆检索；共享与个人知识的细分放在 search_results 返回后完成。
- 不重复规划 split_assistant_message；当前能力已存在，本轮只在其基础上补实时 thinking、历史 thinking 与检索展示。
- 默认不改 sibling 后端协议；若实现中没有发现联调缺口，则 agent_file.py、schemas.py、message.py、retrieval_context.py 仅作为真值来源。

**Further Considerations**
1. 当前 sibling 后端在 process.py 中会在请求包含 input_files 时关闭 internet_search；如果后续要支持“上传文件 + 联网搜索并存”，那是额外的后端需求，不在当前默认实现范围内。
2. 由于流里没有结构化 search_results 事件，“搜索完成后立即显示结果”将依赖当前轮结束后的消息回刷，而不是单纯依靠 SSE 文本事件。