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
		accessToken      = "access-1"
		clientID         = "cli-123"
		deviceID         = "device-456"
		parentFlowID     = "flow-parent-e2e"
		workerFlowID     = "flow-worker-e2e"
		childSessionID   = "child-session-e2e"
		parentSessionID  = "parent-session-e2e"
		parentRunID      = "parent-run-e2e"
		parentMessageID  = "parent-message-e2e"
		childMessageID   = "child-message-e2e"
		collaborationID  = "collab-e2e-1"
		workerSessionID  = "worker-session-e2e"
	)

	var (
		stateMu        sync.Mutex
		workerConn     net.Conn
		workerWriteMu  sync.Mutex
		workerReadyOnce sync.Once
		workerReadyCh  = make(chan struct{})
		handlerErrCh   = make(chan error, 1)
		streamCalls    int
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

	sendWorkerEnvelope := func(event string, payload map[string]any) error {
		workerWriteMu.Lock()
		defer workerWriteMu.Unlock()
		stateMu.Lock()
		conn := workerConn
		stateMu.Unlock()
		if conn == nil {
			return fmt.Errorf("worker connection unavailable")
		}
		raw, err := json.Marshal(map[string]any{
			"type":    collaborationMessageType,
			"event":   event,
			"payload": payload,
		})
		if err != nil {
			return err
		}
		return wsutil.WriteServerText(conn, raw)
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

		case request.URL.Path == internalconfig.DefaultBackendAPIPrefix+"/agents/client/websocket":
			if request.URL.Query().Get("client_id") != clientID || request.URL.Query().Get("device_id") != deviceID {
				recordHandlerErr(fmt.Errorf("unexpected worker identity query: %s", request.URL.RawQuery))
			}
			if request.Header.Get("Authorization") != "Bearer "+accessToken {
				recordHandlerErr(fmt.Errorf("unexpected websocket authorization: %s", request.Header.Get("Authorization")))
			}
			conn, _, _, err := ws.UpgradeHTTP(request, writer)
			if err != nil {
				recordHandlerErr(err)
				return
			}
			stateMu.Lock()
			workerConn = conn
			stateMu.Unlock()
			defer func() {
				stateMu.Lock()
				if workerConn == conn {
					workerConn = nil
				}
				stateMu.Unlock()
				_ = conn.Close()
			}()

			if err := sendWorkerEnvelope(collaborationEventConnectionReady, map[string]any{
				"client_id":  clientID,
				"device_id":  deviceID,
				"session_id": workerSessionID,
			}); err != nil {
				recordHandlerErr(err)
				return
			}

			fetchRecentRaw, _, err := wsutil.ReadClientData(conn)
			if err != nil {
				recordHandlerErr(err)
				return
			}
			var fetchRecentEnvelope collaborationEnvelope
			if err := json.Unmarshal(fetchRecentRaw, &fetchRecentEnvelope); err != nil {
				recordHandlerErr(err)
				return
			}
			if fetchRecentEnvelope.Event != collaborationEventTasksFetchRecent {
				recordHandlerErr(fmt.Errorf("unexpected first worker event: %s", fetchRecentEnvelope.Event))
				return
			}
			if err := sendWorkerEnvelope(collaborationEventTasksRecent, map[string]any{"tasks": []map[string]any{}}); err != nil {
				recordHandlerErr(err)
				return
			}
			workerReadyOnce.Do(func() {
				close(workerReadyCh)
			})

			for {
				payloadRaw, _, readErr := wsutil.ReadClientData(conn)
				if readErr != nil {
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
				if envelope.Event == collaborationEventConnectionPing {
					if err := sendWorkerEnvelope(collaborationEventConnectionPong, map[string]any{}); err != nil {
						recordHandlerErr(err)
						return
					}
					continue
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
				case collaborationEventTaskError:
					taskState.Status = "failed"
					taskState.StatusPayload = payload
					taskState.ErrorMessage = firstNonEmptySubtask(strings.TrimSpace(stringValue(payload["error"])), strings.TrimSpace(stringValue(payload["message"])))
					taskState.FinishedAt = now
					taskState.UpdatedAt = now
				default:
					stateMu.Unlock()
					recordHandlerErr(fmt.Errorf("unexpected worker callback event: %s", envelope.Event))
					return
				}
				stateMu.Unlock()
			}

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
				DeliveryPolicy:  firstNonEmptySubtask(strings.TrimSpace(stringValue(createBody["delivery_policy"])), "fail_if_offline"),
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
			if err := sendWorkerEnvelope(collaborationEventTaskDispatch, map[string]any{
				"task_id":           taskSnapshot.ID,
				"task_kind":         taskSnapshot.TaskKind,
				"delivery_policy":   taskSnapshot.DeliveryPolicy,
				"source_session_id": taskSnapshot.SourceSessionID,
				"source_run_id":     taskSnapshot.SourceRunID,
				"source_message_id": taskSnapshot.SourceMessageID,
				"child_session_id":  taskSnapshot.ChildSessionID,
				"child_run_id":      taskSnapshot.ChildRunID,
				"target_client_id":  taskSnapshot.TargetClientID,
				"target_device_id":  taskSnapshot.TargetDeviceID,
				"command":           taskSnapshot.CommandPayload,
			}); err != nil {
				recordHandlerErr(err)
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
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

	workerCtx, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()
	workerErrCh := make(chan error, 1)
	go func() {
		workerErrCh <- workerRuntime.RunCollaborationWorker(workerCtx, providerClient, &config, CollaborationWorkerOptions{
			RecentTaskLimit: 1,
			MaxConcurrency:  1,
			ReconnectDelay:  time.Hour,
			PingInterval:    time.Hour,
		})
	}()

	select {
	case <-workerReadyCh:
	case err := <-handlerErrCh:
		t.Fatalf("test backend failed before worker ready: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("等待 worker 就绪超时")
	}

	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerClient, config, map[string]any{
		"flow_id": parentFlowID,
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": parentSessionID, "run_id": parentRunID, "message_id": parentMessageID},
		"function": map[string]any{
			"name": "run_subtask",
			"arguments": map[string]any{
				"prompt": "请总结当前协同执行",
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

	cancelWorker()
	select {
	case workerErr := <-workerErrCh:
		if workerErr != nil && !errors.Is(workerErr, context.Canceled) {
			t.Fatalf("unexpected worker error: %v", workerErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("等待 worker 退出超时")
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
		accessToken      = "access-local-1"
		clientID         = "cli-local-123"
		deviceID         = "device-local-456"
		parentFlowID     = "flow-parent-local"
		workerFlowID     = "flow-local-fallback"
		childSessionID   = "child-session-local"
		parentSessionID  = "parent-session-local"
		parentRunID      = "parent-run-local"
		parentMessageID  = "parent-message-local"
		childMessageID   = "child-message-local"
		collaborationID  = "collab-local-1"
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
				DeliveryPolicy:  firstNonEmptySubtask(strings.TrimSpace(stringValue(createBody["delivery_policy"])), "fail_if_offline"),
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

	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerClient, config, map[string]any{
		"flow_id": parentFlowID,
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": parentSessionID, "run_id": parentRunID, "message_id": parentMessageID},
		"function": map[string]any{
			"name": "run_subtask",
			"arguments": map[string]any{
				"prompt": "请总结当前本地回退执行",
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
			DeliveryPolicy:  "fail_if_offline",
			CommandPayload: map[string]any{
				"prompt":            "请总结当前协同执行",
				"flow_id":           "flow-worker",
				"request_mode":      "agent",
				"working_directory": tempDir,
			},
		},
	}

	var (
		capturedEvents []string
		eventsMu       sync.Mutex
	)
	resultPayloadCh := make(chan map[string]any, 1)
	handlerErrCh := make(chan error, 1)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != internalconfig.DefaultBackendAPIPrefix+"/agents/client/websocket" {
			select {
			case handlerErrCh <- fmt.Errorf("unexpected websocket path: %s", request.URL.Path):
			default:
			}
			http.NotFound(writer, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer access-1" {
			select {
			case handlerErrCh <- fmt.Errorf("unexpected authorization header: %s", request.Header.Get("Authorization")):
			default:
			}
		}
		if request.URL.Query().Get("client_id") != "cli-123" || request.URL.Query().Get("device_id") != "device-456" {
			select {
			case handlerErrCh <- fmt.Errorf("unexpected client identity query: %s", request.URL.RawQuery):
			default:
			}
		}
		conn, _, _, err := ws.UpgradeHTTP(request, writer)
		if err != nil {
			select {
			case handlerErrCh <- err:
			default:
			}
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
			select {
			case handlerErrCh <- err:
			default:
			}
			return
		}
		if err := wsutil.WriteServerText(conn, readyPayload); err != nil {
			select {
			case handlerErrCh <- err:
			default:
			}
			return
		}

		fetchPayloadRaw, _, err := wsutil.ReadClientData(conn)
		if err != nil {
			select {
			case handlerErrCh <- err:
			default:
			}
			return
		}
		var fetchEnvelope collaborationEnvelope
		if err := json.Unmarshal(fetchPayloadRaw, &fetchEnvelope); err != nil {
			select {
			case handlerErrCh <- err:
			default:
			}
			return
		}
		if fetchEnvelope.Event != collaborationEventTasksFetchRecent {
			select {
			case handlerErrCh <- fmt.Errorf("unexpected first event: %s", fetchEnvelope.Event):
			default:
			}
			return
		}

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
			select {
			case handlerErrCh <- err:
			default:
			}
			return
		}
		if err := wsutil.WriteServerText(conn, recentPayload); err != nil {
			select {
			case handlerErrCh <- err:
			default:
			}
			return
		}

		for {
			payloadRaw, _, readErr := wsutil.ReadClientData(conn)
			if readErr != nil {
				return
			}
			var envelope collaborationEnvelope
			if err := json.Unmarshal(payloadRaw, &envelope); err != nil {
				select {
				case handlerErrCh <- err:
				default:
				}
				return
			}
			eventsMu.Lock()
			capturedEvents = append(capturedEvents, envelope.Event)
			eventsMu.Unlock()
			switch envelope.Event {
			case collaborationEventTaskAck, collaborationEventTaskStatus:
				continue
			case collaborationEventTaskResult:
				var payload map[string]any
				if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
					select {
					case handlerErrCh <- err:
					default:
					}
					return
				}
				resultMap, _ := payload["result"].(map[string]any)
				resultPayloadCh <- resultMap
				return
			case collaborationEventTaskError:
				var payload map[string]any
				if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
					select {
					case handlerErrCh <- err:
					default:
					}
					return
				}
				select {
				case handlerErrCh <- fmt.Errorf("worker returned task.error: %#v", payload):
				default:
				}
				return
			case collaborationEventConnectionPing:
				pongPayload, err := json.Marshal(map[string]any{
					"type":    collaborationMessageType,
					"event":   collaborationEventConnectionPong,
					"payload": map[string]any{},
				})
				if err != nil {
					select {
					case handlerErrCh <- err:
					default:
					}
					return
				}
				if err := wsutil.WriteServerText(conn, pongPayload); err != nil {
					select {
					case handlerErrCh <- err:
					default:
					}
					return
				}
			default:
				select {
				case handlerErrCh <- fmt.Errorf("unexpected client event: %s", envelope.Event):
				default:
				}
				return
			}
		}
	}))
	defer server.Close()

	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "access-1"
	config.ClientID = "cli-123"
	config.DeviceID = "device-456"
	config.Workdir = tempDir

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- runtimeClient.RunCollaborationWorker(ctx, providerStub, &config, CollaborationWorkerOptions{
			RecentTaskLimit: 2,
			MaxConcurrency:  1,
			ReconnectDelay:  20 * time.Millisecond,
			PingInterval:    time.Hour,
		})
	}()

	select {
	case handlerErr := <-handlerErrCh:
		t.Fatalf("websocket handler failed: %v", handlerErr)
	case resultPayload := <-resultPayloadCh:
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
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("等待协同 worker 结果超时")
	}

	select {
	case handlerErr := <-handlerErrCh:
		t.Fatalf("websocket handler failed: %v", handlerErr)
	default:
	}

	select {
	case runErr := <-errCh:
		if runErr != nil && !errors.Is(runErr, context.Canceled) {
			t.Fatalf("unexpected worker error: %v", runErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("协同 worker 未按预期退出")
	}

	eventsMu.Lock()
	defer eventsMu.Unlock()
	joinedEvents := strings.Join(capturedEvents, ",")
	if joinedEvents != collaborationEventTaskAck+","+collaborationEventTaskStatus+","+collaborationEventTaskResult {
		t.Fatalf("unexpected worker event sequence: %s", joinedEvents)
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