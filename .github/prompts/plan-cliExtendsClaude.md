## Plan: CLI 本地工具对标补齐

基于对标仓库的 CLI 工具注册表与工具实现目录，以及当前 taiyiflow-cli 的本地工具实现，建议不要追求“全量工具数一致”，而是按代码任务闭环优先补齐 4 类核心能力：结构化命令外壳（Shell）工具、任务态工具、代码语义导航工具、细粒度编辑工具。首轮推荐以 BashTool + TaskOutput/Stop + FileEdit + LSP + Todo/Task 为主线，Glob/TerminalCapture/NotebookEdit 作为第二梯队，明确排除 Web、Computer Use、桌面端、IM、远程触发、多 Agent 编排等非首版本地 CLI 范围。补充约束：凡是 CLI 对标补齐过程中发现现有太乙智启后台 stream 接口、请求 schema、事件模型或客户端回调协议不足以承载新能力的，必须同步把后台改造纳入同一实施批次，不能只在 CLI 侧做兼容性绕行。

claude-code-haha的源码在「/Users/neeker/工作/技术开发平台/人工智能平台/开发平台/claude/claude-code-haha」目录。

**Steps**

1. 对标基线冻结：把对标仓库中的工具按“本地代码任务必需 / 本地增强 / 非本地或超范围”三类分组，形成本仓可执行的最小对标清单。这里要明确当前 taiyiflow-cli 已有 search_files、read_file、list_dir、write_file、Skill 与 built_in_command，但缺少正式的结构化 Shell 工具、任务输出读取、LSP、Todo/Task、细粒度 file_edit。这个步骤阻塞后续所有设计。
2. 协议差距审计：逐项确认 CLI 新工具是否仅靠客户端本地实现即可落地，还是会触及太乙智启后台的 stream 入口、请求 schema、事件类型、客户端回调载荷或会话字段。如果存在协议缺口，就把 CLI 改造与后台改造绑定为同一批次需求，不允许留“CLI 先本地兜底、后台以后再补”的技术债。重点检查 BashTool 后台任务、任务输出回传、LSP 结果结构、Todo/Task 状态透传是否需要新增字段或事件。依赖步骤 1，阻塞步骤 3-9 的接口设计。
3. 第一阶段先补 BashTool 族能力：把当前 built_in_command 的 run_command_via_client 收敛为正式本地工具，补齐结构化参数、描述字段、超时、后台运行、输出持久化、任务停止与任务结果读取。需要同时决定是复用现有命令执行核心，还是抽象出统一的本地任务执行器；若现有客户端回调协议无法承载后台任务句柄、增量输出或结果分页，就同步修改后台 stream 协议和 schema。依赖步骤 1-2。
4. 第一阶段并行补任务态工具：增加 session 级 Todo 工具，至少支持创建或更新当前任务清单；如果希望更接近 claude-code-haha，再补 TaskCreate、TaskList、TaskUpdate、TaskOutput、TaskStop 的轻量版。TUI 需要能显示当前任务摘要与后台命令状态；若这些状态需要由后台参与记账或透传，也应在同批次改造后台。可与步骤 3 并行，但共享任务状态模型并依赖步骤 2 的协议结论。
5. 第二阶段补代码理解工具：新增 LSP 工具，支持 definition、references、implementation、documentSymbol、workspaceSymbol 等操作，避免继续把所有代码理解都压到 grep 搜索。与此同时评估是否把 search_files 拆分为 grep + glob 两个工具；若保守演进，可先保留 search_files，再新增 glob_files 作为查找文件名的专用工具。若 LSP 结果需要在后端统一建模、裁剪或审计，同步扩展后端请求或事件 schema。依赖步骤 1-2，可在步骤 3 完成后启动。
6. 第二阶段补编辑人体工学：在 write_file 之外增加 file_edit 工具，采用 old_string、new_string、replace_all 这类更适合模型使用的输入模式，并补“先读后写”“读取后文件变更校验”“差异摘要”这类保护。NotebookEdit 不作为首批必做，但如果目标场景包含 Jupyter，再进入同阶段；若需要后端识别新的工具类型或审批语义，同步改后台。依赖步骤 1-2，建议在步骤 5 同期设计。
7. 第三阶段补交互型辅助工具：增加 ask_user_question 或等价的结构化澄清工具，避免只能靠自然语言追问；若 BashTool 支持后台任务，再增加 terminal_capture 或等价工具读取前台终端输出。涉及新增交互事件时，同步补后台 stream 事件定义与序列化。依赖步骤 3 和步骤 4。
8. 统一安全与治理：所有新增工具都要接入 approval_mode、sandbox_roots、敏感路径拒绝、审计日志与 TUI 确认流；Shell 相关工具还要统一危险命令拦截、超时、输出截断、后台任务生命周期治理。若安全策略需要后端参与校验、记录或回显，也纳入后台同步改造。贯穿步骤 3-7。
9. 规格与提示词同步：更新规格文档中的“首期 / 第二阶段工具清单”和运行时系统提示，同时补充 CLI/后台协议变更说明，确保模型知道何时优先用 BashTool、LSP、Todo、FileEdit，而不是继续滥用 write_file 或 search_files。依赖步骤 2-8。

