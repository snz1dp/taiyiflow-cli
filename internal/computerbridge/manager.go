package computerbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

const (
	toolDisplaysContext = "taiyiflow-computer__displays_context"
	toolWindowsContext  = "taiyiflow-computer__windows_context"
	toolFocusWindow     = "taiyiflow-computer__focus_window"
	toolScreenshot      = "taiyiflow-computer__screenshot"
	toolMouseMove       = "taiyiflow-computer__mouse_move"
	toolMouseClick      = "taiyiflow-computer__mouse_click"
	toolMouseDrag       = "taiyiflow-computer__mouse_drag"
	toolScroll          = "taiyiflow-computer__scroll"
	toolKeyPress        = "taiyiflow-computer__key_press"
	toolTypeText        = "taiyiflow-computer__type_text"

	defaultCommandTimeout = 5 * time.Second
	mouseEventDown        = 0
	mouseEventUp          = 1
	mouseEventDragged     = 2
)

var (
	darwinCurrentMousePositionFunc = darwinCurrentMousePosition
	darwinPostMouseMoveFunc        = darwinPostMouseMove
	darwinPostMouseButtonEventFunc = darwinPostMouseButtonEvent
	darwinSettleMouseBeforeClick   = func(ctx context.Context) error {
		timer := time.NewTimer(35 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
	darwinTypeTextFunc            = darwinTypeText
	darwinPostModifiedKeyCodeFunc = darwinPostModifiedKeyCode
	darwinSettleAfterShortcut     = func(ctx context.Context) error {
		return waitForDarwinInputSettle(ctx, 160*time.Millisecond)
	}
	displayScreenshotDimensionsMu sync.RWMutex
	displayScreenshotByID         = map[string]displayScreenshotDimensions{}
	displayScreenshotByIndex      = map[int]displayScreenshotDimensions{}
)

type displayScreenshotDimensions struct {
	Width  int
	Height int
}

type displayLayout struct {
	DisplayID    string
	DisplayIndex int
	OriginX      int
	OriginY      int
	Width        int
	Height       int
	PixelWidth   int
	PixelHeight  int
	ScaleFactor  float64
	Primary      bool
}

func displayLogicalWidth(layout displayLayout) int {
	if layout.Width > 0 {
		return layout.Width
	}
	return layout.PixelWidth
}

func displayLogicalHeight(layout displayLayout) int {
	if layout.Height > 0 {
		return layout.Height
	}
	return layout.PixelHeight
}

func displayPixelWidth(layout displayLayout) int {
	if layout.PixelWidth > 0 {
		return layout.PixelWidth
	}
	return layout.Width
}

func displayPixelHeight(layout displayLayout) int {
	if layout.PixelHeight > 0 {
		return layout.PixelHeight
	}
	return layout.Height
}

func displayScaleFactor(layout displayLayout) float64 {
	if layout.ScaleFactor > 0 {
		return layout.ScaleFactor
	}
	if logicalWidth, pixelWidth := displayLogicalWidth(layout), displayPixelWidth(layout); logicalWidth > 0 && pixelWidth > 0 {
		return float64(pixelWidth) / float64(logicalWidth)
	}
	if logicalHeight, pixelHeight := displayLogicalHeight(layout), displayPixelHeight(layout); logicalHeight > 0 && pixelHeight > 0 {
		return float64(pixelHeight) / float64(logicalHeight)
	}
	return 1.0
}

func translateDisplayLocalCoordinate(value int, logicalSize int, pixelSize int) int {
	if logicalSize <= 0 || pixelSize <= 0 || logicalSize == pixelSize {
		return value
	}
	translated := int(math.Round(float64(value) * float64(logicalSize) / float64(pixelSize)))
	if value >= 0 && value < pixelSize && translated >= logicalSize {
		return logicalSize - 1
	}
	return translated
}

func cacheDisplayScreenshotDimensions(displayID string, displayIndex int, width int, height int) {
	if width <= 0 || height <= 0 {
		return
	}
	displayScreenshotDimensionsMu.Lock()
	defer displayScreenshotDimensionsMu.Unlock()
	entry := displayScreenshotDimensions{Width: width, Height: height}
	if trimmedID := strings.TrimSpace(displayID); trimmedID != "" {
		displayScreenshotByID[trimmedID] = entry
	}
	if displayIndex > 0 {
		displayScreenshotByIndex[displayIndex] = entry
	}
}

func cachedDisplayScreenshotDimensions(displayID string, displayIndex int) (displayScreenshotDimensions, bool) {
	displayScreenshotDimensionsMu.RLock()
	defer displayScreenshotDimensionsMu.RUnlock()
	if trimmedID := strings.TrimSpace(displayID); trimmedID != "" {
		if entry, ok := displayScreenshotByID[trimmedID]; ok {
			return entry, true
		}
	}
	if displayIndex > 0 {
		if entry, ok := displayScreenshotByIndex[displayIndex]; ok {
			return entry, true
		}
	}
	return displayScreenshotDimensions{}, false
}

func clearDisplayScreenshotDimensionsCache() {
	displayScreenshotDimensionsMu.Lock()
	defer displayScreenshotDimensionsMu.Unlock()
	displayScreenshotByID = map[string]displayScreenshotDimensions{}
	displayScreenshotByIndex = map[int]displayScreenshotDimensions{}
}

func resolveDisplayLocalPixelDimensions(arguments map[string]any, layout displayLayout) (int, int) {
	width := intValue(arguments["screenshot_width_px"], 0)
	height := intValue(arguments["screenshot_height_px"], 0)
	if width > 0 && height > 0 {
		return width, height
	}
	if cached, ok := cachedDisplayScreenshotDimensions(strings.TrimSpace(firstString(arguments, "display_id")), intValue(arguments["display_index"], 0)); ok {
		if width <= 0 {
			width = cached.Width
		}
		if height <= 0 {
			height = cached.Height
		}
	}
	if width <= 0 {
		width = displayPixelWidth(layout)
	}
	if height <= 0 {
		height = displayPixelHeight(layout)
	}
	return width, height
}

func readPNGDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	config, err := png.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}

type Capability struct {
	ToolName   string `json:"tool_name"`
	Available  bool   `json:"available"`
	Category   string `json:"category,omitempty"`
	Reason     string `json:"reason,omitempty"`
	RiskTier   string `json:"risk_tier,omitempty"`
	Permission string `json:"permission,omitempty"`
}

type Status struct {
	Platform       string       `json:"platform"`
	Available      bool         `json:"available"`
	ReadAvailable  bool         `json:"read_available"`
	InputAvailable bool         `json:"input_available"`
	Message        string       `json:"message"`
	Capabilities   []Capability `json:"capabilities,omitempty"`
}

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Diagnose(config internalconfig.ComputerConfig) Status {
	_ = config
	if !internalconfig.ComputerEnabled(config) {
		return disabledStatus(runtime.GOOS)
	}
	switch runtime.GOOS {
	case "darwin":
		return diagnoseDarwin()
	case "windows":
		return diagnoseWindows()
	case "linux":
		return diagnoseLinux()
	default:
		return unsupportedStatus()
	}
}

func (m *Manager) Execute(ctx context.Context, config internalconfig.ComputerConfig, toolName string, arguments map[string]any) (map[string]any, error) {
	_ = config
	if !internalconfig.ComputerEnabled(config) {
		return nil, fmt.Errorf("桌面操作已在配置中禁用")
	}
	switch runtime.GOOS {
	case "darwin":
		return executeDarwinTool(ctx, toolName, arguments)
	case "windows":
		return executeWindowsTool(ctx, toolName, arguments)
	case "linux":
		return executeLinuxTool(ctx, toolName, arguments)
	default:
		return nil, fmt.Errorf("当前平台 %s 尚未实现桌面操作", runtime.GOOS)
	}
}

func disabledStatus(platform string) Status {
	trimmedPlatform := strings.TrimSpace(platform)
	if trimmedPlatform == "" {
		trimmedPlatform = "unknown"
	}
	capabilities := []Capability{}
	for _, item := range allCapabilities() {
		item.Available = false
		item.Reason = "已在配置中禁用"
		capabilities = append(capabilities, item)
	}
	return Status{
		Platform:       trimmedPlatform,
		Available:      false,
		ReadAvailable:  false,
		InputAvailable: false,
		Message:        fmt.Sprintf("平台: %s | 已在配置中禁用桌面操作", trimmedPlatform),
		Capabilities:   capabilities,
	}
}

func unsupportedStatus() Status {
	platform := runtime.GOOS
	if strings.TrimSpace(platform) == "" {
		platform = "unknown"
	}
	capabilities := []Capability{}
	for _, item := range allCapabilities() {
		item.Available = false
		item.Reason = fmt.Sprintf("当前平台 %s 尚未实现", platform)
		capabilities = append(capabilities, item)
	}
	return Status{
		Platform:       platform,
		Available:      false,
		ReadAvailable:  false,
		InputAvailable: false,
		Message:        fmt.Sprintf("平台: %s | 当前尚未实现桌面操作桥接", platform),
		Capabilities:   capabilities,
	}
}

func allCapabilities() []Capability {
	return []Capability{
		{ToolName: toolDisplaysContext, Category: "read", RiskTier: "read_only", Permission: "read"},
		{ToolName: toolWindowsContext, Category: "read", RiskTier: "read_only", Permission: "read"},
		{ToolName: toolScreenshot, Category: "read", RiskTier: "read_only", Permission: "read"},
		{ToolName: toolFocusWindow, Category: "input", RiskTier: "mutating", Permission: "input"},
		{ToolName: toolKeyPress, Category: "input", RiskTier: "mutating", Permission: "input"},
		{ToolName: toolTypeText, Category: "input", RiskTier: "mutating", Permission: "input"},
		{ToolName: toolMouseMove, Category: "input", RiskTier: "mutating", Permission: "input"},
		{ToolName: toolMouseClick, Category: "input", RiskTier: "mutating", Permission: "input"},
		{ToolName: toolMouseDrag, Category: "input", RiskTier: "mutating", Permission: "input"},
		{ToolName: toolScroll, Category: "input", RiskTier: "mutating", Permission: "input"},
	}
}

func diagnoseDisplayOnlyPlatform(platform string) Status {
	capabilities := allCapabilities()
	lookup := map[string]*Capability{}
	for index := range capabilities {
		lookup[capabilities[index].ToolName] = &capabilities[index]
		lookup[capabilities[index].ToolName].Reason = fmt.Sprintf("当前平台 %s 尚未实现", platformLabel(platform))
	}
	layouts, err := platformDisplayLayouts()
	if err == nil && len(layouts) > 0 {
		lookup[toolDisplaysContext].Available = true
		lookup[toolDisplaysContext].Reason = ""
	} else if err != nil {
		lookup[toolDisplaysContext].Reason = err.Error()
	} else {
		lookup[toolDisplaysContext].Reason = "未发现在线显示器"
	}
	readable := []string{}
	for _, item := range capabilities {
		if item.Available && item.Category == "read" {
			readable = append(readable, strings.TrimPrefix(item.ToolName, "taiyiflow-computer__"))
		}
	}
	parts := []string{fmt.Sprintf("平台: %s", platformLabel(platform))}
	if len(readable) > 0 {
		parts = append(parts, fmt.Sprintf("可用读取: %s", strings.Join(readable, ",")))
	} else {
		parts = append(parts, "可用读取: <none>")
	}
	parts = append(parts, "可用输入: <none>")
	return Status{
		Platform:       platform,
		Available:      len(readable) > 0,
		ReadAvailable:  len(readable) > 0,
		InputAvailable: false,
		Message:        strings.Join(parts, " | "),
		Capabilities:   capabilities,
	}
}

func diagnoseLinux() Status {
	capabilities := allCapabilities()
	lookup := map[string]*Capability{}
	for index := range capabilities {
		lookup[capabilities[index].ToolName] = &capabilities[index]
		lookup[capabilities[index].ToolName].Reason = fmt.Sprintf("当前平台 %s 尚未实现", platformLabel("linux"))
	}
	layouts, err := platformDisplayLayouts()
	if err == nil && len(layouts) > 0 {
		lookup[toolDisplaysContext].Available = true
		lookup[toolDisplaysContext].Reason = ""
	} else if err != nil {
		lookup[toolDisplaysContext].Reason = err.Error()
	} else {
		lookup[toolDisplaysContext].Reason = "未发现在线显示器"
	}
	if available, reason := linuxTypeTextAvailable(); available {
		lookup[toolTypeText].Available = true
		lookup[toolTypeText].Reason = linuxTypeTextCapabilityReason()
	} else {
		lookup[toolTypeText].Reason = reason
	}
	if available, reason := linuxKeyPressAvailable(); available {
		lookup[toolKeyPress].Available = true
		lookup[toolKeyPress].Reason = ""
	} else {
		lookup[toolKeyPress].Reason = reason
	}
	return buildPlatformStatus("linux", capabilities)
}

func diagnoseWindows() Status {
	capabilities := allCapabilities()
	lookup := map[string]*Capability{}
	for index := range capabilities {
		lookup[capabilities[index].ToolName] = &capabilities[index]
		lookup[capabilities[index].ToolName].Reason = fmt.Sprintf("当前平台 %s 尚未实现", platformLabel("windows"))
	}
	layouts, err := platformDisplayLayouts()
	if err == nil && len(layouts) > 0 {
		lookup[toolDisplaysContext].Available = true
		lookup[toolDisplaysContext].Reason = ""
	} else if err != nil {
		lookup[toolDisplaysContext].Reason = err.Error()
	} else {
		lookup[toolDisplaysContext].Reason = "未发现在线显示器"
	}
	if available, reason := windowsTypeTextAvailable(); available {
		lookup[toolTypeText].Available = true
		lookup[toolTypeText].Reason = windowsTypeTextCapabilityReason()
	} else {
		lookup[toolTypeText].Reason = reason
	}
	if available, reason := windowsKeyPressAvailable(); available {
		lookup[toolKeyPress].Available = true
		lookup[toolKeyPress].Reason = ""
	} else {
		lookup[toolKeyPress].Reason = reason
	}
	return buildPlatformStatus("windows", capabilities)
}

func buildPlatformStatus(platform string, capabilities []Capability) Status {
	readable := []string{}
	inputable := []string{}
	for _, item := range capabilities {
		if !item.Available {
			continue
		}
		if item.Category == "read" {
			readable = append(readable, strings.TrimPrefix(item.ToolName, "taiyiflow-computer__"))
		} else {
			inputable = append(inputable, strings.TrimPrefix(item.ToolName, "taiyiflow-computer__"))
		}
	}
	parts := []string{fmt.Sprintf("平台: %s", platformLabel(platform))}
	if len(readable) > 0 {
		parts = append(parts, fmt.Sprintf("可用读取: %s", strings.Join(readable, ",")))
	} else {
		parts = append(parts, "可用读取: <none>")
	}
	if len(inputable) > 0 {
		parts = append(parts, fmt.Sprintf("可用输入: %s", strings.Join(inputable, ",")))
	} else {
		parts = append(parts, "可用输入: <none>")
	}
	return Status{
		Platform:       platform,
		Available:      len(readable) > 0 || len(inputable) > 0,
		ReadAvailable:  len(readable) > 0,
		InputAvailable: len(inputable) > 0,
		Message:        strings.Join(parts, " | "),
		Capabilities:   capabilities,
	}
}

func linuxTypeTextCapabilityReason() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), "wayland") || strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "" {
		return "Wayland 通过 wtype 直接输入，不修改剪贴板"
	}
	return "X11 当前通过文本剪贴板粘贴输入，暂不支持完整多格式剪贴板恢复"
}

