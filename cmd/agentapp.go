package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/plugins"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

// agentappCommandSynopsis 返回 agentapp 子命令用法摘要（root usage 引用）。
func agentappCommandSynopsis() string {
	return "taiyiflow-cli agentapp [init|list|plugins|installations|list-installed|help] [参数]  智能体应用管理"
}

// printAgentAppUsage 输出 agentapp 子命令帮助。
func (a *App) printAgentAppUsage() {
	fmt.Fprintln(a.stdout, "用法:")
	fmt.Fprintln(a.stdout, "  "+agentappCommandSynopsis())
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "子命令:")
	fmt.Fprintln(a.stdout, "  init <名称> [--dir 目录] [--display-name 名称] [--overwrite]")
	fmt.Fprintln(a.stdout, "                                生成智能体应用编排工程骨架（agentapp.json + README.md）")
	fmt.Fprintln(a.stdout, "                                manifest.contributions.plugins 声明插件集合引用")
	fmt.Fprintln(a.stdout, "  list [--installed]          列出服务端可用的智能体应用（--installed 仅已安装）")
	fmt.Fprintln(a.stdout, "  plugins <应用ID或名称>       查看应用引用的插件清单（含下载地址与完整性告警）")
	fmt.Fprintln(a.stdout, "  installations [状态]        查询当前用户的安装记录（状态可传 installed/uninstalled）")
	fmt.Fprintln(a.stdout, "  list-installed              列出本地已安装插件（读取 ~/.snz1dp/plugins，与 Electron 宿主共享）")
	fmt.Fprintln(a.stdout, "  help                        显示当前帮助")
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "说明:")
	fmt.Fprintln(a.stdout, "  智能体应用 = 插件集合 + Prompt 模板 + Skills + MCP 策略的编排声明。")
	fmt.Fprintln(a.stdout, "  CLI 侧为只读消费与安装记录操作；插件包落盘安装由 Electron 宿主完成")
	fmt.Fprintln(a.stdout, "  （应用详情页切换时自动级联下载，见 agentapp-cascade）。")
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "示例:")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli agentapp init code-review-assistant")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli agentapp list")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli agentapp plugins code-review-assistant")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli agentapp installations installed")
}

// runAgentApp 是 agentapp 子命令入口（root.go 命令分发调用）。
func (a *App) runAgentApp(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printAgentAppUsage()
		return nil
	}
	switch strings.TrimSpace(args[0]) {
	case "help", "-h", "--help":
		a.printAgentAppUsage()
		return nil
	case "init":
		return a.runAgentAppInit(args[1:])
	case "list":
		return a.runAgentAppList(ctx, args[1:])
	case "plugins":
		return a.runAgentAppPlugins(ctx, args[1:])
	case "installations":
		return a.runAgentAppInstallations(ctx, args[1:])
	case "list-installed":
		return a.runAgentAppListInstalled(args[1:])
	default:
		return fmt.Errorf("未知 agentapp 子命令 %q，执行 `taiyiflow-cli agentapp help` 查看用法", args[0])
	}
}

// runAgentAppInit 处理 agentapp init：生成应用编排工程骨架（P6.3）。
func (a *App) runAgentAppInit(args []string) error {
	var name, dir, displayName string
	var overwrite bool
	rest := args
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--dir":
			if i+1 >= len(rest) {
				return fmt.Errorf("--dir 缺少参数值")
			}
			i++
			dir = rest[i]
		case "--display-name":
			if i+1 >= len(rest) {
				return fmt.Errorf("--display-name 缺少参数值")
			}
			i++
			displayName = rest[i]
		case "--overwrite":
			overwrite = true
		default:
			if name == "" && !strings.HasPrefix(rest[i], "-") {
				name = rest[i]
			} else {
				return fmt.Errorf("未知参数 %q", rest[i])
			}
		}
	}
	if name == "" {
		return fmt.Errorf("用法: taiyiflow-cli agentapp init <名称> [--dir 目录] [--display-name 名称] [--overwrite]")
	}
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("获取当前目录失败: %w", err)
		}
		dir = cwd
	}

	result, err := plugins.ScaffoldAgentApp(dir, name, displayName, overwrite)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "智能体应用工程已创建: %s\n", result.AppDir)
	fmt.Fprintf(a.stdout, "应用标识（name）: %s\n", result.AppName)
	fmt.Fprintf(a.stdout, "生成文件:\n")
	for _, file := range result.Files {
		fmt.Fprintf(a.stdout, "  %s\n", file)
	}
	fmt.Fprintf(a.stdout, "\n后续步骤:\n")
	fmt.Fprintf(a.stdout, "  1. 编辑 %s，替换 contributions.plugins 中的示例引用为实际插件名\n", plugins.AgentAppManifestFileName)
	fmt.Fprintf(a.stdout, "  2. 通过服务端管理端创建应用（请求体基于该清单）\n")
	fmt.Fprintf(a.stdout, "  3. taiyiflow-cli agentapp plugins %s 核对插件引用完整性\n", result.AppName)
	return nil
}

