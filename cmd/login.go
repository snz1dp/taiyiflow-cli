package cmd

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/loginprompt"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

type tuiSnapshotRefresh func(context.Context, tui.Snapshot) (tui.Snapshot, []tui.FlowItem, error)

type passwordLoginCommand struct {
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
}

var openCaptchaPreviewFile = defaultOpenCaptchaPreviewFile

type verificationImageRenderData struct {
	ASCII     string
	PNGBase64 string
	Width     int
	Height    int
}

type loginChannelCapabilities struct {
	SupportsQRCode bool
	ChannelNames   []string
}

const (
	loginFormActionPassword  = "password_login"
	loginFormActionStartQR   = "start_qr"
	loginFormActionRefreshQR = "refresh_qr"
	loginFormActionWaitQR    = "wait_qr"
	loginFormActionLogout    = "logout"
)

const (
	loginVerifyTypeKaptcha = loginprompt.VerifyTypeKaptcha
	loginVerifyTypeSMS     = loginprompt.VerifyTypeSMS
	loginVerifyTypeMail    = loginprompt.VerifyTypeMail
	loginVerifyTypeTOTP    = loginprompt.VerifyTypeTOTP
)

const loginQRCodeUnavailableMessage = "当前认证服务未启用扫码登录。"

func (a *App) runLogin(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.runTUI(ctx, []string{"/login"})
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	providerClient := buildProviderClient(config)
	snapshot := a.buildLoginCLISnapshot(ctx, providerClient, config)
	refresh := func(refreshCtx context.Context, _ tui.Snapshot) (tui.Snapshot, []tui.FlowItem, error) {
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return tui.Snapshot{}, nil, err
		}
		providerClient.SetAPIKey(updatedConfig.AccessToken)
		return a.buildLoginCLISnapshot(refreshCtx, providerClient, updatedConfig), nil, nil
	}

	result, err := a.executeTUILoginCommand(ctx, providerClient, snapshot, buildLoginCLICommand(args), refresh)
	if err != nil {
		return err
	}
	if err := a.emitLoginCLIResult(result); err != nil {
		return err
	}
	if shouldAutoWaitQRCodeCLI(args, result.LoginForm) {
		return a.completeCLIQRCodeLogin(ctx, providerClient, snapshot, refresh, result.LoginForm)
	}
	return nil
}

func (a *App) executeTUILoginCommand(
	ctx context.Context,
	providerClient *providerclient.Client,
	snapshot tui.Snapshot,
	command string,
	refresh tuiSnapshotRefresh,
) (tui.LocalCommandResult, error) {
	trimmed := strings.TrimSpace(command)
	localConfig, err := a.configManager.Load()
	if err != nil {
		return tui.LocalCommandResult{}, err
	}
	if shouldEnsureLoginDeviceID(trimmed) {
		localConfig, err = a.ensureLoginDeviceID(localConfig)
		if err != nil {
			return tui.LocalCommandResult{}, err
		}
	}
	authClient := providerclient.NewAuthClient(localConfig.BackendBaseURL)
	capabilities := loadLoginChannelCapabilities(ctx, authClient)

	switch {
	case strings.EqualFold(trimmed, "/login"):
		return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, "password", capabilities, passwordLoginCommand{Remember: true}, nil)}, nil
	case strings.EqualFold(trimmed, "/login help"):
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: loginSlashUsage(capabilities.SupportsQRCode)}}}, nil
	case strings.EqualFold(trimmed, "/login status"):
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: renderLoginStatus(localConfig, snapshot)}}}, nil
	case strings.EqualFold(trimmed, "/login logout"):
		return a.executeTUILogout(ctx, providerClient, snapshot, localConfig, refresh)
	case strings.EqualFold(trimmed, "/login qr") || strings.EqualFold(trimmed, "/login qr start"):
		if !capabilities.SupportsQRCode {
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: loginQRCodeUnavailableMessage}}}, nil
		}
		qrToken, err := authClient.GetQRCodeLoginToken(ctx, "", false, "login", "")
		if err != nil {
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
		}
		loginForm := buildTUILoginFormState(localConfig, snapshot, "qr", capabilities, passwordLoginCommand{Remember: true}, qrToken)
		if loginForm != nil {
			loginForm.QRWaiting = true
		}
		return tui.LocalCommandResult{Handled: true, LoginForm: loginForm}, nil
	case strings.HasPrefix(strings.ToLower(trimmed), "/login password "), strings.HasPrefix(strings.ToLower(trimmed), "/login qr wait "):
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "旧登录命令已移除，请直接使用 /login。"}}}, nil
	default:
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: loginSlashUsage(capabilities.SupportsQRCode)}}}, nil
	}
}

func shouldEnsureLoginDeviceID(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "/login help", "/login logout":
		return false
	default:
		return true
	}
}

func qrWaitTimeoutSeconds(config internalconfig.Config) int {
	return internalconfig.NormalizeQRWaitTimeoutSeconds(config.QRWaitTimeoutSeconds)
}

func (a *App) ensureLoginDeviceID(config internalconfig.Config) (internalconfig.Config, error) {
	if strings.TrimSpace(config.DeviceID) != "" {
		return config, nil
	}
	generatedDeviceID, err := generateDeviceID()
	if err != nil {
		return config, err
	}
	config.DeviceID = generatedDeviceID
	if err := a.configManager.Save(config); err != nil {
		return config, err
	}
	return config, nil
}

