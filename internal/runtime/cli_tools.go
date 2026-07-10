package runtime

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	cliExeOnce sync.Once
	cliExePath string
	cliDir     string
)

// initCLIExe 解析当前进程可执行文件路径及其所在目录。
// 解析符号链接后返回真实路径。无法确定时留空。
func initCLIExe() {
	cliExeOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		resolved, err := filepath.EvalSymlinks(exe)
		if err != nil {
			resolved = exe
		}
		cliExePath = resolved
		cliDir = filepath.Dir(resolved)
	})
}

// cliExecutableDir 返回当前 CLI 二进制文件所在目录。
func cliExecutableDir() string {
	initCLIExe()
	return cliDir
}

// cliExecutablePath 返回当前进程可执行文件的绝对路径（解析符号链接后）。
func cliExecutablePath() string {
	initCLIExe()
	return cliExePath
}

// pythonSubDir 返回 python3.12 下的可执行文件子目录。
// Windows 下 Python 安装结构没有 bin 子目录，可执行文件直接位于 python3.12/ 中。
func pythonSubDir() string {
	if runtime.GOOS == "windows" {
		return "python3.12"
	}
	return filepath.Join("python3.12", "bin")
}

// nodeSubDir 返回 node22 下的可执行文件子目录。
// Windows 下 Node.js 安装结构没有 bin 子目录，可执行文件直接位于 node22/ 中。
func nodeSubDir() string {
	if runtime.GOOS == "windows" {
		return "node22"
	}
	return filepath.Join("node22", "bin")
}

// commandOverrideMap 定义需要特殊路由的命令名到解析函数的映射。
// python/node/rg 不在此映射中，由 PATH 解析（CLI 目录追加到 PATH 末尾作为回退）。
// 仅 taiyiflow-cli 需要特殊路由，始终指向进程自身可执行文件路径。
var commandOverrideMap = map[string]func(cliDir string) string{
	"taiyiflow-cli": func(_ string) string { return cliExecutablePath() },
}

// resolveCommandInCLIDir 根据命令名映射规则解析可执行文件路径。
//   - taiyiflow-cli             → 进程自身可执行文件路径
//   - 其他命令                   → 返回原始命令名，由 PATH 解析
//
// CLI 目录（含 python3.12/bin、node22/bin）已追加到 PATH 末尾，
// 操作系统内置工具优先被找到，CLI 捆绑工具仅在系统 PATH 中不存在时作为回退。
//
// 已是绝对路径或包含路径分隔符的命令不做覆盖。
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

	// 检查是否在特殊路由映射中（仅 taiyiflow-cli）
	if resolver, ok := commandOverrideMap[command]; ok {
		candidate := resolver(dir)
		// Windows: 尝试 .exe 扩展名
		if runtime.GOOS == "windows" && filepath.Ext(candidate) == "" {
			if isExecutableFile(candidate + ".exe") {
				return candidate + ".exe"
			}
		}
		if isExecutableFile(candidate) {
			return candidate
		}
		// taiyiflow-cli 始终返回自身路径（即使文件不存在，因为可能正在运行）
		if command == "taiyiflow-cli" {
			return candidate
		}
		return command
	}

	// 其他命令：返回原始命令名，由 PATH 解析。
	// CLI 目录已追加到 PATH 末尾，系统工具优先，CLI 捆绑工具作为回退。
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

// cliPathDirs 返回需要前置到 PATH 的目录列表，按优先级从高到低排列。
// 包含 python3.12/bin（Windows 下为 python3.12）、node22/bin（Windows 下为 node22）和 CLI 目录本身。
func cliPathDirs() []string {
	dir := cliExecutableDir()
	if dir == "" {
		return nil
	}
	return []string{
		filepath.Join(dir, pythonSubDir()),
		filepath.Join(dir, nodeSubDir()),
		dir,
	}
}

// appendCLIDirToPath 将 CLI 相关目录追加到 PATH 值末尾，
// 使操作系统内置工具优先被找到，CLI 捆绑工具作为回退。
func appendCLIDirToPath(currentPath string) string {
	dirs := cliPathDirs()
	if len(dirs) == 0 {
		return currentPath
	}
	parts := []string{}
	if currentPath != "" {
		parts = append(parts, currentPath)
	}
	parts = append(parts, dirs...)
	return strings.Join(parts, string(os.PathListSeparator))
}
