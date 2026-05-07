package cmd

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

func toolPanelText(panel *tui.ToolPanel) string {
	if panel == nil {
		return ""
	}
	parts := []string{}
	if strings.TrimSpace(panel.Title) != "" {
		parts = append(parts, strings.TrimSpace(panel.Title))
	}
	if strings.TrimSpace(panel.Summary) != "" {
		parts = append(parts, strings.TrimSpace(panel.Summary))
	}
	if strings.TrimSpace(panel.Body) != "" {
		parts = append(parts, strings.TrimSpace(panel.Body))
	}
	if strings.TrimSpace(panel.Hint) != "" {
		parts = append(parts, "提示: "+strings.TrimSpace(panel.Hint))
	}
	return strings.Join(parts, "\n\n")
}

func buildLocalToolPayload(toolName string, arguments map[string]any) map[string]any {
	return map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name":      toolName,
			"arguments": arguments,
		},
	}
}

func runLocalReadTool(ctx context.Context, config internalconfig.Config, toolName string, arguments map[string]any) (string, any, error) {
	runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), io.Discard, io.Discard)
	tool, result, err := runtimeClient.ExecuteClientToolPayload(ctx, config, buildLocalToolPayload(toolName, arguments))
	return tool, result, err
}

func runLocalUserTool(ctx context.Context, config internalconfig.Config, toolName string, arguments map[string]any) (string, any, error) {
	runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), io.Discard, io.Discard)
	execConfig := config
	if strings.TrimSpace(execConfig.ApprovalMode) != "readonly" {
		execConfig.ApprovalMode = "auto"
	}
	tool, result, err := runtimeClient.ExecuteClientToolPayload(ctx, execConfig, buildLocalToolPayload(toolName, arguments))
	return tool, result, err
}

func (a *App) executeTasksSlashCommand(ctx context.Context, config internalconfig.Config, command string) (string, *tui.ToolPanel, error) {
	toolName, arguments, err := parseTasksCommand(command)
	if err != nil {
		return "", nil, err
	}
	runTool := runLocalReadTool
	if toolName == "task_stop" || toolName == "task_delete" || toolName == "task_cleanup" {
		runTool = runLocalUserTool
	}
	resolvedTool, result, execErr := runTool(ctx, config, toolName, arguments)
	if execErr != nil {
		return "", nil, execErr
	}
	panel := buildTUIClientToolPanel(resolvedTool, result, nil)
	if panel != nil {
		return toolPanelText(panel), panel, nil
	}
	resultMap, _ := result.(map[string]any)
	if resolvedTool == "task_list" && intMapValue(resultMap, "count") == 0 {
		return "当前没有后台任务。", nil, nil
	}
	return fallbackString(stringMapValue(resultMap, "status"), "命令已完成"), nil, nil
}

func isTasksSlashCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return lower == "/tasks" || strings.HasPrefix(lower, "/tasks ")
}

func isTasksSlashListCommand(command string) bool {
	trimmed := strings.TrimSpace(command)
	return strings.EqualFold(trimmed, "/tasks") || strings.EqualFold(trimmed, "/tasks list")
}

func parseTasksCommand(command string) (string, map[string]any, error) {
	trimmed := strings.TrimSpace(command)
	if !isTasksSlashCommand(trimmed) {
		return "", nil, fmt.Errorf("任务面板命令统一使用 /tasks，避免与命令行 task 子命令混淆")
	}
	if strings.EqualFold(trimmed, "/tasks") || strings.EqualFold(trimmed, "/tasks list") {
		return "task_list", map[string]any{}, nil
	}
	if strings.EqualFold(trimmed, "/tasks cleanup") || strings.EqualFold(trimmed, "/tasks clean") {
		return "task_cleanup", map[string]any{}, nil
	}
	parts := strings.Fields(trimmed)
	if len(parts) < 3 {
		return "", nil, fmt.Errorf("用法: /tasks [list|output|capture|stop|remove] <task_id> 或 /tasks cleanup")
	}
	subcommand := strings.ToLower(strings.TrimSpace(parts[1]))
	taskID := strings.TrimSpace(parts[2])
	if taskID == "" {
		return "", nil, fmt.Errorf("task_id 不能为空")
	}
	switch subcommand {
	case "output":
		return "task_output", map[string]any{"task_id": taskID}, nil
	case "capture":
		arguments := map[string]any{"task_id": taskID}
		if len(parts) >= 4 {
			arguments["stream"] = strings.TrimSpace(parts[3])
		}
		return "terminal_capture", arguments, nil
	case "stop":
		return "task_stop", map[string]any{"task_id": taskID}, nil
	case "remove", "delete":
		return "task_delete", map[string]any{"task_id": taskID}, nil
	default:
		return "", nil, fmt.Errorf("不支持的 /tasks 子命令: %s", subcommand)
	}
}

