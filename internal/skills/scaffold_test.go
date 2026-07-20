package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestCreateSkillScaffold_WorkspaceScope(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	result, err := CreateSkillScaffold(config, "my-test-skill", internalconfig.SkillInstallScopeWorkspace, false)
	if err != nil {
		t.Fatalf("CreateSkillScaffold failed: %v", err)
	}

	// 验证 slug
	if result.Slug != "my-test-skill" {
		t.Errorf("expected slug 'my-test-skill', got %q", result.Slug)
	}

	// 验证路径在工作区下
	expectedDir := filepath.Join(tempDir, ".snz1dp", "skills", "my-test-skill")
	expectedFile := filepath.Join(expectedDir, "SKILL.md")
	if result.FilePath != expectedFile {
		t.Errorf("expected file path %q, got %q", expectedFile, result.FilePath)
	}

	// 验证文件存在
	if _, err := os.Stat(expectedFile); err != nil {
		t.Fatalf("skill file not created: %v", err)
	}

	// 验证文件内容包含 name 字段
	content, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("read skill file failed: %v", err)
	}
	if !strings.Contains(string(content), "name: my-test-skill") {
		t.Errorf("skill file does not contain name field")
	}

	// 验证 scope
	if result.Scope != internalconfig.SkillInstallScopeWorkspace {
		t.Errorf("expected scope 'workspace', got %q", result.Scope)
	}
}

func TestCreateSkillScaffold_UserScope(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir
	config.Skills.Directory = filepath.Join(tempDir, "user-skills")

	result, err := CreateSkillScaffold(config, "user-skill", internalconfig.SkillInstallScopeUser, false)
	if err != nil {
		t.Fatalf("CreateSkillScaffold failed: %v", err)
	}

	// 验证路径在用户目录下
	expectedFile := filepath.Join(tempDir, "user-skills", "user-skill", "SKILL.md")
	if result.FilePath != expectedFile {
		t.Errorf("expected file path %q, got %q", expectedFile, result.FilePath)
	}

	// 验证 scope
	if result.Scope != internalconfig.SkillInstallScopeUser {
		t.Errorf("expected scope 'user', got %q", result.Scope)
	}
}

func TestCreateSkillScaffold_DefaultScopeIsWorkspace(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	// 不传 scope，默认应该是 workspace
	result, err := CreateSkillScaffold(config, "default-scope-test", "", false)
	if err != nil {
		t.Fatalf("CreateSkillScaffold failed: %v", err)
	}

	if result.Scope != internalconfig.SkillInstallScopeWorkspace {
		t.Errorf("expected default scope 'workspace', got %q", result.Scope)
	}
}

func TestCreateSkillScaffold_EmptyName(t *testing.T) {
	config := internalconfig.Config{}
	_, err := CreateSkillScaffold(config, "", internalconfig.SkillInstallScopeWorkspace, false)
	if err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
}

func TestCreateSkillScaffold_AlreadyExists(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	// 第一次创建
	_, err := CreateSkillScaffold(config, "duplicate-skill", internalconfig.SkillInstallScopeWorkspace, false)
	if err != nil {
		t.Fatalf("first CreateSkillScaffold failed: %v", err)
	}

	// 第二次创建应该失败
	_, err = CreateSkillScaffold(config, "duplicate-skill", internalconfig.SkillInstallScopeWorkspace, false)
	if err == nil {
		t.Fatal("expected error for duplicate skill, got nil")
	}
	if !strings.Contains(err.Error(), "已存在") {
		t.Errorf("expected '已存在' in error, got: %v", err)
	}
}

func TestCreateSkillScaffold_SlugifyName(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	// 名称包含空格和大写
	result, err := CreateSkillScaffold(config, "My Cool Skill", internalconfig.SkillInstallScopeWorkspace, false)
	if err != nil {
		t.Fatalf("CreateSkillScaffold failed: %v", err)
	}

	if result.Slug != "my-cool-skill" {
		t.Errorf("expected slug 'my-cool-skill', got %q", result.Slug)
	}

	// 验证目录名是 slug 风格
	expectedDir := filepath.Join(tempDir, ".snz1dp", "skills", "my-cool-skill")
	if result.SkillDir != expectedDir {
		t.Errorf("expected skill dir %q, got %q", expectedDir, result.SkillDir)
	}
}

