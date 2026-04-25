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
	"sort"
	"strings"
	"time"
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

func (r *ClientRuntime) clientToolDefinitionsForMode(mode string) []map[string]any {
	definitions := []map[string]any{
		{
			"name":                 "lsp_query",
			"description":          "通过本地 stdio 语言服务器（LSP）执行 definition、references、implementation、document_symbol、workspace_symbol 查询。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"server_command":      map[string]any{"type": "string", "description": "语言服务器命令，例如 gopls 或 pyright-langserver。"},
					"server_args":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"workspace_root":      map[string]any{"type": "string", "description": "LSP 工作区根目录，默认当前工作目录。"},
					"operation":           map[string]any{"type": "string", "enum": []string{"definition", "references", "implementation", "document_symbol", "workspace_symbol"}},
					"path":                map[string]any{"type": "string", "description": "需要定位的文件路径；workspace_symbol 可省略。"},
					"line":                map[string]any{"type": "integer", "minimum": 1},
					"character":           map[string]any{"type": "integer", "minimum": 1},
					"symbol_query":        map[string]any{"type": "string", "description": "workspace_symbol 查询关键字。"},
					"language_id":         map[string]any{"type": "string", "description": "可选语言 ID；默认按文件扩展名推断。"},
					"include_declaration": map[string]any{"type": "boolean"},
					"max_results":         map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
					"timeout":             map[string]any{"type": "integer", "minimum": 1, "maximum": maxCommandTimeout},
				},
				"required":             []string{"server_command", "operation"},
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
					"path":           map[string]any{"type": "string", "description": "起始目录，默认当前工作目录。"},
					"pattern":        map[string]any{"type": "string", "description": "glob 模式，例如 **/*.go 或 cmd/*.go。"},
					"include_hidden": map[string]any{"type": "boolean"},
					"kind":           map[string]any{"type": "string", "enum": []string{"all", "file", "directory"}},
					"max_results":    map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
				},
				"required":             []string{"pattern"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "todo_read",
			"description":          "读取当前会话保存的 Todo 清单。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
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
			"description":          "基于 old_string/new_string 的细粒度文本编辑，要求 old_string 与文件当前内容精确匹配；失败后应先 read_file 获取最新内容，不要盲目重复重试。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":                   map[string]any{"type": "string", "description": "目标文件路径。"},
					"old_string":             map[string]any{"type": "string", "description": "要替换的旧文本。"},
					"new_string":             map[string]any{"type": "string", "description": "替换后的新文本。"},
					"replace_all":            map[string]any{"type": "boolean"},
					"dry_run":                map[string]any{"type": "boolean"},
					"expected_sha256_before": map[string]any{"type": "string", "description": "可选的编辑前内容摘要，用于检测文件是否变化。"},
				},
				"required":             []string{"path", "old_string", "new_string"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "shell_exec",
			"description":          "执行结构化本地 Shell 命令，支持前台执行或后台任务句柄返回。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"raw_command":       map[string]any{"type": "string", "description": "直接交给 shell 执行的原始命令。"},
					"command":           map[string]any{"type": "string", "description": "可执行文件名；与 args 配合使用。"},
					"args":              map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"working_directory": map[string]any{"type": "string", "description": "命令工作目录，默认当前工作目录。"},
					"timeout":           map[string]any{"type": "integer", "minimum": 1, "maximum": maxCommandTimeout},
					"env_overrides":     map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
					"expect_json":       map[string]any{"type": "boolean"},
					"background":        map[string]any{"type": "boolean", "description": "为 true 时转为后台任务并返回 task_id。"},
					"task_name":         map[string]any{"type": "string", "description": "可选任务名称，便于 TUI 与模型识别。"},
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
			"description":          "列出当前 CLI 进程内已知的本地后台任务。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		{
			"name":                 "search_files",
			"description":          "在当前工作目录访问范围内搜索文本，支持正则、大小写、include/exclude 与分页。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":            map[string]any{"type": "string", "description": "要搜索的文本或正则表达式。"},
					"path":             map[string]any{"type": "string", "description": "搜索根目录，默认当前工作目录。"},
					"glob":             map[string]any{"type": "string", "description": "兼容旧参数，等同附加一条 include_patterns。"},
					"include_patterns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"exclude_patterns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"case_sensitive":   map[string]any{"type": "boolean"},
					"is_regexp":        map[string]any{"type": "boolean"},
					"include_hidden":   map[string]any{"type": "boolean"},
					"offset":           map[string]any{"type": "integer", "minimum": 0},
					"max_results":      map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
				},
				"required":             []string{"query"},
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
			"name":                 "list_dir",
			"description":          "列出当前工作目录访问范围内的目录内容，支持递归、深度限制、隐藏文件过滤和 glob。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":           map[string]any{"type": "string", "description": "目标目录路径。默认当前工作目录。"},
					"recursive":      map[string]any{"type": "boolean"},
					"max_depth":      map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
					"include_hidden": map[string]any{"type": "boolean"},
					"glob":           map[string]any{"type": "string"},
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
			"name":                 "skills_list",
			"description":          "列出本地已发现的技能（Skill）摘要及启用状态。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
			},
		},
		{
			"name":                 "skill_view",
			"description":          "读取单个本地技能（Skill）的完整内容。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "description": "技能名称。"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "skill_manage",
			"description":          "启用或禁用本地技能（Skill）。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{"type": "string", "enum": []string{"enable", "disable"}},
					"name":   map[string]any{"type": "string", "description": "技能名称。"},
				},
				"required":             []string{"action", "name"},
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
		"lsp_query":    true,
		"glob_files":   true,
		"todo_read":    true,
		"task_output":  true,
		"task_list":    true,
		"search_files": true,
		"read_file":    true,
		"list_dir":     true,
		"skills_list":  true,
		"skill_view":   true,
	}
}

