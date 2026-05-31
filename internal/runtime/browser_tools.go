package runtime

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/browserbridge"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const (
	browserToolPrefix           = "taiyiflow-in-chrome__"
	browserToolTabsContext      = browserToolPrefix + "tabs_context"
	browserToolTabsCreate       = browserToolPrefix + "tabs_create"
	browserToolNavigate         = browserToolPrefix + "navigate"
	browserToolClick            = browserToolPrefix + "click"
	browserToolType             = browserToolPrefix + "type"
	browserToolScreenshot       = browserToolPrefix + "screenshot"
	browserToolReadText         = browserToolPrefix + "read_text"
	browserToolReadConsole      = browserToolPrefix + "read_console"
	defaultBrowserScreenshotDir = ".snz1dp/browser"
	defaultBrowserScreenshotExt = ".png"
)

var sharedBrowserToolManager = browserbridge.NewManager()

type BrowserToolExecutor interface {
	ExecuteBrowserTool(ctx context.Context, config internalconfig.Config, toolName string, arguments map[string]any) (map[string]any, error)
}

type defaultBrowserToolExecutor struct{}

func (defaultBrowserToolExecutor) ExecuteBrowserTool(
	ctx context.Context,
	config internalconfig.Config,
	toolName string,
	arguments map[string]any,
) (map[string]any, error) {
	return sharedBrowserToolManager.Execute(ctx, config.Browser, toolName, arguments)
}

func (r *ClientRuntime) SetBrowserToolExecutor(executor BrowserToolExecutor) {
	r.browserToolExecutor = executor
}

func (r *ClientRuntime) browserTool(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	toolName string,
	arguments map[string]any,
) (map[string]any, error) {
	prepared := cloneArguments(arguments)
	normalizeBrowserToolArguments(toolName, prepared)
	if toolName == browserToolScreenshot {
		pathValue, err := r.resolveBrowserScreenshotPath(config, prepared)
		if err != nil {
			return nil, err
		}
		prepared["path"] = pathValue
	}
	executor := r.browserToolExecutor
	if executor == nil {
		executor = defaultBrowserToolExecutor{}
	}
	result, err := executor.ExecuteBrowserTool(ctx, config, toolName, prepared)
	if err != nil {
		return nil, err
	}
	result, err = normalizeBrowserToolResult(toolName, prepared, result)
	if err != nil {
		return nil, err
	}
	if toolName == browserToolScreenshot {
		ensureScreenshotImageUnderstandingInput(prepared)
		// 截图不要改变大小，直接传原图给图像理解服务
		prepared["image_compression_strategy"] = provider.ImageCompressionStrategyPassthrough
		imageUnderstanding, understandErr := r.understandImageWithProvider(
			ctx, providerClient, config, prepared, stringValue(result["path"]),
		)
		if understandErr != nil {
			result["image_understanding_error"] = understandErr.Error()
		} else if strings.TrimSpace(imageUnderstanding) != "" {
			result["image_understanding"] = imageUnderstanding
		}
	}
	r.writeStreamEventf(r.stdout, "[浏览器工具] %s\n", previewBrowserTool(toolName, prepared))
	auditPayload := map[string]any{"tool": toolName}
	if pathValue := strings.TrimSpace(stringValue(prepared["path"])); pathValue != "" {
		auditPayload["path"] = pathValue
	}
	if tabID := strings.TrimSpace(stringValue(result["tab_id"])); tabID != "" {
		auditPayload["tab_id"] = tabID
	}
	_ = appendAuditLog(config, toolName, auditPayload)
	return result, nil
}

func browserClientToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":                 browserToolTabsContext,
			"description":          "读取当前浏览器标签页上下文，返回标签页 ID、标题、URL 与当前活动标签页。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		{
			"name":                 browserToolTabsCreate,
			"description":          "创建新浏览器标签页，并在必要时切换为当前活动标签页。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":      map[string]any{"type": "string", "description": "新标签页打开的 URL；默认 about:blank。"},
					"activate": map[string]any{"type": "boolean", "description": "是否切换为当前活动标签页；默认 true。"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 browserToolNavigate,
			"description":          "让指定标签页导航到 URL；未指定 tab_id 时默认使用当前活动标签页。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":    map[string]any{"type": "string", "description": "目标 URL。"},
					"tab_id": map[string]any{"type": "string", "description": "可选标签页 ID。"},
				},
				"required":             []string{"url"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 browserToolClick,
			"description":          "点击页面中的 CSS 选择器元素。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector": map[string]any{"type": "string", "description": "要点击的 CSS 选择器。"},
					"tab_id":   map[string]any{"type": "string", "description": "可选标签页 ID。"},
				},
				"required":             []string{"selector"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 browserToolType,
			"description":          "向页面元素输入文本；支持覆盖写入、追加输入和输入后回车。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector":    map[string]any{"type": "string", "description": "目标输入元素的 CSS 选择器。"},
					"text":        map[string]any{"type": "string", "description": "要输入的文本。"},
					"tab_id":      map[string]any{"type": "string", "description": "可选标签页 ID。"},
					"append":      map[string]any{"type": "boolean", "description": "为 true 时在原内容后追加；默认覆盖写入。"},
					"press_enter": map[string]any{"type": "boolean", "description": "输入完成后是否回车。"},
				},
				"required":             []string{"selector", "text"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 browserToolScreenshot,
			"description":          "截取当前标签页截图并保存为 PNG；若服务端配置了 image_flow_id，会额外返回 image_understanding 图像增强结果。未提供 path 时自动落到工作区内 .snz1dp/browser/。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":        map[string]any{"type": "string", "description": "可选输出路径。"},
					"tab_id":      map[string]any{"type": "string", "description": "可选标签页 ID。"},
					"full_page":   map[string]any{"type": "boolean", "description": "保留字段；当前默认截全页。"},
					"input_value": map[string]any{"type": "string", "description": "可选图像理解提示词"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 browserToolReadText,
			"description":          "读取页面文本；可选择全文或某个 CSS 选择器的文本内容。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"selector":  map[string]any{"type": "string", "description": "可选 CSS 选择器；留空时读取整页正文。"},
					"tab_id":    map[string]any{"type": "string", "description": "可选标签页 ID。"},
					"max_chars": map[string]any{"type": "integer", "minimum": 1, "maximum": maxReadBytesHard},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 browserToolReadConsole,
			"description":          "读取当前标签页最近的控制台输出与异常日志。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tab_id": map[string]any{"type": "string", "description": "可选标签页 ID。"},
					"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
				},
				"additionalProperties": false,
			},
		},
	}
}

func isBrowserTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case browserToolTabsContext, browserToolTabsCreate, browserToolNavigate, browserToolClick, browserToolType, browserToolScreenshot, browserToolReadText, browserToolReadConsole:
		return true
	default:
		return false
	}
}

func browserReadOnlyTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case browserToolTabsContext, browserToolReadText, browserToolReadConsole:
		return true
	default:
		return false
	}
}

func browserToolRequiresConfirmation(config internalconfig.Config, toolName string, arguments map[string]any) bool {
	if !internalconfig.BrowserEnabled(config.Browser) {
		return false
	}
	if !isBrowserTool(toolName) {
		return false
	}
	if action, matched := evaluatePermissionAction(config, toolName, commandPreview(arguments)); matched {
		return action == "ask"
	}
	if browserReadOnlyTool(toolName) {
		return false
	}
	switch internalconfig.NormalizeBrowserPermissionMode(config.Browser.PermissionMode) {
	case internalconfig.BrowserPermissionModeSkipAll:
		return false
	case internalconfig.BrowserPermissionModeFollowPlan:
		return strings.TrimSpace(config.ApprovalMode) == "confirm"
	default:
		return true
	}
}

