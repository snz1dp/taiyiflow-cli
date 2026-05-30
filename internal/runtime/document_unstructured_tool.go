package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func (r *ClientRuntime) documentUnstructured(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	if providerClient == nil {
		return nil, newToolError("EXECUTION_FAILED", "document_unstructured 缺少 provider 上下文")
	}
	currentFlowID := argumentString(arguments, "flow_id")
	if currentFlowID == "" {
		return nil, newToolError("PARAM_ERROR", "document_unstructured 缺少 flow_id")
	}
	paths, err := documentUnstructuredPaths(arguments)
	if err != nil {
		return nil, err
	}
	workingDirectory := argumentString(arguments, "working_directory", "workingDirectory")
	if workingDirectory == "" {
		workingDirectory = strings.TrimSpace(config.Workdir)
	}
	hasImageInput := false
	hasNonImageInput := false
	for _, rawPath := range paths {
		resolvedPath, resolveErr := ResolveUploadFilePath(workingDirectory, rawPath)
		if resolveErr != nil {
			return nil, newToolError("PARAM_ERROR", "%v", resolveErr)
		}
		extension := normalizeDocumentExtension(filepath.Ext(resolvedPath))
		if internalconfig.IsImageUnderstandingExtension(extension) {
			hasImageInput = true
		} else {
			hasNonImageInput = true
		}
	}
	if hasImageInput {
		if hasNonImageInput {
			return nil, newToolError("PARAM_ERROR", "document_unstructured 不支持图片与文档混合输入；图片请改用 image_understanding")
		}
		return r.runImageUnderstanding(ctx, providerClient, config, arguments, paths, buildDefaultImageUnderstandingTweaks())
	}
	configuredExts := internalconfig.NormalizeDocUnstructuredExts(config.DocUnstructuredExts)
	if len(configuredExts) == 0 {
		return nil, newToolError("EXECUTION_FAILED", "当前未配置 doc_unstructured_exts，请先在全局配置中启用需要走服务端结构化解析的扩展名")
	}
	serviceConfig, err := providerClient.GetServiceConfig(ctx)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取服务端配置失败: %v", err)
	}
	if serviceConfig == nil || strings.TrimSpace(serviceConfig.DocUnstructuredFlowID) == "" {
		return nil, newToolError("EXECUTION_FAILED", "服务端未配置 doc_unstructured_flow_id，无法执行文档结构化解析")
	}
	serviceExts := internalconfig.NormalizeDocUnstructuredExts(serviceConfig.DocExts)
	serviceExtSet := make(map[string]struct{}, len(serviceExts))
	for _, extension := range serviceExts {
		serviceExtSet[extension] = struct{}{}
	}
	configuredExtSet := make(map[string]struct{}, len(configuredExts))
	for _, extension := range configuredExts {
		configuredExtSet[extension] = struct{}{}
	}
	currentSessionID := argumentString(arguments, "session_id")
	currentSessionID, err = EnsureUploadSessionID(ctx, providerClient, currentSessionID)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "%v", err)
	}
	remotePaths := make([]string, 0, len(paths))
	uploadedFiles := make([]map[string]any, 0, len(paths))
	for _, rawPath := range paths {
		resolvedPath, resolveErr := ResolveUploadFilePath(workingDirectory, rawPath)
		if resolveErr != nil {
			return nil, newToolError("PARAM_ERROR", "%v", resolveErr)
		}
		extension := normalizeDocumentExtension(filepath.Ext(resolvedPath))
		if extension == "" {
			return nil, newToolError("PARAM_ERROR", "文件 %s 缺少可识别扩展名，无法使用 document_unstructured", resolvedPath)
		}
		if _, ok := configuredExtSet[extension]; !ok {
			return nil, newToolError("PARAM_ERROR", "文件 %s 的扩展名 .%s 未在 doc_unstructured_exts 中启用", resolvedPath, extension)
		}
		if len(serviceExtSet) > 0 {
			if _, ok := serviceExtSet[extension]; !ok {
				return nil, newToolError("EXECUTION_FAILED", "服务端当前不支持 .%s 文档结构化解析", extension)
			}
		}
		remotePath, uploadErr := UploadInputFile(ctx, providerClient, currentFlowID, currentSessionID, workingDirectory, rawPath)
		if uploadErr != nil {
			return nil, newToolError("EXECUTION_FAILED", "%v", uploadErr)
		}
		remotePaths = append(remotePaths, remotePath)
		uploadedFiles = append(uploadedFiles, map[string]any{
			"local_path":  resolvedPath,
			"remote_path": remotePath,
			"extension":   extension,
		})
	}
	result, err := providerClient.RunDocumentUnstructured(ctx, provider.DocumentUnstructuredRequest{
		FlowID:     strings.TrimSpace(serviceConfig.DocUnstructuredFlowID),
		Input:      argumentString(arguments, "input", "prompt"),
		InputType:  fallbackString(argumentString(arguments, "input_type", "inputType"), "any"),
		InputFiles: remotePaths,
		OutputType: fallbackString(argumentString(arguments, "output_type", "outputType"), "any"),
		SessionID:  currentSessionID,
	})
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "调用服务端文档结构化流程失败: %v", err)
	}
	if result == nil {
		return nil, newToolError("EXECUTION_FAILED", "服务端文档结构化流程未返回结果")
	}
	parsedOutput, parsed := parseDocumentUnstructuredOutput(result.Output)
	items := documentUnstructuredItems(result.Output, parsedOutput, parsed)
	response := map[string]any{
		"count":          len(items),
		"items":          items,
		"media_type":     strings.TrimSpace(result.MediaType),
		"session_id":     fallbackString(strings.TrimSpace(result.SessionID), currentSessionID),
		"uploaded_files": uploadedFiles,
	}
	if parsed && includeParsedDocumentOutput(parsedOutput) {
		response["parsed_output"] = parsedOutput
	} else if !parsed {
		if rawOutput := strings.TrimSpace(result.Output); rawOutput != "" {
			response["raw_output"] = rawOutput
		}
	}
	return response, nil
}

