package cmd

import (
	"bytes"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestLoadSkillPayloadIncludesBundledEnabledSkills(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}

	app := &App{
		stdout:        &bytes.Buffer{},
		stderr:        &bytes.Buffer{},
		configManager: manager,
	}

	config := internalconfig.Default()
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	names, packages, err := app.loadSkillPayload(config)
	if err != nil {
		t.Fatalf("loadSkillPayload failed: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("expected enabled bundled skill names")
	}
	if !containsIgnoreCase(names, "updateConfig") {
		t.Fatalf("expected updateConfig in requested names: %#v", names)
	}
	payload, ok := packages.([]map[string]any)
	if !ok || len(payload) == 0 {
		t.Fatalf("expected bundled client skill packages, got %#v", packages)
	}
	found := false
	for _, pkg := range payload {
		if pkg["root_dir"] == "bundled" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected bundled package root, got %#v", payload)
	}
}

func TestExecuteSkillsDisableAndEnableBundledSkill(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}

	app := &App{
		stdout:        &bytes.Buffer{},
		stderr:        &bytes.Buffer{},
		configManager: manager,
	}

	config := internalconfig.Default()
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	if _, err := app.executeSkillsCommand([]string{"disable", "updateConfig"}); err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	updated, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if !containsIgnoreCase(updated.Skills.Disabled, "updateConfig") {
		t.Fatalf("expected disabled skill recorded, got %#v", updated.Skills.Disabled)
	}

	if _, err := app.executeSkillsCommand([]string{"enable", "updateConfig"}); err != nil {
		t.Fatalf("enable failed: %v", err)
	}
	updated, err = manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if containsIgnoreCase(updated.Skills.Disabled, "updateConfig") {
		t.Fatalf("expected disabled list cleared, got %#v", updated.Skills.Disabled)
	}
	if !containsIgnoreCase(updated.Skills.Enabled, "updateConfig") {
		t.Fatalf("expected enabled list updated, got %#v", updated.Skills.Enabled)
	}
}

func TestExecuteSkillsListGroupsBundledSkillsBySource(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, configManager: manager}
	config := internalconfig.Default()
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	output, err := app.executeSkillsCommand([]string{"list"})
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if !strings.Contains(output, "来源分组: 内置技能") {
		t.Fatalf("expected grouped bundled source, got %q", output)
	}
	if !strings.Contains(output, "taiyiflowApi") {
		t.Fatalf("expected renamed bundled skill, got %q", output)
	}
	if !strings.Contains(output, "状态 默认启用") {
		t.Fatalf("expected status badge, got %q", output)
	}
}

func TestExecuteSkillsViewShowsFilesAndDisplayLabels(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, configManager: manager}
	config := internalconfig.Default()
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	output, err := app.executeSkillsCommand([]string{"view", "taiyiflowApi"})
	if err != nil {
		t.Fatalf("view failed: %v", err)
	}
	if !strings.Contains(output, "来源: 内置技能") {
		t.Fatalf("expected display source label, got %q", output)
	}
	if !strings.Contains(output, "参考文件:") || !strings.Contains(output, "taiyiflow-cli-api-notes.md") {
		t.Fatalf("expected extracted file listing, got %q", output)
	}
	if !strings.Contains(output, "状态: 默认启用") {
		t.Fatalf("expected state label, got %q", output)
	}
}

func containsIgnoreCase(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}
