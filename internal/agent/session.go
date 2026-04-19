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
	FlowID    string    `json:"flow_id"`
	SessionID string    `json:"session_id"`
	Workdir   string    `json:"workdir,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
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
	if strings.TrimSpace(state.Workdir) == "" {
		state.Workdir = normalizedWorkdir
	}
	return state, nil
}

func (s *SessionStore) Save(state SessionState) error {
	if state.FlowID == "" {
		return errors.New("flow_id 不能为空")
	}
	state.Workdir = normalizeWorkdir(state.Workdir)
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
