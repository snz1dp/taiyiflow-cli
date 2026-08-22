package runtime

import (
	"context"
	"fmt"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// scheduleClientToolDefinitions 返回定时任务管理相关的客户端工具定义。
func scheduleClientToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":                 "schedule_list",
			"description":          "查询当前用户的定时任务列表，支持按名称、客户端类型、启用状态过滤。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string", "description": "按任务名称模糊过滤。"},
					"client_type": map[string]any{"type": "string", "description": "按客户端类型过滤：cli、desktop、web。"},
					"disabled":    map[string]any{"type": "boolean", "description": "按禁用状态过滤：true 仅返回已禁用，false 仅返回已启用。"},
					"offset":      map[string]any{"type": "integer", "minimum": 0, "description": "分页偏移量，默认 0。"},
					"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "每页条数，默认 20。"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "schedule_create",
			"description":          "创建定时任务。支持 cron 表达式、interval 间隔或 at 一次性执行。创建后默认启用。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":              map[string]any{"type": "string", "description": "定时任务名称。"},
					"description":       map[string]any{"type": "string", "description": "任务描述。"},
					"prompt":            map[string]any{"type": "string", "description": "要执行的提示词或命令内容。"},
					"request_mode":      map[string]any{"type": "string", "enum": []string{"command", "agent", "ask", "plan"}, "description": "执行模式：command 为直接执行命令，agent/ask/plan 为智能体执行。"},
					"schedule_type":     map[string]any{"type": "string", "enum": []string{"cron", "interval", "once"}, "description": "调度类型：cron 表达式、interval 间隔、once 一次性。"},
					"cron_expr":         map[string]any{"type": "string", "description": "cron 表达式（schedule_type 为 cron 时必填），如 '0 9 * * *'。"},
					"interval_seconds":  map[string]any{"type": "integer", "minimum": 60, "description": "间隔秒数（schedule_type 为 interval 时必填），最小 60 秒。"},
					"run_at":            map[string]any{"type": "string", "description": "一次性执行时间（schedule_type 为 once 时，与 delay_seconds 二选一），ISO 8601 格式。"},
					"delay_seconds":     map[string]any{"type": "integer", "minimum": 1, "description": "延迟秒数（schedule_type 为 once 时，与 run_at 二选一），表示从当前时间起延迟多少秒执行。"},
					"working_directory": map[string]any{"type": "string", "description": "执行工作目录。"},
					"flow_id":           map[string]any{"type": "string", "description": "指定智能体 ID（agent/ask/plan 模式时使用）。"},
					"client_type":       map[string]any{"type": "string", "enum": []string{"cli", "desktop", "web"}, "description": "目标客户端类型，默认 cli。"},
					"disabled":          map[string]any{"type": "boolean", "description": "是否创建后立即禁用，默认 false（即启用）。"},
				},
				"required":             []string{"name", "prompt", "request_mode", "schedule_type"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "schedule_update",
			"description":          "更新定时任务的配置，支持修改名称、提示词、调度规则、启用状态等。仅传入需要修改的字段。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"schedule_id":       map[string]any{"type": "string", "description": "定时任务 ID。"},
					"name":              map[string]any{"type": "string", "description": "新名称。"},
					"prompt":            map[string]any{"type": "string", "description": "新的提示词或命令内容。"},
					"request_mode":      map[string]any{"type": "string", "enum": []string{"command", "agent", "ask", "plan"}, "description": "新的执行模式。"},
					"schedule_type":     map[string]any{"type": "string", "enum": []string{"cron", "interval", "once"}, "description": "新的调度类型。"},
					"cron_expr":         map[string]any{"type": "string", "description": "新的 cron 表达式。"},
					"interval_seconds":  map[string]any{"type": "integer", "minimum": 60, "description": "新的间隔秒数。"},
					"run_at":            map[string]any{"type": "string", "description": "新的一次性执行时间。"},
					"working_directory": map[string]any{"type": "string", "description": "新的工作目录。"},
					"disabled":          map[string]any{"type": "boolean", "description": "是否禁用。"},
				},
				"required":             []string{"schedule_id"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "schedule_delete",
			"description":          "删除定时任务。删除后不可恢复，关联的执行记录保留。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"schedule_id": map[string]any{"type": "string", "description": "要删除的定时任务 ID。"},
				},
				"required":             []string{"schedule_id"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "schedule_enable",
			"description":          "启用定时任务，使其按调度规则执行。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"schedule_id": map[string]any{"type": "string", "description": "要启用的定时任务 ID。"},
				},
				"required":             []string{"schedule_id"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "schedule_disable",
			"description":          "禁用定时任务，暂停其调度执行。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"schedule_id": map[string]any{"type": "string", "description": "要禁用的定时任务 ID。"},
				},
				"required":             []string{"schedule_id"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "schedule_runs",
			"description":          "查询定时任务的执行记录，支持按状态过滤。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"schedule_id": map[string]any{"type": "string", "description": "定时任务 ID。"},
					"status":      map[string]any{"type": "string", "description": "按执行状态过滤：pending、running、success、failed。"},
					"offset":      map[string]any{"type": "integer", "minimum": 0, "description": "分页偏移量，默认 0。"},
					"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "每页条数，默认 20。"},
				},
				"required":             []string{"schedule_id"},
				"additionalProperties": false,
			},
		},
	}
}

