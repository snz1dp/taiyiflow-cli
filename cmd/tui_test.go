package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
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
		if item.Name == "taiyiflow-api" {
			found = true
			if item.SourceLabel != "内置技能" {
				t.Fatalf("unexpected source label: %#v", item)
			}
			if len(item.ReferenceFiles) != 1 {
				t.Fatalf("expected reference files for taiyiflow-api, got %#v", item)
			}
		}
	}
	if !found {
		t.Fatalf("expected taiyiflow-api in picker: %#v", picker.Items)
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

func TestApplyConfigFormSubmissionReturnsFieldError(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	app := &App{configManager: manager}
	if err := manager.Save(internalconfig.Default()); err != nil {
		t.Fatalf("save default config failed: %v", err)
	}

	_, err = app.applyConfigFormSubmission(internaltui.ConfigFormSubmission{
		Values: internaltui.ConfigFormValues{
			BackendBaseURL:  "http://localhost:7860",
			ApprovalMode:    "auto",
			PermissionRules: "shell_exec=deny",
		},
	})
	if err == nil {
		t.Fatal("expected invalid permission rules to fail")
	}
	var fieldErr internaltui.ConfigFieldError
	if !errors.As(err, &fieldErr) {
		t.Fatalf("expected ConfigFieldError, got %T (%v)", err, err)
	}
	if fieldErr.Field != "permission_rules" {
		t.Fatalf("expected permission_rules field error, got %#v", fieldErr)
	}
	if !strings.Contains(fieldErr.Message, "权限规则格式错误") {
		t.Fatalf("expected original validation message preserved, got %#v", fieldErr)
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

func TestBuildTUISkillAutoInstallConfirmRequest(t *testing.T) {
	request := buildTUISkillAutoInstallConfirmRequest(skillInstallApprovalRequest{
		Candidate: skillInstallCandidate{
			Slug:        "context-review",
			DisplayName: "Context Review",
			Summary:     "上下文审查技能",
		},
		Scope:       internalconfig.SkillInstallScopeWorkspace,
		InstallPath: "/tmp/work/.snz1dp/skills",
		Reason:      "缺失技能名 Context Review 的精确匹配",
	})
	if !strings.Contains(request.Title, "自动安装技能") {
		t.Fatalf("unexpected title: %#v", request)
	}
	if request.Command != "install context-review" {
		t.Fatalf("expected install command preview, got %#v", request)
	}
	if !strings.Contains(request.PreviewBody, "/tmp/work/.snz1dp/skills") || !strings.Contains(request.PreviewBody, "上下文审查技能") {
		t.Fatalf("unexpected preview body: %#v", request)
	}
	if request.ApproveLabel != "安装并继续" || request.RejectLabel != "跳过安装" {
		t.Fatalf("unexpected labels: %#v", request)
	}
}

func TestApplyMetadataContextWindowBudgetInitializesSnapshotMetrics(t *testing.T) {
	snapshot := internaltui.Snapshot{}

	applyMetadataContextWindowBudget(&snapshot, 200000)

	if snapshot.RequestStats == nil || snapshot.RequestStats.TokenUsage == nil || snapshot.RequestStats.TokenUsage.ContextWindow == nil {
		t.Fatalf("expected context window metrics to be initialized, got %#v", snapshot.RequestStats)
	}
	if snapshot.RequestStats.TokenUsage.ContextWindow.MaxInputTokens != 200000 {
		t.Fatalf("expected max input tokens to be applied, got %#v", snapshot.RequestStats.TokenUsage.ContextWindow)
	}
}

func TestApplyMetadataContextWindowBudgetIgnoresInvalidBudget(t *testing.T) {
	snapshot := internaltui.Snapshot{}

	applyMetadataContextWindowBudget(&snapshot, 0)

	if snapshot.RequestStats != nil {
		t.Fatalf("expected request stats unchanged for invalid budget, got %#v", snapshot.RequestStats)
	}
}

func TestBuildTUISnapshotIncludesCurrentUserName(t *testing.T) {
	app := &App{}
	snapshot, err := app.buildTUISnapshot(context.Background(), &stubProvider{}, internalconfig.Default(), "", backendStatus{Available: true})
	if err != nil {
		t.Fatalf("buildTUISnapshot failed: %v", err)
	}
	if snapshot.CurrentUserName != "" {
		t.Fatalf("expected empty current user with default stub provider, got %#v", snapshot)
	}

	providerWithUser := &stubSnapshotProvider{stubProvider: &stubProvider{}, currentUser: &provider.CurrentUser{Name: "张三"}}
	snapshot, err = app.buildTUISnapshot(context.Background(), providerWithUser, internalconfig.Default(), "", backendStatus{Available: true})
	if err != nil {
		t.Fatalf("buildTUISnapshot failed: %v", err)
	}
	if snapshot.CurrentUserName != "张三" {
		t.Fatalf("expected current user name to be mapped, got %#v", snapshot)
	}
}

type stubSnapshotProvider struct {
	*stubProvider
	currentUser    *provider.CurrentUser
	currentUserErr error
	serviceConfig  *provider.ServiceConfig
	serviceErr     error
	metadata       *provider.Metadata
	metadataErr    error
	knowledgeBases *provider.KnowledgeBaseList
	knowledgeErr   error
}

func (s stubSnapshotProvider) GetCurrentUserInfo(context.Context) (*provider.CurrentUser, error) {
	return s.currentUser, s.currentUserErr
}

func (s stubSnapshotProvider) GetMetadata(context.Context, string) (*provider.Metadata, error) {
	if s.metadata != nil || s.metadataErr != nil {
		return s.metadata, s.metadataErr
	}
	return s.stubProvider.GetMetadata(context.Background(), "")
}

func (s stubSnapshotProvider) GetServiceConfig(context.Context) (*provider.ServiceConfig, error) {
	if s.serviceConfig != nil || s.serviceErr != nil {
		return s.serviceConfig, s.serviceErr
	}
	return s.stubProvider.GetServiceConfig(context.Background())
}

func (s stubSnapshotProvider) ListKnowledgeBases(context.Context, string, int, int) (*provider.KnowledgeBaseList, error) {
	if s.knowledgeBases != nil || s.knowledgeErr != nil {
		return s.knowledgeBases, s.knowledgeErr
	}
	return s.stubProvider.ListKnowledgeBases(context.Background(), "", 0, 0)
}

func TestBuildTUISnapshotMarksAuthRequired(t *testing.T) {
	app := &App{}
	providerWithAuthError := &stubSnapshotProvider{stubProvider: &stubProvider{}, currentUserErr: fmt.Errorf("HTTP 401 unauthorized")}
	snapshot, err := app.buildTUISnapshot(context.Background(), providerWithAuthError, internalconfig.Default(), "", backendStatus{Available: true})
	if err != nil {
		t.Fatalf("buildTUISnapshot failed: %v", err)
	}
	if !snapshot.AuthRequired {
		t.Fatalf("expected auth required snapshot, got %#v", snapshot)
	}
	if !strings.Contains(snapshot.AuthStatusReason, "/login") {
		t.Fatalf("expected auth status reason to include /login hint, got %#v", snapshot)
	}
}

func TestBuildInitialTUISnapshotMarksAuthRequired(t *testing.T) {
	app := &App{}
	snapshot, err := app.buildInitialTUISnapshot(internalconfig.Default(), "", backendStatus{Available: true, AuthRequired: true, Reason: "请重新登录太乙智启服务"}, nil)
	if err != nil {
		t.Fatalf("buildInitialTUISnapshot failed: %v", err)
	}
	if !snapshot.AuthRequired {
		t.Fatalf("expected initial snapshot to keep auth required, got %#v", snapshot)
	}
	if !strings.Contains(snapshot.AuthStatusReason, "/login") {
		t.Fatalf("expected initial auth status reason to include /login hint, got %#v", snapshot)
	}
	if !strings.Contains(snapshot.AuthStatusReason, "请重新登录太乙智启服务") {
		t.Fatalf("expected initial auth status reason to preserve backend reason, got %#v", snapshot)
	}
}

func TestPrepareInitialTUIAuthenticationRefreshesAccessTokenBeforeInitialSnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/taiyi/composer/api/v1/users/whoami/userinfo":
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(writer, `{"code":401,"message":"unauthorized"}`)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case "/taiyi/composer/api/v1/version":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"package":"taiyiflow","main_version":"1.0.0"}`)
		case "/xeai/sso/salt":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"salt-ticket","value":"salt-value"}}`)
		case "/xeai/sso":
			if got := request.URL.Query().Get("persistent_token"); got != "persist-1" {
				t.Fatalf("unexpected persistent token: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","persistent_token":"persist-2","idle_duration":3600,"scope":"employee"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AccessToken = "expired-access"
	config.PersistentToken = "persist-1"
	config.DeviceID = "device-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	providerClient := buildProviderClient(config)
	updatedConfig := app.prepareInitialTUIAuthentication(context.Background(), providerClient, config)
	backend := app.inspectBackendStatus(context.Background(), providerClient, updatedConfig.BackendBaseURL)
	snapshot, err := app.buildInitialTUISnapshot(updatedConfig, "", backend, nil)
	if err != nil {
		t.Fatalf("buildInitialTUISnapshot failed: %v", err)
	}
	if snapshot.AuthRequired {
		t.Fatalf("expected refreshed startup auth to avoid login overlay, got %#v", snapshot)
	}
	if reloaded, reloadErr := manager.Load(); reloadErr != nil {
		t.Fatalf("reload config failed: %v", reloadErr)
	} else if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-2" {
		t.Fatalf("expected refreshed auth state to persist before startup snapshot, got %#v", reloaded)
	}
}

func TestBuildTUIHelpLinesUsesLoginOverlayWording(t *testing.T) {
	lines := buildTUIHelpLines(internaltui.Snapshot{BackendAvailable: true})
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "/chat") {
		t.Fatalf("expected help text to remove /chat, got %q", joined)
	}
	if !strings.Contains(joined, "/internet [on|off|status]") || !strings.Contains(joined, "/knowledge 打开知识库范围选择器") || !strings.Contains(joined, "/files 查看当前对话附带的文件状态") {
		t.Fatalf("expected help text to include split dialogue commands, got %q", joined)
	}
	if !strings.Contains(joined, "/login 打开登录表单") {
		t.Fatalf("expected help text to describe /login as overlay entry, got %q", joined)
	}
	if !strings.Contains(joined, "kitty/iTerm2 终端图片协议") {
		t.Fatalf("expected help text to mention terminal image captcha rendering, got %q", joined)
	}
	if strings.Contains(joined, "/login [status|qr|password|logout]") {
		t.Fatalf("expected help text to drop legacy login command catalog, got %q", joined)
	}

	readonlyLines := buildTUIHelpLines(internaltui.Snapshot{BackendAvailable: false, BackendStatusReason: "服务端不可用"})
	readonlyJoined := strings.Join(readonlyLines, "\n")
	if !strings.Contains(readonlyJoined, "/login 打开登录表单") {
		t.Fatalf("expected readonly help text to direct to /login overlay, got %q", readonlyJoined)
	}
	if strings.Contains(readonlyJoined, "查看登录状态") {
		t.Fatalf("expected readonly help text to stop using legacy status wording, got %q", readonlyJoined)
	}
}

