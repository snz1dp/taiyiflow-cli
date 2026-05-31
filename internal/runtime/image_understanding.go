package runtime

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const (
	imageUnderstandingSystemPromptComponent = "Prompt-d2zsV"
	imageUnderstandingUserPromptComponent   = "Prompt-GRXIq"
	imageUnderstandingTextOutputComponent   = "TextOutput-fXIUJ"

	screenshotImageUnderstandingSystemTemplate = "你是截图理解助手。当前输入图像仅来自桌面 ComputerUse 或浏览器自动化截图，不是文档结构化解析任务。请只依据截图中可见的像素内容，优先识别界面结构、可交互元素、状态变化、错误提示、可见文本和与当前问题直接相关的线索；不要臆测不可见内容，不要补全被遮挡区域，也不要把截图按文档理解流程处理。"
	screenshotImageUnderstandingUserTemplate   = "请仅依据截图中可见内容回答问题，并且只返回纯文本结果；若截图中没有足够证据，请直接说明“截图中未见相关信息”。\n问题：{question}"
	screenshotImageUnderstandingDefaultInput  = "请概述截图中的界面结构、可见文本、可交互元素和关键状态信息。"
)

func (r *ClientRuntime) understandImageWithProvider(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	arguments map[string]any,
	localPath string,
) (string, error) {
	trimmedLocalPath := strings.TrimSpace(localPath)
	if trimmedLocalPath == "" {
		return "", fmt.Errorf("调用图像理解服务失败: 截图文件路径不能为空")
	}
	response, err := r.runImageUnderstanding(ctx, providerClient, config, arguments, []string{trimmedLocalPath}, buildScreenshotImageUnderstandingTweaks())
	if err != nil {
		return "", fmt.Errorf("调用图像理解服务失败: %w", err)
	}
	return strings.TrimSpace(stringValue(response["text"])), nil
}

func (r *ClientRuntime) imageUnderstanding(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	paths, err := imageUnderstandingPaths(arguments)
	if err != nil {
		return nil, err
	}
	return r.runImageUnderstanding(ctx, providerClient, config, arguments, paths, buildDefaultImageUnderstandingTweaks())
}

func (r *ClientRuntime) runImageUnderstanding(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	arguments map[string]any,
	paths []string,
	tweaks map[string]any,
) (map[string]any, error) {
	if providerClient == nil {
		return nil, newToolError("EXECUTION_FAILED", "image_understanding 缺少 provider 上下文")
	}
	imageRunner, ok := providerClient.(provider.ImageUnderstandingRunner)
	if !ok {
		return nil, newToolError("EXECUTION_FAILED", "当前 provider 不支持 image_understanding")
	}
	serviceConfig, err := providerClient.GetServiceConfig(ctx)
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "获取图像理解服务配置失败: %v", err)
	}
	if serviceConfig == nil || strings.TrimSpace(serviceConfig.ImageFlowID) == "" {
		return nil, newToolError("EXECUTION_FAILED", "服务端未配置 image_flow_id，无法执行图像理解")
	}
	flowID := strings.TrimSpace(argumentString(arguments, "flow_id", "flowID"))
	if flowID == "" {
		return nil, newToolError("PARAM_ERROR", "image_understanding 缺少 flow_id")
	}
	workingDirectory := strings.TrimSpace(argumentString(arguments, "working_directory", "workingDirectory"))
	if workingDirectory == "" {
		workingDirectory = strings.TrimSpace(config.Workdir)
	}
	sessionID, err := EnsureUploadSessionID(ctx, providerClient, argumentString(arguments, "session_id", "sessionID"))
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
		if !internalconfig.IsImageUnderstandingExtension(extension) {
			return nil, newToolError("PARAM_ERROR", "文件 %s 不是受支持的图片格式，请改用 document_unstructured 或其他工具", resolvedPath)
		}
		remotePath, uploadErr := UploadInputFile(ctx, providerClient, flowID, sessionID, workingDirectory, rawPath)
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
	compressionStrategy := strings.TrimSpace(argumentString(arguments, "image_compression_strategy"))
	if compressionStrategy == "" {
		compressionStrategy = provider.ImageCompressionStrategyFollowSystem
	}
	result, err := imageRunner.RunImageUnderstanding(ctx, provider.ImageUnderstandingRequest{
		FlowID:                   strings.TrimSpace(serviceConfig.ImageFlowID),
		Input:                    argumentString(arguments, "input_value", "input", "prompt"),
		InputType:                fallbackString(argumentString(arguments, "input_type", "inputType"), "any"),
		Tweaks:                   tweaks,
		InputFiles:               remotePaths,
		OutputType:               "any",
		SessionID:                sessionID,
		ImageCompressionStrategy: compressionStrategy,
	})
	if err != nil {
		return nil, newToolError("EXECUTION_FAILED", "调用图像理解服务失败: %v", err)
	}
	if result == nil {
		return nil, newToolError("EXECUTION_FAILED", "服务端图像理解流程未返回结果")
	}
	text := selectImageUnderstandingText(result)
	response := map[string]any{
		"text":           text,
		"media_type":     strings.TrimSpace(result.MediaType),
		"session_id":     fallbackString(strings.TrimSpace(result.SessionID), sessionID),
		"uploaded_files": uploadedFiles,
	}
	if rawOutput := strings.TrimSpace(result.Output); rawOutput != "" {
		response["raw_output"] = rawOutput
	}
	return response, nil
}

func imageUnderstandingPaths(arguments map[string]any) ([]string, error) {
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
		return nil, newToolError("PARAM_ERROR", "image_understanding 至少需要提供 path 或 paths")
	}
	filtered := make([]string, 0, len(paths))
	for _, path := range paths {
		if trimmed := strings.TrimSpace(path); trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	if len(filtered) == 0 {
		return nil, newToolError("PARAM_ERROR", "image_understanding 至少需要提供一个非空文件路径")
	}
	return filtered, nil
}

func ensureScreenshotImageUnderstandingInput(arguments map[string]any) {
	if arguments == nil {
		return
	}
	if strings.TrimSpace(argumentString(arguments, "input_value")) != "" {
		return
	}
	if input := strings.TrimSpace(argumentString(arguments, "input", "prompt")); input != "" {
		arguments["input_value"] = input
		return
	}
	arguments["input_value"] = screenshotImageUnderstandingDefaultInput
	}

func buildDefaultImageUnderstandingTweaks() map[string]any {
	return map[string]any{
		imageUnderstandingTextOutputComponent: map[string]any{
			"output_format": "text",
		},
	}
}

func selectImageUnderstandingText(result *provider.ImageUnderstandingResult) string {
	if result == nil {
		return ""
	}
	if trimmed := strings.TrimSpace(result.Text); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(result.Output)
}

func buildScreenshotImageUnderstandingTweaks() map[string]any {
	return map[string]any{
		imageUnderstandingSystemPromptComponent: map[string]any{
			"template": screenshotImageUnderstandingSystemTemplate,
		},
		imageUnderstandingUserPromptComponent: map[string]any{
			"template": screenshotImageUnderstandingUserTemplate,
		},
		imageUnderstandingTextOutputComponent: map[string]any{
			"output_format": "text",
		},
	}
}
