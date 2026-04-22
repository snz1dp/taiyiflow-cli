package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

var bracketErrorCodePattern = regexp.MustCompile(`^\[[A-Z0-9_]+\]\s*`)

func (a *App) runTUI(ctx context.Context, args []string) error {
	startupCommand := ""
	if len(args) > 0 {
		startupCommand = strings.TrimSpace(args[0])
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	providerClient := buildProviderClient(config)
	backend := a.inspectBackendStatus(ctx, providerClient, config.BackendBaseURL)
	if backend.Available {
		config, err = a.ensureDefaultFlow(ctx, providerClient, config)
		if err != nil {
			return err
		}
	}
	workdir := resolveLocalWorkdir(config.Workdir)
	snapshot, err := a.buildTUISnapshot(ctx, providerClient, config, strings.TrimSpace(config.DefaultFlow), backend)
	if err != nil {
		return err
	}

	flowItems := []tui.FlowItem{}
	if backend.Available {
		flows, flowErr := providerClient.ListFlows(ctx, "", "chat", 0, 100)
		if flowErr == nil {
			for _, item := range flows.Items {
				flowItems = append(flowItems, tui.FlowItem{
					ID:          item.ID,
					Name:        item.Name,
					Description: item.Description,
					Type:        item.Type,
				})
			}
		}
	}

	runner := func(runCtx context.Context, request tui.Request, emit func(tui.Event), confirm func(tui.ConfirmRequest) bool) error {
		currentFlow := strings.TrimSpace(request.FlowID)
		if currentFlow == "" {
			return fmt.Errorf("未配置默认智能体，请先执行 taiyiflow-cli agents use <名称>")
		}

		sessionState, err := a.prepareSession(currentFlow, false, workdir, request.RequestMode)
		if err != nil {
			return err
		}

		logWriter := &tuiLogWriter{emit: emit}
		defer logWriter.Flush()

		runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), logWriter, logWriter)
		quietRuntimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), io.Discard, io.Discard)
		persistSandboxEscapeAlwaysAllow := func(context.Context) error {
			runtimeClient.SetSandboxEscapePolicyOverride(internalconfig.SandboxEscapePolicyAllow)
			quietRuntimeClient.SetSandboxEscapePolicyOverride(internalconfig.SandboxEscapePolicyAllow)
			sessionState.SandboxEscapePolicyOverride = internalconfig.SandboxEscapePolicyAllow
			return a.sessionStore.Save(sessionState)
		}
		sandboxEscapeConfirmer := func(path string) (bool, error) {
			return confirm(buildTUISandboxEscapeConfirmRequest(path, persistSandboxEscapeAlwaysAllow)), nil
		}
		runtimeClient.SetSandboxEscapeConfirmer(sandboxEscapeConfirmer)
		quietRuntimeClient.SetSandboxEscapeConfirmer(sandboxEscapeConfirmer)
		requestedSkillNames, clientSkillPackages, err := a.loadSkillPayload(config)
		if err != nil {
			return err
		}
		stream, streamSession, err := providerClient.Stream(runCtx, provider.StreamRequest{
			FlowID:                 currentFlow,
			Input:                  request.Input,
			InputType:              "any",
			OutputType:             "any",
			SessionID:              sessionState.SessionID,
			RunID:                  request.RunID,
			RequestType:            normalizeRequestMode(request.RequestMode),
			DeviceType:             provider.DeviceTypeCLI,
			ClientContextDir:       resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, workdir)),
			CommandExecutionTarget: "client",
			UserAgent:              buildCLIUserAgent(),
			RequestedSkillNames:    requestedSkillNames,
			ClientMCPServers:       quietRuntimeClient.ClientMCPServersForMode(request.RequestMode),
			ClientSkillPackages:    clientSkillPackages,
		})
		if err != nil {
			return fmt.Errorf("%s", explainBackendFailure(err))
		}

		if streamSession != nil && strings.TrimSpace(streamSession.SessionID) != "" {
			sessionState.SessionID = streamSession.SessionID
			sessionState.FlowID = currentFlow
			sessionState.FlowDisplayName = strings.TrimSpace(a.resolveFlowDisplayName(runCtx, providerClient, currentFlow))
			title, titleErr := lookupSessionTitle(runCtx, providerClient, currentFlow, streamSession.SessionID)
			if titleErr == nil {
				sessionState.SessionTitle = strings.TrimSpace(title)
			}
			sessionState.Workdir = workdir
			sessionState.RequestMode = normalizeRequestMode(request.RequestMode)
			sessionState.ContextDir = resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, workdir))
			if err := a.sessionStore.Save(sessionState); err != nil {
				return err
			}
			emit(tui.Event{Kind: tui.EventSessionUpdate, SessionID: streamSession.SessionID})
		}

		for event := range stream {
			switch event.Type {
			case provider.EventMessage:
				if chunk, ok := event.Data["chunk"].(string); ok && chunk != "" {
					emit(tui.Event{Kind: tui.EventAssistantChunk, Text: chunk})
					continue
				}
				if message, ok := event.Data["message"].(string); ok && strings.TrimSpace(message) != "" {
					emit(tui.Event{Kind: tui.EventProgressNote, Text: message})
				}
			case provider.EventToolCall:
				emit(tui.Event{Kind: tui.EventToolCall, Text: strings.TrimSpace(stringMapValue(event.Data, "text"))})
			case provider.EventBuiltIn:
				if !modeAllowsBuiltIn(request.RequestMode) {
					emit(tui.Event{Kind: tui.EventErrorNote, Text: fmt.Sprintf("已拒绝本地命令执行：当前为%s。", requestModeLabel(request.RequestMode))})
					if err := submitClientCallFailure(runCtx, providerClient, event.Data, fmt.Sprintf("当前%s不允许执行本地命令", requestModeLabel(request.RequestMode))); err != nil {
						return err
					}
					continue
				}
				localConfig, configErr := a.configManager.Load()
				if configErr != nil {
					return configErr
				}
				localConfig = applyEffectiveSandboxEscapePolicy(localConfig, sessionState.SandboxEscapePolicyOverride, "")
				emit(tui.Event{Kind: tui.EventBuiltInCall, Text: previewBuiltInCommand(event.Data)})
				emit(tui.Event{Kind: tui.EventProgressNote, Text: "正在执行本地命令..."})
				if requiresTUICommandConfirmation(localConfig, event.Data) {
					approved := confirm(buildTUIConfirmRequest(event.Data))
					if !approved {
						emit(tui.Event{Kind: tui.EventErrorNote, Text: "已拒绝本次客户端命令执行。"})
						if err := submitClientCallFailure(runCtx, providerClient, event.Data, "用户拒绝执行客户端命令"); err != nil {
							return err
						}
						continue
					}
					execConfig := localConfig
					execConfig.ApprovalMode = "auto"
					if err := quietRuntimeClient.HandleBuiltInEvent(runCtx, providerClient, execConfig, event.Data); err != nil {
						return err
					}
					continue
				}
				if err := quietRuntimeClient.HandleBuiltInEvent(runCtx, providerClient, localConfig, event.Data); err != nil {
					return err
				}
			case provider.EventClientTool:
				toolName := nestedStringValue(event.Data, "function", "name")
				if !modeAllowsClientTool(request.RequestMode, toolName) {
					emit(tui.Event{Kind: tui.EventErrorNote, Text: fmt.Sprintf("已拒绝客户端工具 %s：当前为%s。", toolName, requestModeLabel(request.RequestMode))})
					if err := submitClientCallFailure(runCtx, providerClient, event.Data, fmt.Sprintf("当前%s不允许执行本地工具 %s", requestModeLabel(request.RequestMode), toolName)); err != nil {
						return err
					}
					continue
				}
				localConfig, configErr := a.configManager.Load()
				if configErr != nil {
					return configErr
				}
				localConfig = applyEffectiveSandboxEscapePolicy(localConfig, sessionState.SandboxEscapePolicyOverride, "")
				emit(tui.Event{Kind: tui.EventClientToolCall, Text: clientruntime.PreviewClientToolCall(event.Data)})
				if clientruntime.RequiresClientToolConfirmation(localConfig, event.Data) {
					approved := confirm(buildTUIClientToolConfirmRequest(event.Data))
					if !approved {
						emit(tui.Event{Kind: tui.EventErrorNote, Text: "已拒绝本次客户端工具执行。"})
						if err := submitClientCallFailure(runCtx, providerClient, event.Data, "用户拒绝执行客户端工具"); err != nil {
							return err
						}
						continue
					}
					execConfig := localConfig
					execConfig.ApprovalMode = "auto"
					toolName, result, toolErr := runtimeClient.ExecuteClientToolPayload(runCtx, execConfig, event.Data)
					emitTUIClientToolResultEvents(emit, toolName, result, toolErr)
					if err := runtimeClient.SubmitClientToolCallback(runCtx, providerClient, event.Data, result, toolErr); err != nil {
						return err
					}
					continue
				}
				toolName, result, toolErr := runtimeClient.ExecuteClientToolPayload(runCtx, localConfig, event.Data)
				emitTUIClientToolResultEvents(emit, toolName, result, toolErr)
				if err := runtimeClient.SubmitClientToolCallback(runCtx, providerClient, event.Data, result, toolErr); err != nil {
					return err
				}
			case provider.EventError:
				return normalizeStreamEventError(event.Data["error"])
			case provider.EventClose:
				return nil
			}
		}

		if request.RunID != "" && strings.TrimSpace(sessionState.SessionID) != "" {
			stats, statsErr := providerClient.GetRunStats(runCtx, currentFlow, sessionState.SessionID, request.RunID)
			if statsErr == nil && stats != nil {
				emit(tui.Event{Kind: tui.EventStatsUpdate, Stats: buildTUIRequestMetrics(request.RunID, stats)})
			}
		}

		return nil
	}

	refreshSnapshot := func(refreshCtx context.Context, current tui.Snapshot) (tui.Snapshot, []tui.FlowItem, error) {
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return tui.Snapshot{}, nil, err
		}
		if strings.TrimSpace(updatedConfig.DefaultFlow) == "" {
			updatedConfig.DefaultFlow = strings.TrimSpace(current.DefaultFlow)
		}

		refreshedClient := buildProviderClient(updatedConfig)
		backend := a.inspectBackendStatus(refreshCtx, refreshedClient, updatedConfig.BackendBaseURL)
		if backend.Available {
			updatedConfig, err = a.ensureDefaultFlow(refreshCtx, refreshedClient, updatedConfig)
			if err != nil {
				return tui.Snapshot{}, nil, err
			}
		}

		flowID := strings.TrimSpace(firstNonEmpty(updatedConfig.DefaultFlow, current.DefaultFlow))
		snapshot, err := a.buildTUISnapshot(refreshCtx, refreshedClient, updatedConfig, flowID, backend)
		if err != nil {
			return tui.Snapshot{}, nil, err
		}
		providerClient = refreshedClient
		config = updatedConfig
		workdir = resolveLocalWorkdir(updatedConfig.Workdir)

		flowItems := []tui.FlowItem{}
		if backend.Available {
			flows, flowErr := refreshedClient.ListFlows(refreshCtx, "", "chat", 0, 100)
			if flowErr == nil {
				for _, item := range flows.Items {
					flowItems = append(flowItems, tui.FlowItem{
						ID:          item.ID,
						Name:        item.Name,
						Description: item.Description,
						Type:        item.Type,
					})
				}
			}
		}

		return snapshot, flowItems, nil
	}

	flowChange := func(changeCtx context.Context, flowID string) (tui.Snapshot, error) {
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return tui.Snapshot{}, err
		}
		updatedConfig.DefaultFlow = strings.TrimSpace(flowID)
		if err := a.configManager.Save(updatedConfig); err != nil {
			return tui.Snapshot{}, err
		}
		return a.buildTUISnapshot(changeCtx, providerClient, updatedConfig, updatedConfig.DefaultFlow, backendStatus{Available: true})
	}

	sessionChange := func(changeCtx context.Context, current tui.Snapshot, sessionID string) (tui.Snapshot, error) {
		flowID := strings.TrimSpace(current.DefaultFlow)
		if flowID == "" {
			return tui.Snapshot{}, fmt.Errorf("当前未选择智能体，无法切换会话")
		}
		loadedState, loadErr := a.prepareSession(flowID, false, current.Workdir, current.RequestMode)
		if loadErr != nil {
			return tui.Snapshot{}, loadErr
		}
		if err := a.sessionStore.Save(agent.SessionState{
			FlowID:                      flowID,
			FlowDisplayName:             strings.TrimSpace(current.DefaultFlowLabel),
			SessionID:                   strings.TrimSpace(sessionID),
			SessionTitle:                "",
			Workdir:                     current.Workdir,
			RequestMode:                 normalizeRequestMode(firstNonEmpty(current.RequestMode, loadedState.RequestMode)),
			ContextDir:                  resolveLocalWorkdir(firstNonEmpty(loadedState.ContextDir, current.Workdir)),
			SandboxEscapePolicyOverride: loadedState.SandboxEscapePolicyOverride,
		}); err != nil {
			return tui.Snapshot{}, err
		}
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return tui.Snapshot{}, err
		}
		return a.buildTUISnapshot(changeCtx, providerClient, updatedConfig, flowID, backendStatus{Available: true})
	}

	approvalChange := func(changeCtx context.Context, mode string) (tui.Snapshot, error) {
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return tui.Snapshot{}, err
		}
		updatedConfig.ApprovalMode = strings.TrimSpace(mode)
		if err := a.configManager.Save(updatedConfig); err != nil {
			return tui.Snapshot{}, err
		}
		return a.buildTUISnapshot(changeCtx, providerClient, updatedConfig, updatedConfig.DefaultFlow, backendStatus{Available: true})
	}

	configFormSubmit := func(changeCtx context.Context, snapshot tui.Snapshot, submission tui.ConfigFormSubmission) (tui.LocalCommandResult, error) {
		updatedConfig, err := a.applyConfigFormSubmission(submission)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}

		updatedSnapshot, updatedFlows, err := refreshSnapshot(changeCtx, snapshot)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}

		return tui.LocalCommandResult{
			Handled:        true,
			Snapshot:       &updatedSnapshot,
			Flows:          updatedFlows,
			RebuildEntries: true,
			Events:         []tui.Event{{Kind: tui.EventSystemNote, Text: fmt.Sprintf("已更新配置\n%s", renderConfigSummary(a.configManager.Path(), updatedConfig))}},
		}, nil
	}

	localCommand := func(commandCtx context.Context, command string, snapshot tui.Snapshot) (tui.LocalCommandResult, error) {
		trimmed := strings.TrimSpace(command)
		switch {
		case trimmed == "/help":
			helpLines := []string{
				"可用命令:",
				"/new 重置当前智能体的本地会话",
				"/clear 清空当前界面消息",
				"/agent 或 /agent <任务> 切换执行模式",
				"/ask 或 /ask <问题> 切换问答模式",
				"/plan 或 /plan <目标> 切换计划模式",
				"/session 查看当前会话",
				"/sessions [页码] 查看当前智能体的服务端会话列表",
				"/config 打开服务端配置弹窗",
				"!<shell 命令> 在当前目录执行本地命令",
				"/agents 或 /agents <名称> 选择智能体",
				"/skills [list|view|enable|disable] 管理本地技能",
				"/exit 退出",
			}
			if !snapshot.BackendAvailable {
				reason := strings.TrimSpace(snapshot.BackendStatusReason)
				if reason == "" {
					reason = "服务端不可用"
				}
				helpLines = []string{
					"当前处于只读模式。",
					"原因: " + reason,
					"可用命令:",
					"/clear 清空当前界面消息",
					"/config 打开当前配置弹窗",
					"/skills 查看本地技能",
					"/help 查看当前提示",
					"/exit 退出",
				}
			}
			return tui.LocalCommandResult{
				Handled: true,
				Events:  []tui.Event{{Kind: tui.EventSystemNote, Text: strings.Join(helpLines, "\n")}},
			}, nil
		case trimmed == "/config" || strings.EqualFold(trimmed, "/config show"):
			formState, err := a.buildTUIConfigFormState(commandCtx, providerClient, snapshot)
			if err != nil {
				return tui.LocalCommandResult{}, err
			}
			return tui.LocalCommandResult{Handled: true, ConfigForm: formState}, nil
		case strings.HasPrefix(strings.ToLower(trimmed), "/config session "):
			flowID := strings.TrimSpace(snapshot.DefaultFlow)
			if flowID == "" {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "当前未选择智能体，无法设置会话配置。"}}}, nil
			}
			sessionState, err := a.prepareSession(flowID, false, snapshot.Workdir, snapshot.RequestMode)
			if err != nil {
				return tui.LocalCommandResult{}, err
			}
			output, err := a.runInteractiveSessionConfigCommand(trimmed, &sessionState)
			if err != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
			}
			updatedSnapshot := snapshot
			updatedSnapshot.RequestMode = normalizeRequestMode(firstNonEmpty(sessionState.RequestMode, snapshot.RequestMode))
			updatedSnapshot.ContextDir = resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, snapshot.Workdir))
			updatedSnapshot.SessionSandboxEscapePolicy = strings.TrimSpace(sessionState.SandboxEscapePolicyOverride)
			return tui.LocalCommandResult{Handled: true, Snapshot: &updatedSnapshot, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: output}}}, nil
		case strings.EqualFold(trimmed, "/skills") || strings.HasPrefix(strings.ToLower(trimmed), "/skills "):
			output, err := a.executeSkillsCommand(parseSlashSkillsArgs(trimmed))
			if err != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
			}
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: output}}}, nil
		case strings.EqualFold(trimmed, "/ask") || strings.HasPrefix(strings.ToLower(trimmed), "/ask ") || strings.EqualFold(trimmed, "/plan") || strings.HasPrefix(strings.ToLower(trimmed), "/plan ") || strings.EqualFold(trimmed, "/agent") || strings.HasPrefix(strings.ToLower(trimmed), "/agent "):
			mode, prompt, ok := parseModeSlashCommand(trimmed)
			if !ok {
				return tui.LocalCommandResult{}, nil
			}
			updatedSnapshot := snapshot
			updatedSnapshot.RequestMode = mode
			updatedSnapshot.ContextDir = resolveLocalWorkdir(snapshot.Workdir)
			if strings.TrimSpace(prompt) == "" {
				if strings.TrimSpace(snapshot.DefaultFlow) != "" {
					currentSessionState, sessionErr := a.prepareSession(snapshot.DefaultFlow, false, snapshot.Workdir, mode)
					if sessionErr != nil {
						return tui.LocalCommandResult{}, sessionErr
					}
					if err := a.sessionStore.Save(agent.SessionState{
						FlowID:                      snapshot.DefaultFlow,
						FlowDisplayName:             strings.TrimSpace(snapshot.DefaultFlowLabel),
						SessionID:                   snapshot.SessionID,
						SessionTitle:                strings.TrimSpace(snapshot.SessionTitle),
						Workdir:                     snapshot.Workdir,
						RequestMode:                 mode,
						ContextDir:                  updatedSnapshot.ContextDir,
						SandboxEscapePolicyOverride: currentSessionState.SandboxEscapePolicyOverride,
					}); err != nil {
						return tui.LocalCommandResult{}, err
					}
				}
				return tui.LocalCommandResult{Handled: true, Snapshot: &updatedSnapshot, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: fmt.Sprintf("已切换为%s。", requestModeLabel(mode))}}}, nil
			}
			return tui.LocalCommandResult{
				Handled:     true,
				Snapshot:    &updatedSnapshot,
				QueuedInput: strings.TrimSpace(prompt),
				Events: []tui.Event{{
					Kind: tui.EventSystemNote,
					Text: fmt.Sprintf("已切换为%s，正在发送请求。", requestModeLabel(mode)),
				}},
			}, nil
		case strings.HasPrefix(strings.ToLower(trimmed), "/config "):
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "TUI 中请直接输入 /config 打开弹窗表单。"}}}, nil
		case trimmed == "!":
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "! 用法: !<shell 命令>"}}}, nil
		case strings.HasPrefix(trimmed, "!"):
			rawCommand := strings.TrimSpace(strings.TrimPrefix(trimmed, "!"))
			if rawCommand == "" {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "! 用法: !<shell 命令>"}}}, nil
			}
			localConfig, err := a.configManager.Load()
			if err != nil {
				return tui.LocalCommandResult{}, err
			}
			effectiveConfig := localConfig
			if flowID := strings.TrimSpace(snapshot.DefaultFlow); flowID != "" {
				sessionState, sessionErr := a.prepareSession(flowID, false, snapshot.Workdir, snapshot.RequestMode)
				if sessionErr == nil {
					effectiveConfig = applyEffectiveSandboxEscapePolicy(localConfig, sessionState.SandboxEscapePolicyOverride, "")
				}
			}
			runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), io.Discard, io.Discard)
			result, err := runtimeClient.ExecuteShellCommand(commandCtx, effectiveConfig, rawCommand)
			if err != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
			}
			return tui.LocalCommandResult{Handled: true, Events: buildLocalShellCommandEvents(rawCommand, result)}, nil
		case trimmed == "/clear":
			return tui.LocalCommandResult{Handled: true, ClearHistory: true}, nil
		case trimmed == "/session":
			sessionID := strings.TrimSpace(snapshot.SessionID)
			resolvedSnapshot := snapshot
			if sessionID != "" && strings.TrimSpace(resolvedSnapshot.SessionTitle) == "" {
				title, err := lookupSessionTitle(commandCtx, providerClient, snapshot.DefaultFlow, sessionID)
				if err == nil {
					resolvedSnapshot.SessionTitle = strings.TrimSpace(title)
				}
			}
			text := tui.RenderSessionStatusText(resolvedSnapshot, "")
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: text}}}, nil
		case strings.EqualFold(trimmed, "/sessions") || strings.HasPrefix(strings.ToLower(trimmed), "/sessions "):
			page, err := parseSessionsPage(trimmed)
			if err != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
			}
			flowID := strings.TrimSpace(snapshot.DefaultFlow)
			if flowID == "" {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "当前未选择智能体，无法获取服务端会话。"}}}, nil
			}
			const pageSize = 10
			offset := (page - 1) * pageSize
			sessions, err := providerClient.ListSessions(commandCtx, flowID, offset, pageSize)
			if err != nil {
				return tui.LocalCommandResult{}, fmt.Errorf("%s", explainBackendFailure(err))
			}
			agentLabel := snapshot.DefaultFlowLabel
			if strings.TrimSpace(agentLabel) == "" {
				agentLabel = a.resolveFlowDisplayName(commandCtx, providerClient, flowID)
			}
			if sessions == nil || len(sessions.Items) == 0 {
				if sessions != nil && sessions.Total > 0 && page > 1 {
					return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: fmt.Sprintf("页码超出范围，请输入 1 到 %d。", totalPages(sessions.Total, pageSize))}}}, nil
				}
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: fmt.Sprintf("智能体 %s 当前没有服务端会话。", sanitizeFlowDisplay(agentLabel))}}}, nil
			}
			pages := totalPages(sessions.Total, pageSize)
			if pages == 0 {
				pages = 1
			}
			items := make([]tui.SessionItem, 0, len(sessions.Items))
			for _, item := range sessions.Items {
				name := strings.TrimSpace(item.Name)
				if name == "" {
					name = "<未命名会话>"
				}
				recentAt := item.UpdatedAt
				if recentAt.IsZero() {
					recentAt = item.CreatedAt
				}
				items = append(items, tui.SessionItem{
					ID:           item.ID,
					Title:        name,
					RelativeTime: formatRelativeTimeChinese(recentAt),
					IsCurrent:    strings.TrimSpace(snapshot.SessionID) != "" && item.ID == snapshot.SessionID,
				})
			}
			hint := "使用方向键选择，左右键翻页，Enter 切换当前会话，Esc 关闭。"
			if pages > 1 {
				hint = fmt.Sprintf("使用方向键选择，左右键翻页。当前共 %d 页，当前页 %d。", pages, page)
			}
			return tui.LocalCommandResult{
				Handled: true,
				SessionPicker: &tui.SessionPickerState{
					Title:      fmt.Sprintf("会话列表（%s）", sanitizeFlowDisplay(agentLabel)),
					Hint:       hint,
					Page:       page,
					TotalPages: pages,
					Items:      items,
				},
			}, nil
		case trimmed == "/new":
			if strings.TrimSpace(snapshot.DefaultFlow) == "" {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "当前未选择默认智能体，无法重置会话。"}}}, nil
			}
			if err := a.sessionStore.Delete(snapshot.DefaultFlow, snapshot.Workdir); err != nil {
				return tui.LocalCommandResult{}, err
			}
			updatedSnapshot := snapshot
			updatedSnapshot.SessionID = ""
			updatedSnapshot.SessionTitle = ""
			updatedSnapshot.RequestMode = provider.RequestTypeAgent
			updatedSnapshot.ContextDir = resolveLocalWorkdir(snapshot.Workdir)
			updatedSnapshot.SessionSandboxEscapePolicy = ""
			return tui.LocalCommandResult{
				Handled:        true,
				ClearHistory:   true,
				Snapshot:       &updatedSnapshot,
				RebuildEntries: true,
				Events: []tui.Event{
					{Kind: tui.EventSessionUpdate, SessionID: ""},
				},
			}, nil
		default:
			return tui.LocalCommandResult{}, nil
		}
	}

	return tui.Run(ctx, snapshot, flowItems, runner, refreshSnapshot, flowChange, sessionChange, approvalChange, configFormSubmit, localCommand, startupCommand)
}

