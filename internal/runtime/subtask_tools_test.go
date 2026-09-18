package runtime

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

type subtaskProviderStub struct {
	provider.Provider
	mu                       sync.Mutex
	tasks                    map[string]provider.ClientCollaborationTask
	createdSessionID         string
	nextSessionID            int
	createdTaskStatus        string
	createdTaskStatusPayload map[string]any
	createdRequest           provider.ClientCollaborationCreateRequest
	createdRequests          []provider.ClientCollaborationCreateRequest
	getTaskIDs               []string
	updatedTaskIDs           []string
	updateRequests           []provider.ClientCollaborationUpdateRequest
	stream                   <-chan provider.StreamEvent
	streams                  []<-chan provider.StreamEvent
	streamSession            *provider.StreamSession
	streamSessions           []*provider.StreamSession
	streamCalls              int
	lastStream               provider.StreamRequest
	currentTask              provider.ClientCollaborationTask
}

func (s *subtaskProviderStub) CreateSessionID(context.Context) (string, error) {
	if strings.TrimSpace(s.createdSessionID) != "" {
		return s.createdSessionID, nil
	}
	s.nextSessionID++
	return fmt.Sprintf("child-session-%d", s.nextSessionID), nil
}

func (s *subtaskProviderStub) CreateClientCollaboration(_ context.Context, request provider.ClientCollaborationCreateRequest) (*provider.ClientCollaborationTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createdRequest = request
	s.createdRequests = append(s.createdRequests, request)
	taskID := fmt.Sprintf("collab-%d", len(s.createdRequests))
	task := provider.ClientCollaborationTask{
		ID:              taskID,
		Status:          firstNonEmptySubtask(strings.TrimSpace(s.createdTaskStatus), "pending"),
		TaskKind:        request.TaskKind,
		DeliveryPolicy:  request.DeliveryPolicy,
		TargetClientID:  request.TargetClientID,
		TargetDeviceID:  request.TargetDeviceID,
		SourceSessionID: request.SourceSessionID,
		SourceRunID:     request.SourceRunID,
		SourceMessageID: request.SourceMessageID,
		ChildSessionID:  request.ChildSessionID,
		ChildRunID:      request.ChildRunID,
		CommandPayload:  request.CommandPayload,
		StatusPayload:   s.createdTaskStatusPayload,
	}
	if s.tasks == nil {
		s.tasks = map[string]provider.ClientCollaborationTask{}
	}
	s.tasks[taskID] = task
	s.currentTask = task
	taskCopy := task
	return &taskCopy, nil
}

func (s *subtaskProviderStub) UpdateClientCollaboration(_ context.Context, taskID string, request provider.ClientCollaborationUpdateRequest) (*provider.ClientCollaborationTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updatedTaskIDs = append(s.updatedTaskIDs, taskID)
	s.updateRequests = append(s.updateRequests, request)
	task := s.lookupTaskLocked(taskID)
	if strings.TrimSpace(request.Status) != "" {
		task.Status = request.Status
	}
	if request.StatusPayload != nil {
		task.StatusPayload = request.StatusPayload
	}
	if request.ResultPayload != nil {
		task.ResultPayload = request.ResultPayload
	}
	if strings.TrimSpace(request.ErrorMessage) != "" {
		task.ErrorMessage = request.ErrorMessage
	}
	if s.tasks == nil {
		s.tasks = map[string]provider.ClientCollaborationTask{}
	}
	s.tasks[taskID] = task
	if s.currentTask.ID == taskID {
		s.currentTask = task
	}
	taskCopy := task
	return &taskCopy, nil
}

func (s *subtaskProviderStub) GetClientCollaboration(_ context.Context, taskID string) (*provider.ClientCollaborationTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getTaskIDs = append(s.getTaskIDs, taskID)
	if task, ok := s.tasks[taskID]; ok {
		taskCopy := task
		return &taskCopy, nil
	}
	if taskID != s.currentTask.ID {
		return &provider.ClientCollaborationTask{ID: taskID, Status: "failed", ErrorMessage: "unexpected task id"}, nil
	}
	taskCopy := s.currentTask
	return &taskCopy, nil
}

