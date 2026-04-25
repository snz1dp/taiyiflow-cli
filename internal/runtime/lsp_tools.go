package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

type lspRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type lspResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *lspError       `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
}

type lspError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (r *ClientRuntime) lspQuery(config internalconfig.Config, arguments map[string]any) (map[string]any, error) {
	serverCommand := strings.TrimSpace(stringValue(arguments["server_command"]))
	if serverCommand == "" {
		return nil, newToolError("PARAM_ERROR", "lsp_query 缺少 server_command")
	}
	operation := strings.TrimSpace(stringValue(arguments["operation"]))
	switch operation {
	case "definition", "references", "implementation", "document_symbol", "workspace_symbol":
	default:
		return nil, newToolError("PARAM_ERROR", "不支持的 lsp 操作: %s", operation)
	}

	workspaceRoot, err := r.resolveAllowedDirectoryWithPolicy(config, stringValue(arguments["workspace_root"]))
	if err != nil {
		return nil, err
	}
	requestPath := strings.TrimSpace(stringValue(arguments["path"]))
	resolvedPath := ""
	if operation != "workspace_symbol" {
		if requestPath == "" {
			return nil, newToolError("PARAM_ERROR", "%s 缺少 path", operation)
		}
		resolvedPath, err = r.resolveAllowedFilePathWithPolicy(config, requestPath, false)
		if err != nil {
			return nil, err
		}
	}
	line := intValue(arguments["line"], 1)
	character := intValue(arguments["character"], 1)
	if requiresLSPPosition(operation) {
		if line < 1 || character < 1 {
			return nil, newToolError("PARAM_ERROR", "%s 需要从 1 开始的 line 和 character", operation)
		}
	}
	timeout := intValue(arguments["timeout"], defaultCommandTimeout)
	if timeout < 1 {
		timeout = defaultCommandTimeout
	}
	if timeout > maxCommandTimeout {
		timeout = maxCommandTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, serverCommand, stringSlice(arguments["server_args"])...)
	command.Dir = workspaceRoot
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "创建 LSP stdin 失败: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "创建 LSP stdout 失败: %v", err)
	}
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "启动 LSP 服务失败: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	}()

	reader := bufio.NewReader(stdout)
	rootURI := pathToFileURI(workspaceRoot)
	initializeParams := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   rootURI,
		"capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "taiyiflow-cli", "version": "dev"},
	}
	if _, err := callLSP(ctx, stdin, reader, 1, "initialize", initializeParams); err != nil {
		return nil, withLSPStderr(err, stderr.String())
	}
	if err := sendLSP(stdin, lspRequest{JSONRPC: "2.0", Method: "initialized", Params: map[string]any{}}); err != nil {
		return nil, withLSPStderr(newToolError("EXECUTION_FAILED", "发送 initialized 失败: %v", err), stderr.String())
	}

	if resolvedPath != "" {
		content, readErr := os.ReadFile(resolvedPath)
		if readErr != nil {
			return nil, newToolError("EXECUTION_FAILED", "读取 LSP 目标文件失败: %v", readErr)
		}
		if err := sendLSP(stdin, lspRequest{JSONRPC: "2.0", Method: "textDocument/didOpen", Params: map[string]any{
			"textDocument": map[string]any{
				"uri":        pathToFileURI(resolvedPath),
				"languageId": fallbackString(stringValue(arguments["language_id"]), inferLanguageID(resolvedPath)),
				"version":    1,
				"text":       string(content),
			},
		}}); err != nil {
			return nil, withLSPStderr(newToolError("EXECUTION_FAILED", "发送 didOpen 失败: %v", err), stderr.String())
		}
	}

	params, err := buildLSPQueryParams(operation, arguments, resolvedPath, line, character)
	if err != nil {
		return nil, err
	}
	response, err := callLSP(ctx, stdin, reader, 2, lspMethodName(operation), params)
	if err != nil {
		return nil, withLSPStderr(err, stderr.String())
	}
	items, err := normalizeLSPResult(operation, response.Result, workspaceRoot, intValue(arguments["max_results"], 50))
	if err != nil {
		return nil, err
	}
	r.writeStreamEventf(r.stdout, "[本地工具] lsp_query %s 命中 %d 项\n", operation, len(items))
	_ = appendAuditLog(config, "lsp_query", map[string]any{"operation": operation, "count": len(items), "workspace_root": workspaceRoot})
	return map[string]any{
		"operation":      operation,
		"server_command": serverCommand,
		"workspace_root": workspaceRoot,
		"items":          items,
		"count":          len(items),
	}, nil
}

func sendLSP(writer io.Writer, request lspRequest) error {
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	_, err = writer.Write(body)
	return err
}

func callLSP(ctx context.Context, writer io.Writer, reader *bufio.Reader, id int, method string, params any) (lspResponse, error) {
	if err := sendLSP(writer, lspRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return lspResponse{}, newToolError("EXECUTION_FAILED", "发送 LSP 请求失败: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return lspResponse{}, newToolError("EXECUTION_FAILED", "LSP 请求超时")
		default:
		}
		message, err := readLSPMessage(reader)
		if err != nil {
			return lspResponse{}, newToolError("EXECUTION_FAILED", "读取 LSP 响应失败: %v", err)
		}
		var response lspResponse
		if err := json.Unmarshal(message, &response); err != nil {
			return lspResponse{}, newToolError("EXECUTION_FAILED", "解析 LSP 响应失败: %v", err)
		}
		if response.Method != "" {
			continue
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			return lspResponse{}, newToolError("EXECUTION_FAILED", "LSP %s 失败: %s", method, response.Error.Message)
		}
		return response, nil
	}
}

func readLSPMessage(reader *bufio.Reader) ([]byte, error) {
	contentLength := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "content-length:") {
			_, err := fmt.Sscanf(trimmed, "Content-Length: %d", &contentLength)
			if err != nil {
				_, err = fmt.Sscanf(trimmed, "content-length: %d", &contentLength)
			}
			if err != nil {
				return nil, err
			}
		}
	}
	if contentLength <= 0 {
		return nil, io.ErrUnexpectedEOF
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(reader, body); err != nil {
		return nil, err
	}
	return body, nil
}

func buildLSPQueryParams(operation string, arguments map[string]any, resolvedPath string, line int, character int) (map[string]any, error) {
	switch operation {
	case "workspace_symbol":
		query := strings.TrimSpace(stringValue(arguments["symbol_query"]))
		if query == "" {
			return nil, newToolError("PARAM_ERROR", "workspace_symbol 缺少 symbol_query")
		}
		return map[string]any{"query": query}, nil
	case "document_symbol":
		return map[string]any{"textDocument": map[string]any{"uri": pathToFileURI(resolvedPath)}}, nil
	case "definition", "references", "implementation":
		params := map[string]any{
			"textDocument": map[string]any{"uri": pathToFileURI(resolvedPath)},
			"position":     map[string]any{"line": line - 1, "character": character - 1},
		}
		if operation == "references" {
			params["context"] = map[string]any{"includeDeclaration": boolValue(arguments["include_declaration"])}
		}
		return params, nil
	default:
		return nil, newToolError("PARAM_ERROR", "不支持的 lsp 操作: %s", operation)
	}
}

func lspMethodName(operation string) string {
	switch operation {
	case "document_symbol":
		return "textDocument/documentSymbol"
	case "workspace_symbol":
		return "workspace/symbol"
	default:
		return "textDocument/" + operation
	}
}

func normalizeLSPResult(operation string, raw json.RawMessage, workspaceRoot string, maxResults int) ([]map[string]any, error) {
	if maxResults < 1 {
		maxResults = 50
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || len(bytes.TrimSpace(raw)) == 0 {
		return []map[string]any{}, nil
	}
	switch operation {
	case "definition", "references", "implementation":
		return normalizeLSPLocations(raw, workspaceRoot, maxResults)
	case "document_symbol", "workspace_symbol":
		return normalizeLSPSymbols(raw, workspaceRoot, maxResults)
	default:
		return nil, newToolError("PARAM_ERROR", "不支持的 lsp 操作: %s", operation)
	}
}

func normalizeLSPLocations(raw json.RawMessage, workspaceRoot string, maxResults int) ([]map[string]any, error) {
	var many []map[string]any
	if err := json.Unmarshal(raw, &many); err == nil {
		return normalizeLocationItems(many, workspaceRoot, maxResults), nil
	}
	var single map[string]any
	if err := json.Unmarshal(raw, &single); err == nil {
		return normalizeLocationItems([]map[string]any{single}, workspaceRoot, maxResults), nil
	}
	return nil, newToolError("EXECUTION_FAILED", "无法解析 LSP 位置结果")
}

func normalizeLocationItems(items []map[string]any, workspaceRoot string, maxResults int) []map[string]any {
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		uri := firstNonEmptyLSPString(item, "uri", "targetUri")
		rangeValue := mapValue(item, "range")
		if len(rangeValue) == 0 {
			rangeValue = mapValue(item, "targetSelectionRange")
		}
		normalized := map[string]any{
			"uri":   uri,
			"path":  lspURIToPath(uri, workspaceRoot),
			"range": normalizeLSPRange(rangeValue),
		}
		result = append(result, normalized)
		if len(result) >= maxResults {
			break
		}
	}
	return result
}

func normalizeLSPSymbols(raw json.RawMessage, workspaceRoot string, maxResults int) ([]map[string]any, error) {
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, newToolError("EXECUTION_FAILED", "无法解析 LSP 符号结果")
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := map[string]any{
			"name":           stringValue(item["name"]),
			"kind":           intValue(item["kind"], 0),
			"container_name": stringValue(item["containerName"]),
		}
		if location := mapValue(item, "location"); len(location) > 0 {
			uri := stringValue(location["uri"])
			entry["uri"] = uri
			entry["path"] = lspURIToPath(uri, workspaceRoot)
			entry["range"] = normalizeLSPRange(mapValue(location, "range"))
		} else {
			uri := stringValue(item["uri"])
			entry["uri"] = uri
			entry["path"] = lspURIToPath(uri, workspaceRoot)
			selection := mapValue(item, "selectionRange")
			if len(selection) == 0 {
				selection = mapValue(item, "range")
			}
			entry["range"] = normalizeLSPRange(selection)
		}
		result = append(result, entry)
		if len(result) >= maxResults {
			break
		}
	}
	return result, nil
}

func normalizeLSPRange(raw map[string]any) map[string]any {
	start := mapValue(raw, "start")
	end := mapValue(raw, "end")
	return map[string]any{
		"start_line":      intValue(start["line"], 0) + 1,
		"start_character": intValue(start["character"], 0) + 1,
		"end_line":        intValue(end["line"], 0) + 1,
		"end_character":   intValue(end["character"], 0) + 1,
	}
}

func mapValue(raw map[string]any, key string) map[string]any {
	value, ok := raw[key].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return value
}

func firstNonEmptyLSPString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(stringValue(raw[key])); value != "" {
			return value
		}
	}
	return ""
}

func pathToFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func lspURIToPath(value string, workspaceRoot string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "file" {
		return value
	}
	if parsed.Path == "" {
		return workspaceRoot
	}
	return filepath.FromSlash(parsed.Path)
}

func inferLanguageID(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescriptreact"
	case ".js":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
	case ".md":
		return "markdown"
	default:
		return "plaintext"
	}
}

func requiresLSPPosition(operation string) bool {
	switch operation {
	case "definition", "references", "implementation":
		return true
	default:
		return false
	}
}

func withLSPStderr(err error, stderr string) error {
	trimmed := strings.TrimSpace(stderr)
	if trimmed == "" {
		return err
	}
	return newToolError("EXECUTION_FAILED", "%s；stderr: %s", err.Error(), trimMultilinePreview(trimmed, 240))
}