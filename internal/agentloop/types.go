package agentloop

import (
	"encoding/json"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const StateVersion = 1

const (
	IntentNone      = ""
	IntentResume    = "resume"
	IntentUserPause = "user_pause"
	IntentStopped   = "stopped"
)

type RecoveryState struct {
	Version            int             `json:"version"`
	FlowID             string          `json:"flow_id"`
	SessionID          string          `json:"session_id"`
	RunID              string          `json:"run_id"`
	ExecutionAttemptID string          `json:"execution_attempt_id,omitempty"`
	ClientID           string          `json:"client_id,omitempty"`
	DeviceID           string          `json:"device_id,omitempty"`
	Intent             string          `json:"intent,omitempty"`
	ControlAction      string          `json:"control_action,omitempty"`
	ControlRequestID   string          `json:"control_request_id,omitempty"`
	PendingStart       bool            `json:"pending_start,omitempty"`
	CheckpointID       string          `json:"checkpoint_id,omitempty"`
	CheckpointVersion  int             `json:"checkpoint_version,omitempty"`
	Status             string          `json:"status,omitempty"`
	PauseReason        string          `json:"pause_reason,omitempty"`
	RequiresUserResume bool            `json:"requires_user_resume,omitempty"`
	AwaitingResume     bool            `json:"awaiting_resume,omitempty"`
	InvalidatedByUser  bool            `json:"invalidated_by_user,omitempty"`
	Model              string          `json:"model,omitempty"`
	RequestSnapshot    json.RawMessage `json:"request_snapshot,omitempty"`
	Active             bool            `json:"active,omitempty"`
	LastHeartbeat      time.Time       `json:"last_heartbeat,omitempty"`
	RecoveryAttempts   int             `json:"recovery_attempts,omitempty"`
	NextRetryAt        time.Time       `json:"next_retry_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

func (s RecoveryState) AutoResumable() bool {
	return s.Intent == IntentResume && s.AwaitingResume && !s.InvalidatedByUser && s.PauseReason == "network" && !s.RequiresUserResume && s.RunID != "" && s.CheckpointID != "" && s.CheckpointVersion > 0
}

func (s RecoveryState) ResumeRequest() (provider.StreamRequest, error) {
	request := provider.StreamRequest{}
	if len(s.RequestSnapshot) > 0 {
		if err := json.Unmarshal(s.RequestSnapshot, &request); err != nil {
			return provider.StreamRequest{}, err
		}
	}
	request.Input = ""
	request.InputFiles = nil
	request.FlowID = s.FlowID
	request.SessionID = s.SessionID
	request.RunID = s.RunID
	request.RequestType = provider.RequestTypeAgent
	request.ExecutionAttemptID = s.ExecutionAttemptID
	request.ClientID = s.ClientID
	request.DeviceID = s.DeviceID
	request.ExecutionMode = provider.ExecutionModeResume
	request.CheckpointID = s.CheckpointID
	request.CheckpointVersion = s.CheckpointVersion
	request.AfterEventID = ""
	request.SubscriptionID = ""
	request.Model = s.Model
	return request, nil
}

type ObserverState struct {
	Version             int       `json:"version"`
	FlowID              string    `json:"flow_id"`
	SessionID           string    `json:"session_id"`
	RunID               string    `json:"run_id,omitempty"`
	ExecutionAttemptID  string    `json:"execution_attempt_id,omitempty"`
	SubscriptionID      string    `json:"subscription_id,omitempty"`
	AfterEventID        string    `json:"after_event_id,omitempty"`
	NeedsSnapshot       bool      `json:"needs_snapshot,omitempty"`
	Closed              bool      `json:"closed,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures,omitempty"`
	CleanCloses         int       `json:"clean_closes,omitempty"`
	StableConnections   int       `json:"stable_connections,omitempty"`
	PollingFallbacks    int       `json:"polling_fallbacks,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type PendingToolCallback struct {
	Version     int                       `json:"version"`
	Result      provider.ClientCallResult `json:"result"`
	CreatedAt   time.Time                 `json:"created_at"`
	Attempts    int                       `json:"attempts"`
	NextRetryAt time.Time                 `json:"next_retry_at,omitempty"`
	UpdatedAt   time.Time                 `json:"updated_at"`
}
