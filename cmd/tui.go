package cmd

import (
	"context"
	"errors"
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
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/instructions"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	clientruntime "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/runtime"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/textsanitize"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

var bracketErrorCodePattern = regexp.MustCompile(`^\[[A-Z0-9_]+\]\s*`)

const skillsPickerCommandPrefix = "/skills-picker"
const knowledgePickerCommandPrefix = "/knowledge-picker"
const sessionPickerDisplayPageSize = 25
const sessionPickerFetchBatchSize = 100
const sessionHistoryPageSize = 30

func (a *App) prepareInitialTUIAuthentication(
	ctx context.Context,
	providerClient *providerclient.Client,
	config internalconfig.Config,
) internalconfig.Config {
	updatedConfig, _, _ := a.ensureProviderAuthenticated(ctx, providerClient, config)
	return updatedConfig
}

func (a *App) runTUI(ctx context.Context, args []string) error {
	startupCommand := ""
	if len(args) > 0 {
		startupCommand = strings.TrimSpace(args[0])
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	config, _, err = a.ensureClientIdentity(config)
	if err != nil {
		return err
	}
	workdir, instanceLease, err := acquireWorkspaceLease(config)
	if err != nil {
		return err
	}
	defer func() {
		_ = instanceLease.Close()
	}()

	providerClient := buildProviderClient(config)
	config = a.prepareInitialTUIAuthentication(ctx, providerClient, config)
	authRefreshProvider := func() provider.Provider {
		return a.providerWithCallbackAuthRefresh(providerClient, &config)
	}
	backend := a.inspectBackendStatus(ctx, providerClient, config.BackendBaseURL)
	if backend.Available {
		config, err = a.ensureDefaultFlow(ctx, providerClient, config)
		if err != nil {
			return err
		}
	}
	configRef := newPrimaryClientConfigRef(config)
	var serviceConfig *provider.ServiceConfig
	if backend.Available {
		serviceConfig, _ = authRefreshProvider().GetServiceConfig(ctx)
	}
	snapshot, err := a.buildInitialTUISnapshot(config, strings.TrimSpace(config.DefaultFlow), backend, serviceConfig)
	if err != nil {
		return err
	}
	primaryDispatchCh := make(chan string, 8)
	primaryClientCtx, primaryClientCancel := context.WithCancel(ctx)
	defer primaryClientCancel()
	primaryClientErrCh := make(chan error, 1)
	var primaryClient *primaryClientManager
	primaryClient = newPrimaryClientManager(configRef, instanceLease, snapshot.SessionID, primaryClientOptions{
		OnDispatch: func(dispatch primaryTaskDispatch) {
			prompt := strings.TrimSpace(dispatch.Prompt)
			if prompt == "" {
				return
			}
			select {
			case primaryDispatchCh <- prompt:
			default:
			}
		},
	})
	go func() {
		primaryClientErrCh <- primaryClient.Run(primaryClientCtx)
	}()
	defer func() {
		primaryClientCancel()
		select {
		case err := <-primaryClientErrCh:
			if err != nil && !errors.Is(err, context.Canceled) {
				fmt.Fprintf(a.stderr, "提示: 主 CLI 长连接已退出: %v\n", err)
			}
		default:
		}
	}()

	flowItems := []tui.FlowItem{}

	runner := func(runCtx context.Context, request tui.Request, emit func(tui.Event), confirm func(tui.ConfirmRequest) bool, askUser func(clientruntime.AskUserRequest) (clientruntime.AskUserResponse, error)) (returnErr error) {
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
		defer func() {
			errorMessage := ""
			if returnErr != nil {
				errorMessage = returnErr.Error()
			}
			runtimeClient.EmitOnComplete(config, currentFlow, sessionState.SessionID, firstNonEmpty(sessionState.LastRunID, request.RunID), sessionState.LastMessageID, normalizeRequestMode(request.RequestMode), returnErr == nil, errorMessage)
		}()
		runtimeClient.SetAskUserPrompter(func(_ context.Context, request clientruntime.AskUserRequest) (clientruntime.AskUserResponse, error) {
			return askUser(request)
		})
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
		persistBrowserToolAlwaysAllow := a.persistConfigValue("browser_permission_mode", internalconfig.BrowserPermissionModeSkipAll)
		persistComputerToolAlwaysAllow := a.persistConfigValue("computer_permission_mode", internalconfig.ComputerPermissionModeSkipAll)
		sandboxEscapeConfirmer := func(path string) (bool, error) {
			return confirm(buildTUISandboxEscapeConfirmRequest(path, persistSandboxEscapeAlwaysAllow)), nil
		}
		runtimeClient.SetSandboxEscapeConfirmer(sandboxEscapeConfirmer)
		quietRuntimeClient.SetSandboxEscapeConfirmer(sandboxEscapeConfirmer)
		skillRetryCount := 0
		autoLoopCurrentRound := 0
		originalUserInput := request.Input
		callbackProvider := a.providerWithCallbackAuthRefresh(providerClient, &config)
		for {
			// 优先使用 callbackProvider 上缓存的 serviceConfig（登录成功后会自动刷新），
			// fallback 到启动时获取的值
			effectiveServiceConfig := extractServiceConfig(callbackProvider)
			if effectiveServiceConfig == nil {
				effectiveServiceConfig = serviceConfig
			}
			resolvedContextDir := resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, workdir))
			skillConfig := a.resolveEffectiveSkillConfig(config, resolvedContextDir)
			requestedSkillNames, clientSkillPackages, skillErr := loadSkillPayloadWithConfig(skillConfig, request.RequestMode)
			if skillErr != nil {
				return skillErr
			}
			clientContextDir := resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, workdir))
			// Load project-level instruction files (CLAUDE.md, AGENT.md, etc.)
			var clientInstructions any
			if instrResult, instrErr := instructions.Load(resolvedContextDir); instrErr == nil && instrResult != nil {
				clientInstructions = instrResult.ToPackage()
			}
			commandExecutionTarget := "client"
			ensuredSessionID, ensureErr := clientruntime.EnsureSessionID(runCtx, callbackProvider, sessionState.SessionID)
			if ensureErr != nil {
				return ensureErr
			}
			sessionState.SessionID = ensuredSessionID
			if applySessionRequestLineage(&sessionState, currentFlow, firstNonEmpty(sessionState.Workdir, workdir), request.RequestMode, request.RunID) {
				if err := saveSessionStateIfReady(a.sessionStore, sessionState); err != nil {
					return err
				}
				primaryClient.UpdateSession(sessionState.SessionID)
			}
			// 注入子任务能力字段，确保 run_subtask 创建的子智能体拥有与主任务相同的能力
			runtimeClient.SetSubtaskCapabilities(
				clientSkillPackages,
				requestedSkillNames,
				request.ReasoningEnabled,
				request.InternetSearch,
				boolPtr(config.CrossSessionMemoryEnabled),
			)
			stream, streamSession, streamErr := callbackProvider.Stream(runCtx, provider.StreamRequest{
				FlowID:                    currentFlow,
				Input:                     buildStreamInputWithTodoContext(sessionState.TodoItems, request.Input),
				InputType:                 "any",
				Tweaks:                    buildTaskTweaks(config, clientContextDir, commandExecutionTarget),
				InputFiles:                append([]string(nil), request.InputFiles...),
				InternetSearch:            request.InternetSearch,
				KnowledgeScopes:           append([]string(nil), request.KnowledgeScopes...),
				KnowledgeDocs:             append([]string(nil), request.KnowledgeDocs...),
				OutputType:                "any",
				SessionID:                 sessionState.SessionID,
				RunID:                     request.RunID,
				RequestType:               normalizeRequestMode(request.RequestMode),
				ReasoningEnabled:          request.ReasoningEnabled,
				AgentAutoLoopEnabled:      resolveAgentAutoLoopEnabled(config, sessionState, request.RequestMode, effectiveServiceConfig),
				AgentAutoLoopMaxRounds:    resolveAgentAutoLoopMaxRounds(config, sessionState, request.RequestMode, effectiveServiceConfig),
				CrossSessionMemoryEnabled: boolPtr(config.CrossSessionMemoryEnabled),
				ClientType:                provider.ClientTypeCLI,
				ClientID:                  config.ClientID,
				DeviceID:                  config.DeviceID,
				ClientContextDir:          clientContextDir,
				CommandExecutionTarget:    commandExecutionTarget,
				UserAgent:                 buildCLIUserAgent(),
				RequestedSkillNames:       requestedSkillNames,
				ClientMCPServers:          quietRuntimeClient.ClientMCPServersForMode(request.RequestMode, config),
				ClientSkillPackages:       clientSkillPackages,
				ClientInstructions:        clientInstructions,
			})
			if streamErr != nil {
				return fmt.Errorf("%s", explainBackendFailure(streamErr))
			}

			if streamSession != nil && strings.TrimSpace(streamSession.SessionID) != "" {
				sessionState.SessionID = streamSession.SessionID
				sessionState.FlowID = currentFlow
				sessionState.FlowDisplayName = strings.TrimSpace(a.resolveFlowDisplayName(runCtx, providerClient, currentFlow))
				title, titleErr := lookupSessionTitle(runCtx, providerClient, currentFlow, streamSession.SessionID)
				if titleErr == nil && strings.TrimSpace(title) != "" {
					sessionState.SessionTitle = strings.TrimSpace(title)
				}
				sessionState.Workdir = workdir
				sessionState.RequestMode = normalizeRequestMode(request.RequestMode)
				sessionState.ContextDir = resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, workdir))
				if err := a.sessionStore.Save(sessionState); err != nil {
					return err
				}
				primaryClient.UpdateSession(sessionState.SessionID)
				emit(tui.Event{Kind: tui.EventSessionUpdate, SessionID: streamSession.SessionID, SessionTitle: resolveSessionTitle(sessionState)})
			}

			retryRequested := false
			assistantRenderState := tuiAssistantRenderState{}
			var latestAssistantOutput strings.Builder
		eventLoop:
			for event := range stream {
				if updateSessionLineageFromEvent(&sessionState, event, request.RunID) {
					if err := saveSessionStateIfReady(a.sessionStore, sessionState); err != nil {
						return err
					}
					primaryClient.UpdateSession(sessionState.SessionID)
				}
				localConfig, configErr := a.configManager.Load()
				if configErr != nil {
					return configErr
				}
				switch event.Type {
				case provider.EventMessage:
					if metrics := buildTUIStreamStatsMetrics(event.Data); metrics != nil {
						emit(tui.Event{Kind: tui.EventStatsUpdate, Stats: metrics})
					}
					if sessionTitle := firstNonEmptyTrimmedStringMapValue(event.Data, "session_name"); sessionTitle != "" {
						sessionState.SessionTitle = sessionTitle
						if strings.TrimSpace(sessionState.SessionID) != "" {
							if err := a.sessionStore.Save(sessionState); err != nil {
								return err
							}
						}
						emit(tui.Event{Kind: tui.EventSessionUpdate, SessionID: sessionState.SessionID, SessionTitle: resolveSessionTitle(sessionState)})
					}
					if assistantChunkEvent, ok := buildTUIAssistantChunkEvent(event.Data); ok {
						segments := emitTUIAssistantChunk(emit, &assistantRenderState, assistantChunkEvent)
						for _, segment := range segments {
							if segment.Reasoning {
								continue
							}
							latestAssistantOutput.WriteString(segment.Text)
						}
						continue
					}
					if message, ok := event.Data["message"].(string); ok && strings.TrimSpace(message) != "" {
						emit(tui.Event{Kind: tui.EventProgressNote, Text: message})
					}
				case provider.EventCompression:
					compressionText := buildTUICompressionProgressText(event.Data)
					if compressionText == "" {
						compressionText = "正在进行上下文压缩"
					}
					emit(tui.Event{Kind: tui.EventProgressNote, Text: compressionText, EventType: "compression_progress"})
					if metrics := buildTUICompressionMetrics(event.Data); metrics != nil {
						emit(tui.Event{Kind: tui.EventStatsUpdate, Stats: metrics})
					}
				case provider.EventCompressionStart:
					startText := buildTUICompressionProgressText(event.Data)
					if startText == "" || !strings.Contains(startText, "开始进行上下文压缩") {
						if startText != "" && !strings.Contains(startText, "上下文压缩完成") {
							startText = "开始进行上下文压缩（" + startText + "）"
						} else {
							startText = "开始进行上下文压缩"
						}
					}
					emit(tui.Event{Kind: tui.EventProgressNote, Text: startText, EventType: "compression_progress"})
					if metrics := buildTUICompressionMetrics(event.Data); metrics != nil {
						emit(tui.Event{Kind: tui.EventStatsUpdate, Stats: metrics})
					}
				case provider.EventCompressionEnd:
					endText := buildTUICompressionProgressText(event.Data)
					if !strings.Contains(endText, "上下文压缩完成") {
						if endText != "" {
							endText = "上下文压缩完成（" + endText + "）"
						} else {
							endText = "上下文压缩完成"
						}
					}
					emit(tui.Event{Kind: tui.EventProgressNote, Text: endText, EventType: "compression_progress"})
					if metrics := buildTUICompressionMetrics(event.Data); metrics != nil {
						emit(tui.Event{Kind: tui.EventStatsUpdate, Stats: metrics})
					}
				case provider.EventToolCall:
					if metrics := buildTUIStreamStatsMetrics(event.Data); metrics != nil {
						emit(tui.Event{Kind: tui.EventStatsUpdate, Stats: metrics})
					}
					emit(tui.Event{Kind: tui.EventToolCall, Text: strings.TrimSpace(stringMapValue(event.Data, "text"))})
				case provider.EventBuiltIn:
					if metrics := buildTUIStreamStatsMetrics(event.Data); metrics != nil {
						emit(tui.Event{Kind: tui.EventStatsUpdate, Stats: metrics})
					}
					if !modeAllowsBuiltIn(request.RequestMode) {
						emit(tui.Event{Kind: tui.EventErrorNote, Text: fmt.Sprintf("已拒绝本地命令执行：当前为%s。", requestModeLabel(request.RequestMode))})
						if err := submitClientCallFailure(runCtx, callbackProvider, localConfig, event.Data, fmt.Sprintf("当前%s不允许执行本地命令", requestModeLabel(request.RequestMode))); err != nil {
							return err
						}
						continue
					}
					localConfig = applyEffectiveSandboxEscapePolicy(localConfig, sessionState.SandboxEscapePolicyOverride, "")
					emit(tui.Event{Kind: tui.EventBuiltInCall, Text: previewBuiltInCommand(event.Data)})
					emit(tui.Event{Kind: tui.EventProgressNote, Text: "正在执行本地命令..."})
					if requiresTUICommandConfirmation(localConfig, event.Data) {
						approved := confirm(buildTUIConfirmRequest(event.Data))
						if !approved {
							emit(tui.Event{Kind: tui.EventErrorNote, Text: "已拒绝本次客户端命令执行。"})
							if err := submitClientCallFailure(runCtx, callbackProvider, localConfig, event.Data, "用户拒绝执行客户端命令"); err != nil {
								return err
							}
							continue
						}
						execConfig := localConfig
						if strings.TrimSpace(execConfig.ApprovalMode) != "readonly" {
							execConfig.ApprovalMode = "auto"
						}
						if err := quietRuntimeClient.HandleBuiltInEvent(runCtx, callbackProvider, execConfig, event.Data); err != nil {
							return err
						}
						continue
					}
					if err := quietRuntimeClient.HandleBuiltInEvent(runCtx, callbackProvider, localConfig, event.Data); err != nil {
						return err
					}
				case provider.EventClientTool:
					if metrics := buildTUIStreamStatsMetrics(event.Data); metrics != nil {
						emit(tui.Event{Kind: tui.EventStatsUpdate, Stats: metrics})
					}
					toolName := nestedStringValue(event.Data, "function", "name")
					if !modeAllowsClientTool(request.RequestMode, toolName) {
						emit(tui.Event{Kind: tui.EventErrorNote, Text: fmt.Sprintf("已拒绝客户端工具 %s：当前为%s。", toolName, requestModeLabel(request.RequestMode))})
						if err := submitClientCallFailure(runCtx, callbackProvider, localConfig, event.Data, fmt.Sprintf("当前%s不允许执行本地工具 %s", requestModeLabel(request.RequestMode), toolName)); err != nil {
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
						approved := confirm(buildTUIClientToolConfirmRequest(localConfig, sessionState.ContextDir, event.Data, persistApprovedSkillReview, persistSkillReviewAllowAll, persistBrowserToolAlwaysAllow, persistComputerToolAlwaysAllow))
						if !approved {
							emit(tui.Event{Kind: tui.EventErrorNote, Text: "已拒绝本次客户端工具执行。"})
							if err := submitClientCallFailure(runCtx, callbackProvider, localConfig, event.Data, "用户拒绝执行客户端工具"); err != nil {
								return err
							}
							continue
						}
						execConfig := approvedClientToolExecutionConfig(localConfig, toolName)
						toolName, result, toolErr := runtimeClient.ExecuteClientToolPayloadWithProvider(runCtx, callbackProvider, execConfig, event.Data)
						emitTUIClientToolResultEvents(emit, execConfig, toolName, result, toolErr)
						if err := runtimeClient.SubmitClientToolCallback(runCtx, callbackProvider, localConfig, event.Data, result, toolErr); err != nil {
							return err
						}
						continue
					}
					toolName, result, toolErr := runtimeClient.ExecuteClientToolPayloadWithProvider(runCtx, callbackProvider, localConfig, event.Data)
					emitTUIClientToolResultEvents(emit, localConfig, toolName, result, toolErr)
					if err := runtimeClient.SubmitClientToolCallback(runCtx, callbackProvider, localConfig, event.Data, result, toolErr); err != nil {
						return err
					}
				case provider.EventError:
					normalizedErr := normalizeStreamEventError(event.Data["error"])
					if skillRetryCount < 1 {
						missingSkillNames := detectMissingSkillNames(event.Data, normalizedErr)
						if len(missingSkillNames) > 0 {
							installedAny, installErr := a.autoInstallCandidates(
								skillConfig,
								&sessionState,
								request.Input,
								requestedSkillNames,
								missingSkillNames,
								func(approval skillInstallApprovalRequest) bool {
									return confirm(buildTUISkillAutoInstallConfirmRequest(approval))
								},
								func(text string) {
									if strings.TrimSpace(text) != "" {
										emit(tui.Event{Kind: tui.EventProgressNote, Text: text})
									}
								},
							)
							if installErr != nil {
								return installErr
							}
							if installedAny {
								skillRetryCount++
								retryRequested = true
								emit(tui.Event{Kind: tui.EventProgressNote, Text: fmt.Sprintf("检测到缺失技能 %s，已完成补装，正在自动重试。", strings.Join(missingSkillNames, ", "))})
								break eventLoop
							}
						}
					}
					return normalizedErr
				case provider.EventClose:
					break eventLoop
				}
			}
			for _, segment := range assistantRenderState.flush() {
				eventToEmit := tui.Event{Kind: tui.EventAssistantChunk, Text: segment.Text}
				if segment.Reasoning {
					eventToEmit.Kind = tui.EventReasoningChunk
				}
				emit(eventToEmit)
			}
			if retryRequested {
				continue
			}

			// 自动续跑判定：检查是否需要调用 loop_decide_flow_id
			maxRounds := resolveAgentAutoLoopMaxRounds(config, sessionState, request.RequestMode, effectiveServiceConfig)
			loopDecideFlowID := resolveLoopDecideFlowID(config, effectiveServiceConfig)

			if shouldAutoLoop(maxRounds > 0, loopDecideFlowID, autoLoopCurrentRound, maxRounds) {
				autoLoopCurrentRound++
				emit(tui.Event{Kind: tui.EventProgressNote, Text: fmt.Sprintf("正在判定是否需要自动续跑（第 %d/%d 轮）...", autoLoopCurrentRound, maxRounds)})

				decideResult, decideErr := callLoopDecide(runCtx, callbackProvider, loopDecideFlowID, originalUserInput, latestAssistantOutput.String())
				if decideErr != nil {
					break
				}

				if decideResult.IsNeedRerun && strings.TrimSpace(decideResult.NextPrompt) != "" {
					reason := strings.TrimSpace(decideResult.Reason)
					if reason == "" {
						reason = "需要继续执行"
					}
					emit(tui.Event{Kind: tui.EventProgressNote, Text: fmt.Sprintf("自动续跑: %s", reason)})
					emit(tui.Event{Kind: tui.EventFollowupRequest})
					request = prepareTUIFollowupRequest(request, decideResult.NextPrompt)
					continue
				}

				// 不需要续跑，任务已闭环
				reason := strings.TrimSpace(decideResult.Reason)
				if reason != "" {
					emit(tui.Event{Kind: tui.EventProgressNote, Text: fmt.Sprintf("任务已闭环: %s", reason)})
				}
			}

			break
		}

		if request.RunID != "" && strings.TrimSpace(sessionState.SessionID) != "" {
			stats, statsErr := callbackProvider.GetRunStats(runCtx, currentFlow, sessionState.SessionID, request.RunID)
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
			updatedConfig, _, err = a.ensureProviderAuthenticated(refreshCtx, refreshedClient, updatedConfig)
			if err != nil {
				return tui.Snapshot{}, nil, err
			}
			updatedConfig, err = a.ensureDefaultFlow(refreshCtx, refreshedClient, updatedConfig)
			if err != nil {
				return tui.Snapshot{}, nil, err
			}
		}

		refreshedProvider := a.providerWithCallbackAuthRefresh(refreshedClient, &updatedConfig)
		// 同步刷新 provider 上的 serviceConfig，确保后续 runner 迭代能拿到最新值
		if sc, scErr := refreshedProvider.GetServiceConfig(refreshCtx); scErr == nil && sc != nil {
			if cp, ok := refreshedProvider.(*callbackAuthRefreshProvider); ok {
				cp.serviceConfig = sc
			}
		}
		flowID := strings.TrimSpace(firstNonEmpty(updatedConfig.DefaultFlow, current.DefaultFlow))
		snapshot, err := a.buildTUISnapshot(refreshCtx, refreshedProvider, updatedConfig, flowID, backend)
		if err != nil {
			return tui.Snapshot{}, nil, err
		}
		if strings.TrimSpace(snapshot.DefaultFlow) == strings.TrimSpace(current.DefaultFlow) {
			snapshot = preserveTUIChatState(current, snapshot)
		}
		providerClient = refreshedClient
		config = updatedConfig
		workdir = resolveLocalWorkdir(updatedConfig.Workdir)

		flowItems := []tui.FlowItem{}
		if backend.Available {
			flows, flowErr := refreshedProvider.ListFlows(refreshCtx, "", "chat", 0, 100)
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
		config = updatedConfig
		configRef.Store(updatedConfig)
		primaryClient.UpdateSession(snapshot.SessionID)

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
		updatedProvider := a.providerWithCallbackAuthRefresh(providerClient, &updatedConfig)
		updatedSnapshot, err := a.buildTUISnapshot(changeCtx, updatedProvider, updatedConfig, updatedConfig.DefaultFlow, backendStatus{Available: true})
		if err != nil {
			return tui.Snapshot{}, err
		}
		config = updatedConfig
		configRef.Store(updatedConfig)
		primaryClient.UpdateSession(updatedSnapshot.SessionID)
		return updatedSnapshot, nil
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
		updatedProvider := a.providerWithCallbackAuthRefresh(providerClient, &updatedConfig)
		updatedSnapshot, err := a.buildTUISnapshot(changeCtx, updatedProvider, updatedConfig, flowID, backendStatus{Available: true})
		if err != nil {
			return tui.Snapshot{}, err
		}
		config = updatedConfig
		configRef.Store(updatedConfig)
		primaryClient.UpdateSession(updatedSnapshot.SessionID)
		return preserveTUIChatState(current, updatedSnapshot), nil
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
		updatedProvider := a.providerWithCallbackAuthRefresh(providerClient, &updatedConfig)
		updatedSnapshot, err := a.buildTUISnapshot(changeCtx, updatedProvider, updatedConfig, updatedConfig.DefaultFlow, backendStatus{Available: true})
		if err != nil {
			return tui.Snapshot{}, err
		}
		config = updatedConfig
		configRef.Store(updatedConfig)
		return updatedSnapshot, nil
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

	loginFormSubmit := func(changeCtx context.Context, snapshot tui.Snapshot, submission tui.LoginFormSubmission) (tui.LocalCommandResult, error) {
		return a.submitTUILoginForm(changeCtx, providerClient, snapshot, submission, refreshSnapshot)
	}

	localCommand := func(commandCtx context.Context, command string, snapshot tui.Snapshot) (tui.LocalCommandResult, error) {
		trimmed := strings.TrimSpace(command)
		commandProvider := authRefreshProvider()
		switch {
		case trimmed == "/help":
			return tui.LocalCommandResult{
				Handled: true,
				Events:  []tui.Event{{Kind: tui.EventSystemNote, Text: strings.Join(buildTUIHelpLines(snapshot), "\n")}},
			}, nil
		case trimmed == "/about":
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: renderAboutText()}}}, nil
		case trimmed == "/config" || strings.EqualFold(trimmed, "/config show"):
			formState, err := a.buildTUIConfigFormState(commandCtx, commandProvider, snapshot)
			if err != nil {
				return tui.LocalCommandResult{}, err
			}
			return tui.LocalCommandResult{Handled: true, ConfigForm: formState}, nil
		case strings.EqualFold(trimmed, "/login") || strings.HasPrefix(strings.ToLower(trimmed), "/login "):
			return a.executeTUILoginCommand(commandCtx, providerClient, snapshot, trimmed, refreshSnapshot)
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
		case strings.EqualFold(trimmed, "/internet") || strings.HasPrefix(strings.ToLower(trimmed), "/internet "):
			return executeTUIInternetCommand(trimmed, snapshot)
		case strings.EqualFold(trimmed, "/knowledge") || strings.HasPrefix(strings.ToLower(trimmed), "/knowledge "):
			return executeTUIKnowledgeCommand(trimmed, snapshot)
		case strings.EqualFold(trimmed, "/upload") || strings.HasPrefix(strings.ToLower(trimmed), "/upload "):
			return executeTUIUploadCommand(ctx, authRefreshProvider(), trimmed, snapshot)
		case strings.EqualFold(trimmed, knowledgePickerCommandPrefix) || strings.HasPrefix(strings.ToLower(trimmed), knowledgePickerCommandPrefix+" "):
			return executeTUIKnowledgePickerCommand(trimmed, snapshot)
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
		case isMemorySlashCommand(trimmed):
			output, panel, commandErr := a.executeMemorySlashCommand(commandCtx, commandProvider, trimmed, snapshot.DefaultFlow)
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
		case trimmed == "!" || trimmed == "！":
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "! 用法: !<shell 命令>"}}}, nil
		case strings.HasPrefix(trimmed, "!") || strings.HasPrefix(trimmed, "！"):
			return a.executeTUILocalShellCommand(commandCtx, trimmed, snapshot)
		case trimmed == "/clear":
			return tui.LocalCommandResult{Handled: true, ClearHistory: true}, nil
		case strings.EqualFold(trimmed, "/session") || strings.HasPrefix(strings.ToLower(trimmed), "/session "):
			return a.executeTUISessionCommand(commandCtx, commandProvider, trimmed, snapshot)
		case strings.EqualFold(trimmed, "/sessions") || strings.HasPrefix(strings.ToLower(trimmed), "/sessions "):
			page, err := parseSessionsPage(trimmed)
			if err != nil {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
			}
			flowID := strings.TrimSpace(snapshot.DefaultFlow)
			if flowID == "" {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "当前未选择智能体，无法获取服务端会话。"}}}, nil
			}
			items, err := loadSessionPickerItems(commandCtx, commandProvider, flowID, snapshot.SessionID)
			if err != nil {
				return tui.LocalCommandResult{}, fmt.Errorf("%s", explainBackendFailure(err))
			}
			agentLabel := snapshot.DefaultFlowLabel
			if strings.TrimSpace(agentLabel) == "" {
				agentLabel = a.resolveFlowDisplayName(commandCtx, commandProvider, flowID)
			}
			if len(items) == 0 {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: fmt.Sprintf("智能体 %s 当前没有服务端会话。", sanitizeFlowDisplay(agentLabel))}}}, nil
			}
			pages := totalPages(len(items), sessionPickerDisplayPageSize)
			if pages == 0 {
				pages = 1
			}
			if page > pages {
				return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: fmt.Sprintf("页码超出范围，请输入 1 到 %d。", pages)}}}, nil
			}
			return tui.LocalCommandResult{
				Handled: true,
				SessionPicker: &tui.SessionPickerState{
					Title:      fmt.Sprintf("会话列表(session list)（%s）", sanitizeFlowDisplay(agentLabel)),
					Hint:       fmt.Sprintf("已加载 %d 个会话，使用方向键选择，左右键翻页，/ 搜索已加载会话，Enter 切换当前会话，Esc 关闭。", len(items)),
					Page:       page,
					TotalPages: pages,
					PageSize:   sessionPickerDisplayPageSize,
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
			updatedSnapshot.HistoryLoadedCount = 0
			updatedSnapshot.HistoryTotal = 0
			updatedSnapshot.RequestStats = nil
			primaryClient.UpdateSession("")
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

	historyLoad := func(loadCtx context.Context, snapshot tui.Snapshot) (tui.HistoryLoadResult, error) {
		flowID := strings.TrimSpace(snapshot.DefaultFlow)
		sessionID := strings.TrimSpace(snapshot.SessionID)
		if flowID == "" || sessionID == "" {
			return tui.HistoryLoadResult{LoadedCount: snapshot.HistoryLoadedCount, Total: snapshot.HistoryTotal}, nil
		}
		page, err := loadSessionHistoryPage(loadCtx, authRefreshProvider(), flowID, sessionID, snapshot.HistoryLoadedCount, sessionHistoryPageSize)
		if err != nil {
			return tui.HistoryLoadResult{}, fmt.Errorf("%s", explainBackendFailure(err))
		}
		return tui.HistoryLoadResult{
			Entries:     page.Entries,
			LoadedCount: page.LoadedCount,
			Total:       page.Total,
		}, nil
	}

	return tui.Run(ctx, snapshot, flowItems, runner, refreshSnapshot, flowChange, sessionChange, approvalChange, configFormSubmit, loginFormSubmit, localCommand, a.localShellStreamHandler, historyLoad, startupCommand, primaryDispatchCh)
}

func (a *App) buildInitialTUISnapshot(
	config internalconfig.Config,
	flowID string,
	backend backendStatus,
	serviceConfig *provider.ServiceConfig,
) (tui.Snapshot, error) {
	snapshot := tui.Snapshot{
		Version:                       buildinfo.Version,
		BackendURL:                    config.BackendBaseURL,
		BackendAvailable:              backend.Available,
		BackendStatusReason:           strings.TrimSpace(backend.Reason),
		DefaultFlow:                   flowID,
		DefaultFlowLabel:              strings.TrimSpace(flowID),
		Workdir:                       resolveLocalWorkdir(config.Workdir),
		ContextDir:                    resolveLocalWorkdir(config.Workdir),
		RequestMode:                   provider.RequestTypeAgent,
		ApprovalMode:                  config.ApprovalMode,
		ReasoningEnabled:              config.ReasoningEnabled,
		ConfiguredDocUnstructuredExts: internalconfig.NormalizeDocUnstructuredExts(config.DocUnstructuredExts),
	}
	if backend.AuthRequired {
		snapshot.AuthRequired = true
		snapshot.AuthStatusReason = formatAuthRequiredStatusReason(backend.Reason)
	}
	applyServiceConfigToTUISnapshot(&snapshot, serviceConfig)
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
	snapshot.TodoItems = buildTUITodoItems(sessionState.TodoItems)
	snapshot.DefaultFlowLabel = resolveFlowLabel(flowID, snapshot.DefaultFlowLabel, sessionState.FlowDisplayName)
	return snapshot, nil
}

func buildTUIHelpLines(snapshot tui.Snapshot) []string {
	if !snapshot.BackendAvailable {
		reason := strings.TrimSpace(snapshot.BackendStatusReason)
		if reason == "" {
			reason = "服务端不可用"
		}
		return []string{
			"当前处于只读模式。",
			"原因: " + reason,
			"可用命令:",
			"/about 显示当前客户端版本、组织与后端对接说明",
			"/refresh 刷新当前会话的历史消息",
			"/session 查看当前会话；/session title <标题> 设置当前会话标题（新会话除外）",
			"/login 打开登录表单",
			"/clear 清空当前界面消息",
			"/config 打开当前配置弹窗",
			"/todo [show|hide|status] 查看或切换右侧待办栏",
			"/tasks 查看本地后台任务",
			"/memory [status|trigger] 查看或触发跨会话记忆整合",
			"/skills 查看本地技能与远程仓库技能",
			"/reasoning 查看思考模式",
			"/help 查看当前提示",
			"/exit 退出",
		}
	}
	return []string{
		"可用命令:",
		"/about 显示当前客户端版本、组织与后端对接说明",
		"/new 重置当前智能体的本地会话",
		"/refresh 刷新当前会话的历史消息",
		"/clear 清空当前界面消息",
		"/internet [on|off|status] 查看或切换当前对话的联网搜索",
		"/knowledge 打开知识库范围选择器；/knowledge status 查看当前状态；/knowledge auto 切回自动",
		"/upload <本地文件路径> 上传文件并附带到当前对话；/upload status 查看当前文件状态",
		"/review [路径] 发起当前工作区或指定路径的代码审查",
		"/agent 或 /agent <任务> 切换执行模式",
		"/ask 或 /ask <问题> 切换问答模式",
		"/plan 或 /plan <目标> 切换计划模式",
		"/reasoning [on|off|default|status] 切换当前会话思考模式",
		"/session 查看当前会话；/session title <标题> 设置当前会话标题（新会话除外）",
		"/sessions [页码] 查看当前智能体的服务端会话列表",
		"/login 打开登录表单；/login status 和 /login logout 查看或清理当前登录态；图形验证码优先使用 kitty/iTerm2 终端图片协议",
		"/config 打开全局与权限配置弹窗",
		"/todo [show|hide|status] 查看或切换右侧待办栏；有未完成待办时自动显示，全部完成后自动隐藏",
		"!<shell 命令> 在当前目录执行本地命令",
		"/agents 或 /agents <名称> 选择智能体",
		"/tasks [list|output|capture|stop|remove] 管理本地后台任务",
		"/memory [status|trigger] [flow=<智能体>] [key=value] 查看或触发跨会话记忆整合",
		"/skills [list|view|search|view-remote|install|remove|update|source|enable|disable|help] 管理本地技能与远程仓库技能",
		"/exit 退出",
	}
}

func (a *App) buildTUISnapshot(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
	backend backendStatus,
) (tui.Snapshot, error) {
	snapshot := tui.Snapshot{
		Version:                       buildinfo.Version,
		BackendURL:                    config.BackendBaseURL,
		BackendAvailable:              backend.Available,
		BackendStatusReason:           strings.TrimSpace(backend.Reason),
		DefaultFlow:                   flowID,
		DefaultFlowLabel:              a.resolveFlowDisplayName(ctx, providerClient, flowID),
		Workdir:                       resolveLocalWorkdir(config.Workdir),
		ContextDir:                    resolveLocalWorkdir(config.Workdir),
		RequestMode:                   provider.RequestTypeAgent,
		ApprovalMode:                  config.ApprovalMode,
		ReasoningEnabled:              config.ReasoningEnabled,
		ConfiguredDocUnstructuredExts: internalconfig.NormalizeDocUnstructuredExts(config.DocUnstructuredExts),
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
		snapshot.TodoItems = buildTUITodoItems(sessionState.TodoItems)
		snapshot.DefaultFlowLabel = resolveFlowLabel(flowID, snapshot.DefaultFlowLabel, sessionState.FlowDisplayName)
		return snapshot, nil
	}
	if versionInfo, err := providerClient.GetVersionInfo(ctx); err == nil && versionInfo != nil {
		snapshot.ServerPackage = strings.TrimSpace(versionInfo.Package)
		snapshot.ServerVersion = firstNonEmpty(strings.TrimSpace(versionInfo.MainVersion), strings.TrimSpace(versionInfo.Version))
	}
	if currentUser, err := providerClient.GetCurrentUserInfo(ctx); err == nil && currentUser != nil {
		snapshot.CurrentUserName = firstNonEmpty(
			strings.TrimSpace(currentUser.Name),
			firstNonEmpty(strings.TrimSpace(currentUser.UserName), strings.TrimSpace(currentUser.UserID)),
		)
	} else if err != nil && isAuthenticationError(err) {
		snapshot.AuthRequired = true
		snapshot.AuthStatusReason = formatAuthRequiredStatusReason(explainBackendFailure(err))
	}
	if serviceConfig, err := providerClient.GetServiceConfig(ctx); err == nil {
		applyServiceConfigToTUISnapshot(&snapshot, serviceConfig)
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
	snapshot.TodoItems = buildTUITodoItems(sessionState.TodoItems)
	if strings.TrimSpace(snapshot.SessionID) != "" {
		title, titleErr := lookupSessionTitle(ctx, providerClient, flowID, snapshot.SessionID)
		if titleErr == nil && strings.TrimSpace(title) != "" {
			sessionState.SessionTitle = strings.TrimSpace(title)
			snapshot.SessionTitle = resolveSessionTitle(sessionState)
		}
		historyPage, historyErr := loadRecentSessionHistoryPage(ctx, providerClient, flowID, snapshot.SessionID)
		if historyErr == nil {
			snapshot.History = historyPage.Entries
			snapshot.HistoryLoadedCount = historyPage.LoadedCount
			snapshot.HistoryTotal = historyPage.Total
		}
		snapshot.RequestStats = loadLatestSessionMetrics(ctx, providerClient, flowID, snapshot.SessionID)
	}

	metadata, err := providerClient.GetMetadata(ctx, flowID)
	if err == nil {
		applyMetadataToTUISnapshot(&snapshot, metadata)
		applyMetadataContextWindowBudget(&snapshot, metadata.MaxInputTokens)
		if strings.TrimSpace(metadata.Name) != "" {
			snapshot.DefaultFlowLabel = metadata.Name
			sessionState.FlowDisplayName = strings.TrimSpace(metadata.Name)
		}
		if snapshot.KnowledgeSearchSupported {
			ownerAppID, ownerErr := resolveOwnerAppID(ctx, providerClient, flowID)
			if ownerErr != nil {
				return tui.Snapshot{}, ownerErr
			}
			if knowledgeBases, listErr := providerClient.ListKnowledgeBases(ctx, ownerAppID, "", 0, 100); listErr == nil {
				snapshot.KnowledgeBases = buildTUIKnowledgeBaseItems(knowledgeBases, nil)
			}
		}
	}
	snapshot = preserveTUIChatState(tui.Snapshot{}, snapshot)
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

func formatAuthRequiredStatusReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return "服务端已连接，但当前登录态无效或缺失。登录表单已自动打开，也可手动执行 /login。"
	}
	if strings.Contains(trimmed, "/login") {
		return trimmed
	}
	return trimmed + "。登录表单已自动打开，也可手动执行 /login。"
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

func (a *App) executeTUILocalShellCommand(ctx context.Context, command string, snapshot tui.Snapshot) (tui.LocalCommandResult, error) {
	var rawCommand string
	rawCommand = strings.TrimSpace(command)
	if strings.HasPrefix(rawCommand, "！") {
		rawCommand = strings.TrimPrefix(rawCommand, "！")
	} else {
		rawCommand = strings.TrimPrefix(rawCommand, "!")
	}
	rawCommand = strings.TrimSpace(rawCommand)
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
	mode, detail := classifyTUILocalShellCommand(rawCommand)
	switch mode {
	case tuiLocalShellModeBackground:
		return executeTUILocalShellTask(ctx, effectiveConfig, rawCommand, detail)
	case tuiLocalShellModeUnsupported:
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: detail}}}, nil
	default:
		runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), io.Discard, io.Discard)
		result, execErr := runtimeClient.ExecuteShellCommand(ctx, effectiveConfig, rawCommand)
		if execErr != nil {
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: execErr.Error()}}}, nil
		}
		return tui.LocalCommandResult{Handled: true, Events: buildLocalShellCommandEvents(rawCommand, result)}, nil
	}
}

