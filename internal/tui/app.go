package tui

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type Snapshot struct {
	Version          string
	BackendURL       string
	ServerPackage    string
	ServerVersion    string
	DefaultFlow      string
	DefaultFlowLabel string
	Workdir          string
	ApprovalMode     string
	SessionID        string
	WelcomeMessage   string
}

type FlowItem struct {
	ID          string
	Name        string
	Description string
	Type        string
}

type slashCommand struct {
	Trigger     string
	Completion  string
	Description string
}

type EventKind string

const (
	EventAssistantChunk EventKind = "assistant_chunk"
	EventProgressNote   EventKind = "progress_note"
	EventSystemNote     EventKind = "system_note"
	EventErrorNote      EventKind = "error_note"
	EventSessionUpdate  EventKind = "session_update"
	EventToolCall       EventKind = "tool_call"
	EventBuiltInCall    EventKind = "built_in_call"
	EventClientToolCall EventKind = "client_tool_call"
	EventStatsUpdate    EventKind = "stats_update"
)

type Event struct {
	Kind      EventKind
	Text      string
	SessionID string
	Stats     *RequestMetrics
}

type Request struct {
	Input  string
	FlowID string
	RunID  string
}

type ContextWindow struct {
	MaxInputTokens              int
	ShortTermContextTokens      int
	CurrentRoundInputTokens     int
	NextRoundAvailableTokens    int
	CurrentRoundAvailableTokens int
	HistoryMessageCount         int
	PreparedMessageCount        int
}

type TokenUsage struct {
	InputTokens   int
	OutputTokens  int
	TotalTokens   int
	StopReason    string
	ModelName     string
	ContextWindow *ContextWindow
}

type CompressionStats struct {
	Count            int
	BeforeTokens     int
	AfterTokens      int
	RemainingTokens  int
	CompressionRatio float64
	TargetType       string
}

type RequestMetrics struct {
	RunID         string
	StartedAt     time.Time
	CompletedAt   time.Time
	InputChars    int
	OutputChars   int
	ChunkCount    int
	ToolCallCount int
	CommandCount  int
	TokenUsage    *TokenUsage
	Compression   *CompressionStats
}

type ConfirmRequest struct {
	Title   string
	Command string
	Detail  string
}

type LocalCommandResult struct {
	Handled      bool
	ClearHistory bool
	Events       []Event
}

type Runner func(ctx context.Context, request Request, emit func(Event), confirm func(ConfirmRequest) bool) error

type FlowChangeHandler func(ctx context.Context, flowID string) (Snapshot, error)

type LocalCommandHandler func(ctx context.Context, command string, snapshot Snapshot) (LocalCommandResult, error)

type entry struct {
	role    string
	content string
}

type confirmState struct {
	request    ConfirmRequest
	responseCh chan bool
}

type streamEventMsg struct {
	event Event
}

type streamDoneMsg struct{}

type streamFailedMsg struct {
	err error
}

type confirmRequestMsg struct {
	request    ConfirmRequest
	responseCh chan bool
}

type flowChangedMsg struct {
	snapshot Snapshot
	err      error
}

type progressPhase string

const (
	progressIdle       progressPhase = "idle"
	progressSending    progressPhase = "sending"
	progressStreaming  progressPhase = "streaming"
	progressConfirming progressPhase = "confirming"
	progressExecuting  progressPhase = "executing"
	progressSwitching  progressPhase = "switching"
	progressFailed     progressPhase = "failed"
)

type progressState struct {
	phase   progressPhase
	label   string
	hint    string
	percent float64
}

type model struct {
	ctx            context.Context
	snapshot       Snapshot
	flows          []FlowItem
	runner         Runner
	flowChange     FlowChangeHandler
	localCommand   LocalCommandHandler
	eventCh        chan tea.Msg
	entries        []entry
	input          textarea.Model
	flowFilter     textinput.Model
	viewport       viewport.Model
	progress       progress.Model
	markdown       *glamour.TermRenderer
	state          progressState
	activityHint   string
	requestStats   RequestMetrics
	width          int
	height         int
	contentWidth   int
	ready          bool
	quitting       bool
	showFlowPicker bool
	flowCursor     int
	commandCursor  int
	pendingConfirm *confirmState
	requestCancel  context.CancelFunc
	interrupting   bool
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#F1F8E9")).
			Background(lipgloss.Color("#2E7D32")).
			Padding(0, 1)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#2E7D32")).
			Background(lipgloss.Color("#102418")).
			Padding(1, 2)

	overlayStyle = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("#4CAF50")).
			Background(lipgloss.Color("#102418")).
			Padding(1, 2)

	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784"))
	valueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#E8F5E9"))
	hintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#A5D6A7"))

	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#C8E6C9")).
			Bold(true)

	assistantStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1F8E9"))
	progressNoteStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#A5D6A7"))
	systemStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784"))
	errorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)

	userMessageStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(lipgloss.Color("#66BB6A")).
				PaddingLeft(1).
				Foreground(lipgloss.Color("#C8E6C9"))

	assistantMessageStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(lipgloss.Color("#AED581")).
				PaddingLeft(1)

	systemMessageStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(lipgloss.Color("#2E7D32")).
				PaddingLeft(1).
				Foreground(lipgloss.Color("#81C784"))

	errorMessageStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(lipgloss.Color("203")).
				PaddingLeft(1).
				Foreground(lipgloss.Color("203")).
				Bold(true)

	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#2E7D32")).
			Padding(0, 1)

	selectedFlowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F1F8E9")).
				Background(lipgloss.Color("#2E7D32")).
				Bold(true)

	progressLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#81C784")).
				Bold(true)

	headerBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F1F8E9")).
			Background(lipgloss.Color("#1B5E20")).
			Bold(true).
			Padding(0, 1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E8F5E9")).
			Background(lipgloss.Color("#16351F")).
			Padding(0, 1)

	statusBarDimStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#A5D6A7")).
				Background(lipgloss.Color("#16351F"))

	historyFrameStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("#2E7D32")).
				Padding(0, 1)

	minimalHistoryStyle = lipgloss.NewStyle()

	inlineProgressStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#A5D6A7")).
				Padding(0, 1)

	statusAccentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F1F8E9")).
				Background(lipgloss.Color("#2E7D32")).
				Bold(true).
				Padding(0, 1)

	commandHintStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#A5D6A7"))

	selectedCommandStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F1F8E9")).
				Background(lipgloss.Color("#2E7D32")).
				Bold(true)
)

