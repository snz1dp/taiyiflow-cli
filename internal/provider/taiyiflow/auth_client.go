package taiyiflow

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const authContextPath = "/xeai"

type AuthError struct {
	Operation  string
	StatusCode int
	Code       int
	Message    string
}

func (e *AuthError) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "认证请求失败"
	}
	parts := []string{}
	if strings.TrimSpace(e.Operation) != "" {
		parts = append(parts, strings.TrimSpace(e.Operation))
	}
	parts = append(parts, message)
	if e.StatusCode > 0 {
		parts = append(parts, fmt.Sprintf("HTTP %d", e.StatusCode))
	}
	if e.Code != 0 {
		parts = append(parts, fmt.Sprintf("code=%d", e.Code))
	}
	return strings.Join(parts, ": ")
}

type AuthClient struct {
	baseURL    string
	httpClient *http.Client
}

type authEnvelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type TokenPair struct {
	Ticket string `json:"ticket"`
	Value  string `json:"value"`
}

type AuthSession struct {
	UserID          string `json:"userid"`
	AccessToken     string `json:"access_token"`
	PersistentToken string `json:"persistent_token"`
	IdleDuration    int    `json:"idle_duration"`
	Scope           string `json:"scope"`
}

func (s *AuthSession) UnmarshalJSON(data []byte) error {
	type authSessionAlias AuthSession

	var accessToken string
	if err := json.Unmarshal(data, &accessToken); err == nil {
		s.AccessToken = strings.TrimSpace(accessToken)
		return nil
	}

	var alias authSessionAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	*s = AuthSession(alias)
	return nil
}

type authUserInfo struct {
	UserID       string `json:"userid"`
	AccountName  string `json:"account_name"`
	DisplayName  string `json:"display_name"`
	Scope        string `json:"scope"`
	AuthMode     string `json:"auth_mode"`
	Code         string `json:"code"`
	RegistEmail  string `json:"regist_email"`
	RegistMobile string `json:"regist_mobile"`
}

type PersistentLoginToken struct {
	Ticket   string       `json:"ticket"`
	UserInfo authUserInfo `json:"userinfo"`
}

type PasswordLoginToken struct {
	Ticket string `json:"ticket"`
	PubKey string `json:"pubkey"`
}

type PasswordLoginRequest struct {
	Ticket        string
	PublicKey     string
	Username      string
	Password      string
	KaptchaTicket string
	KaptchaCode   string
}

type VerifyCodeToken struct {
	Ticket   string `json:"ticket"`
	Hint     string `json:"hint"`
	Type     string `json:"type"`
	Target   string `json:"target"`
	Interval int    `json:"interval"`
}

type VerifyCodeRequestLoginToken struct {
	Ticket      string `json:"ticket"`
	Hint        string `json:"hint"`
	Type        string `json:"type"`
	Target      string `json:"target"`
	Interval    int    `json:"interval"`
	UserExisted bool   `json:"user_existed"`
	OtherUser   bool   `json:"other_user"`
}

type RedirectLoginToken struct {
	Ticket   string `json:"ticket"`
	FireURL  string `json:"fireurl"`
	ScanHint string `json:"scanhint"`
}

type WaitEvent struct {
	UserID       string `json:"userid"`
	AccessToken  string `json:"access_token"`
	IdleDuration int    `json:"idle_duration"`
	Scope        string `json:"scope"`
	State        int    `json:"state"`
}

type KaptchaToken struct {
	Ticket  string `json:"ticket"`
	Expired int    `json:"expired"`
}

