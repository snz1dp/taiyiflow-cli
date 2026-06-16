//go:build linux

package cmd

import "strings"

// readPlatformUUIDSeed 在 Linux 上读取 DMI Product UUID。
// /sys/class/dmi/id/product_uuid 是主板级别的硬件标识，重装系统不会变化。
// 该文件通常需要 root 权限才能读取；无权限时返回空字符串，
// 由 collectDeviceIDSeed 中的 machine_id 路径兜底。
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
	payload, err := deviceIDReadFile("/sys/class/dmi/id/product_uuid")
	if err != nil {
		return ""
	}
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || trimmed == "Not Settable" || trimmed == "Not Present" {
		return ""
	}
	return trimmed
}
