## Plan: CLI 本机操作双层落地

把现有浏览器自动化视为本机操作的 Phase 0 基线，不重做一套新浏览器链路；先在 taiyiflow-cli 内收敛统一的本机操作能力模型与权限语义，再新增桌面级 taiyiflow-computer__* 工具族与跨平台原生桥接。首期仅改 CLI 侧，不改 taiyiflow 后端 stream schema；但因为用户已明确要求 Windows/macOS/Linux（含 X11 与 Wayland，首批 Wayland compositor 覆盖 GNOME、KDE Plasma、Sway/wlroots、Hyprland）同步进入范围，且允许引入 CGO/原生依赖，所以构建与发布链路必须从当前纯 Go + CGO_ENABLED=0 的多平台交叉构建，升级为按平台原生构建或专用工具链构建。

**Steps**
1. Phase A: 范围冻结与现状对齐。把“本机操作”拆成两个并列子域：浏览器内自动化继续沿用现有 taiyiflow-in-chrome__*，桌面级操作新增 taiyiflow-computer__*。明确本轮只做 CLI 侧闭环，不改 taiyiflow 后端 schema 或 SSE 事件；同时把规格、技能文案、计划文档里对浏览器自动化“已实现/排除/待实现”的冲突描述统一起来。该步骤阻塞后续契约冻结。
2. Phase B: 先修浏览器 Phase 0 的协议漂移。现有浏览器自动化链路已接入 runtime、browserbridge、doctor、TUI 与扩展，但 Chrome 当前会话模式和 isolated 模式的协议仍需归一：至少包括 tabs_create 默认 URL、read_text 的 max_chars/max_length 参数名不一致、screenshot 在扩展链路返回 image_base64 而 TUI/运行时按 path 展示与审计、以及读写工具在两种模式下结果字段形状不一致。先把这批问题收敛成统一的请求/返回契约，并补回归测试。该步骤阻塞所有后续本机操作统一抽象。
3. Phase C: 冻结通用本机操作契约。保留浏览器工具族不改名；新增桌面级工具族 taiyiflow-computer__*，首批建议至少覆盖：displays_context、windows_context、focus_window、screenshot、mouse_move、mouse_click、mouse_drag、scroll、key_press、type_text。为所有桌面级工具定义统一的参数和返回：显示器尺寸/缩放、窗口边界、输入坐标系、截图产物路径、错误码和 capability 元数据。该步骤依赖 1 和 2，阻塞运行时接线与原生桥接实现。
4. Phase D: 运行时与技能层接线。参考现有 browser_tools.go 模式新增一组桌面操作工具，实现 schema 注册、preview、审计日志、request mode 过滤、SubmitClientCallResult 回传、TUI 工具面板展示与 slash/help 能力说明。新增一个 bundled skill 用于桌面级操作指南；浏览器技能保留为独立技能，不与桌面级技能混名。该步骤依赖 3，可与 5 并行。
5. Phase E: 权限与安全模型重构。浏览器工具现有 skip_all_permission_checks 可绕过全局 readonly；桌面级操作不能直接复制这套语义。需要新增独立的 computer.permission_mode 和更细粒度的风险分层：读取类（窗口列表、显示器信息、截图）与变更类（焦点切换、鼠标、键盘）分开；即便全局模式为自动或浏览器跳过确认，桌面级高风险输入也应保留单独 gate，避免出现浏览器策略误放大到全桌面控制。该步骤依赖 3，阻塞 7-9 的最终验收。
6. Phase F: 桌面桥接抽象与构建策略。新增统一的 computerbridge 抽象层，把 runtime 与 OS 实现隔离；约定 capability 探测、错误码、截图落盘与输入事件接口。由于用户已接受 CGO/原生依赖，且当前 Makefile 使用 CGO_ENABLED=0 做 darwin/linux/windows 交叉构建，因此这里必须同步重做发布策略：把当前单机 cross-build 调整为按平台原生构建或专用工具链构建，拆出对应的 CI/发布入口，而不是继续沿用现有一套 go build 循环。该步骤依赖 3，阻塞 7-10 的原生实现与发版验收。
7. Phase G: macOS 实现。基于 Quartz/CoreGraphics 完成显示器与窗口信息、全屏/窗口截图、鼠标与键盘事件注入、焦点切换与权限探测；把“屏幕录制/辅助功能”权限缺失转成 doctor 可读提示，而不是在运行期返回模糊错误。该步骤依赖 5 和 6，可与 8、9 并行。
8. Phase H: Windows 实现。基于 User32/GDI（必要时配合更稳定的截图路径）实现显示器/窗口枚举、焦点切换、截图与 SendInput 键鼠控制；补齐前台窗口限制、UAC、会话隔离等错误分类，并纳入 doctor 输出。该步骤依赖 5 和 6，可与 7、9 并行。
9. Phase I: Linux 实现，分两条子线并行。X11 路线实现完整窗口、截图、键鼠与焦点控制；Wayland 路线按 compositor 拆成 GNOME、KDE Plasma、Sway/wlroots、Hyprland 四个分支，分别落 capability 探测、截图、窗口信息与输入注入路径。这里必须把“实际支持到哪个 compositor、是否依赖 portal 或 compositor 专用接口、哪些动作可做/不可做”写进 capability 与 doctor；不能把 Wayland 当成单一平台处理。该步骤依赖 5 和 6，是整体风险最高的阶段。
10. Phase J: CLI/TUI/doctor 配置面与可见性收口。在配置文件、TUI 配置表单、doctor、自描述能力清单与技能展示中新增桌面操作的模式、权限与依赖探测输出；同时保留并复用现有 browser mode/channel/extension setup UI，不要让桌面级配置与浏览器配置互相混淆。该步骤依赖 4，可与 7-9 的中后期并行。
11. Phase K: 测试、验收与文档。先补 runtime/bridge 的单元测试与模式过滤测试，再补 OS-specific integration tests（通过 build tags 或环境变量显式开启）；最后执行人工验收矩阵：浏览器当前会话模式、浏览器 isolated 模式、macOS 桌面、Windows 桌面、Linux X11、Linux Wayland（GNOME/KDE/Sway/Hyprland）。同步更新规格、技能说明、安装说明、doctor 说明与已知限制。该步骤依赖 2-10。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/browser_tools.go — 现有浏览器工具定义、权限逻辑与 screenshot 自动路径逻辑；Phase B 需要以这里为浏览器契约基线。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go — 客户端工具注册、mode 过滤、确认逻辑与 preview 主入口；新增 desktop tool 族和权限策略的主落点。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go — 本地工具 dispatch 主入口；桌面级工具最终需要和浏览器工具一样在这里闭环。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/browserbridge/cdp_bridge.go — isolated 浏览器模式的真值实现；也是浏览器工具 canonical result shape 的一侧来源。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/browserbridge/native_bridge.go — 当前会话扩展模式的桥接层；Phase B 需要在这里或其上层补归一化，而不是继续原样透传漂移结果。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/browserbridge/native_messaging.go — 当前会话模式的宿主、状态与诊断链路；desktop bridge 可复用这里的状态/诊断模式，但不应强耦合浏览器协议。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/browser-extension/chrome/src/background.ts — 当前会话浏览器工具真实执行端；当前已确认至少有 tabs_create、read_text、screenshot 与 Go 侧 schema 的漂移，需要先收敛。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go — 需要新增桌面操作配置，并重审 browser/computer 两套 permission mode 的关系。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/doctor.go — 当前已展示浏览器自动化状态；需要扩展到桌面级 capability、依赖和权限诊断。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/local_capability.go — 本地能力可见性与只读/评审模式放行逻辑，需要加入桌面操作的只读子集与能力标签。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — 当前浏览器 screenshot 面板假定结果里有 path；Phase B 需要先修当前浏览器两模式差异，后续再扩展 desktop tool 面板。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — 当前已有 browser 配置项；需要新增桌面操作配置项与说明，并避免配置表单混乱。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/bundled_registry.go — 需要新增桌面操作技能定义，同时保留现有 taiyiflow-in-chrome 作为浏览器子技能。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/builtin/taiyiflow-in-chrome/SKILL.md — 浏览器子技能说明需要改成“本机操作的浏览器子域”，并对齐真实契约。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/skills.go — 需要把现有“需浏览器配置”限制说明扩展成浏览器与桌面两类本机操作能力展示。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools_test.go — 已有浏览器工具 dispatch 与权限测试；Phase B 和 Phase D/E 需要在这里补桌面级工具与浏览器契约归一测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/Makefile — 当前 CGO_ENABLED=0 + 多平台 cross-build 是桌面级原生依赖的直接阻塞点，必须同步调整。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/go.mod — 需要引入桌面级原生依赖并明确其 build tags / OS 条件。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/specs/taiyiflow-go-client.md — 作为产品与架构基线，需要补充本机操作的双层范围与验收口径。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/.github/prompts/plan-commandAndBundledSkills.prompt.md — 当前仍把 taiyiflow-in-chrome 标成“暂不复刻/排除”，需要与现状和新计划对齐。

