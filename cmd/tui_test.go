package cmd

import (
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestBuildTUISkillsPickerIncludesRenamedBundledSkill(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Default()

	picker, err := buildTUISkillsPicker(config)
	if err != nil {
		t.Fatalf("buildTUISkillsPicker failed: %v", err)
	}
	if picker == nil || len(picker.Items) == 0 {
		t.Fatalf("expected picker items, got %#v", picker)
	}
	found := false
	for _, item := range picker.Items {
		if item.Name == "taiyiflowApi" {
			found = true
			if item.SourceLabel != "内置技能" {
				t.Fatalf("unexpected source label: %#v", item)
			}
			if len(item.ReferenceFiles) != 1 {
				t.Fatalf("expected reference files for taiyiflowApi, got %#v", item)
			}
		}
	}
	if !found {
		t.Fatalf("expected taiyiflowApi in picker: %#v", picker.Items)
	}
}

func TestBuildTUIClientToolPanelShowsSuggestedReadRangeForFileEditFailure(t *testing.T) {
	panel := buildTUIClientToolPanel("file_edit", map[string]any{
		"path":                      "cmd/root.go",
		"status":                    "not_found",
		"sha256_before":             "abc123",
		"nearby_excerpt":            "10: func main() {\n11: \tprintln(\"hello\")",
		"prefetched_read": map[string]any{
			"start_line": 10,
			"end_line":   11,
			"content":    "func main() {\n\tprintln(\"hello\")",
		},
		"suggested_read_start_line": 10,
		"suggested_read_end_line":   11,
	}, assertError("file_edit 未找到 old_string"))
	if panel == nil {
		t.Fatal("expected panel")
	}
	if !strings.Contains(panel.Body, "建议先读: read_file cmd/root.go 行 10-11") {
		t.Fatalf("unexpected panel body: %#v", panel)
	}
	if !strings.Contains(panel.Body, "预取上下文(10-11 行):") || !strings.Contains(panel.Body, "println(\"hello\")") {
		t.Fatalf("expected prefetched context in panel body: %#v", panel)
	}
}

func assertError(message string) error {
	return simpleError(message)
}

type simpleError string

func (e simpleError) Error() string {
	return string(e)
}
