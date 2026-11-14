package runtime

import (
	"bufio"
	"bytes"
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
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.shellExec(context.Background(), config, map[string]any{
		"raw_command": "printf 'hello from task\\n'; sleep 0.5",
		"background":  true,
		"task_name":   "hello-task",
	})
	if err != nil {
		t.Fatalf("shellExec background failed: %v", err)
	}
	taskID := result["task_id"].(string)

	seenStdout := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, outputErr := runtimeClient.taskOutput(config, map[string]any{"task_id": taskID})
		if outputErr != nil {
			if seenStdout {
				break
			}
			t.Fatalf("taskOutput failed before auto cleanup: %v", outputErr)
		}
		if strings.Contains(stringValue(output["stdout"]), "hello from task") {
			seenStdout = true
		}
		if seenStdout && stringValue(output["status"]) != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !seenStdout {
		t.Fatal("expected taskOutput to expose running task stdout before auto cleanup")
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, outputErr := runtimeClient.taskOutput(config, map[string]any{"task_id": taskID})
		if outputErr != nil {
			if seenStdout {
				break
			}
			t.Fatalf("taskOutput failed before completion: %v", outputErr)
		}
		if strings.Contains(stringValue(output["stdout"]), "hello from task") {
			seenStdout = true
		}
		if seenStdout && stringValue(output["status"]) != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !seenStdout {
		t.Fatal("expected taskOutput to expose running task stdout before completion")
	}

	// 任务完成后仍应可查询（不再自动清理）
	finalOutput, err := runtimeClient.taskOutput(config, map[string]any{"task_id": taskID})
	if err != nil {
		t.Fatalf("expected completed task to remain queryable, got error: %v", err)
	}
	if stringValue(finalOutput["status"]) == "running" {
		t.Fatal("expected task to have completed")
	}

	listed, err := runtimeClient.taskList(config)
	if err != nil {
		t.Fatalf("taskList failed: %v", err)
	}
	if listed["count"].(int) != 1 {
		t.Fatalf("expected completed task to remain in list, got count %v", listed["count"])
	}
}

func TestNormalizeCommandArgumentsUsesConfigCommandTimeout(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), CommandTimeoutSeconds: 45}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"raw_command": "printf 'hello'",
	}, configValue)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Timeout != 45 {
		t.Fatalf("expected timeout 45, got %#v", payload.Timeout)
	}
}

func TestNormalizeCommandArgumentsSplitsSimpleCommandField(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir()}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"command": `git status --short`,
	}, configValue)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Mode != "exec" {
		t.Fatalf("expected exec mode, got %#v", payload)
	}
	if payload.Command != "git" {
		t.Fatalf("expected command git, got %#v", payload)
	}
	if len(payload.Args) != 2 || payload.Args[0] != "status" || payload.Args[1] != "--short" {
		t.Fatalf("unexpected args: %#v", payload.Args)
	}
}

func TestNormalizeCommandArgumentsKeepsShellSyntaxAsRawCommand(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir()}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"command": `git status | cat`,
	}, configValue)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Mode != "shell" {
		t.Fatalf("expected shell mode, got %#v", payload)
	}
	if payload.RawCommand != `git status | cat` {
		t.Fatalf("unexpected raw command: %#v", payload)
	}
}

func TestNormalizeStructuredCommandArgsDegradesToShellForPipeInArgs(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir()}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"command": "git",
		"args":    []any{"status", "|", "cat"},
	}, configValue)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Mode != "shell" {
		t.Fatalf("expected shell mode for pipe in args, got %#v", payload)
	}
	if payload.Command != "" || payload.Args != nil {
		t.Fatalf("expected exec fields cleared, got command=%q args=%#v", payload.Command, payload.Args)
	}
}

func TestNormalizeStructuredCommandArgsDegradesToShellForRedirectionInArgs(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir()}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"command": "echo",
		"args":    []any{"hello", ">", "output.txt"},
	}, configValue)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Mode != "shell" {
		t.Fatalf("expected shell mode for redirection in args, got %#v", payload)
	}
}

