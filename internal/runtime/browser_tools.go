package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/browserbridge"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

const (
	browserToolPrefix             = "taiyiflow-in-chrome__"
	browserToolTabsContext        = browserToolPrefix + "tabs_context"
	browserToolTabsCreate         = browserToolPrefix + "tabs_create"
	browserToolNavigate           = browserToolPrefix + "navigate"
	browserToolClick              = browserToolPrefix + "click"
	browserToolType               = browserToolPrefix + "type"
	browserToolScreenshot         = browserToolPrefix + "screenshot"
	browserToolReadText           = browserToolPrefix + "read_text"
	browserToolReadConsole        = browserToolPrefix + "read_console"
	defaultBrowserScreenshotDir   = ".snz1dp/browser"
	defaultBrowserScreenshotExt   = ".png"
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
	config internalconfig.Config,
	toolName string,
	arguments map[string]any,
) (map[string]any, error) {
	prepared := cloneArguments(arguments)
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
			"name":        browserToolTabsContext,
			"description": "读取当前浏览器标签页上下文，返回标签页 ID、标题、URL 与当前活动标签页。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		{
			"name":        browserToolTabsCreate,
			"description": "创建新浏览器标签页，并在必要时切换为当前活动标签页。",
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
			"name":        browserToolNavigate,
			"description": "让指定标签页导航到 URL；未指定 tab_id 时默认使用当前活动标签页。",
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
			"name":        browserToolClick,
			"description": "点击页面中的 CSS 选择器元素。",
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
			"name":        browserToolType,
			"description": "向页面元素输入文本；支持覆盖写入、追加输入和输入后回车。",
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
			"name":        browserToolScreenshot,
			"description": "截取当前标签页截图并保存为 PNG；若未提供 path，会自动落到工作区内 .snz1dp/browser/。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":     map[string]any{"type": "string", "description": "可选输出路径。"},
					"tab_id":   map[string]any{"type": "string", "description": "可选标签页 ID。"},
					"full_page": map[string]any{"type": "boolean", "description": "保留字段；当前默认截全页。"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":        browserToolReadText,
			"description": "读取页面文本；可选择全文或某个 CSS 选择器的文本内容。",
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
			"name":        browserToolReadConsole,
			"description": "读取当前标签页最近的控制台输出与异常日志。",
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

func (r *ClientRuntime) resolveBrowserScreenshotPath(config internalconfig.Config, arguments map[string]any) (string, error) {
	targetPath := strings.TrimSpace(argumentString(arguments, "path"))
	if targetPath == "" {
		targetPath = filepath.Join(defaultBrowserScreenshotDir, time.Now().Format("20060102-150405")+defaultBrowserScreenshotExt)
	}
	return r.resolveAllowedFilePathWithPolicy(config, targetPath, true)
}