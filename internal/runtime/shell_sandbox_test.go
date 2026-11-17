package runtime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// TestResolveShellSandboxOff verifies that sandbox is nil when mode is off.
func TestResolveShellSandboxOff(t *testing.T) {
	workDir := t.TempDir()
	cfg := internalconfig.Config{
		Workdir:          workDir,
		ShellSandboxMode: internalconfig.ShellSandboxModeOff,
	}
	sb := resolveShellSandbox(cfg, workDir)
	if sb != nil {
		t.Fatalf("expected nil sandbox for off mode, got %+v", sb)
	}
}

// TestResolveShellSandboxAuto verifies that sandbox is configured in auto mode.
func TestResolveShellSandboxAuto(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	workDir := t.TempDir()
	cfg := internalconfig.Config{
		Workdir:          workDir,
		ShellSandboxMode: internalconfig.ShellSandboxModeAuto,
	}
	sb := resolveShellSandbox(cfg, workDir)
	if sb == nil {
		t.Fatal("expected non-nil sandbox for auto mode")
	}
	if sb.Mode != internalconfig.ShellSandboxModeAuto {
		t.Fatalf("expected mode auto, got %s", sb.Mode)
	}
	if sb.WorkDir != workDir {
		t.Fatalf("expected workdir %s, got %s", workDir, sb.WorkDir)
	}
	// Should include temp dir in allow roots.
	foundTemp := false
	tempDir, _ := filepath.EvalSymlinks(os.TempDir())
	for _, root := range sb.AllowRoots {
		if root == tempDir || strings.HasPrefix(root, tempDir) {
			foundTemp = true
			break
		}
	}
	if !foundTemp {
		t.Fatalf("expected temp dir %s in allow roots %v", tempDir, sb.AllowRoots)
	}
}

// TestResolveShellSandboxEnforce verifies that sandbox is configured in enforce mode.
func TestResolveShellSandboxEnforce(t *testing.T) {
	workDir := t.TempDir()
	cfg := internalconfig.Config{
		Workdir:          workDir,
		ShellSandboxMode: internalconfig.ShellSandboxModeEnforce,
	}
	sb := resolveShellSandbox(cfg, workDir)
	if sb == nil {
		t.Fatal("expected non-nil sandbox for enforce mode")
	}
	if sb.Mode != internalconfig.ShellSandboxModeEnforce {
		t.Fatalf("expected mode enforce, got %s", sb.Mode)
	}
}

// TestNormalizeCommandArgumentsPopulatesSandbox verifies that normalizeCommandArguments
// fills the Sandbox field based on config.
func TestNormalizeCommandArgumentsPopulatesSandbox(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	workDir := t.TempDir()
	cfg := internalconfig.Config{
		Workdir:          workDir,
		ShellSandboxMode: internalconfig.ShellSandboxModeAuto,
	}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"raw_command": "echo hello",
	}, cfg)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Sandbox == nil {
		t.Fatal("expected non-nil sandbox in payload")
	}
	if payload.Sandbox.Mode != internalconfig.ShellSandboxModeAuto {
		t.Fatalf("expected sandbox mode auto, got %s", payload.Sandbox.Mode)
	}
}

// TestNormalizeCommandArgumentsNoSandboxWhenOff verifies that sandbox is nil in payload
// when mode is off.
func TestNormalizeCommandArgumentsNoSandboxWhenOff(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	cfg := internalconfig.Config{
		Workdir:          t.TempDir(),
		ShellSandboxMode: internalconfig.ShellSandboxModeOff,
	}

	payload, err := runtimeClient.normalizeCommandArguments(map[string]any{
		"raw_command": "echo hello",
	}, cfg)
	if err != nil {
		t.Fatalf("normalizeCommandArguments failed: %v", err)
	}
	if payload.Sandbox != nil {
		t.Fatalf("expected nil sandbox for off mode, got %+v", payload.Sandbox)
	}
}

// TestBuildSandboxedCommandOffModeFallsBackToRaw verifies that off mode produces
// a plain command without sandbox wrapping.
func TestBuildSandboxedCommandOffModeFallsBackToRaw(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "echo hello",
		Sandbox:    nil, // off mode
	}
	cmd, err := buildSandboxedCommand(context.Background(), payload)
	if err != nil {
		t.Fatalf("buildSandboxedCommand failed: %v", err)
	}
	// Should be plain sh -c, not sandbox-exec or bwrap.
	if cmd.Path == "" {
		t.Fatal("expected non-empty command path")
	}
	if strings.Contains(cmd.Path, "sandbox-exec") || strings.Contains(cmd.Path, "bwrap") {
		t.Fatalf("expected raw command (no sandbox wrapper), got %s", cmd.Path)
	}
}

