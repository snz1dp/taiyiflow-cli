package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

func (a *App) runTUI(ctx context.Context, args []string) error {
	_ = args

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	providerClient := providerclient.NewClient(config.BackendBaseURL, config.APIKey)
	backend := a.inspectBackendStatus(ctx, providerClient, config.BackendBaseURL)
	if backend.Available {
		config, err = a.ensureDefaultFlow(ctx, providerClient, config)
		if err != nil {
			return err
		}
	}
	workdir := resolveLocalWorkdir(config.Workdir)
	snapshot, err := a.buildTUISnapshot(ctx, providerClient, config, strings.TrimSpace(config.DefaultFlow), backend)
	if err != nil {
		return err
	}

	flowItems := []tui.FlowItem{}
	if backend.Available {
		flows, flowErr := providerClient.ListFlows(ctx, "", "chat", 0, 100)
		if flowErr == nil {
			for _, item := range flows.Items {
				flowItems = append(flowItems, tui.FlowItem{
					ID:          item.ID,
					Name:        item.Name,
					Description: item.Description,
					Type:        item.Type,
				})
			}
		}
	}

	runner := func(runCtx context.Context, request tui.Request, emit func(tui.Event), confirm func(tui.ConfirmRequest) bool) error {
		currentFlow := strings.TrimSpace(request.FlowID)
		if currentFlow == "" {
			return fmt.Errorf("未配置默认智能体，请先执行 taiyiflow agents use <名称>")
		}

		sessionState, err := a.prepareSession(currentFlow, false, workdir)
		if err != nil {
			return err
		}

		logWriter := &tuiLogWriter{emit: emit}
		defer logWriter.Flush()

		runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), logWriter, logWriter)
		quietRuntimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), io.Discard, io.Discard)
		stream, streamSession, err := providerClient.Stream(runCtx, provider.StreamRequest{
			FlowID:                 currentFlow,
			Input:                  request.Input,
			InputType:              "any",
			OutputType:             "any",
			SessionID:              sessionState.SessionID,
			RunID:                  request.RunID,
			RequestType:            "agent",
			CommandExecutionTarget: "client",
			ClientMCPServers:       quietRuntimeClient.ClientMCPServers(),
		})
		if err != nil {
			return fmt.Errorf("%s", explainBackendFailure(err))
		}

		if streamSession != nil && strings.TrimSpace(streamSession.SessionID) != "" {
			sessionState.SessionID = streamSession.SessionID
			sessionState.FlowID = currentFlow
			sessionState.Workdir = workdir
			if err := a.sessionStore.Save(sessionState); err != nil {
				return err
			}
			emit(tui.Event{Kind: tui.EventSessionUpdate, SessionID: streamSession.SessionID})
		}

		for event := range stream {
			switch event.Type {
			case provider.EventMessage:
				if chunk, ok := event.Data["chunk"].(string); ok && chunk != "" {
					emit(tui.Event{Kind: tui.EventAssistantChunk, Text: chunk})
					continue
				}
				if message, ok := event.Data["message"].(string); ok && strings.TrimSpace(message) != "" {
					emit(tui.Event{Kind: tui.EventProgressNote, Text: message})
				}
			case provider.EventToolCall:
				emit(tui.Event{Kind: tui.EventToolCall})
			case provider.EventBuiltIn:
				localConfig, configErr := a.configManager.Load()
				if configErr != nil {
					return configErr
				}
				emit(tui.Event{Kind: tui.EventBuiltInCall})
				emit(tui.Event{Kind: tui.EventSystemNote, Text: "执行回调: run_command_via_client"})
				if requiresTUICommandConfirmation(localConfig, event.Data) {
					approved := confirm(buildTUIConfirmRequest(event.Data))
					if !approved {
						emit(tui.Event{Kind: tui.EventErrorNote, Text: "已拒绝本次客户端命令执行。"})
						if err := submitTUIClientCallFailure(runCtx, providerClient, event.Data, "用户拒绝执行客户端命令"); err != nil {
							return err
						}
						continue
					}
					execConfig := localConfig
					execConfig.ApprovalMode = "auto"
					if err := quietRuntimeClient.HandleBuiltInEvent(runCtx, providerClient, execConfig, event.Data); err != nil {
						return err
					}
					continue
				}
				if err := quietRuntimeClient.HandleBuiltInEvent(runCtx, providerClient, localConfig, event.Data); err != nil {
					return err
				}
			case provider.EventClientTool:
				localConfig, configErr := a.configManager.Load()
				if configErr != nil {
					return configErr
				}
				emit(tui.Event{Kind: tui.EventClientToolCall})
				if err := runtimeClient.HandleClientToolEvent(runCtx, providerClient, localConfig, event.Data); err != nil {
					return err
				}
			case provider.EventError:
				return fmt.Errorf("服务端请求失败，请稍后重试")
			case provider.EventClose:
				return nil
			}
		}

		if request.RunID != "" && strings.TrimSpace(sessionState.SessionID) != "" {
			stats, statsErr := providerClient.GetRunStats(runCtx, currentFlow, sessionState.SessionID, request.RunID)
			if statsErr == nil && stats != nil {
				emit(tui.Event{Kind: tui.EventStatsUpdate, Stats: buildTUIRequestMetrics(request.RunID, stats)})
			}
		}

		return nil
	}

	refreshSnapshot := func(refreshCtx context.Context, current tui.Snapshot) (tui.Snapshot, []tui.FlowItem, error) {
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return tui.Snapshot{}, nil, err
		}
		if strings.TrimSpace(updatedConfig.DefaultFlow) == "" {
			updatedConfig.DefaultFlow = strings.TrimSpace(current.DefaultFlow)
		}

		refreshedClient := providerclient.NewClient(updatedConfig.BackendBaseURL, updatedConfig.APIKey)
		backend := a.inspectBackendStatus(refreshCtx, refreshedClient, updatedConfig.BackendBaseURL)
		if backend.Available {
			updatedConfig, err = a.ensureDefaultFlow(refreshCtx, refreshedClient, updatedConfig)
			if err != nil {
				return tui.Snapshot{}, nil, err
			}
		}

		flowID := strings.TrimSpace(firstNonEmpty(updatedConfig.DefaultFlow, current.DefaultFlow))
		snapshot, err := a.buildTUISnapshot(refreshCtx, refreshedClient, updatedConfig, flowID, backend)
		if err != nil {
			return tui.Snapshot{}, nil, err
		}

		flowItems := []tui.FlowItem{}
		if backend.Available {
			flows, flowErr := refreshedClient.ListFlows(refreshCtx, "", "chat", 0, 100)
			if flowErr == nil {
				for _, item := range flows.Items {
					flowItems = append(flowItems, tui.FlowItem{
						ID:          item.ID,
						Name:        item.Name,
						Description: item.Description,
						Type:        item.Type,
					})
				}
			}
		}

		return snapshot, flowItems, nil
	}

	flowChange := func(changeCtx context.Context, flowID string) (tui.Snapshot, error) {
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return tui.Snapshot{}, err
		}
		updatedConfig.DefaultFlow = strings.TrimSpace(flowID)
		if err := a.configManager.Save(updatedConfig); err != nil {
			return tui.Snapshot{}, err
		}
		return a.buildTUISnapshot(changeCtx, providerClient, updatedConfig, updatedConfig.DefaultFlow, backendStatus{Available: true})
	}

	sessionChange := func(changeCtx context.Context, current tui.Snapshot, sessionID string) (tui.Snapshot, error) {
		flowID := strings.TrimSpace(current.DefaultFlow)
		if flowID == "" {
			return tui.Snapshot{}, fmt.Errorf("当前未选择智能体，无法切换会话")
		}
		if err := a.sessionStore.Save(agent.SessionState{FlowID: flowID, SessionID: strings.TrimSpace(sessionID), Workdir: current.Workdir}); err != nil {
			return tui.Snapshot{}, err
		}
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return tui.Snapshot{}, err
		}
		return a.buildTUISnapshot(changeCtx, providerClient, updatedConfig, flowID, backendStatus{Available: true})
	}

	approvalChange := func(changeCtx context.Context, mode string) (tui.Snapshot, error) {
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return tui.Snapshot{}, err
		}
		updatedConfig.ApprovalMode = strings.TrimSpace(mode)
		if err := a.configManager.Save(updatedConfig); err != nil {
			return tui.Snapshot{}, err
		}
		return a.buildTUISnapshot(changeCtx, providerClient, updatedConfig, updatedConfig.DefaultFlow, backendStatus{Available: true})
	}

	localCommand := func(commandCtx context.Context, command string, snapshot tui.Snapshot) (tui.LocalCommandResult, error) {
		trimmed := strings.TrimSpace(command)
		switch {
		case trimmed == "/help":
			helpLines := []string{
				"可用命令:",
				"/agents 或 /agents <名称> 选择智能体",
				"!<shell 命令> 在当前目录执行本地命令",
				"/mode 打开命令执行模式选择",
				"/session 查看当前会话",
				"/sessions [页码] 查看当前智能体的服务端会话列表",
				"/new 重置当前智能体的本地会话",
				"/clear 清空当前界面消息",
				"/exit 退出",
			}
			if !snapshot.BackendAvailable {
				reason := strings.TrimSpace(snapshot.BackendStatusReason)
				if reason == "" {
					reason = "服务端不可用"
				}
				helpLines = []string{
					"当前处于只读模式。",
					"原因: " + reason,
					"可用命令:",
					"/help 查看当前提示",
					"/clear 清空当前界面消息",
					"/exit 退出",
				}
			}
			return tui.LocalCommandResult{
				Handled: true,
				Events:  []tui.Event{{Kind: tui.EventSystemNote, Text: strings.Join(helpLines, "\n")}},
			}, nil
		case trimmed == "/mode":
			return tui.LocalCommandResult{Handled: true, ApprovalPicker: buildApprovalPicker(snapshot)}, nil
		case trimmed == "!":
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "! 用法: !<shell 命令>"}}}, nil
		case strings.HasPrefix(trimmed, "!"):
			rawCommand := strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
			if rawCommand == "" {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "! 用法: !<shell 命令>"}}}, nil
			}
			localConfig, err := a.configManager.Load()
			if err != nil {
				return tui.LocalCommandResult{}, err
			}
			runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), io.Discard, io.Discard)
			result, err := runtimeClient.ExecuteShellCommand(commandCtx, localConfig, rawCommand)
			if err != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
			}
			return tui.LocalCommandResult{Handled: true, Events: buildLocalShellCommandEvents(result)}, nil
		case trimmed == "/clear":
			return tui.LocalCommandResult{Handled: true, ClearHistory: true}, nil
		case trimmed == "/session":
			sessionID := strings.TrimSpace(snapshot.SessionID)
			resolvedSnapshot := snapshot
			if sessionID != "" && strings.TrimSpace(resolvedSnapshot.SessionTitle) == "" {
				title, err := lookupSessionTitle(commandCtx, providerClient, snapshot.DefaultFlow, sessionID)
				if err == nil {
					resolvedSnapshot.SessionTitle = strings.TrimSpace(title)
				}
			}
			text := tui.RenderSessionStatusText(resolvedSnapshot, "")
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: text}}}, nil
		case strings.EqualFold(trimmed, "/sessions") || strings.HasPrefix(strings.ToLower(trimmed), "/sessions "):
			page, err := parseSessionsPage(trimmed)
			if err != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
			}
			flowID := strings.TrimSpace(snapshot.DefaultFlow)
			if flowID == "" {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "当前未选择智能体，无法获取服务端会话。"}}}, nil
			}
			const pageSize = 10
			offset := (page - 1) * pageSize
			sessions, err := providerClient.ListSessions(commandCtx, flowID, offset, pageSize)
			if err != nil {
				return tui.LocalCommandResult{}, fmt.Errorf("%s", explainBackendFailure(err))
			}
			agentLabel := snapshot.DefaultFlowLabel
			if strings.TrimSpace(agentLabel) == "" {
				agentLabel = sanitizeFlowDisplay(flowID)
			}
			if sessions == nil || len(sessions.Items) == 0 {
				if sessions != nil && sessions.Total > 0 && page > 1 {
					return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: fmt.Sprintf("页码超出范围，请输入 1 到 %d。", totalPages(sessions.Total, pageSize))}}}, nil
				}
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: fmt.Sprintf("智能体 %s 当前没有服务端会话。", sanitizeFlowDisplay(agentLabel))}}}, nil
			}
			pages := totalPages(sessions.Total, pageSize)
			if pages == 0 {
				pages = 1
			}
			items := make([]tui.SessionItem, 0, len(sessions.Items))
			for _, item := range sessions.Items {
				name := strings.TrimSpace(item.Name)
				if name == "" {
					name = "<未命名会话>"
				}
				recentAt := item.UpdatedAt
				if recentAt.IsZero() {
					recentAt = item.CreatedAt
				}
				items = append(items, tui.SessionItem{
					ID:           item.ID,
					Title:        name,
					RelativeTime: formatRelativeTimeChinese(recentAt),
					IsCurrent:    strings.TrimSpace(snapshot.SessionID) != "" && item.ID == snapshot.SessionID,
				})
			}
			hint := "使用方向键选择，左右键翻页，Enter 切换当前会话，Esc 关闭。"
			if pages > 1 {
				hint = fmt.Sprintf("使用方向键选择，左右键翻页。当前共 %d 页，当前页 %d。", pages, page)
			}
			return tui.LocalCommandResult{
				Handled: true,
				SessionPicker: &tui.SessionPickerState{
					Title:      fmt.Sprintf("会话列表（%s）", sanitizeFlowDisplay(agentLabel)),
					Hint:       hint,
					Page:       page,
					TotalPages: pages,
					Items:      items,
				},
			}, nil
		case trimmed == "/new":
			if strings.TrimSpace(snapshot.DefaultFlow) == "" {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "当前未选择默认智能体，无法重置会话。"}}}, nil
			}
			if err := a.sessionStore.Delete(snapshot.DefaultFlow, snapshot.Workdir); err != nil {
				return tui.LocalCommandResult{}, err
			}
			return tui.LocalCommandResult{
				Handled:      true,
				ClearHistory: true,
				Events: []tui.Event{
					{Kind: tui.EventSessionUpdate, SessionID: ""},
				},
			}, nil
		default:
			return tui.LocalCommandResult{}, nil
		}
	}

	return tui.Run(ctx, snapshot, flowItems, runner, refreshSnapshot, flowChange, sessionChange, approvalChange, localCommand)
}

