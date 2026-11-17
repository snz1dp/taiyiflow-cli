package runtime

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agentloop"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

func TestRecordCheckpointStateSkipsWhenPrimaryActive(t *testing.T) {
	store := agentloop.NewStore(t.TempDir())
	if _, err := store.RecordPendingStart("flow-1", "session-1", "run-1", "client-1", "device-1"); err != nil {
		t.Fatal(err)
	}
	worker := &collaborationWorker{
		logf: func(string, ...any) {},
		agentLoop: &workerAgentLoopCoordinator{
			store:      store,
			controller: agentloop.NewController(nil, store),
		},
	}
	worker.recordAgentLoopCheckpointState(agentLoopResumeNotification{
		NotificationID: "n-1", CheckpointID: "cp-1", CheckpointVersion: 2,
		SessionID: "session-1", RunID: "run-1", FlowID: "flow-1",
		Status: "ready", PauseReason: "network",
	})
	state, err := store.LoadRecovery("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.CheckpointID != "" {
		t.Fatalf("原端活跃时不得用 checkpoint_state 抹掉 Active 标志: %#v", state)
	}
}

func TestSubmitCallbackPermanentRejectionDiscardsQueuedRecord(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.SetCallbackRetryDelay(time.Millisecond, 3)
	store := agentloop.NewStore(t.TempDir())
	rt.SetAgentLoopStore(store)
	stub := &callbackProviderStub{callbackErr: &provider.HTTPError{StatusCode: 403, Body: "forbidden"}}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	if err := rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), true, nil, ""); err != nil {
		t.Fatalf("权威拒绝应被吞掉而非中断执行: %v", err)
	}
	if stub.callbackCallCount != 1 {
		t.Fatalf("权威拒绝不应重试: %d", stub.callbackCallCount)
	}
	if _, err := store.LoadToolCallback("flow-1", "sess-1", "run-1", "call-1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("权威拒绝后应清理队列记录: %v", err)
	}
}

func TestRemoveQueuedCallbackFailureOnlyWarns(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	var stderr bytes.Buffer
	rt.stderr = &stderr
	root := t.TempDir()
	store := agentloop.NewStore(root)
	rt.SetAgentLoopStore(store)
	callback := provider.ClientCallResult{FlowID: "flow-1", SessionID: "sess-1", RunID: "run-1", CallID: "call-1"}
	if _, err := store.EnqueueToolCallback(callback, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// 把 callbacks 目录换成普通文件，使后续删除必然失败。
	callbacksDir := filepath.Join(root, "callbacks")
	if err := os.RemoveAll(callbacksDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(callbacksDir, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt.removeQueuedCallback(callback)
	if !strings.Contains(stderr.String(), "警告") {
		t.Fatalf("清理失败应仅告警: %q", stderr.String())
	}
}