func (s *subtaskProviderStub) Stream(_ context.Context, request provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastStream = request
	if s.streamCalls < len(s.streams) {
		stream := s.streams[s.streamCalls]
		var streamSession *provider.StreamSession
		if s.streamCalls < len(s.streamSessions) {
			streamSession = s.streamSessions[s.streamCalls]
		}
		s.streamCalls++
		return stream, streamSession, nil
	}
	s.streamCalls++
	return s.stream, s.streamSession, nil
}

func streamWithEvents(events ...provider.StreamEvent) <-chan provider.StreamEvent {
	stream := make(chan provider.StreamEvent, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return stream
}

func (s *subtaskProviderStub) lookupTaskLocked(taskID string) provider.ClientCollaborationTask {
	if task, ok := s.tasks[taskID]; ok {
		return task
	}
	if s.currentTask.ID == taskID {
		return s.currentTask
	}
	return provider.ClientCollaborationTask{ID: taskID, Status: "failed", ErrorMessage: "unexpected task id"}
}

func TestExecuteClientToolPayloadRunSubtaskCreatesAndExecutesLocalCollaboration(t *testing.T) {
	tempDir := t.TempDir()
	providerStub := &subtaskProviderStub{
		createdSessionID: "child-session-1",
		stream: streamWithEvents(
			provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "子任务完成", "message_id": "child-message-1"}},
			provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}},
		),
		streamSession: &provider.StreamSession{SessionID: "child-session-1"},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)

	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		ClientID:     "cli-123",
		DeviceID:     "device-456",
	}, map[string]any{
		"flow_id": "parent-flow",
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": "parent-session-1", "run_id": "parent-run-1", "message_id": "parent-message-1"},
		"function": map[string]any{
			"name": "run_subtask",
			"arguments": map[string]any{
				"prompt": "请总结当前变更",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected run_subtask to succeed, got %v", err)
	}
	if toolName != "run_subtask" {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if providerStub.createdRequest.SourceSessionID != "parent-session-1" || providerStub.createdRequest.SourceRunID != "parent-run-1" || providerStub.createdRequest.SourceMessageID != "parent-message-1" {
		t.Fatalf("expected parent lineage in create request, got %#v", providerStub.createdRequest)
	}
	if providerStub.createdRequest.TargetClientID != "cli-123" || providerStub.createdRequest.TargetDeviceID != "device-456" {
		t.Fatalf("expected current client identity in create request, got %#v", providerStub.createdRequest)
	}
	if providerStub.lastStream.SessionID != "child-session-1" {
		t.Fatalf("expected child session id in stream request, got %#v", providerStub.lastStream)
	}
	if providerStub.lastStream.RunID != providerStub.createdRequest.ChildRunID {
		t.Fatalf("expected child run id in stream request, got %#v / %#v", providerStub.lastStream, providerStub.createdRequest)
	}
	if len(providerStub.updateRequests) != 2 {
		t.Fatalf("expected running+success updates, got %#v", providerStub.updateRequests)
	}
	if providerStub.updateRequests[0].Status != "running" || providerStub.updateRequests[1].Status != "success" {
		t.Fatalf("unexpected update sequence: %#v", providerStub.updateRequests)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if result["task_id"] != "collab-1" || result["text"] != "子任务完成" {
		t.Fatalf("unexpected subtask result: %#v", result)
	}
	if result["child_message_id"] != "child-message-1" {
		t.Fatalf("expected child_message_id in result, got %#v", result)
	}
}

func TestExecuteClientToolPayloadRunSubtaskRejectedWhenMaxParallelismIsZero(t *testing.T) {
	tempDir := t.TempDir()
	providerStub := &subtaskProviderStub{
		createdSessionID: "child-session-1",
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	// 显式禁用子智能体：即使模型强行发起 run_subtask 调用，也应被兜底拒绝
	runtimeClient.SetMaxParallelismSubtask(0)

	_, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		ClientID:     "cli-123",
		DeviceID:     "device-456",
	}, map[string]any{
		"flow_id": "parent-flow",
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": "parent-session-1", "run_id": "parent-run-1", "message_id": "parent-message-1"},
		"function": map[string]any{
			"name":      "run_subtask",
			"arguments": map[string]any{"prompt": "test"},
		},
	})
	if err != nil {
		t.Fatalf("expected tool-level failure result without transport error, got %v", err)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if result["status"] != "failed" {
		t.Fatalf("expected failed status when max_parallelism_subtask is 0, got %#v", result)
	}
	if !strings.Contains(stringValue(result["error_message"]), "子智能体已禁用") {
		t.Fatalf("expected disabled error message, got %#v", result["error_message"])
	}
	if len(providerStub.createdRequests) != 0 {
		t.Fatalf("expected no collaboration task to be created, got %#v", providerStub.createdRequests)
	}
}

func TestExecuteClientToolPayloadRunSubtaskSelfTargetPrefersDispatchedWorker(t *testing.T) {
	tempDir := t.TempDir()
	providerStub := &subtaskProviderStub{
		createdSessionID:         "child-session-1",
		createdTaskStatus:        "dispatched",
		createdTaskStatusPayload: map[string]any{"delivery_channel": "client_websocket"},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)

	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		ClientID:     "cli-123",
		DeviceID:     "device-456",
	}, map[string]any{
		"flow_id": "parent-flow",
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": "parent-session-1", "run_id": "parent-run-1", "message_id": "parent-message-1"},
		"function": map[string]any{
			"name": "run_subtask",
			"arguments": map[string]any{
				"prompt":              "请总结当前变更",
				"wait_for_completion": false,
			},
		},
	})
	if err != nil {
		t.Fatalf("expected run_subtask to succeed, got %v", err)
	}
	if toolName != "run_subtask" {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if providerStub.streamCalls != 0 {
		t.Fatalf("expected self-target task to be delegated to worker without local stream, got %d", providerStub.streamCalls)
	}
	if len(providerStub.updateRequests) != 0 {
		t.Fatalf("expected delegated task not to emit local status updates, got %#v", providerStub.updateRequests)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if result["status"] != "dispatched" {
		t.Fatalf("expected dispatched status from worker-delegated task, got %#v", result)
	}
	statusPayload, ok := result["status_payload"].(map[string]any)
	if !ok || statusPayload["delivery_channel"] != "client_websocket" {
		t.Fatalf("expected delivery channel to round-trip, got %#v", result)
	}
}

func TestExecuteClientCollaborationTaskReusesExistingChildLineage(t *testing.T) {
	tempDir := t.TempDir()
	providerStub := &subtaskProviderStub{
		stream: streamWithEvents(
			provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "远端 worker 完成", "message_id": "child-message-2"}},
			provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}},
		),
		streamSession: &provider.StreamSession{SessionID: "child-session-existing"},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	result, err := runtimeClient.ExecuteClientCollaborationTask(context.Background(), providerStub, internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		ClientID:     "cli-123",
		DeviceID:     "device-456",
	}, &provider.ClientCollaborationTask{
		ID:              "collab-existing",
		TargetClientID:  "cli-123",
		TargetDeviceID:  "device-456",
		SourceSessionID: "parent-session-2",
		SourceRunID:     "parent-run-2",
		SourceMessageID: "parent-message-2",
		ChildSessionID:  "child-session-existing",
		ChildRunID:      "child-run-existing",
		TaskKind:        "dialogue_subtask",
		DeliveryPolicy:  "local_default",
		CommandPayload: map[string]any{
			"prompt":            "请检查当前变更",
			"flow_id":           "flow-child",
			"request_mode":      "agent",
			"working_directory": tempDir,
		},
	})
	if err != nil {
		t.Fatalf("expected ExecuteClientCollaborationTask to succeed, got %v", err)
	}
	if providerStub.createdRequest.ChildSessionID != "" {
		t.Fatalf("expected existing collaboration execution not to create a new task, got %#v", providerStub.createdRequest)
	}
	if providerStub.lastStream.SessionID != "child-session-existing" {
		t.Fatalf("expected existing child session id in stream request, got %#v", providerStub.lastStream)
	}
	if providerStub.lastStream.RunID != "child-run-existing" {
		t.Fatalf("expected existing child run id in stream request, got %#v", providerStub.lastStream)
	}
	if len(providerStub.updateRequests) != 0 {
		t.Fatalf("expected ExecuteClientCollaborationTask not to write HTTP updates directly, got %#v", providerStub.updateRequests)
	}
	if result["task_id"] != "collab-existing" || result["text"] != "远端 worker 完成" {
		t.Fatalf("unexpected collaboration execution result: %#v", result)
	}
	if result["child_session_id"] != "child-session-existing" || result["child_run_id"] != "child-run-existing" {
		t.Fatalf("expected existing child lineage in result, got %#v", result)
	}
	if result["child_message_id"] != "child-message-2" {
		t.Fatalf("expected child_message_id in result, got %#v", result)
	}
}

