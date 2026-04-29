package cmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
)

type skillInstallCandidate struct {
	Slug        string
	DisplayName string
	Summary     string
	Reason      string
	Query       string
}

type skillInstallApprovalRequest struct {
	Candidate   skillInstallCandidate
	Scope       string
	InstallPath string
	Reason      string
}

func (a *App) ensureSkillSupplyBeforeStream(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	flowID string,
	input string,
	sessionState *agent.SessionState,
	requestType string,
	approve func(skillInstallApprovalRequest) bool,
	note func(string),
) ([]string, any, error) {
	if note == nil {
		note = func(string) {}
	}
	resolvedContextDir := resolveLocalWorkdir(firstNonEmpty(sessionContextDir(sessionState), config.Workdir))
	skillConfig := a.resolveEffectiveSkillConfig(config, resolvedContextDir)
	requestedSkillNames, clientSkillPackages, err := loadSkillPayloadWithConfig(skillConfig)
	if err != nil {
		return nil, nil, err
	}
	preview, err := providerClient.ResolveSkillPreview(ctx, provider.SkillResolveRequest{
		FlowID:                 strings.TrimSpace(flowID),
		SessionID:              strings.TrimSpace(sessionID(sessionState)),
		RequestType:            normalizeRequestMode(requestType),
		CommandExecutionTarget: "client",
		RequestedSkillNames:    requestedSkillNames,
		ClientSkillPackages:    clientSkillPackages,
	})
	if err != nil {
		note(fmt.Sprintf("技能 resolve 预览失败，继续使用当前本地技能: %v", err))
		return requestedSkillNames, clientSkillPackages, nil
	}
	if len(preview.MissingSkillNames) > 0 {
		note(fmt.Sprintf("技能 resolve 预览完成：已解析 %d 个技能，缺失 %d 个。", len(preview.Skills), len(preview.MissingSkillNames)))
	}
	if internalconfig.NormalizeSkillAutoInstallPolicy(config.Skills.AutoInstallPolicy) == internalconfig.SkillAutoInstallPolicyOff {
		return requestedSkillNames, clientSkillPackages, nil
	}

	installConfig := skillConfig
	candidates, err := collectSkillInstallCandidates(installConfig, input, requestedSkillNames, preview.MissingSkillNames, note)
	if err != nil {
		return requestedSkillNames, clientSkillPackages, err
	}
	if len(candidates) == 0 {
		return requestedSkillNames, clientSkillPackages, nil
	}
	installDir, err := skills.ResolveInstallDirectory(installConfig)
	if err != nil {
		return requestedSkillNames, clientSkillPackages, err
	}
	installedAny := false
	policy := internalconfig.NormalizeSkillAutoInstallPolicy(installConfig.Skills.AutoInstallPolicy)
	for _, candidate := range candidates {
		if policy == internalconfig.SkillAutoInstallPolicyOff {
			note(fmt.Sprintf("技能自动补装已关闭，跳过候选 %s。", candidateLabel(candidate)))
			break
		}
		if policy == internalconfig.SkillAutoInstallPolicyConfirm {
			if approve == nil {
				note(fmt.Sprintf("当前环境无法确认安装，已跳过候选 %s。", candidateLabel(candidate)))
				continue
			}
			approved := approve(skillInstallApprovalRequest{
				Candidate:   candidate,
				Scope:       internalconfig.NormalizeSkillInstallScope(installConfig.Skills.InstallScope),
				InstallPath: installDir,
				Reason:      candidate.Reason,
			})
			if !approved {
				note(fmt.Sprintf("已跳过自动安装候选 %s。", candidateLabel(candidate)))
				continue
			}
		}
		result, installErr := skills.InstallRemote(installConfig, candidate.Slug, "", false)
		if installErr != nil {
			note(fmt.Sprintf("自动安装技能 %s 失败: %v", candidateLabel(candidate), installErr))
			continue
		}
		installedAny = true
		note(fmt.Sprintf("已自动安装技能 %s (%s)，目录: %s", fallbackConfigValue(result.Name, result.Slug), result.Slug, result.TargetDir))
		if sessionState != nil {
			appendApprovedSkillName(sessionState, result.Name)
		}
	}
	if !installedAny {
		return requestedSkillNames, clientSkillPackages, nil
	}
	if sessionState != nil && a.sessionStore != nil {
		if strings.TrimSpace(sessionState.FlowID) == "" {
			sessionState.FlowID = strings.TrimSpace(flowID)
		}
		if strings.TrimSpace(sessionState.Workdir) == "" {
			sessionState.Workdir = resolvedContextDir
		}
		if strings.TrimSpace(sessionState.ContextDir) == "" {
			sessionState.ContextDir = resolvedContextDir
		}
		if err := a.sessionStore.Save(*sessionState); err != nil {
			return nil, nil, err
		}
	}
	note("技能自动补装完成，已刷新本轮请求的技能载荷。")
	return loadSkillPayloadWithConfig(a.resolveEffectiveSkillConfig(installConfig, resolvedContextDir))
}

