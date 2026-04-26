package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

type persistedTaskState struct {
	TaskID      string    `json:"task_id"`
	TaskName    string    `json:"task_name"`
	Command     string    `json:"command"`
	CWD         string    `json:"cwd"`
	FlowID      string    `json:"flow_id,omitempty"`
	SessionID   string    `json:"session_id,omitempty"`
	RunID       string    `json:"run_id,omitempty"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	ExitCode    int       `json:"exit_code,omitempty"`
	HasExitCode bool      `json:"has_exit_code,omitempty"`
	LastError   string    `json:"error,omitempty"`
}

func defaultTaskStoreDir() (string, error) {
	rootDir, err := internalconfig.DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(rootDir, "tasks"), nil
}

func taskFilePaths(storeDir string, taskID string) (string, string, string) {
	basePath := filepath.Join(storeDir, strings.TrimSpace(taskID))
	return basePath + ".json", basePath + ".stdout", basePath + ".stderr"
}

func (m *localTaskManager) ensureLoaded(config internalconfig.Config) error {
	storeDir, err := defaultTaskStoreDir()
	if err != nil {
		return fmt.Errorf("解析任务目录失败: %w", err)
	}

	m.mu.RLock()
	loaded := m.loadedRoots[storeDir]
	m.mu.RUnlock()
	if loaded {
		return nil
	}

	entries, err := os.ReadDir(storeDir)
	if err != nil {
		if os.IsNotExist(err) {
			m.mu.Lock()
			m.loadedRoots[storeDir] = true
			m.mu.Unlock()
			return nil
		}
		return fmt.Errorf("读取任务目录失败: %w", err)
	}

	loadedTasks := make([]*localTask, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		task, loadErr := loadPersistedTask(filepath.Join(storeDir, entry.Name()), storeDir)
		if loadErr != nil {
			return loadErr
		}
		loadedTasks = append(loadedTasks, task)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, task := range loadedTasks {
		if _, exists := m.tasks[task.id]; exists {
			continue
		}
		m.tasks[task.id] = task
	}
	m.loadedRoots[storeDir] = true
	return nil
}

func loadPersistedTask(metadataPath string, storeDir string) (*localTask, error) {
	content, err := os.ReadFile(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("读取任务元数据失败: %w", err)
	}
	var state persistedTaskState
	if err := json.Unmarshal(content, &state); err != nil {
		return nil, fmt.Errorf("解析任务元数据失败: %w", err)
	}
	stdoutPath, stderrPath := strings.TrimSuffix(metadataPath, ".json")+".stdout", strings.TrimSuffix(metadataPath, ".json")+".stderr"
	return &localTask{
		id:           strings.TrimSpace(state.TaskID),
		name:         strings.TrimSpace(state.TaskName),
		preview:      strings.TrimSpace(state.Command),
		cwd:          strings.TrimSpace(state.CWD),
		flowID:       strings.TrimSpace(state.FlowID),
		sessionID:    strings.TrimSpace(state.SessionID),
		runID:        strings.TrimSpace(state.RunID),
		startedAt:    state.StartedAt,
		completedAt:  state.CompletedAt,
		status:       strings.TrimSpace(state.Status),
		exitCode:     state.ExitCode,
		hasExitCode:  state.HasExitCode,
		lastError:    strings.TrimSpace(state.LastError),
		storeDir:     storeDir,
		metadataPath: metadataPath,
		stdoutPath:   stdoutPath,
		stderrPath:   stderrPath,
	}, nil
}

func (m *localTaskManager) persistTask(task *localTask) error {
	task.mu.RLock()
	state := persistedTaskState{
		TaskID:      task.id,
		TaskName:    task.name,
		Command:     task.preview,
		CWD:         task.cwd,
		FlowID:      task.flowID,
		SessionID:   task.sessionID,
		RunID:       task.runID,
		Status:      task.status,
		StartedAt:   task.startedAt,
		CompletedAt: task.completedAt,
		ExitCode:    task.exitCode,
		HasExitCode: task.hasExitCode,
		LastError:   task.lastError,
	}
	metadataPath := task.metadataPath
	task.mu.RUnlock()

	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化任务元数据失败: %w", err)
	}
	if err := writeFileAtomically(metadataPath, content); err != nil {
		return fmt.Errorf("写入任务元数据失败: %w", err)
	}
	return nil
}

func (m *localTaskManager) deleteTask(task *localTask) error {
	task.mu.RLock()
	metadataPath := task.metadataPath
	stdoutPath := task.stdoutPath
	stderrPath := task.stderrPath
	taskID := task.id
	task.mu.RUnlock()

	for _, path := range []string{metadataPath, stdoutPath, stderrPath} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	m.mu.Lock()
	delete(m.tasks, taskID)
	m.mu.Unlock()
	return nil
}

func (m *localTaskManager) tasksForStore(config internalconfig.Config) ([]*localTask, error) {
	if err := m.ensureLoaded(config); err != nil {
		return nil, err
	}
	storeDir, err := defaultTaskStoreDir()
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	tasks := make([]*localTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		if task.storeDir != storeDir {
			continue
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(left, right int) bool {
		return tasks[left].startedAt.After(tasks[right].startedAt)
	})
	return tasks, nil
}

func (task *localTask) readPersistedOutput(stderr bool, maxBytes int) (string, bool) {
	task.mu.RLock()
	filePath := task.stdoutPath
	fallbackContent := task.stdout.String()
	if stderr {
		filePath = task.stderrPath
		fallbackContent = task.stderr.String()
	}
	task.mu.RUnlock()

	if strings.TrimSpace(filePath) != "" {
		content, err := os.ReadFile(filePath)
		if err == nil {
			trimmed, truncated := truncateUTF8(string(content), maxBytes)
			return trimmed, truncated
		}
	}
	trimmed, truncated := truncateUTF8(fallbackContent, maxBytes)
	return trimmed, truncated
}

func (task *localTask) appendOutputFile(content []byte, stderr bool) {
	filePath := task.stdoutPath
	if stderr {
		filePath = task.stderrPath
	}
	if strings.TrimSpace(filePath) == "" {
		return
	}
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(content)
}
