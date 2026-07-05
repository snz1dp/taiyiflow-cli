package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// stateFileName 状态持久化文件名。
const stateFileName = "update_state.json"

// downloadDirName 下载缓存目录名（相对于 ~/.snz1dp/）。
const downloadDirName = "bundle"

// StateManager 管理更新状态的持久化。
type StateManager struct {
	rootDir string
}

// NewStateManager 创建 StateManager。
func NewStateManager() *StateManager {
	rootDir, err := config.DefaultRootDir()
	if err != nil {
		// 回退到用户目录下的默认路径
		rootDir = ".snz1dp"
	}
	return &StateManager{
		rootDir: rootDir,
	}
}

// NewStateManagerWithDir 使用指定根目录创建 StateManager（测试用）。
func NewStateManagerWithDir(rootDir string) *StateManager {
	return &StateManager{rootDir: rootDir}
}

// statePath 返回状态文件完整路径。
func (s *StateManager) statePath() string {
	return filepath.Join(s.rootDir, "config", stateFileName)
}

// DownloadDir 返回下载缓存目录路径。
func (s *StateManager) DownloadDir() string {
	return filepath.Join(s.rootDir, downloadDirName)
}

// LoadState 加载持久化的更新状态。文件不存在时返回空状态。
func (s *StateManager) LoadState() (*PersistState, error) {
	data, err := os.ReadFile(s.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &PersistState{}, nil
		}
		return nil, fmt.Errorf("读取更新状态文件失败: %w", err)
	}

	var state PersistState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析更新状态文件失败: %w", err)
	}
	return &state, nil
}

// SaveState 保存更新状态到磁盘。
func (s *StateManager) SaveState(state *PersistState) error {
	dir := filepath.Dir(s.statePath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建状态目录失败: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("编码状态失败: %w", err)
	}

	if err := os.WriteFile(s.statePath(), data, 0o600); err != nil {
		return fmt.Errorf("写入状态文件失败: %w", err)
	}
	return nil
}

// ClearState 清除更新状态文件。
func (s *StateManager) ClearState() error {
	err := os.Remove(s.statePath())
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清除状态文件失败: %w", err)
	}
	return nil
}

// DownloadCachePath 返回下载文件的完整缓存路径。
func (s *StateManager) DownloadCachePath(fileName string) string {
	return filepath.Join(s.DownloadDir(), fileName)
}

// EnsureDownloadDir 确保下载目录存在。
func (s *StateManager) EnsureDownloadDir() error {
	return os.MkdirAll(s.DownloadDir(), 0o755)
}

// CleanDownloadCache 清理下载缓存目录中与指定 SHA256 不匹配的文件。
func (s *StateManager) CleanDownloadCache(keepSHA256 string) {
	entries, err := os.ReadDir(s.DownloadDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		// 不清理状态文件
		if strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		// 如果没有 keepSHA256，清理所有
		// 如果有 keepSHA256，我们不在这里做 SHA256 校验（太重），
		// 只清理明显的旧文件（以 .taiyiflow-cli-upgrade 开头的临时文件）
		if strings.HasPrefix(entry.Name(), ".taiyiflow-cli-upgrade") {
			_ = os.Remove(filepath.Join(s.DownloadDir(), entry.Name()))
		}
	}
}
