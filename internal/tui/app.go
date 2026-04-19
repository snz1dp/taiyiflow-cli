package tui

import (
	"context"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"regexp"
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
	Version             string
	BackendURL          string
	BackendAvailable    bool
	BackendStatusReason string
	ServerPackage       string
	ServerVersion       string
	DefaultFlow         string
	DefaultFlowLabel    string
	Workdir             string
	ApprovalMode        string
	SessionID           string
	SessionTitle        string
	History             []HistoryEntry
	WelcomeMessage      string
}

type HistoryEntry struct {
	Role    string
	Content string
}

type FlowItem struct {
	ID          string
	Name        string
	Description string
	Type        string
}

type SessionItem struct {
	ID           string
	Title        string
	RelativeTime string
	IsCurrent    bool
}

type SessionPickerState struct {
	Title      string
	Hint       string
	Page       int
	TotalPages int
	Items      []SessionItem
}

type ApprovalOption struct {
	Value       string
	Label       string
	Description string
	IsCurrent   bool
}

type ApprovalPickerState struct {
	Title   string
	Hint    string
	Options []ApprovalOption
}

type ConfigFormValues struct {
	BackendBaseURL string
	APIKey         string
	DefaultFlow    string
	ApprovalMode   string
	Workdir        string
	OutputLanguage string
}

type ConfigChoice struct {
	Value       string
	Label       string
	Description string
}

type ConfigFormState struct {
	Title       string
	Hint        string
	Path        string
	Values      ConfigFormValues
	HasAPIKey   bool
	SubmitText  string
	FlowOptions []ConfigChoice
}

type ConfigFormSubmission struct {
	Values      ConfigFormValues
	KeepAPIKey  bool
	ClearAPIKey bool
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
	Handled        bool
	ClearHistory   bool
	Events         []Event
	SessionPicker  *SessionPickerState
	ApprovalPicker *ApprovalPickerState
	ConfigForm     *ConfigFormState
	Snapshot       *Snapshot
	Flows          []FlowItem
	RebuildEntries bool
}

type Runner func(ctx context.Context, request Request, emit func(Event), confirm func(ConfirmRequest) bool) error

type FlowChangeHandler func(ctx context.Context, flowID string) (Snapshot, error)

type SessionChangeHandler func(ctx context.Context, snapshot Snapshot, sessionID string) (Snapshot, error)

type ApprovalChangeHandler func(ctx context.Context, mode string) (Snapshot, error)

type ConfigFormSubmitHandler func(ctx context.Context, snapshot Snapshot, submission ConfigFormSubmission) (LocalCommandResult, error)

type LocalCommandHandler func(ctx context.Context, command string, snapshot Snapshot) (LocalCommandResult, error)

type SnapshotRefreshHandler func(ctx context.Context, snapshot Snapshot) (Snapshot, []FlowItem, error)

type entry struct {
	role    string
	content string
}

type confirmState struct {
	request    ConfirmRequest
	responseCh chan bool
	cursor     int
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

type sessionChangedMsg struct {
	snapshot     Snapshot
	sessionID    string
	sessionTitle string
	err          error
}

type sessionPickerLoadedMsg struct {
	result LocalCommandResult
	err    error
}

type approvalChangedMsg struct {
	snapshot Snapshot
	mode     string
	err      error
}

type configFormSavedMsg struct {
	result LocalCommandResult
	err    error
}

type localCommandExecutedMsg struct {
	result LocalCommandResult
	err    error
}

type backendRetryTickMsg struct{}

type snapshotRefreshedMsg struct {
	snapshot Snapshot
	flows    []FlowItem
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
	progressSession    progressPhase = "session"
	progressApproval   progressPhase = "approval"
	progressFailed     progressPhase = "failed"
)

type progressState struct {
	phase   progressPhase
	label   string
	hint    string
	percent float64
}

type model struct {
	ctx              context.Context
	snapshot         Snapshot
	flows            []FlowItem
	runner           Runner
	refreshSnapshot  SnapshotRefreshHandler
	flowChange       FlowChangeHandler
	sessionChange    SessionChangeHandler
	approvalChange   ApprovalChangeHandler
	configFormSubmit ConfigFormSubmitHandler
	localCommand     LocalCommandHandler
	eventCh          chan tea.Msg
	entries          []entry
	input            textarea.Model
	flowFilter       textinput.Model
	viewport         viewport.Model
	progress         progress.Model
	markdown         *glamour.TermRenderer
	state            progressState
	activityHint     string
	requestStats     RequestMetrics
	width            int
	height           int
	contentWidth     int
	ready            bool
	quitting         bool
	showFlowPicker   bool
	sessionPicker    *SessionPickerState
	approvalPicker   *ApprovalPickerState
	configForm       *configFormState
	flowCursor       int
	sessionCursor    int
	approvalCursor   int
	commandCursor    int
	startupCommand   string
	pendingConfirm   *confirmState
	requestCancel    context.CancelFunc
	interrupting     bool
}

type configFormState struct {
	title          string
	hint           string
	path           string
	hasAPIKey      bool
	submitText     string
	inputs         []textinput.Model
	cursor         int
	flowOptions    []ConfigChoice
	flowCursor     int
	flowOpen       bool
	approvalCursor int
}

type configFieldSpec struct {
	label       string
	placeholder string
	description string
}

const (
	configFieldBackendBaseURL = iota
	configFieldAPIKey
	configFieldDefaultFlow
	configFieldApprovalMode
	configFieldWorkdir
	configFieldOutputLanguage
	configFieldCount
)

var (
	ansiCSIRegexp = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiOSCRegexp = regexp.MustCompile(`\x1b\][^\a]*(\a|\x1b\\)`)

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

	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#F1F8E9"))

	userMessageStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(lipgloss.Color("#66BB6A")).
				PaddingLeft(1).
				Foreground(lipgloss.Color("#C8E6C9"))

	assistantMessageStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(lipgloss.Color("#AED581"))

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

	transientStatusStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.NormalBorder()).
				BorderLeft(true).
				BorderForeground(lipgloss.Color("#4CAF50")).
				PaddingLeft(1).
				Foreground(lipgloss.Color("#A5D6A7"))

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

	backendAlertStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF8E1")).
				Background(lipgloss.Color("#B71C1C")).
				Bold(true).
				Padding(0, 1)
)

var slashCommands = []slashCommand{
	{Trigger: "/agents", Completion: "/agents ", Description: "打开智能体选择，或用 /agents <名称> 直接切换"},
	{Trigger: "/config", Completion: "/config ", Description: "查看或更新服务端地址、API Key 等配置"},
	{Trigger: "/new", Completion: "/new", Description: "重置当前智能体的本地会话"},
	{Trigger: "/session", Completion: "/session", Description: "显示当前会话状态"},
	{Trigger: "/sessions", Completion: "/sessions ", Description: "列出当前智能体的服务端会话，可附页码"},
	{Trigger: "/help", Completion: "/help", Description: "显示可用命令帮助"},
	{Trigger: "/clear", Completion: "/clear", Description: "清空当前界面消息"},
	{Trigger: "/exit", Completion: "/exit", Description: "退出当前智能对话"},
}