func (a *App) buildTUISnapshot(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
	backend backendStatus,
) (tui.Snapshot, error) {
	snapshot := tui.Snapshot{
		Version:             buildinfo.Version,
		BackendURL:          config.BackendBaseURL,
		BackendAvailable:    backend.Available,
		BackendStatusReason: strings.TrimSpace(backend.Reason),
		DefaultFlow:         flowID,
		DefaultFlowLabel:    a.resolveFlowDisplayName(ctx, providerClient, flowID),
		Workdir:             resolveLocalWorkdir(config.Workdir),
		ContextDir:          resolveLocalWorkdir(config.Workdir),
		RequestMode:         provider.RequestTypeAgent,
		ApprovalMode:        config.ApprovalMode,
	}
	if !backend.Available {
		if strings.TrimSpace(flowID) == "" {
			return snapshot, nil
		}

		sessionState, err := a.prepareSession(flowID, false, snapshot.Workdir, "")
		if err != nil {
			return tui.Snapshot{}, err
		}
		snapshot.SessionID = sessionState.SessionID
		snapshot.SessionTitle = resolveSessionTitle(sessionState)
		snapshot.RequestMode = normalizeRequestMode(sessionState.RequestMode)
		snapshot.ContextDir = resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, snapshot.Workdir))
		snapshot.SessionSandboxEscapePolicy = strings.TrimSpace(sessionState.SandboxEscapePolicyOverride)
		snapshot.DefaultFlowLabel = resolveFlowLabel(flowID, snapshot.DefaultFlowLabel, sessionState.FlowDisplayName)
		return snapshot, nil
	}
	if versionInfo, err := providerClient.GetVersionInfo(ctx); err == nil && versionInfo != nil {
		snapshot.ServerPackage = strings.TrimSpace(versionInfo.Package)
		snapshot.ServerVersion = firstNonEmpty(strings.TrimSpace(versionInfo.MainVersion), strings.TrimSpace(versionInfo.Version))
	}
	if strings.TrimSpace(flowID) == "" {
		return snapshot, nil
	}

	sessionState, err := a.prepareSession(flowID, false, snapshot.Workdir, "")
	if err != nil {
		return tui.Snapshot{}, err
	}
	snapshot.SessionID = sessionState.SessionID
	snapshot.SessionTitle = resolveSessionTitle(sessionState)
	snapshot.RequestMode = normalizeRequestMode(sessionState.RequestMode)
	snapshot.ContextDir = resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, snapshot.Workdir))
	snapshot.SessionSandboxEscapePolicy = strings.TrimSpace(sessionState.SandboxEscapePolicyOverride)
	if strings.TrimSpace(snapshot.SessionID) != "" {
		title, titleErr := lookupSessionTitle(ctx, providerClient, flowID, snapshot.SessionID)
		if titleErr == nil {
			snapshot.SessionTitle = strings.TrimSpace(title)
			sessionState.SessionTitle = snapshot.SessionTitle
		}
		snapshot.History = loadRecentSessionHistory(ctx, providerClient, flowID, snapshot.SessionID)
	}

	metadata, err := providerClient.GetMetadata(ctx, flowID)
	if err == nil {
		snapshot.WelcomeMessage = metadata.WelcomeMessage
		if strings.TrimSpace(metadata.Name) != "" {
			snapshot.DefaultFlowLabel = metadata.Name
			sessionState.FlowDisplayName = strings.TrimSpace(metadata.Name)
		}
	}
	if strings.TrimSpace(sessionState.FlowDisplayName) == "" {
		sessionState.FlowDisplayName = strings.TrimSpace(snapshot.DefaultFlowLabel)
	}
	if strings.TrimSpace(snapshot.SessionTitle) != "" {
		sessionState.SessionTitle = strings.TrimSpace(snapshot.SessionTitle)
	}
	if strings.TrimSpace(sessionState.FlowID) == "" {
		sessionState.FlowID = flowID
	}
	if strings.TrimSpace(sessionState.Workdir) == "" {
		sessionState.Workdir = snapshot.Workdir
	}
	if strings.TrimSpace(sessionState.RequestMode) == "" {
		sessionState.RequestMode = snapshot.RequestMode
	}
	if strings.TrimSpace(sessionState.ContextDir) == "" {
		sessionState.ContextDir = snapshot.ContextDir
	}
	if err := a.sessionStore.Save(sessionState); err != nil {
		return snapshot, nil
	}
	return snapshot, nil
}

