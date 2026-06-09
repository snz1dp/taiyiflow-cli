package runtime

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const (
	defaultSubtaskTimeoutSeconds = 300
	defaultSubtaskPollInterval   = 2 * time.Second
)

type subtaskExecutionRequest struct {
	Prompt            string
	FlowID            string
	RequestMode       string
	WorkingDirectory  string
	TargetClientID    string
	TargetDeviceID    string
	DeliveryPolicy    string
	WaitForCompletion bool
	TimeoutSeconds    int
	ParentSessionID   string
	ParentRunID       string
	ParentMessageID   string
	ChildSessionID    string
	ChildRunID        string
}

type subtaskExecutionResult struct {
	Text           string
	Reason         string
	ChildSessionID string
	ChildRunID     string
	ChildMessageID string
}

type subtaskBatchItemResult struct {
	Index   int
	Request subtaskExecutionRequest
	Result  map[string]any
	Err     error
}

func (r *ClientRuntime) ExecuteClientCollaborationTask(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	task *provider.ClientCollaborationTask,
) (map[string]any, error) {
	if providerClient == nil {
		return nil, newToolError("EXECUTION_FAILED", "客户端协同任务执行需要可用的 provider")
	}
	if task == nil {
		return nil, newToolError("PARAM_ERROR", "客户端协同任务不能为空")
	}
	request, err := r.parseSubtaskExecutionRequestFromTask(ctx, providerClient, config, task)
	if err != nil {
		return nil, err
	}
	executionResult, execErr := r.executeLocalSubtask(ctx, providerClient, config, request)
	if execErr != nil {
		result := buildSubtaskResult(task, nil)
		result["status"] = "failed"
		result["error_message"] = execErr.Error()
		return result, nil
	}
	return buildSubtaskResult(task, &executionResult), nil
}

func (r *ClientRuntime) runSubtask(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
	arguments map[string]any,
) (map[string]any, error) {
	if providerClient == nil {
		return nil, newToolError("EXECUTION_FAILED", "run_subtask 需要可用的 provider")
	}

	requests, maxParallelism, err := r.parseSubtaskExecutionRequests(ctx, providerClient, config, payload, arguments)
	if err != nil {
		return nil, err
	}
	if len(requests) == 1 {
		return r.executeManagedSubtaskRequest(ctx, providerClient, config, requests[0])
	}
	return r.executeManagedSubtaskRequests(ctx, providerClient, config, requests, maxParallelism), nil
}


