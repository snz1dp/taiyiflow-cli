package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"

	"gopkg.in/yaml.v3"
)

const skillFileName = "SKILL.md"

const (
	skillRequestTypeAsk   = "ask"
	skillRequestTypePlan  = "plan"
	skillRequestTypeAgent = "agent"
)

type Manifest struct {
	Name          string         `yaml:"name"`
	Description   string         `yaml:"description"`
	Homepage      string         `yaml:"homepage,omitempty"`
	Category      string         `yaml:"category"`
	Prerequisites []string       `yaml:"prerequisites"`
	Files         []SkillFile    `yaml:"files,omitempty"`
	Metadata      map[string]any `yaml:"metadata,omitempty"`
}

type SkillFile struct {
	Source      string `yaml:"source,omitempty"`
	Path        string `yaml:"path,omitempty"`
	Description string `yaml:"description,omitempty"`
}

type Skill struct {
	Manifest
	Path        string
	RootDir     string
	Source      string
	RelativeDir string
	Body        string
	Summary     string
	// Enabled 表示该技能是否纳入“渐进式披露”候选集合，并会随请求一起上送给后端。
	// 它不是“必须显式启用后才能使用”的白名单开关；当前默认策略是除非被
	// skills.disabled 显式禁用，否则所有技能都可被选用。skills.enabled 仅保留为
	// 显式放行/兼容配置。
	Enabled bool
}

func SkillDir(item Skill) string {
	if path := strings.TrimSpace(item.Path); path != "" {
		return filepath.Clean(filepath.Dir(path))
	}
	rootDir := strings.TrimSpace(item.RootDir)
	relativeDir := strings.TrimSpace(item.RelativeDir)
	if rootDir != "" && relativeDir != "" {
		return filepath.Clean(filepath.Join(rootDir, filepath.FromSlash(relativeDir)))
	}
	if rootDir != "" {
		return filepath.Clean(rootDir)
	}
	return ""
}

func RequiredEnvKeys(item Skill) []string {
	seen := map[string]bool{}
	collectRequiredEnvKeys(item.Metadata, seen)
	if len(seen) == 0 {
		return nil
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	return result
}

func collectRequiredEnvKeys(value any, seen map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		if requires, ok := typed["requires"]; ok {
			collectRequiredEnvValues(requires, seen)
		}
		for _, nested := range typed {
			collectRequiredEnvKeys(nested, seen)
		}
	case map[any]any:
		if requires, ok := typed["requires"]; ok {
			collectRequiredEnvValues(requires, seen)
		}
		for _, nested := range typed {
			collectRequiredEnvKeys(nested, seen)
		}
	case []any:
		for _, nested := range typed {
			collectRequiredEnvKeys(nested, seen)
		}
	}
}

func collectRequiredEnvValues(value any, seen map[string]bool) {
	switch typed := value.(type) {
	case map[string]any:
		collectRequiredEnvSlice(typed["env"], seen)
	case map[any]any:
		collectRequiredEnvSlice(typed["env"], seen)
	}
}

func collectRequiredEnvSlice(value any, seen map[string]bool) {
	for _, item := range metadataStringSlice(value) {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		seen[trimmed] = true
	}
}

func metadataStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, strings.TrimSpace(item))
		}
		return result
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			result = append(result, strings.TrimSpace(fmt.Sprint(item)))
		}
		return result
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		return []string{trimmed}
	default:
		return nil
	}
}

func ResolveDirectory(config internalconfig.Config) (string, error) {
	directories, err := ResolveDirectories(config)
	if err != nil {
		return "", err
	}
	if len(directories) == 0 {
		return "", nil
	}
	return directories[0], nil
}

