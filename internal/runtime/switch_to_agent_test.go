package runtime

import (
	"context"
	"io"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

// --- switchToAgent 方法测试 ---

func TestSwitchToAgentSetsPendingModeSwitch(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	r.SetCurrentRequestMode(provider.RequestTypeAsk)
	result, err := r.switchToAgent(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.pendingModeSwitch != provider.RequestTypeAgent {
		t.Fatalf("expected pendingModeSwitch=%q, got %q", provider.RequestTypeAgent, r.pendingModeSwitch)
	}
	switchedTo, _ := result["switched_to"].(string)
	if switchedTo != "agent" {
		t.Fatalf("expected switched_to=%q, got %q", "agent", switchedTo)
	}
	message, _ := result["message"].(string)
	if !strings.Contains(message, "agent") {
		t.Fatalf("expected message to contain 'agent', got %q", message)
	}
	// 验证 additional_tools 字段存在且非空
	additionalTools, ok := result["additional_tools"].([]map[string]any)
	if !ok {
		t.Fatalf("expected additional_tools to be []map[string]any, got %T", result["additional_tools"])
	}
	if len(additionalTools) == 0 {
		t.Fatal("expected non-empty additional_tools")
	}
	// 验证每个条目包含 name 和 description
	for i, tool := range additionalTools {
		name, _ := tool["name"].(string)
		if name == "" {
			t.Fatalf("additional_tools[%d] missing name", i)
		}
		desc, _ := tool["description"].(string)
		if desc == "" {
			t.Fatalf("additional_tools[%d] missing description", i)
		}
	}
	// 验证包含 shell_exec（agent 模式核心工具）
	foundShellExec := false
	for _, tool := range additionalTools {
		if name, _ := tool["name"].(string); name == "shell_exec" {
			foundShellExec = true
			break
		}
	}
	if !foundShellExec {
		t.Fatal("expected additional_tools to contain shell_exec")
	}
}

func TestSwitchToAgentIncludesReason(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	result, err := r.switchToAgent(map[string]any{"reason": "需要执行代码"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	message, _ := result["message"].(string)
	if !strings.Contains(message, "需要执行代码") {
		t.Fatalf("expected message to contain reason, got %q", message)
	}
}

func TestSwitchToAgentWithoutReasonHasDefaultMessage(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	result, err := r.switchToAgent(map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	message, _ := result["message"].(string)
	if !strings.Contains(message, "已切换到执行模式") {
		t.Fatalf("expected default message, got %q", message)
	}
}

// --- ConsumePendingModeSwitch 测试 ---

func TestConsumePendingModeSwitchReturnsValue(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	r.pendingModeSwitch = provider.RequestTypeAgent
	mode := r.ConsumePendingModeSwitch()
	if mode != provider.RequestTypeAgent {
		t.Fatalf("expected mode=%q, got %q", provider.RequestTypeAgent, mode)
	}
}

func TestConsumePendingModeSwitchClearsAfterRead(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	r.pendingModeSwitch = provider.RequestTypeAgent
	_ = r.ConsumePendingModeSwitch()
	if r.pendingModeSwitch != "" {
		t.Fatalf("expected pendingModeSwitch to be cleared, got %q", r.pendingModeSwitch)
	}
	mode := r.ConsumePendingModeSwitch()
	if mode != "" {
		t.Fatalf("expected empty mode after consume, got %q", mode)
	}
}

func TestConsumePendingModeSwitchNilSafe(t *testing.T) {
	var r *ClientRuntime
	mode := r.ConsumePendingModeSwitch()
	if mode != "" {
		t.Fatalf("expected empty mode for nil receiver, got %q", mode)
	}
}

func TestConsumePendingModeSwitchEmptyByDefault(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	mode := r.ConsumePendingModeSwitch()
	if mode != "" {
		t.Fatalf("expected empty mode by default, got %q", mode)
	}
}

// --- 通过 executeClientToolPayload 集成测试 ---

func TestExecuteClientToolPayloadSwitchToAgent(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	r.SetCurrentRequestMode(provider.RequestTypeAsk)
	config := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}
	payload := map[string]any{
		"server": map[string]any{"name": localToolServerName},
		"function": map[string]any{
			"name":      "switch_to_agent",
			"arguments": map[string]any{"reason": "测试"},
		},
	}
	_, result, err := r.executeClientToolPayload(context.Background(), nil, config, payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.pendingModeSwitch != provider.RequestTypeAgent {
		t.Fatalf("expected pendingModeSwitch=%q, got %q", provider.RequestTypeAgent, r.pendingModeSwitch)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any result, got %T", result)
	}
	switchedTo, _ := resultMap["switched_to"].(string)
	if switchedTo != "agent" {
		t.Fatalf("expected switched_to=%q, got %q", "agent", switchedTo)
	}
	// 验证 additional_tools 字段存在
	additionalTools, ok := resultMap["additional_tools"].([]map[string]any)
	if !ok {
		t.Fatalf("expected additional_tools to be []map[string]any, got %T", resultMap["additional_tools"])
	}
	if len(additionalTools) == 0 {
		t.Fatal("expected non-empty additional_tools")
	}
}

// --- 工具定义可见性测试 ---

func TestSwitchToAgentDefinitionAvailableInAskMode(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := r.clientToolDefinitionsForMode(provider.RequestTypeAsk)
	for _, item := range definitions {
		name := strings.TrimSpace(stringValue(item["name"]))
		if name == "switch_to_agent" {
			// 验证描述包含关键信息
			description, _ := item["description"].(string)
			if !strings.Contains(description, "执行模式") {
				t.Fatalf("expected switch_to_agent description to mention 执行模式, got %q", description)
			}
			// 验证描述提及返回值包含新增工具能力描述
			if !strings.Contains(description, "additional_tools") {
				t.Fatalf("expected switch_to_agent description to mention additional_tools, got %q", description)
			}
			return
		}
	}
	t.Fatal("expected switch_to_agent tool definition in ask mode")
}

func TestSwitchToAgentDefinitionAvailableInPlanMode(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := r.clientToolDefinitionsForMode(provider.RequestTypePlan)
	for _, item := range definitions {
		name := strings.TrimSpace(stringValue(item["name"]))
		if name == "switch_to_agent" {
			return
		}
	}
	t.Fatal("expected switch_to_agent tool definition in plan mode")
}

func TestSwitchToAgentDefinitionHiddenInAgentMode(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := r.clientToolDefinitionsForMode(provider.RequestTypeAgent)
	for _, item := range definitions {
		name := strings.TrimSpace(stringValue(item["name"]))
		if name == "switch_to_agent" {
			t.Fatalf("expected switch_to_agent to be hidden in agent mode, but found it in definitions")
		}
	}
}

// --- allowedClientToolSetForMode 测试 ---

func TestAllowedClientToolSetForModeIncludesSwitchToAgentInAskMode(t *testing.T) {
	allowed := allowedClientToolSetForMode(provider.RequestTypeAsk)
	if !allowed["switch_to_agent"] {
		t.Fatal("expected switch_to_agent to be allowed in ask mode")
	}
}

func TestAllowedClientToolSetForModeIncludesSwitchToAgentInPlanMode(t *testing.T) {
	allowed := allowedClientToolSetForMode(provider.RequestTypePlan)
	if !allowed["switch_to_agent"] {
		t.Fatal("expected switch_to_agent to be allowed in plan mode")
	}
}

// --- canonicalClientToolName 测试 ---

func TestCanonicalClientToolNameSwitchToAgent(t *testing.T) {
	name := canonicalClientToolName("switch_to_agent")
	if name != "switch_to_agent" {
		t.Fatalf("expected canonical name 'switch_to_agent', got %q", name)
	}
}

// --- previewClientTool 测试 ---

func TestPreviewClientToolSwitchToAgentWithReason(t *testing.T) {
	preview := previewClientTool("switch_to_agent", map[string]any{"reason": "需要执行代码"})
	if !strings.Contains(preview, "switch_to_agent") {
		t.Fatalf("expected preview to contain 'switch_to_agent', got %q", preview)
	}
	if !strings.Contains(preview, "需要执行代码") {
		t.Fatalf("expected preview to contain reason, got %q", preview)
	}
}

func TestPreviewClientToolSwitchToAgentWithoutReason(t *testing.T) {
	preview := previewClientTool("switch_to_agent", map[string]any{})
	if preview != "switch_to_agent" {
		t.Fatalf("expected preview 'switch_to_agent', got %q", preview)
	}
}

// --- additionalAgentToolDescriptions 测试 ---

func TestAdditionalAgentToolDescriptionsReturnsDiff(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	r.SetCurrentRequestMode(provider.RequestTypeAsk)
	additional := r.additionalAgentToolDescriptions()
	if len(additional) == 0 {
		t.Fatal("expected non-empty additional tool descriptions")
	}
	// 验证 shell_exec 在新增列表中
	found := false
	for _, tool := range additional {
		if name, _ := tool["name"].(string); name == "shell_exec" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected shell_exec in additional tools")
	}
}

func TestAdditionalAgentToolDescriptionsExcludesAskModeTools(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	r.SetCurrentRequestMode(provider.RequestTypeAsk)
	additional := r.additionalAgentToolDescriptions()
	// ask 模式已有的工具不应出现在新增列表中
	askOnlyTools := []string{"ask_user", "lsp_query", "glob_files", "search_files", "read_file", "skill"}
	for _, tool := range additional {
		name, _ := tool["name"].(string)
		for _, askOnly := range askOnlyTools {
			if name == askOnly {
				t.Fatalf("ask mode tool %q should not appear in additional tools", name)
			}
		}
	}
}

func TestAdditionalAgentToolDescriptionsExcludesSwitchToAgent(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	r.SetCurrentRequestMode(provider.RequestTypeAsk)
	additional := r.additionalAgentToolDescriptions()
	for _, tool := range additional {
		if name, _ := tool["name"].(string); name == "switch_to_agent" {
			t.Fatal("switch_to_agent should not appear in additional tools")
		}
	}
}

func TestAdditionalAgentToolDescriptionsUsesPlanAsBase(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	r.SetCurrentRequestMode(provider.RequestTypePlan)
	additional := r.additionalAgentToolDescriptions()
	if len(additional) == 0 {
		t.Fatal("expected non-empty additional tool descriptions for plan mode")
	}
	// plan 模式已有的工具不应出现在新增列表中
	planDefs := r.clientToolDefinitionsForMode(provider.RequestTypePlan)
	planNames := make(map[string]bool, len(planDefs))
	for _, def := range planDefs {
		name := strings.TrimSpace(stringValue(def["name"]))
		if name != "" {
			planNames[name] = true
		}
	}
	for _, tool := range additional {
		name, _ := tool["name"].(string)
		if planNames[name] {
			t.Fatalf("plan mode tool %q should not appear in additional tools", name)
		}
	}
	// shell_exec 仍应在新增列表中
	found := false
	for _, tool := range additional {
		if name, _ := tool["name"].(string); name == "shell_exec" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected shell_exec in additional tools for plan mode")
	}
}

func TestAdditionalAgentToolDescriptionsFallsBackToAskWhenUnset(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	// 不设置 currentRequestMode，应回退到 ask
	additional := r.additionalAgentToolDescriptions()
	if len(additional) == 0 {
		t.Fatal("expected non-empty additional tool descriptions when currentRequestMode is unset")
	}
}

func TestAdditionalAgentToolDescriptionsFallsBackToAskWhenAgent(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	// 如果 currentRequestMode 是 agent（不应发生但防御性处理），应回退到 ask
	r.SetCurrentRequestMode(provider.RequestTypeAgent)
	additional := r.additionalAgentToolDescriptions()
	if len(additional) == 0 {
		t.Fatal("expected non-empty additional tool descriptions when currentRequestMode is agent")
	}
}

// --- cloneRuntime 测试 ---

func TestCloneRuntimeDoesNotInheritPendingModeSwitch(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	r.pendingModeSwitch = provider.RequestTypeAgent
	r.SetCurrentRequestMode(provider.RequestTypePlan)
	clone := r.cloneRuntime()
	if clone.pendingModeSwitch != "" {
		t.Fatalf("expected clone pendingModeSwitch to be empty, got %q", clone.pendingModeSwitch)
	}
	if clone.currentRequestMode != provider.RequestTypePlan {
		t.Fatalf("expected clone currentRequestMode to be %q, got %q", provider.RequestTypePlan, clone.currentRequestMode)
	}
}

// --- 完整流程测试：switch_to_agent → ConsumePendingModeSwitch ---

func TestSwitchToAgentFullFlow(t *testing.T) {
	r := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	r.SetCurrentRequestMode(provider.RequestTypeAsk)

	// 初始状态：无待处理切换
	if mode := r.ConsumePendingModeSwitch(); mode != "" {
		t.Fatalf("expected empty mode initially, got %q", mode)
	}

	// 执行 switch_to_agent
	result, err := r.switchToAgent(map[string]any{"reason": "需要执行代码"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// 消费切换请求
	mode := r.ConsumePendingModeSwitch()
	if mode != provider.RequestTypeAgent {
		t.Fatalf("expected mode=%q, got %q", provider.RequestTypeAgent, mode)
	}

	// 二次消费应返回空
	mode = r.ConsumePendingModeSwitch()
	if mode != "" {
		t.Fatalf("expected empty mode after second consume, got %q", mode)
	}
}