// runAgentAppList 处理 agentapp list：列出服务端可用应用。
func (a *App) runAgentAppList(ctx context.Context, args []string) error {
	installedOnly := false
	for _, arg := range args {
		switch arg {
		case "--installed":
			installedOnly = true
		default:
			return fmt.Errorf("未知参数 %q", arg)
		}
	}

	_, client, err := a.prepareAgentAppClient(ctx)
	if err != nil {
		return err
	}
	apps, err := client.ListAgentApps(ctx, installedOnly, 0, 100)
	if err != nil {
		return fmt.Errorf("获取智能体应用列表失败: %w", err)
	}
	if len(apps) == 0 {
		fmt.Fprintln(a.stdout, "暂无可用的智能体应用")
		return nil
	}
	header := "可用智能体应用:"
	if installedOnly {
		header = "已安装的智能体应用:"
	}
	fmt.Fprintf(a.stdout, "%s（%d 个）\n", header, len(apps))
	for _, app := range apps {
		displayName := app.DisplayName
		if displayName == "" || strings.EqualFold(displayName, app.Name) {
			displayName = ""
		}
		line := fmt.Sprintf("- %s@%s", app.Name, app.Version)
		if displayName != "" {
			line += fmt.Sprintf("（%s）", displayName)
		}
		fmt.Fprintln(a.stdout, line)
		if trimmed := strings.TrimSpace(app.Description); trimmed != "" {
			fmt.Fprintf(a.stdout, "    %s\n", trimmed)
		}
	}
	return nil
}

// runAgentAppPlugins 处理 agentapp plugins：查看应用引用的插件清单。
func (a *App) runAgentAppPlugins(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: taiyiflow-cli agentapp plugins <应用ID或名称>")
	}
	agentAppID := strings.TrimSpace(args[0])
	if len(args) > 1 {
		return fmt.Errorf("plugins 不接受多余参数，收到 %q", args[1])
	}

	_, client, err := a.prepareAgentAppClient(ctx)
	if err != nil {
		return err
	}
	page, err := client.ListAgentAppPlugins(ctx, agentAppID, 0, 100)
	if err != nil {
		return fmt.Errorf("获取应用插件列表失败: %w", err)
	}

	// 引用完整性告警（悬空引用/禁用插件），服务端不阻断列表
	for _, warning := range page.Warnings {
		fmt.Fprintf(a.stdout, "⚠ %s\n", warning)
	}
	if page.Total == 0 {
		fmt.Fprintf(a.stdout, "应用 %s 未引用任何插件\n", agentAppID)
		return nil
	}
	fmt.Fprintf(a.stdout, "应用 %s 引用 %d 个插件:\n", agentAppID, page.Total)
	for _, plugin := range page.Data {
		line := fmt.Sprintf("- %s@%s", plugin.Name, plugin.Version)
		if plugin.DisplayName != "" && !strings.EqualFold(plugin.DisplayName, plugin.Name) {
			line += fmt.Sprintf("（%s）", plugin.DisplayName)
		}
		if !plugin.Enabled {
			line += " [已禁用]"
		}
		fmt.Fprintln(a.stdout, line)
		if plugin.ArtifactURI == "" {
			fmt.Fprintln(a.stdout, "    ⚠ 无可分发产物（artifact_uri 为空）")
		} else {
			fmt.Fprintf(a.stdout, "    产物: %s\n", plugin.ArtifactURI)
			if plugin.ArtifactHash != "" {
				fmt.Fprintf(a.stdout, "    校验值（SHA-256）: %s\n", plugin.ArtifactHash)
			}
		}
	}
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "插件包下载与落盘安装由 Electron 宿主完成（应用切换时级联安装）")
	return nil
}

