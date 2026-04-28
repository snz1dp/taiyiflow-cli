package skills

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

const (
	defaultRegistryTimeout = 30 * time.Second
	remoteOriginRelPath    = ".snz1dp/origin.json"
)

var remoteSkillSlugPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)

type RemoteSearchResult struct {
	Slug        string
	DisplayName string
	Summary     string
	Popularity  float64
	Downloads   int64
	Version     string
	UpdatedAt   int64
}

type RemoteSkillDetail struct {
	Slug          string
	DisplayName   string
	Summary       string
	Version       string
	UpdatedAt     int64
	OwnerHandle   string
	OwnerName     string
	SupportedOS   []string
	SupportedEnvs []string
	RegistryURL   string
}

type RemoteInstallResult struct {
	Name        string
	Slug        string
	Version     string
	TargetDir   string
	RegistryURL string
}

type RemoteRemoveResult struct {
	Name      string
	Slug      string
	TargetDir string
}

type RemoteUpdateResult struct {
	Name            string
	Slug            string
	PreviousVersion string
	Version         string
	TargetDir       string
	Changed         bool
	RegistryURL     string
}

type InstalledRemoteSkill struct {
	Name             string
	Slug             string
	InstalledVersion string
	TargetDir        string
	RegistryURL      string
}

type remoteSearchResponse struct {
	Results []struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"displayName"`
		Summary     string `json:"summary"`
		Popularity  float64 `json:"popularity"`
		Downloads   int64   `json:"downloads"`
		Version     string `json:"version"`
		UpdatedAt   int64  `json:"updatedAt"`
	} `json:"results"`
}

type remoteDetailResponse struct {
	Skill *struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"displayName"`
		Summary     string `json:"summary"`
		UpdatedAt   int64  `json:"updatedAt"`
	} `json:"skill"`
	LatestVersion *struct {
		Version string `json:"version"`
	} `json:"latestVersion"`
	Metadata *struct {
		OS      []string `json:"os"`
		Systems []string `json:"systems"`
	} `json:"metadata"`
	Owner *struct {
		Handle      string `json:"handle"`
		DisplayName string `json:"displayName"`
	} `json:"owner"`
}

type remoteOrigin struct {
	Version          int    `json:"version"`
	Registry         string `json:"registry"`
	Slug             string `json:"slug"`
	InstalledVersion string `json:"installed_version"`
	InstalledAt      int64  `json:"installed_at"`
}

func SearchRemote(config internalconfig.Config, query string, limit int) ([]RemoteSearchResult, string, error) {
	registryURL := resolveRegistryURL(config)
	searchQuery := strings.TrimSpace(query)
	if searchQuery == "" {
		searchQuery = "*"
	}
	values := url.Values{}
	values.Set("q", searchQuery)
	if limit > 0 {
		values.Set("limit", fmt.Sprintf("%d", limit))
	}
	body, err := registryRequest(registryURL, "/api/v1/search", values)
	if err != nil {
		return nil, registryURL, err
	}
	var response remoteSearchResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, registryURL, fmt.Errorf("解析技能仓库搜索结果失败: %w", err)
	}
	results := make([]RemoteSearchResult, 0, len(response.Results))
	for _, item := range response.Results {
		results = append(results, RemoteSearchResult{
			Slug:        strings.TrimSpace(item.Slug),
			DisplayName: strings.TrimSpace(item.DisplayName),
			Summary:     normalizeRemoteSkillSummary(item.Summary),
			Popularity:  item.Popularity,
			Downloads:   item.Downloads,
			Version:     strings.TrimSpace(item.Version),
			UpdatedAt:   item.UpdatedAt,
		})
	}
	return results, registryURL, nil
}

