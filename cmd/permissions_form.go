package cmd

import (
	"fmt"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func formatSandboxRootsInline(items []string) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		parts = append(parts, trimmed)
	}
	return strings.Join(parts, ", ")
}

func parseSandboxRootsInline(value string) []string {
	parts := strings.Split(strings.TrimSpace(value), ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

func formatPermissionRulesInline(items []internalconfig.PermissionRule) string {
	parts := make([]string, 0, len(items))
	for _, item := range internalconfig.NormalizePermissionRules(items) {
		action := strings.TrimSpace(item.Action)
		switch {
		case strings.TrimSpace(item.ToolName) != "":
			parts = append(parts, fmt.Sprintf("tool:%s=%s", strings.TrimSpace(item.ToolName), action))
		case strings.TrimSpace(item.CommandPrefix) != "":
			parts = append(parts, fmt.Sprintf("shell:%s=%s", strings.TrimSpace(item.CommandPrefix), action))
		}
	}
	return strings.Join(parts, "; ")
}

func parsePermissionRulesInline(value string) ([]internalconfig.PermissionRule, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	segments := strings.Split(trimmed, ";")
	rules := make([]internalconfig.PermissionRule, 0, len(segments))
	for _, segment := range segments {
		entry := strings.TrimSpace(segment)
		if entry == "" {
			continue
		}
		equalsIndex := strings.LastIndex(entry, "=")
		if equalsIndex <= 0 || equalsIndex >= len(entry)-1 {
			return nil, fmt.Errorf("权限规则格式错误: %s，期望 kind:value=allow|ask|deny", entry)
		}
		left := strings.TrimSpace(entry[:equalsIndex])
		action := internalconfig.NormalizePermissionAction(entry[equalsIndex+1:])
		colonIndex := strings.Index(left, ":")
		if colonIndex <= 0 || colonIndex >= len(left)-1 {
			return nil, fmt.Errorf("权限规则格式错误: %s，期望 tool:name=... 或 shell:prefix=...", entry)
		}
		kind := strings.ToLower(strings.TrimSpace(left[:colonIndex]))
		value := strings.TrimSpace(left[colonIndex+1:])
		if value == "" {
			return nil, fmt.Errorf("权限规则缺少目标值: %s", entry)
		}
		rule := internalconfig.PermissionRule{Action: action}
		switch kind {
		case "tool":
			rule.ToolName = value
		case "shell":
			rule.CommandPrefix = value
		default:
			return nil, fmt.Errorf("权限规则类型仅支持 tool 或 shell: %s", entry)
		}
		rules = append(rules, rule)
	}
	return internalconfig.NormalizePermissionRules(rules), nil
}
