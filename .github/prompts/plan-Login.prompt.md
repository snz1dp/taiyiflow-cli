## Plan: CLI 登录与用户态展示改造

目标是在 taiyiflow-cli 中落地“可配置 API 前缀 + 默认读取 /users/whoami/userinfo 展示用户名 + 获取失败时进入完整登录流程（用户名密码/扫码/多因子）+ 持久化令牌自动登录”。其中登录服务上下文路径固定为 /xeai，登录服务接口文档地址为 http://localhost:8585/xeai/v3/api-docs，默认测试联调 base_url 为 http://localhost:4005。整体方案优先复用现有配置管理、Provider 客户端、TUI 弹窗交互与状态栏渲染路径，避免引入并行的认证栈。

**Steps**
1. Phase 1 基线与配置扩展（阻塞后续）
1.1 在 internal/config/config.go 的 Config 结构新增后端前缀字段（如 backend_api_prefix），Default() 默认值设为 /taiyi/composer/api/v1；同时将默认测试联调 base_url 设为 http://localhost:4005，Load()/Save() 保持兼容。*阻塞 2-6*
1.2 在 cmd/config.go 增加新配置键的 set/unset/normalize/summary 展示，支持命令行修改并落盘 taiyiflow.yaml。*依赖 1.1*
1.3 在 cmd/tui.go + internal/tui/app.go 的 /config 表单链路补齐该字段（值回填、提交、校验、文案、字段索引同步），避免字段偏移。*依赖 1.1；可与 1.2 并行，但最终需联调*

2. Phase 2 Provider 路由拼接与用户信息接口
2.1 在 internal/provider/taiyiflow/client.go 引入统一的 API 路径拼接方法，消除散落的 /api/v1 硬编码；新路径基于 base_url + api_prefix 组合。*依赖 1.1*
2.2 为 Provider 增加“当前用户信息”读取能力，默认调用 GET {api_prefix}/users/whoami/userinfo，抽取姓名字段（name）并保留兜底字段（user_name/user_id）。*依赖 2.1*
2.3 调整 buildProviderClient 与 NewClient 参数，确保 App 启动、/config 热刷新、健康检查、流式请求都使用同一前缀配置。*依赖 2.1*
2.4 同步更新 internal/provider/provider.go 抽象（如新增 GetCurrentUserInfo）及调用方编译影响。*依赖 2.2*

3. Phase 3 Footer 用户展示
3.1 扩展 tui.Snapshot（internal/tui/app.go）增加当前用户名/显示名字段，来源为 buildTUISnapshot。*依赖 2.2*
3.2 在 cmd/tui.go 的 buildTUISnapshot 中：后端可用时优先拉取 whoami/userinfo，失败时标记需要认证；成功时将姓名写入 snapshot。*依赖 2.2*
3.3 在 internal/tui/app.go 的 statusBarSegments 或 renderStatusBar 路径加入“当前用户: 姓名”段，宽度不足时可降级隐藏。*依赖 3.1*

