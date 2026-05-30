## Plan: 命令层与内置技能对标

基于当前代码真值，taiyiflow-cli 的基础本地工具已经覆盖首轮代码任务闭环；下一阶段应从“补同名工具”切换为“补命令工作流层、任务持久化、terminal_capture、细粒度权限规则”，并把缺省内置技能一并随 CLI 程序打包发布。推荐把“默认 bundled skills 打包”和“命令层/任务治理补齐”并行推进；实现上允许分层降级，但目标不是只做低耦合样例，而是把缺省内置技能全集纳入 CLI 自身产物。

**Current Status**
- 已完成 bundled skills 的第一轮闭环实现：新增内置技能注册表、嵌入式 SKILL.md 资产、Discover/LoadByName/ClientPackages 合并发现。
- 已完成 bundled 默认启用语义，并新增 `skills.disabled` 显式禁用列表；`skills enable/disable` 已可对 bundled skill 生效。
- 已完成 `loadSkillPayload` 对 bundled skills 的接线；命令层与 runtime 现在都能看到 bundled 来源的技能包。
- 已完成 H5 的首版参考文件提取目录：带 `files` frontmatter 的 bundled skill 会把嵌入资源提取到 `SNZ1DP_HOME/bundled-skills/<skill-name>/`，并通过 `root_dir`、`files` 暴露给 payload 与 runtime。
- 已完成 H6 的首版命令层分层展示：`/skills list/view` 已按 bundled/workspace/user/custom 分组显示，并展示状态、参考文件与裁剪/受限标签；TUI 复用同一份 slash 输出。
- 已完成 H6 的专门 TUI picker/panel：输入 `/skills` 或 `/skills list` 会打开技能列表弹层，支持方向键选择、Enter 打开技能详情面板。
- 已完成 Phase D 的首版任务持久化：后台任务现在会把 metadata 与 stdout/stderr 落到 `SNZ1DP_HOME/tasks/`，并保留 `flow_id`、`session_id`、`run_id`、`cwd` 等上下文；新建 manager 后可恢复 `task_list` / `task_output`。
- 已完成 Phase E 的首版 `terminal_capture`：在 `task_output` 之外新增终端视角输出读取，支持读取 stdout/stderr/both，并已接入 TUI panel 与 `/tasks capture`。
- 已完成 `/tasks` 命令首版：交互式 CLI 与 TUI 都支持 `/tasks [list|output|capture|stop|remove] <task_id>`，复用现有后台任务与持久化输出，并支持清理已结束任务及其持久化输出。
- 已完成 `/permissions` 等价入口首版：交互式 CLI 与 TUI 都支持 `/permissions show`、`/permissions set approval|sandbox|roots ...`、`/permissions clear roots`，统一操作当前本地审批与外部资源访问配置。
- 已完成细粒度权限规则首版：本地配置新增 `permission_rules`，支持 allow/ask/deny 三态、按工具名或命令前缀匹配、规则持久化，以及 `/permissions rule add/remove` 的 CLI/TUI 入口；runtime 已把规则接入 `shell_exec` 与客户端工具授权链。
- 已完成 `/review` 工作流命令首版：交互式 CLI 与 TUI 都支持 `/review [路径或范围说明]`，并复用现有 agent 请求模式与会话上下文发起代码审查。
- 已补 bundled skills 相关单元测试，覆盖默认发现、同名覆盖、禁用语义、参考文件提取和 payload 输出一致性。
- 已把 `claudeApi`、`claudeInChrome` 重命名为 `taiyiflow-api`、`taiyiflow-in-chrome`，不再保留旧名。
- 命令层、任务治理、内置技能与细粒度权限规则的首轮闭环已完成；后续重点转向规则细化、review 扩展能力与更高保真度的 claude-code-haha 行为对齐。

