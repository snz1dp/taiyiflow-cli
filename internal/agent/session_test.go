package agent

import (
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestSessionStoreSaveLoadSandboxPolicyOverride(t *testing.T) {
	tempDir := t.TempDir()
	store := &SessionStore{rootDir: tempDir}

	state := SessionState{
		FlowID:                       "demo-flow",
		Workdir:                      tempDir,
		SandboxEscapePolicyOverride: "ALLOW",
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("save session failed: %v", err)
	}

	loaded, err := store.Load("demo-flow", tempDir)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if loaded.SandboxEscapePolicyOverride != internalconfig.SandboxEscapePolicyAllow {
		t.Fatalf("expected normalized override %q, got %q", internalconfig.SandboxEscapePolicyAllow, loaded.SandboxEscapePolicyOverride)
	}
}

func TestSessionStoreLoadKeepsEmptySandboxPolicyOverride(t *testing.T) {
	tempDir := t.TempDir()
	store := &SessionStore{rootDir: tempDir}

	state := SessionState{
		FlowID:  "demo-flow",
		Workdir: tempDir,
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("save session failed: %v", err)
	}

	loaded, err := store.Load("demo-flow", tempDir)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if loaded.SandboxEscapePolicyOverride != "" {
		t.Fatalf("expected empty override, got %q", loaded.SandboxEscapePolicyOverride)
	}
}
