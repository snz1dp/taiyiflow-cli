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
	isTerminalFD             = term.IsTerminal
)

type fdWriter interface {
	Fd() uintptr
}

type App struct {
	configManager *internalconfig.Manager
	sessionStore  *agent.SessionStore
	stdout        io.Writer
	stderr        io.Writer
	stdin         io.Reader
	runtime       *clientruntime.ClientRuntime
}

type rootOptions struct {
	Flow                string
	NewSession          bool
	Continue            bool
	ShowStreamEvents    bool
	SandboxEscapePolicy string
}

func buildProviderClient(config internalconfig.Config) *providerclient.Client {
	client := providerclient.NewClient(config.BackendBaseURL, config.APIKey)
	client.SetStreamIdleTimeoutSeconds(config.StreamIdleTimeoutSeconds)
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
	case "skills":
		return a.runSkills(args[1:])
	case "cli":
		return errors.New("`cli` 子命令已移除，请改用 `taiyiflow-cli task`")
	case "config":
		if len(args) == 1 {
			return a.runTUI(ctx, []string{"/config"})
		}
		return a.runConfig(args[1:])
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
	flagSet.StringVar(&options.Flow, "flow", "", "兼容旧参数：指定要运行的智能体 ID 或名称")
	flagSet.BoolVar(&options.NewSession, "new", false, "忽略已有会话并新建会话")
	flagSet.BoolVar(&options.Continue, "continue", false, "继续当前会话")
	flagSet.BoolVar(&options.ShowStreamEvents, "show-stream-events", false, "显示系统提示与工具调用等流式调试信息")
	flagSet.StringVar(&options.SandboxEscapePolicy, "external-resource-access-policy", "", "外部资源访问策略（confirm|deny|allow），仅影响当前命令")
	flagSet.StringVar(&options.SandboxEscapePolicy, "sandbox-escape-policy", "", "兼容旧参数：外部资源访问策略（confirm|deny|allow），仅影响当前命令")

	if err := flagSet.Parse(args); err != nil {
		return err
	}

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
	providerClient := buildProviderClient(config)
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

	sessionState, err := a.prepareSession(flowID, options.NewSession, workdir, "")
	if err != nil {
		return err
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
	fmt.Fprintln(a.stdout, "进入交互模式，输入 /ask、/plan、/agent 切换请求模式，输入 /reasoning 调整思考模式，输入 /review 发起代码审查。")

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
			updatedState, err := a.streamTask(ctx, providerClient, config, flowID, prompt, sessionState, options.SandboxEscapePolicy, options.ShowStreamEvents)
			if err != nil {
				fmt.Fprintf(a.stderr, "执行审查失败: %v\n", err)
				continue
			}
			sessionState = updatedState
			continue
		case line == "/tasks" || strings.HasPrefix(strings.ToLower(line), "/tasks "):
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
				FlowID:                      flowID,
				FlowDisplayName:             flowDisplayName,
				Workdir:                     workdir,
				RequestMode:                 provider.RequestTypeAgent,
				ContextDir:                  workdir,
				SandboxEscapePolicyOverride: sessionState.SandboxEscapePolicyOverride,
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
					FlowID:                      flowID,
					FlowDisplayName:             flowDisplayName,
					Workdir:                     workdir,
					RequestMode:                 firstNonEmpty(sessionState.RequestMode, provider.RequestTypeAgent),
					SandboxEscapePolicyOverride: sessionState.SandboxEscapePolicyOverride,
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
			updatedState, err := a.streamTask(ctx, providerClient, config, flowID, prompt, sessionState, options.SandboxEscapePolicy, options.ShowStreamEvents)
			if err != nil {
				fmt.Fprintf(a.stderr, "执行任务失败: %v\n", err)
				continue
			}
			sessionState = updatedState
			continue
		}

		updatedState, err := a.streamTask(ctx, providerClient, config, flowID, line, sessionState, options.SandboxEscapePolicy, options.ShowStreamEvents)
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

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	providerClient := buildProviderClient(config)
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

	sessionState, err := a.prepareSession(flowID, options.NewSession, workdir, "")
	if err != nil {
		return err
	}

	_, err = a.streamTask(ctx, providerClient, config, flowID, task, sessionState, options.SandboxEscapePolicy, options.ShowStreamEvents)
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

func (a *App) streamTask(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
	task string,
	sessionState agent.SessionState,
	commandSandboxEscapePolicy string,
	showStreamEvents bool,
) (agent.SessionState, error) {
	a.runtime.SetStreamEventLogsEnabled(showStreamEvents)
	defer a.runtime.SetStreamEventLogsEnabled(true)

	runtimeConfig := applyEffectiveSandboxEscapePolicy(config, sessionState.SandboxEscapePolicyOverride, commandSandboxEscapePolicy)
	requestedSkillNames, clientSkillPackages, err := a.loadSkillPayload(config)
	if err != nil {
		return sessionState, err
	}
	stream, streamSession, err := providerClient.Stream(ctx, provider.StreamRequest{
		FlowID:                 flowID,
		Input:                  task,
		InputType:              "any",
		OutputType:             "any",
		SessionID:              sessionState.SessionID,
		RequestType:            normalizeRequestMode(sessionState.RequestMode),
		ReasoningEnabled:       resolveReasoningEnabled(config, sessionState.ReasoningOverride),
		DeviceType:             provider.DeviceTypeCLI,
		ClientContextDir:       resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, config.Workdir)),
		CommandExecutionTarget: "client",
		UserAgent:              buildCLIUserAgent(),
		RequestedSkillNames:    requestedSkillNames,
		ClientMCPServers:       a.runtime.ClientMCPServersForMode(sessionState.RequestMode),
		ClientSkillPackages:    clientSkillPackages,
	})
	if err != nil {
		return sessionState, err
	}
	hasVisibleOutput := false

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
			if chunk, ok := event.Data["chunk"].(string); ok && chunk != "" {
				chunk = sanitizeCLIStreamText(chunk)
				if !hasVisibleOutput {
					chunk = strings.TrimLeft(chunk, "\r\n")
					if chunk == "" {
						continue
					}
				}
				fmt.Fprint(a.stdout, chunk)
				hasVisibleOutput = true
				continue
			}
			if showStreamEvents {
				if message, ok := event.Data["message"].(string); ok && strings.TrimSpace(message) != "" {
					message = sanitizeCLIStreamText(message)
					if strings.TrimSpace(message) == "" {
						continue
					}
					if hasVisibleOutput {
						fmt.Fprintf(a.stdout, "\n[系统提示]: %s\n", strings.TrimSpace(message))
					} else {
						fmt.Fprintf(a.stdout, "[系统提示]: %s\n", strings.TrimSpace(message))
					}
					hasVisibleOutput = true
				}
			}
		case provider.EventError:
			if hasVisibleOutput {
				fmt.Fprintln(a.stderr)
			}
			return sessionState, normalizeStreamEventError(event.Data["error"])
		case provider.EventBuiltIn:
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
			if err := a.runtime.HandleClientToolEvent(ctx, providerClient, runtimeConfig, event.Data); err != nil {
				return sessionState, err
			}
		case provider.EventToolCall:
			if showStreamEvents {
				if text, ok := event.Data["text"].(string); ok && text != "" {
					text = sanitizeCLIStreamText(text)
					if strings.TrimSpace(text) == "" {
						continue
					}
					fmt.Fprintf(a.stdout, "[工具调用]: %s\n", text)
					hasVisibleOutput = true
				}
			}
		case provider.EventClose:
			fmt.Fprintln(a.stdout)
		}
	}

	return sessionState, nil
}

