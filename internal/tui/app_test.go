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

func TestRenderTransientStatusHiddenInAgentMode(t *testing.T) {
	model := model{
		snapshot:     Snapshot{RequestMode: "agent"},
		viewport:     viewport.Model{Width: 80},
		activityHint: "执行 读取文件",
	}

	if got := sanitizeTerminalText(model.renderTransientStatus()); strings.TrimSpace(got) != "" {
		t.Fatalf("expected no transient status in agent mode, got %q", got)
	}

	model.snapshot.RequestMode = "ask"
	if got := sanitizeTerminalText(model.renderTransientStatus()); !strings.Contains(got, "执行 读取文件") {
		t.Fatalf("expected transient status in ask mode, got %q", got)
	}
}

func TestRenderStatusBarShowsModeSegmentForAllRequestModes(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		modeLabel string
	}{
		{name: "agent mode", mode: "agent", modeLabel: "执行模式"},
		{name: "ask mode", mode: "ask", modeLabel: "问答模式"},
		{name: "plan mode", mode: "plan", modeLabel: "计划模式"},
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
