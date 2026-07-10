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

	// 创建子目录结构
	// Windows 下 python/node 可执行文件直接在 python3.12/ 和 node22/ 目录下，
	// 非 Windows 下在 python3.12/bin/ 和 node22/bin/ 下。
	pythonSubDir := filepath.Join(tmpDir, "python3.12")
	nodeSubDir := filepath.Join(tmpDir, "node22")
	if runtime.GOOS != "windows" {
		pythonSubDir = filepath.Join(pythonSubDir, "bin")
		nodeSubDir = filepath.Join(nodeSubDir, "bin")
	}
	if err := os.MkdirAll(pythonSubDir, 0o755); err != nil {
		t.Fatalf("创建 python 子目录失败: %v", err)
	}
	if err := os.MkdirAll(nodeSubDir, 0o755); err != nil {
		t.Fatalf("创建 node 子目录失败: %v", err)
	}

	// 创建测试用可执行文件
	createTestExe := func(path string) {
		if runtime.GOOS != "windows" {
			if err := os.WriteFile(path, []byte("#!/bin/sh\necho fake\n"), 0o755); err != nil {
				t.Fatalf("创建测试可执行文件 %s 失败: %v", path, err)
			}
		} else {
			if err := os.WriteFile(path+".exe", []byte("fake"), 0o755); err != nil {
				t.Fatalf("创建测试可执行文件 %s 失败: %v", path, err)
			}
		}
	}

	// python 子目录下的文件
	createTestExe(filepath.Join(pythonSubDir, "python"))
	createTestExe(filepath.Join(pythonSubDir, "python3"))
	createTestExe(filepath.Join(pythonSubDir, "python3.12"))
	// node 子目录下的文件
	createTestExe(filepath.Join(nodeSubDir, "node"))
	// CLI 根目录下的文件
	createTestExe(filepath.Join(tmpDir, "rg"))

	// 临时替换 cliDir（先触发 Once.Do 避免后续覆盖）
	cliExecutableDir()
	originalDir := cliDir
	originalExe := cliExePath
	cliDir = tmpDir
	cliExePath = filepath.Join(tmpDir, "taiyiflow-cli")
	defer func() {
		cliDir = originalDir
		cliExePath = originalExe
	}()

	// 测试 1: python → 返回原始命令名（由 PATH 解析，CLI 目录追加到 PATH 末尾）
	resolved := resolveCommandInCLIDir("python")
	expected := "python"
	if resolved != expected {
		t.Errorf("resolveCommandInCLIDir(\"python\") = %q, want %q", resolved, expected)
	}

	// 测试 2: python3 → 返回原始命令名
	resolved = resolveCommandInCLIDir("python3")
	expected = "python3"
	if resolved != expected {
		t.Errorf("resolveCommandInCLIDir(\"python3\") = %q, want %q", resolved, expected)
	}

	// 测试 3: python3.12 → 返回原始命令名
	resolved = resolveCommandInCLIDir("python3.12")
	expected = "python3.12"
	if resolved != expected {
		t.Errorf("resolveCommandInCLIDir(\"python3.12\") = %q, want %q", resolved, expected)
	}

	// 测试 4: node → 返回原始命令名
	resolved = resolveCommandInCLIDir("node")
	expected = "node"
	if resolved != expected {
		t.Errorf("resolveCommandInCLIDir(\"node\") = %q, want %q", resolved, expected)
	}

	// 测试 5: taiyiflow-cli → 进程自身路径
	resolved = resolveCommandInCLIDir("taiyiflow-cli")
	expected = filepath.Join(tmpDir, "taiyiflow-cli")
	if resolved != expected {
		t.Errorf("resolveCommandInCLIDir(\"taiyiflow-cli\") = %q, want %q", resolved, expected)
	}

	// 测试 6: rg → 返回原始命令名（由 PATH 解析）
	resolved = resolveCommandInCLIDir("rg")
	expected = "rg"
	if resolved != expected {
		t.Errorf("resolveCommandInCLIDir(\"rg\") = %q, want %q", resolved, expected)
	}

	// 测试 7: CLI 目录中不存在的命令应返回原始名称
	cmd := "nonexistent-tool-xyz"
	resolved = resolveCommandInCLIDir(cmd)
	if resolved != cmd {
		t.Errorf("resolveCommandInCLIDir(%q) = %q, want %q", cmd, resolved, cmd)
	}

	// 测试 8: 映射命令文件不存在时回退到系统默认
	// python3.13 不在 override map 中，应返回原始命令名
	resolved = resolveCommandInCLIDir("python3.13")
	if resolved != "python3.13" {
		t.Errorf("resolveCommandInCLIDir(\"python3.13\") = %q, want %q", resolved, "python3.13")
	}

	// 测试 9: rg 文件存在时也返回原始命令名（由 PATH 解析）
	rgPath := filepath.Join(tmpDir, "rg")
	if runtime.GOOS != "windows" {
		_ = os.Remove(rgPath)
	} else {
		_ = os.Remove(rgPath + ".exe")
	}
	resolved = resolveCommandInCLIDir("rg")
	if resolved != "rg" {
		t.Errorf("resolveCommandInCLIDir(\"rg\") = %q, want %q", resolved, "rg")
	}

	// 测试 10: 绝对路径不做覆盖
	absPath := "/usr/bin/python"
	if runtime.GOOS == "windows" {
		absPath = `C:\Python\python.exe`
	}
	resolved = resolveCommandInCLIDir(absPath)
	if resolved != absPath {
		t.Errorf("resolveCommandInCLIDir(绝对路径) = %q, want %q", resolved, absPath)
	}

	// 测试 11: 空命令返回空
	resolved = resolveCommandInCLIDir("")
	if resolved != "" {
		t.Errorf("resolveCommandInCLIDir(\"\") = %q, want \"\"", resolved)
	}
}

