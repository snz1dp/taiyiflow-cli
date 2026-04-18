package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Snapshot struct {
	Version          string
	BackendURL       string
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

type EventKind string

const (
	EventAssistantChunk EventKind = "assistant_chunk"
	EventSystemNote     EventKind = "system_note"
	EventErrorNote      EventKind = "error_note"
	EventSessionUpdate  EventKind = "session_update"
)

type Event struct {
	Kind      EventKind
	Text      string
	SessionID string
}

type Request struct {
	Input  string
	FlowID string
}

type ConfirmRequest struct {
	Title   string
	Command string
	Detail  string
}

type Runner func(ctx context.Context, request Request, emit func(Event), confirm func(ConfirmRequest) bool) error

type FlowChangeHandler func(ctx context.Context, flowID string) (Snapshot, error)

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
	eventCh        chan tea.Msg
	entries        []entry
	input          textinput.Model
	viewport       viewport.Model
	progress       progress.Model
	state          progressState
	width          int
	height         int
	contentWidth   int
	ready          bool
	quitting       bool
	showFlowPicker bool
	flowCursor     int
	pendingConfirm *confirmState
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("24")).
			Padding(0, 1)

	panelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("24")).
			Padding(1, 2)

	overlayStyle = lipgloss.NewStyle().
			Border(lipgloss.ThickBorder()).
			BorderForeground(lipgloss.Color("62")).
			Background(lipgloss.Color("236")).
			Padding(1, 2)

	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	valueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	hintStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	systemStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("110"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)

	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 2)

	selectedFlowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("24")).
				Bold(true)

	progressLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("110")).
				Bold(true)
)

func Run(ctx context.Context, snapshot Snapshot, flows []FlowItem, runner Runner, flowChange FlowChangeHandler) error {
	inputModel := textinput.New()
	inputModel.Placeholder = "例如：帮我总结当前仓库状态"
	inputModel.Prompt = "> "
	inputModel.Focus()

	viewportModel := viewport.New(0, 0)
	viewportModel.Style = lipgloss.NewStyle()

	progressModel := progress.New(progress.WithDefaultGradient())

	initialEntries := buildInitialEntries(snapshot)
	program := tea.NewProgram(model{
		ctx:        ctx,
		snapshot:   snapshot,
		flows:      flows,
		runner:     runner,
		flowChange: flowChange,
		eventCh:    make(chan tea.Msg),
		entries:    initialEntries,
		input:      inputModel,
		viewport:   viewportModel,
		progress:   progressModel,
		state:      newProgressState(progressIdle),
		flowCursor: currentFlowIndex(flows, snapshot.DefaultFlow),
	}, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.progress.SetPercent(m.state.percent))
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
		m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
		m.syncViewport()
		progressCmd := m.transitionProgress(progressFailed)
		m.input.Focus()
		return m, progressCmd
	case streamDoneMsg:
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
		m.entries = buildInitialEntries(m.snapshot)
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("已切换默认 flow: %s -> %s", fallback(previousFlow, "<未配置>"), fallback(m.snapshot.DefaultFlowLabel, sanitizeFlowLabel(m.snapshot.DefaultFlow)))})
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
	case "ctrl+c", "q":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		if m.isBusy() {
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit
	case "ctrl+o":
		if m.isBusy() {
			m.entries = append(m.entries, entry{role: "system", content: "当前正在处理请求，暂时不能切换 flow。"})
			m.syncViewport()
			return m, nil
		}
		if len(m.flows) == 0 {
			m.entries = append(m.entries, entry{role: "error", content: "当前没有可切换的 flow 列表。"})
			m.syncViewport()
			return m, nil
		}
		m.showFlowPicker = true
		m.flowCursor = currentFlowIndex(m.flows, m.snapshot.DefaultFlow)
		m.input.Blur()
		return m, nil
	case "pgup":
		m.viewport.HalfViewUp()
		return m, nil
	case "pgdown":
		m.viewport.HalfViewDown()
		return m, nil
	case "up":
		m.viewport.LineUp(1)
		return m, nil
	case "down":
		m.viewport.LineDown(1)
		return m, nil
	case "home":
		m.viewport.GotoTop()
		return m, nil
	case "end":
		m.viewport.GotoBottom()
		return m, nil
	case "enter":
		if m.isBusy() {
			return m, nil
		}
		input := strings.TrimSpace(m.input.Value())
		if input == "" {
			return m, nil
		}
		if strings.HasPrefix(input, "/flow ") {
			flowID := strings.TrimSpace(strings.TrimPrefix(input, "/flow "))
			if flowID == "" {
				m.entries = append(m.entries, entry{role: "error", content: "用法: /flow <ID>"})
				m.syncViewport()
				m.input.SetValue("")
				return m, nil
			}
			m.input.SetValue("")
			return m, tea.Batch(m.transitionProgress(progressSwitching), changeFlowCmd(m.ctx, flowID, m.flowChange))
		}
		m.entries = append(m.entries, entry{role: "user", content: input})
		m.input.SetValue("")
		m.syncViewport()
		request := Request{Input: input, FlowID: m.snapshot.DefaultFlow}
		return m, tea.Batch(m.transitionProgress(progressSending), startStream(m.ctx, request, m.runner, m.eventCh))
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		m.pendingConfirm.responseCh <- true
		close(m.pendingConfirm.responseCh)
		m.pendingConfirm = nil
		return m, tea.Batch(m.transitionProgress(progressExecuting), waitForStream(m.eventCh))
	case "n", "N", "esc", "ctrl+c":
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
	case "esc":
		m.showFlowPicker = false
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
		if len(m.flows) == 0 {
			m.showFlowPicker = false
			m.input.Focus()
			return m, nil
		}
		selected := m.flows[m.flowCursor]
		return m, tea.Batch(m.transitionProgress(progressSwitching), changeFlowCmd(m.ctx, selected.ID, m.flowChange))
	default:
		return m, nil
	}
}

