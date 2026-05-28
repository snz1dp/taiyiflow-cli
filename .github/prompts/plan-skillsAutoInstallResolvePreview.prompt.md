## Plan: CLI 技能自动补装与预览接线

在 taiyiflow-cli 中新增一次请求前的“技能预解析（resolve preview）→ 远程搜索候选 → 按策略确认安装 → 重新装配本地技能载荷”闭环，并把 Provider 扩展到后端 /api/v1/agents/flows/{flow_id_or_name}/sessions/{session_id}/skills/resolve。推荐做法是把自动补装放在 stream 前的公共预处理层，而不是塞进 loadSkillPayload 本身：loadSkillPayload 继续只负责发现并上送当前本地技能；新的预处理层负责调用后端预览、识别缺失技能、执行远程 registry 搜索和安装，再刷新 skill payload。自动搜索范围按用户已确认的“包含自由文本意图匹配”，自动安装目录按现有 skills.install_scope 配置决定。首版仍应把自动安装默认策略设计为 confirm，避免静默把任意新技能永久纳入后续渐进披露集合。

**Steps**
1. 阶段一，扩展 Provider 预览能力。为 CLI Provider 增加技能预览请求/响应类型，并在 Provider 接口中新增 resolve 预览方法；taiyiflow 客户端实现 POST /api/v1/agents/flows/{flow_id_or_name}/sessions/{session_id}/skills/resolve，请求体对齐后端 SkillResolveRequest，响应体对齐 SkillResolvePreviewResponse。这里要明确保留 request_type、command_execution_target、available_tools、client_skill_packages、requested_skill_names 等字段，并由 URL path 提供 flow_id 与 session_id、由登录上下文提供 owner_user_id，保证预览结果与真实 stream 上下文一致。该步骤阻塞后续自动补装闭环。
2. 阶段二，抽取请求前公共预处理。新增 App 级别的技能自动解析入口，例如 ensureSkillSupplyBeforeStream 之类的公共函数，由交互式 CLI 的 streamTask 和 TUI 的 runner 在调用 loadSkillPayload 前共同使用。该函数应先基于当前会话 ContextDir 与最新 config 生成一份“本地技能快照”，再调用 Provider resolve 预览，获得 missing_skill_names 与已解析技能信息；如果没有缺失技能，直接返回原始 skill payload。该步骤依赖步骤 1。
3. 阶段三，实现缺失技能候选生成与自由文本补全策略。对于后端返回的 missing_skill_names，先走精确匹配搜索；对于仅有自由文本任务、但本地和后端都无法命中显式技能名的情况，再按任务文本调用 registry 搜索，生成候选列表并做本地去重、已安装过滤、分数排序。这里建议把“显式缺失技能名补装”和“自由文本推荐搜索”拆成两个子路径：前者优先级更高、置信度更强；后者仅在策略允许时进入推荐或确认。该步骤与步骤 4 可并行设计，但实现上通常先完成精确缺失路径再加自由文本意图匹配。
4. 阶段四，落地自动安装策略与交互。增加技能自动安装配置，例如 skills.auto_install_policy（建议 off|confirm|allow）和可选的自由文本搜索开关；默认策略建议 confirm。交互式 CLI 中，在 confirm 模式下输出明确确认提示并接收用户输入；TUI 中复用现有 ToolPanel/确认对话样式。安装目标目录继续遵循 skills.install_scope；若为 workspace，则必须使用当前会话 ContextDir 对应的 .snz1dp/skills。安装成功后立即重新执行本地 Discover/loadSkillPayload，使本轮请求能带上新技能，不需要重启会话。该步骤依赖步骤 2，可与步骤 3 交叉推进。
5. 阶段五，控制自动补装后的作用域与会话状态。由于当前技能默认不是白名单模式，自动安装后的技能会进入后续候选集合，因此需要显式设计作用域控制。推荐做法是区分“安装到磁盘”与“本会话自动确认过的技能”：前者由 install_scope 决定落盘位置，后者通过 SessionState 记录自动确认或已批准的技能名，避免后续 skill review 再次弹确认，并为之后的策略细化预留基础。必要时补充会话字段或复用 ApprovedSkillNames，并确保 /new、会话切换、ContextDir 切换后的行为明确。该步骤依赖步骤 4。
6. 阶段六，补充 CLI/TUI 命令与可观测性。更新 /skills help、skills help、交互帮助文案与配置说明，明确自动安装策略、自由文本匹配边界、install_scope 跟随规则，以及自动补装失败时的降级路径。建议在 show-stream-events 或系统提示里增加“resolve 预览结果”“自动搜索命中”“安装成功/跳过”的可观测日志，便于联调。该步骤可与步骤 4、5 并行完成。
7. 阶段七，完善测试矩阵。CLI 侧新增 Provider resolve 单元测试、stream 前预处理测试、自动安装后本轮请求带上新技能的请求装配测试、ContextDir/workspace install_scope 场景测试、自由文本搜索命中/歧义/拒绝安装/无命中场景测试。TUI 侧新增确认弹窗与安装后刷新 picker/请求快照测试。后端侧若接口字段不变可只做回归校验；若要补充 metadata 字段，再同步更新 schema 与测试。该步骤依赖前述实现完成。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go — 新增 resolve 预览请求/响应类型与 Provider 接口方法。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go — 实现 POST /api/v1/agents/flows/{flow_id_or_name}/sessions/{session_id}/skills/resolve，并对齐请求/响应 JSON。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client_test.go — Provider resolve 预览接口回归测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go — 交互式 CLI stream 前接入公共自动补装预处理。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — TUI 请求发送前接入同一预处理，并补 TUI 确认/提示反馈。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/skills.go — 保持 loadSkillPayload 只负责本地载荷装配，同时新增自动策略帮助文案或共用辅助函数。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/registry.go — 复用远程 SearchRemote/InstallRemote，必要时补充自由文本候选排序辅助逻辑。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go — 增加自动安装策略配置项，并定义默认值与归一化函数。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/agent/session.go — 如需区分自动批准技能，补充或复用会话级状态持久化。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/skills_test.go — loadSkillPayload 与自动补装后的即时生效测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go — streamTask 请求装配与自动补装闭环测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go — TUI 自动补装确认与技能审批联动测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/agent_chat.py — 会话级 resolve 预览接口真值来源，负责从 path 与当前用户收口上下文。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/api/v1/schemas.py — SkillResolveRequest/SkillResolvePreviewResponse 字段真值来源。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow/src/backend/base/taiyiflow/services/agent/service.py — resolve_skills/preview_skill_resolution 能力边界与 missing_skill_names 语义来源。

