//go:build linux

package computerbridge

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var xrandrMonitorGeometryPattern = regexp.MustCompile(`^(\d+)/\d+x(\d+)/\d+([+-]\d+)([+-]\d+)$`)
var xrandrConnectedModePattern = regexp.MustCompile(`^(\S+)\s+connected(?:\s+primary)?(?:\s+(\d+)x(\d+)[+-]\d+[+-]\d+)?`)

func platformDisplayLayouts() ([]displayLayout, error) {
	commandCtx, cancel := context.WithTimeout(context.Background(), defaultCommandTimeout)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, "xrandr", "--listmonitors").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("执行 xrandr --listmonitors 失败: %w", summarizeCommandError(err, output))
	}
	layouts, err := parseXrandrDisplayLayouts(string(output))
	if err != nil {
		return nil, err
	}
	if currentModes, modeErr := queryXrandrCurrentModes(); modeErr == nil {
		mergeXrandrCurrentModes(layouts, currentModes)
	}
	return layouts, nil
}

type xrandrOutputMode struct {
	Width  int
	Height int
}

func queryXrandrCurrentModes() (map[string]xrandrOutputMode, error) {
	commandCtx, cancel := context.WithTimeout(context.Background(), defaultCommandTimeout)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, "xrandr", "--query").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("执行 xrandr --query 失败: %w", summarizeCommandError(err, output))
	}
	return parseXrandrCurrentModes(string(output)), nil
}

func parseXrandrDisplayLayouts(output string) ([]displayLayout, error) {
	trimmed := strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n"))
	if trimmed == "" {
		return nil, fmt.Errorf("xrandr 未返回显示器布局")
	}
	lines := strings.Split(trimmed, "\n")
	layouts := make([]displayLayout, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "Monitors:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		geometry := fields[2]
		match := xrandrMonitorGeometryPattern.FindStringSubmatch(geometry)
		if len(match) != 5 {
			continue
		}
		width, widthErr := strconv.Atoi(match[1])
		height, heightErr := strconv.Atoi(match[2])
		originX, originXErr := strconv.Atoi(match[3])
		originY, originYErr := strconv.Atoi(match[4])
		if widthErr != nil || heightErr != nil || originXErr != nil || originYErr != nil {
			continue
		}
		nameToken := fields[1]
		primary := strings.Contains(nameToken, "*")
		displayID := strings.TrimLeft(nameToken, "+*")
		if len(fields) >= 4 {
			displayID = strings.TrimSpace(fields[len(fields)-1])
		}
		if displayID == "" {
			displayID = strings.TrimLeft(nameToken, "+*")
		}
		layout := displayLayout{
			DisplayID:   displayID,
			Width:       width,
			Height:      height,
			PixelWidth:  width,
			PixelHeight: height,
			OriginX:     originX,
			OriginY:     originY,
			Primary:     primary,
		}
		if index, err := strconv.Atoi(strings.TrimSuffix(fields[0], ":")); err == nil {
			layout.DisplayIndex = index + 1
		}
		layouts = append(layouts, layout)
	}
	if len(layouts) == 0 {
		return nil, fmt.Errorf("未从 xrandr 输出解析到显示器布局")
	}
	sort.SliceStable(layouts, func(left int, right int) bool {
		if layouts[left].DisplayIndex != 0 && layouts[right].DisplayIndex != 0 && layouts[left].DisplayIndex != layouts[right].DisplayIndex {
			return layouts[left].DisplayIndex < layouts[right].DisplayIndex
		}
		if layouts[left].Primary != layouts[right].Primary {
			return layouts[left].Primary
		}
		if layouts[left].OriginX != layouts[right].OriginX {
			return layouts[left].OriginX < layouts[right].OriginX
		}
		return layouts[left].OriginY < layouts[right].OriginY
	})
	for index := range layouts {
		if layouts[index].DisplayIndex == 0 {
			layouts[index].DisplayIndex = index + 1
		}
		if strings.TrimSpace(layouts[index].DisplayID) == "" {
			layouts[index].DisplayID = strconv.Itoa(layouts[index].DisplayIndex)
		}
	}
	return layouts, nil
}

func parseXrandrCurrentModes(output string) map[string]xrandrOutputMode {
	trimmed := strings.TrimSpace(strings.ReplaceAll(output, "\r\n", "\n"))
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	modes := make(map[string]xrandrOutputMode, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		match := xrandrConnectedModePattern.FindStringSubmatch(line)
		if len(match) != 4 {
			continue
		}
		outputName := strings.TrimSpace(match[1])
		if outputName == "" {
			continue
		}
		width, widthErr := strconv.Atoi(strings.TrimSpace(match[2]))
		height, heightErr := strconv.Atoi(strings.TrimSpace(match[3]))
		if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
			continue
		}
		modes[outputName] = xrandrOutputMode{Width: width, Height: height}
	}
	if len(modes) == 0 {
		return nil
	}
	return modes
}

func mergeXrandrCurrentModes(layouts []displayLayout, modes map[string]xrandrOutputMode) {
	if len(layouts) == 0 || len(modes) == 0 {
		return
	}
	for index := range layouts {
		mode, ok := modes[strings.TrimSpace(layouts[index].DisplayID)]
		if !ok {
			continue
		}
		if mode.Width > 0 {
			layouts[index].PixelWidth = mode.Width
		}
		if mode.Height > 0 {
			layouts[index].PixelHeight = mode.Height
		}
	}
}
