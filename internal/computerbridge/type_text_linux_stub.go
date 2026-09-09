//go:build !linux

package computerbridge

import (
	"context"
	"fmt"
)

func linuxTypeTextAvailable() (bool, string) {
	return false, "当前构建未启用 Linux 文本输入"
}

func linuxKeyPressAvailable() (bool, string) {
	return false, "当前构建未启用 Linux 键盘输入"
}

func linuxTypeText(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	_ = ctx
	_ = arguments
	return nil, fmt.Errorf("当前构建未启用 Linux 文本输入")
}

func linuxKeyPress(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	_ = ctx
	_ = arguments
	return nil, fmt.Errorf("当前构建未启用 Linux 键盘输入")
}
