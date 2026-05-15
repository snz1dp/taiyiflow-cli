package cmd

import (
	"fmt"
	"sort"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
)

func skillsCommandSynopsis() string {
	return strings.Join([]string{
		"taiyiflow-cli skills [list|view|search|view-remote|install|remove|",
		"                        update|source|enable|disable|help] [参数]",
	}, "\n")
}

func skillsSlashSynopsis() string {
	return strings.Join([]string{
		"/skills [list|view|search|view-remote|install|remove|update|source|enable|disable|help]",
	}, "\n")
}

func renderSkillsUsage(synopsis string) string {
	lines := []string{
		"用法:",
		"  " + synopsis,
		"",
		"子命令:",
		"  list                           列出本地已发现技能及来源状态",
		"  view <名称>                    查看本地技能详情",
		"  search [关键词]                搜索远程技能仓库，关键词可省略",
		"  view-remote <slug>             查看远程技能详情",
		"  install <slug> [version]       安装远程技能，可选指定版本",
		"  remove <名称或slug>            删除已安装远程技能",
		"  update <名称或slug|all>        更新指定远程技能或全部远程技能",
		"  source [show|set|reset]        查看或切换远程技能仓库地址",
		"  enable <名称>                  恢复技能的候选资格",
		"  disable <名称>                 将技能移出候选集合",
		"  help                           显示当前帮助",
		"",
		"自动补装配置:",
		"  taiyiflow-cli config set skills_auto_install_policy off|confirm|allow",
		"  taiyiflow-cli config set skills_free_text_search true|false",
		"",
		"示例:",
		"  taiyiflow-cli skills search debug",
		"  taiyiflow-cli skills view-remote remote-skill",
		"  taiyiflow-cli skills install remote-skill",
		"  taiyiflow-cli skills source set https://mirror-cn.clawhub.com",
	}
	return strings.Join(lines, "\n")
}

func skillsCommandUsage() string {
	return renderSkillsUsage(skillsCommandSynopsis())
}

