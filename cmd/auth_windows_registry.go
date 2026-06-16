//go:build windows

package cmd

import "golang.org/x/sys/windows/registry"

// openMachineGuidKey 打开 Windows 注册表中 MachineGuid 所在的键。
// 独立为函数便于测试 mock。
func openMachineGuidKey() (registry.Key, error) {
	return registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`,
		registry.READ|registry.WOW64_64KEY,
	)
}
