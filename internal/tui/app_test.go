package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestStartStreamErrorDoesNotQueueDoneMessage(t *testing.T) {
	eventCh := make(chan tea.Msg, 4)
	runnerErr := errors.New("backend failed")
	cmd := startStream(context.Background(), Request{}, func(ctx context.Context, request Request, emit func(Event), confirm func(ConfirmRequest) bool) error {
		return runnerErr
	}, eventCh)

	msg := cmd()
	failed, ok := msg.(streamFailedMsg)
	if !ok {
		t.Fatalf("expected streamFailedMsg, got %T", msg)
	}
	if !errors.Is(failed.err, runnerErr) {
		t.Fatalf("expected runner error to propagate, got %v", failed.err)
	}

	select {
	case extra := <-eventCh:
		t.Fatalf("expected no trailing message after stream failure, got %T", extra)
	default:
	}
}

func TestTrimTextKeepsValidUTF8(t *testing.T) {
	trimmed := trimText("执行 访问沙盒外目录命令预览", 9)
	if !strings.HasPrefix(trimmed, "执行 访问沙") {
		t.Fatalf("unexpected trimmed prefix: %q", trimmed)
	}
	if !strings.HasSuffix(trimmed, "...") {
		t.Fatalf("expected ellipsis suffix, got %q", trimmed)
	}
	if strings.Contains(trimmed, "\ufffd") {
		t.Fatalf("expected trimmed text without replacement rune, got %q", trimmed)
	}
}

func TestRenderSessionStatusTextIncludesSandboxPolicy(t *testing.T) {
	text := RenderSessionStatusText(Snapshot{
		RequestMode:                "agent",
		SessionSandboxEscapePolicy: "allow",
		SessionID:                  "session-123",
		SessionTitle:               "测试会话",
	}, "已更新")
	if !strings.Contains(text, "会话外部资源访问策略: allow") {
		t.Fatalf("expected sandbox policy in session status text, got %q", text)
	}
	if !strings.Contains(text, "会话标题: 测试会话") {
		t.Fatalf("expected session title in session status text, got %q", text)
	}
}

func TestVisibleSlashCommandsListsAgentBeforeAgents(t *testing.T) {
	input := textarea.New()
	input.SetValue("/a")

	model := model{
		snapshot: Snapshot{BackendAvailable: true},
		input:    input,
	}

	commands := model.visibleSlashCommands()
	if len(commands) < 2 {
		t.Fatalf("expected slash command matches, got %d", len(commands))
	}
	agentIndex := -1
	agentsIndex := -1
	for index, command := range commands {
		switch command.Trigger {
		case "/agent":
			agentIndex = index
		case "/agents":
			agentsIndex = index
		}
	}
	if agentIndex < 0 || agentsIndex < 0 {
		t.Fatalf("expected both /agent and /agents in slash commands, got %#v", commands)
	}
	if agentIndex >= agentsIndex {
		t.Fatalf("expected /agent before /agents, got index %d and %d", agentIndex, agentsIndex)
	}
}

func TestVisibleSlashCommandsOmitsPermissionsCommand(t *testing.T) {
	input := textarea.New()
	input.SetValue("/p")

	model := model{
		snapshot: Snapshot{BackendAvailable: true},
		input:    input,
	}

	commands := model.visibleSlashCommands()
	for _, command := range commands {
		if command.Trigger == "/permissions" {
			t.Fatalf("expected /permissions to be removed from slash commands, got %#v", commands)
		}
	}
}

func TestFitOverlayContentKeepsFocusedConfigFieldVisible(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title:     "配置设置",
		Hint:      "测试超长弹窗",
		HasAPIKey: true,
		Values: ConfigFormValues{
			BackendBaseURL:           "http://localhost:7860",
			SandboxRoots:             "/tmp/a, /tmp/b",
			PermissionRules:          "tool:shell_exec=deny; shell:git status=allow",
			Workdir:                  "/workspace/demo",
			StreamIdleTimeoutSeconds: "300",
			OutputLanguage:           "zh-CN",
			SkillsRegistryURL:        "https://mirror-cn.clawhub.com",
		},
	})
	state.cursor = configFieldSkillsRegistryURL

	model := model{
		width:        80,
		height:       14,
		contentWidth: 74,
		configForm:   state,
	}

	rendered := sanitizeTerminalText(model.fitOverlayContent(model.renderConfigForm()))
	if !strings.Contains(rendered, "技能仓库地址") {
		t.Fatalf("expected focused field to remain visible, got %q", rendered)
	}
	if !strings.Contains(rendered, "上方还有") {
		t.Fatalf("expected cropped overlay indicator, got %q", rendered)
	}
	if strings.Contains(rendered, overlayFocusMarker) {
		t.Fatalf("expected overlay marker to be stripped, got %q", rendered)
	}
}

