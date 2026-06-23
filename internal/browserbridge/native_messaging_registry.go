//go:build !windows

package browserbridge

// registerNativeMessagingHostForPlatform 在非 Windows 平台上为空操作。
// macOS 与 Linux 通过文件系统目录发现 Native Messaging Host 清单，
// 无需额外的注册步骤。
func registerNativeMessagingHostForPlatform(_ string, _ string) error {
	return nil
}

// unregisterNativeMessagingHostFromRegistry 在非 Windows 平台上为空操作。
func unregisterNativeMessagingHostFromRegistry(_ string) error {
	return nil
}

// diagnoseNativeMessagingRegistry 在非 Windows 平台上始终返回 true，
// 表示注册检查通过（macOS/Linux 不依赖注册表）。
func diagnoseNativeMessagingRegistry(_ string, _ string) (bool, string) {
	return true, ""
}
