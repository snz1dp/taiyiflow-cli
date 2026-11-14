package agentloop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	mathrand "math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

var (
	ErrSubmissionUncertain    = errors.New("agentloop submission outcome is uncertain")
	ErrNoResumableCheckpoint  = errors.New("no resumable agentloop checkpoint")
	ErrAlreadyRunning         = errors.New("agentloop is already running")
	ErrUserControlInvalidated = errors.New("agentloop recovery was invalidated by user")
)

const (
	primaryActiveTTL    = 2 * time.Minute
	maxRecoveryAttempts = 8
)

type EventInfo struct {
	Name               string
	EventID            string
	SessionID          string
	RunID              string
	ExecutionAttemptID string
	CheckpointID       string
	CheckpointVersion  int
	Status             string
	PauseReason        string
	RequiresUserResume bool
	Accepted           bool
	ResumeReady        bool
	NetworkFreeze      bool
	CheckpointCreated  bool
	Terminal           bool
	RunFinished        bool
	ResyncRequired     bool
	RunSnapshot        bool
	RetryAfter         time.Duration
}

func ClassifyEvent(event provider.StreamEvent) EventInfo {
	info := EventInfo{
		Name:               stringValue(event.Data, "name"),
		EventID:            event.EventID(),
		SessionID:          stringValue(event.Data, "session_id"),
		RunID:              stringValue(event.Data, "run_id"),
		ExecutionAttemptID: stringValue(event.Data, "execution_attempt_id"),
		CheckpointID:       stringValue(event.Data, "checkpoint_id"),
		CheckpointVersion:  intValue(event.Data, "checkpoint_version"),
		Status:             firstValue(event.Data, "run_status", "checkpoint_status", "status"),
		PauseReason:        stringValue(event.Data, "pause_reason"),
		RequiresUserResume: boolValue(event.Data, "requires_user_resume"),
		RunFinished:        boolValue(event.Data, "run_finished"),
		RetryAfter:         durationValue(event.Data, "retry_after"),
	}
	if info.Name == "" {
		info.Name = string(event.Type)
	}
	switch info.Name {
	case "start-flow":
		info.Accepted = true
	case "resume-flow":
		info.Accepted = true
		info.ResumeReady = true
	case "agent_loop_checkpoint_created":
		info.CheckpointCreated = true
	case "agent_loop_network_freeze_requested", "network_freezing":
		info.NetworkFreeze = true
	case "resync_required":
		info.ResyncRequired = true
	case "run_snapshot":
		info.RunSnapshot = true
	case "close", "end-flow", "agent_loop_cancelled":
		info.Terminal = true
	}
	if info.Status == "network_freezing" || stringValue(event.Data, "code") == "network_freezing" {
		info.NetworkFreeze = true
	}
	return info
}

type Controller struct {
	Provider provider.Provider
	Store    *Store
	Sleep    func(context.Context, time.Duration) error
}

func NewController(providerClient provider.Provider, store *Store) *Controller {
	return &Controller{Provider: providerClient, Store: store, Sleep: sleepContext}
}

func NewIdentifier() string {
	content := make([]byte, 16)
	if _, err := rand.Read(content); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(content)
}

func (c *Controller) PrepareStart(request provider.StreamRequest) (provider.StreamRequest, error) {
	if provider.NormalizeRequestType(request.RequestType) != provider.RequestTypeAgent {
		return request, nil
	}
	request.RequestType = provider.RequestTypeAgent
	request.ExecutionMode = provider.ExecutionModeStart
	request.CheckpointID = ""
	request.CheckpointVersion = 0
	request.AfterEventID = ""
	request.SubscriptionID = ""
	if strings.TrimSpace(request.ExecutionAttemptID) == "" {
		request.ExecutionAttemptID = NewIdentifier()
	}
	if request.FlowID == "" || request.SessionID == "" || request.RunID == "" {
		return provider.StreamRequest{}, fmt.Errorf("AgentLoop start 缺少 flow/session/run 标识")
	}
	if _, err := c.Store.RecordPendingStartRequest(request); err != nil {
		return provider.StreamRequest{}, fmt.Errorf("保存 AgentLoop 发起状态失败: %w", err)
	}
	return request, nil
}

