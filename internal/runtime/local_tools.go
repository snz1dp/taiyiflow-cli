package runtime

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
)

const (
	maxReadBytesDefault = 64 * 1024
	maxReadBytesHard    = 256 * 1024
)

type toolError struct {
	Code    string
	Message string
}

func (e toolError) Error() string {
	return e.Message
}

func newToolError(code string, format string, args ...any) error {
	return toolError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func canonicalClientToolName(toolName string) string {
	switch strings.TrimSpace(toolName) {
	case "skill":
		return "skill"
	case "ask_user":
		return "ask_user"
	case "lsp_query":
		return "lsp_query"
	case "glob_files":
		return "glob_files"
	case "search_files":
		return "search_files"
	case "shell_exec":
		return "shell_exec"
	default:
		return strings.TrimSpace(toolName)
	}
}

func firstArgument(arguments map[string]any, keys ...string) (any, bool) {
	for _, key := range keys {
		if value, ok := arguments[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func argumentString(arguments map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := firstArgument(arguments, key); ok {
			if trimmed := strings.TrimSpace(stringValue(value)); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func argumentStringSlice(arguments map[string]any, keys ...string) []string {
	for _, key := range keys {
		if value, ok := firstArgument(arguments, key); ok {
			if items := stringSlice(value); len(items) > 0 {
				return items
			}
		}
	}
	return nil
}

func argumentBool(arguments map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, ok := firstArgument(arguments, key); ok {
			return boolValue(value)
		}
	}
	return false
}

func argumentInt(arguments map[string]any, defaultValue int, keys ...string) int {
	for _, key := range keys {
		if value, ok := firstArgument(arguments, key); ok {
			return intValue(value, defaultValue)
		}
	}
	return defaultValue
}

func CurrentShellExecCommandArgsExample() string {
	if runtime.GOOS == "windows" {
		return `{"command":"cmd","args":["/C","dir"]}`
	}
	return `{"command":"ls","args":["-la"]}`
}

func CurrentShellExecRawCommandExample() string {
	if runtime.GOOS == "windows" {
		return `{"rawCommand":"dir | more"}`
	}
	return `{"rawCommand":"ls -la | cat"}`
}

func shellExecToolDescription() string {
	return fmt.Sprintf(
		"执行结构化本地 Shell 命令，支持前台执行或后台任务句柄返回。当前 OS: %s/%s。按当前 OS 选择命令，不要假设 Linux、macOS、Windows 的命令语义等价。优先使用 command + args，不要把整条命令塞进 command；示例: %s。只有需要管道、重定向或 shell 内建命令时才使用 rawCommand；示例: %s。",
		runtime.GOOS,
		runtime.GOARCH,
		CurrentShellExecCommandArgsExample(),
		CurrentShellExecRawCommandExample(),
	)
}

func (r *ClientRuntime) clientToolDefinitionsForMode(mode string) []map[string]any {
	definitions := []map[string]any{
		{
			"name":                 "ask_user",
			"description":          "向用户收集澄清信息或执行前确认。questions 中每项使用 name、label、input_format、options_list 描述；input_format 支持 text 或 select，multi_select 为 true 时允许多选并返回字符串数组。用户取消时返回错误，而不是空结果。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"questions": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name":         map[string]any{"type": "string", "description": "题目键名，返回结果会使用该字段名。"},
								"label":        map[string]any{"type": "string", "description": "展示给用户的问题文案。"},
								"input_format": map[string]any{"type": "string", "enum": []string{"text", "select"}, "description": "text 为自由输入，select 为从 options_list 中选择。"},
								"options_list": map[string]any{
									"type": "array",
									"items": map[string]any{
										"anyOf": []any{
											map[string]any{"type": "string"},
											map[string]any{
												"type": "object",
												"properties": map[string]any{
													"label": map[string]any{"type": "string"},
													"value": map[string]any{"type": "string"},
												},
												"required":             []string{"label"},
												"additionalProperties": false,
											},
										},
									},
									"description": "select 题的候选项；可传字符串或 {label, value} 对象。",
								},
								"multi_select": map[string]any{"type": "boolean", "description": "仅对 select 生效；为 true 时允许多选，并返回字符串数组。"},
							},
							"required":             []string{"name", "label"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"questions"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "lsp_query",
			"description":          "通过本地标准输入输出(stdio)语言服务器(language server)执行定义跳转(go_to_definition)、引用查询(find_references)、实现跳转(go_to_implementation)、文档符号(document_symbol)、工作区符号(workspace_symbol)查询。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"serverCommand":      map[string]any{"type": "string", "description": "语言服务器命令(language server command)，例如 gopls 或 pyright-langserver。"},
					"serverArgs":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"workspaceRoot":      map[string]any{"type": "string", "description": "工作区根目录(workspace root)，默认当前工作目录。"},
					"operation":          map[string]any{"type": "string", "enum": []string{"go_to_definition", "find_references", "go_to_implementation", "document_symbol", "workspace_symbol"}},
					"filePath":           map[string]any{"type": "string", "description": "需要定位的文件路径(file path)；workspace_symbol 可省略。"},
					"line":               map[string]any{"type": "integer", "minimum": 1},
					"character":          map[string]any{"type": "integer", "minimum": 1},
					"query":              map[string]any{"type": "string", "description": "工作区符号查询关键字(query)。"},
					"languageId":         map[string]any{"type": "string", "description": "可选语言标识(language id)；默认按文件扩展名推断。"},
					"includeDeclaration": map[string]any{"type": "boolean"},
					"maxResults":         map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
					"timeout":            map[string]any{"type": "integer", "minimum": 1, "maximum": internalconfig.MaxCommandTimeoutSeconds},
				},
				"required":             []string{"serverCommand", "operation"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "glob_files",
			"description":          "按 glob 模式枚举当前工作目录访问范围内的文件或目录。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":          map[string]any{"type": "string", "description": "起始目录，默认当前工作目录。"},
					"pattern":       map[string]any{"type": "string", "description": "glob 模式，例如 **/*.go 或 cmd/*.go。"},
					"includeHidden": map[string]any{"type": "boolean"},
					"kind":          map[string]any{"type": "string", "enum": []string{"all", "file", "directory"}},
					"maxResults":    map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "todo_write",
			"description":          "覆盖写入当前会话的 Todo 清单。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"title":  map[string]any{"type": "string"},
								"status": map[string]any{"type": "string", "enum": []string{"not_started", "in_progress", "completed"}},
								"notes":  map[string]any{"type": "string"},
							},
							"required":             []string{"title"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"items"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "file_edit",
			"description":          "基于 old_string/new_string 的细粒度文本编辑，要求 old_string 与文件当前内容精确匹配；若返回 PATCH_CONFLICT，应优先使用结果中的 prefetched_read 与 retry_hint.recommended_args 组织下一次 file_edit，而不是盲目重试。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":                   map[string]any{"type": "string", "description": "目标文件路径。"},
					"old_string":             map[string]any{"type": "string", "description": "要替换的旧文本；冲突后可优先复用 retry_hint.recommended_args.old_string。"},
					"new_string":             map[string]any{"type": "string", "description": "替换后的新文本。"},
					"replace_all":            map[string]any{"type": "boolean"},
					"dry_run":                map[string]any{"type": "boolean"},
					"expected_sha256_before": map[string]any{"type": "string", "description": "可选的编辑前内容摘要，用于检测文件是否变化；冲突后可优先复用 retry_hint.recommended_args.expected_sha256_before。"},
				},
				"required":             []string{"path", "old_string", "new_string"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "shell_exec",
			"description":          shellExecToolDescription(),
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"rawCommand":       map[string]any{"type": "string", "description": fmt.Sprintf("直接交给 shell 执行的原始命令。仅在需要管道、重定向或 shell 内建命令时使用；当前 OS 示例：%s。", CurrentShellExecRawCommandExample())},
					"command":          map[string]any{"type": "string", "description": fmt.Sprintf("可执行文件名；优先与 args 配合使用，不要把整条命令塞进 command。当前 OS 结构化示例：%s。", CurrentShellExecCommandArgsExample())},
					"args":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "命令参数数组，仅在使用 command 时生效。"},
					"workingDirectory": map[string]any{"type": "string", "description": "命令工作目录，默认当前工作目录。"},
					"timeout":          map[string]any{"type": "integer", "minimum": 1, "maximum": internalconfig.MaxCommandTimeoutSeconds},
					"env":              map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
					"expectJson":       map[string]any{"type": "boolean"},
					"background":       map[string]any{"type": "boolean", "description": "为 true 时转为后台任务并返回 task_id。"},
					"taskName":         map[string]any{"type": "string", "description": "可选任务名称，便于 TUI 与模型识别。"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "task_output",
			"description":          "读取本地后台任务的当前状态与输出摘要。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id":   map[string]any{"type": "string", "description": "后台任务 ID。"},
					"max_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": maxReadBytesHard},
				},
				"required":             []string{"task_id"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "task_stop",
			"description":          "停止仍在运行的本地后台任务。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "后台任务 ID。"},
				},
				"required":             []string{"task_id"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "task_list",
			"description":          "列出当前本地任务存储中已知的后台任务。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		{
			"name":                 "task_delete",
			"description":          "删除已完成、失败或已取消的后台任务及其持久化输出。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "description": "后台任务 ID。"},
				},
				"required":             []string{"task_id"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "task_cleanup",
			"description":          "批量清理所有已结束的后台任务及其持久化输出，保留运行中的任务。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		{
			"name":                 "terminal_capture",
			"description":          "读取后台任务的终端视角输出，适合持续查看 stdout/stderr。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id":   map[string]any{"type": "string", "description": "后台任务 ID。"},
					"stream":    map[string]any{"type": "string", "enum": []string{"stdout", "stderr", "both"}},
					"max_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": maxReadBytesHard},
				},
				"required":             []string{"task_id"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "search_files",
			"description":          "在当前工作目录访问范围内搜索文本，支持 ripgrep 风格模式、glob 过滤和输出模式。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern":         map[string]any{"type": "string", "description": "要搜索的文本或正则表达式。"},
					"path":            map[string]any{"type": "string", "description": "搜索根目录，默认当前工作目录。"},
					"glob":            map[string]any{"type": "string", "description": "文件 glob 过滤，例如 **/*.go。"},
					"includePatterns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"excludePatterns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"caseSensitive":   map[string]any{"type": "boolean"},
					"isRegexp":        map[string]any{"type": "boolean"},
					"includeHidden":   map[string]any{"type": "boolean"},
					"outputMode":      map[string]any{"type": "string", "enum": []string{"content", "files_with_matches", "count"}},
					"multiline":       map[string]any{"type": "boolean"},
					"offset":          map[string]any{"type": "integer", "minimum": 0},
					"maxResults":      map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "read_file",
			"description":          "读取当前工作目录访问范围内的文本文件，支持按行读取、二进制识别和 UTF-8 安全截断。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":       map[string]any{"type": "string", "description": "目标文件路径。"},
					"start_line": map[string]any{"type": "integer", "minimum": 1},
					"end_line":   map[string]any{"type": "integer", "minimum": 1},
					"max_bytes":  map[string]any{"type": "integer", "minimum": 1, "maximum": maxReadBytesHard},
				},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "document_unstructured",
			"description":          "上传本地文件到服务端文档结构化流程(doc_unstructured_flow_id)并返回稳定 JSON 结果；具体适用场景由当前技能提示、配置和服务端能力共同决定。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":              map[string]any{"type": "string", "description": "单个本地文件路径；相对路径基于 working_directory。"},
					"paths":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "多个本地文件路径；与 path 可同时使用。"},
					"input":             map[string]any{"type": "string", "description": "可选附加说明，会作为 input_value 发送给服务端解析流程。"},
					"input_type":        map[string]any{"type": "string", "description": "可选输入类型；默认 any。"},
					"output_type":       map[string]any{"type": "string", "description": "可选输出类型；默认 any。"},
					"working_directory": map[string]any{"type": "string", "description": "解析相对路径的基准目录。"},
					"flow_id":           map[string]any{"type": "string", "description": "当前会话所属智能体 ID；通常由上下文自动注入。"},
					"session_id":        map[string]any{"type": "string", "description": "当前会话 ID；通常由上下文自动注入。"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "write_file",
			"description":          "在当前工作目录访问范围内写入文本文件，支持 create、overwrite、append 和原子写入。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]any{"type": "string", "description": "目标文件路径。"},
					"content": map[string]any{"type": "string", "description": "要写入的文本内容。"},
					"mode":    map[string]any{"type": "string", "enum": []string{"create", "overwrite", "append"}},
				},
				"required":             []string{"path", "content"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "skill",
			"description":          "按名称装载本地技能(skill)，返回可立即执行的技能提示内容(prompt)。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string", "description": "技能名称。"},
					"input": map[string]any{"type": "string", "description": "可选技能输入，用于补充本次技能调用上下文。"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
	}
	if provider.NormalizeRequestType(mode) == provider.RequestTypeAgent {
		return definitions
	}
	allowed := allowedClientToolSetForMode(mode)
	filtered := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		name := strings.TrimSpace(stringValue(definition["name"]))
		if allowed[name] {
			filtered = append(filtered, definition)
		}
	}
	return filtered
}

func allowedClientToolSetForMode(mode string) map[string]bool {
	if provider.NormalizeRequestType(mode) == provider.RequestTypeAgent {
		return map[string]bool{}
	}
	return map[string]bool{
		"ask_user":         true,
		"lsp_query":        true,
		"glob_files":       true,
		"task_output":      true,
		"task_list":        true,
		"terminal_capture": true,
		"search_files":     true,
		"read_file":        true,
		"skill":            true,
	}
}

func RequiresClientToolConfirmation(config internalconfig.Config, payload map[string]any) bool {
	toolName := canonicalClientToolName(nestedString(payload, "function", "name"))
	arguments := nestedMap(payload, "function", "arguments")
	if strings.TrimSpace(config.ApprovalMode) == "readonly" && mutatingClientTool(toolName) {
		return false
	}
	if isWriteConfirmationTool(toolName) {
		return internalconfig.NormalizeWriteConfirmationPolicy(config.WriteConfirmationPolicy) == internalconfig.WriteConfirmationPolicyConfirm
	}
	if action, matched := evaluatePermissionAction(config, toolName, commandPreview(arguments)); matched {
		return action == "ask"
	}
	if toolName == "skill" {
		return internalconfig.NormalizeSkillReviewPolicy(config.SkillReviewPolicy) == internalconfig.SkillReviewPolicyConfirm
	}
	return shouldConfirmMutatingTool(config, toolName)
}

func PreviewClientToolCall(payload map[string]any) string {
	toolName := canonicalClientToolName(nestedString(payload, "function", "name"))
	arguments := nestedMap(payload, "function", "arguments")
	return previewClientTool(toolName, arguments)
}

func (r *ClientRuntime) authorizeClientTool(toolName string, arguments map[string]any, config internalconfig.Config) error {
	mode := strings.TrimSpace(config.ApprovalMode)
	if isWriteConfirmationTool(toolName) {
		if mode == "readonly" {
			return newToolError("PERMISSION_DENIED", "当前审批模式为只读，拒绝执行 %s", canonicalClientToolName(toolName))
		}
		if action, matched := evaluatePermissionAction(config, toolName, commandPreview(arguments)); matched && action == "deny" {
			return newToolError("PERMISSION_DENIED", "命中权限规则，拒绝执行 %s", canonicalClientToolName(toolName))
		}
		if internalconfig.NormalizeWriteConfirmationPolicy(config.WriteConfirmationPolicy) != internalconfig.WriteConfirmationPolicyConfirm {
			return nil
		}
		if mode == "auto" {
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
	if action, matched := evaluatePermissionAction(config, toolName, commandPreview(arguments)); matched {
		switch action {
		case "deny":
			return newToolError("PERMISSION_DENIED", "命中权限规则，拒绝执行 %s", canonicalClientToolName(toolName))
		case "allow":
			if mode == "readonly" && mutatingClientTool(toolName) {
				return newToolError("PERMISSION_DENIED", "当前审批模式为只读，拒绝执行 %s", canonicalClientToolName(toolName))
			}
			return nil
		case "ask":
			if mode == "auto" {
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
	if canonicalClientToolName(toolName) == "skill" {
		if internalconfig.NormalizeSkillReviewPolicy(config.SkillReviewPolicy) != internalconfig.SkillReviewPolicyConfirm {
			return nil
		}
		if mode == "auto" {
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
	if !mutatingClientTool(toolName) {
		return nil
	}
	if mode == "readonly" {
		return newToolError("PERMISSION_DENIED", "当前审批模式为只读，拒绝执行 %s", toolName)
	}
	if mode == "confirm" {
		approved, err := r.confirmClientTool(toolName, arguments)
		if err != nil {
			return err
		}
		if !approved {
			return newToolError("PERMISSION_DENIED", "用户拒绝执行 %s", toolName)
		}
	}
	return nil
}

func (r *ClientRuntime) confirmClientTool(toolName string, arguments map[string]any) (bool, error) {
	preview := previewClientTool(toolName, arguments)
	fmt.Fprintf(r.stdout, "\n[客户端工具]\n%s\n是否执行？[y/N]: ", preview)
	reader := bufio.NewReader(r.stdin)
	answerLine, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, fs.ErrClosed) {
		if !errors.Is(err, bytes.ErrTooLarge) {
			if !errors.Is(err, io.EOF) {
				return false, fmt.Errorf("读取工具确认输入失败: %w", err)
			}
		}
	}
	answer := strings.TrimSpace(strings.ToLower(answerLine))
	return answer == "y" || answer == "yes", nil
}

func shouldConfirmMutatingTool(config internalconfig.Config, toolName string) bool {
	if action, matched := evaluatePermissionAction(config, toolName, ""); matched {
		return action == "ask"
	}
	return strings.TrimSpace(config.ApprovalMode) == "confirm" && mutatingClientTool(toolName)
}

func mutatingClientTool(toolName string) bool {
	switch canonicalClientToolName(toolName) {
	case "shell_exec", "task_stop", "task_delete", "task_cleanup", "todo_write", "file_edit", "document_unstructured", "write_file":
		return true
	default:
		return false
	}
}

func isWriteConfirmationTool(toolName string) bool {
	switch canonicalClientToolName(toolName) {
	case "file_edit", "write_file":
		return true
	default:
		return false
	}
}

func previewClientTool(toolName string, arguments map[string]any) string {
	path := argumentString(arguments, "path", "filePath")
	name := argumentString(arguments, "name")
	switch canonicalClientToolName(toolName) {
	case "ask_user":
		questions := 0
		if rawQuestions, ok := arguments["questions"].([]any); ok {
			questions = len(rawQuestions)
		}
		return fmt.Sprintf("ask_user %d 题", questions)
	case "lsp_query":
		operation := fallbackString(argumentString(arguments, "operation"), "<missing>")
		return fmt.Sprintf("lsp_query %s (%s)", operation, fallbackString(argumentString(arguments, "serverCommand", "server_command"), "<missing>"))
	case "glob_files":
		return fmt.Sprintf("glob_files %s @ %s", fallbackString(argumentString(arguments, "pattern"), "<missing>"), fallbackString(argumentString(arguments, "path"), "."))
	case "todo_write":
		items := 0
		if rawItems, ok := arguments["items"].([]any); ok {
			items = len(rawItems)
		}
		return fmt.Sprintf("todo_write %d 项", items)
	case "file_edit":
		return fmt.Sprintf("file_edit %s", path)
	case "shell_exec":
		return fmt.Sprintf("shell_exec %s", fallbackString(commandPreview(arguments), "<missing>"))
	case "task_output":
		return fmt.Sprintf("task_output %s", fallbackString(stringValue(arguments["task_id"]), "<missing>"))
	case "task_stop":
		return fmt.Sprintf("task_stop %s", fallbackString(stringValue(arguments["task_id"]), "<missing>"))
	case "task_delete":
		return fmt.Sprintf("task_delete %s", fallbackString(stringValue(arguments["task_id"]), "<missing>"))
	case "task_cleanup":
		return "task_cleanup"
	case "task_list":
		return "task_list"
	case "terminal_capture":
		return fmt.Sprintf("terminal_capture %s", fallbackString(stringValue(arguments["task_id"]), "<missing>"))
	case "write_file":
		return fmt.Sprintf("write_file %s (%s)", path, fallbackString(stringValue(arguments["mode"]), "overwrite"))
	case "read_file", "search_files":
		return fmt.Sprintf("%s %s", toolName, fallbackString(path, "."))
	case "document_unstructured":
		paths := argumentStringSlice(arguments, "paths")
		if singlePath := argumentString(arguments, "path", "file_path"); singlePath != "" {
			paths = append([]string{singlePath}, paths...)
		}
		if len(paths) == 0 {
			return "document_unstructured <missing>"
		}
		if len(paths) == 1 {
			return fmt.Sprintf("document_unstructured %s", paths[0])
		}
		return fmt.Sprintf("document_unstructured %d 文件", len(paths))
	case "skill":
		return fmt.Sprintf("skill %s", fallbackString(name, "<missing>"))
	default:
		return toolName
	}
}

func (r *ClientRuntime) readFile(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	targetPath := strings.TrimSpace(stringValue(arguments["path"]))
	if targetPath == "" {
		return nil, newToolError("PARAM_ERROR", "read_file 缺少 path")
	}
	resolvedPath, err := r.resolveAllowedFilePathWithPolicy(config, targetPath, false)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取文件失败: %v", err)
	}
	if looksBinaryContent(content) {
		return map[string]any{
			"path":         resolvedPath,
			"binary":       true,
			"size":         len(content),
			"content_type": "binary",
		}, nil
	}
	startLine := intValue(arguments["start_line"], 1)
	if startLine < 1 {
		startLine = 1
	}
	lines := splitLines(string(content))
	endLine := intValue(arguments["end_line"], len(lines))
	if endLine < startLine {
		endLine = startLine
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}
	if startLine > len(lines) && len(lines) > 0 {
		startLine = len(lines)
	}
	selected := ""
	if len(lines) > 0 && startLine <= endLine {
		selected = strings.Join(lines[startLine-1:endLine], "\n")
	}
	maxBytes := intValue(arguments["max_bytes"], maxReadBytesDefault)
	if maxBytes < 1 {
		maxBytes = maxReadBytesDefault
	}
	if maxBytes > maxReadBytesHard {
		maxBytes = maxReadBytesHard
	}
	truncated, wasTruncated := truncateUTF8(selected, maxBytes)
	r.writeStreamEventf(r.stdout, "[本地工具] read_file %s 行 %d-%d\n", resolvedPath, startLine, endLine)
	_ = appendAuditLog(config, "read_file", map[string]any{"path": resolvedPath, "start_line": startLine, "end_line": endLine})
	return map[string]any{
		"path":         resolvedPath,
		"content":      truncated,
		"binary":       false,
		"truncated":    wasTruncated,
		"start_line":   startLine,
		"end_line":     endLine,
		"total_lines":  len(lines),
		"total_bytes":  len(content),
		"content_type": "text/utf-8",
	}, nil
}

func (r *ClientRuntime) writeFile(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	targetPath := strings.TrimSpace(stringValue(arguments["path"]))
	if targetPath == "" {
		return nil, newToolError("PARAM_ERROR", "write_file 缺少 path")
	}
	mode := strings.ToLower(strings.TrimSpace(fallbackString(stringValue(arguments["mode"]), "overwrite")))
	if mode != "create" && mode != "overwrite" && mode != "append" {
		return nil, newToolError("PARAM_ERROR", "write_file mode 仅支持 create、overwrite、append")
	}
	resolvedPath, err := r.resolveAllowedFilePathWithPolicy(config, targetPath, true)
	if err != nil {
		return nil, err
	}
	content := []byte(stringValue(arguments["content"]))
	originalContent := []byte{}
	created := false
	if existing, readErr := os.ReadFile(resolvedPath); readErr == nil {
		originalContent = existing
	} else if os.IsNotExist(readErr) {
		created = true
	} else {
		return nil, newToolError("EXECUTION_FAILED", "读取目标文件失败: %v", readErr)
	}
	if mode == "create" && !created {
		return nil, newToolError("PARAM_ERROR", "目标文件已存在: %s", resolvedPath)
	}
	finalContent := content
	if mode == "append" {
		finalContent = append(originalContent, content...)
	}
	if err := writeFileAtomically(resolvedPath, finalContent); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "写入文件失败: %v", err)
	}
	r.writeStreamEventf(r.stdout, "[本地工具] write_file %s (%s，%d 字节)\n", resolvedPath, mode, len(finalContent))
	_ = appendAuditLog(config, "write_file", map[string]any{"path": resolvedPath, "mode": mode, "size": len(finalContent)})
	return map[string]any{
		"path":          resolvedPath,
		"mode":          mode,
		"created":       created,
		"bytes_written": len(finalContent),
		"size":          len(finalContent),
	}, nil
}

func (r *ClientRuntime) skill(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	name := argumentString(arguments, "name")
	if name == "" {
		return nil, newToolError("PARAM_ERROR", "skill 缺少 name")
	}
	item, _, err := skills.LoadByName(config, name)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "%v", err)
	}
	return map[string]any{
		"name":         item.Name,
		"description":  item.Description,
		"input":        argumentString(arguments, "input"),
		"prompt":       item.Body,
		"markdown":     skills.Markdown(*item),
		"path":         item.Path,
		"skill_dir":    skills.SkillDir(*item),
		"root_dir":     item.RootDir,
		"source":       item.Source,
		"relative_dir": item.RelativeDir,
		"files":        item.Files,
		"kind":         "prompt",
	}, nil
}

func (r *ClientRuntime) searchFilesLocal(_ context.Context, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	pattern := argumentString(arguments, "pattern", "query")
	if pattern == "" {
		return nil, newToolError("PARAM_ERROR", "文件搜索缺少关键词")
	}
	baseDir, err := r.resolveAllowedDirectoryWithPolicy(config, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}
	maxResults := argumentInt(arguments, 50, "maxResults", "max_results")
	if maxResults < 1 {
		maxResults = 1
	}
	if maxResults > 200 {
		maxResults = 200
	}
	offset := intValue(arguments["offset"], 0)
	if offset < 0 {
		offset = 0
	}
	includeHidden := argumentBool(arguments, "includeHidden", "include_hidden")
	includePatterns := normalizePatternList(argumentStringSlice(arguments, "includePatterns", "include_patterns"), strings.TrimSpace(stringValue(arguments["glob"])))
	excludePatterns := normalizePatternList(argumentStringSlice(arguments, "excludePatterns", "exclude_patterns"))
	matcher, err := buildSearchMatcher(pattern, argumentBool(arguments, "isRegexp", "is_regexp"), argumentBool(arguments, "caseSensitive", "case_sensitive"))
	if err != nil {
		return nil, newToolError("PARAM_ERROR", "%v", err)
	}
	outputMode := strings.ToLower(strings.TrimSpace(fallbackString(argumentString(arguments, "outputMode", "output_mode"), "content")))
	if outputMode != "content" && outputMode != "files_with_matches" && outputMode != "count" {
		return nil, newToolError("PARAM_ERROR", "search_files 的输出模式(output mode)仅支持 content、files_with_matches、count")
	}
	total := 0
	hasMore := false
	results := []map[string]any{}
	fileCounts := map[string]int{}
	stopSearch := errors.New("search_limit_reached")
	err = filepath.WalkDir(baseDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != baseDir {
				relativePath, _ := filepath.Rel(baseDir, path)
				if isHiddenPath(relativePath) && !includeHidden {
					return filepath.SkipDir
				}
			}
			return nil
		}
		relativePath, _ := filepath.Rel(baseDir, path)
		if isHiddenPath(relativePath) && !includeHidden {
			return nil
		}
		if !matchesPatterns(relativePath, includePatterns, excludePatterns) {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil || looksBinaryContent(content) {
			return nil
		}
		for lineNumber, line := range splitLines(string(content)) {
			if !matcher(line) {
				continue
			}
			fileCounts[filepath.ToSlash(relativePath)]++
			if total >= offset && len(results) < maxResults {
				results = append(results, map[string]any{
					"path":          path,
					"relative_path": filepath.ToSlash(relativePath),
					"line":          lineNumber + 1,
					"text":          line,
				})
			} else if len(results) >= maxResults {
				hasMore = true
				return stopSearch
			}
			total++
		}
		return nil
	})
	if err != nil && !errors.Is(err, stopSearch) {
		return nil, newToolError("EXECUTION_FAILED", "文件搜索失败: %v", err)
	}
	r.writeStreamEventf(r.stdout, "[本地工具] search_files %s 命中 %d 条\n", baseDir, len(results))
	_ = appendAuditLog(config, "search_files", map[string]any{"path": baseDir, "pattern": pattern, "count": len(results), "output_mode": outputMode})
	response := map[string]any{
		"matches":       results,
		"count":         len(results),
		"total_matches": total,
		"offset":        offset,
		"has_more":      hasMore,
		"root":          baseDir,
		"engine":        "go",
		"output_mode":   outputMode,
	}
	if outputMode == "files_with_matches" {
		files := make([]map[string]any, 0, len(fileCounts))
		for relativePath, count := range fileCounts {
			files = append(files, map[string]any{"relative_path": relativePath, "count": count})
		}
		sort.Slice(files, func(left, right int) bool {
			return stringValue(files[left]["relative_path"]) < stringValue(files[right]["relative_path"])
		})
		response["files"] = files
		response["count"] = len(files)
		return response, nil
	}
	if outputMode == "count" {
		response["count"] = total
		response["matches"] = []map[string]any{}
		return response, nil
	}
	return response, nil
}

func buildSearchMatcher(query string, isRegexp bool, caseSensitive bool) (func(string) bool, error) {
	if isRegexp {
		pattern := query
		if !caseSensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("正则表达式无效: %w", err)
		}
		return re.MatchString, nil
	}
	needle := query
	if !caseSensitive {
		needle = strings.ToLower(needle)
	}
	return func(line string) bool {
		candidate := line
		if !caseSensitive {
			candidate = strings.ToLower(candidate)
		}
		return strings.Contains(candidate, needle)
	}, nil
}

func normalizePatternList(items []string, extra ...string) []string {
	result := []string{}
	for _, item := range append(items, extra...) {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func matchesPatterns(path string, includes []string, excludes []string) bool {
	normalized := filepath.ToSlash(path)
	if len(includes) > 0 {
		matched := false
		for _, pattern := range includes {
			if matchGlob(pattern, normalized) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for _, pattern := range excludes {
		if matchGlob(pattern, normalized) {
			return false
		}
	}
	return true
}

func expandGlobPatterns(config internalconfig.Config, pattern string) []string {
	normalizedPattern := filepath.ToSlash(strings.TrimSpace(pattern))
	if normalizedPattern == "" {
		return nil
	}
	items, _, err := skills.Discover(config)
	if err != nil || len(items) == 0 {
		return []string{normalizedPattern}
	}
	aliasMap := buildSkillSegmentAliasMap(items)
	if len(aliasMap) == 0 {
		return []string{normalizedPattern}
	}
	variants := []string{""}
	for _, segment := range strings.Split(normalizedPattern, "/") {
		options := []string{segment}
		if !containsGlobMeta(segment) {
			if aliases := aliasMap[strings.ToLower(segment)]; len(aliases) > 0 {
				options = append(options, aliases...)
			}
		}
		next := make([]string, 0, len(variants)*len(options))
		seen := map[string]struct{}{}
		for _, prefix := range variants {
			for _, option := range options {
				candidate := option
				if prefix != "" {
					candidate = prefix + "/" + option
				}
				if _, ok := seen[candidate]; ok {
					continue
				}
				seen[candidate] = struct{}{}
				next = append(next, candidate)
				if len(next) >= 32 {
					break
				}
			}
			if len(next) >= 32 {
				break
			}
		}
		variants = next
		if len(variants) == 0 {
			return []string{normalizedPattern}
		}
	}
	return variants
}

func buildSkillSegmentAliasMap(items []skills.Skill) map[string][]string {
	aliasSets := map[string]map[string]struct{}{}
	for _, item := range items {
		candidates := []string{}
		for _, candidate := range []string{
			strings.TrimSpace(item.Name),
			filepath.Base(strings.TrimSpace(item.RelativeDir)),
			filepath.Base(filepath.Dir(strings.TrimSpace(item.Path))),
		} {
			trimmed := strings.TrimSpace(candidate)
			if trimmed == "" || trimmed == "." || strings.Contains(trimmed, "/") || strings.Contains(trimmed, `\`) {
				continue
			}
			candidates = append(candidates, trimmed)
		}
		if len(candidates) < 2 {
			continue
		}
		uniqueCandidates := []string{}
		seenCandidates := map[string]struct{}{}
		for _, candidate := range candidates {
			key := strings.ToLower(candidate)
			if _, ok := seenCandidates[key]; ok {
				continue
			}
			seenCandidates[key] = struct{}{}
			uniqueCandidates = append(uniqueCandidates, candidate)
		}
		for _, candidate := range uniqueCandidates {
			key := strings.ToLower(candidate)
			if aliasSets[key] == nil {
				aliasSets[key] = map[string]struct{}{}
			}
			for _, alias := range uniqueCandidates {
				aliasSets[key][alias] = struct{}{}
			}
		}
	}
	result := map[string][]string{}
	for key, candidates := range aliasSets {
		items := make([]string, 0, len(candidates))
		for candidate := range candidates {
			items = append(items, candidate)
		}
		sort.Strings(items)
		result[key] = items
	}
	return result
}

func containsGlobMeta(segment string) bool {
	return strings.ContainsAny(segment, "*?[")
}

func matchAnyGlob(patterns []string, path string) bool {
	for _, pattern := range patterns {
		if matchGlob(pattern, path) {
			return true
		}
	}
	return false
}

func matchGlob(pattern string, path string) bool {
	normalizedPattern := filepath.ToSlash(strings.TrimSpace(pattern))
	normalizedPath := filepath.ToSlash(strings.TrimSpace(path))
	if normalizedPattern == "" || normalizedPath == "" {
		return false
	}
	if !strings.Contains(normalizedPattern, "/") {
		matched, err := filepath.Match(normalizedPattern, filepath.Base(normalizedPath))
		return err == nil && matched
	}
	return matchGlobSegments(strings.Split(normalizedPattern, "/"), strings.Split(normalizedPath, "/"))
}

func matchGlobSegments(patternSegments []string, pathSegments []string) bool {
	for len(patternSegments) > 0 && patternSegments[0] == "" {
		patternSegments = patternSegments[1:]
	}
	for len(pathSegments) > 0 && pathSegments[0] == "" {
		pathSegments = pathSegments[1:]
	}
	if len(patternSegments) == 0 {
		return len(pathSegments) == 0
	}
	if patternSegments[0] == "**" {
		for len(patternSegments) > 1 && patternSegments[1] == "**" {
			patternSegments = patternSegments[1:]
		}
		if len(patternSegments) == 1 {
			return true
		}
		for index := 0; index <= len(pathSegments); index++ {
			if matchGlobSegments(patternSegments[1:], pathSegments[index:]) {
				return true
			}
		}
		return false
	}
	if len(pathSegments) == 0 {
		return false
	}
	matched, err := filepath.Match(patternSegments[0], pathSegments[0])
	if err != nil || !matched {
		return false
	}
	return matchGlobSegments(patternSegments[1:], pathSegments[1:])
}

func splitLines(content string) []string {
	trimmed := strings.ReplaceAll(content, "\r\n", "\n")
	if trimmed == "" {
		return []string{}
	}
	trimmed = strings.TrimSuffix(trimmed, "\n")
	return strings.Split(trimmed, "\n")
}

func truncateUTF8(content string, maxBytes int) (string, bool) {
	if len(content) <= maxBytes {
		return content, false
	}
	truncated := content[:maxBytes]
	for len(truncated) > 0 && !utf8.ValidString(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated, true
}

func looksBinaryContent(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	limit := len(content)
	if limit > 8192 {
		limit = 8192
	}
	sample := content[:limit]
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	return !utf8.Valid(sample)
}

func looksBinaryEditableContent(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	limit := len(content)
	if limit > 8192 {
		limit = 8192
	}
	sample := content[:limit]
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	if utf8.Valid(sample) {
		return false
	}
	controlCount := 0
	for _, value := range sample {
		switch value {
		case '\n', '\r', '\t', '\f':
			continue
		}
		if value < 0x20 || value == 0x7f {
			controlCount++
		}
	}
	return controlCount > 0 && controlCount*10 > len(sample)
}

func writeFileAtomically(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(path), ".taiyiflow-*")
	if err != nil {
		return err
	}
	temporaryPath := temporaryFile.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporaryFile.Write(content); err != nil {
		temporaryFile.Close()
		return err
	}
	if err := temporaryFile.Chmod(0o644); err != nil {
		temporaryFile.Close()
		return err
	}
	if err := temporaryFile.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func fallbackString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func trimMultilinePreview(value string, maxLen int) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(value, "\n", " / "))
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= maxLen {
		return trimmed
	}
	if maxLen < 4 {
		return trimmed[:maxLen]
	}
	return trimmed[:maxLen-3] + "..."
}
