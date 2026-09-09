package cmd

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
)

type callbackAuthRefreshProvider struct {
	provider.Provider
	app           *App
	client        *providerclient.Client
	config        *internalconfig.Config
	serviceConfig *provider.ServiceConfig
	// refreshMu 串行化持久令牌刷新登录。callbackAuthRefreshProvider 可能被多个 goroutine
	// 共享（后端 API 调用 + 并发 MCP 工具调用），加锁避免重复刷新风暴，并消除对 *config
	// 的并发写入竞争（*p.config = updatedConfig 是整体结构体写入）。
	refreshMu sync.Mutex
}

func (p *callbackAuthRefreshProvider) shouldRetryAfterAuthRefresh(ctx context.Context, err error) (bool, error) {
	if err == nil || !isAuthenticationError(err) {
		return false, nil
	}
	retry, refreshErr := p.refreshAuth(ctx)
	if refreshErr != nil {
		return false, fmt.Errorf("%w；自动刷新登录失败: %v", err, refreshErr)
	}
	return retry, nil
}

// RefreshAuth 触发一次持久令牌刷新登录，返回 true 表示刷新成功且 access_token 已更新。
// 供 MCP 调用在收到 401/403 时复用，与后端自动刷新登录语义对齐；忽略刷新错误细节，
// 只关心是否拿到可用的新令牌（失败时 MCP 调用按原始鉴权错误返回）。
func (p *callbackAuthRefreshProvider) RefreshAuth(ctx context.Context) bool {
	retry, _ := p.refreshAuth(ctx)
	return retry
}

// refreshAuth 执行实际的持久令牌刷新登录，通过 refreshMu 串行化。
// 返回 (是否拿到新令牌, 刷新过程中的错误)。nil 依赖（app/client/config）视为不可刷新。
func (p *callbackAuthRefreshProvider) refreshAuth(ctx context.Context) (bool, error) {
	if p.app == nil || p.client == nil || p.config == nil {
		return false, nil
	}
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()

	updatedConfig, _, refreshErr := p.app.tryPersistentLogin(ctx, p.client, *p.config)
	if refreshErr != nil {
		return false, refreshErr
	}
	*p.config = updatedConfig
	if strings.TrimSpace(updatedConfig.AccessToken) == "" {
		return false, nil
	}
	// 登录成功后刷新 serviceConfig，确保后续请求能拿到服务端配置
	if sc, scErr := p.Provider.GetServiceConfig(ctx); scErr == nil && sc != nil {
		p.serviceConfig = sc
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

func (p *callbackAuthRefreshProvider) GetFlow(ctx context.Context, flowID string) (*provider.FlowSummary, error) {
	flow, err := p.Provider.GetFlow(ctx, flowID)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return flow, err
	}
	return p.Provider.GetFlow(ctx, flowID)
}

func (p *callbackAuthRefreshProvider) ListKnowledgeBases(ctx context.Context, appid string, filter string, offset, limit int) (*provider.KnowledgeBaseList, error) {
	knowledgeBases, err := p.Provider.ListKnowledgeBases(ctx, appid, filter, offset, limit)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return knowledgeBases, err
	}
	return p.Provider.ListKnowledgeBases(ctx, appid, filter, offset, limit)
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

func (p *callbackAuthRefreshProvider) RunFlow(ctx context.Context, request provider.RunFlowRequest) (*provider.RunFlowResult, error) {
	result, err := p.Provider.RunFlow(ctx, request)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return result, err
	}
	return p.Provider.RunFlow(ctx, request)
}

func (p *callbackAuthRefreshProvider) RunImageUnderstanding(ctx context.Context, request provider.ImageUnderstandingRequest) (*provider.ImageUnderstandingResult, error) {
	runner, ok := p.Provider.(provider.ImageUnderstandingRunner)
	if !ok {
		return nil, fmt.Errorf("当前 provider 不支持 image_understanding")
	}
	result, err := runner.RunImageUnderstanding(ctx, request)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return result, err
	}
	return runner.RunImageUnderstanding(ctx, request)
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

func (p *callbackAuthRefreshProvider) ListMemoryConsolidations(ctx context.Context, query provider.MemoryConsolidationQuery) (*provider.MemoryConsolidationPage, error) {
	page, err := p.Provider.ListMemoryConsolidations(ctx, query)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return page, err
	}
	return p.Provider.ListMemoryConsolidations(ctx, query)
}

