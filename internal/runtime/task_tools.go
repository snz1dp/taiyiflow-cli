package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

const maxTaskOutputBytes = 128 * 1024

var sharedLocalTaskManager = newLocalTaskManager()

type localTaskManager struct {
	mu          sync.RWMutex
	tasks       map[string]*localTask
	loadedRoots map[string]bool
}

type localTask struct {
	id              string
	name            string
	preview         string
	cwd             string
	sourceSkill     string
	flowID          string
	sessionID       string
	runID           string
	injectedEnvKeys []string
	storeDir        string
	metadataPath    string
	stdoutPath      string
	stderrPath      string
	startedAt       time.Time
	completedAt     time.Time
	status          string
	exitCode        int
	hasExitCode     bool
	stdout          bytes.Buffer
	stderr          bytes.Buffer
	stdoutTruncated bool
	stderrTruncated bool
	lastError       string
	cancel          context.CancelFunc
	done            chan struct{}
	mu              sync.RWMutex
}

type taskStreamWriter struct {
	task   *localTask
	stderr bool
}

func newLocalTaskManager() *localTaskManager {
	return &localTaskManager{tasks: map[string]*localTask{}, loadedRoots: map[string]bool{}}
}

func ShutdownLocalTasks() {
	sharedLocalTaskManager.shutdown()
}

func CleanupFinishedLocalTasks() {
	sharedLocalTaskManager.cleanupFinished()
}

func (m *localTaskManager) shutdown() {
	tasks := m.snapshotTasks()
	for _, task := range tasks {
		task.mu.RLock()
		cancel := task.cancel
		done := task.done
		task.mu.RUnlock()
		if cancel != nil {
			cancel()
		}
		if done != nil {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
		_ = m.deleteTask(task)
	}
	storeDirs := m.loadedStoreDirs()
	if len(storeDirs) == 0 {
		if storeDir, err := defaultTaskStoreDir(); err == nil {
			storeDirs = append(storeDirs, storeDir)
		}
	}
	for _, storeDir := range storeDirs {
		_ = clearPersistedTaskStore(storeDir)
	}
	m.resetLoadedRoots()
}

func (m *localTaskManager) cleanupFinished() {
	tasks := m.snapshotTasks()
	for _, task := range tasks {
		task.mu.RLock()
		status := task.status
		task.mu.RUnlock()
		if status == "running" {
			continue
		}
		_ = m.deleteTask(task)
	}
	for _, storeDir := range m.loadedStoreDirs() {
		_ = clearPersistedTaskStore(storeDir)
	}
}

func (m *localTaskManager) snapshotTasks() []*localTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tasks := make([]*localTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	return tasks
}

func (r *ClientRuntime) shellExec(
	ctx context.Context,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	if !boolValue(arguments["background"]) {
		result, err := r.runCommand(ctx, config, arguments)
		if err != nil {
			return nil, err
		}
		result["mode"] = "foreground"
		return result, nil
	}
	return sharedLocalTaskManager.startTask(ctx, r, config, arguments)
}

func (r *ClientRuntime) taskOutput(
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	return sharedLocalTaskManager.taskOutput(r, config, arguments)
}

func (r *ClientRuntime) taskStop(
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	return sharedLocalTaskManager.taskStop(r, config, arguments)
}

func (r *ClientRuntime) taskDelete(
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	return sharedLocalTaskManager.taskDelete(r, config, arguments)
}

func (r *ClientRuntime) taskCleanup(
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	return sharedLocalTaskManager.taskCleanup(r, config, arguments)
}

func (r *ClientRuntime) taskList(config internalconfig.Config) (map[string]any, error) {
	return sharedLocalTaskManager.taskList(r, config), nil
}

func (r *ClientRuntime) terminalCapture(
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	return sharedLocalTaskManager.terminalCapture(r, config, arguments)
}

func (m *localTaskManager) startTask(
	ctx context.Context,
	runtimeClient *ClientRuntime,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	if err := m.ensureLoaded(config); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "初始化任务存储失败: %v", err)
	}
	payload, err := runtimeClient.normalizeCommandArguments(arguments, config)
	if err != nil {
		return nil, err
	}
	if err := guardDangerousCommand(payload, config); err != nil {
		return nil, err
	}
	backgroundCtx, cancel := context.WithCancel(context.Background())
	command, err := buildCommand(backgroundCtx, payload)
	if err != nil {
		cancel()
		return nil, err
	}
	command.Dir = payload.WorkingDirectory
	command.Env = mergeEnv(payload.EnvOverrides)
	stdin, err := runtimeClient.resolveCommandStdin(ctx, payload)
	if err != nil {
		cancel()
		return nil, err
	}
	if stdin != nil {
		command.Stdin = stdin
	}

	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("创建任务标准输出失败: %w", err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("创建任务错误输出失败: %w", err)
	}
	if err := command.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("启动后台任务失败: %w", err)
	}

	storeDir, err := defaultTaskStoreDir()
	if err != nil {
		cancel()
		return nil, newToolError("EXECUTION_FAILED", "解析任务存储目录失败: %v", err)
	}
	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())
	metadataPath, stdoutPath, stderrPath := taskFilePaths(storeDir, taskID)
	task := &localTask{
		id:              taskID,
		name:            strings.TrimSpace(fallbackString(argumentString(arguments, "taskName", "task_name"), taskID)),
		preview:         commandPreview(arguments),
		cwd:             payload.WorkingDirectory,
		sourceSkill:     payload.SourceSkill,
		flowID:          strings.TrimSpace(stringValue(arguments["flow_id"])),
		sessionID:       strings.TrimSpace(stringValue(arguments["session_id"])),
		runID:           strings.TrimSpace(stringValue(arguments["run_id"])),
		injectedEnvKeys: append([]string(nil), payload.InjectedEnvKeys...),
		storeDir:        storeDir,
		metadataPath:    metadataPath,
		stdoutPath:      stdoutPath,
		stderrPath:      stderrPath,
		startedAt:       time.Now(),
		status:          "running",
		cancel:          cancel,
		done:            make(chan struct{}),
	}

	m.mu.Lock()
	m.tasks[taskID] = task
	m.loadedRoots[storeDir] = true
	m.mu.Unlock()
	if err := m.persistTask(task); err != nil {
		task.mu.Lock()
		task.lastError = strings.TrimSpace(err.Error())
		task.mu.Unlock()
	}

	taskAudit := map[string]any{"task_id": taskID, "command": task.preview, "cwd": task.cwd}
	if task.sourceSkill != "" {
		taskAudit["source_skill"] = task.sourceSkill
	}
	if len(task.injectedEnvKeys) > 0 {
		taskAudit["injected_env_keys"] = append([]string(nil), task.injectedEnvKeys...)
	}
	_ = appendAuditLog(config, "task_start", taskAudit)
	runtimeClient.writeStreamEventf(runtimeClient.stdout, "[本地工具] shell_exec 后台任务 %s 已启动\n", taskID)

	go m.waitTask(task, command, stdoutPipe, stderrPipe, config)

	return taskSummary(task), nil
}

