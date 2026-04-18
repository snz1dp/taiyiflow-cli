package provider

import "context"

type StreamEventType string

const (
	EventMessage StreamEventType = "message"
	EventError   StreamEventType = "error"
	EventClose   StreamEventType = "close"
)

type StreamEvent struct {
	Type StreamEventType
	Data map[string]any
	Raw  string
}

type StreamRequest struct {
	FlowID                 string
	Input                  string
	SessionID              string
	RequestType            string
	CommandExecutionTarget string
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

type Provider interface {
	Name() string
	ListFlows(ctx context.Context, filter string, offset, limit int) (*FlowList, error)
	GetMetadata(ctx context.Context, flowID string) (*Metadata, error)
	Stream(ctx context.Context, request StreamRequest) (<-chan StreamEvent, *StreamSession, error)
}
