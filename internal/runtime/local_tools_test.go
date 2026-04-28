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

func TestResolveAllowedPathExpandsTildeForRequestedPathAndSandboxRoot(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOMEDRIVE", filepath.VolumeName(homeDir))
	t.Setenv("HOMEPATH", strings.TrimPrefix(homeDir, filepath.VolumeName(homeDir)))
	workdir := t.TempDir()
	targetDir := filepath.Join(homeDir, ".snz1dp", "skills")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	config := internalconfig.Config{
		Workdir:      workdir,
		ApprovalMode: "auto",
		SandboxRoots: []string{"~/.snz1dp/skills"},
	}
	resolved, err := resolveAllowedPath(config, "~/.snz1dp/skills/demo.txt", true)
	if err != nil {
		t.Fatalf("expected tilde path to resolve: %v", err)
	}
	expected, err := resolveWithExistingParent(filepath.Join(targetDir, "demo.txt"))
	if err != nil {
		t.Fatalf("resolve expected path failed: %v", err)
	}
	if resolved != expected {
		t.Fatalf("unexpected resolved path: got %s want %s", resolved, expected)
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
	if _, err := confirmNoInputRuntime.resolveAllowedPathWithPolicy(confirmConfig, outsidePath, true); err == nil || !strings.Contains(err.Error(), "--allow参数允许访问") {
		t.Fatalf("expected confirm policy without input to fail with guidance, got: %v", err)
	}
}

func TestToolErrorDoesNotExposeBracketCode(t *testing.T) {
	err := newToolError("PERMISSION_DENIED", "路径被拒绝")
	if strings.Contains(err.Error(), "[") || strings.Contains(err.Error(), "]") {
		t.Fatalf("expected user-facing error without bracket code, got: %s", err.Error())
	}
}

func TestFileEditToolDefinitionMentionsRetryHintReuse(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := runtimeClient.clientToolDefinitionsForMode("agent")
	for _, item := range definitions {
		name, _ := item["name"].(string)
		if name != "file_edit" {
			continue
		}
		description, _ := item["description"].(string)
		if !strings.Contains(description, "prefetched_read") || !strings.Contains(description, "retry_hint.recommended_args") {
			t.Fatalf("expected file_edit description to mention retry reuse, got %q", description)
		}
		inputSchema, _ := item["inputSchema"].(map[string]any)
		properties, _ := inputSchema["properties"].(map[string]any)
		oldStringSchema, _ := properties["old_string"].(map[string]any)
		expectedHashSchema, _ := properties["expected_sha256_before"].(map[string]any)
		if !strings.Contains(stringValue(oldStringSchema["description"]), "retry_hint.recommended_args.old_string") {
			t.Fatalf("expected old_string description to mention retry reuse, got %#v", oldStringSchema)
		}
		if !strings.Contains(stringValue(expectedHashSchema["description"]), "retry_hint.recommended_args.expected_sha256_before") {
			t.Fatalf("expected expected_sha256_before description to mention retry reuse, got %#v", expectedHashSchema)
		}
		return
	}
	t.Fatal("expected file_edit tool definition")
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

func TestMatchGlobSupportsDoublestar(t *testing.T) {
	testCases := []struct {
		pattern string
		path    string
		matched bool
	}{
		{pattern: "cmd/*.go", path: "cmd/main.go", matched: true},
		{pattern: "**/*.go", path: "cmd/nested/main.go", matched: true},
		{pattern: "**/review/**", path: "skills/review/scripts/run.js", matched: true},
		{pattern: "**/review/**", path: "skills/reviewer/scripts/run.js", matched: false},
	}
	for _, testCase := range testCases {
		if got := matchGlob(testCase.pattern, testCase.path); got != testCase.matched {
			t.Fatalf("matchGlob(%q, %q) = %v, want %v", testCase.pattern, testCase.path, got, testCase.matched)
		}
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

func TestRequiresClientToolConfirmationRespectsSkillAndWritePolicies(t *testing.T) {
	skillPayload := map[string]any{
		"function": map[string]any{
			"name":      "skill",
			"arguments": map[string]any{"name": "demo"},
		},
	}
	writePayload := map[string]any{
		"function": map[string]any{
			"name":      "write_file",
			"arguments": map[string]any{"path": "notes.txt", "content": "hello"},
		},
	}

	confirmConfig := internalconfig.Config{
		ApprovalMode:            "auto",
		SkillReviewPolicy:       internalconfig.SkillReviewPolicyConfirm,
		WriteConfirmationPolicy: internalconfig.WriteConfirmationPolicyConfirm,
	}
	if !RequiresClientToolConfirmation(confirmConfig, skillPayload) {
		t.Fatal("expected skill payload to require confirmation")
	}
	if !RequiresClientToolConfirmation(confirmConfig, writePayload) {
		t.Fatal("expected write payload to require confirmation")
	}

	ignoreConfig := confirmConfig
	ignoreConfig.SkillReviewPolicy = internalconfig.SkillReviewPolicyIgnore
	if RequiresClientToolConfirmation(ignoreConfig, skillPayload) {
		t.Fatal("expected ignored skill review policy to skip confirmation")
	}

	directConfig := confirmConfig
	directConfig.WriteConfirmationPolicy = internalconfig.WriteConfirmationPolicyDirect
	if RequiresClientToolConfirmation(directConfig, writePayload) {
		t.Fatal("expected direct write policy to skip confirmation")
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

func TestPermissionRuleDeniesMutatingClientTool(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		PermissionRules: []internalconfig.PermissionRule{{
			Action:   "deny",
			ToolName: "write_file",
		}},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"path":    "notes.txt",
				"content": "blocked",
				"mode":    "create",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "命中权限规则") {
		t.Fatalf("expected permission rule denial, got %v", err)
	}
}

func TestPermissionRuleAskAndAllowShellExec(t *testing.T) {
	tempDir := t.TempDir()
	askRuntime := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	askConfig := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		PermissionRules: []internalconfig.PermissionRule{{
			Action:        "ask",
			CommandPrefix: "echo",
		}},
	}
	if _, err := askRuntime.ExecuteShellCommand(context.Background(), askConfig, "echo blocked"); err == nil || !strings.Contains(err.Error(), "用户拒绝执行") {
		t.Fatalf("expected ask rule rejection, got %v", err)
	}

	allowRuntime := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	allowConfig := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "confirm",
		PermissionRules: []internalconfig.PermissionRule{{
			Action:        "allow",
			CommandPrefix: "echo",
		}},
	}
	result, err := allowRuntime.ExecuteShellCommand(context.Background(), allowConfig, "echo allowed")
	if err != nil {
		t.Fatalf("expected allow rule to bypass confirmation, got %v", err)
	}
	if !strings.Contains(stringValue(result["stdout"]), "allowed") {
		t.Fatalf("unexpected allow rule stdout: %#v", result)
	}
}

func TestExecuteClientToolPayloadAutoModeBypassesAskRuleForFileEdit(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "notes.txt")
	if err := os.WriteFile(filePath, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		PermissionRules: []internalconfig.PermissionRule{{
			Action:   "ask",
			ToolName: "file_edit",
		}},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "file_edit",
			"arguments": map[string]any{
				"path":       "notes.txt",
				"old_string": "before",
				"new_string": "after",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected auto mode to bypass ask rule, got %v", err)
	}
	content, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatalf("read file failed: %v", readErr)
	}
	if !strings.Contains(string(content), "after") {
		t.Fatalf("expected file_edit to be applied, got %q", string(content))
	}
}

func TestExecuteClientToolPayloadAutoModeBypassesSkillReview(t *testing.T) {
	tempDir := t.TempDir()
	skillDir := filepath.Join(tempDir, ".snz1dp", "skills", "context-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: Context Review\ndescription: 审查工作区技能\n---\n\n检查工作区上下文。\n"), 0o644); err != nil {
		t.Fatalf("write skill file failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	config := internalconfig.Config{
		Workdir:           tempDir,
		ApprovalMode:      "auto",
		SkillReviewPolicy: internalconfig.SkillReviewPolicyConfirm,
	}
	_, result, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "skill",
			"arguments": map[string]any{
				"name": "Context Review",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected auto mode to bypass skill review confirmation, got %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok || strings.TrimSpace(stringValue(resultMap["name"])) != "Context Review" {
		t.Fatalf("unexpected skill result: %#v", result)
	}
}

func TestRequiresClientToolConfirmationSkipsReadonlyMutatingTools(t *testing.T) {
	writePayload := map[string]any{
		"function": map[string]any{
			"name":      "write_file",
			"arguments": map[string]any{"path": "notes.txt", "content": "hello"},
		},
	}
	config := internalconfig.Config{
		ApprovalMode:            "readonly",
		WriteConfirmationPolicy: internalconfig.WriteConfirmationPolicyConfirm,
	}
	if RequiresClientToolConfirmation(config, writePayload) {
		t.Fatal("expected readonly mutating tool to be denied directly without extra confirmation")
	}
}
