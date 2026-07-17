package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
)

func skillsCommandSynopsis() string {
	return strings.Join([]string{
		"taiyiflow-cli skills [list|view|search|view-remote|install|remove|",
		"                        update|enable|disable|help] [参数]",
	}, "\n")
}

func skillsSlashSynopsis() string {
	return strings.Join([]string{
		"/skills [list|view|search|view-remote|install|remove|update|enable|disable|help]",
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
		"  search [关键词]                搜索服务端技能，关键词可省略",
		"  view-remote <名称或ID>         查看服务端技能详情",
		"  install <名称或ID>             从服务端安装技能到本地",
		"  remove <名称或ID>             卸载已安装的服务端技能",
		"  update [名称或ID|all]         更新指定服务端技能或全部服务端技能",
		"  enable <名称>                  恢复技能的候选资格",
		"  disable <名称>                 将技能移出候选集合",
		"",
		"  后端技能管理（高级）:",
		"  backend list                   列出后端可见技能及安装状态",
		"  backend view <名称或ID>        查看后端技能详情",
		"  backend install <名称或ID>     从后端下载安装技能到本地",
		"  backend remove <名称或ID>     卸载后端技能并清除本地缓存",
		"  backend sync                   同步后端已安装技能到本地",
		"  backend upload <本地技能名>     上传本地技能到后端",
		"  backend upload-file <文件路径>  上传 ZIP 或 MD 文件到后端",
		"  backend delete <技能ID>        从后端删除自定义技能",
		"",
		"  help                           显示当前帮助",
		"",
		"自动补装配置:",
		"  taiyiflow-cli config set skills_auto_install_policy off|confirm|allow",
		"  taiyiflow-cli config set skills_free_text_search true|false",
		"",
		"示例:",
		"  taiyiflow-cli skills search debug",
		"  taiyiflow-cli skills view-remote my-skill",
		"  taiyiflow-cli skills install my-skill",
		"  taiyiflow-cli skills backend list",
		"  taiyiflow-cli skills backend install my-skill",
		"  taiyiflow-cli skills backend upload my-local-skill",
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

func (a *App) runSkills(ctx context.Context, args []string) error {
	output, err := a.executeSkillsCommand(ctx, args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) != "" {
		fmt.Fprintln(a.stdout, output)
	}
	return nil
}

func (a *App) executeSkillsCommand(ctx context.Context, args []string) (string, error) {
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
		client := buildProviderClient(config)
		if err := a.ensureBackendAuth(ctx, client, config); err != nil {
			return "", err
		}
		skillList, err := skills.FetchBackendSkills(ctx, client, provider.FetchVisibleSkillsParams{})
		if err != nil {
			return "", fmt.Errorf("获取服务端技能列表失败: %w", err)
		}
		filtered := filterBackendSkillsByQuery(skillList, query)
		lines := []string{"服务端技能列表:"}
		if len(filtered) == 0 {
			lines = append(lines, "未搜索到匹配的技能。")
			return strings.Join(lines, "\n"), nil
		}
		for _, item := range filtered {
			line := fmt.Sprintf("- %s", item.Name)
			if strings.TrimSpace(item.DisplayName) != "" && !strings.EqualFold(item.DisplayName, item.Name) {
				line += fmt.Sprintf(" (%s)", item.DisplayName)
			}
			if strings.TrimSpace(item.Version) != "" {
				line += fmt.Sprintf(" | 版本 %s", item.Version)
			}
			if strings.TrimSpace(item.Summary) != "" {
				line += fmt.Sprintf(" | %s", item.Summary)
			}
			lines = append(lines, line)
		}
		return strings.Join(lines, "\n"), nil
	case "view-remote", "remote-view", "detail":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills %s <名称或ID>", subcommand)
		}
		return a.backendSkillsView(ctx, config, args[1])
	case "install":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills install <名称或ID>")
		}
		return a.backendSkillsInstall(ctx, config, args[1])
	case "remove", "delete", "uninstall":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills %s <名称或ID>", subcommand)
		}
		return a.backendSkillsRemove(ctx, config, strings.Join(args[1:], " "))
	case "update":
		if len(args) < 2 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills update [名称或ID|all]")
		}
		if strings.EqualFold(strings.TrimSpace(args[1]), "all") {
			return a.backendSkillsSync(ctx, config)
		}
		return a.backendSkillsInstall(ctx, config, args[1])
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
	case "backend":
		return a.executeBackendSkillsCommand(ctx, config, args[1:])
	default:
		return "", fmt.Errorf("不支持的 skills 子命令: %s\n\n%s", subcommand, skillsCommandUsage())
	}
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

