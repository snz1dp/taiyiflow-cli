# 命令参考

太乙智启 CLI 采用单入口主命令 + 少量辅助命令的结构。所有子命令都通过 `taiyiflow-cli <子命令>` 调用，参数解析基于 Go 标准库 `flag`，单横线与双横线写法等价（`-agent` 与 `--agent` 相同）。

```bash
taiyiflow-cli --help        # 查看全部子命令
taiyiflow-cli <子命令> --help  # 查看子命令参数（部分子命令用 help 子命令）
```

## 命令总览

| 命令 | 用途 |
| --- | --- |
| `task` | 发起单次任务；不带任务描述时进入交互模式 |
| `login` | 登录、查看登录态、扫码登录、退出登录 |
| `config` | 查看与修改全局配置 |
| `agents` | 列出、查看与切换默认智能体 |
| `skills` | 本地与服务端技能管理 |
| `mcp` | 模型上下文协议（MCP）服务器管理 |
| `browser` | 浏览器扩展与本地消息宿主安装 |
| `schedule` | 定时任务管理 |
| `memory` | 跨会话记忆查看与整合触发 |
| `daemon` | worker 守护进程安装与启停 |
| `worker` | 启动协同驻留执行进程 |
| `collaboration` | 协同任务与在线客户端查询 |
| `upgrade` | 检查并更新 CLI 版本 |
| `doctor` | 环境自检 |
| `about` / `version` | 产品与版本信息 |

兼容别名：`cli`、`flows`、`tui` 等历史入口仍可用，新文档统一使用上表命令。

## task — 任务执行

```bash
taiyiflow-cli task [参数] [任务描述]
```

不带任务描述且未指定 `--continue` / `--resume` / `--attach` 时，进入交互模式。

| 参数 | 说明 |
| --- | --- |
| `--agent <ID或名称>` | 指定要运行的智能体 |
| `--session-id <ID>` | 在已有会话内启动任务 |
| `--file <路径>` | 上传一个本地文件作为本次任务输入 |
| `--mode <agent\|ask\|plan>` | 请求模式，默认 `agent` |
| `--reasoning <on\|off>` | 思考模式，仅影响当前 task 会话 |
| `--internet-search <on\|off>` | 联网搜索，仅影响当前 task 会话 |
| `--new` | 忽略已有会话并新建会话 |
| `--continue` | 继续当前会话 |
| `--resume` | 从当前会话的可恢复检查点显式恢复 AgentLoop，不重复发送用户输入 |
| `--attach` | 以只读方式观察当前会话的远程执行，不执行本地工具 |
| `--show-stream-events` | 显示系统提示与工具调用等流式调试信息 |
| `--show-thinking` | 显示思考内容（不输出 think 标签） |
| `--external-resource-access-policy <confirm\|deny\|allow>` | 外部资源访问策略，仅影响当前命令 |
| `--cross-session-memory-enabled` | 跨会话记忆开关，默认使用系统配置 |
| `--config-override <key=value>` | 临时覆盖配置项，可多次使用，仅影响当前命令 |
| `--enable-subtask` | 在非交互式模式下启用子智能体（run_subtask）功能 |

互斥约束（违反时直接报错）：

- `--new` 与 `--session-id` 不能同时使用；
- `--resume` 与 `--attach`、`--new` 不能同时使用；
- `--attach` 与 `--new` 不能同时使用；
- `--resume` / `--attach` 不能携带 `--file`，也不接受新的任务输入。

示例：

```bash
# 单次任务
taiyiflow-cli task "修复 internal/config 里的空指针问题"

# 计划模式 + 指定智能体
taiyiflow-cli task --mode plan --agent 太乙智启 "设计缓存层方案"

# 恢复上次暂停的执行
taiyiflow-cli task --resume

# 只读观察远程执行（例如另一台设备发起的任务）
taiyiflow-cli task --attach

# 临时覆盖配置
taiyiflow-cli task --config-override approval_mode=auto "批量重命名测试文件"
```

