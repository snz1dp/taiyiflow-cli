## Plan: 技能仓库与供应修正

围绕 taiyiflow-cli 的技能远程仓库与技能供应链路，建议分四个阶段推进：先补远程仓库列表数据契约与渲染字段，再修正 TUI/CLI 的安装目标选择与全局配置语义，最后把技能自动供应改成跟随当前有效工作区/上下文目录，而不是只依赖持久化 config.workdir。这样可以一次性覆盖 ESC 取消搜索、默认列表摘要、人气/下载量展示、安装目标选择、以及“当前工作区技能包未自动供应”的根因。

**Steps**
1. 阶段一，梳理并扩展远程技能仓库数据契约。调整 internal/skills/registry.go 的 RemoteSearchResult 与 remoteSearchResponse，把 summary、popularity、downloads 作为远程搜索结果的稳定字段；考虑 popularity/downloads 为可选字段，客户端在字段缺失时隐藏展示，避免外部镜像站尚未联调时直接报错。此步骤还需要同步 cmd/tui.go 的 buildTUISkillsPicker 映射和 internal/tui/app.go 的 RemoteSkillItem 结构。该步骤阻塞后续列表展示改动。
2. 阶段二，修正技能远程仓库界面交互。修改 internal/tui/app.go 的 handleSkillsPickerSearchKey、renderSkillsPicker、相关状态结构，使 ESC 在 SearchMode 下同时退出输入模式并恢复默认远程列表视图；默认远程列表与搜索列表共用同一渲染字段集，统一展示 summary、popularity、downloads。若本地列表也要展示“摘要（Summary）”，则在 cmd/tui.go 的 buildTUISkillsPicker 中优先把 skills.Skill.Summary 映射到 tui.SkillItem，并在 renderSkillsPicker 的本地分支优先渲染 summary，description 作为回退。该步骤依赖步骤 1。
3. 阶段三，收敛“技能目录”配置语义并补安装目标选择。将 internal/config/config.go 的 SkillConfig 从自由路径 directory 迁移为“默认安装位置/范围”枚举（user、workspace），保留旧 directory 仅做兼容读取或一次性迁移；同步修改 internal/tui/app.go 的 ConfigFormValues、configField 常量序列、configFormFieldSpecs、configFormFieldValue、configFormValuesFromInputs、isConfigTextField，以及 cmd/tui.go 和 cmd/config.go 的配置读写与摘要显示。TUI 侧在 SkillsPickerState 或 model 中增加“安装目标选择”弹层状态与“本会话不再询问”状态；cmd/tui.go 的 executeTUISkillsPickerCommand 在 install/update/install-detail/update-detail 前先解析候选安装目标，若用户目录与工作区目录都可用，则弹出二选一，默认取全局配置；勾选“不再询问”后，仅在当前 TUI 会话缓存。CLI 侧的 skills install/update 复用同一目标解析函数，在交互式终端下做一次性选择提示；非交互场景按全局默认静默执行。该步骤可与步骤 2 并行设计，但真正落地依赖步骤 1 完成数据结构整理。
4. 阶段四，修复技能自动供应与渐进式披露的工作区感知，并核查技能状态变更后的动态生效。将 cmd/skills.go 的 loadSkillPayload 从只接收 Config，改为显式接收“有效技能工作目录/上下文目录”，并在 cmd/root.go、cmd/tui.go 的 stream 发送路径用 sessionState.ContextDir 或 snapshot.Workdir 传入，而不是只依赖 config.Workdir。internal/skills/skills.go 的 ResolveDirectories、ResolveInstallDirectory、classifySkillSource 需要围绕新的安装范围与有效工作目录做拆分：发现目录跟随当前工作区，用户目录始终是默认用户技能目录，安装目录由 install scope 决定。同时梳理技能新增、删除、更新、disable/enable 后的刷新链路，确认 Discover、buildTUISkillsPicker、executeSkillsCommand、loadSkillPayload 与现有会话中的 skills picker/请求装配是否使用最新状态，必要时增加显式重建或刷新，避免界面已变更但下一次请求仍沿用旧技能集合。这里要补一组回归测试，验证多工作区/切换上下文后，workspace/.snz1dp/skills 下的技能能进入 Discover、EnabledSkillNames、ClientPackages，并最终出现在 provider.StreamRequest 的 RequestedSkillNames 与 ClientSkillPackages 中。该步骤阻塞最终验收。
5. 阶段五，补命令面与文档/帮助文案。更新 cmd/skills.go 的 search/list/install/update 输出，使 CLI 结果与 TUI 字段一致；更新 skillsCommandUsage、skillsSlashUsage、config summary、doctor 输出对“技能目录/安装范围”的文案，避免仍然暴露自由路径语义。若 specs/taiyiflow-go-client.md 与 README.md 中仍写“技能目录可自由配置路径”，同步改成“用户目录/工作区目录”与“远程仓库可展示摘要、人气、下载量（字段由镜像站提供）”。该步骤依赖步骤 3 和步骤 4。
6. 阶段六，完善测试与联调兜底。扩充 internal/tui/app_test.go 中 skills picker 的键位和渲染断言，新增 ESC 取消搜索、默认远程列表摘要/指标展示、安装目标选择弹层与会话记忆；扩充 cmd/tui_test.go 覆盖 buildTUISkillsPicker 对 summary/popularity/downloads 的映射，以及 install-detail 经过目标选择后的刷新；扩充 internal/skills/skills_test.go 和 cmd/skills_test.go 覆盖 install scope、workspace 优先、payload 跟随有效工作目录，以及 CLI 安装目标解析。对外部镜像站协议字段增加可选字段兼容测试，避免联调前阻塞本地开发。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/registry.go — 远程仓库搜索响应、安装目标解析、下载/安装入口。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/skills.go — 技能发现目录、来源判定、渐进式披露候选与 client payload 组装。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go — Skills 配置模型与默认值，负责从“自由目录”迁移到“安装范围”。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — Skills picker 状态、搜索键位、默认/远程列表渲染、配置表单字段定义。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — buildTUISkillsPicker、executeTUISkillsPickerCommand、TUI 本地命令分发、流式请求装配。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/skills.go — CLI skills 子命令输出、loadSkillPayload、帮助文案。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/config.go — 非 TUI 配置写入与摘要输出，需同步安装范围语义。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/doctor.go — 自检输出的技能目录/技能来源说明。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app_test.go — 键位、弹层与渲染回归测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go — skills picker 构建、远程搜索映射、安装刷新测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/skills_test.go — 发现目录顺序、workspace 覆盖、payload 组装测试。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/skills_test.go — loadSkillPayload 与 CLI 安装/更新路径测试。