func (a *App) submitTUILoginForm(
	ctx context.Context,
	providerClient *providerclient.Client,
	snapshot tui.Snapshot,
	submission tui.LoginFormSubmission,
	refresh tuiSnapshotRefresh,
) (tui.LocalCommandResult, error) {
	localConfig, err := a.configManager.Load()
	if err != nil {
		return tui.LocalCommandResult{}, err
	}
	authClient := providerclient.NewAuthClient(localConfig.BackendBaseURL)
	capabilities := loadLoginChannelCapabilities(ctx, authClient)
	remember := submission.Remember
	if strings.TrimSpace(submission.Mode) == "" {
		submission.Mode = "password"
	}
	switch strings.TrimSpace(submission.Action) {
	case loginFormActionLogout:
		return a.executeTUILogout(ctx, providerClient, snapshot, localConfig, refresh)
	case loginFormActionStartQR, loginFormActionRefreshQR:
		if !capabilities.SupportsQRCode {
			parsed := passwordLoginCommand{
				Username:               strings.TrimSpace(submission.Username),
				Password:               submission.Password,
				PasswordPublicKey:      strings.TrimSpace(submission.PasswordPublicKey),
				PasswordTicket:         strings.TrimSpace(submission.PasswordTicket),
				Remember:               remember,
				VerifyType:             normalizeLoginVerifyType(submission.VerifyType),
				VerifyHint:             strings.TrimSpace(submission.VerifyHint),
				VerifyTarget:           strings.TrimSpace(submission.VerifyTarget),
				VerifyImageASCII:       submission.VerifyImageASCII,
				VerifyImagePNGBase64:   submission.VerifyImagePNGBase64,
				VerifyImageWidth:       submission.VerifyImageWidth,
				VerifyImageHeight:      submission.VerifyImageHeight,
				VerifyImageURL:         strings.TrimSpace(submission.VerifyImageURL),
				VerifyImagePreviewPath: strings.TrimSpace(submission.VerifyImagePreviewPath),
				Ticket:                 strings.TrimSpace(submission.Ticket),
				Code:                   strings.TrimSpace(submission.Code),
			}
			return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildTUILoginFormState(localConfig, snapshot, "password", capabilities, parsed, nil), loginQRCodeUnavailableMessage, passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: loginQRCodeUnavailableMessage}}}, nil
		}
		qrToken, err := authClient.GetQRCodeLoginToken(ctx, "", false, "login", "")
		parsed := passwordLoginCommand{
			Username:               strings.TrimSpace(submission.Username),
			Password:               submission.Password,
			PasswordPublicKey:      strings.TrimSpace(submission.PasswordPublicKey),
			PasswordTicket:         strings.TrimSpace(submission.PasswordTicket),
			Remember:               remember,
			VerifyType:             normalizeLoginVerifyType(submission.VerifyType),
			VerifyHint:             strings.TrimSpace(submission.VerifyHint),
			VerifyTarget:           strings.TrimSpace(submission.VerifyTarget),
			VerifyImageASCII:       submission.VerifyImageASCII,
			VerifyImagePNGBase64:   submission.VerifyImagePNGBase64,
			VerifyImageWidth:       submission.VerifyImageWidth,
			VerifyImageHeight:      submission.VerifyImageHeight,
			VerifyImageURL:         strings.TrimSpace(submission.VerifyImageURL),
			VerifyImagePreviewPath: strings.TrimSpace(submission.VerifyImagePreviewPath),
			Ticket:                 strings.TrimSpace(submission.Ticket),
			Code:                   strings.TrimSpace(submission.Code),
		}
		if err != nil {
			return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, "qr", capabilities, parsed, qrTokenFromSubmission(submission)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
		}
		loginForm := buildTUILoginFormState(localConfig, snapshot, "qr", capabilities, parsed, qrToken)
		if loginForm != nil {
			loginForm.QRWaiting = true
			loginForm.QRWaitPollCount = 0
		}
		return tui.LocalCommandResult{Handled: true, LoginForm: loginForm}, nil
	case loginFormActionWaitQR:
		if !capabilities.SupportsQRCode {
			parsed := passwordLoginCommand{
				Username:               strings.TrimSpace(submission.Username),
				Password:               submission.Password,
				PasswordPublicKey:      strings.TrimSpace(submission.PasswordPublicKey),
				PasswordTicket:         strings.TrimSpace(submission.PasswordTicket),
				Remember:               remember,
				VerifyType:             normalizeLoginVerifyType(submission.VerifyType),
				VerifyHint:             strings.TrimSpace(submission.VerifyHint),
				VerifyTarget:           strings.TrimSpace(submission.VerifyTarget),
				VerifyImageASCII:       submission.VerifyImageASCII,
				VerifyImagePNGBase64:   submission.VerifyImagePNGBase64,
				VerifyImageWidth:       submission.VerifyImageWidth,
				VerifyImageHeight:      submission.VerifyImageHeight,
				VerifyImageURL:         strings.TrimSpace(submission.VerifyImageURL),
				VerifyImagePreviewPath: strings.TrimSpace(submission.VerifyImagePreviewPath),
				Ticket:                 strings.TrimSpace(submission.Ticket),
				Code:                   strings.TrimSpace(submission.Code),
			}
			return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildTUILoginFormState(localConfig, snapshot, "password", capabilities, parsed, nil), loginQRCodeUnavailableMessage, passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: loginQRCodeUnavailableMessage}}}, nil
		}
		return a.executeTUIQRWait(ctx, providerClient, snapshot, localConfig, authClient, remember, submission.QRTicket, submission.QRLoginURL, submission.QRScanHint, submission.QRWaitPollCount, refresh)
	case "", loginFormActionPassword:
		parsed := passwordLoginCommand{
			Username:               strings.TrimSpace(submission.Username),
			Password:               submission.Password,
			PasswordPublicKey:      strings.TrimSpace(submission.PasswordPublicKey),
			PasswordTicket:         strings.TrimSpace(submission.PasswordTicket),
			Remember:               remember,
			VerifyType:             normalizeLoginVerifyType(submission.VerifyType),
			VerifyHint:             strings.TrimSpace(submission.VerifyHint),
			VerifyTarget:           strings.TrimSpace(submission.VerifyTarget),
			VerifyImageASCII:       submission.VerifyImageASCII,
			VerifyImagePNGBase64:   submission.VerifyImagePNGBase64,
			VerifyImageWidth:       submission.VerifyImageWidth,
			VerifyImageHeight:      submission.VerifyImageHeight,
			VerifyImageURL:         strings.TrimSpace(submission.VerifyImageURL),
			VerifyImagePreviewPath: strings.TrimSpace(submission.VerifyImagePreviewPath),
			Ticket:                 strings.TrimSpace(submission.Ticket),
			Code:                   strings.TrimSpace(submission.Code),
		}
		return a.executeTUIPasswordLogin(ctx, providerClient, snapshot, localConfig, authClient, capabilities, parsed, refresh)
	default:
		return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, submission.Mode, capabilities, passwordLoginCommand{
			Username:               strings.TrimSpace(submission.Username),
			Password:               submission.Password,
			PasswordPublicKey:      strings.TrimSpace(submission.PasswordPublicKey),
			PasswordTicket:         strings.TrimSpace(submission.PasswordTicket),
			Remember:               remember,
			VerifyType:             normalizeLoginVerifyType(submission.VerifyType),
			VerifyHint:             strings.TrimSpace(submission.VerifyHint),
			VerifyTarget:           strings.TrimSpace(submission.VerifyTarget),
			VerifyImageASCII:       submission.VerifyImageASCII,
			VerifyImagePNGBase64:   submission.VerifyImagePNGBase64,
			VerifyImageWidth:       submission.VerifyImageWidth,
			VerifyImageHeight:      submission.VerifyImageHeight,
			VerifyImageURL:         strings.TrimSpace(submission.VerifyImageURL),
			VerifyImagePreviewPath: strings.TrimSpace(submission.VerifyImagePreviewPath),
			Ticket:                 strings.TrimSpace(submission.Ticket),
			Code:                   strings.TrimSpace(submission.Code),
		}, qrTokenFromSubmission(submission)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "不支持的登录动作。"}}}, nil
	}
}

func loadLoginChannelCapabilities(ctx context.Context, authClient *providerclient.AuthClient) loginChannelCapabilities {
	if authClient == nil {
		return loginChannelCapabilities{}
	}
	channelNames, err := authClient.GetChannelNames(ctx)
	if err != nil {
		return loginChannelCapabilities{}
	}
	return loginChannelCapabilities{SupportsQRCode: len(channelNames) > 0, ChannelNames: channelNames}
}

