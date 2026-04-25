package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func (r *ClientRuntime) globFiles(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	baseDir, err := r.resolveAllowedDirectoryWithPolicy(config, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}
	pattern := strings.TrimSpace(stringValue(arguments["pattern"]))
	if pattern == "" {
		return nil, newToolError("PARAM_ERROR", "glob_files 缺少 pattern")
	}
	kind := strings.ToLower(strings.TrimSpace(fallbackString(stringValue(arguments["kind"]), "all")))
	includeHidden := boolValue(arguments["include_hidden"])
	maxResults := intValue(arguments["max_results"], 200)
	if maxResults < 1 {
		maxResults = 1
	}
	if maxResults > 1000 {
		maxResults = 1000
	}
	items := []map[string]any{}
	err = filepath.WalkDir(baseDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == baseDir {
			return nil
		}
		relPath, relErr := filepath.Rel(baseDir, path)
		if relErr != nil {
			relPath = entry.Name()
		}
		relPath = filepath.ToSlash(relPath)
		if isHiddenPath(relPath) && !includeHidden {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !matchGlob(pattern, relPath) {
			return nil
		}
		entryKind := "file"
		if entry.IsDir() {
			entryKind = "directory"
		}
		if kind != "all" && kind != entryKind {
			return nil
		}
		items = append(items, map[string]any{
			"path":          path,
			"relative_path": relPath,
			"kind":          entryKind,
		})
		if len(items) >= maxResults {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return nil, newToolError("EXECUTION_FAILED", "glob_files 扫描失败: %v", err)
	}
	sort.Slice(items, func(left, right int) bool {
		return stringValue(items[left]["relative_path"]) < stringValue(items[right]["relative_path"])
	})
	r.writeStreamEventf(r.stdout, "[本地工具] glob_files %s 命中 %d 项\n", pattern, len(items))
	_ = appendAuditLog(config, "glob_files", map[string]any{"path": baseDir, "pattern": pattern, "count": len(items)})
	return map[string]any{"root": baseDir, "pattern": pattern, "items": items, "count": len(items)}, nil
}

func (r *ClientRuntime) todoRead(config internalconfig.Config, payload map[string]any) (map[string]any, error) {
	state, err := loadRuntimeSessionState(payload, config)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(state.TodoItems))
	for _, item := range state.TodoItems {
		items = append(items, map[string]any{"title": item.Title, "status": item.Status, "notes": item.Notes})
	}
	r.writeStreamEventf(r.stdout, "[本地工具] todo_read %d 项\n", len(items))
	_ = appendAuditLog(config, "todo_read", map[string]any{"flow_id": state.FlowID, "count": len(items)})
	return map[string]any{"items": items, "count": len(items), "session_id": state.SessionID}, nil
}

func (r *ClientRuntime) todoWrite(config internalconfig.Config, payload map[string]any, arguments map[string]any) (map[string]any, error) {
	items, err := parseTodoItems(arguments)
	if err != nil {
		return nil, err
	}
	store, state, err := loadSessionStoreAndState(payload, config)
	if err != nil {
		return nil, err
	}
	state.TodoItems = items
	if sessionID := strings.TrimSpace(nestedString(payload, "context", "session_id")); sessionID != "" {
		state.SessionID = sessionID
	}
	if err := store.Save(state); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "保存 Todo 失败: %v", err)
	}
	resultItems := make([]map[string]any, 0, len(items))
	for _, item := range items {
		resultItems = append(resultItems, map[string]any{"title": item.Title, "status": item.Status, "notes": item.Notes})
	}
	r.writeStreamEventf(r.stdout, "[本地工具] todo_write %d 项\n", len(resultItems))
	_ = appendAuditLog(config, "todo_write", map[string]any{"flow_id": state.FlowID, "count": len(resultItems)})
	return map[string]any{"items": resultItems, "count": len(resultItems), "session_id": state.SessionID}, nil
}

