package cmd

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

var bracketErrorCodePattern = regexp.MustCompile(`^\[[A-Z0-9_]+\]\s*`)

const skillsPickerCommandPrefix = "/skills-picker"

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
		persistSkillReviewAllowAll := func(context.Context) error {
			sessionState.SkillReviewPolicyOverride = internalconfig.SkillReviewPolicyIgnore
			return a.sessionStore.Save(sessionState)
		}
		sandboxEscapeConfirmer := func(path string) (bool, error) {
			return confirm(buildTUISandboxEscapeConfirmRequest(path, persistSandboxEscapeAlwaysAllow)), nil
		}
		runtimeClient.SetSandboxEscapeConfirmer(sandboxEscapeConfirmer)
		quietRuntimeClient.SetSandboxEscapeConfirmer(sandboxEscapeConfirmer)
		requestedSkillNames, clientSkillPackages, err := a.ensureSkillSupplyBeforeStream(
			runCtx,
			providerClient,
			config,
			currentFlow,
			request.Input,
			&sessionState,
			request.RequestMode,
			func(approval skillInstallApprovalRequest) bool {
				return confirm(buildTUISkillAutoInstallConfirmRequest(approval))
			},
			func(text string) {
				if strings.TrimSpace(text) != "" {
					emit(tui.Event{Kind: tui.EventProgressNote, Text: text})
				}
			},
		)
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
			ReasoningEnabled:       request.ReasoningEnabled,
			AgentAutoLoopEnabled:   resolveAgentAutoLoopEnabled(config, sessionState, request.RequestMode),
			AgentAutoLoopMaxRounds: resolveAgentAutoLoopMaxRounds(config, sessionState, request.RequestMode),
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
				if sessionTitle := firstNonEmptyTrimmedStringMapValue(event.Data, "session_name"); sessionTitle != "" {
					sessionState.SessionTitle = sessionTitle
					if strings.TrimSpace(sessionState.SessionID) != "" {
						if err := a.sessionStore.Save(sessionState); err != nil {
							return err
						}
					}
					emit(tui.Event{Kind: tui.EventSessionUpdate, SessionID: sessionState.SessionID, SessionTitle: sessionTitle})
				}
				if assistantChunkEvent, ok := buildTUIAssistantChunkEvent(event.Data); ok {
					emit(assistantChunkEvent)
					continue
				}
				if message, ok := event.Data["message"].(string); ok && strings.TrimSpace(message) != "" {
					emit(tui.Event{Kind: tui.EventProgressNote, Text: message})
				}
			case provider.EventCompression:
				if metrics := buildTUICompressionMetrics(event.Data); metrics != nil {
					emit(tui.Event{Kind: tui.EventStatsUpdate, Stats: metrics})
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
					if strings.TrimSpace(execConfig.ApprovalMode) != "readonly" {
						execConfig.ApprovalMode = "auto"
					}
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
				localConfig = applyEffectiveSkillReviewForPayload(localConfig, sessionState.ContextDir, sessionState, event.Data)
				emit(tui.Event{Kind: tui.EventClientToolCall, Text: clientruntime.PreviewClientToolCall(event.Data)})
				if clientruntime.RequiresClientToolConfirmation(localConfig, event.Data) {
					persistApprovedSkillReview := func(context.Context) error {
						skillName := resolveSkillReviewApprovalName(localConfig, sessionState.ContextDir, event.Data)
						if skillName == "" {
							return nil
						}
						appendApprovedSkillName(&sessionState, skillName)
						return a.sessionStore.Save(sessionState)
					}
					approved := confirm(buildTUIClientToolConfirmRequest(localConfig, sessionState.ContextDir, event.Data, persistApprovedSkillReview, persistSkillReviewAllowAll))
					if !approved {
						emit(tui.Event{Kind: tui.EventErrorNote, Text: "已拒绝本次客户端工具执行。"})
						if err := submitClientCallFailure(runCtx, providerClient, event.Data, "用户拒绝执行客户端工具"); err != nil {
							return err
						}
						continue
					}
					execConfig := localConfig
					if strings.TrimSpace(execConfig.ApprovalMode) != "readonly" {
						execConfig.ApprovalMode = "auto"
					}
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
			FlowID:                         flowID,
			FlowDisplayName:                strings.TrimSpace(current.DefaultFlowLabel),
			SessionID:                      strings.TrimSpace(sessionID),
			SessionTitle:                   "",
			Workdir:                        current.Workdir,
			RequestMode:                    normalizeRequestMode(firstNonEmpty(current.RequestMode, loadedState.RequestMode)),
			ContextDir:                     resolveLocalWorkdir(firstNonEmpty(loadedState.ContextDir, current.Workdir)),
			AgentAutoLoopEnabledOverride:   loadedState.AgentAutoLoopEnabledOverride,
			AgentAutoLoopMaxRoundsOverride: loadedState.AgentAutoLoopMaxRoundsOverride,
			SandboxEscapePolicyOverride:    loadedState.SandboxEscapePolicyOverride,
			SkillReviewPolicyOverride:      loadedState.SkillReviewPolicyOverride,
			ApprovedSkillNames:             loadedState.ApprovedSkillNames,
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
				"/review [路径] 发起当前工作区或指定路径的代码审查",
				"/agent 或 /agent <任务> 切换执行模式",
				"/ask 或 /ask <问题> 切换问答模式",
				"/plan 或 /plan <目标> 切换计划模式",
				"/reasoning [on|off|default|status] 切换当前会话思考模式",
				"/session 查看当前会话",
				"/sessions [页码] 查看当前智能体的服务端会话列表",
				"/config 打开全局与权限配置弹窗",
				"!<shell 命令> 在当前目录执行本地命令",
				"/agents 或 /agents <名称> 选择智能体",
				"/tasks [list|output|capture|stop|remove] 管理本地后台任务",
				"/skills [list|view|search|view-remote|install|remove|update|source|enable|disable|help] 管理本地技能与远程仓库技能",
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
					"/tasks 查看本地后台任务",
					"/skills 查看本地技能与远程仓库技能",
					"/reasoning 查看思考模式",
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
		case strings.EqualFold(trimmed, skillsPickerCommandPrefix) || strings.HasPrefix(strings.ToLower(trimmed), skillsPickerCommandPrefix+" "):
			localConfig, configErr := a.configManager.Load()
			if configErr != nil {
				return tui.LocalCommandResult{}, configErr
			}
			result, pickerErr := a.executeTUISkillsPickerCommand(localConfig, resolveTUISkillsContextDir(snapshot), trimmed)
			if pickerErr != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: pickerErr.Error()}}}, nil
			}
			return result, nil
		case strings.EqualFold(trimmed, "/skills") || strings.EqualFold(trimmed, "/skills list"):
			localConfig, configErr := a.configManager.Load()
			if configErr != nil {
				return tui.LocalCommandResult{}, configErr
			}
			picker, pickerErr := buildTUISkillsPicker(localConfig, resolveTUISkillsContextDir(snapshot), "")
			if pickerErr != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: pickerErr.Error()}}}, nil
			}
			return tui.LocalCommandResult{Handled: true, SkillsPicker: picker}, nil
		case strings.EqualFold(trimmed, "/skills help"):
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: skillsSlashUsage()}}}, nil
		case strings.EqualFold(trimmed, "/reasoning") || strings.HasPrefix(strings.ToLower(trimmed), "/reasoning "):
			output, updatedSnapshot, err := a.executeTUIReasoningCommand(trimmed, snapshot)
			if err != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
			}
			result := tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: output}}}
			if updatedSnapshot != nil {
				result.Snapshot = updatedSnapshot
			}
			return result, nil
		case isTasksSlashCommand(trimmed):
			localConfig, configErr := a.configManager.Load()
			if configErr != nil {
				return tui.LocalCommandResult{}, configErr
			}
			if isTasksSlashListCommand(trimmed) {
				picker, emptyMessage, pickerErr := buildTUITasksPicker(commandCtx, localConfig)
				if pickerErr != nil {
					return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: pickerErr.Error()}}}, nil
				}
				if picker != nil {
					return tui.LocalCommandResult{Handled: true, TasksPicker: picker}, nil
				}
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: emptyMessage}}}, nil
			}
			output, panel, commandErr := a.executeTasksSlashCommand(commandCtx, localConfig, trimmed)
			if commandErr != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: commandErr.Error()}}}, nil
			}
			events := []tui.Event{}
			if panel != nil {
				events = append(events, tui.Event{Kind: tui.EventToolPanel, Panel: panel})
			} else if strings.TrimSpace(output) != "" {
				events = append(events, tui.Event{Kind: tui.EventSystemNote, Text: output})
			}
			return tui.LocalCommandResult{Handled: true, Events: events}, nil
		case strings.EqualFold(trimmed, "/permissions") || strings.EqualFold(trimmed, "/permissions show") || strings.HasPrefix(strings.ToLower(trimmed), "/permissions "):
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "/permissions 已移除，请使用 /config 管理权限与执行配置。"}}}, nil
		case strings.EqualFold(trimmed, "/review") || strings.HasPrefix(strings.ToLower(trimmed), "/review "):
			target, reviewErr := parseReviewSlashCommand(trimmed)
			if reviewErr != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: reviewErr.Error()}}}, nil
			}
			resolvedWorkdir := resolveLocalWorkdir(snapshot.Workdir)
			prompt := a.buildReviewPrompt(ctx, resolvedWorkdir, target)
			updatedSnapshot := snapshot
			updatedSnapshot.RequestMode = reviewRequestMode()
			updatedSnapshot.ContextDir = resolvedWorkdir
			return tui.LocalCommandResult{
				Handled:     true,
				Snapshot:    &updatedSnapshot,
				QueuedInput: strings.TrimSpace(prompt),
				Events: []tui.Event{{
					Kind: tui.EventSystemNote,
					Text: "已切换为问答只读模式，正在发起代码审查。",
				}},
			}, nil
		case strings.HasPrefix(strings.ToLower(trimmed), "/skills "):
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
						FlowID:                         snapshot.DefaultFlow,
						FlowDisplayName:                strings.TrimSpace(snapshot.DefaultFlowLabel),
						SessionID:                      snapshot.SessionID,
						SessionTitle:                   strings.TrimSpace(snapshot.SessionTitle),
						Workdir:                        snapshot.Workdir,
						RequestMode:                    mode,
						ContextDir:                     updatedSnapshot.ContextDir,
						ReasoningOverride:              currentSessionState.ReasoningOverride,
						AgentAutoLoopEnabledOverride:   currentSessionState.AgentAutoLoopEnabledOverride,
						AgentAutoLoopMaxRoundsOverride: currentSessionState.AgentAutoLoopMaxRoundsOverride,
						SandboxEscapePolicyOverride:    currentSessionState.SandboxEscapePolicyOverride,
						SkillReviewPolicyOverride:      currentSessionState.SkillReviewPolicyOverride,
						ApprovedSkillNames:             currentSessionState.ApprovedSkillNames,
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
					Title:      fmt.Sprintf("会话列表(session list)（%s）", sanitizeFlowDisplay(agentLabel)),
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
			updatedSnapshot.History = nil
			updatedSnapshot.RequestStats = nil
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
		ReasoningEnabled:    config.ReasoningEnabled,
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
		snapshot.ReasoningOverride = strings.TrimSpace(sessionState.ReasoningOverride)
		snapshot.ReasoningEnabled = resolveReasoningEnabled(config, sessionState.ReasoningOverride)
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
	snapshot.ReasoningOverride = strings.TrimSpace(sessionState.ReasoningOverride)
	snapshot.ReasoningEnabled = resolveReasoningEnabled(config, sessionState.ReasoningOverride)
	snapshot.SessionSandboxEscapePolicy = strings.TrimSpace(sessionState.SandboxEscapePolicyOverride)
	if strings.TrimSpace(snapshot.SessionID) != "" {
		title, titleErr := lookupSessionTitle(ctx, providerClient, flowID, snapshot.SessionID)
		if titleErr == nil {
			snapshot.SessionTitle = strings.TrimSpace(title)
			sessionState.SessionTitle = snapshot.SessionTitle
		}
		snapshot.History = loadRecentSessionHistory(ctx, providerClient, flowID, snapshot.SessionID)
		snapshot.RequestStats = loadLatestSessionMetrics(ctx, providerClient, flowID, snapshot.SessionID)
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

func loadLatestSessionMetrics(ctx context.Context, providerClient provider.Provider, flowID string, sessionID string) *tui.RequestMetrics {
	runMetrics := loadLatestRunMetrics(ctx, providerClient, flowID, sessionID)
	sessionMetrics := loadSessionContextMetrics(ctx, providerClient, flowID, sessionID)
	return mergeSessionMetrics(runMetrics, sessionMetrics)
}

func loadLatestRunMetrics(ctx context.Context, providerClient provider.Provider, flowID string, sessionID string) *tui.RequestMetrics {
	trimmedFlowID := strings.TrimSpace(flowID)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if providerClient == nil || trimmedFlowID == "" || trimmedSessionID == "" {
		return nil
	}

	const statsMessageLimit = 30
	messages, err := providerClient.ListSessionMessages(ctx, trimmedFlowID, trimmedSessionID, 0, statsMessageLimit)
	if err != nil || messages == nil || len(messages.Items) == 0 {
		return nil
	}

	latestRunID := ""
	for _, item := range messages.Items {
		if trimmedRunID := strings.TrimSpace(item.RunID); trimmedRunID != "" {
			latestRunID = trimmedRunID
			break
		}
	}
	if latestRunID == "" {
		return nil
	}

	stats, err := providerClient.GetRunStats(ctx, trimmedFlowID, trimmedSessionID, latestRunID)
	if err != nil || stats == nil {
		return nil
	}
	return buildTUIRequestMetrics(latestRunID, stats)
}

func loadSessionContextMetrics(ctx context.Context, providerClient provider.Provider, flowID string, sessionID string) *tui.RequestMetrics {
	trimmedFlowID := strings.TrimSpace(flowID)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if providerClient == nil || trimmedFlowID == "" || trimmedSessionID == "" {
		return nil
	}
	preview, err := providerClient.GetSessionContextWindow(ctx, trimmedFlowID, trimmedSessionID)
	if err != nil || preview == nil || preview.ContextWindow == nil {
		return nil
	}
	metrics := &tui.RequestMetrics{
		TokenUsage: &tui.TokenUsage{
			ContextWindow: &tui.ContextWindow{
				MaxInputTokens:              preview.ContextWindow.MaxInputTokens,
				ShortTermContextTokens:      preview.ContextWindow.ShortTermContextTokens,
				CurrentRoundInputTokens:     preview.ContextWindow.CurrentRoundInputTokens,
				NextRoundAvailableTokens:    preview.ContextWindow.NextRoundAvailableTokens,
				CurrentRoundAvailableTokens: preview.ContextWindow.CurrentRoundAvailableTokens,
				HistoryMessageCount:         preview.ContextWindow.HistoryMessageCount,
				PreparedMessageCount:        preview.ContextWindow.PreparedMessageCount,
				HighWatermarkRatio:          preview.ContextWindow.HighWatermarkRatio,
				CompressionTargetRatio:      preview.ContextWindow.CompressionTargetRatio,
				HighWatermarkTokens:         preview.ContextWindow.HighWatermarkTokens,
				CompressionTargetTokens:     preview.ContextWindow.CompressionTargetTokens,
			},
		},
	}
	if preview.LatestCompression != nil {
		metrics.Compression = &tui.CompressionStats{
			Count:            1,
			BeforeTokens:     preview.LatestCompression.BeforeTokens,
			AfterTokens:      preview.LatestCompression.AfterTokens,
			RemainingTokens:  preview.LatestCompression.RemainingTokens,
			CompressionRatio: preview.LatestCompression.CompressionRatio,
			TargetType:       preview.LatestCompression.TargetType,
		}
	}
	return metrics
}

func mergeSessionMetrics(runMetrics *tui.RequestMetrics, sessionMetrics *tui.RequestMetrics) *tui.RequestMetrics {
	if runMetrics == nil {
		return sessionMetrics
	}
	if sessionMetrics == nil {
		return runMetrics
	}
	merged := &tui.RequestMetrics{
		RunID:         runMetrics.RunID,
		StartedAt:     runMetrics.StartedAt,
		CompletedAt:   runMetrics.CompletedAt,
		InputChars:    runMetrics.InputChars,
		OutputChars:   runMetrics.OutputChars,
		ChunkCount:    runMetrics.ChunkCount,
		ToolCallCount: runMetrics.ToolCallCount,
		CommandCount:  runMetrics.CommandCount,
	}
	if runMetrics.TokenUsage != nil {
		tokenUsage := *runMetrics.TokenUsage
		merged.TokenUsage = &tokenUsage
		if runMetrics.TokenUsage.ContextWindow != nil {
			contextWindow := *runMetrics.TokenUsage.ContextWindow
			merged.TokenUsage.ContextWindow = &contextWindow
		}
	}
	if runMetrics.Compression != nil {
		compression := *runMetrics.Compression
		merged.Compression = &compression
	}
	if sessionMetrics.TokenUsage != nil && sessionMetrics.TokenUsage.ContextWindow != nil {
		if merged.TokenUsage == nil {
			merged.TokenUsage = &tui.TokenUsage{}
		}
		contextWindow := *sessionMetrics.TokenUsage.ContextWindow
		merged.TokenUsage.ContextWindow = &contextWindow
	}
	if merged.Compression == nil && sessionMetrics.Compression != nil {
		compression := *sessionMetrics.Compression
		merged.Compression = &compression
	}
	return merged
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
		Title:        "客户端命令(client command)待确认",
		Command:      previewBuiltInCommand(payload),
		Detail:       "该命令由服务端下发，是否允许在本地执行？",
		Hint:         "Y 执行本次命令，N 取消本次命令；方向键或 Tab 选择，Enter 确认。",
		ApproveLabel: "执行本次命令",
		RejectLabel:  "取消本次命令",
	}
	return request
}

