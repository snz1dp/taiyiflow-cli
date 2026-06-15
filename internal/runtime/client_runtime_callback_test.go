package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// ---------------------------------------------------------------------------
// callbackProviderStub: 最小 mock，仅实现 SubmitClientCallResult 和
// UpdateClientCollaboration，其余方法通过嵌入 provider.Provider 接口零值
// 满足编译，调用时会 panic（测试中不应触达）。
// ---------------------------------------------------------------------------

type callbackProviderStub struct {
	provider.Provider
	mu                sync.Mutex
	callback          provider.ClientCallResult
	callbackCalled    bool
	callbackCallCount int
	callbackCtxDone   bool
	callbackErr       error
	updateCalled      bool
	updateTaskID      string
	updateRequest     provider.ClientCollaborationUpdateRequest
	updateErr         error
}

func (s *callbackProviderStub) SubmitClientCallResult(ctx context.Context, result provider.ClientCallResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callback = result
	s.callbackCalled = true
	s.callbackCallCount++
	s.callbackCtxDone = ctx.Err() != nil
	return s.callbackErr
}

func (s *callbackProviderStub) UpdateClientCollaboration(_ context.Context, taskID string, request provider.ClientCollaborationUpdateRequest) (*provider.ClientCollaborationTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updateCalled = true
	s.updateTaskID = taskID
	s.updateRequest = request
	if s.updateErr != nil {
		return nil, s.updateErr
	}
	return &provider.ClientCollaborationTask{ID: taskID, Status: request.Status}, nil
}

// ---------------------------------------------------------------------------
// transientFailStub: 前 failCount 次调用返回错误，之后成功。用于测试重试行为。
// ---------------------------------------------------------------------------

type transientFailStub struct {
	provider.Provider
	mu         sync.Mutex
	failCount  int
	callCount  int
	failErr    error
	lastResult provider.ClientCallResult
}

func (s *transientFailStub) SubmitClientCallResult(_ context.Context, result provider.ClientCallResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callCount++
	s.lastResult = result
	if s.callCount <= s.failCount {
		return s.failErr
	}
	return nil
}

// validCallbackPayload 构造合法的回调 payload
func validCallbackPayload() map[string]any {
	return map[string]any{
		"flow_id": "flow-1",
		"context": map[string]any{
			"session_id": "sess-1",
			"message_id": "msg-1",
			"run_id":     "run-1",
		},
		"function": map[string]any{
			"id":   "call-1",
			"name": "test_tool",
		},
	}
}

// ===========================================================================
// DetachedCallbackContext 测试
// ===========================================================================

func TestDetachedCallbackContext_PositiveTimeout(t *testing.T) {
	parent := context.Background()
	timeout := 5 * time.Second

	ctx, cancel := DetachedCallbackContext(parent, timeout)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("期望 DetachedCallbackContext 返回带 Deadline 的上下文")
	}

	remaining := time.Until(deadline)
	if remaining < 4*time.Second || remaining > 6*time.Second {
		t.Fatalf("Deadline 应在 ~5s 后到期，实际剩余 %v", remaining)
	}
}

func TestDetachedCallbackContext_ZeroTimeout_FallsBackToDefault(t *testing.T) {
	parent := context.Background()

	ctx, cancel := DetachedCallbackContext(parent, 0)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("timeout=0 时应回退到默认超时，期望带 Deadline 的上下文")
	}

	expected := time.Duration(internalconfig.DefaultHTTPTimeoutSeconds) * time.Second
	remaining := time.Until(deadline)
	if remaining < expected-2*time.Second || remaining > expected+2*time.Second {
		t.Fatalf("timeout=0 时应回退到 %v，实际剩余 %v", expected, remaining)
	}
}

func TestDetachedCallbackContext_NegativeTimeout_FallsBackToDefault(t *testing.T) {
	parent := context.Background()

	ctx, cancel := DetachedCallbackContext(parent, -10*time.Second)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("timeout<0 时应回退到默认超时，期望带 Deadline 的上下文")
	}

	expected := time.Duration(internalconfig.DefaultHTTPTimeoutSeconds) * time.Second
	remaining := time.Until(deadline)
	if remaining < expected-2*time.Second || remaining > expected+2*time.Second {
		t.Fatalf("timeout<0 时应回退到 %v，实际剩余 %v", expected, remaining)
	}
}

