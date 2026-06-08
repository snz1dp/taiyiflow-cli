---
name: run-skill-generator
description: 当需要批量创建或调整技能文件时，先统一技能结构和元数据约定，再生成最小可维护的模板集合。
category: workflow
metadata:
  request_types:
    - agent
---

当需要批量创建或调整技能文件时，先统一技能结构和元数据约定，再生成最小可维护的模板集合。

技能文件结构约定：
- 每个技能是一个目录，包含一个 `SKILL.md` 文件。
- `SKILL.md` 采用 YAML frontmatter + Markdown body 格式。
- frontmatter 必须包含 `name`、`description`、`category`。
- 可选字段：`version`、`metadata.request_types`（控制技能在哪些请求类型下可用）、`files`（附属文件）、`prerequisites`（前置条件）。
- `description` 应场景导向（"当…时使用"），不要实现导向。

生成原则：
- 每个技能的描述要明确回答"什么场景下应该触发这个技能"。
- 正文部分包含具体的行为准则和输出格式，不要只写一句概括。
- 如果技能只在特定请求类型下有意义，必须在 `metadata.request_types` 中声明。
- 避免创建功能重叠的技能，优先扩展已有技能的覆盖范围。
