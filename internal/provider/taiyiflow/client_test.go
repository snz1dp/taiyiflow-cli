package taiyiflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
	if got := client.apiURL("/version"); got != "http://example.com/taiyi/composer/api/v1/version" {
		t.Fatalf("expected normalized api url, got %q", got)
	}
	client.SetAPIPrefix("")
	if got := client.apiURL("/version"); got != "http://example.com/taiyi/composer/api/v1/version" {
		t.Fatalf("expected empty prefix to fall back to default api prefix, got %q", got)
	}
	client.SetAPIPrefix("/api/v1")
	if got := client.apiURL("/version"); got != "http://example.com/taiyi/composer/api/v1/version" {
		t.Fatalf("expected legacy api prefix to normalize to default api prefix, got %q", got)
	}
}

func TestStreamIgnoresStandardClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/taiyi/composer/api/v1/run/demo-flow/stream" {
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
		if request.URL.Path != "/taiyi/composer/api/v1/run/demo-flow/stream" {
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
		if request.URL.Path != "/taiyi/composer/api/v1/run/demo-flow/stream" {
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
		case "/taiyi/composer/api/v1/agents/flows/demo-flow/sessions/session-123/messages":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"total":1,"offset":0,"data":[{"id":"message-1","run_id":"run-123","token_usage":{"input_tokens":120,"output_tokens":40,"total_tokens":160}}]}`)
		case "/taiyi/composer/api/v1/agents/dialogue/run-123/compressions":
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
		if request.URL.Path != "/taiyi/composer/api/v1/agents/flows/demo-flow/sessions/session-123/context-window" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"flow_id":"demo-flow","session_id":"session-123","context_window":{"max_input_tokens":8192,"short_term_context_tokens":3072,"current_round_input_tokens":3072,"next_round_available_tokens":5120,"current_round_available_tokens":5120,"history_message_count":14,"prepared_message_count":14,"high_watermark_ratio":0.85,"compression_target_ratio":0.6,"high_watermark_tokens":6963,"compression_target_tokens":4915},"latest_compression":{"trigger_reason":"input_max_tokens_exceeded","compression_strategy":"token_truncate","target_type":"history_messages","before_tokens":9200,"target_tokens":8192,"after_tokens":6000,"remaining_tokens":2192,"compression_ratio":0.65,"reserved_history_tokens":512},"source_updated_at":"2026-04-23T12:00:00Z","source":"history_rebuild"}`)
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
}

func TestGetMetadataParsesMaxInputTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/taiyi/composer/api/v1/run/demo-flow/metadata" {
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
		if request.URL.Path != "/taiyi/composer/api/v1/config" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"feature_flags":{"text_to_speech":true,"speech_to_text":true,"human_ai_dialogue":true},"max_file_size_upload":100,"doc_exts":[".pdf","PDF","docx",""],"milvus_enabled":true,"default_flow_id":" flow-1 "}`)
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
}

func TestListKnowledgeBasesParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/taiyi/composer/api/v1/agents/knowledge/bases" {
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

func TestGetCurrentUserInfoParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/taiyi/composer/api/v1/users/whoami/userinfo" {
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
	var captured skillResolveRequestBody
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/taiyi/composer/api/v1/agents/skills/resolve" {
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
	if captured.FlowID != "demo-flow" || captured.SessionID != "session-123" {
		t.Fatalf("expected flow/session ids to be preserved, got %#v", captured)
	}
	if captured.RequestType != provider.RequestTypeAgent {
		t.Fatalf("expected normalized request type %q, got %q", provider.RequestTypeAgent, captured.RequestType)
	}
	if captured.CommandExecutionTarget != "client" {
		t.Fatalf("expected command target client, got %q", captured.CommandExecutionTarget)
	}
	if len(captured.RequestedSkillNames) != 2 || captured.RequestedSkillNames[1] != "debug-helper" {
		t.Fatalf("expected requested skill names to be preserved, got %#v", captured.RequestedSkillNames)
	}
	if len(captured.AvailableTools) != 2 || captured.AvailableTools[0] != "shell_exec" {
		t.Fatalf("expected available tools to be preserved, got %#v", captured.AvailableTools)
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
		if request.URL.Path != "/taiyi/composer/api/v1/run/demo-flow/stream" {
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
		if request.URL.Path != "/taiyi/composer/api/v1/run/demo-flow/stream" {
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

func TestStreamRequestUsesInputValueWithoutPromptText(t *testing.T) {
	var (
		captured       streamRequestBody
		capturedRawMap map[string]any
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/taiyi/composer/api/v1/run/demo-flow/stream" {
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
