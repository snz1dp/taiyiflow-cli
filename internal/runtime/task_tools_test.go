package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
)

func TestShellExecBackgroundAndTaskOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.shellExec(context.Background(), config, map[string]any{
		"raw_command": "printf 'hello from task\\n'",
		"background":  true,
		"task_name":   "hello-task",
	})
	if err != nil {
		t.Fatalf("shellExec background failed: %v", err)
	}
	taskID := result["task_id"].(string)

	var output map[string]any
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, err = runtimeClient.taskOutput(config, map[string]any{"task_id": taskID})
		if err != nil {
			t.Fatalf("taskOutput failed: %v", err)
		}
		if output["status"].(string) != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if output["status"].(string) != "completed" {
		t.Fatalf("expected completed task, got %#v", output)
	}
	if !strings.Contains(output["stdout"].(string), "hello from task") {
		t.Fatalf("expected stdout content, got %#v", output)
	}

	listed, err := runtimeClient.taskList(config)
	if err != nil {
		t.Fatalf("taskList failed: %v", err)
	}
	if listed["count"].(int) < 1 {
		t.Fatalf("expected task list to include background task, got %#v", listed)
	}
}

func TestTaskStopCancelsBackgroundCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.shellExec(context.Background(), config, map[string]any{
		"raw_command": "while true; do echo tick; sleep 1; done",
		"background":  true,
		"task_name":   "loop-task",
	})
	if err != nil {
		t.Fatalf("shellExec background failed: %v", err)
	}
	taskID := result["task_id"].(string)

	if _, err := runtimeClient.taskStop(config, map[string]any{"task_id": taskID}); err != nil {
		t.Fatalf("taskStop failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, err := runtimeClient.taskOutput(config, map[string]any{"task_id": taskID})
		if err != nil {
			t.Fatalf("taskOutput failed: %v", err)
		}
		if output["status"].(string) == "cancelled" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected task to become cancelled")
}

func TestTaskManagerRestoresPersistedTaskAcrossManagerInstances(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto", SandboxEscapePolicy: internalconfig.SandboxEscapePolicyAllow}
	manager := newLocalTaskManager()

	result, err := manager.startTask(runtimeClient, configValue, map[string]any{
		"raw_command": "printf 'persisted task\\n'",
		"background":  true,
		"task_name":   "persisted-task",
		"flow_id":     "demo-flow",
		"session_id":  "session-1",
		"run_id":      "run-1",
	})
	if err != nil {
		t.Fatalf("startTask failed: %v", err)
	}
	taskID := result["task_id"].(string)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, outputErr := manager.taskOutput(runtimeClient, configValue, map[string]any{"task_id": taskID})
		if outputErr != nil {
			t.Fatalf("taskOutput failed: %v", outputErr)
		}
		if output["status"].(string) != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	restoredManager := newLocalTaskManager()
	listed := restoredManager.taskList(runtimeClient, configValue)
	if listed["count"].(int) != 1 {
		t.Fatalf("expected one restored task, got %#v", listed)
	}
	tasks := listed["tasks"].([]map[string]any)
	if tasks[0]["session_id"] != "session-1" {
		t.Fatalf("expected restored session_id, got %#v", tasks[0])
	}
	expectedWorkdir, pathErr := filepath.EvalSymlinks(configValue.Workdir)
	if pathErr != nil {
		expectedWorkdir = configValue.Workdir
	}
	if tasks[0]["cwd"] != expectedWorkdir {
		t.Fatalf("expected restored workdir, got %#v", tasks[0])
	}
	output, err := restoredManager.taskOutput(runtimeClient, configValue, map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("restored taskOutput failed: %v", err)
	}
	if !strings.Contains(output["stdout"].(string), "persisted task") {
		t.Fatalf("expected restored stdout, got %#v", output)
	}
	if strings.TrimSpace(stringValue(output["stdout_path"])) == "" {
		t.Fatalf("expected stdout_path in restored output, got %#v", output)
	}
}

func TestExecuteClientToolPayloadShellExecPersistsContextIdentifiers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto", SandboxEscapePolicy: internalconfig.SandboxEscapePolicyAllow}

	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), configValue, map[string]any{
		"flow_id": "demo-flow",
		"context": map[string]any{"session_id": "session-ctx", "run_id": "run-ctx"},
		"server":  map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "shell_exec",
			"arguments": map[string]any{
				"raw_command": "printf 'ctx task\\n'",
				"background":  true,
				"task_name":   "ctx-task",
			},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteClientToolPayload failed: %v", err)
	}
	if toolName != "shell_exec" {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	result := resultAny.(map[string]any)
	taskID := result["task_id"].(string)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, outputErr := runtimeClient.taskOutput(configValue, map[string]any{"task_id": taskID})
		if outputErr != nil {
			t.Fatalf("taskOutput failed: %v", outputErr)
		}
		if output["status"].(string) != "running" {
			if output["flow_id"] != "demo-flow" || output["session_id"] != "session-ctx" || output["run_id"] != "run-ctx" {
				t.Fatalf("expected context identifiers in task output, got %#v", output)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected background task to finish and preserve context identifiers")
}

func TestExecuteClientToolPayloadShellExecDefaultsToClientContextDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto", SandboxEscapePolicy: internalconfig.SandboxEscapePolicyAllow}
	contextDir := t.TempDir()

	_, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), configValue, map[string]any{
		"flow_id":            "demo-flow",
		"client_context_dir": contextDir,
		"server":             map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "shell_exec",
			"arguments": map[string]any{
				"raw_command": "pwd",
			},
		},
	})
	if err != nil {
		t.Fatalf("ExecuteClientToolPayload failed: %v", err)
	}
	result := resultAny.(map[string]any)
	resolvedContextDir, pathErr := filepath.EvalSymlinks(contextDir)
	if pathErr != nil {
		resolvedContextDir = contextDir
	}
	if strings.TrimSpace(stringValue(result["stdout"])) != resolvedContextDir {
		t.Fatalf("expected stdout cwd %q, got %#v", resolvedContextDir, result)
	}
	if result["cwd"] != resolvedContextDir {
		t.Fatalf("expected result cwd %q, got %#v", resolvedContextDir, result)
	}
}

func TestTerminalCaptureReturnsCombinedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"raw_command": "printf 'capture out\\n'; printf 'capture err\\n' 1>&2",
		"background":  true,
		"task_name":   "capture-task",
	})
	if err != nil {
		t.Fatalf("shellExec background failed: %v", err)
	}
	taskID := result["task_id"].(string)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		capture, captureErr := runtimeClient.terminalCapture(configValue, map[string]any{"task_id": taskID})
		if captureErr != nil {
			t.Fatalf("terminalCapture failed: %v", captureErr)
		}
		content := stringValue(capture["content"])
		if strings.Contains(content, "capture out") && strings.Contains(content, "capture err") {
			if capture["stream"] != "both" {
				t.Fatalf("expected both stream, got %#v", capture)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected terminal_capture to include stdout and stderr")
}

func TestTaskDeleteRemovesPersistedFilesAndListEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"raw_command": "printf 'delete task\\n'",
		"background":  true,
		"task_name":   "delete-task",
	})
	if err != nil {
		t.Fatalf("shellExec background failed: %v", err)
	}
	taskID := result["task_id"].(string)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, outputErr := runtimeClient.taskOutput(configValue, map[string]any{"task_id": taskID})
		if outputErr != nil {
			t.Fatalf("taskOutput failed: %v", outputErr)
		}
		if output["status"] != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	deleted, err := runtimeClient.taskDelete(configValue, map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("taskDelete failed: %v", err)
	}
	if deleted["deleted"] != true {
		t.Fatalf("expected deleted flag, got %#v", deleted)
	}
	if _, err := runtimeClient.taskOutput(configValue, map[string]any{"task_id": taskID}); err == nil {
		t.Fatal("expected deleted task to be unavailable")
	}
	listResult, err := runtimeClient.taskList(configValue)
	if err != nil {
		t.Fatalf("taskList failed: %v", err)
	}
	if listResult["count"].(int) != 0 {
		t.Fatalf("expected deleted task to disappear from list, got %#v", listResult)
	}
}

