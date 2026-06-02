package cmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/term"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
)

var (
	cliDisableFocusReporting = "\x1b[?1004l"
	cmdANSICSIRegexp         = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
	cmdANSIOSCRegexp         = regexp.MustCompile(`\x1b\][^\a]*(\a|\x1b\\)`)
	cliCitationRegexp        = regexp.MustCompile(`\[\^([123])\.(\d+)\^\]|\^([123])\.(\d+)\^`)
	cliCitationExactRegexp   = regexp.MustCompile(`^(?:\[\^([123])\.(\d+)\^\]|\^([123])\.(\d+)\^)$`)
	cliCitationPrefixRegexp  = regexp.MustCompile(`^(?:\[\^|\^)(?:[123](?:\.\d*(?:\^\]?)?)?)?$`)
	isTerminalFD             = term.IsTerminal
)

type fdWriter interface {
	Fd() uintptr
}

type App struct {
	configManager            *internalconfig.Manager
	sessionStore             *agent.SessionStore
	stdout                   io.Writer
	stderr                   io.Writer
	stdin                    io.Reader
	runtime                  *clientruntime.ClientRuntime
	skillInstallScopeSession string
	skillInstallScopePinned  bool
}

type rootOptions struct {
	Flow                      string
	SessionID                 string
	UploadFile                string
	RequestMode               string
	ReasoningMode             string
	InternetSearchMode        string
	NewSession                bool
	Continue                  bool
	ShowStreamEvents          bool
	ShowThinking              bool
	SandboxEscapePolicy       string
	CrossSessionMemoryEnabled bool
	CrossSessionMemorySet     bool
}

type cliStreamRenderState struct {
	insideThink         bool
	pending             string
	citationPending     string
	controlResidue      streamControlResidueState
	streamingSuffix     string
	splitNextAssistant  bool
	hasPrintedAssistant bool
}

func buildProviderClient(config internalconfig.Config) *providerclient.Client {
	client := providerclient.NewClient(config.BackendBaseURL, config.AccessToken)
	client.SetAPIPrefix(config.BackendAPIPrefix)
	client.SetHTTPTimeoutSeconds(config.HTTPTimeoutSeconds)
	client.SetStreamIdleTimeoutSeconds(config.StreamIdleTimeoutSeconds)
	return client
}

func buildAuthClient(config internalconfig.Config) *providerclient.AuthClient {
	client := providerclient.NewAuthClient(config.BackendBaseURL)
	client.SetAuthAPIPrefix(config.AuthAPIPrefix)
	return client
}

func NewApp() (*App, error) {
	configManager, err := internalconfig.NewManager()
	if err != nil {
		return nil, err
	}

	sessionStore, err := agent.NewSessionStore()
	if err != nil {
		return nil, err
	}

	return &App{
		configManager: configManager,
		sessionStore:  sessionStore,
		stdout:        os.Stdout,
		stderr:        os.Stderr,
		stdin:         os.Stdin,
		runtime:       clientruntime.NewClientRuntime(os.Stdin, os.Stdout, os.Stderr),
	}, nil
}

func (a *App) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.runTUI(ctx, nil)
	}

	switch args[0] {
	case "task":
		return a.runTask(ctx, args[1:])
	case "memory":
		return a.runMemory(ctx, args[1:])
	case "about":
		return a.runAbout(args[1:])
	case "login":
		return a.runLogin(ctx, args[1:])
	case "skills":
		return a.runSkills(args[1:])
	case "cli":
		return errors.New("`cli` 子命令已移除，请改用 `taiyiflow-cli task`")
	case "config":
		if len(args) == 1 {
			return a.runTUI(ctx, []string{"/config"})
		}
		return a.runConfig(args[1:])
	case "browser":
		return a.runBrowser(ctx, args[1:])
	case "doctor":
		return a.runDoctor(args[1:])
	case "agents":
		return a.runAgents(ctx, args[1:])
	case "flows":
		return errors.New("`flows` 子命令已移除，请改用 `taiyiflow-cli agents`")
	case "tui":
		return errors.New("`tui` 子命令已移除，请直接执行 `taiyiflow-cli`")
	case "version":
		return a.runVersion(args[1:])
	case "help", "--help", "-h":
		a.printRootUsage()
		return nil
	default:
		return a.runTask(ctx, args)
	}
}

func (a *App) runTask(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("taiyiflow-cli", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)

	var options rootOptions
	flagSet.StringVar(&options.Flow, "agent", "", "指定要运行的智能体 ID 或名称")
	flagSet.StringVar(&options.SessionID, "session-id", "", "指定已有会话 ID，并在该会话内启动任务")
	flagSet.StringVar(&options.UploadFile, "file", "", "上传一个本地文件并作为本次任务输入")
	flagSet.StringVar(&options.RequestMode, "mode", "", "task 请求模式（agent|ask|plan），默认 agent")
	flagSet.StringVar(&options.ReasoningMode, "reasoning", "", "思考模式（on|off），仅影响当前 task 会话")
	flagSet.StringVar(&options.InternetSearchMode, "internet-search", "", "联网搜索（on|off），仅影响当前 task 会话")
	flagSet.BoolVar(&options.NewSession, "new", false, "忽略已有会话并新建会话")
	flagSet.BoolVar(&options.Continue, "continue", false, "继续当前会话")
	flagSet.BoolVar(&options.ShowStreamEvents, "show-stream-events", false, "显示系统提示与工具调用等流式调试信息")
	flagSet.BoolVar(&options.ShowThinking, "show-thinking", false, "显示思考内容（不输出 <think></think> 标签）")
	flagSet.StringVar(&options.SandboxEscapePolicy, "external-resource-access-policy", "", "外部资源访问策略（confirm|deny|allow），仅影响当前命令")
	flagSet.BoolVar(&options.CrossSessionMemoryEnabled, "cross-session-memory-enabled", false, "跨会话记忆（true|false），默认使用系统配置")

	if err := flagSet.Parse(args); err != nil {
		return err
	}
	flagSet.Visit(func(flag *flag.Flag) {
		switch flag.Name {
		case "cross-session-memory-enabled":
			options.CrossSessionMemorySet = true
		}
	})
	if options.NewSession && strings.TrimSpace(options.SessionID) != "" {
		return errors.New("--new 与 --session-id 不能同时使用")
	}
	requestMode, err := resolveTaskRequestMode(options.RequestMode)
	if err != nil {
		return err
	}
	options.RequestMode = requestMode

	reasoningMode, err := resolveTaskReasoningMode(options.ReasoningMode)
	if err != nil {
		return err
	}
	options.ReasoningMode = reasoningMode

	internetSearchMode, err := resolveTaskInternetSearchMode(options.InternetSearchMode)
	if err != nil {
		return err
	}
	options.InternetSearchMode = internetSearchMode

	task := strings.TrimSpace(strings.Join(flagSet.Args(), " "))
	if task == "" && !options.Continue {
		return a.runInteractive(ctx, options)
	}

	return a.runTaskOnce(ctx, options, task)
}

