package provider

import (
	"context"
	"strings"
	"time"
)

type StreamEventType string

const (
	EventMessage     StreamEventType = "message"
	EventError       StreamEventType = "error"
	EventClose       StreamEventType = "close"
	EventCompression StreamEventType = "compression"
	EventBytes       StreamEventType = "bytes"
	EventCustom      StreamEventType = "custom"
	EventToolCall    StreamEventType = "tool_call"
	EventBuiltIn     StreamEventType = "built_in"
	EventClientTool  StreamEventType = "client_tool"
	EventMCPTool     StreamEventType = "mcp_tool"
	RequestTypeAsk                   = "ask"
	RequestTypePlan                  = "plan"
	RequestTypeAgent                 = "agent"
	DeviceTypeCLI                    = "cli"
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
	FlowID                 string
	Input                  string
	InputType              string
	InputFiles             []string
	InternetSearch         bool
	KnowledgeScopes        []string
	KnowledgeDocs          []string
	OutputType             string
	OutputComponent        string
	SessionID              string
	RunID                  string
	RequestType            string
	DeviceType             string
	ClientContextDir       string
	UserAgent              string
	CommandExecutionTarget string
	MaxIterations          int
	AgentContinueWindows   int
	AgentAutoLoopEnabled   *bool
	AgentAutoLoopMaxRounds int
	MessageID              string
	RequestedSkillNames    []string
	AgentInputs            map[string]any
	ReasoningEnabled       bool
	MasterFlowID           string
	AvailableTools         []any
	ClientMCPServers       map[string]any
	ClientSkillPackages    any
}

type StreamSession struct {
	SessionID string
}

type Metadata struct {
	ID             string
	Name           string
	Description    string
	WelcomeMessage string
	Enabled        bool
	IsChat         bool
}

type ServiceVersion struct {
	Package     string
	Version     string
	MainVersion string
}

type ContextWindowUsage struct {
	MaxInputTokens              int
	ShortTermContextTokens      int
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

type SessionMessage struct {
	ID          string
	SessionID   string
	RunID       string
	Sender      string
	SenderName  string
	Text        string
	SessionName string
	Timestamp   time.Time
	Error       bool
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
	ListSessions(ctx context.Context, flowID string, offset, limit int) (*SessionList, error)
	ListSessionMessages(ctx context.Context, flowID string, sessionID string, offset, limit int) (*SessionMessageList, error)
	GetVersionInfo(ctx context.Context) (*ServiceVersion, error)
	GetMetadata(ctx context.Context, flowID string) (*Metadata, error)
	Stream(ctx context.Context, request StreamRequest) (<-chan StreamEvent, *StreamSession, error)
	GetRunStats(ctx context.Context, flowID string, sessionID string, runID string) (*RunStats, error)
	GetSessionContextWindow(ctx context.Context, flowID string, sessionID string) (*SessionContextPreview, error)
	SubmitClientCallResult(ctx context.Context, result ClientCallResult) error
}
