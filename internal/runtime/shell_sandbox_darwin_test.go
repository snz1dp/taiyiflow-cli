//go:build darwin

package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSeatbeltProfileGeneration verifies the generated Seatbelt profile
// contains expected rules with the deny-by-default write strategy.
func TestSeatbeltProfileGeneration(t *testing.T) {
	workDir := t.TempDir()
	extraRoot := t.TempDir()
	profile := generateSeatbeltProfile(workDir, []string{extraRoot})

	// Should contain version 1.
	if !strings.Contains(profile, "(version 1)") {
		t.Error("profile should contain (version 1)")
	}
	// Should contain allow default (for non-file operations).
	if !strings.Contains(profile, "(allow default)") {
		t.Error("profile should contain (allow default)")
	}
	// Should deny all writes globally.
	if !strings.Contains(profile, `(deny file-write* (subpath "/"))`) {
		t.Error("profile should deny all writes globally")
	}
	// Should allow writes to /dev/null.
	if !strings.Contains(profile, `(allow file-write* (literal "/dev/null"))`) {
		t.Error("profile should allow writes to /dev/null")
	}
	// Should allow writes to workdir.
	if !strings.Contains(profile, workDir) {
		t.Error("profile should contain workdir path")
	}
	// Should allow writes to extra root.
	if !strings.Contains(profile, extraRoot) {
		t.Error("profile should contain extra root path")
	}
	// Should deny reads to home directory.
	homeDir, _ := os.UserHomeDir()
	if !strings.Contains(profile, fmt.Sprintf(`(deny file-read* (subpath %q))`, homeDir)) {
		t.Error("profile should deny reads to home directory")
	}
	// Should deny access to sensitive paths.
	sshPath := filepath.Join(homeDir, ".ssh")
	if !strings.Contains(profile, sshPath) {
		t.Error("profile should deny .ssh path")
	}
	// Should deny access to /etc/passwd.
	if !strings.Contains(profile, "/etc/passwd") {
		t.Error("profile should deny /etc/passwd")
	}
}

// TestSeatbeltProfileBlocksWriteToTmp verifies that the Seatbelt profile
// denies writes to /tmp (which is not in the allowed roots on macOS).
func TestSeatbeltProfileBlocksWriteToTmp(t *testing.T) {
	workDir := t.TempDir()
	// Generate profile with only workdir (no /tmp in allow roots).
	profile := generateSeatbeltProfile(workDir, []string{})
	// The profile should deny all writes, then only allow workdir.
	// /tmp should NOT be in the allow list.
	if strings.Contains(profile, `(allow file-write* (subpath "/tmp"))`) ||
		strings.Contains(profile, `(allow file-write* (subpath "/private/tmp"))`) {
		t.Error("profile should not allow writes to /tmp when it's not in allow roots")
	}
}