func (a *App) runInteractive(ctx context.Context, options rootOptions) error {
	a.disableCLIFocusReporting()

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	if options.CrossSessionMemorySet {
		config.CrossSessionMemoryEnabled = options.CrossSessionMemoryEnabled
	}
	providerClient := buildProviderClient(config)
	config, _, err = a.ensureProviderAuthenticated(ctx, providerClient, config)
	if err != nil {
		return err
	}
	if err := a.ensureBackendAvailable(ctx, providerClient, config.BackendBaseURL); err != nil {
		return err
	}
	config, err = a.ensureDefaultFlow(ctx, providerClient, config)
	if err != nil {
		return err
	}

	flowID, err := resolveFlowID(options.Flow, config.DefaultFlow)
	if err != nil {
		return err
	}
	workdir := resolveLocalWorkdir(config.Workdir)

	sessionState, err := a.prepareSession(flowID, options.NewSession, workdir, options.RequestMode)
	if err != nil {
		return err
	}
	sessionState = applyExplicitTaskSessionID(sessionState, options.SessionID)
	if options.ReasoningMode != "" {
		sessionState.ReasoningOverride = options.ReasoningMode
	}
	if options.InternetSearchMode != "" {
		sessionState.InternetSearchOverride = options.InternetSearchMode
	}
	flowDisplayName := a.resolveFlowDisplayName(ctx, providerClient, flowID)
	sessionState.FlowDisplayName = flowDisplayName

	metadata, err := providerClient.GetMetadata(ctx, flowID)
	if err == nil && metadata.WelcomeMessage != "" {
		fmt.Fprintln(a.stdout, metadata.WelcomeMessage)
	}
	if err == nil && strings.TrimSpace(metadata.Name) != "" {
		flowDisplayName = metadata.Name
		sessionState.FlowDisplayName = flowDisplayName
	}
	if err != nil {
		fmt.Fprintf(a.stderr, "提示: 获取元信息失败，继续进入交互模式: %v\n", err)
	}

	reader := bufio.NewScanner(a.stdin)
	fmt.Fprintln(a.stdout, "进入交互模式，输入 /ask、/plan、/agent 切换请求模式，输入 /reasoning 调整思考模式，输入 /exit 退出。")

	for {
		fmt.Fprint(a.stdout, "> ")
		if !reader.Scan() {
			if err := reader.Err(); err != nil {
				return fmt.Errorf("读取输入失败: %w", err)
			}
			return nil
		}

		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}

		switch {
		case line == "/exit":
			return nil
		case line == "/review" || strings.HasPrefix(strings.ToLower(line), "/review "):
			target, reviewErr := parseReviewSlashCommand(line)
			if reviewErr != nil {
				fmt.Fprintf(a.stderr, "审查命令执行失败: %v\n", reviewErr)
				continue
			}
			prompt := a.buildReviewPrompt(ctx, workdir, target)
			sessionState.RequestMode = reviewRequestMode()
			sessionState.ContextDir = workdir
			sessionState.FlowDisplayName = flowDisplayName
			updatedState, err := a.streamTask(ctx, providerClient, config, flowID, prompt, sessionState, options.SandboxEscapePolicy, options.ShowStreamEvents, options.ShowThinking)
			if err != nil {
				fmt.Fprintf(a.stderr, "执行审查失败: %v\n", err)
				continue
			}
			sessionState = updatedState
			continue
		case isTasksSlashCommand(line):
			output, _, err := a.executeTasksSlashCommand(ctx, config, line)
			if err != nil {
				fmt.Fprintf(a.stderr, "任务命令执行失败: %v\n", err)
				continue
			}
			fmt.Fprintln(a.stdout, output)
			continue
		case line == "/permissions" || strings.HasPrefix(strings.ToLower(line), "/permissions "):
			fmt.Fprintln(a.stdout, "/permissions 已移除，请使用 /config 管理权限与执行配置。")
			continue
		case line == "/skills" || strings.HasPrefix(strings.ToLower(line), "/skills "):
			output, err := a.executeSkillsCommand(parseSlashSkillsArgs(line))
			if err != nil {
				fmt.Fprintf(a.stderr, "技能命令执行失败: %v\n", err)
				continue
			}
			fmt.Fprintln(a.stdout, output)
			continue
		case isMemorySlashCommand(line):
			output, _, err := a.executeMemorySlashCommand(ctx, providerClient, line, flowID)
			if err != nil {
				fmt.Fprintf(a.stderr, "跨会话记忆命令执行失败: %v\n", err)
				continue
			}
			if strings.TrimSpace(output) != "" {
				fmt.Fprintln(a.stdout, output)
			}
			continue
		case line == "/reasoning" || strings.HasPrefix(strings.ToLower(line), "/reasoning "):
			output, updatedState, err := a.executeReasoningCommand(config, line, sessionState)
			if err != nil {
				fmt.Fprintf(a.stderr, "思考模式命令执行失败: %v\n", err)
				continue
			}
			sessionState = updatedState
			fmt.Fprintln(a.stdout, output)
			continue
		case line == "/agents":
			fmt.Fprintln(a.stdout, "请使用 `taiyiflow-cli agents` 查看或切换默认智能体。")
			continue
		case line == "/help":
			a.printInteractiveUsage(ctx, providerClient, flowDisplayName, flowID, sessionState)
			continue
		case line == "/about":
			fmt.Fprintln(a.stdout, renderAboutText())
			continue
		case line == "/clear":
			a.clearInteractiveScreen()
			continue
		case line == "/session":
			a.printSessionStatus(ctx, providerClient, flowDisplayName, flowID, sessionState)
			continue
		case line == "/sessions":
			if err := a.printSavedSessions(flowID, sessionState.SessionID, workdir); err != nil {
				return err
			}
			continue
		case line == "/new":
			sessionState = agent.SessionState{
				FlowID:                         flowID,
				FlowDisplayName:                flowDisplayName,
				Workdir:                        workdir,
				RequestMode:                    provider.RequestTypeAgent,
				ContextDir:                     workdir,
				AgentAutoLoopMaxRoundsOverride: sessionState.AgentAutoLoopMaxRoundsOverride,
				SandboxEscapePolicyOverride:    sessionState.SandboxEscapePolicyOverride,
			}
			if err := a.sessionStore.Delete(flowID, workdir); err != nil {
				return err
			}
			a.clearInteractiveScreen()
			fmt.Fprintln(a.stdout, "已重置本地会话。")
			continue
		case strings.HasPrefix(strings.ToLower(line), "/config session "):
			output, err := a.runInteractiveSessionConfigCommand(line, &sessionState)
			if err != nil {
				fmt.Fprintf(a.stderr, "会话配置命令执行失败: %v\n", err)
				continue
			}
			fmt.Fprintln(a.stdout, output)
			continue
		case line == "/config" || strings.HasPrefix(strings.ToLower(line), "/config "):
			if err := a.runInteractiveConfigCommand(line); err != nil {
				fmt.Fprintf(a.stderr, "配置命令执行失败: %v\n", err)
				continue
			}
			if !isMutatingSlashConfigCommand(line) {
				continue
			}

			updatedConfig, err := a.configManager.Load()
			if err != nil {
				return err
			}
			config = updatedConfig
			providerClient = buildProviderClient(config)
			workdir = resolveLocalWorkdir(config.Workdir)

			if err := a.ensureBackendAvailable(ctx, providerClient, config.BackendBaseURL); err != nil {
				flowID = strings.TrimSpace(firstNonEmpty(options.Flow, config.DefaultFlow))
				flowDisplayName = a.resolveFlowDisplayName(ctx, providerClient, flowID)
				sessionState = agent.SessionState{
					FlowID:                         flowID,
					FlowDisplayName:                flowDisplayName,
					Workdir:                        workdir,
					RequestMode:                    firstNonEmpty(sessionState.RequestMode, provider.RequestTypeAgent),
					ContextDir:                     resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, workdir)),
					AgentAutoLoopMaxRoundsOverride: sessionState.AgentAutoLoopMaxRoundsOverride,
					SandboxEscapePolicyOverride:    sessionState.SandboxEscapePolicyOverride,
					SkillReviewPolicyOverride:      sessionState.SkillReviewPolicyOverride,
					ApprovedSkillNames:             sessionState.ApprovedSkillNames,
				}
				fmt.Fprintf(a.stderr, "提示: 新配置尚未连通服务端: %v\n", err)
				continue
			}

			config, err = a.ensureDefaultFlow(ctx, providerClient, config)
			if err != nil {
				fmt.Fprintf(a.stderr, "提示: 刷新默认智能体失败: %v\n", err)
				continue
			}
			flowID, err = resolveFlowID(options.Flow, config.DefaultFlow)
			if err != nil {
				fmt.Fprintf(a.stderr, "提示: %v\n", err)
				continue
			}
			flowDisplayName = a.resolveFlowDisplayName(ctx, providerClient, flowID)
			sessionState, err = a.prepareSession(flowID, false, workdir, sessionState.RequestMode)
			if err != nil {
				return err
			}
			sessionState.FlowDisplayName = flowDisplayName
			continue
		}

		if mode, prompt, ok := parseModeSlashCommand(line); ok {
			sessionState.RequestMode = mode
			sessionState.ContextDir = workdir
			sessionState.FlowDisplayName = flowDisplayName
			if strings.TrimSpace(prompt) == "" {
				if err := a.sessionStore.Save(sessionState); err != nil {
					return err
				}
				fmt.Fprintf(a.stdout, "已切换请求模式: %s\n", requestModeLabel(mode))
				continue
			}
			updatedState, err := a.streamTask(ctx, providerClient, config, flowID, prompt, sessionState, options.SandboxEscapePolicy, options.ShowStreamEvents, options.ShowThinking)
			if err != nil {
				fmt.Fprintf(a.stderr, "执行任务失败: %v\n", err)
				continue
			}
			sessionState = updatedState
			continue
		}

		updatedState, err := a.streamTask(ctx, providerClient, config, flowID, line, sessionState, options.SandboxEscapePolicy, options.ShowStreamEvents, options.ShowThinking)
		if err != nil {
			fmt.Fprintf(a.stderr, "执行任务失败: %v\n", err)
			continue
		}
		sessionState = updatedState
	}
}

