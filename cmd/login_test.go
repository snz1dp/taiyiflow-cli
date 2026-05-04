package cmd

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

func TestSubmitTUILoginFormPasswordUpdatesAuthState(t *testing.T) {
	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER}))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/password_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"ticket-1","pubkey":%q}}`, publicKeyPEM)
		case "/xeai/ajax_logon":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","idle_duration":3600,"scope":"employee"}}`)
		case "/xeai/logged/persistent_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"persist-1","userinfo":{"userid":"u-1","display_name":"张三"}}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	providerClient := buildProviderClient(config)
	snapshot := tui.Snapshot{BackendAvailable: true}
	refreshCalled := false
	result, err := app.submitTUILoginForm(context.Background(), providerClient, snapshot, tui.LoginFormSubmission{
		Mode:     "password",
		Username: "zhangsan",
		Password: "secret-123",
		Remember: true,
	}, func(_ context.Context, current tui.Snapshot) (tui.Snapshot, []tui.FlowItem, error) {
		refreshCalled = true
		current.CurrentUserName = "张三"
		current.AuthRequired = false
		return current, nil, nil
	})
	if err != nil {
		t.Fatalf("submitTUILoginForm failed: %v", err)
	}
	if !refreshCalled {
		t.Fatal("expected refresh callback to run after login")
	}
	if result.Snapshot == nil || result.Snapshot.CurrentUserName != "张三" {
		t.Fatalf("expected refreshed snapshot with current user, got %#v", result.Snapshot)
	}
	if result.FooterHint != "已完成用户名密码登录。 已默认记住登录。 张三(已登录)" {
		t.Fatalf("expected footer hint to preserve success message and current user, got %#v", result)
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-1" {
		t.Fatalf("expected persisted auth state, got %#v", reloaded)
	}
}

func TestExecuteTUILoginCommandOpensOverlay(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	result, err := app.executeTUILoginCommand(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true}, "/login", nil)
	if err != nil {
		t.Fatalf("executeTUILoginCommand failed: %v", err)
	}
	if result.LoginForm == nil {
		t.Fatalf("expected /login to open login overlay, got %#v", result)
	}
	if result.LoginForm.Mode != "password" {
		t.Fatalf("expected password mode by default, got %#v", result.LoginForm)
	}
	if !result.LoginForm.Remember {
		t.Fatalf("expected remember login enabled by default, got %#v", result.LoginForm)
	}
	if result.LoginForm.Title != "登录" {
		t.Fatalf("unexpected login overlay title: %#v", result.LoginForm)
	}
	if result.LoginForm.StatusText != "请登录后继续使用" {
		t.Fatalf("expected login overlay to use simplified status text, got %#v", result.LoginForm)
	}
	if strings.Contains(result.LoginForm.StatusText, "设备标识") || strings.Contains(result.LoginForm.StatusText, "最近认证时间") {
		t.Fatalf("expected login overlay to hide detailed auth status, got %#v", result.LoginForm)
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if strings.TrimSpace(reloaded.DeviceID) == "" {
		t.Fatalf("expected /login to persist generated device id, got %#v", reloaded)
	}
}

func TestSubmitTUILoginFormPasswordRequiresCaptcha(t *testing.T) {
	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER}))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/password_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"ticket-1","pubkey":%q}}`, publicKeyPEM)
		case "/xeai/ajax_logon":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":4001,"message":"需要验证码"}`)
		case "/xeai/kaptcha/ticket":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"captcha-ticket-1","expired":120}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	result, err := app.submitTUILoginForm(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true, AuthRequired: true}, tui.LoginFormSubmission{
		Mode:     "password",
		Username: "zhangsan",
		Password: "secret-123",
		Remember: true,
	}, nil)
	if err != nil {
		t.Fatalf("submitTUILoginForm failed: %v", err)
	}
	if result.LoginForm == nil {
		t.Fatalf("expected captcha flow to reopen login form, got %#v", result)
	}
	if result.LoginForm.KaptchaTicket != "captcha-ticket-1" {
		t.Fatalf("expected captcha ticket to be fetched, got %#v", result.LoginForm)
	}
	if result.LoginForm.Password != "secret-123" || result.LoginForm.Username != "zhangsan" {
		t.Fatalf("expected password flow to preserve username/password, got %#v", result.LoginForm)
	}
	if len(result.Events) != 1 || !strings.Contains(result.Events[0].Text, "需要验证码") {
		t.Fatalf("expected captcha-required hint event, got %#v", result.Events)
	}
	if !strings.Contains(result.LoginForm.StatusText, "需要验证码") {
		t.Fatalf("expected captcha-required hint in login overlay status, got %#v", result.LoginForm)
	}
	if result.LoginForm.FocusField != 3 {
		t.Fatalf("expected captcha flow to focus captcha field, got %#v", result.LoginForm)
	}
}