func (c *Controller) ObserveEvent(event provider.StreamEvent) (EventInfo, error) {
	info := ClassifyEvent(event)
	if info.SessionID == "" {
		return info, nil
	}
	if info.Accepted {
		_, err := c.Store.RecordAcceptedExecution(info.SessionID, info.RunID, info.ExecutionAttemptID)
		return info, err
	}
	if info.CheckpointCreated {
		if info.PauseReason == "user" || info.RequiresUserResume {
			if _, err := c.Store.SetUserControlIntent(info.SessionID, info.RunID, IntentUserPause); err != nil {
				return info, err
			}
		}
		_, err := c.Store.UpdateRecovery(info.SessionID, func(state *RecoveryState) error {
			if state.RunID != "" && info.RunID != "" && state.RunID != info.RunID {
				return nil
			}
			state.Active = false
			state.CheckpointID = info.CheckpointID
			if info.CheckpointVersion > 0 {
				state.CheckpointVersion = info.CheckpointVersion
			}
			state.Status = info.Status
			state.PauseReason = info.PauseReason
			state.RequiresUserResume = info.RequiresUserResume
			if state.Intent != IntentUserPause && state.Intent != IntentStopped &&
				info.PauseReason == "network" && !info.RequiresUserResume {
				state.Intent = IntentResume
				state.AwaitingResume = true
			}
			return nil
		})
		return info, err
	}
	if info.NetworkFreeze {
		_, err := c.Store.MarkDisconnected(info.SessionID, info.RunID)
		return info, err
	}
	if info.Terminal {
		if c.TerminalIsResumableFreeze(info.SessionID, info) {
			return info, nil
		}
		return info, c.finalizeTerminal(info.SessionID, info.RunID, info)
	}
	return info, nil
}

func terminalIsResumable(state RecoveryState, info EventInfo) bool {
	return !info.RunFinished && state.AutoResumable()
}

// TerminalIsResumableFreeze 判断终态事件是否只是网络冻结导致的断流：这类中断仍可由恢复循环续跑。
func (c *Controller) TerminalIsResumableFreeze(sessionID string, info EventInfo) bool {
	if info.RunFinished {
		return false
	}
	state, err := c.Store.LoadRecovery(sessionID)
	if err != nil {
		return false
	}
	return state.AutoResumable()
}

// finalizeTerminal 落地真终态：标记结束后删除恢复状态；用户暂停需保留 run 身份供显式恢复，
// 可自动续跑的冻结中断则交给恢复循环处理。
func (c *Controller) finalizeTerminal(sessionID, runID string, info EventInfo) error {
	state, err := c.Store.LoadRecovery(sessionID)
	if err != nil {
		return err
	}
	if state.RunID == "" && state.FlowID == "" {
		return nil
	}
	if runID == "" {
		runID = state.RunID
	}
	if terminalIsResumable(state, info) || state.Intent == IntentUserPause {
		return nil
	}
	if _, err := c.Store.MarkTerminal(sessionID, runID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := c.Store.ClearRecovery(sessionID, runID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (c *Controller) Heartbeat(sessionID, runID string) error {
	_, err := c.Store.Heartbeat(sessionID, runID)
	return err
}

func (c *Controller) PrimaryActive(sessionID string) (bool, error) {
	state, err := c.Store.LoadRecovery(sessionID)
	if err != nil {
		return false, err
	}
	return state.Active && !state.LastHeartbeat.IsZero() && c.Store.now().Sub(state.LastHeartbeat) <= primaryActiveTTL, nil
}

func (c *Controller) MarkDisconnected(sessionID, runID string) error {
	_, err := c.Store.MarkDisconnected(sessionID, runID)
	return err
}

func (c *Controller) ReconcileSubmission(ctx context.Context, sessionID string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if err := c.Sleep(ctx, time.Duration(attempt)*200*time.Millisecond); err != nil {
				return err
			}
		}
		state, err := c.Store.LoadRecovery(sessionID)
		if err != nil {
			return err
		}
		execution, executionErr := c.Provider.GetSessionExecution(ctx, sessionID)
		if executionErr == nil && execution != nil && execution.Executing && execution.ExecutingRunID == state.RunID {
			_, err = c.Store.RecordAcceptedExecution(sessionID, execution.ExecutingRunID, execution.ExecutionAttemptID)
			return err
		} else if executionErr != nil {
			lastErr = executionErr
		}
		checkpoints, checkpointErr := c.Provider.ListResumableAgentLoopCheckpoints(ctx, provider.AgentLoopCheckpointQuery{
			ClientID: state.ClientID, DeviceID: state.DeviceID, FlowID: state.FlowID, SessionID: sessionID,
		})
		if checkpointErr == nil {
			checkpoint, found := selectCheckpoint(checkpoints, state, false, c.Store.now())
			if found {
				_, err = c.Store.SetResumeIntent(checkpoint)
				return err
			}
		} else {
			lastErr = checkpointErr
		}
	}
	if lastErr != nil {
		return fmt.Errorf("%w: %v", ErrSubmissionUncertain, lastErr)
	}
	return ErrSubmissionUncertain
}

// FlushToolCallbacks 补报待上报的工具结果，runID 为空表示所有 run。单条失败不阻断其余补报，
// 汇总为联合错误返回；被服务端永久拒绝或超过最大尝试次数的记录会被丢弃。
func (c *Controller) FlushToolCallbacks(ctx context.Context, runID string) error {
	return c.flushToolCallbacks(ctx, "", runID)
}

// FlushSessionToolCallbacks 仅补报指定会话的工具结果，供新执行发起前尽力清理同会话遗留记录。
func (c *Controller) FlushSessionToolCallbacks(ctx context.Context, sessionID string) error {
	return c.flushToolCallbacks(ctx, strings.TrimSpace(sessionID), "")
}

func (c *Controller) flushToolCallbacks(ctx context.Context, sessionID, runID string) error {
	callbacks, err := c.Store.ListDueToolCallbacks(c.Store.now())
	if err != nil {
		return err
	}
	var failures []error
	for _, pending := range callbacks {
		result := pending.Result
		if sessionID != "" && result.SessionID != sessionID {
			continue
		}
		if runID != "" && result.RunID != runID {
			continue
		}
		err := c.Provider.SubmitClientCallResult(ctx, result)
		if err == nil || IsLateToolResultRejected(err) || IsPermanentCallbackRejection(err) {
			if removeErr := c.Store.RemoveToolCallback(result.FlowID, result.SessionID, result.RunID, result.CallID); removeErr != nil {
				failures = append(failures, removeErr)
			}
			continue
		}
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}
		marked, markErr := c.Store.MarkToolCallbackAttempt(result.FlowID, result.SessionID, result.RunID, result.CallID)
		if markErr != nil {
			failures = append(failures, errors.Join(err, markErr))
			continue
		}
		if marked.Attempts >= maxToolCallbackAttempts {
			removeErr := c.Store.RemoveToolCallback(result.FlowID, result.SessionID, result.RunID, result.CallID)
			if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				failures = append(failures, errors.Join(err, removeErr))
				continue
			}
			failures = append(failures, fmt.Errorf("客户端工具结果补报 %d 次仍未成功，已放弃上报: run_id=%s call_id=%s: %w", marked.Attempts, result.RunID, result.CallID, err))
			continue
		}
		failures = append(failures, fmt.Errorf("补报客户端工具结果失败: %w", err))
	}
	return errors.Join(failures...)
}

