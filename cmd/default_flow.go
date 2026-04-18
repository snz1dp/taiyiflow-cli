package cmd

import (
	"context"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

func (a *App) ensureDefaultFlow(ctx context.Context, client *providerclient.Client, config internalconfig.Config) (internalconfig.Config, error) {
	if strings.TrimSpace(config.DefaultFlow) != "" {
		return config, nil
	}

	flows, err := client.ListFlows(ctx, "", "chat", 0, 100)
	if err != nil || len(flows.Items) == 0 {
		return config, nil
	}

	chosen := strings.TrimSpace(flows.Items[0].ID)
	if chosen == "" {
		chosen = strings.TrimSpace(flows.Items[0].Name)
	}
	if chosen == "" {
		return config, nil
	}

	config.DefaultFlow = chosen
	if err := a.configManager.Save(config); err != nil {
		return config, err
	}
	return config, nil
}
