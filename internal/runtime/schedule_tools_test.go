package runtime

import (
	"context"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// stubScheduleProvider 实现 schedule 相关的 Provider 接口。
type stubScheduleProvider struct {
	provider.Provider
	createRequest provider.ScheduleTaskCreateRequest
	updateRequest provider.ScheduleTaskUpdateRequest
	deletedID     string
	enabledID     string
	disabledID    string
	createErr     error
	deleteErr     error
	enableErr     error
	disableErr    error
	listResult    *provider.ScheduleTaskPage
	runsResult    *provider.ScheduleRunRecordPage
}

func (s *stubScheduleProvider) ListSchedules(_ context.Context, params provider.ScheduleListParams) (*provider.ScheduleTaskPage, error) {
	if s.listResult != nil {
		return s.listResult, nil
	}
	return &provider.ScheduleTaskPage{}, nil
}

func (s *stubScheduleProvider) CreateSchedule(_ context.Context, request provider.ScheduleTaskCreateRequest) (*provider.ScheduleTask, error) {
	s.createRequest = request
	if s.createErr != nil {
		return nil, s.createErr
	}
	return &provider.ScheduleTask{ID: "sched-1", Name: request.Name, ScheduleType: request.ScheduleType}, nil
}

func (s *stubScheduleProvider) UpdateSchedule(_ context.Context, id string, request provider.ScheduleTaskUpdateRequest) (*provider.ScheduleTask, error) {
	s.updateRequest = request
	return &provider.ScheduleTask{ID: id, Name: "updated"}, nil
}

func (s *stubScheduleProvider) DeleteSchedule(_ context.Context, id string) error {
	s.deletedID = id
	return s.deleteErr
}

func (s *stubScheduleProvider) EnableSchedule(_ context.Context, id string) error {
	s.enabledID = id
	return s.enableErr
}

func (s *stubScheduleProvider) DisableSchedule(_ context.Context, id string) error {
	s.disabledID = id
	return s.disableErr
}

func (s *stubScheduleProvider) ListScheduleRuns(_ context.Context, scheduleID, status string, offset, limit int) (*provider.ScheduleRunRecordPage, error) {
	if s.runsResult != nil {
		return s.runsResult, nil
	}
	return &provider.ScheduleRunRecordPage{}, nil
}

func (s *stubScheduleProvider) SubmitClientCallResult(context.Context, provider.ClientCallResult) error { return nil }

// --- scheduleCreate 校验测试 ---

func TestScheduleCreateRequiresMandatoryFields(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}
	config := internalconfig.Default()

	_, err := r.scheduleCreate(context.Background(), stub, config, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing mandatory fields")
	}
}

func TestScheduleCreateRejectsInvalidScheduleType(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}
	config := internalconfig.Default()

	_, err := r.scheduleCreate(context.Background(), stub, config, map[string]any{
		"name":          "test",
		"prompt":        "do something",
		"request_mode":  "agent",
		"schedule_type": "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid schedule_type")
	}
}

func TestScheduleCreateCronRequiresCronExpr(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}
	config := internalconfig.Default()

	_, err := r.scheduleCreate(context.Background(), stub, config, map[string]any{
		"name":          "test",
		"prompt":        "do something",
		"request_mode":  "agent",
		"schedule_type": "cron",
	})
	if err == nil {
		t.Fatal("expected error for cron without cron_expr")
	}
}

func TestScheduleCreateIntervalRequiresPositiveSeconds(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}
	config := internalconfig.Default()

	_, err := r.scheduleCreate(context.Background(), stub, config, map[string]any{
		"name":          "test",
		"prompt":        "do something",
		"request_mode":  "agent",
		"schedule_type": "interval",
	})
	if err == nil {
		t.Fatal("expected error for interval without interval_seconds")
	}
}

func TestScheduleCreateOnceRequiresRunAtOrDelay(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}
	config := internalconfig.Default()

	_, err := r.scheduleCreate(context.Background(), stub, config, map[string]any{
		"name":          "test",
		"prompt":        "do something",
		"request_mode":  "agent",
		"schedule_type": "once",
	})
	if err == nil {
		t.Fatal("expected error for once without run_at or delay_seconds")
	}
}

