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

func TestGetRunStatsPollsCompressionRecordsUntilAvailable(t *testing.T) {
	compressionRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/agents/flows/demo-flow/sessions/session-123/messages":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"total":1,"offset":0,"data":[{"id":"message-1","run_id":"run-123","token_usage":{"input_tokens":120,"output_tokens":40,"total_tokens":160}}]}`)
		case "/api/v1/agents/dialogue/run-123/compressions":
			compressionRequests++
			writer.Header().Set("Content-Type", "application/json")
			if compressionRequests == 1 {
				fmt.Fprint(writer, `[]`)
				return
			}
			fmt.Fprint(writer, `[{"trigger_reason":"input_max_tokens_exceeded","compression_strategy":"token_truncate","target_type":"history_messages","before_tokens":9200,"target_tokens":8192,"after_tokens":6000,"remaining_tokens":2192,"compression_ratio":0.65,"reserved_history_tokens":512}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	client.compressionPollDelay = time.Millisecond
	client.compressionPollAttempts = 3

	stats, err := client.GetRunStats(context.Background(), "demo-flow", "session-123", "run-123")
	if err != nil {
		t.Fatalf("expected GetRunStats to succeed, got %v", err)
	}
	if stats == nil {
		t.Fatal("expected stats response")
	}
	if compressionRequests < 2 {
		t.Fatalf("expected compression endpoint to be retried, got %d requests", compressionRequests)
	}
	if len(stats.Compressions) != 1 {
		t.Fatalf("expected one compression record after polling, got %#v", stats.Compressions)
	}
	if stats.Compressions[0].AfterTokens != 6000 {
		t.Fatalf("expected compression payload to be preserved, got %#v", stats.Compressions[0])
	}
}

func TestGetSessionContextWindowParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/agents/flows/demo-flow/sessions/session-123/context-window" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"flow_id":"demo-flow","session_id":"session-123","context_window":{"max_input_tokens":8192,"short_term_context_tokens":3072,"current_round_input_tokens":3072,"next_round_available_tokens":5120,"current_round_available_tokens":5120,"history_message_count":14,"prepared_message_count":14},"latest_compression":{"trigger_reason":"input_max_tokens_exceeded","compression_strategy":"token_truncate","target_type":"history_messages","before_tokens":9200,"target_tokens":8192,"after_tokens":6000,"remaining_tokens":2192,"compression_ratio":0.65,"reserved_history_tokens":512},"source_updated_at":"2026-04-23T12:00:00Z","source":"history_rebuild"}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	preview, err := client.GetSessionContextWindow(context.Background(), "demo-flow", "session-123")
	if err != nil {
		t.Fatalf("expected GetSessionContextWindow to succeed, got %v", err)
	}
	if preview == nil || preview.ContextWindow == nil {
		t.Fatalf("expected session context preview, got %#v", preview)
	}
	if preview.ContextWindow.CurrentRoundInputTokens != 3072 {
		t.Fatalf("expected session context payload to be preserved, got %#v", preview.ContextWindow)
	}
	if preview.LatestCompression == nil || preview.LatestCompression.AfterTokens != 6000 {
		t.Fatalf("expected latest compression payload, got %#v", preview.LatestCompression)
	}
	if preview.Source != "history_rebuild" {
		t.Fatalf("expected preview source to be preserved, got %#v", preview)
	}
}

func TestStreamParsesCompressionEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/run/demo-flow/stream" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Fatalf("expected SSE response writer to implement http.Flusher")
		}

		fmt.Fprint(writer, "event: compression\n")
		fmt.Fprint(writer, "data: {\"count\":1,\"target_type\":\"history_messages\",\"before_tokens\":9200,\"after_tokens\":6000,\"remaining_tokens\":2192,\"compression_ratio\":0.65}\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream, _, err := client.Stream(ctx, provider.StreamRequest{FlowID: "demo-flow"})
	if err != nil {
		t.Fatalf("expected stream request to succeed, got %v", err)
	}

	event, ok := <-stream
	if !ok {
		t.Fatal("expected one SSE event before stream closes")
	}
	if event.Type != provider.EventCompression {
		t.Fatalf("expected compression event, got %s", event.Type)
	}
	if got := event.Data["after_tokens"]; got != float64(6000) {
		t.Fatalf("expected compression payload to be preserved, got %#v", event.Data)
	}
	if got := event.Data["target_type"]; got != "history_messages" {
		t.Fatalf("expected target type in compression payload, got %#v", event.Data)
	}
	for range stream {
	}
}
