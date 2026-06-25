package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/buildinfo"
)

const (
	upgradeManifestPath    = "/taiyi/bundles/manifest.json"
	upgradeChecksumsPath   = "downloads/SHA256SUMS"
	upgradeArtifactGroup   = "taiyiflow-cli"
	upgradeHTTPTimeout     = 30 * time.Second
	upgradeDownloadTimeout = 10 * time.Minute
)

type upgradeManifest struct {
	Project     string            `json:"project"`
	GeneratedAt string            `json:"generated_at"`
	Artifacts   []upgradeArtifact `json:"artifacts"`
	Checksums   string            `json:"checksums"`
}

type upgradeArtifact struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	Group      string `json:"group"`
	GroupTitle string `json:"group_title"`
}

func (a *App) runUpgrade(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)

	var force bool
	var manifestURL string
	flagSet.BoolVar(&force, "force", false, "强制更新，即使当前版本与远程版本相同")
	flagSet.StringVar(&manifestURL, "manifest-url", "", "自定义 manifest.json 地址（默认从配置的后端地址获取）")

	if err := flagSet.Parse(args); err != nil {
		return err
	}

	config, err := a.configManager.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	// 确定 manifest URL
	resolvedManifestURL := strings.TrimSpace(manifestURL)
	if resolvedManifestURL == "" {
		baseURL := strings.TrimRight(config.BackendBaseURL, "/")
		if baseURL == "" {
			return errors.New("未配置后端地址，请先执行 `taiyiflow-cli config init` 或使用 --manifest-url 指定")
		}
		resolvedManifestURL = baseURL + upgradeManifestPath
	}

	fmt.Fprintf(a.stdout, "正在检查更新...\n")
	fmt.Fprintf(a.stdout, "当前版本: %s (%s)\n", buildinfo.Version, buildinfo.Commit)

	// 下载 manifest
	manifest, err := fetchUpgradeManifest(ctx, resolvedManifestURL)
	if err != nil {
		return fmt.Errorf("获取版本信息失败: %w", err)
	}

	// 查找匹配当前平台的 artifact
	artifact, err := findUpgradeArtifact(manifest, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "远程版本: %s\n", manifest.GeneratedAt)
	fmt.Fprintf(a.stdout, "目标文件: %s (%s)\n", artifact.Name, formatUpgradeSize(artifact.SizeBytes))

	// 确定 checksums 文件 URL
	// - manifest.Checksums 有值时：视为相对路径，基于 manifest 所在目录拼接
	// - manifest.Checksums 为空时：回退到默认绝对路径，基于 baseURL（scheme+host）拼接
	baseURL := strings.TrimRight(config.BackendBaseURL, "/")
	var checksumsURL string
	if checksumsRelPath := strings.TrimSpace(manifest.Checksums); checksumsRelPath != "" {
		manifestDir := resolvedManifestURL
		if idx := strings.LastIndex(manifestDir, "/"); idx >= 0 {
			manifestDir = manifestDir[:idx]
		}
		checksumsURL = manifestDir + "/" + strings.TrimLeft(checksumsRelPath, "/")
	} else {
		checksumsURL = baseURL + upgradeChecksumsPath
	}

	// 下载并解析远程 checksums 文件
	remoteChecksums, err := fetchRemoteChecksums(ctx, checksumsURL)
	if err != nil {
		return fmt.Errorf("获取校验文件失败: %w", err)
	}

	// 从 checksums 中查找当前平台对应的 SHA256
	artifactFileName := buildUpgradeArtifactName(runtime.GOOS, runtime.GOARCH)
	remoteSHA256, err := findChecksumForArtifact(remoteChecksums, artifactFileName)
	if err != nil {
		return fmt.Errorf("在校验文件中未找到当前平台的校验和: %w", err)
	}

	// 获取当前可执行文件路径
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取当前可执行文件路径失败: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("解析可执行文件路径失败: %w", err)
	}

	// 版本比较：计算本地可执行文件的 SHA256，与远程 checksums 比较
	if !force {
		localSHA256, err := computeLocalSHA256(execPath)
		if err != nil {
			fmt.Fprintf(a.stderr, "警告: 计算本地文件校验和失败: %v，将继续更新\n", err)
		} else if strings.EqualFold(localSHA256, remoteSHA256) {
			fmt.Fprintf(a.stdout, "当前已是最新版本，无需更新。\n")
			fmt.Fprintf(a.stdout, "本地 SHA256: %s\n", localSHA256)
			return nil
		} else {
			fmt.Fprintf(a.stdout, "本地 SHA256: %s\n", localSHA256)
			fmt.Fprintf(a.stdout, "远程 SHA256: %s\n", remoteSHA256)
		}
	}

	// 构造下载 URL（复用上方已声明的 baseURL）
	downloadURL := baseURL + "/" + strings.TrimLeft(artifact.Path, "/")
	if strings.TrimSpace(manifestURL) != "" {
		// 如果使用了自定义 manifest URL，基于 manifest 所在目录推算下载路径
		manifestDir := strings.TrimRight(resolvedManifestURL, "/")
		lastSlash := strings.LastIndex(manifestDir, "/")
		if lastSlash >= 0 {
			manifestDir = manifestDir[:lastSlash]
		}
		downloadURL = manifestDir + "/" + strings.TrimLeft(artifact.Path, "/")
	}

	fmt.Fprintf(a.stdout, "正在下载: %s\n", downloadURL)

	// 下载到临时文件（与目标可执行文件同目录，确保 rename 原子性）
	execDir := filepath.Dir(execPath)
	tmpFile, err := os.CreateTemp(execDir, ".taiyiflow-cli-upgrade-*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		// 清理临时文件（如果还在的话）
		_ = os.Remove(tmpPath)
	}()

	// 下载文件并计算 SHA256
	downloadCtx, downloadCancel := context.WithTimeout(ctx, upgradeDownloadTimeout)
	defer downloadCancel()

	hasher := sha256.New()
	downloadedBytes, err := downloadUpgradeBinary(downloadCtx, downloadURL, tmpFile, hasher)
	if closeErr := tmpFile.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}

	// 校验 SHA256（使用远程 checksums 文件中的值）
	actualSHA256 := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualSHA256, remoteSHA256) {
		return fmt.Errorf("校验和不匹配: 期望 %s，实际 %s", remoteSHA256, actualSHA256)
	}
	fmt.Fprintf(a.stdout, "校验通过: SHA256 %s\n", actualSHA256)
	fmt.Fprintf(a.stdout, "已下载: %s\n", formatUpgradeSize(downloadedBytes))

	// 设置可执行权限（Unix 系统）
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			return fmt.Errorf("设置可执行权限失败: %w", err)
		}
	}

	// Windows 上不能直接替换正在运行的 exe，使用 rename-then-notify 策略
	if runtime.GOOS == "windows" {
		// 先把旧文件 rename 走，再 rename 新文件过来
		backupPath := execPath + ".old"
		_ = os.Remove(backupPath)
		if err := os.Rename(execPath, backupPath); err != nil {
			return fmt.Errorf("备份旧版本失败: %w", err)
		}
		if err := os.Rename(tmpPath, execPath); err != nil {
			// 回滚
			_ = os.Rename(backupPath, execPath)
			return fmt.Errorf("替换可执行文件失败: %w", err)
		}
		_ = os.Remove(backupPath)
	} else {
		// Unix: rename 是原子操作，直接替换
		if err := os.Rename(tmpPath, execPath); err != nil {
			return fmt.Errorf("替换可执行文件失败: %w", err)
		}
	}

	fmt.Fprintf(a.stdout, "更新完成！已从 %s 更新到 %s\n", buildinfo.Version, manifest.GeneratedAt)
	return nil
}

