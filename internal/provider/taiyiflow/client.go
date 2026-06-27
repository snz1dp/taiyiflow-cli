package taiyiflow

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const SessionHeader = "X-TaiyiFlow-Session-id"

const defaultStreamIdleTimeout = time.Duration(internalconfig.DefaultStreamIdleTimeoutSeconds) * time.Second
const defaultCompressionPollDelay = 120 * time.Millisecond
const defaultCompressionPollAttempts = 4

type streamIdleTimeoutError struct {
	idleTimeout time.Duration
}

func (e streamIdleTimeoutError) Error() string {
	return fmt.Sprintf("backend idle timeout after %s without SSE activity", e.idleTimeout)
}

type idleTimeoutReadCloser struct {
	reader      io.ReadCloser
	idleTimeout time.Duration
	timer       *time.Timer
	mu          sync.Mutex
	lastActive  time.Time
	timedOut    bool
	closed      bool
	closeOnce   sync.Once
	closeErr    error
}

func newIdleTimeoutReadCloser(reader io.ReadCloser, idleTimeout time.Duration) *idleTimeoutReadCloser {
	wrapped := &idleTimeoutReadCloser{reader: reader, idleTimeout: idleTimeout, lastActive: time.Now()}
	if idleTimeout > 0 {
		wrapped.timer = time.AfterFunc(idleTimeout, wrapped.handleIdleTimeout)
	}
	return wrapped
}

func (r *idleTimeoutReadCloser) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	if count > 0 && r.timer != nil {
		r.markActive()
	}
	if err != nil && r.didTimeOut() {
		return count, streamIdleTimeoutError{idleTimeout: r.idleTimeout}
	}
	return count, err
}

func (r *idleTimeoutReadCloser) Close() error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	if r.timer != nil {
		r.timer.Stop()
	}
	return r.closeUnderlying()
}

func (r *idleTimeoutReadCloser) markActive() {
	r.mu.Lock()
	r.lastActive = time.Now()
	closed := r.closed
	r.mu.Unlock()
	if !closed {
		r.timer.Reset(r.idleTimeout)
	}
}

func (r *idleTimeoutReadCloser) handleIdleTimeout() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	elapsed := time.Since(r.lastActive)
	if elapsed < r.idleTimeout {
		remaining := r.idleTimeout - elapsed
		r.mu.Unlock()
		r.timer.Reset(remaining)
		return
	}
	r.timedOut = true
	r.closed = true
	r.mu.Unlock()
	_ = r.closeUnderlying()
}

func (r *idleTimeoutReadCloser) didTimeOut() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.timedOut
}

func (r *idleTimeoutReadCloser) closeUnderlying() error {
	r.closeOnce.Do(func() {
		r.closeErr = r.reader.Close()
	})
	return r.closeErr
}

type Client struct {
	baseURL                 string
	apiPrefix               string
	apiKey                  string
	httpClient              *http.Client
	streamHTTPClient        *http.Client
	streamIdleTimeout       time.Duration
	compressionPollDelay    time.Duration
	compressionPollAttempts int
}

type metadataResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	WelcomeMessage string `json:"welcome_message"`
	MaxInputTokens int    `json:"max_input_tokens"`
	Enabled        bool   `json:"enabled"`
	IsChat         bool   `json:"is_chat"`

	// 子标题
	SubHead   string   `json:"sub_head"`
	Questions []string `json:"questions"`

	// knowledge_search
	// image_understanding
	// audio_understanding
	// video_understanding
	// document_understanding
	// support_knowledge
	// support_mcp_tools
	// support_human_ai_dialogue
	Features []string `json:"features"`
}

type serviceFeatureFlagsResponse struct {
	TextToSpeech    bool `json:"text_to_speech"`
	SpeechToText    bool `json:"speech_to_text"`
	HumanAIDialogue bool `json:"human_ai_dialogue"`
}

type serviceConfigResponse struct {
	FeatureFlags           serviceFeatureFlagsResponse `json:"feature_flags"`
	MaxFileSizeUpload      int                         `json:"max_file_size_upload"`
	DocExts                []string                    `json:"doc_exts"`
	MilvusEnabled          bool                        `json:"milvus_enabled"`
	DefaultFlowID          string                      `json:"default_flow_id"`
	ImageFlowID            string                      `json:"image_flow_id"`
	DocUnstructuredFlowID  string                      `json:"doc_unstructured_flow_id"`
	AgentAutoLoopEnabled   bool                        `json:"agent_auto_loop_enabled"`
	AgentAutoLoopMaxRounds int                         `json:"agent_auto_loop_max_rounds"`
	LoopDecideFlowID       string                      `json:"loop_decide_flow_id"`
}

type versionInfoResponse struct {
	Package     string `json:"package"`
	Version     string `json:"version"`
	MainVersion string `json:"main_version"`
}

type currentUserInfoResponse struct {
	Name      string `json:"name"`
	UserID    string `json:"user_id"`
	UserName  string `json:"user_name"`
	UserScope string `json:"user_scope"`
}

type flowItemResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
	OwnerAppID  string `json:"owner_appid"`
}

type flowListResponse struct {
	Total  int                `json:"total"`
	Offset int                `json:"offset"`
	Data   []flowItemResponse `json:"data"`
}

type knowledgeBaseResponse struct {
	ID          string `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

type knowledgeBasePageResponse struct {
	Total  int                     `json:"total"`
	Offset int                     `json:"offset"`
	Data   []knowledgeBaseResponse `json:"data"`
}

type sessionItemResponse struct {
	ID          string `json:"id"`
	SessionName string `json:"session_name"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
	ClientType  string `json:"client_type"`
}

type sessionListResponse struct {
	Total  int                   `json:"total"`
	Offset int                   `json:"offset"`
	Data   []sessionItemResponse `json:"data"`
}

type sessionUpdateRequest struct {
	SessionName string `json:"session_name"`
}

type sessionMessageResponse struct {
	ID               string                 `json:"id"`
	SessionID        string                 `json:"session_id"`
	RunID            string                 `json:"run_id"`
	Sender           string                 `json:"sender"`
	SenderName       string                 `json:"sender_name"`
	Text             string                 `json:"text"`
	ReasonText       string                 `json:"reason_text"`
	SearchResults    []searchResultResponse `json:"search_results"`
	SearchKeyword    string                 `json:"search_keyword"`
	ReasoningEnabled bool                   `json:"reasoning_enabled"`
	SessionName      string                 `json:"session_name"`
	Timestamp        string                 `json:"timestamp"`
	Error            bool                   `json:"error"`
	TokenUsage       map[string]any         `json:"token_usage"`
}

type sessionMessagePageResponse struct {
	Total  int                      `json:"total"`
	Offset int                      `json:"offset"`
	Data   []sessionMessageResponse `json:"data"`
}

type searchResultResponse struct {
	DocID       string `json:"docid"`
	FileType    string `json:"filetype"`
	FilePath    string `json:"file_path"`
	Filename    string `json:"filename"`
	Content     string `json:"content"`
	PageNumbers []int  `json:"page_numbers"`
	Text        string `json:"text"`
	Collection  string `json:"collection"`
	Partition   string `json:"partition"`
	Title       string `json:"title"`
	Link        string `json:"link"`
	Site        string `json:"site"`
	Source      string `json:"source"`
	Provider    string `json:"provider"`
}

type uploadFileResponse struct {
	FlowID      string `json:"flow_id"`
	FlowIDAlt   string `json:"flowId"`
	FilePath    string `json:"file_path"`
	FilePathAlt string `json:"filePath"`
}

type sessionIDResponse struct {
	SessionID string `json:"session_id"`
}

type compressionRecordResponse struct {
	TriggerReason         string  `json:"trigger_reason"`
	CompressionStrategy   string  `json:"compression_strategy"`
	TargetType            string  `json:"target_type"`
	BeforeTokens          int     `json:"before_tokens"`
	TargetTokens          int     `json:"target_tokens"`
	AfterTokens           int     `json:"after_tokens"`
	RemainingTokens       int     `json:"remaining_tokens"`
	CompressionRatio      float64 `json:"compression_ratio"`
	ReservedHistoryTokens int     `json:"reserved_history_tokens"`
	CreatedAt             string  `json:"created_at"`
}