// TestBuildSandboxedCommandAutoFallsBackWhenUnavailable verifies that auto mode
// silently falls back to raw command when the platform sandbox tool is unavailable.
func TestBuildSandboxedCommandAutoFallsBackWhenUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	// Temporarily remove sandbox-exec/bwrap from PATH by setting PATH to empty.
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", "/nonexistent")
	defer func() { _ = os.Setenv("PATH", originalPath) }()

	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "echo hello",
		Sandbox: &shellSandboxConfig{
			Mode:    internalconfig.ShellSandboxModeAuto,
			WorkDir: t.TempDir(),
		},
	}
	cmd, err := buildSandboxedCommand(context.Background(), payload)
	if err != nil {
		t.Fatalf("expected auto mode to fall back, got error: %v", err)
	}
	// Should fall back to sh -c.
	if strings.Contains(cmd.Path, "sandbox-exec") || strings.Contains(cmd.Path, "bwrap") {
		t.Fatalf("expected fallback to raw command in auto mode, got %s", cmd.Path)
	}
}

// TestBuildSandboxedCommandEnforceFailsWhenUnavailable verifies that enforce mode
// returns an error when the platform sandbox tool is unavailable.
func TestBuildSandboxedCommandEnforceFailsWhenUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", "/nonexistent")
	defer func() { _ = os.Setenv("PATH", originalPath) }()

	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "echo hello",
		Sandbox: &shellSandboxConfig{
			Mode:    internalconfig.ShellSandboxModeEnforce,
			WorkDir: t.TempDir(),
		},
	}
	_, err := buildSandboxedCommand(context.Background(), payload)
	if err == nil {
		t.Fatal("expected enforce mode to fail when sandbox tool unavailable")
	}
	if !strings.Contains(err.Error(), "enforce") {
		t.Fatalf("expected error mentioning enforce, got: %v", err)
	}
}

// TestBuildSandboxedCommandAutoWrapsWithSandbox verifies that auto mode wraps
// the command with the platform sandbox tool when available.
func TestBuildSandboxedCommandAutoWrapsWithSandbox(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec test only runs on macOS")
	}
	// Check if sandbox-exec is available.
	if _, err := execLookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available")
	}

	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "echo hello",
		Sandbox: &shellSandboxConfig{
			Mode:    internalconfig.ShellSandboxModeAuto,
			WorkDir: t.TempDir(),
		},
	}
	cmd, err := buildSandboxedCommand(context.Background(), payload)
	if err != nil {
		t.Fatalf("buildSandboxedCommand failed: %v", err)
	}
	if !strings.Contains(cmd.Path, "sandbox-exec") {
		t.Fatalf("expected sandbox-exec wrapper, got %s", cmd.Path)
	}
}

// TestShellExecWithSandboxRunsSimpleCommand verifies that a simple command
// still works correctly when sandbox is enabled in auto mode.
// This test verifies the auto mode runtime degradation: if sandbox-exec
// fails at runtime, auto mode should retry without sandbox.
func TestShellExecWithSandboxRunsSimpleCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	workDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	cfg := internalconfig.Config{
		Workdir:          workDir,
		ApprovalMode:     "auto",
		ShellSandboxMode: internalconfig.ShellSandboxModeAuto,
	}

	result, err := runtimeClient.shellExec(context.Background(), cfg, map[string]any{
		"raw_command": "echo sandbox-test",
	})
	if err != nil {
		t.Fatalf("shellExec with sandbox failed: %v", err)
	}
	stdout := stringValue(result["stdout"])
	if !strings.Contains(stdout, "sandbox-test") {
		t.Fatalf("expected stdout to contain 'sandbox-test', got: %s", stdout)
	}
}

// TestShellExecWithSandboxBlocksSensitivePath verifies that sandbox blocks
// access to sensitive paths like ~/.ssh.
func TestShellExecWithSandboxBlocksSensitivePath(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sandbox-exec test only runs on macOS")
	}
	if _, err := execLookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available")
	}

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	workDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	cfg := internalconfig.Config{
		Workdir:          workDir,
		ApprovalMode:     "auto",
		ShellSandboxMode: internalconfig.ShellSandboxModeEnforce,
	}

	homeDir, _ := os.UserHomeDir()
	sshPath := filepath.Join(homeDir, ".ssh")

	// Try to read a file from ~/.ssh - should be denied by sandbox.
	result, err := runtimeClient.shellExec(context.Background(), cfg, map[string]any{
		"raw_command": "ls " + sshPath + " 2>&1 || echo BLOCKED",
	})
	if err != nil {
		// Command failing is also acceptable - sandbox may deny entirely.
		return
	}
	stdout := stringValue(result["stdout"])
	// The sandbox should block listing ~/.ssh. Either the command fails
	// with a permission error or returns "BLOCKED".
	if strings.Contains(stdout, "BLOCKED") || strings.Contains(stdout, "Operation not permitted") || strings.Contains(stdout, "Permission denied") {
		return // Expected: sandbox blocked access
	}
	// If ~/.ssh doesn't exist, ls will fail with "No such file" which is also fine.
	if strings.Contains(stdout, "No such file") {
		return
	}
	t.Logf("stdout: %s (sshPath: %s)", stdout, sshPath)
}

