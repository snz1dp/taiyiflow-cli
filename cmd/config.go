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
	config.AccessToken = ""
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
	case "backend_api_prefix":
		config.BackendAPIPrefix = internalconfig.NormalizeBackendAPIPrefix(trimmedValue)
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
	case "skill_review_policy":
		normalizedPolicy := internalconfig.NormalizeSkillReviewPolicy(trimmedValue)
		if normalizedPolicy != trimmedValue {
			if strings.ToLower(trimmedValue) != normalizedPolicy {
				return fmt.Errorf("skill_review_policy 仅支持 confirm、ignore")
			}
		}
		config.SkillReviewPolicy = normalizedPolicy
	case "write_confirmation_policy":
		normalizedPolicy := internalconfig.NormalizeWriteConfirmationPolicy(trimmedValue)
		if normalizedPolicy != trimmedValue {
			if strings.ToLower(trimmedValue) != normalizedPolicy {
				return fmt.Errorf("write_confirmation_policy 仅支持 confirm、direct")
			}
		}
		config.WriteConfirmationPolicy = normalizedPolicy
	case "http_timeout_seconds":
		seconds, err := strconv.Atoi(trimmedValue)
		if err != nil || seconds < 1 || seconds > internalconfig.MaxHTTPTimeoutSeconds {
			return fmt.Errorf("http_timeout_seconds 仅支持 1-%d 的整数秒", internalconfig.MaxHTTPTimeoutSeconds)
		}
		config.HTTPTimeoutSeconds = seconds
	case "command_timeout_seconds":
		seconds, err := strconv.Atoi(trimmedValue)
		if err != nil || seconds < 1 || seconds > internalconfig.MaxCommandTimeoutSeconds {
			return fmt.Errorf("command_timeout_seconds 仅支持 1-%d 的整数秒", internalconfig.MaxCommandTimeoutSeconds)
		}
		config.CommandTimeoutSeconds = seconds
	case "stream_idle_timeout_seconds":
		seconds, err := strconv.Atoi(trimmedValue)
		if err != nil || seconds < 0 {
			return fmt.Errorf("stream_idle_timeout_seconds 仅支持大于等于 0 的整数秒，0 表示禁用")
		}
		config.StreamIdleTimeoutSeconds = seconds
	case "stream_retry_attempts":
		attempts, err := strconv.Atoi(trimmedValue)
		if err != nil || attempts < 0 || attempts > internalconfig.MaxStreamRetryAttempts {
			return fmt.Errorf("stream_retry_attempts 仅支持 0-%d 的整数，0 表示不重试", internalconfig.MaxStreamRetryAttempts)
		}
		config.StreamRetryAttempts = attempts
	case "cross_session_memory_enabled":
		normalized := strings.ToLower(trimmedValue)
		if normalized != "true" && normalized != "false" {
			return fmt.Errorf("cross_session_memory_enabled 仅支持 true 或 false")
		}
		config.CrossSessionMemoryEnabled = normalized == "true"
	case "qr_wait_timeout_seconds":
		seconds, err := strconv.Atoi(trimmedValue)
		if err != nil || seconds < 0 {
			return fmt.Errorf("qr_wait_timeout_seconds 仅支持大于等于 0 的整数秒，0 表示禁用")
		}
		config.QRWaitTimeoutSeconds = seconds
	case "agent_auto_loop_max_rounds":
		rounds, err := strconv.Atoi(trimmedValue)
		if err != nil || rounds < 0 || rounds > internalconfig.MaxAgentAutoLoopMaxRounds {
			return fmt.Errorf("agent_auto_loop_max_rounds 仅支持 0-%d 的整数，0 表示禁用", internalconfig.MaxAgentAutoLoopMaxRounds)
		}
		config.AgentAutoLoopMaxRounds = rounds
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
	case "skills_install_scope":
		normalizedScope := internalconfig.NormalizeSkillInstallScope(trimmedValue)
		if strings.ToLower(trimmedValue) != normalizedScope {
			return fmt.Errorf("skills_install_scope 仅支持 user 或 workspace")
		}
		config.Skills.InstallScope = normalizedScope
		config.Skills.Directory = ""
	case "skills_auto_install_policy":
		normalizedPolicy := internalconfig.NormalizeSkillAutoInstallPolicy(trimmedValue)
		if strings.ToLower(trimmedValue) != normalizedPolicy {
			return fmt.Errorf("skills_auto_install_policy 仅支持 off、confirm、allow")
		}
		config.Skills.AutoInstallPolicy = normalizedPolicy
	case "skills_free_text_search":
		normalized := strings.ToLower(trimmedValue)
		if normalized != "true" && normalized != "false" {
			return fmt.Errorf("skills_free_text_search 仅支持 true 或 false")
		}
		config.Skills.FreeTextSearchEnabled = normalized == "true"
	case "skills_registry_url":
		normalizedURL, err := normalizeRemoteRegistryURL(trimmedValue)
		if err != nil {
			return err
		}
		config.Skills.RegistryURL = normalizedURL
	case "sandbox_roots":
		if trimmedValue == "" {
			return fmt.Errorf("sandbox_roots（可访问外部资源目录）不能为空，如需清空请使用 taiyiflow-cli config unset sandbox_roots")
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
	case "backend_api_prefix":
		config.BackendAPIPrefix = defaults.BackendAPIPrefix
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
	case "skill_review_policy":
		config.SkillReviewPolicy = defaults.SkillReviewPolicy
	case "write_confirmation_policy":
		config.WriteConfirmationPolicy = defaults.WriteConfirmationPolicy
	case "http_timeout_seconds":
		config.HTTPTimeoutSeconds = defaults.HTTPTimeoutSeconds
	case "command_timeout_seconds":
		config.CommandTimeoutSeconds = defaults.CommandTimeoutSeconds
	case "stream_idle_timeout_seconds":
		config.StreamIdleTimeoutSeconds = defaults.StreamIdleTimeoutSeconds
	case "stream_retry_attempts":
		config.StreamRetryAttempts = defaults.StreamRetryAttempts
	case "cross_session_memory_enabled":
		config.CrossSessionMemoryEnabled = defaults.CrossSessionMemoryEnabled
	case "qr_wait_timeout_seconds":
		config.QRWaitTimeoutSeconds = defaults.QRWaitTimeoutSeconds
	case "agent_auto_loop_max_rounds":
		config.AgentAutoLoopMaxRounds = defaults.AgentAutoLoopMaxRounds
	case "output_language":
		config.OutputLanguage = defaults.OutputLanguage
	case "audit_enabled":
		config.AuditEnabled = defaults.AuditEnabled
	case "skills_install_scope":
		config.Skills.InstallScope = defaults.Skills.InstallScope
		config.Skills.Directory = ""
	case "skills_auto_install_policy":
		config.Skills.AutoInstallPolicy = defaults.Skills.AutoInstallPolicy
	case "skills_free_text_search":
		config.Skills.FreeTextSearchEnabled = defaults.Skills.FreeTextSearchEnabled
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
	case "backend_prefix", "api_prefix", "backend_prefix_path":
		return "backend_api_prefix"
	case "sandbox_escape_policy", "external_resource_policy":
		return "external_resource_access_policy"
	case "skill_package_review_policy", "skills_review_policy":
		return "skill_review_policy"
	case "write_summary_policy", "write_review_policy", "write_confirm_policy":
		return "write_confirmation_policy"
	case "command_timeout", "local_command_timeout", "local_command_timeout_seconds":
		return "command_timeout_seconds"
	case "http_timeout", "request_timeout", "backend_http_timeout":
		return "http_timeout_seconds"
	case "stream_idle_timeout":
		return "stream_idle_timeout_seconds"
	case "stream_retry", "stream_retry_count", "stream_retry_times", "stream_retry_attempt", "stream_retries":
		return "stream_retry_attempts"
	case "cross_session_memory", "cross_session_memory_enabled", "cross_session_memory_enable", "cross_session_mem", "memory_cross_session":
		return "cross_session_memory_enabled"
	case "qr_wait_timeout", "qrcode_wait_timeout", "qr_login_wait_timeout":
		return "qr_wait_timeout_seconds"
	case "agent_auto_continue_max_rounds", "agent_autoloop_max_rounds", "agent_auto_loop_rounds":
		return "agent_auto_loop_max_rounds"
	case "skills_directory":
		return "skills_install_scope"
	case "skills_auto_install", "skill_auto_install_policy":
		return "skills_auto_install_policy"
	case "skills_free_text_search_enabled", "skills_intent_search", "skills_auto_install_free_text":
		return "skills_free_text_search"
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
		return "", fmt.Errorf("backend_base_url 必须是完整的 HTTP 地址，例如 http://localhost:4005")
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
		fmt.Sprintf("服务端前缀: %s", fallbackConfigValue(config.BackendAPIPrefix, internalconfig.Default().BackendAPIPrefix)),
		fmt.Sprintf("默认智能体: %s", fallbackConfigValue(config.DefaultFlow, "<未配置>")),
		fmt.Sprintf("命令执行模式: %s", fallbackConfigValue(config.ApprovalMode, internalconfig.Default().ApprovalMode)),
		fmt.Sprintf("默认思考模式: %t", config.ReasoningEnabled),
		fmt.Sprintf("工作目录: %s", fallbackConfigValue(config.Workdir, "<当前目录>")),
		fmt.Sprintf("外部资源访问策略: %s", fallbackConfigValue(config.SandboxEscapePolicy, internalconfig.Default().SandboxEscapePolicy)),
		fmt.Sprintf("技能包审查策略: %s", fallbackConfigValue(config.SkillReviewPolicy, internalconfig.Default().SkillReviewPolicy)),
		fmt.Sprintf("写入确认策略: %s", fallbackConfigValue(config.WriteConfirmationPolicy, internalconfig.Default().WriteConfirmationPolicy)),
		fmt.Sprintf("命令执行超时: %d 秒", config.CommandTimeoutSeconds),
		fmt.Sprintf("流式空闲超时: %d 秒", config.StreamIdleTimeoutSeconds),
		fmt.Sprintf("流失败自动重试次数: %d", config.StreamRetryAttempts),
		fmt.Sprintf("跨会话记忆: %t", config.CrossSessionMemoryEnabled),
		fmt.Sprintf("扫码等待超时: %d 秒", config.QRWaitTimeoutSeconds),
		fmt.Sprintf("智能体自动续跑最大轮数: %d（0 表示禁用）", config.AgentAutoLoopMaxRounds),
		fmt.Sprintf("输出语言: %s", fallbackConfigValue(config.OutputLanguage, internalconfig.Default().OutputLanguage)),
		fmt.Sprintf("审计日志: %t", config.AuditEnabled),
		fmt.Sprintf("技能安装位置: %s", skillInstallScopeLabel(config)),
		fmt.Sprintf("技能自动补装策略: %s", fallbackConfigValue(config.Skills.AutoInstallPolicy, internalconfig.Default().Skills.AutoInstallPolicy)),
		fmt.Sprintf("自由文本技能搜索: %t", config.Skills.FreeTextSearchEnabled),
		fmt.Sprintf("技能仓库: %s", fallbackConfigValue(config.Skills.RegistryURL, internalconfig.Default().Skills.RegistryURL)),
		fmt.Sprintf("可访问外部资源目录: %s", fallbackConfigValue(strings.Join(config.SandboxRoots, ", "), "<仅当前工作目录>")),
	}
	return strings.Join(lines, "\n")
}

func skillInstallScopeLabel(config internalconfig.Config) string {
	if strings.TrimSpace(config.Skills.Directory) != "" {
		return fmt.Sprintf("兼容旧配置: %s", strings.TrimSpace(config.Skills.Directory))
	}
	switch internalconfig.NormalizeSkillInstallScope(config.Skills.InstallScope) {
	case internalconfig.SkillInstallScopeWorkspace:
		return "工作区 (.snz1dp/skills)"
	default:
		return "用户目录 (~/.snz1dp/skills)"
	}
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
