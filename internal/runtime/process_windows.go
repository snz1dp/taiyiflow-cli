//go:build windows

package runtime

import (
	"os"
	"os/exec"
	"strconv"
)

func configureProcessTree(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
		kill.Stdout = nil
		kill.Stderr = nil
		return kill.Run()
	}
}
