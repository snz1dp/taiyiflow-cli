package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveCommandInCLIDir(t *testing.T) {
	// 创建临时目录模拟 CLI 目录
	tmpDir := t.TempDir()

	// 创建一个假的 "python" 可执行文件
	pythonPath := filepath.Join(tmpDir, "python")
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(pythonPath, []byte("#!/bin/sh\necho fake-python\n"), 0o755); err != nil {
			t.Fatalf("创建测试可执行文件失败: %v", err)
		}
	} else {
		pythonPath = filepath.Join(tmpDir, "python.exe")
		if err := os.WriteFile(pythonPath, []byte("fake"), 0o755); err != nil {
			t.Fatalf("创建测试可执行文件失败: %v", err)
		}
	}

	// 临时替换 cliDir（先触发 Once.Do 避免后续覆盖）
	cliExecutableDir()
	original := cliDir
	cliDir = tmpDir
	defer func() { cliDir = original }()

	// 测试 1: CLI 目录中存在的命令应返回绝对路径
	cmd := "python"
	resolved := resolveCommandInCLIDir(cmd)
	expected := pythonPath
	if runtime.GOOS != "windows" {
		if resolved != expected {
			t.Errorf("resolveCommandInCLIDir(%q) = %q, want %q", cmd, resolved, expected)
		}
	} else {
		// Windows: resolveCommandInCLIDir 应返回带 .exe 的路径
		if resolved != expected {
			t.Errorf("resolveCommandInCLIDir(%q) = %q, want %q", cmd, resolved, expected)
		}
	}

	// 测试 2: CLI 目录中不存在的命令应返回原始名称
	cmd = "nonexistent-tool-xyz"
	resolved = resolveCommandInCLIDir(cmd)
	if resolved != cmd {
		t.Errorf("resolveCommandInCLIDir(%q) = %q, want %q", cmd, resolved, cmd)
	}

	// 测试 3: 绝对路径不做覆盖
	absPath := "/usr/bin/python"
	if runtime.GOOS == "windows" {
		absPath = `C:\Python\python.exe`
	}
	resolved = resolveCommandInCLIDir(absPath)
	if resolved != absPath {
		t.Errorf("resolveCommandInCLIDir(绝对路径) = %q, want %q", resolved, absPath)
	}

	// 测试 4: 空命令返回空
	resolved = resolveCommandInCLIDir("")
	if resolved != "" {
		t.Errorf("resolveCommandInCLIDir(\"\") = %q, want \"\"", resolved)
	}
}

func TestPrependCLIDirToPath(t *testing.T) {
	original := cliDir
	cliDir = "/fake/cli/dir"
	defer func() { cliDir = original }()

	// 测试有 PATH 的情况
	result := prependCLIDirToPath("/usr/bin:/bin")
	expected := "/fake/cli/dir" + string(os.PathListSeparator) + "/usr/bin:/bin"
	if result != expected {
		t.Errorf("prependCLIDirToPath(\"/usr/bin:/bin\") = %q, want %q", result, expected)
	}

	// 测试空 PATH 的情况
	result = prependCLIDirToPath("")
	if result != "/fake/cli/dir" {
		t.Errorf("prependCLIDirToPath(\"\") = %q, want %q", result, "/fake/cli/dir")
	}

	// 测试 cliDir 为空的情况
	cliDir = ""
	result = prependCLIDirToPath("/usr/bin")
	if result != "/usr/bin" {
		t.Errorf("prependCLIDirToPath with empty cliDir = %q, want %q", result, "/usr/bin")
	}
}

func TestPrependCLIDirToPathInEnv(t *testing.T) {
	original := cliDir
	cliDir = "/fake/cli/dir"
	defer func() { cliDir = original }()

	// 测试 PATH 键存在
	envMap := map[string]string{"PATH": "/usr/bin:/bin", "HOME": "/tmp"}
	prependCLIDirToPathInEnv(envMap)
	expected := "/fake/cli/dir" + string(os.PathListSeparator) + "/usr/bin:/bin"
	if envMap["PATH"] != expected {
		t.Errorf("PATH = %q, want %q", envMap["PATH"], expected)
	}

	// 测试 Windows 风格的 Path 键
	envMap = map[string]string{"Path": "/usr/bin"}
	prependCLIDirToPathInEnv(envMap)
	// 应该找到 "Path" 键并修改它
	if envMap["Path"] != "/fake/cli/dir"+string(os.PathListSeparator)+"/usr/bin" {
		t.Errorf("Path = %q", envMap["Path"])
	}
	if _, hasUpper := envMap["PATH"]; hasUpper {
		t.Error("不应创建额外的 PATH 键")
	}

	// 测试 PATH 不存在时自动创建
	envMap = map[string]string{"HOME": "/tmp"}
	prependCLIDirToPathInEnv(envMap)
	if envMap["PATH"] != "/fake/cli/dir" {
		t.Errorf("PATH = %q, want %q", envMap["PATH"], "/fake/cli/dir")
	}
}
