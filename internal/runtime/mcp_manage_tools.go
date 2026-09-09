package runtime

import (
	"context"
	"fmt"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	internalmcp "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/mcp"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// mcpManageClientToolDefinitions 返回 MCP 管理相关的客户端工具定义。
// 复刻自 taiyiflow-chatui 的 mcp-builtin-tools.js，供模型在对话中直接调用管理 MCP 服务配置。
func mcpManageClientToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":                 "mcp_list_servers",
			"description":          "列出当前用户可访问的所有 MCP 服务器及其状态。返回按分组（默认加载/可选加载/我的服务）组织的服务器列表，包含名称、启用状态、类型、工具数量等信息。当用户询问\"有哪些 MCP 服务\"、\"MCP 配置\"、\"工具列表\"时调用。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "mcp_list_tools",
			"description":          "列出指定 MCP 服务器下的所有工具及其启用状态。当用户询问某个 MCP 服务\"有哪些工具\"、\"工具详情\"时调用。需要先通过 mcp_list_servers 获取服务器名称。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_name": map[string]any{
						"type":        "string",
						"description": "MCP 服务器名称（从 mcp_list_servers 返回的 name 字段获取）",
					},
				},
				"required":             []string{"server_name"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "mcp_toggle_server",
			"description":          "启用或禁用指定的 MCP 服务器。系统内置服务（默认加载分组）不可禁用。当用户说\"启用/禁用某个 MCP 服务\"、\"关闭某个服务\"时调用。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_name": map[string]any{
						"type":        "string",
						"description": "要操作的 MCP 服务器名称",
					},
					"enabled": map[string]any{
						"type":        "boolean",
						"description": "目标状态：true=启用，false=禁用",
					},
				},
				"required":             []string{"server_name", "enabled"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "mcp_toggle_tool",
			"description":          "启用或禁用指定 MCP 服务器下的单个工具。当用户说\"禁用某个工具\"、\"关闭某个功能\"时调用。需要先通过 mcp_list_tools 确认工具名称。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_name": map[string]any{
						"type":        "string",
						"description": "工具所属的 MCP 服务器名称",
					},
					"tool_name": map[string]any{
						"type":        "string",
						"description": "要操作的工具名称",
					},
					"enabled": map[string]any{
						"type":        "boolean",
						"description": "目标状态：true=启用，false=禁用",
					},
				},
				"required":             []string{"server_name", "tool_name", "enabled"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "mcp_add_server",
			"description":          "添加用户自建的 MCP 服务器。支持 Streamable HTTP、SSE 和 Stdio 三种类型。当用户说\"添加一个 MCP 服务\"、\"注册新的 MCP\"时调用。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "服务名称（仅允许英文、数字、下划线和连字符）",
					},
					"server_url": map[string]any{
						"type":        "string",
						"description": "服务地址。HTTP 类型如 http://localhost:8080/mcp；Stdio 类型如 stdio://command args",
					},
					"server_type": map[string]any{
						"type":        "string",
						"description": "服务类型",
						"enum":        []string{"streamable_http", "sse", "stdio"},
					},
					"description": map[string]any{
						"type":        "string",
						"description": "服务描述（可选）",
					},
					"default_enabled": map[string]any{
						"type":        "boolean",
						"description": "默认启用状态（可选，默认 true）。设为 false 时，其他客户端首次看到该服务时默认处于禁用状态",
					},
					"pass_user_identity": map[string]any{
						"type":        "boolean",
						"description": "是否传递用户登录令牌（可选，默认 false）。仅 streamable_http / sse 类型生效，开启后调用该服务时携带当前用户会话令牌（Bearer）",
					},
				},
				"required":             []string{"name", "server_url", "server_type"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "mcp_edit_server",
			"description":          "编辑用户自建的 MCP 服务器配置。仅允许编辑用户自己创建的服务（\"我的服务\"分组），系统内置和可选服务不可编辑。当用户说\"修改 MCP 服务配置\"、\"更新服务地址\"时调用。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_name": map[string]any{
						"type":        "string",
						"description": "要编辑的 MCP 服务器名称",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "新的服务名称（可选，不传则不修改）",
					},
					"server_url": map[string]any{
						"type":        "string",
						"description": "新的服务地址（可选，不传则不修改）",
					},
					"server_type": map[string]any{
						"type":        "string",
						"description": "新的服务类型（可选，不传则不修改）",
						"enum":        []string{"streamable_http", "sse", "stdio"},
					},
					"description": map[string]any{
						"type":        "string",
						"description": "新的服务描述（可选，不传则不修改）",
					},
					"default_enabled": map[string]any{
						"type":        "boolean",
						"description": "默认启用状态（可选，不传则不修改）。设为 false 时，其他客户端首次看到该服务时默认处于禁用状态",
					},
					"pass_user_identity": map[string]any{
						"type":        "boolean",
						"description": "是否传递用户登录令牌（可选，不传则不修改）。仅 streamable_http / sse 类型生效，开启后调用该服务时携带当前用户会话令牌（Bearer）",
					},
				},
				"required":             []string{"server_name"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "mcp_delete_server",
			"description":          "删除用户自建的 MCP 服务器。仅允许删除用户自己创建的服务（\"我的服务\"分组），系统内置和可选服务不可删除。删除后不可恢复。当用户说\"删除某个 MCP 服务\"、\"移除服务\"时调用。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_name": map[string]any{
						"type":        "string",
						"description": "要删除的 MCP 服务器名称",
					},
				},
				"required":             []string{"server_name"},
				"additionalProperties": false,
			},
		},
	}
}

