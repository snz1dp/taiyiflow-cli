package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	if !isExecutableSlashCommandInput("/about") {
		t.Fatalf("expected /about to be executable")
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
	if !isExecutableSlashCommandInput("/think 1") {
		t.Fatalf("expected /think to be executable")
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
		runner: func(ctx context.Context, request Request, emit func(Event), confirm func(ConfirmRequest) bool) error {
			return nil
		},
	}

	cmd := model.retryLastFailedRequest()
	if cmd == nil {
		t.Fatal("expected retry command")
	}
	if len(model.entries) != 1 || model.entries[0].role != "user" || model.entries[0].content != request.Input {
		t.Fatalf("expected retry to append user entry, got %#v", model.entries)
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
		contentWidth: 80,
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
	if !strings.Contains(rendered, "填写 0-100 的整数，0 表示禁用") {
		t.Fatalf("expected numeric hint for max rounds field, got %q", rendered)
	}
	if strings.Contains(rendered, "智能体自动续跑默认开关") || strings.Contains(rendered, "恢复默认(default)") {
		t.Fatalf("expected auto loop toggle field removed, got %q", rendered)
	}
	if !isConfigTextField(configFieldAgentAutoLoopMaxRounds) {
		t.Fatal("expected max rounds field to use text input")
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

func TestConfigFormValuesFromInputsReadsAgentAutoLoopMaxRoundsAtShiftedIndex(t *testing.T) {
	state := newConfigFormState(ConfigFormState{})
	state.inputs[16].SetValue("0")
	state.inputs[17].SetValue("zh-CN")
	state.inputs[21].SetValue("https://mirror-cn.clawhub.com")

	values := configFormValuesFromInputs(state.inputs)
	if values.AgentAutoLoopMaxRounds != "0" {
		t.Fatalf("expected max rounds from input index 16, got %q", values.AgentAutoLoopMaxRounds)
	}
	if values.OutputLanguage != "zh-CN" {
		t.Fatalf("expected output language from input index 17, got %q", values.OutputLanguage)
	}
	if values.SkillsRegistryURL != "https://mirror-cn.clawhub.com" {
		t.Fatalf("expected registry url from input index 21, got %q", values.SkillsRegistryURL)
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
		VerifyImageURL:         "https://example.com/xeai/kaptcha/image?ticket=captcha-ticket-1",
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
	if !strings.Contains(rendered, "已回退为 ANSI 彩色块预览") {
		t.Fatalf("expected captcha step to explain ansi color fallback, got %q", rendered)
	}
	if !strings.Contains(rendered, "已将图形验证码保存到本地图片，并尝试用系统预览打开") {
		t.Fatalf("expected captcha step to show preview path hint, got %q", rendered)
	}
	if strings.Contains(rendered, "本地路径(image_path):") || strings.Contains(rendered, "/tmp/captcha-preview.png") {
		t.Fatalf("expected captcha step to hide local preview path, got %q", rendered)
	}
	if strings.Contains(rendered, "图片地址(image_url):") || strings.Contains(rendered, "https://example.com/xeai/kaptcha/image?ticket=captcha-ticket-1") {
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
		VerifyImageURL:       "https://example.com/xeai/kaptcha/image?ticket=captcha-ticket-2",
		Ticket:               "captcha-ticket-2",
	})
	rendered := sanitizeTerminalText((model{contentWidth: 80, loginForm: state}).renderLoginForm())
	if strings.Contains(rendered, "图片地址(image_url):") || strings.Contains(rendered, "https://example.com/xeai/kaptcha/image?ticket=captcha-ticket-2") {
		t.Fatalf("expected plain terminal captcha step to hide http image url, got %q", rendered)
	}
	if strings.Contains(rendered, "本地路径(image_path):") {
		t.Fatalf("expected plain terminal captcha step to hide local image path, got %q", rendered)
	}
	if !strings.Contains(rendered, "已回退为 ANSI 彩色块预览") {
		t.Fatalf("expected plain terminal captcha step to mention ansi color preview, got %q", rendered)
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

func TestRenderTransientStatusAppendsStreamingIndicatorWithoutAssistantEntry(t *testing.T) {
	model := model{
		viewport:                 viewport.Model{Width: 80},
		activityHint:             "正在搜索",
		state:                    newProgressState(progressStreaming),
		streamingIndicatorActive: true,
		streamingIndicatorFrame:  2,
	}

	if got := sanitizeTerminalText(model.renderTransientStatus()); !strings.Contains(got, "正在搜索...") {
		t.Fatalf("expected streaming indicator in transient status, got %q", got)
	}
}

func TestRenderEntriesAppendsStreamingIndicatorToLatestAssistant(t *testing.T) {
	rendered := sanitizeTerminalText(renderEntries([]entry{{role: "user", content: "问题"}, {role: "assistant", content: "回答"}}, 80, 20, nil, Snapshot{}, "..."))
	if !strings.Contains(rendered, "回答...") {
		t.Fatalf("expected assistant entry to include streaming indicator, got %q", rendered)
	}
}

func TestInterruptRequestRemovesStreamingIndicator(t *testing.T) {
	canceled := false
	appModel := model{
		entries:                  []entry{{role: "user", content: "问题"}, {role: "assistant", content: "回答"}},
		state:                    newProgressState(progressStreaming),
		streamingIndicatorActive: true,
		streamingIndicatorFrame:  2,
		activityHint:             "正在输出",
		requestCancel: func() {
			canceled = true
		},
		viewport: viewport.Model{Width: 80, Height: 10},
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
	if got := sanitizeTerminalText(updated.viewport.View()); strings.Contains(got, "回答...") || strings.Contains(got, "请求已中断...") {
		t.Fatalf("expected interrupt to remove streaming ellipsis from viewport, got %q", got)
	}
}

func TestCompressionProgressNoteRendersInFooterInsteadOfTransientStatus(t *testing.T) {
	model := model{
		snapshot:     Snapshot{BackendAvailable: true, RequestMode: "agent", ApprovalMode: "confirm"},
		state:        newProgressState(progressStreaming),
		viewport:     viewport.Model{Width: 80},
		width:        120,
		contentWidth: 120,
	}

	model.applyEvent(Event{Kind: EventProgressNote, Text: "上下文压缩完成：320 -> 160", EventType: "compression_progress"})

	if got := sanitizeTerminalText(model.renderTransientStatus()); strings.Contains(got, "上下文压缩完成") {
		t.Fatalf("expected compression progress to stay out of transient status, got %q", got)
	}
	if got := sanitizeTerminalText(model.renderStatusBar()); !strings.Contains(got, "上下文压缩完成：320 -> 160") {
		t.Fatalf("expected compression progress in footer status bar, got %q", got)
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
					ContextUsageTokens:       3072,
					DirectMessageTokens:      2048,
					CompressedHistoryTokens:  1024,
				},
			},
		},
	}

	got := model.contextUsageSegment(false)
	if !strings.Contains(got, "当前上下文 2K/8K 25%") {
		t.Fatalf("expected current round usage in context segment, got %q", got)
	}
	if !strings.Contains(got, "可用 6K") {
		t.Fatalf("expected available context tokens in context segment, got %q", got)
	}
	if !strings.Contains(got, "历史 3K（直传 2K / 压缩 1K）") {
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
	if !strings.Contains(got, "5K") {
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
					ContextUsageTokens:       3072,
					DirectMessageTokens:      2048,
					CompressedHistoryTokens:  1024,
					HistoryMessageCount:      12,
				},
			},
		},
	}

	got := model.inputContextUsageHint()
	if !strings.Contains(got, "共8K剩6K可用") {
		t.Fatalf("expected compact availability summary in input hint, got %q", got)
	}
	if !strings.Contains(got, "历史 3K（直传 2K / 压缩 1K）") {
		t.Fatalf("expected history split summary in input hint, got %q", got)
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
	if !strings.Contains(got, "共8K剩6K可用") {
		t.Fatalf("expected compact availability summary in input hint, got %q", got)
	}
	if !strings.Contains(got, "历史 3K（直传 2K / 压缩 1K）") {
		t.Fatalf("expected history split summary in input hint, got %q", got)
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
	if !strings.Contains(got, "共8K剩6K可用") {
		t.Fatalf("expected compact context hint on the right side of status bar, got %q", got)
	}
}

func TestAssistantChunkClearsCompressionFooterHint(t *testing.T) {
	model := model{width: 120, contentWidth: 120}

	model.applyEvent(Event{Kind: EventProgressNote, Text: "开始进行上下文压缩", EventType: "compression_progress"})
	model.applyEvent(Event{Kind: EventAssistantChunk, Text: "已恢复输出", MessageID: "msg-1"})

	if strings.TrimSpace(model.footerHint) != "" {
		t.Fatalf("expected assistant chunk to clear footer compression hint, got %q", model.footerHint)
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

func TestConfigFormValuesFromInputsIncludesHTTPTimeout(t *testing.T) {
	state := newConfigFormState(ConfigFormState{})
	state.inputs[10].SetValue("75")
	state.inputs[11].SetValue("45")
	state.inputs[12].SetValue("300")
	state.inputs[13].SetValue("2")
	state.inputs[15].SetValue("90")

	values := configFormValuesFromInputs(state.inputs)
	if values.HTTPTimeoutSeconds != "75" {
		t.Fatalf("expected http timeout from form inputs, got %#v", values)
	}
	if values.CommandTimeoutSeconds != "45" || values.StreamIdleTimeoutSeconds != "300" || values.StreamRetryAttempts != "2" || values.QRWaitTimeoutSeconds != "90" {
		t.Fatalf("unexpected timeout values from form inputs: %#v", values)
	}
	if !isConfigTextField(configFieldHTTPTimeout) {
		t.Fatal("expected http timeout field to be treated as text input")
	}
	if !isConfigTextField(configFieldStreamRetryAttempts) {
		t.Fatal("expected stream retry attempts field to be treated as text input")
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

func TestApplyEventWritesToolCallPromptIntoTimeline(t *testing.T) {
	m := model{}

	m.applyEvent(Event{Kind: EventToolCall, Text: "读取文件"})
	m.applyEvent(Event{Kind: EventBuiltInCall, Text: "git status"})
	m.applyEvent(Event{Kind: EventClientToolCall, Text: "read_file notes.txt"})

	if len(m.entries) != 3 {
		t.Fatalf("expected three timeline prompt entries, got %#v", m.entries)
	}
	if !strings.Contains(m.entries[0].content, "正在执行服务端工具调用：读取文件") {
		t.Fatalf("unexpected service tool note: %#v", m.entries[0])
	}
	if !strings.Contains(m.entries[1].content, "正在执行本地命令执行：git status") {
		t.Fatalf("unexpected built-in note: %#v", m.entries[1])
	}
	if !strings.Contains(m.entries[2].content, "正在执行本地工具调用：read_file notes.txt") {
		t.Fatalf("unexpected client tool note: %#v", m.entries[2])
	}
}
