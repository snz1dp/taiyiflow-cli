package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
)

type stubProvider struct {
	stream             <-chan provider.StreamEvent
	streamErr          error
	streamSession      *provider.StreamSession
	messages           *provider.SessionMessageList
	runStats           *provider.RunStats
	sessionContext     *provider.SessionContextPreview
	memoryTasks        *provider.MemoryConsolidationPage
	memoryTasksErr     error
	memoryTrigger      *provider.MemoryConsolidationTriggerResult
	memoryTriggerErr   error
	updateSessionTitle func(context.Context, string, string, string) (string, error)
	skillPreview       *provider.SkillResolvePreview
	skillPreviewErr    error
	uploadResult       *provider.UploadResult
	uploadErr          error
	uploadHook         func(context.Context, string, string, string, string) (*provider.UploadResult, error)
	lastRequest        provider.StreamRequest
	lastResolveRequest provider.SkillResolveRequest
	submitResults      *[]provider.ClientCallResult
	submitHook         func(provider.ClientCallResult) error
}

type sequenceStreamProvider struct {
	stubProvider
	streams []<-chan provider.StreamEvent
	calls   int
}

type sessionListProvider struct {
	stubProvider
	responses map[int]*provider.SessionList
}

func (s *sequenceStreamProvider) Stream(_ context.Context, request provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	s.calls++
	s.lastRequest = request
	if len(s.streams) == 0 {
		stream := make(chan provider.StreamEvent)
		close(stream)
		return stream, nil, nil
	}
	stream := s.streams[0]
	s.streams = s.streams[1:]
	return stream, nil, nil
}

func (s *sessionListProvider) ListSessions(_ context.Context, _ string, offset, limit int) (*provider.SessionList, error) {
	if response, ok := s.responses[offset]; ok {
		return response, nil
	}
	return &provider.SessionList{Total: 0, Offset: offset}, nil
}

func (s stubProvider) Name() string { return "stub" }

func (s stubProvider) ListFlows(context.Context, string, string, int, int) (*provider.FlowList, error) {
	return &provider.FlowList{}, nil
}

func (s stubProvider) ListKnowledgeBases(context.Context, string, int, int) (*provider.KnowledgeBaseList, error) {
	return &provider.KnowledgeBaseList{}, nil
}

func (s stubProvider) ListSessions(context.Context, string, int, int) (*provider.SessionList, error) {
	return nil, nil
}

func (s stubProvider) UpdateSessionTitle(ctx context.Context, flowID string, sessionID string, sessionTitle string) (string, error) {
	if s.updateSessionTitle != nil {
		return s.updateSessionTitle(ctx, flowID, sessionID, sessionTitle)
	}
	return strings.TrimSpace(sessionTitle), nil
}

func (s stubProvider) ListSessionMessages(context.Context, string, string, int, int) (*provider.SessionMessageList, error) {
	return s.messages, nil
}

func (s stubProvider) CreateSessionID(context.Context) (string, error) {
	return "session-created", nil
}

func (s stubProvider) UploadFile(ctx context.Context, flowID string, sessionID string, localPath string, remotePath string) (*provider.UploadResult, error) {
	if s.uploadHook != nil {
		return s.uploadHook(ctx, flowID, sessionID, localPath, remotePath)
	}
	if s.uploadResult != nil || s.uploadErr != nil {
		return s.uploadResult, s.uploadErr
	}
	return &provider.UploadResult{}, nil
}

func (s stubProvider) RunDocumentUnstructured(context.Context, provider.DocumentUnstructuredRequest) (*provider.DocumentUnstructuredResult, error) {
	return &provider.DocumentUnstructuredResult{}, nil
}

func (s stubProvider) GetVersionInfo(context.Context) (*provider.ServiceVersion, error) {
	return nil, nil
}

func (s stubProvider) GetServiceConfig(context.Context) (*provider.ServiceConfig, error) {
	return &provider.ServiceConfig{}, nil
}

func (s stubProvider) GetCurrentUserInfo(context.Context) (*provider.CurrentUser, error) {
	return nil, nil
}

func (s stubProvider) GetMetadata(context.Context, string) (*provider.Metadata, error) {
	return &provider.Metadata{}, nil
}

