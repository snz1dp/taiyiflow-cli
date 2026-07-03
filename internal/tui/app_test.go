package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
)

func testVerificationPNGBase64(t *testing.T) string {
	t.Helper()
	preview := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			pixel := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if (x+y)%2 == 0 {
				pixel = color.RGBA{R: 24, G: 24, B: 24, A: 255}
			}
			preview.Set(x, y, pixel)
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, preview); err != nil {
		t.Fatalf("encode test verification image failed: %v", err)
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func TestStartStreamErrorDoesNotQueueDoneMessage(t *testing.T) {
	eventCh := make(chan tea.Msg, 4)
	runnerErr := errors.New("backend failed")
	cmd := startStream(context.Background(), Request{}, func(ctx context.Context, request Request, emit func(Event), confirm func(ConfirmRequest) bool, askUser func(clientruntime.AskUserRequest) (clientruntime.AskUserResponse, error)) error {
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

func TestStartStreamReturnsAskUserRequestMessage(t *testing.T) {
	eventCh := make(chan tea.Msg, 4)
	cmd := startStream(context.Background(), Request{}, func(ctx context.Context, request Request, emit func(Event), confirm func(ConfirmRequest) bool, askUser func(clientruntime.AskUserRequest) (clientruntime.AskUserResponse, error)) error {
		_, err := askUser(clientruntime.AskUserRequest{
			Questions: []clientruntime.AskUserQuestion{{Name: "project", Label: "项目名称", InputFormat: "text"}},
		})
		return err
	}, eventCh)

	msg := cmd()
	requestMsg, ok := msg.(askUserRequestMsg)
	if !ok {
		t.Fatalf("expected askUserRequestMsg, got %T", msg)
	}
	if len(requestMsg.request.Questions) != 1 || requestMsg.request.Questions[0].Name != "project" {
		t.Fatalf("unexpected ask_user request: %#v", requestMsg.request)
	}
	requestMsg.responseCh <- askUserResult{response: clientruntime.AskUserResponse{Answers: map[string]any{"project": "Alpha"}}}
	close(requestMsg.responseCh)
	next := <-eventCh
	if _, ok := next.(streamDoneMsg); !ok {
		t.Fatalf("expected streamDoneMsg after ask_user response, got %T", next)
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

func TestBuildWelcomeRenderLinesPlacesSubHeadBeforeCommands(t *testing.T) {
	snapshot := Snapshot{
		BackendAvailable: true,
		SubHead:          "支持联网与知识库",
		WelcomeMessage:   "欢迎使用",
		Questions:        []string{"问题一"},
	}

	lines := buildWelcomeRenderLines(snapshot, buildWelcomeContent(snapshot))
	if len(lines) != 3 {
		t.Fatalf("expected subhead, command hint, and remaining content, got %#v", lines)
	}
	if lines[0] != "支持联网与知识库" {
		t.Fatalf("expected subhead first, got %#v", lines)
	}
	if !strings.HasPrefix(lines[1], "可用命令: ") {
		t.Fatalf("expected command hint after subhead, got %#v", lines)
	}
	if !strings.Contains(lines[2], "欢迎使用") {
		t.Fatalf("expected welcome text after command hint, got %#v", lines)
	}
	if strings.Contains(lines[2], "支持联网与知识库") {
		t.Fatalf("expected subhead not duplicated in trailing content, got %#v", lines)
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

func TestIsExecutableSlashCommandInputUsesSplitDialogueCommands(t *testing.T) {
	if isExecutableSlashCommandInput("/chat") {
		t.Fatalf("expected /chat to be removed from executable commands")
	}
	if !isExecutableSlashCommandInput("/retry") {
		t.Fatalf("expected /retry to be executable")
	}
	if !isExecutableSlashCommandInput("/about") {
		t.Fatalf("expected /about to be executable")
	}
	if !isExecutableSlashCommandInput("/refresh") {
		t.Fatalf("expected /refresh to be executable")
	}
	if !isExecutableSlashCommandInput("/internet on") {
		t.Fatalf("expected /internet on to be executable")
	}
	if !isExecutableSlashCommandInput("/knowledge auto") {
		t.Fatalf("expected /knowledge auto to be executable")
	}
	if !isExecutableSlashCommandInput("/upload report.txt") {
		t.Fatalf("expected /upload to be executable")
	}
	if isExecutableSlashCommandInput("/think 1") {
		t.Fatalf("expected /think to be removed from executable commands")
	}
	if !isExecutableSlashCommandInput("/session title 项目排障") {
		t.Fatalf("expected /session title to be executable")
	}
	if !isExecutableSlashCommandInput("/todo hide") {
		t.Fatalf("expected /todo hide to be executable")
	}
}

func TestViewShowsTodoSidebarWhenPendingItemsExist(t *testing.T) {
	input := textarea.New()
	input.Prompt = "> "
	appModel := model{
		ready:    true,
		width:    120,
		height:   24,
		snapshot: Snapshot{BackendAvailable: true, TodoItems: []TodoItem{{Title: "实现右侧待办栏", Status: "in_progress"}, {Title: "补齐测试", Status: "completed"}}},
		input:    input,
		viewport: viewport.New(0, 0),
		entries:  []entry{{role: "system", content: "历史消息"}},
	}
	appModel.syncLayout()

	rendered := sanitizeTerminalText(appModel.View())
	if !strings.Contains(rendered, "待办(todo)") {
		t.Fatalf("expected todo sidebar in view, got %q", rendered)
	}
	if !strings.Contains(rendered, "1. [进行中]") || !strings.Contains(rendered, "实现右侧") {
		t.Fatalf("expected todo item in view, got %q", rendered)
	}
	if !strings.Contains(rendered, "│ 待办(todo)") {
		t.Fatalf("expected todo content to keep left padding from divider, got %q", rendered)
	}
	if strings.Contains(rendered, "┐ ┌") {
		t.Fatalf("expected history and todo panes to share one outer frame, got %q", rendered)
	}
	if !strings.Contains(rendered, "历史消息") {
		t.Fatalf("expected history content to remain visible, got %q", rendered)
	}
}

func TestViewHidesTodoSidebarWhenAllItemsCompleted(t *testing.T) {
	input := textarea.New()
	input.Prompt = "> "
	appModel := model{
		ready:    true,
		width:    120,
		height:   24,
		snapshot: Snapshot{BackendAvailable: true, TodoItems: []TodoItem{{Title: "完成改造", Status: "completed"}}},
		input:    input,
		viewport: viewport.New(0, 0),
		entries:  []entry{{role: "system", content: "历史消息"}},
	}
	appModel.syncLayout()

	rendered := sanitizeTerminalText(appModel.View())
	if strings.Contains(rendered, "待办(todo)") {
		t.Fatalf("expected todo sidebar to hide after completion, got %q", rendered)
	}
}

func TestApplyEventTodoUpdateRefreshesSidebarLayoutImmediately(t *testing.T) {
	input := textarea.New()
	input.Prompt = "> "
	appModel := model{
		ready:    true,
		width:    120,
		height:   24,
		snapshot: Snapshot{BackendAvailable: true},
		input:    input,
		viewport: viewport.New(0, 0),
		entries:  []entry{{role: "system", content: "历史消息"}},
	}
	appModel.syncLayout()
	initialWidth := appModel.viewport.Width

	appModel.applyEvent(Event{Kind: EventTodoUpdate, TodoItems: []TodoItem{{Title: "实现右侧待办栏", Status: "in_progress"}}})

	if appModel.todoPaneWidth() == 0 {
		t.Fatal("expected todo pane width to become visible after todo update")
	}
	if appModel.viewport.Width >= initialWidth {
		t.Fatalf("expected viewport width to shrink for todo sidebar, before=%d after=%d", initialWidth, appModel.viewport.Width)
	}
	rendered := sanitizeTerminalText(appModel.View())
	if !strings.Contains(rendered, "待办(todo)") {
		t.Fatalf("expected todo sidebar to render immediately after todo update, got %q", rendered)
	}
}

func TestBuildRequestUsesAutoKnowledgeScopeWhenNoneSelected(t *testing.T) {
	request := model{snapshot: Snapshot{
		DefaultFlow:              "flow-1",
		KnowledgeSearchSupported: true,
		KnowledgeBases:           []KnowledgeBaseItem{{Code: "kb-a", Name: "知识库A"}},
	}}.buildRequest("你好", "run-1")
	if len(request.KnowledgeScopes) != 1 || request.KnowledgeScopes[0] != "all" {
		t.Fatalf("expected auto knowledge scope to map to all, got %#v", request.KnowledgeScopes)
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
		if command.Trigger == "/think" {
			t.Fatalf("expected /think to be removed from slash commands, got %#v", commands)
		}
	}
}

func TestVisibleSlashCommandsKeepsLoginInReadOnlyMode(t *testing.T) {
	input := textarea.New()
	input.SetValue("/l")

	model := model{
		snapshot: Snapshot{BackendAvailable: false},
		input:    input,
	}

	commands := model.visibleSlashCommands()
	for _, command := range commands {
		if command.Trigger == "/login" {
			return
		}
	}
	t.Fatalf("expected /login to stay visible in read-only mode, got %#v", commands)
}

func TestVisibleSlashCommandsDescribeConfigHelpEntry(t *testing.T) {
	input := textarea.New()
	input.SetValue("/con")

	model := model{
		snapshot: Snapshot{BackendAvailable: true},
		input:    input,
	}

	for _, command := range model.visibleSlashCommands() {
		if command.Trigger != "/config" {
			continue
		}
		if !strings.Contains(command.Description, "/config help") {
			t.Fatalf("expected /config description to reference help entry, got %#v", command)
		}
		if !strings.Contains(command.Description, "查看配置项说明") {
			t.Fatalf("expected /config description to use localized wording, got %#v", command)
		}
		return
	}
	t.Fatal("expected /config to appear in visible slash commands")
}

func TestVisibleSlashCommandsShowsRetryOnlyAfterFailure(t *testing.T) {
	input := textarea.New()
	input.SetValue("/ret")

	model := model{
		snapshot: Snapshot{BackendAvailable: true},
		input:    input,
	}
	for _, command := range model.visibleSlashCommands() {
		if command.Trigger == "/retry" {
			t.Fatalf("expected /retry to be hidden before a failed request, got %#v", command)
		}
	}

	request := model.buildRequest("重新试一下", "run-old")
	model.lastRetryableRequest = cloneRequest(request)
	model.retryAvailable = true
	commands := model.visibleSlashCommands()
	for _, command := range commands {
		if command.Trigger == "/retry" {
			return
		}
	}
	t.Fatalf("expected /retry to be visible after a failed request, got %#v", commands)
}

func TestRetryLastFailedRequestReusesInputWithNewRunID(t *testing.T) {
	input := textarea.New()
	request := Request{Input: "再试一次", FlowID: "flow-1", RunID: "run-old"}

	model := model{
		ctx:                  context.Background(),
		snapshot:             Snapshot{BackendAvailable: true},
		input:                input,
		eventCh:              make(chan tea.Msg, 4),
		progress:             progress.New(progress.WithDefaultGradient()),
		state:                newProgressState(progressIdle),
		lastRetryableRequest: cloneRequest(request),
		retryAvailable:       true,
		runner: func(ctx context.Context, request Request, emit func(Event), confirm func(ConfirmRequest) bool, askUser func(clientruntime.AskUserRequest) (clientruntime.AskUserResponse, error)) error {
			return nil
		},
	}

	cmd := model.retryLastFailedRequest()
	if cmd == nil {
		t.Fatal("expected retry command")
	}
	// startRequest 会添加 user 条目和空的 assistant 占位条目（用于立即显示 ... 动画）
	if len(model.entries) != 2 || model.entries[0].role != "user" || model.entries[0].content != request.Input {
		t.Fatalf("expected retry to append user entry, got %#v", model.entries)
	}
	if model.entries[1].role != "assistant" || model.entries[1].content != "" {
		t.Fatalf("expected retry to append empty assistant placeholder, got %#v", model.entries[1])
	}
	if model.lastRetryableRequest == nil || model.lastRetryableRequest.Input != request.Input || model.lastRetryableRequest.FlowID != request.FlowID {
		t.Fatalf("expected retry to keep retryable request content, got %#v", model.lastRetryableRequest)
	}
	if model.lastRetryableRequest.RunID == "" || model.lastRetryableRequest.RunID == request.RunID {
		t.Fatalf("expected retry to generate a new run id, got %#v", model.lastRetryableRequest.RunID)
	}
	if model.retryAvailable {
		t.Fatal("expected retry availability to clear while retry request is running")
	}
}

func TestEnterExecutesLoginSlashCommandImmediately(t *testing.T) {
	input := textarea.New()
	input.SetValue("/login")

	captured := ""
	appModel := model{
		ctx:      context.Background(),
		snapshot: Snapshot{BackendAvailable: true},
		input:    input,
		localCommand: func(ctx context.Context, command string, snapshot Snapshot) (LocalCommandResult, error) {
			captured = command
			return LocalCommandResult{
				Handled: true,
				LoginForm: &LoginFormState{
					Title: "登录(login)",
					Mode:  "password",
				},
			}, nil
		},
	}

	updatedModel, _ := appModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	updated := updatedModel.(model)

	if captured != "/login" {
		t.Fatalf("expected enter to execute /login immediately, got %q", captured)
	}
	if updated.loginForm == nil {
		t.Fatal("expected /login to open login overlay")
	}
	if got := strings.TrimSpace(updated.input.Value()); got != "" {
		t.Fatalf("expected input to clear after executing /login, got %q", got)
	}
}

func TestTodoSlashCommandTogglesSidebarWithoutLocalCommand(t *testing.T) {
	input := textarea.New()
	input.Prompt = "> "
	input.SetValue("/todo hide")
	appModel := model{
		ready:    true,
		width:    120,
		height:   24,
		snapshot: Snapshot{BackendAvailable: true, TodoItems: []TodoItem{{Title: "实现右侧待办栏", Status: "in_progress"}}},
		input:    input,
		viewport: viewport.New(0, 0),
	}
	appModel.syncLayout()

	updatedModel, cmd := appModel.handleMainKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected /todo hide not to dispatch external command, got %#v", cmd)
	}
	updated := updatedModel.(model)
	if !updated.todoPanelHidden {
		t.Fatal("expected todo panel to be hidden after /todo hide")
	}
	if got := strings.TrimSpace(updated.input.Value()); got != "" {
		t.Fatalf("expected input to clear after /todo hide, got %q", got)
	}

	updated.input.SetValue("/todo show")
	reshownModel, cmd := updated.handleMainKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf("expected /todo show not to dispatch external command, got %#v", cmd)
	}
	reshown := reshownModel.(model)
	if reshown.todoPanelHidden {
		t.Fatal("expected todo panel to be shown after /todo show")
	}
}

func TestRefreshSlashCommandForcesHistoryReload(t *testing.T) {
	input := textarea.New()
	input.SetValue("/refresh")
	appModel := model{
		ctx:      context.Background(),
		ready:    true,
		width:    120,
		height:   24,
		snapshot: Snapshot{BackendAvailable: true, History: []HistoryEntry{{Role: "assistant", Content: "旧历史消息"}}},
		input:    input,
		viewport: viewport.New(0, 0),
		entries:  []entry{{role: "assistant", content: "本地临时消息"}},
		refreshSnapshot: func(ctx context.Context, snapshot Snapshot) (Snapshot, []FlowItem, error) {
			updated := snapshot
			updated.History = []HistoryEntry{{Role: "assistant", Content: "刷新后的历史消息"}}
			return updated, nil, nil
		},
	}
	appModel.syncLayout()

	updatedModel, cmd := appModel.handleMainKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected /refresh to trigger snapshot refresh")
	}
	updated := updatedModel.(model)
	if !updated.manualSnapshotRefresh {
		t.Fatal("expected manual refresh flag to be set before snapshot reload")
	}
	msg := cmd()
	refreshMsg, ok := msg.(snapshotRefreshedMsg)
	if !ok {
		t.Fatalf("expected snapshotRefreshedMsg, got %T", msg)
	}
	finalModel, nextCmd := updated.Update(refreshMsg)
	if nextCmd != nil {
		t.Fatalf("expected refresh update not to enqueue follow-up command, got %#v", nextCmd)
	}
	final := finalModel.(model)
	if final.manualSnapshotRefresh {
		t.Fatal("expected manual refresh flag to clear after snapshot reload")
	}
	rendered, _ := renderEntries(final.entries, 80, 20, nil, final.snapshot, "", -1)
	rendered = sanitizeTerminalText(rendered)
	if !strings.Contains(rendered, "刷新后的历史消息") {
		t.Fatalf("expected refreshed history in entries, got %q", rendered)
	}
	if strings.Contains(rendered, "本地临时消息") {
		t.Fatalf("expected local stale entries to be replaced, got %q", rendered)
	}
	if final.activityHint != "" {
		t.Fatalf("expected refresh activity hint to clear, got %q", final.activityHint)
	}
	if got := strings.TrimSpace(final.input.Value()); got != "" {
		t.Fatalf("expected input to clear after /refresh, got %q", got)
	}
	if final.viewport.YOffset != 0 && final.viewport.YOffset < 0 {
		t.Fatalf("unexpected viewport offset after refresh: %d", final.viewport.YOffset)
	}
}

func TestMaybeLoadOlderHistoryPrependsEntriesWithoutJumpingViewport(t *testing.T) {
	loaded := false
	appModel := model{
		ctx: context.Background(),
		snapshot: Snapshot{
			BackendAvailable:   true,
			DefaultFlow:        "flow-1",
			SessionID:          "session-1",
			History:            []HistoryEntry{{Role: "assistant", Content: "当前历史回答"}},
			HistoryLoadedCount: 1,
			HistoryTotal:       2,
		},
		input:    textarea.New(),
		viewport: viewport.New(0, 0),
		entries:  []entry{{role: "assistant", content: "当前历史回答"}},
		historyLoad: func(ctx context.Context, snapshot Snapshot) (HistoryLoadResult, error) {
			loaded = true
			if snapshot.HistoryLoadedCount != 1 {
				t.Fatalf("expected loader to receive current loaded count, got %#v", snapshot)
			}
			return HistoryLoadResult{
				Entries:     []HistoryEntry{{Role: "user", Content: "更早的问题"}},
				LoadedCount: 2,
				Total:       2,
			}, nil
		},
		ready:        true,
		width:        100,
		height:       12,
		contentWidth: 80,
	}
	appModel.syncLayout()
	appModel.viewport.GotoTop()

	updatedModel, cmd := appModel.handleMainKey(tea.KeyMsg{Type: tea.KeyUp})
	if cmd == nil {
		t.Fatal("expected top-of-history scroll to trigger older history load")
	}
	updated := updatedModel.(model)
	if !updated.historyLoading {
		t.Fatal("expected history loading state to be enabled")
	}

	msg := cmd()
	historyMsg, ok := msg.(historyLoadedMsg)
	if !ok {
		t.Fatalf("expected historyLoadedMsg, got %T", msg)
	}
	updatedModel, nextCmd := updated.Update(historyMsg)
	if nextCmd != nil {
		t.Fatalf("expected history load update not to enqueue follow-up command, got %#v", nextCmd)
	}
	updated = updatedModel.(model)

	if !loaded {
		t.Fatal("expected history loader to run")
	}
	if updated.snapshot.HistoryLoadedCount != 2 || updated.snapshot.HistoryTotal != 2 {
		t.Fatalf("expected updated history counters, got %#v", updated.snapshot)
	}
	if len(updated.entries) < 2 || updated.entries[0].content != "更早的问题" {
		t.Fatalf("expected older history to be prepended, got %#v", updated.entries)
	}
	if updated.viewport.YOffset <= 0 {
		t.Fatalf("expected viewport offset to stay anchored after prepend, got %d", updated.viewport.YOffset)
	}
	if got := sanitizeTerminalText(updated.viewport.View()); !strings.Contains(got, "当前历史回答") {
		t.Fatalf("expected original top content to stay visible after prepend, got %q", got)
	}
}

func TestReliableMultilineShortcutsInsertNewline(t *testing.T) {
	testCases := []struct {
		name string
		msg  tea.KeyMsg
	}{
		{name: "alt enter", msg: tea.KeyMsg{Type: tea.KeyEnter, Alt: true}},
		{name: "ctrl j", msg: tea.KeyMsg{Type: tea.KeyCtrlJ}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			input := textarea.New()
			input.SetValue("第一行")

			appModel := model{
				snapshot: Snapshot{BackendAvailable: true},
				input:    input,
			}

			updatedModel, cmd := appModel.handleMainKey(testCase.msg)
			if cmd != nil {
				t.Fatalf("expected newline shortcut not to start command, got %#v", cmd)
			}
			updated := updatedModel.(model)
			if got := updated.input.Value(); got != "第一行\n" {
				t.Fatalf("expected newline to be inserted, got %q", got)
			}
		})
	}
}

func TestF6TogglesMouseScrollMode(t *testing.T) {
	appModel := model{}

	updatedModel, cmd := appModel.Update(tea.KeyMsg{Type: tea.KeyF6})
	if cmd == nil {
		t.Fatal("expected F6 to emit mouse mode command")
	}
	updated := updatedModel.(model)
	if !updated.mouseScrollEnabled {
		t.Fatal("expected F6 to enable mouse scroll mode")
	}
	if !strings.Contains(updated.footerHint, "鼠标滚动已开启") {
		t.Fatalf("expected enable hint after F6, got %q", updated.footerHint)
	}

	updatedModel, cmd = updated.Update(tea.KeyMsg{Type: tea.KeyF6})
	if cmd == nil {
		t.Fatal("expected second F6 to emit mouse disable command")
	}
	updated = updatedModel.(model)
	if updated.mouseScrollEnabled {
		t.Fatal("expected second F6 to disable mouse scroll mode")
	}
	if !strings.Contains(updated.footerHint, "已恢复鼠标选字") {
		t.Fatalf("expected disable hint after second F6, got %q", updated.footerHint)
	}
}

func TestBackspaceShrinksInputHeightAfterDeletingTrailingNewline(t *testing.T) {
	input := textarea.New()
	input.Prompt = "> "
	input.SetValue("第一行\n")
	input.Focus()

	appModel := model{
		ready:        true,
		width:        100,
		height:       20,
		contentWidth: 92,
		snapshot:     Snapshot{BackendAvailable: true},
		input:        input,
	}
	appModel.syncLayout()
	if got := appModel.input.Height(); got != 2 {
		t.Fatalf("expected initial multiline height 2, got %d", got)
	}

	updatedModel, cmd := appModel.handleMainKey(tea.KeyMsg{Type: tea.KeyBackspace})
	updated := updatedModel.(model)
	if got := updated.input.Value(); got != "第一行" {
		t.Fatalf("expected trailing newline removed, got %q", got)
	}
	if got := updated.input.Height(); got != 1 {
		t.Fatalf("expected input height to shrink to 1 after deleting newline, got %d", got)
	}
	if cmd == nil {
		t.Fatal("expected textarea update to keep returning its internal cursor command")
	}
}

func TestFitOverlayContentKeepsFocusedConfigFieldVisible(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Hint:  "测试超长弹窗",
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

func TestRenderConfigFormHidesAccessTokenField(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Values: ConfigFormValues{
			BackendBaseURL: "http://localhost:7860",
		},
	})

	model := model{
		contentWidth: 140,
		configForm:   state,
	}

	rendered := sanitizeTerminalText(model.renderConfigForm())
	if strings.Contains(rendered, "访问令牌") || strings.Contains(rendered, "API Key") {
		t.Fatalf("expected config form to hide access token field, got %q", rendered)
	}
}

func TestRenderConfigFormGroupsFieldsByCategory(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
	})

	model := model{
		contentWidth: 80,
		configForm:   state,
	}

	rendered := sanitizeTerminalText(model.renderConfigForm())
	categories := []string{"[服务端与智能体]", "[权限与审查]", "[运行与超时]", "[会话与输出]", "[技能配置]"}
	lastIndex := -1
	for _, category := range categories {
		index := strings.Index(rendered, category)
		if index < 0 {
			t.Fatalf("expected config form to render category %q, got %q", category, rendered)
		}
		if index <= lastIndex {
			t.Fatalf("expected category %q to appear after previous categories, got %q", category, rendered)
		}
		if strings.Count(rendered, category) != 1 {
			t.Fatalf("expected category %q to render once, got %q", category, rendered)
		}
		lastIndex = index
	}
}

func TestRenderConfigFormShowsDocUnstructuredDefaultHint(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
	})
	state.cursor = configFieldDocUnstructuredExts

	model := model{
		contentWidth: 80,
		configForm:   state,
	}

	rendered := sanitizeTerminalText(model.renderConfigForm())
	if !strings.Contains(rendered, "服务端文档解析类型") {
		t.Fatalf("expected doc unstructured field rendered, got %q", rendered)
	}
	if !strings.Contains(rendered, "留空表示恢复内置默认值") {
		t.Fatalf("expected doc unstructured field to mention built-in default state, got %q", rendered)
	}
}

