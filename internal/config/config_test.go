package config

import (
	"os"
	"path/filepath"
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