func (r *ClientRuntime) executeManagedSubtaskRequest(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	request subtaskExecutionRequest,
) (map[string]any, error) {
	createdTask, err := providerClient.CreateClientCollaboration(ctx, provider.ClientCollaborationCreateRequest{
		TaskKind:        "dialogue_subtask",
		TargetClientID:  request.TargetClientID,
		TargetDeviceID:  request.TargetDeviceID,
		DeliveryPolicy:  request.DeliveryPolicy,
		CommandPayload:  buildSubtaskCommandPayload(request),
		SourceSessionID: request.ParentSessionID,
		SourceRunID:     request.ParentRunID,
		SourceMessageID: request.ParentMessageID,
		ChildSessionID:  request.ChildSessionID,
		ChildRunID:      request.ChildRunID,
	})
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "创建子任务失败: %v", err)
	}

	if shouldRunSubtaskRemotely(createdTask, config, request) {
		if !request.WaitForCompletion {
			return buildSubtaskResult(createdTask, nil), nil
		}
		finalTask, waitErr := waitForClientCollaboration(ctx, providerClient, createdTask.ID, request.TimeoutSeconds)
		if waitErr != nil {
			result := buildSubtaskResult(createdTask, nil)
			result["status"] = "failed"
			result["error_message"] = waitErr.Error()
			return result, nil
		}
		result := buildSubtaskResult(finalTask, nil)
		if strings.TrimSpace(finalTask.Status) != "success" {
			result["error_message"] = firstNonEmptySubtask(strings.TrimSpace(finalTask.ErrorMessage), strings.TrimSpace(finalTask.Status))
			return result, nil
		}
		return result, nil
	}

	if _, err := providerClient.UpdateClientCollaboration(ctx, createdTask.ID, provider.ClientCollaborationUpdateRequest{
		Status: "running",
		StatusPayload: map[string]any{
			"executor": "local_serial",
			"target":   "self",
		},
	}); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "更新子任务运行状态失败: %v", err)
	}

	executionCtx, cancel := context.WithTimeout(ctx, time.Duration(request.TimeoutSeconds)*time.Second)
	defer cancel()
	executionResult, execErr := r.executeLocalSubtask(executionCtx, providerClient, config, request)
	if execErr != nil {
		updatedTask, updateErr := providerClient.UpdateClientCollaboration(ctx, createdTask.ID, provider.ClientCollaborationUpdateRequest{
			Status:       "failed",
			ErrorMessage: execErr.Error(),
			StatusPayload: map[string]any{
				"executor": "local_serial",
				"target":   "self",
			},
		})
		if updateErr != nil {
			result := buildSubtaskResult(createdTask, nil)
			result["status"] = "failed"
			result["error_message"] = fmt.Sprintf("子任务执行失败: %v；写回失败状态也失败: %v", execErr, updateErr)
			return result, nil
		}
		result := buildSubtaskResult(updatedTask, nil)
		result["error_message"] = execErr.Error()
		return result, nil
	}

	updatedTask, err := providerClient.UpdateClientCollaboration(ctx, createdTask.ID, provider.ClientCollaborationUpdateRequest{
		Status: "success",
		StatusPayload: map[string]any{
			"executor": "local_serial",
			"target":   "self",
		},
		ResultPayload: buildSubtaskResultPayload(request, executionResult),
	})
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "更新子任务成功状态失败: %v", err)
	}
	return buildSubtaskResult(updatedTask, &executionResult), nil
}

func shouldRunSubtaskRemotely(task *provider.ClientCollaborationTask, config internalconfig.Config, request subtaskExecutionRequest) bool {
	if !subtaskTargetsCurrentClient(config, request) {
		return true
	}
	if task == nil {
		return false
	}
	switch strings.TrimSpace(task.Status) {
	case "dispatched", "acknowledged", "running", "success", "failed":
		return true
	default:
		return false
	}
}