func Run(ctx context.Context, snapshot Snapshot, flows []FlowItem, runner Runner, refreshSnapshot SnapshotRefreshHandler, flowChange FlowChangeHandler, sessionChange SessionChangeHandler, approvalChange ApprovalChangeHandler, configFormSubmit ConfigFormSubmitHandler, localCommand LocalCommandHandler, startupCommand string) error {
	inputModel := textarea.New()
	if snapshot.BackendAvailable {
		inputModel.Placeholder = "输入内容后回车发送"
	} else {
		inputModel.Placeholder = "服务端不可用，当前为只读模式，仅支持 /config、/help、/clear、/exit"
	}
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
		ctx:              ctx,
		snapshot:         snapshot,
		flows:            flows,
		runner:           runner,
		refreshSnapshot:  refreshSnapshot,
		flowChange:       flowChange,
		sessionChange:    sessionChange,
		approvalChange:   approvalChange,
		configFormSubmit: configFormSubmit,
		localCommand:     localCommand,
		eventCh:          make(chan tea.Msg),
		entries:          initialEntries,
		input:            inputModel,
		flowFilter:       flowFilterModel,
		viewport:         viewportModel,
		progress:         progressModel,
		state:            newProgressState(progressIdle),
		flowCursor:       currentFlowIndex(flows, snapshot.DefaultFlow),
		startupCommand:   strings.TrimSpace(startupCommand),
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
	cmds := []tea.Cmd{textinput.Blink, m.input.Focus(), m.progress.SetPercent(m.state.percent), m.scheduleBackendRetry()}
	if strings.TrimSpace(m.startupCommand) != "" {
		cmds = append(cmds, executeLocalCommandCmd(m.ctx, m.snapshot, m.startupCommand, m.localCommand))
	}
	return tea.Batch(cmds...)
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
	case backendRetryTickMsg:
		if m.snapshot.BackendAvailable || m.refreshSnapshot == nil {
			return m, nil
		}
		return m, refreshSnapshotCmd(m.ctx, m.snapshot, m.refreshSnapshot)
	case snapshotRefreshedMsg:
		if typed.err != nil {
			if !m.snapshot.BackendAvailable {
				return m, m.scheduleBackendRetry()
			}
			return m, nil
		}
		previouslyAvailable := m.snapshot.BackendAvailable
		previousReason := strings.TrimSpace(m.snapshot.BackendStatusReason)
		m.snapshot = typed.snapshot
		m.flows = typed.flows
		m.syncInputPlaceholder()
		if !m.snapshot.BackendAvailable {
			m.showFlowPicker = false
			m.sessionPicker = nil
			m.approvalPicker = nil
			m.flowFilter.Blur()
			if previouslyAvailable {
				m.entries = append(m.entries, entry{role: "error", content: m.backendUnavailableMessage()})
			} else if strings.TrimSpace(m.snapshot.BackendStatusReason) != previousReason && strings.TrimSpace(m.snapshot.BackendStatusReason) != "" {
				m.entries = append(m.entries, entry{role: "system", content: "服务端状态更新: " + strings.TrimSpace(m.snapshot.BackendStatusReason)})
			}
			m.syncLayout()
			return m, m.scheduleBackendRetry()
		}
		if !previouslyAvailable {
			m.entries = append(m.entries, entry{role: "system", content: "服务端连接已恢复，已退出只读模式。"})
		}
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
		return m, tea.Batch(progressCmd, m.maybeRefreshSnapshot())
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
		return m, tea.Batch(progressCmd, m.maybeRefreshSessionSnapshot())
	case confirmRequestMsg:
		m.pendingConfirm = &confirmState{request: typed.request, responseCh: typed.responseCh, cursor: 0}
		m.input.Blur()
		return m, tea.Batch(m.transitionProgress(progressConfirming), waitForStream(m.eventCh))
	case flowChangedMsg:
		if typed.err != nil {
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			m.input.Focus()
			return m, tea.Batch(m.transitionProgress(progressFailed), m.maybeRefreshSnapshot())
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
	case sessionChangedMsg:
		if typed.err != nil {
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			m.input.Focus()
			return m, tea.Batch(m.transitionProgress(progressFailed), m.maybeRefreshSnapshot())
		}
		m.snapshot = typed.snapshot
		m.sessionPicker = nil
		m.activityHint = ""
		m.entries = buildInitialEntries(m.snapshot)
		m.entries = append(m.entries, entry{role: "system", content: RenderSessionStatusText(m.snapshot, "已切换会话")})
		m.syncViewport()
		m.input.Focus()
		return m, m.transitionProgress(progressIdle)
	case sessionPickerLoadedMsg:
		if typed.err != nil {
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			return m, m.maybeRefreshSnapshot()
		}
		m.applyLocalCommandResult(typed.result)
		return m, nil
	case approvalChangedMsg:
		if typed.err != nil {
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			m.input.Focus()
			return m, tea.Batch(m.transitionProgress(progressFailed), m.maybeRefreshSnapshot())
		}
		m.snapshot = typed.snapshot
		m.approvalPicker = nil
		m.approvalCursor = 0
		m.input.Focus()
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("已切换命令执行模式: %s", approvalModeLabel(typed.mode))})
		m.syncViewport()
		if m.state.phase == progressApproval {
			return m, m.transitionProgress(progressIdle)
		}
		return m, nil
	case configFormSavedMsg:
		if typed.err != nil {
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			return m, tea.Batch(m.transitionProgress(progressFailed), textinput.Blink)
		}
		m.closeConfigForm()
		m.applyLocalCommandResult(typed.result)
		return m, tea.Batch(m.transitionProgress(progressIdle), textinput.Blink)
	case localCommandExecutedMsg:
		if typed.err != nil {
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			return m, tea.Batch(m.transitionProgress(progressFailed), textinput.Blink)
		}
		m.applyLocalCommandResult(typed.result)
		return m, tea.Batch(m.transitionProgress(progressIdle), textinput.Blink)
	case tea.KeyMsg:
		if m.pendingConfirm != nil {
			return m.handleConfirmKey(typed)
		}
		if m.configForm != nil {
			return m.handleConfigFormKey(typed)
		}
		if m.approvalPicker != nil {
			return m.handleApprovalPickerKey(typed)
		}
		if m.sessionPicker != nil {
			return m.handleSessionPickerKey(typed)
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
		if !m.snapshot.BackendAvailable && !isAvailableInReadOnlyMode(input) {
			m.entries = append(m.entries, entry{role: "error", content: m.backendUnavailableMessage()})
			m.input.SetValue("")
			m.syncViewport()
			return m, nil
		}
		if strings.HasPrefix(input, "!") {
			if commandHandled, cmd := m.handleLocalSlashCommand(input); commandHandled {
				return m, cmd
			}
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
	case "left", "h", "up", "k":
		if m.pendingConfirm.cursor > 0 {
			m.pendingConfirm.cursor--
		}
		return m, nil
	case "right", "l", "down", "j", "tab":
		if m.pendingConfirm.cursor < 2 {
			m.pendingConfirm.cursor++
		} else {
			m.pendingConfirm.cursor = 0
		}
		return m, nil
	case "y", "Y":
		m.pendingConfirm.cursor = 0
		m.pendingConfirm.responseCh <- true
		close(m.pendingConfirm.responseCh)
		m.pendingConfirm = nil
		return m, tea.Batch(m.transitionProgress(progressExecuting), waitForStream(m.eventCh))
	case "a", "A":
		m.pendingConfirm.cursor = 2
		m.pendingConfirm.responseCh <- true
		close(m.pendingConfirm.responseCh)
		m.pendingConfirm = nil
		if m.approvalChange != nil {
			return m, tea.Batch(changeApprovalModeCmd(m.ctx, "auto", m.approvalChange), m.transitionProgress(progressExecuting), waitForStream(m.eventCh))
		}
		return m, tea.Batch(m.transitionProgress(progressExecuting), waitForStream(m.eventCh))
	case "enter":
		approved := m.pendingConfirm.cursor != 1
		alwaysApprove := m.pendingConfirm.cursor == 2
		m.pendingConfirm.responseCh <- approved
		close(m.pendingConfirm.responseCh)
		m.pendingConfirm = nil
		if approved {
			if alwaysApprove && m.approvalChange != nil {
				return m, tea.Batch(changeApprovalModeCmd(m.ctx, "auto", m.approvalChange), m.transitionProgress(progressExecuting), waitForStream(m.eventCh))
			}
			return m, tea.Batch(m.transitionProgress(progressExecuting), waitForStream(m.eventCh))
		}
		return m, tea.Batch(m.transitionProgress(progressStreaming), waitForStream(m.eventCh))
	case "n", "N", "q", "esc", "ctrl+c":
		m.pendingConfirm.cursor = 1
		m.pendingConfirm.responseCh <- false
		close(m.pendingConfirm.responseCh)
		m.pendingConfirm = nil
		return m, tea.Batch(m.transitionProgress(progressStreaming), waitForStream(m.eventCh))
	default:
		return m, nil
	}
}