**Relevant files**

- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go — 当前本地工具定义、分发、权限预览与大部分文件、Git、Skill 工具实现的主入口；补齐新工具时应优先复用这里的 schema 注册、确认逻辑与审计模式。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go — 当前 built_in_command 的命令执行入口；BashTool 与后台任务能力应从这里抽象或下沉，避免形成两套命令执行路径。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/security_helpers.go — 路径边界、sandbox escape、敏感目录拒绝的安全基线；新增工具必须复用这里的边界判断。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — 当前 TUI 的工具确认与结果展示入口；Todo、后台任务、LSP、FileEdit 的状态与结果展示都需要在这里落地。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_capability.go — 本地能力与 request mode 的开关控制；新增工具后需要重新确认哪些模式可见、哪些模式只读。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/specs/taiyiflow-go-client.md — 当前产品与架构基线，需要把“对标 Claude Code 的本地工具补齐顺序”反映到阶段规划中。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go — 当前本地工具测试入口；新增工具和权限边界都应在这里或相邻测试文件补齐。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/endpoints.py — 后台 stream 入口与 X-TaiyiFlow-Session-id 响应头基线；若 CLI 新能力需要新增交互事件或回调语义，应在这里同步调整。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/schemas.py — SimplifiedAPIRequest、StreamData、available_tools 与客户端上下文字段的 schema 真值来源；协议扩展要先落这里。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/processing/process.py — 客户端工具与可用工具归并、流式处理与会话上下文拼装的处理层；CLI 与后台协同时需要在这里补处理逻辑。

**Verification**

1. 先做工具矩阵核对：列出“当前已有 / 对标必补 / 明确延后”的工具清单，确认没有把 Web、Computer Use、IM、桌面端等超范围能力混入首版。
2. 先做 CLI 与后台协议核对：逐项确认新增工具是否需要扩展 SimplifiedAPIRequest、StreamData、client_mcp_servers、客户端回调载荷或 stream 事件；一旦需要，就在 CLI 和后台同时建变更清单。
3. 对每类新增工具补最小可执行测试：文件编辑工具测试读后写保护与 replace_all；Shell 或 Task 工具测试超时、后台运行、结果读取与停止；LSP 工具至少验证 schema 与无服务时的错误返回。
4. 对 CLI 与后台做一轮联调回归：确认 stream 事件、会话头 X-TaiyiFlow-Session-id、客户端工具回调与新增字段在真实后端上可闭环。
5. 对 TUI 做一次交互回归：确认新增工具在审批模式、结果摘要、错误展示、后台任务状态上都有稳定输出。
6. 更新规格文档后，回看是否仍符合“单入口主命令 + 少量辅助命令 + 首版不追求全量抽象”的基线。

**Decisions**

- 包含范围：仅比较 CLI 本地工具层，重点看代码任务闭环，不把对标仓库的桌面端、Computer Use、Channel、远程触发、Web 搜索、远端 MCP 资源、多 Agent 团队协作纳入首轮对标要求。
- 后台同步原则：如果 CLI 新工具落地依赖太乙智启后台扩展 stream 协议、请求 schema、事件模型、会话字段或客户端回调协议，则 CLI 与后台必须同批次设计、实现和联调，不接受单边兼容绕行。
- 当前最核心的 5 个缺口：正式 BashTool、任务输出或后台任务管理、Todo 或 Task、LSP、FileEdit。
- 次优先缺口：独立 Glob 工具、TerminalCapture、NotebookEdit、结构化 ask_user_question。
- search_files 现状能覆盖部分 grep 或 glob 场景，因此“拆成 grep + glob”不是绝对阻塞项；但 LSP 与 BashTool 不能继续由现有能力替代。

**Further Considerations**

1. BashTool 设计建议优先复用现有 runCommand 核心，再补任务状态与输出读取，避免先重写执行层。选项 A：内聚在现有 runtime。选项 B：抽成独立本地任务执行器。推荐 A。
2. Todo 与 Task 二选一时，建议先做 Todo 的轻量闭环，再看是否需要升级为完整 Task CRUD。选项 A：先 Todo。选项 B：直接 Task 系统。推荐 A。
3. NotebookEdit 只有在明确支持数据分析或 Jupyter 场景时才进入近期计划，否则会稀释首版代码任务闭环。推荐延后。