func ResolveInstallDirectory(config internalconfig.Config) (string, error) {
	if scope := internalconfig.NormalizeSkillInstallScope(config.Skills.InstallScope); scope == internalconfig.SkillInstallScopeWorkspace {
		workdir := resolveSkillWorkdir(config.Workdir)
		if strings.TrimSpace(workdir) == "" {
			return "", fmt.Errorf("未找到可写入的技能目录")
		}
		return filepath.Join(workdir, ".snz1dp", "skills"), nil
	}
	directory := strings.TrimSpace(config.Skills.Directory)
	if directory != "" {
		resolved, err := filepath.Abs(directory)
		if err != nil {
			return "", err
		}
		return filepath.Clean(resolved), nil
	}
	defaultDir, err := internalconfig.DefaultSkillsDir()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.Abs(defaultDir)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func ResolveDirectories(config internalconfig.Config) ([]string, error) {
	items := []string{}
	if workdir := resolveSkillWorkdir(config.Workdir); workdir != "" {
		items = append(items, filepath.Join(workdir, ".snz1dp", "skills"))
	}
	directory := strings.TrimSpace(config.Skills.Directory)
	if directory != "" {
		items = append(items, directory)
	} else {
		homeSkillsDir, err := internalconfig.DefaultSkillsDir()
		if err != nil {
			return nil, err
		}
		items = append(items, homeSkillsDir)
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		resolved, err := filepath.Abs(item)
		if err != nil {
			return nil, err
		}
		resolved = filepath.Clean(resolved)
		if !seen[resolved] {
			seen[resolved] = true
			result = append(result, resolved)
		}
	}
	return result, nil
}

// DiscoverResult 包含发现的所有技能以及加载失败的远程技能信息。
type DiscoverResult struct {
	Items              []Skill
	Directory          string
	BrokenRemoteSkills []InstalledRemoteSkill
}

// Discover 发现本地技能目录中的所有技能，返回技能列表和目录信息。
// 加载失败的远程安装技能会记录在 BrokenRemoteSkills 中，供调用方触发重新下载。
func Discover(config internalconfig.Config) ([]Skill, string, error) {
	result, err := DiscoverDetailed(config)
	if err != nil {
		return nil, "", err
	}
	return result.Items, result.Directory, nil
}

// DiscoverDetailed 发现本地技能目录中的所有技能，返回包含加载失败远程技能信息的详细结果。
func DiscoverDetailed(config internalconfig.Config) (DiscoverResult, error) {
	directories, err := ResolveDirectories(config)
	if err != nil {
		return DiscoverResult{}, err
	}

	enabled := enabledSet(config.Skills.Enabled)
	disabled := enabledSet(config.Skills.Disabled)
	items := []Skill{}
	brokenRemoteSkills := []InstalledRemoteSkill{}
	seenNames := map[string]bool{}
	seenPaths := map[string]bool{}
	availableDirectories := []string{}
	for _, directory := range directories {
		info, err := os.Stat(directory)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return DiscoverResult{}, fmt.Errorf("读取技能目录失败: %w", err)
		}
		if !info.IsDir() {
			return DiscoverResult{}, fmt.Errorf("技能目录不是有效目录: %s", directory)
		}
		availableDirectories = append(availableDirectories, directory)
		err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				// 遍历过程中的子项错误（如权限不足）跳过，不中断整个发现流程
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Name() != skillFileName {
				return nil
			}
			skill, err := LoadFile(directory, path)
			if err != nil {
				// 单个技能文件加载失败时跳过，不中断整个发现流程。
				// 如果该技能目录下存在 origin.json（说明是远程安装的技能），
				// 记录到 brokenRemoteSkills 供调用方触发重新下载。
				skillDir := filepath.Dir(path)
				if origin, originErr := readRemoteOrigin(skillDir); originErr == nil && origin != nil {
					brokenRemoteSkills = append(brokenRemoteSkills, InstalledRemoteSkill{
						Name:             origin.Slug,
						Slug:             origin.Slug,
						TargetDir:        skillDir,
						RegistryURL:      origin.Registry,
						InstalledVersion: origin.InstalledVersion,
					})
				}
				return nil
			}
			identity := fileIdentity(path)
			if seenPaths[identity] {
				return nil
			}
			seenPaths[identity] = true
			nameKey := strings.ToLower(skill.Name)
			if seenNames[nameKey] {
				return nil
			}
			seenNames[nameKey] = true
			skill.Enabled = resolveSkillEnabled(skill.Name, enabled, disabled)
			skill.Source = classifySkillSource(directory, config)
			items = append(items, skill)
			return nil
		})
		if err != nil {
			return DiscoverResult{}, err
		}
	}

	for _, bundled := range discoverBundledSkills(config, enabled, disabled) {
		nameKey := strings.ToLower(bundled.Name)
		if seenNames[nameKey] {
			continue
		}
		seenNames[nameKey] = true
		items = append(items, bundled)
	}

	availableDirectories = append(availableDirectories, "bundled")

	sort.Slice(items, func(left, right int) bool {
		if items[left].Enabled != items[right].Enabled {
			return items[left].Enabled
		}
		leftSource := sourcePriority(items[left].Source)
		rightSource := sourcePriority(items[right].Source)
		if leftSource != rightSource {
			return leftSource < rightSource
		}
		return strings.ToLower(items[left].Name) < strings.ToLower(items[right].Name)
	})
	return DiscoverResult{
		Items:              items,
		Directory:          strings.Join(availableDirectories, " -> "),
		BrokenRemoteSkills: brokenRemoteSkills,
	}, nil
}