func (m model) handleApprovalPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.approvalPicker == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.closeApprovalPicker()
		return m, nil
	case "up", "k":
		if m.approvalCursor > 0 {
			m.approvalCursor--
		}
		return m, nil
	case "down", "j", "tab":
		if len(m.approvalPicker.Options) > 0 {
			m.approvalCursor = (m.approvalCursor + 1) % len(m.approvalPicker.Options)
		}
		return m, nil
	case "enter":
		if m.approvalPicker == nil || len(m.approvalPicker.Options) == 0 || m.approvalChange == nil {
			m.closeApprovalPicker()
			return m, nil
		}
		selected := m.approvalPicker.Options[m.approvalCursor]
		return m, tea.Batch(changeApprovalModeCmd(m.ctx, selected.Value, m.approvalChange), m.transitionProgress(progressApproval))
	default:
		return m, nil
	}
}

func (m model) handleConfigFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.configForm == nil {
		return m, nil
	}
	if m.configForm.flowOpen {
		return m.handleConfigFlowDropdownKey(msg)
	}

	switch msg.String() {
	case "esc", "q":
		m.closeConfigForm()
		return m, nil
	case "ctrl+c":
		if activeConfigFormValue(m.configForm) != "" {
			var cmd tea.Cmd
			m.configForm.inputs[m.configForm.cursor], cmd = m.configForm.inputs[m.configForm.cursor].Update(msg)
			m.syncLayout()
			return m, cmd
		}
		m.closeConfigForm()
		return m, nil
	case "shift+tab", "up", "k":
		m.moveConfigFormCursor(-1)
		return m, nil
	case "tab", "down", "j":
		m.moveConfigFormCursor(1)
		return m, nil
	case "left", "h":
		if m.configForm.cursor == configFieldApprovalMode {
			m.moveConfigApprovalCursor(-1)
			return m, nil
		}
		if m.configForm.cursor == configFieldDefaultFlow {
			m.moveConfigFlowCursor(-1)
			return m, nil
		}
	case "right", "l":
		if m.configForm.cursor == configFieldApprovalMode {
			m.moveConfigApprovalCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldDefaultFlow {
			m.moveConfigFlowCursor(1)
			return m, nil
		}
	case "enter":
		if m.configForm.cursor == configFieldDefaultFlow {
			m.openConfigFlowDropdown()
			return m, nil
		}
		if m.configForm.cursor < configFieldCount-1 {
			m.moveConfigFormCursor(1)
			return m, nil
		}
		return m.submitConfigForm()
	case " ":
		if m.configForm.cursor == configFieldDefaultFlow {
			m.openConfigFlowDropdown()
			return m, nil
		}
		if m.configForm.cursor == configFieldApprovalMode {
			m.moveConfigApprovalCursor(1)
			return m, nil
		}
	case "ctrl+s":
		return m.submitConfigForm()
	default:
		if !isConfigTextField(m.configForm.cursor) {
			return m, nil
		}
		var cmd tea.Cmd
		m.configForm.inputs[m.configForm.cursor], cmd = m.configForm.inputs[m.configForm.cursor].Update(msg)
		m.syncLayout()
		return m, cmd
	}
	return m, nil
}

func (m model) handleConfigFlowDropdownKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.configForm == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.configForm.flowOpen = false
		m.syncLayout()
		return m, nil
	case "up", "k":
		m.moveConfigFlowCursor(-1)
		return m, nil
	case "down", "j", "tab":
		m.moveConfigFlowCursor(1)
		return m, nil
	case "enter", " ":
		m.configForm.flowOpen = false
		m.syncLayout()
		return m, nil
	default:
		return m, nil
	}
}

func (m model) handleSessionPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sessionPicker == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.closeSessionPicker()
		return m, nil
	case "up", "k":
		if m.sessionCursor > 0 {
			m.sessionCursor--
		}
		return m, nil
	case "down", "j":
		if m.sessionCursor < len(m.sessionPicker.Items)-1 {
			m.sessionCursor++
		}
		return m, nil
	case "left", "h", "pgup":
		if m.sessionPicker.Page > 1 {
			return m, m.loadSessionPickerPage(m.sessionPicker.Page - 1)
		}
		return m, nil
	case "right", "l", "pgdown":
		if m.sessionPicker.Page < m.sessionPicker.TotalPages {
			return m, m.loadSessionPickerPage(m.sessionPicker.Page + 1)
		}
		return m, nil
	case "tab":
		if len(m.sessionPicker.Items) > 0 {
			m.sessionCursor = (m.sessionCursor + 1) % len(m.sessionPicker.Items)
		}
		return m, nil
	case "enter":
		if len(m.sessionPicker.Items) == 0 || m.sessionChange == nil {
			m.closeSessionPicker()
			return m, nil
		}
		selected := m.sessionPicker.Items[m.sessionCursor]
		return m, tea.Batch(m.transitionProgress(progressSession), changeSessionCmd(m.ctx, m.snapshot, selected.ID, selected.Title, m.sessionChange))
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
		if strings.TrimSpace(event.SessionID) == "" {
			m.snapshot.SessionTitle = ""
		}
	}
	m.syncViewport()
	return cmd
}

func RenderSessionStatusText(snapshot Snapshot, prefix string) string {
	lines := []string{}
	if strings.TrimSpace(prefix) != "" {
		lines = append(lines, prefix)
	}
	sessionID := strings.TrimSpace(snapshot.SessionID)
	if sessionID == "" {
		lines = append(lines, "当前会话: 新会话")
		return strings.Join(lines, "\n")
	}
	lines = append(lines, fmt.Sprintf("当前会话: %s", sessionID))
	if title := strings.TrimSpace(snapshot.SessionTitle); title != "" {
		lines = append(lines, fmt.Sprintf("会话标题: %s", title))
	}
	return strings.Join(lines, "\n")
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

	historyWidth := m.historyPaneWidth()
	viewportWidth := historyWidth
	if !m.useMinimalChrome() {
		viewportWidth = historyWidth - 4
	}
	if viewportWidth < 20 {
		viewportWidth = 20
	}
	inputHeight := m.currentInputHeight()
	alertHeight := 0
	if !m.snapshot.BackendAvailable {
		alertHeight = 1
	}
	headerHeight := 2
	if m.useMinimalChrome() {
		headerHeight = 4
	}
	historyFrameHeight := 2
	if m.useMinimalChrome() {
		historyFrameHeight = 0
	}
	inputBoxHeight := inputHeight + 2
	statusBarHeight := 1

	historyHeight := m.height - (alertHeight + headerHeight + historyFrameHeight + inputBoxHeight + statusBarHeight)
	if historyHeight < 3 {
		historyHeight = 3
	}
	m.viewport.Width = viewportWidth
	m.viewport.Height = historyHeight
	m.updateMarkdownRenderer(viewportWidth - 2)
	inputWidth := historyWidth - 4
	if inputWidth <= 0 {
		inputWidth = contentWidth - 10
	}
	if inputWidth < 20 {
		inputWidth = 20
	}
	m.input.SetWidth(inputWidth)
	m.input.SetHeight(inputHeight)
	m.flowFilter.Width = minInt(contentWidth-16, 48)
	if m.flowFilter.Width < 18 {
		m.flowFilter.Width = 18
	}
	if m.configForm != nil {
		formWidth := minInt(contentWidth-16, 72)
		if formWidth < 24 {
			formWidth = 24
		}
		for index := range m.configForm.inputs {
			m.configForm.inputs[index].Width = formWidth
		}
	}
	m.progress.Width = contentWidth - 18
	if m.progress.Width < 12 {
		m.progress.Width = 12
	}
	m.syncViewport()
}