func buildPermissionsPanel(configPath string, config internalconfig.Config) *tui.ToolPanel {
	bodyLines := []string{
		fmt.Sprintf("配置路径: %s", configPath),
		fmt.Sprintf("命令执行模式(approval_mode): %s", fallbackConfigValue(config.ApprovalMode, internalconfig.Default().ApprovalMode)),
		fmt.Sprintf("外部资源访问策略(external_resource_access_policy): %s", fallbackConfigValue(config.SandboxEscapePolicy, internalconfig.Default().SandboxEscapePolicy)),
		fmt.Sprintf("可访问外部资源目录(sandbox_roots): %s", fallbackConfigValue(strings.Join(config.SandboxRoots, ", "), "<仅当前工作目录>")),
	}
	if len(config.PermissionRules) > 0 {
		bodyLines = append(bodyLines, "", "细粒度规则(permission_rules):")
		for index, item := range config.PermissionRules {
			line := fmt.Sprintf("%d. %s", index+1, strings.ToUpper(item.Action))
			if strings.TrimSpace(item.ToolName) != "" {
				line += " | tool=" + strings.TrimSpace(item.ToolName)
			}
			if strings.TrimSpace(item.CommandPrefix) != "" {
				line += " | prefix=" + strings.TrimSpace(item.CommandPrefix)
			}
			bodyLines = append(bodyLines, line)
		}
	}
	return &tui.ToolPanel{
		Title:   "权限设置(permissions)",
		Summary: fmt.Sprintf("%s · %s", fallbackConfigValue(config.ApprovalMode, internalconfig.Default().ApprovalMode), fallbackConfigValue(config.SandboxEscapePolicy, internalconfig.Default().SandboxEscapePolicy)),
		Body:    strings.Join(bodyLines, "\n"),
		Hint:    "可用命令: /permissions set approval <confirm|auto|readonly>、/permissions set sandbox <confirm|deny|allow>、/permissions set roots <a,b>、/permissions clear roots、/permissions rule add tool <tool> <allow|ask|deny>、/permissions rule add shell <prefix> <allow|ask|deny>、/permissions rule remove <序号>。",
	}
}

func (a *App) executePermissionsCommand(command string) (internalconfig.Config, string, *tui.ToolPanel, error) {
	config, err := a.configManager.Load()
	if err != nil {
		return internalconfig.Config{}, "", nil, err
	}
	trimmed := strings.TrimSpace(command)
	if strings.EqualFold(trimmed, "/permissions") || strings.EqualFold(trimmed, "/permissions show") {
		panel := buildPermissionsPanel(a.configManager.Path(), config)
		return config, toolPanelText(panel), panel, nil
	}
	parts := strings.Fields(trimmed)
	if len(parts) < 3 {
		return internalconfig.Config{}, "", nil, fmt.Errorf("用法: /permissions [show|set|clear] ...")
	}
	action := strings.ToLower(strings.TrimSpace(parts[1]))
	if action == "rule" {
		if len(parts) < 4 {
			return internalconfig.Config{}, "", nil, fmt.Errorf("用法: /permissions rule [add|remove] ...")
		}
		ruleAction := strings.ToLower(strings.TrimSpace(parts[2]))
		switch ruleAction {
		case "add":
			if len(parts) < 6 {
				return internalconfig.Config{}, "", nil, fmt.Errorf("用法: /permissions rule add [tool|shell] <值> <allow|ask|deny>")
			}
			kind := strings.ToLower(strings.TrimSpace(parts[3]))
			permissionAction := internalconfig.NormalizePermissionAction(parts[len(parts)-1])
			value := strings.TrimSpace(strings.Join(parts[4:len(parts)-1], " "))
			rule := internalconfig.PermissionRule{Action: permissionAction}
			switch kind {
			case "tool":
				rule.ToolName = value
			case "shell":
				rule.CommandPrefix = value
			default:
				return internalconfig.Config{}, "", nil, fmt.Errorf("rule add 仅支持 tool 或 shell")
			}
			config.PermissionRules = append(config.PermissionRules, rule)
		case "remove":
			if len(parts) != 4 {
				return internalconfig.Config{}, "", nil, fmt.Errorf("用法: /permissions rule remove <序号>")
			}
			index, parseErr := strconv.Atoi(strings.TrimSpace(parts[3]))
			if parseErr != nil {
				return internalconfig.Config{}, "", nil, fmt.Errorf("rule 序号必须是整数")
			}
			if index < 1 || index > len(config.PermissionRules) {
				return internalconfig.Config{}, "", nil, fmt.Errorf("rule 序号超出范围")
			}
			config.PermissionRules = append(config.PermissionRules[:index-1], config.PermissionRules[index:]...)
		default:
			return internalconfig.Config{}, "", nil, fmt.Errorf("不支持的 rule 操作: %s", ruleAction)
		}
		config.PermissionRules = internalconfig.NormalizePermissionRules(config.PermissionRules)
		if err := a.configManager.Save(config); err != nil {
			return internalconfig.Config{}, "", nil, err
		}
		panel := buildPermissionsPanel(a.configManager.Path(), config)
		return config, "已更新权限规则。\n\n" + toolPanelText(panel), panel, nil
	}
	key := strings.ToLower(strings.TrimSpace(parts[2]))
	switch action {
	case "set":
		if len(parts) < 4 {
			return internalconfig.Config{}, "", nil, fmt.Errorf("用法: /permissions set [approval|sandbox|roots] <值>")
		}
		value := strings.TrimSpace(strings.Join(parts[3:], " "))
		switch key {
		case "approval":
			err = assignConfigValue(&config, "approval_mode", value)
		case "sandbox":
			err = assignConfigValue(&config, "external_resource_access_policy", value)
		case "roots":
			err = assignConfigValue(&config, "sandbox_roots", value)
		default:
			return internalconfig.Config{}, "", nil, fmt.Errorf("不支持的权限项: %s", key)
		}
	case "clear":
		switch key {
		case "roots":
			err = resetConfigValue(&config, "sandbox_roots")
		default:
			return internalconfig.Config{}, "", nil, fmt.Errorf("仅支持 /permissions clear roots")
		}
	default:
		return internalconfig.Config{}, "", nil, fmt.Errorf("不支持的 /permissions 操作: %s", action)
	}
	if err != nil {
		return internalconfig.Config{}, "", nil, err
	}
	if err := a.configManager.Save(config); err != nil {
		return internalconfig.Config{}, "", nil, err
	}
	panel := buildPermissionsPanel(a.configManager.Path(), config)
	return config, "已更新权限设置。\n\n" + toolPanelText(panel), panel, nil
}

