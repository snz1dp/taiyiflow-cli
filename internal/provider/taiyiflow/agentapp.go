package taiyiflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// agentApp 相关 API 对应后端 /agents/agent-apps 路由（agent_app.py）。
// CLI 定位为只读消费 + 安装记录操作：插件包落盘安装由 Electron 宿主负责（P6.1），
// CLI 不下载 artifact，避免双写入方破坏 installed.json 单一写入方原则。

// agentAppResponse 对应后端 AgentAppRead 的 JSON 结构。
type agentAppResponse struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Icon        string         `json:"icon"`
	Version     string         `json:"version"`
	Manifest    map[string]any `json:"manifest"`
	FlowID      string         `json:"flow_id"`
	AccessScope string         `json:"access_scope"`
	UpdatedAt   string         `json:"updated_at"`
}

// agentAppPluginResponse 对应后端 AgentPluginRead 的 JSON 结构。
type agentAppPluginResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	Version      string `json:"version"`
	ArtifactURI  string `json:"artifact_uri"`
	ArtifactHash string `json:"artifact_hash"`
	Enabled      bool   `json:"enabled"`
	UpdatedAt    string `json:"updated_at"`
}

// agentAppPluginPageResponse 对应后端 AgentAppPluginPage 的 JSON 结构。
type agentAppPluginPageResponse struct {
	Data     []agentAppPluginResponse `json:"data"`
	Total    int                      `json:"total"`
	Offset   int                      `json:"offset"`
	Limit    int                      `json:"limit"`
	Warnings []string                 `json:"warnings"`
}

// agentAppInstallRecordResponse 对应后端 AgentAppInstallRecordRead 的 JSON 结构。
type agentAppInstallRecordResponse struct {
	ID             string `json:"id"`
	AgentAppID     string `json:"agent_app_id"`
	AppName        string `json:"app_name"`
	AppVersion     string `json:"app_version"`
	Status         string `json:"status"`
	Enabled        bool   `json:"enabled"`
	ClientPlatform string `json:"client_platform"`
	InstalledAt    string `json:"installed_at"`
	UninstalledAt  string `json:"uninstalled_at"`
}

// agentAppInstallRecordPageResponse 对应后端 AgentAppInstallRecordPage 的 JSON 结构。
type agentAppInstallRecordPageResponse struct {
	Data   []agentAppInstallRecordResponse `json:"data"`
	Total  int                             `json:"total"`
	Offset int                             `json:"offset"`
	Limit  int                             `json:"limit"`
}

// mapAgentApp 将响应结构转换为 provider.AgentApp。
func mapAgentApp(item agentAppResponse) provider.AgentApp {
	return provider.AgentApp{
		ID:          strings.TrimSpace(item.ID),
		Name:        strings.TrimSpace(item.Name),
		DisplayName: strings.TrimSpace(item.DisplayName),
		Description: strings.TrimSpace(item.Description),
		Icon:        strings.TrimSpace(item.Icon),
		Version:     strings.TrimSpace(item.Version),
		Manifest:    item.Manifest,
		FlowID:      strings.TrimSpace(item.FlowID),
		AccessScope: strings.TrimSpace(item.AccessScope),
		UpdatedAt:   strings.TrimSpace(item.UpdatedAt),
	}
}

// mapAgentAppPlugin 将响应结构转换为 provider.AgentAppPlugin。
func mapAgentAppPlugin(item agentAppPluginResponse) provider.AgentAppPlugin {
	return provider.AgentAppPlugin{
		ID:           strings.TrimSpace(item.ID),
		Name:         strings.TrimSpace(item.Name),
		DisplayName:  strings.TrimSpace(item.DisplayName),
		Description:  strings.TrimSpace(item.Description),
		Version:      strings.TrimSpace(item.Version),
		ArtifactURI:  strings.TrimSpace(item.ArtifactURI),
		ArtifactHash: strings.TrimSpace(item.ArtifactHash),
		Enabled:      item.Enabled,
		UpdatedAt:    strings.TrimSpace(item.UpdatedAt),
	}
}

