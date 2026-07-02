//go:build windows

package runtime

import (
	"context"
	"fmt"
	"os/exec"
)

// buildSandboxedCommandPlatform on Windows falls back to no OS-level sandbox.
// Windows AppContainer/Job Object integration would require significant
// platform-specific code; for now we surface an error so that enforce mode
// fails clearly and auto mode falls back gracefully.
func buildSandboxedCommandPlatform(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	return nil, fmt.Errorf("Windows 平台暂不支持 OS 级 shell 沙盒")
}
