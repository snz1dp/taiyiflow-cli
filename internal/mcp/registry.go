package mcp

import (
	"context"
	"fmt"
	"sync"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// Registry 是 MCP 服务器注册表，负责：
// 1. 从后端加载 MCP 服务器列表
// 2. 应用本地禁用配置（mcp.json）
// 3. 构建 client_mcp_servers 格式的工具定义
// 4. 路由工具调用到对应的 MCP 服务器
//
// 与 taiyiflow-chatui 的 mcp-store.js 功能对齐。
type Registry struct {
	mu      sync.RWMutex
	client  *Client
	servers []ServerConfig
	loaded  bool
}

// NewRegistry 创建 MCP 服务器注册表。
func NewRegistry() *Registry {
	return &Registry{
		client: NewClient(),
	}
}

// LoadServers 从后端加载 MCP 服务器列表，并应用本地禁用配置。
// 与 chatui mcp-store.js 的 loadServers 逻辑对齐：
// - 系统内置服务（is_system=true）始终启用
// - 非系统服务若 ID 在本地禁用集合中则标记为禁用
// - 工具级禁用通过 disabled_tool_keys 过滤
func (r *Registry) LoadServers(ctx context.Context, providerClient provider.Provider, flowID string) error {
	if flowID == "" {
		return nil
	}

	servers, err := providerClient.ListMCPServers(ctx, flowID, "cli")
	if err != nil {
		return fmt.Errorf("加载 MCP 服务器列表失败: %w", err)
	}

	// 加载本地禁用配置
	mcpConfig, err := Load()
	if err != nil {
		// 配置读取失败不阻塞，使用空配置
		mcpConfig = &MCPConfig{}
	}
	disabledServerIDs := make(map[string]bool, len(mcpConfig.DisabledServerIDs))
	for _, id := range mcpConfig.DisabledServerIDs {
		disabledServerIDs[id] = true
	}
	disabledToolKeys := make(map[string]bool, len(mcpConfig.DisabledToolKeys))
	for _, key := range mcpConfig.DisabledToolKeys {
		disabledToolKeys[key] = true
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.servers = nil

	for _, srv := range servers {
		// 系统内置服务始终启用；非系统服务检查本地禁用
		if !srv.IsSystem && disabledServerIDs[srv.ID] {
			continue
		}

		// 提取工具列表，过滤被本地禁用的工具
		var enabledTools []ToolDefinition
		for _, tool := range srv.Tools {
			toolKey := srv.ID + ":" + tool.Name
			if disabledToolKeys[toolKey] {
				continue
			}
			enabledTools = append(enabledTools, ToolDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				InputSchema: tool.InputSchema,
			})
		}

		if len(enabledTools) == 0 {
			continue
		}

		serverConfig := ServerConfig{
			ID:         srv.ID,
			Name:       srv.Name,
			ServerURL:  srv.ServerURL,
			ServerType: srv.ServerType,
			AuthConfig: srv.AuthConfig,
			Tools:      enabledTools,
		}
		r.servers = append(r.servers, serverConfig)
	}

	r.loaded = true
	return nil
}

// ClientMCPServers 构建 client_mcp_servers 格式的工具定义。
// 返回 { serverName: [{name, description, input_schema}] }，
// 与 chatui mcp-store.js 的 enabledToolsByServerName getter 对齐。
func (r *Registry) ClientMCPServers() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]any, len(r.servers))
	for _, srv := range r.servers {
		if len(srv.Tools) == 0 {
			continue
		}
		tools := make([]map[string]any, 0, len(srv.Tools))
		for _, tool := range srv.Tools {
			tools = append(tools, map[string]any{
				"name":         tool.Name,
				"description":  tool.Description,
				"input_schema": tool.InputSchema,
			})
		}
		result[srv.Name] = tools
	}
	return result
}

// CallTool 路由工具调用到对应的 MCP 服务器。
// serverName 是 client_tool 事件中 server.name 字段的值。
func (r *Registry) CallTool(ctx context.Context, serverName, toolName string, toolArgs map[string]any) (any, error) {
	r.mu.RLock()
	var target *ServerConfig
	for i := range r.servers {
		if r.servers[i].Name == serverName {
			target = &r.servers[i]
			break
		}
	}
	r.mu.RUnlock()

	if target == nil {
		return nil, fmt.Errorf("未找到 MCP 服务器: %s", serverName)
	}

	return r.client.CallTool(ctx, *target, toolName, toolArgs)
}

// HasServer 检查是否存在指定名称的 MCP 服务器。
func (r *Registry) HasServer(serverName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, srv := range r.servers {
		if srv.Name == serverName {
			return true
		}
	}
	return false
}

// IsLoaded 返回是否已成功加载过服务器列表。
func (r *Registry) IsLoaded() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loaded
}

// Close 清理所有连接资源。
func (r *Registry) Close() {
	r.client.Close()
}