func TestBuildTUISnapshotMapsMetadataChatCapabilities(t *testing.T) {
	app := newTestAppWithSessionStore(t)
	providerWithMetadata := &stubSnapshotProvider{
		stubProvider: &stubProvider{},
		serviceConfig: &provider.ServiceConfig{
			FeatureFlags:      provider.ServiceFeatureFlags{TextToSpeech: true, SpeechToText: true, HumanAIDialogue: true},
			MaxFileSizeUpload: 100,
			DocExts:           []string{"pdf", "docx"},
			MilvusEnabled:     true,
		},
		metadata: &provider.Metadata{
			WelcomeMessage: "欢迎使用",
			SubHead:        "支持联网与知识库",
			Questions:      []string{"问题一", "问题二"},
			Capabilities: provider.ChatCapabilities{
				InternetSearch:         true,
				SupportKnowledge:       true,
				SupportMCPTools:        true,
				SupportHumanAIDialogue: true,
				DocumentUnderstanding:  true,
			},
		},
		knowledgeBases: &provider.KnowledgeBaseList{Items: []provider.KnowledgeBaseSummary{{Code: "kb-a", Name: "知识库A"}}},
	}
	snapshot, err := app.buildTUISnapshot(context.Background(), providerWithMetadata, internalconfig.Default(), "flow-1", backendStatus{Available: true})
	if err != nil {
		t.Fatalf("buildTUISnapshot failed: %v", err)
	}
	if snapshot.WelcomeMessage != "欢迎使用" || snapshot.SubHead != "支持联网与知识库" {
		t.Fatalf("expected metadata welcome fields to map, got %#v", snapshot)
	}
	if len(snapshot.Questions) != 2 {
		t.Fatalf("expected metadata questions to map, got %#v", snapshot.Questions)
	}
	if !snapshot.InternetSearchSupported || !snapshot.InternetSearchEnabled || !snapshot.InternetSearchDefault {
		t.Fatalf("expected internet search capability to map, got %#v", snapshot)
	}
	if !snapshot.KnowledgeSearchSupported || len(snapshot.KnowledgeBases) != 1 || snapshot.KnowledgeBases[0].Code != "kb-a" {
		t.Fatalf("expected knowledge capability to map, got %#v", snapshot)
	}
	if !snapshot.KnowledgeSystemEnabled || !snapshot.MCPToolsSupported || !snapshot.TextToSpeechSupported || !snapshot.SpeechToTextSupported || !snapshot.HumanAIDialogueSupported {
		t.Fatalf("expected service and metadata capabilities to map, got %#v", snapshot)
	}
	if snapshot.MaxFileSizeUploadMB != 100 || len(snapshot.SupportedDocumentExts) != 2 {
		t.Fatalf("expected upload limits and doc extensions to map, got %#v", snapshot)
	}
	if len(snapshot.FileCapabilities) != 1 || snapshot.FileCapabilities[0].Key != provider.FeatureDocumentUnderstanding {
		t.Fatalf("expected document file capability to map, got %#v", snapshot.FileCapabilities)
	}
}