func collectSkillInstallCandidates(
	config internalconfig.Config,
	input string,
	requestedSkillNames []string,
	missingSkillNames []string,
	note func(string),
) ([]skillInstallCandidate, error) {
	localNameSet := make(map[string]bool, len(requestedSkillNames))
	for _, item := range requestedSkillNames {
		localNameSet[normalizeSkillLookupKey(item)] = true
	}
	installedSlugSet := map[string]bool{}
	installedItems, err := skills.ListInstalledRemoteSkills(config)
	if err != nil {
		note(fmt.Sprintf("读取已安装远程技能失败，继续按本地名称去重: %v", err))
	} else {
		for _, item := range installedItems {
			installedSlugSet[strings.ToLower(strings.TrimSpace(item.Slug))] = true
			localNameSet[normalizeSkillLookupKey(item.Name)] = true
		}
	}
	seenSlugs := map[string]bool{}
	candidates := make([]skillInstallCandidate, 0, len(missingSkillNames)+1)
	for _, missingName := range missingSkillNames {
		results, _, searchErr := skills.SearchRemote(config, missingName, 10)
		if searchErr != nil {
			note(fmt.Sprintf("搜索缺失技能 %s 失败: %v", strings.TrimSpace(missingName), searchErr))
			continue
		}
		candidate, ok := selectExactSkillInstallCandidate(missingName, results, localNameSet, installedSlugSet)
		if !ok {
			note(fmt.Sprintf("缺失技能 %s 未命中可自动安装候选。", strings.TrimSpace(missingName)))
			continue
		}
		if !seenSlugs[strings.ToLower(candidate.Slug)] {
			seenSlugs[strings.ToLower(candidate.Slug)] = true
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) > 0 || !config.Skills.FreeTextSearchEnabled {
		return candidates, nil
	}
	query := strings.TrimSpace(input)
	if query == "" {
		return candidates, nil
	}
	results, _, searchErr := skills.SearchRemote(config, query, 5)
	if searchErr != nil {
		note(fmt.Sprintf("按任务文本搜索远程技能失败: %v", searchErr))
		return candidates, nil
	}
	filtered := filterSkillInstallResults(results, localNameSet, installedSlugSet)
	if len(filtered) == 1 {
		candidate := filtered[0]
		candidate.Reason = "自由文本任务匹配"
		candidate.Query = query
		if !seenSlugs[strings.ToLower(candidate.Slug)] {
			candidates = append(candidates, candidate)
		}
		return candidates, nil
	}
	if len(filtered) > 1 {
		note(fmt.Sprintf("自由文本搜索命中 %d 个候选，已跳过自动安装。", len(filtered)))
	}
	return candidates, nil
}

func selectExactSkillInstallCandidate(
	missingName string,
	results []skills.RemoteSearchResult,
	localNameSet map[string]bool,
	installedSlugSet map[string]bool,
) (skillInstallCandidate, bool) {
	targetKey := normalizeSkillLookupKey(missingName)
	for _, item := range results {
		slug := strings.ToLower(strings.TrimSpace(item.Slug))
		if slug == "" || installedSlugSet[slug] {
			continue
		}
		if localNameSet[normalizeSkillLookupKey(item.DisplayName)] {
			continue
		}
		if normalizeSkillLookupKey(item.Slug) != targetKey && normalizeSkillLookupKey(item.DisplayName) != targetKey {
			continue
		}
		return skillInstallCandidate{
			Slug:        strings.TrimSpace(item.Slug),
			DisplayName: strings.TrimSpace(item.DisplayName),
			Summary:     strings.TrimSpace(item.Summary),
			Reason:      fmt.Sprintf("缺失技能名 %s 的精确匹配", strings.TrimSpace(missingName)),
			Query:       strings.TrimSpace(missingName),
		}, true
	}
	return skillInstallCandidate{}, false
}

func filterSkillInstallResults(
	results []skills.RemoteSearchResult,
	localNameSet map[string]bool,
	installedSlugSet map[string]bool,
) []skillInstallCandidate {
	filtered := make([]skillInstallCandidate, 0, len(results))
	for _, item := range results {
		slug := strings.ToLower(strings.TrimSpace(item.Slug))
		if slug == "" || installedSlugSet[slug] {
			continue
		}
		if localNameSet[normalizeSkillLookupKey(item.DisplayName)] || localNameSet[normalizeSkillLookupKey(item.Slug)] {
			continue
		}
		filtered = append(filtered, skillInstallCandidate{
			Slug:        strings.TrimSpace(item.Slug),
			DisplayName: strings.TrimSpace(item.DisplayName),
			Summary:     strings.TrimSpace(item.Summary),
		})
	}
	return filtered
}

func normalizeSkillLookupKey(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return -1
		}
	}, trimmed)
}

func candidateLabel(candidate skillInstallCandidate) string {
	if strings.TrimSpace(candidate.DisplayName) != "" {
		return candidate.DisplayName
	}
	return candidate.Slug
}

func sessionContextDir(state *agent.SessionState) string {
	if state == nil {
		return ""
	}
	return strings.TrimSpace(state.ContextDir)
}

func sessionID(state *agent.SessionState) string {
	if state == nil {
		return ""
	}
	return strings.TrimSpace(state.SessionID)
}

func (a *App) confirmSkillAutoInstall(request skillInstallApprovalRequest) bool {
	if a.stdout == nil || a.stdin == nil {
		return false
	}
	_, _ = fmt.Fprintf(a.stdout, "[系统提示]: 检测到可补装技能 %s (%s)\n", candidateLabel(request.Candidate), request.Candidate.Slug)
	if strings.TrimSpace(request.Reason) != "" {
		_, _ = fmt.Fprintf(a.stdout, "原因: %s\n", request.Reason)
	}
	if strings.TrimSpace(request.Candidate.Summary) != "" {
		_, _ = fmt.Fprintf(a.stdout, "摘要: %s\n", request.Candidate.Summary)
	}
	_, _ = fmt.Fprintf(a.stdout, "安装位置: %s\n目标目录: %s\n是否安装? [y/N]: ", skillInstallScopeLabelText(request.Scope), request.InstallPath)
	reader := bufio.NewReader(a.stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}