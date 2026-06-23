//go:build windows

package browserbridge

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// windowsNativeMessagingRegistrySubKey 返回指定浏览器通道在 Windows 注册表中
// NativeMessagingHosts 所在的子键路径（HKCU 下）。
func windowsNativeMessagingRegistrySubKey(channel string) string {
	switch normalizedBrowserChannel(channel) {
	case internalconfig.BrowserChannelChromium:
		return `Software\Chromium\NativeMessagingHosts`
	case internalconfig.BrowserChannelEdge:
		return `Software\Microsoft\Edge\NativeMessagingHosts`
	case internalconfig.BrowserChannelBrave:
		return `Software\BraveSoftware\Brave-Browser\NativeMessagingHosts`
	default:
		return `Software\Google\Chrome\NativeMessagingHosts`
	}
}

// registerNativeMessagingHostForPlatform 在 Windows 注册表 HKCU 下为所有相关
// 浏览器通道注册 Native Messaging Host 清单路径，使 Chrome 系浏览器能够发现
// 并启动本地消息宿主。非 Windows 平台上该函数为空操作。
func registerNativeMessagingHostForPlatform(channel string, manifestPath string) error {
	channels := nativeMessagingManifestChannels(channel)
	for _, ch := range channels {
		subKey := windowsNativeMessagingRegistrySubKey(ch) + `\` + NativeMessagingHostName
		key, _, err := registry.CreateKey(
			registry.CURRENT_USER,
			subKey,
			registry.SET_VALUE|registry.CREATE_SUB_KEY,
		)
		if err != nil {
			return fmt.Errorf(
				"创建 Windows 注册表键 HKCU\\%s 失败: %w",
				subKey, err,
			)
		}
		absManifestPath := manifestPath
		if !filepath.IsAbs(absManifestPath) {
			if resolved, resolveErr := filepath.Abs(absManifestPath); resolveErr == nil {
				absManifestPath = resolved
			}
		}
		if err := key.SetStringValue("", strings.ReplaceAll(absManifestPath, "/", "\\")); err != nil {
			_ = key.Close()
			return fmt.Errorf(
				"写入 Windows 注册表键 HKCU\\%s 默认值失败: %w",
				subKey, err,
			)
		}
		_ = key.Close()
	}
	return nil
}

// unregisterNativeMessagingHostFromRegistry 从 Windows 注册表中移除当前宿主名称
// 对应的所有通道注册键。非 Windows 平台上该函数为空操作。
func unregisterNativeMessagingHostFromRegistry(channel string) error {
	channels := nativeMessagingManifestChannels(channel)
	for _, ch := range channels {
		parentSubKey := windowsNativeMessagingRegistrySubKey(ch)
		_ = registry.DeleteKey(registry.CURRENT_USER, parentSubKey+`\`+NativeMessagingHostName)
	}
	return nil
}

// diagnoseNativeMessagingRegistry 检测 Windows 注册表中指定通道的 Native Messaging
// Host 注册项是否存在且指向给定的清单路径。非 Windows 平台上始终返回 true。
func diagnoseNativeMessagingRegistry(channel string, manifestPath string) (registered bool, registryPath string) {
	subKey := windowsNativeMessagingRegistrySubKey(channel) + `\` + NativeMessagingHostName
	key, err := registry.OpenKey(registry.CURRENT_USER, subKey, registry.QUERY_VALUE)
	if err != nil {
		return false, ""
	}
	defer key.Close()
	val, _, err := key.GetStringValue("")
	if err != nil {
		return false, ""
	}
	normalizedRegistry := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(val), "/", "\\"))
	normalizedManifest := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(manifestPath), "/", "\\"))
	return normalizedRegistry == normalizedManifest, val
}