func LoadByName(config internalconfig.Config, name string) (*Skill, string, error) {
	items, directory, err := Discover(config)
	if err != nil {
		return nil, "", err
	}
	trimmed := strings.TrimSpace(name)
	// 第一轮：精确匹配 SKILL.md frontmatter 中的 name（友好名称，对应服务端 display_name）
	for _, item := range items {
		if strings.EqualFold(item.Name, trimmed) {
			copyItem := item
			return &copyItem, directory, nil
		}
	}
	// 第二轮：匹配别名（RelativeDir、目录名等）
	for _, item := range items {
		if matchesSkillAlias(item, trimmed) {
			copyItem := item
			return &copyItem, directory, nil
		}
	}
	// 第三轮：用 SlugifySkillName 匹配目录名（slug，对应服务端 name）
	// 适用于模型传入 slug 风格名称的场景
	targetSlug := SlugifySkillName(trimmed)
	if targetSlug != "" {
		for _, item := range items {
			dirBase := filepath.Base(SkillDir(item))
			if strings.EqualFold(dirBase, targetSlug) {
				copyItem := item
				return &copyItem, directory, nil
			}
		}
	}
	return nil, directory, fmt.Errorf("未找到技能: %s", trimmed)
}

// LoadBySlug 通过 slug（磁盘目录名）精确匹配本地技能
func LoadBySlug(config internalconfig.Config, slug string) (*Skill, string, error) {
	items, directory, err := Discover(config)
	if err != nil {
		return nil, "", err
	}
	trimmed := strings.TrimSpace(slug)
	if trimmed == "" {
		return nil, directory, fmt.Errorf("slug 不能为空")
	}
	for _, item := range items {
		dirBase := filepath.Base(SkillDir(item))
		if strings.EqualFold(dirBase, trimmed) {
			copyItem := item
			return &copyItem, directory, nil
		}
	}
	return nil, directory, fmt.Errorf("未找到技能 (slug=%s)", trimmed)
}

func matchesSkillAlias(item Skill, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if strings.EqualFold(item.RelativeDir, target) {
		return true
	}
	if relativeBase := filepath.Base(strings.TrimSpace(item.RelativeDir)); relativeBase != "." && relativeBase != "" {
		if strings.EqualFold(relativeBase, target) {
			return true
		}
	}
	if item.Path != "" {
		pathBase := filepath.Base(filepath.Dir(item.Path))
		if pathBase != "." && pathBase != "" && strings.EqualFold(pathBase, target) {
			return true
		}
	}
	return false
}