func TestScheduleCreateOnceAcceptsDelaySeconds(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}
	config := internalconfig.Default()

	result, err := r.scheduleCreate(context.Background(), stub, config, map[string]any{
		"name":          "delayed task",
		"prompt":        "do something",
		"request_mode":  "agent",
		"schedule_type": "once",
		"delay_seconds": 300,
	})
	if err != nil {
		t.Fatalf("expected success with delay_seconds, got %v", err)
	}
	if result["id"] != "sched-1" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if stub.createRequest.DelaySeconds != 300 {
		t.Fatalf("expected delay_seconds=300 in request, got %#v", stub.createRequest)
	}
}

func TestScheduleCreateNormalizesScheduleType(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}
	config := internalconfig.Default()

	_, err := r.scheduleCreate(context.Background(), stub, config, map[string]any{
		"name":          "test",
		"prompt":        "do something",
		"request_mode":  "agent",
		"schedule_type": " CRON ",
		"cron_expr":     "0 9 * * *",
	})
	if err != nil {
		t.Fatalf("expected normalized schedule_type to pass, got %v", err)
	}
	if stub.createRequest.ScheduleType != "cron" {
		t.Fatalf("expected normalized schedule_type 'cron', got %q", stub.createRequest.ScheduleType)
	}
}

func TestScheduleCreateSuccess(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}
	config := internalconfig.Default()
	config.ClientID = "cli-test"
	config.DeviceID = "device-test"

	result, err := r.scheduleCreate(context.Background(), stub, config, map[string]any{
		"name":          "daily build",
		"prompt":        "run build",
		"request_mode":  "agent",
		"schedule_type": "cron",
		"cron_expr":     "0 9 * * *",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["name"] != "daily build" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if stub.createRequest.ClientID != "cli-test" {
		t.Fatalf("expected client_id in request, got %#v", stub.createRequest)
	}
}

// --- schedulePreview 测试 ---

func TestSchedulePreview(t *testing.T) {
	tests := []struct {
		toolName  string
		arguments map[string]any
		expected  string
	}{
		{"schedule_create", map[string]any{"name": "每日构建"}, "创建定时任务: 每日构建"},
		{"schedule_update", map[string]any{"schedule_id": "s-1"}, "更新定时任务: s-1"},
		{"schedule_delete", map[string]any{"schedule_id": "s-2"}, "删除定时任务: s-2"},
		{"schedule_enable", map[string]any{"schedule_id": "s-3"}, "启用定时任务: s-3"},
		{"schedule_disable", map[string]any{"schedule_id": "s-4"}, "禁用定时任务: s-4"},
		{"schedule_list", map[string]any{}, "schedule_list"},
	}
	for _, tt := range tests {
		got := schedulePreview(tt.toolName, tt.arguments)
		if got != tt.expected {
			t.Errorf("schedulePreview(%q) = %q, want %q", tt.toolName, got, tt.expected)
		}
	}
}

// --- mutatingScheduleTool 测试 ---

func TestMutatingScheduleTool(t *testing.T) {
	readOnly := []string{"schedule_list", "schedule_runs"}
	mutating := []string{"schedule_create", "schedule_update", "schedule_delete", "schedule_enable", "schedule_disable"}

	for _, name := range readOnly {
		if mutatingScheduleTool(name) {
			t.Errorf("expected %q to be read-only", name)
		}
	}
	for _, name := range mutating {
		if !mutatingScheduleTool(name) {
			t.Errorf("expected %q to be mutating", name)
		}
	}
}

// --- isScheduleTool 测试 ---

func TestIsScheduleTool(t *testing.T) {
	valid := []string{"schedule_list", "schedule_create", "schedule_update", "schedule_delete", "schedule_enable", "schedule_disable", "schedule_runs"}
	for _, name := range valid {
		if !isScheduleTool(name) {
			t.Errorf("expected %q to be a schedule tool", name)
		}
	}
	if isScheduleTool("shell_exec") {
		t.Error("expected shell_exec not to be a schedule tool")
	}
}

// --- scheduleDelete / scheduleEnable / scheduleDisable 测试 ---

func TestScheduleDeleteRequiresID(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}

	_, err := r.scheduleDelete(context.Background(), stub, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing schedule_id")
	}
}

