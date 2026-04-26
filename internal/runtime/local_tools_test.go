package runtime

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestWriteAndReadFile(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}

	writeResult, err := runtimeClient.writeFile(config, map[string]any{
		"path":    "notes.txt",
		"content": "hello\nworld\n",
		"mode":    "create",
	})
	if err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	if writeResult["size"].(int) != len("hello\nworld\n") {
		t.Fatalf("unexpected size: %v", writeResult["size"])
	}

	readResult, err := runtimeClient.readFile(config, map[string]any{"path": "notes.txt", "start_line": 2, "end_line": 2})
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if readResult["content"].(string) != "world" {
		t.Fatalf("unexpected read content: %q", readResult["content"].(string))
	}
}

func TestResolveAllowedPathRejectsEscape(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}
	outsidePath := filepath.Join(tempDir, "..", "outside.txt")
	if _, err := resolveAllowedPath(config, outsidePath, true); err == nil {
		t.Fatal("expected resolveAllowedPath to reject sandbox escape")
	}
}

func TestSandboxEscapePolicyDenyAllowConfirm(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")

	denyRuntime := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	denyConfig := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyDeny,
	}
	if _, err := denyRuntime.resolveAllowedPathWithPolicy(denyConfig, outsidePath, true); err == nil {
		t.Fatal("expected deny policy to reject sandbox escape")
	}

	allowRuntime := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	allowConfig := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyAllow,
	}
	if resolved, err := allowRuntime.resolveAllowedPathWithPolicy(allowConfig, outsidePath, true); err != nil {
		t.Fatalf("expected allow policy to pass: %v", err)
	} else if !strings.HasSuffix(resolved, "outside.txt") {
		t.Fatalf("unexpected resolved path: %s", resolved)
	}

	confirmApprovedRuntime := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	confirmConfig := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyConfirm,
	}
	if _, err := confirmApprovedRuntime.resolveAllowedPathWithPolicy(confirmConfig, outsidePath, true); err != nil {
		t.Fatalf("expected confirm policy with approval to pass: %v", err)
	}

	confirmRejectedRuntime := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	if _, err := confirmRejectedRuntime.resolveAllowedPathWithPolicy(confirmConfig, outsidePath, true); err == nil {
		t.Fatal("expected confirm policy rejection to fail")
	}

	confirmNoInputRuntime := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	if _, err := confirmNoInputRuntime.resolveAllowedPathWithPolicy(confirmConfig, outsidePath, true); err == nil || !strings.Contains(err.Error(), "无交互输入") {
		t.Fatalf("expected confirm policy without input to fail with guidance, got: %v", err)
	}
}

func TestToolErrorDoesNotExposeBracketCode(t *testing.T) {
	err := newToolError("PERMISSION_DENIED", "路径被拒绝")
	if strings.Contains(err.Error(), "[") || strings.Contains(err.Error(), "]") {
		t.Fatalf("expected user-facing error without bracket code, got: %s", err.Error())
	}
}

func TestSandboxEscapeAlwaysApproveSkipsSubsequentConfirmations(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	confirmCalls := 0
	runtimeClient.SetSandboxEscapeConfirmer(func(path string) (bool, error) {
		confirmCalls++
		runtimeClient.SetSandboxEscapePolicyOverride(internalconfig.SandboxEscapePolicyAllow)
		return true, nil
	})
	config := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyConfirm,
	}
	if _, err := runtimeClient.resolveAllowedPathWithPolicy(config, outsidePath, true); err != nil {
		t.Fatalf("expected first confirm to pass: %v", err)
	}
	if _, err := runtimeClient.resolveAllowedPathWithPolicy(config, outsidePath, true); err != nil {
		t.Fatalf("expected second path resolution to reuse always-approve override: %v", err)
	}
	if confirmCalls != 1 {
		t.Fatalf("expected confirmer to be called once, got %d", confirmCalls)
	}
}