func loadSkillPayloadWithConfig(config internalconfig.Config, requestType string) ([]string, any, error) {
	items, directory, err := skills.Discover(config)
	if err != nil {
		return nil, nil, err
	}
	items = skills.FilterByRequestType(items, requestType)
	requestedSkillNames := skills.EnabledSkillNames(items)
	clientSkillPackages := skills.ClientPackages(items, directory)
	return requestedSkillNames, clientSkillPackages, nil
}

func (a *App) loadSkillPayload(config internalconfig.Config, contextDir string, requestType string) ([]string, any, error) {
	return loadSkillPayloadWithConfig(a.resolveEffectiveSkillConfig(config, contextDir), requestType)
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
	case "taiyiflow-in-chrome", "taiyiflowinchrome":
		return "需浏览器配置"
	case "batch", "stuck", "loop", "runskillgenerator":
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

// ---- 后端技能管理子命令 ----

func (a *App) executeBackendSkillsCommand(ctx context.Context, config internalconfig.Config, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("用法: taiyiflow-cli skills backend <list|view|install|remove|sync|upload|upload-file|delete> [参数]")
	}

	subcommand := strings.ToLower(strings.TrimSpace(args[0]))
	subArgs := args[1:]

	switch subcommand {
	case "list":
		return a.backendSkillsList(ctx, config)
	case "view":
		if len(subArgs) < 1 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills backend view <名称或ID>")
		}
		return a.backendSkillsView(ctx, config, subArgs[0])
	case "install":
		if len(subArgs) < 1 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills backend install <名称或ID>")
		}
		return a.backendSkillsInstall(ctx, config, subArgs[0])
	case "remove":
		if len(subArgs) < 1 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills backend remove <名称或ID>")
		}
		return a.backendSkillsRemove(ctx, config, subArgs[0])
	case "sync":
		return a.backendSkillsSync(ctx, config)
	case "upload":
		if len(subArgs) < 1 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills backend upload <本地技能名>")
		}
		return a.backendSkillsUpload(ctx, config, subArgs[0])
	case "upload-file":
		if len(subArgs) < 1 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills backend upload-file <文件路径> [显示名称]")
		}
		displayName := ""
		if len(subArgs) > 1 {
			displayName = subArgs[1]
		}
		return a.backendSkillsUploadFile(ctx, config, subArgs[0], displayName)
	case "delete":
		if len(subArgs) < 1 {
			return "", fmt.Errorf("用法: taiyiflow-cli skills backend delete <技能ID>")
		}
		return a.backendSkillsDelete(ctx, config, subArgs[0])
	default:
		return "", fmt.Errorf("不支持的后端技能子命令: %s\n可用子命令: list, view, install, remove, sync, upload, upload-file, delete", subcommand)
	}
}

func (a *App) backendSkillsList(ctx context.Context, config internalconfig.Config) (string, error) {
	client := buildProviderClient(config)
	if err := a.ensureBackendAuth(ctx, client, config); err != nil {
		return "", err
	}

	skillList, err := skills.FetchBackendSkills(ctx, client, provider.FetchVisibleSkillsParams{})
	if err != nil {
		return "", fmt.Errorf("获取后端技能列表失败: %w", err)
	}

	if len(skillList) == 0 {
		return "后端暂无可用的技能包。", nil
	}

	// 获取安装记录
	installRecords, _ := client.QueryInstallations(ctx, provider.QueryInstallationsParams{
		Limit: 200,
	})
	installedMap := map[string]bool{}
	if installRecords != nil {
		for _, record := range installRecords.Items {
			if record.Status == "installed" {
				installedMap[record.SkillPackageID] = true
			}
		}
	}

	lines := []string{"后端技能列表:"}
	for _, skill := range skillList {
		status := "未安装"
		if installedMap[skill.ID] {
			status = "已安装"
		}
		name := skill.DisplayName
		if name == "" {
			name = skill.Name
		}
		lines = append(lines, fmt.Sprintf("  %-30s %-10s %-10s %s", name, skill.Version, status, skill.Summary))
	}
	return strings.Join(lines, "\n"), nil
}