// isMcpManageTool 判断工具名是否为 MCP 管理工具。
func isMcpManageTool(toolName string) bool {
	switch canonicalClientToolName(toolName) {
	case "mcp_list_servers", "mcp_list_tools", "mcp_toggle_server", "mcp_toggle_tool",
		"mcp_add_server", "mcp_edit_server", "mcp_delete_server":
		return true
	}
	return false
}

// mutatingMcpManageTool 判断 MCP 管理工具是否为变更操作（非只读）。
func mutatingMcpManageTool(toolName string) bool {
	switch canonicalClientToolName(toolName) {
	case "mcp_list_servers", "mcp_list_tools":
		return false
	default:
		return true
	}
}

// mcpManageTool 执行 MCP 管理工具。
func (r *ClientRuntime) mcpManageTool(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, toolName string, arguments map[string]any) (map[string]any, error) {
	if providerClient == nil {
		return nil, newToolError("PROVIDER_UNAVAILABLE", "MCP 管理需要已认证的后端连接")
	}
	switch canonicalClientToolName(toolName) {
	case "mcp_list_servers":
		return r.mcpListServers(ctx, providerClient, config)
	case "mcp_list_tools":
		return r.mcpListTools(ctx, providerClient, config, arguments)
	case "mcp_toggle_server":
		return r.mcpToggleServer(ctx, providerClient, config, arguments)
	case "mcp_toggle_tool":
		return r.mcpToggleTool(ctx, providerClient, config, arguments)
	case "mcp_add_server":
		return r.mcpAddServer(ctx, providerClient, config, arguments)
	case "mcp_edit_server":
		return r.mcpEditServer(ctx, providerClient, config, arguments)
	case "mcp_delete_server":
		return r.mcpDeleteServer(ctx, providerClient, config, arguments)
	default:
		return nil, newToolError("PARAM_ERROR", "不支持的 MCP 管理工具: %s", toolName)
	}
}

// supportsUserIdentity 判断服务类型是否支持传递用户登录会话令牌。
// 仅 streamable_http / sse 类型支持，stdio 类型不涉及 HTTP 鉴权头。
// 与 taiyiflow-chatui mcp-builtin-tools.js 的 supportsUserIdentity 对齐。
func supportsUserIdentity(serverType string) bool {
	return serverType == "streamable_http" || serverType == "sse"
}