func TestDetachedCallbackContext_ParentCanceled_DetachedSurvives(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	timeout := 5 * time.Second

	ctx, cancel := DetachedCallbackContext(parent, timeout)
	defer cancel()

	parentCancel()
	time.Sleep(50 * time.Millisecond)

	if ctx.Err() != nil {
		t.Fatalf("父上下文已取消，但脱离的上下文不应受影响，实际 err=%v", ctx.Err())
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("脱离的上下文应保留 Deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 4*time.Second {
		t.Fatalf("脱离的上下文 Deadline 不应被缩短，实际剩余 %v", remaining)
	}
}

func TestDetachedCallbackContext_ParentTimedOut_DetachedSurvives(t *testing.T) {
	parent, parentCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer parentCancel()

	ctx, cancel := DetachedCallbackContext(parent, 5*time.Second)
	defer cancel()

	time.Sleep(100 * time.Millisecond)

	if ctx.Err() != nil {
		t.Fatalf("父上下文已超时，但脱离的上下文不应受影响，实际 err=%v", ctx.Err())
	}
}

func TestDetachedCallbackContext_PreservesParentValues(t *testing.T) {
	type ctxKey string
	parent := context.WithValue(context.Background(), ctxKey("auth"), "Bearer test-token-123")

	ctx, cancel := DetachedCallbackContext(parent, 5*time.Second)
	defer cancel()

	got := ctx.Value(ctxKey("auth"))
	if got != "Bearer test-token-123" {
		t.Fatalf("脱离的上下文应保留父上下文的 Value，期望 %q，实际 %v", "Bearer test-token-123", got)
	}
}

func TestDetachedCallbackContext_PreservesNestedValues(t *testing.T) {
	type ctxKey string
	parent := context.WithValue(context.Background(), ctxKey("auth"), "token-abc")
	parent = context.WithValue(parent, ctxKey("request_id"), "req-456")

	ctx, cancel := DetachedCallbackContext(parent, 5*time.Second)
	defer cancel()

	if ctx.Value(ctxKey("auth")) != "token-abc" {
		t.Fatal("应保留第一层 Value")
	}
	if ctx.Value(ctxKey("request_id")) != "req-456" {
		t.Fatal("应保留嵌套层 Value")
	}
}

func TestDetachedCallbackContext_CancelFuncWorks(t *testing.T) {
	parent := context.Background()
	ctx, cancel := DetachedCallbackContext(parent, 10*time.Second)

	cancel()

	if ctx.Err() == nil {
		t.Fatal("调用 cancel 后上下文应被取消")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("cancel 后 err 应为 context.Canceled，实际 %v", ctx.Err())
	}
}

func TestDetachedCallbackContext_DeadlineExpires(t *testing.T) {
	parent := context.Background()
	timeout := 100 * time.Millisecond

	ctx, cancel := DetachedCallbackContext(parent, timeout)
	defer cancel()

	time.Sleep(200 * time.Millisecond)

	if ctx.Err() == nil {
		t.Fatal("超时后上下文应被取消")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("超时后 err 应为 context.DeadlineExceeded，实际 %v", ctx.Err())
	}
}

func TestDetachedCallbackContext_BackgroundParent(t *testing.T) {
	ctx, cancel := DetachedCallbackContext(context.Background(), 3*time.Second)
	defer cancel()

	if ctx.Err() != nil {
		t.Fatalf("Background 父上下文不应被取消，实际 err=%v", ctx.Err())
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("应设置 Deadline")
	}
	remaining := time.Until(deadline)
	if remaining < 2*time.Second || remaining > 4*time.Second {
		t.Fatalf("Deadline 应在 ~3s 后到期，实际剩余 %v", remaining)
	}
}

func TestDetachedCallbackContext_ConcurrentUsage(t *testing.T) {
	const goroutines = 20
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			parent, parentCancel := context.WithCancel(context.Background())
			defer parentCancel()

			ctx, ctxCancel := DetachedCallbackContext(parent, 2*time.Second)
			defer ctxCancel()

			parentCancel()
			time.Sleep(10 * time.Millisecond)

			if ctx.Err() != nil {
				errCh <- fmt.Errorf("goroutine %d: 脱离的上下文不应被父取消影响，实际 err=%v", idx, ctx.Err())
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatal(err)
	}
}

// ===========================================================================
// submitCallback 测试
// ===========================================================================

func TestSubmitCallback_MissingFlowID(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	payload := map[string]any{
		"context":  map[string]any{"session_id": "sess-1", "run_id": "run-1"},
		"function": map[string]any{"id": "call-1"},
	}

	err := rt.submitCallback(context.Background(), stub, config, payload, true, nil, "")
	if err == nil {
		t.Fatal("缺少 flow_id 时应返回错误")
	}
	if stub.callbackCalled {
		t.Fatal("缺少关键字段时不应调用 Provider")
	}
}

func TestSubmitCallback_MissingRunID(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	payload := map[string]any{
		"flow_id":  "flow-1",
		"context":  map[string]any{"session_id": "sess-1"},
		"function": map[string]any{"id": "call-1"},
	}

	err := rt.submitCallback(context.Background(), stub, config, payload, true, nil, "")
	if err == nil {
		t.Fatal("缺少 run_id 时应返回错误")
	}
}

func TestSubmitCallback_MissingCallID(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	payload := map[string]any{
		"flow_id":  "flow-1",
		"context":  map[string]any{"run_id": "run-1"},
		"function": map[string]any{"name": "test_tool"},
	}

	err := rt.submitCallback(context.Background(), stub, config, payload, true, nil, "")
	if err == nil {
		t.Fatal("缺少 call_id 时应返回错误")
	}
}

func TestSubmitCallback_EmptyPayload(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	err := rt.submitCallback(context.Background(), stub, config, map[string]any{}, true, nil, "")
	if err == nil {
		t.Fatal("空 payload 应返回缺少关键字段错误")
	}
}

func TestSubmitCallback_Success(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	result := map[string]any{"output": "hello"}
	err := rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), true, result, "")
	if err != nil {
		t.Fatalf("合法 payload 不应返回错误，实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应调用 Provider.SubmitClientCallResult")
	}
	if stub.callback.FlowID != "flow-1" {
		t.Fatalf("FlowID 应为 flow-1，实际 %s", stub.callback.FlowID)
	}
	if stub.callback.SessionID != "sess-1" {
		t.Fatalf("SessionID 应为 sess-1，实际 %s", stub.callback.SessionID)
	}
	if stub.callback.MessageID != "msg-1" {
		t.Fatalf("MessageID 应为 msg-1，实际 %s", stub.callback.MessageID)
	}
	if stub.callback.RunID != "run-1" {
		t.Fatalf("RunID 应为 run-1，实际 %s", stub.callback.RunID)
	}
	if stub.callback.CallID != "call-1" {
		t.Fatalf("CallID 应为 call-1，实际 %s", stub.callback.CallID)
	}
	if !stub.callback.Success {
		t.Fatal("Success 应为 true")
	}
	if stub.callback.ErrorMessage != "" {
		t.Fatalf("ErrorMessage 应为空，实际 %s", stub.callback.ErrorMessage)
	}
}