func buildLocalShellCommandEvents(command string, result map[string]any) []tui.Event {
	events := []tui.Event{}
	if trimmed := strings.TrimSpace(command); trimmed != "" {
		events = append(events, tui.Event{Kind: tui.EventSystemNote, Text: "! " + trimmed})
	}
	if stdout, ok := result["stdout"].(string); ok {
		stdout = strings.TrimRight(stdout, "\n")
		if stdout != "" {
			events = append(events, tui.Event{Kind: tui.EventSystemNote, Text: stdout})
		}
	}
	if stderr, ok := result["stderr"].(string); ok {
		stderr = strings.TrimRight(stderr, "\n")
		if stderr != "" {
			events = append(events, tui.Event{Kind: tui.EventErrorNote, Text: stderr})
		}
	}
	return events
}

func resolveFlowLabel(flowID string, fromSnapshot string, fromCache string) string {
	if trimmed := strings.TrimSpace(fromCache); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(fromSnapshot); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(flowID)
}

func resolveSessionTitle(state agent.SessionState) string {
	if strings.TrimSpace(state.SessionID) == "" {
		return "新会话"
	}
	if strings.TrimSpace(state.SessionTitle) != "" {
		return strings.TrimSpace(state.SessionTitle)
	}
	return strings.TrimSpace(state.SessionID)
}