func (r *ClientRuntime) parseSubtaskExecutionRequest(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
	arguments map[string]any,
) (subtaskExecutionRequest, error) {
	prompt := strings.TrimSpace(argumentString(arguments, "prompt", "input"))
	if prompt == "" {
		return subtaskExecutionRequest{}, newToolError("PARAM_ERROR", "run_subtask 缺少 prompt")
	}
	if rawSerial, ok := firstArgument(arguments, "serial"); ok && !boolValue(rawSerial) {
		return subtaskExecutionRequest{}, newToolError("PARAM_ERROR", "run_subtask 当前仅支持串行执行")
	}
	flowID := strings.TrimSpace(argumentString(arguments, "flow_id"))
	if flowID == "" {
		flowID = strings.TrimSpace(stringValue(payload["flow_id"]))
	}
	if flowID == "" {
		return subtaskExecutionRequest{}, newToolError("PARAM_ERROR", "run_subtask 缺少 flow_id")
	}
	requestMode := provider.RequestTypeAgent
	if rawMode, ok := firstArgument(arguments, "request_mode", "requestMode"); ok {
		requestMode = provider.NormalizeRequestType(stringValue(rawMode))
	}
	workingDirectory, err := r.resolveAllowedDirectoryWithPolicy(config, argumentString(arguments, "workingDirectory", "working_directory"))
	if err != nil {
		return subtaskExecutionRequest{}, err
	}
	waitForCompletion := true
	if rawWait, ok := firstArgument(arguments, "wait_for_completion", "waitForCompletion"); ok {
		waitForCompletion = boolValue(rawWait)
	}
	request := subtaskExecutionRequest{
		Prompt:            prompt,
		FlowID:            flowID,
		RequestMode:       requestMode,
		WorkingDirectory:  workingDirectory,
		TargetClientID:    firstNonEmptySubtask(strings.TrimSpace(argumentString(arguments, "target_client_id", "targetClientID")), strings.TrimSpace(config.ClientID)),
		TargetDeviceID:    firstNonEmptySubtask(strings.TrimSpace(argumentString(arguments, "target_device_id", "targetDeviceID")), strings.TrimSpace(config.DeviceID)),
		DeliveryPolicy:    firstNonEmptySubtask(strings.TrimSpace(argumentString(arguments, "delivery_policy", "deliveryPolicy")), "fail_if_offline"),
		WaitForCompletion: waitForCompletion,
		TimeoutSeconds:    argumentInt(arguments, defaultSubtaskTimeoutSeconds, "timeout_seconds", "timeoutSeconds"),
		ParentSessionID:   firstNonEmptySubtask(strings.TrimSpace(argumentString(arguments, "session_id", "sessionId")), strings.TrimSpace(nestedString(payload, "context", "session_id"))),
		ParentRunID:       firstNonEmptySubtask(strings.TrimSpace(argumentString(arguments, "run_id", "runId")), strings.TrimSpace(nestedString(payload, "context", "run_id"))),
		ParentMessageID:   firstNonEmptySubtask(strings.TrimSpace(argumentString(arguments, "message_id", "messageId")), strings.TrimSpace(nestedString(payload, "context", "message_id"))),
	}
	if request.TimeoutSeconds < 1 {
		request.TimeoutSeconds = defaultSubtaskTimeoutSeconds
	}
	if request.TargetClientID == "" && request.TargetDeviceID == "" {
		return subtaskExecutionRequest{}, newToolError("PARAM_ERROR", "run_subtask 缺少 target_client_id 或 target_device_id，且当前配置也未提供 client_id/device_id")
	}
	if request.ParentSessionID == "" || request.ParentRunID == "" || request.ParentMessageID == "" {
		if state, err := loadSessionStateForSubtask(payload, config); err == nil {
			request.ParentSessionID = firstNonEmptySubtask(request.ParentSessionID, strings.TrimSpace(state.SessionID))
			request.ParentRunID = firstNonEmptySubtask(request.ParentRunID, strings.TrimSpace(state.LastRunID))
			request.ParentMessageID = firstNonEmptySubtask(request.ParentMessageID, strings.TrimSpace(state.LastMessageID))
		}
	}
	childSessionID, err := providerClient.CreateSessionID(ctx)
	if err != nil {
		return subtaskExecutionRequest{}, newToolError("EXECUTION_FAILED", "创建子会话失败: %v", err)
	}
	request.ChildSessionID = strings.TrimSpace(childSessionID)
	if request.ChildSessionID == "" {
		return subtaskExecutionRequest{}, newToolError("EXECUTION_FAILED", "provider 返回空的子会话 ID")
	}
	request.ChildRunID = newSubtaskRunID()
	return request, nil
}