// isScheduleTool 判断工具名是否为定时任务管理工具。
func isScheduleTool(toolName string) bool {
	switch canonicalClientToolName(toolName) {
	case "schedule_list", "schedule_create", "schedule_update", "schedule_delete",
		"schedule_enable", "schedule_disable", "schedule_runs":
		return true
	}
	return false
}

// mutatingScheduleTool 判断定时任务工具是否为变更操作（非只读）。
// REG-1 修复：供非交互模式过滤使用。
func mutatingScheduleTool(toolName string) bool {
	switch canonicalClientToolName(toolName) {
	case "schedule_list", "schedule_runs":
		return false
	default:
		return true
	}
}

// scheduleTool 执行定时任务管理工具。
func (r *ClientRuntime) scheduleTool(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, toolName string, arguments map[string]any) (map[string]any, error) {
	if providerClient == nil {
		return nil, newToolError("PROVIDER_UNAVAILABLE", "定时任务管理需要已认证的后端连接")
	}
	switch canonicalClientToolName(toolName) {
	case "schedule_list":
		return r.scheduleList(ctx, providerClient, arguments)
	case "schedule_create":
		return r.scheduleCreate(ctx, providerClient, config, arguments)
	case "schedule_update":
		return r.scheduleUpdate(ctx, providerClient, arguments)
	case "schedule_delete":
		return r.scheduleDelete(ctx, providerClient, arguments)
	case "schedule_enable":
		return r.scheduleEnable(ctx, providerClient, arguments)
	case "schedule_disable":
		return r.scheduleDisable(ctx, providerClient, arguments)
	case "schedule_runs":
		return r.scheduleRuns(ctx, providerClient, arguments)
	default:
		return nil, newToolError("PARAM_ERROR", "不支持的定时任务工具: %s", toolName)
	}
}

func (r *ClientRuntime) scheduleList(ctx context.Context, providerClient provider.Provider, arguments map[string]any) (map[string]any, error) {
	params := provider.ScheduleListParams{
		Name:       argumentString(arguments, "name"),
		ClientType: argumentString(arguments, "client_type"),
		Offset:     intValue(arguments["offset"], 0),
		Limit:      intValue(arguments["limit"], 20),
	}
	if _, ok := arguments["disabled"]; ok {
		bv := argumentBool(arguments, "disabled")
		params.Disabled = &bv
	}
	page, err := providerClient.ListSchedules(ctx, params)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "查询定时任务列表失败: %v", err)
	}
	items := make([]map[string]any, 0, len(page.Data))
	for _, item := range page.Data {
		items = append(items, mapScheduleTaskToMap(item))
	}
	return map[string]any{
		"total":  page.Total,
		"offset": page.Offset,
		"items":  items,
	}, nil
}