func resolveLocalWorkdir(configured string) string {
	base := strings.TrimSpace(configured)
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "."
		}
		base = cwd
	}

	absPath, err := filepath.Abs(base)
	if err != nil {
		return base
	}

	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err == nil && strings.TrimSpace(resolvedPath) != "" {
		absPath = resolvedPath
	}

	info, err := os.Stat(absPath)
	if err != nil || !info.IsDir() {
		return absPath
	}
	return absPath
}

func loadRecentSessionHistory(ctx context.Context, providerClient provider.Provider, flowID string, sessionID string) []tui.HistoryEntry {
	trimmedFlowID := strings.TrimSpace(flowID)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if providerClient == nil || trimmedFlowID == "" || trimmedSessionID == "" {
		return nil
	}

	const historyLimit = 30
	messages, err := providerClient.ListSessionMessages(ctx, trimmedFlowID, trimmedSessionID, 0, historyLimit)
	if err != nil || messages == nil || len(messages.Items) == 0 {
		return nil
	}

	entries := make([]tui.HistoryEntry, 0, len(messages.Items))
	for index := len(messages.Items) - 1; index >= 0; index-- {
		item := messages.Items[index]
		content := strings.TrimSpace(item.Text)
		if content == "" {
			continue
		}
		role := mapMessageRole(item)
		entries = append(entries, tui.HistoryEntry{Role: role, Content: content})
	}
	return entries
}

