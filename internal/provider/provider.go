package provider

import (
	"context"
	"strings"
	"time"
)

type StreamEventType string

const (
	EventMessage                         StreamEventType = "message"
	EventError                           StreamEventType = "error"
	EventClose                           StreamEventType = "close"
	EventCompression                     StreamEventType = "compression"
	EventCompressionStart                StreamEventType = "compression_start"
	EventCompressionEnd                  StreamEventType = "compression_end"
	EventBytes                           StreamEventType = "bytes"
	EventCustom                          StreamEventType = "custom"
	EventToolCall                        StreamEventType = "tool_call"
	EventBuiltIn                         StreamEventType = "built_in"
	EventClientTool                      StreamEventType = "client_tool"
	EventMCPTool                         StreamEventType = "mcp_tool"
	RequestTypeAsk                                       = "ask"
	RequestTypePlan                                      = "plan"
	RequestTypeAgent                                     = "agent"
	ClientTypeCLI                                        = "cli"
	ImageCompressionStrategyPassthrough                  = "passthrough"
	ImageCompressionStrategyFollowSystem                 = "follow_system"
	FeatureInternetSearch                                = "internet_search"
	FeatureKnowledgeSearch                               = "knowledge_search"
	FeatureImageUnderstanding                            = "image_understanding"
	FeatureAudioUnderstanding                            = "audio_understanding"
	FeatureVideoUnderstanding                            = "video_understanding"
	FeatureDocumentUnderstanding                         = "document_understanding"
	FeatureSupportKnowledge                              = "support_knowledge"
	FeatureSupportMCPTools                               = "support_mcp_tools"
	FeatureSupportHumanAIDialogue                        = "support_human_ai_dialogue"
)

func NormalizeRequestType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case RequestTypePlan:
		return RequestTypePlan
	case RequestTypeAgent:
		return RequestTypeAgent
	default:
		return RequestTypeAsk
	}
}

type StreamEvent struct {
	Type StreamEventType
	Data map[string]any
	Raw  string
}

type StreamRequest struct {
	FlowID                    string
	Input                     string
	InputType                 string
	Tweaks                    map[string]any
	InputFiles                []string
	InternetSearch            bool
	KnowledgeScopes           []string
	KnowledgeDocs             []string
	OutputType                string
	OutputComponent           string
	SessionID                 string
	RunID                     string
	RequestType               string
	ClientType                string
	ClientID                  string
	DeviceID                  string
	ClientContextDir          string
	UserAgent                 string
	CommandExecutionTarget    string
	MaxIterations             int
	AgentContinueWindows      int
	AgentAutoLoopEnabled      *bool
	AgentAutoLoopMaxRounds    int
	CrossSessionMemoryEnabled *bool
	MessageID                 string
	RequestedSkillNames       []string
	AgentInputs               map[string]any
	ReasoningEnabled          bool
	MasterFlowID              string
	AvailableTools            []any
	ClientMCPServers          map[string]any
	ClientSkillPackages       any
	ClientInstructions        any
	ParentID                  string
}

type SkillResolveRequest struct {
	FlowID                 string
	SessionID              string
	RequestType            string
	RuntimeStrategy        string
	CommandExecutionTarget string
	RequestedSkillNames    []string
	AvailableTools         []string
	ClientSkillPackages    any
}

type ResolvedSkill struct {
	Name        string
	Content     string
	Source      string
	Description string
	Version     string
}

type SkillResolvePreview struct {
	Skills            []ResolvedSkill
	MissingSkillNames []string
	AllowedTools      []string
}

type StreamSession struct {
	SessionID string
}

type ChatCapabilities struct {
	Features               []string
	UnknownFeatures        []string
	InternetSearch         bool
	KnowledgeSearch        bool
	SupportKnowledge       bool
	SupportMCPTools        bool
	SupportHumanAIDialogue bool
	ImageUnderstanding     bool
	AudioUnderstanding     bool
	VideoUnderstanding     bool
	DocumentUnderstanding  bool
}

type ServiceFeatureFlags struct {
	TextToSpeech    bool
	SpeechToText    bool
	HumanAIDialogue bool
}

type ServiceConfig struct {
	FeatureFlags           ServiceFeatureFlags
	MaxFileSizeUpload      int
	DocExts                []string
	MilvusEnabled          bool
	DefaultFlowID          string
	ImageFlowID            string
	DocUnstructuredFlowID  string
	AgentAutoLoopEnabled   bool
	AgentAutoLoopMaxRounds int
	LoopDecideFlowID       string
}

type KnowledgeBaseSummary struct {
	ID          string
	Code        string
	Name        string
	Type        string
	Description string
}

