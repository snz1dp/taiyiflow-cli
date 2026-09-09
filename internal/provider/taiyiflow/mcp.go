package taiyiflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// mcpServerRaw 表示后端返回的 MCP 服务器原始对象。
// 字段与 taiyiflow-chatui 的 MCPServer 接口保持一致。
type mcpServerRaw struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	ServerURL        string         `json:"server_url"`
	ServerType       string         `json:"server_type"`
	Description      string         `json:"description,omitempty"`
	AuthConfig       map[string]any `json:"auth_config,omitempty"`
	PassUserIdentity bool           `json:"pass_user_identity,omitempty"`
	IsSystem         bool           `json:"is_system,omitempty"`
	OwnerUserID      string         `json:"owner_user_id,omitempty"`
	ViaClient        bool           `json:"via_client,omitempty"`
	ClientType       string         `json:"client_type,omitempty"`
	Enabled          *bool          `json:"enabled,omitempty"`
	FlowID           string         `json:"flow_id,omitempty"`
	Tools            []mcpToolRaw   `json:"tools,omitempty"`
}

// mcpToolRaw 表示后端返回的工具定义原始对象。
type mcpToolRaw struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
	Enabled     *bool          `json:"enabled,omitempty"`
}

// toSummary 将原始服务器对象转换为 provider.MCPServerSummary。
func (s *mcpServerRaw) toSummary() provider.MCPServerSummary {
	summary := provider.MCPServerSummary{
		ID:               s.ID,
		Name:             s.Name,
		ServerURL:        s.ServerURL,
		ServerType:       s.ServerType,
		Description:      s.Description,
		AuthConfig:       s.AuthConfig,
		PassUserIdentity: s.PassUserIdentity,
		IsSystem:         s.IsSystem,
		OwnerUserID:      s.OwnerUserID,
		Enabled:          s.Enabled,
		FlowID:           s.FlowID,
	}
	for _, tool := range s.Tools {
		summary.Tools = append(summary.Tools, provider.MCPToolSummary{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
			Enabled:     tool.Enabled,
		})
	}
	return summary
}

// ListMCPServers 获取指定 flow 下的 MCP 服务器列表。
// clientType 用于标识客户端类型，CLI 传 "cli"。
func (c *Client) ListMCPServers(ctx context.Context, flowID string, clientType string) ([]provider.MCPServerSummary, error) {
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return nil, fmt.Errorf("获取 MCP 服务器列表失败: flow_id 不能为空")
	}

	endpoint, err := url.Parse(c.apiURL("/agents/mcp/servers"))
	if err != nil {
		return nil, fmt.Errorf("构造 MCP 服务器列表请求地址失败: %w", err)
	}
	query := endpoint.Query()
	query.Set("flow_id", flowID)
	if strings.TrimSpace(clientType) != "" {
		query.Set("client_type", strings.TrimSpace(clientType))
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取 MCP 服务器列表失败", resp)
	}

	var rawServers []mcpServerRaw
	if err := json.NewDecoder(resp.Body).Decode(&rawServers); err != nil {
		return nil, fmt.Errorf("解析 MCP 服务器列表响应失败: %w", err)
	}

	servers := make([]provider.MCPServerSummary, 0, len(rawServers))
	for i := range rawServers {
		servers = append(servers, rawServers[i].toSummary())
	}
	return servers, nil
}

// CreateMCPServer 创建用户自建 MCP 服务器。
func (c *Client) CreateMCPServer(ctx context.Context, request provider.MCPServerCreateRequest) (*provider.MCPServerSummary, error) {
	content, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("序列化创建 MCP 服务器请求失败: %w", err)
	}

	endpoint := c.apiURL("/agents/mcp/servers")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("创建 MCP 服务器失败", resp)
	}

	var raw mcpServerRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析创建 MCP 服务器响应失败: %w", err)
	}
	summary := raw.toSummary()
	return &summary, nil
}

// UpdateMCPServer 编辑用户自建 MCP 服务器。
func (c *Client) UpdateMCPServer(ctx context.Context, serverID string, request provider.MCPServerUpdateRequest) (*provider.MCPServerSummary, error) {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return nil, fmt.Errorf("编辑 MCP 服务器失败: server_id 不能为空")
	}

	content, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("序列化编辑 MCP 服务器请求失败: %w", err)
	}

	endpoint := c.apiURL(fmt.Sprintf("/agents/mcp/servers/%s", url.PathEscape(serverID)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("编辑 MCP 服务器失败", resp)
	}

	var raw mcpServerRaw
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析编辑 MCP 服务器响应失败: %w", err)
	}
	summary := raw.toSummary()
	return &summary, nil
}

// DeleteMCPServer 删除用户自建 MCP 服务器。
func (c *Client) DeleteMCPServer(ctx context.Context, serverID string) error {
	serverID = strings.TrimSpace(serverID)
	if serverID == "" {
		return fmt.Errorf("删除 MCP 服务器失败: server_id 不能为空")
	}

	endpoint := c.apiURL(fmt.Sprintf("/agents/mcp/servers/%s", url.PathEscape(serverID)))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return readHTTPError("删除 MCP 服务器失败", resp)
	}
	return nil
}