func (a *App) buildTUISnapshot(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
	backend backendStatus,
) (tui.Snapshot, error) {
	snapshot := tui.Snapshot{
		Version:             buildinfo.Version,
		BackendURL:          config.BackendBaseURL,
		BackendAvailable:    backend.Available,
		BackendStatusReason: strings.TrimSpace(backend.Reason),
		DefaultFlow:         flowID,
		DefaultFlowLabel:    sanitizeFlowDisplay(flowID),
		Workdir:             resolveLocalWorkdir(config.Workdir),
		ApprovalMode:        config.ApprovalMode,
	}
	if !backend.Available {
		if strings.TrimSpace(flowID) == "" {
			return snapshot, nil
		}

		sessionState, err := a.prepareSession(flowID, false, snapshot.Workdir)
		if err != nil {
			return tui.Snapshot{}, err
		}
		snapshot.SessionID = sessionState.SessionID
		return snapshot, nil
	}
	if versionInfo, err := providerClient.GetVersionInfo(ctx); err == nil && versionInfo != nil {
		snapshot.ServerPackage = strings.TrimSpace(versionInfo.Package)
		snapshot.ServerVersion = firstNonEmpty(strings.TrimSpace(versionInfo.MainVersion), strings.TrimSpace(versionInfo.Version))
	}
	if strings.TrimSpace(flowID) == "" {
		return snapshot, nil
	}

	sessionState, err := a.prepareSession(flowID, false, snapshot.Workdir)
	if err != nil {
		return tui.Snapshot{}, err
	}
	snapshot.SessionID = sessionState.SessionID
	if strings.TrimSpace(snapshot.SessionID) != "" {
		title, titleErr := lookupSessionTitle(ctx, providerClient, flowID, snapshot.SessionID)
		if titleErr == nil {
			snapshot.SessionTitle = strings.TrimSpace(title)
		}
		snapshot.History = loadRecentSessionHistory(ctx, providerClient, flowID, snapshot.SessionID)
	}

	metadata, err := providerClient.GetMetadata(ctx, flowID)
	if err == nil {
		snapshot.WelcomeMessage = metadata.WelcomeMessage
		if strings.TrimSpace(metadata.Name) != "" {
			snapshot.DefaultFlowLabel = metadata.Name
		}
	}
	return snapshot, nil
}