func TestNormalizeStructuredCommandArgsDegradesToShellForLogicalOpsInArgs(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir()}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"command": "make",
		"args":    []any{"build", "&&", "make", "test"},
	}, configValue)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Mode != "shell" {
		t.Fatalf("expected shell mode for && in args, got %#v", payload)
	}
}

func TestNormalizeStructuredCommandArgsKeepsExecForNormalArgs(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir()}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"command": "grep",
		"args":    []any{"-r", "func.*main", "--include=*.go", "."},
	}, configValue)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Mode != "exec" {
		t.Fatalf("expected exec mode for normal args, got %#v", payload)
	}
	if payload.Command != "grep" {
		t.Fatalf("expected grep command, got %q", payload.Command)
	}
}

func TestShellExecStructuredPipeEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pipe test uses POSIX shell")
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"command": "echo",
		"args":    []any{"hello world", "|", "wc", "-w"},
	})
	if err != nil {
		t.Fatalf("shellExec failed: %v", err)
	}
	stdout := strings.TrimSpace(stringValue(result["stdout"]))
	if stdout != "2" {
		t.Fatalf("expected '2' from 'echo hello world | wc -w', got %q", stdout)
	}
}

func TestShellExecStructuredPipeWithGrep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pipe test uses POSIX shell")
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"command": "printf",
		"args":    []any{"foo\\nbar\\nbaz\\n", "|", "grep", "ba"},
	})
	if err != nil {
		t.Fatalf("shellExec failed: %v", err)
	}
	stdout := strings.TrimSpace(stringValue(result["stdout"]))
	if stdout != "bar\nbaz" {
		t.Fatalf("expected 'bar\\nbaz' from pipe+grep, got %q", stdout)
	}
}

func TestNormalizeCommandArgumentsUsesShellForStructuredBuiltinCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("builtin test uses POSIX export")
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir()}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"command": "export",
		"args":    []any{"TASK_TEST_VALUE=hello world"},
	}, configValue)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Mode != "shell" {
		t.Fatalf("expected shell mode, got %#v", payload)
	}
	if payload.RawCommand != "export TASK_TEST_VALUE='hello world'" {
		t.Fatalf("unexpected raw command: %#v", payload)
	}
	if payload.Command != "" || payload.Args != nil {
		t.Fatalf("expected structured builtin to clear exec command fields, got %#v", payload)
	}
}

func TestNormalizeLegacyCommandFieldUsesRawForWindowsBuiltins(t *testing.T) {
	commandName, args, rawCommand := normalizeLegacyCommandField(`dir /b`, nil, t.TempDir(), "windows")
	if commandName != "" {
		t.Fatalf("expected empty exec command, got %q", commandName)
	}
	if args != nil {
		t.Fatalf("expected nil args, got %#v", args)
	}
	if rawCommand != `dir /b` {
		t.Fatalf("expected raw command fallback, got %q", rawCommand)
	}
}

func TestNormalizeLegacyCommandFieldPreservesQuotedArguments(t *testing.T) {
	commandName, args, rawCommand := normalizeLegacyCommandField(`python -c "print('ok')"`, nil, t.TempDir(), runtime.GOOS)
	if rawCommand != "" {
		t.Fatalf("expected exec mode, got raw command %q", rawCommand)
	}
	if commandName != "python" {
		t.Fatalf("expected python command, got %q", commandName)
	}
	if len(args) != 2 || args[0] != "-c" || args[1] != "print('ok')" {
		t.Fatalf("unexpected args: %#v", args)
	}
}

func TestNormalizeCommandArgumentsToleratesTransportEscapedQuotes(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir()}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"command": `python -c \"print('ok')\"`,
	}, configValue)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Mode != "exec" {
		t.Fatalf("expected exec mode, got %#v", payload)
	}
	if payload.Command != "python" {
		t.Fatalf("expected python command, got %#v", payload)
	}
	if len(payload.Args) != 2 || payload.Args[0] != "-c" || payload.Args[1] != "print('ok')" {
		t.Fatalf("unexpected args: %#v", payload.Args)
	}
}

func TestNormalizeTransportEscapesPreservesEscapedDollar(t *testing.T) {
	if got := normalizeTransportEscapes(`printf \"%s\" \"\$HOME\"`); got != `printf "%s" "\$HOME"` {
		t.Fatalf("unexpected normalized command: %q", got)
	}
}