func windowsTypeTextCapabilityReason() string {
	return "输入前会快照并恢复常见多格式剪贴板；遇到暂不支持恢复的格式时会拒绝覆盖剪贴板"
}

func executeDisplayOnlyTool(ctx context.Context, platform string, toolName string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(toolName) {
	case toolDisplaysContext:
		layouts, err := platformDisplayLayouts()
		if err != nil {
			return nil, fmt.Errorf("读取显示器信息失败: %w", err)
		}
		return buildDisplaysContext(platform, layouts), nil
	default:
		return nil, fmt.Errorf("当前平台 %s 仅实现 displays_context，暂不支持 %s", platformLabel(platform), toolName)
	}
}

func executeLinuxTool(ctx context.Context, toolName string, arguments map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(toolName) {
	case toolDisplaysContext:
		layouts, err := platformDisplayLayouts()
		if err != nil {
			return nil, fmt.Errorf("读取显示器信息失败: %w", err)
		}
		return buildDisplaysContext("linux", layouts), nil
	case toolTypeText:
		if available, reason := linuxTypeTextAvailable(); !available {
			return nil, fmt.Errorf("输入文本失败: %s", reason)
		}
		return linuxTypeText(ctx, arguments)
	case toolKeyPress:
		if available, reason := linuxKeyPressAvailable(); !available {
			return nil, fmt.Errorf("发送按键失败: %s", reason)
		}
		return linuxKeyPress(ctx, arguments)
	default:
		return nil, fmt.Errorf("当前平台 %s 仅实现 displays_context、type_text 和 key_press，暂不支持 %s", platformLabel("linux"), toolName)
	}
}