func Summaries(items []Skill) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"name":          item.Name,
			"description":   item.Description,
			"homepage":      item.Homepage,
			"category":      item.Category,
			"prerequisites": item.Prerequisites,
			"files":         item.Files,
			"metadata":      item.Metadata,
			"summary":       item.Summary,
			"path":          item.Path,
			"skill_dir":     SkillDir(item),
			"root_dir":      item.RootDir,
			"source":        item.Source,
			"relative_dir":  item.RelativeDir,
			"enabled":       item.Enabled,
		})
	}
	return result
}

func EnabledSkillNames(items []Skill) []string {
	names := []string{}
	for _, item := range items {
		if item.Enabled {
			names = append(names, item.Name)
		}
	}
	return names
}

func FilterByRequestType(items []Skill, requestType string) []Skill {
	normalizedRequestType := normalizeSkillRequestType(requestType)
	filtered := make([]Skill, 0, len(items))
	for _, item := range items {
		if skillSupportsRequestType(item, normalizedRequestType) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func ApplicableRequestTypes(item Skill) []string {
	if normalized := normalizeSkillRequestTypes(metadataRequestTypes(item.Metadata)); len(normalized) > 0 {
		return normalized
	}
	definition, ok := bundledSkillDefinitionByName(item.Name)
	if !ok {
		return nil
	}
	return normalizeSkillRequestTypes(definition.RequestTypes)
}

func metadataRequestTypes(metadata map[string]any) []string {
	if len(metadata) == 0 {
		return nil
	}
	if value, ok := metadata["request_types"]; ok {
		return metadataStringSlice(value)
	}
	if value, ok := metadata["request_type"]; ok {
		return metadataStringSlice(value)
	}
	return nil
}

func normalizeSkillRequestTypes(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized, ok := parseSkillRequestType(value)
		if !ok || seen[normalized] {
			continue
		}
		seen[normalized] = true
		result = append(result, normalized)
	}
	return result
}

func normalizeSkillRequestType(value string) string {
	if normalized, ok := parseSkillRequestType(value); ok {
		return normalized
	}
	return skillRequestTypeAsk
}

func parseSkillRequestType(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case skillRequestTypeAsk:
		return skillRequestTypeAsk, true
	case skillRequestTypePlan:
		return skillRequestTypePlan, true
	case skillRequestTypeAgent:
		return skillRequestTypeAgent, true
	default:
		return "", false
	}
}

func skillSupportsRequestType(item Skill, requestType string) bool {
	requestTypes := ApplicableRequestTypes(item)
	if len(requestTypes) == 0 {
		return true
	}
	for _, itemRequestType := range requestTypes {
		if itemRequestType == requestType {
			return true
		}
	}
	return false
}

func ClientPackages(items []Skill, rootDir string) any {
	_ = rootDir
	enabledItems := []Skill{}
	for _, item := range items {
		if item.Enabled {
			enabledItems = append(enabledItems, item)
		}
	}
	if len(enabledItems) == 0 {
		return nil
	}
	packages := []map[string]any{}
	packageIndexes := map[string]int{}
	for _, item := range enabledItems {
		root := item.RootDir
		if root == "" {
			if item.Path != "" {
				root = filepath.Dir(item.Path)
			}
		}
		if root == "" {
			root = item.Source
		}
		index, ok := packageIndexes[root]
		if !ok {
			index = len(packages)
			packageIndexes[root] = index
			packages = append(packages, map[string]any{
				"package_name": fmt.Sprintf("local_skills_%d", index+1),
				"root_dir":     root,
				"skills":       []map[string]any{},
			})
		}
		skillsValue, _ := packages[index]["skills"].([]map[string]any)
		skillName := item.Name
		// 默认启用的内置技能携带完整内容（summary=false），
		// 默认不启用的内置技能及其他来源技能只发送摘要（summary=true），
		// 由后端按需拉取全文。
		isSummary := item.Source != "bundled" || !bundledSkillDefaultEnabled(item.Name)
		content := item.Summary
		if !isSummary {
			content = item.Body
		}
		skillsValue = append(skillsValue, map[string]any{
			"name":          skillName,
			"description":   item.Description,
			"homepage":      item.Homepage,
			"category":      item.Category,
			"prerequisites": item.Prerequisites,
			"files":         item.Files,
			"metadata":      item.Metadata,
			"summary":       item.Summary,
			"content":       content,
			"is_summary":    isSummary,
			"path":          item.Path,
			"skill_dir":     SkillDir(item),
			"root_dir":      item.RootDir,
			"source":        item.Source,
			"relative_dir":  item.RelativeDir,
			"enabled":       item.Enabled,
		})
		packages[index]["skills"] = skillsValue
	}
	return packages
}

