package taiyiflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func TestNewClientUsesUnlimitedTimeoutForStreamRequests(t *testing.T) {
	client := NewClient("http://example.com", "")

	if got := client.httpClient.Timeout; got != time.Duration(internalconfig.DefaultHTTPTimeoutSeconds)*time.Second {
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

func TestSetHTTPTimeoutSeconds(t *testing.T) {
	client := NewClient("http://example.com", "")
	client.SetHTTPTimeoutSeconds(90)
	if client.httpClient.Timeout != 90*time.Second {
		t.Fatalf("expected 90s standard timeout, got %s", client.httpClient.Timeout)
	}
	if client.streamHTTPClient.Timeout != 0 {
		t.Fatalf("expected stream timeout to remain unlimited, got %s", client.streamHTTPClient.Timeout)
	}
	client.SetHTTPTimeoutSeconds(0)
	if client.httpClient.Timeout != time.Duration(internalconfig.DefaultHTTPTimeoutSeconds)*time.Second {
		t.Fatalf("expected invalid timeout to normalize to default, got %s", client.httpClient.Timeout)
	}
}

func TestSetAPIPrefix(t *testing.T) {
	client := NewClient("http://example.com", "")
	client.SetAPIPrefix("taiyi/composer/api/v1/")
	if got := client.apiURL("/version"); got != "http://example.com"+backendAPIPath("/version") {
		t.Fatalf("expected normalized api url, got %q", got)
	}
	client.SetAPIPrefix("")
	if got := client.apiURL("/version"); got != "http://example.com"+backendAPIPath("/version") {
		t.Fatalf("expected empty prefix to fall back to default api prefix, got %q", got)
	}
	client.SetAPIPrefix("/api/v1")
	if got := client.apiURL("/version"); got != "http://example.com"+backendAPIPath("/version") {
		t.Fatalf("expected legacy api prefix to normalize to default api prefix, got %q", got)
	}
}

func TestStreamIgnoresStandardClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/demo-flow/stream") {
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
		if request.URL.Path != backendAPIPath("/run/demo-flow/stream") {
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

func TestStreamIdleTimeoutAllowsLongActiveStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/demo-flow/stream") {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Fatalf("expected SSE response writer to implement http.Flusher")
		}
		for index := 0; index < 4; index++ {
			fmt.Fprint(writer, "event: message\n")
			fmt.Fprintf(writer, "data: {\"chunk\":\"part-%d\"}\n\n", index)
			flusher.Flush()
			time.Sleep(15 * time.Millisecond)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	client.streamIdleTimeout = 30 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stream, _, err := client.Stream(ctx, provider.StreamRequest{FlowID: "demo-flow"})
	if err != nil {
		t.Fatalf("expected stream request to succeed, got %v", err)
	}

	chunks := []string{}
	for event := range stream {
		if event.Type == provider.EventError {
			t.Fatalf("expected active stream to avoid idle timeout, got %#v", event.Data)
		}
		if chunk, ok := event.Data["chunk"].(string); ok {
			chunks = append(chunks, chunk)
		}
	}
	if len(chunks) != 4 {
		t.Fatalf("expected all active stream chunks, got %#v", chunks)
	}
}

func TestIdleTimeoutReadCloserReportsNormalEOF(t *testing.T) {
	reader := newIdleTimeoutReadCloser(io.NopCloser(strings.NewReader("done")), time.Second)
	buffer := make([]byte, 4)
	count, err := reader.Read(buffer)
	if err != nil || count != 4 || string(buffer) != "done" {
		t.Fatalf("expected first read to return content, count=%d err=%v buffer=%q", count, err, string(buffer))
	}
	count, err = reader.Read(buffer)
	if err != io.EOF || count != 0 {
		t.Fatalf("expected normal EOF, count=%d err=%v", count, err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("expected close to succeed, got %v", err)
	}
}

func TestGetRunStatsPollsCompressionRecordsUntilAvailable(t *testing.T) {
	compressionRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/agents/flows/demo-flow/sessions/session-123/messages"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"total":1,"offset":0,"data":[{"id":"message-1","run_id":"run-123","token_usage":{"input_tokens":120,"output_tokens":40,"total_tokens":160}}]}`)
		case backendAPIPath("/agents/flows/demo-flow/sessions/session-123/run/run-123/compressions"):
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
		if request.URL.Path != backendAPIPath("/agents/flows/demo-flow/sessions/session-123/context-window") {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"flow_id":"demo-flow","session_id":"session-123","context_window":{"max_input_tokens":8192,"context_usage_tokens":3072,"direct_message_tokens":2048,"compressed_history_tokens":1024,"current_round_input_tokens":3072,"next_round_available_tokens":5120,"current_round_available_tokens":5120,"history_message_count":14,"prepared_message_count":14,"high_watermark_ratio":0.85,"compression_target_ratio":0.6,"high_watermark_tokens":6963,"compression_target_tokens":4915},"latest_compression":{"trigger_reason":"input_max_tokens_exceeded","compression_strategy":"token_truncate","target_type":"history_messages","before_tokens":9200,"target_tokens":8192,"after_tokens":6000,"remaining_tokens":2192,"compression_ratio":0.65,"reserved_history_tokens":512},"memory_tiers":["session","flow_user_day","dream"],"source_updated_at":"2026-04-23T12:00:00Z","source":"history_rebuild"}`)
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
	if preview.ContextWindow.ContextUsageTokens != 3072 || preview.ContextWindow.DirectMessageTokens != 2048 || preview.ContextWindow.CompressedHistoryTokens != 1024 {
		t.Fatalf("expected context usage split fields to be parsed, got %#v", preview.ContextWindow)
	}
	if preview.ContextWindow.HighWatermarkRatio != 0.85 || preview.ContextWindow.CompressionTargetRatio != 0.6 {
		t.Fatalf("expected watermark ratios to be parsed, got %#v", preview.ContextWindow)
	}
	if preview.ContextWindow.HighWatermarkTokens != 6963 || preview.ContextWindow.CompressionTargetTokens != 4915 {
		t.Fatalf("expected watermark tokens to be parsed, got %#v", preview.ContextWindow)
	}
	if preview.LatestCompression == nil || preview.LatestCompression.AfterTokens != 6000 {
		t.Fatalf("expected latest compression payload, got %#v", preview.LatestCompression)
	}
	if preview.Source != "history_rebuild" {
		t.Fatalf("expected preview source to be preserved, got %#v", preview)
	}
	if len(preview.MemoryTiers) != 3 || preview.MemoryTiers[2] != "dream" {
		t.Fatalf("expected memory tiers to be parsed, got %#v", preview.MemoryTiers)
	}
}

func TestListMemoryConsolidationsParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/agents/memory-consolidations") {
			http.NotFound(writer, request)
			return
		}
		if got := request.URL.Query().Get("flow_id"); got != "flow-uuid-1" {
			t.Fatalf("expected flow_id query, got %q", got)
		}
		if got := request.URL.Query().Get("status"); got != "succeeded" {
			t.Fatalf("expected status query, got %q", got)
		}
		if got := request.URL.Query().Get("limit"); got != "3" {
			t.Fatalf("expected limit query, got %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"total":1,"offset":0,"data":[{"id":"task-1","scope_key":"dreamtask::scope","flow_id":"flow-uuid-1","owner_appid":"app-1","owner_user_id":"user-1","window_start":"2026-05-01T08:00:00Z","window_end":"2026-05-02T09:00:00Z","trigger_reason":"auto_dream","status":"succeeded","attempt_count":1,"last_error":"","last_source_count":8,"last_session_count":5,"last_task_id":"backend-1","result_segment_key":"dream-segment-1","last_run_at":"2026-05-02T09:05:00Z","created_at":"2026-05-02T09:00:00Z","updated_at":"2026-05-02T09:05:00Z"}]}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	page, err := client.ListMemoryConsolidations(context.Background(), provider.MemoryConsolidationQuery{
		FlowID: "flow-uuid-1",
		Status: "succeeded",
		Limit:  3,
	})
	if err != nil {
		t.Fatalf("expected ListMemoryConsolidations to succeed, got %v", err)
	}
	if page == nil || len(page.Items) != 1 {
		t.Fatalf("expected one DreamTask item, got %#v", page)
	}
	if page.Items[0].ResultSegmentKey != "dream-segment-1" || page.Items[0].LastSessionCount != 5 {
		t.Fatalf("expected DreamTask payload to be preserved, got %#v", page.Items[0])
	}
}

func TestTriggerMemoryConsolidationPostsRequestAndParsesResponse(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/agents/memory-consolidations/trigger") {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", request.Method)
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body failed: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		fmt.Fprint(writer, `{"task_id":"task-1","backend_task_id":"backend-1","status":"pending","scheduled":true,"reason":""}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	result, err := client.TriggerMemoryConsolidation(context.Background(), provider.MemoryConsolidationTriggerRequest{
		FlowID:         "flow-uuid-1",
		MinHours:       6,
		MinSessions:    3,
		ExecuteEnabled: false,
	})
	if err != nil {
		t.Fatalf("expected TriggerMemoryConsolidation to succeed, got %v", err)
	}
	if captured["flow_id"] != "flow-uuid-1" {
		t.Fatalf("expected flow_id in request body, got %#v", captured)
	}
	if captured["min_hours"] != float64(6) || captured["min_sessions"] != float64(3) {
		t.Fatalf("expected min thresholds in request body, got %#v", captured)
	}
	if captured["execute_enabled"] != false {
		t.Fatalf("expected execute_enabled=false, got %#v", captured)
	}
	if result == nil || result.TaskID != "task-1" || !result.Scheduled {
		t.Fatalf("expected trigger response to be preserved, got %#v", result)
	}
}

func TestUpdateSessionTitlePostsSessionName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", request.Method)
		}
		if request.URL.Path != backendAPIPath("/agents/flows/demo-flow/sessions/session-123") {
			http.NotFound(writer, request)
			return
		}
		if got := request.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Fatalf("expected json content type, got %q", got)
		}
		var payload map[string]string
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body failed: %v", err)
		}
		if payload["session_name"] != "用户询问录音问题" {
			t.Fatalf("expected session_name payload, got %#v", payload)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"id":"session-123","session_name":"用户询问录音问题"}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	title, err := client.UpdateSessionTitle(context.Background(), "demo-flow", "session-123", "用户询问录音问题")
	if err != nil {
		t.Fatalf("expected UpdateSessionTitle to succeed, got %v", err)
	}
	if title != "用户询问录音问题" {
		t.Fatalf("expected updated title to round-trip, got %q", title)
	}
}

func TestGetMetadataParsesMaxInputTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/demo-flow/metadata") {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"id":"demo-flow","name":"Demo Flow","description":"测试流程","welcome_message":"hi","max_input_tokens":200000,"enabled":true,"is_chat":true,"features":["internet_search","support_knowledge","support_mcp_tools","support_human_ai_dialogue","custom_feature"]}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	metadata, err := client.GetMetadata(context.Background(), "demo-flow")
	if err != nil {
		t.Fatalf("expected GetMetadata to succeed, got %v", err)
	}
	if metadata == nil {
		t.Fatal("expected metadata response")
	}
	if metadata.MaxInputTokens != 200000 {
		t.Fatalf("expected max_input_tokens=200000, got %#v", metadata)
	}
	if !metadata.Capabilities.InternetSearch || !metadata.Capabilities.KnowledgeSearch || !metadata.Capabilities.SupportKnowledge || !metadata.Capabilities.SupportMCPTools || !metadata.Capabilities.SupportHumanAIDialogue {
		t.Fatalf("expected metadata capabilities to normalize known features, got %#v", metadata.Capabilities)
	}
	if len(metadata.Capabilities.UnknownFeatures) != 1 || metadata.Capabilities.UnknownFeatures[0] != "custom_feature" {
		t.Fatalf("expected unknown feature to be preserved, got %#v", metadata.Capabilities)
	}
}

func TestGetServiceConfigParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/config") {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"feature_flags":{"text_to_speech":true,"speech_to_text":true,"human_ai_dialogue":true},"max_file_size_upload":100,"doc_exts":[".pdf","PDF","docx",""],"milvus_enabled":true,"default_flow_id":" flow-1 ","image_flow_id":" image-flow ","doc_unstructured_flow_id":" doc-unstructured-flow "}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	config, err := client.GetServiceConfig(context.Background())
	if err != nil {
		t.Fatalf("expected GetServiceConfig to succeed, got %v", err)
	}
	if config == nil {
		t.Fatal("expected service config response")
	}
	if !config.FeatureFlags.TextToSpeech || !config.FeatureFlags.SpeechToText || !config.FeatureFlags.HumanAIDialogue {
		t.Fatalf("expected feature flags to map, got %#v", config.FeatureFlags)
	}
	if config.MaxFileSizeUpload != 100 || !config.MilvusEnabled || config.DefaultFlowID != "flow-1" {
		t.Fatalf("unexpected service config values: %#v", config)
	}
	if len(config.DocExts) != 2 || config.DocExts[0] != "pdf" || config.DocExts[1] != "docx" {
		t.Fatalf("expected normalized doc extensions, got %#v", config.DocExts)
	}
	if config.ImageFlowID != "image-flow" {
		t.Fatalf("expected image flow id to be preserved, got %#v", config)
	}
	if config.DocUnstructuredFlowID != "doc-unstructured-flow" {
		t.Fatalf("expected doc unstructured flow id to be preserved, got %#v", config)
	}
}

func TestListKnowledgeBasesParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/agents/knowledge/bases") {
			http.NotFound(writer, request)
			return
		}
		if request.URL.Query().Get("filter") != "demo" {
			t.Fatalf("expected filter query to be forwarded, got %q", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"total":1,"offset":0,"data":[{"id":"kb-1","code":"kb_demo","display_name":"演示知识库","type":"personal","description":"测试知识库"}]}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	list, err := client.ListKnowledgeBases(context.Background(), "demo", 0, 20)
	if err != nil {
		t.Fatalf("expected ListKnowledgeBases to succeed, got %v", err)
	}
	if list == nil || len(list.Items) != 1 {
		t.Fatalf("expected one knowledge base, got %#v", list)
	}
	if list.Items[0].Code != "kb_demo" || list.Items[0].Name != "演示知识库" {
		t.Fatalf("unexpected knowledge base payload: %#v", list.Items[0])
	}
}

func TestCreateSessionIDParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/session_id") {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"session_id":"session-xyz"}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	sessionID, err := client.CreateSessionID(context.Background())
	if err != nil {
		t.Fatalf("expected CreateSessionID to succeed, got %v", err)
	}
	if sessionID != "session-xyz" {
		t.Fatalf("expected session-xyz, got %q", sessionID)
	}
}

func TestUploadFilePostsMultipartFormAndParsesResponse(t *testing.T) {
	tempDir := t.TempDir()
	localPath := filepath.Join(tempDir, "demo.txt")
	if err := os.WriteFile(localPath, []byte("hello upload"), 0o644); err != nil {
		t.Fatalf("write temp file failed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/agents/upload/demo-flow") {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", request.Method)
		}
		if got := request.URL.Query().Get("session_id"); got != "session-123" {
			t.Fatalf("expected session_id query to be forwarded, got %q", got)
		}
		if err := request.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatalf("parse multipart form failed: %v", err)
		}
		if got := request.MultipartForm.Value["session_id"]; len(got) != 1 || got[0] != "session-123" {
			t.Fatalf("expected multipart session_id to be forwarded, got %#v", got)
		}
		if request.FormValue("file_path") != "folder/remote.txt" {
			t.Fatalf("expected file_path to be forwarded, got %q", request.FormValue("file_path"))
		}
		file, header, err := request.FormFile("file")
		if err != nil {
			t.Fatalf("read uploaded file failed: %v", err)
		}
		defer file.Close()
		content, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("read uploaded file content failed: %v", err)
		}
		if header.Filename != "remote.txt" {
			t.Fatalf("expected uploaded filename remote.txt, got %q", header.Filename)
		}
		if string(content) != "hello upload" {
			t.Fatalf("unexpected uploaded file content: %q", string(content))
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"flowId":"demo-flow","file_path":"demo-flow/session-123/folder/remote.txt"}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	result, err := client.UploadFile(context.Background(), "demo-flow", "session-123", localPath, "folder/remote.txt")
	if err != nil {
		t.Fatalf("expected UploadFile to succeed, got %v", err)
	}
	if result == nil {
		t.Fatal("expected upload result")
	}
	if result.FlowID != "demo-flow" || result.FilePath != "demo-flow/session-123/folder/remote.txt" {
		t.Fatalf("unexpected upload result: %#v", result)
	}
}

func TestUploadFileRejectsEmptySessionID(t *testing.T) {
	tempDir := t.TempDir()
	localPath := filepath.Join(tempDir, "demo.txt")
	if err := os.WriteFile(localPath, []byte("hello upload"), 0o644); err != nil {
		t.Fatalf("write temp file failed: %v", err)
	}

	client := NewClient("https://example.com", "")
	_, err := client.UploadFile(context.Background(), "demo-flow", "", localPath, "")
	if err == nil || !strings.Contains(err.Error(), "当前会话 ID 不能为空") {
		t.Fatalf("expected empty session_id to be rejected, got %v", err)
	}
}

