package cmd

import (
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestParseConfigOverrideValid(t *testing.T) {
	o, err := parseConfigOverride("approval_mode=auto")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.Key != "approval_mode" || o.Value != "auto" {
		t.Fatalf("unexpected override: %#v", o)
	}
}

func TestParseConfigOverrideWithEqualsInValue(t *testing.T) {
	o, err := parseConfigOverride("backend_base_url=https://example.com/path?key=value")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if o.Key != "backend_base_url" {
		t.Fatalf("unexpected key: %q", o.Key)
	}
	if o.Value != "https://example.com/path?key=value" {
		t.Fatalf("unexpected value: %q", o.Value)
	}
}

func TestParseConfigOverrideRejectsMissingEquals(t *testing.T) {
	if _, err := parseConfigOverride("approval_mode"); err == nil {
		t.Fatal("expected error for missing =")
	}
}

func TestParseConfigOverrideRejectsEmptyKey(t *testing.T) {
	if _, err := parseConfigOverride("=value"); err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestApplyConfigOverridesSingle(t *testing.T) {
	config := internalconfig.Default()
	overrides := []configOverride{{Key: "approval_mode", Value: "auto"}}
	updated, err := applyConfigOverrides(config, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.ApprovalMode != "auto" {
		t.Fatalf("expected approval_mode=auto, got %q", updated.ApprovalMode)
	}
}

func TestApplyConfigOverridesMultiple(t *testing.T) {
	config := internalconfig.Default()
	overrides := []configOverride{
		{Key: "approval_mode", Value: "readonly"},
		{Key: "reasoning_enabled", Value: "true"},
		{Key: "command_timeout_seconds", Value: "120"},
	}
	updated, err := applyConfigOverrides(config, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.ApprovalMode != "readonly" {
		t.Fatalf("expected approval_mode=readonly, got %q", updated.ApprovalMode)
	}
	if !updated.ReasoningEnabled {
		t.Fatal("expected reasoning_enabled=true")
	}
	if updated.CommandTimeoutSeconds != 120 {
		t.Fatalf("expected command_timeout_seconds=120, got %d", updated.CommandTimeoutSeconds)
	}
}

func TestApplyConfigOverridesRejectsInvalidValue(t *testing.T) {
	config := internalconfig.Default()
	overrides := []configOverride{{Key: "approval_mode", Value: "invalid"}}
	if _, err := applyConfigOverrides(config, overrides); err == nil {
		t.Fatal("expected error for invalid approval_mode value")
	}
}

func TestApplyConfigOverridesRejectsUnknownKey(t *testing.T) {
	config := internalconfig.Default()
	overrides := []configOverride{{Key: "nonexistent_key", Value: "value"}}
	if _, err := applyConfigOverrides(config, overrides); err == nil {
		t.Fatal("expected error for unknown config key")
	}
}

func TestApplyConfigOverridesDoesNotMutateOriginal(t *testing.T) {
	config := internalconfig.Default()
	originalMode := config.ApprovalMode
	overrides := []configOverride{{Key: "approval_mode", Value: "auto"}}
	_, err := applyConfigOverrides(config, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.ApprovalMode != originalMode {
		t.Fatalf("expected original config to remain unchanged, got %q", config.ApprovalMode)
	}
}

func TestStringArrayFlagSet(t *testing.T) {
	var flag stringArrayFlag
	if err := flag.Set("a=1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := flag.Set("b=2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(flag) != 2 || flag[0] != "a=1" || flag[1] != "b=2" {
		t.Fatalf("unexpected flag values: %#v", flag)
	}
}

func TestParseConfigOverridesFromFlag(t *testing.T) {
	raw := []string{"approval_mode=auto", "reasoning_enabled=true"}
	overrides, err := parseConfigOverridesFromFlag(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(overrides) != 2 {
		t.Fatalf("expected 2 overrides, got %d", len(overrides))
	}
	if overrides[0].Key != "approval_mode" || overrides[0].Value != "auto" {
		t.Fatalf("unexpected first override: %#v", overrides[0])
	}
	if overrides[1].Key != "reasoning_enabled" || overrides[1].Value != "true" {
		t.Fatalf("unexpected second override: %#v", overrides[1])
	}
}

func TestParseConfigOverridesFromFlagRejectsInvalid(t *testing.T) {
	raw := []string{"approval_mode=auto", "invalid-no-equals"}
	if _, err := parseConfigOverridesFromFlag(raw); err == nil {
		t.Fatal("expected error for invalid override format")
	}
}

func TestApplyConfigOverridesWithNormalizedKeyAlias(t *testing.T) {
	config := internalconfig.Default()
	overrides := []configOverride{{Key: "cross-session-memory", Value: "true"}}
	updated, err := applyConfigOverrides(config, overrides)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !updated.CrossSessionMemoryEnabled {
		t.Fatal("expected cross_session_memory_enabled=true via alias")
	}
}