func (a *App) localShellStreamHandler(ctx context.Context, rawCommand string, snapshot tui.Snapshot, emit func(output string, isError bool)) (int, error) {
	localConfig, err := a.configManager.Load()
	if err != nil {
		return -1, err
	}
	effectiveConfig := localConfig
	if flowID := strings.TrimSpace(snapshot.DefaultFlow); flowID != "" {
		sessionState, sessionErr := a.prepareSession(flowID, false, snapshot.Workdir, snapshot.RequestMode)
		if sessionErr == nil {
			effectiveConfig = applyEffectiveSandboxEscapePolicy(localConfig, sessionState.SandboxEscapePolicyOverride, "")
		}
	}
	mode, detail := classifyTUILocalShellCommand(rawCommand)
	if mode == tuiLocalShellModeUnsupported {
		emit(detail+"\n", true)
		return 1, nil
	}
	if mode == tuiLocalShellModeBackground {
		// 后台任务模式不支持流式输出，回退到同步路径
		result, bgErr := executeTUILocalShellTask(ctx, effectiveConfig, rawCommand, "background")
		if bgErr != nil {
			return -1, bgErr
		}
		for _, ev := range result.Events {
			if strings.TrimSpace(ev.Text) != "" {
				emit(ev.Text+"\n", ev.Kind == tui.EventErrorNote)
			}
		}
		return 0, nil
	}
	if strings.TrimSpace(effectiveConfig.ApprovalMode) == "readonly" {
		return -1, fmt.Errorf("当前审批模式为只读，不能执行本地命令")
	}
	runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), io.Discard, io.Discard)
	runtimeClient.SetStreamCallback(emit)
	result, execErr := runtimeClient.ExecuteShellCommand(ctx, effectiveConfig, rawCommand)
	if execErr != nil {
		return -1, execErr
	}
	exitCode := 0
	if code, ok := result["exit_code"].(int); ok {
		exitCode = code
	}
	return exitCode, nil
}

