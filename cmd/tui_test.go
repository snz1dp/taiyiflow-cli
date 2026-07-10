package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	internaltui "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/workspaceinstance"
)

type pagedSessionMessageProvider struct {
	stubProvider
	responses map[int]*provider.SessionMessageList
	offsets   []int
	limits    []int
}

func (s *pagedSessionMessageProvider) ListSessionMessages(_ context.Context, _ string, _ string, offset, limit int) (*provider.SessionMessageList, error) {
	s.offsets = append(s.offsets, offset)
	s.limits = append(s.limits, limit)
	if response, ok := s.responses[offset]; ok {
		return response, nil
	}
	return &provider.SessionMessageList{Total: 0, Offset: offset}, nil
}

func TestBuildTUISkillsPickerIncludesRenamedBundledSkill(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Default()

	picker, err := buildTUISkillsPicker(config, "", "")
	if err != nil {
		t.Fatalf("buildTUISkillsPicker failed: %v", err)
	}
	if picker == nil || len(picker.Items) == 0 {
		t.Fatalf("expected picker items, got %#v", picker)
	}
	found := false
	for _, item := range picker.Items {
		if item.Name == "taiyiflow-api" {
			found = true
			if item.SourceLabel != "内置技能" {
				t.Fatalf("unexpected source label: %#v", item)
			}
			if len(item.ReferenceFiles) != 1 {
				t.Fatalf("expected reference files for taiyiflow-api, got %#v", item)
			}
		}
	}
	if !found {
		t.Fatalf("expected taiyiflow-api in picker: %#v", picker.Items)
	}
}

func TestBuildTUISkillsPickerLoadsRemoteItems(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/search" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","popularity":4.8,"downloads":12345,"version":"1.2.3","updatedAt":1710000000}]}`))
	}))
	defer registry.Close()
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Default()
	config.Skills.RegistryURL = registry.URL

	picker, err := buildTUISkillsPicker(config, "", "remote")
	if err != nil {
		t.Fatalf("buildTUISkillsPicker failed: %v", err)
	}
	if picker == nil || len(picker.RemoteItems) != 1 {
		t.Fatalf("expected one remote item, got %#v", picker)
	}
	if picker.RemoteItems[0].Slug != "remote-skill" || picker.RemoteItems[0].Version != "1.2.3" {
		t.Fatalf("unexpected remote item: %#v", picker.RemoteItems[0])
	}
	if picker.RemoteItems[0].Summary != "远程技能摘要" {
		t.Fatalf("expected sanitized remote summary, got %#v", picker.RemoteItems[0])
	}
	if picker.RemoteItems[0].Popularity != 4.8 || picker.RemoteItems[0].Downloads != 12345 {
		t.Fatalf("expected popularity/downloads to map, got %#v", picker.RemoteItems[0])
	}
	if picker.RemoteQuery != "remote" {
		t.Fatalf("expected remote query to persist, got %#v", picker)
	}
}

func TestPrepareTUIFollowupRequestRefreshesRunID(t *testing.T) {
	original := internaltui.Request{
		Input:            "第一轮问题",
		InputFiles:       []string{"a.txt"},
		FlowID:           "flow-1",
		InternetSearch:   true,
		KnowledgeScopes:  []string{"all"},
		KnowledgeDocs:    []string{"doc-1"},
		RunID:            "run-old",
		ReasoningEnabled: true,
	}

	updated := prepareTUIFollowupRequest(original, "第二轮问题")

	if updated.Input != "第二轮问题" {
		t.Fatalf("expected followup input updated, got %q", updated.Input)
	}
	if updated.RunID == "" || updated.RunID == original.RunID {
		t.Fatalf("expected followup request to use a new run id, got %q", updated.RunID)
	}
	if updated.FlowID != original.FlowID || updated.RequestMode != original.RequestMode || updated.ReasoningEnabled != original.ReasoningEnabled {
		t.Fatalf("expected followup request to preserve execution context, got %#v", updated)
	}
	if len(updated.InputFiles) != 1 || updated.InputFiles[0] != "a.txt" {
		t.Fatalf("expected followup request to preserve input files, got %#v", updated.InputFiles)
	}
	if len(updated.KnowledgeScopes) != 1 || updated.KnowledgeScopes[0] != "all" {
		t.Fatalf("expected followup request to preserve knowledge scopes, got %#v", updated.KnowledgeScopes)
	}
	if len(updated.KnowledgeDocs) != 1 || updated.KnowledgeDocs[0] != "doc-1" {
		t.Fatalf("expected followup request to preserve knowledge docs, got %#v", updated.KnowledgeDocs)
	}
	if !updated.InternetSearch {
		t.Fatal("expected followup request to preserve internet search flag")
	}
}

func TestApplyConfigFormSubmissionReturnsFieldError(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app := &App{configManager: manager}
	if err := manager.Save(internalconfig.Default()); err != nil {
		t.Fatalf("save default config failed: %v", err)
	}

	_, err = app.applyConfigFormSubmission(internaltui.ConfigFormSubmission{
		Values: internaltui.ConfigFormValues{
			BackendBaseURL:  "http://localhost:7860",
			ApprovalMode:    "auto",
			PermissionRules: "shell_exec=deny",
		},
	})
	if err == nil {
		t.Fatal("expected invalid permission rules to fail")
	}
	var fieldErr internaltui.ConfigFieldError
	if !errors.As(err, &fieldErr) {
		t.Fatalf("expected ConfigFieldError, got %T (%v)", err, err)
	}
	if fieldErr.Field != "permission_rules" {
		t.Fatalf("expected permission_rules field error, got %#v", fieldErr)
	}
	if !strings.Contains(fieldErr.Message, "权限规则格式错误") {
		t.Fatalf("expected original validation message preserved, got %#v", fieldErr)
	}
}

func TestApprovedClientToolExecutionConfigAddsEphemeralAllowRule(t *testing.T) {
	config := internalconfig.Config{
		ApprovalMode: "confirm",
		PermissionRules: []internalconfig.PermissionRule{
			{Action: "deny", ToolName: "write_file"},
		},
	}

	updated := approvedClientToolExecutionConfig(config, "taiyiflow-computer__focus_window")

	if updated.ApprovalMode != "auto" {
		t.Fatalf("expected approval mode auto, got %#v", updated.ApprovalMode)
	}
	if len(updated.PermissionRules) != 2 {
		t.Fatalf("expected ephemeral allow rule prepended, got %#v", updated.PermissionRules)
	}
	if updated.PermissionRules[0].Action != "allow" || updated.PermissionRules[0].ToolName != "taiyiflow-computer__focus_window" {
		t.Fatalf("unexpected ephemeral allow rule: %#v", updated.PermissionRules[0])
	}
	if updated.PermissionRules[1].Action != "deny" || updated.PermissionRules[1].ToolName != "write_file" {
		t.Fatalf("expected existing permission rules preserved, got %#v", updated.PermissionRules)
	}
}

func TestApprovedClientToolExecutionConfigKeepsReadonly(t *testing.T) {
	updated := approvedClientToolExecutionConfig(internalconfig.Config{ApprovalMode: "readonly"}, "taiyiflow-computer__focus_window")
	if updated.ApprovalMode != "readonly" {
		t.Fatalf("expected readonly approval mode preserved, got %#v", updated.ApprovalMode)
	}
	if len(updated.PermissionRules) != 1 || updated.PermissionRules[0].Action != "allow" {
		t.Fatalf("expected allow rule even in readonly mode, got %#v", updated.PermissionRules)
	}
}

func TestBuildTUIComputerDisplaysPanelShowsOrigin(t *testing.T) {
	panel := buildTUIComputerToolPanel("taiyiflow-computer__displays_context", map[string]any{
		"displays": []any{
			map[string]any{"display_id": "2", "name": "HDMI-1", "primary": true, "origin_x": 1920, "origin_y": -120, "width_px": 2560, "height_px": 1440, "scale_factor": 1.0},
		},
	})
	if panel == nil || !strings.Contains(panel.Body, "origin (1920,-120)") {
		t.Fatalf("expected displays_context panel to show origin, got %#v", panel)
	}
}

func TestBuildTUIComputerScreenshotPanelShowsOriginAndImageUnderstanding(t *testing.T) {
	panel := buildTUIComputerToolPanel("taiyiflow-computer__screenshot", map[string]any{
		"display_id":          "2",
		"display_index":       2,
		"display_origin_x":    1920,
		"display_origin_y":    -120,
		"display_width_px":    2560,
		"display_height_px":   1440,
		"path":                "/tmp/shot.png",
		"size":                123,
		"image_understanding": "Status",
	})
	if panel == nil || !strings.Contains(panel.Body, "origin: (1920,-120)") || !strings.Contains(panel.Body, "image_understanding: Status") {
		t.Fatalf("expected computer screenshot panel to show origin and image understanding, got %#v", panel)
	}
	if !strings.Contains(panel.Hint, "image_understanding") {
		t.Fatalf("expected computer screenshot hint to direct users to image_understanding, got %#v", panel)
	}
}

func TestBuildTUIBrowserScreenshotPanelShowsImageUnderstanding(t *testing.T) {
	panel := buildTUIBrowserToolPanel("taiyiflow-in-chrome__screenshot", map[string]any{
		"tab_id":              "tab-1",
		"path":                "/tmp/browser.png",
		"size":                64,
		"image_understanding": "页面中央是控制台界面",
	})
	if panel == nil || !strings.Contains(panel.Body, "image_understanding: 页面中央是控制台界面") {
		t.Fatalf("expected browser screenshot panel to show image understanding, got %#v", panel)
	}
	if !strings.Contains(panel.Hint, "image_understanding") {
		t.Fatalf("expected browser screenshot hint to direct users to image_understanding, got %#v", panel)
	}
}

func TestTUIAssistantRenderStateDropsTrailingFinalizeControlResidue(t *testing.T) {
	state := tuiAssistantRenderState{}

	segments := state.consume("结果FINAL")
	if len(segments) != 1 || segments[0].Text != "结果" || segments[0].Reasoning {
		t.Fatalf("expected visible answer before finalize residue, got %#v", segments)
	}
	if segments = state.consume("IZE"); len(segments) != 0 {
		t.Fatalf("expected split finalize tail to stay buffered, got %#v", segments)
	}
	if segments = state.flush(); len(segments) != 0 {
		t.Fatalf("expected flush to drop buffered finalize residue, got %#v", segments)
	}
}

func TestTUIAssistantRenderStatePreservesUppercaseWordEndingInIZE(t *testing.T) {
	state := tuiAssistantRenderState{}

	segments := state.consume("S")
	if len(segments) != 1 || segments[0].Text != "S" || segments[0].Reasoning {
		t.Fatalf("expected first uppercase word chunk, got %#v", segments)
	}
	segments = state.consume("IZE")
	if len(segments) != 1 || segments[0].Text != "IZE" || segments[0].Reasoning {
		t.Fatalf("expected uppercase word suffix to be preserved, got %#v", segments)
	}
}

func TestEmitTUIAssistantChunkKeepsSplitMarkerButDropsClosingThinkTagText(t *testing.T) {
	state := tuiAssistantRenderState{}
	events := []internaltui.Event{}
	emit := func(event internaltui.Event) {
		events = append(events, event)
	}

	emitTUIAssistantChunk(emit, &state, internaltui.Event{Kind: internaltui.EventAssistantChunk, Text: "开始<think>内部推理", MessageID: "msg-1"})
	events = nil

	emitTUIAssistantChunk(emit, &state, internaltui.Event{Kind: internaltui.EventAssistantChunk, Text: "</think>", MessageID: "msg-1", SplitAssistant: true})
	if len(events) != 1 {
		t.Fatalf("expected one split marker event, got %#v", events)
	}
	if events[0].Text != "" {
		t.Fatalf("expected split marker to drop hidden closing tag text, got %#v", events[0])
	}
	if !events[0].SplitAssistant || events[0].Kind != internaltui.EventAssistantChunk {
		t.Fatalf("expected split marker assistant event, got %#v", events[0])
	}

	events = nil
	emitTUIAssistantChunk(emit, &state, internaltui.Event{Kind: internaltui.EventAssistantChunk, Text: "结论", MessageID: "msg-1"})
	if len(events) != 1 || events[0].Text != "结论" || events[0].Kind != internaltui.EventAssistantChunk {
		t.Fatalf("expected visible answer after closing think tag, got %#v", events)
	}
}

func TestConsumeHandlesWatermarkCharSplitAcrossChunks(t *testing.T) {
	state := tuiAssistantRenderState{}

	// U+202E (RLO) = 0xE2 0x80 0xAE，模拟 SSE 在第 1 字节后截断
	// chunk1 = "文本" + 0xE2
	chunk1 := "文本\xe2"
	segments1 := state.consume(chunk1)
	if len(segments1) != 1 || segments1[0].Text != "文本" {
		t.Fatalf("chunk1: expected partial text without incomplete byte, got %#v", segments1)
	}

	// chunk2 = 0x80 0xAE + "更多文本"
	// 拼接后 0xE2+0x80+0xAE = U+202E，应被 StripWatermarkCharacters 移除
	chunk2 := "\x80\xae更多文本"
	segments2 := state.consume(chunk2)
	if len(segments2) != 1 || segments2[0].Text != "更多文本" {
		t.Fatalf("chunk2: expected watermark stripped after reassembly, got %#v", segments2)
	}
}

func TestConsumeHandlesMultipleWatermarkCharsSplitAcrossChunks(t *testing.T) {
	state := tuiAssistantRenderState{}

	// U+202E (RLO) = 0xE2 0x80 0xAE (3 bytes)
	// U+200B (ZWSP) = 0xE2 0x80 0x8B (3 bytes)
	// 模拟 chunk1 以两个水印字符的前 2 字节结尾
	chunk1 := "前文\xe2\x80\xe2\x80"
	segments1 := state.consume(chunk1)
	if len(segments1) != 1 || segments1[0].Text != "前文" {
		t.Fatalf("chunk1: expected partial text, got %#v", segments1)
	}

	// chunk2 补全两个水印字符的最后一字节 + 正常文本
	chunk2 := "\xae\x8b后文"
	segments2 := state.consume(chunk2)
	if len(segments2) != 1 || segments2[0].Text != "后文" {
		t.Fatalf("chunk2: expected both watermark chars stripped, got %#v", segments2)
	}
}

func TestConsumePreservesNormalMultiByteCharAcrossChunks(t *testing.T) {
	state := tuiAssistantRenderState{}

	// "你" = U+4F60 = 0xE4 0xBD 0xA0 (3 bytes)
	// 模拟 chunk1 在"你"的第 1 字节后截断
	chunk1 := "hello\xe4"
	segments1 := state.consume(chunk1)
	if len(segments1) != 1 || segments1[0].Text != "hello" {
		t.Fatalf("chunk1: expected partial text without incomplete byte, got %#v", segments1)
	}

	// chunk2 补全"你"的剩余字节
	chunk2 := "\xbd\xa0world"
	segments2 := state.consume(chunk2)
	if len(segments2) != 1 || segments2[0].Text != "你world" {
		t.Fatalf("chunk2: expected complete multi-byte char preserved, got %#v", segments2)
	}
}

func TestConsumeHandlesEmptyChunkAfterPendingUTF8(t *testing.T) {
	state := tuiAssistantRenderState{}

	// chunk1 以不完整 UTF-8 结尾
	chunk1 := "text\xe2"
	segments1 := state.consume(chunk1)
	if len(segments1) != 1 || segments1[0].Text != "text" {
		t.Fatalf("chunk1: expected partial text, got %#v", segments1)
	}

	// chunk2 为空，不应崩溃，也不应输出
	segments2 := state.consume("")
	if segments2 != nil {
		t.Fatalf("chunk2: expected nil for empty chunk, got %#v", segments2)
	}

	// chunk3 补全水印字符并带正常文本
	segments3 := state.consume("\x80\xaeok")
	if len(segments3) != 1 || segments3[0].Text != "ok" {
		t.Fatalf("chunk3: expected watermark stripped, got %#v", segments3)
	}
}

func TestTrailingIncompleteUTF8(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"ascii", "hello", ""},
		{"complete_chinese", "你好", ""},
		{"incomplete_3byte_1", "文本\xe2", "\xe2"},
		{"incomplete_3byte_2", "文本\xe2\x80", "\xe2\x80"},
		{"complete_3byte", "文本\xe2\x80\xae", ""},
		{"incomplete_2byte_1", "text\xc2", "\xc2"},
		{"incomplete_4byte_1", "text\xf0", "\xf0"},
		{"incomplete_4byte_3", "text\xf0\x90\x80", "\xf0\x90\x80"},
		{"all_continuation", "\x80\x80", "\x80\x80"},
		{"ascii_then_incomplete", "abc\xe2\x80", "\xe2\x80"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trailingIncompleteUTF8(tt.input)
			if got != tt.want {
				t.Errorf("trailingIncompleteUTF8(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLoadSessionHistoryPageTracksPaginationAndDisplayOrder(t *testing.T) {
	providerClient := &pagedSessionMessageProvider{
		responses: map[int]*provider.SessionMessageList{
			30: {
				Total:  65,
				Offset: 30,
				Items: []provider.SessionMessage{
					{Sender: "Machine", Text: "较新的回答"},
					{Sender: "user", Text: "更早的问题"},
				},
			},
		},
	}

	page, err := loadSessionHistoryPage(context.Background(), providerClient, "flow-1", "session-1", 30, 30)
	if err != nil {
		t.Fatalf("expected loadSessionHistoryPage to succeed, got %v", err)
	}
	if len(providerClient.offsets) != 1 || providerClient.offsets[0] != 30 {
		t.Fatalf("expected offset 30 request, got %#v", providerClient.offsets)
	}
	if len(providerClient.limits) != 1 || providerClient.limits[0] != 30 {
		t.Fatalf("expected limit 30 request, got %#v", providerClient.limits)
	}
	if page.LoadedCount != 32 || page.Total != 65 {
		t.Fatalf("expected loaded/total counters to advance, got %#v", page)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("expected two displayable history entries, got %#v", page.Entries)
	}
	if page.Entries[0].Role != "user" || page.Entries[0].Content != "更早的问题" {
		t.Fatalf("expected older user message first after reverse, got %#v", page.Entries)
	}
	if page.Entries[1].Role != "assistant" || page.Entries[1].Content != "较新的回答" {
		t.Fatalf("expected newer assistant message second, got %#v", page.Entries)
	}
}

func TestExecuteTUISkillsPickerInstallDetailRefreshesPickerAndPanel(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	workspaceDir := t.TempDir()
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/search":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","version":"1.2.3","updatedAt":1710000000}]}`))
		case "/api/v1/skills/remote-skill":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"skill":{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","updatedAt":1710000000},"latestVersion":{"version":"1.2.3"},"metadata":{"os":["macOS"],"systems":["cli"]},"owner":{"handle":"tester","displayName":"测试者"},"metaContent":{"skillMd":"---\nname: Remote Skill\n---\n\n远程技能正文详情"}}`))
		case "/api/v1/download":
			archiveBytes := buildRemoteSkillArchive(t, "1.2.3")
			writer.Header().Set("Content-Type", "application/zip")
			_, _ = writer.Write(archiveBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer registry.Close()

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.RegistryURL = registry.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, configManager: manager}

	promptResult, err := app.executeTUISkillsPickerCommand(config, workspaceDir, "/skills-picker install-detail-select remote-skill remote")
	if err != nil {
		t.Fatalf("executeTUISkillsPickerCommand failed: %v", err)
	}
	if promptResult.SkillsPicker == nil || len(promptResult.SkillsPicker.RemoteItems) != 1 {
		t.Fatalf("expected refreshed remote picker, got %#v", promptResult.SkillsPicker)
	}
	if len(promptResult.Events) == 0 || promptResult.Events[len(promptResult.Events)-1].Panel == nil {
		t.Fatalf("expected install target panel event, got %#v", promptResult.Events)
	}
	installCommand := promptResult.Events[len(promptResult.Events)-1].Panel.Actions[0].Command
	result, err := app.executeTUISkillsPickerCommand(config, workspaceDir, installCommand)
	if err != nil {
		t.Fatalf("execute install-scope failed: %v", err)
	}
	if result.SkillsPicker == nil || len(result.SkillsPicker.RemoteItems) != 1 {
		t.Fatalf("expected refreshed remote picker, got %#v", result.SkillsPicker)
	}
	if !result.SkillsPicker.RemoteItems[0].Installed {
		t.Fatalf("expected remote item to be marked installed, got %#v", result.SkillsPicker.RemoteItems[0])
	}
	if len(result.Events) < 2 || result.Events[1].Panel == nil {
		t.Fatalf("expected note and detail panel events, got %#v", result.Events)
	}
	if !strings.Contains(result.Events[1].Panel.Body, "发布者: 测试者 (tester)") {
		t.Fatalf("expected full remote detail panel, got %#v", result.Events[1].Panel)
	}
	if !strings.Contains(result.Events[1].Panel.Body, "SKILL.md 内容:") || !strings.Contains(result.Events[1].Panel.Body, "远程技能正文详情") {
		t.Fatalf("expected remote skill markdown in detail panel, got %#v", result.Events[1].Panel)
	}
	defaultSkillsDir, dirErr := internalconfig.DefaultSkillsDir()
	if dirErr != nil {
		t.Fatalf("default skills dir failed: %v", dirErr)
	}
	installedSkillPath := filepath.Join(defaultSkillsDir, "remote-skill", "SKILL.md")
	if _, statErr := os.Stat(installedSkillPath); statErr != nil {
		t.Fatalf("expected installed skill file, got %v", statErr)
	}
}