::: warning 注意
顶层 `--help` 文本中出现的 `--stream-retry-attempts` 为历史残留，当前版本实际不接受该参数（传入会报 `flag provided but not defined`）。流式重试行为由配置控制，见[配置参考](/configuration)。
:::

## login — 登录

```bash
taiyiflow-cli login            # 打开登录表单（等价于 TUI /login）
taiyiflow-cli login status     # 查看当前登录状态
taiyiflow-cli login qr         # 终端显示二维码并自动等待扫码完成
taiyiflow-cli login logout     # 清除本地登录态
taiyiflow-cli login help       # 登录帮助
```

- 图形验证码优先使用 kitty / iTerm2 终端图片协议，tmux 中自动透传；`TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL=kitty|iterm2|off` 可显式控制。
- 扫码登录是否可用取决于认证服务端能力；未启用时提示“当前认证服务未启用扫码登录”。
- 旧的 `login password`、`login qr wait` 子命令已移除。

## config — 配置管理

```bash
taiyiflow-cli config show              # 查看当前 YAML 配置
taiyiflow-cli config path              # 显示配置文件路径
taiyiflow-cli config set <配置项> <值>  # 设置配置项
taiyiflow-cli config unset <配置项>     # 清空配置项（恢复默认）
taiyiflow-cli config help              # 配置帮助与常用配置项
```

示例：

```bash
taiyiflow-cli config set backend_base_url https://snz1.cn
taiyiflow-cli config set approval_mode confirm
taiyiflow-cli config set browser_mode isolated
taiyiflow-cli config set computer_enabled false
taiyiflow-cli config set doc_unstructured_exts pdf,docx
taiyiflow-cli config unset file_search_exclude_dirs
```

全部配置项、默认值与取值范围见[配置参考](/configuration)。

## agents — 智能体管理

```bash
taiyiflow-cli agents              # 列出可用智能体（可跟过滤关键词）
taiyiflow-cli agents list [关键词] # 同上
taiyiflow-cli agents current      # 查看当前默认智能体
taiyiflow-cli agents use <名称>    # 设置默认智能体
```

## skills — 技能管理

```bash
taiyiflow-cli skills list                    # 列出本地已发现技能及来源状态
taiyiflow-cli skills view <名称>              # 查看本地技能详情
taiyiflow-cli skills search [关键词]          # 搜索服务端技能
taiyiflow-cli skills view-remote <名称或ID>   # 查看服务端技能详情
taiyiflow-cli skills install <名称或ID>       # 从服务端安装技能到本地
taiyiflow-cli skills remove <名称或ID>        # 卸载已安装的服务端技能
taiyiflow-cli skills update [名称或ID|all]    # 更新指定或全部服务端技能
taiyiflow-cli skills enable <名称>            # 恢复技能的候选资格
taiyiflow-cli skills disable <名称>           # 将技能移出候选集合
```

后端技能管理（高级）：

```bash
taiyiflow-cli skills backend list                # 列出后端可见技能及安装状态
taiyiflow-cli skills backend view <名称或ID>      # 查看后端技能详情
taiyiflow-cli skills backend install <名称或ID>   # 从后端下载安装技能到本地
taiyiflow-cli skills backend remove <名称或ID>    # 卸载后端技能并清除本地缓存
taiyiflow-cli skills backend sync                # 同步后端已安装技能到本地
taiyiflow-cli skills backend upload <本地技能名>   # 上传本地技能到后端
taiyiflow-cli skills backend upload-file <文件路径> # 上传 ZIP 或 MD 文件到后端
taiyiflow-cli skills backend delete <技能ID>      # 从后端删除自定义技能
```

相关配置：

```bash
taiyiflow-cli config set skills_auto_install_policy off|confirm|allow
taiyiflow-cli config set skills_free_text_search true|false
```