func executeWindowsTool(ctx context.Context, toolName string, arguments map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(toolName) {
	case toolDisplaysContext:
		layouts, err := platformDisplayLayouts()
		if err != nil {
			return nil, fmt.Errorf("读取显示器信息失败: %w", err)
		}
		return buildDisplaysContext("windows", layouts), nil
	case toolTypeText:
		if available, reason := windowsTypeTextAvailable(); !available {
			return nil, fmt.Errorf("输入文本失败: %s", reason)
		}
		return windowsTypeText(ctx, arguments)
	case toolKeyPress:
		if available, reason := windowsKeyPressAvailable(); !available {
			return nil, fmt.Errorf("发送按键失败: %s", reason)
		}
		return windowsKeyPress(ctx, arguments)
	default:
		return nil, fmt.Errorf("当前平台 %s 仅实现 displays_context、type_text 和 key_press，暂不支持 %s", platformLabel("windows"), toolName)
	}
}

func buildDisplaysContext(platform string, layouts []displayLayout) map[string]any {
	items := make([]map[string]any, 0, len(layouts))
	for index, layout := range layouts {
		item := map[string]any{
			"display_id":       fallbackString(strings.TrimSpace(layout.DisplayID), strconv.Itoa(index+1)),
			"display_index":    fallbackInt(layout.DisplayIndex, index+1),
			"name":             fallbackString(strings.TrimSpace(layout.DisplayID), fmt.Sprintf("Display %d", index+1)),
			"width_px":         displayPixelWidth(layout),
			"height_px":        displayPixelHeight(layout),
			"logical_width":    displayLogicalWidth(layout),
			"logical_height":   displayLogicalHeight(layout),
			"refresh_hz":       0.0,
			"scale_factor":     displayScaleFactor(layout),
			"primary":          layout.Primary,
			"online":           true,
			"mirrored":         false,
			"connection_type":  "",
			"display_type":     "",
			"rotation_degrees": 0,
		}
		applyDisplayLayout(item, layout)
		items = append(items, item)
	}
	return map[string]any{
		"platform":         platform,
		"displays":         items,
		"count":            len(items),
		"coordinate_space": "global_screen",
	}
}

func fallbackInt(value int, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func platformLabel(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		if strings.TrimSpace(platform) == "" {
			return "unknown"
		}
		return strings.TrimSpace(platform)
	}
}

func diagnoseDarwin() Status {
	capabilities := allCapabilities()
	lookup := map[string]*Capability{}
	for index := range capabilities {
		lookup[capabilities[index].ToolName] = &capabilities[index]
	}

	if _, err := exec.LookPath("system_profiler"); err == nil {
		lookup[toolDisplaysContext].Available = true
	} else {
		lookup[toolDisplaysContext].Reason = "未找到 system_profiler"
	}
	if _, err := exec.LookPath("screencapture"); err == nil {
		lookup[toolScreenshot].Available = true
	} else {
		lookup[toolScreenshot].Reason = "未找到 screencapture"
	}

	accessibilityError := darwinAccessibilityError()
	if accessibilityError == "" {
		lookup[toolWindowsContext].Available = true
		lookup[toolFocusWindow].Available = true
		lookup[toolKeyPress].Available = true
		lookup[toolTypeText].Available = true
	} else {
		lookup[toolWindowsContext].Reason = accessibilityError
		lookup[toolFocusWindow].Reason = accessibilityError
		lookup[toolKeyPress].Reason = accessibilityError
		lookup[toolTypeText].Reason = accessibilityError
	}
	pointerInputReason := darwinPointerInputError(accessibilityError)
	if pointerInputReason == "" {
		lookup[toolMouseMove].Available = true
		lookup[toolMouseClick].Available = true
		lookup[toolMouseDrag].Available = true
		lookup[toolScroll].Available = true
	} else {
		lookup[toolMouseMove].Reason = pointerInputReason
		lookup[toolMouseClick].Reason = pointerInputReason
		lookup[toolMouseDrag].Reason = pointerInputReason
		lookup[toolScroll].Reason = pointerInputReason
	}

	readable := []string{}
	inputable := []string{}
	for _, item := range capabilities {
		if !item.Available {
			continue
		}
		if item.Category == "read" {
			readable = append(readable, strings.TrimPrefix(item.ToolName, "taiyiflow-computer__"))
		} else {
			inputable = append(inputable, strings.TrimPrefix(item.ToolName, "taiyiflow-computer__"))
		}
	}
	parts := []string{"平台: macOS"}
	if len(readable) > 0 {
		parts = append(parts, fmt.Sprintf("可用读取: %s", strings.Join(readable, ",")))
	} else {
		parts = append(parts, "可用读取: <none>")
	}
	if len(inputable) > 0 {
		parts = append(parts, fmt.Sprintf("可用输入: %s", strings.Join(inputable, ",")))
	} else {
		parts = append(parts, "可用输入: <none>")
	}
	if accessibilityError != "" {
		parts = append(parts, "辅助功能: 未授权")
	}
	return Status{
		Platform:       "darwin",
		Available:      len(readable) > 0 || len(inputable) > 0,
		ReadAvailable:  len(readable) > 0,
		InputAvailable: len(inputable) > 0,
		Message:        strings.Join(parts, " | "),
		Capabilities:   capabilities,
	}
}

