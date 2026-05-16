package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func UploadInputFile(
	ctx context.Context,
	providerClient provider.Provider,
	flowID string,
	sessionID string,
	baseDir string,
	localPath string,
) (string, error) {
	resolvedPath, err := ResolveUploadFilePath(baseDir, localPath)
	if err != nil {
		return "", err
	}
	uploadResult, err := providerClient.UploadFile(ctx, flowID, sessionID, resolvedPath, "")
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
