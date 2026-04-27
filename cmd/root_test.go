package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
)

type stubProvider struct {
	stream         <-chan provider.StreamEvent
	streamErr      error
	streamSession  *provider.StreamSession
	messages       *provider.SessionMessageList
	runStats       *provider.RunStats
	sessionContext *provider.SessionContextPreview
	lastRequest    provider.StreamRequest
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

func (s *stubProvider) Stream(_ context.Context, request provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	s.lastRequest = request
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

type fakeTTYWriter struct {
	bytes.Buffer
	fd uintptr
}

func (w *fakeTTYWriter) Fd() uintptr {
	return w.fd
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

func TestRunInteractiveSessionConfigCommandPersistsAgentAutoLoopOverrides(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()
	state := agent.SessionState{FlowID: "demo-flow", Workdir: workdir, RequestMode: provider.RequestTypeAgent}

	output, err := app.runInteractiveSessionConfigCommand("/config session set agent_auto_loop_enabled off", &state)
	if err != nil {
		t.Fatalf("set agent_auto_loop_enabled failed: %v", err)
	}
	if output != "已设置会话 Agent 自动续跑开关: off" {
		t.Fatalf("unexpected output: %q", output)
	}

	output, err = app.runInteractiveSessionConfigCommand("/config session set agent_auto_loop_max_rounds 5", &state)
	if err != nil {
		t.Fatalf("set agent_auto_loop_max_rounds failed: %v", err)
	}
	if output != "已设置会话 Agent 自动续跑最大轮数: 5" {
		t.Fatalf("unexpected output: %q", output)
	}

	loaded, err := app.prepareSession("demo-flow", false, workdir, provider.RequestTypeAgent)
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.AgentAutoLoopEnabledOverride != "off" {
		t.Fatalf("expected saved auto loop enabled override off, got %q", loaded.AgentAutoLoopEnabledOverride)
	}
	if loaded.AgentAutoLoopMaxRoundsOverride != 5 {
		t.Fatalf("expected saved auto loop max rounds override 5, got %d", loaded.AgentAutoLoopMaxRoundsOverride)
	}

	output, err = app.runInteractiveSessionConfigCommand("/config session unset agent_auto_loop_enabled", &state)
	if err != nil {
		t.Fatalf("unset agent_auto_loop_enabled failed: %v", err)
	}
	if output != "已清空会话 Agent 自动续跑开关，恢复继承全局配置" {
		t.Fatalf("unexpected output: %q", output)
	}
	if state.AgentAutoLoopEnabledOverride != "" {
		t.Fatalf("expected in-memory auto loop enabled override cleared, got %q", state.AgentAutoLoopEnabledOverride)
	}
}

func TestResolveAgentAutoLoopEnabledPreservesFalseOverride(t *testing.T) {
	config := internalconfig.Default()
	config.AgentAutoLoopEnabled = true
	resolved := resolveAgentAutoLoopEnabled(config, agent.SessionState{AgentAutoLoopEnabledOverride: "off"}, provider.RequestTypeAgent)
	if resolved == nil {
		t.Fatal("expected explicit agent auto loop override to be sent")
	}
	if *resolved {
		t.Fatal("expected false override to win over global config")
	}
}

func TestDisableCLIFocusReportingWritesSequenceToTerminalOutput(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	stdout := &fakeTTYWriter{fd: 1}
	var stderr bytes.Buffer
	app.stdout = stdout
	app.stderr = &stderr

	previous := isTerminalFD
	isTerminalFD = func(fd int) bool {
		return fd == 1
	}
	defer func() {
		isTerminalFD = previous
	}()

	app.disableCLIFocusReporting()

	if got := stdout.String(); got != cliDisableFocusReporting {
		t.Fatalf("expected focus disable sequence on stdout, got %q", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected stderr to stay empty, got %q", got)
	}
}

func TestDisableCLIFocusReportingSkipsNonTerminalWriters(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr

	previous := isTerminalFD
	isTerminalFD = func(int) bool {
		return false
	}
	defer func() {
		isTerminalFD = previous
	}()

	app.disableCLIFocusReporting()

	if got := stdout.String(); got != "" {
		t.Fatalf("expected stdout to stay empty, got %q", got)
	}
	if got := stderr.String(); got != "" {
		t.Fatalf("expected stderr to stay empty, got %q", got)
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
		&stubProvider{stream: stream},
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
		&stubProvider{stream: stream},
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

func TestStreamTaskSanitizesTerminalEscapeSequencesFromChunks(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "\x1b[O\x1b[I正式输出"}}
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
	close(stream)

	_, err := app.streamTask(
		context.Background(),
		&stubProvider{stream: stream},
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

	if got := stdout.String(); got != "正式输出\n" {
		t.Fatalf("expected sanitized stdout, got %q", got)
	}
}

func TestLoadLatestSessionMetricsUsesLatestRunStats(t *testing.T) {
	metrics := loadLatestSessionMetrics(
		context.Background(),
		&stubProvider{
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

func TestStreamTaskIncludesWorkspaceSkillPackagesInRequest(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	workspaceDir := t.TempDir()
	skillDir := filepath.Join(workspaceDir, ".snz1dp", "skills", "context-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	content := "---\nname: Context Review\ndescription: 当前工作区技能\ncategory: engineering\n---\n\n检查上下文目录。\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill file failed: %v", err)
	}

	stream := make(chan provider.StreamEvent, 1)
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
	close(stream)
	providerStub := &stubProvider{stream: stream}

	_, err := app.streamTask(
		context.Background(),
		providerStub,
		internalconfig.Config{Workdir: workspaceDir},
		"demo-flow",
		"demo task",
		agent.SessionState{RequestMode: provider.RequestTypeAgent, ContextDir: workspaceDir},
		"",
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if !containsIgnoreCase(providerStub.lastRequest.RequestedSkillNames, "Context Review") {
		t.Fatalf("expected workspace skill in requested names, got %#v", providerStub.lastRequest.RequestedSkillNames)
	}
	packages, ok := providerStub.lastRequest.ClientSkillPackages.([]map[string]any)
	if !ok || len(packages) == 0 {
		t.Fatalf("expected client skill packages in request, got %#v", providerStub.lastRequest.ClientSkillPackages)
	}
	found := false
	for _, pkg := range packages {
		skillsValue, ok := pkg["skills"].([]map[string]any)
		if !ok {
			continue
		}
		for _, skill := range skillsValue {
			if skill["name"] == "Context Review" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected workspace skill payload in request, got %#v", packages)
	}
}

func TestLoadLatestSessionMetricsOverridesContextWindowWithSessionPreview(t *testing.T) {
	metrics := loadLatestSessionMetrics(
		context.Background(),
		&stubProvider{
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
