package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AgentAppScaffoldResult 表示 AgentApp 工程脚手架创建结果（P6.3 agentapp init）。
type AgentAppScaffoldResult struct {
	AppName  string   // 应用 name（服务端唯一标识，name + owner_appid 复合唯一）
	AppDir   string   // 应用工程根目录（绝对路径）
	Manifest string   // agentapp.json 绝对路径
	Files    []string // 生成的文件相对路径列表
}

// AgentAppManifestFileName AgentApp 编排清单文件名（P6.3 约定）。
// 与 plugin.json 区分：AgentApp 是服务端实体（AgentAppManageCreateRequest），
// manifest.contributions.plugins 声明插件集合引用，服务端按名解析级联安装。
const AgentAppManifestFileName = "agentapp.json"

// ScaffoldAgentApp 在指定目录下创建 AgentApp 编排工程骨架（P6.3，agentapp init 模板源）。
//
// 产物对齐服务端 AgentAppManageCreateRequest（agent_app.py /manage 创建入口）：
//   - agentapp.json：可直接作为 POST /agents/agent-apps/manage 请求体基础
//     （manifest.contributions.plugins 支持字符串列表与 {name, version} 对象列表）；
//   - README.md：编排说明与后续发布步骤引导。
//
// 参数：
//   - targetDir: 目标父目录（应用目录将创建为 <targetDir>/<name>）
//   - name: 应用短名（规范化规则与插件一致：小写字母、数字、连字符）
//   - displayName: 人类可读名称（写入 display_name 字段；为空时用 name）
//   - overwrite: 为 true 时同名目录已存在则覆盖；为 false 时报错
func ScaffoldAgentApp(targetDir string, name string, displayName string, overwrite bool) (AgentAppScaffoldResult, error) {
	normalized := NormalizePluginName(name)
	if normalized == "" {
		return AgentAppScaffoldResult{}, fmt.Errorf("应用名称 %q 无法规范化（仅允许小写字母、数字、连字符）", name)
	}
	if displayName == "" {
		displayName = normalized
	}

	appDir := filepath.Join(targetDir, normalized)
	if _, err := os.Stat(appDir); err == nil && !overwrite {
		return AgentAppScaffoldResult{}, fmt.Errorf("应用目录已存在: %s（如需覆盖请加 --overwrite）", appDir)
	}

	absDir, err := filepath.Abs(appDir)
	if err != nil {
		return AgentAppScaffoldResult{}, fmt.Errorf("解析应用目录路径失败: %w", err)
	}

	files, err := writeAgentAppTemplate(absDir, normalized, displayName)
	if err != nil {
		return AgentAppScaffoldResult{}, err
	}

	return AgentAppScaffoldResult{
		AppName:  normalized,
		AppDir:   absDir,
		Manifest: filepath.Join(absDir, AgentAppManifestFileName),
		Files:    files,
	}, nil
}

// writeAgentAppTemplate 写入 AgentApp 工程模板文件，返回生成的相对路径列表。
func writeAgentAppTemplate(appDir string, appName string, displayName string) ([]string, error) {
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录 %s 失败: %w", appDir, err)
	}

	files := map[string]string{
		AgentAppManifestFileName: agentAppManifestTemplate(appName, displayName),
		"README.md":              agentAppReadmeTemplate(appName, displayName),
	}

	// map 遍历无序，按 key 排序保证输出稳定
	names := make([]string, 0, len(files))
	for relPath := range files {
		names = append(names, relPath)
	}
	sortStrings(names)

	var written []string
	for _, relPath := range names {
		absPath := filepath.Join(appDir, relPath)
		if err := os.WriteFile(absPath, []byte(files[relPath]), 0o644); err != nil {
			return nil, fmt.Errorf("写入 %s 失败: %w", absPath, err)
		}
		written = append(written, relPath)
	}
	return written, nil
}

// sortStrings 简单字符串排序（避免引入 sort 包到本文件的额外依赖管理成本）。
func sortStrings(items []string) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j] < items[j-1]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// agentAppManifestTemplate 生成 AgentApp 编排清单模板。
//
// 字段对齐服务端 AgentAppManageCreateRequest：
//   - name: 应用标识名（name + owner_appid 复合唯一，创建后不可改）
//   - display_name / description / icon / version: 展示与版本元信息
//   - manifest.contributions.plugins: 插件集合引用（字符串或 {name, version} 对象）
//   - access_scope: public / org / private
//
// manifest 内引用示例同时演示两种声明形式，实际使用时二选一。
func agentAppManifestTemplate(appName string, displayName string) string {
	manifest := map[string]any{
		"name":         appName,
		"display_name": displayName,
		"description":  fmt.Sprintf("由 taiyiflow-cli agentapp init 生成的智能体应用编排（P6.3 模板）"),
		"version":      "0.1.0",
		"manifest": map[string]any{
			"contributions": map[string]any{
				"plugins": []any{
					// 形式 1：字符串引用（仅插件名）
					"com.acme.example-plugin",
					// 形式 2：对象引用（name 必填，version 仅作安装提示不参与匹配）
					map[string]any{
						"name":    "com.acme.another-plugin",
						"version": "1.0.0",
					},
				},
			},
			// mcpPolicies / promptTemplates / agentTools 为可选编排声明，
			// 结构见服务端 AgentAppRead manifest 注释（schemas.py 592-597 行）
		},
		"access_scope": "public",
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		// map[string]any 序列化不会失败，防御性兜底
		return "{}\n"
	}
	return string(encoded) + "\n"
}

// agentAppReadmeTemplate 生成应用编排说明文档。
func agentAppReadmeTemplate(appName string, displayName string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# %s\n\n", displayName))
	sb.WriteString(fmt.Sprintf("智能体应用 `%s` 的编排工程，由 `taiyiflow-cli agentapp init` 生成。\n\n", appName))
	sb.WriteString("## 结构\n\n")
	sb.WriteString("- `agentapp.json`：编排清单（可直接作为服务端创建请求体基础）\n\n")
	sb.WriteString("## 编排声明\n\n")
	sb.WriteString("AgentApp = 插件集合 + Prompt 模板 + Skills + MCP 策略的编排声明：\n\n")
	sb.WriteString("- `manifest.contributions.plugins`：插件集合引用（字符串或 `{name, version}` 对象）\n")
	sb.WriteString("- `manifest.mcpPolicies`：MCP 工具启停策略（enable/disable 白名单）\n")
	sb.WriteString("- `manifest.promptTemplates`：提示词模板声明（含 skill_name）\n")
	sb.WriteString("- `manifest.agentTools`：智能体工具声明（含 skill_name）\n\n")
	sb.WriteString("## 后续步骤\n\n")
	sb.WriteString("1. 编辑 `agentapp.json`，替换 `contributions.plugins` 中的示例引用为实际插件名；\n")
	sb.WriteString("2. 通过服务端管理端创建应用（POST /agents/agent-apps/manage，请求体基于本清单）；\n")
	sb.WriteString("3. 客户端应用切换时自动级联安装引用的插件（悬空引用仅告警不阻断）；\n")
	sb.WriteString("4. 用 `taiyiflow-cli agentapp plugins <应用名>` 核对插件引用完整性。\n")
	return sb.String()
}
