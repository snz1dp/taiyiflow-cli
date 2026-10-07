package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/plugins"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

// writeTestInstallRecord 将模拟的 installed.json 写入插件安装目录（复用 plugins.InstallRootDir）。
func writeTestInstallRecord(t *testing.T, content []byte) error {
	t.Helper()
	pluginsDir, err := plugins.InstallRootDir()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(pluginsDir, "installed.json"), content, 0o644)
}

// newAgentAppTestApp 构造带临时根目录与内存输出的测试 App。
func newAgentAppTestApp(t *testing.T) (*App, *bytes.Buffer) {
	t.Helper()
	t.Setenv(config.RootDirEnvName(), t.TempDir())
	manager, err := config.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	stdout := &bytes.Buffer{}
	app := &App{stdout: stdout, stderr: &bytes.Buffer{}, configManager: manager}
	return app, stdout
}

// TestAgentAppUsageNoArgs 验证无参数时输出用法帮助且不报错。
func TestAgentAppUsageNoArgs(t *testing.T) {
	app, stdout := newAgentAppTestApp(t)
	if err := app.runAgentApp(context.Background(), nil); err != nil {
		t.Fatalf("runAgentApp(nil) failed: %v", err)
	}
	output := stdout.String()
	for _, keyword := range []string{"用法:", "init", "list", "plugins", "installations", "list-installed", "help"} {
		if !strings.Contains(output, keyword) {
			t.Fatalf("expected usage to contain %q, got:\n%s", keyword, output)
		}
	}
}

// TestAgentAppInitScaffold 验证 init 生成 agentapp.json + README.md 且字段对齐服务端请求体。
func TestAgentAppInitScaffold(t *testing.T) {
	app, stdout := newAgentAppTestApp(t)
	dir := t.TempDir()
	if err := app.runAgentApp(context.Background(), []string{"init", "code-review-assistant", "--dir", dir, "--display-name", "代码审查助手"}); err != nil {
		t.Fatalf("agentapp init failed: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "智能体应用工程已创建") || !strings.Contains(output, "code-review-assistant") {
		t.Fatalf("unexpected init output:\n%s", output)
	}

	// manifest 字段对齐 AgentAppManageCreateRequest
	manifestPath := filepath.Join(dir, "code-review-assistant", "agentapp.json")
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest failed: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("unmarshal manifest failed: %v", err)
	}
	if manifest["name"] != "code-review-assistant" {
		t.Fatalf("expected name=code-review-assistant, got %v", manifest["name"])
	}
	if manifest["display_name"] != "代码审查助手" {
		t.Fatalf("expected display_name=代码审查助手, got %v", manifest["display_name"])
	}
	if manifest["version"] != "0.1.0" {
		t.Fatalf("expected version=0.1.0, got %v", manifest["version"])
	}
	if manifest["access_scope"] != "public" {
		t.Fatalf("expected access_scope=public, got %v", manifest["access_scope"])
	}
	// contributions.plugins 必须存在且为非空数组（模板含两种引用形式示例）
	inner, _ := manifest["manifest"].(map[string]any)
	if inner == nil {
		t.Fatal("expected manifest.manifest object")
	}
	contributions, _ := inner["contributions"].(map[string]any)
	if contributions == nil {
		t.Fatal("expected manifest.manifest.contributions object")
	}
	pluginsRef, ok := contributions["plugins"].([]any)
	if !ok || len(pluginsRef) != 2 {
		t.Fatalf("expected contributions.plugins with 2 example refs, got %v", contributions["plugins"])
	}

	// README 存在
	if _, err := os.Stat(filepath.Join(dir, "code-review-assistant", "README.md")); os.IsNotExist(err) {
		t.Fatal("missing README.md")
	}
}

