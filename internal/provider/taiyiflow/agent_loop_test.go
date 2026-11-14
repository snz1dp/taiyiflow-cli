package taiyiflow

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func TestAgentLoopControlAndSessionExecution(t *testing.T) {
	var controlBodies []agentLoopControlRequestBody
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/sessions/session-1"):
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"id":"session-1","flow_id":"flow-1","executing":true,"executing_run_id":"run-1","execution_attempt_id":"attempt-1","run_status":"agent_loop_running","origin_client_id":"client-1","origin_device_id":"device-1","current_event_id":"event-8","current_event_seq":8}`))
		case backendAPIPath("/run/sessions/session-1/pause"), backendAPIPath("/run/sessions/session-1/cancel"):
			var body agentLoopControlRequestBody
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatalf("decode control request: %v", err)
			}
			controlBodies = append(controlBodies, body)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"session_id":"session-1","run_id":"run-1","status":"pause_requested","request_id":"request-1","checkpoint_id":"checkpoint-1","subtask_cancel_requested":2,"subtask_cancel_pending":1,"subtask_cancel_failed":false}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	execution, err := client.GetSessionExecution(context.Background(), "session-1")
	if err != nil {
		t.Fatalf("GetSessionExecution returned error: %v", err)
	}
	if execution.ExecutionAttemptID != "attempt-1" || execution.CurrentEventID != "event-8" || execution.CurrentEventSeq != 8 {
		t.Fatalf("unexpected execution metadata: %#v", execution)
	}
	control := provider.AgentLoopControlRequest{RunID: "run-1", RequestID: "request-1", ClientID: "client-1", DeviceID: "device-1"}
	paused, err := client.PauseAgentLoop(context.Background(), "session-1", control)
	if err != nil {
		t.Fatalf("PauseAgentLoop returned error: %v", err)
	}
	if paused.CheckpointID != "checkpoint-1" || paused.SubtaskCancelRequested != 2 || paused.SubtaskCancelPending != 1 {
		t.Fatalf("unexpected pause response: %#v", paused)
	}
	if _, err := client.CancelAgentLoop(context.Background(), "session-1", control); err != nil {
		t.Fatalf("CancelAgentLoop returned error: %v", err)
	}
	if len(controlBodies) != 2 {
		t.Fatalf("expected two control requests, got %d", len(controlBodies))
	}
	for _, body := range controlBodies {
		if body.RunID != "run-1" || body.RequestID != "request-1" || body.ClientID != "client-1" || body.DeviceID != "device-1" {
			t.Fatalf("unexpected control body: %#v", body)
		}
	}
}

func TestListResumableAgentLoopCheckpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/checkpoints/resumable") {
			http.NotFound(writer, request)
			return
		}
		query := request.URL.Query()
		if query.Get("client_id") != "client-1" || query.Get("device_id") != "device-1" || query.Get("flow_id") != "flow-1" || query.Get("session_id") != "session-1" {
			t.Errorf("unexpected checkpoint query: %s", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[{"id":"checkpoint-1","flow_id":"flow-1","session_id":"session-1","run_id":"run-1","origin_client_id":"client-1","origin_device_id":"device-1","status":"paused","version":3,"resume_stage":"network_freezing","pause_reason":"network","requires_user_resume":false,"waiting_call_ids":["call-1"],"pending_call_ids":["call-2"],"uncertain_call_ids":["call-3"],"updated_at":"2026-11-13T09:00:00Z","expires_at":"2026-11-14T09:00:00Z"}]`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	checkpoints, err := client.ListResumableAgentLoopCheckpoints(context.Background(), provider.AgentLoopCheckpointQuery{
		ClientID: "client-1", DeviceID: "device-1", FlowID: "flow-1", SessionID: "session-1",
	})
	if err != nil {
		t.Fatalf("ListResumableAgentLoopCheckpoints returned error: %v", err)
	}
	if len(checkpoints) != 1 {
		t.Fatalf("expected one checkpoint, got %#v", checkpoints)
	}
	checkpoint := checkpoints[0]
	if checkpoint.ID != "checkpoint-1" || checkpoint.Version != 3 || checkpoint.RequiresUserResume {
		t.Fatalf("unexpected checkpoint: %#v", checkpoint)
	}
	if len(checkpoint.WaitingCallIDs) != 1 || len(checkpoint.PendingCallIDs) != 1 || len(checkpoint.UncertainCallIDs) != 1 {
		t.Fatalf("unexpected pending call sets: %#v", checkpoint)
	}
	if checkpoint.UpdatedAt.Location() != time.UTC || checkpoint.ExpiresAt.Location() != time.UTC {
		t.Fatalf("expected UTC timestamps, got %#v", checkpoint)
	}
}

func TestStreamRequestExecutionFields(t *testing.T) {
	body := buildStreamRequestBody(provider.StreamRequest{
		ExecutionMode:      provider.ExecutionModeResume,
		ExecutionAttemptID: "attempt-1",
		CheckpointID:       "checkpoint-1",
		CheckpointVersion:  3,
		AfterEventID:       "event-8",
		SubscriptionID:     "subscription-1",
		Model:              "model-1",
	})
	if body.ExecutionMode != provider.ExecutionModeResume || body.ExecutionAttemptID != "attempt-1" || body.CheckpointID != "checkpoint-1" || body.CheckpointVersion != 3 || body.AfterEventID != "event-8" || body.SubscriptionID != "subscription-1" || body.Model != "model-1" {
		t.Fatalf("unexpected execution request body: %#v", body)
	}
	content, err := json.Marshal(buildStreamRequestBody(provider.StreamRequest{}))
	if err != nil {
		t.Fatalf("marshal empty execution fields: %v", err)
	}
	for _, field := range []string{"execution_mode", "execution_attempt_id", "checkpoint_id", "checkpoint_version", "after_event_id", "subscription_id", "model"} {
		if strings.Contains(string(content), `"`+field+`"`) {
			t.Fatalf("expected empty %s to be omitted: %s", field, content)
		}
	}
}

func TestStreamEventIDFromSSEAndWebSocket(t *testing.T) {
	output := make(chan provider.StreamEvent, 2)
	readSSE(context.Background(), strings.NewReader("id: event-sse\nevent: message\ndata: {\"event_id\":\"event-payload\",\"chunk\":\"one\"}\n\n"), output)
	close(output)
	event := <-output
	if event.EventID() != "event-sse" {
		t.Fatalf("expected SSE id to take precedence, got %#v", event)
	}

	event, ok := parseWebSocketStreamMessage([]byte(`{"id":"event-ws","event":"message","data":{"event_id":"event-payload","chunk":"one"}}`))
	if !ok || event.EventID() != "event-ws" {
		t.Fatalf("expected WebSocket id to be preserved, got %#v, %v", event, ok)
	}
	event, ok = parseWebSocketStreamMessage([]byte(`{"event":"message","data":{"event_id":"event-payload","chunk":"one"}}`))
	if !ok || event.EventID() != "event-payload" {
		t.Fatalf("expected WebSocket payload event id fallback, got %#v, %v", event, ok)
	}
}

func TestAgentLoopControlPreservesHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"detail":"run mismatch"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	_, err := client.CancelAgentLoop(context.Background(), "session-1", provider.AgentLoopControlRequest{RunID: "run-1"})
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected HTTPError, got %T: %v", err, err)
	}
	if httpErr.StatusCode != http.StatusConflict {
		t.Fatalf("expected status 409, got %d", httpErr.StatusCode)
	}
}
