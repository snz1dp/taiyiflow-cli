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

func TestAssignConfigValueCommandTimeoutSeconds(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "command_timeout_seconds", "45"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if config.CommandTimeoutSeconds != 45 {
		t.Fatalf("expected command timeout 45, got %d", config.CommandTimeoutSeconds)
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

func TestRenderConfigSummaryUsesExternalResourceAccessPolicyLabel(t *testing.T) {
	config := internalconfig.Default()
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
