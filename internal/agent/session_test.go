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
		FlowID:                      "demo-flow",
		Workdir:                     tempDir,
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
		FlowID:    "demo-flow",
		Workdir:   tempDir,
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

func TestSessionStoreSaveLoadApprovedSkillNames(t *testing.T) {
	tempDir := t.TempDir()
	store := &SessionStore{rootDir: tempDir}

	state := SessionState{
		FlowID:             "demo-flow",
		Workdir:            tempDir,
		ApprovedSkillNames: []string{" Context Review ", "context review", "email-summary"},
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("save session failed: %v", err)
	}

	loaded, err := store.Load("demo-flow", tempDir)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if len(loaded.ApprovedSkillNames) != 2 {
		t.Fatalf("expected normalized approved skill names, got %#v", loaded.ApprovedSkillNames)
	}
	if loaded.ApprovedSkillNames[0] != "Context Review" || loaded.ApprovedSkillNames[1] != "email-summary" {
		t.Fatalf("unexpected approved skill names: %#v", loaded.ApprovedSkillNames)
	}
}

func TestSessionStoreSavePreservesExistingSessionTitleAliases(t *testing.T) {
	tempDir := t.TempDir()
	store := &SessionStore{rootDir: tempDir}

	initial := SessionState{
		FlowID:              "demo-flow",
		Workdir:             tempDir,
		SessionID:           "session-1",
		SessionTitle:        "旧标题",
		SessionTitleAliases: map[string]string{"session-1": "旧标题", "session-2": "历史标题"},
	}
	if err := store.Save(initial); err != nil {
		t.Fatalf("save initial session failed: %v", err)
	}

	updated := SessionState{
		FlowID:              "demo-flow",
		Workdir:             tempDir,
		SessionID:           "session-1",
		SessionTitle:        "新标题",
		SessionTitleAliases: map[string]string{"session-1": "新标题"},
	}
	if err := store.Save(updated); err != nil {
		t.Fatalf("save updated session failed: %v", err)
	}

	loaded, err := store.Load("demo-flow", tempDir)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if loaded.LocalSessionTitle("session-1") != "新标题" {
		t.Fatalf("expected updated alias for current session, got %#v", loaded.SessionTitleAliases)
	}
	if loaded.LocalSessionTitle("session-2") != "历史标题" {
		t.Fatalf("expected existing alias to be preserved, got %#v", loaded.SessionTitleAliases)
	}
}

func TestSessionStoreSaveLoadLastLineageIDs(t *testing.T) {
	tempDir := t.TempDir()
	store := &SessionStore{rootDir: tempDir}

	state := SessionState{
		FlowID:        "demo-flow",
		Workdir:       tempDir,
		SessionID:     "session-1",
		LastRunID:     "  run-1  ",
		LastMessageID: "  message-1  ",
	}
	if err := store.Save(state); err != nil {
		t.Fatalf("save session failed: %v", err)
	}

	loaded, err := store.Load("demo-flow", tempDir)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if loaded.LastRunID != "run-1" {
		t.Fatalf("expected trimmed last run id, got %#v", loaded)
	}
	if loaded.LastMessageID != "message-1" {
		t.Fatalf("expected trimmed last message id, got %#v", loaded)
	}
}
