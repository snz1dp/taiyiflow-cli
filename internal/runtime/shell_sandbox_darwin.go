//go:build darwin

package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// buildSandboxedCommandPlatform wraps the command with macOS sandbox-exec (Seatbelt).
//
// Profile strategy (deny-by-default for file writes, restricted reads):
//  1. (allow default) – allow all non-file operations (process, network, IPC, etc.)
//  2. (deny file-write* (subpath "/")) – deny ALL file writes globally
//  3. (allow file-write* (literal "/dev/null")) – allow writes to /dev/null for redirects
//  4. (allow file-write* (subpath "<workdir>")) – allow writes to workdir
//  5. (allow file-write* (subpath "<root>")) – allow writes to each sandbox root
//  6. (deny file-read* (subpath "<homedir>")) – deny reads to home directory
//  7. (allow file-read* (subpath "<workdir>")) – re-allow reads to workdir
//  8. (allow file-read* (subpath "<root>")) – re-allow reads to sandbox roots
//  9. (deny file-read* file-write* (subpath "<sensitive>")) – deny sensitive paths
//
// This blocks:
//   - Writes to /tmp, /etc, and any path outside allowed roots
//   - Reads to home directory (except workdir and sandbox roots within it)
//   - Reads and writes to sensitive paths (~/.ssh, ~/.gnupg, /etc/passwd, etc.)
//   - Reads to system files like /etc/passwd, /etc/master.passwd, /etc/sudoers
//
// System binary paths (/usr, /bin, /sbin, /System, /Library) remain readable
// for command execution. Network and process operations remain allowed.
func buildSandboxedCommandPlatform(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return nil, fmt.Errorf("sandbox-exec not found in PATH: %w", err)
	}

	sb := payload.Sandbox
	profile := generateSeatbeltProfile(sb.WorkDir, sb.AllowRoots)

	// Build the original command args that will run inside the sandbox.
	var innerArgs []string
	if payload.Mode == "shell" {
		innerArgs = []string{"sh", "-c", payload.RawCommand}
	} else {
		// 命令名由 PATH 解析（CLI 目录已追加到 PATH 末尾）
		innerArgs = append([]string{resolveCommandInCLIDir(payload.Command)}, payload.Args...)
	}

	// sandbox-exec -p '<profile>' -- <inner command...>
	args := []string{"-p", profile, "--"}
	args = append(args, innerArgs...)

	return exec.CommandContext(ctx, "sandbox-exec", args...), nil
}

// generateSeatbeltProfile builds a Seatbelt profile string with deny-by-default
// for file writes and restricted reads to the home directory.
func generateSeatbeltProfile(workDir string, allowRoots []string) string {
	var b strings.Builder
	b.WriteString("(version 1)")
	// Allow all non-file operations (process, network, IPC, etc.)
	b.WriteString("(allow default)")

	// --- Write restrictions: deny all, then re-allow specific paths ---

	// Deny all file writes globally.
	b.WriteString(`(deny file-write* (subpath "/"))`)

	// Allow writes to /dev/null for shell redirects (2>/dev/null, >/dev/null).
	b.WriteString(`(allow file-write* (literal "/dev/null"))`)

	// Re-allow writes to workdir.
	if workDir != "" {
		b.WriteString(fmt.Sprintf(`(allow file-write* (subpath %q))`, workDir))
	}

	// Re-allow writes to each sandbox root (includes temp dir, sandbox_roots, skill dirs).
	for _, root := range allowRoots {
		if root == workDir {
			continue
		}
		b.WriteString(fmt.Sprintf(`(allow file-write* (subpath %q))`, root))
	}

	// --- Read restrictions: deny home dir reads, then re-allow allowed paths ---

	// Deny reads to home directory to prevent shell commands from reading
	// user files outside the allowed roots (e.g., ~/.zshrc, ~/.bash_history).
	homeDir := homeDirectory()
	if homeDir != "" {
		b.WriteString(fmt.Sprintf(`(deny file-read* (subpath %q))`, homeDir))
	}

	// Re-allow reads to workdir (may be inside home dir).
	if workDir != "" {
		b.WriteString(fmt.Sprintf(`(allow file-read* (subpath %q))`, workDir))
	}

	// Re-allow reads to sandbox roots (may be inside home dir).
	for _, root := range allowRoots {
		if root == workDir {
			continue
		}
		b.WriteString(fmt.Sprintf(`(allow file-read* (subpath %q))`, root))
	}

	// --- Sensitive path denies: always deny, placed last to take precedence ---

	// Deny all access (read + write) to sensitive paths.
	// These come after the allow rules so they take precedence.
	for _, p := range sensitivePaths() {
		b.WriteString(fmt.Sprintf(`(deny file-read* file-write* (subpath %q))`, p))
	}

	return b.String()
}

func homeDirectory() string {
	// Use os.UserHomeDir for reliability; fall back to expandTildePath.
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	expanded, err := expandTildePath("~")
	if err != nil {
		return ""
	}
	return expanded
}
