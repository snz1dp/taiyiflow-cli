package agent

import (
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

type SessionState struct {
	FlowID                         string            `json:"flow_id"`
	FlowDisplayName                string            `json:"flow_display_name,omitempty"`
	SessionID                      string            `json:"session_id"`
	SessionTitle                   string            `json:"session_title,omitempty"`
	SessionTitleAliases            map[string]string `json:"session_title_aliases,omitempty"`
	LastRunID                      string            `json:"last_run_id,omitempty"`
	LastMessageID                  string            `json:"last_message_id,omitempty"`
	Workdir                        string            `json:"workdir,omitempty"`
	RequestMode                    string            `json:"request_mode,omitempty"`
	ContextDir                     string            `json:"context_dir,omitempty"`
	ReasoningOverride              string            `json:"reasoning_override,omitempty"`
	InternetSearchOverride         string            `json:"internet_search_override,omitempty"`
	AgentAutoLoopEnabledOverride   string            `json:"agent_auto_loop_enabled_override,omitempty"`
	AgentAutoLoopMaxRoundsOverride *int              `json:"agent_auto_loop_max_rounds_override,omitempty"`
	SandboxEscapePolicyOverride    string            `json:"sandbox_escape_policy_override,omitempty"`
	SkillReviewPolicyOverride      string            `json:"skill_review_policy_override,omitempty"`
	ApprovedSkillNames             []string          `json:"approved_skill_names,omitempty"`
	TodoItems                      []TodoItem        `json:"todo_items,omitempty"`
	UpdatedAt                      time.Time         `json:"updated_at"`
}

type TodoItem struct {
	Title  string `json:"title"`
	Status string `json:"status"`
	Notes  string `json:"notes,omitempty"`
}

type SessionStore struct {
	rootDir string
}

func (s SessionState) LocalSessionTitle(sessionID string) string {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" || len(s.SessionTitleAliases) == 0 {
		return ""
	}
	return strings.TrimSpace(s.SessionTitleAliases[trimmedSessionID])
}