func (a *App) clearInteractiveScreen() {
	fmt.Fprint(a.stdout, "\033[H\033[2J")
}

func (a *App) runTaskOnce(ctx context.Context, options rootOptions, task string) error {
	a.disableCLIFocusReporting()
	if a.runtime != nil {
		previousInteractivePromptsEnabled := a.runtime.InteractivePromptsEnabled()
		a.runtime.SetInteractivePromptsEnabled(false)
		defer a.runtime.SetInteractivePromptsEnabled(previousInteractivePromptsEnabled)
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	if options.CrossSessionMemorySet {
		config.CrossSessionMemoryEnabled = options.CrossSessionMemoryEnabled
	}
	providerClient := buildProviderClient(config)
	config, _, err = a.ensureProviderAuthenticated(ctx, providerClient, config)
	if err != nil {
		return err
	}
	if err := a.ensureBackendAvailable(ctx, providerClient, config.BackendBaseURL); err != nil {
		return err
	}
	config, err = a.ensureDefaultFlow(ctx, providerClient, config)
	if err != nil {
		return err
	}

	flowID, err := resolveFlowID(options.Flow, config.DefaultFlow)
	if err != nil {
		return err
	}
	workdir := resolveLocalWorkdir(config.Workdir)

	sessionState, err := a.prepareSession(flowID, options.NewSession, workdir, options.RequestMode)
	if err != nil {
		return err
	}
	sessionState = applyExplicitTaskSessionID(sessionState, options.SessionID)
	if options.ReasoningMode != "" {
		sessionState.ReasoningOverride = options.ReasoningMode
	}
	if options.InternetSearchMode != "" {
		sessionState.InternetSearchOverride = options.InternetSearchMode
	}
	inputFiles := []string(nil)
	if strings.TrimSpace(options.UploadFile) != "" {
		uploadProviderClient := a.providerWithCallbackAuthRefresh(providerClient, &config)
		ensuredSessionID, ensureErr := clientruntime.EnsureUploadSessionID(ctx, uploadProviderClient, sessionState.SessionID)
		if ensureErr != nil {
			return ensureErr
		}
		sessionState.SessionID = ensuredSessionID
		uploadedPath, uploadErr := uploadInputFile(
			ctx,
			uploadProviderClient,
			flowID,
			sessionState.SessionID,
			firstNonEmpty(sessionState.ContextDir, workdir),
			options.UploadFile,
		)
		if uploadErr != nil {
			return uploadErr
		}
		inputFiles = append(inputFiles, uploadedPath)
	}

	_, err = a.streamTaskWithInputFiles(ctx, providerClient, config, flowID, task, sessionState, options.SandboxEscapePolicy, options.ShowStreamEvents, options.ShowThinking, inputFiles)
	return err
}

func (a *App) prepareSession(flowID string, newSession bool, workdir string, requestMode string) (agent.SessionState, error) {
	resolvedWorkdir := resolveLocalWorkdir(workdir)
	requestedMode := strings.TrimSpace(requestMode)
	normalizedMode := provider.RequestTypeAgent
	if requestedMode != "" {
		normalizedMode = normalizeRequestMode(requestMode)
	}
	if newSession {
		if err := a.sessionStore.Delete(flowID, resolvedWorkdir); err != nil {
			return agent.SessionState{}, err
		}
		return agent.SessionState{FlowID: flowID, Workdir: resolvedWorkdir, RequestMode: normalizedMode, ContextDir: resolvedWorkdir}, nil
	}
	state, err := a.sessionStore.Load(flowID, resolvedWorkdir)
	if err != nil {
		return agent.SessionState{}, err
	}
	if requestedMode != "" {
		state.RequestMode = normalizedMode
	} else if strings.TrimSpace(state.RequestMode) == "" {
		state.RequestMode = provider.RequestTypeAgent
	}
	if strings.TrimSpace(state.ContextDir) == "" {
		state.ContextDir = resolvedWorkdir
	}
	return state, nil
}

func applyExplicitTaskSessionID(state agent.SessionState, sessionID string) agent.SessionState {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return state
	}
	if strings.TrimSpace(state.SessionID) != trimmedSessionID {
		state.SessionTitle = ""
	}
	state.SessionID = trimmedSessionID
	return state
}

func (a *App) streamTask(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
	task string,
	sessionState agent.SessionState,
	commandSandboxEscapePolicy string,
	showStreamEvents bool,
	showThinking bool,
) (agent.SessionState, error) {
	return a.streamTaskWithInputFiles(
		ctx,
		providerClient,
		config,
		flowID,
		task,
		sessionState,
		commandSandboxEscapePolicy,
		showStreamEvents,
		showThinking,
		nil,
	)
}

func (a *App) streamTaskWithInputFiles(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
	task string,
	sessionState agent.SessionState,
	commandSandboxEscapePolicy string,
	showStreamEvents bool,
	showThinking bool,
	inputFiles []string,
) (agent.SessionState, error) {
	return a.streamTaskWithSkillRetry(
		ctx,
		providerClient,
		config,
		flowID,
		task,
		sessionState,
		commandSandboxEscapePolicy,
		showStreamEvents,
		showThinking,
		inputFiles,
		0,
	)
}

