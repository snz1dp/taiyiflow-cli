package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

const defaultMemoryStatusLimit = 5

type memoryCommandAction string

const (
	memoryCommandList    memoryCommandAction = "list"
	memoryCommandTrigger memoryCommandAction = "trigger"
)

type memoryCommandOptions struct {
	FlowID         string
	Status         string
	Limit          int
	MinHours       int
	MinSessions    int
	ExecuteEnabled bool
}

func (a *App) runMemory(ctx context.Context, args []string) error {
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	providerClient := buildProviderClient(config)
	config, _, err = a.ensureProviderAuthenticated(ctx, providerClient, config)
	if err != nil {
		return err
	}
	if err := a.ensureBackendAvailable(ctx, providerClient, config.BackendBaseURL); err != nil {
		return err
	}
	config, err = a.ensureDefaultFlow(ctx, providerClient, config)
	if err != nil {
		return err
	}

	output, _, err := a.executeMemoryArgsCommand(
		ctx,
		a.providerWithCallbackAuthRefresh(providerClient, &config),
		args,
		strings.TrimSpace(config.DefaultFlow),
		"taiyiflow-cli memory",
	)
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) != "" {
		fmt.Fprintln(a.stdout, output)
	}
	return nil
}

func isMemorySlashCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return lower == "/memory" || strings.HasPrefix(lower, "/memory ")
}

func (a *App) executeMemorySlashCommand(
	ctx context.Context,
	providerClient provider.Provider,
	command string,
	defaultFlowID string,
) (string, *tui.ToolPanel, error) {
	trimmed := strings.TrimSpace(command)
	if !isMemorySlashCommand(trimmed) {
		return "", nil, fmt.Errorf("跨会话记忆命令统一使用 /memory")
	}
	remainder := strings.TrimSpace(trimmed[len("/memory"):])
	args := []string{}
	if remainder != "" {
		args = strings.Fields(remainder)
	}
	return a.executeMemoryArgsCommand(ctx, providerClient, args, defaultFlowID, "/memory")
}

func (a *App) executeMemoryArgsCommand(
	ctx context.Context,
	providerClient provider.Provider,
	args []string,
	defaultFlowID string,
	commandLabel string,
) (string, *tui.ToolPanel, error) {
	if providerClient == nil {
		return "", nil, fmt.Errorf("当前服务端连接不可用，无法执行跨会话记忆命令")
	}
	action, options, err := parseMemoryCommandArgs(args, defaultFlowID, commandLabel)
	if err != nil {
		return "", nil, err
	}
	if trimmedFlowID := strings.TrimSpace(options.FlowID); trimmedFlowID != "" {
		options.FlowID = a.resolveFlowIdentifier(ctx, providerClient, trimmedFlowID)
	}

	switch action {
	case memoryCommandTrigger:
		result, err := providerClient.TriggerMemoryConsolidation(ctx, provider.MemoryConsolidationTriggerRequest{
			FlowID:         options.FlowID,
			MinHours:       options.MinHours,
			MinSessions:    options.MinSessions,
			ExecuteEnabled: options.ExecuteEnabled,
		})
		if err != nil {
			return "", nil, fmt.Errorf("触发跨会话记忆整合失败: %w", err)
		}
		panel := buildMemoryTriggerPanel(options, result)
		return toolPanelText(panel), panel, nil
	default:
		page, err := providerClient.ListMemoryConsolidations(ctx, provider.MemoryConsolidationQuery{
			FlowID: options.FlowID,
			Status: options.Status,
			Offset: 0,
			Limit:  options.Limit,
		})
		if err != nil {
			return "", nil, fmt.Errorf("获取跨会话记忆整合账本失败: %w", err)
		}
		panel := buildMemoryStatusPanel(options, page)
		return toolPanelText(panel), panel, nil
	}
}

