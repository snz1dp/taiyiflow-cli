# 快速开始

太乙智启命令行（CLI）客户端是一个本地优先、代码任务优先的单入口智能体客户端。它在你的终端里运行，直连太乙智启后台的流式接口，把智能体的工具调用落到本地文件、本地命令与本机环境上。

本页带你在 5 分钟内完成：安装 → 登录 → 发起第一个任务 → 进入交互模式。

## 环境要求

- 操作系统：macOS、Linux 或 Windows（守护进程托管仅支持 macOS / Linux）
- 一个可用的太乙智启账号（默认后端 `https://snz1.cn`）
- 可选：Node.js、Python、ripgrep、git 等本地脚手架（`doctor` 会检测并报告）

## 安装

### 方式一：下载发布产物

从发布渠道获取对应平台的 `taiyiflow-cli` 二进制，放入 `PATH` 目录（例如 `/usr/local/bin`），并赋予可执行权限：

```bash
chmod +x taiyiflow-cli
sudo mv taiyiflow-cli /usr/local/bin/
taiyiflow-cli version
```

### 方式二：从源码构建

需要 Go 工具链；完整发布构建还需要 Node.js（用于浏览器扩展产物）：

```bash
git clone <仓库地址> taiyiflow-cli
cd taiyiflow-cli
go build -o taiyiflow-cli .
./taiyiflow-cli version
```

仓库同时提供 `make build`、`make build-all`、`make release` 等构建目标，详见[构建、升级与自检](/build-upgrade)。

### 验证安装

```bash
taiyiflow-cli about
```

输出产品名、组织、版本、提交与构建时间即表示安装成功。

## 登录

CLI 与交互界面共用同一套认证实现，登录态保存在本地配置文件中。

```bash
# 打开登录表单（密码 + 验证码，支持图形验证码终端显示）
taiyiflow-cli login

# 扫码登录：终端直接显示二维码并自动等待扫码完成
taiyiflow-cli login qr

# 查看当前登录状态
taiyiflow-cli login status

# 退出登录，清除本地登录态
taiyiflow-cli login logout
```

说明：

- 图形验证码优先使用 kitty 与 iTerm2 终端图片协议显示；在 tmux 中会自动透传到底层终端。可通过环境变量 `TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL=kitty|iterm2|off` 显式指定或关闭。
- 登录成功后会写入访问令牌（`access_token`）、持久令牌（`persistent_token`）、设备标识（`device_id`）与认证时间。敏感值以 `enc:v1:` 前缀加密存储。
- 长时间闲置后的首次请求会优先用 `persistent_token` 自动续期并重放原始请求，只有续期失败才需要重新登录。
- 旧的 `login password` 子命令已移除，密码登录统一收口到 `login` 表单。

## 选择智能体

```bash
# 列出当前账号可用的智能体
taiyiflow-cli agents

# 查看当前默认智能体
taiyiflow-cli agents current

# 设置默认智能体（按名称）
taiyiflow-cli agents use 太乙智启
```

未显式设置时，首次启动会自动选择默认智能体。

## 发起第一个任务

```bash
taiyiflow-cli task "统计当前目录下的 Go 文件数量，并列出最大的 5 个文件"
```

CLI 会：

1. 连接后台流式接口（默认 SSE，可切换 WebSocket）；
2. 智能体按需调用本地工具（读文件、搜索、执行命令等），工具调用默认需要你确认；
3. 流式输出回答，结束后返回终端。

常用变体：

```bash
# 指定智能体与请求模式
taiyiflow-cli task --agent 太乙智启 --mode plan "为这个项目设计一个日志模块"

# 上传本地文件作为任务输入
taiyiflow-cli task --file ./error.log "分析这份日志里的报错原因"

# 新建会话 / 继续当前会话
taiyiflow-cli task --new "开始一个新话题"
taiyiflow-cli task --continue "继续刚才的分析"

# 显示思考内容与流式调试信息
taiyiflow-cli task --show-thinking --show-stream-events "解释这段代码"
```

完整参数见[命令参考](/commands)。

## 进入交互模式

不带任务描述直接运行，即进入交互式终端界面（TUI）：

```bash
taiyiflow-cli
```

在交互模式里：

- 直接输入文字发起对话；
- `/ask`、`/plan`、`/agent` 切换请求模式；
- `/skills` 管理技能，`/config` 打开配置弹窗，`/permissions` 管理权限；
- `/help` 查看全部斜杠命令，`/exit` 退出。

详见[交互模式（TUI）](/tui)。

## 自检

遇到连接、权限或工具链问题时，先运行自检：

```bash
taiyiflow-cli doctor
```

它会报告配置路径、后端地址与超时、默认智能体、本地脚手架、浏览器与桌面自动化状态、技能目录等。详见[构建、升级与自检](/build-upgrade)。

## 下一步

- [命令参考](/commands)：全部子命令与参数
- [配置参考](/configuration)：配置文件位置、全部配置项与默认值
- [安全与权限](/security)：审批模式、沙盒与权限规则
- [传输协议参考](/protocol)：流式接口、事件类型与会话机制