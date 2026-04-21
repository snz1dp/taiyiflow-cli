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
	"os/exec"
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
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func newToolError(code string, format string, args ...any) error {
	return toolError{Code: code, Message: fmt.Sprintf(format, args...)}
}

func (r *ClientRuntime) clientToolDefinitions() []map[string]any {
	return r.clientToolDefinitionsForMode(provider.RequestTypeAgent)
}

func (r *ClientRuntime) clientToolDefinitionsForMode(mode string) []map[string]any {
	definitions := []map[string]any{
		{
			"name":                 "search_files",
			"description":          "在工作目录沙箱内搜索文本，支持正则、大小写、include/exclude 与分页。",
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
			"name":                 "git_status",
			"description":          "查看当前工作目录或目标路径所在 Git 仓库的状态摘要。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string", "description": "仓库内文件或目录路径，默认当前工作目录。"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "git_diff",
			"description":          "查看当前仓库 diff，支持 staged、ref、path 和输出截断。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":      map[string]any{"type": "string", "description": "仓库内文件或目录路径，默认当前工作目录。"},
					"staged":    map[string]any{"type": "boolean"},
					"ref":       map[string]any{"type": "string", "description": "可选比较引用，例如 HEAD~1。"},
					"max_bytes": map[string]any{"type": "integer", "minimum": 1, "maximum": maxReadBytesHard},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "git_log",
			"description":          "查看当前仓库提交历史，支持按路径过滤和最大提交数限制。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":        map[string]any{"type": "string", "description": "仓库内文件或目录路径，默认当前工作目录。"},
					"ref":         map[string]any{"type": "string", "description": "可选起始引用，例如 main 或 HEAD~5。"},
					"max_commits": map[string]any{"type": "integer", "minimum": 1, "maximum": 50},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "git_restore",
			"description":          "恢复工作区或暂存区文件，支持 staged/worktree/source 与多路径。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":     map[string]any{"type": "string", "description": "单个路径，默认当前工作目录。"},
					"paths":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"source":   map[string]any{"type": "string", "description": "可选来源引用，如 HEAD。"},
					"staged":   map[string]any{"type": "boolean"},
					"worktree": map[string]any{"type": "boolean"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "git_reset_soft",
			"description":          "执行 git reset --soft 到目标引用，用于回退提交但保留暂存区。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":   map[string]any{"type": "string", "description": "用于定位仓库的路径，默认当前工作目录。"},
					"target": map[string]any{"type": "string", "description": "目标引用，例如 HEAD~1。"},
				},
				"required":             []string{"target"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "git_add",
			"description":          "暂存 Git 文件变更，支持单路径、多路径或当前仓库范围内的全部变更。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":  map[string]any{"type": "string", "description": "单个路径，默认当前工作目录。"},
					"paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"all":   map[string]any{"type": "boolean", "description": "为 true 时执行 git add -A。"},
				},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "git_commit",
			"description":          "提交当前已暂存的 Git 变更，需提供提交信息。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":        map[string]any{"type": "string", "description": "用于定位仓库的路径，默认当前工作目录。"},
					"message":     map[string]any{"type": "string", "description": "提交信息，可包含多行正文。"},
					"amend":       map[string]any{"type": "boolean"},
					"allow_empty": map[string]any{"type": "boolean"},
				},
				"required":             []string{"message"},
				"additionalProperties": false,
			},
		},
		{
			"name":                 "read_file",
			"description":          "读取工作目录沙箱内的文本文件，支持按行读取、二进制识别和 UTF-8 安全截断。",
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
			"description":          "列出工作目录沙箱内的目录内容，支持递归、深度限制、隐藏文件过滤和 glob。",
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
			"description":          "在工作目录沙箱内写入文本文件，支持 create、overwrite、append 和原子写入。",
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
			"name":                 "apply_patch",
			"description":          "对工作目录沙箱内文件应用 unified diff 子集，支持 dry_run 和冲突报告。",
			"waiting_for_response": true,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"patch":   map[string]any{"type": "string", "description": "标准 unified diff 内容。"},
					"dry_run": map[string]any{"type": "boolean"},
				},
				"required":             []string{"patch"},
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
		"search_files": true,
		"git_status":   true,
		"git_diff":     true,
		"git_log":      true,
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
	case "write_file", "apply_patch", "skill_manage", "git_restore", "git_reset_soft", "git_add", "git_commit":
		return true
	default:
		return false
	}
}