func (p *callbackAuthRefreshProvider) TriggerMemoryConsolidation(ctx context.Context, request provider.MemoryConsolidationTriggerRequest) (*provider.MemoryConsolidationTriggerResult, error) {
	result, err := p.Provider.TriggerMemoryConsolidation(ctx, request)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return result, err
	}
	return p.Provider.TriggerMemoryConsolidation(ctx, request)
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

func (p *callbackAuthRefreshProvider) CreateClientCollaboration(ctx context.Context, request provider.ClientCollaborationCreateRequest) (*provider.ClientCollaborationTask, error) {
	task, err := p.Provider.CreateClientCollaboration(ctx, request)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return task, err
	}
	return p.Provider.CreateClientCollaboration(ctx, request)
}

func (p *callbackAuthRefreshProvider) GetClientCollaboration(ctx context.Context, taskID string) (*provider.ClientCollaborationTask, error) {
	task, err := p.Provider.GetClientCollaboration(ctx, taskID)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return task, err
	}
	return p.Provider.GetClientCollaboration(ctx, taskID)
}

func (p *callbackAuthRefreshProvider) UpdateClientCollaboration(ctx context.Context, taskID string, request provider.ClientCollaborationUpdateRequest) (*provider.ClientCollaborationTask, error) {
	task, err := p.Provider.UpdateClientCollaboration(ctx, taskID, request)
	retry, refreshErr := p.shouldRetryAfterAuthRefresh(ctx, err)
	if refreshErr != nil {
		return nil, refreshErr
	}
	if !retry {
		return task, err
	}
	return p.Provider.UpdateClientCollaboration(ctx, taskID, request)
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

// extractServiceConfig 从 provider 中提取 callbackAuthRefreshProvider 上缓存的 serviceConfig。
// 当登录成功后，serviceConfig 会被自动刷新，因此应优先使用此函数获取最新值。
func extractServiceConfig(p provider.Provider) *provider.ServiceConfig {
	if cp, ok := p.(*callbackAuthRefreshProvider); ok {
		return cp.serviceConfig
	}
	return nil
}

// extractTaiyiflowClient 从 provider 中提取底层 *providerclient.Client。
// 支持直接传入 *providerclient.Client 或被 callbackAuthRefreshProvider 包装后的 provider。
// 修复：providerWithCallbackAuthRefresh 包装后，直接类型断言 providerClient.(*providerclient.Client) 永远失败。
func extractTaiyiflowClient(p provider.Provider) (*providerclient.Client, bool) {
	if client, ok := p.(*providerclient.Client); ok {
		return client, true
	}
	if cp, ok := p.(*callbackAuthRefreshProvider); ok && cp.client != nil {
		return cp.client, true
	}
	return nil, false
}

// extractUserIdentityRefresh 从 provider 中提取 MCP 鉴权刷新闭包。
// 仅当 provider 被 callbackAuthRefreshProvider 包装（即具备持久令牌自动刷新能力）时返回非 nil，
// 注入到 MCP Registry，使 MCP 服务返回 401/403 时能触发一次刷新登录并用新令牌重试。
// 未包装（无 PersistentToken）时返回 nil，MCP 调用退化为不重试，与现有行为一致。
func extractUserIdentityRefresh(p provider.Provider) func(ctx context.Context) bool {
	if cp, ok := p.(*callbackAuthRefreshProvider); ok {
		return cp.RefreshAuth
	}
	return nil
}

func modeAllowsBuiltIn(mode string) bool {
	return normalizeRequestMode(mode) == provider.RequestTypeAgent
}

func modeAllowsClientTool(mode string, toolName string) bool {
	if normalizeRequestMode(mode) == provider.RequestTypeAgent {
		return true
	}
	switch strings.TrimSpace(toolName) {
	case "ask_user", "lsp_query", "glob_files", "task_output", "task_list", "search_files", "read_file", "skill_fetch", "switch_to_agent", "taiyiflow-in-chrome__tabs_context", "taiyiflow-in-chrome__read_text", "taiyiflow-in-chrome__read_console", "taiyiflow-computer__displays_context", "taiyiflow-computer__windows_context", "taiyiflow-computer__screenshot":
		return true
	default:
		return false
	}
}

func submitClientCallFailure(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
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
	// 使用独立上下文提交失败回调，避免父上下文已取消时出现 "context deadline exceeded"。
	// 超时时间使用系统配置中的 HTTP 请求超时设定。
	callbackCtx, cancel := clientruntime.DetachedCallbackContext(ctx, time.Duration(config.HTTPTimeoutSeconds)*time.Second)
	defer cancel()
	return providerClient.SubmitClientCallResult(callbackCtx, result)
}