func TestSubmitTUILoginFormPasswordShowsInlineErrorAndKeepsPasswordFocus(t *testing.T) {
	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER}))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/password_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"ticket-1","pubkey":%q}}`, publicKeyPEM)
		case "/xeai/ajax_logon":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":4001,"message":"用户名或密码错误"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	result, err := app.submitTUILoginForm(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true, AuthRequired: true}, tui.LoginFormSubmission{
		Mode:     "password",
		Username: "zhangsan",
		Password: "secret-123",
		Remember: true,
	}, nil)
	if err != nil {
		t.Fatalf("submitTUILoginForm failed: %v", err)
	}
	if result.LoginForm == nil {
		t.Fatalf("expected failed password login to keep login form open, got %#v", result)
	}
	if !strings.Contains(result.LoginForm.StatusText, "用户名或密码错误") {
		t.Fatalf("expected inline password error in login overlay status, got %#v", result.LoginForm)
	}
	if strings.Contains(result.LoginForm.StatusText, "/ajax_logon") || strings.Contains(result.LoginForm.StatusText, "HTTP 200") || strings.Contains(result.LoginForm.StatusText, "code=401") {
		t.Fatalf("expected inline password error to hide transport details, got %#v", result.LoginForm)
	}
	if len(result.Events) != 1 || result.Events[0].Text != "用户名或密码错误" {
		t.Fatalf("expected user-facing event text only, got %#v", result.Events)
	}
	if result.LoginForm.FocusField != 2 {
		t.Fatalf("expected failed password login to keep focus on password field, got %#v", result.LoginForm)
	}
	if !result.LoginForm.StatusIsError {
		t.Fatalf("expected failed password login to mark status as error, got %#v", result.LoginForm)
	}
}

func TestLoginSlashUsagePrioritizesOverlayEntry(t *testing.T) {
	usage := loginSlashUsage()
	if !strings.Contains(usage, "/login 打开登录表单") {
		t.Fatalf("expected usage to prioritize /login overlay entry, got %q", usage)
	}
	if strings.Contains(usage, "兼容命令(自动化或脚本场景):") {
		t.Fatalf("expected usage to drop compatibility command section, got %q", usage)
	}
	if !strings.Contains(usage, "/login qr") {
		t.Fatalf("expected usage to retain qr entry, got %q", usage)
	}
	if strings.Contains(usage, "\n/login password ") || strings.Contains(usage, "\n/login qr wait ") {
		t.Fatalf("expected usage to drop removed commands, got %q", usage)
	}
	if !strings.Contains(usage, "旧的 /login password 与 /login qr wait 已移除") {
		t.Fatalf("expected usage to explain removed commands, got %q", usage)
	}
	if strings.Contains(usage, "打开 overlay 登录表单") {
		t.Fatalf("expected usage to stop using old overlay wording, got %q", usage)
	}
}

func TestRunLoginStatusSubcommandUsesRootDispatch(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.AccessToken = "access-1"
	config.PersistentToken = "persist-1"
	config.DeviceID = "device-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{configManager: manager, stdout: &stdout, stderr: &stderr}
	if err := app.Run(context.Background(), []string{"login", "status"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !strings.Contains(stdout.String(), "登录状态:") {
		t.Fatalf("expected login status output, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "记住登录: 开启") {
		t.Fatalf("expected remember-login status output, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunLoginPasswordSubcommandShowsRemovedCommandHint(t *testing.T) {
	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER}))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/password_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"ticket-1","pubkey":%q}}`, publicKeyPEM)
		case "/xeai/ajax_logon":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","idle_duration":3600,"scope":"employee"}}`)
		case "/xeai/logged/persistent_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"persist-1","userinfo":{"userid":"u-1","display_name":"张三"}}}`)
		case "/taiyi/composer/api/v1/users/whoami/userinfo":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan","user_scope":"employee"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{configManager: manager, stdout: &stdout, stderr: &stderr}
	if err := app.Run(context.Background(), []string{"login", "password", "zhangsan", "secret-123"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.AccessToken != "" || reloaded.PersistentToken != "" {
		t.Fatalf("expected removed password subcommand to avoid persisting auth state, got %#v", reloaded)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected removed password subcommand to avoid stdout output, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "旧登录命令已移除，请直接使用 /login。") {
		t.Fatalf("expected removed password subcommand hint in stderr, got %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "已完成用户名密码登录") {
		t.Fatalf("expected removed password subcommand to stop success output, got %q", stderr.String())
	}
}

func TestRunLoginQRSubcommandWaitsForScanAndPersistsAuthState(t *testing.T) {
	waitCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"qr-ticket-1","fireurl":"https://example.com/scan","scanhint":"请使用企业微信扫码"}}`)
		case "/xeai/oauth/api/wait_event":
			writer.Header().Set("Content-Type", "application/json")
			if waitCalls == 0 {
				waitCalls++
				fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"","access_token":"","idle_duration":0,"scope":"","state":0}}`)
				return
			}
			waitCalls++
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","idle_duration":3600,"scope":"employee","state":2}}`)
		case "/xeai/logged/persistent_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"persist-1","userinfo":{"userid":"u-1","display_name":"张三"}}}`)
		case "/taiyi/composer/api/v1/users/whoami/userinfo":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan","user_scope":"employee"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := &App{configManager: manager, stdout: &stdout, stderr: &stderr}
	if err := app.Run(context.Background(), []string{"login", "qr"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "扫码登录二维码:") {
		t.Fatalf("expected QR login heading, got %q", output)
	}
	if !strings.Contains(output, "██") {
		t.Fatalf("expected terminal QR code in output, got %q", output)
	}
	if strings.Contains(output, "ticket:") || strings.Contains(output, "login_url:") || strings.Contains(output, "兼容等待命令") {
		t.Fatalf("expected QR output to hide raw ticket/url/wait command details, got %q", output)
	}
	if !strings.Contains(output, "请扫码并在手机端确认登录，可按 Ctrl+C 取消。") {
		t.Fatalf("expected auto wait hint, got %q", output)
	}
	if strings.Contains(output, "二维码已生成，等待扫码") || strings.Contains(output, "二维码已扫码") || strings.Contains(output, "扫码结果尚未完成") {
		t.Fatalf("expected QR output to stop printing dynamic wait states, got %q", output)
	}
	if !strings.Contains(output, "扫码登录成功。 已默认记住登录。") {
		t.Fatalf("expected QR login success output, got %q", output)
	}
	if !strings.Contains(output, "张三(已登录)") {
		t.Fatalf("expected refreshed current-user hint, got %q", output)
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-1" {
		t.Fatalf("expected persisted auth state, got %#v", reloaded)
	}
	if waitCalls < 2 {
		t.Fatalf("expected QR wait flow to poll until login succeeded, got %d calls", waitCalls)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestExecuteTUIQRWaitKeepsStaticQRCodeStateWithoutDynamicMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/wait_event":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"","access_token":"","idle_duration":0,"scope":"","state":1}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	result, err := app.executeTUIQRWait(
		context.Background(),
		buildProviderClient(config),
		tui.Snapshot{BackendAvailable: true},
		config,
		providerclient.NewAuthClient(server.URL),
		true,
		"ticket-1",
		"https://example.com/scan",
		"请使用企业微信扫码",
		0,
		nil,
	)
	if err != nil {
		t.Fatalf("executeTUIQRWait failed: %v", err)
	}
	if result.LoginForm == nil {
		t.Fatalf("expected login form to stay open, got %#v", result)
	}
	if !result.LoginForm.QRWaiting {
		t.Fatalf("expected retryable scanned state to keep qr waiting, got %#v", result.LoginForm)
	}
	if strings.Contains(result.LoginForm.StatusText, "二维码已扫码") || strings.Contains(result.LoginForm.StatusText, "扫码结果") {
		t.Fatalf("expected login form to stop appending dynamic qr wait status, got %#v", result.LoginForm)
	}
	if len(result.Events) != 0 {
		t.Fatalf("expected retryable qr wait state to stay silent, got %#v", result.Events)
	}
}

