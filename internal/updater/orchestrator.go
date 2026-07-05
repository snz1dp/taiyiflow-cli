package updater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
)

// Orchestrator 编排 check-update → download → verify → replace → report-status 全流程。
type Orchestrator struct {
	updater *Updater
	state   *StateManager
}

// NewOrchestrator 创建 Orchestrator。
func NewOrchestrator(baseURL, apiPrefix, apiKey string) *Orchestrator {
	return &Orchestrator{
		updater: NewUpdater(baseURL, apiPrefix, apiKey),
		state:   NewStateManager(),
	}
}

// NewOrchestratorWithStateManager 使用自定义 StateManager 创建 Orchestrator（测试用）。
func NewOrchestratorWithStateManager(baseURL, apiPrefix, apiKey string, stateMgr *StateManager) *Orchestrator {
	return &Orchestrator{
		updater: NewUpdater(baseURL, apiPrefix, apiKey),
		state:   stateMgr,
	}
}

// CheckAndDownload 检查更新并下载（不执行替换）。
// 返回 CheckUpdateResponse 和下载结果。
// 如果 force=true，即使版本相同也强制检查。
func (o *Orchestrator) CheckAndDownload(ctx context.Context, deviceID string, force bool, onProgress ProgressCallback) (*CheckUpdateResponse, *DownloadResult, error) {
	// 1. 调用 check-update API
	checkReq := &CheckUpdateRequest{
		Platform:       platformDetail(),
		CurrentVersion: buildinfo.Version,
		DeviceID:       deviceID,
		FileType:       fileType(),
		ClientGroup:    ClientGroup,
	}

	checkResp, err := o.updater.CheckUpdate(ctx, checkReq)
	if err != nil {
		return nil, nil, fmt.Errorf("检查更新失败: %w", err)
	}

	if !checkResp.HasUpdate {
		return checkResp, nil, nil
	}

	// 2. 检查本地缓存是否已有可复用的下载文件
	cachedPath := o.state.DownloadCachePath(checkResp.FileName)
	if checkResp.FileSHA256 != "" {
		if ok, _ := VerifySHA256(cachedPath, checkResp.FileSHA256); ok {
			// 缓存命中，直接使用
			info, _ := os.Stat(cachedPath)
			return checkResp, &DownloadResult{
				Bytes:  info.Size(),
				SHA256: checkResp.FileSHA256,
				Path:   cachedPath,
			}, nil
		}
	}

	// 3. 保存目标版本状态（用于重启后闭环验证）
	persistState := &PersistState{
		TargetVersion: checkResp.Version,
		UpdatePolicy:  checkResp.UpdatePolicy,
		ForceUpdate:   checkResp.ForceUpdate,
		DeliveryID:    checkResp.DeliveryID,
	}
	_ = o.state.SaveState(persistState)

	// 4. 上报 download_started
	if checkResp.DeliveryID != "" {
		_ = o.updater.ReportStatus(ctx, &ReportStatusRequest{
			DeliveryID: checkResp.DeliveryID,
			Action:     string(ReportDownloadStarted),
			DeviceID:   deviceID,
		})
	}

	// 5. 下载文件
	downloadPath := cachedPath
	if downloadPath == "" {
		downloadPath = o.state.DownloadCachePath(checkResp.FileName)
	}

	result, err := DownloadFile(ctx, checkResp.DownloadURL, downloadPath, checkResp.FileSize, onProgress)
	if err != nil {
		// 上报 download_failed
		if checkResp.DeliveryID != "" {
			_ = o.updater.ReportStatus(ctx, &ReportStatusRequest{
				DeliveryID: checkResp.DeliveryID,
				Action:     string(ReportDownloadFailed),
				DeviceID:   deviceID,
				ErrorMessage: err.Error(),
			})
		}
		return checkResp, nil, fmt.Errorf("下载失败: %w", err)
	}

	// 6. SHA256 校验
	if checkResp.FileSHA256 != "" {
		if !stringsEqualFold(result.SHA256, checkResp.FileSHA256) {
			// 上报 download_failed
			if checkResp.DeliveryID != "" {
				_ = o.updater.ReportStatus(ctx, &ReportStatusRequest{
					DeliveryID: checkResp.DeliveryID,
					Action:     string(ReportDownloadFailed),
					DeviceID:   deviceID,
					ErrorMessage: fmt.Sprintf("SHA256 校验不匹配: 期望 %s, 实际 %s", checkResp.FileSHA256, result.SHA256),
				})
			}
			return checkResp, nil, fmt.Errorf("SHA256 校验不匹配: 期望 %s, 实际 %s", checkResp.FileSHA256, result.SHA256)
		}
	}

	// 7. 更新持久化状态
	persistState.Download = &DownloadInfo{
		State:      StateDownloaded,
		FileName:   checkResp.FileName,
		FilePath:   result.Path,
		FileSize:   result.Bytes,
		FileSHA256: result.SHA256,
		Version:    checkResp.Version,
	}
	_ = o.state.SaveState(persistState)

	// 8. 上报 download_completed
	if checkResp.DeliveryID != "" {
		_ = o.updater.ReportStatus(ctx, &ReportStatusRequest{
			DeliveryID: checkResp.DeliveryID,
			Action:     string(ReportDownloadCompleted),
			DeviceID:   deviceID,
		})
	}

	return checkResp, result, nil
}