func buildLocalShellCommandEvents(result map[string]any) []tui.Event {
	events := []tui.Event{}
	if stdout, ok := result["stdout"].(string); ok {
		stdout = strings.TrimRight(stdout, "\n")
		if stdout != "" {
			events = append(events, tui.Event{Kind: tui.EventSystemNote, Text: stdout})
		}
	}
	if stderr, ok := result["stderr"].(string); ok {
		stderr = strings.TrimRight(stderr, "\n")
		if stderr != "" {
			events = append(events, tui.Event{Kind: tui.EventErrorNote, Text: stderr})
		}
	}
	return events
}

func resolveLocalWorkdir(configured string) string {
	base := strings.TrimSpace(configured)
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "."
		}
		base = cwd
	}

	absPath, err := filepath.Abs(base)
	if err != nil {
		return base
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err == nil && strings.TrimSpace(resolvedPath) != "" {
		absPath = resolvedPath
	}

	info, err := os.Stat(absPath)
	if err != nil || !info.IsDir() {
		return absPath
	}
	return absPath
}

func loadRecentSessionHistory(ctx context.Context, providerClient provider.Provider, flowID string, sessionID string) []tui.HistoryEntry {
	trimmedFlowID := strings.TrimSpace(flowID)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if providerClient == nil || trimmedFlowID == "" || trimmedSessionID == "" {
		return nil
	}

	const historyLimit = 30
	messages, err := providerClient.ListSessionMessages(ctx, trimmedFlowID, trimmedSessionID, 0, historyLimit)
	if err != nil || messages == nil || len(messages.Items) == 0 {
		return nil
	}

	entries := make([]tui.HistoryEntry, 0, len(messages.Items))
	for index := len(messages.Items) - 1; index >= 0; index-- {
		item := messages.Items[index]
		content := strings.TrimSpace(item.Text)
		if content == "" {
			continue
		}
		role := mapMessageRole(item)
		entries = append(entries, tui.HistoryEntry{Role: role, Content: content})
	}
	return entries
}

