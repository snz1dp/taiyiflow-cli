package computerbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

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

type Capability struct {
	ToolName   string `json:"tool_name"`
	Available  bool   `json:"available"`
	Category   string `json:"category,omitempty"`
	Reason     string `json:"reason,omitempty"`
	RiskTier   string `json:"risk_tier,omitempty"`
	Permission string `json:"permission,omitempty"`
}

type Status struct {
	Platform      string       `json:"platform"`
	Available     bool         `json:"available"`
	ReadAvailable bool         `json:"read_available"`
	InputAvailable bool        `json:"input_available"`
	Message       string       `json:"message"`
	Capabilities  []Capability `json:"capabilities,omitempty"`
}

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Diagnose(config internalconfig.ComputerConfig) Status {
	_ = config
	if runtime.GOOS != "darwin" {
		return unsupportedStatus()
	}
	return diagnoseDarwin()
}

func (m *Manager) Execute(ctx context.Context, config internalconfig.ComputerConfig, toolName string, arguments map[string]any) (map[string]any, error) {
	_ = config
	if runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("当前平台 %s 尚未实现桌面 ComputerUse", runtime.GOOS)
	}
	return executeDarwinTool(ctx, toolName, arguments)
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
		Platform:      platform,
		Available:     false,
		ReadAvailable: false,
		InputAvailable: false,
		Message:       fmt.Sprintf("平台: %s | 当前尚未实现桌面 ComputerUse 桥接", platform),
		Capabilities:  capabilities,
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
		Platform:      "darwin",
		Available:     len(readable) > 0 || len(inputable) > 0,
		ReadAvailable: len(readable) > 0,
		InputAvailable: len(inputable) > 0,
		Message:       strings.Join(parts, " | "),
		Capabilities:  capabilities,
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
		return nil, fmt.Errorf("不支持的桌面 ComputerUse 工具: %s", toolName)
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
	if err := darwinPostMouseMove(x, y); err != nil {
		return nil, err
	}
	return map[string]any{
		"platform":         "darwin",
		"x":                x,
		"y":                y,
		"coordinate_space": "global_screen",
	}, nil
}

func darwinMouseClick(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	x, y, hasPosition, err := resolveCoordinatePair(arguments, "x", "y", false)
	if err != nil {
		return nil, err
	}
	if !hasPosition {
		x, y, err = darwinCurrentMousePosition()
		if err != nil {
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
		if err := darwinPostMouseButtonEvent(mouseEventDown, buttonCode, x, y, current); err != nil {
			return nil, err
		}
		if err := darwinPostMouseButtonEvent(mouseEventUp, buttonCode, x, y, current); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"platform":         "darwin",
		"x":                x,
		"y":                y,
		"button":           buttonLabel,
		"click_count":      clickCount,
		"coordinate_space": "global_screen",
	}, nil
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
	return map[string]any{
		"platform":         "darwin",
		"from_x":           fromX,
		"from_y":           fromY,
		"to_x":             toX,
		"to_y":             toY,
		"button":           buttonLabel,
		"duration_ms":      durationMS,
		"coordinate_space": "global_screen",
	}, nil
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
	if err := darwinPostScrollEvent(deltaX, deltaY); err != nil {
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
	Name             string `json:"_name"`
	DisplayID        string `json:"_spdisplays_displayID"`
	Pixels           string `json:"_spdisplays_pixels"`
	Resolution       string `json:"_spdisplays_resolution"`
	PixelResolution  string `json:"spdisplays_pixelresolution"`
	Main             string `json:"spdisplays_main"`
	Online           string `json:"spdisplays_online"`
	Mirror           string `json:"spdisplays_mirror"`
	ConnectionType   string `json:"spdisplays_connection_type"`
	DisplayType      string `json:"spdisplays_display_type"`
	Rotation         any    `json:"spdisplays_rotation"`
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
				"display_id":      fallbackString(strings.TrimSpace(display.DisplayID), strconv.Itoa(index)),
				"display_index":   index,
				"name":            strings.TrimSpace(display.Name),
				"width_px":        physicalWidth,
				"height_px":       physicalHeight,
				"logical_width":   logicalWidth,
				"logical_height":  logicalHeight,
				"refresh_hz":      refreshHz,
				"scale_factor":    scaleFactor,
				"primary":         strings.TrimSpace(display.Main) == "spdisplays_yes",
				"online":          strings.TrimSpace(display.Online) == "spdisplays_yes",
				"mirrored":        strings.TrimSpace(display.Mirror) == "spdisplays_on",
				"connection_type": strings.TrimSpace(display.ConnectionType),
				"display_type":    strings.TrimSpace(display.DisplayType),
				"rotation_degrees": parseRotation(display.Rotation),
			}
			items = append(items, item)
		}
	}
	return map[string]any{
		"platform": "darwin",
		"displays": items,
		"count":    len(items),
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
	commandCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建截图目录失败: %w", err)
	}
	args := []string{"-x"}
	if displayID := strings.TrimSpace(firstString(arguments, "display_index", "display_id")); displayID != "" {
		args = append(args, "-D", displayID)
		arguments["display_id"] = displayID
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
	if displayID := strings.TrimSpace(firstString(arguments, "display_index", "display_id")); displayID != "" {
		result["display_id"] = displayID
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
	scriptLines := []string{"tell application \"System Events\""}
	for index, line := range lines {
		if line != "" {
			scriptLines = append(scriptLines, fmt.Sprintf("  keystroke %s", appleScriptString(line)))
		}
		if index < len(lines)-1 {
			scriptLines = append(scriptLines, "  key code 36")
		}
	}
	if boolValue(arguments["press_enter"]) {
		scriptLines = append(scriptLines, "  key code 36")
	}
	scriptLines = append(scriptLines, "end tell")
	if _, err := runOSAScript(ctx, strings.Join(scriptLines, "\n")); err != nil {
		return nil, err
	}
	return map[string]any{
		"platform":     "darwin",
		"text_length":  len(text),
		"line_count":   len(lines),
		"press_enter":  boolValue(arguments["press_enter"]),
	}, nil
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
	modifierClause, err := appleScriptModifiers(arguments["modifiers"])
	if err != nil {
		return nil, err
	}
	command, err := appleScriptKeyCommand(key, modifierClause)
	if err != nil {
		return nil, err
	}
	scriptLines := []string{"tell application \"System Events\"", fmt.Sprintf("  repeat %d times", repeatCount), "    " + command, "  end repeat", "end tell"}
	if _, err := runOSAScript(ctx, strings.Join(scriptLines, "\n")); err != nil {
		return nil, err
	}
	return map[string]any{
		"platform":    "darwin",
		"key":         key,
		"repeat":      repeatCount,
		"modifiers":   stringSlice(arguments["modifiers"]),
	}, nil
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
		"enter": 36,
		"return": 36,
		"tab": 48,
		"escape": 53,
		"esc": 53,
		"space": 49,
		"backspace": 51,
		"delete": 51,
		"left": 123,
		"right": 124,
		"down": 125,
		"up": 126,
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
	replacer := strings.NewReplacer(`\\`, `\\\\`, `"`, `\\"`)
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