func (s *stubProvider) ResolveSkillPreview(_ context.Context, request provider.SkillResolveRequest) (*provider.SkillResolvePreview, error) {
	s.lastResolveRequest = request
	if s.skillPreview != nil || s.skillPreviewErr != nil {
		return s.skillPreview, s.skillPreviewErr
	}
	return &provider.SkillResolvePreview{}, nil
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

func (s stubProvider) ListMemoryConsolidations(context.Context, provider.MemoryConsolidationQuery) (*provider.MemoryConsolidationPage, error) {
	return s.memoryTasks, s.memoryTasksErr
}

func (s stubProvider) TriggerMemoryConsolidation(context.Context, provider.MemoryConsolidationTriggerRequest) (*provider.MemoryConsolidationTriggerResult, error) {
	return s.memoryTrigger, s.memoryTriggerErr
}

func (s stubProvider) SubmitClientCallResult(_ context.Context, result provider.ClientCallResult) error {
	if s.submitHook != nil {
		return s.submitHook(result)
	}
	if s.submitResults != nil {
		*s.submitResults = append(*s.submitResults, result)
	}
	return nil
}

func (s stubProvider) CreateClientCollaboration(context.Context, provider.ClientCollaborationCreateRequest) (*provider.ClientCollaborationTask, error) {
	return &provider.ClientCollaborationTask{}, nil
}

func (s stubProvider) GetClientCollaboration(context.Context, string) (*provider.ClientCollaborationTask, error) {
	return &provider.ClientCollaborationTask{}, nil
}

func (s stubProvider) UpdateClientCollaboration(context.Context, string, provider.ClientCollaborationUpdateRequest) (*provider.ClientCollaborationTask, error) {
	return &provider.ClientCollaborationTask{}, nil
}

func streamWithEvents(events ...provider.StreamEvent) <-chan provider.StreamEvent {
	stream := make(chan provider.StreamEvent, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return stream
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

func TestPrepareSessionOverridesSavedRequestModeWhenRequested(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()
	state := agent.SessionState{FlowID: "demo-flow", Workdir: workdir, RequestMode: provider.RequestTypePlan}
	if err := app.sessionStore.Save(state); err != nil {
		t.Fatalf("save session failed: %v", err)
	}

	loaded, err := app.prepareSession("demo-flow", false, workdir, provider.RequestTypeAgent)
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.RequestMode != provider.RequestTypeAgent {
		t.Fatalf("expected override request mode %q, got %q", provider.RequestTypeAgent, loaded.RequestMode)
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

func TestApplyExplicitTaskSessionIDOverridesSessionAndClearsStaleTitle(t *testing.T) {
	updated := applyExplicitTaskSessionID(agent.SessionState{SessionID: "session-old", SessionTitle: "旧标题"}, " session-new ")
	if updated.SessionID != "session-new" {
		t.Fatalf("expected explicit session id override, got %#v", updated)
	}
	if updated.SessionTitle != "" {
		t.Fatalf("expected stale session title to be cleared, got %#v", updated)
	}

	unchanged := applyExplicitTaskSessionID(agent.SessionState{SessionID: "session-keep", SessionTitle: "保留标题"}, "session-keep")
	if unchanged.SessionTitle != "保留标题" {
		t.Fatalf("expected same session id to keep title, got %#v", unchanged)
	}
}

func TestStreamTaskDoesNotRetryDisconnectedEvent(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{stdout: &stdout, stderr: &stderr, configManager: manager, sessionStore: store}
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	config := internalconfig.Default()
	config.Workdir = t.TempDir()
	providerStub := &sequenceStreamProvider{streams: []<-chan provider.StreamEvent{
		streamWithEvents(provider.StreamEvent{Type: provider.EventError, Data: map[string]any{"error": "读取流失败: read tcp: connection reset by peer"}}),
	}}

	_, err = app.streamTask(
		context.Background(),
		providerStub,
		config,
		"demo-flow",
		"请处理任务",
		agent.SessionState{FlowID: "demo-flow", RequestMode: provider.RequestTypeAgent, ContextDir: config.Workdir, Workdir: config.Workdir},
		"",
		true,
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "流式连接已断开") {
		t.Fatalf("expected disconnected stream error, got %v", err)
	}
	if providerStub.calls != 1 {
		t.Fatalf("expected no CLI retry, got %d calls", providerStub.calls)
	}
	if strings.Contains(stdout.String(), "正在自动重试") {
		t.Fatalf("expected no retry progress output, got %q", stdout.String())
	}
}

func TestStreamTaskIncludesClientIdentityAndGeneratedRunID(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	providerStub := &stubProvider{
		stream:        streamWithEvents(provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{"message": "over"}}),
		streamSession: &provider.StreamSession{SessionID: "session-123"},
	}
	config := internalconfig.Default()
	config.Workdir = t.TempDir()
	config.ClientID = "cli-123"
	config.DeviceID = "device-456"

	_, err := app.streamTask(
		context.Background(),
		providerStub,
		config,
		"demo-flow",
		"请处理任务",
		agent.SessionState{FlowID: "demo-flow", RequestMode: provider.RequestTypeAgent, ContextDir: config.Workdir, Workdir: config.Workdir},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if providerStub.lastRequest.ClientID != "cli-123" {
		t.Fatalf("expected client_id to be forwarded, got %#v", providerStub.lastRequest)
	}
	if providerStub.lastRequest.DeviceID != "device-456" {
		t.Fatalf("expected device_id to be forwarded, got %#v", providerStub.lastRequest)
	}
	if strings.TrimSpace(providerStub.lastRequest.RunID) == "" {
		t.Fatalf("expected generated run_id, got %#v", providerStub.lastRequest)
	}
	if providerStub.lastRequest.ClientContextDir != resolveLocalWorkdir(config.Workdir) {
		t.Fatalf("expected client_context_dir to stay aligned with workdir, got %#v", providerStub.lastRequest)
	}
	if providerStub.lastRequest.ClientType != provider.ClientTypeCLI {
		t.Fatalf("expected client_type=%q, got %#v", provider.ClientTypeCLI, providerStub.lastRequest)
	}
	if providerStub.lastRequest.CommandExecutionTarget != "client" {
		t.Fatalf("expected command target client, got %#v", providerStub.lastRequest)
	}
}

func TestStreamTaskPersistsSessionLineage(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	providerStub := &stubProvider{
		stream: streamWithEvents(
			provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "子任务结果", "message_id": "message-789"}},
			provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{"message": "over"}},
		),
		streamSession: &provider.StreamSession{SessionID: "session-123"},
	}
	config := internalconfig.Default()
	config.Workdir = t.TempDir()
	config.ClientID = "cli-123"
	config.DeviceID = "device-456"

	_, err := app.streamTask(
		context.Background(),
		providerStub,
		config,
		"demo-flow",
		"请处理任务",
		agent.SessionState{FlowID: "demo-flow", RequestMode: provider.RequestTypeAgent, ContextDir: config.Workdir, Workdir: config.Workdir},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	loaded, err := app.sessionStore.Load("demo-flow", config.Workdir)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if loaded.SessionID != "session-123" {
		t.Fatalf("expected persisted session id, got %#v", loaded)
	}
	if loaded.LastRunID != providerStub.lastRequest.RunID {
		t.Fatalf("expected persisted last run id %q, got %#v", providerStub.lastRequest.RunID, loaded)
	}
	if loaded.LastMessageID != "message-789" {
		t.Fatalf("expected persisted last message id, got %#v", loaded)
	}
}

func TestResolveTaskRequestModeDefaultsToAgent(t *testing.T) {
	mode, err := resolveTaskRequestMode("")
	if err != nil {
		t.Fatalf("resolveTaskRequestMode returned error: %v", err)
	}
	if mode != provider.RequestTypeAgent {
		t.Fatalf("expected default task request mode %q, got %q", provider.RequestTypeAgent, mode)
	}
}

func TestResolveTaskRequestModeRejectsInvalidValue(t *testing.T) {
	if _, err := resolveTaskRequestMode("invalid"); err == nil {
		t.Fatal("expected invalid task request mode to return error")
	}
}

func TestResolveTaskReasoningMode(t *testing.T) {
	t.Run("empty keeps inherited", func(t *testing.T) {
		mode, err := resolveTaskReasoningMode("")
		if err != nil {
			t.Fatalf("resolveTaskReasoningMode returned error: %v", err)
		}
		if mode != "" {
			t.Fatalf("expected empty reasoning mode, got %q", mode)
		}
	})

	t.Run("accept on off", func(t *testing.T) {
		mode, err := resolveTaskReasoningMode("on")
		if err != nil || mode != "on" {
			t.Fatalf("expected on, got mode=%q err=%v", mode, err)
		}
		mode, err = resolveTaskReasoningMode("OFF")
		if err != nil || mode != "off" {
			t.Fatalf("expected off, got mode=%q err=%v", mode, err)
		}
	})

	t.Run("reject invalid", func(t *testing.T) {
		if _, err := resolveTaskReasoningMode("default"); err == nil {
			t.Fatal("expected invalid reasoning mode to return error")
		}
	})
}

func TestResolveTaskInternetSearchMode(t *testing.T) {
	t.Run("empty keeps inherited", func(t *testing.T) {
		mode, err := resolveTaskInternetSearchMode("")
		if err != nil {
			t.Fatalf("resolveTaskInternetSearchMode returned error: %v", err)
		}
		if mode != "" {
			t.Fatalf("expected empty internet search mode, got %q", mode)
		}
	})

	t.Run("accept on off", func(t *testing.T) {
		mode, err := resolveTaskInternetSearchMode("on")
		if err != nil || mode != "on" {
			t.Fatalf("expected on, got mode=%q err=%v", mode, err)
		}
		mode, err = resolveTaskInternetSearchMode("OFF")
		if err != nil || mode != "off" {
			t.Fatalf("expected off, got mode=%q err=%v", mode, err)
		}
	})

	t.Run("reject invalid", func(t *testing.T) {
		if _, err := resolveTaskInternetSearchMode("default"); err == nil {
			t.Fatal("expected invalid internet search mode to return error")
		}
	})
}

func TestEnsureDefaultFlowUsesServiceConfigDefaultFlowID(t *testing.T) {
	flowListCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/config"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"default_flow_id":"flow-from-config"}`)
		case backendAPIPath("/agents/flows"):
			flowListCalled = true
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"total":1,"offset":0,"data":[{"id":"flow-from-list","name":"列表流程"}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.DefaultFlow = ""
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	client := buildProviderClient(config)
	updatedConfig, err := app.ensureDefaultFlow(context.Background(), client, config)
	if err != nil {
		t.Fatalf("ensureDefaultFlow failed: %v", err)
	}
	if updatedConfig.DefaultFlow != "flow-from-config" {
		t.Fatalf("expected service default flow, got %#v", updatedConfig.DefaultFlow)
	}
	if flowListCalled {
		t.Fatal("expected flow list fallback not to be called when /config has default_flow_id")
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.DefaultFlow != "flow-from-config" {
		t.Fatalf("expected service default flow to persist, got %#v", reloaded.DefaultFlow)
	}
}

func TestRunTaskOnceRefreshesPersistentLoginBeforeStream(t *testing.T) {
	streamCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/users/whoami/userinfo"):
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(writer, `{"code":401,"message":"unauthorized"}`)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case authAPIPath("/sso/salt"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"salt-ticket","value":"salt-value"}}`)
		case authAPIPath("/sso"):
			if got := request.URL.Query().Get("persistent_token"); got != "persist-1" {
				t.Fatalf("unexpected persistent token: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","persistent_token":"persist-2","idle_duration":3600,"scope":"employee"}}`)
		case backendAPIPath("/version"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"package":"taiyiflow","main_version":"1.0.0"}`)
		case backendAPIPath("/config"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"default_flow_id":"flow-1"}`)
		case backendAPIPath("/session_id"):
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprintf(writer, `{"detail":"unexpected auth %s"}`, got)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"session_id":"session-created"}`)
		case backendAPIPath("/run/flow-1/stream"):
			streamCalled = true
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprintf(writer, `{"detail":"unexpected auth %s"}`, got)
				return
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "event: close\ndata: {}\n\n")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "expired-access"
	config.PersistentToken = "persist-1"
	config.DeviceID = "device-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	app := &App{
		configManager: manager,
		sessionStore:  store,
		stdout:        io.Discard,
		stderr:        io.Discard,
		stdin:         strings.NewReader(""),
		runtime:       clientruntime.NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard),
	}

	if err := app.runTaskOnce(context.Background(), rootOptions{}, "测试任务"); err != nil {
		t.Fatalf("runTaskOnce failed: %v", err)
	}
	if !streamCalled {
		t.Fatal("expected stream endpoint to be called")
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-2" {
		t.Fatalf("expected refreshed auth state to persist, got %#v", reloaded)
	}
}

func TestRunTaskStreamRetryFlagOverridesSystemConfig(t *testing.T) {
	streamCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/users/whoami/userinfo"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case backendAPIPath("/version"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"package":"taiyiflow","main_version":"1.0.0"}`)
		case backendAPIPath("/config"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"default_flow_id":"flow-1"}`)
		case backendAPIPath("/session_id"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"session_id":"session-created"}`)
		case backendAPIPath("/run/flow-1/stream"):
			streamCalls++
			writer.Header().Set("Content-Type", "text/event-stream")
			if streamCalls < 3 {
				fmt.Fprint(writer, "event: error\ndata: {\"error\":\"读取流失败: read tcp: connection reset by peer\"}\n\n")
				return
			}
			fmt.Fprint(writer, "event: close\ndata: {}\n\n")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "access-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	app := &App{
		configManager: manager,
		sessionStore:  store,
		stdout:        &stdout,
		stderr:        io.Discard,
		stdin:         strings.NewReader(""),
		runtime:       clientruntime.NewClientRuntime(strings.NewReader(""), &stdout, io.Discard),
	}

	err = app.runTask(context.Background(), []string{"--stream-retry-attempts", "2", "--show-stream-events", "测试任务"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("expected removed stream retry flag to be rejected, got %v", err)
	}
	if streamCalls != 0 {
		t.Fatalf("expected removed flag to fail before issuing stream request, got %d calls", streamCalls)
	}
}

func TestRunTaskCrossSessionMemoryConfigAndFlagPrecedence(t *testing.T) {
	testCases := []struct {
		name       string
		configured bool
		args       []string
		expected   bool
	}{
		{name: "inherit configured true", configured: true, args: []string{"测试任务"}, expected: true},
		{name: "flag true overrides configured false", configured: false, args: []string{"--cross-session-memory-enabled=true", "测试任务"}, expected: true},
		{name: "flag false overrides configured true", configured: true, args: []string{"--cross-session-memory-enabled=false", "测试任务"}, expected: false},
	}

	for _, item := range testCases {
		t.Run(item.name, func(t *testing.T) {
			captured := runTaskAndCaptureStreamBody(t, item.configured, item.args)
			value, ok := captured["cross_session_memory_enabled"].(bool)
			if !ok {
				t.Fatalf("expected cross_session_memory_enabled bool in request body, got %#v", captured["cross_session_memory_enabled"])
			}
			if value != item.expected {
				t.Fatalf("expected cross_session_memory_enabled=%v, got %v", item.expected, value)
			}
		})
	}
}

