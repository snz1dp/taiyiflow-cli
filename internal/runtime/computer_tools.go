package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/computerbridge"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const (
	computerToolPrefix           = "taiyiflow-computer__"
	computerToolDisplaysContext  = computerToolPrefix + "displays_context"
	computerToolWindowsContext   = computerToolPrefix + "windows_context"
	computerToolFocusWindow      = computerToolPrefix + "focus_window"
	computerToolScreenshot       = computerToolPrefix + "screenshot"
	computerToolMouseMove        = computerToolPrefix + "mouse_move"
	computerToolMouseClick       = computerToolPrefix + "mouse_click"
	computerToolMouseDrag        = computerToolPrefix + "mouse_drag"
	computerToolScroll           = computerToolPrefix + "scroll"
	computerToolKeyPress         = computerToolPrefix + "key_press"
	computerToolTypeText         = computerToolPrefix + "type_text"
	defaultComputerScreenshotDir = ".snz1dp/computer"
	defaultComputerScreenshotExt = ".png"
)

var sharedComputerToolManager = computerbridge.NewManager()

type ComputerToolExecutor interface {
	ExecuteComputerTool(ctx context.Context, config internalconfig.Config, toolName string, arguments map[string]any) (map[string]any, error)
}

type defaultComputerToolExecutor struct{}

func (defaultComputerToolExecutor) ExecuteComputerTool(
	ctx context.Context,
	config internalconfig.Config,
	toolName string,
	arguments map[string]any,
) (map[string]any, error) {
	return sharedComputerToolManager.Execute(ctx, config.Computer, toolName, arguments)
}

func (r *ClientRuntime) SetComputerToolExecutor(executor ComputerToolExecutor) {
	r.computerToolExecutor = executor
}

func (r *ClientRuntime) computerTool(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	toolName string,
	arguments map[string]any,
) (map[string]any, error) {
	if !internalconfig.ComputerEnabled(config.Computer) {
		return nil, fmt.Errorf("桌面操作已在配置中禁用")
	}
	prepared := cloneArguments(arguments)
	if toolName == computerToolScreenshot {
		pathValue, err := r.resolveComputerScreenshotPath(config, prepared)
		if err != nil {
			return nil, err
		}
		prepared["path"] = pathValue
	}
	executor := r.computerToolExecutor
	if executor == nil {
		executor = defaultComputerToolExecutor{}
	}
	result, err := executor.ExecuteComputerTool(ctx, config, toolName, prepared)
	if err != nil {
		return nil, err
	}
	if toolName == computerToolScreenshot {
		normalized := cloneArguments(result)
		if strings.TrimSpace(stringValue(normalized["path"])) == "" {
			normalized["path"] = strings.TrimSpace(argumentString(prepared, "path"))
		}
		if strings.TrimSpace(stringValue(normalized["content_type"])) == "" {
			normalized["content_type"] = "image/png"
		}
		ensureScreenshotImageUnderstandingInput(prepared)
		// 截图不要改变大小，直接传原图给图像理解服务
		prepared["image_compression_strategy"] = "passthrough"
		imageUnderstanding, understandErr := r.understandImageWithProvider(
			ctx, providerClient, config, prepared, stringValue(normalized["path"]),
		)
		if understandErr != nil {
			normalized["image_understanding_error"] = understandErr.Error()
		} else if strings.TrimSpace(imageUnderstanding) != "" {
			normalized["image_understanding"] = imageUnderstanding
		}
		result = normalized
	}
	r.writeStreamEventf(r.stdout, "[桌面工具] %s\n", previewComputerTool(toolName, prepared))
	auditPayload := map[string]any{"tool": toolName}
	if pathValue := strings.TrimSpace(stringValue(prepared["path"])); pathValue != "" {
		auditPayload["path"] = pathValue
	}
	if windowID := strings.TrimSpace(argumentString(prepared, "window_id")); windowID != "" {
		auditPayload["window_id"] = windowID
	}
	_ = appendAuditLog(config, toolName, auditPayload)
	return result, nil
}

func computerClientToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":                 computerToolDisplaysContext,
			"description":          "读取本机显示器上下文，返回显示器物理像素尺寸(width_px/height_px)、逻辑桌面尺寸(logical_width/logical_height)、缩放比(scale_factor)、主屏和旋转信息。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		{
			"name":                 computerToolWindowsContext,
			"description":          "读取当前桌面窗口上下文，返回窗口标题、边界、进程与前台状态。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		{
			"name":                 computerToolFocusWindow,
			"description":          "将指定窗口或进程切到前台焦点。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"window_id":    map[string]any{"type": "string"},
					"process_id":   map[string]any{"type": "integer", "minimum": 1},
					"process_name": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 computerToolScreenshot,
			"description":          "截取当前桌面屏幕并保存为 PNG；若指定 display_index 或 display_id，结果会附带该显示器在全局桌面中的 origin、物理像素尺寸、逻辑桌面尺寸与 scale_factor，便于把截图像素坐标换算为桌面坐标。若服务端配置了 image_flow_id，会额外返回 image_understanding 图像增强结果。未提供 path 时自动落到工作区内 .snz1dp/computer/。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":          map[string]any{"type": "string"},
					"display_id":    map[string]any{"type": "string"},
					"display_index": map[string]any{"type": "integer", "minimum": 1},
					"input_value":   map[string]any{"type": "string", "description": "可选图像理解提示词；"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 computerToolMouseMove,
			"description":          "移动鼠标到指定坐标。默认使用全局桌面坐标；若同时传入 display_index 或 display_id，则默认将 x/y 视为该显示器截图内的局部像素坐标，并按对应显示器 scale_factor 自动换算到全局桌面坐标；若你已持有全局桌面坐标，可用 coordinate_space=global_screen 显式覆盖。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x":                map[string]any{"type": "integer"},
					"y":                map[string]any{"type": "integer"},
					"display_id":       map[string]any{"type": "string"},
					"display_index":    map[string]any{"type": "integer", "minimum": 1},
					"coordinate_space": map[string]any{"type": "string", "enum": []string{"global_screen", "display_local"}},
				},
				"required":             []string{"x", "y"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 computerToolMouseClick,
			"description":          "在当前位置或指定坐标执行鼠标点击。默认使用全局桌面坐标；若同时传入 display_index 或 display_id，则默认将 x/y 视为该显示器截图内的局部像素坐标，并按对应显示器 scale_factor 自动换算到全局桌面坐标；若你已持有全局桌面坐标，可用 coordinate_space=global_screen 显式覆盖。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"x":                map[string]any{"type": "integer"},
					"y":                map[string]any{"type": "integer"},
					"display_id":       map[string]any{"type": "string"},
					"display_index":    map[string]any{"type": "integer", "minimum": 1},
					"coordinate_space": map[string]any{"type": "string", "enum": []string{"global_screen", "display_local"}},
					"button":           map[string]any{"type": "string", "enum": []string{"left", "right", "middle"}},
					"click_count":      map[string]any{"type": "integer", "minimum": 1, "maximum": 3},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 computerToolMouseDrag,
			"description":          "从起点拖拽到终点坐标。默认使用全局桌面坐标；若同时传入 display_index 或 display_id，则默认将 from/to 坐标视为该显示器截图内的局部像素坐标，并按对应显示器 scale_factor 自动换算到全局桌面坐标；若你已持有全局桌面坐标，可用 coordinate_space=global_screen 显式覆盖。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from_x":           map[string]any{"type": "integer"},
					"from_y":           map[string]any{"type": "integer"},
					"to_x":             map[string]any{"type": "integer"},
					"to_y":             map[string]any{"type": "integer"},
					"display_id":       map[string]any{"type": "string"},
					"display_index":    map[string]any{"type": "integer", "minimum": 1},
					"coordinate_space": map[string]any{"type": "string", "enum": []string{"global_screen", "display_local"}},
					"duration_ms":      map[string]any{"type": "integer", "minimum": 0, "maximum": 10000},
				},
				"required":             []string{"from_x", "from_y", "to_x", "to_y"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 computerToolScroll,
			"description":          "执行垂直或水平滚动。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"delta_x": map[string]any{"type": "integer"},
					"delta_y": map[string]any{"type": "integer"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 computerToolKeyPress,
			"description":          "发送单个按键或常见特殊键，可附加修饰键和重复次数。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"key":       map[string]any{"type": "string"},
					"modifiers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"repeat":    map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				},
				"required":             []string{"key"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 computerToolTypeText,
			"description":          "向当前焦点输入文本，可选输入后回车。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":        map[string]any{"type": "string"},
					"press_enter": map[string]any{"type": "boolean"},
				},
				"required":             []string{"text"},
				"additionalProperties": false,
			},
		},
	}
}

func isComputerTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case computerToolDisplaysContext, computerToolWindowsContext, computerToolFocusWindow, computerToolScreenshot, computerToolMouseMove, computerToolMouseClick, computerToolMouseDrag, computerToolScroll, computerToolKeyPress, computerToolTypeText:
		return true
	default:
		return false
	}
}

func computerReadOnlyTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case computerToolDisplaysContext, computerToolWindowsContext, computerToolScreenshot:
		return true
	default:
		return false
	}
}

func computerToolRequiresConfirmation(config internalconfig.Config, toolName string, arguments map[string]any) bool {
	if !internalconfig.ComputerEnabled(config.Computer) {
		return false
	}
	if !isComputerTool(toolName) {
		return false
	}
	if action, matched := evaluatePermissionAction(config, toolName, commandPreview(arguments)); matched {
		return action == "ask" && !computerReadOnlyTool(toolName)
	}
	if computerReadOnlyTool(toolName) {
		return false
	}
	switch internalconfig.NormalizeComputerPermissionMode(config.Computer.PermissionMode) {
	case internalconfig.ComputerPermissionModeSkipAll:
		return false
	case internalconfig.ComputerPermissionModeFollowPlan:
		return strings.TrimSpace(config.ApprovalMode) == "confirm"
	default:
		return true
	}
}

