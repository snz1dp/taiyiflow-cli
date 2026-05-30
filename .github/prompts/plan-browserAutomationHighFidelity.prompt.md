以“Go CLI + TypeScript Chrome 扩展”的双栈方案复刻参考仓库的浏览器自动化链路：默认接入 Chrome 当前用户会话，通过 Native Messaging 连接扩展；同时保留一个独立自动化浏览器实例模式，并通过统一工具协议与配置切换共享同一套技能与运行时入口。推荐首期先打通 Chrome 会话链路、7 项核心浏览器能力、配置/诊断/权限闭环，再补独立实例适配与更高阶任务循环。

**Steps**
1. Phase A: 范围与对标基线重置。把当前仓库里“taiyiflow-in-chrome 暂不复刻/裁剪实现”的历史判断改成正式实施项，冻结首批范围为：导航/打开页面、点击与输入、截图、读取页面文本或 DOM、读取控制台日志、多标签页管理、长任务或后台运行。明确不纳入首期的内容：参考仓库里的远程 bridge、analytics、ant-only browser_task/lightning agent loop、桌面级操作。这个步骤阻塞后续协议设计与文档更新。
2. Phase B: 配置模型与进程入口设计。扩展配置模型，新增 browser 自动化配置段，至少包含 mode（默认 chrome_session，可切到 isolated）、permission_mode、browser_path 或 channel、profile_dir、connect_timeout、extension_setup 或 manifest_install、socket_dir 等字段；同时在 CLI 主入口增加隐藏运行模式，用于启动 Native Messaging host 与本地 browser MCP/bridge 服务。该步骤依赖 1，阻塞后续 CLI 启动与诊断闭环。
3. Phase C: 工具契约与命名冻结。基于当前内置技能文案，冻结本仓库的浏览器工具族命名，建议直接使用 taiyiflow-in-chrome__* 前缀以兼容现有技能提示；把每个工具明确分为读操作和变更操作，并给出统一的参数与返回约定。首批工具建议至少覆盖：tabs_context、tabs_create、navigate、click、type、screenshot、read_dom 或 read_text、read_console，以及一个面向长时流程的后台任务包装。该步骤依赖 1，可与 2 并行推进细节，但在 4 和 5 开始前必须冻结。
4. Phase D: Go 侧浏览器桥接层。新增一个专门的桥接层目录承载 Native Messaging framing、Unix socket 或 Windows named pipe 生命周期、请求响应关联、超时控制、扩展断连重试、会话级客户端注册与资源清理。桥接层需要同时服务两种后端：Chrome 扩展会话模式，以及独立浏览器实例模式的适配接口。该步骤依赖 2 和 3，是整个高保真方案的主干。
5. Phase E: Chrome 扩展子项目。仓库内新增一个最小 TypeScript Chrome 扩展子项目，仅承担扩展端逻辑，不把 Node/TS 引入 CLI 主体。扩展端至少包含 background/service worker 与 content script：负责 Native Messaging 握手、当前标签页与多标签管理、页面导航、点击/输入、截图、DOM 或文本提取、控制台日志读取，以及长任务状态回传。默认优先 Chrome，后续再按 Chromium 家族扩展到 Edge/Brave。该步骤依赖 2 和 3，可与 4 并行。
6. Phase F: Native Messaging 安装与 doctor 诊断。实现本地 wrapper/manifest 生成、浏览器数据目录与 NativeMessagingHosts 目录探测、首次安装后的重连提示，并在 doctor 中增加浏览器自动化自检：配置模式、浏览器可执行文件、manifest 安装状态、扩展连接状态、socket 可达性、独立实例后端可用性。该步骤依赖 4 和 5，是首个用户可验证闭环。
7. Phase G: Runtime 工具接线。把浏览器工具族正式接入当前 local tool server，在运行时入口里补 schema、dispatch、preview、审计日志、callback 结果回传，以及读/写类操作的确认逻辑。这里不能只在技能层加说明，必须让 provider 回调链路和 session/run 上下文完整贯通。该步骤依赖 3 和 4，建议在 5 的扩展协议稳定后联调。
8. Phase H: 权限模型扩展。当前仓库的权限规则只支持精确 tool_name 或 shell command_prefix，无法直接覆盖整个浏览器工具族；因此需要新增浏览器专属权限模式或 tool 前缀匹配能力。推荐把参考仓库的 ask、follow_a_plan、skip_all_permission_checks 映射为本仓库的浏览器权限层，并允许系统配置和会话内覆盖两级生效，同时与只读模式和现有确认框保持一致。该步骤依赖 3，阻塞最终验收。
9. Phase I: 独立浏览器实例模式。因为你接受 Node/TS 仅用于扩展端，不希望它渗透到 CLI/桥接侧，所以独立实例模式建议走 Go 侧 CDP（Chrome DevTools Protocol）适配，而不是再引入 Playwright sidecar。该模式应复用与扩展端相同的工具契约和配置切换，确保技能层与 provider 层不需要分叉。该步骤依赖 3 和 4，可在 Chrome 会话模式打通后并行推进。
10. Phase J: 技能、UI 与提示更新。把内置技能从“占位说明”升级为真实可用能力：根据 browser capability 决定是否展示启用说明、缺失扩展时给出安装引导、连通后引导模型先做 tabs_context。同步更新 CLI/TUI 的配置面板、slash 帮助、技能限制标签与 doctor 输出，消除当前“已内置但未实现”的误导。该步骤依赖 5、6、7。
11. Phase K: 测试与文档验收。补齐单元测试、协议测试、端到端 smoke test 和文档；更新规格与计划文档，明确 Chrome 会话模式、独立实例模式、权限策略、已支持浏览器、首期不支持项。该步骤依赖 6 到 10。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/builtin/taiyiflow-in-chrome/SKILL.md — 当前只是一段占位提示，后续需要改成真实激活文案、安装引导和工具族约束来源。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/bundled_registry.go — 当前已把 taiyiflow-in-chrome 作为默认内置技能注册，但没有实现能力；后续需要同步 summary 与启用条件。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go — 本地工具统一入口；浏览器工具族必须从这里接入，拿到 flow_id、session_id、run_id 与 callback 回传链路。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go — 本地工具 schema、权限确认、preview 与 mutating 分类的主落点；浏览器工具族的 schema 和读写分类需要落这里。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/security_helpers.go — 现有权限规则只能精确匹配工具名或 shell 前缀；浏览器权限模式或 tool 前缀匹配扩展要改这里。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go — 新增 browser 配置字段、默认值、归一化与持久化。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go — 当前主入口只有普通命令和 task 模式；后续要加隐藏 browser host/bridge 入口、配置切换和帮助文案。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/doctor.go — 增加 manifest、扩展连接、socket、浏览器可执行文件和隔离实例后端的自检输出。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/skills.go — 当前会把 taiyiflow-in-chrome 标为“裁剪实现”；能力落地后需要改技能展示和限制标签。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — 配置表单、状态面板和 slash 入口的主要改动点；需要增加 browser 相关配置项与连接/授权状态展示。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_capability.go — 适合补本地浏览器与扩展能力探测，供 doctor 与交互提示复用。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/specs/taiyiflow-go-client.md — 当前产品与架构基线，需要补浏览器自动化边界、双模式设计和验收要求。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/.github/prompts/plan-commandAndBundledSkills.prompt.md — 这里仍把 taiyiflow-in-chrome 标成“暂不复刻/排除”，需要更新或废弃。
- internal/browserbridge/ — 建议新增 Go 桥接层目录，承载 Native Messaging、socket、协议与会话管理。
- internal/runtime/ — 建议在该目录下新增浏览器工具实现文件，而不是把逻辑继续塞进现有大文件。
- browser-extension/chrome/ — 建议新增最小 Chrome 扩展子项目目录，单独维护 TS 构建链。

