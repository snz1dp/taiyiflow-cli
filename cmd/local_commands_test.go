package cmd

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
)

func TestExecutePermissionsCommandSetAndShow(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app := &App{configManager: manager}
	if err := manager.Save(internalconfig.Default()); err != nil {
		t.Fatalf("save default config failed: %v", err)
	}

	updated, output, panel, err := app.executePermissionsCommand("/permissions set approval readonly")
	if err != nil {
		t.Fatalf("set approval failed: %v", err)
	}
	if updated.ApprovalMode != "readonly" {
		t.Fatalf("expected readonly approval mode, got %#v", updated)
	}
	if panel == nil || !strings.Contains(output, "readonly") {
		t.Fatalf("expected permissions panel output, got panel=%#v output=%q", panel, output)
	}

	_, output, _, err = app.executePermissionsCommand("/permissions show")
	if err != nil {
		t.Fatalf("show permissions failed: %v", err)
	}
	if !strings.Contains(output, "命令执行模式(approval_mode): readonly") {
		t.Fatalf("unexpected permissions output: %q", output)
	}
}

func TestExecutePermissionsCommandRuleAddAndRemove(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app := &App{configManager: manager}
	if err := manager.Save(internalconfig.Default()); err != nil {
		t.Fatalf("save default config failed: %v", err)
	}

	updated, output, _, err := app.executePermissionsCommand("/permissions rule add tool shell_exec deny")
	if err != nil {
		t.Fatalf("add permission rule failed: %v", err)
	}
	if len(updated.PermissionRules) != 1 {
		t.Fatalf("expected 1 permission rule, got %#v", updated.PermissionRules)
	}
	if updated.PermissionRules[0].ToolName != "shell_exec" || updated.PermissionRules[0].Action != "deny" {
		t.Fatalf("unexpected permission rule: %#v", updated.PermissionRules[0])
	}
	if !strings.Contains(output, "tool=shell_exec") {
		t.Fatalf("expected rule output, got %q", output)
	}

	updated, _, _, err = app.executePermissionsCommand("/permissions rule remove 1")
	if err != nil {
		t.Fatalf("remove permission rule failed: %v", err)
	}
	if len(updated.PermissionRules) != 0 {
		t.Fatalf("expected no permission rules after removal, got %#v", updated.PermissionRules)
	}
}

func TestParseReviewSlashCommand(t *testing.T) {
	target, err := parseReviewSlashCommand("/review cmd")
	if err != nil {
		t.Fatalf("parse review command failed: %v", err)
	}
	if target != "cmd" {
		t.Fatalf("unexpected review target: %q", target)
	}

	target, err = parseReviewSlashCommand("/review")
	if err != nil {
		t.Fatalf("parse bare review command failed: %v", err)
	}
	if target != "" {
		t.Fatalf("unexpected bare review target: %q", target)
	}
}