func (m *model) syncViewport() {
	content := renderEntries(m.entries, m.viewport.Width, m.viewport.Height, m.markdown, m.snapshot)
	if transient := m.renderTransientStatus(); transient != "" {
		if strings.TrimSpace(content) != "" {
			content += "\n\n"
		}
		content += transient
	}
	m.viewport.SetContent(content)
	if m.shouldPinHistoryTop() {
		m.viewport.GotoTop()
		return
	}
	m.viewport.GotoBottom()
}

func (m *model) refreshLayoutAfterStateChange() {
	if m.ready && m.width > 0 && m.height > 0 {
		m.syncLayout()
		return
	}
	if m.viewport.Width > 0 && m.viewport.Height > 0 {
		m.syncViewport()
	}
}

func (m model) View() string {
	if m.quitting {
		return "\n"
	}

	topParts := []string{}
	if alert := m.renderBackendAlertBar(); alert != "" {
		topParts = append(topParts, alert)
	}
	header := m.renderHeader()
	if strings.TrimSpace(header) == "" {
		header = headerBarStyle.Width(maxInt(40, m.contentWidth)).Render("太乙智启")
	}
	topParts = append(topParts, header)
	topBlock := lipgloss.JoinVertical(lipgloss.Left, topParts...)

	bodyParts := []string{}
	history := m.renderHistoryPane()
	inputView := m.input.View()
	if m.pendingConfirm != nil || m.configForm != nil || m.showFlowPicker || m.sessionPicker != nil || m.approvalPicker != nil {
		inputView = hintStyle.Render("当前有未处理的弹层，请先完成操作。")
	} else if commandHints := m.renderSlashCommandHints(); commandHints != "" {
		inputView = lipgloss.JoinVertical(lipgloss.Left, inputView, "", commandHints)
	}
	inputBoxWidth := m.historyPaneWidth()
	if inputBoxWidth <= 0 {
		inputBoxWidth = m.contentWidth - 6
	}
	if inputBoxWidth < 20 {
		inputBoxWidth = 20
	}
	inputBox := inputStyle.Width(inputBoxWidth).Render(inputView)
	bodyParts = append(bodyParts, history, inputBox, m.renderStatusBar())
	bodyContent := lipgloss.JoinVertical(lipgloss.Left, bodyParts...)
	content := lipgloss.JoinVertical(lipgloss.Left, topBlock, bodyContent)
	if !m.ready || m.width == 0 {
		return "\n" + content + "\n"
	}

	topPlaced := lipgloss.Place(m.width, lipgloss.Height(topBlock), lipgloss.Center, lipgloss.Top, topBlock)
	bodyHeight := m.height - lipgloss.Height(topPlaced)
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	bodyPlaced := lipgloss.Place(m.width, bodyHeight, lipgloss.Center, lipgloss.Top, bodyContent)
	base := lipgloss.JoinVertical(lipgloss.Left, topPlaced, bodyPlaced)
	if overlay := m.renderOverlay(); overlay != "" {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, overlay)
	}

	return base
}

func (m model) renderBackendAlertBar() string {
	if m.snapshot.BackendAvailable {
		return ""
	}
	reason := strings.TrimSpace(m.snapshot.BackendStatusReason)
	if reason == "" {
		reason = "当前无法连接服务端"
	}
	message := joinStatusSegments([]string{"服务端未连接", reason, "每 5 秒自动重试"}, "  ·  ")
	width := m.width
	if width <= 0 {
		width = m.contentWidth
	}
	if width <= 0 {
		width = 60
	}
	return backendAlertStyle.Width(width).Render(trimForWidth(message, width-2))
}

func (m model) renderHeader() string {
	renderWidth := m.width
	if renderWidth <= 0 {
		renderWidth = m.contentWidth
	}
	if renderWidth <= 0 {
		renderWidth = 80
	}
	flowLabel := fallback(m.snapshot.DefaultFlowLabel, "未配置")
	clientVersion := fallback(m.snapshot.Version, "0.1.0")
	serverVersion := formatHeaderServerVersion(m.serverVersionLabel())
	sessionTitle := strings.TrimSpace(m.snapshot.SessionTitle)
	if sessionTitle == "" {
		sessionTitle = fallback(m.snapshot.SessionID, "新会话")
	}
	workdir := fallback(strings.TrimSpace(m.snapshot.Workdir), ".")
	flowLabelDisplay := trimForWidth(flowLabel, maxInt(8, renderWidth/6))
	versionLine := joinStatusSegments([]string{
		"CLI v" + clientVersion,
		serverVersion,
	}, "  ·  ")
	contentWidth := maxInt(18, renderWidth-2)
	workdirWidth := maxInt(24, contentWidth/2)
	workdirDisplay := trimLeftForWidth(workdir, workdirWidth)
	if m.useMinimalChrome() {
		lines := []string{
			trimForWidth(sessionTitle, contentWidth),
			trimForWidth(versionLine, contentWidth),
			trimForWidth(flowLabelDisplay, contentWidth),
			trimLeftForWidth(workdir, contentWidth),
		}
		return headerBarStyle.Width(renderWidth).Render(strings.Join(lines, "\n"))
	}
	line1 := renderHeaderLine(sessionTitle, flowLabelDisplay, contentWidth, false)
	line2 := renderHeaderLine(workdirDisplay, versionLine, contentWidth, true)
	return headerBarStyle.Width(renderWidth).Render(strings.Join([]string{line1, line2}, "\n"))
}

func renderHeaderLine(left string, right string, width int, preferRight bool) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if width <= 0 {
		return ""
	}
	if left == "" {
		return trimForWidth(right, width)
	}
	if right == "" {
		return trimForWidth(left, width)
	}

	gapWidth := 2
	available := width - gapWidth
	if available <= 0 {
		return trimForWidth(left+" "+right, width)
	}

	rightWidth := lipgloss.Width(right)
	if preferRight {
		maxRightWidth := available - 12
		if maxRightWidth < 18 {
			maxRightWidth = available
		}
		if rightWidth > maxRightWidth {
			rightWidth = maxRightWidth
		}
	} else {
		maxRightWidth := maxInt(18, available/3)
		if maxRightWidth > available {
			maxRightWidth = available
		}
		if rightWidth > maxRightWidth {
			rightWidth = maxRightWidth
		}
	}

	rightRendered := trimForWidth(right, rightWidth)
	leftWidth := available - lipgloss.Width(rightRendered)
	if leftWidth < 0 {
		leftWidth = 0
	}
	leftRendered := ""
	if leftWidth > 0 {
		leftRendered = trimLeftForWidth(left, leftWidth)
	}
	if strings.TrimSpace(leftRendered) == "" {
		return trimForWidth(rightRendered, width)
	}

	spaces := width - lipgloss.Width(leftRendered) - lipgloss.Width(rightRendered)
	if spaces < gapWidth {
		spaces = gapWidth
	}
	return leftRendered + strings.Repeat(" ", spaces) + rightRendered
}

func formatHeaderServerVersion(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "SERVER v0.1.0"
	}
	if strings.Contains(trimmed, "服务") {
		return strings.ReplaceAll(trimmed, "服务", "SERVER")
	}
	return "SERVER " + trimmed
}

func (m model) renderHistoryPane() string {
	width := m.historyPaneWidth()
	if m.useMinimalChrome() {
		return minimalHistoryStyle.Width(width).Render(m.viewport.View())
	}
	return historyFrameStyle.Width(width).Render(m.viewport.View())
}

func (m model) historyPaneWidth() int {
	width := m.width
	if !m.useMinimalChrome() {
		width = m.width - 2
	}
	if width < 20 {
		width = 20
	}
	return width
}

