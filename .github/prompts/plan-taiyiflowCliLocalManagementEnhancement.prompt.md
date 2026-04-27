## Plan: taiyiflow-cli 本地管理增强

围绕现有 taiyiflow-cli 的 slash 命令、TUI 状态机、本地 runtime 与 skills 目录机制，分三阶段补齐五项能力：新增基于 mirror-cn.clawhub.com 的远程技能仓库管理命令，补充手动思考模式切换命令与配置表单字段，统一 shell 默认工作目录为当前工作区目录，升级 /tasks 为可操作的任务管理弹窗，并将 /permissions 并入 /config 表单形成可编辑的权限管理视图。方案尽量复用现有本地命令分发、ConfigForm、SkillsPicker、ToolPanel 与 task/permission 持久化逻辑，不改太乙智启后端协议。

**Steps**
1. 阶段一：补齐配置模型与命令入口。扩展 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go 的 Config 与 Skills 配置，新增远程技能源配置字段与思考模式默认字段；同步扩展 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 中 ConfigFormValues/ConfigFormState 所需字段与 slash command 提示；在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 和 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 中新增 /reasoning、扩展 /skills 子命令提示与 /config 表单读写。此步骤阻塞后续所有实现。
2. 阶段二：实现远程技能仓库管理。新增或扩展 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/skills.go 附近的 remote registry 能力，建议拆分为新的内部文件承载：远程 spec 解析、搜索/详情 API 调用、下载解压、origin 元数据、update 计算。默认源指向 https://mirror-cn.clawhub.com，并允许通过配置覆盖。命令面在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_commands.go 中扩展 /skills search、/skills view-remote、/skills install、/skills remove、/skills update；TUI 面在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 中让现有 /skills picker 在安装/删除/更新后可刷新。此步骤依赖步骤 1。
3. 阶段二补充：远程技能安装需要复用现有本地技能目录优先级。安装目标优先落到工作区目录 <workdir>/.snz1dp/skills，若无工作区则落到 home skills 目录；每个远程安装技能写入来源元数据文件，记录 registry、slug、version、installed_at 等，用于后续 update 与 remove。删除仅删除远程安装目录，不影响 bundled skills。该子步骤与步骤 2 串行。
4. 阶段三：接入思考模式切换。基于 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go 和 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go 已有的 ReasoningEnabled/Reasoning 字段，在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go 与 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 的 StreamRequest 组装处接入手动 reasoning 开关；slash 命令建议支持 /reasoning on、/reasoning off、/reasoning status，配置表单提供默认值字段，运行时 slash 状态优先于默认配置。此步骤依赖步骤 1，但可与步骤 2 并行实施。
5. 阶段三补充：统一 shell 默认目录。核对 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 的 resolveLocalWorkdir、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go 的 resolveBaseDirectory 与 shell 参数归一化逻辑，确保未显式传 workingDirectory 时一律采用当前工作区目录，而不是被其它 session/context 字段意外覆盖；同时保证后台 task 元数据中的 cwd 与 UI 展示同步。此步骤可与步骤 4 并行。
6. 阶段四：把 /tasks 从只读 panel 升级为管理弹窗。在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go 中新增 TaskPickerState 与对应键盘处理逻辑，风格上复用 SessionPicker/SkillsPicker；在 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 中把 /tasks 默认行为改为打开任务列表弹窗，Enter 进入操作菜单，至少支持查看输出、停止、删除、刷新。底层动作继续调用 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_commands.go 与 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/task_store.go 的现有 task_* 能力。此步骤依赖步骤 1，可与步骤 4/5 并行，但与步骤 7 共用 Config/TUI 状态扩展方式，建议连续完成。
7. 阶段四补充：把 /permissions 管理并入 /config 表单。扩展现有 ConfigForm 为多分区表单，新增 approval、sandbox、roots、permission rules 的编辑能力；保留 /permissions show/rule/set/clear 命令兼容，但在 TUI 中输入 /permissions 时直接跳转到 /config 的权限分区或高亮权限区域。提交时复用 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go 中 applyConfigFormSubmission，并补一个规则列表增删改的提交结构，必要时把 permissions 的增删逻辑从 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_commands.go 提炼成可复用函数。此步骤依赖步骤 1。
8. 阶段五：补齐测试与文档。扩展 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_commands_test.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/skills_test.go，分别覆盖远程技能命令、reasoning 开关透传、shell cwd 默认值、/tasks 交互状态与权限表单提交；如 README 或 specs 中已有命令说明，补充新增 slash 命令与配置项。此步骤依赖全部实现完成。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — TUI 本地 slash 命令分发、ConfigForm 构建与提交、ToolPanel 渲染；是 /skills、/tasks、/permissions、/reasoning 的主要接入点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_commands.go — 现有 /tasks 与 /permissions 命令解析、task_* 调用与 panel 构建入口；适合继续扩展 /skills 远程子命令与可复用权限操作。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go — 交互式 CLI 非 TUI 路径与 StreamRequest 组装；需要同步 reasoning、skills 命令与 cwd 行为。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go — 配置结构、默认值、序列化入口；需要新增 skills source 与 reasoning 默认配置。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — TUI Snapshot、ConfigForm、slash command 提示与 picker 状态机；需要新增 TaskPickerState、扩展 ConfigForm 字段。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/skills.go — 本地技能发现、目录优先级、加载与客户端透传；远程安装后仍需接入这里的发现链路。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go — shell_exec 工作目录归一化与命令执行核心；需要验证默认 cwd 行为。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/task_store.go — 任务元数据与输出持久化；任务管理弹窗应直接消费这里暴露的状态字段。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go — StreamRequest.ReasoningEnabled 的真值定义。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go — reasoning 到后端 JSON 字段的映射实现。
- /Users/neeker/工作/技术开发平台/人工智能平台/开放龙虾/openclaw/src/infra/clawhub.ts — 远程 registry 请求、认证与 base URL 解析参考。
- /Users/neeker/工作/技术开发平台/人工智能平台/开放龙虾/openclaw/src/agents/skills-clawhub.ts — 技能下载、安装与 origin 元数据参考。
- /Users/neeker/工作/技术开发平台/人工智能平台/开放龙虾/openclaw/src/cli/skills-cli.ts — search/install/update 命令组织参考。

