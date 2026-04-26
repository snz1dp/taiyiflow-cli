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

func resolveAllowedDirectory(config internalconfig.Config, requested string) (string, error) {
	resolved, err := resolveAllowedPath(config, requested, false)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", newToolError("EXECUTION_FAILED", "目录不存在: %v", err)
	}
	if !info.IsDir() {
		return "", newToolError("PARAM_ERROR", "目标必须是目录: %s", resolved)
	}
	return resolved, nil
}

func (r *ClientRuntime) resolveAllowedDirectoryWithPolicy(config internalconfig.Config, requested string) (string, error) {
	resolved, err := r.resolveAllowedPathWithPolicy(config, requested, false)
	if err != nil {
		return "", err
	}
	info, statErr := os.Stat(resolved)
	if statErr != nil {
		return "", newToolError("EXECUTION_FAILED", "目录不存在: %v", statErr)
	}
	if !info.IsDir() {
		return "", newToolError("PARAM_ERROR", "目标必须是目录: %s", resolved)
	}
	return resolved, nil
}

func (r *ClientRuntime) resolveAllowedFilePathWithPolicy(config internalconfig.Config, requested string, allowCreate bool) (string, error) {
	resolved, err := r.resolveAllowedPathWithPolicy(config, requested, allowCreate)
	if err != nil {
		return "", err
	}
	if !allowCreate {
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			return "", newToolError("EXECUTION_FAILED", "文件不存在: %v", statErr)
		}
		if info.IsDir() {
			return "", newToolError("PARAM_ERROR", "目标必须是文件: %s", resolved)
		}
	}
	return resolved, nil
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
			return "", newToolError("PERMISSION_DENIED", "路径超出当前工作目录访问范围，已按 deny 策略拒绝: %s", escapeErr.ResolvedPath)
		default:
			approved, interactive, confirmErr := r.confirmSandboxEscapePath(escapeErr.ResolvedPath)
			if confirmErr != nil {
				return "", confirmErr
			}
			if !approved {
				if !interactive {
					return "", newToolError("PERMISSION_DENIED", "路径超出当前工作目录访问范围，当前为 confirm 且无交互输入，请改用 allow 或会话覆盖")
				}
				return "", newToolError("PERMISSION_DENIED", "路径超出当前工作目录访问范围，用户拒绝访问: %s", escapeErr.ResolvedPath)
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
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(baseDir, target)
	}
	resolved, err := filepath.Abs(target)
	if err != nil {
		return "", newToolError("PARAM_ERROR", "解析路径失败: %v", err)
	}
	resolved, err = resolveWithExistingParent(resolved)
	if err != nil {
		return "", newToolError("PARAM_ERROR", "解析真实路径失败: %v", err)
	}
	if isSensitivePath(resolved) {
		return "", newToolError("PERMISSION_DENIED", "目标路径属于受保护目录: %s", resolved)
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
	roots := []string{baseDir}
	for _, item := range config.SandboxRoots {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		root := trimmed
		if !filepath.IsAbs(root) {
			root = filepath.Join(baseDir, root)
		}
		resolved, err := filepath.Abs(root)
		if err != nil {
			return nil, newToolError("PARAM_ERROR", "解析外部资源访问根目录 sandbox_roots 失败: %v", err)
		}
		resolved, err = resolveWithExistingParent(resolved)
		if err != nil {
			return nil, newToolError("PARAM_ERROR", "解析外部资源访问根目录 sandbox_roots 的真实路径失败: %v", err)
		}
		roots = append(roots, resolved)
	}
	return roots, nil
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
		if strings.TrimSpace(key) == "" || isSensitiveEnvKey(key) {
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
			return newToolError("PERMISSION_DENIED", "命中权限规则，拒绝执行 shell_exec")
		case "allow":
			return nil
		}
	}
	preview := strings.ToLower(strings.TrimSpace(payload.RawCommand + " " + payload.Command + " " + strings.Join(payload.Args, " ")))
	highRiskPatterns := []string{"rm -rf /", "mkfs", "shutdown", "reboot", "dd if=", "killall", "sudo "}
	for _, pattern := range highRiskPatterns {
		if strings.Contains(preview, pattern) && strings.TrimSpace(config.ApprovalMode) == "auto" {
			return newToolError("PERMISSION_DENIED", "检测到高风险命令，请切换到 confirm 模式后再执行")
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
