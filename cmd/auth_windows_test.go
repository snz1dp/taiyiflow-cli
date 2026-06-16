//go:build windows

package cmd

import (
	"testing"
)

// TestDefaultReadPlatformUUIDViaMock 验证 Windows 平台通过 deviceIDPlatformUUID
// mock 可以正确返回 MachineGuid。实际注册表读取由 defaultReadPlatformUUID 负责，
// 因注册表无法在单元测试中安全 mock，故仅验证 mock 路径。
func TestDefaultReadPlatformUUIDViaMock(t *testing.T) {
	originalPlatformUUID := deviceIDPlatformUUID
	t.Cleanup(func() { deviceIDPlatformUUID = originalPlatformUUID })

	deviceIDPlatformUUID = func() string { return "a1b2c3d4-e5f6-7890-abcd-ef1234567890" }

	got := readPlatformUUIDSeed()
	if got != "a1b2c3d4-e5f6-7890-abcd-ef1234567890" {
		t.Fatalf("expected mock MachineGuid, got %q", got)
	}
}