type sessionContextWindowResponse struct {
	MaxInputTokens              int     `json:"max_input_tokens"`
	ContextUsageTokens          int     `json:"context_usage_tokens"`
	DirectMessageTokens         int     `json:"direct_message_tokens"`
	CompressedHistoryTokens     int     `json:"compressed_history_tokens"`
	CurrentRoundInputTokens     int     `json:"current_round_input_tokens"`
	NextRoundAvailableTokens    int     `json:"next_round_available_tokens"`
	CurrentRoundAvailableTokens int     `json:"current_round_available_tokens"`
	HistoryMessageCount         int     `json:"history_message_count"`
	PreparedMessageCount        int     `json:"prepared_message_count"`
	HighWatermarkRatio          float64 `json:"high_watermark_ratio"`
	CompressionTargetRatio      float64 `json:"compression_target_ratio"`
	HighWatermarkTokens         int     `json:"high_watermark_tokens"`
	CompressionTargetTokens     int     `json:"compression_target_tokens"`
}

type sessionContextPreviewResponse struct {
	FlowID            string                        `json:"flow_id"`
	SessionID         string                        `json:"session_id"`
	ContextWindow     *sessionContextWindowResponse `json:"context_window"`
	LatestCompression *compressionRecordResponse    `json:"latest_compression"`
	MemoryTiers       []string                      `json:"memory_tiers"`
	SourceUpdatedAt   string                        `json:"source_updated_at"`
	Source            string                        `json:"source"`
}

type memoryConsolidationTaskResponse struct {
	ID               string `json:"id"`
	ScopeKey         string `json:"scope_key"`
	FlowID           string `json:"flow_id"`
	OwnerAppID       string `json:"owner_appid"`
	OwnerUserID      string `json:"owner_user_id"`
	WindowStart      string `json:"window_start"`
	WindowEnd        string `json:"window_end"`
	TriggerReason    string `json:"trigger_reason"`
	Status           string `json:"status"`
	AttemptCount     int    `json:"attempt_count"`
	LastError        string `json:"last_error"`
	LastSourceCount  int    `json:"last_source_count"`
	LastSessionCount int    `json:"last_session_count"`
	LastTaskID       string `json:"last_task_id"`
	ResultSegmentKey string `json:"result_segment_key"`
	LastRunAt        string `json:"last_run_at"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

type memoryConsolidationTaskPageResponse struct {
	Total  int                               `json:"total"`
	Offset int                               `json:"offset"`
	Data   []memoryConsolidationTaskResponse `json:"data"`
}

type memoryConsolidationTriggerRequestBody struct {
	FlowID         string `json:"flow_id"`
	OwnerAppID     string `json:"owner_appid,omitempty"`
	MinHours       int    `json:"min_hours"`
	MinSessions    int    `json:"min_sessions"`
	ExecuteEnabled bool   `json:"execute_enabled"`
}

type memoryConsolidationTriggerResponse struct {
	TaskID        string `json:"task_id"`
	BackendTaskID string `json:"backend_task_id"`
	Status        string `json:"status"`
	Scheduled     bool   `json:"scheduled"`
	Reason        string `json:"reason"`
}

type skillResolveRequestBody struct {
	RequestType            string   `json:"request_type,omitempty"`
	RuntimeStrategy        string   `json:"runtime_strategy,omitempty"`
	CommandExecutionTarget string   `json:"command_execution_target,omitempty"`
	RequestedSkillNames    []string `json:"requested_skill_names,omitempty"`
	AvailableTools         []string `json:"available_tools,omitempty"`
	ClientSkillPackages    any      `json:"client_skill_packages,omitempty"`
}

type resolvedSkillResponse struct {
	Name        string `json:"name"`
	Content     string `json:"content"`
	Source      string `json:"source"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

type skillResolvePreviewResponse struct {
	Skills            []resolvedSkillResponse `json:"skills"`
	MissingSkillNames []string                `json:"missing_skill_names"`
	AllowedTools      []string                `json:"allowed_tools"`
}

type streamRequestBody struct {
	InputValue                string         `json:"input_value,omitempty"`
	InputType                 string         `json:"input_type,omitempty"`
	Tweaks                    map[string]any `json:"tweaks,omitempty"`
	Files                     []string       `json:"files,omitempty"`
	InternetSearch            bool           `json:"internet_search,omitempty"`
	KnowledgeScopes           []string       `json:"knowledge_scopes,omitempty"`
	KnowledgeDocs             []string       `json:"knowledge_docs,omitempty"`
	OutputType                string         `json:"output_type,omitempty"`
	OutputComponent           string         `json:"output_component,omitempty"`
	SessionID                 string         `json:"session_id,omitempty"`
	RunID                     string         `json:"run_id,omitempty"`
	RequestType               string         `json:"request_type,omitempty"`
	ClientType                string         `json:"client_type,omitempty"`
	ClientID                  string         `json:"client_id,omitempty"`
	DeviceID                  string         `json:"device_id,omitempty"`
	ClientContextDir          string         `json:"client_context_dir,omitempty"`
	CommandExecutionTarget    string         `json:"command_execution_target,omitempty"`
	MaxIterations             int            `json:"max_iterations,omitempty"`
	AgentContinueWindows      int            `json:"agent_continue_windows,omitempty"`
	AgentAutoLoopEnabled      *bool          `json:"agent_auto_loop_enabled,omitempty"`
	AgentAutoLoopMaxRounds    int            `json:"agent_auto_loop_max_rounds"`
	CrossSessionMemoryEnabled *bool          `json:"cross_session_memory_enabled,omitempty"`
	MessageID                 string         `json:"message_id,omitempty"`
	RequestedSkillNames       []string       `json:"requested_skill_names,omitempty"`
	AgentInputs               map[string]any `json:"agent_inputs,omitempty"`
	Reasoning                 bool           `json:"reasoning,omitempty"`
	MasterFlowID              string         `json:"master_flow_id,omitempty"`
	AvailableTools            []any          `json:"available_tools,omitempty"`
	ClientMCPServers          map[string]any `json:"client_mcp_servers,omitempty"`
	ClientSkillPackages       any            `json:"client_skill_packages,omitempty"`
	ClientInstructions        any            `json:"client_instructions,omitempty"`
	ParentID                  string         `json:"parent_id,omitempty"`
}

type clientCallResultBody struct {
	FlowID       string `json:"flow_id"`
	SessionID    string `json:"session_id,omitempty"`
	MessageID    string `json:"message_id,omitempty"`
	RunID        string `json:"run_id"`
	CallID       string `json:"call_id"`
	Success      bool   `json:"success"`
	Result       any    `json:"result,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type clientCollaborationCreateRequestBody struct {
	TaskKind        string         `json:"task_kind,omitempty"`
	TargetClientID  string         `json:"target_client_id,omitempty"`
	TargetDeviceID  string         `json:"target_device_id,omitempty"`
	DeliveryPolicy  string         `json:"delivery_policy,omitempty"`
	CommandPayload  map[string]any `json:"command_payload,omitempty"`
	SourceSessionID string         `json:"source_session_id,omitempty"`
	SourceRunID     string         `json:"source_run_id,omitempty"`
	SourceMessageID string         `json:"source_message_id,omitempty"`
	ChildSessionID  string         `json:"child_session_id,omitempty"`
	ChildRunID      string         `json:"child_run_id,omitempty"`
}

type clientCollaborationUpdateRequestBody struct {
	Status        string         `json:"status,omitempty"`
	StatusPayload map[string]any `json:"status_payload,omitempty"`
	ResultPayload map[string]any `json:"result_payload,omitempty"`
	ErrorMessage  string         `json:"error_message,omitempty"`
}

type clientCollaborationTaskResponse struct {
	ID              string         `json:"id"`
	OwnerUserID     string         `json:"owner_user_id"`
	TargetClientID  string         `json:"target_client_id"`
	TargetDeviceID  string         `json:"target_device_id"`
	SourceSessionID string         `json:"source_session_id"`
	SourceRunID     string         `json:"source_run_id"`
	SourceMessageID string         `json:"source_message_id"`
	ChildSessionID  string         `json:"child_session_id"`
	ChildRunID      string         `json:"child_run_id"`
	TaskKind        string         `json:"task_kind"`
	DeliveryPolicy  string         `json:"delivery_policy"`
	Status          string         `json:"status"`
	CommandPayload  map[string]any `json:"command_payload"`
	StatusPayload   map[string]any `json:"status_payload"`
	ResultPayload   map[string]any `json:"result_payload"`
	ErrorMessage    string         `json:"error_message"`
	DispatchedAt    string         `json:"dispatched_at"`
	FinishedAt      string         `json:"finished_at"`
	ExpiredAt       string         `json:"expired_at"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
}

func NewClient(baseURL, apiKey string) *Client {
	streamTransport := http.DefaultTransport.(*http.Transport).Clone()
	streamTransport.DialContext = (&net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 60 * time.Second,
	}).DialContext
	streamTransport.MaxIdleConns = 100
	streamTransport.MaxIdleConnsPerHost = 20
	streamTransport.IdleConnTimeout = 600 * time.Second
	streamTransport.TLSHandshakeTimeout = 30 * time.Second
	streamTransport.ExpectContinueTimeout = time.Second

	return &Client{
		baseURL:                 strings.TrimRight(baseURL, "/"),
		apiPrefix:               internalconfig.DefaultBackendAPIPrefix,
		apiKey:                  apiKey,
		httpClient:              &http.Client{Timeout: time.Duration(internalconfig.DefaultHTTPTimeoutSeconds) * time.Second},
		streamHTTPClient:        &http.Client{Transport: streamTransport},
		streamIdleTimeout:       defaultStreamIdleTimeout,
		compressionPollDelay:    defaultCompressionPollDelay,
		compressionPollAttempts: defaultCompressionPollAttempts,
	}
}

