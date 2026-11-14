# 技能（Skill）

技能（Skill）是可复用的提示词与操作指引包，核心是一个 `SKILL.md` 文件（YAML frontmatter + Markdown 正文）。CLI 在本地发现技能，按“渐进式披露”策略把候选技能随请求上送后台，由智能体按任务需要装载使用。

## 技能目录与发现顺序

CLI 按以下顺序发现技能，同名技能按发现顺序去重（先发现的优先）：

1. **工作区目录**：`<工作目录>/.snz1dp/skills`
2. **用户级目录**：`~/.snz1dp/skills`（`SNZ1DP_HOME` 可重定位；历史自定义路径配置仍兼容读取）
3. **兼容目录**：`~/.agent/skills`（只读加载，兼容其他 agent 工具安装的技能，不作为安装目标）

此外还有随客户端分发的**内置技能（bundled）**，`skills list` 与 `doctor` 会报告其数量。内置技能不能被 `skill_delete` 删除。

```bash
taiyiflow-cli skills list          # 列出本地已发现技能及来源状态
taiyiflow-cli skills view <名称>    # 查看技能详情（frontmatter + 正文）
```

## SKILL.md 结构

```markdown
---
name: 代码审查助手
description: 当用户要求审查代码变更时使用此技能。
category: development
metadata:
  request_types: [agent, plan]
  requires:
    env: [GITHUB_TOKEN]
---

# 代码审查助手

## 触发条件
...

## 执行步骤
...
```

- `name`：人类友好名称，系统自动转换为 slug 风格目录名（如 `代码审查助手` → 对应目录）；
- `description`：场景导向描述（“当…时使用”），是渐进式披露匹配的主要依据；
- `metadata.request_types`：声明技能适用的请求模式（ask / plan / agent）；
- `metadata.requires.env`：声明依赖的环境变量，装载时会附带提示。

## 渐进式披露与启用策略

默认策略**不是白名单**：未被显式禁用的技能都会纳入候选集合，随 stream 请求通过 `client_skill_packages` 上送（含元数据、正文与完整 markdown）。

- `skills disable <名称>`：把技能移出候选集合（写入 `skills.disabled`，优先级最高）；
- `skills enable <名称>`：恢复候选资格；
- `skills.enabled` 仅保留为显式放行/兼容配置。

智能体运行中通过 `skill_fetch` 工具按名称装载技能正文；装载默认需要审查确认（`skill_review_policy=confirm`），可设为 `ignore` 跳过。

## 服务端技能仓库

CLI 通过太乙智启后端接口（`/agents/skills`）访问服务端技能仓库：

```bash
taiyiflow-cli skills search [关键词]          # 搜索服务端技能
taiyiflow-cli skills view-remote <名称或ID>   # 查看服务端技能详情
taiyiflow-cli skills install <名称或ID>       # 安装到本地
taiyiflow-cli skills update [名称或ID|all]    # 更新已安装的服务端技能
taiyiflow-cli skills remove <名称或ID>        # 卸载
```

安装的服务端技能会写入来源元数据（`.snz1dp/origin.json`，记录 registry、slug、版本、安装时间），用于后续更新、卸载与同步识别。加载失败的远程技能会被记录并触发重新下载。

### 后端技能管理（高级）

```bash
taiyiflow-cli skills backend list                # 后端可见技能及安装状态
taiyiflow-cli skills backend install <名称或ID>   # 从后端下载安装
taiyiflow-cli skills backend sync                # 同步后端已安装技能到本地
taiyiflow-cli skills backend upload <本地技能名>   # 上传本地技能到后端
taiyiflow-cli skills backend upload-file <路径>   # 上传 ZIP 或 MD 文件
taiyiflow-cli skills backend delete <技能ID>      # 从后端删除自定义技能
```

## 自动补装

发起请求前，若服务端指出缺失技能（`missing_skill_names`），CLI 可按策略自动补装：

| 配置项 | 取值 | 行为 |
| --- | --- | --- |
| `skills_auto_install_policy` | `off`（默认） | 不自动补装 |
| | `confirm` | 请求确认后补装 |
| | `allow` | 直接自动安装 |
| `skills_free_text_search` | `false`（默认） | 未命中显式缺失技能时，不按任务文本搜索远程候选 |
| | `true` | 按任务文本搜索远程技能候选 |

## 安装范围

```bash
taiyiflow-cli config set skills_install_scope user       # 安装到用户级目录（默认）
taiyiflow-cli config set skills_install_scope workspace  # 安装到当前工作区 .snz1dp/skills
```

## 在 TUI 中管理

交互模式输入 `/skills` 打开技能选择器弹窗，含“本地技能 / 远程仓库”双标签页，可直接浏览、安装、更新、删除；`/skills list`、`/skills search <关键词>` 等子命令与命令行等价。

## 创建自己的技能

智能体可通过 `skill_create` 工具创建技能包（写入 SKILL.md），通过 `skill_upload` 上传到服务端共享。手工创建时，把技能目录放入上述任一发现目录即可被自动发现。