func TestSubmitCallback_Failure(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	err := rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), false, nil, "工具执行失败")
	if err != nil {
		t.Fatalf("合法 payload 不应返回错误，实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应调用 Provider.SubmitClientCallResult")
	}
	if stub.callback.Success {
		t.Fatal("Success 应为 false")
	}
	if stub.callback.ErrorMessage != "工具执行失败" {
		t.Fatalf("ErrorMessage 应为 '工具执行失败'，实际 %s", stub.callback.ErrorMessage)
	}
}

func TestSubmitCallback_ProviderReturnsError(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.SetCallbackRetryDelay(time.Millisecond, 3)
	stub := &callbackProviderStub{callbackErr: fmt.Errorf("network error")}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	err := rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), true, nil, "")
	if err == nil {
		t.Fatal("Provider 返回错误时应向上传播")
	}
	if !strings.Contains(err.Error(), "network error") {
		t.Fatalf("错误应包含 'network error'，实际: %v", err)
	}
}

func TestSubmitCallback_ParentCanceled_DetachedStillWorks(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	parent, parentCancel := context.WithCancel(context.Background())
	parentCancel()

	err := rt.submitCallback(parent, stub, config, validCallbackPayload(), true, nil, "")
	if err != nil {
		t.Fatalf("即使父上下文已取消，submitCallback 仍应通过脱离上下文成功提交，实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应成功提交回调结果")
	}
	if stub.callbackCtxDone {
		t.Fatal("Provider 收到的上下文不应被取消")
	}
}