func TestExecuteClientToolPayloadRunSubtaskAggregatesParallelResults(t *testing.T) {
	tempDir := t.TempDir()
	providerStub := &subtaskProviderStub{
		streams: []<-chan provider.StreamEvent{
			streamWithEvents(
				provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "子任务一完成", "message_id": "child-message-1"}},
				provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}},
			),
			streamWithEvents(
				provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "子任务二完成", "message_id": "child-message-2"}},
				provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}},
			),
		},
		streamSessions: []*provider.StreamSession{
			{SessionID: "child-session-1"},
			{SessionID: "child-session-2"},
		},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)

	_, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		ClientID:     "cli-123",
		DeviceID:     "device-456",
	}, map[string]any{
		"flow_id": "parent-flow",
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": "parent-session-1", "run_id": "parent-run-1", "message_id": "parent-message-1"},
		"function": map[string]any{
			"name": "run_subtask",
			"arguments": map[string]any{
				"parallel": true,
				"subtasks": []any{
					map[string]any{"prompt": "请总结代码改动一", "working_directory": tempDir},
					map[string]any{"prompt": "请总结代码改动二", "working_directory": tempDir},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected parallel run_subtask to succeed, got %v", err)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if result["status"] != "success" || result["success_count"] != 2 || result["failure_count"] != 0 {
		t.Fatalf("unexpected aggregate status: %#v", result)
	}
	if result["mode"] != "parallel" || result["total"] != 2 {
		t.Fatalf("unexpected aggregate metadata: %#v", result)
	}
	items, ok := result["results"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected two aggregate result items, got %#v", result["results"])
	}
	textSet := map[string]bool{}
	taskIDSet := map[string]bool{}
	for _, item := range items {
		entry, _ := item.(map[string]any)
		textSet[stringValue(entry["text"])] = true
		taskIDSet[stringValue(entry["task_id"])] = true
	}
	if !textSet["子任务一完成"] || !textSet["子任务二完成"] {
		t.Fatalf("unexpected aggregate item payloads: %#v", items)
	}
	if len(taskIDSet) != 2 {
		t.Fatalf("expected distinct task ids in aggregate payload, got %#v", items)
	}
	statusCounts := map[string]int{}
	for _, request := range providerStub.updateRequests {
		statusCounts[request.Status]++
	}
	if statusCounts["running"] != 2 || statusCounts["success"] != 2 {
		t.Fatalf("unexpected update sequence counts: %#v", providerStub.updateRequests)
	}
	if len(providerStub.createdRequests) != 2 {
		t.Fatalf("expected two collaboration create requests, got %#v", providerStub.createdRequests)
	}
}

func TestFilterSubtaskExcludedToolsRemovesRunSubtask(t *testing.T) {
	servers := map[string]any{
		"local": []map[string]any{
			{"name": "shell_exec", "description": "execute shell"},
			{"name": "run_subtask", "description": "create subtask"},
			{"name": "read_file", "description": "read file"},
		},
	}
	filtered := filterSubtaskExcludedTools(servers)
	definitions, ok := filtered["local"].([]map[string]any)
	if !ok {
		t.Fatalf("expected filtered definitions, got %#v", filtered["local"])
	}
	if len(definitions) != 2 {
		t.Fatalf("expected 2 tools after filtering run_subtask, got %d", len(definitions))
	}
	for _, def := range definitions {
		name := stringValue(def["name"])
		if name == "run_subtask" {
			t.Fatalf("run_subtask should have been filtered out, got %#v", definitions)
		}
	}
}

func TestFilterSubtaskExcludedToolsPreservesOtherServers(t *testing.T) {
	servers := map[string]any{
		"local": []map[string]any{
			{"name": "run_subtask", "description": "create subtask"},
			{"name": "shell_exec", "description": "execute shell"},
		},
		"other": []map[string]any{
			{"name": "custom_tool", "description": "custom"},
		},
	}
	filtered := filterSubtaskExcludedTools(servers)
	localDefs, ok := filtered["local"].([]map[string]any)
	if !ok || len(localDefs) != 1 {
		t.Fatalf("expected 1 local tool after filtering, got %#v", filtered["local"])
	}
	if stringValue(localDefs[0]["name"]) != "shell_exec" {
		t.Fatalf("expected shell_exec to remain, got %#v", localDefs)
	}
	otherDefs, ok := filtered["other"].([]map[string]any)
	if !ok || len(otherDefs) != 1 {
		t.Fatalf("expected other server tools to be preserved, got %#v", filtered["other"])
	}
}

func TestFilterSubtaskExcludedToolsHandlesEmptyInput(t *testing.T) {
	if result := filterSubtaskExcludedTools(nil); result != nil {
		t.Fatalf("expected nil for nil input, got %#v", result)
	}
	if result := filterSubtaskExcludedTools(map[string]any{}); len(result) != 0 {
		t.Fatalf("expected empty map for empty input, got %#v", result)
	}
}

func TestFilterSubtaskExcludedToolsHandlesNonDefinitionValues(t *testing.T) {
	servers := map[string]any{
		"local":   []map[string]any{{"name": "run_subtask"}},
		"unknown": "not-a-definition-list",
	}
	filtered := filterSubtaskExcludedTools(servers)
	if filtered["unknown"] != "not-a-definition-list" {
		t.Fatalf("expected non-definition values to pass through, got %#v", filtered["unknown"])
	}
	localDefs, ok := filtered["local"].([]map[string]any)
	if !ok || len(localDefs) != 0 {
		t.Fatalf("expected empty local tools after filtering, got %#v", filtered["local"])
	}
}

func TestExecuteLocalSubtaskStreamRequestExcludesRunSubtask(t *testing.T) {
	tempDir := t.TempDir()
	providerStub := &subtaskProviderStub{
		createdSessionID: "child-session-1",
		stream: streamWithEvents(
			provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "done", "message_id": "msg-1"}},
			provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}},
		),
		streamSession: &provider.StreamSession{SessionID: "child-session-1"},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)

	_, _, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		ClientID:     "cli-123",
		DeviceID:     "device-456",
	}, map[string]any{
		"flow_id": "parent-flow",
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": "parent-session-1", "run_id": "parent-run-1", "message_id": "parent-message-1"},
		"function": map[string]any{
			"name":      "run_subtask",
			"arguments": map[string]any{"prompt": "test"},
		},
	})
	if err != nil {
		t.Fatalf("expected run_subtask to succeed, got %v", err)
	}

	// 验证传给子任务的 ClientMCPServers 中不包含 run_subtask
	mcpServers := providerStub.lastStream.ClientMCPServers
	localDefs, ok := mcpServers["local"].([]map[string]any)
	if !ok {
		t.Fatalf("expected local tool definitions, got %#v", mcpServers["local"])
	}
	for _, def := range localDefs {
		if name := stringValue(def["name"]); name == "run_subtask" {
			t.Fatalf("subtask stream request should not include run_subtask tool, but found it in %#v", localDefs)
		}
	}
}

