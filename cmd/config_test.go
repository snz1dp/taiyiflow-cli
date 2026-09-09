package cmd

import (
	"bytes"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestAssignConfigValueStreamIdleTimeoutSeconds(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "stream_idle_timeout_seconds", "0"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if config.StreamIdleTimeoutSeconds != 0 {
		t.Fatalf("expected stream idle timeout 0, got %d", config.StreamIdleTimeoutSeconds)
	}
}

func TestResetConfigValueStreamIdleTimeoutSeconds(t *testing.T) {
	config := internalconfig.Default()
	config.StreamIdleTimeoutSeconds = 999
	if err := resetConfigValue(&config, "stream_idle_timeout_seconds"); err != nil {
		t.Fatalf("resetConfigValue returned error: %v", err)
	}
	if config.StreamIdleTimeoutSeconds != internalconfig.Default().StreamIdleTimeoutSeconds {
		t.Fatalf("expected default stream idle timeout %d, got %d", internalconfig.Default().StreamIdleTimeoutSeconds, config.StreamIdleTimeoutSeconds)
	}
}

func TestAssignConfigValueRejectsRemovedStreamRetryAttempts(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "stream_retry_attempts", "3"); err == nil {
		t.Fatal("expected removed stream retry attempts config to be rejected")
	}
}

func TestResetConfigValueRejectsRemovedStreamRetryAttempts(t *testing.T) {
	config := internalconfig.Default()
	if err := resetConfigValue(&config, "stream_retry_attempts"); err == nil {
		t.Fatal("expected removed stream retry attempts config reset to be rejected")
	}
}

func TestAssignAndResetConfigValueCrossSessionMemoryEnabled(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "cross-session-memory", "true"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if !config.CrossSessionMemoryEnabled {
		t.Fatal("expected cross session memory to be enabled")
	}
	if err := assignConfigValue(&config, "cross_session_memory_enabled", "maybe"); err == nil {
		t.Fatal("expected invalid cross_session_memory_enabled value to fail")
	}
	if err := resetConfigValue(&config, "cross_session_memory_enabled"); err != nil {
		t.Fatalf("resetConfigValue returned error: %v", err)
	}
	if config.CrossSessionMemoryEnabled != internalconfig.Default().CrossSessionMemoryEnabled {
		t.Fatalf("expected default cross session memory %v, got %v", internalconfig.Default().CrossSessionMemoryEnabled, config.CrossSessionMemoryEnabled)
	}
}

func TestAssignConfigValueCommandTimeoutSeconds(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "command_timeout_seconds", "45"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if config.CommandTimeoutSeconds != 45 {
		t.Fatalf("expected command timeout 45, got %d", config.CommandTimeoutSeconds)
	}
}

func TestAssignAndResetBrowserConfigValues(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "browser_mode", "disabled"); err != nil {
		t.Fatalf("assignConfigValue browser_mode disabled returned error: %v", err)
	}
	if config.Browser.Mode != internalconfig.BrowserModeDisabled {
		t.Fatalf("expected browser mode %q, got %q", internalconfig.BrowserModeDisabled, config.Browser.Mode)
	}
	if err := assignConfigValue(&config, "browser_mode", "isolated"); err != nil {
		t.Fatalf("assignConfigValue browser_mode returned error: %v", err)
	}
	if config.Browser.Mode != internalconfig.BrowserModeIsolated {
		t.Fatalf("expected browser mode %q, got %q", internalconfig.BrowserModeIsolated, config.Browser.Mode)
	}
	if err := assignConfigValue(&config, "browser_permission_mode", "skip_all_permission_checks"); err != nil {
		t.Fatalf("assignConfigValue browser_permission_mode returned error: %v", err)
	}
	if config.Browser.PermissionMode != internalconfig.BrowserPermissionModeSkipAll {
		t.Fatalf("expected browser permission mode %q, got %q", internalconfig.BrowserPermissionModeSkipAll, config.Browser.PermissionMode)
	}
	if err := assignConfigValue(&config, "browser_channel", "edge"); err != nil {
		t.Fatalf("assignConfigValue browser_channel returned error: %v", err)
	}
	if config.Browser.Channel != internalconfig.BrowserChannelEdge {
		t.Fatalf("expected browser channel %q, got %q", internalconfig.BrowserChannelEdge, config.Browser.Channel)
	}
	if err := assignConfigValue(&config, "browser_connect_timeout_seconds", "30"); err != nil {
		t.Fatalf("assignConfigValue browser_connect_timeout_seconds returned error: %v", err)
	}
	if config.Browser.ConnectTimeoutSeconds != 30 {
		t.Fatalf("expected browser connect timeout 30, got %d", config.Browser.ConnectTimeoutSeconds)
	}
	if err := assignConfigValue(&config, "browser_headless", "true"); err != nil {
		t.Fatalf("assignConfigValue browser_headless returned error: %v", err)
	}
	if !config.Browser.Headless {
		t.Fatal("expected browser headless to be enabled")
	}
	if err := resetConfigValue(&config, "browser_mode"); err != nil {
		t.Fatalf("resetConfigValue browser_mode returned error: %v", err)
	}
	if config.Browser.Mode != internalconfig.Default().Browser.Mode {
		t.Fatalf("expected default browser mode %q, got %q", internalconfig.Default().Browser.Mode, config.Browser.Mode)
	}
	if err := resetConfigValue(&config, "browser_headless"); err != nil {
		t.Fatalf("resetConfigValue browser_headless returned error: %v", err)
	}
	if config.Browser.Headless != internalconfig.Default().Browser.Headless {
		t.Fatalf("expected default browser headless %v, got %v", internalconfig.Default().Browser.Headless, config.Browser.Headless)
	}
}

