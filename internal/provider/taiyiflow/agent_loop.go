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

type agentLoopControlRequestBody struct {
	RunID     string `json:"run_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	DeviceID  string `json:"device_id,omitempty"`
}

type agentLoopControlResponseBody struct {
	SessionID              string `json:"session_id"`
	RunID                  string `json:"run_id"`
	Status                 string `json:"status"`
	RequestID              string `json:"request_id"`
	CheckpointID           string `json:"checkpoint_id"`
	SubtaskCancelRequested int    `json:"subtask_cancel_requested"`
	SubtaskCancelPending   int    `json:"subtask_cancel_pending"`
	SubtaskCancelFailed    bool   `json:"subtask_cancel_failed"`
}

type agentLoopCheckpointResponseBody struct {
	ID                 string   `json:"id"`
	FlowID             string   `json:"flow_id"`
	SessionID          string   `json:"session_id"`
	RunID              string   `json:"run_id"`
	OriginClientID     string   `json:"origin_client_id"`
	OriginDeviceID     string   `json:"origin_device_id"`
	Status             string   `json:"status"`
	Version            int      `json:"version"`
	ResumeStage        string   `json:"resume_stage"`
	PauseReason        string   `json:"pause_reason"`
	RequiresUserResume bool     `json:"requires_user_resume"`
	WaitingCallIDs     []string `json:"waiting_call_ids"`
	PendingCallIDs     []string `json:"pending_call_ids"`
	UncertainCallIDs   []string `json:"uncertain_call_ids"`
	UpdatedAt          string   `json:"updated_at"`
	ExpiresAt          string   `json:"expires_at"`
}

type sessionExecutionResponseBody struct {
	ID                 string `json:"id"`
	FlowID             string `json:"flow_id"`
	Executing          bool   `json:"executing"`
	ExecutingRunID     string `json:"executing_run_id"`
	ExecutionAttemptID string `json:"execution_attempt_id"`
	RunStatus          string `json:"run_status"`
	OriginClientID     string `json:"origin_client_id"`
	OriginDeviceID     string `json:"origin_device_id"`
	CurrentEventID     string `json:"current_event_id"`
	CurrentEventSeq    int64  `json:"current_event_seq"`
}

func (c *Client) GetSessionExecution(ctx context.Context, sessionID string) (*provider.SessionExecutionMetadata, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("获取会话执行状态失败: 会话不能为空")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiURL(fmt.Sprintf("/sessions/%s", url.PathEscape(sessionID))), nil)
	if err != nil {
		return nil, fmt.Errorf("创建会话执行状态请求失败: %w", err)
	}
	c.applyAuth(request)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("获取会话执行状态失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取会话执行状态失败", response)
	}
	var payload sessionExecutionResponseBody
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析会话执行状态失败: %w", err)
	}
	return &provider.SessionExecutionMetadata{
		SessionID:          strings.TrimSpace(payload.ID),
		FlowID:             strings.TrimSpace(payload.FlowID),
		Executing:          payload.Executing,
		ExecutingRunID:     strings.TrimSpace(payload.ExecutingRunID),
		ExecutionAttemptID: strings.TrimSpace(payload.ExecutionAttemptID),
		RunStatus:          strings.TrimSpace(payload.RunStatus),
		OriginClientID:     strings.TrimSpace(payload.OriginClientID),
		OriginDeviceID:     strings.TrimSpace(payload.OriginDeviceID),
		CurrentEventID:     strings.TrimSpace(payload.CurrentEventID),
		CurrentEventSeq:    payload.CurrentEventSeq,
	}, nil
}

func (c *Client) PauseAgentLoop(ctx context.Context, sessionID string, request provider.AgentLoopControlRequest) (*provider.AgentLoopControlResponse, error) {
	return c.controlAgentLoop(ctx, sessionID, "pause", request)
}

func (c *Client) CancelAgentLoop(ctx context.Context, sessionID string, request provider.AgentLoopControlRequest) (*provider.AgentLoopControlResponse, error) {
	return c.controlAgentLoop(ctx, sessionID, "cancel", request)
}

func (c *Client) controlAgentLoop(ctx context.Context, sessionID string, action string, input provider.AgentLoopControlRequest) (*provider.AgentLoopControlResponse, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("智能体循环控制失败: 会话不能为空")
	}
	content, err := json.Marshal(agentLoopControlRequestBody{
		RunID:     strings.TrimSpace(input.RunID),
		RequestID: strings.TrimSpace(input.RequestID),
		ClientID:  strings.TrimSpace(input.ClientID),
		DeviceID:  strings.TrimSpace(input.DeviceID),
	})
	if err != nil {
		return nil, fmt.Errorf("序列化智能体循环控制请求失败: %w", err)
	}
	endpoint := c.apiURL(fmt.Sprintf("/run/sessions/%s/%s", url.PathEscape(sessionID), action))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("创建智能体循环控制请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	c.applyAuth(request)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("智能体循环控制请求失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("智能体循环控制请求失败", response)
	}
	var payload agentLoopControlResponseBody
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析智能体循环控制响应失败: %w", err)
	}
	return &provider.AgentLoopControlResponse{
		SessionID:              strings.TrimSpace(payload.SessionID),
		RunID:                  strings.TrimSpace(payload.RunID),
		Status:                 strings.TrimSpace(payload.Status),
		RequestID:              strings.TrimSpace(payload.RequestID),
		CheckpointID:           strings.TrimSpace(payload.CheckpointID),
		SubtaskCancelRequested: payload.SubtaskCancelRequested,
		SubtaskCancelPending:   payload.SubtaskCancelPending,
		SubtaskCancelFailed:    payload.SubtaskCancelFailed,
	}, nil
}

func (c *Client) ListResumableAgentLoopCheckpoints(ctx context.Context, query provider.AgentLoopCheckpointQuery) ([]provider.AgentLoopCheckpoint, error) {
	if strings.TrimSpace(query.ClientID) == "" {
		return nil, fmt.Errorf("发现可恢复检查点失败: client_id 不能为空")
	}
	endpoint, err := url.Parse(c.apiURL("/run/checkpoints/resumable"))
	if err != nil {
		return nil, fmt.Errorf("构造可恢复检查点请求失败: %w", err)
	}
	values := endpoint.Query()
	values.Set("client_id", strings.TrimSpace(query.ClientID))
	if value := strings.TrimSpace(query.DeviceID); value != "" {
		values.Set("device_id", value)
	}
	if value := strings.TrimSpace(query.FlowID); value != "" {
		values.Set("flow_id", value)
	}
	if value := strings.TrimSpace(query.SessionID); value != "" {
		values.Set("session_id", value)
	}
	endpoint.RawQuery = values.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建可恢复检查点请求失败: %w", err)
	}
	c.applyAuth(request)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("发现可恢复检查点失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("发现可恢复检查点失败", response)
	}
	var payload []agentLoopCheckpointResponseBody
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析可恢复检查点响应失败: %w", err)
	}
	checkpoints := make([]provider.AgentLoopCheckpoint, 0, len(payload))
	for _, item := range payload {
		updatedAt, err := parseTaiyiTime(item.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("解析检查点更新时间失败: %w", err)
		}
		expiresAt, err := parseTaiyiTime(item.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("解析检查点过期时间失败: %w", err)
		}
		checkpoints = append(checkpoints, provider.AgentLoopCheckpoint{
			ID:                 strings.TrimSpace(item.ID),
			FlowID:             strings.TrimSpace(item.FlowID),
			SessionID:          strings.TrimSpace(item.SessionID),
			RunID:              strings.TrimSpace(item.RunID),
			OriginClientID:     strings.TrimSpace(item.OriginClientID),
			OriginDeviceID:     strings.TrimSpace(item.OriginDeviceID),
			Status:             strings.TrimSpace(item.Status),
			Version:            item.Version,
			ResumeStage:        strings.TrimSpace(item.ResumeStage),
			PauseReason:        strings.TrimSpace(item.PauseReason),
			RequiresUserResume: item.RequiresUserResume,
			WaitingCallIDs:     append([]string(nil), item.WaitingCallIDs...),
			PendingCallIDs:     append([]string(nil), item.PendingCallIDs...),
			UncertainCallIDs:   append([]string(nil), item.UncertainCallIDs...),
			UpdatedAt:          updatedAt,
			ExpiresAt:          expiresAt,
		})
	}
	return checkpoints, nil
}