func TestConfigFormSaveErrorStaysInConfigOverlay(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Hint:  "修改后保存",
		Values: ConfigFormValues{
			BackendBaseURL: "http://localhost:7860",
		},
	})
	state.cursor = configFieldCommandTimeout
	appModel := model{
		contentWidth: 80,
		configForm:   state,
	}

	updated, _ := appModel.Update(configFormSavedMsg{err: ConfigFieldError{Field: "backend_base_url", Message: "backend_base_url 必须是完整的 HTTP 地址"}})
	typed := updated.(model)

	if typed.configForm == nil {
		t.Fatal("expected config form to stay open on save error")
	}
	if typed.configForm.fieldErrorKey != "backend_base_url" || typed.configForm.fieldErrorText != "backend_base_url 必须是完整的 HTTP 地址" {
		t.Fatalf("expected config form to store field error, got %#v", typed.configForm)
	}
	if typed.configForm.cursor != configFieldBackendBaseURL {
		t.Fatalf("expected config form to focus invalid field, got %d", typed.configForm.cursor)
	}
	if typed.configForm.statusText != "" || typed.configForm.statusIsError {
		t.Fatalf("expected top-level status cleared for field error, got %#v", typed.configForm)
	}
	if len(typed.entries) != 0 {
		t.Fatalf("expected save error not to be appended to history, got %#v", typed.entries)
	}
	rendered := sanitizeTerminalText(typed.renderConfigForm())
	if !strings.Contains(rendered, "backend_base_url 必须是完整的 HTTP 地址") {
		t.Fatalf("expected config form to render save error, got %q", rendered)
	}
}

func TestRenderConfigFormUsesTextInputForAgentAutoLoopMaxRounds(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Values: ConfigFormValues{
			AgentAutoLoopMaxRounds: "0",
		},
	})
	state.cursor = configFieldAgentAutoLoopMaxRounds

	model := model{
		contentWidth: 80,
		configForm:   state,
	}

	rendered := sanitizeTerminalText(model.renderConfigForm())
	if !strings.Contains(rendered, "智能体自动续跑最大轮数") {
		t.Fatalf("expected max rounds field rendered, got %q", rendered)
	}
	if !strings.Contains(rendered, "填写 0-10000 的整数，0 表示禁用") {
		t.Fatalf("expected numeric hint for max rounds field, got %q", rendered)
	}
	if strings.Contains(rendered, "智能体自动续跑默认开关") || strings.Contains(rendered, "恢复默认(default)") {
		t.Fatalf("expected auto loop toggle field removed, got %q", rendered)
	}
	if !isConfigTextField(configFieldAgentAutoLoopMaxRounds) {
		t.Fatal("expected max rounds field to use text input")
	}
}

func TestHandleConfigFormKeyTextInputAllowsShortcutRunes(t *testing.T) {
	state := newConfigFormState(ConfigFormState{Title: "配置设置"})
	appModel := model{configForm: state}
	appModel.focusConfigFormField(configFieldWorkdir)

	updatedAny, _ := appModel.handleConfigFormKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	updated := updatedAny.(model)
	updatedAny, _ = updated.handleConfigFormKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	updated = updatedAny.(model)
	updatedAny, _ = updated.handleConfigFormKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	updated = updatedAny.(model)

	if updated.configForm == nil {
		t.Fatal("expected config form to stay open while editing text")
	}
	if updated.configForm.cursor != configFieldWorkdir {
		t.Fatalf("expected config cursor to stay on workdir field, got %d", updated.configForm.cursor)
	}
	if got := updated.configForm.inputs[configFieldWorkdir].Value(); got != "q k" {
		t.Fatalf("expected config text input to preserve shortcut-like runes, got %q", got)
	}
}

func TestRenderConfigFormIncludesCrossSessionMemoryToggle(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Values: ConfigFormValues{
			CrossSessionMemoryEnabled: true,
		},
	})
	state.cursor = configFieldCrossSessionMemory

	model := model{
		contentWidth: 80,
		configForm:   state,
	}

	rendered := sanitizeTerminalText(model.renderConfigForm())
	if !strings.Contains(rendered, "跨会话记忆") || !strings.Contains(rendered, "后端启用跨会话记忆") {
		t.Fatalf("expected cross session memory field rendered, got %q", rendered)
	}
	if !strings.Contains(rendered, "(*) 开启") {
		t.Fatalf("expected cross session memory enabled option selected, got %q", rendered)
	}
	if isConfigTextField(configFieldCrossSessionMemory) {
		t.Fatal("expected cross session memory field to use toggle choices")
	}
}

func TestRenderConfigFormUsesChoiceFieldsForBrowserOptions(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Values: ConfigFormValues{
			BrowserMode:           internalconfig.BrowserModeChromeSession,
			BrowserPermissionMode: internalconfig.BrowserPermissionModeSkipAll,
			BrowserChannel:        internalconfig.BrowserChannelEdge,
			BrowserExtensionSetup: internalconfig.BrowserExtensionSetupNative,
		},
	})
	state.cursor = configFieldBrowserMode

	rendered := sanitizeTerminalText(model{contentWidth: 88, configForm: state}.renderConfigForm())
	if !strings.Contains(rendered, "浏览器模式") || !strings.Contains(rendered, "(*) 当前会话") {
		t.Fatalf("expected browser mode choice field rendered, got %q", rendered)
	}
	if !strings.Contains(rendered, "浏览器权限模式") || !strings.Contains(rendered, "直接允许") {
		t.Fatalf("expected browser permission choice field rendered, got %q", rendered)
	}
	if !strings.Contains(rendered, "浏览器通道") || !strings.Contains(rendered, "Edge") {
		t.Fatalf("expected browser channel choice field rendered, got %q", rendered)
	}
	if isConfigTextField(configFieldBrowserMode) || isConfigTextField(configFieldBrowserPermissionMode) || isConfigTextField(configFieldBrowserChannel) || isConfigTextField(configFieldBrowserExtensionSetup) || isConfigTextField(configFieldBrowserHeadless) {
		t.Fatal("expected browser enum and boolean fields to use choice rendering")
	}
}

func TestRenderConfigFormUsesChoiceFieldsForComputerOptions(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Values: ConfigFormValues{
			ComputerEnabled:        true,
			ComputerPermissionMode: internalconfig.ComputerPermissionModeFollowPlan,
		},
	})
	state.cursor = configFieldComputerPermissionMode

	rendered := sanitizeTerminalText(model{contentWidth: 88, configForm: state}.renderConfigForm())
	if !strings.Contains(rendered, "桌面操作") || !strings.Contains(rendered, "桌面操作权限模式") || !strings.Contains(rendered, "跟随全局审批") || !strings.Contains(rendered, "始终允许") {
		t.Fatalf("expected computer option fields rendered, got %q", rendered)
	}
	if isConfigTextField(configFieldComputerEnabled) || isConfigTextField(configFieldComputerPermissionMode) {
		t.Fatal("expected desktop automation toggles to use choice rendering")
	}
}

func TestRenderConfigFormHidesComputerPermissionWhenDisabled(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Values: ConfigFormValues{
			ComputerEnabled:        false,
			ComputerPermissionMode: internalconfig.ComputerPermissionModeFollowPlan,
		},
	})

	rendered := sanitizeTerminalText(model{contentWidth: 88, configForm: state}.renderConfigForm())
	if strings.Contains(rendered, "桌面操作权限模式") {
		t.Fatalf("expected desktop permission field hidden when disabled, got %q", rendered)
	}
}

func TestRenderConfigFormHidesChromeSessionIncompatibleBrowserFields(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Values: ConfigFormValues{
			BrowserMode:           internalconfig.BrowserModeChromeSession,
			BrowserExtensionSetup: internalconfig.BrowserExtensionSetupNative,
		},
	})

	rendered := sanitizeTerminalText(model{contentWidth: 88, configForm: state}.renderConfigForm())
	if strings.Contains(rendered, "浏览器数据目录") || strings.Contains(rendered, "隔离浏览器无头模式") || strings.Contains(rendered, "远程调试地址") {
		t.Fatalf("expected isolated-only browser fields hidden in chrome_session mode, got %q", rendered)
	}
	if !strings.Contains(rendered, "扩展安装方式") || !strings.Contains(rendered, "浏览器桥接 socket 目录") {
		t.Fatalf("expected chrome_session fields rendered, got %q", rendered)
	}
}

func TestRenderConfigFormHidesNativeMessagingFieldsForIsolatedBrowser(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Values: ConfigFormValues{
			BrowserMode:           internalconfig.BrowserModeIsolated,
			BrowserExtensionSetup: internalconfig.BrowserExtensionSetupNative,
			BrowserHeadless:       "true",
		},
	})
	state.cursor = configFieldBrowserHeadless

	rendered := sanitizeTerminalText(model{contentWidth: 88, configForm: state}.renderConfigForm())
	if strings.Contains(rendered, "扩展安装方式") || strings.Contains(rendered, "浏览器桥接 socket 目录") {
		t.Fatalf("expected chrome_session-only browser fields hidden in isolated mode, got %q", rendered)
	}
	if !strings.Contains(rendered, "浏览器数据目录") || !strings.Contains(rendered, "隔离浏览器无头模式") || !strings.Contains(rendered, "(*) 开启") {
		t.Fatalf("expected isolated browser fields rendered, got %q", rendered)
	}
}

func TestRenderConfigFormHidesBrowserFieldsWhenDisabled(t *testing.T) {
	state := newConfigFormState(ConfigFormState{
		Title: "配置设置",
		Values: ConfigFormValues{
			BrowserMode:           internalconfig.BrowserModeDisabled,
			BrowserPermissionMode: internalconfig.BrowserPermissionModeSkipAll,
			BrowserChannel:        internalconfig.BrowserChannelEdge,
			BrowserExtensionSetup: internalconfig.BrowserExtensionSetupNative,
		},
	})

	rendered := sanitizeTerminalText(model{contentWidth: 88, configForm: state}.renderConfigForm())
	if !strings.Contains(rendered, "浏览器模式") {
		t.Fatalf("expected browser mode field to remain visible, got %q", rendered)
	}
	for _, hidden := range []string{"浏览器权限模式", "浏览器通道", "浏览器可执行文件路径", "浏览器数据目录", "浏览器连接超时（秒）", "扩展安装方式", "浏览器桥接 socket 目录"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("expected browser field %q hidden when disabled, got %q", hidden, rendered)
		}
	}
}

func TestConfigFormValuesFromInputsReadsAgentAutoLoopMaxRoundsAtShiftedIndex(t *testing.T) {
	state := newConfigFormState(ConfigFormState{})
	state.inputs[configFieldAgentAutoLoopMaxRounds].SetValue("0")
	state.inputs[configFieldOutputLanguage].SetValue("zh-CN")
	state.inputs[configFieldSkillsRegistryURL].SetValue("https://mirror-cn.clawhub.com")

	values := configFormValuesFromInputs(state.inputs)
	if values.AgentAutoLoopMaxRounds != "0" {
		t.Fatalf("expected max rounds from %d, got %q", configFieldAgentAutoLoopMaxRounds, values.AgentAutoLoopMaxRounds)
	}
	if values.OutputLanguage != "zh-CN" {
		t.Fatalf("expected output language from %d, got %q", configFieldOutputLanguage, values.OutputLanguage)
	}
	if values.SkillsRegistryURL != "https://mirror-cn.clawhub.com" {
		t.Fatalf("expected registry url from %d, got %q", configFieldSkillsRegistryURL, values.SkillsRegistryURL)
	}
}

func TestRenderLoginFormShowsQRCodeState(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:          "登录(login)",
		SupportsQRCode: true,
		Mode:           "qr",
		Remember:       true,
		QRTicket:       "ticket-1",
		QRLoginURL:     "https://example.com/login/scan",
		QRScanHint:     "请使用微信扫码确认",
		QRCodeASCII:    "██  ██\n  ██  ",
	})
	state.cursor = loginFieldSubmit

	model := model{
		contentWidth: 80,
		loginForm:    state,
	}

	rendered := sanitizeTerminalText(model.renderLoginForm())
	if !strings.Contains(rendered, "终端二维码(qr_code)") {
		t.Fatalf("expected terminal qr code label in login overlay, got %q", rendered)
	}
	if strings.Contains(rendered, "扫码地址(qr_url)") || strings.Contains(rendered, "ticket-1") {
		t.Fatalf("expected qr login details to hide raw url and ticket, got %q", rendered)
	}
	if !strings.Contains(rendered, "继续等待(Enter/w)") || !strings.Contains(rendered, "g 重新生成二维码") {
		t.Fatalf("expected qr wait action hints in login overlay, got %q", rendered)
	}
}

func TestRenderOverlayUsesFullscreenForLoginForm(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:    "登录",
		Mode:     "password",
		Remember: true,
	})

	appModel := model{
		width:        96,
		height:       28,
		contentWidth: 88,
		loginForm:    state,
	}

	rendered := appModel.renderOverlay()
	if lipgloss.Width(rendered) != 96 {
		t.Fatalf("expected login overlay to use full width, got %d", lipgloss.Width(rendered))
	}
	if lipgloss.Height(rendered) != 28 {
		t.Fatalf("expected login overlay to use full height, got %d", lipgloss.Height(rendered))
	}
}

func TestRenderOverlayUsesWiderConfirmOverlay(t *testing.T) {
	appModel := model{
		width:        140,
		height:       30,
		contentWidth: 132,
		pendingConfirm: &confirmState{
			request: ConfirmRequest{
				Title:        "写入比较摘要(write summary)待确认",
				Expanded:     true,
				PreviewTitle: "比较预览(compare preview)",
				PreviewBody:  "--- notes.txt\n+++ notes.txt\n@@\n-old\n+new",
				ApproveLabel: "确认本次写入",
				RejectLabel:  "取消本次写入",
			},
		},
	}

	rendered := appModel.renderOverlay()
	if lipgloss.Width(rendered) != 140 {
		t.Fatalf("expected expanded confirm overlay to use full width, got %d", lipgloss.Width(rendered))
	}
	if lipgloss.Height(rendered) != 30 {
		t.Fatalf("expected expanded confirm overlay to use full height, got %d", lipgloss.Height(rendered))
	}
}

