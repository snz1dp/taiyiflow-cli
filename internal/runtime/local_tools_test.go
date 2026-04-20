package runtime

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestWriteReadAndPatchFile(t *testing.T) {
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

	patch := strings.Join([]string{
		"--- a/notes.txt",
		"+++ b/notes.txt",
		"@@ -1,2 +1,2 @@",
		" hello",
		"-world",
		"+taiyiflow",
		"",
	}, "\n")
	if _, err := runtimeClient.applyPatch(config, map[string]any{"patch": patch}); err != nil {
		t.Fatalf("apply patch failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(tempDir, "notes.txt"))
	if err != nil {
		t.Fatalf("read patched file failed: %v", err)
	}
	if string(content) != "hello\ntaiyiflow\n" {
		t.Fatalf("unexpected patched content: %q", string(content))
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

func TestSearchFilesAndListDir(t *testing.T) {
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
		"query":            "alpha",
		"path":             "docs",
		"include_patterns": []any{"*.md"},
		"max_results":      1,
	})
	if err != nil {
		t.Fatalf("search files failed: %v", err)
	}
	if searchResult["count"].(int) != 1 {
		t.Fatalf("unexpected search count: %v", searchResult["count"])
	}

	listResult, err := runtimeClient.listDir(config, map[string]any{"path": "docs"})
	if err != nil {
		t.Fatalf("list dir failed: %v", err)
	}
	entries := listResult["entries"].([]map[string]any)
	if len(entries) != 1 || entries[0]["name"].(string) != "a.md" {
		t.Fatalf("unexpected list dir result: %#v", entries)
	}
}

func TestApplyPatchConflictDetails(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}
	if err := os.WriteFile(filepath.Join(tempDir, "notes.txt"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	patch := strings.Join([]string{
		"--- a/notes.txt",
		"+++ b/notes.txt",
		"@@ -1,2 +1,2 @@",
		" hello",
		"-planet",
		"+taiyiflow",
		"",
	}, "\n")
	result, err := runtimeClient.applyPatch(config, map[string]any{"patch": patch, "dry_run": true})
	if err == nil {
		t.Fatal("expected applyPatch to report conflict")
	}
	conflicts := result["conflicts"].([]map[string]any)
	if len(conflicts) != 1 {
		t.Fatalf("unexpected conflicts: %#v", conflicts)
	}
	if conflicts[0]["operation"].(string) != "delete" {
		t.Fatalf("unexpected conflict operation: %#v", conflicts[0])
	}
	if conflicts[0]["line_number"].(int) != 2 {
		t.Fatalf("unexpected conflict line: %#v", conflicts[0])
	}
	if conflicts[0]["expected"].(string) != "planet" || conflicts[0]["actual"].(string) != "world" {
		t.Fatalf("unexpected conflict payload: %#v", conflicts[0])
	}
}

func TestGitStatusDiffAndLog(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tempDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = tempDir
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(output))
		}
	}
	runGit("init")
	runGit("config", "user.name", "Taiyi Tester")
	runGit("config", "user.email", "taiyi@example.com")
	if err := os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runGit("add", "main.go")
	runGit("commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte("# demo\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	runtimeClient := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}
	statusResult, err := runtimeClient.gitStatus(context.Background(), config, map[string]any{"path": tempDir})
	if err != nil {
		t.Fatalf("git status failed: %v", err)
	}
	if len(statusResult["modified"].([]string)) != 1 || len(statusResult["untracked"].([]string)) != 1 {
		t.Fatalf("unexpected git status result: %#v", statusResult)
	}
	diffResult, err := runtimeClient.gitDiff(context.Background(), config, map[string]any{"path": tempDir})
	if err != nil {
		t.Fatalf("git diff failed: %v", err)
	}
	if !strings.Contains(diffResult["content"].(string), "func main() {}") {
		t.Fatalf("unexpected git diff content: %q", diffResult["content"].(string))
	}
	logResult, err := runtimeClient.gitLog(context.Background(), config, map[string]any{"path": tempDir, "max_commits": 5})
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if logResult["count"].(int) < 1 {
		t.Fatalf("unexpected git log result: %#v", logResult)
	}
}

func TestGitAddAndCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	tempDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = tempDir
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(output))
		}
	}
	runGit("init")
	runGit("config", "user.name", "Taiyi Tester")
	runGit("config", "user.email", "taiyi@example.com")
	if err := os.WriteFile(filepath.Join(tempDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runGit("add", "main.go")
	runGit("commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(tempDir, "README.md"), []byte("# updated\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	runtimeClient := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}
	addResult, err := runtimeClient.gitAdd(context.Background(), config, map[string]any{"paths": []any{"README.md"}})
	if err != nil {
		t.Fatalf("git add failed: %v", err)
	}
	if addResult["count"].(int) != 1 {
		t.Fatalf("unexpected git add result: %#v", addResult)
	}
	commitResult, err := runtimeClient.gitCommit(context.Background(), config, map[string]any{"message": "docs: update readme"})
	if err != nil {
		t.Fatalf("git commit failed: %v", err)
	}
	if strings.TrimSpace(commitResult["subject"].(string)) != "docs: update readme" {
		t.Fatalf("unexpected git commit subject: %#v", commitResult)
	}
	logResult, err := runtimeClient.gitLog(context.Background(), config, map[string]any{"path": tempDir, "max_commits": 1})
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	commits := logResult["commits"].([]map[string]any)
	if len(commits) != 1 || commits[0]["subject"].(string) != "docs: update readme" {
		t.Fatalf("unexpected git log result after commit: %#v", logResult)
	}
}
