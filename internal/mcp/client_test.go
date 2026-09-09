package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestApplyAuthHeadersPassUserIdentity 验证 pass_user_identity 场景下的用户令牌注入逻辑，
// 与 taiyiflow-chatui mcpClient.js 的 buildAuthHeaders 行为对齐：
// 1. PassUserIdentity=true 且未显式配置 Authorization 时注入 Bearer 令牌
// 2. auth_config 显式配置 bearer token 时不覆盖
// 3. PassUserIdentity=false 时不注入
// 4. 令牌为空时不注入
func TestApplyAuthHeadersPassUserIdentity(t *testing.T) {
	staticToken := func(token string) func() string {
		return func() string { return token }
	}

	tests := []struct {
		name     string
		server   ServerConfig
		wantAuth string
	}{
		{
			name: "pass_user_identity 注入用户令牌",
			server: ServerConfig{
				PassUserIdentity:          true,
				UserIdentityTokenProvider: staticToken("user-token-1"),
			},
			wantAuth: "Bearer user-token-1",
		},
		{
			name: "auth_config bearer 优先，不覆盖",
			server: ServerConfig{
				AuthConfig:                map[string]any{"type": "bearer", "token": "svc-token"},
				PassUserIdentity:          true,
				UserIdentityTokenProvider: staticToken("user-token-1"),
			},
			wantAuth: "Bearer svc-token",
		},
		{
			name: "pass_user_identity=false 不注入",
			server: ServerConfig{
				UserIdentityTokenProvider: staticToken("user-token-1"),
			},
			wantAuth: "",
		},
		{
			name: "令牌为空不注入",
			server: ServerConfig{
				PassUserIdentity: true,
			},
			wantAuth: "",
		},
		{
			name: "api_key 与用户令牌共存",
			server: ServerConfig{
				AuthConfig:                map[string]any{"type": "api_key", "key": "k1"},
				PassUserIdentity:          true,
				UserIdentityTokenProvider: staticToken("user-token-1"),
			},
			wantAuth: "Bearer user-token-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "http://example.com/mcp", nil)
			if err != nil {
				t.Fatalf("构造请求失败: %v", err)
			}
			applyAuthHeaders(req, tt.server)
			if got := req.Header.Get("Authorization"); got != tt.wantAuth {
				t.Fatalf("Authorization = %q, want %q", got, tt.wantAuth)
			}
			if tt.name == "api_key 与用户令牌共存" {
				if got := req.Header.Get("X-API-Key"); got != "k1" {
					t.Fatalf("X-API-Key = %q, want %q", got, "k1")
				}
			}
		})
	}
}

// TestApplyAuthHeadersUsesRefreshedToken 验证令牌刷新场景：
// provider 每次请求时实时解析，令牌被刷新后新请求应携带新令牌，
// 与 callbackAuthRefreshProvider 自动刷新登录后的行为对齐。
func TestApplyAuthHeadersUsesRefreshedToken(t *testing.T) {
	currentToken := "token-v1"
	server := ServerConfig{
		PassUserIdentity:          true,
		UserIdentityTokenProvider: func() string { return currentToken },
	}

	newRequest := func() *http.Request {
		req, err := http.NewRequest(http.MethodPost, "http://example.com/mcp", nil)
		if err != nil {
			t.Fatalf("构造请求失败: %v", err)
		}
		return req
	}

	req1 := newRequest()
	applyAuthHeaders(req1, server)
	if got := req1.Header.Get("Authorization"); got != "Bearer token-v1" {
		t.Fatalf("刷新前 Authorization = %q, want %q", got, "Bearer token-v1")
	}

	// 模拟自动刷新登录：config.AccessToken 被原地更新
	currentToken = "token-v2"

	req2 := newRequest()
	applyAuthHeaders(req2, server)
	if got := req2.Header.Get("Authorization"); got != "Bearer token-v2" {
		t.Fatalf("刷新后 Authorization = %q, want %q", got, "Bearer token-v2")
	}
}