func TestRenderAskUserFormShowsQuestionsAndOptions(t *testing.T) {
	form := newAskUserFormState(clientruntime.AskUserRequest{
		Questions: []clientruntime.AskUserQuestion{
			{Name: "project", Label: "项目名称", InputFormat: "text"},
			{Name: "targets", Label: "选择目标", InputFormat: "select", MultiSelect: true, OptionsList: []clientruntime.AskUserOption{{Label: "alpha", Value: "alpha"}, {Label: "beta", Value: "beta"}}},
		},
	}, make(chan askUserResult, 1))
	form.questions[0].input.SetValue("Alpha")
	form.questions[1].selected[1] = true
	appModel := model{
		width:        120,
		height:       30,
		contentWidth: 112,
		askUserForm:  form,
	}

	rendered := sanitizeTerminalText(appModel.renderOverlay())
	if !strings.Contains(rendered, "用户提问(ask_user)") {
		t.Fatalf("expected ask_user overlay title, got %q", rendered)
	}
	if !strings.Contains(rendered, "项目名称") || !strings.Contains(rendered, "选择目标") {
		t.Fatalf("expected ask_user questions in overlay, got %q", rendered)
	}
	if !strings.Contains(rendered, "alpha") || !strings.Contains(rendered, "[x] beta") {
		t.Fatalf("expected ask_user options in overlay, got %q", rendered)
	}
}

func TestHandleAskUserFormKeyEnterSubmitsResponse(t *testing.T) {
	responseCh := make(chan askUserResult, 1)
	form := newAskUserFormState(clientruntime.AskUserRequest{
		Questions: []clientruntime.AskUserQuestion{{Name: "project", Label: "项目名称", InputFormat: "text"}},
	}, responseCh)
	form.questions[0].input.SetValue("Alpha")
	appModel := model{askUserForm: form, eventCh: make(chan tea.Msg, 2)}

	updatedAny, _ := appModel.handleAskUserFormKey(tea.KeyMsg{Type: tea.KeyEnter})
	updated := updatedAny.(*model)
	if updated.askUserForm != nil {
		t.Fatal("expected ask_user form to close after submit")
	}
	result := <-responseCh
	if result.err != nil {
		t.Fatalf("expected ask_user submit success, got %v", result.err)
	}
	if result.response.Answers["project"] != "Alpha" {
		t.Fatalf("unexpected ask_user answer: %#v", result.response.Answers)
	}
}

func TestHandleAskUserFormKeyEscCancelsResponse(t *testing.T) {
	responseCh := make(chan askUserResult, 1)
	form := newAskUserFormState(clientruntime.AskUserRequest{
		Questions: []clientruntime.AskUserQuestion{{Name: "project", Label: "项目名称", InputFormat: "text"}},
	}, responseCh)
	appModel := model{askUserForm: form, eventCh: make(chan tea.Msg, 2)}

	updatedAny, _ := appModel.handleAskUserFormKey(tea.KeyMsg{Type: tea.KeyEsc})
	updated := updatedAny.(*model)
	if updated.askUserForm != nil {
		t.Fatal("expected ask_user form to close after cancel")
	}
	result := <-responseCh
	if result.err == nil || !strings.Contains(result.err.Error(), "取消 ask_user") {
		t.Fatalf("expected ask_user cancel error, got %#v", result.err)
	}
}

func TestHandleAskUserFormKeyTextInputAllowsShortcutRunes(t *testing.T) {
	responseCh := make(chan askUserResult, 1)
	form := newAskUserFormState(clientruntime.AskUserRequest{
		Questions: []clientruntime.AskUserQuestion{{Name: "project", Label: "项目名称", InputFormat: "text"}},
	}, responseCh)
	appModel := model{askUserForm: form, eventCh: make(chan tea.Msg, 2)}

	updatedAny, _ := appModel.handleAskUserFormKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	updated := updatedAny.(model)
	updatedAny, _ = updated.handleAskUserFormKey(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	updated = updatedAny.(model)
	updatedAny, _ = updated.handleAskUserFormKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	updated = updatedAny.(model)

	if updated.askUserForm == nil {
		t.Fatal("expected ask_user form to stay open while editing text")
	}
	if got := updated.askUserForm.questions[0].input.Value(); got != "q g" {
		t.Fatalf("expected ask_user text input to preserve shortcut-like runes, got %q", got)
	}
	select {
	case result := <-responseCh:
		t.Fatalf("expected no ask_user response while editing text, got %#v", result)
	default:
	}
	if updated.askUserForm.cursor != 0 {
		t.Fatalf("expected ask_user cursor to stay on current text field, got %d", updated.askUserForm.cursor)
	}
}

func TestRenderOverlayUsesLargeNonFullscreenSizeForSessionPicker(t *testing.T) {
	items := make([]SessionItem, 0, 40)
	for index := 0; index < 40; index++ {
		items = append(items, SessionItem{
			ID:           "session",
			Title:        "最近会话",
			RelativeTime: "1 分钟前",
		})
	}

	appModel := model{
		width:        120,
		height:       32,
		contentWidth: 112,
		sessionPicker: &SessionPickerState{
			Title:      "会话列表",
			Hint:       "使用方向键选择",
			Page:       1,
			TotalPages: 1,
			PageSize:   40,
			Items:      items,
		},
	}

	rendered := appModel.renderOverlay()
	expectedWidth := appModel.sessionPickerOverlayWidth() + 2
	if lipgloss.Width(rendered) != expectedWidth {
		t.Fatalf("expected session picker overlay width %d, got %d", expectedWidth, lipgloss.Width(rendered))
	}
	expectedHeight := appModel.overlayVisibleLineLimit() + overlayStyle.GetVerticalFrameSize()
	if lipgloss.Height(rendered) != expectedHeight {
		t.Fatalf("expected session picker overlay height %d, got %d", expectedHeight, lipgloss.Height(rendered))
	}
	if lipgloss.Height(rendered) >= appModel.height {
		t.Fatalf("expected session picker overlay to stay below full height, got %d", lipgloss.Height(rendered))
	}
}

func TestRenderConfirmOverlayShowsVisibleActionButtons(t *testing.T) {
	appModel := model{
		width:        120,
		height:       24,
		contentWidth: 112,
		pendingConfirm: &confirmState{
			request: ConfirmRequest{
				Title:        "写入比较摘要(write summary)待确认",
				PreviewTitle: "比较预览(compare preview)",
				PreviewBody:  "--- notes.txt\n+++ notes.txt\n@@\n-old\n+new",
				ApproveLabel: "确认本次写入",
				RejectLabel:  "取消本次写入",
			},
			cursor: 0,
		},
	}

	rendered := sanitizeTerminalText(appModel.renderConfirmOverlay())
	if !strings.Contains(rendered, "Y 确认本次写入") || !strings.Contains(rendered, "N 取消本次写入") {
		t.Fatalf("expected confirm overlay to render explicit action buttons, got %q", rendered)
	}
	if !strings.Contains(rendered, "操作(actions)") {
		t.Fatalf("expected confirm overlay to render action section, got %q", rendered)
	}
}

func TestRenderConfirmPreviewBodyHighlightsUnifiedDiff(t *testing.T) {
	appModel := model{
		width:          120,
		height:         24,
		contentWidth:   112,
		pendingConfirm: &confirmState{request: ConfirmRequest{Expanded: true}},
	}

	rendered := appModel.renderConfirmPreviewBody("--- notes.txt\n+++ notes.txt\n@@ expected_sha256_before=abc123 @@\n-before line\n+after line")
	sanitized := sanitizeTerminalText(rendered)
	if strings.Contains(sanitized, "---\n notes.txt") || strings.Contains(sanitized, "+\nafter line") {
		t.Fatalf("expected unified diff lines to stay intact in preview, got %q", sanitized)
	}
	if !strings.Contains(sanitized, "-before line") || !strings.Contains(sanitized, "+after line") {
		t.Fatalf("expected diff preview to keep unified diff content, got %q", sanitized)
	}
}

func TestRenderInlineStyledDiffPairHighlightsChangedRunes(t *testing.T) {
	removeRendered, addRendered := renderInlineStyledDiffPair("-before line", "+before value")
	sanitized := sanitizeTerminalText(removeRendered + "\n" + addRendered)
	if !strings.Contains(sanitized, "-before line") || !strings.Contains(sanitized, "+before value") {
		t.Fatalf("expected inline diff render to preserve visible content, got %q", sanitized)
	}
	pair := computeInlineDiffPair("before line", "before value")
	if pair.CommonPrefix != "before " || pair.OldChanged != "lin" || pair.NewChanged != "valu" || pair.CommonSuffix != "e" {
		t.Fatalf("unexpected inline diff pair: %#v", pair)
	}
}

func TestRenderInlineDiffBlockUsesLCSAnchorsForInsertedMiddleLine(t *testing.T) {
	rendered := renderInlineDiffBlock(
		[]string{"-before line", "-shared tail", "-last old"},
		[]string{"+before value", "+inserted only", "+shared tail", "+last new"},
		120,
	)
	got := make([]string, 0, len(rendered))
	for _, line := range rendered {
		got = append(got, sanitizeTerminalText(line))
	}
	expected := []string{
		"-before line",
		"+before value",
		"+inserted only",
		"-shared tail",
		"+shared tail",
		"-last old",
		"+last new",
	}
	if strings.Join(got, "\n") != strings.Join(expected, "\n") {
		t.Fatalf("expected LCS anchors to preserve middle insertion alignment, got %#v", got)
	}
	anchors := computeInlineDiffLineAnchors(
		[]string{"-before line", "-shared tail", "-last old"},
		[]string{"+before value", "+inserted only", "+shared tail", "+last new"},
	)
	if len(anchors) != 1 || anchors[0].RemoveIndex != 1 || anchors[0].AddIndex != 2 {
		t.Fatalf("unexpected inline diff anchors: %#v", anchors)
	}
}

func TestCurrentInputHeightCapsAtThirtyPercentOfViewport(t *testing.T) {
	appModel := model{
		width:        100,
		height:       20,
		contentWidth: 92,
		input:        textarea.New(),
	}
	appModel.input.Prompt = "> "
	appModel.input.SetValue(strings.Repeat("很长的输入内容\n", 20))

	if got := appModel.currentInputHeight(); got != 6 {
		t.Fatalf("expected input height capped at 30%% of viewport, got %d", got)
	}
}

func TestQRCodeWaitTickSubmitsWaitAction(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:           "登录",
		SupportsQRCode:  true,
		Mode:            "qr",
		Remember:        true,
		QRTicket:        "ticket-1",
		QRLoginURL:      "https://example.com/login/scan",
		QRScanHint:      "请打开微信扫码登录。",
		QRCodeASCII:     "██  ██\n  ██  ",
		QRWaiting:       true,
		QRWaitPollCount: 7,
	})

	captured := LoginFormSubmission{}
	appModel := model{
		ctx:       context.Background(),
		snapshot:  Snapshot{BackendAvailable: true},
		loginForm: state,
		loginFormSubmit: func(ctx context.Context, snapshot Snapshot, submission LoginFormSubmission) (LocalCommandResult, error) {
			captured = submission
			return LocalCommandResult{Handled: true}, nil
		},
	}

	updatedModel, cmd := appModel.Update(qrCodeWaitTickMsg{ticket: "ticket-1"})
	updated := updatedModel.(model)
	if updated.loginForm == nil || !updated.loginForm.qrWaiting {
		t.Fatalf("expected qr waiting state to remain active, got %#v", updated.loginForm)
	}
	if cmd == nil {
		t.Fatal("expected qr wait tick to submit wait_qr action")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg from qr wait tick, got %T", cmd())
	}
	for _, item := range batch {
		if item == nil {
			continue
		}
		if _, ok := item().(loginFormSavedMsg); ok {
			break
		}
	}
	if captured.Action != "wait_qr" {
		t.Fatalf("expected qr wait submission action, got %#v", captured)
	}
	if captured.QRTicket != "ticket-1" || captured.QRWaitPollCount != 7 {
		t.Fatalf("expected qr wait submission to preserve ticket and poll count, got %#v", captured)
	}
}

func TestHandleLoginFormModeSwitchToQRAutoStartsQRCode(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:          "登录(login)",
		SupportsQRCode: true,
		Mode:           "password",
		Remember:       true,
	})
	state.cursor = loginFieldMode

	captured := LoginFormSubmission{}
	appModel := model{
		ctx:       context.Background(),
		snapshot:  Snapshot{BackendAvailable: true},
		loginForm: state,
		loginFormSubmit: func(ctx context.Context, snapshot Snapshot, submission LoginFormSubmission) (LocalCommandResult, error) {
			captured = submission
			return LocalCommandResult{Handled: true}, nil
		},
	}

	updatedModel, cmd := appModel.handleLoginFormKey(tea.KeyMsg{Type: tea.KeyRight})
	updated := updatedModel.(model)
	if updated.loginForm == nil || updated.loginForm.mode != loginFormModeQR {
		t.Fatalf("expected qr mode after switching, got %#v", updated.loginForm)
	}
	if cmd == nil {
		t.Fatal("expected switch to qr mode to auto-submit start_qr")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg from qr mode switch, got %T", cmd())
	}
	for _, item := range batch {
		if item == nil {
			continue
		}
		if _, ok := item().(loginFormSavedMsg); ok {
			break
		}
	}
	if captured.Action != "start_qr" {
		t.Fatalf("expected auto-generated qr action, got %#v", captured)
	}
	if captured.Mode != loginFormModeQR {
		t.Fatalf("expected qr submission mode, got %#v", captured)
	}
}

func TestHandleLoginFormEnterOnUsernameMovesToPassword(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:    "登录(login)",
		Mode:     "password",
		Username: "zhangsan",
	})
	state.cursor = loginFieldUsername
	state.focusLoginFieldCursor()

	submitted := false
	appModel := model{
		ctx:       context.Background(),
		snapshot:  Snapshot{BackendAvailable: true},
		loginForm: state,
		loginFormSubmit: func(ctx context.Context, snapshot Snapshot, submission LoginFormSubmission) (LocalCommandResult, error) {
			submitted = true
			return LocalCommandResult{Handled: true}, nil
		},
	}

	updatedModel, cmd := appModel.handleLoginFormKey(tea.KeyMsg{Type: tea.KeyEnter})
	updated := updatedModel.(model)
	if cmd != nil {
		t.Fatalf("expected username enter to only move focus, got command %T", cmd)
	}
	if submitted {
		t.Fatal("expected username enter not to submit login form")
	}
	if updated.loginForm == nil || updated.loginForm.cursor != loginFieldPassword {
		t.Fatalf("expected username enter to move focus to password, got %#v", updated.loginForm)
	}
	if !updated.loginForm.inputs[1].Focused() {
		t.Fatalf("expected password input focused after username enter, got %#v", updated.loginForm.inputs[1])
	}
}

func TestHandleLoginFormKeyQuitsWhenLoginIsRequired(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:    "登录(login)",
		Mode:     "password",
		Remember: true,
	})
	appModel := model{
		loginForm: state,
		snapshot: Snapshot{
			BackendAvailable: true,
			AuthRequired:     true,
		},
	}

	updatedModel, cmd := appModel.handleLoginFormKey(tea.KeyMsg{Type: tea.KeyEsc})
	updated := updatedModel.(model)
	if !updated.quitting {
		t.Fatalf("expected login overlay close to quit when auth is required, got %#v", updated)
	}
	if cmd == nil {
		t.Fatal("expected quit command when auth is required")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg, got %T", cmd())
	}
}

func TestHandleLoginFormKeyIgnoresQShortcut(t *testing.T) {
	state := newLoginFormState(LoginFormState{Title: "登录(login)", SupportsQRCode: true, Mode: "password"})
	state.cursor = loginFieldMode
	appModel := model{loginForm: state, snapshot: Snapshot{BackendAvailable: true}}

	updatedModel, cmd := appModel.handleLoginFormKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	updated := updatedModel.(model)
	if cmd != nil {
		t.Fatalf("expected q not to close login form, got command %T", cmd)
	}
	if updated.loginForm == nil {
		t.Fatal("expected login form to remain open after q")
	}
	if updated.quitting {
		t.Fatalf("expected q not to quit app, got %#v", updated)
	}
}

func TestRenderLoginFormShowsTaiyiHeaderAndHidesRemember(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:    "登录(login)",
		Mode:     "password",
		Username: "zhangsan",
		Password: "secret-123",
		Remember: true,
	})

	appModel := model{
		contentWidth: 80,
		loginForm:    state,
	}

	rendered := sanitizeTerminalText(appModel.renderLoginForm())
	if !strings.Contains(rendered, "太乙智启") {
		t.Fatalf("expected taiyiflow header in login overlay, got %q", rendered)
	}
	if strings.Contains(rendered, "记住登录(remember)") {
		t.Fatalf("expected remember toggle to be hidden, got %q", rendered)
	}
	if strings.Contains(rendered, "密码登录与扫码登录统一使用同一服务地址") {
		t.Fatalf("expected login overlay to hide meaningless default hint, got %q", rendered)
	}
	if strings.Contains(rendered, "验证码票据(captcha_ticket)") {
		t.Fatalf("expected captcha ticket field to stay hidden before captcha step, got %q", rendered)
	}
	if !strings.Contains(rendered, "用户名(username)") || !strings.Contains(rendered, "密码(password)") {
		t.Fatalf("expected username/password fields in default password mode, got %q", rendered)
	}
	if strings.Contains(rendered, "扫码登录(qr)") || strings.Contains(rendered, "登录方式(login_mode)") {
		t.Fatalf("expected password-only form to hide qr mode option, got %q", rendered)
	}
}

func TestRenderLoginFormShowsCaptchaStepWhenTicketPresent(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:                  "登录(login)",
		Mode:                   "password",
		Username:               "zhangsan",
		Password:               "secret-123",
		Remember:               true,
		VerifyType:             "kaptcha",
		VerifyImageASCII:       "##@@\n@@##",
		VerifyImagePNGBase64:   testVerificationPNGBase64(t),
		VerifyImageWidth:       120,
		VerifyImageHeight:      48,
		VerifyImageURL:         "https://example.com" + internalconfig.DefaultAuthAPIPrefix + "/kaptcha/image?ticket=captcha-ticket-1",
		VerifyImagePreviewPath: "/tmp/captcha-preview.png",
		Ticket:                 "captcha-ticket-1",
	})

	appModel := model{
		contentWidth: 80,
		loginForm:    state,
	}

	rendered := sanitizeTerminalText(appModel.renderLoginForm())
	if !strings.Contains(rendered, "登录账号(username)") {
		t.Fatalf("expected username summary in captcha step, got %q", rendered)
	}
	if !strings.Contains(rendered, "图形验证码(code)") {
		t.Fatalf("expected captcha input in captcha step, got %q", rendered)
	}
	if !strings.Contains(rendered, "图形验证码(image)") {
		t.Fatalf("expected captcha step to show captcha type and image, got %q", rendered)
	}
	if !strings.Contains(rendered, "已回退为色块预览") {
		t.Fatalf("expected captcha step to explain color block fallback, got %q", rendered)
	}
	if !strings.Contains(rendered, "已将图形验证码保存到本地图片，并尝试用系统预览打开") {
		t.Fatalf("expected captcha step to show preview path hint, got %q", rendered)
	}
	if strings.Contains(rendered, "本地路径(image_path):") || strings.Contains(rendered, "/tmp/captcha-preview.png") {
		t.Fatalf("expected captcha step to hide local preview path, got %q", rendered)
	}
	if strings.Contains(rendered, "图片地址(image_url):") || strings.Contains(rendered, "https://example.com"+internalconfig.DefaultAuthAPIPrefix+"/kaptcha/image?ticket=captcha-ticket-1") {
		t.Fatalf("expected captcha step to hide image url, got %q", rendered)
	}
	if strings.Contains(rendered, "验证码票据(captcha_ticket)") {
		t.Fatalf("expected captcha step to hide ticket field, got %q", rendered)
	}
	if !strings.Contains(rendered, "密码(password)") {
		t.Fatalf("expected captcha step to keep password field visible, got %q", rendered)
	}
}