func TestSubmitCallback_UsesConfigTimeout(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 1}

	err := rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), true, nil, "")
	if err != nil {
		t.Fatalf("1 秒超时内应完成提交，实际: %v", err)
	}
	if !stub.callbackCalled {
		t.Fatal("应成功提交回调结果")
	}
}

func TestSubmitCallback_WithResultPayload(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	result := map[string]any{
		"exit_code": 0,
		"stdout":    "hello world",
		"stderr":    "",
	}
	err := rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), true, result, "")
	if err != nil {
		t.Fatalf("不应返回错误，实际: %v", err)
	}

	gotResult, ok := stub.callback.Result.(map[string]any)
	if !ok {
		t.Fatalf("Result 应为 map[string]any，实际 %T", stub.callback.Result)
	}
	if gotResult["stdout"] != "hello world" {
		t.Fatalf("Result.stdout 应为 'hello world'，实际 %v", gotResult["stdout"])
	}
}

// ===========================================================================
// SubmitClientToolCallback 测试
// ===========================================================================

func TestSubmitClientToolCallback_WithError(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	toolErr := fmt.Errorf("shell 命令执行超时")
	err := rt.SubmitClientToolCallback(context.Background(), stub, config, validCallbackPayload(), nil, toolErr)
	if err != nil {
		t.Fatalf("SubmitClientToolCallback 不应返回错误，实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应提交回调结果")
	}
	if stub.callback.Success {
		t.Fatal("有 toolErr 时 Success 应为 false")
	}
	if stub.callback.ErrorMessage != "shell 命令执行超时" {
		t.Fatalf("ErrorMessage 应包含工具错误信息，实际: %s", stub.callback.ErrorMessage)
	}
}

func TestSubmitClientToolCallback_Success(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	result := map[string]any{"exit_code": 0, "stdout": "hello world"}
	err := rt.SubmitClientToolCallback(context.Background(), stub, config, validCallbackPayload(), result, nil)
	if err != nil {
		t.Fatalf("SubmitClientToolCallback 不应返回错误，实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应提交回调结果")
	}
	if !stub.callback.Success {
		t.Fatal("无 toolErr 时 Success 应为 true")
	}
	if stub.callback.ErrorMessage != "" {
		t.Fatalf("无 toolErr 时 ErrorMessage 应为空，实际: %s", stub.callback.ErrorMessage)
	}
}

func TestSubmitClientToolCallback_MissingFields(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	err := rt.SubmitClientToolCallback(context.Background(), stub, config, map[string]any{}, nil, nil)
	if err == nil {
		t.Fatal("空 payload 应返回缺少关键字段错误")
	}
}

func TestSubmitClientToolCallback_NilError_SubmitsSuccess(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	err := rt.SubmitClientToolCallback(context.Background(), stub, config, validCallbackPayload(), "some-result", nil)
	if err != nil {
		t.Fatalf("不应返回错误，实际: %v", err)
	}

	if !stub.callback.Success {
		t.Fatal("err=nil 时应提交 Success=true")
	}
}

func TestSubmitClientToolCallback_ParentCanceled(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	parent, parentCancel := context.WithCancel(context.Background())
	parentCancel()

	err := rt.SubmitClientToolCallback(parent, stub, config, validCallbackPayload(), "result", nil)
	if err != nil {
		t.Fatalf("即使父上下文已取消，仍应通过脱离上下文成功提交，实际: %v", err)
	}
	if !stub.callbackCalled {
		t.Fatal("应成功提交回调结果")
	}
}

// ===========================================================================
// HandleBuiltInEvent 测试
// ===========================================================================