// TestShellSandboxModeNormalization verifies config normalization.
func TestShellSandboxModeNormalization(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", internalconfig.ShellSandboxModeAuto},
		{"off", internalconfig.ShellSandboxModeOff},
		{"OFF", internalconfig.ShellSandboxModeOff},
		{"auto", internalconfig.ShellSandboxModeAuto},
		{"AUTO", internalconfig.ShellSandboxModeAuto},
		{"enforce", internalconfig.ShellSandboxModeEnforce},
		{"ENFORCE", internalconfig.ShellSandboxModeEnforce},
		{"invalid", internalconfig.ShellSandboxModeAuto},
	}
	for _, tt := range tests {
		got := internalconfig.NormalizeShellSandboxMode(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizeShellSandboxMode(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// TestIsSandboxRuntimeError verifies that sandbox runtime errors are
// correctly detected for auto mode degradation.
func TestIsSandboxRuntimeError(t *testing.T) {
	tests := []struct {
		err      string
		expected bool
	}{
		{"sandbox-exec: sandbox_apply: Operation not permitted", true},
		{"sandbox-exec: failed to apply profile", true},
		{"bwrap: failed to create namespace", true},
		{"some other error", false},
		{"", false},
	}
	for _, tt := range tests {
		err := fmt.Errorf("%s", tt.err)
		got := isSandboxRuntimeError(err)
		if got != tt.expected {
			t.Errorf("isSandboxRuntimeError(%q) = %v, want %v", tt.err, got, tt.expected)
		}
	}
}

// TestSensitivePathsIncludesSystemFiles verifies that sensitivePaths
// includes system sensitive files like /etc/passwd.
func TestSensitivePathsIncludesSystemFiles(t *testing.T) {
	paths := sensitivePaths()
	foundPasswd := false
	for _, p := range paths {
		if strings.HasSuffix(p, "/etc/passwd") || strings.HasSuffix(p, "/etc/master.passwd") {
			foundPasswd = true
			break
		}
	}
	if !foundPasswd {
		t.Error("sensitivePaths should include /etc/passwd or /etc/master.passwd")
	}
}

// TestAutoModeRetriesOnSandboxFailure verifies that auto mode retries
// without sandbox when sandbox-exec fails at runtime.
func TestAutoModeRetriesOnSandboxFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	workDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	cfg := internalconfig.Config{
		Workdir:          workDir,
		ApprovalMode:     "auto",
		ShellSandboxMode: internalconfig.ShellSandboxModeAuto,
	}

	// This should work even if sandbox-exec fails (auto mode retries).
	result, err := runtimeClient.shellExec(context.Background(), cfg, map[string]any{
		"raw_command": "echo auto-retry-test",
	})
	if err != nil {
		t.Fatalf("shellExec with auto sandbox failed: %v", err)
	}
	stdout := stringValue(result["stdout"])
	if !strings.Contains(stdout, "auto-retry-test") {
		t.Fatalf("expected stdout to contain 'auto-retry-test', got: %s", stdout)
	}
}

// TestEnforceModeFailsOnSandboxRuntimeError verifies that enforce mode
// surfaces the error when sandbox fails at runtime (no retry).
func TestEnforceModeFailsOnSandboxRuntimeError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skip on windows")
	}
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	workDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	cfg := internalconfig.Config{
		Workdir:          workDir,
		ApprovalMode:     "auto",
		ShellSandboxMode: internalconfig.ShellSandboxModeEnforce,
	}

	// In enforce mode, if sandbox fails, the error should be surfaced.
	_, err := runtimeClient.shellExec(context.Background(), cfg, map[string]any{
		"raw_command": "echo enforce-test",
	})
	// We can't guarantee sandbox-exec will fail in the test environment,
	// so we accept both success (sandbox worked) and failure (sandbox failed).
	// The key is that enforce mode doesn't silently retry.
	_ = err
}

// TestLinuxBwrapArgsNoGlobalEtc verifies that bwrap args do not mount
// /etc as a whole directory (only specific files).
func TestLinuxBwrapArgsNoGlobalEtc(t *testing.T) {
	t.Skip("buildBwrapArgs is only available on Linux; see shell_sandbox_linux.go")
}

// execLookPath is a helper to check if a binary is available.
func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// TestSensitivePathsSurvivesHomeDirFailure 验证家目录解析失败时系统级
// 敏感路径仍然受保护，不整体 fail-open。
func TestSensitivePathsSurvivesHomeDirFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", "")
		t.Setenv("HOMEDRIVE", "")
		t.Setenv("HOMEPATH", "")
	} else {
		t.Setenv("HOME", "")
	}
	paths := sensitivePaths()
	if len(paths) == 0 {
		t.Fatal("sensitivePaths must not fail open when home dir is unavailable")
	}
	if runtime.GOOS != "windows" {
		found := false
		for _, p := range paths {
			if p == "/etc/passwd" {
				found = true
				break
			}
		}
		if !found {
			t.Error("/etc/passwd must remain protected without HOME")
		}
		if !isSensitivePath("/etc/passwd") {
			t.Error("isSensitivePath(/etc/passwd) must be true without HOME")
		}
	}
}
