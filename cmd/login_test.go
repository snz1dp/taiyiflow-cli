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
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

func TestExecuteTUILoginCommandPasswordUpdatesAuthState(t *testing.T) {
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
	result, err := app.executeTUILoginCommand(context.Background(), providerClient, snapshot, "/login password zhangsan secret-123", func(_ context.Context, current tui.Snapshot) (tui.Snapshot, []tui.FlowItem, error) {
		refreshCalled = true
		current.CurrentUserName = "张三"
		current.AuthRequired = false
		return current, nil, nil
	})
	if err != nil {
		t.Fatalf("executeTUILoginCommand failed: %v", err)
	}
	if !refreshCalled {
		t.Fatal("expected refresh callback to run after login")
	}
	if result.Snapshot == nil || result.Snapshot.CurrentUserName != "张三" {
		t.Fatalf("expected refreshed snapshot with current user, got %#v", result.Snapshot)
	}
	if result.FooterHint != "张三(已登录)" {
		t.Fatalf("expected footer hint to announce current user, got %#v", result)
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
	if result.LoginForm.Title != "登录(login)" {
		t.Fatalf("unexpected login overlay title: %#v", result.LoginForm)
	}
	if strings.Contains(result.LoginForm.StatusText, "<未生成>") {
		t.Fatalf("expected login overlay to show generated device id, got %#v", result.LoginForm)
	}
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if strings.TrimSpace(reloaded.DeviceID) == "" {
		t.Fatalf("expected /login to persist generated device id, got %#v", reloaded)
	}
}

func TestExecuteTUILoginCommandPasswordRequiresCaptcha(t *testing.T) {
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
	result, err := app.executeTUILoginCommand(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true, AuthRequired: true}, "/login password zhangsan secret-123", nil)
	if err != nil {
		t.Fatalf("executeTUILoginCommand failed: %v", err)
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

func TestExecuteTUILoginCommandPasswordShowsInlineErrorAndKeepsPasswordFocus(t *testing.T) {
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
	result, err := app.executeTUILoginCommand(context.Background(), buildProviderClient(config), tui.Snapshot{BackendAvailable: true, AuthRequired: true}, "/login password zhangsan secret-123", nil)
	if err != nil {
		t.Fatalf("executeTUILoginCommand failed: %v", err)
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
	if !strings.Contains(usage, "兼容命令(自动化或脚本场景):") {
		t.Fatalf("expected usage to separate compatibility commands, got %q", usage)
	}
	if !strings.Contains(usage, "/login qr") || !strings.Contains(usage, "/login password") {
		t.Fatalf("expected usage to retain compatibility commands, got %q", usage)
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

func TestRunLoginPasswordSubcommandPersistsAuthState(t *testing.T) {
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
	if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-1" {
		t.Fatalf("expected persisted auth state, got %#v", reloaded)
	}
	if !strings.Contains(stdout.String(), "已完成用户名密码登录") {
		t.Fatalf("expected password login success output, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "张三(已登录)") {
		t.Fatalf("expected refreshed current-user hint, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}

func TestRunLoginQRSubcommandPrintsWaitCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/oauth/api/token":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"qr-ticket-1","fireurl":"https://example.com/scan","scanhint":"请使用企业微信扫码"}}`)
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
	if !strings.Contains(output, "扫码登录信息:") {
		t.Fatalf("expected QR login heading, got %q", output)
	}
	if !strings.Contains(output, "ticket: qr-ticket-1") {
		t.Fatalf("expected QR login ticket in output, got %q", output)
	}
	if !strings.Contains(output, "login_url: https://example.com/scan") {
		t.Fatalf("expected QR login url in output, got %q", output)
	}
	if !strings.Contains(output, "下一步: taiyiflow-cli login qr wait qr-ticket-1") {
		t.Fatalf("expected QR wait command hint, got %q", output)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}