func buildTUIClientToolConfirmRequest(
	config internalconfig.Config,
	contextDir string,
	payload map[string]any,
	approveSkill func(context.Context) error,
	allowAllSkills func(context.Context) error,
) tui.ConfirmRequest {
	toolName := nestedStringValue(payload, "function", "name")
	arguments, _ := nestedMapValue(payload, "function", "arguments")
	request := tui.ConfirmRequest{
		Title:        "客户端工具(client tool)待确认",
		Command:      clientruntime.PreviewClientToolCall(payload),
		Detail:       "该工具会修改本地工作区或本地运行状态，是否允许执行？",
		Hint:         "Y 允许本次执行，N 本次拒绝；方向键或 Tab 选择，Enter 确认。",
		ApproveLabel: "允许本次执行",
		RejectLabel:  "本次拒绝",
	}
	switch strings.TrimSpace(toolName) {
	case "skill":
		request.Title = "技能包审查(skill package review)"
		request.Detail = "该技能会把本地技能包内容注入当前会话，确认后即可在当前对话中使用。"
		request.PreviewTitle = "技能摘要(skill summary)"
		request.PreviewBody = buildSkillReviewPreview(config, contextDir, fallbackString(stringMapValue(arguments, "name"), "<missing>"))
		request.ApproveLabel = "本次允许"
		request.RejectLabel = "本次拒绝"
		request.AlwaysApproveLabel = "始终允许此技能"
		request.AlwaysApprove = approveSkill
		request.AllowAllLabel = "允许全部技能"
		request.AllowAll = allowAllSkills
		request.Hint = "Y 允许本次，N 本次拒绝，S 始终允许此技能，A 允许全部技能；方向键或 Tab 选择，Enter 确认。"
		request.DisableDefaultAlwaysApprove = true
	case "file_edit", "write_file":
		request.Title = "写入比较摘要(write summary)待确认"
		request.Detail = "该工具会修改本地文件，是否允许本次写入？"
		request.PreviewTitle = "比较摘要(compare summary)"
		request.PreviewBody = buildWriteConfirmationPreview(strings.TrimSpace(toolName), arguments)
		request.ApproveLabel = "确认本次写入"
		request.RejectLabel = "取消本次写入"
		request.Hint = "Y 确认本次写入，N 取消本次写入；方向键或 Tab 选择，Enter 确认。"
		request.DisableDefaultAlwaysApprove = true
	}
	return request
}

