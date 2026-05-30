//go:build (!darwin && !windows && !linux) || (darwin && !cgo)

package computerbridge

import "fmt"

func platformDisplayLayouts() ([]displayLayout, error) {
	return nil, fmt.Errorf("当前构建未启用显示器布局读取")
}