func TestExecuteTUISkillsPickerInstallSelectUsesRememberedSessionScope(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	workspaceDir := t.TempDir()
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/search":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","version":"1.2.3","updatedAt":1710000000}]}`))
		case "/api/v1/skills/remote-skill":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"skill":{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","updatedAt":1710000000},"latestVersion":{"version":"1.2.3"},"metadata":{"os":["macOS"],"systems":["cli"]},"owner":{"handle":"tester","displayName":"测试者"}}`))
		case "/api/v1/download":
			archiveBytes := buildRemoteSkillArchive(t, "1.2.3")
			writer.Header().Set("Content-Type", "application/zip")
			_, _ = writer.Write(archiveBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer registry.Close()
	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.RegistryURL = registry.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, configManager: manager, skillInstallScopeSession: internalconfig.SkillInstallScopeWorkspace, skillInstallScopePinned: true}
	result, err := app.executeTUISkillsPickerCommand(config, workspaceDir, "/skills-picker install-select remote-skill remote")
	if err != nil {
		t.Fatalf("executeTUISkillsPickerCommand failed: %v", err)
	}
	if len(result.Events) == 0 || result.Events[0].Panel != nil {
		t.Fatalf("expected direct install note without prompt, got %#v", result.Events)
	}
	installedSkillPath := filepath.Join(workspaceDir, ".snz1dp", "skills", "remote-skill", "SKILL.md")
	if _, statErr := os.Stat(installedSkillPath); statErr != nil {
		t.Fatalf("expected workspace installed skill file, got %v", statErr)
	}
}

