package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

const (
	remoteOriginRelPath = ".snz1dp/origin.json"
)

// remoteOrigin 记录远程技能的安装来源元数据。
// backend_skills.go 在安装后端技能时写入此记录，
// 在卸载和同步时读取此记录来识别远程技能。
type remoteOrigin struct {
	Version          int    `json:"version"`
	Registry         string `json:"registry"`
	Slug             string `json:"slug"`
	InstalledVersion string `json:"installed_version"`
	InstalledAt      int64  `json:"installed_at"`
}

// InstalledRemoteSkill 表示一个已安装的远程技能（来源为后端服务）。
type InstalledRemoteSkill struct {
	Name             string
	Slug             string
	InstalledVersion string
	TargetDir        string
	RegistryURL      string
}

// ListInstalledRemoteSkills 发现本地所有带有 origin.json 的技能，
// 返回它们的名称、slug、版本和目录信息。
// 此函数仅扫描本地文件系统，不发起网络请求。
func ListInstalledRemoteSkills(config internalconfig.Config) ([]InstalledRemoteSkill, error) {
	return listInstalledRemoteSkills(config)
}

func listInstalledRemoteSkills(config internalconfig.Config) ([]InstalledRemoteSkill, error) {
	items, _, err := Discover(config)
	if err != nil {
		return nil, err
	}
	results := []InstalledRemoteSkill{}
	seen := map[string]bool{}
	for _, item := range items {
		skillDir := filepath.Dir(item.Path)
		origin, err := readRemoteOrigin(skillDir)
		if err != nil {
			// origin.json 读取或解析失败时跳过，不中断整个扫描
			continue
		}
		if origin == nil {
			continue
		}
		key := filepath.Clean(skillDir)
		if seen[key] {
			continue
		}
		seen[key] = true
		results = append(results, InstalledRemoteSkill{
			Name:             strings.TrimSpace(item.Name),
			Slug:             strings.TrimSpace(origin.Slug),
			InstalledVersion: strings.TrimSpace(origin.InstalledVersion),
			TargetDir:        skillDir,
			RegistryURL:      strings.TrimSpace(origin.Registry),
		})
	}
	sort.Slice(results, func(left int, right int) bool {
		leftName := strings.ToLower(results[left].Name)
		rightName := strings.ToLower(results[right].Name)
		if !strings.EqualFold(leftName, rightName) {
			return leftName < rightName
		}
		return strings.ToLower(results[left].Slug) < strings.ToLower(results[right].Slug)
	})
	return results, nil
}

// writeRemoteOrigin 将来源元数据写入技能目录下的 .snz1dp/origin.json
func writeRemoteOrigin(skillDir string, origin remoteOrigin) error {
	metadataPath := filepath.Join(skillDir, remoteOriginRelPath)
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o755); err != nil {
		return fmt.Errorf("创建技能来源目录失败: %w", err)
	}
	body, err := json.MarshalIndent(origin, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化技能来源失败: %w", err)
	}
	if err := os.WriteFile(metadataPath, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入技能来源失败: %w", err)
	}
	return nil
}

// readRemoteOrigin 从技能目录读取 .snz1dp/origin.json
func readRemoteOrigin(skillDir string) (*remoteOrigin, error) {
	metadataPath := filepath.Join(skillDir, remoteOriginRelPath)
	body, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取技能来源失败: %w", err)
	}
	var origin remoteOrigin
	if err := json.Unmarshal(body, &origin); err != nil {
		return nil, fmt.Errorf("解析技能来源失败: %w", err)
	}
	if strings.TrimSpace(origin.Slug) == "" {
		return nil, nil
	}
	return &origin, nil
}

// compactStrings 过滤空字符串并返回紧凑列表。
func compactStrings(items []string) []string {
	result := []string{}
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}

// ScanRemoteOrigins 直接扫描技能安装目录下的 .snz1dp/origin.json，
// 不依赖 Discover（不要求 SKILL.md 可解析）。
// 用于发现 SKILL.md 损坏或缺失但 origin.json 仍存在的远程安装技能，
// 以便后续重新从服务端下载恢复。
func ScanRemoteOrigins(config internalconfig.Config) ([]InstalledRemoteSkill, error) {
	directories, err := ResolveDirectories(config)
	if err != nil {
		return nil, err
	}
	results := []InstalledRemoteSkill{}
	seen := map[string]bool{}
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillDir := filepath.Join(directory, entry.Name())
			origin, err := readRemoteOrigin(skillDir)
			if err != nil || origin == nil {
				continue
			}
			key := filepath.Clean(skillDir)
			if seen[key] {
				continue
			}
			seen[key] = true
			// 尝试从 SKILL.md 读取技能名称，若失败则用 slug 作为名称
			name := origin.Slug
			skillFilePath := filepath.Join(skillDir, skillFileName)
			if content, err := os.ReadFile(skillFilePath); err == nil {
				if manifest, _, pErr := parseSkillDocument(string(content)); pErr == nil && strings.TrimSpace(manifest.Name) != "" {
					name = manifest.Name
				}
			}
			results = append(results, InstalledRemoteSkill{
				Name:             strings.TrimSpace(name),
				Slug:             strings.TrimSpace(origin.Slug),
				InstalledVersion: strings.TrimSpace(origin.InstalledVersion),
				TargetDir:        skillDir,
				RegistryURL:      strings.TrimSpace(origin.Registry),
			})
		}
	}
	sort.Slice(results, func(left int, right int) bool {
		leftSlug := strings.ToLower(results[left].Slug)
		rightSlug := strings.ToLower(results[right].Slug)
		return leftSlug < rightSlug
	})
	return results, nil
}
