package cmd

import (
	"context"
	"fmt"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

type callbackAuthRefreshProvider struct {
	provider.Provider
	app    *App
	client *providerclient.Client
	config *internalconfig.Config
}

func (p *callbackAuthRefreshProvider) Stream(ctx context.Context, request provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	stream, session, err := p.Provider.Stream(ctx, request)
	if err == nil || !isAuthenticationError(err) || p.app == nil || p.client == nil || p.config == nil {
		return stream, session, err
	}

	updatedConfig, _, refreshErr := p.app.tryPersistentLogin(ctx, p.client, *p.config)
	if refreshErr != nil {
		return nil, nil, fmt.Errorf("%w；自动刷新登录失败: %v", err, refreshErr)
	}
	if strings.TrimSpace(updatedConfig.AccessToken) == "" {
		return nil, nil, err
	}
	*p.config = updatedConfig
	return p.Provider.Stream(ctx, request)
}

func (p *callbackAuthRefreshProvider) SubmitClientCallResult(ctx context.Context, result provider.ClientCallResult) error {
	err := p.Provider.SubmitClientCallResult(ctx, result)
	if err == nil || !isAuthenticationError(err) || p.app == nil || p.client == nil || p.config == nil {
		return err
	}

	updatedConfig, changed, refreshErr := p.app.tryPersistentLogin(ctx, p.client, *p.config)
	if refreshErr != nil {
		return fmt.Errorf("%w；自动刷新登录失败: %v", err, refreshErr)
	}
	if strings.TrimSpace(updatedConfig.AccessToken) == "" {
		return err
	}
	_ = changed
	*p.config = updatedConfig
	return p.Provider.SubmitClientCallResult(ctx, result)
}

func (a *App) providerWithCallbackAuthRefresh(providerClient provider.Provider, config *internalconfig.Config) provider.Provider {
	if existing, ok := providerClient.(*callbackAuthRefreshProvider); ok {
		existing.config = config
		return existing
	}
	client, ok := providerClient.(*providerclient.Client)
	if !ok || a == nil || a.configManager == nil || config == nil {
		return providerClient
	}
	if strings.TrimSpace(config.PersistentToken) == "" {
		return providerClient
	}
	return &callbackAuthRefreshProvider{Provider: providerClient, app: a, client: client, config: config}
}

func modeAllowsBuiltIn(mode string) bool {
	return normalizeRequestMode(mode) == provider.RequestTypeAgent
}

func modeAllowsClientTool(mode string, toolName string) bool {
	if normalizeRequestMode(mode) == provider.RequestTypeAgent {
		return true
	}
	switch strings.TrimSpace(toolName) {
	case "lsp_query", "glob_files", "task_output", "task_list", "search_files", "read_file", "skill":
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
