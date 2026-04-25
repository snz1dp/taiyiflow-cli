## Plan: TUI 多轮答复消息拆分

目标是把“单次用户提问中的多轮 Agent 迭代答复”从当前一条 assistant 拼接，改为按阶段拆成多条 assistant 消息。
你已确认两点：
1. 拆分边界按工具调用阶段。
2. 走全链路改造（含 provider 事件模型消费侧）。

**Steps**
1. 阶段一：定义拆分规范（阻塞后续）
1. 明确主规则：出现工具调用事件后，后续第一段有文本的 assistant 输出新开一条消息。
2. 明确优先级：若事件内有 message_id 或 stage 边界，则优先按 message_id/stage 精确拆分；工具调用规则作为兜底。
3. 明确兼容：缺少元数据时仍可按工具调用边界拆分，不影响旧链路。

2. 阶段二：全链路元数据透传（依赖阶段一）
1. 扩展 TUI 事件结构，让 assistant chunk 事件可携带 message_id、stage、event_type。
2. 在 stream 消费层提取 provider.EventMessage 的 data 元字段并透传给 TUI。
3. 保持 provider 的原始 map 数据兼容，不做破坏式协议调整。

3. 阶段三：TUI 聚合状态机改造（依赖阶段二）
1. 新增拆分状态：如 last_assistant_message_id 与 split_next_assistant。
2. EventToolCall、EventBuiltInCall、EventClientToolCall 到达时，设置 split_next_assistant=true。
3. EventAssistantChunk 到达时按条件判断新开或追加：
1. 新开条件：当前无 assistant 尾条，或 split_next_assistant=true，或 message_id 变化，或明确 stage 边界触发。
2. 其余情况继续追加到当前 assistant 条目。
4. 请求结束、失败、打断时清理拆分状态，防止串到下一轮。

4. 阶段四：历史与状态一致性（依赖阶段三）
1. 校验历史回放不被重新合并，保持多条 assistant 分段展示。
2. 校验会话切换、clear history 后状态归零。
3. 保持 welcome/system/error 的既有行为不变，控制改动边界。

5. 阶段五：测试补齐（与阶段四部分可并行）
1. 在 TUI 测试新增：
1. 无工具调用时仍为单 assistant。
2. 工具调用后后续答复新开 assistant。
3. message_id 变化可触发新开 assistant。
4. done/failed 后状态正确重置。
2. 在 cmd 层新增或补充 stream 到 TUI 事件透传测试，覆盖 message_id/stage/event_type。
3. 全量回归 TUI 相关现有测试，确认无回退。

6. 阶段六：验证验收（依赖阶段五）
1. 自动化测试：go test ./internal/tui。
2. 命令层测试：go test ./cmd（或先跑 TUI/stream 相关子集）。
3. 手工联调：构造“工具调用 -> 文本答复 -> 工具调用 -> 文本答复”，确认界面拆成多条 assistant 消息。
4. 切会话后复验，确认历史与实时行为一致。

**Relevant files**
- taiyiflow-cli/internal/tui/app.go
- taiyiflow-cli/internal/tui/app_test.go
- taiyiflow-cli/cmd/tui.go
- taiyiflow-cli/internal/provider/provider.go
- taiyiflow/src/backend/base/taiyiflow/api/v1/schemas.py
- taiyiflow/src/backend/base/taiyiflow/processing/process.py

**Decisions**
- 拆分规则：按工具调用阶段拆分。
- 改造范围：全链路（含 provider 事件模型消费侧）。
- 本次不包含：后端协议新增字段、数据库结构变更、非 TUI 渲染重构。

## 完成状态（2026-04-25）

1. 阶段一到阶段三已完成。
1.1 已定义并落地拆分规范：工具调用阶段拆分为主规则，message_id/stage 为高优先级边界。
1.2 已完成全链路透传：cmd 侧解析并透传 message_id、stage、event_type 到 TUI。
1.3 已完成 TUI 状态机改造：支持工具调用边界、message_id 变化、stage=end 后续分段；并在 done/failed/切会话/清历史时重置状态。

2. 阶段四已完成。
2.1 已保证历史重建与切会话路径不会污染新一轮拆分状态。
2.2 welcome/system/error 渲染逻辑保持不变。

3. 阶段五已完成。
3.1 已补充 TUI 测试：无边界合并、工具调用拆分、message_id 拆分、stage=end 拆分、done/failed/会话切换状态重置、端到端链路测试。
3.2 已补充 cmd 测试：assistant chunk 元数据透传、id/text 回退、空 chunk 拒绝。

4. 阶段六已完成。
4.1 已执行并通过：go test ./internal/tui。
4.2 已执行并通过：go test ./cmd。
4.3 已执行并通过：go test ./...。

## 验收结论

已达到“一问多轮迭代且有答复内容即拆分”的目标行为：在同一请求链路中，"工具调用 -> 答复 -> 工具调用 -> 答复" 将在 TUI 中呈现为多条 assistant 消息，而不是单条持续拼接。