func skillsSlashUsage() string {
	usage := renderSkillsUsage(skillsSlashSynopsis())
	return strings.ReplaceAll(usage, "taiyiflow-cli skills", "/skills")
}

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
	case "help", "-h", "--help":
		return skillsCommandUsage(), nil
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
			fmt.Sprintf("纳入候选: %t", item.Enabled),
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
	case "search":
		query := strings.TrimSpace(strings.Join(args[1:], " "))
		results, registryURL, err := skills.SearchRemote(config, query, 20)
		if err != nil {
			return "", err
		}
		lines := []string{fmt.Sprintf("技能仓库: %s", registryURL)}
		if len(results) == 0 {
			lines = append(lines, "未搜索到远程技能。")
			return strings.Join(lines, "\n"), nil
		}
		for _, item := range results {
			line := fmt.Sprintf("- %s", item.Slug)
			if strings.TrimSpace(item.DisplayName) != "" {
				line += fmt.Sprintf(" (%s)", item.DisplayName)
			}
			if strings.TrimSpace(item.Version) != "" {
				line += fmt.Sprintf(" | 最新版本 %s", item.Version)
			}
			if item.Popularity > 0 {
				line += fmt.Sprintf(" | %s", formatSkillPopularityLabel(item.Popularity))
			}
			if item.Downloads > 0 {
				line += fmt.Sprintf(" | %s", formatSkillDownloadsLabel(item.Downloads))
			}
			if strings.TrimSpace(item.Summary) != "" {
				line += fmt.Sprintf(" | %s", item.Summary)
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), nil
	case "view-remote", "remote-view", "detail":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills %s <slug>", subcommand)
		}
		detail, registryURL, err := skills.FetchRemoteDetail(config, args[1])
		if err != nil {
			return "", err
		}
		lines := []string{
			fmt.Sprintf("技能仓库: %s", registryURL),
			fmt.Sprintf("Slug: %s", fallbackConfigValue(detail.Slug, "<未填写>")),
			fmt.Sprintf("显示名称: %s", fallbackConfigValue(detail.DisplayName, "<未填写>")),
			fmt.Sprintf("摘要: %s", fallbackConfigValue(detail.Summary, "<未填写>")),
			fmt.Sprintf("最新版本: %s", fallbackConfigValue(detail.Version, "<未填写>")),
		}
		if detail.OwnerHandle != "" || detail.OwnerName != "" {
			owner := fallbackConfigValue(detail.OwnerName, detail.OwnerHandle)
			if detail.OwnerHandle != "" && !strings.EqualFold(owner, detail.OwnerHandle) {
				owner += fmt.Sprintf(" (%s)", detail.OwnerHandle)
			}
			lines = append(lines, fmt.Sprintf("发布者: %s", owner))
		}
		if len(detail.SupportedOS) > 0 {
			lines = append(lines, fmt.Sprintf("支持系统: %s", strings.Join(detail.SupportedOS, ", ")))
		}
		if len(detail.SupportedEnvs) > 0 {
			lines = append(lines, fmt.Sprintf("适用环境: %s", strings.Join(detail.SupportedEnvs, ", ")))
		}
		return strings.Join(lines, "\n"), nil
	case "install":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills install <slug> [version]")
		}
		version := ""
		if len(args) > 2 {
			version = strings.TrimSpace(args[2])
		}
		result, err := skills.InstallRemote(config, args[1], version, false)
		if err != nil {
			return "", err
		}
		lines := []string{
			fmt.Sprintf("已安装远程技能: %s (%s)", fallbackConfigValue(result.Name, result.Slug), result.Slug),
			fmt.Sprintf("版本: %s", result.Version),
			fmt.Sprintf("目录: %s", result.TargetDir),
			fmt.Sprintf("技能仓库: %s", result.RegistryURL),
		}
		return strings.Join(lines, "\n"), nil
	case "remove", "delete", "uninstall":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills %s <名称或slug>", subcommand)
		}
		result, err := skills.RemoveInstalled(config, strings.Join(args[1:], " "))
		if err != nil {
			return "", err
		}
		config.Skills.Enabled = removeSkillNameAliases(config.Skills.Enabled, result.Name, result.Slug)
		config.Skills.Disabled = removeSkillNameAliases(config.Skills.Disabled, result.Name, result.Slug)
		if err := a.configManager.Save(config); err != nil {
			return "", err
		}
		return strings.Join([]string{
			fmt.Sprintf("已删除技能: %s (%s)", fallbackConfigValue(result.Name, result.Slug), result.Slug),
			fmt.Sprintf("目录: %s", result.TargetDir),
		}, "\n"), nil
	case "update":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills update <名称或slug|all>")
		}
		if strings.EqualFold(strings.TrimSpace(args[1]), "all") {
			results, err := skills.UpdateAllRemote(config)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "当前没有已安装的远程技能。", nil
			}
			lines := []string{"远程技能更新结果:"}
			for _, item := range results {
				if item.Changed {
					lines = append(lines, fmt.Sprintf("- %s (%s): %s -> %s", fallbackConfigValue(item.Name, item.Slug), item.Slug, fallbackConfigValue(item.PreviousVersion, "<未知>"), item.Version))
					continue
				}
				lines = append(lines, fmt.Sprintf("- %s (%s): 已是最新版本 %s", fallbackConfigValue(item.Name, item.Slug), item.Slug, item.Version))
			}
			return strings.Join(lines, "\n"), nil
		}
		result, err := skills.UpdateRemote(config, strings.Join(args[1:], " "))
		if err != nil {
			return "", err
		}
		if !result.Changed {
			return strings.Join([]string{
				fmt.Sprintf("技能已是最新版本: %s (%s)", fallbackConfigValue(result.Name, result.Slug), result.Slug),
				fmt.Sprintf("版本: %s", result.Version),
			}, "\n"), nil
		}
		return strings.Join([]string{
			fmt.Sprintf("已更新技能: %s (%s)", fallbackConfigValue(result.Name, result.Slug), result.Slug),
			fmt.Sprintf("版本: %s -> %s", fallbackConfigValue(result.PreviousVersion, "<未知>"), result.Version),
			fmt.Sprintf("目录: %s", result.TargetDir),
		}, "\n"), nil
	case "source":
		action := "show"
		if len(args) > 1 {
			action = strings.ToLower(strings.TrimSpace(args[1]))
		}
		switch action {
		case "show", "":
			return fmt.Sprintf("当前技能仓库: %s", resolveRegistryURLDisplay(config)), nil
		case "set":
			if len(args) < 3 {
				return "", fmt.Errorf("用法: taiyiflow-cli skills source set <url>")
			}
			registryURL, err := normalizeRemoteRegistryURL(strings.Join(args[2:], " "))
			if err != nil {
				return "", err
			}
			config.Skills.RegistryURL = registryURL
			if err := a.configManager.Save(config); err != nil {
				return "", err
			}
			return fmt.Sprintf("已更新技能仓库: %s", registryURL), nil
		case "reset", "unset", "default":
			config.Skills.RegistryURL = internalconfig.Default().Skills.RegistryURL
			if err := a.configManager.Save(config); err != nil {
				return "", err
			}
			return fmt.Sprintf("已恢复默认技能仓库: %s", config.Skills.RegistryURL), nil
		default:
			return "", fmt.Errorf("不支持的 source 子命令: %s", action)
		}
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
		if subcommand == "enable" {
			return fmt.Sprintf("已恢复技能候选资格: %s", item.Name), nil
		}
		return fmt.Sprintf("已禁用技能候选资格: %s", item.Name), nil
	default:
		return "", fmt.Errorf("不支持的 skills 子命令: %s\n\n%s", subcommand, skillsCommandUsage())
	}
}

