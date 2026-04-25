package cmd

import (
	"bytes"
	"context"
	"testing"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
)

type stubProvider struct {
	stream        <-chan provider.StreamEvent
	streamErr     error
	streamSession *provider.StreamSession
	messages      *provider.SessionMessageList
	runStats      *provider.RunStats
	sessionContext *provider.SessionContextPreview
}

func (s stubProvider) Name() string { return "stub" }

func (s stubProvider) ListFlows(context.Context, string, string, int, int) (*provider.FlowList, error) {
	return nil, nil
}

func (s stubProvider) ListSessions(context.Context, string, int, int) (*provider.SessionList, error) {
	return nil, nil
}

func (s stubProvider) ListSessionMessages(context.Context, string, string, int, int) (*provider.SessionMessageList, error) {
	return s.messages, nil
}

func (s stubProvider) GetVersionInfo(context.Context) (*provider.ServiceVersion, error) {
	return nil, nil
}

func (s stubProvider) GetMetadata(context.Context, string) (*provider.Metadata, error) {
	return nil, nil
}

func (s stubProvider) Stream(context.Context, provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	return s.stream, s.streamSession, s.streamErr
}

func (s stubProvider) GetRunStats(context.Context, string, string, string) (*provider.RunStats, error) {
	return s.runStats, nil
}

func (s stubProvider) GetSessionContextWindow(context.Context, string, string) (*provider.SessionContextPreview, error) {
	return s.sessionContext, nil
}

func (s stubProvider) SubmitClientCallResult(context.Context, provider.ClientCallResult) error {
	return nil
}

func newTestAppWithSessionStore(t *testing.T) *App {
	t.Helper()
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	return &App{sessionStore: store}
}

func TestPrepareSessionKeepsSavedRequestModeWithoutOverride(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()
	state := agent.SessionState{FlowID: "demo-flow", Workdir: workdir, RequestMode: provider.RequestTypePlan}
	if err := app.sessionStore.Save(state); err != nil {
		t.Fatalf("save session failed: %v", err)
	}

	loaded, err := app.prepareSession("demo-flow", false, workdir, "")
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.RequestMode != provider.RequestTypePlan {
		t.Fatalf("expected saved request mode %q, got %q", provider.RequestTypePlan, loaded.RequestMode)
	}
}

func TestPrepareSessionDefaultsMissingSessionToAgentMode(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()

	loaded, err := app.prepareSession("demo-flow", false, workdir, "")
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.RequestMode != provider.RequestTypeAgent {
		t.Fatalf("expected default request mode %q, got %q", provider.RequestTypeAgent, loaded.RequestMode)
	}
}

func TestPrepareSessionNewSessionDefaultsToAgentMode(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()

	loaded, err := app.prepareSession("demo-flow", true, workdir, "")
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.RequestMode != provider.RequestTypeAgent {
		t.Fatalf("expected new session request mode %q, got %q", provider.RequestTypeAgent, loaded.RequestMode)
	}
}

