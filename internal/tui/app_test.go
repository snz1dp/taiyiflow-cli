package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
)

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
	if !strings.Contains(got, "2.0K/8.2K") {
		t.Fatalf("expected compact current usage summary in input hint, got %q", got)
	}
	if !strings.Contains(got, "/6.1K") {
		t.Fatalf("expected next-round availability rate in input hint, got %q", got)
	}
	if !strings.Contains(got, "历史 12") {
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
	if !strings.Contains(got, "草稿 7") {
		t.Fatalf("expected draft token estimate in input hint, got %q", got)
	}
	if !strings.Contains(got, "本轮 25%") {
		t.Fatalf("expected compact current usage summary in input hint, got %q", got)
	}
	if !strings.Contains(got, "下轮 75%") {
		t.Fatalf("expected next-round availability rate in input hint, got %q", got)
	}
}

func TestInputContextUsageHintDefaultsToFullAvailabilityForNewSession(t *testing.T) {
	model := model{}

	got := model.inputContextUsageHint()
	if got != "下轮 100%" {
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
	if !strings.Contains(got, "■■■□□□□□□□  25%") {
		t.Fatalf("expected context usage grid in status bar, got %q", got)
	}
	if !strings.Contains(got, "下轮 75%") {
		t.Fatalf("expected compact context hint on the right side of status bar, got %q", got)
	}
}

func TestFormatContextUsageGridClampsToBounds(t *testing.T) {
	if got := formatContextUsageGrid(9999, 8192, 10); got != "■■■■■■■■■■ 100%" {
		t.Fatalf("expected full grid when usage exceeds budget, got %q", got)
	}
	if got := formatContextUsageGrid(-1, 8192, 10); got != "□□□□□□□□□□   0%" {
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
