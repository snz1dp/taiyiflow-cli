package runtime

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agentloop"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const (
	collaborationCapabilityAgentLoopResume     = "agent_loop.resume.v1"
	collaborationEventAgentLoopResumeRequested = "agent_loop.resume_requested"
	collaborationEventAgentLoopResumeAck       = "agent_loop.resume_notification_ack"
	collaborationEventAgentLoopCheckpointState = "agent_loop.checkpoint_state"
)

type agentLoopResumeNotification struct {
	NotificationID     string    `json:"notification_id"`
	CheckpointID       string    `json:"checkpoint_id"`
	CheckpointVersion  int       `json:"checkpoint_version"`
	SessionID          string    `json:"session_id"`
	RunID              string    `json:"run_id"`
	FlowID             string    `json:"flow_id"`
	Status             string    `json:"status"`
	ResumeStage        string    `json:"resume_stage"`
	PauseReason        string    `json:"pause_reason"`
	RequiresUserResume bool      `json:"requires_user_resume"`
	ExpiresAt          time.Time `json:"expires_at"`
}

type workerAgentLoopCoordinator struct {
	controller *agentloop.Controller
	store      *agentloop.Store
	initErr    error
	discovery  chan struct{}
}

func newWorkerAgentLoopCoordinator(runtimeClient *ClientRuntime, providerClient provider.Provider, logf func(string, ...any)) *workerAgentLoopCoordinator {
	coordinator := &workerAgentLoopCoordinator{discovery: make(chan struct{}, 1)}
	store := runtimeClient.agentLoopStore
	if store == nil {
		var err error
		store, err = agentloop.DefaultStore()
		if err != nil {
			coordinator.initErr = err
			logf("初始化 AgentLoop worker 恢复状态失败: %v", err)
			return coordinator
		}
		runtimeClient.SetAgentLoopStore(store)
	}
	coordinator.store = store
	coordinator.controller = agentloop.NewController(providerClient, store)
	return coordinator
}

func (n agentLoopResumeNotification) checkpoint() provider.AgentLoopCheckpoint {
	return provider.AgentLoopCheckpoint{
		ID:                 strings.TrimSpace(n.CheckpointID),
		Version:            n.CheckpointVersion,
		SessionID:          strings.TrimSpace(n.SessionID),
		RunID:              strings.TrimSpace(n.RunID),
		FlowID:             strings.TrimSpace(n.FlowID),
		Status:             strings.TrimSpace(n.Status),
		ResumeStage:        strings.TrimSpace(n.ResumeStage),
		PauseReason:        strings.TrimSpace(n.PauseReason),
		RequiresUserResume: n.RequiresUserResume,
		ExpiresAt:          n.ExpiresAt,
	}
}

func (n agentLoopResumeNotification) valid() bool {
	return strings.TrimSpace(n.NotificationID) != "" && strings.TrimSpace(n.CheckpointID) != "" && n.CheckpointVersion > 0 &&
		strings.TrimSpace(n.SessionID) != "" && strings.TrimSpace(n.RunID) != "" && strings.TrimSpace(n.FlowID) != ""
}

func (w *collaborationWorker) scheduleAgentLoopNotification(ctx context.Context, conn net.Conn, notification agentLoopResumeNotification) {
	if w.agentLoop == nil || w.agentLoop.controller == nil {
		if w.agentLoop != nil && w.agentLoop.initErr != nil {
			w.logf("无法处理 AgentLoop 恢复通知: %v", w.agentLoop.initErr)
		}
		return
	}
	if !notification.valid() {
		w.logf("忽略字段不全的 AgentLoop 恢复通知")
		return
	}
	claim, acquired, err := w.agentLoop.store.BeginNotification(notification.SessionID, notification.NotificationID)
	if err != nil {
		w.logf("保存 AgentLoop 通知去重状态失败: notification_id=%s err=%v", notification.NotificationID, err)
		return
	}
	if !acquired {
		w.logf("AgentLoop 恢复通知已被其他进程处理: notification_id=%s", notification.NotificationID)
		return
	}
	select {
	case w.semaphore <- struct{}{}:
		go w.executeAgentLoopNotification(ctx, conn, notification, claim)
	default:
		_ = claim.Release()
		w.logf("AgentLoop 恢复并发已满，稍后重试通知: notification_id=%s", notification.NotificationID)
	}
}

