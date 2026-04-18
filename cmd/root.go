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
		return a.runInteractive(ctx, rootOptions{})
	}

	switch args[0] {
	case "config":
		return a.runConfig(args[1:])
	case "doctor":
		return a.runDoctor(args[1:])
	case "flows":
		return a.runFlows(ctx, args[1:])
	case "tui":
		return a.runTUI(ctx, args[1:])
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
	flagSet := flag.NewFlagSet("taiyiflow", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)

	var options rootOptions
	flagSet.StringVar(&options.Flow, "flow", "", "指定要运行的智能服务 ID 或名称")
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

	flowID, err := resolveFlowID(options.Flow, config.DefaultFlow)
	if err != nil {
		return err
	}

	providerClient := providerclient.NewClient(config.BackendBaseURL, config.APIKey)
	sessionState, err := a.prepareSession(flowID, options.NewSession)
	if err != nil {
		return err
	}

	metadata, err := providerClient.GetMetadata(ctx, flowID)
	if err == nil && metadata.WelcomeMessage != "" {
		fmt.Fprintln(a.stdout, metadata.WelcomeMessage)
	}
	if err != nil {
		fmt.Fprintf(a.stderr, "提示: 获取元信息失败，继续进入交互模式: %v\n", err)
	}

	reader := bufio.NewScanner(a.stdin)
	fmt.Fprintln(a.stdout, "进入交互模式，输入 /exit 退出，输入 /new 新建会话。")

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

		switch line {
		case "/exit", "/quit":
			return nil
		case "/help":
			a.printInteractiveUsage(flowID, sessionState)
			continue
		case "/clear":
			fmt.Fprint(a.stdout, "\033[H\033[2J")
			continue
		case "/session":
			a.printSessionStatus(flowID, sessionState)
			continue
		case "/new":
			sessionState = agent.SessionState{FlowID: flowID}
			if err := a.sessionStore.Delete(flowID); err != nil {
				return err
			}
			fmt.Fprintln(a.stdout, "已重置本地会话。")
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

	flowID, err := resolveFlowID(options.Flow, config.DefaultFlow)
	if err != nil {
		return err
	}

	providerClient := providerclient.NewClient(config.BackendBaseURL, config.APIKey)
	sessionState, err := a.prepareSession(flowID, options.NewSession)
	if err != nil {
		return err
	}

	_, err = a.streamTask(ctx, providerClient, config, flowID, task, sessionState)
	return err
}

func (a *App) prepareSession(flowID string, newSession bool) (agent.SessionState, error) {
	if newSession {
		if err := a.sessionStore.Delete(flowID); err != nil {
			return agent.SessionState{}, err
		}
		return agent.SessionState{FlowID: flowID}, nil
	}
	return a.sessionStore.Load(flowID)
}

func (a *App) streamTask(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
	task string,
	sessionState agent.SessionState,
) (agent.SessionState, error) {
	stream, streamSession, err := providerClient.Stream(ctx, provider.StreamRequest{
		FlowID:                 flowID,
		Input:                  task,
		InputType:              "any",
		OutputType:             "any",
		SessionID:              sessionState.SessionID,
		RequestType:            "agent",
		CommandExecutionTarget: "client",
		ClientMCPServers:       a.runtime.ClientMCPServers(),
	})
	if err != nil {
		return sessionState, err
	}

	if streamSession != nil && streamSession.SessionID != "" {
		sessionState.SessionID = streamSession.SessionID
		sessionState.FlowID = flowID
		if err := a.sessionStore.Save(sessionState); err != nil {
			return sessionState, err
		}
	}

	for event := range stream {
		switch event.Type {
		case provider.EventMessage:
			if message, ok := event.Data["message"].(string); ok && message != "" {
				fmt.Fprintf(a.stdout, "\n[%s]\n", message)
			}
			if chunk, ok := event.Data["chunk"].(string); ok {
				fmt.Fprint(a.stdout, chunk)
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
	fmt.Fprintln(a.stdout, "  taiyiflow [--flow FLOW] [--new] [--continue] [任务描述]")
	fmt.Fprintln(a.stdout, "  taiyiflow config [show|init|path|set]")
	fmt.Fprintln(a.stdout, "  taiyiflow doctor")
	fmt.Fprintln(a.stdout, "  taiyiflow flows [list|current|use] [参数]")
	fmt.Fprintln(a.stdout, "  taiyiflow tui")
	fmt.Fprintln(a.stdout, "  taiyiflow version")
	fmt.Fprintf(a.stdout, "当前版本: %s\n", buildinfo.Version)
}

func (a *App) printInteractiveUsage(flowID string, sessionState agent.SessionState) {
	fmt.Fprintln(a.stdout, "会话内命令:")
	fmt.Fprintln(a.stdout, "  /help     显示帮助")
	fmt.Fprintln(a.stdout, "  /session  显示当前会话状态")
	fmt.Fprintln(a.stdout, "  /new      重置当前 flow 的本地会话")
	fmt.Fprintln(a.stdout, "  /clear    清屏")
	fmt.Fprintln(a.stdout, "  /exit     退出")
	a.printSessionStatus(flowID, sessionState)
}

func (a *App) printSessionStatus(flowID string, sessionState agent.SessionState) {
	fmt.Fprintf(a.stdout, "当前 flow: %s\n", flowID)
	if strings.TrimSpace(sessionState.SessionID) == "" {
		fmt.Fprintln(a.stdout, "当前 session: <未建立>")
		return
	}
	fmt.Fprintf(a.stdout, "当前 session: %s\n", sessionState.SessionID)
}

func resolveFlowID(inputFlow string, defaultFlow string) (string, error) {
	flowID := strings.TrimSpace(firstNonEmpty(inputFlow, defaultFlow))
	if flowID == "" {
		return "", errors.New("未配置默认 flow，请先执行 `taiyiflow flows` 查看可用智能服务，再执行 `taiyiflow flows use <ID>` 或使用 `--flow` 指定")
	}
	return flowID, nil
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