func TestShellExecRawCommandKeepsEscapedDollarLiteral(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}
	t.Setenv("HOME", "/tmp/should-not-expand")

	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"raw_command": `printf \"%s\" \"\$HOME\"`,
	})
	if err != nil {
		t.Fatalf("shellExec failed: %v", err)
	}
	if got := strings.TrimSpace(stringValue(result["stdout"])); got != "$HOME" {
		t.Fatalf("expected escaped dollar to remain literal, got %q", got)
	}
}

func TestShellExecReceivesExplicitSensitiveEnvOverrides(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"command": "sh",
		"args":    []any{"-lc", `printf %s "$DASHSCOPE_API_KEY"`},
		"env": map[string]any{
			"DASHSCOPE_API_KEY": "secret-1",
		},
	})
	if err != nil {
		t.Fatalf("shellExec failed: %v", err)
	}
	if strings.TrimSpace(stringValue(result["stdout"])) != "secret-1" {
		t.Fatalf("expected explicit env override to reach subprocess, got %#v", result)
	}
	if result["exit_code"] != 0 {
		t.Fatalf("expected zero exit code, got %#v", result)
	}
}

func TestShellExecInjectsRequiredEnvFromSourceSkill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	workdir := t.TempDir()
	skillRoot := t.TempDir()
	skillDir := filepath.Join(skillRoot, "wan-video")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	content := "---\nname: wan-video\ndescription: 视频生成\nmetadata:\n  openclaw:\n    requires:\n      env:\n        - DASHSCOPE_API_KEY\n---\n\n使用万相视频能力。\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}
	t.Setenv("DASHSCOPE_API_KEY", "secret-from-host")

	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}
	configValue.Skills.Directory = skillRoot

	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"command":      "sh",
		"args":         []any{"-lc", `printf %s "$DASHSCOPE_API_KEY"`},
		"source_skill": "wan-video",
	})
	if err != nil {
		t.Fatalf("shellExec failed: %v", err)
	}
	if strings.TrimSpace(stringValue(result["stdout"])) != "secret-from-host" {
		t.Fatalf("expected source skill env injection to reach subprocess, got %#v", result)
	}
	if result["source_skill"] != "wan-video" {
		t.Fatalf("expected source_skill in result, got %#v", result)
	}
	injectedEnvKeys := stringSlice(result["injected_env_keys"])
	if len(injectedEnvKeys) != 1 || injectedEnvKeys[0] != "DASHSCOPE_API_KEY" {
		t.Fatalf("expected injected env keys in result, got %#v", result)
	}
	if result["exit_code"] != 0 {
		t.Fatalf("expected zero exit code, got %#v", result)
	}
}

func TestShellExecRejectsMissingRequiredEnvFromSourceSkill(t *testing.T) {
	workdir := t.TempDir()
	skillRoot := t.TempDir()
	skillDir := filepath.Join(skillRoot, "wan-video")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	content := "---\nname: wan-video\ndescription: 视频生成\nmetadata:\n  openclaw:\n    requires:\n      env:\n        - DASHSCOPE_API_KEY\n---\n\n使用万相视频能力。\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}
	configValue.Skills.Directory = skillRoot

	_, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"raw_command":  "printf 'noop'",
		"source_skill": "wan-video",
	}, configValue)
	if err == nil {
		t.Fatal("expected missing required env to fail")
	}
	toolErr, ok := err.(toolError)
	if !ok || toolErr.Code != "MISSING_ENV" {
		t.Fatalf("expected MISSING_ENV error, got %#v", err)
	}
}