func Markdown(item Skill) string {
	return renderClientSkillMarkdown(item)
}

func renderClientSkillMarkdown(item Skill) string {
	manifestBytes, err := yaml.Marshal(item.Manifest)
	if err != nil {
		return strings.TrimSpace(item.Body)
	}

	manifest := strings.TrimRight(string(manifestBytes), "\n")
	body := strings.TrimSpace(item.Body)
	if body == "" {
		return "---\n" + manifest + "\n---"
	}
	return "---\n" + manifest + "\n---\n\n" + body
}

func LoadFile(rootDir string, path string) (Skill, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("读取技能文件失败: %w", err)
	}
	manifest, body, err := parseSkillDocument(string(content))
	if err != nil {
		return Skill{}, fmt.Errorf("解析技能文件失败 %s: %w", path, err)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return Skill{}, fmt.Errorf("技能文件缺少 name 字段: %s", path)
	}
	description := strings.TrimSpace(manifest.Description)
	if description == "" {
		description = firstParagraph(body)
		manifest.Description = description
	}
	summary := summarize(body)
	if strings.TrimSpace(summary) == "" {
		summary = description
	}
	relativeDir, relErr := filepath.Rel(rootDir, filepath.Dir(path))
	if relErr != nil {
		relativeDir = filepath.Base(filepath.Dir(path))
	}
	if relativeDir == "." {
		relativeDir = ""
	}
	return Skill{
		Manifest:    manifest,
		Path:        path,
		RootDir:     rootDir,
		RelativeDir: filepath.ToSlash(relativeDir),
		Body:        strings.TrimSpace(body),
		Summary:     summary,
	}, nil
}

func resolveSkillWorkdir(configured string) string {
	trimmed := strings.TrimSpace(configured)
	if trimmed == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return ""
		}
		trimmed = cwd
	}
	resolved, err := filepath.Abs(trimmed)
	if err != nil {
		return trimmed
	}
	return filepath.Clean(resolved)
}

func classifySkillSource(directory string, config internalconfig.Config) string {
	workdir := resolveSkillWorkdir(config.Workdir)
	if workdir != "" && filepath.Clean(directory) == filepath.Clean(filepath.Join(workdir, ".snz1dp", "skills")) {
		return "workspace"
	}
	configured := strings.TrimSpace(config.Skills.Directory)
	if configured != "" {
		resolvedConfigured, err := filepath.Abs(configured)
		if err == nil && filepath.Clean(directory) == filepath.Clean(resolvedConfigured) {
			return "custom"
		}
	}
	return "home"
}

func resolveSkillEnabled(name string, enabled map[string]bool, disabled map[string]bool) bool {
	nameKey := strings.ToLower(strings.TrimSpace(name))
	if nameKey == "" {
		return false
	}
	if disabled[nameKey] {
		return false
	}
	if enabled[nameKey] {
		return true
	}
	return true
}

func bundledSkillDefaultEnabled(name string) bool {
	definition, ok := bundledSkillDefinitionByName(name)
	return ok && definition.DefaultEnabled
}

func sourcePriority(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "custom":
		return 0
	case "workspace":
		return 1
	case "home":
		return 2
	case "bundled":
		return 3
	default:
		return 9
	}
}

