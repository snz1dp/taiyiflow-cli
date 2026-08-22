package cmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

// runSchedule 定时任务管理子命令入口。
func (a *App) runSchedule(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printScheduleUsage()
		return nil
	}
	switch args[0] {
	case "list", "ls":
		return a.runScheduleList(ctx, args[1:])
	case "create", "new":
		return a.runScheduleCreate(ctx, args[1:])
	case "edit", "update":
		return a.runScheduleEdit(ctx, args[1:])
	case "delete", "rm":
		return a.runScheduleDelete(ctx, args[1:])
	case "enable":
		return a.runScheduleToggle(ctx, args[1:], true)
	case "disable":
		return a.runScheduleToggle(ctx, args[1:], false)
	case "runs", "history":
		return a.runScheduleRuns(ctx, args[1:])
	case "help", "--help", "-h":
		a.printScheduleUsage()
		return nil
	default:
		return fmt.Errorf("未知的 schedule 子命令: %s（可用: list, create, edit, delete, enable, disable, runs）", args[0])
	}
}

func (a *App) printScheduleUsage() {
	fmt.Fprintf(a.stdout, `用法: taiyiflow-cli schedule <子命令> [选项]

定时任务管理

子命令:
  list      列出定时任务
  create    创建定时任务
  edit      编辑定时任务
  delete    删除定时任务
  enable    启用定时任务
  disable   停用定时任务
  runs      查看执行历史

示例:
  taiyiflow-cli schedule list
  taiyiflow-cli schedule create --name "每日构建" --cron "0 9 * * *" --prompt "执行项目构建并报告结果"
  taiyiflow-cli schedule runs <schedule-id>
`)
}

// scheduleProviderClient 构建已认证的 Provider 客户端，同时返回最终生效的配置。
func (a *App) scheduleProviderClient(ctx context.Context) (provider.Provider, internalconfig.Config, error) {
	config, err := a.configManager.Load()
	if err != nil {
		return nil, internalconfig.Config{}, err
	}
	config, _, err = a.ensureClientIdentity(config)
	if err != nil {
		return nil, internalconfig.Config{}, err
	}
	providerClient := buildProviderClient(config)
	config, _, err = a.ensureProviderAuthenticated(ctx, providerClient, config)
	if err != nil {
		return nil, internalconfig.Config{}, err
	}
	return a.providerWithCallbackAuthRefresh(providerClient, &config), config, nil
}