func parseMemoryCommandArgs(args []string, defaultFlowID string, commandLabel string) (memoryCommandAction, memoryCommandOptions, error) {
	options := memoryCommandOptions{
		FlowID:         strings.TrimSpace(defaultFlowID),
		Limit:          defaultMemoryStatusLimit,
		MinHours:       1,
		MinSessions:    1,
		ExecuteEnabled: true,
	}
	action := memoryCommandList
	remainder := append([]string(nil), args...)
	if len(remainder) > 0 {
		token := strings.ToLower(strings.TrimSpace(remainder[0]))
		switch token {
		case "status", "list", "show":
			action = memoryCommandList
			remainder = remainder[1:]
		case "trigger", "run":
			action = memoryCommandTrigger
			remainder = remainder[1:]
		default:
			if !strings.Contains(token, "=") {
				return "", memoryCommandOptions{}, fmt.Errorf(
					"不支持的跨会话记忆子命令: %s\n%s\n%s",
					token,
					memoryStatusUsage(commandLabel),
					memoryTriggerUsage(commandLabel),
				)
			}
		}
	}

	for _, rawArg := range remainder {
		key, value, err := parseMemoryKeyValue(rawArg)
		if err != nil {
			return "", memoryCommandOptions{}, fmt.Errorf("%w\n%s", err, memoryUsageForAction(commandLabel, action))
		}
		switch key {
		case "flow", "agent":
			options.FlowID = value
		case "status":
			options.Status = value
		case "limit":
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 100 {
				return "", memoryCommandOptions{}, fmt.Errorf("limit 仅支持 1-100 的整数")
			}
			options.Limit = parsed
		case "hours", "min_hours":
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 {
				return "", memoryCommandOptions{}, fmt.Errorf("hours 仅支持大于等于 1 的整数")
			}
			options.MinHours = parsed
		case "sessions", "min_sessions":
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 {
				return "", memoryCommandOptions{}, fmt.Errorf("sessions 仅支持大于等于 1 的整数")
			}
			options.MinSessions = parsed
		case "execute":
			parsed, err := parseMemoryBoolValue(value)
			if err != nil {
				return "", memoryCommandOptions{}, err
			}
			options.ExecuteEnabled = parsed
		default:
			return "", memoryCommandOptions{}, fmt.Errorf("不支持的跨会话记忆参数: %s", key)
		}
	}

	if action == memoryCommandTrigger && strings.TrimSpace(options.FlowID) == "" {
		return "", memoryCommandOptions{}, fmt.Errorf("当前未配置默认智能体，请通过 flow=<智能体ID> 指定或先执行 taiyiflow-cli agents use <名称>")
	}
	return action, options, nil
}

func parseMemoryKeyValue(argument string) (string, string, error) {
	parts := strings.SplitN(strings.TrimSpace(argument), "=", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("跨会话记忆参数统一使用 key=value 形式")
	}
	key := strings.ToLower(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])
	if key == "" || value == "" {
		return "", "", fmt.Errorf("跨会话记忆参数不能为空")
	}
	return key, value, nil
}

func parseMemoryBoolValue(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("execute 仅支持 on/off")
	}
}

func memoryStatusUsage(commandLabel string) string {
	return fmt.Sprintf("用法: %s [status|list] [flow=<智能体>] [status=<状态>] [limit=<数量>]", commandLabel)
}

func memoryTriggerUsage(commandLabel string) string {
	return fmt.Sprintf("用法: %s trigger [flow=<智能体>] [hours=<小时>] [sessions=<会话数>] [execute=on|off]", commandLabel)
}

func memoryUsageForAction(commandLabel string, action memoryCommandAction) string {
	if action == memoryCommandTrigger {
		return memoryTriggerUsage(commandLabel)
	}
	return memoryStatusUsage(commandLabel)
}

func buildMemoryStatusPanel(options memoryCommandOptions, page *provider.MemoryConsolidationPage) *tui.ToolPanel {
	items := []provider.MemoryConsolidationTask{}
	total := 0
	if page != nil {
		items = append(items, page.Items...)
		total = page.Total
	}
	flowLabel := sanitizeFlowDisplay(firstNonEmpty(options.FlowID, "全部智能体"))
	summary := fmt.Sprintf("%s · 最近 %d/%d 条整合任务", flowLabel, len(items), total)
	if total == 0 {
		summary = fmt.Sprintf("%s · 当前没有整合任务", flowLabel)
	}
	bodyLines := []string{}
	if len(items) == 0 {
		bodyLines = append(bodyLines, "当前没有符合条件的跨会话记忆整合记录。")
	} else {
		for index, item := range items {
			bodyLines = append(bodyLines, renderMemoryTaskBlock(index+1, item)...)
			if index != len(items)-1 {
				bodyLines = append(bodyLines, "")
			}
		}
	}
	return &tui.ToolPanel{
		Title:   "跨会话记忆(memory)",
		Summary: summary,
		Body:    strings.Join(bodyLines, "\n"),
		Hint:    "/memory status 查看最近账本；/memory trigger hours=1 sessions=1 execute=on 可手动触发。",
	}
}