func (r *ClientRuntime) scheduleCreate(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	name := argumentString(arguments, "name")
	prompt := argumentString(arguments, "prompt")
	requestMode := argumentString(arguments, "request_mode")
	scheduleType := argumentString(arguments, "schedule_type")
	if name == "" || prompt == "" || requestMode == "" || scheduleType == "" {
		return nil, newToolError("PARAM_ERROR", "schedule_create 需要 name、prompt、request_mode、schedule_type")
	}

	// BUG-3 修复：校验 schedule_type 与关联调度字段的一致性，
	// 避免创建出缺少调度规则的无效定时任务。
	// 同时将 schedule_type 归一化为小写，避免模型传入 "CRON" 等非标准值。
	cronExpr := argumentString(arguments, "cron_expr")
	intervalSeconds := intValue(arguments["interval_seconds"], 0)
	runAt := argumentString(arguments, "run_at")
	delaySeconds := intValue(arguments["delay_seconds"], 0)
	scheduleType = strings.ToLower(strings.TrimSpace(scheduleType))
	switch scheduleType {
	case "cron":
		if strings.TrimSpace(cronExpr) == "" {
			return nil, newToolError("PARAM_ERROR", "schedule_type 为 cron 时必须提供 cron_expr")
		}
	case "interval":
		if intervalSeconds <= 0 {
			return nil, newToolError("PARAM_ERROR", "schedule_type 为 interval 时必须提供大于 0 的 interval_seconds")
		}
	case "once":
		// RISK-3 修复：once 类型支持 run_at 或 delay_seconds 二选一，与 CLI --delay 能力对齐。
		if strings.TrimSpace(runAt) == "" && delaySeconds <= 0 {
			return nil, newToolError("PARAM_ERROR", "schedule_type 为 once 时必须提供 run_at 或 delay_seconds")
		}
	default:
		return nil, newToolError("PARAM_ERROR", "不支持的 schedule_type: %s（可用: cron、interval、once）", scheduleType)
	}

	request := provider.ScheduleTaskCreateRequest{
		Name:             name,
		Description:      argumentString(arguments, "description"),
		Prompt:           prompt,
		RequestMode:      requestMode,
		ScheduleType:     scheduleType,
		CronExpr:         cronExpr,
		IntervalSeconds:  intervalSeconds,
		RunAt:            runAt,
		DelaySeconds:     delaySeconds,
		WorkingDirectory: argumentString(arguments, "working_directory"),
		FlowID:           argumentString(arguments, "flow_id"),
		ClientType:       firstNonEmptySubtask(argumentString(arguments, "client_type"), provider.ClientTypeCLI),
		ClientID:         strings.TrimSpace(config.ClientID),
		DeviceID:         strings.TrimSpace(config.DeviceID),
		Disabled:         argumentBool(arguments, "disabled"),
	}

	task, err := providerClient.CreateSchedule(ctx, request)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "创建定时任务失败: %v", err)
	}
	return mapScheduleTaskToMap(*task), nil
}

func (r *ClientRuntime) scheduleUpdate(ctx context.Context, providerClient provider.Provider, arguments map[string]any) (map[string]any, error) {
	scheduleID := argumentString(arguments, "schedule_id")
	if scheduleID == "" {
		return nil, newToolError("PARAM_ERROR", "schedule_update 需要 schedule_id")
	}

	request := provider.ScheduleTaskUpdateRequest{}
	if v := argumentString(arguments, "name"); v != "" {
		request.Name = &v
	}
	if v := argumentString(arguments, "prompt"); v != "" {
		request.Prompt = &v
	}
	if v := argumentString(arguments, "request_mode"); v != "" {
		request.RequestMode = &v
	}
	if v := argumentString(arguments, "schedule_type"); v != "" {
		request.ScheduleType = &v
	}
	if v := argumentString(arguments, "cron_expr"); v != "" {
		request.CronExpr = &v
	}
	if v, ok := arguments["interval_seconds"]; ok {
		iv := intValue(v, 0)
		request.IntervalSeconds = &iv
	}
	if v := argumentString(arguments, "run_at"); v != "" {
		request.RunAt = &v
	}
	if v := argumentString(arguments, "working_directory"); v != "" {
		request.WorkingDirectory = &v
	}
	if _, ok := arguments["disabled"]; ok {
		bv := argumentBool(arguments, "disabled")
		request.Disabled = &bv
	}

	task, err := providerClient.UpdateSchedule(ctx, scheduleID, request)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "更新定时任务失败: %v", err)
	}
	return mapScheduleTaskToMap(*task), nil
}

func (r *ClientRuntime) scheduleDelete(ctx context.Context, providerClient provider.Provider, arguments map[string]any) (map[string]any, error) {
	scheduleID := argumentString(arguments, "schedule_id")
	if scheduleID == "" {
		return nil, newToolError("PARAM_ERROR", "schedule_delete 需要 schedule_id")
	}
	if err := providerClient.DeleteSchedule(ctx, scheduleID); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "删除定时任务失败: %v", err)
	}
	return map[string]any{"deleted": true, "schedule_id": scheduleID}, nil
}

func (r *ClientRuntime) scheduleEnable(ctx context.Context, providerClient provider.Provider, arguments map[string]any) (map[string]any, error) {
	scheduleID := argumentString(arguments, "schedule_id")
	if scheduleID == "" {
		return nil, newToolError("PARAM_ERROR", "schedule_enable 需要 schedule_id")
	}
	if err := providerClient.EnableSchedule(ctx, scheduleID); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "启用定时任务失败: %v", err)
	}
	return map[string]any{"enabled": true, "schedule_id": scheduleID}, nil
}

