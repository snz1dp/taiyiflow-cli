package cmd

import (
	"context"
	"fmt"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
)

type callbackAuthRefreshProvider struct {
	provider.Provider
	app    *App
	client *providerclient.Client
	config *internalconfig.Config
}

func (p *callbackAuthRefreshProvider) shouldRetryAfterAuthRefresh(ctx context.Context, err error) (bool, error) {
	if err == nil || !isAuthenticationError(err) || p.app == nil || p.client == nil || p.config == nil {
		return false, nil
	}

	updatedConfig, _, refreshErr := p.app.tryPersistentLogin(ctx, p.client, *p.config)
	if refreshErr != nil {
		return false, fmt.Errorf("%w；自动刷新登录失败: %v", err, refreshErr)
	}
	*p.config = updatedConfig
	if strings.TrimSpace(updatedConfig.AccessToken) == "" {
		return false, nil
	}
	return true, nil
}

func (p *callbackAuthRefreshProvider) ListFlows(ctx context.Context, filter string, agentType string, offset, limit int) (*provider.FlowList, error) {
	flows, err := p.Provider.ListFlows(ctx, filter, agentType, offset, limit)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return flows, err
	}
	return p.Provider.ListFlows(ctx, filter, agentType, offset, limit)
}

func (p *callbackAuthRefreshProvider) ListKnowledgeBases(ctx context.Context, filter string, offset, limit int) (*provider.KnowledgeBaseList, error) {
	knowledgeBases, err := p.Provider.ListKnowledgeBases(ctx, filter, offset, limit)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return knowledgeBases, err
	}
	return p.Provider.ListKnowledgeBases(ctx, filter, offset, limit)
}

func (p *callbackAuthRefreshProvider) ListSessions(ctx context.Context, flowID string, offset, limit int) (*provider.SessionList, error) {
	sessions, err := p.Provider.ListSessions(ctx, flowID, offset, limit)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return sessions, err
	}
	return p.Provider.ListSessions(ctx, flowID, offset, limit)
}

func (p *callbackAuthRefreshProvider) UpdateSessionTitle(ctx context.Context, flowID string, sessionID string, sessionTitle string) (string, error) {
	title, err := p.Provider.UpdateSessionTitle(ctx, flowID, sessionID, sessionTitle)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return "", refreshErr
	}
	if !retry {
		return title, err
	}
	return p.Provider.UpdateSessionTitle(ctx, flowID, sessionID, sessionTitle)
}

func (p *callbackAuthRefreshProvider) ListSessionMessages(ctx context.Context, flowID string, sessionID string, offset, limit int) (*provider.SessionMessageList, error) {
	messages, err := p.Provider.ListSessionMessages(ctx, flowID, sessionID, offset, limit)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return messages, err
	}
	return p.Provider.ListSessionMessages(ctx, flowID, sessionID, offset, limit)
}

func (p *callbackAuthRefreshProvider) CreateSessionID(ctx context.Context) (string, error) {
	sessionID, err := p.Provider.CreateSessionID(ctx)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return "", refreshErr
	}
	if !retry {
		return sessionID, err
	}
	return p.Provider.CreateSessionID(ctx)
}

func (p *callbackAuthRefreshProvider) UploadFile(ctx context.Context, flowID string, sessionID string, localPath string, remotePath string) (*provider.UploadResult, error) {
	result, err := p.Provider.UploadFile(ctx, flowID, sessionID, localPath, remotePath)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return result, err
	}
	return p.Provider.UploadFile(ctx, flowID, sessionID, localPath, remotePath)
}

func (p *callbackAuthRefreshProvider) RunDocumentUnstructured(ctx context.Context, request provider.DocumentUnstructuredRequest) (*provider.DocumentUnstructuredResult, error) {
	result, err := p.Provider.RunDocumentUnstructured(ctx, request)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return result, err
	}
	return p.Provider.RunDocumentUnstructured(ctx, request)
}

func (p *callbackAuthRefreshProvider) GetVersionInfo(ctx context.Context) (*provider.ServiceVersion, error) {
	versionInfo, err := p.Provider.GetVersionInfo(ctx)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return versionInfo, err
	}
	return p.Provider.GetVersionInfo(ctx)
}

func (p *callbackAuthRefreshProvider) GetServiceConfig(ctx context.Context) (*provider.ServiceConfig, error) {
	serviceConfig, err := p.Provider.GetServiceConfig(ctx)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return serviceConfig, err
	}
	return p.Provider.GetServiceConfig(ctx)
}

func (p *callbackAuthRefreshProvider) GetCurrentUserInfo(ctx context.Context) (*provider.CurrentUser, error) {
	currentUser, err := p.Provider.GetCurrentUserInfo(ctx)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return currentUser, err
	}
	return p.Provider.GetCurrentUserInfo(ctx)
}

func (p *callbackAuthRefreshProvider) GetMetadata(ctx context.Context, flowID string) (*provider.Metadata, error) {
	metadata, err := p.Provider.GetMetadata(ctx, flowID)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return metadata, err
	}
	return p.Provider.GetMetadata(ctx, flowID)
}

func (p *callbackAuthRefreshProvider) ResolveSkillPreview(ctx context.Context, request provider.SkillResolveRequest) (*provider.SkillResolvePreview, error) {
	ensuredSessionID, ensureErr := clientruntime.EnsureSessionID(ctx, p, request.SessionID)
	if ensureErr != nil {
		return nil, ensureErr
	}
	request.SessionID = ensuredSessionID
	preview, err := p.Provider.ResolveSkillPreview(ctx, request)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return preview, err
	}
	return p.Provider.ResolveSkillPreview(ctx, request)
}

func (p *callbackAuthRefreshProvider) Stream(ctx context.Context, request provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	ensuredSessionID, ensureErr := clientruntime.EnsureSessionID(ctx, p, request.SessionID)
	if ensureErr != nil {
		return nil, nil, ensureErr
	}
	request.SessionID = ensuredSessionID
	stream, session, err := p.Provider.Stream(ctx, request)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, nil, refreshErr
	}
	if !retry {
		return stream, session, err
	}
	return p.Provider.Stream(ctx, request)
}

func (p *callbackAuthRefreshProvider) GetRunStats(ctx context.Context, flowID string, sessionID string, runID string) (*provider.RunStats, error) {
	stats, err := p.Provider.GetRunStats(ctx, flowID, sessionID, runID)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return stats, err
	}
	return p.Provider.GetRunStats(ctx, flowID, sessionID, runID)
}

func (p *callbackAuthRefreshProvider) GetSessionContextWindow(ctx context.Context, flowID string, sessionID string) (*provider.SessionContextPreview, error) {
	preview, err := p.Provider.GetSessionContextWindow(ctx, flowID, sessionID)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return preview, err
	}
	return p.Provider.GetSessionContextWindow(ctx, flowID, sessionID)
}

func (p *callbackAuthRefreshProvider) SubmitClientCallResult(ctx context.Context, result provider.ClientCallResult) error {
	err := p.Provider.SubmitClientCallResult(ctx, result)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return refreshErr
	}
	if !retry {
		return err
	}
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
	case "ask_user", "lsp_query", "glob_files", "task_output", "task_list", "search_files", "read_file", "skill", "taiyiflow-in-chrome__tabs_context", "taiyiflow-in-chrome__read_text", "taiyiflow-in-chrome__read_console", "taiyiflow-computer__displays_context", "taiyiflow-computer__windows_context", "taiyiflow-computer__screenshot":
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