func buildSkillReviewPreview(config internalconfig.Config, contextDir string, name string) string {
	previewConfig := config
	if strings.TrimSpace(contextDir) != "" {
		previewConfig.Workdir = contextDir
	}
	item, _, err := skills.LoadByName(previewConfig, name)
	if err != nil || item == nil {
		return fmt.Sprintf("名称: %s", fallbackString(name, "<missing>"))
	}
	lines := []string{fmt.Sprintf("名称: %s", item.Name)}
	if strings.TrimSpace(item.Source) != "" {
		lines = append(lines, fmt.Sprintf("来源: %s", item.Source))
	}
	if strings.TrimSpace(item.RelativeDir) != "" {
		lines = append(lines, fmt.Sprintf("相对目录: %s", item.RelativeDir))
	}
	if strings.TrimSpace(item.Path) != "" {
		lines = append(lines, fmt.Sprintf("技能文件: %s", item.Path))
	}
	if strings.TrimSpace(item.Summary) != "" {
		lines = append(lines, fmt.Sprintf("摘要: %s", trimText(item.Summary, 240)))
	} else if strings.TrimSpace(item.Description) != "" {
		lines = append(lines, fmt.Sprintf("摘要: %s", trimText(item.Description, 240)))
	}
	return strings.Join(lines, "\n")
}

func buildWriteConfirmationPreview(toolName string, arguments map[string]any) string {
	path := fallbackString(stringMapValue(arguments, "path"), ".")
	mode := fallbackString(stringMapValue(arguments, "mode"), "overwrite")
	lines := []string{fmt.Sprintf("路径: %s", path)}
	if toolName == "write_file" {
		lines = append(lines, fmt.Sprintf("模式: %s", mode))
		if content := trimText(stringMapValue(arguments, "content"), 240); content != "" {
			lines = append(lines, fmt.Sprintf("内容摘要: %s", content))
		}
		return strings.Join(lines, "\n")
	}
	oldString := trimText(stringMapValue(arguments, "old_string"), 120)
	newString := trimText(stringMapValue(arguments, "new_string"), 120)
	if oldString != "" {
		lines = append(lines, fmt.Sprintf("编辑前: %s", oldString))
	}
	if newString != "" {
		lines = append(lines, fmt.Sprintf("编辑后: %s", newString))
	}
	if expectedHash := strings.TrimSpace(stringMapValue(arguments, "expected_sha256_before")); expectedHash != "" {
		lines = append(lines, fmt.Sprintf("编辑前摘要: %s", expectedHash))
	}
	return strings.Join(lines, "\n")
}

func buildTUISandboxEscapeConfirmRequest(path string, alwaysApprove func(context.Context) error) tui.ConfirmRequest {
	return tui.ConfirmRequest{
		Title:                       "工作目录外路径访问(sandbox escape)",
		Command:                     path,
		Detail:                      "目标路径位于当前工作目录外，是否允许本次访问？",
		PreviewTitle:                "访问目标(target path)",
		PreviewBody:                 fmt.Sprintf("路径: %s", path),
		Hint:                        "Y 允许本次访问，N 本次拒绝，A 允许全部外部路径；方向键或 Tab 选择，Enter 确认。",
		ApproveLabel:                "允许本次访问",
		RejectLabel:                 "本次拒绝",
		AlwaysApproveLabel:          "允许全部外部路径",
		AlwaysApprove:               alwaysApprove,
		DisableDefaultAlwaysApprove: true,
	}
}

func buildTUISkillAutoInstallConfirmRequest(request skillInstallApprovalRequest) tui.ConfirmRequest {
	previewLines := []string{
		fmt.Sprintf("技能: %s (%s)", candidateLabel(request.Candidate), request.Candidate.Slug),
		fmt.Sprintf("安装位置: %s", skillInstallScopeLabelText(request.Scope)),
		fmt.Sprintf("目标目录: %s", request.InstallPath),
	}
	if strings.TrimSpace(request.Candidate.Summary) != "" {
		previewLines = append(previewLines, fmt.Sprintf("摘要: %s", request.Candidate.Summary))
	}
	return tui.ConfirmRequest{
		Title:        "自动安装技能(skill auto install)待确认",
		Command:      fmt.Sprintf("install %s", request.Candidate.Slug),
		Detail:       fallbackString(strings.TrimSpace(request.Reason), "本轮请求命中了可补装的远程技能，确认后会先安装再继续发送请求。"),
		PreviewTitle: "安装预览(install preview)",
		PreviewBody:  strings.Join(previewLines, "\n"),
		Hint:         "Y 安装并继续，N 跳过本次安装；方向键或 Tab 选择，Enter 确认。",
		ApproveLabel: "安装并继续",
		RejectLabel:  "跳过安装",
	}
}

func applyEffectiveSkillReviewForPayload(config internalconfig.Config, contextDir string, sessionState agent.SessionState, payload map[string]any) internalconfig.Config {
	updated := applyEffectiveSkillReviewPolicy(config, sessionState.SkillReviewPolicyOverride)
	if !strings.EqualFold(strings.TrimSpace(nestedStringValue(payload, "function", "name")), "skill") {
		return updated
	}
	if len(sessionState.ApprovedSkillNames) == 0 {
		return updated
	}
	approvedName := resolveSkillReviewApprovalName(updated, contextDir, payload)
	if approvedName == "" {
		return updated
	}
	for _, item := range sessionState.ApprovedSkillNames {
		if strings.EqualFold(strings.TrimSpace(item), approvedName) {
			updated.SkillReviewPolicy = internalconfig.SkillReviewPolicyIgnore
			break
		}
	}
	return updated
}

func resolveSkillReviewApprovalName(config internalconfig.Config, contextDir string, payload map[string]any) string {
	arguments := map[string]any{}
	if payload != nil {
		arguments, _ = nestedMapValue(payload, "function", "arguments")
	}
	name := strings.TrimSpace(fallbackString(stringMapValue(arguments, "name"), ""))
	if name == "" {
		return ""
	}
	previewConfig := config
	if strings.TrimSpace(contextDir) != "" {
		previewConfig.Workdir = contextDir
	}
	item, _, err := skills.LoadByName(previewConfig, name)
	if err == nil && item != nil && strings.TrimSpace(item.Name) != "" {
		return strings.TrimSpace(item.Name)
	}
	return name
}

func appendApprovedSkillName(state *agent.SessionState, name string) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return
	}
	for _, item := range state.ApprovedSkillNames {
		if strings.EqualFold(strings.TrimSpace(item), trimmed) {
			return
		}
	}
	state.ApprovedSkillNames = append(state.ApprovedSkillNames, trimmed)
}

func emitTUIClientToolResultEvents(emit func(tui.Event), toolName string, result any, toolErr error) {
	if toolErr != nil {
		emit(tui.Event{Kind: tui.EventErrorNote, Text: errorText(toolErr)})
	}
	if strings.TrimSpace(toolName) == "skill" {
		if toolErr == nil {
			skillName := ""
			if resultMap, ok := result.(map[string]any); ok {
				skillName = strings.TrimSpace(stringMapValue(resultMap, "name"))
			}
			text := "技能已装载。"
			if skillName != "" {
				text = "技能已装载：" + skillName
			}
			emit(tui.Event{Kind: tui.EventSystemNote, Text: text})
		}
		return
	}
	panel := buildTUIClientToolPanel(toolName, result, toolErr)
	if panel != nil {
		if statusText := buildToolPanelStatusText(panel, ""); statusText != "" {
			emit(tui.Event{Kind: tui.EventSystemNote, Text: statusText})
		}
		emit(tui.Event{Kind: tui.EventToolPanel, Panel: panel})
	}
}

