package taiyiflow

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExchangePersistentToken(t *testing.T) {
	t.Helper()
	expectedSign := buildPersistentLoginSignature("device-1", "salt-value")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/sso/salt":
			if got := request.URL.Query().Get("persistent_token"); got != "persist-token" {
				t.Fatalf("unexpected persistent token: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"salt-ticket","value":"salt-value"}}`)
		case "/xeai/sso":
			query := request.URL.Query()
			if got := query.Get("persistent_token"); got != "persist-token" {
				t.Fatalf("unexpected persistent token on sso: %q", got)
			}
			if got := query.Get("salt_ticket"); got != "salt-ticket" {
				t.Fatalf("unexpected salt ticket: %q", got)
			}
			if got := query.Get("alg"); got != "SHA-256" {
				t.Fatalf("unexpected sign alg: %q", got)
			}
			if got := query.Get("sign"); got != expectedSign {
				t.Fatalf("unexpected sign: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","persistent_token":"persist-token-2","idle_duration":3600,"scope":"employee"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	session, err := client.ExchangePersistentToken(context.Background(), "persist-token", "device-1")
	if err != nil {
		t.Fatalf("expected persistent login to succeed, got %v", err)
	}
	if session == nil {
		t.Fatal("expected auth session")
	}
	if session.AccessToken != "access-1" || session.PersistentToken != "persist-token-2" {
		t.Fatalf("unexpected auth session: %#v", session)
	}
}

func TestCreatePersistentToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/xeai/logged/persistent_token" {
			http.NotFound(writer, request)
			return
		}
		if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		if got := request.URL.Query().Get("device_id"); got != "device-1" {
			t.Fatalf("unexpected device id: %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"persist-token","userinfo":{"userid":"u-1","display_name":"张三"}}}`)
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	persistentToken, err := client.CreatePersistentToken(context.Background(), "access-1", "device-1")
	if err != nil {
		t.Fatalf("expected create persistent token to succeed, got %v", err)
	}
	if persistentToken == nil || persistentToken.Ticket != "persist-token" {
		t.Fatalf("unexpected persistent token response: %#v", persistentToken)
	}
}

func TestPasswordLoginEncryptsPasswordAndParsesSession(t *testing.T) {
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
		case "/xeai/ajax_logon":
			query := request.URL.Query()
			if got := query.Get("ticket"); got != "ticket-1" {
				t.Fatalf("unexpected ticket: %q", got)
			}
			if got := query.Get("password_ticket"); got != "ticket-1" {
				t.Fatalf("unexpected password ticket: %q", got)
			}
			if got := query.Get("username"); got != "zhangsan" {
				t.Fatalf("unexpected username: %q", got)
			}
			if got := query.Get("password"); got != "" {
				t.Fatalf("expected legacy password field to be omitted, got %q", got)
			}
			encryptedPassword, decodeErr := base64.StdEncoding.DecodeString(query.Get("password0"))
			if decodeErr != nil {
				t.Fatalf("decode encrypted password failed: %v", decodeErr)
			}
			password, decryptErr := rsa.DecryptPKCS1v15(nil, privateKey, encryptedPassword)
			if decryptErr != nil {
				t.Fatalf("decrypt password failed: %v", decryptErr)
			}
			if string(password) != "secret-123" {
				t.Fatalf("unexpected decrypted password: %q", string(password))
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","idle_duration":3600,"scope":"employee"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	session, err := client.PasswordLogin(context.Background(), PasswordLoginRequest{
		VerifyTicket:         "ticket-1",
		PasswordTicket: "ticket-1",
		PublicKey:      publicKeyPEM,
		Username:       "zhangsan",
		Password:       "secret-123",
	})
	if err != nil {
		t.Fatalf("expected password login to succeed, got %v", err)
	}
	if session == nil || session.AccessToken != "access-1" {
		t.Fatalf("unexpected session: %#v", session)
	}
}

func TestPasswordLoginWithCodeSendsEncryptedPasswordAndCode(t *testing.T) {
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
		case "/xeai/ajax_logon":
			query := request.URL.Query()
			if got := query.Get("username"); got != "zhangsan" {
				t.Fatalf("unexpected username: %q", got)
			}
			encryptedPassword, decodeErr := base64.StdEncoding.DecodeString(query.Get("password0"))
			if decodeErr != nil {
				t.Fatalf("decode encrypted password failed: %v", decodeErr)
			}
			password, decryptErr := rsa.DecryptPKCS1v15(nil, privateKey, encryptedPassword)
			if decryptErr != nil {
				t.Fatalf("decrypt password failed: %v", decryptErr)
			}
			if string(password) != "secret-123" {
				t.Fatalf("unexpected decrypted password: %q", string(password))
			}
			if got := query.Get("code"); got != "ABCD" {
				t.Fatalf("unexpected code: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","idle_duration":3600,"scope":"employee"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	session, err := client.PasswordLogin(context.Background(), PasswordLoginRequest{
		VerifyTicket:         "ticket-1",
		PasswordTicket: "ticket-1",
		PublicKey:      publicKeyPEM,
		Username:       "zhangsan",
		Password:       "secret-123",
		Code:           "ABCD",
	})
	if err != nil {
		t.Fatalf("expected captcha password login to succeed, got %v", err)
	}
	if session == nil || session.AccessToken != "access-1" {
		t.Fatalf("unexpected session: %#v", session)
	}
}

func TestGetPasswordLoginTokenRequiresPasswordTicket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/xeai/oauth/api/password_token" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"pubkey":"public-key-1"}}`)
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	_, err := client.GetPasswordLoginToken(context.Background())
	if err == nil {
		t.Fatal("expected password token request to fail when password_ticket is missing")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthError, got %T", err)
	}
	if authErr.Message != "password_ticket 不能为空" {
		t.Fatalf("expected missing password_ticket message, got %#v", authErr)
	}
}

func TestPasswordLoginRequiresPasswordTicket(t *testing.T) {
	privateKey, err := rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key failed: %v", err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key failed: %v", err)
	}
	publicKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicKeyDER}))

	client := NewAuthClient("https://example.invalid")
	_, err = client.PasswordLogin(context.Background(), PasswordLoginRequest{
		VerifyTicket: "ticket-1",
		PublicKey:    publicKeyPEM,
		Username:     "zhangsan",
		Password:     "secret-123",
	})
	if err == nil {
		t.Fatal("expected password login to reject empty password_ticket")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthError, got %T", err)
	}
	if authErr.Message != "请先获取登录公钥" {
		t.Fatalf("unexpected error message: %#v", authErr)
	}
}