**Verification**
1. 浏览器 Phase 0 归一化验证：同一组 tabs_create、navigate、click、type、screenshot、read_text、read_console 调用在 chrome_session 与 isolated 模式下返回相同的字段形状，TUI 不再依赖模式分支猜测结果格式。
2. Runtime 验证：补 go test ./internal/runtime ./cmd ./internal/browserbridge，重点覆盖浏览器契约归一、desktop tools 注册、review/ask 模式只读过滤、approval/permission 交互和 doctor 输出。
3. TUI 验证：补 go test ./internal/tui，确认浏览器与 desktop tool 结果都能正确渲染，不出现 screenshot 面板空 path、错误 summary 或模式相关崩溃。
4. 原生桥接集成验证：为 macOS、Windows、Linux X11、Linux Wayland 分别提供 gated integration tests，只在显式环境变量开启时运行，避免 CI 中误触发真实键鼠事件。
5. 手工验收矩阵：
   1. 浏览器当前会话模式：安装宿主/扩展后，通过同一工具契约完成 tabs_context、tabs_create、navigate、click、type、screenshot、read_text、read_console。
   2. 浏览器 isolated 模式：不依赖扩展，完成与当前会话模式同等能力，并确认返回字段一致。
   3. macOS：确认屏幕录制/辅助功能权限提示、截图、鼠标、键盘、窗口列表、焦点切换全部闭环。
   4. Windows：确认前台窗口、SendInput、截图与窗口枚举闭环，并能区分权限/会话限制错误。
   5. Linux X11：确认完整窗口、截图、键鼠、焦点控制闭环。
   6. Linux Wayland：分别在 GNOME、KDE Plasma、Sway/wlroots、Hyprland 验证 capability 探测、截图、窗口信息与输入注入；doctor 必须明确当前 compositor 与可用动作。