func (s *SessionState) SetLocalSessionTitle(sessionID string, title string) {
	if s == nil {
		return
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	trimmedTitle := strings.TrimSpace(title)
	if trimmedSessionID == "" {
		return
	}
	if trimmedTitle == "" {
		delete(s.SessionTitleAliases, trimmedSessionID)
		if len(s.SessionTitleAliases) == 0 {
			s.SessionTitleAliases = nil
		}
		return
	}
	if s.SessionTitleAliases == nil {
		s.SessionTitleAliases = map[string]string{}
	}
	s.SessionTitleAliases[trimmedSessionID] = trimmedTitle
}

func NewSessionStore() (*SessionStore, error) {
	rootDir, err := internalconfig.DefaultRootDir()
	if err != nil {
		return nil, err
	}
	return &SessionStore{rootDir: filepath.Join(rootDir, "sessions")}, nil
}

func (s *SessionStore) Load(flowID string, workdir string) (SessionState, error) {
	normalizedWorkdir := normalizeWorkdir(workdir)
	content, err := os.ReadFile(s.filePath(flowID, normalizedWorkdir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SessionState{FlowID: flowID, Workdir: normalizedWorkdir}, nil
		}
		return SessionState{}, fmt.Errorf("读取会话失败: %w", err)
	}

	var state SessionState
	if err := json.Unmarshal(content, &state); err != nil {
		return SessionState{}, fmt.Errorf("解析会话失败: %w", err)
	}
	if state.FlowID == "" {
		state.FlowID = flowID
	}
	state.FlowDisplayName = strings.TrimSpace(state.FlowDisplayName)
	state.SessionTitle = strings.TrimSpace(state.SessionTitle)
	state.SessionTitleAliases = normalizeSessionTitleAliases(state.SessionTitleAliases)
	state.LastRunID = strings.TrimSpace(state.LastRunID)
	state.LastMessageID = strings.TrimSpace(state.LastMessageID)
	if strings.TrimSpace(state.Workdir) == "" {
		state.Workdir = normalizedWorkdir
	}
	state.RequestMode = normalizeRequestMode(state.RequestMode)
	if strings.TrimSpace(state.ContextDir) == "" {
		state.ContextDir = normalizedWorkdir
	} else {
		state.ContextDir = normalizeWorkdir(state.ContextDir)
	}
	state.ReasoningOverride = normalizeReasoningOverride(state.ReasoningOverride)
	state.InternetSearchOverride = normalizeInternetSearchOverride(state.InternetSearchOverride)
	state.AgentAutoLoopEnabledOverride = normalizeAgentAutoLoopEnabledOverride(state.AgentAutoLoopEnabledOverride)
	state.AgentAutoLoopMaxRoundsOverride = normalizeAgentAutoLoopMaxRoundsOverride(state.AgentAutoLoopMaxRoundsOverride)
	state.SandboxEscapePolicyOverride = normalizeSandboxEscapePolicyOverride(state.SandboxEscapePolicyOverride)
	state.SkillReviewPolicyOverride = normalizeSkillReviewPolicyOverride(state.SkillReviewPolicyOverride)
	state.ApprovedSkillNames = normalizeApprovedSkillNames(state.ApprovedSkillNames)
	state.TodoItems = normalizeTodoItems(state.TodoItems)
	return state, nil
}

func (s *SessionStore) Save(state SessionState) error {
	if state.FlowID == "" {
		return errors.New("智能体不能为空")
	}
	state.FlowDisplayName = strings.TrimSpace(state.FlowDisplayName)
	state.SessionTitle = strings.TrimSpace(state.SessionTitle)
	state.Workdir = normalizeWorkdir(state.Workdir)
	state.SessionTitleAliases = mergeSessionTitleAliases(readExistingSessionTitleAliases(s.filePath(state.FlowID, state.Workdir)), state.SessionTitleAliases)
	state.LastRunID = strings.TrimSpace(state.LastRunID)
	state.LastMessageID = strings.TrimSpace(state.LastMessageID)
	state.RequestMode = normalizeRequestMode(state.RequestMode)
	if strings.TrimSpace(state.ContextDir) == "" {
		state.ContextDir = state.Workdir
	} else {
		state.ContextDir = normalizeWorkdir(state.ContextDir)
	}
	state.ReasoningOverride = normalizeReasoningOverride(state.ReasoningOverride)
	state.InternetSearchOverride = normalizeInternetSearchOverride(state.InternetSearchOverride)
	state.AgentAutoLoopEnabledOverride = normalizeAgentAutoLoopEnabledOverride(state.AgentAutoLoopEnabledOverride)
	state.AgentAutoLoopMaxRoundsOverride = normalizeAgentAutoLoopMaxRoundsOverride(state.AgentAutoLoopMaxRoundsOverride)
	state.SandboxEscapePolicyOverride = normalizeSandboxEscapePolicyOverride(state.SandboxEscapePolicyOverride)
	state.SkillReviewPolicyOverride = normalizeSkillReviewPolicyOverride(state.SkillReviewPolicyOverride)
	state.ApprovedSkillNames = normalizeApprovedSkillNames(state.ApprovedSkillNames)
	state.TodoItems = normalizeTodoItems(state.TodoItems)
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return fmt.Errorf("创建会话目录失败: %w", err)
	}
	state.UpdatedAt = time.Now()
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化会话失败: %w", err)
	}
	if err := os.WriteFile(s.filePath(state.FlowID, state.Workdir), content, 0o644); err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	return nil
}