func buildToolPanelStatusText(panel *tui.ToolPanel, fallback string) string {
	if panel == nil {
		return strings.TrimSpace(fallback)
	}
	if summary := strings.TrimSpace(panel.Summary); summary != "" {
		return "结果已生成：" + summary
	}
	if title := strings.TrimSpace(panel.Title); title != "" {
		return "结果已生成：" + title
	}
	return strings.TrimSpace(fallback)
}

func buildTUIClientToolPanel(toolName string, result any, toolErr error) *tui.ToolPanel {
	resultMap, ok := result.(map[string]any)
	if !ok {
		return nil
	}
	switch strings.TrimSpace(toolName) {
	case "lsp_query":
		items := mapSliceValue(resultMap, "items")
		if len(items) == 0 {
			return nil
		}
		lines := make([]string, 0, len(items))
		for index, item := range items {
			line := fmt.Sprintf("%d. %s", index+1, fallbackString(stringMapValue(item, "name"), fallbackString(stringMapValue(item, "path"), fallbackString(stringMapValue(item, "uri"), "<unknown>"))))
			if path := strings.TrimSpace(stringMapValue(item, "path")); path != "" {
				line += "\n   路径: " + path
			}
			if rangeValue, ok := item["range"].(map[string]any); ok {
				line += fmt.Sprintf("\n   位置: %d:%d", intMapValue(rangeValue, "start_line"), intMapValue(rangeValue, "start_character"))
			}
			lines = append(lines, line)
		}
		return &tui.ToolPanel{
			Title:   "语言服务查询(lsp_query)结果",
			Summary: fmt.Sprintf("%s 命中 %d 项", fallbackString(stringMapValue(resultMap, "operation"), "lsp_query"), len(items)),
			Body:    strings.Join(lines, "\n\n"),
			Hint:    "若结果为空，请确认语言服务器命令、工作区根目录和光标位置是否正确。",
		}
	case "task_output":
		body := strings.TrimSpace(stringMapValue(resultMap, "stdout"))
		stderr := strings.TrimSpace(stringMapValue(resultMap, "stderr"))
		if stderr != "" {
			if body != "" {
				body += "\n\n"
			}
			body += "stderr:\n" + stderr
		}
		if body == "" {
			body = "当前任务尚无输出。"
		}
		return &tui.ToolPanel{
			Title:   "后台任务输出(task_output)",
			Summary: fmt.Sprintf("%s · %s", fallbackString(stringMapValue(resultMap, "task_id"), "<unknown>"), fallbackString(stringMapValue(resultMap, "status"), "unknown")),
			Body:    body,
			Hint:    "可滚动查看 stdout/stderr；继续对话时可再次调用 task_output 刷新。",
		}
	case "terminal_capture":
		body := strings.TrimSpace(stringMapValue(resultMap, "content"))
		if body == "" {
			body = "当前任务尚无终端输出。"
		}
		return &tui.ToolPanel{
			Title:   "终端捕获(terminal_capture)",
			Summary: fmt.Sprintf("%s · %s · %s", fallbackString(stringMapValue(resultMap, "task_id"), "<unknown>"), fallbackString(stringMapValue(resultMap, "status"), "unknown"), fallbackString(stringMapValue(resultMap, "stream"), "both")),
			Body:    body,
			Hint:    "适合持续查看终端视角输出；需要结构化状态时继续使用 task_output。",
		}
	case "task_list":
		tasks := mapSliceValue(resultMap, "tasks")
		if len(tasks) == 0 {
			return nil
		}
		lines := make([]string, 0, len(tasks))
		for index, item := range tasks {
			line := fmt.Sprintf("%d. %s [%s]\n   %s", index+1, fallbackString(stringMapValue(item, "task_name"), fallbackString(stringMapValue(item, "task_id"), "<unknown>")), fallbackString(stringMapValue(item, "status"), "unknown"), fallbackString(stringMapValue(item, "command"), "<unknown>"))
			if taskID := strings.TrimSpace(stringMapValue(item, "task_id")); taskID != "" {
				line += "\n   task_id: " + taskID
			}
			if sessionID := strings.TrimSpace(stringMapValue(item, "session_id")); sessionID != "" {
				line += "\n   session_id: " + sessionID
			}
			if cwd := strings.TrimSpace(stringMapValue(item, "cwd")); cwd != "" {
				line += "\n   cwd: " + cwd
			}
			lines = append(lines, line)
		}
		return &tui.ToolPanel{
			Title:   "后台任务列表",
			Summary: fmt.Sprintf("task_list %d 个任务", len(tasks)),
			Body:    strings.Join(lines, "\n\n"),
			Hint:    "可根据 task_id 继续读取输出、停止任务或删除已结束任务，也可用 /tasks cleanup 批量清理已结束任务。",
		}
	case "task_delete":
		return &tui.ToolPanel{
			Title:   "后台任务删除",
			Summary: fmt.Sprintf("%s · %s", fallbackString(stringMapValue(resultMap, "task_id"), "<unknown>"), fallbackString(stringMapValue(resultMap, "status"), "unknown")),
			Body:    "任务及其持久化输出已删除。",
			Hint:    "再次查看时，该任务不会出现在 /tasks list 中。",
		}
	case "task_cleanup":
		deletedCount := intMapValue(resultMap, "deleted_count")
		runningCount := intMapValue(resultMap, "running_count")
		body := fmt.Sprintf("已清理 %d 个已结束任务。", deletedCount)
		deletedTaskIDs := stringSliceMapValue(resultMap, "deleted_task_ids")
		if len(deletedTaskIDs) > 0 {
			body += "\n\n已清理任务:\n- " + strings.Join(deletedTaskIDs, "\n- ")
		}
		if runningCount > 0 {
			body += fmt.Sprintf("\n\n保留 %d 个运行中任务。", runningCount)
		}
		return &tui.ToolPanel{
			Title:   "后台任务清理",
			Summary: fmt.Sprintf("已清理 %d 个任务", deletedCount),
			Body:    body,
			Hint:    "该操作只会删除已结束任务；运行中的任务会被保留。",
		}
	case "todo_write":
		items := mapSliceValue(resultMap, "items")
		lines := make([]string, 0, len(items))
		for index, item := range items {
			line := fmt.Sprintf("%d. [%s] %s", index+1, fallbackString(stringMapValue(item, "status"), "not_started"), fallbackString(stringMapValue(item, "title"), "<empty>"))
			if notes := strings.TrimSpace(stringMapValue(item, "notes")); notes != "" {
				line += "\n   " + notes
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			lines = append(lines, "当前会话暂无 Todo。")
		}
		return &tui.ToolPanel{
			Title:   "会话待办(todo_write)",
			Summary: fmt.Sprintf("%s %d 项", toolName, len(items)),
			Body:    strings.Join(lines, "\n\n"),
			Hint:    "Todo 会保存在当前会话状态中，后续请求可继续覆盖更新。",
		}
	case "skill":
		if prompt := strings.TrimSpace(stringMapValue(resultMap, "prompt")); prompt != "" {
			return &tui.ToolPanel{
				Title:   "技能装载(skill)结果",
				Summary: fallbackString(stringMapValue(resultMap, "name"), "skill"),
				Body:    prompt,
				Hint:    "这是当前技能的提示主体，可作为后续执行上下文直接使用。",
			}
		}
		return nil
	case "file_edit":
		if toolErr != nil {
			bodyLines := []string{
				fmt.Sprintf("目标文件: %s", fallbackString(stringMapValue(resultMap, "path"), ".")),
				fmt.Sprintf("状态: %s", fallbackString(stringMapValue(resultMap, "status"), "failed")),
				fmt.Sprintf("摘要前: %s", fallbackString(stringMapValue(resultMap, "sha256_before"), "<missing>")),
			}
			if startLine := intMapValue(resultMap, "suggested_read_start_line"); startLine > 0 {
				endLine := intMapValue(resultMap, "suggested_read_end_line")
				if endLine < startLine {
					endLine = startLine
				}
				bodyLines = append(bodyLines, fmt.Sprintf("建议先读: read_file %s 行 %d-%d", fallbackString(stringMapValue(resultMap, "path"), "."), startLine, endLine))
			}
			if excerpt := strings.TrimSpace(stringMapValue(resultMap, "nearby_excerpt")); excerpt != "" {
				bodyLines = append(bodyLines, "", "附近内容:", excerpt)
			}
			if prefetchedRead, ok := resultMap["prefetched_read"].(map[string]any); ok {
				prefetchedContent := strings.TrimSpace(stringMapValue(prefetchedRead, "content"))
				if prefetchedContent != "" {
					startLine := intMapValue(prefetchedRead, "start_line")
					endLine := intMapValue(prefetchedRead, "end_line")
					sectionTitle := "预取上下文:"
					if startLine > 0 && endLine >= startLine {
						sectionTitle = fmt.Sprintf("预取上下文(%d-%d 行):", startLine, endLine)
					}
					bodyLines = append(bodyLines, "", sectionTitle, prefetchedContent)
				}
			}
			if rawSuggestions, ok := resultMap["suggestions"].([]string); ok && len(rawSuggestions) > 0 {
				bodyLines = append(bodyLines, "", "建议:")
				for _, item := range rawSuggestions {
					bodyLines = append(bodyLines, "- "+item)
				}
			} else if rawSuggestions, ok := resultMap["suggestions"].([]any); ok && len(rawSuggestions) > 0 {
				bodyLines = append(bodyLines, "", "建议:")
				for _, item := range rawSuggestions {
					bodyLines = append(bodyLines, "- "+fmt.Sprintf("%v", item))
				}
			}
			return &tui.ToolPanel{
				Title:   "文件编辑(file_edit)失败",
				Summary: strings.TrimSpace(errorText(toolErr)),
				Body:    strings.Join(bodyLines, "\n"),
				Hint:    "先 read_file 再复制精确片段，通常比直接重试 file_edit 更有效。",
			}
		}
		return &tui.ToolPanel{
			Title:   "文件编辑(file_edit)摘要",
			Summary: fmt.Sprintf("file_edit %s 替换 %d 处", fallbackString(stringMapValue(resultMap, "path"), "."), intMapValue(resultMap, "replacements")),
			Body:    fmt.Sprintf("编辑前: %s\n编辑后: %s\n摘要前: %s\n摘要后: %s", fallbackString(stringMapValue(resultMap, "preview_before"), "<empty>"), fallbackString(stringMapValue(resultMap, "preview_after"), "<empty>"), fallbackString(stringMapValue(resultMap, "sha256_before"), "<missing>"), fallbackString(stringMapValue(resultMap, "sha256_after"), "<missing>")),
			Hint:    "若替换范围过大，可改用更精确的 old_string 或关闭 replace_all。",
		}
	default:
		return nil
	}
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

func buildTUIAssistantChunkEvent(payload map[string]any) (tui.Event, bool) {
	chunk := firstNonEmptyRawStringMapValue(payload, "chunk", "text")
	if chunk == "" {
		return tui.Event{}, false
	}
	return tui.Event{
		Kind:      tui.EventAssistantChunk,
		Text:      chunk,
		MessageID: firstNonEmptyTrimmedStringMapValue(payload, "message_id", "id"),
		Stage:     strings.TrimSpace(stringMapValue(payload, "stage")),
		EventType: strings.TrimSpace(stringMapValue(payload, "event_type")),
	}, true
}

func firstNonEmptyRawStringMapValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyTrimmedStringMapValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringMapValue(payload, key)); value != "" {
			return value
		}
	}
	return ""
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
				HighWatermarkRatio:          stats.TokenUsage.ContextWindow.HighWatermarkRatio,
				CompressionTargetRatio:      stats.TokenUsage.ContextWindow.CompressionTargetRatio,
				HighWatermarkTokens:         stats.TokenUsage.ContextWindow.HighWatermarkTokens,
				CompressionTargetTokens:     stats.TokenUsage.ContextWindow.CompressionTargetTokens,
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