func (w *collaborationWorker) executeAgentLoopNotification(ctx context.Context, conn net.Conn, notification agentLoopResumeNotification, claim *agentloop.NotificationClaim) {
	defer func() { <-w.semaphore }()
	completed := false
	defer func() {
		if !completed {
			_ = claim.Release()
		}
	}()
	complete := func() {
		if err := claim.Complete(); err != nil {
			w.logf("完成 AgentLoop 通知去重状态失败: notification_id=%s err=%v", notification.NotificationID, err)
			return
		}
		completed = true
	}
	if !notification.ExpiresAt.IsZero() && !w.agentLoop.store.Now().Before(notification.ExpiresAt) {
		complete()
		return
	}
	state, err := w.agentLoop.store.LoadRecovery(notification.SessionID)
	if err != nil {
		w.logf("读取 AgentLoop 恢复状态失败: notification_id=%s err=%v", notification.NotificationID, err)
		return
	}
	if state.RunID == "" || state.RunID != notification.RunID || state.FlowID != notification.FlowID {
		complete()
		return
	}
	if state.Intent == agentloop.IntentStopped || state.Intent == agentloop.IntentUserPause || state.InvalidatedByUser {
		w.logf("AgentLoop 已被用户停止或暂停，忽略恢复通知: notification_id=%s intent=%s", notification.NotificationID, state.Intent)
		complete()
		return
	}
	active, err := w.agentLoop.controller.PrimaryActive(notification.SessionID)
	if err != nil {
		w.logf("确认 AgentLoop 原端活跃状态失败: notification_id=%s err=%v", notification.NotificationID, err)
		return
	}
	if active {
		w.logf("AgentLoop 原端仍活跃，忽略 worker 恢复通知: notification_id=%s session_id=%s", notification.NotificationID, notification.SessionID)
		return
	}
	stream, _, attempt, err := w.agentLoop.controller.ResumeAutomatic(ctx, notification.SessionID)
	if err != nil {
		if errors.Is(err, agentloop.ErrNoResumableCheckpoint) || errors.Is(err, agentloop.ErrUserControlInvalidated) {
			complete()
		} else if !errors.Is(err, agentloop.ErrClaimed) && !errors.Is(err, agentloop.ErrAlreadyRunning) && !errors.Is(err, context.Canceled) {
			w.logf("AgentLoop 自动恢复发起失败: notification_id=%s err=%v", notification.NotificationID, err)
		}
		return
	}
	matchesNotification := attempt.Request.CheckpointID == notification.CheckpointID && attempt.Request.CheckpointVersion == notification.CheckpointVersion
	if !matchesNotification {
		w.logf("AgentLoop 恢复实得检查点与通知不符，仅本地去重不发送 ACK: notification_id=%s 通知=%s/%d 实得=%s/%d",
			notification.NotificationID, notification.CheckpointID, notification.CheckpointVersion,
			attempt.Request.CheckpointID, attempt.Request.CheckpointVersion)
		complete()
	}
	acknowledged := false
	for event := range stream {
		info := agentloop.ClassifyEvent(event)
		if info.ResumeReady && matchesNotification && !acknowledged {
			if err := w.sendEnvelope(conn, collaborationEventAgentLoopResumeAck, map[string]any{
				"notification_id":    notification.NotificationID,
				"checkpoint_id":      notification.CheckpointID,
				"checkpoint_version": notification.CheckpointVersion,
			}); err != nil {
				// ACK 失败不能遗弃恢复流：继续消费并执行工具，否则 run 僵死等待工具结果。
				// 通知认领保持 pending，TTL 过期后重投会因 ErrAlreadyRunning 被安全忽略。
				w.logf("发送 AgentLoop 恢复通知 ACK 失败（继续消费恢复流）: notification_id=%s err=%v", notification.NotificationID, err)
			} else {
				complete()
			}
			acknowledged = true
		}
		switch event.Type {
		case provider.EventBuiltIn:
			if err := w.runtime.HandleBuiltInEvent(ctx, w.provider, w.currentConfig(), event.Data); err != nil {
				w.logf("AgentLoop 恢复流内置工具回调失败: run_id=%s err=%v", notification.RunID, err)
			}
		case provider.EventClientTool:
			if err := w.runtime.HandleClientToolEvent(ctx, w.provider, w.currentConfig(), event.Data); err != nil {
				w.logf("AgentLoop 恢复流客户端工具回调失败: run_id=%s err=%v", notification.RunID, err)
			}
		case provider.EventError:
			w.logf("AgentLoop 自动恢复流结束: run_id=%s err=%v", notification.RunID, stringValue(event.Data["error"]))
		}
	}
}

func (w *collaborationWorker) recordAgentLoopCheckpointState(notification agentLoopResumeNotification) {
	if w.agentLoop == nil || w.agentLoop.store == nil || w.agentLoop.controller == nil {
		return
	}
	checkpoint := notification.checkpoint()
	if checkpoint.ID == "" || checkpoint.SessionID == "" || checkpoint.RunID == "" || checkpoint.Version <= 0 {
		w.logf("忽略字段不全的 AgentLoop checkpoint_state")
		return
	}
	// 与主动发现路径一致：原端仍活跃时不得记录 checkpoint_state，
	// 否则 RecordCheckpointState 会抹掉共享存储里的 Active 标志，诱发 worker 抢跑接管。
	active, err := w.agentLoop.controller.PrimaryActive(checkpoint.SessionID)
	if err != nil {
		w.logf("确认 AgentLoop 原端活跃状态失败: checkpoint_id=%s err=%v", checkpoint.ID, err)
		return
	}
	if active {
		w.logf("AgentLoop 原端仍活跃，忽略 checkpoint_state: session_id=%s checkpoint_id=%s", checkpoint.SessionID, checkpoint.ID)
		return
	}
	if _, err := w.agentLoop.store.RecordCheckpointState(checkpoint); err != nil {
		w.logf("保存 AgentLoop checkpoint_state 失败: checkpoint_id=%s err=%v", checkpoint.ID, err)
	}
}

