package taiyiflow

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/taiyiflow/taiyiflow-cli/internal/provider"
)

const SessionHeader = "X-TaiyiFlow-Session-id"

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

type metadataResponse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	WelcomeMessage string `json:"welcome_message"`
	Enabled        bool   `json:"enabled"`
	IsChat         bool   `json:"is_chat"`
}

type flowItemResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Type        string `json:"type"`
}

type flowListResponse struct {
	Total  int                `json:"total"`
	Offset int                `json:"offset"`
	Data   []flowItemResponse `json:"data"`
}

type streamRequestBody struct {
	InputValue             string `json:"input_value,omitempty"`
	SessionID              string `json:"session_id,omitempty"`
	RequestType            string `json:"request_type,omitempty"`
	CommandExecutionTarget string `json:"command_execution_target,omitempty"`
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *Client) Name() string {
	return "taiyiflow"
}

func (c *Client) ListFlows(ctx context.Context, filter string, offset, limit int) (*provider.FlowList, error) {
	endpoint, err := url.Parse(fmt.Sprintf("%s/api/v1/agents/flows", c.baseURL))
	if err != nil {
		return nil, fmt.Errorf("构造 flows 请求地址失败: %w", err)
	}

	query := endpoint.Query()
	if strings.TrimSpace(filter) != "" {
		query.Set("filter", strings.TrimSpace(filter))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建 flows 请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 flows 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取 flows 列表失败", resp)
	}

	var payload flowListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 flows 响应失败: %w", err)
	}

	items := make([]provider.FlowSummary, 0, len(payload.Data))
	for _, item := range payload.Data {
		items = append(items, provider.FlowSummary{
			ID:          item.ID,
			Name:        item.Name,
			Description: item.Description,
			Type:        item.Type,
		})
	}

	return &provider.FlowList{
		Total:  payload.Total,
		Offset: payload.Offset,
		Items:  items,
	}, nil
}

func (c *Client) GetMetadata(ctx context.Context, flowID string) (*provider.Metadata, error) {
	endpoint := fmt.Sprintf("%s/api/v1/run/%s/metadata", c.baseURL, url.PathEscape(flowID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 metadata 请求失败: %w", err)
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 metadata 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, readHTTPError("获取 metadata 失败", resp)
	}

	var payload metadataResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("解析 metadata 响应失败: %w", err)
	}

	return &provider.Metadata{
		ID:             payload.ID,
		Name:           payload.Name,
		Description:    payload.Description,
		WelcomeMessage: payload.WelcomeMessage,
		Enabled:        payload.Enabled,
		IsChat:         payload.IsChat,
	}, nil
}

func (c *Client) Stream(ctx context.Context, request provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	endpoint := fmt.Sprintf("%s/api/v1/run/%s/stream", c.baseURL, url.PathEscape(request.FlowID))
	body := streamRequestBody{
		InputValue:             request.Input,
		SessionID:              request.SessionID,
		RequestType:            firstNonEmpty(request.RequestType, "agent"),
		CommandExecutionTarget: firstNonEmpty(request.CommandExecutionTarget, "client"),
	}

	content, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("序列化 stream 请求失败: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return nil, nil, fmt.Errorf("创建 stream 请求失败: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "text/event-stream")
	c.applyAuth(httpRequest)

	resp, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return nil, nil, fmt.Errorf("请求 stream 接口失败: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		return nil, nil, readHTTPError("请求 stream 接口失败", resp)
	}

	streamSession := &provider.StreamSession{SessionID: resp.Header.Get(SessionHeader)}
	stream := make(chan provider.StreamEvent)

	go func() {
		defer close(stream)
		defer resp.Body.Close()
		readSSE(resp.Body, stream)
	}()

	return stream, streamSession, nil
}

func (c *Client) applyAuth(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
}

func readSSE(reader io.Reader, output chan<- provider.StreamEvent) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var eventName string
	var dataLines []string

	flush := func() {
		if eventName == "" && len(dataLines) == 0 {
			return
		}
		raw := strings.Join(dataLines, "\n")
		payload := map[string]any{}
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				payload = map[string]any{"raw": raw, "error": err.Error()}
			}
		}
		typeName := provider.StreamEventType(firstNonEmpty(eventName, string(provider.EventMessage)))
		output <- provider.StreamEvent{Type: typeName, Data: payload, Raw: raw}
		eventName = ""
		dataLines = nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			flush()
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}

	flush()

	if err := scanner.Err(); err != nil {
		output <- provider.StreamEvent{
			Type: provider.EventError,
			Data: map[string]any{"error": fmt.Sprintf("读取 SSE 失败: %v", err)},
		}
	}
}

func readHTTPError(prefix string, response *http.Response) error {
	content, _ := io.ReadAll(response.Body)
	message := strings.TrimSpace(string(content))
	if message == "" {
		message = response.Status
	}
	return fmt.Errorf("%s: %s", prefix, message)
}

func firstNonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
