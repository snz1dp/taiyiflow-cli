package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestValidateManifestValid 校验合法 manifest（对齐 examples/hello-plugin）通过。
func TestValidateManifestValid(t *testing.T) {
	manifest := map[string]any{
		"id":          "example.hello-plugin",
		"name":        "Hello 插件示例",
		"version":     "0.1.0",
		"devMode":     true,
		"permissions": []any{"ui.headerAction"},
		"contributions": map[string]any{
			"headerActions": []any{
				map[string]any{
					"id":     "hello-btn",
					"icon":   "waving_hand",
					"label":  "Hello",
					"action": map[string]any{"type": "server:route", "route": "/plugins/hello-plugin/invoke"},
				},
			},
		},
	}
	result := ValidateManifest(manifest)
	if !result.Valid {
		t.Fatalf("合法 manifest 校验失败: %+v", result.Errors)
	}
}

// TestValidateManifestMissingRequired 缺必填字段时报错且路径指向字段。
func TestValidateManifestMissingRequired(t *testing.T) {
	manifest := map[string]any{
		"name":    "缺 id 与 version 的插件",
		"version": "0.1.0",
	}
	result := ValidateManifest(manifest)
	if result.Valid {
		t.Fatal("缺少 id 字段应校验失败")
	}
	found := false
	for _, e := range result.Errors {
		if e.Path == "id" || e.Path == "(root)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("错误路径应指向 id 或 root，实际: %+v", result.Errors)
	}
}

// TestValidateManifestInvalidPermission 非法权限名报错（schema enum 拦截）。
func TestValidateManifestInvalidPermission(t *testing.T) {
	manifest := map[string]any{
		"id":         "com.acme.evil",
		"name":       "非法权限插件",
		"version":    "0.1.0",
		"permissions": []any{"ipc.fake-permission"},
	}
	result := ValidateManifest(manifest)
	if result.Valid {
		t.Fatal("非法权限名应校验失败（对应主进程 dev-load 拒绝路径）")
	}
}

// TestValidateManifestInvalidSemver 非法版本号报错。
func TestValidateManifestInvalidSemver(t *testing.T) {
	manifest := map[string]any{
		"id":      "com.acme.bad-version",
		"name":    "坏版本插件",
		"version": "v1.0",
	}
	result := ValidateManifest(manifest)
	if result.Valid {
		t.Fatal("非 semver 版本号应校验失败")
	}
}

// TestValidateManifestFileRoundtrip 文件级校验：合法/不存在/语法错误三路径。
func TestValidateManifestFileRoundtrip(t *testing.T) {
	dir := t.TempDir()

	// 1. 合法文件
	validPath := filepath.Join(dir, "plugin.json")
	validManifest := map[string]any{"id": "com.acme.file", "name": "文件校验插件", "version": "1.0.0"}
	content, _ := json.Marshal(validManifest)
	if err := os.WriteFile(validPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := ValidateManifestFile(validPath)
	if err != nil || !result.Valid {
		t.Fatalf("合法文件应通过: err=%v result=%+v", err, result)
	}

	// 2. 不存在
	if _, err := ValidateManifestFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("不存在的文件应返回错误")
	}

	// 3. JSON 语法错误
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badPath, []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateManifestFile(badPath); err == nil {
		t.Fatal("语法错误的文件应返回解析错误")
	}
}

// TestSchemaEmbeddedConsistency 嵌入 schema 与宿主共享文件一致性：
// 读取 embed 内容应与 taiyiflow-electron/schemas/plugin-manifest.schema.json 相同
//（Makefile 复制 + 本测试双保险，见 plugin-devkit-plan.md 4.1）。
// 注：跨仓库路径在 CI 中可能不可达，仅在本地存在时校验。
func TestSchemaEmbeddedConsistency(t *testing.T) {
	hostPath := filepath.Join("..", "..", "..", "taiyiflow-electron", "schemas", "plugin-manifest.schema.json")
	host, err := os.ReadFile(hostPath)
	if os.IsNotExist(err) {
		t.Skip("宿主仓库不可达（独立 CI 环境），跳过一致性校验")
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(host) != manifestSchemaJSON {
		t.Fatal("嵌入 schema 与宿主 schemas/plugin-manifest.schema.json 不一致：请执行 make sync-plugin-schema 或手动复制")
	}
}
