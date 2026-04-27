package cmd

import (
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

func TestAssignConfigValueExternalResourceAccessPolicyAlias(t *testing.T) {
	config := internalconfig.Default()
	if err := assignConfigValue(&config, "external_resource_access_policy", "allow"); err != nil {
		t.Fatalf("assignConfigValue returned error: %v", err)
	}
	if config.SandboxEscapePolicy != internalconfig.SandboxEscapePolicyAllow {
		t.Fatalf("expected external resource access policy %q, got %q", internalconfig.SandboxEscapePolicyAllow, config.SandboxEscapePolicy)
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
	if !strings.Contains(summary, "外部资源访问根目录:") {
		t.Fatalf("expected external resource root label, got %q", summary)
	}
}