func TestScheduleDeleteSuccess(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}

	result, err := r.scheduleDelete(context.Background(), stub, map[string]any{"schedule_id": "s-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["deleted"] != true {
		t.Fatalf("unexpected result: %#v", result)
	}
	if stub.deletedID != "s-1" {
		t.Fatalf("expected deleted ID s-1, got %q", stub.deletedID)
	}
}

func TestScheduleEnableRequiresID(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}

	_, err := r.scheduleEnable(context.Background(), stub, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing schedule_id")
	}
}

func TestScheduleDisableRequiresID(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}

	_, err := r.scheduleDisable(context.Background(), stub, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing schedule_id")
	}
}

func TestScheduleRunsRequiresID(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}

	_, err := r.scheduleRuns(context.Background(), stub, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing schedule_id")
	}
}

// --- scheduleUpdate 测试 ---

func TestScheduleUpdateRequiresID(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}

	_, err := r.scheduleUpdate(context.Background(), stub, map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing schedule_id")
	}
}

func TestScheduleUpdateSetsOnlyProvidedFields(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{}

	_, err := r.scheduleUpdate(context.Background(), stub, map[string]any{
		"schedule_id": "s-1",
		"name":        "new name",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.updateRequest.Name == nil || *stub.updateRequest.Name != "new name" {
		t.Fatalf("expected name to be set, got %#v", stub.updateRequest)
	}
	if stub.updateRequest.Prompt != nil {
		t.Fatalf("expected prompt to be nil, got %#v", stub.updateRequest.Prompt)
	}
}

// --- scheduleList 测试 ---

func TestScheduleListWithDisabledFilter(t *testing.T) {
	r := NewClientRuntime(nil, nil, nil)
	stub := &stubScheduleProvider{
		listResult: &provider.ScheduleTaskPage{
			Total: 1,
			Data:  []provider.ScheduleTask{{ID: "s-1", Name: "test", Disabled: true}},
		},
	}

	result, err := r.scheduleList(context.Background(), stub, map[string]any{"disabled": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["total"] != 1 {
		t.Fatalf("unexpected total: %#v", result["total"])
	}
}

// --- mutatingClientTool 对 schedule 工具的判定测试 ---

func TestMutatingClientToolSchedule(t *testing.T) {
	readOnly := []string{"schedule_list", "schedule_runs"}
	mutating := []string{"schedule_create", "schedule_update", "schedule_delete", "schedule_enable", "schedule_disable"}

	for _, name := range readOnly {
		if mutatingClientTool(name) {
			t.Errorf("expected %q to be read-only in mutatingClientTool", name)
		}
	}
	for _, name := range mutating {
		if !mutatingClientTool(name) {
			t.Errorf("expected %q to be mutating in mutatingClientTool", name)
		}
	}
}

// --- previewClientTool 对 schedule 工具的预览测试 ---

func TestPreviewClientToolSchedule(t *testing.T) {
	tests := []struct {
		toolName  string
		arguments map[string]any
		expected  string
	}{
		{"schedule_create", map[string]any{"name": "每日构建"}, "创建定时任务: 每日构建"},
		{"schedule_update", map[string]any{"schedule_id": "s-1"}, "更新定时任务: s-1"},
		{"schedule_delete", map[string]any{"schedule_id": "s-2"}, "删除定时任务: s-2"},
		{"schedule_enable", map[string]any{"schedule_id": "s-3"}, "启用定时任务: s-3"},
		{"schedule_disable", map[string]any{"schedule_id": "s-4"}, "禁用定时任务: s-4"},
	}
	for _, tt := range tests {
		got := previewClientTool(tt.toolName, tt.arguments)
		if got != tt.expected {
			t.Errorf("previewClientTool(%q) = %q, want %q", tt.toolName, got, tt.expected)
		}
	}
}