func TestExecuteTUIQRWaitShowsTerminalQRCodeStateError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/wait_event":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"","access_token":"","idle_duration":0,"scope":"","state":-1}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	result, err := app.executeTUIQRWait(
		context.Background(),
		buildProviderClient(config),
		tui.Snapshot{BackendAvailable: true},
		config,
		providerclient.NewAuthClient(server.URL),
		true,
		"ticket-1",
		"https://example.com/scan",
		"请使用企业微信扫码",
		0,
		nil,
	)
	if err != nil {
		t.Fatalf("executeTUIQRWait failed: %v", err)
	}
	if result.LoginForm == nil || result.LoginForm.QRWaiting {
		t.Fatalf("expected terminal qr state to stop waiting, got %#v", result.LoginForm)
	}
	if len(result.Events) != 1 || result.Events[0].Kind != tui.EventErrorNote || !strings.Contains(result.Events[0].Text, "请重新生成二维码后再试") {
		t.Fatalf("expected terminal qr state error event, got %#v", result.Events)
	}
}

func TestExecuteTUIQRWaitStopsAutoRetryAfterTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/wait_event":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"","access_token":"","idle_duration":0,"scope":"","state":0}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.QRWaitTimeoutSeconds = 2
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	result, err := app.executeTUIQRWait(
		context.Background(),
		buildProviderClient(config),
		tui.Snapshot{BackendAvailable: true},
		config,
		providerclient.NewAuthClient(server.URL),
		true,
		"ticket-1",
		"https://example.com/scan",
		"请使用企业微信扫码",
		config.QRWaitTimeoutSeconds-1,
		nil,
	)
	if err != nil {
		t.Fatalf("executeTUIQRWait failed: %v", err)
	}
	if result.LoginForm == nil {
		t.Fatalf("expected login form to stay open, got %#v", result)
	}
	if result.LoginForm.QRWaiting {
		t.Fatalf("expected timeout to stop qr auto waiting, got %#v", result.LoginForm)
	}
	if !strings.Contains(result.LoginForm.StatusText, "扫码等待已超过2秒") {
		t.Fatalf("expected timeout text in login form status, got %#v", result.LoginForm)
	}
	if len(result.Events) != 1 {
		t.Fatalf("expected only timeout event, got %#v", result.Events)
	}
	if result.Events[0].Kind != tui.EventSystemNote || !strings.Contains(result.Events[0].Text, "扫码等待已超过2秒") {
		t.Fatalf("expected timeout system note event, got %#v", result.Events)
	}
}

