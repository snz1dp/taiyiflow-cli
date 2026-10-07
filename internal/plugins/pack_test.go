package plugins

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTestPluginDir 在临时目录创建一个最小可打包插件工程，返回插件目录路径。
// extraFiles 为附加文件映射（相对插件目录路径 → 内容），用于验证附属文件入包。
func writeTestPluginDir(t *testing.T, manifestContent string, extraFiles map[string]string) string {
	t.Helper()
	baseDir := t.TempDir()
	pluginDir := filepath.Join(baseDir, "hello-plugin")
	if err := os.MkdirAll(filepath.Join(pluginDir, "ui"), 0o755); err != nil {
		t.Fatalf("创建插件目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifestContent), 0o644); err != nil {
		t.Fatalf("写入 plugin.json 失败: %v", err)
	}
	for relPath, content := range extraFiles {
		absPath := filepath.Join(pluginDir, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			t.Fatalf("创建目录 %s 失败: %v", absPath, err)
		}
		if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
			t.Fatalf("写入 %s 失败: %v", absPath, err)
		}
	}
	return pluginDir
}

// validTestManifest 返回 schema 校验可通过的最小 manifest。
func validTestManifest() string {
	return `{
  "id": "com.acme.hello-plugin",
  "name": "Hello Plugin",
  "description": "测试插件",
  "version": "1.0.0",
  "permissions": ["ui.headerAction"],
  "contributions": {
    "headerActions": [
      {
        "id": "hello-btn",
        "icon": "waving_hand",
        "label": "Hello",
        "action": {
          "type": "server:route",
          "route": "/plugins/hello/invoke",
          "payload": {}
        }
      }
    ]
  }
}`
}

// TestPackPluginSuccess 验证正常打包：产物命名、hash 正确、zip 内容完整。
func TestPackPluginSuccess(t *testing.T) {
	pluginDir := writeTestPluginDir(t, validTestManifest(), map[string]string{
		"ui/icon.svg": "<svg/>",
		"prompts/hello.json": `{"type":"user_prompt"}`,
	})

	result, err := PackPlugin(pluginDir, PackOptions{})
	if err != nil {
		t.Fatalf("PackPlugin 失败: %v", err)
	}

	// 产物命名 {id}-{version}.zip
	if result.PluginID != "com.acme.hello-plugin" {
		t.Errorf("PluginID = %q, 期望 com.acme.hello-plugin", result.PluginID)
	}
	if result.Version != "1.0.0" {
		t.Errorf("Version = %q, 期望 1.0.0", result.Version)
	}
	if filepath.Base(result.ZipPath) != "com.acme.hello-plugin-1.0.0.zip" {
		t.Errorf("ZipPath 文件名 = %q, 期望 com.acme.hello-plugin-1.0.0.zip", filepath.Base(result.ZipPath))
	}

	// zip 落盘且 hash 与解包重算一致
	zipBytes, err := os.ReadFile(result.ZipPath)
	if err != nil {
		t.Fatalf("读取 zip 产物失败: %v", err)
	}
	sum := sha256.Sum256(zipBytes)
	if result.Hash != hex.EncodeToString(sum[:]) {
		t.Errorf("hash 不一致: result=%s 重算=%s", result.Hash, hex.EncodeToString(sum[:]))
	}

	// 旁置 hash 文件内容格式「hex  文件名」
	hashContent, err := os.ReadFile(result.HashPath)
	if err != nil {
		t.Fatalf("读取 hash 文件失败: %v", err)
	}
	expectedHashLine := result.Hash + "  " + filepath.Base(result.ZipPath)
	if strings.TrimSpace(string(hashContent)) != expectedHashLine {
		t.Errorf("hash 文件内容 = %q, 期望 %q", strings.TrimSpace(string(hashContent)), expectedHashLine)
	}

	// zip 内容：顶层目录 + 全部文件（3 个源文件）
	if result.FileCount != 3 {
		t.Errorf("FileCount = %d, 期望 3", result.FileCount)
	}
	zipReader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("解析 zip 失败: %v", err)
	}
	gotFiles := map[string]bool{}
	for _, f := range zipReader.File {
		gotFiles[f.Name] = true
	}
	for _, want := range []string{
		"hello-plugin/plugin.json",
		"hello-plugin/ui/icon.svg",
		"hello-plugin/prompts/hello.json",
	} {
		if !gotFiles[want] {
			t.Errorf("zip 缺少条目 %s（实际: %v）", want, gotFiles)
		}
	}
}