var slashCommands = []slashCommand{
	{Trigger: "/agents", Completion: "/agents ", Description: "打开智能体选择，或用 /agents <名称> 直接切换"},
	{Trigger: "/command", Completion: "/command ", Description: "在当前目录执行本地 shell 命令"},
	{Trigger: "/new", Completion: "/new", Description: "重置当前智能体的本地会话"},
	{Trigger: "/session", Completion: "/session", Description: "显示当前会话状态"},
	{Trigger: "/sessions", Completion: "/sessions ", Description: "列出当前智能体的服务端会话，可附页码"},
	{Trigger: "/help", Completion: "/help", Description: "显示可用命令帮助"},
	{Trigger: "/clear", Completion: "/clear", Description: "清空当前界面消息"},
	{Trigger: "/exit", Completion: "/exit", Description: "退出当前 TUI 会话"},
}

func Run(ctx context.Context, snapshot Snapshot, flows []FlowItem, runner Runner, flowChange FlowChangeHandler, localCommand LocalCommandHandler) error {
	inputModel := textarea.New()
	inputModel.Placeholder = "输入内容后回车发送"
	inputModel.Prompt = "> "
	inputModel.ShowLineNumbers = false
	focusedStyle, blurredStyle := fixedTextareaStyles()
	inputModel.FocusedStyle = focusedStyle
	inputModel.BlurredStyle = blurredStyle
	inputModel.SetWidth(60)
	inputModel.SetHeight(1)
	inputModel.Focus()

	flowFilterModel := textinput.New()
	flowFilterModel.Placeholder = "输入名称或描述筛选智能体"
	flowFilterModel.Prompt = "搜索> "
	flowFilterModel.Blur()

	viewportModel := viewport.New(0, 0)
	viewportModel.Style = lipgloss.NewStyle()

	progressModel := progress.New(progress.WithDefaultGradient())

	initialEntries := buildInitialEntries(snapshot)
	program := tea.NewProgram(model{
		ctx:          ctx,
		snapshot:     snapshot,
		flows:        flows,
		runner:       runner,
		flowChange:   flowChange,
		localCommand: localCommand,
		eventCh:      make(chan tea.Msg),
		entries:      initialEntries,
		input:        inputModel,
		flowFilter:   flowFilterModel,
		viewport:     viewportModel,
		progress:     progressModel,
		state:        newProgressState(progressIdle),
		flowCursor:   currentFlowIndex(flows, snapshot.DefaultFlow),
	}, tea.WithAltScreen())
	finalModel, err := program.Run()
	if err == nil {
		if finalState, ok := finalModel.(model); ok && finalState.quitting {
			fmt.Println("再见～")
		}
	}
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.input.Focus(), m.progress.SetPercent(m.state.percent))
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case progress.FrameMsg:
		var cmd tea.Cmd
		updated, nextCmd := m.progress.Update(typed)
		progressModel, ok := updated.(progress.Model)
		if ok {
			m.progress = progressModel
		}
		cmd = nextCmd
		return m, cmd
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		m.ready = true
		m.syncLayout()
		return m, nil
	case streamEventMsg:
		cmd := m.applyEvent(typed.event)
		if m.isBusy() {
			return m, tea.Batch(cmd, waitForStream(m.eventCh))
		}
		return m, cmd
	case streamFailedMsg:
		if m.interrupting || errors.Is(typed.err, context.Canceled) {
			progressCmd := m.completeInterruptedRequest()
			m.input.Focus()
			return m, progressCmd
		}
		m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
		m.finishRequestStats()
		m.activityHint = ""
		m.requestCancel = nil
		m.syncViewport()
		progressCmd := m.transitionProgress(progressFailed)
		m.input.Focus()
		return m, progressCmd
	case streamDoneMsg:
		if m.interrupting {
			progressCmd := m.completeInterruptedRequest()
			m.input.Focus()
			return m, progressCmd
		}
		m.finishRequestStats()
		m.activityHint = ""
		m.requestCancel = nil
		progressCmd := m.transitionProgress(progressIdle)
		m.input.Focus()
		return m, progressCmd
	case confirmRequestMsg:
		m.pendingConfirm = &confirmState{request: typed.request, responseCh: typed.responseCh}
		m.input.Blur()
		return m, tea.Batch(m.transitionProgress(progressConfirming), waitForStream(m.eventCh))
	case flowChangedMsg:
		if typed.err != nil {
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			m.input.Focus()
			return m, m.transitionProgress(progressFailed)
		}
		previousFlow := fallback(m.snapshot.DefaultFlowLabel, sanitizeFlowLabel(m.snapshot.DefaultFlow))
		m.snapshot = typed.snapshot
		m.flowCursor = currentFlowIndex(m.flows, m.snapshot.DefaultFlow)
		m.flowFilter.SetValue("")
		m.flowFilter.Blur()
		m.requestStats = RequestMetrics{}
		m.activityHint = ""
		m.entries = buildInitialEntries(m.snapshot)
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("已切换默认智能体: %s -> %s", fallback(previousFlow, "<未配置>"), fallback(m.snapshot.DefaultFlowLabel, sanitizeFlowLabel(m.snapshot.DefaultFlow)))})
		m.showFlowPicker = false
		m.syncViewport()
		m.input.Focus()
		return m, m.transitionProgress(progressIdle)
	case tea.KeyMsg:
		if m.pendingConfirm != nil {
			return m.handleConfirmKey(typed)
		}
		if m.showFlowPicker {
			return m.handleFlowPickerKey(typed)
		}
		return m.handleMainKey(typed)
	}
	return m, nil
}

