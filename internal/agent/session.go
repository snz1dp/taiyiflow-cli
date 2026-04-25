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
	FlowID          string    `json:"flow_id"`
	FlowDisplayName string    `json:"flow_display_name,omitempty"`
	SessionID       string    `json:"session_id"`
	SessionTitle    string    `json:"session_title,omitempty"`
	Workdir         string    `json:"workdir,omitempty"`
	RequestMode     string    `json:"request_mode,omitempty"`
	ContextDir      string    `json:"context_dir,omitempty"`
	SandboxEscapePolicyOverride string `json:"sandbox_escape_policy_override,omitempty"`
	TodoItems       []TodoItem `json:"todo_items,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type TodoItem struct {
	Title  string `json:"title"`
	Status string `json:"status"`
	Notes  string `json:"notes,omitempty"`
}

type SessionStore struct {
	rootDir string
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
	if strings.TrimSpace(state.Workdir) == "" {
		state.Workdir = normalizedWorkdir
	}
	state.RequestMode = normalizeRequestMode(state.RequestMode)
	if strings.TrimSpace(state.ContextDir) == "" {
		state.ContextDir = normalizedWorkdir
	} else {
		state.ContextDir = normalizeWorkdir(state.ContextDir)
	}
	state.SandboxEscapePolicyOverride = normalizeSandboxEscapePolicyOverride(state.SandboxEscapePolicyOverride)
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
	state.RequestMode = normalizeRequestMode(state.RequestMode)
	if strings.TrimSpace(state.ContextDir) == "" {
		state.ContextDir = state.Workdir
	} else {
		state.ContextDir = normalizeWorkdir(state.ContextDir)
	}
	state.SandboxEscapePolicyOverride = normalizeSandboxEscapePolicyOverride(state.SandboxEscapePolicyOverride)
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
