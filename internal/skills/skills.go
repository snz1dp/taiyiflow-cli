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
	Name          string   `yaml:"name"`
	Description   string   `yaml:"description"`
	Category      string   `yaml:"category"`
	Prerequisites []string `yaml:"prerequisites"`
}

type Skill struct {
	Manifest
	Path        string
	RelativeDir string
	Body        string
	Summary     string
	Enabled     bool
}

func ResolveDirectory(config internalconfig.Config) (string, error) {
	directory := strings.TrimSpace(config.Skills.Directory)
	if directory != "" {
		return filepath.Abs(directory)
	}
	return internalconfig.DefaultSkillsDir()
}

func Discover(config internalconfig.Config) ([]Skill, string, error) {
	directory, err := ResolveDirectory(config)
	if err != nil {
		return nil, "", err
	}
	info, err := os.Stat(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return []Skill{}, directory, nil
		}
		return nil, "", fmt.Errorf("读取技能目录失败: %w", err)
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("技能目录不是有效目录: %s", directory)
	}

	enabled := enabledSet(config.Skills.Enabled)
	items := []Skill{}
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
		skill.Enabled = enabled[strings.ToLower(skill.Name)]
		items = append(items, skill)
		return nil
	})
	if err != nil {
		return nil, "", err
	}

	sort.Slice(items, func(left, right int) bool {
		if items[left].Enabled != items[right].Enabled {
			return items[left].Enabled
		}
		return strings.ToLower(items[left].Name) < strings.ToLower(items[right].Name)
	})
	return items, directory, nil
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
	return nil, directory, fmt.Errorf("未找到技能: %s", trimmed)
}

func Summaries(items []Skill) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"name":          item.Name,
			"description":   item.Description,
			"category":      item.Category,
			"prerequisites": item.Prerequisites,
			"summary":       item.Summary,
			"path":          item.Path,
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
	enabledItems := []Skill{}
	for _, item := range items {
		if item.Enabled {
			enabledItems = append(enabledItems, item)
		}
	}
	if len(enabledItems) == 0 {
		return nil
	}
	return []map[string]any{
		{
			"package_name": "local_skills",
			"root_dir":     rootDir,
			"skills":       Summaries(enabledItems),
		},
	}
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
	if strings.TrimSpace(manifest.Description) == "" {
		manifest.Description = firstParagraph(body)
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
		RelativeDir: filepath.ToSlash(relativeDir),
		Body:        strings.TrimSpace(body),
		Summary:     summarize(body),
	}, nil
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
	if len([]rune(paragraph)) <= 120 {
		return paragraph
	}
	runes := []rune(paragraph)
	return strings.TrimSpace(string(runes[:120])) + "..."
}

func firstParagraph(body string) string {
	paragraphs := strings.Split(strings.TrimSpace(body), "\n\n")
	for _, paragraph := range paragraphs {
		cleaned := strings.TrimSpace(paragraph)
		if cleaned != "" {
			return cleaned
		}
	}
	return ""
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
