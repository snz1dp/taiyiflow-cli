## Plan: 定时任务守护进程

为模型新增一个本地定时任务工具，调度目标不是 shell_exec，而是复用现有的非交互任务模式（runTaskOnce）去执行模型任务；调度引擎改为单实例独立守护进程，所有 TUI 进程只负责创建/查看，不参与触发，从而满足“默认继承发起主会话”“只有一个后台调度进程”“多个 TUI 可查看定时任务”这三个约束。

**Steps**
1. 阶段一：收口模型侧调度契约。新增本地工具 `task_schedule`，输入至少包含任务提示词、任务名、工作目录，以及四类互斥调度参数中的一种：`delay_seconds`、`run_at`、`interval_seconds`、`cron_expr`；可选 `timezone`、`request_mode`、`flow_id`、`session_id`。这里直接复用 `enrichToolArgumentsWithContext` 的 flow/session/run/workdir 注入逻辑，确保未显式传入时默认落到发起当前对话的主会话。此步先冻结输入输出契约，再进入后续实现。 
2. 阶段二：扩展任务持久化模型。以现有 `persistedTaskState` 为基线，增加 `entry_type`（`run` / `schedule`）、`schedule_type`（`once` / `interval` / `cron`）、`scheduled_at`、`next_run_at`、`last_run_at`、`last_run_task_id`、`request_mode`、`prompt`、`timezone`、`disabled`、`daemon_pid` 等字段。保持 stdout/stderr 仅属于实际执行的 run 记录，schedule 记录只维护调度元数据和最近一次子任务关联，避免周期任务覆盖历史输出。此步依赖第 1 步。 
3. 阶段三：引入单实例调度守护进程。新增一个独立命令入口（建议 `scheduler run`，实现风格参考 `browser host`），在本地根目录写锁文件或占用文件锁，确保同一时刻只有一个守护进程持有调度权。创建定时任务时只写入 schedule 记录并确保守护进程已启动；多个 TUI 进程都只能读取记录，不能竞争触发。守护进程启动后需要扫描持久化 schedule，恢复 `next_run_at`，补处理已到期但尚未触发的单次任务，并持续计算 interval/cron 的下一次触发时间。此步依赖第 2 步。 
4. 阶段四：把调度触发桥接到现有 task 模式。将 `App.runTaskOnce` 周边的“认证、默认 flow、prepareSession、applyExplicitTaskSessionID、非交互 runtime”收口为可复用执行路径，供守护进程按 schedule 记录直接调用。每次真实触发都生成一个新的 run 记录，复用现有后台任务的输出持久化和状态跟踪；父 schedule 记录只更新 `last_run_task_id`、`last_run_at`、`next_run_at`、最近错误和活动状态。这样既满足“后台任务可以是当前进程的 task 模式”，又不破坏现有 `task_output` 对 stdout/stderr 的假设。此步依赖第 3 步。 
5. 阶段五：扩展本地读工具与 TUI 展示。让 `task_list` 同时返回 run 与 schedule，两类条目共享列表接口，但新增 `entry_type`、`schedule_summary`、`next_run_at`、`last_run_task_id` 等字段；`task_output` 对 run 继续返回 stdout/stderr，对 schedule 返回调度详情与最近一次子任务引用。TUI 侧复用现有 `/tasks`、`buildTUITasksPicker`、`TasksPickerState`，在列表和详情中展示“类型、计划规则、下次触发、上次执行、所属 session”。可追加 `/tasks scheduled` 过滤别名，但不在首版引入“编辑 cron/间隔”UI。此步依赖第 2 步和第 4 步；其中列表字段扩展可与第 3 步并行推进。 
6. 阶段六：补齐权限、帮助文本和回归测试。把 `task_schedule` 纳入本地工具注册、权限判定和 TUI 帮助文案；为 schedule->run 桥接、单实例锁、多进程可见性、默认会话继承、周期任务重复触发准确性补窄测和命令层测试。此步依赖前 5 步，但测试脚手架可提前并行准备。

