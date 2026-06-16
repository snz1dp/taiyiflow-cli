//go:build darwin

package cmd

import (
	"os/exec"
	"strings"
)

// readPlatformUUIDSeed 通过 macOS 的 ioreg 命令读取 IOPlatformUUID。
// IOPlatformUUID 是 macOS 上最稳定的硬件标识，不受网络环境或主机名变更影响。
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
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "IOPlatformUUID") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		value := strings.TrimSpace(parts[1])
		value = strings.Trim(value, "\"")
		if value != "" {
			return value
		}
	}
	return ""
}