func (r *ClientRuntime) scheduleDisable(ctx context.Context, providerClient provider.Provider, arguments map[string]any) (map[string]any, error) {
	scheduleID := argumentString(arguments, "schedule_id")
	if scheduleID == "" {
		return nil, newToolError("PARAM_ERROR", "schedule_disable 需要 schedule_id")
	}
	if err := providerClient.DisableSchedule(ctx, scheduleID); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "禁用定时任务失败: %v", err)
	}
	return map[string]any{"disabled": true, "schedule_id": scheduleID}, nil
}

func (r *ClientRuntime) scheduleRuns(ctx context.Context, providerClient provider.Provider, arguments map[string]any) (map[string]any, error) {
	scheduleID := argumentString(arguments, "schedule_id")
	if scheduleID == "" {
		return nil, newToolError("PARAM_ERROR", "schedule_runs 需要 schedule_id")
	}
	status := argumentString(arguments, "status")
	offset := intValue(arguments["offset"], 0)
	limit := intValue(arguments["limit"], 20)

	page, err := providerClient.ListScheduleRuns(ctx, scheduleID, status, offset, limit)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "查询定时任务执行记录失败: %v", err)
	}
	items := make([]map[string]any, 0, len(page.Data))
	for _, item := range page.Data {
		items = append(items, mapScheduleRunRecordToMap(item))
	}
	return map[string]any{
		"total":  page.Total,
		"offset": page.Offset,
		"items":  items,
	}, nil
}

// mapScheduleTaskToMap 将 ScheduleTask 转为工具返回的 map。
func mapScheduleTaskToMap(task provider.ScheduleTask) map[string]any {
	result := map[string]any{
		"id":            task.ID,
		"name":          task.Name,
		"prompt":        task.Prompt,
		"request_mode":  task.RequestMode,
		"schedule_type": task.ScheduleType,
		"disabled":      task.Disabled,
		"client_type":   task.ClientType,
	}
	if task.Description != "" {
		result["description"] = task.Description
	}
	if task.CronExpr != "" {
		result["cron_expr"] = task.CronExpr
	}
	if task.IntervalSeconds > 0 {
		result["interval_seconds"] = task.IntervalSeconds
	}
	if task.RunAt != "" {
		result["run_at"] = task.RunAt
	}
	if task.WorkingDirectory != "" {
		result["working_directory"] = task.WorkingDirectory
	}
	if task.FlowID != "" {
		result["flow_id"] = task.FlowID
	}
	if task.NextRunAt != "" {
		result["next_run_at"] = task.NextRunAt
	}
	if task.LastRunAt != "" {
		result["last_run_at"] = task.LastRunAt
	}
	if task.LastErrorMessage != "" {
		result["last_error_message"] = task.LastErrorMessage
	}
	if task.CreatedAt != "" {
		result["created_at"] = task.CreatedAt
	}
	return result
}

// mapScheduleRunRecordToMap 将 ScheduleRunRecord 转为工具返回的 map。
func mapScheduleRunRecordToMap(record provider.ScheduleRunRecord) map[string]any {
	result := map[string]any{
		"id":     record.ID,
		"status": record.Status,
	}
	if record.RequestMode != "" {
		result["request_mode"] = record.RequestMode
	}
	if record.TriggeredAt != "" {
		result["triggered_at"] = record.TriggeredAt
	}
	if record.StartedAt != "" {
		result["started_at"] = record.StartedAt
	}
	if record.FinishedAt != "" {
		result["finished_at"] = record.FinishedAt
	}
	if record.ErrorMessage != "" {
		result["error_message"] = record.ErrorMessage
	}
	if record.DurationMS > 0 {
		result["duration_ms"] = record.DurationMS
	}
	if record.ClientType != "" {
		result["client_type"] = record.ClientType
	}
	return result
}

// schedulePreview 生成定时任务工具的确认预览文本。
func schedulePreview(toolName string, arguments map[string]any) string {
	switch toolName {
	case "schedule_create":
		name := argumentString(arguments, "name")
		return fmt.Sprintf("创建定时任务: %s", name)
	case "schedule_update":
		id := argumentString(arguments, "schedule_id")
		return fmt.Sprintf("更新定时任务: %s", id)
	case "schedule_delete":
		id := argumentString(arguments, "schedule_id")
		return fmt.Sprintf("删除定时任务: %s", id)
	case "schedule_enable":
		id := argumentString(arguments, "schedule_id")
		return fmt.Sprintf("启用定时任务: %s", id)
	case "schedule_disable":
		id := argumentString(arguments, "schedule_id")
		return fmt.Sprintf("禁用定时任务: %s", id)
	default:
		return toolName
	}
}
