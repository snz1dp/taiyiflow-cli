package cmd

import (
	"fmt"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
)

func (a *App) runSkills(args []string) error {
	output, err := a.executeSkillsCommand(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) != "" {
		fmt.Fprintln(a.stdout, output)
	}
	return nil
}

func (a *App) executeSkillsCommand(args []string) (string, error) {
	config, err := a.configManager.Load()
	if err != nil {
		return "", err
	}
	subcommand := "list"
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		subcommand = strings.ToLower(strings.TrimSpace(args[0]))
	}

	switch subcommand {
	case "list":
		items, directory, err := skills.Discover(config)
		if err != nil {
			return "", err
		}
		lines := []string{fmt.Sprintf("技能目录: %s", directory)}
		if len(items) == 0 {
			lines = append(lines, "当前未发现任何技能（Skill）。")
			return strings.Join(lines, "\n"), nil
		}
		for _, item := range items {
			marker := "[ ]"
			if item.Enabled {
				marker = "[x]"
			}
			line := fmt.Sprintf("%s %s", marker, item.Name)
			if strings.TrimSpace(item.Description) != "" {
				line += fmt.Sprintf(" - %s", item.Description)
			}
			if strings.TrimSpace(item.Category) != "" {
				line += fmt.Sprintf(" | 分类 %s", item.Category)
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), nil
	case "view":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills view <名称>")
		}
		name := strings.TrimSpace(strings.Join(args[1:], " "))
		item, _, err := skills.LoadByName(config, name)
		if err != nil {
			return "", err
		}
		lines := []string{
			fmt.Sprintf("名称: %s", item.Name),
			fmt.Sprintf("描述: %s", fallbackConfigValue(item.Description, "<未填写>")),
			fmt.Sprintf("分类: %s", fallbackConfigValue(item.Category, "<未填写>")),
			fmt.Sprintf("路径: %s", item.Path),
			fmt.Sprintf("启用: %t", item.Enabled),
		}
		if len(item.Prerequisites) > 0 {
			lines = append(lines, fmt.Sprintf("前置条件: %s", strings.Join(item.Prerequisites, ", ")))
		}
		if strings.TrimSpace(item.Body) != "" {
			lines = append(lines, "", item.Body)
		}
		return strings.Join(lines, "\n"), nil
	case "enable", "disable":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills %s <名称>", subcommand)
		}
		name := strings.TrimSpace(strings.Join(args[1:], " "))
		item, _, err := skills.LoadByName(config, name)
		if err != nil {
			return "", err
		}
		enabled := normalizeSkillNames(config.Skills.Enabled)
		if subcommand == "enable" {
			enabled = append(enabled, item.Name)
		} else {
			filtered := []string{}
			for _, current := range enabled {
				if !strings.EqualFold(current, item.Name) {
					filtered = append(filtered, current)
				}
			}
			enabled = filtered
		}
		config.Skills.Enabled = normalizeSkillNames(enabled)
		if err := a.configManager.Save(config); err != nil {
			return "", err
		}
		return fmt.Sprintf("已%s技能: %s", map[string]string{"enable": "启用", "disable": "禁用"}[subcommand], item.Name), nil
	default:
		return "", fmt.Errorf("不支持的 skills 子命令: %s", subcommand)
	}
}

func (a *App) loadSkillPayload(config internalconfig.Config) ([]string, any, error) {
	items, directory, err := skills.Discover(config)
	if err != nil {
		return nil, nil, err
	}
	requestedSkillNames := skills.EnabledSkillNames(items)
	clientSkillPackages := skills.ClientPackages(items, directory)
	return requestedSkillNames, clientSkillPackages, nil
}

func normalizeSkillNames(items []string) []string {
	seen := map[string]string{}
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		seen[strings.ToLower(trimmed)] = trimmed
	}
	result := make([]string, 0, len(seen))
	for _, value := range seen {
		result = append(result, value)
	}
	for left := 0; left < len(result); left++ {
		for right := left + 1; right < len(result); right++ {
			if strings.ToLower(result[right]) < strings.ToLower(result[left]) {
				result[left], result[right] = result[right], result[left]
			}
		}
	}
	return result
}

func parseSlashSkillsArgs(command string) []string {
	trimmed := strings.TrimSpace(command)
	if !strings.HasPrefix(strings.ToLower(trimmed), "/skills") {
		return nil
	}
	remainder := strings.TrimSpace(trimmed[len("/skills"):])
	if remainder == "" {
		return []string{"list"}
	}
	return strings.Fields(remainder)
}
