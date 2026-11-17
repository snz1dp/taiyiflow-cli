package agentloop

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

var callbackRetryDelays = []time.Duration{5 * time.Second, 15 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute}

// maxToolCallbackAttempts 是单条工具结果的最大补报次数，超过后放弃，避免坏记录永久占用队列。
const maxToolCallbackAttempts = 20

func (s *Store) callbackPath(flowID, sessionID, runID, callID string) string {
	return s.path("callbacks", stateKey(flowID, sessionID, runID, callID)+".json")
}

func validateCallback(result provider.ClientCallResult) error {
	if strings.TrimSpace(result.FlowID) == "" || strings.TrimSpace(result.SessionID) == "" || strings.TrimSpace(result.RunID) == "" || strings.TrimSpace(result.CallID) == "" {
		return fmt.Errorf("tool callback identity is incomplete")
	}
	return nil
}

func (s *Store) EnqueueToolCallback(result provider.ClientCallResult, createdAt time.Time) (PendingToolCallback, error) {
	if err := validateCallback(result); err != nil {
		return PendingToolCallback{}, err
	}
	key := stateKey(result.FlowID, result.SessionID, result.RunID, result.CallID)
	unlock := s.lock("callback:" + key)
	defer unlock()
	path := s.callbackPath(result.FlowID, result.SessionID, result.RunID, result.CallID)
	pending := PendingToolCallback{Version: StateVersion, Result: result, CreatedAt: createdAt.UTC(), UpdatedAt: s.now().UTC()}
	if pending.CreatedAt.IsZero() {
		pending.CreatedAt = pending.UpdatedAt
	}
	if err := readJSON(path, &pending); err == nil {
		if err := validateVersion(pending.Version); err != nil {
			return PendingToolCallback{}, err
		}
		pending.Result = result
		pending.UpdatedAt = s.now().UTC()
	} else if !errors.Is(err, os.ErrNotExist) {
		return PendingToolCallback{}, err
	}
	if err := writeJSONAtomic(path, pending); err != nil {
		return PendingToolCallback{}, err
	}
	return pending, nil
}

func (s *Store) LoadToolCallback(flowID, sessionID, runID, callID string) (PendingToolCallback, error) {
	var pending PendingToolCallback
	if err := readJSON(s.callbackPath(flowID, sessionID, runID, callID), &pending); err != nil {
		return PendingToolCallback{}, err
	}
	if err := validateVersion(pending.Version); err != nil {
		return PendingToolCallback{}, err
	}
	return pending, nil
}

func (s *Store) ListDueToolCallbacks(now time.Time) ([]PendingToolCallback, error) {
	entries, err := os.ReadDir(s.path("callbacks"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	callbacks := make([]PendingToolCallback, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var pending PendingToolCallback
		if err := readJSON(s.path("callbacks", entry.Name()), &pending); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				s.quarantineCallbackFile(entry.Name())
			}
			continue
		}
		if err := validateVersion(pending.Version); err != nil || validateCallback(pending.Result) != nil {
			s.quarantineCallbackFile(entry.Name())
			continue
		}
		if !pending.NextRetryAt.IsZero() && pending.NextRetryAt.After(now) {
			continue
		}
		callbacks = append(callbacks, pending)
	}
	sort.Slice(callbacks, func(i, j int) bool {
		return callbacks[i].CreatedAt.Before(callbacks[j].CreatedAt)
	})
	return callbacks, nil
}

func (s *Store) MarkToolCallbackAttempt(flowID, sessionID, runID, callID string) (PendingToolCallback, error) {
	key := stateKey(flowID, sessionID, runID, callID)
	unlock := s.lock("callback:" + key)
	defer unlock()
	pending, err := s.LoadToolCallback(flowID, sessionID, runID, callID)
	if err != nil {
		return PendingToolCallback{}, err
	}
	pending.Attempts++
	delay := callbackRetryDelays[len(callbackRetryDelays)-1]
	if pending.Attempts <= len(callbackRetryDelays) {
		delay = callbackRetryDelays[pending.Attempts-1]
	}
	pending.NextRetryAt = s.now().Add(delay).UTC()
	pending.UpdatedAt = s.now().UTC()
	if err := writeJSONAtomic(s.callbackPath(flowID, sessionID, runID, callID), pending); err != nil {
		return PendingToolCallback{}, err
	}
	return pending, nil
}

func (s *Store) RemoveToolCallback(flowID, sessionID, runID, callID string) error {
	key := stateKey(flowID, sessionID, runID, callID)
	unlock := s.lock("callback:" + key)
	defer unlock()
	if err := os.Remove(s.callbackPath(flowID, sessionID, runID, callID)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// quarantineCallbackFile 把无法解析或身份不全的记录移出补报队列（改名保留原文件供排查），
// 否则坏记录会被每次列举静默跳过、永久占用磁盘。
func (s *Store) quarantineCallbackFile(name string) {
	path := s.path("callbacks", name)
	_ = os.Rename(path, path+".corrupt")
}
