package provider

import "context"

type StreamEventType string

const (
	EventMessage    StreamEventType = "message"
	EventError      StreamEventType = "error"
	EventClose      StreamEventType = "close"
	EventBytes      StreamEventType = "bytes"
	EventCustom     StreamEventType = "custom"
	EventToolCall   StreamEventType = "tool_call"
	EventBuiltIn    StreamEventType = "built_in"
	EventClientTool StreamEventType = "client_tool"
	EventMCPTool    StreamEventType = "mcp_tool"
)

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
	CommandExecutionTarget string
	MaxIterations          int
	AgentContinueWindows   int
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
	GetMetadata(ctx context.Context, flowID string) (*Metadata, error)
	Stream(ctx context.Context, request StreamRequest) (<-chan StreamEvent, *StreamSession, error)
	SubmitClientCallResult(ctx context.Context, result ClientCallResult) error
}
