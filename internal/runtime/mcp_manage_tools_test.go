package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// ---------------------------------------------------------------------------
// stubMcpProvider 实现 MCP 管理相关的 Provider 接口
// ---------------------------------------------------------------------------

type stubMcpProvider struct {
	provider.Provider
	servers        []provider.MCPServerSummary
	listErr        error
	createResult   *provider.MCPServerSummary
	createErr      error
	updateResult   *provider.MCPServerSummary
	updateErr      error
	deleteErr      error
	lastCreate     provider.MCPServerCreateRequest
	lastUpdateID   string
	lastUpdate     provider.MCPServerUpdateRequest
	lastDeleteID   string
	listFlowID     string
	listClientType string
}

func (s *stubMcpProvider) ListMCPServers(_ context.Context, flowID string, clientType string) ([]provider.MCPServerSummary, error) {
	s.listFlowID = flowID
	s.listClientType = clientType
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.servers, nil
}

func (s *stubMcpProvider) CreateMCPServer(_ context.Context, request provider.MCPServerCreateRequest) (*provider.MCPServerSummary, error) {
	s.lastCreate = request
	if s.createErr != nil {
		return nil, s.createErr
	}
	if s.createResult != nil {
		return s.createResult, nil
	}
	return &provider.MCPServerSummary{
		ID:         "new-server-id",
		Name:       request.Name,
		ServerURL:  request.ServerURL,
		ServerType: request.ServerType,
	}, nil
}

func (s *stubMcpProvider) UpdateMCPServer(_ context.Context, serverID string, request provider.MCPServerUpdateRequest) (*provider.MCPServerSummary, error) {
	s.lastUpdateID = serverID
	s.lastUpdate = request
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	if s.updateResult != nil {
		return s.updateResult, nil
	}
	result := provider.MCPServerSummary{ID: serverID, Name: "updated"}
	if request.Name != nil {
		result.Name = *request.Name
	}
	if request.ServerURL != nil {
		result.ServerURL = *request.ServerURL
	}
	if request.ServerType != nil {
		result.ServerType = *request.ServerType
	}
	if request.Description != nil {
		result.Description = *request.Description
	}
	return &result, nil
}

func (s *stubMcpProvider) DeleteMCPServer(_ context.Context, serverID string) error {
	s.lastDeleteID = serverID
	return s.deleteErr
}

// setupMcpTestEnv 设置测试环境，将 SNZ1DP_HOME 指向临时目录，
// 避免测试污染真实的 ~/.snz1dp/config/mcp.json。
func setupMcpTestEnv(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("SNZ1DP_HOME", tmpDir)
	return tmpDir
}

// mcpTestConfig 返回带有默认智能体配置的测试 Config。
func mcpTestConfig() internalconfig.Config {
	config := internalconfig.Default()
	config.DefaultFlow = "flow-test-001"
	return config
}