func (a *App) refreshLoginSnapshotResult(
	ctx context.Context,
	snapshot tui.Snapshot,
	refresh tuiSnapshotRefresh,
	message string,
) (tui.LocalCommandResult, error) {
	trimmedMessage := strings.TrimSpace(message)
	result := tui.LocalCommandResult{Handled: true}
	if trimmedMessage != "" {
		result.Events = []tui.Event{{Kind: tui.EventSystemNote, Text: trimmedMessage}}
		result.FooterHint = trimmedMessage
	}
	if refresh == nil {
		return result, nil
	}
	updatedSnapshot, updatedFlows, err := refresh(ctx, snapshot)
	if err != nil {
		return tui.LocalCommandResult{}, err
	}
	result.Snapshot = &updatedSnapshot
	result.Flows = updatedFlows
	if trimmedMessage != "" {
		if currentUser := strings.TrimSpace(updatedSnapshot.CurrentUserName); currentUser != "" {
			result.FooterHint = joinLoginFooterHint(trimmedMessage, currentUser+"(已登录)")
		}
	}
	return result, nil
}

func (a *App) refreshLoginSnapshotResultAfterNewSession(
	ctx context.Context,
	snapshot tui.Snapshot,
	refresh tuiSnapshotRefresh,
	message string,
) (tui.LocalCommandResult, error) {
	if a != nil && a.sessionStore != nil && strings.TrimSpace(snapshot.DefaultFlow) != "" {
		if err := a.sessionStore.Delete(snapshot.DefaultFlow, snapshot.Workdir); err != nil {
			return tui.LocalCommandResult{}, err
		}
	}
	result, err := a.refreshLoginSnapshotResult(ctx, snapshot, refresh, message)
	if err != nil {
		return tui.LocalCommandResult{}, err
	}
	result.ClearHistory = true
	result.RebuildEntries = true
	result.Events = append(result.Events, tui.Event{Kind: tui.EventSessionUpdate, SessionID: ""})
	if result.Snapshot == nil {
		updatedSnapshot := resetSnapshotForNewSession(snapshot)
		result.Snapshot = &updatedSnapshot
		return result, nil
	}
	updatedSnapshot := resetSnapshotForNewSession(*result.Snapshot)
	result.Snapshot = &updatedSnapshot
	return result, nil
}

func resetSnapshotForNewSession(snapshot tui.Snapshot) tui.Snapshot {
	updatedSnapshot := snapshot
	updatedSnapshot.SessionID = ""
	updatedSnapshot.SessionTitle = ""
	updatedSnapshot.RequestMode = provider.RequestTypeAgent
	updatedSnapshot.ContextDir = resolveLocalWorkdir(snapshot.Workdir)
	updatedSnapshot.SessionSandboxEscapePolicy = ""
	updatedSnapshot.History = nil
	updatedSnapshot.RequestStats = preserveNewSessionRequestStats(snapshot.RequestStats)
	return updatedSnapshot
}

func preserveNewSessionRequestStats(source *tui.RequestMetrics) *tui.RequestMetrics {
	if source == nil || source.TokenUsage == nil || source.TokenUsage.ContextWindow == nil {
		return nil
	}
	contextWindow := source.TokenUsage.ContextWindow
	if contextWindow.MaxInputTokens <= 0 {
		return nil
	}
	return &tui.RequestMetrics{
		TokenUsage: &tui.TokenUsage{
			ContextWindow: &tui.ContextWindow{
				MaxInputTokens:              contextWindow.MaxInputTokens,
				NextRoundAvailableTokens:    contextWindow.MaxInputTokens,
				CurrentRoundAvailableTokens: contextWindow.MaxInputTokens,
				HighWatermarkRatio:          contextWindow.HighWatermarkRatio,
				CompressionTargetRatio:      contextWindow.CompressionTargetRatio,
				HighWatermarkTokens:         contextWindow.HighWatermarkTokens,
				CompressionTargetTokens:     contextWindow.CompressionTargetTokens,
			},
		},
	}
}

func joinLoginFooterHint(parts ...string) string {
	trimmedParts := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			trimmedParts = append(trimmedParts, trimmed)
		}
	}
	return strings.Join(trimmedParts, " ")
}

func loginSlashUsage(supportsQRCode bool) string {
	lines := []string{
		"登录命令:",
		"/login 打开登录表单",
		"/login status 查看当前登录状态",
		"/login logout 清除本地登录态",
		"提示: 图形验证码优先使用 kitty 与 iTerm2 终端图片协议；在 tmux 中会自动透传到底层终端。",
		"提示: 可通过 TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL=kitty|iterm2|off 显式指定或关闭图片协议。",
	}
	if supportsQRCode {
		lines = append(lines, "/login qr 在终端显示二维码，并自动等待扫码登录完成")
	} else {
		lines = append(lines, "提示: 当前认证服务未启用扫码登录，仅支持密码登录。")
	}
	return strings.Join(lines, "\n")
}

func buildLoginCLICommand(args []string) string {
	trimmedArgs := make([]string, 0, len(args)+1)
	trimmedArgs = append(trimmedArgs, "/login")
	for _, arg := range args {
		if trimmed := strings.TrimSpace(arg); trimmed != "" {
			trimmedArgs = append(trimmedArgs, trimmed)
		}
	}
	return strings.Join(trimmedArgs, " ")
}

func (a *App) buildLoginCLISnapshot(ctx context.Context, providerClient provider.Provider, config internalconfig.Config) tui.Snapshot {
	snapshot := tui.Snapshot{
		BackendURL:       strings.TrimSpace(config.BackendBaseURL),
		BackendAvailable: strings.TrimSpace(config.BackendBaseURL) != "",
		DefaultFlow:      strings.TrimSpace(config.DefaultFlow),
		Workdir:          resolveLocalWorkdir(config.Workdir),
		ContextDir:       resolveLocalWorkdir(config.Workdir),
	}
	if !snapshot.BackendAvailable || strings.TrimSpace(config.AccessToken) == "" {
		a.loadLoginSnapshotSession(ctx, providerClient, config, &snapshot)
		return snapshot
	}
	currentUser, err := providerClient.GetCurrentUserInfo(ctx)
	if err == nil {
		snapshot.CurrentUserName = strings.TrimSpace(currentUser.Name)
		a.loadLoginSnapshotSession(ctx, providerClient, config, &snapshot)
		return snapshot
	}
	if isAuthenticationError(err) {
		snapshot.AuthRequired = true
		snapshot.AuthStatusReason = "服务端已连接，但当前登录态无效或缺失。"
		a.loadLoginSnapshotSession(ctx, providerClient, config, &snapshot)
		return snapshot
	}
	snapshot.BackendAvailable = false
	snapshot.BackendStatusReason = err.Error()
	a.loadLoginSnapshotSession(ctx, providerClient, config, &snapshot)
	return snapshot
}

func (a *App) loadLoginSnapshotSession(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, snapshot *tui.Snapshot) {
	if a == nil || a.sessionStore == nil || snapshot == nil {
		return
	}
	flowID := strings.TrimSpace(config.DefaultFlow)
	if flowID == "" {
		return
	}
	sessionState, err := a.prepareSession(flowID, false, snapshot.Workdir, "")
	if err != nil {
		return
	}
	snapshot.SessionID = sessionState.SessionID
	snapshot.SessionTitle = resolveSessionTitle(sessionState)
	snapshot.ContextDir = resolveLocalWorkdir(firstNonEmpty(sessionState.ContextDir, snapshot.Workdir))
	if strings.TrimSpace(snapshot.SessionID) == "" || providerClient == nil || snapshot.AuthRequired || !snapshot.BackendAvailable {
		return
	}
	if title, titleErr := lookupSessionTitle(ctx, providerClient, flowID, snapshot.SessionID); titleErr == nil && strings.TrimSpace(title) != "" {
		snapshot.SessionTitle = strings.TrimSpace(title)
	}
	snapshot.History = loadRecentSessionHistory(ctx, providerClient, flowID, snapshot.SessionID)
	snapshot.RequestStats = loadLatestSessionMetrics(ctx, providerClient, flowID, snapshot.SessionID)
}

