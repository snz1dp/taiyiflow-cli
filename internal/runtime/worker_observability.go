package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

const (
	workerLogFileName       = "worker.log"
	workerExecutionsDirName = "worker"
	workerExecutionsFile    = "executions.json"
	maxWorkerExecutions     = 200
	// RISK-2 修复：日志文件超过此大小时执行轮转（默认 10MB）。
	maxWorkerLogBytes = 10 * 1024 * 1024
)

// workerExecutionRecord 本地执行记录。
type workerExecutionRecord struct {
	ID           string `json:"id"`
	TaskType     string `json:"task_type"`
	TaskID       string `json:"task_id,omitempty"`
	ScheduleID   string `json:"schedule_id,omitempty"`
	RunRecordID  string `json:"run_record_id,omitempty"`
	Name         string `json:"name,omitempty"`
	RequestMode  string `json:"request_mode,omitempty"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error_message,omitempty"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at,omitempty"`
	DurationMS   int64  `json:"duration_ms,omitempty"`
	WorkingDir   string `json:"working_directory,omitempty"`
}

// workerObservability 提供 Worker 的结构化日志和本地执行记录。
type workerObservability struct {
	mu         sync.Mutex
	logPath    string
	execPath   string
	executions []workerExecutionRecord
	loaded     bool
	warnedIO   bool
	logf       func(format string, args ...any)
}

// newWorkerObservability 创建 Worker 可观测性实例。
func newWorkerObservability(logf func(format string, args ...any)) *workerObservability {
	rootDir, err := internalconfig.DefaultRootDir()
	if err != nil {
		return &workerObservability{logf: logf}
	}
	return &workerObservability{
		logPath:  filepath.Join(rootDir, "logs", workerLogFileName),
		execPath: filepath.Join(rootDir, workerExecutionsDirName, workerExecutionsFile),
		logf:     logf,
	}
}

// warnIOError 在首次磁盘 IO 失败时输出警告，避免完全静默。
func (o *workerObservability) warnIOError(operation string, err error) {
	if o.warnedIO || o.logf == nil {
		return
	}
	o.warnedIO = true
	o.logf("Worker 可观测性磁盘写入警告（后续同类错误不再重复提示）: %s: %v", operation, err)
}

// logEvent 写入结构化日志行。
// RISK-2 修复：写入前检查文件大小，超过 maxWorkerLogBytes 时轮转（重命名为 .old 后重建）。
func (o *workerObservability) logEvent(eventType string, fields map[string]any) {
	if o == nil || o.logPath == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	record := map[string]any{
		"timestamp": time.Now().Format(time.RFC3339),
		"event":     eventType,
	}
	for k, v := range fields {
		record[k] = v
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(o.logPath), 0o755); err != nil {
		o.warnIOError("创建日志目录", err)
		return
	}
	// 日志轮转：超过阈值时将当前日志重命名为 .old（覆盖旧轮转文件）
	if info, statErr := os.Stat(o.logPath); statErr == nil && info.Size() > maxWorkerLogBytes {
		rotatedPath := o.logPath + ".old"
		_ = os.Remove(rotatedPath)
		if renameErr := os.Rename(o.logPath, rotatedPath); renameErr != nil {
			o.warnIOError("轮转日志文件", renameErr)
		}
	}
	file, err := os.OpenFile(o.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		o.warnIOError("打开日志文件", err)
		return
	}
	defer file.Close()
	if _, err := file.WriteString(string(encoded) + "\n"); err != nil {
		o.warnIOError("写入日志文件", err)
	}
}

// recordExecution 记录一次任务执行。
func (o *workerObservability) recordExecution(record workerExecutionRecord) {
	if o == nil || o.execPath == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	if !o.loaded {
		o.loadExecutions()
		o.loaded = true
	}
	o.executions = append(o.executions, record)
	// 保留最近 N 条
	if len(o.executions) > maxWorkerExecutions {
		o.executions = o.executions[len(o.executions)-maxWorkerExecutions:]
	}
	o.saveExecutions()
}

