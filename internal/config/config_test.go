package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeSandboxEscapePolicy(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: SandboxEscapePolicyConfirm},
		{input: "confirm", expected: SandboxEscapePolicyConfirm},
		{input: "deny", expected: SandboxEscapePolicyDeny},
		{input: "allow", expected: SandboxEscapePolicyAllow},
		{input: "ALLOW", expected: SandboxEscapePolicyAllow},
		{input: "unexpected", expected: SandboxEscapePolicyConfirm},
	}
	for _, item := range cases {
		if got := NormalizeSandboxEscapePolicy(item.input); got != item.expected {
			t.Fatalf("NormalizeSandboxEscapePolicy(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestNormalizeSkillReviewPolicy(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: SkillReviewPolicyConfirm},
		{input: "confirm", expected: SkillReviewPolicyConfirm},
		{input: "ignore", expected: SkillReviewPolicyIgnore},
		{input: "IGNORE", expected: SkillReviewPolicyIgnore},
		{input: "unexpected", expected: SkillReviewPolicyConfirm},
	}
	for _, item := range cases {
		if got := NormalizeSkillReviewPolicy(item.input); got != item.expected {
			t.Fatalf("NormalizeSkillReviewPolicy(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestNormalizeWriteConfirmationPolicy(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: WriteConfirmationPolicyConfirm},
		{input: "confirm", expected: WriteConfirmationPolicyConfirm},
		{input: "direct", expected: WriteConfirmationPolicyDirect},
		{input: "DIRECT", expected: WriteConfirmationPolicyDirect},
		{input: "unexpected", expected: WriteConfirmationPolicyConfirm},
	}
	for _, item := range cases {
		if got := NormalizeWriteConfirmationPolicy(item.input); got != item.expected {
			t.Fatalf("NormalizeWriteConfirmationPolicy(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestNormalizeDocUnstructuredExts(t *testing.T) {
	values := NormalizeDocUnstructuredExts([]string{".PDF", " pdf ", "docx", ".docx", "", " .Md ", "png", ".jpeg"})
	if len(values) != 3 {
		t.Fatalf("expected 3 normalized extensions, got %#v", values)
	}
	if values[0] != "pdf" || values[1] != "docx" || values[2] != "md" {
		t.Fatalf("unexpected normalized extensions: %#v", values)
	}
}

func TestNormalizeSkillAutoInstallPolicy(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: SkillAutoInstallPolicyOff},
		{input: "confirm", expected: SkillAutoInstallPolicyConfirm},
		{input: "off", expected: SkillAutoInstallPolicyOff},
		{input: "allow", expected: SkillAutoInstallPolicyAllow},
		{input: "ALLOW", expected: SkillAutoInstallPolicyAllow},
		{input: "unexpected", expected: SkillAutoInstallPolicyOff},
	}
	for _, item := range cases {
		if got := NormalizeSkillAutoInstallPolicy(item.input); got != item.expected {
			t.Fatalf("NormalizeSkillAutoInstallPolicy(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestNormalizeBrowserMode(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: BrowserModeChromeSession},
		{input: "disabled", expected: BrowserModeDisabled},
		{input: "chrome_session", expected: BrowserModeChromeSession},
		{input: "isolated", expected: BrowserModeIsolated},
		{input: "ISOLATED", expected: BrowserModeIsolated},
		{input: "unexpected", expected: BrowserModeChromeSession},
	}
	for _, item := range cases {
		if got := NormalizeBrowserMode(item.input); got != item.expected {
			t.Fatalf("NormalizeBrowserMode(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestBrowserEnabled(t *testing.T) {
	if !BrowserEnabled(BrowserConfig{}) {
		t.Fatal("expected zero-value browser config to remain enabled")
	}
	if BrowserEnabled(BrowserConfig{Mode: BrowserModeDisabled}) {
		t.Fatal("expected disabled browser mode to report disabled")
	}
}

func TestNormalizeBrowserPermissionMode(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: BrowserPermissionModeAsk},
		{input: "ask", expected: BrowserPermissionModeAsk},
		{input: "follow_a_plan", expected: BrowserPermissionModeFollowPlan},
		{input: "skip_all_permission_checks", expected: BrowserPermissionModeSkipAll},
		{input: "unexpected", expected: BrowserPermissionModeAsk},
	}
	for _, item := range cases {
		if got := NormalizeBrowserPermissionMode(item.input); got != item.expected {
			t.Fatalf("NormalizeBrowserPermissionMode(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestNormalizeComputerPermissionMode(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: ComputerPermissionModeAsk},
		{input: "ask", expected: ComputerPermissionModeAsk},
		{input: "follow_a_plan", expected: ComputerPermissionModeFollowPlan},
		{input: "skip_all_permission_checks", expected: ComputerPermissionModeSkipAll},
		{input: "unexpected", expected: ComputerPermissionModeAsk},
	}
	for _, item := range cases {
		if got := NormalizeComputerPermissionMode(item.input); got != item.expected {
			t.Fatalf("NormalizeComputerPermissionMode(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestNormalizeBrowserChannel(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: BrowserChannelChrome},
		{input: "chromium", expected: BrowserChannelChromium},
		{input: "edge", expected: BrowserChannelEdge},
		{input: "brave", expected: BrowserChannelBrave},
		{input: "unexpected", expected: BrowserChannelChrome},
	}
	for _, item := range cases {
		if got := NormalizeBrowserChannel(item.input); got != item.expected {
			t.Fatalf("NormalizeBrowserChannel(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestNormalizeBrowserExtensionSetup(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: BrowserExtensionSetupNative},
		{input: "native_messaging", expected: BrowserExtensionSetupNative},
		{input: "manual", expected: BrowserExtensionSetupManual},
		{input: "none", expected: BrowserExtensionSetupNone},
		{input: "unexpected", expected: BrowserExtensionSetupNative},
	}
	for _, item := range cases {
		if got := NormalizeBrowserExtensionSetup(item.input); got != item.expected {
			t.Fatalf("NormalizeBrowserExtensionSetup(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestNormalizeBrowserConnectTimeoutSeconds(t *testing.T) {
	cases := []struct {
		input    int
		expected int
	}{
		{input: 0, expected: DefaultBrowserConnectTimeout},
		{input: 1, expected: 1},
		{input: MaxBrowserConnectTimeout + 1, expected: MaxBrowserConnectTimeout},
		{input: -1, expected: DefaultBrowserConnectTimeout},
	}
	for _, item := range cases {
		if got := NormalizeBrowserConnectTimeoutSeconds(item.input); got != item.expected {
			t.Fatalf("NormalizeBrowserConnectTimeoutSeconds(%d) = %d, want %d", item.input, got, item.expected)
		}
	}
}

func TestNormalizeBrowserConfig(t *testing.T) {
	got := NormalizeBrowserConfig(BrowserConfig{
		Mode:                  "isolated",
		PermissionMode:        "skip_all_permission_checks",
		Channel:               "edge",
		ExecutablePath:        "  /Applications/Google Chrome.app/Contents/MacOS/Google Chrome  ",
		ProfileDir:            "  /tmp/profile  ",
		ConnectTimeoutSeconds: MaxBrowserConnectTimeout + 10,
		ExtensionSetup:        "manual",
		SocketDir:             "  /tmp/socket  ",
		RemoteDebuggingURL:    "  http://127.0.0.1:9222  ",
	})
	if got.Mode != BrowserModeIsolated {
		t.Fatalf("expected normalized browser mode %q, got %q", BrowserModeIsolated, got.Mode)
	}
	if got.PermissionMode != BrowserPermissionModeSkipAll {
		t.Fatalf("expected normalized browser permission mode %q, got %q", BrowserPermissionModeSkipAll, got.PermissionMode)
	}
	if got.Channel != BrowserChannelEdge {
		t.Fatalf("expected normalized browser channel %q, got %q", BrowserChannelEdge, got.Channel)
	}
	if got.ExecutablePath != "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
		t.Fatalf("expected trimmed executable path, got %q", got.ExecutablePath)
	}
	if got.ProfileDir != "/tmp/profile" {
		t.Fatalf("expected trimmed profile dir, got %q", got.ProfileDir)
	}
	if got.ConnectTimeoutSeconds != MaxBrowserConnectTimeout {
		t.Fatalf("expected normalized browser timeout %d, got %d", MaxBrowserConnectTimeout, got.ConnectTimeoutSeconds)
	}
	if got.ExtensionSetup != BrowserExtensionSetupManual {
		t.Fatalf("expected normalized browser extension setup %q, got %q", BrowserExtensionSetupManual, got.ExtensionSetup)
	}
	if got.SocketDir != "/tmp/socket" {
		t.Fatalf("expected trimmed socket dir, got %q", got.SocketDir)
	}
	if got.RemoteDebuggingURL != "http://127.0.0.1:9222" {
		t.Fatalf("expected trimmed remote debugging url, got %q", got.RemoteDebuggingURL)
	}
}

func TestNormalizeStreamIdleTimeoutSeconds(t *testing.T) {
	cases := []struct {
		input    int
		expected int
	}{
		{input: DefaultStreamIdleTimeoutSeconds, expected: DefaultStreamIdleTimeoutSeconds},
		{input: 0, expected: 0},
		{input: 300, expected: 300},
		{input: -1, expected: DefaultStreamIdleTimeoutSeconds},
	}
	for _, item := range cases {
		if got := NormalizeStreamIdleTimeoutSeconds(item.input); got != item.expected {
			t.Fatalf("NormalizeStreamIdleTimeoutSeconds(%d) = %d, want %d", item.input, got, item.expected)
		}
	}
}

func TestNormalizeCommandTimeoutSeconds(t *testing.T) {
	cases := []struct {
		input    int
		expected int
	}{
		{input: DefaultCommandTimeoutSeconds, expected: DefaultCommandTimeoutSeconds},
		{input: 1, expected: 1},
		{input: MaxCommandTimeoutSeconds + 1, expected: MaxCommandTimeoutSeconds},
		{input: 0, expected: DefaultCommandTimeoutSeconds},
		{input: -1, expected: DefaultCommandTimeoutSeconds},
	}
	for _, item := range cases {
		if got := NormalizeCommandTimeoutSeconds(item.input); got != item.expected {
			t.Fatalf("NormalizeCommandTimeoutSeconds(%d) = %d, want %d", item.input, got, item.expected)
		}
	}
}

func TestNormalizeHTTPTimeoutSeconds(t *testing.T) {
	cases := []struct {
		input    int
		expected int
	}{
		{input: DefaultHTTPTimeoutSeconds, expected: DefaultHTTPTimeoutSeconds},
		{input: 1, expected: 1},
		{input: MaxHTTPTimeoutSeconds + 1, expected: MaxHTTPTimeoutSeconds},
		{input: 0, expected: DefaultHTTPTimeoutSeconds},
		{input: -1, expected: DefaultHTTPTimeoutSeconds},
	}
	for _, item := range cases {
		if got := NormalizeHTTPTimeoutSeconds(item.input); got != item.expected {
			t.Fatalf("NormalizeHTTPTimeoutSeconds(%d) = %d, want %d", item.input, got, item.expected)
		}
	}
}

func TestNormalizeAgentAutoLoopMaxRounds(t *testing.T) {
	cases := []struct {
		input    int
		expected int
	}{
		{input: DefaultAgentAutoLoopMaxRounds, expected: DefaultAgentAutoLoopMaxRounds},
		{input: 5, expected: 5},
		{input: 0, expected: 0},
		{input: -1, expected: DefaultAgentAutoLoopMaxRounds},
		{input: MaxAgentAutoLoopMaxRounds + 1, expected: MaxAgentAutoLoopMaxRounds},
	}
	for _, item := range cases {
		if got := NormalizeAgentAutoLoopMaxRounds(item.input); got != item.expected {
			t.Fatalf("NormalizeAgentAutoLoopMaxRounds(%d) = %d, want %d", item.input, got, item.expected)
		}
	}
}

func TestDefaultConfigIncludesAgentAutoLoopDefaults(t *testing.T) {
	config := Default()
	if config.BackendBaseURL != "https://snz1.cn" {
		t.Fatalf("expected default backend base url https://snz1.cn, got %q", config.BackendBaseURL)
	}
	if config.BackendAPIPrefix != DefaultBackendAPIPrefix {
		t.Fatalf("expected default backend api prefix %q, got %q", DefaultBackendAPIPrefix, config.BackendAPIPrefix)
	}
	if config.HTTPTimeoutSeconds != DefaultHTTPTimeoutSeconds {
		t.Fatalf("expected default http timeout %d, got %d", DefaultHTTPTimeoutSeconds, config.HTTPTimeoutSeconds)
	}
	if config.AgentAutoLoopEnabled {
		t.Fatal("expected agent auto loop to default to disabled")
	}
	if config.AgentAutoLoopMaxRounds != DefaultAgentAutoLoopMaxRounds {
		t.Fatalf("expected default agent auto loop max rounds %d, got %d", DefaultAgentAutoLoopMaxRounds, config.AgentAutoLoopMaxRounds)
	}
	if config.Skills.AutoInstallPolicy != SkillAutoInstallPolicyOff {
		t.Fatalf("expected default skill auto install policy %q, got %q", SkillAutoInstallPolicyOff, config.Skills.AutoInstallPolicy)
	}
	if config.Skills.FreeTextSearchEnabled {
		t.Fatal("expected default free text skill search to be disabled")
	}
	if config.CrossSessionMemoryEnabled {
		t.Fatal("expected cross session memory to default to disabled")
	}
	if config.Browser.Mode != BrowserModeChromeSession {
		t.Fatalf("expected default browser mode %q, got %q", BrowserModeChromeSession, config.Browser.Mode)
	}
	if config.Browser.PermissionMode != BrowserPermissionModeAsk {
		t.Fatalf("expected default browser permission mode %q, got %q", BrowserPermissionModeAsk, config.Browser.PermissionMode)
	}
	if config.Browser.Channel != BrowserChannelChrome {
		t.Fatalf("expected default browser channel %q, got %q", BrowserChannelChrome, config.Browser.Channel)
	}
	if config.Browser.ConnectTimeoutSeconds != DefaultBrowserConnectTimeout {
		t.Fatalf("expected default browser timeout %d, got %d", DefaultBrowserConnectTimeout, config.Browser.ConnectTimeoutSeconds)
	}
	if strings.Join(config.DocUnstructuredExts, ",") != strings.Join(defaultDocUnstructuredExts, ",") {
		t.Fatalf("expected default doc_unstructured_exts %#v, got %#v", defaultDocUnstructuredExts, config.DocUnstructuredExts)
	}
}

func TestManagerLoadMissingFileUsesDefaultDocUnstructuredExts(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if strings.Join(loaded.DocUnstructuredExts, ",") != strings.Join(defaultDocUnstructuredExts, ",") {
		t.Fatalf("expected default doc_unstructured_exts %#v, got %#v", defaultDocUnstructuredExts, loaded.DocUnstructuredExts)
	}
}
func TestManagerLoadReadsCrossSessionMemoryEnabled(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:4005\ncross_session_memory_enabled: true\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if !loaded.CrossSessionMemoryEnabled {
		t.Fatal("expected cross_session_memory_enabled to load as true")
	}
}

func TestManagerLoadNormalizesDocUnstructuredExts(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:4005\ndoc_unstructured_exts:\n  - .PDF\n  - pdf\n  - docx\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if len(loaded.DocUnstructuredExts) != 2 || loaded.DocUnstructuredExts[0] != "pdf" || loaded.DocUnstructuredExts[1] != "docx" {
		t.Fatalf("expected normalized doc_unstructured_exts, got %#v", loaded.DocUnstructuredExts)
	}
}

func TestNormalizeBackendAPIPrefix(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: DefaultBackendAPIPrefix},
		{input: "/api/v1", expected: DefaultBackendAPIPrefix},
		{input: "api/v1/", expected: DefaultBackendAPIPrefix},
		{input: DefaultBackendAPIPrefix + "/", expected: DefaultBackendAPIPrefix},
	}
	for _, item := range cases {
		if got := NormalizeBackendAPIPrefix(item.input); got != item.expected {
			t.Fatalf("NormalizeBackendAPIPrefix(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestNormalizeAuthAPIPrefix(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: DefaultAuthAPIPrefix},
		{input: "xeai/", expected: DefaultAuthAPIPrefix},
		{input: "/custom-auth/", expected: "/custom-auth"},
	}
	for _, item := range cases {
		if got := NormalizeAuthAPIPrefix(item.input); got != item.expected {
			t.Fatalf("NormalizeAuthAPIPrefix(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestManagerLoadTrimsAuthState(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:4005\npersistent_token: '  keep-me  '\nclient_id: '  client-1  '\ndevice_id: '  device-1  '\nauth_updated_at: '  2026-05-01T08:00:00Z  '\naccess_token: '  access-token  '\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if loaded.PersistentToken != "keep-me" {
		t.Fatalf("expected trimmed persistent token, got %q", loaded.PersistentToken)
	}
	if loaded.ClientID != "client-1" {
		t.Fatalf("expected trimmed client id, got %q", loaded.ClientID)
	}
	if loaded.DeviceID != "device-1" {
		t.Fatalf("expected trimmed device id, got %q", loaded.DeviceID)
	}
	if loaded.AuthUpdatedAt != "2026-05-01T08:00:00Z" {
		t.Fatalf("expected trimmed auth timestamp, got %q", loaded.AuthUpdatedAt)
	}
	if loaded.AccessToken != "access-token" {
		t.Fatalf("expected trimmed access token, got %q", loaded.AccessToken)
	}
}

func TestManagerSaveWritesAccessTokenField(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	config := Default()
	config.AccessToken = "access-token"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	content, err := os.ReadFile(manager.path)
	if err != nil {
		t.Fatalf("read config failed: %v", err)
	}
	serialized := string(content)
	if !strings.Contains(serialized, "access_token:") {
		t.Fatalf("expected serialized config to use access_token, got %q", serialized)
	}
	if strings.Contains(serialized, "access-token") {
		t.Fatalf("expected serialized config not to contain plaintext access token, got %q", serialized)
	}
	if !strings.Contains(serialized, encryptedConfigValuePrefix) {
		t.Fatalf("expected serialized config to use encrypted token marker, got %q", serialized)
	}
}

func TestManagerSaveLoadEncryptsAndDecryptsAuthTokens(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	config := Default()
	config.AccessToken = "access-token"
	config.PersistentToken = "persistent-token"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if loaded.AccessToken != "access-token" {
		t.Fatalf("expected decrypted access token, got %q", loaded.AccessToken)
	}
	if loaded.PersistentToken != "persistent-token" {
		t.Fatalf("expected decrypted persistent token, got %q", loaded.PersistentToken)
	}
}

func TestManagerLoadKeepsPlaintextAuthTokensForBackwardCompatibility(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:4005\npersistent_token: persist-plain\naccess_token: access-plain\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if loaded.AccessToken != "access-plain" {
		t.Fatalf("expected plaintext access token to still load, got %q", loaded.AccessToken)
	}
	if loaded.PersistentToken != "persist-plain" {
		t.Fatalf("expected plaintext persistent token to still load, got %q", loaded.PersistentToken)
	}
}

func TestManagerLoadRejectsBrokenEncryptedAuthToken(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:4005\naccess_token: enc:v1:not-base64\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	if _, err := manager.Load(); err == nil || !strings.Contains(err.Error(), "解密 access_token 失败") {
		t.Fatalf("expected encrypted token decode failure, got %v", err)
	}
}

func TestManagerLoadAppliesDefaultSandboxEscapePolicy(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:7860\napproval_mode: confirm\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if loaded.SandboxEscapePolicy != SandboxEscapePolicyConfirm {
		t.Fatalf("expected default sandbox policy %q, got %q", SandboxEscapePolicyConfirm, loaded.SandboxEscapePolicy)
	}
}

func TestManagerLoadNormalizesInvalidSandboxEscapePolicy(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:7860\napproval_mode: confirm\nsandbox_escape_policy: unknown\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if loaded.SandboxEscapePolicy != SandboxEscapePolicyConfirm {
		t.Fatalf("expected normalized sandbox policy %q, got %q", SandboxEscapePolicyConfirm, loaded.SandboxEscapePolicy)
	}
}

func TestManagerLoadAppliesDefaultStreamIdleTimeoutSeconds(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:7860\napproval_mode: confirm\nstream_idle_timeout_seconds: -5\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if loaded.StreamIdleTimeoutSeconds != DefaultStreamIdleTimeoutSeconds {
		t.Fatalf("expected default stream idle timeout %d, got %d", DefaultStreamIdleTimeoutSeconds, loaded.StreamIdleTimeoutSeconds)
	}
}

func TestManagerLoadNormalizesInvalidCommandTimeoutSeconds(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:7860\napproval_mode: confirm\ncommand_timeout_seconds: 9999\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if loaded.CommandTimeoutSeconds != MaxCommandTimeoutSeconds {
		t.Fatalf("expected normalized command timeout %d, got %d", MaxCommandTimeoutSeconds, loaded.CommandTimeoutSeconds)
	}
}

func TestManagerLoadNormalizesInvalidHTTPTimeoutSeconds(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:7860\napproval_mode: confirm\nhttp_timeout_seconds: 9999\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if loaded.HTTPTimeoutSeconds != MaxHTTPTimeoutSeconds {
		t.Fatalf("expected normalized http timeout %d, got %d", MaxHTTPTimeoutSeconds, loaded.HTTPTimeoutSeconds)
	}
}

func TestManagerLoadNormalizesBrowserConfig(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:7860\nbrowser:\n  mode: unexpected\n  permission_mode: unexpected\n  channel: edge\n  executable_path: '  /Applications/Chrome  '\n  profile_dir: '  /tmp/profile  '\n  connect_timeout_seconds: 999\n  extension_setup: manual\n  socket_dir: '  /tmp/socket  '\n  remote_debugging_url: '  http://127.0.0.1:9222  '\n")
	if err := os.WriteFile(manager.path, content, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	loaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if loaded.Browser.Mode != BrowserModeChromeSession {
		t.Fatalf("expected normalized browser mode %q, got %q", BrowserModeChromeSession, loaded.Browser.Mode)
	}
	if loaded.Browser.PermissionMode != BrowserPermissionModeAsk {
		t.Fatalf("expected normalized browser permission mode %q, got %q", BrowserPermissionModeAsk, loaded.Browser.PermissionMode)
	}
	if loaded.Browser.Channel != BrowserChannelEdge {
		t.Fatalf("expected normalized browser channel %q, got %q", BrowserChannelEdge, loaded.Browser.Channel)
	}
	if loaded.Browser.ExecutablePath != "/Applications/Chrome" {
		t.Fatalf("expected trimmed executable path, got %q", loaded.Browser.ExecutablePath)
	}
	if loaded.Browser.ProfileDir != "/tmp/profile" {
		t.Fatalf("expected trimmed profile dir, got %q", loaded.Browser.ProfileDir)
	}
	if loaded.Browser.ConnectTimeoutSeconds != MaxBrowserConnectTimeout {
		t.Fatalf("expected normalized browser timeout %d, got %d", MaxBrowserConnectTimeout, loaded.Browser.ConnectTimeoutSeconds)
	}
	if loaded.Browser.ExtensionSetup != BrowserExtensionSetupManual {
		t.Fatalf("expected normalized browser extension setup %q, got %q", BrowserExtensionSetupManual, loaded.Browser.ExtensionSetup)
	}
	if loaded.Browser.SocketDir != "/tmp/socket" {
		t.Fatalf("expected trimmed socket dir, got %q", loaded.Browser.SocketDir)
	}
	if loaded.Browser.RemoteDebuggingURL != "http://127.0.0.1:9222" {
		t.Fatalf("expected trimmed remote debugging url, got %q", loaded.Browser.RemoteDebuggingURL)
	}
}

func TestDefaultBundledSkillsDirUsesRootDir(t *testing.T) {
	t.Setenv(RootDirEnvName(), t.TempDir())
	bundledDir, err := DefaultBundledSkillsDir()
	if err != nil {
		t.Fatalf("DefaultBundledSkillsDir failed: %v", err)
	}
	if filepath.Base(bundledDir) != "bundled-skills" {
		t.Fatalf("unexpected bundled skills dir: %s", bundledDir)
	}
}