func mapMessageRole(item provider.SessionMessage) string {
	if item.Error {
		return "error"
	}
	sender := strings.TrimSpace(strings.ToLower(item.Sender))
	switch sender {
	case strings.ToLower("User"):
		return "user"
	case strings.ToLower("Machine"):
		return "assistant"
	default:
		return "system"
	}
}

type tuiLogWriter struct {
	emit    func(tui.Event)
	pending string
}

func (w *tuiLogWriter) Write(content []byte) (int, error) {
	text := w.pending + string(content)
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		w.pending = lines[0]
		return len(content), nil
	}

	for _, line := range lines[:len(lines)-1] {
		trimmed := strings.TrimSpace(line)
		if summary, ok := parseTUIToolSummary(trimmed); ok {
			w.emit(tui.Event{Kind: tui.EventToolSummary, Text: summary})
			continue
		}
		if trimmed != "" && !shouldSuppressTUILogLine(trimmed) {
			w.emit(tui.Event{Kind: tui.EventSystemNote, Text: trimmed})
		}
	}
	w.pending = lines[len(lines)-1]
	return len(content), nil
}

func (w *tuiLogWriter) Flush() {
	trimmed := strings.TrimSpace(w.pending)
	if summary, ok := parseTUIToolSummary(trimmed); ok {
		w.emit(tui.Event{Kind: tui.EventToolSummary, Text: summary})
	} else if trimmed != "" && !shouldSuppressTUILogLine(trimmed) {
		w.emit(tui.Event{Kind: tui.EventSystemNote, Text: trimmed})
	}
	w.pending = ""
}

func shouldSuppressTUILogLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "[执行客户端命令]") || strings.HasPrefix(trimmed, "[本地工具]") || strings.HasPrefix(trimmed, "[本地命令]")
}

func parseTUIToolSummary(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "[本地工具]"):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "[本地工具]")), true
	case strings.HasPrefix(trimmed, "[本地命令]"):
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "[本地命令]")), true
	default:
		return "", false
	}
}

func requiresTUICommandConfirmation(config internalconfig.Config, payload map[string]any) bool {
	if strings.TrimSpace(config.ApprovalMode) != "confirm" {
		return false
	}
	commandPayload, ok := payload["command"].(map[string]any)
	if !ok {
		return false
	}
	policy, ok := commandPayload["policy"].(map[string]any)
	if !ok {
		return true
	}
	value, ok := policy["confirmation_required"]
	if !ok {
		return true
	}
	if required, ok := value.(bool); ok {
		return required
	}
	if text, ok := value.(string); ok {
		return strings.EqualFold(strings.TrimSpace(text), "true")
	}
	return true
}

func buildTUIConfirmRequest(payload map[string]any) tui.ConfirmRequest {
	request := tui.ConfirmRequest{
		Title:   "客户端命令待确认",
		Command: previewBuiltInCommand(payload),
		Detail:  "该命令由服务端下发，是否允许在本地执行？",
		Hint:    "使用方向键或 Tab 选择，Enter 确认。",
	}
	return request
}

func buildTUIClientToolConfirmRequest(payload map[string]any) tui.ConfirmRequest {
	toolName := nestedStringValue(payload, "function", "name")
	arguments, _ := nestedMapValue(payload, "function", "arguments")
	request := tui.ConfirmRequest{
		Title:   "客户端工具待确认",
		Command: clientruntime.PreviewClientToolCall(payload),
		Detail:  "该工具会修改本地工作区或 Git 状态，是否允许执行？",
		Hint:    "使用方向键或 Tab 选择，Enter 确认。",
	}
	switch strings.TrimSpace(toolName) {
	case "apply_patch":
		request.PreviewTitle = "补丁预览"
		request.PreviewBody = summarizePatchPreview(stringMapValue(arguments, "patch"))
	case "git_restore":
		request.PreviewTitle = "恢复目标"
		request.PreviewBody = summarizeGitRestorePreview(arguments)
	case "git_reset_soft":
		request.PreviewTitle = "软回退目标"
		request.PreviewBody = summarizeGitResetSoftPreview(arguments)
	case "git_add":
		request.PreviewTitle = "暂存目标"
		request.PreviewBody = summarizeGitAddPreview(arguments)
	case "git_commit":
		request.PreviewTitle = "提交草稿"
		request.PreviewBody = summarizeGitCommitPreview(arguments)
	case "write_file":
		request.PreviewTitle = "写入目标"
		request.PreviewBody = fmt.Sprintf("路径: %s\n模式: %s", fallbackString(stringMapValue(arguments, "path"), "."), fallbackString(stringMapValue(arguments, "mode"), "overwrite"))
	}
	return request
}

func buildTUISandboxEscapeConfirmRequest(path string, alwaysApprove func(context.Context) error) tui.ConfirmRequest {
	return tui.ConfirmRequest{
		Title:                       "访问工作目录外路径",
		Command:                     path,
		Detail:                      "目标路径位于当前工作目录外，是否允许本次访问？",
		PreviewTitle:                "访问目标",
		PreviewBody:                 fmt.Sprintf("路径: %s", path),
		Hint:                        "使用方向键或 Tab 选择，Enter 确认。",
		AlwaysApprove:               alwaysApprove,
		DisableDefaultAlwaysApprove: true,
	}
}

func emitTUIClientToolResultEvents(emit func(tui.Event), toolName string, result any, toolErr error) {
	if toolErr != nil {
		emit(tui.Event{Kind: tui.EventErrorNote, Text: errorText(toolErr)})
	}
	panel := buildTUIClientToolPanel(toolName, result, toolErr)
	if panel != nil {
		emit(tui.Event{Kind: tui.EventToolPanel, Panel: panel})
	}
}

func buildTUIClientToolPanel(toolName string, result any, toolErr error) *tui.ToolPanel {
	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil
	}
	switch strings.TrimSpace(toolName) {
	case "git_diff":
		content := strings.TrimSpace(stringMapValue(resultMap, "content"))
		if content == "" {
			return nil
		}
		body := content
		lineCount := intMapValue(resultMap, "line_count")
		if lineCount == 0 {
			lineCount = len(strings.Split(strings.TrimSpace(content), "\n"))
		}
		if boolMapValue(resultMap, "truncated") {
			body += "\n\n[diff 已按 max_bytes 截断]"
		}
		return &tui.ToolPanel{
			Title:   "Git Diff 预览",
			Summary: fmt.Sprintf("git_diff %s（%d 行）", fallbackString(stringMapValue(resultMap, "path"), "."), lineCount),
			Body:    body,
			Hint:    "支持滚动浏览完整 diff，关闭后可继续查看后续对话输出。",
		}
	case "apply_patch":
		conflicts := mapSliceValue(resultMap, "conflicts")
		if len(conflicts) == 0 && toolErr == nil {
			return nil
		}
		lines := []string{}
		for index, conflict := range conflicts {
			line := fmt.Sprintf("%d. %s", index+1, fallbackString(stringMapValue(conflict, "path"), "<unknown>"))
			reason := strings.TrimSpace(stringMapValue(conflict, "reason"))
			if reason != "" {
				line += "\n   原因: " + reason
			}
			if hunk := intMapValue(conflict, "hunk_index"); hunk > 0 {
				line += fmt.Sprintf("\n   hunk: %d", hunk)
			}
			if lineNumber := intMapValue(conflict, "line_number"); lineNumber > 0 {
				line += fmt.Sprintf("，行: %d", lineNumber)
			}
			if operation := strings.TrimSpace(stringMapValue(conflict, "operation")); operation != "" {
				line += "，操作: " + operation
			}
			expected := strings.TrimSpace(stringMapValue(conflict, "expected"))
			actual := strings.TrimSpace(stringMapValue(conflict, "actual"))
			if expected != "" || actual != "" {
				line += fmt.Sprintf("\n   期望: %s\n   实际: %s", fallbackString(expected, "<empty>"), fallbackString(actual, "<empty>"))
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			lines = append(lines, strings.TrimSpace(errorText(toolErr)))
		}
		return &tui.ToolPanel{
			Title:   "补丁冲突详情",
			Summary: fmt.Sprintf("apply_patch 冲突 %d 处", len(conflicts)),
			Body:    strings.Join(lines, "\n\n"),
			Hint:    "按 Enter 或 Esc 关闭，然后可继续让模型修正补丁。",
		}
	default:
		return nil
	}
}

