package runtime

import (
	"sort"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

type HookEventName string

const (
	HookPreToolCall HookEventName = "pre_tool_call"
	HookPostToolCall HookEventName = "post_tool_call"
	HookOnComplete HookEventName = "on_complete"
)

type HookEvent struct {
	Name         HookEventName
	Timestamp    time.Time
	ToolType     string
	ToolName     string
	Preview      string
	FlowID       string
	SessionID    string
	RunID        string
	MessageID    string
	CallID       string
	RequestMode  string
	Success      *bool
	ErrorMessage string
	Payload      map[string]any
}

type HookHandler func(config internalconfig.Config, event HookEvent) error

func (r *ClientRuntime) RegisterHookHandler(handler HookHandler) {
	if handler == nil {
		return
	}
	r.hookHandlers = append(r.hookHandlers, handler)
}

func (r *ClientRuntime) EmitOnComplete(config internalconfig.Config, flowID string, sessionID string, runID string, messageID string, requestMode string, success bool, errorMessage string) {
	r.emitHook(config, HookEvent{
		Name:         HookOnComplete,
		FlowID:       strings.TrimSpace(flowID),
		SessionID:    strings.TrimSpace(sessionID),
		RunID:        strings.TrimSpace(runID),
		MessageID:    strings.TrimSpace(messageID),
		RequestMode:  strings.TrimSpace(requestMode),
		Success:      boolPtrHook(success),
		ErrorMessage: strings.TrimSpace(errorMessage),
	})
}

func (r *ClientRuntime) emitHook(config internalconfig.Config, event HookEvent) {
	if r == nil || len(r.hookHandlers) == 0 {
		return
	}
	if event.Name == "" {
		return
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	event.ToolType = strings.TrimSpace(event.ToolType)
	event.ToolName = strings.TrimSpace(event.ToolName)
	event.Preview = strings.TrimSpace(event.Preview)
	event.FlowID = strings.TrimSpace(event.FlowID)
	event.SessionID = strings.TrimSpace(event.SessionID)
	event.RunID = strings.TrimSpace(event.RunID)
	event.MessageID = strings.TrimSpace(event.MessageID)
	event.CallID = strings.TrimSpace(event.CallID)
	event.RequestMode = strings.TrimSpace(event.RequestMode)
	event.ErrorMessage = strings.TrimSpace(event.ErrorMessage)
	event.Payload = cloneHookPayload(event.Payload)
	for _, handler := range r.hookHandlers {
		if handler == nil {
			continue
		}
		_ = handler(config, event)
	}
}

func auditHookHandler(config internalconfig.Config, event HookEvent) error {
	return appendAuditLog(config, string(event.Name), event.auditPayload())
}

func (event HookEvent) auditPayload() map[string]any {
	payload := map[string]any{}
	if event.ToolType != "" {
		payload["tool_type"] = event.ToolType
	}
	if event.ToolName != "" {
		payload["tool_name"] = event.ToolName
	}
	if event.Preview != "" {
		payload["preview"] = event.Preview
	}
	if event.FlowID != "" {
		payload["flow_id"] = event.FlowID
	}
	if event.SessionID != "" {
		payload["session_id"] = event.SessionID
	}
	if event.RunID != "" {
		payload["run_id"] = event.RunID
	}
	if event.MessageID != "" {
		payload["message_id"] = event.MessageID
	}
	if event.CallID != "" {
		payload["call_id"] = event.CallID
	}
	if event.RequestMode != "" {
		payload["request_mode"] = event.RequestMode
	}
	if event.Success != nil {
		payload["success"] = *event.Success
	}
	if event.ErrorMessage != "" {
		payload["error_message"] = event.ErrorMessage
	}
	if len(event.Payload) > 0 {
		payload["detail"] = event.Payload
	}
	return payload
}

func buildPreToolHookEvent(toolType string, toolName string, preview string, payload map[string]any, detail map[string]any) HookEvent {
	return HookEvent{
		Name:      HookPreToolCall,
		ToolType:  strings.TrimSpace(toolType),
		ToolName:  strings.TrimSpace(toolName),
		Preview:   strings.TrimSpace(preview),
		FlowID:    strings.TrimSpace(stringValue(payload["flow_id"])),
		SessionID: strings.TrimSpace(nestedString(payload, "context", "session_id")),
		RunID:     strings.TrimSpace(nestedString(payload, "context", "run_id")),
		MessageID: strings.TrimSpace(nestedString(payload, "context", "message_id")),
		CallID:    strings.TrimSpace(firstNonEmptyHook(nestedString(payload, "function", "id"), nestedString(payload, "command", "id"))),
		Payload:   detail,
	}
}

func buildPostToolHookEvent(toolType string, toolName string, preview string, payload map[string]any, success bool, execErr error, result any, detail map[string]any) HookEvent {
	mergedDetail := cloneHookPayload(detail)
	resultSummary := summarizeHookResult(result)
	if resultSummary != nil {
		mergedDetail["result"] = resultSummary
	}
	errorMessage := ""
	if execErr != nil {
		errorMessage = execErr.Error()
	}
	return HookEvent{
		Name:         HookPostToolCall,
		ToolType:     strings.TrimSpace(toolType),
		ToolName:     strings.TrimSpace(toolName),
		Preview:      strings.TrimSpace(preview),
		FlowID:       strings.TrimSpace(stringValue(payload["flow_id"])),
		SessionID:    strings.TrimSpace(nestedString(payload, "context", "session_id")),
		RunID:        strings.TrimSpace(nestedString(payload, "context", "run_id")),
		MessageID:    strings.TrimSpace(nestedString(payload, "context", "message_id")),
		CallID:       strings.TrimSpace(firstNonEmptyHook(nestedString(payload, "function", "id"), nestedString(payload, "command", "id"))),
		Success:      boolPtrHook(success),
		ErrorMessage: strings.TrimSpace(errorMessage),
		Payload:      mergedDetail,
	}
}

func summarizeHookResult(result any) any {
	switch typed := result.(type) {
	case nil:
		return nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if len(trimmed) > 200 {
			return trimmed[:200]
		}
		return trimmed
	case map[string]any:
		if len(typed) == 0 {
			return map[string]any{}
		}
		summary := map[string]any{}
		for _, key := range []string{"task_id", "status", "text", "path", "count", "child_session_id", "child_run_id", "child_message_id"} {
			if value, ok := typed[key]; ok {
				summary[key] = value
			}
		}
		if len(summary) > 0 {
			return summary
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return map[string]any{"keys": keys, "size": len(typed)}
	case []any:
		return map[string]any{"items": len(typed)}
	default:
		return typed
	}
}

func cloneHookPayload(payload map[string]any) map[string]any {
	if len(payload) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(payload))
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}

func firstNonEmptyHook(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func boolPtrHook(value bool) *bool {
	result := value
	return &result
}