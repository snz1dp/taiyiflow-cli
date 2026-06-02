//go:build !darwin || !cgo

package computerbridge

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
)

func darwinPointerInputAvailability() (bool, string) {
	return false, "当前构建未启用 CGO，桌面鼠标与滚轮注入不可用"
}

func darwinCurrentMousePosition() (int, int, error) {
	return 0, 0, fmt.Errorf("当前构建未启用 CGO，无法读取鼠标位置")
}

func darwinPostMouseMove(x int, y int) error {
	_ = x
	_ = y
	return fmt.Errorf("当前构建未启用 CGO，无法执行鼠标移动")
}

func darwinPostMouseButtonEvent(kind int, button int, x int, y int, clickState int) error {
	_ = kind
	_ = button
	_ = x
	_ = y
	_ = clickState
	return fmt.Errorf("当前构建未启用 CGO，无法执行鼠标点击或拖拽")
}

func darwinPostScrollEvent(deltaX int, deltaY int) error {
	_ = deltaX
	_ = deltaY
	return fmt.Errorf("当前构建未启用 CGO，无法执行滚轮滚动")
}

func darwinUnicodeTextInputAvailable() bool {
	return false
}

func darwinPostKeyCode(keyCode int) error {
	_ = keyCode
	return fmt.Errorf("当前构建未启用 CGO，无法执行键盘按键注入")
}

func darwinPostModifiedKeyCode(keyCode int, command bool, shift bool, option bool, control bool) error {
	_ = keyCode
	_ = command
	_ = shift
	_ = option
	_ = control
	return fmt.Errorf("当前构建未启用 CGO，无法执行带修饰键的键盘按键注入")
}

func darwinPostCommandVShortcut() error {
	return fmt.Errorf("当前构建未启用 CGO，无法执行 Command+V 粘贴快捷键")
}

func darwinPostUnicodeText(text string) error {
	_ = text
	return fmt.Errorf("当前构建未启用 CGO，无法执行 Unicode 文本输入")
}

func darwinCaptureClipboard() ([]byte, error) {
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("当前平台不支持读取系统剪贴板快照")
	}
	command := exec.Command("pbpaste")
	output, err := command.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("当前构建未启用 CGO，读取系统剪贴板文本失败: %w", err)
	}
	return bytes.Clone(output), nil
}

func darwinRestoreClipboard(snapshot []byte) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("当前平台不支持恢复系统剪贴板快照")
	}
	command := exec.Command("pbcopy")
	command.Stdin = bytes.NewReader(snapshot)
	if err := command.Run(); err != nil {
		return fmt.Errorf("当前构建未启用 CGO，恢复系统剪贴板文本失败: %w", err)
	}
	return nil
}

func darwinSetClipboardText(text string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("当前平台不支持写入系统剪贴板")
	}
	command := exec.Command("pbcopy")
	command.Stdin = bytes.NewBufferString(text)
	if err := command.Run(); err != nil {
		return fmt.Errorf("当前构建未启用 CGO，写入系统剪贴板文本失败: %w", err)
	}
	return nil
}