func RequiresClientToolConfirmation(config internalconfig.Config, payload map[string]any) bool {
	toolName := nestedString(payload, "function", "name")
	return shouldConfirmMutatingTool(config, toolName)
}

func PreviewClientToolCall(payload map[string]any) string {
	toolName := nestedString(payload, "function", "name")
	arguments := nestedMap(payload, "function", "arguments")
	return previewClientTool(toolName, arguments)
}

func (r *ClientRuntime) authorizeClientTool(toolName string, arguments map[string]any, config internalconfig.Config) error {
	mode := strings.TrimSpace(config.ApprovalMode)
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
	return strings.TrimSpace(config.ApprovalMode) == "confirm" && mutatingClientTool(toolName)
}

func mutatingClientTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case "shell_exec", "task_stop", "todo_write", "file_edit", "write_file", "skill_manage":
		return true
	default:
		return false
	}
}

func previewClientTool(toolName string, arguments map[string]any) string {
	path := strings.TrimSpace(stringValue(arguments["path"]))
	name := strings.TrimSpace(stringValue(arguments["name"]))
	switch toolName {
	case "lsp_query":
		operation := fallbackString(stringValue(arguments["operation"]), "<missing>")
		return fmt.Sprintf("lsp_query %s (%s)", operation, fallbackString(stringValue(arguments["server_command"]), "<missing>"))
	case "glob_files":
		return fmt.Sprintf("glob_files %s @ %s", fallbackString(stringValue(arguments["pattern"]), "<missing>"), fallbackString(path, "."))
	case "todo_read":
		return "todo_read"
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
	case "task_list":
		return "task_list"
	case "write_file":
		return fmt.Sprintf("write_file %s (%s)", path, fallbackString(stringValue(arguments["mode"]), "overwrite"))
	case "skill_manage":
		return fmt.Sprintf("skill_manage %s %s", strings.TrimSpace(stringValue(arguments["action"])), name)
	case "read_file", "list_dir", "search_files":
		return fmt.Sprintf("%s %s", toolName, fallbackString(path, "."))
	case "skill_view":
		return fmt.Sprintf("skill_view %s", name)
	case "skills_list":
		return "skills_list"
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

func (r *ClientRuntime) listDir(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	resolvedPath, err := r.resolveAllowedDirectoryWithPolicy(config, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}
	recursive := boolValue(arguments["recursive"])
	includeHidden := boolValue(arguments["include_hidden"])
	glob := strings.TrimSpace(stringValue(arguments["glob"]))
	maxDepth := intValue(arguments["max_depth"], 1)
	if recursive && maxDepth < 1 {
		maxDepth = 3
	}
	if !recursive {
		maxDepth = 1
	}
	entries := []map[string]any{}
	err = filepath.WalkDir(resolvedPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == resolvedPath {
			return nil
		}
		relativePath, relErr := filepath.Rel(resolvedPath, path)
		if relErr != nil {
			relativePath = filepath.Base(path)
		}
		relativePath = filepath.ToSlash(relativePath)
		if isHiddenPath(relativePath) && !includeHidden {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		depth := 1
		if relativePath != "." {
			depth = strings.Count(relativePath, "/") + 1
		}
		if depth > maxDepth {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if glob != "" && !matchesSearchGlob(path, resolvedPath, glob) {
			if entry.IsDir() {
				return nil
			}
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return statErr
		}
		entryType := "file"
		if entry.IsDir() {
			entryType = "directory"
		} else if info.Mode()&os.ModeSymlink != 0 {
			entryType = "symlink"
		}
		entries = append(entries, map[string]any{
			"name":     entry.Name(),
			"path":     relativePath,
			"type":     entryType,
			"size":     info.Size(),
			"mod_time": info.ModTime().Format(time.RFC3339),
		})
		return nil
	})
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "列目录失败: %v", err)
	}
	sort.Slice(entries, func(left, right int) bool {
		return stringValue(entries[left]["path"]) < stringValue(entries[right]["path"])
	})
	r.writeStreamEventf(r.stdout, "[本地工具] list_dir %s，共 %d 项\n", resolvedPath, len(entries))
	_ = appendAuditLog(config, "list_dir", map[string]any{"path": resolvedPath, "count": len(entries)})
	return map[string]any{
		"root":      resolvedPath,
		"entries":   entries,
		"count":     len(entries),
		"recursive": recursive,
		"max_depth": maxDepth,
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

func (r *ClientRuntime) skillsList(config internalconfig.Config) (map[string]any, error) {
	items, directory, err := skills.Discover(config)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "%v", err)
	}
	enabledNames := skills.EnabledSkillNames(items)
	return map[string]any{
		"directory":     directory,
		"skills":        skills.Summaries(items),
		"count":         len(items),
		"enabled_count": len(enabledNames),
		"enabled":       enabledNames,
	}, nil
}

func (r *ClientRuntime) skillView(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	name := strings.TrimSpace(stringValue(arguments["name"]))
	if name == "" {
		return nil, newToolError("PARAM_ERROR", "skill_view 缺少 name")
	}
	item, _, err := skills.LoadByName(config, name)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "%v", err)
	}
	return map[string]any{
		"name":          item.Name,
		"description":   item.Description,
		"category":      item.Category,
		"prerequisites": item.Prerequisites,
		"path":          item.Path,
		"enabled":       item.Enabled,
		"content":       item.Body,
	}, nil
}