func (m model) handleMainKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.isBusy() && m.requestCancel != nil {
			m.interrupting = true
			m.requestCancel()
			m.activityHint = "请求已中断"
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "esc":
		if m.isBusy() {
			return m, nil
		}
		if strings.TrimSpace(m.input.Value()) != "" {
			m.input.SetValue("")
			m.syncLayout()
		}
		return m, nil
	case "ctrl+o":
		return m, m.openFlowPicker()
	case "pgup":
		m.viewport.HalfViewUp()
		return m, nil
	case "pgdown":
		m.viewport.HalfViewDown()
		return m, nil
	case "up":
		if m.showSlashCommandHints() {
			m.moveSlashCommandCursor(-1)
			return m, nil
		}
		m.viewport.LineUp(1)
		return m, nil
	case "down":
		if m.showSlashCommandHints() {
			m.moveSlashCommandCursor(1)
			return m, nil
		}
		m.viewport.LineDown(1)
		return m, nil
	case "home":
		m.viewport.GotoTop()
		return m, nil
	case "end":
		m.viewport.GotoBottom()
		return m, nil
	case "tab":
		if m.showSlashCommandHints() && m.applySelectedSlashCommand() {
			m.syncLayout()
			return m, nil
		}
		return m, nil
	case "shift+enter", "alt+enter", "ctrl+j":
		if m.isBusy() {
			return m, nil
		}
		m.input.InsertString("\n")
		m.syncLayout()
		return m, nil
	case "enter":
		if m.isBusy() {
			return m, nil
		}
		input := strings.TrimSpace(m.input.Value())
		if input == "" {
			return m, nil
		}
		if m.showSlashCommandHints() && !isExecutableSlashCommandInput(input) {
			if m.applySelectedSlashCommand() {
				m.syncLayout()
			}
			return m, nil
		}
		if strings.EqualFold(input, "/exit") {
			m.quitting = true
			return m, tea.Quit
		}
		if commandHandled, cmd := m.handleSlashCommand(input); commandHandled {
			return m, cmd
		}
		m.entries = append(m.entries, entry{role: "user", content: input})
		m.input.SetValue("")
		m.activityHint = ""
		m.interrupting = false
		runID := newRequestRunID()
		m.startRequestStats(input, runID)
		m.syncViewport()
		request := Request{Input: input, FlowID: m.snapshot.DefaultFlow, RunID: runID}
		requestCtx, cancel := context.WithCancel(m.ctx)
		m.requestCancel = cancel
		return m, tea.Batch(m.transitionProgress(progressSending), startStream(requestCtx, request, m.runner, m.eventCh))
	}

	wasShowingCommandHints := m.showSlashCommandHints()
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if !wasShowingCommandHints && m.showSlashCommandHints() {
		m.commandCursor = 0
	}
	m.clampSlashCommandCursor()
	return m, cmd
}

func (m model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		m.pendingConfirm.responseCh <- true
		close(m.pendingConfirm.responseCh)
		m.pendingConfirm = nil
		return m, tea.Batch(m.transitionProgress(progressExecuting), waitForStream(m.eventCh))
	case "n", "N", "q", "esc", "ctrl+c":
		m.pendingConfirm.responseCh <- false
		close(m.pendingConfirm.responseCh)
		m.pendingConfirm = nil
		return m, tea.Batch(m.transitionProgress(progressStreaming), waitForStream(m.eventCh))
	default:
		return m, nil
	}
}

func (m model) handleFlowPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.showFlowPicker = false
		m.flowFilter.SetValue("")
		m.flowFilter.Blur()
		m.input.Focus()
		return m, nil
	case "up", "k":
		if m.flowCursor > 0 {
			m.flowCursor--
		}
		return m, nil
	case "down", "j":
		if m.flowCursor < len(m.flows)-1 {
			m.flowCursor++
		}
		return m, nil
	case "enter":
		filtered := m.filteredFlowIndices()
		if len(filtered) == 0 {
			m.showFlowPicker = false
			m.flowFilter.SetValue("")
			m.flowFilter.Blur()
			m.input.Focus()
			return m, nil
		}
		selected := m.flows[filtered[m.flowCursor]]
		return m, tea.Batch(m.transitionProgress(progressSwitching), changeFlowCmd(m.ctx, selected.ID, m.flowChange))
	default:
		var cmd tea.Cmd
		m.flowFilter, cmd = m.flowFilter.Update(msg)
		m.clampFlowCursor()
		return m, cmd
	}
}

func (m *model) applyEvent(event Event) tea.Cmd {
	var cmd tea.Cmd
	switch event.Kind {
	case EventAssistantChunk:
		m.activityHint = ""
		if m.state.phase != progressStreaming {
			cmd = m.transitionProgress(progressStreaming)
		}
		m.requestStats.OutputChars += utf8.RuneCountInString(event.Text)
		m.requestStats.ChunkCount++
		if len(m.entries) == 0 || m.entries[len(m.entries)-1].role != "assistant" {
			m.entries = append(m.entries, entry{role: "assistant", content: event.Text})
		} else {
			m.entries[len(m.entries)-1].content += event.Text
		}
	case EventProgressNote:
		if strings.TrimSpace(event.Text) != "" {
			m.activityHint = strings.TrimSpace(event.Text)
		}
	case EventSystemNote:
		if strings.TrimSpace(event.Text) != "" {
			if isTransientSystemStatus(event.Text) {
				m.applyEvent(Event{Kind: EventProgressNote, Text: event.Text})
			} else {
				m.entries = append(m.entries, entry{role: "system", content: event.Text})
			}
		}
	case EventErrorNote:
		if strings.TrimSpace(event.Text) != "" {
			m.entries = append(m.entries, entry{role: "error", content: event.Text})
		}
	case EventToolCall:
		m.requestStats.ToolCallCount++
		m.activityHint = "执行中"
	case EventBuiltInCall, EventClientToolCall:
		m.requestStats.CommandCount++
		m.activityHint = "执行中"
	case EventStatsUpdate:
		if event.Stats != nil {
			if strings.TrimSpace(event.Stats.RunID) != "" {
				m.requestStats.RunID = event.Stats.RunID
			}
			if event.Stats.TokenUsage != nil {
				m.requestStats.TokenUsage = event.Stats.TokenUsage
			}
			if event.Stats.Compression != nil {
				m.requestStats.Compression = event.Stats.Compression
			}
		}
	case EventSessionUpdate:
		m.snapshot.SessionID = strings.TrimSpace(event.SessionID)
	}
	m.syncViewport()
	return cmd
}

func (m *model) completeInterruptedRequest() tea.Cmd {
	m.finishRequestStats()
	m.activityHint = ""
	m.requestCancel = nil
	m.interrupting = false
	m.entries = append(m.entries, entry{role: "system", content: "请求已中断"})
	m.syncViewport()
	return m.transitionProgress(progressIdle)
}