// mcpServerGroup 获取服务器的分组标签。
func mcpServerGroup(server provider.MCPServerSummary) string {
	if server.IsSystem {
		return "默认加载"
	}
	if server.OwnerUserID != "" {
		return "我的服务"
	}
	return "可选加载"
}

// findMcpServerByName 根据名称查找服务器。
func findMcpServerByName(servers []provider.MCPServerSummary, name string) *provider.MCPServerSummary {
	for i := range servers {
		if servers[i].Name == name {
			return &servers[i]
		}
	}
	return nil
}

// mcpListServers 列出所有 MCP 服务器。
func (r *ClientRuntime) mcpListServers(ctx context.Context, providerClient provider.Provider, config internalconfig.Config) (map[string]any, error) {
	flowID := strings.TrimSpace(config.DefaultFlow)
	if flowID == "" {
		return nil, newToolError("PARAM_ERROR", "未配置默认智能体，请先执行 taiyiflow-cli agents use <名称>")
	}

	servers, err := providerClient.ListMCPServers(ctx, flowID, "cli")
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取 MCP 服务器列表失败: %v", err)
	}

	if len(servers) == 0 {
		return map[string]any{
			"message": "当前没有已加载的 MCP 服务器。",
			"groups":  map[string]any{},
		}, nil
	}

	// 加载本地禁用配置
	mcpConfig, err := internalmcp.Load()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "加载本地 MCP 配置失败: %v", err)
	}

	groups := map[string][]map[string]any{
		"默认加载": {},
		"可选加载": {},
		"我的服务": {},
	}

	for _, server := range servers {
		enabled := true
		if server.IsSystem {
			enabled = true
		} else if mcpConfig.IsServerDisabled(server.ID) {
			enabled = false
		} else if server.Enabled != nil && !*server.Enabled {
			enabled = false
		}

		enabledToolCount := 0
		for _, tool := range server.Tools {
			if tool.Enabled == nil || *tool.Enabled {
				enabledToolCount++
			}
		}

		group := mcpServerGroup(server)
		groups[group] = append(groups[group], map[string]any{
			"name":               server.Name,
			"enabled":            enabled,
			"server_type":        server.ServerType,
			"description":        server.Description,
			"tool_count":         len(server.Tools),
			"enabled_tool_count": enabledToolCount,
		})
	}

	// 移除空分组
	resultGroups := map[string]any{}
	for key, items := range groups {
		if len(items) > 0 {
			resultGroups[key] = items
		}
	}

	totalEnabled := 0
	for _, server := range servers {
		if server.IsSystem {
			totalEnabled++
		} else if !mcpConfig.IsServerDisabled(server.ID) && (server.Enabled == nil || *server.Enabled) {
			totalEnabled++
		}
	}

	return map[string]any{
		"message": fmt.Sprintf("共 %d 个 MCP 服务器，已启用 %d 个。", len(servers), totalEnabled),
		"groups":  resultGroups,
	}, nil
}

