package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// BackendInstallResult 后端技能安装结果
type BackendInstallResult struct {
	Name      string
	SkillID   string
	Version   string
	TargetDir string
	RecordID  string
}

// BackendRemoveResult 后端技能卸载结果
type BackendRemoveResult struct {
	Name      string
	SkillID   string
	RecordID  string
	TargetDir string
}

// BackendUploadResult 后端技能上传结果
type BackendUploadResult struct {
	Name        string
	SkillID     string
	DisplayName string
	Version     string
}

// BackendSyncResult 后端技能同步结果
type BackendSyncResult struct {
	Installed []string
	Updated   []string
	Failed    []string
	Skipped   []string
}

// ClientInfo 客户端信息（用于安装记录上报）
type ClientInfo struct {
	ClientID       string
	DeviceID       string
	ClientPlatform string
	ClientVersion  string
	InstallSource  string
	InstallScope   string
}

// FetchBackendSkills 从后端拉取可见技能列表
func FetchBackendSkills(ctx context.Context, client provider.Provider, params provider.FetchVisibleSkillsParams) ([]provider.VisibleSkillPackage, error) {
	return client.FetchVisibleSkills(ctx, params)
}

// InstallBackendSkill 从后端下载技能内容并写入本地目录，同时上报安装记录
func InstallBackendSkill(
	ctx context.Context,
	client provider.Provider,
	config internalconfig.Config,
	skill provider.VisibleSkillPackage,
	clientInfo ClientInfo,
) (BackendInstallResult, error) {
	// 1. 下载技能内容
	content, err := client.DownloadSkillContent(ctx, skill.ID)
	if err != nil {
		return BackendInstallResult{}, fmt.Errorf("下载技能内容失败: %w", err)
	}

	// 2. 解析安装目录
	installDir, err := ResolveInstallDirectory(config)
	if err != nil {
		return BackendInstallResult{}, fmt.Errorf("解析技能安装目录失败: %w", err)
	}

	// 3. 构造目标目录（使用 slug 风格的 name）
	skillDirName := SlugifySkillName(skill.Name)
	if skillDirName == "" {
		skillDirName = skill.ID
	}
	targetDir := filepath.Join(installDir, skillDirName)

	// 4. 写入技能文件
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return BackendInstallResult{}, fmt.Errorf("创建技能目录失败: %w", err)
	}
	if content.IsZip && len(content.RawBytes) > 0 {
		// ZIP 模式：解压全部文件到目标目录
		if err := unzipSkillArchive(content.RawBytes, targetDir); err != nil {
			return BackendInstallResult{}, fmt.Errorf("解压技能 ZIP 失败: %w", err)
		}
	} else {
		// 回退模式：仅写入 SKILL.md
		skillFilePath := filepath.Join(targetDir, skillFileName)
		if err := os.WriteFile(skillFilePath, []byte(content.Content), 0o644); err != nil {
			return BackendInstallResult{}, fmt.Errorf("写入技能文件失败: %w", err)
		}
	}

	// 5. 写入来源元数据
	origin := remoteOrigin{
		Version:          2,
		Registry:         "backend",
		Slug:             skillDirName,
		InstalledVersion: content.Version,
		InstalledAt:      time.Now().Unix(),
	}
	if err := writeRemoteOrigin(targetDir, origin); err != nil {
		return BackendInstallResult{}, fmt.Errorf("写入技能来源失败: %w", err)
	}

	// 6. 上报安装记录
	installSource := strings.TrimSpace(clientInfo.InstallSource)
	if installSource == "" {
		installSource = "manual"
	}
	record, err := client.ReportInstallation(ctx, provider.ReportInstallationRequest{
		SkillPackageID: skill.ID,
		ClientID:       clientInfo.ClientID,
		DeviceID:       clientInfo.DeviceID,
		InstallSource:  installSource,
		ClientPlatform: clientInfo.ClientPlatform,
		ClientVersion:  clientInfo.ClientVersion,
	})
	if err != nil {
		// 上报失败不阻塞安装，仅记录警告
		// 返回安装结果但不含记录 ID
		return BackendInstallResult{
			Name:      skill.Name,
			SkillID:   skill.ID,
			Version:   content.Version,
			TargetDir: targetDir,
		}, nil
	}

	recordID := ""
	if record != nil {
		recordID = record.ID
	}

	return BackendInstallResult{
		Name:      skill.Name,
		SkillID:   skill.ID,
		Version:   content.Version,
		TargetDir: targetDir,
		RecordID:  recordID,
	}, nil
}