func (a *App) streamTaskWithSkillRetry(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
	task string,
	sessionState agent.SessionState,
	commandSandboxEscapePolicy string,
	showStreamEvents bool,
	showThinking bool,
	inputFiles []string,
	retryCount int,
) (agent.SessionState, error) {
	providerClient = a.providerWithCallbackAuthRefresh(providerClient, &config)
	a.runtime.SetStreamEventLogsEnabled(showStreamEvents)
	defer a.runtime.SetStreamEventLogsEnabled(true)

	runtimeConfig := applyEffectiveSandboxEscapePolicy(config, sessionState.SandboxEscapePolicyOverride, commandSandboxEscapePolicy)
	runtimeConfig = applyEffectiveSkillReviewPolicy(runtimeConfig, sessionState.SkillReviewPolicyOverride)
	resolvedContextDir := resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, config.Workdir))
	skillConfig := a.resolveEffectiveSkillConfig(config, resolvedContextDir)
	requestedSkillNames, clientSkillPackages, err := loadSkillPayloadWithConfig(skillConfig)
	if err != nil {
		return sessionState, err
	}
	clientContextDir := resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, config.Workdir))
	commandExecutionTarget := "client"
	ensuredSessionID, err := clientruntime.EnsureSessionID(ctx, providerClient, sessionState.SessionID)
	if err != nil {
		return sessionState, err
	}
	sessionState.SessionID = ensuredSessionID
	stream, streamSession, err := providerClient.Stream(ctx, provider.StreamRequest{
		FlowID:                    flowID,
		Input:                     strings.TrimSpace(task),
		InputType:                 "any",
		Tweaks:                    buildTaskTweaks(config, clientContextDir, commandExecutionTarget),
		InputFiles:                append([]string(nil), inputFiles...),
		InternetSearch:            resolveInternetSearchEnabled(sessionState.InternetSearchOverride),
		OutputType:                "any",
		SessionID:                 sessionState.SessionID,
		RequestType:               normalizeRequestMode(sessionState.RequestMode),
		ReasoningEnabled:          resolveReasoningEnabled(config, sessionState.ReasoningOverride),
		AgentAutoLoopEnabled:      resolveAgentAutoLoopEnabled(config, sessionState, sessionState.RequestMode),
		AgentAutoLoopMaxRounds:    resolveAgentAutoLoopMaxRounds(config, sessionState, sessionState.RequestMode),
		CrossSessionMemoryEnabled: boolPtr(config.CrossSessionMemoryEnabled),
		ClientType:                provider.ClientTypeCLI,
		ClientContextDir:          clientContextDir,
		CommandExecutionTarget:    commandExecutionTarget,
		UserAgent:                 buildCLIUserAgent(),
		RequestedSkillNames:       requestedSkillNames,
		ClientMCPServers:          a.runtime.ClientMCPServersForMode(sessionState.RequestMode, runtimeConfig),
		ClientSkillPackages:       clientSkillPackages,
	})
	if err != nil {
		return sessionState, err
	}
	hasVisibleOutput := false
	lastVisibleOutputEndedWithNewline := false
	writeVisibleOutput := func(text string) {
		if text == "" {
			return
		}
		fmt.Fprint(a.stdout, text)
		hasVisibleOutput = true
		lastVisibleOutputEndedWithNewline = strings.HasSuffix(text, "\n")
	}
	renderState := cliStreamRenderState{}
	renderStreamingSuffix := isTerminalWriter(a.stdout)
	clearRenderedStreamingSuffix := func() {
		if clearIndicator := clearStreamingSuffixIfNeeded(renderStreamingSuffix, &renderState); clearIndicator != "" {
			fmt.Fprint(a.stdout, clearIndicator)
		}
	}
	defer clearRenderedStreamingSuffix()

	if streamSession != nil && streamSession.SessionID != "" {
		sessionState.SessionID = streamSession.SessionID
		sessionState.FlowID = flowID
		sessionState.FlowDisplayName = strings.TrimSpace(a.resolveFlowDisplayName(ctx, providerClient, flowID))
		title, titleErr := lookupSessionTitle(ctx, providerClient, flowID, streamSession.SessionID)
		if titleErr == nil {
			sessionState.SessionTitle = strings.TrimSpace(title)
		}
		sessionState.Workdir = resolveLocalWorkdir(config.Workdir)
		sessionState.ContextDir = resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, config.Workdir))
		sessionState.RequestMode = normalizeRequestMode(sessionState.RequestMode)
		if err := a.sessionStore.Save(sessionState); err != nil {
			return sessionState, err
		}
	}

	for event := range stream {
		switch event.Type {
		case provider.EventMessage:
			if sessionTitle := firstNonEmptyTrimmedStringMapValue(event.Data, "session_name"); sessionTitle != "" {
				sessionState.SessionTitle = sessionTitle
				if strings.TrimSpace(sessionState.SessionID) != "" {
					if err := a.sessionStore.Save(sessionState); err != nil {
						return sessionState, err
					}
				}
			}
			if splitAssistantMessage(event.Data) {
				renderState.splitNextAssistant = true
			}
			if chunk, ok := event.Data["chunk"].(string); ok && chunk != "" {
				chunk = sanitizeCLIStreamText(chunk)
				if renderState.splitNextAssistant && renderState.hasPrintedAssistant {
					chunk = strings.TrimLeft(chunk, "\r\n")
					if !lastVisibleOutputEndedWithNewline {
						chunk = "\n" + chunk
					}
				}
				chunk = renderState.renderChunk(chunk, showThinking)
				chunk = stripThinkControlTags(chunk)
				clearIndicator := ""
				if renderStreamingSuffix {
					clearIndicator = renderState.clearStreamingSuffix()
				}
				if !hasVisibleOutput {
					clearIndicator = ""
					chunk = strings.TrimLeft(chunk, "\r\n")
					if chunk == "" {
						continue
					}
				}
				if clearIndicator != "" {
					fmt.Fprint(a.stdout, clearIndicator)
				}
				writeVisibleOutput(chunk)
				renderState.hasPrintedAssistant = true
				renderState.splitNextAssistant = false
				if renderStreamingSuffix {
					fmt.Fprint(a.stdout, renderState.showStreamingSuffix())
				}
				continue
			}
			if showStreamEvents {
				if message, ok := event.Data["message"].(string); ok && strings.TrimSpace(message) != "" {
					message = sanitizeCLIStreamText(message)
					message = stripThinkControlTags(message)
					if strings.TrimSpace(message) == "" {
						continue
					}
					if clearIndicator := clearStreamingSuffixIfNeeded(renderStreamingSuffix, &renderState); clearIndicator != "" {
						fmt.Fprint(a.stdout, clearIndicator)
					}
					if hasVisibleOutput {
						writeVisibleOutput(fmt.Sprintf("\n[系统提示]: %s\n", strings.TrimSpace(message)))
					} else {
						writeVisibleOutput(fmt.Sprintf("[系统提示]: %s\n", strings.TrimSpace(message)))
					}
				}
			}
		case provider.EventError:
			normalizedErr := normalizeStreamEventError(event.Data["error"])
			if retryCount < 1 {
				missingSkillNames := detectMissingSkillNames(event.Data, normalizedErr)
				if len(missingSkillNames) > 0 {
					installedAny, installErr := a.autoInstallCandidates(
						skillConfig,
						&sessionState,
						task,
						requestedSkillNames,
						missingSkillNames,
						a.confirmSkillAutoInstall,
						func(text string) {
							if showStreamEvents && strings.TrimSpace(text) != "" {
								writeVisibleOutput(fmt.Sprintf("[系统提示]: %s\n", text))
							}
						},
					)
					if installErr != nil {
						return sessionState, installErr
					}
					if installedAny {
						if strings.TrimSpace(sessionState.FlowID) == "" {
							sessionState.FlowID = strings.TrimSpace(flowID)
						}
						if strings.TrimSpace(sessionState.Workdir) == "" {
							sessionState.Workdir = resolvedContextDir
						}
						if strings.TrimSpace(sessionState.ContextDir) == "" {
							sessionState.ContextDir = resolvedContextDir
						}
						if a.sessionStore != nil {
							if saveErr := a.sessionStore.Save(sessionState); saveErr != nil {
								return sessionState, saveErr
							}
						}
						if hasVisibleOutput {
							writeVisibleOutput("\n")
						}
						if showStreamEvents {
							writeVisibleOutput(fmt.Sprintf("[系统提示]: 检测到缺失技能 %s，已尝试自动补装并重试当前请求。\n", strings.Join(missingSkillNames, ", ")))
						}
						return a.streamTaskWithSkillRetry(
							ctx,
							providerClient,
							config,
							flowID,
							task,
							sessionState,
							commandSandboxEscapePolicy,
							showStreamEvents,
							showThinking,
							inputFiles,
							retryCount+1,
						)
					}
				}
			}
			if clearIndicator := clearStreamingSuffixIfNeeded(renderStreamingSuffix, &renderState); clearIndicator != "" {
				fmt.Fprint(a.stdout, clearIndicator)
			}
			if hasVisibleOutput {
				fmt.Fprintln(a.stderr)
			}
			return sessionState, normalizedErr
		case provider.EventBuiltIn:
			clearRenderedStreamingSuffix()
			if !modeAllowsBuiltIn(sessionState.RequestMode) {
				if err := submitClientCallFailure(ctx, providerClient, event.Data, fmt.Sprintf("当前%s不允许执行本地命令", requestModeLabel(sessionState.RequestMode))); err != nil {
					return sessionState, err
				}
				if showStreamEvents {
					fmt.Fprintf(a.stderr, "[系统提示]: 已拒绝 built_in，本地模式为 %s\n", requestModeLabel(sessionState.RequestMode))
				}
				continue
			}
			if err := a.runtime.HandleBuiltInEvent(ctx, providerClient, runtimeConfig, event.Data); err != nil {
				return sessionState, err
			}
		case provider.EventClientTool:
			clearRenderedStreamingSuffix()
			toolName := nestedStringValue(event.Data, "function", "name")
			if !modeAllowsClientTool(sessionState.RequestMode, toolName) {
				if err := submitClientCallFailure(ctx, providerClient, event.Data, fmt.Sprintf("当前%s不允许执行本地工具 %s", requestModeLabel(sessionState.RequestMode), toolName)); err != nil {
					return sessionState, err
				}
				if showStreamEvents {
					fmt.Fprintf(a.stderr, "[系统提示]: 已拒绝客户端工具 %s，本地模式为 %s\n", toolName, requestModeLabel(sessionState.RequestMode))
				}
				continue
			}
			eventRuntimeConfig := applyEffectiveSkillReviewForPayload(runtimeConfig, sessionState.ContextDir, sessionState, event.Data)
			if err := a.runtime.HandleClientToolEvent(ctx, providerClient, eventRuntimeConfig, event.Data); err != nil {
				return sessionState, err
			}
		case provider.EventToolCall:
			if !showStreamEvents {
				continue
			}
			if text, ok := event.Data["text"].(string); ok && text != "" {
				text = sanitizeCLIStreamText(text)
				text = stripThinkControlTags(text)
				if strings.TrimSpace(text) == "" {
					continue
				}
				if clearIndicator := clearStreamingSuffixIfNeeded(renderStreamingSuffix, &renderState); clearIndicator != "" {
					fmt.Fprint(a.stdout, clearIndicator)
				}
				if status := tuiSearchActivityStatus(text); status != "" {
					if hasVisibleOutput {
						writeVisibleOutput(fmt.Sprintf("\n[系统提示]: %s\n", status))
					} else {
						writeVisibleOutput(fmt.Sprintf("[系统提示]: %s\n", status))
					}
					continue
				}
				if hasVisibleOutput {
					writeVisibleOutput(fmt.Sprintf("\n[工具调用]: %s\n", text))
				} else {
					writeVisibleOutput(fmt.Sprintf("[工具调用]: %s\n", text))
				}
			}
		case provider.EventCompressionStart, provider.EventCompressionEnd:
			if !showStreamEvents {
				continue
			}
			text := sanitizeCLIStreamText(buildCompressionProgressText(event.Data))
			text = stripThinkControlTags(text)
			if text == "" {
				continue
			}
			if clearIndicator := clearStreamingSuffixIfNeeded(renderStreamingSuffix, &renderState); clearIndicator != "" {
				fmt.Fprint(a.stdout, clearIndicator)
			}
			if hasVisibleOutput {
				writeVisibleOutput(fmt.Sprintf("\n[上下文压缩]: %s\n", text))
			} else {
				writeVisibleOutput(fmt.Sprintf("[上下文压缩]: %s\n", text))
			}
		case provider.EventClose:
			if clearIndicator := clearStreamingSuffixIfNeeded(renderStreamingSuffix, &renderState); clearIndicator != "" {
				fmt.Fprint(a.stdout, clearIndicator)
			}
			if pending := renderState.flush(showThinking); pending != "" {
				if !hasVisibleOutput {
					pending = strings.TrimLeft(pending, "\r\n")
				}
				if pending != "" {
					writeVisibleOutput(pending)
				}
			}
			if hasVisibleOutput && !lastVisibleOutputEndedWithNewline {
				writeVisibleOutput("\n")
			}
		}
	}

	if clearIndicator := clearStreamingSuffixIfNeeded(renderStreamingSuffix, &renderState); clearIndicator != "" {
		fmt.Fprint(a.stdout, clearIndicator)
	}
	if pending := renderState.flush(showThinking); pending != "" {
		if !hasVisibleOutput {
			pending = strings.TrimLeft(pending, "\r\n")
		}
		if pending != "" {
			writeVisibleOutput(pending)
		}
	}

	if summary := loadLatestSearchResultsSummary(ctx, providerClient, flowID, sessionState.SessionID); summary != "" {
		writeVisibleOutput(summary + "\n")
	}
	return sessionState, nil
}