详见[技能（Skill）](/skills)。

## mcp — MCP 服务器管理

```bash
taiyiflow-cli mcp list                            # 列出当前智能体的 MCP 服务器
taiyiflow-cli mcp add [选项]                       # 添加用户自建 MCP 服务器
taiyiflow-cli mcp edit <服务器ID> [选项]            # 编辑用户自建 MCP 服务器
taiyiflow-cli mcp remove <服务器ID>                # 删除用户自建 MCP 服务器
taiyiflow-cli mcp enable <服务器ID>                # 启用（本地）
taiyiflow-cli mcp disable <服务器ID>               # 禁用（本地）
taiyiflow-cli mcp enable-tool <服务器ID> <工具名>   # 启用指定工具（本地）
taiyiflow-cli mcp disable-tool <服务器ID> <工具名>  # 禁用指定工具（本地）
taiyiflow-cli mcp status                          # 查看本地 MCP 禁用配置
```

`add` / `edit` 选项：

| 选项 | 说明 |
| --- | --- |
| `--name <名称>` | 服务器名称（add 必填） |
| `--url <地址>` | 服务器地址（add 必填）；stdio 类型格式 `stdio://command args...` |
| `--type <类型>` | `stdio`、`sse`、`streamable_http`、`other`，默认 `streamable_http` |
| `--description <描述>` | 服务器描述 |
| `--default-enabled <bool>` | 默认启用状态，默认 `true` |
| `--pass-user-identity <bool>` | 调用时传递用户登录会话令牌，默认 `false`，仅 sse / streamable_http 生效 |
| `--agent <ID>` | 指定智能体 ID，默认使用当前默认智能体 |

示例：

```bash
taiyiflow-cli mcp add --name my-server --url https://mcp.example.com/sse --type sse
taiyiflow-cli mcp add --name local-tool --url "stdio://npx -y @some/mcp-server" --type stdio
taiyiflow-cli mcp disable abc123
```

启用/禁用状态保存在 `~/.snz1dp/config/mcp.json`，与太乙智启桌面端共享同一配置文件；系统内置服务器（`is_system=true`）不可被禁用。详见 [MCP 接入](/mcp)。

## browser — 浏览器扩展安装

```bash
taiyiflow-cli browser install [--channel chrome|chromium|edge|brave] [--build] \
                              [--extension-dir PATH] [--use-embedded-extension]
taiyiflow-cli browser help
```

- `install` 生成本地消息宿主启动脚本与清单文件；若缺少工作区扩展目录，会自动抽取 CLI 内置的可加载扩展产物。
- 仅在显式传入 `--build` 时才执行 `npm install && npm run build`。
- `host` 为内部模式，供浏览器通过本地消息协议启动，通常不需要手动执行。

详见[浏览器与桌面自动化](/browser-computer)。

## schedule — 定时任务

```bash
taiyiflow-cli schedule list                 # 列出定时任务
taiyiflow-cli schedule create [选项]         # 创建定时任务
taiyiflow-cli schedule edit [选项]           # 编辑定时任务
taiyiflow-cli schedule delete <ID>          # 删除定时任务
taiyiflow-cli schedule enable <ID>          # 启用定时任务
taiyiflow-cli schedule disable <ID>         # 停用定时任务
taiyiflow-cli schedule runs <ID>            # 查看执行历史
```

`create` 选项：