func (c *Client) SetAPIPrefix(prefix string) {
	c.apiPrefix = internalconfig.NormalizeBackendAPIPrefix(prefix)
}

func (c *Client) SetAPIKey(apiKey string) {
	c.apiKey = strings.TrimSpace(apiKey)
}

func (c *Client) SetHTTPTimeoutSeconds(seconds int) {
	c.httpClient.Timeout = time.Duration(internalconfig.NormalizeHTTPTimeoutSeconds(seconds)) * time.Second
}

func (c *Client) apiURL(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return c.baseURL + c.apiPrefix
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	return c.baseURL + c.apiPrefix + trimmed
}

func (c *Client) SetStreamIdleTimeoutSeconds(seconds int) {
	switch {
	case seconds < 0:
		c.streamIdleTimeout = defaultStreamIdleTimeout
	case seconds == 0:
		c.streamIdleTimeout = 0
	default:
		c.streamIdleTimeout = time.Duration(seconds) * time.Second
	}
}

func (c *Client) Name() string {
	return "taiyiflow"
}

func (c *Client) ListFlows(ctx context.Context, filter string, agentType string, offset, limit int) (*provider.FlowList, error) {
	// 这里显式走用户侧智能体接口，不走 /flows 这类管理接口。
	endpoint, err := url.Parse(c.apiURL("/agents/flows"))
	if err != nil {
		return nil, fmt.Errorf("构造 flows 请求地址失败: %w", err)
	}

	query := endpoint.Query()
	if strings.TrimSpace(filter) != "" {
		query.Set("filter", strings.TrimSpace(filter))
	}
	if strings.TrimSpace(agentType) != "" {
		query.Set("type", strings.TrimSpace(agentType))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取用户侧智能体列表失败", resp)
	}

	var payload flowListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析用户侧智能体列表响应失败: %w", err)
	}

	items := make([]provider.FlowSummary, 0, len(payload.Data))
	for _, item := range payload.Data {
		items = append(items, provider.FlowSummary{
			ID:          item.ID,
			Name:        item.Name,
			Description: item.Description,
			Type:        item.Type,
			OwnerAppID:  strings.TrimSpace(item.OwnerAppID),
		})
	}

	return &provider.FlowList{
		Total:  payload.Total,
		Offset: payload.Offset,
		Items:  items,
	}, nil
}

func (c *Client) GetFlow(ctx context.Context, flowID string) (*provider.FlowSummary, error) {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return nil, fmt.Errorf("获取智能体信息失败: 智能体不能为空")
	}

	endpoint := c.apiURL(fmt.Sprintf("/agents/flows/%s", url.PathEscape(flowID)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取智能体信息失败", resp)
	}

	var payload flowItemResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析智能体信息响应失败: %w", err)
	}

	return &provider.FlowSummary{
		ID:          strings.TrimSpace(payload.ID),
		Name:        strings.TrimSpace(payload.Name),
		Description: strings.TrimSpace(payload.Description),
		Type:        strings.TrimSpace(payload.Type),
		OwnerAppID:  strings.TrimSpace(payload.OwnerAppID),
	}, nil
}

func (c *Client) ListSessions(ctx context.Context, flowID string, offset, limit int) (*provider.SessionList, error) {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return nil, fmt.Errorf("获取服务端会话列表失败: 智能体不能为空")
	}

	endpoint, err := url.Parse(c.apiURL(fmt.Sprintf("/agents/flows/%s/sessions", url.PathEscape(flowID))))
	if err != nil {
		return nil, fmt.Errorf("构造 sessions 请求地址失败: %w", err)
	}

	query := endpoint.Query()
	query.Set("orderby", "updated_at desc")
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取服务端会话列表失败", resp)
	}

	var payload sessionListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析服务端会话列表响应失败: %w", err)
	}

	items := make([]provider.SessionSummary, 0, len(payload.Data))
	for _, item := range payload.Data {
		createdAt, err := parseTaiyiTime(item.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("解析服务端会话创建时间失败: %w", err)
		}
		updatedAt, err := parseTaiyiTime(item.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("解析服务端会话更新时间失败: %w", err)
		}
		items = append(items, provider.SessionSummary{
			ID:         item.ID,
			Name:       item.SessionName,
			CreatedAt:  createdAt,
			UpdatedAt:  updatedAt,
			ClientType: item.ClientType,
		})
	}

	return &provider.SessionList{
		Total:  payload.Total,
		Offset: payload.Offset,
		Items:  items,
	}, nil
}

func (c *Client) UpdateSessionTitle(ctx context.Context, flowID string, sessionID string, sessionTitle string) (string, error) {
	trimmedFlowID := strings.TrimSpace(flowID)
	trimmedSessionID := strings.TrimSpace(sessionID)
	trimmedSessionTitle := strings.TrimSpace(sessionTitle)
	if trimmedFlowID == "" {
		return "", fmt.Errorf("更新会话标题失败: 智能体不能为空")
	}
	if trimmedSessionID == "" {
		return "", fmt.Errorf("更新会话标题失败: 会话不能为空")
	}

	content, err := json.Marshal(sessionUpdateRequest{SessionName: trimmedSessionTitle})
	if err != nil {
		return "", fmt.Errorf("序列化更新会话标题请求失败: %w", err)
	}

	endpoint := c.apiURL(fmt.Sprintf(
		"/agents/flows/%s/sessions/%s",
		url.PathEscape(trimmedFlowID),
		url.PathEscape(trimmedSessionID),
	))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("创建更新会话标题请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	c.applyAuth(request)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("更新会话标题失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return "", readHTTPError("更新会话标题失败", response)
	}

	var payload sessionItemResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("解析更新会话标题响应失败: %w", err)
	}
	if strings.TrimSpace(payload.SessionName) != "" {
		return strings.TrimSpace(payload.SessionName), nil
	}
	return trimmedSessionTitle, nil
}

func (c *Client) ListKnowledgeBases(ctx context.Context, appid string, filter string, offset, limit int) (*provider.KnowledgeBaseList, error) {
	endpoint, err := url.Parse(c.apiURL("/agents/knowledge/bases"))
	if err != nil {
		return nil, fmt.Errorf("构造 knowledge bases 请求地址失败: %w", err)
	}

	query := endpoint.Query()
	if strings.TrimSpace(appid) != "" {
		query.Set("appid", strings.TrimSpace(appid))
	}
	if strings.TrimSpace(filter) != "" {
		query.Set("filter", strings.TrimSpace(filter))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取知识库列表失败", resp)
	}

	var payload knowledgeBasePageResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析知识库列表响应失败: %w", err)
	}

	items := make([]provider.KnowledgeBaseSummary, 0, len(payload.Data))
	for _, item := range payload.Data {
		items = append(items, provider.KnowledgeBaseSummary{
			ID:          strings.TrimSpace(item.ID),
			Code:        strings.TrimSpace(item.Code),
			Name:        firstNonEmpty(strings.TrimSpace(item.DisplayName), firstNonEmpty(strings.TrimSpace(item.Name), strings.TrimSpace(item.Code))),
			Type:        strings.TrimSpace(item.Type),
			Description: strings.TrimSpace(item.Description),
		})
	}

	return &provider.KnowledgeBaseList{
		Total:  payload.Total,
		Offset: payload.Offset,
		Items:  items,
	}, nil
}

func (c *Client) CreateSessionID(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL("/session_id"), nil)
	if err != nil {
		return "", fmt.Errorf("创建会话 ID 请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("创建会话 ID 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return "", readHTTPError("创建会话 ID 失败", resp)
	}

	var payload sessionIDResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("解析会话 ID 响应失败: %w", err)
	}
	trimmed := strings.TrimSpace(payload.SessionID)
	if trimmed == "" {
		return "", fmt.Errorf("解析会话 ID 响应失败: session_id 为空")
	}
	return trimmed, nil
}