// sampleMcpServers 返回测试用的服务器列表。
func sampleMcpServers() []provider.MCPServerSummary {
	enabledTrue := true
	enabledFalse := false
	return []provider.MCPServerSummary{
		{
			ID:         "sys-1",
			Name:       "system-tools",
			ServerType: "streamable_http",
			IsSystem:   true,
			Tools: []provider.MCPToolSummary{
				{Name: "search_web", Description: "搜索网页", Enabled: &enabledTrue},
				{Name: "read_file", Description: "读取文件", Enabled: &enabledTrue},
			},
		},
		{
			ID:         "opt-1",
			Name:       "optional-service",
			ServerType: "sse",
			Tools: []provider.MCPToolSummary{
				{Name: "translate", Description: "翻译", Enabled: &enabledFalse},
			},
		},
		{
			ID:          "user-1",
			Name:        "my-mcp",
			ServerURL:   "http://localhost:9090/mcp",
			ServerType:  "streamable_http",
			OwnerUserID: "user-123",
			Description: "我的自建服务",
			Tools: []provider.MCPToolSummary{
				{Name: "custom_tool", Description: "自定义工具", Enabled: &enabledTrue},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// isMcpManageTool 测试
// ---------------------------------------------------------------------------

func TestIsMcpManageTool(t *testing.T) {
	valid := []string{
		"mcp_list_servers", "mcp_list_tools", "mcp_toggle_server",
		"mcp_toggle_tool", "mcp_add_server", "mcp_edit_server", "mcp_delete_server",
	}
	for _, name := range valid {
		if !isMcpManageTool(name) {
			t.Errorf("expected %q to be a MCP manage tool", name)
		}
	}
	invalid := []string{"shell_exec", "schedule_list", "mcp_unknown", ""}
	for _, name := range invalid {
		if isMcpManageTool(name) {
			t.Errorf("expected %q NOT to be a MCP manage tool", name)
		}
	}
}

// ---------------------------------------------------------------------------
// mutatingMcpManageTool 测试
// ---------------------------------------------------------------------------

func TestMutatingMcpManageTool(t *testing.T) {
	readOnly := []string{"mcp_list_servers", "mcp_list_tools"}
	mutating := []string{"mcp_toggle_server", "mcp_toggle_tool", "mcp_add_server", "mcp_edit_server", "mcp_delete_server"}

	for _, name := range readOnly {
		if mutatingMcpManageTool(name) {
			t.Errorf("expected %q to be read-only", name)
		}
	}
	for _, name := range mutating {
		if !mutatingMcpManageTool(name) {
			t.Errorf("expected %q to be mutating", name)
		}
	}
}

// ---------------------------------------------------------------------------
// mutatingClientTool 对 MCP 管理工具的判定测试
// ---------------------------------------------------------------------------

func TestMutatingClientToolMcpManage(t *testing.T) {
	readOnly := []string{"mcp_list_servers", "mcp_list_tools"}
	mutating := []string{"mcp_toggle_server", "mcp_toggle_tool", "mcp_add_server", "mcp_edit_server", "mcp_delete_server"}

	for _, name := range readOnly {
		if mutatingClientTool(name) {
			t.Errorf("expected %q to be read-only in mutatingClientTool", name)
		}
	}
	for _, name := range mutating {
		if !mutatingClientTool(name) {
			t.Errorf("expected %q to be mutating in mutatingClientTool", name)
		}
	}
}

// ---------------------------------------------------------------------------
// mcpManagePreview 测试
// ---------------------------------------------------------------------------

func TestMcpManagePreview(t *testing.T) {
	tests := []struct {
		toolName  string
		arguments map[string]any
		expected  string
	}{
		{"mcp_list_servers", map[string]any{}, "mcp_list_servers"},
		{"mcp_list_tools", map[string]any{"server_name": "my-mcp"}, "mcp_list_tools my-mcp"},
		{"mcp_list_tools", map[string]any{}, "mcp_list_tools <missing>"},
		{"mcp_toggle_server", map[string]any{"server_name": "svc", "enabled": true}, "mcp_toggle_server svc → 启用"},
		{"mcp_toggle_server", map[string]any{"server_name": "svc", "enabled": false}, "mcp_toggle_server svc → 禁用"},
		{"mcp_toggle_tool", map[string]any{"server_name": "svc", "tool_name": "tool1", "enabled": false}, "mcp_toggle_tool svc/tool1 → 禁用"},
		{"mcp_add_server", map[string]any{"name": "new-svc", "server_type": "sse"}, "mcp_add_server new-svc (sse)"},
		{"mcp_add_server", map[string]any{"name": "new-svc"}, "mcp_add_server new-svc (streamable_http)"},
		{"mcp_edit_server", map[string]any{"server_name": "my-mcp"}, "mcp_edit_server my-mcp"},
		{"mcp_delete_server", map[string]any{"server_name": "old-svc"}, "mcp_delete_server old-svc"},
	}
	for _, tt := range tests {
		got := mcpManagePreview(tt.toolName, tt.arguments)
		if got != tt.expected {
			t.Errorf("mcpManagePreview(%q, %v) = %q, want %q", tt.toolName, tt.arguments, got, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// previewClientTool 对 MCP 管理工具的集成测试
// ---------------------------------------------------------------------------

func TestPreviewClientToolMcpManage(t *testing.T) {
	tests := []struct {
		toolName  string
		arguments map[string]any
		expected  string
	}{
		{"mcp_list_servers", map[string]any{}, "mcp_list_servers"},
		{"mcp_toggle_server", map[string]any{"server_name": "svc", "enabled": false}, "mcp_toggle_server svc → 禁用"},
		{"mcp_delete_server", map[string]any{"server_name": "old"}, "mcp_delete_server old"},
	}
	for _, tt := range tests {
		got := previewClientTool(tt.toolName, tt.arguments)
		if got != tt.expected {
			t.Errorf("previewClientTool(%q) = %q, want %q", tt.toolName, got, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// mcpManageTool 路由测试
// ---------------------------------------------------------------------------

func TestMcpManageToolRequiresProvider(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	config := mcpTestConfig()

	_, err := r.mcpManageTool(context.Background(), nil, config, "mcp_list_servers", map[string]any{})
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	if !strings.Contains(err.Error(), "已认证的后端连接") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestMcpManageToolRejectsUnknownTool(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{}
	config := mcpTestConfig()

	_, err := r.mcpManageTool(context.Background(), stub, config, "mcp_unknown_tool", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "不支持的 MCP 管理工具") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// mcpListServers 测试
// ---------------------------------------------------------------------------

func TestMcpListServersSuccess(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpListServers(context.Background(), stub, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	message := result["message"].(string)
	if !strings.Contains(message, "3 个 MCP 服务器") {
		t.Fatalf("expected message to mention 3 servers, got %q", message)
	}

	groups := result["groups"].(map[string]any)
	if _, ok := groups["默认加载"]; !ok {
		t.Fatal("expected 默认加载 group")
	}
	if _, ok := groups["可选加载"]; !ok {
		t.Fatal("expected 可选加载 group")
	}
	if _, ok := groups["我的服务"]; !ok {
		t.Fatal("expected 我的服务 group")
	}

	// 验证系统服务器在默认加载分组
	defaultGroup := groups["默认加载"].([]map[string]any)
	if len(defaultGroup) != 1 {
		t.Fatalf("expected 1 server in 默认加载, got %d", len(defaultGroup))
	}
	if defaultGroup[0]["name"] != "system-tools" {
		t.Fatalf("expected system-tools, got %v", defaultGroup[0]["name"])
	}
	if defaultGroup[0]["enabled"] != true {
		t.Fatal("expected system server to be enabled")
	}
	if defaultGroup[0]["tool_count"] != 2 {
		t.Fatalf("expected tool_count=2, got %v", defaultGroup[0]["tool_count"])
	}
}

func TestMcpListServersEmpty(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: []provider.MCPServerSummary{}}
	config := mcpTestConfig()

	result, err := r.mcpListServers(context.Background(), stub, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result["message"].(string), "没有已加载的 MCP 服务器") {
		t.Fatalf("unexpected message: %v", result["message"])
	}
}

func TestMcpListServersRequiresDefaultFlow(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{}
	config := internalconfig.Default()
	config.DefaultFlow = ""

	_, err := r.mcpListServers(context.Background(), stub, config)
	if err == nil {
		t.Fatal("expected error for missing default flow")
	}
}

func TestMcpListServersProviderError(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{listErr: fmt.Errorf("connection refused")}
	config := mcpTestConfig()

	_, err := r.mcpListServers(context.Background(), stub, config)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected provider error to propagate, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// mcpListTools 测试
// ---------------------------------------------------------------------------

func TestMcpListToolsSuccess(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpListTools(context.Background(), stub, config, map[string]any{
		"server_name": "system-tools",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["server_name"] != "system-tools" {
		t.Fatalf("unexpected server_name: %v", result["server_name"])
	}
	tools := result["tools"].([]map[string]any)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0]["name"] != "search_web" {
		t.Fatalf("expected first tool to be search_web, got %v", tools[0]["name"])
	}
	if tools[0]["enabled"] != true {
		t.Fatal("expected search_web to be enabled")
	}
}

func TestMcpListToolsRequiresServerName(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{}
	config := mcpTestConfig()

	_, err := r.mcpListTools(context.Background(), stub, config, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing server_name")
	}
}

func TestMcpListToolsServerNotFound(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	_, err := r.mcpListTools(context.Background(), stub, config, map[string]any{
		"server_name": "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent server")
	}
	if !strings.Contains(err.Error(), "未找到") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMcpListToolsEmptyServer(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: []provider.MCPServerSummary{
		{ID: "empty-1", Name: "empty-server", ServerType: "sse"},
	}}
	config := mcpTestConfig()

	result, err := r.mcpListTools(context.Background(), stub, config, map[string]any{
		"server_name": "empty-server",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result["message"].(string), "暂无工具") {
		t.Fatalf("unexpected message: %v", result["message"])
	}
}

// ---------------------------------------------------------------------------
// mcpToggleServer 测试
// ---------------------------------------------------------------------------

func TestMcpToggleServerDisableSuccess(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpToggleServer(context.Background(), stub, config, map[string]any{
		"server_name": "my-mcp",
		"enabled":     false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["success"] != true {
		t.Fatal("expected success=true")
	}
	if !strings.Contains(result["message"].(string), "禁用") {
		t.Fatalf("expected disable message, got: %v", result["message"])
	}
}

func TestMcpToggleServerEnableSuccess(t *testing.T) {
	tmpDir := setupMcpTestEnv(t)
	// 先禁用服务器
	configDir := filepath.Join(tmpDir, "config")
	os.MkdirAll(configDir, 0o755)
	os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(`{"disabled_server_ids":["user-1"],"disabled_tool_keys":[]}`), 0o644)

	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpToggleServer(context.Background(), stub, config, map[string]any{
		"server_name": "my-mcp",
		"enabled":     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["success"] != true {
		t.Fatal("expected success=true")
	}
	if !strings.Contains(result["message"].(string), "启用") {
		t.Fatalf("expected enable message, got: %v", result["message"])
	}
}

func TestMcpToggleServerSystemCannotDisable(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	_, err := r.mcpToggleServer(context.Background(), stub, config, map[string]any{
		"server_name": "system-tools",
		"enabled":     false,
	})
	if err == nil {
		t.Fatal("expected error when disabling system server")
	}
	if !strings.Contains(err.Error(), "系统内置服务") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMcpToggleServerAlreadyInState(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	// 服务器当前未被禁用，再次启用应返回"无需操作"
	result, err := r.mcpToggleServer(context.Background(), stub, config, map[string]any{
		"server_name": "my-mcp",
		"enabled":     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result["message"].(string), "无需操作") {
		t.Fatalf("expected no-op message, got: %v", result["message"])
	}
}

func TestMcpToggleServerNotFound(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	_, err := r.mcpToggleServer(context.Background(), stub, config, map[string]any{
		"server_name": "ghost",
		"enabled":     false,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent server")
	}
}

func TestMcpToggleServerRequiresServerName(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{}
	config := mcpTestConfig()

	_, err := r.mcpToggleServer(context.Background(), stub, config, map[string]any{
		"enabled": true,
	})
	if err == nil {
		t.Fatal("expected error for missing server_name")
	}
}

// ---------------------------------------------------------------------------
// mcpToggleTool 测试
// ---------------------------------------------------------------------------

func TestMcpToggleToolDisableSuccess(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpToggleTool(context.Background(), stub, config, map[string]any{
		"server_name": "system-tools",
		"tool_name":   "search_web",
		"enabled":     false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["success"] != true {
		t.Fatal("expected success=true")
	}
	if !strings.Contains(result["message"].(string), "禁用") {
		t.Fatalf("expected disable message, got: %v", result["message"])
	}
}

func TestMcpToggleToolEnableSuccess(t *testing.T) {
	tmpDir := setupMcpTestEnv(t)
	configDir := filepath.Join(tmpDir, "config")
	os.MkdirAll(configDir, 0o755)
	os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(`{"disabled_server_ids":[],"disabled_tool_keys":["sys-1:search_web"]}`), 0o644)

	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpToggleTool(context.Background(), stub, config, map[string]any{
		"server_name": "system-tools",
		"tool_name":   "search_web",
		"enabled":     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["success"] != true {
		t.Fatal("expected success=true")
	}
	if !strings.Contains(result["message"].(string), "启用") {
		t.Fatalf("expected enable message, got: %v", result["message"])
	}
}

func TestMcpToggleToolNotFound(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	_, err := r.mcpToggleTool(context.Background(), stub, config, map[string]any{
		"server_name": "system-tools",
		"tool_name":   "nonexistent_tool",
		"enabled":     false,
	})
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
	}
	if !strings.Contains(err.Error(), "未找到名为") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMcpToggleToolAlreadyInState(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	// search_web 当前未被禁用，再次启用应返回"无需操作"
	result, err := r.mcpToggleTool(context.Background(), stub, config, map[string]any{
		"server_name": "system-tools",
		"tool_name":   "search_web",
		"enabled":     true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result["message"].(string), "无需操作") {
		t.Fatalf("expected no-op message, got: %v", result["message"])
	}
}

func TestMcpToggleToolRequiresParams(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{}
	config := mcpTestConfig()

	// 缺少 server_name
	_, err := r.mcpToggleTool(context.Background(), stub, config, map[string]any{
		"tool_name": "tool",
		"enabled":   true,
	})
	if err == nil {
		t.Fatal("expected error for missing server_name")
	}

	// 缺少 tool_name
	_, err = r.mcpToggleTool(context.Background(), stub, config, map[string]any{
		"server_name": "svc",
		"enabled":     true,
	})
	if err == nil {
		t.Fatal("expected error for missing tool_name")
	}
}

// ---------------------------------------------------------------------------
// mcpAddServer 测试
// ---------------------------------------------------------------------------

func TestMcpAddServerSuccess(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpAddServer(context.Background(), stub, config, map[string]any{
		"name":        "new-service",
		"server_url":  "http://localhost:8080/mcp",
		"server_type": "streamable_http",
		"description": "测试服务",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["success"] != true {
		t.Fatal("expected success=true")
	}
	if !strings.Contains(result["message"].(string), "new-service") {
		t.Fatalf("unexpected message: %v", result["message"])
	}

	// 验证请求参数
	if stub.lastCreate.Name != "new-service" {
		t.Fatalf("expected create name 'new-service', got %q", stub.lastCreate.Name)
	}
	if stub.lastCreate.FlowID != "flow-test-001" {
		t.Fatalf("expected flow_id 'flow-test-001', got %q", stub.lastCreate.FlowID)
	}
	if stub.lastCreate.ClientType != "cli" {
		t.Fatalf("expected client_type 'cli', got %q", stub.lastCreate.ClientType)
	}
}

func TestMcpAddServerDefaultsToStreamableHttp(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: []provider.MCPServerSummary{}}
	config := mcpTestConfig()

	_, err := r.mcpAddServer(context.Background(), stub, config, map[string]any{
		"name":       "auto-type",
		"server_url": "http://localhost:3000/mcp",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.lastCreate.ServerType != "streamable_http" {
		t.Fatalf("expected default server_type 'streamable_http', got %q", stub.lastCreate.ServerType)
	}
}

func TestMcpAddServerRejectsDuplicateName(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	_, err := r.mcpAddServer(context.Background(), stub, config, map[string]any{
		"name":        "my-mcp",
		"server_url":  "http://localhost:9999/mcp",
		"server_type": "sse",
	})
	if err == nil {
		t.Fatal("expected error for duplicate name")
	}
	if !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMcpAddServerRejectsInvalidType(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: []provider.MCPServerSummary{}}
	config := mcpTestConfig()

	_, err := r.mcpAddServer(context.Background(), stub, config, map[string]any{
		"name":        "bad-type",
		"server_url":  "http://localhost:8080/mcp",
		"server_type": "websocket",
	})
	if err == nil {
		t.Fatal("expected error for invalid server_type")
	}
	if !strings.Contains(err.Error(), "不支持的服务器类型") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMcpAddServerRequiresMandatoryFields(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{}
	config := mcpTestConfig()

	// 缺少 name
	_, err := r.mcpAddServer(context.Background(), stub, config, map[string]any{
		"server_url":  "http://localhost:8080/mcp",
		"server_type": "sse",
	})
	if err == nil {
		t.Fatal("expected error for missing name")
	}

	// 缺少 server_url
	_, err = r.mcpAddServer(context.Background(), stub, config, map[string]any{
		"name":        "svc",
		"server_type": "sse",
	})
	if err == nil {
		t.Fatal("expected error for missing server_url")
	}
}

func TestMcpAddServerRequiresDefaultFlow(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{}
	config := internalconfig.Default()
	config.DefaultFlow = ""

	_, err := r.mcpAddServer(context.Background(), stub, config, map[string]any{
		"name":        "svc",
		"server_url":  "http://localhost:8080/mcp",
		"server_type": "sse",
	})
	if err == nil {
		t.Fatal("expected error for missing default flow")
	}
}

func TestMcpAddServerAcceptsStdioType(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: []provider.MCPServerSummary{}}
	config := mcpTestConfig()

	result, err := r.mcpAddServer(context.Background(), stub, config, map[string]any{
		"name":        "local-tool",
		"server_url":  "stdio://npx -y @some/mcp-server",
		"server_type": "stdio",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["success"] != true {
		t.Fatal("expected success=true")
	}
	if stub.lastCreate.ServerType != "stdio" {
		t.Fatalf("expected server_type 'stdio', got %q", stub.lastCreate.ServerType)
	}
}

// ---------------------------------------------------------------------------
// mcpEditServer 测试
// ---------------------------------------------------------------------------

func TestMcpEditServerSuccess(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpEditServer(context.Background(), stub, config, map[string]any{
		"server_name": "my-mcp",
		"server_url":  "http://localhost:9999/mcp",
		"description": "更新后的描述",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["success"] != true {
		t.Fatal("expected success=true")
	}
	if stub.lastUpdateID != "user-1" {
		t.Fatalf("expected update ID 'user-1', got %q", stub.lastUpdateID)
	}
	if stub.lastUpdate.ServerURL == nil || *stub.lastUpdate.ServerURL != "http://localhost:9999/mcp" {
		t.Fatalf("expected server_url to be updated, got %#v", stub.lastUpdate)
	}
	if stub.lastUpdate.Description == nil || *stub.lastUpdate.Description != "更新后的描述" {
		t.Fatalf("expected description to be updated, got %#v", stub.lastUpdate)
	}
	// name 未传入，不应被设置
	if stub.lastUpdate.Name != nil {
		t.Fatal("expected name to be nil when not provided")
	}
}

func TestMcpEditServerRejectsNonUserServer(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	// system-tools 没有 OwnerUserID
	_, err := r.mcpEditServer(context.Background(), stub, config, map[string]any{
		"server_name": "system-tools",
		"name":        "hacked",
	})
	if err == nil {
		t.Fatal("expected error when editing non-user server")
	}
	if !strings.Contains(err.Error(), "不可编辑") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMcpEditServerRejectsOptionalServer(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	// optional-service 没有 OwnerUserID
	_, err := r.mcpEditServer(context.Background(), stub, config, map[string]any{
		"server_name": "optional-service",
		"name":        "hacked",
	})
	if err == nil {
		t.Fatal("expected error when editing optional server")
	}
}

func TestMcpEditServerRequiresAtLeastOneField(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	_, err := r.mcpEditServer(context.Background(), stub, config, map[string]any{
		"server_name": "my-mcp",
	})
	if err == nil {
		t.Fatal("expected error when no fields to update")
	}
	if !strings.Contains(err.Error(), "未提供任何需要修改的字段") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMcpEditServerRejectsInvalidType(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	_, err := r.mcpEditServer(context.Background(), stub, config, map[string]any{
		"server_name": "my-mcp",
		"server_type": "grpc",
	})
	if err == nil {
		t.Fatal("expected error for invalid server_type")
	}
}

func TestMcpEditServerNotFound(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	_, err := r.mcpEditServer(context.Background(), stub, config, map[string]any{
		"server_name": "ghost",
		"name":        "new-name",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent server")
	}
}

// ---------------------------------------------------------------------------
// mcpDeleteServer 测试
// ---------------------------------------------------------------------------

func TestMcpDeleteServerSuccess(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpDeleteServer(context.Background(), stub, config, map[string]any{
		"server_name": "my-mcp",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["success"] != true {
		t.Fatal("expected success=true")
	}
	if !strings.Contains(result["message"].(string), "my-mcp") {
		t.Fatalf("unexpected message: %v", result["message"])
	}
	if stub.lastDeleteID != "user-1" {
		t.Fatalf("expected delete ID 'user-1', got %q", stub.lastDeleteID)
	}
}

func TestMcpDeleteServerRejectsNonUserServer(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	_, err := r.mcpDeleteServer(context.Background(), stub, config, map[string]any{
		"server_name": "system-tools",
	})
	if err == nil {
		t.Fatal("expected error when deleting non-user server")
	}
	if !strings.Contains(err.Error(), "不可删除") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMcpDeleteServerNotFound(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	_, err := r.mcpDeleteServer(context.Background(), stub, config, map[string]any{
		"server_name": "ghost",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent server")
	}
}

func TestMcpDeleteServerRequiresServerName(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{}
	config := mcpTestConfig()

	_, err := r.mcpDeleteServer(context.Background(), stub, config, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing server_name")
	}
}

func TestMcpDeleteServerProviderError(t *testing.T) {
	setupMcpTestEnv(t)
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{
		servers:   sampleMcpServers(),
		deleteErr: fmt.Errorf("permission denied"),
	}
	config := mcpTestConfig()

	_, err := r.mcpDeleteServer(context.Background(), stub, config, map[string]any{
		"server_name": "my-mcp",
	})
	if err == nil {
		t.Fatal("expected error from provider")
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// mcpServerGroup 辅助函数测试
// ---------------------------------------------------------------------------

func TestMcpServerGroup(t *testing.T) {
	tests := []struct {
		server   provider.MCPServerSummary
		expected string
	}{
		{provider.MCPServerSummary{IsSystem: true}, "默认加载"},
		{provider.MCPServerSummary{OwnerUserID: "user-1"}, "我的服务"},
		{provider.MCPServerSummary{}, "可选加载"},
	}
	for _, tt := range tests {
		got := mcpServerGroup(tt.server)
		if got != tt.expected {
			t.Errorf("mcpServerGroup(%+v) = %q, want %q", tt.server, got, tt.expected)
		}
	}
}

// ---------------------------------------------------------------------------
// findMcpServerByName 辅助函数测试
// ---------------------------------------------------------------------------

func TestFindMcpServerByName(t *testing.T) {
	servers := sampleMcpServers()

	found := findMcpServerByName(servers, "my-mcp")
	if found == nil {
		t.Fatal("expected to find my-mcp")
	}
	if found.ID != "user-1" {
		t.Fatalf("expected ID 'user-1', got %q", found.ID)
	}

	notFound := findMcpServerByName(servers, "nonexistent")
	if notFound != nil {
		t.Fatal("expected nil for nonexistent server")
	}

	empty := findMcpServerByName(nil, "anything")
	if empty != nil {
		t.Fatal("expected nil for empty list")
	}
}

// ---------------------------------------------------------------------------
// mcpManageClientToolDefinitions 工具定义完整性测试
// ---------------------------------------------------------------------------

func TestMcpManageClientToolDefinitions(t *testing.T) {
	defs := mcpManageClientToolDefinitions()
	if len(defs) != 7 {
		t.Fatalf("expected 7 tool definitions, got %d", len(defs))
	}

	expectedNames := []string{
		"mcp_list_servers", "mcp_list_tools", "mcp_toggle_server",
		"mcp_toggle_tool", "mcp_add_server", "mcp_edit_server", "mcp_delete_server",
	}
	for i, expected := range expectedNames {
		name := defs[i]["name"].(string)
		if name != expected {
			t.Errorf("tool[%d] name = %q, want %q", i, name, expected)
		}
		if defs[i]["description"] == nil || defs[i]["description"] == "" {
			t.Errorf("tool[%d] (%s) missing description", i, name)
		}
		if defs[i]["waiting_for_response"] != true {
			t.Errorf("tool[%d] (%s) should have waiting_for_response=true", i, name)
		}
		if defs[i]["inputSchema"] == nil {
			t.Errorf("tool[%d] (%s) missing inputSchema", i, name)
		}
	}
}

// ---------------------------------------------------------------------------
// mcpListTools 本地禁用工具状态测试
// ---------------------------------------------------------------------------

func TestMcpListToolsReflectsLocalDisabledTools(t *testing.T) {
	tmpDir := setupMcpTestEnv(t)
	configDir := filepath.Join(tmpDir, "config")
	os.MkdirAll(configDir, 0o755)
	os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(`{"disabled_server_ids":[],"disabled_tool_keys":["sys-1:search_web"]}`), 0o644)

	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpListTools(context.Background(), stub, config, map[string]any{
		"server_name": "system-tools",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tools := result["tools"].([]map[string]any)
	// search_web 被本地禁用
	if tools[0]["name"] != "search_web" {
		t.Fatalf("expected first tool to be search_web, got %v", tools[0]["name"])
	}
	if tools[0]["enabled"] != false {
		t.Fatal("expected search_web to be disabled locally")
	}
	// read_file 未被禁用
	if tools[1]["enabled"] != true {
		t.Fatal("expected read_file to remain enabled")
	}
}

// ---------------------------------------------------------------------------
// mcpListServers 本地禁用服务器状态测试
// ---------------------------------------------------------------------------

func TestMcpListServersReflectsLocalDisabledServers(t *testing.T) {
	tmpDir := setupMcpTestEnv(t)
	configDir := filepath.Join(tmpDir, "config")
	os.MkdirAll(configDir, 0o755)
	os.WriteFile(filepath.Join(configDir, "mcp.json"), []byte(`{"disabled_server_ids":["user-1"],"disabled_tool_keys":[]}`), 0o644)

	r := NewClientRuntime(nil, nil, nil)
	stub := &stubMcpProvider{servers: sampleMcpServers()}
	config := mcpTestConfig()

	result, err := r.mcpListServers(context.Background(), stub, config)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	groups := result["groups"].(map[string]any)
	userGroup := groups["我的服务"].([]map[string]any)
	if len(userGroup) != 1 {
		t.Fatalf("expected 1 server in 我的服务, got %d", len(userGroup))
	}
	if userGroup[0]["enabled"] != false {
		t.Fatal("expected my-mcp to be disabled locally")
	}

	// 验证 message 中的启用计数（系统1 + 可选1 = 2，用户被禁用）
	message := result["message"].(string)
	if !strings.Contains(message, "已启用 2 个") {
		t.Fatalf("expected 2 enabled servers, got message: %q", message)
	}
}
