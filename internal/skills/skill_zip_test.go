package skills

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestPackSkillToZip_FromDisk(t *testing.T) {
	// 创建临时技能目录
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "my-test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// 写入 SKILL.md
	skillMD := `---
name: my-test-skill
description: 测试技能
category: test
---
这是测试技能正文。
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatalf("write SKILL.md failed: %v", err)
	}

	// 写入附属文件
	refDir := filepath.Join(skillDir, "references")
	if err := os.MkdirAll(refDir, 0o755); err != nil {
		t.Fatalf("mkdir references failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(refDir, "notes.md"), []byte("参考笔记"), 0o644); err != nil {
		t.Fatalf("write notes.md failed: %v", err)
	}

	item := Skill{
		Manifest: Manifest{
			Name:        "my-test-skill",
			Description: "测试技能",
			Category:    "test",
		},
		Path:    filepath.Join(skillDir, "SKILL.md"),
		RootDir: tmpDir,
		Source:  "workspace",
		Body:    "这是测试技能正文。",
	}

	zipData, err := PackSkillToZip(item, "my-test-skill")
	if err != nil {
		t.Fatalf("PackSkillToZip failed: %v", err)
	}
	if len(zipData) == 0 {
		t.Fatal("zip data is empty")
	}

	// 验证 ZIP 内容
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}

	expectedFiles := map[string]bool{
		"my-test-skill/SKILL.md":          false,
		"my-test-skill/references/notes.md": false,
	}
	for _, file := range reader.File {
		if _, ok := expectedFiles[file.Name]; ok {
			expectedFiles[file.Name] = true
		}
	}
	for name, found := range expectedFiles {
		if !found {
			t.Errorf("expected file %s in zip, but not found", name)
		}
	}
}

func TestPackSkillToZip_NoDiskDir(t *testing.T) {
	// 没有磁盘目录的情况（如内存中的技能）
	item := Skill{
		Manifest: Manifest{
			Name:        "memory-skill",
			Description: "内存技能",
			Category:    "test",
		},
		Body: "这是内存技能正文。",
	}

	zipData, err := PackSkillToZip(item, "memory-skill")
	if err != nil {
		t.Fatalf("PackSkillToZip failed: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}

	if len(reader.File) != 1 {
		t.Fatalf("expected 1 file in zip, got %d", len(reader.File))
	}
	if reader.File[0].Name != "memory-skill/SKILL.md" {
		t.Errorf("expected file name memory-skill/SKILL.md, got %s", reader.File[0].Name)
	}
}

func TestPackSkillToZip_EmptyDirName(t *testing.T) {
	item := Skill{
		Manifest: Manifest{
			Name: "test",
		},
		Body: "content",
	}
	_, err := PackSkillToZip(item, "")
	if err == nil {
		t.Fatal("expected error for empty dir name, got nil")
	}
}

func TestPackSkillToZip_BundledSkillWithReferences(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Config{}

	item, _, err := LoadByName(config, "taiyiflow-api")
	if err != nil {
		t.Fatalf("load bundled skill failed: %v", err)
	}

	zipData, err := PackSkillToZip(*item, "taiyiflow-api")
	if err != nil {
		t.Fatalf("PackSkillToZip failed: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		t.Fatalf("open zip failed: %v", err)
	}

	hasSkillMD := false
	hasReference := false
	for _, file := range reader.File {
		if file.Name == "taiyiflow-api/SKILL.md" {
			hasSkillMD = true
		}
		if file.Name == "taiyiflow-api/references/taiyiflow-cli-api-notes.md" {
			hasReference = true
		}
	}
	if !hasSkillMD {
		t.Error("expected SKILL.md in zip")
	}
	if !hasReference {
		t.Error("expected references/taiyiflow-cli-api-notes.md in zip")
	}
}