func executeDarwinTool(ctx context.Context, toolName string, arguments map[string]any) (map[string]any, error) {
	switch strings.TrimSpace(toolName) {
	case toolDisplaysContext:
		return darwinDisplaysContext(ctx)
	case toolWindowsContext:
		if reason := darwinAccessibilityError(); reason != "" {
			return nil, fmt.Errorf("读取窗口上下文失败: %s", reason)
		}
		return darwinWindowsContext(ctx)
	case toolScreenshot:
		return darwinScreenshot(ctx, arguments)
	case toolFocusWindow:
		if reason := darwinAccessibilityError(); reason != "" {
			return nil, fmt.Errorf("切换窗口焦点失败: %s", reason)
		}
		return darwinFocusWindow(ctx, arguments)
	case toolKeyPress:
		if reason := darwinAccessibilityError(); reason != "" {
			return nil, fmt.Errorf("发送按键失败: %s", reason)
		}
		return darwinKeyPress(ctx, arguments)
	case toolTypeText:
		if reason := darwinAccessibilityError(); reason != "" {
			return nil, fmt.Errorf("输入文本失败: %s", reason)
		}
		return darwinTypeText(ctx, arguments)
	case toolMouseMove:
		if reason := darwinPointerInputError(darwinAccessibilityError()); reason != "" {
			return nil, fmt.Errorf("移动鼠标失败: %s", reason)
		}
		return darwinMouseMove(ctx, arguments)
	case toolMouseClick:
		if reason := darwinPointerInputError(darwinAccessibilityError()); reason != "" {
			return nil, fmt.Errorf("执行鼠标点击失败: %s", reason)
		}
		return darwinMouseClick(ctx, arguments)
	case toolMouseDrag:
		if reason := darwinPointerInputError(darwinAccessibilityError()); reason != "" {
			return nil, fmt.Errorf("执行鼠标拖拽失败: %s", reason)
		}
		return darwinMouseDrag(ctx, arguments)
	case toolScroll:
		if reason := darwinPointerInputError(darwinAccessibilityError()); reason != "" {
			return nil, fmt.Errorf("执行滚轮滚动失败: %s", reason)
		}
		return darwinScroll(ctx, arguments)
	default:
		return nil, fmt.Errorf("不支持的桌面操作工具: %s", toolName)
	}
}

func darwinPointerInputError(accessibilityError string) string {
	if strings.TrimSpace(accessibilityError) != "" {
		return strings.TrimSpace(accessibilityError)
	}
	available, reason := darwinPointerInputAvailability()
	if available {
		return ""
	}
	if strings.TrimSpace(reason) == "" {
		return "当前构建未启用桌面鼠标与滚轮注入"
	}
	return strings.TrimSpace(reason)
}

func darwinMouseMove(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	x, y, _, err := resolveCoordinatePair(arguments, "x", "y", true)
	if err != nil {
		return nil, err
	}
	layouts, err := displayLayoutsIfNeeded(arguments)
	if err != nil {
		return nil, err
	}
	point, err := translateScreenPoint(arguments, x, y, layouts)
	if err != nil {
		return nil, err
	}
	if err := darwinPostMouseMoveFunc(point.X, point.Y); err != nil {
		return nil, err
	}
	result := map[string]any{
		"platform":         "darwin",
		"x":                point.X,
		"y":                point.Y,
		"coordinate_space": "global_screen",
	}
	applyResolvedScreenPoint(result, arguments, point)
	return result, nil
}

func darwinMouseClick(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	x, y, hasPosition, err := resolveCoordinatePair(arguments, "x", "y", false)
	if err != nil {
		return nil, err
	}
	point := resolvedScreenPoint{X: x, Y: y, InputCoordinateSpace: "global_screen"}
	if !hasPosition {
		x, y, err = darwinCurrentMousePositionFunc()
		if err != nil {
			return nil, err
		}
		point.X = x
		point.Y = y
	} else {
		layouts, layoutErr := displayLayoutsIfNeeded(arguments)
		if layoutErr != nil {
			return nil, layoutErr
		}
		point, err = translateScreenPoint(arguments, x, y, layouts)
		if err != nil {
			return nil, err
		}
		x = point.X
		y = point.Y
		if err := darwinPostMouseMoveFunc(x, y); err != nil {
			return nil, err
		}
		if err := darwinSettleMouseBeforeClick(ctx); err != nil {
			return nil, err
		}
	}
	buttonCode, buttonLabel, err := resolveMouseButton(arguments)
	if err != nil {
		return nil, err
	}
	clickCount := clampInt(intValue(arguments["click_count"], 1), 1, 3)
	for current := 1; current <= clickCount; current++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := darwinPostMouseButtonEventFunc(mouseEventDown, buttonCode, x, y, current); err != nil {
			return nil, err
		}
		if err := darwinPostMouseButtonEventFunc(mouseEventUp, buttonCode, x, y, current); err != nil {
			return nil, err
		}
	}
	result := map[string]any{
		"platform":         "darwin",
		"x":                x,
		"y":                y,
		"button":           buttonLabel,
		"click_count":      clickCount,
		"coordinate_space": "global_screen",
	}
	applyResolvedScreenPoint(result, arguments, point)
	return result, nil
}

func darwinMouseDrag(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fromX, fromY, _, err := resolveCoordinatePair(arguments, "from_x", "from_y", true)
	if err != nil {
		return nil, err
	}
	toX, toY, _, err := resolveCoordinatePair(arguments, "to_x", "to_y", true)
	if err != nil {
		return nil, err
	}
	layouts, err := displayLayoutsIfNeeded(arguments)
	if err != nil {
		return nil, err
	}
	fromPoint, err := translateScreenPoint(arguments, fromX, fromY, layouts)
	if err != nil {
		return nil, err
	}
	toPoint, err := translateScreenPoint(arguments, toX, toY, layouts)
	if err != nil {
		return nil, err
	}
	fromX = fromPoint.X
	fromY = fromPoint.Y
	toX = toPoint.X
	toY = toPoint.Y
	buttonCode, buttonLabel, err := resolveMouseButton(arguments)
	if err != nil {
		return nil, err
	}
	durationMS := clampInt(intValue(arguments["duration_ms"], 0), 0, 10000)
	steps := 1
	if durationMS > 0 {
		steps = clampInt(durationMS/16, 1, 120)
	}
	if err := darwinPostMouseMove(fromX, fromY); err != nil {
		return nil, err
	}
	if err := darwinPostMouseButtonEvent(mouseEventDown, buttonCode, fromX, fromY, 1); err != nil {
		return nil, err
	}
	sleepInterval := time.Duration(0)
	if durationMS > 0 && steps > 1 {
		sleepInterval = time.Duration(durationMS) * time.Millisecond / time.Duration(steps)
	}
	for step := 1; step <= steps; step++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		x := interpolateInt(fromX, toX, step, steps)
		y := interpolateInt(fromY, toY, step, steps)
		if err := darwinPostMouseButtonEvent(mouseEventDragged, buttonCode, x, y, 1); err != nil {
			return nil, err
		}
		if sleepInterval > 0 && step < steps {
			time.Sleep(sleepInterval)
		}
	}
	if err := darwinPostMouseButtonEvent(mouseEventUp, buttonCode, toX, toY, 1); err != nil {
		return nil, err
	}
	result := map[string]any{
		"platform":         "darwin",
		"from_x":           fromX,
		"from_y":           fromY,
		"to_x":             toX,
		"to_y":             toY,
		"button":           buttonLabel,
		"duration_ms":      durationMS,
		"coordinate_space": "global_screen",
	}
	applyResolvedScreenPoint(result, arguments, fromPoint)
	return result, nil
}

func darwinScroll(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deltaX, _, err := strictIntArgument(arguments, "delta_x")
	if err != nil {
		return nil, err
	}
	deltaY, _, err := strictIntArgument(arguments, "delta_y")
	if err != nil {
		return nil, err
	}
	if deltaX == 0 && deltaY == 0 {
		return nil, fmt.Errorf("scroll 至少需要一个非零滚动量")
	}
	if err := darwinPostScrollEvent(deltaX, -deltaY); err != nil {
		return nil, err
	}
	return map[string]any{
		"platform":         "darwin",
		"delta_x":          deltaX,
		"delta_y":          deltaY,
		"scroll_unit":      "line",
		"coordinate_space": "global_screen",
	}, nil
}

type darwinDisplaysPayload struct {
	DisplaysData []darwinDisplayGPU `json:"SPDisplaysDataType"`
}

type darwinDisplayGPU struct {
	Displays []darwinDisplay `json:"spdisplays_ndrvs"`
}