func TestValidateVerifyCodeSendsPasswordTicket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/xeai/send_verify/validate" {
			http.NotFound(writer, request)
			return
		}
		query := request.URL.Query()
		if got := query.Get("username"); got != "zhangsan" {
			t.Fatalf("unexpected username: %q", got)
		}
		if got := query.Get("verify_ticket"); got != "ticket-1" {
			t.Fatalf("unexpected verify_ticket: %q", got)
		}
		if got := query.Get("password_ticket"); got != "password-ticket-1" {
			t.Fatalf("unexpected password_ticket: %q", got)
		}
		if got := query.Get("verify_type"); got != "sms" {
			t.Fatalf("unexpected verify_type: %q", got)
		}
		if got := query.Get("verify_code"); got != "123456" {
			t.Fatalf("unexpected verify_code: %q", got)
		}
		if got := query.Get("verify_id"); got != "verify-id-1" {
			t.Fatalf("unexpected verify_id: %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"code":0,"message":"OK","data":null}`)
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	if err := client.ValidateVerifyCode(context.Background(), "zhangsan", "ticket-1", "password-ticket-1", "sms", "123456", "verify-id-1"); err != nil {
		t.Fatalf("expected verify code validation to succeed, got %v", err)
	}
}

func TestCompleteVerifyLoginSendsPasswordTicket(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/xeai/ajax_logon/verify_login" {
			http.NotFound(writer, request)
			return
		}
		query := request.URL.Query()
		if got := query.Get("ticket"); got != "ticket-1" {
			t.Fatalf("unexpected ticket: %q", got)
		}
		if got := query.Get("password_ticket"); got != "password-ticket-1" {
			t.Fatalf("unexpected password_ticket: %q", got)
		}
		if got := query.Get("verify"); got != "123456" {
			t.Fatalf("unexpected verify code: %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","idle_duration":3600,"scope":"employee"}}`)
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	session, err := client.CompleteVerifyLogin(context.Background(), "ticket-1", "password-ticket-1", "123456")
	if err != nil {
		t.Fatalf("expected verify login to succeed, got %v", err)
	}
	if session == nil || session.AccessToken != "access-1" {
		t.Fatalf("unexpected session: %#v", session)
	}
}

func TestOAuthEndpointsUseXEAIContextPath(t *testing.T) {
	requests := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests[request.URL.Path] = true
		switch request.URL.Path {
		case "/xeai/oauth/api/token":
			if got := request.URL.Query().Get("platform"); got != "" {
				t.Fatalf("unexpected platform: %q", got)
			}
			if got := request.URL.Query().Get("type"); got != "login" {
				t.Fatalf("unexpected token type: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"ticket-1","fireurl":"https://example.com/scan","scanhint":"请扫码"}}`)
		case "/xeai/oauth/api/wait_event":
			if got := request.URL.Query().Get("ticket"); got != "ticket-1" {
				t.Fatalf("unexpected wait ticket: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","idle_duration":3600,"scope":"employee","state":2}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	qrToken, err := client.GetQRCodeLoginToken(context.Background(), "", false, "login", "")
	if err != nil {
		t.Fatalf("expected qr token request to succeed, got %v", err)
	}
	if qrToken == nil || qrToken.Ticket != "ticket-1" {
		t.Fatalf("unexpected qr token: %#v", qrToken)
	}
	waitEvent, err := client.WaitQRCodeLoginEvent(context.Background(), "ticket-1")
	if err != nil {
		t.Fatalf("expected wait event request to succeed, got %v", err)
	}
	if waitEvent == nil || waitEvent.AccessToken != "access-1" {
		t.Fatalf("unexpected wait event: %#v", waitEvent)
	}
	if !requests["/xeai/oauth/api/token"] || !requests["/xeai/oauth/api/wait_event"] {
		t.Fatalf("expected oauth endpoints to be requested under /xeai, got %#v", requests)
	}
}

