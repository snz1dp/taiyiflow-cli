package cmd

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

func TestBuildLoginCLISnapshotLoadsSavedSessionInfo(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	workdir := t.TempDir()
	if err := app.sessionStore.Save(agent.SessionState{
		FlowID:       "flow-1",
		Workdir:      workdir,
		ContextDir:   workdir,
		SessionID:    "session-1",
		SessionTitle: "历史会话",
	}); err != nil {
		t.Fatalf("save session failed: %v", err)
	}

	config := internalconfig.Default()
	config.BackendBaseURL = "https://example.com"
	config.AccessToken = "access-1"
	config.DefaultFlow = "flow-1"
	config.Workdir = workdir

	snapshot := app.buildLoginCLISnapshot(context.Background(), &stubSnapshotProvider{
		stubProvider: &stubProvider{
			messages:       &provider.SessionMessageList{Items: []provider.SessionMessage{{Sender: "user", Text: "你好"}, {Sender: "assistant", Text: "你好，我在。"}}},
			sessionContext: &provider.SessionContextPreview{ContextWindow: &provider.ContextWindowUsage{MaxInputTokens: 8192}},
		},
		currentUser: &provider.CurrentUser{Name: "张三"},
	}, config)

	if snapshot.CurrentUserName != "张三" {
		t.Fatalf("expected current user to be loaded, got %#v", snapshot)
	}
	if snapshot.SessionID != "session-1" || snapshot.SessionTitle != "历史会话" {
		t.Fatalf("expected saved session info to be loaded, got %#v", snapshot)
	}
	if len(snapshot.History) != 2 {
		t.Fatalf("expected saved session history to be loaded, got %#v", snapshot.History)
	}
	if snapshot.RequestStats == nil || snapshot.RequestStats.TokenUsage == nil || snapshot.RequestStats.TokenUsage.ContextWindow == nil {
		t.Fatalf("expected session stats to be loaded, got %#v", snapshot.RequestStats)
	}
}

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
	if !result.ClearHistory || !result.RebuildEntries {
		t.Fatalf("expected login success to reset session like /new, got %#v", result)
	}
	if len(result.Events) != 1 || result.Events[0].Kind != tui.EventSessionUpdate || result.Events[0].SessionID != "" {
		t.Fatalf("expected login success to emit session reset event, got %#v", result.Events)
	}
	if result.Snapshot == nil || result.Snapshot.CurrentUserName != "张三" {
		t.Fatalf("expected refreshed snapshot with current user, got %#v", result.Snapshot)
	}
	if strings.TrimSpace(result.FooterHint) != "" {
		t.Fatalf("expected login success to stay silent, got %#v", result)
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-1" {
		t.Fatalf("expected persisted auth state, got %#v", reloaded)
	}
}

func TestSubmitTUILoginFormPasswordClearsSavedSessionAfterSuccess(t *testing.T) {
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

	app := newTestAppWithSessionStore(t)
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app.configManager = manager
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.DefaultFlow = "flow-1"
	workdir := t.TempDir()
	config.Workdir = workdir
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	if err := app.sessionStore.Save(agent.SessionState{FlowID: "flow-1", Workdir: workdir, SessionID: "session-1", SessionTitle: "旧会话", ContextDir: workdir}); err != nil {
		t.Fatalf("save session failed: %v", err)
	}

	providerClient := buildProviderClient(config)
	snapshot := tui.Snapshot{BackendAvailable: true, DefaultFlow: "flow-1", Workdir: workdir, ContextDir: workdir, SessionID: "session-1", SessionTitle: "旧会话"}
	result, err := app.submitTUILoginForm(context.Background(), providerClient, snapshot, tui.LoginFormSubmission{
		Mode:     "password",
		Username: "zhangsan",
		Password: "secret-123",
		Remember: true,
	}, func(_ context.Context, current tui.Snapshot) (tui.Snapshot, []tui.FlowItem, error) {
		refreshed := app.buildLoginCLISnapshot(context.Background(), &stubSnapshotProvider{
			stubProvider: &stubProvider{},
			currentUser:  &provider.CurrentUser{Name: "张三"},
		}, internalconfig.Config{BackendBaseURL: server.URL, AccessToken: "access-1", DefaultFlow: current.DefaultFlow, Workdir: current.Workdir})
		refreshed.RequestStats = &tui.RequestMetrics{
			TokenUsage: &tui.TokenUsage{
				ContextWindow: &tui.ContextWindow{MaxInputTokens: 8192},
			},
		}
		return refreshed, nil, nil
	})
	if err != nil {
		t.Fatalf("submitTUILoginForm failed: %v", err)
	}
	loaded, err := app.prepareSession("flow-1", false, workdir, "")
	if err != nil {
		t.Fatalf("prepareSession failed: %v", err)
	}
	if loaded.SessionID != "" {
		t.Fatalf("expected saved session to be cleared after login, got %#v", loaded)
	}
	if result.Snapshot == nil || result.Snapshot.SessionID != "" {
		t.Fatalf("expected refreshed snapshot to start a new session, got %#v", result.Snapshot)
	}
	if result.Snapshot.RequestMode != provider.RequestTypeAgent {
		t.Fatalf("expected refreshed snapshot to reset request mode, got %#v", result.Snapshot)
	}
	if len(result.Snapshot.History) != 0 {
		t.Fatalf("expected refreshed snapshot to drop old session history, got %#v", result.Snapshot)
	}
	if result.Snapshot.RequestStats == nil || result.Snapshot.RequestStats.TokenUsage == nil || result.Snapshot.RequestStats.TokenUsage.ContextWindow == nil {
		t.Fatalf("expected refreshed snapshot to preserve context window budget, got %#v", result.Snapshot)
	}
	if result.Snapshot.RequestStats.TokenUsage.ContextWindow.MaxInputTokens != 8192 {
		t.Fatalf("expected refreshed snapshot to preserve max input tokens, got %#v", result.Snapshot.RequestStats.TokenUsage.ContextWindow)
	}
	if result.Snapshot.RequestStats.TokenUsage.ContextWindow.CurrentRoundInputTokens != 0 {
		t.Fatalf("expected refreshed snapshot to clear current round usage for new session, got %#v", result.Snapshot.RequestStats.TokenUsage.ContextWindow)
	}
	if !result.ClearHistory {
		t.Fatalf("expected login success to clear history, got %#v", result)
	}
}

