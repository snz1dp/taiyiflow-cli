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

	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	unicodeenc "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

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
	case "switch_to_agent":
		return "switch_to_agent"
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
		"执行结构化本地 Shell 命令，支持前台执行或后台任务句柄返回。当前 OS: %s/%s。按当前 OS 选择命令，不要假设 Linux、macOS、Windows 的命令语义等价。优先使用 command + args，不要把整条命令塞进 command；示例: %s。只有需要管道、重定向或 shell 内建命令时才使用 rawCommand；示例: %s。需要命令输入时，优先显式提供 stdin；若希望客户端在执行前向用户收集输入，可提供 stdin_prompt。",
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
					"is_artifact":            map[string]any{"type": "boolean", "default": true, "description": "标记本次编辑是否为项目产物。设为 false 时不会出现在变更列表中（适用于临时文件、中间产物等）。"},
				},
				"required":             []string{"path", "old_string", "new_string"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "shell_exec",
			"description":          shellExecToolDescription() + " 可选传入 source_skill（或由上下文注入）以启用受控环境变量自动注入：runtime 只会读取该 skill metadata.requires.env 中声明的键，并仅从当前进程已存在的环境变量补入。",
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
					"stdin":            map[string]any{"type": "string", "description": "可选标准输入(stdin)内容；提供后会直接送给子进程，而不是复用宿主输入流。"},
					"stdinPrompt":      map[string]any{"type": "string", "description": "可选输入提示；当未显式提供 stdin 时，客户端会先向用户收集一行输入，再把该内容写入子进程 stdin。"},
					"source_skill":     map[string]any{"type": "string", "description": "可选技能名称；提供后会按该 skill metadata.requires.env 自动补齐允许的环境变量。"},
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
			"description":          "列出当前会话仍在运行的本地后台任务。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		{
			"name":                 "task_delete",
			"description":          "删除当前会话内仍保留的已结束后台任务及其临时输出。",
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
			"description":          "批量清理当前会话内尚未释放的已结束后台任务，保留运行中的任务。",
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
			"name":                 "run_subtask",
			"description":          "创建并执行子智能体任务。适用场景：1) 并行搜索多个目录或代码面；2) 把独立验证任务隔离到单独上下文执行；3) 长链路任务拆成多个子任务并行推进。支持单子任务（传 prompt）和批量并行（传 subtasks 数组）。子任务拥有与主任务相同的工具能力（shell、文件读写、搜索等）。不要为简单单文件修改或强耦合连续编辑拆分。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt":              map[string]any{"type": "string", "description": "子任务提示词，描述子任务要完成的具体目标。"},
					"subtasks":            map[string]any{"type": "array", "description": "批量并行子任务列表；每项可为字符串 prompt 或完整参数对象。提供后自动进入并行调度模式。", "items": map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "object"}}}},
					"flow_id":             map[string]any{"type": "string", "description": "子任务所属智能体 ID；默认继承当前 flow。"},
					"request_mode":        map[string]any{"type": "string", "enum": []string{"ask", "plan", "agent"}, "description": "子任务请求模式；默认 agent。"},
					"working_directory":   map[string]any{"type": "string", "description": "子任务工作目录；默认继承当前上下文目录。可为不同子任务指定不同目录。"},
					"target_client_id":    map[string]any{"type": "string", "description": "目标客户端 ID；默认当前 CLI client_id。"},
					"target_device_id":    map[string]any{"type": "string", "description": "目标设备 ID；默认当前 CLI device_id。"},
					"delivery_policy":     map[string]any{"type": "string", "description": "协同任务投递策略；默认 local_default。"},
					"wait_for_completion": map[string]any{"type": "boolean", "description": "远端子任务是否等待完成；默认 true。本地串行执行始终等待。"},
					"timeout_seconds":     map[string]any{"type": "integer", "minimum": 1, "maximum": internalconfig.MaxCommandTimeoutSeconds, "description": "本地执行或远端等待的超时时间。"},
					"parallel":            map[string]any{"type": "boolean", "description": "是否启用批量并行调度；默认在提供 subtasks 时开启。"},
					"max_parallelism":     map[string]any{"type": "integer", "minimum": 1, "maximum": 32, "description": "批量子任务的最大并发度；默认等于 subtasks 数量。"},
					"serial":              map[string]any{"type": "boolean", "description": "兼容旧参数；在批量模式下传 true 会强制串行执行。"},
				},
				"anyOf":                []any{map[string]any{"required": []string{"prompt"}}, map[string]any{"required": []string{"subtasks"}}},
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
			"name":                 "image_understanding",
			"description":          "上传本地图像文件到服务端图像理解流程(image_flow_id)并返回纯文本结果；Browser 自动化与 ComputerUse 的截图增强也复用同一能力。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":              map[string]any{"type": "string", "description": "单个本地图像路径；相对路径基于 working_directory。"},
					"paths":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "多个本地图像路径；与 path 可同时使用。"},
					"input":             map[string]any{"type": "string", "description": "可选图像理解问题或补充说明。"},
					"input_type":        map[string]any{"type": "string", "description": "可选输入类型；默认 any。"},
					"working_directory": map[string]any{"type": "string", "description": "解析相对路径的基准目录。"},
					"flow_id":           map[string]any{"type": "string", "description": "当前会话所属智能体 ID；通常由上下文自动注入。"},
					"session_id":        map[string]any{"type": "string", "description": "当前会话 ID；通常由上下文自动注入。"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "document_unstructured",
			"description":          "上传本地非图片文件到服务端文档结构化流程(doc_unstructured_flow_id)并返回稳定 JSON 结果；图片理解默认由 image_understanding 承载，具体适用场景由当前技能提示、配置和服务端能力共同决定。",
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
					"path":        map[string]any{"type": "string", "description": "目标文件路径。"},
					"content":     map[string]any{"type": "string", "description": "要写入的文本内容。"},
					"mode":        map[string]any{"type": "string", "enum": []string{"create", "overwrite", "append"}},
					"is_artifact": map[string]any{"type": "boolean", "default": true, "description": "标记本次写入是否为项目产物。设为 false 时不会出现在变更列表中（适用于临时文件、中间产物等）。"},
				},
				"required":             []string{"path", "content"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "skill",
			"description":          "按名称装载本地技能(skill)，返回可立即执行的技能提示内容(prompt)，并附带 source_skill、requires_env、metadata 等上下文，供后续 shell_exec 等工具做受控能力补全。",
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
		{
			"name":                 "skill_create",
			"description":          "在工作区目录下创建技能包骨架文件 SKILL.md。默认写入 <workdir>/.snz1dp/skills/<slug>/SKILL.md；传入 scope=\"user\" 时写入 ~/.snz1dp/skills/。创建后可通过 write_file 编辑内容，或通过 skill_upload 上传到后端。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":      map[string]any{"type": "string", "description": "技能名称，会自动转换为 slug 风格目录名。"},
					"scope":     map[string]any{"type": "string", "enum": []string{"workspace", "user"}, "description": "可选，默认 workspace。workspace 写入工作区 .snz1dp/skills/，user 写入用户目录 ~/.snz1dp/skills/。"},
					"overwrite": map[string]any{"type": "boolean", "default": false, "description": "同名技能已存在时是否覆盖。默认 false，不覆盖时同名技能已存在会报错。"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "skill_upload",
			"description":          "将本地已创建的技能上传到后台服务，使其成为后端注册的自定义技能。上传后技能可在其他设备上同步安装。需要提供本地技能名称（与 skill_create 时使用的 name 一致）。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "要上传的本地技能名称。"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "skill_delete",
			"description":          "删除本地已创建的技能目录。默认删除工作区范围（<workdir>/.snz1dp/skills/<slug>/）下的技能；传入 scope=\"user\" 时删除用户目录（~/.snz1dp/skills/<slug>/）下的技能。仅删除本地文件，不影响已上传到后端的技能。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string", "description": "要删除的技能名称，会自动转换为 slug 风格目录名。"},
					"scope": map[string]any{"type": "string", "enum": []string{"workspace", "user"}, "description": "可选，默认 workspace。workspace 删除工作区 .snz1dp/skills/ 下的技能，user 删除用户目录 ~/.snz1dp/skills/ 下的技能。"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
	}
	definitions = append(definitions, browserClientToolDefinitions()...)
	definitions = append(definitions, computerClientToolDefinitions()...)
	if !r.interactivePromptsEnabled {
		filtered := definitions[:0]
		for _, definition := range definitions {
			name := strings.TrimSpace(stringValue(definition["name"]))
			if name == "ask_user" {
				continue
			}
			filtered = append(filtered, definition)
		}
		definitions = filtered
	}
	if !r.subtaskEnabled {
		filtered := definitions[:0]
		for _, definition := range definitions {
			name := strings.TrimSpace(stringValue(definition["name"]))
			if name == "run_subtask" {
				continue
			}
			filtered = append(filtered, definition)
		}
		definitions = filtered
	}
	if provider.NormalizeRequestType(mode) == provider.RequestTypeAgent {
		// agent 模式：直接返回全部工具定义，不包含 switch_to_agent（未加入基础列表）。
		return definitions
	}
	// 非 agent 模式（ask/plan）：追加 switch_to_agent 工具定义。
	definitions = append(definitions, map[string]any{
		"name":                 "switch_to_agent",
		"description":          "切换当前会话到执行模式（agent）。在问答模式（ask）或计划模式（plan）下，模型可调用此工具切换到执行模式，以便使用完整的本地工具能力。切换后对后续对话生效，当前对话不受影响。返回值包含 agent 模式新增的工具完整定义（additional_tools），包括工具名称、描述、参数格式（inputSchema）和是否需要等待响应（waiting_for_response），便于模型了解切换后可用的工具及其调用方式。",
		"waiting_for_response": true,
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"reason": map[string]any{"type": "string", "description": "切换到执行模式的原因。"},
			},
			"additionalProperties": false,
		},
	})
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
		"ask_user":                  true,
		"lsp_query":                 true,
		"glob_files":                true,
		"task_output":               true,
		"task_list":                 true,
		"terminal_capture":          true,
		"search_files":              true,
		"read_file":                 true,
		"skill":                     true,
		"skill_create":              true,
		"skill_upload":              true,
		"skill_delete":              true,
		"switch_to_agent":           true,
		browserToolTabsContext:      true,
		browserToolReadText:         true,
		browserToolReadConsole:      true,
		computerToolDisplaysContext: true,
		computerToolWindowsContext:  true,
		computerToolScreenshot:      true,
	}
}

