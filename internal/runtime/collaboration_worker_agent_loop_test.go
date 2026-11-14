package runtime

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gobwas/ws/wsutil"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agentloop"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

type agentLoopWorkerProviderStub struct {
	provider.Provider
	mu             sync.Mutex
	checkpoints    []provider.AgentLoopCheckpoint
	stream         chan provider.StreamEvent
	streamRequests []provider.StreamRequest
	operations     []string
	callbacks      []provider.ClientCallResult
}

func (s *agentLoopWorkerProviderStub) Stream(_ context.Context, request provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	s.mu.Lock()
	s.streamRequests = append(s.streamRequests, request)
	s.operations = append(s.operations, "stream")
	stream := s.stream
	s.mu.Unlock()
	if stream == nil {
		stream = make(chan provider.StreamEvent)
		close(stream)
	}
	return stream, &provider.StreamSession{SessionID: request.SessionID}, nil
}

func (s *agentLoopWorkerProviderStub) GetSessionExecution(context.Context, string) (*provider.SessionExecutionMetadata, error) {
	return &provider.SessionExecutionMetadata{}, nil
}

func (s *agentLoopWorkerProviderStub) ListResumableAgentLoopCheckpoints(_ context.Context, query provider.AgentLoopCheckpointQuery) ([]provider.AgentLoopCheckpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operations = append(s.operations, "checkpoints")
	result := make([]provider.AgentLoopCheckpoint, 0, len(s.checkpoints))
	for _, checkpoint := range s.checkpoints {
		if query.SessionID == "" || query.SessionID == checkpoint.SessionID {
			result = append(result, checkpoint)
		}
	}
	return result, nil
}

func (s *agentLoopWorkerProviderStub) SubmitClientCallResult(_ context.Context, result provider.ClientCallResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks = append(s.callbacks, result)
	s.operations = append(s.operations, "callback")
	return nil
}

func (s *agentLoopWorkerProviderStub) snapshot() ([]provider.StreamRequest, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]provider.StreamRequest(nil), s.streamRequests...), append([]string(nil), s.operations...)
}

