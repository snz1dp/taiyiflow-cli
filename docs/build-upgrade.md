# 构建、升级与自检

## 从源码构建

### 最小构建

只需要 Go 工具链：

```bash
go build -o taiyiflow-cli .
./taiyiflow-cli version
```

### Make 目标

仓库提供完整的构建目标（`make help` 查看全部）：

| 目标 | 说明 |
| --- | --- |
| `make build` | 默认目标：先刷新浏览器扩展 dist，再编译当前平台产物 `./taiyiflow-cli` |
| `make build-all` | 用本地 Go/Node 工具链输出完整 release 集合到 `build/release/`；darwin 产物在 macOS 主机上以 CGO 原生构建 |
| `make build-release` | 完整 release 集合；要求 macOS 主机，内部拆分为 cross（linux/windows）与 darwin-native 两部分 |
| `make build-release-cross` | 仅 linux/windows 交叉编译；要求浏览器扩展 dist 已存在，供容器内复用 |
| `make build-release-darwin-native` | 仅在 macOS 主机生成 darwin/amd64 与 darwin/arm64 原生 CGO 产物 |
| `make build-release-386` | 容器内构建 linux/386（glibc，CGO 开启） |
| `make build-release-386-musl` | 容器内构建 linux/386 musl 变体（Alpine Linux） |
| `make release` | Docker/Podman 构建：容器内刷新扩展 dist 与 linux/windows 产物，macOS 主机生成 darwin 原生产物 |
| `make browser-extension-dist` | 仅刷新浏览器扩展构建产物 |
| `make browser-extension-dist-docker` | 通过 Docker/Podman 刷新扩展产物 |
| `make verify-computer-live-darwin` | macOS 实机验证桌面双击、右键、拖拽与滚轮行为 |
| `make test` | 运行全部 Go 测试 |
| `make clean` | 清理构建输出 |

发布平台集合：

- 交叉编译：`linux/amd64`、`linux/arm64`、`windows/amd64`、`windows/386`、`windows/arm64`
- darwin 原生：`darwin/amd64`、`darwin/arm64`

构建信息（提交、时间）通过 ldflags 注入 `internal/buildinfo`，`about` / `version` 命令展示。

::: tip 桌面实机验证前提
执行 `make verify-computer-live-darwin` 前，需为当前终端/Go 进程授予 macOS 辅助功能权限；验证过程中不要切走前台浏览器窗口。可用 `TAIYIFLOW_COMPUTER_LIVE_BROWSER=edge|chromium|brave` 覆盖默认 Chrome。
:::

## 版本升级

```bash
taiyiflow-cli upgrade              # 检查并更新到最新版本
taiyiflow-cli upgrade --force      # 强制更新（即使版本相同）
taiyiflow-cli upgrade --manifest-url <URL>   # 自定义 manifest 地址
```

- 默认使用后端 check-update API（`--check-api` 默认开启），不回退到 manifest 流程；
- `--manifest-url` 可指向自定义 `manifest.json`（默认从配置的后端地址获取）；
- 守护进程场景的更新流程：`daemon stop` → 替换二进制 → `daemon start`，见[协同与常驻执行](/collaboration)。

## 环境自检（doctor）

```bash
taiyiflow-cli doctor
```

输出示例（节选）：

```text
太乙智启命令行（CLI）自检结果
- 当前工作目录: /path/to/project
- 配置文件路径: /Users/you/.snz1dp/config/taiyiflow.yaml（存在）
- 后台服务地址: https://snz1.cn
- 后台服务前缀: /taiyi/composer/api/v1
- HTTP 超时: 600 秒
- 命令超时: 3600 秒
- 流空闲超时: 1200 秒
- 默认智能体: 太乙智启
- 文件工具链: read_file/glob_files/write_file/file_edit/search_files（内置可用）
- 本地脚手架: node (v22.x), python3.x, rg (ripgrep ...), uv (...), git (...)
- 浏览器自动化: 浏览器模式: 当前会话 | 权限模式: ... | 状态: 可用 | ...
- 桌面自动化: 开关: 开启 | 权限模式: ... | 平台: macOS | 可用读取: ... | 可用输入: ...
- 浏览器扩展链路: 宿主: cn.snz1.taiyiflow_cli.browser_host | 清单: 已安装 | ...
- 技能包目录: /Users/you/.snz1dp/skills -> bundled（发现 N，显示 N）
```

排查指引：

| 现象 | 优先检查 |
| --- | --- |
| 连接失败 / 超时 | 后台服务地址、`http_timeout_seconds`、网络与代理 |
| 登录态异常 | `login status`、`persistent_token` 续期、重新 `login` |
| 浏览器工具不可用 | 浏览器扩展链路段：清单、启动脚本、扩展连接、套接字 |
| 桌面工具不可用 | 桌面自动化段：平台权限（macOS 辅助功能/屏幕录制，Linux xdotool/wmctrl） |
| 命令执行被拒 | 审批模式、权限规则、沙盒模式，见[安全与权限](/security) |
| 技能未生效 | 技能包目录段、`skills list`、`skills.disabled` 列表 |

## 版本信息

```bash
taiyiflow-cli about      # 产品、组织、简介、版本、提交、构建时间
taiyiflow-cli version    # 版本号、提交、构建时间
```

TUI 内 `/about` 展示相同信息。

## 本文档站点构建

文档站点位于仓库 `docs/` 目录，基于 VitePress：

```bash
cd docs
npm install
npm run docs:dev       # 本地开发预览
npm run docs:build     # 构建静态产物到 .vitepress/dist
npm run docs:preview   # 预览构建产物
```

容器化部署由 `docs/BUILD.yaml` + `docs/Dockerfile.docs` 描述，默认上下文路径 `/taiyi/cli/docs`，运行时可用 `WEB_CONTEXT_PATH` 环境变量重定位。