func (m *localTaskManager) waitTask(
	task *localTask,
	command *exec.Cmd,
	stdoutPipe io.ReadCloser,
	stderrPipe io.ReadCloser,
	config internalconfig.Config,
) {
	done := task.done
	defer func() {
		_ = m.deleteTask(task)
		if done != nil {
			close(done)
		}
	}()

	var copyGroup sync.WaitGroup
	copyGroup.Add(2)
	go func() {
		defer copyGroup.Done()
		_, _ = io.Copy(taskStreamWriter{task: task}, stdoutPipe)
	}()
	go func() {
		defer copyGroup.Done()
		_, _ = io.Copy(taskStreamWriter{task: task, stderr: true}, stderrPipe)
	}()
	waitErr := command.Wait()
	copyGroup.Wait()

	task.mu.Lock()
	task.completedAt = time.Now()
	if command.ProcessState != nil {
		task.exitCode = command.ProcessState.ExitCode()
		task.hasExitCode = true
	}
	if waitErr == nil {
		task.status = "completed"
	} else if errorsIsContextCanceled(waitErr) {
		task.status = "cancelled"
		task.lastError = strings.TrimSpace(waitErr.Error())
	} else {
		task.status = "failed"
		task.lastError = strings.TrimSpace(waitErr.Error())
	}
	task.cancel = nil
	task.mu.Unlock()
	if err := m.persistTask(task); err != nil {
		task.mu.Lock()
		task.lastError = strings.TrimSpace(err.Error())
		task.mu.Unlock()
	}
	_ = appendAuditLog(config, "task_complete", map[string]any{"task_id": task.id, "status": task.status, "exit_code": task.exitCode, "error": task.lastError})
}

func (w taskStreamWriter) Write(content []byte) (int, error) {
	w.task.appendOutput(content, w.stderr)
	return len(content), nil
}

func (task *localTask) appendOutput(content []byte, stderr bool) {
	task.mu.Lock()
	defer task.mu.Unlock()
	target := &task.stdout
	truncated := &task.stdoutTruncated
	if stderr {
		target = &task.stderr
		truncated = &task.stderrTruncated
	}
	remaining := maxTaskOutputBytes - target.Len()
	if remaining <= 0 {
		*truncated = true
		return
	}
	if len(content) > remaining {
		content = content[:remaining]
		*truncated = true
	}
	_, _ = target.Write(content)
	task.appendOutputFile(content, stderr)
}

