package agentloop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func (c *Controller) Execute(ctx context.Context, request provider.StreamRequest) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	if provider.NormalizeRequestType(request.RequestType) != provider.RequestTypeAgent {
		return c.Provider.Stream(ctx, request)
	}
	// 新执行发起前尽力补报同会话遗留的工具结果；失败不阻断新执行，记录仍在队列中由后续上报重试。
	_ = c.FlushSessionToolCallbacks(ctx, request.SessionID)
	prepared, err := c.PrepareStart(request)
	if err != nil {
		return nil, nil, err
	}
	stream, session, err := c.Provider.Stream(ctx, prepared)
	var resumeAttempt *ResumeAttempt
	if err != nil {
		if ctx.Err() != nil {
			if cancelErr := c.CancelDetached(prepared); cancelErr != nil {
				return nil, nil, errors.Join(ctx.Err(), fmt.Errorf("服务端取消失败: %w", cancelErr))
			}
			return nil, nil, ctx.Err()
		}
		if reconcileErr := c.ReconcileSubmission(ctx, prepared.SessionID); reconcileErr != nil {
			return nil, nil, fmt.Errorf("请求提交结果不确定，未自动重复发起；请检查远程状态后显式重试: %w", reconcileErr)
		}
		resumeAttempt, err = c.Recover(ctx, prepared.SessionID, false)
		if err != nil {
			return nil, nil, fmt.Errorf("执行可能已受理，但未自动重复发起；恢复当前执行失败: %w", err)
		}
		stream = resumeAttempt.Stream
		session = resumeAttempt.Session
	}
	return c.run(ctx, prepared, stream, session, resumeAttempt)
}

func (c *Controller) Resume(ctx context.Context, sessionID string) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	attempt, err := c.Recover(ctx, sessionID, true)
	if err != nil {
		return nil, nil, err
	}
	return c.run(ctx, attempt.Request, attempt.Stream, attempt.Session, attempt)
}

func (c *Controller) ResumeAutomatic(ctx context.Context, sessionID string) (<-chan provider.StreamEvent, *provider.StreamSession, *ResumeAttempt, error) {
	attempt, err := c.Recover(ctx, sessionID, false)
	if err != nil {
		return nil, nil, nil, err
	}
	stream, session, err := c.run(ctx, attempt.Request, attempt.Stream, attempt.Session, attempt)
	if err != nil {
		attempt.Close()
		return nil, nil, nil, err
	}
	return stream, session, attempt, nil
}

func (c *Controller) run(ctx context.Context, prepared provider.StreamRequest, stream <-chan provider.StreamEvent, session *provider.StreamSession, resumeAttempt *ResumeAttempt) (<-chan provider.StreamEvent, *provider.StreamSession, error) {
	var err error
	streamCtx, cancel := context.WithCancel(ctx)
	output := make(chan provider.StreamEvent)
	go func() {
		defer close(output)
		defer func() {
			if ctx.Err() != nil {
				_ = c.CancelDetached(prepared)
			}
		}()
		defer cancel()
		defer func() {
			if resumeAttempt != nil {
				resumeAttempt.Close()
			}
		}()
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		accepted := resumeAttempt != nil
		currentRunID := prepared.RunID
		for {
			currentStream := stream
			terminal := false
			networkFailure := false
		eventLoop:
			for currentStream != nil {
				select {
				case <-streamCtx.Done():
					return
				case <-heartbeat.C:
					if err := c.Heartbeat(prepared.SessionID, currentRunID); err != nil {
						// 跨进程锁竞争是瞬时状态，跳过本次心跳即可，不能中止执行。
						if errors.Is(err, ErrClaimed) {
							continue
						}
						emitExecutionError(streamCtx, output, fmt.Errorf("保存 AgentLoop 活跃状态失败: %w", err))
						return
					}
				case event, ok := <-currentStream:
					if !ok {
						currentStream = nil
						continue
					}
					var info EventInfo
					if resumeAttempt != nil {
						info, err = resumeAttempt.Observe(event)
					} else {
						info, err = c.ObserveEvent(event)
					}
					if err != nil {
						emitExecutionError(streamCtx, output, fmt.Errorf("保存 AgentLoop 执行状态失败: %w", err))
						return
					}
					if info.Accepted {
						accepted = true
					}
					if info.RunID != "" {
						currentRunID = info.RunID
					}
					if info.NetworkFreeze {
						networkFailure = true
						break eventLoop
					}
					if info.Terminal {
						if c.TerminalIsResumableFreeze(prepared.SessionID, info) {
							networkFailure = true
							break eventLoop
						}
						terminal = true
					}
					if event.Type == provider.EventError {
						select {
						case output <- event:
						case <-streamCtx.Done():
						}
						return
					}
					select {
					case output <- event:
					case <-streamCtx.Done():
						return
					}
					if terminal {
						_ = c.finalizeTerminal(prepared.SessionID, currentRunID, info)
						return
					}
				}
			}
			if resumeAttempt != nil {
				resumeAttempt.Close()
				resumeAttempt = nil
			}
			if terminal || streamCtx.Err() != nil {
				return
			}
			state, stateErr := c.Store.LoadRecovery(prepared.SessionID)
			if stateErr != nil {
				emitExecutionError(streamCtx, output, stateErr)
				return
			}
			if state.Intent == IntentStopped || state.Intent == IntentUserPause {
				return
			}
			if !accepted {
				if err := c.ReconcileSubmission(streamCtx, prepared.SessionID); err != nil {
					emitExecutionError(streamCtx, output, fmt.Errorf("请求提交结果不确定，未自动重复发起；请显式重试: %w", err))
					return
				}
			}
			if !networkFailure {
				if err := c.MarkDisconnected(prepared.SessionID, currentRunID); err != nil {
					emitExecutionError(streamCtx, output, err)
					return
				}
			}
			var recovered bool
			for attempt := 0; attempt < maxRecoveryAttempts; attempt++ {
				state, stateErr = c.Store.LoadRecovery(prepared.SessionID)
				if stateErr != nil {
					emitExecutionError(streamCtx, output, stateErr)
					return
				}
				if state.Intent == IntentStopped || state.Intent == IntentUserPause {
					return
				}
				if !state.NextRetryAt.IsZero() {
					if delay := state.NextRetryAt.Sub(c.Store.now()); delay > 0 {
						if err := c.Sleep(streamCtx, delay); err != nil {
							return
						}
					}
				}
				resumeAttempt, err = c.Recover(streamCtx, prepared.SessionID, false)
				if err == nil {
					stream = resumeAttempt.Stream
					recovered = true
					break
				}
				if errors.Is(err, ErrUserControlInvalidated) {
					return
				}
				if attempt == maxRecoveryAttempts-1 {
					emitExecutionError(streamCtx, output, fmt.Errorf("AgentLoop 自动恢复失败，未重复发起新执行: %w", err))
					return
				}
				_ = c.MarkDisconnected(prepared.SessionID, currentRunID)
				if err := c.Sleep(streamCtx, RecoveryBackoff(attempt+1)); err != nil {
					return
				}
			}
			if !recovered {
				return
			}
		}
	}()
	return output, session, nil
}

func emitExecutionError(ctx context.Context, output chan<- provider.StreamEvent, err error) {
	select {
	case output <- provider.StreamEvent{Type: provider.EventError, Data: map[string]any{"error": err.Error()}}:
	case <-ctx.Done():
	}
}