func sanitizeCLIStreamText(content string) string {
	if content == "" {
		return ""
	}
	cleaned := cmdANSIOSCRegexp.ReplaceAllString(content, "")
	cleaned = cmdANSICSIRegexp.ReplaceAllString(cleaned, "")
	return strings.ReplaceAll(cleaned, "\x1b", "")
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
	fmt.Fprintln(a.stdout, "  taiyiflow-cli task [--agent AGENT] [--new] [--continue] [--show-stream-events] [--external-resource-access-policy POLICY] [任务描述]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli config [show|init|path|set|unset]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli doctor")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli agents [list|current|use] [参数]")
	fmt.Fprintf(a.stdout, "  %s\n", skillsCommandSynopsis())
	fmt.Fprintln(a.stdout, "  taiyiflow-cli version")
	fmt.Fprintf(a.stdout, "当前版本: %s\n", buildinfo.Version)
}

func (a *App) printInteractiveUsage(ctx context.Context, providerClient provider.Provider, flowDisplayName string, flowID string, sessionState agent.SessionState) {
	fmt.Fprintln(a.stdout, "会话内命令:")
	fmt.Fprintln(a.stdout, "  /agent    切换为执行模式，可写成 /agent <任务>")
	fmt.Fprintln(a.stdout, "  /agents   显示智能体切换说明")
	fmt.Fprintln(a.stdout, "  /config   查看或更新服务端与权限配置")
	fmt.Fprintln(a.stdout, "  /config session [show|set|unset] external_resource_access_policy [值]   查看或设置会话外部资源访问策略")
	fmt.Fprintln(a.stdout, "  /review   发起代码审查，可写成 /review [路径或范围说明]")
	fmt.Fprintln(a.stdout, "  /tasks    查看后台任务，可写成 /tasks [list|output|capture|stop|remove] <task_id> 或 /tasks cleanup")
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
	fmt.Fprintln(a.stdout, "  启动参数: --show-stream-events 可显示系统提示与工具调用日志")
	fmt.Fprintln(a.stdout, "  提示: 交互模式下请使用 `taiyiflow-cli agents` 管理默认智能体。")
	a.printSessionStatus(ctx, providerClient, flowDisplayName, flowID, sessionState)
}