// RemoveBackendSkill 卸载后端技能：标记卸载记录 + 清除本地缓存
func RemoveBackendSkill(
	ctx context.Context,
	client provider.Provider,
	config internalconfig.Config,
	skillNameOrID string,
) (BackendRemoveResult, error) {
	// 1. 查询安装记录
	records, err := client.QueryInstallations(ctx, provider.QueryInstallationsParams{
		Limit: 200,
	})
	if err != nil {
		// 网络请求失败时，尝试本地清理
		return removeLocalInstalledSkill(config, skillNameOrID)
	}

	// 2. 查找匹配的安装记录
	var matchedRecord *provider.SkillInstallationRecord
	visibleSkills, _ := client.FetchVisibleSkills(ctx, provider.FetchVisibleSkillsParams{})
	for _, record := range records.Items {
		if record.Status != "installed" {
			continue
		}
		// 按 skill_name 或 skill_package_id 匹配
		if strings.EqualFold(record.SkillName, skillNameOrID) || strings.EqualFold(record.SkillPackageID, skillNameOrID) {
			matchedRecord = &record
			break
		}
		// 交叉查找 visibleSkills
		if visibleSkills != nil {
			for _, vs := range visibleSkills {
				if vs.ID == record.SkillPackageID && (strings.EqualFold(vs.Name, skillNameOrID) || strings.EqualFold(vs.DisplayName, skillNameOrID)) {
					matchedRecord = &record
					break
				}
			}
			if matchedRecord != nil {
				break
			}
		}
	}
	if matchedRecord != nil {
		// 3. 标记卸载
		updated, err := client.UninstallSkillRecord(ctx, matchedRecord.ID)
		if err != nil {
			return BackendRemoveResult{}, fmt.Errorf("标记技能卸载失败: %w", err)
		}

		// 4. 清除本地缓存
		skillName := matchedRecord.SkillName
		if skillName == "" && updated != nil {
			skillName = updated.SkillName
		}
		if skillName == "" && visibleSkills != nil {
			for _, vs := range visibleSkills {
				if vs.ID == matchedRecord.SkillPackageID {
					skillName = vs.Name
					break
				}
			}
		}

		skillDirName := SlugifySkillName(skillName)
		if skillDirName == "" {
			skillDirName = matchedRecord.SkillPackageID
		}
		installDir, err := ResolveInstallDirectory(config)
		if err == nil && skillDirName != "" {
			targetDir := filepath.Join(installDir, skillDirName)
			_ = os.RemoveAll(targetDir)
			return BackendRemoveResult{
				Name:      skillName,
				SkillID:   matchedRecord.SkillPackageID,
				RecordID:  matchedRecord.ID,
				TargetDir: targetDir,
			}, nil
		}

		return BackendRemoveResult{
			Name:     skillName,
			SkillID:  matchedRecord.SkillPackageID,
			RecordID: matchedRecord.ID,
		}, nil
	}

	// 5. 服务端安装记录不存在时，回退到本地扫描清理
	// 适用于技能在服务端已被删除但本地仍残留的情况
	return removeLocalInstalledSkill(config, skillNameOrID)
}

