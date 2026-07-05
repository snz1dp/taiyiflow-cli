package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const (
	collaborationMessageType                  = "collaboration"
	collaborationEventConnectionReady         = "connection.ready"
	collaborationEventConnectionError         = "connection.error"
	collaborationEventConnectionPing          = "connection.ping"
	collaborationEventConnectionPong          = "connection.pong"
	collaborationEventTaskDispatch            = "task.dispatch"
	collaborationEventTaskAck                 = "task.ack"
	collaborationEventTaskStatus              = "task.status"
	collaborationEventTaskResult              = "task.result"
	collaborationEventTaskError               = "task.error"
	collaborationEventTasksFetchRecent        = "tasks.fetch_recent"
	collaborationEventTasksRecent             = "tasks.recent"
	defaultWorkerRequestTimeout               = 5 * time.Minute
	defaultWorkerReconnectDelay               = 3 * time.Second
	defaultWorkerPingInterval                 = 45 * time.Second
	defaultWorkerRecentTaskLimit              = 20
	defaultWorkerMaxConcurrency               = 4
	collaborationWorkerExecutor               = "client_websocket_worker"
	collaborationWorkerFallbackTransport      = "http_update_fallback"
	collaborationWorkerRecoveryStatusPending  = "pending"
	collaborationWorkerRecoveryStatusDispatch = "dispatched"
)

type CollaborationWorkerOptions struct {
	RecentTaskLimit int
	MaxConcurrency  int
	ReconnectDelay  time.Duration
	PingInterval    time.Duration
	Logf            func(format string, args ...any)
}

type collaborationEnvelope struct {
	Type    string          `json:"type"`
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

type collaborationDispatchPayload struct {
	TaskID          string         `json:"task_id"`
	TaskKind        string         `json:"task_kind"`
	DeliveryPolicy  string         `json:"delivery_policy"`
	SourceSessionID string         `json:"source_session_id"`
	SourceRunID     string         `json:"source_run_id"`
	SourceMessageID string         `json:"source_message_id"`
	ChildSessionID  string         `json:"child_session_id"`
	ChildRunID      string         `json:"child_run_id"`
	TargetClientID  string         `json:"target_client_id"`
	TargetDeviceID  string         `json:"target_device_id"`
	Command         map[string]any `json:"command"`
}

type collaborationRecentTasksPayload struct {
	Tasks []collaborationTaskSnapshot `json:"tasks"`
}

type collaborationTaskSnapshot struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

type collaborationWorker struct {
	runtime    *ClientRuntime
	provider   provider.Provider
	config     *internalconfig.Config
	options    CollaborationWorkerOptions
	logf       func(format string, args ...any)
	semaphore  chan struct{}
	writeMu    sync.Mutex
	activeMu   sync.Mutex
	activeTask map[string]struct{}
}

func (r *ClientRuntime) RunCollaborationWorker(
	ctx context.Context,
	providerClient provider.Provider,
	config *internalconfig.Config,
	options CollaborationWorkerOptions,
) error {
	if providerClient == nil {
		return fmt.Errorf("协同驻留进程需要可用的服务提供方")
	}
	if config == nil {
		return fmt.Errorf("协同驻留进程缺少配置")
	}
	worker := newCollaborationWorker(r, providerClient, config, options)
	return worker.run(ctx)
}

func newCollaborationWorker(
	runtimeClient *ClientRuntime,
	providerClient provider.Provider,
	config *internalconfig.Config,
	options CollaborationWorkerOptions,
) *collaborationWorker {
	reconnectDelay := options.ReconnectDelay
	if reconnectDelay <= 0 {
		reconnectDelay = defaultWorkerReconnectDelay
	}
	pingInterval := options.PingInterval
	if pingInterval <= 0 {
		pingInterval = defaultWorkerPingInterval
	}
	recentLimit := options.RecentTaskLimit
	if recentLimit <= 0 {
		recentLimit = defaultWorkerRecentTaskLimit
	}
	maxConcurrency := options.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = defaultWorkerMaxConcurrency
	}
	clonedRuntime := runtimeClient.cloneRuntime()
	clonedRuntime.SetInteractivePromptsEnabled(false)
	clonedRuntime.SetStreamEventLogsEnabled(false)
	logf := options.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &collaborationWorker{
		runtime:  clonedRuntime,
		provider: providerClient,
		config:   config,
		options: CollaborationWorkerOptions{
			RecentTaskLimit: recentLimit,
			MaxConcurrency:  maxConcurrency,
			ReconnectDelay:  reconnectDelay,
			PingInterval:    pingInterval,
			Logf:            logf,
		},
		logf:       logf,
		semaphore:  make(chan struct{}, maxConcurrency),
		activeTask: map[string]struct{}{},
	}
}