func mapMessageRole(item provider.SessionMessage) string {
	if item.Error {
		return "error"
	}
	sender := strings.TrimSpace(strings.ToLower(item.Sender))
	switch sender {
	case strings.ToLower("User"):
		return "user"
	case strings.ToLower("Machine"):
		return "assistant"
	default:
		return "system"
	}
}

type tuiLogWriter struct {
	emit    func(tui.Event)
	pending string
}

func (w *tuiLogWriter) Write(content []byte) (int, error) {
	text := w.pending + string(content)
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		w.pending = lines[0]
		return len(content), nil
	}

	for _, line := range lines[:len(lines)-1] {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !shouldSuppressTUILogLine(trimmed) {
			w.emit(tui.Event{Kind: tui.EventSystemNote, Text: trimmed})
		}
	}
	w.pending = lines[len(lines)-1]
	return len(content), nil
}

func (w *tuiLogWriter) Flush() {
	trimmed := strings.TrimSpace(w.pending)
	if trimmed != "" && !shouldSuppressTUILogLine(trimmed) {
		w.emit(tui.Event{Kind: tui.EventSystemNote, Text: trimmed})
	}
	w.pending = ""
}

func shouldSuppressTUILogLine(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "[执行客户端命令]")
}

func requiresTUICommandConfirmation(config internalconfig.Config, payload map[string]any) bool {
	if strings.TrimSpace(config.ApprovalMode) != "confirm" {
		return false
	}
	commandPayload, ok := payload["command"].(map[string]any)
	if !ok {
		return false
	}
	policy, ok := commandPayload["policy"].(map[string]any)
	if !ok {
		return true
	}
	value, ok := policy["confirmation_required"]
	if !ok {
		return true
	}
	if required, ok := value.(bool); ok {
		return required
	}
	if text, ok := value.(string); ok {
		return strings.EqualFold(strings.TrimSpace(text), "true")
	}
	return true
}