func (r *ClientRuntime) authorizeBrowserTool(toolName string, arguments map[string]any, config internalconfig.Config) error {
	if !internalconfig.BrowserEnabled(config.Browser) {
		return newToolError("PERMISSION_DENIED", "浏览器自动化已在配置中禁用，拒绝执行 %s", canonicalClientToolName(toolName))
	}
	mode := strings.TrimSpace(config.ApprovalMode)
	if action, matched := evaluatePermissionAction(config, toolName, commandPreview(arguments)); matched {
		switch action {
		case "deny":
			return newToolError("PERMISSION_DENIED", "命中权限规则，拒绝执行 %s", canonicalClientToolName(toolName))
		case "allow":
			return nil
		case "ask":
			if browserReadOnlyTool(toolName) {
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
	if browserReadOnlyTool(toolName) {
		return nil
	}
	switch internalconfig.NormalizeBrowserPermissionMode(config.Browser.PermissionMode) {
	case internalconfig.BrowserPermissionModeSkipAll:
		return nil
	case internalconfig.BrowserPermissionModeFollowPlan:
		if mode != "confirm" {
			return nil
		}
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

func previewBrowserTool(toolName string, arguments map[string]any) string {
	switch strings.TrimSpace(toolName) {
	case browserToolTabsContext:
		return browserToolTabsContext
	case browserToolTabsCreate:
		return fmt.Sprintf("%s %s", browserToolTabsCreate, fallbackString(argumentString(arguments, "url"), "about:blank"))
	case browserToolNavigate:
		return fmt.Sprintf("%s %s", browserToolNavigate, fallbackString(argumentString(arguments, "url"), "<missing>"))
	case browserToolClick:
		return fmt.Sprintf("%s %s", browserToolClick, fallbackString(argumentString(arguments, "selector"), "<missing>"))
	case browserToolType:
		return fmt.Sprintf("%s %s", browserToolType, fallbackString(argumentString(arguments, "selector"), "<missing>"))
	case browserToolScreenshot:
		return fmt.Sprintf("%s %s", browserToolScreenshot, fallbackString(argumentString(arguments, "path"), "<auto>"))
	case browserToolReadText:
		return fmt.Sprintf("%s %s", browserToolReadText, fallbackString(argumentString(arguments, "selector"), "<page>"))
	case browserToolReadConsole:
		return fmt.Sprintf("%s %s", browserToolReadConsole, fallbackString(argumentString(arguments, "tab_id", "tabId"), "<active>"))
	default:
		return toolName
	}
}

func cloneArguments(arguments map[string]any) map[string]any {
	if len(arguments) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(arguments))
	for key, value := range arguments {
		cloned[key] = value
	}
	return cloned
}

func normalizeBrowserToolArguments(toolName string, arguments map[string]any) {
	switch strings.TrimSpace(toolName) {
	case browserToolTabsCreate:
		if strings.TrimSpace(argumentString(arguments, "url")) == "" {
			arguments["url"] = "about:blank"
		}
		if _, ok := arguments["activate"]; !ok {
			arguments["activate"] = true
		}
	case browserToolReadText:
		if maxChars, ok := arguments["max_chars"]; ok {
			if _, exists := arguments["max_length"]; !exists {
				arguments["max_length"] = maxChars
			}
		} else if maxLength, ok := arguments["max_length"]; ok {
			arguments["max_chars"] = maxLength
		}
	}
}

func normalizeBrowserToolResult(toolName string, arguments map[string]any, result map[string]any) (map[string]any, error) {
	normalized := cloneArguments(result)
	switch strings.TrimSpace(toolName) {
	case browserToolTabsContext:
		if _, ok := normalized["count"]; !ok {
			switch tabs := normalized["tabs"].(type) {
			case []any:
				normalized["count"] = len(tabs)
			case []map[string]any:
				normalized["count"] = len(tabs)
			}
		}
	case browserToolTabsCreate:
		if strings.TrimSpace(stringValue(normalized["url"])) == "" {
			normalized["url"] = fallbackString(argumentString(arguments, "url"), "about:blank")
		}
		if _, ok := normalized["created"]; !ok {
			normalized["created"] = true
		}
		if _, ok := normalized["active"]; !ok {
			normalized["active"] = boolValue(arguments["activate"])
		}
	case browserToolReadText:
		selector := strings.TrimSpace(argumentString(arguments, "selector"))
		if _, ok := normalized["selector"]; !ok {
			normalized["selector"] = selector
		}
		textValue := stringValue(normalized["text"])
		if _, ok := normalized["found"]; !ok {
			normalized["found"] = selector == "" || strings.TrimSpace(textValue) != ""
		}
		if _, ok := normalized["total_length"]; !ok {
			normalized["total_length"] = len(textValue)
		}
		if _, ok := normalized["truncated"]; !ok {
			maxChars := intValue(arguments["max_chars"], 0)
			totalLength := intValue(normalized["total_length"], len(textValue))
			normalized["truncated"] = maxChars > 0 && totalLength > len(textValue) && len(textValue) >= maxChars
		}
	case browserToolReadConsole:
		if _, ok := normalized["count"]; !ok {
			switch entries := normalized["entries"].(type) {
			case []any:
				normalized["count"] = len(entries)
			case []map[string]any:
				normalized["count"] = len(entries)
			}
		}
	case browserToolScreenshot:
		pathValue := strings.TrimSpace(stringValue(normalized["path"]))
		if pathValue == "" {
			pathValue = strings.TrimSpace(argumentString(arguments, "path"))
		}
		if imageBase64 := strings.TrimSpace(stringValue(normalized["image_base64"])); imageBase64 != "" {
			if pathValue == "" {
				return nil, fmt.Errorf("浏览器截图结果缺少 path")
			}
			imageBytes, err := base64.StdEncoding.DecodeString(imageBase64)
			if err != nil {
				return nil, fmt.Errorf("解析浏览器截图结果失败: %w", err)
			}
			if err := writeFileAtomically(pathValue, imageBytes); err != nil {
				return nil, fmt.Errorf("写入浏览器截图文件失败: %w", err)
			}
			normalized["path"] = pathValue
			normalized["size"] = len(imageBytes)
			if strings.TrimSpace(stringValue(normalized["content_type"])) == "" {
				normalized["content_type"] = "image/png"
			}
			delete(normalized, "image_base64")
			delete(normalized, "format")
		} else if pathValue != "" {
			normalized["path"] = pathValue
		}
	}
	return normalized, nil
}

func (r *ClientRuntime) resolveBrowserScreenshotPath(config internalconfig.Config, arguments map[string]any) (string, error) {
	targetPath := strings.TrimSpace(argumentString(arguments, "path"))
	if targetPath == "" {
		targetPath = filepath.Join(defaultBrowserScreenshotDir, time.Now().Format("20060102-150405")+defaultBrowserScreenshotExt)
	}
	return r.resolveAllowedFilePathWithPolicy(config, targetPath, true)
}
