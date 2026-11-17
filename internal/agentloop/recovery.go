package agentloop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const (
	recoveryClaimTTL   = 2 * time.Minute
	notificationTTL    = 5 * time.Minute
	notificationMaxAge = 7 * 24 * time.Hour
)

type RecoveryClaim struct {
	store *Store
	path  string
	owner string
}

func (c *RecoveryClaim) Release() error {
	if c == nil || c.store == nil {
		return nil
	}
	return c.store.releaseFile(c.path, c.owner)
}

type NotificationClaim struct {
	store             *Store
	path              string
	owner             string
	NotificationID    string
	CheckpointID      string
	CheckpointVersion int
}

func (c *NotificationClaim) Complete() error {
	if c == nil || c.store == nil {
		return nil
	}
	var claim fileClaim
	if err := readJSON(c.path, &claim); err != nil {
		return err
	}
	if claim.Owner != c.owner {
		return ErrClaimed
	}
	claim.Status = "done"
	claim.UpdatedAt = c.store.now().UTC()
	return writeJSONAtomic(c.path, claim)
}

func (c *NotificationClaim) Release() error {
	if c == nil || c.store == nil {
		return nil
	}
	return c.store.releaseFile(c.path, c.owner)
}

func (s *Store) recoveryPath(sessionID string) string {
	return s.path("recovery", stateKey(sessionID)+".json")
}

func (s *Store) HasRecoveryStates() (bool, error) {
	entries, err := os.ReadDir(s.path("recovery"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			return true, nil
		}
	}
	return false, nil
}

func (s *Store) LoadRecovery(sessionID string) (RecoveryState, error) {
	sessionID = strings.TrimSpace(sessionID)
	state := RecoveryState{Version: StateVersion, SessionID: sessionID}
	if sessionID == "" {
		return state, fmt.Errorf("session_id is required")
	}
	if err := readJSON(s.recoveryPath(sessionID), &state); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RecoveryState{Version: StateVersion, SessionID: sessionID}, nil
		}
		return RecoveryState{}, err
	}
	if err := validateVersion(state.Version); err != nil {
		return RecoveryState{}, err
	}
	return state, nil
}

func (s *Store) UpdateRecovery(sessionID string, update func(*RecoveryState) error) (RecoveryState, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return RecoveryState{}, fmt.Errorf("session_id is required")
	}
	unlock := s.lock("recovery:" + sessionID)
	defer unlock()
	path := s.recoveryPath(sessionID)
	lockPath := path + ".lock"
	owner, acquired, err := s.acquireFile(lockPath, 30*time.Second, "state")
	if err != nil {
		return RecoveryState{}, err
	}
	if !acquired {
		return RecoveryState{}, ErrClaimed
	}
	defer s.releaseFile(lockPath, owner)
	state, err := s.LoadRecovery(sessionID)
	if err != nil {
		return RecoveryState{}, err
	}
	createdAt := state.CreatedAt
	if createdAt.IsZero() {
		createdAt = s.now().UTC()
	}
	if err := update(&state); err != nil {
		return RecoveryState{}, err
	}
	state.Version = StateVersion
	state.SessionID = sessionID
	state.CreatedAt = createdAt
	state.UpdatedAt = s.now().UTC()
	if state.FlowID == "" {
		return RecoveryState{}, fmt.Errorf("flow_id is required")
	}
	if err := writeJSONAtomic(path, state); err != nil {
		return RecoveryState{}, err
	}
	return state, nil
}

func (s *Store) ClearRecovery(sessionID string, runID string) error {
	sessionID = strings.TrimSpace(sessionID)
	unlock := s.lock("recovery:" + sessionID)
	defer unlock()
	path := s.recoveryPath(sessionID)
	lockPath := path + ".lock"
	owner, acquired, err := s.acquireFile(lockPath, 30*time.Second, "state")
	if err != nil {
		return err
	}
	if !acquired {
		return ErrClaimed
	}
	defer s.releaseFile(lockPath, owner)
	state, err := s.LoadRecovery(sessionID)
	if err != nil {
		return err
	}
	if runID != "" && state.RunID != "" && state.RunID != strings.TrimSpace(runID) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Store) RecordPendingStart(flowID, sessionID, runID, clientID, deviceID string) (RecoveryState, error) {
	return s.RecordPendingStartRequest(provider.StreamRequest{FlowID: flowID, SessionID: sessionID, RunID: runID, ClientID: clientID, DeviceID: deviceID})
}