func (a *App) backendSkillsView(ctx context.Context, config internalconfig.Config, nameOrID string) (string, error) {
	client := buildProviderClient(config)
	if err := a.ensureBackendAuth(ctx, client, config); err != nil {
		return "", err
	}

	skillList, err := skills.FetchBackendSkills(ctx, client, provider.FetchVisibleSkillsParams{})
	if err != nil {
		return "", fmt.Errorf("获取后端技能列表失败: %w", err)
	}

	var matched *provider.VisibleSkillPackage
	for i := range skillList {
		s := &skillList[i]
		if strings.EqualFold(s.ID, nameOrID) || strings.EqualFold(s.Name, nameOrID) || strings.EqualFold(s.DisplayName, nameOrID) {
			matched = s
			break
		}
	}
	if matched == nil {
		return "", fmt.Errorf("未找到后端技能: %s", nameOrID)
	}

	// 下载技能内容
	content, err := client.DownloadSkillContent(ctx, matched.ID)
	if err != nil {
		return "", fmt.Errorf("下载技能内容失败: %w", err)
	}

	lines := []string{
		fmt.Sprintf("名称: %s", matched.Name),
		fmt.Sprintf("显示名: %s", matched.DisplayName),
		fmt.Sprintf("版本: %s", matched.Version),
		fmt.Sprintf("来源: %s", matched.Source),
		fmt.Sprintf("作用域: %s", matched.Scope),
		fmt.Sprintf("摘要: %s", matched.Summary),
		"",
		"--- SKILL.md 内容 ---",
		content.Content,
	}
	return strings.Join(lines, "\n"), nil
}

func (a *App) backendSkillsInstall(ctx context.Context, config internalconfig.Config, nameOrID string) (string, error) {
	client := buildProviderClient(config)
	if err := a.ensureBackendAuth(ctx, client, config); err != nil {
		return "", err
	}

	skillList, err := skills.FetchBackendSkills(ctx, client, provider.FetchVisibleSkillsParams{})
	if err != nil {
		return "", fmt.Errorf("获取后端技能列表失败: %w", err)
	}

	var matched *provider.VisibleSkillPackage
	for i := range skillList {
		s := &skillList[i]
		if strings.EqualFold(s.ID, nameOrID) || strings.EqualFold(s.Name, nameOrID) || strings.EqualFold(s.DisplayName, nameOrID) {
			matched = s
			break
		}
	}
	if matched == nil {
		return "", fmt.Errorf("未找到后端技能: %s", nameOrID)
	}

	clientInfo := a.buildBackendClientInfo(config)
	result, err := skills.InstallBackendSkill(ctx, client, config, *matched, clientInfo)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已安装后端技能: %s (版本 %s)\n安装目录: %s", result.Name, result.Version, result.TargetDir), nil
}

func (a *App) backendSkillsRemove(ctx context.Context, config internalconfig.Config, nameOrID string) (string, error) {
	client := buildProviderClient(config)
	if err := a.ensureBackendAuth(ctx, client, config); err != nil {
		return "", err
	}

	result, err := skills.RemoveBackendSkill(ctx, client, config, nameOrID)
	if err != nil {
		return "", err
	}
	msg := fmt.Sprintf("已卸载后端技能: %s", result.Name)
	if result.TargetDir != "" {
		msg += fmt.Sprintf("\n已清除本地缓存: %s", result.TargetDir)
	}
	return msg, nil
}

func (a *App) backendSkillsSync(ctx context.Context, config internalconfig.Config) (string, error) {
	client := buildProviderClient(config)
	if err := a.ensureBackendAuth(ctx, client, config); err != nil {
		return "", err
	}

	clientInfo := a.buildBackendClientInfo(config)
	result, err := skills.SyncBackendSkills(ctx, client, config, clientInfo)
	if err != nil {
		return "", err
	}

	lines := []string{"后端技能同步完成:"}
	if len(result.Installed) > 0 {
		lines = append(lines, fmt.Sprintf("  新安装 (%d): %s", len(result.Installed), strings.Join(result.Installed, ", ")))
	}
	if len(result.Updated) > 0 {
		lines = append(lines, fmt.Sprintf("  已更新 (%d): %s", len(result.Updated), strings.Join(result.Updated, ", ")))
	}
	if len(result.Failed) > 0 {
		lines = append(lines, fmt.Sprintf("  失败 (%d): %s", len(result.Failed), strings.Join(result.Failed, ", ")))
	}
	if len(result.Installed) == 0 && len(result.Updated) == 0 && len(result.Failed) == 0 {
		lines = append(lines, "  所有技能已是最新状态。")
	}
	return strings.Join(lines, "\n"), nil
}