func TestRunDocumentUnstructuredPostsSimplifiedRequestAndParsesResponse(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/doc-flow") {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", request.Method)
		}
		if got := request.URL.Query().Get("session_id"); got != "session-doc-1" {
			t.Fatalf("expected session_id query to be forwarded, got %q", got)
		}
		if contentType := request.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
			t.Fatalf("expected application/json content type, got %q", contentType)
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body failed: %v", err)
		}
		writer.Header().Set(SessionHeader, "session-doc-2")
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprint(writer, `{"items":[{"text":"结构化结果"}]}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	result, err := client.RunDocumentUnstructured(context.Background(), provider.DocumentUnstructuredRequest{
		FlowID:     "doc-flow",
		Input:      "请解析文件",
		InputType:  "any",
		InputFiles: []string{"flow/session/demo.pdf"},
		OutputType: "any",
		SessionID:  "session-doc-1",
	})
	if err != nil {
		t.Fatalf("expected RunDocumentUnstructured to succeed, got %v", err)
	}
	if result == nil {
		t.Fatal("expected document unstructured result")
	}
	if result.SessionID != "session-doc-2" {
		t.Fatalf("expected response session id, got %#v", result)
	}
	if result.MediaType != "application/json" {
		t.Fatalf("expected normalized media type, got %#v", result)
	}
	if result.Output != `{"items":[{"text":"结构化结果"}]}` {
		t.Fatalf("expected raw output body to be preserved, got %#v", result)
	}
	if captured["input_value"] != "请解析文件" {
		t.Fatalf("expected input_value to be forwarded, got %#v", captured)
	}
	if captured["session_id"] != "session-doc-1" {
		t.Fatalf("expected session_id to be forwarded, got %#v", captured)
	}
	files, ok := captured["files"].([]any)
	if !ok || len(files) != 1 || files[0] != "flow/session/demo.pdf" {
		t.Fatalf("expected files payload, got %#v", captured)
	}
	if _, exists := captured["input_files"]; exists {
		t.Fatalf("expected simplified request to use files alias, got %#v", captured)
	}
}

func TestRunDocumentUnstructuredReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/doc-flow") {
			http.NotFound(writer, request)
			return
		}
		http.Error(writer, `{"detail":"bad request"}`, http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	_, err := client.RunDocumentUnstructured(context.Background(), provider.DocumentUnstructuredRequest{FlowID: "doc-flow"})
	if err == nil || !strings.Contains(err.Error(), "调用文档结构化服务失败") {
		t.Fatalf("expected http error to surface, got %v", err)
	}
}

func TestRunImageUnderstandingPostsSimplifiedRequestAndPreservesTextResponse(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/image-flow") {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", request.Method)
		}
		if got := request.URL.Query().Get("session_id"); got != "session-image-1" {
			t.Fatalf("expected session_id query to be forwarded, got %q", got)
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body failed: %v", err)
		}
		writer.Header().Set(SessionHeader, "session-image-2")
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(writer, "Hello")
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	result, err := client.RunImageUnderstanding(context.Background(), provider.ImageUnderstandingRequest{
		FlowID:                   "image-flow",
		Input:                    "请提取图像内容",
		InputType:                "any",
		Tweaks: map[string]any{
			"Prompt-G6Qwq":   map[string]any{"template": "系统提示词模板"},
			"Prompt-e2cjZ":   map[string]any{"template": "问题：{question}"},
			"TextOutput-sMWXv": map[string]any{"output_format": "text"},
		},
		InputFiles:               []string{"flow/session/screenshot.png"},
		OutputType:               "any",
		SessionID:                "session-image-1",
		ImageCompressionStrategy: provider.ImageCompressionStrategyPassthrough,
	})
	if err != nil {
		t.Fatalf("expected RunImageUnderstanding to succeed, got %v", err)
	}
	if result == nil {
		t.Fatalf("expected image understanding result, got %#v", result)
	}
	if result.SessionID != "session-image-2" || result.MediaType != "text/plain" {
		t.Fatalf("unexpected image understanding metadata: %#v", result)
	}
	if result.Text != "Hello" {
		t.Fatalf("expected plain text image understanding result, got %#v", result)
	}
	if result.Output != "Hello" {
		t.Fatalf("expected raw text output to be preserved, got %#v", result)
	}
	if captured["input_value"] != "请提取图像内容" {
		t.Fatalf("expected input_value to be forwarded, got %#v", captured)
	}
	tweaks, ok := captured["tweaks"].(map[string]any)
	if !ok {
		t.Fatalf("expected image compression strategy to be nested under tweaks, got %#v", captured)
	}
	if tweaks["image_compression_strategy"] != provider.ImageCompressionStrategyPassthrough {
		t.Fatalf("expected image_compression_strategy to be forwarded under tweaks, got %#v", captured)
	}
	if systemPrompt, ok := tweaks["Prompt-G6Qwq"].(map[string]any); !ok || systemPrompt["template"] != "系统提示词模板" {
		t.Fatalf("expected screenshot system prompt tweak to be preserved, got %#v", tweaks)
	}
	if userPrompt, ok := tweaks["Prompt-e2cjZ"].(map[string]any); !ok || userPrompt["template"] != "问题：{question}" {
		t.Fatalf("expected screenshot user prompt tweak to be preserved, got %#v", tweaks)
	}
	if textOutput, ok := tweaks["TextOutput-sMWXv"].(map[string]any); !ok || textOutput["output_format"] != "text" {
		t.Fatalf("expected screenshot output format tweak to be preserved, got %#v", tweaks)
	}
	if _, exists := captured["image_compression_strategy"]; exists {
		t.Fatalf("expected no top-level image_compression_strategy field, got %#v", captured)
	}
	files, ok := captured["files"].([]any)
	if !ok || len(files) != 1 || files[0] != "flow/session/screenshot.png" {
		t.Fatalf("expected files payload, got %#v", captured)
	}
}

func TestRunImageUnderstandingAcceptsTextOutputFromTweaksFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/image-flow") {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set(SessionHeader, "session-image-text")
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(writer, "页面中央是登录表单，顶部显示错误提示：验证码已过期。")
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	result, err := client.RunImageUnderstanding(context.Background(), provider.ImageUnderstandingRequest{
		FlowID:    "image-flow",
		Input:     "请说明当前页面状态",
		InputType: "any",
		Tweaks: map[string]any{
			"TextOutput-sMWXv": map[string]any{"output_format": "text"},
		},
		SessionID: "session-image-source",
	})
	if err != nil {
		t.Fatalf("expected text output image understanding to succeed, got %v", err)
	}
	if result == nil {
		t.Fatalf("expected text output result, got %#v", result)
	}
	if result.SessionID != "session-image-text" || result.MediaType != "text/plain" {
		t.Fatalf("unexpected text output metadata: %#v", result)
	}
	if result.Text != "页面中央是登录表单，顶部显示错误提示：验证码已过期。" {
		t.Fatalf("expected plain text output to be exposed via Text, got %#v", result)
	}
	if result.Output != "页面中央是登录表单，顶部显示错误提示：验证码已过期。" {
		t.Fatalf("expected raw output to be preserved, got %#v", result)
	}
}

func TestListSessionMessagesParsesReasoningAndSearchResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/agents/flows/demo-flow/sessions/session-123/messages") {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"total":1,"offset":0,"data":[{"id":"msg-1","session_id":"session-123","run_id":"run-123","sender":"Machine","sender_name":"助手","text":"最终回答[^1.1^][^2.1^]","reason_text":"内部思考","search_keyword":"golang upload","reasoning_enabled":true,"search_results":[{"title":"网络结果","link":"https://example.com/a","text":"网页摘要","source":"internet","provider":"bocha"},{"title":"共享文档","file_path":"kb/shared/doc.md","text":"知识摘要","source":"shared","provider":"milvus"}],"session_name":"演示会话","timestamp":"2026-05-14T12:00:00Z","error":false}]}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	list, err := client.ListSessionMessages(context.Background(), "demo-flow", "session-123", 0, 20)
	if err != nil {
		t.Fatalf("expected ListSessionMessages to succeed, got %v", err)
	}
	if list == nil || len(list.Items) != 1 {
		t.Fatalf("expected one session message, got %#v", list)
	}
	message := list.Items[0]
	if message.ReasonText != "内部思考" || message.SearchKeyword != "golang upload" || !message.ReasoningEnabled {
		t.Fatalf("expected reasoning fields to be parsed, got %#v", message)
	}
	if len(message.SearchResults) != 2 {
		t.Fatalf("expected two search results, got %#v", message.SearchResults)
	}
	if message.SearchResults[0].Source != "internet" || message.SearchResults[0].Link != "https://example.com/a" {
		t.Fatalf("expected internet search result to be preserved, got %#v", message.SearchResults[0])
	}
	if message.SearchResults[1].Source != "shared" || message.SearchResults[1].FilePath != "kb/shared/doc.md" {
		t.Fatalf("expected knowledge search result to be preserved, got %#v", message.SearchResults[1])
	}
}

func TestGetCurrentUserInfoParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/users/whoami/userinfo") {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"name":"张三","user_id":"u-123","user_name":"zhangsan","user_scope":"private"}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	currentUser, err := client.GetCurrentUserInfo(context.Background())
	if err != nil {
		t.Fatalf("expected GetCurrentUserInfo to succeed, got %v", err)
	}
	if currentUser == nil {
		t.Fatal("expected current user response")
	}
	if currentUser.Name != "张三" || currentUser.UserName != "zhangsan" || currentUser.UserID != "u-123" {
		t.Fatalf("unexpected current user payload: %#v", currentUser)
	}
}

func TestResolveSkillPreviewPostsRequestAndParsesResponse(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/agents/flows/demo-flow/sessions/session-123/skills/resolve") {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", request.Method)
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body failed: %v", err)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"skills":[{"name":"Context Review","content":"review context","source":"client","description":"检查上下文","version":"1.0.0"}],"missing_skill_names":["debug-helper"],"allowed_tools":["shell_exec","read_file"]}`)
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	preview, err := client.ResolveSkillPreview(context.Background(), provider.SkillResolveRequest{
		FlowID:                 "demo-flow",
		SessionID:              "session-123",
		RequestType:            provider.RequestTypeAgent,
		CommandExecutionTarget: "client",
		RequestedSkillNames:    []string{"Context Review", "debug-helper"},
		AvailableTools:         []string{"shell_exec", "read_file"},
		ClientSkillPackages: []map[string]any{{
			"name": "workspace",
			"skills": []map[string]any{{
				"name":    "Context Review",
				"content": "review context",
			}},
		}},
	})
	if err != nil {
		t.Fatalf("expected resolve skill preview to succeed, got %v", err)
	}
	if _, ok := captured["flow_id"]; ok {
		t.Fatalf("expected request body to omit flow_id, got %#v", captured)
	}
	if _, ok := captured["session_id"]; ok {
		t.Fatalf("expected request body to omit session_id, got %#v", captured)
	}
	if _, ok := captured["owner_user_id"]; ok {
		t.Fatalf("expected request body to omit owner_user_id, got %#v", captured)
	}
	if captured["request_type"] != provider.RequestTypeAgent {
		t.Fatalf("expected normalized request type %q, got %#v", provider.RequestTypeAgent, captured["request_type"])
	}
	if captured["command_execution_target"] != "client" {
		t.Fatalf("expected command target client, got %#v", captured["command_execution_target"])
	}
	requestedSkillNames, ok := captured["requested_skill_names"].([]any)
	if !ok || len(requestedSkillNames) != 2 || requestedSkillNames[1] != "debug-helper" {
		t.Fatalf("expected requested skill names to be preserved, got %#v", captured["requested_skill_names"])
	}
	availableTools, ok := captured["available_tools"].([]any)
	if !ok || len(availableTools) != 2 || availableTools[0] != "shell_exec" {
		t.Fatalf("expected available tools to be preserved, got %#v", captured["available_tools"])
	}
	if preview == nil {
		t.Fatal("expected resolve preview response")
	}
	if len(preview.Skills) != 1 || preview.Skills[0].Name != "Context Review" {
		t.Fatalf("expected resolved skills in response, got %#v", preview.Skills)
	}
	if len(preview.MissingSkillNames) != 1 || preview.MissingSkillNames[0] != "debug-helper" {
		t.Fatalf("expected missing skill names in response, got %#v", preview.MissingSkillNames)
	}
	if len(preview.AllowedTools) != 2 || preview.AllowedTools[1] != "read_file" {
		t.Fatalf("expected allowed tools in response, got %#v", preview.AllowedTools)
	}
}

func TestStreamParsesCompressionEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/demo-flow/stream") {
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

func TestStreamIncludesAgentAutoLoopRequestFields(t *testing.T) {
	var captured streamRequestBody
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/demo-flow/stream") {
			http.NotFound(writer, request)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body failed: %v", err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set(SessionHeader, "session-auto-loop")
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	enabled := false
	stream, session, err := client.Stream(context.Background(), provider.StreamRequest{
		FlowID:                 "demo-flow",
		RequestType:            provider.RequestTypeAgent,
		AgentAutoLoopEnabled:   &enabled,
		AgentAutoLoopMaxRounds: 5,
	})
	if err != nil {
		t.Fatalf("expected stream request to succeed, got %v", err)
	}
	if session == nil || session.SessionID != "session-auto-loop" {
		t.Fatalf("expected session header to be preserved, got %#v", session)
	}
	for range stream {
	}
	if captured.AgentAutoLoopEnabled == nil {
		t.Fatal("expected agent_auto_loop_enabled to be sent")
	}
	if *captured.AgentAutoLoopEnabled {
		t.Fatal("expected agent_auto_loop_enabled to preserve false override")
	}
	if captured.AgentAutoLoopMaxRounds != 5 {
		t.Fatalf("expected agent_auto_loop_max_rounds 5, got %d", captured.AgentAutoLoopMaxRounds)
	}
}