func NewAuthClient(baseURL string) *AuthClient {
	return &AuthClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *AuthClient) GetPasswordLoginToken(ctx context.Context) (*PasswordLoginToken, error) {
	var payload PasswordLoginToken
	if err := c.doEnvelopeRequest(ctx, http.MethodGet, "/oauth/api/password_token", nil, "", &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *AuthClient) PasswordLogin(ctx context.Context, request PasswordLoginRequest) (*AuthSession, error) {
	trimmedTicket := strings.TrimSpace(request.Ticket)
	trimmedUsername := strings.TrimSpace(request.Username)
	if trimmedTicket == "" {
		return nil, &AuthError{Operation: "用户名密码登录", Message: "验证码不能为空"}
	}
	if trimmedUsername == "" {
		return nil, &AuthError{Operation: "用户名密码登录", Message: "用户名不能为空"}
	}
	if strings.TrimSpace(request.Password) == "" {
		return nil, &AuthError{Operation: "用户名密码登录", Message: "密码不能为空"}
	}

	encryptedPassword, err := encryptPasswordWithPublicKey(request.PublicKey, request.Password)
	if err != nil {
		return nil, &AuthError{Operation: "用户名密码登录", Message: err.Error()}
	}

	values := url.Values{}
	values.Set("ticket", trimmedTicket)
	values.Set("password_ticket", trimmedTicket)
	values.Set("username", trimmedUsername)
	values.Set("password0", encryptedPassword)
	values.Set("alg", "RSA")
	if trimmed := strings.TrimSpace(request.KaptchaTicket); trimmed != "" {
		values.Set("kaptcha_ticket", trimmed)
	}
	if trimmed := strings.TrimSpace(request.KaptchaCode); trimmed != "" {
		values.Set("kaptcha_code", trimmed)
	}

	var payload AuthSession
	if err := c.doEnvelopeRequest(ctx, http.MethodPost, "/ajax_logon", values, "", &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *AuthClient) SendVerifyCode(ctx context.Context, username, verifyType, verifyID string) (*VerifyCodeToken, error) {
	values := url.Values{}
	values.Set("username", strings.TrimSpace(username))
	values.Set("verify_type", strings.TrimSpace(verifyType))
	if trimmed := strings.TrimSpace(verifyID); trimmed != "" {
		values.Set("verify_id", trimmed)
	}
	var payload VerifyCodeToken
	if err := c.doEnvelopeRequest(ctx, http.MethodPost, "/send_verify", values, "", &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *AuthClient) ValidateVerifyCode(ctx context.Context, username, verifyTicket, verifyType, verifyCode, verifyID string) error {
	values := url.Values{}
	values.Set("username", strings.TrimSpace(username))
	values.Set("verify_ticket", strings.TrimSpace(verifyTicket))
	values.Set("verify_type", strings.TrimSpace(verifyType))
	values.Set("verify_code", strings.TrimSpace(verifyCode))
	values.Set("verify_id", strings.TrimSpace(verifyID))
	return c.doEnvelopeRequest(ctx, http.MethodPost, "/send_verify/validate", values, "", nil)
}

func (c *AuthClient) RequestVerifyLoginToken(ctx context.Context, verifyType, verifyID string) (*VerifyCodeRequestLoginToken, error) {
	values := url.Values{}
	values.Set("verify_type", strings.TrimSpace(verifyType))
	values.Set("verify_id", strings.TrimSpace(verifyID))
	var payload VerifyCodeRequestLoginToken
	if err := c.doEnvelopeRequest(ctx, http.MethodPost, "/ajax_logon/verify_token", values, "", &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *AuthClient) CompleteVerifyLogin(ctx context.Context, ticket, verify string) (*AuthSession, error) {
	values := url.Values{}
	values.Set("ticket", strings.TrimSpace(ticket))
	values.Set("verify", strings.TrimSpace(verify))
	var payload AuthSession
	if err := c.doEnvelopeRequest(ctx, http.MethodPost, "/ajax_logon/verify_login", values, "", &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *AuthClient) GetQRCodeLoginToken(ctx context.Context, platform string, silent bool, tokenType string, redirectURL string) (*RedirectLoginToken, error) {
	values := url.Values{}
	if trimmed := strings.TrimSpace(platform); trimmed != "" {
		values.Set("platform", trimmed)
	}
	values.Set("silent", fmt.Sprintf("%t", silent))
	if trimmed := strings.TrimSpace(tokenType); trimmed != "" {
		values.Set("type", trimmed)
	}
	if trimmed := strings.TrimSpace(redirectURL); trimmed != "" {
		values.Set("redirect_url", trimmed)
	}
	var payload RedirectLoginToken
	if err := c.doEnvelopeRequest(ctx, http.MethodGet, "/oauth/api/token", values, "", &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *AuthClient) WaitQRCodeLoginEvent(ctx context.Context, ticket string) (*WaitEvent, error) {
	values := url.Values{}
	values.Set("ticket", strings.TrimSpace(ticket))
	var payload WaitEvent
	if err := c.doEnvelopeRequest(ctx, http.MethodGet, "/oauth/api/wait_event", values, "", &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *AuthClient) GetKaptchaTicket(ctx context.Context) (*KaptchaToken, error) {
	var payload KaptchaToken
	if err := c.doEnvelopeRequest(ctx, http.MethodGet, "/kaptcha/ticket", nil, "", &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *AuthClient) VerifyKaptcha(ctx context.Context, ticket, code string) error {
	values := url.Values{}
	values.Set("ticket", strings.TrimSpace(ticket))
	values.Set("code", strings.TrimSpace(code))
	return c.doEnvelopeRequest(ctx, http.MethodGet, "/kaptcha/verify", values, "", nil)
}

func (c *AuthClient) ExchangePersistentToken(ctx context.Context, persistentToken string, deviceID string) (*AuthSession, error) {
	trimmedPersistentToken := strings.TrimSpace(persistentToken)
	trimmedDeviceID := strings.TrimSpace(deviceID)
	if trimmedPersistentToken == "" {
		return nil, &AuthError{Operation: "持久令牌自动登录", Message: "persistent_token 不能为空"}
	}
	if trimmedDeviceID == "" {
		return nil, &AuthError{Operation: "持久令牌自动登录", Message: "device_id 不能为空"}
	}

	values := url.Values{}
	values.Set("persistent_token", trimmedPersistentToken)
	var saltPair TokenPair
	if err := c.doEnvelopeRequest(ctx, http.MethodGet, "/sso/salt", values, "", &saltPair); err != nil {
		return nil, err
	}

	loginValues := url.Values{}
	loginValues.Set("persistent_token", trimmedPersistentToken)
	loginValues.Set("salt_ticket", strings.TrimSpace(saltPair.Ticket))
	loginValues.Set("sign", buildPersistentLoginSignature(trimmedDeviceID, saltPair.Value))
	loginValues.Set("alg", "SHA-256")

	var session AuthSession
	if err := c.doEnvelopeRequest(ctx, http.MethodPost, "/sso", loginValues, "", &session); err != nil {
		return nil, err
	}
	return &session, nil
}

func (c *AuthClient) CreatePersistentToken(ctx context.Context, accessToken string, deviceID string) (*PersistentLoginToken, error) {
	values := url.Values{}
	values.Set("device_id", strings.TrimSpace(deviceID))
	var payload PersistentLoginToken
	if err := c.doEnvelopeRequest(ctx, http.MethodPost, "/logged/persistent_token", values, strings.TrimSpace(accessToken), &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

func (c *AuthClient) doEnvelopeRequest(ctx context.Context, method string, path string, query url.Values, accessToken string, target any) error {
	endpoint, err := c.authURL(path, query)
	if err != nil {
		return &AuthError{Operation: "认证请求", Message: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return &AuthError{Operation: "认证请求", Message: err.Error()}
	}
	if trimmed := strings.TrimSpace(accessToken); trimmed != "" {
		req.Header.Set("Authorization", "Bearer "+trimmed)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &AuthError{Operation: "认证请求", Message: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return readAuthHTTPError(path, resp)
	}

	if err := decodeAuthEnvelope(resp, path, target); err != nil {
		return err
	}
	return nil
}

func (c *AuthClient) authURL(path string, query url.Values) (string, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		trimmedPath = "/"
	}
	if !strings.HasPrefix(trimmedPath, "/") {
		trimmedPath = "/" + trimmedPath
	}
	endpoint, err := url.Parse(c.baseURL + authContextPath + trimmedPath)
	if err != nil {
		return "", err
	}
	if query != nil {
		endpoint.RawQuery = query.Encode()
	}
	return endpoint.String(), nil
}

func decodeAuthEnvelope(resp *http.Response, operation string, target any) error {
	var payload authEnvelope[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return &AuthError{Operation: operation, StatusCode: resp.StatusCode, Message: fmt.Sprintf("解析认证响应失败: %v", err)}
	}
	if payload.Code != 0 {
		return &AuthError{Operation: operation, StatusCode: resp.StatusCode, Code: payload.Code, Message: envelopeMessage(payload.Message, "认证请求失败")}
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(payload.Data, target); err != nil {
		return &AuthError{Operation: operation, StatusCode: resp.StatusCode, Message: fmt.Sprintf("解析认证 data 失败: %v", err)}
	}
	return nil
}

func buildPersistentLoginSignature(deviceID string, salt string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(deviceID) + strings.TrimSpace(salt)))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func encryptPasswordWithPublicKey(publicKey string, password string) (string, error) {
	rsaPublicKey, err := parseLoginPublicKey(publicKey)
	if err != nil {
		return "", err
	}
	encrypted, err := rsa.EncryptPKCS1v15(cryptorand.Reader, rsaPublicKey, []byte(password))
	if err != nil {
		return "", fmt.Errorf("加密密码失败: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

func parseLoginPublicKey(value string) (*rsa.PublicKey, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("登录公钥为空")
	}

	derBytes := []byte(trimmed)
	if block, _ := pem.Decode([]byte(trimmed)); block != nil {
		derBytes = block.Bytes
	} else if decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(trimmed, "\n", "")); err == nil {
		derBytes = decoded
	}

	if parsed, err := x509.ParsePKIXPublicKey(derBytes); err == nil {
		if publicKey, ok := parsed.(*rsa.PublicKey); ok {
			return publicKey, nil
		}
	}
	if publicKey, err := x509.ParsePKCS1PublicKey(derBytes); err == nil {
		return publicKey, nil
	}
	return nil, fmt.Errorf("解析登录公钥失败")
}

func readAuthHTTPError(operation string, resp *http.Response) error {
	var payload authEnvelope[json.RawMessage]
	if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
		return &AuthError{
			Operation:  operation,
			StatusCode: resp.StatusCode,
			Code:       payload.Code,
			Message:    envelopeMessage(payload.Message, resp.Status),
		}
	}
	return &AuthError{
		Operation:  operation,
		StatusCode: resp.StatusCode,
		Message:    strings.TrimSpace(resp.Status),
	}
}

func envelopeMessage(message string, fallback string) string {
	trimmedMessage := strings.TrimSpace(message)
	if trimmedMessage != "" {
		return trimmedMessage
	}
	return strings.TrimSpace(fallback)
}