func (s *Store) RecordPendingStartRequest(request provider.StreamRequest) (RecoveryState, error) {
	snapshot := request
	snapshot.Input = ""
	snapshot.InputFiles = nil
	snapshot.ExecutionMode = ""
	snapshot.CheckpointID = ""
	snapshot.CheckpointVersion = 0
	snapshot.AfterEventID = ""
	snapshot.SubscriptionID = ""
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return RecoveryState{}, fmt.Errorf("保存执行能力快照失败: %w", err)
	}
	return s.UpdateRecovery(request.SessionID, func(state *RecoveryState) error {
		state.FlowID = strings.TrimSpace(request.FlowID)
		state.RunID = strings.TrimSpace(request.RunID)
		state.ExecutionAttemptID = strings.TrimSpace(request.ExecutionAttemptID)
		state.ClientID = strings.TrimSpace(request.ClientID)
		state.DeviceID = strings.TrimSpace(request.DeviceID)
		state.Intent = IntentNone
		state.ControlAction = ""
		state.ControlRequestID = ""
		state.PendingStart = true
		state.Active = true
		state.LastHeartbeat = s.now().UTC()
		state.AwaitingResume = false
		state.InvalidatedByUser = false
		state.CheckpointID = ""
		state.CheckpointVersion = 0
		state.Model = strings.TrimSpace(request.Model)
		state.RequestSnapshot = encoded
		state.RecoveryAttempts = 0
		state.NextRetryAt = time.Time{}
		return nil
	})
}

func (s *Store) RecordAcceptedExecution(sessionID, runID, attemptID string) (RecoveryState, error) {
	return s.UpdateRecovery(sessionID, func(state *RecoveryState) error {
		if state.RunID != "" && runID != "" && state.RunID != strings.TrimSpace(runID) {
			return nil
		}
		if runID != "" {
			state.RunID = strings.TrimSpace(runID)
		}
		if attemptID != "" {
			state.ExecutionAttemptID = strings.TrimSpace(attemptID)
		}
		state.PendingStart = false
		state.Active = true
		state.LastHeartbeat = s.now().UTC()
		state.RecoveryAttempts = 0
		if state.Intent != IntentUserPause && state.Intent != IntentStopped {
			state.Intent = IntentNone
			state.AwaitingResume = false
		}
		return nil
	})
}

func (s *Store) SetResumeIntent(checkpoint provider.AgentLoopCheckpoint) (RecoveryState, error) {
	if checkpoint.ID == "" || checkpoint.Version <= 0 || checkpoint.RunID == "" {
		return RecoveryState{}, fmt.Errorf("checkpoint identity is incomplete")
	}
	return s.UpdateRecovery(checkpoint.SessionID, func(state *RecoveryState) error {
		if state.InvalidatedByUser || state.Intent == IntentStopped || state.Intent == IntentUserPause {
			return nil
		}
		if state.RunID != "" && state.RunID != checkpoint.RunID {
			return fmt.Errorf("checkpoint run does not match active run")
		}
		state.FlowID = checkpoint.FlowID
		state.RunID = checkpoint.RunID
		state.ClientID = checkpoint.OriginClientID
		state.DeviceID = checkpoint.OriginDeviceID
		state.Intent = IntentResume
		state.PendingStart = false
		state.CheckpointID = checkpoint.ID
		state.CheckpointVersion = checkpoint.Version
		state.Status = checkpoint.Status
		state.PauseReason = checkpoint.PauseReason
		state.RequiresUserResume = checkpoint.RequiresUserResume
		state.Active = false
		state.AwaitingResume = true
		return nil
	})
}

func (s *Store) RecordCheckpointState(checkpoint provider.AgentLoopCheckpoint) (RecoveryState, error) {
	if checkpoint.ID == "" || checkpoint.Version <= 0 || checkpoint.RunID == "" || checkpoint.SessionID == "" {
		return RecoveryState{}, fmt.Errorf("checkpoint identity is incomplete")
	}
	return s.UpdateRecovery(checkpoint.SessionID, func(state *RecoveryState) error {
		if state.RunID != "" && state.RunID != checkpoint.RunID {
			return nil
		}
		state.FlowID = checkpoint.FlowID
		state.RunID = checkpoint.RunID
		state.CheckpointID = checkpoint.ID
		state.CheckpointVersion = checkpoint.Version
		state.Status = checkpoint.Status
		state.PauseReason = checkpoint.PauseReason
		state.RequiresUserResume = checkpoint.RequiresUserResume
		state.Active = false
		state.PendingStart = false
		if state.Intent == IntentStopped || state.Intent == IntentUserPause {
			return nil
		}
		if checkpoint.PauseReason == "network" && !checkpoint.RequiresUserResume {
			state.Intent = IntentResume
			state.AwaitingResume = true
		} else if checkpoint.PauseReason == "user" || checkpoint.RequiresUserResume {
			state.Intent = IntentUserPause
			state.AwaitingResume = false
			state.InvalidatedByUser = true
		}
		return nil
	})
}