func (a *App) buildReviewPrompt(ctx context.Context, workdir string, target string) string {
	trimmed := strings.TrimSpace(target)
	targetLabel := "当前工作区变更"
	if trimmed == "" {
		trimmed = ""
	} else {
		targetLabel = trimmed + " 相关变更"
	}
	prompt := fmt.Sprintf("请按代码审查(review)方式检查%s，优先指出缺陷(BUG)、行为回归、风险点和缺少的测试；如果没有发现问题，也要明确说明剩余风险。只做只读审查，不要修改任何文件，不要调用写文件(write_file)、文件编辑(file_edit)、执行命令(shell_exec)或其他会产生本地写入/执行副作用的工具；如果需要修复方案，只输出问题与建议。", targetLabel)
	if diffSummary := summarizeReviewDiff(ctx, workdir); strings.TrimSpace(diffSummary) != "" {
		prompt += "\n\n当前工作区变更(diff)摘要：\n" + diffSummary
	}
	return prompt
}

func reviewRequestMode() string {
	return provider.RequestTypeAsk
}

func parseReviewSlashCommand(command string) (string, error) {
	trimmed := strings.TrimSpace(command)
	if strings.EqualFold(trimmed, "/review") {
		return "", nil
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "/review ") {
		target := strings.TrimSpace(strings.TrimPrefix(trimmed, "/review "))
		if target == "" {
			return "", fmt.Errorf("用法: /review [路径或范围说明]")
		}
		return target, nil
	}
	return "", fmt.Errorf("用法: /review [路径或范围说明]")
}

func summarizeReviewDiff(ctx context.Context, workdir string) string {
	resolvedWorkdir := strings.TrimSpace(workdir)
	if resolvedWorkdir == "" {
		resolvedWorkdir = "."
	}
	if _, err := runGitReviewCommand(ctx, resolvedWorkdir, "rev-parse", "--show-toplevel"); err != nil {
		return ""
	}
	sections := []string{}
	if output, err := runGitReviewCommand(ctx, resolvedWorkdir, "status", "--short", "--untracked-files=all"); err == nil && strings.TrimSpace(output) != "" {
		sections = append(sections, "变更文件:\n"+strings.TrimSpace(output))
	}
	if output, err := runGitReviewCommand(ctx, resolvedWorkdir, "diff", "--cached", "--stat", "--compact-summary"); err == nil && strings.TrimSpace(output) != "" {
		sections = append(sections, "已暂存变更:\n"+strings.TrimSpace(output))
	}
	if output, err := runGitReviewCommand(ctx, resolvedWorkdir, "diff", "--stat", "--compact-summary"); err == nil && strings.TrimSpace(output) != "" {
		sections = append(sections, "未暂存变更:\n"+strings.TrimSpace(output))
	}
	if len(sections) == 0 {
		return "git 工作区当前没有未提交变更。"
	}
	return strings.Join(sections, "\n\n")
}

func runGitReviewCommand(ctx context.Context, workdir string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", workdir}, args...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).CombinedOutput()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