func TestBuildReviewPromptIncludesDiffSummary(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	workdir := t.TempDir()
	runGitCommand(t, workdir, "init")
	runGitCommand(t, workdir, "config", "user.email", "test@example.com")
	runGitCommand(t, workdir, "config", "user.name", "Test User")
	trackedFile := filepath.Join(workdir, "demo.txt")
	if err := os.WriteFile(trackedFile, []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write tracked file failed: %v", err)
	}
	runGitCommand(t, workdir, "add", "demo.txt")
	runGitCommand(t, workdir, "commit", "-m", "init")
	if err := os.WriteFile(trackedFile, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("update tracked file failed: %v", err)
	}

	app := &App{}
	prompt := app.buildReviewPrompt(context.Background(), workdir, "cmd")
	if !strings.Contains(prompt, "cmd 相关变更") {
		t.Fatalf("expected target in review prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "当前工作区变更(diff)摘要") || !strings.Contains(prompt, "未暂存变更") || !strings.Contains(prompt, "demo.txt") {
		t.Fatalf("expected diff summary in review prompt, got %q", prompt)
	}
	if !strings.Contains(prompt, "只做只读审查") || !strings.Contains(prompt, "写文件(write_file)") || !strings.Contains(prompt, "文件编辑(file_edit)") || !strings.Contains(prompt, "执行命令(shell_exec)") {
		t.Fatalf("expected read-only review constraint in prompt, got %q", prompt)
	}
}

func TestReviewRequestModeIsReadOnlyForMutatingTools(t *testing.T) {
	if reviewRequestMode() != provider.RequestTypeAsk {
		t.Fatalf("expected review request mode to be ask, got %q", reviewRequestMode())
	}
	if modeAllowsClientTool(reviewRequestMode(), "file_edit") {
		t.Fatal("expected review mode to reject file_edit")
	}
	if !modeAllowsClientTool(reviewRequestMode(), "ask_user") {
		t.Fatal("expected review mode to allow ask_user")
	}
	if !modeAllowsClientTool(reviewRequestMode(), "read_file") {
		t.Fatal("expected review mode to allow read_file")
	}
	if !modeAllowsClientTool(reviewRequestMode(), "taiyiflow-in-chrome__tabs_context") {
		t.Fatal("expected review mode to allow readonly browser tabs_context")
	}
	if modeAllowsClientTool(reviewRequestMode(), "taiyiflow-in-chrome__click") {
		t.Fatal("expected review mode to reject mutating browser click tool")
	}
	if !modeAllowsClientTool(reviewRequestMode(), "taiyiflow-computer__displays_context") {
		t.Fatal("expected review mode to allow readonly desktop displays_context")
	}
	if !modeAllowsClientTool(reviewRequestMode(), "taiyiflow-computer__screenshot") {
		t.Fatal("expected review mode to allow readonly desktop screenshot")
	}
	if modeAllowsClientTool(reviewRequestMode(), "taiyiflow-computer__type_text") {
		t.Fatal("expected review mode to reject mutating desktop type_text tool")
	}
}

func TestExecuteTasksSlashCommandCapture(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}
	runtimeClient := clientruntime.NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	_, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), configValue, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "shell_exec",
			"arguments": map[string]any{
				"raw_command": "printf 'capture-stdout\\n'; printf 'capture-stderr\\n' 1>&2; sleep 0.5",
				"background":  true,
				"task_name":   "capture-task",
			},
		},
	})
	if err != nil {
		t.Fatalf("start background task failed: %v", err)
	}
	taskID := resultAny.(map[string]any)["task_id"].(string)
	app := &App{}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, panel, commandErr := app.executeTasksSlashCommand(context.Background(), configValue, "/tasks capture "+taskID)
		if commandErr != nil {
			t.Fatalf("tasks capture failed: %v", commandErr)
		}
		if panel != nil && strings.Contains(output, "capture-stderr") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected /tasks capture to render terminal output")
}

func TestExecuteTasksSlashCommandRemove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}
	runtimeClient := clientruntime.NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	_, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), configValue, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "shell_exec",
			"arguments": map[string]any{
				"raw_command": "while true; do echo remove-me; sleep 1; done",
				"background":  true,
				"task_name":   "remove-task",
			},
		},
	})
	if err != nil {
		t.Fatalf("start background task failed: %v", err)
	}
	taskID := resultAny.(map[string]any)["task_id"].(string)
	app := &App{}
	defer clientruntime.ShutdownLocalTasks()

	_, _, commandErr := app.executeTasksSlashCommand(context.Background(), configValue, "/tasks remove "+taskID)
	if commandErr == nil {
		t.Fatal("expected /tasks remove to reject running task")
	}
}

func TestExecuteTasksSlashCommandCleanup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell test uses POSIX shell")
	}
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	configValue := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}
	runtimeClient := clientruntime.NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), configValue, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "shell_exec",
			"arguments": map[string]any{
				"raw_command": "printf 'cleanup-me\\n'",
				"background":  true,
				"task_name":   "cleanup-task",
			},
		},
	})
	if err != nil {
		t.Fatalf("start background task failed: %v", err)
	}
	app := &App{}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		output, panel, commandErr := app.executeTasksSlashCommand(context.Background(), configValue, "/tasks cleanup")
		if commandErr == nil && panel != nil && strings.Contains(output, "已清理 1 个任务") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected /tasks cleanup to clean one completed task")
}

func TestParseTasksCommandAcceptsTasksOnly(t *testing.T) {
	toolName, arguments, err := parseTasksCommand("/tasks output demo-task")
	if err != nil {
		t.Fatalf("parse /tasks output failed: %v", err)
	}
	if toolName != "task_output" || arguments["task_id"] != "demo-task" {
		t.Fatalf("unexpected parse result: tool=%q args=%#v", toolName, arguments)
	}

	if _, _, err := parseTasksCommand("/task output demo-task"); err == nil {
		t.Fatal("expected /task output to be rejected to avoid conflict with CLI task subcommand")
	}
}

func runGitCommand(t *testing.T, workdir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", workdir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", args, err, strings.TrimSpace(string(output)))
	}
}