func TestExecuteClientToolPayloadRunSubtaskParallelAggregatesFailuresWithoutError(t *testing.T) {
	tempDir := t.TempDir()
	providerStub := &subtaskProviderStub{
		streams: []<-chan provider.StreamEvent{
			streamWithEvents(
				provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{"chunk": "子任务一完成", "message_id": "child-message-1"}},
				provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{}},
			),
			streamWithEvents(
				provider.StreamEvent{Type: provider.EventError, Data: map[string]any{"message": "子任务二失败"}},
			),
		},
		streamSessions: []*provider.StreamSession{
			{SessionID: "child-session-1"},
			{SessionID: "child-session-2"},
		},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)

	_, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		ClientID:     "cli-123",
		DeviceID:     "device-456",
	}, map[string]any{
		"flow_id": "parent-flow",
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": "parent-session-1", "run_id": "parent-run-1", "message_id": "parent-message-1"},
		"function": map[string]any{
			"name": "run_subtask",
			"arguments": map[string]any{
				"parallel": true,
				"subtasks": []any{
					map[string]any{"prompt": "请总结代码改动一", "working_directory": tempDir},
					map[string]any{"prompt": "请总结代码改动二", "working_directory": tempDir},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected partial failures to be aggregated without direct tool error, got %v", err)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if result["status"] != "partial_failed" || result["success_count"] != 1 || result["failure_count"] != 1 {
		t.Fatalf("unexpected aggregate partial failure status: %#v", result)
	}
	if !strings.Contains(stringValue(result["error_message"]), "子任务二失败") {
		t.Fatalf("expected aggregate error summary, got %#v", result)
	}
	items, ok := result["results"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected two aggregate result items, got %#v", result["results"])
	}
	statusSet := map[string]bool{}
	for _, item := range items {
		entry, _ := item.(map[string]any)
		statusSet[stringValue(entry["status"])] = true
	}
	if !statusSet["success"] || !statusSet["failed"] {
		t.Fatalf("expected one success and one failed aggregate item, got %#v", items)
	}
}

func TestRunSubtaskLocalExecutionFailureReportsToServer(t *testing.T) {
	tempDir := t.TempDir()
	providerStub := &subtaskProviderStub{
		createdSessionID: "child-session-1",
		stream: streamWithEvents(
			provider.StreamEvent{Type: provider.EventError, Data: map[string]any{"message": "子任务执行出错"}},
		),
		streamSession: &provider.StreamSession{SessionID: "child-session-1"},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)

	_, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:            tempDir,
		ApprovalMode:       "auto",
		ClientID:           "cli-123",
		DeviceID:           "device-456",
		HTTPTimeoutSeconds: 30,
	}, map[string]any{
		"flow_id": "parent-flow",
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": "parent-session-1", "run_id": "parent-run-1", "message_id": "parent-message-1"},
		"function": map[string]any{
			"name": "run_subtask",
			"arguments": map[string]any{
				"prompt": "请总结当前变更",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected run_subtask to not return tool error, got %v", err)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if result["status"] != "failed" {
		t.Fatalf("expected failed status, got %#v", result["status"])
	}
	// 验证服务端收到了 failed 状态更新
	failedUpdates := 0
	for _, req := range providerStub.updateRequests {
		if req.Status == "failed" {
			failedUpdates++
		}
	}
	if failedUpdates == 0 {
		t.Fatalf("expected at least one failed status update to server, got %#v", providerStub.updateRequests)
	}
}

func TestExecuteClientCollaborationTaskFailureReportsToServer(t *testing.T) {
	tempDir := t.TempDir()
	providerStub := &subtaskProviderStub{
		createdSessionID: "child-session-existing",
		stream: streamWithEvents(
			provider.StreamEvent{Type: provider.EventError, Data: map[string]any{"message": "子任务执行出错"}},
		),
		streamSession: &provider.StreamSession{SessionID: "child-session-existing"},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)

	result, err := runtimeClient.ExecuteClientCollaborationTask(context.Background(), providerStub, internalconfig.Config{
		Workdir:            tempDir,
		ApprovalMode:       "auto",
		ClientID:           "cli-123",
		DeviceID:           "device-456",
		HTTPTimeoutSeconds: 30,
	}, &provider.ClientCollaborationTask{
		ID:              "collab-fail-test",
		TargetClientID:  "cli-123",
		TargetDeviceID:  "device-456",
		SourceSessionID: "parent-session-2",
		SourceRunID:     "parent-run-2",
		SourceMessageID: "parent-message-2",
		ChildSessionID:  "child-session-existing",
		ChildRunID:      "child-run-existing",
		TaskKind:        "dialogue_subtask",
		DeliveryPolicy:  "local_default",
		CommandPayload: map[string]any{
			"prompt":            "请检查当前变更",
			"flow_id":           "flow-child",
			"request_mode":      "agent",
			"working_directory": tempDir,
		},
	})
	if err != nil {
		t.Fatalf("expected ExecuteClientCollaborationTask to not return error, got %v", err)
	}
	if result["status"] != "failed" {
		t.Fatalf("expected failed status, got %#v", result["status"])
	}
	// 验证服务端收到了 failed 状态更新
	failedUpdates := 0
	for _, req := range providerStub.updateRequests {
		if req.Status == "failed" {
			failedUpdates++
		}
	}
	if failedUpdates == 0 {
		t.Fatalf("expected at least one failed status update to server, got %#v", providerStub.updateRequests)
	}
}

// TestParseSubtaskExecutionRequestInheritsCurrentRequestMode 验证子任务请求模式解析优先级：
// 显式参数 > 继承主智能体 currentRequestMode > 默认 agent。
func TestParseSubtaskExecutionRequestInheritsCurrentRequestMode(t *testing.T) {
	tempDir := t.TempDir()
	baseConfig := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		ClientID:     "cli-123",
		DeviceID:     "device-456",
	}
	basePayload := map[string]any{
		"flow_id": "parent-flow",
		"context": map[string]any{"session_id": "parent-session-1", "run_id": "parent-run-1", "message_id": "parent-message-1"},
	}

	tests := []struct {
		name            string
		currentMode     string
		arguments       map[string]any
		wantRequestMode string
	}{
		{
			name:            "未指定模式且主智能体为 plan 时继承 plan",
			currentMode:     provider.RequestTypePlan,
			arguments:       map[string]any{"prompt": "test"},
			wantRequestMode: provider.RequestTypePlan,
		},
		{
			name:            "未指定模式且主智能体为 ask 时继承 ask",
			currentMode:     provider.RequestTypeAsk,
			arguments:       map[string]any{"prompt": "test"},
			wantRequestMode: provider.RequestTypeAsk,
		},
		{
			name:            "显式指定 agent 时优先于继承的 plan",
			currentMode:     provider.RequestTypePlan,
			arguments:       map[string]any{"prompt": "test", "request_mode": "agent"},
			wantRequestMode: provider.RequestTypeAgent,
		},
		{
			name:            "未设置主智能体模式时默认 agent",
			currentMode:     "",
			arguments:       map[string]any{"prompt": "test"},
			wantRequestMode: provider.RequestTypeAgent,
		},
		{
			name:            "显式非法模式值经 NormalizeRequestType 归一为 ask",
			currentMode:     provider.RequestTypeAgent,
			arguments:       map[string]any{"prompt": "test", "request_mode": "bogus"},
			wantRequestMode: provider.RequestTypeAsk,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providerStub := &subtaskProviderStub{createdSessionID: "child-session-mode"}
			runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
			if tt.currentMode != "" {
				runtimeClient.SetCurrentRequestMode(tt.currentMode)
			}

			request, err := runtimeClient.parseSubtaskExecutionRequest(
				context.Background(), providerStub, baseConfig, basePayload, tt.arguments,
			)
			if err != nil {
				t.Fatalf("expected parse to succeed, got %v", err)
			}
			if request.RequestMode != tt.wantRequestMode {
				t.Fatalf("expected request mode %q, got %q", tt.wantRequestMode, request.RequestMode)
			}
		})
	}
}

func TestSplitThinkingContent(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantReason string
		wantText   string
	}{
		{
			name:       "no thinking tags",
			input:      "hello world",
			wantReason: "",
			wantText:   "hello world",
		},
		{
			name:       "single thinking block",
			input:      "<think>let me think</think>answer is 42",
			wantReason: "let me think",
			wantText:   "answer is 42",
		},
		{
			name:       "multiple thinking blocks",
			input:      "<think>first thought</think>middle text<think>second thought</think>final answer",
			wantReason: "first thought\n\nsecond thought",
			wantText:   "middle text\n\nfinal answer",
		},
		{
			name:       "thinking at start and end",
			input:      "<think>opening thought</think>body text<think>closing thought</think>",
			wantReason: "opening thought\n\nclosing thought",
			wantText:   "body text",
		},
		{
			name:       "unclosed thinking tag",
			input:      "<think>unfinished thought",
			wantReason: "unfinished thought",
			wantText:   "",
		},
		{
			name:       "empty thinking block",
			input:      "<think></think>just text",
			wantReason: "",
			wantText:   "just text",
		},
		{
			name:       "only thinking blocks, no text",
			input:      "<think>thought one</think><think>thought two</think>",
			wantReason: "thought one\n\nthought two",
			wantText:   "",
		},
		{
			name:       "text before first thinking",
			input:      "prefix text<think>thought</think>suffix text",
			wantReason: "thought",
			wantText:   "prefix text\n\nsuffix text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReason, gotText := splitThinkingContent(tt.input)
			if gotReason != tt.wantReason {
				t.Errorf("reason = %q, want %q", gotReason, tt.wantReason)
			}
			if gotText != tt.wantText {
				t.Errorf("text = %q, want %q", gotText, tt.wantText)
			}
		})
	}
}
