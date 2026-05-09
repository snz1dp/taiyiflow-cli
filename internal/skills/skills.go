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

type Manifest struct {
	Name          string      `yaml:"name"`
	Description   string      `yaml:"description"`
	Category      string      `yaml:"category"`
	Prerequisites []string    `yaml:"prerequisites"`
	Files         []SkillFile `yaml:"files,omitempty"`
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

func Discover(config internalconfig.Config) ([]Skill, string, error) {
	directories, err := ResolveDirectories(config)
	if err != nil {
		return nil, "", err
	}

	enabled := enabledSet(config.Skills.Enabled)
	disabled := enabledSet(config.Skills.Disabled)
	items := []Skill{}
	seenNames := map[string]bool{}
	seenPaths := map[string]bool{}
	availableDirectories := []string{}
	for _, directory := range directories {
		info, err := os.Stat(directory)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, "", fmt.Errorf("读取技能目录失败: %w", err)
		}
		if !info.IsDir() {
			return nil, "", fmt.Errorf("技能目录不是有效目录: %s", directory)
		}
		availableDirectories = append(availableDirectories, directory)
		err = filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Name() != skillFileName {
				return nil
			}
			skill, err := LoadFile(directory, path)
			if err != nil {
				return err
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
			return nil, "", err
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
	return items, strings.Join(availableDirectories, " -> "), nil
}

func LoadByName(config internalconfig.Config, name string) (*Skill, string, error) {
	items, directory, err := Discover(config)
	if err != nil {
		return nil, "", err
	}
	trimmed := strings.TrimSpace(name)
	for _, item := range items {
		if strings.EqualFold(item.Name, trimmed) {
			copyItem := item
			return &copyItem, directory, nil
		}
	}
	for _, item := range items {
		if matchesSkillAlias(item, trimmed) {
			copyItem := item
			return &copyItem, directory, nil
		}
	}
	return nil, directory, fmt.Errorf("未找到技能: %s", trimmed)
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
			"category":      item.Category,
			"prerequisites": item.Prerequisites,
			"files":         item.Files,
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
		skillsValue = append(skillsValue, map[string]any{
			"name":          item.Name,
			"description":   item.Description,
			"category":      item.Category,
			"prerequisites": item.Prerequisites,
			"files":         item.Files,
			"summary":       item.Summary,
			"content":       item.Body,
			"markdown":      renderClientSkillMarkdown(item),
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
	if !strings.HasPrefix(trimmed, "---\n") {
		return Manifest{}, trimmed, nil
	}
	remainder := strings.TrimPrefix(trimmed, "---\n")
	endMarker := strings.Index(remainder, "\n---\n")
	if endMarker < 0 {
		return Manifest{}, "", fmt.Errorf("frontmatter 未正确结束")
	}
	frontmatter := remainder[:endMarker]
	body := remainder[endMarker+5:]
	manifest := Manifest{}
	if err := yaml.Unmarshal([]byte(frontmatter), &manifest); err != nil {
		return Manifest{}, "", fmt.Errorf("frontmatter 解析失败: %w", err)
	}
	return manifest, body, nil
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
