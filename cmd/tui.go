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
	flowID := strings.TrimSpace(config.DefaultFlow)
	sessionState, err := a.prepareSession(flowID, false)
	if err != nil {
		return err
	}

	welcomeMessage := ""
	if flowID != "" {
		metadata, metadataErr := providerClient.GetMetadata(ctx, flowID)
		if metadataErr == nil {
			welcomeMessage = metadata.WelcomeMessage
		}
	}

	runner := func(runCtx context.Context, input string, emit func(tui.Event)) error {
		currentFlow := strings.TrimSpace(config.DefaultFlow)
		if currentFlow == "" {
			return fmt.Errorf("未配置默认 flow，请先执行 taiyiflow flows use <ID>")
		}

		logWriter := &tuiLogWriter{emit: emit}
		defer logWriter.Flush()

		runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), logWriter, logWriter)
		stream, streamSession, err := providerClient.Stream(runCtx, provider.StreamRequest{
			FlowID:                 currentFlow,
			Input:                  input,
			InputType:              "any",
			OutputType:             "any",
			SessionID:              sessionState.SessionID,
			RequestType:            "agent",
			CommandExecutionTarget: "client",
			ClientMCPServers:       a.runtime.ClientMCPServers(),
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
				if requiresTUICommandConfirmation(config, event.Data) {
					emit(tui.Event{Kind: tui.EventErrorNote, Text: "TUI 当前不支持交互式命令确认，已拒绝本次客户端命令。可将 approval_mode 设为 auto 或 readonly。"})
					if err := submitTUIClientCallFailure(runCtx, providerClient, event.Data, "TUI 当前不支持交互式命令确认"); err != nil {
						return err
					}
					continue
				}
				if err := runtimeClient.HandleBuiltInEvent(runCtx, providerClient, config, event.Data); err != nil {
					return err
				}
			case provider.EventClientTool:
				if err := runtimeClient.HandleClientToolEvent(runCtx, providerClient, config, event.Data); err != nil {
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

	return tui.Run(ctx, tui.Snapshot{
		Version:        buildinfo.Version,
		BackendURL:     config.BackendBaseURL,
		DefaultFlow:    config.DefaultFlow,
		Workdir:        config.Workdir,
		ApprovalMode:   config.ApprovalMode,
		SessionID:      sessionState.SessionID,
		WelcomeMessage: welcomeMessage,
	}, runner)
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