func TestExecuteTUILoginCommandOpensOverlay(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/xeai/oauth/api/channel_names" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"code":0,"message":"调用成功!","data":""}`)
	}))
	defer server.Close()
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
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
	if result.LoginForm.SupportsQRCode {
		t.Fatalf("expected /login to hide qr mode when channel capability is unavailable, got %#v", result.LoginForm)
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

func TestParsePasswordLoginCommandAcceptsTicketAndCode(t *testing.T) {
	parsed, err := parsePasswordLoginCommand("/login password zhangsan secret-123 ticket=ticket-1 code=ABCD remember=off")
	if err != nil {
		t.Fatalf("parsePasswordLoginCommand failed: %v", err)
	}
	if parsed.Username != "zhangsan" || parsed.Password != "secret-123" {
		t.Fatalf("unexpected parsed credentials: %#v", parsed)
	}
	if parsed.Ticket != "ticket-1" || parsed.Code != "ABCD" {
		t.Fatalf("expected ticket/code to be parsed, got %#v", parsed)
	}
	if parsed.VerifyType != loginVerifyTypeKaptcha {
		t.Fatalf("expected ticket to imply kaptcha verify type, got %#v", parsed)
	}
	if parsed.Remember {
		t.Fatalf("expected remember=off to disable remember, got %#v", parsed)
	}
}

func TestParsePasswordLoginCommandAcceptsPasswordTicket(t *testing.T) {
	parsed, err := parsePasswordLoginCommand("/login password zhangsan secret-123 verify_type=sms password_ticket=password-ticket-1 ticket=verify-ticket-1 code=123456")
	if err != nil {
		t.Fatalf("parsePasswordLoginCommand failed: %v", err)
	}
	if parsed.PasswordTicket != "password-ticket-1" {
		t.Fatalf("expected password_ticket to be parsed, got %#v", parsed)
	}
	if parsed.Ticket != "verify-ticket-1" {
		t.Fatalf("expected verify ticket to be parsed separately, got %#v", parsed)
	}
}

func TestParsePasswordLoginCommandRejectsLegacyCaptchaAliases(t *testing.T) {
	_, err := parsePasswordLoginCommand("/login password zhangsan secret-123 captcha_ticket=ticket-1 captcha_code=ABCD")
	if err == nil {
		t.Fatal("expected legacy captcha aliases to be rejected")
	}
	if !strings.Contains(err.Error(), "不支持的登录选项: captcha_ticket") {
		t.Fatalf("expected legacy captcha alias error, got %v", err)
	}
}

func TestSubmitTUILoginFormPasswordRequiresCaptcha(t *testing.T) {
	originalOpenCaptchaPreviewFile := openCaptchaPreviewFile
	openedPreviewPath := ""
	openCaptchaPreviewFile = func(path string) error {
		openedPreviewPath = path
		return nil
	}
	defer func() {
		openCaptchaPreviewFile = originalOpenCaptchaPreviewFile
	}()
	t.Setenv("TERM_PROGRAM", "vscode")

	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER}))
	kaptchaTicketCalled := false
	kaptchaImageQueryTicket := ""
	var kaptchaPNG bytes.Buffer
	kaptchaImage := image.NewRGBA(image.Rect(0, 0, 32, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 32; x++ {
			pixel := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if (x/4+y/3)%2 == 0 {
				pixel = color.RGBA{R: 32, G: 32, B: 32, A: 255}
			}
			kaptchaImage.Set(x, y, pixel)
		}
	}
	if err := png.Encode(&kaptchaPNG, kaptchaImage); err != nil {
		t.Fatalf("encode captcha image failed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/password_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"password-ticket-1","pubkey":%q}}`, publicKeyPEM)
		case "/xeai/ajax_logon":
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(writer, `{"code":404,"message":"需要图形验证码","data":{"type":"kaptcha","ticket":"captcha-ticket-1","hint":"请输入图形验证码","interval":120}}`)
		case "/xeai/kaptcha/ticket":
			kaptchaTicketCalled = true
			http.NotFound(writer, request)
		case "/xeai/kaptcha/image":
			kaptchaImageQueryTicket = request.URL.Query().Get("ticket")
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write(kaptchaPNG.Bytes())
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
	if kaptchaTicketCalled {
		t.Fatalf("expected captcha flow not to call /kaptcha/ticket, got %#v", result.LoginForm)
	}
	if result.LoginForm.Ticket != "captcha-ticket-1" {
		t.Fatalf("expected captcha flow to use ajax_logon challenge ticket, got %#v", result.LoginForm)
	}
	if result.LoginForm.PasswordTicket != "password-ticket-1" {
		t.Fatalf("expected captcha flow to keep password_ticket separately, got %#v", result.LoginForm)
	}
	if strings.TrimSpace(result.LoginForm.PasswordPublicKey) != strings.TrimSpace(publicKeyPEM) {
		t.Fatalf("expected captcha flow to preserve login public key, got %#v", result.LoginForm)
	}
	if result.LoginForm.Password != "secret-123" || result.LoginForm.Username != "zhangsan" {
		t.Fatalf("expected password flow to preserve username/password, got %#v", result.LoginForm)
	}
	if len(result.Events) != 1 || !strings.Contains(result.Events[0].Text, "图形验证码") {
		t.Fatalf("expected captcha-required hint event, got %#v", result.Events)
	}
	if !strings.Contains(result.LoginForm.StatusText, "图形验证码") {
		t.Fatalf("expected captcha-required hint in login overlay status, got %#v", result.LoginForm)
	}
	if result.LoginForm.StatusIsError {
		t.Fatalf("expected captcha-required hint to stay non-error, got %#v", result.LoginForm)
	}
	if result.Events[0].Kind != tui.EventSystemNote {
		t.Fatalf("expected captcha-required hint to use system note, got %#v", result.Events)
	}
	if result.LoginForm.VerifyType != "kaptcha" {
		t.Fatalf("expected captcha flow to expose verification type, got %#v", result.LoginForm)
	}
	if strings.TrimSpace(result.LoginForm.VerifyImageASCII) == "" {
		t.Fatalf("expected captcha flow to render captcha image, got %#v", result.LoginForm)
	}
	if strings.TrimSpace(result.LoginForm.VerifyImagePNGBase64) == "" {
		t.Fatalf("expected captcha flow to keep terminal image payload, got %#v", result.LoginForm)
	}
	if !strings.Contains(result.LoginForm.VerifyImageURL, "/xeai/kaptcha/image") || !strings.Contains(result.LoginForm.VerifyImageURL, "ticket=captcha-ticket-1") {
		t.Fatalf("expected captcha flow to keep real captcha image url, got %#v", result.LoginForm)
	}
	if kaptchaImageQueryTicket != "captcha-ticket-1" {
		t.Fatalf("expected captcha image to use ajax_logon challenge ticket, got %q", kaptchaImageQueryTicket)
	}
	if strings.TrimSpace(result.LoginForm.VerifyImagePreviewPath) == "" {
		t.Fatalf("expected captcha flow to keep external preview path, got %#v", result.LoginForm)
	}
	if openedPreviewPath != result.LoginForm.VerifyImagePreviewPath {
		t.Fatalf("expected preview opener to receive same path, got opened=%q result=%q", openedPreviewPath, result.LoginForm.VerifyImagePreviewPath)
	}
	if _, err := os.Stat(result.LoginForm.VerifyImagePreviewPath); err != nil {
		t.Fatalf("expected preview image file to exist, got %v", err)
	}
	if result.LoginForm.VerifyImageWidth <= 0 || result.LoginForm.VerifyImageHeight <= 0 {
		t.Fatalf("expected captcha flow to keep image size, got %#v", result.LoginForm)
	}
	if result.LoginForm.FocusField != 3 {
		t.Fatalf("expected captcha flow to focus captcha field, got %#v", result.LoginForm)
	}
}

func TestSubmitTUILoginFormPasswordCaptchaRefetchesPasswordTokenEachSubmit(t *testing.T) {
	firstPrivateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate first rsa key failed: %v", err)
	}
	firstPublicKeyDER, err := x509.MarshalPKIXPublicKey(&firstPrivateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal first public key failed: %v", err)
	}
	firstPublicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: firstPublicKeyDER}))
	secondPrivateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate second rsa key failed: %v", err)
	}
	secondPublicKeyDER, err := x509.MarshalPKIXPublicKey(&secondPrivateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal second public key failed: %v", err)
	}
	secondPublicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: secondPublicKeyDER}))
	var passwordTokenCalls int
	var ajaxLogonCalls int
	var kaptchaTicketCalls int
	var kaptchaImageCalls int
	var kaptchaPNG bytes.Buffer
	kaptchaImage := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			pixel := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if (x+y)%2 == 0 {
				pixel = color.RGBA{R: 32, G: 32, B: 32, A: 255}
			}
			kaptchaImage.Set(x, y, pixel)
		}
	}
	if err := png.Encode(&kaptchaPNG, kaptchaImage); err != nil {
		t.Fatalf("encode captcha image failed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/password_token":
			passwordTokenCalls++
			writer.Header().Set("Content-Type", "application/json")
			if passwordTokenCalls == 1 {
				fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"password-ticket-1","pubkey":%q}}`, firstPublicKeyPEM)
				return
			}
			fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"password-ticket-2","pubkey":%q}}`, secondPublicKeyPEM)
		case "/xeai/ajax_logon":
			ajaxLogonCalls++
			query := request.URL.Query()
			expectedTicket := ""
			expectedPasswordTicket := "password-ticket-1"
			decryptKey := firstPrivateKey
			if ajaxLogonCalls > 1 {
				expectedTicket = "captcha-ticket-1"
				expectedPasswordTicket = "password-ticket-2"
				decryptKey = secondPrivateKey
			}
			if got := query.Get("ticket"); got != expectedTicket {
				t.Fatalf("unexpected ajax_logon ticket: %q", got)
			}
			if got := query.Get("password_ticket"); got != expectedPasswordTicket {
				t.Fatalf("unexpected password ticket: %q", got)
			}
			encryptedPassword, decodeErr := base64.StdEncoding.DecodeString(query.Get("password0"))
			if decodeErr != nil {
				t.Fatalf("decode encrypted password failed: %v", decodeErr)
			}
			password, decryptErr := rsa.DecryptPKCS1v15(nil, decryptKey, encryptedPassword)
			if decryptErr != nil {
				t.Fatalf("decrypt password failed: %v", decryptErr)
			}
			if string(password) != "secret-123" {
				t.Fatalf("unexpected decrypted password: %q", string(password))
			}
			if ajaxLogonCalls == 1 {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusBadRequest)
				fmt.Fprint(writer, `{"code":404,"message":"需要图形验证码","data":{"type":"kaptcha","ticket":"captcha-ticket-1","hint":"请输入图形验证码","interval":120}}`)
				return
			}
			if got := query.Get("code"); got != "ABCD" {
				t.Fatalf("unexpected code: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","idle_duration":3600,"scope":"employee"}}`)
		case "/xeai/kaptcha/ticket":
			kaptchaTicketCalls++
			http.NotFound(writer, request)
		case "/xeai/kaptcha/image":
			kaptchaImageCalls++
			if got := request.URL.Query().Get("ticket"); got != "captcha-ticket-1" {
				t.Fatalf("unexpected image ticket: %q", got)
			}
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write(kaptchaPNG.Bytes())
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
	firstResult, err := app.submitTUILoginForm(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true, AuthRequired: true}, tui.LoginFormSubmission{
		Mode:     "password",
		Username: "zhangsan",
		Password: "secret-123",
		Remember: false,
	}, nil)
	if err != nil {
		t.Fatalf("first submitTUILoginForm failed: %v", err)
	}
	if firstResult.LoginForm == nil {
		t.Fatalf("expected captcha challenge login form, got %#v", firstResult)
	}
	secondResult, err := app.submitTUILoginForm(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true, AuthRequired: true}, tui.LoginFormSubmission{
		Mode:                 "password",
		Username:             firstResult.LoginForm.Username,
		Password:             firstResult.LoginForm.Password,
		PasswordPublicKey:    firstResult.LoginForm.PasswordPublicKey,
		PasswordTicket:       firstResult.LoginForm.PasswordTicket,
		Remember:             false,
		VerifyType:           firstResult.LoginForm.VerifyType,
		VerifyImageASCII:     firstResult.LoginForm.VerifyImageASCII,
		VerifyImagePNGBase64: firstResult.LoginForm.VerifyImagePNGBase64,
		VerifyImageWidth:     firstResult.LoginForm.VerifyImageWidth,
		VerifyImageHeight:    firstResult.LoginForm.VerifyImageHeight,
		Ticket:               firstResult.LoginForm.Ticket,
		Code:                 "ABCD",
	}, nil)
	if err != nil {
		t.Fatalf("second submitTUILoginForm failed: %v", err)
	}
	if secondResult.LoginForm != nil {
		t.Fatalf("expected captcha login to succeed, got %#v", secondResult)
	}
	if passwordTokenCalls != 2 {
		t.Fatalf("expected password token fetched twice, got %d", passwordTokenCalls)
	}
	if ajaxLogonCalls != 2 {
		t.Fatalf("expected ajax_logon called twice, got %d", ajaxLogonCalls)
	}
	if kaptchaTicketCalls != 0 {
		t.Fatalf("expected /kaptcha/ticket not called, got %d", kaptchaTicketCalls)
	}
	if kaptchaImageCalls != 1 {
		t.Fatalf("expected /kaptcha/image called once, got %d", kaptchaImageCalls)
	}
}

func TestSubmitTUILoginFormPasswordUsesVerifyChallengeFromHTTPError(t *testing.T) {
	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER}))
	var kaptchaImageQueryTicket string
	var kaptchaPNG bytes.Buffer
	kaptchaImage := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			pixel := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if (x+y)%2 == 0 {
				pixel = color.RGBA{R: 32, G: 32, B: 32, A: 255}
			}
			kaptchaImage.Set(x, y, pixel)
		}
	}
	if err := png.Encode(&kaptchaPNG, kaptchaImage); err != nil {
		t.Fatalf("encode captcha image failed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/password_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"ticket-1","pubkey":%q}}`, publicKeyPEM)
		case "/xeai/ajax_logon":
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(writer, `{"code":404,"message":"需要图形验证码","data":{"type":"kaptcha","ticket":"captcha-ticket-1","hint":"请输入图形验证码","interval":120}}`)
		case "/xeai/kaptcha/image":
			kaptchaImageQueryTicket = request.URL.Query().Get("ticket")
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write(kaptchaPNG.Bytes())
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
		Remember: false,
	}, nil)
	if err != nil {
		t.Fatalf("submitTUILoginForm failed: %v", err)
	}
	if result.LoginForm == nil {
		t.Fatalf("expected captcha flow to reopen login form, got %#v", result)
	}
	if result.LoginForm.Ticket != "captcha-ticket-1" {
		t.Fatalf("expected captcha flow to use ajax_logon challenge ticket, got %#v", result.LoginForm)
	}
	if result.LoginForm.VerifyHint != "请输入图形验证码" {
		t.Fatalf("expected captcha flow to keep challenge hint, got %#v", result.LoginForm)
	}
	if kaptchaImageQueryTicket != "captcha-ticket-1" {
		t.Fatalf("expected captcha image to use challenge ticket, got %q", kaptchaImageQueryTicket)
	}
	if strings.TrimSpace(result.LoginForm.VerifyImagePNGBase64) == "" {
		t.Fatalf("expected captcha flow to keep terminal image payload, got %#v", result.LoginForm)
	}
}