func TestAssignAndResetComputerEnabledConfigValue(t *testing.T) {
	config := internalconfig.Default()
	if !internalconfig.ComputerEnabled(config.Computer) {
		t.Fatal("expected desktop automation to be enabled by default")
	}
	if err := assignConfigValue(&config, "computer_enabled", "false"); err != nil {
		t.Fatalf("assignConfigValue computer_enabled returned error: %v", err)
	}
	if internalconfig.ComputerEnabled(config.Computer) {
		t.Fatal("expected desktop automation to be disabled")
	}
	if err := assignConfigValue(&config, "computer_enabled", "maybe"); err == nil {
		t.Fatal("expected invalid computer_enabled value to fail")
	}
	if err := resetConfigValue(&config, "computer_enabled"); err != nil {
		t.Fatalf("resetConfigValue computer_enabled returned error: %v", err)
	}
	if !internalconfig.ComputerEnabled(config.Computer) {
		t.Fatal("expected desktop automation to be re-enabled after reset")
	}
}

func TestAssignAndResetComputerPermissionModeConfigValue(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "computer_permission_mode", "skip_all_permission_checks"); err != nil {
		t.Fatalf("assignConfigValue computer_permission_mode returned error: %v", err)
	}
	if config.Computer.PermissionMode != internalconfig.ComputerPermissionModeSkipAll {
		t.Fatalf("expected desktop permission mode %q, got %#v", internalconfig.ComputerPermissionModeSkipAll, config.Computer)
	}
	if err := assignConfigValue(&config, "computer_permission_mode", "maybe"); err == nil {
		t.Fatal("expected invalid computer_permission_mode value to fail")
	}
	if err := resetConfigValue(&config, "computer_permission_mode"); err != nil {
		t.Fatalf("resetConfigValue computer_permission_mode returned error: %v", err)
	}
	if config.Computer.PermissionMode != internalconfig.Default().Computer.PermissionMode {
		t.Fatalf("expected default desktop permission mode %q, got %#v", internalconfig.Default().Computer.PermissionMode, config.Computer)
	}
}

func TestAssignAndResetConfigValueAuthAPIPrefix(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "auth_prefix", "custom-auth/"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if config.AuthAPIPrefix != "/custom-auth" {
		t.Fatalf("expected auth api prefix /custom-auth, got %q", config.AuthAPIPrefix)
	}
	if err := resetConfigValue(&config, "auth_api_prefix"); err != nil {
		t.Fatalf("resetConfigValue returned error: %v", err)
	}
	if config.AuthAPIPrefix != internalconfig.Default().AuthAPIPrefix {
		t.Fatalf("expected default auth api prefix %q, got %q", internalconfig.Default().AuthAPIPrefix, config.AuthAPIPrefix)
	}
}

func TestResetConfigValueCommandTimeoutSeconds(t *testing.T) {
	config := internalconfig.Default()
	config.CommandTimeoutSeconds = 45
	if err := resetConfigValue(&config, "command_timeout_seconds"); err != nil {
		t.Fatalf("resetConfigValue returned error: %v", err)
	}
	if config.CommandTimeoutSeconds != internalconfig.Default().CommandTimeoutSeconds {
		t.Fatalf("expected default command timeout %d, got %d", internalconfig.Default().CommandTimeoutSeconds, config.CommandTimeoutSeconds)
	}
}

func TestAssignConfigValueQRWaitTimeoutSeconds(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "qr_wait_timeout_seconds", "0"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if config.QRWaitTimeoutSeconds != 0 {
		t.Fatalf("expected qr wait timeout 0, got %d", config.QRWaitTimeoutSeconds)
	}
}

