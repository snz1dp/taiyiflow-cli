package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func TestAttachPollingClosesWhenRunFinished(t *testing.T) {
	store := NewStore(t.TempDir())
	metadata := observerMetadata()
	metadata.Executing = false
	fake := &fakeProvider{execution: metadata, streams: []<-chan provider.StreamEvent{
		observerEvents(), observerEvents(), observerEvents(),
	}}
	controller := NewController(fake, store)
	controller.Sleep = func(context.Context, time.Duration) error { return nil }
	snapshots := 0
	if err := controller.Attach(context.Background(), ObserverRequest{SessionID: "session-1"}, ObserverHandlers{
		OnSnapshot: func(*provider.SessionMessageList) error { snapshots++; return nil },
	}); err != nil {
		t.Fatalf("run 终结后观察应正常退出而非无限轮询: %v", err)
	}
	state, err := store.LoadObserver("session-1")
	if err != nil || !state.Closed {
		t.Fatalf("终结的观察应关闭: %#v, %v", state, err)
	}
	if snapshots < 2 {
		t.Fatalf("关闭前应落地最终快照: %d", snapshots)
	}
}

func TestAttachPollingClosesWhenSessionGone(t *testing.T) {
	store := NewStore(t.TempDir())
	fake := &fakeProvider{execution: observerMetadata(), streams: []<-chan provider.StreamEvent{
		observerEvents(), observerEvents(), observerEvents(),
	}}
	controller := NewController(fake, store)
	controller.Sleep = func(context.Context, time.Duration) error {
		fake.executionErr = &provider.HTTPError{StatusCode: 404, Body: "session not found"}
		return nil
	}
	if err := controller.Attach(context.Background(), ObserverRequest{SessionID: "session-1"}, ObserverHandlers{}); err != nil {
		t.Fatalf("会话已删除时观察应退出: %v", err)
	}
	state, err := store.LoadObserver("session-1")
	if err != nil || !state.Closed {
		t.Fatalf("会话已删除的观察应关闭: %#v, %v", state, err)
	}
}