func TestStreamTaskTrimsLeadingBlankLinesFromFirstChunk(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 3)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "\n\n\nhello"}}
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "\nworld"}}
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
	close(stream)

	_, err := app.streamTask(
		context.Background(),
		stubProvider{stream: stream},
		internalconfig.Config{},
		"demo-flow",
		"demo task",
		agent.SessionState{RequestMode: provider.RequestTypeAgent},
		"",
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "hello\nworld\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestStreamTaskDoesNotPrefixFirstSystemNoteWithBlankLine(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"message": "准备开始"}}
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
	close(stream)

	_, err := app.streamTask(
		context.Background(),
		stubProvider{stream: stream},
		internalconfig.Config{},
		"demo-flow",
		"demo task",
		agent.SessionState{RequestMode: provider.RequestTypeAgent},
		"",
		true,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "[系统提示]: 准备开始\n\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestLoadLatestSessionMetricsUsesLatestRunStats(t *testing.T) {
	metrics := loadLatestSessionMetrics(
		context.Background(),
		stubProvider{
			messages: &provider.SessionMessageList{
				Items: []provider.SessionMessage{
					{RunID: "run-latest"},
					{RunID: "run-older"},
				},
			},
			runStats: &provider.RunStats{
				RunID: "run-latest",
				TokenUsage: &provider.TokenUsage{
					ContextWindow: &provider.ContextWindowUsage{
						MaxInputTokens:           8192,
						CurrentRoundInputTokens:  2048,
						NextRoundAvailableTokens: 6144,
					},
				},
			},
		},
		"demo-flow",
		"session-123",
	)

	if metrics == nil || metrics.TokenUsage == nil || metrics.TokenUsage.ContextWindow == nil {
		t.Fatalf("expected metrics with context window, got %#v", metrics)
	}
	if metrics.RunID != "run-latest" {
		t.Fatalf("expected latest run id, got %#v", metrics)
	}
	if metrics.TokenUsage.ContextWindow.CurrentRoundInputTokens != 2048 {
		t.Fatalf("expected current round input tokens to be preserved, got %#v", metrics.TokenUsage.ContextWindow)
	}
}

func TestLoadLatestSessionMetricsOverridesContextWindowWithSessionPreview(t *testing.T) {
	metrics := loadLatestSessionMetrics(
		context.Background(),
		stubProvider{
			messages: &provider.SessionMessageList{
				Items: []provider.SessionMessage{{RunID: "run-latest"}},
			},
			runStats: &provider.RunStats{
				RunID: "run-latest",
				TokenUsage: &provider.TokenUsage{
					InputTokens:  1200,
					OutputTokens: 800,
					TotalTokens:  2000,
					ContextWindow: &provider.ContextWindowUsage{
						MaxInputTokens:           8192,
						CurrentRoundInputTokens:  2048,
						NextRoundAvailableTokens: 6144,
					},
				},
			},
			sessionContext: &provider.SessionContextPreview{
				FlowID:    "demo-flow",
				SessionID: "session-123",
				ContextWindow: &provider.ContextWindowUsage{
					MaxInputTokens:           8192,
					CurrentRoundInputTokens:  3072,
					NextRoundAvailableTokens: 5120,
				},
			},
		},
		"demo-flow",
		"session-123",
	)

	if metrics == nil || metrics.TokenUsage == nil || metrics.TokenUsage.ContextWindow == nil {
		t.Fatalf("expected merged metrics with session context, got %#v", metrics)
	}
	if metrics.RunID != "run-latest" {
		t.Fatalf("expected latest run id to survive merge, got %#v", metrics)
	}
	if metrics.TokenUsage.TotalTokens != 2000 {
		t.Fatalf("expected run token totals to survive merge, got %#v", metrics.TokenUsage)
	}
	if metrics.TokenUsage.ContextWindow.CurrentRoundInputTokens != 3072 {
		t.Fatalf("expected session preview context window to override run stats, got %#v", metrics.TokenUsage.ContextWindow)
	}
}

func TestBuildTUIAssistantChunkEventIncludesMetadata(t *testing.T) {
	event, ok := buildTUIAssistantChunkEvent(map[string]any{
		"chunk":      "回答片段",
		"message_id": "msg-1",
		"stage":      "end",
		"event_type": "message",
	})

	if !ok {
		t.Fatalf("expected assistant chunk event to be built")
	}
	if event.Kind != "assistant_chunk" {
		t.Fatalf("expected assistant chunk kind, got %#v", event.Kind)
	}
	if event.Text != "回答片段" {
		t.Fatalf("expected chunk text preserved, got %q", event.Text)
	}
	if event.MessageID != "msg-1" || event.Stage != "end" || event.EventType != "message" {
		t.Fatalf("expected metadata to be preserved, got %#v", event)
	}
}

func TestBuildTUIAssistantChunkEventFallsBackToIDAndText(t *testing.T) {
	event, ok := buildTUIAssistantChunkEvent(map[string]any{
		"text": "增量片段",
		"id":   "node-1",
	})

	if !ok {
		t.Fatalf("expected assistant event when text field exists")
	}
	if event.Text != "增量片段" {
		t.Fatalf("expected text field as chunk fallback, got %q", event.Text)
	}
	if event.MessageID != "node-1" {
		t.Fatalf("expected id fallback as message id, got %q", event.MessageID)
	}
}

func TestBuildTUIAssistantChunkEventRejectsEmptyChunk(t *testing.T) {
	if _, ok := buildTUIAssistantChunkEvent(map[string]any{"message": "仅进度"}); ok {
		t.Fatalf("expected no assistant chunk event when chunk/text missing")
	}
}