// mcpListTools 列出指定服务器的工具列表。
func (r *ClientRuntime) mcpListTools(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	serverName := argumentString(arguments, "server_name")
	if serverName == "" {
		return nil, newToolError("PARAM_ERROR", "server_name 不能为空")
	}

	flowID := strings.TrimSpace(config.DefaultFlow)
	if flowID == "" {
		return nil, newToolError("PARAM_ERROR", "未配置默认智能体")
	}

	servers, err := providerClient.ListMCPServers(ctx, flowID, "cli")
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取 MCP 服务器列表失败: %v", err)
	}

	server := findMcpServerByName(servers, serverName)
	if server == nil {
		return nil, newToolError("NOT_FOUND", "未找到名为\"%s\"的 MCP 服务器。请先调用 mcp_list_servers 查看可用服务器。", serverName)
	}

	// 加载本地禁用配置
	mcpConfig, err := internalmcp.Load()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "加载本地 MCP 配置失败: %v", err)
	}

	if len(server.Tools) == 0 {
		return map[string]any{
			"server_name": server.Name,
			"message":     fmt.Sprintf("服务器\"%s\"下暂无工具。", server.Name),
			"tools":       []any{},
		}, nil
	}

	tools := make([]map[string]any, 0, len(server.Tools))
	for _, tool := range server.Tools {
		enabled := tool.Enabled == nil || *tool.Enabled
		// 检查本地工具级禁用
		toolKey := server.ID + ":" + tool.Name
		if mcpConfig.IsToolDisabled(toolKey) {
			enabled = false
		}
		tools = append(tools, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"enabled":     enabled,
		})
	}

	return map[string]any{
		"server_name": server.Name,
		"message":     fmt.Sprintf("服务器\"%s\"共有 %d 个工具。", server.Name, len(tools)),
		"tools":       tools,
	}, nil
}

// mcpToggleServer 启用/禁用 MCP 服务器。
func (r *ClientRuntime) mcpToggleServer(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	serverName := argumentString(arguments, "server_name")
	if serverName == "" {
		return nil, newToolError("PARAM_ERROR", "server_name 不能为空")
	}
	enabled := argumentBool(arguments, "enabled")

	flowID := strings.TrimSpace(config.DefaultFlow)
	if flowID == "" {
		return nil, newToolError("PARAM_ERROR", "未配置默认智能体")
	}

	servers, err := providerClient.ListMCPServers(ctx, flowID, "cli")
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取 MCP 服务器列表失败: %v", err)
	}

	server := findMcpServerByName(servers, serverName)
	if server == nil {
		return nil, newToolError("NOT_FOUND", "未找到名为\"%s\"的 MCP 服务器。", serverName)
	}

	if server.IsSystem && !enabled {
		return nil, newToolError("PERMISSION_DENIED", "\"%s\"是系统内置服务，不可禁用。", server.Name)
	}

	// 检查当前状态
	mcpConfig, err := internalmcp.Load()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "加载本地 MCP 配置失败: %v", err)
	}

	currentDisabled := mcpConfig.IsServerDisabled(server.ID)
	if enabled && !currentDisabled {
		return map[string]any{
			"success": true,
			"message": fmt.Sprintf("服务器\"%s\"已处于启用状态，无需操作。", server.Name),
		}, nil
	}
	if !enabled && currentDisabled {
		return map[string]any{
			"success": true,
			"message": fmt.Sprintf("服务器\"%s\"已处于禁用状态，无需操作。", server.Name),
		}, nil
	}

	if enabled {
		if err := internalmcp.RemoveDisabledServer(server.ID); err != nil {
			return nil, newToolError("EXECUTION_FAILED", "启用 MCP 服务器失败: %v", err)
		}
	} else {
		if err := internalmcp.AddDisabledServer(server.ID); err != nil {
			return nil, newToolError("EXECUTION_FAILED", "禁用 MCP 服务器失败: %v", err)
		}
	}

	action := "启用"
	if !enabled {
		action = "禁用"
	}
	return map[string]any{
		"success": true,
		"message": fmt.Sprintf("已%s MCP 服务器\"%s\"。", action, server.Name),
	}, nil
}

