package tui

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
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

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/loginprompt"
)

type Snapshot struct {
	Version                      string
	BackendURL                   string
	BackendAvailable             bool
	BackendStatusReason          string
	ServerPackage                string
	ServerVersion                string
	AuthRequired                 bool
	AuthStatusReason             string
	CurrentUserName              string
	DefaultFlow                  string
	DefaultFlowLabel             string
	Workdir                      string
	ContextDir                   string
	RequestMode                  string
	ApprovalMode                 string
	ReasoningEnabled             bool
	ReasoningOverride            string
	SessionSandboxEscapePolicy   string
	SessionID                    string
	SessionTitle                 string
	History                      []HistoryEntry
	WelcomeMessage               string
	SubHead                      string
	Questions                    []string
	InternetSearchSupported      bool
	InternetSearchEnabled        bool
	InternetSearchDefault        bool
	KnowledgeSearchSupported     bool
	KnowledgeSystemEnabled       bool
	MCPToolsSupported            bool
	TextToSpeechSupported        bool
	SpeechToTextSupported        bool
	HumanAIDialogueSystemEnabled bool
	HumanAIDialogueFlowSupported bool
	HumanAIDialogueSupported     bool
	KnowledgeBases               []KnowledgeBaseItem
	SelectedKnowledgeBases       []string
	MaxFileSizeUploadMB          int
	SupportedDocumentExts        []string
	FileCapabilities             []FileCapability
	InputFiles                   []string
	RequestStats                 *RequestMetrics
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

type KnowledgeBaseItem struct {
	Code        string
	Name        string
	Description string
	Type        string
	Selected    bool
}

type KnowledgePickerState struct {
	Title string
	Hint  string
	Items []KnowledgeBaseItem
}

type SkillReferenceItem struct {
	Path        string
	Description string
}

type SkillItem struct {
	Name           string
	Description    string
	Category       string
	SourceLabel    string
	StateLabel     string
	RootDir        string
	RelativeDir    string
	Restriction    string
	ReferenceFiles []SkillReferenceItem
}

type RemoteSkillItem struct {
	Slug             string
	DisplayName      string
	Summary          string
	Popularity       float64
	Downloads        int64
	Version          string
	RegistryLabel    string
	Installed        bool
	InstalledVersion string
	CanUpdate        bool
	OwnerLabel       string
	SupportedOS      []string
	SupportedEnvs    []string
}

type SkillsPickerState struct {
	Title         string
	Hint          string
	ActiveTab     string
	Items         []SkillItem
	RemoteItems   []RemoteSkillItem
	RemoteQuery   string
	RemoteHint    string
	RemoteError   string
	SearchMode    bool
	RegistryLabel string
}

type TaskItem struct {
	ID          string
	Name        string
	Status      string
	Command     string
	CWD         string
	SessionID   string
	StartedAt   string
	CompletedAt string
	ExitCode    string
}

type TasksPickerState struct {
	Title string
	Hint  string
	Items []TaskItem
}

type TaskActionItem struct {
	Label       string
	Description string
	Command     string
}

type TaskActionPickerState struct {
	Title string
	Hint  string
	Task  TaskItem
	Items []TaskActionItem
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

type FileCapability struct {
	Key     string
	Label   string
	Enabled bool
}

type ConfigFormValues struct {
	BackendBaseURL           string
	DefaultFlow              string
	ApprovalMode             string
	SandboxEscapePolicy      string
	SkillReviewPolicy        string
	WriteConfirmationPolicy  string
	SandboxRoots             string
	PermissionRules          string
	ReasoningEnabled         bool
	Workdir                  string
	HTTPTimeoutSeconds       string
	CommandTimeoutSeconds    string
	StreamIdleTimeoutSeconds string
	StreamRetryAttempts      string
	QRWaitTimeoutSeconds     string
	AgentAutoLoopEnabled     string
	AgentAutoLoopMaxRounds   string
	OutputLanguage           string
	SkillsInstallScope       string
	SkillsAutoInstallPolicy  string
	SkillsFreeTextSearch     bool
	SkillsRegistryURL        string
}

type ConfigChoice struct {
	Value       string
	Label       string
	Description string
}

type ConfigFormState struct {
	Title         string
	Hint          string
	StatusText    string
	StatusIsError bool
	Path          string
	Values        ConfigFormValues
	SubmitText    string
	FlowOptions   []ConfigChoice
}

type ConfigFormSubmission struct {
	Values ConfigFormValues
}

type ConfigFieldError struct {
	Field   string
	Message string
}

func (e ConfigFieldError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return strings.TrimSpace(e.Message)
	}
	return "配置字段校验失败"
}

type LoginFormState struct {
	Title                  string
	Hint                   string
	StatusText             string
	StatusIsError          bool
	FocusField             int
	SupportsQRCode         bool
	Mode                   string
	Username               string
	Password               string
	PasswordPublicKey      string
	PasswordTicket         string
	Remember               bool
	VerifyType             string
	VerifyHint             string
	VerifyTarget           string
	VerifyImageASCII       string
	VerifyImagePNGBase64   string
	VerifyImageWidth       int
	VerifyImageHeight      int
	VerifyImageURL         string
	VerifyImagePreviewPath string
	Ticket                 string
	Code                   string
	QRTicket               string
	QRLoginURL             string
	QRScanHint             string
	QRCodeASCII            string
	QRWaiting              bool
	QRWaitPollCount        int
}

type LoginFormSubmission struct {
	Action                 string
	Mode                   string
	Username               string
	Password               string
	PasswordPublicKey      string
	PasswordTicket         string
	Remember               bool
	VerifyType             string
	VerifyHint             string
	VerifyTarget           string
	VerifyImageASCII       string
	VerifyImagePNGBase64   string
	VerifyImageWidth       int
	VerifyImageHeight      int
	VerifyImageURL         string
	VerifyImagePreviewPath string
	Ticket                 string
	Code                   string
	QRTicket               string
	QRLoginURL             string
	QRScanHint             string
	QRCodeASCII            string
	QRWaiting              bool
	QRWaitPollCount        int
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
	EventToolSummary    EventKind = "tool_summary"
	EventToolPanel      EventKind = "tool_panel"
	EventSessionUpdate  EventKind = "session_update"
	EventToolCall       EventKind = "tool_call"
	EventBuiltInCall    EventKind = "built_in_call"
	EventClientToolCall EventKind = "client_tool_call"
	EventStatsUpdate    EventKind = "stats_update"
)

type Event struct {
	Kind           EventKind
	Text           string
	MessageID      string
	Stage          string
	EventType      string
	SessionID      string
	SessionTitle   string
	SplitAssistant bool
	Stats          *RequestMetrics
	Panel          *ToolPanel
}

type Request struct {
	Input            string
	InputFiles       []string
	FlowID           string
	InternetSearch   bool
	KnowledgeScopes  []string
	KnowledgeDocs    []string
	RunID            string
	RequestMode      string
	ReasoningEnabled bool
}

type ContextWindow struct {
	MaxInputTokens              int
	ContextUsageTokens          int
	DirectMessageTokens         int
	CompressedHistoryTokens     int
	CurrentRoundInputTokens     int
	NextRoundAvailableTokens    int
	CurrentRoundAvailableTokens int
	HistoryMessageCount         int
	PreparedMessageCount        int
	HighWatermarkRatio          float64
	CompressionTargetRatio      float64
	HighWatermarkTokens         int
	CompressionTargetTokens     int
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
	Title                       string
	Command                     string
	Detail                      string
	PreviewTitle                string
	PreviewBody                 string
	Hint                        string
	ApproveLabel                string
	RejectLabel                 string
	AlwaysApproveLabel          string
	AlwaysApprove               func(context.Context) error
	AllowAllLabel               string
	AllowAll                    func(context.Context) error
	DisableDefaultAlwaysApprove bool
}

type ToolPanel struct {
	Title   string
	Summary string
	Body    string
	Hint    string
	Actions []ToolPanelAction
}

type ToolPanelAction struct {
	Key      string
	Label    string
	Command  string
	Disabled bool
}

type LocalCommandResult struct {
	Handled         bool
	ClearHistory    bool
	Events          []Event
	FooterHint      string
	LoginForm       *LoginFormState
	SessionPicker   *SessionPickerState
	KnowledgePicker *KnowledgePickerState
	SkillsPicker    *SkillsPickerState
	TasksPicker     *TasksPickerState
	ApprovalPicker  *ApprovalPickerState
	ConfigForm      *ConfigFormState
	Snapshot        *Snapshot
	Flows           []FlowItem
	RebuildEntries  bool
	QueuedInput     string
}

type Runner func(ctx context.Context, request Request, emit func(Event), confirm func(ConfirmRequest) bool) error

type FlowChangeHandler func(ctx context.Context, flowID string) (Snapshot, error)

type SessionChangeHandler func(ctx context.Context, snapshot Snapshot, sessionID string) (Snapshot, error)

type ApprovalChangeHandler func(ctx context.Context, mode string) (Snapshot, error)

type ConfigFormSubmitHandler func(ctx context.Context, snapshot Snapshot, submission ConfigFormSubmission) (LocalCommandResult, error)

type LoginFormSubmitHandler func(ctx context.Context, snapshot Snapshot, submission LoginFormSubmission) (LocalCommandResult, error)

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

type confirmOption struct {
	label    string
	approved bool
	onSelect func() tea.Cmd
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

type loginFormSavedMsg struct {
	result LocalCommandResult
	err    error
}

type localCommandExecutedMsg struct {
	result LocalCommandResult
	err    error
}

type qrCodeWaitTickMsg struct {
	ticket string
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
	ctx                    context.Context
	snapshot               Snapshot
	flows                  []FlowItem
	runner                 Runner
	refreshSnapshot        SnapshotRefreshHandler
	flowChange             FlowChangeHandler
	sessionChange          SessionChangeHandler
	approvalChange         ApprovalChangeHandler
	configFormSubmit       ConfigFormSubmitHandler
	loginFormSubmit        LoginFormSubmitHandler
	localCommand           LocalCommandHandler
	eventCh                chan tea.Msg
	entries                []entry
	input                  textarea.Model
	flowFilter             textinput.Model
	viewport               viewport.Model
	toolPanelViewport      viewport.Model
	progress               progress.Model
	markdown               *glamour.TermRenderer
	state                  progressState
	activityHint           string
	footerHint             string
	currentToolLabel       string
	latestToolSummary      string
	latestToolAt           time.Time
	requestStats           RequestMetrics
	width                  int
	height                 int
	contentWidth           int
	ready                  bool
	quitting               bool
	showFlowPicker         bool
	sessionPicker          *SessionPickerState
	knowledgePicker        *KnowledgePickerState
	skillsPicker           *SkillsPickerState
	tasksPicker            *TasksPickerState
	taskActionPicker       *TaskActionPickerState
	approvalPicker         *ApprovalPickerState
	configForm             *configFormState
	loginForm              *loginFormState
	flowCursor             int
	sessionCursor          int
	knowledgeCursor        int
	skillsCursor           int
	tasksCursor            int
	taskActionCursor       int
	approvalCursor         int
	commandCursor          int
	startupCommand         string
	queuedInput            string
	lastRetryableRequest   *Request
	retryAvailable         bool
	pendingConfirm         *confirmState
	toolPanel              *ToolPanel
	overlayManualScroll    bool
	overlayScrollLine      int
	requestCancel          context.CancelFunc
	interrupting           bool
	splitNextAssistant     bool
	lastAssistantMessageID string
}

type configFormState struct {
	title                   string
	hint                    string
	statusText              string
	statusIsError           bool
	path                    string
	submitText              string
	inputs                  []textinput.Model
	cursor                  int
	flowOptions             []ConfigChoice
	flowCursor              int
	flowOpen                bool
	approvalCursor          int
	sandboxCursor           int
	skillReviewCursor       int
	writeConfirmationCursor int
	reasoningCursor         int
	agentAutoLoopCursor     int
	skillsCursor            int
	skillsAutoInstallCursor int
	skillsFreeTextCursor    int
	fieldErrorKey           string
	fieldErrorText          string
}

type loginFormState struct {
	title                  string
	hint                   string
	statusText             string
	statusIsError          bool
	supportsQRCode         bool
	mode                   string
	remember               bool
	passwordPublicKey      string
	passwordTicket         string
	verifyType             string
	verifyHint             string
	verifyTarget           string
	verifyImageASCII       string
	verifyImagePNGBase64   string
	verifyImageWidth       int
	verifyImageHeight      int
	verifyImageURL         string
	verifyImagePreviewPath string
	inputs                 []textinput.Model
	cursor                 int
	qrTicket               string
	qrLoginURL             string
	qrScanHint             string
	qrCodeASCII            string
	qrWaiting              bool
	qrWaitPollCount        int
}

type configFieldSpec struct {
	label       string
	placeholder string
	description string
}

const (
	configFieldBackendBaseURL = iota
	configFieldDefaultFlow
	configFieldApprovalMode
	configFieldSandboxPolicy
	configFieldSkillReviewPolicy
	configFieldWriteConfirmationPolicy
	configFieldSandboxRoots
	configFieldPermissionRules
	configFieldReasoningMode
	configFieldWorkdir
	configFieldHTTPTimeout
	configFieldCommandTimeout
	configFieldStreamIdleTimeout
	configFieldStreamRetryAttempts
	configFieldQRWaitTimeout
	configFieldAgentAutoLoopEnabled
	configFieldAgentAutoLoopMaxRounds
	configFieldOutputLanguage
	configFieldSkillsInstallScope
	configFieldSkillsAutoInstallPolicy
	configFieldSkillsFreeTextSearch
	configFieldSkillsRegistryURL
	configFieldCount
)

const (
	loginFormModePassword = "password"
	loginFormModeQR       = "qr"
)

const (
	loginVerifyTypeKaptcha = loginprompt.VerifyTypeKaptcha
	loginVerifyTypeSMS     = loginprompt.VerifyTypeSMS
	loginVerifyTypeMail    = loginprompt.VerifyTypeMail
	loginVerifyTypeTOTP    = loginprompt.VerifyTypeTOTP
)

const (
	loginFieldMode = iota
	loginFieldUsername
	loginFieldPassword
	loginFieldRemember
	loginFieldCaptchaTicket
	loginFieldCaptchaCode
	loginFieldSubmit
	loginFieldCount
)

var (
	ansiCSIRegexp      = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	ansiOSCRegexp      = regexp.MustCompile(`\x1b\][^\a]*(\a|\x1b\\)`)
	ansiAPCRegexp      = regexp.MustCompile(`(?s)\x1b_G.*?\x1b\\`)
	ansiTmuxDCSRegexp  = regexp.MustCompile(`(?s)\x1bPtmux;.*?\x1b\\`)
	overlayFocusMarker = "<<overlay-focus>>"

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
	{Trigger: "/new", Completion: "/new", Description: "重置当前智能体的本地会话"},
	{Trigger: "/retry", Completion: "/retry", Description: "重新发起最近一次失败的请求"},
	{Trigger: "/clear", Completion: "/clear", Description: "清空当前界面消息"},
	{Trigger: "/internet", Completion: "/internet ", Description: "查看或切换当前对话的联网搜索"},
	{Trigger: "/knowledge", Completion: "/knowledge ", Description: "查看或切换当前对话的知识库范围"},
	{Trigger: "/files", Completion: "/files", Description: "查看当前对话附带的文件状态"},
	{Trigger: "/session", Completion: "/session", Description: "显示当前会话状态"},
	{Trigger: "/reasoning", Completion: "/reasoning ", Description: "切换思考模式，支持 on、off、default、status"},
	{Trigger: "/agent", Completion: "/agent ", Description: "切换为执行模式，或直接发送一个执行请求"},
	{Trigger: "/review", Completion: "/review ", Description: "发起当前工作区或指定路径的代码审查"},
	{Trigger: "/ask", Completion: "/ask ", Description: "切换为问答模式，或直接发送一个问答请求"},
	{Trigger: "/plan", Completion: "/plan ", Description: "切换为计划模式，或直接发送一个计划请求"},
	{Trigger: "/agents", Completion: "/agents ", Description: "打开智能体选择，或用 /agents <名称> 直接切换"},
	{Trigger: "/tasks", Completion: "/tasks ", Description: "查看、捕获、停止或清理本地后台任务"},
	{Trigger: "/skills", Completion: "/skills ", Description: "列出本地技能，浏览远程仓库技能，并执行搜索、安装、更新或删除"},
	{Trigger: "/sessions", Completion: "/sessions ", Description: "列出当前智能体的服务端会话，可附页码"},
	{Trigger: "/login", Completion: "/login ", Description: "查看、登录或退出当前账号状态"},
	{Trigger: "/help", Completion: "/help", Description: "显示可用命令帮助"},
	{Trigger: "/config", Completion: "/config ", Description: "查看或更新系统全局配置"},
	{Trigger: "/exit", Completion: "/exit", Description: "退出当前智能对话"},
}

func Run(ctx context.Context, snapshot Snapshot, flows []FlowItem, runner Runner, refreshSnapshot SnapshotRefreshHandler, flowChange FlowChangeHandler, sessionChange SessionChangeHandler, approvalChange ApprovalChangeHandler, configFormSubmit ConfigFormSubmitHandler, loginFormSubmit LoginFormSubmitHandler, localCommand LocalCommandHandler, startupCommand string) error {
	inputModel := textarea.New()
	if snapshot.BackendAvailable {
		inputModel.Placeholder = "输入内容后回车发送"
	} else {
		inputModel.Placeholder = "服务端不可用，当前为只读模式，仅支持 /config、/tasks、/skills、/reasoning、/help、/clear、/exit"
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
	toolPanelViewportModel := viewport.New(0, 0)
	toolPanelViewportModel.Style = lipgloss.NewStyle()

	progressModel := progress.New(progress.WithDefaultGradient())

	initialEntries := buildInitialEntries(snapshot)
	initialRequestStats := cloneRequestMetrics(snapshot.RequestStats)
	program := tea.NewProgram(model{
		ctx:               ctx,
		snapshot:          snapshot,
		flows:             flows,
		runner:            runner,
		refreshSnapshot:   refreshSnapshot,
		flowChange:        flowChange,
		sessionChange:     sessionChange,
		approvalChange:    approvalChange,
		configFormSubmit:  configFormSubmit,
		loginFormSubmit:   loginFormSubmit,
		localCommand:      localCommand,
		eventCh:           make(chan tea.Msg),
		entries:           initialEntries,
		input:             inputModel,
		flowFilter:        flowFilterModel,
		viewport:          viewportModel,
		toolPanelViewport: toolPanelViewportModel,
		progress:          progressModel,
		state:             newProgressState(progressIdle),
		requestStats:      initialRequestStats,
		flowCursor:        currentFlowIndex(flows, snapshot.DefaultFlow),
		startupCommand:    resolveInitialStartupCommand(snapshot, startupCommand),
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
	if m.refreshSnapshot != nil {
		cmds = append(cmds, refreshSnapshotCmd(m.ctx, m.snapshot, m.refreshSnapshot))
	}
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
		replaceInitialEntries := shouldReplaceEntriesOnSnapshotRefresh(m.entries, m.snapshot)
		previouslyAvailable := m.snapshot.BackendAvailable
		previouslyAuthRequired := m.snapshot.AuthRequired
		previousReason := strings.TrimSpace(m.snapshot.BackendStatusReason)
		m.snapshot = typed.snapshot
		m.requestStats = cloneRequestMetrics(m.snapshot.RequestStats)
		m.flows = typed.flows
		if replaceInitialEntries {
			m.entries = buildInitialEntries(m.snapshot)
		}
		m.syncInputPlaceholder()
		if !m.snapshot.BackendAvailable {
			m.showFlowPicker = false
			m.sessionPicker = nil
			m.skillsPicker = nil
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
		if shouldAutoOpenLoginOverlay(m.snapshot, previouslyAvailable, previouslyAuthRequired, m.localCommand != nil, m.loginForm != nil) {
			return m, executeLocalCommandCmd(m.ctx, m.snapshot, "/login", m.localCommand)
		}
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
			if queuedCmd := m.startQueuedRequest(); queuedCmd != nil {
				return m, queuedCmd
			}
			return m, progressCmd
		}
		m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
		m.finishRequestStats()
		m.resetAssistantChunkState()
		m.activityHint = ""
		m.currentToolLabel = ""
		m.requestCancel = nil
		m.retryAvailable = m.lastRetryableRequest != nil
		m.syncViewport()
		m.input.Focus()
		if queuedCmd := m.startQueuedRequest(); queuedCmd != nil {
			return m, tea.Batch(queuedCmd, m.maybeRefreshSnapshot())
		}
		progressCmd := m.transitionProgress(progressFailed)
		return m, tea.Batch(progressCmd, m.maybeRefreshSnapshot())
	case streamDoneMsg:
		if m.interrupting {
			progressCmd := m.completeInterruptedRequest()
			m.input.Focus()
			if queuedCmd := m.startQueuedRequest(); queuedCmd != nil {
				return m, queuedCmd
			}
			return m, progressCmd
		}
		m.finishRequestStats()
		m.resetAssistantChunkState()
		m.activityHint = ""
		m.currentToolLabel = ""
		m.requestCancel = nil
		m.retryAvailable = false
		m.input.Focus()
		if queuedCmd := m.startQueuedRequest(); queuedCmd != nil {
			return m, tea.Batch(queuedCmd, m.maybeRefreshSessionSnapshot())
		}
		progressCmd := m.transitionProgress(progressIdle)
		return m, tea.Batch(progressCmd, m.maybeRefreshSessionSnapshot())
	case confirmRequestMsg:
		m.pendingConfirm = &confirmState{request: typed.request, responseCh: typed.responseCh, cursor: 0}
		m.resetOverlayScroll()
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
		m.requestStats = cloneRequestMetrics(m.snapshot.RequestStats)
		m.resetAssistantChunkState()
		m.activityHint = ""
		m.entries = buildInitialEntries(m.snapshot)
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("已切换默认智能体: %s -> %s", fallback(previousFlow, "<未配置>"), fallback(m.snapshot.DefaultFlowLabel, sanitizeFlowLabel(m.snapshot.DefaultFlow)))})
		m.showFlowPicker = false
		m.skillsPicker = nil
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
		m.skillsPicker = nil
		m.requestStats = cloneRequestMetrics(m.snapshot.RequestStats)
		m.resetAssistantChunkState()
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
		return m, m.applyLocalCommandResult(typed.result)
	case approvalChangedMsg:
		if typed.err != nil {
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			m.input.Focus()
			return m, tea.Batch(m.transitionProgress(progressFailed), m.maybeRefreshSnapshot())
		}
		m.snapshot = typed.snapshot
		m.approvalPicker = nil
		m.skillsPicker = nil
		m.tasksPicker = nil
		m.taskActionPicker = nil
		m.approvalCursor = 0
		m.requestStats = cloneRequestMetrics(m.snapshot.RequestStats)
		m.input.Focus()
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("已切换命令执行模式: %s", approvalModeLabel(typed.mode))})
		m.syncViewport()
		if m.state.phase == progressApproval {
			return m, m.transitionProgress(progressIdle)
		}
		return m, nil
	case configFormSavedMsg:
		if typed.err != nil {
			if m.configForm != nil {
				var fieldErr ConfigFieldError
				m.configForm.statusText = ""
				m.configForm.statusIsError = false
				m.configForm.fieldErrorKey = ""
				m.configForm.fieldErrorText = ""
				if errors.As(typed.err, &fieldErr) && strings.TrimSpace(fieldErr.Field) != "" {
					m.configForm.fieldErrorKey = strings.TrimSpace(fieldErr.Field)
					m.configForm.fieldErrorText = fieldErr.Error()
					m.focusConfigFormField(configFieldIndexByKey(m.configForm.fieldErrorKey))
				} else {
					m.configForm.statusText = typed.err.Error()
					m.configForm.statusIsError = true
				}
				m.syncLayout()
				return m, tea.Batch(m.transitionProgress(progressIdle), textinput.Blink)
			}
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			return m, tea.Batch(m.transitionProgress(progressFailed), textinput.Blink)
		}
		m.closeConfigForm()
		if cmd := m.applyLocalCommandResult(typed.result); cmd != nil {
			return m, tea.Batch(cmd, textinput.Blink)
		}
		return m, tea.Batch(m.transitionProgress(progressIdle), textinput.Blink)
	case loginFormSavedMsg:
		if typed.err != nil {
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			return m, tea.Batch(m.transitionProgress(progressFailed), textinput.Blink)
		}
		if typed.result.LoginForm == nil {
			m.closeLoginForm()
		}
		if cmd := m.applyLocalCommandResult(typed.result); cmd != nil {
			return m, tea.Batch(cmd, textinput.Blink)
		}
		return m.continueQRCodeWaitIfNeeded()
	case localCommandExecutedMsg:
		if typed.err != nil {
			m.entries = append(m.entries, entry{role: "error", content: typed.err.Error()})
			m.syncViewport()
			return m, tea.Batch(m.transitionProgress(progressFailed), textinput.Blink)
		}
		if cmd := m.applyLocalCommandResult(typed.result); cmd != nil {
			return m, tea.Batch(cmd, textinput.Blink)
		}
		return m.continueQRCodeWaitIfNeeded()
	case qrCodeWaitTickMsg:
		if m.loginForm == nil || m.loginForm.mode != loginFormModeQR || !m.loginForm.qrWaiting {
			return m, tea.Batch(m.transitionProgress(progressIdle), textinput.Blink)
		}
		if strings.TrimSpace(typed.ticket) == "" || strings.TrimSpace(m.loginForm.qrTicket) != strings.TrimSpace(typed.ticket) {
			return m, tea.Batch(m.transitionProgress(progressIdle), textinput.Blink)
		}
		return m, tea.Batch(m.transitionProgress(progressExecuting), submitLoginFormCmd(m.ctx, m.snapshot, LoginFormSubmission{
			Action:          "wait_qr",
			Mode:            m.loginForm.mode,
			Remember:        m.loginForm.remember,
			QRTicket:        strings.TrimSpace(m.loginForm.qrTicket),
			QRLoginURL:      strings.TrimSpace(m.loginForm.qrLoginURL),
			QRScanHint:      strings.TrimSpace(m.loginForm.qrScanHint),
			QRCodeASCII:     m.loginForm.qrCodeASCII,
			QRWaiting:       true,
			QRWaitPollCount: m.loginForm.qrWaitPollCount,
		}, m.loginFormSubmit), textinput.Blink)
	case tea.KeyMsg:
		if m.pendingConfirm != nil {
			return m.handleConfirmKey(typed)
		}
		if m.toolPanel != nil {
			return m.handleToolPanelKey(typed)
		}
		if m.loginForm != nil {
			return m.handleLoginFormKey(typed)
		}
		if m.configForm != nil {
			return m.handleConfigFormKey(typed)
		}
		if m.taskActionPicker != nil {
			return m.handleTaskActionPickerKey(typed)
		}
		if m.approvalPicker != nil {
			return m.handleApprovalPickerKey(typed)
		}
		if m.knowledgePicker != nil {
			return m.handleKnowledgePickerKey(typed)
		}
		if m.tasksPicker != nil {
			return m.handleTasksPickerKey(typed)
		}
		if m.skillsPicker != nil {
			return m.handleSkillsPickerKey(typed)
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
			pending := strings.TrimSpace(m.input.Value())
			if pending != "" {
				queuedUpdated := strings.TrimSpace(m.queuedInput) != ""
				m.queuedInput = pending
				m.input.SetValue("")
				message := "当前任务执行中，下一条输入已加入队列。"
				if queuedUpdated {
					message = "当前任务执行中，已更新排队输入。"
				}
				m.entries = append(m.entries, entry{role: "system", content: message})
				m.syncViewport()
			}
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
		request := m.buildRequest(input, newRequestRunID())
		m.input.SetValue("")
		return m, m.startRequest(request, true)
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
	options := m.confirmOptions()
	optionCount := len(options)
	if optionCount == 0 {
		return m, nil
	}
	switch msg.String() {
	case "left", "h", "up", "k":
		m.resetOverlayScroll()
		if m.pendingConfirm.cursor > 0 {
			m.pendingConfirm.cursor--
		}
		return m, nil
	case "right", "l", "down", "j", "tab":
		m.resetOverlayScroll()
		if m.pendingConfirm.cursor < optionCount-1 {
			m.pendingConfirm.cursor++
		} else {
			m.pendingConfirm.cursor = 0
		}
		return m, nil
	case "pgdown", "f", " ":
		m.scrollOverlayBy(m.overlayPageStep())
		return m, nil
	case "pgup", "b":
		m.scrollOverlayBy(-m.overlayPageStep())
		return m, nil
	case "home", "g":
		m.scrollOverlayTo(0)
		return m, nil
	case "end", "G":
		m.scrollOverlayTo(1 << 30)
		return m, nil
	case "y", "Y":
		m.resetOverlayScroll()
		m.pendingConfirm.cursor = 0
		return m.applyConfirmSelection(options[0])
	case "s", "S":
		if optionCount < 4 {
			return m, nil
		}
		m.resetOverlayScroll()
		m.pendingConfirm.cursor = 2
		return m.applyConfirmSelection(options[2])
	case "a", "A":
		if optionCount < 3 {
			return m, nil
		}
		m.resetOverlayScroll()
		if optionCount >= 4 {
			m.pendingConfirm.cursor = 3
			return m.applyConfirmSelection(options[3])
		}
		m.pendingConfirm.cursor = 2
		return m.applyConfirmSelection(options[2])
	case "enter":
		m.resetOverlayScroll()
		selectedIndex := m.pendingConfirm.cursor
		if selectedIndex < 0 || selectedIndex >= optionCount {
			selectedIndex = 0
		}
		return m.applyConfirmSelection(options[selectedIndex])
	case "n", "N", "q", "esc", "ctrl+c":
		m.resetOverlayScroll()
		m.pendingConfirm.cursor = 1
		return m.applyConfirmSelection(confirmOption{approved: false})
	default:
		return m, nil
	}
}

func (m *model) applyConfirmSelection(option confirmOption) (tea.Model, tea.Cmd) {
	actionCmd := tea.Cmd(nil)
	if option.approved && option.onSelect != nil {
		actionCmd = option.onSelect()
	}
	m.pendingConfirm.responseCh <- option.approved
	close(m.pendingConfirm.responseCh)
	m.pendingConfirm = nil
	if option.approved {
		return m, tea.Batch(actionCmd, m.transitionProgress(progressExecuting), waitForStream(m.eventCh))
	}
	return m, tea.Batch(m.transitionProgress(progressStreaming), waitForStream(m.eventCh))
}

func (m *model) confirmOptions() []confirmOption {
	if m.pendingConfirm == nil {
		return nil
	}
	request := m.pendingConfirm.request
	options := []confirmOption{
		{
			label:    fallback(strings.TrimSpace(request.ApproveLabel), "允许本次"),
			approved: true,
		},
		{
			label:    fallback(strings.TrimSpace(request.RejectLabel), "本次拒绝"),
			approved: false,
		},
	}
	if request.AlwaysApprove != nil || !request.DisableDefaultAlwaysApprove {
		options = append(options, confirmOption{
			label:    fallback(strings.TrimSpace(request.AlwaysApproveLabel), "以后自动同意"),
			approved: true,
			onSelect: func() tea.Cmd { return m.applyConfirmAlwaysApprove(request) },
		})
	}
	if request.AllowAll != nil {
		options = append(options, confirmOption{
			label:    fallback(strings.TrimSpace(request.AllowAllLabel), "允许全部技能"),
			approved: true,
			onSelect: func() tea.Cmd { return m.applyConfirmAllowAll(request) },
		})
	}
	return options
}

func (m *model) applyConfirmAlwaysApprove(request ConfirmRequest) tea.Cmd {
	refreshCmd := tea.Cmd(nil)
	if request.AlwaysApprove != nil {
		if err := request.AlwaysApprove(m.ctx); err != nil {
			m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("保存确认偏好失败: %v", err)})
		} else {
			refreshCmd = m.maybeRefreshSessionSnapshot()
		}
	}
	if request.DisableDefaultAlwaysApprove || m.approvalChange == nil {
		return refreshCmd
	}
	return tea.Batch(refreshCmd, changeApprovalModeCmd(m.ctx, "auto", m.approvalChange))
}