type darwinDisplay struct {
	Name            string `json:"_name"`
	DisplayID       string `json:"_spdisplays_displayID"`
	Pixels          string `json:"_spdisplays_pixels"`
	Resolution      string `json:"_spdisplays_resolution"`
	PixelResolution string `json:"spdisplays_pixelresolution"`
	Main            string `json:"spdisplays_main"`
	Online          string `json:"spdisplays_online"`
	Mirror          string `json:"spdisplays_mirror"`
	ConnectionType  string `json:"spdisplays_connection_type"`
	DisplayType     string `json:"spdisplays_display_type"`
	Rotation        any    `json:"spdisplays_rotation"`
}

type darwinWindowResult struct {
	Windows []map[string]any `json:"windows"`
}

func darwinDisplaysContext(ctx context.Context) (map[string]any, error) {
	commandCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	output, err := exec.CommandContext(commandCtx, "system_profiler", "SPDisplaysDataType", "-json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("读取显示器信息失败: %w", summarizeCommandError(err, output))
	}
	payload := darwinDisplaysPayload{}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("解析显示器信息失败: %w", err)
	}
	layouts, layoutErr := platformDisplayLayouts()
	items := []map[string]any{}
	index := 0
	for _, gpu := range payload.DisplaysData {
		for _, display := range gpu.Displays {
			index++
			physicalWidth, physicalHeight := parseWidthHeight(display.Pixels)
			logicalWidth, logicalHeight, refreshHz := parseDisplayResolution(display.Resolution)
			if logicalWidth == 0 || logicalHeight == 0 {
				logicalWidth, logicalHeight = parseWidthHeight(display.PixelResolution)
			}
			scaleFactor := 1.0
			if physicalWidth > 0 && logicalWidth > 0 {
				scaleFactor = float64(physicalWidth) / float64(logicalWidth)
			}
			item := map[string]any{
				"display_id":       fallbackString(strings.TrimSpace(display.DisplayID), strconv.Itoa(index)),
				"display_index":    index,
				"name":             strings.TrimSpace(display.Name),
				"width_px":         physicalWidth,
				"height_px":        physicalHeight,
				"logical_width":    logicalWidth,
				"logical_height":   logicalHeight,
				"refresh_hz":       refreshHz,
				"scale_factor":     scaleFactor,
				"primary":          strings.TrimSpace(display.Main) == "spdisplays_yes",
				"online":           strings.TrimSpace(display.Online) == "spdisplays_yes",
				"mirrored":         strings.TrimSpace(display.Mirror) == "spdisplays_on",
				"connection_type":  strings.TrimSpace(display.ConnectionType),
				"display_type":     strings.TrimSpace(display.DisplayType),
				"rotation_degrees": parseRotation(display.Rotation),
			}
			if layoutErr == nil {
				if layout, ok := findDisplayLayout(layouts, strings.TrimSpace(display.DisplayID), index); ok {
					applyDisplayLayout(item, layout)
				}
			}
			items = append(items, item)
		}
	}
	if len(items) > 1 {
		sort.SliceStable(items, func(left int, right int) bool {
			return intValue(items[left]["display_index"], left+1) < intValue(items[right]["display_index"], right+1)
		})
	}
	return map[string]any{
		"platform":         "darwin",
		"displays":         items,
		"count":            len(items),
		"coordinate_space": "global_screen",
	}, nil
}

func darwinWindowsContext(ctx context.Context) (map[string]any, error) {
	script := `var se = Application('System Events');
var procs = se.processes.whose({backgroundOnly: false})();
var items = [];
for (var i = 0; i < procs.length; i++) {
  var proc = procs[i];
  var procName = proc.name();
  var procPID = proc.unixId();
  var frontmost = proc.frontmost();
  var procWindows = proc.windows();
  for (var j = 0; j < procWindows.length; j++) {
    var w = procWindows[j];
    var pos = w.position();
    var size = w.size();
    items.push({
      window_id: String(procPID) + ':' + String(j + 1),
      process_name: procName,
      process_id: procPID,
      window_index: j + 1,
      title: w.name(),
      frontmost: frontmost,
      x: Number(pos[0]),
      y: Number(pos[1]),
      width: Number(size[0]),
      height: Number(size[1])
    });
  }
}
JSON.stringify({windows: items});`
	output, err := runOSAScriptJXA(ctx, script)
	if err != nil {
		return nil, err
	}
	payload := darwinWindowResult{}
	if err := json.Unmarshal(output, &payload); err != nil {
		return nil, fmt.Errorf("解析窗口上下文失败: %w", err)
	}
	return map[string]any{
		"platform": "darwin",
		"windows":  payload.Windows,
		"count":    len(payload.Windows),
	}, nil
}

func darwinScreenshot(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	path := strings.TrimSpace(stringValue(arguments["path"]))
	if path == "" {
		return nil, fmt.Errorf("screenshot 缺少 path")
	}
	displaySelector := strings.TrimSpace(firstString(arguments, "display_index", "display_id"))
	displayID := strings.TrimSpace(firstString(arguments, "display_id"))
	displayIndex := intValue(arguments["display_index"], 0)
	commandCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建截图目录失败: %w", err)
	}
	args := []string{"-x"}
	if displaySelector != "" {
		args = append(args, "-D", displaySelector)
	}
	args = append(args, path)
	output, err := exec.CommandContext(commandCtx, "screencapture", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("截取桌面截图失败: %w", summarizeCommandError(err, output))
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("读取截图文件失败: %w", err)
	}
	result := map[string]any{
		"platform":     "darwin",
		"path":         path,
		"size":         int(info.Size()),
		"content_type": "image/png",
	}
	if displaySelector != "" {
		if displayID == "" {
			displayID = displaySelector
		}
		result["display_id"] = displayID
		if displayIndex > 0 {
			result["display_index"] = displayIndex
		}
	}
	screenshotWidth, screenshotHeight, screenshotErr := readPNGDimensions(path)
	if screenshotErr == nil && screenshotWidth > 0 && screenshotHeight > 0 {
		result["screenshot_width_px"] = screenshotWidth
		result["screenshot_height_px"] = screenshotHeight
	}
	if layouts, layoutErr := platformDisplayLayouts(); layoutErr == nil {
		if displaySelector != "" {
			if layout, ok := findDisplayLayout(layouts, displayID, displayIndex); ok {
				applyDisplayScope(result, layout)
				if screenshotWidth > 0 && screenshotHeight > 0 {
					cacheDisplayScreenshotDimensions(layout.DisplayID, layout.DisplayIndex, screenshotWidth, screenshotHeight)
				}
			}
		} else if len(layouts) == 1 && screenshotWidth > 0 && screenshotHeight > 0 {
			cacheDisplayScreenshotDimensions(layouts[0].DisplayID, layouts[0].DisplayIndex, screenshotWidth, screenshotHeight)
		}
	}
	return result, nil
}

func darwinFocusWindow(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	target, err := resolveDarwinProcessTarget(arguments)
	if err != nil {
		return nil, err
	}
	script := buildDarwinFocusScript(target)
	if _, err := runOSAScript(ctx, script); err != nil {
		return nil, err
	}
	result := map[string]any{"platform": "darwin", "focused": true}
	for key, value := range target {
		result[key] = value
	}
	return result, nil
}