func (m *model) applyEvent(event Event) tea.Cmd {
	var cmd tea.Cmd
	switch event.Kind {
	case EventAssistantChunk:
		if m.state.phase != progressStreaming {
			cmd = m.transitionProgress(progressStreaming)
		}
		if len(m.entries) == 0 || m.entries[len(m.entries)-1].role != "assistant" {
			m.entries = append(m.entries, entry{role: "assistant", content: event.Text})
		} else {
			m.entries[len(m.entries)-1].content += event.Text
		}
	case EventSystemNote:
		if strings.TrimSpace(event.Text) != "" {
			m.entries = append(m.entries, entry{role: "system", content: event.Text})
		}
	case EventErrorNote:
		if strings.TrimSpace(event.Text) != "" {
			m.entries = append(m.entries, entry{role: "error", content: event.Text})
		}
	case EventSessionUpdate:
		if strings.TrimSpace(event.SessionID) != "" {
			m.snapshot.SessionID = event.SessionID
		}
	}
	m.syncViewport()
	return cmd
}

func (m *model) syncLayout() {
	if !m.ready || m.width == 0 || m.height == 0 {
		return
	}
	contentWidth := m.width - 6
	if contentWidth < 60 {
		contentWidth = m.width - 2
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
	fixedHeight := 17
	if m.pendingConfirm != nil || m.showFlowPicker {
		fixedHeight += 2
	}
	historyHeight := m.height - fixedHeight
	if historyHeight < 8 {
		historyHeight = 8
	}
	m.viewport.Width = viewportWidth
	m.viewport.Height = historyHeight
	inputWidth := contentWidth - 10
	if inputWidth < 20 {
		inputWidth = 20
	}
	m.input.Width = inputWidth
	m.progress.Width = contentWidth - 18
	if m.progress.Width < 12 {
		m.progress.Width = 12
	}
	m.syncViewport()
}

func (m *model) syncViewport() {
	content := renderEntries(m.entries)
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
}

func (m model) View() string {
	if m.quitting {
		return "\n"
	}

	header := titleStyle.Render("太乙智启命令行（CLI）")
	status := panelStyle.Width(m.contentWidth - 6).Render(strings.Join([]string{
		row("版本", fallback(m.snapshot.Version, "dev")),
		row("后端地址", fallback(m.snapshot.BackendURL, "未配置")),
		row("当前 flow", fallback(m.snapshot.DefaultFlowLabel, "未配置")),
		row("工作目录", fallback(m.snapshot.Workdir, "当前目录")),
		row("审批模式", fallback(m.snapshot.ApprovalMode, "confirm")),
		row("本地会话", fallback(m.snapshot.SessionID, "未建立")),
		row("进度状态", m.state.label),
	}, "\n") + "\n\n" + m.renderProgress())

	history := panelStyle.Width(m.contentWidth - 6).Render(m.viewport.View())
	inputTitle := "输入并回车发送"
	if m.state.hint != "" {
		inputTitle = m.state.hint
	}
	inputView := m.input.View()
	if m.pendingConfirm != nil || m.showFlowPicker {
		inputView = hintStyle.Render("当前有未处理的弹层，请先完成操作。")
	}
	inputBox := inputStyle.Width(m.contentWidth - 6).Render(labelStyle.Render(inputTitle) + "\n" + inputView)
	footer := hintStyle.Render("按 Enter 发送，Ctrl+O 打开 flow 选择，PgUp/PgDn 或方向键滚动消息，按 q 退出。")

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", status, "", history, "", inputBox, "", footer)
	if overlay := m.renderOverlay(); overlay != "" {
		content = lipgloss.JoinVertical(lipgloss.Left, content, "", overlay)
	}
	if !m.ready || m.width == 0 {
		return "\n" + content + "\n"
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, content)
}

func (m model) renderProgress() string {
	parts := []string{
		progressLabelStyle.Render("状态进度"),
		m.progress.ViewAs(m.state.percent),
		hintStyle.Render(m.state.hint),
	}
	return strings.Join(parts, "\n")
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
		return "当前没有可选 flow。"
	}
	lines := []string{"选择默认 flow", "使用方向键或 j/k 选择，Enter 确认，Esc 取消。", ""}
	for index, item := range m.flows {
		line := fmt.Sprintf("%s  %s", flowCursor(index == m.flowCursor), fallback(item.Name, item.Type))
		if strings.TrimSpace(item.Type) != "" {
			line += fmt.Sprintf("  (%s)", item.Type)
		}
		if item.ID == m.snapshot.DefaultFlow {
			line += "  [当前]"
		}
		if index == m.flowCursor {
			line = selectedFlowStyle.Render(line)
		}
		lines = append(lines, line)
		if description := strings.TrimSpace(item.Description); description != "" && index == m.flowCursor {
			lines = append(lines, hintStyle.Render(trimText(description, 100)))
		}
	}
	return strings.Join(lines, "\n")
}