type tuiLocalShellMode string

const (
	tuiLocalShellModeForeground  tuiLocalShellMode = "foreground"
	tuiLocalShellModeBackground  tuiLocalShellMode = "background"
	tuiLocalShellModeUnsupported tuiLocalShellMode = "unsupported"
)

func classifyTUILocalShellCommand(rawCommand string) (tuiLocalShellMode, string) {
	trimmed := strings.TrimSpace(rawCommand)
	if trimmed == "" {
		return tuiLocalShellModeForeground, ""
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return tuiLocalShellModeForeground, ""
	}
	commandName := strings.ToLower(strings.TrimSpace(fields[0]))
	if commandName == "tail" && tailFollowModeRequested(fields[1:]) {
		return tuiLocalShellModeBackground, "follow"
	}
	if isTUIInteractiveShellCommand(commandName) {
		return tuiLocalShellModeUnsupported, fmt.Sprintf("TUI 的 ! 仅支持会自行结束的非交互命令；%s 需要交互终端，请改在系统终端执行。", commandName)
	}
	return tuiLocalShellModeForeground, ""
}

func tailFollowModeRequested(args []string) bool {
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" || trimmed == "--" {
			continue
		}
		if trimmed == "--follow" || strings.HasPrefix(trimmed, "--follow=") {
			return true
		}
		if !strings.HasPrefix(trimmed, "-") {
			continue
		}
		if trimmed == "-f" || trimmed == "-F" {
			return true
		}
		for _, char := range trimmed[1:] {
			if char == 'f' || char == 'F' {
				return true
			}
		}
	}
	return false
}

func isTUIInteractiveShellCommand(commandName string) bool {
	_, ok := tuiInteractiveShellCommands[strings.ToLower(strings.TrimSpace(commandName))]
	return ok
}

var tuiInteractiveShellCommands = map[string]struct{}{
	"htop":   {},
	"less":   {},
	"man":    {},
	"more":   {},
	"nano":   {},
	"screen": {},
	"ssh":    {},
	"tailf":  {},
	"tmux":   {},
	"top":    {},
	"vi":     {},
	"view":   {},
	"vim":    {},
	"watch":  {},
}

func executeTUILocalShellTask(ctx context.Context, config internalconfig.Config, rawCommand string, reason string) (tui.LocalCommandResult, error) {
	runtimeClient := clientruntime.NewClientRuntime(strings.NewReader("\n"), io.Discard, io.Discard)
	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayload(ctx, config, buildLocalToolPayload("shell_exec", map[string]any{
		"rawCommand": rawCommand,
		"background": true,
		"taskName":   buildTUILocalShellTaskName(rawCommand),
	}))
	if err != nil {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
	}
	resultMap, _ := resultAny.(map[string]any)
	if resultMap == nil {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "后台任务已启动，但返回结果无法解析。"}}}, nil
	}
	taskID := strings.TrimSpace(stringMapValue(resultMap, "task_id"))
	status := fallbackString(stringMapValue(resultMap, "status"), "running")
	message := fmt.Sprintf("! %s\n已转为后台任务执行（%s）。", strings.TrimSpace(rawCommand), status)
	if taskID != "" {
		message += fmt.Sprintf(" task_id: %s\n可用 /tasks output %s 查看输出，或 /tasks capture %s 持续读取。", taskID, taskID, taskID)
	}
	if reason == "follow" {
		message += "\n检测到 follow 模式；为避免 TUI 前台阻塞，已自动切到后台任务。"
	}
	panel := buildTUILocalShellTaskPanel(toolName, rawCommand, resultMap)
	events := []tui.Event{{Kind: tui.EventSystemNote, Text: message}}
	if panel != nil {
		events = append(events, tui.Event{Kind: tui.EventToolPanel, Panel: panel})
	}
	return tui.LocalCommandResult{Handled: true, Events: events}, nil
}

func buildTUILocalShellTaskPanel(toolName string, rawCommand string, resultMap map[string]any) *tui.ToolPanel {
	if len(resultMap) == 0 {
		return nil
	}
	taskID := fallbackString(stringMapValue(resultMap, "task_id"), "<unknown>")
	status := fallbackString(stringMapValue(resultMap, "status"), "unknown")
	lines := []string{fmt.Sprintf("命令: %s", strings.TrimSpace(rawCommand))}
	if cwd := strings.TrimSpace(stringMapValue(resultMap, "cwd")); cwd != "" {
		lines = append(lines, "cwd: "+cwd)
	}
	lines = append(lines, "task_id: "+taskID)
	return &tui.ToolPanel{
		Title:   "后台任务(" + strings.TrimSpace(toolName) + ")",
		Summary: fmt.Sprintf("%s · %s", taskID, status),
		Body:    strings.Join(lines, "\n"),
		Hint:    "可用 /tasks output <task_id> 查看输出，/tasks capture <task_id> 持续读取，或 /tasks stop <task_id> 停止任务。",
	}
}

func buildTUILocalShellTaskName(rawCommand string) string {
	trimmed := strings.TrimSpace(rawCommand)
	if trimmed == "" {
		return "TUI 本地命令"
	}
	if len(trimmed) > 48 {
		trimmed = trimmed[:48] + "..."
	}
	return "TUI ! " + trimmed
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

type sessionHistoryPage struct {
	Entries     []tui.HistoryEntry
	LoadedCount int
	Total       int
}

func loadRecentSessionHistoryPage(ctx context.Context, providerClient provider.Provider, flowID string, sessionID string) (sessionHistoryPage, error) {
	return loadSessionHistoryPage(ctx, providerClient, flowID, sessionID, 0, sessionHistoryPageSize)
}

func loadSessionHistoryPage(ctx context.Context, providerClient provider.Provider, flowID string, sessionID string, offset int, limit int) (sessionHistoryPage, error) {
	trimmedFlowID := strings.TrimSpace(flowID)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if providerClient == nil || trimmedFlowID == "" || trimmedSessionID == "" {
		return sessionHistoryPage{}, nil
	}

	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = sessionHistoryPageSize
	}
	messages, err := providerClient.ListSessionMessages(ctx, trimmedFlowID, trimmedSessionID, offset, limit)
	if err != nil {
		return sessionHistoryPage{}, err
	}
	if messages == nil {
		return sessionHistoryPage{LoadedCount: offset, Total: offset}, nil
	}

	entries := make([]tui.HistoryEntry, 0, len(messages.Items))
	for index := len(messages.Items) - 1; index >= 0; index-- {
		item := messages.Items[index]
		role := mapMessageRole(item)
		content := strings.TrimSpace(item.Text)
		if content == "" {
			continue
		}
		entries = append(entries, tui.HistoryEntry{Role: role, Content: content})
	}
	loadedCount := offset + len(messages.Items)
	return sessionHistoryPage{
		Entries:     entries,
		LoadedCount: loadedCount,
		Total:       resolveSessionHistoryTotal(messages, loadedCount),
	}, nil
}

func resolveSessionHistoryTotal(messages *provider.SessionMessageList, loadedCount int) int {
	total := loadedCount
	if messages != nil && messages.Total > total {
		total = messages.Total
	}
	return total
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
				ContextUsageTokens:          preview.ContextWindow.ContextUsageTokens,
				DirectMessageTokens:         preview.ContextWindow.DirectMessageTokens,
				CompressedHistoryTokens:     preview.ContextWindow.CompressedHistoryTokens,
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

func applyMetadataContextWindowBudget(snapshot *tui.Snapshot, maxInputTokens int) {
	if snapshot == nil || maxInputTokens <= 0 {
		return
	}
	if snapshot.RequestStats == nil {
		snapshot.RequestStats = &tui.RequestMetrics{}
	}
	if snapshot.RequestStats.TokenUsage == nil {
		snapshot.RequestStats.TokenUsage = &tui.TokenUsage{}
	}
	if snapshot.RequestStats.TokenUsage.ContextWindow == nil {
		snapshot.RequestStats.TokenUsage.ContextWindow = &tui.ContextWindow{}
	}
	snapshot.RequestStats.TokenUsage.ContextWindow.MaxInputTokens = maxInputTokens
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

// tuiLogWriter 将 runtimeClient 的 stdout/stderr 转换为 TUI 事件。
// suppressMode 用于抑制多行确认提示的续行：一旦遇到以多行抑制前缀开头的行，
// 后续行也会被抑制，直到遇到提示终止符（如 "[y/N]:"）或摘要行。
type tuiLogWriter struct {
	emit         func(tui.Event)
	pending      string
	suppressMode bool
}

// isPromptTerminator 判断一行是否为确认提示的最后一行（等待用户输入的行）。
func isPromptTerminator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.Contains(trimmed, "[y/N]:") {
		return true
	}
	// ask_user 的输入提示行
	askUserPrompts := []string{
		"请输入答案:",
		"请选择序号或候选值:",
		"请选择一个或多个值，多个项用逗号分隔:",
	}
	for _, p := range askUserPrompts {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

// shouldStartMultiLineSuppress 判断抑制前缀是否为多行确认提示的起始行。
// 多行前缀（如 [客户端命令]、[ask_user]）会触发 suppressMode，
// 单行前缀（如 [执行客户端命令]）只抑制当前行，不触发 suppressMode。
func shouldStartMultiLineSuppress(line string) bool {
	multiLinePrefixes := []string{
		"[客户端命令]",
		"[客户端工具]",
		"[路径越界确认]",
		"[ask_user]",
	}
	for _, prefix := range multiLinePrefixes {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func (w *tuiLogWriter) Write(content []byte) (int, error) {
	// 如果在 suppressMode 中且 pending 是终止符，先清除 pending 和 suppressMode
	// 避免 pending 中的终止符与后续内容合并导致误抑制
	if w.suppressMode && w.pending != "" {
		if isPromptTerminator(w.pending) {
			w.pending = ""
			w.suppressMode = false
		}
	}

	text := w.pending + string(content)
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		// 没有完整行，追加到 pending
		w.pending = lines[0]
		return len(content), nil
	}

	for _, line := range lines[:len(lines)-1] {
		trimmed := strings.TrimSpace(line)

		if w.suppressMode {
			// 在抑制模式下，摘要行始终发出（退出抑制模式）
			if summary, ok := parseTUIToolSummary(trimmed); ok {
				w.suppressMode = false
				w.emit(tui.Event{Kind: tui.EventToolSummary, Text: summary})
				continue
			}
			// 新的多行抑制前缀：保持抑制模式（新的确认提示序列）
			if shouldStartMultiLineSuppress(trimmed) {
				continue
			}
			// 终止符：退出抑制模式
			if isPromptTerminator(trimmed) {
				w.suppressMode = false
				continue
			}
			// 续行：抑制但不退出抑制模式
			continue
		}

		if summary, ok := parseTUIToolSummary(trimmed); ok {
			w.emit(tui.Event{Kind: tui.EventToolSummary, Text: summary})
			continue
		}
		if shouldSuppressTUILogLine(trimmed) {
			// 多行前缀进入 suppressMode，单行前缀只抑制当前行
			if shouldStartMultiLineSuppress(trimmed) {
				w.suppressMode = true
			}
			continue
		}
		// 非摘要、非抑制前缀的行（即命令 stdout/stderr 原始输出）不再发射到消息区。
		// 命令执行结果已通过回调提交给服务端，由模型提炼关键信息后输出。
	}
	w.pending = lines[len(lines)-1]
	return len(content), nil
}

func (w *tuiLogWriter) Flush() {
	trimmed := strings.TrimSpace(w.pending)
	if w.suppressMode {
		// 抑制模式下的 pending 行直接丢弃
		w.suppressMode = false
		w.pending = ""
		return
	}
	if summary, ok := parseTUIToolSummary(trimmed); ok {
		w.emit(tui.Event{Kind: tui.EventToolSummary, Text: summary})
	}
	// 非摘要行（命令 stdout/stderr 原始输出）不再发射到消息区。
	w.pending = ""
}

// shouldSuppressTUILogLine 判断一行是否以已知的工具/确认提示前缀开头。
// 注意：此函数只检查行首前缀，多行提示的续行由 tuiLogWriter.suppressMode 状态机处理。
// suppressPrefixes 与 parseTUIToolSummary 的 summaryPrefixes 存在部分重叠
//（[本地工具]、[本地命令]、[浏览器工具]、[桌面工具]），
// 因为这些前缀行既需要作为 ToolSummary 发出（parseTUIToolSummary），
// 又需要在非 Summary 路径中被抑制（shouldSuppressTUILogLine），
// 两者职责不同，保持独立列表更清晰。
// 函数内部对 line 再次 TrimSpace 是为了防御性编程，
// 因为调用方可能传入已 trim 或未 trim 的值。
func shouldSuppressTUILogLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	suppressPrefixes := []string{
		"[执行客户端命令]",
		"[本地工具]",
		"[本地命令]",
		"[浏览器工具]",
		"[桌面工具]",
		"[客户端命令]",
		"[客户端工具]",
		"[路径越界确认]",
		"[ask_user]",
	}
	for _, prefix := range suppressPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func parseTUIToolSummary(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	summaryPrefixes := []string{
		"[本地工具]",
		"[本地命令]",
		"[浏览器工具]",
		"[桌面工具]",
	}
	for _, prefix := range summaryPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix)), true
		}
	}
	return "", false
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
		Hint:         "Y 执行本次命令，N 取消本次命令，A 切换为自动执行；方向键或 Tab 选择，Enter 确认。",
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
	allowBrowserTools func(context.Context) error,
	allowComputerTools func(context.Context) error,
) tui.ConfirmRequest {
	toolName := nestedStringValue(payload, "function", "name")
	arguments, _ := nestedMapValue(payload, "function", "arguments")
	request := tui.ConfirmRequest{
		Title:        "客户端工具(client tool)待确认",
		Command:      clientruntime.PreviewClientToolCall(payload),
		Detail:       "该工具会修改本地工作区或本地运行状态，是否允许执行？",
		Hint:         "Y 允许本次执行，N 本次拒绝，A 切换为自动执行；方向键或 Tab 选择，Enter 确认。",
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
		request.Expanded = true
		request.ApproveLabel = "确认本次写入"
		request.RejectLabel = "取消本次写入"
		request.Hint = "Y 确认本次写入，N 取消本次写入；方向键或 Tab 选择，Enter 确认。"
		request.DisableDefaultAlwaysApprove = true
	default:
		switch {
		case isBrowserToolName(toolName) && allowBrowserTools != nil:
			request.Title = "浏览器操作(browser automation)待确认"
			request.Detail = "该工具会操作浏览器页面、标签页或当前浏览器会话，是否允许执行？"
			request.AlwaysApproveLabel = "始终允许浏览器操作"
			request.AlwaysApprove = allowBrowserTools
			request.Hint = "Y 允许本次执行，N 本次拒绝，A 始终允许浏览器操作；方向键或 Tab 选择，Enter 确认。"
			request.DisableDefaultAlwaysApprove = true
		case isComputerToolName(toolName) && allowComputerTools != nil:
			request.Title = "桌面操作(desktop automation)待确认"
			request.Detail = "该工具会操作当前桌面、窗口、键盘或鼠标，是否允许执行？"
			request.AlwaysApproveLabel = "始终允许桌面操作"
			request.AlwaysApprove = allowComputerTools
			request.Hint = "Y 允许本次执行，N 本次拒绝，A 始终允许桌面操作；方向键或 Tab 选择，Enter 确认。"
			request.DisableDefaultAlwaysApprove = true
		}
	}
	return request
}