**Verification**
1. 运行 taiyiflow-cli 相关单测，至少覆盖 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd 与 /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills、/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime 的新增测试。
2. 运行工作区任务 test tui package，确认 TUI 状态机改动没有破坏现有交互。
3. 运行工作区任务 build taiyiflow-cli，确认命令与配置结构调整后的编译通过。
4. 手动验证：/skills search、/skills view-remote、/skills install、/skills remove、/skills update 至 mirror-cn.clawhub.com；安装后 /skills 列表能刷新并展示来源。
5. 手动验证：/reasoning on、/reasoning off、/reasoning status 与 /config 默认值在新会话和已存在会话中的优先级是否符合预期。
6. 手动验证：未显式指定 workingDirectory 的 ! 命令和 shell_exec 工具是否都落在当前工作区目录；后台任务的 cwd 展示是否一致。
7. 手动验证：/tasks 弹窗支持选择任务并执行停止、删除、查看输出、刷新；/config 中权限分区可编辑 approval、sandbox、roots 与规则列表，并落盘到配置文件。

**Decisions**
- 本轮技能仓库默认使用 https://mirror-cn.clawhub.com，且保留源配置项，便于后续切回官方或企业镜像。
- 思考模式按你的最新确认只做手动切换，不做自动规则；配置仅保存默认值，slash 命令负责运行时覆盖。
- /tasks 的管理模式采用“列表 + Enter 打开操作菜单”；/permissions 不做独立弹窗，而是并入现有 /config 表单。
- shell 默认目录按“当前工作区目录优先”实现，不把 session context_dir 作为 shell 默认 cwd 的覆盖源，除非用户显式传 workingDirectory。
- 本轮不涉及太乙智启后端 schema 变更；reasoning 仅使用现有 request.reasoning 字段透传。

**Further Considerations**
1. 远程技能 API 协议需在实现前对 openclaw 当前镜像站接口再做一次实读确认，重点核对搜索、详情、下载地址与认证是否与 upstream 保持一致。
2. 权限规则表单若要支持复杂编辑，建议采用“规则列表 + 新增/删除行”的轻量状态，而不是一次引入通用表格组件，避免把 Bubble Tea 状态机改得过重。
3. /skills update 是否支持 --all 可以作为首版附带项，但若接口联调复杂，可先落单技能 update，批量更新放第二次迭代。