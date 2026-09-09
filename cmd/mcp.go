package cmd

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	internalmcp "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/mcp"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// runMcp 是 MCP 供应及管理子命令的入口。
// 用法: taiyiflow-cli mcp <子命令> [参数]
func (a *App) runMcp(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return a.showMcpHelp()
	}

	switch args[0] {
	case "help", "-h", "--help":
		return a.showMcpHelp()
	case "list":
		return a.mcpList(ctx, args[1:])
	case "add":
		return a.mcpAdd(ctx, args[1:])
	case "edit", "update":
		return a.mcpEdit(ctx, args[1:])
	case "remove", "rm", "delete":
		return a.mcpRemove(ctx, args[1:])
	case "enable":
		return a.mcpEnable(args[1:])
	case "disable":
		return a.mcpDisable(args[1:])
	case "enable-tool":
		return a.mcpEnableTool(args[1:])
	case "disable-tool":
		return a.mcpDisableTool(args[1:])
	case "status":
		return a.mcpStatus()
	default:
		return fmt.Errorf("不支持的 mcp 子命令: %s，可使用 taiyiflow-cli mcp help 查看用法", args[0])
	}
}

// showMcpHelp 输出 MCP 子命令帮助文本。
func (a *App) showMcpHelp() error {
	lines := []string{
		"MCP 命令用于管理模型上下文协议（MCP）服务器的供应与本地启停配置。",
		"",
		"用法:",
		"  taiyiflow-cli mcp list                          列出当前智能体的 MCP 服务器",
		"  taiyiflow-cli mcp add [选项]                    添加用户自建 MCP 服务器",
		"  taiyiflow-cli mcp edit <服务器ID> [选项]        编辑用户自建 MCP 服务器",
		"  taiyiflow-cli mcp remove <服务器ID>             删除用户自建 MCP 服务器",
		"  taiyiflow-cli mcp enable <服务器ID>             启用 MCP 服务器（本地）",
		"  taiyiflow-cli mcp disable <服务器ID>            禁用 MCP 服务器（本地）",
		"  taiyiflow-cli mcp enable-tool <服务器ID> <工具名>  启用指定工具（本地）",
		"  taiyiflow-cli mcp disable-tool <服务器ID> <工具名> 禁用指定工具（本地）",
		"  taiyiflow-cli mcp status                        查看本地 MCP 禁用配置",
		"  taiyiflow-cli mcp help                          显示本帮助",
		"",
		"add 选项:",
		"  --name <名称>              服务器名称（必填）",
		"  --url <地址>               服务器地址（必填），stdio 类型格式: stdio://command args...",
		"  --type <类型>              服务器类型: stdio、sse、streamable_http、other（默认 streamable_http）",
		"  --description <描述>       服务器描述",
		"  --default-enabled <bool>   默认启用状态（默认 true），设为 false 时新客户端默认禁用",
		"  --pass-user-identity <bool> 调用时传递用户登录会话令牌（默认 false），仅 sse、streamable_http 类型生效",
		"  --agent <ID>               指定智能体 ID（默认使用当前默认智能体）",
		"",
		"edit 选项:",
		"  --name <名称>              服务器名称",
		"  --url <地址>               服务器地址",
		"  --type <类型>              服务器类型: stdio、sse、streamable_http、other",
		"  --description <描述>       服务器描述",
		"  --default-enabled <bool>   默认启用状态",
		"  --pass-user-identity <bool> 调用时传递用户登录会话令牌，仅 sse、streamable_http 类型生效",
		"  --agent <ID>               指定智能体 ID（默认使用当前默认智能体）",
		"",
		"说明:",
		"  启用/禁用状态保存在本地配置文件 ~/.snz1dp/config/mcp.json，",
		"  与太乙智启桌面端（taiyiflow-chatui）共享同一配置文件。",
		"  系统内置服务器（is_system=true）不可被禁用。",
		"  --default-enabled 控制其他客户端首次看到该服务时是否默认启用。",
		"",
		"示例:",
		"  taiyiflow-cli mcp list",
		"  taiyiflow-cli mcp add --name my-server --url https://mcp.example.com/sse --type sse",
		"  taiyiflow-cli mcp add --name local-tool --url \"stdio://npx -y @some/mcp-server\" --type stdio",
		"  taiyiflow-cli mcp add --name my-server --url https://mcp.example.com/sse --default-enabled false",
		"  taiyiflow-cli mcp edit abc123 --default-enabled false",
		"  taiyiflow-cli mcp disable abc123",
		"  taiyiflow-cli mcp enable abc123",
		"  taiyiflow-cli mcp disable-tool abc123 search_web",
	}
	for _, line := range lines {
		fmt.Fprintln(a.stdout, line)
	}
	return nil
}

