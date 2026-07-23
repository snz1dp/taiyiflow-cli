package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// CreateSkillScaffoldResult 表示技能骨架创建结果
type CreateSkillScaffoldResult struct {
	Name      string
	Slug      string
	FilePath  string
	SkillDir  string
	Scope     string
}

// CreateSkillScaffold 在指定目录下创建技能包骨架文件 SKILL.md
// scope 为 "workspace" 或 "user"，workspace 时写入 <workdir>/.snz1dp/skills/，user 时写入 ~/.snz1dp/skills/
// explicitDir 为非空时直接用作技能根目录，忽略 scope
// overwrite 为 true 时，同名技能已存在则覆盖；为 false 时，同名技能已存在则报错
// explicitSlug 为非空时直接用作 slug（磁盘目录名），为空时由 name 自动生成
func CreateSkillScaffold(config internalconfig.Config, rawName string, explicitSlug string, scope string, explicitDir string, overwrite bool) (CreateSkillScaffoldResult, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return CreateSkillScaffoldResult{}, fmt.Errorf("技能名称不能为空")
	}

	slug := strings.TrimSpace(explicitSlug)
	if slug == "" {
		slug = SlugifySkillName(name)
	}
	if slug == "" {
		return CreateSkillScaffoldResult{}, fmt.Errorf("技能名称 %q 无法转换为有效的 slug（仅允许字母、数字、横线），请通过 slug 参数显式指定", name)
	}

	// 禁止覆盖内置技能
	if IsBundledSkill(name) {
		return CreateSkillScaffoldResult{}, fmt.Errorf("不允许创建或覆盖内置技能 %q", name)
	}

	// 确定目标目录
	var baseDir string
	if trimmedDir := strings.TrimSpace(explicitDir); trimmedDir != "" {
		// explicitDir 优先：直接用作技能根目录
		resolved, err := filepath.Abs(trimmedDir)
		if err != nil {
			return CreateSkillScaffoldResult{}, fmt.Errorf("解析 skill_dir 路径失败: %w", err)
		}
		baseDir = filepath.Clean(resolved)
	} else {
		// 默认使用 workspace 范围；仅在显式传入 user 时才使用用户目录
		normalizedScope := internalconfig.NormalizeSkillInstallScope(scope)
		if strings.TrimSpace(scope) == "" {
			normalizedScope = internalconfig.SkillInstallScopeWorkspace
		}
		if normalizedScope == internalconfig.SkillInstallScopeWorkspace {
			workdir := resolveSkillWorkdir(config.Workdir)
			if strings.TrimSpace(workdir) == "" {
				return CreateSkillScaffoldResult{}, fmt.Errorf("未找到可写入的工作区目录")
			}
			baseDir = filepath.Join(workdir, ".snz1dp", "skills")
		} else {
			// user scope
			directory := strings.TrimSpace(config.Skills.Directory)
			if directory != "" {
				resolved, err := filepath.Abs(directory)
				if err != nil {
					return CreateSkillScaffoldResult{}, err
				}
				baseDir = filepath.Clean(resolved)
			} else {
				defaultDir, err := internalconfig.DefaultSkillsDir()
				if err != nil {
					return CreateSkillScaffoldResult{}, err
				}
				resolved, err := filepath.Abs(defaultDir)
				if err != nil {
					return CreateSkillScaffoldResult{}, err
				}
				baseDir = filepath.Clean(resolved)
			}
		}
	}

	skillDir := filepath.Join(baseDir, slug)
	skillFilePath := filepath.Join(skillDir, skillFileName)

	// 检查是否已存在
	if _, err := os.Stat(skillFilePath); err == nil {
		if !overwrite {
			return CreateSkillScaffoldResult{}, fmt.Errorf("技能文件已存在: %s", skillFilePath)
		}
		// overwrite=true，继续覆盖写入
	}

	// 创建目录
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return CreateSkillScaffoldResult{}, fmt.Errorf("创建技能目录失败: %w", err)
	}

	// 生成骨架内容
	content := renderSkillScaffold(name, slug)

	if err := os.WriteFile(skillFilePath, []byte(content), 0o644); err != nil {
		return CreateSkillScaffoldResult{}, fmt.Errorf("写入技能文件失败: %w", err)
	}

	return CreateSkillScaffoldResult{
		Name:     name,
		Slug:     slug,
		FilePath: skillFilePath,
		SkillDir: skillDir,
		Scope:    scopeLabel(scope, explicitDir),
	}, nil
}

// DeleteSkillScaffoldResult 表示技能删除结果
type DeleteSkillScaffoldResult struct {
	Name     string
	Slug     string
	SkillDir string
	Scope    string
}

