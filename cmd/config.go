package cmd

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"

	"gopkg.in/yaml.v3"
)

func (a *App) runConfig(args []string) error {
	if len(args) == 0 {
		return a.showConfig()
	}

	switch args[0] {
	case "show":
		return a.showConfig()
	case "init":
		config, err := a.configManager.InitDefault()
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "已初始化配置文件: %s\n", a.configManager.Path())
		content, _ := yaml.Marshal(config)
		fmt.Fprintln(a.stdout, string(content))
		return nil
	case "path":
		fmt.Fprintln(a.stdout, a.configManager.Path())
		return nil
	case "set":
		return a.setConfig(args[1:])
	case "unset", "clear":
		return a.unsetConfig(args[1:])
	default:
		return fmt.Errorf("不支持的 config 子命令: %s", args[0])
	}
}

func (a *App) showConfig() error {
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	content, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "配置路径: %s\n", a.configManager.Path())
	fmt.Fprintln(a.stdout, string(content))
	return nil
}

func (a *App) setConfig(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("用法: taiyiflow-cli config set <key> <value>")
	}

	key := strings.TrimSpace(args[0])
	value := strings.TrimSpace(strings.Join(args[1:], " "))
	if _, err := a.updateConfigValue(key, value); err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "已更新配置项 %s\n", normalizeConfigKey(key))
	return nil
}

func (a *App) unsetConfig(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("用法: taiyiflow-cli config unset <key>")
	}

	key := strings.TrimSpace(args[0])
	if _, err := a.clearConfigValue(key); err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "已清空配置项 %s\n", normalizeConfigKey(key))
	return nil
}

func (a *App) updateConfigValue(key string, value string) (internalconfig.Config, error) {
	config, err := a.configManager.Load()
	if err != nil {
		return internalconfig.Config{}, err
	}

	if err := assignConfigValue(&config, key, value); err != nil {
		return internalconfig.Config{}, err
	}

	if err := a.configManager.Save(config); err != nil {
		return internalconfig.Config{}, err
	}

	return config, nil
}

func (a *App) clearConfigValue(key string) (internalconfig.Config, error) {
	config, err := a.configManager.Load()
	if err != nil {
		return internalconfig.Config{}, err
	}

	if err := resetConfigValue(&config, key); err != nil {
		return internalconfig.Config{}, err
	}

	if err := a.configManager.Save(config); err != nil {
		return internalconfig.Config{}, err
	}

	return config, nil
}

func assignConfigValue(config *internalconfig.Config, key string, value string) error {
	normalizedKey := normalizeConfigKey(key)
	trimmedValue := strings.TrimSpace(value)

	switch normalizedKey {
	case "backend_base_url":
		normalizedURL, err := normalizeBackendBaseURL(trimmedValue)
		if err != nil {
			return err
		}
		config.BackendBaseURL = normalizedURL
	case "api_key":
		if trimmedValue == "" {
			return fmt.Errorf("api_key 不能为空，如需清空请使用 taiyiflow-cli config unset api_key")
		}
		config.APIKey = trimmedValue
	case "default_flow":
		if trimmedValue == "" {
			return fmt.Errorf("default_flow 不能为空，如需清空请使用 taiyiflow-cli config unset default_flow")
		}
		config.DefaultFlow = trimmedValue
	case "approval_mode":
		normalizedMode := strings.ToLower(trimmedValue)
		switch normalizedMode {
		case "confirm", "auto", "readonly":
			config.ApprovalMode = normalizedMode
		default:
			return fmt.Errorf("approval_mode 仅支持 confirm、auto、readonly")
		}
	case "reasoning_enabled":
		normalized := strings.ToLower(trimmedValue)
		if normalized != "true" && normalized != "false" {
			return fmt.Errorf("reasoning_enabled 仅支持 true 或 false")
		}
		config.ReasoningEnabled = normalized == "true"
	case "workdir":
		if trimmedValue == "" {
			return fmt.Errorf("workdir 不能为空，如需清空请使用 taiyiflow-cli config unset workdir")
		}
		config.Workdir = trimmedValue
	case "external_resource_access_policy":
		normalizedPolicy := internalconfig.NormalizeSandboxEscapePolicy(trimmedValue)
		if normalizedPolicy != trimmedValue {
			if strings.ToLower(trimmedValue) != normalizedPolicy {
				return fmt.Errorf("external_resource_access_policy 仅支持 confirm、deny、allow")
			}
		}
		config.SandboxEscapePolicy = normalizedPolicy
	case "stream_idle_timeout_seconds":
		seconds, err := strconv.Atoi(trimmedValue)
		if err != nil || seconds < 0 {
			return fmt.Errorf("stream_idle_timeout_seconds 仅支持大于等于 0 的整数秒，0 表示禁用")
		}
		config.StreamIdleTimeoutSeconds = seconds
	case "output_language":
		if trimmedValue == "" {
			return fmt.Errorf("output_language 不能为空，如需重置请使用 taiyiflow-cli config unset output_language")
		}
		config.OutputLanguage = trimmedValue
	case "audit_enabled":
		normalized := strings.ToLower(trimmedValue)
		if normalized != "true" && normalized != "false" {
			return fmt.Errorf("audit_enabled 仅支持 true 或 false")
		}
		config.AuditEnabled = normalized == "true"
	case "skills_directory":
		if trimmedValue == "" {
			return fmt.Errorf("skills_directory 不能为空，如需清空请使用 taiyiflow-cli config unset skills_directory")
		}
		config.Skills.Directory = trimmedValue
	case "skills_registry_url":
		normalizedURL, err := normalizeRemoteRegistryURL(trimmedValue)
		if err != nil {
			return err
		}
		config.Skills.RegistryURL = normalizedURL
	case "sandbox_roots":
		if trimmedValue == "" {
			return fmt.Errorf("sandbox_roots（外部资源访问根目录）不能为空，如需清空请使用 taiyiflow-cli config unset sandbox_roots")
		}
		config.SandboxRoots = splitConfigCSV(trimmedValue)
	default:
		return fmt.Errorf("不支持的配置项: %s", normalizedKey)
	}

	return nil
}