func TestHandleBuiltInEvent_UnsupportedServer(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.interactivePromptsEnabled = false
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	payload := map[string]any{
		"flow_id":  "flow-1",
		"context":  map[string]any{"session_id": "sess-1", "run_id": "run-1"},
		"function": map[string]any{"id": "call-1"},
		"server":   map[string]any{"name": "unknown_server"},
		"command":  map[string]any{"name": "some_command"},
	}

	err := rt.HandleBuiltInEvent(context.Background(), stub, config, payload)
	if err != nil {
		t.Fatalf("HandleBuiltInEvent 不应返回错误（错误通过回调提交），实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应提交失败回调")
	}
	if stub.callback.Success {
		t.Fatal("不支持的 server 应提交 Success=false")
	}
	if stub.callback.ErrorMessage == "" {
		t.Fatal("ErrorMessage 不应为空")
	}
}

func TestHandleBuiltInEvent_UnsupportedCommand(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.interactivePromptsEnabled = false
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	payload := map[string]any{
		"flow_id":  "flow-1",
		"context":  map[string]any{"session_id": "sess-1", "run_id": "run-1"},
		"function": map[string]any{"id": "call-1"},
		"server":   map[string]any{"name": "built_in_command"},
		"command":  map[string]any{"name": "unknown_command"},
	}

	err := rt.HandleBuiltInEvent(context.Background(), stub, config, payload)
	if err != nil {
		t.Fatalf("HandleBuiltInEvent 不应返回错误，实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应提交失败回调")
	}
	if stub.callback.Success {
		t.Fatal("不支持的 command 应提交 Success=false")
	}
}

func TestHandleBuiltInEvent_ParentCanceled_StillSubmits(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.interactivePromptsEnabled = false
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	parent, parentCancel := context.WithCancel(context.Background())
	parentCancel()

	payload := map[string]any{
		"flow_id":  "flow-1",
		"context":  map[string]any{"session_id": "sess-1", "run_id": "run-1"},
		"function": map[string]any{"id": "call-1"},
		"server":   map[string]any{"name": "unknown_server"},
		"command":  map[string]any{"name": "some_command"},
	}

	err := rt.HandleBuiltInEvent(parent, stub, config, payload)
	if err != nil {
		t.Fatalf("即使父上下文已取消，HandleBuiltInEvent 仍应通过脱离上下文成功提交，实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应成功提交回调结果")
	}
}

// ===========================================================================
// HandleClientToolEvent 测试
// ===========================================================================

func TestHandleClientToolEvent_UnsupportedTool(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.interactivePromptsEnabled = false
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30, Workdir: t.TempDir()}

	payload := map[string]any{
		"flow_id":  "flow-1",
		"context":  map[string]any{"session_id": "sess-1", "run_id": "run-1"},
		"function": map[string]any{"id": "call-1", "name": "nonexistent_tool"},
		"server":   map[string]any{"name": "local"},
	}

	err := rt.HandleClientToolEvent(context.Background(), stub, config, payload)
	if err != nil {
		t.Fatalf("HandleClientToolEvent 不应返回错误，实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应提交失败回调")
	}
	if stub.callback.Success {
		t.Fatal("不支持的工具应提交 Success=false")
	}
}

func TestHandleClientToolEvent_UnsupportedServer(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.interactivePromptsEnabled = false
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	payload := map[string]any{
		"flow_id":  "flow-1",
		"context":  map[string]any{"session_id": "sess-1", "run_id": "run-1"},
		"function": map[string]any{"id": "call-1", "name": "shell_exec"},
		"server":   map[string]any{"name": "remote_server"},
	}

	err := rt.HandleClientToolEvent(context.Background(), stub, config, payload)
	if err != nil {
		t.Fatalf("HandleClientToolEvent 不应返回错误，实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应提交失败回调")
	}
	if stub.callback.Success {
		t.Fatal("不支持的 server 应提交 Success=false")
	}
}

