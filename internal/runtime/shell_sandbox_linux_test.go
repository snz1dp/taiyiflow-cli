//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func argIndex(args []string, pair ...string) int {
	for i := 0; i+len(pair) <= len(args); i++ {
		match := true
		for j, p := range pair {
			if args[i+j] != p {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// TestBuildBwrapArgsMasksSensitivePaths 验证绑定根内的敏感目录被 tmpfs
// 覆盖、敏感文件被 /dev/null 只读覆盖，且覆盖规则位于绑定之后。
func TestBuildBwrapArgsMasksSensitivePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("export SECRET=1"), 0o600); err != nil {
		t.Fatal(err)
	}

	args := buildBwrapArgs(home, nil)
	bindIdx := argIndex(args, "--bind", home, home)
	if bindIdx < 0 {
		t.Fatalf("expected --bind %s, got %v", home, args)
	}
	tmpfsIdx := argIndex(args, "--tmpfs", sshDir)
	if tmpfsIdx < 0 {
		t.Fatalf("expected --tmpfs %s, got %v", sshDir, args)
	}
	nullIdx := argIndex(args, "--ro-bind", "/dev/null", zshrc)
	if nullIdx < 0 {
		t.Fatalf("expected --ro-bind /dev/null %s, got %v", zshrc, args)
	}
	if tmpfsIdx < bindIdx || nullIdx < bindIdx {
		t.Fatalf("sensitive overlays must come after root bind: %v", args)
	}
}

// TestBuildBwrapArgsSkipsRootInsideSensitivePath 验证根本身位于敏感路径
// 内时跳过绑定（fail-closed）。
func TestBuildBwrapArgsSkipsRootInsideSensitivePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	inside := filepath.Join(home, ".ssh", "project")
	args := buildBwrapArgs(inside, nil)
	if argIndex(args, "--bind", inside, inside) >= 0 {
		t.Fatalf("root inside sensitive path must not be bound: %v", args)
	}
}

// TestBuildBwrapArgsNoOverlayOutsideRoot 验证绑定根之外的敏感路径不产生
// 覆盖规则（命名空间内本就不可见，避免在宿主创建挂载点）。
func TestBuildBwrapArgsNoOverlayOutsideRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	workDir := t.TempDir()
	args := buildBwrapArgs(workDir, nil)
	for _, a := range args {
		if strings.HasPrefix(a, home) {
			t.Fatalf("unexpected reference to home path %q in %v", a, args)
		}
	}
}