func (a *App) runScheduleList(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("schedule list", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	name := flagSet.String("name", "", "按名称过滤")
	all := flagSet.Bool("all", false, "显示所有任务（含已禁用）")
	templates := flagSet.Bool("templates", false, "仅显示模板任务")
	jsonOutput := flagSet.Bool("json", false, "以 JSON 格式输出")
	limit := flagSet.Int("limit", 50, "最大返回条数")
	if err := flagSet.Parse(args); err != nil {
		return err
	}

	providerClient, _, err := a.scheduleProviderClient(ctx)
	if err != nil {
		return err
	}

	params := provider.ScheduleListParams{
		Name:       *name,
		ClientType: provider.ClientTypeCLI,
		Limit:      *limit,
	}
	if !*all {
		disabled := false
		params.Disabled = &disabled
	}
	if *templates {
		isTemplate := true
		params.IsTemplate = &isTemplate
	}

	page, err := providerClient.ListSchedules(ctx, params)
	if err != nil {
		return err
	}

	if *jsonOutput {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(page)
	}

	if len(page.Data) == 0 {
		fmt.Fprintln(a.stdout, "暂无定时任务。使用 `taiyiflow-cli schedule create` 创建。")
		return nil
	}

	fmt.Fprintf(a.stdout, "%-36s  %-20s  %-10s  %-8s  %-20s  %s\n", "ID", "名称", "调度类型", "状态", "下次执行", "提示词")
	fmt.Fprintln(a.stdout, strings.Repeat("─", 120))
	for _, task := range page.Data {
		status := "✓ 启用"
		if task.Disabled {
			status = "✗ 禁用"
		}
		nextRun := task.NextRunAt
		if nextRun == "" {
			nextRun = "-"
		} else if t, parseErr := time.Parse(time.RFC3339, nextRun); parseErr == nil {
			nextRun = t.Local().Format("2006-01-02 15:04")
		}
		prompt := truncateRunes(task.Prompt, 40)
		fmt.Fprintf(a.stdout, "%-36s  %-20s  %-10s  %-8s  %-20s  %s\n",
			task.ID, task.Name, task.ScheduleType, status, nextRun, prompt)
	}
	fmt.Fprintf(a.stdout, "\n共 %d 条任务\n", page.Total)
	return nil
}

func (a *App) runScheduleCreate(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("schedule create", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	name := flagSet.String("name", "", "任务名称（必填）")
	description := flagSet.String("desc", "", "任务描述")
	prompt := flagSet.String("prompt", "", "执行提示词或命令（必填）")
	requestMode := flagSet.String("mode", "agent", "请求模式（agent|ask|plan|command）")
	cronExpr := flagSet.String("cron", "", "Cron 表达式（5 段）")
	intervalSeconds := flagSet.Int("interval", 0, "间隔秒数（最小 60）")
	runAt := flagSet.String("run-at", "", "一次性执行时间（ISO 8601 或 2006-01-02 15:04）")
	delaySeconds := flagSet.Int("delay", 0, "延迟秒数（一次性）")
	workingDir := flagSet.String("dir", "", "工作目录")
	flowID := flagSet.String("agent", "", "关联智能体 ID")
	sessionID := flagSet.String("session-id", "", "关联会话 ID")
	timezone := flagSet.String("tz", "Asia/Shanghai", "调度时区")
	timeoutSeconds := flagSet.Int("timeout", 300, "执行超时秒数")
	preset := flagSet.String("preset", "", "调度预设（hourly|daily|weekly|monthly）")
	jsonOutput := flagSet.Bool("json", false, "以 JSON 格式输出")
	if err := flagSet.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("创建定时任务需要 --name 参数")
	}
	if strings.TrimSpace(*prompt) == "" {
		return fmt.Errorf("创建定时任务需要 --prompt 参数")
	}

	// 处理预设
	if *preset != "" && *cronExpr == "" {
		switch strings.ToLower(*preset) {
		case "hourly":
			*cronExpr = "0 * * * *"
		case "daily":
			*cronExpr = "0 9 * * *"
		case "weekly":
			*cronExpr = "0 9 * * 1"
		case "monthly":
			*cronExpr = "0 9 1 * *"
		default:
			return fmt.Errorf("不支持的调度预设: %s（可用: hourly, daily, weekly, monthly）", *preset)
		}
	}

	// 确定调度类型
	scheduleType := ""
	if *cronExpr != "" {
		scheduleType = "cron"
	} else if *intervalSeconds > 0 {
		scheduleType = "interval"
	} else if *runAt != "" || *delaySeconds > 0 {
		scheduleType = "once"
	} else {
		return fmt.Errorf("需要指定调度方式: --cron, --interval, --run-at 或 --delay")
	}

	providerClient, config, err := a.scheduleProviderClient(ctx)
	if err != nil {
		return err
	}

	request := provider.ScheduleTaskCreateRequest{
		Name:             strings.TrimSpace(*name),
		Description:      strings.TrimSpace(*description),
		ScheduleType:     scheduleType,
		Prompt:           strings.TrimSpace(*prompt),
		RequestMode:      strings.TrimSpace(*requestMode),
		WorkingDirectory: strings.TrimSpace(*workingDir),
		FlowID:           strings.TrimSpace(*flowID),
		SessionID:        strings.TrimSpace(*sessionID),
		Timezone:         strings.TrimSpace(*timezone),
		ClientType:       provider.ClientTypeCLI,
		ClientID:         strings.TrimSpace(config.ClientID),
		DeviceID:         strings.TrimSpace(config.DeviceID),
		MetadataPayload:  map[string]any{"timeout_seconds": *timeoutSeconds},
	}
	switch scheduleType {
	case "cron":
		request.CronExpr = strings.TrimSpace(*cronExpr)
	case "interval":
		request.IntervalSeconds = *intervalSeconds
	case "once":
		if *runAt != "" {
			request.RunAt = strings.TrimSpace(*runAt)
		}
		if *delaySeconds > 0 {
			request.DelaySeconds = *delaySeconds
		}
	}

	task, err := providerClient.CreateSchedule(ctx, request)
	if err != nil {
		return err
	}

	if *jsonOutput {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(task)
	}

	fmt.Fprintf(a.stdout, "✓ 定时任务已创建\n")
	fmt.Fprintf(a.stdout, "  ID:       %s\n", task.ID)
	fmt.Fprintf(a.stdout, "  名称:     %s\n", task.Name)
	fmt.Fprintf(a.stdout, "  调度类型: %s\n", task.ScheduleType)
	if task.NextRunAt != "" {
		if t, parseErr := time.Parse(time.RFC3339, task.NextRunAt); parseErr == nil {
			fmt.Fprintf(a.stdout, "  下次执行: %s\n", t.Local().Format("2006-01-02 15:04:05"))
		} else {
			fmt.Fprintf(a.stdout, "  下次执行: %s\n", task.NextRunAt)
		}
	}
	fmt.Fprintf(a.stdout, "\n提示: 需要运行 `taiyiflow-cli worker` 驻留进程才能接收执行。\n")
	return nil
}

func (a *App) runScheduleEdit(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("schedule edit", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	name := flagSet.String("name", "", "任务名称")
	description := flagSet.String("desc", "", "任务描述")
	prompt := flagSet.String("prompt", "", "执行提示词或命令")
	requestMode := flagSet.String("mode", "", "请求模式（agent|ask|plan|command）")
	cronExpr := flagSet.String("cron", "", "Cron 表达式")
	intervalSeconds := flagSet.Int("interval", 0, "间隔秒数")
	workingDir := flagSet.String("dir", "", "工作目录")
	timezone := flagSet.String("tz", "", "调度时区")
	jsonOutput := flagSet.Bool("json", false, "以 JSON 格式输出")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	remaining := flagSet.Args()
	if len(remaining) == 0 {
		return fmt.Errorf("编辑定时任务需要指定 schedule_id")
	}
	scheduleID := remaining[0]

	// BUG-5 修复：--cron 和 --interval 互斥，同时指定会产生矛盾的调度配置。
	if *cronExpr != "" && *intervalSeconds > 0 {
		return fmt.Errorf("--cron 与 --interval 不能同时使用")
	}

	providerClient, _, err := a.scheduleProviderClient(ctx)
	if err != nil {
		return err
	}

	request := provider.ScheduleTaskUpdateRequest{}
	hasChange := false
	if *name != "" {
		request.Name = name
		hasChange = true
	}
	if *description != "" {
		request.Description = description
		hasChange = true
	}
	if *prompt != "" {
		request.Prompt = prompt
		hasChange = true
	}
	if *requestMode != "" {
		request.RequestMode = requestMode
		hasChange = true
	}
	if *cronExpr != "" {
		scheduleType := "cron"
		request.ScheduleType = &scheduleType
		request.CronExpr = cronExpr
		hasChange = true
	}
	if *intervalSeconds > 0 {
		scheduleType := "interval"
		request.ScheduleType = &scheduleType
		request.IntervalSeconds = intervalSeconds
		hasChange = true
	}
	if *workingDir != "" {
		request.WorkingDirectory = workingDir
		hasChange = true
	}
	if *timezone != "" {
		request.Timezone = timezone
		hasChange = true
	}
	if !hasChange {
		return fmt.Errorf("未指定任何修改项，使用 --name, --prompt, --cron 等参数")
	}

	task, err := providerClient.UpdateSchedule(ctx, scheduleID, request)
	if err != nil {
		return err
	}

	if *jsonOutput {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(task)
	}
	fmt.Fprintf(a.stdout, "✓ 定时任务已更新: %s (%s)\n", task.Name, task.ID)
	return nil
}

func (a *App) runScheduleDelete(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("schedule delete", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	force := flagSet.Bool("force", false, "跳过确认直接删除")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	remaining := flagSet.Args()
	if len(remaining) == 0 {
		return fmt.Errorf("删除定时任务需要指定 schedule_id")
	}
	scheduleID := remaining[0]

	if !*force {
		fmt.Fprintf(a.stdout, "确认删除定时任务 %s？(y/N): ", scheduleID)
		var confirm string
		// RISK-5 修复：处理读取错误（如 stdin 非交互式或已关闭），
		// 读取失败时视为取消，避免静默继续执行删除。
		if _, scanErr := fmt.Fscanln(a.stdin, &confirm); scanErr != nil {
			fmt.Fprintln(a.stdout, "无法读取确认输入，已取消")
			return nil
		}
		if !strings.EqualFold(strings.TrimSpace(confirm), "y") {
			fmt.Fprintln(a.stdout, "已取消")
			return nil
		}
	}

	providerClient, _, err := a.scheduleProviderClient(ctx)
	if err != nil {
		return err
	}
	if err := providerClient.DeleteSchedule(ctx, scheduleID); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "✓ 定时任务已删除: %s\n", scheduleID)
	return nil
}

func (a *App) runScheduleToggle(ctx context.Context, args []string, enable bool) error {
	flagSet := flag.NewFlagSet("schedule enable/disable", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	remaining := flagSet.Args()
	if len(remaining) == 0 {
		return fmt.Errorf("需要指定 schedule_id")
	}
	scheduleID := remaining[0]

	providerClient, _, err := a.scheduleProviderClient(ctx)
	if err != nil {
		return err
	}

	if enable {
		if err := providerClient.EnableSchedule(ctx, scheduleID); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "✓ 定时任务已启用: %s\n", scheduleID)
	} else {
		if err := providerClient.DisableSchedule(ctx, scheduleID); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "✓ 定时任务已停用: %s\n", scheduleID)
	}
	return nil
}

func (a *App) runScheduleRuns(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("schedule runs", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	status := flagSet.String("status", "", "按状态过滤（pending|running|success|failed|timeout|cancelled）")
	limit := flagSet.Int("limit", 20, "最大返回条数")
	jsonOutput := flagSet.Bool("json", false, "以 JSON 格式输出")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	remaining := flagSet.Args()
	if len(remaining) == 0 {
		return fmt.Errorf("查看执行历史需要指定 schedule_id")
	}
	scheduleID := remaining[0]

	providerClient, _, err := a.scheduleProviderClient(ctx)
	if err != nil {
		return err
	}

	page, err := providerClient.ListScheduleRuns(ctx, scheduleID, *status, 0, *limit)
	if err != nil {
		return err
	}

	if *jsonOutput {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(page)
	}

	if len(page.Data) == 0 {
		fmt.Fprintln(a.stdout, "暂无执行记录。")
		return nil
	}

	fmt.Fprintf(a.stdout, "%-36s  %-10s  %-20s  %-10s  %s\n", "记录ID", "状态", "触发时间", "耗时", "错误/摘要")
	fmt.Fprintln(a.stdout, strings.Repeat("─", 110))
	for _, record := range page.Data {
		triggeredAt := record.TriggeredAt
		if t, parseErr := time.Parse(time.RFC3339, triggeredAt); parseErr == nil {
			triggeredAt = t.Local().Format("2006-01-02 15:04:05")
		}
		duration := "-"
		if record.DurationMS > 0 {
			duration = fmt.Sprintf("%.1fs", float64(record.DurationMS)/1000.0)
		}
		errMsg := record.ErrorMessage
		if errMsg == "" {
			if summary, ok := record.ResultPayload["summary"].(string); ok && summary != "" {
				errMsg = summary
			}
		}
		errMsg = truncateRunes(errMsg, 40)
		if errMsg == "" {
			errMsg = "-"
		}
		fmt.Fprintf(a.stdout, "%-36s  %-10s  %-20s  %-10s  %s\n",
			record.ID, record.Status, triggeredAt, duration, errMsg)
	}
	fmt.Fprintf(a.stdout, "\n共 %d 条记录\n", page.Total)
	return nil
}

// truncateRunes 按字符（rune）安全截断字符串，避免在多字节字符中间截断产生无效 UTF-8。
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "…"
}

// isScheduleSlashCommand 判断是否为 /schedule 斜杠命令。
func isScheduleSlashCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return lower == "/schedule" || strings.HasPrefix(lower, "/schedule ")
}