func renderEntries(entries []entry) string {
	if len(entries) == 0 {
		return hintStyle.Render("还没有消息。")
	}
	blocks := make([]string, 0, len(entries))
	for _, item := range entries {
		blocks = append(blocks, renderEntry(item))
	}
	return strings.Join(blocks, "\n\n")
}

func renderEntry(item entry) string {
	switch item.role {
	case "user":
		return userStyle.Render("你") + "\n" + valueStyle.Render(item.content)
	case "assistant":
		return assistantStyle.Render("助理") + "\n" + assistantStyle.Render(item.content)
	case "error":
		return errorStyle.Render("错误") + "\n" + errorStyle.Render(item.content)
	default:
		return systemStyle.Render("系统") + "\n" + systemStyle.Render(item.content)
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
			if err != nil {
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

func waitForStream(eventCh chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-eventCh
	}
}

func buildInitialEntries(snapshot Snapshot) []entry {
	if strings.TrimSpace(snapshot.WelcomeMessage) != "" {
		return []entry{{role: "system", content: snapshot.WelcomeMessage}}
	}
	return []entry{{role: "system", content: "已进入 TUI。直接输入内容并回车发送，Ctrl+O 可切换默认 flow。"}}
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
		return progressState{phase: phase, label: "执行客户端命令", hint: "客户端命令正在本地运行", percent: 0.86}
	case progressSwitching:
		return progressState{phase: phase, label: "切换 flow 中", hint: "正在更新默认 flow 和会话上下文", percent: 0.46}
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

func currentFlowIndex(flows []FlowItem, current string) int {
	for index, item := range flows {
		if item.ID == current {
			return index
		}
	}
	return 0
}

func flowCursor(selected bool) string {
	if selected {
		return ">"
	}
	return " "
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

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func row(label string, value string) string {
	return fmt.Sprintf("%s %s", labelStyle.Render(label+":"), valueStyle.Render(value))
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