func TestTaskCleanupRemovesCompletedTasksAndKeepsRunningTasks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	completedResult, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"raw_command": "printf 'done task\\n'",
		"background":  true,
		"task_name":   "done-task",
	})
	if err != nil {
		t.Fatalf("start completed task failed: %v", err)
	}
	runningResult, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"raw_command": "while true; do echo keep; sleep 1; done",
		"background":  true,
		"task_name":   "keep-task",
	})
	if err != nil {
		t.Fatalf("start running task failed: %v", err)
	}
	runningTaskID := runningResult["task_id"].(string)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, outputErr := runtimeClient.taskOutput(configValue, map[string]any{"task_id": completedResult["task_id"].(string)})
		if outputErr != nil {
			t.Fatalf("taskOutput failed: %v", outputErr)
		}
		if output["status"] != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cleaned, err := runtimeClient.taskCleanup(configValue, map[string]any{})
	if err != nil {
		t.Fatalf("taskCleanup failed: %v", err)
	}
	if cleaned["deleted_count"] != 1 {
		t.Fatalf("expected one deleted task, got %#v", cleaned)
	}
	if cleaned["running_count"] != 1 {
		t.Fatalf("expected one running task kept, got %#v", cleaned)
	}
	listResult, err := runtimeClient.taskList(configValue)
	if err != nil {
		t.Fatalf("taskList failed: %v", err)
	}
	if listResult["count"].(int) != 1 {
		t.Fatalf("expected one task after cleanup, got %#v", listResult)
	}
	if _, err := runtimeClient.taskStop(configValue, map[string]any{"task_id": runningTaskID}); err != nil {
		t.Fatalf("taskStop failed: %v", err)
	}
}

func TestSkillReturnsInvocationPayload(t *testing.T) {
	workdir := t.TempDir()
	skillDir := t.TempDir()
	if err := os.MkdirAll(skillDir+"/reviewer", 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	content := "---\nname: Code Review\ndescription: 审查变更\ncategory: engineering\n---\n\n检查回归风险。\n"
	if err := os.WriteFile(skillDir+"/reviewer/SKILL.md", []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}
	configValue.Skills.Directory = skillDir

	if _, err := runtimeClient.skill(configValue, map[string]any{}); err == nil {
		t.Fatal("expected Skill to require name")
	}

	result, err := runtimeClient.skill(configValue, map[string]any{"name": "Code Review", "input": "检查 cmd/root.go"})
	if err != nil {
		t.Fatalf("skill failed: %v", err)
	}
	if result["name"].(string) != "Code Review" {
		t.Fatalf("unexpected skill result: %#v", result)
	}
	if result["input"].(string) != "检查 cmd/root.go" {
		t.Fatalf("unexpected skill input: %#v", result)
	}
	if !strings.Contains(result["prompt"].(string), "检查回归风险") {
		t.Fatalf("unexpected skill prompt: %#v", result)
	}
	if !strings.Contains(result["markdown"].(string), "name: Code Review") {
		t.Fatalf("unexpected skill markdown: %#v", result)
	}
	if result["root_dir"].(string) != skillDir {
		t.Fatalf("unexpected skill root_dir: %#v", result)
	}
	if result["relative_dir"].(string) != "reviewer" {
		t.Fatalf("unexpected skill relative_dir: %#v", result)
	}
}

func TestBundledSkillReturnsExtractedFiles(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.Setenv(config.RootDirEnvName(), rootDir); err != nil {
		t.Fatalf("set env failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv(config.RootDirEnvName())
	})
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{ApprovalMode: "auto"}

	result, err := runtimeClient.skill(configValue, map[string]any{"name": "taiyiflowApi"})
	if err != nil {
		t.Fatalf("bundled skill failed: %v", err)
	}
	if result["source"].(string) != "bundled" {
		t.Fatalf("unexpected skill source: %#v", result)
	}
	rootPath := result["root_dir"].(string)
	if strings.TrimSpace(rootPath) == "" || rootPath == "bundled" {
		t.Fatalf("expected extracted bundled root dir: %#v", result)
	}
	files, ok := result["files"].([]skills.SkillFile)
	if !ok || len(files) != 1 {
		t.Fatalf("unexpected bundled files payload: %#v", result["files"])
	}
	if _, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(files[0].Path))); err != nil {
		t.Fatalf("expected extracted bundled file: %v", err)
	}
}