**Steps**
1. Phase A: 对标基线重置。更新现有对标文档与规格，把 shell_exec、task_output/task_stop/task_list、todo_write、lsp_query、file_edit 从“核心缺口”移出，重新定义当前差距为：/review 类工作流命令、/permissions 或等价权限入口、任务持久化、terminal_capture、细粒度权限规则、完整 Task CRUD，以及“缺省内置技能随 CLI 打包”的能力。这个步骤阻塞后续设计，避免继续按过时缺口推进。
2. Phase B: 内置技能清单冻结。盘点 claude-code-haha 当前默认 bundled skills 和 skills dir 机制，冻结一份“缺省内置技能清单”，明确每个技能在 taiyiflow-cli 中的落地形态：原样复刻、裁剪后复刻、占位打包但降级能力。该阶段与步骤 3 并行，但阻塞技能打包实现。
3. Phase C: 命令层对标设计。对齐当前 slash 命令与 TUI 命令清单，明确第一批新增命令建议为 /review、/permissions、/tasks。/review 负责把现有工具链组织成代码审查工作流，/permissions 提供规则查看与切换入口，/tasks 提供后台任务查看、重连和操作入口。该阶段依赖步骤 1，可与步骤 4 的数据模型设计并行。
4. Phase D: 任务持久化模型。把当前仅驻留在 sharedLocalTaskManager 的任务态扩展为会话级或本地持久化模型，至少补：任务元数据落盘、任务输出文件、CLI 重启后的任务列表恢复、task_id 与 session_id/workdir 关联。优先沿用 SessionStore 的落盘方式扩展新 store，而不是直接重写 runtime 执行层。该阶段阻塞 /tasks 命令落地与 terminal_capture。
5. Phase E: terminal_capture 设计。基于当前 shell_exec 背景任务 stdout/stderr 缓冲，补一个专门的终端输出读取工具或等价命令，明确与 task_output 的边界：task_output 面向结构化后台任务状态，terminal_capture 面向终端视角的持续输出读取或最近输出片段。若不需要引入真实 pty，首版可先基于后台任务输出文件实现裁剪版。
6. Phase F: 更细粒度权限规则。把当前 approval_mode + sandbox 边界扩展为规则系统，至少支持 allow/ask/deny 三态、按工具名或命令前缀匹配、规则持久化、TUI/CLI 查看与修改。shell_exec 的规则判断建议先做字符串/前缀匹配，后续再评估是否需要像 claude-code-haha 一样引入更细的 shell AST 分析。该阶段与步骤 3 并行设计，但阻塞 /permissions 命令闭环，也决定内置技能中 allowed-tools 的最终可用度。
7. Phase G: review 工作流命令。基于现有 search_files、read_file、file_edit、lsp_query、skill 工具，加一个 /review 工作流命令，而不是先追求完整复刻 claude-code-haha 的全量命令族。首版目标是“对当前改动或指定路径发起审查”，并复用现有 TUI 面板和会话上下文。
8. Phase H: Bundled skill 体系实现。为 taiyiflow-cli 增加一个内置 skill 注册层，支持随二进制打包的 skill 内容、可选参考文件提取到本地目录、统一通过现有 skill 命令与 runtime skill 工具发现。这里的目标是让缺省内置技能全集随 CLI 发布，而不是只打包少量示例技能。
9. Phase I: 内置技能分层落地。对冻结清单中的每个默认技能逐项落地：纯 markdown/frontmatter/参数替换类直接复刻；依赖 files 提取的技能通过受控缓存目录复刻；依赖 allowed-tools、shell hooks、fork context、插件系统的技能先以裁剪模式打包，并在元数据中显式标注受限行为。这样可以实现“内置技能全集打包”与“高级能力逐步追平”并存。
10. Phase J: 验收与文档同步。同步更新规格、对标计划和用户文档，补测试矩阵：命令级、持久化级、权限规则级、skill 装载级。确认新增能力仍符合“单入口主命令 + 少量辅助命令 + 首版不做过度通用抽象”的仓库基线。

