package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// BackendInstallResult 后端技能安装结果
type BackendInstallResult struct {
	Name        string
	SkillID     string
	Version     string
	TargetDir   string
	RecordID    string
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

	// 4. 写入 SKILL.md
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return BackendInstallResult{}, fmt.Errorf("创建技能目录失败: %w", err)
	}
	skillFilePath := filepath.Join(targetDir, skillFileName)
	if err := os.WriteFile(skillFilePath, []byte(content.Content), 0o644); err != nil {
		return BackendInstallResult{}, fmt.Errorf("写入技能文件失败: %w", err)
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
		SkillPackageID:  skill.ID,
		ClientID:        clientInfo.ClientID,
		DeviceID:        clientInfo.DeviceID,
		InstallSource:   installSource,
		ClientPlatform:  clientInfo.ClientPlatform,
		ClientVersion:   clientInfo.ClientVersion,
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
		return BackendRemoveResult{}, fmt.Errorf("查询安装记录失败: %w", err)
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
	if matchedRecord == nil {
		return BackendRemoveResult{}, fmt.Errorf("未找到已安装的技能: %s", skillNameOrID)
	}

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

// UploadLocalSkillToBackend 读取本地技能内容并上传到后端
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

	// 2. 读取 SKILL.md 原文内容
	skillContent := Markdown(*item)

	// 3. 构造 slug 风格名称
	slugName := SlugifySkillName(item.Name)
	if slugName == "" {
		slugName = filepath.Base(SkillDir(*item))
	}

	// 4. 上传到后端
	version := "1.0.0"
	if item.Metadata != nil {
		if v, ok := item.Metadata["version"].(string); ok && strings.TrimSpace(v) != "" {
			version = strings.TrimSpace(v)
		}
	}

	result, err := client.UploadCustomSkill(ctx, provider.UploadCustomSkillRequest{
		Name:        slugName,
		Content:     skillContent,
		DisplayName: item.Name,
		Summary:     item.Summary,
		Version:     version,
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
	// 去除非字母数字横线和中文字符
	var builder strings.Builder
	for _, r := range result {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-':
			builder.WriteRune(r)
		case r >= 0x4e00 && r <= 0x9fff:
			builder.WriteRune(r)
		}
	}
	result = builder.String()
	// 去首尾横线
	result = strings.Trim(result, "-")
	return result
}
