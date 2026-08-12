package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// MCPConfigFileRel 是 MCP 配置文件相对于根目录的路径。
// 与 taiyiflow-chatui Electron 桌面端共享同一文件，路径为 ~/.snz1dp/config/mcp.json。
const MCPConfigFileRel = "config/mcp.json"

// MCPConfig 表示 MCP 本地禁用配置。
// 与 taiyiflow-chatui 的 mcp.json 格式完全一致：
//
//	{
//	  "disabled_server_ids": ["server_id_1", "server_id_2"],
//	  "disabled_tool_keys": ["serverId:toolName", "serverId2:toolName2"]
//	}
type MCPConfig struct {
	DisabledServerIDs []string `json:"disabled_server_ids"`
	DisabledToolKeys  []string `json:"disabled_tool_keys"`
}

// ConfigPath 返回 MCP 配置文件的绝对路径。
// 路径解析规则与 internal/config 一致：优先使用 SNZ1DP_HOME 环境变量，
// 否则回退到 ~/.snz1dp。
func ConfigPath() (string, error) {
	rootDir, err := internalconfig.DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(rootDir, MCPConfigFileRel), nil
}

// Load 读取 MCP 配置文件并返回解析后的配置。
// 若文件不存在或内容为空，返回零值配置（空数组），不视为错误。
func Load() (*MCPConfig, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &MCPConfig{
				DisabledServerIDs: []string{},
				DisabledToolKeys:  []string{},
			}, nil
		}
		return nil, fmt.Errorf("读取 MCP 配置文件失败: %w", err)
	}

	config := &MCPConfig{
		DisabledServerIDs: []string{},
		DisabledToolKeys:  []string{},
	}
	if len(data) == 0 {
		return config, nil
	}
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("解析 MCP 配置文件失败 (%s): %w", path, err)
	}
	if config.DisabledServerIDs == nil {
		config.DisabledServerIDs = []string{}
	}
	if config.DisabledToolKeys == nil {
		config.DisabledToolKeys = []string{}
	}
	return config, nil
}

// Save 将 MCP 配置写入配置文件。
// 写入前会确保父目录存在，与 chatui 保持一致的合并式写入语义：
// 调用方应先 Load 再修改目标字段后 Save，避免覆盖其他字段。
func Save(config *MCPConfig) error {
	path, err := ConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建 MCP 配置目录失败: %w", err)
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 MCP 配置失败: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("写入 MCP 配置文件失败 (%s): %w", path, err)
	}
	return nil
}

// AddDisabledServer 将指定服务器 ID 加入禁用列表（幂等）。
func AddDisabledServer(serverID string) error {
	config, err := Load()
	if err != nil {
		return err
	}
	for _, id := range config.DisabledServerIDs {
		if id == serverID {
			return nil // 已存在，无需重复添加
		}
	}
	config.DisabledServerIDs = append(config.DisabledServerIDs, serverID)
	return Save(config)
}

// RemoveDisabledServer 将指定服务器 ID 从禁用列表移除（幂等）。
func RemoveDisabledServer(serverID string) error {
	config, err := Load()
	if err != nil {
		return err
	}
	filtered := make([]string, 0, len(config.DisabledServerIDs))
	for _, id := range config.DisabledServerIDs {
		if id != serverID {
			filtered = append(filtered, id)
		}
	}
	config.DisabledServerIDs = filtered
	return Save(config)
}

// AddDisabledTool 将指定工具 key（格式 "serverId:toolName"）加入禁用列表（幂等）。
func AddDisabledTool(toolKey string) error {
	config, err := Load()
	if err != nil {
		return err
	}
	for _, key := range config.DisabledToolKeys {
		if key == toolKey {
			return nil // 已存在
		}
	}
	config.DisabledToolKeys = append(config.DisabledToolKeys, toolKey)
	return Save(config)
}

// RemoveDisabledTool 将指定工具 key 从禁用列表移除（幂等）。
func RemoveDisabledTool(toolKey string) error {
	config, err := Load()
	if err != nil {
		return err
	}
	filtered := make([]string, 0, len(config.DisabledToolKeys))
	for _, key := range config.DisabledToolKeys {
		if key != toolKey {
			filtered = append(filtered, key)
		}
	}
	config.DisabledToolKeys = filtered
	return Save(config)
}

// IsServerDisabled 判断指定服务器 ID 是否在禁用列表中。
func (c *MCPConfig) IsServerDisabled(serverID string) bool {
	for _, id := range c.DisabledServerIDs {
		if id == serverID {
			return true
		}
	}
	return false
}

// IsToolDisabled 判断指定工具 key 是否在禁用列表中。
func (c *MCPConfig) IsToolDisabled(toolKey string) bool {
	for _, key := range c.DisabledToolKeys {
		if key == toolKey {
			return true
		}
	}
	return false
}