func (w *collaborationWorker) startAgentLoopDiscovery(ctx context.Context) {
	if w.agentLoop == nil || w.agentLoop.controller == nil {
		return
	}
	if err := w.agentLoop.store.PruneNotifications(); err != nil && !errors.Is(err, context.Canceled) {
		w.logf("清理过期 AgentLoop 通知去重状态失败: %v", err)
	}
	hasRecovery, err := w.agentLoop.store.HasRecoveryStates()
	if err != nil {
		w.logf("检查 AgentLoop 本地恢复状态失败: %v", err)
		return
	}
	if !hasRecovery {
		return
	}
	select {
	case w.agentLoop.discovery <- struct{}{}:
		go w.discoverAgentLoopCheckpoints(ctx)
	default:
	}
}

func (w *collaborationWorker) discoverAgentLoopCheckpoints(ctx context.Context) {
	defer func() { <-w.agentLoop.discovery }()
	if err := w.agentLoop.controller.FlushToolCallbacks(ctx, ""); err != nil && !errors.Is(err, context.Canceled) {
		w.logf("worker 重连补报客户端工具结果失败: %v", err)
	}
	config := w.currentConfig()
	checkpoints, err := w.provider.ListResumableAgentLoopCheckpoints(ctx, provider.AgentLoopCheckpointQuery{
		ClientID: strings.TrimSpace(config.ClientID), DeviceID: strings.TrimSpace(config.DeviceID),
	})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			w.logf("主动发现 AgentLoop 检查点失败: %v", err)
		}
		return
	}
	for _, checkpoint := range checkpoints {
		if ctx.Err() != nil {
			return
		}
		if checkpoint.ID == "" || checkpoint.SessionID == "" || checkpoint.RunID == "" || checkpoint.Version <= 0 ||
			checkpoint.Status != "ready" || checkpoint.PauseReason != "network" || checkpoint.RequiresUserResume ||
			(!checkpoint.ExpiresAt.IsZero() && !w.agentLoop.store.Now().Before(checkpoint.ExpiresAt)) {
			continue
		}
		active, err := w.agentLoop.controller.PrimaryActive(checkpoint.SessionID)
		if err != nil {
			w.logf("确认 AgentLoop 原端活跃状态失败: checkpoint_id=%s err=%v", checkpoint.ID, err)
			continue
		}
		if active {
			w.logf("AgentLoop 原端仍活跃，跳过 worker 主动恢复: session_id=%s checkpoint_id=%s", checkpoint.SessionID, checkpoint.ID)
			continue
		}
		if _, err := w.agentLoop.store.RecordCheckpointState(checkpoint); err != nil {
			w.logf("保存主动发现的 AgentLoop 检查点失败: checkpoint_id=%s err=%v", checkpoint.ID, err)
			continue
		}
		select {
		case w.semaphore <- struct{}{}:
		case <-ctx.Done():
			return
		}
		func() {
			defer func() { <-w.semaphore }()
			stream, _, _, err := w.agentLoop.controller.ResumeAutomatic(ctx, checkpoint.SessionID)
			if err != nil {
				if !errors.Is(err, agentloop.ErrClaimed) && !errors.Is(err, agentloop.ErrAlreadyRunning) && !errors.Is(err, context.Canceled) {
					w.logf("主动恢复 AgentLoop 失败: checkpoint_id=%s err=%v", checkpoint.ID, err)
				}
				return
			}
			w.consumeAgentLoopWorkerStream(ctx, stream, checkpoint.RunID)
		}()
	}
}

func (w *collaborationWorker) consumeAgentLoopWorkerStream(ctx context.Context, stream <-chan provider.StreamEvent, runID string) {
	for event := range stream {
		switch event.Type {
		case provider.EventBuiltIn:
			if err := w.runtime.HandleBuiltInEvent(ctx, w.provider, w.currentConfig(), event.Data); err != nil {
				w.logf("AgentLoop 恢复流内置工具回调失败: run_id=%s err=%v", runID, err)
			}
		case provider.EventClientTool:
			if err := w.runtime.HandleClientToolEvent(ctx, w.provider, w.currentConfig(), event.Data); err != nil {
				w.logf("AgentLoop 恢复流客户端工具回调失败: run_id=%s err=%v", runID, err)
			}
		case provider.EventError:
			w.logf("AgentLoop 自动恢复流结束: run_id=%s err=%v", runID, stringValue(event.Data["error"]))
		}
	}
}
