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
	internalConfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/updater"
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
	var checkAPIOnly bool
	flagSet.BoolVar(&force, "force", false, "强制更新，即使当前版本与远程版本相同")
	flagSet.StringVar(&manifestURL, "manifest-url", "", "自定义 manifest.json 地址（默认从配置的后端地址获取）")
	flagSet.BoolVar(&checkAPIOnly, "check-api", true, "仅使用 check-update API，不回退到 manifest 流程")

	if err := flagSet.Parse(args); err != nil {
		return err
	}

	config, err := a.configManager.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	fmt.Fprintf(a.stdout, "正在检查更新...\n")
	fmt.Fprintf(a.stdout, "当前版本: %s (%s)\n", buildinfo.Version, buildinfo.Commit)

	// 优先尝试 check-update API（与 Electron 端对齐）
	// 如果 check-update API 不可用且未强制指定 --check-api，回退到 manifest 流程
	apiUpgraded := false
	if manifestURL == "" {
		apiUpgraded, err = a.runUpgradeViaAPI(ctx, config, force)
		if err != nil {
			// 检测鉴权失败（HTTP 401/403），即使 --check-api=true 也自动回退到 manifest 流程
			var authErr *updater.AuthError
			if errors.As(err, &authErr) {
				fmt.Fprintf(a.stderr, "check-update API 鉴权失败（HTTP %d），自动回退到 manifest 流程\n", authErr.StatusCode)
			} else if checkAPIOnly {
				return err
			} else {
				fmt.Fprintf(a.stderr, "check-update API 不可用，回退到 manifest 流程: %v\n", err)
			}
		} else if apiUpgraded {
			return nil
		} else {
			// API 返回无更新，但用户可能想用 manifest 流程
			if checkAPIOnly {
				fmt.Fprintf(a.stdout, "当前已是最新版本（check-update API）。\n")
				return nil
			}
			// 尝试 manifest 流程作为补充
			fmt.Fprintf(a.stdout, "check-update API 返回无更新，尝试 manifest 流程...\n")
		}
	}

	if apiUpgraded {
		return nil
	}

	// 回退到 manifest 流程
	return a.runUpgradeViaManifest(ctx, config, force, manifestURL)
}

// runUpgradeViaAPI 使用 check-update API 执行更新流程。
func (a *App) runUpgradeViaAPI(ctx context.Context, config internalConfig.Config, force bool) (bool, error) {
	baseURL := strings.TrimRight(config.BackendBaseURL, "/")
	if baseURL == "" {
		return false, errors.New("未配置后端地址")
	}

	apiPrefix := config.BackendAPIPrefix
	if apiPrefix == "" {
		apiPrefix = internalConfig.DefaultBackendAPIPrefix
	}

	orch := updater.NewOrchestrator(baseURL, apiPrefix, config.AccessToken)

	// 检查更新并下载
	checkResp, dlResult, err := orch.CheckAndDownload(ctx, config.DeviceID, force, func(downloaded, total int64) {
		if total > 0 {
			fmt.Fprintf(a.stdout, "\r下载进度: %s / %s (%.1f%%)",
				formatUpgradeSize(downloaded), formatUpgradeSize(total),
				float64(downloaded)/float64(total)*100)
		} else {
			fmt.Fprintf(a.stdout, "\r下载进度: %s", formatUpgradeSize(downloaded))
		}
	})
	if err != nil {
		return false, err
	}
	fmt.Fprintln(a.stdout) // 进度行换行

	if !checkResp.HasUpdate {
		return false, nil
	}

	if dlResult == nil {
		return false, errors.New("下载结果为空")
	}

	fmt.Fprintf(a.stdout, "远程版本: %s\n", checkResp.Version)
	fmt.Fprintf(a.stdout, "已下载: %s (SHA256: %s)\n", formatUpgradeSize(dlResult.Bytes), dlResult.SHA256)

	// 执行二进制替换
	fmt.Fprintf(a.stdout, "正在安装...\n")
	if err := orch.InstallBinary(ctx, dlResult.Path, checkResp.DeliveryID, config.DeviceID); err != nil {
		return false, err
	}

	fmt.Fprintf(a.stdout, "更新完成！已从 %s 更新到 %s\n", buildinfo.Version, checkResp.Version)
	return true, nil
}

// runUpgradeViaManifest 使用 manifest.json 流程执行更新（旧流程，作为回退）。
func (a *App) runUpgradeViaManifest(ctx context.Context, config internalConfig.Config, force bool, manifestURL string) error {

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

	// 以 manifest.json 所在目录作为 bundle_base_url
	manifestDir := resolvedManifestURL
	if idx := strings.LastIndex(manifestDir, "/"); idx >= 0 {
		manifestDir = manifestDir[:idx]
	}

	// 确定 checksums 文件 URL
	// - manifest.Checksums 有值时：按相对路径/绝对路径/URL 规则解析
	// - manifest.Checksums 为空时：回退到默认路径，基于后端根路径拼接
	baseURL := strings.TrimRight(config.BackendBaseURL, "/")
	var checksumsURL string
	if checksumsRelPath := strings.TrimSpace(manifest.Checksums); checksumsRelPath != "" {
		checksumsURL = resolveBundleURL(manifestDir, checksumsRelPath)
	} else {
		checksumsURL = baseURL + "/" + upgradeChecksumsPath
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

	// 构造下载 URL：以 manifest.json 所在目录为起点，按相对路径/绝对路径/URL 规则解析
	downloadURL := resolveBundleURL(manifestDir, artifact.Path)

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

// verifyStartupUpdate 启动时闭环验证：检查上次更新是否完成。
// 如果当前版本 == 目标版本，说明更新已成功，清除状态。
// 如果当前版本 != 目标版本但存在目标版本记录，说明更新可能未完成，输出警告。
func (a *App) verifyStartupUpdate() {
	orch := updater.NewOrchestrator("", "", "")
	matched, targetVersion, err := orch.VerifyStartup()
	if err != nil {
		// 静默失败，不影响正常启动
		return
	}
	if targetVersion == "" {
		return
	}
	if matched {
		fmt.Fprintf(a.stderr, "更新成功：已升级到版本 %s\n", targetVersion)
	} else {
		fmt.Fprintf(a.stderr, "警告: 上次更新到 %s 可能未完成（当前版本: %s）\n", targetVersion, buildinfo.Version)
	}
}

// resolveBundleURL 根据 manifest.json 所在目录（bundleBaseURL）解析路径。
//   - 如果 path 是完整 URL（http:// 或 https://），直接返回。
//   - 如果 path 是绝对路径（以 / 开头），使用 bundleBaseURL 的根路径（scheme://host）拼接。
//   - 如果 path 是相对路径，使用 bundleBaseURL + "/" + path 拼接。
func resolveBundleURL(bundleBaseURL, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	// 完整 URL，直接使用
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	// 绝对路径：提取 bundleBaseURL 的根路径（scheme://host）拼接
	if strings.HasPrefix(path, "/") {
		return bundleRootFromURL(bundleBaseURL) + path
	}
	// 相对路径：基于 manifest 所在目录拼接
	return bundleBaseURL + "/" + path
}

// bundleRootFromURL 从 URL 中提取根路径（scheme://host）。
// 例如 "https://example.com/taiyi/bundles" → "https://example.com"
func bundleRootFromURL(rawURL string) string {
	if idx := strings.Index(rawURL, "://"); idx >= 0 {
		rest := rawURL[idx+3:]
		if slashIdx := strings.Index(rest, "/"); slashIdx >= 0 {
			return rawURL[:idx+3+slashIdx]
		}
		return rawURL
	}
	return rawURL
}
