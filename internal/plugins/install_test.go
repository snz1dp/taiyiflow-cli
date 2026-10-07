package plugins

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallRootDirSharedWithHost 验证安装根目录 = SNZ1DP_HOME/plugins
// （与 Electron 宿主 plugin-installer.js 的解析规则一致）。
func TestInstallRootDirSharedWithHost(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNZ1DP_HOME", tmpDir)
	rootDir, err := InstallRootDir()
	if err != nil {
		t.Fatalf("获取安装根目录失败: %v", err)
	}
	expected := filepath.Join(tmpDir, "plugins")
	if rootDir != expected {
		t.Errorf("安装根目录应为 %s，实际 %s", expected, rootDir)
	}
	// 目录已惰性创建
	if info, err := os.Stat(rootDir); err != nil || !info.IsDir() {
		t.Errorf("安装根目录应已创建: %v", err)
	}
}

// TestReadInstallRecordEmpty 验证 installed.json 不存在时返回空表（不报错）。
func TestReadInstallRecordEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNZ1DP_HOME", tmpDir)
	records, err := ReadInstallRecord()
	if err != nil {
		t.Fatalf("读取空安装记录失败: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("空安装记录应返回空表，实际 %d 条", len(records))
	}
}

// TestReadInstallRecordHostFormat 验证能解析宿主写入的 installed.json 格式
// （字段名与 Electron plugin-installer.js 写入格式一致）。
func TestReadInstallRecordHostFormat(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNZ1DP_HOME", tmpDir)
	pluginsDir := filepath.Join(tmpDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	// 模拟宿主写入的安装记录（真实字段格式）
	hostRecord := `{
  "com.acme.demo-plugin": {
    "id": "com.acme.demo-plugin",
    "version": "0.1.0",
    "dirName": "demo-plugin",
    "dir": "/some/path/demo-plugin",
    "installedAt": "2026-10-07T12:00:00.000Z",
    "sha256": "abc123",
    "signed": true,
    "publisherKey": "MCowBQYDK2VwAyEA"
  }
}`
	recordPath := filepath.Join(pluginsDir, "installed.json")
	if err := os.WriteFile(recordPath, []byte(hostRecord), 0o644); err != nil {
		t.Fatalf("写安装记录失败: %v", err)
	}
	records, err := ReadInstallRecord()
	if err != nil {
		t.Fatalf("读取安装记录失败: %v", err)
	}
	record, ok := records["com.acme.demo-plugin"]
	if !ok {
		t.Fatalf("应包含 com.acme.demo-plugin")
	}
	if record.Version != "0.1.0" {
		t.Errorf("版本应为 0.1.0，实际 %s", record.Version)
	}
	if !record.Signed {
		t.Error("signed 应为 true")
	}
	if record.DirName != "demo-plugin" {
		t.Errorf("dirName 应为 demo-plugin，实际 %s", record.DirName)
	}
}

// TestReadInstallRecordInvalidFormat 验证非法 JSON 报错。
func TestReadInstallRecordInvalidFormat(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("SNZ1DP_HOME", tmpDir)
	pluginsDir := filepath.Join(tmpDir, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	recordPath := filepath.Join(pluginsDir, "installed.json")
	if err := os.WriteFile(recordPath, []byte("not json"), 0o644); err != nil {
		t.Fatalf("写坏记录失败: %v", err)
	}
	if _, err := ReadInstallRecord(); err == nil {
		t.Error("非法 JSON 应报错")
	}
}
