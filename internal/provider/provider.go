package provider

import (
	"context"
	"strings"
	"time"
)

type StreamEventType string

const (
	EventMessage                  StreamEventType = "message"
	EventError                    StreamEventType = "error"
	EventClose                    StreamEventType = "close"
	EventCompression              StreamEventType = "compression"
	EventCompressionStart         StreamEventType = "compression_start"
	EventCompressionEnd           StreamEventType = "compression_end"
	EventBytes                    StreamEventType = "bytes"
	EventCustom                   StreamEventType = "custom"
	EventToolCall                 StreamEventType = "tool_call"
	EventBuiltIn                  StreamEventType = "built_in"
	EventClientTool               StreamEventType = "client_tool"
	EventMCPTool                  StreamEventType = "mcp_tool"
	RequestTypeAsk                                = "ask"
	RequestTypePlan                               = "plan"
	RequestTypeAgent                              = "agent"
	DeviceTypeCLI                                 = "cli"
	FeatureInternetSearch                         = "internet_search"
	FeatureKnowledgeSearch                        = "knowledge_search"
	FeatureImageUnderstanding                     = "image_understanding"
	FeatureAudioUnderstanding                     = "audio_understanding"
	FeatureVideoUnderstanding                     = "video_understanding"
	FeatureDocumentUnderstanding                  = "document_understanding"
	FeatureSupportKnowledge                       = "support_knowledge"
	FeatureSupportMCPTools                        = "support_mcp_tools"
	FeatureSupportHumanAIDialogue                 = "support_human_ai_dialogue"
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
	DeviceType                string
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
}

type SkillResolveRequest struct {
	FlowID                 string
	SessionID              string
	OwnerUserID            string
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
	FeatureFlags      ServiceFeatureFlags
	MaxFileSizeUpload int
	DocExts           []string
	MilvusEnabled     bool
	DefaultFlowID     string
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
	SourceUpdatedAt   time.Time
	Source            string
}

type FlowSummary struct {
	ID          string
	Name        string
	Description string
	Type        string
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

type Provider interface {
	Name() string
	ListFlows(ctx context.Context, filter string, agentType string, offset, limit int) (*FlowList, error)
	ListKnowledgeBases(ctx context.Context, filter string, offset, limit int) (*KnowledgeBaseList, error)
	ListSessions(ctx context.Context, flowID string, offset, limit int) (*SessionList, error)
	UpdateSessionTitle(ctx context.Context, flowID string, sessionID string, sessionTitle string) (string, error)
	ListSessionMessages(ctx context.Context, flowID string, sessionID string, offset, limit int) (*SessionMessageList, error)
	CreateSessionID(ctx context.Context) (string, error)
	UploadFile(ctx context.Context, flowID string, sessionID string, localPath string, remotePath string) (*UploadResult, error)
	GetVersionInfo(ctx context.Context) (*ServiceVersion, error)
	GetServiceConfig(ctx context.Context) (*ServiceConfig, error)
	GetCurrentUserInfo(ctx context.Context) (*CurrentUser, error)
	GetMetadata(ctx context.Context, flowID string) (*Metadata, error)
	ResolveSkillPreview(ctx context.Context, request SkillResolveRequest) (*SkillResolvePreview, error)
	Stream(ctx context.Context, request StreamRequest) (<-chan StreamEvent, *StreamSession, error)
	GetRunStats(ctx context.Context, flowID string, sessionID string, runID string) (*RunStats, error)
	GetSessionContextWindow(ctx context.Context, flowID string, sessionID string) (*SessionContextPreview, error)
	SubmitClientCallResult(ctx context.Context, result ClientCallResult) error
}