func FetchRemoteDetail(config internalconfig.Config, slug string) (RemoteSkillDetail, string, error) {
	registryURL := resolveRegistryURL(config)
	normalizedSlug, err := normalizeRemoteSkillSlug(slug)
	if err != nil {
		return RemoteSkillDetail{}, registryURL, err
	}
	body, err := registryRequest(registryURL, "/api/v1/skills/"+url.PathEscape(normalizedSlug), nil)
	if err != nil {
		return RemoteSkillDetail{}, registryURL, err
	}
	var response remoteDetailResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return RemoteSkillDetail{}, registryURL, fmt.Errorf("解析技能仓库详情失败: %w", err)
	}
	if response.Skill == nil {
		return RemoteSkillDetail{}, registryURL, fmt.Errorf("远程技能不存在: %s", normalizedSlug)
	}
	detail := RemoteSkillDetail{
		Slug:        strings.TrimSpace(response.Skill.Slug),
		DisplayName: strings.TrimSpace(response.Skill.DisplayName),
		Summary:     normalizeRemoteSkillSummary(response.Skill.Summary),
		UpdatedAt:   response.Skill.UpdatedAt,
		RegistryURL: registryURL,
	}
	if response.LatestVersion != nil {
		detail.Version = strings.TrimSpace(response.LatestVersion.Version)
	}
	if response.Owner != nil {
		detail.OwnerHandle = strings.TrimSpace(response.Owner.Handle)
		detail.OwnerName = strings.TrimSpace(response.Owner.DisplayName)
	}
	if response.Metadata != nil {
		detail.SupportedOS = compactStrings(response.Metadata.OS)
		detail.SupportedEnvs = compactStrings(response.Metadata.Systems)
	}
	return detail, registryURL, nil
}

func InstallRemote(config internalconfig.Config, slug string, version string, force bool) (RemoteInstallResult, error) {
	registryURL := resolveRegistryURL(config)
	normalizedSlug, err := normalizeRemoteSkillSlug(slug)
	if err != nil {
		return RemoteInstallResult{}, err
	}
	detail, _, err := FetchRemoteDetail(config, normalizedSlug)
	if err != nil {
		return RemoteInstallResult{}, err
	}
	resolvedVersion := strings.TrimSpace(version)
	if resolvedVersion == "" {
		resolvedVersion = detail.Version
	}
	if resolvedVersion == "" {
		return RemoteInstallResult{}, fmt.Errorf("远程技能没有可安装版本: %s", normalizedSlug)
	}
	archiveBytes, err := downloadRemoteArchive(registryURL, normalizedSlug, resolvedVersion)
	if err != nil {
		return RemoteInstallResult{}, err
	}
	targetDir, err := resolveRemoteInstallTarget(config, normalizedSlug)
	if err != nil {
		return RemoteInstallResult{}, err
	}
	installedName, err := installArchiveToTarget(archiveBytes, targetDir, registryURL, normalizedSlug, resolvedVersion, force)
	if err != nil {
		return RemoteInstallResult{}, err
	}
	if installedName == "" {
		installedName = normalizedSlug
	}
	return RemoteInstallResult{
		Name:        installedName,
		Slug:        normalizedSlug,
		Version:     resolvedVersion,
		TargetDir:   targetDir,
		RegistryURL: registryURL,
	}, nil
}

func RemoveInstalled(config internalconfig.Config, nameOrSlug string) (RemoteRemoveResult, error) {
	installed, err := resolveInstalledRemoteSkill(config, nameOrSlug)
	if err != nil {
		return RemoteRemoveResult{}, err
	}
	if err := os.RemoveAll(installed.TargetDir); err != nil {
		return RemoteRemoveResult{}, fmt.Errorf("删除技能目录失败: %w", err)
	}
	return RemoteRemoveResult{
		Name:      installed.Name,
		Slug:      installed.Slug,
		TargetDir: installed.TargetDir,
	}, nil
}