func previewClientTool(toolName string, arguments map[string]any) string {
	path := strings.TrimSpace(stringValue(arguments["path"]))
	name := strings.TrimSpace(stringValue(arguments["name"]))
	switch toolName {
	case "write_file":
		return fmt.Sprintf("write_file %s (%s)", path, fallbackString(stringValue(arguments["mode"]), "overwrite"))
	case "apply_patch":
		return "apply_patch <unified diff>"
	case "skill_manage":
		return fmt.Sprintf("skill_manage %s %s", strings.TrimSpace(stringValue(arguments["action"])), name)
	case "read_file", "list_dir", "search_files":
		return fmt.Sprintf("%s %s", toolName, fallbackString(path, "."))
	case "git_status", "git_diff", "git_log":
		return fmt.Sprintf("%s %s", toolName, fallbackString(path, "."))
	case "git_restore":
		paths := stringSlice(arguments["paths"])
		if len(paths) == 0 {
			paths = []string{fallbackString(path, ".")}
		}
		mode := []string{}
		if boolValue(arguments["staged"]) {
			mode = append(mode, "staged")
		}
		if boolValue(arguments["worktree"]) || len(mode) == 0 {
			mode = append(mode, "worktree")
		}
		if source := strings.TrimSpace(stringValue(arguments["source"])); source != "" {
			return fmt.Sprintf("git_restore [%s] %s <- %s", strings.Join(mode, "+"), strings.Join(paths, ", "), source)
		}
		return fmt.Sprintf("git_restore [%s] %s", strings.Join(mode, "+"), strings.Join(paths, ", "))
	case "git_reset_soft":
		return fmt.Sprintf("git_reset_soft %s -> %s", fallbackString(path, "."), fallbackString(stringValue(arguments["target"]), "<missing>"))
	case "git_add":
		paths := stringSlice(arguments["paths"])
		if boolValue(arguments["all"]) {
			return fmt.Sprintf("git_add -A %s", fallbackString(path, "."))
		}
		if len(paths) > 0 {
			return fmt.Sprintf("git_add %s", strings.Join(paths, ", "))
		}
		return fmt.Sprintf("git_add %s", fallbackString(path, "."))
	case "git_commit":
		message := strings.TrimSpace(stringValue(arguments["message"]))
		if message == "" {
			message = "<空消息>"
		}
		return fmt.Sprintf("git_commit %s", trimMultilinePreview(message, 48))
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
	resolvedPath, err := resolveAllowedFilePath(config, targetPath, false)
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
	fmt.Fprintf(r.stdout, "[本地工具] read_file %s 行 %d-%d\n", resolvedPath, startLine, endLine)
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
	resolvedPath, err := resolveAllowedDirectory(config, stringValue(arguments["path"]))
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
	fmt.Fprintf(r.stdout, "[本地工具] list_dir %s，共 %d 项\n", resolvedPath, len(entries))
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
	resolvedPath, err := resolveAllowedFilePath(config, targetPath, true)
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
	fmt.Fprintf(r.stdout, "[本地工具] write_file %s (%s，%d 字节)\n", resolvedPath, mode, len(finalContent))
	_ = appendAuditLog(config, "write_file", map[string]any{"path": resolvedPath, "mode": mode, "size": len(finalContent)})
	return map[string]any{
		"path":          resolvedPath,
		"mode":          mode,
		"created":       created,
		"bytes_written": len(finalContent),
		"size":          len(finalContent),
	}, nil
}

func (r *ClientRuntime) applyPatch(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	patchText := stringValue(arguments["patch"])
	if strings.TrimSpace(patchText) == "" {
		return nil, newToolError("PARAM_ERROR", "apply_patch 缺少 patch 内容")
	}
	files, err := parseUnifiedPatch(patchText)
	if err != nil {
		return nil, newToolError("PARAM_ERROR", "patch 解析失败: %v", err)
	}
	dryRun := boolValue(arguments["dry_run"])
	conflicts := []map[string]any{}
	updatedContent := map[string][]byte{}
	fileResults := []map[string]any{}
	for _, filePatch := range files {
		resolvedPath, resolveErr := resolveAllowedFilePath(config, filePatch.Path, false)
		if resolveErr != nil {
			conflicts = append(conflicts, map[string]any{"path": filePatch.Path, "reason": resolveErr.Error()})
			continue
		}
		originalContent, readErr := os.ReadFile(resolvedPath)
		if readErr != nil {
			conflicts = append(conflicts, map[string]any{"path": resolvedPath, "reason": fmt.Sprintf("读取文件失败: %v", readErr)})
			continue
		}
		applied, applyErr := applyPatchToContent(originalContent, filePatch)
		if applyErr != nil {
			conflicts = append(conflicts, buildPatchConflict(resolvedPath, applyErr))
			continue
		}
		updatedContent[resolvedPath] = applied
		fileResults = append(fileResults, map[string]any{
			"path":         resolvedPath,
			"bytes_before": len(originalContent),
			"bytes_after":  len(applied),
		})
	}
	if len(conflicts) > 0 {
		return map[string]any{
			"dry_run":   dryRun,
			"applied":   false,
			"conflicts": conflicts,
			"files":     fileResults,
		}, newToolError("PATCH_CONFLICT", "补丁存在 %d 处冲突", len(conflicts))
	}
	if !dryRun {
		for path, content := range updatedContent {
			if err := writeFileAtomically(path, content); err != nil {
				return nil, newToolError("EXECUTION_FAILED", "写入补丁结果失败: %v", err)
			}
		}
	}
	fmt.Fprintf(r.stdout, "[本地工具] apply_patch %d 个文件%s\n", len(fileResults), map[bool]string{true: "（dry-run）", false: ""}[dryRun])
	_ = appendAuditLog(config, "apply_patch", map[string]any{"dry_run": dryRun, "files": len(fileResults)})
	return map[string]any{
		"dry_run":   dryRun,
		"applied":   true,
		"conflicts": []map[string]any{},
		"files":     fileResults,
	}, nil
}

func (r *ClientRuntime) gitStatus(ctx context.Context, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	repoRoot, relativePath, err := resolveGitRepository(config, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}
	commandArgs := []string{"-C", repoRoot, "status", "--short", "--branch", "--porcelain=1"}
	if relativePath != "" {
		commandArgs = append(commandArgs, "--", relativePath)
	}
	output, err := exec.CommandContext(ctx, "git", commandArgs...).Output()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取 git status 失败: %v", err)
	}
	branch := ""
	staged := []string{}
	modified := []string{}
	deleted := []string{}
	untracked := []string{}
	renamed := []string{}
	conflicted := []string{}
	for _, line := range splitLines(string(output)) {
		if strings.HasPrefix(line, "## ") {
			branch = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		if len(line) < 4 {
			continue
		}
		xStatus := line[0]
		yStatus := line[1]
		path := strings.TrimSpace(line[3:])
		switch {
		case xStatus == '?' && yStatus == '?':
			untracked = append(untracked, path)
		case xStatus == 'R' || yStatus == 'R':
			renamed = append(renamed, path)
		case xStatus == 'U' || yStatus == 'U' || (xStatus == 'A' && yStatus == 'A'):
			conflicted = append(conflicted, path)
		default:
			if xStatus != ' ' {
				if xStatus == 'D' {
					deleted = append(deleted, path)
				} else {
					staged = append(staged, path)
				}
			}
			if yStatus != ' ' {
				if yStatus == 'D' {
					deleted = append(deleted, path)
				} else {
					modified = append(modified, path)
				}
			}
		}
	}
	changeCount := len(staged) + len(modified) + len(deleted) + len(renamed) + len(conflicted)
	fmt.Fprintf(r.stdout, "[本地工具] git_status %s（%d 个变更，%d 个未跟踪）\n", repoRoot, changeCount, len(untracked))
	_ = appendAuditLog(config, "git_status", map[string]any{"repo": repoRoot, "path": relativePath, "changes": changeCount, "untracked": len(untracked)})
	return map[string]any{
		"repo_root":  repoRoot,
		"path":       relativePath,
		"branch":     branch,
		"staged":     staged,
		"modified":   modified,
		"deleted":    deleted,
		"renamed":    renamed,
		"untracked":  untracked,
		"conflicted": conflicted,
		"count":      changeCount + len(untracked),
	}, nil
}

func (r *ClientRuntime) gitDiff(ctx context.Context, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	repoRoot, relativePath, err := resolveGitRepository(config, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}
	commandArgs := []string{"-C", repoRoot, "diff", "--no-color", "--unified=3"}
	if boolValue(arguments["staged"]) {
		commandArgs = append(commandArgs, "--staged")
	}
	if ref := strings.TrimSpace(stringValue(arguments["ref"])); ref != "" {
		commandArgs = append(commandArgs, ref)
	}
	if relativePath != "" {
		commandArgs = append(commandArgs, "--", relativePath)
	}
	output, err := exec.CommandContext(ctx, "git", commandArgs...).Output()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取 git diff 失败: %v", err)
	}
	maxBytes := intValue(arguments["max_bytes"], maxReadBytesDefault)
	if maxBytes < 1 {
		maxBytes = maxReadBytesDefault
	}
	if maxBytes > maxReadBytesHard {
		maxBytes = maxReadBytesHard
	}
	content, truncated := truncateUTF8(string(output), maxBytes)
	lineCount := 0
	if strings.TrimSpace(content) != "" {
		lineCount = len(splitLines(content))
	}
	detail := fmt.Sprintf("%d 行", lineCount)
	if boolValue(arguments["staged"]) {
		detail += "，staged"
	}
	fmt.Fprintf(r.stdout, "[本地工具] git_diff %s（%s）\n", repoRoot, detail)
	_ = appendAuditLog(config, "git_diff", map[string]any{"repo": repoRoot, "path": relativePath, "staged": boolValue(arguments["staged"]), "truncated": truncated})
	return map[string]any{
		"repo_root":  repoRoot,
		"path":       relativePath,
		"staged":     boolValue(arguments["staged"]),
		"ref":        strings.TrimSpace(stringValue(arguments["ref"])),
		"content":    content,
		"line_count": lineCount,
		"truncated":  truncated,
	}, nil
}

