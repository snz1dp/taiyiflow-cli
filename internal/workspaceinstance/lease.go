package workspaceinstance

import (
	cryptorand "crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	workspaceStateDirName = ".snz1dp"
	instanceStateFileName = "cli-instance.json"
	instanceLockFileName  = "cli-instance.lock"
)

type stateFile struct {
	InstanceID string `json:"instance_id"`
	Workdir    string `json:"workdir,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type Lease struct {
	Workdir    string
	InstanceID string

	lockFile  *os.File
	lockPath  string
	statePath string
}

func Acquire(workdir string) (*Lease, error) {
	resolvedWorkdir, err := resolveWorkdir(workdir)
	if err != nil {
		return nil, err
	}

	stateDir := filepath.Join(resolvedWorkdir, workspaceStateDirName)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建工作区实例目录失败: %w", err)
	}

	lockPath := filepath.Join(stateDir, instanceLockFileName)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("打开工作区实例锁文件失败: %w", err)
	}
	if err := acquireFileLock(lockFile); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("工作区 %s 已有活跃 CLI 实例: %w", resolvedWorkdir, err)
	}

	lease := &Lease{
		Workdir:   resolvedWorkdir,
		lockFile:  lockFile,
		lockPath:  lockPath,
		statePath: filepath.Join(stateDir, instanceStateFileName),
	}
	if err := lease.loadOrCreateState(); err != nil {
		_ = lease.Close()
		return nil, err
	}
	return lease, nil
}

func (l *Lease) Close() error {
	if l == nil || l.lockFile == nil {
		return nil
	}
	unlockErr := releaseFileLock(l.lockFile)
	closeErr := l.lockFile.Close()
	l.lockFile = nil
	if unlockErr != nil {
		return fmt.Errorf("释放工作区实例锁失败: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭工作区实例锁文件失败: %w", closeErr)
	}
	return nil
}

func (l *Lease) loadOrCreateState() error {
	state, err := readStateFile(l.statePath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(state.InstanceID) == "" {
		instanceID, err := generateInstanceID()
		if err != nil {
			return err
		}
		state.InstanceID = instanceID
	}
	if strings.TrimSpace(state.CreatedAt) == "" {
		state.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	state.Workdir = l.Workdir
	if err := writeStateFile(l.statePath, state); err != nil {
		return err
	}
	l.InstanceID = strings.TrimSpace(state.InstanceID)
	return nil
}

func resolveWorkdir(workdir string) (string, error) {
	trimmedWorkdir := strings.TrimSpace(workdir)
	if trimmedWorkdir == "" {
		return "", fmt.Errorf("工作区目录不能为空")
	}
	resolvedWorkdir, err := filepath.Abs(trimmedWorkdir)
	if err != nil {
		return "", fmt.Errorf("解析工作区目录失败: %w", err)
	}
	if symlinkResolved, err := filepath.EvalSymlinks(resolvedWorkdir); err == nil && strings.TrimSpace(symlinkResolved) != "" {
		resolvedWorkdir = symlinkResolved
	}
	info, err := os.Stat(resolvedWorkdir)
	if err != nil {
		return "", fmt.Errorf("读取工作区目录失败: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("工作区路径不是目录: %s", resolvedWorkdir)
	}
	return resolvedWorkdir, nil
}

func readStateFile(statePath string) (stateFile, error) {
	payload, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return stateFile{}, nil
		}
		return stateFile{}, fmt.Errorf("读取工作区实例状态失败: %w", err)
	}
	state := stateFile{}
	if err := json.Unmarshal(payload, &state); err != nil {
		return stateFile{}, fmt.Errorf("解析工作区实例状态失败: %w", err)
	}
	return state, nil
}

func writeStateFile(statePath string, state stateFile) error {
	encoded, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("编码工作区实例状态失败: %w", err)
	}
	tempPath := statePath + ".tmp"
	if err := os.WriteFile(tempPath, append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("写入工作区实例状态失败: %w", err)
	}
	if err := os.Rename(tempPath, statePath); err != nil {
		return fmt.Errorf("提交工作区实例状态失败: %w", err)
	}
	return nil
}

func generateInstanceID() (string, error) {
	raw := make([]byte, 16)
	if _, err := io.ReadFull(cryptorand.Reader, raw); err != nil {
		return "", fmt.Errorf("生成工作区实例标识失败: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("instance-%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}