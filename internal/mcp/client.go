package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// MCP 协议版本与客户端标识，与 taiyiflow-chatui 保持一致。
const (
	mcpProtocolVersion = "2025-03-26"
	mcpClientName      = "taiyiflow-cli"
	mcpClientVersion   = "1.0.0"

	// 工具采集超时
	fetchToolsTimeout = 30 * time.Second
	// 工具调用超时
	callToolTimeout = 120 * time.Second
)

// ToolDefinition 表示一个 MCP 工具定义，用于构建 client_mcp_servers 上送格式。
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// ServerConfig 表示一个 MCP 服务器的连接配置。
type ServerConfig struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	ServerURL    string            `json:"server_url"`
	ServerType   string            `json:"server_type"` // stdio / sse / streamable_http / other
	AuthConfig   map[string]any    `json:"auth_config"`
	Tools        []ToolDefinition  `json:"tools"`
	Env          map[string]string `json:"-"` // 从 auth_config.env 提取
}

// jsonRPCRequest 表示 JSON-RPC 2.0 请求。
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonRPCResponse 表示 JSON-RPC 2.0 响应。
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *jsonRPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

// Client 是 MCP 协议客户端，支持 stdio / streamable_http / SSE 三种传输方式。
type Client struct {
	httpClient *http.Client
	mu         sync.Mutex
	// streamable_http 会话缓存
	httpSessionIDs map[string]string
	// SSE endpoint 缓存
	sseEndpoints map[string]string
	// stdio 进程实例缓存
	stdioInstances map[string]*stdioInstance
}

// NewClient 创建 MCP 协议客户端。
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: callToolTimeout,
		},
		httpSessionIDs: make(map[string]string),
		sseEndpoints:   make(map[string]string),
		stdioInstances: make(map[string]*stdioInstance),
	}
}

// FetchServerTools 连接 MCP 服务器并获取工具列表。
func (c *Client) FetchServerTools(ctx context.Context, server ServerConfig) ([]ToolDefinition, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchToolsTimeout)
	defer cancel()

	switch server.ServerType {
	case "stdio":
		return c.fetchStdioTools(ctx, server)
	case "sse":
		return c.fetchSSETools(ctx, server)
	default:
		return c.fetchStreamableHTTPTools(ctx, server)
	}
}

// CallTool 调用 MCP 服务器上的工具。
func (c *Client) CallTool(ctx context.Context, server ServerConfig, toolName string, toolArgs map[string]any) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, callToolTimeout)
	defer cancel()

	var result json.RawMessage
	var err error

	switch server.ServerType {
	case "stdio":
		result, err = c.callStdioTool(ctx, server, toolName, toolArgs)
	case "sse":
		result, err = c.callSSETool(ctx, server, toolName, toolArgs)
	default:
		result, err = c.callStreamableHTTPTool(ctx, server, toolName, toolArgs)
	}
	if err != nil {
		return nil, err
	}

	return extractToolCallResult(result)
}

// Close 清理所有连接资源。
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, inst := range c.stdioInstances {
		inst.destroy()
	}
	c.stdioInstances = make(map[string]*stdioInstance)
	c.httpSessionIDs = make(map[string]string)
	c.sseEndpoints = make(map[string]string)
}

// ---------- Streamable HTTP 传输 ----------

func (c *Client) fetchStreamableHTTPTools(ctx context.Context, server ServerConfig) ([]ToolDefinition, error) {
	// initialize
	initParams := map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": mcpClientName, "version": mcpClientVersion},
	}
	initResp, err := c.httpJSONRPC(ctx, server, "initialize", initParams)
	if err != nil {
		return nil, fmt.Errorf("MCP initialize 失败: %w", err)
	}

	// 缓存 session ID
	if initResp != nil {
		// session ID 从响应头获取，在 httpJSONRPC 内部处理
	}

	// notifications/initialized（无需响应）
	_ = c.httpNotify(ctx, server, "notifications/initialized", nil)

	// tools/list
	listResp, err := c.httpJSONRPC(ctx, server, "tools/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("MCP tools/list 失败: %w", err)
	}

	return parseToolsListResult(listResp)
}