func TestRenderTUIChatFilesStatusIncludesServiceConfig(t *testing.T) {
	snapshot := internaltui.Snapshot{
		MaxFileSizeUploadMB:   100,
		SupportedDocumentExts: []string{"pdf", "docx"},
		FileCapabilities:      []internaltui.FileCapability{{Key: provider.FeatureDocumentUnderstanding, Label: "文档理解", Enabled: true}},
	}
	status := renderTUIChatFilesStatus(snapshot)
	for _, expected := range []string{"文件输入: 当前未附带文件", "上传上限: 100 MB", ".pdf", ".docx", "文档理解"} {
		if !strings.Contains(status, expected) {
			t.Fatalf("expected files status to contain %q, got %q", expected, status)
		}
	}
}

func TestHumanAIDialogueRequiresServiceAndMetadataSupport(t *testing.T) {
	snapshot := internaltui.Snapshot{}
	applyServiceConfigToTUISnapshot(&snapshot, &provider.ServiceConfig{FeatureFlags: provider.ServiceFeatureFlags{HumanAIDialogue: true}})
	if snapshot.HumanAIDialogueSupported {
		t.Fatalf("expected service flag alone not to enable human AI dialogue, got %#v", snapshot)
	}

	snapshot = internaltui.Snapshot{}
	applyMetadataToTUISnapshot(&snapshot, &provider.Metadata{Capabilities: provider.ChatCapabilities{SupportHumanAIDialogue: true}})
	if snapshot.HumanAIDialogueSupported {
		t.Fatalf("expected metadata feature alone not to enable human AI dialogue, got %#v", snapshot)
	}

	applyServiceConfigToTUISnapshot(&snapshot, &provider.ServiceConfig{FeatureFlags: provider.ServiceFeatureFlags{HumanAIDialogue: true}})
	if !snapshot.HumanAIDialogueSupported {
		t.Fatalf("expected service flag and metadata feature to enable human AI dialogue, got %#v", snapshot)
	}
}