func UpdateRemote(config internalconfig.Config, nameOrSlug string) (RemoteUpdateResult, error) {
	installed, err := resolveInstalledRemoteSkill(config, nameOrSlug)
	if err != nil {
		return RemoteUpdateResult{}, err
	}
	detail, _, err := FetchRemoteDetail(config, installed.Slug)
	if err != nil {
		return RemoteUpdateResult{}, err
	}
	if detail.Version == "" {
		return RemoteUpdateResult{}, fmt.Errorf("远程技能没有可更新版本: %s", installed.Slug)
	}
	if strings.EqualFold(strings.TrimSpace(installed.InstalledVersion), strings.TrimSpace(detail.Version)) {
		repaired, err := repairInstalledSkillDependencies(installed.TargetDir)
		if err != nil {
			return RemoteUpdateResult{}, err
		}
		return RemoteUpdateResult{
			Name:            installed.Name,
			Slug:            installed.Slug,
			PreviousVersion: installed.InstalledVersion,
			Version:         detail.Version,
			TargetDir:       installed.TargetDir,
			Changed:         repaired,
			RegistryURL:     installed.RegistryURL,
		}, nil
	}
	archiveBytes, err := downloadRemoteArchive(installed.RegistryURL, installed.Slug, detail.Version)
	if err != nil {
		return RemoteUpdateResult{}, err
	}
	installedName, err := installArchiveToTarget(archiveBytes, installed.TargetDir, installed.RegistryURL, installed.Slug, detail.Version, true)
	if err != nil {
		return RemoteUpdateResult{}, err
	}
	if installedName == "" {
		installedName = installed.Name
	}
	return RemoteUpdateResult{
		Name:            installedName,
		Slug:            installed.Slug,
		PreviousVersion: installed.InstalledVersion,
		Version:         detail.Version,
		TargetDir:       installed.TargetDir,
		Changed:         true,
		RegistryURL:     installed.RegistryURL,
	}, nil
}

func UpdateAllRemote(config internalconfig.Config) ([]RemoteUpdateResult, error) {
	items, err := listInstalledRemoteSkills(config)
	if err != nil {
		return nil, err
	}
	results := make([]RemoteUpdateResult, 0, len(items))
	for _, item := range items {
		result, err := UpdateRemote(config, item.Slug)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func ListInstalledRemoteSkills(config internalconfig.Config) ([]InstalledRemoteSkill, error) {
	return listInstalledRemoteSkills(config)
}

func resolveRegistryURL(config internalconfig.Config) string {
	base := strings.TrimSpace(config.Skills.RegistryURL)
	if base == "" {
		base = internalconfig.Default().Skills.RegistryURL
	}
	return strings.TrimRight(base, "/")
}

func normalizeRemoteSkillSlug(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("技能 slug 不能为空")
	}
	if !remoteSkillSlugPattern.MatchString(trimmed) {
		return "", fmt.Errorf("无效的技能 slug: %s", value)
	}
	return trimmed, nil
}

func normalizeRemoteSkillSummary(value string) string {
	trimmed := strings.TrimSpace(value)
	for {
		next := strings.TrimSpace(trimmed)
		next = strings.TrimPrefix(next, `\r\n`)
		next = strings.TrimPrefix(next, `\n`)
		next = strings.TrimPrefix(next, `\r`)
		next = strings.TrimSuffix(next, `\r\n`)
		next = strings.TrimSuffix(next, `\n`)
		next = strings.TrimSuffix(next, `\r`)
		next = strings.TrimSpace(next)
		if next == trimmed {
			return next
		}
		trimmed = next
	}
}

func registryRequest(registryURL string, requestPath string, query url.Values) ([]byte, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(registryURL), "/") + "/")
	if err != nil {
		return nil, fmt.Errorf("解析技能仓库地址失败: %w", err)
	}
	relative, err := url.Parse(strings.TrimLeft(requestPath, "/"))
	if err != nil {
		return nil, fmt.Errorf("解析技能仓库路径失败: %w", err)
	}
	endpoint := base.ResolveReference(relative)
	if len(query) > 0 {
		endpoint.RawQuery = query.Encode()
	}
	request, err := http.NewRequest(http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("构建技能仓库请求失败: %w", err)
	}
	request.Header.Set("Accept", "application/json, application/octet-stream")
	request.Header.Set("User-Agent", "taiyiflow-cli/skills")
	response, err := (&http.Client{Timeout: defaultRegistryTimeout}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("访问技能仓库失败: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("读取技能仓库响应失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = response.Status
		}
		return nil, fmt.Errorf("技能仓库请求失败(%d): %s", response.StatusCode, message)
	}
	return body, nil
}