6. 构建验证：现有 go build 与发布构建需升级为适配原生依赖的方案；最终必须产出可安装的 darwin/windows/linux 发布物，而不是只在当前开发机通过。
7. 文档验收：规格、README/doctor 输出、技能文案、安装指南与已知限制一致，不再同时存在“浏览器自动化已实现”和“暂不复刻”的冲突说法。

**Decisions**
- 已确认本轮只做 CLI 侧闭环；不改 taiyiflow 后端 stream schema，继续通过 client_mcp_servers 透传本地工具。
- 已确认本机操作同时包含浏览器内自动化与桌面级控制；浏览器链路视为现有子域，优先先修契约漂移，再在其上做统一抽象。
- 已确认桌面级操作需要 Windows/macOS/Linux 全平台进入首期范围。
- 已确认 Linux 首期不只做 X11；Wayland 也要覆盖，并把 GNOME、KDE Plasma、Sway/wlroots、Hyprland 纳入验收目标。
- 已确认允许引入 CGO/原生依赖；因此 Makefile/发布链路重构是必做项，不是附属优化。
- 建议浏览器与桌面保持两套工具族，不要把 DOM 选择器语义与屏幕坐标/窗口控制语义混成一个 tool namespace。

**Further Considerations**
1. 浏览器已有的 skip_all_permission_checks 可绕过全局 readonly；桌面级是否允许同类策略，需要在实现前单独拍板。当前建议默认不允许，以免浏览器策略外溢到整机控制。
2. Wayland 的“完整支持”必须落到 compositor-specific 实现和测试矩阵；如果不先明确每个 compositor 的能力下限，后续开发会陷入“功能看似完成、实际无法验收”的状态。
3. 若后续希望让模型更稳定地触发桌面级操作，可在 CLI 落地后再补一个后端或技能层面的提示词优化；但这不应阻塞当前 CLI-only 方案。