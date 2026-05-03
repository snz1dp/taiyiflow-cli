package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

type tuiSnapshotRefresh func(context.Context, tui.Snapshot) (tui.Snapshot, []tui.FlowItem, error)

type passwordLoginCommand struct {
	Username      string
	Password      string
	Remember      bool
	KaptchaTicket string
	KaptchaCode   string
}

type qrWaitCommand struct {
	Ticket   string
	Remember bool
}

const (
	loginFormActionPassword  = "password_login"
	loginFormActionStartQR   = "start_qr"
	loginFormActionRefreshQR = "refresh_qr"
	loginFormActionWaitQR    = "wait_qr"
	loginFormActionLogout    = "logout"
)

func (a *App) runLogin(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.runTUI(ctx, []string{"/login"})
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	providerClient := buildProviderClient(config)
	snapshot := buildLoginCLISnapshot(ctx, providerClient, config)
	refresh := func(refreshCtx context.Context, _ tui.Snapshot) (tui.Snapshot, []tui.FlowItem, error) {
		updatedConfig, err := a.configManager.Load()
		if err != nil {
			return tui.Snapshot{}, nil, err
		}
		providerClient.SetAPIKey(updatedConfig.AccessToken)
		return buildLoginCLISnapshot(refreshCtx, providerClient, updatedConfig), nil, nil
	}

	result, err := a.executeTUILoginCommand(ctx, providerClient, snapshot, buildLoginCLICommand(args), refresh)
	if err != nil {
		return err
	}
	return a.emitLoginCLIResult(result)
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

	switch {
	case strings.EqualFold(trimmed, "/login"):
		return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, "password", true, "", "", "", "", nil)}, nil
	case strings.EqualFold(trimmed, "/login help"):
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: loginSlashUsage()}}}, nil
	case strings.EqualFold(trimmed, "/login status"):
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: renderLoginStatus(localConfig, snapshot)}}}, nil
	case strings.EqualFold(trimmed, "/login logout"):
		return a.executeTUILogout(ctx, providerClient, snapshot, localConfig, refresh)
	case strings.HasPrefix(strings.ToLower(trimmed), "/login password "):
		parsed, err := parsePasswordLoginCommand(trimmed)
		if err != nil {
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
		}
		return a.executeTUIPasswordLogin(ctx, providerClient, snapshot, localConfig, authClient, parsed, refresh)
	case strings.EqualFold(trimmed, "/login qr") || strings.EqualFold(trimmed, "/login qr start"):
		qrToken, err := authClient.GetQRCodeLoginToken(ctx, "", false, "login", "")
		if err != nil {
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
		}
		return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, "qr", true, "", "", "", "", qrToken)}, nil
	case strings.HasPrefix(strings.ToLower(trimmed), "/login qr wait "):
		parsed, err := parseQRWaitCommand(trimmed)
		if err != nil {
			return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
		}
		return a.executeTUIQRWait(ctx, providerClient, snapshot, localConfig, authClient, parsed.Remember, parsed.Ticket, "", "", refresh)
	default:
		return tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventErrorNote, Text: loginSlashUsage()}}}, nil
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
	remember := submission.Remember
	if strings.TrimSpace(submission.Mode) == "" {
		submission.Mode = "password"
	}
	switch strings.TrimSpace(submission.Action) {
	case loginFormActionLogout:
		return a.executeTUILogout(ctx, providerClient, snapshot, localConfig, refresh)
	case loginFormActionStartQR, loginFormActionRefreshQR:
		qrToken, err := authClient.GetQRCodeLoginToken(ctx, "", false, "login", "")
		if err != nil {
			return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, "qr", remember, submission.Username, submission.Password, submission.KaptchaTicket, submission.KaptchaCode, qrTokenFromSubmission(submission)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
		}
		return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, "qr", remember, submission.Username, submission.Password, submission.KaptchaTicket, submission.KaptchaCode, qrToken)}, nil
	case loginFormActionWaitQR:
		return a.executeTUIQRWait(ctx, providerClient, snapshot, localConfig, authClient, remember, submission.QRTicket, submission.QRLoginURL, submission.QRScanHint, refresh)
	case "", loginFormActionPassword:
		parsed := passwordLoginCommand{
			Username:      strings.TrimSpace(submission.Username),
			Password:      submission.Password,
			Remember:      remember,
			KaptchaTicket: strings.TrimSpace(submission.KaptchaTicket),
			KaptchaCode:   strings.TrimSpace(submission.KaptchaCode),
		}
		return a.executeTUIPasswordLogin(ctx, providerClient, snapshot, localConfig, authClient, parsed, refresh)
	default:
		return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, submission.Mode, remember, submission.Username, submission.Password, submission.KaptchaTicket, submission.KaptchaCode, qrTokenFromSubmission(submission)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "不支持的登录动作。"}}}, nil
	}
}