func (a *App) emitLoginCLIResult(result tui.LocalCommandResult) error {
	return a.emitLoginCLIResultWithOptions(result, true)
}

func (a *App) emitLoginCLIResultWithOptions(result tui.LocalCommandResult, includeQRCode bool) error {
	lastSystemNote := ""
	for _, event := range result.Events {
		text := strings.TrimSpace(event.Text)
		if text == "" {
			continue
		}
		switch event.Kind {
		case tui.EventErrorNote:
			fmt.Fprintln(a.stderr, text)
		default:
			fmt.Fprintln(a.stdout, text)
			if event.Kind == tui.EventSystemNote {
				lastSystemNote = text
			}
		}
	}
	if includeQRCode {
		if qrText := renderLoginQRCodeCLIOutput(result.LoginForm); qrText != "" {
			fmt.Fprintln(a.stdout, qrText)
		}
	}
	if footerHint := strings.TrimSpace(result.FooterHint); footerHint != "" && footerHint != lastSystemNote {
		fmt.Fprintln(a.stdout, footerHint)
	}
	return nil
}

func renderLoginQRCodeCLIOutput(state *tui.LoginFormState) string {
	if state == nil || !strings.EqualFold(strings.TrimSpace(state.Mode), "qr") {
		return ""
	}
	if state.QRCodeASCII == "" && strings.TrimSpace(state.QRScanHint) == "" {
		return ""
	}
	lines := []string{"扫码登录二维码:"}
	if qrCodeASCII := state.QRCodeASCII; qrCodeASCII != "" {
		lines = append(lines, qrCodeASCII)
	}
	if scanHint := strings.TrimSpace(state.QRScanHint); scanHint != "" {
		lines = append(lines, "扫码提示: "+scanHint)
	}
	return strings.Join(lines, "\n")
}

func shouldAutoWaitQRCodeCLI(args []string, state *tui.LoginFormState) bool {
	if state == nil || !strings.EqualFold(strings.TrimSpace(state.Mode), "qr") || strings.TrimSpace(state.QRTicket) == "" {
		return false
	}
	trimmedArgs := make([]string, 0, len(args))
	for _, arg := range args {
		if trimmed := strings.TrimSpace(arg); trimmed != "" {
			trimmedArgs = append(trimmedArgs, trimmed)
		}
	}
	if len(trimmedArgs) == 1 {
		return strings.EqualFold(trimmedArgs[0], "qr")
	}
	if len(trimmedArgs) == 2 {
		return strings.EqualFold(trimmedArgs[0], "qr") && strings.EqualFold(trimmedArgs[1], "start")
	}
	return false
}

func shouldRetryCLIQRCodeWait(result tui.LocalCommandResult) bool {
	if result.LoginForm == nil || !strings.EqualFold(strings.TrimSpace(result.LoginForm.Mode), "qr") || strings.TrimSpace(result.LoginForm.QRTicket) == "" || !result.LoginForm.QRWaiting {
		return false
	}
	for _, event := range result.Events {
		if event.Kind == tui.EventErrorNote {
			return false
		}
	}
	return true
}