func isBrowserToolName(toolName string) bool {
	return strings.HasPrefix(strings.TrimSpace(toolName), "taiyiflow-in-chrome__")
}

func isComputerToolName(toolName string) bool {
	return strings.HasPrefix(strings.TrimSpace(toolName), "taiyiflow-computer__")
}

func (a *App) persistConfigValue(key string, value string) func(context.Context) error {
	return func(context.Context) error {
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return err
		}
		if err := assignConfigValue(&updatedConfig, key, value); err != nil {
			return err
		}
		return a.configManager.Save(updatedConfig)
	}
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
	if toolName == "write_file" {
		lines := []string{
			fmt.Sprintf("--- %s", path),
			fmt.Sprintf("+++ %s", path),
			fmt.Sprintf("@@ write_file mode=%s @@", mode),
		}
		return strings.Join(truncatePreviewLines(appendDiffBlock(lines, "+", stringMapValue(arguments, "content")), 80), "\n")
	}
	lines := []string{
		fmt.Sprintf("--- %s", path),
		fmt.Sprintf("+++ %s", path),
	}
	if expectedHash := strings.TrimSpace(stringMapValue(arguments, "expected_sha256_before")); expectedHash != "" {
		lines = append(lines, fmt.Sprintf("@@ expected_sha256_before=%s @@", expectedHash))
	} else {
		lines = append(lines, "@@")
	}
	lines = appendDiffBlock(lines, "-", stringMapValue(arguments, "old_string"))
	lines = appendDiffBlock(lines, "+", stringMapValue(arguments, "new_string"))
	return strings.Join(truncatePreviewLines(lines, 80), "\n")
}

func appendDiffBlock(lines []string, prefix string, body string) []string {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	if normalized == "" {
		return append(lines, prefix+"<empty>")
	}
	for _, line := range strings.Split(normalized, "\n") {
		lines = append(lines, prefix+line)
	}
	return lines
}

func truncatePreviewLines(lines []string, maxLines int) []string {
	if maxLines < 1 || len(lines) <= maxLines {
		return lines
	}
	truncated := append([]string{}, lines[:maxLines]...)
	truncated = append(truncated, fmt.Sprintf("... (%d more lines)", len(lines)-maxLines))
	return truncated
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

func emitTUIClientToolResultEvents(emit func(tui.Event), config internalconfig.Config, toolName string, result any, toolErr error) {
	// 不在消息区展示工具调用的原始错误信息。
	// 错误已通过 SubmitClientToolCallback 提交给服务端，
	// 由模型提炼关键信息后通过对话输出告知用户。
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
	if strings.TrimSpace(toolName) == "todo_write" {
		if resultMap, ok := result.(map[string]any); ok {
			items := buildTUITodoItemsFromResult(resultMap)
			emit(tui.Event{Kind: tui.EventTodoUpdate, TodoItems: items})
			emit(tui.Event{Kind: tui.EventSystemNote, Text: buildTodoUpdateEventText(items)})
		}
		return
	}
	panel := buildTUIClientToolPanel(toolName, result, toolErr)
	if panel != nil {
		if shouldOpenTUIClientToolPanel(config, toolName, toolErr) {
			emit(tui.Event{Kind: tui.EventToolPanel, Panel: panel})
		}
	}
}

func shouldOpenTUIClientToolPanel(config internalconfig.Config, toolName string, toolErr error) bool {
	return false
}

func buildTUITodoItems(items []agent.TodoItem) []tui.TodoItem {
	if len(items) == 0 {
		return nil
	}
	result := make([]tui.TodoItem, 0, len(items))
	for _, item := range items {
		result = append(result, tui.TodoItem{
			Title:  strings.TrimSpace(item.Title),
			Status: strings.TrimSpace(item.Status),
			Notes:  strings.TrimSpace(item.Notes),
		})
	}
	return result
}

func buildTUITodoItemsFromResult(resultMap map[string]any) []tui.TodoItem {
	items := mapSliceValue(resultMap, "items")
	if len(items) == 0 {
		return nil
	}
	result := make([]tui.TodoItem, 0, len(items))
	for _, item := range items {
		result = append(result, tui.TodoItem{
			Title:  strings.TrimSpace(stringMapValue(item, "title")),
			Status: strings.TrimSpace(stringMapValue(item, "status")),
			Notes:  strings.TrimSpace(stringMapValue(item, "notes")),
		})
	}
	return result
}

func buildTodoContextHint(items []agent.TodoItem) string {
	if len(items) == 0 {
		return ""
	}
	type activeItem struct {
		index  int
		title  string
		status string
		notes  string
	}
	var active []activeItem
	for i, item := range items {
		status := strings.ToLower(strings.TrimSpace(item.Status))
		status = strings.ReplaceAll(status, "-", "_")
		status = strings.ReplaceAll(status, " ", "_")
		if status == "completed" || status == "done" {
			continue
		}
		active = append(active, activeItem{
			index:  i + 1,
			title:  strings.TrimSpace(item.Title),
			status: status,
			notes:  strings.TrimSpace(item.Notes),
		})
	}
	if len(active) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("<todo_reminder>\n")
	sb.WriteString(fmt.Sprintf("你当前有 %d 项未完成的会话待办，请在本次回复中继续关注并推进：\n", len(active)))
	for _, item := range active {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s", item.index, item.status, item.title))
		if item.notes != "" {
			sb.WriteString(fmt.Sprintf("\n   %s", item.notes))
		}
		sb.WriteString("\n")
	}
	sb.WriteString("</todo_reminder>")
	return sb.String()
}

func buildStreamInputWithTodoContext(todoItems []agent.TodoItem, input string) string {
	hint := buildTodoContextHint(todoItems)
	trimmed := strings.TrimSpace(input)
	if hint == "" {
		return trimmed
	}
	if trimmed == "" {
		return hint
	}
	return hint + "\n\n" + trimmed
}

func buildTodoUpdateEventText(items []tui.TodoItem) string {
	if len(items) == 0 {
		return "当前会话暂无待办。"
	}
	activeCount := 0
	for _, item := range items {
		status := strings.ToLower(strings.TrimSpace(item.Status))
		status = strings.ReplaceAll(status, "-", "_")
		status = strings.ReplaceAll(status, " ", "_")
		if status != "completed" && status != "done" {
			activeCount++
		}
	}
	if activeCount == 0 {
		return fmt.Sprintf("会话待办已更新：共 %d 项，已全部完成，右侧待办栏已自动隐藏。", len(items))
	}
	return fmt.Sprintf("会话待办已更新：共 %d 项，未完成 %d 项。", len(items), activeCount)
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
			Hint:    "列表仅显示当前仍在运行的任务；任务结束或停止后会自动从列表移除。",
		}
	case "task_delete":
		return &tui.ToolPanel{
			Title:   "后台任务删除",
			Summary: fmt.Sprintf("%s · %s", fallbackString(stringMapValue(resultMap, "task_id"), "<unknown>"), fallbackString(stringMapValue(resultMap, "status"), "unknown")),
			Body:    "任务及其临时输出已删除。",
			Hint:    "再次查看时，该任务不会出现在 /tasks 列表中。",
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
			Hint:    "该操作只会清理当前会话仍残留的已结束任务；运行中的任务会被保留。",
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
	case "taiyiflow-in-chrome__tabs_context", "taiyiflow-in-chrome__tabs_create", "taiyiflow-in-chrome__navigate", "taiyiflow-in-chrome__click", "taiyiflow-in-chrome__type", "taiyiflow-in-chrome__screenshot", "taiyiflow-in-chrome__read_text", "taiyiflow-in-chrome__read_console":
		return buildTUIBrowserToolPanel(toolName, resultMap)
	case "taiyiflow-computer__displays_context", "taiyiflow-computer__windows_context", "taiyiflow-computer__focus_window", "taiyiflow-computer__screenshot", "taiyiflow-computer__mouse_move", "taiyiflow-computer__mouse_click", "taiyiflow-computer__mouse_drag", "taiyiflow-computer__scroll", "taiyiflow-computer__key_press", "taiyiflow-computer__type_text":
		return buildTUIComputerToolPanel(toolName, resultMap)
	default:
		return nil
	}
}

func buildTUIComputerToolPanel(toolName string, resultMap map[string]any) *tui.ToolPanel {
	switch strings.TrimSpace(toolName) {
	case "taiyiflow-computer__displays_context":
		displays := mapSliceValue(resultMap, "displays")
		if len(displays) == 0 {
			return &tui.ToolPanel{
				Title:   "桌面显示器",
				Summary: "当前没有可用显示器信息",
				Body:    "当前平台没有返回显示器上下文。",
				Hint:    "若需要截图，可先确认 doctor 中的桌面操作状态。",
			}
		}
		lines := make([]string, 0, len(displays))
		for index, item := range displays {
			prefix := "-"
			if boolMapValueAny(item, "primary") {
				prefix = "*"
			}
			lines = append(lines, fmt.Sprintf("%s %d. [%s] %s\n   origin (%d,%d) · %dx%d px · scale %.2f", prefix, index+1, fallbackString(stringMapValue(item, "display_id"), "<unknown>"), fallbackString(stringMapValue(item, "name"), "<unnamed>"), intMapValue(item, "origin_x"), intMapValue(item, "origin_y"), intMapValue(item, "width_px"), intMapValue(item, "height_px"), floatMapValue(item, "scale_factor")))
		}
		return &tui.ToolPanel{
			Title:   "桌面显示器",
			Summary: fmt.Sprintf("displays_context %d 块屏幕", len(displays)),
			Body:    strings.Join(lines, "\n\n"),
			Hint:    "带 * 的显示器是主屏；后续可继续截图或定位窗口。",
		}
	case "taiyiflow-computer__windows_context":
		windows := mapSliceValue(resultMap, "windows")
		if len(windows) == 0 {
			return &tui.ToolPanel{
				Title:   "桌面窗口",
				Summary: "当前没有可用窗口信息",
				Body:    "当前平台没有返回窗口上下文，或未授予辅助功能权限。",
				Hint:    "若返回权限错误，请先在系统设置中授予辅助功能访问。",
			}
		}
		lines := make([]string, 0, len(windows))
		for index, item := range windows {
			prefix := "-"
			if boolMapValueAny(item, "frontmost") {
				prefix = "*"
			}
			lines = append(lines, fmt.Sprintf("%s %d. [%s] %s\n   %s · (%d,%d) %dx%d", prefix, index+1, fallbackString(stringMapValue(item, "window_id"), "<unknown>"), fallbackString(stringMapValue(item, "title"), "<untitled>"), fallbackString(stringMapValue(item, "process_name"), "<process>"), intMapValue(item, "x"), intMapValue(item, "y"), intMapValue(item, "width"), intMapValue(item, "height")))
		}
		return &tui.ToolPanel{
			Title:   "桌面窗口",
			Summary: fmt.Sprintf("windows_context %d 个窗口", len(windows)),
			Body:    strings.Join(lines, "\n\n"),
			Hint:    "带 * 的窗口属于当前前台进程；后续可继续 focus_window、type_text 或截图。",
		}
	case "taiyiflow-computer__screenshot":
		lines := []string{}
		if displayID := strings.TrimSpace(stringMapValue(resultMap, "display_id")); displayID != "" {
			lines = append(lines, "display_id: "+displayID)
		}
		lines = appendDisplayScopeLines(lines, resultMap)
		lines = append(lines, fmt.Sprintf("path: %s", fallbackString(stringMapValue(resultMap, "path"), "<missing>")))
		lines = append(lines, fmt.Sprintf("size: %d bytes", intMapValue(resultMap, "size")))
		lines = appendImageUnderstandingLines(lines, resultMap)
		return &tui.ToolPanel{
			Title:   "桌面截图",
			Summary: fallbackString(stringMapValue(resultMap, "path"), "screenshot"),
			Body:    strings.Join(lines, "\n"),
			Hint:    "截图已保存到本地文件；若结果已返回 image_understanding，优先直接使用该图像理解结果继续处理。",
		}
	default:
		lines := []string{}
		if windowID := strings.TrimSpace(stringMapValue(resultMap, "window_id")); windowID != "" {
			lines = append(lines, "window_id: "+windowID)
		}
		if processName := strings.TrimSpace(stringMapValue(resultMap, "process_name")); processName != "" {
			lines = append(lines, "process_name: "+processName)
		}
		if _, hasFromX := resultMap["from_x"]; hasFromX {
			if _, hasFromY := resultMap["from_y"]; hasFromY {
				lines = append(lines, fmt.Sprintf("from: (%d,%d)", intMapValue(resultMap, "from_x"), intMapValue(resultMap, "from_y")))
			}
		}
		if _, hasToX := resultMap["to_x"]; hasToX {
			if _, hasToY := resultMap["to_y"]; hasToY {
				lines = append(lines, fmt.Sprintf("to: (%d,%d)", intMapValue(resultMap, "to_x"), intMapValue(resultMap, "to_y")))
			}
		}
		if _, hasX := resultMap["x"]; hasX {
			if _, hasY := resultMap["y"]; hasY {
				lines = append(lines, fmt.Sprintf("position: (%d,%d)", intMapValue(resultMap, "x"), intMapValue(resultMap, "y")))
			}
		}
		if _, hasDeltaX := resultMap["delta_x"]; hasDeltaX {
			if _, hasDeltaY := resultMap["delta_y"]; hasDeltaY {
				lines = append(lines, fmt.Sprintf("delta: (%d,%d)", intMapValue(resultMap, "delta_x"), intMapValue(resultMap, "delta_y")))
			}
		}
		if button := strings.TrimSpace(stringMapValue(resultMap, "button")); button != "" {
			lines = append(lines, "button: "+button)
		}
		if clickCount := intMapValue(resultMap, "click_count"); clickCount > 0 {
			lines = append(lines, fmt.Sprintf("click_count: %d", clickCount))
		}
		if durationMS := intMapValue(resultMap, "duration_ms"); durationMS > 0 {
			lines = append(lines, fmt.Sprintf("duration_ms: %d", durationMS))
		}
		if key := strings.TrimSpace(stringMapValue(resultMap, "key")); key != "" {
			lines = append(lines, "key: "+key)
		}
		if textLength := intMapValue(resultMap, "text_length"); textLength > 0 {
			lines = append(lines, fmt.Sprintf("text_length: %d", textLength))
		}
		if len(lines) == 0 {
			lines = append(lines, "桌面操作已执行。")
		}
		return &tui.ToolPanel{
			Title:   "桌面操作结果",
			Summary: strings.TrimPrefix(strings.TrimSpace(toolName), "taiyiflow-computer__"),
			Body:    strings.Join(lines, "\n"),
			Hint:    "若需要继续定位界面状态，可先读取 displays_context、windows_context 或截图。",
		}
	}
}

