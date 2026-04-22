package taiyiflow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func TestNewClientUsesUnlimitedTimeoutForStreamRequests(t *testing.T) {
	client := NewClient("http://example.com", "")

	if got := client.httpClient.Timeout; got != 60*time.Second {
		t.Fatalf("expected standard client timeout to stay 60s, got %s", got)
	}
	if got := client.streamHTTPClient.Timeout; got != 0 {
		t.Fatalf("expected stream client timeout to be unlimited, got %s", got)
	}
	if got := client.streamIdleTimeout; got != defaultStreamIdleTimeout {
		t.Fatalf("expected default stream idle timeout %s, got %s", defaultStreamIdleTimeout, got)
	}
}

func TestSetStreamIdleTimeoutSeconds(t *testing.T) {
	client := NewClient("http://example.com", "")
	client.SetStreamIdleTimeoutSeconds(0)
	if client.streamIdleTimeout != 0 {
		t.Fatalf("expected idle timeout disabled, got %s", client.streamIdleTimeout)
	}
	client.SetStreamIdleTimeoutSeconds(45)
	if client.streamIdleTimeout != 45*time.Second {
		t.Fatalf("expected 45s idle timeout, got %s", client.streamIdleTimeout)
	}
}

func TestStreamIgnoresStandardClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/run/demo-flow/stream" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set(SessionHeader, "session-123")
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Fatalf("expected SSE response writer to implement http.Flusher")
		}

		time.Sleep(40 * time.Millisecond)
		fmt.Fprint(writer, "event: message\n")
		fmt.Fprint(writer, "data: {\"chunk\":\"done\"}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	client.httpClient.Timeout = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream, session, err := client.Stream(ctx, provider.StreamRequest{FlowID: "demo-flow"})
	if err != nil {
		t.Fatalf("expected stream request to succeed, got %v", err)
	}
	if session == nil || session.SessionID != "session-123" {
		t.Fatalf("expected session header to be propagated, got %#v", session)
	}

	event, ok := <-stream
	if !ok {
		t.Fatal("expected one SSE event before stream closes")
	}
	if event.Type != provider.EventMessage {
		t.Fatalf("expected message event, got %s", event.Type)
	}
	if got := event.Data["chunk"]; got != "done" {
		t.Fatalf("expected chunk payload to be preserved, got %#v", got)
	}

	for followup := range stream {
		if followup.Type == provider.EventError {
			t.Fatalf("expected no SSE timeout error, got %#v", followup.Data)
		}
	}
}

func TestStreamEmitsBackendIdleTimeoutWhenNoSSEActivity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/run/demo-flow/stream" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set(SessionHeader, "session-456")
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Fatalf("expected SSE response writer to implement http.Flusher")
		}
		flusher.Flush()
		time.Sleep(80 * time.Millisecond)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	client.streamIdleTimeout = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream, _, err := client.Stream(ctx, provider.StreamRequest{FlowID: "demo-flow"})
	if err != nil {
		t.Fatalf("expected stream request to succeed, got %v", err)
	}

	for event := range stream {
		if event.Type != provider.EventError {
			continue
		}
		message, _ := event.Data["error"].(string)
		if !strings.Contains(message, "backend idle timeout") {
			t.Fatalf("expected backend idle timeout marker, got %q", message)
		}
		return
	}

	t.Fatal("expected backend idle timeout event")
}