func TestCreateSkillScaffold_OverwriteTrue(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	// 第一次创建
	_, err := CreateSkillScaffold(config, "overwrite-skill", internalconfig.SkillInstallScopeWorkspace, false)
	if err != nil {
		t.Fatalf("first CreateSkillScaffold failed: %v", err)
	}

	// 第二次创建，overwrite=true，应该成功覆盖
	result, err := CreateSkillScaffold(config, "overwrite-skill", internalconfig.SkillInstallScopeWorkspace, true)
	if err != nil {
		t.Fatalf("CreateSkillScaffold with overwrite=true failed: %v", err)
	}

	// 验证文件存在且可读
	if _, err := os.Stat(result.FilePath); err != nil {
		t.Fatalf("skill file not found after overwrite: %v", err)
	}

	// 验证文件内容仍然包含 name 字段
	content, err := os.ReadFile(result.FilePath)
	if err != nil {
		t.Fatalf("read skill file failed: %v", err)
	}
	if !strings.Contains(string(content), "name: overwrite-skill") {
		t.Errorf("skill file does not contain name field after overwrite")
	}
}

func TestDeleteSkillScaffold_WorkspaceScope(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	// 先创建技能
	_, err := CreateSkillScaffold(config, "delete-me", internalconfig.SkillInstallScopeWorkspace, false)
	if err != nil {
		t.Fatalf("CreateSkillScaffold failed: %v", err)
	}

	// 验证文件存在
	expectedFile := filepath.Join(tempDir, ".snz1dp", "skills", "delete-me", "SKILL.md")
	if _, err := os.Stat(expectedFile); err != nil {
		t.Fatalf("skill file not created: %v", err)
	}

	// 删除技能
	result, err := DeleteSkillScaffold(config, "delete-me", internalconfig.SkillInstallScopeWorkspace)
	if err != nil {
		t.Fatalf("DeleteSkillScaffold failed: %v", err)
	}

	// 验证 slug
	if result.Slug != "delete-me" {
		t.Errorf("expected slug 'delete-me', got %q", result.Slug)
	}

	// 验证目录已删除
	expectedDir := filepath.Join(tempDir, ".snz1dp", "skills", "delete-me")
	if _, err := os.Stat(expectedDir); !os.IsNotExist(err) {
		t.Errorf("expected skill dir to be deleted, got stat error: %v", err)
	}

	// 验证 scope
	if result.Scope != internalconfig.SkillInstallScopeWorkspace {
		t.Errorf("expected scope 'workspace', got %q", result.Scope)
	}
}

func TestDeleteSkillScaffold_NotExists(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	_, err := DeleteSkillScaffold(config, "nonexistent-skill", internalconfig.SkillInstallScopeWorkspace)
	if err == nil {
		t.Fatal("expected error for nonexistent skill, got nil")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("expected '不存在' in error, got: %v", err)
	}
}

func TestDeleteSkillScaffold_EmptyName(t *testing.T) {
	config := internalconfig.Config{}
	_, err := DeleteSkillScaffold(config, "", internalconfig.SkillInstallScopeWorkspace)
	if err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
}

func TestDeleteSkillScaffold_UserScope(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir
	config.Skills.Directory = filepath.Join(tempDir, "user-skills")

	// 先创建技能
	_, err := CreateSkillScaffold(config, "user-delete", internalconfig.SkillInstallScopeUser, false)
	if err != nil {
		t.Fatalf("CreateSkillScaffold failed: %v", err)
	}

	// 删除技能
	result, err := DeleteSkillScaffold(config, "user-delete", internalconfig.SkillInstallScopeUser)
	if err != nil {
		t.Fatalf("DeleteSkillScaffold failed: %v", err)
	}

	// 验证目录已删除
	expectedDir := filepath.Join(tempDir, "user-skills", "user-delete")
	if _, err := os.Stat(expectedDir); !os.IsNotExist(err) {
		t.Errorf("expected skill dir to be deleted, got stat error: %v", err)
	}

	// 验证 scope
	if result.Scope != internalconfig.SkillInstallScopeUser {
		t.Errorf("expected scope 'user', got %q", result.Scope)
	}
}

func TestCreateSkillScaffold_BundledSkillRejected(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	// 尝试创建与内置技能同名的技能（即使 overwrite=true）
	_, err := CreateSkillScaffold(config, "planner", internalconfig.SkillInstallScopeWorkspace, true)
	if err == nil {
		t.Fatal("expected error when creating a bundled skill, got nil")
	}
	if !strings.Contains(err.Error(), "内置技能") {
		t.Errorf("expected error mentioning 内置技能, got %q", err)
	}

	// 尝试 overwrite=false 也应被拒绝
	_, err = CreateSkillScaffold(config, "debug", internalconfig.SkillInstallScopeWorkspace, false)
	if err == nil {
		t.Fatal("expected error when creating a bundled skill (overwrite=false), got nil")
	}
	if !strings.Contains(err.Error(), "内置技能") {
		t.Errorf("expected error mentioning 内置技能, got %q", err)
	}
}

