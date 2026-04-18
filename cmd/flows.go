package cmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"

	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

func (a *App) runFlows(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "current":
			return a.showCurrentFlow()
		case "use":
			return a.useFlow(args[1:])
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
	flows, err := client.ListFlows(ctx, filter, 0, 100)
	if err != nil {
		return err
	}

	if len(flows.Items) == 0 {
		fmt.Fprintln(a.stdout, "未找到可用智能服务。")
		return nil
	}

	writer := tabwriter.NewWriter(a.stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\t名称\t类型\t描述")
	for _, item := range flows.Items {
		description := strings.ReplaceAll(strings.TrimSpace(item.Description), "\n", " ")
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", item.ID, item.Name, item.Type, description)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("输出 flows 列表失败: %w", err)
	}

	if config.DefaultFlow != "" {
		fmt.Fprintf(a.stdout, "\n当前默认 flow: %s\n", config.DefaultFlow)
	}
	fmt.Fprintln(a.stdout, "可使用 `taiyiflow flows use <ID>` 更新默认 flow。")

	return nil
}

func (a *App) showCurrentFlow() error {
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	if strings.TrimSpace(config.DefaultFlow) == "" {
		fmt.Fprintln(a.stdout, "当前未配置默认 flow。")
		fmt.Fprintln(a.stdout, "请先执行 `taiyiflow flows` 查看可用智能服务。")
		return nil
	}
	fmt.Fprintf(a.stdout, "当前默认 flow: %s\n", config.DefaultFlow)
	return nil
}

func (a *App) useFlow(args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("用法: taiyiflow flows use <ID>")
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	config.DefaultFlow = strings.TrimSpace(args[0])
	if err := a.configManager.Save(config); err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "已设置默认 flow: %s\n", config.DefaultFlow)
	return nil
}