// mcpToggleTool 启用/禁用单个工具。
func (r *ClientRuntime) mcpToggleTool(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	serverName := argumentString(arguments, "server_name")
	toolName := argumentString(arguments, "tool_name")
	if serverName == "" {
		return nil, newToolError("PARAM_ERROR", "server_name 不能为空")
	}
	if toolName == "" {
		return nil, newToolError("PARAM_ERROR", "tool_name 不能为空")
	}
	enabled := argumentBool(arguments, "enabled")

	flowID := strings.TrimSpace(config.DefaultFlow)
	if flowID == "" {
		return nil, newToolError("PARAM_ERROR", "未配置默认智能体")
	}

	servers, err := providerClient.ListMCPServers(ctx, flowID, "cli")
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取 MCP 服务器列表失败: %v", err)
	}

	server := findMcpServerByName(servers, serverName)
	if server == nil {
		return nil, newToolError("NOT_FOUND", "未找到名为\"%s\"的 MCP 服务器。", serverName)
	}

	// 查找工具
	var foundTool *provider.MCPToolSummary
	for i := range server.Tools {
		if server.Tools[i].Name == toolName {
			foundTool = &server.Tools[i]
			break
		}
	}
	if foundTool == nil {
		return nil, newToolError("NOT_FOUND", "服务器\"%s\"下未找到名为\"%s\"的工具。请先调用 mcp_list_tools 查看可用工具。", server.Name, toolName)
	}

	toolKey := server.ID + ":" + toolName
	mcpConfig, err := internalmcp.Load()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "加载本地 MCP 配置失败: %v", err)
	}

	currentDisabled := mcpConfig.IsToolDisabled(toolKey)
	if enabled && !currentDisabled {
		return map[string]any{
			"success": true,
			"message": fmt.Sprintf("工具\"%s\"已处于启用状态，无需操作。", toolName),
		}, nil
	}
	if !enabled && currentDisabled {
		return map[string]any{
			"success": true,
			"message": fmt.Sprintf("工具\"%s\"已处于禁用状态，无需操作。", toolName),
		}, nil
	}

	if enabled {
		if err := internalmcp.RemoveDisabledTool(toolKey); err != nil {
			return nil, newToolError("EXECUTION_FAILED", "启用工具失败: %v", err)
		}
	} else {
		if err := internalmcp.AddDisabledTool(toolKey); err != nil {
			return nil, newToolError("EXECUTION_FAILED", "禁用工具失败: %v", err)
		}
	}

	action := "启用"
	if !enabled {
		action = "禁用"
	}
	return map[string]any{
		"success": true,
		"message": fmt.Sprintf("已%s服务器\"%s\"下的工具\"%s\"。", action, server.Name, toolName),
	}, nil
}

// mcpAddServer 添加用户自建 MCP 服务器。
func (r *ClientRuntime) mcpAddServer(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	name := argumentString(arguments, "name")
	serverURL := argumentString(arguments, "server_url")
	serverType := argumentString(arguments, "server_type")
	description := argumentString(arguments, "description")

	if name == "" {
		return nil, newToolError("PARAM_ERROR", "name 不能为空")
	}
	if serverURL == "" {
		return nil, newToolError("PARAM_ERROR", "server_url 不能为空")
	}
	if serverType == "" {
		serverType = "streamable_http"
	}

	// 校验服务器类型
	validTypes := map[string]bool{"stdio": true, "sse": true, "streamable_http": true}
	if !validTypes[serverType] {
		return nil, newToolError("PARAM_ERROR", "不支持的服务器类型: %s，可选: stdio、sse、streamable_http", serverType)
	}

	flowID := strings.TrimSpace(config.DefaultFlow)
	if flowID == "" {
		return nil, newToolError("PARAM_ERROR", "未配置默认智能体，请先执行 taiyiflow-cli agents use <名称>")
	}

	// 检查名称是否已存在
	servers, err := providerClient.ListMCPServers(ctx, flowID, "cli")
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取 MCP 服务器列表失败: %v", err)
	}
	if findMcpServerByName(servers, name) != nil {
		return nil, newToolError("PARAM_ERROR", "名为\"%s\"的 MCP 服务器已存在，请使用其他名称。", name)
	}

	request := provider.MCPServerCreateRequest{
		Name:        name,
		ServerURL:   serverURL,
		FlowID:      flowID,
		ServerType:  serverType,
		Description: description,
		ClientType:  "cli",
	}

	// default_enabled：默认 true，用户可设为 false 表示新客户端默认禁用
	if v, ok := arguments["default_enabled"]; ok {
		if boolVal, isBool := v.(bool); isBool {
			request.DefaultEnabled = &boolVal
		}
	}

	// pass_user_identity：仅 streamable_http / sse 类型支持传递用户令牌，其它类型强制 false
	if v, ok := arguments["pass_user_identity"]; ok {
		if boolVal, isBool := v.(bool); isBool {
			passIdentity := supportsUserIdentity(serverType) && boolVal
			request.PassUserIdentity = &passIdentity
		}
	}

	newServer, err := providerClient.CreateMCPServer(ctx, request)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "添加 MCP 服务器失败: %v", err)
	}

	return map[string]any{
		"success": true,
		"message": fmt.Sprintf("已成功添加 MCP 服务器\"%s\"（类型：%s）。", newServer.Name, serverType),
		"server": map[string]any{
			"id":          newServer.ID,
			"name":        newServer.Name,
			"server_url":  newServer.ServerURL,
			"server_type": newServer.ServerType,
		},
	}, nil
}