func (r *ClientRuntime) parseSubtaskExecutionRequests(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
	arguments map[string]any,
) ([]subtaskExecutionRequest, int, error) {
	rawSubtasks, hasSubtasks := arguments["subtasks"]
	if !hasSubtasks {
		if rawParallel, ok := firstArgument(arguments, "parallel"); ok && boolValue(rawParallel) {
			return nil, 0, newToolError("PARAM_ERROR", "run_subtask 并行模式至少需要提供 subtasks")
		}
		if rawSerial, ok := firstArgument(arguments, "serial"); ok && !boolValue(rawSerial) {
			return nil, 0, newToolError("PARAM_ERROR", "run_subtask 并行模式至少需要提供 subtasks")
		}
		request, err := r.parseSubtaskExecutionRequest(ctx, providerClient, config, payload, arguments)
		if err != nil {
			return nil, 0, err
		}
		return []subtaskExecutionRequest{request}, 1, nil
	}
	items, ok := rawSubtasks.([]any)
	if !ok || len(items) == 0 {
		return nil, 0, newToolError("PARAM_ERROR", "run_subtask 的 subtasks 至少需要一项")
	}
	baseArguments := cloneSubtaskArguments(arguments)
	delete(baseArguments, "subtasks")
	delete(baseArguments, "parallel")
	delete(baseArguments, "max_parallelism")
	delete(baseArguments, "maxParallelism")
	requests := make([]subtaskExecutionRequest, 0, len(items))
	for index, item := range items {
		mergedArguments := cloneSubtaskArguments(baseArguments)
		switch typed := item.(type) {
		case string:
			mergedArguments["prompt"] = strings.TrimSpace(typed)
		case map[string]any:
			for key, value := range typed {
				mergedArguments[key] = value
			}
		default:
			return nil, 0, newToolError("PARAM_ERROR", "run_subtask 的 subtasks[%d] 必须是字符串或对象", index)
		}
		request, err := r.parseSubtaskExecutionRequest(ctx, providerClient, config, payload, mergedArguments)
		if err != nil {
			return nil, 0, newToolError("PARAM_ERROR", "run_subtask 的 subtasks[%d] 无效: %v", index, err)
		}
		requests = append(requests, request)
	}
	parallelEnabled := true
	if rawParallel, ok := firstArgument(arguments, "parallel"); ok {
		parallelEnabled = boolValue(rawParallel)
	}
	if rawSerial, ok := firstArgument(arguments, "serial"); ok {
		parallelEnabled = !boolValue(rawSerial)
	}
	maxParallelism := argumentInt(arguments, len(requests), "max_parallelism", "maxParallelism")
	if maxParallelism < 1 {
		maxParallelism = 1
	}
	if !parallelEnabled {
		maxParallelism = 1
	}
	if maxParallelism > len(requests) {
		maxParallelism = len(requests)
	}
	return requests, maxParallelism, nil
}

func (r *ClientRuntime) parseSubtaskExecutionRequestFromTask(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	task *provider.ClientCollaborationTask,
) (subtaskExecutionRequest, error) {
	if task == nil {
		return subtaskExecutionRequest{}, newToolError("PARAM_ERROR", "客户端协同任务不能为空")
	}
	arguments := map[string]any{}
	for key, value := range task.CommandPayload {
		arguments[key] = value
	}
	payload := map[string]any{
		"flow_id": stringValue(arguments["flow_id"]),
		"context": map[string]any{
			"session_id": task.SourceSessionID,
			"run_id":     task.SourceRunID,
			"message_id": task.SourceMessageID,
		},
	}
	request, err := r.parseSubtaskExecutionRequest(ctx, providerClient, config, payload, arguments)
	if err != nil {
		return subtaskExecutionRequest{}, err
	}
	request.TargetClientID = firstNonEmptySubtask(strings.TrimSpace(task.TargetClientID), request.TargetClientID)
	request.TargetDeviceID = firstNonEmptySubtask(strings.TrimSpace(task.TargetDeviceID), request.TargetDeviceID)
	request.DeliveryPolicy = firstNonEmptySubtask(strings.TrimSpace(task.DeliveryPolicy), request.DeliveryPolicy)
	request.ParentSessionID = firstNonEmptySubtask(strings.TrimSpace(task.SourceSessionID), request.ParentSessionID)
	request.ParentRunID = firstNonEmptySubtask(strings.TrimSpace(task.SourceRunID), request.ParentRunID)
	request.ParentMessageID = firstNonEmptySubtask(strings.TrimSpace(task.SourceMessageID), request.ParentMessageID)
	request.ChildSessionID = firstNonEmptySubtask(strings.TrimSpace(task.ChildSessionID), request.ChildSessionID)
	request.ChildRunID = firstNonEmptySubtask(strings.TrimSpace(task.ChildRunID), request.ChildRunID)
	if strings.TrimSpace(request.ChildSessionID) == "" {
		childSessionID, createErr := providerClient.CreateSessionID(ctx)
		if createErr != nil {
			return subtaskExecutionRequest{}, newToolError("EXECUTION_FAILED", "创建子会话失败: %v", createErr)
		}
		request.ChildSessionID = strings.TrimSpace(childSessionID)
	}
	if strings.TrimSpace(request.ChildRunID) == "" {
		request.ChildRunID = newSubtaskRunID()
	}
	return request, nil
}