func TestRenderTransientStatusShownInAllRequestModes(t *testing.T) {
	model := model{
		snapshot:     Snapshot{RequestMode: "agent"},
		viewport:     viewport.Model{Width: 80},
		activityHint: "执行 读取文件",
	}

	if got := sanitizeTerminalText(model.renderTransientStatus()); !strings.Contains(got, "执行 读取文件") {
		t.Fatalf("expected transient status in agent mode, got %q", got)
	}

	model.snapshot.RequestMode = "ask"
	if got := sanitizeTerminalText(model.renderTransientStatus()); !strings.Contains(got, "执行 读取文件") {
		t.Fatalf("expected transient status in ask mode, got %q", got)
	}

	model.snapshot.RequestMode = "plan"
	if got := sanitizeTerminalText(model.renderTransientStatus()); !strings.Contains(got, "执行 读取文件") {
		t.Fatalf("expected transient status in plan mode, got %q", got)
	}
}

func TestRenderStatusBarShowsModeSegmentForAllRequestModes(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		modeLabel string
	}{
		{name: "agent mode", mode: "agent", modeLabel: "执行"},
		{name: "ask mode", mode: "ask", modeLabel: "问答"},
		{name: "plan mode", mode: "plan", modeLabel: "计划"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			model := model{
				snapshot: Snapshot{
					RequestMode:      testCase.mode,
					ApprovalMode:     "manual",
					BackendAvailable: true,
				},
				state:        newProgressState(progressStreaming),
				width:        80,
				contentWidth: 80,
			}

			got := sanitizeTerminalText(model.renderStatusBar())
			if !strings.Contains(got, "流式响应中") {
				t.Fatalf("expected progress label in %s status bar, got %q", testCase.mode, got)
			}
			if !strings.Contains(got, testCase.modeLabel) {
				t.Fatalf("expected request mode segment %q in status bar, got %q", testCase.modeLabel, got)
			}
		})
	}
}

func TestApplyLocalCommandResultClearHistoryDropsSnapshotHistory(t *testing.T) {
	m := model{
		snapshot: Snapshot{
			BackendAvailable: true,
			WelcomeMessage:   "欢迎回来",
			History: []HistoryEntry{
				{Role: "user", Content: "旧问题"},
			},
		},
		requestStats: RequestMetrics{
			TokenUsage: &TokenUsage{
				ContextWindow: &ContextWindow{CurrentRoundInputTokens: 2048},
			},
		},
		entries: []entry{{role: "user", content: "旧问题"}},
	}

	updatedSnapshot := Snapshot{
		BackendAvailable: true,
		WelcomeMessage:   "欢迎回来",
		History: []HistoryEntry{
			{Role: "user", Content: "旧问题"},
			{Role: "assistant", Content: "旧回答"},
		},
	}

	m.applyLocalCommandResult(LocalCommandResult{
		Handled:        true,
		ClearHistory:   true,
		Snapshot:       &updatedSnapshot,
		RebuildEntries: true,
	})

	if len(m.entries) != 1 {
		t.Fatalf("expected only welcome entry after clear history, got %#v", m.entries)
	}
	if m.entries[0].role != "welcome" || m.entries[0].content != "欢迎回来" {
		t.Fatalf("expected welcome entry after clear history, got %#v", m.entries[0])
	}
	if len(m.snapshot.History) != 0 {
		t.Fatalf("expected snapshot history to be cleared, got %#v", m.snapshot.History)
	}
	if m.requestStats.TokenUsage != nil {
		t.Fatalf("expected request stats to be cleared for new session, got %#v", m.requestStats)
	}
}

func TestApplyLocalCommandResultOpensSkillsPicker(t *testing.T) {
	m := model{}
	result := LocalCommandResult{
		Handled: true,
		SkillsPicker: &SkillsPickerState{
			Title: "技能列表",
			ActiveTab: "local",
			Items: []SkillItem{{Name: "taiyiflowApi", Description: "接口参考"}},
		},
	}

	m.applyLocalCommandResult(result)
	if m.skillsPicker == nil || len(m.skillsPicker.Items) != 1 {
		t.Fatalf("expected skills picker to be applied, got %#v", m.skillsPicker)
	}
	if m.skillsCursor != 0 {
		t.Fatalf("expected skills cursor reset, got %d", m.skillsCursor)
	}
}