func TestHandleClientToolEvent_ParentCanceled_StillSubmits(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.interactivePromptsEnabled = false
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	parent, parentCancel := context.WithCancel(context.Background())
	parentCancel()

	payload := map[string]any{
		"flow_id":  "flow-1",
		"context":  map[string]any{"session_id": "sess-1", "run_id": "run-1"},
		"function": map[string]any{"id": "call-1", "name": "nonexistent_tool"},
		"server":   map[string]any{"name": "local"},
	}

	err := rt.HandleClientToolEvent(parent, stub, config, payload)
	if err != nil {
		t.Fatalf("即使父上下文已取消，HandleClientToolEvent 仍应通过脱离上下文成功提交，实际: %v", err)
	}

	if !stub.callbackCalled {
		t.Fatal("应成功提交回调结果")
	}
}

// ===========================================================================
// collaborationWorker.updateTaskFallback 测试
// ===========================================================================

func TestUpdateTaskFallback_Success(t *testing.T) {
	config := &internalconfig.Config{HTTPTimeoutSeconds: 30}
	stub := &callbackProviderStub{}
	worker := &collaborationWorker{
		provider: stub,
		config:   config,
		logf:     func(format string, args ...any) {},
	}

	request := provider.ClientCollaborationUpdateRequest{
		Status: "success",
		StatusPayload: map[string]any{
			"executor":  "test",
			"transport": "fallback",
		},
	}

	worker.updateTaskFallback(context.Background(), "task-123", request)

	if !stub.updateCalled {
		t.Fatal("应调用 UpdateClientCollaboration")
	}
	if stub.updateTaskID != "task-123" {
		t.Fatalf("taskID 应为 task-123，实际 %s", stub.updateTaskID)
	}
	if stub.updateRequest.Status != "success" {
		t.Fatalf("Status 应为 success，实际 %s", stub.updateRequest.Status)
	}
}

func TestUpdateTaskFallback_ParentCanceled_StillSubmits(t *testing.T) {
	config := &internalconfig.Config{HTTPTimeoutSeconds: 30}
	stub := &callbackProviderStub{}
	worker := &collaborationWorker{
		provider: stub,
		config:   config,
		logf:     func(format string, args ...any) {},
	}

	parent, parentCancel := context.WithCancel(context.Background())
	parentCancel()

	request := provider.ClientCollaborationUpdateRequest{
		Status: "failed",
	}

	worker.updateTaskFallback(parent, "task-456", request)

	if !stub.updateCalled {
		t.Fatal("即使父上下文已取消，updateTaskFallback 仍应通过脱离上下文成功提交")
	}
}

func TestUpdateTaskFallback_ProviderError_DoesNotPanic(t *testing.T) {
	config := &internalconfig.Config{HTTPTimeoutSeconds: 30}
	stub := &callbackProviderStub{updateErr: fmt.Errorf("server unavailable")}
	worker := &collaborationWorker{
		provider: stub,
		config:   config,
		logf:     func(format string, args ...any) {},
	}

	request := provider.ClientCollaborationUpdateRequest{
		Status: "failed",
	}

	worker.updateTaskFallback(context.Background(), "task-789", request)

	if !stub.updateCalled {
		t.Fatal("应调用 UpdateClientCollaboration")
	}
}

func TestUpdateTaskFallback_NilConfig_UsesDefaultTimeout(t *testing.T) {
	stub := &callbackProviderStub{}
	worker := &collaborationWorker{
		provider: stub,
		config:   nil,
		logf:     func(format string, args ...any) {},
	}

	request := provider.ClientCollaborationUpdateRequest{
		Status: "success",
	}

	worker.updateTaskFallback(context.Background(), "task-nil", request)

	if !stub.updateCalled {
		t.Fatal("nil config 时应使用默认超时并成功提交")
	}
}

func TestUpdateTaskFallback_WithConfigTimeout(t *testing.T) {
	config := &internalconfig.Config{HTTPTimeoutSeconds: 120}
	stub := &callbackProviderStub{}
	worker := &collaborationWorker{
		provider: stub,
		config:   config,
		logf:     func(format string, args ...any) {},
	}

	request := provider.ClientCollaborationUpdateRequest{
		Status: "running",
	}

	worker.updateTaskFallback(context.Background(), "task-timeout", request)

	if !stub.updateCalled {
		t.Fatal("应使用配置中的超时时间并成功提交")
	}
}

// ===========================================================================
// 并发安全性测试
// ===========================================================================

