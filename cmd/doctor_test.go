package cmd

import (
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestDetectBrowserAutomationStatusUsesChineseLabels(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())
	config := internalconfig.Config{
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeChromeSession,
			PermissionMode: internalconfig.BrowserPermissionModeAsk,
			Channel:        internalconfig.BrowserChannelEdge,
			ExtensionSetup: internalconfig.BrowserExtensionSetupNative,
		},
	}

	message := detectBrowserAutomationStatus(config)
	for _, expected := range []string{
		"浏览器模式: 当前会话",
		"权限模式: 逐次确认",
		"浏览器通道: Edge",
		"扩展接入: 本地消息",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("expected %q in %q", expected, message)
		}
	}
	for _, unexpected := range []string{"mode=", "permission=", "extension="} {
		if strings.Contains(message, unexpected) {
			t.Fatalf("did not expect %q in %q", unexpected, message)
		}
	}
}

func TestDetectComputerAutomationStatusUsesChineseLabels(t *testing.T) {
	config := internalconfig.Config{
		Computer: internalconfig.ComputerConfig{
			PermissionMode: internalconfig.ComputerPermissionModeAsk,
		},
	}

	message := detectComputerAutomationStatus(config)
	for _, expected := range []string{
		"开关: 开启",
		"权限模式: 逐次确认",
		"状态:",
		"平台:",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("expected %q in %q", expected, message)
		}
	}
	for _, unexpected := range []string{"permission_mode=", "desktop_permission="} {
		if strings.Contains(message, unexpected) {
			t.Fatalf("did not expect %q in %q", unexpected, message)
		}
	}
}

func TestDetectComputerAutomationStatusUsesSkipAllLabel(t *testing.T) {
	config := internalconfig.Config{
		Computer: internalconfig.ComputerConfig{
			PermissionMode: internalconfig.ComputerPermissionModeSkipAll,
		},
	}

	message := detectComputerAutomationStatus(config)
	if !strings.Contains(message, "权限模式: 始终允许") {
		t.Fatalf("expected skip all label in %q", message)
	}
}

func TestDetectBrowserAutomationStatusWhenDisabled(t *testing.T) {
	config := internalconfig.Config{
		Browser: internalconfig.BrowserConfig{Mode: internalconfig.BrowserModeDisabled},
	}

	message := detectBrowserAutomationStatus(config)
	for _, expected := range []string{
		"浏览器模式: 已禁用",
		"状态: 不可用",
		"浏览器自动化已在配置中禁用",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("expected %q in %q", expected, message)
		}
	}
	for _, unexpected := range []string{"权限模式:", "浏览器通道:", "扩展接入:"} {
		if strings.Contains(message, unexpected) {
			t.Fatalf("did not expect %q in %q", unexpected, message)
		}
	}
}