func (m model) renderTransientStatus() string {
	status := strings.TrimSpace(m.activityHint)
	if status == "" {
		return ""
	}
	width := m.viewport.Width - 2
	if width < 18 {
		width = 18
	}
	lines := []string{
		trimForWidth(status, width),
	}
	return transientStatusStyle.Width(width).Render(strings.Join(lines, "\n"))
}

func (m model) renderStatusBar() string {
	segments := m.statusBarSegments()
	stateLabel := m.state.label
	if !m.snapshot.BackendAvailable && !m.isBusy() {
		stateLabel = "只读模式"
	}
	renderWidth := m.width
	if renderWidth <= 0 {
		renderWidth = m.contentWidth
	}
	if renderWidth <= 0 {
		renderWidth = 80
	}
	stateChip := statusAccentStyle.Render(stateLabel)
	bodyWidth := renderWidth - lipgloss.Width(stateChip) - 3
	if bodyWidth < 12 {
		bodyWidth = 12
	}
	body := trimForWidth(joinStatusSegments(segments, "  ·  "), bodyWidth)
	line := lipgloss.JoinHorizontal(lipgloss.Left, stateChip, " ", statusBarDimStyle.Render(body))
	return statusBarStyle.Width(renderWidth).Render(trimForWidth(line, renderWidth-2))
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
		return overlayStyle.Width(minInt(m.contentWidth-6, 90)).Render(m.renderConfirmOverlay())
	}
	if m.configForm != nil {
		return overlayStyle.Width(minInt(m.contentWidth-6, 92)).Render(m.renderConfigForm())
	}
	if m.approvalPicker != nil {
		return overlayStyle.Width(minInt(m.contentWidth-6, 90)).Render(m.renderApprovalPicker())
	}
	if m.sessionPicker != nil {
		return overlayStyle.Width(minInt(m.contentWidth-6, 90)).Render(m.renderSessionPicker())
	}
	if m.showFlowPicker {
		return overlayStyle.Width(minInt(m.contentWidth-6, 90)).Render(m.renderFlowPicker())
	}
	return ""
}

func (m model) renderConfirmOverlay() string {
	request := m.pendingConfirm.request
	lines := []string{request.Title}
	if strings.TrimSpace(request.Command) != "" {
		lines = append(lines, "命令: "+request.Command)
	}
	if strings.TrimSpace(request.Detail) != "" {
		lines = append(lines, request.Detail)
	}
	options := []string{
		m.renderOverlayOption("确认执行", m.pendingConfirm.cursor == 0),
		m.renderOverlayOption("拒绝", m.pendingConfirm.cursor == 1),
		m.renderOverlayOption("以后都同意", m.pendingConfirm.cursor == 2),
	}
	lines = append(lines, "", lipgloss.JoinHorizontal(lipgloss.Left, options[0], "  ", options[1], "  ", options[2]), "使用方向键或 Tab 选择，Enter 确认。")
	return strings.Join(lines, "\n\n")
}

