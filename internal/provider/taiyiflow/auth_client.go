package taiyiflow

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const authContextPath = "/xeai"

const (
	authCodeVerifyChallengeRequired = 404
	authCodeVerifyCodeRequired      = 405
	authCodeVerifyCodeInvalid       = 408
)

type AuthError struct {
	Operation       string
	StatusCode      int
	Code            int
	Message         string
	VerifyChallenge *VerifyCodeToken
}

func (e *AuthError) IsVerificationRequired() bool {
	if e == nil {
		return false
	}
	if e.VerifyChallenge != nil {
		return true
	}
	switch e.Code {
	case authCodeVerifyChallengeRequired, authCodeVerifyCodeRequired, authCodeVerifyCodeInvalid:
		return true
	default:
		return false
	}
}

func (e *AuthError) ShouldResetVerificationCode() bool {
	if e == nil {
		return false
	}
	if e.VerifyChallenge != nil {
		return true
	}
	return e.Code == authCodeVerifyCodeInvalid
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
	PasswordTicket string `json:"ticket"`
	PubKey         string `json:"pubkey"`
}

type PasswordLoginRequest struct {
	VerifyTicket   string
	PasswordTicket string
	PublicKey      string
	Username       string
	Password       string
	Code           string
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

type VerificationImage struct {
	ContentType string
	Data        []byte
	URL         string
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
	payload.PasswordTicket = strings.TrimSpace(payload.PasswordTicket)
	payload.PubKey = strings.TrimSpace(payload.PubKey)
	if payload.PubKey == "" {
		return nil, &AuthError{Operation: "获取登录公钥", Message: "登录公钥为空"}
	}
	if payload.PasswordTicket == "" {
		return nil, &AuthError{Operation: "获取登录公钥", Message: "password_ticket 不能为空"}
	}
	return &payload, nil
}

func (t *PasswordLoginToken) EffectivePasswordTicket() string {
	if t == nil {
		return ""
	}
	if trimmed := strings.TrimSpace(t.PasswordTicket); trimmed != "" {
		return trimmed
	}
	return ""
}

func (c *AuthClient) PasswordLogin(ctx context.Context, request PasswordLoginRequest) (*AuthSession, error) {
	trimmedTicket := strings.TrimSpace(request.VerifyTicket)
	trimmedPasswordTicket := strings.TrimSpace(request.PasswordTicket)
	trimmedUsername := strings.TrimSpace(request.Username)
	if trimmedUsername == "" {
		return nil, &AuthError{Operation: "用户名密码登录", Message: "用户名不能为空"}
	}
	if trimmedPasswordTicket == "" {
		return nil, &AuthError{Operation: "用户名密码登录", Message: "请先获取登录公钥"}
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
	values.Set("password_ticket", trimmedPasswordTicket)
	values.Set("username", trimmedUsername)
	values.Set("password0", encryptedPassword)
	if trimmed := strings.TrimSpace(request.Code); trimmed != "" {
		values.Set("code", trimmed)
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

func (c *AuthClient) ValidateVerifyCode(ctx context.Context, username, verifyTicket, passwordTicket, verifyType, verifyCode, verifyID string) error {
	trimmedVerifyTicket := strings.TrimSpace(verifyTicket)
	trimmedPasswordTicket := strings.TrimSpace(passwordTicket)
	if trimmedPasswordTicket == "" {
		trimmedPasswordTicket = trimmedVerifyTicket
	}
	values := url.Values{}
	values.Set("username", strings.TrimSpace(username))
	values.Set("verify_ticket", trimmedVerifyTicket)
	values.Set("password_ticket", trimmedPasswordTicket)
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

func (c *AuthClient) GetChannelNames(ctx context.Context) ([]string, error) {
	var payload string
	if err := c.doEnvelopeRequest(ctx, http.MethodGet, "/oauth/api/channel_names", nil, "", &payload); err != nil {
		return nil, err
	}
	return splitChannelNames(payload), nil
}

func (c *AuthClient) SupportsQRCodeLogin(ctx context.Context) (bool, error) {
	channels, err := c.GetChannelNames(ctx)
	if err != nil {
		return false, err
	}
	return hasQRCodeOAuthChannel(channels), nil
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

func splitChannelNames(value string) []string {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return nil
	}
	parts := strings.FieldsFunc(trimmedValue, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；', '|', '/', '\\', '\n', '\r', '\t':
			return true
		default:
			return false
		}
	})
	if len(parts) == 0 {
		return []string{trimmedValue}
	}
	seen := make(map[string]struct{}, len(parts))
	channels := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmedPart := strings.TrimSpace(part)
		if trimmedPart == "" {
			continue
		}
		normalizedPart := strings.ToLower(trimmedPart)
		if _, exists := seen[normalizedPart]; exists {
			continue
		}
		seen[normalizedPart] = struct{}{}
		channels = append(channels, trimmedPart)
	}
	return channels
}

func hasQRCodeOAuthChannel(channels []string) bool {
	for _, channel := range channels {
		if strings.TrimSpace(channel) != "" {
			return true
		}
	}
	return false
}

func (c *AuthClient) GetVerificationImage(ctx context.Context, ticket string) (*VerificationImage, error) {
	trimmedTicket := strings.TrimSpace(ticket)
	if trimmedTicket == "" {
		return nil, nil
	}

	type imageCandidate struct {
		path  string
		query url.Values
	}

	candidates := []imageCandidate{
		{path: "/kaptcha/image", query: url.Values{"ticket": []string{trimmedTicket}}},
		{path: "/kaptcha/image", query: url.Values{"kaptcha_ticket": []string{trimmedTicket}}},
		{path: "/kaptcha/render", query: url.Values{"ticket": []string{trimmedTicket}}},
		{path: "/kaptcha", query: url.Values{"ticket": []string{trimmedTicket}}},
	}

	var lastErr error
	for _, candidate := range candidates {
		image, err := c.fetchVerificationImageCandidate(ctx, candidate.path, candidate.query)
		if err == nil && image != nil {
			return image, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	return nil, lastErr
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

func (c *AuthClient) fetchVerificationImageCandidate(ctx context.Context, path string, query url.Values) (*VerificationImage, error) {
	endpoint, err := c.authURL(path, query)
	if err != nil {
		return nil, &AuthError{Operation: "获取图形验证码", Message: err.Error()}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, &AuthError{Operation: "获取图形验证码", Message: err.Error()}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &AuthError{Operation: "获取图形验证码", Message: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readAuthHTTPError(path, resp)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &AuthError{Operation: "获取图形验证码", StatusCode: resp.StatusCode, Message: fmt.Sprintf("读取验证码图片失败: %v", err)}
	}
	if len(data) == 0 {
		return nil, nil
	}

	contentType := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if contentType == "" {
		contentType = strings.ToLower(http.DetectContentType(data))
	}
	if !strings.HasPrefix(contentType, "image/") {
		return nil, nil
	}

	return &VerificationImage{ContentType: contentType, Data: data, URL: endpoint}, nil
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
		if target != nil {
			_ = json.Unmarshal(payload.Data, target)
		}
		return buildAuthEnvelopeError(operation, resp.StatusCode, payload, "认证请求失败")
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(payload.Data, target); err != nil {
		return &AuthError{Operation: operation, StatusCode: resp.StatusCode, Message: fmt.Sprintf("解析认证 data 失败: %v", err)}
	}
	return nil
}

func buildAuthEnvelopeError(operation string, statusCode int, payload authEnvelope[json.RawMessage], fallback string) *AuthError {
	authErr := &AuthError{
		Operation:       operation,
		StatusCode:      statusCode,
		Code:            payload.Code,
		Message:         envelopeMessage(payload.Message, fallback),
		VerifyChallenge: decodeVerifyChallengePayload(payload.Data),
	}
	return authErr
}

func decodeVerifyChallengePayload(data json.RawMessage) *VerifyCodeToken {
	trimmedData := bytes.TrimSpace(data)
	if len(trimmedData) == 0 || bytes.Equal(trimmedData, []byte("null")) {
		return nil
	}
	var challenge VerifyCodeToken
	if err := json.Unmarshal(trimmedData, &challenge); err != nil {
		return nil
	}
	challenge.Ticket = strings.TrimSpace(challenge.Ticket)
	challenge.Hint = strings.TrimSpace(challenge.Hint)
	challenge.Type = strings.TrimSpace(challenge.Type)
	challenge.Target = strings.TrimSpace(challenge.Target)
	if challenge.Ticket == "" && challenge.Hint == "" && challenge.Type == "" && challenge.Target == "" && challenge.Interval == 0 {
		return nil
	}
	return &challenge
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
		return buildAuthEnvelopeError(operation, resp.StatusCode, payload, resp.Status)
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