func TestRenderLoginFormShowsMailVerifyHint(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:        "登录(login)",
		Mode:         "password",
		Username:     "zhangsan",
		Password:     "secret-123",
		Remember:     true,
		VerifyType:   "mail",
		VerifyHint:   "验证码已发送到企业邮箱，请查收后输入。",
		VerifyTarget: "zhangsan@example.com",
		Ticket:       "verify-ticket-1",
	})

	appModel := model{
		contentWidth: 80,
		loginForm:    state,
	}

	rendered := sanitizeTerminalText(appModel.renderLoginForm())
	if !strings.Contains(rendered, "验证码已发送到企业邮箱，请查收后输入。") {
		t.Fatalf("expected mail verification hint to be rendered, got %q", rendered)
	}
	if !strings.Contains(rendered, "邮箱验证码(mail_code)") {
		t.Fatalf("expected mail verification field label, got %q", rendered)
	}
	if !strings.Contains(rendered, "zhangsan@example.com") {
		t.Fatalf("expected verify target to stay visible, got %q", rendered)
	}
}

func TestTOTPVerificationCopyUsesDynamicOTPLabel(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:      "登录(login)",
		Mode:       "password",
		Username:   "zhangsan",
		Password:   "secret-123",
		VerifyType: "totp",
		Ticket:     "verify-ticket-1",
	})

	if got := state.inputs[3].Placeholder; got != "请输入动态口令（TOTP）" {
		t.Fatalf("expected totp placeholder to use dynamic otp copy, got %q", got)
	}
	if got := loginVerificationTypeLabel("totp"); got != "动态口令(totp)" {
		t.Fatalf("expected totp type label to use dynamic otp copy, got %q", got)
	}
	if got := loginVerificationCodeLabel("totp"); got != "动态口令(totp_code)" {
		t.Fatalf("expected totp code label to use dynamic otp copy, got %q", got)
	}
	if got := loginVerificationDescription("totp", ""); got != "检测到需要动态口令（TOTP），请输入动态口令后继续登录。" {
		t.Fatalf("expected totp description to use dynamic otp copy, got %q", got)
	}
}

func TestVisibleLoginFieldsKeepPasswordDuringVerification(t *testing.T) {
	fields := visibleLoginFields(loginFormModePassword, true, false)
	if !containsInt(fields, loginFieldPassword) {
		t.Fatalf("expected password field to stay visible during verification, got %#v", fields)
	}
	if !containsInt(fields, loginFieldCaptchaCode) {
		t.Fatalf("expected captcha code field to stay visible during verification, got %#v", fields)
	}
	if containsInt(fields, loginFieldUsername) {
		t.Fatalf("expected username field to remain hidden during verification, got %#v", fields)
	}
}

func TestNewLoginFormStateFallsBackToPasswordWhenQRCodeUnsupported(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:          "登录(login)",
		SupportsQRCode: false,
		Mode:           "qr",
		Remember:       true,
	})
	if state.mode != loginFormModePassword {
		t.Fatalf("expected qr mode to fall back to password when unsupported, got %#v", state)
	}
	if state.cursor == loginFieldMode {
		t.Fatalf("expected mode cursor to be hidden when qr is unsupported, got %#v", state)
	}
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestLoginVerificationImageCellSizeAllowsLargerCaptchaPreview(t *testing.T) {
	widthCells, heightCells := loginVerificationImageCellSize(240, 96, 128)
	if widthCells != 128 || heightCells != 48 {
		t.Fatalf("expected enlarged captcha preview size, got width=%d height=%d", widthCells, heightCells)
	}
}

func TestRenderLoginVerificationImageUsesANSIColorPreviewInPlainTerminal(t *testing.T) {
	t.Setenv("TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL", "off")
	state := newLoginFormState(LoginFormState{VerifyImageASCII: "█▄\n▀ ", VerifyImagePNGBase64: testVerificationPNGBase64(t)})
	appModel := model{contentWidth: 80, loginForm: state}
	rendered := appModel.renderLoginVerificationImage()
	if !strings.Contains(rendered, "\x1b[38;2;") || !strings.Contains(rendered, "▀") {
		t.Fatalf("expected plain terminal captcha to use ansi color blocks, got %q", rendered)
	}
}

func TestLoginVerificationASCIIScalePreservesWiderPadding(t *testing.T) {
	scaleFactor := loginVerificationASCIIScale("  ██  \n  ██  ", 80)
	if scaleFactor != 4 {
		t.Fatalf("expected narrow captcha ascii to use max scale, got %d", scaleFactor)
	}
}

func TestRenderLoginFormHidesImageURLWithoutPreviewPathInPlainTerminal(t *testing.T) {
	t.Setenv("TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL", "off")
	state := newLoginFormState(LoginFormState{
		Title:                "登录(login)",
		Mode:                 "password",
		VerifyType:           "kaptcha",
		VerifyImageASCII:     "##",
		VerifyImagePNGBase64: testVerificationPNGBase64(t),
		VerifyImageURL:       "https://example.com" + internalconfig.DefaultAuthAPIPrefix + "/kaptcha/image?ticket=captcha-ticket-2",
		Ticket:               "captcha-ticket-2",
	})
	rendered := sanitizeTerminalText((model{contentWidth: 80, loginForm: state}).renderLoginForm())
	if strings.Contains(rendered, "图片地址(image_url):") || strings.Contains(rendered, "https://example.com"+internalconfig.DefaultAuthAPIPrefix+"/kaptcha/image?ticket=captcha-ticket-2") {
		t.Fatalf("expected plain terminal captcha step to hide http image url, got %q", rendered)
	}
	if strings.Contains(rendered, "本地路径(image_path):") {
		t.Fatalf("expected plain terminal captcha step to hide local image path, got %q", rendered)
	}
	if !strings.Contains(rendered, "已回退为色块预览") {
		t.Fatalf("expected plain terminal captcha step to mention color block preview, got %q", rendered)
	}
}

func TestRenderLoginFormUsesITerm2InlineImageForCaptcha(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("KITTY_WINDOW_ID", "")
	state := newLoginFormState(LoginFormState{
		Title:                "登录(login)",
		Mode:                 "password",
		Username:             "zhangsan",
		VerifyType:           "kaptcha",
		VerifyImageASCII:     "ascii-fallback",
		VerifyImagePNGBase64: "QUJD",
		VerifyImageWidth:     120,
		VerifyImageHeight:    48,
		Ticket:               "captcha-ticket-1",
	})

	appModel := model{contentWidth: 80, loginForm: state}
	rendered := appModel.renderLoginForm()
	if !strings.Contains(rendered, "\x1b]1337;File=inline=1;width=64;height=26;") {
		t.Fatalf("expected iterm2 inline image protocol, got %q", rendered)
	}
	if !strings.Contains(sanitizeTerminalText(rendered), "当前使用 iTerm2 终端图片协议显示图形验证码") {
		t.Fatalf("expected iterm2 hint in captcha step, got %q", sanitizeTerminalText(rendered))
	}
	if !strings.Contains(sanitizeTerminalText(rendered), "ASCII 备用图(ascii_fallback)") {
		t.Fatalf("expected iterm2 captcha step to keep ascii backup, got %q", sanitizeTerminalText(rendered))
	}
	if !strings.Contains(sanitizeTerminalText(rendered), "ascii-fallback") {
		t.Fatalf("expected iterm2 captcha step to keep ascii fallback content, got %q", sanitizeTerminalText(rendered))
	}
}

func TestRenderLoginFormUsesKittyInlineImageForCaptcha(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("TERM_PROGRAM", "")
	state := newLoginFormState(LoginFormState{
		Title:                "登录(login)",
		Mode:                 "password",
		Username:             "zhangsan",
		VerifyType:           "kaptcha",
		VerifyImageASCII:     "ascii-fallback",
		VerifyImagePNGBase64: "QUJD",
		VerifyImageWidth:     120,
		VerifyImageHeight:    48,
		Ticket:               "captcha-ticket-1",
	})

	appModel := model{contentWidth: 80, loginForm: state}
	rendered := appModel.renderLoginForm()
	if !strings.Contains(rendered, "\x1b_Gf=100,a=T,t=d,c=64,r=26,") {
		t.Fatalf("expected kitty inline image protocol, got %q", rendered)
	}
	if !strings.Contains(sanitizeTerminalText(rendered), "当前使用 kitty 终端图片协议显示图形验证码") {
		t.Fatalf("expected kitty hint in captcha step, got %q", sanitizeTerminalText(rendered))
	}
	if !strings.Contains(sanitizeTerminalText(rendered), "ASCII 备用图(ascii_fallback)") {
		t.Fatalf("expected kitty captcha step to keep ascii backup, got %q", sanitizeTerminalText(rendered))
	}
	if !strings.Contains(sanitizeTerminalText(rendered), "ascii-fallback") {
		t.Fatalf("expected kitty captcha step to keep ascii fallback content, got %q", sanitizeTerminalText(rendered))
	}
}

func TestRenderLoginFormShowsTmuxPassthroughHintForCaptcha(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	state := newLoginFormState(LoginFormState{
		Title:                "登录(login)",
		Mode:                 "password",
		Username:             "zhangsan",
		VerifyType:           "kaptcha",
		VerifyImageASCII:     "ascii-fallback",
		VerifyImagePNGBase64: "QUJD",
		VerifyImageWidth:     120,
		VerifyImageHeight:    48,
		Ticket:               "captcha-ticket-1",
	})

	appModel := model{contentWidth: 80, loginForm: state}
	rendered := sanitizeTerminalText(appModel.renderLoginForm())
	if !strings.Contains(rendered, "当前通过 tmux 透传 iTerm2 终端图片协议显示图形验证码") {
		t.Fatalf("expected tmux passthrough hint in captcha step, got %q", rendered)
	}
}

func TestRenderKittyInlineImageChunksPayload(t *testing.T) {
	rendered := renderKittyInlineImage(strings.Repeat("A", 5000), 24, 6)
	if strings.Count(rendered, "\x1b_G") < 2 {
		t.Fatalf("expected kitty image payload to be chunked, got %q", rendered)
	}
	if !strings.Contains(rendered, "m=1;") || !strings.Contains(rendered, "m=0;") {
		t.Fatalf("expected kitty chunk markers in rendered output, got %q", rendered)
	}
}

func TestDetectTerminalImageProtocolKeepsKittyInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL", "")

	if got := detectTerminalImageProtocol(); got != "kitty" {
		t.Fatalf("expected tmux session to keep kitty inline image protocol, got %q", got)
	}
}

func TestDetectTerminalImageProtocolKeepsITerm2InsideTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL", "")

	if got := detectTerminalImageProtocol(); got != "iterm2" {
		t.Fatalf("expected tmux session to keep iTerm2 inline image protocol, got %q", got)
	}
}

func TestDetectTerminalImageProtocolHonorsExplicitOverride(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv("TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL", "kitty")

	if got := detectTerminalImageProtocol(); got != "kitty" {
		t.Fatalf("expected explicit override to select kitty, got %q", got)
	}
}

func TestDetectTerminalImageProtocolHonorsDisableOverride(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	t.Setenv("KITTY_WINDOW_ID", "1")
	t.Setenv("TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL", "off")

	if got := detectTerminalImageProtocol(); got != "" {
		t.Fatalf("expected disable override to turn off inline image protocol, got %q", got)
	}
}

func TestRenderITerm2InlineImageWrapsForTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	rendered := renderITerm2InlineImage("QUJD", 24, 6)
	if !strings.HasPrefix(rendered, "\x1bPtmux;") {
		t.Fatalf("expected tmux passthrough prefix, got %q", rendered)
	}
	if !strings.Contains(rendered, "\x1b\x1b]1337;File=inline=1;") {
		t.Fatalf("expected tmux passthrough to escape iTerm2 image sequence, got %q", rendered)
	}
}

func TestRenderKittyInlineImageWrapsForTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	rendered := renderKittyInlineImage("QUJD", 24, 6)
	if !strings.HasPrefix(rendered, "\x1bPtmux;") {
		t.Fatalf("expected tmux passthrough prefix, got %q", rendered)
	}
	if !strings.Contains(rendered, "\x1b\x1b_Gf=100,a=T,t=d,") {
		t.Fatalf("expected tmux passthrough to escape kitty image sequence, got %q", rendered)
	}
}

func TestRenderLoginFormHighlightsErrorStatus(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:         "登录(login)",
		Mode:          "password",
		StatusText:    "登录失败: 用户名密码错误，您还有2次尝试机会！",
		StatusIsError: true,
	})

	appModel := model{
		contentWidth: 80,
		loginForm:    state,
	}

	rendered := sanitizeTerminalText(appModel.renderLoginForm())
	if !strings.Contains(rendered, "登录提示(status)") {
		t.Fatalf("expected highlighted login error label, got %q", rendered)
	}
	if !strings.Contains(rendered, "用户名密码错误，您还有2次尝试机会！") {
		t.Fatalf("expected highlighted login error message, got %q", rendered)
	}
}

func TestSanitizeTerminalTextRemovesKittyAPC(t *testing.T) {
	content := "before" + renderKittyInlineImage("QUJD", 24, 6) + "after"
	if got := sanitizeTerminalText(content); got != "beforeafter" {
		t.Fatalf("expected kitty APC to be removed, got %q", got)
	}
}

func TestSanitizeTerminalTextRemovesTmuxPassthrough(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1000/default,123,0")
	content := "before" + renderITerm2InlineImage("QUJD", 24, 6) + "after"
	if got := sanitizeTerminalText(content); got != "beforeafter" {
		t.Fatalf("expected tmux passthrough image sequence to be removed, got %q", got)
	}
}

func TestSanitizeTerminalTextStripsThinkControlTags(t *testing.T) {
	content := "回答前缀</think>\n正在执行服务端工具调用：搜索额度信息<think>"
	got := sanitizeTerminalText(content)

	if strings.Contains(got, "<think>") || strings.Contains(got, "</think>") {
		t.Fatalf("expected think control tags removed, got %q", got)
	}
	if !strings.Contains(got, "回答前缀") || !strings.Contains(got, "正在执行服务端工具调用") {
		t.Fatalf("expected visible content retained, got %q", got)
	}
}

func TestSanitizeTerminalTextStripsWatermarkCharacters(t *testing.T) {
	content := "可见内容" + "\u206a\u200b\u2061\u206b" + "保留正文"
	got := sanitizeTerminalText(content)

	if strings.Contains(got, "\u206a") || strings.Contains(got, "\u200b") || strings.Contains(got, "\u2061") || strings.Contains(got, "\u206b") {
		t.Fatalf("expected watermark characters removed, got %q", got)
	}
	if got != "可见内容保留正文" {
		t.Fatalf("expected visible text retained, got %q", got)
	}
}

func TestNewLoginFormStateRespectsFocusField(t *testing.T) {
	state := newLoginFormState(LoginFormState{
		Title:      "登录(login)",
		Mode:       "password",
		Username:   "zhangsan",
		Password:   "secret-123",
		StatusText: "提示: 用户名或密码错误",
		FocusField: loginFieldPassword,
	})
	if state.cursor != loginFieldPassword {
		t.Fatalf("expected focused password field, got %#v", state)
	}
	if !state.inputs[1].Focused() {
		t.Fatalf("expected password input focused, got %#v", state.inputs[1])
	}
}

func TestResolveInitialStartupCommandOpensLoginWhenAuthRequired(t *testing.T) {
	if got := resolveInitialStartupCommand(Snapshot{BackendAvailable: true, AuthRequired: true}, ""); got != "/login" {
		t.Fatalf("expected auth-required snapshot to auto-open /login, got %q", got)
	}
	if got := resolveInitialStartupCommand(Snapshot{BackendAvailable: true, AuthRequired: true}, "/help"); got != "/help" {
		t.Fatalf("expected explicit startup command to win, got %q", got)
	}
	if got := resolveInitialStartupCommand(Snapshot{BackendAvailable: true, AuthRequired: false}, ""); got != "" {
		t.Fatalf("expected no startup command when auth not required, got %q", got)
	}
}

func TestShouldAutoOpenLoginOverlayOnAuthTransition(t *testing.T) {
	snapshot := Snapshot{BackendAvailable: true, AuthRequired: true}
	if !shouldAutoOpenLoginOverlay(snapshot, true, false, true, false) {
		t.Fatal("expected login overlay to open when auth just became required")
	}
	if !shouldAutoOpenLoginOverlay(snapshot, false, false, true, false) {
		t.Fatal("expected login overlay to open when backend just recovered into auth-required state")
	}
	if shouldAutoOpenLoginOverlay(snapshot, true, true, true, false) {
		t.Fatal("expected no auto-open when auth was already required")
	}
	if shouldAutoOpenLoginOverlay(snapshot, true, false, false, false) {
		t.Fatal("expected no auto-open without local command handler")
	}
	if shouldAutoOpenLoginOverlay(snapshot, true, false, true, true) {
		t.Fatal("expected no auto-open while login overlay already open")
	}
}

func TestInitRequestsSnapshotRefreshWhenHandlerPresent(t *testing.T) {
	refreshed := false
	cmd := model{
		ctx:      context.Background(),
		snapshot: Snapshot{BackendAvailable: true},
		input:    textarea.New(),
		progress: progress.New(progress.WithDefaultGradient()),
		state:    newProgressState(progressIdle),
		refreshSnapshot: func(ctx context.Context, snapshot Snapshot) (Snapshot, []FlowItem, error) {
			refreshed = true
			updated := snapshot
			updated.CurrentUserName = "张三"
			return updated, nil, nil
		},
	}.Init()
	if cmd == nil {
		t.Fatal("expected init command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", cmd())
	}
	foundRefresh := false
	for _, item := range batch {
		if item == nil {
			continue
		}
		msg := item()
		if refreshedMsg, ok := msg.(snapshotRefreshedMsg); ok {
			foundRefresh = true
			if refreshedMsg.snapshot.CurrentUserName != "张三" {
				t.Fatalf("expected refreshed snapshot to propagate, got %#v", refreshedMsg.snapshot)
			}
		}
	}
	if !foundRefresh {
		t.Fatal("expected init to enqueue snapshot refresh")
	}
	if !refreshed {
		t.Fatal("expected refresh handler to run")
	}
}

func TestSnapshotRefreshRebuildsInitialEntriesAndContextBudget(t *testing.T) {
	initial := Snapshot{
		BackendAvailable: true,
		SessionID:        "session-123",
		WelcomeMessage:   "欢迎",
	}
	updated := initial
	updated.History = []HistoryEntry{{Role: "user", Content: "历史问题"}, {Role: "assistant", Content: "历史回答"}}
	updated.RequestStats = &RequestMetrics{
		TokenUsage: &TokenUsage{
			ContextWindow: &ContextWindow{
				MaxInputTokens:           4000,
				NextRoundAvailableTokens: 3200,
			},
		},
	}

	appModel := model{
		snapshot: initial,
		entries:  buildInitialEntries(initial),
		input:    textarea.New(),
		viewport: viewport.New(0, 0),
	}

	updatedModel, _ := appModel.Update(snapshotRefreshedMsg{snapshot: updated})
	result := updatedModel.(model)

	if len(result.entries) != 3 {
		t.Fatalf("expected refreshed entries to include welcome and history, got %#v", result.entries)
	}
	if result.entries[1].content != "历史问题" || result.entries[2].content != "历史回答" {
		t.Fatalf("expected refreshed history entries, got %#v", result.entries)
	}
	if result.requestStats.TokenUsage == nil || result.requestStats.TokenUsage.ContextWindow == nil {
		t.Fatalf("expected refreshed context window metrics, got %#v", result.requestStats)
	}
	if got := result.inputContextUsageHint(); got == "上下文窗口100%可用" || got == "" {
		t.Fatalf("expected concrete context hint after refresh, got %q", got)
	}
}

func TestStreamingIndicatorSuffixUsesFixedWidthFrames(t *testing.T) {
	model := model{
		state:                    newProgressState(progressStreaming),
		streamingIndicatorActive: true,
	}

	testCases := []struct {
		frame int
		want  string
	}{
		{frame: 0, want: ".  "},
		{frame: 1, want: "..  "},
		{frame: 2, want: "..."},
		{frame: 3, want: " .."},
		{frame: 4, want: "  ."},
		{frame: 5, want: ".  "},
		{frame: 6, want: "..  "},
	}

	for _, testCase := range testCases {
		model.streamingIndicatorFrame = testCase.frame
		if got := model.streamingIndicatorSuffix(); got != testCase.want {
			t.Fatalf("expected frame %d to render %q, got %q", testCase.frame, testCase.want, got)
		}
	}
}