func newAgentLoopWorkerFixture(t *testing.T) (*collaborationWorker, *agentloop.Store, *agentLoopWorkerProviderStub, provider.AgentLoopCheckpoint) {
	t.Helper()
	store := agentloop.NewStore(t.TempDir())
	checkpoint := provider.AgentLoopCheckpoint{
		ID: "checkpoint-1", Version: 1, FlowID: "flow-1", SessionID: "session-1", RunID: "run-1",
		OriginClientID: "client-1", OriginDeviceID: "device-1", Status: "ready", PauseReason: "network",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	_, err := store.RecordPendingStartRequest(provider.StreamRequest{
		FlowID: checkpoint.FlowID, SessionID: checkpoint.SessionID, RunID: checkpoint.RunID,
		ClientID: checkpoint.OriginClientID, DeviceID: checkpoint.OriginDeviceID,
		RequestType: provider.RequestTypeAgent, Input: "must-not-be-replayed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetResumeIntent(checkpoint); err != nil {
		t.Fatal(err)
	}
	providerStub := &agentLoopWorkerProviderStub{checkpoints: []provider.AgentLoopCheckpoint{checkpoint}}
	runtimeClient := NewClientRuntime(nil, nil, nil)
	runtimeClient.SetAgentLoopStore(store)
	worker := &collaborationWorker{
		runtime: runtimeClient, provider: providerStub,
		config: &internalconfig.Config{ClientID: checkpoint.OriginClientID, DeviceID: checkpoint.OriginDeviceID},
		logf:   func(string, ...any) {}, semaphore: make(chan struct{}, 1),
		agentLoop: &workerAgentLoopCoordinator{controller: agentloop.NewController(providerStub, store), store: store, discovery: make(chan struct{}, 1)},
	}
	return worker, store, providerStub, checkpoint
}

func TestCollaborationWorkerURLDeclaresAgentLoopResumeCapability(t *testing.T) {
	raw, err := buildCollaborationWorkerWebSocketURL(internalconfig.Config{BackendBaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := strings.Split(parsed.Query().Get("capabilities"), ",")
	for _, expected := range []string{"task.remote_task.v1", "task.dialogue_subtask.v1", collaborationCapabilityAgentLoopResume} {
		found := false
		for _, capability := range capabilities {
			if capability == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing capability %q in %q", expected, capabilities)
		}
	}
}

func TestAgentLoopNotificationResumesAcknowledgesAndDeduplicates(t *testing.T) {
	worker, store, providerStub, checkpoint := newAgentLoopWorkerFixture(t)
	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{
		"name": "resume-flow", "session_id": checkpoint.SessionID, "run_id": checkpoint.RunID,
		"checkpoint_id": checkpoint.ID, "checkpoint_version": checkpoint.Version,
	}}
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{"session_id": checkpoint.SessionID, "run_id": checkpoint.RunID}}
	close(stream)
	providerStub.stream = stream
	notification := agentLoopResumeNotification{
		NotificationID: "notification-1", CheckpointID: checkpoint.ID, CheckpointVersion: checkpoint.Version,
		SessionID: checkpoint.SessionID, RunID: checkpoint.RunID, FlowID: checkpoint.FlowID, ExpiresAt: checkpoint.ExpiresAt,
	}
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	worker.scheduleAgentLoopNotification(context.Background(), clientConn, notification)
	if err := serverConn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	raw, _, err := wsutil.ReadClientData(serverConn)
	if err != nil {
		t.Fatal(err)
	}
	var envelope collaborationEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Event != collaborationEventAgentLoopResumeAck {
		t.Fatalf("expected resume ACK, got %s", envelope.Event)
	}
	var ack map[string]any
	if err := json.Unmarshal(envelope.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	if ack["notification_id"] != notification.NotificationID || ack["checkpoint_id"] != checkpoint.ID || ack["checkpoint_version"] != float64(1) {
		t.Fatalf("unexpected ACK: %#v", ack)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		requests, _ := providerStub.snapshot()
		if len(requests) == 1 {
			if requests[0].ExecutionMode != provider.ExecutionModeResume || requests[0].RunID != checkpoint.RunID || requests[0].Input != "" {
				t.Fatalf("unexpected resume request: %#v", requests[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("resume request was not sent")
		}
		time.Sleep(time.Millisecond)
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		state, stateErr := store.LoadRecovery(checkpoint.SessionID)
		if stateErr == nil && !state.Active && state.CheckpointID == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovery goroutine did not finish: state=%#v err=%v", state, stateErr)
		}
		time.Sleep(time.Millisecond)
	}
	worker.scheduleAgentLoopNotification(context.Background(), clientConn, notification)
	time.Sleep(10 * time.Millisecond)
	requests, _ := providerStub.snapshot()
	if len(requests) != 1 {
		t.Fatalf("duplicate notification started another stream: %#v", requests)
	}
	if _, acquired, err := store.BeginNotification(checkpoint.SessionID, notification.NotificationID); err != nil || acquired {
		t.Fatalf("completed notification should remain deduplicated: acquired=%t err=%v", acquired, err)
	}
}

func TestAgentLoopNotificationDoesNotOverrideUserPause(t *testing.T) {
	worker, store, providerStub, checkpoint := newAgentLoopWorkerFixture(t)
	if _, err := store.SetUserControlIntent(checkpoint.SessionID, checkpoint.RunID, agentloop.IntentUserPause); err != nil {
		t.Fatal(err)
	}
	notification := agentLoopResumeNotification{
		NotificationID: "notification-pause", CheckpointID: checkpoint.ID, CheckpointVersion: checkpoint.Version,
		SessionID: checkpoint.SessionID, RunID: checkpoint.RunID, FlowID: checkpoint.FlowID, ExpiresAt: checkpoint.ExpiresAt,
	}
	claim, acquired, err := store.BeginNotification(checkpoint.SessionID, notification.NotificationID)
	if err != nil || !acquired {
		t.Fatalf("begin notification failed: acquired=%t err=%v", acquired, err)
	}
	worker.semaphore <- struct{}{}
	worker.executeAgentLoopNotification(context.Background(), nil, notification, claim)
	requests, _ := providerStub.snapshot()
	if len(requests) != 0 {
		t.Fatalf("user pause must not auto-resume: %#v", requests)
	}
}

func TestAgentLoopDiscoveryFlushesCallbacksBeforeCheckpointAndResume(t *testing.T) {
	worker, store, providerStub, checkpoint := newAgentLoopWorkerFixture(t)
	_, err := store.EnqueueToolCallback(provider.ClientCallResult{
		FlowID: checkpoint.FlowID, SessionID: checkpoint.SessionID, RunID: checkpoint.RunID, CallID: "call-1", Success: true,
	}, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	stream := make(chan provider.StreamEvent, 2)
	stream <- provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{
		"name": "resume-flow", "session_id": checkpoint.SessionID, "run_id": checkpoint.RunID,
	}}
	stream <- provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{"session_id": checkpoint.SessionID, "run_id": checkpoint.RunID}}
	close(stream)
	providerStub.stream = stream
	worker.agentLoop.discovery <- struct{}{}
	worker.discoverAgentLoopCheckpoints(context.Background())
	requests, operations := providerStub.snapshot()
	if len(requests) != 1 {
		t.Fatalf("expected one discovery resume, got %#v", requests)
	}
	if len(operations) < 3 || operations[0] != "callback" || operations[1] != "checkpoints" || operations[len(operations)-1] != "stream" {
		t.Fatalf("unexpected recovery order: %#v", operations)
	}
	if _, err := store.LoadToolCallback(checkpoint.FlowID, checkpoint.SessionID, checkpoint.RunID, "call-1"); err == nil {
		t.Fatal("successful callback flush should remove pending result")
	}
}