func TestHandleSkillsPickerEnterOpensToolPanel(t *testing.T) {
	m := model{
		contentWidth: 80,
		input:        textarea.New(),
		skillsPicker: &SkillsPickerState{
			Title: "技能列表",
			ActiveTab: "local",
			Items: []SkillItem{{
				Name:        "taiyiflowApi",
				Description: "接口参考",
				SourceLabel: "内置技能",
				StateLabel:  "渐进披露",
			}},
		},
	}

	updatedModel, _ := m.handleSkillsPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	updated, ok := updatedModel.(model)
	if !ok {
		t.Fatalf("expected model type, got %T", updatedModel)
	}
	if updated.skillsPicker == nil {
		t.Fatal("expected skills picker to remain open behind tool panel")
	}
	if updated.toolPanel == nil {
		t.Fatal("expected tool panel to open from selected skill")
	}
	if !strings.Contains(updated.toolPanel.Title, "taiyiflowApi") {
		t.Fatalf("unexpected tool panel title: %#v", updated.toolPanel)
	}
}

func TestHandleSkillsPickerTabSwitchesToRemote(t *testing.T) {
	m := model{
		skillsPicker: &SkillsPickerState{
			Title:      "技能列表",
			ActiveTab:  "local",
			Items:      []SkillItem{{Name: "local"}},
			RemoteItems: []RemoteSkillItem{{Slug: "remote-skill", DisplayName: "Remote Skill"}},
		},
	}

	updatedModel, _ := m.handleSkillsPickerKey(tea.KeyMsg{Type: tea.KeyTab})
	updated := updatedModel.(model)
	if updated.skillsPicker == nil || updated.skillsPicker.ActiveTab != "remote" {
		t.Fatalf("expected remote tab after tab key, got %#v", updated.skillsPicker)
	}
}

func TestHandleSkillsPickerEnterOnRemoteOpensRemoteToolPanel(t *testing.T) {
	m := model{
		contentWidth: 80,
		input:        textarea.New(),
		skillsPicker: &SkillsPickerState{
			Title:      "技能列表",
			ActiveTab:  "remote",
			RemoteItems: []RemoteSkillItem{{
				Slug:          "remote-skill",
				DisplayName:   "Remote Skill",
				Summary:       "远程技能摘要",
				Version:       "1.2.3",
				RegistryLabel: "https://registry.example.com",
			}},
		},
	}

	updatedModel, _ := m.handleSkillsPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	updated := updatedModel.(model)
	if updated.toolPanel != nil {
		t.Fatalf("expected remote detail to load asynchronously, got %#v", updated.toolPanel)
	}
	if updated.skillsPicker == nil {
		t.Fatal("expected picker to remain visible while loading detail")
	}
}

func TestHandleToolPanelEscReturnsToSkillsPicker(t *testing.T) {
	m := model{
		contentWidth: 80,
		input:        textarea.New(),
		skillsPicker: &SkillsPickerState{Title: "技能列表", ActiveTab: "local", Items: []SkillItem{{Name: "local"}}},
		toolPanel:    &ToolPanel{Title: "技能详情"},
	}

	updatedModel, _ := m.handleToolPanelKey(tea.KeyMsg{Type: tea.KeyEsc})
	updated := updatedModel.(model)
	if updated.toolPanel != nil {
		t.Fatalf("expected tool panel to close, got %#v", updated.toolPanel)
	}
	if updated.skillsPicker == nil {
		t.Fatal("expected underlying skills picker to remain after esc")
	}
}

