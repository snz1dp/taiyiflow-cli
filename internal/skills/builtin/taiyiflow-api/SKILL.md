---
name: taiyiflow-api
description: 当需要核对太乙智启平台的接口定义、参数格式、API 行为或文档入口时，先说明当前已知来源，再给出检索路径和需要确认的差异项。
category: docs
files:
  - source: references/taiyiflow-cli-api-notes.md
    path: references/taiyiflow-cli-api-notes.md
    description: 内置接口与技能调用参考。
metadata:
  request_types:
    - ask
    - plan
    - agent
---

当需要核对太乙智启接口、参数或文档入口时，先说明当前已知来源，再给出检索路径和需要确认的差异项。

执行步骤：
1. **确认已知信息**：检查当前上下文中是否已有相关的接口文档、API 笔记或代码注释。
2. **检索补充**：如果已知信息不足，通过以下路径检索：
   - 本地代码中的 API 定义和注释。
   - 内置参考文件 `references/taiyiflow-cli-api-notes.md`。
   - 联网搜索太乙智启官方文档。
3. **标注差异**：如果检索到的信息与当前实现存在差异，明确标注差异点和需要确认的内容。

行为准则：
- 不要凭记忆编造接口参数或返回值格式，必须有代码或文档支撑。
- 如果某个接口的行为不确定，明确标注"需要确认"并给出验证方式。
- 对于已废弃或已变更的接口，标注变更时间和替代方案。