func TestRenderEntriesAppendsStreamingIndicatorToLatestAssistant(t *testing.T) {
	rendered, _ := renderEntries([]entry{{role: "user", content: "问题"}, {role: "assistant", content: "回答"}}, 80, 20, nil, Snapshot{}, ".  ", 1)
	rendered = sanitizeTerminalText(rendered)
	if !strings.Contains(rendered, "回答.") {
		t.Fatalf("expected assistant entry to include streaming indicator, got %q", rendered)
	}
}

func TestSyncViewportDoesNotAppendStreamingIndicatorToPreviousAssistantWhileSending(t *testing.T) {
	model := model{
		entries: []entry{
			{role: "user", content: "上一个问题"},
			{role: "assistant", content: "上一个回答"},
			{role: "user", content: "新问题"},
		},
		state:                      newProgressState(progressSending),
		streamingIndicatorActive:   true,
		streamingIndicatorFrame:    2,
		activityHint:               "正在发送",
		width:                      120,
		contentWidth:               120,
		viewport:                   viewport.Model{Width: 80, Height: 10},
		currentStreamingEntryIndex: -1,
	}

	model.syncViewport()
	got := sanitizeTerminalText(model.viewport.View())
	if strings.Contains(got, "上一个回答.  ") || strings.Contains(got, "上一个回答..  ") || strings.Contains(got, "上一个回答...") || strings.Contains(got, "上一个回答 ..") || strings.Contains(got, "上一个回答  .") {
		t.Fatalf("expected previous assistant entry to stay unchanged, got %q", got)
	}
	if strings.Contains(got, "正在发送.  ") || strings.Contains(got, "正在发送..  ") || strings.Contains(got, "正在发送...") || strings.Contains(got, "正在发送 ..") || strings.Contains(got, "正在发送  .") {
		t.Fatalf("expected activity hint to stay out of history viewport, got %q", got)
	}
	if status := sanitizeTerminalText(model.renderStatusBar()); strings.Contains(status, "正在发送.  ") || strings.Contains(status, "正在发送..  ") || strings.Contains(status, "正在发送...") || strings.Contains(status, "正在发送 ..") || strings.Contains(status, "正在发送  .") {
		t.Fatalf("expected status bar to stay free of activity hint before first chunk, got %q", status)
	}
}

func TestSyncViewportAppendsStreamingIndicatorToCurrentAssistantChunk(t *testing.T) {
	model := model{
		entries: []entry{
			{role: "user", content: "上一个问题"},
			{role: "assistant", content: "上一个回答"},
			{role: "user", content: "新问题"},
		},
		state:                      newProgressState(progressSending),
		streamingIndicatorActive:   true,
		streamingIndicatorFrame:    2,
		viewport:                   viewport.Model{Width: 80, Height: 10},
		currentStreamingEntryIndex: -1,
	}

	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "新回答", MessageID: "msg-1"})
	model.syncViewport()
	got := sanitizeTerminalText(model.viewport.View())
	if strings.Contains(got, "上一个回答.  ") || strings.Contains(got, "上一个回答..  ") || strings.Contains(got, "上一个回答...") || strings.Contains(got, "上一个回答 ..") || strings.Contains(got, "上一个回答  .") {
		t.Fatalf("expected streaming indicator to leave previous assistant entry untouched, got %q", got)
	}
	if !strings.Contains(got, "新回答...") {
		t.Fatalf("expected current assistant chunk to include streaming indicator, got %q", got)
	}
}

func TestInterruptRequestRemovesStreamingIndicator(t *testing.T) {
	canceled := false
	appModel := model{
		entries:                    []entry{{role: "user", content: "问题"}, {role: "assistant", content: "回答"}},
		snapshot:                   Snapshot{BackendAvailable: true, RequestMode: "agent", ApprovalMode: "confirm"},
		state:                      newProgressState(progressStreaming),
		streamingIndicatorActive:   true,
		streamingIndicatorFrame:    2,
		activityHint:               "正在输出",
		currentStreamingEntryIndex: 1,
		requestCancel: func() {
			canceled = true
		},
		viewport:     viewport.Model{Width: 80, Height: 10},
		width:        120,
		contentWidth: 120,
	}
	appModel.syncViewport()
	if got := sanitizeTerminalText(appModel.viewport.View()); !strings.Contains(got, "回答...") {
		t.Fatalf("expected streaming indicator before interrupt, got %q", got)
	}

	updatedModel, cmd := appModel.handleMainKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil {
		t.Fatalf("expected interrupt not to return a command, got %#v", cmd)
	}
	updated := updatedModel.(model)
	if !canceled {
		t.Fatal("expected request cancel to be called")
	}
	if updated.streamingIndicatorActive {
		t.Fatal("expected streaming indicator to be inactive after interrupt")
	}
	if suffix := updated.streamingIndicatorSuffix(); suffix != "" {
		t.Fatalf("expected no streaming suffix after interrupt, got %q", suffix)
	}
	if got := sanitizeTerminalText(updated.viewport.View()); strings.Contains(got, "回答.  ") || strings.Contains(got, "回答..  ") || strings.Contains(got, "回答...") || strings.Contains(got, "回答 ..") || strings.Contains(got, "回答  .") || strings.Contains(got, "请求已中断.  ") || strings.Contains(got, "请求已中断..  ") || strings.Contains(got, "请求已中断...") || strings.Contains(got, "请求已中断 ..") || strings.Contains(got, "请求已中断  .") {
		t.Fatalf("expected interrupt to remove streaming ellipsis from viewport, got %q", got)
	}
	if status := sanitizeTerminalText(updated.renderStatusBar()); strings.Contains(status, "流式响应中") || strings.Contains(status, "请求已发出") {
		t.Fatalf("expected interrupt to clear in-flight status bar label, got %q", status)
	} else if !strings.Contains(status, "请求已中断") {
		t.Fatalf("expected interrupt status bar label, got %q", status)
	}
}

func TestCompressionProgressNoteRendersInFooterAndActivityHint(t *testing.T) {
	model := model{
		snapshot:     Snapshot{BackendAvailable: true, RequestMode: "agent", ApprovalMode: "confirm"},
		state:        newProgressState(progressStreaming),
		viewport:     viewport.Model{Width: 80},
		width:        120,
		contentWidth: 120,
	}

	model.applyEvent(Event{Kind: EventProgressNote, Text: "开始进行上下文压缩：history_trim / history_message_limit", EventType: "compression_progress"})

	if got := sanitizeTerminalText(model.renderStatusBar()); !strings.Contains(got, "正在压缩上下文") {
		t.Fatalf("expected compact compression start footer status, got %q", got)
	}
	if got := strings.TrimSpace(model.activityHint); got != "正在压缩上下文..." {
		t.Fatalf("expected compression start activity hint, got %q", got)
	}
	if got := sanitizeTerminalText(model.renderStatusBar()); strings.Contains(got, "开始进行上下文压缩") {
		t.Fatalf("expected verbose compression start copy removed from footer, got %q", got)
	}
	model.applyEvent(Event{Kind: EventStatsUpdate, Stats: &RequestMetrics{TokenUsage: &TokenUsage{ContextWindow: &ContextWindow{
		MaxInputTokens:           8192,
		CurrentRoundInputTokens:  2048,
		NextRoundAvailableTokens: 6144,
		ContextUsageTokens:       3072,
		DirectMessageTokens:      2048,
		CompressedHistoryTokens:  1024,
	}}, Compression: &CompressionStats{Count: 1, BeforeTokens: 320, AfterTokens: 160, RemainingTokens: 8032, CompressionRatio: 0.5}}})

	model.applyEvent(Event{Kind: EventProgressNote, Text: "上下文压缩完成：320 -> 160", EventType: "compression_progress"})

	if got := sanitizeTerminalText(model.renderStatusBar()); !strings.Contains(got, "正在压缩上下文") {
		t.Fatalf("expected compression hint to stay locked before first assistant chunk, got %q", got)
	}
	if got := strings.TrimSpace(model.activityHint); got != "正在压缩上下文..." {
		t.Fatalf("expected repeated compression messages to keep the same activity hint, got %q", got)
	}

	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "已恢复输出", MessageID: "msg-1"})

	if got := sanitizeTerminalText(model.renderStatusBar()); !strings.Contains(got, "2K/8K") {
		t.Fatalf("expected context usage hint to return after first assistant chunk, got %q", got)
	}
	if got := sanitizeTerminalText(model.renderStatusBar()); strings.Contains(got, "正在压缩上下文") {
		t.Fatalf("expected compression hint cleared after first assistant chunk, got %q", got)
	}
	if model.requestStats.Compression == nil || model.requestStats.Compression.AfterTokens != 160 {
		t.Fatalf("expected final compression stats to stay recorded, got %#v", model.requestStats.Compression)
	}
}

func TestFollowupRequestRestoresCompressionHintUntilNextChunk(t *testing.T) {
	model := model{
		width:        120,
		contentWidth: 120,
		requestStats: RequestMetrics{TokenUsage: &TokenUsage{ContextWindow: &ContextWindow{
			MaxInputTokens:           8192,
			CurrentRoundInputTokens:  2048,
			NextRoundAvailableTokens: 6144,
		}}},
	}

	model.applyEvent(Event{Kind: EventProgressNote, Text: "开始进行上下文压缩", EventType: "compression_progress"})
	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "首轮输出", MessageID: "msg-1"})

	if model.compressionInProgress {
		t.Fatalf("expected first request chunk to release compression lock")
	}

	model.applyEvent(Event{Kind: EventProgressNote, Text: "自动续跑: 需要继续执行"})
	model.applyEvent(Event{Kind: EventFollowupRequest})

	if !model.compressionInProgress {
		t.Fatalf("expected followup request to restore compression lock")
	}
	if got := model.inputContextUsageHint(); got != "正在压缩上下文..." {
		t.Fatalf("expected followup request to restore compression hint, got %q", got)
	}
	if got := strings.TrimSpace(model.activityHint); got != "自动续跑: 需要继续执行" {
		t.Fatalf("expected followup request note preserved, got %q", got)
	}

	model.applyEvent(Event{Kind: EventReasoningChunk, Text: "继续分析", MessageID: "msg-2"})

	if model.compressionInProgress {
		t.Fatalf("expected followup request chunk to release compression lock")
	}
	if got := sanitizeTerminalText(model.renderStatusBar()); strings.Contains(got, "正在压缩上下文") {
		t.Fatalf("expected compression hint cleared after followup request chunk, got %q", got)
	}
	if got := sanitizeTerminalText(model.renderStatusBar()); !strings.Contains(got, "2K/8K") {
		t.Fatalf("expected context usage hint restored after followup request chunk, got %q", got)
	}
}

func TestHandleConfirmKeySupportsSkillSpecificAndGlobalApproval(t *testing.T) {
	t.Run("approve current skill", func(t *testing.T) {
		approvedCh := make(chan bool, 1)
		skillApproved := false
		allApproved := false
		appModel := model{
			ctx: context.Background(),
			pendingConfirm: &confirmState{
				request: ConfirmRequest{
					ApproveLabel:       "本次允许",
					RejectLabel:        "本次拒绝",
					AlwaysApproveLabel: "始终允许此技能",
					AllowAllLabel:      "允许全部技能",
					AlwaysApprove: func(context.Context) error {
						skillApproved = true
						return nil
					},
					AllowAll: func(context.Context) error {
						allApproved = true
						return nil
					},
				},
				responseCh: approvedCh,
				cursor:     2,
			},
		}
		updatedModel, _ := appModel.handleConfirmKey(tea.KeyMsg{Type: tea.KeyEnter})
		updated := updatedModel.(*model)
		if !skillApproved || allApproved {
			t.Fatalf("unexpected approval callbacks: skill=%v all=%v", skillApproved, allApproved)
		}
		select {
		case approved := <-approvedCh:
			if !approved {
				t.Fatal("expected skill approval action to approve")
			}
		default:
			t.Fatal("expected approval response")
		}
		if updated.pendingConfirm != nil {
			t.Fatalf("expected pending confirm cleared, got %#v", updated.pendingConfirm)
		}
	})

	t.Run("approve all skills", func(t *testing.T) {
		approvedCh := make(chan bool, 1)
		skillApproved := false
		allApproved := false
		appModel := model{
			ctx: context.Background(),
			pendingConfirm: &confirmState{
				request: ConfirmRequest{
					ApproveLabel:       "本次允许",
					RejectLabel:        "本次拒绝",
					AlwaysApproveLabel: "始终允许此技能",
					AllowAllLabel:      "允许全部技能",
					AlwaysApprove: func(context.Context) error {
						skillApproved = true
						return nil
					},
					AllowAll: func(context.Context) error {
						allApproved = true
						return nil
					},
				},
				responseCh: approvedCh,
				cursor:     3,
			},
		}
		updatedModel, _ := appModel.handleConfirmKey(tea.KeyMsg{Type: tea.KeyEnter})
		updated := updatedModel.(*model)
		if skillApproved || !allApproved {
			t.Fatalf("unexpected approval callbacks: skill=%v all=%v", skillApproved, allApproved)
		}
		select {
		case approved := <-approvedCh:
			if !approved {
				t.Fatal("expected global approval action to approve")
			}
		default:
			t.Fatal("expected approval response")
		}
		if updated.pendingConfirm != nil {
			t.Fatalf("expected pending confirm cleared, got %#v", updated.pendingConfirm)
		}
	})
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
	if strings.TrimSpace(m.footerHint) != "" {
		t.Fatalf("expected clear history result without footer hint to clear footer, got %q", m.footerHint)
	}
}

func TestApplyLocalCommandResultClearHistoryClearsExistingFooterHint(t *testing.T) {
	m := model{footerHint: "已退出登录。"}

	m.applyLocalCommandResult(LocalCommandResult{Handled: true, ClearHistory: true, RebuildEntries: true})

	if strings.TrimSpace(m.footerHint) != "" {
		t.Fatalf("expected clear history result to clear stale footer hint, got %q", m.footerHint)
	}
}

