package cmd

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

	names, packages, err := app.loadSkillPayload(config, "")
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

func TestLoadSkillPayloadUsesContextDirWorkspaceSkills(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, configManager: manager}
	workspaceDir := t.TempDir()
	skillDir := filepath.Join(workspaceDir, ".snz1dp", "skills", "context-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	content := "---\nname: Context Review\ndescription: 当前工作区技能\ncategory: engineering\n---\n\n检查上下文目录。\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill file failed: %v", err)
	}
	config := internalconfig.Default()
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	names, packages, err := app.loadSkillPayload(config, workspaceDir)
	if err != nil {
		t.Fatalf("loadSkillPayload failed: %v", err)
	}
	if !containsIgnoreCase(names, "Context Review") {
		t.Fatalf("expected workspace skill in requested names, got %#v", names)
	}
	payload, ok := packages.([]map[string]any)
	if !ok {
		t.Fatalf("unexpected packages type: %T", packages)
	}
	found := false
	for _, pkg := range payload {
		skillsPayload, ok := pkg["skills"].([]map[string]any)
		if !ok {
			continue
		}
		for _, item := range skillsPayload {
			if item["name"] == "Context Review" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected context workspace skill in payload, got %#v", payload)
	}
}

func TestLoadSkillPayloadReflectsDisabledSkillImmediately(t *testing.T) {
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
	names, _, err := app.loadSkillPayload(config, "")
	if err != nil {
		t.Fatalf("loadSkillPayload failed: %v", err)
	}
	if !containsIgnoreCase(names, "updateConfig") {
		t.Fatalf("expected updateConfig enabled before disable, got %#v", names)
	}
	if _, err := app.executeSkillsCommand([]string{"disable", "updateConfig"}); err != nil {
		t.Fatalf("disable skill failed: %v", err)
	}
	names, _, err = app.loadSkillPayload(config, "")
	if err != nil {
		t.Fatalf("loadSkillPayload after disable failed: %v", err)
	}
	if containsIgnoreCase(names, "updateConfig") {
		t.Fatalf("expected disabled skill to disappear immediately, got %#v", names)
	}
	if _, err := app.executeSkillsCommand([]string{"enable", "updateConfig"}); err != nil {
		t.Fatalf("re-enable skill failed: %v", err)
	}
	names, _, err = app.loadSkillPayload(config, "")
	if err != nil {
		t.Fatalf("loadSkillPayload after enable failed: %v", err)
	}
	if !containsIgnoreCase(names, "updateConfig") {
		t.Fatalf("expected re-enabled skill to recover immediately, got %#v", names)
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
	if !strings.Contains(output, "渐进披露") {
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
	if !strings.Contains(output, "状态: 渐进披露") {
		t.Fatalf("expected state label, got %q", output)
	}
}

func TestExecuteSkillsRemoteLifecycle(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	workspaceDir := t.TempDir()
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, configManager: manager}

	var (
		versionMu     sync.RWMutex
		latestVersion = "1.0.0"
	)
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/api/v1/search":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","popularity":4.8,"downloads":12345,"version":"1.0.0","updatedAt":1710000000}]}`))
		case request.URL.Path == "/api/v1/skills/remote-skill":
			versionMu.RLock()
			current := latestVersion
			versionMu.RUnlock()
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(fmt.Sprintf(`{"skill":{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","updatedAt":1710000000},"latestVersion":{"version":"%s"},"metadata":{"os":["macOS"],"systems":["cli"]},"owner":{"handle":"tester","displayName":"测试者"}}`, current)))
		case request.URL.Path == "/api/v1/download":
			requestedVersion := request.URL.Query().Get("version")
			if requestedVersion == "" {
				versionMu.RLock()
				requestedVersion = latestVersion
				versionMu.RUnlock()
			}
			archiveBytes := buildRemoteSkillArchive(t, requestedVersion)
			writer.Header().Set("Content-Type", "application/zip")
			_, _ = writer.Write(archiveBytes)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer registry.Close()

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.RegistryURL = registry.URL
	config.Skills.Enabled = []string{"Remote Skill"}
	config.Skills.Disabled = []string{"remote-skill"}
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	searchOutput, err := app.executeSkillsCommand([]string{"search", "remote"})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if !strings.Contains(searchOutput, registry.URL) || !strings.Contains(searchOutput, "remote-skill") {
		t.Fatalf("unexpected search output: %q", searchOutput)
	}
	if !strings.Contains(searchOutput, "远程技能摘要") || strings.Contains(searchOutput, `\n  远程技能摘要`) {
		t.Fatalf("expected sanitized remote summary in search output, got %q", searchOutput)
	}
	if !strings.Contains(searchOutput, "人气 4.8") || !strings.Contains(searchOutput, "下载 12345") {
		t.Fatalf("expected popularity/downloads in search output, got %q", searchOutput)
	}

	viewOutput, err := app.executeSkillsCommand([]string{"view-remote", "remote-skill"})
	if err != nil {
		t.Fatalf("view-remote failed: %v", err)
	}
	if !strings.Contains(viewOutput, "显示名称: Remote Skill") || !strings.Contains(viewOutput, "发布者: 测试者 (tester)") {
		t.Fatalf("unexpected remote view output: %q", viewOutput)
	}

	installOutput, err := app.executeSkillsCommand([]string{"install", "remote-skill"})
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	defaultSkillsDir, dirErr := internalconfig.DefaultSkillsDir()
	if dirErr != nil {
		t.Fatalf("default skills dir failed: %v", dirErr)
	}
	installedSkillPath := filepath.Join(defaultSkillsDir, "remote-skill", "SKILL.md")
	if _, err := os.Stat(installedSkillPath); err != nil {
		t.Fatalf("expected installed skill file: %v", err)
	}
	if !strings.Contains(installOutput, "已安装远程技能") || !strings.Contains(installOutput, "1.0.0") {
		t.Fatalf("unexpected install output: %q", installOutput)
	}

	versionMu.Lock()
	latestVersion = "2.0.0"
	versionMu.Unlock()

	updateOutput, err := app.executeSkillsCommand([]string{"update", "remote-skill"})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	installedBody, err := os.ReadFile(installedSkillPath)
	if err != nil {
		t.Fatalf("read installed skill failed: %v", err)
	}
	if !strings.Contains(string(installedBody), "Version 2.0.0") {
		t.Fatalf("expected updated archive content, got %q", string(installedBody))
	}
	if !strings.Contains(updateOutput, "1.0.0 -> 2.0.0") {
		t.Fatalf("unexpected update output: %q", updateOutput)
	}

	removeOutput, err := app.executeSkillsCommand([]string{"remove", "remote-skill"})
	if err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(installedSkillPath)); !os.IsNotExist(err) {
		t.Fatalf("expected skill directory removed, stat err=%v", err)
	}
	updatedConfig, err := manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if containsIgnoreCase(updatedConfig.Skills.Enabled, "Remote Skill") || containsIgnoreCase(updatedConfig.Skills.Disabled, "remote-skill") {
		t.Fatalf("expected removed skill aliases cleared, got enabled=%#v disabled=%#v", updatedConfig.Skills.Enabled, updatedConfig.Skills.Disabled)
	}
	if !strings.Contains(removeOutput, "已删除技能") {
		t.Fatalf("unexpected remove output: %q", removeOutput)
	}

	sourceOutput, err := app.executeSkillsCommand([]string{"source", "set", "https://example.com/skills/"})
	if err != nil {
		t.Fatalf("source set failed: %v", err)
	}
	if !strings.Contains(sourceOutput, "https://example.com/skills") {
		t.Fatalf("unexpected source set output: %q", sourceOutput)
	}
	updatedConfig, err = manager.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if updatedConfig.Skills.RegistryURL != "https://example.com/skills" {
		t.Fatalf("expected normalized registry URL, got %q", updatedConfig.Skills.RegistryURL)
	}

	resetOutput, err := app.executeSkillsCommand([]string{"source", "reset"})
	if err != nil {
		t.Fatalf("source reset failed: %v", err)
	}
	if !strings.Contains(resetOutput, internalconfig.Default().Skills.RegistryURL) {
		t.Fatalf("unexpected source reset output: %q", resetOutput)
	}
}

func buildRemoteSkillArchive(t *testing.T, version string) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	fileWriter, err := writer.Create("remote-skill/SKILL.md")
	if err != nil {
		t.Fatalf("create zip entry failed: %v", err)
	}
	content := fmt.Sprintf("---\nname: Remote Skill\ndescription: 远程技能 %s\ncategory: remote\n---\n\nVersion %s\n", version, version)
	if _, err := fileWriter.Write([]byte(content)); err != nil {
		t.Fatalf("write zip entry failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip writer failed: %v", err)
	}
	return buffer.Bytes()
}

func containsIgnoreCase(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}
