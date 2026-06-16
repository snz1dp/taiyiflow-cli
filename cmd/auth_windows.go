//go:build windows

package cmd

import "strings"

// readPlatformUUIDSeed 在 Windows 上读取注册表 MachineGuid。
// HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Cryptography\MachineGuid 是 Windows
// 安装时生成的全局唯一标识，重装系统前不会变化，也不受网络环境影响。
func readPlatformUUIDSeed() string {
	if deviceIDPlatformUUID != nil {
		return deviceIDPlatformUUID()
	}
	deviceIDPlatformUUIDOnce.Do(func() {
		deviceIDPlatformUUIDCache = defaultReadPlatformUUID()
	})
	return deviceIDPlatformUUIDCache
}

func defaultReadPlatformUUID() string {
	k, err := openMachineGuidKey()
	if err != nil {
		return ""
	}
	defer k.Close()

	value, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}
