package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

func TestSelfTargetRunSubtaskUsesOnlineWorkerOnlyOnceEndToEnd(t *testing.T) {
	tempDir := t.TempDir()
	const (
		accessToken     = "access-1"
		clientID        = "cli-123"
		deviceID        = "device-456"
		parentFlowID    = "flow-parent-e2e"
		workerFlowID    = "flow-worker-e2e"
		childSessionID  = "child-session-e2e"
		parentSessionID = "parent-session-e2e"
		parentRunID     = "parent-run-e2e"
		parentMessageID = "parent-message-e2e"
		childMessageID  = "child-message-e2e"
		collaborationID = "collab-e2e-1"
		workerSessionID = "worker-session-e2e"
	)

	var (
		stateMu          sync.Mutex
		handlerErrCh     = make(chan error, 1)
		streamCalls      int
		taskState        provider.ClientCollaborationTask
		taskCreated      bool
		dispatchToWorker func(provider.ClientCollaborationTask)
	)

	recordHandlerErr := func(err error) {
		if err == nil {
			return
		}
		select {
		case handlerErrCh <- err:
		default:
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == internalconfig.DefaultBackendAPIPrefix+"/session_id":
			if request.Method != http.MethodGet {
				recordHandlerErr(fmt.Errorf("unexpected session_id method: %s", request.Method))
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				recordHandlerErr(fmt.Errorf("unexpected session_id authorization: %s", request.Header.Get("Authorization")))
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `{"session_id":%q}`, childSessionID)
			return

		case request.URL.Path == internalconfig.DefaultBackendAPIPrefix+"/agents/client/collaborations":
			if request.Method != http.MethodPost {
				recordHandlerErr(fmt.Errorf("unexpected create collaboration method: %s", request.Method))
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				recordHandlerErr(fmt.Errorf("unexpected create collaboration authorization: %s", request.Header.Get("Authorization")))
			}
			var createBody map[string]any
			if err := json.NewDecoder(request.Body).Decode(&createBody); err != nil {
				recordHandlerErr(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			now := time.Now().UTC()
			stateMu.Lock()
			taskCreated = true
			taskState = provider.ClientCollaborationTask{
				ID:              collaborationID,
				OwnerUserID:     "user-1",
				TargetClientID:  strings.TrimSpace(stringValue(createBody["target_client_id"])),
				TargetDeviceID:  strings.TrimSpace(stringValue(createBody["target_device_id"])),
				SourceSessionID: strings.TrimSpace(stringValue(createBody["source_session_id"])),
				SourceRunID:     strings.TrimSpace(stringValue(createBody["source_run_id"])),
				SourceMessageID: strings.TrimSpace(stringValue(createBody["source_message_id"])),
				ChildSessionID:  strings.TrimSpace(stringValue(createBody["child_session_id"])),
				ChildRunID:      strings.TrimSpace(stringValue(createBody["child_run_id"])),
				TaskKind:        firstNonEmptySubtask(strings.TrimSpace(stringValue(createBody["task_kind"])), "dialogue_subtask"),
				DeliveryPolicy:  firstNonEmptySubtask(strings.TrimSpace(stringValue(createBody["delivery_policy"])), "local_default"),
				Status:          collaborationWorkerRecoveryStatusDispatch,
				CommandPayload:  nestedMap(createBody, "command_payload"),
				StatusPayload:   map[string]any{"delivery_channel": "client_websocket"},
				DispatchedAt:    now,
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			taskSnapshot := taskState
			stateMu.Unlock()
			if taskSnapshot.TargetClientID != clientID || taskSnapshot.TargetDeviceID != deviceID {
				recordHandlerErr(fmt.Errorf("unexpected target identity in create request: %#v", createBody))
			}
			if taskSnapshot.SourceSessionID != parentSessionID || taskSnapshot.SourceRunID != parentRunID || taskSnapshot.SourceMessageID != parentMessageID {
				recordHandlerErr(fmt.Errorf("unexpected parent lineage in create request: %#v", createBody))
			}
			if taskSnapshot.ChildSessionID != childSessionID {
				recordHandlerErr(fmt.Errorf("unexpected child session id in create request: %#v", createBody))
			}
			if dispatchToWorker == nil {
				recordHandlerErr(fmt.Errorf("worker dispatcher unavailable"))
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			dispatchToWorker(taskSnapshot)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(collaborationTaskResponseForTest(taskSnapshot))
			return

		case strings.HasPrefix(request.URL.Path, internalconfig.DefaultBackendAPIPrefix+"/agents/client/collaborations/"):
			if request.Method != http.MethodGet {
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			stateMu.Lock()
			taskSnapshot := taskState
			created := taskCreated
			stateMu.Unlock()
			if !created || !strings.HasSuffix(request.URL.Path, collaborationID) {
				http.NotFound(writer, request)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(collaborationTaskResponseForTest(taskSnapshot))
			return

		case strings.HasPrefix(request.URL.Path, internalconfig.DefaultBackendAPIPrefix+"/run/") && strings.HasSuffix(request.URL.Path, "/stream"):
			if request.Method != http.MethodPost {
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				recordHandlerErr(fmt.Errorf("unexpected stream authorization: %s", request.Header.Get("Authorization")))
			}
			if request.Header.Get("Accept") != "text/event-stream" {
				recordHandlerErr(fmt.Errorf("unexpected stream accept header: %s", request.Header.Get("Accept")))
			}
			var streamBody map[string]any
			if err := json.NewDecoder(request.Body).Decode(&streamBody); err != nil {
				recordHandlerErr(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if request.URL.Query().Get("session_id") != childSessionID {
				recordHandlerErr(fmt.Errorf("unexpected stream session query: %s", request.URL.RawQuery))
			}
			if strings.TrimSpace(stringValue(streamBody["run_id"])) == "" {
				recordHandlerErr(fmt.Errorf("expected child run id in stream body, got %#v", streamBody))
			}
			if strings.TrimSpace(stringValue(streamBody["client_id"])) != clientID || strings.TrimSpace(stringValue(streamBody["device_id"])) != deviceID {
				recordHandlerErr(fmt.Errorf("unexpected stream identity body: %#v", streamBody))
			}
			stateMu.Lock()
			streamCalls++
			stateMu.Unlock()
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.Header().Set(providerclient.SessionHeader, childSessionID)
			flusher, ok := writer.(http.Flusher)
			if !ok {
				recordHandlerErr(fmt.Errorf("response writer does not support flushing"))
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			if err := writeSSEEventForTest(writer, string(provider.EventMessage), map[string]any{"chunk": "worker e2e 完成", "message_id": childMessageID}); err != nil {
				recordHandlerErr(err)
				return
			}
			if err := writeSSEEventForTest(writer, string(provider.EventClose), map[string]any{}); err != nil {
				recordHandlerErr(err)
				return
			}
			flusher.Flush()
			return
		}

		http.NotFound(writer, request)
	}))
	defer server.Close()

	providerClient := providerclient.NewClient(server.URL, accessToken)
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	workerRuntime := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = accessToken
	config.ClientID = clientID
	config.DeviceID = deviceID
	config.Workdir = tempDir
	config.ApprovalMode = "auto"
	dispatchToWorker = func(task provider.ClientCollaborationTask) {
		go func() {
			worker := newCollaborationWorker(workerRuntime, providerClient, &config, CollaborationWorkerOptions{
				RecentTaskLimit: 1,
				MaxConcurrency:  1,
				ReconnectDelay:  time.Hour,
				PingInterval:    time.Hour,
			})
			clientConn, serverConn := net.Pipe()
			defer clientConn.Close()
			defer serverConn.Close()

			dispatchPayload, err := json.Marshal(map[string]any{
				"type":  collaborationMessageType,
				"event": collaborationEventTaskDispatch,
				"payload": map[string]any{
					"task_id":           task.ID,
					"task_kind":         task.TaskKind,
					"delivery_policy":   task.DeliveryPolicy,
					"source_session_id": task.SourceSessionID,
					"source_run_id":     task.SourceRunID,
					"source_message_id": task.SourceMessageID,
					"child_session_id":  task.ChildSessionID,
					"child_run_id":      task.ChildRunID,
					"target_client_id":  task.TargetClientID,
					"target_device_id":  task.TargetDeviceID,
					"command":           task.CommandPayload,
				},
			})
			if err != nil {
				recordHandlerErr(err)
				return
			}
			startPing, err := worker.handleServerEnvelope(context.Background(), clientConn, dispatchPayload)
			if err != nil {
				recordHandlerErr(err)
				return
			}
			if startPing {
				recordHandlerErr(fmt.Errorf("task.dispatch should not request ping loop startup"))
				return
			}

			deadline := time.Now().Add(5 * time.Second)
			for {
				if err := serverConn.SetReadDeadline(deadline); err != nil {
					recordHandlerErr(err)
					return
				}
				payloadRaw, _, err := wsutil.ReadClientData(serverConn)
				if err != nil {
					recordHandlerErr(err)
					return
				}
				var envelope collaborationEnvelope
				if err := json.Unmarshal(payloadRaw, &envelope); err != nil {
					recordHandlerErr(err)
					return
				}
				payload := map[string]any{}
				if len(envelope.Payload) > 0 {
					if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
						recordHandlerErr(err)
						return
					}
				}
				stateMu.Lock()
				now := time.Now().UTC()
				switch envelope.Event {
				case collaborationEventTaskAck:
					taskState.Status = "acknowledged"
					taskState.StatusPayload = payload
					taskState.UpdatedAt = now
				case collaborationEventTaskStatus:
					status := strings.TrimSpace(stringValue(payload["status"]))
					if status == "" {
						status = "running"
					}
					taskState.Status = status
					taskState.StatusPayload = payload
					taskState.UpdatedAt = now
				case collaborationEventTaskResult:
					taskState.Status = "success"
					taskState.StatusPayload = payload
					resultPayload, _ := payload["result"].(map[string]any)
					taskState.ResultPayload = resultPayload
					taskState.FinishedAt = now
					taskState.UpdatedAt = now
					stateMu.Unlock()
					return
				case collaborationEventTaskError:
					taskState.Status = "failed"
					taskState.StatusPayload = payload
					taskState.ErrorMessage = firstNonEmptySubtask(strings.TrimSpace(stringValue(payload["error"])), strings.TrimSpace(stringValue(payload["message"])))
					taskState.FinishedAt = now
					taskState.UpdatedAt = now
					stateMu.Unlock()
					return
				default:
					stateMu.Unlock()
					recordHandlerErr(fmt.Errorf("unexpected worker callback event: %s", envelope.Event))
					return
				}
				stateMu.Unlock()
			}
		}()
	}

	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerClient, config, map[string]any{
		"flow_id": parentFlowID,
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": parentSessionID, "run_id": parentRunID, "message_id": parentMessageID},
		"function": map[string]any{
			"name": "run_subtask",
			"arguments": map[string]any{
				"prompt":  "请总结当前协同执行",
				"flow_id": workerFlowID,
			},
		},
	})
	if err != nil {
		t.Fatalf("expected run_subtask to succeed, got %v", err)
	}
	if toolName != "run_subtask" {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if result["status"] != "success" {
		t.Fatalf("expected remote task success result, got %#v", result)
	}
	if result["text"] != "worker e2e 完成" {
		t.Fatalf("expected worker result text, got %#v", result)
	}
	resultPayload, ok := result["result"].(map[string]any)
	if !ok || resultPayload["message_id"] != childMessageID {
		t.Fatalf("expected result payload from worker, got %#v", result)
	}

	stateMu.Lock()
	streamCallCount := streamCalls
	finalTask := taskState
	stateMu.Unlock()
	if streamCallCount != 1 {
		t.Fatalf("expected exactly one stream execution, got %d", streamCallCount)
	}
	if finalTask.Status != "success" || finalTask.ResultPayload["message_id"] != childMessageID {
		t.Fatalf("expected worker to finalize task exactly once, got %#v", finalTask)
	}

	select {
	case err := <-handlerErrCh:
		t.Fatalf("test backend failed: %v", err)
	default:
	}
}

func TestSelfTargetRunSubtaskFallsBackLocallyWithoutOnlineWorkerEndToEnd(t *testing.T) {
	tempDir := t.TempDir()
	const (
		accessToken     = "access-local-1"
		clientID        = "cli-local-123"
		deviceID        = "device-local-456"
		parentFlowID    = "flow-parent-local"
		workerFlowID    = "flow-local-fallback"
		childSessionID  = "child-session-local"
		parentSessionID = "parent-session-local"
		parentRunID     = "parent-run-local"
		parentMessageID = "parent-message-local"
		childMessageID  = "child-message-local"
		collaborationID = "collab-local-1"
	)

	var (
		stateMu        sync.Mutex
		handlerErrCh   = make(chan error, 1)
		streamCalls    int
		updateStatuses []string
		taskState      provider.ClientCollaborationTask
		taskCreated    bool
	)

	recordHandlerErr := func(err error) {
		if err == nil {
			return
		}
		select {
		case handlerErrCh <- err:
		default:
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == internalconfig.DefaultBackendAPIPrefix+"/session_id":
			if request.Method != http.MethodGet {
				recordHandlerErr(fmt.Errorf("unexpected session_id method: %s", request.Method))
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				recordHandlerErr(fmt.Errorf("unexpected session_id authorization: %s", request.Header.Get("Authorization")))
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(writer, `{"session_id":%q}`, childSessionID)
			return

		case request.URL.Path == internalconfig.DefaultBackendAPIPrefix+"/agents/client/collaborations":
			if request.Method != http.MethodPost {
				recordHandlerErr(fmt.Errorf("unexpected create collaboration method: %s", request.Method))
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				recordHandlerErr(fmt.Errorf("unexpected create collaboration authorization: %s", request.Header.Get("Authorization")))
			}
			var createBody map[string]any
			if err := json.NewDecoder(request.Body).Decode(&createBody); err != nil {
				recordHandlerErr(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			now := time.Now().UTC()
			stateMu.Lock()
			taskCreated = true
			taskState = provider.ClientCollaborationTask{
				ID:              collaborationID,
				OwnerUserID:     "user-1",
				TargetClientID:  strings.TrimSpace(stringValue(createBody["target_client_id"])),
				TargetDeviceID:  strings.TrimSpace(stringValue(createBody["target_device_id"])),
				SourceSessionID: strings.TrimSpace(stringValue(createBody["source_session_id"])),
				SourceRunID:     strings.TrimSpace(stringValue(createBody["source_run_id"])),
				SourceMessageID: strings.TrimSpace(stringValue(createBody["source_message_id"])),
				ChildSessionID:  strings.TrimSpace(stringValue(createBody["child_session_id"])),
				ChildRunID:      strings.TrimSpace(stringValue(createBody["child_run_id"])),
				TaskKind:        firstNonEmptySubtask(strings.TrimSpace(stringValue(createBody["task_kind"])), "dialogue_subtask"),
				DeliveryPolicy:  firstNonEmptySubtask(strings.TrimSpace(stringValue(createBody["delivery_policy"])), "local_default"),
				Status:          "offline",
				CommandPayload:  nestedMap(createBody, "command_payload"),
				ErrorMessage:    "目标客户端离线",
				CreatedAt:       now,
				UpdatedAt:       now,
			}
			taskSnapshot := taskState
			stateMu.Unlock()
			if taskSnapshot.TargetClientID != clientID || taskSnapshot.TargetDeviceID != deviceID {
				recordHandlerErr(fmt.Errorf("unexpected target identity in create request: %#v", createBody))
			}
			if taskSnapshot.SourceSessionID != parentSessionID || taskSnapshot.SourceRunID != parentRunID || taskSnapshot.SourceMessageID != parentMessageID {
				recordHandlerErr(fmt.Errorf("unexpected parent lineage in create request: %#v", createBody))
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(collaborationTaskResponseForTest(taskSnapshot))
			return

		case request.URL.Path == internalconfig.DefaultBackendAPIPrefix+"/agents/client/collaborations/"+collaborationID:
			if request.Method != http.MethodPatch {
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			var updateBody map[string]any
			if err := json.NewDecoder(request.Body).Decode(&updateBody); err != nil {
				recordHandlerErr(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			stateMu.Lock()
			if !taskCreated {
				stateMu.Unlock()
				http.NotFound(writer, request)
				return
			}
			status := strings.TrimSpace(stringValue(updateBody["status"]))
			if status != "" {
				taskState.Status = status
				updateStatuses = append(updateStatuses, status)
				if status == "running" || status == "success" {
					taskState.ErrorMessage = ""
				}
			}
			if statusPayload := nestedMap(updateBody, "status_payload"); len(statusPayload) > 0 {
				taskState.StatusPayload = statusPayload
			}
			if resultPayload := nestedMap(updateBody, "result_payload"); len(resultPayload) > 0 {
				taskState.ResultPayload = resultPayload
			}
			if errorMessage := strings.TrimSpace(stringValue(updateBody["error_message"])); errorMessage != "" {
				taskState.ErrorMessage = errorMessage
			}
			now := time.Now().UTC()
			taskState.UpdatedAt = now
			if taskState.Status == "success" || taskState.Status == "failed" || taskState.Status == "offline" {
				taskState.FinishedAt = now
			}
			taskSnapshot := taskState
			stateMu.Unlock()
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(collaborationTaskResponseForTest(taskSnapshot))
			return

		case strings.HasPrefix(request.URL.Path, internalconfig.DefaultBackendAPIPrefix+"/run/") && strings.HasSuffix(request.URL.Path, "/stream"):
			if request.Method != http.MethodPost {
				writer.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				recordHandlerErr(fmt.Errorf("unexpected stream authorization: %s", request.Header.Get("Authorization")))
			}
			var streamBody map[string]any
			if err := json.NewDecoder(request.Body).Decode(&streamBody); err != nil {
				recordHandlerErr(err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if request.URL.Query().Get("session_id") != childSessionID {
				recordHandlerErr(fmt.Errorf("unexpected stream session query: %s", request.URL.RawQuery))
			}
			if strings.TrimSpace(stringValue(streamBody["client_id"])) != clientID || strings.TrimSpace(stringValue(streamBody["device_id"])) != deviceID {
				recordHandlerErr(fmt.Errorf("unexpected stream identity body: %#v", streamBody))
			}
			stateMu.Lock()
			streamCalls++
			childRunID := taskState.ChildRunID
			stateMu.Unlock()
			if strings.TrimSpace(stringValue(streamBody["run_id"])) != childRunID {
				recordHandlerErr(fmt.Errorf("unexpected child run id in stream body: %#v", streamBody))
			}
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.Header().Set(providerclient.SessionHeader, childSessionID)
			flusher, ok := writer.(http.Flusher)
			if !ok {
				recordHandlerErr(fmt.Errorf("response writer does not support flushing"))
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			if err := writeSSEEventForTest(writer, string(provider.EventMessage), map[string]any{"chunk": "本地 fallback 完成", "message_id": childMessageID}); err != nil {
				recordHandlerErr(err)
				return
			}
			if err := writeSSEEventForTest(writer, string(provider.EventClose), map[string]any{}); err != nil {
				recordHandlerErr(err)
				return
			}
			flusher.Flush()
			return
		}

		http.NotFound(writer, request)
	}))
	defer server.Close()

	providerClient := providerclient.NewClient(server.URL, accessToken)
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = accessToken
	config.ClientID = clientID
	config.DeviceID = deviceID
	config.Workdir = tempDir
	config.ApprovalMode = "auto"

	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(
		context.Background(), providerClient, config, map[string]any{
			"flow_id": parentFlowID,
			"server":  map[string]any{"name": "local"},
			"context": map[string]any{
				"session_id": parentSessionID,
				"run_id":     parentRunID,
				"message_id": parentMessageID,
			},
			"function": map[string]any{
				"name": "run_subtask",
				"arguments": map[string]any{
					"prompt":  "请总结当前本地回退执行",
					"flow_id": workerFlowID,
				},
			},
		})
	if err != nil {
		t.Fatalf("expected run_subtask local fallback to succeed, got %v", err)
	}
	if toolName != "run_subtask" {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if result["status"] != "success" || result["text"] != "本地 fallback 完成" {
		t.Fatalf("unexpected local fallback result: %#v", result)
	}
	if result["child_message_id"] != childMessageID {
		t.Fatalf("expected child message id in local fallback result, got %#v", result)
	}

	stateMu.Lock()
	streamCallCount := streamCalls
	finalTask := taskState
	statusSequence := append([]string(nil), updateStatuses...)
	stateMu.Unlock()
	if streamCallCount != 1 {
		t.Fatalf("expected exactly one local stream execution, got %d", streamCallCount)
	}
	if strings.Join(statusSequence, ",") != "running,success" {
		t.Fatalf("unexpected local fallback status sequence: %#v", statusSequence)
	}
	if finalTask.Status != "success" || finalTask.ResultPayload["message_id"] != childMessageID {
		t.Fatalf("expected local fallback to finalize task successfully, got %#v", finalTask)
	}

	select {
	case err := <-handlerErrCh:
		t.Fatalf("test backend failed: %v", err)
	default:
	}
}

func TestRunCollaborationWorkerFetchesRecentTasksAndReportsLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	providerStub := &subtaskProviderStub{
		stream: streamWithEvents(
			provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "worker 执行完成", "message_id": "child-message-3"}},
			provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}},
		),
		streamSession: &provider.StreamSession{SessionID: "child-session-3"},
		currentTask: provider.ClientCollaborationTask{
			ID:              "task-1",
			Status:          collaborationWorkerRecoveryStatusDispatch,
			TargetClientID:  "cli-123",
			TargetDeviceID:  "device-456",
			SourceSessionID: "parent-session-3",
			SourceRunID:     "parent-run-3",
			SourceMessageID: "parent-message-3",
			ChildSessionID:  "child-session-3",
			ChildRunID:      "child-run-3",
			TaskKind:        "dialogue_subtask",
			DeliveryPolicy:  "local_default",
			CommandPayload: map[string]any{
				"prompt":            "请总结当前协同执行",
				"flow_id":           "flow-worker",
				"request_mode":      "agent",
				"working_directory": tempDir,
			},
		},
	}

	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	config := internalconfig.Default()
	config.ClientID = "cli-123"
	config.DeviceID = "device-456"
	config.Workdir = tempDir

	worker := newCollaborationWorker(runtimeClient, providerStub, &config, CollaborationWorkerOptions{
		RecentTaskLimit: 2,
		MaxConcurrency:  1,
		ReconnectDelay:  time.Hour,
		PingInterval:    time.Hour,
	})

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	recentPayload, err := json.Marshal(map[string]any{
		"type":  collaborationMessageType,
		"event": collaborationEventTasksRecent,
		"payload": map[string]any{
			"tasks": []map[string]any{{
				"task_id": "task-1",
				"status":  collaborationWorkerRecoveryStatusDispatch,
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal recent payload failed: %v", err)
	}

	startPing, err := worker.handleServerEnvelope(context.Background(), clientConn, recentPayload)
	if err != nil {
		t.Fatalf("handleServerEnvelope failed: %v", err)
	}
	if startPing {
		t.Fatal("tasks.recent should not request ping loop startup")
	}

	deadline := time.Now().Add(3 * time.Second)
	capturedEvents := make([]string, 0, 3)
	var resultPayload map[string]any
	for len(capturedEvents) < 3 {
		if err := serverConn.SetReadDeadline(deadline); err != nil {
			t.Fatalf("set read deadline failed: %v", err)
		}
		payloadRaw, _, err := wsutil.ReadClientData(serverConn)
		if err != nil {
			t.Fatalf("read worker envelope failed: %v", err)
		}
		var envelope collaborationEnvelope
		if err := json.Unmarshal(payloadRaw, &envelope); err != nil {
			t.Fatalf("unmarshal worker envelope failed: %v", err)
		}
		capturedEvents = append(capturedEvents, envelope.Event)
		switch envelope.Event {
		case collaborationEventTaskAck:
			var payload map[string]any
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				t.Fatalf("unmarshal task ack payload failed: %v", err)
			}
			if payload["task_id"] != "task-1" {
				t.Fatalf("expected task ack for task-1, got %#v", payload)
			}
		case collaborationEventTaskStatus:
			var payload map[string]any
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				t.Fatalf("unmarshal task status payload failed: %v", err)
			}
			if payload["task_id"] != "task-1" || payload["status"] != "running" || payload["executor"] != collaborationWorkerExecutor {
				t.Fatalf("unexpected task status payload: %#v", payload)
			}
		case collaborationEventTaskResult:
			var payload map[string]any
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				t.Fatalf("unmarshal task result payload failed: %v", err)
			}
			if payload["task_id"] != "task-1" || payload["status"] != "success" || payload["executor"] != collaborationWorkerExecutor {
				t.Fatalf("unexpected task result payload: %#v", payload)
			}
			var ok bool
			resultPayload, ok = payload["result"].(map[string]any)
			if !ok {
				t.Fatalf("expected result payload map, got %#v", payload)
			}
		default:
			t.Fatalf("unexpected worker event: %s", envelope.Event)
		}
	}

	joinedEvents := strings.Join(capturedEvents, ",")
	if joinedEvents != collaborationEventTaskAck+","+collaborationEventTaskStatus+","+collaborationEventTaskResult {
		t.Fatalf("unexpected worker event sequence: %s", joinedEvents)
	}
	if resultPayload["text"] != "worker 执行完成" {
		t.Fatalf("unexpected result payload: %#v", resultPayload)
	}
	if resultPayload["session_id"] != "child-session-3" || resultPayload["run_id"] != "child-run-3" {
		t.Fatalf("expected existing child lineage in result payload, got %#v", resultPayload)
	}
	if resultPayload["message_id"] != "child-message-3" {
		t.Fatalf("expected child message id in result payload, got %#v", resultPayload)
	}
	expectedWorkdir, err := filepath.EvalSymlinks(tempDir)
	if err != nil {
		t.Fatalf("resolve temp dir failed: %v", err)
	}
	actualWorkdir := filepath.Clean(stringValue(resultPayload["working_directory"]))
	if resultPayload["flow_id"] != "flow-worker" || actualWorkdir != expectedWorkdir {
		t.Fatalf("expected command metadata in result payload, got %#v", resultPayload)
	}

	providerStub.mu.Lock()
	getTaskIDs := append([]string(nil), providerStub.getTaskIDs...)
	providerStub.mu.Unlock()
	if len(getTaskIDs) != 1 || getTaskIDs[0] != "task-1" {
		t.Fatalf("expected tasks.recent to load task-1 once, got %#v", getTaskIDs)
	}
	if providerStub.lastStream.SessionID != "child-session-3" || providerStub.lastStream.RunID != "child-run-3" {
		t.Fatalf("expected worker to reuse child lineage in stream request, got %#v", providerStub.lastStream)
	}
}

func TestRunCollaborationWorkerStopsOnContextCancelWhileIdle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		conn, _, _, err := ws.UpgradeHTTP(request, writer)
		if err != nil {
			return
		}
		defer conn.Close()

		readyPayload, err := json.Marshal(map[string]any{
			"type":  collaborationMessageType,
			"event": collaborationEventConnectionReady,
			"payload": map[string]any{
				"client_id":  "cli-123",
				"device_id":  "device-456",
				"session_id": "worker-session",
			},
		})
		if err != nil {
			return
		}
		if err := wsutil.WriteServerText(conn, readyPayload); err != nil {
			return
		}

		_, _, _ = wsutil.ReadClientData(conn)
		select {}
	}))
	defer server.Close()

	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "access-1"
	config.ClientID = "cli-123"
	config.DeviceID = "device-456"

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runtimeClient.RunCollaborationWorker(ctx, &subtaskProviderStub{}, &config, CollaborationWorkerOptions{
			ReconnectDelay: time.Hour,
			PingInterval:   time.Hour,
		})
	}()

	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected worker to exit after context cancellation")
	}
}

func collaborationTaskResponseForTest(task provider.ClientCollaborationTask) map[string]any {
	return map[string]any{
		"id":                task.ID,
		"owner_user_id":     task.OwnerUserID,
		"target_client_id":  task.TargetClientID,
		"target_device_id":  task.TargetDeviceID,
		"source_session_id": task.SourceSessionID,
		"source_run_id":     task.SourceRunID,
		"source_message_id": task.SourceMessageID,
		"child_session_id":  task.ChildSessionID,
		"child_run_id":      task.ChildRunID,
		"task_kind":         task.TaskKind,
		"delivery_policy":   task.DeliveryPolicy,
		"status":            task.Status,
		"command_payload":   task.CommandPayload,
		"status_payload":    task.StatusPayload,
		"result_payload":    task.ResultPayload,
		"error_message":     task.ErrorMessage,
		"dispatched_at":     formatCollaborationTaskTimeForTest(task.DispatchedAt),
		"finished_at":       formatCollaborationTaskTimeForTest(task.FinishedAt),
		"expired_at":        formatCollaborationTaskTimeForTest(task.ExpiredAt),
		"created_at":        formatCollaborationTaskTimeForTest(task.CreatedAt),
		"updated_at":        formatCollaborationTaskTimeForTest(task.UpdatedAt),
	}
}

func formatCollaborationTaskTimeForTest(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func writeSSEEventForTest(writer io.Writer, event string, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", strings.TrimSpace(event), raw)
	return err
}
