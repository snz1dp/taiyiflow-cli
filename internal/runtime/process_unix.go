//go:build !windows

package runtime

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// 组 SIGKILL 后自行 setpgid 逃逸的孙进程仍可能持有管道或处于不可中断态，
	// WaitDelay 兜底避免 Wait 无限等待。
	cmd.WaitDelay = 5 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

func assignProcessTree(*exec.Cmd) {}

func releaseProcessTree(*exec.Cmd) {}