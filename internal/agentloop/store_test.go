package agentloop

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func TestRecoveryIntentSeparatesNetworkAndUserStop(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	if _, err := store.RecordPendingStart("flow-1", "session-1", "run-1", "client-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	state, err := store.RecordAcceptedExecution("session-1", "run-1", "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.PendingStart || state.ExecutionAttemptID != "attempt-1" {
		t.Fatalf("unexpected accepted state: %#v", state)
	}
	checkpoint := provider.AgentLoopCheckpoint{
		ID: "checkpoint-1", Version: 2, FlowID: "flow-1", SessionID: "session-1", RunID: "run-1",
		OriginClientID: "client-1", OriginDeviceID: "device-1", Status: "paused", PauseReason: "network",
	}
	state, err = store.SetResumeIntent(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if !state.AutoResumable() {
		t.Fatalf("expected network checkpoint to be resumable: %#v", state)
	}
	restarted := NewStore(store.root)
	restarted.SetClock(func() time.Time { return now })
	loaded, err := restarted.LoadRecovery("session-1")
	if err != nil || !loaded.AutoResumable() {
		t.Fatalf("expected recovery to survive restart: %#v, %v", loaded, err)
	}
	state, err = restarted.SetUserControlIntent("session-1", "run-1", IntentStopped)
	if err != nil {
		t.Fatal(err)
	}
	if state.AutoResumable() || !state.InvalidatedByUser {
		t.Fatalf("expected user stop to block recovery: %#v", state)
	}
	state, err = restarted.SetResumeIntent(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if state.AutoResumable() {
		t.Fatalf("expected notification after user stop to be ignored: %#v", state)
	}
}

func TestRecoveryAndNotificationClaimsExcludeDuplicateWorkers(t *testing.T) {
	store := NewStore(t.TempDir())
	claim, acquired, err := store.ClaimRecovery("checkpoint-1", 3)
	if err != nil || !acquired {
		t.Fatalf("expected first recovery claim: %v, %v", acquired, err)
	}
	other := NewStore(store.root)
	if _, acquired, err := other.ClaimRecovery("checkpoint-1", 3); err != nil || acquired {
		t.Fatalf("expected duplicate claim to be rejected: %v, %v", acquired, err)
	}
	if err := claim.Release(); err != nil {
		t.Fatal(err)
	}
	second, acquired, err := other.ClaimRecovery("checkpoint-1", 3)
	if err != nil || !acquired {
		t.Fatalf("expected claim after release: %v, %v", acquired, err)
	}
	defer second.Release()

	notification, acquired, err := store.BeginNotification("session-1", "notification-1")
	if err != nil || !acquired {
		t.Fatalf("expected first notification claim: %v, %v", acquired, err)
	}
	if err := notification.Complete(); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := other.BeginNotification("session-1", "notification-1"); err != nil || acquired {
		t.Fatalf("expected completed notification to stay deduplicated: %v, %v", acquired, err)
	}
}

func TestRecoveryClaimExpires(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	_, acquired, err := store.ClaimRecovery("checkpoint-1", 1)
	if err != nil || !acquired {
		t.Fatalf("expected initial claim: %v, %v", acquired, err)
	}
	path := filepath.Join(root, "claims", stateKey("checkpoint-1")+"-1.claim")
	old := time.Now().Add(-recoveryClaimTTL - time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	_, acquired, err = NewStore(root).ClaimRecovery("checkpoint-1", 1)
	if err != nil || !acquired {
		t.Fatalf("expected stale claim to be reclaimed: %v, %v", acquired, err)
	}
}

func TestToolCallbackQueuePreservesCreationAndRetries(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	createdAt := now.Add(-time.Hour)
	result := provider.ClientCallResult{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", CallID: "call-1", Success: true, Result: map[string]any{"private": "value"}}
	pending, err := store.EnqueueToolCallback(result, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	if !pending.CreatedAt.Equal(createdAt) {
		t.Fatalf("created_at changed: %s", pending.CreatedAt)
	}
	info, err := os.Stat(store.callbackPath("flow-1", "session-1", "run-1", "call-1"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected callback mode 0600, got %o", info.Mode().Perm())
	}
	pending, err = store.MarkToolCallbackAttempt("flow-1", "session-1", "run-1", "call-1")
	if err != nil {
		t.Fatal(err)
	}
	if pending.Attempts != 1 || !pending.CreatedAt.Equal(createdAt) || !pending.NextRetryAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("unexpected retry state: %#v", pending)
	}
	due, err := store.ListDueToolCallbacks(now)
	if err != nil || len(due) != 0 {
		t.Fatalf("callback should not be due yet: %#v, %v", due, err)
	}
	due, err = NewStore(store.root).ListDueToolCallbacks(now.Add(5 * time.Second))
	if err != nil || len(due) != 1 {
		t.Fatalf("callback should survive restart and become due: %#v, %v", due, err)
	}
	if _, err := store.EnqueueToolCallback(result, now); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadToolCallback("flow-1", "session-1", "run-1", "call-1")
	if err != nil || !loaded.CreatedAt.Equal(createdAt) {
		t.Fatalf("re-enqueue changed original creation time: %#v, %v", loaded, err)
	}
	if err := store.RemoveToolCallback("flow-1", "session-1", "run-1", "call-1"); err != nil {
		t.Fatal(err)
	}
}

func TestObserverCursorAndProtocolFailures(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.UpdateObserver("session-1", func(state *ObserverState) error {
		state.FlowID = "flow-1"
		state.RunID = "run-1"
		state.SubscriptionID = "subscription-1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err := store.AdvanceObserver("session-1", provider.StreamEvent{ID: "event-1"})
	if err != nil || state.AfterEventID != "event-1" {
		t.Fatalf("unexpected observer cursor: %#v, %v", state, err)
	}
	state, err = store.ResetObserverCursor("session-1")
	if err != nil || state.AfterEventID != "" || state.SubscriptionID != "" || !state.NeedsSnapshot {
		t.Fatalf("unexpected resync state: %#v, %v", state, err)
	}
	if !IsObserverProtocolViolation(provider.StreamEvent{Type: provider.EventClientTool}) {
		t.Fatal("observer must reject client tool events")
	}
	if ObserverBackoff(1) != time.Second || ObserverBackoff(20) != ObserverMaximumBackoff {
		t.Fatal("unexpected observer backoff bounds")
	}
	state, err = store.CloseObserver("session-1")
	if err != nil || !state.Closed {
		t.Fatalf("unexpected close state: %#v, %v", state, err)
	}
}

func TestUnsupportedStateVersionFailsClosed(t *testing.T) {
	store := NewStore(t.TempDir())
	path := store.recoveryPath("session-1")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":99,"flow_id":"flow-1","session_id":"session-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := store.LoadRecovery("session-1")
	if !errors.Is(err, ErrStateVersion) {
		t.Fatalf("expected version error, got %v", err)
	}
}