func TestRunTaskRejectsNewWithSessionID(t *testing.T) {
	app := &App{stderr: io.Discard}
	err := app.runTask(context.Background(), []string{"--new", "--session-id", "session-1", "测试任务"})
	if err == nil {
		t.Fatal("expected conflicting --new and --session-id flags to fail")
	}
	if !strings.Contains(err.Error(), "--new 与 --session-id 不能同时使用") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStreamTaskSendsEnvironmentInTweaks(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	app.stdout = io.Discard
	app.stderr = io.Discard
	app.runtime = clientruntime.NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	workdir := t.TempDir()

	stream := make(chan provider.StreamEvent, 1)
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
	close(stream)

	providerStub := &stubProvider{stream: stream}
	_, err := app.streamTask(
		context.Background(),
		providerStub,
		internalconfig.Config{Workdir: workdir, DocUnstructuredExts: []string{"pdf", "docx"}},
		"demo-flow",
		"demo task",
		agent.SessionState{RequestMode: provider.RequestTypeAgent, ContextDir: workdir},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if providerStub.lastRequest.Input != "demo task" {
		t.Fatalf("expected original task input to remain unchanged, got %q", providerStub.lastRequest.Input)
	}
	environmentHint, ok := providerStub.lastRequest.Tweaks["taiyiflow_environment"].(string)
	if !ok {
		t.Fatalf("expected taiyiflow_environment tweak string, got %#v", providerStub.lastRequest.Tweaks)
	}
	for _, expected := range []string{"<taiyiflow_environment>", "OS:", "ARCH:", "工作目录:", workdir, "命令执行目标: client", "demo task"} {
		if expected == "demo task" {
			continue
		}
		if !strings.Contains(environmentHint, expected) {
			t.Fatalf("expected environment hint to contain %q, got %q", expected, environmentHint)
		}
	}
	for _, unexpected := range []string{"shell_exec", "command + args", "rawCommand", "fork/exec ... not supported", ".pdf", ".docx", "local.document_unstructured"} {
		if strings.Contains(environmentHint, unexpected) {
			t.Fatalf("expected environment hint to omit %q, got %q", unexpected, environmentHint)
		}
	}
	if strings.Contains(environmentHint, "demo task") {
		t.Fatalf("expected task body to stay out of environment tweak, got %q", environmentHint)
	}
}

func TestLoadSessionPickerItemsSortsByRecentAtDesc(t *testing.T) {
	now := time.Date(2026, time.May, 11, 12, 0, 0, 0, time.UTC)
	providerStub := &sessionListProvider{responses: map[int]*provider.SessionList{
		0: {
			Total: 3,
			Items: []provider.SessionSummary{
				{ID: "session-old", Name: "old", UpdatedAt: now.Add(-2 * time.Hour), CreatedAt: now.Add(-24 * time.Hour)},
				{ID: "session-new", Name: "new", UpdatedAt: now.Add(-1 * time.Hour), CreatedAt: now.Add(-25 * time.Hour)},
			},
		},
		2: {
			Total: 3,
			Items: []provider.SessionSummary{
				{ID: "session-mid", Name: "mid", UpdatedAt: now.Add(-90 * time.Minute), CreatedAt: now.Add(-26 * time.Hour)},
			},
		},
	}}

	items, err := loadSessionPickerItems(context.Background(), providerStub, "flow-1", "session-old")
	if err != nil {
		t.Fatalf("loadSessionPickerItems failed: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %#v", items)
	}
	orderedIDs := []string{items[0].ID, items[1].ID, items[2].ID}
	expectedIDs := []string{"session-new", "session-mid", "session-old"}
	for index, expected := range expectedIDs {
		if orderedIDs[index] != expected {
			t.Fatalf("expected sorted session ids %v, got %v", expectedIDs, orderedIDs)
		}
	}
	if !items[2].IsCurrent {
		t.Fatalf("expected current session marker to be preserved, got %#v", items)
	}
}

func TestLoadSessionPickerItemsUsesProviderSessionTitles(t *testing.T) {
	now := time.Date(2026, time.May, 11, 12, 0, 0, 0, time.UTC)
	providerStub := &sessionListProvider{responses: map[int]*provider.SessionList{
		0: {
			Total: 2,
			Items: []provider.SessionSummary{
				{ID: "session-1", Name: "服务端标题", UpdatedAt: now.Add(-1 * time.Hour)},
				{ID: "session-2", Name: "原始标题", UpdatedAt: now.Add(-2 * time.Hour)},
			},
		},
	}}

	items, err := loadSessionPickerItems(context.Background(), providerStub, "flow-1", "session-2")
	if err != nil {
		t.Fatalf("loadSessionPickerItems failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %#v", items)
	}
	if items[0].Title != "服务端标题" {
		t.Fatalf("expected provider title to be preserved, got %#v", items[0])
	}
	if items[1].Title != "原始标题" || !items[1].IsCurrent {
		t.Fatalf("expected provider title and current flag to be preserved, got %#v", items[1])
	}
}

func runTaskAndCaptureStreamBody(t *testing.T, crossSessionMemoryEnabled bool, args []string) map[string]any {
	t.Helper()
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/users/whoami/userinfo"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case backendAPIPath("/version"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"package":"taiyiflow","main_version":"1.0.0"}`)
		case backendAPIPath("/config"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"default_flow_id":"flow-1"}`)
		case backendAPIPath("/session_id"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"session_id":"session-created"}`)
		case backendAPIPath("/run/flow-1/stream"):
			if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
				t.Fatalf("decode stream request failed: %v", err)
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "event: close\ndata: {}\n\n")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "access-1"
	config.CrossSessionMemoryEnabled = crossSessionMemoryEnabled
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	app := &App{
		configManager: manager,
		sessionStore:  store,
		stdout:        &stdout,
		stderr:        io.Discard,
		stdin:         strings.NewReader(""),
		runtime:       clientruntime.NewClientRuntime(strings.NewReader(""), &stdout, io.Discard),
	}
	if err := app.runTask(context.Background(), args); err != nil {
		t.Fatalf("runTask failed: %v", err)
	}
	if captured == nil {
		t.Fatal("expected stream request body to be captured")
	}
	return captured
}

func capturedHasClientTool(captured map[string]any, toolName string) bool {
	servers, ok := captured["client_mcp_servers"].(map[string]any)
	if !ok {
		return false
	}
	entries, ok := servers["local"].([]any)
	if !ok {
		return false
	}
	for _, entry := range entries {
		item, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := item["name"].(string)
		if strings.TrimSpace(name) == toolName {
			return true
		}
	}
	return false
}

func TestUploadInputFileResolvesRelativePathAndReturnsServerPath(t *testing.T) {
	baseDir := t.TempDir()
	localDir := filepath.Join(baseDir, "docs")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	localFile := filepath.Join(localDir, "demo.txt")
	if err := os.WriteFile(localFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	var gotFlowID string
	var gotSessionID string
	var gotLocalPath string
	var gotRemotePath string
	filePath, err := uploadInputFile(context.Background(), &stubProvider{uploadHook: func(_ context.Context, flowID string, sessionID string, localPath string, remotePath string) (*provider.UploadResult, error) {
		gotFlowID = flowID
		gotSessionID = sessionID
		gotLocalPath = localPath
		gotRemotePath = remotePath
		return &provider.UploadResult{FilePath: "flow-1/session-1/demo.txt"}, nil
	}}, "flow-1", "session-1", baseDir, filepath.Join("docs", "demo.txt"))
	if err != nil {
		t.Fatalf("uploadInputFile failed: %v", err)
	}
	if filePath != "flow-1/session-1/demo.txt" {
		t.Fatalf("unexpected uploaded file path: %q", filePath)
	}
	if gotFlowID != "flow-1" || gotSessionID != "session-1" {
		t.Fatalf("unexpected upload identifiers: flow=%q session=%q", gotFlowID, gotSessionID)
	}
	if gotLocalPath != localFile {
		t.Fatalf("expected absolute local path %q, got %q", localFile, gotLocalPath)
	}
	if gotRemotePath != "" {
		t.Fatalf("expected empty remote path, got %q", gotRemotePath)
	}
}

func TestRunTaskWithFileUploadsAndForwardsInputFiles(t *testing.T) {
	var captured map[string]any
	uploadCalled := false
	tempWorkdir := t.TempDir()
	localFile := filepath.Join(tempWorkdir, "notes.txt")
	if err := os.WriteFile(localFile, []byte("note"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/users/whoami/userinfo"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case backendAPIPath("/session_id"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"session_id":"session-created"}`)
		case backendAPIPath("/version"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"package":"taiyiflow","main_version":"1.0.0"}`)
		case backendAPIPath("/config"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"default_flow_id":"flow-1"}`)
		case backendAPIPath("/agents/upload/flow-1"):
			uploadCalled = true
			if got := request.FormValue("session_id"); got != "session-created" {
				t.Fatalf("expected generated session_id for first upload, got %q", got)
			}
			file, header, err := request.FormFile("file")
			if err != nil {
				t.Fatalf("read uploaded file failed: %v", err)
			}
			defer file.Close()
			payload, err := io.ReadAll(file)
			if err != nil {
				t.Fatalf("read upload payload failed: %v", err)
			}
			if header.Filename != "notes.txt" {
				t.Fatalf("unexpected uploaded filename: %q", header.Filename)
			}
			if string(payload) != "note" {
				t.Fatalf("unexpected uploaded payload: %q", string(payload))
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"flow_id":"flow-1","file_path":"flow-1/session-created/notes.txt"}`)
		case backendAPIPath("/run/flow-1/stream"):
			if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
				t.Fatalf("decode stream request failed: %v", err)
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "event: close\ndata: {}\n\n")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "access-1"
	config.Workdir = tempWorkdir
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	app := &App{
		configManager: manager,
		sessionStore:  store,
		stdout:        &stdout,
		stderr:        io.Discard,
		stdin:         strings.NewReader(""),
		runtime:       clientruntime.NewClientRuntime(strings.NewReader(""), &stdout, io.Discard),
	}

	if err := app.runTask(context.Background(), []string{"--file", "notes.txt", "分析这个文件"}); err != nil {
		t.Fatalf("runTask failed: %v", err)
	}
	if !uploadCalled {
		t.Fatal("expected upload endpoint to be called")
	}
	if got, _ := captured["session_id"].(string); got != "session-created" {
		t.Fatalf("expected generated session_id to be reused by stream, got %#v", captured["session_id"])
	}
	inputFiles, ok := captured["files"].([]any)
	if !ok || len(inputFiles) != 1 || inputFiles[0] != "flow-1/session-created/notes.txt" {
		t.Fatalf("unexpected files payload: %#v", captured["files"])
	}
}

func TestRunTaskWithExplicitSessionIDUsesUploadAndStreamSession(t *testing.T) {
	var captured map[string]any
	tempWorkdir := t.TempDir()
	localFile := filepath.Join(tempWorkdir, "notes.txt")
	if err := os.WriteFile(localFile, []byte("note"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/users/whoami/userinfo"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case backendAPIPath("/version"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"package":"taiyiflow","main_version":"1.0.0"}`)
		case backendAPIPath("/config"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"default_flow_id":"flow-1"}`)
		case backendAPIPath("/agents/upload/flow-1"):
			if got := request.FormValue("session_id"); got != "session-explicit" {
				t.Fatalf("expected upload session_id to use explicit value, got %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"flow_id":"flow-1","file_path":"flow-1/session-explicit/notes.txt"}`)
		case backendAPIPath("/run/flow-1/stream"):
			if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
				t.Fatalf("decode stream request failed: %v", err)
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "event: close\ndata: {}\n\n")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "access-1"
	config.Workdir = tempWorkdir
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	app := &App{
		configManager: manager,
		sessionStore:  store,
		stdout:        &stdout,
		stderr:        io.Discard,
		stdin:         strings.NewReader(""),
		runtime:       clientruntime.NewClientRuntime(strings.NewReader(""), &stdout, io.Discard),
	}

	if err := app.runTask(context.Background(), []string{"--session-id", "session-explicit", "--file", "notes.txt", "分析这个文件"}); err != nil {
		t.Fatalf("runTask failed: %v", err)
	}
	if got, _ := captured["session_id"].(string); got != "session-explicit" {
		t.Fatalf("expected stream session_id to use explicit value, got %#v", captured["session_id"])
	}
	inputFiles, ok := captured["files"].([]any)
	if !ok || len(inputFiles) != 1 || inputFiles[0] != "flow-1/session-explicit/notes.txt" {
		t.Fatalf("unexpected files payload: %#v", captured["files"])
	}
}