func (r *ClientRuntime) executeLocalSubtask(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	request subtaskExecutionRequest,
) (subtaskExecutionResult, error) {
	stream, streamSession, err := providerClient.Stream(ctx, provider.StreamRequest{
		FlowID:                    request.FlowID,
		Input:                     request.Prompt,
		InputType:                 "any",
		Tweaks:                    buildSubtaskTweaks(request.WorkingDirectory, "client"),
		InternetSearch:            r.subtaskInternetSearch,
		OutputType:                "any",
		SessionID:                 request.ChildSessionID,
		RunID:                     request.ChildRunID,
		RequestType:               request.RequestMode,
		ReasoningEnabled:          r.subtaskReasoningEnabled,
		CrossSessionMemoryEnabled: r.subtaskCrossSessionMemory,
		ClientType:                provider.ClientTypeCLI,
		ClientID:                  config.ClientID,
		DeviceID:                  config.DeviceID,
		ClientContextDir:          request.WorkingDirectory,
		CommandExecutionTarget:    "client",
		RequestedSkillNames:       r.subtaskRequestedSkillNames,
		ClientMCPServers:          r.ClientMCPServersForMode(request.RequestMode, config),
		ClientSkillPackages:       r.subtaskSkillPackages,
		ParentSessionID:           request.ParentSessionID,
	})
	if err != nil {
		return subtaskExecutionResult{}, newToolError("EXECUTION_FAILED", "执行子任务流失败: %v", err)
	}

	childSessionID := request.ChildSessionID
	if streamSession != nil && strings.TrimSpace(streamSession.SessionID) != "" {
		childSessionID = strings.TrimSpace(streamSession.SessionID)
	}
	childConfig := config
	childConfig.Workdir = request.WorkingDirectory

	// 为子任务创建静默运行时：禁用流事件日志，避免子任务的工具调用状态信息
	// （如 [本地工具] read_file ...、[本地命令] ...）输出到父任务的消息区干扰用户。
	quietRuntime := r.cloneRuntime()
	quietRuntime.streamEventLogsEnabled = false

	var outputBuilder strings.Builder
	childMessageID := ""
	for event := range stream {
		if messageID := resolveSubtaskEventMessageID(event.Data); messageID != "" {
			childMessageID = messageID
		}
		switch event.Type {
		case provider.EventMessage:
			if text := extractSubtaskEventText(event.Data); text != "" {
				outputBuilder.WriteString(text)
			}
		case provider.EventBuiltIn:
			if err := quietRuntime.HandleBuiltInEvent(ctx, providerClient, childConfig, event.Data); err != nil {
				return subtaskExecutionResult{}, err
			}
		case provider.EventClientTool:
			if err := quietRuntime.HandleClientToolEvent(ctx, providerClient, childConfig, event.Data); err != nil {
				return subtaskExecutionResult{}, err
			}
		case provider.EventError:
			return subtaskExecutionResult{}, newToolError("EXECUTION_FAILED", "子任务执行失败: %s", resolveSubtaskErrorMessage(event.Data))
		}
	}

	// 解析 <think> 标签，分离 reason 和 text
	rawOutput := strings.TrimSpace(outputBuilder.String())
	reason, text := splitThinkingContent(rawOutput)

	return subtaskExecutionResult{
		Text:           text,
		Reason:         reason,
		ChildSessionID: childSessionID,
		ChildRunID:     request.ChildRunID,
		ChildMessageID: childMessageID,
	}, nil
}

func buildSubtaskCommandPayload(request subtaskExecutionRequest) map[string]any {
	return map[string]any{
		"prompt":              request.Prompt,
		"flow_id":             request.FlowID,
		"request_mode":        request.RequestMode,
		"working_directory":   request.WorkingDirectory,
		"target_client_id":    request.TargetClientID,
		"target_device_id":    request.TargetDeviceID,
		"delivery_policy":     request.DeliveryPolicy,
		"wait_for_completion": request.WaitForCompletion,
		"timeout_seconds":     request.TimeoutSeconds,
	}
}

