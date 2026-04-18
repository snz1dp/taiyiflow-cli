package cmd

import (
	"context"
	"fmt"
	"strings"

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
	snapshot, err := a.buildTUISnapshot(ctx, providerClient, config, strings.TrimSpace(config.DefaultFlow))
	if err != nil {
		return err
	}

	flowItems := []tui.FlowItem{}
	flows, flowErr := providerClient.ListFlows(ctx, "", 0, 100)
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

	runner := func(runCtx context.Context, request tui.Request, emit func(tui.Event), confirm func(tui.ConfirmRequest) bool) error {
		currentFlow := strings.TrimSpace(request.FlowID)
		if currentFlow == "" {
			return fmt.Errorf("未配置默认 flow，请先执行 taiyiflow flows use <ID>")
		}

		localConfig, err := a.configManager.Load()
		if err != nil {
			return err
		}

		sessionState, err := a.prepareSession(currentFlow, false)
		if err != nil {
			return err
		}

		logWriter := &tuiLogWriter{emit: emit}
		defer logWriter.Flush()

		runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), logWriter, logWriter)
		stream, streamSession, err := providerClient.Stream(runCtx, provider.StreamRequest{
			FlowID:                 currentFlow,
			Input:                  request.Input,
			InputType:              "any",
			OutputType:             "any",
			SessionID:              sessionState.SessionID,
			RequestType:            "agent",
			CommandExecutionTarget: "client",
			ClientMCPServers:       runtimeClient.ClientMCPServers(),
		})
		if err != nil {
			return err
		}

		if streamSession != nil && strings.TrimSpace(streamSession.SessionID) != "" {
			sessionState.SessionID = streamSession.SessionID
			sessionState.FlowID = currentFlow
			if err := a.sessionStore.Save(sessionState); err != nil {
				return err
			}
			emit(tui.Event{Kind: tui.EventSessionUpdate, SessionID: streamSession.SessionID})
		}

		for event := range stream {
			switch event.Type {
			case provider.EventMessage:
				if message, ok := event.Data["message"].(string); ok && strings.TrimSpace(message) != "" {
					emit(tui.Event{Kind: tui.EventSystemNote, Text: message})
				}
				if chunk, ok := event.Data["chunk"].(string); ok && chunk != "" {
					emit(tui.Event{Kind: tui.EventAssistantChunk, Text: chunk})
				}
			case provider.EventToolCall:
				if text, ok := event.Data["text"].(string); ok && strings.TrimSpace(text) != "" {
					emit(tui.Event{Kind: tui.EventSystemNote, Text: "工具调用: " + text})
				}
			case provider.EventBuiltIn:
				if requiresTUICommandConfirmation(localConfig, event.Data) {
					approved := confirm(buildTUIConfirmRequest(event.Data))
					if !approved {
						emit(tui.Event{Kind: tui.EventErrorNote, Text: "已拒绝本次客户端命令执行。"})
						if err := submitTUIClientCallFailure(runCtx, providerClient, event.Data, "用户在 TUI 中拒绝执行客户端命令"); err != nil {
							return err
						}
						continue
					}
					execConfig := localConfig
					execConfig.ApprovalMode = "auto"
					if err := runtimeClient.HandleBuiltInEvent(runCtx, providerClient, execConfig, event.Data); err != nil {
						return err
					}
					continue
				}
				if err := runtimeClient.HandleBuiltInEvent(runCtx, providerClient, localConfig, event.Data); err != nil {
					return err
				}
			case provider.EventClientTool:
				if err := runtimeClient.HandleClientToolEvent(runCtx, providerClient, localConfig, event.Data); err != nil {
					return err
				}
			case provider.EventError:
				return fmt.Errorf("后端返回错误: %v", event.Data["error"])
			case provider.EventClose:
				return nil
			}
		}

		return nil
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
		return a.buildTUISnapshot(changeCtx, providerClient, updatedConfig, updatedConfig.DefaultFlow)
	}

	return tui.Run(ctx, snapshot, flowItems, runner, flowChange)
}

func (a *App) buildTUISnapshot(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
) (tui.Snapshot, error) {
	snapshot := tui.Snapshot{
		Version:      buildinfo.Version,
		BackendURL:   config.BackendBaseURL,
		DefaultFlow:  flowID,
		Workdir:      config.Workdir,
		ApprovalMode: config.ApprovalMode,
	}
	if strings.TrimSpace(flowID) == "" {
		return snapshot, nil
	}

	sessionState, err := a.prepareSession(flowID, false)
	if err != nil {
		return tui.Snapshot{}, err
	}
	snapshot.SessionID = sessionState.SessionID

	metadata, err := providerClient.GetMetadata(ctx, flowID)
	if err == nil {
		snapshot.WelcomeMessage = metadata.WelcomeMessage
	}
	return snapshot, nil
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
		if trimmed != "" {
			w.emit(tui.Event{Kind: tui.EventSystemNote, Text: trimmed})
		}
	}
	w.pending = lines[len(lines)-1]
	return len(content), nil
}

func (w *tuiLogWriter) Flush() {
	trimmed := strings.TrimSpace(w.pending)
	if trimmed != "" {
		w.emit(tui.Event{Kind: tui.EventSystemNote, Text: trimmed})
	}
	w.pending = ""
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
		Detail:  "该命令由服务端通过 run_command_via_client 下发，是否允许在本地执行？",
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
		return fmt.Errorf("TUI 模式下客户端回调缺少关键字段")
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