func TestShellExecBackgroundSupportsStructuredBuiltinCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("builtin test uses POSIX export")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"command":    "export",
		"args":       []any{"TASK_TEST_VALUE=hello world"},
		"background": true,
		"task_name":  "export-task",
	})
	if err != nil {
		t.Fatalf("shellExec background failed: %v", err)
	}
	taskID := result["task_id"].(string)
	sharedLocalTaskManager.mu.RLock()
	task := sharedLocalTaskManager.tasks[taskID]
	sharedLocalTaskManager.mu.RUnlock()
	if task == nil {
		t.Fatal("background task was not registered")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, outputErr := runtimeClient.taskOutput(configValue, map[string]any{"task_id": taskID})
		if outputErr == nil && stringValue(output["status"]) != "running" {
			select {
			case <-task.done:
			case <-time.After(2 * time.Second):
				t.Fatal("background task status changed before persistence completed")
			}
			// 任务完成后仍可查询，不再自动清理
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected structured builtin background task to complete and remain queryable")
}

func TestTaskStopCancelsBackgroundCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
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

	// 等待任务状态变为 cancelled
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, outputErr := runtimeClient.taskOutput(config, map[string]any{"task_id": taskID})
		if outputErr == nil && stringValue(output["status"]) == "cancelled" {
			// 任务停止后仍应可查询（不再自动清理）
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected task %s to be cancelled and still queryable", taskID)
}

func TestTaskManagerDoesNotRestorePersistedTaskAcrossManagerInstances(t *testing.T) {
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}
	storeDir, err := defaultTaskStoreDir()
	if err != nil {
		t.Fatalf("defaultTaskStoreDir failed: %v", err)
	}
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mkdir task store failed: %v", err)
	}
	metadataPath, stdoutPath, stderrPath := taskFilePaths(storeDir, "task-persisted")
	content, err := json.MarshalIndent(persistedTaskState{
		TaskID:      "task-persisted",
		TaskName:    "persisted-task",
		Command:     "printf 'persisted task'",
		CWD:         configValue.Workdir,
		Status:      "completed",
		StartedAt:   time.Now().Add(-time.Minute),
		CompletedAt: time.Now().Add(-30 * time.Second),
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal persisted task failed: %v", err)
	}
	if err := os.WriteFile(metadataPath, content, 0o644); err != nil {
		t.Fatalf("write metadata failed: %v", err)
	}
	if err := os.WriteFile(stdoutPath, []byte("persisted task\n"), 0o644); err != nil {
		t.Fatalf("write stdout failed: %v", err)
	}
	if err := os.WriteFile(stderrPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write stderr failed: %v", err)
	}

	restoredManager := newLocalTaskManager()
	listed := restoredManager.taskList(runtimeClient, configValue)
	if listed["count"].(int) != 0 {
		t.Fatalf("expected stale tasks not to be restored, got %#v", listed)
	}
	for _, path := range []string{metadataPath, stdoutPath, stderrPath} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("expected stale task file %s to be cleared, stat err=%v", path, statErr)
		}
	}
}

