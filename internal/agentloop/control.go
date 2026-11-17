package agentloop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const detachedControlTimeout = 5 * time.Second

func (c *Controller) Pause(ctx context.Context, sessionID, runID string) (*provider.AgentLoopControlResponse, error) {
	return c.control(ctx, sessionID, runID, IntentUserPause)
}

func (c *Controller) Cancel(ctx context.Context, sessionID, runID string) (*provider.AgentLoopControlResponse, error) {
	return c.control(ctx, sessionID, runID, IntentStopped)
}

func (c *Controller) CancelDetached(request provider.StreamRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), detachedControlTimeout)
	defer cancel()
	// 传空 runID，令 control() 回落到状态里登记的权威 run：断开/恢复后服务端可能已换用
	// 新的 run，直接用调用方持有的 request.RunID 会与权威 run 不匹配而取消失败。
	_, err := c.control(ctx, request.SessionID, "", IntentStopped)
	return err
}

func (c *Controller) control(ctx context.Context, sessionID, runID, intent string) (*provider.AgentLoopControlResponse, error) {
	state, err := c.Store.LoadRecovery(sessionID)
	if err != nil {
		return nil, err
	}
	if state.FlowID == "" || state.SessionID == "" {
		return nil, fmt.Errorf("当前会话没有可控制的 AgentLoop 执行")
	}
	if runID == "" {
		runID = state.RunID
	}
	if runID == "" || (state.RunID != "" && state.RunID != runID) {
		return nil, fmt.Errorf("控制请求 run_id 与当前执行不匹配")
	}
	action := "pause"
	if intent == IntentStopped {
		action = "cancel"
	}
	var previous RecoveryState
	state, err = c.Store.UpdateRecovery(sessionID, func(state *RecoveryState) error {
		if state.RunID != runID {
			return fmt.Errorf("控制请求 run_id 与当前执行不匹配")
		}
		if state.Intent == IntentStopped && intent != IntentStopped {
			return fmt.Errorf("cancel 已优先于 pause 生效")
		}
		previous = *state
		if state.ControlAction != action || state.ControlRequestID == "" {
			state.ControlRequestID = NewIdentifier()
		}
		state.ControlAction = action
		state.Intent = intent
		state.PendingStart = false
		state.Active = false
		state.AwaitingResume = false
		state.InvalidatedByUser = true
		state.NextRetryAt = time.Time{}
		return nil
	})
	if err != nil {
		return nil, err
	}
	request := provider.AgentLoopControlRequest{
		RunID: runID, RequestID: state.ControlRequestID, ClientID: state.ClientID, DeviceID: state.DeviceID,
	}
	var response *provider.AgentLoopControlResponse
	if intent == IntentStopped {
		response, err = c.Provider.CancelAgentLoop(ctx, sessionID, request)
	} else {
		response, err = c.Provider.PauseAgentLoop(ctx, sessionID, request)
	}
	if err != nil {
		c.rollbackControlIntent(sessionID, state.ControlRequestID, previous, err)
		return nil, err
	}
	if response != nil && response.RunID != "" && response.RunID != runID {
		return nil, fmt.Errorf("控制响应属于其他 run: %s", strings.TrimSpace(response.RunID))
	}
	return response, nil
}

// rollbackControlIntent 在服务端明确拒绝（4xx，408 除外）时恢复控制前的本地意图，
// 避免「本地已暂停/作废、服务端仍在执行」的永久背离。网络错误/5xx 结果不确定，
// 保持 fail-safe：本地意图不回滚，用户可凭同一 ControlRequestID 幂等重试。
func (c *Controller) rollbackControlIntent(sessionID, requestID string, previous RecoveryState, rpcErr error) {
	var httpErr *provider.HTTPError
	if !errors.As(rpcErr, &httpErr) {
		return
	}
	if httpErr.StatusCode < http.StatusBadRequest || httpErr.StatusCode >= http.StatusInternalServerError || httpErr.StatusCode == http.StatusRequestTimeout {
		return
	}
	_, _ = c.Store.UpdateRecovery(sessionID, func(state *RecoveryState) error {
		if state.ControlRequestID != requestID {
			return nil
		}
		state.Intent = previous.Intent
		state.ControlAction = previous.ControlAction
		state.ControlRequestID = previous.ControlRequestID
		state.PendingStart = previous.PendingStart
		state.Active = previous.Active
		state.AwaitingResume = previous.AwaitingResume
		state.InvalidatedByUser = previous.InvalidatedByUser
		state.NextRetryAt = previous.NextRetryAt
		return nil
	})
}

func (c *Controller) ToolsAllowed(sessionID, runID string) bool {
	state, err := c.Store.LoadRecovery(sessionID)
	if err != nil || state.RunID == "" || state.RunID != runID {
		return false
	}
	return state.Intent != IntentStopped && state.Intent != IntentUserPause
}