func (c *Client) UploadFile(ctx context.Context, flowID string, sessionID string, localPath string, remotePath string) (*provider.UploadResult, error) {
	trimmedFlowID := strings.TrimSpace(flowID)
	trimmedSessionID := strings.TrimSpace(sessionID)
	trimmedLocalPath := strings.TrimSpace(localPath)
	trimmedRemotePath := strings.TrimSpace(remotePath)
	if trimmedFlowID == "" {
		return nil, fmt.Errorf("上传文件失败: 智能体不能为空")
	}
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("上传文件失败: 当前会话 ID 不能为空")
	}
	if trimmedLocalPath == "" {
		return nil, fmt.Errorf("上传文件失败: 本地文件路径不能为空")
	}

	file, err := os.Open(trimmedLocalPath)
	if err != nil {
		return nil, fmt.Errorf("打开上传文件失败: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("session_id", trimmedSessionID); err != nil {
		return nil, fmt.Errorf("构造上传表单失败: %w", err)
	}
	if trimmedRemotePath != "" {
		if err := writer.WriteField("file_path", trimmedRemotePath); err != nil {
			return nil, fmt.Errorf("构造上传表单失败: %w", err)
		}
	}
	uploadName := filepath.Base(trimmedLocalPath)
	if trimmedRemotePath != "" {
		uploadName = filepath.Base(trimmedRemotePath)
	}
	part, err := writer.CreateFormFile("file", uploadName)
	if err != nil {
		return nil, fmt.Errorf("构造上传文件字段失败: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("写入上传文件内容失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("完成上传表单失败: %w", err)
	}

	endpointURL, err := url.Parse(c.apiURL(fmt.Sprintf("/agents/upload/%s", url.PathEscape(trimmedFlowID))))
	if err != nil {
		return nil, fmt.Errorf("构造上传请求地址失败: %w", err)
	}
	query := endpointURL.Query()
	query.Set("session_id", trimmedSessionID)
	endpointURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL.String(), &body)
	if err != nil {
		return nil, fmt.Errorf("创建上传请求失败: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("上传文件失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("上传文件失败", resp)
	}

	var payload uploadFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析上传响应失败: %w", err)
	}

	return &provider.UploadResult{
		FlowID:   firstNonEmpty(payload.FlowID, payload.FlowIDAlt),
		FilePath: firstNonEmpty(payload.FilePath, payload.FilePathAlt),
	}, nil
}

func (c *Client) runSimpleFlowRequest(
	ctx context.Context,
	flowID string,
	sessionID string,
	body streamRequestBody,
	serviceName string,
) ([]byte, string, string, error) {
	trimmedServiceName := strings.TrimSpace(serviceName)
	if trimmedServiceName == "" {
		trimmedServiceName = "服务"
	}
	trimmedFlowID := strings.TrimSpace(flowID)
	if trimmedFlowID == "" {
		return nil, "", "", fmt.Errorf("调用%s服务失败: %s流程不能为空", trimmedServiceName, trimmedServiceName)
	}
	trimmedSessionID := strings.TrimSpace(sessionID)

	content, err := json.Marshal(body)
	if err != nil {
		return nil, "", "", fmt.Errorf("序列化%s请求失败: %w", trimmedServiceName, err)
	}

	endpointURL, err := url.Parse(c.apiURL(fmt.Sprintf("/run/%s", url.PathEscape(trimmedFlowID))))
	if err != nil {
		return nil, "", "", fmt.Errorf("构造%s请求地址失败: %w", trimmedServiceName, err)
	}
	if trimmedSessionID != "" {
		query := endpointURL.Query()
		query.Set("session_id", trimmedSessionID)
		endpointURL.RawQuery = query.Encode()
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL.String(), bytes.NewReader(content))
	if err != nil {
		return nil, "", "", fmt.Errorf("创建%s请求失败: %w", trimmedServiceName, err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	c.applyAuth(httpRequest)

	resp, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, "", "", fmt.Errorf("调用%s服务失败: %w", trimmedServiceName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, "", "", readHTTPError(fmt.Sprintf("调用%s服务失败", trimmedServiceName), resp)
	}

	output, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", "", fmt.Errorf("读取%s响应失败: %w", trimmedServiceName, err)
	}

	mediaType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if separatorIndex := strings.Index(mediaType, ";"); separatorIndex >= 0 {
		mediaType = strings.TrimSpace(mediaType[:separatorIndex])
	}
	if mediaType == "" {
		mediaType = "text/plain"
	}

	return output, mediaType, firstNonEmpty(strings.TrimSpace(resp.Header.Get(SessionHeader)), trimmedSessionID), nil
}

func (c *Client) RunFlow(ctx context.Context, request provider.RunFlowRequest) (*provider.RunFlowResult, error) {
	body := streamRequestBody{
		InputValue: request.Input,
		InputType:  firstNonEmpty(strings.TrimSpace(request.InputType), "any"),
		Tweaks:     cloneTweaks(request.Tweaks),
		SessionID:  strings.TrimSpace(request.SessionID),
	}

	output, mediaType, sessionID, err := c.runSimpleFlowRequest(ctx, request.FlowID, request.SessionID, body, "流程执行")
	if err != nil {
		return nil, err
	}

	return &provider.RunFlowResult{
		Output:    string(output),
		MediaType: mediaType,
		SessionID: sessionID,
	}, nil
}

func (c *Client) RunDocumentUnstructured(ctx context.Context, request provider.DocumentUnstructuredRequest) (*provider.DocumentUnstructuredResult, error) {
	body := streamRequestBody{
		InputValue: request.Input,
		InputType:  firstNonEmpty(strings.TrimSpace(request.InputType), "any"),
		Files:      append([]string(nil), request.InputFiles...),
		OutputType: firstNonEmpty(strings.TrimSpace(request.OutputType), "any"),
		SessionID:  strings.TrimSpace(request.SessionID),
	}

	output, mediaType, sessionID, err := c.runSimpleFlowRequest(ctx, request.FlowID, request.SessionID, body, "文档结构化")
	if err != nil {
		return nil, err
	}

	return &provider.DocumentUnstructuredResult{
		SessionID: sessionID,
		Output:    string(output),
		MediaType: mediaType,
	}, nil
}

func (c *Client) RunImageUnderstanding(ctx context.Context, request provider.ImageUnderstandingRequest) (*provider.ImageUnderstandingResult, error) {
	tweaks := mergeImageUnderstandingTweaks(request)
	body := streamRequestBody{
		InputValue: request.Input,
		InputType:  firstNonEmpty(strings.TrimSpace(request.InputType), "any"),
		Tweaks:     tweaks,
		Files:      append([]string(nil), request.InputFiles...),
		OutputType: firstNonEmpty(strings.TrimSpace(request.OutputType), "any"),
		SessionID:  strings.TrimSpace(request.SessionID),
	}

	output, mediaType, sessionID, err := c.runSimpleFlowRequest(ctx, request.FlowID, request.SessionID, body, "图像理解")
	if err != nil {
		return nil, err
	}
	textOutput := strings.TrimSpace(string(output))

	return &provider.ImageUnderstandingResult{
		SessionID: sessionID,
		Output:    string(output),
		MediaType: mediaType,
		Text:      textOutput,
	}, nil
}

func cloneTweaks(source map[string]any) map[string]any {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func mergeImageUnderstandingTweaks(request provider.ImageUnderstandingRequest) map[string]any {
	return cloneTweaks(request.Tweaks)
}

func (c *Client) ListSessionMessages(ctx context.Context, flowID string, sessionID string, offset, limit int) (*provider.SessionMessageList, error) {
	flowID = strings.TrimSpace(flowID)
	sessionID = strings.TrimSpace(sessionID)
	if flowID == "" {
		return nil, fmt.Errorf("获取会话消息失败: 智能体不能为空")
	}
	if sessionID == "" {
		return &provider.SessionMessageList{}, nil
	}

	endpoint, err := url.Parse(c.apiURL(fmt.Sprintf("/agents/flows/%s/sessions/%s/messages", url.PathEscape(flowID), url.PathEscape(sessionID))))
	if err != nil {
		return nil, fmt.Errorf("构造 messages 请求地址失败: %w", err)
	}

	query := endpoint.Query()
	query.Set("orderby", "timestamp desc")
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取会话消息失败", resp)
	}

	var payload sessionMessagePageResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析会话消息响应失败: %w", err)
	}

	items := make([]provider.SessionMessage, 0, len(payload.Data))
	for _, item := range payload.Data {
		timestamp, err := parseTaiyiTime(item.Timestamp)
		if err != nil {
			return nil, fmt.Errorf("解析会话消息时间失败: %w", err)
		}
		items = append(items, provider.SessionMessage{
			ID:               item.ID,
			SessionID:        item.SessionID,
			RunID:            item.RunID,
			Sender:           item.Sender,
			SenderName:       item.SenderName,
			Text:             item.Text,
			ReasonText:       item.ReasonText,
			SearchResults:    mapSearchResults(item.SearchResults),
			SearchKeyword:    item.SearchKeyword,
			ReasoningEnabled: item.ReasoningEnabled,
			SessionName:      item.SessionName,
			Timestamp:        timestamp,
			Error:            item.Error,
		})
	}

	return &provider.SessionMessageList{
		Total:  payload.Total,
		Offset: payload.Offset,
		Items:  items,
	}, nil
}