4. Phase 4 登录与失败兜底（核心）
4.1 认证客户端：在 internal/provider/taiyiflow 新增 auth_client.go（或等效模块），与业务 Provider 共享 base_url，登录服务统一走 /xeai 上下文路径，封装关键接口：/xeai/oauth/api/password_token、/xeai/ajax_logon、/xeai/send_verify、/xeai/kaptcha/*、/xeai/oauth/api/token、/xeai/oauth/api/wait_event、/xeai/logged/persistent_token、/xeai/sso/salt、/xeai/sso。
4.2 会话存储策略：在配置中增加认证状态持久化字段（建议保存 persistent_token、device_id、必要时间戳；敏感字段最小化保存），并定义更新与失效清理逻辑。*依赖 1.1*
4.3 启动自动登录：在 TUI 启动（buildTUISnapshot 前或其内部）尝试 persistent_token 自动登录，成功后刷新 APIKey 或会话令牌，再重试 whoami/userinfo；失败则进入交互登录。*依赖 4.1, 4.2*
4.4 交互登录窗：复用现有 TUI overlay 模式新增登录窗口，支持用户名密码登录、扫码登录、验证码/图形码/短信/邮件/TOTP 多因子挑战流程（按后端 code/ticket 状态机驱动）。*依赖 4.1*
4.5 “默认记住登录”：登录成功后默认调用 /logged/persistent_token 创建持久化令牌并落盘；下次启动自动尝试 sso 流程。*依赖 4.2, 4.4*

5. Phase 5 错误处理与体验收口
5.1 将 401/403/登录态失效从“后端不可用”中区分出来，统一映射为“需要登录/重新登录”。
5.2 对扫码等待态、验证码输入态、登录成功态补充 footerHint/system note，减少黑盒等待感。
5.3 保证 base_url 在登录服务和业务服务完全共享（仅路径前缀与端点不同），避免双地址配置分叉。

6. Phase 6 测试与回归
6.1 单元测试：
- internal/config/config_test.go：新增前缀字段默认值、加载兼容、set/unset 行为。
- internal/provider/taiyiflow/client_test.go：统一前缀拼接、whoami/userinfo 解析、默认前缀归一化覆盖。
- cmd/tui_test.go：/config 提交新字段、footer 用户段渲染、登录失败触发认证流程。
6.2 集成验证（本地联调）：
- 默认 base_url=http://localhost:4005 时可直接联调登录服务与业务服务。
- 登录服务接口契约以 http://localhost:8585/xeai/v3/api-docs 为准。
- 使用默认前缀 /taiyi/composer/api/v1 能连通后端。
- whoami/userinfo 成功时 footer 显示姓名。
- whoami/userinfo 失败时自动进入登录窗，完成登录后恢复并显示姓名。
- 关闭并重启 CLI 后可通过 persistent_token 自动登录。
- 登录相关请求路径均包含 /xeai 上下文前缀。
6.3 回归命令：go test ./internal/provider/taiyiflow ./internal/config ./cmd ./internal/tui

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go — 配置结构、默认值、加载落盘
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go — config set/unset/summary 入口
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go — Provider 接口扩展
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go — API 前缀拼接、userinfo 调用
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/auth_client.go（新增） — 登录服务适配
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — 启动快照、登录兜底、配置表单提交
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — footer 渲染、登录弹窗交互
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config_test.go — 配置单测
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client_test.go — Provider 单测
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go — TUI 行为测试

**Verification**
1. 执行 go test ./internal/config ./internal/provider/taiyiflow ./cmd ./internal/tui
2. 人工验证默认配置首次启动：使用 /taiyi/composer/api/v1 自动连通并能显示当前用户姓名
3. 人工构造 whoami/userinfo 失败（清 token 或模拟 401）验证：进入登录弹窗、支持密码/扫码/多因子、登录后恢复
4. 重启 CLI 验证 persistent_token 自动登录与过期后重登录路径

**Decisions**
- 已确认前缀策略：默认 /taiyi/composer/api/v1，允许配置覆盖。
- 已确认登录范围：失败兜底需要完整覆盖用户名密码、扫码及多因子流程。
- 已确认记住登录：使用 /logged/persistent_token 作为持久化令牌机制。
- base_url 统一：登录服务与业务服务共用同一 base_url。
- 已确认登录服务上下文路径目录：/xeai。
- 已确认登录服务接口地址：http://localhost/xeai/v3/api-docs。
- 已确认默认测试联调 base_url：http://localhost。

**Further Considerations**
1. 安全建议：持久化令牌尽量最小化存储并预留后续迁移到系统钥匙串（当前阶段先按配置文件方案落地）。
2. 兼容建议：不保留 /api/v1 作为可配置值，修改原有配置。
3. 可观测性：在登录与自动登录流程增加可控 debug 日志开关，便于排查认证链路。
