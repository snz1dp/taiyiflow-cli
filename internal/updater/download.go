package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProgressCallback 下载进度回调。
// downloaded 为已下载字节数，total 为总大小（-1 表示未知）。
type ProgressCallback func(downloaded, total int64)

// DownloadResult 下载结果。
type DownloadResult struct {
	Bytes  int64
	SHA256 string
	Path   string
}

// DownloadFile 下载文件到指定路径，同时计算 SHA256。
// 支持进度回调和超时控制。apiKey 非空时会携带 Authorization: Bearer 头。
func DownloadFile(ctx context.Context, url, destPath string, expectedSize int64, apiKey string, onProgress ProgressCallback) (*DownloadResult, error) {
	downloadCtx, cancel := context.WithTimeout(ctx, DefaultDownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("创建下载请求失败: %w", err)
	}
	req.Header.Set("User-Agent", buildUserAgent())
	if trimmed := strings.TrimSpace(apiKey); trimmed != "" {
		req.Header.Set("Authorization", "Bearer "+trimmed)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("下载返回 HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	// 确保目标目录存在
	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return nil, fmt.Errorf("创建下载目录失败: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), ".taiyiflow-cli-dl-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	hasher := sha256.New()
	multiWriter := io.MultiWriter(tmpFile, hasher)

	// 使用 io.Copy 从 resp.Body 复制到 multiWriter（同时写入文件和 hasher）
	var src io.Reader = resp.Body
	if onProgress != nil {
		total := expectedSize
		if total <= 0 && resp.ContentLength > 0 {
			total = resp.ContentLength
		}
		src = &progressReader{
			reader:   resp.Body,
			total:    total,
			callback: onProgress,
		}
	}

	_, err = io.Copy(multiWriter, src)

	if closeErr := tmpFile.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("下载失败: %w", err)
	}

	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))

	// 重命名临时文件到目标路径
	if err := os.Rename(tmpPath, destPath); err != nil {
		return nil, fmt.Errorf("移动下载文件失败: %w", err)
	}

	// 获取文件大小
	info, err := os.Stat(destPath)
	if err != nil {
		return nil, fmt.Errorf("获取文件信息失败: %w", err)
	}

	return &DownloadResult{
		Bytes:  info.Size(),
		SHA256: actualSHA256,
		Path:   destPath,
	}, nil
}

// VerifySHA256 校验本地文件的 SHA256。
func VerifySHA256(filePath, expectedSHA256 string) (bool, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return false, fmt.Errorf("打开文件失败: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, fmt.Errorf("计算 SHA256 失败: %w", err)
	}

	actual := hex.EncodeToString(h.Sum(nil))
	return strings.EqualFold(actual, expectedSHA256), nil
}

// progressReader 包装 Reader，在读取时回调进度。
type progressReader struct {
	reader   io.Reader
	total    int64
	read     int64
	callback ProgressCallback
	lastCall time.Time
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.reader.Read(p)
	if n > 0 {
		pr.read += int64(n)
		// 限制回调频率，最多每 200ms 一次
		now := time.Now()
		if now.Sub(pr.lastCall) > 200*time.Millisecond || err != nil {
			pr.callback(pr.read, pr.total)
			pr.lastCall = now
		}
	}
	return n, err
}
