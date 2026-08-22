package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func newTestObservability(t *testing.T) *workerObservability {
	t.Helper()
	dir := t.TempDir()
	return &workerObservability{
		logPath:  filepath.Join(dir, "logs", "worker.log"),
		execPath: filepath.Join(dir, "worker", "executions.json"),
		logf:     func(format string, args ...any) {},
	}
}

// --- recordExecution 截断测试 ---

func TestRecordExecutionTruncatesToMaxWorkerExecutions(t *testing.T) {
	o := newTestObservability(t)

	// 写入超过上限的记录
	for i := 0; i < maxWorkerExecutions+50; i++ {
		o.recordExecution(workerExecutionRecord{
			ID:     "exec-" + string(rune('a'+i%26)),
			Status: "success",
		})
	}

	if len(o.executions) != maxWorkerExecutions {
		t.Fatalf("expected %d executions, got %d", maxWorkerExecutions, len(o.executions))
	}
	// 验证保留的是最新的记录（尾部）
	last := o.executions[len(o.executions)-1]
	if last.ID == "" {
		t.Fatal("expected last execution to have an ID")
	}
}

// --- saveExecutions 原子写入测试 ---

func TestSaveExecutionsAtomicWrite(t *testing.T) {
	o := newTestObservability(t)

	o.recordExecution(workerExecutionRecord{ID: "exec-1", Status: "success"})

	// 验证文件存在且内容有效
	data, err := os.ReadFile(o.execPath)
	if err != nil {
		t.Fatalf("read executions file failed: %v", err)
	}
	var records []workerExecutionRecord
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatalf("unmarshal executions failed: %v", err)
	}
	if len(records) != 1 || records[0].ID != "exec-1" {
		t.Fatalf("unexpected records: %#v", records)
	}

	// 验证没有残留的 .tmp 文件
	tmpPath := o.execPath + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("expected .tmp file to be cleaned up, got err=%v", err)
	}
}

// --- recentExecutions 边界条件测试 ---

func TestRecentExecutionsEmptyList(t *testing.T) {
	o := newTestObservability(t)

	result := o.recentExecutions(10)
	if len(result) != 0 {
		t.Fatalf("expected empty result, got %d items", len(result))
	}
}

func TestRecentExecutionsLimitZero(t *testing.T) {
	o := newTestObservability(t)
	o.recordExecution(workerExecutionRecord{ID: "exec-1", Status: "success"})

	result := o.recentExecutions(0)
	// limit <= 0 时应返回全部
	if len(result) != 1 {
		t.Fatalf("expected 1 item for limit=0, got %d", len(result))
	}
}

func TestRecentExecutionsReturnsNewestFirst(t *testing.T) {
	o := newTestObservability(t)
	o.recordExecution(workerExecutionRecord{ID: "exec-1", Status: "success"})
	o.recordExecution(workerExecutionRecord{ID: "exec-2", Status: "failed"})
	o.recordExecution(workerExecutionRecord{ID: "exec-3", Status: "success"})

	result := o.recentExecutions(2)
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result))
	}
	if result[0].ID != "exec-3" || result[1].ID != "exec-2" {
		t.Fatalf("expected newest first, got %v, %v", result[0].ID, result[1].ID)
	}
}

func TestRecentExecutionsLimitExceedsTotal(t *testing.T) {
	o := newTestObservability(t)
	o.recordExecution(workerExecutionRecord{ID: "exec-1", Status: "success"})

	result := o.recentExecutions(100)
	if len(result) != 1 {
		t.Fatalf("expected 1 item, got %d", len(result))
	}
}

// --- loadExecutions 测试 ---

func TestLoadExecutionsFromDisk(t *testing.T) {
	o := newTestObservability(t)

	// 先写入
	o.recordExecution(workerExecutionRecord{ID: "exec-1", Status: "success"})

	// 创建新实例，验证从磁盘加载
	o2 := &workerObservability{
		execPath: o.execPath,
		logf:     func(format string, args ...any) {},
	}
	result := o2.recentExecutions(10)
	if len(result) != 1 || result[0].ID != "exec-1" {
		t.Fatalf("expected loaded execution from disk, got %#v", result)
	}
}

func TestLoadExecutionsCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, "executions.json")
	os.WriteFile(execPath, []byte("not valid json"), 0o644)

	o := &workerObservability{
		execPath: execPath,
		logf:     func(format string, args ...any) {},
	}
	result := o.recentExecutions(10)
	if len(result) != 0 {
		t.Fatalf("expected empty result for corrupted file, got %d", len(result))
	}
}

// --- logEvent 测试 ---

func TestLogEventWritesToFile(t *testing.T) {
	o := newTestObservability(t)

	o.logEvent("test.event", map[string]any{"key": "value"})

	data, err := os.ReadFile(o.logPath)
	if err != nil {
		t.Fatalf("read log file failed: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("unmarshal log line failed: %v", err)
	}
	if record["event"] != "test.event" {
		t.Fatalf("unexpected event: %#v", record)
	}
	if record["key"] != "value" {
		t.Fatalf("unexpected field: %#v", record)
	}
}

func TestLogEventNilReceiver(t *testing.T) {
	var o *workerObservability
	// 不应 panic
	o.logEvent("test", nil)
}

// --- executionStartTime 测试 ---

func TestExecutionStartTimeReturnsNonZero(t *testing.T) {
	ts := executionStartTime()
	if ts.IsZero() {
		t.Fatal("expected non-zero time")
	}
}