| 选项 | 说明 |
| --- | --- |
| `--name <名称>` | 任务名称（必填） |
| `--prompt <内容>` | 执行提示词或命令（必填） |
| `--mode <agent\|ask\|plan\|command>` | 请求模式，默认 `agent`；`command` 模式直接执行 shell 命令 |
| `--cron <表达式>` | Cron 表达式（5 段） |
| `--interval <秒>` | 间隔秒数（最小 60） |
| `--delay <秒>` | 延迟秒数（一次性） |
| `--run-at <时间>` | 一次性执行时间（ISO 8601 或 `2006-01-02 15:04`） |
| `--preset <hourly\|daily\|weekly\|monthly>` | 调度预设 |
| `--agent <ID>` | 关联智能体 ID |
| `--session-id <ID>` | 关联会话 ID |
| `--dir <目录>` | 工作目录 |
| `--desc <描述>` | 任务描述 |
| `--timeout <秒>` | 执行超时秒数，默认 300 |
| `--tz <时区>` | 调度时区，默认 `Asia/Shanghai` |
| `--json` | 以 JSON 格式输出 |

示例：

```bash
taiyiflow-cli schedule create --name "每日构建" --cron "0 9 * * *" \
  --prompt "执行项目构建并报告结果"
taiyiflow-cli schedule runs <schedule-id>
```

## memory — 跨会话记忆

```bash
taiyiflow-cli memory status [flow=<智能体>] [status=<状态>] [limit=<数量>]
taiyiflow-cli memory list   [flow=<智能体>] [status=<状态>] [limit=<数量>]
taiyiflow-cli memory trigger [flow=<智能体>] [hours=<小时>] [sessions=<会话数>] [execute=on|off]
```

- `status` / `list`：查看记忆整合状态与记录；
- `trigger`：触发跨会话记忆整合，`execute=off` 表示只预览不执行。

交互模式中等价入口为 `/memory`。跨会话记忆总开关为配置项 `cross_session_memory_enabled`（默认开启）。

## daemon — 守护进程

```bash
taiyiflow-cli daemon install     # 安装并启动 worker 守护进程
taiyiflow-cli daemon uninstall   # 停止并卸载 worker 守护进程
taiyiflow-cli daemon start       # 启动已安装的守护进程
taiyiflow-cli daemon stop        # 停止守护进程（不卸载，用于更新前停止）
taiyiflow-cli daemon status      # 查看守护进程运行状态
```

平台支持：macOS 使用 launchd（用户级 LaunchAgent），Linux 使用 systemd（用户级 user service）。日志输出到 `~/.snz1dp/logs/`。

更新流程：

```bash
taiyiflow-cli daemon stop     # 停止旧进程
# <替换二进制文件>
taiyiflow-cli daemon start    # 启动新进程
```

详见[协同与常驻执行](/collaboration)。

## worker — 协同驻留进程

```bash
taiyiflow-cli worker [--max-concurrency N] [--recent-limit N] \
                     [--ping-seconds N] [--reconnect-seconds N]
```

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `--max-concurrency` | 4 | 允许同时执行的子任务数 |
| `--recent-limit` | 20 | 连接就绪后补抓的最近任务条数 |
| `--ping-seconds` | 45 | 在线状态保活心跳秒数 |
| `--reconnect-seconds` | 3 | 长连接断开后的重连间隔秒数 |

## collaboration — 协同任务

```bash
taiyiflow-cli collaboration list [--status <状态>]  # 查询协同任务列表
taiyiflow-cli collaboration get <task_id>           # 查看协同任务详情
taiyiflow-cli collaboration online                  # 查看当前在线的桌面/CLI 客户端
```

## upgrade — 版本更新

```bash
taiyiflow-cli upgrade [--force] [--manifest-url URL] [--check-api]
```

| 参数 | 说明 |
| --- | --- |
| `--force` | 强制更新，即使当前版本与远程版本相同 |
| `--manifest-url <URL>` | 自定义 manifest.json 地址（默认从配置的后端地址获取） |
| `--check-api` | 仅使用 check-update API，不回退到 manifest 流程（默认开启） |

## doctor / about / version

```bash
taiyiflow-cli doctor     # 环境自检：配置、后端、工具链、浏览器/桌面、技能目录
taiyiflow-cli about      # 产品、组织、简介、版本、提交、构建时间
taiyiflow-cli version    # 版本号、提交、构建时间
```