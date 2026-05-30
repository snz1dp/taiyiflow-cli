package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/browserbridge"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/computerbridge"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
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
		client := buildProviderClient(config)
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
	fmt.Fprintf(a.stdout, "- 后台服务前缀: %s\n", config.BackendAPIPrefix)
	fmt.Fprintf(a.stdout, "- HTTP 超时: %d 秒\n", config.HTTPTimeoutSeconds)
	fmt.Fprintf(a.stdout, "- 命令超时: %d 秒\n", config.CommandTimeoutSeconds)
	fmt.Fprintf(a.stdout, "- 流空闲超时: %d 秒\n", config.StreamIdleTimeoutSeconds)
	fmt.Fprintln(a.stdout, "- 流异常自动重试: 1 次（仅连接中断/超时）")
	fmt.Fprintf(a.stdout, "- 默认智能体: %s\n", flowDisplay)
	fmt.Fprintf(a.stdout, "- 文件工具链: %s\n", detectLocalFileToolStatus())
	fmt.Fprintf(a.stdout, "- 浏览器自动化: %s\n", detectBrowserAutomationStatus(config))
	fmt.Fprintf(a.stdout, "- 桌面操作: %s\n", detectComputerAutomationStatus(config))
	fmt.Fprintf(a.stdout, "- 浏览器扩展链路: %s\n", detectBrowserNativeMessagingStatus(config, cwd))
	if skillItems, directory, skillErr := skills.Discover(config); skillErr == nil {
		enabledCount := 0
		for _, item := range skillItems {
			if item.Enabled {
				enabledCount++
			}
		}
		fmt.Fprintf(a.stdout, "- 技能包目录: %s（发现 %d，显示 %d）\n", directory, len(skillItems), enabledCount)
	} else {
		fmt.Fprintf(a.stdout, "- 技能包目录: 检查失败（%v）\n", skillErr)
	}

	return nil
}

func detectLocalFileToolStatus() string {
	return "read_file/glob_files/write_file/file_edit/search_files（内置可用）"
}

func detectBrowserAutomationStatus(config internalconfig.Config) string {
	status := browserbridge.NewManager().Diagnose(config.Browser)
	parts := []string{
		fmt.Sprintf("浏览器模式: %s", browserModeDisplay(config.Browser.Mode)),
		fmt.Sprintf("权限模式: %s", browserPermissionModeDisplay(config.Browser.PermissionMode)),
		fmt.Sprintf("浏览器通道: %s", browserChannelDisplay(config.Browser.Channel)),
		fmt.Sprintf("扩展接入: %s", browserExtensionSetupDisplay(config.Browser.ExtensionSetup)),
	}
	if status.Available && status.Reachable {
		parts = append(parts, "状态: 可用")
	} else {
		parts = append(parts, "状态: 不可用")
	}
	if executablePath := strings.TrimSpace(status.ExecutablePath); executablePath != "" {
		parts = append(parts, fmt.Sprintf("浏览器程序: %s", executablePath))
	}
	if endpoint := strings.TrimSpace(status.RemoteDebuggingURL); endpoint != "" {
		parts = append(parts, fmt.Sprintf("调试地址: %s", endpoint))
	}
	if message := strings.TrimSpace(status.Message); message != "" {
		parts = append(parts, message)
	}
	return strings.Join(parts, " | ")
}

func detectComputerAutomationStatus(config internalconfig.Config) string {
	status := computerbridge.NewManager().Diagnose(config.Computer)
	parts := []string{
		fmt.Sprintf("开关: %s", enabledStateLabel(internalconfig.ComputerEnabled(config.Computer))),
		fmt.Sprintf("权限模式: %s", computerPermissionModeDisplay(config.Computer.PermissionMode)),
	}
	if status.Available {
		parts = append(parts, "状态: 可用")
	} else {
		parts = append(parts, "状态: 不可用")
	}
	if message := strings.TrimSpace(status.Message); message != "" {
		parts = append(parts, message)
	}
	return strings.Join(parts, " | ")
}

func browserModeDisplay(value string) string {
	switch strings.TrimSpace(value) {
	case internalconfig.BrowserModeChromeSession:
		return "当前会话"
	case internalconfig.BrowserModeIsolated:
		return "隔离实例"
	default:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "未设置"
		}
		return trimmed
	}
}

func browserPermissionModeDisplay(value string) string {
	switch strings.TrimSpace(value) {
	case internalconfig.BrowserPermissionModeAsk:
		return "逐次确认"
	case internalconfig.BrowserPermissionModeFollowPlan:
		return "跟随计划"
	case internalconfig.BrowserPermissionModeSkipAll:
		return "直接允许"
	default:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "未设置"
		}
		return trimmed
	}
}

func computerPermissionModeDisplay(value string) string {
	switch strings.TrimSpace(value) {
	case internalconfig.ComputerPermissionModeAsk:
		return "逐次确认"
	case internalconfig.ComputerPermissionModeFollowPlan:
		return "跟随计划"
	default:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "未设置"
		}
		return trimmed
	}
}

func browserChannelDisplay(value string) string {
	switch strings.TrimSpace(value) {
	case internalconfig.BrowserChannelChrome:
		return "Chrome"
	case internalconfig.BrowserChannelChromium:
		return "Chromium"
	case internalconfig.BrowserChannelEdge:
		return "Edge"
	case internalconfig.BrowserChannelBrave:
		return "Brave"
	default:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "未设置"
		}
		return trimmed
	}
}

func browserExtensionSetupDisplay(value string) string {
	switch strings.TrimSpace(value) {
	case internalconfig.BrowserExtensionSetupNative:
		return "本地消息"
	case internalconfig.BrowserExtensionSetupManual:
		return "手动管理"
	case internalconfig.BrowserExtensionSetupNone:
		return "不接扩展"
	default:
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "未设置"
		}
		return trimmed
	}
}

func detectBrowserNativeMessagingStatus(config internalconfig.Config, cwd string) string {
	extensionDir := detectChromeExtensionProjectDir(cwd)
	status := browserbridge.DiagnoseNativeMessaging(config.Browser, extensionDir)
	return status.Message
}