func (c *Client) GetVersionInfo(ctx context.Context) (*provider.ServiceVersion, error) {
	endpoint := c.apiURL("/version")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取版本信息失败", resp)
	}

	var payload versionInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析版本信息响应失败: %w", err)
	}

	return &provider.ServiceVersion{
		Package:     payload.Package,
		Version:     payload.Version,
		MainVersion: payload.MainVersion,
	}, nil
}

func (c *Client) GetCurrentUserInfo(ctx context.Context) (*provider.CurrentUser, error) {
	endpoint := c.apiURL("/users/whoami/userinfo")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("请求当前用户信息失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求当前用户信息失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取当前用户信息失败", resp)
	}

	var payload currentUserInfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析当前用户信息响应失败: %w", err)
	}

	return &provider.CurrentUser{
		Name:      strings.TrimSpace(payload.Name),
		UserID:    strings.TrimSpace(payload.UserID),
		UserName:  strings.TrimSpace(payload.UserName),
		UserScope: strings.TrimSpace(payload.UserScope),
	}, nil
}

func (c *Client) GetServiceConfig(ctx context.Context) (*provider.ServiceConfig, error) {
	endpoint := c.apiURL("/config")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("请求服务端配置失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求服务端配置失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取服务端配置失败", resp)
	}

	var payload serviceConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析服务端配置失败: %w", err)
	}

	docExts := make([]string, 0, len(payload.DocExts))
	seenDocExts := map[string]struct{}{}
	for _, item := range payload.DocExts {
		trimmed := strings.Trim(strings.ToLower(strings.TrimSpace(item)), ".")
		if trimmed == "" {
			continue
		}
		if _, exists := seenDocExts[trimmed]; exists {
			continue
		}
		seenDocExts[trimmed] = struct{}{}
		docExts = append(docExts, trimmed)
	}

	return &provider.ServiceConfig{
		FeatureFlags: provider.ServiceFeatureFlags{
			TextToSpeech:    payload.FeatureFlags.TextToSpeech,
			SpeechToText:    payload.FeatureFlags.SpeechToText,
			HumanAIDialogue: payload.FeatureFlags.HumanAIDialogue,
		},
		MaxFileSizeUpload:      payload.MaxFileSizeUpload,
		DocExts:                docExts,
		MilvusEnabled:          payload.MilvusEnabled,
		DefaultFlowID:          strings.TrimSpace(payload.DefaultFlowID),
		ImageFlowID:            strings.TrimSpace(payload.ImageFlowID),
		DocUnstructuredFlowID:  strings.TrimSpace(payload.DocUnstructuredFlowID),
		AgentAutoLoopEnabled:   payload.AgentAutoLoopEnabled,
		AgentAutoLoopMaxRounds: payload.AgentAutoLoopMaxRounds,
		LoopDecideFlowID:       strings.TrimSpace(payload.LoopDecideFlowID),
	}, nil
}

func (c *Client) GetMetadata(ctx context.Context, flowID string) (*provider.Metadata, error) {
	endpoint := c.apiURL(fmt.Sprintf("/run/%s/metadata", url.PathEscape(flowID)))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取失败", resp)
	}

	var payload metadataResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析失败: %w", err)
	}

	return &provider.Metadata{
		ID:             payload.ID,
		Name:           payload.Name,
		Description:    payload.Description,
		WelcomeMessage: payload.WelcomeMessage,
		MaxInputTokens: payload.MaxInputTokens,
		Enabled:        payload.Enabled,
		IsChat:         payload.IsChat,
		SubHead:        payload.SubHead,
		Questions:      payload.Questions,
		Features:       payload.Features,
		Capabilities:   provider.NormalizeMetadataFeatures(payload.Features),
	}, nil
}

func (c *Client) ResolveSkillPreview(ctx context.Context, request provider.SkillResolveRequest) (*provider.SkillResolvePreview, error) {
	trimmedFlowID := strings.TrimSpace(request.FlowID)
	trimmedSessionID := strings.TrimSpace(request.SessionID)
	if trimmedFlowID == "" {
		return nil, fmt.Errorf("请求技能预览失败: 智能体不能为空")
	}
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("请求技能预览失败: 会话不能为空")
	}
	body := skillResolveRequestBody{
		RequestType:            provider.NormalizeRequestType(request.RequestType),
		RuntimeStrategy:        strings.TrimSpace(request.RuntimeStrategy),
		CommandExecutionTarget: strings.TrimSpace(request.CommandExecutionTarget),
		RequestedSkillNames:    request.RequestedSkillNames,
		AvailableTools:         request.AvailableTools,
		ClientSkillPackages:    request.ClientSkillPackages,
	}
	content, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化技能预览请求失败: %w", err)
	}
	endpoint := c.apiURL(fmt.Sprintf(
		"/agents/flows/%s/sessions/%s/skills/resolve",
		url.PathEscape(trimmedFlowID),
		url.PathEscape(trimmedSessionID),
	))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("创建技能预览请求失败: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	c.applyAuth(httpRequest)

	resp, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("请求技能预览失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("请求技能预览失败", resp)
	}

	var payload skillResolvePreviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析技能预览响应失败: %w", err)
	}

	resolvedSkills := make([]provider.ResolvedSkill, 0, len(payload.Skills))
	for _, item := range payload.Skills {
		resolvedSkills = append(resolvedSkills, provider.ResolvedSkill{
			Name:        strings.TrimSpace(item.Name),
			Content:     item.Content,
			Source:      strings.TrimSpace(item.Source),
			Description: strings.TrimSpace(item.Description),
			Version:     strings.TrimSpace(item.Version),
		})
	}

	return &provider.SkillResolvePreview{
		Skills:            resolvedSkills,
		MissingSkillNames: payload.MissingSkillNames,
		AllowedTools:      payload.AllowedTools,
	}, nil
}