func TestSubmitTUILoginFormPasswordCaptchaFailureRefreshesImageByReturnedTicket(t *testing.T) {
	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER}))

	firstImage := image.NewRGBA(image.Rect(0, 0, 16, 8))
	secondImage := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			firstPixel := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			secondPixel := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if (x+y)%2 == 0 {
				firstPixel = color.RGBA{R: 32, G: 32, B: 32, A: 255}
			}
			if (x/2+y)%2 == 0 {
				secondPixel = color.RGBA{R: 16, G: 96, B: 180, A: 255}
			}
			firstImage.Set(x, y, firstPixel)
			secondImage.Set(x, y, secondPixel)
		}
	}
	var firstImagePNG bytes.Buffer
	var secondImagePNG bytes.Buffer
	if err := png.Encode(&firstImagePNG, firstImage); err != nil {
		t.Fatalf("encode first captcha image failed: %v", err)
	}
	if err := png.Encode(&secondImagePNG, secondImage); err != nil {
		t.Fatalf("encode second captcha image failed: %v", err)
	}

	var ajaxLogonCalls int
	imageTickets := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/password_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"password-ticket-1","pubkey":%q}}`, publicKeyPEM)
		case "/xeai/ajax_logon":
			ajaxLogonCalls++
			query := request.URL.Query()
			if got := query.Get("password_ticket"); got != "password-ticket-1" {
				t.Fatalf("unexpected password_ticket: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			if ajaxLogonCalls == 1 {
				fmt.Fprint(writer, `{"code":404,"message":"需要图形验证码","data":{"type":"kaptcha","ticket":"captcha-ticket-1","hint":"请输入图形验证码","interval":120}}`)
				return
			}
			if got := query.Get("ticket"); got != "captcha-ticket-1" {
				t.Fatalf("unexpected retry captcha ticket: %q", got)
			}
			if got := query.Get("code"); got != "ABCD" {
				t.Fatalf("unexpected retry captcha code: %q", got)
			}
			fmt.Fprint(writer, `{"code":408,"message":"图形验证码错误","data":{"type":"kaptcha","ticket":"captcha-ticket-2","hint":"请重新输入图形验证码","interval":120}}`)
		case "/xeai/kaptcha/image":
			ticket := request.URL.Query().Get("ticket")
			imageTickets = append(imageTickets, ticket)
			writer.Header().Set("Content-Type", "image/png")
			if ticket == "captcha-ticket-2" {
				_, _ = writer.Write(secondImagePNG.Bytes())
				return
			}
			_, _ = writer.Write(firstImagePNG.Bytes())
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
	firstResult, err := app.submitTUILoginForm(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true, AuthRequired: true}, tui.LoginFormSubmission{
		Mode:     "password",
		Username: "zhangsan",
		Password: "secret-123",
		Remember: false,
	}, nil)
	if err != nil {
		t.Fatalf("first submitTUILoginForm failed: %v", err)
	}
	if firstResult.LoginForm == nil {
		t.Fatalf("expected first captcha challenge form, got %#v", firstResult)
	}
	firstImagePayload := firstResult.LoginForm.VerifyImagePNGBase64

	secondResult, err := app.submitTUILoginForm(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true, AuthRequired: true}, tui.LoginFormSubmission{
		Mode:                 "password",
		Username:             firstResult.LoginForm.Username,
		Password:             firstResult.LoginForm.Password,
		PasswordPublicKey:    firstResult.LoginForm.PasswordPublicKey,
		PasswordTicket:       firstResult.LoginForm.PasswordTicket,
		Remember:             false,
		VerifyType:           firstResult.LoginForm.VerifyType,
		VerifyHint:           firstResult.LoginForm.VerifyHint,
		VerifyTarget:         firstResult.LoginForm.VerifyTarget,
		VerifyImageASCII:     firstResult.LoginForm.VerifyImageASCII,
		VerifyImagePNGBase64: firstResult.LoginForm.VerifyImagePNGBase64,
		VerifyImageWidth:     firstResult.LoginForm.VerifyImageWidth,
		VerifyImageHeight:    firstResult.LoginForm.VerifyImageHeight,
		VerifyImageURL:       firstResult.LoginForm.VerifyImageURL,
		Ticket:               firstResult.LoginForm.Ticket,
		Code:                 "ABCD",
	}, nil)
	if err != nil {
		t.Fatalf("second submitTUILoginForm failed: %v", err)
	}
	if secondResult.LoginForm == nil {
		t.Fatalf("expected captcha retry form, got %#v", secondResult)
	}
	if secondResult.LoginForm.Ticket != "captcha-ticket-2" {
		t.Fatalf("expected retry form to adopt returned verify ticket, got %#v", secondResult.LoginForm)
	}
	if !strings.Contains(secondResult.LoginForm.VerifyImageURL, "ticket=captcha-ticket-2") {
		t.Fatalf("expected retry form to refresh image url with returned ticket, got %#v", secondResult.LoginForm)
	}
	if secondResult.LoginForm.VerifyImagePNGBase64 == firstImagePayload {
		t.Fatalf("expected retry form to refresh captcha payload, got %#v", secondResult.LoginForm)
	}
	if secondResult.LoginForm.Code != "" {
		t.Fatalf("expected invalid captcha code to be cleared, got %#v", secondResult.LoginForm)
	}
	if len(imageTickets) != 2 || imageTickets[0] != "captcha-ticket-1" || imageTickets[1] != "captcha-ticket-2" {
		t.Fatalf("expected captcha image to be fetched twice with refreshed tickets, got %#v", imageTickets)
	}
	if secondResult.LoginForm.FocusField != 3 {
		t.Fatalf("expected retry form to keep captcha field focus, got %#v", secondResult.LoginForm)
	}
}

func TestSubmitTUILoginFormPasswordChallengeTicketOverridesQRTicket(t *testing.T) {
	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER}))
	var kaptchaImageQueryTicket string
	var kaptchaPNG bytes.Buffer
	kaptchaImage := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			pixel := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if (x+y)%2 == 0 {
				pixel = color.RGBA{R: 32, G: 32, B: 32, A: 255}
			}
			kaptchaImage.Set(x, y, pixel)
		}
	}
	if err := png.Encode(&kaptchaPNG, kaptchaImage); err != nil {
		t.Fatalf("encode captcha image failed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/password_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"password-ticket-1","pubkey":%q}}`, publicKeyPEM)
		case "/xeai/ajax_logon":
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(writer, `{"code":404,"message":"需要图形验证码","data":{"type":"kaptcha","ticket":"captcha-ticket-1","hint":"请输入图形验证码","interval":120}}`)
		case "/xeai/kaptcha/image":
			kaptchaImageQueryTicket = request.URL.Query().Get("ticket")
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write(kaptchaPNG.Bytes())
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
		Remember: false,
		QRTicket: "qr-ticket-1",
	}, nil)
	if err != nil {
		t.Fatalf("submitTUILoginForm failed: %v", err)
	}
	if result.LoginForm == nil {
		t.Fatalf("expected captcha flow to reopen login form, got %#v", result)
	}
	if result.LoginForm.Ticket != "captcha-ticket-1" {
		t.Fatalf("expected captcha challenge ticket to override qr ticket, got %#v", result.LoginForm)
	}
	if kaptchaImageQueryTicket != "captcha-ticket-1" {
		t.Fatalf("expected captcha image to use challenge ticket instead of qr ticket, got %q", kaptchaImageQueryTicket)
	}
	if result.LoginForm.QRTicket != "" {
		t.Fatalf("expected password captcha form not to carry qr ticket, got %#v", result.LoginForm)
	}
}

