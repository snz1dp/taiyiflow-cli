package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// shellSandboxConfig carries sandbox settings resolved from Config.ShellSandboxMode.
type shellSandboxConfig struct {
	Mode       string   // off | auto | enforce
	WorkDir    string   // working directory (always allowed for read/write)
	AllowRoots []string // additional allowed roots (temp dir, sandbox_roots, skill dirs)
}

// buildSandboxedCommand wraps the original command with a platform-specific sandbox.
// On macOS it uses sandbox-exec (Seatbelt); on Linux it uses bwrap (bubblewrap);
// on Windows it falls back to running without an OS-level sandbox.
//
// In "auto" mode the function silently falls back to no sandbox when the
// platform tool is unavailable. In "enforce" mode it returns an error.
func buildSandboxedCommand(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	sb := payload.Sandbox
	if sb == nil || sb.Mode == internalconfig.ShellSandboxModeOff {
		return buildRawCommand(ctx, payload)
	}

	wrapped, err := buildSandboxedCommandPlatform(ctx, payload)
	if err != nil {
		if sb.Mode == internalconfig.ShellSandboxModeAuto {
			// Auto mode: fall back to no sandbox.
			return buildRawCommand(ctx, payload)
		}
		// Enforce mode: surface the error.
		return nil, fmt.Errorf("shell sandbox (enforce) 不可用: %w", err)
	}
	return wrapped, nil
}

// buildRawCommand creates the *exec.Cmd without any sandbox wrapping.
// This is the original buildCommand logic, extracted for reuse.
func buildRawCommand(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	if payload.Mode == "shell" {
		if runtime.GOOS == "windows" {
			return exec.CommandContext(ctx, "cmd", "/C", payload.RawCommand), nil
		}
		return exec.CommandContext(ctx, "sh", "-lc", payload.RawCommand), nil
	}
	// 结构化命令模式：返回命令名由 PATH 解析（CLI 目录已追加到 PATH 末尾）
	resolvedCommand := resolveCommandInCLIDir(payload.Command)
	return exec.CommandContext(ctx, resolvedCommand, payload.Args...), nil
}

// resolveShellSandbox resolves the sandbox configuration from Config.
// Returns nil when sandbox is disabled.
func resolveShellSandbox(config internalconfig.Config, workDir string) *shellSandboxConfig {
	mode := internalconfig.NormalizeShellSandboxMode(config.ShellSandboxMode)
	if mode == internalconfig.ShellSandboxModeOff {
		return nil
	}
	roots, err := resolveSandboxRoots(config, workDir)
	if err != nil {
		// If we can't resolve roots, still enable sandbox with just workdir + temp.
		roots = []string{workDir}
		if tempDir := strings.TrimSpace(os.TempDir()); tempDir != "" {
			roots = append(roots, tempDir)
		}
	}
	// Deduplicate and exclude the workdir (it's always in AllowRoots but we
	// also pass it separately for clarity in profile generation).
	allowRoots := make([]string, 0, len(roots))
	seen := map[string]bool{workDir: true}
	for _, root := range roots {
		cleaned := filepath.Clean(root)
		if !seen[cleaned] {
			seen[cleaned] = true
			allowRoots = append(allowRoots, cleaned)
		}
	}
	return &shellSandboxConfig{
		Mode:       mode,
		WorkDir:    workDir,
		AllowRoots: allowRoots,
	}
}

// sensitivePaths returns absolute paths that should always be denied
// regardless of sandbox roots.
func sensitivePaths() []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []string{
		filepath.Join(homeDir, ".ssh"),
		filepath.Join(homeDir, ".gnupg"),
		filepath.Join(homeDir, ".aws"),
		filepath.Join(homeDir, "Library", "Keychains"),
	}
}