**Verification**
1. 配置与入口验证：确认 browser 配置能在 CLI、TUI 和配置文件三处往返一致，隐藏 host/bridge 模式可单独启动且不会破坏现有 task/TUI 入口。
2. 协议验证：对 Go 桥接层补请求响应关联、超时、断连恢复与资源清理测试，至少覆盖 Chrome 扩展未连接、连接中断、重复连接、超时返回四类情况。
3. Runtime 验证：补 go test 覆盖 internal/runtime、internal/config、cmd、internal/tui 中与浏览器工具、权限模式、doctor 输出相关的新增用例。
4. 构建验证：保持 go build -o taiyiflow-cli . 通过；扩展子项目单独执行其安装与构建命令并产出可加载扩展包。
5. Chrome 会话模式手工验收：安装扩展并写入 Native Messaging manifest 后，执行 doctor 应看到“已安装 manifest、扩展已连接、默认模式为 chrome_session”；随后通过同一套工具完成打开页面、点击、输入、截图、读取 DOM/文本、读取控制台日志、多标签页切换。
6. 独立实例模式手工验收：切到 isolated 配置后，不依赖扩展即可完成与 Chrome 会话模式等价的一组工具调用，并确认技能与 provider 层不需要改名或分叉。
7. 权限验证：覆盖 ask、follow_a_plan、skip_all_permission_checks 与现有只读模式、确认框、会话级覆盖之间的交互，确保不会出现浏览器工具绕过全局只读或误触发无限确认。
8. 文档验收：规格、技能文案、doctor 输出说明、安装说明和已知限制保持一致，不再出现“已内置但不可用”的描述偏差。

**Decisions**
- 已确认采用高保真复刻，而不是仅做 Playwright 风格的轻量 MVP。
- 已确认默认目标是 Chrome 当前用户会话，同时要求通过配置支持独立自动化浏览器实例模式。
- 已确认仓库内可以新增一个小型 Chrome 扩展子项目，但 Node/TS 仅限扩展端；CLI、桥接、运行时与隔离实例适配仍保持 Go 为主。
- 推荐独立实例模式使用 Go 侧 CDP 适配，而不是再引入 Playwright sidecar，以避免 Node/TS 越界到 CLI 主体。
- 首期纳入的能力范围包括导航、点击、输入、截图、DOM/文本读取、控制台日志、多标签页、长任务；不纳入参考仓库的远程 bridge、analytics、ant-only lightning browser_task、桌面级操作。
- 浏览器工具族建议沿用 taiyiflow-in-chrome__* 命名，以兼容当前内置技能文案与后续权限分组。

**Further Considerations**
1. 浏览器支持顺序建议明确为“Chrome 先行，Edge/Brave 后补”，否则 manifest 安装与目录探测会在首期被多浏览器兼容成本拉高。
2. 长任务设计建议优先复用当前后台任务与 terminal_capture 模型，只把浏览器动作执行本身接入新桥接层，不另起一套任务系统。
3. 若扩展端首期难以稳定提供高质量 DOM 结构化结果，可以先冻结 read_text/read_dom_snapshot 的返回格式，再逐步增强为更丰富的节点信息，避免工具契约反复变动。