func (m *model) syncLayout() {
	if !m.ready || m.width == 0 || m.height == 0 {
		return
	}
	contentWidth := m.width - 4
	if !m.useMinimalChrome() {
		contentWidth = m.width - 6
	}
	if contentWidth > 120 {
		contentWidth = 120
	}
	if contentWidth < 40 {
		contentWidth = 40
	}
	m.contentWidth = contentWidth

	viewportWidth := contentWidth - 6
	if viewportWidth < 20 {
		viewportWidth = 20
	}
	inputHeight := m.currentInputHeight()
	fixedHeight := 7 + maxInt(0, inputHeight-1)
	if !m.useMinimalChrome() {
		fixedHeight = 8 + maxInt(0, inputHeight-1)
	}
	if m.showProgressLine() {
		fixedHeight += 2
	}
	if m.pendingConfirm != nil || m.showFlowPicker {
		fixedHeight += 3
	}
	historyHeight := m.height - fixedHeight
	if historyHeight < 8 {
		historyHeight = 8
	}
	m.viewport.Width = viewportWidth
	m.viewport.Height = historyHeight
	m.updateMarkdownRenderer(viewportWidth - 2)
	inputWidth := contentWidth - 10
	if inputWidth < 20 {
		inputWidth = 20
	}
	m.input.SetWidth(inputWidth)
	m.input.SetHeight(inputHeight)
	m.flowFilter.Width = minInt(contentWidth-16, 48)
	if m.flowFilter.Width < 18 {
		m.flowFilter.Width = 18
	}
	m.progress.Width = contentWidth - 18
	if m.progress.Width < 12 {
		m.progress.Width = 12
	}
	m.syncViewport()
}

func (m *model) syncViewport() {
	content := renderEntries(m.entries, m.viewport.Width, m.viewport.Height, m.markdown, m.snapshot)
	m.viewport.SetContent(content)
	if m.shouldPinHistoryTop() {
		m.viewport.GotoTop()
		return
	}
	m.viewport.GotoBottom()
}

func (m model) View() string {
	if m.quitting {
		return "\n"
	}

	header := m.renderHeader()
	history := m.renderHistoryPane()
	inputView := m.input.View()
	if m.pendingConfirm != nil || m.showFlowPicker {
		inputView = hintStyle.Render("当前有未处理的弹层，请先完成操作。")
	} else if commandHints := m.renderSlashCommandHints(); commandHints != "" {
		inputView = lipgloss.JoinVertical(lipgloss.Left, inputView, "", commandHints)
	}
	inputBox := inputStyle.Width(m.contentWidth - 6).Render(inputView)
	parts := []string{header, history}
	if m.showProgressLine() {
		parts = append(parts, m.renderProgressLine())
	}
	parts = append(parts, inputBox, m.renderStatusBar())
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	if overlay := m.renderOverlay(); overlay != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, content, "", overlay)
	}
	if !m.ready || m.width == 0 {
		return "\n" + content + "\n"
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, content)
}

func (m model) renderHeader() string {
	flowLabel := fallback(m.snapshot.DefaultFlowLabel, "未配置")
	clientVersion := fallback(m.snapshot.Version, "0.1.0")
	serverVersion := m.serverVersionLabel()
	workdir := fallback(filepath.Base(m.snapshot.Workdir), ".")
	headline := joinStatusSegments([]string{
		"太乙智启 Terminal",
		"C " + clientVersion,
		"S " + serverVersion,
	}, "  ·  ")
	contextLine := joinStatusSegments([]string{
		"智能体 " + flowLabel,
		"目录 " + workdir,
	}, "  ·  ")
	if m.useMinimalChrome() {
		lines := []string{
			trimForWidth(headline, m.contentWidth-2),
			trimForWidth(contextLine, m.contentWidth-2),
		}
		return headerBarStyle.Width(m.contentWidth).Render(strings.Join(lines, "\n"))
	}
	line := joinStatusSegments([]string{headline, contextLine}, "  |  ")
	return headerBarStyle.Width(m.contentWidth).Render(trimForWidth(line, m.contentWidth-2))
}

func (m model) renderHistoryPane() string {
	width := m.contentWidth - 6
	if width < 20 {
		width = m.contentWidth
	}
	if m.useMinimalChrome() {
		return minimalHistoryStyle.Width(width).Render(m.viewport.View())
	}
	return historyFrameStyle.Width(width).Render(m.viewport.View())
}

func (m model) renderProgressLine() string {
	return inlineProgressStyle.Width(m.contentWidth).Render(trimForWidth(strings.TrimSpace(m.activityHint), m.contentWidth-2))
}

func (m model) renderStatusBar() string {
	segments := m.statusBarSegments()
	stateChip := statusAccentStyle.Render(m.state.label)
	bodyWidth := m.contentWidth - lipgloss.Width(stateChip) - 3
	if bodyWidth < 12 {
		bodyWidth = 12
	}
	body := trimForWidth(joinStatusSegments(segments, "  ·  "), bodyWidth)
	line := lipgloss.JoinHorizontal(lipgloss.Left, stateChip, " ", statusBarDimStyle.Render(body))
	return statusBarStyle.Width(m.contentWidth).Render(trimForWidth(line, m.contentWidth-2))
}