func TestCallbackAuthRefreshProviderRetriesSubmitAfterUnauthorized(t *testing.T) {
	callbackCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/agents/client/call/result"):
			callbackCalls++
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(writer, `{"message":"未登录或会话令牌已失效","code":401}`)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{}`)
		case authAPIPath("/sso/salt"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"salt-ticket","value":"salt-value"}}`)
		case authAPIPath("/sso"):
			if got := request.URL.Query().Get("persistent_token"); got != "persist-1" {
				t.Fatalf("unexpected persistent token: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","persistent_token":"persist-2","idle_duration":3600,"scope":"employee"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "expired-access"
	config.PersistentToken = "persist-1"
	config.DeviceID = "device-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	app := &App{configManager: manager}
	client := buildProviderClient(config)
	callbackProvider := app.providerWithCallbackAuthRefresh(client, &config)

	err = callbackProvider.SubmitClientCallResult(context.Background(), provider.ClientCallResult{
		FlowID:    "flow-1",
		SessionID: "session-1",
		MessageID: "message-1",
		RunID:     "run-1",
		CallID:    "call-1",
		Success:   true,
		Result:    map[string]any{"ok": true},
	})
	if err != nil {
		t.Fatalf("SubmitClientCallResult failed: %v", err)
	}
	if callbackCalls != 2 {
		t.Fatalf("expected one failed callback and one retry, got %d calls", callbackCalls)
	}
	if config.AccessToken != "access-1" || config.PersistentToken != "persist-2" {
		t.Fatalf("expected in-memory config refreshed, got %#v", config)
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-2" {
		t.Fatalf("expected refreshed auth state to persist, got %#v", reloaded)
	}
}

func TestCallbackAuthRefreshProviderRetriesImageUnderstandingAfterUnauthorized(t *testing.T) {
	imageCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/run/image-flow"):
			imageCalls++
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(writer, `{"message":"未登录或会话令牌已失效","code":401}`)
				return
			}
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			writer.Header().Set(providerclient.SessionHeader, "session-image-2")
			fmt.Fprint(writer, "图片中央显示登录框")
		case authAPIPath("/sso/salt"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"salt-ticket","value":"salt-value"}}`)
		case authAPIPath("/sso"):
			if got := request.URL.Query().Get("persistent_token"); got != "persist-1" {
				t.Fatalf("unexpected persistent token: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","persistent_token":"persist-2","idle_duration":3600,"scope":"employee"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "expired-access"
	config.PersistentToken = "persist-1"
	config.DeviceID = "device-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	app := &App{configManager: manager}
	client := buildProviderClient(config)
	callbackProvider := app.providerWithCallbackAuthRefresh(client, &config)
	runner, ok := callbackProvider.(provider.ImageUnderstandingRunner)
	if !ok {
		t.Fatalf("expected callback provider to expose image understanding runner, got %T", callbackProvider)
	}

	result, err := runner.RunImageUnderstanding(context.Background(), provider.ImageUnderstandingRequest{
		FlowID:     "image-flow",
		Input:      "请描述当前截图",
		InputType:  "any",
		OutputType: "any",
		SessionID:  "session-image-1",
		Tweaks: map[string]any{
			"TextOutput-sMWXv": map[string]any{"output_format": "text"},
		},
	})
	if err != nil {
		t.Fatalf("RunImageUnderstanding failed: %v", err)
	}
	if imageCalls != 2 {
		t.Fatalf("expected one failed image request and one retry, got %d calls", imageCalls)
	}
	if result == nil || result.Text != "图片中央显示登录框" || result.SessionID != "session-image-2" {
		t.Fatalf("unexpected image understanding result: %#v", result)
	}
	if config.AccessToken != "access-1" || config.PersistentToken != "persist-2" {
		t.Fatalf("expected in-memory config refreshed, got %#v", config)
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-2" {
		t.Fatalf("expected refreshed auth state to persist, got %#v", reloaded)
	}
}

func TestCallbackAuthRefreshProviderRetriesStreamAfterUnauthorized(t *testing.T) {
	streamCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/session_id"):
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(writer, `{"message":"未登录或会话令牌已失效","code":401}`)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"session_id":"session-created"}`)
		case backendAPIPath("/run/flow-1/stream"):
			streamCalls++
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(writer, `{"message":"未登录或会话令牌已失效","code":401}`)
				return
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "event: close\ndata: {}\n\n")
		case authAPIPath("/sso/salt"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"salt-ticket","value":"salt-value"}}`)
		case authAPIPath("/sso"):
			if got := request.URL.Query().Get("persistent_token"); got != "persist-1" {
				t.Fatalf("unexpected persistent token: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","persistent_token":"persist-2","idle_duration":3600,"scope":"employee"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "expired-access"
	config.PersistentToken = "persist-1"
	config.DeviceID = "device-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	app := &App{configManager: manager}
	client := buildProviderClient(config)
	callbackProvider := app.providerWithCallbackAuthRefresh(client, &config)

	stream, _, err := callbackProvider.Stream(context.Background(), provider.StreamRequest{
		FlowID:      "flow-1",
		Input:       "测试任务",
		InputType:   "any",
		OutputType:  "any",
		RequestType: provider.RequestTypeAgent,
	})
	if err != nil {
		t.Fatalf("Stream failed: %v", err)
	}
	for range stream {
	}
	if streamCalls != 1 {
		t.Fatalf("expected stream request to run once after session bootstrap refresh, got %d calls", streamCalls)
	}
	if config.AccessToken != "access-1" || config.PersistentToken != "persist-2" {
		t.Fatalf("expected in-memory config refreshed, got %#v", config)
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-2" {
		t.Fatalf("expected refreshed auth state to persist, got %#v", reloaded)
	}
}

func TestCallbackAuthRefreshProviderRetriesListSessionsAfterUnauthorized(t *testing.T) {
	sessionCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/agents/flows/flow-1/sessions"):
			sessionCalls++
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(writer, `{"message":"未登录或会话令牌已失效","code":401}`)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"total":1,"offset":0,"data":[{"id":"session-1","session_name":"会话一","created_at":"2026-05-11T08:00:00Z","updated_at":"2026-05-11T09:00:00Z","client_type":"cli"}]}`)
		case authAPIPath("/sso/salt"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"salt-ticket","value":"salt-value"}}`)
		case authAPIPath("/sso"):
			if got := request.URL.Query().Get("persistent_token"); got != "persist-1" {
				t.Fatalf("unexpected persistent token: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","persistent_token":"persist-2","idle_duration":3600,"scope":"employee"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "expired-access"
	config.PersistentToken = "persist-1"
	config.DeviceID = "device-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	app := &App{configManager: manager}
	client := buildProviderClient(config)
	callbackProvider := app.providerWithCallbackAuthRefresh(client, &config)

	sessions, err := callbackProvider.ListSessions(context.Background(), "flow-1", 0, 10)
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}
	if sessionCalls != 2 {
		t.Fatalf("expected one failed sessions request and one retry, got %d calls", sessionCalls)
	}
	if sessions == nil || len(sessions.Items) != 1 || sessions.Items[0].ID != "session-1" {
		t.Fatalf("unexpected sessions response: %#v", sessions)
	}
	if config.AccessToken != "access-1" || config.PersistentToken != "persist-2" {
		t.Fatalf("expected in-memory config refreshed, got %#v", config)
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-2" {
		t.Fatalf("expected refreshed auth state to persist, got %#v", reloaded)
	}
}

func TestRunInteractiveSessionConfigCommandPersistsAgentAutoLoopMaxRoundsOverride(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()
	state := agent.SessionState{FlowID: "demo-flow", Workdir: workdir, RequestMode: provider.RequestTypeAgent}

	output, err := app.runInteractiveSessionConfigCommand("/config session set agent_auto_loop_max_rounds 5", &state)
	if err != nil {
		t.Fatalf("set agent_auto_loop_max_rounds failed: %v", err)
	}
	if output != "已设置会话智能体自动续跑最大轮数: 5" {
		t.Fatalf("unexpected output: %q", output)
	}

	loaded, err := app.prepareSession("demo-flow", false, workdir, provider.RequestTypeAgent)
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.AgentAutoLoopMaxRoundsOverride == nil || *loaded.AgentAutoLoopMaxRoundsOverride != 5 {
		t.Fatalf("expected saved auto loop max rounds override 5, got %#v", loaded.AgentAutoLoopMaxRoundsOverride)
	}

	output, err = app.runInteractiveSessionConfigCommand("/config session set agent_auto_loop_max_rounds 0", &state)
	if err != nil {
		t.Fatalf("set agent_auto_loop_max_rounds 0 failed: %v", err)
	}
	if output != "已设置会话智能体自动续跑最大轮数: 0" {
		t.Fatalf("unexpected output: %q", output)
	}
	if state.AgentAutoLoopMaxRoundsOverride == nil || *state.AgentAutoLoopMaxRoundsOverride != 0 {
		t.Fatalf("expected in-memory auto loop max rounds override 0, got %#v", state.AgentAutoLoopMaxRoundsOverride)
	}
}

func TestResolveAgentAutoLoopEnabledDerivedFromMaxRounds(t *testing.T) {
	config := internalconfig.Default()
	config.AgentAutoLoopMaxRounds = 5
	zero := 0
	resolved := resolveAgentAutoLoopEnabled(config, agent.SessionState{AgentAutoLoopMaxRoundsOverride: &zero}, provider.RequestTypeAgent)
	if resolved == nil {
		t.Fatal("expected explicit auto loop state to be sent")
	}
	if *resolved {
		t.Fatal("expected zero max rounds to disable auto loop")
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

func TestRunFallsBackToTaskWhenSubcommandDoesNotMatch(t *testing.T) {
	app := &App{stderr: io.Discard}
	runErr := app.Run(context.Background(), []string{"--unknown-flag"})
	taskErr := app.runTask(context.Background(), []string{"--unknown-flag"})
	if runErr == nil || taskErr == nil {
		t.Fatalf("expected both Run and runTask to return flag parse errors, got run=%v task=%v", runErr, taskErr)
	}
	if runErr.Error() != taskErr.Error() {
		t.Fatalf("expected Run fallback error %q to match runTask error %q", runErr, taskErr)
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "[系统提示]: 准备开始\n" {
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "正式输出\n" {
		t.Fatalf("expected sanitized stdout, got %q", got)
	}
}

func TestStreamTaskUpdatesSessionTitleFromStream(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)
	workdir := t.TempDir()

	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"session_name": "新会话标题"}}
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
	close(stream)

	state, err := app.streamTask(
		context.Background(),
		&stubProvider{stream: stream, streamSession: &provider.StreamSession{SessionID: "session-1"}},
		internalconfig.Config{Workdir: workdir},
		"demo-flow",
		"demo task",
		agent.SessionState{RequestMode: provider.RequestTypeAgent, Workdir: workdir},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if state.SessionTitle != "新会话标题" {
		t.Fatalf("expected session title updated from stream, got %q", state.SessionTitle)
	}

	loaded, err := app.prepareSession("demo-flow", false, workdir, provider.RequestTypeAgent)
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.SessionTitle != "新会话标题" {
		t.Fatalf("expected persisted session title, got %q", loaded.SessionTitle)
	}
}

func TestStreamTaskCreatesSessionIDBeforeFirstStream(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)
	workdir := t.TempDir()

	stream := make(chan provider.StreamEvent, 1)
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
	close(stream)

	providerStub := &stubProvider{stream: stream}
	state, err := app.streamTask(
		context.Background(),
		providerStub,
		internalconfig.Config{Workdir: workdir},
		"demo-flow",
		"demo task",
		agent.SessionState{RequestMode: provider.RequestTypeAgent, Workdir: workdir},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if providerStub.lastRequest.SessionID != "session-created" {
		t.Fatalf("expected generated session_id in stream request, got %#v", providerStub.lastRequest)
	}
	if state.SessionID != "session-created" {
		t.Fatalf("expected returned session state to keep generated session_id, got %#v", state)
	}
}

func TestStreamTaskSplitsAssistantOutputWhenBackendRequestsNewMessage(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 4)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "第一条"}}
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"split_assistant_message": true}}
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "第二条"}}
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "第一条\n第二条\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestStreamTaskDoesNotShowToolCallStatusWhenStreamEventsDisabled(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventToolCall, Data: map[string]any{"text": "internet search starting"}}
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "" {
		t.Fatalf("expected no tool call status when stream events disabled, got %q", got)
	}
}

func TestStreamTaskRejectsAskUserWhenInteractivePromptsDisabled(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(strings.NewReader("should-not-be-read\n"), &stdout, &stderr)
	app.runtime.SetInteractivePromptsEnabled(false)

	results := []provider.ClientCallResult{}
	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventClientTool, Data: map[string]any{
		"flow_id": "demo-flow",
		"context": map[string]any{
			"session_id": "session-1",
			"run_id":     "run-1",
			"message_id": "message-1",
		},
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"id":   "call-1",
			"name": "ask_user",
			"arguments": map[string]any{
				"questions": []any{map[string]any{"name": "value", "label": "请输入值"}},
			},
		},
	}}
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
	close(stream)

	_, err := app.streamTask(
		context.Background(),
		&stubProvider{stream: stream, submitResults: &results},
		internalconfig.Config{},
		"demo-flow",
		"demo task",
		agent.SessionState{RequestMode: provider.RequestTypeAgent},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one client callback result, got %#v", results)
	}
	if results[0].Success {
		t.Fatalf("expected ask_user callback to fail when prompts disabled, got %#v", results[0])
	}
	if !strings.Contains(results[0].ErrorMessage, "task 模式不支持 ask_user") {
		t.Fatalf("expected task-mode ask_user rejection, got %#v", results[0])
	}
	if strings.Contains(stdout.String(), "[ask_user]") || strings.Contains(stdout.String(), "请输入答案") {
		t.Fatalf("expected no ask_user prompt output, got %q", stdout.String())
	}
}

func TestRunTaskOnceRejectsAskUserWithoutPrompting(t *testing.T) {
	var callbackBody struct {
		Success      bool   `json:"success"`
		ErrorMessage string `json:"error_message"`
	}
	callbackCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/users/whoami/userinfo"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case backendAPIPath("/version"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"package":"taiyiflow","main_version":"1.0.0"}`)
		case backendAPIPath("/session_id"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"session_id":"session-created"}`)
		case backendAPIPath("/run/flow-1/stream"):
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "event: client_tool\n")
			fmt.Fprint(writer, "data: {\"flow_id\":\"flow-1\",\"context\":{\"session_id\":\"session-created\",\"run_id\":\"run-1\",\"message_id\":\"message-1\"},\"server\":{\"name\":\"local\"},\"function\":{\"id\":\"call-1\",\"name\":\"ask_user\",\"arguments\":{\"questions\":[{\"name\":\"value\",\"label\":\"请输入值\"}]}}}\n\n")
			fmt.Fprint(writer, "event: close\ndata: {}\n\n")
		case backendAPIPath("/agents/client/call/result"):
			callbackCalled = true
			defer request.Body.Close()
			if err := json.NewDecoder(request.Body).Decode(&callbackBody); err != nil {
				t.Fatalf("decode callback body failed: %v", err)
			}
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.DefaultFlow = "flow-1"
	config.AccessToken = "access-1"
	config.ApprovalMode = "auto"
	config.PermissionRules = []internalconfig.PermissionRule{{Action: "allow", ToolName: "shell_exec"}}
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{
		configManager: manager,
		sessionStore:  store,
		stdout:        &stdout,
		stderr:        &stderr,
		stdin:         strings.NewReader("should-not-be-read\n"),
		runtime:       clientruntime.NewClientRuntime(strings.NewReader("should-not-be-read\n"), &stdout, &stderr),
	}

	if err := app.runTaskOnce(context.Background(), rootOptions{}, "测试任务"); err != nil {
		t.Fatalf("runTaskOnce failed: %v", err)
	}
	if !callbackCalled {
		t.Fatal("expected client callback to be submitted")
	}
	if callbackBody.Success {
		t.Fatalf("expected task-mode ask_user callback to fail, got %#v", callbackBody)
	}
	if !strings.Contains(callbackBody.ErrorMessage, "task 模式不支持 ask_user") {
		t.Fatalf("expected task-mode ask_user rejection, got %#v", callbackBody)
	}
	if strings.Contains(stdout.String(), "[ask_user]") || strings.Contains(stdout.String(), "请输入答案") {
		t.Fatalf("expected no ask_user prompt output in task mode, got %q", stdout.String())
	}
}

func TestRunTaskOnceDoesNotForwardHostStdinToShellExec(t *testing.T) {
	if os.Getenv("TAIYIFLOW_HELPER_PROCESS") == "stdin-echo" {
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			_, _ = os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(content)
		os.Exit(0)
	}

	var callbackBody struct {
		Success      bool           `json:"success"`
		ErrorMessage string         `json:"error_message"`
		Result       map[string]any `json:"result"`
	}
	callbackCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/users/whoami/userinfo"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case backendAPIPath("/version"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"package":"taiyiflow","main_version":"1.0.0"}`)
		case backendAPIPath("/session_id"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"session_id":"session-created"}`)
		case backendAPIPath("/run/flow-1/stream"):
			writer.Header().Set("Content-Type", "text/event-stream")
			payload := fmt.Sprintf(`{"flow_id":"flow-1","context":{"session_id":"session-created","run_id":"run-1","message_id":"message-1"},"function":{"id":"call-1"},"server":{"name":"built_in_command"},"command":{"name":"run_command_via_client","arguments":{"command":%q,"args":["-test.run=TestRunTaskOnceDoesNotForwardHostStdinToShellExec$"],"env":{"TAIYIFLOW_HELPER_PROCESS":"stdin-echo"}},"policy":{"confirmation_required":false}}}`, os.Args[0])
			fmt.Fprint(writer, "event: built_in\n")
			fmt.Fprintf(writer, "data: %s\n\n", payload)
			fmt.Fprint(writer, "event: close\ndata: {}\n\n")
		case backendAPIPath("/agents/client/call/result"):
			callbackCalled = true
			defer request.Body.Close()
			if err := json.NewDecoder(request.Body).Decode(&callbackBody); err != nil {
				t.Fatalf("decode callback body failed: %v", err)
			}
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.DefaultFlow = "flow-1"
	config.AccessToken = "access-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{
		configManager: manager,
		sessionStore:  store,
		stdout:        &stdout,
		stderr:        &stderr,
		stdin:         strings.NewReader("host stdin should not be forwarded\n"),
		runtime:       clientruntime.NewClientRuntime(strings.NewReader("host stdin should not be forwarded\n"), &stdout, &stderr),
	}

	if err := app.runTaskOnce(context.Background(), rootOptions{}, "测试任务"); err != nil {
		t.Fatalf("runTaskOnce failed: %v", err)
	}
	if !callbackCalled {
		t.Fatal("expected client callback to be submitted")
	}
	if !callbackBody.Success {
		t.Fatalf("expected shell_exec callback success, got %#v", callbackBody)
	}
	stdoutValue, _ := callbackBody.Result["stdout"].(string)
	if stdoutValue != "" {
		t.Fatalf("expected task mode not to forward host stdin, got %q", stdoutValue)
	}
}

func TestRunTaskDoesNotAdvertiseAskUserTool(t *testing.T) {
	for _, item := range []struct {
		name string
		args []string
	}{
		{name: "default agent", args: []string{"测试任务"}},
		{name: "ask mode", args: []string{"--mode", "ask", "测试任务"}},
		{name: "plan mode", args: []string{"--mode", "plan", "测试任务"}},
	} {
		t.Run(item.name, func(t *testing.T) {
			captured := runTaskAndCaptureStreamBody(t, false, item.args)
			if capturedHasClientTool(captured, "ask_user") {
				t.Fatalf("expected task stream request not to advertise ask_user, got %#v", captured["client_mcp_servers"])
			}
		})
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

func TestStreamTaskHidesThinkTagsByDefault(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 3)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "开始<think>内部推理"}}
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "继续推理</think>结论"}}
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "开始结论\n" {
		t.Fatalf("expected think content hidden by default, got %q", got)
	}
}

func TestStreamTaskShowsThinkContentWithoutTagsWhenEnabled(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "开始<think>内部推理</think>结论"}}
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
		true,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "开始内部推理结论\n" {
		t.Fatalf("expected think content shown without tags when enabled, got %q", got)
	}
}

func TestStreamTaskDropsSplitClosingThinkTagWhenEnabled(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 3)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "开始<think>内部推理</thi"}}
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "nk>结论"}}
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
		true,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "开始内部推理结论\n" {
		t.Fatalf("expected split closing tag hidden when showThinking enabled, got %q", got)
	}
}

func TestCLIStreamRenderStateRewritesSplitCitationMarkers(t *testing.T) {
	state := cliStreamRenderState{}

	if got := state.renderChunk("结论 ^1.", false); got != "结论 " {
		t.Fatalf("expected citation prefix to be buffered, got %q", got)
	}
	if got := state.renderChunk("2^ 与 [^2.", false); got != "[联网#2] 与 " {
		t.Fatalf("expected internet citation to be rewritten, got %q", got)
	}
	if got := state.renderChunk("1^]", false); got != "[共享#1]" {
		t.Fatalf("expected shared citation to be rewritten, got %q", got)
	}
	if got := state.flush(false); got != "" {
		t.Fatalf("expected no buffered citation text after flush, got %q", got)
	}
}

func TestStreamTaskShowsAndClearsStreamingEllipsisInCLI(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	stdout := &fakeTTYWriter{fd: 1}
	var stderr bytes.Buffer
	app.stdout = stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), stdout, &stderr)

	previous := isTerminalFD
	isTerminalFD = func(fd int) bool {
		return fd == 1
	}
	defer func() {
		isTerminalFD = previous
	}()

	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "答案 ^1.2^"}}
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	want := "答案 [联网#2]...\b\b\b   \b\b\b\n"
	if got := stdout.String(); got != want {
		t.Fatalf("expected citation rewrite and trailing ellipsis cleanup, got %q want %q", got, want)
	}
}

func TestStreamTaskClearsStreamingEllipsisOnCanceledError(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	stdout := &fakeTTYWriter{fd: 1}
	var stderr bytes.Buffer
	app.stdout = stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), stdout, &stderr)

	previous := isTerminalFD
	isTerminalFD = func(fd int) bool {
		return fd == 1
	}
	defer func() {
		isTerminalFD = previous
	}()

	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "答案"}}
	stream <- provider.StreamEvent{Type: provider.EventError, Data: map[string]any{"error": "读取流失败: context canceled"}}
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
		false,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	want := "答案...\b\b\b   \b\b\b"
	if got := stdout.String(); got != want {
		t.Fatalf("expected canceled stream to clear trailing ellipsis, got %q want %q", got, want)
	}
}

func TestStripThinkControlTagsRemovesMarkers(t *testing.T) {
	input := "回答前缀</think>\n正在执行服务端工具调用：搜索额度信息<think>"
	got := stripThinkControlTags(input)

	if strings.Contains(got, "<think>") || strings.Contains(got, "</think>") {
		t.Fatalf("expected think control tags to be stripped, got %q", got)
	}
	if !strings.Contains(got, "回答前缀") || !strings.Contains(got, "正在执行服务端工具调用") {
		t.Fatalf("expected visible text to remain after stripping think tags, got %q", got)
	}
}

func TestStreamTaskShowsToolCallOnlyWhenStreamEventsEnabled(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventToolCall, Data: map[string]any{"text": "read_file"}}
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "" {
		t.Fatalf("expected tool call hidden by default, got %q", got)
	}

	stdout.Reset()
	stderr.Reset()
	stream = make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventToolCall, Data: map[string]any{"text": "read_file"}}
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
	close(stream)

	_, err = app.streamTask(
		context.Background(),
		&stubProvider{stream: stream},
		internalconfig.Config{},
		"demo-flow",
		"demo task",
		agent.SessionState{RequestMode: provider.RequestTypeAgent},
		"",
		true,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "[工具调用]: read_file\n" {
		t.Fatalf("expected tool call shown when stream events enabled, got %q", got)
	}
}

func TestStreamTaskUsesReasoningOverride(t *testing.T) {
	testCases := []struct {
		name              string
		globalReasoning   bool
		override          string
		expectedReasoning bool
	}{
		{name: "inherit global true", globalReasoning: true, override: "", expectedReasoning: true},
		{name: "override on", globalReasoning: false, override: "on", expectedReasoning: true},
		{name: "override off", globalReasoning: true, override: "off", expectedReasoning: false},
	}

	for _, item := range testCases {
		t.Run(item.name, func(t *testing.T) {
			app := newTestAppWithSessionStore(t)
			app.stdout = io.Discard
			app.stderr = io.Discard
			app.runtime = clientruntime.NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)

			stream := make(chan provider.StreamEvent, 1)
			stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
			close(stream)

			providerStub := &stubProvider{stream: stream}
			config := internalconfig.Config{ReasoningEnabled: item.globalReasoning}
			_, err := app.streamTask(
				context.Background(),
				providerStub,
				config,
				"demo-flow",
				"demo task",
				agent.SessionState{RequestMode: provider.RequestTypeAgent, ReasoningOverride: item.override},
				"",
				false,
				false,
			)
			if err != nil {
				t.Fatalf("streamTask failed: %v", err)
			}
			if providerStub.lastRequest.ReasoningEnabled != item.expectedReasoning {
				t.Fatalf("expected reasoning_enabled=%v, got %v", item.expectedReasoning, providerStub.lastRequest.ReasoningEnabled)
			}
		})
	}
}

func TestStreamTaskUsesInternetSearchOverride(t *testing.T) {
	testCases := []struct {
		name                   string
		override               string
		expectedInternetSearch bool
	}{
		{name: "default off", override: "", expectedInternetSearch: false},
		{name: "override on", override: "on", expectedInternetSearch: true},
		{name: "override off", override: "off", expectedInternetSearch: false},
	}

	for _, item := range testCases {
		t.Run(item.name, func(t *testing.T) {
			app := newTestAppWithSessionStore(t)
			app.stdout = io.Discard
			app.stderr = io.Discard
			app.runtime = clientruntime.NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)

			stream := make(chan provider.StreamEvent, 1)
			stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
			close(stream)

			providerStub := &stubProvider{stream: stream}
			_, err := app.streamTask(
				context.Background(),
				providerStub,
				internalconfig.Config{},
				"demo-flow",
				"demo task",
				agent.SessionState{RequestMode: provider.RequestTypeAgent, InternetSearchOverride: item.override},
				"",
				false,
				false,
			)
			if err != nil {
				t.Fatalf("streamTask failed: %v", err)
			}
			if providerStub.lastRequest.InternetSearch != item.expectedInternetSearch {
				t.Fatalf("expected internet_search=%v, got %v", item.expectedInternetSearch, providerStub.lastRequest.InternetSearch)
			}
		})
	}
}

func TestStreamTaskShowsCompressionLifecycleOnlyWhenStreamEventsEnabled(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 3)
	stream <- provider.StreamEvent{Type: provider.EventCompressionStart, Data: map[string]any{"text": "开始进行上下文压缩：history_trim / history_message_limit", "before_tokens": 320, "after_tokens": 160}}
	stream <- provider.StreamEvent{Type: provider.EventCompressionEnd, Data: map[string]any{"text": "上下文压缩完成：320 -> 160", "before_tokens": 320, "after_tokens": 160}}
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("expected compression lifecycle hidden by default, got %q", got)
	}

	stdout.Reset()
	stderr.Reset()
	stream = make(chan provider.StreamEvent, 3)
	stream <- provider.StreamEvent{Type: provider.EventCompressionStart, Data: map[string]any{"text": "开始进行上下文压缩：history_trim / history_message_limit", "before_tokens": 320, "after_tokens": 160}}
	stream <- provider.StreamEvent{Type: provider.EventCompressionEnd, Data: map[string]any{"text": "上下文压缩完成：320 -> 160", "before_tokens": 320, "after_tokens": 160}}
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}}
	close(stream)

	_, err = app.streamTask(
		context.Background(),
		&stubProvider{stream: stream},
		internalconfig.Config{},
		"demo-flow",
		"demo task",
		agent.SessionState{RequestMode: provider.RequestTypeAgent},
		"",
		true,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "[上下文压缩]: 开始进行上下文压缩：history_trim / history_message_limit") {
		t.Fatalf("expected compression start event in output, got %q", output)
	}
	if !strings.Contains(output, "[上下文压缩]: 上下文压缩完成：0.3K -> 0.2K") {
		t.Fatalf("expected compression end event in output, got %q", output)
	}
}

func TestStreamTaskDoesNotAccumulateOrdinaryStreamEventsAcrossRequests(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	providerStub := &sequenceStreamProvider{
		streams: []<-chan provider.StreamEvent{
			streamWithEvents(
				provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "第一轮输出-片段一"}},
				provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "，片段二"}},
				provider.StreamEvent{Type: provider.EventToolCall, Data: map[string]any{"text": "read_file"}},
				provider.StreamEvent{Type: provider.EventCompressionStart, Data: map[string]any{"text": "开始进行上下文压缩：history_trim / history_message_limit", "before_tokens": 320, "after_tokens": 160}},
				provider.StreamEvent{Type: provider.EventCompression, Data: map[string]any{"before_tokens": 320, "after_tokens": 160}},
				provider.StreamEvent{Type: provider.EventCompressionEnd, Data: map[string]any{"text": "上下文压缩完成：320 -> 160", "before_tokens": 320, "after_tokens": 160}},
				provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}},
			),
			streamWithEvents(
				provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "第二轮输出仍然成功"}},
				provider.StreamEvent{Type: provider.EventCompression, Data: map[string]any{"before_tokens": 160, "after_tokens": 160}},
				provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}},
			),
		},
	}

	state := agent.SessionState{RequestMode: provider.RequestTypeAgent}
	var err error
	state, err = app.streamTask(
		context.Background(),
		providerStub,
		internalconfig.Config{},
		"demo-flow",
		"first task",
		state,
		"",
		true,
		false,
	)
	if err != nil {
		t.Fatalf("first streamTask failed: %v", err)
	}

	_, err = app.streamTask(
		context.Background(),
		providerStub,
		internalconfig.Config{},
		"demo-flow",
		"second task",
		state,
		"",
		true,
		false,
	)
	if err != nil {
		t.Fatalf("second streamTask failed: %v", err)
	}
	if providerStub.calls != 2 {
		t.Fatalf("expected two independent stream requests, got %d", providerStub.calls)
	}
	output := stdout.String()
	if !strings.Contains(output, "第一轮输出-片段一，片段二") {
		t.Fatalf("expected first streamed response to be rendered, got %q", output)
	}
	if !strings.Contains(output, "[工具调用]: read_file") {
		t.Fatalf("expected tool call event to stay observable, got %q", output)
	}
	if !strings.Contains(output, "[上下文压缩]: 开始进行上下文压缩：history_trim / history_message_limit") {
		t.Fatalf("expected compression progress to stay observable, got %q", output)
	}
	if !strings.Contains(output, "第二轮输出仍然成功") {
		t.Fatalf("expected subsequent user request to complete, got %q", output)
	}
	if strings.Contains(strings.ToLower(output), "max iterations") || strings.Contains(output, "最大循环") {
		t.Fatalf("expected ordinary stream events not to surface max loop termination, got %q", output)
	}
}

func TestStreamTaskDropsTrailingThinkTagFragmentByDefault(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "结果<think"}}
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "结果\n" {
		t.Fatalf("expected trailing think fragment hidden by default, got %q", got)
	}
}

func TestStreamTaskDropsTrailingFinalizeControlResidue(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 4)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "结果已完成。FINAL"}}
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "IZE"}}
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "结果已完成。\n" {
		t.Fatalf("expected trailing finalize residue hidden, got %q", got)
	}
}

func TestStreamTaskDropsStandaloneFinalizeTailResidueAtClose(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 4)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "结果已完成。"}}
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "IZE"}}
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "结果已完成。\n" {
		t.Fatalf("expected standalone finalize tail residue hidden at close, got %q", got)
	}
}

func TestStreamTaskPreservesUppercaseWordEndingInIZE(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app.stdout = &stdout
	app.stderr = &stderr
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	stream := make(chan provider.StreamEvent, 4)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "S"}}
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "IZE"}}
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
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}

	if got := stdout.String(); got != "SIZE\n" {
		t.Fatalf("expected uppercase word ending in IZE to be preserved, got %q", got)
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

func TestStreamTaskInstallsMissingSkillBeforeRequest(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{stdout: &stdout, stderr: &stderr, configManager: manager, sessionStore: store}
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	workspaceDir := t.TempDir()
	searchQueries := []string{}
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/search":
			searchQueries = append(searchQueries, request.URL.Query().Get("q"))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"上下文审查技能","version":"1.0.0","updatedAt":1710000000}]}`))
		case "/api/v1/skills/remote-skill":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"skill":{"slug":"remote-skill","displayName":"Remote Skill","summary":"上下文审查技能","updatedAt":1710000000},"latestVersion":{"version":"1.0.0"}}`))
		case "/api/v1/download":
			archiveBytes := buildRemoteSkillArchive(t, "1.0.0")
			_, _ = writer.Write(archiveBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer registry.Close()

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.RegistryURL = registry.URL
	config.Skills.InstallScope = internalconfig.SkillInstallScopeWorkspace
	config.Skills.AutoInstallPolicy = internalconfig.SkillAutoInstallPolicyAllow
	config.Skills.FreeTextSearchEnabled = false
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	stream := make(chan provider.StreamEvent, 1)
	stream <- provider.StreamEvent{Type: provider.EventError, Data: map[string]any{"error": "未找到技能: Remote Skill"}}
	close(stream)
	providerStub := &stubProvider{
		stream: stream,
	}

	_, err = app.streamTask(
		context.Background(),
		providerStub,
		config,
		"demo-flow",
		"请做上下文审查",
		agent.SessionState{FlowID: "demo-flow", RequestMode: provider.RequestTypeAgent, ContextDir: workspaceDir, Workdir: workspaceDir},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if len(searchQueries) != 1 || searchQueries[0] != "Remote Skill" {
		t.Fatalf("expected exact missing-skill search query, got %#v", searchQueries)
	}
	if !containsIgnoreCase(providerStub.lastRequest.RequestedSkillNames, "Remote Skill") {
		t.Fatalf("expected installed skill in request, got %#v", providerStub.lastRequest.RequestedSkillNames)
	}
	installedSkillPath := filepath.Join(workspaceDir, ".snz1dp", "skills", "remote-skill", "SKILL.md")
	if _, statErr := os.Stat(installedSkillPath); statErr != nil {
		t.Fatalf("expected workspace installed skill file, got %v", statErr)
	}
	loadedState, err := app.sessionStore.Load("demo-flow", workspaceDir)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if !containsIgnoreCase(loadedState.ApprovedSkillNames, "Remote Skill") {
		t.Fatalf("expected approved skill recorded in session, got %#v", loadedState.ApprovedSkillNames)
	}
}

func TestDetectMissingSkillNamesPrefersStructuredPayload(t *testing.T) {
	eventData := map[string]any{
		"error":      "generic failure",
		"error_code": "MISSING_SKILLS",
		"details": map[string]any{
			"missing_skill_names": []any{"Remote Skill", "Debug Helper"},
		},
	}

	names := detectMissingSkillNames(eventData, fmt.Errorf("后端返回错误: generic failure"))
	if len(names) != 2 {
		t.Fatalf("expected 2 missing skill names, got %#v", names)
	}
	if !containsIgnoreCase(names, "Remote Skill") || !containsIgnoreCase(names, "Debug Helper") {
		t.Fatalf("expected structured missing skills parsed, got %#v", names)
	}
}

func TestStreamTaskInstallsMissingSkillWithStructuredErrorPayload(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{stdout: &stdout, stderr: &stderr, configManager: manager, sessionStore: store}
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	workspaceDir := t.TempDir()
	searchQueries := []string{}
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/search":
			searchQueries = append(searchQueries, request.URL.Query().Get("q"))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"上下文审查技能","version":"1.0.0","updatedAt":1710000000}]}`))
		case "/api/v1/skills/remote-skill":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"skill":{"slug":"remote-skill","displayName":"Remote Skill","summary":"上下文审查技能","updatedAt":1710000000},"latestVersion":{"version":"1.0.0"}}`))
		case "/api/v1/download":
			archiveBytes := buildRemoteSkillArchive(t, "1.0.0")
			_, _ = writer.Write(archiveBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer registry.Close()

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.RegistryURL = registry.URL
	config.Skills.InstallScope = internalconfig.SkillInstallScopeWorkspace
	config.Skills.AutoInstallPolicy = internalconfig.SkillAutoInstallPolicyAllow
	config.Skills.FreeTextSearchEnabled = false
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	stream := make(chan provider.StreamEvent, 1)
	stream <- provider.StreamEvent{Type: provider.EventError, Data: map[string]any{
		"error":      "generic failure",
		"error_code": "MISSING_SKILLS",
		"details": map[string]any{
			"missing_skill_names": []any{"Remote Skill"},
		},
	}}
	close(stream)
	providerStub := &stubProvider{stream: stream}

	_, err = app.streamTask(
		context.Background(),
		providerStub,
		config,
		"demo-flow",
		"请做上下文审查",
		agent.SessionState{FlowID: "demo-flow", RequestMode: provider.RequestTypeAgent, ContextDir: workspaceDir, Workdir: workspaceDir},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if len(searchQueries) != 1 || searchQueries[0] != "Remote Skill" {
		t.Fatalf("expected structured missing-skill search query, got %#v", searchQueries)
	}
	if !containsIgnoreCase(providerStub.lastRequest.RequestedSkillNames, "Remote Skill") {
		t.Fatalf("expected installed skill in request, got %#v", providerStub.lastRequest.RequestedSkillNames)
	}
}

func TestStreamTaskUsesFreeTextSearchForUniqueSkillCandidate(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{stdout: &stdout, stderr: &stderr, configManager: manager, sessionStore: store}
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	workspaceDir := t.TempDir()
	searchQueries := []string{}
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/search":
			searchQueries = append(searchQueries, request.URL.Query().Get("q"))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"上下文审查技能","version":"1.0.0","updatedAt":1710000000}]}`))
		case "/api/v1/skills/remote-skill":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"skill":{"slug":"remote-skill","displayName":"Remote Skill","summary":"上下文审查技能","updatedAt":1710000000},"latestVersion":{"version":"1.0.0"}}`))
		case "/api/v1/download":
			archiveBytes := buildRemoteSkillArchive(t, "1.0.0")
			_, _ = writer.Write(archiveBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer registry.Close()

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.RegistryURL = registry.URL
	config.Skills.InstallScope = internalconfig.SkillInstallScopeWorkspace
	config.Skills.AutoInstallPolicy = internalconfig.SkillAutoInstallPolicyAllow
	config.Skills.FreeTextSearchEnabled = true
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	stream := make(chan provider.StreamEvent, 1)
	stream <- provider.StreamEvent{Type: provider.EventError, Data: map[string]any{"error": "未找到技能: Unknown Skill"}}
	close(stream)
	providerStub := &stubProvider{
		stream: stream,
	}

	input := "请帮我做上下文审查"
	_, err = app.streamTask(
		context.Background(),
		providerStub,
		config,
		"demo-flow",
		input,
		agent.SessionState{FlowID: "demo-flow", RequestMode: provider.RequestTypeAgent, ContextDir: workspaceDir, Workdir: workspaceDir},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if len(searchQueries) != 2 || searchQueries[0] != "Unknown Skill" || searchQueries[1] != input {
		t.Fatalf("expected exact+free-text search queries [Unknown Skill, %q], got %#v", input, searchQueries)
	}
	if !containsIgnoreCase(providerStub.lastRequest.RequestedSkillNames, "Remote Skill") {
		t.Fatalf("expected free-text installed skill in request, got %#v", providerStub.lastRequest.RequestedSkillNames)
	}
}

func TestStreamTaskInstallsMissingSkillIntoLegacyGlobalDirectoryAndReloadsRequest(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{stdout: &stdout, stderr: &stderr, configManager: manager, sessionStore: store}
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	workspaceDir := t.TempDir()
	legacySkillDir := filepath.Join(t.TempDir(), "legacy-skills")
	searchQueries := []string{}
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/search":
			searchQueries = append(searchQueries, request.URL.Query().Get("q"))
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"上下文审查技能","version":"1.0.0","updatedAt":1710000000}]}`))
		case "/api/v1/skills/remote-skill":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"skill":{"slug":"remote-skill","displayName":"Remote Skill","summary":"上下文审查技能","updatedAt":1710000000},"latestVersion":{"version":"1.0.0"}}`))
		case "/api/v1/download":
			archiveBytes := buildRemoteSkillArchive(t, "1.0.0")
			_, _ = writer.Write(archiveBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer registry.Close()

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.Directory = legacySkillDir
	config.Skills.RegistryURL = registry.URL
	config.Skills.InstallScope = internalconfig.SkillInstallScopeUser
	config.Skills.AutoInstallPolicy = internalconfig.SkillAutoInstallPolicyAllow
	config.Skills.FreeTextSearchEnabled = false
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	stream := make(chan provider.StreamEvent, 1)
	stream <- provider.StreamEvent{Type: provider.EventError, Data: map[string]any{"error": "未找到技能: Remote Skill"}}
	close(stream)
	providerStub := &stubProvider{
		stream: stream,
	}

	_, err = app.streamTask(
		context.Background(),
		providerStub,
		config,
		"demo-flow",
		"请做上下文审查",
		agent.SessionState{FlowID: "demo-flow", RequestMode: provider.RequestTypeAgent, ContextDir: workspaceDir, Workdir: workspaceDir},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	if len(searchQueries) != 1 || searchQueries[0] != "Remote Skill" {
		t.Fatalf("expected exact missing-skill search query, got %#v", searchQueries)
	}
	if !containsIgnoreCase(providerStub.lastRequest.RequestedSkillNames, "Remote Skill") {
		t.Fatalf("expected installed skill in request after reload, got %#v", providerStub.lastRequest.RequestedSkillNames)
	}
	installedSkillPath := filepath.Join(legacySkillDir, "remote-skill", "SKILL.md")
	if _, statErr := os.Stat(installedSkillPath); statErr != nil {
		t.Fatalf("expected legacy custom global installed skill file, got %v", statErr)
	}
}

func TestStreamTaskAutoInstallUsesPinnedSessionInstallScope(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{
		stdout:                   &stdout,
		stderr:                   &stderr,
		configManager:            manager,
		sessionStore:             store,
		skillInstallScopeSession: internalconfig.SkillInstallScopeWorkspace,
		skillInstallScopePinned:  true,
	}
	app.runtime = clientruntime.NewClientRuntime(bytes.NewBuffer(nil), &stdout, &stderr)

	workspaceDir := t.TempDir()
	legacySkillDir := filepath.Join(t.TempDir(), "legacy-skills")
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/search":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"上下文审查技能","version":"1.0.0","updatedAt":1710000000}]}`))
		case "/api/v1/skills/remote-skill":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"skill":{"slug":"remote-skill","displayName":"Remote Skill","summary":"上下文审查技能","updatedAt":1710000000},"latestVersion":{"version":"1.0.0"}}`))
		case "/api/v1/download":
			archiveBytes := buildRemoteSkillArchive(t, "1.0.0")
			_, _ = writer.Write(archiveBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer registry.Close()

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.Directory = legacySkillDir
	config.Skills.InstallScope = internalconfig.SkillInstallScopeUser
	config.Skills.RegistryURL = registry.URL
	config.Skills.AutoInstallPolicy = internalconfig.SkillAutoInstallPolicyAllow
	config.Skills.FreeTextSearchEnabled = false
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	stream := make(chan provider.StreamEvent, 1)
	stream <- provider.StreamEvent{Type: provider.EventError, Data: map[string]any{"error": "未找到技能: Remote Skill"}}
	close(stream)
	providerStub := &stubProvider{
		stream: stream,
	}

	_, err = app.streamTask(
		context.Background(),
		providerStub,
		config,
		"demo-flow",
		"请做上下文审查",
		agent.SessionState{FlowID: "demo-flow", RequestMode: provider.RequestTypeAgent, ContextDir: workspaceDir, Workdir: workspaceDir},
		"",
		false,
		false,
	)
	if err != nil {
		t.Fatalf("streamTask failed: %v", err)
	}
	workspaceInstalledSkillPath := filepath.Join(workspaceDir, ".snz1dp", "skills", "remote-skill", "SKILL.md")
	if _, statErr := os.Stat(workspaceInstalledSkillPath); statErr != nil {
		t.Fatalf("expected pinned session scope to install into workspace, got %v", statErr)
	}
	legacyInstalledSkillPath := filepath.Join(legacySkillDir, "remote-skill", "SKILL.md")
	if _, statErr := os.Stat(legacyInstalledSkillPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected pinned session scope to avoid legacy user directory, got %v", statErr)
	}
	if !containsIgnoreCase(providerStub.lastRequest.RequestedSkillNames, "Remote Skill") {
		t.Fatalf("expected installed skill in request after pinned-scope install, got %#v", providerStub.lastRequest.RequestedSkillNames)
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
		"chunk":                   "回答片段",
		"message_id":              "msg-1",
		"stage":                   "end",
		"event_type":              "message",
		"split_assistant_message": true,
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
	if event.MessageID != "msg-1" || event.Stage != "end" || event.EventType != "message" || !event.SplitAssistant {
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

func TestBuildTUIAssistantChunkEventAllowsSplitMarkerWithoutChunk(t *testing.T) {
	event, ok := buildTUIAssistantChunkEvent(map[string]any{"split_assistant_message": true})
	if !ok {
		t.Fatal("expected split marker to produce assistant event")
	}
	if !event.SplitAssistant || event.Text != "" {
		t.Fatalf("expected split-only assistant event, got %#v", event)
	}
}
