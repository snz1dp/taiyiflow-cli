package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
)

// DefaultHTTPTimeout check-update / report-status 请求超时。
const DefaultHTTPTimeout = 30 * time.Second

// DefaultDownloadTimeout 下载超时。
const DefaultDownloadTimeout = 10 * time.Minute

// ClientGroup CLI 客户端分组标识。
const ClientGroup = "taiyiflow-cli"

// Updater 封装 check-update、下载、report-status 全流程。
type Updater struct {
	baseURL    string // 后端基地址（如 https://snz1.cn）
	apiPrefix  string // API 前缀（如 /taiyi/composer/api/v1）
	apiKey     string // Bearer token（可空）
	httpClient *http.Client
}

// NewUpdater 创建 Updater 实例。
func NewUpdater(baseURL, apiPrefix, apiKey string) *Updater {
	return &Updater{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiPrefix: strings.TrimRight(apiPrefix, "/"),
		apiKey:    apiKey,
		httpClient: &http.Client{
			Timeout: DefaultHTTPTimeout,
		},
	}
}

// applyAuth 在 apiKey 非空时为请求添加 Authorization: Bearer 头。
func (u *Updater) applyAuth(req *http.Request) {
	if trimmed := strings.TrimSpace(u.apiKey); trimmed != "" {
		req.Header.Set("Authorization", "Bearer "+trimmed)
	}
}

// CheckUpdate 调用后端 POST /agents/client/check-update 接口检查更新。
// 当 apiKey 非空时会携带 Authorization: Bearer 鉴权头。
func (u *Updater) CheckUpdate(ctx context.Context, req *CheckUpdateRequest) (*CheckUpdateResponse, error) {
	endpoint := u.baseURL + u.apiPrefix + "/agents/client/check-update"

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("编码请求体失败: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, DefaultHTTPTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", buildUserAgent())
	u.applyAuth(httpReq)

	resp, err := u.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("请求 check-update 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("check-update 返回 HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result CheckUpdateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析 check-update 响应失败: %w", err)
	}

	return &result, nil
}

// ReportStatus 调用后端 POST /agents/client/release/report-status 接口上报状态。
// 当 apiKey 非空时会携带 Authorization: Bearer 鉴权头。
func (u *Updater) ReportStatus(ctx context.Context, req *ReportStatusRequest) error {
	endpoint := u.baseURL + u.apiPrefix + "/agents/client/release/report-status"

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("编码请求体失败: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, DefaultHTTPTimeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", buildUserAgent())
	u.applyAuth(httpReq)

	resp, err := u.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("请求 report-status 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("report-status 返回 HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result ReportStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("解析 report-status 响应失败: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("report-status 返回失败: %s", result.Message)
	}

	return nil
}

// buildUserAgent 构造 User-Agent 头。
func buildUserAgent() string {
	return fmt.Sprintf("taiyiflow-cli/%s (%s/%s)", buildinfo.Version, platformString(), archString())
}

// platformString 返回当前平台标识（与后端 file_type 推导对应）。
func platformString() string {
	// 后端 _resolve_file_type 期望 platform 格式为 "darwin-arm64" / "windows-x64" / "linux-arm64" 等
	return platformDetail()
}

// archString 返回架构标识。
func archString() string {
	return runtimeArch()
}
