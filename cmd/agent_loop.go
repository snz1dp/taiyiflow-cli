package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agentloop"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func (a *App) streamAgentLoopAttach(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, flowID, sessionID string) error {
	store, err := agentloop.DefaultStore()
	if err != nil {
		return err
	}
	controller := agentloop.NewController(providerClient, store)
	polling := false
	fmt.Fprintf(a.stdout, "正在只读观察会话 %s；按 Ctrl+C 退出观察，不会停止远程执行。\n", sessionID)
	err = controller.Attach(ctx, agentloop.ObserverRequest{
		FlowID: flowID, SessionID: sessionID, ClientID: config.ClientID, DeviceID: config.DeviceID,
	}, agentloop.ObserverHandlers{
		OnSnapshot: func(messages *provider.SessionMessageList) error {
			if messages == nil {
				return nil
			}
			fmt.Fprintf(a.stdout, "[远程快照] %d 条消息\n", len(messages.Items))
			for _, message := range messages.Items {
				if strings.TrimSpace(message.Text) == "" {
					continue
				}
				sender := firstNonEmpty(strings.TrimSpace(message.SenderName), firstNonEmpty(strings.TrimSpace(message.Sender), "远程"))
				fmt.Fprintf(a.stdout, "[%s] %s\n", sender, message.Text)
			}
			return nil
		},
		OnEvent: func(event provider.StreamEvent) error {
			switch event.Type {
			case provider.EventMessage:
				if chunk := stringMapValue(event.Data, "chunk"); chunk != "" {
					fmt.Fprint(a.stdout, chunk)
				} else if message := strings.TrimSpace(stringMapValue(event.Data, "message")); message != "" {
					fmt.Fprintln(a.stdout, message)
				}
			case provider.EventError:
				fmt.Fprintf(a.stderr, "远程执行错误: %s\n", stringMapValue(event.Data, "error"))
			case provider.EventClose:
				fmt.Fprintln(a.stdout, "\n远程执行观察已结束。")
			}
			return nil
		},
		OnStatus: func(status agentloop.ObserverStatus) error {
			if status.Polling && !polling {
				fmt.Fprintln(a.stderr, "观察流连续断开，已降级为状态轮询；仍不会执行本地工具。")
			}
			polling = status.Polling
			return nil
		},
	})
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(a.stdout, "\n已退出只读观察，远程执行未被停止。")
		return nil
	}
	return err
}

func (a *App) runAgentLoop(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("用法: taiyiflow-cli agentloop <pause|cancel|status|resume|attach> [--session-id ID] [--agent ID]")
	}
	action := strings.ToLower(strings.TrimSpace(args[0]))
	if action == "attach" {
		return a.runTask(ctx, append(args[1:], "--attach"))
	}
	if action == "resume" {
		return a.runTask(ctx, append(args[1:], "--resume"))
	}
	if action != "pause" && action != "cancel" && action != "status" {
		return fmt.Errorf("未知 AgentLoop 操作: %s", action)
	}
	flags := flag.NewFlagSet("agentloop "+action, flag.ContinueOnError)
	flags.SetOutput(a.stderr)
	sessionID := flags.String("session-id", "", "指定会话 ID")
	flow := flags.String("agent", "", "指定智能体 ID 或名称")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("无法识别参数: %s", strings.Join(flags.Args(), " "))
	}
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	config, _, err = a.ensureClientIdentity(config)
	if err != nil {
		return err
	}
	providerClient := buildProviderClient(config)
	config, _, err = a.ensureProviderAuthenticated(ctx, providerClient, config)
	if err != nil {
		return err
	}
	config, err = a.ensureDefaultFlow(ctx, providerClient, config)
	if err != nil {
		return err
	}
	flowID, err := resolveFlowID(*flow, config.DefaultFlow)
	if err != nil {
		return err
	}
	sessionState, err := a.prepareSession(flowID, false, config.Workdir, provider.RequestTypeAgent)
	if err != nil {
		return err
	}
	sessionState = applyExplicitTaskSessionID(sessionState, *sessionID)
	if sessionState.SessionID == "" {
		return errors.New("当前没有可控制的会话，请使用 --session-id 指定")
	}
	store, err := agentloop.DefaultStore()
	if err != nil {
		return err
	}
	commandProvider := a.providerWithCallbackAuthRefresh(providerClient, &config)
	controller := agentloop.NewController(commandProvider, store)
	if action == "status" {
		execution, executionErr := commandProvider.GetSessionExecution(ctx, sessionState.SessionID)
		if executionErr != nil {
			return executionErr
		}
		state, stateErr := store.LoadRecovery(sessionState.SessionID)
		if stateErr != nil {
			return stateErr
		}
		fmt.Fprintf(a.stdout, "会话: %s\n远程执行: executing=%t run_id=%s status=%s\n本地状态: run_id=%s intent=%s checkpoint=%s version=%d awaiting_resume=%t\n",
			sessionState.SessionID, execution.Executing, execution.ExecutingRunID, execution.RunStatus,
			state.RunID, state.Intent, state.CheckpointID, state.CheckpointVersion, state.AwaitingResume)
		return nil
	}
	state, err := store.LoadRecovery(sessionState.SessionID)
	if err != nil {
		return err
	}
	if state.RunID == "" {
		execution, executionErr := commandProvider.GetSessionExecution(ctx, sessionState.SessionID)
		if executionErr != nil {
			return executionErr
		}
		if execution.ExecutingRunID == "" {
			return errors.New("当前会话没有可控制的执行；本地也没有恢复状态")
		}
		state, err = store.UpdateRecovery(sessionState.SessionID, func(state *agentloop.RecoveryState) error {
			state.FlowID = firstNonEmpty(execution.FlowID, flowID)
			state.RunID = execution.ExecutingRunID
			state.ExecutionAttemptID = execution.ExecutionAttemptID
			state.ClientID = firstNonEmpty(execution.OriginClientID, config.ClientID)
			state.DeviceID = firstNonEmpty(execution.OriginDeviceID, config.DeviceID)
			state.Active = execution.Executing
			return nil
		})
		if err != nil {
			return err
		}
	}
	var response *provider.AgentLoopControlResponse
	if action == "pause" {
		response, err = controller.Pause(ctx, sessionState.SessionID, state.RunID)
	} else {
		response, err = controller.Cancel(ctx, sessionState.SessionID, state.RunID)
	}
	if err != nil {
		return fmt.Errorf("%s请求未成功: %w", action, err)
	}
	label := "暂停"
	if action == "cancel" {
		label = "停止"
	}
	fmt.Fprintf(a.stdout, "%s请求已受理: status=%s run_id=%s request_id=%s\n", label, response.Status, response.RunID, response.RequestID)
	if action == "pause" {
		if response.CheckpointID == "" {
			fmt.Fprintln(a.stdout, "暂停检查点尚未生成，请使用 agentloop status 继续确认。")
		} else {
			fmt.Fprintf(a.stdout, "暂停检查点已生成: %s\n", response.CheckpointID)
		}
	} else {
		fmt.Fprintln(a.stdout, "等待服务端权威终止；202/受理不代表所有副作用已结束。")
	}
	return nil
}
