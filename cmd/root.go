package cmd

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
)

type App struct {
	configManager *internalconfig.Manager
	sessionStore  *agent.SessionStore
	stdout        io.Writer
	stderr        io.Writer
	stdin         io.Reader
	runtime       *clientruntime.ClientRuntime
}

type rootOptions struct {
	Flow       string
	NewSession bool
	Continue   bool
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
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	providerClient := providerclient.NewClient(config.BackendBaseURL, config.APIKey)
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

	sessionState, err := a.prepareSession(flowID, options.NewSession, workdir)
	if err != nil {
		return err
	}
	flowDisplayName := a.resolveFlowDisplayName(ctx, providerClient, flowID)

	metadata, err := providerClient.GetMetadata(ctx, flowID)
	if err == nil && metadata.WelcomeMessage != "" {
		fmt.Fprintln(a.stdout, metadata.WelcomeMessage)
	}
	if err == nil && strings.TrimSpace(metadata.Name) != "" {
		flowDisplayName = metadata.Name
	}
	if err != nil {
		fmt.Fprintf(a.stderr, "提示: 获取元信息失败，继续进入交互模式: %v\n", err)
	}

	reader := bufio.NewScanner(a.stdin)
	fmt.Fprintln(a.stdout, "进入交互模式，输入 /config 管理服务端配置，输入 /skills 查看本地技能，输入 /exit 退出，输入 /new 新建会话。")

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
		case line == "/skills" || strings.HasPrefix(strings.ToLower(line), "/skills "):
			output, err := a.executeSkillsCommand(parseSlashSkillsArgs(line))
			if err != nil {
				fmt.Fprintf(a.stderr, "技能命令执行失败: %v\n", err)
				continue
			}
			fmt.Fprintln(a.stdout, output)
			continue
		case line == "/agents":
			fmt.Fprintln(a.stdout, "请使用 `taiyiflow-cli agents` 查看或切换默认智能体。")
			continue
		case line == "/help":
			a.printInteractiveUsage(ctx, providerClient, flowDisplayName, flowID, sessionState)
			continue
		case line == "/clear":
			fmt.Fprint(a.stdout, "\033[H\033[2J")
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
			sessionState = agent.SessionState{FlowID: flowID, Workdir: workdir}
			if err := a.sessionStore.Delete(flowID, workdir); err != nil {
				return err
			}
			fmt.Fprintln(a.stdout, "已重置本地会话。")
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
			providerClient = providerclient.NewClient(config.BackendBaseURL, config.APIKey)
			workdir = resolveLocalWorkdir(config.Workdir)

			if err := a.ensureBackendAvailable(ctx, providerClient, config.BackendBaseURL); err != nil {
				flowID = strings.TrimSpace(firstNonEmpty(options.Flow, config.DefaultFlow))
				flowDisplayName = a.resolveFlowDisplayName(ctx, providerClient, flowID)
				sessionState = agent.SessionState{FlowID: flowID, Workdir: workdir}
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
			sessionState, err = a.prepareSession(flowID, false, workdir)
			if err != nil {
				return err
			}
			continue
		}

		updatedState, err := a.streamTask(ctx, providerClient, config, flowID, line, sessionState)
		if err != nil {
			fmt.Fprintf(a.stderr, "执行任务失败: %v\n", err)
			continue
		}
		sessionState = updatedState
	}
}

func (a *App) runTaskOnce(ctx context.Context, options rootOptions, task string) error {
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	providerClient := providerclient.NewClient(config.BackendBaseURL, config.APIKey)
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

	sessionState, err := a.prepareSession(flowID, options.NewSession, workdir)
	if err != nil {
		return err
	}

	_, err = a.streamTask(ctx, providerClient, config, flowID, task, sessionState)
	return err
}

func (a *App) prepareSession(flowID string, newSession bool, workdir string) (agent.SessionState, error) {
	resolvedWorkdir := resolveLocalWorkdir(workdir)
	if newSession {
		if err := a.sessionStore.Delete(flowID, resolvedWorkdir); err != nil {
			return agent.SessionState{}, err
		}
		return agent.SessionState{FlowID: flowID, Workdir: resolvedWorkdir}, nil
	}
	return a.sessionStore.Load(flowID, resolvedWorkdir)
}

