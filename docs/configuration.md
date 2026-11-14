# 配置参考

CLI 的全部持久化配置保存在一个 YAML 文件里，可通过 `config` 子命令、TUI `/config` 弹窗或 `task --config-override` 临时覆盖来修改。

## 配置文件位置

```text
~/.snz1dp/config/taiyiflow.yaml
```

- 根目录可用环境变量 `SNZ1DP_HOME` 重定位（例如 `SNZ1DP_HOME=/data/snz1dp`，配置随之变为 `/data/snz1dp/config/taiyiflow.yaml`）；
- `taiyiflow-cli config path` 显示当前实际路径；
- 敏感值（`access_token`、`persistent_token`）以 `enc:v1:` 前缀加密存储，不要手工编辑这两个字段；
- MCP 本地启停配置单独存放在 `~/.snz1dp/config/mcp.json`，与桌面端共享。

## 修改方式

```bash
taiyiflow-cli config show                    # 查看当前配置
taiyiflow-cli config set approval_mode auto  # 设置
taiyiflow-cli config unset sandbox_roots     # 清空（恢复默认）
```

- 配置键大小写不敏感，连字符与下划线等价（`browser-mode` 与 `browser_mode` 相同）；
- 每个键都有若干历史别名（见下表“别名”列），新文档统一使用规范键名；
- 单次任务临时覆盖：`taiyiflow-cli task --config-override key=value ...`，仅影响当前命令。

## 后端与认证

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `backend_base_url` | `https://snz1.cn` | 服务端地址，完整 HTTP/HTTPS 地址 |
| `backend_api_prefix` | `/taiyi/composer/api/v1` | 后台 API 前缀（别名 `backend_prefix`、`api_prefix`） |
| `auth_api_prefix` | `/xeai` | 认证 API 前缀（别名 `auth_prefix`、`login_api_prefix`） |
| `default_flow` | 空（自动选择） | 默认智能体 ID；清空用 `config unset` |
| `access_token` | — | 访问令牌，登录后自动写入，加密存储 |
| `persistent_token` | — | 持久令牌，用于自动续期，加密存储 |
| `client_id` / `device_id` | 自动生成 | 客户端与设备标识 |

## 执行与审批

| 配置项 | 默认值 | 取值 | 说明 |
| --- | --- | --- | --- |
| `approval_mode` | `confirm` | `confirm` / `auto` / `readonly` | 命令执行审批模式 |
| `external_resource_access_policy` | `confirm` | `confirm` / `deny` / `allow` | 工作目录外资源访问策略（别名 `sandbox_escape_policy`） |
| `shell_sandbox_mode` | `auto` | `off` / `auto` / `enforce` | Shell 沙盒模式 |
| `sandbox_roots` | 空 | 逗号分隔目录 | 额外允许访问的外部资源目录 |
| `write_confirmation_policy` | `confirm` | `confirm` / `direct` | 文件写入审查；`direct` 时 write_file / file_edit 直接执行 |
| `skill_review_policy` | `confirm` | `confirm` / `ignore` | 技能内容审查策略 |
| `mode_switch_confirmation_policy` | `confirm` | `confirm` / `skip` | 请求模式切换确认 |
| `tool_confirm_timeout_seconds` | `120` | 0–300 | 工具确认倒计时秒数，0 表示禁止倒计时 |
| `audit_enabled` | `false` | `true` / `false` | 审计开关 |

权限规则（`permission_rules`）通过 `/permissions rule` 管理，见[安全与权限](/security)。

## 请求模式与思考

| 配置项 | 默认值 | 取值 | 说明 |
| --- | --- | --- | --- |
| `default_request_mode` | `agent` | `agent` / `ask` / `plan` | 默认请求模式 |
| `request_mode_auto_switch` | `true` | `true` / `false` | 请求模式自动切换 |
| `reasoning_enabled` | `false` | `true` / `false` | 思考模式默认开关 |
| `output_language` | `zh-CN` | 语言标签 | 输出语言 |

## 超时与流式

| 配置项 | 默认值 | 范围 | 说明 |
| --- | --- | --- | --- |
| `http_timeout_seconds` | `600` | 1–2000 | 后台 HTTP 请求超时 |
| `command_timeout_seconds` | `3600` | 1–6000 | 本地命令执行超时 |
| `subtask_timeout_seconds` | `3600` | 0–6000 | 子任务超时，0 表示不限制 |
| `stream_idle_timeout_seconds` | `1200` | ≥0 | 流空闲超时，0 表示禁用 |
| `stream_protocol` | `auto` | `auto` / `sse` / `websocket` | 流式传输协议（`ws` 等价 `websocket`） |
| `qr_wait_timeout_seconds` | `120` | ≥0 | 扫码登录等待超时，0 表示禁用 |

## 智能体循环与子任务