func TestSubmitCallback_ConcurrentSubmissions(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.SetCallbackRetryDelay(time.Millisecond, 3)
	stub := &callbackProviderStub{}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	const goroutines = 10
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			payload := map[string]any{
				"flow_id": fmt.Sprintf("flow-%d", idx),
				"context": map[string]any{
					"session_id": "sess-1",
					"run_id":     fmt.Sprintf("run-%d", idx),
				},
				"function": map[string]any{
					"id":   fmt.Sprintf("call-%d", idx),
					"name": "test_tool",
				},
			}
			if err := rt.submitCallback(context.Background(), stub, config, payload, true, nil, ""); err != nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("并发提交不应返回错误，实际: %v", err)
	}
}

// ===========================================================================
// 回调重试行为测试
// ===========================================================================

func TestSubmitCallback_RetrySucceedsAfterTransientFailure(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.SetCallbackRetryDelay(time.Millisecond, 3)
	stub := &transientFailStub{failCount: 2, failErr: fmt.Errorf("transient error")}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	err := rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), true, nil, "")
	if err != nil {
		t.Fatalf("重试后应成功，实际: %v", err)
	}
	if stub.callCount != 3 {
		t.Fatalf("应在第 3 次调用时成功，实际调用 %d 次", stub.callCount)
	}
}

func TestSubmitCallback_RetryExhaustedReturnsError(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.SetCallbackRetryDelay(time.Millisecond, 3)
	stub := &callbackProviderStub{callbackErr: fmt.Errorf("persistent error")}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	err := rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), true, nil, "")
	if err == nil {
		t.Fatal("重试耗尽后应返回错误")
	}
	if !strings.Contains(err.Error(), "persistent error") {
		t.Fatalf("错误应包含原始错误信息，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "已重试") {
		t.Fatalf("错误应包含重试次数信息，实际: %v", err)
	}
	// 1 次初始 + 3 次重试 = 4 次调用
	if stub.callbackCallCount != 4 {
		t.Fatalf("应调用 4 次（1+3），实际 %d 次", stub.callbackCallCount)
	}
}

func TestSubmitCallback_RetryRespectsCustomMaxRetries(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.SetCallbackRetryDelay(time.Millisecond, 1)
	stub := &callbackProviderStub{callbackErr: fmt.Errorf("always fail")}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	err := rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), true, nil, "")
	if err == nil {
		t.Fatal("应返回错误")
	}
	// 1 次初始 + 1 次重试 = 2 次调用
	if stub.callbackCallCount != 2 {
		t.Fatalf("maxRetries=1 时应调用 2 次（1+1），实际 %d 次", stub.callbackCallCount)
	}
}

func TestSubmitCallback_RetryDelayCausesMeasurableWait(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.SetCallbackRetryDelay(50*time.Millisecond, 2)
	stub := &callbackProviderStub{callbackErr: fmt.Errorf("fail")}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	start := time.Now()
	_ = rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), true, nil, "")
	elapsed := time.Since(start)

	// 2 次重试间隔 × 50ms = 至少 100ms
	if elapsed < 90*time.Millisecond {
		t.Fatalf("重试间隔应至少 90ms，实际 %v", elapsed)
	}
	// 1 次初始 + 2 次重试 = 3 次调用
	if stub.callbackCallCount != 3 {
		t.Fatalf("应调用 3 次（1+2），实际 %d 次", stub.callbackCallCount)
	}
}

func TestSubmitCallback_RetrySucceedsOnLastAttempt(t *testing.T) {
	rt := NewClientRuntime(nil, nil, nil)
	rt.SetCallbackRetryDelay(time.Millisecond, 3)
	// 前 3 次失败，第 4 次成功（刚好在最后一次重试时恢复）
	stub := &transientFailStub{failCount: 3, failErr: fmt.Errorf("temporary")}
	config := internalconfig.Config{HTTPTimeoutSeconds: 30}

	err := rt.submitCallback(context.Background(), stub, config, validCallbackPayload(), true, nil, "")
	if err != nil {
		t.Fatalf("最后一次重试应成功，实际: %v", err)
	}
	if stub.callCount != 4 {
		t.Fatalf("应在第 4 次调用时成功，实际调用 %d 次", stub.callCount)
	}
}
