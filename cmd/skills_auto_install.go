package cmd

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
)

var missingSkillPattern = regexp.MustCompile(`未找到技能[:：]\s*([^\n\r]+)`)

const missingSkillsErrorCode = "MISSING_SKILLS"

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

func detectMissingSkillNamesFromError(err error) []string {
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return nil
	}
	matches := missingSkillPattern.FindAllStringSubmatch(message, -1)
	if len(matches) == 0 {
		return nil
	}
	resolved := map[string]string{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		raw := strings.TrimSpace(match[1])
		if raw == "" {
			continue
		}
		for _, item := range strings.FieldsFunc(raw, func(r rune) bool {
			switch r {
			case ',', '，', '、', ';', '；', '\n', '\r':
				return true
			default:
				return false
			}
		}) {
			trimmed := strings.TrimSpace(item)
			if trimmed == "" {
				continue
			}
			resolved[strings.ToLower(trimmed)] = trimmed
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	items := make([]string, 0, len(resolved))
	for _, item := range resolved {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i]) < strings.ToLower(items[j])
	})
	return items
}

func detectMissingSkillNames(eventData map[string]any, err error) []string {
	structured := extractMissingSkillNamesFromEventData(eventData)
	if len(structured) > 0 {
		return structured
	}
	return detectMissingSkillNamesFromError(err)
}

func extractMissingSkillNamesFromEventData(eventData map[string]any) []string {
	if len(eventData) == 0 {
		return nil
	}

	resolved := map[string]string{}
	appendNames := func(raw any) {
		switch value := raw.(type) {
		case []string:
			for _, item := range value {
				trimmed := strings.TrimSpace(item)
				if trimmed == "" {
					continue
				}
				resolved[strings.ToLower(trimmed)] = trimmed
			}
		case []any:
			for _, item := range value {
				text, ok := item.(string)
				if !ok {
					continue
				}
				trimmed := strings.TrimSpace(text)
				if trimmed == "" {
					continue
				}
				resolved[strings.ToLower(trimmed)] = trimmed
			}
		case string:
			for _, item := range strings.FieldsFunc(value, func(r rune) bool {
				switch r {
				case ',', '，', '、', ';', '；', '\n', '\r':
					return true
				default:
					return false
				}
			}) {
				trimmed := strings.TrimSpace(item)
				if trimmed == "" {
					continue
				}
				resolved[strings.ToLower(trimmed)] = trimmed
			}
		}
	}

	errorCode := strings.ToUpper(strings.TrimSpace(fmt.Sprintf("%v", eventData["error_code"])))
	appendNames(eventData["missing_skill_names"])
	appendNames(eventData["missing_skills"])

	if details, ok := eventData["details"].(map[string]any); ok {
		appendNames(details["missing_skill_names"])
		appendNames(details["missing_skills"])
	}

	if len(resolved) == 0 && errorCode == missingSkillsErrorCode {
		appendNames(eventData["error"])
	}

	if len(resolved) == 0 {
		return nil
	}

	items := make([]string, 0, len(resolved))
	for _, item := range resolved {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i]) < strings.ToLower(items[j])
	})
	return items
}

func (a *App) autoInstallCandidates(
	config internalconfig.Config,
	sessionState *agent.SessionState,
	input string,
	requestedSkillNames []string,
	missingSkillNames []string,
	approve func(skillInstallApprovalRequest) bool,
	note func(string),
) (bool, error) {
	if note == nil {
		note = func(string) {}
	}
	policy := internalconfig.NormalizeSkillAutoInstallPolicy(config.Skills.AutoInstallPolicy)
	if policy == internalconfig.SkillAutoInstallPolicyOff {
		return false, nil
	}
	candidates, err := collectSkillInstallCandidates(config, sessionState, input, requestedSkillNames, missingSkillNames, note)
	if err != nil {
		return false, err
	}
	if len(candidates) == 0 {
		return false, nil
	}
	installDir, err := skills.ResolveInstallDirectory(config)
	if err != nil {
		return false, err
	}
	installedAny := false
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
				Scope:       internalconfig.NormalizeSkillInstallScope(config.Skills.InstallScope),
				InstallPath: installDir,
				Reason:      candidate.Reason,
			})
			if !approved {
				note(fmt.Sprintf("已跳过自动安装候选 %s。", candidateLabel(candidate)))
				continue
			}
		}
		result, installErr := skills.InstallRemote(config, candidate.Slug, "", false)
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
	return installedAny, nil
}

