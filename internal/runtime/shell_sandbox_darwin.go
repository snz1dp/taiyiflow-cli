//go:build darwin

package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// buildSandboxedCommandPlatform wraps the command with macOS sandbox-exec (Seatbelt).
//
// Profile strategy (rules are evaluated in order, last match wins):
//  1. (allow default) – allow everything
//  2. (deny file-write* (subpath "<homedir>")) – deny writes to home dir
//  3. (allow file-write* (subpath "<workdir>")) – re-allow workdir
//  4. (allow file-write* (subpath "<root>")) – re-allow each sandbox root
//  5. (deny file-read* file-write* (subpath "<sensitive>")) – deny sensitive paths
//
// This allows reads to system paths (/usr, /bin, etc.) while restricting
// writes to only the workdir and sandbox roots, and blocking all access
// to sensitive paths like ~/.ssh, ~/.gnupg, ~/.aws.
func buildSandboxedCommandPlatform(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		return nil, fmt.Errorf("sandbox-exec not found in PATH: %w", err)
	}

	sb := payload.Sandbox
	profile := generateSeatbeltProfile(sb.WorkDir, sb.AllowRoots)

	// Build the original command args that will run inside the sandbox.
	var innerArgs []string
	if payload.Mode == "shell" {
		innerArgs = []string{"sh", "-lc", payload.RawCommand}
	} else {
		// 命令名由 PATH 解析（CLI 目录已追加到 PATH 末尾）
		innerArgs = append([]string{resolveCommandInCLIDir(payload.Command)}, payload.Args...)
	}

	// sandbox-exec -p '<profile>' -- <inner command...>
	args := []string{"-p", profile, "--"}
	args = append(args, innerArgs...)

	return exec.CommandContext(ctx, "sandbox-exec", args...), nil
}

// generateSeatbeltProfile builds a Seatbelt profile string.
func generateSeatbeltProfile(workDir string, allowRoots []string) string {
	var b strings.Builder
	b.WriteString("(version 1)")
	b.WriteString("(allow default)")

	// Deny writes to home directory, then re-allow workdir and sandbox roots.
	// Rules are evaluated in order; later matching rules override earlier ones.
	homeDir := homeDirectory()
	if homeDir != "" {
		b.WriteString(fmt.Sprintf("(deny file-write* (subpath %q))", homeDir))
	}
	if workDir != "" {
		b.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))", workDir))
	}
	for _, root := range allowRoots {
		if root == workDir {
			continue
		}
		b.WriteString(fmt.Sprintf("(allow file-write* (subpath %q))", root))
	}

	// Deny all access (read + write) to sensitive paths.
	// These come after the allow rules so they take precedence.
	for _, p := range sensitivePaths() {
		b.WriteString(fmt.Sprintf("(deny file-read* file-write* (subpath %q))", p))
	}

	return b.String()
}

func homeDirectory() string {
	// Use os.UserHomeDir via the existing expandTildePath helper logic.
	expanded, err := expandTildePath("~")
	if err != nil {
		return ""
	}
	return expanded
}