// mcpList 列出当前智能体的 MCP 服务器列表，并标注本地禁用状态。
func (a *App) mcpList(ctx context.Context, args []string) error {
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	client := buildProviderClient(config)
	config, err = a.ensureDefaultFlow(ctx, client, config)
	if err != nil {
		return err
	}

	flowID := config.DefaultFlow
	// 支持通过位置参数覆盖 flow ID
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		flowID = strings.TrimSpace(args[0])
	}
	if strings.TrimSpace(flowID) == "" {
		return fmt.Errorf("未配置默认智能体，请先执行 taiyiflow-cli agents use <名称> 或使用 --agent 指定")
	}

	servers, err := client.ListMCPServers(ctx, flowID, "cli")
	if err != nil {
		return err
	}

	if len(servers) == 0 {
		fmt.Fprintln(a.stdout, "当前智能体未注册 MCP 服务器。")
		return nil
	}

	// 加载本地禁用配置
	mcpConfig, err := internalmcp.Load()
	if err != nil {
		return fmt.Errorf("加载本地 MCP 配置失败: %w", err)
	}

	writer := tabwriter.NewWriter(a.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\t名称\t类型\t状态\t来源\t描述")
	for _, server := range servers {
		status := "启用"
		if server.IsSystem {
			status = "启用(系统)"
		} else if mcpConfig.IsServerDisabled(server.ID) {
			status = "禁用"
		} else if server.Enabled != nil && !*server.Enabled {
			status = "禁用"
		}

		source := "平台"
		if server.OwnerUserID != "" {
			source = "用户自建"
		} else if server.IsSystem {
			source = "系统内置"
		}

		description := strings.ReplaceAll(strings.TrimSpace(server.Description), "\n", " ")
		if len(description) > 40 {
			description = description[:40] + "..."
		}

		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n",
			server.ID, server.Name, server.ServerType, status, source, description)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("输出 MCP 服务器列表失败: %w", err)
	}

	// 输出工具级禁用信息
	if len(mcpConfig.DisabledToolKeys) > 0 {
		fmt.Fprintf(a.stdout, "\n本地禁用的工具 (%d 个):\n", len(mcpConfig.DisabledToolKeys))
		for _, key := range mcpConfig.DisabledToolKeys {
			fmt.Fprintf(a.stdout, "  - %s\n", key)
		}
	}

	return nil
}

// mcpAdd 添加用户自建 MCP 服务器。
func (a *App) mcpAdd(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("mcp add", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	name := flagSet.String("name", "", "服务器名称（必填）")
	serverURL := flagSet.String("url", "", "服务器地址（必填）")
	serverType := flagSet.String("type", "streamable_http", "服务器类型: stdio、sse、streamable_http、other")
	description := flagSet.String("description", "", "服务器描述")
	defaultEnabled := flagSet.Bool("default-enabled", true, "默认启用状态，设为 false 时新客户端默认禁用")
	passUserIdentity := flagSet.Bool("pass-user-identity", false, "调用时传递用户登录会话令牌，仅 sse、streamable_http 类型生效")
	agentID := flagSet.String("agent", "", "指定智能体 ID")

	if err := flagSet.Parse(args); err != nil {
		return err
	}

	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name 不能为空")
	}
	if strings.TrimSpace(*serverURL) == "" {
		return fmt.Errorf("--url 不能为空")
	}

	// 校验服务器类型
	validTypes := map[string]bool{"stdio": true, "sse": true, "streamable_http": true, "other": true}
	if !validTypes[*serverType] {
		return fmt.Errorf("不支持的服务器类型: %s，可选: stdio、sse、streamable_http、other", *serverType)
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	client := buildProviderClient(config)
	config, err = a.ensureDefaultFlow(ctx, client, config)
	if err != nil {
		return err
	}

	flowID := config.DefaultFlow
	if strings.TrimSpace(*agentID) != "" {
		flowID = strings.TrimSpace(*agentID)
	}
	if strings.TrimSpace(flowID) == "" {
		return fmt.Errorf("未配置默认智能体，请先执行 taiyiflow-cli agents use <名称> 或使用 --agent 指定")
	}

	createData := provider.MCPServerCreateRequest{
		Name:           strings.TrimSpace(*name),
		ServerURL:      strings.TrimSpace(*serverURL),
		FlowID:         flowID,
		ServerType:     *serverType,
		ClientType:     "cli",
		DefaultEnabled: defaultEnabled,
	}
	if strings.TrimSpace(*description) != "" {
		createData.Description = strings.TrimSpace(*description)
	}
	// pass_user_identity 仅 sse/streamable_http 类型生效，其它类型强制 false
	passIdentity := *passUserIdentity && (*serverType == "sse" || *serverType == "streamable_http")
	createData.PassUserIdentity = &passIdentity

	server, err := client.CreateMCPServer(ctx, createData)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "已添加 MCP 服务器: %s (ID: %s)\n", server.Name, server.ID)
	return nil
}

