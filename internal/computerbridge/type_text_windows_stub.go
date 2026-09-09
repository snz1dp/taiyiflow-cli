//go:build !windows

package computerbridge

import (
	"context"
	"fmt"
)

func windowsTypeTextAvailable() (bool, string) {
	return false, "当前构建未启用 Windows 文本输入"
}

func windowsKeyPressAvailable() (bool, string) {
	return false, "当前构建未启用 Windows 键盘输入"
}

func windowsTypeText(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	_ = ctx
	_ = arguments
	return nil, fmt.Errorf("当前构建未启用 Windows 文本输入")
}

func windowsKeyPress(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	_ = ctx
	_ = arguments
	return nil, fmt.Errorf("当前构建未启用 Windows 键盘输入")
}