func (c *Client) Stream(ctx context.Context, request provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	endpointURL, err := url.Parse(c.apiURL(fmt.Sprintf("/run/%s/stream", url.PathEscape(request.FlowID))))
	if err != nil {
		return nil, nil, fmt.Errorf("构造 stream 请求地址失败: %w", err)
	}
	trimmedSessionID := strings.TrimSpace(request.SessionID)
	if trimmedSessionID != "" {
		query := endpointURL.Query()
		query.Set("session_id", trimmedSessionID)
		endpointURL.RawQuery = query.Encode()
	}
	body := streamRequestBody{
		InputValue:                request.Input,
		InputType:                 firstNonEmpty(request.InputType, "any"),
		Tweaks:                    request.Tweaks,
		Files:                     request.InputFiles,
		InternetSearch:            request.InternetSearch,
		KnowledgeScopes:           request.KnowledgeScopes,
		KnowledgeDocs:             request.KnowledgeDocs,
		OutputType:                firstNonEmpty(request.OutputType, "any"),
		OutputComponent:           request.OutputComponent,
		SessionID:                 trimmedSessionID,
		RunID:                     request.RunID,
		RequestType:               provider.NormalizeRequestType(request.RequestType),
		ClientType:                firstNonEmpty(strings.TrimSpace(request.ClientType), provider.ClientTypeCLI),
		ClientID:                  strings.TrimSpace(request.ClientID),
		DeviceID:                  strings.TrimSpace(request.DeviceID),
		ClientContextDir:          strings.TrimSpace(request.ClientContextDir),
		CommandExecutionTarget:    firstNonEmpty(request.CommandExecutionTarget, "client"),
		MaxIterations:             request.MaxIterations,
		AgentContinueWindows:      request.AgentContinueWindows,
		AgentAutoLoopEnabled:      request.AgentAutoLoopEnabled,
		AgentAutoLoopMaxRounds:    request.AgentAutoLoopMaxRounds,
		CrossSessionMemoryEnabled: request.CrossSessionMemoryEnabled,
		MessageID:                 request.MessageID,
		RequestedSkillNames:       request.RequestedSkillNames,
		AgentInputs:               request.AgentInputs,
		Reasoning:                 request.ReasoningEnabled,
		MasterFlowID:              request.MasterFlowID,
		AvailableTools:            request.AvailableTools,
		ClientMCPServers:          request.ClientMCPServers,
		ClientSkillPackages:       request.ClientSkillPackages,
		ClientInstructions:        request.ClientInstructions,
		ParentID:                  request.ParentID,
	}

	content, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL.String(), bytes.NewReader(content))
	if err != nil {
		return nil, nil, fmt.Errorf("创建请求失败: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	c.applyAuth(httpRequest, request.UserAgent)

	resp, err := c.streamHTTPClient.Do(httpRequest)
	if err != nil {
		return nil, nil, fmt.Errorf("请求失败: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		return nil, nil, readHTTPError("请求失败", resp)
	}

	streamSession := &provider.StreamSession{SessionID: resp.Header.Get(SessionHeader)}
	stream := make(chan provider.StreamEvent)

	go func() {
		defer close(stream)
		reader := io.ReadCloser(resp.Body)
		if c.streamIdleTimeout > 0 {
			reader = newIdleTimeoutReadCloser(resp.Body, c.streamIdleTimeout)
		}
		defer reader.Close()
		readSSE(reader, stream)
	}()

	return stream, streamSession, nil
}

func (c *Client) GetRunStats(ctx context.Context, flowID string, sessionID string, runID string) (*provider.RunStats, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return &provider.RunStats{}, nil
	}

	stats := &provider.RunStats{RunID: runID}

	if strings.TrimSpace(flowID) != "" && strings.TrimSpace(sessionID) != "" {
		messageEndpoint := c.apiURL(fmt.Sprintf(
			"/agents/flows/%s/sessions/%s/messages?orderby=timestamp&offset=0&limit=100",
			url.PathEscape(flowID),
			url.PathEscape(sessionID),
		))
		messageRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, messageEndpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("创建消息统计请求失败: %w", err)
		}
		c.applyAuth(messageRequest)

		messageResponse, err := c.httpClient.Do(messageRequest)
		if err != nil {
			return nil, fmt.Errorf("请求消息统计失败: %w", err)
		}
		defer messageResponse.Body.Close()

		if messageResponse.StatusCode >= http.StatusBadRequest {
			return nil, readHTTPError("获取消息统计失败", messageResponse)
		}

		var messagePayload sessionMessagePageResponse
		if err := json.NewDecoder(messageResponse.Body).Decode(&messagePayload); err != nil {
			return nil, fmt.Errorf("解析消息统计响应失败: %w", err)
		}

		for _, item := range messagePayload.Data {
			if strings.TrimSpace(item.RunID) != runID {
				continue
			}
			stats.MessageID = item.ID
			if usage := parseTokenUsage(item.TokenUsage); usage != nil {
				stats.TokenUsage = usage
			}
		}
	}

	compressions, err := c.fetchCompressionRecords(ctx, flowID, sessionID, runID)
	if err != nil {
		return nil, err
	}
	stats.Compressions = compressions

	return stats, nil
}

func (c *Client) fetchCompressionRecords(ctx context.Context, flowID string, sessionID string, runID string) ([]provider.CompressionRecord, error) {
	flowID = strings.TrimSpace(flowID)
	sessionID = strings.TrimSpace(sessionID)
	runID = strings.TrimSpace(runID)
	if flowID == "" || sessionID == "" || runID == "" {
		return nil, nil
	}
	attempts := c.compressionPollAttempts
	if attempts < 1 {
		attempts = 1
	}
	delay := c.compressionPollDelay
	for attempt := 0; attempt < attempts; attempt++ {
		compressionPayload, err := c.fetchCompressionPayload(ctx, flowID, sessionID, runID)
		if err != nil {
			return nil, err
		}
		compressions := make([]provider.CompressionRecord, 0, len(compressionPayload))
		for _, item := range compressionPayload {
			compressions = append(compressions, parseCompressionRecord(item))
		}
		if len(compressions) > 0 || attempt == attempts-1 || delay <= 0 {
			return compressions, nil
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, nil
}

func (c *Client) GetSessionContextWindow(ctx context.Context, flowID string, sessionID string) (*provider.SessionContextPreview, error) {
	flowID = strings.TrimSpace(flowID)
	sessionID = strings.TrimSpace(sessionID)
	if flowID == "" {
		return nil, fmt.Errorf("获取会话上下文失败: 智能体不能为空")
	}
	if sessionID == "" {
		return &provider.SessionContextPreview{}, nil
	}

	contextEndpoint := c.apiURL(fmt.Sprintf(
		"/agents/flows/%s/sessions/%s/context-window",
		url.PathEscape(flowID),
		url.PathEscape(sessionID),
	))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, contextEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建会话上下文请求失败: %w", err)
	}
	c.applyAuth(request)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求会话上下文失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取会话上下文失败", response)
	}

	var payload sessionContextPreviewResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析会话上下文响应失败: %w", err)
	}

	preview := &provider.SessionContextPreview{
		FlowID:    strings.TrimSpace(payload.FlowID),
		SessionID: strings.TrimSpace(payload.SessionID),
		Source:    strings.TrimSpace(payload.Source),
	}
	if payload.ContextWindow != nil {
		preview.ContextWindow = &provider.ContextWindowUsage{
			MaxInputTokens:              payload.ContextWindow.MaxInputTokens,
			ContextUsageTokens:          payload.ContextWindow.ContextUsageTokens,
			DirectMessageTokens:         payload.ContextWindow.DirectMessageTokens,
			CompressedHistoryTokens:     payload.ContextWindow.CompressedHistoryTokens,
			CurrentRoundInputTokens:     payload.ContextWindow.CurrentRoundInputTokens,
			NextRoundAvailableTokens:    payload.ContextWindow.NextRoundAvailableTokens,
			CurrentRoundAvailableTokens: payload.ContextWindow.CurrentRoundAvailableTokens,
			HistoryMessageCount:         payload.ContextWindow.HistoryMessageCount,
			PreparedMessageCount:        payload.ContextWindow.PreparedMessageCount,
			HighWatermarkRatio:          payload.ContextWindow.HighWatermarkRatio,
			CompressionTargetRatio:      payload.ContextWindow.CompressionTargetRatio,
			HighWatermarkTokens:         payload.ContextWindow.HighWatermarkTokens,
			CompressionTargetTokens:     payload.ContextWindow.CompressionTargetTokens,
		}
	}
	if payload.LatestCompression != nil {
		compression := parseCompressionRecord(*payload.LatestCompression)
		preview.LatestCompression = &compression
	}
	if parsed, err := parseTaiyiTime(payload.SourceUpdatedAt); err == nil {
		preview.SourceUpdatedAt = parsed
	}
	preview.MemoryTiers = normalizeTrimmedStringSlice(payload.MemoryTiers)
	return preview, nil
}

func (c *Client) ListMemoryConsolidations(ctx context.Context, query provider.MemoryConsolidationQuery) (*provider.MemoryConsolidationPage, error) {
	endpointURL, err := url.Parse(c.apiURL("/agents/memory-consolidations"))
	if err != nil {
		return nil, fmt.Errorf("构造跨会话记忆整合账本请求地址失败: %w", err)
	}
	queryValues := endpointURL.Query()
	if flowID := strings.TrimSpace(query.FlowID); flowID != "" {
		queryValues.Set("flow_id", flowID)
	}
	if statusName := strings.TrimSpace(query.Status); statusName != "" {
		queryValues.Set("status", statusName)
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	queryValues.Set("offset", strconv.Itoa(offset))
	queryValues.Set("limit", strconv.Itoa(limit))
	endpointURL.RawQuery = queryValues.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建跨会话记忆整合账本请求失败: %w", err)
	}
	c.applyAuth(request)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("请求跨会话记忆整合账本失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取跨会话记忆整合账本失败", response)
	}

	var payload memoryConsolidationTaskPageResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析跨会话记忆整合账本响应失败: %w", err)
	}

	items := make([]provider.MemoryConsolidationTask, 0, len(payload.Data))
	for _, item := range payload.Data {
		task := provider.MemoryConsolidationTask{
			ID:               strings.TrimSpace(item.ID),
			ScopeKey:         strings.TrimSpace(item.ScopeKey),
			FlowID:           strings.TrimSpace(item.FlowID),
			OwnerAppID:       strings.TrimSpace(item.OwnerAppID),
			OwnerUserID:      strings.TrimSpace(item.OwnerUserID),
			TriggerReason:    strings.TrimSpace(item.TriggerReason),
			Status:           strings.TrimSpace(item.Status),
			AttemptCount:     item.AttemptCount,
			LastError:        strings.TrimSpace(item.LastError),
			LastSourceCount:  item.LastSourceCount,
			LastSessionCount: item.LastSessionCount,
			LastTaskID:       strings.TrimSpace(item.LastTaskID),
			ResultSegmentKey: strings.TrimSpace(item.ResultSegmentKey),
		}
		if parsed, err := parseTaiyiTime(item.WindowStart); err == nil {
			task.WindowStart = parsed
		}
		if parsed, err := parseTaiyiTime(item.WindowEnd); err == nil {
			task.WindowEnd = parsed
		}
		if parsed, err := parseTaiyiTime(item.LastRunAt); err == nil {
			task.LastRunAt = parsed
		}
		if parsed, err := parseTaiyiTime(item.CreatedAt); err == nil {
			task.CreatedAt = parsed
		}
		if parsed, err := parseTaiyiTime(item.UpdatedAt); err == nil {
			task.UpdatedAt = parsed
		}
		items = append(items, task)
	}

	return &provider.MemoryConsolidationPage{
		Total:  payload.Total,
		Offset: payload.Offset,
		Items:  items,
	}, nil
}