func (r *ClientRuntime) gitLog(ctx context.Context, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	repoRoot, relativePath, err := resolveGitRepository(config, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}
	maxCommits := intValue(arguments["max_commits"], 10)
	if maxCommits < 1 {
		maxCommits = 1
	}
	if maxCommits > 50 {
		maxCommits = 50
	}
	commandArgs := []string{"-C", repoRoot, "log", "--date=iso-strict", fmt.Sprintf("--max-count=%d", maxCommits), "--pretty=format:%H%x1f%an%x1f%ad%x1f%s"}
	if ref := strings.TrimSpace(stringValue(arguments["ref"])); ref != "" {
		commandArgs = append(commandArgs, ref)
	}
	if relativePath != "" {
		commandArgs = append(commandArgs, "--", relativePath)
	}
	output, err := exec.CommandContext(ctx, "git", commandArgs...).Output()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取 git log 失败: %v", err)
	}
	commits := []map[string]any{}
	for _, line := range splitLines(string(output)) {
		parts := strings.SplitN(line, "\x1f", 4)
		if len(parts) != 4 {
			continue
		}
		commits = append(commits, map[string]any{
			"hash":    parts[0],
			"author":  parts[1],
			"date":    parts[2],
			"subject": parts[3],
		})
	}
	fmt.Fprintf(r.stdout, "[本地工具] git_log %s（最近 %d 条）\n", repoRoot, len(commits))
	_ = appendAuditLog(config, "git_log", map[string]any{"repo": repoRoot, "path": relativePath, "count": len(commits)})
	return map[string]any{
		"repo_root":   repoRoot,
		"path":        relativePath,
		"ref":         strings.TrimSpace(stringValue(arguments["ref"])),
		"max_commits": maxCommits,
		"commits":     commits,
		"count":       len(commits),
	}, nil
}

