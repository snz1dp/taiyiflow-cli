package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Snapshot struct {
	Version        string
	BackendURL     string
	DefaultFlow    string
	Workdir        string
	ApprovalMode   string
	SessionID      string
	WelcomeMessage string
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

type Runner func(ctx context.Context, input string, emit func(Event)) error

type entry struct {
	role    string
	content string
}

type streamEventMsg struct {
	event Event
}

type streamDoneMsg struct{}

type streamFailedMsg struct {
	err error
}

type model struct {
	ctx        context.Context
	snapshot   Snapshot
	runner     Runner
	eventCh    chan tea.Msg
	entries    []entry
	input      string
	width      int
	height     int
	ready      bool
	quitting   bool
	submitting bool
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

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244"))

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	hintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("229")).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	systemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("110"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Bold(true)

	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 2)
)

func Run(ctx context.Context, snapshot Snapshot, runner Runner) error {
	initialEntries := []entry{}
	if strings.TrimSpace(snapshot.WelcomeMessage) != "" {
		initialEntries = append(initialEntries, entry{role: "system", content: snapshot.WelcomeMessage})
	} else {
		initialEntries = append(initialEntries, entry{role: "system", content: "已进入 TUI，会话输入框已启用。直接输入内容并回车发送。"})
	}

	program := tea.NewProgram(model{
		ctx:      ctx,
		snapshot: snapshot,
		runner:   runner,
		eventCh:  make(chan tea.Msg),
		entries:  initialEntries,
	}, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = typed.Width
		m.height = typed.Height
		m.ready = true
		return m, nil
	case streamEventMsg:
		m.applyEvent(typed.event)
		if m.submitting {
			return m, waitForStream(m.eventCh)
		}
		return m, nil
	case streamFailedMsg:
		m.submitting = false
		m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
		return m, nil
	case streamDoneMsg:
		m.submitting = false
		return m, nil
	case tea.KeyMsg:
		switch typed.String() {
		case "q", "esc", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if m.submitting {
				return m, nil
			}
			input := strings.TrimSpace(m.input)
			if input == "" {
				return m, nil
			}
			m.entries = append(m.entries, entry{role: "user", content: input})
			m.input = ""
			m.submitting = true
			return m, startStream(m.ctx, input, m.runner, m.eventCh)
		case "backspace":
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
			return m, nil
		case "space":
			m.input += " "
			return m, nil
		default:
			if !typed.Alt && typed.Type == tea.KeyRunes {
				m.input += typed.String()
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *model) applyEvent(event Event) {
	switch event.Kind {
	case EventAssistantChunk:
		if len(m.entries) == 0 || m.entries[len(m.entries)-1].role != "assistant" {
			m.entries = append(m.entries, entry{role: "assistant", content: event.Text})
			return
		}
		m.entries[len(m.entries)-1].content += event.Text
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
}

func (m model) View() string {
	if m.quitting {
		return "\n"
	}

	header := titleStyle.Render("太乙智启命令行（CLI）")
	status := panelStyle.Render(strings.Join([]string{
		row("版本", fallback(m.snapshot.Version, "dev")),
		row("后端地址", fallback(m.snapshot.BackendURL, "未配置")),
		row("默认 flow", fallback(m.snapshot.DefaultFlow, "未配置")),
		row("工作目录", fallback(m.snapshot.Workdir, "当前目录")),
		row("审批模式", fallback(m.snapshot.ApprovalMode, "confirm")),
		row("本地会话", fallback(m.snapshot.SessionID, "未建立")),
	}, "\n"))

	history := panelStyle.Render(m.renderHistory())
	inputTitle := "输入并回车发送"
	if m.submitting {
		inputTitle = "正在等待流式响应完成"
	}
	inputValue := m.input
	if inputValue == "" {
		inputValue = hintStyle.Render("例如：帮我总结当前仓库状态")
	} else {
		inputValue = valueStyle.Render(m.input + "_")
	}
	inputBox := inputStyle.Render(labelStyle.Render(inputTitle) + "\n" + inputValue)
	footer := hintStyle.Render("按 Enter 发送，按 q、esc 或 Ctrl+C 退出。")

	content := lipgloss.JoinVertical(lipgloss.Left, header, "", status, "", history, "", inputBox, "", footer)
	if !m.ready || m.width == 0 {
		return "\n" + content + "\n"
	}

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Top, content)
}

func (m model) renderHistory() string {
	if len(m.entries) == 0 {
		return hintStyle.Render("还没有消息。")
	}

	blocks := make([]string, 0, len(m.entries))
	for _, item := range m.entries {
		blocks = append(blocks, renderEntry(item))
	}
	content := strings.Join(blocks, "\n\n")
	if !m.ready || m.height == 0 {
		return content
	}
	return trimToLastLines(content, m.height/2)
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

func startStream(ctx context.Context, input string, runner Runner, eventCh chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		go func() {
			err := runner(ctx, input, func(event Event) {
				eventCh <- streamEventMsg{event: event}
			})
			if err != nil {
				eventCh <- streamFailedMsg{err: err}
			}
			eventCh <- streamDoneMsg{}
		}()
		return <-eventCh
	}
}

func waitForStream(eventCh chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-eventCh
	}
}

func trimToLastLines(content string, maxLines int) string {
	if maxLines <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}
	return strings.Join(lines[len(lines)-maxLines:], "\n")
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