func tuiSearchActivityStatus(content string) string {
	lower := strings.ToLower(strings.TrimSpace(content))
	switch {
	case strings.Contains(lower, "internet"), strings.Contains(lower, "联网"), strings.Contains(lower, "bocha"), strings.Contains(lower, "tencent"):
		return "正在联网搜索..."
	case strings.Contains(lower, "personal"), strings.Contains(lower, "个人知识"):
		return "正在检索个人知识..."
	case strings.Contains(lower, "knowledge"), strings.Contains(lower, "知识库"), strings.Contains(lower, "milvus"), strings.Contains(lower, "document"):
		return "正在检索共享知识..."
	case strings.Contains(lower, "search"), strings.Contains(lower, "检索"):
		return "正在搜索..."
	default:
		return ""
	}
}

func (s *cliStreamRenderState) renderChunk(chunk string, showThinking bool) string {
	if chunk == "" {
		return chunk
	}

	input := s.pending + chunk
	s.pending = ""
	var builder strings.Builder

	for len(input) > 0 {
		if s.insideThink {
			closeIndex := strings.Index(input, "</think>")
			if closeIndex < 0 {
				fragment := trailingThinkTagFragment(input, "</think>")
				if showThinking {
					builder.WriteString(input[:len(input)-len(fragment)])
				}
				s.pending = fragment
				return s.renderVisibleChunk(builder.String())
			}
			if showThinking {
				builder.WriteString(input[:closeIndex])
			}
			input = input[closeIndex+len("</think>"):]
			s.insideThink = false
			continue
		}

		openIndex := strings.Index(input, "<think>")
		closeIndex := strings.Index(input, "</think>")
		if closeIndex >= 0 && (openIndex < 0 || closeIndex < openIndex) {
			builder.WriteString(input[:closeIndex])
			input = input[closeIndex+len("</think>"):]
			continue
		}
		if openIndex < 0 {
			fragment := trailingThinkTagFragment(input, "<think>", "</think>")
			builder.WriteString(input[:len(input)-len(fragment)])
			s.pending = fragment
			return s.renderVisibleChunk(builder.String())
		}

		builder.WriteString(input[:openIndex])
		input = input[openIndex+len("<think>"):]
		s.insideThink = true
	}

	return s.renderVisibleChunk(builder.String())
}

