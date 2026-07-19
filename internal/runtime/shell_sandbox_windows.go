//go:build windows

package runtime

import (
	"context"
	"fmt"
	"os/exec"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// buildSandboxedCommandPlatform on Windows falls back to no OS-level sandbox.
//
// Windows AppContainer/Job Object integration would require significant
// platform-specific code. For now:
//   - In "enforce" mode: surface a clear error so users know the sandbox
//     is not available on Windows.
//   - In "auto" mode: the caller (buildSandboxedCommand) silently falls
//     back to running without an OS-level sandbox.
//
// Security on Windows relies on the application-level path checking
// (resolveAllowedPath) for file tools (read_file, write_file, file_edit),
// and on guardDangerousCommand for shell_exec. Shell commands on Windows
// are NOT restricted by OS-level file access controls; users who need
// stronger isolation should consider running inside a Windows container
// or VM.
func buildSandboxedCommandPlatform(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	sb := payload.Sandbox
	mode := internalconfig.ShellSandboxModeAuto
	if sb != nil {
		mode = sb.Mode
	}
	return nil, fmt.Errorf(
		"Windows 平台暂不支持 OS 级 shell 沙盒（当前模式: %s）；"+
			"enforce 模式将拒绝执行，auto 模式将降级为无沙盒执行；"+
			"文件类工具（read_file/write_file/file_edit）仍受应用级路径检查保护",
		mode,
	)
}