**Bundled Skills Matrix**
| 技能 | 来源 | 类型 | 主要用途 | 关键依赖 | 复刻策略 | 首批阶段 |
| --- | --- | --- | --- | --- | --- | --- |
| `update-config` | `claude-code-haha/src/skills/bundled/update-config.ts` | 默认内置 | 更新 CLI 配置 | 本地配置文件读写 | 裁剪复刻 | Phase H-I |
| `keybindings` | `claude-code-haha/src/skills/bundled/keybindings.ts` | 默认内置 | 查看或讲解快捷键 | 静态键位信息 | 直接复刻 | Phase H-I |
| `verify` | `claude-code-haha/src/skills/bundled/verify.ts` | 默认内置 | 校验输出或流程完成度 | Prompt 模板、可能依赖本地读写 | 裁剪复刻 | Phase I |
| `debug` | `claude-code-haha/src/skills/bundled/debug.ts` | 默认内置 | 读取并分析当前会话调试日志 | 本地日志路径、Read/Grep/Glob 类能力 | 直接复刻 | Phase H-I |
| `loremipsum` | `claude-code-haha/src/skills/bundled/loremipsum.ts` | 默认内置 | 示例或占位文本生成 | 纯 Prompt | 直接复刻或不启用 | Phase I |
| `skillify` | `claude-code-haha/src/skills/bundled/skillify.ts` | 默认内置 | 把当前流程沉淀为新技能 | Session memory、AskUserQuestion、文件写入 | 裁剪复刻 | Phase I |
| `remember` | `claude-code-haha/src/skills/bundled/remember.ts` | 默认内置 | 记忆整理与提升 | 会话记忆、项目记忆写入 | 裁剪复刻 | Phase I |
| `simplify` | `claude-code-haha/src/skills/bundled/simplify.ts` | 默认内置 | 代码与流程简化 | 代码审查工作流、可能依赖 agent/tool 编排 | 裁剪复刻 | Phase I |
| `batch` | `claude-code-haha/src/skills/bundled/batch.ts` | 默认内置 | 批量并行任务编排 | worktree、多任务/多 agent | 占位打包后续补齐 | Phase I |
| `stuck` | `claude-code-haha/src/skills/bundled/stuck.ts` | 默认内置 | 卡住场景诊断 | 运行时诊断、外部集成 | 占位打包后续补齐 | Phase I |
| `dream` | `claude-code-haha/src/skills/bundled/dream.ts` | 条件内置 | 特性门控技能 | feature flag | 占位打包或后置 | 后续 |
| `hunter` | `claude-code-haha/src/skills/bundled/hunter.ts` | 条件内置 | review artifact 相关 | review artifact feature | 占位打包或后置 | 后续 |
| `loop` | `claude-code-haha/src/skills/bundled/loop.ts` | 条件内置 | 循环/定时触发流程 | 调度系统 | 裁剪复刻 | 后续 |
| `schedule-remote-agents` | `claude-code-haha/src/skills/bundled/schedule-remote-agents.ts` | 条件内置 | 远程 agent 调度 | remote agent 基础设施 | 暂不复刻 | 排除 |
| `taiyiflow-api` | `claude-code-haha/src/skills/bundled/claudeApi.ts` | 条件内置 | 太乙智启内置参考文档与 files 提取 | bundled files 提取 | 裁剪复刻为 taiyiflow 文档技能 | Phase I |
| `taiyiflow-in-chrome` | `claude-code-haha/src/skills/bundled/claudeInChrome.ts` | 条件内置 | 太乙智启浏览器场景 | Chrome 集成 | 暂不复刻 | 排除 |
| `run-skill-generator` | `claude-code-haha/src/skills/bundled/run-skill-generator.ts` | 条件内置 | 生成或运行技能 | skill generator 基础设施 | 占位打包后续补齐 | 后续 |

**Bundled Skill Registration Breakdown**
1. Phase H1: 注册表与嵌入层。
   文件：`internal/skills/bundled_registry.go`、`internal/skills/bundled_assets.go`、`internal/skills/builtin/**/SKILL.md`。
   职责：定义 `BundledSkillDefinition`、注册函数、`//go:embed` 入口、内置技能元数据与正文载入。
2. Phase H2: 发现链路合并。
   文件：`internal/skills/skills.go`。
   职责：把 bundled skills 合并进 `Discover`/`LoadByName`/`Summaries`/`EnabledSkillNames`，明确优先级为“工作区技能覆盖 bundled，同名用户技能再覆盖 bundled 或工作区默认项”。
3. Phase H3: 启停与配置语义。
   文件：`internal/config/config.go`、`cmd/skills.go`。
   职责：把当前 `skills.enabled` 从“白名单启用”调整为可兼容 bundled 默认开启的模型；建议新增 bundled 技能显式禁用语义，避免默认打包后必须逐个 enable 才能生效。
4. Phase H4: runtime 与 payload 接线。
   文件：`cmd/skills.go`、`internal/runtime/local_tools.go`。
   职责：确保 `loadSkillPayload` 和 runtime `skill` 工具既能发现磁盘技能，也能发现 bundled skills；`source`、`path`、`root_dir` 等字段要能表示 bundled 来源。
5. Phase H5: files 提取与缓存目录。
   文件：`internal/skills/bundled_extract.go`、`internal/config/config.go`。
   职责：为依赖参考文件的 bundled skill 提供受控提取目录，建议落在 root dir 下的 `bundled-skills/` 或 `cache/skills/`；同时补目录解析辅助函数。
6. Phase H6: /skills 与展示层。
   文件：`cmd/skills.go`、`cmd/tui.go`、`internal/tui/app.go`。
   职责：在 `/skills list/view/enable/disable` 和 TUI 中清晰区分 bundled/workspace/user skills，必要时展示“裁剪实现”或“受限能力”标记。
7. Phase H7: 测试与覆盖关系。
   文件：`internal/skills/skills_test.go`、`cmd/config_test.go`、`cmd/root_test.go` 或新增 `cmd/skills_test.go`。
   职责：覆盖 bundled 发现、同名覆盖顺序、默认启用/禁用语义、files 提取、`loadSkillPayload` 输出一致性。

