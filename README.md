# 太乙智启命令行（CLI）客户端

太乙智启命令行（CLI）客户端是一个面向本地终端的智能体客户端项目，当前已完成 Go 客户端基础骨架、TaiyiFlow stream 对接、本地工具回调链路，以及 Bubble Tea 单入口 TUI 主界面。

## 特性目标

- 单入口交互，统一处理代码、文档、脚本和排障任务
- 本地优先，围绕当前工作目录、本地文件和本机命令运行
- 代码优先，同时支持开发过程中的文档整理与中文协作文本处理
- 首版默认对齐太乙智启后台 stream 接口实现

## 当前状态

- 状态：开发中
- 主语言：Go
- 首版联调基线：../taiyiflow/src/backend/base
- 默认界面：Bubble Tea TUI
- 版本信息入口：`./taiyiflow-cli about`、交互式 `/about`、TUI `/about`

## 技能（Skill）

- 默认从工作区 `.snz1dp/skills` 与用户级技能目录发现本地技能（Skill），并支持 `skills list`、`skills view`、`skills enable`、`skills disable` 管理。
- 客户端同时支持远程技能仓库搜索、详情查看、安装、更新、删除与仓库源切换，默认仓库地址为 `https://mirror-cn.clawhub.com`。
- TUI 与交互式会话中的 `/skills` 弹窗已支持“本地技能 / 远程仓库”双标签页，可直接浏览远程技能并执行安装、更新、删除。
- 默认未被显式禁用的本地技能都会纳入渐进式披露候选，并在发起 stream 请求时通过 `client_skill_packages` 一并上送，包含技能元数据、正文内容与完整 markdown 文档，便于服务端按名称匹配或直接消费技能内容。

## 构建

- VS Code 默认构建任务：build taiyiflow-cli
- `make build`：默认先更新 `browser-extension/chrome/dist`，再编译当前平台产物 `./taiyiflow-cli`
- `make build-all`：使用本地 Go/Node 工具链，默认先更新 `browser-extension/chrome/dist`，再输出完整 release 集合到 `build/release/`；其中 darwin 产物会在当前 macOS 主机上以 `CGO_ENABLED=1` 原生构建
- `make build-release`：生成完整 release 集合；要求当前主机为 macOS，内部会拆成 `make build-release-cross`（linux/windows）和 `make build-release-darwin-native`（darwin）
- `make build-release-cross`：仅执行 linux/windows 发布产物的 Go 交叉编译；要求 `browser-extension/chrome/dist/manifest.json` 已存在，主要供容器内复用
- `make build-release-darwin-native`：仅在当前 macOS 主机上生成 `darwin/amd64` 与 `darwin/arm64` 原生 CGO 产物
- `make release`：使用 Docker/Podman 构建发布产物；先通过 `snz1.cn/base/node:20-alpine` 刷新浏览器扩展 dist，再在容器里生成 linux/windows 产物，最后回到 macOS 主机生成 darwin 原生产物
- `make browser-extension-dist`：仅刷新浏览器扩展构建产物
- `make browser-extension-dist-docker`：使用 `snz1.cn/base/node:20-alpine` 刷新浏览器扩展构建产物
- `make verify-computer-live-darwin`：在 macOS 上打开一个可见的 Chromium 窗口，实机验证桌面 `double click`、`right click`、`drag` 与 `scroll` 行为；默认使用 Chrome，可通过 `TAIYIFLOW_COMPUTER_LIVE_BROWSER=edge|chromium|brave` 覆盖
- 直接命令行构建：`go build -o taiyiflow-cli .`
- 运行：`./taiyiflow-cli`

桌面实机验证前提：执行 `make verify-computer-live-darwin` 前，需要先为当前终端/Go 进程授予 macOS 辅助功能权限；如果测试页面需要接收事件，验证过程中请不要切走前台浏览器窗口。

## 登录

- 顶层登录入口：`./taiyiflow-cli login`，默认直接进入 TUI 登录表单。
- 查看当前登录态：`./taiyiflow-cli login status`
- 扫码登录：`./taiyiflow-cli login qr`，命令会直接在终端显示二维码并自动等待扫码完成。
- 退出登录：`./taiyiflow-cli login logout`

