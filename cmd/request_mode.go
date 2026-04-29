package cmd

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func normalizeRequestMode(value string) string {
	return provider.NormalizeRequestType(value)
}

func requestModeLabel(value string) string {
	switch normalizeRequestMode(value) {
	case provider.RequestTypePlan:
		return "计划模式（plan）"
	case provider.RequestTypeAgent:
		return "执行模式（agent）"
	default:
		return "问答模式（ask）"
	}
}

func parseModeSlashCommand(command string) (string, string, bool) {
	trimmed := strings.TrimSpace(command)
	for _, item := range []struct {
		trigger string
		mode    string
	}{
		{trigger: "/ask", mode: provider.RequestTypeAsk},
		{trigger: "/plan", mode: provider.RequestTypePlan},
		{trigger: "/agent", mode: provider.RequestTypeAgent},
	} {
		if strings.EqualFold(trimmed, item.trigger) {
			return item.mode, "", true
		}
		prefix := strings.ToLower(item.trigger) + " "
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			return item.mode, strings.TrimSpace(trimmed[len(item.trigger):]), true
		}
	}
	return "", "", false
}

func resolveTaskRequestMode(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return provider.RequestTypeAgent, nil
	}
	switch strings.ToLower(trimmed) {
	case provider.RequestTypeAgent:
		return provider.RequestTypeAgent, nil
	case provider.RequestTypeAsk:
		return provider.RequestTypeAsk, nil
	case provider.RequestTypePlan:
		return provider.RequestTypePlan, nil
	default:
		return "", errors.New("请求模式仅支持 agent、ask 或 plan")
	}
}

func buildCLIUserAgent() string {
	version := strings.TrimSpace(buildinfo.Version)
	if version == "" {
		version = "dev"
	}
	return fmt.Sprintf("taiyiflow-cli/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH)
}