func darwinTypeText(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	text := stringValue(arguments["text"])
	if text == "" {
		return nil, fmt.Errorf("type_text 缺少 text")
	}
	lines := splitTypedText(text)
	pressEnter := boolValue(arguments["press_enter"])
	restoreClipboard := boolValue(arguments["restore_clipboard"])
	inputMethod := "cg_event_unicode"
	pasteBackend := ""
	clipboardRestoreScope := "not_used"
	if shouldDarwinUseClipboardTextInput(text, darwinUnicodeTextInputAvailable()) {
		inputMethod = "clipboard_paste"
		backend, restoreScope, err := darwinPasteText(ctx, text, restoreClipboard)
		if err != nil {
			return nil, err
		}
		pasteBackend = backend
		clipboardRestoreScope = restoreScope
		if pressEnter {
			if err := darwinPostKeyCode(36); err != nil {
				return nil, err
			}
		}
	} else if darwinUnicodeTextInputAvailable() {
		for index, line := range lines {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if line != "" {
				if err := darwinPostUnicodeText(line); err != nil {
					return nil, err
				}
			}
			if index < len(lines)-1 {
				if err := darwinPostKeyCode(36); err != nil {
					return nil, err
				}
			}
		}
		if pressEnter {
			if err := darwinPostKeyCode(36); err != nil {
				return nil, err
			}
		}
	} else {
		inputMethod = "applescript_keystroke"
		if _, err := runOSAScript(ctx, buildDarwinTypeTextScript(text, pressEnter)); err != nil {
			return nil, err
		}
	}
	result := map[string]any{
		"platform":     "darwin",
		"text_length":  utf8.RuneCountInString(text),
		"line_count":   len(lines),
		"press_enter":  pressEnter,
		"input_method": inputMethod,
	}
	if pasteBackend != "" {
		result["paste_backend"] = pasteBackend
		result["clipboard_backend"] = "nspasteboard"
		result["restore_clipboard"] = restoreClipboard
		result["clipboard_restore_scope"] = clipboardRestoreScope
	}
	return result, nil
}

func shouldDarwinUseClipboardTextInput(text string, unicodeInputAvailable bool) bool {
	if requiresDarwinClipboardTextInput(text) {
		return true
	}
	if !unicodeInputAvailable {
		return true
	}
	return false
}

func requiresDarwinClipboardTextInput(text string) bool {
	return len(text) != utf8.RuneCountInString(text)
}

func waitForDarwinInputSettle(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func darwinPasteText(ctx context.Context, text string, restoreClipboard bool) (string, string, error) {
	var previous []byte
	if restoreClipboard {
		captured, err := darwinCaptureClipboard()
		if err != nil {
			return "", "", err
		}
		previous = captured
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	if err := darwinSetClipboardText(text); err != nil {
		return "", "", err
	}
	if err := waitForDarwinInputSettle(ctx, 90*time.Millisecond); err != nil {
		return "", "", err
	}
	pasteBackend, pasteErr := darwinTriggerPasteShortcut(ctx)
	if pasteErr != nil {
		return "", "", pasteErr
	}
	if !restoreClipboard {
		return pasteBackend, "not_restored", nil
	}
	if err := waitForDarwinInputSettle(ctx, 1200*time.Millisecond); err != nil {
		return "", "", err
	}
	restoreErr := darwinRestoreClipboard(previous)
	if restoreErr != nil {
		return "", "", restoreErr
	}
	return pasteBackend, "full_snapshot", nil
}

func darwinTriggerPasteShortcut(ctx context.Context) (string, error) {
	if err := darwinPostCommandVShortcut(); err == nil {
		if err := waitForDarwinInputSettle(ctx, 320*time.Millisecond); err != nil {
			return "", err
		}
		return "cg_event_command_v", nil
	}
	if _, err := runOSAScript(ctx, buildDarwinPasteShortcutScript()); err != nil {
		return "", fmt.Errorf("执行系统粘贴失败: %w", err)
	}
	return "applescript_command_v", nil
}

func buildDarwinPasteShortcutScript() string {
	return "tell application \"System Events\"\n  key code 9 using command down\n  delay 0.35\nend tell"
}

func buildDarwinTypeTextScript(text string, pressEnter bool) string {
	lines := splitTypedText(text)
	scriptLines := []string{"tell application \"System Events\""}
	for index, line := range lines {
		if line != "" {
			scriptLines = append(scriptLines, fmt.Sprintf("  keystroke %s", appleScriptString(line)))
		}
		if index < len(lines)-1 {
			scriptLines = append(scriptLines, "  key code 36")
		}
	}
	if pressEnter {
		scriptLines = append(scriptLines, "  key code 36")
	}
	scriptLines = append(scriptLines, "end tell")
	return strings.Join(scriptLines, "\n")
}

func darwinKeyPress(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	key := strings.TrimSpace(firstString(arguments, "key", "keys"))
	if key == "" {
		return nil, fmt.Errorf("key_press 缺少 key")
	}
	repeatCount := intValue(arguments["repeat"], 1)
	if repeatCount < 1 {
		repeatCount = 1
	}
	modifierState, err := darwinModifierStateFromValue(arguments["modifiers"])
	if err != nil {
		return nil, err
	}
	modifierClause := appleScriptModifierClause(modifierState)
	if fallbackText, fallback, err := darwinKeyPressTextFallback(key, modifierClause, repeatCount); err != nil {
		return nil, err
	} else if fallback {
		if _, err := darwinTypeTextFunc(ctx, map[string]any{"text": fallbackText}); err != nil {
			return nil, err
		}
		return map[string]any{
			"platform":      "darwin",
			"key":           key,
			"repeat":        repeatCount,
			"modifiers":     modifierState.Labels,
			"text_length":   utf8.RuneCountInString(fallbackText),
			"fallback_mode": "type_text",
		}, nil
	}
	if keyCode, ok := darwinKeyCodeForShortcut(key); ok && (modifierState.HasAny() || darwinKeyPressUsesNamedKey(key)) {
		if err := darwinPostKeyCodeWithModifiers(ctx, keyCode, modifierState, repeatCount); err == nil {
			return map[string]any{
				"platform":     "darwin",
				"key":          key,
				"repeat":       repeatCount,
				"modifiers":    modifierState.Labels,
				"input_method": "cg_event_key_code",
			}, nil
		}
	}
	command, err := appleScriptKeyCommand(key, modifierClause)
	if err != nil {
		return nil, err
	}
	scriptLines := []string{"tell application \"System Events\"", fmt.Sprintf("  repeat %d times", repeatCount), "    " + command, "  end repeat", "end tell"}
	if _, err := runOSAScript(ctx, strings.Join(scriptLines, "\n")); err != nil {
		return nil, err
	}
	if modifierState.HasAny() {
		if err := darwinSettleAfterShortcut(ctx); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"platform":     "darwin",
		"key":          key,
		"repeat":       repeatCount,
		"modifiers":    modifierState.Labels,
		"input_method": "applescript_key",
	}, nil
}

type darwinModifierState struct {
	Command bool
	Shift   bool
	Option  bool
	Control bool
	Labels  []string
}

func (state darwinModifierState) HasAny() bool {
	return state.Command || state.Shift || state.Option || state.Control
}

func darwinModifierStateFromValue(value any) (darwinModifierState, error) {
	items := stringSlice(value)
	state := darwinModifierState{Labels: []string{}}
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "command", "cmd", "meta":
			if !state.Command {
				state.Command = true
				state.Labels = append(state.Labels, "command")
			}
		case "shift":
			if !state.Shift {
				state.Shift = true
				state.Labels = append(state.Labels, "shift")
			}
		case "option", "alt":
			if !state.Option {
				state.Option = true
				state.Labels = append(state.Labels, "option")
			}
		case "control", "ctrl":
			if !state.Control {
				state.Control = true
				state.Labels = append(state.Labels, "control")
			}
		default:
			return darwinModifierState{}, fmt.Errorf("不支持的修饰键: %s", item)
		}
	}
	return state, nil
}

func appleScriptModifierClause(state darwinModifierState) string {
	if !state.HasAny() {
		return ""
	}
	mapped := make([]string, 0, len(state.Labels))
	for _, label := range state.Labels {
		switch label {
		case "command":
			mapped = append(mapped, "command down")
		case "shift":
			mapped = append(mapped, "shift down")
		case "option":
			mapped = append(mapped, "option down")
		case "control":
			mapped = append(mapped, "control down")
		}
	}
	return "{" + strings.Join(mapped, ", ") + "}"
}

