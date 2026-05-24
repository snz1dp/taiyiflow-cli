package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// EnsureSessionID 返回当前请求所需的会话 ID；缺失时向服务端申请新的 session_id。
func EnsureSessionID(ctx context.Context, providerClient provider.Provider, sessionID string) (string, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID != "" {
		return trimmedSessionID, nil
	}
	if providerClient == nil {
		return "", fmt.Errorf("创建会话失败: provider 不可用")
	}
	createdSessionID, err := providerClient.CreateSessionID(ctx)
	if err != nil {
		return "", fmt.Errorf("创建会话失败: 创建会话 ID 失败: %w", err)
	}
	trimmedSessionID = strings.TrimSpace(createdSessionID)
	if trimmedSessionID == "" {
		return "", fmt.Errorf("创建会话失败: 服务端未返回会话 ID")
	}
	return trimmedSessionID, nil
}

// EnsureUploadSessionID 返回上传所需的会话 ID；缺失时向服务端申请新的 session_id。
func EnsureUploadSessionID(ctx context.Context, providerClient provider.Provider, sessionID string) (string, error) {
	trimmedSessionID, err := EnsureSessionID(ctx, providerClient, sessionID)
	if err == nil {
		return trimmedSessionID, nil
	}
	message := err.Error()
	message = strings.TrimPrefix(message, "创建会话失败: ")
	return "", fmt.Errorf("上传文件失败: %s", message)
}

func UploadInputFile(
	ctx context.Context,
	providerClient provider.Provider,
	flowID string,
	sessionID string,
	baseDir string,
	localPath string,
) (string, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return "", fmt.Errorf("上传文件失败: 当前会话 ID 不能为空")
	}
	resolvedPath, err := ResolveUploadFilePath(baseDir, localPath)
	if err != nil {
		return "", err
	}
	uploadResult, err := providerClient.UploadFile(ctx, flowID, trimmedSessionID, resolvedPath, "")
	if err != nil {
		return "", err
	}
	if uploadResult == nil || strings.TrimSpace(uploadResult.FilePath) == "" {
		return "", fmt.Errorf("上传文件失败: 服务端未返回文件路径")
	}
	return strings.TrimSpace(uploadResult.FilePath), nil
}

func ResolveUploadFilePath(baseDir string, localPath string) (string, error) {
	trimmedPath := strings.TrimSpace(localPath)
	if trimmedPath == "" {
		return "", fmt.Errorf("上传文件失败: 本地文件路径不能为空")
	}
	resolvedPath := trimmedPath
	if !filepath.IsAbs(resolvedPath) {
		anchorDir := strings.TrimSpace(baseDir)
		if anchorDir == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return "", fmt.Errorf("上传文件失败: 获取当前目录失败: %w", err)
			}
			anchorDir = cwd
		}
		resolvedPath = filepath.Join(anchorDir, resolvedPath)
	}
	absPath, err := filepath.Abs(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("上传文件失败: 解析文件路径失败: %w", err)
	}
	fileInfo, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("上传文件失败: %w", err)
	}
	if fileInfo.IsDir() {
		return "", fmt.Errorf("上传文件失败: %s 是目录，请提供文件路径", absPath)
	}
	return absPath, nil
}
