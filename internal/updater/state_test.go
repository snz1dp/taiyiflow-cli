package updater

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateManager_SaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewStateManagerWithDir(tmpDir)

	// 空状态
	state, err := sm.LoadState()
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if state.TargetVersion != "" {
		t.Fatalf("expected empty state, got target_version=%s", state.TargetVersion)
	}

	// 保存状态
	state.TargetVersion = "1.2.0"
	state.UpdatePolicy = "notify"
	state.ForceUpdate = false
	state.DeliveryID = "test-delivery-id"
	state.Download = &DownloadInfo{
		State:      StateDownloaded,
		FileName:   "taiyiflow-cli-darwin-arm64",
		FilePath:   "/tmp/test-binary",
		FileSize:   12345,
		FileSHA256: "abc123",
		Version:    "1.2.0",
	}

	if err := sm.SaveState(state); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	// 验证文件存在
	if _, err := os.Stat(sm.statePath()); err != nil {
		t.Fatalf("state file not found: %v", err)
	}

	// 重新加载
	loaded, err := sm.LoadState()
	if err != nil {
		t.Fatalf("LoadState after save failed: %v", err)
	}
	if loaded.TargetVersion != "1.2.0" {
		t.Errorf("expected target_version=1.2.0, got %s", loaded.TargetVersion)
	}
	if loaded.Download == nil || loaded.Download.FileName != "taiyiflow-cli-darwin-arm64" {
		t.Errorf("download info mismatch: %+v", loaded.Download)
	}
}

func TestStateManager_ClearState(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewStateManagerWithDir(tmpDir)

	state := &PersistState{
		TargetVersion: "1.0.0",
		UpdatePolicy:  "silent",
	}
	if err := sm.SaveState(state); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	if err := sm.ClearState(); err != nil {
		t.Fatalf("ClearState failed: %v", err)
	}

	// 文件应不存在
	if _, err := os.Stat(sm.statePath()); !os.IsNotExist(err) {
		t.Fatalf("state file should not exist after clear")
	}

	// 再次清除不应报错
	if err := sm.ClearState(); err != nil {
		t.Fatalf("ClearState on non-existent file should not error: %v", err)
	}
}

func TestStateManager_DownloadDir(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewStateManagerWithDir(tmpDir)

	dir := sm.DownloadDir()
	expected := filepath.Join(tmpDir, "bundle")
	if dir != expected {
		t.Errorf("expected download dir %s, got %s", expected, dir)
	}

	if err := sm.EnsureDownloadDir(); err != nil {
		t.Fatalf("EnsureDownloadDir failed: %v", err)
	}

	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("download dir not created: %v", err)
	}
}

func TestStateManager_DownloadCachePath(t *testing.T) {
	tmpDir := t.TempDir()
	sm := NewStateManagerWithDir(tmpDir)

	path := sm.DownloadCachePath("taiyiflow-cli-darwin-arm64")
	expected := filepath.Join(tmpDir, "bundle", "taiyiflow-cli-darwin-arm64")
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}
}