// TestAgentAppInitNoOverwrite 验证同名目录已存在且未传 --overwrite 时报错。
func TestAgentAppInitNoOverwrite(t *testing.T) {
	app, _ := newAgentAppTestApp(t)
	dir := t.TempDir()
	if err := app.runAgentApp(context.Background(), []string{"init", "dup-app", "--dir", dir}); err != nil {
		t.Fatalf("first init failed: %v", err)
	}
	if err := app.runAgentApp(context.Background(), []string{"init", "dup-app", "--dir", dir}); err == nil {
		t.Fatal("expected error for existing dir without --overwrite")
	}
	// overwrite 后成功
	if err := app.runAgentApp(context.Background(), []string{"init", "dup-app", "--dir", dir, "--overwrite"}); err != nil {
		t.Fatalf("overwrite init failed: %v", err)
	}
}

// TestAgentAppInitMissingName 验证 init 缺少名称时报用法错误。
func TestAgentAppInitMissingName(t *testing.T) {
	app, _ := newAgentAppTestApp(t)
	err := app.runAgentApp(context.Background(), []string{"init"})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "用法") {
		t.Fatalf("expected usage error, got: %v", err)
	}
}

// TestAgentAppUnknownSubcommand 验证未知子命令报错并提示 help。
func TestAgentAppUnknownSubcommand(t *testing.T) {
	app, _ := newAgentAppTestApp(t)
	err := app.runAgentApp(context.Background(), []string{"bogus"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "agentapp help") {
		t.Fatalf("expected error to mention help, got: %v", err)
	}
}

// TestAgentAppListInstalledEmpty 验证本地无插件时输出空态提示。
func TestAgentAppListInstalledEmpty(t *testing.T) {
	app, stdout := newAgentAppTestApp(t)
	if err := app.runAgentApp(context.Background(), []string{"list-installed"}); err != nil {
		t.Fatalf("list-installed failed: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "暂无已安装插件") {
		t.Fatalf("expected empty hint, got:\n%s", output)
	}
}

// TestAgentAppListInstalledRecords 验证读取宿主写入的 installed.json 并按名排序输出。
func TestAgentAppListInstalledRecords(t *testing.T) {
	app, stdout := newAgentAppTestApp(t)
	// 模拟 Electron 宿主写入的安装记录（字段与 plugin-installer.js 格式一致）
	records := map[string]map[string]any{
		"zeta-tool": {
			"id": "zeta-tool", "version": "1.2.0", "dirName": "zeta-tool",
			"installedAt": "2026-10-01T10:00:00Z", "sha256": "abc", "signed": true,
		},
		"alpha-tool": {
			"id": "alpha-tool", "version": "0.9.0", "dirName": "alpha-tool",
			"installedAt": "2026-10-02T10:00:00Z", "sha256": "def", "signed": false,
		},
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal records failed: %v", err)
	}
	if writeErr := writeTestInstallRecord(t, encoded); writeErr != nil {
		t.Fatalf("write install record failed: %v", writeErr)
	}

	if runErr := app.runAgentApp(context.Background(), []string{"list-installed"}); runErr != nil {
		t.Fatalf("list-installed failed: %v", runErr)
	}
	output := stdout.String()
	// 排序断言：alpha-tool 应排在 zeta-tool 之前
	alphaIndex := strings.Index(output, "alpha-tool@0.9.0")
	zetaIndex := strings.Index(output, "zeta-tool@1.2.0")
	if alphaIndex < 0 || zetaIndex < 0 {
		t.Fatalf("expected both plugins in output, got:\n%s", output)
	}
	if alphaIndex > zetaIndex {
		t.Fatalf("expected alphabetical order, got:\n%s", output)
	}
	if !strings.Contains(output, "已签名") || !strings.Contains(output, "未签名") {
		t.Fatalf("expected signed/unsigned marks, got:\n%s", output)
	}
}

// TestAgentAppListInstalledRejectsArgs 验证 list-installed 拒绝多余参数。
func TestAgentAppListInstalledRejectsArgs(t *testing.T) {
	app, _ := newAgentAppTestApp(t)
	err := app.runAgentApp(context.Background(), []string{"list-installed", "extra"})
	if err == nil {
		t.Fatal("expected error for extra args")
	}
}

// TestAgentAppPluginsMissingArg 验证 plugins 缺少应用标识时报用法错误。
func TestAgentAppPluginsMissingArg(t *testing.T) {
	app, _ := newAgentAppTestApp(t)
	err := app.runAgentApp(context.Background(), []string{"plugins"})
	if err == nil {
		t.Fatal("expected error for missing app id")
	}
}

// TestAgentAppInstallationsBadStatus 验证 installations 非法状态过滤报错。
func TestAgentAppInstallationsBadStatus(t *testing.T) {
	app, _ := newAgentAppTestApp(t)
	err := app.runAgentApp(context.Background(), []string{"installations", "bogus"})
	if err == nil {
		t.Fatal("expected error for invalid status filter")
	}
}

// TestAgentAppListRemote 验证 list 子命令解析服务端响应并渲染应用列表。
func TestAgentAppListRemote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/taiyi/composer/api/v1/agents/agent-apps" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"11111111-1111-1111-1111-111111111111","name":"code-review","display_name":"代码审查助手","description":"审查 PR","version":"2.0.0","manifest":{},"access_scope":"public","updated_at":"2026-10-01T00:00:00Z"}]`)
	}))
	defer server.Close()

	client := providerclient.NewClient(server.URL, "test-token")
	// 直接调用 provider 层验证解析（绕过认证流程，认证依赖真实后端）
	apps, err := client.ListAgentApps(context.Background(), false, 0, 100)
	if err != nil {
		t.Fatalf("ListAgentApps failed: %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(apps))
	}
	if apps[0].Name != "code-review" || apps[0].Version != "2.0.0" {
		t.Fatalf("unexpected app: %+v", apps[0])
	}
}