func (w *collaborationWorker) run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := w.runConnection(ctx)
		if err == nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
		} else if ctx.Err() != nil {
			return ctx.Err()
		} else {
			w.logf("协同驻留进程连接中断: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.options.ReconnectDelay):
		}
	}
}

func (w *collaborationWorker) runConnection(ctx context.Context) error {
	config := w.currentConfig()
	wsURL, err := buildCollaborationWorkerWebSocketURL(config)
	if err != nil {
		return err
	}
	headers := http.Header{}
	if token := strings.TrimSpace(config.AccessToken); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	dialer := ws.Dialer{Header: ws.HandshakeHeaderHTTP(headers)}
	conn, _, _, err := dialer.Dial(ctx, wsURL)
	if err != nil {
		return fmt.Errorf("连接协同长连接失败: %w", err)
	}
	defer conn.Close()
	stopInterruptRead := interruptCollaborationWorkerConnectionOnCancel(ctx, conn)
	defer stopInterruptRead()
	w.logf("协同客户端已连接: 客户端标识=%s 设备标识=%s", strings.TrimSpace(config.ClientID), strings.TrimSpace(config.DeviceID))

	var pingCancel context.CancelFunc
	defer func() {
		if pingCancel != nil {
			pingCancel()
		}
	}()

	for {
		payload, opCode, readErr := wsutil.ReadServerData(conn)
		if readErr != nil {
			return readErr
		}
		if opCode != ws.OpText {
			continue
		}
		startPing, handleErr := w.handleServerEnvelope(ctx, conn, payload)
		if handleErr != nil {
			return handleErr
		}
		if startPing && pingCancel == nil {
			pingCtx, cancel := context.WithCancel(ctx)
			pingCancel = cancel
			go w.pingLoop(pingCtx, conn)
		}
	}
}

func interruptCollaborationWorkerConnectionOnCancel(ctx context.Context, conn net.Conn) func() {
	if ctx == nil || conn == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() {
		close(done)
	}
}

func (w *collaborationWorker) handleServerEnvelope(ctx context.Context, conn net.Conn, raw []byte) (bool, error) {
	var envelope collaborationEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return false, fmt.Errorf("解析协同长连接消息失败: %w", err)
	}
	if strings.TrimSpace(envelope.Type) != collaborationMessageType {
		return false, nil
	}
	startPing := false
	switch strings.TrimSpace(envelope.Event) {
	case collaborationEventConnectionReady:
		startPing = true
		if err := w.sendEnvelope(conn, collaborationEventTasksFetchRecent, map[string]any{"limit": w.options.RecentTaskLimit}); err != nil {
			return false, err
		}
	case collaborationEventConnectionError:
		message := "服务端返回连接错误"
		if len(envelope.Payload) > 0 {
			var payload map[string]any
			if err := json.Unmarshal(envelope.Payload, &payload); err == nil {
				message = firstNonEmptySubtask(strings.TrimSpace(stringValue(payload["message"])), message)
			}
		}
		return false, fmt.Errorf("%s", message)
	case collaborationEventConnectionPing:
		if err := w.sendEnvelope(conn, collaborationEventConnectionPong, map[string]any{}); err != nil {
			return false, err
		}
	case collaborationEventConnectionPong:
		return false, nil
	case collaborationEventTaskDispatch:
		var payload collaborationDispatchPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return false, fmt.Errorf("解析任务下发事件失败: %w", err)
		}
		w.scheduleTask(ctx, conn, w.buildTaskFromDispatch(payload))
	case collaborationEventTasksRecent:
		var payload collaborationRecentTasksPayload
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return false, fmt.Errorf("解析最近任务事件失败: %w", err)
		}
		for _, taskSnapshot := range payload.Tasks {
			w.loadAndScheduleTask(ctx, conn, taskSnapshot)
		}
	}
	return startPing, nil
}

func (w *collaborationWorker) pingLoop(ctx context.Context, conn net.Conn) {
	ticker := time.NewTicker(w.options.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.sendEnvelope(conn, collaborationEventConnectionPing, map[string]any{}); err != nil {
				w.logf("协同驻留进程心跳发送失败: %v", err)
				return
			}
		}
	}
}