// removeLocalInstalledSkill 在服务端无安装记录或网络不可用时，
// 扫描本地技能并清除匹配项。
// 先查有 origin.json 的远程安装技能，再回退到全量 Discover 扫描，
// 确保纯本地手动创建的技能也能被删除。
func removeLocalInstalledSkill(config internalconfig.Config, skillNameOrID string) (BackendRemoveResult, error) {
	// 1. 先扫描有 origin.json 的远程安装技能
	installedItems, localErr := ListInstalledRemoteSkills(config)
	if localErr == nil {
		for _, item := range installedItems {
			if strings.EqualFold(item.Slug, skillNameOrID) ||
				strings.EqualFold(item.Name, skillNameOrID) ||
				strings.EqualFold(filepath.Base(strings.TrimSpace(item.TargetDir)), skillNameOrID) {
				targetDir := strings.TrimSpace(item.TargetDir)
				if targetDir != "" {
					_ = os.RemoveAll(targetDir)
				}
				return BackendRemoveResult{
					Name:      item.Name,
					SkillID:   item.Slug,
					TargetDir: targetDir,
				}, nil
			}
		}
	}

	// 2. 回退到全量 Discover 扫描，匹配无 origin.json 的纯本地技能
	discoveredItems, _, discoverErr := Discover(config)
	if discoverErr != nil {
		return BackendRemoveResult{}, fmt.Errorf("未找到已安装的技能: %s", skillNameOrID)
	}
	for _, item := range discoveredItems {
		// 跳过内置技能
		if IsBundledSkill(item.Name) {
			continue
		}
		skillDir := SkillDir(item)
		dirBase := filepath.Base(strings.TrimSpace(skillDir))
		slugName := SlugifySkillName(item.Name)
		if strings.EqualFold(item.Name, skillNameOrID) ||
			strings.EqualFold(slugName, skillNameOrID) ||
			strings.EqualFold(dirBase, skillNameOrID) {
			if skillDir != "" {
				_ = os.RemoveAll(skillDir)
			}
			return BackendRemoveResult{
				Name:      item.Name,
				SkillID:   slugName,
				TargetDir: skillDir,
			}, nil
		}
	}
	return BackendRemoveResult{}, fmt.Errorf("未找到已安装的技能: %s", skillNameOrID)
}

// UploadLocalSkillToBackend 读取本地技能内容，打包为 ZIP 后上传到后端
func UploadLocalSkillToBackend(
	ctx context.Context,
	client provider.Provider,
	config internalconfig.Config,
	skillName string,
) (BackendUploadResult, error) {
	// 1. 从本地发现技能
	item, _, err := LoadByName(config, skillName)
	if err != nil {
		return BackendUploadResult{}, fmt.Errorf("未找到本地技能 %s: %w", skillName, err)
	}

	// 2. 禁止上传内置技能
	if item.Source == "bundled" || IsBundledSkill(item.Name) {
		return BackendUploadResult{}, fmt.Errorf("不允许上传内置技能 %q", skillName)
	}

	// 3. 构造 slug 风格名称
	// 优先使用技能磁盘目录名（CreateSkillScaffold 已通过 SlugifySkillName 生成），
	// 保证 ZIP 顶层目录名与磁盘目录一致；目录名无效时回退到技能名称，再保底。
	slugName := SlugifySkillName(filepath.Base(SkillDir(*item)))
	if slugName == "" {
		slugName = SlugifySkillName(item.Name)
	}
	if slugName == "" {
		slugName = "custom-skill"
	}

	// 4. 将技能目录打包为内存 ZIP
	zipData, err := PackSkillToZip(*item, slugName)
	if err != nil {
		return BackendUploadResult{}, fmt.Errorf("打包技能 %s 为 ZIP 失败: %w", skillName, err)
	}

	// 5. 通过 multipart/form-data 上传 ZIP 到后端
	result, err := client.UploadCustomSkillArchive(ctx, provider.UploadCustomSkillArchiveRequest{
		ZipData:     zipData,
		ZipFileName: slugName + ".zip",
		Name:        slugName,
		DisplayName: item.Name,
	})
	if err != nil {
		return BackendUploadResult{}, fmt.Errorf("上传技能 %s 失败: %w", skillName, err)
	}

	return BackendUploadResult{
		Name:        result.Name,
		SkillID:     result.ID,
		DisplayName: result.DisplayName,
		Version:     result.Version,
	}, nil
}

