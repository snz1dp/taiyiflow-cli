package runtime

import (
	"context"
	"io"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestExecuteClientToolPayloadEmitsPreAndPostHooks(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	events := []HookEvent{}
	runtimeClient.RegisterHookHandler(func(_ internalconfig.Config, event HookEvent) error {
		events = append(events, event)
		return nil
	})

	toolName, result, err := runtimeClient.ExecuteClientToolPayload(context.Background(), internalconfig.Config{Workdir: t.TempDir()}, map[string]any{
		"flow_id": "demo-flow",
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": "session-1", "run_id": "run-1", "message_id": "message-1"},
		"function": map[string]any{
			"id":   "call-1",
			"name": "task_list",
		},
	})
	if err != nil {
		t.Fatalf("expected task_list to succeed, got %v", err)
	}
	if toolName != "task_list" {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if result == nil {
		t.Fatal("expected non-nil tool result")
	}
	if len(events) != 2 {
		t.Fatalf("expected pre/post hooks, got %#v", events)
	}
	if events[0].Name != HookPreToolCall || events[1].Name != HookPostToolCall {
		t.Fatalf("unexpected hook order: %#v", events)
	}
	if events[0].ToolName != "task_list" || events[1].ToolName != "task_list" {
		t.Fatalf("expected task_list hook payloads, got %#v", events)
	}
	if events[1].Success == nil || !*events[1].Success {
		t.Fatalf("expected successful post hook, got %#v", events[1])
	}
}