func (a *App) printSessionStatus(ctx context.Context, providerClient provider.Provider, flowDisplayName string, flowID string, sessionState agent.SessionState) {
	resolvedFlowName := strings.TrimSpace(flowDisplayName)
	if resolvedFlowName == "" {
		resolvedFlowName = strings.TrimSpace(sessionState.FlowDisplayName)
	}
	if resolvedFlowName == "" {
		resolvedFlowName = sanitizeFlowDisplay(flowID)
	}
	fmt.Fprintf(a.stdout, "当前智能体: %s\n", resolvedFlowName)
	if strings.TrimSpace(sessionState.SessionID) == "" {
		fmt.Fprintf(a.stdout, "当前模式: %s\n", requestModeLabel(sessionState.RequestMode))
		fmt.Fprintln(a.stdout, "当前会话: 新会话（未同步服务端）")
		return
	}
	fmt.Fprintf(a.stdout, "当前模式: %s\n", requestModeLabel(sessionState.RequestMode))
	if title, err := lookupSessionTitle(ctx, providerClient, flowID, sessionState.SessionID); err == nil && strings.TrimSpace(title) != "" {
		fmt.Fprintf(a.stdout, "当前会话: %s\n", strings.TrimSpace(title))
		fmt.Fprintf(a.stdout, "会话ID: %s\n", sessionState.SessionID)
		return
	}
	if strings.TrimSpace(sessionState.SessionTitle) != "" {
		fmt.Fprintf(a.stdout, "当前会话: %s\n", strings.TrimSpace(sessionState.SessionTitle))
		fmt.Fprintf(a.stdout, "会话ID: %s\n", sessionState.SessionID)
		return
	}
	fmt.Fprintf(a.stdout, "当前会话: %s\n", sessionState.SessionID)
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

func (a *App) runInteractiveConfigCommand(command string) error {
	return a.runConfig(parseSlashConfigArgs(command))
}

func (a *App) runInteractiveSessionConfigCommand(command string, state *agent.SessionState) (string, error) {
	args := parseSlashConfigArgs(command)
	if len(args) < 2 || strings.ToLower(strings.TrimSpace(args[0])) != "session" {
		return "", fmt.Errorf("用法: /config session [show|set|unset] external_resource_access_policy [confirm|deny|allow]")
	}
	action := strings.ToLower(strings.TrimSpace(args[1]))
	key := ""
	if len(args) >= 3 {
		key = normalizeConfigKey(args[2])
	}
	if key != "" && key != "external_resource_access_policy" {
		return "", fmt.Errorf("会话配置仅支持 external_resource_access_policy")
	}
	switch action {
	case "show":
		value := strings.TrimSpace(state.SandboxEscapePolicyOverride)
		if value == "" {
			value = "<继承全局配置>"
		}
		return fmt.Sprintf("会话外部资源访问策略: %s", value), nil
	case "set":
		if key != "external_resource_access_policy" || len(args) < 4 {
			return "", fmt.Errorf("用法: /config session set external_resource_access_policy <confirm|deny|allow>")
		}
		policy := internalconfig.NormalizeSandboxEscapePolicy(args[3])
		if strings.ToLower(strings.TrimSpace(args[3])) != policy {
			return "", fmt.Errorf("external_resource_access_policy 仅支持 confirm、deny、allow")
		}
		state.SandboxEscapePolicyOverride = policy
		if err := a.sessionStore.Save(*state); err != nil {
			return "", err
		}
		return fmt.Sprintf("已设置会话外部资源访问策略: %s", policy), nil
	case "unset", "clear":
		if key != "external_resource_access_policy" {
			return "", fmt.Errorf("用法: /config session unset external_resource_access_policy")
		}
		state.SandboxEscapePolicyOverride = ""
		if err := a.sessionStore.Save(*state); err != nil {
			return "", err
		}
		return "已清空会话外部资源访问策略，恢复继承全局配置", nil
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