func summarizePatchPreview(patch string) string {
	trimmed := strings.TrimSpace(patch)
	if trimmed == "" {
		return "补丁内容为空。"
	}
	files := []string{}
	for _, line := range strings.Split(trimmed, "\n") {
		if strings.HasPrefix(line, "+++ ") {
			path := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			path = strings.TrimPrefix(path, "b/")
			if path != "" {
				files = append(files, path)
			}
		}
	}
	if len(files) == 0 {
		return trimText(trimmed, 240)
	}
	preview := []string{fmt.Sprintf("涉及 %d 个文件:", len(files))}
	for _, file := range files {
		preview = append(preview, "- "+file)
	}
	return strings.Join(preview, "\n")
}

func summarizeGitAddPreview(arguments map[string]any) string {
	if boolMapValue(arguments, "all") {
		return fmt.Sprintf("范围: %s\n动作: git add -A", fallbackString(stringMapValue(arguments, "path"), "."))
	}
	paths := stringSliceMapValue(arguments, "paths")
	if len(paths) == 0 {
		paths = []string{fallbackString(stringMapValue(arguments, "path"), ".")}
	}
	return "将暂存以下路径:\n- " + strings.Join(paths, "\n- ")
}

func summarizeGitRestorePreview(arguments map[string]any) string {
	paths := stringSliceMapValue(arguments, "paths")
	if len(paths) == 0 {
		paths = []string{fallbackString(stringMapValue(arguments, "path"), ".")}
	}
	mode := []string{}
	if boolMapValue(arguments, "staged") {
		mode = append(mode, "staged")
	}
	if boolMapValue(arguments, "worktree") || len(mode) == 0 {
		mode = append(mode, "worktree")
	}
	parts := []string{fmt.Sprintf("恢复范围: %s", strings.Join(paths, ", ")), fmt.Sprintf("恢复位置: %s", strings.Join(mode, " + "))}
	if source := strings.TrimSpace(stringMapValue(arguments, "source")); source != "" {
		parts = append(parts, "来源引用: "+source)
	}
	return strings.Join(parts, "\n")
}

func summarizeGitResetSoftPreview(arguments map[string]any) string {
	return fmt.Sprintf("仓库定位: %s\n目标引用: %s\n动作: git reset --soft", fallbackString(stringMapValue(arguments, "path"), "."), fallbackString(stringMapValue(arguments, "target"), "<missing>"))
}

func summarizeGitCommitPreview(arguments map[string]any) string {
	message := strings.TrimSpace(stringMapValue(arguments, "message"))
	if message == "" {
		message = "<空消息>"
	}
	parts := []string{"提交信息:", message}
	if boolMapValue(arguments, "amend") {
		parts = append(parts, "", "模式: amend")
	}
	if boolMapValue(arguments, "allow_empty") {
		parts = append(parts, "允许空提交: true")
	}
	return strings.Join(parts, "\n")
}