// UploadSkillArchiveToBackend 上传 ZIP 技能包到后端
func UploadSkillArchiveToBackend(
	ctx context.Context,
	client provider.Provider,
	filePath string,
	displayName string,
) (BackendUploadResult, error) {
	slugName := SlugifySkillName(displayName)
	if slugName == "" {
		slugName = "custom-skill"
	}
	result, err := client.UploadCustomSkillArchive(ctx, provider.UploadCustomSkillArchiveRequest{
		FilePath:    filePath,
		Name:        slugName,
		DisplayName: displayName,
	})
	if err != nil {
		return BackendUploadResult{}, fmt.Errorf("上传技能 ZIP 失败: %w", err)
	}
	return BackendUploadResult{
		Name:        result.Name,
		SkillID:     result.ID,
		DisplayName: result.DisplayName,
		Version:     result.Version,
	}, nil
}

// UploadSkillContentToBackend 上传纯文本技能内容到后端
func UploadSkillContentToBackend(
	ctx context.Context,
	client provider.Provider,
	name string,
	content string,
	displayName string,
	summary string,
	version string,
) (BackendUploadResult, error) {
	slugName := SlugifySkillName(name)
	if slugName == "" {
		slugName = "custom-skill"
	}
	if strings.TrimSpace(version) == "" {
		version = "1.0.0"
	}
	result, err := client.UploadCustomSkill(ctx, provider.UploadCustomSkillRequest{
		Name:        slugName,
		Content:     content,
		DisplayName: displayName,
		Summary:     summary,
		Version:     version,
	})
	if err != nil {
		return BackendUploadResult{}, fmt.Errorf("上传技能失败: %w", err)
	}
	return BackendUploadResult{
		Name:        result.Name,
		SkillID:     result.ID,
		DisplayName: result.DisplayName,
		Version:     result.Version,
	}, nil
}

// DeleteBackendSkill 从后端删除自定义技能
func DeleteBackendSkill(
	ctx context.Context,
	client provider.Provider,
	skillID string,
) error {
	return client.DeleteCustomSkill(ctx, skillID)
}

// SyncBackendSkills 检测服务端已安装但本地缺失的技能，自动补装
func SyncBackendSkills(
	ctx context.Context,
	client provider.Provider,
	config internalconfig.Config,
	clientInfo ClientInfo,
) (BackendSyncResult, error) {
	result := BackendSyncResult{
		Installed: []string{},
		Updated:   []string{},
		Failed:    []string{},
		Skipped:   []string{},
	}

	// 1. 获取本地已发现技能名称集合
	localItems, _, err := Discover(config)
	if err != nil {
		return result, fmt.Errorf("发现本地技能失败: %w", err)
	}
	localNames := map[string]bool{}
	localVersions := map[string]string{}
	for _, item := range localItems {
		if item.Source == "bundled" {
			continue
		}
		lowerName := strings.ToLower(strings.TrimSpace(item.Name))
		if lowerName != "" {
			localNames[lowerName] = true
		}
		dirName := filepath.Base(SkillDir(item))
		if dirName != "" && dirName != "." {
			localNames[strings.ToLower(dirName)] = true
		}
		// 提取版本
		version := ""
		if item.Metadata != nil {
			if v, ok := item.Metadata["version"].(string); ok {
				version = strings.TrimSpace(v)
			}
		}
		if version != "" {
			localVersions[lowerName] = version
			localVersions[strings.ToLower(dirName)] = version
		}
	}

	// 2. 获取服务端安装记录
	records, err := client.QueryInstallations(ctx, provider.QueryInstallationsParams{
		ClientID: clientInfo.ClientID,
		Limit:    200,
	})
	if err != nil {
		return result, fmt.Errorf("查询安装记录失败: %w", err)
	}

	// 3. 获取可见技能列表（用于交叉查找技能名称）
	visibleSkills, err := client.FetchVisibleSkills(ctx, provider.FetchVisibleSkillsParams{})
	if err != nil {
		return result, fmt.Errorf("获取可见技能列表失败: %w", err)
	}
	visibleSkillMap := map[string]provider.VisibleSkillPackage{}
	for _, vs := range visibleSkills {
		visibleSkillMap[vs.ID] = vs
	}

	// 4. 筛选缺失和过期技能
	var missing []provider.SkillInstallationRecord
	var outdated []provider.SkillInstallationRecord
	for _, record := range records.Items {
		if record.Status != "installed" {
			continue
		}
		// 解析技能名称
		skillName := strings.ToLower(strings.TrimSpace(record.SkillName))
		if skillName == "" {
			if vs, ok := visibleSkillMap[record.SkillPackageID]; ok {
				skillName = strings.ToLower(strings.TrimSpace(vs.Name))
			}
		}
		if skillName == "" {
			continue
		}
		if !localNames[skillName] {
			missing = append(missing, record)
		} else {
			localVersion := localVersions[skillName]
			serverVersion := strings.TrimSpace(record.SkillVersion)
			if localVersion != "" && serverVersion != "" && localVersion != serverVersion {
				outdated = append(outdated, record)
			}
		}
	}

	// 5. 逐个安装缺失技能
	for _, record := range missing {
		vs, ok := visibleSkillMap[record.SkillPackageID]
		if !ok {
			result.Failed = append(result.Failed, record.SkillName)
			continue
		}
		installResult, err := InstallBackendSkill(ctx, client, config, vs, clientInfo)
		if err != nil {
			name := vs.Name
			if name == "" {
				name = record.SkillPackageID
			}
			result.Failed = append(result.Failed, name)
			continue
		}
		result.Installed = append(result.Installed, installResult.Name)
	}

	// 6. 逐个更新过期技能
	for _, record := range outdated {
		vs, ok := visibleSkillMap[record.SkillPackageID]
		if !ok {
			result.Failed = append(result.Failed, record.SkillName)
			continue
		}
		installResult, err := InstallBackendSkill(ctx, client, config, vs, clientInfo)
		if err != nil {
			name := vs.Name
			if name == "" {
				name = record.SkillPackageID
			}
			result.Failed = append(result.Failed, name)
			continue
		}
		result.Updated = append(result.Updated, installResult.Name)
	}

	return result, nil
}