func (c *Controller) Recover(ctx context.Context, sessionID string, explicit bool) (*ResumeAttempt, error) {
	state, err := c.Store.LoadRecovery(sessionID)
	if err != nil {
		return nil, err
	}
	if !explicit && (state.InvalidatedByUser || state.Intent == IntentStopped || state.Intent == IntentUserPause) {
		return nil, ErrUserControlInvalidated
	}
	if explicit {
		state, err = c.Store.AuthorizeExplicitResume(sessionID, state.RunID)
		if err != nil {
			return nil, err
		}
	}
	if err := c.FlushToolCallbacks(ctx, state.RunID); err != nil {
		return nil, err
	}
	execution, executionErr := c.Provider.GetSessionExecution(ctx, sessionID)
	if executionErr == nil && execution != nil && execution.Executing {
		if execution.ExecutingRunID == state.RunID {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("会话已由其他 run 执行: %s", execution.ExecutingRunID)
	}
	checkpoints, err := c.Provider.ListResumableAgentLoopCheckpoints(ctx, provider.AgentLoopCheckpointQuery{
		ClientID: state.ClientID, DeviceID: state.DeviceID, FlowID: state.FlowID, SessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}
	checkpoint, found := selectCheckpoint(checkpoints, state, explicit, c.Store.now())
	if !found {
		return nil, ErrNoResumableCheckpoint
	}
	claim, acquired, err := c.Store.ClaimRecovery(checkpoint.ID, checkpoint.Version)
	if err != nil {
		return nil, err
	}
	if !acquired {
		return nil, ErrClaimed
	}
	state, err = c.Store.SetResumeIntent(checkpoint)
	if err != nil {
		claim.Release()
		return nil, err
	}
	if !explicit && !state.AutoResumable() {
		claim.Release()
		return nil, ErrUserControlInvalidated
	}
	request, err := state.ResumeRequest()
	if err != nil {
		claim.Release()
		return nil, fmt.Errorf("读取恢复能力快照失败: %w", err)
	}
	stream, session, err := c.Provider.Stream(ctx, request)
	if err != nil {
		c.scheduleRetry(state, RetryAfterFromError(err))
		claim.Release()
		return nil, err
	}
	return &ResumeAttempt{controller: c, state: state, Stream: stream, Session: session, Request: request, claim: claim}, nil
}

type ResumeAttempt struct {
	controller *Controller
	state      RecoveryState
	Stream     <-chan provider.StreamEvent
	Session    *provider.StreamSession
	Request    provider.StreamRequest
	claim      *RecoveryClaim

	mu    sync.Mutex
	ready bool
}

func (a *ResumeAttempt) Observe(event provider.StreamEvent) (EventInfo, error) {
	info, err := a.controller.ObserveEvent(event)
	if info.ResumeReady {
		a.mu.Lock()
		a.ready = true
		a.mu.Unlock()
		if a.claim != nil {
			_ = a.claim.Release()
			a.claim = nil
		}
	}
	if info.NetworkFreeze {
		a.controller.scheduleRetry(a.state, info.RetryAfter)
	}
	return info, err
}

func (a *ResumeAttempt) Close() {
	a.mu.Lock()
	ready := a.ready
	a.mu.Unlock()
	if !ready {
		a.controller.scheduleRetry(a.state, 0)
	}
	if a.claim != nil {
		_ = a.claim.Release()
		a.claim = nil
	}
}

func (c *Controller) scheduleRetry(state RecoveryState, serverDelay time.Duration) {
	if state.RecoveryAttempts >= maxRecoveryAttempts || state.Intent != IntentResume {
		return
	}
	delay := serverDelay
	if delay <= 0 {
		delay = RecoveryBackoff(state.RecoveryAttempts + 1)
	}
	_, _ = c.Store.ScheduleRecoveryRetry(state.SessionID, state.RunID, c.Store.now().Add(delay))
}

func RecoveryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	for index := 1; index < attempt && delay < 30*time.Second; index++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	jitter := time.Duration(mathrand.Int63n(int64(delay/4 + 1)))
	return delay + jitter
}

func selectCheckpoint(checkpoints []provider.AgentLoopCheckpoint, state RecoveryState, explicit bool, now time.Time) (provider.AgentLoopCheckpoint, bool) {
	var selected provider.AgentLoopCheckpoint
	found := false
	for _, checkpoint := range checkpoints {
		if checkpoint.ID == "" || checkpoint.Version <= 0 || checkpoint.RunID != state.RunID || checkpoint.SessionID != state.SessionID || checkpoint.FlowID != state.FlowID {
			continue
		}
		if !checkpoint.ExpiresAt.IsZero() && !checkpoint.ExpiresAt.After(now) {
			continue
		}
		if state.ClientID != "" && checkpoint.OriginClientID != state.ClientID {
			continue
		}
		if state.DeviceID != "" && checkpoint.OriginDeviceID != state.DeviceID {
			continue
		}
		if !explicit && (checkpoint.PauseReason != "network" || checkpoint.RequiresUserResume) {
			continue
		}
		if !found || checkpoint.Version > selected.Version || (checkpoint.Version == selected.Version && checkpoint.UpdatedAt.After(selected.UpdatedAt)) {
			selected = checkpoint
			found = true
		}
	}
	return selected, found
}

func RetryAfterFromError(err error) time.Duration {
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		return 0
	}
	var payload map[string]any
	if json.Unmarshal([]byte(httpErr.Body), &payload) != nil {
		return 0
	}
	if detail, ok := payload["detail"].(map[string]any); ok {
		payload = detail
	}
	return durationValue(payload, "retry_after")
}