// TestAgentAppPluginsRemote 验证 plugins 列表解析（含 artifact 与 warnings）。
func TestAgentAppPluginsRemote(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/plugins") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"22222222-2222-2222-2222-222222222222","name":"review-ui","version":"1.0.0","artifact_uri":"https://example.com/review-ui-1.0.0.zip","artifact_hash":"deadbeef","enabled":true},{"id":"33333333-3333-3333-3333-333333333333","name":"dangling-ref","version":"0.1.0","enabled":false}],"total":2,"offset":0,"limit":100,"warnings":["引用的插件 missing-plugin 不存在"]}`)
	}))
	defer server.Close()

	client := providerclient.NewClient(server.URL, "test-token")
	page, err := client.ListAgentAppPlugins(context.Background(), "code-review", 0, 100)
	if err != nil {
		t.Fatalf("ListAgentAppPlugins failed: %v", err)
	}
	if page.Total != 2 || len(page.Data) != 2 {
		t.Fatalf("expected 2 plugins, got %+v", page)
	}
	if page.Data[0].ArtifactURI == "" || page.Data[0].ArtifactHash != "deadbeef" {
		t.Fatalf("unexpected artifact info: %+v", page.Data[0])
	}
	if len(page.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %+v", page.Warnings)
	}
}

// TestAgentAppInstallRemote 验证 install 请求体与响应解析。
func TestAgentAppInstallRemote(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/install") {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"44444444-4444-4444-4444-444444444444","agent_app_id":"11111111-1111-1111-1111-111111111111","app_name":"code-review","app_version":"2.0.0","status":"installed","enabled":true,"installed_at":"2026-10-07T12:00:00Z"}`)
	}))
	defer server.Close()

	client := providerclient.NewClient(server.URL, "test-token")
	record, err := client.InstallAgentApp(context.Background(), providerAgentAppInstallRequest())
	if err != nil {
		t.Fatalf("InstallAgentApp failed: %v", err)
	}
	if record.Status != "installed" || record.AppName != "code-review" {
		t.Fatalf("unexpected record: %+v", record)
	}
	if capturedBody["agent_app_id"] != "code-review" {
		t.Fatalf("expected agent_app_id in request body, got: %v", capturedBody)
	}
}

// providerAgentAppInstallRequest 构造测试用安装请求。
func providerAgentAppInstallRequest() provider.AgentAppInstallRequest {
	return provider.AgentAppInstallRequest{AgentAppID: "code-review", ClientPlatform: "cli"}
}
