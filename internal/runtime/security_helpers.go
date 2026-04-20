package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

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

func resolveAllowedFilePath(config internalconfig.Config, requested string, allowCreate bool) (string, error) {
	resolved, err := resolveAllowedPath(config, requested, allowCreate)
	if err != nil {
		return "", err
	}
	if !allowCreate {
		info, err := os.Stat(resolved)
		if err != nil {
			return "", newToolError("EXECUTION_FAILED", "文件不存在: %v", err)
		}
		if info.IsDir() {
			return "", newToolError("PARAM_ERROR", "目标必须是文件: %s", resolved)
		}
	}
	return resolved, nil
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
	return "", newToolError("PERMISSION_DENIED", "路径超出工作目录沙箱: %s", resolved)
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
			return nil, newToolError("PARAM_ERROR", "解析 sandbox_roots 失败: %v", err)
		}
		resolved, err = resolveWithExistingParent(resolved)
		if err != nil {
			return nil, newToolError("PARAM_ERROR", "解析 sandbox_roots 真实路径失败: %v", err)
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
	preview := strings.ToLower(strings.TrimSpace(payload.RawCommand + " " + payload.Command + " " + strings.Join(payload.Args, " ")))
	highRiskPatterns := []string{"rm -rf /", "mkfs", "shutdown", "reboot", "dd if=", "killall", "sudo "}
	for _, pattern := range highRiskPatterns {
		if strings.Contains(preview, pattern) && strings.TrimSpace(config.ApprovalMode) == "auto" {
			return newToolError("PERMISSION_DENIED", "检测到高风险命令，请切换到 confirm 模式后再执行")
		}
	}
	return nil
}

func isHiddenPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}