func TestResetConfigValueQRWaitTimeoutSeconds(t *testing.T) {
	config := internalconfig.Default()
	config.QRWaitTimeoutSeconds = 99
	if err := resetConfigValue(&config, "qr_wait_timeout_seconds"); err != nil {
		t.Fatalf("resetConfigValue returned error: %v", err)
	}
	if config.QRWaitTimeoutSeconds != internalconfig.Default().QRWaitTimeoutSeconds {
		t.Fatalf("expected default qr wait timeout %d, got %d", internalconfig.Default().QRWaitTimeoutSeconds, config.QRWaitTimeoutSeconds)
	}
}

func TestAssignConfigValueExternalResourceAccessPolicyAlias(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "external_resource_access_policy", "allow"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if config.SandboxEscapePolicy != internalconfig.SandboxEscapePolicyAllow {
		t.Fatalf("expected external resource access policy %q, got %q", internalconfig.SandboxEscapePolicyAllow, config.SandboxEscapePolicy)
	}
}

func TestAssignConfigValueSkillsAutoInstallPolicy(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "skills_auto_install_policy", "allow"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if config.Skills.AutoInstallPolicy != internalconfig.SkillAutoInstallPolicyAllow {
		t.Fatalf("expected skills auto install policy %q, got %q", internalconfig.SkillAutoInstallPolicyAllow, config.Skills.AutoInstallPolicy)
	}
	if err := assignConfigValue(&config, "skills_free_text_search", "false"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if config.Skills.FreeTextSearchEnabled {
		t.Fatal("expected free text search to be disabled")
	}
}

func TestAssignAndResetConfigValueDocUnstructuredExts(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "document_unstructured_extensions", ".PDF, docx, pdf"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if len(config.DocUnstructuredExts) != 2 || config.DocUnstructuredExts[0] != "pdf" || config.DocUnstructuredExts[1] != "docx" {
		t.Fatalf("expected normalized doc_unstructured_exts, got %#v", config.DocUnstructuredExts)
	}
	if err := resetConfigValue(&config, "doc_unstructured_exts"); err != nil {
		t.Fatalf("resetConfigValue returned error: %v", err)
	}
	if len(config.DocUnstructuredExts) != 0 {
		t.Fatalf("expected cleared doc_unstructured_exts, got %#v", config.DocUnstructuredExts)
	}
}

func TestRenderConfigSummaryUsesExternalResourceAccessPolicyLabel(t *testing.T) {
	config := internalconfig.Default()
	config.DocUnstructuredExts = []string{"pdf", "docx"}
	summary := renderConfigSummary("/tmp/taiyiflow.yaml", config)
	if !strings.Contains(summary, "外部资源访问策略:") {
		t.Fatalf("expected external resource access policy label, got %q", summary)
	}
	if strings.Contains(summary, "沙箱越界策略:") {
		t.Fatalf("expected sandbox wording to be removed, got %q", summary)
	}
	if strings.Contains(summary, "沙箱根目录:") {
		t.Fatalf("expected sandbox root wording to be removed, got %q", summary)
	}
	if !strings.Contains(summary, "可访问外部资源目录:") {
		t.Fatalf("expected external resource root label, got %q", summary)
	}
	if !strings.Contains(summary, "技能自动补装策略:") || !strings.Contains(summary, "自由文本技能搜索:") {
		t.Fatalf("expected skill auto install labels, got %q", summary)
	}
	if !strings.Contains(summary, "浏览器自动化模式:") || !strings.Contains(summary, "浏览器权限模式:") || !strings.Contains(summary, "桌面操作:") || !strings.Contains(summary, "桌面操作权限模式:") {
		t.Fatalf("expected browser/computer config labels, got %q", summary)
	}
	if !strings.Contains(summary, "跨会话记忆: true") {
		t.Fatalf("expected cross session memory label, got %q", summary)
	}
	if !strings.Contains(summary, "文档服务端解析类型:") || !strings.Contains(summary, ".pdf") || !strings.Contains(summary, ".docx") {
		t.Fatalf("expected doc unstructured ext summary, got %q", summary)
	}
}

func TestRenderConfigSummaryUsesDefaultDocUnstructuredExts(t *testing.T) {
	config := internalconfig.Default()
	summary := renderConfigSummary("/tmp/taiyiflow.yaml", config)
	for _, expected := range []string{"文档服务端解析类型:", ".mp3", ".mp4", ".pdf"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("expected doc unstructured summary to contain %q, got %q", expected, summary)
		}
	}
	if strings.Contains(summary, ".jpg") {
		t.Fatalf("expected default doc unstructured summary to exclude image extensions, got %q", summary)
	}
}