func (s *cliStreamRenderState) flush(showThinking bool) string {
	result := s.renderVisibleChunk("")
	s.controlResidue.flush()
	if showThinking || s.insideThink || s.pending == "" {
		s.pending = ""
		return result
	}
	s.pending = ""
	return result
}

func (s *cliStreamRenderState) renderVisibleChunk(chunk string) string {
	return s.controlResidue.consume(s.rewriteCitationChunk(chunk))
}

func trailingThinkTagFragment(content string, tags ...string) string {
	longest := ""
	for _, tag := range tags {
		maxLength := len(tag) - 1
		if maxLength <= 0 || content == "" {
			continue
		}
		if len(content) < maxLength {
			maxLength = len(content)
		}
		for length := maxLength; length > 0; length-- {
			suffix := content[len(content)-length:]
			if strings.HasPrefix(tag, suffix) {
				if len(suffix) > len(longest) {
					longest = suffix
				}
				break
			}
		}
	}
	return longest
}

func sanitizeCLIStreamText(content string) string {
	if content == "" {
		return ""
	}
	cleaned := cmdANSIOSCRegexp.ReplaceAllString(content, "")
	cleaned = cmdANSICSIRegexp.ReplaceAllString(cleaned, "")
	return strings.ReplaceAll(cleaned, "\x1b", "")
}

func stripThinkControlTags(content string) string {
	if content == "" {
		return ""
	}
	cleaned := strings.ReplaceAll(content, "<think>", "")
	return strings.ReplaceAll(cleaned, "</think>", "")
}

func isTerminalWriter(writer io.Writer) bool {
	fdTarget, ok := writer.(fdWriter)
	if !ok {
		return false
	}
	return isTerminalFD(int(fdTarget.Fd()))
}

func clearStreamingSuffixIfNeeded(enabled bool, state *cliStreamRenderState) string {
	if !enabled || state == nil {
		return ""
	}
	return state.clearStreamingSuffix()
}

func (s *cliStreamRenderState) rewriteCitationChunk(chunk string) string {
	input := s.citationPending + chunk
	s.citationPending = ""
	if input == "" {
		return ""
	}
	if fragment := trailingCLICitationFragment(input); fragment != "" {
		s.citationPending = fragment
		input = input[:len(input)-len(fragment)]
	}
	return rewriteCLICitationMarkers(input)
}

func trailingCLICitationFragment(content string) string {
	if content == "" {
		return ""
	}
	maxLength := len(content)
	if maxLength > 16 {
		maxLength = 16
	}
	for length := maxLength; length >= 1; length-- {
		suffix := content[len(content)-length:]
		if cliCitationExactRegexp.MatchString(suffix) {
			return ""
		}
		if cliCitationPrefixRegexp.MatchString(suffix) {
			return suffix
		}
	}
	return ""
}

func rewriteCLICitationMarkers(content string) string {
	if content == "" {
		return ""
	}
	return cliCitationRegexp.ReplaceAllStringFunc(content, func(match string) string {
		parts := cliCitationRegexp.FindStringSubmatch(match)
		prefix := parts[1]
		index := parts[2]
		if prefix == "" {
			prefix = parts[3]
			index = parts[4]
		}
		label := cliCitationSourceLabel(prefix)
		if label == "" {
			return match
		}
		return fmt.Sprintf("[%s#%s]", label, index)
	})
}

func cliCitationSourceLabel(prefix string) string {
	switch strings.TrimSpace(prefix) {
	case "1":
		return "联网"
	case "2":
		return "共享"
	case "3":
		return "个人"
	default:
		return ""
	}
}

func (s *cliStreamRenderState) showStreamingSuffix() string {
	s.streamingSuffix = "..."
	return s.streamingSuffix
}

func (s *cliStreamRenderState) clearStreamingSuffix() string {
	if s.streamingSuffix == "" {
		return ""
	}
	cleared := strings.Repeat("\b", len(s.streamingSuffix)) + strings.Repeat(" ", len(s.streamingSuffix)) + strings.Repeat("\b", len(s.streamingSuffix))
	s.streamingSuffix = ""
	return cleared
}

func (a *App) disableCLIFocusReporting() {
	if writeTerminalControlSequence(a.stdout, cliDisableFocusReporting) {
		return
	}
	writeTerminalControlSequence(a.stderr, cliDisableFocusReporting)
}

func writeTerminalControlSequence(writer io.Writer, sequence string) bool {
	fdTarget, ok := writer.(fdWriter)
	if !ok || !isTerminalFD(int(fdTarget.Fd())) {
		return false
	}
	_, _ = io.WriteString(writer, sequence)
	return true
}

func (a *App) printRootUsage() {
	fmt.Fprintln(a.stdout, "用法:")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli task [--agent AGENT] [--file PATH] [--mode agent|plan|ask]")
	fmt.Fprintln(a.stdout, "                     [--reasoning on|off] [--internet-search on|off]")
	fmt.Fprintln(a.stdout, "                     [--stream-retry-attempts 0-3] [--new] [--continue]")
	fmt.Fprintln(a.stdout, "                     [--show-stream-events] [--show-thinking]")
	fmt.Fprintln(a.stdout, "                     [--external-resource-access-policy allow|deny]")
	fmt.Fprintln(a.stdout, "                     [--session-id SESSION_ID]")
	fmt.Fprintln(a.stdout, "                     [任务描述]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli login [help|status|logout|password|qr]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli config [show|init|path|set|unset]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli browser [install|help]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli agents [list|current|use] [参数]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli memory [status|trigger] [flow=<智能体>] [参数]")
	fmt.Fprintf(a.stdout, "  %s\n", skillsCommandSynopsis())
	fmt.Fprintln(a.stdout, "  taiyiflow-cli doctor")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli about")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli version")
	fmt.Fprintf(a.stdout, "当前版本: %s\n", buildinfo.Version)
}

