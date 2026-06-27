package cmd

import (
	"context"
	"fmt"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func sanitizeFlowDisplay(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "<未配置>"
	}
	return trimmed
}

func (a *App) resolveFlowDisplayName(ctx context.Context, client provider.Provider, flowIDOrName string) string {
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

// resolveOwnerAppID 通过 GET /agents/flows/{flow_id} 获取智能体的 owner_appid。
// 如果获取失败或 owner_appid 为空，返回错误，调用方应将此视为服务端不可用。
func resolveOwnerAppID(ctx context.Context, client provider.Provider, flowID string) (string, error) {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return "", fmt.Errorf("flowID 为空，无法获取 owner_appid")
	}
	if client == nil {
		return "", fmt.Errorf("provider 为空，无法获取 owner_appid")
	}
	flow, err := client.GetFlow(ctx, flowID)
	if err != nil {
		return "", fmt.Errorf("获取智能体 owner_appid 失败，服务端可能不可用: %w", err)
	}
	ownerAppID := strings.TrimSpace(flow.OwnerAppID)
	if ownerAppID == "" {
		return "", fmt.Errorf("智能体 owner_appid 为空，服务端可能不可用")
	}
	return ownerAppID, nil
}

func (a *App) resolveFlowIdentifier(ctx context.Context, client provider.Provider, flowIDOrName string) string {
	trimmed := strings.TrimSpace(flowIDOrName)
	if trimmed == "" || client == nil {
		return trimmed
	}

	flows, err := client.ListFlows(ctx, "", "chat", 0, 100)
	if err == nil {
		for _, item := range flows.Items {
			if item.ID == trimmed || item.Name == trimmed {
				if strings.TrimSpace(item.ID) != "" {
					return strings.TrimSpace(item.ID)
				}
				return trimmed
			}
		}
	}

	metadata, err := client.GetMetadata(ctx, trimmed)
	if err == nil && strings.TrimSpace(metadata.ID) != "" {
		return strings.TrimSpace(metadata.ID)
	}

	return trimmed
}