func (a *App) completeCLIQRCodeLogin(
	ctx context.Context,
	providerClient *providerclient.Client,
	snapshot tui.Snapshot,
	refresh tuiSnapshotRefresh,
	state *tui.LoginFormState,
) error {
	if state == nil || strings.TrimSpace(state.QRTicket) == "" {
		return nil
	}
	fmt.Fprintln(a.stdout, "请扫码并在手机端确认登录，可按 Ctrl+C 取消。")
	for {
		localConfig, err := a.configManager.Load()
		if err != nil {
			return err
		}
		result, err := a.executeTUIQRWait(
			ctx,
			providerClient,
			snapshot,
			localConfig,
			providerclient.NewAuthClient(localConfig.BackendBaseURL),
			state.Remember,
			state.QRTicket,
			state.QRLoginURL,
			state.QRScanHint,
			state.QRWaitPollCount,
			refresh,
		)
		if err != nil {
			return err
		}
		if err := a.emitLoginCLIResultWithOptions(result, false); err != nil {
			return err
		}
		if result.LoginForm != nil {
			state = result.LoginForm
		}
		if !shouldRetryCLIQRCodeWait(result) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func renderLoginStatus(config internalconfig.Config, snapshot tui.Snapshot) string {
	status := "未登录"
	if strings.TrimSpace(snapshot.CurrentUserName) != "" {
		status = strings.TrimSpace(snapshot.CurrentUserName) + "(已登录)"
	} else if !snapshot.AuthRequired && strings.TrimSpace(config.AccessToken) != "" {
		status = "已保存访问令牌，但当前未拉取到用户信息"
	}
	remember := "关闭"
	if strings.TrimSpace(config.PersistentToken) != "" {
		remember = "开启"
	}
	deviceID := strings.TrimSpace(config.DeviceID)
	if deviceID == "" {
		deviceID = "<未生成>"
	}
	updatedAt := strings.TrimSpace(config.AuthUpdatedAt)
	if updatedAt == "" {
		updatedAt = "<未知>"
	}
	return strings.Join([]string{
		"登录状态: " + status,
		"记住登录: " + remember,
		"设备标识: " + deviceID,
		"最近认证时间: " + updatedAt,
	}, "\n")
}

func renderLoginFormStatus(snapshot tui.Snapshot) string {
	if currentUser := strings.TrimSpace(snapshot.CurrentUserName); currentUser != "" {
		return "当前账号: " + currentUser
	}
	if snapshot.AuthRequired {
		return "当前未登录"
	}
	return "请登录后继续使用"
}

func normalizeQRScanHint(hint string, channelNames []string) string {
	if channelLabel := joinQRChannelNames(channelNames); channelLabel != "" {
		return "请使用" + channelLabel + "扫码登录。"
	}
	trimmed := strings.TrimSpace(hint)
	trimmed = strings.TrimRight(trimmed, "。.!！")
	if trimmed == "" {
		return "请使用可用渠道扫码登录。"
	}
	if !strings.HasPrefix(trimmed, "请") {
		trimmed = "请" + trimmed
	}
	if !strings.Contains(trimmed, "扫码") {
		trimmed += "扫码"
	}
	if !strings.Contains(trimmed, "登录") {
		trimmed += "登录"
	}
	return trimmed + "。"
}

func joinQRChannelNames(channelNames []string) string {
	trimmedNames := make([]string, 0, len(channelNames))
	seen := make(map[string]struct{}, len(channelNames))
	for _, channelName := range channelNames {
		trimmedName := strings.TrimSpace(channelName)
		if trimmedName == "" {
			continue
		}
		normalizedName := strings.ToLower(trimmedName)
		if _, exists := seen[normalizedName]; exists {
			continue
		}
		seen[normalizedName] = struct{}{}
		trimmedNames = append(trimmedNames, trimmedName)
	}
	return strings.Join(trimmedNames, "或")
}

func buildTUILoginFormState(
	config internalconfig.Config,
	snapshot tui.Snapshot,
	mode string,
	capabilities loginChannelCapabilities,
	parsed passwordLoginCommand,
	qrToken *providerclient.RedirectLoginToken,
) *tui.LoginFormState {
	formMode := "password"
	if capabilities.SupportsQRCode && strings.EqualFold(strings.TrimSpace(mode), "qr") {
		formMode = "qr"
	}
	verifyType := normalizeLoginVerifyType(parsed.VerifyType)
	if verifyType == "" && strings.TrimSpace(parsed.Ticket) != "" {
		verifyType = loginVerifyTypeKaptcha
	}
	state := &tui.LoginFormState{
		Title:                  "登录",
		Hint:                   loginFormHint(parsed),
		StatusText:             renderLoginFormStatus(snapshot),
		SupportsQRCode:         capabilities.SupportsQRCode,
		Mode:                   formMode,
		Username:               strings.TrimSpace(parsed.Username),
		Password:               parsed.Password,
		PasswordPublicKey:      strings.TrimSpace(parsed.PasswordPublicKey),
		PasswordTicket:         strings.TrimSpace(parsed.PasswordTicket),
		Remember:               parsed.Remember,
		VerifyType:             verifyType,
		VerifyHint:             strings.TrimSpace(parsed.VerifyHint),
		VerifyTarget:           strings.TrimSpace(parsed.VerifyTarget),
		VerifyImageASCII:       parsed.VerifyImageASCII,
		VerifyImagePNGBase64:   parsed.VerifyImagePNGBase64,
		VerifyImageWidth:       parsed.VerifyImageWidth,
		VerifyImageHeight:      parsed.VerifyImageHeight,
		VerifyImageURL:         strings.TrimSpace(parsed.VerifyImageURL),
		VerifyImagePreviewPath: strings.TrimSpace(parsed.VerifyImagePreviewPath),
		Ticket:                 strings.TrimSpace(parsed.Ticket),
		Code:                   strings.TrimSpace(parsed.Code),
	}
	if qrToken != nil {
		state.QRTicket = strings.TrimSpace(qrToken.Ticket)
		state.QRLoginURL = strings.TrimSpace(qrToken.FireURL)
		state.QRScanHint = normalizeQRScanHint(qrToken.ScanHint, capabilities.ChannelNames)
		state.QRCodeASCII = renderQRCodeASCII(state.QRLoginURL)
	}
	return state
}

func renderQRCodeASCII(content string) string {
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return ""
	}
	code, err := qrcode.New(trimmedContent, qrcode.Medium)
	if err != nil {
		return ""
	}
	bitmap := code.Bitmap()
	if len(bitmap) == 0 {
		return ""
	}
	const quietZone = 1
	const darkBlock = "█"
	const upperHalfBlock = "▀"
	const lowerHalfBlock = "▄"
	const lightBlock = " "

	rowWidth := 0
	for _, row := range bitmap {
		if len(row) > rowWidth {
			rowWidth = len(row)
		}
	}
	if rowWidth == 0 {
		return ""
	}

	lines := make([]string, 0, (len(bitmap)+quietZone*2+1)/2)
	for rowIndex := -quietZone; rowIndex < len(bitmap)+quietZone; rowIndex += 2 {
		var lineBuilder strings.Builder
		for columnIndex := -quietZone; columnIndex < rowWidth+quietZone; columnIndex++ {
			topDark := qrBitmapModule(bitmap, rowIndex, columnIndex)
			bottomDark := qrBitmapModule(bitmap, rowIndex+1, columnIndex)
			switch {
			case topDark && bottomDark:
				lineBuilder.WriteString(darkBlock)
			case topDark:
				lineBuilder.WriteString(upperHalfBlock)
			case bottomDark:
				lineBuilder.WriteString(lowerHalfBlock)
			default:
				lineBuilder.WriteString(lightBlock)
			}
		}
		lines = append(lines, lineBuilder.String())
	}
	for len(lines) > 1 && strings.TrimSpace(lines[0]) == "" && strings.TrimSpace(lines[1]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 1 && strings.TrimSpace(lines[len(lines)-1]) == "" && strings.TrimSpace(lines[len(lines)-2]) == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

func qrBitmapModule(bitmap [][]bool, rowIndex int, columnIndex int) bool {
	if rowIndex < 0 || rowIndex >= len(bitmap) || columnIndex < 0 {
		return false
	}
	if columnIndex >= len(bitmap[rowIndex]) {
		return false
	}
	return bitmap[rowIndex][columnIndex]
}

func mergeLoginStatusText(base string, message string) string {
	trimmedBase := strings.TrimSpace(base)
	trimmedMessage := strings.TrimSpace(message)
	if trimmedMessage == "" {
		return trimmedBase
	}
	if trimmedBase == "" {
		return "登录失败: " + trimmedMessage
	}
	return trimmedBase + "\n\n登录失败: " + trimmedMessage
}

func withLoginFormFeedback(state *tui.LoginFormState, message string, focusField int) *tui.LoginFormState {
	if state == nil {
		return nil
	}
	state.StatusText = mergeLoginStatusText(state.StatusText, message)
	state.StatusIsError = true
	state.FocusField = focusField
	return state
}

func withLoginFormStatus(state *tui.LoginFormState, message string, isError bool) *tui.LoginFormState {
	if state == nil {
		return nil
	}
	trimmedMessage := strings.TrimSpace(message)
	if trimmedMessage == "" {
		return state
	}
	trimmedStatus := strings.TrimSpace(state.StatusText)
	if trimmedStatus == "" {
		state.StatusText = trimmedMessage
	} else {
		state.StatusText = trimmedStatus + "\n\n" + trimmedMessage
	}
	state.StatusIsError = isError
	return state
}

func withLoginFormNotice(state *tui.LoginFormState, message string, focusField int) *tui.LoginFormState {
	state = withLoginFormStatus(state, message, false)
	if state == nil {
		return nil
	}
	state.FocusField = focusField
	return state
}

func loginFeedbackMessage(err error) string {
	if err == nil {
		return ""
	}
	var authErr *providerclient.AuthError
	if errors.As(err, &authErr) {
		if message := strings.TrimSpace(authErr.Message); message != "" {
			return message
		}
	}
	return strings.TrimSpace(err.Error())
}

func buildPasswordLoginFormState(config internalconfig.Config, snapshot tui.Snapshot, capabilities loginChannelCapabilities, parsed passwordLoginCommand) *tui.LoginFormState {
	return buildTUILoginFormState(config, snapshot, "password", capabilities, parsed, nil)
}

func buildQRLoginFormState(config internalconfig.Config, snapshot tui.Snapshot, supportsQRCode bool, remember bool, ticket string, qrLoginURL string, qrScanHint string, waitPollCount int) *tui.LoginFormState {
	state := buildTUILoginFormState(config, snapshot, "qr", loginChannelCapabilities{SupportsQRCode: supportsQRCode}, passwordLoginCommand{Remember: remember}, &providerclient.RedirectLoginToken{
		Ticket:   strings.TrimSpace(ticket),
		FireURL:  strings.TrimSpace(qrLoginURL),
		ScanHint: strings.TrimSpace(qrScanHint),
	})
	if state != nil {
		state.QRWaitPollCount = waitPollCount
	}
	return state
}

func passwordLoginFocusField(parsed passwordLoginCommand) int {
	if normalizeLoginVerifyType(parsed.VerifyType) != "" || strings.TrimSpace(parsed.Ticket) != "" {
		return 3
	}
	if strings.TrimSpace(parsed.Username) == "" {
		return 1
	}
	return 2
}

func isVerificationRequiredError(err error) bool {
	var authErr *providerclient.AuthError
	if errors.As(err, &authErr) {
		if authErr.IsVerificationRequired() {
			return true
		}
	}
	return loginprompt.HasVerificationSignal(err.Error())
}

func shouldResetVerificationCode(err error) bool {
	var authErr *providerclient.AuthError
	return errors.As(err, &authErr) && authErr.ShouldResetVerificationCode()
}

func applyVerificationChallengeFromError(parsed *passwordLoginCommand, err error) {
	if parsed == nil || err == nil {
		return
	}
	var authErr *providerclient.AuthError
	if !errors.As(err, &authErr) || authErr.VerifyChallenge == nil {
		return
	}
	challenge := authErr.VerifyChallenge
	if verifyType := normalizeLoginVerifyType(challenge.Type); verifyType != "" {
		parsed.VerifyType = verifyType
	}
	if ticket := strings.TrimSpace(challenge.Ticket); ticket != "" {
		parsed.Ticket = ticket
	}
	if hint := strings.TrimSpace(challenge.Hint); hint != "" {
		parsed.VerifyHint = hint
	}
	if target := strings.TrimSpace(challenge.Target); target != "" {
		parsed.VerifyTarget = target
	}
}

func qrTokenFromSubmission(submission tui.LoginFormSubmission) *providerclient.RedirectLoginToken {
	if strings.TrimSpace(submission.QRTicket) == "" && strings.TrimSpace(submission.QRLoginURL) == "" && strings.TrimSpace(submission.QRScanHint) == "" {
		return nil
	}
	return &providerclient.RedirectLoginToken{
		Ticket:   strings.TrimSpace(submission.QRTicket),
		FireURL:  strings.TrimSpace(submission.QRLoginURL),
		ScanHint: strings.TrimSpace(submission.QRScanHint),
	}
}

func (a *App) executeTUILogout(
	ctx context.Context,
	providerClient *providerclient.Client,
	snapshot tui.Snapshot,
	localConfig internalconfig.Config,
	refresh tuiSnapshotRefresh,
) (tui.LocalCommandResult, error) {
	updatedConfig, changed := clearAuthenticationState(localConfig, false)
	if changed {
		if err := a.configManager.Save(updatedConfig); err != nil {
			return tui.LocalCommandResult{}, err
		}
	}
	providerClient.SetAPIKey("")
	return a.refreshLoginSnapshotResult(ctx, snapshot, refresh, "已退出登录。")
}

func (a *App) executeTUIPasswordLogin(
	ctx context.Context,
	providerClient *providerclient.Client,
	snapshot tui.Snapshot,
	localConfig internalconfig.Config,
	authClient *providerclient.AuthClient,
	capabilities loginChannelCapabilities,
	parsed passwordLoginCommand,
	refresh tuiSnapshotRefresh,
) (tui.LocalCommandResult, error) {
	if strings.TrimSpace(parsed.Username) == "" || strings.TrimSpace(parsed.Password) == "" {
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildPasswordLoginFormState(localConfig, snapshot, capabilities, parsed), "username 和 password 不能为空", passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "username 和 password 不能为空"}}}, nil
	}
	parsed.VerifyType = effectiveLoginVerifyType(parsed)
	if parsed.VerifyType != "" && strings.TrimSpace(parsed.Code) == "" {
		message := buildVerificationNotice(parsed)
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormNotice(buildPasswordLoginFormState(localConfig, snapshot, capabilities, parsed), message, passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventSystemNote, Text: message}}}, nil
	}
	loginToken, err := authClient.GetPasswordLoginToken(ctx)
	if err != nil {
		message := loginFeedbackMessage(err)
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildPasswordLoginFormState(localConfig, snapshot, capabilities, parsed), message, passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: message}}}, nil
	}
	loginTicket := strings.TrimSpace(parsed.Ticket)
	loginPasswordTicket := loginToken.EffectivePasswordTicket()
	loginPublicKey := strings.TrimSpace(loginToken.PubKey)
	parsed.PasswordTicket = loginPasswordTicket
	parsed.PasswordPublicKey = loginPublicKey
	session, err := authClient.PasswordLogin(ctx, providerclient.PasswordLoginRequest{
		VerifyTicket:   loginTicket,
		PasswordTicket: loginPasswordTicket,
		PublicKey:      loginPublicKey,
		Username:       parsed.Username,
		Password:       parsed.Password,
		Code:           parsed.Code,
	})
	if err != nil {
		if isVerificationRequiredError(err) {
			applyVerificationChallengeFromError(&parsed, err)
			parsed.VerifyType = normalizeLoginVerifyType(detectLoginVerifyType(loginFeedbackMessage(err), parsed.VerifyType))
			if parsed.VerifyType == "" {
				parsed.VerifyType = loginVerifyTypeKaptcha
			}
			if parsed.VerifyType == loginVerifyTypeKaptcha {
				resetCaptchaChallengeRenderState(&parsed)
				if challengeTicket := strings.TrimSpace(parsed.Ticket); challengeTicket != "" {
					loginTicket = challengeTicket
				}
			}
			if parsed.VerifyType == loginVerifyTypeKaptcha {
				populateCaptchaChallenge(ctx, authClient, &parsed, loginTicket, loginPasswordTicket, loginPublicKey)
			} else {
				populateVerifyChallenge(ctx, authClient, &parsed, loginPasswordTicket)
			}
			if shouldResetVerificationCode(err) {
				parsed.Code = ""
			}
			message := buildVerificationNotice(parsed)
			return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormNotice(buildPasswordLoginFormState(localConfig, snapshot, capabilities, parsed), message, passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventSystemNote, Text: message}}}, nil
		}
		message := loginFeedbackMessage(err)
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildPasswordLoginFormState(localConfig, snapshot, capabilities, parsed), message, passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: message}}}, nil
	}
	if _, err := a.persistAuthenticatedSession(ctx, providerClient, localConfig, session, parsed.Remember); err != nil {
		message := loginFeedbackMessage(err)
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildPasswordLoginFormState(localConfig, snapshot, capabilities, parsed), message, passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: message}}}, nil
	}
	return a.refreshLoginSnapshotResultAfterNewSession(ctx, snapshot, refresh, "")
}