func buildTUIBrowserToolPanel(toolName string, resultMap map[string]any) *tui.ToolPanel {
	switch strings.TrimSpace(toolName) {
	case "taiyiflow-in-chrome__tabs_context":
		tabs := mapSliceValue(resultMap, "tabs")
		if len(tabs) == 0 {
			return &tui.ToolPanel{
				Title:   "浏览器标签页",
				Summary: "当前没有可用标签页",
				Body:    "当前浏览器没有可用标签页，可先调用 tabs_create 创建新标签页。",
				Hint:    "通常建议先获取 tabs_context，再决定后续点击、输入或导航动作。",
			}
		}
		lines := make([]string, 0, len(tabs))
		for index, item := range tabs {
			prefix := "-"
			if boolMapValueAny(item, "active") {
				prefix = "*"
			}
			lines = append(lines, fmt.Sprintf("%s %d. [%s] %s\n   %s", prefix, index+1, fallbackString(stringMapValue(item, "tab_id"), "<unknown>"), fallbackString(stringMapValue(item, "title"), "<untitled>"), fallbackString(stringMapValue(item, "url"), "<empty>")))
		}
		return &tui.ToolPanel{
			Title:   "浏览器标签页",
			Summary: fmt.Sprintf("tabs_context %d 个标签页", len(tabs)),
			Body:    strings.Join(lines, "\n\n"),
			Hint:    "带 * 的标签页是当前活动标签页；可继续使用 navigate、click、type、read_text 等工具。",
		}
	case "taiyiflow-in-chrome__read_text":
		body := strings.TrimSpace(stringMapValue(resultMap, "text"))
		if body == "" {
			body = "页面文本为空。"
		}
		return &tui.ToolPanel{
			Title:   "页面文本读取",
			Summary: fmt.Sprintf("%s · %s", fallbackString(stringMapValue(resultMap, "tab_id"), "<active>"), fallbackString(stringMapValue(resultMap, "title"), fallbackString(stringMapValue(resultMap, "url"), "read_text"))),
			Body:    body,
			Hint:    "若结果过长，可在下一次调用中传入 max_chars 或更精确的 selector。",
		}
	case "taiyiflow-in-chrome__read_console":
		entries := mapSliceValue(resultMap, "entries")
		if len(entries) == 0 {
			return &tui.ToolPanel{
				Title:   "浏览器控制台",
				Summary: fmt.Sprintf("%s 无控制台输出", fallbackString(stringMapValue(resultMap, "tab_id"), "<active>")),
				Body:    "当前没有捕获到控制台输出或异常。",
				Hint:    "可在页面交互后再次读取 read_console，查看最近日志。",
			}
		}
		lines := make([]string, 0, len(entries))
		for _, entry := range entries {
			lines = append(lines, fmt.Sprintf("[%s] %s\n%s", strings.ToUpper(fallbackString(stringMapValue(entry, "level"), "log")), fallbackString(stringMapValue(entry, "timestamp"), "<unknown>"), fallbackString(stringMapValue(entry, "text"), "<empty>")))
		}
		return &tui.ToolPanel{
			Title:   "浏览器控制台",
			Summary: fmt.Sprintf("read_console %d 条", len(entries)),
			Body:    strings.Join(lines, "\n\n"),
			Hint:    "包含 console 输出与未捕获异常；需要定位 DOM 变化时，建议配合 read_text 或 screenshot。",
		}
	case "taiyiflow-in-chrome__screenshot":
		lines := []string{}
		if title := strings.TrimSpace(stringMapValue(resultMap, "title")); title != "" {
			lines = append(lines, "title: "+title)
			lines = append(lines, "url: "+fallbackString(stringMapValue(resultMap, "url"), "<empty>"))
		}
		lines = append(lines, fmt.Sprintf("tab_id: %s", fallbackString(stringMapValue(resultMap, "tab_id"), "<active>")))
		lines = append(lines, fmt.Sprintf("path: %s", fallbackString(stringMapValue(resultMap, "path"), "<missing>")))
		lines = append(lines, fmt.Sprintf("size: %d bytes", intMapValue(resultMap, "size")))
		lines = appendImageUnderstandingLines(lines, resultMap)
		return &tui.ToolPanel{
			Title:   "浏览器截图",
			Summary: fallbackString(stringMapValue(resultMap, "path"), "screenshot"),
			Body:    strings.Join(lines, "\n"),
			Hint:    "截图已保存到本地文件；若结果已返回 image_understanding，优先直接使用该图像理解结果继续处理页面内容。",
		}
	default:
		lines := []string{}
		if title := strings.TrimSpace(stringMapValue(resultMap, "title")); title != "" {
			lines = append(lines, "title: "+title)
		}
		if url := strings.TrimSpace(stringMapValue(resultMap, "url")); url != "" {
			lines = append(lines, "url: "+url)
		}
		if selector := strings.TrimSpace(stringMapValue(resultMap, "selector")); selector != "" {
			lines = append(lines, "selector: "+selector)
		}
		if textLength := intMapValue(resultMap, "text_length"); textLength > 0 {
			lines = append(lines, fmt.Sprintf("text_length: %d", textLength))
		}
		if len(lines) == 0 {
			lines = append(lines, "浏览器操作已执行。")
		}
		return &tui.ToolPanel{
			Title:   "浏览器操作结果",
			Summary: strings.TrimPrefix(strings.TrimSpace(toolName), "taiyiflow-in-chrome__"),
			Body:    strings.Join(lines, "\n"),
			Hint:    "若需要确认页面状态，可继续读取 tabs_context、read_text 或截图。",
		}
	}
}

func appendDisplayScopeLines(lines []string, payload map[string]any) []string {
	if displayIndex := intMapValue(payload, "display_index"); displayIndex > 0 {
		lines = append(lines, fmt.Sprintf("display_index: %d", displayIndex))
	}
	if _, hasOriginX := payload["display_origin_x"]; hasOriginX {
		lines = append(lines, fmt.Sprintf("origin: (%d,%d)", intMapValue(payload, "display_origin_x"), intMapValue(payload, "display_origin_y")))
	}
	if width := intMapValue(payload, "display_width_px"); width > 0 {
		lines = append(lines, fmt.Sprintf("display_size: %dx%d px", width, intMapValue(payload, "display_height_px")))
	}
	return lines
}

func appendImageUnderstandingLines(lines []string, payload map[string]any) []string {
	if imageUnderstanding := strings.TrimSpace(stringMapValue(payload, "image_understanding")); imageUnderstanding != "" {
		lines = append(lines, "image_understanding: "+imageUnderstanding)
	}
	if imageUnderstanding, ok := nestedMapValue(payload, "image_understanding"); ok {
		if summary := strings.TrimSpace(firstNonEmptyTrimmedStringMapValue(imageUnderstanding, "summary")); summary != "" {
			lines = append(lines, "image_understanding.summary: "+summary)
		}
		if scene := strings.TrimSpace(firstNonEmptyTrimmedStringMapValue(imageUnderstanding, "scene")); scene != "" {
			lines = append(lines, "image_understanding.scene: "+scene)
		}
		if text := strings.TrimSpace(firstNonEmptyTrimmedStringMapValue(imageUnderstanding, "text")); text != "" {
			lines = append(lines, "image_understanding.text: "+text)
		}
	}
	if errText := strings.TrimSpace(stringMapValue(payload, "image_understanding_error")); errText != "" {
		lines = append(lines, "image_understanding_error: "+errText)
	}
	return lines
}

func boolMapValueAny(payload map[string]any, key string) bool {
	value, ok := payload[key]
	if !ok {
		return false
	}
	typed, ok := value.(bool)
	if !ok {
		return false
	}
	return typed
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

func buildTUIAssistantChunkEvent(payload map[string]any) (tui.Event, bool) {
	chunk := firstNonEmptyRawStringMapValue(payload, "chunk", "text")
	if chunk == "" && !splitAssistantMessage(payload) {
		return tui.Event{}, false
	}
	return tui.Event{
		Kind:           tui.EventAssistantChunk,
		Text:           chunk,
		MessageID:      firstNonEmptyTrimmedStringMapValue(payload, "message_id", "id"),
		Stage:          strings.TrimSpace(stringMapValue(payload, "stage")),
		EventType:      strings.TrimSpace(stringMapValue(payload, "event_type")),
		SplitAssistant: splitAssistantMessage(payload),
	}, true
}

func buildTUIStreamStatsMetrics(payload map[string]any) *tui.RequestMetrics {
	if len(payload) == 0 {
		return nil
	}
	metrics := &tui.RequestMetrics{}
	usagePayload := extractStreamTokenUsagePayload(payload)
	if usagePayload != nil {
		tokenUsage := &tui.TokenUsage{
			InputTokens:  firstIntMapValue(usagePayload, "input", "input_tokens", "prompt_tokens"),
			OutputTokens: firstIntMapValue(usagePayload, "output", "output_tokens", "completion_tokens"),
			TotalTokens:  firstIntMapValue(usagePayload, "total", "total_tokens"),
			StopReason:   firstNonEmptyTrimmedStringMapValue(usagePayload, "stop_reason", "finish_reason"),
			ModelName:    firstNonEmptyTrimmedStringMapValue(usagePayload, "model_name", "model"),
		}
		if contextWindow := buildTUIContextWindowFromValue(usagePayload["context_window"]); contextWindow != nil {
			tokenUsage.ContextWindow = contextWindow
		} else if contextWindow := buildTUIContextWindowFromValue(payload["context_window"]); contextWindow != nil {
			tokenUsage.ContextWindow = contextWindow
		}
		if tokenUsage.InputTokens != 0 || tokenUsage.OutputTokens != 0 || tokenUsage.TotalTokens != 0 || tokenUsage.StopReason != "" || tokenUsage.ModelName != "" || tokenUsage.ContextWindow != nil {
			metrics.TokenUsage = tokenUsage
			metrics.RunID = firstNonEmptyTrimmedStringMapValue(usagePayload, "run_id")
		}
	} else if contextWindow := buildTUIContextWindowFromValue(payload["context_window"]); contextWindow != nil {
		metrics.TokenUsage = &tui.TokenUsage{ContextWindow: contextWindow}
	}
	if metrics.TokenUsage == nil {
		return nil
	}
	if metrics.RunID == "" {
		metrics.RunID = firstNonEmptyTrimmedStringMapValue(payload, "run_id")
	}
	return metrics
}

func extractStreamTokenUsagePayload(payload map[string]any) map[string]any {
	if directUsage, ok := payload["token_usage"].(map[string]any); ok {
		return directUsage
	}
	if properties, ok := nestedMapValue(payload, "properties"); ok {
		if nestedUsage, ok := properties["token_usage"].(map[string]any); ok {
			return nestedUsage
		}
	}
	return nil
}

func firstIntMapValue(payload map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		parsed := intMapValue(payload, key)
		if parsed != 0 {
			return parsed
		}
	}
	return 0
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

func floatMapValue(payload map[string]any, key string) float64 {
	value, ok := payload[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return 0
	}
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
				ContextUsageTokens:          stats.TokenUsage.ContextWindow.ContextUsageTokens,
				DirectMessageTokens:         stats.TokenUsage.ContextWindow.DirectMessageTokens,
				CompressedHistoryTokens:     stats.TokenUsage.ContextWindow.CompressedHistoryTokens,
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
	metrics := &tui.RequestMetrics{}
	if contextWindow := buildTUICompressionContextWindow(payload); contextWindow != nil {
		metrics.TokenUsage = &tui.TokenUsage{ContextWindow: contextWindow}
	}
	beforeTokens := payloadIntValue(payload, "before_tokens")
	afterTokens := payloadIntValue(payload, "after_tokens")
	remainingTokens := payloadIntValue(payload, "remaining_tokens")
	compressionRatio := payloadFloatValue(payload, "compression_ratio")
	targetType := strings.TrimSpace(stringMapValue(payload, "target_type"))
	if beforeTokens != 0 || afterTokens != 0 || remainingTokens != 0 || compressionRatio != 0 || targetType != "" {
		count := payloadIntValue(payload, "count")
		if count <= 0 {
			count = 1
		}
		metrics.Compression = &tui.CompressionStats{
			Count:            count,
			BeforeTokens:     beforeTokens,
			AfterTokens:      afterTokens,
			RemainingTokens:  remainingTokens,
			CompressionRatio: compressionRatio,
			TargetType:       targetType,
		}
	}
	if metrics.TokenUsage == nil && metrics.Compression == nil {
		return nil
	}
	return metrics
}

func buildTUICompressionContextWindow(payload map[string]any) *tui.ContextWindow {
	return buildTUIContextWindowFromValue(payload["context_window"])
}

func buildTUIContextWindowFromValue(rawContextWindow any) *tui.ContextWindow {
	if rawContextWindow == nil {
		return nil
	}
	contextWindow, ok := rawContextWindow.(map[string]any)
	if !ok {
		return nil
	}
	resolved := &tui.ContextWindow{
		MaxInputTokens:              payloadIntValue(contextWindow, "max_input_tokens"),
		ContextUsageTokens:          payloadIntValue(contextWindow, "context_usage_tokens"),
		DirectMessageTokens:         payloadIntValue(contextWindow, "direct_message_tokens"),
		CompressedHistoryTokens:     payloadIntValue(contextWindow, "compressed_history_tokens"),
		CurrentRoundInputTokens:     payloadIntValue(contextWindow, "current_round_input_tokens"),
		NextRoundAvailableTokens:    payloadIntValue(contextWindow, "next_round_available_tokens"),
		CurrentRoundAvailableTokens: payloadIntValue(contextWindow, "current_round_available_tokens"),
		HistoryMessageCount:         payloadIntValue(contextWindow, "history_message_count"),
		PreparedMessageCount:        payloadIntValue(contextWindow, "prepared_message_count"),
		HighWatermarkRatio:          payloadFloatValue(contextWindow, "high_watermark_ratio"),
		CompressionTargetRatio:      payloadFloatValue(contextWindow, "compression_target_ratio"),
		HighWatermarkTokens:         payloadIntValue(contextWindow, "high_watermark_tokens"),
		CompressionTargetTokens:     payloadIntValue(contextWindow, "compression_target_tokens"),
	}
	if resolved.MaxInputTokens == 0 &&
		resolved.ContextUsageTokens == 0 &&
		resolved.DirectMessageTokens == 0 &&
		resolved.CompressedHistoryTokens == 0 &&
		resolved.CurrentRoundInputTokens == 0 &&
		resolved.NextRoundAvailableTokens == 0 &&
		resolved.CurrentRoundAvailableTokens == 0 &&
		resolved.HistoryMessageCount == 0 &&
		resolved.PreparedMessageCount == 0 &&
		resolved.HighWatermarkRatio == 0 &&
		resolved.CompressionTargetRatio == 0 &&
		resolved.HighWatermarkTokens == 0 &&
		resolved.CompressionTargetTokens == 0 {
		return nil
	}
	return resolved
}

func buildTUICompressionProgressText(payload map[string]any) string {
	return strings.TrimSpace(buildCompressionProgressText(payload))
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

func loadSessionPickerItems(ctx context.Context, providerClient provider.Provider, flowID string, currentSessionID string) ([]tui.SessionItem, error) {
	sessions, err := providerClient.ListSessions(ctx, flowID, 0, sessionPickerFetchBatchSize)
	if err != nil {
		return nil, err
	}
	if sessions == nil || len(sessions.Items) == 0 {
		return nil, nil
	}
	loaded := append([]provider.SessionSummary{}, sessions.Items...)
	total := sessions.Total
	if total < len(loaded) {
		total = len(loaded)
	}
	for len(loaded) < total {
		batch, batchErr := providerClient.ListSessions(ctx, flowID, len(loaded), sessionPickerFetchBatchSize)
		if batchErr != nil {
			return nil, batchErr
		}
		if batch == nil || len(batch.Items) == 0 {
			break
		}
		loaded = append(loaded, batch.Items...)
		if batch.Total > total {
			total = batch.Total
		}
	}
	sort.SliceStable(loaded, func(left int, right int) bool {
		return sessionSummaryRecentAt(loaded[left]).After(sessionSummaryRecentAt(loaded[right]))
	})
	items := make([]tui.SessionItem, 0, len(loaded))
	for _, item := range loaded {
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
			IsCurrent:    strings.TrimSpace(currentSessionID) != "" && item.ID == currentSessionID,
		})
	}
	return items, nil
}

