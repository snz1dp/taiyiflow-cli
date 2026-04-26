package cmd

import (
	"fmt"
	"sort"
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
			lines = append(lines, "当前未发现任何技能(skill)。")
			return strings.Join(lines, "\n"), nil
		}
		for _, group := range groupSkillsBySource(items) {
			lines = append(lines, "")
			lines = append(lines, fmt.Sprintf("来源分组: %s", sourceDisplayLabel(group.source)))
			for _, item := range group.items {
				marker := "[ ]"
				if item.Enabled {
					marker = "[x]"
				}
				line := fmt.Sprintf("%s %s", marker, item.Name)
				if strings.TrimSpace(item.Description) != "" {
					line += fmt.Sprintf(" - %s", item.Description)
				}
				badges := skillDisplayBadges(item, config)
				if len(badges) > 0 {
					line += fmt.Sprintf(" | %s", strings.Join(badges, " | "))
				}
				lines = append(lines, line)
			}
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
		badges := skillDisplayBadges(*item, config)
		lines := []string{
			fmt.Sprintf("名称: %s", item.Name),
			fmt.Sprintf("描述: %s", fallbackConfigValue(item.Description, "<未填写>")),
			fmt.Sprintf("分类: %s", fallbackConfigValue(item.Category, "<未填写>")),
			fmt.Sprintf("来源: %s", sourceDisplayLabel(item.Source)),
			fmt.Sprintf("路径: %s", item.Path),
			fmt.Sprintf("根目录: %s", fallbackConfigValue(item.RootDir, "<未填写>")),
			fmt.Sprintf("相对目录: %s", fallbackConfigValue(item.RelativeDir, "<未填写>")),
			fmt.Sprintf("状态: %s", skillStateLabel(*item, config)),
			fmt.Sprintf("启用: %t", item.Enabled),
		}
		if len(badges) > 0 {
			lines = append(lines, fmt.Sprintf("标签: %s", strings.Join(badges, ", ")))
		}
		if len(item.Prerequisites) > 0 {
			lines = append(lines, fmt.Sprintf("前置条件: %s", strings.Join(item.Prerequisites, ", ")))
		}
		if len(item.Files) > 0 {
			lines = append(lines, "参考文件:")
			for _, file := range item.Files {
				line := fmt.Sprintf("- %s", fallbackConfigValue(file.Path, file.Source))
				if strings.TrimSpace(file.Description) != "" {
					line += fmt.Sprintf(" - %s", file.Description)
				}
				lines = append(lines, line)
			}
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
		disabled := normalizeSkillNames(config.Skills.Disabled)
		if subcommand == "enable" {
			enabled = append(enabled, item.Name)
			filteredDisabled := []string{}
			for _, current := range disabled {
				if !strings.EqualFold(current, item.Name) {
					filteredDisabled = append(filteredDisabled, current)
				}
			}
			disabled = filteredDisabled
		} else {
			filtered := []string{}
			for _, current := range enabled {
				if !strings.EqualFold(current, item.Name) {
					filtered = append(filtered, current)
				}
			}
			enabled = filtered
			disabled = append(disabled, item.Name)
		}
		config.Skills.Enabled = normalizeSkillNames(enabled)
		config.Skills.Disabled = normalizeSkillNames(disabled)
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

type skillSourceGroup struct {
	source string
	items  []skills.Skill
}

func groupSkillsBySource(items []skills.Skill) []skillSourceGroup {
	groups := map[string][]skills.Skill{}
	order := []string{"bundled", "workspace", "home", "custom", ""}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.Source))
		groups[key] = append(groups[key], item)
	}
	result := []skillSourceGroup{}
	seen := map[string]bool{}
	for _, key := range order {
		if len(groups[key]) == 0 {
			continue
		}
		seen[key] = true
		result = append(result, skillSourceGroup{source: key, items: groups[key]})
	}
	extraKeys := make([]string, 0, len(groups))
	for key := range groups {
		if seen[key] {
			continue
		}
		extraKeys = append(extraKeys, key)
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		result = append(result, skillSourceGroup{source: key, items: groups[key]})
	}
	return result
}

func sourceDisplayLabel(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "bundled":
		return "内置技能"
	case "workspace":
		return "工作区技能"
	case "home":
		return "用户技能"
	case "custom":
		return "自定义技能"
	default:
		return fallbackConfigValue(source, "<未填写>")
	}
}

func skillStateLabel(item skills.Skill, config internalconfig.Config) string {
	name := strings.TrimSpace(item.Name)
	if containsNormalizedSkillName(config.Skills.Disabled, name) {
		return "显式禁用"
	}
	if containsNormalizedSkillName(config.Skills.Enabled, name) {
		return "显式启用"
	}
	if item.Enabled {
		if strings.EqualFold(item.Source, "bundled") {
			return "默认启用"
		}
		return "已启用"
	}
	return "未启用"
}

func skillDisplayBadges(item skills.Skill, config internalconfig.Config) []string {
	badges := []string{}
	if strings.TrimSpace(item.Category) != "" {
		badges = append(badges, fmt.Sprintf("分类 %s", item.Category))
	}
	badges = append(badges, fmt.Sprintf("状态 %s", skillStateLabel(item, config)))
	if len(item.Files) > 0 {
		badges = append(badges, fmt.Sprintf("参考文件 %d", len(item.Files)))
	}
	if note := skillRestrictionNote(item.Name); note != "" {
		badges = append(badges, note)
	}
	return badges
}

func skillRestrictionNote(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "scheduleRemoteAgents", "scheduleremoteagents":
		return "受限能力"
	case "taiyiflowInChrome", "taiyiflowinchrome", "batch", "stuck", "loop", "runskillgenerator":
		return "裁剪实现"
	default:
		return ""
	}
}

func containsNormalizedSkillName(items []string, target string) bool {
	needle := strings.ToLower(strings.TrimSpace(target))
	if needle == "" {
		return false
	}
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item)) == needle {
			return true
		}
	}
	return false
}