// SlugifySkillName 将技能名称转换为 slug 风格
func SlugifySkillName(name string) string {
	if name == "" {
		return ""
	}
	result := strings.ToLower(strings.TrimSpace(name))
	result = strings.ReplaceAll(result, " ", "-")
	result = strings.ReplaceAll(result, "_", "-")
	// 合并连续横线
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}
	// 只保留小写字母、数字和横线，过滤中文字符及其他非 ASCII 字符
	var builder strings.Builder
	for _, r := range result {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-':
			builder.WriteRune(r)
		}
	}
	result = builder.String()
	// 去首尾横线
	result = strings.Trim(result, "-")
	return result
}

// unzipSkillArchive 将 ZIP 字节解压到目标目录
//
// ZIP 结构约定：顶层目录名/SKILL.md、顶层目录名/references/...
// 解压时去掉 ZIP 内的顶层目录名，将文件直接写入 targetDir。
// 跳过目录条目和隐藏文件（以 . 开头）。
func unzipSkillArchive(zipData []byte, targetDir string) error {
	reader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return fmt.Errorf("打开 ZIP 失败: %w", err)
	}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		entryName := filepath.FromSlash(file.Name)
		// 去掉 ZIP 内顶层目录名（slug/SKILL.md → SKILL.md）
		if idx := strings.IndexByte(entryName, '/'); idx >= 0 {
			entryName = entryName[idx+1:]
		}
		if entryName == "" || strings.HasPrefix(filepath.Base(entryName), ".") {
			continue
		}
		destPath := filepath.Join(targetDir, entryName)
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", filepath.Dir(destPath), err)
		}
		rc, err := file.Open()
		if err != nil {
			return fmt.Errorf("打开 ZIP 条目 %s 失败: %w", file.Name, err)
		}
		out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return fmt.Errorf("创建文件 %s 失败: %w", destPath, err)
		}
		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			return fmt.Errorf("写入文件 %s 失败: %w", destPath, err)
		}
		rc.Close()
		out.Close()
	}
	return nil
}