func (c *Client) callStreamableHTTPTool(ctx context.Context, server ServerConfig, toolName string, toolArgs map[string]any) (json.RawMessage, error) {
	params := map[string]any{
		"name":      toolName,
		"arguments": toolArgs,
	}
	return c.httpJSONRPC(ctx, server, "tools/call", params)
}

var httpRequestID atomic.Int64

func (c *Client) httpJSONRPC(ctx context.Context, server ServerConfig, method string, params any) (json.RawMessage, error) {
	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      httpRequestID.Add(1),
		Method:  method,
		Params:  params,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化 JSON-RPC 请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.ServerURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	applyAuthHeaders(req, server.AuthConfig)

	// 携带已缓存的 session ID
	c.mu.Lock()
	if sessionID, ok := c.httpSessionIDs[server.ServerURL]; ok {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	c.mu.Unlock()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 MCP 服务器失败: %w", err)
	}
	defer resp.Body.Close()

	// 缓存 session ID
	if sessionID := resp.Header.Get("Mcp-Session-Id"); sessionID != "" {
		c.mu.Lock()
		c.httpSessionIDs[server.ServerURL] = sessionID
		c.mu.Unlock()
	}

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("MCP 服务器返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return parseSSEResponse(resp.Body)
	}

	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("解析 JSON-RPC 响应失败: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}
	return rpcResp.Result, nil
}

func (c *Client) httpNotify(ctx context.Context, server ServerConfig, method string, params any) error {
	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.ServerURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	applyAuthHeaders(req, server.AuthConfig)

	c.mu.Lock()
	if sessionID, ok := c.httpSessionIDs[server.ServerURL]; ok {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	c.mu.Unlock()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ---------- SSE 传输 ----------

func (c *Client) fetchSSETools(ctx context.Context, server ServerConfig) ([]ToolDefinition, error) {
	endpoint, err := c.sseConnect(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("SSE 连接失败: %w", err)
	}

	// initialize
	initParams := map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": mcpClientName, "version": mcpClientVersion},
	}
	if _, err := c.sseJSONRPC(ctx, server, endpoint, "initialize", initParams); err != nil {
		return nil, fmt.Errorf("MCP initialize 失败: %w", err)
	}

	// notifications/initialized
	_ = c.sseNotify(ctx, server, endpoint, "notifications/initialized", nil)

	// tools/list
	listResp, err := c.sseJSONRPC(ctx, server, endpoint, "tools/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("MCP tools/list 失败: %w", err)
	}

	return parseToolsListResult(listResp)
}

func (c *Client) callSSETool(ctx context.Context, server ServerConfig, toolName string, toolArgs map[string]any) (json.RawMessage, error) {
	endpoint, err := c.sseConnect(ctx, server)
	if err != nil {
		return nil, fmt.Errorf("SSE 连接失败: %w", err)
	}

	params := map[string]any{
		"name":      toolName,
		"arguments": toolArgs,
	}
	return c.sseJSONRPC(ctx, server, endpoint, "tools/call", params)
}

func (c *Client) sseConnect(ctx context.Context, server ServerConfig) (string, error) {
	c.mu.Lock()
	if endpoint, ok := c.sseEndpoints[server.ServerURL]; ok {
		c.mu.Unlock()
		return endpoint, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.ServerURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/event-stream")
	applyAuthHeaders(req, server.AuthConfig)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("SSE 连接返回 HTTP %d", resp.StatusCode)
	}

	// 读取第一个 SSE 事件获取 endpoint
	reader := bufio.NewReader(resp.Body)
	endpoint := ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("SSE 流中未找到 endpoint 事件: %w", err)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			// endpoint 可能是相对路径或绝对路径
			if strings.HasPrefix(data, "/") {
				// 相对路径，拼接 base URL
				baseURL := server.ServerURL
				if idx := strings.Index(baseURL, "://"); idx >= 0 {
					schemeEnd := idx + 3
					hostEnd := strings.Index(baseURL[schemeEnd:], "/")
					if hostEnd >= 0 {
						baseURL = baseURL[:schemeEnd+hostEnd]
					}
				}
				endpoint = baseURL + data
			} else {
				endpoint = data
			}
			break
		}
	}

	c.mu.Lock()
	c.sseEndpoints[server.ServerURL] = endpoint
	c.mu.Unlock()

	return endpoint, nil
}