func (r *ClientRuntime) fileEdit(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	targetPath := strings.TrimSpace(stringValue(arguments["path"]))
	if targetPath == "" {
		return nil, newToolError("PARAM_ERROR", "file_edit 缺少 path")
	}
	oldString := stringValue(arguments["old_string"])
	if oldString == "" {
		return nil, newToolError("PARAM_ERROR", "file_edit 缺少 old_string")
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
		return nil, newToolError("PARAM_ERROR", "file_edit 不支持二进制文件: %s", resolvedPath)
	}
	before := string(content)
	beforeHash := hashSHA256(content)
	if expected := strings.TrimSpace(stringValue(arguments["expected_sha256_before"])); expected != "" && expected != beforeHash {
		result := buildFileEditDiagnosticResult(resolvedPath, before, beforeHash, oldString, map[string]any{
			"status":                 "stale_content",
			"expected_sha256_before": expected,
			"occurrences":            0,
			"replacements":           0,
		})
		return result, newToolError("PATCH_CONFLICT", "文件内容摘要不匹配，当前=%s；请先 read_file 获取最新内容后再重试", beforeHash)
	}
	occurrences := strings.Count(before, oldString)
	if occurrences == 0 {
		result := buildFileEditDiagnosticResult(resolvedPath, before, beforeHash, oldString, map[string]any{
			"status":       "not_found",
			"occurrences":  0,
			"replacements": 0,
		})
		return result, newToolError("PATCH_CONFLICT", "file_edit 未找到 old_string；old_string 必须与当前文件内容完全一致，请先 read_file 后再重试")
	}
	replaceAll := boolValue(arguments["replace_all"])
	if occurrences > 1 && !replaceAll {
		result := buildFileEditDiagnosticResult(resolvedPath, before, beforeHash, oldString, map[string]any{
			"status":       "ambiguous_match",
			"occurrences":  occurrences,
			"replacements": 0,
			"replace_all":  false,
		})
		return result, newToolError("PATCH_CONFLICT", "file_edit 匹配到 %d 处，需显式开启 replace_all 或提供更精确的 old_string", occurrences)
	}
	newString := stringValue(arguments["new_string"])
	replacements := 1
	after := strings.Replace(before, oldString, newString, 1)
	if replaceAll {
		replacements = occurrences
		after = strings.ReplaceAll(before, oldString, newString)
	}
	dryRun := boolValue(arguments["dry_run"])
	afterHash := hashSHA256([]byte(after))
	if !dryRun {
		if err := writeFileAtomically(resolvedPath, []byte(after)); err != nil {
			return nil, newToolError("EXECUTION_FAILED", "写入 file_edit 结果失败: %v", err)
		}
	}
	result := map[string]any{
		"status":              "applied",
		"path":                resolvedPath,
		"dry_run":             dryRun,
		"replacements":        replacements,
		"occurrences":         occurrences,
		"replace_all":         replaceAll,
		"bytes_before":        len(before),
		"bytes_after":         len(after),
		"sha256_before":       beforeHash,
		"sha256_after":        afterHash,
		"changed":             before != after,
		"preview_before":      trimMultilinePreview(oldString, 120),
		"preview_after":       trimMultilinePreview(newString, 120),
	}
	r.writeStreamEventf(r.stdout, "[本地工具] file_edit %s 替换 %d 处%s\n", resolvedPath, replacements, map[bool]string{true: "（dry-run）", false: ""}[dryRun])
	_ = appendAuditLog(config, "file_edit", map[string]any{"path": resolvedPath, "replacements": replacements, "dry_run": dryRun})
	return result, nil
}

func buildFileEditDiagnosticResult(path string, before string, beforeHash string, oldString string, extras map[string]any) map[string]any {
	result := map[string]any{
		"path":                 path,
		"sha256_before":        beforeHash,
		"changed":              false,
		"preview_before":       trimMultilinePreview(oldString, 120),
		"exact_match_required": true,
		"suggestions":          buildFileEditSuggestions(before, oldString),
	}
	for key, value := range extras {
		result[key] = value
	}
	if normalizedMatch := strings.Contains(strings.ReplaceAll(before, "\r\n", "\n"), strings.ReplaceAll(oldString, "\r\n", "\n")); normalizedMatch {
		result["normalized_newlines_match"] = true
	}
	if trimmed := strings.TrimSpace(oldString); trimmed != "" && trimmed != oldString && strings.Contains(before, trimmed) {
		result["trimmed_match_found"] = true
	}
	if excerpt := nearestFileEditExcerpt(before, oldString); excerpt != "" {
		result["nearby_excerpt"] = excerpt
	}
	return result
	}