// TestPackPluginRejectsInvalidManifest 验证 validate 前置：manifest 非法时拒绝打包。
func TestPackPluginRejectsInvalidManifest(t *testing.T) {
	invalidManifest := strings.Replace(validTestManifest(), `"ui.headerAction"`, `"not.a.permission"`, 1)
	pluginDir := writeTestPluginDir(t, invalidManifest, nil)

	_, err := PackPlugin(pluginDir, PackOptions{})
	if err == nil {
		t.Fatal("manifest 非法时 PackPlugin 应返回错误，实际返回 nil")
	}
	if !strings.Contains(err.Error(), "拒绝打包") {
		t.Errorf("错误信息应包含「拒绝打包」, 实际: %v", err)
	}

	// 不应产出任何文件
	parent := filepath.Dir(pluginDir)
	entries, readErr := os.ReadDir(parent)
	if readErr != nil {
		t.Fatalf("读取父目录失败: %v", readErr)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".zip") || strings.HasSuffix(e.Name(), ".sha256") {
			t.Errorf("校验失败不应产出产物文件: %s", e.Name())
		}
	}
}

// TestPackPluginSkipsHiddenFiles 验证隐藏文件不入包（.DS_Store 等）。
func TestPackPluginSkipsHiddenFiles(t *testing.T) {
	pluginDir := writeTestPluginDir(t, validTestManifest(), map[string]string{
		".DS_Store":          "junk",
		".plugin-dev.json":   `{"plugins":["./x"]}`,
		"ui/.DS_Store":       "junk",
		"ui/icon.svg":        "<svg/>",
	})

	result, err := PackPlugin(pluginDir, PackOptions{})
	if err != nil {
		t.Fatalf("PackPlugin 失败: %v", err)
	}
	if result.FileCount != 2 {
		t.Errorf("FileCount = %d, 期望 2（plugin.json + ui/icon.svg，隐藏文件跳过）", result.FileCount)
	}

	zipBytes, err := os.ReadFile(result.ZipPath)
	if err != nil {
		t.Fatalf("读取 zip 产物失败: %v", err)
	}
	zipReader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("解析 zip 失败: %v", err)
	}
	for _, f := range zipReader.File {
		if strings.Contains(f.Name, ".DS_Store") || strings.Contains(f.Name, ".plugin-dev") {
			t.Errorf("zip 不应包含隐藏文件: %s", f.Name)
		}
	}
}

// TestPackPluginDeterministicHash 验证同内容两次打包 hash 一致（确定性输出）。
func TestPackPluginDeterministicHash(t *testing.T) {
	pluginDir := writeTestPluginDir(t, validTestManifest(), map[string]string{
		"ui/icon.svg": "<svg/>",
	})

	first, err := PackPlugin(pluginDir, PackOptions{})
	if err != nil {
		t.Fatalf("第一次 PackPlugin 失败: %v", err)
	}
	second, err := PackPlugin(pluginDir, PackOptions{})
	if err != nil {
		t.Fatalf("第二次 PackPlugin 失败: %v", err)
	}
	if first.Hash != second.Hash {
		t.Errorf("同内容两次打包 hash 不一致: %s != %s", first.Hash, second.Hash)
	}
}

// TestPackPluginCustomOutputDir 验证 --out 自定义输出目录。
func TestPackPluginCustomOutputDir(t *testing.T) {
	pluginDir := writeTestPluginDir(t, validTestManifest(), nil)
	outDir := filepath.Join(t.TempDir(), "dist")

	result, err := PackPlugin(pluginDir, PackOptions{OutputDir: outDir})
	if err != nil {
		t.Fatalf("PackPlugin 失败: %v", err)
	}
	if filepath.Dir(result.ZipPath) != outDir {
		t.Errorf("产物目录 = %s, 期望 %s", filepath.Dir(result.ZipPath), outDir)
	}
	if _, err := os.Stat(result.ZipPath); err != nil {
		t.Errorf("产物文件不存在: %v", err)
	}
}

// TestPackPluginMissingManifest 验证 plugin.json 缺失时报错。
func TestPackPluginMissingManifest(t *testing.T) {
	pluginDir := t.TempDir()
	if _, err := PackPlugin(pluginDir, PackOptions{}); err == nil {
		t.Fatal("plugin.json 缺失时 PackPlugin 应返回错误")
	}
}