func previewBuiltInCommand(payload map[string]any) string {
	commandPayload, ok := payload["command"].(map[string]any)
	if !ok {
		return "<未知命令>"
	}
	arguments, ok := commandPayload["arguments"].(map[string]any)
	if !ok {
		return "<未知命令>"
	}
	if rawCommand, ok := arguments["raw_command"].(string); ok && strings.TrimSpace(rawCommand) != "" {
		return strings.TrimSpace(rawCommand)
	}
	parts := []string{}
	if commandName, ok := arguments["command"].(string); ok && strings.TrimSpace(commandName) != "" {
		parts = append(parts, strings.TrimSpace(commandName))
	}
	if rawArgs, ok := arguments["args"].([]any); ok {
		for _, item := range rawArgs {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
	}
	if len(parts) == 0 {
		return "<未知命令>"
	}
	return strings.Join(parts, " ")
}

func submitTUIClientCallFailure(
	ctx context.Context,
	providerClient provider.Provider,
	payload map[string]any,
	errorMessage string,
) error {
	return submitClientCallFailure(ctx, providerClient, payload, errorMessage)
}

func stringMapValue(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprintf("%v", value)
}

func boolMapValue(payload map[string]any, key string) bool {
	value, ok := payload[key]
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

func intMapValue(payload map[string]any, key string) int {
	value, ok := payload[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int32:
		return int(typed)
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return 0
}

func nestedMapValue(payload map[string]any, keys ...string) (map[string]any, bool) {
	current := payload
	for index, key := range keys {
		value, ok := current[key]
		if !ok || value == nil {
			return nil, false
		}
		if index == len(keys)-1 {
			next, ok := value.(map[string]any)
			return next, ok
		}
		next, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

func mapSliceValue(payload map[string]any, key string) []map[string]any {
	value, ok := payload[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				result = append(result, mapped)
			}
		}
		return result
	default:
		return nil
	}
}

func stringSliceMapValue(payload map[string]any, key string) []string {
	value, ok := payload[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(fmt.Sprintf("%v", item))
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	default:
		return nil
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	trimmed := strings.TrimSpace(err.Error())
	return strings.TrimSpace(bracketErrorCodePattern.ReplaceAllString(trimmed, ""))
}

func fallbackString(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
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

func nestedStringValue(payload map[string]any, keys ...string) string {
	current := payload
	for index, key := range keys {
		value, ok := current[key]
		if !ok {
			return ""
		}
		if index == len(keys)-1 {
			return fmt.Sprintf("%v", value)
		}
		next, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

func buildTUIRequestMetrics(runID string, stats *provider.RunStats) *tui.RequestMetrics {
	if stats == nil {
		return nil
	}
	resolvedRunID := strings.TrimSpace(stats.RunID)
	if resolvedRunID == "" {
		resolvedRunID = runID
	}
	metrics := &tui.RequestMetrics{RunID: resolvedRunID}
	if stats.TokenUsage != nil {
		metrics.TokenUsage = &tui.TokenUsage{
			InputTokens:  stats.TokenUsage.InputTokens,
			OutputTokens: stats.TokenUsage.OutputTokens,
			TotalTokens:  stats.TokenUsage.TotalTokens,
			StopReason:   stats.TokenUsage.StopReason,
			ModelName:    stats.TokenUsage.ModelName,
		}
		if stats.TokenUsage.ContextWindow != nil {
			metrics.TokenUsage.ContextWindow = &tui.ContextWindow{
				MaxInputTokens:              stats.TokenUsage.ContextWindow.MaxInputTokens,
				ShortTermContextTokens:      stats.TokenUsage.ContextWindow.ShortTermContextTokens,
				CurrentRoundInputTokens:     stats.TokenUsage.ContextWindow.CurrentRoundInputTokens,
				NextRoundAvailableTokens:    stats.TokenUsage.ContextWindow.NextRoundAvailableTokens,
				CurrentRoundAvailableTokens: stats.TokenUsage.ContextWindow.CurrentRoundAvailableTokens,
				HistoryMessageCount:         stats.TokenUsage.ContextWindow.HistoryMessageCount,
				PreparedMessageCount:        stats.TokenUsage.ContextWindow.PreparedMessageCount,
			}
		}
	}
	if len(stats.Compressions) > 0 {
		latest := stats.Compressions[len(stats.Compressions)-1]
		metrics.Compression = &tui.CompressionStats{
			Count:            len(stats.Compressions),
			BeforeTokens:     latest.BeforeTokens,
			AfterTokens:      latest.AfterTokens,
			RemainingTokens:  latest.RemainingTokens,
			CompressionRatio: latest.CompressionRatio,
			TargetType:       latest.TargetType,
		}
	}
	return metrics
}

func parseSessionsPage(command string) (int, error) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 1 {
		return 1, nil
	}
	if len(fields) != 2 {
		return 0, fmt.Errorf("/sessions 用法: /sessions [页码]")
	}
	page, err := strconv.Atoi(strings.TrimSpace(fields[1]))
	if err != nil || page < 1 {
		return 0, fmt.Errorf("/sessions 页码必须是大于等于 1 的数字")
	}
	return page, nil
}

func (a *App) buildTUIConfigFormState(ctx context.Context, providerClient provider.Provider, snapshot tui.Snapshot) (*tui.ConfigFormState, error) {
	config, err := a.configManager.Load()
	if err != nil {
		return nil, err
	}

	flowOptions := []tui.ConfigChoice{}
	if snapshot.BackendAvailable && providerClient != nil {
		flows, flowErr := providerClient.ListFlows(ctx, "", "chat", 0, 100)
		if flowErr == nil && flows != nil {
			for _, item := range flows.Items {
				value := strings.TrimSpace(item.ID)
				if value == "" {
					value = strings.TrimSpace(item.Name)
				}
				if value == "" {
					continue
				}
				label := strings.TrimSpace(item.Name)
				if label == "" {
					label = value
				}
				flowOptions = append(flowOptions, tui.ConfigChoice{
					Value:       value,
					Label:       label,
					Description: strings.TrimSpace(item.Description),
				})
			}
		}
	}

	return &tui.ConfigFormState{
		Title:       "配置服务端连接",
		Hint:        "修改服务端地址、鉴权和运行默认项。保存后会立即刷新连接状态。",
		Path:        a.configManager.Path(),
		HasAPIKey:   strings.TrimSpace(config.APIKey) != "",
		SubmitText:  "Ctrl+S 保存配置",
		FlowOptions: flowOptions,
		Values: tui.ConfigFormValues{
			BackendBaseURL:           strings.TrimSpace(config.BackendBaseURL),
			DefaultFlow:              strings.TrimSpace(config.DefaultFlow),
			ApprovalMode:             strings.TrimSpace(config.ApprovalMode),
			Workdir:                  strings.TrimSpace(config.Workdir),
			StreamIdleTimeoutSeconds: strconv.Itoa(config.StreamIdleTimeoutSeconds),
			OutputLanguage:           strings.TrimSpace(config.OutputLanguage),
		},
	}, nil
}

func (a *App) applyConfigFormSubmission(submission tui.ConfigFormSubmission) (internalconfig.Config, error) {
	config, err := a.configManager.Load()
	if err != nil {
		return internalconfig.Config{}, err
	}

	if err := assignConfigValue(&config, "backend_base_url", submission.Values.BackendBaseURL); err != nil {
		return internalconfig.Config{}, err
	}

	if submission.ClearAPIKey {
		config.APIKey = ""
	} else if !submission.KeepAPIKey {
		trimmedAPIKey := strings.TrimSpace(submission.Values.APIKey)
		if trimmedAPIKey != "" {
			if err := assignConfigValue(&config, "api_key", trimmedAPIKey); err != nil {
				return internalconfig.Config{}, err
			}
		}
	}

	if strings.TrimSpace(submission.Values.DefaultFlow) == "" {
		config.DefaultFlow = ""
	} else if err := assignConfigValue(&config, "default_flow", submission.Values.DefaultFlow); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.ApprovalMode) == "" {
		config.ApprovalMode = internalconfig.Default().ApprovalMode
	} else if err := assignConfigValue(&config, "approval_mode", submission.Values.ApprovalMode); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.Workdir) == "" {
		config.Workdir = ""
	} else if err := assignConfigValue(&config, "workdir", submission.Values.Workdir); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.StreamIdleTimeoutSeconds) == "" {
		config.StreamIdleTimeoutSeconds = internalconfig.Default().StreamIdleTimeoutSeconds
	} else if err := assignConfigValue(&config, "stream_idle_timeout_seconds", submission.Values.StreamIdleTimeoutSeconds); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.OutputLanguage) == "" {
		config.OutputLanguage = internalconfig.Default().OutputLanguage
	} else if err := assignConfigValue(&config, "output_language", submission.Values.OutputLanguage); err != nil {
		return internalconfig.Config{}, err
	}

	if err := a.configManager.Save(config); err != nil {
		return internalconfig.Config{}, err
	}

	return config, nil
}

func totalPages(total int, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return (total + pageSize - 1) / pageSize
}

func nextSessionsPage(current int, pages int) int {
	if current < pages {
		return current + 1
	}
	if pages > 1 {
		return pages - 1
	}
	return 1
}

func formatRelativeTimeChinese(value time.Time) string {
	if value.IsZero() {
		return "刚刚"
	}
	delta := time.Since(value)
	if delta < 0 {
		delta = 0
	}
	if delta < time.Minute {
		return "刚刚"
	}
	if delta < time.Hour {
		return fmt.Sprintf("%d 分钟前", int(delta/time.Minute))
	}
	if delta < 24*time.Hour {
		return fmt.Sprintf("%d 小时前", int(delta/time.Hour))
	}
	if delta < 30*24*time.Hour {
		return fmt.Sprintf("%d 天前", int(delta/(24*time.Hour)))
	}
	if delta < 365*24*time.Hour {
		return fmt.Sprintf("%d 个月前", int(delta/(30*24*time.Hour)))
	}
	return fmt.Sprintf("%d 年前", int(delta/(365*24*time.Hour)))
}

func buildApprovalPicker(snapshot tui.Snapshot) *tui.ApprovalPickerState {
	current := strings.TrimSpace(snapshot.ApprovalMode)
	if current == "" {
		current = "confirm"
	}
	return &tui.ApprovalPickerState{
		Title: "命令执行模式",
		Hint:  "选择客户端命令的执行模式。",
		Options: []tui.ApprovalOption{
			{Value: "confirm", Label: "每次确认", Description: "执行客户端命令前逐次确认。", IsCurrent: current == "confirm"},
			{Value: "auto", Label: "自动执行", Description: "以后都同意，客户端命令默认自动执行。", IsCurrent: current == "auto"},
			{Value: "readonly", Label: "只读模式", Description: "禁止执行本地命令。", IsCurrent: current == "readonly"},
		},
	}
}