func normalizeLoginVerifyType(value string) string {
	return loginprompt.NormalizeVerifyType(value)
}

func effectiveLoginVerifyType(parsed passwordLoginCommand) string {
	verifyType := normalizeLoginVerifyType(parsed.VerifyType)
	if verifyType == "" && strings.TrimSpace(parsed.Ticket) != "" {
		return loginVerifyTypeKaptcha
	}
	return verifyType
}

func detectLoginVerifyType(message string, fallback string) string {
	return loginprompt.DetectVerifyType(message, fallback)
}

func loginFormHint(parsed passwordLoginCommand) string {
	if parsed.VerifyHint != "" {
		return strings.TrimSpace(parsed.VerifyHint)
	}
	if effectiveLoginVerifyType(parsed) != "" {
		return buildVerificationNotice(parsed)
	}
	return ""
}

func buildVerificationNotice(parsed passwordLoginCommand) string {
	return loginprompt.Notice(effectiveLoginVerifyType(parsed), parsed.VerifyHint, parsed.VerifyTarget)
}

func resetCaptchaChallengeRenderState(parsed *passwordLoginCommand) {
	if parsed == nil {
		return
	}
	parsed.VerifyImageASCII = ""
	parsed.VerifyImagePNGBase64 = ""
	parsed.VerifyImageWidth = 0
	parsed.VerifyImageHeight = 0
	parsed.VerifyImageURL = ""
	parsed.VerifyImagePreviewPath = ""
}