**Verification**
1. 为 Provider 新增单测，验证 resolve 请求 URL 会携带 flow_id、session_id，请求体只携带 request_type、command_execution_target、available_tools、requested_skill_names、client_skill_packages，并正确解析 missing_skill_names/allowed_tools。
2. 在 taiyiflow-cli 里新增请求前预处理测试：本地已具备技能时不触发安装；后端返回 missing_skill_names 时按策略进入搜索/安装；安装成功后同一轮请求的 RequestedSkillNames 与 ClientSkillPackages 立即包含新技能。
3. 覆盖 ContextDir 与 install_scope：在 workspace scope 下自动安装应落到当前 ContextDir/.snz1dp/skills，切换 ContextDir 后不应误写到旧目录；user scope 则落到用户目录。
4. 覆盖自由文本路径：命中唯一高置信候选时在 confirm/allow 下可继续；多候选歧义、零候选、用户拒绝、安装失败都要安全降级到原始请求，不得阻塞普通对话。
5. 覆盖 TUI 行为：确认框、系统提示、安装后刷新 skills picker 与下一次请求快照；必要时复用现有 skills picker 测试夹具。
6. 运行聚焦测试命令，例如 go test ./cmd ./internal/provider/taiyiflow ./internal/agent；若实现跨包较多，再补一次 go test ./... 或至少执行工作区已有 taiyiflow-cli build 任务确认编译通过。
7. 与后端联调时，使用真实 /api/v1/agents/flows/{flow_id_or_name}/sessions/{session_id}/skills/resolve 校验 CLI 嵌套 client_skill_packages 仍可被后端正确展开，不会把已上传本地技能误判为 missing。

**Decisions**
- 自动搜索范围：包含自由文本意图匹配，但实现顺序应先做显式 missing_skill_names 的高置信补装，再叠加自由文本候选推荐。
- 自动安装作用域：跟随现有 skills.install_scope，不额外引入独立的 auto_install_scope。
- 公共抽象边界：loadSkillPayload 保持纯本地装配职责；自动搜索/安装/确认/刷新由新的 stream 前预处理层负责，供 CLI 与 TUI 复用。
- 兼容性边界：后端现有会话级 /agents/flows/{flow_id_or_name}/sessions/{session_id}/skills/resolve 已足够支撑“缺什么技能”的判定，但不足以直接告诉 CLI “应从哪个远程 slug 安装”，因此自由文本搜索与 registry 候选选择仍由 CLI 负责。
- 安全边界：首版默认策略建议 confirm；即使支持 allow，也应保留歧义候选不自动安装、失败即降级、不修改无关配置的原则。

**Further Considerations**
1. 若后续需要更稳的自动补装命中率，可在后端 SkillResolvePreviewResponse 中补充 missing skill metadata（例如推荐 slug 或 registry source）；首版不阻塞 CLI 落地，但会影响自由文本路径的准确率。
2. 若后续发现自动安装导致技能集合持续膨胀，可再引入“会话级临时启用但不持久落盘”或“仅本轮附加技能”模式；本次改造先不扩到这一步。