func documentUnstructuredPaths(arguments map[string]any) ([]string, error) {
	paths := make([]string, 0, 4)
	for _, key := range []string{"path", "file_path", "filePath"} {
		if value := argumentString(arguments, key); value != "" {
			paths = append(paths, value)
		}
	}
	for _, key := range []string{"paths", "file_paths", "filePaths"} {
		paths = append(paths, argumentStringSlice(arguments, key)...)
	}
	if len(paths) == 0 {
		return nil, newToolError("PARAM_ERROR", "document_unstructured 至少需要提供 path 或 paths")
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	if len(filtered) == 0 {
		return nil, newToolError("PARAM_ERROR", "document_unstructured 至少需要提供一个非空文件路径")
	}
	return filtered, nil
}

func normalizeDocumentExtension(extension string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(extension)), ".")
}

func parseDocumentUnstructuredOutput(raw string) (any, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}

func documentUnstructuredItems(raw string, parsed any, parsedOK bool) []any {
	if parsedOK {
		switch typed := parsed.(type) {
		case []any:
			return typed
		case map[string]any:
			if items, ok := typed["items"].([]any); ok {
				return items
			}
			return []any{typed}
		case string:
			if strings.TrimSpace(typed) == "" {
				return nil
			}
			return []any{map[string]any{"text": strings.TrimSpace(typed)}}
		default:
			return []any{typed}
		}
	}
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	return []any{map[string]any{"text": strings.TrimSpace(raw)}}
}

func includeParsedDocumentOutput(parsed any) bool {
	switch typed := parsed.(type) {
	case []any:
		return false
	case map[string]any:
		if len(typed) == 1 {
			_, onlyItems := typed["items"]
			return !onlyItems
		}
		return true
	default:
		return false
	}
}