func renderMemoryTaskBlock(index int, item provider.MemoryConsolidationTask) []string {
	lines := []string{
		fmt.Sprintf("%d. %s | %s -> %s | %d 会话 / %d 条来源", index, formatMemoryStatus(item.Status), formatMemoryTimestamp(item.WindowStart), formatMemoryTimestamp(item.WindowEnd), item.LastSessionCount, item.LastSourceCount),
		fmt.Sprintf("   flow: %s", sanitizeFlowDisplay(item.FlowID)),
		fmt.Sprintf("   task_id: %s", sanitizeFlowDisplay(item.ID)),
	}
	if strings.TrimSpace(item.LastTaskID) != "" {
		lines = append(lines, fmt.Sprintf("   backend_task_id: %s", strings.TrimSpace(item.LastTaskID)))
	}
	if strings.TrimSpace(item.ResultSegmentKey) != "" {
		lines = append(lines, fmt.Sprintf("   result_segment: %s", strings.TrimSpace(item.ResultSegmentKey)))
	}
	if !item.LastRunAt.IsZero() {
		lines = append(lines, fmt.Sprintf("   last_run_at: %s", formatMemoryTimestamp(item.LastRunAt)))
	}
	if strings.TrimSpace(item.LastError) != "" {
		lines = append(lines, fmt.Sprintf("   error: %s", strings.ReplaceAll(strings.TrimSpace(item.LastError), "\n", " ")))
	}
	return lines
}

func buildMemoryTriggerPanel(options memoryCommandOptions, result *provider.MemoryConsolidationTriggerResult) *tui.ToolPanel {
	resolved := &provider.MemoryConsolidationTriggerResult{}
	if result != nil {
		resolved = result
	}
	flowLabel := sanitizeFlowDisplay(options.FlowID)
	bodyLines := []string{
		fmt.Sprintf("智能体: %s", flowLabel),
		fmt.Sprintf("状态: %s", formatMemoryStatus(resolved.Status)),
		fmt.Sprintf("执行方式: %s", formatMemoryExecuteMode(options.ExecuteEnabled)),
		fmt.Sprintf("最小时长阈值: %d 小时", options.MinHours),
		fmt.Sprintf("最少会话阈值: %d", options.MinSessions),
	}
	if strings.TrimSpace(resolved.TaskID) != "" {
		bodyLines = append(bodyLines, fmt.Sprintf("task_id: %s", strings.TrimSpace(resolved.TaskID)))
	}
	if strings.TrimSpace(resolved.BackendTaskID) != "" {
		bodyLines = append(bodyLines, fmt.Sprintf("backend_task_id: %s", strings.TrimSpace(resolved.BackendTaskID)))
	}
	if strings.TrimSpace(resolved.Reason) != "" {
		bodyLines = append(bodyLines, fmt.Sprintf("说明: %s", formatMemoryReason(resolved.Reason)))
	}
	return &tui.ToolPanel{
		Title:   "跨会话记忆(memory)",
		Summary: fmt.Sprintf("%s · %s", flowLabel, formatMemoryStatus(resolved.Status)),
		Body:    strings.Join(bodyLines, "\n"),
		Hint:    "/memory status 查看最近账本；如需降低阈值，可使用 /memory trigger hours=1 sessions=1。",
	}
}

func formatMemoryStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending":
		return "待执行"
	case "running":
		return "执行中"
	case "succeeded":
		return "已完成"
	case "failed":
		return "失败"
	case "skipped":
		return "已跳过"
	default:
		trimmed := strings.TrimSpace(status)
		if trimmed == "" {
			return "未知状态"
		}
		return trimmed
	}
}

func formatMemoryReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "already_pending":
		return "已有待执行或执行中的整合任务。"
	case "execution_disabled":
		return "本次仅创建账本，未立即异步执行。"
	case "launch_failed":
		return "后台任务创建失败，请稍后重试。"
	case "threshold_not_met_or_no_source_records":
		return "未达到阈值或暂无可整合来源记录。"
	default:
		return strings.TrimSpace(reason)
	}
}

func formatMemoryExecuteMode(executeEnabled bool) string {
	if executeEnabled {
		return "创建后立即异步执行"
	}
	return "仅建账，不立即执行"
}

func formatMemoryTimestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04")
}

func buildSessionMemoryTierLine(preview *provider.SessionContextPreview) string {
	if preview == nil || len(preview.MemoryTiers) == 0 {
		return ""
	}
	labels := make([]string, 0, len(preview.MemoryTiers))
	for _, item := range preview.MemoryTiers {
		label := formatMemoryTierLabel(item)
		if label == "" {
			continue
		}
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		return ""
	}
	return "记忆层: " + strings.Join(labels, " / ")
}

func loadSessionMemoryPreview(ctx context.Context, providerClient provider.Provider, flowID string, sessionID string) *provider.SessionContextPreview {
	if providerClient == nil || strings.TrimSpace(flowID) == "" || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	preview, err := providerClient.GetSessionContextWindow(ctx, flowID, sessionID)
	if err != nil {
		return nil
	}
	return preview
}

func formatMemoryTierLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "session":
		return "当前会话"
	case "flow_user_day":
		return "当日归纳"
	case "dream":
		return "长期记忆"
	default:
		return strings.TrimSpace(value)
	}
}