func buildSubtaskResult(task *provider.ClientCollaborationTask, execution *subtaskExecutionResult) map[string]any {
	result := map[string]any{}
	if task != nil {
		result["task_id"] = task.ID
		result["status"] = task.Status
		result["task_kind"] = task.TaskKind
		result["delivery_policy"] = task.DeliveryPolicy
		result["target_client_id"] = task.TargetClientID
		result["target_device_id"] = task.TargetDeviceID
		result["source_session_id"] = task.SourceSessionID
		result["source_run_id"] = task.SourceRunID
		result["source_message_id"] = task.SourceMessageID
		result["child_session_id"] = task.ChildSessionID
		result["child_run_id"] = task.ChildRunID
		if len(task.StatusPayload) > 0 {
			result["status_payload"] = task.StatusPayload
		}
		if len(task.ResultPayload) > 0 {
			result["result"] = task.ResultPayload
			if text := strings.TrimSpace(stringValue(task.ResultPayload["text"])); text != "" {
				result["text"] = text
			}
			if reason := strings.TrimSpace(stringValue(task.ResultPayload["reason"])); reason != "" {
				result["reason"] = reason
			}
		}
		if strings.TrimSpace(task.ErrorMessage) != "" {
			result["error_message"] = task.ErrorMessage
		}
	}
	if execution != nil {
		if strings.TrimSpace(execution.Text) != "" {
			result["text"] = execution.Text
		}
		if strings.TrimSpace(execution.Reason) != "" {
			result["reason"] = execution.Reason
		}
		if strings.TrimSpace(execution.ChildSessionID) != "" {
			result["child_session_id"] = execution.ChildSessionID
		}
		if strings.TrimSpace(execution.ChildRunID) != "" {
			result["child_run_id"] = execution.ChildRunID
		}
		if strings.TrimSpace(execution.ChildMessageID) != "" {
			result["child_message_id"] = execution.ChildMessageID
		}
	}
	return result
}

func buildSubtaskResultPayload(request subtaskExecutionRequest, execution subtaskExecutionResult) map[string]any {
	payload := map[string]any{
		"text":              execution.Text,
		"flow_id":           request.FlowID,
		"session_id":        execution.ChildSessionID,
		"run_id":            execution.ChildRunID,
		"message_id":        execution.ChildMessageID,
		"request_mode":      request.RequestMode,
		"working_directory": request.WorkingDirectory,
	}
	if strings.TrimSpace(execution.Reason) != "" {
		payload["reason"] = execution.Reason
	}
	return payload
}

func (r *ClientRuntime) executeManagedSubtaskRequests(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	requests []subtaskExecutionRequest,
	maxParallelism int,
) map[string]any {
	if len(requests) == 0 {
		return map[string]any{
			"status":        "failed",
			"error_message": "run_subtask 未提供可执行的子任务",
		}
	}
	if maxParallelism < 1 {
		maxParallelism = 1
	}
	if maxParallelism > len(requests) {
		maxParallelism = len(requests)
	}
	results := make([]subtaskBatchItemResult, len(requests))
	var waitGroup sync.WaitGroup
	semaphore := make(chan struct{}, maxParallelism)
	for index, request := range requests {
		waitGroup.Add(1)
		go func(itemIndex int, itemRequest subtaskExecutionRequest) {
			defer waitGroup.Done()
			select {
			case <-ctx.Done():
				results[itemIndex] = subtaskBatchItemResult{Index: itemIndex, Request: itemRequest, Err: ctx.Err()}
				return
			case semaphore <- struct{}{}:
			}
			defer func() {
				<-semaphore
			}()
			runtimeClient := r.cloneRuntime()
			result, err := runtimeClient.executeManagedSubtaskRequest(ctx, providerClient, config, itemRequest)
			results[itemIndex] = subtaskBatchItemResult{Index: itemIndex, Request: itemRequest, Result: result, Err: err}
		}(index, request)
	}
	waitGroup.Wait()
	return buildSubtaskBatchResult(results, maxParallelism)
}