func TestHandleSkillsPickerSearchInputUpdatesQueryAndRunsSearch(t *testing.T) {
	captured := ""
	m := model{
		ctx:   context.Background(),
		input: textarea.New(),
		skillsPicker: &SkillsPickerState{
			Title:       "技能列表",
			ActiveTab:   "remote",
			SearchMode:  true,
			RemoteQuery: "rem",
		},
		localCommand: func(ctx context.Context, command string, snapshot Snapshot) (LocalCommandResult, error) {
			captured = command
			return LocalCommandResult{Handled: true, SkillsPicker: &SkillsPickerState{Title: "技能列表", ActiveTab: "remote", RemoteQuery: "remote debug"}}, nil
		},
	}

	updatedModel, _ := m.handleSkillsPickerSearchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ote debug")})
	updated := updatedModel.(model)
	if updated.skillsPicker.RemoteQuery != "remote debug" {
		t.Fatalf("expected remote query to update, got %#v", updated.skillsPicker)
	}
	_, cmd := updated.handleSkillsPickerSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected search command")
	}
	msg := cmd()
	resultMsg, ok := msg.(localCommandExecutedMsg)
	if !ok {
		t.Fatalf("expected localCommandExecutedMsg, got %T", msg)
	}
	if captured != "/skills-picker search remote debug" {
		t.Fatalf("unexpected search command: %q", captured)
	}
	updated.applyLocalCommandResult(resultMsg.result)
	if updated.skillsPicker == nil || updated.skillsPicker.RemoteQuery != "remote debug" {
		t.Fatalf("expected refreshed picker query, got %#v", updated.skillsPicker)
	}
	if updated.skillsPicker.ActiveTab != "remote" {
		t.Fatalf("expected remote tab, got %#v", updated.skillsPicker)
	}
	if updated.skillsPicker.SearchMode {
		t.Fatalf("expected search mode to close after enter, got %#v", updated.skillsPicker)
	}
}

func TestHandleSkillsPickerSearchEscClearsQueryAndRefreshesDefaultRemoteList(t *testing.T) {
	captured := ""
	m := model{
		ctx:   context.Background(),
		input: textarea.New(),
		skillsPicker: &SkillsPickerState{
			Title:       "技能列表",
			ActiveTab:   "remote",
			SearchMode:  true,
			RemoteQuery: "remote debug",
		},
		localCommand: func(ctx context.Context, command string, snapshot Snapshot) (LocalCommandResult, error) {
			captured = command
			return LocalCommandResult{Handled: true, SkillsPicker: &SkillsPickerState{Title: "技能列表", ActiveTab: "remote"}}, nil
		},
	}

	updatedModel, cmd := m.handleSkillsPickerSearchKey(tea.KeyMsg{Type: tea.KeyEsc})
	updated := updatedModel.(model)
	if updated.skillsPicker == nil {
		t.Fatal("expected skills picker to remain open")
	}
	if updated.skillsPicker.SearchMode {
		t.Fatalf("expected search mode to close, got %#v", updated.skillsPicker)
	}
	if updated.skillsPicker.RemoteQuery != "" {
		t.Fatalf("expected query to be cleared, got %#v", updated.skillsPicker)
	}
	if cmd == nil {
		t.Fatal("expected refresh command after clearing query")
	}
	msg := cmd()
	resultMsg, ok := msg.(localCommandExecutedMsg)
	if !ok {
		t.Fatalf("expected localCommandExecutedMsg, got %T", msg)
	}
	if captured != "/skills-picker refresh" {
		t.Fatalf("unexpected refresh command: %q", captured)
	}
	updated.applyLocalCommandResult(resultMsg.result)
	if updated.skillsPicker == nil || updated.skillsPicker.RemoteQuery != "" {
		t.Fatalf("expected default remote list state after refresh, got %#v", updated.skillsPicker)
	}
}

func TestRenderSkillsPickerKeepsLongRemoteLinesWithinWidth(t *testing.T) {
	m := model{
		contentWidth: 40,
		skillsPicker: &SkillsPickerState{
			Title:         "这是一个非常长的技能弹窗标题，用来验证不会把窗口撑开",
			Hint:          "这是一个更长的提示文案，用来验证远程搜索列表在窄窗口下仍然会被裁宽。",
			ActiveTab:     "remote",
			RegistryLabel: "registry.example.com/very/long/path/for/testing",
			RemoteQuery:   "extremely long remote query text",
			RemoteItems: []RemoteSkillItem{{
				Slug:        "remote-skill-with-a-very-long-slug",
				DisplayName: "Remote Skill With A Very Long Display Name",
				Summary:     "这是一个很长的远程技能摘要，用来覆盖多行裁宽逻辑是否稳定。",
				Popularity:  4.8,
				Downloads:   12345,
				Version:     "1.2.3",
				Installed:   true,
				CanUpdate:   true,
			}},
		},
	}

	rendered := sanitizeTerminalText(m.renderSkillsPicker())
	assertRenderedMaxLineWidth(t, rendered, 24)
	if !strings.Contains(rendered, ">  Remote Skill") {
		t.Fatalf("expected a single cursor marker on selected row, got %q", rendered)
	}
}

