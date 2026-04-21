package cmd

import (
	"context"
	"fmt"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func modeAllowsBuiltIn(mode string) bool {
	return normalizeRequestMode(mode) == provider.RequestTypeAgent
}

func modeAllowsClientTool(mode string, toolName string) bool {
	if normalizeRequestMode(mode) == provider.RequestTypeAgent {
		return true
	}
	switch strings.TrimSpace(toolName) {
	case "search_files", "git_status", "git_diff", "git_log", "read_file", "list_dir", "skills_list", "skill_view":
		return true
	default:
		return false
	}
}

func submitClientCallFailure(
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