func buildTUICompressionMetrics(payload map[string]any) *tui.RequestMetrics {
	if len(payload) == 0 {
		return nil
	}
	beforeTokens := payloadIntValue(payload, "before_tokens")
	afterTokens := payloadIntValue(payload, "after_tokens")
	remainingTokens := payloadIntValue(payload, "remaining_tokens")
	compressionRatio := payloadFloatValue(payload, "compression_ratio")
	targetType := strings.TrimSpace(stringMapValue(payload, "target_type"))
	if beforeTokens == 0 && afterTokens == 0 && remainingTokens == 0 && compressionRatio == 0 && targetType == "" {
		return nil
	}
	count := payloadIntValue(payload, "count")
	if count <= 0 {
		count = 1
	}
	return &tui.RequestMetrics{
		Compression: &tui.CompressionStats{
			Count:            count,
			BeforeTokens:     beforeTokens,
			AfterTokens:      afterTokens,
			RemainingTokens:  remainingTokens,
			CompressionRatio: compressionRatio,
			TargetType:       targetType,
		},
	}
}

func payloadIntValue(payload map[string]any, key string) int {
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
	case float32:
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

func payloadFloatValue(payload map[string]any, key string) float64 {
	value, ok := payload[key]
	if !ok || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case float32:
		return float64(typed)
	case float64:
		return typed
	case int:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err == nil {
			return parsed
		}
	}
	return 0
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
		Title:       "服务端连接配置(server connection)",
		Hint:        "修改服务端地址、鉴权、思考模式和运行默认项。保存后会立即刷新连接状态。",
		Path:        a.configManager.Path(),
		HasAPIKey:   strings.TrimSpace(config.APIKey) != "",
		SubmitText:  "保存配置",
		FlowOptions: flowOptions,
		Values: tui.ConfigFormValues{
			BackendBaseURL:           strings.TrimSpace(config.BackendBaseURL),
			DefaultFlow:              strings.TrimSpace(config.DefaultFlow),
			ApprovalMode:             strings.TrimSpace(config.ApprovalMode),
			SandboxEscapePolicy:      strings.TrimSpace(config.SandboxEscapePolicy),
			SkillReviewPolicy:        strings.TrimSpace(config.SkillReviewPolicy),
			WriteConfirmationPolicy:  strings.TrimSpace(config.WriteConfirmationPolicy),
			SandboxRoots:             formatSandboxRootsInline(config.SandboxRoots),
			PermissionRules:          formatPermissionRulesInline(config.PermissionRules),
			ReasoningEnabled:         config.ReasoningEnabled,
			Workdir:                  strings.TrimSpace(config.Workdir),
			CommandTimeoutSeconds:    strconv.Itoa(config.CommandTimeoutSeconds),
			StreamIdleTimeoutSeconds: strconv.Itoa(config.StreamIdleTimeoutSeconds),
			AgentAutoLoopEnabled:     strconv.FormatBool(config.AgentAutoLoopEnabled),
			AgentAutoLoopMaxRounds:   strconv.Itoa(config.AgentAutoLoopMaxRounds),
			OutputLanguage:           strings.TrimSpace(config.OutputLanguage),
			SkillsInstallScope:       internalconfig.NormalizeSkillInstallScope(config.Skills.InstallScope),
			SkillsAutoInstallPolicy:  internalconfig.NormalizeSkillAutoInstallPolicy(config.Skills.AutoInstallPolicy),
			SkillsFreeTextSearch:     config.Skills.FreeTextSearchEnabled,
			SkillsRegistryURL:        strings.TrimSpace(config.Skills.RegistryURL),
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

	if strings.TrimSpace(submission.Values.SandboxEscapePolicy) == "" {
		config.SandboxEscapePolicy = internalconfig.Default().SandboxEscapePolicy
	} else if err := assignConfigValue(&config, "external_resource_access_policy", submission.Values.SandboxEscapePolicy); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.SkillReviewPolicy) == "" {
		config.SkillReviewPolicy = internalconfig.Default().SkillReviewPolicy
	} else if err := assignConfigValue(&config, "skill_review_policy", submission.Values.SkillReviewPolicy); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.WriteConfirmationPolicy) == "" {
		config.WriteConfirmationPolicy = internalconfig.Default().WriteConfirmationPolicy
	} else if err := assignConfigValue(&config, "write_confirmation_policy", submission.Values.WriteConfirmationPolicy); err != nil {
		return internalconfig.Config{}, err
	}

	config.SandboxRoots = parseSandboxRootsInline(submission.Values.SandboxRoots)
	parsedPermissionRules, err := parsePermissionRulesInline(submission.Values.PermissionRules)
	if err != nil {
		return internalconfig.Config{}, err
	}
	config.PermissionRules = parsedPermissionRules

	if err := assignConfigValue(&config, "reasoning_enabled", strconv.FormatBool(submission.Values.ReasoningEnabled)); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.Workdir) == "" {
		config.Workdir = ""
	} else if err := assignConfigValue(&config, "workdir", submission.Values.Workdir); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.CommandTimeoutSeconds) == "" {
		config.CommandTimeoutSeconds = internalconfig.Default().CommandTimeoutSeconds
	} else if err := assignConfigValue(&config, "command_timeout_seconds", submission.Values.CommandTimeoutSeconds); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.StreamIdleTimeoutSeconds) == "" {
		config.StreamIdleTimeoutSeconds = internalconfig.Default().StreamIdleTimeoutSeconds
	} else if err := assignConfigValue(&config, "stream_idle_timeout_seconds", submission.Values.StreamIdleTimeoutSeconds); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.AgentAutoLoopEnabled) == "" {
		config.AgentAutoLoopEnabled = internalconfig.Default().AgentAutoLoopEnabled
	} else if err := assignConfigValue(&config, "agent_auto_loop_enabled", submission.Values.AgentAutoLoopEnabled); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.AgentAutoLoopMaxRounds) == "" {
		config.AgentAutoLoopMaxRounds = internalconfig.Default().AgentAutoLoopMaxRounds
	} else if err := assignConfigValue(&config, "agent_auto_loop_max_rounds", submission.Values.AgentAutoLoopMaxRounds); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.OutputLanguage) == "" {
		config.OutputLanguage = internalconfig.Default().OutputLanguage
	} else if err := assignConfigValue(&config, "output_language", submission.Values.OutputLanguage); err != nil {
		return internalconfig.Config{}, err
	}

	if err := assignConfigValue(&config, "skills_install_scope", submission.Values.SkillsInstallScope); err != nil {
		return internalconfig.Config{}, err
	}
	config.Skills.Directory = ""

	if strings.TrimSpace(submission.Values.SkillsAutoInstallPolicy) == "" {
		config.Skills.AutoInstallPolicy = internalconfig.Default().Skills.AutoInstallPolicy
	} else if err := assignConfigValue(&config, "skills_auto_install_policy", submission.Values.SkillsAutoInstallPolicy); err != nil {
		return internalconfig.Config{}, err
	}

	if err := assignConfigValue(&config, "skills_free_text_search", strconv.FormatBool(submission.Values.SkillsFreeTextSearch)); err != nil {
		return internalconfig.Config{}, err
	}

	if strings.TrimSpace(submission.Values.SkillsRegistryURL) == "" {
		config.Skills.RegistryURL = internalconfig.Default().Skills.RegistryURL
	} else if err := assignConfigValue(&config, "skills_registry_url", submission.Values.SkillsRegistryURL); err != nil {
		return internalconfig.Config{}, err
	}

	if err := a.configManager.Save(config); err != nil {
		return internalconfig.Config{}, err
	}

	return config, nil
}