func TestExecuteTUIKnowledgeCommandSwitchesToAuto(t *testing.T) {
	snapshot := internaltui.Snapshot{
		KnowledgeSearchSupported: true,
		KnowledgeBases:           []internaltui.KnowledgeBaseItem{{Code: "kb-a", Name: "知识库A", Selected: true}},
		SelectedKnowledgeBases:   []string{"kb-a"},
	}
	result, err := executeTUIKnowledgeCommand("/knowledge auto", snapshot)
	if err != nil {
		t.Fatalf("executeTUIKnowledgeCommand failed: %v", err)
	}
	if result.Snapshot == nil || len(result.Snapshot.SelectedKnowledgeBases) != 0 {
		t.Fatalf("expected knowledge selection switched to auto, got %#v", result.Snapshot)
	}
	if result.Snapshot.KnowledgeBases[0].Selected {
		t.Fatalf("expected picker selection flag cleared, got %#v", result.Snapshot.KnowledgeBases)
	}
	if len(result.Events) != 1 || !strings.Contains(result.Events[0].Text, "auto（自动）") {
		t.Fatalf("expected auto status note, got %#v", result.Events)
	}
}

func TestExecuteTUIKnowledgeCommandOpensPickerForRootCommand(t *testing.T) {
	snapshot := internaltui.Snapshot{
		KnowledgeSearchSupported: true,
		KnowledgeBases:           []internaltui.KnowledgeBaseItem{{Code: "kb-a", Name: "知识库A"}},
	}
	result, err := executeTUIKnowledgeCommand("/knowledge", snapshot)
	if err != nil {
		t.Fatalf("executeTUIKnowledgeCommand failed: %v", err)
	}
	if !result.Handled || result.KnowledgePicker == nil {
		t.Fatalf("expected knowledge picker result, got %#v", result)
	}
	if result.KnowledgePicker.Title != "知识库范围(knowledge)" {
		t.Fatalf("unexpected picker title: %#v", result.KnowledgePicker)
	}
	if len(result.KnowledgePicker.Items) != 1 || result.KnowledgePicker.Items[0].Code != "kb-a" {
		t.Fatalf("unexpected picker items: %#v", result.KnowledgePicker.Items)
	}
	if len(result.Events) != 0 {
		t.Fatalf("expected no extra events when opening picker, got %#v", result.Events)
	}
}

