package agentloop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const observerSnapshotPageSize = 200

type ObserverRequest struct {
	FlowID    string
	SessionID string
	ClientID  string
	DeviceID  string
}

type ObserverStatus struct {
	RunID             string
	Polling           bool
	Failures          int
	StableConnections int
	PollingFallbacks  int
}

type ObserverHandlers struct {
	OnEvent    func(provider.StreamEvent) error
	OnSnapshot func(*provider.SessionMessageList) error
	OnMetadata func(*provider.SessionExecutionMetadata) error
	OnStatus   func(ObserverStatus) error
}

func (c *Controller) Attach(ctx context.Context, request ObserverRequest, handlers ObserverHandlers) error {
	request.SessionID = strings.TrimSpace(request.SessionID)
	if request.SessionID == "" {
		return fmt.Errorf("attach 缺少 session_id")
	}
	metadata, err := c.Provider.GetSessionExecution(ctx, request.SessionID)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("attach 未返回会话执行元数据")
	}
	flowID := firstNonEmptyString(metadata.FlowID, request.FlowID)
	if flowID == "" {
		return fmt.Errorf("attach 缺少 flow_id")
	}
	if handlers.OnMetadata != nil {
		if err := handlers.OnMetadata(metadata); err != nil {
			return err
		}
	}
	state, err := c.Store.UpdateObserver(request.SessionID, func(state *ObserverState) error {
		if state.FlowID != flowID || state.RunID != metadata.ExecutingRunID || state.Closed {
			state.AfterEventID = ""
			state.SubscriptionID = ""
			state.NeedsSnapshot = true
			state.ConsecutiveFailures = 0
		}
		state.FlowID = flowID
		state.RunID = metadata.ExecutingRunID
		state.ExecutionAttemptID = metadata.ExecutionAttemptID
		state.Closed = false
		if state.SubscriptionID == "" {
			state.SubscriptionID = NewIdentifier()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if state.NeedsSnapshot {
		if err := c.refreshObserverSnapshot(ctx, state, handlers); err != nil {
			return err
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		recovery, recoveryErr := c.Store.LoadRecovery(request.SessionID)
		if recoveryErr != nil {
			return recoveryErr
		}
		if recovery.Intent == IntentStopped || recovery.Intent == IntentUserPause || recovery.InvalidatedByUser {
			return nil
		}
		if recovery.Intent == IntentResume && recovery.AwaitingResume {
			if err := c.Sleep(ctx, ObserverPollingInterval); err != nil {
				return err
			}
			continue
		}
		state, err = c.Store.LoadObserver(request.SessionID)
		if err != nil {
			return err
		}
		if state.Closed {
			return nil
		}
		if state.ConsecutiveFailures >= ObserverMaxFailures {
			state, err = c.Store.UpdateObserver(request.SessionID, func(state *ObserverState) error {
				state.PollingFallbacks++
				return nil
			})
			if err != nil {
				return err
			}
			if err := c.pollObserver(ctx, state, handlers); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if observerTargetGone(err) {
					_, err = c.Store.CloseObserver(request.SessionID)
					return err
				}
				state, err = c.recordObserverFailure(request.SessionID, handlers)
				if err != nil {
					return err
				}
				if err := c.Sleep(ctx, ObserverBackoff(state.ConsecutiveFailures)); err != nil {
					return err
				}
				continue
			}
			if err := c.Sleep(ctx, ObserverPollingInterval); err != nil {
				return err
			}
		}
		state, err = c.Store.LoadObserver(request.SessionID)
		if err != nil {
			return err
		}
		if state.Closed {
			return nil
		}
		if state.SubscriptionID == "" {
			state, err = c.Store.UpdateObserver(request.SessionID, func(state *ObserverState) error {
				state.SubscriptionID = NewIdentifier()
				return nil
			})
			if err != nil {
				return err
			}
		}
		streamRequest := provider.StreamRequest{
			FlowID: state.FlowID, SessionID: request.SessionID, RunID: state.RunID,
			RequestType: provider.RequestTypeAgent, ExecutionMode: provider.ExecutionModeAttach,
			ExecutionAttemptID: state.ExecutionAttemptID, AfterEventID: state.AfterEventID,
			SubscriptionID: state.SubscriptionID, ClientID: ObserverClientID(request.ClientID), DeviceID: request.DeviceID,
		}
		streamCtx, streamCancel := context.WithCancel(ctx)
		stream, _, streamErr := c.Provider.Stream(streamCtx, streamRequest)
		if streamErr != nil {
			streamCancel()
			state, err = c.recordObserverFailure(request.SessionID, handlers)
			if err != nil {
				return err
			}
			if err := c.Sleep(ctx, ObserverBackoff(state.ConsecutiveFailures)); err != nil {
				return err
			}
			continue
		}
		connectedAt := c.Store.now()
		resync := false
		closed := false
		for event := range stream {
			if IsObserverProtocolViolation(event) {
				streamCancel()
				return fmt.Errorf("attach 只读观察收到禁止的客户端工具事件")
			}
			info := ClassifyEvent(event)
			if info.ResyncRequired {
				if _, err := c.Store.ResetObserverCursor(request.SessionID); err != nil {
					streamCancel()
					return err
				}
				state, err = c.Store.LoadObserver(request.SessionID)
				if err != nil {
					streamCancel()
					return err
				}
				if err := c.refreshObserverSnapshot(ctx, state, handlers); err != nil {
					streamCancel()
					return err
				}
				resync = true
				break
			}
			state, err = c.Store.UpdateObserver(request.SessionID, func(state *ObserverState) error {
				if info.RunID != "" {
					state.RunID = info.RunID
				}
				if info.ExecutionAttemptID != "" {
					state.ExecutionAttemptID = info.ExecutionAttemptID
				}
				return nil
			})
			if err != nil {
				streamCancel()
				return err
			}
			state, err = c.Store.AdvanceObserver(request.SessionID, event)
			if err != nil {
				streamCancel()
				return err
			}
			if info.RunSnapshot {
				if err := c.refreshObserverSnapshot(ctx, state, handlers); err != nil {
					streamCancel()
					return err
				}
			}
			if handlers.OnEvent != nil {
				if err := handlers.OnEvent(event); err != nil {
					streamCancel()
					return err
				}
			}
			if info.Terminal {
				// 冻结导致的断流不是真终态：保持观察，等待原端续跑后重连。
				if c.TerminalIsResumableFreeze(request.SessionID, info) {
					break
				}
				closed = true
				break
			}
		}
		streamCancel()
		if closed {
			state, err = c.Store.UpdateObserver(request.SessionID, func(state *ObserverState) error {
				state.CleanCloses++
				return nil
			})
			if err != nil {
				return err
			}
			_, err = c.Store.CloseObserver(request.SessionID)
			return err
		}
		if resync {
			continue
		}
		stable := c.Store.now().Sub(connectedAt) >= ObserverStableConnection
		state, err = c.Store.UpdateObserver(request.SessionID, func(state *ObserverState) error {
			if stable {
				state.StableConnections++
				state.ConsecutiveFailures = 0
			} else {
				state.ConsecutiveFailures++
			}
			return nil
		})
		if err != nil {
			return err
		}
		if handlers.OnStatus != nil {
			if err := handlers.OnStatus(ObserverStatus{RunID: state.RunID, Failures: state.ConsecutiveFailures, StableConnections: state.StableConnections, PollingFallbacks: state.PollingFallbacks}); err != nil {
				return err
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := c.Sleep(ctx, ObserverBackoff(state.ConsecutiveFailures)); err != nil {
			return err
		}
	}
}

func (c *Controller) recordObserverFailure(sessionID string, handlers ObserverHandlers) (ObserverState, error) {
	state, err := c.Store.UpdateObserver(sessionID, func(state *ObserverState) error {
		state.ConsecutiveFailures++
		return nil
	})
	if err == nil && handlers.OnStatus != nil {
		err = handlers.OnStatus(ObserverStatus{RunID: state.RunID, Failures: state.ConsecutiveFailures, StableConnections: state.StableConnections, PollingFallbacks: state.PollingFallbacks})
	}
	return state, err
}

func (c *Controller) pollObserver(ctx context.Context, state ObserverState, handlers ObserverHandlers) error {
	metadata, err := c.Provider.GetSessionExecution(ctx, state.SessionID)
	if err != nil {
		return err
	}
	if metadata == nil {
		return fmt.Errorf("attach 轮询未返回会话执行元数据")
	}
	if handlers.OnMetadata != nil {
		if err := handlers.OnMetadata(metadata); err != nil {
			return err
		}
	}
	if !metadata.Executing {
		// run 已结束（网络冻结期 executing 仍为 true）：落地最终快照后关闭观察，避免无限轮询。
		if metadata.FlowID != "" {
			state.FlowID = metadata.FlowID
		}
		if err := c.refreshObserverSnapshot(ctx, state, handlers); err != nil {
			return err
		}
		_, err = c.Store.CloseObserver(state.SessionID)
		return err
	}
	state, err = c.Store.UpdateObserver(state.SessionID, func(current *ObserverState) error {
		if metadata.ExecutingRunID != "" && current.RunID != metadata.ExecutingRunID {
			current.AfterEventID = ""
			current.SubscriptionID = ""
			current.NeedsSnapshot = true
		}
		if metadata.FlowID != "" {
			current.FlowID = metadata.FlowID
		}
		if metadata.ExecutingRunID != "" {
			current.RunID = metadata.ExecutingRunID
			current.ExecutionAttemptID = metadata.ExecutionAttemptID
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := c.refreshObserverSnapshot(ctx, state, handlers); err != nil {
		return err
	}
	if handlers.OnStatus != nil {
		return handlers.OnStatus(ObserverStatus{RunID: state.RunID, Polling: true, Failures: state.ConsecutiveFailures, StableConnections: state.StableConnections, PollingFallbacks: state.PollingFallbacks})
	}
	return nil
}

func (c *Controller) refreshObserverSnapshot(ctx context.Context, state ObserverState, handlers ObserverHandlers) error {
	messages, err := c.loadObserverMessages(ctx, state.FlowID, state.SessionID)
	if err != nil {
		return err
	}
	if handlers.OnSnapshot != nil {
		if err := handlers.OnSnapshot(messages); err != nil {
			return err
		}
	}
	_, err = c.Store.UpdateObserver(state.SessionID, func(current *ObserverState) error {
		current.NeedsSnapshot = false
		return nil
	})
	return err
}

func (c *Controller) loadObserverMessages(ctx context.Context, flowID, sessionID string) (*provider.SessionMessageList, error) {
	result := &provider.SessionMessageList{}
	for offset := 0; ; {
		page, err := c.Provider.ListSessionMessages(ctx, flowID, sessionID, offset, observerSnapshotPageSize)
		if err != nil {
			return nil, err
		}
		if page == nil || len(page.Items) == 0 {
			break
		}
		result.Items = append(result.Items, page.Items...)
		result.Total = page.Total
		offset += len(page.Items)
		result.Offset = offset
		if len(page.Items) < observerSnapshotPageSize || (page.Total > 0 && offset >= page.Total) {
			break
		}
	}
	return result, nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// observerTargetGone 会话已被服务端删除（404/410）时观察目标不复存在，继续重试没有意义。
func observerTargetGone(err error) bool {
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == http.StatusNotFound || httpErr.StatusCode == http.StatusGone
}
