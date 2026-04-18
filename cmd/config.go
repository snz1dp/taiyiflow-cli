package cmd

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func (a *App) runConfig(args []string) error {
	if len(args) == 0 {
		return a.showConfig()
	}

	switch args[0] {
	case "show":
		return a.showConfig()
	case "init":
		config, err := a.configManager.InitDefault()
		if err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "已初始化配置文件: %s\n", a.configManager.Path())
		content, _ := yaml.Marshal(config)
		fmt.Fprintln(a.stdout, string(content))
		return nil
	case "path":
		fmt.Fprintln(a.stdout, a.configManager.Path())
		return nil
	case "set":
		return a.setConfig(args[1:])
	default:
		return fmt.Errorf("不支持的 config 子命令: %s", args[0])
	}
}

func (a *App) showConfig() error {
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}
	content, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "配置路径: %s\n", a.configManager.Path())
	fmt.Fprintln(a.stdout, string(content))
	return nil
}

func (a *App) setConfig(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("用法: taiyiflow config set <key> <value>")
	}

	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	key := strings.TrimSpace(args[0])
	value := strings.TrimSpace(strings.Join(args[1:], " "))

	switch key {
	case "backend_base_url":
		config.BackendBaseURL = value
	case "api_key":
		config.APIKey = value
	case "default_flow":
		config.DefaultFlow = value
	case "approval_mode":
		config.ApprovalMode = value
	case "workdir":
		config.Workdir = value
	case "output_language":
		config.OutputLanguage = value
	default:
		return fmt.Errorf("不支持的配置项: %s", key)
	}

	if err := a.configManager.Save(config); err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "已更新配置项 %s\n", key)
	return nil
}