func (r *ClientRuntime) gitRestore(ctx context.Context, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	repoRoot, relativePath, err := resolveGitRepository(config, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}
	paths := stringSlice(arguments["paths"])
	if len(paths) == 0 && relativePath != "" {
		paths = []string{relativePath}
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	staged := boolValue(arguments["staged"])
	worktree := boolValue(arguments["worktree"])
	if !staged && !worktree {
		worktree = true
	}
	commandArgs := []string{"-C", repoRoot, "restore"}
	if source := strings.TrimSpace(stringValue(arguments["source"])); source != "" {
		commandArgs = append(commandArgs, "--source", source)
	}
	if staged {
		commandArgs = append(commandArgs, "--staged")
	}
	if worktree {
		commandArgs = append(commandArgs, "--worktree")
	}
	commandArgs = append(commandArgs, "--")
	commandArgs = append(commandArgs, paths...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, newToolError("EXECUTION_FAILED", "执行 git restore 失败: %s", message)
	}
	_ = appendAuditLog(config, "git_restore", map[string]any{"repo": repoRoot, "paths": paths, "staged": staged, "worktree": worktree})
	return map[string]any{
		"repo_root": repoRoot,
		"paths":     paths,
		"staged":    staged,
		"worktree":  worktree,
		"source":    strings.TrimSpace(stringValue(arguments["source"])),
		"count":     len(paths),
	}, nil
}

func (r *ClientRuntime) gitResetSoft(ctx context.Context, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	repoRoot, _, err := resolveGitRepository(config, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(stringValue(arguments["target"]))
	if target == "" {
		return nil, newToolError("PARAM_ERROR", "git_reset_soft 缺少 target")
	}
	beforeOutput, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取当前提交失败: %v", err)
	}
	output, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "reset", "--soft", target).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, newToolError("EXECUTION_FAILED", "执行 git reset --soft 失败: %s", message)
	}
	afterOutput, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "HEAD").Output()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取回退后提交失败: %v", err)
	}
	before := strings.TrimSpace(string(beforeOutput))
	after := strings.TrimSpace(string(afterOutput))
	_ = appendAuditLog(config, "git_reset_soft", map[string]any{"repo": repoRoot, "target": target, "before": before, "after": after})
	return map[string]any{
		"repo_root":    repoRoot,
		"target":       target,
		"before_commit": before,
		"after_commit":  after,
	}, nil
}