func (w *collaborationWorker) buildTaskFromDispatch(payload collaborationDispatchPayload) *provider.ClientCollaborationTask {
	return &provider.ClientCollaborationTask{
		ID:              strings.TrimSpace(payload.TaskID),
		TargetClientID:  strings.TrimSpace(payload.TargetClientID),
		TargetDeviceID:  strings.TrimSpace(payload.TargetDeviceID),
		SourceSessionID: strings.TrimSpace(payload.SourceSessionID),
		SourceRunID:     strings.TrimSpace(payload.SourceRunID),
		SourceMessageID: strings.TrimSpace(payload.SourceMessageID),
		ChildSessionID:  strings.TrimSpace(payload.ChildSessionID),
		ChildRunID:      strings.TrimSpace(payload.ChildRunID),
		TaskKind:        strings.TrimSpace(payload.TaskKind),
		DeliveryPolicy:  strings.TrimSpace(payload.DeliveryPolicy),
		Status:          collaborationWorkerRecoveryStatusDispatch,
		CommandPayload:  payload.Command,
	}
}

func (w *collaborationWorker) loadAndScheduleTask(ctx context.Context, conn net.Conn, snapshot collaborationTaskSnapshot) {
	status := strings.TrimSpace(snapshot.Status)
	if status != collaborationWorkerRecoveryStatusPending && status != collaborationWorkerRecoveryStatusDispatch {
		return
	}
	taskID := strings.TrimSpace(snapshot.TaskID)
	if taskID == "" {
		return
	}
	task, err := w.provider.GetClientCollaboration(ctx, taskID)
	if err != nil {
		w.logf("拉取最近协同任务失败: task_id=%s err=%v", taskID, err)
		return
	}
	w.scheduleTask(ctx, conn, task)
}

func (w *collaborationWorker) scheduleTask(ctx context.Context, conn net.Conn, task *provider.ClientCollaborationTask) {
	if task == nil || strings.TrimSpace(task.ID) == "" {
		return
	}
	if !w.matchesCurrentClient(task) {
		return
	}
	if !w.activateTask(task.ID) {
		return
	}
	go w.executeTask(ctx, conn, task)
}

func (w *collaborationWorker) executeTask(ctx context.Context, conn net.Conn, task *provider.ClientCollaborationTask) {
	defer w.deactivateTask(task.ID)
	if err := w.sendEnvelope(conn, collaborationEventTaskAck, map[string]any{"task_id": task.ID}); err != nil {
		w.logf("发送协同任务确认回执失败: task_id=%s err=%v", task.ID, err)
	}
	select {
	case <-ctx.Done():
		return
	case w.semaphore <- struct{}{}:
	}
	defer func() {
		<-w.semaphore
	}()
	if err := w.sendEnvelope(conn, collaborationEventTaskStatus, map[string]any{
		"task_id":  task.ID,
		"status":   "running",
		"executor": collaborationWorkerExecutor,
	}); err != nil {
		w.logf("发送协同任务运行中状态失败: task_id=%s err=%v", task.ID, err)
	}

	execRuntime := w.runtime.cloneRuntime()
	execConfig := w.currentConfig()
	request, err := execRuntime.parseSubtaskExecutionRequestFromTask(ctx, w.provider, execConfig, task)
	if err != nil {
		w.reportTaskFailure(ctx, conn, task.ID, err.Error())
		return
	}
	executionResult, execErr := execRuntime.executeLocalSubtask(ctx, w.provider, execConfig, request)
	if execErr != nil {
		w.reportTaskFailure(ctx, conn, task.ID, execErr.Error())
		return
	}
	w.reportTaskSuccess(ctx, conn, task.ID, buildSubtaskResultPayload(request, executionResult))
}

func (w *collaborationWorker) reportTaskSuccess(ctx context.Context, conn net.Conn, taskID string, resultPayload map[string]any) {
	payload := map[string]any{
		"task_id":  taskID,
		"result":   resultPayload,
		"status":   "success",
		"executor": collaborationWorkerExecutor,
	}
	if err := w.sendEnvelope(conn, collaborationEventTaskResult, payload); err != nil {
		w.logf("发送协同任务成功结果失败，回退接口更新: task_id=%s err=%v", taskID, err)
		w.updateTaskFallback(ctx, taskID, provider.ClientCollaborationUpdateRequest{
			Status: "success",
			StatusPayload: map[string]any{
				"executor":  collaborationWorkerExecutor,
				"transport": collaborationWorkerFallbackTransport,
			},
			ResultPayload: resultPayload,
		})
	}
}

func (w *collaborationWorker) reportTaskFailure(ctx context.Context, conn net.Conn, taskID string, errorMessage string) {
	trimmedMessage := firstNonEmptySubtask(strings.TrimSpace(errorMessage), "客户端协同任务执行失败")
	payload := map[string]any{
		"task_id":  taskID,
		"error":    trimmedMessage,
		"message":  trimmedMessage,
		"status":   "failed",
		"executor": collaborationWorkerExecutor,
	}
	if err := w.sendEnvelope(conn, collaborationEventTaskError, payload); err != nil {
		w.logf("发送协同任务失败结果失败，回退接口更新: task_id=%s err=%v", taskID, err)
		w.updateTaskFallback(ctx, taskID, provider.ClientCollaborationUpdateRequest{
			Status:       "failed",
			ErrorMessage: trimmedMessage,
			StatusPayload: map[string]any{
				"executor":  collaborationWorkerExecutor,
				"transport": collaborationWorkerFallbackTransport,
			},
		})
	}
}

