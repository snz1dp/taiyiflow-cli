//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// buildSandboxedCommandPlatform wraps the command with Linux bwrap (bubblewrap).
//
// Strategy: create a restricted mount namespace where:
//   - System binary/library paths are read-only bind-mounted
//   - Specific /etc files (not the whole directory) are read-only mounted
//   - The workdir and sandbox roots are read-write bind-mounted
//   - Everything else is invisible (not mounted)
//   - Network is shared with the host so curl/git still work
//
// By mounting only specific /etc files instead of the whole /etc directory,
// we prevent shell commands from reading /etc/passwd, /etc/shadow, etc.
func buildSandboxedCommandPlatform(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("bwrap not found in PATH: %w", err)
	}

	sb := payload.Sandbox
	args := buildBwrapArgs(sb.WorkDir, sb.AllowRoots)

	// Append the inner command.
	if payload.Mode == "shell" {
		args = append(args, "sh", "-c", payload.RawCommand)
	} else {
		// 命令名由 PATH 解析（CLI 目录已追加到 PATH 末尾）
		args = append(args, resolveCommandInCLIDir(payload.Command))
		args = append(args, payload.Args...)
	}

	return exec.CommandContext(ctx, bwrapPath, args...), nil
}

// buildBwrapArgs constructs the bwrap argument list for sandboxing.
//
// Mount strategy:
//   - /usr, /bin, /sbin, /lib, /lib64: read-only (system binaries and libraries)
//   - Specific /etc files: read-only (only essential config files)
//   - /dev, /proc: virtual filesystems for process management
//   - workdir + allowRoots: read-write
//   - Sensitive paths inside bound roots: masked (empty tmpfs / read-only /dev/null)
//   - Everything else: not visible (not mounted)
func buildBwrapArgs(workDir string, allowRoots []string) []string {
	var args []string

	// Read-only system paths needed for command execution.
	// Note: /etc is NOT mounted as a whole to prevent reading /etc/passwd,
	// /etc/shadow, /etc/sudoers, etc. Only specific /etc files are mounted.
	roPaths := []string{"/usr", "/bin", "/sbin", "/lib", "/lib64"}
	for _, p := range roPaths {
		if pathExists(p) {
			args = append(args, "--ro-bind", p, p)
		}
	}

	// Mount specific /etc files/dirs needed for system operation.
	// This is more restrictive than mounting all of /etc.
	etcFiles := []string{
		"/etc/hosts",
		"/etc/resolv.conf",
		"/etc/nsswitch.conf",
		"/etc/localtime",
		"/etc/ld.so.cache",
		"/etc/ld.so.conf",
		"/etc/mime.types",
		"/etc/ssl/certs",
		"/etc/ssl/openssl.cnf",
		"/etc/terminfo",
		"/etc/termcap",
	}
	for _, f := range etcFiles {
		if pathExists(f) {
			args = append(args, "--ro-bind", f, f)
		}
	}

	// /dev and /proc for process management.
	args = append(args, "--dev", "/dev")
	args = append(args, "--proc", "/proc")

	// Read-write paths: workdir and sandbox roots.
	// 敏感路径与 darwin profile 的 deny 规则对齐：绑定根内的敏感目录用空
	// tmpfs 覆盖、敏感文件用 /dev/null 只读覆盖；根本身位于敏感路径内则
	// 跳过绑定（fail-closed）。仅覆盖宿主上真实存在的路径，避免 bwrap
	// 在宿主目录中创建挂载点。
	sensitive := sensitivePaths()
	overlaid := map[string]bool{}
	seen := map[string]bool{}
	bindRoot := func(root string) {
		if seen[root] {
			return
		}
		seen[root] = true
		for _, sp := range sensitive {
			if isWithinRoot(root, sp) {
				return
			}
		}
		args = append(args, "--bind", root, root)
		for _, sp := range sensitive {
			if overlaid[sp] || !isWithinRoot(sp, root) || !pathExists(sp) {
				continue
			}
			overlaid[sp] = true
			if info, err := os.Stat(sp); err == nil && info.IsDir() {
				args = append(args, "--tmpfs", sp)
			} else {
				args = append(args, "--ro-bind", "/dev/null", sp)
			}
		}
	}
	if workDir != "" {
		bindRoot(workDir)
	}
	for _, root := range allowRoots {
		bindRoot(root)
	}

	// Unshare all namespaces (pid, ipc, uts, user) but keep network shared.
	args = append(args, "--unshare-all", "--share-net")

	return args
}

// pathExists checks if a path exists on the filesystem.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
