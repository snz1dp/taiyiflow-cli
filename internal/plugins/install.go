package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// 安装目录约定（与 taiyiflow-electron/src-electron/plugins/plugin-installer.js 一致）：
// - 根目录：SNZ1DP_HOME 环境变量（绝对路径），未设置时 ~/.snz1dp；
// - 插件安装子目录：{root}/plugins；
// - 安装记录：{root}/plugins/installed.json（pluginId → 安装元数据）。
// CLI 与 Electron 宿主共享同一目录：CLI 侧管理（list/inspect），宿主侧
// 消费（boot 时 plugin:list-installed 加载）。

// InstallRootDir 返回插件安装根目录（{SNZ1DP_HOME}/plugins，惰性创建）。
//
// SNZ1DP_HOME 解析复用 internal/config.DefaultRootDir（与技能目录、
// 更新缓存目录同根），保证 CLI 各子系统对根目录的理解一致。
func InstallRootDir() (string, error) {
	rootDir, err := config.DefaultRootDir()
	if err != nil {
		return "", fmt.Errorf("解析插件安装根目录失败: %w", err)
	}
	pluginsDir := filepath.Join(rootDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		return "", fmt.Errorf("创建插件安装目录失败: %w", err)
	}
	return pluginsDir, nil
}

// InstallRecordEntry 表示 installed.json 中单个插件的安装元数据
// （字段与宿主 plugin-installer.js 写入格式一致，CLI 只读消费）。
type InstallRecordEntry struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	DirName      string `json:"dirName"`
	Dir          string `json:"dir"`
	InstalledAt  string `json:"installedAt"`
	SHA256       string `json:"sha256"`
	Signed       bool   `json:"signed"`
	PublisherKey string `json:"publisherKey"`
}

// ReadInstallRecord 读取宿主写入的 installed.json（无文件时返回空表）。
// CLI 只读：不写回，保证安装记录的唯一写入方是 Electron 宿主 install 通道。
func ReadInstallRecord() (map[string]InstallRecordEntry, error) {
	pluginsDir, err := InstallRootDir()
	if err != nil {
		return nil, err
	}
	recordPath := filepath.Join(pluginsDir, "installed.json")
	content, err := os.ReadFile(recordPath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]InstallRecordEntry{}, nil
		}
		return nil, fmt.Errorf("读取 %s 失败: %w", recordPath, err)
	}
	var records map[string]InstallRecordEntry
	if err := json.Unmarshal(content, &records); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", recordPath, err)
	}
	if records == nil {
		records = map[string]InstallRecordEntry{}
	}
	return records, nil
}