// mcpEditServer 编辑用户自建 MCP 服务器。
func (r *ClientRuntime) mcpEditServer(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	serverName := argumentString(arguments, "server_name")
	if serverName == "" {
		return nil, newToolError("PARAM_ERROR", "server_name 不能为空")
	}

	flowID := strings.TrimSpace(config.DefaultFlow)
	if flowID == "" {
		return nil, newToolError("PARAM_ERROR", "未配置默认智能体")
	}

	servers, err := providerClient.ListMCPServers(ctx, flowID, "cli")
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取 MCP 服务器列表失败: %v", err)
	}

	server := findMcpServerByName(servers, serverName)
	if server == nil {
		return nil, newToolError("NOT_FOUND", "未找到名为\"%s\"的 MCP 服务器。", serverName)
	}

	if server.OwnerUserID == "" {
		return nil, newToolError("PERMISSION_DENIED", "\"%s\"不是用户自建服务，不可编辑。仅\"我的服务\"分组中的服务器支持编辑。", server.Name)
	}

	// 构建更新数据（仅包含用户传入的字段）
	updateRequest := provider.MCPServerUpdateRequest{}
	hasUpdate := false

	if v := argumentString(arguments, "name"); v != "" {
		updateRequest.Name = &v
		hasUpdate = true
	}
	if v := argumentString(arguments, "server_url"); v != "" {
		updateRequest.ServerURL = &v
		hasUpdate = true
	}
	if v := argumentString(arguments, "server_type"); v != "" {
		validTypes := map[string]bool{"stdio": true, "sse": true, "streamable_http": true}
		if !validTypes[v] {
			return nil, newToolError("PARAM_ERROR", "不支持的服务器类型: %s，可选: stdio、sse、streamable_http", v)
		}
		updateRequest.ServerType = &v
		hasUpdate = true
	}
	if v, ok := arguments["description"]; ok {
		desc := stringValue(v)
		updateRequest.Description = &desc
		hasUpdate = true
	}
	if v, ok := arguments["default_enabled"]; ok {
		if boolVal, isBool := v.(bool); isBool {
			updateRequest.DefaultEnabled = &boolVal
			hasUpdate = true
		}
	}
	if v, ok := arguments["pass_user_identity"]; ok {
		if boolVal, isBool := v.(bool); isBool {
			// 以编辑后的最终服务类型判断是否支持传递用户令牌（未传 server_type 时沿用原类型），
			// 与 taiyiflow-chatui handleMcpEditServer 逻辑对齐
			effectiveServerType := server.ServerType
			if updateRequest.ServerType != nil {
				effectiveServerType = *updateRequest.ServerType
			}
			passIdentity := supportsUserIdentity(effectiveServerType) && boolVal
			updateRequest.PassUserIdentity = &passIdentity
			hasUpdate = true
		}
	}

	if !hasUpdate {
		return nil, newToolError("PARAM_ERROR", "未提供任何需要修改的字段。可修改：name、server_url、server_type、description、default_enabled、pass_user_identity。")
	}

	updated, err := providerClient.UpdateMCPServer(ctx, server.ID, updateRequest)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "编辑 MCP 服务器失败: %v", err)
	}

	return map[string]any{
		"success": true,
		"message": fmt.Sprintf("已成功更新 MCP 服务器\"%s\"。", updated.Name),
		"server": map[string]any{
			"id":          updated.ID,
			"name":        updated.Name,
			"server_url":  updated.ServerURL,
			"server_type": updated.ServerType,
			"description": updated.Description,
		},
	}, nil
}