func (c *Client) sseJSONRPC(ctx context.Context, server ServerConfig, endpoint, method string, params any) (json.RawMessage, error) {
	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      httpRequestID.Add(1),
		Method:  method,
		Params:  params,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	applyAuthHeaders(req, server.AuthConfig)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("SSE endpoint 返回 HTTP %d: %s", resp.StatusCode, string(body))
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return parseSSEResponse(resp.Body)
	}

	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		return nil, rpcResp.Error
	}
	return rpcResp.Result, nil
}

func (c *Client) sseNotify(ctx context.Context, server ServerConfig, endpoint, method string, params any) error {
	reqBody := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	applyAuthHeaders(req, server.AuthConfig)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ---------- Stdio 传输 ----------

type stdioInstance struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	mu      sync.Mutex
	closed  bool
}

func (c *Client) getOrCreateStdioInstance(server ServerConfig) (*stdioInstance, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if inst, ok := c.stdioInstances[server.ServerURL]; ok {
		return inst, nil
	}

	// 解析 stdio:// URL 格式: stdio://command arg1 arg2 ...
	commandStr := strings.TrimPrefix(server.ServerURL, "stdio://")
	parts := strings.Fields(commandStr)
	if len(parts) == 0 {
		return nil, fmt.Errorf("无效的 stdio 命令: %s", server.ServerURL)
	}

	cmd := exec.Command(parts[0], parts[1:]...)
	// 继承父进程环境变量，再追加自定义变量。
	// 注意：一旦 cmd.Env 非 nil，Go 不再自动继承父进程环境，必须先显式赋值。
	cmd.Env = os.Environ()
	if server.AuthConfig != nil {
		if env, ok := server.AuthConfig["env"].(map[string]any); ok {
			for k, v := range env {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%v", k, v))
			}
		}
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 stdin 管道失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("创建 stdout 管道失败: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 stdio 进程失败: %w", err)
	}

	inst := &stdioInstance{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}
	c.stdioInstances[server.ServerURL] = inst
	return inst, nil
}

func (inst *stdioInstance) sendRequest(ctx context.Context, req jsonRPCRequest) (json.RawMessage, error) {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.closed {
		return nil, fmt.Errorf("stdio 实例已关闭")
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	if _, err := inst.stdin.Write(data); err != nil {
		return nil, fmt.Errorf("写入 stdio 失败: %w", err)
	}

	// 使用 goroutine + channel 实现 context 感知的读取超时
	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := inst.stdout.ReadBytes('\n')
		ch <- readResult{line: line, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("stdio 响应超时: %w", ctx.Err())
	case res := <-ch:
		if res.err != nil {
			return nil, fmt.Errorf("读取 stdio 响应失败: %w", res.err)
		}
		var rpcResp jsonRPCResponse
		if err := json.Unmarshal(bytes.TrimSpace(res.line), &rpcResp); err != nil {
			return nil, fmt.Errorf("解析 stdio JSON-RPC 响应失败: %w", err)
		}
		if rpcResp.Error != nil {
			return nil, rpcResp.Error
		}
		return rpcResp.Result, nil
	}
}

func (inst *stdioInstance) sendNotification(method string, params any) error {
	inst.mu.Lock()
	defer inst.mu.Unlock()

	if inst.closed {
		return nil
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = inst.stdin.Write(data)
	return err
}

func (inst *stdioInstance) destroy() {
	inst.mu.Lock()
	defer inst.mu.Unlock()
	if inst.closed {
		return
	}
	inst.closed = true
	inst.stdin.Close()
	if inst.cmd.Process != nil {
		inst.cmd.Process.Kill()
	}
	inst.cmd.Wait()
}

func (c *Client) fetchStdioTools(ctx context.Context, server ServerConfig) ([]ToolDefinition, error) {
	inst, err := c.getOrCreateStdioInstance(server)
	if err != nil {
		return nil, err
	}

	// initialize
	initParams := map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": mcpClientName, "version": mcpClientVersion},
	}
	if _, err := inst.sendRequest(ctx, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      httpRequestID.Add(1),
		Method:  "initialize",
		Params:  initParams,
	}); err != nil {
		return nil, fmt.Errorf("stdio MCP initialize 失败: %w", err)
	}

	// notifications/initialized
	_ = inst.sendNotification("notifications/initialized", nil)

	// tools/list
	listResult, err := inst.sendRequest(ctx, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      httpRequestID.Add(1),
		Method:  "tools/list",
		Params:  map[string]any{},
	})
	if err != nil {
		return nil, fmt.Errorf("stdio MCP tools/list 失败: %w", err)
	}

	return parseToolsListResult(listResult)
}