func (r *ClientRuntime) gitAdd(ctx context.Context, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	repoRoot, relativePaths, addAll, err := resolveGitAddTargets(config, arguments)
	if err != nil {
		return nil, err
	}
	commandArgs := []string{"-C", repoRoot, "add"}
	if addAll {
		commandArgs = append(commandArgs, "-A")
	}
	if len(relativePaths) > 0 {
		commandArgs = append(commandArgs, "--")
		commandArgs = append(commandArgs, relativePaths...)
	}
	output, err := exec.CommandContext(ctx, "git", commandArgs...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, newToolError("EXECUTION_FAILED", "执行 git add 失败: %s", message)
	}
	displayTargets := relativePaths
	if len(displayTargets) == 0 {
		displayTargets = []string{"."}
	}
	fmt.Fprintf(r.stdout, "[本地工具] git_add %s（暂存 %d 项）\n", repoRoot, len(displayTargets))
	_ = appendAuditLog(config, "git_add", map[string]any{"repo": repoRoot, "paths": displayTargets, "all": addAll})
	return map[string]any{
		"repo_root": repoRoot,
		"paths":     displayTargets,
		"all":       addAll,
		"count":     len(displayTargets),
	}, nil
}

func (r *ClientRuntime) gitCommit(ctx context.Context, config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	repoRoot, _, err := resolveGitRepository(config, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}
	message := strings.TrimSpace(stringValue(arguments["message"]))
	if message == "" {
		return nil, newToolError("PARAM_ERROR", "git_commit 缺少 message")
	}
	amend := boolValue(arguments["amend"])
	allowEmpty := boolValue(arguments["allow_empty"])
	stagedFiles, err := gitStagedFiles(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	if len(stagedFiles) == 0 && !allowEmpty {
		return map[string]any{"repo_root": repoRoot, "message": message, "staged_files": []string{}}, newToolError("EXECUTION_FAILED", "当前没有已暂存的变更，无法提交")
	}
	commandArgs := []string{"-C", repoRoot, "commit", "-m", message}
	if amend {
		commandArgs = append(commandArgs, "--amend")
	}
	if allowEmpty {
		commandArgs = append(commandArgs, "--allow-empty")
	}
	output, err := exec.CommandContext(ctx, "git", commandArgs...).CombinedOutput()
	if err != nil {
		messageText := strings.TrimSpace(string(output))
		if messageText == "" {
			messageText = err.Error()
		}
		return map[string]any{"repo_root": repoRoot, "message": message, "staged_files": stagedFiles}, newToolError("EXECUTION_FAILED", "执行 git commit 失败: %s", messageText)
	}
	hashOutput, hashErr := exec.CommandContext(ctx, "git", "-C", repoRoot, "rev-parse", "HEAD").Output()
	if hashErr != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取最新提交失败: %v", hashErr)
	}
	commitHash := strings.TrimSpace(string(hashOutput))
	subject := strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	fmt.Fprintf(r.stdout, "[本地工具] git_commit %s（%d 个文件）\n", repoRoot, len(stagedFiles))
	_ = appendAuditLog(config, "git_commit", map[string]any{"repo": repoRoot, "hash": commitHash, "amend": amend, "allow_empty": allowEmpty, "count": len(stagedFiles)})
	return map[string]any{
		"repo_root":    repoRoot,
		"hash":         commitHash,
		"message":      message,
		"subject":      subject,
		"staged_files": stagedFiles,
		"count":        len(stagedFiles),
		"amend":        amend,
		"allow_empty":  allowEmpty,
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
	baseDir, err := resolveAllowedDirectory(config, stringValue(arguments["path"]))
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
	fmt.Fprintf(r.stdout, "[本地工具] search_files %s 命中 %d 条\n", baseDir, len(results))
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

type patchFile struct {
	Path  string
	Hunks []patchHunk
}

type patchHunk struct {
	OldStart int
	Lines    []patchLine
}

type patchLine struct {
	Kind rune
	Text string
}

type patchApplyError struct {
	Reason     string
	HunkIndex  int
	LineNumber int
	Operation  string
	Expected   string
	Actual     string
}

func (e patchApplyError) Error() string {
	if e.Expected != "" {
		return fmt.Sprintf("%s: 第 %d 个 hunk，第 %d 行，期望 %q，实际 %q", e.Reason, e.HunkIndex, e.LineNumber, e.Expected, e.Actual)
	}
	return fmt.Sprintf("%s: 第 %d 个 hunk，第 %d 行", e.Reason, e.HunkIndex, e.LineNumber)
}

func parseUnifiedPatch(content string) ([]patchFile, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	files := []patchFile{}
	index := 0
	for index < len(lines) {
		line := lines[index]
		if !strings.HasPrefix(line, "--- ") {
			index++
			continue
		}
		if index+1 >= len(lines) || !strings.HasPrefix(lines[index+1], "+++ ") {
			return nil, fmt.Errorf("patch 文件头不完整")
		}
		path := strings.TrimSpace(strings.TrimPrefix(lines[index+1], "+++ "))
		path = strings.TrimPrefix(path, "b/")
		currentFile := patchFile{Path: path}
		index += 2
		for index < len(lines) && strings.HasPrefix(lines[index], "@@ ") {
			hunkHeader := lines[index]
			oldStart, err := parseOldStart(hunkHeader)
			if err != nil {
				return nil, err
			}
			index++
			hunk := patchHunk{OldStart: oldStart}
			for index < len(lines) {
				currentLine := lines[index]
				if strings.HasPrefix(currentLine, "@@ ") || strings.HasPrefix(currentLine, "--- ") {
					break
				}
				if currentLine == "" && index == len(lines)-1 {
					index++
					continue
				}
				if currentLine == "\\ No newline at end of file" {
					index++
					continue
				}
				kind := rune(currentLine[0])
				if kind != ' ' && kind != '+' && kind != '-' {
					return nil, fmt.Errorf("不支持的补丁行: %s", currentLine)
				}
				hunk.Lines = append(hunk.Lines, patchLine{Kind: kind, Text: currentLine[1:]})
				index++
			}
			currentFile.Hunks = append(currentFile.Hunks, hunk)
		}
		files = append(files, currentFile)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("未解析到任何 patch 文件")
	}
	return files, nil
}

func parseOldStart(header string) (int, error) {
	re := regexp.MustCompile(`@@ -(\d+)`)
	matches := re.FindStringSubmatch(header)
	if len(matches) != 2 {
		return 0, fmt.Errorf("无法解析 hunk 头: %s", header)
	}
	value := 0
	_, err := fmt.Sscanf(matches[1], "%d", &value)
	if err != nil {
		return 0, err
	}
	if value < 1 {
		value = 1
	}
	return value, nil
}

func applyPatchToContent(original []byte, filePatch patchFile) ([]byte, error) {
	originalLines := splitLines(string(original))
	result := make([]string, 0, len(originalLines))
	originalIndex := 0
	for hunkIndex, hunk := range filePatch.Hunks {
		targetIndex := hunk.OldStart - 1
		if targetIndex < originalIndex || targetIndex > len(originalLines) {
			return nil, patchApplyError{Reason: "hunk 定位失败", HunkIndex: hunkIndex + 1, LineNumber: hunk.OldStart, Operation: "locate"}
		}
		result = append(result, originalLines[originalIndex:targetIndex]...)
		currentIndex := targetIndex
		for _, line := range hunk.Lines {
			switch line.Kind {
			case ' ':
				if currentIndex >= len(originalLines) || originalLines[currentIndex] != line.Text {
					actual := "<EOF>"
					if currentIndex < len(originalLines) {
						actual = originalLines[currentIndex]
					}
					return nil, patchApplyError{Reason: "上下文不匹配", HunkIndex: hunkIndex + 1, LineNumber: currentIndex + 1, Operation: "context", Expected: line.Text, Actual: actual}
				}
				result = append(result, line.Text)
				currentIndex++
			case '-':
				if currentIndex >= len(originalLines) || originalLines[currentIndex] != line.Text {
					actual := "<EOF>"
					if currentIndex < len(originalLines) {
						actual = originalLines[currentIndex]
					}
					return nil, patchApplyError{Reason: "删除行不匹配", HunkIndex: hunkIndex + 1, LineNumber: currentIndex + 1, Operation: "delete", Expected: line.Text, Actual: actual}
				}
				currentIndex++
			case '+':
				result = append(result, line.Text)
			}
		}
		originalIndex = currentIndex
	}
	result = append(result, originalLines[originalIndex:]...)
	joined := strings.Join(result, "\n")
	if len(original) > 0 && original[len(original)-1] == '\n' {
		joined += "\n"
	}
	return []byte(joined), nil
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

func buildPatchConflict(path string, err error) map[string]any {
	conflict := map[string]any{
		"path":   path,
		"reason": err.Error(),
	}
	var applyErr patchApplyError
	if errors.As(err, &applyErr) {
		conflict["hunk_index"] = applyErr.HunkIndex
		conflict["line_number"] = applyErr.LineNumber
		conflict["operation"] = applyErr.Operation
		conflict["expected"] = applyErr.Expected
		conflict["actual"] = applyErr.Actual
	}
	return conflict
}

func resolveGitRepository(config internalconfig.Config, requested string) (string, string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", "", newToolError("EXECUTION_FAILED", "系统未安装 git，无法执行 Git 工具")
	}
	target := strings.TrimSpace(requested)
	baseDir, err := resolveAllowedDirectory(config, "")
	if err != nil {
		return "", "", err
	}
	workingPath := baseDir
	if target != "" {
		workingPath, err = resolveAllowedPath(config, target, false)
		if err != nil {
			return "", "", err
		}
	}
	stat, err := os.Stat(workingPath)
	if err != nil {
		return "", "", newToolError("EXECUTION_FAILED", "读取 Git 目标路径失败: %v", err)
	}
	commandDir := workingPath
	if !stat.IsDir() {
		commandDir = filepath.Dir(workingPath)
	}
	output, err := exec.Command("git", "-C", commandDir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", "", newToolError("EXECUTION_FAILED", "目标路径不在 Git 仓库中: %s", workingPath)
	}
	repoRoot := strings.TrimSpace(string(output))
	repoRoot, err = resolveAllowedDirectory(config, repoRoot)
	if err != nil {
		return "", "", err
	}
	relativePath := ""
	if target != "" && workingPath != repoRoot {
		relativePath, err = filepath.Rel(repoRoot, workingPath)
		if err != nil {
			return "", "", newToolError("EXECUTION_FAILED", "计算仓库相对路径失败: %v", err)
		}
		relativePath = filepath.ToSlash(relativePath)
	}
	return repoRoot, relativePath, nil
}

func resolveGitAddTargets(config internalconfig.Config, arguments map[string]any) (string, []string, bool, error) {
	requestedPaths := normalizeGitPathList(stringSlice(arguments["paths"]))
	requestedPath := strings.TrimSpace(stringValue(arguments["path"]))
	addAll := boolValue(arguments["all"])
	if len(requestedPaths) == 0 && requestedPath != "" {
		requestedPaths = []string{requestedPath}
	}
	if addAll {
		repoRoot, relativePath, err := resolveGitRepository(config, requestedPath)
		if err != nil {
			return "", nil, false, err
		}
		if relativePath == "" {
			return repoRoot, nil, true, nil
		}
		return repoRoot, []string{relativePath}, true, nil
	}
	if len(requestedPaths) == 0 {
		repoRoot, relativePath, err := resolveGitRepository(config, requestedPath)
		if err != nil {
			return "", nil, false, err
		}
		if relativePath == "" {
			return repoRoot, []string{"."}, false, nil
		}
		return repoRoot, []string{relativePath}, false, nil
	}
	repoRoot := ""
	resolved := []string{}
	seen := map[string]struct{}{}
	for _, item := range requestedPaths {
		currentRepo, relativePath, err := resolveGitRepository(config, item)
		if err != nil {
			return "", nil, false, err
		}
		if repoRoot == "" {
			repoRoot = currentRepo
		} else if repoRoot != currentRepo {
			return "", nil, false, newToolError("PARAM_ERROR", "git_add 的多个路径必须位于同一个仓库")
		}
		if relativePath == "" {
			relativePath = "."
		}
		if _, ok := seen[relativePath]; ok {
			continue
		}
		seen[relativePath] = struct{}{}
		resolved = append(resolved, relativePath)
	}
	return repoRoot, resolved, false, nil
}

func normalizeGitPathList(items []string) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func gitStagedFiles(ctx context.Context, repoRoot string) ([]string, error) {
	output, err := exec.CommandContext(ctx, "git", "-C", repoRoot, "diff", "--cached", "--name-only").Output()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "读取已暂存文件失败: %v", err)
	}
	files := []string{}
	for _, line := range splitLines(string(output)) {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, nil
}