func TestExecuteClientToolPayloadShellExecPersistsContextIdentifiersAndSourceSkill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto", SandboxEscapePolicy: internalconfig.SandboxEscapePolicyAllow}
	skillRoot := t.TempDir()
	skillDir := filepath.Join(skillRoot, "wan-video")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	content := "---\nname: wan-video\ndescription: 视频生成\nmetadata:\n  openclaw:\n    requires:\n      env:\n        - DASHSCOPE_API_KEY\n---\n\n使用万相视频能力。\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}
	t.Setenv("DASHSCOPE_API_KEY", "secret-from-context")
	configValue.Skills.Directory = skillRoot

	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), configValue, map[string]any{
		"flow_id": "demo-flow",
		"context": map[string]any{"session_id": "session-ctx", "run_id": "run-ctx", "source_skill": "wan-video"},
		"server":  map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "shell_exec",
			"arguments": map[string]any{
				"command":    "sh",
				"args":       []any{"-lc", `printf %s "$DASHSCOPE_API_KEY"; sleep 0.5`},
				"background": true,
				"task_name":  "ctx-task",
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
	if result["flow_id"] != "demo-flow" || result["session_id"] != "session-ctx" || result["run_id"] != "run-ctx" {
		t.Fatalf("expected context identifiers in task summary, got %#v", result)
	}
	if result["source_skill"] != "wan-video" {
		t.Fatalf("expected source_skill in task summary, got %#v", result)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, outputErr := runtimeClient.taskOutput(configValue, map[string]any{"task_id": taskID})
		if outputErr != nil {
			t.Fatalf("taskOutput failed before auto cleanup: %v", outputErr)
		}
		if output["flow_id"] != "demo-flow" || output["session_id"] != "session-ctx" || output["run_id"] != "run-ctx" {
			t.Fatalf("expected context identifiers in task output, got %#v", output)
		}
		if output["source_skill"] != "wan-video" {
			t.Fatalf("expected source_skill in task output, got %#v", output)
		}
		if strings.TrimSpace(stringValue(output["stdout"])) == "secret-from-context" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected running task output to preserve context identifiers and injected env")
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
		"raw_command": "printf 'capture out\\n'; printf 'capture err\\n' 1>&2; sleep 0.5",
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

func TestTaskDeleteRejectsRunningTask(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"raw_command": "while true; do echo keep; sleep 1; done",
		"background":  true,
		"task_name":   "delete-task",
	})
	if err != nil {
		t.Fatalf("shellExec background failed: %v", err)
	}
	taskID := result["task_id"].(string)
	defer ShutdownLocalTasks()

	if _, err := runtimeClient.taskDelete(configValue, map[string]any{"task_id": taskID}); err == nil {
		t.Fatal("expected running task delete to be rejected")
	}
}

func TestTaskCleanupSkipsAutoClearedCompletedTasksAndKeepsRunningTasks(t *testing.T) {
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
		if outputErr == nil && stringValue(output["status"]) != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cleaned, err := runtimeClient.taskCleanup(configValue, map[string]any{})
	if err != nil {
		t.Fatalf("taskCleanup failed: %v", err)
	}
	// 已完成任务不再自动清理，需要 taskCleanup 显式清理
	if cleaned["deleted_count"].(int) != 1 {
		t.Fatalf("expected one completed task to be cleaned by taskCleanup, got %#v", cleaned)
	}
	if cleaned["running_count"].(int) != 1 {
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
	// 停止后任务仍保留，不再自动清理
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, outputErr := runtimeClient.taskOutput(configValue, map[string]any{"task_id": runningTaskID})
		if outputErr == nil && stringValue(output["status"]) == "cancelled" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected stopped running task to be cancelled and still queryable")
}

func TestShutdownLocalTasksStopsRunningTasksAndClearsStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}
	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"raw_command": "while true; do echo keep; sleep 1; done",
		"background":  true,
		"task_name":   "shutdown-task",
	})
	if err != nil {
		t.Fatalf("shellExec background failed: %v", err)
	}
	taskID := result["task_id"].(string)
	storeDir, err := defaultTaskStoreDir()
	if err != nil {
		t.Fatalf("defaultTaskStoreDir failed: %v", err)
	}

	ShutdownLocalTasks()

	if _, outputErr := runtimeClient.taskOutput(configValue, map[string]any{"task_id": taskID}); outputErr == nil {
		t.Fatalf("expected shutdown to remove task %s", taskID)
	}
	entries, readErr := os.ReadDir(storeDir)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read task store failed: %v", readErr)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), taskID) {
			t.Fatalf("expected shutdown to clear persisted task files, found %s", entry.Name())
		}
	}
}