func (m *localTaskManager) taskOutput(
	runtimeClient *ClientRuntime,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	if err := m.ensureLoaded(config); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取任务存储失败: %v", err)
	}
	task, err := m.lookupTask(config, arguments)
	if err != nil {
		return nil, err
	}
	result := taskDetails(task, intValue(arguments["max_bytes"], maxReadBytesDefault))
	runtimeClient.writeStreamEventf(runtimeClient.stdout, "[本地工具] task_output %s (%s)\n", task.id, taskStatusValue(task))
	_ = appendAuditLog(config, "task_output", map[string]any{"task_id": task.id, "status": result["status"]})
	return result, nil
}

func (m *localTaskManager) taskStop(
	runtimeClient *ClientRuntime,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	if err := m.ensureLoaded(config); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取任务存储失败: %v", err)
	}
	task, err := m.lookupTask(config, arguments)
	if err != nil {
		return nil, err
	}
	task.mu.RLock()
	status := task.status
	cancel := task.cancel
	task.mu.RUnlock()
	if status == "running" && cancel == nil {
		return nil, newToolError("EXECUTION_FAILED", "任务已恢复到当前会话，但未附着到当前进程，暂不支持停止")
	}
	if status == "running" && cancel != nil {
		cancel()
		result := taskSummary(task)
		result["status"] = "stopping"
		result["stop_requested"] = true
		runtimeClient.writeStreamEventf(runtimeClient.stdout, "[本地工具] task_stop %s (%s)\n", task.id, "stopping")
		_ = appendAuditLog(config, "task_stop", map[string]any{"task_id": task.id, "status": "stopping"})
		return result, nil
	}
	result := taskDetails(task, maxReadBytesDefault)
	runtimeClient.writeStreamEventf(runtimeClient.stdout, "[本地工具] task_stop %s (%s)\n", task.id, taskStatusValue(task))
	_ = appendAuditLog(config, "task_stop", map[string]any{"task_id": task.id, "status": result["status"]})
	return result, nil
}

func (m *localTaskManager) taskDelete(
	runtimeClient *ClientRuntime,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	if err := m.ensureLoaded(config); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取任务存储失败: %v", err)
	}
	task, err := m.lookupTask(config, arguments)
	if err != nil {
		return nil, err
	}
	result := taskSummary(task)
	status, _ := result["status"].(string)
	if status == "running" {
		return nil, newToolError("EXECUTION_FAILED", "运行中的任务不能删除，请先停止任务")
	}
	if err := m.deleteTask(task); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "删除任务失败: %v", err)
	}
	result["deleted"] = true
	runtimeClient.writeStreamEventf(runtimeClient.stdout, "[本地工具] task_delete %s (%s)\n", task.id, status)
	_ = appendAuditLog(config, "task_delete", map[string]any{"task_id": task.id, "status": status})
	return result, nil
}

func (m *localTaskManager) taskCleanup(
	runtimeClient *ClientRuntime,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	tasks, err := m.tasksForStore(config)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取任务存储失败: %v", err)
	}
	deletedTaskIDs := make([]string, 0, len(tasks))
	runningCount := 0
	for _, task := range tasks {
		task.mu.RLock()
		status := task.status
		taskID := task.id
		task.mu.RUnlock()
		if status == "running" {
			runningCount++
			continue
		}
		if err := m.deleteTask(task); err != nil {
			return nil, newToolError("EXECUTION_FAILED", "清理任务失败: %v", err)
		}
		deletedTaskIDs = append(deletedTaskIDs, taskID)
	}
	result := map[string]any{
		"deleted":          len(deletedTaskIDs) > 0,
		"deleted_count":    len(deletedTaskIDs),
		"deleted_task_ids": deletedTaskIDs,
		"running_count":    runningCount,
	}
	runtimeClient.writeStreamEventf(runtimeClient.stdout, "[本地工具] task_cleanup 删除 %d 个任务，保留 %d 个运行中任务\n", len(deletedTaskIDs), runningCount)
	_ = appendAuditLog(config, "task_cleanup", map[string]any{"deleted_count": len(deletedTaskIDs), "running_count": runningCount})
	return result, nil
}

func (m *localTaskManager) taskList(
	runtimeClient *ClientRuntime,
	config internalconfig.Config,
) map[string]any {
	tasks, err := m.tasksForStore(config)
	if err != nil {
		return map[string]any{"tasks": []map[string]any{}, "count": 0, "error": err.Error()}
	}
	items := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, taskSummary(task))
	}
	runtimeClient.writeStreamEventf(runtimeClient.stdout, "[本地工具] task_list %d 个任务\n", len(items))
	_ = appendAuditLog(config, "task_list", map[string]any{"count": len(items)})
	return map[string]any{"tasks": items, "count": len(items)}
}