func (s *Store) SetUserControlIntent(sessionID, runID, intent string) (RecoveryState, error) {
	if intent != IntentUserPause && intent != IntentStopped {
		return RecoveryState{}, fmt.Errorf("invalid user control intent")
	}
	return s.UpdateRecovery(sessionID, func(state *RecoveryState) error {
		if runID != "" && state.RunID != "" && state.RunID != runID {
			return nil
		}
		state.Intent = intent
		state.PendingStart = false
		state.Active = false
		state.AwaitingResume = false
		state.InvalidatedByUser = true
		state.NextRetryAt = time.Time{}
		return nil
	})
}

func (s *Store) ClearResumeIntent(sessionID, runID string) (RecoveryState, error) {
	return s.UpdateRecovery(sessionID, func(state *RecoveryState) error {
		if runID != "" && state.RunID != "" && state.RunID != runID {
			return nil
		}
		state.Intent = IntentNone
		state.PendingStart = false
		state.AwaitingResume = false
		state.InvalidatedByUser = false
		state.CheckpointID = ""
		state.CheckpointVersion = 0
		state.Status = ""
		state.PauseReason = ""
		state.RequiresUserResume = false
		state.NextRetryAt = time.Time{}
		return nil
	})
}

func (s *Store) AuthorizeExplicitResume(sessionID, runID string) (RecoveryState, error) {
	return s.UpdateRecovery(sessionID, func(state *RecoveryState) error {
		if runID != "" && state.RunID != "" && state.RunID != runID {
			return fmt.Errorf("resume run does not match active run")
		}
		state.Intent = IntentNone
		state.InvalidatedByUser = false
		state.AwaitingResume = false
		state.Active = false
		return nil
	})
}

func (s *Store) Heartbeat(sessionID, runID string) (RecoveryState, error) {
	return s.UpdateRecovery(sessionID, func(state *RecoveryState) error {
		if state.RunID != runID || (!state.Active && !state.PendingStart) {
			return nil
		}
		state.LastHeartbeat = s.now().UTC()
		state.UpdatedAt = s.now().UTC()
		return nil
	})
}

func (s *Store) MarkDisconnected(sessionID, runID string) (RecoveryState, error) {
	return s.UpdateRecovery(sessionID, func(state *RecoveryState) error {
		if state.RunID != runID || state.Intent == IntentStopped || state.Intent == IntentUserPause {
			return nil
		}
		state.Active = false
		state.PendingStart = false
		state.Intent = IntentResume
		state.AwaitingResume = true
		state.Status = "network_freezing"
		state.PauseReason = "network"
		state.RequiresUserResume = false
		return nil
	})
}

func (s *Store) MarkTerminal(sessionID, runID string) (RecoveryState, error) {
	return s.UpdateRecovery(sessionID, func(state *RecoveryState) error {
		if runID != "" && state.RunID != "" && state.RunID != runID {
			return nil
		}
		state.Active = false
		state.PendingStart = false
		state.AwaitingResume = false
		state.Intent = IntentNone
		state.CheckpointID = ""
		state.CheckpointVersion = 0
		state.NextRetryAt = time.Time{}
		return nil
	})
}

func (s *Store) ScheduleRecoveryRetry(sessionID, runID string, retryAt time.Time) (RecoveryState, error) {
	return s.UpdateRecovery(sessionID, func(state *RecoveryState) error {
		if state.RunID != runID || state.Intent != IntentResume {
			return nil
		}
		state.AwaitingResume = true
		state.RecoveryAttempts++
		state.NextRetryAt = retryAt.UTC()
		return nil
	})
}

func (s *Store) ClaimRecovery(checkpointID string, version int) (*RecoveryClaim, bool, error) {
	if checkpointID == "" || version <= 0 {
		return nil, false, fmt.Errorf("checkpoint identity is incomplete")
	}
	path := s.path("claims", fmt.Sprintf("%s-%d.claim", stateKey(checkpointID), version))
	owner, acquired, err := s.acquireFile(path, recoveryClaimTTL, "recovery")
	if err != nil || !acquired {
		return nil, acquired, err
	}
	return &RecoveryClaim{store: s, path: path, owner: owner}, true, nil
}

func (s *Store) BeginNotification(sessionID, notificationID string) (*NotificationClaim, bool, error) {
	if notificationID == "" {
		return nil, false, fmt.Errorf("notification_id is required")
	}
	path := s.path("notifications", stateKey(sessionID, notificationID)+".json")
	owner, acquired, err := s.acquireFile(path, notificationTTL, "pending")
	if err != nil || !acquired {
		return nil, acquired, err
	}
	return &NotificationClaim{store: s, path: path, owner: owner, NotificationID: notificationID}, true, nil
}

func (s *Store) PruneNotifications() error {
	entries, err := os.ReadDir(s.path("notifications"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	cutoff := s.now().Add(-notificationMaxAge)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(s.path("notifications", entry.Name()))
		}
	}
	return nil
}