func downloadRemoteArchive(registryURL string, slug string, version string) ([]byte, error) {
	query := url.Values{}
	query.Set("slug", slug)
	if strings.TrimSpace(version) != "" {
		query.Set("version", strings.TrimSpace(version))
	}
	body, err := registryRequest(registryURL, "/api/v1/download", query)
	if err != nil {
		return nil, fmt.Errorf("下载远程技能失败: %w", err)
	}
	return body, nil
}

func resolveRemoteInstallTarget(config internalconfig.Config, slug string) (string, error) {
	rootDir, err := ResolveInstallDirectory(config)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(rootDir) == "" {
		return "", fmt.Errorf("未找到可写入的技能目录")
	}
	return filepath.Join(rootDir, slug), nil
}

func installArchiveToTarget(archiveBytes []byte, targetDir string, registryURL string, slug string, version string, force bool) (string, error) {
	parentDir := filepath.Dir(targetDir)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return "", fmt.Errorf("创建技能目录失败: %w", err)
	}
	info, err := os.Stat(targetDir)
	if err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("技能目标路径不是目录: %s", targetDir)
		}
		if !force {
			return "", fmt.Errorf("技能已存在: %s", filepath.Base(targetDir))
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("读取技能目录失败: %w", err)
	}
	stageDir, err := os.MkdirTemp(parentDir, filepath.Base(targetDir)+".tmp-")
	if err != nil {
		return "", fmt.Errorf("创建技能暂存目录失败: %w", err)
	}
	defer os.RemoveAll(stageDir)
	extractDir, err := os.MkdirTemp("", "taiyiflow-skill-archive-")
	if err != nil {
		return "", fmt.Errorf("创建技能解压目录失败: %w", err)
	}
	defer os.RemoveAll(extractDir)
	if err := extractSkillArchive(archiveBytes, extractDir); err != nil {
		return "", err
	}
	skillRoot, err := findSkillRoot(extractDir)
	if err != nil {
		return "", err
	}
	if err := copyDirectoryContents(skillRoot, stageDir); err != nil {
		return "", err
	}
	skillFilePath := filepath.Join(stageDir, skillFileName)
	skillInfo, err := LoadFile(stageDir, skillFilePath)
	if err != nil {
		return "", fmt.Errorf("校验安装后的技能失败: %w", err)
	}
	if err := writeRemoteOrigin(stageDir, remoteOrigin{
		Version:          1,
		Registry:         registryURL,
		Slug:             slug,
		InstalledVersion: version,
		InstalledAt:      time.Now().Unix(),
	}); err != nil {
		return "", err
	}
	if err := installSkillDependencies(stageDir); err != nil {
		return "", err
	}
	if force {
		if err := os.RemoveAll(targetDir); err != nil {
			return "", fmt.Errorf("覆盖现有技能失败: %w", err)
		}
	}
	if err := os.Rename(stageDir, targetDir); err != nil {
		return "", fmt.Errorf("移动技能到目标目录失败: %w", err)
	}
	return strings.TrimSpace(skillInfo.Name), nil
}