func (w *collaborationWorker) updateTaskFallback(ctx context.Context, taskID string, request provider.ClientCollaborationUpdateRequest) {
	// 使用独立上下文执行 HTTP 回退更新，避免父上下文（WebSocket 连接上下文）已取消时
	// 出现 "context deadline exceeded"，确保协同任务状态能可靠回报到服务端。
	// 超时时间使用系统配置中的 HTTP 请求超时设定。
	timeout := time.Duration(defaultWorkerRequestTimeout)
	if w.config != nil {
		timeout = time.Duration(w.config.HTTPTimeoutSeconds) * time.Second
	}

	callbackCtx, cancel := DetachedCallbackContext(ctx, timeout)
	defer cancel()
	if _, err := w.provider.UpdateClientCollaboration(callbackCtx, taskID, request); err != nil {
		w.logf("协同任务接口回退更新失败: task_id=%s err=%v", taskID, err)
	}
}

func (w *collaborationWorker) sendEnvelope(conn net.Conn, event string, payload map[string]any) error {
	envelope, err := json.Marshal(map[string]any{
		"type":    collaborationMessageType,
		"event":   strings.TrimSpace(event),
		"payload": payload,
	})
	if err != nil {
		return fmt.Errorf("编码协同长连接消息失败: %w", err)
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if err := wsutil.WriteClientText(conn, envelope); err != nil {
		return fmt.Errorf("发送协同长连接消息失败: %w", err)
	}
	return nil
}

func (w *collaborationWorker) activateTask(taskID string) bool {
	w.activeMu.Lock()
	defer w.activeMu.Unlock()
	if _, exists := w.activeTask[taskID]; exists {
		return false
	}
	w.activeTask[taskID] = struct{}{}
	return true
}

func (w *collaborationWorker) deactivateTask(taskID string) {
	w.activeMu.Lock()
	defer w.activeMu.Unlock()
	delete(w.activeTask, taskID)
}

func (w *collaborationWorker) matchesCurrentClient(task *provider.ClientCollaborationTask) bool {
	if task == nil {
		return false
	}
	config := w.currentConfig()
	if strings.TrimSpace(task.TargetClientID) != "" && strings.TrimSpace(task.TargetClientID) != strings.TrimSpace(config.ClientID) {
		return false
	}
	if strings.TrimSpace(task.TargetDeviceID) != "" && strings.TrimSpace(task.TargetDeviceID) != strings.TrimSpace(config.DeviceID) {
		return false
	}
	return true
}

func (w *collaborationWorker) currentConfig() internalconfig.Config {
	if w == nil || w.config == nil {
		return internalconfig.Default()
	}
	return *w.config
}

func buildCollaborationWorkerWebSocketURL(config internalconfig.Config) (string, error) {
	baseURL, err := url.Parse(strings.TrimSpace(config.BackendBaseURL))
	if err != nil {
		return "", fmt.Errorf("解析后台服务地址失败: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(baseURL.Scheme)) {
	case "https":
		baseURL.Scheme = "wss"
	case "http":
		baseURL.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", fmt.Errorf("不支持的后台服务地址协议: %s", baseURL.Scheme)
	}
	baseURL.Path = joinCollaborationWorkerURLPath(baseURL.Path, internalconfig.NormalizeBackendAPIPrefix(config.BackendAPIPrefix), "/agents/client/websocket")
	queryValues := baseURL.Query()
	queryValues.Set("client_id", strings.TrimSpace(config.ClientID))
	queryValues.Set("device_id", strings.TrimSpace(config.DeviceID))
	queryValues.Set("client_type", provider.ClientTypeCLI)
	queryValues.Set("device_type", goruntime.GOOS)
	queryValues.Set("platform", goruntime.GOOS+"-"+goruntime.GOARCH)
	queryValues.Set("client_version", buildinfo.Version)
	queryValues.Set("connection_role", "collaboration_worker")
	queryValues.Set("session_id", fmt.Sprintf("worker-%d", time.Now().UnixNano()))
	baseURL.RawQuery = queryValues.Encode()
	return baseURL.String(), nil
}

func joinCollaborationWorkerURLPath(parts ...string) string {
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		segments = append(segments, strings.Trim(trimmed, "/"))
	}
	joined := strings.Join(segments, "/")
	if joined == "" {
		return "/"
	}
	return "/" + joined
}
