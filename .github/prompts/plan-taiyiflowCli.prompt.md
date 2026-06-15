## Plan: Taiyiflow CLI 客户端能力对标 Hermes

目标：仅对标 Hermes 的客户端 CLI 能力（不含 gateway/服务端能力），在保持“太乙后端 stream 为真值”的前提下，补齐本地代码任务闭环能力与 skill 扩展能力。推荐路径：先工具闭环，再 skill，再交互收敛。

**Steps**
1. Phase A 能力基线与边界锁定（阻塞后续）
1.1 明确对标范围：仅 CLI/TUI + 本地工具 + skill 管理；排除 gateway、多平台消息、服务端调度。
1.2 建立 Hermes→Taiyiflow 能力映射：
- Hermes 已有：read_file、write_file、search_files、glob_files、shell_exec/task、skill。
- Taiyiflow 当前：search_files、shell_exec、TUI 会话与状态栏、基础 slash。
1.3 锁定安全边界：写入/补丁/命令执行统一遵循 approval_mode；默认工作目录沙箱。

2. Phase B 文件工具闭环（最高优先级）
2.1 工具注册扩展（*并行可行*）：在 ClientMCPServers 增加 read_file/glob_files/write_file/file_edit schema 与参数约束。
2.2 工具路由扩展（*依赖 2.1*）：在 HandleClientToolEvent 按 toolName 分发，回调沿用 submitCallback。
2.3 read_file（*依赖 2.2*）：支持 start_line/end_line、最大读取行/字节、二进制检测、UTF-8 安全截断。
2.4 glob_files（*依赖 2.2*）：支持 pattern/includeHidden/kind/maxResults，承担文件名与目录项枚举。
2.5 write_file（*依赖 2.2*）：支持 create/overwrite/append、临时文件原子写、路径安全校验、写后统计。
2.6 file_edit（*依赖 2.2*）：支持 old_string/new_string/replace_all、读取后变更校验与失败诊断。
2.7 search_files 增强（*并行可行*）：补 pattern、glob、outputMode、caseSensitive、isRegexp 与分页字段。

3. Phase C 安全与治理（与 Phase B 并行推进）
3.1 路径防护：拒绝敏感目录（如 ~/.ssh）与越权路径；支持可配置白名单根目录。
3.2 环境变量脱敏：子进程执行时剔除 API Key 等敏感变量，避免工具输出泄露。
3.3 危险命令守卫：对高危 shell 命令加确认与审计。
3.4 错误分级：参数错误/权限拒绝/执行失败/补丁冲突四类统一错误码与文案。

4. Phase D skill 客户端支持
4.1 skill 元数据规范：采用 SKILL.md（YAML frontmatter + markdown body）最小字段：name/description/category/prerequisites。
4.2 本地发现与加载：约定 ~/.snz1dp/skills 目录，启动时扫描、校验、缓存摘要。
4.3 CLI 与 slash 暴露：新增 skills 命令（list/view/enable/disable）与 /skills 展示。
4.4 Stream 透传对齐：将启用技能映射到 RequestedSkillNames 与 ClientSkillPackages 字段。
4.5 执行入口收窄：运行时 skill 工具只负责按名称装载单个技能内容；列表与启停仍由 /skills 命令承担，避免工具职责混杂。

5. Phase E TUI 交互对标（Hermes 风格但保持现架构）
5.1 状态反馈分层：执行中/工具调用/补丁应用进度以状态栏为主，历史区只保留关键里程碑。
5.2 工具摘要展示：展示工具名、目标路径、耗时、结果统计，避免刷屏。
5.3 中断与排队：补充 busy 时输入策略（中断当前任务/排队下一个输入）。
5.4 窄终端降级：小宽度下自动简化状态栏信息，保留核心可见性。

6. Phase F 交付节奏
6.1 MVP（两周）：read_file/glob_files/write_file/file_edit + 安全基线 + TUI 状态分层。
6.2 Beta（一到两周）：skills list/view/enable/disable + RequestedSkillNames 透传 + /skills。
6.3 GA（一周）：审计日志、错误分级完善、文档与回归测试。

**Relevant files**
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go — 工具注册、分发、读写/补丁实现、安全守卫。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go — 流事件到本地工具执行链路、skills 透传字段组装。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go — 状态栏与历史区分层、工具执行反馈、busy 输入策略。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/config/config.go — skills 配置、沙箱根目录、审计开关。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/doctor.go — 文件工具与 skills 诊断输出。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/provider.go — StreamRequest 中 skills 相关字段保持稳定约定。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/provider/taiyiflow/client.go — requested_skill_names/client_skill_packages 请求体透传。
- /Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/specs/taiyiflow-go-client.md — 更新客户端能力边界与验收标准。

**Verification**
1. 工具单测：read/list/write/patch 的参数边界、二进制识别、冲突 patch。
2. 安全单测：敏感路径拒绝、越权路径拒绝、readonly 下写操作拒绝。
3. 集成验证：EventClientTool -> runtime -> submitCallback 全链路。
4. TUI 验证：等待与工具执行期间状态持续可见，历史区无多余空行。
5. skill 验证：skills 命令与 /skills 一致；RequestedSkillNames 透传正确。
6. 构建验证：gofmt、go build、doctor 全通过。

**Decisions**
- 包含：客户端工具闭环、skill 本地管理、TUI 状态反馈、权限与审计。
- 排除：服务端能力改造、gateway/多平台消息、自动 git 提交。
- 依赖：首版协议以后端 stream 为准，不引入新的服务端接口假设。

**Further Considerations**
1. Patch 格式选择：Option A unified diff 子集；Option B 自定义 patch DSL。推荐 A（兼容现有研发习惯）。
2. skill 存储：Option A ~/.snz1dp/skills；Option B 仓库内 .snz1dp/skills。推荐 A，全局可复用。
3. Busy 输入策略：Option A 默认“排队”；Option B 默认“中断”。推荐 A，减少误中断。