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

	// 协同任务状态更新重试参数
	collaborationUpdateMaxRetries    = 3
	collaborationUpdateBaseDelay     = 1 * time.Second
	collaborationUpdateBackoffFactor = 2
)

// updateCollaborationWithRetry 向服务端更新协同任务状态，支持自动重试和指数退避。
// 使用 DetachedCallbackContext 确保父上下文取消或超时后仍能提交。
// 重试策略: 首次尝试 + 最多 collaborationUpdateMaxRetries 次重试，
// 退避间隔按指数增长（1s → 2s → 4s）。
// logf 为可选的日志函数，传 nil 则不记录重试日志。
func updateCollaborationWithRetry(
	ctx context.Context,
	providerClient provider.Provider,
	timeout time.Duration,
	taskID string,
	request provider.ClientCollaborationUpdateRequest,
	logf func(format string, args ...any),
) (*provider.ClientCollaborationTask, error) {
	callbackCtx, cancel := DetachedCallbackContext(ctx, timeout)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt <= collaborationUpdateMaxRetries; attempt++ {
		task, err := providerClient.UpdateClientCollaboration(callbackCtx, taskID, request)
		if err == nil {
			return task, nil
		}
		lastErr = err
		if attempt < collaborationUpdateMaxRetries {
			delay := collaborationUpdateBaseDelay * time.Duration(collaborationUpdateBackoffFactor<<attempt)
			if logf != nil {
				logf("[协同任务] 状态更新失败，第 %d 次重试（等待 %v）: task_id=%s err=%v", attempt+1, delay, taskID, err)
			}
			// 修复：退避等待响应 context 取消，避免进程退出被无谓延迟
			select {
			case <-callbackCtx.Done():
				return nil, fmt.Errorf("协同任务状态更新失败（等待重试期间被取消）: %w", callbackCtx.Err())
			case <-time.After(delay):
			}
		}
	}
	return nil, fmt.Errorf("协同任务状态更新失败（已重试 %d 次）: %w", collaborationUpdateMaxRetries, lastErr)
}

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
	// Features 携带发布端的能力参数快照（reasoning/internet_search/knowledge 等），
	// 用于跨客户端协同时让执行者端继承发布端的能力设定。
	Features map[string]any
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
		return map[string]any{
			"status":        "failed",
			"error_message": "客户端协同任务执行需要可用的 provider",
		}, nil
	}
	if task == nil {
		return map[string]any{
			"status":        "failed",
			"error_message": "客户端协同任务不能为空",
		}, nil
	}
	request, err := r.parseSubtaskExecutionRequestFromTask(ctx, providerClient, config, task)
	if err != nil {
		// 解析失败时向服务端写回失败状态，避免任务卡在非终态。
		r.reportCollaborationTaskFailure(ctx, providerClient, config, task.ID, err.Error())
		result := buildSubtaskResult(task, nil)
		result["status"] = "failed"
		result["error_message"] = err.Error()
		return result, nil
	}
	executionResult, execErr := r.executeLocalSubtask(ctx, providerClient, config, request)
	if execErr != nil {
		// 执行失败时向服务端写回失败状态，并携带已收集的部分内容。
		r.reportCollaborationTaskFailureWithResult(ctx, providerClient, config, task.ID, execErr.Error(), request, executionResult)
		result := buildSubtaskResult(task, &executionResult)
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
		return map[string]any{
			"status":        "failed",
			"error_message": "run_subtask 需要可用的 provider",
		}, nil
	}

	requests, maxParallelism, err := r.parseSubtaskExecutionRequests(ctx, providerClient, config, payload, arguments)
	if err != nil {
		return map[string]any{
			"status":        "failed",
			"error_message": err.Error(),
		}, nil
	}
	if len(requests) == 1 {
		result, subErr := r.executeManagedSubtaskRequest(ctx, providerClient, config, requests[0])
		if subErr != nil {
			return map[string]any{
				"status":        "failed",
				"error_message": subErr.Error(),
			}, nil
		}
		return result, nil
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
		return map[string]any{
			"status":        "failed",
			"error_message": fmt.Sprintf("创建子任务失败: %v", err),
		}, nil
	}

	if shouldRunSubtaskRemotely(createdTask, config, request) {
		if !request.WaitForCompletion {
			return buildSubtaskResult(createdTask, nil), nil
		}
		finalTask, waitErr := waitForClientCollaboration(ctx, providerClient, createdTask.ID, request.TimeoutSeconds)
		if waitErr != nil {
			// 等待远程子任务失败或超时时，向服务端写回失败状态，避免任务卡在 running/dispatched。
			_, updateErr := updateCollaborationWithRetry(ctx, providerClient, time.Duration(config.HTTPTimeoutSeconds)*time.Second, createdTask.ID, provider.ClientCollaborationUpdateRequest{
				Status:       "failed",
				ErrorMessage: waitErr.Error(),
				StatusPayload: map[string]any{
					"executor": "remote_wait",
					"target":   "remote",
				},
			}, func(format string, args ...any) {
				r.writeStreamEventf(r.stderr, format+"\n", args...)
			})
			result := buildSubtaskResult(createdTask, nil)
			result["status"] = "failed"
			result["error_message"] = waitErr.Error()
			if updateErr != nil {
				result["error_message"] = fmt.Sprintf("子任务等待失败: %v；写回失败状态也失败: %v", waitErr, updateErr)
			}
			return result, nil
		}
		result := buildSubtaskResult(finalTask, nil)
		if strings.TrimSpace(finalTask.Status) != "success" {
			result["error_message"] = firstNonEmptySubtask(strings.TrimSpace(finalTask.ErrorMessage), strings.TrimSpace(finalTask.Status))
			return result, nil
		}
		return result, nil
	}

	if _, err := updateCollaborationWithRetry(ctx, providerClient, time.Duration(config.HTTPTimeoutSeconds)*time.Second, createdTask.ID, provider.ClientCollaborationUpdateRequest{
		Status: "running",
		StatusPayload: map[string]any{
			"executor": "local_serial",
			"target":   "self",
		},
	}, func(format string, args ...any) {
		r.writeStreamEventf(r.stderr, format+"\n", args...)
	}); err != nil {
		result := buildSubtaskResult(createdTask, nil)
		result["status"] = "failed"
		result["error_message"] = fmt.Sprintf("更新子任务运行状态失败: %v", err)
		return result, nil
	}

	executionResult, execErr := r.executeLocalSubtask(ctx, providerClient, config, request)
	if execErr != nil {
		// 执行失败时，将已收集的部分内容一并写回服务端，避免丢失。
		partialResultPayload := buildSubtaskResultPayload(request, executionResult)
		updatedTask, updateErr := updateCollaborationWithRetry(ctx, providerClient, time.Duration(config.HTTPTimeoutSeconds)*time.Second, createdTask.ID, provider.ClientCollaborationUpdateRequest{
			Status:        "failed",
			ErrorMessage:  execErr.Error(),
			ResultPayload: partialResultPayload,
			StatusPayload: map[string]any{
				"executor": "local_serial",
				"target":   "self",
			},
		}, func(format string, args ...any) {
			r.writeStreamEventf(r.stderr, format+"\n", args...)
		})
		if updateErr != nil {
			result := buildSubtaskResult(createdTask, &executionResult)
			result["status"] = "failed"
			result["error_message"] = fmt.Sprintf("子任务执行失败: %v；写回失败状态也失败: %v", execErr, updateErr)
			return result, nil
		}
		result := buildSubtaskResult(updatedTask, &executionResult)
		result["error_message"] = execErr.Error()
		return result, nil
	}

	updatedTask, err := updateCollaborationWithRetry(ctx, providerClient, time.Duration(config.HTTPTimeoutSeconds)*time.Second, createdTask.ID, provider.ClientCollaborationUpdateRequest{
		Status: "success",
		StatusPayload: map[string]any{
			"executor": "local_serial",
			"target":   "self",
		},
		ResultPayload: buildSubtaskResultPayload(request, executionResult),
	}, func(format string, args ...any) {
		r.writeStreamEventf(r.stderr, format+"\n", args...)
	})
	if err != nil {
		// 写回成功状态失败不应丢弃执行结果，仍然返回子任务输出。
		result := buildSubtaskResult(createdTask, &executionResult)
		result["status"] = "success"
		result["error_message"] = fmt.Sprintf("更新子任务成功状态失败: %v", err)
		return result, nil
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
		DeliveryPolicy:    firstNonEmptySubtask(strings.TrimSpace(argumentString(arguments, "delivery_policy", "deliveryPolicy")), "local_default"),
		WaitForCompletion: waitForCompletion,
		TimeoutSeconds:    argumentInt(arguments, config.SubtaskTimeoutSeconds, "timeout_seconds", "timeoutSeconds"),
		ParentSessionID:   firstNonEmptySubtask(strings.TrimSpace(argumentString(arguments, "session_id", "sessionId")), strings.TrimSpace(nestedString(payload, "context", "session_id"))),
		ParentRunID:       firstNonEmptySubtask(strings.TrimSpace(argumentString(arguments, "run_id", "runId")), strings.TrimSpace(nestedString(payload, "context", "run_id"))),
		ParentMessageID:   firstNonEmptySubtask(strings.TrimSpace(argumentString(arguments, "message_id", "messageId")), strings.TrimSpace(nestedString(payload, "context", "message_id"))),
		// 携带发布端能力快照，供跨客户端执行者继承
		Features: r.buildSubtaskFeaturesPayload(),
	}
	if request.TimeoutSeconds < 0 {
		request.TimeoutSeconds = config.SubtaskTimeoutSeconds
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
	// 用配置值限制最大并发数
	configMax := r.MaxParallelismSubtask()
	if maxParallelism > configMax {
		maxParallelism = configMax
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
	// 从 command_payload 中读取发布端的能力参数，覆盖本地 runtime 的能力设定。
	// 确保跨客户端协同时执行者继承发布端的深度思考、联网搜索、知识库等设定。
	if rawFeatures, ok := task.CommandPayload["features"]; ok {
		if featuresMap, ok := rawFeatures.(map[string]any); ok {
			r.applySubtaskFeaturesFromPayload(featuresMap)
			request.Features = featuresMap
		}
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
	// 动态超时：超过 TimeoutSeconds 未收到任何 CHUNK 则判定为超时。
	// 每收到一个流事件即重置计时器，只要数据持续流动就不会超时。
	executionCtx, execCancel := context.WithCancel(ctx)
	var idleTimer *time.Timer
	var watchdogDone chan struct{}
	if request.TimeoutSeconds > 0 {
		idleTimer = time.NewTimer(time.Duration(request.TimeoutSeconds) * time.Second)
		watchdogDone = make(chan struct{})
		go func() {
			defer close(watchdogDone)
			select {
			case <-idleTimer.C:
				execCancel()
			case <-executionCtx.Done():
			}
		}()
	}
	defer func() {
		execCancel() // 先取消上下文，让 watchdog 退出
		if idleTimer != nil {
			idleTimer.Stop()
		}
		if watchdogDone != nil {
			<-watchdogDone
		}
	}()

	// NOTE: ClientInstructions is intentionally not passed to subtasks.
	// Subtasks receive their instructions from the parent task's prompt.
	// Passing ClientInstructions here would cause double-injection of
	// project-level instruction files (CLAUDE.md, AGENT.md, etc.).
	stream, streamSession, err := providerClient.Stream(executionCtx, provider.StreamRequest{
		FlowID:                    request.FlowID,
		Input:                     request.Prompt,
		InputType:                 "any",
		Tweaks:                    buildSubtaskTweaks(request.WorkingDirectory, "client"),
		InternetSearch:            r.subtaskInternetSearch,
		KnowledgeScopes:           append([]string(nil), r.subtaskKnowledgeScopes...),
		KnowledgeDocs:             append([]string(nil), r.subtaskKnowledgeDocs...),
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
		ClientMCPServers:          filterSubtaskExcludedTools(filterMCPServersByNames(r.ClientMCPServersForMode(request.RequestMode, config), r.subtaskClientMCPServerNames)),
		ClientSkillPackages:       r.subtaskSkillPackages,
		ParentID:                  request.ParentRunID,
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

	// buildPartialResult 从已收集的流输出中构建部分结果。
	// 用于超时或执行出错时保留已收到的内容，避免丢弃。
	buildPartialResult := func() subtaskExecutionResult {
		rawOutput := strings.TrimSpace(outputBuilder.String())
		reason, text := splitThinkingContent(rawOutput)
		return subtaskExecutionResult{
			Text:           text,
			Reason:         reason,
			ChildSessionID: childSessionID,
			ChildRunID:     request.ChildRunID,
			ChildMessageID: childMessageID,
		}
	}

	for event := range stream {
		// 收到任意流事件即重置空闲计时器
		// 修复：Reset 前必须先 Stop 并排空 channel，避免 Go < 1.23 上的竞态
		if idleTimer != nil {
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(time.Duration(request.TimeoutSeconds) * time.Second)
		}
		if messageID := resolveSubtaskEventMessageID(event.Data); messageID != "" {
			childMessageID = messageID
		}
		switch event.Type {
		case provider.EventMessage:
			if text := extractSubtaskEventText(event.Data); text != "" {
				outputBuilder.WriteString(text)
			}
		case provider.EventBuiltIn:
			if err := quietRuntime.HandleBuiltInEvent(executionCtx, providerClient, childConfig, event.Data); err != nil {
				// 出错时返回已收集的部分内容，供调用方写回服务端
				return buildPartialResult(), err
			}
		case provider.EventClientTool:
			if err := quietRuntime.HandleClientToolEvent(executionCtx, providerClient, childConfig, event.Data); err != nil {
				// 出错时返回已收集的部分内容，供调用方写回服务端
				return buildPartialResult(), err
			}
		case provider.EventError:
			// 出错时返回已收集的部分内容，供调用方写回服务端
			return buildPartialResult(), newToolError("EXECUTION_FAILED", "子任务执行失败: %s", resolveSubtaskErrorMessage(event.Data))
		}
	}

	// 循环结束后检测空闲超时：如果上下文因 idle timer 触发而被取消，
	// 返回已收集的部分内容并附带超时错误。
	if executionCtx.Err() != nil && ctx.Err() == nil {
		return buildPartialResult(), newToolError("EXECUTION_FAILED", "子任务空闲超时（超过 %d 秒未收到任何 CHUNK）", request.TimeoutSeconds)
	}

	// 正常结束：解析 <think> 标签，分离 reason 和 text
	return buildPartialResult(), nil
}

// reportCollaborationTaskFailure 向服务端写回协同任务的失败状态。
// 使用 updateCollaborationWithRetry 确保即使父上下文已超时或取消，仍能可靠写回，
// 并通过自动重试和指数退避提高写回成功率。
func (r *ClientRuntime) reportCollaborationTaskFailure(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	taskID string,
	errorMessage string,
) {
	if providerClient == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	trimmedMessage := firstNonEmptySubtask(strings.TrimSpace(errorMessage), "客户端协同任务执行失败")
	_, err := updateCollaborationWithRetry(ctx, providerClient, time.Duration(config.HTTPTimeoutSeconds)*time.Second, taskID, provider.ClientCollaborationUpdateRequest{
		Status:       "failed",
		ErrorMessage: trimmedMessage,
		StatusPayload: map[string]any{
			"executor": "local_direct",
			"target":   "self",
		},
	}, func(format string, args ...any) {
		r.writeStreamEventf(r.stderr, format+"\n", args...)
	})
	if err != nil {
		// 写回失败不影响后续流程，仅记录日志。
		r.writeStreamEventf(r.stderr, "[协同任务] 写回失败状态失败: task_id=%s err=%v\n", taskID, err)
	}
}

// reportCollaborationTaskFailureWithResult 向服务端写回协同任务的失败状态，并携带已收集的部分执行结果。
// 用于子任务执行过程中发生错误时，将已收到的部分内容一并写回，避免丢失。
func (r *ClientRuntime) reportCollaborationTaskFailureWithResult(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	taskID string,
	errorMessage string,
	request subtaskExecutionRequest,
	executionResult subtaskExecutionResult,
) {
	if providerClient == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	trimmedMessage := firstNonEmptySubtask(strings.TrimSpace(errorMessage), "客户端协同任务执行失败")
	partialResultPayload := buildSubtaskResultPayload(request, executionResult)
	_, err := updateCollaborationWithRetry(ctx, providerClient, time.Duration(config.HTTPTimeoutSeconds)*time.Second, taskID, provider.ClientCollaborationUpdateRequest{
		Status:        "failed",
		ErrorMessage:  trimmedMessage,
		ResultPayload: partialResultPayload,
		StatusPayload: map[string]any{
			"executor": "local_direct",
			"target":   "self",
		},
	}, func(format string, args ...any) {
		r.writeStreamEventf(r.stderr, format+"\n", args...)
	})
	if err != nil {
		// 写回失败不影响后续流程，仅记录日志。
		r.writeStreamEventf(r.stderr, "[协同任务] 写回失败状态（含部分内容）失败: task_id=%s err=%v\n", taskID, err)
	}
}

// buildSubtaskFeaturesPayload 从当前 runtime 的能力字段构建 features 快照。
// 用于跨客户端协同时将发布端的能力设定（深度思考、联网搜索、知识库、技能、MCP）透传给执行者端。
func (r *ClientRuntime) buildSubtaskFeaturesPayload() map[string]any {
	features := map[string]any{
		"reasoning":       r.subtaskReasoningEnabled,
		"internet_search": r.subtaskInternetSearch,
	}
	if len(r.subtaskKnowledgeScopes) > 0 {
		features["knowledge_scopes"] = append([]string(nil), r.subtaskKnowledgeScopes...)
	}
	if len(r.subtaskKnowledgeDocs) > 0 {
		features["knowledge_docs"] = append([]string(nil), r.subtaskKnowledgeDocs...)
	}
	// 携带发布端请求的技能名称列表，供执行者端过滤本地技能
	if len(r.subtaskRequestedSkillNames) > 0 {
		features["requested_skill_names"] = append([]string(nil), r.subtaskRequestedSkillNames...)
	}
	// 携带发布端使用的 MCP server 名称列表，供执行者端过滤本地 MCP servers
	mcpServerNames := r.clientMCPServerNames()
	if len(mcpServerNames) > 0 {
		features["client_mcp_server_names"] = mcpServerNames
	}
	return features
}

// clientMCPServerNames 提取当前 runtime 已注册的 MCP server 名称列表。
// 用于 features 快照，让跨客户端执行者知道发布端使用了哪些 MCP server。
func (r *ClientRuntime) clientMCPServerNames() []string {
	servers := r.ClientMCPServers()
	if len(servers) == 0 {
		return nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	return names
}

// applySubtaskFeaturesFromPayload 从协同任务 command_payload 中的 features 字段
// 覆盖当前 runtime 的能力设定。用于执行者端继承发布端的能力参数。
// 若 payload 中无 features 或字段缺失，则保持 runtime 现有值不变。
func (r *ClientRuntime) applySubtaskFeaturesFromPayload(features map[string]any) {
	if len(features) == 0 {
		return
	}
	if rawReasoning, ok := features["reasoning"]; ok {
		r.subtaskReasoningEnabled = boolValue(rawReasoning)
	}
	if rawSearch, ok := features["internet_search"]; ok {
		r.subtaskInternetSearch = boolValue(rawSearch)
	}
	if rawScopes, ok := features["knowledge_scopes"]; ok {
		r.subtaskKnowledgeScopes = stringSliceValue(rawScopes)
	}
	if rawDocs, ok := features["knowledge_docs"]; ok {
		r.subtaskKnowledgeDocs = stringSliceValue(rawDocs)
	}
	// 继承发布端请求的技能名称列表，执行者端据此过滤本地技能
	if rawSkillNames, ok := features["requested_skill_names"]; ok {
		r.subtaskRequestedSkillNames = stringSliceValue(rawSkillNames)
	}
	// 继承发布端使用的 MCP server 名称列表，执行者端据此过滤本地 MCP servers
	if rawMCPServers, ok := features["client_mcp_server_names"]; ok {
		r.subtaskClientMCPServerNames = stringSliceValue(rawMCPServers)
	}
}

func buildSubtaskCommandPayload(request subtaskExecutionRequest) map[string]any {
	payload := map[string]any{
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
	// 携带发布端能力快照，供跨客户端执行者继承
	if len(request.Features) > 0 {
		payload["features"] = request.Features
	}
	return payload
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
			// 思考内容（reason）不放入返回结果，避免 <tag>thinking</tag> 标签内容干扰父智能体
		}
		if strings.TrimSpace(task.ErrorMessage) != "" {
			result["error_message"] = task.ErrorMessage
		}
	}
	if execution != nil {
		if strings.TrimSpace(execution.Text) != "" {
			result["text"] = execution.Text
		}
		// 思考内容（reason）不放入返回结果，避免 <tag>thinking</tag> 标签内容干扰父智能体
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
	// 思考内容（reason）不写入服务端 payload，避免 <tag>thinking</tag> 标签内容干扰父智能体
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
	var waitCtx context.Context
	var cancel context.CancelFunc
	if timeoutSeconds > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	} else {
		waitCtx, cancel = ctx, func() {}
	}
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

// subtaskExcludedTools 列出子智能体不允许使用的工具名称。
// 子智能体执行时不应再调用 run_subtask，防止无限嵌套。
var subtaskExcludedTools = map[string]bool{
	"run_subtask": true,
}

// filterSubtaskExcludedTools 从 MCP servers 工具定义中移除子智能体不允许使用的工具，
// 防止子智能体在执行时再次调用 run_subtask 产生无限嵌套。
func filterSubtaskExcludedTools(servers map[string]any) map[string]any {
	if len(servers) == 0 {
		return servers
	}
	filtered := make(map[string]any, len(servers))
	for serverName, tools := range servers {
		definitions, ok := tools.([]map[string]any)
		if !ok {
			filtered[serverName] = tools
			continue
		}
		cleaned := make([]map[string]any, 0, len(definitions))
		for _, def := range definitions {
			name := strings.TrimSpace(stringValue(def["name"]))
			if subtaskExcludedTools[name] {
				continue
			}
			cleaned = append(cleaned, def)
		}
		filtered[serverName] = cleaned
	}
	return filtered
}

// filterMCPServersByNames 按名称白名单过滤 MCP servers。
// 跨客户端协同时，发布端通过 features 携带 client_mcp_server_names，
// 执行者端据此只保留发布端使用过的 MCP server，确保子智能体复用主智能体的 clientMcp。
// 若 allowedNames 为空则不过滤（向后兼容旧版本发布端）。
func filterMCPServersByNames(servers map[string]any, allowedNames []string) map[string]any {
	if len(allowedNames) == 0 || len(servers) == 0 {
		return servers
	}
	allowed := make(map[string]bool, len(allowedNames))
	for _, name := range allowedNames {
		allowed[strings.TrimSpace(name)] = true
	}
	filtered := make(map[string]any, len(servers))
	for serverName, tools := range servers {
		if allowed[serverName] {
			filtered[serverName] = tools
		}
	}
	return filtered
}

// splitThinkingContent 从文本中提取所有 <think> 标签内容和剩余文本。
// 返回值: reason (所有 <think> 标签内的内容合并), text (所有非 <think> 文本合并)。
// 支持流式输出中可能出现多段 <think> 块的情况。
func splitThinkingContent(content string) (reason string, text string) {
	const openTag = "<think>"
	const closeTag = "</think>"

	var reasonParts []string
	var textParts []string

	cursor := 0
	for {
		// 查找下一个 <think> 标签
		openIdx := strings.Index(content[cursor:], openTag)
		if openIdx == -1 {
			// 没有更多 <think> 标签，剩余内容全部作为 text
			if cursor < len(content) {
				if trimmed := strings.TrimSpace(content[cursor:]); trimmed != "" {
					textParts = append(textParts, trimmed)
				}
			}
			break
		}
		absOpenIdx := cursor + openIdx

		// <think> 标签之前的文本归入 text
		if absOpenIdx > cursor {
			if trimmed := strings.TrimSpace(content[cursor:absOpenIdx]); trimmed != "" {
				textParts = append(textParts, trimmed)
			}
		}

		// 查找对应的 </think> 标签
		afterOpen := absOpenIdx + len(openTag)
		closeIdx := strings.Index(content[afterOpen:], closeTag)
		if closeIdx == -1 {
			// <think> 标签未闭合，将 <think> 标签后的所有内容作为 reason
			if afterOpen < len(content) {
				reasonParts = append(reasonParts, strings.TrimSpace(content[afterOpen:]))
			}
			break
		}
		absCloseIdx := afterOpen + closeIdx

		// 提取 <think> 和 </think> 之间的内容作为 reason
		reasonParts = append(reasonParts, strings.TrimSpace(content[afterOpen:absCloseIdx]))

		// 移动游标到 </think> 标签之后
		cursor = absCloseIdx + len(closeTag)
	}

	reason = strings.Join(reasonParts, "\n\n")
	text = strings.Join(textParts, "\n\n")

	return reason, text
}