func (m *localTaskManager) terminalCapture(
	runtimeClient *ClientRuntime,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	if err := m.ensureLoaded(config); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取任务存储失败: %v", err)
	}
	task, err := m.lookupTask(config, arguments)
	if err != nil {
		return nil, err
	}
	maxBytes := intValue(arguments["max_bytes"], maxReadBytesDefault)
	if maxBytes < 1 {
		maxBytes = maxReadBytesDefault
	}
	stream := strings.ToLower(strings.TrimSpace(fallbackString(stringValue(arguments["stream"]), "both")))
	stdout, stdoutTruncated := task.readPersistedOutput(false, maxBytes)
	stderr, stderrTruncated := task.readPersistedOutput(true, maxBytes)
	content := ""
	switch stream {
	case "stdout":
		content = stdout
	case "stderr":
		content = stderr
	case "both", "":
		content = strings.TrimSpace(stdout)
		trimmedStderr := strings.TrimSpace(stderr)
		if trimmedStderr != "" {
			if content != "" {
				content += "\n\n"
			}
			content += "stderr:\n" + trimmedStderr
		}
		stream = "both"
	default:
		return nil, newToolError("PARAM_ERROR", "stream 仅支持 stdout、stderr、both")
	}
	if strings.TrimSpace(content) == "" {
		content = "当前任务尚无终端输出。"
	}
	result := taskDetails(task, maxBytes)
	result["stream"] = stream
	result["content"] = content
	result["content_truncated"] = stdoutTruncated || stderrTruncated
	runtimeClient.writeStreamEventf(runtimeClient.stdout, "[本地工具] terminal_capture %s (%s)\n", task.id, taskStatusValue(task))
	_ = appendAuditLog(config, "terminal_capture", map[string]any{"task_id": task.id, "stream": stream, "status": result["status"]})
	return result, nil
}

func (m *localTaskManager) lookupTask(config internalconfig.Config, arguments map[string]any) (*localTask, error) {
	taskID := strings.TrimSpace(stringValue(arguments["task_id"]))
	if taskID == "" {
		return nil, newToolError("PARAM_ERROR", "task_id 不能为空")
	}
	storeDir, err := defaultTaskStoreDir()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "解析任务存储目录失败: %v", err)
	}
	m.mu.RLock()
	task, ok := m.tasks[taskID]
	m.mu.RUnlock()
	if !ok || task.storeDir != storeDir {
		return nil, newToolError("PARAM_ERROR", "未找到后台任务: %s", taskID)
	}
	return task, nil
}

func taskSummary(task *localTask) map[string]any {
	task.mu.RLock()
	defer task.mu.RUnlock()
	result := map[string]any{
		"task_id":      task.id,
		"task_name":    task.name,
		"command":      task.preview,
		"cwd":          task.cwd,
		"source_skill": task.sourceSkill,
		"flow_id":      task.flowID,
		"session_id":   task.sessionID,
		"run_id":       task.runID,
		"status":       task.status,
		"started_at":   task.startedAt.Format(time.RFC3339),
		"completed_at": "",
	}
	if len(task.injectedEnvKeys) > 0 {
		result["injected_env_keys"] = append([]string(nil), task.injectedEnvKeys...)
	}
	if !task.completedAt.IsZero() {
		result["completed_at"] = task.completedAt.Format(time.RFC3339)
	}
	if task.hasExitCode {
		result["exit_code"] = task.exitCode
	}
	if strings.TrimSpace(task.stdoutPath) != "" {
		result["stdout_path"] = task.stdoutPath
	}
	if strings.TrimSpace(task.stderrPath) != "" {
		result["stderr_path"] = task.stderrPath
	}
	if task.lastError != "" {
		result["error"] = task.lastError
	}
	return result
}

func taskDetails(task *localTask, maxBytes int) map[string]any {
	summary := taskSummary(task)
	task.mu.RLock()
	stdoutTruncated := task.stdoutTruncated
	stderrTruncated := task.stderrTruncated
	task.mu.RUnlock()
	if maxBytes < 1 {
		maxBytes = maxReadBytesDefault
	}
	if maxBytes > maxReadBytesHard {
		maxBytes = maxReadBytesHard
	}
	trimmedStdout, stdoutReturnedTruncated := task.readPersistedOutput(false, maxBytes)
	trimmedStderr, stderrReturnedTruncated := task.readPersistedOutput(true, maxBytes)
	summary["stdout"] = trimmedStdout
	summary["stderr"] = trimmedStderr
	summary["stdout_truncated"] = stdoutTruncated || stdoutReturnedTruncated
	summary["stderr_truncated"] = stderrTruncated || stderrReturnedTruncated
	return summary
}

func taskStatusValue(task *localTask) string {
	task.mu.RLock()
	defer task.mu.RUnlock()
	return task.status
}

func errorsIsContextCanceled(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(text, "context canceled") || strings.Contains(text, "signal: killed")
}