| 配置项 | 默认值 | 范围 | 说明 |
| --- | --- | --- | --- |
| `agent_auto_loop_enabled` | `false` | `true` / `false` | 智能体自动续跑开关 |
| `agent_auto_loop_max_rounds` | `0` | 0–10000 | 单次请求内 agent/tool 迭代上限，0 表示禁用自动续跑 |
| `max_parallelism_subtask` | `3` | 0–32 | 子智能体最大并发，0 表示禁用子智能体 |
| `cross_session_memory_enabled` | `true` | `true` / `false` | 跨会话记忆开关 |

::: tip 循环边界
“最大循环”只约束单次请求内的 agent/tool 迭代，不跨用户轮次累计；普通文本分片、上下文压缩进度与统计刷新事件不计入终止条件。
:::

## 文件与文档

| 配置项 | 默认值 | 说明 |
| --- | --- | --- |
| `workdir` | 当前目录 | 默认工作目录 |
| `doc_unstructured_exts` | 内置列表 | 文档结构化解析扩展名（逗号分隔）；`unset` 恢复内置默认 |
| `file_search_exclude_dirs` | 内置列表 | 文件搜索排除目录（逗号分隔）；`unset` 恢复内置默认 |

`doc_unstructured_exts` 内置默认覆盖常见音视频与办公文档：`mp3, wav, ogg, flac, aif, m4a, aac, mp4, webm, avi, mov, flv, wmv, rmvb, mkv, pdf, doc, docx, xls, xlsx, ppt, pptx`。

`file_search_exclude_dirs` 内置默认覆盖版本控制、依赖、构建产物、IDE、缓存等目录（`.git`、`node_modules`、`vendor`、`dist`、`build`、`target`、`__pycache__`、`.snz1dp` 等）。

## 浏览器自动化

| 配置项 | 默认值 | 取值 | 说明 |
| --- | --- | --- | --- |
| `browser_mode` | `chrome_session` | `disabled` / `chrome_session` / `isolated` | 浏览器自动化模式 |
| `browser_permission_mode` | `ask` | `ask` / `follow_a_plan` / `skip_all_permission_checks` | 浏览器变更工具审批 |
| `browser_channel` | `chrome` | `chrome` / `chromium` / `edge` / `brave` | 浏览器通道 |
| `browser_executable_path` | 空 | 路径 | 自定义浏览器可执行文件 |
| `browser_profile_dir` | 空 | 路径 | 独立实例的用户数据目录 |
| `browser_connect_timeout_seconds` | `15` | 1–120 | 浏览器连接超时 |
| `browser_extension_setup` | `native_messaging` | `native_messaging` / `manual` / `none` | 扩展接入方式 |
| `browser_socket_dir` | 空 | 路径 | 本地消息套接字目录 |
| `browser_remote_debugging_url` | 空 | URL | 远程调试地址（如 `http://127.0.0.1:9222`） |
| `browser_headless` | `false` | `true` / `false` | 无头模式 |

详见[浏览器与桌面自动化](/browser-computer)。

## 桌面自动化

| 配置项 | 默认值 | 取值 | 说明 |
| --- | --- | --- | --- |
| `computer_enabled` | `true` | `true` / `false` | 桌面操作开关 |
| `computer_permission_mode` | `ask` | `ask` / `follow_a_plan` / `skip_all_permission_checks` | 桌面变更工具审批 |

## 技能

| 配置项 | 默认值 | 取值 | 说明 |
| --- | --- | --- | --- |
| `skills_install_scope` | `user` | `user` / `workspace` | 技能安装范围 |
| `skills_auto_install_policy` | `off` | `off` / `confirm` / `allow` | 请求前自动补装策略 |
| `skills_free_text_search` | `false` | `true` / `false` | 未命中显式缺失技能时按任务文本搜索远程候选 |

技能启用/禁用列表（`skills.enabled` / `skills.disabled`）通过 `skills enable` / `skills disable` 管理，见[技能（Skill）](/skills)。

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `SNZ1DP_HOME` | 重定位 `~/.snz1dp` 根目录（配置、技能、日志等随之迁移） |
| `TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL` | 终端图片协议：`kitty` / `iterm2` / `off`，控制图形验证码显示 |

## 配置示例

```yaml
backend_base_url: https://snz1.cn
default_flow: 太乙智启
approval_mode: confirm
external_resource_access_policy: confirm
shell_sandbox_mode: auto
write_confirmation_policy: confirm
default_request_mode: agent
request_mode_auto_switch: true
reasoning_enabled: false
cross_session_memory_enabled: true
http_timeout_seconds: 600
command_timeout_seconds: 3600
stream_idle_timeout_seconds: 1200
stream_protocol: auto
max_parallelism_subtask: 3
browser:
  mode: chrome_session
  permission_mode: ask
  channel: chrome
computer:
  enabled: true
  permission_mode: ask
skills:
  install_scope: user
  auto_install_policy: off
```