func resolveReasoningEnabled(config internalconfig.Config, override string) bool {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "on":
		return true
	case "off":
		return false
	default:
		return config.ReasoningEnabled
	}
}

func resolveAgentAutoLoopEnabled(config internalconfig.Config, state agent.SessionState, requestMode string) *bool {
	if normalizeRequestMode(requestMode) != provider.RequestTypeAgent {
		return nil
	}
	value := config.AgentAutoLoopEnabled
	switch strings.ToLower(strings.TrimSpace(state.AgentAutoLoopEnabledOverride)) {
	case "on":
		value = true
	case "off":
		value = false
	}
	return &value
}

func resolveAgentAutoLoopMaxRounds(config internalconfig.Config, state agent.SessionState, requestMode string) int {
	if normalizeRequestMode(requestMode) != provider.RequestTypeAgent {
		return 0
	}
	if state.AgentAutoLoopMaxRoundsOverride > 0 {
		return state.AgentAutoLoopMaxRoundsOverride
	}
	return config.AgentAutoLoopMaxRounds
}

func reasoningStatusText(enabled bool) string {
	if enabled {
		return "开启"
	}
	return "关闭"
}

func executeReasoningDirective(argument string, config internalconfig.Config, state agent.SessionState) (string, agent.SessionState, error) {
	trimmed := strings.ToLower(strings.TrimSpace(argument))
	switch trimmed {
	case "", "status":
		effective := resolveReasoningEnabled(config, state.ReasoningOverride)
		source := "会话覆盖"
		if strings.TrimSpace(state.ReasoningOverride) == "" {
			source = "全局配置"
		}
		return fmt.Sprintf("当前思考模式: %s（来源: %s）", reasoningStatusText(effective), source), state, nil
	case "on":
		state.ReasoningOverride = "on"
		return "已开启当前会话的思考模式。", state, nil
	case "off":
		state.ReasoningOverride = "off"
		return "已关闭当前会话的思考模式。", state, nil
	case "default", "reset", "clear":
		state.ReasoningOverride = ""
		return fmt.Sprintf("已恢复为全局默认思考模式：%s。", reasoningStatusText(config.ReasoningEnabled)), state, nil
	default:
		return "", state, fmt.Errorf("用法: /reasoning [on|off|default|status]")
	}
}

func (a *App) executeReasoningCommand(config internalconfig.Config, command string, state agent.SessionState) (string, agent.SessionState, error) {
	argument := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), "/reasoning"))
	output, updatedState, err := executeReasoningDirective(argument, config, state)
	if err != nil {
		return "", state, err
	}
	if strings.TrimSpace(updatedState.FlowID) != "" && strings.TrimSpace(updatedState.Workdir) != "" {
		if saveErr := a.sessionStore.Save(updatedState); saveErr != nil {
			return "", state, saveErr
		}
	}
	return output, updatedState, nil
}

func (a *App) executeTUIReasoningCommand(command string, snapshot tui.Snapshot) (string, *tui.Snapshot, error) {
	config, err := a.configManager.Load()
	if err != nil {
		return "", nil, err
	}
	currentState, err := a.prepareSession(snapshot.DefaultFlow, false, snapshot.Workdir, snapshot.RequestMode)
	if err != nil {
		return "", nil, err
	}
	state := agent.SessionState{
		FlowID:                         snapshot.DefaultFlow,
		FlowDisplayName:                snapshot.DefaultFlowLabel,
		SessionID:                      snapshot.SessionID,
		SessionTitle:                   snapshot.SessionTitle,
		Workdir:                        snapshot.Workdir,
		RequestMode:                    snapshot.RequestMode,
		ContextDir:                     snapshot.ContextDir,
		ReasoningOverride:              snapshot.ReasoningOverride,
		AgentAutoLoopEnabledOverride:   currentState.AgentAutoLoopEnabledOverride,
		AgentAutoLoopMaxRoundsOverride: currentState.AgentAutoLoopMaxRoundsOverride,
		SandboxEscapePolicyOverride:    snapshot.SessionSandboxEscapePolicy,
		SkillReviewPolicyOverride:      currentState.SkillReviewPolicyOverride,
		ApprovedSkillNames:             currentState.ApprovedSkillNames,
	}
	output, updatedState, err := a.executeReasoningCommand(config, command, state)
	if err != nil {
		return "", nil, err
	}
	updatedSnapshot := snapshot
	updatedSnapshot.ReasoningOverride = updatedState.ReasoningOverride
	updatedSnapshot.ReasoningEnabled = resolveReasoningEnabled(config, updatedState.ReasoningOverride)
	return output, &updatedSnapshot, nil
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
		Title: "命令执行模式(command execution mode)",
		Hint:  "选择客户端命令的执行模式。",
		Options: []tui.ApprovalOption{
			{Value: "confirm", Label: "每次确认", Description: "执行客户端命令前逐次确认。", IsCurrent: current == "confirm"},
			{Value: "auto", Label: "自动执行", Description: "以后都同意，客户端命令默认自动执行。", IsCurrent: current == "auto"},
			{Value: "readonly", Label: "只读模式", Description: "禁止执行本地命令。", IsCurrent: current == "readonly"},
		},
	}
}

func buildTUISkillsPicker(config internalconfig.Config, contextDir string, query string) (*tui.SkillsPickerState, error) {
	skillConfig := config
	resolvedContextDir := resolveLocalWorkdir(firstNonEmpty(contextDir, config.Workdir))
	if strings.TrimSpace(resolvedContextDir) != "" {
		skillConfig.Workdir = resolvedContextDir
	}
	items, _, err := skills.Discover(skillConfig)
	if err != nil {
		return nil, err
	}
	pickerItems := make([]tui.SkillItem, 0, len(items))
	for _, item := range items {
		referenceFiles := make([]tui.SkillReferenceItem, 0, len(item.Files))
		for _, file := range item.Files {
			referenceFiles = append(referenceFiles, tui.SkillReferenceItem{
				Path:        strings.TrimSpace(file.Path),
				Description: strings.TrimSpace(file.Description),
			})
		}
		pickerItems = append(pickerItems, tui.SkillItem{
			Name:           item.Name,
			Description:    item.Description,
			Category:       item.Category,
			SourceLabel:    sourceDisplayLabel(item.Source),
			StateLabel:     skillStateLabel(item, config),
			RootDir:        item.RootDir,
			RelativeDir:    item.RelativeDir,
			Restriction:    skillRestrictionNote(item.Name),
			ReferenceFiles: referenceFiles,
		})
	}
	hint := "使用方向键选择，Enter 查看详情，Esc 关闭。"
	if len(pickerItems) == 0 {
		hint = "当前没有可展示的技能，Esc 关闭。"
	}
	remoteQuery := strings.TrimSpace(query)
	remoteResults, registryURL, remoteErr := skills.SearchRemote(config, remoteQuery, 20)
	installedMap := map[string]skills.InstalledRemoteSkill{}
	installedItems, installedErr := skills.ListInstalledRemoteSkills(skillConfig)
	if installedErr == nil {
		for _, item := range installedItems {
			installedMap[strings.ToLower(strings.TrimSpace(item.Slug))] = item
		}
	}
	registryLabel := compactSkillsRegistryLabel(registryURL)
	remoteItems := make([]tui.RemoteSkillItem, 0, len(remoteResults))
	for _, item := range remoteResults {
		installed, ok := installedMap[strings.ToLower(strings.TrimSpace(item.Slug))]
		remoteItems = append(remoteItems, tui.RemoteSkillItem{
			Slug:             strings.TrimSpace(item.Slug),
			DisplayName:      strings.TrimSpace(item.DisplayName),
			Summary:          strings.TrimSpace(item.Summary),
			Popularity:       item.Popularity,
			Downloads:        item.Downloads,
			Version:          strings.TrimSpace(item.Version),
			RegistryLabel:    registryLabel,
			Installed:        ok,
			InstalledVersion: strings.TrimSpace(installed.InstalledVersion),
			CanUpdate:        ok && !strings.EqualFold(strings.TrimSpace(installed.InstalledVersion), strings.TrimSpace(item.Version)),
		})
	}
	sort.SliceStable(remoteItems, func(left int, right int) bool {
		if remoteItems[left].Installed != remoteItems[right].Installed {
			return remoteItems[left].Installed
		}
		if remoteItems[left].CanUpdate != remoteItems[right].CanUpdate {
			return remoteItems[left].CanUpdate
		}
		leftName := strings.ToLower(firstNonEmpty(remoteItems[left].DisplayName, remoteItems[left].Slug))
		rightName := strings.ToLower(firstNonEmpty(remoteItems[right].DisplayName, remoteItems[right].Slug))
		return leftName < rightName
	})
	remoteError := ""
	if remoteErr != nil {
		remoteError = remoteErr.Error()
	}
	return &tui.SkillsPickerState{
		Title:         "技能列表",
		Hint:          hint,
		ActiveTab:     "local",
		Items:         pickerItems,
		RemoteItems:   remoteItems,
		RemoteQuery:   remoteQuery,
		RemoteHint:    "按 / 输入搜索词，Enter 查看详情，i 安装，u 更新，d 删除，r 刷新。",
		RemoteError:   remoteError,
		RegistryLabel: registryLabel,
	}, nil
}

