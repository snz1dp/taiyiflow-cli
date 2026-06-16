//go:build !darwin && !linux && !windows

package cmd

// readPlatformUUIDSeed 在未适配的平台上不可用，返回空字符串。
// 这些平台依赖 collectDeviceIDSeed 中的 machine_id / hostname / macs 路径。
func readPlatformUUIDSeed() string {
	if deviceIDPlatformUUID != nil {
		return deviceIDPlatformUUID()
	}
	return ""
}