func RequiresClientToolConfirmation(config internalconfig.Config, payload map[string]any) bool {
	toolName := canonicalClientToolName(nestedString(payload, "function", "name"))
	arguments := nestedMap(payload, "function", "arguments")
	if isBrowserTool(toolName) {
		return browserToolRequiresConfirmation(config, toolName, arguments)
	}
	if isComputerTool(toolName) {
		return computerToolRequiresConfirmation(config, toolName, arguments)
	}
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
	if toolName == "switch_to_agent" {
		return internalconfig.NormalizeModeSwitchConfirmationPolicy(config.ModeSwitchConfirmationPolicy) == internalconfig.ModeSwitchConfirmationPolicyConfirm
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
	if isBrowserTool(toolName) {
		return r.authorizeBrowserTool(toolName, arguments, config)
	}
	if isComputerTool(toolName) {
		return r.authorizeComputerTool(toolName, arguments, config)
	}
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
	if canonicalClientToolName(toolName) == "switch_to_agent" {
		if internalconfig.NormalizeModeSwitchConfirmationPolicy(config.ModeSwitchConfirmationPolicy) != internalconfig.ModeSwitchConfirmationPolicyConfirm {
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
	if !r.interactivePromptsEnabled {
		return false, newToolError("PERMISSION_DENIED", "当前 task 模式不支持交互式工具确认，请预先放行所需工具")
	}
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
	if isBrowserTool(toolName) {
		return !browserReadOnlyTool(toolName)
	}
	if isComputerTool(toolName) {
		return !computerReadOnlyTool(toolName)
	}
	switch canonicalClientToolName(toolName) {
	case "shell_exec", "task_stop", "task_delete", "task_cleanup", "todo_write", "file_edit", "document_unstructured", "write_file", "skill_create", "skill_upload", "skill_delete":
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
	if isBrowserTool(toolName) {
		return previewBrowserTool(toolName, arguments)
	}
	if isComputerTool(toolName) {
		return previewComputerTool(toolName, arguments)
	}
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
	case "run_subtask":
		if rawSubtasks, ok := arguments["subtasks"].([]any); ok && len(rawSubtasks) > 0 {
			return fmt.Sprintf("run_subtask %d 项", len(rawSubtasks))
		}
		return fmt.Sprintf("run_subtask %s", fallbackString(argumentString(arguments, "flow_id"), "<current-flow>"))
	case "write_file":
		return fmt.Sprintf("write_file %s (%s)", path, fallbackString(stringValue(arguments["mode"]), "overwrite"))
	case "read_file", "search_files":
		return fmt.Sprintf("%s %s", toolName, fallbackString(path, "."))
	case "image_understanding":
		paths := argumentStringSlice(arguments, "paths")
		if singlePath := argumentString(arguments, "path", "file_path"); singlePath != "" {
			paths = append([]string{singlePath}, paths...)
		}
		if len(paths) == 0 {
			return "image_understanding <missing>"
		}
		if len(paths) == 1 {
			return fmt.Sprintf("image_understanding %s", paths[0])
		}
		return fmt.Sprintf("image_understanding %d 图片", len(paths))
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
	case "skill_create":
		scope := argumentString(arguments, "scope")
		if scope == "" {
			scope = "workspace"
		}
		overwrite := argumentBool(arguments, "overwrite")
		if overwrite {
			return fmt.Sprintf("skill_create %s (%s, overwrite)", fallbackString(name, "<missing>"), scope)
		}
		return fmt.Sprintf("skill_create %s (%s)", fallbackString(name, "<missing>"), scope)
	case "skill_upload":
		return fmt.Sprintf("skill_upload %s", fallbackString(name, "<missing>"))
	case "skill_delete":
		scope := argumentString(arguments, "scope")
		if scope == "" {
			scope = "workspace"
		}
		return fmt.Sprintf("skill_delete %s (%s)", fallbackString(name, "<missing>"), scope)
	case "switch_to_agent":
		reason := argumentString(arguments, "reason")
		if reason != "" {
			return fmt.Sprintf("switch_to_agent: %s", reason)
		}
		return "switch_to_agent"
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
	// 非 UTF-8 文本文件（GBK、Big5、Shift-JIS 等）自动转码为 UTF-8
	textContent := string(content)
	contentEncoding := "utf-8"
	if !utf8.Valid(content) {
		textContent, contentEncoding = decodeNonUTF8ToUTF8(content)
	}
	startLine := intValue(arguments["start_line"], 1)
	if startLine < 1 {
		startLine = 1
	}
	lines := splitLines(textContent)
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
	_ = appendAuditLog(config, "read_file", map[string]any{"path": resolvedPath, "start_line": startLine, "end_line": endLine, "encoding": contentEncoding})
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
		"encoding":     contentEncoding,
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
	isArtifact := true
	if value, ok := arguments["is_artifact"]; ok {
		isArtifact = boolValue(value)
	}
	if !isArtifact {
		r.trackNonArtifactFile(resolvedPath)
	}
	r.writeStreamEventf(r.stdout, "[本地工具] write_file %s (%s，%d 字节)\n", resolvedPath, mode, len(finalContent))
	_ = appendAuditLog(config, "write_file", map[string]any{"path": resolvedPath, "mode": mode, "size": len(finalContent), "is_artifact": isArtifact})
	return map[string]any{
		"path":          resolvedPath,
		"mode":          mode,
		"created":       created,
		"bytes_written": len(finalContent),
		"size":          len(finalContent),
		"is_artifact":   isArtifact,
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
		"homepage":     item.Homepage,
		"input":        argumentString(arguments, "input"),
		"prompt":       item.Body,
		"markdown":     skills.Markdown(*item),
		"path":         item.Path,
		"skill_dir":    skills.SkillDir(*item),
		"root_dir":     item.RootDir,
		"source":       item.Source,
		"source_skill": item.Name,
		"relative_dir": item.RelativeDir,
		"files":        item.Files,
		"metadata":     item.Metadata,
		"requires_env": skills.RequiredEnvKeys(*item),
		"kind":         "prompt",
	}, nil
}

func (r *ClientRuntime) skillCreate(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	name := argumentString(arguments, "name")
	if name == "" {
		return nil, newToolError("PARAM_ERROR", "skill_create 缺少 name")
	}
	scope := argumentString(arguments, "scope")
	overwrite := argumentBool(arguments, "overwrite")
	result, err := skills.CreateSkillScaffold(config, name, scope, overwrite)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "%v", err)
	}
	r.writeStreamEventf(r.stdout, "[本地工具] skill_create %s -> %s\n", result.Name, result.FilePath)
	_ = appendAuditLog(config, "skill_create", map[string]any{
		"name":      result.Name,
		"slug":      result.Slug,
		"file_path": result.FilePath,
		"skill_dir": result.SkillDir,
		"scope":     result.Scope,
	})
	return map[string]any{
		"name":      result.Name,
		"slug":      result.Slug,
		"file_path": result.FilePath,
		"skill_dir": result.SkillDir,
		"scope":     result.Scope,
		"kind":      "created",
	}, nil
}

func (r *ClientRuntime) skillUpload(ctx context.Context, providerClient provider.Provider, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	name := argumentString(arguments, "name")
	if name == "" {
		return nil, newToolError("PARAM_ERROR", "skill_upload 缺少 name")
	}
	result, err := skills.UploadLocalSkillToBackend(ctx, providerClient, config, name)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "%v", err)
	}
	r.writeStreamEventf(r.stdout, "[本地工具] skill_upload %s -> skill_id=%s\n", result.Name, result.SkillID)
	_ = appendAuditLog(config, "skill_upload", map[string]any{
		"name":         result.Name,
		"skill_id":     result.SkillID,
		"display_name": result.DisplayName,
		"version":      result.Version,
	})
	return map[string]any{
		"name":         result.Name,
		"skill_id":     result.SkillID,
		"display_name": result.DisplayName,
		"version":      result.Version,
		"kind":         "uploaded",
	}, nil
}