func darwinPostKeyCodeWithModifiers(ctx context.Context, keyCode int, modifierState darwinModifierState, repeatCount int) error {
	if repeatCount < 1 {
		repeatCount = 1
	}
	for current := 0; current < repeatCount; current++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if modifierState.HasAny() {
			if err := darwinPostModifiedKeyCodeFunc(keyCode, modifierState.Command, modifierState.Shift, modifierState.Option, modifierState.Control); err != nil {
				return err
			}
			if err := darwinSettleAfterShortcut(ctx); err != nil {
				return err
			}
			continue
		}
		if err := darwinPostKeyCode(keyCode); err != nil {
			return err
		}
	}
	return nil
}

func darwinKeyPressTextFallback(key string, modifierClause string, repeatCount int) (string, bool, error) {
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		return "", false, nil
	}
	if darwinKeyPressUsesNamedKey(trimmedKey) {
		return "", false, nil
	}
	keyRunes := []rune(trimmedKey)
	fallbackText := trimmedKey
	if len(keyRunes) == 1 && keyRunes[0] <= utf8.RuneSelf {
		return "", false, nil
	}
	if strings.TrimSpace(modifierClause) != "" {
		return "", false, fmt.Errorf("key_press 不支持带修饰键的非 ASCII 文本，请改用 type_text")
	}
	if repeatCount < 1 {
		repeatCount = 1
	}
	return strings.Repeat(fallbackText, repeatCount), true, nil
}

func darwinKeyPressUsesNamedKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return false
	}
	switch normalized {
	case "enter", "return", "tab", "escape", "esc", "space", "backspace", "delete", "left", "right", "down", "up":
		return true
	}
	if strings.HasPrefix(normalized, "f") {
		if len(normalized) >= 2 && len(normalized) <= 3 {
			return true
		}
	}
	return false
}

func darwinKeyCodeForShortcut(key string) (int, bool) {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return 0, false
	}
	namedCodes := map[string]int{
		"enter":     36,
		"return":    36,
		"tab":       48,
		"escape":    53,
		"esc":       53,
		"space":     49,
		"backspace": 51,
		"delete":    51,
		"left":      123,
		"right":     124,
		"down":      125,
		"up":        126,
		"f1":        122,
		"f2":        120,
		"f3":        99,
		"f4":        118,
		"f5":        96,
		"f6":        97,
		"f7":        98,
		"f8":        100,
		"f9":        101,
		"f10":       109,
		"f11":       103,
		"f12":       111,
	}
	if code, ok := namedCodes[normalized]; ok {
		return code, true
	}
	if len([]rune(normalized)) != 1 {
		return 0, false
	}
	ansiCodes := map[rune]int{
		'a': 0, 's': 1, 'd': 2, 'f': 3, 'h': 4, 'g': 5, 'z': 6, 'x': 7,
		'c': 8, 'v': 9, 'b': 11, 'q': 12, 'w': 13, 'e': 14, 'r': 15, 'y': 16,
		't': 17, '1': 18, '2': 19, '3': 20, '4': 21, '6': 22, '5': 23,
		'=': 24, '9': 25, '7': 26, '-': 27, '8': 28, '0': 29, ']': 30,
		'o': 31, 'u': 32, '[': 33, 'i': 34, 'p': 35, 'l': 37, 'j': 38,
		'\'': 39, 'k': 40, ';': 41, '\\': 42, ',': 43, '/': 44, 'n': 45,
		'm': 46, '.': 47, '`': 50,
	}
	code, ok := ansiCodes[[]rune(normalized)[0]]
	return code, ok
}

func darwinAccessibilityError() string {
	if _, err := exec.LookPath("osascript"); err != nil {
		return "未找到 osascript"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	script := `var se = Application('System Events');
try {
  var procs = se.processes.whose({backgroundOnly: false})();
  if (procs.length > 0) {
    var ws = procs[0].windows();
    if (ws.length > 0) {
      ws[0].name();
    }
  }
  JSON.stringify({ok: true});
} catch (e) {
  JSON.stringify({ok: false, error: String(e)});
}`
	output, err := runOSAScriptJXA(ctx, script)
	if err != nil {
		return err.Error()
	}
	var payload struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(output, &payload); err != nil {
		return fmt.Sprintf("解析辅助功能状态失败: %v", err)
	}
	if payload.OK {
		return ""
	}
	return strings.TrimSpace(payload.Error)
}

func runOSAScript(ctx context.Context, script string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, "osascript")
	command.Stdin = strings.NewReader(script)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("执行 osascript 失败: %w", summarizeCommandError(err, output))
	}
	return output, nil
}

func runOSAScriptJXA(ctx context.Context, script string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, "osascript", "-l", "JavaScript")
	command.Stdin = strings.NewReader(script)
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("执行 JXA 失败: %w", summarizeCommandError(err, output))
	}
	return output, nil
}

func resolveDarwinProcessTarget(arguments map[string]any) (map[string]any, error) {
	if windowID := strings.TrimSpace(stringValue(arguments["window_id"])); windowID != "" {
		parts := strings.Split(windowID, ":")
		if len(parts) >= 1 && strings.TrimSpace(parts[0]) != "" {
			if pid, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
				return map[string]any{"window_id": windowID, "process_id": pid}, nil
			}
		}
	}
	if processID := intValue(arguments["process_id"], 0); processID > 0 {
		return map[string]any{"process_id": processID}, nil
	}
	if processName := strings.TrimSpace(firstString(arguments, "process_name", "app_name")); processName != "" {
		return map[string]any{"process_name": processName}, nil
	}
	return nil, fmt.Errorf("focus_window 需要 window_id、process_id 或 process_name")
}

func buildDarwinFocusScript(target map[string]any) string {
	if processID := intValue(target["process_id"], 0); processID > 0 {
		return fmt.Sprintf("tell application \"System Events\"\n  tell first process whose unix id is %d\n    set frontmost to true\n  end tell\nend tell", processID)
	}
	return fmt.Sprintf("tell application \"System Events\"\n  tell process %s\n    set frontmost to true\n  end tell\nend tell", appleScriptString(stringValue(target["process_name"])))
}

func appleScriptKeyCommand(key string, modifierClause string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(key))
	if normalized == "" {
		return "", fmt.Errorf("key_press 缺少 key")
	}
	keyCodes := map[string]int{
		"enter":     36,
		"return":    36,
		"tab":       48,
		"escape":    53,
		"esc":       53,
		"space":     49,
		"backspace": 51,
		"delete":    51,
		"left":      123,
		"right":     124,
		"down":      125,
		"up":        126,
	}
	if code, ok := keyCodes[normalized]; ok {
		if modifierClause != "" {
			return fmt.Sprintf("key code %d using %s", code, modifierClause), nil
		}
		return fmt.Sprintf("key code %d", code), nil
	}
	if len([]rune(key)) != 1 {
		return "", fmt.Errorf("key_press 暂仅支持单字符或常见特殊键")
	}
	if modifierClause != "" {
		return fmt.Sprintf("keystroke %s using %s", appleScriptString(key), modifierClause), nil
	}
	return fmt.Sprintf("keystroke %s", appleScriptString(key)), nil
}

func appleScriptModifiers(value any) (string, error) {
	items := stringSlice(value)
	if len(items) == 0 {
		return "", nil
	}
	mapped := make([]string, 0, len(items))
	for _, item := range items {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "command", "cmd", "meta":
			mapped = append(mapped, "command down")
		case "shift":
			mapped = append(mapped, "shift down")
		case "option", "alt":
			mapped = append(mapped, "option down")
		case "control", "ctrl":
			mapped = append(mapped, "control down")
		default:
			return "", fmt.Errorf("不支持的修饰键: %s", item)
		}
	}
	return "{" + strings.Join(mapped, ", ") + "}", nil
}

func appleScriptString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}

func splitTypedText(value string) []string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	return strings.Split(normalized, "\n")
}