func (a *App) printInteractiveUsage(ctx context.Context, providerClient provider.Provider, flowDisplayName string, flowID string, sessionState agent.SessionState) {
	fmt.Fprintln(a.stdout, "会话内命令:")
	fmt.Fprintln(a.stdout, "  /agent    切换为执行模式，可写成 /agent <任务>")
	fmt.Fprintln(a.stdout, "  /agents   显示智能体切换说明")
	fmt.Fprintln(a.stdout, "  /config   查看或更新服务端与权限配置")
	fmt.Fprintln(a.stdout, "  /config session [show|set|unset] external_resource_access_policy|agent_auto_loop_max_rounds [值]   查看或设置会话级运行策略")
	fmt.Fprintln(a.stdout, "  /about    显示当前客户端版本、组织与后端对接说明")
	fmt.Fprintln(a.stdout, "  /review   发起代码审查，可写成 /review [路径或范围说明]")
	fmt.Fprintln(a.stdout, "  /tasks    查看后台任务，可写成 /tasks [list|output|capture|stop|remove] <task_id> 或 /tasks cleanup")
	fmt.Fprintln(a.stdout, "  /memory   查看或触发跨会话记忆整合，可写成 /memory [status|trigger] [flow=<智能体>] [参数]")
	fmt.Fprintln(a.stdout, "  /skills   管理本地技能与远程仓库技能，可写成 /skills [list|view|search|view-remote|install|remove|update|source|enable|disable|help]")
	fmt.Fprintln(a.stdout, "  /reasoning   切换思考模式，可写成 /reasoning [on|off|default|status]")
	fmt.Fprintln(a.stdout, "  /ask      切换为问答模式，可写成 /ask <问题>")
	fmt.Fprintln(a.stdout, "  /plan     切换为计划模式，可写成 /plan <目标>")
	fmt.Fprintln(a.stdout, "  /help     显示帮助")
	fmt.Fprintln(a.stdout, "  /session  显示当前会话状态")
	fmt.Fprintln(a.stdout, "  /sessions 显示本地已保存会话")
	fmt.Fprintln(a.stdout, "  /new      清屏并重置当前智能体的本地会话")
	fmt.Fprintln(a.stdout, "  /clear    清屏")
	fmt.Fprintln(a.stdout, "  /exit     退出")
	fmt.Fprintln(a.stdout, "  启动参数: --reasoning on|off 可设置本次会话初始思考模式；--internet-search on|off 可设置本次会话联网搜索；--stream-retry-attempts 0-3 可覆盖本次任务流失败重试次数；--show-stream-events 可显示系统提示与工具调用日志；--show-thinking 可显示思考内容（不输出 <think></think> 标签）")
	fmt.Fprintln(a.stdout, "  提示: 交互模式下请使用 `taiyiflow-cli agents` 管理默认智能体。")
	a.printSessionStatus(ctx, providerClient, flowDisplayName, flowID, sessionState)
}

func resolveInternetSearchEnabled(override string) bool {
	return strings.EqualFold(strings.TrimSpace(override), "on")
}

func (a *App) printSessionStatus(ctx context.Context, providerClient provider.Provider, flowDisplayName string, flowID string, sessionState agent.SessionState) {
	resolvedFlowName := strings.TrimSpace(flowDisplayName)
	if resolvedFlowName == "" {
		resolvedFlowName = strings.TrimSpace(sessionState.FlowDisplayName)
	}
	if resolvedFlowName == "" {
		resolvedFlowName = sanitizeFlowDisplay(flowID)
	}
	resolvedFlowID := a.resolveFlowIdentifier(ctx, providerClient, flowID)
	fmt.Fprintf(a.stdout, "当前智能体: %s\n", resolvedFlowName)
	if strings.TrimSpace(sessionState.SessionID) == "" {
		fmt.Fprintf(a.stdout, "当前模式: %s\n", requestModeLabel(sessionState.RequestMode))
		fmt.Fprintln(a.stdout, "当前会话: 新会话（未同步服务端）")
		return
	}
	fmt.Fprintf(a.stdout, "当前模式: %s\n", requestModeLabel(sessionState.RequestMode))
	resolvedSessionID := strings.TrimSpace(sessionState.SessionID)
	if title, err := lookupSessionTitle(ctx, providerClient, resolvedFlowID, sessionState.SessionID); err == nil && strings.TrimSpace(title) != "" {
		fmt.Fprintf(a.stdout, "当前会话: %s\n", strings.TrimSpace(title))
		fmt.Fprintf(a.stdout, "会话ID: %s\n", resolvedSessionID)
	} else if strings.TrimSpace(sessionState.SessionTitle) != "" {
		fmt.Fprintf(a.stdout, "当前会话: %s\n", strings.TrimSpace(sessionState.SessionTitle))
		fmt.Fprintf(a.stdout, "会话ID: %s\n", resolvedSessionID)
	} else {
		fmt.Fprintf(a.stdout, "当前会话: %s\n", resolvedSessionID)
	}
	preview := loadSessionMemoryPreview(ctx, providerClient, resolvedFlowID, resolvedSessionID)
	if line := buildSessionMemoryTierLine(preview); line != "" {
		fmt.Fprintln(a.stdout, line)
	}
}

func (a *App) printSavedSessions(currentFlowID string, currentSessionID string, currentWorkdir string) error {
	states, err := a.sessionStore.List()
	if err != nil {
		return err
	}
	if len(states) == 0 {
		fmt.Fprintln(a.stdout, "当前没有已保存的本地会话。")
		return nil
	}

	resolvedWorkdir := resolveLocalWorkdir(currentWorkdir)
	fmt.Fprintln(a.stdout, "本地已保存会话:")
	for _, state := range states {
		flowLabel := strings.TrimSpace(state.FlowDisplayName)
		if flowLabel == "" {
			flowLabel = sanitizeFlowDisplay(state.FlowID)
		}
		line := fmt.Sprintf("- %s", flowLabel)
		if strings.TrimSpace(state.SessionID) != "" {
			title := strings.TrimSpace(state.SessionTitle)
			if title == "" {
				title = state.SessionID
			}
			line += fmt.Sprintf(" | 会话 %s", title)
		} else {
			line += " | 新会话（未同步服务端）"
		}
		if strings.TrimSpace(state.Workdir) != "" {
			line += fmt.Sprintf(" | 目录 %s", state.Workdir)
		}
		line += fmt.Sprintf(" | 模式 %s", requestModeLabel(state.RequestMode))
		if !state.UpdatedAt.IsZero() {
			line += fmt.Sprintf(" | 更新时间 %s", state.UpdatedAt.Format("2006-01-02 15:04:05"))
		}
		if state.FlowID == currentFlowID {
			line += " | 当前智能体"
		}
		if strings.TrimSpace(state.Workdir) != "" && state.Workdir == resolvedWorkdir {
			line += " | 当前目录"
		}
		if strings.TrimSpace(currentSessionID) != "" && state.SessionID == currentSessionID {
			line += " | 当前会话"
		}
		fmt.Fprintln(a.stdout, line)
	}
	return nil
}