func (r *ClientRuntime) skillDelete(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	name := argumentString(arguments, "name")
	if name == "" {
		return nil, newToolError("PARAM_ERROR", "skill_delete 缺少 name")
	}
	scope := argumentString(arguments, "scope")
	result, err := skills.DeleteSkillScaffold(config, name, scope)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "%v", err)
	}
	r.writeStreamEventf(r.stdout, "[本地工具] skill_delete %s -> removed %s\n", result.Name, result.SkillDir)
	_ = appendAuditLog(config, "skill_delete", map[string]any{
		"name":      result.Name,
		"slug":      result.Slug,
		"skill_dir": result.SkillDir,
		"scope":     result.Scope,
	})
	return map[string]any{
		"name":      result.Name,
		"slug":      result.Slug,
		"skill_dir": result.SkillDir,
		"scope":     result.Scope,
		"kind":      "deleted",
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
	excludeDirs := config.FileSearchExcludeDirs
	if len(excludeDirs) == 0 {
		excludeDirs = internalconfig.DefaultFileSearchExcludeDirs()
	}
	err = filepath.WalkDir(baseDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != baseDir {
				dirName := entry.Name()
				if shouldSkipDir(dirName, excludeDirs) {
					return filepath.SkipDir
				}
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
		// 非 UTF-8 文本文件（GBK、Big5、Shift-JIS 等）自动转码为 UTF-8 后再搜索
		textContent := string(content)
		if !utf8.Valid(content) {
			textContent, _ = decodeNonUTF8ToUTF8(content)
		}
		for lineNumber, line := range splitLines(textContent) {
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

// looksBinaryContent 判断文件内容是否为二进制（非文本）。
// 采用多层检测策略，兼容 UTF-8、UTF-16（含无 BOM）、GBK、Big5、Shift-JIS 等编码的文本文件：
//  1. BOM 正向识别（UTF-8 BOM / UTF-16 LE/BE BOM）
//  2. UTF-8 有效性检查
//  3. UTF-16 无 BOM 启发式检测（交替 null 字节 + ASCII 字节模式）
//  4. 多编码尝试解码（GBK/Big5/Shift-JIS 等，放在 null 字节检查之前避免误判）
//  5. Null 字节比例检测（仅对无法被任何编码识别的内容生效）
//  6. 控制字符比例检测
func looksBinaryContent(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	limit := len(content)
	if limit > 8192 {
		limit = 8192
	}
	sample := content[:limit]

	// 0. 截断边界修正：当 sample 是从更大文件中截取的固定长度样本时，
	// 末尾可能恰好落在一个 UTF-8 多字节字符的中间（如 0xe5 是 3 字节序列的首字节）。
	// 这会导致 utf8.Valid(sample) 返回 false，进而触发后续编码检测的误判。
	// 去除末尾不完整的 UTF-8 字节序列，确保后续 UTF-8 有效性检查的准确性。
	sample = trimTrailingIncompleteUTF8(sample)

	// 1. BOM 检测：有 BOM 的内容一定是文本
	if len(sample) >= 3 && sample[0] == 0xEF && sample[1] == 0xBB && sample[2] == 0xBF {
		return false // UTF-8 BOM
	}
	if len(sample) >= 2 {
		if (sample[0] == 0xFF && sample[1] == 0xFE) || (sample[0] == 0xFE && sample[1] == 0xFF) {
			return false // UTF-16 LE/BE BOM
		}
	}

	// 2. UTF-16 无 BOM 启发式检测：
	// UTF-16LE 的 ASCII 字符编码为 [byte, 0x00]，UTF-16BE 为 [0x00, byte]，
	// 导致约 50% 的 null 字节。必须在 null 字节比例检查之前识别，否则会被误判为二进制。
	if looksLikeUTF16LENoBOM(sample) || looksLikeUTF16BENoBOM(sample) {
		return false
	}

	// 3. Null 字节比例检测：真正的二进制文件（PNG/JPEG/ELF/ZIP 等）通常含有大量 null 字节，
	// 而 GBK/Big5/Shift-JIS 等文本编码极少包含 null 字节。
	// 阈值设为 10%：足以过滤二进制文件，同时不会误判偶尔包含 null 字节的文本文件。
	// （UTF-16 已在步骤 2 被识别，此处不会再误判）
	// 关键：必须在 utf8.Valid 之前执行，因为 null 字节（0x00）是有效 UTF-8（ASCII NUL），
	// utf8.Valid 会对全 null 或高 null 比例的二进制内容返回 true，导致误判为文本。
	nullCount := bytes.Count(sample, []byte{0})
	if nullCount > 0 && nullCount*10 > len(sample) {
		return true
	}

	// 4. 控制字符比例检测（排除常见文本控制字符 \n \r \t \f）
	// 同样必须在 utf8.Valid 之前执行，原因同上：控制字符（如 0x01-0x1F）也是有效 UTF-8。
	controlCount := 0
	for _, b := range sample {
		switch b {
		case '\n', '\r', '\t', '\f':
			continue
		}
		if b < 0x20 || b == 0x7f {
			controlCount++
		}
	}
	if controlCount > 0 && controlCount*10 > len(sample) {
		return true
	}

	// 5. 有效 UTF-8 一定是文本
	if utf8.Valid(sample) {
		return false
	}

	// 6. 多编码尝试解码：如果能被已知编码成功解码且替换字符极少，则认定为文本。
	// 此时 null 字节和控制字符比例均正常，可以安全地尝试编码解码。
	if tryDecodeAsText(sample) {
		return false
	}

	// 所有检查均未判定为二进制，视为文本
	return false
}

// tryDecodeAsText 尝试用常见非 UTF-8 编码解码样本内容，判断是否为可读文本。
// 注意：ISO-8859-1 和 Windows-1252 可以解码任意字节序列（替换字符恒为 0），
// 因此必须额外检查可打印字符比例，避免把二进制内容误判为文本。
func tryDecodeAsText(sample []byte) bool {
	// CJK 编码：这些编码有明确的无效字节序列，替换字符比例可以有效区分文本和二进制。
	// 必须在 charset.DetermineEncoding 之前尝试，因为 charset 对 CJK 内容可能返回 windows-1252。
	cjkCandidates := []encoding.Encoding{
		simplifiedchinese.GBK,
		simplifiedchinese.GB18030,
		traditionalchinese.Big5,
		japanese.ShiftJIS,
		japanese.EUCJP,
		korean.EUCKR,
	}
	for _, enc := range cjkCandidates {
		if enc == nil {
			continue
		}
		if decoded, err := decodeWithEncoding(enc, sample); err == nil && replacementRatio(decoded) < 0.02 {
			return true
		}
	}
	// charset.DetermineEncoding（基于字节频率分析）：
	// 可能返回 windows-1252 等"万能解码器"，必须额外检查可打印字符比例。
	if enc, _, _ := charset.DetermineEncoding(sample, ""); enc != nil {
		if decoded, err := decodeWithEncoding(enc, sample); err == nil &&
			replacementRatio(decoded) < 0.05 && printableRatio(decoded) > 0.80 {
			return true
		}
	}
	// Western 编码：ISO-8859-1/Windows-1252 可以解码任意字节（替换字符恒为 0），
	// 必须额外检查可打印字符比例，避免把二进制内容误判为 Western 文本。
	westernCandidates := []encoding.Encoding{
		charmap.ISO8859_1,
		charmap.Windows1252,
	}
	for _, enc := range westernCandidates {
		if enc == nil {
			continue
		}
		if decoded, err := decodeWithEncoding(enc, sample); err == nil &&
			replacementRatio(decoded) < 0.02 && printableRatio(decoded) > 0.80 {
			return true
		}
	}
	return false
}

// looksLikeUTF16LENoBOM 启发式检测无 BOM 的 UTF-16LE 编码内容。
// UTF-16LE 的 ASCII 字符编码为 [byte, 0x00]，因此偶数位为可打印字节、奇数位为 null 字节。
// 关键：必须同时要求 null 字节和可打印字节成对出现，
// 避免把"大量 null 字节的二进制文件"误判为 UTF-16LE 文本。
func looksLikeUTF16LENoBOM(sample []byte) bool {
	if len(sample) < 4 {
		return false
	}
	pairs := len(sample) / 2
	nullPrintablePairs := 0 // 奇数位为 null 且偶数位为可打印 ASCII 的配对数
	for i := 0; i < pairs; i++ {
		even := sample[i*2]
		odd := sample[i*2+1]
		if odd == 0x00 && (even >= 0x20 && even <= 0x7E || even == '\n' || even == '\r' || even == '\t') {
			nullPrintablePairs++
		}
	}
	// 至少 60% 的配对满足 [可打印, null] 模式，才能认定为 UTF-16LE
	return nullPrintablePairs > pairs*6/10
}

// looksLikeUTF16BENoBOM 启发式检测无 BOM 的 UTF-16BE 编码内容。
// UTF-16BE 的 ASCII 字符编码为 [0x00, byte]，因此偶数位为 null 字节、奇数位为可打印字节。
func looksLikeUTF16BENoBOM(sample []byte) bool {
	if len(sample) < 4 {
		return false
	}
	pairs := len(sample) / 2
	nullPrintablePairs := 0 // 偶数位为 null 且奇数位为可打印 ASCII 的配对数
	for i := 0; i < pairs; i++ {
		even := sample[i*2]
		odd := sample[i*2+1]
		if even == 0x00 && (odd >= 0x20 && odd <= 0x7E || odd == '\n' || odd == '\r' || odd == '\t') {
			nullPrintablePairs++
		}
	}
	return nullPrintablePairs > pairs*6/10
}

// printableRatio 计算字符串中可打印字符（含常见空白符）的占比。
// 用于防止 ISO-8859-1/Windows-1252 等"万能解码器"把二进制内容误判为文本。
func printableRatio(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	printable := 0
	total := 0
	for _, r := range s {
		total++
		if (r >= 0x20 && r <= 0x7E) || r == '\n' || r == '\r' || r == '\t' || r == '\f' || r > 0xA0 {
			printable++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(printable) / float64(total)
}

// decodeNonUTF8ToUTF8 尝试将非 UTF-8 编码的字节内容转换为 UTF-8 字符串。
// 检测顺序：UTF-16 BOM → UTF-16 无 BOM 启发式 → charset 自动检测 → CJK 编码 → Western 编码。
// 返回 UTF-8 字符串和检测到的编码名称；若内容已是有效 UTF-8 则编码名称为 "utf-8"。
// 若所有编码尝试均失败，返回原始字节的 lossy UTF-8 转换和 "unknown"。
func decodeNonUTF8ToUTF8(content []byte) (string, string) {
	// 1. UTF-16 BOM 检测：必须在 utf8.Valid 之前，因为 UTF-16 BOM 字节本身也是合法 UTF-8
	if len(content) >= 2 {
		if content[0] == 0xFF && content[1] == 0xFE {
			dec := unicodeenc.UTF16(unicodeenc.LittleEndian, unicodeenc.UseBOM).NewDecoder()
			if decoded, err := dec.Bytes(content); err == nil && replacementRatio(string(decoded)) < 0.05 {
				return string(decoded), "utf-16le"
			}
		} else if content[0] == 0xFE && content[1] == 0xFF {
			dec := unicodeenc.UTF16(unicodeenc.BigEndian, unicodeenc.UseBOM).NewDecoder()
			if decoded, err := dec.Bytes(content); err == nil && replacementRatio(string(decoded)) < 0.05 {
				return string(decoded), "utf-16be"
			}
		}
	}
	// 2. UTF-16 无 BOM 启发式检测：必须在 utf8.Valid 之前，
	// 因为 UTF-16LE 编码的 ASCII 文本（如 H\x00e\x00l\x00）是合法 UTF-8（null 是有效码点），
	// utf8.Valid 会返回 true 导致跳过 UTF-16 解码。
	if decoded, name, ok := tryDecodeAsUTF16NoBOM(content); ok {
		return decoded, name
	}
	// 3. 有效 UTF-8 检查（在 UTF-16 检测之后）
	if utf8.Valid(content) {
		return string(content), "utf-8"
	}
	// 4. charset.DetermineEncoding 优先检测 CJK 编码
	// 基于字节频率分析，能更好地区分 GBK/Shift_JIS/EUC-JP 等 CJK 编码。
	// 仅信任 CJK 编码结果；非 CJK 结果（如 windows-1252）在后续步骤中作为回退。
	if enc, name, _ := charset.DetermineEncoding(content, ""); enc != nil && isCJKEncodingName(name) {
		decoded, decErr := decodeWithEncoding(enc, content)
		if decErr == nil && replacementRatio(decoded) < 0.02 {
			return decoded, normalizeCharsetName(name)
		}
	}
	// 5. 常见 CJK 编码逐个尝试（回退方案）
	// 必须在 charset.DetermineEncoding 的非 CJK 结果之前，
	// 因为 charset 对 CJK 内容可能返回 windows-1252 等"万能解码器"导致乱码
	cjkCandidates := []struct {
		enc  encoding.Encoding
		name string
	}{
		{simplifiedchinese.GBK, "gbk"},
		{simplifiedchinese.GB18030, "gb18030"},
		{traditionalchinese.Big5, "big5"},
		{japanese.ShiftJIS, "shift_jis"},
		{japanese.EUCJP, "euc-jp"},
		{korean.EUCKR, "euc-kr"},
	}
	for _, candidate := range cjkCandidates {
		if candidate.enc == nil {
			continue
		}
		decoded, decErr := decodeWithEncoding(candidate.enc, content)
		if decErr == nil && replacementRatio(decoded) < 0.02 {
			return decoded, candidate.name
		}
	}
	// 6. charset 自动检测（非 CJK 结果）
	// 可能返回 windows-1252 等"万能解码器"，需额外验证可打印字符比例。
	if enc, name, _ := charset.DetermineEncoding(content, ""); enc != nil {
		decoded, decErr := decodeWithEncoding(enc, content)
		if decErr == nil && replacementRatio(decoded) < 0.05 && printableRatio(decoded) > 0.80 {
			return decoded, name
		}
	}
	// 6. Western 编码回退（ISO-8859-1/Windows-1252 可解码任意字节，需额外验证可打印字符比例）
	westernCandidates := []struct {
		enc  encoding.Encoding
		name string
	}{
		{charmap.ISO8859_1, "iso-8859-1"},
		{charmap.Windows1252, "windows-1252"},
	}
	for _, candidate := range westernCandidates {
		if candidate.enc == nil {
			continue
		}
		decoded, decErr := decodeWithEncoding(candidate.enc, content)
		if decErr == nil && replacementRatio(decoded) < 0.02 && printableRatio(decoded) > 0.80 {
			return decoded, candidate.name
		}
	}
	// 7. 最终回退：使用 charset 检测结果做 lossy 转换（仅在替换字符比例可接受时使用）
	if enc, name, _ := charset.DetermineEncoding(content, ""); enc != nil {
		decoded, decErr := decodeWithEncoding(enc, content)
		if decErr == nil && replacementRatio(decoded) < 0.10 {
			return decoded, name
		}
	}
	return string(content), "unknown"
}

// tryDecodeAsUTF16NoBOM 尝试将无 BOM 的 UTF-16 内容解码为 UTF-8。
// 依次尝试 UTF-16LE 和 UTF-16BE，返回解码结果、编码名称和是否成功。
func tryDecodeAsUTF16NoBOM(content []byte) (string, string, bool) {
	if len(content) < 4 {
		return "", "", false
	}
	// 尝试 UTF-16LE（无 BOM）
	if looksLikeUTF16LENoBOM(content[:min(len(content), 8192)]) {
		dec := unicodeenc.UTF16(unicodeenc.LittleEndian, unicodeenc.IgnoreBOM).NewDecoder()
		if decoded, err := dec.Bytes(content); err == nil && replacementRatio(string(decoded)) < 0.05 {
			return string(decoded), "utf-16le", true
		}
	}
	// 尝试 UTF-16BE（无 BOM）
	if looksLikeUTF16BENoBOM(content[:min(len(content), 8192)]) {
		dec := unicodeenc.UTF16(unicodeenc.BigEndian, unicodeenc.IgnoreBOM).NewDecoder()
		if decoded, err := dec.Bytes(content); err == nil && replacementRatio(string(decoded)) < 0.05 {
			return string(decoded), "utf-16be", true
		}
	}
	return "", "", false
}

// trimTrailingIncompleteUTF8 去除字节切片末尾不完整的 UTF-8 字节序列。
// 当从文件中截取固定长度的样本时，末尾可能恰好落在一个多字节 UTF-8 字符的中间，
// 导致 utf8.Valid 返回 false。此函数确保返回的切片以完整的 UTF-8 字符结尾。
func trimTrailingIncompleteUTF8(b []byte) []byte {
	if len(b) == 0 {
		return b
	}
	// 从末尾向前检查最多 3 个字节（UTF-8 最长 4 字节）
	for i := 1; i <= 3 && i <= len(b); i++ {
		lastByte := b[len(b)-i]
		if lastByte < 0x80 {
			// ASCII 字节，完整
			return b
		}
		if lastByte >= 0xC0 {
			// 这是多字节序列的首字节，检查序列是否完整
			var expectedLen int
			switch {
			case lastByte < 0xE0:
				expectedLen = 2
			case lastByte < 0xF0:
				expectedLen = 3
			default:
				expectedLen = 4
			}
			if i < expectedLen {
				// 序列不完整，截断
				return b[:len(b)-i]
			}
			// 序列完整
			return b
		}
		// lastByte 在 0x80-0xBF 之间，是延续字节，继续向前检查
	}
	return b
}

// decodeWithEncoding 使用指定编码将字节切片解码为 UTF-8 字符串。
func decodeWithEncoding(enc encoding.Encoding, content []byte) (string, error) {
	reader := transform.NewReader(bytes.NewReader(content), enc.NewDecoder())
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// replacementRatio 计算字符串中 Unicode 替换字符（U+FFFD）的占比。
func replacementRatio(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	count := strings.Count(s, "\ufffd")
	return float64(count) / float64(utf8.RuneCountInString(s))
}

// isCJKEncodingName 判断编码名称是否为 CJK 编码。
// 用于在 charset.DetermineEncoding 返回结果时，优先信任 CJK 编码的检测结果。
func isCJKEncodingName(name string) bool {
	name = strings.ToLower(name)
	cjkNames := []string{"gbk", "gb2312", "gb18030", "big5", "shift_jis", "sjis", "windows-31j", "euc-jp", "euc-kr", "euc-cn", "iso-2022-jp"}
	for _, cjk := range cjkNames {
		if strings.Contains(name, cjk) {
			return true
		}
	}
	return false
}

// normalizeCharsetName 标准化编码名称。
// charset.DetermineEncoding 可能返回多种变体名称（如 "shift_jis", "sjis", "windows-31j"），
// 此函数将它们统一为标准名称，便于后续处理和测试验证。
func normalizeCharsetName(name string) string {
	name = strings.ToLower(name)
	switch {
	case strings.Contains(name, "shift_jis") || strings.Contains(name, "sjis") || strings.Contains(name, "windows-31j"):
		return "shift_jis"
	case strings.Contains(name, "euc-jp"):
		return "euc-jp"
	case strings.Contains(name, "euc-kr"):
		return "euc-kr"
	case strings.Contains(name, "gb18030"):
		return "gb18030"
	case strings.Contains(name, "gbk") || strings.Contains(name, "gb2312"):
		return "gbk"
	case strings.Contains(name, "big5"):
		return "big5"
	}
	return name
}

// looksBinaryEditableContent 判断文件内容是否为不可编辑的二进制文件。
// 复用 looksBinaryContent 的多层检测策略，确保与 readFile 的二进制判定逻辑一致。
func looksBinaryEditableContent(content []byte) bool {
	return looksBinaryContent(content)
}

func writeFileAtomically(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(path), ".snz1dp-*")
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