func TestAttachKeepsObservingOnFreezeTerminal(t *testing.T) {
	store := NewStore(t.TempDir())
	first := observerEvents(
		provider.StreamEvent{ID: "event-1", Type: provider.EventMessage, Data: map[string]any{"name": "message", "run_id": "run-1"}},
		provider.StreamEvent{ID: "event-2", Type: provider.EventClose, Data: map[string]any{"name": "close", "run_id": "run-1", "run_finished": false}},
	)
	second := observerEvents(provider.StreamEvent{ID: "event-3", Type: provider.EventClose, Data: map[string]any{"name": "close", "run_id": "run-1", "run_finished": true}})
	fake := &fakeProvider{execution: observerMetadata(), streams: []<-chan provider.StreamEvent{first, second}}
	controller := NewController(fake, store)
	resumed := false
	controller.Sleep = func(context.Context, time.Duration) error {
		if !resumed {
			resumed = true
			// 模拟原端/worker 续跑成功，解除 AwaitingResume 后观察应重连。
			_, err := store.RecordAcceptedExecution("session-1", "run-1", "attempt-1")
			return err
		}
		return nil
	}
	if err := controller.Attach(context.Background(), ObserverRequest{SessionID: "session-1"}, ObserverHandlers{
		OnEvent: func(event provider.StreamEvent) error {
			if event.EventID() == "event-1" {
				// 模拟原端在冻结时落地的恢复状态（共享存储）。
				if _, err := store.RecordPendingStartRequest(provider.StreamRequest{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1"}); err != nil {
					return err
				}
				if _, err := store.MarkDisconnected("session-1", "run-1"); err != nil {
					return err
				}
			}
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("冻结断流后应重连而非关闭观察: %d 次连接", len(fake.requests))
	}
	state, err := store.LoadObserver("session-1")
	if err != nil || !state.Closed || state.CleanCloses != 1 {
		t.Fatalf("真终态才应关闭观察: %#v, %v", state, err)
	}
}

func TestControlRollsBackOnDefinitiveRejection(t *testing.T) {
	store := NewStore(t.TempDir())
	fake := &fakeProvider{controlErr: &provider.HTTPError{StatusCode: 400, Body: "run not pausable"}}
	controller := NewController(fake, store)
	if _, err := store.RecordPendingStart("flow-1", "session-1", "run-1", "client-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Pause(context.Background(), "session-1", "run-1"); err == nil {
		t.Fatal("服务端拒绝时应返回错误")
	}
	state, err := store.LoadRecovery("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Intent != IntentNone || state.InvalidatedByUser || !state.Active || !state.PendingStart || state.ControlRequestID != "" {
		t.Fatalf("明确拒绝后应恢复控制前意图: %#v", state)
	}
}

func TestControlKeepsIntentOnUncertainFailure(t *testing.T) {
	store := NewStore(t.TempDir())
	fake := &fakeProvider{controlErr: errors.New("network down")}
	controller := NewController(fake, store)
	if _, err := store.RecordPendingStart("flow-1", "session-1", "run-1", "client-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Pause(context.Background(), "session-1", "run-1"); err == nil {
		t.Fatal("网络错误时应返回错误")
	}
	state, err := store.LoadRecovery("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.Intent != IntentUserPause || !state.InvalidatedByUser {
		t.Fatalf("结果不确定时应保持 fail-safe 本地意图: %#v", state)
	}
}

func TestClearRecoveryFailsClosedWhenLocked(t *testing.T) {
	store := NewStore(t.TempDir())
	if _, err := store.RecordPendingStart("flow-1", "session-1", "run-1", "client-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	lockPath := store.recoveryPath("session-1") + ".lock"
	if err := os.WriteFile(lockPath, []byte(`{"owner":"other-process","status":"state"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearRecovery("session-1", "run-1"); !errors.Is(err, ErrClaimed) {
		t.Fatalf("跨进程锁占用时应返回 ErrClaimed: %v", err)
	}
	if _, err := os.Stat(store.recoveryPath("session-1")); err != nil {
		t.Fatalf("锁占用时不得删除恢复状态: %v", err)
	}
}

func TestClaimRecoveryStaleStealIsExclusive(t *testing.T) {
	store := NewStore(t.TempDir())
	path := store.path("claims", fmt.Sprintf("%s-%d.claim", stateKey("cp-1"), 1))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	staleTime := time.Now().Add(-3 * time.Minute)
	content, err := json.Marshal(fileClaim{Owner: "dead-owner", CreatedAt: staleTime, UpdatedAt: staleTime})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, staleTime, staleTime); err != nil {
		t.Fatal(err)
	}
	const racers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	acquired := 0
	var winner *RecoveryClaim
	for index := 0; index < racers; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claim, got, err := store.ClaimRecovery("cp-1", 1)
			if err != nil {
				t.Errorf("认领失败: %v", err)
				return
			}
			if got {
				mu.Lock()
				acquired++
				winner = claim
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if acquired != 1 {
		t.Fatalf("过期锁清理必须独占，实际 %d 个认领者", acquired)
	}
	if winner != nil {
		_ = winner.Release()
	}
}

func TestFreezeTerminalResumableWithoutCheckpoint(t *testing.T) {
	store := NewStore(t.TempDir())
	controller := NewController(&fakeProvider{}, store)
	if _, err := store.RecordPendingStart("flow-1", "session-1", "run-1", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkDisconnected("session-1", "run-1"); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadRecovery("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.AutoResumable() {
		t.Fatal("无检查点时不应满足完整自动恢复条件")
	}
	if !controller.TerminalIsResumableFreeze("session-1", EventInfo{RunFinished: false}) {
		t.Fatal("冻结终态即使本地暂无检查点也应可续跑")
	}
	if controller.TerminalIsResumableFreeze("session-1", EventInfo{RunFinished: true}) {
		t.Fatal("真终态不可续跑")
	}
}

func TestResumeAttemptSchedulesRetryOncePerFreeze(t *testing.T) {
	store := NewStore(t.TempDir())
	controller := NewController(&fakeProvider{}, store)
	if _, err := store.RecordPendingStart("flow-1", "session-1", "run-1", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkDisconnected("session-1", "run-1"); err != nil {
		t.Fatal(err)
	}
	state, err := store.LoadRecovery("session-1")
	if err != nil {
		t.Fatal(err)
	}
	attempt := &ResumeAttempt{controller: controller, state: state}
	freeze := provider.StreamEvent{Type: provider.EventMessage, Data: map[string]any{
		"name": "agent_loop_network_freeze_requested", "session_id": "session-1", "run_id": "run-1",
	}}
	if _, err := attempt.Observe(freeze); err != nil {
		t.Fatal(err)
	}
	attempt.Close()
	state, err = store.LoadRecovery("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if state.RecoveryAttempts != 1 {
		t.Fatalf("冻结事件与 Close 只应登记一次重试: %d", state.RecoveryAttempts)
	}
}

func TestListDueToolCallbacksQuarantinesCorruptRecords(t *testing.T) {
	store := NewStore(t.TempDir())
	result := provider.ClientCallResult{FlowID: "flow-1", SessionID: "session-1", RunID: "run-1", CallID: "call-1"}
	if _, err := store.EnqueueToolCallback(result, time.Now()); err != nil {
		t.Fatal(err)
	}
	badPath := store.path("callbacks", "bad.json")
	if err := os.WriteFile(badPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueToolCallbacks(time.Now())
	if err != nil || len(due) != 1 {
		t.Fatalf("坏记录不应阻塞正常补报: %d, %v", len(due), err)
	}
	if _, err := os.Stat(badPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("坏记录应被移出队列: %v", err)
	}
	if _, err := os.Stat(badPath + ".corrupt"); err != nil {
		t.Fatalf("坏记录应改名保留供排查: %v", err)
	}
}

func TestLateToolResultRejectedRequiresHTTPError(t *testing.T) {
	if IsLateToolResultRejected(fmt.Errorf("迟到结果")) {
		t.Fatal("非 HTTP 错误不得按关键词丢弃回调")
	}
	if !IsLateToolResultRejected(&provider.HTTPError{StatusCode: 400, Body: "tool_call_paused"}) {
		t.Fatal("400 + tool_call_paused 应识别为丢弃信号")
	}
}