func (a *App) executeTUISkillsPickerCommand(config internalconfig.Config, contextDir string, command string) (tui.LocalCommandResult, error) {
	remainder := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(command), skillsPickerCommandPrefix))
	action := "open"
	args := []string{}
	skillConfig := config
	resolvedContextDir := resolveLocalWorkdir(firstNonEmpty(contextDir, config.Workdir))
	if strings.TrimSpace(resolvedContextDir) != "" {
		skillConfig.Workdir = resolvedContextDir
	}
	if remainder != "" {
		args = strings.Fields(remainder)
		if len(args) > 0 {
			action = strings.ToLower(strings.TrimSpace(args[0]))
			args = args[1:]
		}
	}
	buildPicker := func(query string) (tui.LocalCommandResult, error) {
		picker, err := buildTUISkillsPicker(skillConfig, resolvedContextDir, query)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		if picker != nil {
			picker.ActiveTab = "remote"
		}
		return tui.LocalCommandResult{Handled: true, SkillsPicker: picker}, nil
	}
	buildDetailResult := func(slug string, query string, note string) (tui.LocalCommandResult, error) {
		result, err := buildPicker(query)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		detail, _, detailErr := skills.FetchRemoteDetail(skillConfig, slug)
		if detailErr != nil {
			if note != "" {
				result.Events = append(result.Events, tui.Event{Kind: tui.EventSystemNote, Text: note})
			}
			result.Events = append(result.Events, tui.Event{Kind: tui.EventErrorNote, Text: detailErr.Error()})
			return result, nil
		}
		installedItems, installedErr := skills.ListInstalledRemoteSkills(skillConfig)
		installed := skills.InstalledRemoteSkill{}
		installedFound := false
		if installedErr == nil {
			for _, item := range installedItems {
				if strings.EqualFold(strings.TrimSpace(item.Slug), strings.TrimSpace(slug)) {
					installed = item
					installedFound = true
					break
				}
			}
		}
		panelItem := buildRemoteSkillDetailItem(detail, compactSkillsRegistryLabel(detail.RegistryURL), installed, installedFound)
		if note != "" {
			result.Events = append(result.Events, tui.Event{Kind: tui.EventSystemNote, Text: note})
		}
		result.Events = append(result.Events, tui.Event{Kind: tui.EventToolPanel, Panel: buildRemoteSkillPanel(panelItem, query)})
		return result, nil
	}
	splitSlugAndQuery := func(values []string) (string, string, error) {
		if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
			return "", "", fmt.Errorf("缺少远程技能 slug")
		}
		return strings.TrimSpace(values[0]), strings.TrimSpace(strings.Join(values[1:], " ")), nil
	}
	splitInstallScopeArgs := func(values []string) (string, string, bool, bool, string, error) {
		if len(values) < 3 {
			return "", "", false, false, "", fmt.Errorf("缺少安装目标参数")
		}
		slug := strings.TrimSpace(values[0])
		scope := internalconfig.NormalizeSkillInstallScope(values[1])
		if slug == "" || strings.TrimSpace(values[1]) == "" {
			return "", "", false, false, "", fmt.Errorf("缺少安装目标参数")
		}
		remember := strings.EqualFold(strings.TrimSpace(values[2]), "remember")
		reopenDetail := false
		queryStart := 3
		if len(values) > 3 && strings.EqualFold(strings.TrimSpace(values[3]), "detail") {
			reopenDetail = true
			queryStart = 4
		}
		query := ""
		if len(values) > queryStart {
			query = strings.TrimSpace(strings.Join(values[queryStart:], " "))
		}
		return slug, scope, remember, reopenDetail, query, nil
	}
	executeInstall := func(slug string, scope string, remember bool, reopenDetail bool, query string) (tui.LocalCommandResult, error) {
		installConfig := skillConfig
		installConfig.Skills.InstallScope = internalconfig.NormalizeSkillInstallScope(scope)
		installConfig.Skills.Directory = ""
		resultData, installErr := skills.InstallRemote(installConfig, slug, "", false)
		if installErr != nil {
			result, pickerErr := buildPicker(query)
			if pickerErr != nil {
				return tui.LocalCommandResult{}, pickerErr
			}
			result.Events = []tui.Event{{Kind: tui.EventErrorNote, Text: installErr.Error()}}
			return result, nil
		}
		if remember {
			a.skillInstallScopeSession = installConfig.Skills.InstallScope
			a.skillInstallScopePinned = true
		}
		lines := []string{
			fmt.Sprintf("已安装远程技能: %s (%s)", fallbackConfigValue(resultData.Name, resultData.Slug), resultData.Slug),
			fmt.Sprintf("版本: %s", resultData.Version),
			fmt.Sprintf("目录: %s", resultData.TargetDir),
			fmt.Sprintf("技能仓库: %s", resultData.RegistryURL),
		}
		note := strings.Join(lines, "\n")
		if remember {
			note = fmt.Sprintf("本会话后续安装默认写入%s。\n%s", skillInstallScopeLabelText(installConfig.Skills.InstallScope), note)
		}
		if reopenDetail {
			return buildDetailResult(slug, query, note)
		}
		result, pickerErr := buildPicker(query)
		if pickerErr != nil {
			return tui.LocalCommandResult{}, pickerErr
		}
		result.Events = []tui.Event{{Kind: tui.EventSystemNote, Text: note}}
		return result, nil
	}
	buildInstallPromptResult := func(slug string, query string, reopenDetail bool) (tui.LocalCommandResult, error) {
		result, err := buildPicker(query)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		defaultScope := a.resolveSkillInstallScope(skillConfig)
		result.Events = append(result.Events, tui.Event{Kind: tui.EventToolPanel, Panel: buildSkillInstallTargetPanel(slug, query, reopenDetail, defaultScope)})
		return result, nil
	}
	switch action {
	case "open", "refresh", "search":
		return buildPicker(strings.TrimSpace(strings.Join(args, " ")))
	case "detail":
		slug, query, err := splitSlugAndQuery(args)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		return buildDetailResult(slug, query, "")
	case "install-select", "install-detail-select":
		slug, query, err := splitSlugAndQuery(args)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		reopenDetail := strings.HasPrefix(action, "install-detail")
		if a.skillInstallScopePinned {
			return executeInstall(slug, a.skillInstallScopeSession, false, reopenDetail, query)
		}
		return buildInstallPromptResult(slug, query, reopenDetail)
	case "install-scope":
		slug, scope, remember, reopenDetail, query, err := splitInstallScopeArgs(args)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		return executeInstall(slug, scope, remember, reopenDetail, query)
	case "install", "update", "remove":
		slug, query, err := splitSlugAndQuery(args)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		if action == "install" {
			return executeInstall(slug, a.resolveSkillInstallScope(skillConfig), false, false, query)
		}
		output, commandErr := a.executeSkillsCommand([]string{action, slug})
		result, pickerErr := buildPicker(query)
		if pickerErr != nil {
			return tui.LocalCommandResult{}, pickerErr
		}
		if commandErr != nil {
			result.Events = []tui.Event{{Kind: tui.EventErrorNote, Text: commandErr.Error()}}
			return result, nil
		}
		result.Events = []tui.Event{{Kind: tui.EventSystemNote, Text: output}}
		return result, nil
	case "install-detail", "update-detail", "remove-detail":
		slug, query, err := splitSlugAndQuery(args)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		baseAction := strings.TrimSuffix(action, "-detail")
		if baseAction == "install" {
			return executeInstall(slug, a.resolveSkillInstallScope(skillConfig), false, true, query)
		}
		output, commandErr := a.executeSkillsCommand([]string{baseAction, slug})
		if commandErr != nil {
			result, pickerErr := buildPicker(query)
			if pickerErr != nil {
				return tui.LocalCommandResult{}, pickerErr
			}
			result.Events = []tui.Event{{Kind: tui.EventErrorNote, Text: commandErr.Error()}}
			return result, nil
		}
		return buildDetailResult(slug, query, output)
	default:
		return tui.LocalCommandResult{}, fmt.Errorf("不支持的 skills picker 动作: %s", action)
	}
}