func TestSubmitTUILoginFormPasswordVerifyLoginPreservesPasswordTicket(t *testing.T) {
	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER}))
	var verifyLoginTicket string
	var verifyLoginPasswordTicket string

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/password_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"code":0,"message":"OK","data":{"ticket":"password-ticket-1","pubkey":%q}}`, publicKeyPEM)
		case "/xeai/ajax_logon":
			query := request.URL.Query()
			if got := query.Get("password_ticket"); got != "password-ticket-1" {
				t.Fatalf("unexpected ajax_logon password_ticket: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":405,"message":"需要短信验证码","data":{"type":"sms","hint":"请输入短信验证码","target":"138****0000"}}`)
		case "/xeai/ajax_logon/verify_token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"verify-ticket-1","type":"sms","hint":"请输入短信验证码","target":"138****0000"}}`)
		case "/xeai/ajax_logon/verify_login":
			verifyLoginTicket = request.URL.Query().Get("ticket")
			verifyLoginPasswordTicket = request.URL.Query().Get("password_ticket")
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","idle_duration":3600,"scope":"employee"}}`)
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
	firstResult, err := app.submitTUILoginForm(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true, AuthRequired: true}, tui.LoginFormSubmission{
		Mode:     "password",
		Username: "zhangsan",
		Password: "secret-123",
		Remember: false,
	}, nil)
	if err != nil {
		t.Fatalf("first submitTUILoginForm failed: %v", err)
	}
	if firstResult.LoginForm == nil {
		t.Fatalf("expected verify challenge login form, got %#v", firstResult)
	}
	if firstResult.LoginForm.PasswordTicket != "password-ticket-1" {
		t.Fatalf("expected login form to preserve password ticket, got %#v", firstResult.LoginForm)
	}
	if firstResult.LoginForm.Ticket != "verify-ticket-1" {
		t.Fatalf("expected login form to store verify ticket separately, got %#v", firstResult.LoginForm)
	}

	secondResult, err := app.submitTUILoginForm(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true, AuthRequired: true}, tui.LoginFormSubmission{
		Mode:              "password",
		Username:          firstResult.LoginForm.Username,
		Password:          firstResult.LoginForm.Password,
		PasswordTicket:    firstResult.LoginForm.PasswordTicket,
		Remember:          false,
		VerifyType:        firstResult.LoginForm.VerifyType,
		VerifyHint:        firstResult.LoginForm.VerifyHint,
		VerifyTarget:      firstResult.LoginForm.VerifyTarget,
		Ticket:            firstResult.LoginForm.Ticket,
		Code:              "123456",
		PasswordPublicKey: firstResult.LoginForm.PasswordPublicKey,
	}, nil)
	if err != nil {
		t.Fatalf("second submitTUILoginForm failed: %v", err)
	}
	if secondResult.LoginForm != nil {
		t.Fatalf("expected verify login to succeed, got %#v", secondResult)
	}
	if verifyLoginTicket != "verify-ticket-1" {
		t.Fatalf("expected verify_login to use verify ticket, got %q", verifyLoginTicket)
	}
	if verifyLoginPasswordTicket != "password-ticket-1" {
		t.Fatalf("expected verify_login to preserve password_ticket from password token, got %q", verifyLoginPasswordTicket)
	}
}

