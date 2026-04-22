package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestDiscoverSkillsAndEnableState(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "reviewer"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "reviewer", "SKILL.md"), []byte("---\nname: Code Review\ndescription: 审查变更\ncategory: engineering\nprerequisites:\n  - repo access\n---\n\n检查回归风险。\n"), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tempDir, "writer"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "writer", "SKILL.md"), []byte("---\nname: Draft Writer\ndescription: 生成初稿\ncategory: docs\n---\n\n输出规划草稿。\n"), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	config := internalconfig.Config{}
	config.Skills.Directory = tempDir
	config.Skills.Enabled = []string{"code review"}

	items, directory, err := Discover(config)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if directory != tempDir {
		t.Fatalf("unexpected directory: %s", directory)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(items))
	}
	if !items[0].Enabled {
		t.Fatalf("expected enabled skill to be sorted first")
	}
	if items[0].Name != "Code Review" {
		t.Fatalf("unexpected first skill: %s", items[0].Name)
	}

	item, _, err := LoadByName(config, "draft writer")
	if err != nil {
		t.Fatalf("load by name failed: %v", err)
	}
	if item.Name != "Draft Writer" {
		t.Fatalf("unexpected loaded skill: %s", item.Name)
	}
	if item.Enabled {
		t.Fatalf("expected Draft Writer disabled")
	}
}

func TestClientPackagesIncludeMarkdownAndContent(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "reviewer"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	content := "---\nname: Code Review\ndescription: 审查变更\ncategory: engineering\nprerequisites:\n  - repo access\n---\n\n检查回归风险。\n"
	if err := os.WriteFile(filepath.Join(tempDir, "reviewer", "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	config := internalconfig.Config{}
	config.Skills.Directory = tempDir
	config.Skills.Enabled = []string{"Code Review"}

	items, directory, err := Discover(config)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}

	payload, ok := ClientPackages(items, directory).([]map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %T", ClientPackages(items, directory))
	}
	if len(payload) != 1 {
		t.Fatalf("expected 1 package, got %d", len(payload))
	}
	skillsPayload, ok := payload[0]["skills"].([]map[string]any)
	if !ok || len(skillsPayload) != 1 {
		t.Fatalf("unexpected skills payload: %#v", payload[0]["skills"])
	}
	skillPayload := skillsPayload[0]
	if got := skillPayload["content"]; got != "检查回归风险。" {
		t.Fatalf("unexpected content: %#v", got)
	}
	markdown, ok := skillPayload["markdown"].(string)
	if !ok {
		t.Fatalf("unexpected markdown payload: %#v", skillPayload["markdown"])
	}
	if !strings.Contains(markdown, "name: Code Review") {
		t.Fatalf("markdown missing manifest: %s", markdown)
	}
	if !strings.Contains(markdown, "检查回归风险。") {
		t.Fatalf("markdown missing body: %s", markdown)
	}
}