type KnowledgeBaseList struct {
	Total  int
	Offset int
	Items  []KnowledgeBaseSummary
}

type Metadata struct {
	ID             string
	Name           string
	Description    string
	WelcomeMessage string
	MaxInputTokens int
	Enabled        bool
	IsChat         bool
	SubHead        string
	Questions      []string
	Features       []string
	Capabilities   ChatCapabilities
}

func NormalizeMetadataFeatures(features []string) ChatCapabilities {
	capabilities := ChatCapabilities{}
	known := map[string]struct{}{}
	unknown := map[string]struct{}{}
	for _, feature := range features {
		canonical := canonicalFeatureName(feature)
		if canonical == "" {
			continue
		}
		switch canonical {
		case FeatureInternetSearch:
			capabilities.InternetSearch = true
		case FeatureKnowledgeSearch:
			capabilities.KnowledgeSearch = true
		case FeatureSupportKnowledge:
			capabilities.SupportKnowledge = true
			capabilities.KnowledgeSearch = true
		case FeatureSupportMCPTools:
			capabilities.SupportMCPTools = true
		case FeatureSupportHumanAIDialogue:
			capabilities.SupportHumanAIDialogue = true
		case FeatureImageUnderstanding:
			capabilities.ImageUnderstanding = true
		case FeatureAudioUnderstanding:
			capabilities.AudioUnderstanding = true
		case FeatureVideoUnderstanding:
			capabilities.VideoUnderstanding = true
		case FeatureDocumentUnderstanding:
			capabilities.DocumentUnderstanding = true
		default:
			if _, exists := unknown[canonical]; exists {
				continue
			}
			unknown[canonical] = struct{}{}
			capabilities.UnknownFeatures = append(capabilities.UnknownFeatures, canonical)
			continue
		}
		if _, exists := known[canonical]; exists {
			continue
		}
		known[canonical] = struct{}{}
		capabilities.Features = append(capabilities.Features, canonical)
	}
	return capabilities
}

func canonicalFeatureName(value string) string {
	canonical := strings.ToLower(strings.TrimSpace(value))
	canonical = strings.ReplaceAll(canonical, "-", "_")
	canonical = strings.ReplaceAll(canonical, " ", "_")
	return canonical
}

type ServiceVersion struct {
	Package     string
	Version     string
	MainVersion string
}

type CurrentUser struct {
	Name      string
	UserID    string
	UserName  string
	UserScope string
}

type ContextWindowUsage struct {
	MaxInputTokens              int
	ContextUsageTokens          int
	DirectMessageTokens         int
	CompressedHistoryTokens     int
	CurrentRoundInputTokens     int
	NextRoundAvailableTokens    int
	CurrentRoundAvailableTokens int
	HistoryMessageCount         int
	PreparedMessageCount        int
	HighWatermarkRatio          float64
	CompressionTargetRatio      float64
	HighWatermarkTokens         int
	CompressionTargetTokens     int
}

type TokenUsage struct {
	InputTokens   int
	OutputTokens  int
	TotalTokens   int
	StopReason    string
	ModelName     string
	ContextWindow *ContextWindowUsage
}

type CompressionRecord struct {
	TriggerReason         string
	CompressionStrategy   string
	TargetType            string
	BeforeTokens          int
	TargetTokens          int
	AfterTokens           int
	RemainingTokens       int
	CompressionRatio      float64
	ReservedHistoryTokens int
}

type RunStats struct {
	RunID        string
	MessageID    string
	TokenUsage   *TokenUsage
	Compressions []CompressionRecord
}

type SessionContextPreview struct {
	FlowID            string
	SessionID         string
	ContextWindow     *ContextWindowUsage
	LatestCompression *CompressionRecord
	MemoryTiers       []string
	SourceUpdatedAt   time.Time
	Source            string
}

type MemoryConsolidationQuery struct {
	FlowID string
	Status string
	Offset int
	Limit  int
}