**Relevant files**
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/local_tools.go` — 新增 `task_schedule` 定义，扩展 `task_list` / `task_output` 返回字段，并把帮助文本、工具描述、危险工具分类与权限入口对齐。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/client_runtime.go` — 在 `executeClientToolPayload` 中接入 `task_schedule`，继续复用 `enrichToolArgumentsWithContext` 做默认 flow/session/workdir 注入。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/task_store.go` — 扩展 schedule/run 统一持久化模型、恢复逻辑、排序与查找逻辑。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/task_tools.go` — 现有 run 型后台任务实现基线；需要抽出可复用的 task summary/detail 结构，并接入 schedule 创建、查询与可能的停用逻辑。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root.go` — `runTaskOnce`、`prepareSession`、`applyExplicitTaskSessionID` 是守护进程复用当前 task 模式的核心参考面。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/browser.go` — `runBrowserHost` 是独立常驻子命令的最近实现模板，可直接借鉴守护进程命令组织方式。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui.go` — `/tasks` 命令、`buildTUITasksPicker`、工具结果面板都要扩展 schedule 维度。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/tui/app.go` — `TaskItem`、`TasksPickerState` 需要增加 schedule 展示字段。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/agent/session.go` — 会话状态持久化真值来源，定时执行默认回到发起主会话时要对齐这里的 session/workdir 语义。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/task_tools_test.go`、`/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/root_test.go`、`/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/tui_test.go` — 覆盖工具契约、守护进程执行桥接、以及 TUI 多进程查看回归。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/cmd/scheduler.go`（新增） — 独立守护进程命令入口与启动/锁持有逻辑。
- `/Users/neeker/工作/技术开发平台/人工智能平台/太乙智启/taiyiflow-cli/internal/runtime/task_scheduler.go`（新增） — 调度循环、cron/interval 计算、到期触发与恢复逻辑。

**Verification**
1. 运行 runtime 单测：验证 `task_schedule` 在未显式传入 `session_id` 时，会从 payload context 继承当前主会话，并把 schedule 记录正确持久化。
2. 运行 store/daemon 窄测：在同一 root 目录下并发启动两个守护进程，断言只有一个拿到锁，且到期 schedule 只触发一次。
3. 运行命令层测试：验证守护进程触发模型任务时走 `runTaskOnce` 的非交互路径，并通过 `applyExplicitTaskSessionID` 回到发起 session。
4. 运行周期任务测试：分别覆盖 `delay_seconds`、`run_at`、`interval_seconds`、`cron_expr`，校验 `next_run_at`、`last_run_at`、`last_run_task_id` 的更新与重复触发准确性。
5. 运行 TUI 测试：用两个独立 App/TUI 实例指向同一临时 root，一边创建 schedule，一边执行 `/tasks`，确认 schedule 条目可见且不会由查看进程重复执行。
6. 手工验证：启动 TUI A 创建一次性任务和周期任务，再打开 TUI B 查看 `/tasks`；等待触发后确认子 run 产生输出，schedule 条目的最近运行信息同步更新。

**Decisions**
- 已确认：首版同时支持一次性调度和周期调度；周期调度同时接受 `interval_seconds` 与 `cron_expr`。
- 已确认：调度引擎是独立守护进程；TUI 进程只负责创建与查看，不负责竞争调度权。
- 已确认：默认会话使用发起当前工具调用的主会话；若后续需要显式切换 session，再作为附加参数能力扩展。
- 推荐：`run_at` 采用带时区偏移的 RFC3339 字符串，`cron_expr` 可选 `timezone`，未提供时默认使用守护进程本地时区。
- 明确不纳入首版：操作系统级自启动/登录项托管、跨机器分布式调度、多实例 leader 选举以外的网络协调、TUI 内编辑已有 schedule 规则。