func installSkillDependencies(skillDir string) error {
	packageJSONPath := filepath.Join(skillDir, "package.json")
	info, err := os.Stat(packageJSONPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取技能依赖描述失败: %w", err)
	}
	if info.IsDir() {
		return nil
	}
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("技能包含 package.json，但当前环境未找到 npm: %w", err)
	}
	args := []string{"install", "--omit=dev"}
	packageLockPath := filepath.Join(skillDir, "package-lock.json")
	if _, err := os.Stat(packageLockPath); err == nil {
		args = []string{"ci", "--omit=dev"}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("读取技能锁文件失败: %w", err)
	}
	command := exec.Command(npmPath, args...)
	command.Dir = skillDir
	output, err := command.CombinedOutput()
	if err != nil {
		trimmedOutput := strings.TrimSpace(string(output))
		if trimmedOutput == "" {
			return fmt.Errorf("安装技能依赖失败: %w", err)
		}
		return fmt.Errorf("安装技能依赖失败: %w\n%s", err, trimmedOutput)
	}
	return nil
}

func repairInstalledSkillDependencies(skillDir string) (bool, error) {
	packageJSONPath := filepath.Join(skillDir, "package.json")
	if _, err := os.Stat(packageJSONPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("读取技能依赖描述失败: %w", err)
	}
	nodeModulesPath := filepath.Join(skillDir, "node_modules")
	if _, err := os.Stat(nodeModulesPath); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("读取技能依赖目录失败: %w", err)
	}
	if err := installSkillDependencies(skillDir); err != nil {
		return false, err
	}
	return true, nil
}

func extractSkillArchive(archiveBytes []byte, destDir string) error {
	reader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return fmt.Errorf("解析技能归档失败: %w", err)
	}
	for _, file := range reader.File {
		cleanName := filepath.Clean(file.Name)
		if cleanName == "." || strings.HasPrefix(cleanName, "..") {
			return fmt.Errorf("技能归档包含非法路径: %s", file.Name)
		}
		targetPath := filepath.Join(destDir, cleanName)
		relPath, err := filepath.Rel(destDir, targetPath)
		if err != nil || strings.HasPrefix(relPath, "..") {
			return fmt.Errorf("技能归档包含越界路径: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return fmt.Errorf("创建归档目录失败: %w", err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return fmt.Errorf("创建归档目录失败: %w", err)
		}
		readerHandle, err := file.Open()
		if err != nil {
			return fmt.Errorf("读取归档文件失败: %w", err)
		}
		mode := file.Mode()
		if mode == 0 {
			mode = 0o644
		}
		writerHandle, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			readerHandle.Close()
			return fmt.Errorf("创建归档文件失败: %w", err)
		}
		if _, err := io.Copy(writerHandle, readerHandle); err != nil {
			writerHandle.Close()
			readerHandle.Close()
			return fmt.Errorf("写入归档文件失败: %w", err)
		}
		writerHandle.Close()
		readerHandle.Close()
	}
	return nil
}

func findSkillRoot(rootDir string) (string, error) {
	candidates := []string{}
	err := filepath.WalkDir(rootDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() != skillFileName {
			return nil
		}
		candidates = append(candidates, filepath.Dir(path))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("扫描解压技能失败: %w", err)
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("归档中未找到 %s", skillFileName)
	}
	sort.Slice(candidates, func(left int, right int) bool {
		leftDepth := strings.Count(filepath.Clean(candidates[left]), string(filepath.Separator))
		rightDepth := strings.Count(filepath.Clean(candidates[right]), string(filepath.Separator))
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return candidates[left] < candidates[right]
	})
	return candidates[0], nil
}

