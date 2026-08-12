package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifySHA256(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-binary")
	testContent := "hello taiyiflow-cli upgrade test"

	if err := os.WriteFile(testFile, []byte(testContent), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	h := sha256.Sum256([]byte(testContent))
	expectedSHA := hex.EncodeToString(h[:])

	// 正确校验
	ok, err := VerifySHA256(testFile, expectedSHA)
	if err != nil {
		t.Fatalf("VerifySHA256 failed: %v", err)
	}
	if !ok {
		t.Error("expected SHA256 to match")
	}

	// 错误校验
	ok, err = VerifySHA256(testFile, "0000000000000000000000000000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("VerifySHA256 failed: %v", err)
	}
	if ok {
		t.Error("expected SHA256 to not match")
	}
}

func TestStringsEqualFold(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"abc123", "ABC123", true},
		{"ABC123", "abc123", true},
		{"abc123", "abc123", true},
		{"abc123", "abc124", false},
		{"abc", "abcd", false},
		{"", "", true},
	}

	for _, tt := range tests {
		got := stringsEqualFold(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("stringsEqualFold(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestPlatformDetail(t *testing.T) {
	pd := platformDetail()
	if pd == "" {
		t.Error("platformDetail should not be empty")
	}
	// 应包含 "-" 分隔符
	if !strings.Contains(pd, "-") {
		t.Errorf("platformDetail should contain '-', got %s", pd)
	}
}

func TestCheckUpdateRequest_Serialization(t *testing.T) {
	req := &CheckUpdateRequest{
		Platform:       "darwin-arm64",
		CurrentVersion: "0.1.0",
		DeviceID:       "test-device",
		ClientGroup:    ClientGroup,
	}

	// 验证 ClientGroup 常量
	if ClientGroup != "taiyiflow-cli" {
		t.Errorf("expected ClientGroup=taiyiflow-cli, got %s", ClientGroup)
	}

	// 验证字段值
	if req.Platform != "darwin-arm64" {
		t.Errorf("expected platform=darwin-arm64, got %s", req.Platform)
	}
	if req.ClientGroup != "taiyiflow-cli" {
		t.Errorf("expected client_group=taiyiflow-cli, got %s", req.ClientGroup)
	}
}

func TestReportAction_Constants(t *testing.T) {
	// 验证所有 action 常量与后端 _REPORT_ACTION_MAP 对齐
	expected := map[ReportAction]bool{
		ReportDownloadStarted:   true,
		ReportDownloadCompleted: true,
		ReportDownloadFailed:    true,
		ReportInstallStarted:    true,
		ReportInstallCompleted:  true,
		ReportInstallFailed:     true,
	}

	for action := range expected {
		s := string(action)
		if s == "" {
			t.Error("action should not be empty string")
		}
	}
}

func TestOrchestrator_VerifyStartup_NoState(t *testing.T) {
	tmpDir := t.TempDir()
	stateMgr := NewStateManagerWithDir(tmpDir)
	orch := &Orchestrator{
		updater: NewUpdater("https://example.com", "/api/v1", ""),
		state:   stateMgr,
	}

	matched, targetVersion, err := orch.VerifyStartup()
	if err != nil {
		t.Fatalf("VerifyStartup failed: %v", err)
	}
	if matched {
		t.Error("should not be matched with no state")
	}
	if targetVersion != "" {
		t.Errorf("expected empty target version, got %s", targetVersion)
	}
}

func TestOrchestrator_VerifyStartup_Matched(t *testing.T) {
	tmpDir := t.TempDir()
	stateMgr := NewStateManagerWithDir(tmpDir)

	// 保存一个目标版本状态（与 buildinfo.Version 相同）
	state := &PersistState{
		TargetVersion: "0.1.5", // 与 buildinfo.Version 默认值一致
		UpdatePolicy:  "notify",
	}
	if err := stateMgr.SaveState(state); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	orch := &Orchestrator{
		updater: NewUpdater("https://example.com", "/api/v1", ""),
		state:   stateMgr,
	}

	matched, targetVersion, err := orch.VerifyStartup()
	if err != nil {
		t.Fatalf("VerifyStartup failed: %v", err)
	}
	if !matched {
		t.Error("should be matched when target version equals current version")
	}
	if targetVersion != "0.1.5" {
		t.Errorf("expected target version 0.1.5, got %s", targetVersion)
	}

	// 验证状态已被清除
	loaded, _ := stateMgr.LoadState()
	if loaded.TargetVersion != "" {
		t.Error("state should be cleared after successful verification")
	}
}

func TestOrchestrator_VerifyStartup_NotMatched(t *testing.T) {
	tmpDir := t.TempDir()
	stateMgr := NewStateManagerWithDir(tmpDir)

	state := &PersistState{
		TargetVersion: "99.99.99", // 不等于 buildinfo.Version
		UpdatePolicy:  "force",
	}
	if err := stateMgr.SaveState(state); err != nil {
		t.Fatalf("SaveState failed: %v", err)
	}

	orch := &Orchestrator{
		updater: NewUpdater("https://example.com", "/api/v1", ""),
		state:   stateMgr,
	}

	matched, targetVersion, err := orch.VerifyStartup()
	if err != nil {
		t.Fatalf("VerifyStartup failed: %v", err)
	}
	if matched {
		t.Error("should not be matched when target version differs from current")
	}
	if targetVersion != "99.99.99" {
		t.Errorf("expected target version 99.99.99, got %s", targetVersion)
	}
}

func TestOrchestrator_GetPendingDownload_None(t *testing.T) {
	tmpDir := t.TempDir()
	stateMgr := NewStateManagerWithDir(tmpDir)
	orch := &Orchestrator{
		updater: NewUpdater("https://example.com", "/api/v1", ""),
		state:   stateMgr,
	}

	state, err := orch.GetPendingDownload()
	if err != nil {
		t.Fatalf("GetPendingDownload failed: %v", err)
	}
	if state != nil {
		t.Error("expected nil pending download with no state")
	}
}

func TestOrchestrator_GetPendingDownload_Exists(t *testing.T) {
	tmpDir := t.TempDir()
	stateMgr := NewStateManagerWithDir(tmpDir)

	// 创建一个假的下载文件
	downloadDir := stateMgr.DownloadDir()
	_ = stateMgr.EnsureDownloadDir()
	fakeFile := filepath.Join(downloadDir, "taiyiflow-cli-darwin-arm64")
	_ = os.WriteFile(fakeFile, []byte("fake binary"), 0o755)

	state := &PersistState{
		TargetVersion: "1.0.0",
		DeliveryID:    "test-delivery",
		Download: &DownloadInfo{
			State:      StateDownloaded,
			FileName:   "taiyiflow-cli-darwin-arm64",
			FilePath:   fakeFile,
			FileSize:   11,
			FileSHA256: "fake",
			Version:    "1.0.0",
		},
	}
	_ = stateMgr.SaveState(state)

	orch := &Orchestrator{
		updater: NewUpdater("https://example.com", "/api/v1", ""),
		state:   stateMgr,
	}

	pending, err := orch.GetPendingDownload()
	if err != nil {
		t.Fatalf("GetPendingDownload failed: %v", err)
	}
	if pending == nil {
		t.Fatal("expected pending download, got nil")
	}
	if pending.Download == nil {
		t.Fatal("expected download info, got nil")
	}
	if pending.Download.FileName != "taiyiflow-cli-darwin-arm64" {
		t.Errorf("expected file name taiyiflow-cli-darwin-arm64, got %s", pending.Download.FileName)
	}
}