func TestApplyLocalCommandResultOpensSkillsPicker(t *testing.T) {
	m := model{}
	result := LocalCommandResult{
		Handled: true,
		SkillsPicker: &SkillsPickerState{
			Title:     "技能列表",
			ActiveTab: "local",
			Items:     []SkillItem{{Name: "taiyiflow-api", Description: "接口参考"}},
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
			Title:     "技能列表",
			ActiveTab: "local",
			Items: []SkillItem{{
				Name:        "taiyiflow-api",
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
	if !strings.Contains(updated.toolPanel.Title, "taiyiflow-api") {
		t.Fatalf("unexpected tool panel title: %#v", updated.toolPanel)
	}
}

func TestHandleSkillsPickerDeleteOnLocalRunsRemoveCommand(t *testing.T) {
	captured := ""
	m := model{
		ctx:   context.Background(),
		input: textarea.New(),
		skillsPicker: &SkillsPickerState{
			Title:       "技能列表",
			ActiveTab:   "local",
			RemoteQuery: "remote debug",
			Items: []SkillItem{{
				Name:         "Remote Skill",
				Description:  "已安装远程技能",
				Removable:    true,
				RemoveTarget: "remote-skill",
			}},
		},
		localCommand: func(ctx context.Context, command string, snapshot Snapshot) (LocalCommandResult, error) {
			captured = command
			return LocalCommandResult{Handled: true}, nil
		},
	}

	updatedModel, cmd := m.handleSkillsPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	updated := updatedModel.(model)
	if strings.TrimSpace(updated.activityHint) != "正在删除已安装技能..." {
		t.Fatalf("unexpected activity hint: %q", updated.activityHint)
	}
	if cmd == nil {
		t.Fatal("expected delete command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", msg)
	}
	for _, nested := range batch {
		if nested != nil {
			_ = nested()
		}
	}
	if captured != "/skills-picker remove-local remote-skill remote debug" {
		t.Fatalf("unexpected delete command: %q", captured)
	}
}

func TestHandleSkillsPickerToggleDisableOnLocalRunsDisableCommand(t *testing.T) {
	captured := ""
	m := model{
		ctx:   context.Background(),
		input: textarea.New(),
		skillsPicker: &SkillsPickerState{
			Title:       "技能列表",
			ActiveTab:   "local",
			RemoteQuery: "remote debug",
			Items: []SkillItem{{
				Name:         "Remote Skill",
				Description:  "已安装远程技能",
				ToggleTarget: "remote-skill",
			}},
		},
		localCommand: func(ctx context.Context, command string, snapshot Snapshot) (LocalCommandResult, error) {
			captured = command
			return LocalCommandResult{Handled: true}, nil
		},
	}

	updatedModel, cmd := m.handleSkillsPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	updated := updatedModel.(model)
	if strings.TrimSpace(updated.activityHint) != "正在禁用已安装技能..." {
		t.Fatalf("unexpected activity hint: %q", updated.activityHint)
	}
	if cmd == nil {
		t.Fatal("expected disable command")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", msg)
	}
	for _, nested := range batch {
		if nested != nil {
			_ = nested()
		}
	}
	if captured != "/skills-picker disable-local remote-skill remote debug" {
		t.Fatalf("unexpected disable command: %q", captured)
	}
}

func TestBuildSkillDetailPanelIncludesToggleAndDeleteActionsForRemovableSkill(t *testing.T) {
	panel := buildSkillDetailPanel(SkillItem{
		Name:          "Remote Skill",
		Description:   "已安装远程技能",
		SkillMarkdown: "---\nname: Remote Skill\n---\n\n详细内容",
		ToggleTarget:  "remote-skill",
		Removable:     true,
		RemoveTarget:  "remote-skill",
		ReferenceFiles: []SkillReferenceItem{{
			Path:        "references/guide.md",
			Description: "参考说明",
		}},
	}, "remote debug")
	if panel == nil {
		t.Fatal("expected panel")
	}
	if !strings.Contains(panel.Body, "SKILL.md 内容:") || !strings.Contains(panel.Body, "详细内容") {
		t.Fatalf("expected skill markdown content in panel body, got %#v", panel)
	}
	if strings.Index(panel.Body, "SKILL.md 内容:") > strings.Index(panel.Body, "参考文件:") {
		t.Fatalf("expected skill markdown block to appear before references, got %#v", panel)
	}
	if len(panel.Actions) != 2 {
		t.Fatalf("expected two actions, got %#v", panel.Actions)
	}
	toggleAction := panel.Actions[0]
	if toggleAction.Key != "x" || toggleAction.Disabled {
		t.Fatalf("unexpected toggle action: %#v", toggleAction)
	}
	if toggleAction.Command != "/skills-picker disable-local-detail remote-skill remote debug" {
		t.Fatalf("unexpected toggle action command: %#v", toggleAction)
	}
	deleteAction := panel.Actions[1]
	if deleteAction.Key != "d" || deleteAction.Disabled {
		t.Fatalf("unexpected delete action: %#v", deleteAction)
	}
	if deleteAction.Command != "/skills-picker remove-local-detail remote-skill remote debug" {
		t.Fatalf("unexpected delete action command: %#v", deleteAction)
	}
}

func TestOpenToolPanelResetsViewportToTop(t *testing.T) {
	body := strings.Join([]string{
		"第1行",
		"第2行",
		"第3行",
		"第4行",
		"第5行",
		"第6行",
		"第7行",
		"第8行",
		"第9行",
		"第10行",
	}, "\n")
	m := model{
		contentWidth:      80,
		height:            30,
		input:             textarea.New(),
		toolPanelViewport: viewport.New(24, 4),
	}
	m.toolPanelViewport.SetContent(body)
	m.toolPanelViewport.GotoBottom()

	m.openToolPanel(&ToolPanel{Title: "技能详情", Body: body})

	if m.toolPanelViewport.YOffset != 0 {
		t.Fatalf("expected viewport to reset to top, got offset %d", m.toolPanelViewport.YOffset)
	}
	if !strings.Contains(m.toolPanelViewport.View(), "第1行") {
		t.Fatalf("expected viewport to show top content, got %q", m.toolPanelViewport.View())
	}
}

func TestApplyLocalCommandResultCanCloseToolPanelWhenRefreshingSkillsPicker(t *testing.T) {
	m := model{
		toolPanel:         &ToolPanel{Title: "技能详情"},
		toolPanelViewport: viewport.New(24, 4),
		input:             textarea.New(),
	}
	m.toolPanelViewport.SetContent(strings.Join([]string{"第1行", "第2行", "第3行", "第4行", "第5行"}, "\n"))
	m.toolPanelViewport.GotoBottom()

	m.applyLocalCommandResult(LocalCommandResult{
		Handled:        true,
		CloseToolPanel: true,
		SkillsPicker: &SkillsPickerState{
			Title:     "技能列表",
			ActiveTab: "local",
			Items:     []SkillItem{{Name: "remote-skill"}},
		},
	})

	if m.toolPanel != nil {
		t.Fatalf("expected tool panel to close, got %#v", m.toolPanel)
	}
	if m.toolPanelViewport.YOffset != 0 {
		t.Fatalf("expected tool panel viewport to reset after close, got %d", m.toolPanelViewport.YOffset)
	}
	if strings.TrimSpace(m.toolPanelViewport.View()) != "" {
		t.Fatalf("expected tool panel viewport content to clear after close, got %q", m.toolPanelViewport.View())
	}
	if m.skillsPicker == nil || len(m.skillsPicker.Items) != 1 {
		t.Fatalf("expected refreshed skills picker, got %#v", m.skillsPicker)
	}
}

func TestHandleSkillsPickerTabSwitchesToRemote(t *testing.T) {
	m := model{
		skillsPicker: &SkillsPickerState{
			Title:       "技能列表",
			ActiveTab:   "local",
			Items:       []SkillItem{{Name: "local"}},
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
			Title:     "技能列表",
			ActiveTab: "remote",
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

func TestHandleSessionPickerSearchFiltersLoadedItems(t *testing.T) {
	m := model{
		input: textarea.New(),
		sessionPicker: &SessionPickerState{
			Title:      "会话列表",
			Page:       1,
			TotalPages: 2,
			PageSize:   2,
			Items: []SessionItem{
				{ID: "session-1", Title: "alpha"},
				{ID: "session-2", Title: "beta"},
				{ID: "session-3", Title: "alpha prod"},
			},
		},
	}

	updatedModel, _ := m.handleSessionPickerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	updated := updatedModel.(model)
	if !updated.sessionPicker.SearchMode {
		t.Fatalf("expected search mode to open, got %#v", updated.sessionPicker)
	}

	updatedModel, _ = updated.handleSessionPickerSearchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("alpha")})
	updated = updatedModel.(model)
	if updated.sessionPicker.SearchQuery != "alpha" {
		t.Fatalf("expected query to update, got %#v", updated.sessionPicker)
	}
	visible := updated.visibleSessionPickerItems()
	if len(visible) != 2 {
		t.Fatalf("expected 2 visible items after filtering, got %#v", visible)
	}
	if visible[0].ID != "session-1" || visible[1].ID != "session-3" {
		t.Fatalf("unexpected filtered items: %#v", visible)
	}
	rendered := updated.renderSessionPicker()
	if !strings.Contains(rendered, "搜索: alpha_") {
		t.Fatalf("expected rendered picker to show active query, got %q", rendered)
	}
	if strings.Contains(rendered, "beta") {
		t.Fatalf("expected non-matching item to be hidden, got %q", rendered)
	}
	if updated.sessionPicker.TotalPages != 1 {
		t.Fatalf("expected total pages to collapse after filtering, got %#v", updated.sessionPicker)
	}
}

func TestHandleSessionPickerEnterUsesFilteredSelection(t *testing.T) {
	capturedSessionID := ""
	m := model{
		ctx:      context.Background(),
		snapshot: Snapshot{},
		input:    textarea.New(),
		sessionPicker: &SessionPickerState{
			Title:       "会话列表",
			Page:        1,
			TotalPages:  2,
			PageSize:    2,
			SearchQuery: "prod",
			Items: []SessionItem{
				{ID: "session-1", Title: "alpha"},
				{ID: "session-2", Title: "beta"},
				{ID: "session-3", Title: "alpha prod"},
			},
		},
		sessionChange: func(ctx context.Context, snapshot Snapshot, sessionID string) (Snapshot, error) {
			capturedSessionID = sessionID
			return snapshot, nil
		},
	}
	m.syncSessionPickerPagination()

	_, cmd := m.handleSessionPickerKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected session change command")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", cmd())
	}
	foundSessionChanged := false
	for _, item := range batch {
		if item == nil {
			continue
		}
		if _, ok := item().(sessionChangedMsg); ok {
			foundSessionChanged = true
			break
		}
	}
	if !foundSessionChanged {
		t.Fatal("expected batch to include sessionChangedMsg")
	}
	if capturedSessionID != "session-3" {
		t.Fatalf("expected filtered session to be selected, got %q", capturedSessionID)
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

func TestSkillsPickerOverlayUsesMaximumScreenSize(t *testing.T) {
	m := model{
		ready:        true,
		width:        120,
		height:       32,
		contentWidth: 100,
		skillsPicker: &SkillsPickerState{
			Title:       "技能列表",
			ActiveTab:   "remote",
			RemoteItems: []RemoteSkillItem{},
		},
	}
	for index := 1; index <= 40; index++ {
		m.skillsPicker.RemoteItems = append(m.skillsPicker.RemoteItems, RemoteSkillItem{
			Slug:        "remote-skill-" + strconv.Itoa(index),
			DisplayName: "Remote Skill " + strconv.Itoa(index),
			Summary:     "远程技能摘要 " + strconv.Itoa(index),
		})
	}

	rendered := sanitizeTerminalText(m.renderOverlay())
	if got := lipgloss.Width(rendered); got != m.width {
		t.Fatalf("expected skills picker overlay width to use full screen width %d, got %d in %q", m.width, got, rendered)
	}
	if got := lipgloss.Height(rendered); got != m.height {
		t.Fatalf("expected skills picker overlay height to use full screen height %d, got %d in %q", m.height, got, rendered)
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
	assertRenderedMaxLineWidth(t, rendered, m.toolPanelContentWidth())
}

func TestToolPanelOverlayUsesMaximumScreenSize(t *testing.T) {
	bodyLines := make([]string, 0, 80)
	for index := 1; index <= 80; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	m := model{
		ready:             true,
		width:             120,
		height:            40,
		contentWidth:      100,
		input:             textarea.New(),
		toolPanelViewport: viewport.New(0, 0),
	}
	m.openToolPanel(&ToolPanel{
		Title:   "远程技能详情｜Remote Skill",
		Summary: "用于验证技能详情窗口最大化。",
		Body:    strings.Join(bodyLines, "\n"),
		Hint:    "Esc 返回上层技能列表。",
		Actions: []ToolPanelAction{{Key: "i", Label: "安装", Command: "/skills-picker install remote-skill"}},
	})

	if m.toolPanelViewport.Height <= 24 {
		t.Fatalf("expected maximized tool panel viewport height to exceed old 24-line cap, got %d", m.toolPanelViewport.Height)
	}
	rendered := sanitizeTerminalText(m.renderOverlay())
	if got := lipgloss.Width(rendered); got != m.width {
		t.Fatalf("expected tool panel overlay width to use full screen width %d, got %d in %q", m.width, got, rendered)
	}
	if got := lipgloss.Height(rendered); got != m.height {
		t.Fatalf("expected tool panel overlay height to use full screen height %d, got %d in %q", m.height, got, rendered)
	}
}

func TestRenderOverlayKeepsToolPanelScrollInsideViewport(t *testing.T) {
	bodyLines := make([]string, 0, 32)
	for index := 1; index <= 32; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	m := model{
		contentWidth:      80,
		height:            24,
		toolPanelViewport: viewport.New(0, 0),
		toolPanel: &ToolPanel{
			Title:   "远程技能详情",
			Summary: "用于验证滚动时只在正文区域内滚动。",
			Body:    strings.Join(bodyLines, "\n"),
			Hint:    "Esc 关闭详情。",
		},
	}

	m.syncToolPanelViewport()
	renderedPanel := m.renderToolPanel()
	if got, limit := strings.Count(renderedPanel, "\n")+1, m.overlayVisibleLineLimit(); got > limit {
		t.Fatalf("expected tool panel lines to fit overlay limit, got %d > %d", got, limit)
	}
	renderedOverlay := sanitizeTerminalText(m.renderOverlay())
	if strings.Contains(renderedOverlay, "↑ 上方还有") || strings.Contains(renderedOverlay, "↓ 下方还有") {
		t.Fatalf("expected tool panel to avoid outer overlay clipping hints, got %q", renderedOverlay)
	}
}

func TestReopenToolPanelAfterScrollKeepsLayoutStable(t *testing.T) {
	bodyLines := make([]string, 0, 40)
	for index := 1; index <= 40; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	m := model{
		contentWidth:      80,
		height:            24,
		input:             textarea.New(),
		toolPanelViewport: viewport.New(0, 0),
		skillsPicker: &SkillsPickerState{
			Title:     "技能列表",
			ActiveTab: "remote",
			RemoteItems: []RemoteSkillItem{{
				Slug:        "remote-skill",
				DisplayName: "Remote Skill",
			}},
		},
	}
	panel := &ToolPanel{
		Title:   "远程技能详情",
		Summary: "用于验证第二次打开后仍保持稳定布局。",
		Body:    strings.Join(bodyLines, "\n"),
		Hint:    "Esc 关闭详情。",
	}

	m.openToolPanel(panel)
	m.toolPanelViewport.HalfViewDown()
	m.closeToolPanel()
	m.openToolPanel(panel)

	if m.toolPanelViewport.YOffset != 0 {
		t.Fatalf("expected reopened tool panel viewport to reset to top, got %d", m.toolPanelViewport.YOffset)
	}
	renderedOverlay := sanitizeTerminalText(m.renderOverlay())
	if strings.Contains(renderedOverlay, "↑ 上方还有") || strings.Contains(renderedOverlay, "↓ 下方还有") {
		t.Fatalf("expected reopened tool panel to avoid outer overlay clipping hints, got %q", renderedOverlay)
	}
	if !strings.Contains(renderedOverlay, "第1行远程技能详情内容") {
		t.Fatalf("expected reopened tool panel to show top content, got %q", renderedOverlay)
	}
	if strings.Contains(renderedOverlay, "第40行远程技能详情内容") {
		t.Fatalf("expected reopened tool panel not to jump near bottom, got %q", renderedOverlay)
	}
}

func TestToolPanelOverlayHeightStaysStableAcrossScrollAndReopen(t *testing.T) {
	bodyLines := make([]string, 0, 40)
	for index := 1; index <= 40; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	panel := &ToolPanel{
		Title:   "远程技能详情",
		Summary: "用于验证滚动时详情窗口高度保持稳定。",
		Body:    strings.Join(bodyLines, "\n"),
		Hint:    "Esc 关闭详情。",
	}
	m := model{
		ready:             true,
		width:             100,
		height:            28,
		contentWidth:      80,
		input:             textarea.New(),
		toolPanelViewport: viewport.New(0, 0),
	}

	m.openToolPanel(panel)
	firstHeight := lipgloss.Height(sanitizeTerminalText(m.renderOverlay()))
	m.toolPanelViewport.HalfViewDown()
	scrolledHeight := lipgloss.Height(sanitizeTerminalText(m.renderOverlay()))
	m.closeToolPanel()
	m.openToolPanel(panel)
	reopenedHeight := lipgloss.Height(sanitizeTerminalText(m.renderOverlay()))

	if firstHeight != scrolledHeight || firstHeight != reopenedHeight {
		t.Fatalf("expected overlay height to remain stable, got first=%d scrolled=%d reopened=%d", firstHeight, scrolledHeight, reopenedHeight)
	}
}

func TestToolPanelOverlayHeightStaysStableNearBottom(t *testing.T) {
	bodyLines := make([]string, 0, 16)
	for index := 1; index <= 16; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	m := model{
		ready:             true,
		width:             100,
		height:            24,
		contentWidth:      80,
		input:             textarea.New(),
		toolPanelViewport: viewport.New(0, 0),
		toolPanel: &ToolPanel{
			Title:   "远程技能详情",
			Summary: "用于验证靠近底部时窗口高度保持稳定。",
			Body:    strings.Join(bodyLines, "\n"),
			Hint:    "Esc 关闭详情。",
		},
	}

	m.syncToolPanelViewport()
	firstHeight := lipgloss.Height(m.renderOverlay())
	m.toolPanelViewport.HalfViewDown()
	m.toolPanelViewport.HalfViewDown()
	nearBottomHeight := lipgloss.Height(m.renderOverlay())

	if firstHeight != nearBottomHeight {
		t.Fatalf("expected overlay height to remain stable near bottom, got first=%d nearBottom=%d", firstHeight, nearBottomHeight)
	}
}

func TestRenderToolPanelNormalizesCRLFBody(t *testing.T) {
	m := model{
		contentWidth:      80,
		height:            24,
		toolPanelViewport: viewport.New(0, 0),
		toolPanel: &ToolPanel{
			Title:   "远程技能详情",
			Summary: "用于验证 CRLF 正文不会把终端布局打坏。",
			Body:    "说明区\n\nSKILL.md 内容:\r\n第一行\r\n第二行\r\n第三行",
			Hint:    "Esc 关闭详情。",
		},
	}

	m.syncToolPanelViewport()
	rendered := m.renderOverlay()
	if strings.Contains(rendered, "\r") {
		t.Fatalf("expected tool panel render to normalize CRLF, got %q", rendered)
	}
}

func TestRenderToolPanelBodyBlockStaysStableAfterMultipleScrolls(t *testing.T) {
	bodyLines := make([]string, 0, 18)
	for index := 1; index <= 18; index++ {
		line := "第" + strconv.Itoa(index) + "行远程技能详情内容"
		if index >= 12 {
			line += strings.Repeat("超长片段", 8)
		}
		bodyLines = append(bodyLines, line)
	}
	m := model{
		ready:             true,
		width:             100,
		height:            24,
		contentWidth:      80,
		toolPanelViewport: viewport.New(0, 0),
		toolPanel: &ToolPanel{
			Title:   "远程技能详情",
			Summary: "用于验证正文块在多次滚动后保持固定宽高。",
			Body:    strings.Join(bodyLines, "\n"),
			Hint:    "Esc 关闭详情。",
		},
	}

	m.syncToolPanelViewport()
	firstRendered := sanitizeTerminalText(m.renderToolPanel())
	m.toolPanelViewport.HalfViewDown()
	m.toolPanelViewport.HalfViewDown()
	afterScrollRendered := sanitizeTerminalText(m.renderToolPanel())

	assertRenderedMaxLineWidth(t, firstRendered, m.toolPanelContentWidth())
	assertRenderedMaxLineWidth(t, afterScrollRendered, m.toolPanelContentWidth())
	if lipgloss.Height(firstRendered) != lipgloss.Height(afterScrollRendered) {
		t.Fatalf("expected tool panel height to stay stable after multiple scrolls, got first=%d after=%d", lipgloss.Height(firstRendered), lipgloss.Height(afterScrollRendered))
	}
}

func TestRemoteDetailReopenAndScrollSequenceKeepsOverlayStable(t *testing.T) {
	bodyLines := make([]string, 0, 28)
	for index := 1; index <= 28; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	panel := &ToolPanel{
		Title:   "远程技能详情｜Remote Skill",
		Summary: "用于验证远程详情反复打开和滚动后的窗口稳定性。",
		Body:    strings.Join(bodyLines, "\n"),
		Hint:    "Esc 返回上层技能列表。",
	}
	m := model{
		ready:             true,
		width:             100,
		height:            24,
		contentWidth:      80,
		input:             textarea.New(),
		toolPanelViewport: viewport.New(0, 0),
		skillsPicker: &SkillsPickerState{
			Title:     "技能列表",
			ActiveTab: "remote",
			RemoteItems: []RemoteSkillItem{{
				Slug:        "remote-skill",
				DisplayName: "Remote Skill",
			}},
		},
	}

	m.openToolPanel(panel)
	for index := 0; index < 3; index++ {
		m.toolPanelViewport.LineDown(1)
	}
	firstScrolled := sanitizeTerminalText(m.renderOverlay())
	firstHeight := lipgloss.Height(firstScrolled)
	m.closeToolPanel()
	m.openToolPanel(panel)
	for index := 0; index < 3; index++ {
		m.toolPanelViewport.LineDown(1)
	}
	secondScrolled := sanitizeTerminalText(m.renderOverlay())
	secondHeight := lipgloss.Height(secondScrolled)

	if firstHeight != secondHeight {
		t.Fatalf("expected overlay height to stay stable across reopen sequence, got first=%d second=%d", firstHeight, secondHeight)
	}
	assertRenderedMaxLineWidth(t, firstScrolled, m.width)
	assertRenderedMaxLineWidth(t, secondScrolled, m.width)
	if strings.Contains(secondScrolled, "↑ 上方还有") || strings.Contains(secondScrolled, "↓ 下方还有") {
		t.Fatalf("expected reopened remote detail to avoid outer clipping hints, got %q", secondScrolled)
	}
}

func TestMouseWheelScrollsToolPanelViewport(t *testing.T) {
	bodyLines := make([]string, 0, 30)
	for index := 1; index <= 30; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	m := model{
		ready:              true,
		width:              100,
		height:             24,
		contentWidth:       80,
		mouseScrollEnabled: true,
		toolPanelViewport:  viewport.New(0, 0),
		toolPanel: &ToolPanel{
			Title:   "远程技能详情",
			Summary: "用于验证鼠标滚轮滚动详情正文。",
			Body:    strings.Join(bodyLines, "\n"),
			Hint:    "Esc 返回上层技能列表。",
		},
	}
	m.syncToolPanelViewport()

	updatedModel, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	updated := updatedModel.(model)
	if updated.toolPanelViewport.YOffset == 0 {
		t.Fatalf("expected mouse wheel down to scroll tool panel viewport, got %d", updated.toolPanelViewport.YOffset)
	}

	updatedModel, _ = updated.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	updated = updatedModel.(model)
	if updated.toolPanelViewport.YOffset != 0 {
		t.Fatalf("expected mouse wheel up to scroll tool panel viewport back to top, got %d", updated.toolPanelViewport.YOffset)
	}
}

func TestMouseWheelDoesNotScrollWhenMouseScrollModeDisabled(t *testing.T) {
	bodyLines := make([]string, 0, 30)
	for index := 1; index <= 30; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	m := model{
		ready:             true,
		width:             100,
		height:            24,
		contentWidth:      80,
		toolPanelViewport: viewport.New(0, 0),
		toolPanel: &ToolPanel{
			Title:   "远程技能详情",
			Summary: "用于验证未开启鼠标滚动时仍可选字。",
			Body:    strings.Join(bodyLines, "\n"),
			Hint:    "Esc 返回上层技能列表。",
		},
	}
	m.syncToolPanelViewport()

	updatedModel, _ := m.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	updated := updatedModel.(model)
	if updated.toolPanelViewport.YOffset != 0 {
		t.Fatalf("expected mouse wheel to be ignored when mouse scroll mode is disabled, got %d", updated.toolPanelViewport.YOffset)
	}
}

func TestMouseResidueKeyScrollsToolPanelViewport(t *testing.T) {
	bodyLines := make([]string, 0, 30)
	for index := 1; index <= 30; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	m := model{
		ready:              true,
		width:              100,
		height:             24,
		contentWidth:       80,
		mouseScrollEnabled: true,
		toolPanelViewport:  viewport.New(0, 0),
		toolPanel: &ToolPanel{
			Title:   "远程技能详情",
			Summary: "用于验证鼠标残片仍能驱动滚动。",
			Body:    strings.Join(bodyLines, "\n"),
			Hint:    "Esc 返回上层技能列表。",
		},
	}
	m.syncToolPanelViewport()

	updatedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<65;53;22M[<65;53;22M")})
	updated := updatedModel.(model)
	if updated.toolPanelViewport.YOffset != 6 {
		t.Fatalf("expected duplicated mouse residue to scroll tool panel by 6 lines, got %d", updated.toolPanelViewport.YOffset)
	}
	if got := updated.input.Value(); got != "" {
		t.Fatalf("expected mouse residue not to pollute input, got %q", got)
	}
}

func TestMouseResidueKeyDoesNotInsertIntoMainInput(t *testing.T) {
	lines := make([]string, 0, 40)
	for index := 1; index <= 40; index++ {
		lines = append(lines, "第"+strconv.Itoa(index)+"行主视图内容")
	}
	input := textarea.New()
	input.Prompt = "> "
	m := model{
		ready:              true,
		width:              100,
		height:             12,
		contentWidth:       80,
		mouseScrollEnabled: true,
		input:              input,
		viewport:           viewport.New(0, 0),
		entries:            []entry{{role: "assistant", content: strings.Join(lines, "\n")}},
	}
	m.syncLayout()
	m.viewport.GotoTop()
	initialOffset := m.viewport.YOffset

	updatedModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<65;53;22M")})
	updated := updatedModel.(model)
	if got := updated.input.Value(); got != "" {
		t.Fatalf("expected mouse residue not to be inserted into main input, got %q", got)
	}
	if updated.viewport.YOffset <= initialOffset {
		t.Fatalf("expected mouse residue to scroll main viewport downward, initial=%d updated=%d", initialOffset, updated.viewport.YOffset)
	}
}

func TestViewPlacesToolPanelOverlayNearTop(t *testing.T) {
	m := model{
		ready:        true,
		width:        100,
		height:       24,
		contentWidth: 80,
		input:        textarea.New(),
		toolPanel: &ToolPanel{
			Title:   "远程技能详情",
			Summary: "用于验证详情面板不会垂直居中漂移。",
			Body:    "第1行\n第2行\n第3行\n第4行",
			Hint:    "Esc 返回上层技能列表。",
		},
		toolPanelViewport: viewport.New(0, 0),
	}
	m.syncToolPanelViewport()

	rendered := sanitizeTerminalText(m.View())
	if got := firstBorderLineIndex(rendered); got != 0 {
		t.Fatalf("expected tool panel overlay to start at top border line, got %d in %q", got, rendered)
	}
}

func TestToolPanelViewDoesNotExceedScreenHeightAfterReopenAndScroll(t *testing.T) {
	bodyLines := make([]string, 0, 40)
	for index := 1; index <= 40; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	m := model{
		ready:             true,
		width:             100,
		height:            24,
		contentWidth:      80,
		input:             textarea.New(),
		toolPanelViewport: viewport.New(0, 0),
		skillsPicker: &SkillsPickerState{
			Title:     "技能列表",
			ActiveTab: "remote",
			RemoteItems: []RemoteSkillItem{{
				Slug:        "remote-skill",
				DisplayName: "Remote Skill",
			}},
		},
	}
	panel := &ToolPanel{
		Title:   "远程技能详情｜Remote Skill",
		Summary: "用于验证重开并滚动后整屏高度不会超过终端高度。",
		Body:    strings.Join(bodyLines, "\n"),
		Hint:    "Esc 返回上层技能列表。",
	}

	m.openToolPanel(panel)
	for index := 0; index < 4; index++ {
		m.toolPanelViewport.LineDown(1)
	}
	m.closeToolPanel()
	m.openToolPanel(panel)
	for index := 0; index < 4; index++ {
		m.toolPanelViewport.LineDown(1)
	}

	rendered := sanitizeTerminalText(m.View())
	if got := lipgloss.Height(rendered); got > m.height {
		t.Fatalf("expected view height <= screen height %d, got %d", m.height, got)
	}
}

func TestToolPanelTopBorderStaysStableAcrossReopenAndPagedScroll(t *testing.T) {
	bodyLines := []string{
		"Slug: remote-skill",
		"显示名称: Remote Skill",
		"摘要: 用于验证远程技能详情反复打开与滚动后的边框稳定性。",
		"最新版本: 1.2.3",
		"发布者: Taiyi",
		"支持系统: darwin, linux",
		"适用环境: cli, vscode",
		"仓库: default",
		"",
		"SKILL.md 内容:",
	}
	for index := 1; index <= 48; index++ {
		line := "- 第" + strconv.Itoa(index) + "行技能说明"
		if index%5 == 0 {
			line += strings.Repeat(" 超长片段", 10)
		}
		bodyLines = append(bodyLines, line)
	}
	panel := &ToolPanel{
		Title:   "远程技能详情｜Remote Skill",
		Summary: "用于验证第二次打开后多次分页滚动不会把整个窗口往上顶。",
		Body:    strings.Join(bodyLines, "\n"),
		Hint:    "Esc 返回上层技能列表。",
		Actions: []ToolPanelAction{{Key: "i", Label: "安装", Command: "/skills-picker install remote-skill"}},
	}
	m := model{
		ready:             true,
		width:             100,
		height:            24,
		contentWidth:      80,
		input:             textarea.New(),
		toolPanelViewport: viewport.New(0, 0),
		skillsPicker: &SkillsPickerState{
			Title:     "技能列表",
			ActiveTab: "remote",
			RemoteItems: []RemoteSkillItem{{
				Slug:        "remote-skill",
				DisplayName: "Remote Skill",
			}},
		},
	}

	m.openToolPanel(panel)
	baseline := firstBorderLineIndex(sanitizeTerminalText(m.View()))
	if baseline < 0 {
		t.Fatalf("expected initial tool panel view to contain a border, got %q", sanitizeTerminalText(m.View()))
	}

	for index := 0; index < 3; index++ {
		m.toolPanelViewport.HalfViewDown()
	}
	m.closeToolPanel()
	m.openToolPanel(panel)

	for index := 0; index < 6; index++ {
		m.toolPanelViewport.HalfViewDown()
		rendered := sanitizeTerminalText(m.View())
		if got := firstBorderLineIndex(rendered); got != baseline {
			t.Fatalf("expected top border line to stay at %d after reopen scroll %d, got %d in %q", baseline, index+1, got, rendered)
		}
	}
}

func TestRenderToolPanelKeepsOverlayLimitWithMultilineSummary(t *testing.T) {
	bodyLines := make([]string, 0, 24)
	for index := 1; index <= 24; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行远程技能详情内容")
	}
	m := model{
		contentWidth:      80,
		height:            24,
		toolPanelViewport: viewport.New(0, 0),
		toolPanel: &ToolPanel{
			Title:   "远程技能详情",
			Summary: "第一行摘要\n第二行摘要\n第三行摘要",
			Body:    strings.Join(bodyLines, "\n"),
			Hint:    "Esc 返回上层技能列表。",
			Actions: []ToolPanelAction{{Key: "i", Label: "安装", Command: "/skills-picker install remote-skill"}},
		},
	}

	m.syncToolPanelViewport()
	renderedPanel := sanitizeTerminalText(m.renderToolPanel())
	if got, limit := strings.Count(renderedPanel, "\n")+1, m.overlayVisibleLineLimit(); got > limit {
		t.Fatalf("expected multiline summary tool panel lines to fit overlay limit, got %d > %d with %q", got, limit, renderedPanel)
	}
}

func TestToolPanelCapsVerboseRemoteSummary(t *testing.T) {
	summaryLines := make([]string, 0, 32)
	for index := 1; index <= 32; index++ {
		summaryLines = append(summaryLines, "第"+strconv.Itoa(index)+"行远程技能摘要")
	}
	bodyLines := make([]string, 0, 32)
	for index := 1; index <= 32; index++ {
		bodyLines = append(bodyLines, "第"+strconv.Itoa(index)+"行 SKILL.md 内容")
	}
	m := model{
		ready:             true,
		width:             100,
		height:            18,
		contentWidth:      80,
		input:             textarea.New(),
		toolPanelViewport: viewport.New(0, 0),
		toolPanel: &ToolPanel{
			Title:   "远程技能详情",
			Summary: strings.Join(summaryLines, "\n"),
			Body:    strings.Join(bodyLines, "\n"),
			Hint:    "Esc 返回上层技能列表。",
			Actions: []ToolPanelAction{{Key: "i", Label: "安装", Command: "/skills-picker install remote-skill"}},
		},
	}

	m.syncToolPanelViewport()
	for index := 0; index < 4; index++ {
		m.toolPanelViewport.HalfViewDown()
	}
	renderedPanel := sanitizeTerminalText(m.renderToolPanel())
	if got, limit := strings.Count(renderedPanel, "\n")+1, m.toolPanelContentHeight(); got > limit {
		t.Fatalf("expected verbose summary panel content height <= %d, got %d in %q", limit, got, renderedPanel)
	}
	renderedView := sanitizeTerminalText(m.View())
	if got := lipgloss.Height(renderedView); got > m.height {
		t.Fatalf("expected verbose summary view height <= screen height %d, got %d", m.height, got)
	}
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

func firstBorderLineIndex(rendered string) int {
	for index, line := range strings.Split(rendered, "\n") {
		if strings.ContainsRune(line, '┏') || strings.ContainsRune(line, '┌') || strings.ContainsRune(line, '╭') {
			return index
		}
	}
	return -1
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
					ContextUsageTokens:       3072,
					DirectMessageTokens:      2048,
					CompressedHistoryTokens:  1024,
					HistoryMessageCount:      12,
				},
			},
		},
	}

	got := model.inputContextUsageHint()
	if !strings.Contains(got, "2K/8K") {
		t.Fatalf("expected compact usage summary in input hint, got %q", got)
	}
	if !strings.Contains(got, "1K+2K") {
		t.Fatalf("expected compact history split summary in input hint, got %q", got)
	}
	if strings.Contains(got, "共12条消息") {
		t.Fatalf("expected history message count hidden to save width, got %q", got)
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
					ContextUsageTokens:       3072,
					DirectMessageTokens:      2048,
					CompressedHistoryTokens:  1024,
				},
			},
		},
	}

	got := model.inputContextUsageHint()
	if strings.Contains(got, "7 ·") {
		t.Fatalf("expected draft token estimate removed to save width, got %q", got)
	}
	if !strings.Contains(got, "2K/8K") {
		t.Fatalf("expected compact usage summary in input hint, got %q", got)
	}
	if !strings.Contains(got, "1K+2K") {
		t.Fatalf("expected compact history split summary in input hint, got %q", got)
	}
}