// TestCallToolRefreshesTokenOnAuthError 验证 streamable_http 工具调用在收到 401 时，
// 触发 UserIdentityRefresh 刷新登录并用新令牌重试一次后成功。
// 覆盖“仅 MCP 服务认为令牌过期、后端尚未触发自动刷新”的场景。
func TestCallToolRefreshesTokenOnAuthError(t *testing.T) {
	var requests atomic.Int32
	var sawRefreshedToken atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		auth := r.Header.Get("Authorization")
		if n == 1 {
			// 首次调用：旧令牌被拒绝
			if auth != "Bearer token-v1" {
				t.Errorf("首次请求 Authorization = %q, want %q", auth, "Bearer token-v1")
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"token expired"}`))
			return
		}
		// 重试：应携带刷新后的新令牌
		if auth == "Bearer token-v2" {
			sawRefreshedToken.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))
	defer srv.Close()

	currentToken := "token-v1"
	var refreshCalls atomic.Int32
	server := ServerConfig{
		Name:                      "test-server",
		ServerURL:                 srv.URL,
		ServerType:                "streamable_http",
		PassUserIdentity:          true,
		UserIdentityTokenProvider: func() string { return currentToken },
		UserIdentityRefresh: func(ctx context.Context) bool {
			refreshCalls.Add(1)
			currentToken = "token-v2" // 模拟刷新登录后 access_token 被更新
			return true
		},
	}

	client := NewClient()
	defer client.Close()

	result, err := client.CallTool(context.Background(), server, "echo", map[string]any{"msg": "hi"})
	if err != nil {
		t.Fatalf("CallTool 返回错误: %v", err)
	}
	if result != "ok" {
		t.Fatalf("CallTool 结果 = %v, want %q", result, "ok")
	}
	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("刷新调用次数 = %d, want 1", got)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("服务器收到请求数 = %d, want 2", got)
	}
	if !sawRefreshedToken.Load() {
		t.Fatalf("重试请求未携带刷新后的令牌 token-v2")
	}
}

// TestCallToolNoRetryWhenRefreshFails 验证刷新失败（返回 false）时不再重试，
// 直接返回原始鉴权错误，避免用旧令牌做无意义的二次请求。
func TestCallToolNoRetryWhenRefreshFails(t *testing.T) {
	var requests atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"token expired"}`))
	}))
	defer srv.Close()

	server := ServerConfig{
		Name:                      "test-server",
		ServerURL:                 srv.URL,
		ServerType:                "streamable_http",
		PassUserIdentity:          true,
		UserIdentityTokenProvider: func() string { return "token-v1" },
		UserIdentityRefresh:       func(ctx context.Context) bool { return false },
	}

	client := NewClient()
	defer client.Close()

	_, err := client.CallTool(context.Background(), server, "echo", map[string]any{})
	if err == nil {
		t.Fatalf("CallTool 应返回鉴权错误")
	}
	var authErr *authRequiredError
	if !errors.As(err, &authErr) {
		t.Fatalf("错误类型 = %T, want *authRequiredError", err)
	}
	if authErr.statusCode != http.StatusUnauthorized {
		t.Fatalf("statusCode = %d, want 401", authErr.statusCode)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("服务器收到请求数 = %d, want 1（刷新失败不应重试）", got)
	}
}

// TestCallToolNoRefreshWithoutPassUserIdentity 验证未配置 pass_user_identity 的服务
// 收到 401 时不触发刷新（UserIdentityRefresh 为 nil），直接返回错误。
func TestCallToolNoRefreshWithoutPassUserIdentity(t *testing.T) {
	var requests atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer srv.Close()

	server := ServerConfig{
		Name:       "test-server",
		ServerURL:  srv.URL,
		ServerType: "streamable_http",
		// PassUserIdentity=false，UserIdentityRefresh=nil
	}

	client := NewClient()
	defer client.Close()

	_, err := client.CallTool(context.Background(), server, "echo", map[string]any{})
	if err == nil {
		t.Fatalf("CallTool 应返回鉴权错误")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("服务器收到请求数 = %d, want 1（无刷新能力不应重试）", got)
	}
}
