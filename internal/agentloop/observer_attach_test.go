package agentloop

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func observerEvents(events ...provider.StreamEvent) <-chan provider.StreamEvent {
	stream := make(chan provider.StreamEvent, len(events))
	for _, event := range events {
		stream <- event
	}
	close(stream)
	return stream
}

func observerMetadata() *provider.SessionExecutionMetadata {
	return &provider.SessionExecutionMetadata{
		FlowID: "flow-1", SessionID: "session-1", Executing: true, ExecutingRunID: "run-1",
		ExecutionAttemptID: "attempt-1", OriginClientID: "client-1", OriginDeviceID: "device-1",
	}
}

func TestAttachContinuesCursorWithoutReplayingSnapshot(t *testing.T) {
	store := NewStore(t.TempDir())
	first := observerEvents(provider.StreamEvent{ID: "event-1", Type: provider.EventMessage, Data: map[string]any{"name": "message", "run_id": "run-1"}})
	second := observerEvents(provider.StreamEvent{ID: "event-2", Type: provider.EventClose, Data: map[string]any{"name": "close", "run_id": "run-1"}})
	fake := &fakeProvider{execution: observerMetadata(), streams: []<-chan provider.StreamEvent{first, second}}
	controller := NewController(fake, store)
	controller.Sleep = func(context.Context, time.Duration) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	snapshots := 0
	err := controller.Attach(ctx, ObserverRequest{SessionID: "session-1", ClientID: "client-1"}, ObserverHandlers{
		OnSnapshot: func(*provider.SessionMessageList) error { snapshots++; return nil },
		OnEvent: func(provider.StreamEvent) error {
			cancel()
			return nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected local observer cancellation, got %v", err)
	}
	if len(fake.controlRequests) != 0 {
		t.Fatal("detaching an observer must not cancel the server execution")
	}
	if err := controller.Attach(context.Background(), ObserverRequest{SessionID: "session-1", ClientID: "client-1"}, ObserverHandlers{
		OnSnapshot: func(*provider.SessionMessageList) error { snapshots++; return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 {
		t.Fatalf("cursor continuation must not reload a full snapshot: %d", snapshots)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("unexpected attach requests: %#v", fake.requests)
	}
	for _, request := range fake.requests {
		if request.ExecutionMode != provider.ExecutionModeAttach || request.ClientID != "client-1:observer" || request.Input != "" {
			t.Fatalf("unexpected read-only request: %#v", request)
		}
	}
	if fake.requests[0].AfterEventID != "" || fake.requests[1].AfterEventID != "event-1" {
		t.Fatalf("cursor was not continued: %#v", fake.requests)
	}
	state, err := store.LoadObserver("session-1")
	if err != nil || !state.Closed || state.CleanCloses != 1 {
		t.Fatalf("unexpected closed observer: %#v, %v", state, err)
	}
}

func TestAttachResyncClearsCursorAndReloadsSnapshot(t *testing.T) {
	store := NewStore(t.TempDir())
	first := observerEvents(
		provider.StreamEvent{ID: "event-1", Type: provider.EventMessage, Data: map[string]any{"name": "message"}},
		provider.StreamEvent{ID: "event-2", Type: provider.EventMessage, Data: map[string]any{"name": "resync_required"}},
	)
	second := observerEvents(provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{"name": "close"}})
	fake := &fakeProvider{execution: observerMetadata(), streams: []<-chan provider.StreamEvent{first, second}}
	controller := NewController(fake, store)
	controller.Sleep = func(context.Context, time.Duration) error { return nil }
	snapshots := 0
	if err := controller.Attach(context.Background(), ObserverRequest{SessionID: "session-1"}, ObserverHandlers{
		OnSnapshot: func(*provider.SessionMessageList) error { snapshots++; return nil },
	}); err != nil {
		t.Fatal(err)
	}
	if snapshots != 2 {
		t.Fatalf("resync must reload the complete snapshot: %d", snapshots)
	}
	if len(fake.requests) != 2 || fake.requests[1].AfterEventID != "" || fake.requests[1].SubscriptionID == "" || fake.requests[1].SubscriptionID == fake.requests[0].SubscriptionID {
		t.Fatalf("resync must reset cursor and subscription: %#v", fake.requests)
	}
	state, err := store.LoadObserver("session-1")
	if err != nil || state.ConsecutiveFailures != 0 {
		t.Fatalf("protocol resync must not count as a network failure: %#v, %v", state, err)
	}
}

func TestAttachRejectsToolEvents(t *testing.T) {
	store := NewStore(t.TempDir())
	stream := observerEvents(provider.StreamEvent{Type: provider.EventClientTool, Data: map[string]any{"call_id": "call-1"}})
	fake := &fakeProvider{execution: observerMetadata(), stream: stream}
	controller := NewController(fake, store)
	called := false
	err := controller.Attach(context.Background(), ObserverRequest{SessionID: "session-1"}, ObserverHandlers{
		OnEvent: func(provider.StreamEvent) error { called = true; return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "禁止") || called {
		t.Fatalf("observer tool event must fail closed: called=%t err=%v", called, err)
	}
	if len(fake.controlRequests) != 0 {
		t.Fatal("observer protocol violation must not control the primary run")
	}
}

func TestAttachFallsBackToPollingAfterRepeatedFailures(t *testing.T) {
	store := NewStore(t.TempDir())
	closed := observerEvents(provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{"name": "close"}})
	fake := &fakeProvider{execution: observerMetadata(), streams: []<-chan provider.StreamEvent{
		observerEvents(), observerEvents(), observerEvents(), closed,
	}}
	controller := NewController(fake, store)
	controller.Sleep = func(context.Context, time.Duration) error { return nil }
	polling := 0
	snapshots := 0
	if err := controller.Attach(context.Background(), ObserverRequest{SessionID: "session-1"}, ObserverHandlers{
		OnSnapshot: func(*provider.SessionMessageList) error { snapshots++; return nil },
		OnStatus: func(status ObserverStatus) error {
			if status.Polling {
				polling++
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if polling != 1 || snapshots != 2 || len(fake.requests) != 4 {
		t.Fatalf("unexpected polling fallback: polling=%d snapshots=%d requests=%d", polling, snapshots, len(fake.requests))
	}
	state, err := store.LoadObserver("session-1")
	if err != nil || state.PollingFallbacks != 1 || state.CleanCloses != 1 {
		t.Fatalf("unexpected observer metrics: %#v, %v", state, err)
	}
}

func TestAttachStableConnectionResetsFailures(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	first := observerEvents(provider.StreamEvent{ID: "event-1", Type: provider.EventMessage, Data: map[string]any{"name": "message"}})
	second := observerEvents(provider.StreamEvent{Type: provider.EventClose, Data: map[string]any{"name": "close"}})
	fake := &fakeProvider{execution: observerMetadata(), streams: []<-chan provider.StreamEvent{first, second}}
	controller := NewController(fake, store)
	controller.Sleep = func(context.Context, time.Duration) error { return nil }
	if err := controller.Attach(context.Background(), ObserverRequest{SessionID: "session-1"}, ObserverHandlers{
		OnEvent: func(provider.StreamEvent) error {
			now = now.Add(ObserverStableConnection + time.Second)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadObserver("session-1")
	if err != nil || state.StableConnections != 1 || state.ConsecutiveFailures != 0 {
		t.Fatalf("stable connection must reset failures: %#v, %v", state, err)
	}
}