func (s *SessionStore) Delete(flowID string, workdir string) error {
	err := os.Remove(s.filePath(flowID, normalizeWorkdir(workdir)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}

func (s *SessionStore) List() ([]SessionState, error) {
	entries, err := os.ReadDir(s.rootDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []SessionState{}, nil
		}
		return nil, fmt.Errorf("读取会话目录失败: %w", err)
	}

	states := make([]SessionState, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		content, readErr := os.ReadFile(filepath.Join(s.rootDir, entry.Name()))
		if readErr != nil {
			return nil, fmt.Errorf("读取会话文件失败: %w", readErr)
		}

		var state SessionState
		if err := json.Unmarshal(content, &state); err != nil {
			return nil, fmt.Errorf("解析会话文件失败: %w", err)
		}
		if strings.TrimSpace(state.FlowID) == "" {
			state.FlowID = strings.TrimSuffix(entry.Name(), ".json")
		}
		state.FlowDisplayName = strings.TrimSpace(state.FlowDisplayName)
		state.SessionTitle = strings.TrimSpace(state.SessionTitle)
		state.SessionTitleAliases = normalizeSessionTitleAliases(state.SessionTitleAliases)
		states = append(states, state)
	}

	sort.Slice(states, func(i, j int) bool {
		return states[i].UpdatedAt.After(states[j].UpdatedAt)
	})

	return states, nil
}

func (s *SessionStore) filePath(flowID string, workdir string) string {
	workdirHash := sha1.Sum([]byte(normalizeWorkdir(workdir)))
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	fileName := fmt.Sprintf("%s-%x.json", replacer.Replace(flowID), workdirHash[:6])
	return filepath.Join(s.rootDir, fileName)
}

func normalizeWorkdir(workdir string) string {
	trimmed := strings.TrimSpace(workdir)
	if trimmed == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "."
		}
		trimmed = cwd
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return trimmed
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err == nil && strings.TrimSpace(resolvedPath) != "" {
		return resolvedPath
	}
	return absPath
}

func normalizeRequestMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "plan":
		return "plan"
	case "agent":
		return "agent"
	default:
		return "ask"
	}
}

func normalizeSandboxEscapePolicyOverride(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return internalconfig.NormalizeSandboxEscapePolicy(trimmed)
}

func normalizeSkillReviewPolicyOverride(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return internalconfig.NormalizeSkillReviewPolicy(trimmed)
}

func normalizeApprovedSkillNames(items []string) []string {
	result := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	return result
}

func readExistingSessionTitleAliases(filePath string) map[string]string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	var state SessionState
	if err := json.Unmarshal(content, &state); err != nil {
		return nil
	}
	return normalizeSessionTitleAliases(state.SessionTitleAliases)
}

func mergeSessionTitleAliases(existing map[string]string, current map[string]string) map[string]string {
	normalizedExisting := normalizeSessionTitleAliases(existing)
	normalizedCurrent := normalizeSessionTitleAliases(current)
	if len(normalizedExisting) == 0 && len(normalizedCurrent) == 0 {
		return nil
	}
	if len(normalizedExisting) == 0 {
		return normalizedCurrent
	}
	if len(normalizedCurrent) == 0 {
		return normalizedExisting
	}
	for key, value := range normalizedCurrent {
		normalizedExisting[key] = value
	}
	return normalizedExisting
}

func normalizeSessionTitleAliases(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(value))
	for key, title := range value {
		trimmedKey := strings.TrimSpace(key)
		trimmedTitle := strings.TrimSpace(title)
		if trimmedKey == "" || trimmedTitle == "" {
			continue
		}
		normalized[trimmedKey] = trimmedTitle
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func normalizeReasoningOverride(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "enable", "enabled":
		return "on"
	case "off", "false", "disable", "disabled":
		return "off"
	default:
		return ""
	}
}

func normalizeInternetSearchOverride(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "enable", "enabled":
		return "on"
	case "off", "false", "disable", "disabled":
		return "off"
	default:
		return ""
	}
}

func normalizeAgentAutoLoopEnabledOverride(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "enable", "enabled":
		return "on"
	case "off", "false", "disable", "disabled":
		return "off"
	default:
		return ""
	}
}

func normalizeAgentAutoLoopMaxRoundsOverride(value *int) *int {
	if value == nil {
		return nil
	}
	if *value < 0 {
		return nil
	}
	normalized := internalconfig.NormalizeAgentAutoLoopMaxRounds(*value)
	return &normalized
}

func normalizeTodoItems(items []TodoItem) []TodoItem {
	result := make([]TodoItem, 0, len(items))
	for _, item := range items {
		title := strings.TrimSpace(item.Title)
		if title == "" {
			continue
		}
		status := normalizeTodoStatus(item.Status)
		result = append(result, TodoItem{
			Title:  title,
			Status: status,
			Notes:  strings.TrimSpace(item.Notes),
		})
	}
	return result
}

func normalizeTodoStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "in_progress", "in-progress":
		return "in_progress"
	case "completed", "done":
		return "completed"
	default:
		return "not_started"
	}
}