func resolveFlowID(inputFlow string, defaultFlow string) (string, error) {
	flowID := strings.TrimSpace(firstNonEmpty(inputFlow, defaultFlow))
	if flowID == "" {
		return "", errors.New("未配置默认智能体，请先执行 `taiyiflow-cli agents` 查看可用智能体，再执行 `taiyiflow-cli agents use <名称>` 或使用 `--agent` 指定")
	}
	return flowID, nil
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func uploadInputFile(
	ctx context.Context,
	providerClient provider.Provider,
	flowID string,
	sessionID string,
	baseDir string,
	localPath string,
) (string, error) {
	return clientruntime.UploadInputFile(ctx, providerClient, flowID, sessionID, baseDir, localPath)
}

func boolPtr(value bool) *bool {
	return &value
}

func buildTaskEnvironmentHint(contextDir string, commandExecutionTarget string) string {
	resolvedContextDir := resolveLocalWorkdir(contextDir)
	resolvedTarget := strings.TrimSpace(commandExecutionTarget)
	if resolvedTarget == "" {
		resolvedTarget = "client"
	}
	lines := []string{
		"<taiyiflow_environment>",
		fmt.Sprintf("OS: %s", runtime.GOOS),
		fmt.Sprintf("ARCH: %s", runtime.GOARCH),
		fmt.Sprintf("工作目录: %s", resolvedContextDir),
		fmt.Sprintf("命令执行目标: %s", resolvedTarget),
	}
	lines = append(lines, "</taiyiflow_environment>")
	return strings.Join(lines, "\n")
}

func buildTaskTweaks(config internalconfig.Config, contextDir string, commandExecutionTarget string) map[string]any {
	return map[string]any{
		"taiyiflow_environment": buildTaskEnvironmentHint(contextDir, commandExecutionTarget),
	}
}

func splitAssistantMessage(payload map[string]any) bool {
	value, ok := payload["split_assistant_message"]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func (a *App) runInteractiveConfigCommand(command string) error {
	return a.runConfig(parseSlashConfigArgs(command))
}

func (a *App) runInteractiveSessionConfigCommand(command string, state *agent.SessionState) (string, error) {
	args := parseSlashConfigArgs(command)
	if len(args) < 2 || strings.ToLower(strings.TrimSpace(args[0])) != "session" {
		return "", fmt.Errorf("用法: /config session [show|set|unset] <external_resource_access_policy|agent_auto_loop_max_rounds> [value]")
	}
	action := strings.ToLower(strings.TrimSpace(args[1]))
	key := ""
	if len(args) >= 3 {
		key = normalizeConfigKey(args[2])
	}
	if key != "" && key != "external_resource_access_policy" && key != "agent_auto_loop_max_rounds" {
		return "", fmt.Errorf("会话配置仅支持 external_resource_access_policy、agent_auto_loop_max_rounds")
	}
	switch action {
	case "show":
		if key == "external_resource_access_policy" {
			value := strings.TrimSpace(state.SandboxEscapePolicyOverride)
			if value == "" {
				value = "<继承全局配置>"
			}
			return fmt.Sprintf("会话外部资源访问策略: %s", value), nil
		}
		if key == "agent_auto_loop_max_rounds" {
			if state.AgentAutoLoopMaxRoundsOverride == nil {
				return "会话智能体自动续跑最大轮数: <继承全局配置>", nil
			}
			return fmt.Sprintf("会话智能体自动续跑最大轮数: %d", *state.AgentAutoLoopMaxRoundsOverride), nil
		}
		policy := strings.TrimSpace(state.SandboxEscapePolicyOverride)
		if policy == "" {
			policy = "<继承全局配置>"
		}
		maxRounds := "<继承全局配置>"
		if state.AgentAutoLoopMaxRoundsOverride != nil {
			maxRounds = strconv.Itoa(*state.AgentAutoLoopMaxRoundsOverride)
		}
		return fmt.Sprintf("会话外部资源访问策略: %s\n会话智能体自动续跑最大轮数: %s", policy, maxRounds), nil
	case "set":
		if len(args) < 4 {
			return "", fmt.Errorf("用法: /config session set <external_resource_access_policy|agent_auto_loop_max_rounds> <value>")
		}
		switch key {
		case "external_resource_access_policy":
			policy := internalconfig.NormalizeSandboxEscapePolicy(args[3])
			if strings.ToLower(strings.TrimSpace(args[3])) != policy {
				return "", fmt.Errorf("external_resource_access_policy 仅支持 confirm、deny、allow")
			}
			state.SandboxEscapePolicyOverride = policy
		case "agent_auto_loop_max_rounds":
			rounds, err := strconv.Atoi(strings.TrimSpace(args[3]))
			if err != nil || rounds < 0 || rounds > internalconfig.MaxAgentAutoLoopMaxRounds {
				return "", fmt.Errorf("agent_auto_loop_max_rounds 仅支持 0-%d 的整数，0 表示禁用", internalconfig.MaxAgentAutoLoopMaxRounds)
			}
			state.AgentAutoLoopMaxRoundsOverride = &rounds
		default:
			return "", fmt.Errorf("用法: /config session set <external_resource_access_policy|agent_auto_loop_max_rounds> <value>")
		}
		if err := a.sessionStore.Save(*state); err != nil {
			return "", err
		}
		switch key {
		case "external_resource_access_policy":
			return fmt.Sprintf("已设置会话外部资源访问策略: %s", state.SandboxEscapePolicyOverride), nil
		default:
			return fmt.Sprintf("已设置会话智能体自动续跑最大轮数: %d", *state.AgentAutoLoopMaxRoundsOverride), nil
		}
	case "unset", "clear":
		switch key {
		case "external_resource_access_policy":
			state.SandboxEscapePolicyOverride = ""
		case "agent_auto_loop_max_rounds":
			state.AgentAutoLoopMaxRoundsOverride = nil
		default:
			return "", fmt.Errorf("用法: /config session unset <external_resource_access_policy|agent_auto_loop_max_rounds>")
		}
		if err := a.sessionStore.Save(*state); err != nil {
			return "", err
		}
		switch key {
		case "external_resource_access_policy":
			return "已清空会话外部资源访问策略，恢复继承全局配置", nil
		default:
			return "已清空会话智能体自动续跑最大轮数，恢复继承全局配置", nil
		}
	default:
		return "", fmt.Errorf("会话配置操作仅支持 show、set、unset")
	}
}

func parseSlashConfigArgs(command string) []string {
	trimmed := strings.TrimSpace(command)
	if !strings.HasPrefix(strings.ToLower(trimmed), "/config") {
		return nil
	}
	remainder := strings.TrimSpace(trimmed[len("/config"):])
	if remainder == "" {
		return nil
	}
	return strings.Fields(remainder)
}

func isMutatingSlashConfigCommand(command string) bool {
	args := parseSlashConfigArgs(command)
	if len(args) == 0 {
		return false
	}
	if strings.ToLower(strings.TrimSpace(args[0])) == "session" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "init", "set", "unset", "clear":
		return true
	default:
		return false
	}
}

func resolveEffectiveSandboxEscapePolicy(config internalconfig.Config, sessionOverride string, commandOverride string) string {
	if strings.TrimSpace(sessionOverride) != "" {
		return internalconfig.NormalizeSandboxEscapePolicy(sessionOverride)
	}
	if strings.TrimSpace(commandOverride) != "" {
		return internalconfig.NormalizeSandboxEscapePolicy(commandOverride)
	}
	if strings.TrimSpace(config.SandboxEscapePolicy) != "" {
		return internalconfig.NormalizeSandboxEscapePolicy(config.SandboxEscapePolicy)
	}
	return internalconfig.Default().SandboxEscapePolicy
}

func applyEffectiveSandboxEscapePolicy(config internalconfig.Config, sessionOverride string, commandOverride string) internalconfig.Config {
	updated := config
	updated.SandboxEscapePolicy = resolveEffectiveSandboxEscapePolicy(config, sessionOverride, commandOverride)
	return updated
}

func applyEffectiveSkillReviewPolicy(config internalconfig.Config, sessionOverride string) internalconfig.Config {
	updated := config
	if strings.TrimSpace(sessionOverride) != "" {
		updated.SkillReviewPolicy = internalconfig.NormalizeSkillReviewPolicy(sessionOverride)
	} else {
		updated.SkillReviewPolicy = internalconfig.NormalizeSkillReviewPolicy(updated.SkillReviewPolicy)
	}
	updated.WriteConfirmationPolicy = internalconfig.NormalizeWriteConfirmationPolicy(updated.WriteConfirmationPolicy)
	return updated
}

func lookupSessionTitle(ctx context.Context, providerClient provider.Provider, flowID string, sessionID string) (string, error) {
	if providerClient == nil {
		return "", nil
	}
	trimmedFlowID := strings.TrimSpace(flowID)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedFlowID == "" || trimmedSessionID == "" {
		return "", nil
	}

	const pageSize = 100
	offset := 0
	for {
		sessions, err := providerClient.ListSessions(ctx, trimmedFlowID, offset, pageSize)
		if err != nil {
			return "", err
		}
		if sessions == nil || len(sessions.Items) == 0 {
			return "", nil
		}
		for _, item := range sessions.Items {
			if strings.TrimSpace(item.ID) == trimmedSessionID {
				return strings.TrimSpace(item.Name), nil
			}
		}
		offset += len(sessions.Items)
		if sessions.Total > 0 && offset >= sessions.Total {
			return "", nil
		}
		if len(sessions.Items) < pageSize {
			return "", nil
		}
	}
}
