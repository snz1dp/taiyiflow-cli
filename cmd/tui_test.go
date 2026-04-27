package cmd

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	internaltui "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

func TestBuildTUISkillsPickerIncludesRenamedBundledSkill(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Default()

	picker, err := buildTUISkillsPicker(config, "")
	if err != nil {
		t.Fatalf("buildTUISkillsPicker failed: %v", err)
	}
	if picker == nil || len(picker.Items) == 0 {
		t.Fatalf("expected picker items, got %#v", picker)
	}
	found := false
	for _, item := range picker.Items {
		if item.Name == "taiyiflowApi" {
			found = true
			if item.SourceLabel != "内置技能" {
				t.Fatalf("unexpected source label: %#v", item)
			}
			if len(item.ReferenceFiles) != 1 {
				t.Fatalf("expected reference files for taiyiflowApi, got %#v", item)
			}
		}
	}
	if !found {
		t.Fatalf("expected taiyiflowApi in picker: %#v", picker.Items)
	}
}

func TestBuildTUISkillsPickerLoadsRemoteItems(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/search" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","version":"1.2.3","updatedAt":1710000000}]}`))
	}))
	defer registry.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Default()
	config.Skills.RegistryURL = registry.URL

	picker, err := buildTUISkillsPicker(config, "remote")
	if err != nil {
		t.Fatalf("buildTUISkillsPicker failed: %v", err)
	}
	if picker == nil || len(picker.RemoteItems) != 1 {
		t.Fatalf("expected one remote item, got %#v", picker)
	}
	if picker.RemoteItems[0].Slug != "remote-skill" || picker.RemoteItems[0].Version != "1.2.3" {
		t.Fatalf("unexpected remote item: %#v", picker.RemoteItems[0])
	}
	if picker.RemoteItems[0].Summary != "远程技能摘要" {
		t.Fatalf("expected sanitized remote summary, got %#v", picker.RemoteItems[0])
	}
	if picker.RemoteQuery != "remote" {
		t.Fatalf("expected remote query to persist, got %#v", picker)
	}
}

func TestExecuteTUISkillsPickerInstallDetailRefreshesPickerAndPanel(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	workspaceDir := t.TempDir()
	registry := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/search":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","version":"1.2.3","updatedAt":1710000000}]}`))
		case "/api/v1/skills/remote-skill":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"skill":{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","updatedAt":1710000000},"latestVersion":{"version":"1.2.3"},"metadata":{"os":["macOS"],"systems":["cli"]},"owner":{"handle":"tester","displayName":"测试者"}}`))
		case "/api/v1/download":
			archiveBytes := buildRemoteSkillArchive(t, "1.2.3")
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
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, configManager: manager}

	result, err := app.executeTUISkillsPickerCommand(config, "/skills-picker install-detail remote-skill remote")
	if err != nil {
		t.Fatalf("executeTUISkillsPickerCommand failed: %v", err)
	}
	if result.SkillsPicker == nil || len(result.SkillsPicker.RemoteItems) != 1 {
		t.Fatalf("expected refreshed remote picker, got %#v", result.SkillsPicker)
	}
	if !result.SkillsPicker.RemoteItems[0].Installed {
		t.Fatalf("expected remote item to be marked installed, got %#v", result.SkillsPicker.RemoteItems[0])
	}
	if len(result.Events) < 2 || result.Events[1].Panel == nil {
		t.Fatalf("expected note and detail panel events, got %#v", result.Events)
	}
	if !strings.Contains(result.Events[1].Panel.Body, "发布者: 测试者 (tester)") {
		t.Fatalf("expected full remote detail panel, got %#v", result.Events[1].Panel)
	}
	defaultSkillsDir, dirErr := internalconfig.DefaultSkillsDir()
	if dirErr != nil {
		t.Fatalf("default skills dir failed: %v", dirErr)
	}
	installedSkillPath := filepath.Join(defaultSkillsDir, "remote-skill", "SKILL.md")
	if _, statErr := os.Stat(installedSkillPath); statErr != nil {
		t.Fatalf("expected installed skill file, got %v", statErr)
	}
}