func (a *App) refreshLoginSnapshotResult(
	ctx context.Context,
	snapshot tui.Snapshot,
	refresh tuiSnapshotRefresh,
	message string,
) (tui.LocalCommandResult, error) {
	result := tui.LocalCommandResult{Handled: true, Events: []tui.Event{{Kind: tui.EventSystemNote, Text: message}}, FooterHint: strings.TrimSpace(message)}
	if refresh == nil {
		return result, nil
	}
	updatedSnapshot, updatedFlows, err := refresh(ctx, snapshot)
	if err != nil {
		return tui.LocalCommandResult{}, err
	}
	result.Snapshot = &updatedSnapshot
	result.Flows = updatedFlows
	if currentUser := strings.TrimSpace(updatedSnapshot.CurrentUserName); currentUser != "" {
		result.FooterHint = currentUser + "(已登录)"
	}
	return result, nil
}

func loginSlashUsage() string {
	return strings.Join([]string{
		"登录命令:",
		"/login 打开登录表单",
		"/login status 查看当前登录状态",
		"/login logout 清除本地登录态",
		"兼容命令(自动化或脚本场景):",
		"/login qr 生成扫码登录地址，并打开扫码表单",
		"/login qr wait <ticket> [remember=on|off] 等待扫码登录完成",
		"/login password <username> <password> [remember=on|off] [captcha_ticket=<ticket>] [captcha_code=<code>]",
		"提示: 推荐直接使用 /login；密码登录与扫码登录都已收口到表单中。",
	}, "\n")
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

func buildLoginCLISnapshot(ctx context.Context, providerClient *providerclient.Client, config internalconfig.Config) tui.Snapshot {
	snapshot := tui.Snapshot{
		BackendURL:       strings.TrimSpace(config.BackendBaseURL),
		BackendAvailable: strings.TrimSpace(config.BackendBaseURL) != "",
	}
	if !snapshot.BackendAvailable || strings.TrimSpace(config.AccessToken) == "" {
		return snapshot
	}
	currentUser, err := providerClient.GetCurrentUserInfo(ctx)
	if err == nil {
		snapshot.CurrentUserName = strings.TrimSpace(currentUser.Name)
		return snapshot
	}
	if isAuthenticationError(err) {
		snapshot.AuthRequired = true
		snapshot.AuthStatusReason = "服务端已连接，但当前登录态无效或缺失。"
		return snapshot
	}
	snapshot.BackendAvailable = false
	snapshot.BackendStatusReason = err.Error()
	return snapshot
}

func (a *App) emitLoginCLIResult(result tui.LocalCommandResult) error {
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
	if qrText := renderLoginQRCodeCLIOutput(result.LoginForm); qrText != "" {
		fmt.Fprintln(a.stdout, qrText)
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
	if strings.TrimSpace(state.QRTicket) == "" && strings.TrimSpace(state.QRLoginURL) == "" && strings.TrimSpace(state.QRScanHint) == "" {
		return ""
	}
	lines := []string{"扫码登录信息:"}
	if ticket := strings.TrimSpace(state.QRTicket); ticket != "" {
		lines = append(lines, "ticket: "+ticket)
	}
	if loginURL := strings.TrimSpace(state.QRLoginURL); loginURL != "" {
		lines = append(lines, "login_url: "+loginURL)
	}
	if scanHint := strings.TrimSpace(state.QRScanHint); scanHint != "" {
		lines = append(lines, "scan_hint: "+scanHint)
	}
	if ticket := strings.TrimSpace(state.QRTicket); ticket != "" {
		lines = append(lines, "下一步: taiyiflow-cli login qr wait "+ticket)
	}
	return strings.Join(lines, "\n")
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

func buildTUILoginFormState(
	config internalconfig.Config,
	snapshot tui.Snapshot,
	mode string,
	remember bool,
	username string,
	password string,
	kaptchaTicket string,
	kaptchaCode string,
	qrToken *providerclient.RedirectLoginToken,
) *tui.LoginFormState {
	formMode := "password"
	if strings.EqualFold(strings.TrimSpace(mode), "qr") {
		formMode = "qr"
	}
	state := &tui.LoginFormState{
		Title:         "登录(login)",
		Hint:          loginFormHint(kaptchaTicket),
		StatusText:    renderLoginStatus(config, snapshot),
		Mode:          formMode,
		Username:      strings.TrimSpace(username),
		Password:      password,
		Remember:      remember,
		KaptchaTicket: strings.TrimSpace(kaptchaTicket),
		KaptchaCode:   strings.TrimSpace(kaptchaCode),
	}
	if qrToken != nil {
		state.QRTicket = strings.TrimSpace(qrToken.Ticket)
		state.QRLoginURL = strings.TrimSpace(qrToken.FireURL)
		state.QRScanHint = strings.TrimSpace(qrToken.ScanHint)
	}
	return state
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

func loginFormHint(kaptchaTicket string) string {
	if strings.TrimSpace(kaptchaTicket) != "" {
		return "检测到需要验证码，请输入验证码后继续登录。"
	}
	return "密码登录与扫码登录统一使用同一服务地址。"
}

func buildPasswordLoginFormState(config internalconfig.Config, snapshot tui.Snapshot, parsed passwordLoginCommand) *tui.LoginFormState {
	return buildTUILoginFormState(config, snapshot, "password", parsed.Remember, parsed.Username, parsed.Password, parsed.KaptchaTicket, parsed.KaptchaCode, nil)
}

func passwordLoginFocusField(parsed passwordLoginCommand) int {
	if strings.TrimSpace(parsed.KaptchaTicket) != "" {
		return 3
	}
	if strings.TrimSpace(parsed.Username) == "" {
		return 1
	}
	return 2
}

func isCaptchaRequiredError(err error) bool {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(message, "验证码") || strings.Contains(message, "captcha") || strings.Contains(message, "kaptcha")
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
	parsed passwordLoginCommand,
	refresh tuiSnapshotRefresh,
) (tui.LocalCommandResult, error) {
	if strings.TrimSpace(parsed.Username) == "" || strings.TrimSpace(parsed.Password) == "" {
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildPasswordLoginFormState(localConfig, snapshot, parsed), "username 和 password 不能为空", passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "username 和 password 不能为空"}}}, nil
	}
	if strings.TrimSpace(parsed.KaptchaTicket) != "" && strings.TrimSpace(parsed.KaptchaCode) == "" {
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildPasswordLoginFormState(localConfig, snapshot, parsed), "请输入验证码后继续登录。", passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "请输入验证码后继续登录。"}}}, nil
	}
	loginToken, err := authClient.GetPasswordLoginToken(ctx)
	if err != nil {
		message := loginFeedbackMessage(err)
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildPasswordLoginFormState(localConfig, snapshot, parsed), message, passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: message}}}, nil
	}
	session, err := authClient.PasswordLogin(ctx, providerclient.PasswordLoginRequest{
		Ticket:        strings.TrimSpace(loginToken.Ticket),
		PublicKey:     strings.TrimSpace(loginToken.PubKey),
		Username:      parsed.Username,
		Password:      parsed.Password,
		KaptchaTicket: parsed.KaptchaTicket,
		KaptchaCode:   parsed.KaptchaCode,
	})
	if err != nil {
		if isCaptchaRequiredError(err) {
			if strings.TrimSpace(parsed.KaptchaTicket) == "" {
				captchaToken, captchaErr := authClient.GetKaptchaTicket(ctx)
				if captchaErr == nil && captchaToken != nil {
					parsed.KaptchaTicket = strings.TrimSpace(captchaToken.Ticket)
				}
			}
			if strings.TrimSpace(parsed.KaptchaTicket) != "" {
				parsed.KaptchaCode = ""
				return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildPasswordLoginFormState(localConfig, snapshot, parsed), "需要验证码，请输入验证码后继续登录。", passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "需要验证码，请输入验证码后继续登录。"}}}, nil
			}
		}
		message := loginFeedbackMessage(err)
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildPasswordLoginFormState(localConfig, snapshot, parsed), message, passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: message}}}, nil
	}
	if _, err := a.persistAuthenticatedSession(ctx, providerClient, localConfig, session, parsed.Remember); err != nil {
		message := loginFeedbackMessage(err)
		return tui.LocalCommandResult{Handled: true, LoginForm: withLoginFormFeedback(buildPasswordLoginFormState(localConfig, snapshot, parsed), message, passwordLoginFocusField(parsed)), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: message}}}, nil
	}
	message := "已完成用户名密码登录。"
	if parsed.Remember {
		message += " 已默认记住登录。"
	}
	return a.refreshLoginSnapshotResult(ctx, snapshot, refresh, message)
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
	refresh tuiSnapshotRefresh,
) (tui.LocalCommandResult, error) {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, "qr", remember, "", "", "", "", &providerclient.RedirectLoginToken{FireURL: qrLoginURL, ScanHint: qrScanHint}), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: "ticket 不能为空"}}}, nil
	}
	waitEvent, err := authClient.WaitQRCodeLoginEvent(ctx, ticket)
	if err != nil {
		return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, "qr", remember, "", "", "", "", &providerclient.RedirectLoginToken{Ticket: ticket, FireURL: qrLoginURL, ScanHint: qrScanHint}), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
	}
	if strings.TrimSpace(waitEvent.AccessToken) == "" {
		text := fmt.Sprintf("扫码结果尚未返回 access_token，当前 state=%d。请完成扫码后继续等待。", waitEvent.State)
		return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, "qr", remember, "", "", "", "", &providerclient.RedirectLoginToken{Ticket: ticket, FireURL: qrLoginURL, ScanHint: qrScanHint}), Events: []tui.Event{{Kind: tui.EventSystemNote, Text: text}}}, nil
	}
	session := &providerclient.AuthSession{
		UserID:       strings.TrimSpace(waitEvent.UserID),
		AccessToken:  strings.TrimSpace(waitEvent.AccessToken),
		IdleDuration: waitEvent.IdleDuration,
		Scope:        strings.TrimSpace(waitEvent.Scope),
	}
	if _, err := a.persistAuthenticatedSession(ctx, providerClient, localConfig, session, remember); err != nil {
		return tui.LocalCommandResult{Handled: true, LoginForm: buildTUILoginFormState(localConfig, snapshot, "qr", remember, "", "", "", "", &providerclient.RedirectLoginToken{Ticket: ticket, FireURL: qrLoginURL, ScanHint: qrScanHint}), Events: []tui.Event{{Kind: tui.EventErrorNote, Text: err.Error()}}}, nil
	}
	message := "扫码登录成功。"
	if remember {
		message += " 已默认记住登录。"
	}
	return a.refreshLoginSnapshotResult(ctx, snapshot, refresh, message)
}