**Relevant files**
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go` — 当前交互式 CLI 命令分发与本地命令帮助；/review、/permissions、/tasks 的非 TUI 入口可复用这里的 slash 命令结构。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go` — 当前 TUI 命令处理、确认框、工具结果面板；新增命令与任务/权限面板需落这里。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go` — slash 命令补全、选择器状态、交互流程；/tasks、/permissions 及 review 结果展示建议复用现有 picker/panel 模式。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/task_tools.go` — 当前 shell_exec/task_output/task_stop/task_list 的进程内实现；任务持久化和 terminal_capture 的主落点。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/agent/session.go` — 当前会话持久化模型已包含 TodoItems；若任务要按 session/workdir 关联，可参考或扩展同类 store 模式。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/security_helpers.go` — 当前 sandbox 与敏感路径基线；更细粒度权限规则的最小安全基线应复用这里的判断。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go` — 当前工具/命令执行统一入口；权限规则与 terminal_capture 不应绕开这里另起执行通道。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/skills/skills.go` — 当前 markdown 技能发现、装载和 client package 输出入口；bundled skill 注册表、内置技能发现顺序和 files 提取机制的最佳接入点。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/skills.go` — 当前 /skills 命令入口；内置 skill 复刻后需要在这里暴露 bundled/local/user skill 的统一 list/view/enable/disable 行为。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/specs/taiyiflow-go-client.md` — 规格基线，需要把“默认内置技能随 CLI 打包”与“命令/权限/持久化/skills 体系补齐”反映进去。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/.github/prompts/plan-cliExtendsClaude.md` — 对标计划文档，需要继续从“工具补齐计划”更新为“命令层与内置技能体系补齐计划”。

**Verification**
1. 命令层矩阵核对：列出 taiyiflow-cli 与 claude-code-haha 的代码任务相关命令对照表，确认首批仅纳入 /review、/permissions、/tasks，不把桌面端、remote、本机操作混入范围。
2. 任务持久化验证：补测试覆盖 CLI 重启后任务列表恢复、task_id 与 session/workdir 对应、输出文件读取、任务完成/取消后的状态一致性。
3. terminal_capture 验证：确认与 task_output 的职责边界清晰，输出裁剪、刷新和错误态在 TUI 与 CLI 中一致。
4. 权限规则验证：覆盖 allow/ask/deny 三态、命令前缀匹配、规则持久化读写、只读模式与会话覆盖关系。
5. 内置技能打包验证：覆盖 bundled skill 注册、内置技能发现优先级、markdown/frontmatter 解析、参数替换、参考文件提取，以及与现有 /skills 和 runtime skill 工具的一致发现。
6. 全量内置技能验收：对冻结清单逐项确认“已原样复刻 / 已裁剪打包 / 已打包但受权限或上下文限制”，避免出现 CLI 发布物与缺省技能清单不一致。
7. 端到端回归：至少回归 `go test ./internal/runtime ./internal/skills ./cmd`，并补一个 TUI/交互级最小人工检查清单。

**Decisions**
- 当前对标边界仍限定在本地 CLI 代码任务面，不纳入 claude-code-haha 的桌面端、remote、channel、本机操作、多智能体编排。
- 命令层优先级仍然很高，但这次明确把“缺省内置技能随 CLI 打包”纳入同优先级主线，不再作为后置增强项。
- 任务持久化默认按本地 store 方案推进，优先与 session/workdir 关联；除非发现必须依赖后台 stream 协议，否则不先改后端。
- terminal_capture 首版可先做后台任务输出视角，不强求一开始就复刻 claude-code-haha 的完整终端/pty 语义。
- 内置技能策略改为“全集打包、分层生效”：所有缺省内置技能都进入 CLI 发布物；其中高级权限、fork、插件依赖能力允许先以裁剪模式交付。

**Further Considerations**
1. /review 的首版范围建议限定为“针对当前工作区变更或指定路径做代码审查”，不要一开始就追 PR 审查、提交建议和多模式 review 族命令。
2. 权限规则的首版建议采用“工具名 + 命令前缀”的规则模型；只有在实际命令绕过案例出现后，再评估引入 shell AST 分析。
3. 若要复刻 claude-code-haha 的 bundled skill 参考文件能力，建议把提取目录放在当前 root dir 下的受控子目录，避免直接引入插件目录和跨目录写入复杂度。
4. 对依赖 allowed-tools、shell hooks、fork context 的内置技能，建议在 skill 元数据里显式暴露“当前为裁剪实现”标记，避免模型或用户误以为已完全追平 claude-code-haha。