func TestDeleteSkillScaffold_BundledSkillRejected(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	// 尝试删除内置技能
	_, err := DeleteSkillScaffold(config, "verify", internalconfig.SkillInstallScopeWorkspace)
	if err == nil {
		t.Fatal("expected error when deleting a bundled skill, got nil")
	}
	if !strings.Contains(err.Error(), "内置技能") {
		t.Errorf("expected error mentioning 内置技能, got %q", err)
	}

	// user scope 也应被拒绝
	_, err = DeleteSkillScaffold(config, "pua", internalconfig.SkillInstallScopeUser)
	if err == nil {
		t.Fatal("expected error when deleting a bundled skill (user scope), got nil")
	}
	if !strings.Contains(err.Error(), "内置技能") {
		t.Errorf("expected error mentioning 内置技能, got %q", err)
	}
}

func TestIsBundledSkill(t *testing.T) {
	// 已知内置技能
	if !IsBundledSkill("planner") {
		t.Error("expected planner to be a bundled skill")
	}
	if !IsBundledSkill("Planner") {
		t.Error("expected case-insensitive match for bundled skill")
	}
	if !IsBundledSkill(" debug ") {
		t.Error("expected trimmed match for bundled skill")
	}

	// 非内置技能
	if IsBundledSkill("my-custom-skill") {
		t.Error("did not expect my-custom-skill to be a bundled skill")
	}
	if IsBundledSkill("") {
		t.Error("did not expect empty string to be a bundled skill")
	}
}

// TestCreateSkillScaffold_NameNotEqualSlug 验证 name（人类友好名称）与 slug（机器标识符）可以不同。
// 当 name 包含非 ASCII 字符（如中文）时，slug 可能为空导致创建失败；
// 当 name 包含空格或大写字母时，slug 应为小写横线风格。
func TestCreateSkillScaffold_NameNotEqualSlug(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	// 名称包含空格和大写：slug 应为小写横线风格，name 保持原文
	result, err := CreateSkillScaffold(config, "My Cool Skill", internalconfig.SkillInstallScopeWorkspace, false)
	if err != nil {
		t.Fatalf("CreateSkillScaffold failed: %v", err)
	}

	// name 应保持原文
	if result.Name != "My Cool Skill" {
		t.Errorf("expected name 'My Cool Skill', got %q", result.Name)
	}

	// slug 应为小写横线风格
	if result.Slug != "my-cool-skill" {
		t.Errorf("expected slug 'my-cool-skill', got %q", result.Slug)
	}

	// name 和 slug 不应相同
	if result.Name == result.Slug {
		t.Errorf("name and slug should differ, both are %q", result.Name)
	}

	// 验证 SKILL.md frontmatter 中 name 字段保持原文
	content, err := os.ReadFile(result.FilePath)
	if err != nil {
		t.Fatalf("read skill file failed: %v", err)
	}
	if !strings.Contains(string(content), "name: My Cool Skill") {
		t.Errorf("skill file should contain 'name: My Cool Skill' in frontmatter, got: %s", string(content))
	}

	// 验证骨架模板中包含 slug 说明注释
	if !strings.Contains(string(content), "slug") {
		t.Errorf("skill file should mention slug in comments, got: %s", string(content))
	}
}

// TestCreateSkillScaffold_ChineseNameRejected 验证纯中文名称无法生成有效 slug 时应报错。
func TestCreateSkillScaffold_ChineseNameRejected(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	// 纯中文名称无法生成有效 slug，应报错
	_, err := CreateSkillScaffold(config, "沙盒安全防护测试", internalconfig.SkillInstallScopeWorkspace, false)
	if err == nil {
		t.Fatal("expected error for Chinese-only name (no valid slug), got nil")
	}
	if !strings.Contains(err.Error(), "slug") {
		t.Errorf("expected error mentioning slug, got: %v", err)
	}
}

// TestCreateSkillScaffold_MixedNameSlug 验证混合名称（含中文和英文）的 slug 生成。
func TestCreateSkillScaffold_MixedNameSlug(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{}
	config.Workdir = tempDir

	// 名称包含中文和英文：slug 应只保留英文部分
	result, err := CreateSkillScaffold(config, "沙盒安全 sandbox-security-test", internalconfig.SkillInstallScopeWorkspace, false)
	if err != nil {
		t.Fatalf("CreateSkillScaffold failed: %v", err)
	}

	// name 应保持原文
	if result.Name != "沙盒安全 sandbox-security-test" {
		t.Errorf("expected name '沙盒安全 sandbox-security-test', got %q", result.Name)
	}

	// slug 应只包含英文部分
	if result.Slug != "sandbox-security-test" {
		t.Errorf("expected slug 'sandbox-security-test', got %q", result.Slug)
	}

	// 验证 frontmatter 中 name 保持原文
	content, err := os.ReadFile(result.FilePath)
	if err != nil {
		t.Fatalf("read skill file failed: %v", err)
	}
	if !strings.Contains(string(content), "name: 沙盒安全 sandbox-security-test") {
		t.Errorf("skill file should contain original name in frontmatter")
	}
}