func copyDirectoryContents(sourceDir string, targetDir string) error {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("读取技能内容失败: %w", err)
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(sourceDir, entry.Name())
		targetPath := filepath.Join(targetDir, entry.Name())
		if entry.IsDir() {
			if err := copyDirectory(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func copyDirectory(sourceDir string, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("创建技能目录失败: %w", err)
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("读取技能目录失败: %w", err)
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(sourceDir, entry.Name())
		targetPath := filepath.Join(targetDir, entry.Name())
		if entry.IsDir() {
			if err := copyDirectory(sourcePath, targetPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(sourcePath, targetPath); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(sourcePath string, targetPath string) error {
	stat, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("读取技能文件失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("创建技能目录失败: %w", err)
	}
	readerHandle, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("打开技能文件失败: %w", err)
	}
	defer readerHandle.Close()
	writerHandle, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, stat.Mode())
	if err != nil {
		return fmt.Errorf("创建技能文件失败: %w", err)
	}
	defer writerHandle.Close()
	if _, err := io.Copy(writerHandle, readerHandle); err != nil {
		return fmt.Errorf("写入技能文件失败: %w", err)
	}
	return nil
}

func writeRemoteOrigin(skillDir string, origin remoteOrigin) error {
	metadataPath := filepath.Join(skillDir, remoteOriginRelPath)
	if err := os.MkdirAll(filepath.Dir(metadataPath), 0o755); err != nil {
		return fmt.Errorf("创建技能来源目录失败: %w", err)
	}
	body, err := json.MarshalIndent(origin, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化技能来源失败: %w", err)
	}
	if err := os.WriteFile(metadataPath, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入技能来源失败: %w", err)
	}
	return nil
}

func readRemoteOrigin(skillDir string) (*remoteOrigin, error) {
	metadataPath := filepath.Join(skillDir, remoteOriginRelPath)
	body, err := os.ReadFile(metadataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取技能来源失败: %w", err)
	}
	var origin remoteOrigin
	if err := json.Unmarshal(body, &origin); err != nil {
		return nil, fmt.Errorf("解析技能来源失败: %w", err)
	}
	if origin.Version != 1 || strings.TrimSpace(origin.Slug) == "" {
		return nil, nil
	}
	return &origin, nil
}

func resolveInstalledRemoteSkill(config internalconfig.Config, nameOrSlug string) (InstalledRemoteSkill, error) {
	target := strings.TrimSpace(nameOrSlug)
	if target == "" {
		return InstalledRemoteSkill{}, fmt.Errorf("技能名称不能为空")
	}
	items, err := listInstalledRemoteSkills(config)
	if err != nil {
		return InstalledRemoteSkill{}, err
	}
	for _, item := range items {
		if strings.EqualFold(item.Name, target) || strings.EqualFold(item.Slug, target) {
			return item, nil
		}
	}
	return InstalledRemoteSkill{}, fmt.Errorf("未找到已安装的远程技能: %s", target)
}

func listInstalledRemoteSkills(config internalconfig.Config) ([]InstalledRemoteSkill, error) {
	items, _, err := Discover(config)
	if err != nil {
		return nil, err
	}
	results := []InstalledRemoteSkill{}
	seen := map[string]bool{}
	for _, item := range items {
		skillDir := filepath.Dir(item.Path)
		origin, err := readRemoteOrigin(skillDir)
		if err != nil {
			return nil, err
		}
		if origin == nil {
			continue
		}
		key := filepath.Clean(skillDir)
		if seen[key] {
			continue
		}
		seen[key] = true
		results = append(results, InstalledRemoteSkill{
			Name:             strings.TrimSpace(item.Name),
			Slug:             strings.TrimSpace(origin.Slug),
			InstalledVersion: strings.TrimSpace(origin.InstalledVersion),
			TargetDir:        skillDir,
			RegistryURL:      strings.TrimSpace(origin.Registry),
		})
	}
	sort.Slice(results, func(left int, right int) bool {
		leftName := strings.ToLower(results[left].Name)
		rightName := strings.ToLower(results[right].Name)
		if !strings.EqualFold(leftName, rightName) {
			return leftName < rightName
		}
		return strings.ToLower(results[left].Slug) < strings.ToLower(results[right].Slug)
	})
	return results, nil
}

func compactStrings(items []string) []string {
	result := []string{}
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		result = append(result, trimmed)
	}
	return result
}