// InstallBinary 执行二进制替换。
// downloadPath 为已下载并校验通过的二进制文件路径。
func (o *Orchestrator) InstallBinary(ctx context.Context, downloadPath, deliveryID, deviceID string) error {
	// 获取当前可执行文件路径
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取当前可执行文件路径失败: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("解析可执行文件路径失败: %w", err)
	}

	// 上报 install_started
	if deliveryID != "" {
		_ = o.updater.ReportStatus(ctx, &ReportStatusRequest{
			DeliveryID: deliveryID,
			Action:     string(ReportInstallStarted),
			DeviceID:   deviceID,
		})
	}

	// 设置可执行权限
	if runtime.GOOS != "windows" {
		if err := os.Chmod(downloadPath, 0o755); err != nil {
			return fmt.Errorf("设置可执行权限失败: %w", err)
		}
	}

	// 平台差异处理
	if runtime.GOOS == "windows" {
		// Windows: 不能直接替换正在运行的 exe
		backupPath := execPath + ".old"
		_ = os.Remove(backupPath)
		if err := os.Rename(execPath, backupPath); err != nil {
			return fmt.Errorf("备份旧版本失败: %w", err)
		}
		if err := os.Rename(downloadPath, execPath); err != nil {
			_ = os.Rename(backupPath, execPath) // 回滚
			return fmt.Errorf("替换可执行文件失败: %w", err)
		}
		_ = os.Remove(backupPath)
	} else {
		// Unix: rename 是原子操作
		if err := os.Rename(downloadPath, execPath); err != nil {
			return fmt.Errorf("替换可执行文件失败: %w", err)
		}
	}

	// 上报 install_completed
	if deliveryID != "" {
		_ = o.updater.ReportStatus(ctx, &ReportStatusRequest{
			DeliveryID: deliveryID,
			Action:     string(ReportInstallCompleted),
			DeviceID:   deviceID,
		})
	}

	return nil
}

// VerifyStartup 启动时闭环验证：检查是否有待验证的目标版本。
// 如果当前版本 == 目标版本，说明更新已成功，清除状态。
// 如果当前版本 != 目标版本，说明更新可能失败，返回警告。
func (o *Orchestrator) VerifyStartup() (matched bool, targetVersion string, err error) {
	state, err := o.state.LoadState()
	if err != nil {
		return false, "", err
	}

	if state.TargetVersion == "" {
		return false, "", nil
	}

	if buildinfo.Version == state.TargetVersion {
		// 更新成功，清除状态
		_ = o.state.ClearState()
		return true, state.TargetVersion, nil
	}

	// 更新未完成
	return false, state.TargetVersion, nil
}

// GetPendingDownload 获取未完成的下载（用于恢复下载流程）。
func (o *Orchestrator) GetPendingDownload() (*PersistState, error) {
	state, err := o.state.LoadState()
	if err != nil {
		return nil, err
	}
	if state.Download == nil || state.Download.State != StateDownloaded {
		return nil, nil
	}

	// 检查文件是否仍然存在
	if _, err := os.Stat(state.Download.FilePath); err != nil {
		return nil, nil
	}

	return state, nil
}

// ClearState 清除更新状态。
func (o *Orchestrator) ClearState() error {
	return o.state.ClearState()
}

// stringsEqualFold 不区分大小写比较字符串。
func stringsEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