func TestExecuteTUISkillsPickerRemoveLocalDetailRefreshesPickerAndClosesPanel(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	workspaceDir := t.TempDir()
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/search":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","version":"1.2.3","updatedAt":1710000000}]}`))
		case "/api/v1/download":
			archiveBytes := buildRemoteSkillArchive(t, "1.2.3")
			writer.Header().Set("Content-Type", "application/zip")
			_, _ = writer.Write(archiveBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer registry.Close()

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.RegistryURL = registry.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	installedSkillDir := filepath.Join(workspaceDir, ".snz1dp", "skills", "remote-skill")
	if err := os.MkdirAll(filepath.Join(installedSkillDir, ".snz1dp"), 0o755); err != nil {
		t.Fatalf("create installed skill metadata dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(installedSkillDir, "SKILL.md"), []byte("---\nname: Remote Skill\ndescription: 已安装远程技能\ncategory: remote\n---\n\nInstalled body\n"), 0o644); err != nil {
		t.Fatalf("write installed skill failed: %v", err)
	}
	originBody := fmt.Sprintf("{\"version\":1,\"registry\":%q,\"slug\":\"remote-skill\",\"installedVersion\":\"1.2.3\"}", registry.URL)
	if err := os.WriteFile(filepath.Join(installedSkillDir, ".snz1dp", "origin.json"), []byte(originBody), 0o644); err != nil {
		t.Fatalf("write installed skill origin failed: %v", err)
	}
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, configManager: manager, skillInstallScopeSession: internalconfig.SkillInstallScopeWorkspace, skillInstallScopePinned: true}

	picker, err := buildTUISkillsPicker(config, workspaceDir, "remote")
	if err != nil {
		t.Fatalf("buildTUISkillsPicker failed: %v", err)
	}
	removableFound := false
	for _, item := range picker.Items {
		if item.RemoveTarget == "remote-skill" {
			removableFound = item.Removable
			if item.ToggleTarget != "remote-skill" {
				t.Fatalf("expected toggle target for installed remote skill, got %#v", item)
			}
			if item.Disabled {
				t.Fatalf("expected installed remote skill to default enabled, got %#v", item)
			}
			break
		}
	}
	if !removableFound {
		t.Fatalf("expected installed remote skill to be marked removable, got %#v", picker.Items)
	}

	result, err := app.executeTUISkillsPickerCommand(config, workspaceDir, "/skills-picker remove-local-detail remote-skill remote")
	if err != nil {
		t.Fatalf("remove-local-detail failed: %v", err)
	}
	if !result.CloseToolPanel {
		t.Fatalf("expected local detail removal to close panel, got %#v", result)
	}
	if result.SkillsPicker == nil {
		t.Fatalf("expected refreshed picker, got %#v", result)
	}
	for _, item := range result.SkillsPicker.Items {
		if item.RemoveTarget == "remote-skill" || item.Name == "Remote Skill" {
			t.Fatalf("expected removed skill to disappear from local picker, got %#v", result.SkillsPicker.Items)
		}
	}
	if len(result.Events) == 0 || !strings.Contains(result.Events[0].Text, "已删除技能") || !strings.Contains(result.Events[0].Text, "remote-skill") {
		t.Fatalf("expected delete confirmation note, got %#v", result.Events)
	}
	if len(result.SkillsPicker.RemoteItems) != 1 || result.SkillsPicker.RemoteItems[0].Installed {
		t.Fatalf("expected remote entry to become uninstalled, got %#v", result.SkillsPicker.RemoteItems)
	}
}

func TestExecuteTUISkillsPickerDisableLocalDetailRefreshesPickerAndKeepsSkillInstalled(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	workspaceDir := t.TempDir()
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/search":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","version":"1.2.3","updatedAt":1710000000}]}`))
		case "/api/v1/download":
			archiveBytes := buildRemoteSkillArchive(t, "1.2.3")
			writer.Header().Set("Content-Type", "application/zip")
			_, _ = writer.Write(archiveBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer registry.Close()

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.RegistryURL = registry.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	installedSkillDir := filepath.Join(workspaceDir, ".snz1dp", "skills", "remote-skill")
	if err := os.MkdirAll(filepath.Join(installedSkillDir, ".snz1dp"), 0o755); err != nil {
		t.Fatalf("create installed skill metadata dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(installedSkillDir, "SKILL.md"), []byte("---\nname: Remote Skill\ndescription: 已安装远程技能\ncategory: remote\n---\n\nInstalled body\n"), 0o644); err != nil {
		t.Fatalf("write installed skill failed: %v", err)
	}
	originBody := fmt.Sprintf("{\"version\":1,\"registry\":%q,\"slug\":\"remote-skill\",\"installedVersion\":\"1.2.3\"}", registry.URL)
	if err := os.WriteFile(filepath.Join(installedSkillDir, ".snz1dp", "origin.json"), []byte(originBody), 0o644); err != nil {
		t.Fatalf("write installed skill origin failed: %v", err)
	}
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, configManager: manager, skillInstallScopeSession: internalconfig.SkillInstallScopeWorkspace, skillInstallScopePinned: true}

	result, err := app.executeTUISkillsPickerCommand(config, workspaceDir, "/skills-picker disable-local-detail remote-skill remote")
	if err != nil {
		t.Fatalf("disable-local-detail failed: %v", err)
	}
	if result.CloseToolPanel {
		t.Fatalf("expected local disable to keep detail open, got %#v", result)
	}
	if result.SkillsPicker == nil {
		t.Fatalf("expected refreshed picker, got %#v", result)
	}
	if result.SkillsPicker.ActiveTab != "local" {
		t.Fatalf("expected local tab to remain active, got %#v", result.SkillsPicker)
	}
	if len(result.Events) < 2 || !strings.Contains(result.Events[0].Text, "已禁用技能候选资格") {
		t.Fatalf("expected disable confirmation note, got %#v", result.Events)
	}
	if result.Events[1].Panel == nil || !strings.Contains(result.Events[1].Panel.Body, "状态: 显式禁用") {
		t.Fatalf("expected refreshed local detail panel, got %#v", result.Events)
	}
	disabledFound := false
	for _, item := range result.SkillsPicker.Items {
		if item.ToggleTarget == "remote-skill" {
			disabledFound = true
			if !item.Disabled || item.StateLabel != "显式禁用" {
				t.Fatalf("expected picker item disabled after toggle, got %#v", item)
			}
		}
	}
	if !disabledFound {
		t.Fatalf("expected disabled installed skill to stay in picker, got %#v", result.SkillsPicker.Items)
	}
	updatedConfig, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if !containsIgnoreCase(updatedConfig.Skills.Disabled, "Remote Skill") {
		t.Fatalf("expected disabled skill recorded, got %#v", updatedConfig.Skills.Disabled)
	}
	if _, statErr := os.Stat(filepath.Join(installedSkillDir, "SKILL.md")); statErr != nil {
		t.Fatalf("expected installed skill files kept after disable, got %v", statErr)
	}
	if len(result.SkillsPicker.RemoteItems) != 1 || !result.SkillsPicker.RemoteItems[0].Installed {
		t.Fatalf("expected remote entry to remain installed, got %#v", result.SkillsPicker.RemoteItems)
	}
}

