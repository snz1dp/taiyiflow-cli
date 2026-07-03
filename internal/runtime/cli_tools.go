package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	cliDirOnce sync.Once
	cliDir     string
)

// cliExecutableDir 返回当前 CLI 二进制文件所在目录。
// 解析符号链接后返回真实路径。无法确定时返回空字符串。
func cliExecutableDir() string {
	cliDirOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		resolved, err := filepath.EvalSymlinks(exe)
		if err != nil {
			resolved = exe
		}
		cliDir = filepath.Dir(resolved)
	})
	return cliDir
}

// resolveCommandInCLIDir 检查给定命令名是否存在于 CLI 二进制文件所在目录中。
// 如果存在且可执行，返回绝对路径；否则返回原始命令名不变。
// 已是绝对路径的命令不做覆盖。
func resolveCommandInCLIDir(command string) string {
	dir := cliExecutableDir()
	if dir == "" || command == "" {
		return command
	}
	// 已是绝对路径的命令不做覆盖
	if filepath.IsAbs(command) {
		return command
	}
	// 包含路径分隔符的命令（相对路径）不做覆盖
	if strings.ContainsAny(command, `/\`) {
		return command
	}
	candidate := filepath.Join(dir, command)
	if runtime.GOOS == "windows" {
		// Windows: 如果没有扩展名，尝试 .exe
		if filepath.Ext(command) == "" {
			candidateExe := candidate + ".exe"
			if isExecutableFile(candidateExe) {
				return candidateExe
			}
		}
	}
	if isExecutableFile(candidate) {
		return candidate
	}
	return command
}

// isExecutableFile 检查路径是否为可执行文件（非目录）。
// 在非 Windows 平台额外检查可执行权限位。
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	// POSIX: 检查任意可执行权限位
	return info.Mode().Perm()&0o111 != 0
}

// prependCLIDirToPath 将 CLI 二进制文件所在目录前置到 PATH 值中，
// 使 shell 模式命令也能优先找到 CLI 目录中的工具。
func prependCLIDirToPath(currentPath string) string {
	dir := cliExecutableDir()
	if dir == "" {
		return currentPath
	}
	if currentPath == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + currentPath
}
