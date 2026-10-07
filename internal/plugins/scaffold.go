package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// readFileIfExists 读取文件；文件不存在时返回 (nil, nil)。
func readFileIfExists(path string) ([]byte, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}
	return os.ReadFile(path)
}

// ScaffoldResult 表示插件工程脚手架创建结果。
type ScaffoldResult struct {
	PluginID string // 插件 id（com.acme.<name> 形式）
	PluginDir string // 插件工程根目录（绝对路径）
	Files    []string // 生成的文件相对路径列表
}

// ScaffoldPlugin 在指定目录下创建插件工程骨架（DevKit 批次 2，plugin init 模板源）。
// 模板对齐 taiyiflow-electron/examples/hello-plugin（最小可用：一个 headerAction +
// mock 命中 toast 与插词），生成后可直接 plugin validate 通过并走 dev-load 链路。
//
// 参数：
//   - targetDir: 目标父目录（插件目录将创建为 <targetDir>/<name>）
//   - name: 插件短名（仅允许小写字母、数字、连字符；将转为 com.acme.<name> 形式 id）
//   - displayName: 人类可读名称（写入 manifest name 字段；为空时用 name）
//   - overwrite: 为 true 时同名目录已存在则覆盖；为 false 时报错
func ScaffoldPlugin(targetDir string, name string, displayName string, overwrite bool) (ScaffoldResult, error) {
	normalized := NormalizePluginName(name)
	if normalized == "" {
		return ScaffoldResult{}, fmt.Errorf("插件名称 %q 无法规范化（仅允许小写字母、数字、连字符）", name)
	}
	if displayName == "" {
		displayName = normalized
	}

	pluginDir := filepath.Join(targetDir, normalized)
	if _, err := os.Stat(pluginDir); err == nil && !overwrite {
		return ScaffoldResult{}, fmt.Errorf("插件目录已存在: %s（如需覆盖请加 --overwrite）", pluginDir)
	}

	absDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return ScaffoldResult{}, fmt.Errorf("解析插件目录路径失败: %w", err)
	}

	pluginID := fmt.Sprintf("com.acme.%s", normalized)
	files, err := writePluginTemplate(absDir, pluginID, displayName)
	if err != nil {
		return ScaffoldResult{}, err
	}

	return ScaffoldResult{
		PluginID:  pluginID,
		PluginDir: absDir,
		Files:     files,
	}, nil
}

// NormalizePluginName 把插件短名规范化为合法目录名与 id 后缀
//（小写、连字符分隔；复用 skills SlugifySkillName 的语义）。
func NormalizePluginName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-', r == '_', r == ' ':
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	result := strings.Trim(b.String(), "-")
	return result
}

// writePluginTemplate 写入插件工程模板文件，返回生成的相对路径列表。
func writePluginTemplate(pluginDir string, pluginID string, displayName string) ([]string, error) {
	dirs := []string{filepath.Join(pluginDir, "ui"), filepath.Join(pluginDir, "prompts")}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建目录 %s 失败: %w", dir, err)
		}
	}

	files := map[string]string{
		"plugin.json": pluginManifestTemplate(pluginID, displayName),
		"prompts/hello.json": `{
  "type": "user_prompt",
  "text": "请用一句话介绍 {{topic}} 插件",
  "arguments": ["topic"]
}
`,
		"ui/icon.svg": `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24" fill="currentColor">
  <path d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20zm0 18a8 8 0 1 1 0-16 8 8 0 0 1 0 16zm-1-5h2v2h-2v-2zm0-8h2v6h-2V7z"/>
</svg>
`,
		"SKILL.md": skillMdTemplate(pluginID, displayName),
	}

	var written []string
	for relPath, content := range files {
		absPath := filepath.Join(pluginDir, relPath)
		if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("写入 %s 失败: %w", absPath, err)
		}
		written = append(written, relPath)
	}
	return written, nil
}

// pluginManifestTemplate 生成最小可用 manifest（对齐 examples/hello-plugin）。
func pluginManifestTemplate(pluginID string, displayName string) string {
	return fmt.Sprintf(`{
  "id": %q,
  "name": %q,
  "description": "由 taiyiflow-cli plugin init 生成的插件工程（DevKit 批次 2 模板）",
  "version": "0.1.0",
  "author": "",
  "capabilities": ["desktop", "web"],
  "devMode": true,
  "devKit": {
    "mockResponses": {
      "/plugins/%s/invoke": [
        { "cmd": "toast", "message": "插件 mock 命中！", "type": "positive" },
        { "cmd": "insert_prompt", "text": "请用一句话介绍 %s 插件" }
      ]
    }
  },
  "permissions": ["ui.headerAction"],
  "contributions": {
    "headerActions": [
      {
        "id": "hello-btn",
        "icon": "waving_hand",
        "label": %q,
        "tooltip": "DevKit 模板插件（mock 模式，无网络请求）",
        "order": 90,
        "action": {
          "type": "server:route",
          "route": "/plugins/%s/invoke",
          "payload": { "mode": "demo" }
        }
      }
    ]
  }
}
`, pluginID, displayName, pluginID, displayName, displayName, pluginID)
}

// skillMdTemplate 生成插件包内可选的技能说明文件（插件 = 技能包超集）。
func skillMdTemplate(pluginID string, displayName string) string {
	return fmt.Sprintf(`---
name: %s
description: %s 插件的技能说明（插件 = 技能包超集，SKILL.md 可选）
---

# %s

本插件由 ` + "`taiyiflow-cli plugin init`" + ` 生成。

## 开发调试

1. ` + "`taiyiflow-cli plugin dev <插件目录>`" + ` 写入 .plugin-dev.json；
2. 宿主仓库执行 ` + "`quasar dev`" + `，dev 插件自动加载（DEV 角标）；
3. 点击标题栏按钮命中 mockResponses（无网络请求）；
4. 修改 plugin.json 保存后 watch 自动热重载。
`, displayName, displayName, displayName)
}
