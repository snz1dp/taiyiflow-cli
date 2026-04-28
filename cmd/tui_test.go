package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	internaltui "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

func TestBuildTUISkillsPickerIncludesRenamedBundledSkill(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Default()

	picker, err := buildTUISkillsPicker(config, "", "")
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
		_, _ = writer.Write([]byte(`{"results":[{"slug":"remote-skill","displayName":"Remote Skill","summary":"远程技能摘要","popularity":4.8,"downloads":12345,"version":"1.2.3","updatedAt":1710000000}]}`))
	}))
	defer registry.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Default()
	config.Skills.RegistryURL = registry.URL

	picker, err := buildTUISkillsPicker(config, "", "remote")
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
	if picker.RemoteItems[0].Popularity != 4.8 || picker.RemoteItems[0].Downloads != 12345 {
		t.Fatalf("expected popularity/downloads to map, got %#v", picker.RemoteItems[0])
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

	promptResult, err := app.executeTUISkillsPickerCommand(config, workspaceDir, "/skills-picker install-detail-select remote-skill remote")
	if err != nil {
		t.Fatalf("executeTUISkillsPickerCommand failed: %v", err)
	}
	if promptResult.SkillsPicker == nil || len(promptResult.SkillsPicker.RemoteItems) != 1 {
		t.Fatalf("expected refreshed remote picker, got %#v", promptResult.SkillsPicker)
	}
	if len(promptResult.Events) == 0 || promptResult.Events[len(promptResult.Events)-1].Panel == nil {
		t.Fatalf("expected install target panel event, got %#v", promptResult.Events)
	}
	installCommand := promptResult.Events[len(promptResult.Events)-1].Panel.Actions[0].Command
	result, err := app.executeTUISkillsPickerCommand(config, workspaceDir, installCommand)
	if err != nil {
		t.Fatalf("execute install-scope failed: %v", err)
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

func TestExecuteTUISkillsPickerInstallSelectUsesRememberedSessionScope(t *testing.T) {
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
	app := &App{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, configManager: manager, skillInstallScopeSession: internalconfig.SkillInstallScopeWorkspace, skillInstallScopePinned: true}
	result, err := app.executeTUISkillsPickerCommand(config, workspaceDir, "/skills-picker install-select remote-skill remote")
	if err != nil {
		t.Fatalf("executeTUISkillsPickerCommand failed: %v", err)
	}
	if len(result.Events) == 0 || result.Events[0].Panel != nil {
		t.Fatalf("expected direct install note without prompt, got %#v", result.Events)
	}
	installedSkillPath := filepath.Join(workspaceDir, ".snz1dp", "skills", "remote-skill", "SKILL.md")
	if _, statErr := os.Stat(installedSkillPath); statErr != nil {
		t.Fatalf("expected workspace installed skill file, got %v", statErr)
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

func TestEmitTUIClientToolResultEventsEmitsStatusNoteBeforePanel(t *testing.T) {
	events := []internaltui.Event{}
	emitTUIClientToolResultEvents(func(event internaltui.Event) {
		events = append(events, event)
	}, "lsp_query", map[string]any{
		"operation": "find_references",
		"items": []any{
			map[string]any{"name": "symbol", "path": "internal/tui/app.go"},
		},
	}, nil)
	if len(events) != 2 {
		t.Fatalf("expected status note and panel, got %#v", events)
	}
	if events[0].Kind != internaltui.EventSystemNote || !strings.Contains(events[0].Text, "结果已生成") {
		t.Fatalf("expected system note before panel, got %#v", events)
	}
	if events[1].Kind != internaltui.EventToolPanel || events[1].Panel == nil {
		t.Fatalf("expected tool panel after note, got %#v", events)
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
			SkillReviewPolicy:        "ignore",
			WriteConfirmationPolicy:  "direct",
			SandboxRoots:             "/tmp/a, /tmp/b",
			PermissionRules:          "tool:shell_exec=deny; shell:git status=allow",
			ReasoningEnabled:         true,
			Workdir:                  "/workspace/demo",
			CommandTimeoutSeconds:    "45",
			StreamIdleTimeoutSeconds: "300",
			OutputLanguage:           "zh-CN",
			SkillsInstallScope:       "workspace",
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
	if updated.SkillReviewPolicy != "ignore" {
		t.Fatalf("expected skill review policy saved, got %#v", updated.SkillReviewPolicy)
	}
	if updated.WriteConfirmationPolicy != "direct" {
		t.Fatalf("expected write confirmation policy saved, got %#v", updated.WriteConfirmationPolicy)
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
	if updated.Skills.InstallScope != "workspace" {
		t.Fatalf("expected skills install scope saved, got %#v", updated.Skills)
	}
	if updated.Skills.Directory != "" {
		t.Fatalf("expected legacy skills directory cleared, got %#v", updated.Skills)
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
	if reloaded.Skills.InstallScope != "workspace" {
		t.Fatalf("expected persisted skills install scope, got %#v", reloaded.Skills)
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

func TestBuildTUIClientToolConfirmRequestUsesSkillAndWritePolicies(t *testing.T) {
	workspaceDir := t.TempDir()
	skillDir := filepath.Join(workspaceDir, ".snz1dp", "skills", "context-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: Context Review\ndescription: 审查工作区技能\n---\n\n检查工作区上下文。\n"), 0o644); err != nil {
		t.Fatalf("write skill file failed: %v", err)
	}

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	skillRequest := buildTUIClientToolConfirmRequest(config, workspaceDir, map[string]any{
		"function": map[string]any{
			"name":      "skill",
			"arguments": map[string]any{"name": "Context Review"},
		},
	}, func(context.Context) error { return nil }, func(context.Context) error { return nil })
	if skillRequest.Title != "技能包审查(skill package review)" {
		t.Fatalf("unexpected skill request title: %#v", skillRequest)
	}
	if skillRequest.ApproveLabel != "本次允许" || skillRequest.RejectLabel != "本次拒绝" || skillRequest.AlwaysApproveLabel != "始终允许此技能" || skillRequest.AllowAllLabel != "允许全部技能" {
		t.Fatalf("unexpected skill request labels: %#v", skillRequest)
	}
	if !strings.Contains(skillRequest.PreviewBody, "相对目录: context-review") {
		t.Fatalf("expected skill preview to include relative dir, got %#v", skillRequest.PreviewBody)
	}

	writeRequest := buildTUIClientToolConfirmRequest(config, workspaceDir, map[string]any{
		"function": map[string]any{
			"name":      "write_file",
			"arguments": map[string]any{"path": "notes.txt", "mode": "overwrite", "content": "hello world"},
		},
	}, nil, nil)
	if writeRequest.Title != "写入比较摘要(write summary)待确认" {
		t.Fatalf("unexpected write request title: %#v", writeRequest)
	}
	if writeRequest.ApproveLabel != "确认本次写入" || writeRequest.RejectLabel != "取消本次写入" {
		t.Fatalf("unexpected write request labels: %#v", writeRequest)
	}
	if !writeRequest.DisableDefaultAlwaysApprove {
		t.Fatalf("expected write request to disable always approve, got %#v", writeRequest)
	}
}

func TestApplyEffectiveSkillReviewForPayloadUsesApprovedSkillNames(t *testing.T) {
	workspaceDir := t.TempDir()
	skillDir := filepath.Join(workspaceDir, ".snz1dp", "skills", "context-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: Context Review\ndescription: 审查工作区技能\n---\n\n检查工作区上下文。\n"), 0o644); err != nil {
		t.Fatalf("write skill file failed: %v", err)
	}

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.SkillReviewPolicy = internalconfig.SkillReviewPolicyConfirm
	updated := applyEffectiveSkillReviewForPayload(config, workspaceDir, agent.SessionState{
		ApprovedSkillNames: []string{"Context Review"},
	}, map[string]any{
		"function": map[string]any{
			"name":      "skill",
			"arguments": map[string]any{"name": "context-review"},
		},
	})
	if updated.SkillReviewPolicy != internalconfig.SkillReviewPolicyIgnore {
		t.Fatalf("expected approved skill to skip confirmation, got %#v", updated.SkillReviewPolicy)
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