func sessionSummaryRecentAt(item provider.SessionSummary) time.Time {
	if !item.UpdatedAt.IsZero() {
		return item.UpdatedAt
	}
	return item.CreatedAt
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
		Hint:        "修改服务端地址、浏览器自动化和运行默认项。保存后会立即刷新连接状态。",
		Path:        a.configManager.Path(),
		SubmitText:  "保存配置",
		FlowOptions: flowOptions,
		Values: tui.ConfigFormValues{
			BackendBaseURL:            strings.TrimSpace(config.BackendBaseURL),
			DefaultFlow:               strings.TrimSpace(config.DefaultFlow),
			ApprovalMode:              strings.TrimSpace(config.ApprovalMode),
			SandboxEscapePolicy:       strings.TrimSpace(config.SandboxEscapePolicy),
			SkillReviewPolicy:         strings.TrimSpace(config.SkillReviewPolicy),
			WriteConfirmationPolicy:   strings.TrimSpace(config.WriteConfirmationPolicy),
			SandboxRoots:              formatSandboxRootsInline(config.SandboxRoots),
			PermissionRules:           formatPermissionRulesInline(config.PermissionRules),
			ReasoningEnabled:          config.ReasoningEnabled,
			Workdir:                   strings.TrimSpace(config.Workdir),
			HTTPTimeoutSeconds:        strconv.Itoa(config.HTTPTimeoutSeconds),
			CommandTimeoutSeconds:     strconv.Itoa(config.CommandTimeoutSeconds),
			StreamIdleTimeoutSeconds:  strconv.Itoa(config.StreamIdleTimeoutSeconds),
			SubtaskTimeoutSeconds:     strconv.Itoa(config.SubtaskTimeoutSeconds),
			CrossSessionMemoryEnabled: config.CrossSessionMemoryEnabled,
			QRWaitTimeoutSeconds:      strconv.Itoa(config.QRWaitTimeoutSeconds),
			AgentAutoLoopMaxRounds:    strconv.Itoa(config.AgentAutoLoopMaxRounds),
			OutputLanguage:            strings.TrimSpace(config.OutputLanguage),
			ComputerEnabled:           internalconfig.ComputerEnabled(config.Computer),
			ComputerPermissionMode:    strings.TrimSpace(config.Computer.PermissionMode),
			BrowserMode:               strings.TrimSpace(config.Browser.Mode),
			BrowserPermissionMode:     strings.TrimSpace(config.Browser.PermissionMode),
			BrowserChannel:            strings.TrimSpace(config.Browser.Channel),
			BrowserExecutablePath:     strings.TrimSpace(config.Browser.ExecutablePath),
			BrowserProfileDir:         strings.TrimSpace(config.Browser.ProfileDir),
			BrowserConnectTimeout:     strconv.Itoa(config.Browser.ConnectTimeoutSeconds),
			BrowserExtensionSetup:     strings.TrimSpace(config.Browser.ExtensionSetup),
			BrowserSocketDir:          strings.TrimSpace(config.Browser.SocketDir),
			BrowserRemoteDebuggingURL: strings.TrimSpace(config.Browser.RemoteDebuggingURL),
			BrowserHeadless:           strconv.FormatBool(config.Browser.Headless),
			SkillsInstallScope:        internalconfig.NormalizeSkillInstallScope(config.Skills.InstallScope),
			SkillsAutoInstallPolicy:   internalconfig.NormalizeSkillAutoInstallPolicy(config.Skills.AutoInstallPolicy),
			SkillsFreeTextSearch:      config.Skills.FreeTextSearchEnabled,
			SkillsRegistryURL:         strings.TrimSpace(config.Skills.RegistryURL),
			DocUnstructuredExts:       strings.Join(prefixDocumentExts(config.DocUnstructuredExts), ", "),
			ShellSandboxMode:          strings.TrimSpace(config.ShellSandboxMode),
		},
	}, nil
}

func (a *App) applyConfigFormSubmission(submission tui.ConfigFormSubmission) (internalconfig.Config, error) {
	config, err := a.configManager.Load()
	if err != nil {
		return internalconfig.Config{}, err
	}

	if err := assignConfigValue(&config, "backend_base_url", submission.Values.BackendBaseURL); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "backend_base_url", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.DefaultFlow) == "" {
		config.DefaultFlow = ""
	} else if err := assignConfigValue(&config, "default_flow", submission.Values.DefaultFlow); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "default_flow", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.ApprovalMode) == "" {
		config.ApprovalMode = internalconfig.Default().ApprovalMode
	} else if err := assignConfigValue(&config, "approval_mode", submission.Values.ApprovalMode); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "approval_mode", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.SandboxEscapePolicy) == "" {
		config.SandboxEscapePolicy = internalconfig.Default().SandboxEscapePolicy
	} else if err := assignConfigValue(&config, "external_resource_access_policy", submission.Values.SandboxEscapePolicy); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "external_resource_access_policy", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.SkillReviewPolicy) == "" {
		config.SkillReviewPolicy = internalconfig.Default().SkillReviewPolicy
	} else if err := assignConfigValue(&config, "skill_review_policy", submission.Values.SkillReviewPolicy); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "skill_review_policy", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.WriteConfirmationPolicy) == "" {
		config.WriteConfirmationPolicy = internalconfig.Default().WriteConfirmationPolicy
	} else if err := assignConfigValue(&config, "write_confirmation_policy", submission.Values.WriteConfirmationPolicy); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "write_confirmation_policy", Message: err.Error()}
	}

	config.SandboxRoots = parseSandboxRootsInline(submission.Values.SandboxRoots)
	parsedPermissionRules, err := parsePermissionRulesInline(submission.Values.PermissionRules)
	if err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "permission_rules", Message: err.Error()}
	}
	config.PermissionRules = parsedPermissionRules

	if err := assignConfigValue(&config, "reasoning_enabled", strconv.FormatBool(submission.Values.ReasoningEnabled)); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "reasoning_enabled", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.Workdir) == "" {
		config.Workdir = ""
	} else if err := assignConfigValue(&config, "workdir", submission.Values.Workdir); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "workdir", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.DocUnstructuredExts) == "" {
		config.DocUnstructuredExts = nil
	} else if err := assignConfigValue(&config, "doc_unstructured_exts", submission.Values.DocUnstructuredExts); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "doc_unstructured_exts", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.HTTPTimeoutSeconds) == "" {
		config.HTTPTimeoutSeconds = internalconfig.Default().HTTPTimeoutSeconds
	} else if err := assignConfigValue(&config, "http_timeout_seconds", submission.Values.HTTPTimeoutSeconds); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "http_timeout_seconds", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.CommandTimeoutSeconds) == "" {
		config.CommandTimeoutSeconds = internalconfig.Default().CommandTimeoutSeconds
	} else if err := assignConfigValue(&config, "command_timeout_seconds", submission.Values.CommandTimeoutSeconds); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "command_timeout_seconds", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.StreamIdleTimeoutSeconds) == "" {
		config.StreamIdleTimeoutSeconds = internalconfig.Default().StreamIdleTimeoutSeconds
	} else if err := assignConfigValue(&config, "stream_idle_timeout_seconds", submission.Values.StreamIdleTimeoutSeconds); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "stream_idle_timeout_seconds", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.SubtaskTimeoutSeconds) == "" {
		config.SubtaskTimeoutSeconds = internalconfig.Default().SubtaskTimeoutSeconds
	} else if err := assignConfigValue(&config, "subtask_timeout_seconds", submission.Values.SubtaskTimeoutSeconds); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "subtask_timeout_seconds", Message: err.Error()}
	}

	if err := assignConfigValue(&config, "cross_session_memory_enabled", strconv.FormatBool(submission.Values.CrossSessionMemoryEnabled)); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "cross_session_memory_enabled", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.QRWaitTimeoutSeconds) == "" {
		config.QRWaitTimeoutSeconds = internalconfig.Default().QRWaitTimeoutSeconds
	} else if err := assignConfigValue(&config, "qr_wait_timeout_seconds", submission.Values.QRWaitTimeoutSeconds); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "qr_wait_timeout_seconds", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.AgentAutoLoopMaxRounds) == "" {
		config.AgentAutoLoopMaxRounds = internalconfig.Default().AgentAutoLoopMaxRounds
	} else if err := assignConfigValue(&config, "agent_auto_loop_max_rounds", submission.Values.AgentAutoLoopMaxRounds); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "agent_auto_loop_max_rounds", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.OutputLanguage) == "" {
		config.OutputLanguage = internalconfig.Default().OutputLanguage
	} else if err := assignConfigValue(&config, "output_language", submission.Values.OutputLanguage); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "output_language", Message: err.Error()}
	}

	if err := assignConfigValue(&config, "computer_enabled", strconv.FormatBool(submission.Values.ComputerEnabled)); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "computer_enabled", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.ComputerPermissionMode) == "" {
		config.Computer.PermissionMode = internalconfig.Default().Computer.PermissionMode
	} else if err := assignConfigValue(&config, "computer_permission_mode", submission.Values.ComputerPermissionMode); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "computer_permission_mode", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.BrowserMode) == "" {
		config.Browser.Mode = internalconfig.Default().Browser.Mode
	} else if err := assignConfigValue(&config, "browser_mode", submission.Values.BrowserMode); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "browser_mode", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.BrowserPermissionMode) == "" {
		config.Browser.PermissionMode = internalconfig.Default().Browser.PermissionMode
	} else if err := assignConfigValue(&config, "browser_permission_mode", submission.Values.BrowserPermissionMode); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "browser_permission_mode", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.BrowserChannel) == "" {
		config.Browser.Channel = internalconfig.Default().Browser.Channel
	} else if err := assignConfigValue(&config, "browser_channel", submission.Values.BrowserChannel); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "browser_channel", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.BrowserExecutablePath) == "" {
		config.Browser.ExecutablePath = ""
	} else if err := assignConfigValue(&config, "browser_executable_path", submission.Values.BrowserExecutablePath); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "browser_executable_path", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.BrowserProfileDir) == "" {
		config.Browser.ProfileDir = ""
	} else if err := assignConfigValue(&config, "browser_profile_dir", submission.Values.BrowserProfileDir); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "browser_profile_dir", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.BrowserConnectTimeout) == "" {
		config.Browser.ConnectTimeoutSeconds = internalconfig.Default().Browser.ConnectTimeoutSeconds
	} else if err := assignConfigValue(&config, "browser_connect_timeout_seconds", submission.Values.BrowserConnectTimeout); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "browser_connect_timeout_seconds", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.BrowserExtensionSetup) == "" {
		config.Browser.ExtensionSetup = internalconfig.Default().Browser.ExtensionSetup
	} else if err := assignConfigValue(&config, "browser_extension_setup", submission.Values.BrowserExtensionSetup); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "browser_extension_setup", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.BrowserSocketDir) == "" {
		config.Browser.SocketDir = ""
	} else if err := assignConfigValue(&config, "browser_socket_dir", submission.Values.BrowserSocketDir); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "browser_socket_dir", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.BrowserRemoteDebuggingURL) == "" {
		config.Browser.RemoteDebuggingURL = ""
	} else if err := assignConfigValue(&config, "browser_remote_debugging_url", submission.Values.BrowserRemoteDebuggingURL); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "browser_remote_debugging_url", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.BrowserHeadless) == "" {
		config.Browser.Headless = internalconfig.Default().Browser.Headless
	} else if err := assignConfigValue(&config, "browser_headless", submission.Values.BrowserHeadless); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "browser_headless", Message: err.Error()}
	}

	if err := assignConfigValue(&config, "skills_install_scope", submission.Values.SkillsInstallScope); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "skills_install_scope", Message: err.Error()}
	}
	config.Skills.Directory = ""

	if strings.TrimSpace(submission.Values.SkillsAutoInstallPolicy) == "" {
		config.Skills.AutoInstallPolicy = internalconfig.Default().Skills.AutoInstallPolicy
	} else if err := assignConfigValue(&config, "skills_auto_install_policy", submission.Values.SkillsAutoInstallPolicy); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "skills_auto_install_policy", Message: err.Error()}
	}

	if err := assignConfigValue(&config, "skills_free_text_search", strconv.FormatBool(submission.Values.SkillsFreeTextSearch)); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "skills_free_text_search", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.SkillsRegistryURL) == "" {
		config.Skills.RegistryURL = internalconfig.Default().Skills.RegistryURL
	} else if err := assignConfigValue(&config, "skills_registry_url", submission.Values.SkillsRegistryURL); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "skills_registry_url", Message: err.Error()}
	}

	if strings.TrimSpace(submission.Values.ShellSandboxMode) == "" {
		config.ShellSandboxMode = internalconfig.Default().ShellSandboxMode
	} else if err := assignConfigValue(&config, "shell_sandbox_mode", submission.Values.ShellSandboxMode); err != nil {
		return internalconfig.Config{}, tui.ConfigFieldError{Field: "shell_sandbox_mode", Message: err.Error()}
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

func resolveAgentAutoLoopEnabled(config internalconfig.Config, state agent.SessionState, requestMode string, serviceConfig *provider.ServiceConfig) *bool {
	if normalizeRequestMode(requestMode) != provider.RequestTypeAgent {
		return nil
	}
	value := resolveAgentAutoLoopMaxRounds(config, state, requestMode, serviceConfig) > 0
	return &value
}

func approvedClientToolExecutionConfig(config internalconfig.Config, toolName string) internalconfig.Config {
	execConfig := config
	if strings.TrimSpace(execConfig.ApprovalMode) != "readonly" {
		execConfig.ApprovalMode = "auto"
	}
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedToolName == "" {
		return execConfig
	}
	execConfig.PermissionRules = append(
		[]internalconfig.PermissionRule{{Action: "allow", ToolName: trimmedToolName}},
		execConfig.PermissionRules...,
	)
	return execConfig
}

func resolveAgentAutoLoopMaxRounds(config internalconfig.Config, state agent.SessionState, requestMode string, serviceConfig *provider.ServiceConfig) int {
	if normalizeRequestMode(requestMode) != provider.RequestTypeAgent {
		return 0
	}
	// 优先级：会话级覆盖 > CLI 配置 > 服务端配置
	if state.AgentAutoLoopMaxRoundsOverride != nil {
		auto_loop := config.AgentAutoLoopEnabled
		if state.AgentAutoLoopEnabledOverride != "" {
			override_loop := strings.ToLower(strings.TrimSpace(state.AgentAutoLoopEnabledOverride))
			auto_loop = override_loop == "on" || override_loop == "true" || override_loop == "yes"
		}
		if auto_loop {
			return internalconfig.NormalizeAgentAutoLoopMaxRounds(*state.AgentAutoLoopMaxRoundsOverride)
		} else {
			return 0
		}
	}
	if config.AgentAutoLoopMaxRounds > 0 {
		if config.AgentAutoLoopEnabled {
			return internalconfig.NormalizeAgentAutoLoopMaxRounds(config.AgentAutoLoopMaxRounds)
		} else {
			return 0
		}
	}
	// 使用服务端配置作为默认值
	if serviceConfig != nil && serviceConfig.AgentAutoLoopMaxRounds > 0 {
		if serviceConfig.AgentAutoLoopEnabled {
			return internalconfig.NormalizeAgentAutoLoopMaxRounds(serviceConfig.AgentAutoLoopMaxRounds)
		} else {
			return 0
		}
	}
	return 0
}

func resolveLoopDecideFlowID(config internalconfig.Config, serviceConfig *provider.ServiceConfig) string {
	// 优先级：CLI 配置 > 服务端配置
	if config.LoopDecideFlowID != "" {
		return config.LoopDecideFlowID
	}
	if serviceConfig != nil && serviceConfig.LoopDecideFlowID != "" {
		return serviceConfig.LoopDecideFlowID
	}
	return ""
}

func prepareTUIFollowupRequest(request tui.Request, nextPrompt string) tui.Request {
	request.Input = nextPrompt
	request.RunID = newRequestRunID()
	return request
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

func (a *App) executeTUISessionCommand(ctx context.Context, providerClient provider.Provider, command string, snapshot tui.Snapshot) (tui.LocalCommandResult, error) {
	trimmed := strings.TrimSpace(command)
	lowered := strings.ToLower(trimmed)
	const sessionTitleCommandPrefix = "/session title"

	sessionCommandError := func(text string) (tui.LocalCommandResult, error) {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: text}}}, nil
	}

	switch {
	case strings.EqualFold(trimmed, "/session"):
		resolvedSnapshot := snapshot
		if flowID := strings.TrimSpace(snapshot.DefaultFlow); flowID != "" {
			currentState, err := a.prepareSession(flowID, false, snapshot.Workdir, snapshot.RequestMode)
			if err != nil {
				return tui.LocalCommandResult{}, err
			}
			if strings.TrimSpace(currentState.SessionID) == strings.TrimSpace(resolvedSnapshot.SessionID) && strings.TrimSpace(currentState.SessionID) != "" {
				resolvedSnapshot.SessionTitle = resolveSessionTitle(currentState)
			}
		}

		sessionID := strings.TrimSpace(resolvedSnapshot.SessionID)
		resolvedFlowID := a.resolveFlowIdentifier(ctx, providerClient, snapshot.DefaultFlow)
		if providerClient != nil && sessionID != "" && (strings.TrimSpace(resolvedSnapshot.SessionTitle) == "" || strings.TrimSpace(resolvedSnapshot.SessionTitle) == sessionID) {
			currentState := agent.SessionState{SessionID: sessionID, SessionTitle: strings.TrimSpace(resolvedSnapshot.SessionTitle)}
			if flowID := strings.TrimSpace(snapshot.DefaultFlow); flowID != "" {
				loadedState, err := a.prepareSession(flowID, false, snapshot.Workdir, snapshot.RequestMode)
				if err == nil {
					currentState = loadedState
					currentState.SessionID = sessionID
				}
			}
			title, err := lookupSessionTitle(ctx, providerClient, resolvedFlowID, sessionID)
			if err == nil && strings.TrimSpace(title) != "" {
				currentState.SessionTitle = strings.TrimSpace(title)
				resolvedSnapshot.SessionTitle = resolveSessionTitle(currentState)
			}
		}

		text := tui.RenderSessionStatusText(resolvedSnapshot, "")
		if line := buildSessionMemoryTierLine(loadSessionMemoryPreview(ctx, providerClient, resolvedFlowID, sessionID)); line != "" {
			text = strings.TrimRight(text, "\n") + "\n" + line
		}
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: text}}}, nil
	case strings.EqualFold(trimmed, sessionTitleCommandPrefix), strings.HasPrefix(lowered, sessionTitleCommandPrefix+" "):
		title := strings.TrimSpace(trimmed[len(sessionTitleCommandPrefix):])
		if title == "" {
			return sessionCommandError("用法: /session title <标题>")
		}

		flowID := strings.TrimSpace(snapshot.DefaultFlow)
		if flowID == "" {
			return sessionCommandError("当前未选择智能体，无法设置会话标题。")
		}
		sessionID := strings.TrimSpace(snapshot.SessionID)
		if sessionID == "" {
			return sessionCommandError("当前是新会话，发送首条消息创建会话后才能设置标题。")
		}
		if providerClient == nil {
			return sessionCommandError("当前服务端连接不可用，无法设置会话标题。")
		}

		updatedTitle, err := providerClient.UpdateSessionTitle(ctx, flowID, sessionID, title)
		if err != nil {
			return sessionCommandError(explainBackendFailure(err))
		}
		if strings.TrimSpace(updatedTitle) == "" {
			updatedTitle = title
		}

		currentState, err := a.prepareSession(flowID, false, snapshot.Workdir, snapshot.RequestMode)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		currentState.FlowID = flowID
		currentState.FlowDisplayName = strings.TrimSpace(snapshot.DefaultFlowLabel)
		currentState.SessionID = sessionID
		currentState.SessionTitle = strings.TrimSpace(updatedTitle)
		currentState.Workdir = snapshot.Workdir
		currentState.RequestMode = normalizeRequestMode(firstNonEmpty(snapshot.RequestMode, currentState.RequestMode))
		currentState.ContextDir = resolveLocalWorkdir(firstNonEmpty(currentState.ContextDir, firstNonEmpty(snapshot.ContextDir, snapshot.Workdir)))
		if err := a.sessionStore.Save(currentState); err != nil {
			return tui.LocalCommandResult{}, err
		}

		updatedSnapshot := snapshot
		updatedSnapshot.SessionTitle = resolveSessionTitle(currentState)
		return tui.LocalCommandResult{
			Handled:  true,
			Snapshot: &updatedSnapshot,
			Events: []tui.Event{{
				Kind: tui.EventSystemNote,
				Text: fmt.Sprintf("已将当前会话标题设置为：%s", updatedSnapshot.SessionTitle),
			}},
		}, nil
	default:
		return sessionCommandError("/session 用法: /session 或 /session title <标题>")
	}
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