func TestRenderSkillsPickerShowsPopularityAndDownloads(t *testing.T) {
	m := model{
		contentWidth: 120,
		skillsPicker: &SkillsPickerState{
			Title:         "技能列表",
			ActiveTab:     "remote",
			RegistryLabel: "registry.example.com",
			RemoteItems: []RemoteSkillItem{{
				Slug:        "remote-skill",
				DisplayName: "Remote Skill",
				Summary:     "远程技能摘要",
				Popularity:  4.8,
				Downloads:   12345,
				Version:     "1.2.3",
			}},
		},
	}

	rendered := sanitizeTerminalText(m.renderSkillsPicker())
	if !strings.Contains(rendered, "人气 4.8") || !strings.Contains(rendered, "下载 12345") {
		t.Fatalf("expected popularity and downloads in rendered output, got %q", rendered)
	}
}

func TestRenderToolPanelKeepsTitleAndHintWithinWidth(t *testing.T) {
	m := model{
		contentWidth:      40,
		toolPanelViewport: viewport.New(24, 8),
		toolPanel: &ToolPanel{
			Title:   "这是一个非常长的详情标题，用来验证详情面板标题不会撑开弹窗宽度",
			Summary: "摘要信息也比较长，用来覆盖 summary 的裁宽处理。",
			Body:    "正文内容\n第二行内容",
			Hint:    "这是一个很长的提示文案，用来验证详情面板底部提示不会撑开整块弹窗。",
		},
	}
	m.syncToolPanelViewport()

	rendered := sanitizeTerminalText(m.renderToolPanel())
	assertRenderedMaxLineWidth(t, rendered, 24)
}

func assertRenderedMaxLineWidth(t *testing.T, rendered string, maxWidth int) {
	t.Helper()
	rendered = strings.ReplaceAll(rendered, overlayFocusMarker, "")
	for _, line := range strings.Split(rendered, "\n") {
		if lipgloss.Width(line) > maxWidth {
			t.Fatalf("expected line width <= %d, got %d for %q", maxWidth, lipgloss.Width(line), line)
		}
	}
}

func TestContextUsageSegmentShowsCurrentRoundUsage(t *testing.T) {
	model := model{
		requestStats: RequestMetrics{
			TokenUsage: &TokenUsage{
				ContextWindow: &ContextWindow{
					MaxInputTokens:           8192,
					CurrentRoundInputTokens:  2048,
					NextRoundAvailableTokens: 6144,
					ShortTermContextTokens:   3072,
				},
			},
		},
	}

	got := model.contextUsageSegment(false)
	if !strings.Contains(got, "当前上下文 2.0k/8.2k 25%") {
		t.Fatalf("expected current round usage in context segment, got %q", got)
	}
	if !strings.Contains(got, "可用 6.1k") {
		t.Fatalf("expected available context tokens in context segment, got %q", got)
	}
	if !strings.Contains(got, "历史 3.1k") {
		t.Fatalf("expected short-term history in context segment, got %q", got)
	}
}

func TestContextUsageSegmentShowsWatermarkWarning(t *testing.T) {
	model := model{
		requestStats: RequestMetrics{
			TokenUsage: &TokenUsage{
				ContextWindow: &ContextWindow{
					MaxInputTokens:           8192,
					CurrentRoundInputTokens:  7000,
					NextRoundAvailableTokens: 1192,
					HighWatermarkTokens:      6963,
					CompressionTargetTokens:  4915,
				},
			},
		},
	}

	got := model.contextUsageSegment(false)
	if !strings.Contains(got, "已过高水位") {
		t.Fatalf("expected watermark warning in context segment, got %q", got)
	}
	if !strings.Contains(got, "4.9k") {
		t.Fatalf("expected compression target hint in context segment, got %q", got)
	}
}

func TestInputContextUsageHintShowsCurrentUsageDetails(t *testing.T) {
	model := model{
		snapshot: Snapshot{SessionID: "session-123"},
		requestStats: RequestMetrics{
			TokenUsage: &TokenUsage{
				ContextWindow: &ContextWindow{
					MaxInputTokens:           8192,
					CurrentRoundInputTokens:  2048,
					NextRoundAvailableTokens: 6144,
					ShortTermContextTokens:   3072,
					HistoryMessageCount:      12,
				},
			},
		},
	}

	got := model.inputContextUsageHint()
	if !strings.Contains(got, "2/6K") {
		t.Fatalf("expected compact current usage summary in input hint, got %q", got)
	}
	if !strings.Contains(got, "共12条消息") {
		t.Fatalf("expected next-round availability rate in input hint, got %q", got)
	}
	if !strings.Contains(got, "2/6K") {
		t.Fatalf("expected history message count in input hint, got %q", got)
	}
}

