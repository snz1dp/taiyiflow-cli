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

func TestNormalizeAgentAutoLoopMaxRounds(t *testing.T) {
	cases := []struct {
		input    int
		expected int
	}{
		{input: DefaultAgentAutoLoopMaxRounds, expected: DefaultAgentAutoLoopMaxRounds},
		{input: 5, expected: 5},
		{input: 0, expected: DefaultAgentAutoLoopMaxRounds},
		{input: -1, expected: DefaultAgentAutoLoopMaxRounds},
	}
	for _, item := range cases {
		if got := NormalizeAgentAutoLoopMaxRounds(item.input); got != item.expected {
			t.Fatalf("NormalizeAgentAutoLoopMaxRounds(%d) = %d, want %d", item.input, got, item.expected)
		}
	}
}

func TestDefaultConfigIncludesAgentAutoLoopDefaults(t *testing.T) {
	config := Default()
	if config.BackendBaseURL != "http://localhost:4005" {
		t.Fatalf("expected default backend base url http://localhost:4005, got %q", config.BackendBaseURL)
	}
	if config.BackendAPIPrefix != DefaultBackendAPIPrefix {
		t.Fatalf("expected default backend api prefix %q, got %q", DefaultBackendAPIPrefix, config.BackendAPIPrefix)
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
}

func TestNormalizeBackendAPIPrefix(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{input: "", expected: DefaultBackendAPIPrefix},
		{input: "/api/v1", expected: DefaultBackendAPIPrefix},
		{input: "api/v1/", expected: DefaultBackendAPIPrefix},
		{input: "/taiyi/composer/api/v1/", expected: DefaultBackendAPIPrefix},
	}
	for _, item := range cases {
		if got := NormalizeBackendAPIPrefix(item.input); got != item.expected {
			t.Fatalf("NormalizeBackendAPIPrefix(%q) = %q, want %q", item.input, got, item.expected)
		}
	}
}

func TestManagerLoadTrimsAuthState(t *testing.T) {
	tempDir := t.TempDir()
	manager := &Manager{path: filepath.Join(tempDir, "taiyiflow.yaml")}

	content := []byte("backend_base_url: http://localhost:4005\npersistent_token: '  keep-me  '\ndevice_id: '  device-1  '\nauth_updated_at: '  2026-05-01T08:00:00Z  '\naccess_token: '  access-token  '\n")
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

	content := []byte("backend_base_url: http://localhost:7860\napproval_mode: confirm\ncommand_timeout_seconds: 999\n")
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
