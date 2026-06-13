package cmd

import (
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func applySessionRequestLineage(state *agent.SessionState, flowID string, workdir string, requestMode string, runID string) bool {
	if state == nil {
		return false
	}
	changed := false
	trimmedFlowID := strings.TrimSpace(flowID)
	if trimmedFlowID != "" && strings.TrimSpace(state.FlowID) != trimmedFlowID {
		state.FlowID = trimmedFlowID
		changed = true
	}
	resolvedWorkdir := resolveLocalWorkdir(firstNonEmpty(strings.TrimSpace(state.Workdir), workdir))
	if resolvedWorkdir != "" && strings.TrimSpace(state.Workdir) != resolvedWorkdir {
		state.Workdir = resolvedWorkdir
		changed = true
	}
	resolvedContextDir := resolveLocalWorkdir(firstNonEmpty(strings.TrimSpace(state.ContextDir), resolvedWorkdir))
	if resolvedContextDir != "" && strings.TrimSpace(state.ContextDir) != resolvedContextDir {
		state.ContextDir = resolvedContextDir
		changed = true
	}
	normalizedMode := normalizeRequestMode(requestMode)
	if normalizedMode != "" && strings.TrimSpace(state.RequestMode) != normalizedMode {
		state.RequestMode = normalizedMode
		changed = true
	}
	trimmedRunID := strings.TrimSpace(runID)
	if trimmedRunID != "" && strings.TrimSpace(state.LastRunID) != trimmedRunID {
		state.LastRunID = trimmedRunID
		changed = true
	}
	return changed
}

func updateSessionLineageFromEvent(state *agent.SessionState, event provider.StreamEvent, currentRunID string) bool {
	return updateSessionLineageFromPayload(state, event.Data, currentRunID)
}

func updateSessionLineageFromPayload(state *agent.SessionState, payload map[string]any, currentRunID string) bool {
	if state == nil || len(payload) == 0 {
		return false
	}
	changed := false
	eventRunID := resolveLineageRunID(payload)
	// 仅当事件属于当前请求的 run 时才更新 LastRunID/LastMessageID，
	// 防止子智能体的 run_id/message_id 污染父会话的 session state 文件。
	runIDMatches := strings.TrimSpace(currentRunID) == "" || eventRunID == "" || strings.TrimSpace(eventRunID) == strings.TrimSpace(currentRunID)
	if runIDMatches {
		if eventRunID != "" && strings.TrimSpace(state.LastRunID) != eventRunID {
			state.LastRunID = eventRunID
			changed = true
		}
		if messageID := resolveLineageMessageID(payload); messageID != "" && strings.TrimSpace(state.LastMessageID) != messageID {
			state.LastMessageID = messageID
			changed = true
		}
	}
	return changed
}

func resolveLineageRunID(payload map[string]any) string {
	if runID := firstNonEmptyTrimmedStringMapValue(payload, "run_id"); runID != "" {
		return runID
	}
	return strings.TrimSpace(nestedStringValue(payload, "context", "run_id"))
}

func resolveLineageMessageID(payload map[string]any) string {
	if messageID := firstNonEmptyTrimmedStringMapValue(payload, "message_id", "id"); messageID != "" {
		return messageID
	}
	return strings.TrimSpace(nestedStringValue(payload, "context", "message_id"))
}

func saveSessionStateIfReady(store *agent.SessionStore, state agent.SessionState) error {
	if store == nil {
		return nil
	}
	if strings.TrimSpace(state.FlowID) == "" || strings.TrimSpace(state.SessionID) == "" {
		return nil
	}
	return store.Save(state)
}