func TestSandboxEscapePolicyUsesInjectedConfirmer(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")
	canonicalOutsideDir, err := filepath.EvalSymlinks(outsideDir)
	if err != nil {
		t.Fatalf("eval symlink failed: %v", err)
	}
	expectedPath := filepath.Join(canonicalOutsideDir, "outside.txt")
	stdout := &bytes.Buffer{}
	runtimeClient := NewClientRuntime(strings.NewReader(""), stdout, io.Discard)
	called := false
	runtimeClient.SetSandboxEscapeConfirmer(func(path string) (bool, error) {
		called = true
		if path != expectedPath {
			t.Fatalf("unexpected path: %s", path)
		}
		return true, nil
	})
	config := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyConfirm,
	}
	if _, err := runtimeClient.resolveAllowedPathWithPolicy(config, outsidePath, true); err != nil {
		t.Fatalf("expected injected confirmer to approve path: %v", err)
	}
	if !called {
		t.Fatal("expected injected confirmer to be called")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no terminal prompt output when confirmer injected, got: %q", stdout.String())
	}
}

func TestSearchFiles(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "docs", "a.md"), []byte("alpha\nbeta\nalpha again\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}

	searchResult, err := runtimeClient.searchFiles(context.Background(), config, map[string]any{
		"pattern":         "alpha",
		"path":            "docs",
		"includePatterns": []any{"*.md"},
		"maxResults":      1,
	})
	if err != nil {
		t.Fatalf("search files failed: %v", err)
	}
	if searchResult["count"].(int) != 1 {
		t.Fatalf("unexpected search count: %v", searchResult["count"])
	}
}

func TestCanonicalClientToolNameUsesSnakeCaseNamesOnly(t *testing.T) {
	if got := canonicalClientToolName("lsp_query"); got != "lsp_query" {
		t.Fatalf("unexpected canonical lsp_query name: %s", got)
	}
	if got := canonicalClientToolName("glob_files"); got != "glob_files" {
		t.Fatalf("unexpected canonical glob_files name: %s", got)
	}
	if got := canonicalClientToolName("search_files"); got != "search_files" {
		t.Fatalf("unexpected canonical search_files name: %s", got)
	}
	if got := canonicalClientToolName("shell_exec"); got != "shell_exec" {
		t.Fatalf("unexpected canonical shell_exec name: %s", got)
	}
	if got := canonicalClientToolName("skill"); got != "skill" {
		t.Fatalf("unexpected canonical skill name: %s", got)
	}
	if got := canonicalClientToolName("LSP"); got != "LSP" {
		t.Fatalf("legacy alias should not be normalized: %s", got)
	}
	if got := canonicalClientToolName("Glob"); got != "Glob" {
		t.Fatalf("legacy alias should not be normalized: %s", got)
	}
	if got := canonicalClientToolName("Grep"); got != "Grep" {
		t.Fatalf("legacy alias should not be normalized: %s", got)
	}
	if got := canonicalClientToolName("Bash"); got != "Bash" {
		t.Fatalf("legacy alias should not be normalized: %s", got)
	}
	if got := canonicalClientToolName("Skill"); got != "Skill" {
		t.Fatalf("legacy alias should not be normalized: %s", got)
	}
}

func TestSetStreamEventLogsEnabledSuppressesBracketLogs(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "notes.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	stdout := &bytes.Buffer{}
	runtimeClient := NewClientRuntime(strings.NewReader(""), stdout, io.Discard)
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}

	runtimeClient.SetStreamEventLogsEnabled(false)
	if _, err := runtimeClient.readFile(config, map[string]any{"path": "notes.txt", "start_line": 1, "end_line": 1}); err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if _, err := runtimeClient.runCommand(context.Background(), config, map[string]any{"raw_command": "echo ok"}); err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	if output := stdout.String(); strings.Contains(output, "[本地工具]") || strings.Contains(output, "[本地命令]") || strings.Contains(output, "[执行客户端命令]") {
		t.Fatalf("expected bracket logs to be suppressed, got %q", output)
	}
	if !strings.Contains(stdout.String(), "ok") {
		t.Fatalf("expected command stdout to remain visible, got %q", stdout.String())
	}

	stdout.Reset()
	runtimeClient.SetStreamEventLogsEnabled(true)
	if _, err := runtimeClient.readFile(config, map[string]any{"path": "notes.txt", "start_line": 1, "end_line": 1}); err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if _, err := runtimeClient.runCommand(context.Background(), config, map[string]any{"raw_command": "echo ok"}); err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "[本地工具] read_file") {
		t.Fatalf("expected read_file stream log, got %q", output)
	}
	if !strings.Contains(output, "[本地命令]") {
		t.Fatalf("expected run command stream log, got %q", output)
	}
}