func buildSubtaskBatchResult(results []subtaskBatchItemResult, maxParallelism int) map[string]any {
	aggregated := map[string]any{
		"mode":            "parallel",
		"max_parallelism": maxParallelism,
		"total":           len(results),
	}
	items := make([]any, 0, len(results))
	taskIDs := make([]string, 0, len(results))
	successCount := 0
	failureCount := 0
	pendingCount := 0
	failureMessages := make([]string, 0, len(results))
	for _, item := range results {
		entry := cloneSubtaskArguments(item.Result)
		entry["index"] = item.Index
		entry["prompt"] = item.Request.Prompt
		entry["flow_id"] = firstNonEmptySubtask(strings.TrimSpace(stringValue(entry["flow_id"])), item.Request.FlowID)
		if _, exists := entry["request_mode"]; !exists {
			entry["request_mode"] = item.Request.RequestMode
		}
		if _, exists := entry["working_directory"]; !exists {
			entry["working_directory"] = item.Request.WorkingDirectory
		}
		if item.Err != nil {
			entry["error_message"] = firstNonEmptySubtask(strings.TrimSpace(stringValue(entry["error_message"])), item.Err.Error())
		}
		status := strings.TrimSpace(stringValue(entry["status"]))
		if status == "" {
			if item.Err != nil {
				status = "failed"
			} else {
				status = "success"
			}
			entry["status"] = status
		}
		if taskID := strings.TrimSpace(stringValue(entry["task_id"])); taskID != "" {
			taskIDs = append(taskIDs, taskID)
		}
		switch status {
		case "success":
			successCount++
		case "pending", "dispatched", "acknowledged", "running":
			pendingCount++
		default:
			failureCount++
			if message := strings.TrimSpace(stringValue(entry["error_message"])); message != "" {
				failureMessages = append(failureMessages, fmt.Sprintf("#%d %s", item.Index, message))
			}
		}
		items = append(items, entry)
	}
	aggregated["results"] = items
	aggregated["task_ids"] = taskIDs
	aggregated["success_count"] = successCount
	aggregated["failure_count"] = failureCount
	aggregated["pending_count"] = pendingCount
	switch {
	case failureCount == 0 && pendingCount == 0:
		aggregated["status"] = "success"
	case failureCount == 0:
		aggregated["status"] = "pending"
	case successCount > 0 || pendingCount > 0:
		aggregated["status"] = "partial_failed"
	default:
		aggregated["status"] = "failed"
	}
	if len(failureMessages) > 0 {
		aggregated["error_message"] = strings.Join(failureMessages, "; ")
	}
	return aggregated
}

func cloneSubtaskArguments(arguments map[string]any) map[string]any {
	if len(arguments) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		cloned[key] = value
	}
	return cloned
}

func subtaskTargetsCurrentClient(config internalconfig.Config, request subtaskExecutionRequest) bool {
	if request.TargetClientID != "" && request.TargetClientID != strings.TrimSpace(config.ClientID) {
		return false
	}
	if request.TargetDeviceID != "" && request.TargetDeviceID != strings.TrimSpace(config.DeviceID) {
		return false
	}
	return true
}

