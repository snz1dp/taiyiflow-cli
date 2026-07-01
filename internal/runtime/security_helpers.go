package runtime

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	reader := bufio.NewReader(r.stdin)
	answerLine, err := reader.ReadString('\n')
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
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	blocked := []string{
		filepath.Join(homeDir, ".ssh"),
		filepath.Join(homeDir, ".gnupg"),
		filepath.Join(homeDir, ".aws"),
		filepath.Join(homeDir, "Library", "Keychains"),
	}
	for _, item := range blocked {
		if isWithinRoot(target, item) || target == item {
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
	if action, matched := evaluatePermissionAction(config, "shell_exec", payload.RawCommand+" "+payload.Command+" "+strings.Join(payload.Args, " ")); matched {
		switch action {
		case "deny":
			return newToolError("PERMISSION_DENIED", "命中权限规则，拒绝执行")
		case "allow":
			return nil
		}
	}
	preview := strings.ToLower(strings.TrimSpace(payload.RawCommand + " " + payload.Command + " " + strings.Join(payload.Args, " ")))
	highRiskPatterns := []string{"rm -rf /", "mkfs", "shutdown", "reboot", "dd if=", "killall", "sudo "}
	for _, pattern := range highRiskPatterns {
		if strings.Contains(preview, pattern) && strings.TrimSpace(config.ApprovalMode) == "auto" {
			return newToolError("PERMISSION_DENIED", "检测到高风险命令，请切换到允许模式后再执行")
		}
	}
	return nil
}

func evaluatePermissionAction(config internalconfig.Config, toolName string, commandPreview string) (string, bool) {
	normalizedTool := strings.ToLower(strings.TrimSpace(toolName))
	normalizedCommand := strings.ToLower(strings.TrimSpace(commandPreview))
	for _, rule := range config.PermissionRules {
		if tool := strings.ToLower(strings.TrimSpace(rule.ToolName)); tool != "" && tool == normalizedTool {
			return internalconfig.NormalizePermissionAction(rule.Action), true
		}
		if prefix := strings.ToLower(strings.TrimSpace(rule.CommandPrefix)); prefix != "" && strings.HasPrefix(normalizedCommand, prefix) {
			return internalconfig.NormalizePermissionAction(rule.Action), true
		}
	}
	return "", false
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
