package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

func (a *App) runDoctor(args []string) error {
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	flowDisplay := sanitizeFlowDisplay(config.DefaultFlow)
	if config.BackendBaseURL != "" {
		client := providerclient.NewClient(config.BackendBaseURL, config.APIKey)
		config, err = a.ensureDefaultFlow(context.Background(), client, config)
		if err != nil {
			return err
		}
		if config.DefaultFlow != "" {
			flowDisplay = a.resolveFlowDisplayName(context.Background(), client, config.DefaultFlow)
		}
	}

	fileExistedLabel := "存在"
	if !a.configManager.Exists() {
		fileExistedLabel = "不存在"
	}

	fmt.Fprintln(a.stdout, "太乙智启命令行（CLI）自检结果")
	fmt.Fprintf(a.stdout, "- 当前工作目录: %s\n", cwd)
	fmt.Fprintf(a.stdout, "- 配置文件路径: %s（%s）\n", a.configManager.Path(), fileExistedLabel)
	fmt.Fprintf(a.stdout, "- 后台服务地址: %s\n", config.BackendBaseURL)
	fmt.Fprintf(a.stdout, "- 默认智能体: %s\n", flowDisplay)
	fmt.Fprintf(a.stdout, "- 本地搜索工具: %s\n", detectLocalSearchTool())

	return nil
}

func detectLocalSearchTool() string {
	if _, err := exec.LookPath("rg"); err == nil {
		return "rg（可用）"
	}
	if _, err := exec.LookPath("grep"); err == nil {
		return "grep（可用，已启用降级）"
	}
	return "未检测到 rg/grep（search_files 不可用）"
}