func collectSkillInstallCandidates(
	config internalconfig.Config,
	sessionState *agent.SessionState,
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
	searchedQueries := map[string]struct{}{}
	candidates := make([]skillInstallCandidate, 0, len(missingSkillNames)+1)
	for _, missingName := range missingSkillNames {
		query := strings.TrimSpace(missingName)
		if query == "" {
			continue
		}
		searchedQueries[strings.ToLower(query)] = struct{}{}
		results, _, searchErr := skills.SearchRemote(config, query, 10)
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
	searchKeywords := buildSkillSearchKeywords(input, missingSkillNames, sessionState)
	if len(searchKeywords) == 0 {
		return candidates, nil
	}
	note(fmt.Sprintf("自由文本关键词检索: %s", strings.Join(searchKeywords, ", ")))
	aggregatedResults := make([]skills.RemoteSearchResult, 0, len(searchKeywords)*5)
	for _, keyword := range searchKeywords {
		normalizedKeyword := strings.ToLower(strings.TrimSpace(keyword))
		if _, duplicated := searchedQueries[normalizedKeyword]; duplicated {
			continue
		}
		searchedQueries[normalizedKeyword] = struct{}{}
		results, _, searchErr := skills.SearchRemote(config, keyword, 5)
		if searchErr != nil {
			note(fmt.Sprintf("按关键词 %s 搜索远程技能失败: %v", keyword, searchErr))
			continue
		}
		aggregatedResults = append(aggregatedResults, results...)
	}
	filtered := filterSkillInstallResults(aggregatedResults, localNameSet, installedSlugSet)
	if len(filtered) == 1 {
		candidate := filtered[0]
		candidate.Reason = "关键词检索匹配"
		candidate.Query = strings.Join(searchKeywords, " ")
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
	type scoredCandidate struct {
		candidate skillInstallCandidate
		score     int
	}
	scored := map[string]scoredCandidate{}
	filtered := make([]skillInstallCandidate, 0, len(results))
	for _, item := range results {
		slug := strings.ToLower(strings.TrimSpace(item.Slug))
		if slug == "" || installedSlugSet[slug] {
			continue
		}
		if localNameSet[normalizeSkillLookupKey(item.DisplayName)] || localNameSet[normalizeSkillLookupKey(item.Slug)] {
			continue
		}
		entry, ok := scored[slug]
		if !ok {
			entry = scoredCandidate{
				candidate: skillInstallCandidate{
					Slug:        strings.TrimSpace(item.Slug),
					DisplayName: strings.TrimSpace(item.DisplayName),
					Summary:     strings.TrimSpace(item.Summary),
				},
			}
		}
		entry.score++
		scored[slug] = entry
	}
	if len(scored) == 0 {
		return filtered
	}
	type sortable struct {
		slug  string
		entry scoredCandidate
	}
	sorted := make([]sortable, 0, len(scored))
	for slug, entry := range scored {
		sorted = append(sorted, sortable{slug: slug, entry: entry})
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].entry.score == sorted[j].entry.score {
			return sorted[i].slug < sorted[j].slug
		}
		return sorted[i].entry.score > sorted[j].entry.score
	})
	for _, item := range sorted {
		filtered = append(filtered, item.entry.candidate)
	}
	return filtered
}

var skillKeywordPattern = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_-]{2,}|[\p{Han}]{2,}`)

func buildSkillSearchKeywords(input string, missingSkillNames []string, sessionState *agent.SessionState) []string {
	keywords := make([]string, 0, 16)
	seen := map[string]struct{}{}
	appendKeyword := func(raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return
		}
		if isSkillSearchStopWord(trimmed) {
			return
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		keywords = append(keywords, trimmed)
	}
	for _, name := range missingSkillNames {
		appendKeyword(name)
	}
	for _, text := range []string{strings.TrimSpace(input), buildSkillSearchSessionSummary(sessionState)} {
		for _, token := range skillKeywordPattern.FindAllString(text, -1) {
			appendKeyword(token)
			if len(keywords) >= 8 {
				return keywords
			}
		}
	}
	return keywords
}

func buildSkillSearchSessionSummary(sessionState *agent.SessionState) string {
	if sessionState == nil {
		return ""
	}
	parts := []string{}
	appendPart := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	appendPart(sessionState.FlowDisplayName)
	appendPart(sessionState.SessionTitle)
	for _, todoItem := range sessionState.TodoItems {
		appendPart(todoItem.Title)
		appendPart(todoItem.Notes)
	}
	summary := strings.Join(parts, " ")
	if len([]rune(summary)) > 120 {
		return string([]rune(summary)[:120])
	}
	return summary
}

func isSkillSearchStopWord(word string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(word))
	if trimmed == "" {
		return true
	}
	if allPunctuation := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.Is(unicode.Han, r) {
			return r
		}
		return -1
	}, trimmed); allPunctuation == "" {
		return true
	}
	stopWords := map[string]struct{}{
		"请": {}, "帮我": {}, "一下": {}, "我们": {}, "需要": {}, "处理": {},
		"这个": {}, "那个": {}, "任务": {}, "问题": {}, "实现": {}, "一个": {},
		"the": {}, "and": {}, "for": {}, "with": {}, "from": {}, "that": {},
		"this": {}, "help": {}, "need": {}, "please": {}, "task": {},
	}
	_, blocked := stopWords[trimmed]
	return blocked
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
