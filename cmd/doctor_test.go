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