func buildTUIConfirmRequest(payload map[string]any) tui.ConfirmRequest {
	request := tui.ConfirmRequest{
		Title:   "客户端命令待确认",
		Command: previewBuiltInCommand(payload),
		Detail:  "该命令由服务端下发，是否允许在本地执行？",
	}
	return request
}

func previewBuiltInCommand(payload map[string]any) string {
	commandPayload, ok := payload["command"].(map[string]any)
	if !ok {
		return "<未知命令>"
	}
	arguments, ok := commandPayload["arguments"].(map[string]any)
	if !ok {
		return "<未知命令>"
	}
	if rawCommand, ok := arguments["raw_command"].(string); ok && strings.TrimSpace(rawCommand) != "" {
		return strings.TrimSpace(rawCommand)
	}
	parts := []string{}
	if commandName, ok := arguments["command"].(string); ok && strings.TrimSpace(commandName) != "" {
		parts = append(parts, strings.TrimSpace(commandName))
	}
	if rawArgs, ok := arguments["args"].([]any); ok {
		for _, item := range rawArgs {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
	}
	if len(parts) == 0 {
		return "<未知命令>"
	}
	return strings.Join(parts, " ")
}

func submitTUIClientCallFailure(
	ctx context.Context,
	providerClient provider.Provider,
	payload map[string]any,
	errorMessage string,
) error {
	result := provider.ClientCallResult{
		FlowID:       stringMapValue(payload, "flow_id"),
		SessionID:    nestedStringValue(payload, "context", "session_id"),
		MessageID:    nestedStringValue(payload, "context", "message_id"),
		RunID:        nestedStringValue(payload, "context", "run_id"),
		CallID:       nestedStringValue(payload, "function", "id"),
		Success:      false,
		ErrorMessage: errorMessage,
	}
	if result.FlowID == "" || result.RunID == "" || result.CallID == "" {
		return fmt.Errorf("客户端回调缺少关键字段")
	}
	return providerClient.SubmitClientCallResult(ctx, result)
}

func stringMapValue(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprintf("%v", value)
}

func nestedStringValue(payload map[string]any, keys ...string) string {
	current := payload
	for index, key := range keys {
		value, ok := current[key]
		if !ok {
			return ""
		}
		if index == len(keys)-1 {
			return fmt.Sprintf("%v", value)
		}
		next, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

func buildTUIRequestMetrics(runID string, stats *provider.RunStats) *tui.RequestMetrics {
	if stats == nil {
		return nil
	}
	resolvedRunID := strings.TrimSpace(stats.RunID)
	if resolvedRunID == "" {
		resolvedRunID = runID
	}
	metrics := &tui.RequestMetrics{RunID: resolvedRunID}
	if stats.TokenUsage != nil {
		metrics.TokenUsage = &tui.TokenUsage{
			InputTokens:  stats.TokenUsage.InputTokens,
			OutputTokens: stats.TokenUsage.OutputTokens,
			TotalTokens:  stats.TokenUsage.TotalTokens,
			StopReason:   stats.TokenUsage.StopReason,
			ModelName:    stats.TokenUsage.ModelName,
		}
		if stats.TokenUsage.ContextWindow != nil {
			metrics.TokenUsage.ContextWindow = &tui.ContextWindow{
				MaxInputTokens:              stats.TokenUsage.ContextWindow.MaxInputTokens,
				ShortTermContextTokens:      stats.TokenUsage.ContextWindow.ShortTermContextTokens,
				CurrentRoundInputTokens:     stats.TokenUsage.ContextWindow.CurrentRoundInputTokens,
				NextRoundAvailableTokens:    stats.TokenUsage.ContextWindow.NextRoundAvailableTokens,
				CurrentRoundAvailableTokens: stats.TokenUsage.ContextWindow.CurrentRoundAvailableTokens,
				HistoryMessageCount:         stats.TokenUsage.ContextWindow.HistoryMessageCount,
				PreparedMessageCount:        stats.TokenUsage.ContextWindow.PreparedMessageCount,
			}
		}
	}
	if len(stats.Compressions) > 0 {
		latest := stats.Compressions[len(stats.Compressions)-1]
		metrics.Compression = &tui.CompressionStats{
			Count:            len(stats.Compressions),
			BeforeTokens:     latest.BeforeTokens,
			AfterTokens:      latest.AfterTokens,
			RemainingTokens:  latest.RemainingTokens,
			CompressionRatio: latest.CompressionRatio,
			TargetType:       latest.TargetType,
		}
	}
	return metrics
}

func parseSessionsPage(command string) (int, error) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 1 {
		return 1, nil
	}
	if len(fields) != 2 {
		return 0, fmt.Errorf("/sessions 用法: /sessions [页码]")
	}
	page, err := strconv.Atoi(strings.TrimSpace(fields[1]))
	if err != nil || page < 1 {
		return 0, fmt.Errorf("/sessions 页码必须是大于等于 1 的数字")
	}
	return page, nil
}

func totalPages(total int, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return (total + pageSize - 1) / pageSize
}

func nextSessionsPage(current int, pages int) int {
	if current < pages {
		return current + 1
	}
	if pages > 1 {
		return pages - 1
	}
	return 1
}

func formatRelativeTimeChinese(value time.Time) string {
	if value.IsZero() {
		return "刚刚"
	}
	delta := time.Since(value)
	if delta < 0 {
		delta = 0
	}
	if delta < time.Minute {
		return "刚刚"
	}
	if delta < time.Hour {
		return fmt.Sprintf("%d 分钟前", int(delta/time.Minute))
	}
	if delta < 24*time.Hour {
		return fmt.Sprintf("%d 小时前", int(delta/time.Hour))
	}
	if delta < 30*24*time.Hour {
		return fmt.Sprintf("%d 天前", int(delta/(24*time.Hour)))
	}
	if delta < 365*24*time.Hour {
		return fmt.Sprintf("%d 个月前", int(delta/(30*24*time.Hour)))
	}
	return fmt.Sprintf("%d 年前", int(delta/(365*24*time.Hour)))
}

func buildApprovalPicker(snapshot tui.Snapshot) *tui.ApprovalPickerState {
	current := strings.TrimSpace(snapshot.ApprovalMode)
	if current == "" {
		current = "confirm"
	}
	return &tui.ApprovalPickerState{
		Title: "命令执行模式",
		Hint:  "选择客户端命令的执行模式。",
		Options: []tui.ApprovalOption{
			{Value: "confirm", Label: "每次确认", Description: "执行客户端命令前逐次确认。", IsCurrent: current == "confirm"},
			{Value: "auto", Label: "自动执行", Description: "以后都同意，客户端命令默认自动执行。", IsCurrent: current == "auto"},
			{Value: "readonly", Label: "只读模式", Description: "禁止执行本地命令。", IsCurrent: current == "readonly"},
		},
	}
}