func populateCaptchaChallenge(ctx context.Context, authClient *providerclient.AuthClient, parsed *passwordLoginCommand, loginTicket string, loginPasswordTicket string, loginPublicKey string) {
	if parsed == nil {
		return
	}
	parsed.VerifyType = loginVerifyTypeKaptcha
	if trimmed := strings.TrimSpace(loginTicket); trimmed != "" {
		parsed.Ticket = trimmed
	}
	if trimmed := strings.TrimSpace(loginPasswordTicket); trimmed != "" {
		parsed.PasswordTicket = trimmed
	}
	if trimmed := strings.TrimSpace(loginPublicKey); trimmed != "" {
		parsed.PasswordPublicKey = trimmed
	}
	if (strings.TrimSpace(parsed.VerifyImageASCII) == "" || strings.TrimSpace(parsed.VerifyImagePNGBase64) == "" || parsed.VerifyImageWidth == 0 || parsed.VerifyImageHeight == 0) && strings.TrimSpace(parsed.Ticket) != "" {
		captchaImage, err := authClient.GetVerificationImage(ctx, parsed.Ticket)
		if err == nil && captchaImage != nil {
			renderData := buildVerificationImageRenderData(captchaImage.Data)
			if parsed.VerifyImageASCII == "" {
				parsed.VerifyImageASCII = renderData.ASCII
			}
			if parsed.VerifyImagePNGBase64 == "" {
				parsed.VerifyImagePNGBase64 = renderData.PNGBase64
			}
			if parsed.VerifyImageWidth == 0 {
				parsed.VerifyImageWidth = renderData.Width
			}
			if parsed.VerifyImageHeight == 0 {
				parsed.VerifyImageHeight = renderData.Height
			}
			if strings.TrimSpace(parsed.VerifyImageURL) == "" {
				parsed.VerifyImageURL = strings.TrimSpace(captchaImage.URL)
			}
			if strings.TrimSpace(parsed.VerifyImagePreviewPath) == "" {
				parsed.VerifyImagePreviewPath = maybePrepareExternalCaptchaPreview(captchaImage.Data)
			}
		}
	}
}

func maybePrepareExternalCaptchaPreview(imageData []byte) string {
	if len(imageData) == 0 || !shouldUseExternalCaptchaPreview() {
		return ""
	}
	previewPath, err := writeCaptchaPreviewImage(imageData)
	if err != nil {
		return ""
	}
	_ = openCaptchaPreviewFile(previewPath)
	return previewPath
}

func shouldUseExternalCaptchaPreview() bool {
	if strings.ToLower(strings.TrimSpace(os.Getenv("TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL"))) == "off" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(os.Getenv("TERM_PROGRAM")), "vscode")
}

func writeCaptchaPreviewImage(imageData []byte) (string, error) {
	previewDir := filepath.Join(os.TempDir(), "taiyiflow-cli", "captcha-preview")
	if err := os.MkdirAll(previewDir, 0o755); err != nil {
		return "", err
	}
	previewFile, err := os.CreateTemp(previewDir, "captcha-*.png")
	if err != nil {
		return "", err
	}
	defer previewFile.Close()
	if _, err := previewFile.Write(imageData); err != nil {
		return "", err
	}
	return previewFile.Name(), nil
}

func defaultOpenCaptchaPreviewFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "linux":
		return exec.Command("xdg-open", path).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	default:
		return nil
	}
}

func populateVerifyChallenge(ctx context.Context, authClient *providerclient.AuthClient, parsed *passwordLoginCommand, loginPasswordTicket string) {
	if parsed == nil || effectiveLoginVerifyType(*parsed) == "" || effectiveLoginVerifyType(*parsed) == loginVerifyTypeKaptcha {
		return
	}
	if trimmed := strings.TrimSpace(loginPasswordTicket); trimmed != "" {
		parsed.PasswordTicket = trimmed
	}
	token, err := authClient.RequestVerifyLoginToken(ctx, parsed.VerifyType, "")
	if err != nil || token == nil {
		return
	}
	if verifyType := normalizeLoginVerifyType(token.Type); verifyType != "" {
		parsed.VerifyType = verifyType
	}
	if ticket := strings.TrimSpace(token.Ticket); ticket != "" {
		parsed.Ticket = ticket
	}
	if hint := strings.TrimSpace(token.Hint); hint != "" {
		parsed.VerifyHint = strings.TrimSpace(token.Hint)
	}
	if target := strings.TrimSpace(token.Target); target != "" {
		parsed.VerifyTarget = strings.TrimSpace(token.Target)
	}
}

func buildVerificationImageRenderData(imageData []byte) verificationImageRenderData {
	if len(imageData) == 0 {
		return verificationImageRenderData{}
	}
	decodedImage, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return verificationImageRenderData{}
	}

	bounds := decodedImage.Bounds()
	imageWidth := bounds.Dx()
	imageHeight := bounds.Dy()
	if imageWidth <= 0 || imageHeight <= 0 {
		return verificationImageRenderData{}
	}

	renderData := verificationImageRenderData{
		ASCII:  renderVerificationImageASCIIFromImage(decodedImage),
		Width:  imageWidth,
		Height: imageHeight,
	}
	var pngBuffer bytes.Buffer
	if err := png.Encode(&pngBuffer, decodedImage); err == nil {
		renderData.PNGBase64 = base64.StdEncoding.EncodeToString(pngBuffer.Bytes())
	}
	return renderData
}

