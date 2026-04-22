package cmd

import (
	"testing"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func newTestAppWithSessionStore(t *testing.T) *App {
	t.Helper()
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	store, err := agent.NewSessionStore()
	if err != nil {
		t.Fatalf("new session store failed: %v", err)
	}
	return &App{sessionStore: store}
}

func TestPrepareSessionKeepsSavedRequestModeWithoutOverride(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()
	state := agent.SessionState{FlowID: "demo-flow", Workdir: workdir, RequestMode: provider.RequestTypePlan}
	if err := app.sessionStore.Save(state); err != nil {
		t.Fatalf("save session failed: %v", err)
	}

	loaded, err := app.prepareSession("demo-flow", false, workdir, "")
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.RequestMode != provider.RequestTypePlan {
		t.Fatalf("expected saved request mode %q, got %q", provider.RequestTypePlan, loaded.RequestMode)
	}
}

func TestPrepareSessionDefaultsMissingSessionToAgentMode(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()

	loaded, err := app.prepareSession("demo-flow", false, workdir, "")
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.RequestMode != provider.RequestTypeAgent {
		t.Fatalf("expected default request mode %q, got %q", provider.RequestTypeAgent, loaded.RequestMode)
	}
}

func TestPrepareSessionNewSessionDefaultsToAgentMode(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()

	loaded, err := app.prepareSession("demo-flow", true, workdir, "")
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.RequestMode != provider.RequestTypeAgent {
		t.Fatalf("expected new session request mode %q, got %q", provider.RequestTypeAgent, loaded.RequestMode)
	}
}
