package skills

import (
	"embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

//go:embed builtin
var bundledSkillFS embed.FS

func discoverBundledSkills(config internalconfig.Config, enabled map[string]bool, disabled map[string]bool) []Skill {
	items := make([]Skill, 0, len(bundledSkillDefinitions))
	for _, definition := range bundledSkillDefinitions {
		item, err := loadBundledSkill(config, definition)
		if err != nil {
			continue
		}
		item.Enabled = resolveSkillEnabled(item.Name, enabled, disabled)
		items = append(items, item)
	}
	return items
}

func loadBundledSkill(config internalconfig.Config, definition BundledSkillDefinition) (Skill, error) {
	assetPath := path.Join("builtin", definition.RelativeDir, skillFileName)
	content, err := bundledSkillFS.ReadFile(assetPath)
	if err != nil {
		return Skill{}, fmt.Errorf("读取内置技能失败 %s: %w", definition.Name, err)
	}
	manifest, body, err := parseSkillDocument(string(content))
	if err != nil {
		return Skill{}, fmt.Errorf("解析内置技能失败 %s: %w", definition.Name, err)
	}
	manifest.Name = definition.Name
	if strings.TrimSpace(manifest.Description) == "" {
		manifest.Description = definition.Summary
	}
	body = strings.TrimSpace(body)
	rootDir := "bundled"
	if len(manifest.Files) > 0 {
		rootDir, manifest.Files, err = extractBundledSkillFiles(config, definition, manifest.Files)
		if err != nil {
			return Skill{}, err
		}
	}
	return Skill{
		Manifest:    manifest,
		Path:        assetPath,
		RootDir:     rootDir,
		Source:      "bundled",
		RelativeDir: definition.RelativeDir,
		Body:        body,
		Summary:     summarize(body),
	}, nil
}

func extractBundledSkillFiles(config internalconfig.Config, definition BundledSkillDefinition, files []SkillFile) (string, []SkillFile, error) {
	_ = config
	rootDir, err := internalconfig.DefaultBundledSkillsDir()
	if err != nil {
		return "", nil, fmt.Errorf("解析内置技能目录失败 %s: %w", definition.Name, err)
	}
	targetRoot := filepath.Join(rootDir, definition.RelativeDir)
	if err := os.MkdirAll(targetRoot, 0o755); err != nil {
		return "", nil, fmt.Errorf("创建内置技能目录失败 %s: %w", definition.Name, err)
	}
	normalizedFiles := make([]SkillFile, 0, len(files))
	for _, item := range files {
		source := strings.TrimSpace(item.Source)
		if source == "" {
			return "", nil, fmt.Errorf("内置技能 %s 缺少文件条目", definition.Name)
		}
		relPath, err := normalizeBundledRelativePath(fallbackFilePath(item.Path, source))
		if err != nil {
			return "", nil, fmt.Errorf("内置技能 %s 的文件路径无效: %w", definition.Name, err)
		}
		assetPath := path.Join("builtin", definition.RelativeDir, path.Clean(source))
		content, err := bundledSkillFS.ReadFile(assetPath)
		if err != nil {
			return "", nil, fmt.Errorf("读取内置技能参考文件失败 %s: %w", assetPath, err)
		}
		targetPath := filepath.Join(targetRoot, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return "", nil, fmt.Errorf("创建内置技能参考文件目录失败 %s: %w", targetPath, err)
		}
		if err := os.WriteFile(targetPath, content, 0o644); err != nil {
			return "", nil, fmt.Errorf("写入内置技能参考文件失败 %s: %w", targetPath, err)
		}
		normalizedFiles = append(normalizedFiles, SkillFile{
			Source:      source,
			Path:        relPath,
			Description: strings.TrimSpace(item.Description),
		})
	}
	return targetRoot, normalizedFiles, nil
}

func fallbackFilePath(configured string, source string) string {
	trimmed := strings.TrimSpace(configured)
	if trimmed != "" {
		return trimmed
	}
	return path.Base(strings.TrimSpace(source))
}

func normalizeBundledRelativePath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("路径不能为空")
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." || cleaned == "" || cleaned == "/" {
		return "", fmt.Errorf("路径不能为空")
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." || path.IsAbs(cleaned) {
		return "", fmt.Errorf("路径必须限制在技能目录内")
	}
	return cleaned, nil
}