**Verification**
1. 运行 go test ./internal/tui，重点验证 skills picker 键位、渲染与安装目标弹层状态。
2. 运行 go test ./internal/skills ./cmd，验证远程仓库结果映射、install scope、payload 跟随当前有效工作目录。
3. 手动验证 TUI：打开 /skills，切到远程仓库，输入搜索词后按 ESC 应恢复默认列表；默认列表和搜索列表都能看到摘要、人气、下载量；安装时弹出“用户目录/工作区”选择，勾选“本会话不再询问”后本次 TUI 会话内不再重复询问。
4. 手动验证技能动态生效：在 TUI 与 CLI 中分别执行技能新增、删除、更新、disable/enable 后，立即重新打开 skills picker、重新执行 skills list/view，并发起下一次请求，确认界面展示、Discover 结果、RequestedSkillNames、ClientSkillPackages 都已切换到最新状态，不需要重启 CLI 或重建整个会话。
5. 手动验证 CLI：执行 skills search 和 skills install/skills update，确认输出字段一致；在交互式终端下能选择安装范围，在非交互环境按全局默认执行。
6. 手动验证自动供应：分别在用户目录技能、当前工作区 .snz1dp/skills、切换会话 context_dir 场景下发起一次请求，检查 provider.StreamRequest 中 RequestedSkillNames 与 ClientSkillPackages 是否包含预期技能包。
7. 若外部镜像站尚未提供 popularity/downloads，使用现有 httptest mock 覆盖“有字段/无字段”两种响应，确认客户端兼容。

**Decisions**
- 已确认本次范围包含远程仓库协议字段扩展，以及 TUI 与命令行 skills install 双入口的安装目标选择。
- “本会话不再询问”仅做会话级缓存，不写入持久化配置；持久化默认值仍然由全局 Skills 安装范围决定。
- 远程技能仓库服务不在当前双仓中，客户端需先做可选字段兼容；服务端字段联调属于外部依赖，但不阻塞客户端结构和测试先落地。
- 自动供应修复优先按“当前有效上下文目录”建模，而不是继续把技能发现绑定到静态 config.workdir。

**Further Considerations**
1. 旧版 skills.directory 的迁移策略建议采用“读取兼容、写入新字段、UI 不再暴露旧字段”；如果检测到自定义绝对路径且不等于默认用户目录，可在 config summary/doctor 中给出一次性迁移提示。
2. 人气（popularity）的表现形式建议先按整数热度或浮点分数二选一统一格式化，避免 TUI/CLI 各自解释字段；客户端下载量建议统一显示为“下载 N”。
3. 如果后续确认命令行一问一答体验不足，可追加显式参数如 --install-scope user|workspace，但这不作为本轮阻塞项。