func buildFileEditSuggestions(before string, oldString string) []string {
	suggestions := []string{
		"先调用 read_file 获取目标文件最新内容，再复制精确片段作为 old_string。",
		"old_string 必须与当前文件内容完全一致，包括缩进、空格和换行。",
	}
	if strings.Contains(strings.ReplaceAll(before, "\r\n", "\n"), strings.ReplaceAll(oldString, "\r\n", "\n")) {
		suggestions = append(suggestions, "看起来更像是换行风格不一致；请直接从 read_file 返回结果复制 old_string。")
	}
	trimmed := strings.TrimSpace(oldString)
	if trimmed != "" && trimmed != oldString && strings.Contains(before, trimmed) {
		suggestions = append(suggestions, "文件里可能存在相同文本但前后缩进或空白不同；请把完整上下文一起作为 old_string。")
	}
	if strings.Count(before, oldString) > 1 {
		suggestions = append(suggestions, "当前片段命中多处；如果确实要全量替换，请显式设置 replace_all=true。")
	}
	return suggestions
}

func nearestFileEditExcerpt(before string, oldString string) string {
	lines := splitLines(before)
	if len(lines) == 0 {
		return ""
	}
	needleLines := splitLines(oldString)
	needle := ""
	for _, line := range needleLines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			needle = trimmed
			break
		}
	}
	if needle == "" {
		needle = strings.TrimSpace(oldString)
	}
	if needle == "" {
		return ""
	}
	for index, line := range lines {
		if !strings.Contains(line, needle) {
			continue
		}
		start := index - 1
		if start < 0 {
			start = 0
		}
		end := index + 2
		if end > len(lines) {
			end = len(lines)
		}
		preview := make([]string, 0, end-start)
		for current := start; current < end; current++ {
			preview = append(preview, fmt.Sprintf("%d: %s", current+1, lines[current]))
		}
		return strings.Join(preview, "\n")
	}
	end := 3
	if end > len(lines) {
		end = len(lines)
	}
	preview := make([]string, 0, end)
	for current := 0; current < end; current++ {
		preview = append(preview, fmt.Sprintf("%d: %s", current+1, lines[current]))
	}
	return strings.Join(preview, "\n")
}

func parseTodoItems(arguments map[string]any) ([]agent.TodoItem, error) {
	rawItems, ok := arguments["items"].([]any)
	if !ok {
		return nil, newToolError("PARAM_ERROR", "todo_write items 必须是数组")
	}
	items := make([]agent.TodoItem, 0, len(rawItems))
	for _, rawItem := range rawItems {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, newToolError("PARAM_ERROR", "todo_write items 中存在非法对象")
		}
		title := strings.TrimSpace(stringValue(item["title"]))
		if title == "" {
			return nil, newToolError("PARAM_ERROR", "todo_write title 不能为空")
		}
		items = append(items, agent.TodoItem{
			Title:  title,
			Status: normalizeTodoStatusValue(item["status"]),
			Notes:  strings.TrimSpace(stringValue(item["notes"])),
		})
	}
	return items, nil
}

func normalizeTodoStatusValue(value any) string {
	switch strings.ToLower(strings.TrimSpace(stringValue(value))) {
	case "in_progress", "in-progress":
		return "in_progress"
	case "completed", "done":
		return "completed"
	default:
		return "not_started"
	}
}

func loadRuntimeSessionState(payload map[string]any, config internalconfig.Config) (agent.SessionState, error) {
	_, state, err := loadSessionStoreAndState(payload, config)
	return state, err
}

func loadSessionStoreAndState(payload map[string]any, config internalconfig.Config) (*agent.SessionStore, agent.SessionState, error) {
	flowID := strings.TrimSpace(stringValue(payload["flow_id"]))
	if flowID == "" {
		return nil, agent.SessionState{}, newToolError("PARAM_ERROR", "当前工具调用缺少 flow_id，无法关联会话")
	}
	store, err := agent.NewSessionStore()
	if err != nil {
		return nil, agent.SessionState{}, newToolError("EXECUTION_FAILED", "初始化会话存储失败: %v", err)
	}
	state, err := store.Load(flowID, config.Workdir)
	if err != nil {
		return nil, agent.SessionState{}, newToolError("EXECUTION_FAILED", "读取会话状态失败: %v", err)
	}
	return store, state, nil
}

func hashSHA256(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}