func TestSkillReturnsInvocationPayload(t *testing.T) {
	workdir := t.TempDir()
	skillDir := t.TempDir()
	if err := os.MkdirAll(skillDir+"/reviewer", 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	content := "---\nname: Code Review\ndescription: 审查变更\nhomepage: https://example.com/code-review\ncategory: engineering\nmetadata:\n  openclaw:\n    requires:\n      env:\n        - REVIEW_API_KEY\n---\n\n检查回归风险。\n"
	if err := os.WriteFile(skillDir+"/reviewer/SKILL.md", []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}
	configValue.Skills.Directory = skillDir

	if _, err := runtimeClient.skill(context.Background(), nil, configValue, map[string]any{}); err == nil {
		t.Fatal("expected Skill to require name")
	}

	result, err := runtimeClient.skill(context.Background(), nil, configValue, map[string]any{"name": "Code Review", "input": "检查 cmd/root.go"})
	if err != nil {
		t.Fatalf("skill failed: %v", err)
	}
	if result["source_skill"].(string) != "Code Review" {
		t.Fatalf("expected source_skill in skill result, got %#v", result)
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
	if result["skill_dir"].(string) != filepath.Join(skillDir, "reviewer") {
		t.Fatalf("unexpected skill skill_dir: %#v", result)
	}
	if result["relative_dir"].(string) != "reviewer" {
		t.Fatalf("unexpected skill relative_dir: %#v", result)
	}
	if result["homepage"].(string) != "https://example.com/code-review" {
		t.Fatalf("unexpected skill homepage: %#v", result)
	}
	requiresEnv := stringSlice(result["requires_env"])
	if len(requiresEnv) != 1 || requiresEnv[0] != "REVIEW_API_KEY" {
		t.Fatalf("unexpected skill requires_env: %#v", result)
	}
	metadata, ok := result["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata in skill result, got %#v", result)
	}
	if _, ok := metadata["openclaw"]; !ok {
		t.Fatalf("expected openclaw metadata in skill result, got %#v", metadata)
	}
}

func TestReadFileResolvesRelativePathInsideSkillDirectory(t *testing.T) {
	workdir := t.TempDir()
	skillRoot := t.TempDir()
	skillPackageDir := filepath.Join(skillRoot, "reviewer")
	if err := os.MkdirAll(filepath.Join(skillPackageDir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir skill docs failed: %v", err)
	}
	content := "---\nname: Code Review\ndescription: 审查变更\n---\n\n读取技能参考文件。\n"
	if err := os.WriteFile(filepath.Join(skillPackageDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillPackageDir, "docs", "reference.md"), []byte("skill local reference\n"), 0o644); err != nil {
		t.Fatalf("write reference failed: %v", err)
	}

	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}
	configValue.Skills.Directory = skillRoot

	result, err := runtimeClient.readFile(configValue, map[string]any{"path": "docs/reference.md"})
	if err != nil {
		t.Fatalf("readFile failed: %v", err)
	}
	if strings.TrimSpace(stringValue(result["content"])) != "skill local reference" {
		t.Fatalf("expected skill-local reference content, got %#v", result)
	}
	resolvedReferencePath, resolveErr := filepath.EvalSymlinks(filepath.Join(skillPackageDir, "docs", "reference.md"))
	if resolveErr != nil {
		resolvedReferencePath = filepath.Join(skillPackageDir, "docs", "reference.md")
	}
	if result["path"] != resolvedReferencePath {
		t.Fatalf("expected resolved path inside skill directory, got %#v", result)
	}
}

func TestShellExecResolvesSkillAliasAsWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	workdir := t.TempDir()
	skillRoot := t.TempDir()
	skillPackageDir := filepath.Join(skillRoot, "qq-email-summary")
	if err := os.MkdirAll(filepath.Join(skillPackageDir, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir skill scripts failed: %v", err)
	}
	content := "---\nname: email-summary\ndescription: 邮件摘要\n---\n\n运行技能目录内脚本。\n"
	if err := os.WriteFile(filepath.Join(skillPackageDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillPackageDir, "scripts", "run.js"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatalf("write script failed: %v", err)
	}

	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}
	configValue.Skills.Directory = skillRoot

	result, err := runtimeClient.shellExec(context.Background(), configValue, map[string]any{
		"raw_command":       "pwd",
		"working_directory": "email-summary",
	})
	if err != nil {
		t.Fatalf("shellExec failed: %v", err)
	}
	resolvedSkillDir, resolveErr := filepath.EvalSymlinks(skillPackageDir)
	if resolveErr != nil {
		resolvedSkillDir = skillPackageDir
	}
	if strings.TrimSpace(stringValue(result["stdout"])) != resolvedSkillDir {
		t.Fatalf("expected cwd to resolve to skill directory %q, got %#v", resolvedSkillDir, result)
	}
	if result["cwd"] != resolvedSkillDir {
		t.Fatalf("expected result cwd %q, got %#v", resolvedSkillDir, result)
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

	result, err := runtimeClient.skill(context.Background(), nil, configValue, map[string]any{"name": "taiyiflow-api"})
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

func TestGlobFilesMatchesSkillDirectoryAlias(t *testing.T) {
	rootDir := t.TempDir()
	if err := os.Setenv(config.RootDirEnvName(), rootDir); err != nil {
		t.Fatalf("set env failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv(config.RootDirEnvName())
	})
	workdir := t.TempDir()
	skillDir := filepath.Join(workdir, ".snz1dp", "skills", "qq-email-summary")
	if err := os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: email-summary\ndescription: 邮件摘要\n---\n\n运行邮件摘要。\n"), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "scripts", "run.js"), []byte("console.log('ok')\n"), 0o644); err != nil {
		t.Fatalf("write script failed: %v", err)
	}

	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}

	globResult, err := runtimeClient.globFiles(configValue, map[string]any{
		"path":    ".snz1dp/skills",
		"pattern": "**/email-summary/**",
	})
	if err != nil {
		t.Fatalf("globFiles failed: %v", err)
	}
	items, ok := globResult["items"].([]map[string]any)
	if !ok {
		t.Fatalf("unexpected items payload: %#v", globResult["items"])
	}
	if len(items) != 4 {
		t.Fatalf("unexpected glob result: %#v", globResult)
	}
	matched := map[string]bool{}
	for _, item := range items {
		matched[stringValue(item["relative_path"])] = true
	}
	for _, expected := range []string{
		"qq-email-summary",
		"qq-email-summary/SKILL.md",
		"qq-email-summary/scripts",
		"qq-email-summary/scripts/run.js",
	} {
		if !matched[expected] {
			t.Fatalf("missing expected glob match %q: %#v", expected, globResult)
		}
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

func TestFileEditAcceptsNonUTF8TextWithoutTreatingItAsBinary(t *testing.T) {
	workdir := t.TempDir()
	filePath := workdir + "/legacy.txt"
	legacyComment := []byte{0xB2, 0xE2, 0xCA, 0xD4}
	content := append([]byte("title=demo\ncomment="), legacyComment...)
	content = append(content, '\n')
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}

	result, err := runtimeClient.fileEdit(configValue, map[string]any{
		"path":       "legacy.txt",
		"old_string": "demo",
		"new_string": "updated",
	})
	if err != nil {
		t.Fatalf("expected non-utf8 text file to remain editable, got %v", err)
	}
	if result["replacements"].(int) != 1 {
		t.Fatalf("unexpected file edit result: %#v", result)
	}
	updatedContent, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatalf("read updated file failed: %v", readErr)
	}
	if !bytes.Contains(updatedContent, []byte("title=updated")) {
		t.Fatalf("expected updated ASCII segment, got %q", string(updatedContent))
	}
}

func TestFileEditAcceptsReadFileLFContentAgainstCRLFFile(t *testing.T) {
	workdir := t.TempDir()
	filePath := workdir + "/main.go"
	content := "package main\r\n\r\nfunc main() {\r\n\tprintln(\"hello\")\r\n}\r\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	configValue := internalconfig.Config{Workdir: workdir, ApprovalMode: "auto"}

	readResult, err := runtimeClient.readFile(configValue, map[string]any{
		"path":       "main.go",
		"start_line": 3,
		"end_line":   4,
	})
	if err != nil {
		t.Fatalf("readFile failed: %v", err)
	}
	oldString := readResult["content"].(string)
	if strings.Contains(oldString, "\r\n") {
		t.Fatalf("expected readFile to normalize CRLF to LF, got %q", oldString)
	}

	result, err := runtimeClient.fileEdit(configValue, map[string]any{
		"path":       "main.go",
		"old_string": oldString,
		"new_string": "func main() {\n\tprintln(\"updated\")",
	})
	if err != nil {
		t.Fatalf("expected fileEdit to accept LF old_string copied from readFile, got %v", err)
	}
	if result["replacements"].(int) != 1 {
		t.Fatalf("unexpected replacements: %#v", result)
	}
	if normalized, ok := result["normalized_old_string_match"].(bool); !ok || !normalized {
		t.Fatalf("expected normalized_old_string_match flag, got %#v", result)
	}
	updatedContent, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read updated file failed: %v", err)
	}
	updatedText := string(updatedContent)
	if !strings.Contains(updatedText, "println(\"updated\")") {
		t.Fatalf("expected updated content, got %q", updatedText)
	}
	if !strings.Contains(updatedText, "\r\n") {
		t.Fatalf("expected edited file to preserve CRLF line endings, got %q", updatedText)
	}
	if strings.Contains(updatedText, "func main() {\n") {
		t.Fatalf("expected edited block not to introduce bare LF into CRLF file, got %q", updatedText)
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