// runAgentAppInstallations 处理 agentapp installations：查询安装记录。
func (a *App) runAgentAppInstallations(ctx context.Context, args []string) error {
	statusFilter := ""
	if len(args) > 0 {
		statusFilter = strings.TrimSpace(args[0])
		switch statusFilter {
		case "installed", "uninstalled", "":
		default:
			return fmt.Errorf("状态过滤仅支持 installed/uninstalled，收到 %q", statusFilter)
		}
	}

	_, client, err := a.prepareAgentAppClient(ctx)
	if err != nil {
		return err
	}
	page, err := client.ListAgentAppInstallRecords(ctx, statusFilter, 0, 100)
	if err != nil {
		return fmt.Errorf("获取安装记录失败: %w", err)
	}
	if len(page.Data) == 0 {
		fmt.Fprintln(a.stdout, "暂无安装记录")
		return nil
	}
	fmt.Fprintf(a.stdout, "安装记录（%d 条）:\n", len(page.Data))
	for _, record := range page.Data {
		statusMark := record.Status
		if statusMark == "installed" && record.Enabled {
			statusMark = "installed（已启用）"
		} else if statusMark == "installed" && !record.Enabled {
			statusMark = "installed（已禁用）"
		} else if statusMark == "uninstalled" {
			statusMark = "uninstalled（已卸载，记录保留）"
		}
		fmt.Fprintf(a.stdout, "- %s@%s [%s] 安装于 %s\n", record.AppName, record.AppVersion, statusMark, record.InstalledAt)
	}
	return nil
}

// runAgentAppListInstalled 处理 agentapp list-installed：列出本地已安装插件。
//
// 复用 plugin list-installed 的数据源（{SNZ1DP_HOME}/plugins/installed.json，
// 与 Electron 宿主共享目录，CLI 只读），便于用户对账应用插件与本地产物。
func (a *App) runAgentAppListInstalled(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("list-installed 不接受参数，收到 %q", args[0])
	}
	records, err := plugins.ReadInstallRecord()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(a.stdout, "暂无已安装插件（安装目录为空）")
		fmt.Fprintf(a.stdout, "安装目录: %s\n", mustInstallRootDir())
		return nil
	}
	// 按插件名排序保证输出稳定（map 遍历无序）
	names := make([]string, 0, len(records))
	for name := range records {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintf(a.stdout, "本地已安装 %d 个插件:\n", len(records))
	for _, name := range names {
		record := records[name]
		signMark := "未签名"
		if record.Signed {
			signMark = "已签名"
		}
		fmt.Fprintf(a.stdout, "  %s@%s（%s，安装于 %s）\n", record.ID, record.Version, signMark, record.InstalledAt)
	}
	fmt.Fprintf(a.stdout, "\n安装目录: %s\n", mustInstallRootDir())
	return nil
}

// prepareAgentAppClient 加载配置并构建已认证的后端客户端。
//
// 返回（配置, 客户端, 错误）；认证失败时返回错误（引导用户先执行 login）。
func (a *App) prepareAgentAppClient(ctx context.Context) (internalconfig.Config, *providerclient.Client, error) {
	loaded, loadErr := a.configManager.Load()
	if loadErr != nil {
		return loaded, nil, loadErr
	}
	providerClient := buildProviderClient(loaded)
	if authErr := a.ensureBackendAuth(ctx, providerClient, loaded); authErr != nil {
		return loaded, providerClient, fmt.Errorf("认证失败，请先执行 taiyiflow-cli login: %w", authErr)
	}
	return loaded, providerClient, nil
}