func TestFormatCompactIntSupportsMUnits(t *testing.T) {
	if got := formatCompactInt(999); got != "999" {
		t.Fatalf("expected plain format for <1000, got %q", got)
	}
	if got := formatCompactInt(1200); got != "1K" {
		t.Fatalf("expected K format, got %q", got)
	}
	if got := formatCompactInt(12500); got != "13K" {
		t.Fatalf("expected integer K format, got %q", got)
	}
	if got := formatCompactInt(1300000); got != "1M" {
		t.Fatalf("expected decimal M format, got %q", got)
	}
	if got := formatCompactInt(12000000); got != "12M" {
		t.Fatalf("expected integer M format, got %q", got)
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

func TestRenderStatusBarShowsCurrentUser(t *testing.T) {
	model := model{
		snapshot: Snapshot{
			BackendAvailable: true,
			RequestMode:      "agent",
			ApprovalMode:     "confirm",
			CurrentUserName:  "张三",
		},
		state:        newProgressState(progressIdle),
		width:        120,
		contentWidth: 120,
	}

	got := sanitizeTerminalText(model.renderStatusBar())
	if !strings.Contains(got, "张三") {
		t.Fatalf("expected current user segment in status bar, got %q", got)
	}
}

func TestRenderStatusBarShowsAuthRequired(t *testing.T) {
	model := model{
		snapshot: Snapshot{
			BackendAvailable: true,
			AuthRequired:     true,
			RequestMode:      "agent",
			ApprovalMode:     "confirm",
		},
		state:        newProgressState(progressIdle),
		width:        120,
		contentWidth: 120,
	}

	got := sanitizeTerminalText(model.renderStatusBar())
	if !strings.Contains(got, "当前未登录") {
		t.Fatalf("expected auth required segment in status bar, got %q", got)
	}
}

func TestBuildBackendStatusEntriesUsesLoginOverlayHint(t *testing.T) {
	entries := buildBackendStatusEntries(Snapshot{
		BackendAvailable: true,
		AuthRequired:     true,
		AuthStatusReason: "服务端已连接，但当前登录态无效或缺失。登录表单已自动打开，也可手动执行 /login。",
	})
	if len(entries) != 1 {
		t.Fatalf("expected one backend status entry, got %#v", entries)
	}
	if !strings.Contains(entries[0].content, "登录表单已自动打开") {
		t.Fatalf("expected login overlay hint in backend status entry, got %q", entries[0].content)
	}
	if strings.Count(entries[0].content, "/login") != 1 {
		t.Fatalf("expected backend status entry to mention /login once, got %q", entries[0].content)
	}
	if strings.Contains(entries[0].content, "/login qr") || strings.Contains(entries[0].content, "/login password") {
		t.Fatalf("expected backend status entry to stop repeating legacy login commands, got %q", entries[0].content)
	}
}

func TestSyncInputPlaceholderUsesLoginOverlayHint(t *testing.T) {
	input := textarea.New()
	appModel := model{
		snapshot: Snapshot{BackendAvailable: true, AuthRequired: true},
		input:    input,
	}
	appModel.syncInputPlaceholder()
	if got := appModel.input.Placeholder; !strings.Contains(got, "登录表单已自动打开") {
		t.Fatalf("expected auth-required placeholder to reference login overlay, got %q", got)
	}
	if strings.Contains(appModel.input.Placeholder, "/login qr") || strings.Contains(appModel.input.Placeholder, "/login password") {
		t.Fatalf("expected auth-required placeholder to stop repeating legacy login commands, got %q", appModel.input.Placeholder)
	}
}

func TestRenderStatusBarShowsFooterHint(t *testing.T) {
	model := model{
		snapshot: Snapshot{
			BackendAvailable: true,
			RequestMode:      "agent",
			ApprovalMode:     "confirm",
			CurrentUserName:  "张三",
		},
		state:        newProgressState(progressIdle),
		width:        120,
		contentWidth: 120,
		footerHint:   "张三(已登录)",
	}

	got := sanitizeTerminalText(model.renderStatusBar())
	if !strings.Contains(got, "张三(已登录)") {
		t.Fatalf("expected footer hint in status bar, got %q", got)
	}
}

func TestRenderStatusBarShowsMouseModeShortcut(t *testing.T) {
	model := model{
		snapshot: Snapshot{
			BackendAvailable: true,
			RequestMode:      "agent",
			ApprovalMode:     "confirm",
		},
		state:        newProgressState(progressIdle),
		width:        140,
		contentWidth: 140,
	}

	got := sanitizeTerminalText(model.renderStatusBar())
	if !strings.Contains(got, "F6选字") {
		t.Fatalf("expected default mouse selection shortcut in status bar, got %q", got)
	}

	model.mouseScrollEnabled = true
	got = sanitizeTerminalText(model.renderStatusBar())
	if !strings.Contains(got, "F6滚动") {
		t.Fatalf("expected mouse scroll shortcut in status bar after toggle, got %q", got)
	}
}

func TestRenderStatusBarUsesCompactLeftStatusLabels(t *testing.T) {
	model := model{
		snapshot: Snapshot{
			BackendAvailable:         true,
			RequestMode:              "agent",
			ApprovalMode:             "auto",
			ReasoningEnabled:         true,
			InternetSearchSupported:  true,
			InternetSearchEnabled:    true,
			KnowledgeSearchSupported: true,
			KnowledgeBases:           []KnowledgeBaseItem{{Code: "kb-a", Name: "知识库A"}},
			CurrentUserName:          "默认管理员",
		},
		state:        newProgressState(progressIdle),
		width:        200,
		contentWidth: 200,
	}

	got := sanitizeTerminalText(model.renderStatusBar())
	for _, expected := range []string{"执行", "自动", "思考开", "F6选字", "联网 / 知识库", "默认管理员"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected compact status label %q in status bar, got %q", expected, got)
		}
	}
	for _, unexpected := range []string{"执行模式", "自动执行任何命令", "思考模式已开", "鼠标选字/F6", "联网搜索开启", "自动选择知识库", "admin"} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("expected verbose status label %q removed from status bar, got %q", unexpected, got)
		}
	}
}

func TestRenderStatusBarHidesDisabledCapabilityLabels(t *testing.T) {
	model := model{
		snapshot: Snapshot{
			BackendAvailable:         true,
			RequestMode:              "agent",
			ApprovalMode:             "confirm",
			InternetSearchSupported:  true,
			InternetSearchEnabled:    false,
			KnowledgeSearchSupported: true,
		},
		state:        newProgressState(progressIdle),
		width:        160,
		contentWidth: 160,
	}

	got := sanitizeTerminalText(model.renderStatusBar())
	for _, unexpected := range []string{"Web", "WebOff", "AutoKB", "NoKB", "联网", "知识库"} {
		if strings.Contains(got, unexpected) {
			t.Fatalf("expected disabled capability label %q to be hidden, got %q", unexpected, got)
		}
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
	if !strings.Contains(got, "2K/8K") {
		t.Fatalf("expected compact context hint on the right side of status bar, got %q", got)
	}
}

func TestAssistantChunkClearsCompressionFooterHint(t *testing.T) {
	model := model{
		width:        120,
		contentWidth: 120,
		requestStats: RequestMetrics{TokenUsage: &TokenUsage{ContextWindow: &ContextWindow{
			MaxInputTokens:           8192,
			CurrentRoundInputTokens:  2048,
			NextRoundAvailableTokens: 6144,
		}}},
	}

	model.applyEvent(Event{Kind: EventProgressNote, Text: "开始进行上下文压缩", EventType: "compression_progress"})
	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "已恢复输出", MessageID: "msg-1"})

	if strings.TrimSpace(model.footerHint) != "" {
		t.Fatalf("expected assistant chunk to clear footer compression hint, got %q", model.footerHint)
	}
	if model.compressionInProgress {
		t.Fatalf("expected assistant chunk to release compression lock")
	}
	model.applyEvent(Event{Kind: EventProgressNote, Text: "上下文压缩完成：320 -> 160", EventType: "compression_progress"})
	if model.compressionInProgress {
		t.Fatalf("expected later compression messages to be ignored after first assistant chunk")
	}
	if got := sanitizeTerminalText(model.renderStatusBar()); strings.Contains(got, "正在压缩上下文") {
		t.Fatalf("expected compression hint to stay cleared after later compression messages, got %q", got)
	}
}