func (c *Client) TriggerMemoryConsolidation(ctx context.Context, request provider.MemoryConsolidationTriggerRequest) (*provider.MemoryConsolidationTriggerResult, error) {
	flowID := strings.TrimSpace(request.FlowID)
	if flowID == "" {
		return nil, fmt.Errorf("触发跨会话记忆整合失败: 智能体不能为空")
	}
	minHours := request.MinHours
	if minHours < 1 {
		minHours = 1
	}
	minSessions := request.MinSessions
	if minSessions < 1 {
		minSessions = 1
	}
	body := memoryConsolidationTriggerRequestBody{
		FlowID:         flowID,
		OwnerAppID:     strings.TrimSpace(request.OwnerAppID),
		MinHours:       minHours,
		MinSessions:    minSessions,
		ExecuteEnabled: request.ExecuteEnabled,
	}
	content, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化跨会话记忆整合请求失败: %w", err)
	}

	endpoint := c.apiURL("/agents/memory-consolidations/trigger")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("创建跨会话记忆整合请求失败: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	c.applyAuth(httpRequest)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("请求跨会话记忆整合失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("触发跨会话记忆整合失败", response)
	}

	var payload memoryConsolidationTriggerResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析跨会话记忆整合响应失败: %w", err)
	}

	return &provider.MemoryConsolidationTriggerResult{
		TaskID:        strings.TrimSpace(payload.TaskID),
		BackendTaskID: strings.TrimSpace(payload.BackendTaskID),
		Status:        strings.TrimSpace(payload.Status),
		Scheduled:     payload.Scheduled,
		Reason:        strings.TrimSpace(payload.Reason),
	}, nil
}

func (c *Client) CreateClientCollaboration(ctx context.Context, request provider.ClientCollaborationCreateRequest) (*provider.ClientCollaborationTask, error) {
	body := clientCollaborationCreateRequestBody{
		TaskKind:        firstNonEmpty(strings.TrimSpace(request.TaskKind), "dialogue_subtask"),
		TargetClientID:  strings.TrimSpace(request.TargetClientID),
		TargetDeviceID:  strings.TrimSpace(request.TargetDeviceID),
		DeliveryPolicy:  firstNonEmpty(strings.TrimSpace(request.DeliveryPolicy), "fail_if_offline"),
		CommandPayload:  request.CommandPayload,
		SourceSessionID: strings.TrimSpace(request.SourceSessionID),
		SourceRunID:     strings.TrimSpace(request.SourceRunID),
		SourceMessageID: strings.TrimSpace(request.SourceMessageID),
		ChildSessionID:  strings.TrimSpace(request.ChildSessionID),
		ChildRunID:      strings.TrimSpace(request.ChildRunID),
	}
	content, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化客户端协同任务请求失败: %w", err)
	}
	endpoint := c.apiURL("/agents/client/collaborations")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("创建客户端协同任务请求失败: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	c.applyAuth(httpRequest)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("创建客户端协同任务失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("创建客户端协同任务失败", response)
	}

	var payload clientCollaborationTaskResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析客户端协同任务响应失败: %w", err)
	}
	return mapClientCollaborationTask(payload)
}

func (c *Client) GetClientCollaboration(ctx context.Context, taskID string) (*provider.ClientCollaborationTask, error) {
	trimmedTaskID := strings.TrimSpace(taskID)
	if trimmedTaskID == "" {
		return nil, fmt.Errorf("查询客户端协同任务失败: task_id 不能为空")
	}
	endpoint := c.apiURL(fmt.Sprintf("/agents/client/collaborations/%s", url.PathEscape(trimmedTaskID)))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建查询客户端协同任务请求失败: %w", err)
	}
	c.applyAuth(httpRequest)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("查询客户端协同任务失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("查询客户端协同任务失败", response)
	}

	var payload clientCollaborationTaskResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析客户端协同任务详情失败: %w", err)
	}
	return mapClientCollaborationTask(payload)
}

func (c *Client) UpdateClientCollaboration(ctx context.Context, taskID string, request provider.ClientCollaborationUpdateRequest) (*provider.ClientCollaborationTask, error) {
	trimmedTaskID := strings.TrimSpace(taskID)
	if trimmedTaskID == "" {
		return nil, fmt.Errorf("更新客户端协同任务失败: task_id 不能为空")
	}
	body := clientCollaborationUpdateRequestBody{
		Status:        strings.TrimSpace(request.Status),
		StatusPayload: request.StatusPayload,
		ResultPayload: request.ResultPayload,
		ErrorMessage:  strings.TrimSpace(request.ErrorMessage),
	}
	content, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("序列化更新客户端协同任务请求失败: %w", err)
	}
	endpoint := c.apiURL(fmt.Sprintf("/agents/client/collaborations/%s", url.PathEscape(trimmedTaskID)))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("创建更新客户端协同任务请求失败: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	c.applyAuth(httpRequest)

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("更新客户端协同任务失败: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("更新客户端协同任务失败", response)
	}

	var payload clientCollaborationTaskResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析更新客户端协同任务响应失败: %w", err)
	}
	return mapClientCollaborationTask(payload)
}

func (c *Client) fetchCompressionPayload(ctx context.Context, flowID string, sessionID string, runID string) ([]compressionRecordResponse, error) {
	compressionEndpoint := c.apiURL(fmt.Sprintf(
		"/agents/flows/%s/sessions/%s/run/%s/compressions",
		url.PathEscape(flowID),
		url.PathEscape(sessionID),
		url.PathEscape(runID),
	))
	compressionRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, compressionEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建上下文压缩统计请求失败: %w", err)
	}
	c.applyAuth(compressionRequest)

	compressionResponse, err := c.httpClient.Do(compressionRequest)
	if err != nil {
		return nil, fmt.Errorf("请求上下文压缩统计失败: %w", err)
	}
	defer compressionResponse.Body.Close()

	if compressionResponse.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取上下文压缩统计失败", compressionResponse)
	}

	var compressionPayload []compressionRecordResponse
	if err := json.NewDecoder(compressionResponse.Body).Decode(&compressionPayload); err != nil {
		return nil, fmt.Errorf("解析上下文压缩统计响应失败: %w", err)
	}
	return compressionPayload, nil
}

func (c *Client) applyAuth(req *http.Request, requestUserAgent ...string) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	userAgent := defaultUserAgent()
	if len(requestUserAgent) > 0 && strings.TrimSpace(requestUserAgent[0]) != "" {
		userAgent = strings.TrimSpace(requestUserAgent[0])
	}
	req.Header.Set("User-Agent", userAgent)
}

