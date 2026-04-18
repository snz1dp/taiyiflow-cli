package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	internalconfig "github.com/taiyiflow/taiyiflow-cli/internal/config"
)

type SessionState struct {
	FlowID    string    `json:"flow_id"`
	SessionID string    `json:"session_id"`
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

func (s *SessionStore) Load(flowID string) (SessionState, error) {
	content, err := os.ReadFile(s.filePath(flowID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SessionState{FlowID: flowID}, nil
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
	return state, nil
}

func (s *SessionStore) Save(state SessionState) error {
	if state.FlowID == "" {
		return errors.New("flow_id 不能为空")
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return fmt.Errorf("创建会话目录失败: %w", err)
	}
	state.UpdatedAt = time.Now()
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化会话失败: %w", err)
	}
	if err := os.WriteFile(s.filePath(state.FlowID), content, 0o644); err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	return nil
}

func (s *SessionStore) Delete(flowID string) error {
	err := os.Remove(s.filePath(flowID))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}

func (s *SessionStore) filePath(flowID string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")
	return filepath.Join(s.rootDir, replacer.Replace(flowID)+".json")
}