func TestStreamIncludesCrossSessionMemoryRequestField(t *testing.T) {
	var captured streamRequestBody
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/demo-flow/stream") {
			http.NotFound(writer, request)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body failed: %v", err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	enabled := false
	stream, _, err := client.Stream(context.Background(), provider.StreamRequest{
		FlowID:                    "demo-flow",
		CrossSessionMemoryEnabled: &enabled,
	})
	if err != nil {
		t.Fatalf("expected stream request to succeed, got %v", err)
	}
	for range stream {
	}
	if captured.CrossSessionMemoryEnabled == nil {
		t.Fatal("expected cross_session_memory_enabled to be sent")
	}
	if *captured.CrossSessionMemoryEnabled {
		t.Fatal("expected cross_session_memory_enabled to preserve false override")
	}
}

func TestStreamIncludesSessionIDInQueryAndBody(t *testing.T) {
	var captured streamRequestBody
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/demo-flow/stream") {
			http.NotFound(writer, request)
			return
		}
		if got := request.URL.Query().Get("session_id"); got != "session-123" {
			t.Fatalf("expected session_id query to be forwarded, got %q", got)
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Fatalf("decode request body failed: %v", err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	stream, _, err := client.Stream(context.Background(), provider.StreamRequest{
		FlowID:    "demo-flow",
		SessionID: "session-123",
	})
	if err != nil {
		t.Fatalf("expected stream request to succeed, got %v", err)
	}
	for range stream {
	}
	if captured.SessionID != "session-123" {
		t.Fatalf("expected session_id in request body, got %#v", captured)
	}
}

func TestStreamRequestUsesInputValueWithoutPromptText(t *testing.T) {
	var (
		captured       streamRequestBody
		capturedRawMap map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/demo-flow/stream") {
			http.NotFound(writer, request)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&capturedRawMap); err != nil {
			t.Fatalf("decode raw request body failed: %v", err)
		}
		payload, err := json.Marshal(capturedRawMap)
		if err != nil {
			t.Fatalf("marshal captured map failed: %v", err)
		}
		if err := json.Unmarshal(payload, &captured); err != nil {
			t.Fatalf("decode typed request body failed: %v", err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.Header().Set(SessionHeader, "session-input-only")
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	stream, session, err := client.Stream(context.Background(), provider.StreamRequest{
		FlowID: "demo-flow",
		Input:  "仅发送用户输入文本",
	})
	if err != nil {
		t.Fatalf("expected stream request to succeed, got %v", err)
	}
	if session == nil || session.SessionID != "session-input-only" {
		t.Fatalf("expected session header to be preserved, got %#v", session)
	}
	for range stream {
	}

	if captured.InputValue != "仅发送用户输入文本" {
		t.Fatalf("expected input_value to keep user text, got %q", captured.InputValue)
	}
	if _, exists := capturedRawMap["prompt_text"]; exists {
		t.Fatalf("expected request body not to include prompt_text, got %#v", capturedRawMap)
	}
}

func TestStreamRequestIncludesTweaks(t *testing.T) {
	var capturedRawMap map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != backendAPIPath("/run/demo-flow/stream") {
			http.NotFound(writer, request)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&capturedRawMap); err != nil {
			t.Fatalf("decode raw request body failed: %v", err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
	}))
	defer server.Close()

	client := NewClient(server.URL, "")
	stream, _, err := client.Stream(context.Background(), provider.StreamRequest{
		FlowID: "demo-flow",
		Input:  "原始用户输入",
		Tweaks: map[string]any{
			"taiyiflow_environment": "<taiyiflow_environment>\n工作目录: /tmp/demo\n</taiyiflow_environment>",
		},
	})
	if err != nil {
		t.Fatalf("expected stream request to succeed, got %v", err)
	}
	for range stream {
	}

	tweaksValue, ok := capturedRawMap["tweaks"].(map[string]any)
	if !ok {
		t.Fatalf("expected tweaks object in request body, got %#v", capturedRawMap["tweaks"])
	}
	if tweaksValue["taiyiflow_environment"] != "<taiyiflow_environment>\n工作目录: /tmp/demo\n</taiyiflow_environment>" {
		t.Fatalf("unexpected taiyiflow_environment tweak: %#v", tweaksValue)
	}
	if capturedRawMap["input_value"] != "原始用户输入" {
		t.Fatalf("expected input_value to remain original text, got %#v", capturedRawMap["input_value"])
	}
}