// mcpEdit 编辑用户自建 MCP 服务器。
func (a *App) mcpEdit(ctx context.Context, args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("用法: taiyiflow-cli mcp edit <服务器ID> [选项]")
	}
	serverID := strings.TrimSpace(args[0])

	flagSet := flag.NewFlagSet("mcp edit", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	name := flagSet.String("name", "", "服务器名称")
	serverURL := flagSet.String("url", "", "服务器地址")
	serverType := flagSet.String("type", "", "服务器类型: stdio、sse、streamable_http、other")
	description := flagSet.String("description", "", "服务器描述")
	defaultEnabled := flagSet.String("default-enabled", "", "默认启用状态: true 或 false")
	passUserIdentity := flagSet.String("pass-user-identity", "", "调用时传递用户登录会话令牌: true 或 false，仅 sse、streamable_http 类型生效")
	agentID := flagSet.String("agent", "", "指定智能体 ID")

	if err := flagSet.Parse(args[1:]); err != nil {
		return err
	}

	// 校验服务器类型（仅在指定时）
	if strings.TrimSpace(*serverType) != "" {
		validTypes := map[string]bool{"stdio": true, "sse": true, "streamable_http": true, "other": true}
		if !validTypes[*serverType] {
			return fmt.Errorf("不支持的服务器类型: %s，可选: stdio、sse、streamable_http、other", *serverType)
		}
	}

	// 解析 default-enabled 布尔值
	var defaultEnabledPtr *bool
	if strings.TrimSpace(*defaultEnabled) != "" {
		val := strings.TrimSpace(*defaultEnabled)
		if val == "true" || val == "1" {
			b := true
			defaultEnabledPtr = &b
		} else if val == "false" || val == "0" {
			b := false
			defaultEnabledPtr = &b
		} else {
			return fmt.Errorf("--default-enabled 值无效: %s，可选: true、false", val)
		}
	}

	// 构建更新请求（仅包含用户传入的字段）
	updateData := provider.MCPServerUpdateRequest{}
	hasUpdate := false

	if strings.TrimSpace(*name) != "" {
		v := strings.TrimSpace(*name)
		updateData.Name = &v
		hasUpdate = true
	}
	if strings.TrimSpace(*serverURL) != "" {
		v := strings.TrimSpace(*serverURL)
		updateData.ServerURL = &v
		hasUpdate = true
	}
	if strings.TrimSpace(*serverType) != "" {
		v := strings.TrimSpace(*serverType)
		updateData.ServerType = &v
		hasUpdate = true
	}
	if strings.TrimSpace(*description) != "" {
		v := strings.TrimSpace(*description)
		updateData.Description = &v
		hasUpdate = true
	}
	if defaultEnabledPtr != nil {
		updateData.DefaultEnabled = defaultEnabledPtr
		hasUpdate = true
	}

	// 解析 pass-user-identity 布尔值
	var passUserIdentityPtr *bool
	if strings.TrimSpace(*passUserIdentity) != "" {
		val := strings.TrimSpace(*passUserIdentity)
		if val == "true" || val == "1" {
			b := true
			passUserIdentityPtr = &b
		} else if val == "false" || val == "0" {
			b := false
			passUserIdentityPtr = &b
		} else {
			return fmt.Errorf("--pass-user-identity 值无效: %s，可选: true、false", val)
		}
	}

	// pass_user_identity 仅 sse/streamable_http 类型生效；
	// 若本次同时指定了 --type 则按最终类型校验，非支持类型强制 false
	if passUserIdentityPtr != nil {
		if updateData.ServerType != nil && *passUserIdentityPtr &&
			*updateData.ServerType != "sse" && *updateData.ServerType != "streamable_http" {
			b := false
			passUserIdentityPtr = &b
		}
		updateData.PassUserIdentity = passUserIdentityPtr
		hasUpdate = true
	}

	if !hasUpdate {
		return fmt.Errorf("未提供任何需要修改的字段。可修改: --name、--url、--type、--description、--default-enabled、--pass-user-identity")
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	client := buildProviderClient(config)
	config, err = a.ensureDefaultFlow(ctx, client, config)
	if err != nil {
		return err
	}

	flowID := config.DefaultFlow
	if strings.TrimSpace(*agentID) != "" {
		flowID = strings.TrimSpace(*agentID)
	}
	if strings.TrimSpace(flowID) == "" {
		return fmt.Errorf("未配置默认智能体，请先执行 taiyiflow-cli agents use <名称> 或使用 --agent 指定")
	}

	updated, err := client.UpdateMCPServer(ctx, serverID, updateData)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "已更新 MCP 服务器: %s (ID: %s)\n", updated.Name, updated.ID)
	return nil
}