func TestExecuteTUIInternetCommandTogglesState(t *testing.T) {
	snapshot := internaltui.Snapshot{InternetSearchSupported: true}
	result, err := executeTUIInternetCommand("/internet on", snapshot)
	if err != nil {
		t.Fatalf("executeTUIInternetCommand failed: %v", err)
	}
	if result.Snapshot == nil || !result.Snapshot.InternetSearchEnabled {
		t.Fatalf("expected internet search enabled, got %#v", result.Snapshot)
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

func TestBuildTUICompressionProgressTextReturnsPayloadText(t *testing.T) {
	text := buildTUICompressionProgressText(map[string]any{
		"text":          "上下文压缩完成：320 -> 160",
		"before_tokens": 320,
		"after_tokens":  160,
	})
	if text != "上下文压缩完成：0.3K -> 0.2K" {
		t.Fatalf("unexpected compression progress text: %q", text)
	}
}

func TestBuildTUICompressionProgressTextSkipsZeroTokenNoop(t *testing.T) {
	text := buildTUICompressionProgressText(map[string]any{
		"text":          "上下文压缩完成：0K -> 0K",
		"before_tokens": 0,
		"after_tokens":  0,
	})
	if text != "" {
		t.Fatalf("expected zero-token compression progress hidden, got %q", text)
	}
}

func TestBuildTUICompressionMetricsAcceptsCompressionEndPayload(t *testing.T) {
	metrics := buildTUICompressionMetrics(map[string]any{
		"count":             1,
		"before_tokens":     9200,
		"after_tokens":      6000,
		"remaining_tokens":  2192,
		"compression_ratio": 0.65,
		"target_type":       "history_messages",
	})
	if metrics == nil || metrics.Compression == nil {
		t.Fatalf("expected compression metrics, got %#v", metrics)
	}
	if metrics.Compression.AfterTokens != 6000 || metrics.Compression.TargetType != "history_messages" {
		t.Fatalf("unexpected compression metrics: %#v", metrics.Compression)
	}
}

func TestEmitTUIClientToolResultEventsSkipsPanelForSkill(t *testing.T) {
	events := []internaltui.Event{}
	emitTUIClientToolResultEvents(func(event internaltui.Event) {
		events = append(events, event)
	}, "skill", map[string]any{
		"name":   "Context Review",
		"prompt": "技能主体内容",
	}, nil)
	if len(events) != 1 {
		t.Fatalf("expected only one note for skill load, got %#v", events)
	}
	if events[0].Kind != internaltui.EventSystemNote || !strings.Contains(events[0].Text, "技能已装载：Context Review") {
		t.Fatalf("expected skill load note without panel, got %#v", events)
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
			HTTPTimeoutSeconds:       "75",
			CommandTimeoutSeconds:    "45",
			StreamIdleTimeoutSeconds: "300",
			StreamRetryAttempts:      "2",
			QRWaitTimeoutSeconds:     "90",
			AgentAutoLoopEnabled:     "false",
			OutputLanguage:           "zh-CN",
			SkillsInstallScope:       "workspace",
			SkillsAutoInstallPolicy:  "allow",
			SkillsFreeTextSearch:     false,
			SkillsRegistryURL:        "https://mirror-cn.clawhub.com",
		},
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
	if updated.Skills.AutoInstallPolicy != internalconfig.SkillAutoInstallPolicyAllow {
		t.Fatalf("expected skills auto install policy saved, got %#v", updated.Skills)
	}
	if updated.Skills.FreeTextSearchEnabled {
		t.Fatalf("expected free text search flag saved, got %#v", updated.Skills)
	}
	if updated.Skills.Directory != "" {
		t.Fatalf("expected legacy skills directory cleared, got %#v", updated.Skills)
	}
	if updated.CommandTimeoutSeconds != 45 {
		t.Fatalf("expected command timeout saved, got %#v", updated.CommandTimeoutSeconds)
	}
	if updated.HTTPTimeoutSeconds != 75 {
		t.Fatalf("expected http timeout saved, got %#v", updated.HTTPTimeoutSeconds)
	}
	if updated.QRWaitTimeoutSeconds != 90 {
		t.Fatalf("expected qr wait timeout saved, got %#v", updated.QRWaitTimeoutSeconds)
	}
	if updated.StreamRetryAttempts != 2 {
		t.Fatalf("expected stream retry attempts saved, got %#v", updated.StreamRetryAttempts)
	}
	if updated.AgentAutoLoopEnabled {
		t.Fatalf("expected agent auto loop switch saved as false, got %#v", updated.AgentAutoLoopEnabled)
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
	if reloaded.QRWaitTimeoutSeconds != 90 {
		t.Fatalf("expected persisted qr wait timeout, got %#v", reloaded.QRWaitTimeoutSeconds)
	}
	if reloaded.StreamRetryAttempts != 2 {
		t.Fatalf("expected persisted stream retry attempts, got %#v", reloaded.StreamRetryAttempts)
	}
	if reloaded.AgentAutoLoopEnabled {
		t.Fatalf("expected persisted auto loop switch false, got %#v", reloaded.AgentAutoLoopEnabled)
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