func (a *App) backendSkillsUpload(ctx context.Context, config internalconfig.Config, localSkillName string) (string, error) {
	client := buildProviderClient(config)
	if err := a.ensureBackendAuth(ctx, client, config); err != nil {
		return "", err
	}

	result, err := skills.UploadLocalSkillToBackend(ctx, client, config, localSkillName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已上传技能到后端: %s (ID: %s, 版本: %s)", result.Name, result.SkillID, result.Version), nil
}

func (a *App) backendSkillsUploadFile(ctx context.Context, config internalconfig.Config, filePath string, displayName string) (string, error) {
	client := buildProviderClient(config)
	if err := a.ensureBackendAuth(ctx, client, config); err != nil {
		return "", err
	}

	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("解析文件路径失败: %w", err)
	}
	if _, err := os.Stat(absPath); err != nil {
		return "", fmt.Errorf("文件不存在: %s", absPath)
	}

	if displayName == "" {
		displayName = strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
	}

	ext := strings.ToLower(filepath.Ext(absPath))
	if ext == ".zip" {
		result, err := skills.UploadSkillArchiveToBackend(ctx, client, absPath, displayName)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("已上传 ZIP 技能包到后端: %s (ID: %s, 版本: %s)", result.Name, result.SkillID, result.Version), nil
	}

	// .md 文件按纯文本内容上传
	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("读取文件失败: %w", err)
	}
	result, err := skills.UploadSkillContentToBackend(ctx, client, displayName, string(content), displayName, "", "1.0.0")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已上传技能到后端: %s (ID: %s, 版本: %s)", result.Name, result.SkillID, result.Version), nil
}

func (a *App) backendSkillsDelete(ctx context.Context, config internalconfig.Config, skillID string) (string, error) {
	client := buildProviderClient(config)
	if err := a.ensureBackendAuth(ctx, client, config); err != nil {
		return "", err
	}

	if err := skills.DeleteBackendSkill(ctx, client, skillID); err != nil {
		return "", err
	}
	return fmt.Sprintf("已从后端删除自定义技能: %s", skillID), nil
}

func (a *App) buildBackendClientInfo(config internalconfig.Config) skills.ClientInfo {
	version := buildinfo.Version
	if version == "" {
		version = "dev"
	}
	platform := "darwin/arm64"
	return skills.ClientInfo{
		ClientID:       config.ClientID,
		DeviceID:      config.DeviceID,
		ClientPlatform: platform,
		ClientVersion:  version,
		InstallSource:  "cli-manual",
		InstallScope:   config.Skills.InstallScope,
	}
}

func (a *App) ensureBackendAuth(ctx context.Context, client *providerclient.Client, config internalconfig.Config) error {
	// 复用已有的认证流程
	_, _, err := a.ensureProviderAuthenticated(ctx, client, config)
	return err
}

// filterBackendSkillsByQuery 按关键词模糊过滤后端技能列表。
// 空关键词时返回全部技能（参考 chatui 的前端过滤逻辑）。
func filterBackendSkillsByQuery(skills []provider.VisibleSkillPackage, query string) []provider.VisibleSkillPackage {
	trimmedQuery := strings.ToLower(strings.TrimSpace(query))
	if trimmedQuery == "" || trimmedQuery == "*" {
		return skills
	}
	filtered := make([]provider.VisibleSkillPackage, 0, len(skills))
	for _, skill := range skills {
		name := strings.ToLower(strings.TrimSpace(skill.Name))
		displayName := strings.ToLower(strings.TrimSpace(skill.DisplayName))
		summary := strings.ToLower(strings.TrimSpace(skill.Summary))
		description := strings.ToLower(strings.TrimSpace(skill.Description))
		if strings.Contains(name, trimmedQuery) ||
			strings.Contains(displayName, trimmedQuery) ||
			strings.Contains(summary, trimmedQuery) ||
			strings.Contains(description, trimmedQuery) {
			filtered = append(filtered, skill)
		}
	}
	return filtered
}