func TestInputContextUsageHintShowsProjectedUsageWhileTyping(t *testing.T) {
	input := textarea.New()
	input.SetValue("请总结这段实现")
	model := model{
		snapshot: Snapshot{SessionID: "session-123"},
		input:    input,
		requestStats: RequestMetrics{
			TokenUsage: &TokenUsage{
				ContextWindow: &ContextWindow{
					MaxInputTokens:           8192,
					CurrentRoundInputTokens:  2048,
					NextRoundAvailableTokens: 6144,
				},
			},
		},
	}

	got := model.inputContextUsageHint()
	if !strings.Contains(got, "7") {
		t.Fatalf("expected draft token estimate in input hint, got %q", got)
	}
	if !strings.Contains(got, "2/6K") {
		t.Fatalf("expected compact current usage summary in input hint, got %q", got)
	}
	if !strings.Contains(got, "7 · 2/6K") {
		t.Fatalf("expected next-round availability rate in input hint, got %q", got)
	}
}

func TestInputContextUsageHintDefaultsToFullAvailabilityForNewSession(t *testing.T) {
	model := model{}

	got := model.inputContextUsageHint()
	if got != "上下文窗口100%可用" {
		t.Fatalf("expected default full availability hint for new session, got %q", got)
	}
}

func TestInputContextUsageHintDefaultsToPendingForReopenedSession(t *testing.T) {
	model := model{snapshot: Snapshot{SessionID: "session-123"}}

	got := model.inputContextUsageHint()
	if got != "正在输出..." {
		t.Fatalf("expected pending availability hint for reopened session, got %q", got)
	}
}

func TestStatusBarSegmentsKeepContextBeforeTokenUsage(t *testing.T) {
	model := model{
		snapshot: Snapshot{
			BackendAvailable: true,
			RequestMode:      "agent",
			ApprovalMode:     "confirm",
		},
		state: newProgressState(progressStreaming),
		requestStats: RequestMetrics{
			TokenUsage: &TokenUsage{
				InputTokens:  3200,
				OutputTokens: 800,
				TotalTokens:  4000,
				ContextWindow: &ContextWindow{
					MaxInputTokens:           8192,
					CurrentRoundInputTokens:  2048,
					NextRoundAvailableTokens: 6144,
				},
			},
		},
	}

	segments := model.statusBarSegments(50)
	joined := strings.Join(segments, " | ")
	if strings.Contains(joined, "上下文 ") {
		t.Fatalf("expected context text to move out of left status segments, got %q", joined)
	}
	if strings.Contains(joined, "Tk ") || strings.Contains(joined, "Token ") {
		t.Fatalf("expected token usage segment to be dropped before context segment, got %q", joined)
	}
	if grid := model.contextUsageGrid(); !strings.Contains(grid, "25%") {
		t.Fatalf("expected context grid to expose usage ratio, got %q", grid)
	}
}

func TestStatusBarSegmentsDoNotShowCompressionCount(t *testing.T) {
	model := model{
		snapshot: Snapshot{
			BackendAvailable: true,
			RequestMode:      "agent",
			ApprovalMode:     "confirm",
		},
		state: newProgressState(progressStreaming),
		requestStats: RequestMetrics{
			TokenUsage: &TokenUsage{
				InputTokens:  3200,
				OutputTokens: 800,
				TotalTokens:  4000,
				ContextWindow: &ContextWindow{
					MaxInputTokens:           8192,
					CurrentRoundInputTokens:  2048,
					NextRoundAvailableTokens: 6144,
				},
			},
			Compression: &CompressionStats{
				Count:            1,
				BeforeTokens:     9200,
				AfterTokens:      6000,
				CompressionRatio: 0.65,
			},
		},
	}

	segments := model.statusBarSegments(50)
	joined := strings.Join(segments, " | ")
	if strings.Contains(joined, "压缩 ") {
		t.Fatalf("expected compression count to stay out of status bar, got %q", joined)
	}
	if strings.Contains(joined, "上下文 ") {
		t.Fatalf("expected context text to move out of left status segments, got %q", joined)
	}
	if strings.Contains(joined, "Tk ") || strings.Contains(joined, "Token ") {
		t.Fatalf("expected token usage segment to be dropped before context segment, got %q", joined)
	}
}