func fetchUpgradeManifest(ctx context.Context, url string) (*upgradeManifest, error) {
	reqCtx, cancel := context.WithTimeout(ctx, upgradeHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildUpgradeUserAgent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	var manifest upgradeManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("解析 manifest 失败: %w", err)
	}
	return &manifest, nil
}

func findUpgradeArtifact(manifest *upgradeManifest, goos, goarch string) (*upgradeArtifact, error) {
	expectedName := buildUpgradeArtifactName(goos, goarch)
	for i := range manifest.Artifacts {
		artifact := &manifest.Artifacts[i]
		if artifact.Group != upgradeArtifactGroup {
			continue
		}
		// 匹配文件名：name 字段格式为 "taiyiflow-cli/taiyiflow-cli-darwin-arm64"
		artifactFileName := artifact.Name
		if idx := strings.LastIndex(artifactFileName, "/"); idx >= 0 {
			artifactFileName = artifactFileName[idx+1:]
		}
		if artifactFileName == expectedName {
			return artifact, nil
		}
	}
	return nil, fmt.Errorf("未找到适用于 %s/%s 的更新包（期望: %s）", goos, goarch, expectedName)
}

func buildUpgradeArtifactName(goos, goarch string) string {
	name := "taiyiflow-cli-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

func downloadUpgradeBinary(ctx context.Context, url string, writer io.Writer, hasher io.Writer) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", buildUpgradeUserAgent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	multiWriter := io.MultiWriter(writer, hasher)
	return io.Copy(multiWriter, resp.Body)
}

func buildUpgradeUserAgent() string {
	return fmt.Sprintf("taiyiflow-cli/%s (%s/%s)", buildinfo.Version, runtime.GOOS, runtime.GOARCH)
}

func formatUpgradeSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
	)
	switch {
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// fetchRemoteChecksums 下载并解析远程 checksums 文件。
// 文件格式为标准的 sha256sum 输出格式：
//
//	<sha256hex>  <path/to/file>
func fetchRemoteChecksums(ctx context.Context, url string) (map[string]string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, upgradeHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", buildUpgradeUserAgent())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 限制 1MB
	if err != nil {
		return nil, fmt.Errorf("读取校验文件失败: %w", err)
	}

	return parseChecksumsContent(string(body))
}

// parseChecksumsContent 解析 sha256sum 格式的校验文件内容。
// 返回 map[文件名]SHA256，例如 map["taiyiflow-cli-darwin-arm64"] = "abc123..."
func parseChecksumsContent(content string) (map[string]string, error) {
	result := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 格式: <sha256hex>  <path/to/file>（两个空格或一个空格分隔）
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		sha256hex := parts[0]
		filePath := parts[len(parts)-1]
		// 取文件名部分（去掉目录前缀）
		fileName := filePath
		if idx := strings.LastIndex(fileName, "/"); idx >= 0 {
			fileName = fileName[idx+1:]
		}
		result[fileName] = sha256hex
	}
	if len(result) == 0 {
		return nil, errors.New("校验文件为空或格式不正确")
	}
	return result, nil
}

// findChecksumForArtifact 从解析后的 checksums map 中查找指定文件名对应的 SHA256。
func findChecksumForArtifact(checksums map[string]string, artifactFileName string) (string, error) {
	sha256hex, ok := checksums[artifactFileName]
	if !ok {
		return "", fmt.Errorf("未找到 %s 的校验和（可用: %v）", artifactFileName, checksumKeys(checksums))
	}
	return sha256hex, nil
}

// checksumKeys 返回 map 的所有键，用于错误提示。
func checksumKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// computeLocalSHA256 计算本地可执行文件的 SHA256 校验和。
func computeLocalSHA256(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
