package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
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