func TestReasoningChunkClearsCompressionFooterHint(t *testing.T) {
	model := model{
		width:        120,
		contentWidth: 120,
		requestStats: RequestMetrics{TokenUsage: &TokenUsage{ContextWindow: &ContextWindow{
			MaxInputTokens:           8192,
			CurrentRoundInputTokens:  2048,
			NextRoundAvailableTokens: 6144,
		}}},
	}

	model.applyEvent(Event{Kind: EventProgressNote, Text: "开始进行上下文压缩", EventType: "compression_progress"})
	model.applyEvent(Event{Kind: EventReasoningChunk, Text: "先分析一下", MessageID: "msg-1"})

	if model.compressionInProgress {
		t.Fatalf("expected reasoning chunk to release compression lock")
	}
	model.applyEvent(Event{Kind: EventProgressNote, Text: "上下文压缩完成：320 -> 160", EventType: "compression_progress"})
	if model.compressionInProgress {
		t.Fatalf("expected later compression messages to be ignored after first reasoning chunk")
	}
	if got := sanitizeTerminalText(model.renderStatusBar()); strings.Contains(got, "正在压缩上下文") {
		t.Fatalf("expected compression hint cleared after reasoning chunk, got %q", got)
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

func TestApplyEventStatsUpdatePreservesExistingContextWindow(t *testing.T) {
	model := model{
		requestStats: RequestMetrics{
			TokenUsage: &TokenUsage{
				ContextWindow: &ContextWindow{
					MaxInputTokens:           8192,
					CurrentRoundInputTokens:  2048,
					NextRoundAvailableTokens: 6144,
					ContextUsageTokens:       3072,
				},
			},
		},
	}
	stats := &RequestMetrics{
		TokenUsage: &TokenUsage{
			InputTokens:  256,
			OutputTokens: 128,
			TotalTokens:  384,
			StopReason:   "user_interrupted",
		},
	}

	model.applyEvent(Event{Kind: EventStatsUpdate, Stats: stats})

	if model.requestStats.TokenUsage == nil || model.requestStats.TokenUsage.ContextWindow == nil {
		t.Fatalf("expected existing context window to be preserved, got %#v", model.requestStats.TokenUsage)
	}
	if got := model.requestStats.TokenUsage.ContextWindow.CurrentRoundInputTokens; got != 2048 {
		t.Fatalf("expected preserved current round input tokens, got %d", got)
	}
	if got := model.requestStats.TokenUsage.ContextWindow.NextRoundAvailableTokens; got != 6144 {
		t.Fatalf("expected preserved next round availability, got %d", got)
	}
	if got := model.requestStats.TokenUsage.TotalTokens; got != 384 {
		t.Fatalf("expected incoming token totals to still update, got %d", got)
	}
	if got := model.requestStats.TokenUsage.StopReason; got != "user_interrupted" {
		t.Fatalf("expected incoming stop reason to still update, got %q", got)
	}
	if hint := model.inputContextUsageHint(); !strings.Contains(hint, "2K/8K") {
		t.Fatalf("expected preserved footer context usage hint, got %q", hint)
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

	assistantEntries := []entry{}
	for _, item := range model.entries {
		if item.role == "assistant" {
			assistantEntries = append(assistantEntries, item)
		}
	}
	if len(assistantEntries) != 2 {
		t.Fatalf("expected two assistant entries after tool call split, got %#v", model.entries)
	}
	if assistantEntries[0].content != "第一段" || assistantEntries[1].content != "第二段" {
		t.Fatalf("unexpected split assistant content: %#v", model.entries)
	}
}

func TestReasoningChunkCreatesSeparateEntryAndMergesByMessage(t *testing.T) {
	model := model{}

	model.applyEvent(Event{Kind: EventReasoningChunk, Text: "第一段思考", MessageID: "msg-1"})
	model.applyEvent(Event{Kind: EventReasoningChunk, Text: "第二段思考", MessageID: "msg-1"})
	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "最终回答", MessageID: "msg-1"})

	if len(model.entries) != 2 {
		t.Fatalf("expected reasoning entry plus assistant entry, got %#v", model.entries)
	}
	if model.entries[0].role != "reasoning" || model.entries[0].content != "第一段思考第二段思考" {
		t.Fatalf("unexpected reasoning entry: %#v", model.entries[0])
	}
	if model.entries[1].role != "assistant" || model.entries[1].content != "最终回答" {
		t.Fatalf("unexpected assistant entry after reasoning: %#v", model.entries[1])
	}
}

func TestRewriteRetrievalCitationMarkers(t *testing.T) {
	input := "联网引用 ^1.2^，共享引用 [^2.1^]，个人引用 ^3.4^。"
	got := rewriteRetrievalCitationMarkers(input)
	want := "联网引用 [联网#2]，共享引用 [共享#1]，个人引用 [个人#4]。"
	if got != want {
		t.Fatalf("unexpected citation rewrite: got %q want %q", got, want)
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

func TestAssistantChunkSplitWhenBackendRequestsNewMessage(t *testing.T) {
	model := model{}

	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "第一段", MessageID: "msg-1"})
	model.applyEvent(Event{Kind: EventAssistantChunk, SplitAssistant: true, MessageID: "msg-1"})
	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "第二段", MessageID: "msg-1"})

	assistantEntries := []entry{}
	for _, item := range model.entries {
		if item.role == "assistant" {
			assistantEntries = append(assistantEntries, item)
		}
	}
	if len(assistantEntries) != 2 {
		t.Fatalf("expected two assistant entries after split marker, got %#v", model.entries)
	}
	if assistantEntries[0].content != "第一段" || assistantEntries[1].content != "第二段" {
		t.Fatalf("unexpected assistant content after split marker: %#v", assistantEntries)
	}
}

func TestStreamEventBatchWithPendingDoesNotStartCompetingWait(t *testing.T) {
	model := model{
		eventCh:                    make(chan tea.Msg),
		progress:                   progress.New(progress.WithDefaultGradient()),
		state:                      newProgressState(progressExecuting),
		viewport:                   viewport.Model{Width: 80, Height: 10},
		currentStreamingEntryIndex: -1,
	}

	_, cmd := model.Update(streamEventBatchMsg{
		events:  []Event{{Kind: EventAssistantChunk, Text: "最后一段", MessageID: "msg-1"}},
		pending: streamDoneMsg{},
	})

	if cmd == nil {
		t.Fatal("expected command for progress transition and pending stream completion")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", cmd())
	}
	if len(batch) != 2 {
		t.Fatalf("expected only progress transition and pending message, got %d commands", len(batch))
	}
	if _, ok := batch[1]().(streamDoneMsg); !ok {
		t.Fatalf("expected second command to deliver pending streamDoneMsg, got %T", batch[1]())
	}
}

func TestConfirmRequestDoesNotStartStreamWaitBeforeResponse(t *testing.T) {
	model := model{
		eventCh:  make(chan tea.Msg),
		input:    textarea.New(),
		progress: progress.New(progress.WithDefaultGradient()),
		state:    newProgressState(progressStreaming),
	}

	_, cmd := model.Update(confirmRequestMsg{request: ConfirmRequest{Title: "确认"}, responseCh: make(chan bool, 1)})
	if cmd == nil {
		t.Fatal("expected command for progress transition")
	}
	if batch, ok := cmd().(tea.BatchMsg); ok && len(batch) > 1 {
		t.Fatalf("expected no competing waitForStream while confirm blocks the runner, got %d commands", len(batch))
	}
}

func TestAskUserRequestDoesNotStartStreamWaitBeforeResponse(t *testing.T) {
	model := model{
		eventCh:  make(chan tea.Msg),
		input:    textarea.New(),
		progress: progress.New(progress.WithDefaultGradient()),
		state:    newProgressState(progressStreaming),
	}

	_, cmd := model.Update(askUserRequestMsg{
		request:    clientruntime.AskUserRequest{Questions: []clientruntime.AskUserQuestion{{Name: "project", Label: "项目名称", InputFormat: "text"}}},
		responseCh: make(chan askUserResult, 1),
	})
	if cmd == nil {
		t.Fatal("expected command for progress transition and cursor blink")
	}
	if batch, ok := cmd().(tea.BatchMsg); ok && len(batch) > 2 {
		t.Fatalf("expected no competing waitForStream while ask_user blocks the runner, got %d commands", len(batch))
	}
}

func TestStaleStreamEventDoesNotAppendToCurrentRequest(t *testing.T) {
	appModel := model{
		eventCh:      make(chan tea.Msg),
		state:        newProgressState(progressStreaming),
		requestStats: RequestMetrics{RunID: "run-new"},
		entries:      []entry{{role: "assistant", content: "当前输出", messageID: "msg-new"}},
	}

	updated, _ := appModel.Update(streamEventMsg{runID: "run-old", event: Event{Kind: EventAssistantChunk, Text: "旧输出", MessageID: "msg-old"}})
	typed := updated.(model)

	if len(typed.entries) != 1 || typed.entries[0].content != "当前输出" {
		t.Fatalf("expected stale stream event to be ignored, got %#v", typed.entries)
	}
}

func TestStaleStreamBatchReturnsPendingCurrentEvent(t *testing.T) {
	appModel := model{
		eventCh:      make(chan tea.Msg),
		state:        newProgressState(progressStreaming),
		requestStats: RequestMetrics{RunID: "run-new"},
	}

	_, cmd := appModel.Update(streamEventBatchMsg{
		runID:   "run-old",
		events:  []Event{{Kind: EventAssistantChunk, Text: "旧输出", MessageID: "msg-old"}},
		pending: streamEventMsg{runID: "run-new", event: Event{Kind: EventAssistantChunk, Text: "当前输出", MessageID: "msg-new"}},
	})
	if cmd == nil {
		t.Fatal("expected pending current stream event to be returned")
	}
	msg, ok := cmd().(streamEventMsg)
	if !ok || msg.runID != "run-new" || msg.event.Text != "当前输出" {
		t.Fatalf("expected pending current stream event, got %#v", msg)
	}
}

func TestConfigFormValuesFromInputsIncludesHTTPTimeout(t *testing.T) {
	state := newConfigFormState(ConfigFormState{})
	state.inputs[10].SetValue("75")
	state.inputs[11].SetValue("45")
	state.inputs[12].SetValue("300")
	state.inputs[15].SetValue("90")

	values := configFormValuesFromInputs(state.inputs)
	if values.HTTPTimeoutSeconds != "75" {
		t.Fatalf("expected http timeout from form inputs, got %#v", values)
	}
	if values.CommandTimeoutSeconds != "45" || values.StreamIdleTimeoutSeconds != "300" || values.QRWaitTimeoutSeconds != "90" {
		t.Fatalf("unexpected timeout values from form inputs: %#v", values)
	}
	if !isConfigTextField(configFieldHTTPTimeout) {
		t.Fatal("expected http timeout field to be treated as text input")
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

func TestStartRequestStatsPreservesKnownContextAndCompressionHints(t *testing.T) {
	model := model{
		snapshot: Snapshot{SessionID: "session-123"},
		requestStats: RequestMetrics{
			TokenUsage: &TokenUsage{
				ContextWindow: &ContextWindow{
					MaxInputTokens:           8192,
					CurrentRoundInputTokens:  2048,
					NextRoundAvailableTokens: 6144,
					ContextUsageTokens:       3072,
					DirectMessageTokens:      2048,
					CompressedHistoryTokens:  1024,
				},
			},
			Compression: &CompressionStats{
				Count:            1,
				BeforeTokens:     9200,
				AfterTokens:      6000,
				RemainingTokens:  2192,
				CompressionRatio: 0.65,
			},
		},
	}

	model.startRequestStats("问题", "run-1")
	got := model.inputContextUsageHint()

	if strings.Contains(got, "正在输出...") {
		t.Fatalf("expected startRequestStats to preserve known context hint, got %q", got)
	}
	if !strings.Contains(got, "2K/8K") {
		t.Fatalf("expected preserved context availability hint, got %q", got)
	}
	// 压缩统计信息不再持久显示在 inputContextUsageHint 中，
	// 压缩后的上下文用量通过 contextUsageGrid 和正常的上下文提示反映。
	if strings.Contains(got, "压9K→6K") {
		t.Fatalf("expected compression hint to not appear persistently in inputContextUsageHint, got %q", got)
	}
	if strings.Contains(got, "余2K") {
		t.Fatalf("expected remaining token suffix removed from compression hint, got %q", got)
	}
}

func TestStartRequestAssumesCompressionWhenConversationHistoryExists(t *testing.T) {
	model := model{
		ctx:      context.Background(),
		snapshot: Snapshot{SessionID: "session-123"},
		entries: []entry{
			{role: "welcome", content: "欢迎"},
			{role: "user", content: "之前的问题"},
			{role: "assistant", content: "之前的回答"},
		},
		requestStats: RequestMetrics{TokenUsage: &TokenUsage{ContextWindow: &ContextWindow{
			MaxInputTokens:           8192,
			CurrentRoundInputTokens:  2048,
			NextRoundAvailableTokens: 6144,
		}}},
	}

	model.startRequest(Request{Input: "新的问题", RunID: "run-1"}, true)

	if !model.compressionInProgress {
		t.Fatalf("expected existing conversation history to assume compression in progress")
	}
	if got := model.inputContextUsageHint(); got != "正在压缩上下文..." {
		t.Fatalf("expected compression hint before first chunk, got %q", got)
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

func TestUpdateStreamDoneDoesNotCleanFinishedTasks(t *testing.T) {
	originalCleanup := cleanupFinishedLocalTasks
	cleanupCalls := 0
	cleanupFinishedLocalTasks = func() {
		cleanupCalls++
	}
	defer func() {
		cleanupFinishedLocalTasks = originalCleanup
	}()

	m := model{
		snapshot: Snapshot{BackendAvailable: true},
		state:    newProgressState(progressStreaming),
		input:    textarea.New(),
		viewport: viewport.New(80, 20),
	}

	updated, _ := m.Update(streamDoneMsg{})
	typed, ok := updated.(model)
	if !ok {
		t.Fatalf("expected model type after stream done, got %T", updated)
	}
	// stream done 不再触发清理，任务保留供智能体查询
	if cleanupCalls != 0 {
		t.Fatalf("expected stream done to NOT cleanup finished tasks, got %d calls", cleanupCalls)
	}
	if typed.state.phase != progressIdle {
		t.Fatalf("expected progress idle after stream done, got %s", typed.state.phase)
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
	originalCleanup := cleanupFinishedLocalTasks
	cleanupCalls := 0
	cleanupFinishedLocalTasks = func() {
		cleanupCalls++
	}
	defer func() {
		cleanupFinishedLocalTasks = originalCleanup
	}()

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
	if cleanupCalls != 0 {
		t.Fatalf("expected stream failed to NOT cleanup finished tasks, got %d calls", cleanupCalls)
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

func TestApplyEventToolCallDoesNotInjectTimelineNote(t *testing.T) {
	m := model{}

	m.applyEvent(Event{Kind: EventToolCall, Text: "读取文件"})
	m.applyEvent(Event{Kind: EventBuiltInCall, Text: "git status"})
	m.applyEvent(Event{Kind: EventClientToolCall, Text: "read_file notes.txt"})

	if len(m.entries) != 0 {
		t.Fatalf("expected no timeline entries for tool calls, got %#v", m.entries)
	}
	if m.currentToolLabel == "" {
		t.Fatal("expected currentToolLabel to be set for the last tool call")
	}
}

func TestIsMarkdownTableRow(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"| a | b | c |", true},
		{"|---|---|---|", true},
		{"| 列1 | 列2 | 列3 |", true},
		{"  | indented | table |  ", true},
		{"| single |", true},
		{"|", false},
		{"", false},
		{"not a table", false},
		{"| incomplete", false},
		{"incomplete |", false},
		{"hello | world", false},
	}
	for _, tc := range cases {
		got := isMarkdownTableRow(tc.line)
		if got != tc.want {
			t.Errorf("isMarkdownTableRow(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestHardWrapTextPreservesMarkdownTableRows(t *testing.T) {
	tableContent := "| 名称 | 状态 | 详细描述信息 |\n|------|------|-------------|\n| 项目A | 运行中 | 这是一个很长的描述内容 |\n| 项目B | 已停止 | 另一个很长的描述内容 |"

	// Wrap at a narrow width that would normally break table rows.
	wrapped := hardWrapText(tableContent, 20)
	lines := strings.Split(wrapped, "\n")

	// Each table row should be preserved intact, not split.
	expectedRows := []string{
		"| 名称 | 状态 | 详细描述信息 |",
		"|------|------|-------------|",
		"| 项目A | 运行中 | 这是一个很长的描述内容 |",
		"| 项目B | 已停止 | 另一个很长的描述内容 |",
	}
	if len(lines) != len(expectedRows) {
		t.Fatalf("expected %d lines, got %d: %q", len(expectedRows), len(lines), wrapped)
	}
	for i, expected := range expectedRows {
		if lines[i] != expected {
			t.Errorf("line %d: got %q, want %q", i, lines[i], expected)
		}
	}
}

func TestHardWrapTextStillWrapsNonTableContent(t *testing.T) {
	content := "这是一段很长的普通文本内容需要被换行处理"
	wrapped := hardWrapText(content, 10)
	lines := strings.Split(wrapped, "\n")
	if len(lines) < 2 {
		t.Fatalf("expected non-table content to be wrapped into multiple lines, got %d lines: %q", len(lines), wrapped)
	}
}

func TestApplyEventToolSummaryDoesNotInjectSystemEntry(t *testing.T) {
	m := model{}

	m.applyEvent(Event{Kind: EventToolSummary, Text: "读取文件 test.go"})

	if len(m.entries) != 0 {
		t.Fatalf("expected no entries injected by EventToolSummary, got %#v", m.entries)
	}
	if m.latestToolSummary != "读取文件 test.go" {
		t.Fatalf("expected latestToolSummary to be set, got %q", m.latestToolSummary)
	}
	if m.currentToolLabel != "" {
		t.Fatalf("expected currentToolLabel to be cleared, got %q", m.currentToolLabel)
	}
}

func TestApplyEventToolSummaryIgnoresEmptyText(t *testing.T) {
	m := model{}

	m.applyEvent(Event{Kind: EventToolSummary, Text: "   "})

	if m.latestToolSummary != "" {
		t.Fatalf("expected latestToolSummary to remain empty for blank text, got %q", m.latestToolSummary)
	}
}

func TestApplyEventToolSummaryIgnoresEmptyTextAfterNonEmpty(t *testing.T) {
	m := model{}

	m.applyEvent(Event{Kind: EventToolSummary, Text: "读取文件 test.go"})
	m.applyEvent(Event{Kind: EventToolSummary, Text: "  "})

	if m.latestToolSummary != "读取文件 test.go" {
		t.Fatalf("expected latestToolSummary to remain unchanged after blank text, got %q", m.latestToolSummary)
	}
}

func TestToolSummarySegmentReturnsEmptyWhenNoData(t *testing.T) {
	m := model{}

	if got := m.toolSummarySegment(false); got != "" {
		t.Fatalf("expected empty toolSummarySegment with no data, got %q", got)
	}
}

func TestToolSummarySegmentReturnsLatestSummary(t *testing.T) {
	m := model{}
	m.applyEvent(Event{Kind: EventToolSummary, Text: "读取文件 test.go"})

	got := m.toolSummarySegment(false)
	if !strings.Contains(got, "读取文件") {
		t.Fatalf("expected toolSummarySegment to contain summary text, got %q", got)
	}
}

func TestToolSummarySegmentReturnsEmptyAfterExpiry(t *testing.T) {
	m := model{}
	m.applyEvent(Event{Kind: EventToolSummary, Text: "读取文件 test.go"})
	// 模拟过期：将 latestToolAt 设为 21 秒前
	m.latestToolAt = time.Now().Add(-21 * time.Second)

	if got := m.toolSummarySegment(false); got != "" {
		t.Fatalf("expected empty toolSummarySegment after expiry, got %q", got)
	}
}

func TestToolSummarySegmentPrefersCurrentToolLabel(t *testing.T) {
	m := model{}
	m.applyEvent(Event{Kind: EventToolSummary, Text: "读取文件 test.go"})
	m.currentToolLabel = "执行命令"

	got := m.toolSummarySegment(false)
	if !strings.Contains(got, "执行命令") {
		t.Fatalf("expected toolSummarySegment to prefer currentToolLabel, got %q", got)
	}
	if strings.Contains(got, "读取文件") {
		t.Fatalf("expected toolSummarySegment to not show summary when currentToolLabel is set, got %q", got)
	}
}

