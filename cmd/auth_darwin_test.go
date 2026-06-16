//go:build darwin

package cmd

import (
	"runtime"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// TestDeviceIDIsDeterministicOnRealMac 在真实 macOS 环境下验证
// generateDeviceID 端到端确定性：不注入任何 mock，走完整生产路径
// （readPlatformUUIDSeed → sync.Once → defaultReadPlatformUUID → ioreg），
// 多次调用必须返回完全一致的结果。
func TestDeviceIDIsDeterministicOnRealMac(t *testing.T) {
	// 保存并清空所有 mock 变量，确保走生产路径
	originalHostname := deviceIDHostname
	originalInterfaces := deviceIDInterfaces
	originalReadFile := deviceIDReadFile
	originalRandomRead := deviceIDRandomRead
	originalPlatformUUID := deviceIDPlatformUUID
	resetDeviceIDCaches()
	t.Cleanup(func() {
		deviceIDHostname = originalHostname
		deviceIDInterfaces = originalInterfaces
		deviceIDReadFile = originalReadFile
		deviceIDRandomRead = originalRandomRead
		deviceIDPlatformUUID = originalPlatformUUID
		resetDeviceIDCaches()
	})

	// 关键：deviceIDPlatformUUID 保持 nil，让 readPlatformUUIDSeed 走
	// sync.Once + defaultReadPlatformUUID() 的真实 ioreg 路径
	deviceIDPlatformUUID = nil

	// 第一次调用
	first, err := generateDeviceID()
	if err != nil {
		t.Fatalf("first generateDeviceID failed: %v", err)
	}
	if first == "" {
		t.Fatal("first generateDeviceID returned empty string")
	}

	// 第二次调用（sync.Once 缓存应生效）
	second, err := generateDeviceID()
	if err != nil {
		t.Fatalf("second generateDeviceID failed: %v", err)
	}

	// 第三次调用（进一步确认稳定性）
	third, err := generateDeviceID()
	if err != nil {
		t.Fatalf("third generateDeviceID failed: %v", err)
	}

	if first != second || second != third {
		t.Fatalf("device_id 在同一进程内多次调用结果不一致: first=%q second=%q third=%q",
			first, second, third)
	}

	// 验证 ID 格式：应为 32 位十六进制（SHA256 前 16 字节）
	if len(first) != 32 {
		t.Fatalf("device_id 长度应为 32（SHA256 前 16 字节 hex），实际 %d: %q", len(first), first)
	}
	for _, c := range first {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("device_id 包含非法字符 %q: %q", string(c), first)
		}
	}

	t.Logf("macOS 真实设备 ID（确定性验证通过）: %s", first)
}

// TestDeviceIDSeedUsesPlatformUUIDOnRealMac 验证在真实 macOS 上
// collectDeviceIDSeed 优先使用 IOPlatformUUID，seed 格式符合预期。
func TestDeviceIDSeedUsesPlatformUUIDOnRealMac(t *testing.T) {
	originalPlatformUUID := deviceIDPlatformUUID
	resetDeviceIDCaches()
	t.Cleanup(func() {
		deviceIDPlatformUUID = originalPlatformUUID
		resetDeviceIDCaches()
	})

	deviceIDPlatformUUID = nil

	seed, err := collectDeviceIDSeed()
	if err != nil {
		t.Fatalf("collectDeviceIDSeed failed: %v", err)
	}

	// 在 macOS 上，IOPlatformUUID 应该可用，seed 应以 platform_uuid= 开头
	if !strings.HasPrefix(seed, "platform_uuid=") {
		t.Fatalf("macOS 上期望 seed 以 platform_uuid= 开头，实际: %q", seed)
	}

	// seed 应包含 goos 和 goarch
	if !strings.Contains(seed, "goos="+runtime.GOOS) {
		t.Fatalf("seed 缺少 goos: %q", seed)
	}
	if !strings.Contains(seed, "goarch="+runtime.GOARCH) {
		t.Fatalf("seed 缺少 goarch: %q", seed)
	}

	// seed 不应包含 hostname 或 macs（因为 platform_uuid 已可用）
	if strings.Contains(seed, "hostname=") {
		t.Fatalf("seed 不应包含 hostname（platform_uuid 可用时）: %q", seed)
	}
	if strings.Contains(seed, "macs=") {
		t.Fatalf("seed 不应包含 macs（platform_uuid 可用时）: %q", seed)
	}

	t.Logf("macOS 真实 seed: %s", seed)
}

// TestDeviceIDCacheResetProducesSameResult 验证缓存重置后
// 再次调用仍返回相同结果（模拟进程重启场景）。
func TestDeviceIDCacheResetProducesSameResult(t *testing.T) {
	originalPlatformUUID := deviceIDPlatformUUID
	resetDeviceIDCaches()
	t.Cleanup(func() {
		deviceIDPlatformUUID = originalPlatformUUID
		resetDeviceIDCaches()
	})

	deviceIDPlatformUUID = nil

	// 第一次调用（填充缓存）
	first, err := generateDeviceID()
	if err != nil {
		t.Fatalf("first generateDeviceID failed: %v", err)
	}

	// 重置缓存（模拟新进程）
	resetDeviceIDCaches()

	// 第二次调用（重新走 ioreg）
	second, err := generateDeviceID()
	if err != nil {
		t.Fatalf("second generateDeviceID after cache reset failed: %v", err)
	}

	if first != second {
		t.Fatalf("缓存重置后 device_id 不一致（模拟进程重启失败）: before=%q after=%q",
			first, second)
	}

	t.Logf("缓存重置后 device_id 一致: %s", first)
}

// TestEnsureClientIdentityEndToEndOnMac 验证 ensureClientIdentity
// 在真实 macOS 环境下的完整链路：生成 → 持久化 → 幂等。
func TestEnsureClientIdentityEndToEndOnMac(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}

	// 第一次调用：应生成并持久化 DeviceID
	updatedConfig, changed, err := app.ensureClientIdentity(config)
	if err != nil {
		t.Fatalf("first ensureClientIdentity failed: %v", err)
	}
	if !changed {
		t.Fatal("expected first call to persist generated ids")
	}
	if updatedConfig.DeviceID == "" {
		t.Fatal("expected non-empty DeviceID")
	}

	// 从磁盘重新加载
	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.DeviceID != updatedConfig.DeviceID {
		t.Fatalf("DeviceID 持久化不一致: in-memory=%q on-disk=%q",
			updatedConfig.DeviceID, reloaded.DeviceID)
	}

	// 第二次调用：应幂等，不再变更
	againConfig, changed, err := app.ensureClientIdentity(reloaded)
	if err != nil {
		t.Fatalf("second ensureClientIdentity failed: %v", err)
	}
	if changed {
		t.Fatalf("expected second call to be idempotent, got changed=true: %#v", againConfig)
	}
	if againConfig.DeviceID != updatedConfig.DeviceID {
		t.Fatalf("DeviceID 在幂等调用后变化: before=%q after=%q",
			updatedConfig.DeviceID, againConfig.DeviceID)
	}

	t.Logf("ensureClientIdentity 端到端验证通过: DeviceID=%s", updatedConfig.DeviceID)
}
