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
}

func (s stubProvider) Name() string { return "stub" }

func (s stubProvider) ListFlows(context.Context, string, string, int, int) (*provider.FlowList, error) {
	return nil, nil
}

func (s stubProvider) ListSessions(context.Context, string, int, int) (*provider.SessionList, error) {
	return nil, nil
}

func (s stubProvider) ListSessionMessages(context.Context, string, string, int, int) (*provider.SessionMessageList, error) {
	return nil, nil
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
	return nil, nil
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