// mapAgentAppInstallRecord 将响应结构转换为 provider.AgentAppInstallRecord。
func mapAgentAppInstallRecord(item agentAppInstallRecordResponse) provider.AgentAppInstallRecord {
	return provider.AgentAppInstallRecord{
		ID:             strings.TrimSpace(item.ID),
		AgentAppID:     strings.TrimSpace(item.AgentAppID),
		AppName:        strings.TrimSpace(item.AppName),
		AppVersion:     strings.TrimSpace(item.AppVersion),
		Status:         strings.TrimSpace(item.Status),
		Enabled:        item.Enabled,
		ClientPlatform: strings.TrimSpace(item.ClientPlatform),
		InstalledAt:    strings.TrimSpace(item.InstalledAt),
		UninstalledAt:  strings.TrimSpace(item.UninstalledAt),
	}
}

// ListAgentApps 查询当前用户可用的智能体应用列表（GET /agents/agent-apps）。
//
// installedOnly 为 true 时仅返回已安装应用；分页参数透传服务端。
func (c *Client) ListAgentApps(ctx context.Context, installedOnly bool, offset, limit int) ([]provider.AgentApp, error) {
	endpointURL, err := url.Parse(c.apiURL("/agents/agent-apps"))
	if err != nil {
		return nil, fmt.Errorf("构造智能体应用列表请求地址失败: %w", err)
	}
	query := endpointURL.Query()
	if installedOnly {
		query.Set("installed_only", "true")
	}
	query.Set("offset", fmt.Sprintf("%d", offset))
	query.Set("limit", fmt.Sprintf("%d", limit))
	endpointURL.RawQuery = query.Encode()

	payload, err := c.getAgentAppJSON(ctx, endpointURL.String(), "请求智能体应用列表失败")
	if err != nil {
		return nil, err
	}
	var items []agentAppResponse
	if err := json.Unmarshal(payload, &items); err != nil {
		return nil, fmt.Errorf("解析智能体应用列表响应失败: %w", err)
	}
	result := make([]provider.AgentApp, 0, len(items))
	for _, item := range items {
		result = append(result, mapAgentApp(item))
	}
	return result, nil
}

// ListAgentAppPlugins 查询应用 manifest 引用的插件列表（GET /agents/agent-apps/{id}/plugins）。
//
// agentAppID 可以是 UUID 或应用 name。返回含 artifact_uri/hash 下载信息与引用完整性告警。
func (c *Client) ListAgentAppPlugins(ctx context.Context, agentAppID string, offset, limit int) (*provider.AgentAppPluginPage, error) {
	path := fmt.Sprintf("/agents/agent-apps/%s/plugins", url.PathEscape(strings.TrimSpace(agentAppID)))
	endpointURL, err := url.Parse(c.apiURL(path))
	if err != nil {
		return nil, fmt.Errorf("构造应用插件列表请求地址失败: %w", err)
	}
	query := endpointURL.Query()
	query.Set("offset", fmt.Sprintf("%d", offset))
	query.Set("limit", fmt.Sprintf("%d", limit))
	endpointURL.RawQuery = query.Encode()

	payload, err := c.getAgentAppJSON(ctx, endpointURL.String(), "请求应用插件列表失败")
	if err != nil {
		return nil, err
	}
	var page agentAppPluginPageResponse
	if err := json.Unmarshal(payload, &page); err != nil {
		return nil, fmt.Errorf("解析应用插件列表响应失败: %w", err)
	}
	result := &provider.AgentAppPluginPage{
		Data:     make([]provider.AgentAppPlugin, 0, len(page.Data)),
		Total:    page.Total,
		Offset:   page.Offset,
		Limit:    page.Limit,
		Warnings: page.Warnings,
	}
	for _, item := range page.Data {
		result.Data = append(result.Data, mapAgentAppPlugin(item))
	}
	return result, nil
}