type MemoryConsolidationTask struct {
	ID               string
	ScopeKey         string
	FlowID           string
	OwnerAppID       string
	OwnerUserID      string
	WindowStart      time.Time
	WindowEnd        time.Time
	TriggerReason    string
	Status           string
	AttemptCount     int
	LastError        string
	LastSourceCount  int
	LastSessionCount int
	LastTaskID       string
	ResultSegmentKey string
	LastRunAt        time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type MemoryConsolidationPage struct {
	Total  int
	Offset int
	Items  []MemoryConsolidationTask
}

type MemoryConsolidationTriggerRequest struct {
	FlowID         string
	OwnerAppID     string
	MinHours       int
	MinSessions    int
	ExecuteEnabled bool
}

type MemoryConsolidationTriggerResult struct {
	TaskID        string
	BackendTaskID string
	Status        string
	Scheduled     bool
	Reason        string
}

type FlowSummary struct {
	ID          string
	Name        string
	Description string
	Type        string
	OwnerAppID  string
}

type FlowList struct {
	Total  int
	Offset int
	Items  []FlowSummary
}

type SessionSummary struct {
	ID         string
	Name       string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ClientType string
}

type SessionList struct {
	Total  int
	Offset int
	Items  []SessionSummary
}

type SearchResult struct {
	DocID       string
	FileType    string
	FilePath    string
	Filename    string
	Content     string
	PageNumbers []int
	Text        string
	Collection  string
	Partition   string
	Title       string
	Link        string
	Site        string
	Source      string
	Provider    string
}

type UploadResult struct {
	FlowID   string
	FilePath string
}

type DocumentUnstructuredRequest struct {
	FlowID     string
	Input      string
	InputType  string
	InputFiles []string
	OutputType string
	SessionID  string
}

type DocumentUnstructuredResult struct {
	SessionID string
	Output    string
	MediaType string
}

type ImageUnderstandingRequest struct {
	FlowID     string
	Input      string
	InputType  string
	Tweaks     map[string]any
	InputFiles []string
	OutputType string
	SessionID  string
}

type ImageUnderstandingResult struct {
	SessionID string
	Output    string
	MediaType string
	Text      string
}

type ImageUnderstandingRunner interface {
	RunImageUnderstanding(ctx context.Context, request ImageUnderstandingRequest) (*ImageUnderstandingResult, error)
}

type SessionMessage struct {
	ID               string
	SessionID        string
	RunID            string
	Sender           string
	SenderName       string
	Text             string
	ReasonText       string
	SearchResults    []SearchResult
	SearchKeyword    string
	ReasoningEnabled bool
	SessionName      string
	Timestamp        time.Time
	Error            bool
}

type SessionMessageList struct {
	Total  int
	Offset int
	Items  []SessionMessage
}

type ClientCallResult struct {
	FlowID       string
	SessionID    string
	MessageID    string
	RunID        string
	CallID       string
	Success      bool
	Result       any
	ErrorMessage string
}

type RunFlowRequest struct {
	FlowID    string
	SessionID string
	Input     string
	InputType string
	Tweaks    map[string]any
}

type RunFlowResult struct {
	Output    string
	MediaType string
	SessionID string
}

type ClientCollaborationCreateRequest struct {
	TaskKind        string
	TargetClientID  string
	TargetDeviceID  string
	DeliveryPolicy  string
	CommandPayload  map[string]any
	SourceSessionID string
	SourceRunID     string
	SourceMessageID string
	ChildSessionID  string
	ChildRunID      string
}

type ClientCollaborationUpdateRequest struct {
	Status        string
	StatusPayload map[string]any
	ResultPayload map[string]any
	ErrorMessage  string
}

// VisibleSkillPackage 对应后端 VisibleSkillPackageRead
type VisibleSkillPackage struct {
	ID          string
	Name        string
	DisplayName string
	Summary     string
	Description string
	Version     string
	Scope       string
	Source      string
	Category    string
	UpdatedAt   string
}

// SkillPackageContent 对应后端 SkillPackageContentRead
type SkillPackageContent struct {
	Content string
	Version string
}

// SkillInstallationRecord 对应后端 SkillInstallationRecordRead
type SkillInstallationRecord struct {
	ID              string
	SkillPackageID  string
	SkillName       string
	SkillVersion    string
	Status          string
	InstallSource   string
	ClientID        string
	DeviceID        string
	ClientPlatform  string
	ClientVersion   string
	CreatedAt       string
	UpdatedAt       string
}

// SkillInstallationPage 安装记录分页结果
type SkillInstallationPage struct {
	Total  int
	Offset int
	Limit  int
	Items  []SkillInstallationRecord
}

// UploadCustomSkillRequest 上传自定义技能请求
type UploadCustomSkillRequest struct {
	Name        string
	Content     string
	DisplayName string
	Summary     string
	Version     string
}

// UploadCustomSkillArchiveRequest 上传自定义技能 ZIP 请求
type UploadCustomSkillArchiveRequest struct {
	FilePath    string
	Name        string
	DisplayName string
}

// ReportInstallationRequest 上报安装记录请求
type ReportInstallationRequest struct {
	SkillPackageID  string
	ClientID        string
	DeviceID        string
	InstallSource   string
	ClientPlatform  string
	ClientVersion   string
}

type ClientCollaborationTask struct {
	ID              string
	OwnerUserID     string
	TargetClientID  string
	TargetDeviceID  string
	SourceSessionID string
	SourceRunID     string
	SourceMessageID string
	ChildSessionID  string
	ChildRunID      string
	TaskKind        string
	DeliveryPolicy  string
	Status          string
	CommandPayload  map[string]any
	StatusPayload   map[string]any
	ResultPayload   map[string]any
	ErrorMessage    string
	DispatchedAt    time.Time
	FinishedAt      time.Time
	ExpiredAt       time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Provider interface {
	Name() string
	ListFlows(ctx context.Context, filter string, agentType string, offset, limit int) (*FlowList, error)
	GetFlow(ctx context.Context, flowID string) (*FlowSummary, error)
	ListKnowledgeBases(ctx context.Context, appid string, filter string, offset, limit int) (*KnowledgeBaseList, error)
	ListSessions(ctx context.Context, flowID string, offset, limit int) (*SessionList, error)
	UpdateSessionTitle(ctx context.Context, flowID string, sessionID string, sessionTitle string) (string, error)
	ListSessionMessages(ctx context.Context, flowID string, sessionID string, offset, limit int) (*SessionMessageList, error)
	CreateSessionID(ctx context.Context) (string, error)
	UploadFile(ctx context.Context, flowID string, sessionID string, localPath string, remotePath string) (*UploadResult, error)
	RunDocumentUnstructured(ctx context.Context, request DocumentUnstructuredRequest) (*DocumentUnstructuredResult, error)
	RunFlow(ctx context.Context, request RunFlowRequest) (*RunFlowResult, error)
	GetVersionInfo(ctx context.Context) (*ServiceVersion, error)
	GetServiceConfig(ctx context.Context) (*ServiceConfig, error)
	GetCurrentUserInfo(ctx context.Context) (*CurrentUser, error)
	GetMetadata(ctx context.Context, flowID string) (*Metadata, error)
	ResolveSkillPreview(ctx context.Context, request SkillResolveRequest) (*SkillResolvePreview, error)
	Stream(ctx context.Context, request StreamRequest) (<-chan StreamEvent, *StreamSession, error)
	GetRunStats(ctx context.Context, flowID string, sessionID string, runID string) (*RunStats, error)
	GetSessionContextWindow(ctx context.Context, flowID string, sessionID string) (*SessionContextPreview, error)
	ListMemoryConsolidations(ctx context.Context, query MemoryConsolidationQuery) (*MemoryConsolidationPage, error)
	TriggerMemoryConsolidation(ctx context.Context, request MemoryConsolidationTriggerRequest) (*MemoryConsolidationTriggerResult, error)
	SubmitClientCallResult(ctx context.Context, result ClientCallResult) error
	CreateClientCollaboration(ctx context.Context, request ClientCollaborationCreateRequest) (*ClientCollaborationTask, error)
	GetClientCollaboration(ctx context.Context, taskID string) (*ClientCollaborationTask, error)
	UpdateClientCollaboration(ctx context.Context, taskID string, request ClientCollaborationUpdateRequest) (*ClientCollaborationTask, error)

	// 后端技能包管理 API
	FetchVisibleSkills(ctx context.Context, params FetchVisibleSkillsParams) ([]VisibleSkillPackage, error)
	DownloadSkillContent(ctx context.Context, skillID string) (*SkillPackageContent, error)
	UploadCustomSkill(ctx context.Context, request UploadCustomSkillRequest) (*VisibleSkillPackage, error)
	UploadCustomSkillArchive(ctx context.Context, request UploadCustomSkillArchiveRequest) (*VisibleSkillPackage, error)
	DeleteCustomSkill(ctx context.Context, skillID string) error
	ReportInstallation(ctx context.Context, request ReportInstallationRequest) (*SkillInstallationRecord, error)
	QueryInstallations(ctx context.Context, params QueryInstallationsParams) (*SkillInstallationPage, error)
	UninstallSkillRecord(ctx context.Context, recordID string) (*SkillInstallationRecord, error)
}

// FetchVisibleSkillsParams 查询可见技能列表参数
type FetchVisibleSkillsParams struct {
	FlowID                 string
	SessionID              string
	CommandExecutionTarget string
	AvailableTools         []string
	TerminalType           string
}

// QueryInstallationsParams 查询安装记录参数
type QueryInstallationsParams struct {
	ClientID       string
	SkillPackageID string
	Status         string
	Offset         int
	Limit          int
}