func resolveRegistryURLDisplay(config internalconfig.Config) string {
	value := strings.TrimSpace(config.Skills.RegistryURL)
	if value == "" {
		return internalconfig.Default().Skills.RegistryURL
	}
	return value
}

func removeSkillNameAliases(items []string, aliases ...string) []string {
	filtered := []string{}
	for _, item := range items {
		matched := false
		for _, alias := range aliases {
			if alias != "" && strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(alias)) {
				matched = true
				break
			}
		}
		if !matched {
			filtered = append(filtered, item)
		}
	}
	return normalizeSkillNames(filtered)
}

func (a *App) resolveEffectiveSkillConfig(config internalconfig.Config, contextDir string) internalconfig.Config {
	resolved := config
	if a.configManager != nil {
		if latestConfig, err := a.configManager.Load(); err == nil {
			resolved = latestConfig
		}
	}
	if a.skillInstallScopePinned {
		resolved.Skills.InstallScope = internalconfig.NormalizeSkillInstallScope(a.skillInstallScopeSession)
	} else {
		resolved.Skills.InstallScope = internalconfig.NormalizeSkillInstallScope(resolved.Skills.InstallScope)
	}
	resolvedContextDir := resolveLocalWorkdir(firstNonEmpty(contextDir, resolved.Workdir))
	if strings.TrimSpace(resolvedContextDir) != "" {
		resolved.Workdir = resolvedContextDir
	}
	return resolved
}

func loadSkillPayloadWithConfig(config internalconfig.Config) ([]string, any, error) {
	items, directory, err := skills.Discover(config)
	if err != nil {
		return nil, nil, err
	}
	requestedSkillNames := skills.EnabledSkillNames(items)
	clientSkillPackages := skills.ClientPackages(items, directory)
	return requestedSkillNames, clientSkillPackages, nil
}

func (a *App) loadSkillPayload(config internalconfig.Config, contextDir string) ([]string, any, error) {
	return loadSkillPayloadWithConfig(a.resolveEffectiveSkillConfig(config, contextDir))
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
		return "显式放行"
	}
	if item.Enabled {
		return "渐进披露"
	}
	return "未纳入候选"
}

func skillDisplayBadges(item skills.Skill, config internalconfig.Config) []string {
	badges := []string{}
	badges = append(badges, fmt.Sprintf("%s", skillStateLabel(item, config)))
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
	case "schedule-remote-agents", "scheduleremoteagents":
		return "受限能力"
	case "taiyiflow-in-chrome", "taiyiflowinchrome", "batch", "stuck", "loop", "runskillgenerator":
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

func formatSkillPopularityLabel(value float64) string {
	formatted := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".")
	if formatted == "" {
		return "人气"
	}
	return "人气 " + formatted
}

func formatSkillDownloadsLabel(value int64) string {
	return fmt.Sprintf("下载 %d", value)
}