func (m model) renderSlashCommandHints() string {
	if !m.showSlashCommandHints() {
		return ""
	}
	commands := m.visibleSlashCommands()
	lines := []string{commandHintStyle.Render("命令提示：上下键选择，Tab 补全，Enter 应用")}
	for index, command := range commands {
		line := fmt.Sprintf("%s  %s", flowCursor(index == m.commandCursor), command.Trigger)
		if strings.TrimSpace(command.Description) != "" {
			line += "  " + command.Description
		}
		if index == m.commandCursor {
			line = selectedCommandStyle.Render(trimForWidth(line, m.contentWidth-12))
		} else {
			line = commandHintStyle.Render(trimForWidth(line, m.contentWidth-12))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m model) renderOverlay() string {
	if m.pendingConfirm != nil {
		request := m.pendingConfirm.request
		lines := []string{request.Title}
		if strings.TrimSpace(request.Command) != "" {
			lines = append(lines, "命令: "+request.Command)
		}
		if strings.TrimSpace(request.Detail) != "" {
			lines = append(lines, request.Detail)
		}
		lines = append(lines, "按 y 或 Enter 确认，按 n 或 Esc 拒绝。")
		return overlayStyle.Width(minInt(m.contentWidth-6, 90)).Render(strings.Join(lines, "\n\n"))
	}
	if m.showFlowPicker {
		return overlayStyle.Width(minInt(m.contentWidth-6, 90)).Render(m.renderFlowPicker())
	}
	return ""
}

func (m model) renderFlowPicker() string {
	if len(m.flows) == 0 {
		return "当前没有可切换的智能体。"
	}
	filtered := m.filteredFlowIndices()
	lines := []string{
		"选择默认智能体",
		"输入关键词筛选，使用方向键或 j/k 选择，Enter 确认，Esc 或 q 取消。",
		m.flowFilter.View(),
		fmt.Sprintf("匹配 %d / %d", len(filtered), len(m.flows)),
		"",
	}
	if len(filtered) == 0 {
		lines = append(lines, hintStyle.Render("没有匹配的智能体，可继续修改搜索关键词。"))
		return strings.Join(lines, "\n")
	}
	for position, index := range filtered {
		item := m.flows[index]
		line := fmt.Sprintf("%s  %s", flowCursor(position == m.flowCursor), fallback(item.Name, item.ID))
		if item.ID == m.snapshot.DefaultFlow || item.Name == m.snapshot.DefaultFlow {
			line += "  [当前]"
		}
		if position == m.flowCursor {
			line = selectedFlowStyle.Render(line)
		}
		lines = append(lines, line)
		if description := strings.TrimSpace(item.Description); description != "" && position == m.flowCursor {
			lines = append(lines, hintStyle.Render(trimText(description, 100)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) statusBarSegments() []string {
	segments := []string{}
	if strings.TrimSpace(m.snapshot.SessionID) != "" {
		segments = append(segments, ""+compactSessionID(m.snapshot.SessionID))
	}
	segments = append(segments, ""+approvalModeLabel(m.snapshot.ApprovalMode))
	if contextSegment := m.contextUsageSegment(); contextSegment != "" {
		segments = append(segments, contextSegment)
	}
	if tokenSegment := m.tokenUsageSegment(); tokenSegment != "" {
		segments = append(segments, tokenSegment)
	}
	if requestSegment := m.requestStatsSegment(); requestSegment != "" {
		segments = append(segments, requestSegment)
	}
	if compressionSegment := m.compressionSegment(); compressionSegment != "" {
		segments = append(segments, compressionSegment)
	}
	if m.useMinimalChrome() {
		return segments
	}
	segments = append([]string{"" + fallback(m.snapshot.DefaultFlowLabel, "未配置")}, segments...)
	return segments
}

func (m model) showProgressLine() bool {
	return strings.TrimSpace(m.activityHint) != ""
}

func (m model) useMinimalChrome() bool {
	return m.width > 0 && m.width < 76
}

func renderEntries(entries []entry, width int, height int, renderer *glamour.TermRenderer, snapshot Snapshot) string {
	if len(entries) == 0 {
		return hintStyle.Render("还没有消息。")
	}
	blocks := make([]string, 0, len(entries))
	for _, item := range entries {
		blocks = append(blocks, renderEntry(item, width, height, renderer, snapshot))
	}
	return strings.Join(blocks, "\n\n")
}

func renderEntry(item entry, width int, height int, renderer *glamour.TermRenderer, snapshot Snapshot) string {
	switch item.role {
	case "welcome":
		return renderWelcomePane(snapshot, item.content, width, height)
	case "user":
		return userMessageStyle.Render(item.content)
	case "assistant":
		return assistantMessageStyle.Render(renderMarkdown(item.content, width, renderer))
	case "error":
		return errorMessageStyle.Render(item.content)
	default:
		return systemMessageStyle.Render(item.content)
	}
}

func startStream(ctx context.Context, request Request, runner Runner, eventCh chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		go func() {
			err := runner(ctx, request, func(event Event) {
				eventCh <- streamEventMsg{event: event}
			}, func(request ConfirmRequest) bool {
				responseCh := make(chan bool, 1)
				eventCh <- confirmRequestMsg{request: request, responseCh: responseCh}
				return <-responseCh
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				eventCh <- streamFailedMsg{err: err}
			}
			eventCh <- streamDoneMsg{}
		}()
		return <-eventCh
	}
}

func changeFlowCmd(ctx context.Context, flowID string, handler FlowChangeHandler) tea.Cmd {
	return func() tea.Msg {
		snapshot, err := handler(ctx, flowID)
		return flowChangedMsg{snapshot: snapshot, err: err}
	}
}

func (m *model) openFlowPicker() tea.Cmd {
	if m.isBusy() {
		m.entries = append(m.entries, entry{role: "system", content: "当前正在处理请求，暂时不能切换智能体。"})
		m.syncViewport()
		return nil
	}
	if len(m.flows) == 0 {
		m.entries = append(m.entries, entry{role: "error", content: "当前没有可切换的智能体列表。"})
		m.syncViewport()
		return nil
	}
	m.showFlowPicker = true
	m.flowFilter.SetValue("")
	m.flowFilter.Focus()
	m.flowCursor = m.currentFilteredFlowCursor()
	m.input.Blur()
	return nil
}

func (m *model) handleSlashCommand(input string) (bool, tea.Cmd) {
	trimmed := strings.TrimSpace(input)
	switch {
	case strings.EqualFold(trimmed, "/agents"):
		m.input.SetValue("")
		return true, m.openFlowPicker()
	case strings.HasPrefix(trimmed, "/agents "):
		flowID := strings.TrimSpace(strings.TrimPrefix(trimmed, "/agents "))
		if flowID == "" {
			m.entries = append(m.entries, entry{role: "error", content: "用法: /agents <名称>"})
			m.syncViewport()
			m.input.SetValue("")
			return true, nil
		}
		m.input.SetValue("")
		return true, tea.Batch(m.transitionProgress(progressSwitching), changeFlowCmd(m.ctx, flowID, m.flowChange))
	default:
		return m.handleLocalSlashCommand(trimmed)
	}
}

func (m *model) handleLocalSlashCommand(command string) (bool, tea.Cmd) {
	if m.localCommand == nil {
		return false, nil
	}
	result, err := m.localCommand(m.ctx, command, m.snapshot)
	m.input.SetValue("")
	if err != nil {
		m.entries = append(m.entries, entry{role: "error", content: err.Error()})
		m.syncViewport()
		return true, nil
	}
	if !result.Handled {
		return false, nil
	}
	if result.ClearHistory {
		m.entries = buildInitialEntries(m.snapshot)
	}
	for _, event := range result.Events {
		m.applyEvent(event)
	}
	m.syncViewport()
	return true, nil
}

func (m model) showSlashCommandHints() bool {
	if m.pendingConfirm != nil || m.showFlowPicker || m.isBusy() {
		return false
	}
	trimmed := strings.TrimSpace(m.input.Value())
	if trimmed == "" || strings.Contains(trimmed, "\n") || !strings.HasPrefix(trimmed, "/") {
		return false
	}
	return len(m.visibleSlashCommands()) > 0
}

func (m model) visibleSlashCommands() []slashCommand {
	query := slashCommandQuery(m.input.Value())
	visible := make([]slashCommand, 0, len(slashCommands))
	for _, command := range slashCommands {
		trigger := strings.ToLower(command.Trigger)
		description := strings.ToLower(command.Description)
		if query == "" || strings.HasPrefix(trigger, query) || strings.Contains(description, query) {
			visible = append(visible, command)
		}
	}
	return visible
}

func (m *model) moveSlashCommandCursor(delta int) {
	commands := m.visibleSlashCommands()
	if len(commands) == 0 {
		m.commandCursor = 0
		return
	}
	m.commandCursor += delta
	if m.commandCursor < 0 {
		m.commandCursor = len(commands) - 1
		return
	}
	if m.commandCursor >= len(commands) {
		m.commandCursor = 0
	}
}

func (m *model) clampSlashCommandCursor() {
	commands := m.visibleSlashCommands()
	if len(commands) == 0 {
		m.commandCursor = 0
		return
	}
	if m.commandCursor < 0 {
		m.commandCursor = 0
	}
	if m.commandCursor >= len(commands) {
		m.commandCursor = len(commands) - 1
	}
}

func (m *model) applySelectedSlashCommand() bool {
	commands := m.visibleSlashCommands()
	if len(commands) == 0 {
		return false
	}
	m.clampSlashCommandCursor()
	selected := commands[m.commandCursor]
	m.input.SetValue(selected.Completion)
	return true
}

func waitForStream(eventCh chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-eventCh
	}
}

func buildInitialEntries(snapshot Snapshot) []entry {
	return []entry{{role: "welcome", content: strings.TrimSpace(snapshot.WelcomeMessage)}}
}

func (m model) shouldPinHistoryTop() bool {
	for _, item := range m.entries {
		switch item.role {
		case "user", "assistant", "error":
			return false
		}
	}
	return len(m.entries) > 0 && m.entries[0].role == "welcome"
}

func newProgressState(phase progressPhase) progressState {
	switch phase {
	case progressSending:
		return progressState{phase: phase, label: "请求已发出", hint: "正在向后端发起请求", percent: 0.18}
	case progressStreaming:
		return progressState{phase: phase, label: "流式响应中", hint: "正在接收模型输出", percent: 0.72}
	case progressConfirming:
		return progressState{phase: phase, label: "等待命令确认", hint: "请先确认客户端命令执行", percent: 0.34}
	case progressExecuting:
		return progressState{phase: phase, label: "处理中", hint: "", percent: 0.86}
	case progressSwitching:
		return progressState{phase: phase, label: "切换智能体中", hint: "正在更新默认智能体和会话上下文", percent: 0.46}
	case progressFailed:
		return progressState{phase: phase, label: "执行失败", hint: "当前可继续输入新的请求", percent: 1}
	default:
		return progressState{phase: progressIdle, label: "空闲", hint: "等待输入", percent: 0}
	}
}

func (m *model) transitionProgress(phase progressPhase) tea.Cmd {
	m.state = newProgressState(phase)
	return m.progress.SetPercent(m.state.percent)
}

func (m model) isBusy() bool {
	switch m.state.phase {
	case progressSending, progressStreaming, progressConfirming, progressExecuting, progressSwitching:
		return true
	default:
		return false
	}
}

func (m *model) startRequestStats(input string, runID string) {
	m.requestStats = RequestMetrics{
		RunID:      runID,
		StartedAt:  time.Now(),
		InputChars: utf8.RuneCountInString(input),
	}
}

func (m *model) finishRequestStats() {
	if m.requestStats.StartedAt.IsZero() || !m.requestStats.CompletedAt.IsZero() {
		return
	}
	m.requestStats.CompletedAt = time.Now()
}

func (m *model) updateMarkdownRenderer(width int) {
	if width < 20 {
		width = 20
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		m.markdown = nil
		return
	}
	m.markdown = renderer
}

func fixedTextareaStyles() (textarea.Style, textarea.Style) {
	focused := textarea.Style{
		Base:             lipgloss.NewStyle(),
		CursorLine:       lipgloss.NewStyle(),
		CursorLineNumber: lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784")),
		EndOfBuffer:      lipgloss.NewStyle().Foreground(lipgloss.Color("#2E7D32")),
		LineNumber:       lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784")),
		Placeholder:      lipgloss.NewStyle().Foreground(lipgloss.Color("#66BB6A")),
		Prompt:           lipgloss.NewStyle().Foreground(lipgloss.Color("#E8F5E9")),
		Text:             lipgloss.NewStyle().Foreground(lipgloss.Color("#F1F8E9")),
	}
	blurred := textarea.Style{
		Base:             lipgloss.NewStyle(),
		CursorLine:       lipgloss.NewStyle(),
		CursorLineNumber: lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784")),
		EndOfBuffer:      lipgloss.NewStyle().Foreground(lipgloss.Color("#2E7D32")),
		LineNumber:       lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784")),
		Placeholder:      lipgloss.NewStyle().Foreground(lipgloss.Color("#66BB6A")),
		Prompt:           lipgloss.NewStyle().Foreground(lipgloss.Color("#A5D6A7")),
		Text:             lipgloss.NewStyle().Foreground(lipgloss.Color("#C8E6C9")),
	}
	return focused, blurred
}

func (m model) serverVersionLabel() string {
	return snapshotServerVersion(m.snapshot)
}

func (m model) currentInputHeight() int {
	return inputHeightForValue(m.input.Value(), m.input.Prompt, m.contentWidth-10)
}

func snapshotServerVersion(snapshot Snapshot) string {
	version := strings.TrimSpace(snapshot.ServerVersion)
	if version == "" {
		version = "未知"
	}
	if pkg := strings.TrimSpace(snapshot.ServerPackage); pkg != "" {
		return " 服务版本：v" + version
	}
	return version
}

func renderWelcomePane(snapshot Snapshot, welcomeMessage string, width int, height int) string {
	if width < 36 {
		width = 36
	}
	if height < 12 {
		height = 12
	}
	targetHeight := maxInt(10, height*4/5)
	if targetHeight > height {
		targetHeight = height
	}
	innerWidth := width - 6
	if innerWidth < 28 {
		innerWidth = 28
	}
	contentHeight := targetHeight - 4
	if contentHeight < 8 {
		contentHeight = 8
	}
	bannerBlock := buildWelcomeBanner(innerWidth)
	bannerAreaHeight := contentHeight * 4 / 5
	bannerMinHeight := lipgloss.Height(bannerBlock) + 2
	if bannerAreaHeight < bannerMinHeight {
		bannerAreaHeight = bannerMinHeight
	}
	footerHeight := contentHeight - bannerAreaHeight
	if footerHeight < 4 {
		footerHeight = 4
		bannerAreaHeight = contentHeight - footerHeight
	}
	if bannerAreaHeight < bannerMinHeight {
		bannerAreaHeight = bannerMinHeight
		footerHeight = maxInt(3, contentHeight-bannerAreaHeight)
	}

	footerLines := []string{
		hintStyle.Render(centerText(trimForWidth(
			"太乙智启 CLI v"+fallback(snapshot.Version, "0.1.0")+
				" · "+snapshotServerVersion(snapshot), innerWidth), innerWidth)),
		"",
		hintStyle.Render(centerText(trimForWidth(joinStatusSegments([]string{
			"" + fallback(snapshot.DefaultFlowLabel, "未配置"),
			"" + compactSessionID(snapshot.SessionID),
		}, "  ·  "), innerWidth), innerWidth)),
		hintStyle.Render(centerText(trimForWidth("/agents /command /session /new /exit", innerWidth), innerWidth)),
	}
	if strings.TrimSpace(welcomeMessage) != "" {
		footerLines = append(footerLines, "", hintStyle.Render(centerText(trimForWidth(strings.TrimSpace(welcomeMessage), innerWidth), innerWidth)))
	}

	bannerSection := lipgloss.Place(innerWidth, bannerAreaHeight, lipgloss.Center, lipgloss.Bottom, bannerBlock)
	footerBlock := lipgloss.JoinVertical(lipgloss.Left, footerLines...)
	footerSection := lipgloss.Place(innerWidth, footerHeight, lipgloss.Center, lipgloss.Top, footerBlock)
	panel := panelStyle.Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, bannerSection, footerSection))

	panelHeight := lipgloss.Height(panel)
	topGap := 0
	bottomGap := 0
	if height > panelHeight {
		topGap = (height - panelHeight) / 2
		bottomGap = height - panelHeight - topGap
	}
	parts := []string{}
	if topGap > 0 {
		parts = append(parts, strings.Repeat("\n", topGap))
	}
	parts = append(parts, panel)
	if bottomGap > 0 {
		parts = append(parts, strings.Repeat("\n", bottomGap))
	}
	return strings.Join(parts, "")
}

func inputHeightForValue(value string, prompt string, inputWidth int) int {
	availableWidth := inputWidth - lipgloss.Width(prompt) - 2
	if availableWidth < 12 {
		availableWidth = 12
	}
	trimmed := value
	if trimmed == "" {
		return 1
	}
	lines := strings.Split(trimmed, "\n")
	visualLines := 0
	for _, line := range lines {
		runes := utf8.RuneCountInString(line)
		if runes == 0 {
			visualLines++
			continue
		}
		visualLines += (runes-1)/availableWidth + 1
	}
	if visualLines < 1 {
		visualLines = 1
	}
	if visualLines > 6 {
		visualLines = 6
	}
	return visualLines
}

func (m model) filteredFlowIndices() []int {
	query := strings.ToLower(strings.TrimSpace(m.flowFilter.Value()))
	indices := make([]int, 0, len(m.flows))
	for index, item := range m.flows {
		if query == "" || flowMatchesQuery(item, query) {
			indices = append(indices, index)
		}
	}
	return indices
}

func (m model) currentFilteredFlowCursor() int {
	filtered := m.filteredFlowIndices()
	current := currentFlowIndex(m.flows, m.snapshot.DefaultFlow)
	for position, index := range filtered {
		if index == current {
			return position
		}
	}
	return 0
}

func (m *model) clampFlowCursor() {
	filtered := m.filteredFlowIndices()
	if len(filtered) == 0 {
		m.flowCursor = 0
		return
	}
	if m.flowCursor < 0 {
		m.flowCursor = 0
	}
	if m.flowCursor >= len(filtered) {
		m.flowCursor = len(filtered) - 1
	}
}

func (m model) contextUsageSegment() string {
	usage := m.requestStats.TokenUsage
	if usage == nil || usage.ContextWindow == nil || usage.ContextWindow.MaxInputTokens <= 0 {
		return ""
	}
	current := usage.ContextWindow.CurrentRoundInputTokens
	if current <= 0 {
		current = usage.ContextWindow.ShortTermContextTokens
	}
	maxTokens := usage.ContextWindow.MaxInputTokens
	if maxTokens <= 0 {
		return ""
	}
	percent := float64(current) / float64(maxTokens) * 100
	return fmt.Sprintf("上下文 %s/%s %.0f%%", formatCompactInt(current), formatCompactInt(maxTokens), percent)
}

func (m model) tokenUsageSegment() string {
	usage := m.requestStats.TokenUsage
	if usage == nil {
		return ""
	}
	if usage.TotalTokens > 0 {
		return fmt.Sprintf("Token %s/%s/%s", formatCompactInt(usage.InputTokens), formatCompactInt(usage.OutputTokens), formatCompactInt(usage.TotalTokens))
	}
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		return fmt.Sprintf("Token %s/%s", formatCompactInt(usage.InputTokens), formatCompactInt(usage.OutputTokens))
	}
	return ""
}

func (m model) requestStatsSegment() string {
	if m.requestStats.StartedAt.IsZero() {
		return ""
	}
	duration := time.Since(m.requestStats.StartedAt)
	if !m.requestStats.CompletedAt.IsZero() {
		duration = m.requestStats.CompletedAt.Sub(m.requestStats.StartedAt)
	}
	parts := []string{"耗时" + formatDuration(duration)}
	if m.requestStats.ToolCallCount > 0 {
		parts = append(parts, fmt.Sprintf("%d 工具", m.requestStats.ToolCallCount))
	}
	if m.requestStats.CommandCount > 0 {
		parts = append(parts, fmt.Sprintf("%d 命令", m.requestStats.CommandCount))
	}
	return strings.Join(parts, " · ")
}

func (m model) compressionSegment() string {
	compression := m.requestStats.Compression
	if compression == nil || compression.Count == 0 {
		return ""
	}
	line := fmt.Sprintf("压缩 %d 次 %s→%s", compression.Count, formatCompactInt(compression.BeforeTokens), formatCompactInt(compression.AfterTokens))
	if compression.CompressionRatio > 0 {
		line += fmt.Sprintf(" %.0f%%", compression.CompressionRatio*100)
	}
	return line
}

func currentFlowIndex(flows []FlowItem, current string) int {
	for index, item := range flows {
		if item.ID == current || item.Name == current {
			return index
		}
	}
	return 0
}

func compactSessionID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "未建立"
	}
	if len(trimmed) <= 10 {
		return trimmed
	}
	return trimmed[:4] + "…" + trimmed[len(trimmed)-4:]
}