func (m *model) applyConfirmAllowAll(request ConfirmRequest) tea.Cmd {
	if request.AllowAll == nil {
		return nil
	}
	if err := request.AllowAll(m.ctx); err != nil {
		m.entries = append(m.entries, entry{role: "system", content: fmt.Sprintf("保存确认偏好失败: %v", err)})
		return nil
	}
	return m.maybeRefreshSessionSnapshot()
}

func (m model) handleToolPanelKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.toolPanel != nil {
		for _, action := range m.toolPanel.Actions {
			if action.Disabled || strings.TrimSpace(action.Key) == "" {
				continue
			}
			if msg.String() == action.Key {
				m.activityHint = "正在执行技能操作..."
				return m, tea.Batch(m.transitionProgress(progressExecuting), executeLocalCommandCmd(m.ctx, m.snapshot, action.Command, m.localCommand))
			}
		}
	}
	switch msg.String() {
	case "enter", "esc", "q", "ctrl+c":
		m.closeToolPanel()
		return m, nil
	case "down", "j":
		m.toolPanelViewport.LineDown(1)
		return m, nil
	case "up", "k":
		m.toolPanelViewport.LineUp(1)
		return m, nil
	case "pgdown", "f", " ":
		m.toolPanelViewport.HalfViewDown()
		return m, nil
	case "pgup", "b":
		m.toolPanelViewport.HalfViewUp()
		return m, nil
	case "home", "g":
		m.toolPanelViewport.GotoTop()
		return m, nil
	case "end", "G":
		m.toolPanelViewport.GotoBottom()
		return m, nil
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
		if m.configForm.cursor == configFieldSandboxPolicy {
			m.moveConfigSandboxCursor(-1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillReviewPolicy {
			m.moveConfigSkillReviewCursor(-1)
			return m, nil
		}
		if m.configForm.cursor == configFieldWriteConfirmationPolicy {
			m.moveConfigWriteConfirmationCursor(-1)
			return m, nil
		}
		if m.configForm.cursor == configFieldReasoningMode {
			m.moveConfigReasoningCursor(-1)
			return m, nil
		}
		if m.configForm.cursor == configFieldAgentAutoLoopEnabled {
			m.moveConfigAgentAutoLoopCursor(-1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillsInstallScope {
			m.moveConfigSkillsCursor(-1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillsAutoInstallPolicy {
			m.moveConfigSkillsAutoInstallCursor(-1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillsFreeTextSearch {
			m.moveConfigSkillsFreeTextCursor(-1)
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
		if m.configForm.cursor == configFieldSandboxPolicy {
			m.moveConfigSandboxCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillReviewPolicy {
			m.moveConfigSkillReviewCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldWriteConfirmationPolicy {
			m.moveConfigWriteConfirmationCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldReasoningMode {
			m.moveConfigReasoningCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldAgentAutoLoopEnabled {
			m.moveConfigAgentAutoLoopCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillsInstallScope {
			m.moveConfigSkillsCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillsAutoInstallPolicy {
			m.moveConfigSkillsAutoInstallCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillsFreeTextSearch {
			m.moveConfigSkillsFreeTextCursor(1)
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
		if m.configForm.cursor == configFieldSandboxPolicy {
			m.moveConfigSandboxCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillReviewPolicy {
			m.moveConfigSkillReviewCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldWriteConfirmationPolicy {
			m.moveConfigWriteConfirmationCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldReasoningMode {
			m.moveConfigReasoningCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldAgentAutoLoopEnabled {
			m.moveConfigAgentAutoLoopCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillsInstallScope {
			m.moveConfigSkillsCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillsAutoInstallPolicy {
			m.moveConfigSkillsAutoInstallCursor(1)
			return m, nil
		}
		if m.configForm.cursor == configFieldSkillsFreeTextSearch {
			m.moveConfigSkillsFreeTextCursor(1)
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

func (m model) handleLoginFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.loginForm == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		if m.mustCompleteLogin() {
			m.quitting = true
			return m, tea.Quit
		}
		m.closeLoginForm()
		return m, nil
	case "shift+tab", "up":
		m.moveLoginFormCursor(-1)
		return m, nil
	case "tab", "down":
		m.moveLoginFormCursor(1)
		return m, nil
	case "g":
		if m.loginForm.mode == loginFormModeQR {
			return m.submitLoginForm("refresh_qr")
		}
	case "w":
		if m.loginForm.mode == loginFormModeQR && strings.TrimSpace(m.loginForm.qrTicket) != "" {
			return m.submitLoginForm("wait_qr")
		}
	case "ctrl+l":
		return m.submitLoginForm("logout")
	case "left", "h":
		switch m.loginForm.cursor {
		case loginFieldMode:
			return m.switchLoginFormMode(loginFormModePassword)
		}
	case "right", "l":
		switch m.loginForm.cursor {
		case loginFieldMode:
			return m.switchLoginFormMode(loginFormModeQR)
		}
	case " ":
		if m.loginForm.cursor == loginFieldMode {
			return m.toggleLoginFormMode()
		}
	case "enter":
		switch m.loginForm.cursor {
		case loginFieldMode:
			return m.toggleLoginFormMode()
		case loginFieldUsername:
			if m.loginForm.mode == loginFormModePassword {
				m.blurLoginFormField(loginFieldUsername)
				m.loginForm.cursor = loginFieldPassword
				m.focusLoginFormField(loginFieldPassword)
				m.syncLayout()
				return m, nil
			}
			return m.submitLoginForm("")
		default:
			return m.submitLoginForm("")
		}
	}
	if !isLoginTextField(m.loginForm.cursor) || m.loginForm.mode != loginFormModePassword {
		return m, nil
	}
	inputIndex := loginInputIndex(m.loginForm.cursor)
	if inputIndex < 0 || inputIndex >= len(m.loginForm.inputs) {
		return m, nil
	}
	var cmd tea.Cmd
	m.loginForm.inputs[inputIndex], cmd = m.loginForm.inputs[inputIndex].Update(msg)
	return m, cmd
}

func (m model) continueQRCodeWaitIfNeeded() (tea.Model, tea.Cmd) {
	if m.loginForm == nil || m.loginForm.mode != loginFormModeQR || !m.loginForm.qrWaiting || strings.TrimSpace(m.loginForm.qrTicket) == "" {
		return m, tea.Batch(m.transitionProgress(progressIdle), textinput.Blink)
	}
	return m, tea.Batch(m.transitionProgress(progressExecuting), m.scheduleQRCodeWait(), textinput.Blink)
}

func (m model) scheduleQRCodeWait() tea.Cmd {
	if m.loginForm == nil || m.loginForm.mode != loginFormModeQR {
		return nil
	}
	ticket := strings.TrimSpace(m.loginForm.qrTicket)
	if ticket == "" {
		return nil
	}
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return qrCodeWaitTickMsg{ticket: ticket}
	})
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

func (m model) handleKnowledgePickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.knowledgePicker == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.closeKnowledgePicker()
		return m, nil
	case "up", "k":
		if m.knowledgeCursor > 0 {
			m.knowledgeCursor--
		}
		return m, nil
	case "down", "j":
		if m.knowledgeCursor < len(m.knowledgePicker.Items)-1 {
			m.knowledgeCursor++
		}
		return m, nil
	case "tab":
		if len(m.knowledgePicker.Items) > 0 {
			m.knowledgeCursor = (m.knowledgeCursor + 1) % len(m.knowledgePicker.Items)
		}
		return m, nil
	case "enter":
		if len(m.knowledgePicker.Items) == 0 {
			m.closeKnowledgePicker()
			return m, nil
		}
		selected := m.knowledgePicker.Items[m.knowledgeCursor]
		return m, tea.Batch(m.transitionProgress(progressExecuting), executeLocalCommandCmd(m.ctx, m.snapshot, buildKnowledgePickerCommand("select", selected.Code), m.localCommand))
	default:
		return m, nil
	}
}

func (m model) handleSkillsPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.skillsPicker == nil {
		return m, nil
	}
	if m.skillsPicker.SearchMode {
		return m.handleSkillsPickerSearchKey(msg)
	}
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.closeSkillsPicker()
		return m, nil
	case "left", "h", "shift+tab":
		m.switchSkillsPickerTab("local")
		return m, nil
	case "right", "l":
		m.switchSkillsPickerTab("remote")
		return m, nil
	case "up", "k":
		if m.skillsCursor > 0 {
			m.skillsCursor--
		}
		return m, nil
	case "down", "j":
		if m.skillsCursor < m.activeSkillsPickerItemCount()-1 {
			m.skillsCursor++
		}
		return m, nil
	case "tab":
		if m.skillsPicker.ActiveTab == "remote" {
			m.switchSkillsPickerTab("local")
		} else {
			m.switchSkillsPickerTab("remote")
		}
		return m, nil
	case "/", "s":
		if m.skillsPicker.ActiveTab == "remote" {
			m.skillsPicker.SearchMode = true
			return m, nil
		}
		return m, nil
	case "r":
		if m.skillsPicker.ActiveTab == "remote" {
			return m, executeLocalCommandCmd(m.ctx, m.snapshot, buildSkillsPickerCommand("refresh", "", m.skillsPicker.RemoteQuery), m.localCommand)
		}
		return m, nil
	case "i":
		selected, ok := m.selectedRemoteSkill()
		if !ok || m.skillsPicker.ActiveTab != "remote" {
			return m, nil
		}
		m.activityHint = "正在准备安装远程技能..."
		return m, tea.Batch(m.transitionProgress(progressExecuting), executeLocalCommandCmd(m.ctx, m.snapshot, buildSkillsPickerCommand("install-select", selected.Slug, m.skillsPicker.RemoteQuery), m.localCommand))
	case "u":
		selected, ok := m.selectedRemoteSkill()
		if !ok || m.skillsPicker.ActiveTab != "remote" || !selected.Installed {
			return m, nil
		}
		m.activityHint = "正在更新远程技能..."
		return m, tea.Batch(m.transitionProgress(progressExecuting), executeLocalCommandCmd(m.ctx, m.snapshot, buildSkillsPickerCommand("update", selected.Slug, m.skillsPicker.RemoteQuery), m.localCommand))
	case "d":
		selected, ok := m.selectedRemoteSkill()
		if !ok || m.skillsPicker.ActiveTab != "remote" || !selected.Installed {
			return m, nil
		}
		m.activityHint = "正在删除远程技能..."
		return m, tea.Batch(m.transitionProgress(progressExecuting), executeLocalCommandCmd(m.ctx, m.snapshot, buildSkillsPickerCommand("remove", selected.Slug, m.skillsPicker.RemoteQuery), m.localCommand))
	case "enter":
		if m.activeSkillsPickerItemCount() == 0 {
			m.closeSkillsPicker()
			return m, nil
		}
		if m.skillsPicker.ActiveTab == "remote" {
			selected, ok := m.selectedRemoteSkill()
			if !ok {
				return m, nil
			}
			m.activityHint = "正在加载技能详情..."
			return m, tea.Batch(m.transitionProgress(progressExecuting), executeLocalCommandCmd(m.ctx, m.snapshot, buildSkillsPickerCommand("detail", selected.Slug, m.skillsPicker.RemoteQuery), m.localCommand))
		}
		selected := m.skillsPicker.Items[m.skillsCursor]
		m.openToolPanel(buildSkillDetailPanel(selected))
		return m, nil
	default:
		return m, nil
	}
}

func (m model) handleSkillsPickerSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.skillsPicker == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		hadQuery := strings.TrimSpace(m.skillsPicker.RemoteQuery) != ""
		m.skillsPicker.RemoteQuery = ""
		m.skillsPicker.SearchMode = false
		if hadQuery {
			return m, executeLocalCommandCmd(m.ctx, m.snapshot, buildSkillsPickerCommand("refresh", "", ""), m.localCommand)
		}
		return m, nil
	case tea.KeyEnter:
		m.skillsPicker.SearchMode = false
		return m, executeLocalCommandCmd(m.ctx, m.snapshot, buildSkillsPickerCommand("search", "", m.skillsPicker.RemoteQuery), m.localCommand)
	case tea.KeyBackspace, tea.KeyCtrlH:
		if m.skillsPicker.RemoteQuery != "" {
			_, size := utf8.DecodeLastRuneInString(m.skillsPicker.RemoteQuery)
			m.skillsPicker.RemoteQuery = m.skillsPicker.RemoteQuery[:len(m.skillsPicker.RemoteQuery)-size]
		}
		return m, nil
	case tea.KeyRunes:
		m.skillsPicker.RemoteQuery += msg.String()
		return m, nil
	default:
		return m, nil
	}
}

func (m model) handleTasksPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.tasksPicker == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.closeTasksPicker()
		return m, nil
	case "up", "k":
		if m.tasksCursor > 0 {
			m.tasksCursor--
		}
		return m, nil
	case "down", "j":
		if m.tasksCursor < len(m.tasksPicker.Items)-1 {
			m.tasksCursor++
		}
		return m, nil
	case "tab":
		if len(m.tasksPicker.Items) > 0 {
			m.tasksCursor = (m.tasksCursor + 1) % len(m.tasksPicker.Items)
		}
		return m, nil
	case "enter":
		if len(m.tasksPicker.Items) == 0 {
			m.closeTasksPicker()
			return m, nil
		}
		selected := m.tasksPicker.Items[m.tasksCursor]
		m.resetOverlayScroll()
		m.taskActionPicker = buildTaskActionPicker(selected)
		m.taskActionCursor = 0
		return m, nil
	default:
		return m, nil
	}
}

func (m model) handleTaskActionPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.taskActionPicker == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q", "ctrl+c":
		m.closeTaskActionPicker(false)
		return m, nil
	case "up", "k":
		if m.taskActionCursor > 0 {
			m.taskActionCursor--
		}
		return m, nil
	case "down", "j":
		if m.taskActionCursor < len(m.taskActionPicker.Items)-1 {
			m.taskActionCursor++
		}
		return m, nil
	case "tab":
		if len(m.taskActionPicker.Items) > 0 {
			m.taskActionCursor = (m.taskActionCursor + 1) % len(m.taskActionPicker.Items)
		}
		return m, nil
	case "enter":
		if len(m.taskActionPicker.Items) == 0 {
			m.closeTaskActionPicker(true)
			return m, nil
		}
		selected := m.taskActionPicker.Items[m.taskActionCursor]
		m.closeTaskActionPicker(true)
		_, cmd := m.handleLocalSlashCommand(selected.Command)
		return m, cmd
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
		m.footerHint = ""
		chunk := event.Text
		messageID := strings.TrimSpace(event.MessageID)
		stage := strings.TrimSpace(strings.ToLower(event.Stage))
		if chunk == "" {
			if event.SplitAssistant {
				m.splitNextAssistant = true
			}
			if messageID != "" {
				m.lastAssistantMessageID = messageID
			}
			if stage == "end" {
				m.splitNextAssistant = true
			}
			break
		}
		hasAssistantEntry := len(m.entries) > 0 && m.entries[len(m.entries)-1].role == "assistant"
		if !hasAssistantEntry && strings.TrimSpace(chunk) == "" {
			if messageID != "" {
				m.lastAssistantMessageID = messageID
			}
			if stage == "end" {
				m.splitNextAssistant = true
			}
			break
		}
		m.activityHint = ""
		if m.state.phase != progressStreaming {
			cmd = m.transitionProgress(progressStreaming)
		}
		m.requestStats.OutputChars += utf8.RuneCountInString(chunk)
		m.requestStats.ChunkCount++
		startNewAssistantEntry := !hasAssistantEntry || m.splitNextAssistant || event.SplitAssistant
		if !startNewAssistantEntry && messageID != "" && m.lastAssistantMessageID != "" && messageID != m.lastAssistantMessageID {
			startNewAssistantEntry = true
		}
		if startNewAssistantEntry {
			m.entries = append(m.entries, entry{role: "assistant", content: chunk})
		} else {
			m.entries[len(m.entries)-1].content += chunk
			m.footerHint = ""
		}
		m.splitNextAssistant = false
		if messageID != "" {
			m.lastAssistantMessageID = messageID
		}
		if stage == "end" || event.SplitAssistant {
			m.splitNextAssistant = true
		}
	case EventProgressNote:
		if strings.TrimSpace(event.Text) != "" {
			if event.EventType == "compression_progress" {
				m.footerHint = strings.TrimSpace(event.Text)
				break
			}
			m.activityHint = strings.TrimSpace(event.Text)
			m.footerHint = ""
		}
	case EventToolSummary:
		summary := strings.TrimSpace(event.Text)
		if summary != "" {
			m.latestToolSummary = summary
			m.latestToolAt = time.Now()
			m.currentToolLabel = ""
			m.entries = append(m.entries, entry{role: "system", content: summary})
		}
	case EventToolPanel:
		if event.Panel != nil {
			m.openToolPanel(event.Panel)
			if summary := strings.TrimSpace(event.Panel.Summary); summary != "" {
				m.latestToolSummary = summary
				m.latestToolAt = time.Now()
			}
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
		m.currentToolLabel = currentToolLabel(event.Text, "服务端工具调用")
		if note := buildEventTimelineNote("服务端工具调用", event.Text); note != "" {
			m.entries = append(m.entries, entry{role: "system", content: note})
		}
		m.activityHint = "执行 " + m.currentToolLabel
		m.splitNextAssistant = true
	case EventBuiltInCall:
		m.requestStats.CommandCount++
		m.currentToolLabel = currentToolLabel(event.Text, "本地命令执行")
		if note := buildEventTimelineNote("本地命令执行", event.Text); note != "" {
			m.entries = append(m.entries, entry{role: "system", content: note})
		}
		m.activityHint = "执行 " + m.currentToolLabel
		m.splitNextAssistant = true
	case EventClientToolCall:
		m.requestStats.CommandCount++
		m.currentToolLabel = currentToolLabel(event.Text, "本地工具调用")
		if note := buildEventTimelineNote("本地工具调用", event.Text); note != "" {
			m.entries = append(m.entries, entry{role: "system", content: note})
		}
		m.activityHint = "执行 " + m.currentToolLabel
		m.splitNextAssistant = true
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
		} else if strings.TrimSpace(event.SessionTitle) != "" {
			m.snapshot.SessionTitle = strings.TrimSpace(event.SessionTitle)
		}
	}
	m.syncViewport()
	return cmd
}

func buildEventTimelineNote(kind string, detail string) string {
	kind = strings.TrimSpace(kind)
	detail = strings.TrimSpace(detail)
	if kind == "" {
		return detail
	}
	if detail == "" {
		return "正在执行" + kind + "。"
	}
	return "正在执行" + kind + "：" + detail
}

func RenderSessionStatusText(snapshot Snapshot, prefix string) string {
	lines := []string{}
	if strings.TrimSpace(prefix) != "" {
		lines = append(lines, prefix)
	}
	lines = append(lines, fmt.Sprintf("当前模式: %s", requestModeLabel(snapshot.RequestMode)))
	lines = append(lines, fmt.Sprintf("思考模式: %s", reasoningStatus(snapshot.ReasoningEnabled, false)))
	sandboxPolicy := strings.TrimSpace(snapshot.SessionSandboxEscapePolicy)
	if sandboxPolicy == "" {
		sandboxPolicy = "<继承全局配置>"
	}
	lines = append(lines, fmt.Sprintf("会话外部资源访问策略: %s", sandboxPolicy))
	sessionID := strings.TrimSpace(snapshot.SessionID)
	if sessionID == "" {
		lines = append(lines, "新会话")
		return strings.Join(lines, "\n")
	} else if title := strings.TrimSpace(snapshot.SessionTitle); title != "" {
		lines = append(lines, fmt.Sprintf("会话标题: %s", title))
	}
	return strings.Join(lines, "\n")
}

func (m *model) completeInterruptedRequest() tea.Cmd {
	m.finishRequestStats()
	m.resetAssistantChunkState()
	m.activityHint = ""
	m.currentToolLabel = ""
	m.requestCancel = nil
	m.interrupting = false
	m.entries = append(m.entries, entry{role: "system", content: "请求已中断"})
	m.syncViewport()
	return m.transitionProgress(progressIdle)
}

func (m *model) startQueuedRequest() tea.Cmd {
	queued := strings.TrimSpace(m.queuedInput)
	if queued == "" || !m.snapshot.BackendAvailable {
		return nil
	}
	m.queuedInput = ""
	request := m.buildRequest(queued, newRequestRunID())
	return m.startRequest(request, true)
}

func (m *model) startRequest(request Request, appendUserEntry bool) tea.Cmd {
	request.InputFiles = append([]string(nil), request.InputFiles...)
	request.KnowledgeScopes = append([]string(nil), request.KnowledgeScopes...)
	request.KnowledgeDocs = append([]string(nil), request.KnowledgeDocs...)
	if strings.TrimSpace(request.RunID) == "" {
		request.RunID = newRequestRunID()
	}
	if appendUserEntry {
		m.entries = append(m.entries, entry{role: "user", content: request.Input})
	}
	m.activityHint = ""
	m.currentToolLabel = ""
	m.interrupting = false
	m.retryAvailable = false
	m.lastRetryableRequest = cloneRequest(request)
	m.startRequestStats(request.Input, request.RunID)
	m.syncViewport()
	requestCtx, cancel := context.WithCancel(m.ctx)
	m.requestCancel = cancel
	return tea.Batch(m.transitionProgress(progressSending), startStream(requestCtx, request, m.runner, m.eventCh))
}

func cloneRequest(request Request) *Request {
	cloned := request
	cloned.InputFiles = append([]string(nil), request.InputFiles...)
	cloned.KnowledgeScopes = append([]string(nil), request.KnowledgeScopes...)
	cloned.KnowledgeDocs = append([]string(nil), request.KnowledgeDocs...)
	return &cloned
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
	m.syncToolPanelViewport()
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
	if m.loginForm != nil {
		formWidth := minInt(contentWidth-16, 72)
		if formWidth < 24 {
			formWidth = 24
		}
		for index := range m.loginForm.inputs {
			m.loginForm.inputs[index].Width = formWidth
		}
	}
	m.progress.Width = contentWidth - 18
	if m.progress.Width < 12 {
		m.progress.Width = 12
	}
	m.syncViewport()
}

func (m model) useMinimalChrome() bool {
	return m.width > 0 && m.width < 76
}

func (m *model) syncViewport() {
	content := renderEntries(m.entries, m.viewport.Width, m.viewport.Height, m.markdown, m.snapshot)
	if transient := m.renderTransientStatus(); transient != "" {
		if strings.TrimSpace(content) != "" {
			content += "\n"
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
	if m.pendingConfirm != nil || m.toolPanel != nil || m.configForm != nil || m.showFlowPicker || m.sessionPicker != nil || m.knowledgePicker != nil || m.skillsPicker != nil || m.tasksPicker != nil || m.taskActionPicker != nil || m.approvalPicker != nil {
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
	workdirDisplay := trimMiddleLeftForWidth(workdir, workdirWidth)
	if m.useMinimalChrome() {
		lines := []string{
			trimForWidth(sessionTitle, contentWidth),
			trimForWidth(versionLine, contentWidth),
			trimForWidth(flowLabelDisplay, contentWidth),
			trimMiddleLeftForWidth(workdir, contentWidth),
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
	rightParts := []string{}
	if footerHint := strings.TrimSpace(m.footerHint); footerHint != "" {
		rightParts = append(rightParts, statusBarDimStyle.Render(trimForWidth(footerHint, maxInt(18, renderWidth/3))))
	}
	if contextGrid := m.contextUsageGrid(); contextGrid != "" {
		rightParts = append(rightParts, contextGrid)
	}
	if contextHint := m.inputContextUsageHint(); contextHint != "" {
		rightParts = append(
			rightParts,
			statusBarDimStyle.Render(trimForWidth(contextHint, maxInt(12, renderWidth/4))),
		)
	}
	rightColumn := strings.Join(rightParts, " ")
	rightWidth := lipgloss.Width(rightColumn)
	leftWidth := bodyWidth
	if rightWidth > 0 {
		leftWidth -= rightWidth + 2
		if leftWidth < 12 {
			leftWidth = 12
		}
	}
	segments := m.statusBarSegments(leftWidth)
	body := trimForWidth(joinStatusSegments(segments, " · "), leftWidth)
	leftColumn := statusBarDimStyle.Render(body)
	bodyLine := leftColumn
	if rightWidth > 0 {
		spacerWidth := bodyWidth - lipgloss.Width(leftColumn) - rightWidth
		if spacerWidth < 1 {
			spacerWidth = 1
		}
		bodyLine = lipgloss.JoinHorizontal(
			lipgloss.Left,
			leftColumn,
			strings.Repeat(" ", spacerWidth),
			rightColumn,
		)
	}
	line := lipgloss.JoinHorizontal(lipgloss.Left, stateChip, " ", bodyLine)
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
	content := ""
	width := 0
	fullScreenLogin := false
	if m.pendingConfirm != nil {
		width = minInt(m.contentWidth-4, 112)
		content = m.renderConfirmOverlay()
	} else if m.toolPanel != nil {
		width = minInt(m.contentWidth-6, 96)
		content = m.renderToolPanel()
	} else if m.loginForm != nil {
		content = m.renderLoginForm()
		fullScreenLogin = true
	} else if m.configForm != nil {
		width = minInt(m.contentWidth-6, 92)
		content = m.renderConfigForm()
	} else if m.taskActionPicker != nil {
		width = minInt(m.contentWidth-6, 92)
		content = m.renderTaskActionPicker()
	} else if m.approvalPicker != nil {
		width = minInt(m.contentWidth-6, 90)
		content = m.renderApprovalPicker()
	} else if m.tasksPicker != nil {
		width = minInt(m.contentWidth-6, 100)
		content = m.renderTasksPicker()
	} else if m.knowledgePicker != nil {
		width = minInt(m.contentWidth-6, 92)
		content = m.renderKnowledgePicker()
	} else if m.skillsPicker != nil {
		width = minInt(m.contentWidth-6, 100)
		content = m.renderSkillsPicker()
	} else if m.sessionPicker != nil {
		width = minInt(m.contentWidth-6, 90)
		content = m.renderSessionPicker()
	} else if m.showFlowPicker {
		width = minInt(m.contentWidth-6, 90)
		content = m.renderFlowPicker()
	}
	if content == "" {
		return ""
	}
	if fullScreenLogin {
		innerWidth := m.width - 2
		if innerWidth < 20 {
			innerWidth = 20
		}
		innerHeight := m.height - 2
		if innerHeight < 5 {
			innerHeight = 5
		}
		return overlayStyle.Width(innerWidth).Height(innerHeight).Render(m.fitOverlayContent(content))
	}
	if width < 24 {
		width = 24
	}
	return overlayStyle.Width(width).Render(m.fitOverlayContent(content))
}

func (m model) renderConfirmOverlay() string {
	request := m.pendingConfirm.request
	options := m.confirmOptions()
	lines := []string{request.Title}
	if strings.TrimSpace(request.Command) != "" {
		lines = append(lines, "命令: "+request.Command)
	}
	if strings.TrimSpace(request.Detail) != "" {
		lines = append(lines, request.Detail)
	}
	if strings.TrimSpace(request.PreviewTitle) != "" {
		lines = append(lines, request.PreviewTitle)
	}
	if strings.TrimSpace(request.PreviewBody) != "" {
		lines = append(lines, hardWrapText(request.PreviewBody, maxInt(24, minInt(m.contentWidth-14, 104))))
	}
	renderedOptions := make([]string, 0, len(options))
	for index, option := range options {
		renderedOptions = append(renderedOptions, m.renderOverlayOption("[ "+option.label+" ]", m.pendingConfirm.cursor == index))
	}
	optionsLine := ""
	if len(renderedOptions) > 0 {
		optionsLine = renderedOptions[0]
		for _, option := range renderedOptions[1:] {
			optionsLine = lipgloss.JoinHorizontal(lipgloss.Left, optionsLine, "  ", option)
		}
	}
	hint := strings.TrimSpace(request.Hint)
	if hint == "" {
		hint = confirmHintForOptionCount(len(options))
	}
	lines = append(lines, "", markOverlayFocus(optionsLine, true), hint)
	return strings.Join(lines, "\n\n")
}

func confirmHintForOptionCount(optionCount int) string {
	switch optionCount {
	case 4:
		return "Y 允许本次，N 本次拒绝，S 始终允许此技能，A 允许全部技能；方向键或 Tab 选择，Enter 确认。"
	case 3:
		return "Y 允许本次，N 本次拒绝，A 以后自动同意；方向键或 Tab 选择，Enter 确认。"
	default:
		return "Y 允许本次，N 本次拒绝；方向键或 Tab 选择，Enter 确认。"
	}
}

func (m model) renderToolPanel() string {
	if m.toolPanel == nil {
		return ""
	}
	panel := m.toolPanel
	overlayWidth := maxInt(24, m.contentWidth-16)
	lines := []string{trimForWidth(fallback(strings.TrimSpace(panel.Title), "工具结果"), overlayWidth)}
	if strings.TrimSpace(panel.Summary) != "" {
		lines = append(lines, hintStyle.Render(trimForWidth(panel.Summary, overlayWidth)))
	}
	if strings.TrimSpace(panel.Body) != "" {
		lines = append(lines, "", m.toolPanelViewport.View())
	}
	if len(panel.Actions) > 0 {
		parts := make([]string, 0, len(panel.Actions))
		for _, action := range panel.Actions {
			label := fmt.Sprintf("[%s] %s", action.Key, action.Label)
			if action.Disabled {
				label += "(不可用)"
			}
			parts = append(parts, label)
		}
		lines = append(lines, "", hintStyle.Render(trimForWidth("操作: "+strings.Join(parts, "  "), overlayWidth)))
	}
	hint := strings.TrimSpace(panel.Hint)
	if hint == "" {
		hint = "按 Enter 或 Esc 关闭，方向键或 j/k 滚动。"
	} else {
		hint += " 方向键或 j/k 滚动。"
	}
	lines = append(lines, "", hintStyle.Render(trimForWidth(hint, overlayWidth)))
	return strings.Join(lines, "\n")
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
		lines = append(lines, markOverlayFocus(line, index == m.approvalCursor))
		if strings.TrimSpace(option.Description) != "" {
			lines = append(lines, hintStyle.Render(trimForWidth(option.Description, m.contentWidth-16)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) renderLoginForm() string {
	if m.loginForm == nil {
		return ""
	}
	hasVerification := hasLoginVerificationChallenge(m.loginForm)
	title := strings.TrimSpace(m.loginForm.title)
	if title == "" {
		title = "登录"
	}
	lines := []string{headerBarStyle.Render("太乙智启 · " + title)}
	if strings.TrimSpace(m.loginForm.hint) != "" {
		lines = append(lines, strings.TrimSpace(m.loginForm.hint))
	}
	if strings.TrimSpace(m.loginForm.statusText) != "" {
		statusLabel := labelStyle.Render("当前状态(status)")
		statusBody := hintStyle.Render(hardWrapText(strings.TrimSpace(m.loginForm.statusText), maxInt(24, m.contentWidth-16)))
		if m.loginForm.statusIsError {
			statusLabel = errorMessageStyle.Render("登录提示(status)")
			statusBody = errorMessageStyle.Render(hardWrapText(strings.TrimSpace(m.loginForm.statusText), maxInt(24, m.contentWidth-16)))
		}
		lines = append(lines, "", statusLabel, statusBody)
	}
	lines = append(lines, "")

	if m.loginForm.supportsQRCode {
		modeLabel := "密码登录(password)"
		modeAlt := "扫码登录(qr)"
		if m.loginForm.mode == loginFormModeQR {
			modeLabel = "扫码登录(qr)"
			modeAlt = "密码登录(password)"
		}
		modeLine := fmt.Sprintf("当前方式: %s  切换到 %s", modeLabel, modeAlt)
		if m.loginForm.cursor == loginFieldMode {
			modeLine = markOverlayFocus(selectedFlowStyle.Render(trimForWidth(modeLine, m.contentWidth-16)), true)
		} else {
			modeLine = commandHintStyle.Render(trimForWidth(modeLine, m.contentWidth-16))
		}
		lines = append(lines, labelStyle.Render("登录方式(login_mode)"), modeLine, "")
	}

	if m.loginForm.mode == loginFormModePassword {
		if hasVerification {
			if username := strings.TrimSpace(m.loginForm.inputs[0].Value()); username != "" {
				lines = append(lines, labelStyle.Render("登录账号(username)"), valueStyle.Render(trimForWidth(username, m.contentWidth-16)), "")
			}
			lines = append(lines, m.renderLoginFormTextField(loginFieldPassword, "密码(password)", "请输入密码"))
			if target := strings.TrimSpace(m.loginForm.verifyTarget); target != "" {
				lines = append(lines,
					labelStyle.Render("验证目标(verify_target)"),
					valueStyle.Render(trimForWidth(target, m.contentWidth-16)),
					"",
				)
			}
			if verificationImage := m.renderLoginVerificationImage(); verificationImage != "" {
				lines = append(lines, labelStyle.Render("图形验证码(image)"), verificationImage, "")
				if previewHint := loginVerificationImagePreviewHint(m.loginForm, m.contentWidth-16); previewHint != "" {
					lines = append(lines, previewHint, "")
				}
				if imageHint := loginVerificationImageHint(m.loginForm); imageHint != "" {
					lines = append(lines, hintStyle.Render(trimForWidth(imageHint, m.contentWidth-16)), "")
				}
				if backupASCII := renderLoginVerificationImageBackupASCII(m.loginForm); backupASCII != "" {
					lines = append(lines,
						labelStyle.Render("ASCII 备用图(ascii_fallback)"),
						backupASCII,
						hintStyle.Render(trimForWidth("如果上方终端图片未显示，请以下方 ASCII 备用图为准。", m.contentWidth-16)),
						"",
					)
				}
			}
			lines = append(lines, m.renderLoginFormTextField(loginFieldCaptchaCode, loginVerificationCodeLabel(m.loginForm.verifyType), loginVerificationDescription(m.loginForm.verifyType, m.loginForm.verifyHint)))
		} else {
			lines = append(lines,
				m.renderLoginFormTextField(loginFieldUsername, "用户名(username)", "请输入用户名"),
				m.renderLoginFormTextField(loginFieldPassword, "密码(password)", "请输入密码"),
			)
		}
	} else {
		if strings.TrimSpace(m.loginForm.qrCodeASCII) != "" {
			lines = append(lines, labelStyle.Render("终端二维码(qr_code)"), m.loginForm.qrCodeASCII)
		}
		if strings.TrimSpace(m.loginForm.qrScanHint) != "" {
			lines = append(lines, labelStyle.Render("扫码提示(qr_hint)"), hintStyle.Render(hardWrapText(strings.TrimSpace(m.loginForm.qrScanHint), maxInt(24, m.contentWidth-16))))
		}
		if strings.TrimSpace(m.loginForm.qrTicket) != "" {
			lines = append(lines, hintStyle.Render(trimForWidth("操作提示: g 重新生成二维码，Enter 或 w 继续等待扫码确认。", m.contentWidth-16)))
		}
		lines = append(lines, "")
	}

	lines = append(lines, m.renderLoginSubmitField(), "")
	if m.loginForm.mode == loginFormModeQR {
		lines = append(lines, commandHintStyle.Render("Tab 切换字段，左右键切换登录方式，g 重新生成二维码，Enter 或 w 继续等待，Ctrl+L 退出登录，Esc 关闭。"))
	} else {
		lines = append(lines, commandHintStyle.Render("Tab 切换字段，左右键切换登录方式，Enter 提交，Ctrl+L 退出登录，Esc 关闭。"))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderLoginFormTextField(field int, label string, description string) string {
	if m.loginForm == nil {
		return ""
	}
	inputIndex := loginInputIndex(field)
	if inputIndex < 0 || inputIndex >= len(m.loginForm.inputs) {
		return ""
	}
	renderedLabel := labelStyle.Render(label)
	if m.loginForm.cursor == field {
		renderedLabel = markOverlayFocus(selectedFlowStyle.Render(trimForWidth(label, m.contentWidth-16)), true)
	}
	parts := []string{renderedLabel, m.loginForm.inputs[inputIndex].View()}
	if strings.TrimSpace(description) != "" {
		parts = append(parts, hintStyle.Render(trimForWidth(description, m.contentWidth-16)))
	}
	parts = append(parts, "")
	return strings.Join(parts, "\n")
}

func (m model) renderLoginVerificationImage() string {
	if m.loginForm == nil {
		return ""
	}
	if protocolImage := renderTerminalVerificationImage(m.loginForm, m.contentWidth-16); protocolImage != "" {
		return protocolImage
	}
	if colorImage := renderANSIColorVerificationImage(m.loginForm.verifyImagePNGBase64, m.contentWidth-16); colorImage != "" {
		return colorImage
	}
	return upscaleVerificationASCIIArt(m.loginForm.verifyImageASCII, loginVerificationASCIIScale(m.loginForm.verifyImageASCII, m.contentWidth))
}

func loginVerificationImageHint(state *loginFormState) string {
	if state == nil || strings.TrimSpace(state.verifyImagePNGBase64) == "" {
		return ""
	}
	switch detectTerminalImageProtocol() {
	case "kitty":
		if runningInsideTmux() {
			return "当前通过 tmux 透传 kitty 终端图片协议显示图形验证码。"
		}
		return "当前使用 kitty 终端图片协议显示图形验证码。"
	case "iterm2":
		if runningInsideTmux() {
			return "当前通过 tmux 透传 iTerm2 终端图片协议显示图形验证码。"
		}
		return "当前使用 iTerm2 终端图片协议显示图形验证码。"
	default:
		if canRenderANSIColorVerificationImage(state.verifyImagePNGBase64) {
			return "当前终端未启用专用图片协议，已回退为 ANSI 彩色块预览。"
		}
		if strings.TrimSpace(state.verifyImageASCII) == "" {
			return ""
		}
		return "当前终端未启用图片协议，已回退为 ASCII 图形验证码。"
	}
}

func renderLoginVerificationImageBackupASCII(state *loginFormState) string {
	if state == nil {
		return ""
	}
	if detectTerminalImageProtocol() == "" {
		return ""
	}
	return strings.TrimSpace(state.verifyImageASCII)
}

func loginVerificationImagePreviewHint(state *loginFormState, width int) string {
	if state == nil {
		return ""
	}
	previewPath := strings.TrimSpace(state.verifyImagePreviewPath)
	if previewPath == "" {
		return ""
	}
	lines := []string{hintStyle.Render(hardWrapText("已将图形验证码保存到本地图片，并尝试用系统预览打开。", width))}
	return strings.Join(lines, "\n")
}

func (m model) renderLoginSubmitField() string {
	if m.loginForm == nil {
		return ""
	}
	label := "提交登录"
	if m.loginForm.mode == loginFormModeQR {
		if strings.TrimSpace(m.loginForm.qrTicket) == "" {
			label = "生成扫码地址"
		} else {
			label = "继续等待(Enter/w)"
		}
	}
	line := fmt.Sprintf("[%s] Enter", label)
	if m.loginForm.cursor == loginFieldSubmit {
		return markOverlayFocus(selectedFlowStyle.Render(trimForWidth(line, m.contentWidth-16)), true)
	}
	return commandHintStyle.Render(trimForWidth(line, m.contentWidth-16))
}

func (m model) renderConfigForm() string {
	if m.configForm == nil {
		return ""
	}

	lines := []string{fallback(strings.TrimSpace(m.configForm.title), "配置设置")}
	if strings.TrimSpace(m.configForm.hint) != "" {
		lines = append(lines, strings.TrimSpace(m.configForm.hint))
	}
	if strings.TrimSpace(m.configForm.statusText) != "" {
		statusText := hardWrapText(strings.TrimSpace(m.configForm.statusText), maxInt(24, m.contentWidth-16))
		if m.configForm.statusIsError {
			lines = append(lines, errorMessageStyle.Render(statusText))
		} else {
			lines = append(lines, hintStyle.Render(statusText))
		}
	}
	if strings.TrimSpace(m.configForm.path) != "" {
		lines = append(lines, hintStyle.Render(trimForWidth("配置文件: "+strings.TrimSpace(m.configForm.path), m.contentWidth-14)))
	}
	lines = append(lines, "")

	for index, spec := range configFormFieldSpecs() {
		label := spec.label
		if index == m.configForm.cursor {
			label = selectedFlowStyle.Render(trimForWidth(label, m.contentWidth-16))
		} else {
			label = labelStyle.Render(trimForWidth(label, m.contentWidth-16))
		}
		if index == m.configForm.cursor && !m.configForm.flowOpen {
			label = markOverlayFocus(label, true)
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
		case configFieldSandboxPolicy:
			lines = append(lines, m.renderConfigSandboxField())
		case configFieldSkillReviewPolicy:
			lines = append(lines, m.renderConfigSkillReviewField())
		case configFieldWriteConfirmationPolicy:
			lines = append(lines, m.renderConfigWriteConfirmationField())
		case configFieldReasoningMode:
			lines = append(lines, m.renderConfigReasoningField())
		case configFieldAgentAutoLoopEnabled:
			lines = append(lines, m.renderConfigAgentAutoLoopField())
		case configFieldSkillsInstallScope:
			lines = append(lines, m.renderConfigSkillsInstallScopeField())
		case configFieldSkillsAutoInstallPolicy:
			lines = append(lines, m.renderConfigSkillsAutoInstallField())
		case configFieldSkillsFreeTextSearch:
			lines = append(lines, m.renderConfigSkillsFreeTextSearchField())
		default:
			lines = append(lines, m.configForm.inputs[index].View())
		}
		if key := configFieldKey(index); key != "" && strings.TrimSpace(m.configForm.fieldErrorText) != "" && m.configForm.fieldErrorKey == key {
			lines = append(lines, errorMessageStyle.Render(trimForWidth(m.configForm.fieldErrorText, m.contentWidth-16)))
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
		commandHintStyle.Render(submitText+"，退出键(Esc)关闭，切换键(Tab)切换字段，回车键(Enter)下一项，保存键(Ctrl+S)保存。"),
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
		line += "  [回车键(Enter)选择]"
	}
	if m.configForm.cursor == configFieldDefaultFlow {
		return markOverlayFocus(selectedFlowStyle.Render(trimForWidth(line, m.contentWidth-16)), !m.configForm.flowOpen)
	}
	return commandHintStyle.Render(trimForWidth(line, m.contentWidth-16))
}

func (m model) renderConfigFlowDropdown() string {
	if m.configForm == nil || len(m.configForm.flowOptions) == 0 {
		return hintStyle.Render("暂无可选智能体。")
	}
	lines := []string{hintStyle.Render("选择默认智能体：上下键移动，回车键(Enter)确认。")}
	for index, option := range m.configForm.flowOptions {
		line := fmt.Sprintf("%s  %s", flowCursor(index == m.configForm.flowCursor), option.Label)
		if index == m.configForm.flowCursor {
			lines = append(lines, markOverlayFocus(selectedFlowStyle.Render(trimForWidth(line, m.contentWidth-20)), true))
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

func (m model) renderConfigSandboxField() string {
	if m.configForm == nil {
		return ""
	}
	parts := make([]string, 0, len(configSandboxChoices()))
	for index, option := range configSandboxChoices() {
		selected := index == m.configForm.sandboxCursor
		marker := "( )"
		if selected {
			marker = "(*)"
		}
		item := marker + " " + option.Label
		if m.configForm.cursor == configFieldSandboxPolicy && selected {
			parts = append(parts, selectedFlowStyle.Render(item))
		} else if selected {
			parts = append(parts, valueStyle.Render(item))
		} else {
			parts = append(parts, commandHintStyle.Render(item))
		}
	}
	return strings.Join(parts, "  ")
}

func (m model) renderConfigSkillReviewField() string {
	if m.configForm == nil {
		return ""
	}
	parts := make([]string, 0, len(configSkillReviewChoices()))
	for index, option := range configSkillReviewChoices() {
		selected := index == m.configForm.skillReviewCursor
		marker := "( )"
		if selected {
			marker = "(*)"
		}
		item := marker + " " + option.Label
		if m.configForm.cursor == configFieldSkillReviewPolicy && selected {
			parts = append(parts, selectedFlowStyle.Render(item))
		} else if selected {
			parts = append(parts, valueStyle.Render(item))
		} else {
			parts = append(parts, commandHintStyle.Render(item))
		}
	}
	return strings.Join(parts, "  ")
}

func (m model) renderConfigWriteConfirmationField() string {
	if m.configForm == nil {
		return ""
	}
	parts := make([]string, 0, len(configWriteConfirmationChoices()))
	for index, option := range configWriteConfirmationChoices() {
		selected := index == m.configForm.writeConfirmationCursor
		marker := "( )"
		if selected {
			marker = "(*)"
		}
		item := marker + " " + option.Label
		if m.configForm.cursor == configFieldWriteConfirmationPolicy && selected {
			parts = append(parts, selectedFlowStyle.Render(item))
		} else if selected {
			parts = append(parts, valueStyle.Render(item))
		} else {
			parts = append(parts, commandHintStyle.Render(item))
		}
	}
	return strings.Join(parts, "  ")
}

func (m model) renderConfigReasoningField() string {
	if m.configForm == nil {
		return ""
	}
	parts := make([]string, 0, len(configReasoningChoices()))
	for index, option := range configReasoningChoices() {
		selected := index == m.configForm.reasoningCursor
		marker := "( )"
		if selected {
			marker = "(*)"
		}
		item := marker + " " + option.Label
		if m.configForm.cursor == configFieldReasoningMode && selected {
			parts = append(parts, selectedFlowStyle.Render(item))
		} else if selected {
			parts = append(parts, valueStyle.Render(item))
		} else {
			parts = append(parts, commandHintStyle.Render(item))
		}
	}
	return strings.Join(parts, "  ")
}

func (m model) renderConfigAgentAutoLoopField() string {
	if m.configForm == nil {
		return ""
	}
	parts := make([]string, 0, len(configAgentAutoLoopChoices()))
	for index, option := range configAgentAutoLoopChoices() {
		selected := index == m.configForm.agentAutoLoopCursor
		marker := "( )"
		if selected {
			marker = "(*)"
		}
		item := marker + " " + option.Label
		if m.configForm.cursor == configFieldAgentAutoLoopEnabled && selected {
			parts = append(parts, selectedFlowStyle.Render(item))
		} else if selected {
			parts = append(parts, valueStyle.Render(item))
		} else {
			parts = append(parts, commandHintStyle.Render(item))
		}
	}
	return strings.Join(parts, "  ")
}

func (m model) renderConfigSkillsInstallScopeField() string {
	if m.configForm == nil {
		return ""
	}
	parts := make([]string, 0, len(configSkillsInstallScopeChoices()))
	for index, option := range configSkillsInstallScopeChoices() {
		selected := index == m.configForm.skillsCursor
		marker := "( )"
		if selected {
			marker = "(*)"
		}
		item := marker + " " + option.Label
		if m.configForm.cursor == configFieldSkillsInstallScope && selected {
			parts = append(parts, selectedFlowStyle.Render(item))
		} else if selected {
			parts = append(parts, valueStyle.Render(item))
		} else {
			parts = append(parts, commandHintStyle.Render(item))
		}
	}
	return strings.Join(parts, "  ")
}

func (m model) renderConfigSkillsAutoInstallField() string {
	if m.configForm == nil {
		return ""
	}
	parts := make([]string, 0, len(configSkillsAutoInstallChoices()))
	for index, option := range configSkillsAutoInstallChoices() {
		selected := index == m.configForm.skillsAutoInstallCursor
		marker := "( )"
		if selected {
			marker = "(*)"
		}
		item := marker + " " + option.Label
		if m.configForm.cursor == configFieldSkillsAutoInstallPolicy && selected {
			parts = append(parts, selectedFlowStyle.Render(item))
		} else if selected {
			parts = append(parts, valueStyle.Render(item))
		} else {
			parts = append(parts, commandHintStyle.Render(item))
		}
	}
	return strings.Join(parts, "  ")
}

func (m model) renderConfigSkillsFreeTextSearchField() string {
	if m.configForm == nil {
		return ""
	}
	parts := make([]string, 0, len(configSkillsFreeTextSearchChoices()))
	for index, option := range configSkillsFreeTextSearchChoices() {
		selected := index == m.configForm.skillsFreeTextCursor
		marker := "( )"
		if selected {
			marker = "(*)"
		}
		item := marker + " " + option.Label
		if m.configForm.cursor == configFieldSkillsFreeTextSearch && selected {
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
	overlayWidth := maxInt(24, m.contentWidth-16)
	lines := []string{
		trimForWidth(m.sessionPicker.Title, overlayWidth),
		trimForWidth(fallback(strings.TrimSpace(m.sessionPicker.Hint), "使用方向键选择，左右键翻页，Enter 切换当前会话，Esc 关闭。"), overlayWidth),
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
		lines = append(lines, markOverlayFocus(line, index == m.sessionCursor))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderSkillsPicker() string {
	if m.skillsPicker == nil {
		return ""
	}
	overlayWidth := maxInt(24, m.contentWidth-16)
	localTab := m.renderOverlayOption("本地技能", m.skillsPicker.ActiveTab != "remote")
	remoteTab := m.renderOverlayOption("远程仓库", m.skillsPicker.ActiveTab == "remote")
	lines := []string{
		trimForWidth(m.skillsPicker.Title, overlayWidth),
		trimForWidth(fallback(strings.TrimSpace(m.skillsPicker.Hint), "使用方向键选择，Enter 查看详情，Esc 关闭。"), overlayWidth),
		trimForWidth(localTab+"  "+remoteTab, overlayWidth),
		"",
	}
	if m.skillsPicker.ActiveTab == "remote" {
		if strings.TrimSpace(m.skillsPicker.RegistryLabel) != "" {
			lines = append(lines, hintStyle.Render(trimForWidth("仓库: "+m.skillsPicker.RegistryLabel, overlayWidth)))
		}
		query := strings.TrimSpace(m.skillsPicker.RemoteQuery)
		if query == "" {
			query = "*"
		}
		searchLine := "搜索: " + query
		if m.skillsPicker.SearchMode {
			searchLine += "_"
		}
		lines = append(lines, trimForWidth(searchLine, overlayWidth))
		lines = append(lines, hintStyle.Render(trimForWidth(fallback(strings.TrimSpace(m.skillsPicker.RemoteHint), "按 / 输入搜索词，Enter 查看详情，i 安装，u 更新，d 删除，r 刷新。"), overlayWidth)))
		if strings.TrimSpace(m.skillsPicker.RemoteError) != "" {
			lines = append(lines, errorMessageStyle.Render(trimForWidth(m.skillsPicker.RemoteError, overlayWidth)))
		}
		if len(m.skillsPicker.RemoteItems) == 0 {
			lines = append(lines, hintStyle.Render("当前没有可展示的远程技能。"))
			return strings.Join(lines, "\n")
		}
		for index, item := range m.skillsPicker.RemoteItems {
			name := item.Slug
			if strings.TrimSpace(item.DisplayName) != "" {
				name = item.DisplayName + " (" + item.Slug + ")"
			}
			line := fmt.Sprintf("%s  %s", flowCursor(index == m.skillsCursor), name)
			meta := []string{}
			if strings.TrimSpace(item.Version) != "" {
				meta = append(meta, "最新 "+item.Version)
			}
			if popularityLabel := formatSkillPopularity(item.Popularity); popularityLabel != "" {
				meta = append(meta, popularityLabel)
			}
			if downloadsLabel := formatSkillDownloads(item.Downloads); downloadsLabel != "" {
				meta = append(meta, downloadsLabel)
			}
			if item.Installed {
				installedLabel := "已安装"
				if strings.TrimSpace(item.InstalledVersion) != "" {
					installedLabel += " " + item.InstalledVersion
				}
				meta = append(meta, installedLabel)
				if item.CanUpdate {
					meta = append(meta, "可更新")
				}
			}
			if len(meta) > 0 {
				line += "  " + strings.Join(meta, "  ")
			}
			if strings.TrimSpace(item.Summary) != "" && strings.TrimSpace(item.Summary) != ">" {
				line += "\n   " + item.Summary
			}
			line = trimMultilineForWidth(line, overlayWidth)
			if index == m.skillsCursor {
				line = selectedFlowStyle.Render(line)
			} else {
				line = commandHintStyle.Render(line)
			}
			lines = append(lines, markOverlayFocus(line, index == m.skillsCursor))
		}
		return strings.Join(lines, "\n")
	}
	if len(m.skillsPicker.Items) == 0 {
		lines = append(lines, hintStyle.Render("当前没有可展示的技能。"))
		return strings.Join(lines, "\n")
	}
	for index, item := range m.skillsPicker.Items {
		line := fmt.Sprintf("%s  %s", flowCursor(index == m.skillsCursor), item.Name)
		meta := []string{}
		if strings.TrimSpace(item.SourceLabel) != "" {
			meta = append(meta, item.SourceLabel)
		}
		if strings.TrimSpace(item.StateLabel) != "" {
			meta = append(meta, item.StateLabel)
		}
		if strings.TrimSpace(item.Restriction) != "" {
			meta = append(meta, item.Restriction)
		}
		if len(item.ReferenceFiles) > 0 {
			meta = append(meta, fmt.Sprintf("参考文件 %d", len(item.ReferenceFiles)))
		}
		if len(meta) > 0 {
			line += "  " + strings.Join(meta, "  ")
		}
		description := strings.TrimSpace(item.Description)
		if description != "" {
			line += "\n   " + description
		}
		line = trimMultilineForWidth(line, overlayWidth)
		if index == m.skillsCursor {
			line = selectedFlowStyle.Render(line)
		} else {
			line = commandHintStyle.Render(line)
		}
		lines = append(lines, markOverlayFocus(line, index == m.skillsCursor))
	}
	return strings.Join(lines, "\n")
}

func formatSkillPopularity(value float64) string {
	if value <= 0 {
		return ""
	}
	formatted := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".")
	if formatted == "" {
		return ""
	}
	return "人气 " + formatted
}

func formatSkillDownloads(value int64) string {
	if value <= 0 {
		return ""
	}
	return fmt.Sprintf("下载 %d", value)
}

func (m model) renderTasksPicker() string {
	if m.tasksPicker == nil {
		return ""
	}
	lines := []string{
		m.tasksPicker.Title,
		fallback(strings.TrimSpace(m.tasksPicker.Hint), "使用方向键选择任务，Enter 打开操作菜单，Esc 关闭。"),
		"",
	}
	if len(m.tasksPicker.Items) == 0 {
		lines = append(lines, hintStyle.Render("当前没有可展示的后台任务。"))
		return strings.Join(lines, "\n")
	}
	for index, item := range m.tasksPicker.Items {
		line := fmt.Sprintf("%s  %s [%s]", flowCursor(index == m.tasksCursor), fallback(strings.TrimSpace(item.Name), item.ID), fallback(strings.TrimSpace(item.Status), "unknown"))
		meta := []string{}
		if strings.TrimSpace(item.SessionID) != "" {
			meta = append(meta, "session "+item.SessionID)
		}
		if strings.TrimSpace(item.ExitCode) != "" {
			meta = append(meta, "exit "+item.ExitCode)
		}
		if len(meta) > 0 {
			line += "  " + strings.Join(meta, "  ")
		}
		if strings.TrimSpace(item.Command) != "" {
			line += "\n   " + item.Command
		}
		if strings.TrimSpace(item.CWD) != "" {
			line += "\n   cwd: " + item.CWD
		}
		if index == m.tasksCursor {
			line = selectedFlowStyle.Render(trimForWidth(line, m.contentWidth-16))
		} else {
			line = commandHintStyle.Render(trimForWidth(line, m.contentWidth-16))
		}
		lines = append(lines, markOverlayFocus(line, index == m.tasksCursor))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderTaskActionPicker() string {
	if m.taskActionPicker == nil {
		return ""
	}
	lines := []string{
		m.taskActionPicker.Title,
		fallback(strings.TrimSpace(m.taskActionPicker.Hint), "使用方向键选择操作，Enter 执行，Esc 返回任务列表。"),
		"",
	}
	for index, item := range m.taskActionPicker.Items {
		line := fmt.Sprintf("%s  %s", flowCursor(index == m.taskActionCursor), item.Label)
		if strings.TrimSpace(item.Description) != "" {
			line += "\n   " + item.Description
		}
		if index == m.taskActionCursor {
			line = selectedFlowStyle.Render(trimForWidth(line, m.contentWidth-16))
		} else {
			line = commandHintStyle.Render(trimForWidth(line, m.contentWidth-16))
		}
		lines = append(lines, markOverlayFocus(line, index == m.taskActionCursor))
	}
	return strings.Join(lines, "\n")
}

func (m model) renderKnowledgePicker() string {
	if m.knowledgePicker == nil {
		return ""
	}
	lines := []string{
		m.knowledgePicker.Title,
		fallback(strings.TrimSpace(m.knowledgePicker.Hint), "使用方向键选择，Enter 确认，Esc 关闭。"),
		"",
	}
	if len(m.knowledgePicker.Items) == 0 {
		lines = append(lines, hintStyle.Render("当前没有可选知识库。"))
		return strings.Join(lines, "\n")
	}
	for index, item := range m.knowledgePicker.Items {
		line := fmt.Sprintf("%s  %s", flowCursor(index == m.knowledgeCursor), fallback(strings.TrimSpace(item.Name), item.Code))
		meta := []string{}
		if strings.TrimSpace(item.Code) != "" {
			meta = append(meta, item.Code)
		}
		if strings.TrimSpace(item.Type) != "" {
			meta = append(meta, item.Type)
		}
		if item.Selected {
			meta = append(meta, "已选")
		}
		if len(meta) > 0 {
			line += "  " + strings.Join(meta, "  ")
		}
		if strings.TrimSpace(item.Description) != "" {
			line += "\n   " + item.Description
		}
		if index == m.knowledgeCursor {
			line = selectedFlowStyle.Render(trimForWidth(line, m.contentWidth-16))
		} else {
			line = commandHintStyle.Render(trimForWidth(line, m.contentWidth-16))
		}
		lines = append(lines, markOverlayFocus(line, index == m.knowledgeCursor))
	}
	return strings.Join(lines, "\n")
}

func buildTaskActionPicker(item TaskItem) *TaskActionPickerState {
	options := []TaskActionItem{
		{Label: "查看结构化输出", Description: "读取 stdout、stderr、退出码等结构化信息。", Command: "/tasks output " + item.ID},
		{Label: "查看终端输出", Description: "读取合并后的终端视图，适合持续查看日志。", Command: "/tasks capture " + item.ID},
	}
	status := strings.ToLower(strings.TrimSpace(item.Status))
	if status == "running" {
		options = append(options, TaskActionItem{Label: "停止任务", Description: "发送停止请求，并保留当前输出记录。", Command: "/tasks stop " + item.ID})
	} else {
		options = append(options, TaskActionItem{Label: "删除任务", Description: "删除任务及其持久化输出文件。", Command: "/tasks remove " + item.ID})
	}
	return &TaskActionPickerState{
		Title: fmt.Sprintf("任务操作(tasks): %s", fallback(strings.TrimSpace(item.Name), item.ID)),
		Hint:  "Enter 执行所选操作，Esc 返回任务列表。",
		Task:  item,
		Items: options,
	}
}

func buildSkillDetailPanel(item SkillItem) *ToolPanel {
	body := []string{
		fmt.Sprintf("名称: %s", item.Name),
		fmt.Sprintf("来源: %s", fallback(strings.TrimSpace(item.SourceLabel), "<未填写>")),
		fmt.Sprintf("状态: %s", fallback(strings.TrimSpace(item.StateLabel), "<未填写>")),
	}
	if strings.TrimSpace(item.Category) != "" {
		body = append(body, fmt.Sprintf("分类: %s", item.Category))
	}
	if strings.TrimSpace(item.RootDir) != "" {
		body = append(body, fmt.Sprintf("根目录: %s", item.RootDir))
	}
	if strings.TrimSpace(item.RelativeDir) != "" {
		body = append(body, fmt.Sprintf("相对目录: %s", item.RelativeDir))
	}
	if strings.TrimSpace(item.Restriction) != "" {
		body = append(body, fmt.Sprintf("能力标记: %s", item.Restriction))
	}
	if len(item.ReferenceFiles) > 0 {
		body = append(body, "", "参考文件:")
		for _, file := range item.ReferenceFiles {
			line := "- " + fallback(strings.TrimSpace(file.Path), "<未填写>")
			if strings.TrimSpace(file.Description) != "" {
				line += " - " + strings.TrimSpace(file.Description)
			}
			body = append(body, line)
		}
	}
	return &ToolPanel{
		Title:   fmt.Sprintf("技能详情｜%s", item.Name),
		Summary: fallback(strings.TrimSpace(item.Description), "当前技能没有额外说明。"),
		Body:    strings.Join(body, "\n"),
		Hint:    "Esc 关闭详情面板。",
	}
}

func trimMultilineForWidth(content string, width int) string {
	if width <= 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lines[index] = trimForWidth(line, width)
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
		lines = append(lines, markOverlayFocus(line, position == m.flowCursor))
		if description := strings.TrimSpace(item.Description); description != "" && position == m.flowCursor {
			lines = append(lines, hintStyle.Render(trimText(description, 100)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m model) statusBarSegments(maxWidth int) []string {
	compact := m.useMinimalChrome() || maxWidth < 56
	segments := []statusSegment{}
	if !m.snapshot.BackendAvailable {
		segments = append(segments, statusSegment{text: backendStatusLabel(m.snapshot), priority: 0})
	}
	if authSegment := authStatusLabel(m.snapshot, compact); authSegment != "" {
		segments = append(segments, statusSegment{text: authSegment, priority: 0})
	}
	segments = append(segments, statusSegment{text: requestModeStatus(m.snapshot.RequestMode, compact), priority: 1})
	segments = append(segments, statusSegment{text: approvalModeStatus(m.snapshot.ApprovalMode, compact), priority: 1})
	segments = append(segments, statusSegment{text: reasoningStatus(m.snapshot.ReasoningEnabled, compact), priority: 1})
	if capabilitySegment := chatCapabilityStatus(m.snapshot, compact); capabilitySegment != "" {
		segments = append(segments, statusSegment{text: capabilitySegment, priority: 2})
	}
	if userSegment := currentUserStatus(m.snapshot.CurrentUserName, compact); userSegment != "" {
		segments = append(segments, statusSegment{text: userSegment, priority: 4})
	}
	if sandboxSegment := sessionSandboxEscapePolicyStatus(m.snapshot.SessionSandboxEscapePolicy, compact); sandboxSegment != "" {
		segments = append(segments, statusSegment{text: sandboxSegment, priority: 1})
	}
	if toolSegment := m.toolSummarySegment(compact); toolSegment != "" {
		priority := 2
		if m.isBusy() {
			priority = 0
		}
		segments = append(segments, statusSegment{text: toolSegment, priority: priority})
	}
	if requestSegment := m.requestStatsSegment(compact); requestSegment != "" {
		segments = append(segments, statusSegment{text: requestSegment, priority: 5})
	}
	return fitStatusSegments(segments, maxWidth)
}

func chatCapabilityStatus(snapshot Snapshot, compact bool) string {
	parts := []string{}
	if snapshot.InternetSearchSupported {
		label := "联网搜索关闭"
		if snapshot.InternetSearchEnabled {
			label = "联网搜索开启"
		}
		parts = append(parts, label)
	}
	if snapshot.KnowledgeSearchSupported {
		if count := len(snapshot.SelectedKnowledgeBases); count > 0 {
			parts = append(parts, fmt.Sprintf("知识库%d", count))
		} else if len(snapshot.KnowledgeBases) > 0 {
			parts = append(parts, "自动选择知识库")
		} else {
			parts = append(parts, "无知识库")
		}
	} else if snapshot.KnowledgeSystemEnabled {
		parts = append(parts, "知识库可用")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " / ")
}

type statusSegment struct {
	text     string
	priority int
}

func fitStatusSegments(segments []statusSegment, maxWidth int) []string {
	if len(segments) == 0 {
		return nil
	}
	current := append([]statusSegment{}, segments...)
	for len(current) > 1 && lipgloss.Width(joinStatusSegments(statusSegmentTexts(current), "  ·  ")) > maxWidth {
		removeIndex := -1
		removePriority := -1
		for index, segment := range current {
			if segment.priority >= removePriority {
				removePriority = segment.priority
				removeIndex = index
			}
		}
		if removeIndex < 0 {
			break
		}
		current = append(current[:removeIndex], current[removeIndex+1:]...)
	}
	return statusSegmentTexts(current)
}

func statusSegmentTexts(segments []statusSegment) []string {
	result := make([]string, 0, len(segments))
	for _, segment := range segments {
		if strings.TrimSpace(segment.text) != "" {
			result = append(result, segment.text)
		}
	}
	return result
}

func buildBackendStatusEntries(snapshot Snapshot) []entry {
	if snapshot.BackendAvailable && snapshot.AuthRequired {
		reason := strings.TrimSpace(snapshot.AuthStatusReason)
		if reason == "" {
			reason = "服务端已连接，但当前尚未登录。登录表单已自动打开，也可手动执行 /login。"
		}
		message := reason
		if !strings.Contains(reason, "/login") {
			message += "\n可用命令: /login"
		}
		return []entry{{role: "error", content: message}}
	}
	if snapshot.BackendAvailable {
		return nil
	}
	message := "当前无法连接服务端，已进入只读模式。"
	message += "\n可用命令: /config /tasks /skills /reasoning /help /clear /exit"
	return []entry{{role: "error", content: message}}
}

func backendStatusLabel(snapshot Snapshot) string {
	if snapshot.BackendAvailable {
		return "服务端已连接"
	}
	reason := strings.TrimSpace(snapshot.BackendStatusReason)
	if reason == "" {
		return "服务端未连接"
	}
	return "服务端未连接: " + reason
}

func currentUserStatus(name string, compact bool) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	return trimmed
}

func authStatusLabel(snapshot Snapshot, compact bool) string {
	if !snapshot.AuthRequired || strings.TrimSpace(snapshot.CurrentUserName) != "" {
		return ""
	}
	if compact {
		return "未登录"
	}
	return "当前未登录"
}

func (m model) backendUnavailableMessage() string {
	message := "当前无法连接服务端，已进入只读模式。"
	message += " 可用命令: /config /tasks /skills /reasoning /help /clear /exit"
	return message
}

func welcomeCommandsLabel(snapshot Snapshot) []string {
	labelValue := "可用命令: "
	if snapshot.BackendAvailable {
		return []string{labelValue + "/agents /internet /knowledge /files /login /config /tasks /skills /reasoning /session /new /exit"}
	}
	return []string{labelValue + "/login /config /tasks /skills /reasoning /help /clear /exit"}
}

func isAvailableInReadOnlyMode(input string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(input))
	switch {
	case trimmed == "/login", strings.HasPrefix(trimmed, "/login "):
		return true
	case trimmed == "/help", trimmed == "/clear", trimmed == "/exit":
		return true
	case trimmed == "/skills", strings.HasPrefix(trimmed, "/skills "):
		return true
	case trimmed == "/review", strings.HasPrefix(trimmed, "/review "):
		return true
	case trimmed == "/tasks", strings.HasPrefix(trimmed, "/tasks "):
		return true
	case trimmed == "/config", strings.HasPrefix(trimmed, "/config "):
		return true
	case trimmed == "/reasoning", strings.HasPrefix(trimmed, "/reasoning "):
		return true
	case trimmed == "/session", strings.HasPrefix(trimmed, "/session "):
		return true
	case trimmed == "/new", strings.HasPrefix(trimmed, "/new "):
		return true
	default:
		return false
	}
}

func renderEntries(entries []entry, width int, height int, renderer *glamour.TermRenderer, snapshot Snapshot) string {
	if len(entries) == 0 {
		return ""
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
		lines := buildWelcomeRenderLines(snapshot, content)
		return systemMessageStyle.Render(strings.Join(lines, "\n"))
	case "user":
		return userMessageStyle.Render(wrapped)
	case "assistant":
		rendered := renderMarkdown(hardWrapText(content, textWidth), renderer)
		return assistantMessageStyle.Render(rendered)
	case "error":
		return errorMessageStyle.Render(wrapped)
	default:
		return systemMessageStyle.Render(wrapped)
	}
}

func buildWelcomeRenderLines(snapshot Snapshot, content string) []string {
	trimmedContent := strings.TrimSpace(content)
	lines := []string{}
	if subHead := strings.TrimSpace(snapshot.SubHead); subHead != "" {
		lines = append(lines, subHead)
		if strings.HasPrefix(trimmedContent, subHead) {
			trimmedContent = strings.TrimSpace(strings.TrimPrefix(trimmedContent, subHead))
		}
	}
	lines = append(lines, welcomeCommandsLabel(snapshot)...)
	if trimmedContent != "" {
		lines = append(lines, trimmedContent)
	}
	return lines
}

func sanitizeTerminalText(content string) string {
	if content == "" {
		return ""
	}
	cleaned := ansiTmuxDCSRegexp.ReplaceAllString(content, "")
	cleaned = ansiAPCRegexp.ReplaceAllString(cleaned, "")
	cleaned = ansiOSCRegexp.ReplaceAllString(cleaned, "")
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
				return
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
	m.resetOverlayScroll()
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
	case strings.EqualFold(trimmed, "/retry"):
		m.input.SetValue("")
		return true, m.retryLastFailedRequest()
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

func (m *model) retryLastFailedRequest() tea.Cmd {
	if m.isBusy() {
		m.entries = append(m.entries, entry{role: "error", content: "当前请求仍在执行，无法重试。"})
		m.refreshLayoutAfterStateChange()
		return nil
	}
	if !m.snapshot.BackendAvailable {
		m.entries = append(m.entries, entry{role: "error", content: m.backendUnavailableMessage()})
		m.refreshLayoutAfterStateChange()
		return nil
	}
	if !m.retryAvailable || m.lastRetryableRequest == nil {
		m.entries = append(m.entries, entry{role: "error", content: "当前没有可重试的失败请求。"})
		m.refreshLayoutAfterStateChange()
		return nil
	}
	request := *cloneRequest(*m.lastRetryableRequest)
	request.RunID = newRequestRunID()
	return m.startRequest(request, true)
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
	return true, m.applyLocalCommandResult(result)
}

func (m *model) applyLocalCommandResult(result LocalCommandResult) tea.Cmd {
	if result.ClearHistory {
		m.entries = buildWelcomeEntries(m.snapshot)
		m.resetAssistantChunkState()
		m.activityHint = ""
		m.requestStats = cloneRequestMetrics(m.snapshot.RequestStats)
		if result.Snapshot != nil {
			result.Snapshot.History = nil
		}
	}
	if result.ClearHistory && strings.TrimSpace(result.FooterHint) == "" {
		m.footerHint = ""
	}
	if footerHint := strings.TrimSpace(result.FooterHint); footerHint != "" {
		m.footerHint = footerHint
	}
	if result.LoginForm != nil {
		m.resetOverlayScroll()
		m.loginForm = newLoginFormState(*result.LoginForm)
		m.toolPanel = nil
		m.configForm = nil
		m.approvalPicker = nil
		m.sessionPicker = nil
		m.skillsPicker = nil
		m.tasksPicker = nil
		m.taskActionPicker = nil
		m.showFlowPicker = false
		m.flowFilter.Blur()
		m.input.Blur()
		m.syncLayout()
	}
	if result.ConfigForm != nil {
		m.resetOverlayScroll()
		m.configForm = newConfigFormState(*result.ConfigForm)
		m.approvalPicker = nil
		m.sessionPicker = nil
		m.skillsPicker = nil
		m.tasksPicker = nil
		m.taskActionPicker = nil
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
			m.skillsPicker = nil
			m.tasksPicker = nil
			m.taskActionPicker = nil
			m.approvalPicker = nil
			m.flowFilter.Blur()
		}
		if result.RebuildEntries {
			m.entries = buildInitialEntries(m.snapshot)
			m.resetAssistantChunkState()
			m.activityHint = ""
			m.requestStats = cloneRequestMetrics(m.snapshot.RequestStats)
		}
	}
	if result.SessionPicker != nil {
		m.resetOverlayScroll()
		m.sessionPicker = result.SessionPicker
		m.sessionCursor = 0
		m.knowledgePicker = nil
		m.skillsPicker = nil
		m.tasksPicker = nil
		m.taskActionPicker = nil
		m.approvalPicker = nil
		m.configForm = nil
		m.input.Blur()
	}
	if result.ApprovalPicker != nil {
		m.resetOverlayScroll()
		m.approvalPicker = result.ApprovalPicker
		m.approvalCursor = 0
		m.sessionPicker = nil
		m.knowledgePicker = nil
		m.skillsPicker = nil
		m.tasksPicker = nil
		m.taskActionPicker = nil
		m.configForm = nil
		m.input.Blur()
	}
	if result.KnowledgePicker != nil {
		m.resetOverlayScroll()
		m.knowledgePicker = result.KnowledgePicker
		m.knowledgeCursor = 0
		m.sessionPicker = nil
		m.skillsPicker = nil
		m.tasksPicker = nil
		m.taskActionPicker = nil
		m.approvalPicker = nil
		m.configForm = nil
		m.input.Blur()
	}
	if result.SkillsPicker != nil {
		m.resetOverlayScroll()
		m.skillsPicker = result.SkillsPicker
		m.skillsCursor = 0
		m.sessionPicker = nil
		m.knowledgePicker = nil
		m.tasksPicker = nil
		m.taskActionPicker = nil
		m.approvalPicker = nil
		m.configForm = nil
		m.input.Blur()
	}
	if result.TasksPicker != nil {
		m.resetOverlayScroll()
		m.tasksPicker = result.TasksPicker
		m.tasksCursor = 0
		m.taskActionPicker = nil
		m.sessionPicker = nil
		m.knowledgePicker = nil
		m.skillsPicker = nil
		m.approvalPicker = nil
		m.configForm = nil
		m.input.Blur()
	}
	for _, event := range result.Events {
		m.applyEvent(event)
	}
	if queued := strings.TrimSpace(result.QueuedInput); queued != "" {
		m.queuedInput = queued
		if !m.isBusy() {
			m.refreshLayoutAfterStateChange()
			return m.startQueuedRequest()
		}
	}
	m.refreshLayoutAfterStateChange()
	if m.mustCompleteLogin() && m.loginForm == nil && m.localCommand != nil {
		return executeLocalCommandCmd(m.ctx, m.snapshot, "/login", m.localCommand)
	}
	return nil
}

func (m model) showSlashCommandHints() bool {
	if m.pendingConfirm != nil || m.toolPanel != nil || m.loginForm != nil || m.configForm != nil || m.showFlowPicker || m.sessionPicker != nil || m.knowledgePicker != nil || m.skillsPicker != nil || m.tasksPicker != nil || m.taskActionPicker != nil || m.approvalPicker != nil || m.isBusy() {
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
		if command.Trigger == "/retry" && (!m.retryAvailable || m.lastRetryableRequest == nil) {
			continue
		}
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

func resolveInitialStartupCommand(snapshot Snapshot, startupCommand string) string {
	trimmed := strings.TrimSpace(startupCommand)
	if trimmed != "" {
		return trimmed
	}
	if snapshot.BackendAvailable && snapshot.AuthRequired {
		return "/login"
	}
	return ""
}

func shouldAutoOpenLoginOverlay(snapshot Snapshot, previouslyAvailable bool, previouslyAuthRequired bool, hasLocalCommand bool, loginFormOpen bool) bool {
	if !hasLocalCommand || loginFormOpen {
		return false
	}
	if !snapshot.BackendAvailable || !snapshot.AuthRequired {
		return false
	}
	if !previouslyAvailable {
		return true
	}
	return !previouslyAuthRequired
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

func (m model) buildRequest(input string, runID string) Request {
	knowledgeScopes := append([]string(nil), m.snapshot.SelectedKnowledgeBases...)
	if m.snapshot.KnowledgeSearchSupported && len(m.snapshot.KnowledgeBases) > 0 && len(knowledgeScopes) == 0 {
		knowledgeScopes = []string{"all"}
	}
	request := Request{
		Input:            input,
		InputFiles:       append([]string(nil), m.snapshot.InputFiles...),
		FlowID:           m.snapshot.DefaultFlow,
		InternetSearch:   m.snapshot.InternetSearchEnabled,
		KnowledgeScopes:  knowledgeScopes,
		RunID:            runID,
		RequestMode:      requestModeKey(m.snapshot.RequestMode),
		ReasoningEnabled: m.snapshot.ReasoningEnabled,
	}
	return request
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

func submitLoginFormCmd(ctx context.Context, snapshot Snapshot, submission LoginFormSubmission, handler LoginFormSubmitHandler) tea.Cmd {
	if handler == nil {
		return nil
	}
	return func() tea.Msg {
		result, err := handler(ctx, snapshot, submission)
		return loginFormSavedMsg{result: result, err: err}
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

func (m *model) closeKnowledgePicker() {
	m.knowledgePicker = nil
	m.knowledgeCursor = 0
	m.input.Focus()
}

func (m *model) closeSkillsPicker() {
	m.skillsPicker = nil
	m.skillsCursor = 0
	m.input.Focus()
}

func (m *model) switchSkillsPickerTab(tab string) {
	if m.skillsPicker == nil {
		return
	}
	if tab != "remote" {
		tab = "local"
	}
	m.skillsPicker.ActiveTab = tab
	m.skillsPicker.SearchMode = false
	m.skillsCursor = 0
}

func (m *model) activeSkillsPickerItemCount() int {
	if m.skillsPicker == nil {
		return 0
	}
	if m.skillsPicker.ActiveTab == "remote" {
		return len(m.skillsPicker.RemoteItems)
	}
	return len(m.skillsPicker.Items)
}

func (m *model) selectedRemoteSkill() (RemoteSkillItem, bool) {
	if m.skillsPicker == nil || m.skillsPicker.ActiveTab != "remote" {
		return RemoteSkillItem{}, false
	}
	if m.skillsCursor < 0 || m.skillsCursor >= len(m.skillsPicker.RemoteItems) {
		return RemoteSkillItem{}, false
	}
	return m.skillsPicker.RemoteItems[m.skillsCursor], true
}

func buildSkillsPickerCommand(action string, slug string, query string) string {
	parts := []string{"/skills-picker", strings.TrimSpace(action)}
	if strings.TrimSpace(slug) != "" {
		parts = append(parts, strings.TrimSpace(slug))
	}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, strings.TrimSpace(query))
	}
	return strings.Join(parts, " ")
}

func buildKnowledgePickerCommand(action string, code string) string {
	parts := []string{"/knowledge-picker", strings.TrimSpace(action)}
	if strings.TrimSpace(code) != "" {
		parts = append(parts, strings.TrimSpace(code))
	}
	return strings.Join(parts, " ")
}

func (m *model) closeTasksPicker() {
	m.tasksPicker = nil
	m.tasksCursor = 0
	m.taskActionPicker = nil
	m.taskActionCursor = 0
	m.input.Focus()
}

func (m *model) closeConfigForm() {
	m.configForm = nil
	m.input.Focus()
	m.syncLayout()
}

func (m *model) closeLoginForm() {
	m.loginForm = nil
	m.input.Focus()
	m.syncLayout()
}

func (m *model) closeApprovalPicker() {
	m.approvalPicker = nil
	m.approvalCursor = 0
	m.input.Focus()
}

func (m *model) closeTaskActionPicker(focusTasks bool) {
	m.taskActionPicker = nil
	m.taskActionCursor = 0
	m.resetOverlayScroll()
	if !focusTasks {
		m.input.Focus()
	}
}

func (m *model) closeToolPanel() {
	m.toolPanel = nil
	m.resetOverlayScroll()
	m.toolPanelViewport.SetContent("")
	m.toolPanelViewport.GotoTop()
	if m.skillsPicker == nil && m.knowledgePicker == nil && m.tasksPicker == nil && m.taskActionPicker == nil && m.sessionPicker == nil && m.approvalPicker == nil && m.loginForm == nil && m.configForm == nil && !m.showFlowPicker {
		m.input.Focus()
	} else {
		m.input.Blur()
	}
	m.syncLayout()
}

func (m *model) openToolPanel(panel *ToolPanel) {
	m.toolPanel = panel
	m.resetOverlayScroll()
	m.input.Blur()
	m.syncToolPanelViewport()
}

func (m *model) moveLoginFormCursor(delta int) {
	if m.loginForm == nil {
		return
	}
	visible := visibleLoginFields(m.loginForm.mode, hasLoginCaptchaTicket(m.loginForm), m.loginForm.supportsQRCode)
	if len(visible) == 0 {
		return
	}
	currentIndex := 0
	for index, field := range visible {
		if field == m.loginForm.cursor {
			currentIndex = index
			break
		}
	}
	if isLoginTextField(m.loginForm.cursor) {
		m.blurLoginFormField(m.loginForm.cursor)
	}
	currentIndex += delta
	if currentIndex < 0 {
		currentIndex = len(visible) - 1
	} else if currentIndex >= len(visible) {
		currentIndex = 0
	}
	m.loginForm.cursor = visible[currentIndex]
	m.focusLoginFormField(m.loginForm.cursor)
	m.syncLayout()
}

func (m *model) setLoginFormMode(mode string) {
	if m.loginForm == nil {
		return
	}
	mode = normalizeLoginFormMode(mode)
	if mode == m.loginForm.mode {
		return
	}
	if isLoginTextField(m.loginForm.cursor) {
		m.blurLoginFormField(m.loginForm.cursor)
	}
	m.loginForm.mode = mode
	if !loginFieldVisible(mode, m.loginForm.cursor, hasLoginCaptchaTicket(m.loginForm), m.loginForm.supportsQRCode) {
		visible := visibleLoginFields(mode, hasLoginCaptchaTicket(m.loginForm), m.loginForm.supportsQRCode)
		if len(visible) > 0 {
			m.loginForm.cursor = visible[0]
		}
	}
	m.focusLoginFormField(m.loginForm.cursor)
	m.syncLayout()
}

func (m model) switchLoginFormMode(mode string) (tea.Model, tea.Cmd) {
	if m.loginForm == nil {
		return m, nil
	}
	if !m.loginForm.supportsQRCode {
		m.setLoginFormMode(loginFormModePassword)
		return m, nil
	}
	previousMode := m.loginForm.mode
	previousTicket := strings.TrimSpace(m.loginForm.qrTicket)
	m.setLoginFormMode(mode)
	if previousMode != loginFormModeQR && m.loginForm.mode == loginFormModeQR && previousTicket == "" && strings.TrimSpace(m.loginForm.qrTicket) == "" {
		return m.submitLoginForm("start_qr")
	}
	return m, nil
}

func (m model) toggleLoginFormMode() (tea.Model, tea.Cmd) {
	if m.loginForm == nil {
		return m, nil
	}
	if m.loginForm.mode == loginFormModeQR {
		return m.switchLoginFormMode(loginFormModePassword)
	}
	return m.switchLoginFormMode(loginFormModeQR)
}

func (m *model) focusLoginFormField(field int) {
	if m.loginForm == nil || !isLoginTextField(field) || m.loginForm.mode != loginFormModePassword {
		return
	}
	inputIndex := loginInputIndex(field)
	if inputIndex < 0 || inputIndex >= len(m.loginForm.inputs) {
		return
	}
	m.loginForm.inputs[inputIndex].Focus()
}

func (m *model) blurLoginFormField(field int) {
	if m.loginForm == nil || !isLoginTextField(field) {
		return
	}
	inputIndex := loginInputIndex(field)
	if inputIndex < 0 || inputIndex >= len(m.loginForm.inputs) {
		return
	}
	m.loginForm.inputs[inputIndex].Blur()
}

func (m *model) syncToolPanelViewport() {
	if m.toolPanel == nil {
		return
	}
	panelWidth := maxInt(24, minInt(m.contentWidth-18, 80))
	panelHeight := maxInt(8, minInt(m.height-14, 24))
	if panelHeight < 8 {
		panelHeight = 8
	}
	m.toolPanelViewport.Width = panelWidth
	m.toolPanelViewport.Height = panelHeight
	body := hardWrapText(m.toolPanel.Body, panelWidth)
	m.toolPanelViewport.SetContent(body)
}

func (m *model) resetOverlayScroll() {
	m.overlayManualScroll = false
	m.overlayScrollLine = 0
}

func (m *model) scrollOverlayBy(delta int) {
	m.overlayManualScroll = true
	m.overlayScrollLine += delta
	if m.overlayScrollLine < 0 {
		m.overlayScrollLine = 0
	}
}

func (m *model) scrollOverlayTo(line int) {
	m.overlayManualScroll = true
	if line < 0 {
		line = 0
	}
	m.overlayScrollLine = line
}

func (m model) overlayPageStep() int {
	step := m.overlayVisibleLineLimit() - 2
	if step < 1 {
		step = 1
	}
	return step
}

func (m model) overlayVisibleLineLimit() int {
	limit := m.height - overlayStyle.GetVerticalFrameSize() - 6
	if m.loginForm != nil {
		limit = m.height - overlayStyle.GetVerticalFrameSize() - 2
	}
	if limit < 5 {
		limit = 5
	}
	return limit
}

func (m model) fitOverlayContent(content string) string {
	content, focusLine := stripOverlayFocusMarker(content)
	lines := strings.Split(content, "\n")
	maxLines := m.overlayVisibleLineLimit()
	if maxLines <= 0 || len(lines) <= maxLines {
		return content
	}
	start := 0
	if m.overlayManualScroll {
		start = m.overlayScrollLine
	} else if focusLine > 0 {
		start = focusLine - maxLines/3
	}
	maxStart := len(lines) - maxLines
	if maxStart < 0 {
		maxStart = 0
	}
	if start < 0 {
		start = 0
	}
	if start > maxStart {
		start = maxStart
	}
	end := start + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	visible := append([]string{}, lines[start:end]...)
	if start > 0 {
		if len(visible) == maxLines {
			visible = visible[1:]
		}
		visible = append([]string{hintStyle.Render(fmt.Sprintf("↑ 上方还有 %d 行", start))}, visible...)
	}
	if end < len(lines) {
		if len(visible) == maxLines {
			visible = visible[:len(visible)-1]
		}
		visible = append(visible, hintStyle.Render(fmt.Sprintf("↓ 下方还有 %d 行", len(lines)-end)))
	}
	return strings.Join(visible, "\n")
}

func markOverlayFocus(line string, active bool) string {
	if !active {
		return line
	}
	return overlayFocusMarker + line
}

func stripOverlayFocusMarker(content string) (string, int) {
	lines := strings.Split(content, "\n")
	focusLine := 0
	found := false
	for index, line := range lines {
		if strings.Contains(line, overlayFocusMarker) {
			if !found {
				focusLine = index
				found = true
			}
			lines[index] = strings.ReplaceAll(line, overlayFocusMarker, "")
		}
	}
	return strings.Join(lines, "\n"), focusLine
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

func (m *model) focusConfigFormField(index int) {
	if m.configForm == nil || index < 0 || index >= len(m.configForm.inputs) {
		return
	}
	if m.configForm.flowOpen {
		m.configForm.flowOpen = false
	}
	current := m.configForm.cursor
	if current >= 0 && current < len(m.configForm.inputs) && isConfigTextField(current) {
		m.configForm.inputs[current].Blur()
	}
	m.configForm.cursor = index
	if isConfigTextField(m.configForm.cursor) {
		m.configForm.inputs[m.configForm.cursor].Focus()
	}
	m.syncLayout()
}

func (m model) submitConfigForm() (tea.Model, tea.Cmd) {
	if m.configForm == nil {
		return m, nil
	}
	m.configForm.statusText = ""
	m.configForm.statusIsError = false
	m.configForm.fieldErrorKey = ""
	m.configForm.fieldErrorText = ""
	submission := ConfigFormSubmission{Values: configFormValuesFromInputs(m.configForm.inputs)}
	if m.configForm.flowCursor >= 0 && m.configForm.flowCursor < len(m.configForm.flowOptions) {
		submission.Values.DefaultFlow = strings.TrimSpace(m.configForm.flowOptions[m.configForm.flowCursor].Value)
	}
	approvalChoices := configApprovalChoices()
	if m.configForm.approvalCursor >= 0 && m.configForm.approvalCursor < len(approvalChoices) {
		submission.Values.ApprovalMode = strings.TrimSpace(approvalChoices[m.configForm.approvalCursor].Value)
	}
	sandboxChoices := configSandboxChoices()
	if m.configForm.sandboxCursor >= 0 && m.configForm.sandboxCursor < len(sandboxChoices) {
		submission.Values.SandboxEscapePolicy = strings.TrimSpace(sandboxChoices[m.configForm.sandboxCursor].Value)
	}
	skillReviewChoices := configSkillReviewChoices()
	if m.configForm.skillReviewCursor >= 0 && m.configForm.skillReviewCursor < len(skillReviewChoices) {
		submission.Values.SkillReviewPolicy = strings.TrimSpace(skillReviewChoices[m.configForm.skillReviewCursor].Value)
	}
	writeConfirmationChoices := configWriteConfirmationChoices()
	if m.configForm.writeConfirmationCursor >= 0 && m.configForm.writeConfirmationCursor < len(writeConfirmationChoices) {
		submission.Values.WriteConfirmationPolicy = strings.TrimSpace(writeConfirmationChoices[m.configForm.writeConfirmationCursor].Value)
	}
	reasoningChoices := configReasoningChoices()
	if m.configForm.reasoningCursor >= 0 && m.configForm.reasoningCursor < len(reasoningChoices) {
		submission.Values.ReasoningEnabled = reasoningChoices[m.configForm.reasoningCursor].Value == "on"
	}
	agentAutoLoopChoices := configAgentAutoLoopChoices()
	if m.configForm.agentAutoLoopCursor >= 0 && m.configForm.agentAutoLoopCursor < len(agentAutoLoopChoices) {
		submission.Values.AgentAutoLoopEnabled = strings.TrimSpace(agentAutoLoopChoices[m.configForm.agentAutoLoopCursor].Value)
	}
	skillsChoices := configSkillsInstallScopeChoices()
	if m.configForm.skillsCursor >= 0 && m.configForm.skillsCursor < len(skillsChoices) {
		submission.Values.SkillsInstallScope = strings.TrimSpace(skillsChoices[m.configForm.skillsCursor].Value)
	}
	skillsAutoInstallChoices := configSkillsAutoInstallChoices()
	if m.configForm.skillsAutoInstallCursor >= 0 && m.configForm.skillsAutoInstallCursor < len(skillsAutoInstallChoices) {
		submission.Values.SkillsAutoInstallPolicy = strings.TrimSpace(skillsAutoInstallChoices[m.configForm.skillsAutoInstallCursor].Value)
	}
	skillsFreeTextChoices := configSkillsFreeTextSearchChoices()
	if m.configForm.skillsFreeTextCursor >= 0 && m.configForm.skillsFreeTextCursor < len(skillsFreeTextChoices) {
		submission.Values.SkillsFreeTextSearch = strings.TrimSpace(skillsFreeTextChoices[m.configForm.skillsFreeTextCursor].Value) == "true"
	}
	return m, tea.Batch(m.transitionProgress(progressExecuting), submitConfigFormCmd(m.ctx, m.snapshot, submission, m.configFormSubmit))
}

func (m model) submitLoginForm(action string) (tea.Model, tea.Cmd) {
	if m.loginForm == nil {
		return m, nil
	}
	action = strings.TrimSpace(action)
	if action == "" {
		switch m.loginForm.mode {
		case loginFormModeQR:
			if strings.TrimSpace(m.loginForm.qrTicket) == "" {
				action = "start_qr"
			} else {
				action = "wait_qr"
			}
		default:
			action = "password_login"
		}
	}
	submission := LoginFormSubmission{
		Action:                 action,
		Mode:                   m.loginForm.mode,
		Username:               strings.TrimSpace(m.loginForm.inputs[0].Value()),
		Password:               m.loginForm.inputs[1].Value(),
		PasswordPublicKey:      strings.TrimSpace(m.loginForm.passwordPublicKey),
		PasswordTicket:         strings.TrimSpace(m.loginForm.passwordTicket),
		Remember:               m.loginForm.remember,
		VerifyType:             strings.TrimSpace(m.loginForm.verifyType),
		VerifyHint:             strings.TrimSpace(m.loginForm.verifyHint),
		VerifyTarget:           strings.TrimSpace(m.loginForm.verifyTarget),
		VerifyImageASCII:       m.loginForm.verifyImageASCII,
		VerifyImagePNGBase64:   m.loginForm.verifyImagePNGBase64,
		VerifyImageWidth:       m.loginForm.verifyImageWidth,
		VerifyImageHeight:      m.loginForm.verifyImageHeight,
		VerifyImageURL:         m.loginForm.verifyImageURL,
		VerifyImagePreviewPath: m.loginForm.verifyImagePreviewPath,
		Ticket:                 strings.TrimSpace(m.loginForm.inputs[2].Value()),
		Code:                   strings.TrimSpace(m.loginForm.inputs[3].Value()),
		QRTicket:               strings.TrimSpace(m.loginForm.qrTicket),
		QRLoginURL:             strings.TrimSpace(m.loginForm.qrLoginURL),
		QRScanHint:             strings.TrimSpace(m.loginForm.qrScanHint),
		QRCodeASCII:            m.loginForm.qrCodeASCII,
		QRWaiting:              m.loginForm.qrWaiting,
		QRWaitPollCount:        m.loginForm.qrWaitPollCount,
	}
	return m, tea.Batch(m.transitionProgress(progressExecuting), submitLoginFormCmd(m.ctx, m.snapshot, submission, m.loginFormSubmit))
}

func newConfigFormState(state ConfigFormState) *configFormState {
	values := state.Values
	models := make([]textinput.Model, 0, len(configFormFieldSpecs()))
	for index, spec := range configFormFieldSpecs() {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = spec.placeholder
		input.CharLimit = 512
		input.SetValue(configFormFieldValue(index, values))
		if index == 0 {
			input.Focus()
		} else {
			input.Blur()
		}
		models = append(models, input)
	}
	flowOptions := normalizeConfigFlowChoices(state.FlowOptions, values.DefaultFlow)
	return &configFormState{
		title:                   state.Title,
		hint:                    state.Hint,
		statusText:              state.StatusText,
		statusIsError:           state.StatusIsError,
		fieldErrorKey:           "",
		fieldErrorText:          "",
		path:                    state.Path,
		submitText:              state.SubmitText,
		inputs:                  models,
		cursor:                  0,
		flowOptions:             flowOptions,
		flowCursor:              configChoiceIndex(flowOptions, values.DefaultFlow),
		approvalCursor:          configChoiceIndex(configApprovalChoices(), values.ApprovalMode),
		sandboxCursor:           configChoiceIndex(configSandboxChoices(), values.SandboxEscapePolicy),
		skillReviewCursor:       configChoiceIndex(configSkillReviewChoices(), values.SkillReviewPolicy),
		writeConfirmationCursor: configChoiceIndex(configWriteConfirmationChoices(), values.WriteConfirmationPolicy),
		reasoningCursor:         configChoiceIndex(configReasoningChoices(), reasoningChoiceValue(values.ReasoningEnabled)),
		agentAutoLoopCursor:     configChoiceIndex(configAgentAutoLoopChoices(), values.AgentAutoLoopEnabled),
		skillsCursor:            configChoiceIndex(configSkillsInstallScopeChoices(), values.SkillsInstallScope),
		skillsAutoInstallCursor: configChoiceIndex(configSkillsAutoInstallChoices(), values.SkillsAutoInstallPolicy),
		skillsFreeTextCursor:    configChoiceIndex(configSkillsFreeTextSearchChoices(), boolChoiceValue(values.SkillsFreeTextSearch)),
	}
}

func newLoginFormState(state LoginFormState) *loginFormState {
	codePlaceholder := loginprompt.CodePlaceholder(state.VerifyType)
	placeholders := []string{"请输入用户名", "请输入密码", "无验证码可留空", codePlaceholder}
	values := []string{state.Username, state.Password, state.Ticket, state.Code}
	models := make([]textinput.Model, 0, len(placeholders))
	for index, placeholder := range placeholders {
		input := textinput.New()
		input.Prompt = ""
		input.Placeholder = placeholder
		input.CharLimit = 512
		input.SetValue(values[index])
		input.Blur()
		if index == 1 {
			input.EchoMode = textinput.EchoPassword
			input.EchoCharacter = '•'
		}
		models = append(models, input)
	}
	mode := normalizeLoginFormMode(state.Mode)
	if !state.SupportsQRCode {
		mode = loginFormModePassword
	}
	loginState := &loginFormState{
		title:                  state.Title,
		hint:                   state.Hint,
		statusText:             state.StatusText,
		statusIsError:          state.StatusIsError,
		supportsQRCode:         state.SupportsQRCode,
		mode:                   mode,
		remember:               state.Remember,
		passwordPublicKey:      strings.TrimSpace(state.PasswordPublicKey),
		passwordTicket:         strings.TrimSpace(state.PasswordTicket),
		verifyType:             strings.TrimSpace(state.VerifyType),
		verifyHint:             strings.TrimSpace(state.VerifyHint),
		verifyTarget:           strings.TrimSpace(state.VerifyTarget),
		verifyImageASCII:       state.VerifyImageASCII,
		verifyImagePNGBase64:   state.VerifyImagePNGBase64,
		verifyImageWidth:       state.VerifyImageWidth,
		verifyImageHeight:      state.VerifyImageHeight,
		verifyImageURL:         state.VerifyImageURL,
		verifyImagePreviewPath: state.VerifyImagePreviewPath,
		inputs:                 models,
		cursor:                 loginFieldMode,
		qrTicket:               strings.TrimSpace(state.QRTicket),
		qrLoginURL:             strings.TrimSpace(state.QRLoginURL),
		qrScanHint:             strings.TrimSpace(state.QRScanHint),
		qrCodeASCII:            state.QRCodeASCII,
		qrWaiting:              state.QRWaiting,
		qrWaitPollCount:        state.QRWaitPollCount,
	}
	if state.FocusField >= 0 && loginFieldVisible(mode, state.FocusField, hasLoginVerificationChallenge(loginState), state.SupportsQRCode) {
		loginState.cursor = state.FocusField
		loginState.focusLoginFieldCursor()
		return loginState
	}
	if mode == loginFormModePassword {
		if hasLoginVerificationChallenge(loginState) {
			loginState.cursor = loginFieldCaptchaCode
			loginState.focusLoginFieldCursor()
		} else {
			loginState.cursor = loginFieldUsername
			loginState.focusLoginFieldCursor()
		}
	}
	return loginState
}

func (s *loginFormState) focusLoginFieldCursor() {
	if s == nil || !isLoginTextField(s.cursor) {
		return
	}
	inputIndex := loginInputIndex(s.cursor)
	if inputIndex < 0 || inputIndex >= len(s.inputs) {
		return
	}
	s.inputs[inputIndex].Focus()
}

func normalizeLoginFormMode(mode string) string {
	if strings.EqualFold(strings.TrimSpace(mode), loginFormModeQR) {
		return loginFormModeQR
	}
	return loginFormModePassword
}

func loginFieldVisible(mode string, field int, hasVerification bool, supportsQRCode bool) bool {
	if field == loginFieldMode {
		return supportsQRCode
	}
	if field == loginFieldSubmit {
		return true
	}
	if normalizeLoginFormMode(mode) != loginFormModePassword {
		return false
	}
	if hasVerification {
		return field == loginFieldPassword || field == loginFieldCaptchaCode
	}
	return field == loginFieldUsername || field == loginFieldPassword
}

func visibleLoginFields(mode string, hasVerification bool, supportsQRCode bool) []int {
	fields := make([]int, 0, loginFieldCount)
	for field := 0; field < loginFieldCount; field++ {
		if loginFieldVisible(mode, field, hasVerification, supportsQRCode) {
			fields = append(fields, field)
		}
	}
	return fields
}

func hasLoginVerificationChallenge(state *loginFormState) bool {
	if state == nil {
		return false
	}
	if strings.TrimSpace(state.verifyType) != "" {
		return true
	}
	return hasLoginCaptchaTicket(state)
}

func hasLoginCaptchaTicket(state *loginFormState) bool {
	if state == nil || len(state.inputs) <= 2 {
		return false
	}
	return strings.TrimSpace(state.inputs[2].Value()) != ""
}

func loginVerificationTypeLabel(verifyType string) string {
	return loginprompt.TypeLabel(verifyType)
}

func loginVerificationCodeLabel(verifyType string) string {
	return loginprompt.CodeLabel(verifyType)
}

func loginVerificationDescription(verifyType string, verifyHint string) string {
	return loginprompt.Description(verifyType, verifyHint)
}

func renderTerminalVerificationImage(state *loginFormState, maxWidth int) string {
	if state == nil || strings.TrimSpace(state.verifyImagePNGBase64) == "" {
		return ""
	}
	const scaleFactor = 2
	renderedImage := upscaleVerificationImagePNG(state.verifyImagePNGBase64, scaleFactor)
	widthCells, heightCells := loginVerificationImageCellSize(state.verifyImageWidth*scaleFactor, state.verifyImageHeight*scaleFactor, maxWidth)
	switch detectTerminalImageProtocol() {
	case "kitty":
		return renderKittyInlineImage(renderedImage, widthCells, heightCells)
	case "iterm2":
		return renderITerm2InlineImage(renderedImage, widthCells, heightCells)
	default:
		return ""
	}
}

func detectTerminalImageProtocol() string {
	if override, ok := terminalImageProtocolOverride(); ok {
		return override
	}
	if strings.TrimSpace(os.Getenv("KITTY_WINDOW_ID")) != "" || strings.Contains(strings.ToLower(strings.TrimSpace(os.Getenv("TERM"))), "kitty") {
		return "kitty"
	}
	termProgram := strings.ToLower(strings.TrimSpace(os.Getenv("TERM_PROGRAM")))
	lcTerminal := strings.ToLower(strings.TrimSpace(os.Getenv("LC_TERMINAL")))
	if termProgram == "iterm.app" || lcTerminal == "iterm2" {
		return "iterm2"
	}
	return ""
}

func terminalImageProtocolOverride() (string, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL"))) {
	case "kitty":
		return "kitty", true
	case "iterm2", "iterm":
		return "iterm2", true
	case "off", "none", "disable", "disabled", "false", "0":
		return "", true
	default:
		return "", false
	}
}

func runningInsideTmux() bool {
	return strings.TrimSpace(os.Getenv("TMUX")) != ""
}

func loginVerificationImageCellSize(imageWidth int, imageHeight int, maxWidth int) (int, int) {
	maxCells := 96
	if maxWidth > 0 {
		maxCells = minInt(128, maxInt(32, maxWidth))
	}
	widthCells := maxCells
	if imageWidth > 0 {
		widthCells = maxInt(32, minInt(maxCells, int(math.Round(float64(imageWidth)*0.85))))
	}
	heightCells := 10
	if imageWidth > 0 && imageHeight > 0 {
		ratio := float64(imageHeight) / float64(imageWidth)
		heightCells = int(math.Round(float64(widthCells) * ratio))
	}
	heightCells = maxInt(10, minInt(48, heightCells))
	return widthCells, heightCells
}

func upscaleVerificationImagePNG(base64Data string, scaleFactor int) string {
	trimmed := strings.TrimSpace(base64Data)
	if trimmed == "" || scaleFactor <= 1 {
		return trimmed
	}
	rawData, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return trimmed
	}
	sourceImage, _, err := image.Decode(bytes.NewReader(rawData))
	if err != nil {
		return trimmed
	}
	bounds := sourceImage.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return trimmed
	}
	scaledImage := image.NewNRGBA(image.Rect(0, 0, bounds.Dx()*scaleFactor, bounds.Dy()*scaleFactor))
	for y := 0; y < scaledImage.Bounds().Dy(); y++ {
		sourceY := bounds.Min.Y + y/scaleFactor
		for x := 0; x < scaledImage.Bounds().Dx(); x++ {
			sourceX := bounds.Min.X + x/scaleFactor
			scaledImage.Set(x, y, sourceImage.At(sourceX, sourceY))
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, scaledImage); err != nil {
		return trimmed
	}
	return base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func upscaleVerificationASCIIArt(content string, scaleFactor int) string {
	if strings.TrimSpace(content) == "" || scaleFactor <= 1 {
		return strings.Trim(content, "\n")
	}
	lines := strings.Split(strings.Trim(content, "\n"), "\n")
	expandedLines := make([]string, 0, len(lines)*scaleFactor)
	for _, line := range lines {
		var widened strings.Builder
		for _, char := range line {
			for repeat := 0; repeat < scaleFactor; repeat++ {
				widened.WriteRune(char)
			}
		}
		expanded := widened.String()
		for repeat := 0; repeat < scaleFactor; repeat++ {
			expandedLines = append(expandedLines, expanded)
		}
	}
	return strings.Join(expandedLines, "\n")
}

func canRenderANSIColorVerificationImage(base64Data string) bool {
	_, ok := decodeVerificationImageBase64(base64Data)
	return ok
}

func renderANSIColorVerificationImage(base64Data string, maxWidth int) string {
	decodedImage, ok := decodeVerificationImageBase64(base64Data)
	if !ok {
		return ""
	}
	return renderANSIColorVerificationImageFromDecoded(decodedImage, maxWidth)
}

func decodeVerificationImageBase64(base64Data string) (image.Image, bool) {
	trimmed := strings.TrimSpace(base64Data)
	if trimmed == "" {
		return nil, false
	}
	rawData, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return nil, false
	}
	decodedImage, _, err := image.Decode(bytes.NewReader(rawData))
	if err != nil {
		return nil, false
	}
	bounds := decodedImage.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, false
	}
	return decodedImage, true
}

func renderANSIColorVerificationImageFromDecoded(decodedImage image.Image, maxWidth int) string {
	bounds := decodedImage.Bounds()
	targetWidth := bounds.Dx()
	if maxWidth > 0 {
		targetWidth = minInt(bounds.Dx(), maxInt(36, minInt(96, maxWidth)))
	}
	if targetWidth <= 0 {
		return ""
	}
	targetHeightPixels := int(math.Round(float64(bounds.Dy()) * float64(targetWidth) / float64(bounds.Dx())))
	targetHeightPixels = maxInt(12, minInt(48, targetHeightPixels))
	if targetHeightPixels%2 != 0 {
		targetHeightPixels++
	}
	var output strings.Builder
	for y := 0; y < targetHeightPixels; y += 2 {
		for x := 0; x < targetWidth; x++ {
			top := sampleVerificationImageColor(decodedImage, bounds, x, y, targetWidth, targetHeightPixels)
			bottom := sampleVerificationImageColor(decodedImage, bounds, x, y+1, targetWidth, targetHeightPixels)
			output.WriteString(fmt.Sprintf("\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", top.red, top.green, top.blue, bottom.red, bottom.green, bottom.blue))
		}
		output.WriteString("\x1b[0m")
		if y+2 < targetHeightPixels {
			output.WriteByte('\n')
		}
	}
	return output.String()
}

type verificationImageColor struct {
	red   uint8
	green uint8
	blue  uint8
}

func sampleVerificationImageColor(decodedImage image.Image, bounds image.Rectangle, x int, y int, targetWidth int, targetHeight int) verificationImageColor {
	if targetWidth <= 0 || targetHeight <= 0 {
		return verificationImageColor{red: 255, green: 255, blue: 255}
	}
	sourceX := bounds.Min.X + minInt(bounds.Dx()-1, maxInt(0, x*bounds.Dx()/targetWidth))
	sourceY := bounds.Min.Y + minInt(bounds.Dy()-1, maxInt(0, y*bounds.Dy()/targetHeight))
	red, green, blue, alpha := decodedImage.At(sourceX, sourceY).RGBA()
	if alpha == 0 {
		return verificationImageColor{red: 255, green: 255, blue: 255}
	}
	return verificationImageColor{red: uint8(red >> 8), green: uint8(green >> 8), blue: uint8(blue >> 8)}
}

func loginVerificationASCIIScale(content string, contentWidth int) int {
	baseWidth := verificationASCIIMaxLineWidth(content)
	if baseWidth <= 0 {
		return 2
	}
	targetWidth := maxInt(64, minInt(112, contentWidth))
	if targetWidth < 96 {
		targetWidth = 96
	}
	scaleFactor := int(math.Ceil(float64(targetWidth) / float64(baseWidth)))
	return maxInt(2, minInt(4, scaleFactor))
}

func verificationASCIIMaxLineWidth(content string) int {
	maxWidth := 0
	for _, line := range strings.Split(strings.Trim(content, "\n"), "\n") {
		lineWidth := lipgloss.Width(line)
		if lineWidth > maxWidth {
			maxWidth = lineWidth
		}
	}
	return maxWidth
}

func renderITerm2InlineImage(base64Data string, widthCells int, heightCells int) string {
	if strings.TrimSpace(base64Data) == "" {
		return ""
	}
	sequence := fmt.Sprintf("\x1b]1337;File=inline=1;width=%d;height=%d;preserveAspectRatio=1:%s\a", widthCells, heightCells, base64Data)
	return wrapTmuxPassthrough(sequence)
}

func renderKittyInlineImage(base64Data string, widthCells int, heightCells int) string {
	trimmed := strings.TrimSpace(base64Data)
	if trimmed == "" {
		return ""
	}
	const kittyChunkSize = 4096
	var builder strings.Builder
	remaining := trimmed
	firstChunk := true
	for len(remaining) > 0 {
		chunk := remaining
		continuation := 0
		if len(chunk) > kittyChunkSize {
			chunk = remaining[:kittyChunkSize]
			continuation = 1
		}
		if firstChunk {
			builder.WriteString(fmt.Sprintf("\x1b_Gf=100,a=T,t=d,c=%d,r=%d,m=%d;%s\x1b\\", widthCells, heightCells, continuation, chunk))
			firstChunk = false
		} else {
			builder.WriteString(fmt.Sprintf("\x1b_Gm=%d;%s\x1b\\", continuation, chunk))
		}
		remaining = remaining[len(chunk):]
	}
	return wrapTmuxPassthrough(builder.String())
}

func wrapTmuxPassthrough(sequence string) string {
	if !runningInsideTmux() || strings.TrimSpace(sequence) == "" {
		return sequence
	}
	escaped := strings.ReplaceAll(sequence, "\x1b", "\x1b\x1b")
	return "\x1bPtmux;" + escaped + "\x1b\\"
}

func (m model) mustCompleteLogin() bool {
	return m.snapshot.BackendAvailable && m.snapshot.AuthRequired && strings.TrimSpace(m.snapshot.CurrentUserName) == ""
}

func isLoginTextField(field int) bool {
	switch field {
	case loginFieldUsername, loginFieldPassword, loginFieldCaptchaTicket, loginFieldCaptchaCode:
		return true
	default:
		return false
	}
}

func loginInputIndex(field int) int {
	switch field {
	case loginFieldUsername:
		return 0
	case loginFieldPassword:
		return 1
	case loginFieldCaptchaTicket:
		return 2
	case loginFieldCaptchaCode:
		return 3
	default:
		return -1
	}
}

func configFormFieldSpecs() []configFieldSpec {
	return []configFieldSpec{
		{label: "服务地址", placeholder: "https://snz1.cn", description: "必须是完整的 HTTP(http) 或 HTTPS(https) 地址。"},
		{label: "默认智能体", placeholder: "", description: "按回车键(Enter)打开下拉列表，留空项表示自动选择(auto)。"},
		{label: "命令执行模式", placeholder: "", description: "左右键或空格切换单选项。"},
		{label: "外部资源访问策略", placeholder: "", description: "左右键或空格切换每次确认(confirm)、禁止访问(deny)、直接允许(allow)。"},
		{label: "技能包审查策略", placeholder: "", description: "左右键或空格切换逐次审查(confirm)、忽略审查(ignore)。选择忽略审查(ignore)后不再弹出技能审查窗口。"},
		{label: "文件修改审查窗口", placeholder: "", description: "左右键或空格切换显示审查窗口(confirm)、直接写入(direct)。选择直接写入(direct)后 write_file、file_edit 不再弹出审查窗口。"},
		{label: "可访问外部资源目录", placeholder: "/path/a, /path/b", description: "多个目录用逗号分隔；留空表示仅允许当前工作区。"},
		{label: "细粒度权限规则", placeholder: "tool:shell_exec=deny; shell:git status=allow", description: "使用工具规则(tool:name=动作)或命令前缀规则(shell:prefix=动作)，多个规则用分号分隔。"},
		{label: "默认思考模式", placeholder: "", description: "左右键或空格切换是否默认启用思考模式。"},
		{label: "工作区目录", placeholder: "留空表示当前目录", description: "默认使用当前工作目录。"},
		{label: "HTTP 请求超时（秒）", placeholder: "60", description: "标准 HTTP 请求在未显式覆写时使用该默认值，范围 1-300 秒；不影响流式长连接。"},
		{label: "命令执行超时（秒）", placeholder: "30", description: "本地命令与语言服务查询在未显式传超时(timeout)时使用该默认值，范围 1-300 秒。"},
		{label: "流式空闲超时（秒）", placeholder: "300", description: "大于等于 0 的整数秒，0 表示禁用流式空闲超时。"},
		{label: "流失败自动重试次数", placeholder: "0", description: "流连接异常后的自动重试次数，范围 0-3；0 表示不自动重试。"},
		{label: "扫码等待超时（秒）", placeholder: "30", description: "二维码登录在自动轮询时使用该默认值；大于等于 0 的整数秒，0 表示持续等待直到手动取消。"},
		{label: "智能体自动续跑默认开关", placeholder: "", description: "仅对智能体模式生效；左右键或空格切换恢复默认(default)、开启(true)、关闭(false)。"},
		{label: "智能体自动续跑最大轮数", placeholder: "3", description: "仅对智能体模式生效；填写大于等于 1 的整数。"},
		{label: "输出语言", placeholder: "zh-CN", description: "留空会恢复默认输出语言。"},
		{label: "技能安装位置", placeholder: "", description: "左右键或空格切换用户目录(user)或工作区(workspace)。"},
		{label: "技能自动补装策略", placeholder: "", description: "左右键或空格切换关闭(off)、确认(confirm)、自动安装(allow)。默认建议使用确认(confirm)。"},
		{label: "自由文本技能搜索", placeholder: "", description: "左右键或空格切换是否允许按任务文本搜索远程技能候选。"},
		{label: "技能仓库地址", placeholder: "https://mirror-cn.clawhub.com", description: "远程技能搜索、详情和安装默认使用该镜像站。"},
	}
}

func configFormFieldValue(index int, values ConfigFormValues) string {
	switch index {
	case 0:
		return strings.TrimSpace(values.BackendBaseURL)
	case 1:
		return ""
	case 2:
		return ""
	case 3:
		return ""
	case 4:
		return ""
	case 5:
		return ""
	case 6:
		return strings.TrimSpace(values.SandboxRoots)
	case 7:
		return strings.TrimSpace(values.PermissionRules)
	case 8:
		return ""
	case 9:
		return strings.TrimSpace(values.Workdir)
	case 10:
		return strings.TrimSpace(values.HTTPTimeoutSeconds)
	case 11:
		return strings.TrimSpace(values.CommandTimeoutSeconds)
	case 12:
		return strings.TrimSpace(values.StreamIdleTimeoutSeconds)
	case 13:
		return strings.TrimSpace(values.StreamRetryAttempts)
	case 14:
		return strings.TrimSpace(values.QRWaitTimeoutSeconds)
	case 15:
		return ""
	case 16:
		return strings.TrimSpace(values.AgentAutoLoopMaxRounds)
	case 17:
		return strings.TrimSpace(values.OutputLanguage)
	case 18:
		return ""
	case 19:
		return ""
	case 20:
		return ""
	case 21:
		return strings.TrimSpace(values.SkillsRegistryURL)
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
		values.DefaultFlow = strings.TrimSpace(inputs[1].Value())
	}
	if len(inputs) > 6 {
		values.SandboxRoots = strings.TrimSpace(inputs[6].Value())
	}
	if len(inputs) > 7 {
		values.PermissionRules = strings.TrimSpace(inputs[7].Value())
	}
	if len(inputs) > 9 {
		values.Workdir = strings.TrimSpace(inputs[9].Value())
	}
	if len(inputs) > 10 {
		values.HTTPTimeoutSeconds = strings.TrimSpace(inputs[10].Value())
	}
	if len(inputs) > 11 {
		values.CommandTimeoutSeconds = strings.TrimSpace(inputs[11].Value())
	}
	if len(inputs) > 12 {
		values.StreamIdleTimeoutSeconds = strings.TrimSpace(inputs[12].Value())
	}
	if len(inputs) > 13 {
		values.StreamRetryAttempts = strings.TrimSpace(inputs[13].Value())
	}
	if len(inputs) > 14 {
		values.QRWaitTimeoutSeconds = strings.TrimSpace(inputs[14].Value())
	}
	if len(inputs) > 16 {
		values.AgentAutoLoopMaxRounds = strings.TrimSpace(inputs[16].Value())
	}
	if len(inputs) > 17 {
		values.OutputLanguage = strings.TrimSpace(inputs[17].Value())
	}
	if len(inputs) > 21 {
		values.SkillsRegistryURL = strings.TrimSpace(inputs[21].Value())
	}
	return values
}

func isConfigTextField(index int) bool {
	switch index {
	case configFieldBackendBaseURL, configFieldSandboxRoots, configFieldPermissionRules, configFieldWorkdir, configFieldHTTPTimeout, configFieldCommandTimeout, configFieldStreamIdleTimeout, configFieldStreamRetryAttempts, configFieldQRWaitTimeout, configFieldAgentAutoLoopMaxRounds, configFieldOutputLanguage, configFieldSkillsRegistryURL:
		return true
	default:
		return false
	}
}

func configFieldKey(index int) string {
	switch index {
	case configFieldBackendBaseURL:
		return "backend_base_url"
	case configFieldDefaultFlow:
		return "default_flow"
	case configFieldApprovalMode:
		return "approval_mode"
	case configFieldSandboxPolicy:
		return "external_resource_access_policy"
	case configFieldSkillReviewPolicy:
		return "skill_review_policy"
	case configFieldWriteConfirmationPolicy:
		return "write_confirmation_policy"
	case configFieldSandboxRoots:
		return "sandbox_roots"
	case configFieldPermissionRules:
		return "permission_rules"
	case configFieldReasoningMode:
		return "reasoning_enabled"
	case configFieldWorkdir:
		return "workdir"
	case configFieldHTTPTimeout:
		return "http_timeout_seconds"
	case configFieldCommandTimeout:
		return "command_timeout_seconds"
	case configFieldStreamIdleTimeout:
		return "stream_idle_timeout_seconds"
	case configFieldStreamRetryAttempts:
		return "stream_retry_attempts"
	case configFieldQRWaitTimeout:
		return "qr_wait_timeout_seconds"
	case configFieldAgentAutoLoopEnabled:
		return "agent_auto_loop_enabled"
	case configFieldAgentAutoLoopMaxRounds:
		return "agent_auto_loop_max_rounds"
	case configFieldOutputLanguage:
		return "output_language"
	case configFieldSkillsInstallScope:
		return "skills_install_scope"
	case configFieldSkillsAutoInstallPolicy:
		return "skills_auto_install_policy"
	case configFieldSkillsFreeTextSearch:
		return "skills_free_text_search"
	case configFieldSkillsRegistryURL:
		return "skills_registry_url"
	default:
		return ""
	}
}

func configFieldIndexByKey(key string) int {
	trimmed := strings.TrimSpace(key)
	for index := 0; index < configFieldCount; index++ {
		if configFieldKey(index) == trimmed {
			return index
		}
	}
	return -1
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

func (m *model) moveConfigSandboxCursor(delta int) {
	choices := configSandboxChoices()
	if m.configForm == nil || len(choices) == 0 {
		return
	}
	m.configForm.sandboxCursor += delta
	if m.configForm.sandboxCursor < 0 {
		m.configForm.sandboxCursor = len(choices) - 1
	} else if m.configForm.sandboxCursor >= len(choices) {
		m.configForm.sandboxCursor = 0
	}
	m.syncLayout()
}

func (m *model) moveConfigSkillReviewCursor(delta int) {
	choices := configSkillReviewChoices()
	if m.configForm == nil || len(choices) == 0 {
		return
	}
	m.configForm.skillReviewCursor += delta
	if m.configForm.skillReviewCursor < 0 {
		m.configForm.skillReviewCursor = len(choices) - 1
	} else if m.configForm.skillReviewCursor >= len(choices) {
		m.configForm.skillReviewCursor = 0
	}
	m.syncLayout()
}

func (m *model) moveConfigWriteConfirmationCursor(delta int) {
	choices := configWriteConfirmationChoices()
	if m.configForm == nil || len(choices) == 0 {
		return
	}
	m.configForm.writeConfirmationCursor += delta
	if m.configForm.writeConfirmationCursor < 0 {
		m.configForm.writeConfirmationCursor = len(choices) - 1
	} else if m.configForm.writeConfirmationCursor >= len(choices) {
		m.configForm.writeConfirmationCursor = 0
	}
	m.syncLayout()
}

func (m *model) moveConfigReasoningCursor(delta int) {
	choices := configReasoningChoices()
	if m.configForm == nil || len(choices) == 0 {
		return
	}
	m.configForm.reasoningCursor += delta
	if m.configForm.reasoningCursor < 0 {
		m.configForm.reasoningCursor = len(choices) - 1
	} else if m.configForm.reasoningCursor >= len(choices) {
		m.configForm.reasoningCursor = 0
	}
	m.syncLayout()
}

func (m *model) moveConfigAgentAutoLoopCursor(delta int) {
	choices := configAgentAutoLoopChoices()
	if m.configForm == nil || len(choices) == 0 {
		return
	}
	m.configForm.agentAutoLoopCursor += delta
	if m.configForm.agentAutoLoopCursor < 0 {
		m.configForm.agentAutoLoopCursor = len(choices) - 1
	} else if m.configForm.agentAutoLoopCursor >= len(choices) {
		m.configForm.agentAutoLoopCursor = 0
	}
	m.syncLayout()
}

func (m *model) moveConfigSkillsCursor(delta int) {
	choices := configSkillsInstallScopeChoices()
	if m.configForm == nil || len(choices) == 0 {
		return
	}
	m.configForm.skillsCursor += delta
	if m.configForm.skillsCursor < 0 {
		m.configForm.skillsCursor = len(choices) - 1
	} else if m.configForm.skillsCursor >= len(choices) {
		m.configForm.skillsCursor = 0
	}
	m.syncLayout()
}

func (m *model) moveConfigSkillsAutoInstallCursor(delta int) {
	choices := configSkillsAutoInstallChoices()
	if m.configForm == nil || len(choices) == 0 {
		return
	}
	m.configForm.skillsAutoInstallCursor += delta
	if m.configForm.skillsAutoInstallCursor < 0 {
		m.configForm.skillsAutoInstallCursor = len(choices) - 1
	} else if m.configForm.skillsAutoInstallCursor >= len(choices) {
		m.configForm.skillsAutoInstallCursor = 0
	}
	m.syncLayout()
}

func (m *model) moveConfigSkillsFreeTextCursor(delta int) {
	choices := configSkillsFreeTextSearchChoices()
	if m.configForm == nil || len(choices) == 0 {
		return
	}
	m.configForm.skillsFreeTextCursor += delta
	if m.configForm.skillsFreeTextCursor < 0 {
		m.configForm.skillsFreeTextCursor = len(choices) - 1
	} else if m.configForm.skillsFreeTextCursor >= len(choices) {
		m.configForm.skillsFreeTextCursor = 0
	}
	m.syncLayout()
}

func configReasoningChoices() []ConfigChoice {
	return []ConfigChoice{{Value: "off", Label: "关闭"}, {Value: "on", Label: "开启"}}
}

func configAgentAutoLoopChoices() []ConfigChoice {
	return []ConfigChoice{
		{Value: "", Label: "恢复默认", Description: "清空显式配置，恢复默认值。"},
		{Value: "true", Label: "开启", Description: "默认开启智能体自动续跑。"},
		{Value: "false", Label: "关闭", Description: "默认关闭智能体自动续跑。"},
	}
}

func reasoningChoiceValue(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func boolChoiceValue(enabled bool) string {
	if enabled {
		return "true"
	}
	return "false"
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

func configSandboxChoices() []ConfigChoice {
	return []ConfigChoice{
		{Value: "confirm", Label: "每次确认", Description: "命令访问工作区外资源时逐次确认。"},
		{Value: "deny", Label: "禁止访问", Description: "禁止访问当前工作区之外的资源。"},
		{Value: "allow", Label: "直接允许", Description: "允许访问工作区外资源，不再弹出确认。"},
	}
}

func configSkillReviewChoices() []ConfigChoice {
	return []ConfigChoice{
		{Value: internalconfig.SkillReviewPolicyConfirm, Label: "逐次审查", Description: "每次加载本地技能前都显示技能包审查弹窗。"},
		{Value: internalconfig.SkillReviewPolicyIgnore, Label: "忽略审查", Description: "默认跳过技能包审查弹窗，直接加载本地技能。"},
	}
}

func configWriteConfirmationChoices() []ConfigChoice {
	return []ConfigChoice{
		{Value: internalconfig.WriteConfirmationPolicyConfirm, Label: "逐次确认", Description: "执行 write_file、file_edit 前显示写入比较摘要。"},
		{Value: internalconfig.WriteConfirmationPolicyDirect, Label: "直接写入", Description: "跳过写入比较摘要确认，直接执行写入工具。"},
	}
}

func configSkillsInstallScopeChoices() []ConfigChoice {
	return []ConfigChoice{
		{Value: "user", Label: "用户目录", Description: "安装到 ~/.snz1dp/skills。"},
		{Value: "workspace", Label: "工作区", Description: "安装到当前工作区的 .snz1dp/skills。"},
	}
}

func configSkillsAutoInstallChoices() []ConfigChoice {
	return []ConfigChoice{
		{Value: internalconfig.SkillAutoInstallPolicyOff, Label: "关闭", Description: "关闭请求前自动补装。"},
		{Value: internalconfig.SkillAutoInstallPolicyConfirm, Label: "确认", Description: "发现候选技能时先确认，再执行安装。"},
		{Value: internalconfig.SkillAutoInstallPolicyAllow, Label: "自动安装", Description: "发现明确候选技能时直接安装。"},
	}
}

func configSkillsFreeTextSearchChoices() []ConfigChoice {
	return []ConfigChoice{
		{Value: "true", Label: "开启", Description: "允许按任务文本搜索远程技能候选。"},
		{Value: "false", Label: "关闭", Description: "仅按显式缺失技能名搜索。"},
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
	if welcome := buildWelcomeContent(snapshot); welcome != "" {
		entries = append(entries, entry{role: "welcome", content: welcome})
	}
	entries = append(entries, buildBackendStatusEntries(snapshot)...)
	for _, item := range snapshot.History {
		if strings.TrimSpace(item.Content) == "" {
			continue
		}
		entries = append(entries, entry{role: item.Role, content: item.Content})
	}
	if len(entries) == 0 {
		entries = append(entries, entry{role: "welcome", content: buildWelcomeContent(snapshot)})
	}
	return entries
}

func shouldReplaceEntriesOnSnapshotRefresh(current []entry, snapshot Snapshot) bool {
	expected := buildInitialEntries(snapshot)
	if len(current) != len(expected) {
		return false
	}
	for index := range current {
		if current[index].role != expected[index].role {
			return false
		}
		if strings.TrimSpace(current[index].content) != strings.TrimSpace(expected[index].content) {
			return false
		}
	}
	return true
}

func buildWelcomeEntries(snapshot Snapshot) []entry {
	entries := []entry{}
	if welcome := buildWelcomeContent(snapshot); welcome != "" {
		entries = append(entries, entry{role: "welcome", content: welcome})
	}
	entries = append(entries, buildBackendStatusEntries(snapshot)...)
	return entries
}

func buildWelcomeContent(snapshot Snapshot) string {
	lines := []string{}
	if subHead := strings.TrimSpace(snapshot.SubHead); subHead != "" {
		lines = append(lines, subHead)
	}
	if welcome := strings.TrimSpace(snapshot.WelcomeMessage); welcome != "" {
		lines = append(lines, welcome)
	}
	questions := []string{}
	for _, question := range snapshot.Questions {
		trimmed := strings.TrimSpace(question)
		if trimmed != "" {
			questions = append(questions, trimmed)
		}
	}
	if len(questions) > 0 {
		lines = append(lines, "示例问题:\n"+strings.Join(numberedLines(questions), "\n"))
	}
	return strings.Join(lines, "\n\n")
}

func numberedLines(values []string) []string {
	result := make([]string, 0, len(values))
	for index, value := range values {
		result = append(result, fmt.Sprintf("%d. %s", index+1, value))
	}
	return result
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
		if m.snapshot.AuthRequired {
			m.input.Placeholder = "服务端已连接但未登录，登录表单已自动打开；也可手动执行 /login"
			return
		}
		m.input.Placeholder = "输入内容后回车发送"
		return
	}
	m.input.Placeholder = "服务端不可用，当前为只读模式，仅支持 /config、/tasks、/skills、/reasoning、/help、/clear、/exit"
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
	m.resetAssistantChunkState()
	m.requestStats = RequestMetrics{
		RunID:      runID,
		StartedAt:  time.Now(),
		InputChars: utf8.RuneCountInString(input),
	}
}

func (m *model) resetAssistantChunkState() {
	m.splitNextAssistant = false
	m.lastAssistantMessageID = ""
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

func (m model) contextUsageSegment(compact bool) string {
	usage := m.requestStats.TokenUsage
	if usage == nil || usage.ContextWindow == nil {
		return ""
	}
	contextWindow := usage.ContextWindow
	maxTokens := contextWindow.MaxInputTokens
	currentTokens := contextWindow.CurrentRoundInputTokens
	available := resolveContextAvailableTokens(contextWindow)
	if maxTokens > 0 {
		if currentTokens < 0 {
			currentTokens = 0
		}
		if currentTokens > maxTokens {
			currentTokens = maxTokens
		}
		percent := float64(currentTokens) / float64(maxTokens) * 100
		if compact {
			parts := []string{fmt.Sprintf("上下文 %s/%s", formatCompactInt(currentTokens), formatCompactInt(maxTokens))}
			if available > 0 {
				parts = append(parts, "可"+formatCompactInt(available))
			}
			return strings.Join(parts, " ")
		}
		parts := []string{fmt.Sprintf("当前上下文 %s/%s %.0f%%", formatCompactInt(currentTokens), formatCompactInt(maxTokens), percent)}
		if available > 0 {
			parts = append(parts, fmt.Sprintf("可用 %s", formatCompactInt(available)))
		}
		if historyUsage := formatContextHistoryUsage(contextWindow); historyUsage != "" {
			parts = append(parts, historyUsage)
		}
		if watermarkWarning := formatContextWatermarkWarning(contextWindow); watermarkWarning != "" {
			parts = append(parts, watermarkWarning)
		}
		return strings.Join(parts, " · ")
	}
	if available <= 0 {
		return ""
	}
	if compact {
		return fmt.Sprintf("上下文可用 %s", formatCompactInt(available))
	}
	return fmt.Sprintf("上下文可用 %s", formatCompactInt(available))
}

func (m model) contextUsageGrid() string {
	usage := m.requestStats.TokenUsage
	if usage == nil || usage.ContextWindow == nil {
		return ""
	}
	return formatContextUsageGrid(
		usage.ContextWindow.CurrentRoundInputTokens,
		usage.ContextWindow.MaxInputTokens,
		8,
	)
}

func formatContextUsageGrid(currentTokens int, maxTokens int, cellCount int) string {
	if maxTokens <= 0 || cellCount <= 0 {
		return ""
	}
	if currentTokens < 0 {
		currentTokens = 0
	}
	if currentTokens > maxTokens {
		currentTokens = maxTokens
	}
	usedRatio := float64(currentTokens) / float64(maxTokens)
	usedCells := int(math.Round(usedRatio * float64(cellCount)))
	if currentTokens > 0 && usedCells == 0 {
		usedCells = 1
	}
	if usedCells > cellCount {
		usedCells = cellCount
	}
	builder := strings.Builder{}
	builder.Grow(cellCount)
	for index := 0; index < cellCount; index++ {
		if index < usedCells {
			builder.WriteRune('■')
			continue
		}
		builder.WriteRune('□')
	}
	return fmt.Sprintf("%s%3.0f%%", builder.String(), usedRatio*100)
}

func (m model) inputContextUsageHint() string {
	usage := m.requestStats.TokenUsage
	if usage == nil || usage.ContextWindow == nil {
		return m.defaultInputContextUsageHint()
	}
	contextWindow := usage.ContextWindow
	parts := []string{}
	maxTokens := contextWindow.MaxInputTokens
	available := resolveContextAvailableTokens(contextWindow)
	if nextRoundAvailabilityRate := formatContextAvailabilityRate(available, maxTokens); nextRoundAvailabilityRate != "" {
		parts = append(parts, nextRoundAvailabilityRate)
	}
	if watermarkWarning := formatContextWatermarkWarning(contextWindow); watermarkWarning != "" {
		parts = append(parts, watermarkWarning)
	}
	if historyUsage := formatContextHistoryUsage(contextWindow); historyUsage != "" {
		parts = append(parts, historyUsage)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " · ")
	}
	return ""
}

func (m model) defaultInputContextUsageHint() string {
	if strings.TrimSpace(m.snapshot.SessionID) == "" {
		return "上下文窗口100%可用"
	}
	return "正在输出..."
}

func formatContextAvailabilityRate(available int, maxTokens int) string {
	if maxTokens <= 0 || available < 0 {
		return ""
	}
	if available > maxTokens {
		available = maxTokens
	}
	return fmt.Sprintf("共%s剩%s可用", formatCompactInt(maxTokens), formatCompactInt(available))
}

func formatContextHistoryUsage(contextWindow *ContextWindow) string {
	if contextWindow == nil || contextWindow.ContextUsageTokens <= 0 {
		return ""
	}
	historyUsage := fmt.Sprintf("历史 %s", formatCompactInt(contextWindow.ContextUsageTokens))
	breakdown := make([]string, 0, 2)
	if contextWindow.DirectMessageTokens > 0 {
		breakdown = append(breakdown, fmt.Sprintf("直传 %s", formatCompactInt(contextWindow.DirectMessageTokens)))
	}
	if contextWindow.CompressedHistoryTokens > 0 {
		breakdown = append(breakdown, fmt.Sprintf("压缩 %s", formatCompactInt(contextWindow.CompressedHistoryTokens)))
	}
	if len(breakdown) == 0 {
		return historyUsage
	}
	return historyUsage + "（" + strings.Join(breakdown, " / ") + "）"
}

func resolveContextAvailableTokens(contextWindow *ContextWindow) int {
	if contextWindow == nil {
		return 0
	}
	if contextWindow.NextRoundAvailableTokens > 0 {
		return contextWindow.NextRoundAvailableTokens
	}
	if contextWindow.CurrentRoundAvailableTokens > 0 {
		return contextWindow.CurrentRoundAvailableTokens
	}
	if contextWindow.MaxInputTokens > 0 {
		available := contextWindow.MaxInputTokens - contextWindow.CurrentRoundInputTokens
		if available > 0 {
			return available
		}
	}
	return 0
}

func formatContextWatermarkWarning(contextWindow *ContextWindow) string {
	if contextWindow == nil {
		return ""
	}
	if contextWindow.HighWatermarkTokens <= 0 {
		return ""
	}
	currentTokens := contextWindow.CurrentRoundInputTokens
	if currentTokens < contextWindow.HighWatermarkTokens {
		return ""
	}
	if contextWindow.CompressionTargetTokens > 0 {
		return fmt.Sprintf("已过高水位→%s", formatCompactInt(contextWindow.CompressionTargetTokens))
	}
	return "已过高水位"
}

func cloneRequestMetrics(source *RequestMetrics) RequestMetrics {
	if source == nil {
		return RequestMetrics{}
	}
	cloned := *source
	if source.TokenUsage != nil {
		tokenUsage := *source.TokenUsage
		if source.TokenUsage.ContextWindow != nil {
			contextWindow := *source.TokenUsage.ContextWindow
			tokenUsage.ContextWindow = &contextWindow
		}
		cloned.TokenUsage = &tokenUsage
	}
	if source.Compression != nil {
		compression := *source.Compression
		cloned.Compression = &compression
	}
	return cloned
}

func (m model) requestStatsSegment(compact bool) string {
	if m.requestStats.StartedAt.IsZero() {
		return ""
	}
	duration := time.Since(m.requestStats.StartedAt)
	if !m.requestStats.CompletedAt.IsZero() {
		duration = m.requestStats.CompletedAt.Sub(m.requestStats.StartedAt)
	}
	if compact {
		parts := []string{formatDuration(duration)}
		if m.requestStats.ToolCallCount > 0 {
			parts = append(parts, fmt.Sprintf("%dT", m.requestStats.ToolCallCount))
		}
		if m.requestStats.CommandCount > 0 {
			parts = append(parts, fmt.Sprintf("%dC", m.requestStats.CommandCount))
		}
		return strings.Join(parts, " ")
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

func currentFlowIndex(flows []FlowItem, current string) int {
	for index, item := range flows {
		if item.ID == current || item.Name == current {
			return index
		}
	}
	return 0
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

func renderMarkdown(content string, renderer *glamour.TermRenderer) string {
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
	if value < 1000000 {
		return fmt.Sprintf("%dK", int(math.Round(float64(value)/1000.0)))
	}
	return fmt.Sprintf("%dM", int(math.Round(float64(value)/1000000.0)))
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

func trimMiddleLeftForWidth(text string, width int) string {
	if width <= 0 {
		return text
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	ellipsis := "..."
	if width <= lipgloss.Width(ellipsis)+4 {
		return trimLeftForWidth(text, width)
	}
	runes := []rune(text)
	if len(runes) <= 2 {
		return trimLeftForWidth(text, width)
	}
	leftLen := len(runes) / 3
	if leftLen < 2 {
		leftLen = 2
	}
	rightLen := len(runes) / 2
	if rightLen < 3 {
		rightLen = 3
	}
	if leftLen+rightLen >= len(runes) {
		rightLen = len(runes) - leftLen - 1
	}
	if rightLen < 1 {
		return trimLeftForWidth(text, width)
	}
	build := func(left int, right int) string {
		return string(runes[:left]) + ellipsis + string(runes[len(runes)-right:])
	}
	trimmed := build(leftLen, rightLen)
	for lipgloss.Width(trimmed) > width && (leftLen > 1 || rightLen > 1) {
		if leftLen >= rightLen && leftLen > 1 {
			leftLen--
		} else if rightLen > 1 {
			rightLen--
		} else {
			break
		}
		trimmed = build(leftLen, rightLen)
	}
	if lipgloss.Width(trimmed) > width {
		return trimLeftForWidth(text, width)
	}
	return trimmed
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
	case strings.EqualFold(trimmed, "/internet"):
		return true
	case strings.EqualFold(trimmed, "/knowledge"):
		return true
	case strings.EqualFold(trimmed, "/files"):
		return true
	case strings.EqualFold(trimmed, "/ask"):
		return true
	case strings.EqualFold(trimmed, "/plan"):
		return true
	case strings.EqualFold(trimmed, "/agent"):
		return true
	case strings.EqualFold(trimmed, "/config"):
		return true
	case strings.EqualFold(trimmed, "/config show"):
		return true
	case strings.EqualFold(trimmed, "/login"):
		return true
	case strings.EqualFold(trimmed, "/login help"):
		return true
	case strings.EqualFold(trimmed, "/login status"):
		return true
	case strings.EqualFold(trimmed, "/login logout"):
		return true
	case strings.EqualFold(trimmed, "/login qr"):
		return true
	case strings.EqualFold(trimmed, "/login qr start"):
		return true
	case strings.EqualFold(trimmed, "/new"):
		return true
	case strings.EqualFold(trimmed, "/session"):
		return true
	case strings.EqualFold(trimmed, "/reasoning"):
		return true
	case strings.EqualFold(trimmed, "/reasoning status"):
		return true
	case strings.EqualFold(trimmed, "/skills"):
		return true
	case strings.HasPrefix(strings.ToLower(trimmed), "/internet "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/internet ")) != ""
	case strings.HasPrefix(strings.ToLower(trimmed), "/knowledge "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/knowledge ")) != ""
	case strings.HasPrefix(strings.ToLower(trimmed), "/files "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/files ")) != ""
	case strings.HasPrefix(strings.ToLower(trimmed), "/reasoning "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/reasoning ")) != ""
	case strings.HasPrefix(strings.ToLower(trimmed), "/skills "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/skills ")) != ""
	case strings.HasPrefix(strings.ToLower(trimmed), "/login qr wait "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/login qr wait ")) != ""
	case strings.HasPrefix(strings.ToLower(trimmed), "/login password "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/login password ")) != ""
	case strings.EqualFold(trimmed, "/review"):
		return true
	case strings.HasPrefix(strings.ToLower(trimmed), "/review "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/review ")) != ""
	case strings.EqualFold(trimmed, "/tasks"):
		return true
	case strings.EqualFold(trimmed, "/tasks list"):
		return true
	case strings.HasPrefix(strings.ToLower(trimmed), "/tasks "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/tasks ")) != ""
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
	case strings.HasPrefix(strings.ToLower(trimmed), "/ask "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/ask ")) != ""
	case strings.HasPrefix(strings.ToLower(trimmed), "/plan "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/plan ")) != ""
	case strings.HasPrefix(strings.ToLower(trimmed), "/agent "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/agent ")) != ""
	case strings.HasPrefix(strings.ToLower(trimmed), "/agents "):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "/agents ")) != ""
	default:
		return false
	}
}

func trimText(content string, maxLen int) string {
	content = strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))
	runes := []rune(content)
	if len(runes) <= maxLen {
		return content
	}
	if maxLen < 4 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

func currentToolLabel(text string, fallbackValue string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return fallbackValue
	}
	return trimText(trimmed, 48)
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

func approvalModeStatus(value string, compact bool) string {
	if compact {
		switch strings.TrimSpace(strings.ToLower(value)) {
		case "auto":
			return "自动"
		case "readonly":
			return "只读"
		case "confirm", "":
			return "确认"
		default:
			return trimText(strings.TrimSpace(value), 8)
		}
	}
	return approvalModeLabel(value)
}

func reasoningStatus(enabled bool, compact bool) string {
	if compact {
		if enabled {
			return "思考开"
		}
		return "思考关"
	}
	if enabled {
		return "思考模式已开"
	}
	return "思考模式已关"
}

func sessionSandboxEscapePolicyStatus(value string, compact bool) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "allow":
		if compact {
			return "允许访问外部资源"
		}
		return "允许访问外部资源"
	case "deny":
		if compact {
			return "禁止访问外部资源"
		}
		return "禁止访问外部资源"
	case "confirm":
		if compact {
			return "访问外部需确认"
		}
		return "访问外部资源需确认"
	default:
		return ""
	}
}

func requestModeStatus(value string, compact bool) string {
	if compact {
		switch requestModeKey(value) {
		case "plan":
			return "计划"
		case "agent":
			return "执行"
		default:
			return "问答"
		}
	}
	return requestModeLabel(value)
}

func requestModeLabel(value string) string {
	switch requestModeKey(value) {
	case "plan":
		return "计划模式"
	case "agent":
		return "执行模式"
	default:
		return "问答模式"
	}
}

func requestModeKey(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "plan":
		return "plan"
	case "agent":
		return "agent"
	default:
		return "ask"
	}
}

func (m model) toolSummarySegment(compact bool) string {
	if strings.TrimSpace(m.currentToolLabel) != "" {
		if compact {
			return "执行 " + trimText(m.currentToolLabel, 20)
		}
		return "执行 " + trimText(m.currentToolLabel, 40)
	}
	if strings.TrimSpace(m.latestToolSummary) == "" {
		return ""
	}
	if time.Since(m.latestToolAt) > 20*time.Second {
		return ""
	}
	if compact {
		return trimText(m.latestToolSummary, 20)
	}
	return trimText(m.latestToolSummary, 48)
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
