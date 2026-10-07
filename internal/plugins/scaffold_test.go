package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestScaffoldPluginRoundtrip 脚手架生成 → 立即 validate 通过（模板与 schema 一致性）。
func TestScaffoldPluginRoundtrip(t *testing.T) {
	dir := t.TempDir()
	result, err := ScaffoldPlugin(dir, "demo-plugin", "演示插件", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.PluginID != "com.acme.demo-plugin" {
		t.Fatalf("插件 id 应为 com.acme.demo-plugin，实际 %s", result.PluginID)
	}
	if result.PluginDir != filepath.Join(dir, "demo-plugin") {
		t.Fatalf("插件目录应为 %s，实际 %s", filepath.Join(dir, "demo-plugin"), result.PluginDir)
	}

	// 生成的 manifest 必须能通过共享 schema 校验（init → validate 链路契约）
	manifestPath := filepath.Join(result.PluginDir, "plugin.json")
	validateResult, err := ValidateManifestFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if !validateResult.Valid {
		t.Fatalf("脚手架生成的 manifest 校验失败: %+v", validateResult.Errors)
	}

	// 生成文件完整性
	for _, required := range []string{"plugin.json", "prompts/hello.json", "ui/icon.svg", "SKILL.md"} {
		if _, err := os.Stat(filepath.Join(result.PluginDir, required)); os.IsNotExist(err) {
			t.Fatalf("缺少模板文件: %s", required)
		}
	}
}

// TestScaffoldPluginNameNormalization 名称规范化：大写/下划线/空格 → 小写连字符。
func TestScaffoldPluginNameNormalization(t *testing.T) {
	cases := map[string]string{
		"Demo Plugin":  "demo-plugin",
		"DemoPlugin":   "demoplugin",
		"demo_plugin":  "demo-plugin",
		"  DEMO  ":     "demo",
		"--demo--":     "demo",
		"非法名称":        "",
	}
	for input, expected := range cases {
		if got := NormalizePluginName(input); got != expected {
			t.Errorf("NormalizePluginName(%q) = %q, 期望 %q", input, got, expected)
		}
	}
}

// TestScaffoldPluginNoOverwrite 同名目录已存在且未开启 overwrite 时报错。
func TestScaffoldPluginNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	if _, err := ScaffoldPlugin(dir, "dup-plugin", "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := ScaffoldPlugin(dir, "dup-plugin", "", false); err == nil {
		t.Fatal("同名目录已存在时应报错（未传 --overwrite）")
	}
	// 开启 overwrite 后成功
	if _, err := ScaffoldPlugin(dir, "dup-plugin", "", true); err != nil {
		t.Fatalf("overwrite 模式应成功: %v", err)
	}
}

// TestRegisterDevPluginRoundtrip plugin dev：写入 → 幂等 → 相对路径形式。
func TestRegisterDevPluginRoundtrip(t *testing.T) {
	workspace := t.TempDir()
	pluginDir := filepath.Join(workspace, "my-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 插件目录必须有 plugin.json（dev 命令前置校验）
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"id":"x","name":"x","version":"0.1.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. 首次写入
	list, err := RegisterDevPlugin(workspace, pluginDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0] != "./my-plugin" {
		t.Fatalf("首次写入应为 [./my-plugin]，实际 %v", list)
	}

	// 2. 幂等：重复注册不重复添加
	list, err = RegisterDevPlugin(workspace, pluginDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("重复注册应幂等，实际 %v", list)
	}

	// 3. 落盘内容校验
	content, err := os.ReadFile(filepath.Join(workspace, DevConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	var config DevConfig
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Plugins) != 1 || config.Plugins[0] != "./my-plugin" {
		t.Fatalf("落盘配置应为 [./my-plugin]，实际 %v", config.Plugins)
	}

	// 4. 第二个插件追加
	plugin2 := filepath.Join(workspace, "my-plugin-2")
	if err := os.MkdirAll(plugin2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin2, "plugin.json"), []byte(`{"id":"y","name":"y","version":"0.1.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err = RegisterDevPlugin(workspace, plugin2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("第二个插件应追加，实际 %v", list)
	}
}

// TestRegisterDevPluginMissingManifest 插件目录无 plugin.json 时报错。
func TestRegisterDevPluginMissingManifest(t *testing.T) {
	workspace := t.TempDir()
	emptyDir := filepath.Join(workspace, "no-manifest")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterDevPlugin(workspace, emptyDir); err == nil {
		t.Fatal("无 plugin.json 的目录应报错")
	}
}