func TestRenderStatusBarShowsContextGrid(t *testing.T) {
	model := model{
		snapshot: Snapshot{
			BackendAvailable: true,
			RequestMode:      "agent",
			ApprovalMode:     "confirm",
		},
		state:        newProgressState(progressStreaming),
		width:        120,
		contentWidth: 120,
		requestStats: RequestMetrics{
			TokenUsage: &TokenUsage{
				ContextWindow: &ContextWindow{
					MaxInputTokens:           8192,
					CurrentRoundInputTokens:  2048,
					NextRoundAvailableTokens: 6144,
				},
			},
		},
	}

	got := sanitizeTerminalText(model.renderStatusBar())
	if !strings.Contains(got, "■■□□□□□□ 25%") {
		t.Fatalf("expected context usage grid in status bar, got %q", got)
	}
	if !strings.Contains(got, "2/6K") {
		t.Fatalf("expected compact context hint on the right side of status bar, got %q", got)
	}
}

func TestFormatContextUsageGridClampsToBounds(t *testing.T) {
	if got := formatContextUsageGrid(9999, 8192, 10); got != "■■■■■■■■■■100%" {
		t.Fatalf("expected full grid when usage exceeds budget, got %q", got)
	}
	if got := formatContextUsageGrid(-1, 8192, 10); got != "□□□□□□□□□□  0%" {
		t.Fatalf("expected empty grid for negative usage, got %q", got)
	}
}

func TestApplyEventStatsUpdateStoresCompressionWithoutSummaryEntry(t *testing.T) {
	model := model{}
	stats := &RequestMetrics{
		Compression: &CompressionStats{
			Count:            1,
			BeforeTokens:     9200,
			AfterTokens:      6000,
			RemainingTokens:  2192,
			CompressionRatio: 0.65,
			TargetType:       "history_messages",
		},
	}

	model.applyEvent(Event{Kind: EventStatsUpdate, Stats: stats})
	if len(model.entries) != 0 {
		t.Fatalf("expected compression summary to stay out of transcript, got %#v", model.entries)
	}
	if model.requestStats.Compression == nil || model.requestStats.Compression.Count != 1 {
		t.Fatalf("expected compression stats to be stored, got %#v", model.requestStats.Compression)
	}

	model.applyEvent(Event{Kind: EventStatsUpdate, Stats: stats})
	if len(model.entries) != 0 {
		t.Fatalf("expected repeated compression updates to stay out of transcript, got %#v", model.entries)
	}
}

func TestAssistantChunkWithoutSplitBoundaryKeepsSingleEntry(t *testing.T) {
	model := model{}

	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "第一段", MessageID: "msg-1"})
	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "第二段", MessageID: "msg-1"})

	if len(model.entries) != 1 {
		t.Fatalf("expected single assistant entry, got %#v", model.entries)
	}
	if model.entries[0].role != "assistant" || model.entries[0].content != "第一段第二段" {
		t.Fatalf("expected merged assistant content, got %#v", model.entries[0])
	}
}

func TestAssistantChunkSplitAfterToolCall(t *testing.T) {
	model := model{}

	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "第一段", MessageID: "msg-1"})
	model.applyEvent(Event{Kind: EventToolCall, Text: "读取文件"})
	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "第二段", MessageID: "msg-1"})

	if len(model.entries) != 2 {
		t.Fatalf("expected two assistant entries after tool call split, got %#v", model.entries)
	}
	if model.entries[0].content != "第一段" || model.entries[1].content != "第二段" {
		t.Fatalf("unexpected split assistant content: %#v", model.entries)
	}
}

func TestAssistantChunkSplitWhenMessageIDChanges(t *testing.T) {
	model := model{}

	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "第一条", MessageID: "msg-1"})
	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "第二条", MessageID: "msg-2"})

	if len(model.entries) != 2 {
		t.Fatalf("expected two assistant entries when message_id changes, got %#v", model.entries)
	}
	if model.entries[0].content != "第一条" || model.entries[1].content != "第二条" {
		t.Fatalf("unexpected assistant content after message_id split: %#v", model.entries)
	}
}

func TestStartRequestStatsResetsAssistantSplitState(t *testing.T) {
	model := model{splitNextAssistant: true, lastAssistantMessageID: "msg-old"}

	model.startRequestStats("问题", "run-1")

	if model.splitNextAssistant {
		t.Fatalf("expected split state reset when request starts")
	}
	if model.lastAssistantMessageID != "" {
		t.Fatalf("expected last assistant message id reset, got %q", model.lastAssistantMessageID)
	}
}

