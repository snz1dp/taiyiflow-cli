package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// runtimeToolProbe 定义单个运行时工具的探测配置。
type runtimeToolProbe struct {
	label      string   // 环境提示词中显示的工具名
	candidates []string // 按优先级尝试的命令名
	versionArg string   // 获取版本的参数
}

// probedRuntimeTools 列出需要探测的运行时工具。
var probedRuntimeTools = []runtimeToolProbe{
	{label: "node22", candidates: []string{"node22", "node"}, versionArg: "--version"},
	{label: "python3.12", candidates: []string{"python3.12", "python3", "python"}, versionArg: "--version"},
	{label: "rg", candidates: []string{"rg"}, versionArg: "--version"},
}

// runtimeDetectTimeout 是单次工具探测的超时时间。
const runtimeDetectTimeout = 3 * time.Second

// runtimeToolResult 表示单个工具的探测结果。
type runtimeToolResult struct {
	label   string
	command string
	version string
}

// 全局缓存，CLI 进程生命周期内只探测一次。
var (
	runtimeToolsCache     []runtimeToolResult
	runtimeToolsCacheOnce sync.Once
)

// detectLocalRuntimeTools 探测当前环境中可用的运行时工具。
// 探测顺序：CLI 可执行文件所在目录 → PATH 查找。
// 结果在进程生命周期内缓存。
func detectLocalRuntimeTools() []runtimeToolResult {
	runtimeToolsCacheOnce.Do(func() {
		runtimeToolsCache = probeRuntimeTools()
	})
	return runtimeToolsCache
}

// toolDisplayName 返回工具的显示名称，优先使用实际命中的命令名（basename），
// 命令路径为空时回退到 probe label。
func toolDisplayName(r runtimeToolResult) string {
	if r.command != "" {
		return filepath.Base(r.command)
	}
	return r.label
}

// detectLocalRuntimeToolsSummary 返回可读的工具摘要字符串，用于 doctor 输出。
func detectLocalRuntimeToolsSummary() string {
	results := detectLocalRuntimeTools()
	if len(results) == 0 {
		return "未检测到"
	}
	parts := make([]string, 0, len(results))
	for _, r := range results {
		name := toolDisplayName(r)
		if r.version != "" {
			parts = append(parts, fmt.Sprintf("%s (%s)", name, r.version))
		} else {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}

// detectLocalRuntimeToolsInline 返回适合嵌入环境提示词的紧凑字符串。
func detectLocalRuntimeToolsInline() string {
	results := detectLocalRuntimeTools()
	if len(results) == 0 {
		return ""
	}
	parts := make([]string, 0, len(results))
	for _, r := range results {
		name := toolDisplayName(r)
		if r.version != "" {
			parts = append(parts, name+" "+r.version)
		} else {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, ", ")
}

// probeRuntimeTools 执行实际探测逻辑，返回找到的工具列表。
func probeRuntimeTools() []runtimeToolResult {
	cliDir := resolveCLIExecutableDir()
	var results []runtimeToolResult
	for _, probe := range probedRuntimeTools {
		if r := probeOneTool(probe, cliDir); r != nil {
			results = append(results, *r)
		}
	}
	return results
}

// probeOneTool 探测单个工具，依次尝试候选命令名。
// 先在 CLI 可执行文件同目录查找，再通过 PATH 查找。
func probeOneTool(probe runtimeToolProbe, cliDir string) *runtimeToolResult {
	for _, cmdName := range probe.candidates {
		// 1. 优先在 CLI 同目录查找
		if cliDir != "" {
			fullPath := filepath.Join(cliDir, cmdName)
			if isExecutable(fullPath) {
				version := probeVersion(fullPath, probe.versionArg)
				return &runtimeToolResult{label: probe.label, command: fullPath, version: version}
			}
		}
		// 2. 通过 PATH 查找
		if path, err := exec.LookPath(cmdName); err == nil && path != "" {
			version := probeVersion(path, probe.versionArg)
			return &runtimeToolResult{label: probe.label, command: path, version: version}
		}
	}
	return nil
}

// probeVersion 执行命令获取版本信息，返回第一行的 trimmed 内容。
// 超时或失败时返回空字符串。
func probeVersion(execPath string, versionArg string) string {
	if versionArg == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), runtimeDetectTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, execPath, versionArg)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[0])
}

// resolveCLIExecutableDir 返回当前 CLI 可执行文件所在目录。
func resolveCLIExecutableDir() string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(exePath)
	if err != nil {
		resolved = exePath
	}
	return filepath.Dir(resolved)
}

// isExecutable 检查路径是否存在且为可执行文件。
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	// Windows 上不检查可执行位
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}