func TestPasswordLoginReturnsEnvelopeMessageOnBusinessError(t *testing.T) {
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
		case "/xeai/ajax_logon":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":4001,"message":"用户名或密码错误","data":null}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	_, err = client.PasswordLogin(context.Background(), PasswordLoginRequest{
		VerifyTicket:   "ticket-1",
		PasswordTicket: "password-ticket-1",
		PublicKey:      publicKeyPEM,
		Username:       "zhangsan",
		Password:       "secret-123",
	})
	if err == nil {
		t.Fatal("expected password login to fail")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthError, got %T", err)
	}
	if authErr.Code != 4001 {
		t.Fatalf("expected business error code 4001, got %#v", authErr)
	}
	if authErr.Message != "用户名或密码错误" {
		t.Fatalf("expected business error message from envelope message field, got %#v", authErr)
	}
}

func TestPasswordLoginReturnsVerifyChallengeOnHTTPError(t *testing.T) {
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
		case "/xeai/ajax_logon":
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(writer, `{"code":404,"message":"需要图形验证码","data":{"type":"kaptcha","ticket":"captcha-ticket-1","hint":"请输入图形验证码","interval":120}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	_, err = client.PasswordLogin(context.Background(), PasswordLoginRequest{
		VerifyTicket:   "ticket-1",
		PasswordTicket: "password-ticket-1",
		PublicKey:      publicKeyPEM,
		Username:       "zhangsan",
		Password:       "secret-123",
	})
	if err == nil {
		t.Fatal("expected password login to fail")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthError, got %T", err)
	}
	if authErr.Code != 404 {
		t.Fatalf("expected challenge error code 404, got %#v", authErr)
	}
	if authErr.VerifyChallenge == nil {
		t.Fatalf("expected verify challenge payload, got %#v", authErr)
	}
	if authErr.VerifyChallenge.Type != "kaptcha" || authErr.VerifyChallenge.Ticket != "captcha-ticket-1" {
		t.Fatalf("unexpected verify challenge payload: %#v", authErr.VerifyChallenge)
	}
	if authErr.VerifyChallenge.Hint != "请输入图形验证码" || authErr.VerifyChallenge.Interval != 120 {
		t.Fatalf("unexpected verify challenge details: %#v", authErr.VerifyChallenge)
	}
}

func TestPasswordLoginParsesStringSessionToken(t *testing.T) {
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
		case "/xeai/ajax_logon":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"登录成功","data":"session-token-1"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	session, err := client.PasswordLogin(context.Background(), PasswordLoginRequest{
		VerifyTicket:   "ticket-1",
		PasswordTicket: "password-ticket-1",
		PublicKey:      publicKeyPEM,
		Username:       "zhangsan",
		Password:       "secret-123",
	})
	if err != nil {
		t.Fatalf("expected password login to succeed, got %v", err)
	}
	if session == nil || session.AccessToken != "session-token-1" {
		t.Fatalf("unexpected session: %#v", session)
	}
}

func TestGetQRCodeLoginTokenReturnsEnvelopeMessageOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/xeai/oauth/api/token" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(writer, `{"code":40101,"message":"登录已过期，请重新登录","data":null}`)
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	_, err := client.GetQRCodeLoginToken(context.Background(), "", false, "login", "")
	if err == nil {
		t.Fatal("expected qr token request to fail")
	}
	var authErr *AuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("expected AuthError, got %T", err)
	}
	if authErr.Code != 40101 {
		t.Fatalf("expected envelope code 40101, got %#v", authErr)
	}
	if authErr.Message != "登录已过期，请重新登录" {
		t.Fatalf("expected http error to surface envelope message, got %#v", authErr)
	}
}

func TestGetVerificationImageFallsBackToKaptchaTicketQuery(t *testing.T) {
	var imageBuffer bytes.Buffer
	verificationImage := image.NewRGBA(image.Rect(0, 0, 8, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 8; x++ {
			pixel := color.RGBA{R: 255, G: 255, B: 255, A: 255}
			if (x+y)%2 == 0 {
				pixel = color.RGBA{R: 16, G: 16, B: 16, A: 255}
			}
			verificationImage.Set(x, y, pixel)
		}
	}
	if err := png.Encode(&imageBuffer, verificationImage); err != nil {
		t.Fatalf("encode verification image failed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/kaptcha/image":
			query := request.URL.Query()
			if got := query.Get("ticket"); got == "ticket-1" {
				http.NotFound(writer, request)
				return
			}
			if got := query.Get("kaptcha_ticket"); got != "ticket-1" {
				t.Fatalf("unexpected kaptcha_ticket query: %q", got)
			}
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write(imageBuffer.Bytes())
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := NewAuthClient(server.URL)
	verificationImageResponse, err := client.GetVerificationImage(context.Background(), "ticket-1")
	if err != nil {
		t.Fatalf("expected verification image fallback to succeed, got %v", err)
	}
	if verificationImageResponse == nil || len(verificationImageResponse.Data) == 0 {
		t.Fatalf("expected verification image payload, got %#v", verificationImageResponse)
	}
	if verificationImageResponse.ContentType != "image/png" {
		t.Fatalf("unexpected verification image content type: %#v", verificationImageResponse)
	}
	if !strings.Contains(verificationImageResponse.URL, "kaptcha_ticket=ticket-1") {
		t.Fatalf("expected verification image url to reflect fallback query, got %#v", verificationImageResponse)
	}
}
