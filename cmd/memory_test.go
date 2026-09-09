package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agent"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	internaltui "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/tui"
)

type memoryProviderStub struct {
	*stubProvider
	flows              *provider.FlowList
	metadata           *provider.Metadata
	lastQuery          provider.MemoryConsolidationQuery
	lastTriggerRequest provider.MemoryConsolidationTriggerRequest
}

func (s *memoryProviderStub) ListFlows(context.Context, string, string, int, int) (*provider.FlowList, error) {
	if s.flows != nil {
		return s.flows, nil
	}
	return s.stubProvider.ListFlows(context.Background(), "", "", 0, 0)
}

func (s *memoryProviderStub) GetFlow(context.Context, string) (*provider.FlowSummary, error) {
	return s.stubProvider.GetFlow(context.Background(), "")
}

func (s *memoryProviderStub) GetMetadata(context.Context, string) (*provider.Metadata, error) {
	if s.metadata != nil {
		return s.metadata, nil
	}
	return s.stubProvider.GetMetadata(context.Background(), "")
}

func (s *memoryProviderStub) ListMemoryConsolidations(_ context.Context, query provider.MemoryConsolidationQuery) (*provider.MemoryConsolidationPage, error) {
	s.lastQuery = query
	return s.stubProvider.ListMemoryConsolidations(context.Background(), query)
}

func (s *memoryProviderStub) TriggerMemoryConsolidation(_ context.Context, request provider.MemoryConsolidationTriggerRequest) (*provider.MemoryConsolidationTriggerResult, error) {
	s.lastTriggerRequest = request
	return s.stubProvider.TriggerMemoryConsolidation(context.Background(), request)
}

func TestExecuteMemoryArgsCommandStatusResolvesFlowIDAndRendersTasks(t *testing.T) {
	app := &App{}
	providerStub := &memoryProviderStub{
		stubProvider: &stubProvider{
			memoryTasks: &provider.MemoryConsolidationPage{
				Total: 1,
				Items: []provider.MemoryConsolidationTask{{
					ID:               "task-1",
					FlowID:           "flow-uuid-1",
					Status:           "succeeded",
					WindowStart:      time.Date(2026, 5, 1, 8, 0, 0, 0, time.UTC),
					WindowEnd:        time.Date(2026, 5, 2, 9, 0, 0, 0, time.UTC),
					LastSourceCount:  8,
					LastSessionCount: 5,
				}},
			},
		},
		flows: &provider.FlowList{Items: []provider.FlowSummary{{ID: "flow-uuid-1", Name: "演示智能体"}}},
	}

	output, panel, err := app.executeMemoryArgsCommand(context.Background(), providerStub, []string{"status"}, "演示智能体", "/memory")
	if err != nil {
		t.Fatalf("executeMemoryArgsCommand status failed: %v", err)
	}
	if providerStub.lastQuery.FlowID != "flow-uuid-1" {
		t.Fatalf("expected resolved flow id, got %#v", providerStub.lastQuery)
	}
	if panel == nil || !strings.Contains(output, "已完成") || !strings.Contains(output, "task-1") {
		t.Fatalf("expected memory panel output, got output=%q panel=%#v", output, panel)
	}
}

func TestExecuteMemoryArgsCommandTriggerUsesThresholdOptions(t *testing.T) {
	app := &App{}
	providerStub := &memoryProviderStub{
		stubProvider: &stubProvider{
			memoryTrigger: &provider.MemoryConsolidationTriggerResult{
				TaskID:    "task-1",
				Status:    "pending",
				Scheduled: true,
			},
		},
		flows: &provider.FlowList{Items: []provider.FlowSummary{{ID: "flow-uuid-1", Name: "演示智能体"}}},
	}

	output, panel, err := app.executeMemoryArgsCommand(context.Background(), providerStub, []string{"trigger", "hours=6", "sessions=3", "execute=off"}, "演示智能体", "/memory")
	if err != nil {
		t.Fatalf("executeMemoryArgsCommand trigger failed: %v", err)
	}
	if providerStub.lastTriggerRequest.FlowID != "flow-uuid-1" || providerStub.lastTriggerRequest.MinHours != 6 || providerStub.lastTriggerRequest.MinSessions != 3 || providerStub.lastTriggerRequest.ExecuteEnabled {
		t.Fatalf("unexpected trigger request: %#v", providerStub.lastTriggerRequest)
	}
	if panel == nil || !strings.Contains(output, "仅建账，不立即执行") {
		t.Fatalf("expected trigger output to include execute mode, got output=%q panel=%#v", output, panel)
	}
}

func TestPrintSessionStatusIncludesMemoryTierLine(t *testing.T) {
	buffer := &bytes.Buffer{}
	app := &App{stdout: buffer}
	providerStub := &stubProvider{
		sessionContext: &provider.SessionContextPreview{MemoryTiers: []string{"session", "flow_user_day", "dream"}},
	}

	app.printSessionStatus(context.Background(), providerStub, "演示智能体", "flow-uuid-1", agent.SessionState{
		SessionID:    "session-1",
		RequestMode:  provider.RequestTypeAgent,
		SessionTitle: "",
	})

	output := buffer.String()
	if !strings.Contains(output, "记忆层: 当前会话 / 当日归纳 / 长期记忆") {
		t.Fatalf("expected session status to include memory tier line, got %q", output)
	}
}

func TestBuildTUIHelpLinesIncludesMemoryCommand(t *testing.T) {
	lines := buildTUIHelpLines(internaltui.Snapshot{BackendAvailable: true})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "/memory [status|trigger]") {
		t.Fatalf("expected TUI help to include memory command, got %q", joined)
	}
}