func joinStatusSegments(parts []string, separator string) string {
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	return strings.Join(filtered, separator)
}

func renderMarkdown(content string, width int, renderer *glamour.TermRenderer) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	if renderer == nil {
		return assistantStyle.Render(trimmed)
	}
	rendered, err := renderer.Render(trimmed)
	if err != nil {
		return assistantStyle.Render(trimmed)
	}
	return strings.TrimRight(rendered, "\n")
}

func isTransientSystemStatus(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	stageKeywords := []string{
		"开始运行",
		"多模态推理",
		"共享知识库",
		"个人知识库",
		"联网搜索",
		"大模型推理",
		"执行对话输出",
		"对话输出",
	}
	hasStageKeyword := false
	for _, keyword := range stageKeywords {
		if strings.Contains(trimmed, keyword) {
			hasStageKeyword = true
			break
		}
	}
	if !hasStageKeyword {
		return false
	}
	return strings.HasSuffix(trimmed, "...") || strings.HasPrefix(trimmed, "完成")
}

func flowMatchesQuery(item FlowItem, query string) bool {
	fields := []string{item.Name, item.Type, item.Description, item.ID}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(strings.TrimSpace(field)), query) {
			return true
		}
	}
	return false
}

func formatCompactInt(value int) string {
	if value < 1000 {
		return fmt.Sprintf("%d", value)
	}
	if value < 10000 {
		return fmt.Sprintf("%.1fk", float64(value)/1000)
	}
	return fmt.Sprintf("%dk", value/1000)
}