func waitForClientCollaboration(ctx context.Context, providerClient provider.Provider, taskID string, timeoutSeconds int) (*provider.ClientCollaborationTask, error) {
	if timeoutSeconds < 1 {
		timeoutSeconds = defaultSubtaskTimeoutSeconds
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(defaultSubtaskPollInterval)
	defer ticker.Stop()
	for {
		task, err := providerClient.GetClientCollaboration(waitCtx, taskID)
		if err != nil {
			return nil, newToolError("EXECUTION_FAILED", "轮询子任务状态失败: %v", err)
		}
		if task != nil {
			switch strings.TrimSpace(task.Status) {
			case "success", "failed", "offline":
				return task, nil
			}
		}
		select {
		case <-waitCtx.Done():
			return nil, newToolError("EXECUTION_FAILED", "等待子任务完成超时")
		case <-ticker.C:
		}
	}
}

func buildSubtaskTweaks(contextDir string, commandExecutionTarget string) map[string]any {
	return map[string]any{
		"taiyiflow_environment": buildSubtaskEnvironmentHint(contextDir, commandExecutionTarget),
	}
}

func buildSubtaskEnvironmentHint(contextDir string, commandExecutionTarget string) string {
	resolvedTarget := strings.TrimSpace(commandExecutionTarget)
	if resolvedTarget == "" {
		resolvedTarget = "client"
	}
	lines := []string{
		"<taiyiflow_environment>",
		fmt.Sprintf("OS: %s", goruntime.GOOS),
		fmt.Sprintf("ARCH: %s", goruntime.GOARCH),
		fmt.Sprintf("工作目录: %s", strings.TrimSpace(contextDir)),
		fmt.Sprintf("命令执行目标: %s", resolvedTarget),
		"</taiyiflow_environment>",
	}
	return strings.Join(lines, "\n")
}

func loadSessionStateForSubtask(payload map[string]any, config internalconfig.Config) (agent.SessionState, error) {
	flowID := strings.TrimSpace(stringValue(payload["flow_id"]))
	if flowID == "" {
		return agent.SessionState{}, fmt.Errorf("当前上下文缺少 flow_id")
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		return agent.SessionState{}, err
	}
	return store.Load(flowID, config.Workdir)
}

func extractSubtaskEventText(payload map[string]any) string {
	for _, key := range []string{"chunk", "text", "content"} {
		if text := stringValue(payload[key]); text != "" {
			return text
		}
	}
	return ""
}

func resolveSubtaskEventMessageID(payload map[string]any) string {
	for _, key := range []string{"message_id", "id"} {
		if value := strings.TrimSpace(stringValue(payload[key])); value != "" {
			return value
		}
	}
	return strings.TrimSpace(nestedString(payload, "context", "message_id"))
}

func resolveSubtaskErrorMessage(payload map[string]any) string {
	for _, key := range []string{"error", "message", "detail"} {
		if value := strings.TrimSpace(stringValue(payload[key])); value != "" {
			return value
		}
	}
	return "未提供错误详情"
}

func firstNonEmptySubtask(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func newSubtaskRunID() string {
	raw := make([]byte, 16)
	if _, err := cryptorand.Read(raw); err != nil {
		return fmt.Sprintf("subtask-%d", time.Now().UnixNano())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

// splitThinkingContent 从文本中提取 <think> 标签内容和剩余文本
// 返回值: reason (<think> 标签内的内容), text (</think> 标签后的内容)
// 如果存在多个 <think> 块，取最后一个
func splitThinkingContent(content string) (reason string, text string) {
	const openTag = "<think>"
	const closeTag = "</think>"

	// 查找最后一个 </think> 标签的位置
	lastCloseIdx := strings.LastIndex(content, closeTag)
	if lastCloseIdx == -1 {
		// 没有 </think> 标签，整个内容作为 text
		return "", content
	}

	// 查找对应的 <think> 标签
	searchStart := 0
	lastOpenIdx := -1
	for {
		idx := strings.Index(content[searchStart:], openTag)
		if idx == -1 {
			break
		}
		absIdx := searchStart + idx
		if absIdx < lastCloseIdx {
			lastOpenIdx = absIdx
			searchStart = absIdx + len(openTag)
		} else {
			break
		}
	}

	if lastOpenIdx == -1 {
		// 有 </think> 但没有对应的 <think>，整个内容作为 text
		return "", content
	}

	// 提取 reason: <think> 和 </think> 之间的内容
	reasonStart := lastOpenIdx + len(openTag)
	reason = strings.TrimSpace(content[reasonStart:lastCloseIdx])

	// 提取 text: </think> 之后的内容
	text = strings.TrimSpace(content[lastCloseIdx+len(closeTag):])

	return reason, text
}