// mcpDeleteServer 删除用户自建 MCP 服务器。
func (r *ClientRuntime) mcpDeleteServer(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	serverName := argumentString(arguments, "server_name")
	if serverName == "" {
		return nil, newToolError("PARAM_ERROR", "server_name 不能为空")
	}

	flowID := strings.TrimSpace(config.DefaultFlow)
	if flowID == "" {
		return nil, newToolError("PARAM_ERROR", "未配置默认智能体")
	}

	servers, err := providerClient.ListMCPServers(ctx, flowID, "cli")
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取 MCP 服务器列表失败: %v", err)
	}

	server := findMcpServerByName(servers, serverName)
	if server == nil {
		return nil, newToolError("NOT_FOUND", "未找到名为\"%s\"的 MCP 服务器。", serverName)
	}

	if server.OwnerUserID == "" {
		return nil, newToolError("PERMISSION_DENIED", "\"%s\"不是用户自建服务，不可删除。仅\"我的服务\"分组中的服务器支持删除。", server.Name)
	}

	if err := providerClient.DeleteMCPServer(ctx, server.ID); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "删除 MCP 服务器失败: %v", err)
	}

	// 同时清理本地禁用配置中的相关条目
	_ = internalmcp.RemoveDisabledServer(server.ID)

	return map[string]any{
		"success": true,
		"message": fmt.Sprintf("已成功删除 MCP 服务器\"%s\"。", server.Name),
	}, nil
}

// mcpManagePreview 生成 MCP 管理工具的操作预览文本。
func mcpManagePreview(toolName string, arguments map[string]any) string {
	switch canonicalClientToolName(toolName) {
	case "mcp_list_servers":
		return "mcp_list_servers"
	case "mcp_list_tools":
		return fmt.Sprintf("mcp_list_tools %s", fallbackString(argumentString(arguments, "server_name"), "<missing>"))
	case "mcp_toggle_server":
		enabled := argumentBool(arguments, "enabled")
		action := "启用"
		if !enabled {
			action = "禁用"
		}
		return fmt.Sprintf("mcp_toggle_server %s → %s", fallbackString(argumentString(arguments, "server_name"), "<missing>"), action)
	case "mcp_toggle_tool":
		enabled := argumentBool(arguments, "enabled")
		action := "启用"
		if !enabled {
			action = "禁用"
		}
		return fmt.Sprintf("mcp_toggle_tool %s/%s → %s", fallbackString(argumentString(arguments, "server_name"), "<missing>"), fallbackString(argumentString(arguments, "tool_name"), "<missing>"), action)
	case "mcp_add_server":
		return fmt.Sprintf("mcp_add_server %s (%s)", fallbackString(argumentString(arguments, "name"), "<missing>"), fallbackString(argumentString(arguments, "server_type"), "streamable_http"))
	case "mcp_edit_server":
		return fmt.Sprintf("mcp_edit_server %s", fallbackString(argumentString(arguments, "server_name"), "<missing>"))
	case "mcp_delete_server":
		return fmt.Sprintf("mcp_delete_server %s", fallbackString(argumentString(arguments, "server_name"), "<missing>"))
	default:
		return toolName
	}
}
