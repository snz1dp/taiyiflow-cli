package agentloop

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

var ErrClaimed = errors.New("agentloop operation already claimed")
var ErrStateVersion = errors.New("unsupported agentloop state version")

type Store struct {
	root string
	now  func() time.Time

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

type fileClaim struct {
	Owner     string    `json:"owner"`
	Status    string    `json:"status,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func DefaultStore() (*Store, error) {
	root, err := internalconfig.DefaultRootDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(root, "agentloop")), nil
}

func NewStore(root string) *Store {
	return &Store{root: root, now: time.Now, locks: map[string]*sync.Mutex{}}
}

func (s *Store) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

func (s *Store) Now() time.Time {
	return s.now()
}

func (s *Store) lock(key string) func() {
	s.locksMu.Lock()
	mutex := s.locks[key]
	if mutex == nil {
		mutex = &sync.Mutex{}
		s.locks[key] = mutex
	}
	s.locksMu.Unlock()
	mutex.Lock()
	return mutex.Unlock
}

func (s *Store) path(parts ...string) string {
	items := append([]string{s.root}, parts...)
	return filepath.Join(items...)
}

func stateKey(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:])
}

func validateVersion(version int) error {
	if version != StateVersion {
		return fmt.Errorf("%w: %d", ErrStateVersion, version)
	}
	return nil
}

func readJSON(path string, target any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(content, target); err != nil {
		return fmt.Errorf("parse agentloop state: %w", err)
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create agentloop state directory: %w", err)
	}
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode agentloop state: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create agentloop temporary state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect agentloop state: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return fmt.Errorf("write agentloop state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync agentloop state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close agentloop state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace agentloop state: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect agentloop state: %w", err)
	}
	return nil
}

func randomOwner() (string, error) {
	content := make([]byte, 16)
	if _, err := rand.Read(content); err != nil {
		return "", err
	}
	return hex.EncodeToString(content), nil
}

func (s *Store) acquireFile(path string, staleAfter time.Duration, status string) (string, bool, error) {
	unlock, err := lockStoreFile(path)
	if err != nil {
		return "", false, err
	}
	defer unlock()
	owner, err := randomOwner()
	if err != nil {
		return "", false, fmt.Errorf("create agentloop claim owner: %w", err)
	}
	now := s.now().UTC()
	claim := fileClaim{Owner: owner, Status: status, CreatedAt: now, UpdatedAt: now}
	content, err := json.Marshal(claim)
	if err != nil {
		return "", false, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return "", false, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if _, err := file.Write(content); err != nil {
				file.Close()
				os.Remove(path)
				return "", false, err
			}
			if err := file.Close(); err != nil {
				os.Remove(path)
				return "", false, err
			}
			return owner, true, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", false, err
		}
		var existing fileClaim
		if readErr := readJSON(path, &existing); readErr == nil && existing.Status == "done" {
			return "", false, nil
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return "", false, statErr
		}
		if s.now().Sub(info.ModTime()) <= staleAfter {
			return "", false, nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", false, err
		}
	}
	return "", false, nil
}

func (s *Store) releaseFile(path string, owner string) error {
	unlock, err := lockStoreFile(path)
	if err != nil {
		return err
	}
	defer unlock()
	var claim fileClaim
	if err := readJSON(path, &claim); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if claim.Owner != owner {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