func (r *ClientRuntime) skillManage(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	action := strings.ToLower(strings.TrimSpace(stringValue(arguments["action"])))
	name := strings.TrimSpace(stringValue(arguments["name"]))
	if name == "" {
		return nil, newToolError("PARAM_ERROR", "skill_manage 缺少 name")
	}
	if action != "enable" && action != "disable" {
		return nil, newToolError("PARAM_ERROR", "skill_manage action 仅支持 enable、disable")
	}
	manager, err := internalconfig.NewManager()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "加载配置管理器失败: %v", err)
	}
	latestConfig, err := manager.Load()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取配置失败: %v", err)
	}
	item, _, err := skills.LoadByName(latestConfig, name)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "%v", err)
	}
	enabled := normalizeSkillList(latestConfig.Skills.Enabled)
	if action == "enable" {
		enabled = append(enabled, item.Name)
	} else {
		filtered := []string{}
		for _, current := range enabled {
			if !strings.EqualFold(current, item.Name) {
				filtered = append(filtered, current)
			}
		}
		enabled = filtered
	}
	latestConfig.Skills.Enabled = normalizeSkillList(enabled)
	if err := manager.Save(latestConfig); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "保存配置失败: %v", err)
	}
	_ = appendAuditLog(latestConfig, "skill_manage", map[string]any{"action": action, "name": item.Name})
	return map[string]any{
		"action":         action,
		"name":           item.Name,
		"enabled_skills": latestConfig.Skills.Enabled,
		"config_path":    manager.Path(),
	}, nil
}

func (r *ClientRuntime) searchFilesLocal(_ context.Context, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	query := strings.TrimSpace(stringValue(arguments["query"]))
	if query == "" {
		return nil, newToolError("PARAM_ERROR", "文件搜索缺少关键词")
	}
	baseDir, err := r.resolveAllowedDirectoryWithPolicy(config, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}
	maxResults := intValue(arguments["max_results"], 50)
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
	includeHidden := boolValue(arguments["include_hidden"])
	includePatterns := normalizePatternList(stringSlice(arguments["include_patterns"]), strings.TrimSpace(stringValue(arguments["glob"])))
	excludePatterns := normalizePatternList(stringSlice(arguments["exclude_patterns"]))
	matcher, err := buildSearchMatcher(query, boolValue(arguments["is_regexp"]), boolValue(arguments["case_sensitive"]))
	if err != nil {
		return nil, newToolError("PARAM_ERROR", "%v", err)
	}
	total := 0
	hasMore := false
	results := []map[string]any{}
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
	_ = appendAuditLog(config, "search_files", map[string]any{"path": baseDir, "query": query, "count": len(results)})
	return map[string]any{
		"matches":       results,
		"count":         len(results),
		"total_matches": total,
		"offset":        offset,
		"has_more":      hasMore,
		"root":          baseDir,
		"engine":        "go",
	}, nil
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

func matchGlob(pattern string, path string) bool {
	normalizedPattern := filepath.ToSlash(strings.TrimSpace(pattern))
	matched, err := filepath.Match(normalizedPattern, path)
	if err == nil && matched {
		return true
	}
	if !strings.Contains(normalizedPattern, "/") {
		matched, err = filepath.Match(normalizedPattern, filepath.Base(path))
		return err == nil && matched
	}
	return false
}

func normalizeSkillList(items []string) []string {
	seen := map[string]string{}
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		seen[strings.ToLower(trimmed)] = trimmed
	}
	keys := make([]string, 0, len(seen))
	for _, value := range seen {
		keys = append(keys, value)
	}
	sort.Slice(keys, func(left, right int) bool {
		return strings.ToLower(keys[left]) < strings.ToLower(keys[right])
	})
	return keys
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

