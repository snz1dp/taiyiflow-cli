//go:build !windows

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRawCommandCancellationKillsProcessGroup(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	command, err := buildRawCommand(ctx, commandArguments{
		Mode:       "shell",
		RawCommand: fmt.Sprintf("sleep 30 & child=$!; printf '%%s' \"$child\" > %q; wait", pidPath),
	})
	if err != nil {
		t.Fatal(err)
	}
	if command.SysProcAttr == nil || !command.SysProcAttr.Setpgid {
		t.Fatal("raw command must start in an isolated process group")
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = command.Cancel()
		_, _ = command.Process.Wait()
	})
	deadline := time.Now().Add(2 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		content, readErr := os.ReadFile(pidPath)
		if readErr == nil && strings.TrimSpace(string(content)) != "" {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(content)))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("child process did not start")
	}
	cancel()
	if err := command.Wait(); err == nil {
		t.Fatal("expected cancelled process group to exit with an error")
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("child process %d survived process-group cancellation", childPID)
}
