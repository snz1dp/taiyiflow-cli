# 浏览器与桌面自动化

CLI 提供两组独立的自动化工具族：浏览器自动化（`taiyiflow-in-chrome__*`）操作网页 DOM，桌面自动化（`taiyiflow-computer__*`）操作本机屏幕与窗口。两者语义不同，不要混用（DOM 选择器不能用于桌面坐标，反之亦然）。

## 浏览器自动化

### 三种模式

由 `browser_mode` 配置控制：

| 模式 | 说明 |
| --- | --- |
| `chrome_session`（默认） | 连接你当前正在使用的浏览器会话，复用登录态与标签页 |
| `isolated` | 启动独立浏览器实例，稳定可控的自动化环境 |
| `disabled` | 禁用浏览器自动化 |

```bash
taiyiflow-cli config set browser_mode chrome_session
taiyiflow-cli config set browser_mode isolated
taiyiflow-cli config set browser_mode disabled
```

### 当前会话模式安装

`chrome_session` 模式依赖浏览器扩展 + 本地消息宿主（Native Messaging）：

```bash
taiyiflow-cli browser install [--channel chrome|chromium|edge|brave]
```

安装过程会：

1. 生成本地消息宿主启动脚本与清单文件（宿主名 `cn.snz1.taiyiflow_cli.browser_host`）；
2. 若工作区缺少扩展目录，自动抽取 CLI 内置的可加载扩展产物；
3. 输出需要在浏览器中加载的扩展目录路径。

随后在浏览器的扩展管理页（`chrome://extensions` 等）开启开发者模式并加载该目录。`doctor` 会报告完整链路状态：清单、启动脚本、扩展连接、套接字可达性与最近握手时间。

- 仅显式传入 `--build` 时才执行 `npm install && npm run build`（面向扩展开发者）；
- `--extension-dir` 指定自定义扩展目录，`--use-embedded-extension` 强制使用内置产物；
- 对最终用户，仓库内的 `browser-extension/chrome` 源码目录不是运行时必需项。

### 独立实例模式

`isolated` 模式直接启动受控浏览器，可用配置：

```bash
taiyiflow-cli config set browser_channel chromium          # chrome|chromium|edge|brave
taiyiflow-cli config set browser_executable_path /path/to/chrome
taiyiflow-cli config set browser_profile_dir ~/.cache/taiyiflow-browser
taiyiflow-cli config set browser_headless true
taiyiflow-cli config set browser_remote_debugging_url http://127.0.0.1:9222
```

### 浏览器工具清单

| 工具 | 说明 |
| --- | --- |
| `taiyiflow-in-chrome__tabs_context` | 读取当前标签页上下文（ID、标题、URL、活动标签页） |
| `taiyiflow-in-chrome__tabs_create` | 创建新标签页 |
| `taiyiflow-in-chrome__navigate` | 导航到 URL |
| `taiyiflow-in-chrome__click` | 点击 CSS 选择器元素 |
| `taiyiflow-in-chrome__type` | 向输入元素写入文本 |
| `taiyiflow-in-chrome__screenshot` | 截取页面截图（PNG） |
| `taiyiflow-in-chrome__read_text` | 读取页面或指定选择器的文本 |
| `taiyiflow-in-chrome__read_console` | 读取控制台输出与异常日志 |

### 浏览器审批

`browser_permission_mode` 控制变更类工具（导航、点击、输入等）的审批：

| 取值 | 行为 |
| --- | --- |
| `ask`（默认） | 每次变更操作前确认 |
| `follow_a_plan` | 按计划批量放行 |
| `skip_all_permission_checks` | 跳过全部审批 |

::: tip 与全局审批的关系
浏览器权限模式独立于全局 `approval_mode`：即使全局为 `readonly`，只要 `browser_permission_mode=skip_all_permission_checks`，浏览器变更工具仍会放行。只读工具（tabs_context、read_text、read_console）不受审批限制。
:::

## 桌面自动化

### 开关与审批

```bash
taiyiflow-cli config set computer_enabled true            # 默认开启
taiyiflow-cli config set computer_permission_mode ask     # ask|follow_a_plan|skip_all_permission_checks
```

### 平台权限要求

桌面操作依赖操作系统授权，首次使用前 CLI 会做权限预检；权限不足时返回结构化错误与修复指引，不会静默失败。

| 平台 | 权限 | 受影响能力 |
| --- | --- | --- |
| macOS | 辅助功能（Accessibility） | 窗口列表、焦点切换、键盘输入、鼠标操作 |
| macOS | 屏幕录制（Screen Capture） | 截图 |
| Linux | xdotool 已安装 | 键盘、鼠标、滚轮 |
| Linux | wmctrl 已安装 | 窗口列表、焦点切换 |
| Windows | 无额外权限要求 | 全部 |

macOS 授权路径：系统设置 → 隐私与安全性 → 辅助功能 / 屏幕录制，为终端或 CLI 进程授权。

### 桌面工具清单

| 工具 | 说明 |
| --- | --- |
| `taiyiflow-computer__displays_context` | 读取显示器上下文（物理/逻辑尺寸、缩放比、主屏） |
| `taiyiflow-computer__windows_context` | 读取窗口上下文（标题、边界、进程、前台状态） |
| `taiyiflow-computer__focus_window` | 将指定窗口/进程切到前台 |
| `taiyiflow-computer__screenshot` | 截取桌面屏幕（PNG，可指定显示器） |
| `taiyiflow-computer__mouse_move` | 移动鼠标到坐标 |
| `taiyiflow-computer__mouse_click` | 鼠标点击（左/右/中键，可多次） |
| `taiyiflow-computer__mouse_drag` | 从起点拖拽到终点 |
| `taiyiflow-computer__scroll` | 垂直/水平滚动 |
| `taiyiflow-computer__key_press` | 发送单个按键或组合键 |
| `taiyiflow-computer__type_text` | 输入文本（中文等非 ASCII 文本必须用此工具，macOS 走剪贴板粘贴） |

### 坐标换算

多显示器场景下，截图结果附带 `display_index`、显示器原点与缩放比。对同一块屏幕继续操作时，可直接传截图内局部坐标 + `display_index`，CLI 自动换算到全局桌面坐标；已持有全局坐标时可用 `coordinate_space=global_screen` 显式覆盖。

### 审批与只读工具

`computer_permission_mode` 语义与浏览器一致。只读工具（displays_context、windows_context、screenshot）在 ask / plan 模式下也可用；输入类工具（focus_window、key_press、type_text、mouse_*、scroll）属于变更操作，按权限模式审批。

## 自检

```bash
taiyiflow-cli doctor
```

输出中“浏览器自动化”“桌面自动化”“浏览器扩展链路”三段分别报告模式、权限模式、可用工具与链路状态，是排查自动化问题的第一入口。