package cmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

func (a *App) runAgents(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "current":
			return a.showCurrentAgent(ctx)
		case "use":
			return a.useAgent(ctx, args[1:])
		}
	}

	filter := ""
	if len(args) > 0 {
		if args[0] == "list" {
			filter = strings.TrimSpace(strings.Join(args[1:], " "))
		} else {
			filter = strings.TrimSpace(strings.Join(args, " "))
		}
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	client := providerclient.NewClient(config.BackendBaseURL, config.APIKey)
	config, err = a.ensureDefaultFlow(ctx, client, config)
	if err != nil {
		return err
	}
	flows, err := client.ListFlows(ctx, filter, "chat", 0, 100)
	if err != nil {
		return err
	}

	if len(flows.Items) == 0 {
		fmt.Fprintln(a.stdout, "未找到可用智能体。")
		return nil
	}

	writer := tabwriter.NewWriter(a.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(writer, "名称\t描述")
	for _, item := range flows.Items {
		description := strings.ReplaceAll(strings.TrimSpace(item.Description), "\n", " ")
		fmt.Fprintf(writer, "%s\t%s\n", item.Name, description)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("输出智能体列表失败: %w", err)
	}

	if config.DefaultFlow != "" {
		fmt.Fprintf(a.stdout, "\n当前默认智能体: %s\n", a.resolveFlowDisplayName(ctx, client, config.DefaultFlow))
	}
	fmt.Fprintln(a.stdout, "可使用 `taiyiflow agents use <名称>` 更新默认智能体。")

	return nil
}

func (a *App) showCurrentAgent(ctx context.Context) error {
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	client := providerclient.NewClient(config.BackendBaseURL, config.APIKey)
	config, err = a.ensureDefaultFlow(ctx, client, config)
	if err != nil {
		return err
	}
	if strings.TrimSpace(config.DefaultFlow) == "" {
		fmt.Fprintln(a.stdout, "当前未配置默认智能体。")
		fmt.Fprintln(a.stdout, "请先执行 `taiyiflow agents` 查看可用智能体。")
		return nil
	}
	fmt.Fprintf(a.stdout, "当前默认智能体: %s\n", a.resolveFlowDisplayName(ctx, client, config.DefaultFlow))
	return nil
}

func (a *App) useAgent(ctx context.Context, args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("用法: taiyiflow agents use <名称>")
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	config.DefaultFlow = strings.TrimSpace(args[0])
	if err := a.configManager.Save(config); err != nil {
		return err
	}

	client := providerclient.NewClient(config.BackendBaseURL, config.APIKey)
	fmt.Fprintf(a.stdout, "已设置默认智能体: %s\n", a.resolveFlowDisplayName(ctx, client, config.DefaultFlow))
	return nil
}