// mcpRemove 删除用户自建 MCP 服务器。
func (a *App) mcpRemove(ctx context.Context, args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("用法: taiyiflow-cli mcp remove <服务器ID>")
	}
	serverID := strings.TrimSpace(args[0])

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	client := buildProviderClient(config)
	if err := client.DeleteMCPServer(ctx, serverID); err != nil {
		return err
	}

	// 同时清理本地禁用配置中的相关条目
	_ = internalmcp.RemoveDisabledServer(serverID)

	fmt.Fprintf(a.stdout, "已删除 MCP 服务器: %s\n", serverID)
	return nil
}

// mcpEnable 启用 MCP 服务器（从本地禁用列表移除）。
func (a *App) mcpEnable(args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("用法: taiyiflow-cli mcp enable <服务器ID>")
	}
	serverID := strings.TrimSpace(args[0])

	if err := internalmcp.RemoveDisabledServer(serverID); err != nil {
		return fmt.Errorf("启用 MCP 服务器失败: %w", err)
	}

	fmt.Fprintf(a.stdout, "已启用 MCP 服务器: %s\n", serverID)
	return nil
}

// mcpDisable 禁用 MCP 服务器（加入本地禁用列表）。
func (a *App) mcpDisable(args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("用法: taiyiflow-cli mcp disable <服务器ID>")
	}
	serverID := strings.TrimSpace(args[0])

	if err := internalmcp.AddDisabledServer(serverID); err != nil {
		return fmt.Errorf("禁用 MCP 服务器失败: %w", err)
	}

	fmt.Fprintf(a.stdout, "已禁用 MCP 服务器: %s\n", serverID)
	return nil
}

// mcpEnableTool 启用指定工具（从本地禁用列表移除）。
func (a *App) mcpEnableTool(args []string) error {
	if len(args) < 2 || strings.TrimSpace(args[0]) == "" || strings.TrimSpace(args[1]) == "" {
		return fmt.Errorf("用法: taiyiflow-cli mcp enable-tool <服务器ID> <工具名>")
	}
	serverID := strings.TrimSpace(args[0])
	toolName := strings.TrimSpace(args[1])
	toolKey := serverID + ":" + toolName

	if err := internalmcp.RemoveDisabledTool(toolKey); err != nil {
		return fmt.Errorf("启用工具失败: %w", err)
	}

	fmt.Fprintf(a.stdout, "已启用工具: %s (服务器: %s)\n", toolName, serverID)
	return nil
}

// mcpDisableTool 禁用指定工具（加入本地禁用列表）。
func (a *App) mcpDisableTool(args []string) error {
	if len(args) < 2 || strings.TrimSpace(args[0]) == "" || strings.TrimSpace(args[1]) == "" {
		return fmt.Errorf("用法: taiyiflow-cli mcp disable-tool <服务器ID> <工具名>")
	}
	serverID := strings.TrimSpace(args[0])
	toolName := strings.TrimSpace(args[1])
	toolKey := serverID + ":" + toolName

	if err := internalmcp.AddDisabledTool(toolKey); err != nil {
		return fmt.Errorf("禁用工具失败: %w", err)
	}

	fmt.Fprintf(a.stdout, "已禁用工具: %s (服务器: %s)\n", toolName, serverID)
	return nil
}

// mcpStatus 显示本地 MCP 禁用配置状态。
func (a *App) mcpStatus() error {
	configPath, err := internalmcp.ConfigPath()
	if err != nil {
		return err
	}

	mcpConfig, err := internalmcp.Load()
	if err != nil {
		return fmt.Errorf("加载本地 MCP 配置失败: %w", err)
	}

	fmt.Fprintf(a.stdout, "MCP 配置文件: %s\n\n", configPath)

	if len(mcpConfig.DisabledServerIDs) == 0 {
		fmt.Fprintln(a.stdout, "禁用的服务器: 无")
	} else {
		fmt.Fprintf(a.stdout, "禁用的服务器 (%d 个):\n", len(mcpConfig.DisabledServerIDs))
		for _, id := range mcpConfig.DisabledServerIDs {
			fmt.Fprintf(a.stdout, "  - %s\n", id)
		}
	}

	fmt.Fprintln(a.stdout)

	if len(mcpConfig.DisabledToolKeys) == 0 {
		fmt.Fprintln(a.stdout, "禁用的工具: 无")
	} else {
		fmt.Fprintf(a.stdout, "禁用的工具 (%d 个):\n", len(mcpConfig.DisabledToolKeys))
		for _, key := range mcpConfig.DisabledToolKeys {
			parts := strings.SplitN(key, ":", 2)
			if len(parts) == 2 {
				fmt.Fprintf(a.stdout, "  - 服务器 %s → 工具 %s\n", parts[0], parts[1])
			} else {
				fmt.Fprintf(a.stdout, "  - %s\n", key)
			}
		}
	}

	return nil
}
