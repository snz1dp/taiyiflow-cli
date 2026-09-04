package runtime

import (
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// ssoTokenToolDefinitions 返回 get_sso_token 工具的定义列表。
// 该工具供模型在构造服务端产物下载 URL 时获取最新的单点登录令牌。
func ssoTokenToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "get_sso_token",
			"description": "获取当前智能体的单点登录令牌（access_token）。令牌有时效性，每次需要认证时必须调用此工具获取最新令牌，不要缓存。返回的令牌可用于：1) 追加到下载 URL 的 __TOKEN__ query 参数；2) 设置 Authorization: Bearer <token> 请求头。",
			"inputSchema": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"required":             []string{},
				"additionalProperties": false,
			},
		},
	}
}

// isSsoTokenTool 判断工具名是否为 SSO 令牌相关工具。
func isSsoTokenTool(toolName string) bool {
	return toolName == "get_sso_token"
}

// ssoToken 从当前配置中读取 access_token 并返回结构化结果。
// 每次调用都实时读取配置，确保返回最新令牌。
// 若令牌为空（未登录或已过期），返回明确的错误提示。
func (r *ClientRuntime) ssoToken(config internalconfig.Config) (map[string]any, error) {
	accessToken := strings.TrimSpace(config.AccessToken)
	if accessToken == "" {
		return map[string]any{
			"success": false,
			"token":   "",
			"error":   "当前无有效的单点登录令牌，用户可能未登录或令牌已过期。请提示用户重新登录。",
		}, nil
	}
	return map[string]any{
		"success": true,
		"token":   accessToken,
		"usage_hint": "将此令牌追加到下载 URL 的 __TOKEN__ 参数（如 ?__TOKEN__=<token>），" +
			"或设置请求头 Authorization: Bearer <token>。" +
			"令牌有时效性，下次需要认证时请重新调用此工具。",
	}, nil
}
