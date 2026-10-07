package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DevConfigFileName 宿主 dev 通道读取的插件目录声明文件名（与
// taiyiflow-electron/src-electron/plugins/ipc-plugin-handlers.js 常量一致）。
const DevConfigFileName = ".plugin-dev.json"

// DevConfig 表示 .plugin-dev.json 的结构。
type DevConfig struct {
	Plugins []string `json:"plugins"`
}

// RegisterDevPlugin 把插件目录写入 .plugin-dev.json 的 plugins 数组
//（DevKit 批次 2，plugin dev 命令核心实现）。
//
// 参数：
//   - workspaceDir: 宿主仓库根目录（.plugin-dev.json 所在目录，即 quasar dev 启动目录）
//   - pluginDir: 插件目录（绝对路径或相对 workspaceDir 的路径；写入时转为相对路径）
//
// 幂等：已在列表中的目录不重复添加。返回写入后的完整插件目录列表。
func RegisterDevPlugin(workspaceDir string, pluginDir string) ([]string, error) {
	if pluginDir == "" {
		return nil, fmt.Errorf("插件目录不能为空")
	}
	absWorkspace, err := filepath.Abs(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("解析工作区目录失败: %w", err)
	}

	// 校验插件目录存在且含 plugin.json
	absPlugin, err := filepath.Abs(pluginDir)
	if err != nil {
		return nil, fmt.Errorf("解析插件目录失败: %w", err)
	}
	manifestPath := filepath.Join(absPlugin, "plugin.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("插件清单不存在: %s（请先在插件目录创建 plugin.json）", manifestPath)
	}

	// 读取现有配置（不存在时视为空配置）
	configPath := filepath.Join(absWorkspace, DevConfigFileName)
	config := DevConfig{Plugins: []string{}}
	if content, err := os.ReadFile(configPath); err == nil {
		if err := json.Unmarshal(content, &config); err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", configPath, err)
		}
		if config.Plugins == nil {
			config.Plugins = []string{}
		}
	}

	// 转为相对 workspace 的路径（与宿主 dev 通道语义一致：相对 process.cwd()）
	relPlugin, err := filepath.Rel(absWorkspace, absPlugin)
	if err != nil {
		return nil, fmt.Errorf("计算相对路径失败: %w", err)
	}
	// 统一 ./ 前缀（对齐 examples 默认配置风格）
	entry := "./" + filepath.ToSlash(relPlugin)

	// 幂等去重（按清理后的路径比较）
	for i, existing := range config.Plugins {
		if normalizeDevEntry(existing, absWorkspace) == entry {
			// 已存在：统一为规范化形式
			config.Plugins[i] = entry
			return writeDevConfig(configPath, config)
		}
	}
	config.Plugins = append(config.Plugins, entry)
	return writeDevConfig(configPath, config)
}

// writeDevConfig 落盘 .plugin-dev.json。
func writeDevConfig(configPath string, config DevConfig) ([]string, error) {
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("序列化 %s 失败: %w", configPath, err)
	}
	// MarshalIndent 不带尾换行，补一个保持文件风格
	content = append(content, '\n')
	if err := os.WriteFile(configPath, content, 0o644); err != nil {
		return nil, fmt.Errorf("写入 %s 失败: %w", configPath, err)
	}
	return config.Plugins, nil
}

// normalizeDevEntry 把已有声明条目规范化为 ./相对路径形式（幂等比较用）。
func normalizeDevEntry(entry string, absWorkspace string) string {
	if entry == "" {
		return ""
	}
	// 绝对路径转相对
	if filepath.IsAbs(entry) {
		if rel, err := filepath.Rel(absWorkspace, entry); err == nil {
			return "./" + filepath.ToSlash(rel)
		}
	}
	// 统一 ./ 前缀与斜杠
	s := filepath.ToSlash(entry)
	if !filepath.IsAbs(s) && len(s) > 0 && s[0] != '.' {
		s = "./" + s
	}
	return s
}