func (m model) renderApprovalPicker() string {
	if m.approvalPicker == nil {
		return ""
	}
	lines := []string{
		m.approvalPicker.Title,
		fallback(strings.TrimSpace(m.approvalPicker.Hint), "使用方向键选择，Enter 确认，Esc 关闭。"),
		"",
	}
	for index, option := range m.approvalPicker.Options {
		line := fmt.Sprintf("%s  %s", flowCursor(index == m.approvalCursor), option.Label)
		if option.IsCurrent {
			line += "  [当前]"
		}
		if index == m.approvalCursor {
			line = selectedFlowStyle.Render(trimForWidth(line, m.contentWidth-16))
		} else {
			line = commandHintStyle.Render(trimForWidth(line, m.contentWidth-16))
		}
		lines = append(lines, line)
		if strings.TrimSpace(option.Description) != "" {
			lines = append(lines, hintStyle.Render(trimForWidth(option.Description, m.contentWidth-16)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) renderConfigForm() string {
	if m.configForm == nil {
		return ""
	}

	lines := []string{fallback(strings.TrimSpace(m.configForm.title), "配置设置")}
	if strings.TrimSpace(m.configForm.hint) != "" {
		lines = append(lines, strings.TrimSpace(m.configForm.hint))
	}
	if strings.TrimSpace(m.configForm.path) != "" {
		lines = append(lines, hintStyle.Render(trimForWidth("配置文件: "+strings.TrimSpace(m.configForm.path), m.contentWidth-14)))
	}
	lines = append(lines, "")

	for index, spec := range configFormFieldSpecs(m.configForm.hasAPIKey) {
		label := spec.label
		if index == m.configForm.cursor {
			label = selectedFlowStyle.Render(trimForWidth(label, m.contentWidth-16))
		} else {
			label = labelStyle.Render(trimForWidth(label, m.contentWidth-16))
		}
		lines = append(lines, label)
		switch index {
		case configFieldDefaultFlow:
			lines = append(lines, m.renderConfigFlowField())
			if m.configForm.flowOpen {
				lines = append(lines, m.renderConfigFlowDropdown())
			}
		case configFieldApprovalMode:
			lines = append(lines, m.renderConfigApprovalField())
		default:
			lines = append(lines, m.configForm.inputs[index].View())
		}
		if strings.TrimSpace(spec.description) != "" {
			lines = append(lines, hintStyle.Render(trimForWidth(spec.description, m.contentWidth-16)))
		}
		lines = append(lines, "")
	}

	submitText := strings.TrimSpace(m.configForm.submitText)
	if submitText == "" {
		submitText = "保存配置"
	}
	lines = append(lines,
		commandHintStyle.Render(submitText+"，Esc 关闭，Tab 切换字段，Enter 下一项，Ctrl+S 保存。"),
	)
	return strings.Join(lines, "\n")
}

func (m model) renderConfigFlowField() string {
	if m.configForm == nil {
		return ""
	}
	selected := configChoiceLabel(m.configForm.flowOptions, m.configForm.flowCursor, "自动选择")
	line := fmt.Sprintf("%s  %s", flowCursor(m.configForm.cursor == configFieldDefaultFlow), selected)
	if m.configForm.flowOpen {
		line += "  [展开中]"
	} else {
		line += "  [Enter 选择]"
	}
	if m.configForm.cursor == configFieldDefaultFlow {
		return selectedFlowStyle.Render(trimForWidth(line, m.contentWidth-16))
	}
	return commandHintStyle.Render(trimForWidth(line, m.contentWidth-16))
}

func (m model) renderConfigFlowDropdown() string {
	if m.configForm == nil || len(m.configForm.flowOptions) == 0 {
		return hintStyle.Render("暂无可选智能体。")
	}
	lines := []string{hintStyle.Render("选择默认智能体：上下键移动，Enter 确认。")}
	for index, option := range m.configForm.flowOptions {
		line := fmt.Sprintf("%s  %s", flowCursor(index == m.configForm.flowCursor), option.Label)
		if index == m.configForm.flowCursor {
			lines = append(lines, selectedFlowStyle.Render(trimForWidth(line, m.contentWidth-20)))
		} else {
			lines = append(lines, commandHintStyle.Render(trimForWidth(line, m.contentWidth-20)))
		}
		if description := strings.TrimSpace(option.Description); description != "" && index == m.configForm.flowCursor {
			lines = append(lines, hintStyle.Render(trimForWidth(description, m.contentWidth-20)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) renderConfigApprovalField() string {
	if m.configForm == nil {
		return ""
	}
	parts := make([]string, 0, len(configApprovalChoices()))
	for index, option := range configApprovalChoices() {
		selected := index == m.configForm.approvalCursor
		marker := "( )"
		if selected {
			marker = "(*)"
		}
		item := marker + " " + option.Label
		if m.configForm.cursor == configFieldApprovalMode && selected {
			parts = append(parts, selectedFlowStyle.Render(item))
		} else if selected {
			parts = append(parts, valueStyle.Render(item))
		} else {
			parts = append(parts, commandHintStyle.Render(item))
		}
	}
	return strings.Join(parts, "  ")
}

func (m model) renderSessionPicker() string {
	if m.sessionPicker == nil {
		return ""
	}
	lines := []string{
		m.sessionPicker.Title,
		fallback(strings.TrimSpace(m.sessionPicker.Hint), "使用方向键选择，左右键翻页，Enter 切换当前会话，Esc 关闭。"),
		fmt.Sprintf("第 %d / %d 页", m.sessionPicker.Page, m.sessionPicker.TotalPages),
		"",
	}
	if len(m.sessionPicker.Items) == 0 {
		lines = append(lines, hintStyle.Render("当前页没有会话。"))
		return strings.Join(lines, "\n")
	}
	for index, item := range m.sessionPicker.Items {
		line := fmt.Sprintf("%s  %s", flowCursor(index == m.sessionCursor), item.Title)
		if strings.TrimSpace(item.RelativeTime) != "" {
			line += "  " + item.RelativeTime
		}
		if item.IsCurrent {
			line += "  [当前]"
		}
		if index == m.sessionCursor {
			line = selectedFlowStyle.Render(trimForWidth(line, m.contentWidth-16))
		} else {
			line = commandHintStyle.Render(trimForWidth(line, m.contentWidth-16))
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m model) renderOverlayOption(label string, selected bool) string {
	if selected {
		return selectedFlowStyle.Render(label)
	}
	return commandHintStyle.Render(label)
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
	if !m.snapshot.BackendAvailable {
		segments = append(segments, backendStatusLabel(m.snapshot))
	}
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

func buildBackendStatusEntries(snapshot Snapshot) []entry {
	if snapshot.BackendAvailable {
		return nil
	}
	message := "当前无法连接服务端，已进入只读模式。"
	if reason := strings.TrimSpace(snapshot.BackendStatusReason); reason != "" {
		message += "\n原因: " + reason
	}
	message += "\n可用命令: /config /help /clear /exit"
	return []entry{{role: "error", content: message}}
}

func (m model) backendUnavailableMessage() string {
	message := "当前无法连接服务端，已进入只读模式。"
	if reason := strings.TrimSpace(m.snapshot.BackendStatusReason); reason != "" {
		message += " 原因: " + reason
	}
	message += " 可用命令: /config /help /clear /exit"
	return message
}

func isAvailableInReadOnlyMode(input string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(input))
	switch {
	case trimmed == "/help", trimmed == "/clear", trimmed == "/exit":
		return true
	case trimmed == "/config", strings.HasPrefix(trimmed, "/config "):
		return true
	default:
		return false
	}
}

func backendStatusLabel(snapshot Snapshot) string {
	if snapshot.BackendAvailable {
		return "服务端已连接"
	}
	return "服务端未连接"
}

func welcomeCommandsLabel(snapshot Snapshot) []string {
	labelValues := []string{
		" _____     _       _ _____ _",
		"|_   _|_ _(_)_   _(_)  ___| | _____      __",
		"  | |/ _` | | | | | | |_  | |/ _ \\ \\ /\\ / /",
		"  | | (_| | | |_| | |  _| | | (_) \\ V  V /",
		"  |_|\\__,_|_|\\__, |_|_|   |_|\\___/ \\_/\\_/",
		"             |___/",
		"  :: Snz1DP ::         CLI v" + fallback(snapshot.Version, "0.1.0"),
		"",
	}
	labelValue := "常用命令："
	if snapshot.BackendAvailable {
		labelValues = append(labelValues, labelValue+"/agents /config /session /new /exit")
	} else {
		labelValues = append(labelValues, labelValue+"/config /help /clear /exit")
	}
	return labelValues
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
	content := sanitizeTerminalText(item.content)
	textWidth := maxInt(10, width-4)
	wrapped := hardWrapText(content, textWidth)
	switch item.role {
	case "welcome":
		_ = height
		lines := append([]string{}, welcomeCommandsLabel(snapshot)...)
		if strings.TrimSpace(content) != "" {
			lines = append(lines, strings.TrimSpace(content))
		}
		return systemMessageStyle.Render(strings.Join(lines, "\n"))
	case "user":
		return userMessageStyle.Render(wrapped)
	case "assistant":
		rendered := renderMarkdown(hardWrapText(content, textWidth), textWidth, renderer)
		return assistantMessageStyle.Render(rendered)
	case "error":
		return errorMessageStyle.Render(wrapped)
	default:
		return systemMessageStyle.Render(wrapped)
	}
}

func sanitizeTerminalText(content string) string {
	if content == "" {
		return ""
	}
	cleaned := ansiOSCRegexp.ReplaceAllString(content, "")
	cleaned = ansiCSIRegexp.ReplaceAllString(cleaned, "")
	cleaned = strings.ReplaceAll(cleaned, "\x1b", "")
	return cleaned
}

func hardWrapText(content string, width int) string {
	if width <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	wrapped := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}

		prefix := leadingIndent(line)
		body := strings.TrimPrefix(line, prefix)
		if body == "" {
			wrapped = append(wrapped, prefix)
			continue
		}

		bodyWidth := width - lipgloss.Width(prefix)
		if bodyWidth < 1 {
			bodyWidth = 1
		}

		runes := []rune(body)
		segment := make([]rune, 0, len(runes))
		for _, current := range runes {
			candidate := string(append(segment, current))
			if lipgloss.Width(candidate) > bodyWidth {
				if len(segment) == 0 {
					wrapped = append(wrapped, prefix+string(current))
					continue
				}
				wrapped = append(wrapped, prefix+string(segment))
				segment = []rune{current}
				continue
			}
			segment = append(segment, current)
		}
		if len(segment) > 0 {
			wrapped = append(wrapped, prefix+string(segment))
		}
	}
	return strings.Join(wrapped, "\n")
}

func leadingIndent(line string) string {
	for i, r := range line {
		if r != ' ' && r != '\t' {
			return line[:i]
		}
	}
	return line
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
	if !m.snapshot.BackendAvailable {
		m.entries = append(m.entries, entry{role: "error", content: m.backendUnavailableMessage()})
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
		m.refreshLayoutAfterStateChange()
		return true, nil
	}
	if !result.Handled {
		m.refreshLayoutAfterStateChange()
		return false, nil
	}
	m.applyLocalCommandResult(result)
	return true, nil
}

func (m *model) applyLocalCommandResult(result LocalCommandResult) {
	if result.ClearHistory {
		m.entries = buildWelcomeEntries(m.snapshot)
		m.activityHint = ""
		m.requestStats = RequestMetrics{}
	}
	if result.ConfigForm != nil {
		m.configForm = newConfigFormState(*result.ConfigForm)
		m.approvalPicker = nil
		m.sessionPicker = nil
		m.showFlowPicker = false
		m.flowFilter.Blur()
		m.input.Blur()
		m.syncLayout()
	}
	if result.Snapshot != nil {
		m.snapshot = *result.Snapshot
		m.flows = result.Flows
		m.flowCursor = currentFlowIndex(m.flows, m.snapshot.DefaultFlow)
		m.syncInputPlaceholder()
		if !m.snapshot.BackendAvailable {
			m.showFlowPicker = false
			m.sessionPicker = nil
			m.approvalPicker = nil
			m.flowFilter.Blur()
		}
		if result.RebuildEntries {
			m.entries = buildInitialEntries(m.snapshot)
			m.activityHint = ""
			m.requestStats = RequestMetrics{}
		}
	}
	if result.SessionPicker != nil {
		m.sessionPicker = result.SessionPicker
		m.sessionCursor = 0
		m.approvalPicker = nil
		m.configForm = nil
		m.input.Blur()
	}
	if result.ApprovalPicker != nil {
		m.approvalPicker = result.ApprovalPicker
		m.approvalCursor = 0
		m.sessionPicker = nil
		m.configForm = nil
		m.input.Blur()
	}
	for _, event := range result.Events {
		m.applyEvent(event)
	}
	m.refreshLayoutAfterStateChange()
}

func (m model) showSlashCommandHints() bool {
	if m.pendingConfirm != nil || m.configForm != nil || m.showFlowPicker || m.sessionPicker != nil || m.approvalPicker != nil || m.isBusy() {
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
		if !m.snapshot.BackendAvailable && !isAvailableInReadOnlyMode(command.Trigger) {
			continue
		}
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

func refreshSnapshotCmd(ctx context.Context, snapshot Snapshot, handler SnapshotRefreshHandler) tea.Cmd {
	if handler == nil {
		return nil
	}
	return func() tea.Msg {
		updated, flows, err := handler(ctx, snapshot)
		return snapshotRefreshedMsg{snapshot: updated, flows: flows, err: err}
	}
}

func changeSessionCmd(ctx context.Context, snapshot Snapshot, sessionID string, sessionTitle string, handler SessionChangeHandler) tea.Cmd {
	return func() tea.Msg {
		updated, err := handler(ctx, snapshot, sessionID)
		return sessionChangedMsg{snapshot: updated, sessionID: sessionID, sessionTitle: sessionTitle, err: err}
	}
}

func loadSessionPickerCmd(ctx context.Context, snapshot Snapshot, command string, handler LocalCommandHandler) tea.Cmd {
	return func() tea.Msg {
		result, err := handler(ctx, command, snapshot)
		return sessionPickerLoadedMsg{result: result, err: err}
	}
}

func changeApprovalModeCmd(ctx context.Context, mode string, handler ApprovalChangeHandler) tea.Cmd {
	return func() tea.Msg {
		snapshot, err := handler(ctx, mode)
		return approvalChangedMsg{snapshot: snapshot, mode: mode, err: err}
	}
}

func submitConfigFormCmd(ctx context.Context, snapshot Snapshot, submission ConfigFormSubmission, handler ConfigFormSubmitHandler) tea.Cmd {
	if handler == nil {
		return nil
	}
	return func() tea.Msg {
		result, err := handler(ctx, snapshot, submission)
		return configFormSavedMsg{result: result, err: err}
	}
}

func executeLocalCommandCmd(ctx context.Context, snapshot Snapshot, command string, handler LocalCommandHandler) tea.Cmd {
	if handler == nil {
		return nil
	}
	return func() tea.Msg {
		result, err := handler(ctx, command, snapshot)
		return localCommandExecutedMsg{result: result, err: err}
	}
}

func (m *model) loadSessionPickerPage(page int) tea.Cmd {
	if m.localCommand == nil {
		return nil
	}
	return loadSessionPickerCmd(m.ctx, m.snapshot, fmt.Sprintf("/sessions %d", page), m.localCommand)
}

func (m *model) closeSessionPicker() {
	m.sessionPicker = nil
	m.sessionCursor = 0
	m.input.Focus()
}

func (m *model) closeConfigForm() {
	m.configForm = nil
	m.input.Focus()
	m.syncLayout()
}

func (m *model) closeApprovalPicker() {
	m.approvalPicker = nil
	m.approvalCursor = 0
	m.input.Focus()
}

func (m *model) moveConfigFormCursor(delta int) {
	if m.configForm == nil || len(m.configForm.inputs) == 0 {
		return
	}
	if m.configForm.flowOpen {
		m.configForm.flowOpen = false
	}
	current := m.configForm.cursor
	if isConfigTextField(current) {
		m.configForm.inputs[current].Blur()
	}
	m.configForm.cursor += delta
	if m.configForm.cursor < 0 {
		m.configForm.cursor = len(m.configForm.inputs) - 1
	} else if m.configForm.cursor >= len(m.configForm.inputs) {
		m.configForm.cursor = 0
	}
	if isConfigTextField(m.configForm.cursor) {
		m.configForm.inputs[m.configForm.cursor].Focus()
	}
	m.syncLayout()
}

func (m model) submitConfigForm() (tea.Model, tea.Cmd) {
	if m.configForm == nil {
		return m, nil
	}
	submission := ConfigFormSubmission{Values: configFormValuesFromInputs(m.configForm.inputs)}
	if m.configForm.flowCursor >= 0 && m.configForm.flowCursor < len(m.configForm.flowOptions) {
		submission.Values.DefaultFlow = strings.TrimSpace(m.configForm.flowOptions[m.configForm.flowCursor].Value)
	}
	approvalChoices := configApprovalChoices()
	if m.configForm.approvalCursor >= 0 && m.configForm.approvalCursor < len(approvalChoices) {
		submission.Values.ApprovalMode = strings.TrimSpace(approvalChoices[m.configForm.approvalCursor].Value)
	}
	apiKey := strings.TrimSpace(submission.Values.APIKey)
	if m.configForm.hasAPIKey && apiKey == "" {
		submission.KeepAPIKey = true
	}
	if apiKey == "-" {
		submission.Values.APIKey = ""
		submission.ClearAPIKey = true
		submission.KeepAPIKey = false
	}
	return m, tea.Batch(m.transitionProgress(progressExecuting), submitConfigFormCmd(m.ctx, m.snapshot, submission, m.configFormSubmit))
}

func newConfigFormState(state ConfigFormState) *configFormState {
	values := state.Values
	models := make([]textinput.Model, 0, len(configFormFieldSpecs(state.HasAPIKey)))
	for index, spec := range configFormFieldSpecs(state.HasAPIKey) {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = spec.placeholder
		input.CharLimit = 512
		input.SetValue(configFormFieldValue(index, values, state.HasAPIKey))
		if index == 0 {
			input.Focus()
		} else {
			input.Blur()
		}
		models = append(models, input)
	}
	flowOptions := normalizeConfigFlowChoices(state.FlowOptions, values.DefaultFlow)
	return &configFormState{
		title:          state.Title,
		hint:           state.Hint,
		path:           state.Path,
		hasAPIKey:      state.HasAPIKey,
		submitText:     state.SubmitText,
		inputs:         models,
		cursor:         0,
		flowOptions:    flowOptions,
		flowCursor:     configChoiceIndex(flowOptions, values.DefaultFlow),
		approvalCursor: configChoiceIndex(configApprovalChoices(), values.ApprovalMode),
	}
}

func configFormFieldSpecs(hasAPIKey bool) []configFieldSpec {
	apiDescription := "留空表示不修改。"
	if hasAPIKey {
		apiDescription = "已配置 API Key。留空表示保持不变，输入 - 表示清空并保存。"
	}
	return []configFieldSpec{
		{label: "服务端地址", placeholder: "http://localhost:7860", description: "必须是完整的 HTTP 或 HTTPS 地址。"},
		{label: "API Key", placeholder: "可选", description: apiDescription},
		{label: "默认智能体", placeholder: "", description: "按 Enter 打开下拉列表，留空项表示自动选择。"},
		{label: "命令执行模式", placeholder: "", description: "左右键或空格切换单选项。"},
		{label: "工作目录", placeholder: "留空表示当前目录", description: "默认使用当前工作目录。"},
		{label: "输出语言", placeholder: "zh-CN", description: "留空会恢复默认输出语言。"},
	}
}

func configFormFieldValue(index int, values ConfigFormValues, hasAPIKey bool) string {
	switch index {
	case 0:
		return strings.TrimSpace(values.BackendBaseURL)
	case 1:
		if hasAPIKey {
			return ""
		}
		return strings.TrimSpace(values.APIKey)
	case 2:
		return ""
	case 3:
		return ""
	case 4:
		return strings.TrimSpace(values.Workdir)
	case 5:
		return strings.TrimSpace(values.OutputLanguage)
	default:
		return ""
	}
}

func configFormValuesFromInputs(inputs []textinput.Model) ConfigFormValues {
	values := ConfigFormValues{}
	if len(inputs) > 0 {
		values.BackendBaseURL = strings.TrimSpace(inputs[0].Value())
	}
	if len(inputs) > 1 {
		values.APIKey = strings.TrimSpace(inputs[1].Value())
	}
	if len(inputs) > 4 {
		values.Workdir = strings.TrimSpace(inputs[4].Value())
	}
	if len(inputs) > 5 {
		values.OutputLanguage = strings.TrimSpace(inputs[5].Value())
	}
	return values
}

func isConfigTextField(index int) bool {
	switch index {
	case configFieldBackendBaseURL, configFieldAPIKey, configFieldWorkdir, configFieldOutputLanguage:
		return true
	default:
		return false
	}
}

func (m *model) openConfigFlowDropdown() {
	if m.configForm == nil {
		return
	}
	m.configForm.flowOpen = true
	m.syncLayout()
}

func (m *model) moveConfigFlowCursor(delta int) {
	if m.configForm == nil || len(m.configForm.flowOptions) == 0 {
		return
	}
	m.configForm.flowCursor += delta
	if m.configForm.flowCursor < 0 {
		m.configForm.flowCursor = len(m.configForm.flowOptions) - 1
	} else if m.configForm.flowCursor >= len(m.configForm.flowOptions) {
		m.configForm.flowCursor = 0
	}
	m.syncLayout()
}

func (m *model) moveConfigApprovalCursor(delta int) {
	choices := configApprovalChoices()
	if m.configForm == nil || len(choices) == 0 {
		return
	}
	m.configForm.approvalCursor += delta
	if m.configForm.approvalCursor < 0 {
		m.configForm.approvalCursor = len(choices) - 1
	} else if m.configForm.approvalCursor >= len(choices) {
		m.configForm.approvalCursor = 0
	}
	m.syncLayout()
}

func normalizeConfigFlowChoices(options []ConfigChoice, currentValue string) []ConfigChoice {
	choices := []ConfigChoice{{Value: "", Label: "自动选择", Description: "保存为空，启动时自动选择一个可用智能体。"}}
	seen := map[string]struct{}{"": {}}
	for _, option := range options {
		value := strings.TrimSpace(option.Value)
		label := strings.TrimSpace(option.Label)
		if value == "" && label == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		if label == "" {
			label = value
		}
		choices = append(choices, ConfigChoice{Value: value, Label: label, Description: strings.TrimSpace(option.Description)})
		seen[value] = struct{}{}
	}
	trimmedCurrent := strings.TrimSpace(currentValue)
	if trimmedCurrent != "" {
		if _, ok := seen[trimmedCurrent]; !ok {
			choices = append(choices, ConfigChoice{Value: trimmedCurrent, Label: trimmedCurrent, Description: "当前配置值，未在已加载智能体列表中命中。"})
		}
	}
	return choices
}

func configApprovalChoices() []ConfigChoice {
	return []ConfigChoice{
		{Value: "confirm", Label: "每次确认", Description: "执行本地命令前逐次确认。"},
		{Value: "auto", Label: "自动执行", Description: "自动执行本地命令。"},
		{Value: "readonly", Label: "只读模式", Description: "禁止执行本地命令。"},
	}
}

func configChoiceIndex(options []ConfigChoice, value string) int {
	trimmedValue := strings.TrimSpace(value)
	for index, option := range options {
		if strings.TrimSpace(option.Value) == trimmedValue {
			return index
		}
	}
	return 0
}

func configChoiceLabel(options []ConfigChoice, index int, fallbackLabel string) string {
	if index >= 0 && index < len(options) {
		label := strings.TrimSpace(options[index].Label)
		if label != "" {
			return label
		}
		return strings.TrimSpace(options[index].Value)
	}
	return fallbackLabel
}

func activeConfigFormValue(form *configFormState) string {
	if form == nil || form.cursor < 0 || form.cursor >= len(form.inputs) {
		return ""
	}
	return strings.TrimSpace(form.inputs[form.cursor].Value())
}

func buildInitialEntries(snapshot Snapshot) []entry {
	entries := []entry{}
	if strings.TrimSpace(snapshot.WelcomeMessage) != "" {
		entries = append(entries, entry{role: "welcome", content: strings.TrimSpace(snapshot.WelcomeMessage)})
	}
	entries = append(entries, buildBackendStatusEntries(snapshot)...)
	for _, item := range snapshot.History {
		if strings.TrimSpace(item.Content) == "" {
			continue
		}
		entries = append(entries, entry{role: item.Role, content: item.Content})
	}
	if len(entries) == 0 {
		entries = append(entries, entry{role: "welcome", content: strings.TrimSpace(snapshot.WelcomeMessage)})
	}
	return entries
}

func buildWelcomeEntries(snapshot Snapshot) []entry {
	entries := []entry{}
	if strings.TrimSpace(snapshot.WelcomeMessage) != "" {
		entries = append(entries, entry{role: "welcome", content: strings.TrimSpace(snapshot.WelcomeMessage)})
	}
	entries = append(entries, buildBackendStatusEntries(snapshot)...)
	return entries
}

func (m model) shouldPinHistoryTop() bool {
	if len(m.entries) != 1 {
		return false
	}
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
	case progressSession:
		return progressState{phase: phase, label: "切换会话中", hint: "正在更新当前会话", percent: 0.46}
	case progressApproval:
		return progressState{phase: phase, label: "更新模式中", hint: "正在更新命令执行模式", percent: 0.46}
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

func (m *model) syncInputPlaceholder() {
	if m.snapshot.BackendAvailable {
		m.input.Placeholder = "输入内容后回车发送"
		return
	}
	m.input.Placeholder = "服务端不可用，当前为只读模式，仅支持 /config、/help、/clear、/exit"
}

func (m model) scheduleBackendRetry() tea.Cmd {
	if m.snapshot.BackendAvailable || m.refreshSnapshot == nil {
		return nil
	}
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return backendRetryTickMsg{}
	})
}

func (m model) maybeRefreshSnapshot() tea.Cmd {
	if m.refreshSnapshot == nil || !m.snapshot.BackendAvailable {
		return nil
	}
	return refreshSnapshotCmd(m.ctx, m.snapshot, m.refreshSnapshot)
}

func (m model) maybeRefreshSessionSnapshot() tea.Cmd {
	if m.refreshSnapshot == nil || !m.snapshot.BackendAvailable || strings.TrimSpace(m.snapshot.SessionID) == "" {
		return nil
	}
	return refreshSnapshotCmd(m.ctx, m.snapshot, m.refreshSnapshot)
}

func (m model) isBusy() bool {
	switch m.state.phase {
	case progressSending, progressStreaming, progressConfirming, progressExecuting, progressSwitching, progressSession, progressApproval:
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
	if !snapshot.BackendAvailable {
		return "未连接"
	}
	version := strings.TrimSpace(snapshot.ServerVersion)
	if version == "" {
		version = "0.1.0"
	}
	if pkg := strings.TrimSpace(snapshot.ServerPackage); pkg != "" {
		return "v" + version
	}
	return version
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
		return "新会话"
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
	return strings.Trim(rendered, "\n")
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
	runes := []rune(text)
	trimmed := string(runes)
	for lipgloss.Width(trimmed)+lipgloss.Width(ellipsis) > width && len(runes) > 0 {
		runes = runes[:len(runes)-1]
		trimmed = string(runes)
	}
	return trimmed + ellipsis
}

func trimLeftForWidth(text string, width int) string {
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
	runes := []rune(text)
	trimmed := string(runes)
	for lipgloss.Width(trimmed)+lipgloss.Width(ellipsis) > width && len(runes) > 0 {
		runes = runes[1:]
		trimmed = string(runes)
	}
	return ellipsis + trimmed
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
	case strings.EqualFold(trimmed, "/config"):
		return true
	case strings.EqualFold(trimmed, "/config show"):
		return true
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
		return "禁止执行任何命令"
	case "confirm", "":
		return "命令需要用户确认"
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
	return trimmed
}
