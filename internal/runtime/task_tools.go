package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

const maxTaskOutputBytes = 128 * 1024

var sharedLocalTaskManager = newLocalTaskManager()

type localTaskManager struct {
	mu    sync.RWMutex
	tasks map[string]*localTask
}

type localTask struct {
	id         string
	name       string
	preview    string
	cwd        string
	startedAt  time.Time
	completedAt time.Time
	status     string
	exitCode   int
	hasExitCode bool
	stdout     bytes.Buffer
	stderr     bytes.Buffer
	stdoutTruncated bool
	stderrTruncated bool
	lastError  string
	cancel     context.CancelFunc
	mu         sync.RWMutex
}

type taskStreamWriter struct {
	task   *localTask
	stderr bool
}

func newLocalTaskManager() *localTaskManager {
	return &localTaskManager{tasks: map[string]*localTask{}}
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
	return sharedLocalTaskManager.startTask(r, config, arguments)
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

func (r *ClientRuntime) taskList(config internalconfig.Config) (map[string]any, error) {
	return sharedLocalTaskManager.taskList(r, config), nil
}

func (m *localTaskManager) startTask(
	runtimeClient *ClientRuntime,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
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

	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())
	task := &localTask{
		id:        taskID,
		name:      strings.TrimSpace(fallbackString(stringValue(arguments["task_name"]), taskID)),
		preview:   commandPreview(arguments),
		cwd:       payload.WorkingDirectory,
		startedAt: time.Now(),
		status:    "running",
		cancel:    cancel,
	}

	m.mu.Lock()
	m.tasks[taskID] = task
	m.mu.Unlock()

	_ = appendAuditLog(config, "task_start", map[string]any{"task_id": taskID, "command": task.preview, "cwd": task.cwd})
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
	defer task.mu.Unlock()
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
}

func (m *localTaskManager) taskOutput(
	runtimeClient *ClientRuntime,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	task, err := m.lookupTask(arguments)
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
	task, err := m.lookupTask(arguments)
	if err != nil {
		return nil, err
	}
	task.mu.RLock()
	status := task.status
	cancel := task.cancel
	task.mu.RUnlock()
	if status == "running" && cancel != nil {
		cancel()
	}
	result := taskDetails(task, maxReadBytesDefault)
	runtimeClient.writeStreamEventf(runtimeClient.stdout, "[本地工具] task_stop %s (%s)\n", task.id, taskStatusValue(task))
	_ = appendAuditLog(config, "task_stop", map[string]any{"task_id": task.id, "status": result["status"]})
	return result, nil
}

func (m *localTaskManager) taskList(
	runtimeClient *ClientRuntime,
	config internalconfig.Config,
) map[string]any {
	m.mu.RLock()
	tasks := make([]*localTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		tasks = append(tasks, task)
	}
	m.mu.RUnlock()
	sort.Slice(tasks, func(left, right int) bool {
		return tasks[left].startedAt.After(tasks[right].startedAt)
	})
	items := make([]map[string]any, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, taskSummary(task))
	}
	runtimeClient.writeStreamEventf(runtimeClient.stdout, "[本地工具] task_list %d 个任务\n", len(items))
	_ = appendAuditLog(config, "task_list", map[string]any{"count": len(items)})
	return map[string]any{"tasks": items, "count": len(items)}
}

func (m *localTaskManager) lookupTask(arguments map[string]any) (*localTask, error) {
	taskID := strings.TrimSpace(stringValue(arguments["task_id"]))
	if taskID == "" {
		return nil, newToolError("PARAM_ERROR", "task_id 不能为空")
	}
	m.mu.RLock()
	task, ok := m.tasks[taskID]
	m.mu.RUnlock()
	if !ok {
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
		"status":       task.status,
		"started_at":   task.startedAt.Format(time.RFC3339),
		"completed_at": "",
	}
	if !task.completedAt.IsZero() {
		result["completed_at"] = task.completedAt.Format(time.RFC3339)
	}
	if task.hasExitCode {
		result["exit_code"] = task.exitCode
	}
	if task.lastError != "" {
		result["error"] = task.lastError
	}
	return result
}

func taskDetails(task *localTask, maxBytes int) map[string]any {
	summary := taskSummary(task)
	task.mu.RLock()
	stdout := task.stdout.String()
	stderr := task.stderr.String()
	stdoutTruncated := task.stdoutTruncated
	stderrTruncated := task.stderrTruncated
	task.mu.RUnlock()
	if maxBytes < 1 {
		maxBytes = maxReadBytesDefault
	}
	if maxBytes > maxReadBytesHard {
		maxBytes = maxReadBytesHard
	}
	trimmedStdout, stdoutReturnedTruncated := truncateUTF8(stdout, maxBytes)
	trimmedStderr, stderrReturnedTruncated := truncateUTF8(stderr, maxBytes)
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