func (c *Client) callStdioTool(ctx context.Context, server ServerConfig, toolName string, toolArgs map[string]any) (json.RawMessage, error) {
	inst, err := c.getOrCreateStdioInstance(server)
	if err != nil {
		return nil, err
	}

	params := map[string]any{
		"name":      toolName,
		"arguments": toolArgs,
	}
	return inst.sendRequest(ctx, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      httpRequestID.Add(1),
		Method:  "tools/call",
		Params:  params,
	})
}

// ---------- 辅助函数 ----------

// parseToolsListResult 解析 tools/list 响应中的工具列表。
func parseToolsListResult(raw json.RawMessage) ([]ToolDefinition, error) {
	if raw == nil {
		return nil, nil
	}

	var result struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("解析工具列表失败: %w", err)
	}

	tools := make([]ToolDefinition, 0, len(result.Tools))
	for _, t := range result.Tools {
		tools = append(tools, ToolDefinition{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return tools, nil
}

// extractToolCallResult 提取 tools/call 响应中的结果。
// MCP 协议返回格式: { content: [...], isError?: boolean }
func extractToolCallResult(raw json.RawMessage) (any, error) {
	if raw == nil {
		return nil, nil
	}

	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		// 无法解析为 MCP 标准格式，直接返回原始 JSON
		var generic any
		if json.Unmarshal(raw, &generic) == nil {
			return generic, nil
		}
		return string(raw), nil
	}

	if result.IsError {
		var texts []string
		for _, item := range result.Content {
			if item.Text != "" {
				texts = append(texts, item.Text)
			}
		}
		return nil, fmt.Errorf("MCP 工具执行错误: %s", strings.Join(texts, "\n"))
	}

	// 拼接所有 text content
	var texts []string
	for _, item := range result.Content {
		if item.Type == "text" && item.Text != "" {
			texts = append(texts, item.Text)
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "\n"), nil
	}

	// 无文本内容，返回原始 JSON
	var generic any
	if json.Unmarshal(raw, &generic) == nil {
		return generic, nil
	}
	return string(raw), nil
}

// parseSSEResponse 从 SSE 流中解析 JSON-RPC 响应。
func parseSSEResponse(body io.Reader) (json.RawMessage, error) {
	reader := bufio.NewReader(body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("SSE 流中未找到 JSON-RPC 响应: %w", err)
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			var rpcResp jsonRPCResponse
			if err := json.Unmarshal([]byte(data), &rpcResp); err != nil {
				continue
			}
			if rpcResp.Error != nil {
				return nil, rpcResp.Error
			}
			return rpcResp.Result, nil
		}
	}
}

// applyAuthHeaders 根据 auth_config 为请求添加认证头。
func applyAuthHeaders(req *http.Request, authConfig map[string]any) {
	if authConfig == nil {
		return
	}

	authType, _ := authConfig["type"].(string)
	switch authType {
	case "bearer":
		if token, ok := authConfig["token"].(string); ok && token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	case "api_key":
		key, _ := authConfig["key"].(string)
		header, _ := authConfig["header"].(string)
		if header == "" {
			header = "X-API-Key"
		}
		if key != "" {
			req.Header.Set(header, key)
		}
	}

	// 自定义请求头
	if headers, ok := authConfig["headers"].(map[string]any); ok {
		for k, v := range headers {
			if strVal, ok := v.(string); ok {
				req.Header.Set(k, strVal)
			}
		}
	}
}