func TestShouldRetryCLIQRCodeWaitStopsWhenQRWaitingIsDisabled(t *testing.T) {
	result := tui.LocalCommandResult{
		Handled: true,
		LoginForm: &tui.LoginFormState{
			Mode:      "qr",
			QRTicket:  "ticket-1",
			QRWaiting: false,
		},
		Events: []tui.Event{{Kind: tui.EventSystemNote, Text: "扫码等待已超过2秒。请检查手机端是否已完成确认；如二维码失效请重新生成后再试。"}},
	}
	if shouldRetryCLIQRCodeWait(result) {
		t.Fatalf("expected cli qr wait to stop when qr waiting is disabled, got %#v", result)
	}
}

func TestRenderQRCodeASCIIUsesCompactHalfBlocks(t *testing.T) {
	rendered := renderQRCodeASCII("https://example.com/login/scan")
	if strings.TrimSpace(rendered) == "" {
		t.Fatal("expected non-empty qr code ascii")
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) > 18 {
		t.Fatalf("expected compact qr code height, got %d lines: %q", len(lines), rendered)
	}
	if !strings.Contains(rendered, "▀") && !strings.Contains(rendered, "▄") && !strings.Contains(rendered, "█") {
		t.Fatalf("expected qr code to use terminal block characters, got %q", rendered)
	}
}