func parsePasswordLoginCommand(command string) (passwordLoginCommand, error) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 4 {
		return passwordLoginCommand{}, fmt.Errorf("用法: /login password <username> <password> [remember=on|off] [captcha_ticket=<ticket>] [captcha_code=<code>]")
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
		case "captcha_ticket":
			parsed.KaptchaTicket = strings.TrimSpace(value)
		case "captcha_code":
			parsed.KaptchaCode = strings.TrimSpace(value)
		default:
			return passwordLoginCommand{}, fmt.Errorf("不支持的登录选项: %s", key)
		}
	}
	if parsed.Username == "" || parsed.Password == "" {
		return passwordLoginCommand{}, fmt.Errorf("username 和 password 不能为空")
	}
	return parsed, nil
}

func parseQRWaitCommand(command string) (qrWaitCommand, error) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) < 4 {
		return qrWaitCommand{}, fmt.Errorf("用法: /login qr wait <ticket> [remember=on|off]")
	}
	parsed := qrWaitCommand{Ticket: strings.TrimSpace(fields[3]), Remember: true}
	for _, field := range fields[4:] {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return qrWaitCommand{}, fmt.Errorf("登录选项格式错误: %s", field)
		}
		if strings.ToLower(strings.TrimSpace(key)) != "remember" {
			return qrWaitCommand{}, fmt.Errorf("不支持的扫码登录选项: %s", key)
		}
		remember, err := parseLoginRememberFlag(value)
		if err != nil {
			return qrWaitCommand{}, err
		}
		parsed.Remember = remember
	}
	if parsed.Ticket == "" {
		return qrWaitCommand{}, fmt.Errorf("ticket 不能为空")
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