func fileIdentity(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil && strings.TrimSpace(resolved) != "" {
		return filepath.Clean(resolved)
	}
	resolved, err = filepath.Abs(path)
	if err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func parseSkillDocument(content string) (Manifest, string, error) {
	trimmed := strings.ReplaceAll(content, "\r\n", "\n")
	if strings.HasPrefix(trimmed, "---\n") {
		frontmatter, body, ok := splitSkillDocumentSections(strings.TrimPrefix(trimmed, "---\n"))
		if !ok {
			return Manifest{}, "", fmt.Errorf("frontmatter 未正确结束")
		}
		manifest, err := parseSkillManifest(frontmatter)
		if err != nil {
			return Manifest{}, "", err
		}
		return manifest, body, nil
	}
	frontmatter, body, ok := splitSkillDocumentSections(trimmed)
	if !ok || !looksLikeSkillManifest(frontmatter) {
		return Manifest{}, trimmed, nil
	}
	manifest, err := parseSkillManifest(frontmatter)
	if err != nil {
		return Manifest{}, "", err
	}
	return manifest, body, nil
}

func splitSkillDocumentSections(content string) (string, string, bool) {
	if endMarker := strings.Index(content, "\n---\n"); endMarker >= 0 {
		return content[:endMarker], content[endMarker+5:], true
	}
	if strings.HasSuffix(content, "\n---") {
		return strings.TrimSuffix(content, "\n---"), "", true
	}
	return "", "", false
}

func looksLikeSkillManifest(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "name:"),
			strings.HasPrefix(trimmed, "description:"),
			strings.HasPrefix(trimmed, "homepage:"),
			strings.HasPrefix(trimmed, "category:"),
			strings.HasPrefix(trimmed, "prerequisites:"),
			strings.HasPrefix(trimmed, "files:"),
			strings.HasPrefix(trimmed, "metadata:"):
			return true
		}
	}
	return false
}

func parseSkillManifest(frontmatter string) (Manifest, error) {
	manifest := Manifest{}
	if err := yaml.Unmarshal([]byte(frontmatter), &manifest); err != nil {
		return Manifest{}, fmt.Errorf("frontmatter 解析失败: %w", err)
	}
	return manifest, nil
}

func summarize(body string) string {
	paragraph := firstParagraph(body)
	if strings.TrimSpace(paragraph) == "" {
		return ""
	}
	if len([]rune(paragraph)) <= 120 {
		return paragraph
	}
	runes := []rune(paragraph)
	return strings.TrimSpace(string(runes[:120])) + "..."
}

func firstParagraph(body string) string {
	paragraphs := strings.Split(strings.TrimSpace(body), "\n\n")
	for _, paragraph := range paragraphs {
		cleaned := cleanSkillParagraph(paragraph)
		if cleaned != "" {
			return cleaned
		}
	}
	return ""
}

func cleanSkillParagraph(paragraph string) string {
	lines := strings.Split(strings.ReplaceAll(paragraph, "\r\n", "\n"), "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		cleaned := cleanSkillLine(line)
		if cleaned != "" {
			parts = append(parts, cleaned)
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func cleanSkillLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return ""
	}
	if trimmed == "---" || strings.HasPrefix(trimmed, "```") {
		return ""
	}
	for strings.HasPrefix(trimmed, ">") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
	}
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "#") {
		trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if trimmed == "" {
			return ""
		}
		return ""
	}
	for _, prefix := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(trimmed, prefix) {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			break
		}
	}
	for index := 1; index <= 9; index++ {
		orderedPrefix := fmt.Sprintf("%d. ", index)
		if strings.HasPrefix(trimmed, orderedPrefix) {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, orderedPrefix))
			break
		}
	}
	return strings.TrimSpace(trimmed)
}

func enabledSet(items []string) map[string]bool {
	result := make(map[string]bool, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		result[strings.ToLower(trimmed)] = true
	}
	return result
}