func TestAppendCLIDirToPath(t *testing.T) {
	originalDir := cliDir
	cliDir = "/fake/cli/dir"
	defer func() { cliDir = originalDir }()

	// 根据平台确定期望的子目录
	pythonDir := filepath.Join("/fake/cli/dir", pythonSubDir())
	nodeDir := filepath.Join("/fake/cli/dir", nodeSubDir())

	// 测试有 PATH 的情况：应追加 python 子目录、node 子目录和 CLI 目录到末尾
	result := appendCLIDirToPath("/usr/bin:/bin")
	expected := "/usr/bin:/bin" + string(os.PathListSeparator) +
		pythonDir + string(os.PathListSeparator) +
		nodeDir + string(os.PathListSeparator) +
		"/fake/cli/dir"
	if result != expected {
		t.Errorf("appendCLIDirToPath(\"/usr/bin:/bin\") = %q, want %q", result, expected)
	}

	// 测试空 PATH 的情况
	result = appendCLIDirToPath("")
	expected = pythonDir + string(os.PathListSeparator) +
		nodeDir + string(os.PathListSeparator) +
		"/fake/cli/dir"
	if result != expected {
		t.Errorf("appendCLIDirToPath(\"\") = %q, want %q", result, expected)
	}

	// 测试 cliDir 为空的情况
	cliDir = ""
	result = appendCLIDirToPath("/usr/bin")
	if result != "/usr/bin" {
		t.Errorf("appendCLIDirToPath with empty cliDir = %q, want %q", result, "/usr/bin")
	}
}

func TestAppendCLIDirToPathInEnv(t *testing.T) {
	originalDir := cliDir
	cliDir = "/fake/cli/dir"
	defer func() { cliDir = originalDir }()

	pythonDir := filepath.Join("/fake/cli/dir", pythonSubDir())
	nodeDir := filepath.Join("/fake/cli/dir", nodeSubDir())
	expectedPath := "/usr/bin:/bin" + string(os.PathListSeparator) +
		pythonDir + string(os.PathListSeparator) +
		nodeDir + string(os.PathListSeparator) +
		"/fake/cli/dir"

	// 测试 PATH 键存在
	envMap := map[string]string{"PATH": "/usr/bin:/bin", "HOME": "/tmp"}
	appendCLIDirToPathInEnv(envMap)
	if envMap["PATH"] != expectedPath {
		t.Errorf("PATH = %q, want %q", envMap["PATH"], expectedPath)
	}

	// 测试 Windows 风格的 Path 键
	envMap = map[string]string{"Path": "/usr/bin"}
	appendCLIDirToPathInEnv(envMap)
	// 应该找到 "Path" 键并修改它
	if envMap["Path"] != "/usr/bin"+string(os.PathListSeparator)+
		pythonDir+string(os.PathListSeparator)+
		nodeDir+string(os.PathListSeparator)+
		"/fake/cli/dir" {
		t.Errorf("Path = %q", envMap["Path"])
	}
	if _, hasUpper := envMap["PATH"]; hasUpper {
		t.Error("不应创建额外的 PATH 键")
	}

	// 测试 PATH 不存在时自动创建
	envMap = map[string]string{"HOME": "/tmp"}
	appendCLIDirToPathInEnv(envMap)
	if envMap["PATH"] != pythonDir+string(os.PathListSeparator)+
		nodeDir+string(os.PathListSeparator)+
		"/fake/cli/dir" {
		t.Errorf("PATH = %q, want %q", envMap["PATH"], pythonDir+string(os.PathListSeparator)+"...")
	}
}