func executeTUIInternetCommand(command string, snapshot tui.Snapshot) (tui.LocalCommandResult, error) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(command)))
	if len(fields) == 0 {
		return tui.LocalCommandResult{}, nil
	}
	if len(fields) == 1 || (len(fields) == 2 && fields[1] == "status") {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: renderTUIInternetStatus(snapshot)}}}, nil
	}
	if !snapshot.InternetSearchSupported {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "当前智能体未启用联网搜索能力。"}}}, nil
	}
	updatedSnapshot := snapshot
	switch fields[1] {
	case "on":
		updatedSnapshot.InternetSearchEnabled = true
		return tui.LocalCommandResult{Handled: true, Snapshot: &updatedSnapshot, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: "已开启联网搜索。"}}}, nil
	case "off":
		updatedSnapshot.InternetSearchEnabled = false
		return tui.LocalCommandResult{Handled: true, Snapshot: &updatedSnapshot, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: "已关闭联网搜索。"}}}, nil
	default:
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "用法: /internet [on|off|status]"}}}, nil
	}
}

func executeTUIKnowledgeCommand(command string, snapshot tui.Snapshot) (tui.LocalCommandResult, error) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(command)))
	if len(fields) == 0 {
		return tui.LocalCommandResult{}, nil
	}
	if !snapshot.KnowledgeSearchSupported {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "当前智能体未启用知识库检索能力。"}}}, nil
	}
	if len(fields) == 1 {
		picker := buildTUIKnowledgePickerState(snapshot)
		if picker == nil {
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: renderTUIKnowledgeStatus(snapshot)}}}, nil
		}
		return tui.LocalCommandResult{Handled: true, KnowledgePicker: picker}, nil
	}
	switch fields[1] {
	case "status":
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: renderTUIKnowledgeStatus(snapshot)}}}, nil
	case "auto", "clear":
		updatedSnapshot := snapshot
		updatedSnapshot.SelectedKnowledgeBases = nil
		markKnowledgeBaseSelection(&updatedSnapshot)
		return tui.LocalCommandResult{Handled: true, Snapshot: &updatedSnapshot, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: "已将知识库范围切换为 auto（自动）。"}}}, nil
	case "pick", "select":
		picker := buildTUIKnowledgePickerState(snapshot)
		if picker == nil {
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: renderTUIKnowledgeStatus(snapshot)}}}, nil
		}
		return tui.LocalCommandResult{Handled: true, KnowledgePicker: picker}, nil
	default:
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "用法: /knowledge [status|auto]，或直接输入 /knowledge 打开选择器"}}}, nil
	}
}

func executeTUIUploadCommand(ctx context.Context, providerClient provider.Provider, command string, snapshot tui.Snapshot) (tui.LocalCommandResult, error) {
	trimmed := strings.TrimSpace(command)
	if strings.EqualFold(trimmed, "/upload") {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: renderTUIChatFilesStatus(snapshot)}}}, nil
	}
	if !strings.HasPrefix(strings.ToLower(trimmed), "/upload ") {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "用法: /upload <本地文件路径>，或 /upload status"}}}, nil
	}
	argument := strings.TrimSpace(trimmed[len("/upload"):])
	if argument == "" || strings.EqualFold(argument, "status") {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: renderTUIChatFilesStatus(snapshot)}}}, nil
	}
	if !snapshot.BackendAvailable {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "当前服务端不可用，无法上传文件。"}}}, nil
	}
	flowID := strings.TrimSpace(snapshot.DefaultFlow)
	if flowID == "" {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "当前未配置智能体，无法上传文件。"}}}, nil
	}
	ensuredSessionID, err := clientruntime.EnsureUploadSessionID(ctx, providerClient, snapshot.SessionID)
	if err != nil {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
	}
	uploadedPath, err := clientruntime.UploadInputFile(ctx, providerClient, flowID, ensuredSessionID, firstNonEmpty(snapshot.ContextDir, snapshot.Workdir), argument)
	if err != nil {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
	}
	updatedSnapshot := snapshot
	updatedSnapshot.SessionID = ensuredSessionID
	updatedSnapshot.InputFiles = append(append([]string(nil), snapshot.InputFiles...), uploadedPath)
	return tui.LocalCommandResult{
		Handled:  true,
		Snapshot: &updatedSnapshot,
		Events: []tui.Event{{
			Kind: tui.EventSystemNote,
			Text: fmt.Sprintf("已上传文件并附带到当前对话：%s", filepath.Base(argument)),
		}},
	}, nil
}

type tuiRenderedSegment struct {
	Text      string
	Reasoning bool
}

type tuiAssistantRenderState struct {
	insideThink    bool
	pending        string
	pendingUTF8    string
	controlResidue streamControlResidueState
}

func emitTUIAssistantChunk(emit func(tui.Event), state *tuiAssistantRenderState, assistantChunkEvent tui.Event) []tuiRenderedSegment {
	if state == nil {
		return nil
	}
	segments := state.consume(assistantChunkEvent.Text)
	emitted := false
	for _, segment := range segments {
		eventToEmit := assistantChunkEvent
		eventToEmit.Text = segment.Text
		eventToEmit.SplitAssistant = false
		if segment.Reasoning {
			eventToEmit.Kind = tui.EventReasoningChunk
		}
		emit(eventToEmit)
		emitted = true
	}
	if !emitted && assistantChunkEvent.SplitAssistant {
		boundaryEvent := assistantChunkEvent
		boundaryEvent.Text = ""
		emit(boundaryEvent)
	}
	return segments
}

func (s *tuiAssistantRenderState) consume(chunk string) []tuiRenderedSegment {
	// 拼接上一个 chunk 末尾不完整的 UTF-8 字节，确保多字节字符完整后再清洗
	if s.pendingUTF8 != "" {
		chunk = s.pendingUTF8 + chunk
		s.pendingUTF8 = ""
	}
	// 检测并暂存当前 chunk 末尾可能不完整的 UTF-8 序列，
	// 防止水印字符在 SSE 分帧时被拆分到两个 chunk 导致逐 chunk 清洗失效
	if incomplete := trailingIncompleteUTF8(chunk); incomplete != "" {
		s.pendingUTF8 = incomplete
		chunk = chunk[:len(chunk)-len(incomplete)]
	}
	chunk = textsanitize.StripWatermarkCharacters(chunk)
	chunk = stripInvisibleFormatChars(chunk)
	chunk = stripInvalidUTF8(chunk)
	if chunk == "" {
		return nil
	}
	input := s.pending + chunk
	s.pending = ""
	segments := []tuiRenderedSegment{}
	appendSegment := func(text string, reasoning bool) {
		if !reasoning {
			text = s.controlResidue.consume(text)
		}
		if text == "" {
			return
		}
		if len(segments) > 0 && segments[len(segments)-1].Reasoning == reasoning {
			segments[len(segments)-1].Text += text
			return
		}
		segments = append(segments, tuiRenderedSegment{Text: text, Reasoning: reasoning})
	}
	for len(input) > 0 {
		if s.insideThink {
			closeIndex := strings.Index(input, "</think>")
			if closeIndex < 0 {
				fragment := trailingThinkTagFragment(input, "</think>")
				appendSegment(input[:len(input)-len(fragment)], true)
				s.pending = fragment
				return segments
			}
			appendSegment(input[:closeIndex], true)
			input = input[closeIndex+len("</think>"):]
			s.insideThink = false
			continue
		}
		openIndex := strings.Index(input, "<think>")
		closeIndex := strings.Index(input, "</think>")
		if closeIndex >= 0 && (openIndex < 0 || closeIndex < openIndex) {
			appendSegment(input[:closeIndex], false)
			input = input[closeIndex+len("</think>"):]
			continue
		}
		if openIndex < 0 {
			fragment := trailingThinkTagFragment(input, "<think>", "</think>")
			appendSegment(input[:len(input)-len(fragment)], false)
			s.pending = fragment
			return segments
		}
		appendSegment(input[:openIndex], false)
		input = input[openIndex+len("<think>"):]
		s.insideThink = true
	}
	return segments
}

func (s *tuiAssistantRenderState) flush() []tuiRenderedSegment {
	s.pending = ""
	s.pendingUTF8 = ""
	s.controlResidue.flush()
	return nil
}

func loadLatestSearchResultsSummary(ctx context.Context, providerClient provider.Provider, flowID string, sessionID string) string {
	trimmedFlowID := strings.TrimSpace(flowID)
	trimmedSessionID := strings.TrimSpace(sessionID)
	if providerClient == nil || trimmedFlowID == "" || trimmedSessionID == "" {
		return ""
	}
	messages, err := providerClient.ListSessionMessages(ctx, trimmedFlowID, trimmedSessionID, 0, 10)
	if err != nil || messages == nil {
		return ""
	}
	for _, item := range messages.Items {
		if len(item.SearchResults) == 0 {
			continue
		}
		return buildSearchResultsSummary(item.SearchKeyword, item.SearchResults)
	}
	return ""
}

func buildSearchResultsSummary(keyword string, results []provider.SearchResult) string {
	if len(results) == 0 {
		return ""
	}
	lines := []string{"检索结果摘要:"}
	if trimmedKeyword := strings.TrimSpace(keyword); trimmedKeyword != "" {
		lines = append(lines, "关键词: "+trimmedKeyword)
	}
	counters := map[string]int{}
	for _, item := range results {
		prefix, label := retrievalSourceSummaryDescriptor(item.Source)
		counters[prefix]++
		lines = append(lines, fmt.Sprintf("%s.%d %s", prefix, counters[prefix], summarizeSearchResult(item, label)))
	}
	return strings.Join(lines, "\n")
}

func retrievalSourceSummaryDescriptor(source string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "internet":
		return "1", "联网"
	case "personal":
		return "3", "个人"
	default:
		return "2", "共享"
	}
}

func summarizeSearchResult(result provider.SearchResult, sourceLabel string) string {
	title := strings.TrimSpace(result.Title)
	if title == "" {
		title = strings.TrimSpace(result.Text)
	}
	if title == "" {
		title = strings.TrimSpace(result.Filename)
	}
	if title == "" {
		title = strings.TrimSpace(result.Content)
	}
	title = strings.ReplaceAll(title, "\n", " ")
	runes := []rune(title)
	if len(runes) > 36 {
		title = string(runes[:36]) + "..."
	}
	if title == "" {
		title = sourceLabel + "结果"
	}
	if link := strings.TrimSpace(result.Link); link != "" {
		return fmt.Sprintf("%s - %s", title, link)
	}
	return title
}

func executeTUIKnowledgePickerCommand(command string, snapshot tui.Snapshot) (tui.LocalCommandResult, error) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) != 3 || !strings.EqualFold(fields[1], "select") {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "用法: /knowledge-picker select <知识库编码>"}}}, nil
	}
	code := strings.TrimSpace(fields[2])
	if code == "" {
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "知识库编码不能为空。"}}}, nil
	}
	updatedSnapshot := snapshot
	selected := map[string]struct{}{}
	for _, item := range snapshot.SelectedKnowledgeBases {
		selected[strings.TrimSpace(item)] = struct{}{}
	}
	if _, exists := selected[code]; exists {
		delete(selected, code)
	} else {
		selected[code] = struct{}{}
	}
	updatedSnapshot.SelectedKnowledgeBases = sortedKnowledgeCodes(selected, updatedSnapshot.KnowledgeBases)
	markKnowledgeBaseSelection(&updatedSnapshot)
	message := fmt.Sprintf("已更新知识库范围：%s", strings.Join(updatedSnapshot.SelectedKnowledgeBases, ", "))
	if len(updatedSnapshot.SelectedKnowledgeBases) == 0 {
		message = "已将知识库范围切换为 auto（自动）。"
	}
	return tui.LocalCommandResult{Handled: true, Snapshot: &updatedSnapshot, KnowledgePicker: buildTUIKnowledgePickerState(updatedSnapshot), Events: []tui.Event{{Kind: tui.EventSystemNote, Text: message}}}, nil
}

func applyMetadataToTUISnapshot(snapshot *tui.Snapshot, metadata *provider.Metadata) {
	if snapshot == nil || metadata == nil {
		return
	}
	snapshot.WelcomeMessage = strings.TrimSpace(metadata.WelcomeMessage)
	snapshot.SubHead = strings.TrimSpace(metadata.SubHead)
	snapshot.Questions = compactNonEmptyStrings(metadata.Questions)
	capabilities := metadata.Capabilities
	if len(capabilities.Features) == 0 && len(metadata.Features) > 0 {
		capabilities = provider.NormalizeMetadataFeatures(metadata.Features)
	}
	snapshot.InternetSearchSupported = capabilities.InternetSearch
	snapshot.InternetSearchEnabled = capabilities.InternetSearch
	snapshot.InternetSearchDefault = capabilities.InternetSearch
	snapshot.KnowledgeSearchSupported = capabilities.SupportKnowledge || capabilities.KnowledgeSearch
	snapshot.MCPToolsSupported = capabilities.SupportMCPTools
	snapshot.HumanAIDialogueFlowSupported = capabilities.SupportHumanAIDialogue
	refreshHumanAIDialogueSupport(snapshot)
	snapshot.FileCapabilities = buildTUIFileCapabilities(capabilities)
	if !snapshot.KnowledgeSearchSupported {
		snapshot.KnowledgeBases = nil
		snapshot.SelectedKnowledgeBases = nil
	}
}

func applyServiceConfigToTUISnapshot(snapshot *tui.Snapshot, serviceConfig *provider.ServiceConfig) {
	if snapshot == nil || serviceConfig == nil {
		return
	}
	snapshot.MaxFileSizeUploadMB = serviceConfig.MaxFileSizeUpload
	snapshot.SupportedDocumentExts = compactNonEmptyStrings(serviceConfig.DocExts)
	snapshot.DocUnstructuredFlowAvailable = strings.TrimSpace(serviceConfig.DocUnstructuredFlowID) != ""
	snapshot.KnowledgeSystemEnabled = serviceConfig.MilvusEnabled
	snapshot.TextToSpeechSupported = serviceConfig.FeatureFlags.TextToSpeech
	snapshot.SpeechToTextSupported = serviceConfig.FeatureFlags.SpeechToText
	snapshot.HumanAIDialogueSystemEnabled = serviceConfig.FeatureFlags.HumanAIDialogue
	refreshHumanAIDialogueSupport(snapshot)
}

func refreshHumanAIDialogueSupport(snapshot *tui.Snapshot) {
	if snapshot == nil {
		return
	}
	snapshot.HumanAIDialogueSupported = snapshot.HumanAIDialogueSystemEnabled && snapshot.HumanAIDialogueFlowSupported
}

func buildTUIFileCapabilities(capabilities provider.ChatCapabilities) []tui.FileCapability {
	items := []tui.FileCapability{
		{Key: provider.FeatureImageUnderstanding, Label: "图片理解", Enabled: capabilities.ImageUnderstanding},
		{Key: provider.FeatureAudioUnderstanding, Label: "音频理解", Enabled: capabilities.AudioUnderstanding},
		{Key: provider.FeatureVideoUnderstanding, Label: "视频理解", Enabled: capabilities.VideoUnderstanding},
		{Key: provider.FeatureDocumentUnderstanding, Label: "文档理解", Enabled: capabilities.DocumentUnderstanding},
	}
	result := make([]tui.FileCapability, 0, len(items))
	for _, item := range items {
		if item.Enabled {
			result = append(result, item)
		}
	}
	return result
}