func (a *App) streamTask(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
	task string,
	sessionState agent.SessionState,
) (agent.SessionState, error) {
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
		RequestType:            "agent",
		CommandExecutionTarget: "client",
		RequestedSkillNames:    requestedSkillNames,
		ClientMCPServers:       a.runtime.ClientMCPServers(),
		ClientSkillPackages:    clientSkillPackages,
	})
	if err != nil {
		return sessionState, err
	}

	if streamSession != nil && streamSession.SessionID != "" {
		sessionState.SessionID = streamSession.SessionID
		sessionState.FlowID = flowID
		sessionState.Workdir = resolveLocalWorkdir(config.Workdir)
		if err := a.sessionStore.Save(sessionState); err != nil {
			return sessionState, err
		}
	}

	for event := range stream {
		switch event.Type {
		case provider.EventMessage:
			if chunk, ok := event.Data["chunk"].(string); ok && chunk != "" {
				fmt.Fprint(a.stdout, chunk)
				continue
			}
			if message, ok := event.Data["message"].(string); ok && strings.TrimSpace(message) != "" {
				fmt.Fprintf(a.stdout, "\n[进度] %s\n", strings.TrimSpace(message))
			}
		case provider.EventError:
			fmt.Fprintln(a.stderr)
			return sessionState, fmt.Errorf("后端返回错误: %v", event.Data["error"])
		case provider.EventBuiltIn:
			if err := a.runtime.HandleBuiltInEvent(ctx, providerClient, config, event.Data); err != nil {
				return sessionState, err
			}
		case provider.EventClientTool:
			if err := a.runtime.HandleClientToolEvent(ctx, providerClient, config, event.Data); err != nil {
				return sessionState, err
			}
		case provider.EventToolCall:
			if text, ok := event.Data["text"].(string); ok && text != "" {
				fmt.Fprintf(a.stdout, "\n[工具调用] %s\n", text)
			}
		case provider.EventClose:
			fmt.Fprintln(a.stdout)
		}
	}

	return sessionState, nil
}

func (a *App) printRootUsage() {
	fmt.Fprintln(a.stdout, "用法:")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli task [--agent AGENT] [--new] [--continue] [任务描述]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli config [show|init|path|set|unset]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli doctor")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli agents [list|current|use] [参数]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli skills [list|view|enable|disable] [参数]")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli version")
	fmt.Fprintf(a.stdout, "当前版本: %s\n", buildinfo.Version)
}

func (a *App) printInteractiveUsage(ctx context.Context, providerClient provider.Provider, flowDisplayName string, flowID string, sessionState agent.SessionState) {
	fmt.Fprintln(a.stdout, "会话内命令:")
	fmt.Fprintln(a.stdout, "  /agents   显示智能体切换说明")
	fmt.Fprintln(a.stdout, "  /config   查看或更新服务端配置")
	fmt.Fprintln(a.stdout, "  /skills   列出、查看或切换本地技能")
	fmt.Fprintln(a.stdout, "  /help     显示帮助")
	fmt.Fprintln(a.stdout, "  /session  显示当前会话状态")
	fmt.Fprintln(a.stdout, "  /sessions 显示本地已保存会话")
	fmt.Fprintln(a.stdout, "  /new      重置当前智能体的本地会话")
	fmt.Fprintln(a.stdout, "  /clear    清屏")
	fmt.Fprintln(a.stdout, "  /exit     退出")
	fmt.Fprintln(a.stdout, "  提示: 交互模式下请使用 `taiyiflow-cli agents` 管理默认智能体。")
	a.printSessionStatus(ctx, providerClient, flowDisplayName, flowID, sessionState)
}

func (a *App) printSessionStatus(ctx context.Context, providerClient provider.Provider, flowDisplayName string, flowID string, sessionState agent.SessionState) {
	_ = flowDisplayName
	if strings.TrimSpace(sessionState.SessionID) == "" {
		fmt.Fprintln(a.stdout, "当前会话: 新会话（未同步服务端）")
		return
	}
	if title, err := lookupSessionTitle(ctx, providerClient, flowID, sessionState.SessionID); err == nil && strings.TrimSpace(title) != "" {
		fmt.Fprintf(a.stdout, "当前会话: %s\n", sessionState.SessionID)
		fmt.Fprintf(a.stdout, "会话标题: %s\n", title)
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
		line := fmt.Sprintf("- %s", sanitizeFlowDisplay(state.FlowID))
		if strings.TrimSpace(state.SessionID) != "" {
			line += fmt.Sprintf(" | 会话 %s", state.SessionID)
		} else {
			line += " | 新会话（未同步服务端）"
		}
		if strings.TrimSpace(state.Workdir) != "" {
			line += fmt.Sprintf(" | 目录 %s", state.Workdir)
		}
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
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "init", "set", "unset", "clear":
		return true
	default:
		return false
	}
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
