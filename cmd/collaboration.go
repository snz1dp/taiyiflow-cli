package cmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// runCollaboration 协同任务管理子命令入口。
func (a *App) runCollaboration(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printCollaborationUsage()
		return nil
	}
	switch args[0] {
	case "list":
		return a.runCollaborationList(ctx, args[1:])
	case "get":
		return a.runCollaborationGet(ctx, args[1:])
	case "online":
		return a.runCollaborationOnline(ctx, args[1:])
	case "help", "--help", "-h":
		a.printCollaborationUsage()
		return nil
	default:
		return fmt.Errorf("未知的协同任务子命令: %s", args[0])
	}
}

func (a *App) printCollaborationUsage() {
	fmt.Fprintln(a.stdout, `用法: taiyiflow-cli collaboration <子命令> [选项]

子命令:
  list     查询协同任务列表
  get      查看协同任务详情
  online   查看当前在线的桌面/CLI 客户端

示例:
  taiyiflow-cli collaboration list
  taiyiflow-cli collaboration list --status pending
  taiyiflow-cli collaboration get <task_id>
  taiyiflow-cli collaboration online`)
}

func (a *App) collaborationProviderClient(ctx context.Context) (provider.Provider, error) {
	config, err := a.configManager.Load()
	if err != nil {
		return nil, err
	}
	config, _, err = a.ensureClientIdentity(config)
	if err != nil {
		return nil, err
	}
	providerClient := buildProviderClient(config)
	config, _, err = a.ensureProviderAuthenticated(ctx, providerClient, config)
	if err != nil {
		return nil, err
	}
	return a.providerWithCallbackAuthRefresh(providerClient, &config), nil
}

func (a *App) runCollaborationList(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("collaboration list", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	status := flagSet.String("status", "", "按状态过滤（pending|dispatched|running|success|failed|expired|cancelled）")
	taskKind := flagSet.String("kind", "", "按任务类型过滤")
	limit := flagSet.Int("limit", 20, "最大返回条数")
	jsonOutput := flagSet.Bool("json", false, "以 JSON 格式输出")
	if err := flagSet.Parse(args); err != nil {
		return err
	}

	providerClient, err := a.collaborationProviderClient(ctx)
	if err != nil {
		return err
	}

	page, err := providerClient.ListClientCollaborations(ctx, provider.CollaborationListParams{
		Status:   *status,
		TaskKind: *taskKind,
		Offset:   0,
		Limit:    *limit,
	})
	if err != nil {
		return err
	}

	if *jsonOutput {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(page)
	}

	if len(page.Data) == 0 {
		fmt.Fprintln(a.stdout, "当前没有协同任务。")
		return nil
	}

	fmt.Fprintf(a.stdout, "%-36s  %-12s  %-10s  %-12s  %s\n", "任务ID", "类型", "状态", "投递策略", "创建时间")
	fmt.Fprintln(a.stdout, strings.Repeat("─", 100))
	for _, task := range page.Data {
		fmt.Fprintf(a.stdout, "%-36s  %-12s  %-10s  %-12s  %s\n",
			task.ID, task.TaskKind, task.Status, task.DeliveryPolicy,
			task.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(a.stdout, "\n共 %d 条记录\n", page.Total)
	return nil
}

func (a *App) runCollaborationGet(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("collaboration get", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	jsonOutput := flagSet.Bool("json", false, "以 JSON 格式输出")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	remaining := flagSet.Args()
	if len(remaining) == 0 {
		return fmt.Errorf("查看协同任务详情需要指定 task_id")
	}
	taskID := remaining[0]

	providerClient, err := a.collaborationProviderClient(ctx)
	if err != nil {
		return err
	}

	task, err := providerClient.GetClientCollaboration(ctx, taskID)
	if err != nil {
		return err
	}

	if *jsonOutput {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(task)
	}

	fmt.Fprintf(a.stdout, "任务ID:       %s\n", task.ID)
	fmt.Fprintf(a.stdout, "类型:         %s\n", task.TaskKind)
	fmt.Fprintf(a.stdout, "状态:         %s\n", task.Status)
	fmt.Fprintf(a.stdout, "投递策略:     %s\n", task.DeliveryPolicy)
	fmt.Fprintf(a.stdout, "目标客户端:   %s\n", task.TargetClientID)
	fmt.Fprintf(a.stdout, "目标设备:     %s\n", task.TargetDeviceID)
	if task.ErrorMessage != "" {
		fmt.Fprintf(a.stdout, "错误信息:     %s\n", task.ErrorMessage)
	}
	if !task.DispatchedAt.IsZero() {
		fmt.Fprintf(a.stdout, "下发时间:     %s\n", task.DispatchedAt.Format("2006-01-02 15:04:05"))
	}
	if !task.FinishedAt.IsZero() {
		fmt.Fprintf(a.stdout, "完成时间:     %s\n", task.FinishedAt.Format("2006-01-02 15:04:05"))
	}
	if len(task.ResultPayload) > 0 {
		resultJSON, _ := json.MarshalIndent(task.ResultPayload, "              ", "  ")
		fmt.Fprintf(a.stdout, "执行结果:     %s\n", string(resultJSON))
	}
	return nil
}

func (a *App) runCollaborationOnline(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("collaboration online", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	jsonOutput := flagSet.Bool("json", false, "以 JSON 格式输出")
	if err := flagSet.Parse(args); err != nil {
		return err
	}

	providerClient, err := a.collaborationProviderClient(ctx)
	if err != nil {
		return err
	}

	clients, err := providerClient.GetOnlineDesktopClients(ctx)
	if err != nil {
		return err
	}

	if *jsonOutput {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(clients)
	}

	if len(clients) == 0 {
		fmt.Fprintln(a.stdout, "当前没有在线的桌面/CLI 客户端。")
		return nil
	}

	fmt.Fprintf(a.stdout, "%-36s  %-36s  %-10s  %s\n", "客户端ID", "设备ID", "类型", "平台")
	fmt.Fprintln(a.stdout, strings.Repeat("─", 100))
	for _, client := range clients {
		fmt.Fprintf(a.stdout, "%-36s  %-36s  %-10s  %s\n",
			client.ClientID, client.DeviceID, client.ClientType, client.Platform)
	}
	fmt.Fprintf(a.stdout, "\n共 %d 个在线客户端\n", len(clients))
	return nil
}
