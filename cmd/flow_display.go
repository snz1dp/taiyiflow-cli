package cmd

import (
	"context"
	"regexp"
	"strings"

	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

var likelyUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func sanitizeFlowDisplay(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "<未配置>"
	}
	if likelyUUIDPattern.MatchString(trimmed) {
		return "<已配置>"
	}
	return trimmed
}

func (a *App) resolveFlowDisplayName(ctx context.Context, client *providerclient.Client, flowIDOrName string) string {
	trimmed := strings.TrimSpace(flowIDOrName)
	if trimmed == "" {
		return "<未配置>"
	}

	flows, err := client.ListFlows(ctx, "", "chat", 0, 100)
	if err == nil {
		for _, item := range flows.Items {
			if item.ID == trimmed || item.Name == trimmed {
				if strings.TrimSpace(item.Name) != "" {
					return item.Name
				}
				return sanitizeFlowDisplay(trimmed)
			}
		}
	}

	metadata, err := client.GetMetadata(ctx, trimmed)
	if err == nil && strings.TrimSpace(metadata.Name) != "" {
		return metadata.Name
	}

	return sanitizeFlowDisplay(trimmed)
}