func TestGlobFilesFileEditAndTodo(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.Setenv(config.RootDirEnvName(), rootDir); err != nil {
		t.Fatalf("set env failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv(config.RootDirEnvName())
	})
	workdir := t.TempDir()
	if err := os.MkdirAll(workdir+"/cmd", 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	filePath := workdir + "/cmd/main.go"
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}

	globResult, err := runtimeClient.globFiles(configValue, map[string]any{"pattern": "cmd/*.go"})
	if err != nil {
		t.Fatalf("globFiles failed: %v", err)
	}
	if globResult["count"].(int) != 1 {
		t.Fatalf("unexpected glob result: %#v", globResult)
	}

	editResult, err := runtimeClient.fileEdit(configValue, map[string]any{
		"path":       "cmd/main.go",
		"old_string": "hello",
		"new_string": "taiyiflow",
	})
	if err != nil {
		t.Fatalf("fileEdit failed: %v", err)
	}
	if editResult["replacements"].(int) != 1 {
		t.Fatalf("unexpected file edit result: %#v", editResult)
	}
	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if !strings.Contains(string(content), "taiyiflow") {
		t.Fatalf("expected file edit to update content, got %q", string(content))
	}

	payload := map[string]any{"flow_id": "demo-flow", "context": map[string]any{"session_id": "session-1"}}
	todoResult, err := runtimeClient.todoWrite(configValue, payload, map[string]any{"items": []any{map[string]any{"title": "补齐 file_edit", "status": "in_progress"}}})
	if err != nil {
		t.Fatalf("todoWrite failed: %v", err)
	}
	if todoResult["count"].(int) != 1 {
		t.Fatalf("unexpected todo result: %#v", todoResult)
	}
}

func TestFileEditReturnsDiagnosticsWhenOldStringNotFound(t *testing.T) {
	workdir := t.TempDir()
	filePath := workdir + "/main.go"
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}

	result, err := runtimeClient.fileEdit(configValue, map[string]any{
		"path":       "main.go",
		"old_string": "println(\"missing\")",
		"new_string": "println(\"updated\")",
	})
	if err == nil {
		t.Fatal("expected fileEdit to fail when old_string is missing")
	}
	if !strings.Contains(err.Error(), "建议先 read_file") || !strings.Contains(err.Error(), "行 ") {
		t.Fatalf("expected error text to include suggested read_file range, got %v", err)
	}
	if result["status"].(string) != "not_found" {
		t.Fatalf("expected not_found diagnostic, got %#v", result)
	}
	if result["sha256_before"].(string) == "" {
		t.Fatalf("expected sha256_before in diagnostic result, got %#v", result)
	}
	if suggestions, ok := result["suggestions"].([]string); !ok || len(suggestions) == 0 {
		t.Fatalf("expected suggestions in diagnostic result, got %#v", result)
	}
	if excerpt := strings.TrimSpace(stringValue(result["nearby_excerpt"])); excerpt == "" {
		t.Fatalf("expected nearby excerpt in diagnostic result, got %#v", result)
	}
	if result["suggested_read_start_line"].(int) < 1 || result["suggested_read_end_line"].(int) < result["suggested_read_start_line"].(int) {
		t.Fatalf("expected suggested read lines in diagnostic result, got %#v", result)
	}
	prefetchedRead, ok := result["prefetched_read"].(map[string]any)
	if !ok {
		t.Fatalf("expected prefetched_read payload in diagnostic result, got %#v", result)
	}
	if strings.TrimSpace(stringValue(prefetchedRead["content"])) == "" {
		t.Fatalf("expected prefetched_read content, got %#v", prefetchedRead)
	}
	if intValue(prefetchedRead["start_line"], 0) != result["suggested_read_start_line"].(int) || intValue(prefetchedRead["end_line"], 0) != result["suggested_read_end_line"].(int) {
		t.Fatalf("expected prefetched_read lines to match suggested range, got %#v result=%#v", prefetchedRead, result)
	}
	retryHint, ok := result["retry_hint"].(map[string]any)
	if !ok {
		t.Fatalf("expected retry_hint payload in diagnostic result, got %#v", result)
	}
	if retryHint["tool_name"] != "file_edit" {
		t.Fatalf("expected file_edit retry tool, got %#v", retryHint)
	}
	recommendedArgs, ok := retryHint["recommended_args"].(map[string]any)
	if !ok {
		t.Fatalf("expected recommended_args in retry_hint, got %#v", retryHint)
	}
	if strings.TrimSpace(stringValue(recommendedArgs["old_string"])) == "" || recommendedArgs["expected_sha256_before"] != result["sha256_before"] {
		t.Fatalf("expected reusable retry arguments, got %#v result=%#v", retryHint, result)
	}
}