// ListAgentAppInstallRecords 分页查询当前用户的安装记录（GET /agents/agent-apps/installations）。
//
// statusFilter 为空时不过滤（installed/uninstalled 全量）。
func (c *Client) ListAgentAppInstallRecords(ctx context.Context, statusFilter string, offset, limit int) (*provider.AgentAppInstallRecordPage, error) {
	endpointURL, err := url.Parse(c.apiURL("/agents/agent-apps/installations"))
	if err != nil {
		return nil, fmt.Errorf("构造安装记录请求地址失败: %w", err)
	}
	query := endpointURL.Query()
	if trimmed := strings.TrimSpace(statusFilter); trimmed != "" {
		query.Set("status", trimmed)
	}
	query.Set("offset", fmt.Sprintf("%d", offset))
	query.Set("limit", fmt.Sprintf("%d", limit))
	endpointURL.RawQuery = query.Encode()

	payload, err := c.getAgentAppJSON(ctx, endpointURL.String(), "请求安装记录失败")
	if err != nil {
		return nil, err
	}
	var page agentAppInstallRecordPageResponse
	if err := json.Unmarshal(payload, &page); err != nil {
		return nil, fmt.Errorf("解析安装记录响应失败: %w", err)
	}
	result := &provider.AgentAppInstallRecordPage{
		Data:   make([]provider.AgentAppInstallRecord, 0, len(page.Data)),
		Total:  page.Total,
		Offset: page.Offset,
		Limit:  page.Limit,
	}
	for _, item := range page.Data {
		result.Data = append(result.Data, mapAgentAppInstallRecord(item))
	}
	return result, nil
}

// InstallAgentApp 安装智能体应用（POST /agents/agent-apps/{id}/install，幂等）。
//
// 服务端会级联安装 manifest 引用的插件记录（悬空引用仅告警不阻断）。
func (c *Client) InstallAgentApp(ctx context.Context, request provider.AgentAppInstallRequest) (*provider.AgentAppInstallRecord, error) {
	agentAppID := strings.TrimSpace(request.AgentAppID)
	path := fmt.Sprintf("/agents/agent-apps/%s/install", url.PathEscape(agentAppID))
	body := map[string]any{
		"agent_app_id":    agentAppID,
		"client_id":       strings.TrimSpace(request.ClientID),
		"device_id":       strings.TrimSpace(request.DeviceID),
		"client_platform": strings.TrimSpace(request.ClientPlatform),
		"client_version":  strings.TrimSpace(request.ClientVersion),
	}
	payload, err := c.postAgentAppJSON(ctx, c.apiURL(path), body, "安装智能体应用失败")
	if err != nil {
		return nil, err
	}
	var record agentAppInstallRecordResponse
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("解析安装响应失败: %w", err)
	}
	mapped := mapAgentAppInstallRecord(record)
	return &mapped, nil
}

// UninstallAgentApp 卸载智能体应用（POST /agents/agent-apps/{id}/uninstall，保留历史记录）。
func (c *Client) UninstallAgentApp(ctx context.Context, agentAppID string) (*provider.AgentAppInstallRecord, error) {
	path := fmt.Sprintf("/agents/agent-apps/%s/uninstall", url.PathEscape(strings.TrimSpace(agentAppID)))
	payload, err := c.postAgentAppJSON(ctx, c.apiURL(path), map[string]any{}, "卸载智能体应用失败")
	if err != nil {
		return nil, err
	}
	var record agentAppInstallRecordResponse
	if err := json.Unmarshal(payload, &record); err != nil {
		return nil, fmt.Errorf("解析卸载响应失败: %w", err)
	}
	mapped := mapAgentAppInstallRecord(record)
	return &mapped, nil
}

// getAgentAppJSON 发起带认证的 GET 请求并返回原始 JSON 字节。
func (c *Client) getAgentAppJSON(ctx context.Context, endpointURL string, errorPrefix string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpointURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	c.applyAuth(req)
	return c.doAgentAppRequest(req, errorPrefix)
}

// postAgentAppJSON 发起带认证的 POST 请求并返回原始 JSON 字节。
func (c *Client) postAgentAppJSON(ctx context.Context, endpointURL string, body map[string]any, errorPrefix string) ([]byte, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("构造请求体失败: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, strings.NewReader(string(encoded)))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	c.applyAuth(req)
	return c.doAgentAppRequest(req, errorPrefix)
}

// doAgentAppRequest 执行请求并统一处理状态码与响应读取。
func (c *Client) doAgentAppRequest(req *http.Request, errorPrefix string) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", errorPrefix, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError(errorPrefix, resp)
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	return payload, nil
}