// loadExecutions 从磁盘加载执行记录。
func (o *workerObservability) loadExecutions() {
	data, err := os.ReadFile(o.execPath)
	if err != nil {
		o.executions = nil
		return
	}
	var records []workerExecutionRecord
	if err := json.Unmarshal(data, &records); err != nil {
		o.executions = nil
		return
	}
	o.executions = records
}

// saveExecutions 将执行记录写入磁盘。
// RISK-3 修复：使用临时文件 + rename 的原子写入模式，
// 避免进程崩溃时直接覆盖导致 executions.json 损坏。
func (o *workerObservability) saveExecutions() {
	if err := os.MkdirAll(filepath.Dir(o.execPath), 0o755); err != nil {
		o.warnIOError("创建执行记录目录", err)
		return
	}
	data, err := json.MarshalIndent(o.executions, "", "  ")
	if err != nil {
		return
	}
	tmpPath := o.execPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o644); err != nil {
		o.warnIOError("写入执行记录临时文件", err)
		return
	}
	if err := os.Rename(tmpPath, o.execPath); err != nil {
		o.warnIOError("重命名执行记录文件", err)
		_ = os.Remove(tmpPath)
	}
}

// recentExecutions 返回最近 N 条执行记录（最新在前）。
func (o *workerObservability) recentExecutions(limit int) []workerExecutionRecord {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.loaded {
		o.loadExecutions()
		o.loaded = true
	}
	if limit <= 0 || limit > len(o.executions) {
		limit = len(o.executions)
	}
	result := make([]workerExecutionRecord, 0, limit)
	for i := len(o.executions) - 1; i >= len(o.executions)-limit; i-- {
		result = append(result, o.executions[i])
	}
	return result
}

// logConnection 记录连接事件。
func (o *workerObservability) logConnection(event string, clientID, deviceID string) {
	o.logEvent("connection."+event, map[string]any{
		"client_id": clientID,
		"device_id": deviceID,
	})
}

// logTaskReceived 记录任务接收。
func (o *workerObservability) logTaskReceived(taskType, taskID, name string) {
	o.logEvent("task.received", map[string]any{
		"task_type": taskType,
		"task_id":   taskID,
		"name":      name,
	})
}

// logTaskCompleted 记录任务完成。
func (o *workerObservability) logTaskCompleted(taskType, taskID, status string, durationMS int64) {
	o.logEvent("task.completed", map[string]any{
		"task_type":   taskType,
		"task_id":     taskID,
		"status":      status,
		"duration_ms": durationMS,
	})
}

// executionStartTime 返回当前时间，作为执行记录的起始时间戳。
func executionStartTime() time.Time {
	return time.Now()
}

// finishExecution 完成执行记录并写入本地存储。
func (o *workerObservability) finishExecution(
	startedAt time.Time,
	taskType string,
	record workerExecutionRecord,
	err error,
) {
	record.StartedAt = startedAt.Format(time.RFC3339)
	record.FinishedAt = time.Now().Format(time.RFC3339)
	record.DurationMS = time.Since(startedAt).Milliseconds()
	if err != nil {
		record.Status = "failed"
		record.ErrorMessage = err.Error()
	} else {
		record.Status = "success"
	}
	o.recordExecution(record)
	o.logTaskCompleted(taskType, record.TaskID, record.Status, record.DurationMS)
}

// workerLogPath 返回 Worker 日志文件路径（供外部查询）。
func workerLogPath() (string, error) {
	rootDir, err := internalconfig.DefaultRootDir()
	if err != nil {
		return "", fmt.Errorf("获取根目录失败: %w", err)
	}
	return filepath.Join(rootDir, "logs", workerLogFileName), nil
}

// workerExecutionsPath 返回 Worker 执行记录文件路径（供外部查询）。
func workerExecutionsPath() (string, error) {
	rootDir, err := internalconfig.DefaultRootDir()
	if err != nil {
		return "", fmt.Errorf("获取根目录失败: %w", err)
	}
	return filepath.Join(rootDir, workerExecutionsDirName, workerExecutionsFile), nil
}
