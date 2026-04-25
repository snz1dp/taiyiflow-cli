package agent

import (
	"os"
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

func TestSessionStoreSaveLoadTodoItems(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.Setenv(internalconfig.RootDirEnvName(), tempDir); err != nil {
		t.Fatalf("set env failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Unsetenv(internalconfig.RootDirEnvName())
	})

	store, err := NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	state := SessionState{
		FlowID:  "demo-flow",
		Workdir: tempDir,
		TodoItems: []TodoItem{{Title: "实现 shell/task", Status: "in-progress"}},
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("save session failed: %v", err)
	}
	loaded, err := store.Load("demo-flow", tempDir)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if len(loaded.TodoItems) != 1 {
		t.Fatalf("expected one todo item, got %#v", loaded.TodoItems)
	}
	if loaded.TodoItems[0].Status != "in_progress" {
		t.Fatalf("expected normalized todo status, got %#v", loaded.TodoItems[0])
	}
}