// DeleteSkillScaffold 删除指定名称和范围的本地技能目录
// scope 为 "workspace" 或 "user"，workspace 时删除 <workdir>/.snz1dp/skills/<slug>/，user 时删除 ~/.snz1dp/skills/<slug>/
// explicitDir 为非空时直接用作技能根目录，忽略 scope
// explicitSlug 为非空时直接用作 slug（磁盘目录名），为空时由 name 自动生成
func DeleteSkillScaffold(config internalconfig.Config, rawName string, explicitSlug string, scope string, explicitDir string) (DeleteSkillScaffoldResult, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return DeleteSkillScaffoldResult{}, fmt.Errorf("技能名称不能为空")
	}

	slug := strings.TrimSpace(explicitSlug)
	if slug == "" {
		slug = SlugifySkillName(name)
	}
	if slug == "" {
		return DeleteSkillScaffoldResult{}, fmt.Errorf("技能名称 %q 无法转换为有效的 slug（仅允许字母、数字、横线），请通过 slug 参数显式指定", name)
	}

	// 禁止删除内置技能
	if IsBundledSkill(name) {
		return DeleteSkillScaffoldResult{}, fmt.Errorf("不允许删除内置技能 %q", name)
	}

	// 确定目标目录
	var baseDir string
	if trimmedDir := strings.TrimSpace(explicitDir); trimmedDir != "" {
		// explicitDir 优先：直接用作技能根目录
		resolved, err := filepath.Abs(trimmedDir)
		if err != nil {
			return DeleteSkillScaffoldResult{}, fmt.Errorf("解析 skill_dir 路径失败: %w", err)
		}
		baseDir = filepath.Clean(resolved)
	} else {
		// 与 CreateSkillScaffold 保持一致的逻辑
		normalizedScope := internalconfig.NormalizeSkillInstallScope(scope)
		if strings.TrimSpace(scope) == "" {
			normalizedScope = internalconfig.SkillInstallScopeWorkspace
		}
		if normalizedScope == internalconfig.SkillInstallScopeWorkspace {
			workdir := resolveSkillWorkdir(config.Workdir)
			if strings.TrimSpace(workdir) == "" {
				return DeleteSkillScaffoldResult{}, fmt.Errorf("未找到可写入的工作区目录")
			}
			baseDir = filepath.Join(workdir, ".snz1dp", "skills")
		} else {
			directory := strings.TrimSpace(config.Skills.Directory)
			if directory != "" {
				resolved, err := filepath.Abs(directory)
				if err != nil {
					return DeleteSkillScaffoldResult{}, err
				}
				baseDir = filepath.Clean(resolved)
			} else {
				defaultDir, err := internalconfig.DefaultSkillsDir()
				if err != nil {
					return DeleteSkillScaffoldResult{}, err
				}
				resolved, err := filepath.Abs(defaultDir)
				if err != nil {
					return DeleteSkillScaffoldResult{}, err
				}
				baseDir = filepath.Clean(resolved)
			}
		}
	}

	skillDir := filepath.Join(baseDir, slug)

	// 检查技能目录是否存在
	skillFilePath := filepath.Join(skillDir, skillFileName)
	if _, err := os.Stat(skillFilePath); err != nil {
		if os.IsNotExist(err) {
			return DeleteSkillScaffoldResult{}, fmt.Errorf("技能不存在: %s", skillDir)
		}
		return DeleteSkillScaffoldResult{}, fmt.Errorf("检查技能目录失败: %w", err)
	}

	// 删除整个技能目录
	if err := os.RemoveAll(skillDir); err != nil {
		return DeleteSkillScaffoldResult{}, fmt.Errorf("删除技能目录失败: %w", err)
	}

	return DeleteSkillScaffoldResult{
		Name:     name,
		Slug:     slug,
		SkillDir: skillDir,
		Scope:    scopeLabel(scope, explicitDir),
	}, nil
}

// scopeLabel 返回用于结果中的 scope 标签。
// 当 explicitDir 非空时返回 "custom"，表示使用了自定义目录。
func scopeLabel(scope string, explicitDir string) string {
	if strings.TrimSpace(explicitDir) != "" {
		return "custom"
	}
	normalized := internalconfig.NormalizeSkillInstallScope(scope)
	if strings.TrimSpace(scope) == "" {
		normalized = internalconfig.SkillInstallScopeWorkspace
	}
	return normalized
}

func renderSkillScaffold(name string, slug string) string {
	return fmt.Sprintf(`---
name: %s
description: 在此处填写技能描述（场景导向，如"当…时使用"）
category: general
---

# %s

在此处编写技能正文内容。

<!--
  说明：
  - name（上方 frontmatter 中的）是人类友好阅读名称，可使用任意语言文字。
  - slug 是机器标识符，由技能名称自动生成，仅包含小写字母、数字和横线，
    用作磁盘目录名和后端技能唯一标识。
  - 两者不需要保持一致。例如 name 可以是"沙盒安全防护测试"，slug 则为"sandbox-security-test"。
  - 当前 slug: %s
-->

## 触发条件

描述什么情况下应该使用这个技能。

## 执行步骤

1. 第一步
2. 第二步

## 验证方式

描述如何确认技能执行成功。
`, name, name, slug)
}
