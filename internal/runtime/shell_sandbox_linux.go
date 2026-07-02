//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// buildSandboxedCommandPlatform wraps the command with Linux bwrap (bubblewrap).
//
// Strategy: create a restricted mount namespace where system paths are
// read-only bind-mounted, the workdir and sandbox roots are read-write
// bind-mounted, and everything else is invisible. Network is shared
// with the host so commands like curl/git still work.
func buildSandboxedCommandPlatform(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("bwrap not found in PATH: %w", err)
	}

	sb := payload.Sandbox
	args := buildBwrapArgs(sb.WorkDir, sb.AllowRoots)

	// Append the inner command.
	if payload.Mode == "shell" {
		args = append(args, "sh", "-lc", payload.RawCommand)
	} else {
		args = append(args, payload.Command)
		args = append(args, payload.Args...)
	}

	return exec.CommandContext(ctx, bwrapPath, args...), nil
}

// buildBwrapArgs constructs the bwrap argument list for sandboxing.
func buildBwrapArgs(workDir string, allowRoots []string) []string {
	var args []string

	// Read-only system paths needed for command execution.
	roPaths := []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc"}
	for _, p := range roPaths {
		if pathExists(p) {
			args = append(args, "--ro-bind", p, p)
		}
	}

	// /dev and /proc for process management.
	args = append(args, "--dev", "/dev")
	args = append(args, "--proc", "/proc")

	// Read-write paths: workdir and sandbox roots.
	seen := map[string]bool{}
	if workDir != "" {
		args = append(args, "--bind", workDir, workDir)
		seen[workDir] = true
	}
	for _, root := range allowRoots {
		if seen[root] {
			continue
		}
		args = append(args, "--bind", root, root)
		seen[root] = true
	}

	// Unshare all namespaces (pid, ipc, uts, user) but keep network shared.
	args = append(args, "--unshare-all", "--share-net")

	return args
}

func pathExists(p string) bool {
	_, err := exec.LookPath(p)
	// LookPath searches PATH; for absolute paths we need stat instead.
	if strings.HasPrefix(p, "/") {
		return true // Assume system paths exist; bwrap will error if not.
	}
	return err == nil
}