func TestRenderConfigSummaryHidesBrowserDetailsWhenDisabled(t *testing.T) {
	config := internalconfig.Default()
	config.Browser.Mode = internalconfig.BrowserModeDisabled
	summary := renderConfigSummary("/tmp/taiyiflow.yaml", config)
	for _, expected := range []string{"浏览器自动化模式:", "桌面操作:", "桌面操作权限模式:"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("expected %q in %q", expected, summary)
		}
	}
	for _, hidden := range []string{"浏览器权限模式:", "浏览器通道:", "浏览器可执行文件:", "浏览器 Profile 目录:", "浏览器连接超时:", "浏览器扩展接入:", "浏览器调试端点:", "浏览器 Headless:"} {
		if strings.Contains(summary, hidden) {
			t.Fatalf("expected %q hidden in %q", hidden, summary)
		}
	}
}

func TestRenderConfigSummaryUsesPermissionDisplayLabels(t *testing.T) {
	config := internalconfig.Default()
	config.Browser.PermissionMode = internalconfig.BrowserPermissionModeFollowPlan
	config.Computer.PermissionMode = internalconfig.ComputerPermissionModeSkipAll
	summary := renderConfigSummary("/tmp/taiyiflow.yaml", config)
	for _, expected := range []string{"浏览器权限模式: 跟随全局审批", "桌面操作权限模式: 始终允许", "浏览器自动化模式: 当前会话"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("expected %q in %q", expected, summary)
		}
	}
}

func TestAssignConfigValueRejectsUnsupportedAccessTokenConfig(t *testing.T) {
	config := internalconfig.Default()
	err := assignConfigValue(&config, "access_token", "token-1")
	if err == nil || !strings.Contains(err.Error(), "不支持的配置项: access_token") {
		t.Fatalf("expected unsupported access_token error, got %v", err)
	}
}

func TestShowConfigOmitsAccessToken(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.AccessToken = "access-token"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	stdout := &bytes.Buffer{}
	app := &App{configManager: manager, stdout: stdout}
	if err := app.showConfig(); err != nil {
		t.Fatalf("showConfig failed: %v", err)
	}
	if strings.Contains(stdout.String(), "access_token") || strings.Contains(stdout.String(), "access-token") {
		t.Fatalf("expected showConfig to omit access_token, got %q", stdout.String())
	}
}

func TestRunConfigHelpOutputsLocalizedUsage(t *testing.T) {
	stdout := &bytes.Buffer{}
	app := &App{stdout: stdout}
	if err := app.runConfig([]string{"help"}); err != nil {
		t.Fatalf("runConfig help failed: %v", err)
	}
	output := stdout.String()
	for _, expected := range []string{
		"配置命令用于查看、设置或清空命令行（CLI）全局配置。",
		"taiyiflow-cli config set <配置项> <值>",
		"browser_mode",
		"disabled",
		"computer_enabled",
		"browser_permission_mode",
		"doc_unstructured_exts",
		"未配置时使用内置默认值",
		"readonly（只读模式）",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected help output to contain %q, got %q", expected, output)
		}
	}
}

func TestRunConfigUnknownSubcommandSuggestsHelp(t *testing.T) {
	app := &App{stdout: &bytes.Buffer{}}
	err := app.runConfig([]string{"unknown"})
	if err == nil {
		t.Fatal("expected unknown subcommand to fail")
	}
	if !strings.Contains(err.Error(), "taiyiflow-cli config help") {
		t.Fatalf("expected unknown subcommand error to suggest help, got %v", err)
	}
}

func TestAssignConfigValueStreamProtocol(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValue string
		wantErr   bool
	}{
		{name: "auto", input: "auto", wantValue: "auto"},
		{name: "sse", input: "sse", wantValue: "sse"},
		{name: "websocket", input: "websocket", wantValue: "websocket"},
		{name: "ws alias", input: "ws", wantValue: "websocket"},
		{name: "uppercase WS", input: "WS", wantValue: "websocket"},
		{name: "mixed case WebSocket", input: "WebSocket", wantValue: "websocket"},
		{name: "invalid value", input: "grpc", wantErr: true},
		{name: "empty value", input: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := internalconfig.Default()
			err := assignConfigValue(&config, "stream_protocol", tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for input %q, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("assignConfigValue returned error for input %q: %v", tt.input, err)
			}
			if config.StreamProtocol != tt.wantValue {
				t.Fatalf("expected stream_protocol %q, got %q", tt.wantValue, config.StreamProtocol)
			}
		})
	}
}
