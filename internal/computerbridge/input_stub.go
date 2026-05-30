//go:build !darwin || !cgo

package computerbridge

import "fmt"

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

func darwinPostUnicodeText(text string) error {
	_ = text
	return fmt.Errorf("当前构建未启用 CGO，无法执行 Unicode 文本输入")
}