func parseWidthHeight(value string) (int, int) {
	parts := strings.Split(value, "x")
	if len(parts) != 2 {
		return 0, 0
	}
	left, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	rightText := strings.TrimSpace(parts[1])
	fields := strings.Fields(rightText)
	if len(fields) > 0 {
		rightText = fields[0]
	}
	right, _ := strconv.Atoi(strings.TrimSpace(rightText))
	return left, right
}

func parseDisplayResolution(value string) (int, int, float64) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, 0, 0
	}
	parts := strings.Split(trimmed, "@")
	width, height := parseWidthHeight(parts[0])
	refreshHz := 0.0
	if len(parts) > 1 {
		refreshText := strings.TrimSpace(strings.TrimSuffix(parts[1], "Hz"))
		refreshHz, _ = strconv.ParseFloat(refreshText, 64)
	}
	return width, height, refreshHz
}

func parseRotation(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return 0
}

type resolvedScreenPoint struct {
	X                    int
	Y                    int
	InputCoordinateSpace string
	Display              displayLayout
	HasDisplay           bool
}

func displayLayoutsIfNeeded(arguments map[string]any) ([]displayLayout, error) {
	if !usesDisplayLocalCoordinates(arguments) {
		return nil, nil
	}
	return platformDisplayLayouts()
}

func usesDisplayLocalCoordinates(arguments map[string]any) bool {
	coordinateSpace := strings.ToLower(strings.TrimSpace(firstString(arguments, "coordinate_space")))
	switch coordinateSpace {
	case "display_local":
		return true
	case "", "global_screen":
		return coordinateSpace == "" && strings.TrimSpace(firstString(arguments, "display_id", "display_index")) != ""
	default:
		return false
	}
}

func translateScreenPoint(arguments map[string]any, x int, y int, layouts []displayLayout) (resolvedScreenPoint, error) {
	coordinateSpace, err := resolveRequestedCoordinateSpace(arguments)
	if err != nil {
		return resolvedScreenPoint{}, err
	}
	point := resolvedScreenPoint{
		X:                    x,
		Y:                    y,
		InputCoordinateSpace: coordinateSpace,
	}
	if coordinateSpace != "display_local" {
		return point, nil
	}
	displaySpecifier := strings.TrimSpace(firstString(arguments, "display_id", "display_index"))
	if displaySpecifier == "" {
		return resolvedScreenPoint{}, fmt.Errorf("display_local 坐标需要 display_id 或 display_index")
	}
	layout, ok := findDisplayLayout(layouts, strings.TrimSpace(firstString(arguments, "display_id")), intValue(arguments["display_index"], 0))
	if !ok {
		return resolvedScreenPoint{}, fmt.Errorf("未找到显示器 %s", displaySpecifier)
	}
	pixelWidth, pixelHeight := resolveDisplayLocalPixelDimensions(arguments, layout)
	point.X = layout.OriginX + translateDisplayLocalCoordinate(x, displayLogicalWidth(layout), pixelWidth)
	point.Y = layout.OriginY + translateDisplayLocalCoordinate(y, displayLogicalHeight(layout), pixelHeight)
	point.Display = layout
	point.HasDisplay = true
	return point, nil
}

func resolveRequestedCoordinateSpace(arguments map[string]any) (string, error) {
	coordinateSpace := strings.ToLower(strings.TrimSpace(firstString(arguments, "coordinate_space")))
	switch coordinateSpace {
	case "":
		if strings.TrimSpace(firstString(arguments, "display_id", "display_index")) != "" {
			return "display_local", nil
		}
		return "global_screen", nil
	case "global_screen", "display_local":
		return coordinateSpace, nil
	default:
		return "", fmt.Errorf("coordinate_space 仅支持 global_screen 或 display_local")
	}
}

func findDisplayLayout(layouts []displayLayout, displayID string, displayIndex int) (displayLayout, bool) {
	trimmedID := strings.TrimSpace(displayID)
	if trimmedID != "" {
		for _, layout := range layouts {
			if strings.EqualFold(strings.TrimSpace(layout.DisplayID), trimmedID) {
				return layout, true
			}
		}
	}
	if displayIndex > 0 {
		for _, layout := range layouts {
			if layout.DisplayIndex == displayIndex {
				return layout, true
			}
		}
	}
	if trimmedID != "" {
		if parsedIndex, err := strconv.Atoi(trimmedID); err == nil && parsedIndex > 0 {
			for _, layout := range layouts {
				if layout.DisplayIndex == parsedIndex {
					return layout, true
				}
			}
		}
	}
	return displayLayout{}, false
}

func applyDisplayLayout(target map[string]any, layout displayLayout) {
	target["display_index"] = layout.DisplayIndex
	target["origin_x"] = layout.OriginX
	target["origin_y"] = layout.OriginY
	target["primary"] = layout.Primary
	target["coordinate_space"] = "global_screen"
}

func applyDisplayScope(target map[string]any, layout displayLayout) {
	target["display_index"] = layout.DisplayIndex
	target["display_origin_x"] = layout.OriginX
	target["display_origin_y"] = layout.OriginY
	target["display_width_px"] = displayPixelWidth(layout)
	target["display_height_px"] = displayPixelHeight(layout)
	target["display_logical_width"] = displayLogicalWidth(layout)
	target["display_logical_height"] = displayLogicalHeight(layout)
	target["display_scale_factor"] = displayScaleFactor(layout)
}

func applyResolvedScreenPoint(target map[string]any, arguments map[string]any, point resolvedScreenPoint) {
	target["input_coordinate_space"] = point.InputCoordinateSpace
	if displayID := strings.TrimSpace(stringValue(arguments["display_id"])); displayID != "" {
		target["display_id"] = displayID
	}
	if point.HasDisplay {
		applyDisplayScope(target, point.Display)
	}
}

func strictIntArgument(arguments map[string]any, key string) (int, bool, error) {
	value, ok := arguments[key]
	if !ok {
		return 0, false, nil
	}
	switch typed := value.(type) {
	case int:
		return typed, true, nil
	case int64:
		return int(typed), true, nil
	case float64:
		return int(typed), true, nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, true, fmt.Errorf("%s 必须是整数", key)
		}
		return parsed, true, nil
	default:
		return 0, true, fmt.Errorf("%s 必须是整数", key)
	}
}

func resolveCoordinatePair(arguments map[string]any, xKey string, yKey string, required bool) (int, int, bool, error) {
	x, hasX, err := strictIntArgument(arguments, xKey)
	if err != nil {
		return 0, 0, false, err
	}
	y, hasY, err := strictIntArgument(arguments, yKey)
	if err != nil {
		return 0, 0, false, err
	}
	if !hasX && !hasY {
		if required {
			return 0, 0, false, fmt.Errorf("%s 和 %s 需要同时提供", xKey, yKey)
		}
		return 0, 0, false, nil
	}
	if !hasX || !hasY {
		return 0, 0, false, fmt.Errorf("%s 和 %s 需要同时提供", xKey, yKey)
	}
	return x, y, true, nil
}

func resolveMouseButton(arguments map[string]any) (int, string, error) {
	button := strings.ToLower(strings.TrimSpace(firstString(arguments, "button")))
	switch button {
	case "", "left":
		return 0, "left", nil
	case "right":
		return 1, "right", nil
	case "middle", "center":
		return 2, "middle", nil
	default:
		return 0, "", fmt.Errorf("不支持的鼠标按键: %s", button)
	}
}

func clampInt(value int, minimum int, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func interpolateInt(from int, to int, step int, steps int) int {
	if steps <= 1 {
		return to
	}
	return from + ((to-from)*step)/steps
}

func summarizeCommandError(err error, output []byte) error {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, trimmed)
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func intValue(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(stringValue(item))
			if text != "" {
				items = append(items, text)
			}
		}
		return items
	default:
		return nil
	}
}

func firstString(arguments map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := arguments[key]; ok {
			if trimmed := strings.TrimSpace(stringValue(value)); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func fallbackString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