func TestBuildTUIClientToolPanelShowsSuggestedReadRangeForFileEditFailure(t *testing.T) {
	panel := buildTUIClientToolPanel("file_edit", map[string]any{
		"path":           "cmd/root.go",
		"status":         "not_found",
		"sha256_before":  "abc123",
		"nearby_excerpt": "10: func main() {\n11: \tprintln(\"hello\")",
		"prefetched_read": map[string]any{
			"start_line": 10,
			"end_line":   11,
			"content":    "func main() {\n\tprintln(\"hello\")",
		},
		"suggested_read_start_line": 10,
		"suggested_read_end_line":   11,
	}, assertError("file_edit 未找到 old_string"))
	if panel == nil {
		t.Fatal("expected panel")
	}
	if !strings.Contains(panel.Body, "建议先读: read_file cmd/root.go 行 10-11") {
		t.Fatalf("unexpected panel body: %#v", panel)
	}
	if !strings.Contains(panel.Body, "预取上下文(10-11 行):") || !strings.Contains(panel.Body, "println(\"hello\")") {
		t.Fatalf("expected prefetched context in panel body: %#v", panel)
	}
}

func TestApplyConfigFormSubmissionIncludesPermissionFields(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app := &App{configManager: manager}
	if err := manager.Save(internalconfig.Default()); err != nil {
		t.Fatalf("save default config failed: %v", err)
	}

	updated, err := app.applyConfigFormSubmission(internaltui.ConfigFormSubmission{
		Values: internaltui.ConfigFormValues{
			BackendBaseURL:           "http://localhost:7860",
			ApprovalMode:             "auto",
			SandboxEscapePolicy:      "allow",
			SandboxRoots:             "/tmp/a, /tmp/b",
			PermissionRules:          "tool:shell_exec=deny; shell:git status=allow",
			ReasoningEnabled:         true,
			Workdir:                  "/workspace/demo",
			CommandTimeoutSeconds:    "45",
			StreamIdleTimeoutSeconds: "300",
			OutputLanguage:           "zh-CN",
			SkillsDirectory:          "/workspace/demo/.snz1dp/skills",
			SkillsRegistryURL:        "https://mirror-cn.clawhub.com",
		},
		KeepAPIKey: true,
	})
	if err != nil {
		t.Fatalf("applyConfigFormSubmission failed: %v", err)
	}
	if updated.SandboxEscapePolicy != "allow" {
		t.Fatalf("expected allow sandbox policy, got %#v", updated)
	}
	if len(updated.SandboxRoots) != 2 || updated.SandboxRoots[0] != "/tmp/a" || updated.SandboxRoots[1] != "/tmp/b" {
		t.Fatalf("unexpected sandbox roots: %#v", updated.SandboxRoots)
	}
	if len(updated.PermissionRules) != 2 {
		t.Fatalf("expected 2 permission rules, got %#v", updated.PermissionRules)
	}
	if updated.PermissionRules[0].ToolName != "shell_exec" || updated.PermissionRules[0].Action != "deny" {
		t.Fatalf("unexpected first permission rule: %#v", updated.PermissionRules[0])
	}
	if updated.PermissionRules[1].CommandPrefix != "git status" || updated.PermissionRules[1].Action != "allow" {
		t.Fatalf("unexpected second permission rule: %#v", updated.PermissionRules[1])
	}
	if updated.Skills.Directory != "/workspace/demo/.snz1dp/skills" {
		t.Fatalf("expected skills directory saved, got %#v", updated.Skills)
	}
	if updated.CommandTimeoutSeconds != 45 {
		t.Fatalf("expected command timeout saved, got %#v", updated.CommandTimeoutSeconds)
	}

	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("load updated config failed: %v", err)
	}
	if len(reloaded.PermissionRules) != 2 || len(reloaded.SandboxRoots) != 2 {
		t.Fatalf("expected persisted permission fields, got %#v", reloaded)
	}
	if reloaded.Skills.Directory != "/workspace/demo/.snz1dp/skills" {
		t.Fatalf("expected persisted skills directory, got %#v", reloaded.Skills)
	}
	if reloaded.CommandTimeoutSeconds != 45 {
		t.Fatalf("expected persisted command timeout, got %#v", reloaded.CommandTimeoutSeconds)
	}
	if got := formatPermissionRulesInline(reloaded.PermissionRules); got != "tool:shell_exec=deny; shell:git status=allow" {
		t.Fatalf("unexpected formatted rules: %q", got)
	}
	if got := formatSandboxRootsInline(reloaded.SandboxRoots); got != "/tmp/a, /tmp/b" {
		t.Fatalf("unexpected formatted roots: %q", got)
	}
}

func TestParsePermissionRulesInlineRejectsInvalidFormat(t *testing.T) {
	_, err := parsePermissionRulesInline("shell_exec=deny")
	if err == nil {
		t.Fatal("expected invalid permission rule format to fail")
	}
}

func assertError(message string) error {
	return simpleError(message)
}

type simpleError string

func (e simpleError) Error() string {
	return string(e)
}