func preserveTUIChatState(previous tui.Snapshot, next tui.Snapshot) tui.Snapshot {
	if next.InternetSearchSupported {
		if previous.InternetSearchSupported {
			next.InternetSearchEnabled = previous.InternetSearchEnabled
		}
	} else {
		next.InternetSearchEnabled = false
	}
	next.InputFiles = append([]string(nil), previous.InputFiles...)
	if next.KnowledgeSearchSupported {
		selected := map[string]struct{}{}
		for _, code := range previous.SelectedKnowledgeBases {
			trimmed := strings.TrimSpace(code)
			if trimmed != "" {
				selected[trimmed] = struct{}{}
			}
		}
		next.SelectedKnowledgeBases = sortedKnowledgeCodes(selected, next.KnowledgeBases)
		markKnowledgeBaseSelection(&next)
	} else {
		next.SelectedKnowledgeBases = nil
	}
	return next
}

func buildTUIKnowledgeBaseItems(list *provider.KnowledgeBaseList, selected map[string]struct{}) []tui.KnowledgeBaseItem {
	if list == nil || len(list.Items) == 0 {
		return nil
	}
	items := make([]tui.KnowledgeBaseItem, 0, len(list.Items))
	for _, item := range list.Items {
		code := strings.TrimSpace(item.Code)
		if code == "" {
			code = strings.TrimSpace(item.ID)
		}
		if code == "" {
			continue
		}
		_, isSelected := selected[code]
		items = append(items, tui.KnowledgeBaseItem{
			Code:        code,
			Name:        firstNonEmpty(strings.TrimSpace(item.Name), code),
			Description: strings.TrimSpace(item.Description),
			Type:        strings.TrimSpace(item.Type),
			Selected:    isSelected,
		})
	}
	sort.SliceStable(items, func(i int, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}

func buildTUIKnowledgePickerState(snapshot tui.Snapshot) *tui.KnowledgePickerState {
	if len(snapshot.KnowledgeBases) == 0 {
		return nil
	}
	items := append([]tui.KnowledgeBaseItem(nil), snapshot.KnowledgeBases...)
	return &tui.KnowledgePickerState{
		Title: "知识库范围(knowledge)",
		Hint:  "使用方向键选择，Enter 切换当前知识库是否纳入本次对话范围。",
		Items: items,
	}
}

func markKnowledgeBaseSelection(snapshot *tui.Snapshot) {
	if snapshot == nil {
		return
	}
	selected := map[string]struct{}{}
	for _, code := range snapshot.SelectedKnowledgeBases {
		trimmed := strings.TrimSpace(code)
		if trimmed != "" {
			selected[trimmed] = struct{}{}
		}
	}
	for index := range snapshot.KnowledgeBases {
		_, ok := selected[strings.TrimSpace(snapshot.KnowledgeBases[index].Code)]
		snapshot.KnowledgeBases[index].Selected = ok
	}
}

func sortedKnowledgeCodes(selected map[string]struct{}, items []tui.KnowledgeBaseItem) []string {
	ordered := []string{}
	seen := map[string]struct{}{}
	for _, item := range items {
		code := strings.TrimSpace(item.Code)
		if code == "" {
			continue
		}
		if _, ok := selected[code]; ok {
			ordered = append(ordered, code)
			seen[code] = struct{}{}
		}
	}
	for code := range selected {
		if _, ok := seen[code]; ok {
			continue
		}
		ordered = append(ordered, code)
	}
	sort.Strings(ordered)
	return ordered
}

func renderTUIInternetStatus(snapshot tui.Snapshot) string {
	if !snapshot.InternetSearchSupported {
		return "联网搜索: 当前智能体未启用"
	}
	state := "关闭"
	if snapshot.InternetSearchEnabled {
		state = "开启"
	}
	if snapshot.InternetSearchDefault {
		state += "（默认）"
	}
	return "联网搜索: " + state
}

func renderTUIKnowledgeStatus(snapshot tui.Snapshot) string {
	if !snapshot.KnowledgeSearchSupported {
		return "知识库范围: 当前智能体未启用"
	}
	if len(snapshot.SelectedKnowledgeBases) > 0 {
		return "知识库范围: " + strings.Join(snapshot.SelectedKnowledgeBases, ", ")
	}
	if len(snapshot.KnowledgeBases) > 0 {
		return fmt.Sprintf("知识库范围: auto（自动，可选 %d 个，使用 /knowledge 打开选择器）", len(snapshot.KnowledgeBases))
	}
	return "知识库范围: 当前没有可选知识库"
}

func renderTUIChatFilesStatus(snapshot tui.Snapshot) string {
	lines := []string{}
	if len(snapshot.InputFiles) == 0 {
		lines = append(lines, "文件输入: 当前未附带文件")
	} else {
		lines = append(lines, fmt.Sprintf("文件输入: 已附带 %d 个文件", len(snapshot.InputFiles)))
	}
	if snapshot.MaxFileSizeUploadMB > 0 {
		lines = append(lines, fmt.Sprintf("上传上限: %d MB", snapshot.MaxFileSizeUploadMB))
	}
	if len(snapshot.SupportedDocumentExts) > 0 {
		lines = append(lines, "服务端支持扩展: "+formatDocumentExts(snapshot.SupportedDocumentExts, 16))
	}
	if len(snapshot.ConfiguredDocUnstructuredExts) > 0 {
		lines = append(lines, "结构化扩展: "+formatDocumentExts(snapshot.ConfiguredDocUnstructuredExts, 16))
	}
	if snapshot.DocUnstructuredFlowAvailable {
		lines = append(lines, "结构化解析: 服务端流程已启用")
	} else if len(snapshot.ConfiguredDocUnstructuredExts) > 0 {
		lines = append(lines, "结构化解析: 服务端未配置文档结构化流程")
	}
	if len(snapshot.FileCapabilities) > 0 {
		labels := make([]string, 0, len(snapshot.FileCapabilities))
		for _, item := range snapshot.FileCapabilities {
			if item.Enabled && strings.TrimSpace(item.Label) != "" {
				labels = append(labels, strings.TrimSpace(item.Label))
			}
		}
		if len(labels) > 0 {
			lines = append(lines, "理解能力: "+strings.Join(labels, "、"))
		}
	}
	return strings.Join(lines, "\n")
}

func formatDocumentExts(values []string, limit int) string {
	if limit < 1 || len(values) <= limit {
		return strings.Join(prefixDocumentExts(values), ", ")
	}
	shown := prefixDocumentExts(values[:limit])
	return fmt.Sprintf("%s 等 %d 种", strings.Join(shown, ", "), len(values))
}

func prefixDocumentExts(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.Trim(strings.ToLower(strings.TrimSpace(value)), ".")
		if trimmed != "" {
			result = append(result, "."+trimmed)
		}
	}
	return result
}

func compactNonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func totalPages(total int, pageSize int) int {
	if total <= 0 || pageSize <= 0 {
		return 0
	}
	return (total + pageSize - 1) / pageSize
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
	installedSkillLookup := map[string]skills.InstalledRemoteSkill{}
	installedItems, installedErr := skills.ListInstalledRemoteSkills(skillConfig)
	if installedErr == nil {
		for _, item := range installedItems {
			for _, key := range []string{item.Name, item.Slug, filepath.Base(strings.TrimSpace(item.TargetDir))} {
				trimmed := strings.ToLower(strings.TrimSpace(key))
				if trimmed == "" {
					continue
				}
				installedSkillLookup[trimmed] = item
			}
		}
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
		removeTarget := resolveInstalledRemoteSkillSlug(item, installedSkillLookup)
		pickerItems = append(pickerItems, tui.SkillItem{
			Name:           item.Name,
			Description:    item.Description,
			Category:       item.Category,
			SourceLabel:    sourceDisplayLabel(item.Source),
			StateLabel:     skillStateLabel(item, config),
			Disabled:       containsNormalizedSkillName(config.Skills.Disabled, item.Name),
			RootDir:        item.RootDir,
			RelativeDir:    item.RelativeDir,
			Restriction:    skillRestrictionNote(item.Name),
			SkillMarkdown:  strings.TrimSpace(skills.Markdown(item)),
			ToggleTarget:   removeTarget,
			Removable:      removeTarget != "",
			RemoveTarget:   removeTarget,
			ReferenceFiles: referenceFiles,
		})
	}
	hint := "使用方向键选择，Enter 查看详情，已安装项按 x 禁用或恢复、按 d 删除，Tab 切换标签，Esc 关闭。"
	if len(pickerItems) == 0 {
		hint = "当前没有可展示的技能，Esc 关闭。"
	}
	remoteQuery := strings.TrimSpace(query)
	remoteResults, registryURL, remoteErr := skills.SearchRemote(config, remoteQuery, 20)
	installedMap := map[string]skills.InstalledRemoteSkill{}
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
	currentSkillConfig := func() internalconfig.Config {
		current := skillConfig
		if a.configManager != nil {
			loaded, err := a.configManager.Load()
			if err == nil {
				current = loaded
			}
		}
		if strings.TrimSpace(resolvedContextDir) != "" {
			current.Workdir = resolvedContextDir
		}
		return current
	}
	buildRemotePicker := func(query string) (tui.LocalCommandResult, error) {
		picker, err := buildTUISkillsPicker(currentSkillConfig(), resolvedContextDir, query)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		if picker != nil {
			picker.ActiveTab = "remote"
		}
		return tui.LocalCommandResult{Handled: true, SkillsPicker: picker}, nil
	}
	buildLocalPicker := func(query string) (tui.LocalCommandResult, error) {
		picker, err := buildTUISkillsPicker(currentSkillConfig(), resolvedContextDir, query)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		if picker != nil {
			picker.ActiveTab = "local"
		}
		return tui.LocalCommandResult{Handled: true, SkillsPicker: picker}, nil
	}
	buildDetailResult := func(slug string, query string, note string) (tui.LocalCommandResult, error) {
		result, err := buildRemotePicker(query)
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
	buildLocalDetailResult := func(target string, query string, note string) (tui.LocalCommandResult, error) {
		result, err := buildLocalPicker(query)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		if note != "" {
			result.Events = append(result.Events, tui.Event{Kind: tui.EventSystemNote, Text: note})
		}
		if result.SkillsPicker == nil {
			return result, nil
		}
		for _, item := range result.SkillsPicker.Items {
			if strings.EqualFold(strings.TrimSpace(item.ToggleTarget), strings.TrimSpace(target)) || strings.EqualFold(strings.TrimSpace(item.RemoveTarget), strings.TrimSpace(target)) {
				result.Events = append(result.Events, tui.Event{Kind: tui.EventToolPanel, Panel: buildLocalSkillDetailPanel(item, query)})
				return result, nil
			}
		}
		result.Events = append(result.Events, tui.Event{Kind: tui.EventErrorNote, Text: fmt.Sprintf("未找到已安装技能: %s", target)})
		return result, nil
	}
	splitTargetAndQuery := func(values []string) (string, string, error) {
		if len(values) == 0 || strings.TrimSpace(values[0]) == "" {
			return "", "", fmt.Errorf("缺少技能 slug")
		}
		return strings.TrimSpace(values[0]), strings.TrimSpace(strings.Join(values[1:], " ")), nil
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
		resultData, installErr := skills.InstallRemote(installConfig, slug, "", false)
		if installErr != nil {
			result, pickerErr := buildRemotePicker(query)
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
		result, pickerErr := buildRemotePicker(query)
		if pickerErr != nil {
			return tui.LocalCommandResult{}, pickerErr
		}
		result.Events = []tui.Event{{Kind: tui.EventSystemNote, Text: note}}
		return result, nil
	}
	executeLocalRemove := func(target string, query string, closeToolPanel bool) (tui.LocalCommandResult, error) {
		output, commandErr := a.executeSkillsCommand([]string{"remove", target})
		result, pickerErr := buildLocalPicker(query)
		if pickerErr != nil {
			return tui.LocalCommandResult{}, pickerErr
		}
		if commandErr != nil {
			result.Events = []tui.Event{{Kind: tui.EventErrorNote, Text: commandErr.Error()}}
			return result, nil
		}
		result.CloseToolPanel = closeToolPanel
		result.Events = []tui.Event{{Kind: tui.EventSystemNote, Text: output}}
		return result, nil
	}
	executeLocalToggle := func(target string, query string, enable bool, reopenDetail bool) (tui.LocalCommandResult, error) {
		action := "disable"
		if enable {
			action = "enable"
		}
		output, commandErr := a.executeSkillsCommand([]string{action, target})
		if commandErr != nil {
			result, pickerErr := buildLocalPicker(query)
			if pickerErr != nil {
				return tui.LocalCommandResult{}, pickerErr
			}
			result.Events = []tui.Event{{Kind: tui.EventErrorNote, Text: commandErr.Error()}}
			return result, nil
		}
		if reopenDetail {
			return buildLocalDetailResult(target, query, output)
		}
		result, pickerErr := buildLocalPicker(query)
		if pickerErr != nil {
			return tui.LocalCommandResult{}, pickerErr
		}
		result.Events = []tui.Event{{Kind: tui.EventSystemNote, Text: output}}
		return result, nil
	}
	buildInstallPromptResult := func(slug string, query string, reopenDetail bool) (tui.LocalCommandResult, error) {
		result, err := buildRemotePicker(query)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		defaultScope := a.resolveSkillInstallScope(skillConfig)
		result.Events = append(result.Events, tui.Event{Kind: tui.EventToolPanel, Panel: buildSkillInstallTargetPanel(slug, query, reopenDetail, defaultScope)})
		return result, nil
	}
	switch action {
	case "open", "refresh", "search":
		return buildRemotePicker(strings.TrimSpace(strings.Join(args, " ")))
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
		result, pickerErr := buildRemotePicker(query)
		if pickerErr != nil {
			return tui.LocalCommandResult{}, pickerErr
		}
		if commandErr != nil {
			result.Events = []tui.Event{{Kind: tui.EventErrorNote, Text: commandErr.Error()}}
			return result, nil
		}
		result.Events = []tui.Event{{Kind: tui.EventSystemNote, Text: output}}
		return result, nil
	case "remove-local", "remove-local-detail":
		target, query, err := splitTargetAndQuery(args)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		return executeLocalRemove(target, query, action == "remove-local-detail")
	case "disable-local", "enable-local", "disable-local-detail", "enable-local-detail":
		target, query, err := splitTargetAndQuery(args)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
		return executeLocalToggle(target, query, strings.HasPrefix(action, "enable"), strings.HasSuffix(action, "-detail"))
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
			result, pickerErr := buildRemotePicker(query)
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

func resolveInstalledRemoteSkillSlug(item skills.Skill, installedLookup map[string]skills.InstalledRemoteSkill) string {
	if strings.EqualFold(strings.TrimSpace(item.Source), "bundled") {
		return ""
	}
	for _, candidate := range []string{item.Name, item.RelativeDir, filepath.Base(strings.TrimSpace(item.RootDir))} {
		trimmed := strings.ToLower(strings.TrimSpace(candidate))
		if trimmed == "" {
			continue
		}
		installed, ok := installedLookup[trimmed]
		if ok && strings.TrimSpace(installed.Slug) != "" {
			return strings.TrimSpace(installed.Slug)
		}
	}
	return ""
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
		SkillMarkdown:    strings.TrimSpace(detail.SkillMarkdown),
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

func buildLocalSkillDetailPanel(item tui.SkillItem, query string) *tui.ToolPanel {
	body := []string{
		fmt.Sprintf("名称: %s", item.Name),
		fmt.Sprintf("来源: %s", firstNonEmpty(strings.TrimSpace(item.SourceLabel), "<未填写>")),
		fmt.Sprintf("状态: %s", firstNonEmpty(strings.TrimSpace(item.StateLabel), "<未填写>")),
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
	if strings.TrimSpace(item.SkillMarkdown) != "" {
		body = append(body, "", "SKILL.md 内容:", strings.TrimSpace(item.SkillMarkdown))
	}
	if len(item.ReferenceFiles) > 0 {
		body = append(body, "", "参考文件:")
		for _, file := range item.ReferenceFiles {
			line := "- " + firstNonEmpty(strings.TrimSpace(file.Path), "<未填写>")
			if strings.TrimSpace(file.Description) != "" {
				line += " - " + strings.TrimSpace(file.Description)
			}
			body = append(body, line)
		}
	}
	actions := []tui.ToolPanelAction{}
	if strings.TrimSpace(item.ToggleTarget) != "" {
		toggleAction := tui.ToolPanelAction{
			Key:     "x",
			Label:   "禁用",
			Command: buildHiddenSkillsPickerCommand("disable-local-detail", item.ToggleTarget, query),
		}
		if item.Disabled {
			toggleAction.Label = "恢复候选"
			toggleAction.Command = buildHiddenSkillsPickerCommand("enable-local-detail", item.ToggleTarget, query)
		}
		actions = append(actions, toggleAction)
	}
	actions = append(actions, tui.ToolPanelAction{
		Key:      "d",
		Label:    "删除",
		Command:  buildHiddenSkillsPickerCommand("remove-local-detail", item.RemoveTarget, query),
		Disabled: !item.Removable || strings.TrimSpace(item.RemoveTarget) == "",
	})
	return &tui.ToolPanel{
		Title:   fmt.Sprintf("技能详情｜%s", item.Name),
		Summary: firstNonEmpty(strings.TrimSpace(item.Description), "当前技能没有额外说明。"),
		Body:    strings.Join(body, "\n"),
		Hint:    "Esc 关闭详情面板。",
		Actions: actions,
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
	if strings.TrimSpace(item.SkillMarkdown) != "" {
		lines = append(lines, "", "SKILL.md 内容:", strings.TrimSpace(item.SkillMarkdown))
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
		return nil, "当前没有正在运行的后台任务。", nil
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
		Hint:  "列表仅显示当前仍在运行的任务；任务结束后会自动移除。",
		Items: items,
	}, "", nil
}
