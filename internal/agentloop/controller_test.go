package agentloop

import (
	"context"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

type fakeProvider struct {
	provider.Provider
	execution       *provider.SessionExecutionMetadata
	executionErr    error
	checkpoints     []provider.AgentLoopCheckpoint
	stream          <-chan provider.StreamEvent
	streams         []<-chan provider.StreamEvent
	streamErr       error
	submitErr       error
	messages        *provider.SessionMessageList
	messageErr      error
	calls           []string
	requests        []provider.StreamRequest
	controlRequests []provider.AgentLoopControlRequest
	cancelled       chan struct{}
}

func (f *fakeProvider) GetSessionExecution(context.Context, string) (*provider.SessionExecutionMetadata, error) {
	f.calls = append(f.calls, "execution")
	return f.execution, f.executionErr
}

func (f *fakeProvider) ListResumableAgentLoopCheckpoints(context.Context, provider.AgentLoopCheckpointQuery) ([]provider.AgentLoopCheckpoint, error) {
	f.calls = append(f.calls, "checkpoints")
	return f.checkpoints, nil
}

func (f *fakeProvider) SubmitClientCallResult(context.Context, provider.ClientCallResult) error {
	f.calls = append(f.calls, "submit")
	return f.submitErr
}

func (f *fakeProvider) ListSessionMessages(_ context.Context, _ string, _ string, offset, _ int) (*provider.SessionMessageList, error) {
	f.calls = append(f.calls, "messages")
	if f.messageErr != nil {
		return nil, f.messageErr
	}
	if f.messages == nil {
		return &provider.SessionMessageList{}, nil
	}
	result := *f.messages
	result.Offset = offset
	return &result, nil
}

func (f *fakeProvider) PauseAgentLoop(_ context.Context, _ string, request provider.AgentLoopControlRequest) (*provider.AgentLoopControlResponse, error) {
	f.calls = append(f.calls, "pause")
	f.controlRequests = append(f.controlRequests, request)
	return &provider.AgentLoopControlResponse{RunID: request.RunID, RequestID: request.RequestID, Status: "pause_requested"}, nil
}

func (f *fakeProvider) CancelAgentLoop(ctx context.Context, _ string, request provider.AgentLoopControlRequest) (*provider.AgentLoopControlResponse, error) {
	f.calls = append(f.calls, "cancel")
	f.controlRequests = append(f.controlRequests, request)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if f.cancelled != nil {
		close(f.cancelled)
	}
	return &provider.AgentLoopControlResponse{RunID: request.RunID, RequestID: request.RequestID, Status: "cancel_requested"}, nil
}

func (f *fakeProvider) Stream(_ context.Context, request provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	f.calls = append(f.calls, "stream")
	f.requests = append(f.requests, request)
	stream := f.stream
	if len(f.streams) > 0 {
		stream = f.streams[0]
		f.streams = f.streams[1:]
	}
	return stream, &provider.StreamSession{SessionID: request.SessionID}, f.streamErr
}

func TestRecoverFlushesCallbacksAndUsesResumeWithoutInput(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	events := make(chan provider.StreamEvent, 1)
	events <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"name": "resume-flow", "session_id": "session-1", "run_id": "run-1", "execution_attempt_id": "attempt-2"}}
	close(events)
	checkpoint := provider.AgentLoopCheckpoint{
		ID: "checkpoint-1", Version: 4, FlowID: "flow-1", SessionID: "session-1", RunID: "run-1",
		OriginClientID: "client-1", OriginDeviceID: "device-1", Status: "paused", PauseReason: "network", ExpiresAt: now.Add(time.Hour),
	}
	fake := &fakeProvider{execution: &provider.SessionExecutionMetadata{}, checkpoints: []provider.AgentLoopCheckpoint{checkpoint}, stream: events}
	controller := NewController(fake, store)
	original := provider.StreamRequest{
		FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", RequestType: provider.RequestTypeAgent,
		ClientID: "client-1", DeviceID: "device-1", Input: "do not replay", Model: "model-original",
		AgentAutoLoopMaxRounds: 7, RequestedSkillNames: []string{"skill-1"},
	}
	prepared, err := controller.PrepareStart(original)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ExecutionMode != provider.ExecutionModeStart || prepared.ExecutionAttemptID == "" {
		t.Fatalf("unexpected prepared start: %#v", prepared)
	}
	if _, err := store.RecordAcceptedExecution("session-1", "run-1", "attempt-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkDisconnected("session-1", "run-1"); err != nil {
		t.Fatal(err)
	}
	callback := provider.ClientCallResult{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", CallID: "call-1", Success: true}
	if _, err := store.EnqueueToolCallback(callback, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	attempt, err := controller.Recover(context.Background(), "session-1", false)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Close()
	if !reflect.DeepEqual(fake.calls, []string{"submit", "execution", "checkpoints", "stream"}) {
		t.Fatalf("unexpected recovery order: %v", fake.calls)
	}
	if len(fake.requests) != 1 {
		t.Fatalf("expected one resume request, got %d", len(fake.requests))
	}
	request := fake.requests[0]
	if request.ExecutionMode != provider.ExecutionModeResume || request.Input != "" || request.RunID != "run-1" || request.CheckpointID != "checkpoint-1" || request.CheckpointVersion != 4 {
		t.Fatalf("unexpected resume request: %#v", request)
	}
	if request.Model != "model-original" || request.AgentAutoLoopMaxRounds != 7 || len(request.RequestedSkillNames) != 1 {
		t.Fatalf("capability snapshot was not preserved: %#v", request)
	}
	info, err := attempt.Observe(<-events)
	if err != nil || !info.ResumeReady {
		t.Fatalf("expected resume ready: %#v, %v", info, err)
	}
	state, err := store.LoadRecovery("session-1")
	if err != nil || state.AwaitingResume || !state.Active || state.ExecutionAttemptID != "attempt-2" {
		t.Fatalf("unexpected ready state: %#v, %v", state, err)
	}
	callbacks, err := store.ListDueToolCallbacks(now)
	if err != nil || len(callbacks) != 0 {
		t.Fatalf("expected callback to be removed: %#v, %v", callbacks, err)
	}
}

func TestUserPauseRequiresExplicitResume(t *testing.T) {
	store := NewStore(t.TempDir())
	checkpoint := provider.AgentLoopCheckpoint{ID: "checkpoint-1", Version: 1, FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", OriginClientID: "client-1", PauseReason: "user", RequiresUserResume: true}
	fake := &fakeProvider{execution: &provider.SessionExecutionMetadata{}, checkpoints: []provider.AgentLoopCheckpoint{checkpoint}, stream: make(chan provider.StreamEvent)}
	controller := NewController(fake, store)
	if _, err := controller.PrepareStart(provider.StreamRequest{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", RequestType: provider.RequestTypeAgent, ClientID: "client-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetUserControlIntent("session-1", "run-1", IntentUserPause); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Recover(context.Background(), "session-1", false); !errors.Is(err, ErrUserControlInvalidated) {
		t.Fatalf("automatic recovery must reject user pause, got %v", err)
	}
	attempt, err := controller.Recover(context.Background(), "session-1", true)
	if err != nil {
		t.Fatalf("explicit resume failed: %v", err)
	}
	attempt.Close()
	if len(fake.requests) != 1 || fake.requests[0].ExecutionMode != provider.ExecutionModeResume {
		t.Fatalf("explicit resume must retain execution mode: %#v", fake.requests)
	}
}

func TestUncertainSubmissionNeverFallsBackToStart(t *testing.T) {
	store := NewStore(t.TempDir())
	fake := &fakeProvider{execution: &provider.SessionExecutionMetadata{}}
	controller := NewController(fake, store)
	controller.Sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := controller.PrepareStart(provider.StreamRequest{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", RequestType: provider.RequestTypeAgent}); err != nil {
		t.Fatal(err)
	}
	err := controller.ReconcileSubmission(context.Background(), "session-1")
	if !errors.Is(err, ErrSubmissionUncertain) {
		t.Fatalf("expected uncertain submission, got %v", err)
	}
	if len(fake.requests) != 0 {
		t.Fatalf("reconciliation must not start another execution: %#v", fake.requests)
	}
	state, err := store.LoadRecovery("session-1")
	if err != nil || !state.PendingStart {
		t.Fatalf("pending start must remain for explicit user handling: %#v, %v", state, err)
	}
}

func TestControlReusesRequestIDAndCancelPrecedesPause(t *testing.T) {
	store := NewStore(t.TempDir())
	fake := &fakeProvider{}
	controller := NewController(fake, store)
	if _, err := controller.PrepareStart(provider.StreamRequest{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", RequestType: provider.RequestTypeAgent, ClientID: "client-1", DeviceID: "device-1"}); err != nil {
		t.Fatal(err)
	}
	first, err := controller.Pause(context.Background(), "session-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := controller.Pause(context.Background(), "session-1", "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.RequestID == "" || first.RequestID != second.RequestID || len(fake.controlRequests) != 2 {
		t.Fatalf("pause request ID must be stable: %#v %#v", first, second)
	}
	if controller.ToolsAllowed("session-1", "run-1") {
		t.Fatal("user pause must block new client tools")
	}
	if _, err := controller.Cancel(context.Background(), "session-1", "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Pause(context.Background(), "session-1", "run-1"); err == nil {
		t.Fatal("pause after cancel must fail locally")
	}
	state, err := store.LoadRecovery("session-1")
	if err != nil || state.Intent != IntentStopped || !state.InvalidatedByUser {
		t.Fatalf("unexpected stopped state: %#v, %v", state, err)
	}
}

func TestExecuteCancellationUsesDetachedServerCancel(t *testing.T) {
	store := NewStore(t.TempDir())
	stream := make(chan provider.StreamEvent)
	fake := &fakeProvider{stream: stream, cancelled: make(chan struct{})}
	controller := NewController(fake, store)
	ctx, cancel := context.WithCancel(context.Background())
	events, _, err := controller.Execute(ctx, provider.StreamRequest{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", RequestType: provider.RequestTypeAgent, ClientID: "client-1", DeviceID: "device-1"})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-fake.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("detached server cancel was not called")
	}
	for range events {
	}
	state, err := store.LoadRecovery("session-1")
	if err != nil || state.Intent != IntentStopped {
		t.Fatalf("cancellation must persist stopped intent: %#v, %v", state, err)
	}
	if len(fake.requests) != 1 || fake.requests[0].ExecutionMode != provider.ExecutionModeStart {
		t.Fatalf("cancellation must not retry start: %#v", fake.requests)
	}
}

func TestLateToolResultIsNotRetried(t *testing.T) {
	store := NewStore(t.TempDir())
	result := provider.ClientCallResult{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", CallID: "call-1"}
	if _, err := store.EnqueueToolCallback(result, time.Now()); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{submitErr: &provider.HTTPError{StatusCode: http.StatusConflict, Body: `{"detail":"tool_call_paused"}`}}
	controller := NewController(fake, store)
	if err := controller.FlushToolCallbacks(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadToolCallback("flow-1", "session-1", "run-1", "call-1"); err == nil {
		t.Fatal("expected late callback to be removed")
	}
}

func TestExecuteReconnectsWithResumeInsteadOfStart(t *testing.T) {
	store := NewStore(t.TempDir())
	startEvents := make(chan provider.StreamEvent, 1)
	startEvents <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"name": "start-flow", "session_id": "session-1", "run_id": "run-1", "execution_attempt_id": "attempt-1"}}
	close(startEvents)
	resumeEvents := make(chan provider.StreamEvent, 2)
	resumeEvents <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"name": "resume-flow", "session_id": "session-1", "run_id": "run-1", "execution_attempt_id": "attempt-2"}}
	resumeEvents <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{"session_id": "session-1", "run_id": "run-1"}}
	close(resumeEvents)
	fake := &fakeProvider{
		execution:   &provider.SessionExecutionMetadata{},
		checkpoints: []provider.AgentLoopCheckpoint{{ID: "checkpoint-1", Version: 1, FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", OriginClientID: "client-1", PauseReason: "network"}},
		streams:     []<-chan provider.StreamEvent{startEvents, resumeEvents},
	}
	controller := NewController(fake, store)
	controller.Sleep = func(context.Context, time.Duration) error { return nil }
	stream, _, err := controller.Execute(context.Background(), provider.StreamRequest{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", RequestType: provider.RequestTypeAgent, ClientID: "client-1", Input: "original"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for event := range stream {
		if event.Type == provider.EventError {
			t.Fatalf("unexpected execution error: %#v", event.Data)
		}
		names = append(names, ClassifyEvent(event).Name)
	}
	if !reflect.DeepEqual(names, []string{"start-flow", "resume-flow", "close"}) {
		t.Fatalf("unexpected event sequence: %v", names)
	}
	if len(fake.requests) != 2 || fake.requests[0].ExecutionMode != provider.ExecutionModeStart || fake.requests[1].ExecutionMode != provider.ExecutionModeResume || fake.requests[1].Input != "" {
		t.Fatalf("disconnect must resume without replaying input: %#v", fake.requests)
	}
}

func TestExecuteUncertainFirstResponseDoesNotRetryStart(t *testing.T) {
	store := NewStore(t.TempDir())
	empty := make(chan provider.StreamEvent)
	close(empty)
	fake := &fakeProvider{execution: &provider.SessionExecutionMetadata{}, stream: empty}
	controller := NewController(fake, store)
	controller.Sleep = func(context.Context, time.Duration) error { return nil }
	stream, _, err := controller.Execute(context.Background(), provider.StreamRequest{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", RequestType: provider.RequestTypeAgent})
	if err != nil {
		t.Fatal(err)
	}
	var receivedError error
	for event := range stream {
		if event.Type == provider.EventError {
			receivedError = errors.New(stringValue(event.Data, "error"))
		}
	}
	if receivedError == nil || !strings.Contains(receivedError.Error(), "提交结果不确定") {
		t.Fatalf("expected uncertain submission error, got %v", receivedError)
	}
	if len(fake.requests) != 1 || fake.requests[0].ExecutionMode != provider.ExecutionModeStart {
		t.Fatalf("must not retry start: %#v", fake.requests)
	}
}

func TestEventClassificationAndCapabilityPreservation(t *testing.T) {
	event := provider.StreamEvent{ID: "event-1", Type: provider.EventError, Data: map[string]any{"name": "network_freezing", "session_id": "session-1", "run_id": "run-1", "retry_after": 2.5}}
	info := ClassifyEvent(event)
	if !info.NetworkFreeze || info.RetryAfter != 2500*time.Millisecond || info.EventID != "event-1" {
		t.Fatalf("unexpected event classification: %#v", info)
	}
	store := NewStore(t.TempDir())
	controller := NewController(&fakeProvider{}, store)
	ask := provider.StreamRequest{FlowID: "flow-1", RequestType: provider.RequestTypeAsk}
	prepared, err := controller.PrepareStart(ask)
	if err != nil || prepared.ExecutionMode != "" {
		t.Fatalf("ask must retain legacy behavior: %#v, %v", prepared, err)
	}
}

func TestToolSuspendTerminalIsForwardedAndCleaned(t *testing.T) {
	store := NewStore(t.TempDir())
	events := make(chan provider.StreamEvent, 3)
	events <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"name": "start-flow", "session_id": "session-1", "run_id": "run-1", "execution_attempt_id": "attempt-1"}}
	events <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"name": "agent_loop_checkpoint_created", "session_id": "session-1", "run_id": "run-1", "checkpoint_id": "cp-1", "checkpoint_version": 3, "pause_reason": "tool_suspend"}}
	events <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"name": "end-flow", "session_id": "session-1", "run_id": "run-1", "run_finished": true}}
	close(events)
	fake := &fakeProvider{stream: events}
	controller := NewController(fake, store)
	stream, _, err := controller.Execute(context.Background(), provider.StreamRequest{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", RequestType: provider.RequestTypeAgent, ClientID: "client-1"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	sawError := false
	for event := range stream {
		if event.Type == provider.EventError {
			sawError = true
		}
		names = append(names, ClassifyEvent(event).Name)
	}
	if sawError {
		t.Fatal("tool_suspend 真终态不得伪装成自动恢复失败")
	}
	if !reflect.DeepEqual(names, []string{"start-flow", "agent_loop_checkpoint_created", "end-flow"}) {
		t.Fatalf("终态事件必须原样下发: %v", names)
	}
	if len(fake.requests) != 1 || fake.requests[0].ExecutionMode != provider.ExecutionModeStart {
		t.Fatalf("真终态不得进入恢复续跑: %#v", fake.requests)
	}
	state, err := store.LoadRecovery("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.RunID != "" || state.CheckpointID != "" {
		t.Fatalf("真终态后应清理恢复状态: %#v", state)
	}
}

func TestNetworkFreezeTerminalKeepsResumableState(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	controller := NewController(&fakeProvider{}, store)
	if _, err := controller.PrepareStart(provider.StreamRequest{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", RequestType: provider.RequestTypeAgent, ClientID: "client-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkDisconnected("session-1", "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRecovery("session-1", func(state *RecoveryState) error {
		state.CheckpointID = "cp-1"
		state.CheckpointVersion = 4
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 网络冻结的 close 不带 run_finished，应判定为可续跑冻结并保留状态。
	info, err := controller.ObserveEvent(provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"name": "close", "session_id": "session-1", "run_id": "run-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Terminal {
		t.Fatalf("close 应判为终态事件: %#v", info)
	}
	state, err := store.LoadRecovery("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.AutoResumable() {
		t.Fatalf("网络冻结 close 不得清理可续跑状态: %#v", state)
	}
}

func TestFlushToolCallbacksSkipsCorruptAndIsolatesFailures(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	for _, callID := range []string{"call-good", "call-bad"} {
		if _, err := store.EnqueueToolCallback(provider.ClientCallResult{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", CallID: callID}, now); err != nil {
			t.Fatal(err)
		}
	}
	corrupt := store.callbackPath("flow-1", "session-1", "run-1", "call-corrupt")
	if err := os.WriteFile(corrupt, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{submitErr: &provider.HTTPError{StatusCode: http.StatusBadRequest, Body: `{"detail":"bad request"}`}}
	controller := NewController(fake, store)
	// 永久 4xx 由服务端权威拒绝，视为已处置：静默丢弃且不阻断其余补报，故不返回错误。
	if err := controller.FlushToolCallbacks(context.Background(), "run-1"); err != nil {
		t.Fatalf("永久拒绝应被静默清理而非反复报错: %v", err)
	}
	// 永久 4xx：两条健康记录都应被丢弃；损坏文件不得阻断整份队列。
	for _, callID := range []string{"call-good", "call-bad"} {
		if _, loadErr := store.LoadToolCallback("flow-1", "session-1", "run-1", callID); loadErr == nil {
			t.Fatalf("永久拒绝的回调应被移除: %s", callID)
		}
	}
	due, dueErr := store.ListDueToolCallbacks(now)
	if dueErr != nil {
		t.Fatalf("损坏回调不得让列表失败: %v", dueErr)
	}
	if len(due) != 0 {
		t.Fatalf("健康回调应已清理，仅剩损坏文件被跳过: %#v", due)
	}
}

func TestFlushToolCallbacksPausedMachineCodeDiscardedAt400(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	if _, err := store.EnqueueToolCallback(provider.ClientCallResult{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", CallID: "call-1"}, now); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{submitErr: &provider.HTTPError{StatusCode: http.StatusBadRequest, Body: `{"detail":"tool_call_paused"}`}}
	controller := NewController(fake, store)
	if err := controller.FlushToolCallbacks(context.Background(), "run-1"); err != nil {
		t.Fatalf("暂停机器码应识别为迟到丢弃，不得重试: %v", err)
	}
	if _, err := store.LoadToolCallback("flow-1", "session-1", "run-1", "call-1"); err == nil {
		t.Fatal("暂停迟到的回调应被移除")
	}
}

func TestFlushToolCallbacksExhaustsAfterMaxAttempts(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	if _, err := store.EnqueueToolCallback(provider.ClientCallResult{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", CallID: "call-1"}, now); err != nil {
		t.Fatal(err)
	}
	fake := &fakeProvider{submitErr: &provider.HTTPError{StatusCode: http.StatusInternalServerError, Body: `{"detail":"boom"}`}}
	controller := NewController(fake, store)
	var err error
	for attempt := 0; attempt <= maxToolCallbackAttempts+2; attempt++ {
		current := attempt
		store.SetClock(func() time.Time { return now.Add(time.Duration(current) * time.Hour) })
		err = controller.FlushToolCallbacks(context.Background(), "run-1")
		if err != nil && strings.Contains(err.Error(), "已放弃上报") {
			break
		}
	}
	if err == nil || !strings.Contains(err.Error(), "已放弃上报") {
		t.Fatalf("超过最大尝试次数应放弃并告警: %v", err)
	}
	if _, loadErr := store.LoadToolCallback("flow-1", "session-1", "run-1", "call-1"); loadErr == nil {
		t.Fatal("放弃后应移除回调记录")
	}
}