func renderVerificationImageASCIIFromImage(decodedImage image.Image) string {
	bounds := decodedImage.Bounds()
	imageWidth := bounds.Dx()
	imageHeight := bounds.Dy()
	if imageWidth <= 0 || imageHeight <= 0 {
		return ""
	}
	targetWidth := imageWidth
	if targetWidth > 56 {
		targetWidth = 56
	}
	if targetWidth < 32 {
		targetWidth = 32
	}
	targetHeightPixels := int(float64(imageHeight) * float64(targetWidth) / float64(imageWidth))
	if targetHeightPixels < 10 {
		targetHeightPixels = 10
	}
	if targetHeightPixels > 28 {
		targetHeightPixels = 28
	}
	if targetHeightPixels%2 != 0 {
		targetHeightPixels++
	}
	minGray, maxGray := verificationImageGrayRange(decodedImage)
	threshold := (minGray + maxGray) / 2
	if maxGray-minGray < 24 {
		threshold = 128
	}
	lines := make([]string, 0, targetHeightPixels/2)
	for y := 0; y < targetHeightPixels; y += 2 {
		var line strings.Builder
		for x := 0; x < targetWidth; x++ {
			topDark := verificationPixelDark(decodedImage, bounds, x, y, targetWidth, targetHeightPixels, threshold)
			bottomDark := verificationPixelDark(decodedImage, bounds, x, y+1, targetWidth, targetHeightPixels, threshold)
			switch {
			case topDark && bottomDark:
				line.WriteRune('█')
			case topDark:
				line.WriteRune('▀')
			case bottomDark:
				line.WriteRune('▄')
			default:
				line.WriteRune(' ')
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(trimVerificationImageBlankLines(lines), "\n")
}

func verificationImageGrayRange(decodedImage image.Image) (uint32, uint32) {
	bounds := decodedImage.Bounds()
	minGray := uint32(255)
	maxGray := uint32(0)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			gray := verificationPixelGray(decodedImage.At(x, y))
			if gray < minGray {
				minGray = gray
			}
			if gray > maxGray {
				maxGray = gray
			}
		}
	}
	return minGray, maxGray
}

func verificationPixelDark(decodedImage image.Image, bounds image.Rectangle, x int, y int, targetWidth int, targetHeight int, threshold uint32) bool {
	if targetWidth <= 0 || targetHeight <= 0 {
		return false
	}
	sourceX := bounds.Min.X + x*bounds.Dx()/targetWidth
	sourceY := bounds.Min.Y + y*bounds.Dy()/targetHeight
	return verificationPixelGray(decodedImage.At(sourceX, sourceY)) <= threshold
}

func verificationPixelGray(pixel color.Color) uint32 {
	red, green, blue, _ := pixel.RGBA()
	return ((299 * (red >> 8)) + (587 * (green >> 8)) + (114 * (blue >> 8))) / 1000
}

func trimVerificationImageBlankLines(lines []string) []string {
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if start >= end {
		return nil
	}
	return lines[start:end]
}

func (a *App) executeTUIQRWait(
	ctx context.Context,
	providerClient *providerclient.Client,
	snapshot tui.Snapshot,
	localConfig internalconfig.Config,
	authClient *providerclient.AuthClient,
	remember bool,
	ticket string,
	qrLoginURL string,
	qrScanHint string,
	waitPollCount int,
	refresh tuiSnapshotRefresh,
) (tui.LocalCommandResult, error) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildQRLoginFormState(localConfig, snapshot, true, remember, "", qrLoginURL, qrScanHint, waitPollCount), "ticket 不能为空", 0), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "ticket 不能为空"}}}, nil
	}
	waitEvent, err := authClient.WaitQRCodeLoginEvent(ctx, ticket)
	if err != nil {
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildQRLoginFormState(localConfig, snapshot, true, remember, ticket, qrLoginURL, qrScanHint, waitPollCount), err.Error(), 0), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
	}
	if strings.TrimSpace(waitEvent.AccessToken) == "" {
		if waitEvent.State < 0 {
			message := fmt.Sprintf("扫码登录已结束，状态=%d。请重新生成二维码后再试。", waitEvent.State)
			loginForm := withLoginFormStatus(buildQRLoginFormState(localConfig, snapshot, true, remember, ticket, qrLoginURL, qrScanHint, waitPollCount), message, true)
			if loginForm != nil {
				loginForm.QRWaiting = false
			}
			return tui.LocalCommandResult{Handled: true, LoginForm: loginForm, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: message}}}, nil
		}
		nextWaitPollCount := waitPollCount + 1
		loginForm := buildQRLoginFormState(localConfig, snapshot, true, remember, ticket, qrLoginURL, qrScanHint, nextWaitPollCount)
		events := []tui.Event{}
		if loginForm != nil {
			loginForm.QRWaiting = true
		}
		waitTimeoutSeconds := qrWaitTimeoutSeconds(localConfig)
		if waitTimeoutSeconds > 0 && nextWaitPollCount >= waitTimeoutSeconds {
			timeoutText := fmt.Sprintf("扫码等待已超过%d秒。请检查手机端是否已完成确认；如二维码失效请重新生成后再试。", waitTimeoutSeconds)
			loginForm = withLoginFormStatus(loginForm, timeoutText, false)
			if loginForm != nil {
				loginForm.QRWaiting = false
			}
			events = append(events, tui.Event{Kind: tui.EventSystemNote, Text: timeoutText})
		}
		return tui.LocalCommandResult{Handled: true, LoginForm: loginForm, Events: events}, nil
	}
	session := &providerclient.AuthSession{
		UserID:       strings.TrimSpace(waitEvent.UserID),
		AccessToken:  strings.TrimSpace(waitEvent.AccessToken),
		IdleDuration: waitEvent.IdleDuration,
		Scope:        strings.TrimSpace(waitEvent.Scope),
	}
	if _, err := a.persistAuthenticatedSession(ctx, providerClient, localConfig, session, remember); err != nil {
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildQRLoginFormState(localConfig, snapshot, true, remember, ticket, qrLoginURL, qrScanHint, waitPollCount), err.Error(), 0), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
	}
	return a.refreshLoginSnapshotResultAfterNewSession(ctx, snapshot, refresh, "")
}

func parsePasswordLoginCommand(command string) (passwordLoginCommand, error) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 4 {
		return passwordLoginCommand{}, fmt.Errorf("用法: /login password <username> <password> [remember=on|off] [ticket=<ticket>] [code=<code>]")
	}
	parsed := passwordLoginCommand{
		Username: strings.TrimSpace(fields[2]),
		Password: strings.TrimSpace(fields[3]),
		Remember: true,
	}
	for _, field := range fields[4:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return passwordLoginCommand{}, fmt.Errorf("登录选项格式错误: %s", field)
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "remember":
			remember, err := parseLoginRememberFlag(value)
			if err != nil {
				return passwordLoginCommand{}, err
			}
			parsed.Remember = remember
		case "verify_type":
			parsed.VerifyType = normalizeLoginVerifyType(value)
		case "verify_hint":
			parsed.VerifyHint = strings.TrimSpace(value)
		case "verify_target":
			parsed.VerifyTarget = strings.TrimSpace(value)
		case "password_ticket":
			parsed.PasswordTicket = strings.TrimSpace(value)
		case "ticket":
			parsed.Ticket = strings.TrimSpace(value)
			if parsed.VerifyType == "" {
				parsed.VerifyType = loginVerifyTypeKaptcha
			}
		case "code":
			parsed.Code = strings.TrimSpace(value)
		default:
			return passwordLoginCommand{}, fmt.Errorf("不支持的登录选项: %s", key)
		}
	}
	if parsed.Username == "" || parsed.Password == "" {
		return passwordLoginCommand{}, fmt.Errorf("username 和 password 不能为空")
	}
	return parsed, nil
}

func parseLoginRememberFlag(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "on", "true", "yes", "1":
		return true, nil
	case "off", "false", "no", "0":
		return false, nil
	default:
		return false, fmt.Errorf("remember 仅支持 on/off")
	}
}