// executeScheduleSlashCommand 执行 TUI 中的 /schedule 斜杠命令。
// 支持 /schedule、/schedule list、/schedule runs <id>、/schedule enable <id>、/schedule disable <id>。
func (a *App) executeScheduleSlashCommand(ctx context.Context, providerClient provider.Provider, command string) (string, *tui.ToolPanel, error) {
	trimmed := strings.TrimSpace(command)
	if !isScheduleSlashCommand(trimmed) {
		return "", nil, fmt.Errorf("定时任务命令统一使用 /schedule")
	}
	if providerClient == nil {
		return "", nil, fmt.Errorf("当前服务端连接不可用，无法执行定时任务命令")
	}

	remainder := strings.TrimSpace(trimmed[len("/schedule"):])
	args := []string{}
	if remainder != "" {
		args = strings.Fields(remainder)
	}

	// 默认或 list：列出定时任务
	if len(args) == 0 || strings.EqualFold(args[0], "list") {
		page, err := providerClient.ListSchedules(ctx, provider.ScheduleListParams{Limit: 50})
		if err != nil {
			return "", nil, fmt.Errorf("查询定时任务列表失败: %w", err)
		}
		if len(page.Data) == 0 {
			return "当前没有定时任务。使用 taiyiflow-cli schedule create 创建。", nil, nil
		}
		var builder strings.Builder
		builder.WriteString(fmt.Sprintf("定时任务列表（共 %d 条）：\n\n", page.Total))
		builder.WriteString(fmt.Sprintf("%-36s  %-20s  %-10s  %-8s  %s\n", "ID", "名称", "类型", "状态", "下次执行"))
		builder.WriteString(strings.Repeat("─", 100) + "\n")
		for _, task := range page.Data {
			status := "启用"
			if task.Disabled {
				status = "禁用"
			}
			nextRun := task.NextRunAt
			if nextRun == "" {
				nextRun = "-"
			}
			builder.WriteString(fmt.Sprintf("%-36s  %-20s  %-10s  %-8s  %s\n",
				task.ID, task.Name, task.ScheduleType, status, nextRun))
		}
		return builder.String(), nil, nil
	}

	// runs <id>
	if strings.EqualFold(args[0], "runs") {
		if len(args) < 2 {
			return "", nil, fmt.Errorf("用法: /schedule runs <schedule_id>")
		}
		scheduleID := args[1]
		page, err := providerClient.ListScheduleRuns(ctx, scheduleID, "", 0, 10)
		if err != nil {
			return "", nil, fmt.Errorf("查询执行记录失败: %w", err)
		}
		if len(page.Data) == 0 {
			return "暂无执行记录。", nil, nil
		}
		var builder strings.Builder
		builder.WriteString(fmt.Sprintf("执行记录（共 %d 条，显示最近 %d 条）：\n\n", page.Total, len(page.Data)))
		for _, record := range page.Data {
			duration := "-"
			if record.DurationMS > 0 {
				duration = fmt.Sprintf("%.1fs", float64(record.DurationMS)/1000.0)
			}
			errMsg := ""
			if record.ErrorMessage != "" {
				errMsg = " | " + record.ErrorMessage
				errMsg = truncateRunes(errMsg, 60)
			}
			builder.WriteString(fmt.Sprintf("  %s  %-10s  %s  耗时:%s%s\n",
				record.TriggeredAt, record.Status, record.RequestMode, duration, errMsg))
		}
		return builder.String(), nil, nil
	}

	// enable/disable <id>
	if strings.EqualFold(args[0], "enable") || strings.EqualFold(args[0], "disable") {
		if len(args) < 2 {
			return "", nil, fmt.Errorf("用法: /schedule %s <schedule_id>", args[0])
		}
		scheduleID := args[1]
		if strings.EqualFold(args[0], "enable") {
			if err := providerClient.EnableSchedule(ctx, scheduleID); err != nil {
				return "", nil, fmt.Errorf("启用定时任务失败: %w", err)
			}
			return fmt.Sprintf("定时任务 %s 已启用。", scheduleID), nil, nil
		}
		if err := providerClient.DisableSchedule(ctx, scheduleID); err != nil {
			return "", nil, fmt.Errorf("禁用定时任务失败: %w", err)
		}
		return fmt.Sprintf("定时任务 %s 已禁用。", scheduleID), nil, nil
	}

	return "", nil, fmt.Errorf("用法: /schedule [list|runs <id>|enable <id>|disable <id>]")
}
