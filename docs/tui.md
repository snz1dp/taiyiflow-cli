# 交互模式（TUI）

不带任务描述运行 `taiyiflow-cli`（或 `taiyiflow-cli task`）即进入交互式终端界面。TUI 基于 Bubble Tea 构建，是 CLI 的默认界面：对话、模式切换、技能管理、权限审批、配置修改都在同一个窗口里完成。

```bash
taiyiflow-cli                 # 进入交互模式
taiyiflow-cli --agent 太乙智启  # 指定智能体进入
taiyiflow-cli --new           # 新建会话进入
```

## 界面结构

- 主区域：对话消息流（用户输入、智能体回答、工具调用与系统提示）；
- 底部：输入框与状态栏（当前智能体、会话、请求模式、审批模式、思考模式等）；
- 右侧：待办栏（有未完成待办时自动显示，全部完成后自动隐藏）；
- 弹窗：登录表单、配置表单、技能选择器、权限面板、智能体选择器等。

后端不可用时进入只读模式：仍可查看历史、登录、改配置，但不能发起任务。

## 输入与快捷键

| 按键 | 行为 |
| --- | --- |
| `Enter` | 发送当前输入 |
| `Alt+Enter` / `Ctrl+J` | 换行；任务执行中则把输入作为“执行引导”提交给正在运行的任务 |
| `Shift+Enter` | 同上（VS Code 终端等支持 kitty 键盘协议的环境额外可用） |
| `Ctrl+C` | 任务执行中：停止本地执行并请求服务端取消；空闲时：退出 |
| `Esc` | 清空当前输入（执行中无效） |
| `Ctrl+O` | 打开智能体选择器 |
| `↑` / `↓` | 逐行滚动消息；斜杠命令提示出现时用于选择命令 |
| `PgUp` / `PgDn` | 半屏滚动；向上滚动到顶时自动加载更早的历史 |
| `Home` / `End` | 跳到消息顶部 / 底部 |
| `Tab` | 斜杠命令提示出现时补全选中命令 |

弹窗内通用：`Tab` / `Shift+Tab` 或 `↑` / `↓` 移动焦点，`Esc` 关闭，配置表单用 `Ctrl+S` 提交。登录表单另支持 `Ctrl+L` 退出登录、二维码模式下 `g` 刷新 / `w` 等待扫码。

::: tip VS Code 终端
在 VS Code 集成终端（`TERM_PROGRAM=vscode`）中，CLI 会自动启用 kitty 键盘协议的 push/pop 模式并翻译按键序列，保证 `Shift+Enter`、`Shift+Tab` 等组合键可用。
:::

## 斜杠命令

在输入框以 `/` 开头输入命令。输入 `/` 时会弹出命令提示，可用方向键选择、`Tab` 补全。

### 对话与模式

| 命令 | 说明 |
| --- | --- |
| `/agent [任务]` | 切换执行模式（agent），可直接携带任务 |
| `/ask [问题]` | 切换问答模式（ask） |
| `/plan [目标]` | 切换计划模式（plan） |
| `/reasoning [on\|off\|default\|status]` | 切换当前会话思考模式 |
| `/internet [on\|off\|status]` | 查看或切换当前对话的联网搜索 |
| `/knowledge` | 打开知识库范围选择器；`/knowledge status` 查看状态；`/knowledge auto` 切回自动 |
| `/upload <本地文件路径>` | 上传文件并附带到当前对话；`/upload status` 查看文件状态 |
| `/review [路径]` | 发起当前工作区或指定路径的代码审查 |

### 会话管理

| 命令 | 说明 |
| --- | --- |
| `/new [任务]` | 重置当前智能体的本地会话，可直接发起对话 |
| `/session` | 查看当前会话；`/session title <标题>` 设置会话标题（新会话除外） |
| `/sessions [页码]` | 查看当前智能体的服务端会话列表 |
| `/refresh` | 刷新当前会话的历史消息 |
| `/clear` | 清空当前界面消息 |
| `/agents [名称]` | 选择智能体 |

### 执行控制

| 命令 | 说明 |
| --- | --- |
| `/pause` | 请求暂停当前执行（服务端生成检查点后可恢复） |
| `/cancel` | 请求停止当前执行 |

暂停/停止请求受理后等待服务端权威状态；详见[执行控制与恢复](/execution-control)。

### 本地能力管理

| 命令 | 说明 |
| --- | --- |
| `/tasks [list\|output\|capture\|stop\|remove]` | 管理本地后台任务 |
| `/memory [status\|trigger] [flow=<智能体>] [key=value]` | 查看或触发跨会话记忆整合 |
| `/skills [list\|view\|search\|view-remote\|install\|remove\|update\|source\|enable\|disable\|help]` | 管理本地技能与远程仓库技能（双标签页弹窗） |
| `/permissions [show\|set\|clear\|rule]` | 查看与修改权限设置 |
| `/todo [show\|hide\|status]` | 查看或切换右侧待办栏 |
| `!<shell 命令>` | 在当前目录直接执行本地命令（不经过智能体） |

`/permissions` 子命令：

```text
/permissions show                          查看当前权限设置
/permissions set approval <confirm|auto|readonly>
/permissions set sandbox <confirm|deny|allow>
/permissions set roots <a,b>               设置沙盒根目录
/permissions clear roots                   清空沙盒根目录
/permissions rule add tool <tool> <allow|ask|deny>
/permissions rule add shell <prefix> <allow|ask|deny>
/permissions rule remove <序号>
```

详见[安全与权限](/security)。

### 系统与账号

| 命令 | 说明 |
| --- | --- |
| `/login` | 打开登录表单；`/login status`、`/login logout` 查看或清理登录态 |
| `/config` | 打开全局与权限配置弹窗；`/config session ...` 设置会话级配置 |
| `/about` | 显示客户端版本、组织与后端对接说明 |
| `/help` | 查看当前可用命令提示 |
| `/exit` | 退出 |

## 请求模式

三种请求模式决定智能体的行为边界：

- **agent（执行模式）**：完整工具链，可读写文件、执行命令、发起子任务；
- **ask（问答模式）**：以回答为主，不主动执行变更类操作；
- **plan（计划模式）**：先产出计划与拆解，再按需进入执行。

模式切换默认需要确认（`mode_switch_confirmation_policy=confirm`），可配置为 `skip`。开启 `request_mode_auto_switch` 后，服务端可按任务内容建议自动切换。

## 待办与后台任务

- 智能体通过 `todo_write` 维护待办清单，右侧待办栏自动显隐；
- 本地后台任务（`shell_exec` 的 `background=true` 等）通过 `/tasks` 查看输出、停止或清理；
- 交互模式默认启用子智能体（`run_subtask`），非交互式 `task` 需要 `--enable-subtask` 显式开启。

## 只读模式

后端不可用（网络中断、服务维护等）时，TUI 进入只读模式，可用命令收敛为：`/about`、`/refresh`、`/session`、`/login`、`/clear`、`/config`、`/todo`、`/tasks`、`/memory`、`/skills`、`/reasoning`、`/help`、`/exit`。恢复连接后自动回到完整功能。