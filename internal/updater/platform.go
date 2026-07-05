package updater

import (
	"fmt"
	"runtime"
)

// platformDetail 返回后端期望的 platform 字符串，格式为 "{os}-{arch}"。
// 后端 _resolve_file_type 函数根据此值推导默认 file_type。
func platformDetail() string {
	os := runtime.GOOS
	arch := runtime.GOARCH

	// 后端 platform 格式约定：
	// darwin-arm64 / darwin-x64 / windows-x64 / linux-arm64 / linux-x64
	switch os {
	case "darwin":
		return fmt.Sprintf("darwin-%s", arch)
	case "windows":
		return fmt.Sprintf("windows-%s", arch)
	case "linux":
		return fmt.Sprintf("linux-%s", arch)
	default:
		return fmt.Sprintf("%s-%s", os, arch)
	}
}

// runtimeArch 返回标准化架构名。
func runtimeArch() string {
	return runtime.GOARCH
}

// fileType 返回当前平台对应的安装包文件类型后缀。
// CLI 是单一二进制文件，不使用安装包格式（dmg/exe/deb），
// 后端在 file_type 为空时按 platform 推导默认值。
// CLI 端直接传空，让后端推导。
func fileType() string {
	return ""
}