func resetConfigValue(config *internalconfig.Config, key string) error {
	defaults := internalconfig.Default()

	switch normalizeConfigKey(key) {
	case "backend_base_url":
		config.BackendBaseURL = defaults.BackendBaseURL
	case "api_key":
		config.APIKey = ""
	case "default_flow":
		config.DefaultFlow = ""
	case "approval_mode":
		config.ApprovalMode = defaults.ApprovalMode
	case "reasoning_enabled":
		config.ReasoningEnabled = defaults.ReasoningEnabled
	case "workdir":
		config.Workdir = ""
	case "external_resource_access_policy":
		config.SandboxEscapePolicy = defaults.SandboxEscapePolicy
	case "stream_idle_timeout_seconds":
		config.StreamIdleTimeoutSeconds = defaults.StreamIdleTimeoutSeconds
	case "output_language":
		config.OutputLanguage = defaults.OutputLanguage
	case "audit_enabled":
		config.AuditEnabled = defaults.AuditEnabled
	case "skills_directory":
		config.Skills.Directory = ""
	case "skills_registry_url":
		config.Skills.RegistryURL = defaults.Skills.RegistryURL
	case "sandbox_roots":
		config.SandboxRoots = nil
	default:
		return fmt.Errorf("不支持的配置项: %s", normalizeConfigKey(key))
	}

	return nil
}

func normalizeConfigKey(key string) string {
	normalized := strings.ToLower(strings.TrimSpace(key))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	switch normalized {
	case "sandbox_escape_policy", "external_resource_policy":
		return "external_resource_access_policy"
	case "stream_idle_timeout":
		return "stream_idle_timeout_seconds"
	default:
		return normalized
	}
}

func normalizeBackendBaseURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("backend_base_url 不能为空")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("backend_base_url 必须是完整的 HTTP 地址，例如 http://localhost:7860")
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("backend_base_url 仅支持 http 或 https")
	}

	return strings.TrimRight(trimmed, "/"), nil
}

func renderConfigSummary(path string, config internalconfig.Config) string {
	lines := []string{
		fmt.Sprintf("配置路径: %s", path),
		fmt.Sprintf("服务端地址: %s", fallbackConfigValue(config.BackendBaseURL, internalconfig.Default().BackendBaseURL)),
		fmt.Sprintf("默认智能体: %s", fallbackConfigValue(config.DefaultFlow, "<未配置>")),
		fmt.Sprintf("命令执行模式: %s", fallbackConfigValue(config.ApprovalMode, internalconfig.Default().ApprovalMode)),
		fmt.Sprintf("默认思考模式: %t", config.ReasoningEnabled),
		fmt.Sprintf("工作目录: %s", fallbackConfigValue(config.Workdir, "<当前目录>")),
		fmt.Sprintf("外部资源访问策略: %s", fallbackConfigValue(config.SandboxEscapePolicy, internalconfig.Default().SandboxEscapePolicy)),
		fmt.Sprintf("流式空闲超时: %d 秒", config.StreamIdleTimeoutSeconds),
		fmt.Sprintf("输出语言: %s", fallbackConfigValue(config.OutputLanguage, internalconfig.Default().OutputLanguage)),
		fmt.Sprintf("审计日志: %t", config.AuditEnabled),
		fmt.Sprintf("技能目录: %s", fallbackConfigValue(config.Skills.Directory, "<默认 ~/.snz1dp/skills>")),
		fmt.Sprintf("技能仓库: %s", fallbackConfigValue(config.Skills.RegistryURL, internalconfig.Default().Skills.RegistryURL)),
		fmt.Sprintf("外部资源访问根目录: %s", fallbackConfigValue(strings.Join(config.SandboxRoots, ", "), "<仅当前工作目录>")),
	}
	return strings.Join(lines, "\n")
}

func normalizeRemoteRegistryURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("skills_registry_url 不能为空")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || strings.TrimSpace(parsed.Scheme) == "" || strings.TrimSpace(parsed.Host) == "" {
		return "", fmt.Errorf("skills_registry_url 必须是完整的 HTTP 地址，例如 https://mirror-cn.clawhub.com")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("skills_registry_url 仅支持 http 或 https")
	}
	return strings.TrimRight(trimmed, "/"), nil
}

func splitConfigCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func fallbackConfigValue(value string, defaultValue string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultValue
	}
	return trimmed
}