func IsLateToolResultRejected(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "tool_call_paused") && !strings.Contains(message, "late result") && !strings.Contains(message, "迟到") {
		return false
	}
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		return true
	}
	// 用户暂停冻结即作废：后端以 400 + 机器码 tool_call_paused 拒绝迟到结果，须与 409/410/422
	// 一样识别为丢弃信号，否则会被当成可重试而无限补报。
	return httpErr.StatusCode == http.StatusBadRequest || httpErr.StatusCode == http.StatusConflict ||
		httpErr.StatusCode == http.StatusGone || httpErr.StatusCode == http.StatusUnprocessableEntity
}

// IsPermanentCallbackRejection 判断服务端是否权威拒绝该回调（重试不可能成功）。
// tool_call_paused 已被 IsLateToolResultRejected 覆盖，这里排除该机器码，避免误判。
func IsPermanentCallbackRejection(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if strings.Contains(strings.ToLower(err.Error()), "tool_call_paused") {
		return false
	}
	switch httpErr.StatusCode {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound,
		http.StatusMethodNotAllowed, http.StatusUnprocessableEntity:
		return true
	default:
		return false
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func intValue(values map[string]any, key string) int {
	switch value := values[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(value))
		return parsed
	default:
		return 0
	}
}

func boolValue(values map[string]any, key string) bool {
	value, _ := values[key].(bool)
	return value
}

func durationValue(values map[string]any, key string) time.Duration {
	switch value := values[key].(type) {
	case float64:
		return time.Duration(value * float64(time.Second))
	case int:
		return time.Duration(value) * time.Second
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err == nil {
			return time.Duration(parsed * float64(time.Second))
		}
	}
	return 0
}

func firstValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(values, key); value != "" {
			return value
		}
	}
	return ""
}