func formatDuration(duration time.Duration) string {
	if duration < time.Second {
		return fmt.Sprintf("%dms", duration.Milliseconds())
	}
	if duration < time.Minute {
		return fmt.Sprintf("%.1fs", duration.Seconds())
	}
	minutes := int(duration / time.Minute)
	seconds := int((duration % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

func newRequestRunID() string {
	raw := make([]byte, 16)
	if _, err := cryptorand.Read(raw); err != nil {
		fallback := time.Now().UnixNano()
		return fmt.Sprintf("run-%d", fallback)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

func trimForWidth(text string, width int) string {
	if width <= 0 {
		return text
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	ellipsis := "..."
	if width <= lipgloss.Width(ellipsis) {
		return ellipsis[:width]
	}
	trimmed := text
	for lipgloss.Width(trimmed)+lipgloss.Width(ellipsis) > width && len(trimmed) > 0 {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed + ellipsis
}

func flowCursor(selected bool) string {
	if selected {
		return ">"
	}
	return " "
}

func slashCommandQuery(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return ""
	}
	command := trimmed
	if index := strings.IndexAny(trimmed, " \t"); index >= 0 {
		command = trimmed[:index]
	}
	return strings.ToLower(command)
}

func isExecutableSlashCommandInput(input string) bool {
	trimmed := strings.TrimSpace(input)
	switch {
	case strings.EqualFold(trimmed, "/agents"):
		return true
	case strings.HasPrefix(strings.ToLower(trimmed), "/command "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/command ")) != ""
	case strings.EqualFold(trimmed, "/new"):
		return true
	case strings.EqualFold(trimmed, "/session"):
		return true
	case strings.EqualFold(trimmed, "/sessions"):
		return true
	case strings.HasPrefix(strings.ToLower(trimmed), "/sessions "):
		page := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(trimmed), "/sessions "))
		if page == "" {
			return false
		}
		value, err := strconv.Atoi(page)
		return err == nil && value >= 1
	case strings.EqualFold(trimmed, "/help"):
		return true
	case strings.EqualFold(trimmed, "/clear"):
		return true
	case strings.EqualFold(trimmed, "/exit"):
		return true
	case strings.HasPrefix(strings.ToLower(trimmed), "/agents "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/agents ")) != ""
	default:
		return false
	}
}

func trimText(content string, maxLen int) string {
	content = strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
	if len(content) <= maxLen {
		return content
	}
	if maxLen < 4 {
		return content[:maxLen]
	}
	return content[:maxLen-3] + "..."
}

func centerText(text string, width int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || width <= 0 {
		return trimmed
	}
	return lipgloss.Place(width, 1, lipgloss.Center, lipgloss.Top, trimmed)
}

func buildWelcomeBanner(width int) string {
	primaryStyle := valueStyle.Copy().Bold(true)
	secondaryStyle := hintStyle.Copy()
	if width < 24 {
		return primaryStyle.Render("TAIYIFLOW")
	}

	boxWidth := minInt(width, 38)
	if boxWidth < 26 {
		boxWidth = width
	}
	innerWidth := boxWidth - 2
	if innerWidth < 18 {
		return primaryStyle.Render("TAIYIFLOW")
	}

	centered := lipgloss.NewStyle().Width(innerWidth).Align(lipgloss.Center)
	top := hintStyle.Render("╭" + strings.Repeat("─", innerWidth) + "╮")
	middleTop := hintStyle.Render("│") + primaryStyle.Render(centered.Render("TaiyiFlow")) + hintStyle.Render("│")
	middleBottom := hintStyle.Render("│") + secondaryStyle.Render(centered.Render("太乙智启 CLI")) + hintStyle.Render("│")
	bottom := hintStyle.Render("╰" + strings.Repeat("─", innerWidth) + "╯")

	return strings.Join([]string{top, middleTop, middleBottom, bottom}, "\n")
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func row(label string, value string) string {
	return fmt.Sprintf("%s %s", labelStyle.Render(label+":"), valueStyle.Render(value))
}

func approvalModeLabel(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "auto":
		return "自动执行任何命令"
	case "readonly":
		return "自动执行只读命令"
	case "confirm", "":
		return "命令执行需要确认"
	default:
		return strings.TrimSpace(value)
	}
}

func fallback(value string, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func sanitizeFlowLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "<未配置>"
	}
	return "<已配置>"
}