func (r *ClientRuntime) authorizeComputerTool(toolName string, arguments map[string]any, config internalconfig.Config) error {
	if !internalconfig.ComputerEnabled(config.Computer) {
		return newToolError("PERMISSION_DENIED", "桌面操作已在配置中禁用，拒绝执行 %s", canonicalClientToolName(toolName))
	}
	mode := strings.TrimSpace(config.ApprovalMode)
	if action, matched := evaluatePermissionAction(config, toolName, commandPreview(arguments)); matched {
		switch action {
		case "deny":
			return newToolError("PERMISSION_DENIED", "命中权限规则，拒绝执行 %s", canonicalClientToolName(toolName))
		case "allow":
			if mode == "readonly" && !computerReadOnlyTool(toolName) {
				return newToolError("PERMISSION_DENIED", "当前审批模式为只读，拒绝执行 %s", canonicalClientToolName(toolName))
			}
			return nil
		case "ask":
			if computerReadOnlyTool(toolName) {
				return nil
			}
			approved, err := r.confirmClientTool(toolName, arguments)
			if err != nil {
				return err
			}
			if !approved {
				return newToolError("PERMISSION_DENIED", "用户拒绝执行 %s", canonicalClientToolName(toolName))
			}
			return nil
		}
	}
	if computerReadOnlyTool(toolName) {
		return nil
	}
	switch internalconfig.NormalizeComputerPermissionMode(config.Computer.PermissionMode) {
	case internalconfig.ComputerPermissionModeSkipAll:
		return nil
	case internalconfig.ComputerPermissionModeFollowPlan:
		if mode != "confirm" {
			return nil
		}
	}
	if mode == "readonly" {
		return newToolError("PERMISSION_DENIED", "当前审批模式为只读，拒绝执行 %s", canonicalClientToolName(toolName))
	}
	approved, err := r.confirmClientTool(toolName, arguments)
	if err != nil {
		return err
	}
	if !approved {
		return newToolError("PERMISSION_DENIED", "用户拒绝执行 %s", canonicalClientToolName(toolName))
	}
	return nil
}

func previewComputerTool(toolName string, arguments map[string]any) string {
	switch strings.TrimSpace(toolName) {
	case computerToolDisplaysContext:
		return computerToolDisplaysContext
	case computerToolWindowsContext:
		return computerToolWindowsContext
	case computerToolFocusWindow:
		return fmt.Sprintf("%s %s", computerToolFocusWindow, fallbackString(argumentString(arguments, "window_id", "process_name"), "<target>"))
	case computerToolScreenshot:
		return fmt.Sprintf("%s %s", computerToolScreenshot, fallbackString(argumentString(arguments, "path"), "<auto>"))
	case computerToolMouseMove:
		return fmt.Sprintf("%s (%d,%d)", computerToolMouseMove, argumentInt(arguments, 0, "x"), argumentInt(arguments, 0, "y"))
	case computerToolMouseClick:
		return fmt.Sprintf("%s %s", computerToolMouseClick, fallbackString(argumentString(arguments, "button"), "left"))
	case computerToolMouseDrag:
		return fmt.Sprintf("%s (%d,%d)->(%d,%d)", computerToolMouseDrag, argumentInt(arguments, 0, "from_x"), argumentInt(arguments, 0, "from_y"), argumentInt(arguments, 0, "to_x"), argumentInt(arguments, 0, "to_y"))
	case computerToolScroll:
		return fmt.Sprintf("%s dx=%d dy=%d", computerToolScroll, argumentInt(arguments, 0, "delta_x"), argumentInt(arguments, 0, "delta_y"))
	case computerToolKeyPress:
		return fmt.Sprintf("%s %s", computerToolKeyPress, fallbackString(argumentString(arguments, "key"), "<missing>"))
	case computerToolTypeText:
		return fmt.Sprintf("%s %d chars", computerToolTypeText, utf8.RuneCountInString(argumentString(arguments, "text")))
	default:
		return toolName
	}
}

func (r *ClientRuntime) resolveComputerScreenshotPath(config internalconfig.Config, arguments map[string]any) (string, error) {
	targetPath := strings.TrimSpace(argumentString(arguments, "path"))
	if targetPath == "" {
		targetPath = filepath.Join(defaultComputerScreenshotDir, time.Now().Format("20060102-150405")+defaultComputerScreenshotExt)
	}
	return r.resolveAllowedFilePathWithPolicy(config, targetPath, true)
}
