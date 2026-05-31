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
	case "help", "-h", "--help":
		return a.showConfigHelp()
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
		return fmt.Errorf("不支持的 config 子命令: %s，可使用 taiyiflow-cli config help 查看用法", args[0])
	}
}

func (a *App) showConfigHelp() error {
	lines := []string{
		"配置命令用于查看、设置或清空命令行（CLI）全局配置。",
		"",
		"用法:",
		"  taiyiflow-cli config show",
		"  taiyiflow-cli config path",
		"  taiyiflow-cli config set <配置项> <值>",
		"  taiyiflow-cli config unset <配置项>",
		"  taiyiflow-cli config help",
		"",
		"常用配置项:",
		"  backend_base_url              服务端地址，需填写完整的 HTTP 或 HTTPS 地址。",
		"  default_flow                  默认智能体 ID；留空时启动后自动选择。",
		"  approval_mode                 命令执行模式；可选 confirm（每次确认）、auto（自动执行）、readonly（只读模式）。",
		"  browser_mode                  浏览器自动化模式；可选 disabled（禁用）、chrome_session（连接当前浏览器会话）、isolated（独立实例）。",
		"  browser_permission_mode       浏览器变更工具审批；可选 ask、follow_a_plan、skip_all_permission_checks。",
		"  computer_enabled              桌面操作开关；可选 true、false。",
		"  computer_permission_mode      桌面操作变更工具审批；可选 ask、follow_a_plan、skip_all_permission_checks。",
		"  doc_unstructured_exts         逗号分隔扩展名；未配置时使用内置默认值，可用自定义列表覆盖。",
		"  skills_auto_install_policy    技能自动补装策略；可选 off、confirm、allow。",
		"  cross_session_memory_enabled  跨会话记忆开关；可选 true、false。",
		"",
		"示例:",
		"  taiyiflow-cli config set backend_base_url https://snz1.cn",
		"  taiyiflow-cli config set browser_mode disabled",
		"  taiyiflow-cli config set browser_mode isolated",
		"  taiyiflow-cli config set computer_enabled false",
		"  taiyiflow-cli config set browser_remote_debugging_url http://127.0.0.1:9222",
		"  taiyiflow-cli config set doc_unstructured_exts pdf,docx",
		"  taiyiflow-cli config unset doc_unstructured_exts",
		"",
		"提示: 可先执行 taiyiflow-cli config show 查看当前 YAML 配置。",
	}
	_, err := fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
	return err
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
		return fmt.Errorf("用法: taiyiflow-cli config set <配置项> <值>；可使用 taiyiflow-cli config help 查看示例")
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
		return fmt.Errorf("用法: taiyiflow-cli config unset <配置项>；可使用 taiyiflow-cli config help 查看示例")
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
	case "auth_api_prefix":
		config.AuthAPIPrefix = internalconfig.NormalizeAuthAPIPrefix(trimmedValue)
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
	case "browser_mode":
		normalizedMode := internalconfig.NormalizeBrowserMode(trimmedValue)
		if strings.ToLower(trimmedValue) != normalizedMode {
			return fmt.Errorf("browser_mode 仅支持 disabled、chrome_session、isolated")
		}
		config.Browser.Mode = normalizedMode
	case "browser_permission_mode":
		normalizedMode := internalconfig.NormalizeBrowserPermissionMode(trimmedValue)
		if strings.ToLower(trimmedValue) != normalizedMode {
			return fmt.Errorf("browser_permission_mode 仅支持 ask、follow_a_plan、skip_all_permission_checks")
		}
		config.Browser.PermissionMode = normalizedMode
	case "computer_enabled":
		normalized := strings.ToLower(trimmedValue)
		if normalized != "true" && normalized != "false" {
			return fmt.Errorf("computer_enabled 仅支持 true 或 false")
		}
		config.Computer.Enabled = boolPtr(normalized == "true")
	case "computer_permission_mode":
		normalizedMode := internalconfig.NormalizeComputerPermissionMode(trimmedValue)
		if strings.ToLower(trimmedValue) != normalizedMode {
			return fmt.Errorf("computer_permission_mode 仅支持 ask、follow_a_plan、skip_all_permission_checks")
		}
		config.Computer.PermissionMode = normalizedMode
	case "browser_channel":
		normalizedChannel := internalconfig.NormalizeBrowserChannel(trimmedValue)
		if strings.ToLower(trimmedValue) != normalizedChannel {
			return fmt.Errorf("browser_channel 仅支持 chrome、chromium、edge、brave")
		}
		config.Browser.Channel = normalizedChannel
	case "browser_executable_path":
		if trimmedValue == "" {
			return fmt.Errorf("browser_executable_path 不能为空，如需清空请使用 taiyiflow-cli config unset browser_executable_path")
		}
		config.Browser.ExecutablePath = trimmedValue
	case "browser_profile_dir":
		if trimmedValue == "" {
			return fmt.Errorf("browser_profile_dir 不能为空，如需清空请使用 taiyiflow-cli config unset browser_profile_dir")
		}
		config.Browser.ProfileDir = trimmedValue
	case "browser_connect_timeout_seconds":
		seconds, err := strconv.Atoi(trimmedValue)
		if err != nil || seconds < 1 || seconds > internalconfig.MaxBrowserConnectTimeout {
			return fmt.Errorf("browser_connect_timeout_seconds 仅支持 1-%d 的整数秒", internalconfig.MaxBrowserConnectTimeout)
		}
		config.Browser.ConnectTimeoutSeconds = seconds
	case "browser_extension_setup":
		normalizedSetup := internalconfig.NormalizeBrowserExtensionSetup(trimmedValue)
		if strings.ToLower(trimmedValue) != normalizedSetup {
			return fmt.Errorf("browser_extension_setup 仅支持 native_messaging、manual、none")
		}
		config.Browser.ExtensionSetup = normalizedSetup
	case "browser_socket_dir":
		if trimmedValue == "" {
			return fmt.Errorf("browser_socket_dir 不能为空，如需清空请使用 taiyiflow-cli config unset browser_socket_dir")
		}
		config.Browser.SocketDir = trimmedValue
	case "browser_remote_debugging_url":
		if trimmedValue == "" {
			return fmt.Errorf("browser_remote_debugging_url 不能为空，如需清空请使用 taiyiflow-cli config unset browser_remote_debugging_url")
		}
		config.Browser.RemoteDebuggingURL = trimmedValue
	case "browser_headless":
		normalized := strings.ToLower(trimmedValue)
		if normalized != "true" && normalized != "false" {
			return fmt.Errorf("browser_headless 仅支持 true 或 false")
		}
		config.Browser.Headless = normalized == "true"
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
	case "doc_unstructured_exts":
		normalizedExts := internalconfig.NormalizeDocUnstructuredExts(splitConfigCSV(trimmedValue))
		if len(normalizedExts) == 0 {
			return fmt.Errorf("doc_unstructured_exts 不能为空；如需恢复内置默认值请使用 taiyiflow-cli config unset doc_unstructured_exts")
		}
		config.DocUnstructuredExts = normalizedExts
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
	case "auth_api_prefix":
		config.AuthAPIPrefix = defaults.AuthAPIPrefix
	case "default_flow":
		config.DefaultFlow = ""
	case "approval_mode":
		config.ApprovalMode = defaults.ApprovalMode
	case "browser_mode":
		config.Browser.Mode = defaults.Browser.Mode
	case "browser_permission_mode":
		config.Browser.PermissionMode = defaults.Browser.PermissionMode
	case "computer_enabled":
		config.Computer.Enabled = defaults.Computer.Enabled
	case "computer_permission_mode":
		config.Computer.PermissionMode = defaults.Computer.PermissionMode
	case "browser_channel":
		config.Browser.Channel = defaults.Browser.Channel
	case "browser_executable_path":
		config.Browser.ExecutablePath = ""
	case "browser_profile_dir":
		config.Browser.ProfileDir = ""
	case "browser_connect_timeout_seconds":
		config.Browser.ConnectTimeoutSeconds = defaults.Browser.ConnectTimeoutSeconds
	case "browser_extension_setup":
		config.Browser.ExtensionSetup = defaults.Browser.ExtensionSetup
	case "browser_socket_dir":
		config.Browser.SocketDir = ""
	case "browser_remote_debugging_url":
		config.Browser.RemoteDebuggingURL = ""
	case "browser_headless":
		config.Browser.Headless = defaults.Browser.Headless
	case "reasoning_enabled":
		config.ReasoningEnabled = defaults.ReasoningEnabled
	case "workdir":
		config.Workdir = ""
	case "doc_unstructured_exts":
		config.DocUnstructuredExts = nil
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
	case "auth_prefix", "auth_prefix_path", "login_api_prefix":
		return "auth_api_prefix"
	case "browser", "browser_run_mode", "browser_automation_mode":
		return "browser_mode"
	case "browser_permission", "browser_permissions", "browser_tool_permission_mode":
		return "browser_permission_mode"
	case "computer_enabled", "computer_enable", "computer_use_enabled", "desktop_computer_enabled", "desktop_computer_use_enabled":
		return "computer_enabled"
	case "computer_permission", "computer_permissions", "desktop_permission_mode", "desktop_computer_permission_mode":
		return "computer_permission_mode"
	case "browser_browser", "browser_channel_name":
		return "browser_channel"
	case "browser_path", "browser_executable", "browser_binary":
		return "browser_executable_path"
	case "browser_profile", "browser_user_data_dir":
		return "browser_profile_dir"
	case "browser_connect_timeout", "browser_timeout", "browser_timeout_seconds":
		return "browser_connect_timeout_seconds"
	case "browser_manifest_setup", "browser_native_messaging_setup":
		return "browser_extension_setup"
	case "browser_socket", "browser_socket_path":
		return "browser_socket_dir"
	case "browser_debug_url", "browser_debugging_url", "browser_remote_debug_url", "browser_devtools_url":
		return "browser_remote_debugging_url"
	case "browser_is_headless":
		return "browser_headless"
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
	case "doc_unstructured_extensions", "document_unstructured_exts", "document_unstructured_extensions", "document_parse_exts":
		return "doc_unstructured_exts"
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
		return "", fmt.Errorf("backend_base_url 必须是完整的 HTTP 地址，例如 https://snz1.cn")
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
		fmt.Sprintf("认证端前缀: %s", fallbackConfigValue(config.AuthAPIPrefix, internalconfig.Default().AuthAPIPrefix)),
		fmt.Sprintf("默认智能体: %s", fallbackConfigValue(config.DefaultFlow, "<未配置>")),
		fmt.Sprintf("命令执行模式: %s", fallbackConfigValue(config.ApprovalMode, internalconfig.Default().ApprovalMode)),
		fmt.Sprintf("浏览器自动化模式: %s", browserModeDisplay(config.Browser.Mode)),
		fmt.Sprintf("桌面操作: %s", enabledStateLabel(internalconfig.ComputerEnabled(config.Computer))),
		fmt.Sprintf("桌面操作权限模式: %s", computerPermissionModeDisplay(config.Computer.PermissionMode)),
		fmt.Sprintf("默认思考模式: %t", config.ReasoningEnabled),
		fmt.Sprintf("工作目录: %s", fallbackConfigValue(config.Workdir, "<当前目录>")),
		fmt.Sprintf("外部资源访问策略: %s", fallbackConfigValue(config.SandboxEscapePolicy, internalconfig.Default().SandboxEscapePolicy)),
		fmt.Sprintf("技能包审查策略: %s", fallbackConfigValue(config.SkillReviewPolicy, internalconfig.Default().SkillReviewPolicy)),
		fmt.Sprintf("写入确认策略: %s", fallbackConfigValue(config.WriteConfirmationPolicy, internalconfig.Default().WriteConfirmationPolicy)),
		fmt.Sprintf("命令执行超时: %d 秒", config.CommandTimeoutSeconds),
		fmt.Sprintf("流式空闲超时: %d 秒", config.StreamIdleTimeoutSeconds),
		fmt.Sprintf("跨会话记忆: %t", config.CrossSessionMemoryEnabled),
		fmt.Sprintf("扫码等待超时: %d 秒", config.QRWaitTimeoutSeconds),
		fmt.Sprintf("智能体自动续跑最大轮数: %d（0 表示禁用）", config.AgentAutoLoopMaxRounds),
		fmt.Sprintf("输出语言: %s", fallbackConfigValue(config.OutputLanguage, internalconfig.Default().OutputLanguage)),
		fmt.Sprintf("审计日志: %t", config.AuditEnabled),
		fmt.Sprintf("技能安装位置: %s", skillInstallScopeLabel(config)),
		fmt.Sprintf("技能自动补装策略: %s", fallbackConfigValue(config.Skills.AutoInstallPolicy, internalconfig.Default().Skills.AutoInstallPolicy)),
		fmt.Sprintf("自由文本技能搜索: %t", config.Skills.FreeTextSearchEnabled),
		fmt.Sprintf("技能仓库: %s", fallbackConfigValue(config.Skills.RegistryURL, internalconfig.Default().Skills.RegistryURL)),
		fmt.Sprintf("文档服务端解析类型: %s", fallbackConfigValue(strings.Join(prefixDocumentExts(config.DocUnstructuredExts), ", "), strings.Join(prefixDocumentExts(internalconfig.Default().DocUnstructuredExts), ", "))),
		fmt.Sprintf("可访问外部资源目录: %s", fallbackConfigValue(strings.Join(config.SandboxRoots, ", "), "<仅当前工作目录>")),
	}
	if internalconfig.BrowserEnabled(config.Browser) {
		browserLines := []string{
			fmt.Sprintf("浏览器权限模式: %s", browserPermissionModeDisplay(config.Browser.PermissionMode)),
			fmt.Sprintf("浏览器通道: %s", browserChannelDisplay(config.Browser.Channel)),
			fmt.Sprintf("浏览器可执行文件: %s", fallbackConfigValue(config.Browser.ExecutablePath, "<自动探测>")),
			fmt.Sprintf("浏览器 Profile 目录: %s", fallbackConfigValue(config.Browser.ProfileDir, "<自动管理>")),
			fmt.Sprintf("浏览器连接超时: %d 秒", config.Browser.ConnectTimeoutSeconds),
			fmt.Sprintf("浏览器扩展接入: %s", browserExtensionSetupDisplay(config.Browser.ExtensionSetup)),
			fmt.Sprintf("浏览器调试端点: %s", fallbackConfigValue(config.Browser.RemoteDebuggingURL, "<默认 127.0.0.1:9222>")),
			fmt.Sprintf("浏览器 Headless: %t", config.Browser.Headless),
		}
		lines = append(lines[:7], append(browserLines, lines[7:]...)...)
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

func enabledStateLabel(enabled bool) string {
	if enabled {
		return "开启"
	}
	return "关闭"
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