func resolveTUISkillsContextDir(snapshot tui.Snapshot) string {
	return resolveLocalWorkdir(firstNonEmpty(snapshot.ContextDir, snapshot.Workdir))
}

func (a *App) resolveSkillInstallScope(config internalconfig.Config) string {
	if a.skillInstallScopePinned {
		return internalconfig.NormalizeSkillInstallScope(a.skillInstallScopeSession)
	}
	return internalconfig.NormalizeSkillInstallScope(config.Skills.InstallScope)
}

func skillInstallScopeLabelText(scope string) string {
	switch internalconfig.NormalizeSkillInstallScope(scope) {
	case internalconfig.SkillInstallScopeWorkspace:
		return "工作区 (.snz1dp/skills)"
	default:
		return "用户目录 (~/.snz1dp/skills)"
	}
}

func compactSkillsRegistryLabel(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	return parsed.Host
}

func buildRemoteSkillDetailItem(detail skills.RemoteSkillDetail, registryLabel string, installed skills.InstalledRemoteSkill, installedFound bool) tui.RemoteSkillItem {
	ownerLabel := strings.TrimSpace(detail.OwnerName)
	if ownerLabel == "" {
		ownerLabel = strings.TrimSpace(detail.OwnerHandle)
	} else if strings.TrimSpace(detail.OwnerHandle) != "" && !strings.EqualFold(ownerLabel, strings.TrimSpace(detail.OwnerHandle)) {
		ownerLabel += fmt.Sprintf(" (%s)", strings.TrimSpace(detail.OwnerHandle))
	}
	return tui.RemoteSkillItem{
		Slug:             strings.TrimSpace(detail.Slug),
		DisplayName:      strings.TrimSpace(detail.DisplayName),
		Summary:          strings.TrimSpace(detail.Summary),
		Version:          strings.TrimSpace(detail.Version),
		RegistryLabel:    registryLabel,
		Installed:        installedFound,
		InstalledVersion: strings.TrimSpace(installed.InstalledVersion),
		CanUpdate:        installedFound && !strings.EqualFold(strings.TrimSpace(installed.InstalledVersion), strings.TrimSpace(detail.Version)),
		OwnerLabel:       ownerLabel,
		SupportedOS:      append([]string{}, detail.SupportedOS...),
		SupportedEnvs:    append([]string{}, detail.SupportedEnvs...),
	}
}

func buildRemoteSkillPanel(item tui.RemoteSkillItem, query string) *tui.ToolPanel {
	return &tui.ToolPanel{
		Title:   fmt.Sprintf("远程技能详情｜%s", firstNonEmpty(strings.TrimSpace(item.DisplayName), strings.TrimSpace(item.Slug))),
		Summary: firstNonEmpty(strings.TrimSpace(item.Summary), "当前远程技能没有额外说明。"),
		Body:    buildRemoteSkillPanelBody(item),
		Hint:    "Esc 返回上层技能列表。",
		Actions: []tui.ToolPanelAction{
			{Key: "i", Label: "安装", Command: buildHiddenSkillsPickerCommand("install-detail-select", item.Slug, query), Disabled: item.Installed},
			{Key: "u", Label: "更新", Command: buildHiddenSkillsPickerCommand("update-detail", item.Slug, query), Disabled: !item.Installed},
			{Key: "d", Label: "删除", Command: buildHiddenSkillsPickerCommand("remove-detail", item.Slug, query), Disabled: !item.Installed},
		},
	}
}

func buildSkillInstallTargetPanel(slug string, query string, reopenDetail bool, defaultScope string) *tui.ToolPanel {
	detailFlag := ""
	if reopenDetail {
		detailFlag = "detail"
	}
	return &tui.ToolPanel{
		Title:   fmt.Sprintf("选择安装位置｜%s", strings.TrimSpace(slug)),
		Summary: fmt.Sprintf("默认安装位置: %s", skillInstallScopeLabelText(defaultScope)),
		Body: strings.Join([]string{
			"请选择远程技能安装位置。",
			fmt.Sprintf("当前默认: %s", skillInstallScopeLabelText(defaultScope)),
			"按 1/2 仅安装本次，按 3/4 安装并记住本会话选择。",
		}, "\n"),
		Hint: "Esc 返回上层技能列表。",
		Actions: []tui.ToolPanelAction{
			{Key: "1", Label: "安装到用户目录", Command: buildSkillsPickerActionCommand("install-scope", slug, internalconfig.SkillInstallScopeUser, "once", detailFlag, query)},
			{Key: "2", Label: "安装到工作区", Command: buildSkillsPickerActionCommand("install-scope", slug, internalconfig.SkillInstallScopeWorkspace, "once", detailFlag, query)},
			{Key: "3", Label: "安装到用户目录并记住本会话", Command: buildSkillsPickerActionCommand("install-scope", slug, internalconfig.SkillInstallScopeUser, "remember", detailFlag, query)},
			{Key: "4", Label: "安装到工作区并记住本会话", Command: buildSkillsPickerActionCommand("install-scope", slug, internalconfig.SkillInstallScopeWorkspace, "remember", detailFlag, query)},
		},
	}
}

func buildHiddenSkillsPickerCommand(action string, slug string, query string) string {
	return buildSkillsPickerActionCommand(action, slug, query)
}

func buildSkillsPickerActionCommand(action string, values ...string) string {
	parts := []string{skillsPickerCommandPrefix, strings.TrimSpace(action)}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, " ")
}

func buildRemoteSkillPanelBody(item tui.RemoteSkillItem) string {
	lines := []string{
		fmt.Sprintf("Slug: %s", firstNonEmpty(strings.TrimSpace(item.Slug), "<未填写>")),
		fmt.Sprintf("显示名称: %s", firstNonEmpty(strings.TrimSpace(item.DisplayName), "<未填写>")),
		fmt.Sprintf("摘要: %s", firstNonEmpty(strings.TrimSpace(item.Summary), "<未填写>")),
		fmt.Sprintf("最新版本: %s", firstNonEmpty(strings.TrimSpace(item.Version), "<未填写>")),
	}
	if strings.TrimSpace(item.OwnerLabel) != "" {
		lines = append(lines, fmt.Sprintf("发布者: %s", item.OwnerLabel))
	}
	if len(item.SupportedOS) > 0 {
		lines = append(lines, fmt.Sprintf("支持系统: %s", strings.Join(item.SupportedOS, ", ")))
	}
	if len(item.SupportedEnvs) > 0 {
		lines = append(lines, fmt.Sprintf("适用环境: %s", strings.Join(item.SupportedEnvs, ", ")))
	}
	if strings.TrimSpace(item.RegistryLabel) != "" {
		lines = append(lines, fmt.Sprintf("仓库: %s", item.RegistryLabel))
	}
	if item.Installed {
		lines = append(lines, fmt.Sprintf("安装状态: 已安装 %s", firstNonEmpty(strings.TrimSpace(item.InstalledVersion), "<未知版本>")))
	} else {
		lines = append(lines, "安装状态: 未安装")
	}
	if item.CanUpdate {
		lines = append(lines, "更新状态: 有可用更新")
	}
	return strings.Join(lines, "\n")
}

func buildTUITasksPicker(ctx context.Context, config internalconfig.Config) (*tui.TasksPickerState, string, error) {
	_, result, err := runLocalReadTool(ctx, config, "task_list", map[string]any{})
	if err != nil {
		return nil, "", err
	}
	resultMap, _ := result.(map[string]any)
	tasks := mapSliceValue(resultMap, "tasks")
	if len(tasks) == 0 {
		return nil, "当前没有后台任务。", nil
	}
	items := make([]tui.TaskItem, 0, len(tasks))
	for _, item := range tasks {
		exitCode := ""
		if value, ok := item["exit_code"]; ok {
			exitCode = fmt.Sprintf("%v", value)
		}
		items = append(items, tui.TaskItem{
			ID:          strings.TrimSpace(stringMapValue(item, "task_id")),
			Name:        fallbackString(stringMapValue(item, "task_name"), stringMapValue(item, "task_id")),
			Status:      strings.TrimSpace(stringMapValue(item, "status")),
			Command:     strings.TrimSpace(stringMapValue(item, "command")),
			CWD:         strings.TrimSpace(stringMapValue(item, "cwd")),
			SessionID:   strings.TrimSpace(stringMapValue(item, "session_id")),
			StartedAt:   strings.TrimSpace(stringMapValue(item, "started_at")),
			CompletedAt: strings.TrimSpace(stringMapValue(item, "completed_at")),
			ExitCode:    strings.TrimSpace(exitCode),
		})
	}
	return &tui.TasksPickerState{
		Title: "后台任务(tasks)",
		Hint:  "使用方向键选择任务，Enter 打开操作菜单，Esc 关闭。",
		Items: items,
	}, "", nil
}