func TestUpdateEndToEndToolCallAndAssistantSplitChain(t *testing.T) {
	input := textarea.New()
	m := model{
		snapshot: Snapshot{BackendAvailable: true},
		state:    newProgressState(progressSending),
		eventCh:  make(chan tea.Msg),
		input:    input,
		viewport: viewport.New(80, 20),
	}

	messages := []tea.Msg{
		streamEventMsg{event: Event{Kind: EventToolCall, Text: "读取文件"}},
		streamEventMsg{event: Event{Kind: EventAssistantChunk, Text: "第一段答复", MessageID: "msg-1"}},
		streamEventMsg{event: Event{Kind: EventToolCall, Text: "执行补丁"}},
		streamEventMsg{event: Event{Kind: EventAssistantChunk, Text: "第二段答复", MessageID: "msg-1"}},
		streamDoneMsg{},
	}

	for _, message := range messages {
		updated, _ := m.Update(message)
		typed, ok := updated.(model)
		if !ok {
			t.Fatalf("expected model type after update, got %T", updated)
		}
		m = typed
	}

	assistantEntries := []entry{}
	for _, item := range m.entries {
		if item.role == "assistant" {
			assistantEntries = append(assistantEntries, item)
		}
	}

	if len(assistantEntries) != 2 {
		t.Fatalf("expected two assistant entries in end-to-end chain, got %#v", m.entries)
	}
	if assistantEntries[0].content != "第一段答复" || assistantEntries[1].content != "第二段答复" {
		t.Fatalf("unexpected assistant contents in end-to-end chain: %#v", assistantEntries)
	}
	if m.state.phase != progressIdle {
		t.Fatalf("expected progress idle after stream done, got %s", m.state.phase)
	}
	if m.splitNextAssistant {
		t.Fatalf("expected split flag reset after stream done")
	}
}

func TestAssistantChunkSplitAfterStageEndBoundary(t *testing.T) {
	m := model{}

	m.applyEvent(Event{Kind: EventAssistantChunk, Text: "第一段", MessageID: "msg-1", Stage: "end"})
	m.applyEvent(Event{Kind: EventAssistantChunk, Text: "第二段", MessageID: "msg-1"})

	assistantEntries := []entry{}
	for _, item := range m.entries {
		if item.role == "assistant" {
			assistantEntries = append(assistantEntries, item)
		}
	}

	if len(assistantEntries) != 2 {
		t.Fatalf("expected stage end to split following assistant chunk, got %#v", m.entries)
	}
	if assistantEntries[0].content != "第一段" || assistantEntries[1].content != "第二段" {
		t.Fatalf("unexpected assistant contents after stage split: %#v", assistantEntries)
	}
}

func TestUpdateStreamFailedResetsAssistantSplitState(t *testing.T) {
	m := model{
		state:                  newProgressState(progressStreaming),
		splitNextAssistant:     true,
		lastAssistantMessageID: "msg-1",
		requestStats: RequestMetrics{
			StartedAt: time.Now(),
		},
		input: textarea.New(),
	}

	updated, _ := m.Update(streamFailedMsg{err: errors.New("stream failed")})
	typed, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model type after stream failed, got %T", updated)
	}

	if typed.splitNextAssistant {
		t.Fatalf("expected split flag reset after stream failed")
	}
	if typed.lastAssistantMessageID != "" {
		t.Fatalf("expected message id reset after stream failed, got %q", typed.lastAssistantMessageID)
	}
}

func TestUpdateSessionChangedResetsAssistantSplitState(t *testing.T) {
	m := model{
		snapshot:               Snapshot{BackendAvailable: true},
		state:                  newProgressState(progressSession),
		splitNextAssistant:     true,
		lastAssistantMessageID: "msg-1",
		input:                  textarea.New(),
	}

	updated, _ := m.Update(sessionChangedMsg{snapshot: Snapshot{BackendAvailable: true, SessionID: "session-2"}})
	typed, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model type after session changed, got %T", updated)
	}

	if typed.splitNextAssistant {
		t.Fatalf("expected split flag reset after session changed")
	}
	if typed.lastAssistantMessageID != "" {
		t.Fatalf("expected message id reset after session changed, got %q", typed.lastAssistantMessageID)
	}
}