func TestRenderVerificationImageASCIIFromImageUsesBlockGlyphs(t *testing.T) {
	decodedImage := image.NewRGBA(image.Rect(0, 0, 32, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 32; x++ {
			pixel := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if (x/4+y/3)%2 == 0 {
				pixel = color.RGBA{R: 16, G: 16, B: 16, A: 255}
			}
			decodedImage.Set(x, y, pixel)
		}
	}

	rendered := renderVerificationImageASCIIFromImage(decodedImage)
	if strings.TrimSpace(rendered) == "" {
		t.Fatal("expected block ascii captcha rendering")
	}
	if !strings.ContainsAny(rendered, "█▀▄") {
		t.Fatalf("expected block glyphs in captcha rendering, got %q", rendered)
	}
	if strings.ContainsAny(rendered, ".:-=+*#%@") {
		t.Fatalf("expected old grayscale ramp to be removed, got %q", rendered)
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
	usage := loginSlashUsage(true)
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
	if !strings.Contains(usage, "TAIYIFLOW_CLI_TERMINAL_IMAGE_PROTOCOL=kitty|iterm2|off") {
		t.Fatalf("expected usage to mention terminal image protocol override, got %q", usage)
	}
	if strings.Contains(usage, "打开 overlay 登录表单") {
		t.Fatalf("expected usage to stop using old overlay wording, got %q", usage)
	}
}

func TestLoginSlashUsageHidesQRCodeEntryWhenUnsupported(t *testing.T) {
	usage := loginSlashUsage(false)
	if strings.Contains(usage, "\n/login qr ") || strings.Contains(usage, "\n/login qr\n") {
		t.Fatalf("expected usage to hide qr entry when unsupported, got %q", usage)
	}
	if !strings.Contains(usage, "当前认证服务未启用扫码登录") {
		t.Fatalf("expected usage to explain qr unavailability, got %q", usage)
	}
}

func TestExecuteTUILoginCommandRejectsQRCodeWhenChannelUnsupported(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/xeai/oauth/api/channel_names" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"code":0,"message":"调用成功!","data":""}`)
	}))
	defer server.Close()
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	app := &App{configManager: manager}
	result, err := app.executeTUILoginCommand(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true}, "/login qr", nil)
	if err != nil {
		t.Fatalf("executeTUILoginCommand failed: %v", err)
	}
	if result.LoginForm != nil {
		t.Fatalf("expected qr command to stop before opening qr form when unsupported, got %#v", result)
	}
	if len(result.Events) != 1 || result.Events[0].Text != loginQRCodeUnavailableMessage {
		t.Fatalf("expected qr unsupported error event, got %#v", result.Events)
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
		case "/xeai/oauth/api/channel_names":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"调用成功!","data":"钉钉"}`)
		case "/xeai/oauth/api/token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"qr-ticket-1","fireurl":"https://example.com/scan","scanhint":"请使用微信扫码"}}`)
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
	if !strings.Contains(output, "扫码提示: 请使用钉钉扫码登录。") {
		t.Fatalf("expected qr hint to use channel_names value, got %q", output)
	}
	if strings.Contains(output, "扫码提示: 请使用微信") {
		t.Fatalf("expected qr hint to stop using hard-coded provider names, got %q", output)
	}
	if strings.Contains(output, "二维码已生成，等待扫码") || strings.Contains(output, "二维码已扫码") || strings.Contains(output, "扫码结果尚未完成") {
		t.Fatalf("expected QR output to stop printing dynamic wait states, got %q", output)
	}
	if strings.Contains(output, "扫码登录成功。") || strings.Contains(output, "已默认记住登录。") || strings.Contains(output, "张三(已登录)") {
		t.Fatalf("expected QR login success to stay silent, got %q", output)
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

func TestNormalizeQRScanHintUsesChannelNames(t *testing.T) {
	hint := normalizeQRScanHint("请使用微信扫码", []string{"钉钉", "企业微信"})
	if hint != "请使用钉钉或企业微信扫码登录。" {
		t.Fatalf("expected qr hint to use channel names, got %q", hint)
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
		"请使用微信扫码",
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
		"请使用微信扫码",
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
		"请使用微信扫码",
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