func defaultUserAgent() string {
	version := strings.TrimSpace(buildinfo.Version)
	if version == "" {
		version = "dev"
	}
	return fmt.Sprintf("taiyiflow-cli/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH)
}

func (c *Client) SubmitClientCallResult(ctx context.Context, result provider.ClientCallResult) error {
	body := clientCallResultBody{
		FlowID:       result.FlowID,
		SessionID:    result.SessionID,
		MessageID:    result.MessageID,
		RunID:        result.RunID,
		CallID:       result.CallID,
		Success:      result.Success,
		Result:       result.Result,
		ErrorMessage: result.ErrorMessage,
	}

	content, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("序列化客户端回调结果失败: %w", err)
	}

	endpoint := c.apiURL("/agents/client/call/result")
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return fmt.Errorf("创建客户端回调请求失败: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	c.applyAuth(httpRequest)

	resp, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("提交客户端回调结果失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return readHTTPError("提交客户端回调结果失败", resp)
	}
	return nil
}

func readSSE(reader io.Reader, output chan<- provider.StreamEvent) {
	bufReader := bufio.NewReader(reader)

	var eventName string
	var dataLines []string

	flush := func() {
		if eventName == "" && len(dataLines) == 0 {
			return
		}
		raw := strings.Join(dataLines, "\n")
		payload := map[string]any{}
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				payload = map[string]any{"raw": raw, "error": err.Error()}
			}
		}
		typeName := provider.StreamEventType(firstNonEmpty(eventName, string(provider.EventMessage)))
		output <- provider.StreamEvent{Type: typeName, Data: payload, Raw: raw}
		eventName = ""
		dataLines = nil
	}

	for {
		rawLine, err := bufReader.ReadString('\n')
		line := strings.TrimRight(rawLine, "\r\n")

		if line == "" {
			flush()
		} else if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}

		if err != nil {
			if err != io.EOF {
				flush()
				var idleTimeoutErr streamIdleTimeoutError
				if errors.As(err, &idleTimeoutErr) {
					output <- provider.StreamEvent{
						Type: provider.EventError,
						Data: map[string]any{"error": fmt.Sprintf("读取流失败: %s", idleTimeoutErr.Error())},
					}
					return
				}
				output <- provider.StreamEvent{
					Type: provider.EventError,
					Data: map[string]any{"error": fmt.Sprintf("读取流失败: %v", err)},
				}
			}
			break
		}
	}

	flush()
}

func readHTTPError(prefix string, response *http.Response) error {
	content, _ := io.ReadAll(response.Body)
	message := strings.TrimSpace(string(content))
	if message == "" {
		message = response.Status
	}
	return fmt.Errorf("%s: %s", prefix, message)
}

func mapSearchResults(items []searchResultResponse) []provider.SearchResult {
	if len(items) == 0 {
		return nil
	}
	results := make([]provider.SearchResult, 0, len(items))
	for _, item := range items {
		results = append(results, provider.SearchResult{
			DocID:       item.DocID,
			FileType:    item.FileType,
			FilePath:    item.FilePath,
			Filename:    item.Filename,
			Content:     item.Content,
			PageNumbers: append([]int(nil), item.PageNumbers...),
			Text:        item.Text,
			Collection:  item.Collection,
			Partition:   item.Partition,
			Title:       item.Title,
			Link:        item.Link,
			Site:        item.Site,
			Source:      item.Source,
			Provider:    item.Provider,
		})
	}
	return results
}

func normalizeTrimmedStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	items := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		items = append(items, trimmed)
	}
	if len(items) == 0 {
		return nil
	}
	return items
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func parseTaiyiTime(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, nil
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999-0700",
		"2006-01-02T15:04:05.999999-0700",
		"2006-01-02T15:04:05.999-0700",
		"2006-01-02T15:04:05-0700",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, trimmed)
		if err == nil {
			return parsed, nil
		}
	}

	return time.Time{}, fmt.Errorf("无法识别的时间格式: %s", trimmed)
}

func mapClientCollaborationTask(payload clientCollaborationTaskResponse) (*provider.ClientCollaborationTask, error) {
	dispatchedAt, err := parseTaiyiTime(payload.DispatchedAt)
	if err != nil {
		return nil, fmt.Errorf("解析客户端协同任务 dispatched_at 失败: %w", err)
	}
	finishedAt, err := parseTaiyiTime(payload.FinishedAt)
	if err != nil {
		return nil, fmt.Errorf("解析客户端协同任务 finished_at 失败: %w", err)
	}
	expiredAt, err := parseTaiyiTime(payload.ExpiredAt)
	if err != nil {
		return nil, fmt.Errorf("解析客户端协同任务 expired_at 失败: %w", err)
	}
	createdAt, err := parseTaiyiTime(payload.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("解析客户端协同任务 created_at 失败: %w", err)
	}
	updatedAt, err := parseTaiyiTime(payload.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("解析客户端协同任务 updated_at 失败: %w", err)
	}
	return &provider.ClientCollaborationTask{
		ID:              strings.TrimSpace(payload.ID),
		OwnerUserID:     strings.TrimSpace(payload.OwnerUserID),
		TargetClientID:  strings.TrimSpace(payload.TargetClientID),
		TargetDeviceID:  strings.TrimSpace(payload.TargetDeviceID),
		SourceSessionID: strings.TrimSpace(payload.SourceSessionID),
		SourceRunID:     strings.TrimSpace(payload.SourceRunID),
		SourceMessageID: strings.TrimSpace(payload.SourceMessageID),
		ChildSessionID:  strings.TrimSpace(payload.ChildSessionID),
		ChildRunID:      strings.TrimSpace(payload.ChildRunID),
		TaskKind:        strings.TrimSpace(payload.TaskKind),
		DeliveryPolicy:  strings.TrimSpace(payload.DeliveryPolicy),
		Status:          strings.TrimSpace(payload.Status),
		CommandPayload:  payload.CommandPayload,
		StatusPayload:   payload.StatusPayload,
		ResultPayload:   payload.ResultPayload,
		ErrorMessage:    strings.TrimSpace(payload.ErrorMessage),
		DispatchedAt:    dispatchedAt,
		FinishedAt:      finishedAt,
		ExpiredAt:       expiredAt,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}, nil
}

func parseTokenUsage(payload map[string]any) *provider.TokenUsage {
	if len(payload) == 0 {
		return nil
	}

	usage := &provider.TokenUsage{
		InputTokens:  firstMapIntValue(payload, "input", "input_tokens", "prompt_tokens"),
		OutputTokens: firstMapIntValue(payload, "output", "output_tokens", "completion_tokens"),
		TotalTokens:  firstMapIntValue(payload, "total", "total_tokens"),
		StopReason:   firstMapStringValue(payload, "stop_reason", "finish_reason"),
		ModelName:    firstMapStringValue(payload, "model_name", "model"),
	}

	if contextWindow, ok := payload["context_window"].(map[string]any); ok {
		usage.ContextWindow = &provider.ContextWindowUsage{
			MaxInputTokens:              intValue(contextWindow["max_input_tokens"]),
			ContextUsageTokens:          intValue(contextWindow["context_usage_tokens"]),
			DirectMessageTokens:         intValue(contextWindow["direct_message_tokens"]),
			CompressedHistoryTokens:     intValue(contextWindow["compressed_history_tokens"]),
			CurrentRoundInputTokens:     intValue(contextWindow["current_round_input_tokens"]),
			NextRoundAvailableTokens:    intValue(contextWindow["next_round_available_tokens"]),
			CurrentRoundAvailableTokens: intValue(contextWindow["current_round_available_tokens"]),
			HistoryMessageCount:         intValue(contextWindow["history_message_count"]),
			PreparedMessageCount:        intValue(contextWindow["prepared_message_count"]),
			HighWatermarkRatio:          floatValue(contextWindow["high_watermark_ratio"]),
			CompressionTargetRatio:      floatValue(contextWindow["compression_target_ratio"]),
			HighWatermarkTokens:         intValue(contextWindow["high_watermark_tokens"]),
			CompressionTargetTokens:     intValue(contextWindow["compression_target_tokens"]),
		}
	}

	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 && usage.StopReason == "" && usage.ModelName == "" && usage.ContextWindow == nil {
		return nil
	}

	return usage
}

func parseCompressionRecord(payload compressionRecordResponse) provider.CompressionRecord {
	return provider.CompressionRecord{
		TriggerReason:         payload.TriggerReason,
		CompressionStrategy:   payload.CompressionStrategy,
		TargetType:            payload.TargetType,
		BeforeTokens:          payload.BeforeTokens,
		TargetTokens:          payload.TargetTokens,
		AfterTokens:           payload.AfterTokens,
		RemainingTokens:       payload.RemainingTokens,
		CompressionRatio:      payload.CompressionRatio,
		ReservedHistoryTokens: payload.ReservedHistoryTokens,
	}
}

func firstMapIntValue(payload map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		parsed := intValue(value)
		if parsed != 0 {
			return parsed
		}
	}
	return 0
}

func firstMapStringValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		parsed := stringValue(value)
		if parsed != "" {
			return parsed
		}
	}
	return ""
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int8:
		return int(typed)
	case int16:
		return int(typed)
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float32:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	default:
		return 0
	}
}

func floatValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed
		}
	}
	return 0
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}