func TestLSPQueryWithHelperServer(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		return
	}
	if err := os.Setenv("GO_WANT_HELPER_PROCESS", "1"); err != nil {
		t.Fatalf("set helper env failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv("GO_WANT_HELPER_PROCESS")
	})
	workdir := t.TempDir()
	filePath := workdir + "/main.go"
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}

	definitionResult, err := runtimeClient.lspQuery(configValue, map[string]any{
		"server_command": os.Args[0],
		"server_args":    []any{"-test.run=TestHelperLSPServer", "--"},
		"operation":      "definition",
		"path":           "main.go",
		"line":           3,
		"character":      6,
	})
	if err != nil {
		t.Fatalf("lspQuery definition failed: %v", err)
	}
	if definitionResult["count"].(int) != 1 {
		t.Fatalf("unexpected definition result: %#v", definitionResult)
	}

	workspaceResult, err := runtimeClient.lspQuery(configValue, map[string]any{
		"server_command": os.Args[0],
		"server_args":    []any{"-test.run=TestHelperLSPServer", "--"},
		"operation":      "workspace_symbol",
		"symbol_query":   "main",
	})
	if err != nil {
		t.Fatalf("lspQuery workspace_symbol failed: %v", err)
	}
	if workspaceResult["count"].(int) != 1 {
		t.Fatalf("unexpected workspace symbol result: %#v", workspaceResult)
	}
}

func TestHelperLSPServer(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	writer := os.Stdout
	openedURI := ""
	for {
		body, err := readLSPMessage(reader)
		if err != nil {
			os.Exit(0)
		}
		var request map[string]any
		if err := json.Unmarshal(body, &request); err != nil {
			os.Exit(2)
		}
		method := stringValue(request["method"])
		switch method {
		case "initialize":
			respondTestLSP(writer, intValue(request["id"], 0), map[string]any{"capabilities": map[string]any{}})
		case "initialized":
		case "textDocument/didOpen":
			params, _ := request["params"].(map[string]any)
			textDocument, _ := params["textDocument"].(map[string]any)
			openedURI = stringValue(textDocument["uri"])
		case "textDocument/definition":
			respondTestLSP(writer, intValue(request["id"], 0), []map[string]any{{
				"uri": openedURI,
				"range": map[string]any{
					"start": map[string]any{"line": 2, "character": 5},
					"end":   map[string]any{"line": 2, "character": 9},
				},
			}})
		case "workspace/symbol":
			respondTestLSP(writer, intValue(request["id"], 0), []map[string]any{{
				"name": "main",
				"kind": 12,
				"location": map[string]any{
					"uri": openedURI,
					"range": map[string]any{
						"start": map[string]any{"line": 2, "character": 5},
						"end":   map[string]any{"line": 2, "character": 9},
					},
				},
			}})
		default:
			respondTestLSP(writer, intValue(request["id"], 0), []map[string]any{})
		}
	}
}

func respondTestLSP(writer io.Writer, id int, result any) {
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_, _ = io.WriteString(writer, "Content-Length: ")
	_, _ = io.WriteString(writer, stringValue(len(body)))
	_, _ = io.WriteString(writer, "\r\n\r\n")
	_, _ = writer.Write(body)
}