func TestExecuteTUILocalShellCommandAutoBackgroundsTailFollow(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.Workdir = t.TempDir()
	config.ApprovalMode = "auto"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	app := &App{configManager: manager}

	result, execErr := app.executeTUILocalShellCommand(context.Background(), "!tail -f /dev/null", internaltui.Snapshot{})
	if execErr != nil {
		t.Fatalf("executeTUILocalShellCommand failed: %v", execErr)
	}
	if !result.Handled {
		t.Fatalf("expected local shell command to be handled, got %#v", result)
	}
	if len(result.Events) < 2 {
		t.Fatalf("expected note and task panel events, got %#v", result.Events)
	}
	if !strings.Contains(result.Events[0].Text, "已转为后台任务执行") {
		t.Fatalf("expected background task note, got %#v", result.Events[0])
	}
	if !strings.Contains(result.Events[0].Text, "follow 模式") {
		t.Fatalf("expected follow-mode explanation, got %#v", result.Events[0])
	}
	if result.Events[1].Panel == nil {
		t.Fatalf("expected task panel event, got %#v", result.Events)
	}
	panel := result.Events[1].Panel
	if !strings.Contains(panel.Title, "后台任务") {
		t.Fatalf("expected task panel title, got %#v", panel)
	}
	taskID := strings.TrimSpace(strings.SplitN(panel.Summary, " · ", 2)[0])
	if taskID == "" || taskID == "<unknown>" {
		t.Fatalf("expected concrete task id, got %#v", panel)
	}
	output, tasksPanel, outputErr := app.executeTasksSlashCommand(context.Background(), config, "/tasks")
	if outputErr != nil {
		t.Fatalf("expected /tasks to list background task, got %v", outputErr)
	}
	if tasksPanel == nil || !strings.Contains(tasksPanel.Body, taskID) {
		t.Fatalf("expected /tasks to include task id %q, output=%q panel=%#v", taskID, output, tasksPanel)
	}
	if _, _, stopErr := runLocalUserTool(context.Background(), config, "task_stop", map[string]any{"task_id": taskID}); stopErr != nil {
		t.Fatalf("expected task_stop to succeed, got %v", stopErr)
	}
	// 任务停止后仍保留，不再自动清理
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, detailsAny, detailsErr := runLocalReadTool(context.Background(), config, "task_output", map[string]any{"task_id": taskID})
		if detailsErr == nil {
			if details, ok := detailsAny.(map[string]any); ok {
				if status, _ := details["status"].(string); status == "cancelled" {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected background task %s to be cancelled and still queryable", taskID)
}

func TestExecuteTUILocalShellCommandRejectsInteractiveTTYCommand(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.Workdir = t.TempDir()
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	app := &App{configManager: manager}

	result, execErr := app.executeTUILocalShellCommand(context.Background(), "!vim README.md", internaltui.Snapshot{})
	if execErr != nil {
		t.Fatalf("executeTUILocalShellCommand failed: %v", execErr)
	}
	if !result.Handled || len(result.Events) != 1 {
		t.Fatalf("expected one handled error event, got %#v", result)
	}
	if result.Events[0].Kind != internaltui.EventErrorNote {
		t.Fatalf("expected error note, got %#v", result.Events[0])
	}
	if !strings.Contains(result.Events[0].Text, "需要交互终端") {
		t.Fatalf("expected interactive terminal guidance, got %#v", result.Events[0])
	}
}

func TestBuildTUIClientToolPanelShowsSuggestedReadRangeForFileEditFailure(t *testing.T) {
	panel := buildTUIClientToolPanel("file_edit", map[string]any{
		"path":           "cmd/root.go",
		"status":         "not_found",
		"sha256_before":  "abc123",
		"nearby_excerpt": "10: func main() {\n11: \tprintln(\"hello\")",
		"prefetched_read": map[string]any{
			"start_line": 10,
			"end_line":   11,
			"content":    "func main() {\n\tprintln(\"hello\")",
		},
		"suggested_read_start_line": 10,
		"suggested_read_end_line":   11,
	}, assertError("file_edit 未找到 old_string"))
	if panel == nil {
		t.Fatal("expected panel")
	}
	if !strings.Contains(panel.Body, "建议先读: read_file cmd/root.go 行 10-11") {
		t.Fatalf("unexpected panel body: %#v", panel)
	}
	if !strings.Contains(panel.Body, "预取上下文(10-11 行):") || !strings.Contains(panel.Body, "println(\"hello\")") {
		t.Fatalf("expected prefetched context in panel body: %#v", panel)
	}
}

func TestBuildTUISkillAutoInstallConfirmRequest(t *testing.T) {
	request := buildTUISkillAutoInstallConfirmRequest(skillInstallApprovalRequest{
		Candidate: skillInstallCandidate{
			Slug:        "context-review",
			DisplayName: "Context Review",
			Summary:     "上下文审查技能",
		},
		Scope:       internalconfig.SkillInstallScopeWorkspace,
		InstallPath: "/tmp/work/.snz1dp/skills",
		Reason:      "缺失技能名 Context Review 的精确匹配",
	}, nil)
	if !strings.Contains(request.Title, "自动安装技能") {
		t.Fatalf("unexpected title: %#v", request)
	}
	if request.Command != "install context-review" {
		t.Fatalf("expected install command preview, got %#v", request)
	}
	if !strings.Contains(request.PreviewBody, "/tmp/work/.snz1dp/skills") || !strings.Contains(request.PreviewBody, "上下文审查技能") {
		t.Fatalf("unexpected preview body: %#v", request)
	}
	if request.ApproveLabel != "安装并继续" || request.RejectLabel != "跳过安装" {
		t.Fatalf("unexpected labels: %#v", request)
	}
}

func TestApplyMetadataContextWindowBudgetInitializesSnapshotMetrics(t *testing.T) {
	snapshot := internaltui.Snapshot{}

	applyMetadataContextWindowBudget(&snapshot, 200000)

	if snapshot.RequestStats == nil || snapshot.RequestStats.TokenUsage == nil || snapshot.RequestStats.TokenUsage.ContextWindow == nil {
		t.Fatalf("expected context window metrics to be initialized, got %#v", snapshot.RequestStats)
	}
	if snapshot.RequestStats.TokenUsage.ContextWindow.MaxInputTokens != 200000 {
		t.Fatalf("expected max input tokens to be applied, got %#v", snapshot.RequestStats.TokenUsage.ContextWindow)
	}
}

func TestApplyMetadataContextWindowBudgetIgnoresInvalidBudget(t *testing.T) {
	snapshot := internaltui.Snapshot{}

	applyMetadataContextWindowBudget(&snapshot, 0)

	if snapshot.RequestStats != nil {
		t.Fatalf("expected request stats unchanged for invalid budget, got %#v", snapshot.RequestStats)
	}
}

func TestBuildTUISnapshotIncludesCurrentUserName(t *testing.T) {
	app := &App{}
	snapshot, err := app.buildTUISnapshot(context.Background(), &stubProvider{}, internalconfig.Default(), "", backendStatus{Available: true})
	if err != nil {
		t.Fatalf("buildTUISnapshot failed: %v", err)
	}
	if snapshot.CurrentUserName != "" {
		t.Fatalf("expected empty current user with default stub provider, got %#v", snapshot)
	}

	providerWithUser := &stubSnapshotProvider{stubProvider: &stubProvider{}, currentUser: &provider.CurrentUser{Name: "张三"}}
	snapshot, err = app.buildTUISnapshot(context.Background(), providerWithUser, internalconfig.Default(), "", backendStatus{Available: true})
	if err != nil {
		t.Fatalf("buildTUISnapshot failed: %v", err)
	}
	if snapshot.CurrentUserName != "张三" {
		t.Fatalf("expected current user name to be mapped, got %#v", snapshot)
	}
}

type stubSnapshotProvider struct {
	*stubProvider
	currentUser    *provider.CurrentUser
	currentUserErr error
	serviceConfig  *provider.ServiceConfig
	serviceErr     error
	metadata       *provider.Metadata
	metadataErr    error
	knowledgeBases *provider.KnowledgeBaseList
	knowledgeErr   error
}

func (s stubSnapshotProvider) GetCurrentUserInfo(context.Context) (*provider.CurrentUser, error) {
	return s.currentUser, s.currentUserErr
}

func (s stubSnapshotProvider) GetMetadata(context.Context, string) (*provider.Metadata, error) {
	if s.metadata != nil || s.metadataErr != nil {
		return s.metadata, s.metadataErr
	}
	return s.stubProvider.GetMetadata(context.Background(), "")
}

func (s stubSnapshotProvider) GetServiceConfig(context.Context) (*provider.ServiceConfig, error) {
	if s.serviceConfig != nil || s.serviceErr != nil {
		return s.serviceConfig, s.serviceErr
	}
	return s.stubProvider.GetServiceConfig(context.Background())
}

func (s stubSnapshotProvider) ListKnowledgeBases(context.Context, string, string, int, int) (*provider.KnowledgeBaseList, error) {
	if s.knowledgeBases != nil || s.knowledgeErr != nil {
		return s.knowledgeBases, s.knowledgeErr
	}
	return s.stubProvider.ListKnowledgeBases(context.Background(), "", "", 0, 0)
}

func (s stubSnapshotProvider) GetFlow(context.Context, string) (*provider.FlowSummary, error) {
	return s.stubProvider.GetFlow(context.Background(), "")
}

func TestBuildTUISnapshotMarksAuthRequired(t *testing.T) {
	app := &App{}
	providerWithAuthError := &stubSnapshotProvider{stubProvider: &stubProvider{}, currentUserErr: fmt.Errorf("HTTP 401 unauthorized")}
	snapshot, err := app.buildTUISnapshot(context.Background(), providerWithAuthError, internalconfig.Default(), "", backendStatus{Available: true})
	if err != nil {
		t.Fatalf("buildTUISnapshot failed: %v", err)
	}
	if !snapshot.AuthRequired {
		t.Fatalf("expected auth required snapshot, got %#v", snapshot)
	}
	if !strings.Contains(snapshot.AuthStatusReason, "/login") {
		t.Fatalf("expected auth status reason to include /login hint, got %#v", snapshot)
	}
}

func TestBuildInitialTUISnapshotMarksAuthRequired(t *testing.T) {
	app := &App{}
	snapshot, err := app.buildInitialTUISnapshot(internalconfig.Default(), "", backendStatus{Available: true, AuthRequired: true, Reason: "请重新登录太乙智启服务"}, nil)
	if err != nil {
		t.Fatalf("buildInitialTUISnapshot failed: %v", err)
	}
	if !snapshot.AuthRequired {
		t.Fatalf("expected initial snapshot to keep auth required, got %#v", snapshot)
	}
	if !strings.Contains(snapshot.AuthStatusReason, "/login") {
		t.Fatalf("expected initial auth status reason to include /login hint, got %#v", snapshot)
	}
	if !strings.Contains(snapshot.AuthStatusReason, "请重新登录太乙智启服务") {
		t.Fatalf("expected initial auth status reason to preserve backend reason, got %#v", snapshot)
	}
}

func TestPrepareInitialTUIAuthenticationRefreshesAccessTokenBeforeInitialSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendAPIPath("/users/whoami/userinfo"):
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(writer, `{"code":401,"message":"unauthorized"}`)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case backendAPIPath("/version"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"package":"taiyiflow","main_version":"1.0.0"}`)
		case authAPIPath("/sso/salt"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"salt-ticket","value":"salt-value"}}`)
		case authAPIPath("/sso"):
			if got := request.URL.Query().Get("persistent_token"); got != "persist-1" {
				t.Fatalf("unexpected persistent token: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","persistent_token":"persist-2","idle_duration":3600,"scope":"employee"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "expired-access"
	config.PersistentToken = "persist-1"
	config.DeviceID = "device-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	providerClient := buildProviderClient(config)
	updatedConfig := app.prepareInitialTUIAuthentication(context.Background(), providerClient, config)
	backend := app.inspectBackendStatus(context.Background(), providerClient, updatedConfig.BackendBaseURL)
	snapshot, err := app.buildInitialTUISnapshot(updatedConfig, "", backend, nil)
	if err != nil {
		t.Fatalf("buildInitialTUISnapshot failed: %v", err)
	}
	if snapshot.AuthRequired {
		t.Fatalf("expected refreshed startup auth to avoid login overlay, got %#v", snapshot)
	}
	if reloaded, reloadErr := manager.Load(); reloadErr != nil {
		t.Fatalf("reload config failed: %v", reloadErr)
	} else if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-2" {
		t.Fatalf("expected refreshed auth state to persist before startup snapshot, got %#v", reloaded)
	}
}

func TestRunTUIReturnsWorkspaceInstanceErrorBeforeStartupRequests(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount.Add(1)
		http.NotFound(writer, request)
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	workdir := t.TempDir()
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.Workdir = workdir
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	lease, err := workspaceinstance.Acquire(workdir)
	if err != nil {
		t.Fatalf("acquire workspace lease failed: %v", err)
	}
	defer func() {
		_ = lease.Close()
	}()

	app := &App{configManager: manager, stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	err = app.runTUI(context.Background(), nil)
	if err == nil {
		t.Fatal("expected workspace instance error")
	}
	if !strings.Contains(err.Error(), "已有活跃 CLI 实例") {
		t.Fatalf("expected workspace instance error, got %v", err)
	}
	if got := requestCount.Load(); got != 0 {
		t.Fatalf("expected no startup backend requests before workspace lease check, got %d", got)
	}
}

func TestBuildTUIHelpLinesUsesLoginOverlayWording(t *testing.T) {
	lines := buildTUIHelpLines(internaltui.Snapshot{BackendAvailable: true})
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "/chat") {
		t.Fatalf("expected help text to remove /chat, got %q", joined)
	}
	if strings.Contains(joined, "/think") {
		t.Fatalf("expected help text to remove /think, got %q", joined)
	}
	if !strings.Contains(joined, "/internet [on|off|status]") || !strings.Contains(joined, "/knowledge 打开知识库范围选择器") || !strings.Contains(joined, "/upload <本地文件路径> 上传文件并附带到当前对话") {
		t.Fatalf("expected help text to include split dialogue commands, got %q", joined)
	}
	if !strings.Contains(joined, "/login 打开登录表单") {
		t.Fatalf("expected help text to describe /login as overlay entry, got %q", joined)
	}
	if !strings.Contains(joined, "/refresh 刷新当前会话的历史消息") {
		t.Fatalf("expected help text to include /refresh, got %q", joined)
	}
	if !strings.Contains(joined, "/session title <标题> 设置当前会话标题") {
		t.Fatalf("expected help text to include /session title, got %q", joined)
	}
	if !strings.Contains(joined, "/todo [show|hide|status]") {
		t.Fatalf("expected help text to include /todo sidebar command, got %q", joined)
	}
	if !strings.Contains(joined, "kitty/iTerm2 终端图片协议") {
		t.Fatalf("expected help text to mention terminal image captcha rendering, got %q", joined)
	}
	if strings.Contains(joined, "/login [status|qr|password|logout]") {
		t.Fatalf("expected help text to drop legacy login command catalog, got %q", joined)
	}

	readonlyLines := buildTUIHelpLines(internaltui.Snapshot{BackendAvailable: false, BackendStatusReason: "服务端不可用"})
	readonlyJoined := strings.Join(readonlyLines, "\n")
	if !strings.Contains(readonlyJoined, "/login 打开登录表单") {
		t.Fatalf("expected readonly help text to direct to /login overlay, got %q", readonlyJoined)
	}
	if !strings.Contains(readonlyJoined, "/refresh 刷新当前会话的历史消息") {
		t.Fatalf("expected readonly help text to include /refresh, got %q", readonlyJoined)
	}
	if !strings.Contains(readonlyJoined, "/session title <标题> 设置当前会话标题") {
		t.Fatalf("expected readonly help text to include /session title, got %q", readonlyJoined)
	}
	if strings.Contains(readonlyJoined, "查看登录状态") {
		t.Fatalf("expected readonly help text to stop using legacy status wording, got %q", readonlyJoined)
	}
	if !strings.Contains(readonlyJoined, "/todo [show|hide|status]") {
		t.Fatalf("expected readonly help text to include /todo sidebar command, got %q", readonlyJoined)
	}
}

func TestBuildInitialTUISnapshotLoadsSessionTodoItems(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()
	if err := app.sessionStore.Save(agent.SessionState{
		FlowID:    "flow-1",
		Workdir:   workdir,
		TodoItems: []agent.TodoItem{{Title: "实现右侧待办栏", Status: "in_progress", Notes: "放到历史区右边"}},
	}); err != nil {
		t.Fatalf("save session failed: %v", err)
	}
	config := internalconfig.Default()
	config.Workdir = workdir

	snapshot, err := app.buildInitialTUISnapshot(config, "flow-1", backendStatus{Available: true}, nil)
	if err != nil {
		t.Fatalf("buildInitialTUISnapshot failed: %v", err)
	}
	if len(snapshot.TodoItems) != 1 {
		t.Fatalf("expected initial snapshot to load todo items, got %#v", snapshot.TodoItems)
	}
	if snapshot.TodoItems[0].Title != "实现右侧待办栏" || snapshot.TodoItems[0].Notes != "放到历史区右边" {
		t.Fatalf("unexpected todo item mapping: %#v", snapshot.TodoItems[0])
	}
}

func TestBuildTUISnapshotUsesProviderSessionTitle(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()
	if err := app.sessionStore.Save(agent.SessionState{
		FlowID:       "flow-1",
		Workdir:      workdir,
		SessionID:    "session-1",
		SessionTitle: "",
	}); err != nil {
		t.Fatalf("save session failed: %v", err)
	}
	providerStub := &sessionListProvider{responses: map[int]*provider.SessionList{
		0: {
			Total: 1,
			Items: []provider.SessionSummary{{ID: "session-1", Name: "服务端标题"}},
		},
	}}
	config := internalconfig.Default()
	config.Workdir = workdir

	snapshot, err := app.buildTUISnapshot(context.Background(), providerStub, config, "flow-1", backendStatus{Available: true})
	if err != nil {
		t.Fatalf("buildTUISnapshot failed: %v", err)
	}
	if snapshot.SessionTitle != "服务端标题" {
		t.Fatalf("expected provider session title to win, got %#v", snapshot)
	}
}

func TestExecuteTUISessionCommandUpdatesRemoteSessionTitle(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()
	if err := app.sessionStore.Save(agent.SessionState{
		FlowID:          "flow-1",
		FlowDisplayName: "Flow One",
		Workdir:         workdir,
		SessionID:       "session-1",
		ContextDir:      workdir,
		RequestMode:     provider.RequestTypeAgent,
	}); err != nil {
		t.Fatalf("save session failed: %v", err)
	}
	var gotFlowID string
	var gotSessionID string
	var gotTitle string
	providerStub := &stubProvider{updateSessionTitle: func(_ context.Context, flowID string, sessionID string, sessionTitle string) (string, error) {
		gotFlowID = flowID
		gotSessionID = sessionID
		gotTitle = sessionTitle
		return sessionTitle, nil
	}}

	result, err := app.executeTUISessionCommand(context.Background(), providerStub, "/session title 项目排障", internaltui.Snapshot{
		DefaultFlow:      "flow-1",
		DefaultFlowLabel: "Flow One",
		Workdir:          workdir,
		ContextDir:       workdir,
		RequestMode:      provider.RequestTypeAgent,
		SessionID:        "session-1",
	})
	if err != nil {
		t.Fatalf("executeTUISessionCommand failed: %v", err)
	}
	if result.Snapshot == nil || result.Snapshot.SessionTitle != "项目排障" {
		t.Fatalf("expected updated snapshot title, got %#v", result.Snapshot)
	}
	if len(result.Events) != 1 || !strings.Contains(result.Events[0].Text, "项目排障") {
		t.Fatalf("expected success note to mention title, got %#v", result.Events)
	}
	if gotFlowID != "flow-1" || gotSessionID != "session-1" || gotTitle != "项目排障" {
		t.Fatalf("expected UpdateSessionTitle to receive flow/session/title, got flow=%q session=%q title=%q", gotFlowID, gotSessionID, gotTitle)
	}
	loaded, err := app.sessionStore.Load("flow-1", workdir)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if loaded.SessionTitle != "项目排障" {
		t.Fatalf("expected session title to persist after remote update, got %#v", loaded)
	}
}

func TestExecuteTUISessionCommandRejectsTitleForNewSession(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()

	result, err := app.executeTUISessionCommand(context.Background(), &stubProvider{}, "/session title 项目排障", internaltui.Snapshot{
		DefaultFlow: "flow-1",
		Workdir:     workdir,
	})
	if err != nil {
		t.Fatalf("executeTUISessionCommand failed: %v", err)
	}
	if len(result.Events) != 1 || result.Events[0].Kind != internaltui.EventErrorNote {
		t.Fatalf("expected error note for new session, got %#v", result.Events)
	}
	if !strings.Contains(result.Events[0].Text, "新会话") {
		t.Fatalf("expected new session hint, got %#v", result.Events[0])
	}
}

func TestBuildTUISnapshotMapsMetadataChatCapabilities(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	providerWithMetadata := &stubSnapshotProvider{
		stubProvider: &stubProvider{},
		serviceConfig: &provider.ServiceConfig{
			FeatureFlags:      provider.ServiceFeatureFlags{TextToSpeech: true, SpeechToText: true, HumanAIDialogue: true},
			MaxFileSizeUpload: 100,
			DocExts:           []string{"pdf", "docx"},
			MilvusEnabled:     true,
		},
		metadata: &provider.Metadata{
			WelcomeMessage: "欢迎使用",
			SubHead:        "支持联网与知识库",
			Questions:      []string{"问题一", "问题二"},
			Capabilities: provider.ChatCapabilities{
				InternetSearch:         true,
				SupportKnowledge:       true,
				SupportMCPTools:        true,
				SupportHumanAIDialogue: true,
				DocumentUnderstanding:  true,
			},
		},
		knowledgeBases: &provider.KnowledgeBaseList{Items: []provider.KnowledgeBaseSummary{{Code: "kb-a", Name: "知识库A"}}},
	}
	snapshot, err := app.buildTUISnapshot(context.Background(), providerWithMetadata, internalconfig.Default(), "flow-1", backendStatus{Available: true})
	if err != nil {
		t.Fatalf("buildTUISnapshot failed: %v", err)
	}
	if snapshot.WelcomeMessage != "欢迎使用" || snapshot.SubHead != "支持联网与知识库" {
		t.Fatalf("expected metadata welcome fields to map, got %#v", snapshot)
	}
	if len(snapshot.Questions) != 2 {
		t.Fatalf("expected metadata questions to map, got %#v", snapshot.Questions)
	}
	if !snapshot.InternetSearchSupported || !snapshot.InternetSearchEnabled || !snapshot.InternetSearchDefault {
		t.Fatalf("expected internet search capability to map, got %#v", snapshot)
	}
	if !snapshot.KnowledgeSearchSupported || len(snapshot.KnowledgeBases) != 1 || snapshot.KnowledgeBases[0].Code != "kb-a" {
		t.Fatalf("expected knowledge capability to map, got %#v", snapshot)
	}
	if !snapshot.KnowledgeSystemEnabled || !snapshot.MCPToolsSupported || !snapshot.TextToSpeechSupported || !snapshot.SpeechToTextSupported || !snapshot.HumanAIDialogueSupported {
		t.Fatalf("expected service and metadata capabilities to map, got %#v", snapshot)
	}
	if snapshot.MaxFileSizeUploadMB != 100 || len(snapshot.SupportedDocumentExts) != 2 {
		t.Fatalf("expected upload limits and doc extensions to map, got %#v", snapshot)
	}
	if len(snapshot.FileCapabilities) != 1 || snapshot.FileCapabilities[0].Key != provider.FeatureDocumentUnderstanding {
		t.Fatalf("expected document file capability to map, got %#v", snapshot.FileCapabilities)
	}
}

func TestRenderTUIChatFilesStatusIncludesServiceConfig(t *testing.T) {
	snapshot := internaltui.Snapshot{
		MaxFileSizeUploadMB:   100,
		SupportedDocumentExts: []string{"pdf", "docx"},
		FileCapabilities:      []internaltui.FileCapability{{Key: provider.FeatureDocumentUnderstanding, Label: "文档理解", Enabled: true}},
	}
	status := renderTUIChatFilesStatus(snapshot)
	for _, expected := range []string{"文件输入: 当前未附带文件", "上传上限: 100 MB", ".pdf", ".docx", "文档理解"} {
		if !strings.Contains(status, expected) {
			t.Fatalf("expected files status to contain %q, got %q", expected, status)
		}
	}
}

func TestExecuteTUIUploadCommandUploadsFileAndUpdatesSnapshot(t *testing.T) {
	tempDir := t.TempDir()
	localFile := filepath.Join(tempDir, "report.txt")
	if err := os.WriteFile(localFile, []byte("report"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	var gotFlowID string
	var gotSessionID string
	var gotLocalPath string
	result, err := executeTUIUploadCommand(context.Background(), &stubProvider{uploadHook: func(_ context.Context, flowID string, sessionID string, localPath string, remotePath string) (*provider.UploadResult, error) {
		gotFlowID = flowID
		gotSessionID = sessionID
		gotLocalPath = localPath
		if remotePath != "" {
			t.Fatalf("expected empty remotePath, got %q", remotePath)
		}
		return &provider.UploadResult{FilePath: "flow-1/session-1/report.txt"}, nil
	}}, "/upload report.txt", internaltui.Snapshot{
		BackendAvailable: true,
		DefaultFlow:      "flow-1",
		Workdir:          tempDir,
		ContextDir:       tempDir,
		SessionID:        "session-1",
	})
	if err != nil {
		t.Fatalf("executeTUIUploadCommand failed: %v", err)
	}
	if gotFlowID != "flow-1" || gotSessionID != "session-1" {
		t.Fatalf("unexpected upload identifiers: flow=%q session=%q", gotFlowID, gotSessionID)
	}
	if gotLocalPath != localFile {
		t.Fatalf("expected absolute local path %q, got %q", localFile, gotLocalPath)
	}
	if result.Snapshot == nil || len(result.Snapshot.InputFiles) != 1 || result.Snapshot.InputFiles[0] != "flow-1/session-1/report.txt" {
		t.Fatalf("unexpected snapshot after upload: %#v", result.Snapshot)
	}
	if len(result.Events) != 1 || !strings.Contains(result.Events[0].Text, "已上传文件并附带到当前对话") {
		t.Fatalf("unexpected upload events: %#v", result.Events)
	}
}

func TestExecuteTUIUploadCommandCreatesSessionWhenMissing(t *testing.T) {
	tempDir := t.TempDir()
	localFile := filepath.Join(tempDir, "report.txt")
	if err := os.WriteFile(localFile, []byte("report"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	var gotSessionID string
	result, err := executeTUIUploadCommand(context.Background(), &stubProvider{uploadHook: func(_ context.Context, flowID string, sessionID string, localPath string, remotePath string) (*provider.UploadResult, error) {
		gotSessionID = sessionID
		if flowID != "flow-1" {
			t.Fatalf("unexpected flow id: %q", flowID)
		}
		if localPath != localFile {
			t.Fatalf("expected absolute local path %q, got %q", localFile, localPath)
		}
		if remotePath != "" {
			t.Fatalf("expected empty remotePath, got %q", remotePath)
		}
		return &provider.UploadResult{FilePath: "flow-1/session-created/report.txt"}, nil
	}}, "/upload report.txt", internaltui.Snapshot{
		BackendAvailable: true,
		DefaultFlow:      "flow-1",
		Workdir:          tempDir,
		ContextDir:       tempDir,
	})
	if err != nil {
		t.Fatalf("executeTUIUploadCommand failed: %v", err)
	}
	if gotSessionID != "session-created" {
		t.Fatalf("expected generated session_id, got %q", gotSessionID)
	}
	if result.Snapshot == nil || result.Snapshot.SessionID != "session-created" {
		t.Fatalf("expected snapshot session_id to be updated, got %#v", result.Snapshot)
	}
	if len(result.Snapshot.InputFiles) != 1 || result.Snapshot.InputFiles[0] != "flow-1/session-created/report.txt" {
		t.Fatalf("unexpected snapshot after upload: %#v", result.Snapshot)
	}
}

func TestBuildSearchResultsSummaryUsesCitationBuckets(t *testing.T) {
	summary := buildSearchResultsSummary("数据库 迁移", []provider.SearchResult{
		{Source: "internet", Title: "联网结果", Link: "https://example.com"},
		{Source: "document", Title: "共享文档结果"},
		{Source: "personal", Title: "个人知识结果"},
	})

	if !strings.Contains(summary, "关键词: 数据库 迁移") {
		t.Fatalf("expected keyword in summary, got %q", summary)
	}
	if !strings.Contains(summary, "1.1 联网结果 - https://example.com") {
		t.Fatalf("expected internet citation bucket, got %q", summary)
	}
	if !strings.Contains(summary, "2.1 共享文档结果") {
		t.Fatalf("expected shared citation bucket, got %q", summary)
	}
	if !strings.Contains(summary, "3.1 个人知识结果") {
		t.Fatalf("expected personal citation bucket, got %q", summary)
	}
}

func TestHumanAIDialogueRequiresServiceAndMetadataSupport(t *testing.T) {
	snapshot := internaltui.Snapshot{}
	applyServiceConfigToTUISnapshot(&snapshot, &provider.ServiceConfig{FeatureFlags: provider.ServiceFeatureFlags{HumanAIDialogue: true}})
	if snapshot.HumanAIDialogueSupported {
		t.Fatalf("expected service flag alone not to enable human AI dialogue, got %#v", snapshot)
	}

	snapshot = internaltui.Snapshot{}
	applyMetadataToTUISnapshot(&snapshot, &provider.Metadata{Capabilities: provider.ChatCapabilities{SupportHumanAIDialogue: true}})
	if snapshot.HumanAIDialogueSupported {
		t.Fatalf("expected metadata feature alone not to enable human AI dialogue, got %#v", snapshot)
	}

	applyServiceConfigToTUISnapshot(&snapshot, &provider.ServiceConfig{FeatureFlags: provider.ServiceFeatureFlags{HumanAIDialogue: true}})
	if !snapshot.HumanAIDialogueSupported {
		t.Fatalf("expected service flag and metadata feature to enable human AI dialogue, got %#v", snapshot)
	}
}

func TestExecuteTUIKnowledgeCommandSwitchesToAuto(t *testing.T) {
	snapshot := internaltui.Snapshot{
		KnowledgeSearchSupported: true,
		KnowledgeBases:           []internaltui.KnowledgeBaseItem{{Code: "kb-a", Name: "知识库A", Selected: true}},
		SelectedKnowledgeBases:   []string{"kb-a"},
	}
	result, err := executeTUIKnowledgeCommand("/knowledge auto", snapshot)
	if err != nil {
		t.Fatalf("executeTUIKnowledgeCommand failed: %v", err)
	}
	if result.Snapshot == nil || len(result.Snapshot.SelectedKnowledgeBases) != 0 {
		t.Fatalf("expected knowledge selection switched to auto, got %#v", result.Snapshot)
	}
	if result.Snapshot.KnowledgeBases[0].Selected {
		t.Fatalf("expected picker selection flag cleared, got %#v", result.Snapshot.KnowledgeBases)
	}
	if len(result.Events) != 1 || !strings.Contains(result.Events[0].Text, "auto（自动）") {
		t.Fatalf("expected auto status note, got %#v", result.Events)
	}
}

func TestExecuteTUIKnowledgeCommandOpensPickerForRootCommand(t *testing.T) {
	snapshot := internaltui.Snapshot{
		KnowledgeSearchSupported: true,
		KnowledgeBases:           []internaltui.KnowledgeBaseItem{{Code: "kb-a", Name: "知识库A"}},
	}
	result, err := executeTUIKnowledgeCommand("/knowledge", snapshot)
	if err != nil {
		t.Fatalf("executeTUIKnowledgeCommand failed: %v", err)
	}
	if !result.Handled || result.KnowledgePicker == nil {
		t.Fatalf("expected knowledge picker result, got %#v", result)
	}
	if result.KnowledgePicker.Title != "知识库范围(knowledge)" {
		t.Fatalf("unexpected picker title: %#v", result.KnowledgePicker)
	}
	if len(result.KnowledgePicker.Items) != 1 || result.KnowledgePicker.Items[0].Code != "kb-a" {
		t.Fatalf("unexpected picker items: %#v", result.KnowledgePicker.Items)
	}
	if len(result.Events) != 0 {
		t.Fatalf("expected no extra events when opening picker, got %#v", result.Events)
	}
}

func TestExecuteTUIInternetCommandTogglesState(t *testing.T) {
	snapshot := internaltui.Snapshot{InternetSearchSupported: true}
	result, err := executeTUIInternetCommand("/internet on", snapshot)
	if err != nil {
		t.Fatalf("executeTUIInternetCommand failed: %v", err)
	}
	if result.Snapshot == nil || !result.Snapshot.InternetSearchEnabled {
		t.Fatalf("expected internet search enabled, got %#v", result.Snapshot)
	}
}

func TestEmitTUIClientToolResultEventsEmitsNoNoteWithoutPanel(t *testing.T) {
	events := []internaltui.Event{}
	emitTUIClientToolResultEvents(func(event internaltui.Event) {
		events = append(events, event)
	}, internalconfig.Default(), "lsp_query", map[string]any{
		"operation": "find_references",
		"items": []any{
			map[string]any{"name": "symbol", "path": "internal/tui/app.go"},
		},
	}, nil)
	if len(events) != 0 {
		t.Fatalf("expected no events without panel, got %#v", events)
	}
}

func TestEmitTUIClientToolResultEventsUpdatesTodoWithoutPanel(t *testing.T) {
	events := []internaltui.Event{}
	emitTUIClientToolResultEvents(func(event internaltui.Event) {
		events = append(events, event)
	}, internalconfig.Default(), "todo_write", map[string]any{
		"items": []any{
			map[string]any{"title": "实现右侧待办栏", "status": "in_progress", "notes": "放到历史区右边"},
			map[string]any{"title": "补齐测试", "status": "completed"},
		},
	}, nil)
	if len(events) != 2 {
		t.Fatalf("expected todo update event and note, got %#v", events)
	}
	if events[0].Kind != internaltui.EventTodoUpdate {
		t.Fatalf("expected todo update event first, got %#v", events)
	}
	if len(events[0].TodoItems) != 2 || events[0].TodoItems[0].Title != "实现右侧待办栏" {
		t.Fatalf("expected todo items to map into event, got %#v", events[0].TodoItems)
	}
	if events[1].Kind != internaltui.EventSystemNote || !strings.Contains(events[1].Text, "未完成 1 项") {
		t.Fatalf("expected todo update summary note without panel, got %#v", events)
	}
}

func TestBuildTUICompressionProgressTextReturnsPayloadText(t *testing.T) {
	text := buildTUICompressionProgressText(map[string]any{
		"text":          "上下文压缩完成：320 -> 160",
		"before_tokens": 320,
		"after_tokens":  160,
	})
	if text != "上下文压缩完成：0.3K -> 0.2K" {
		t.Fatalf("unexpected compression progress text: %q", text)
	}
}

func TestBuildTUICompressionProgressTextSkipsZeroTokenNoop(t *testing.T) {
	text := buildTUICompressionProgressText(map[string]any{
		"text":          "上下文压缩完成：0K -> 0K",
		"before_tokens": 0,
		"after_tokens":  0,
	})
	if text != "" {
		t.Fatalf("expected zero-token compression progress hidden, got %q", text)
	}
}

func TestBuildTUICompressionMetricsAcceptsCompressionEndPayload(t *testing.T) {
	metrics := buildTUICompressionMetrics(map[string]any{
		"count":             1,
		"before_tokens":     9200,
		"after_tokens":      6000,
		"remaining_tokens":  2192,
		"compression_ratio": 0.65,
		"target_type":       "history_messages",
		"context_window": map[string]any{
			"max_input_tokens":            10000,
			"current_round_input_tokens":  6400,
			"next_round_available_tokens": 3600,
		},
	})
	if metrics == nil || metrics.Compression == nil {
		t.Fatalf("expected compression metrics, got %#v", metrics)
	}
	if metrics.Compression.AfterTokens != 6000 || metrics.Compression.TargetType != "history_messages" {
		t.Fatalf("unexpected compression metrics: %#v", metrics.Compression)
	}
	if metrics.TokenUsage == nil || metrics.TokenUsage.ContextWindow == nil {
		t.Fatalf("expected context window metrics, got %#v", metrics)
	}
	if metrics.TokenUsage.ContextWindow.CurrentRoundInputTokens != 6400 {
		t.Fatalf("unexpected context window metrics: %#v", metrics.TokenUsage.ContextWindow)
	}
}

func TestBuildTUICompressionProgressTextAppendsContextUsageSummary(t *testing.T) {
	text := buildTUICompressionProgressText(map[string]any{
		"text":          "上下文压缩完成：320 -> 160",
		"before_tokens": 320,
		"after_tokens":  160,
		"context_window": map[string]any{
			"max_input_tokens":            10000,
			"current_round_input_tokens":  6400,
			"next_round_available_tokens": 3600,
		},
	})
	if text != "上下文压缩完成：0.3K -> 0.2K（当前上下文 6.4K/10K，可用 3.6K）" {
		t.Fatalf("unexpected compression progress text with context summary: %q", text)
	}
}

func TestEmitTUIClientToolResultEventsSkipsPanelForSkill(t *testing.T) {
	events := []internaltui.Event{}
	emitTUIClientToolResultEvents(func(event internaltui.Event) {
		events = append(events, event)
	}, internalconfig.Default(), "skill", map[string]any{
		"name":   "Context Review",
		"prompt": "技能主体内容",
	}, nil)
	if len(events) != 1 {
		t.Fatalf("expected only one note for skill load, got %#v", events)
	}
	if events[0].Kind != internaltui.EventSystemNote || !strings.Contains(events[0].Text, "技能已装载：Context Review") {
		t.Fatalf("expected skill load note without panel, got %#v", events)
	}
}

func TestEmitTUIClientToolResultEventsSkipsSuccessfulFileEditPanelInDirectMode(t *testing.T) {
	config := internalconfig.Default()
	config.WriteConfirmationPolicy = internalconfig.WriteConfirmationPolicyDirect
	events := []internaltui.Event{}
	emitTUIClientToolResultEvents(func(event internaltui.Event) {
		events = append(events, event)
	}, config, "file_edit", map[string]any{
		"path":           "notes.txt",
		"replacements":   1,
		"preview_before": "before",
		"preview_after":  "after",
		"sha256_before":  "old-hash",
		"sha256_after":   "new-hash",
	}, nil)
	if len(events) != 0 {
		t.Fatalf("expected no events for direct file_edit success, got %#v", events)
	}
}

func TestEmitTUIClientToolResultEventsSkipsSuccessfulComputerPanel(t *testing.T) {
	events := []internaltui.Event{}
	emitTUIClientToolResultEvents(func(event internaltui.Event) {
		events = append(events, event)
	}, internalconfig.Default(), "taiyiflow-computer__focus_window", map[string]any{
		"window_id":    "win-1",
		"process_name": "Finder",
	}, nil)
	if len(events) != 0 {
		t.Fatalf("expected no events for successful computer tool, got %#v", events)
	}
}

func TestEmitTUIClientToolResultEventsSkipsTaskInfoPanel(t *testing.T) {
	events := []internaltui.Event{}
	emitTUIClientToolResultEvents(func(event internaltui.Event) {
		events = append(events, event)
	}, internalconfig.Default(), "task_output", map[string]any{
		"task_id": "task-1",
		"status":  "running",
		"stdout":  "hello",
	}, nil)
	if len(events) != 0 {
		t.Fatalf("expected no events for task output, got %#v", events)
	}
}

func TestEmitTUIClientToolResultEventsSkipsFileEditFailurePanel(t *testing.T) {
	config := internalconfig.Default()
	config.WriteConfirmationPolicy = internalconfig.WriteConfirmationPolicyDirect
	events := []internaltui.Event{}
	emitTUIClientToolResultEvents(func(event internaltui.Event) {
		events = append(events, event)
	}, config, "file_edit", map[string]any{
		"path":          "notes.txt",
		"status":        "patch_conflict",
		"sha256_before": "old-hash",
	}, errors.New("PATCH_CONFLICT"))
	// 工具错误不再发射 EventErrorNote 到消息区，
	// 错误已通过回调提交给服务端由模型提炼后输出。
	if len(events) != 0 {
		t.Fatalf("expected no events for file_edit failure (error suppressed), got %#v", events)
	}
}

func TestApplyConfigFormSubmissionIncludesPermissionFields(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app := &App{configManager: manager}
	if err := manager.Save(internalconfig.Default()); err != nil {
		t.Fatalf("save default config failed: %v", err)
	}

	updated, err := app.applyConfigFormSubmission(internaltui.ConfigFormSubmission{
		Values: internaltui.ConfigFormValues{
			BackendBaseURL:            "http://localhost:7860",
			ApprovalMode:              "auto",
			SandboxEscapePolicy:       "allow",
			SkillReviewPolicy:         "ignore",
			WriteConfirmationPolicy:   "direct",
			SandboxRoots:              "/tmp/a, /tmp/b",
			PermissionRules:           "tool:shell_exec=deny; shell:git status=allow",
			ReasoningEnabled:          true,
			Workdir:                   "/workspace/demo",
			HTTPTimeoutSeconds:        "75",
			CommandTimeoutSeconds:     "45",
			StreamIdleTimeoutSeconds:  "300",
			CrossSessionMemoryEnabled: true,
			QRWaitTimeoutSeconds:      "90",
			AgentAutoLoopMaxRounds:    "0",
			OutputLanguage:            "zh-CN",
			ComputerEnabled:           false,
			ComputerPermissionMode:    internalconfig.ComputerPermissionModeFollowPlan,
			SkillsInstallScope:        "workspace",
			SkillsAutoInstallPolicy:   "allow",
			SkillsFreeTextSearch:      false,
			SkillsRegistryURL:         "https://mirror-cn.clawhub.com",
		},
	})
	if err != nil {
		t.Fatalf("applyConfigFormSubmission failed: %v", err)
	}
	if updated.SandboxEscapePolicy != "allow" {
		t.Fatalf("expected allow sandbox policy, got %#v", updated)
	}
	if updated.SkillReviewPolicy != "ignore" {
		t.Fatalf("expected skill review policy saved, got %#v", updated.SkillReviewPolicy)
	}
	if updated.WriteConfirmationPolicy != "direct" {
		t.Fatalf("expected write confirmation policy saved, got %#v", updated.WriteConfirmationPolicy)
	}
	if len(updated.SandboxRoots) != 2 || updated.SandboxRoots[0] != "/tmp/a" || updated.SandboxRoots[1] != "/tmp/b" {
		t.Fatalf("unexpected sandbox roots: %#v", updated.SandboxRoots)
	}
	if len(updated.PermissionRules) != 2 {
		t.Fatalf("expected 2 permission rules, got %#v", updated.PermissionRules)
	}
	if updated.PermissionRules[0].ToolName != "shell_exec" || updated.PermissionRules[0].Action != "deny" {
		t.Fatalf("unexpected first permission rule: %#v", updated.PermissionRules[0])
	}
	if updated.PermissionRules[1].CommandPrefix != "git status" || updated.PermissionRules[1].Action != "allow" {
		t.Fatalf("unexpected second permission rule: %#v", updated.PermissionRules[1])
	}
	if updated.Skills.InstallScope != "workspace" {
		t.Fatalf("expected skills install scope saved, got %#v", updated.Skills)
	}
	if updated.Skills.AutoInstallPolicy != internalconfig.SkillAutoInstallPolicyAllow {
		t.Fatalf("expected skills auto install policy saved, got %#v", updated.Skills)
	}
	if updated.Skills.FreeTextSearchEnabled {
		t.Fatalf("expected free text search flag saved, got %#v", updated.Skills)
	}
	if updated.Skills.Directory != "" {
		t.Fatalf("expected legacy skills directory cleared, got %#v", updated.Skills)
	}
	if updated.CommandTimeoutSeconds != 45 {
		t.Fatalf("expected command timeout saved, got %#v", updated.CommandTimeoutSeconds)
	}
	if updated.HTTPTimeoutSeconds != 75 {
		t.Fatalf("expected http timeout saved, got %#v", updated.HTTPTimeoutSeconds)
	}
	if updated.QRWaitTimeoutSeconds != 90 {
		t.Fatalf("expected qr wait timeout saved, got %#v", updated.QRWaitTimeoutSeconds)
	}
	if !updated.CrossSessionMemoryEnabled {
		t.Fatalf("expected cross session memory saved, got %#v", updated.CrossSessionMemoryEnabled)
	}
	if internalconfig.ComputerEnabled(updated.Computer) {
		t.Fatalf("expected desktop automation to be disabled, got %#v", updated.Computer)
	}
	if updated.Computer.PermissionMode != internalconfig.ComputerPermissionModeFollowPlan {
		t.Fatalf("expected desktop permission mode saved, got %#v", updated.Computer)
	}
	if updated.AgentAutoLoopMaxRounds != 0 {
		t.Fatalf("expected agent auto loop max rounds saved as 0, got %#v", updated.AgentAutoLoopMaxRounds)
	}

	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load updated config failed: %v", err)
	}
	if len(reloaded.PermissionRules) != 2 || len(reloaded.SandboxRoots) != 2 {
		t.Fatalf("expected persisted permission fields, got %#v", reloaded)
	}
	if reloaded.Skills.InstallScope != "workspace" {
		t.Fatalf("expected persisted skills install scope, got %#v", reloaded.Skills)
	}
	if reloaded.CommandTimeoutSeconds != 45 {
		t.Fatalf("expected persisted command timeout, got %#v", reloaded.CommandTimeoutSeconds)
	}
	if reloaded.QRWaitTimeoutSeconds != 90 {
		t.Fatalf("expected persisted qr wait timeout, got %#v", reloaded.QRWaitTimeoutSeconds)
	}
	if !reloaded.CrossSessionMemoryEnabled {
		t.Fatalf("expected persisted cross session memory, got %#v", reloaded.CrossSessionMemoryEnabled)
	}
	if internalconfig.ComputerEnabled(reloaded.Computer) {
		t.Fatalf("expected persisted desktop automation disabled state, got %#v", reloaded.Computer)
	}
	if reloaded.Computer.PermissionMode != internalconfig.ComputerPermissionModeFollowPlan {
		t.Fatalf("expected persisted desktop permission mode, got %#v", reloaded.Computer)
	}
	if reloaded.AgentAutoLoopMaxRounds != 0 {
		t.Fatalf("expected persisted auto loop max rounds 0, got %#v", reloaded.AgentAutoLoopMaxRounds)
	}
	if got := formatPermissionRulesInline(reloaded.PermissionRules); got != "tool:shell_exec=deny; shell:git status=allow" {
		t.Fatalf("unexpected formatted rules: %q", got)
	}
	if got := formatSandboxRootsInline(reloaded.SandboxRoots); got != "/tmp/a, /tmp/b" {
		t.Fatalf("unexpected formatted roots: %q", got)
	}
}

func TestApplyConfigFormSubmissionIncludesBrowserFields(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app := &App{configManager: manager}
	if err := manager.Save(internalconfig.Default()); err != nil {
		t.Fatalf("save default config failed: %v", err)
	}

	updated, err := app.applyConfigFormSubmission(internaltui.ConfigFormSubmission{
		Values: internaltui.ConfigFormValues{
			BackendBaseURL:            "http://localhost:7860",
			ApprovalMode:              "auto",
			SkillsInstallScope:        "user",
			SkillsFreeTextSearch:      false,
			BrowserMode:               "isolated",
			BrowserPermissionMode:     "skip_all_permission_checks",
			BrowserChannel:            "edge",
			BrowserExecutablePath:     "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			BrowserProfileDir:         "/tmp/taiyiflow-browser-profile",
			BrowserConnectTimeout:     "30",
			BrowserExtensionSetup:     "manual",
			BrowserSocketDir:          "/tmp/taiyiflow-browser-socket",
			BrowserRemoteDebuggingURL: "http://127.0.0.1:9333",
			BrowserHeadless:           "true",
		},
	})
	if err != nil {
		t.Fatalf("applyConfigFormSubmission failed: %v", err)
	}
	if updated.Browser.Mode != internalconfig.BrowserModeIsolated {
		t.Fatalf("expected isolated browser mode, got %#v", updated.Browser)
	}
	if updated.Browser.PermissionMode != internalconfig.BrowserPermissionModeSkipAll {
		t.Fatalf("expected skip_all browser permission mode, got %#v", updated.Browser)
	}
	if updated.Browser.Channel != internalconfig.BrowserChannelEdge {
		t.Fatalf("expected edge browser channel, got %#v", updated.Browser)
	}
	if updated.Browser.ExecutablePath != "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge" {
		t.Fatalf("unexpected browser executable path: %#v", updated.Browser)
	}
	if updated.Browser.ProfileDir != "/tmp/taiyiflow-browser-profile" {
		t.Fatalf("unexpected browser profile dir: %#v", updated.Browser)
	}
	if updated.Browser.ConnectTimeoutSeconds != 30 {
		t.Fatalf("expected browser connect timeout 30, got %#v", updated.Browser)
	}
	if updated.Browser.ExtensionSetup != internalconfig.BrowserExtensionSetupManual {
		t.Fatalf("expected browser extension setup manual, got %#v", updated.Browser)
	}
	if updated.Browser.SocketDir != "/tmp/taiyiflow-browser-socket" {
		t.Fatalf("unexpected browser socket dir: %#v", updated.Browser)
	}
	if updated.Browser.RemoteDebuggingURL != "http://127.0.0.1:9333" {
		t.Fatalf("unexpected browser remote debugging url: %#v", updated.Browser)
	}
	if !updated.Browser.Headless {
		t.Fatalf("expected browser headless true, got %#v", updated.Browser)
	}
}

func TestBuildTUIClientToolConfirmRequestUsesSkillAndWritePolicies(t *testing.T) {
	workspaceDir := t.TempDir()
	skillDir := filepath.Join(workspaceDir, ".snz1dp", "skills", "context-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: Context Review\ndescription: 审查工作区技能\n---\n\n检查工作区上下文。\n"), 0o644); err != nil {
		t.Fatalf("write skill file failed: %v", err)
	}

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	skillRequest := buildTUIClientToolConfirmRequest(config, workspaceDir, map[string]any{
		"function": map[string]any{
			"name":      "skill",
			"arguments": map[string]any{"name": "Context Review"},
		},
	}, func(context.Context) error { return nil }, func(context.Context) error { return nil }, nil, nil, nil, nil)
	if skillRequest.Title != "技能包审查(skill package review)" {
		t.Fatalf("unexpected skill request title: %#v", skillRequest)
	}
	if skillRequest.ApproveLabel != "本次允许" || skillRequest.RejectLabel != "本次拒绝" || skillRequest.AlwaysApproveLabel != "始终允许此技能" || skillRequest.AllowAllLabel != "允许全部技能" {
		t.Fatalf("unexpected skill request labels: %#v", skillRequest)
	}
	if skillRequest.Expanded {
		t.Fatalf("expected skill request to keep normal overlay size, got %#v", skillRequest)
	}
	if !strings.Contains(skillRequest.PreviewBody, "相对目录: context-review") {
		t.Fatalf("expected skill preview to include relative dir, got %#v", skillRequest.PreviewBody)
	}

	writeRequest := buildTUIClientToolConfirmRequest(config, workspaceDir, map[string]any{
		"function": map[string]any{
			"name":      "write_file",
			"arguments": map[string]any{"path": "notes.txt", "mode": "overwrite", "content": "hello world"},
		},
	}, nil, nil, nil, nil, nil, nil)
	if writeRequest.Title != "写入比较摘要(write summary)待确认" {
		t.Fatalf("unexpected write request title: %#v", writeRequest)
	}
	if writeRequest.ApproveLabel != "确认本次写入" || writeRequest.RejectLabel != "取消本次写入" {
		t.Fatalf("unexpected write request labels: %#v", writeRequest)
	}
	if !writeRequest.DisableDefaultAlwaysApprove {
		t.Fatalf("expected write request to disable always approve, got %#v", writeRequest)
	}
	if !writeRequest.Expanded {
		t.Fatalf("expected write request to use expanded review overlay, got %#v", writeRequest)
	}
	if !strings.Contains(writeRequest.PreviewBody, "--- notes.txt") || !strings.Contains(writeRequest.PreviewBody, "+++ notes.txt") || !strings.Contains(writeRequest.PreviewBody, "+hello world") {
		t.Fatalf("expected write preview to render unified diff, got %#v", writeRequest.PreviewBody)
	}
	if strings.Contains(writeRequest.PreviewBody, "内容摘要") {
		t.Fatalf("expected write preview to stop using summary format, got %#v", writeRequest.PreviewBody)
	}

	editRequest := buildTUIClientToolConfirmRequest(config, workspaceDir, map[string]any{
		"function": map[string]any{
			"name": "file_edit",
			"arguments": map[string]any{
				"path":                   "notes.txt",
				"old_string":             "before line",
				"new_string":             "after line",
				"expected_sha256_before": "abc123",
			},
		},
	}, nil, nil, nil, nil, nil, nil)
	if !strings.Contains(editRequest.PreviewBody, "@@ expected_sha256_before=abc123 @@") || !strings.Contains(editRequest.PreviewBody, "-before line") || !strings.Contains(editRequest.PreviewBody, "+after line") {
		t.Fatalf("expected file_edit preview to render diff body, got %#v", editRequest.PreviewBody)
	}
	if !editRequest.Expanded {
		t.Fatalf("expected file_edit request to use expanded review overlay, got %#v", editRequest)
	}
}

func TestBuildTUIClientToolConfirmRequestUsesBrowserAndComputerAlwaysApprovePolicies(t *testing.T) {
	config := internalconfig.Default()
	browserRequest := buildTUIClientToolConfirmRequest(config, t.TempDir(), map[string]any{
		"function": map[string]any{
			"name":      "taiyiflow-in-chrome__click",
			"arguments": map[string]any{"selector": "button.submit"},
		},
	}, nil, nil, func(context.Context) error { return nil }, nil, nil, nil)
	if browserRequest.Title != "浏览器操作(browser automation)待确认" {
		t.Fatalf("unexpected browser request title: %#v", browserRequest)
	}
	if browserRequest.AlwaysApproveLabel != "始终允许浏览器操作" || !browserRequest.DisableDefaultAlwaysApprove {
		t.Fatalf("unexpected browser request policy labels: %#v", browserRequest)
	}
	if !strings.Contains(browserRequest.Hint, "A 始终允许浏览器操作") {
		t.Fatalf("expected browser request hint to mention always approve, got %#v", browserRequest)
	}

	computerRequest := buildTUIClientToolConfirmRequest(config, t.TempDir(), map[string]any{
		"function": map[string]any{
			"name":      "taiyiflow-computer__focus_window",
			"arguments": map[string]any{"window_id": "win-1"},
		},
	}, nil, nil, nil, func(context.Context) error { return nil }, nil, nil)
	if computerRequest.Title != "桌面操作(desktop automation)待确认" {
		t.Fatalf("unexpected computer request title: %#v", computerRequest)
	}
	if computerRequest.AlwaysApproveLabel != "始终允许桌面操作" || !computerRequest.DisableDefaultAlwaysApprove {
		t.Fatalf("unexpected computer request policy labels: %#v", computerRequest)
	}
	if !strings.Contains(computerRequest.Hint, "A 始终允许桌面操作") {
		t.Fatalf("expected computer request hint to mention always approve, got %#v", computerRequest)
	}
}

func TestBuildTUIHelpLinesIncludesAboutEntry(t *testing.T) {
	lines := buildTUIHelpLines(internaltui.Snapshot{BackendAvailable: true})
	if !strings.Contains(strings.Join(lines, "\n"), "/about 显示当前客户端版本") {
		t.Fatalf("expected /about in help lines, got %q", strings.Join(lines, "\n"))
	}
}

func TestApplyEffectiveSkillReviewForPayloadUsesApprovedSkillNames(t *testing.T) {
	workspaceDir := t.TempDir()
	skillDir := filepath.Join(workspaceDir, ".snz1dp", "skills", "context-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: Context Review\ndescription: 审查工作区技能\n---\n\n检查工作区上下文。\n"), 0o644); err != nil {
		t.Fatalf("write skill file failed: %v", err)
	}

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.SkillReviewPolicy = internalconfig.SkillReviewPolicyConfirm
	updated := applyEffectiveSkillReviewForPayload(config, workspaceDir, agent.SessionState{
		ApprovedSkillNames: []string{"Context Review"},
	}, map[string]any{
		"function": map[string]any{
			"name":      "skill",
			"arguments": map[string]any{"name": "context-review"},
		},
	})
	if updated.SkillReviewPolicy != internalconfig.SkillReviewPolicyIgnore {
		t.Fatalf("expected approved skill to skip confirmation, got %#v", updated.SkillReviewPolicy)
	}
}

func TestParsePermissionRulesInlineRejectsInvalidFormat(t *testing.T) {
	_, err := parsePermissionRulesInline("shell_exec=deny")
	if err == nil {
		t.Fatal("expected invalid permission rule format to fail")
	}
}

func assertError(message string) error {
	return simpleError(message)
}

type simpleError string

func (e simpleError) Error() string {
	return string(e)
}

func TestShouldSuppressTUILogLine(t *testing.T) {
	tests := []struct {
		line     string
		expected bool
	}{
		{"[本地工具] read_file foo.go 行 1-10", true},
		{"[本地命令] ls -la（成功，0.1s，退出码 0）", true},
		{"[执行客户端命令] ls -la", true},
		{"[浏览器工具] tabs_context", true},
		{"[桌面工具] displays_context", true},
		{"[客户端命令]\nls\n是否执行？", true},
		{"[客户端工具]\nread_file\n是否执行？", true},
		{"[路径越界确认]", true},
		{"[ask_user] 请输入以下信息", true},
		{"", true},
		{"普通文本行", false},
		{"some random output", false},
	}
	for _, tt := range tests {
		got := shouldSuppressTUILogLine(tt.line)
		if got != tt.expected {
			t.Errorf("shouldSuppressTUILogLine(%q) = %v, want %v", tt.line, got, tt.expected)
		}
	}
}

func TestParseTUIToolSummary(t *testing.T) {
	tests := []struct {
		line        string
		summary     string
		ok          bool
	}{
		{"[本地工具] read_file foo.go 行 1-10", "read_file foo.go 行 1-10", true},
		{"[本地命令] ls -la（成功，0.1s，退出码 0）", "ls -la（成功，0.1s，退出码 0）", true},
		{"[浏览器工具] tabs_context", "tabs_context", true},
		{"[桌面工具] displays_context", "displays_context", true},
		{"[执行客户端命令] ls -la", "", false},
		{"普通文本", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		summary, ok := parseTUIToolSummary(tt.line)
		if ok != tt.ok || (ok && summary != tt.summary) {
			t.Errorf("parseTUIToolSummary(%q) = (%q, %v), want (%q, %v)", tt.line, summary, ok, tt.summary, tt.ok)
		}
	}
}

func TestTuiLogWriterSuppressesAllToolPrefixes(t *testing.T) {
	var emitted []internaltui.Event
	emit := func(e internaltui.Event) { emitted = append(emitted, e) }
	w := &tuiLogWriter{emit: emit}

	// 摘要前缀行（单行，有换行符）
	w.Write([]byte("[本地工具] read_file foo.go 行 1-10\n"))
	w.Write([]byte("[本地命令] ls -la（成功，0.1s，退出码 0）\n"))
	// 单行抑制前缀（不触发 suppressMode）
	w.Write([]byte("[执行客户端命令] ls -la\n"))
	// 摘要前缀行
	w.Write([]byte("[浏览器工具] tabs_context\n"))
	w.Write([]byte("[桌面工具] displays_context\n"))
	// 多行确认提示（模拟实际输出，含终止符换行）
	w.Write([]byte("\n[客户端命令]\nls -la\n是否执行？[y/N]: \n"))
	w.Write([]byte("\n[客户端工具]\nfile_edit test.go\n是否执行？[y/N]: \n"))
	w.Write([]byte("\n[路径越界确认]\n目标路径越界: /etc\n是否允许本次访问？[y/N]: \n"))
	w.Write([]byte("\n[ask_user] 请输入以下信息\n1. 名字\n请输入答案: \n"))
	// 正常文本
	w.Write([]byte("普通文本行\n"))
	w.Flush()

	// 4 个摘要：[本地工具]、[本地命令]、[浏览器工具]、[桌面工具]
	// 普通文本行不再作为 EventSystemNote 发射（命令原始输出不在消息区展示）
	var summaryCount, noteCount int
	for _, e := range emitted {
		switch e.Kind {
		case internaltui.EventToolSummary:
			summaryCount++
		case internaltui.EventSystemNote:
			noteCount++
		}
	}
	if summaryCount != 4 {
		t.Errorf("expected 4 EventToolSummary, got %d", summaryCount)
	}
	if noteCount != 0 {
		t.Errorf("expected 0 EventSystemNote, got %d", noteCount)
	}
}

// TestTuiLogWriterSuppressesMultiLineConfirmPrompt 验证多行确认提示的续行被正确抑制。
// 模拟 confirmCommand 的实际输出格式：\n[客户端命令]\n{preview}\n是否执行？[y/N]:
func TestTuiLogWriterSuppressesMultiLineConfirmPrompt(t *testing.T) {
	var emitted []internaltui.Event
	emit := func(e internaltui.Event) { emitted = append(emitted, e) }
	w := &tuiLogWriter{emit: emit}

	// 模拟 confirmCommand 的完整输出（单次 Write）
	w.Write([]byte("\n[客户端命令]\nls -la\n是否执行？[y/N]: "))
	// 模拟用户输入后的后续正常输出
	w.Write([]byte("命令执行完成\n"))
	w.Flush()

	// 确认提示部分不应发出任何事件
	// "命令执行完成" 不再作为 EventSystemNote 发出（命令原始输出不在消息区展示）
	var noteCount int
	for _, e := range emitted {
		if e.Kind == internaltui.EventSystemNote {
			noteCount++
		}
	}
	if noteCount != 0 {
		t.Errorf("expected 0 EventSystemNote (normal output suppressed), got %d", noteCount)
	}
}

// TestTuiLogWriterSuppressesMultiLineClientToolPrompt 验证 [客户端工具] 多行提示抑制
func TestTuiLogWriterSuppressesMultiLineClientToolPrompt(t *testing.T) {
	var emitted []internaltui.Event
	emit := func(e internaltui.Event) { emitted = append(emitted, e) }
	w := &tuiLogWriter{emit: emit}

	w.Write([]byte("\n[客户端工具]\nfile_edit path=test.go\n是否执行？[y/N]: "))
	w.Write([]byte("工具执行完成\n"))
	w.Flush()

	for _, e := range emitted {
		if e.Kind == internaltui.EventSystemNote && (strings.Contains(e.Text, "file_edit") || strings.Contains(e.Text, "是否执行")) {
			t.Errorf("leaked suppress line as EventSystemNote: %q", e.Text)
		}
	}
}

// TestTuiLogWriterSuppressesMultiLineSandboxEscapePrompt 验证 [路径越界确认] 多行提示抑制
func TestTuiLogWriterSuppressesMultiLineSandboxEscapePrompt(t *testing.T) {
	var emitted []internaltui.Event
	emit := func(e internaltui.Event) { emitted = append(emitted, e) }
	w := &tuiLogWriter{emit: emit}

	w.Write([]byte("\n[路径越界确认]\n目标路径位于当前工作目录外: /etc/passwd\n是否允许本次访问？[y/N]: "))
	w.Write([]byte("正常输出\n"))
	w.Flush()

	for _, e := range emitted {
		if e.Kind == internaltui.EventSystemNote && (strings.Contains(e.Text, "目标路径") || strings.Contains(e.Text, "是否允许")) {
			t.Errorf("leaked suppress line as EventSystemNote: %q", e.Text)
		}
	}
}

// TestTuiLogWriterSuppressesMultiLineAskUserPrompt 验证 [ask_user] 多行提示抑制
func TestTuiLogWriterSuppressesMultiLineAskUserPrompt(t *testing.T) {
	var emitted []internaltui.Event
	emit := func(e internaltui.Event) { emitted = append(emitted, e) }
	w := &tuiLogWriter{emit: emit}

	// 模拟 promptAskUserStdio 的输出
	w.Write([]byte("\n[ask_user] 请输入以下信息；输入 /cancel 可取消。\n"))
	w.Write([]byte("1. 请输入你的名字\n"))
	w.Write([]byte("请输入答案: "))
	// 模拟后续正常输出
	w.Write([]byte("处理完成\n"))
	w.Flush()

	for _, e := range emitted {
		if e.Kind == internaltui.EventSystemNote && (strings.Contains(e.Text, "请输入") || strings.Contains(e.Text, "ask_user")) {
			t.Errorf("leaked suppress line as EventSystemNote: %q", e.Text)
		}
	}
}

// TestTuiLogWriterFlushInSuppressMode 验证 Flush 在 suppressMode 下正确丢弃 pending 行
func TestTuiLogWriterFlushInSuppressMode(t *testing.T) {
	var emitted []internaltui.Event
	emit := func(e internaltui.Event) { emitted = append(emitted, e) }
	w := &tuiLogWriter{emit: emit}

	// 写入多行确认提示，最后一行（提示行）没有换行符
	w.Write([]byte("\n[客户端命令]\nls -la\n是否执行？[y/N]: "))
	// 不再写入更多数据，直接 Flush
	w.Flush()

	// suppressMode 下的 pending 行应被丢弃，不应发出任何事件
	if len(emitted) != 0 {
		t.Errorf("expected 0 events when flushing in suppress mode, got %d: %+v", len(emitted), emitted)
	}
	// Flush 后 suppressMode 应被重置
	if w.suppressMode {
		t.Errorf("suppressMode should be reset after Flush")
	}
}

// TestTuiLogWriterPartialLineBuffering 验证分行缓冲（partial line buffering）：
// 不完整的行应被缓冲，直到收到换行符后才处理。
// 非摘要行不再作为 EventSystemNote 发射，只验证不产生事件。
func TestTuiLogWriterPartialLineBuffering(t *testing.T) {
	var emitted []internaltui.Event
	emit := func(e internaltui.Event) { emitted = append(emitted, e) }
	w := &tuiLogWriter{emit: emit}

	// 分多次写入同一行
	w.Write([]byte("部分"))
	w.Write([]byte("文本"))
	w.Write([]byte("行\n"))
	w.Flush()

	// 非摘要行不再发射到消息区
	if len(emitted) != 0 {
		t.Errorf("expected 0 events for non-summary buffered line, got %d: %+v", len(emitted), emitted)
	}
}

// TestTuiLogWriterSuppressModeResetAfterTerminator 验证抑制模式在遇到终止符后正确退出
func TestTuiLogWriterSuppressModeResetAfterTerminator(t *testing.T) {
	var emitted []internaltui.Event
	emit := func(e internaltui.Event) { emitted = append(emitted, e) }
	w := &tuiLogWriter{emit: emit}

	// 写入完整的确认提示（含终止符换行）
	w.Write([]byte("\n[客户端命令]\nls -la\n是否执行？[y/N]: \n"))
	// 后续正常文本不再作为 EventSystemNote 发射
	w.Write([]byte("正常输出\n"))
	w.Flush()

	// 非摘要行不再发射到消息区
	var noteCount int
	for _, e := range emitted {
		if e.Kind == internaltui.EventSystemNote {
			noteCount++
		}
	}
	if noteCount != 0 {
		t.Errorf("expected 0 EventSystemNote after suppress mode reset, got %d", noteCount)
	}
}
