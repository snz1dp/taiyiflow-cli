package cmd

import (
	"strings"
	"testing"
)

func TestProbeRuntimeTools(t *testing.T) {
	// probeRuntimeTools 是实际探测函数，在测试环境中至少能找到一些工具
	results := probeRuntimeTools()

	// 在开发环境中，通常 node、python3、rg 至少有一个可用
	// 这里只验证探测不会 panic 且返回格式正确
	for _, r := range results {
		if r.label == "" {
			t.Error("探测结果的 label 不应为空")
		}
		if r.command == "" {
			t.Error("探测结果的 command 不应为空")
		}
	}
}

func TestDetectLocalRuntimeToolsSummary(t *testing.T) {
	// 由于 detectLocalRuntimeToolsSummary 依赖全局缓存（sync.Once），
	// 在测试中可能已经被前一个测试填充。这里只验证输出格式。
	summary := detectLocalRuntimeToolsSummary()
	if summary == "" {
		t.Error("工具摘要不应为空（至少应返回 '未检测到'）")
	}
}

func TestDetectLocalRuntimeToolsInline(t *testing.T) {
	inline := detectLocalRuntimeToolsInline()
	// 如果没有工具，返回空字符串是合理的
	// 如果有工具，应该不包含括号格式（inline 格式与 summary 不同）
	if inline != "" {
		// 验证非空结果中至少包含一个已知工具名
		knownTools := []string{"node22", "python3.12", "rg", "uv", "git", "node", "python3", "python"}
		found := false
		for _, tool := range knownTools {
			if strings.Contains(inline, tool) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("inline 结果 '%s' 不包含任何已知工具名", inline)
		}
	}
}

func TestProbeOneTool(t *testing.T) {
	// 测试探测不存在的工具
	cliDir := resolveCLIExecutableDir()
	result := probeOneTool(runtimeToolProbe{
		label:      "nonexistent_tool_xyz",
		candidates: []string{"nonexistent_tool_xyz_12345"},
		versionArg: "--version",
	}, cliDir)
	if result != nil {
		t.Error("不存在的工具应返回 nil")
	}
}

func TestRuntimeToolResultFormat(t *testing.T) {
	// 验证结果格式
	r := runtimeToolResult{label: "test", command: "/usr/bin/test", version: "1.0.0"}
	if r.label != "test" {
		t.Errorf("expected label 'test', got '%s'", r.label)
	}
	if r.command != "/usr/bin/test" {
		t.Errorf("expected command '/usr/bin/test', got '%s'", r.command)
	}
	if r.version != "1.0.0" {
		t.Errorf("expected version '1.0.0', got '%s'", r.version)
	}
}

func TestToolDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		input    runtimeToolResult
		expected string
	}{
		{
			name:     "absolute_path_returns_basename",
			input:    runtimeToolResult{label: "node22", command: "/usr/local/bin/node", version: "v20.19.4"},
			expected: "node",
		},
		{
			name:     "relative_path_returns_basename",
			input:    runtimeToolResult{label: "rg", command: "bin/rg", version: "15.1.0"},
			expected: "rg",
		},
		{
			name:     "empty_command_falls_back_to_label",
			input:    runtimeToolResult{label: "node22", command: "", version: ""},
			expected: "node22",
		},
		{
			name:     "bare_command_name_returns_as_is",
			input:    runtimeToolResult{label: "python3.12", command: "python3", version: "3.11.13"},
			expected: "python3",
		},
		{
			name:     "trailing_slash_stripped",
			input:    runtimeToolResult{label: "rg", command: "/usr/bin/rg/", version: "15.1.0"},
			expected: "rg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toolDisplayName(tt.input)
			if got != tt.expected {
				t.Errorf("toolDisplayName(%+v) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestResetRuntimeToolsCache(t *testing.T) {
	// 先触发一次探测填充缓存
	_ = detectLocalRuntimeTools()
	// 重置缓存
	resetRuntimeToolsCache()
	// 验证重置后缓存为空
	if runtimeToolsCache != nil {
		t.Error("expected runtimeToolsCache to be nil after reset")
	}
	// 再次探测，验证缓存重新填充
	_ = detectLocalRuntimeTools()
	if runtimeToolsCache == nil {
		t.Error("expected runtimeToolsCache to be populated after re-detect")
	}
}