说明：命令行（CLI）登录与 TUI `/login` 共用同一套认证实现，默认使用配置中的 `backend_base_url`，并在成功登录后更新 `api_key`、`persistent_token`、`device_id` 与认证时间。密码登录与验证码输入统一收口到 `/login` 表单，旧的 `login password` 与 `login qr wait` 子命令已移除。

图形验证码说明：支持时优先使用 kitty 与 iTerm2 终端图片协议显示图形验证码，在 tmux 中会自动透传到底层终端；如需显式指定或关闭，可设置 `TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL=kitty|iterm2|off`。

## 运行时行为说明

- 长时间闲置后的首次普通 CLI 请求与 TUI 对话请求，都会优先尝试使用本地 `persistent_token` 自动续期；续期成功后会直接重放原始 stream 请求，只有续期失败或本地凭据缺失时才回落到重新登录。
- 智能体“最大循环”边界只约束单次请求内的 agent/tool 迭代，不跨用户轮次累计；普通文本分片、上下文压缩进度事件和统计刷新事件不会被计入同一终止条件。
- 文件写入审查由 `write_confirmation_policy` 单独控制；设置为 `direct` 时，`write_file` 与 `file_edit` 会直接执行，不再弹出写入审查窗口。
- `agent_auto_loop_max_rounds` 支持 `0-100`；其中 `0` 表示禁用智能体自动续跑。

## 浏览器扩展与内置资源

- `chrome_session` 模式依赖 `taiyiflow-cli browser install` 安装 Native Messaging host，并在浏览器中加载扩展构建产物。
- 对最终用户与发布产物，仓库内的 `browser-extension/chrome` 不是运行时必需项；若当前工作区缺少该目录，`browser install` 会自动抽取 CLI 内置的扩展最小运行目录到本地目录，直接提供可加载产物。
- 仓库内的 `browser-extension/chrome` 目前仍是扩展开发、调试与手工构建入口，主要服务于仓库维护；不要把“内置资源可运行”误解成“仓库源码目录可以直接删除”。

## 配置示例

命令行（CLI）支持通过启动参数临时覆盖当前任务的外部资源访问策略，也支持通过配置文件持久化默认策略。

单次任务临时覆盖：

```bash
./taiyiflow-cli task --external-resource-access-policy confirm "读取工作目录外的参考文件并整理差异"
```

持久化默认策略：

```bash
./taiyiflow-cli config set external_resource_access_policy deny
./taiyiflow-cli config set write_confirmation_policy direct
./taiyiflow-cli config set agent_auto_loop_max_rounds 0
./taiyiflow-cli config show
```

常见取值：

- `confirm`：访问工作目录外资源前确认
- `deny`：拒绝访问工作目录外资源
- `allow`：允许访问工作目录外资源

兼容性说明：

- 旧启动参数 `--sandbox-escape-policy` 仍可用，但新文档统一推荐 `--external-resource-access-policy`
- 配置键 `external_resource_access_policy` 会映射到现有配置结构，兼容旧键 `sandbox_escape_policy`

## 任务命令约定

- 命令行（CLI）单次任务入口使用 `./taiyiflow-cli task "任务描述"`。
- 交互式 CLI 与 TUI 中的后台任务管理入口使用 `/tasks`，例如 `/tasks`、`/tasks output <task_id>`、`/tasks cleanup`。
- 交互式环境中不使用 `/task`，避免与命令行（CLI）`task` 子命令重名造成歧义。

## 文档

- [规格计划](specs/taiyiflow-go-client.md)
- [仓库级 Copilot 指令](.github/copilot-instructions.md)
- [项目专用智能体](.github/agents/taiyiflow-cli.agent.md)

## 近期路线

1. 搭建 Go 项目骨架
2. 实现 internal/provider/taiyiflow/ 后端适配层
3. 对接后台 stream 接口与会话机制
4. 完成单入口命令行（CLI）交互主流程
