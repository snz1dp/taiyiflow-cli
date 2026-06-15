package instructions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// writeTempFile creates a file under dir with the given name and content.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	fp := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", fp, err)
	}
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", fp, err)
	}
	return fp
}

// setEnvForTest sets an env var and returns a cleanup function.
func setEnvForTest(t *testing.T, key, value string) {
	t.Helper()
	old, hadOld := os.LookupEnv(key)
	os.Setenv(key, value)
	t.Cleanup(func() {
		if hadOld {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	})
}

// unsetEnvForTest unsets an env var and returns a cleanup function.
func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, hadOld := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if hadOld {
			os.Setenv(key, old)
		}
	})
}

// isolateEnv unsets both managed and user instruction env vars so that
// tests do not pick up real system files. Caller can then set them explicitly.
func isolateEnv(t *testing.T) {
	t.Helper()
	unsetEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR")
	unsetEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR")
}

// ---------------------------------------------------------------------------
// 1. mergeLayers — multi-priority discovery & stable sort
// ---------------------------------------------------------------------------

func TestMergeLayers_Empty(t *testing.T) {
	result := mergeLayers(nil)
	if result.Content != "" {
		t.Errorf("expected empty content, got %q", result.Content)
	}
	if len(result.Sources) != 0 {
		t.Errorf("expected no sources, got %v", result.Sources)
	}
	if len(result.Layers) != 0 {
		t.Errorf("expected no layers, got %d", len(result.Layers))
	}
}

func TestMergeLayers_SingleLayer(t *testing.T) {
	layers := []Layer{
		{Scope: ScopeProject, FilePath: "/ws/CLAUDE.md", Content: "# Project Rules", Priority: 20, Position: 0},
	}
	result := mergeLayers(layers)

	expectedHeader := "[Instructions from /ws/CLAUDE.md (project, priority 20)]"
	if !strings.HasPrefix(result.Content, expectedHeader) {
		t.Errorf("content should start with header %q, got:\n%s", expectedHeader, result.Content)
	}
	if !strings.Contains(result.Content, "# Project Rules") {
		t.Errorf("content should contain body text")
	}
	if len(result.Sources) != 1 || result.Sources[0] != "/ws/CLAUDE.md" {
		t.Errorf("unexpected sources: %v", result.Sources)
	}
}

func TestMergeLayers_MultiPrioritySort(t *testing.T) {
	// Intentionally provide layers in reverse priority order.
	layers := []Layer{
		{Scope: ScopeSubdir, FilePath: "/ws/sub/CLAUDE.md", Content: "subdir content", Priority: 30, Position: 4},
		{Scope: ScopeLocal, FilePath: "/ws/CLAUDE.local.md", Content: "local content", Priority: 25, Position: 3},
		{Scope: ScopeProject, FilePath: "/ws/CLAUDE.md", Content: "project content", Priority: 20, Position: 2},
		{Scope: ScopeUser, FilePath: "/home/.claude/CLAUDE.md", Content: "user content", Priority: 11, Position: 1},
		{Scope: ScopeManaged, FilePath: "/etc/taiyiflow/instructions/CLAUDE.md", Content: "managed content", Priority: 0, Position: 0},
	}

	result := mergeLayers(layers)

	// After sort: managed(0) < user(11) < project(20) < local(25) < subdir(30)
	expectedOrder := []string{
		"managed content",
		"user content",
		"project content",
		"local content",
		"subdir content",
	}

	prevIdx := -1
	for _, expected := range expectedOrder {
		idx := strings.Index(result.Content, expected)
		if idx < 0 {
			t.Fatalf("missing expected text %q in merged content:\n%s", expected, result.Content)
		}
		if idx <= prevIdx {
			t.Errorf("text %q appeared at index %d, expected after %d (sort order wrong)", expected, idx, prevIdx)
		}
		prevIdx = idx
	}
}

func TestMergeLayers_StableSort_SamePriority(t *testing.T) {
	// Two layers with identical priority but different positions.
	// Stable sort must preserve the original position order.
	layers := []Layer{
		{Scope: ScopeUser, FilePath: "/a/CLAUDE.md", Content: "first", Priority: 10, Position: 0},
		{Scope: ScopeUser, FilePath: "/b/CLAUDE.md", Content: "second", Priority: 10, Position: 1},
		{Scope: ScopeUser, FilePath: "/c/CLAUDE.md", Content: "third", Priority: 10, Position: 2},
	}

	result := mergeLayers(layers)

	idxFirst := strings.Index(result.Content, "first")
	idxSecond := strings.Index(result.Content, "second")
	idxThird := strings.Index(result.Content, "third")

	if idxFirst < 0 || idxSecond < 0 || idxThird < 0 {
		t.Fatalf("missing content in merged result:\n%s", result.Content)
	}
	if idxFirst >= idxSecond || idxSecond >= idxThird {
		t.Errorf("stable sort violated: first@%d second@%d third@%d", idxFirst, idxSecond, idxThird)
	}
}

// ---------------------------------------------------------------------------
// 2. mergeLayers — header format & double-newline separator
// ---------------------------------------------------------------------------

func TestMergeLayers_HeaderFormat(t *testing.T) {
	layers := []Layer{
		{Scope: ScopeManaged, FilePath: "/etc/taiyiflow/instructions/AGENT.md", Content: "body", Priority: 0, Position: 0},
	}
	result := mergeLayers(layers)

	expected := "[Instructions from /etc/taiyiflow/instructions/AGENT.md (managed, priority 0)]\nbody"
	if result.Content != expected {
		t.Errorf("header format mismatch.\nexpected: %q\ngot:      %q", expected, result.Content)
	}
}

func TestMergeLayers_DoubleNewlineSeparator(t *testing.T) {
	layers := []Layer{
		{Scope: ScopeManaged, FilePath: "/m.md", Content: "AAA", Priority: 0, Position: 0},
		{Scope: ScopeProject, FilePath: "/p.md", Content: "BBB", Priority: 20, Position: 1},
		{Scope: ScopeLocal, FilePath: "/l.md", Content: "CCC", Priority: 25, Position: 2},
	}
	result := mergeLayers(layers)

	// Sections must be separated by exactly \n\n
	if !strings.Contains(result.Content, "\n\n") {
		t.Fatal("expected double newline separator between sections")
	}

	parts := strings.Split(result.Content, "\n\n")
	if len(parts) != 3 {
		t.Errorf("expected 3 sections separated by \\n\\n, got %d parts:\n%s", len(parts), result.Content)
	}

	// Verify each part starts with the correct header
	for i, part := range parts {
		if !strings.HasPrefix(part, "[Instructions from ") {
			t.Errorf("part %d does not start with header: %q", i, part)
		}
	}
}

func TestMergeLayers_EmptyContentSkipped(t *testing.T) {
	layers := []Layer{
		{Scope: ScopeManaged, FilePath: "/m.md", Content: "real content", Priority: 0, Position: 0},
		{Scope: ScopeUser, FilePath: "/u.md", Content: "   \n  \t  ", Priority: 10, Position: 1}, // whitespace only
		{Scope: ScopeProject, FilePath: "/p.md", Content: "", Priority: 20, Position: 2},         // empty
		{Scope: ScopeLocal, FilePath: "/l.md", Content: "more content", Priority: 25, Position: 3},
	}
	result := mergeLayers(layers)

	if len(result.Sources) != 2 {
		t.Errorf("expected 2 sources (skipping empty), got %d: %v", len(result.Sources), result.Sources)
	}
	if !strings.Contains(result.Content, "real content") {
		t.Error("missing 'real content'")
	}
	if !strings.Contains(result.Content, "more content") {
		t.Error("missing 'more content'")
	}
	// Should be exactly one \n\n separator (2 parts)
	parts := strings.Split(result.Content, "\n\n")
	if len(parts) != 2 {
		t.Errorf("expected 2 sections, got %d", len(parts))
	}
}

// ---------------------------------------------------------------------------
// 3. ToPackage — serialization correctness
// ---------------------------------------------------------------------------

func TestToPackage_NilResult(t *testing.T) {
	var r *MergedResult
	if pkg := r.ToPackage(); pkg != nil {
		t.Errorf("expected nil package from nil result, got %+v", pkg)
	}
}

func TestToPackage_EmptyLayers(t *testing.T) {
	r := &MergedResult{}
	if pkg := r.ToPackage(); pkg != nil {
		t.Errorf("expected nil package from empty layers, got %+v", pkg)
	}
}

func TestToPackage_Basic(t *testing.T) {
	layers := []Layer{
		{Scope: ScopeManaged, FilePath: "/m.md", Content: "managed body", Priority: 0, Position: 0},
		{Scope: ScopeProject, FilePath: "/p.md", Content: "project body", Priority: 20, Position: 1},
	}
	r := mergeLayers(layers)
	pkg := r.ToPackage()

	if pkg == nil {
		t.Fatal("expected non-nil package")
	}
	if len(pkg.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(pkg.Layers))
	}

	// Verify layer fields
	if pkg.Layers[0].Scope != "managed" {
		t.Errorf("layer[0].Scope = %q, want %q", pkg.Layers[0].Scope, "managed")
	}
	if pkg.Layers[0].FilePath != "/m.md" {
		t.Errorf("layer[0].FilePath = %q, want %q", pkg.Layers[0].FilePath, "/m.md")
	}
	if pkg.Layers[0].Content != "managed body" {
		t.Errorf("layer[0].Content = %q, want %q", pkg.Layers[0].Content, "managed body")
	}
	if pkg.Layers[0].Priority != 0 {
		t.Errorf("layer[0].Priority = %d, want 0", pkg.Layers[0].Priority)
	}

	if pkg.Layers[1].Scope != "project" {
		t.Errorf("layer[1].Scope = %q, want %q", pkg.Layers[1].Scope, "project")
	}
	if pkg.Layers[1].Priority != 20 {
		t.Errorf("layer[1].Priority = %d, want 20", pkg.Layers[1].Priority)
	}

	// Merged should equal the MergedResult.Content
	if pkg.Merged != r.Content {
		t.Errorf("pkg.Merged does not match r.Content")
	}
}

func TestToPackage_JSONRoundTrip(t *testing.T) {
	layers := []Layer{
		{Scope: ScopeUser, FilePath: "/home/.claude/CLAUDE.md", Content: "user rules", Priority: 11, Position: 0},
	}
	r := mergeLayers(layers)
	pkg := r.ToPackage()

	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var decoded InstructionPackage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if len(decoded.Layers) != 1 {
		t.Fatalf("expected 1 decoded layer, got %d", len(decoded.Layers))
	}
	if decoded.Layers[0].Scope != "user" {
		t.Errorf("decoded scope = %q, want %q", decoded.Layers[0].Scope, "user")
	}
	if decoded.Layers[0].FilePath != "/home/.claude/CLAUDE.md" {
		t.Errorf("decoded file_path = %q", decoded.Layers[0].FilePath)
	}
	if decoded.Layers[0].Priority != 11 {
		t.Errorf("decoded priority = %d, want 11", decoded.Layers[0].Priority)
	}
	if decoded.Merged == "" {
		t.Error("decoded merged should not be empty")
	}

	// Verify JSON field names match the expected schema
	var raw map[string]json.RawMessage
	json.Unmarshal(data, &raw)
	if _, ok := raw["layers"]; !ok {
		t.Error("JSON missing 'layers' field")
	}
	if _, ok := raw["merged"]; !ok {
		t.Error("JSON missing 'merged' field")
	}

	// Check layer field names
	var rawLayer map[string]json.RawMessage
	json.Unmarshal(data, &raw)
	var layerArr []json.RawMessage
	json.Unmarshal(raw["layers"], &layerArr)
	json.Unmarshal(layerArr[0], &rawLayer)
	for _, key := range []string{"scope", "file_path", "content", "priority"} {
		if _, ok := rawLayer[key]; !ok {
			t.Errorf("layer JSON missing %q field", key)
		}
	}
}

func TestToPackage_EmptyContentLayerSkipped(t *testing.T) {
	r := &MergedResult{
		Content: "only real",
		Layers: []Layer{
			{Scope: ScopeProject, FilePath: "/p.md", Content: "only real", Priority: 20, Position: 0},
			{Scope: ScopeLocal, FilePath: "/l.md", Content: "  \n\t ", Priority: 25, Position: 1},
		},
	}
	pkg := r.ToPackage()
	if pkg == nil {
		t.Fatal("expected non-nil package")
	}
	if len(pkg.Layers) != 1 {
		t.Errorf("expected 1 layer (empty skipped), got %d", len(pkg.Layers))
	}
}

func TestToPackage_TrimsContent(t *testing.T) {
	r := &MergedResult{
		Content: "padded",
		Layers: []Layer{
			{Scope: ScopeProject, FilePath: "/p.md", Content: "  padded  \n", Priority: 20, Position: 0},
		},
	}
	pkg := r.ToPackage()
	if pkg == nil {
		t.Fatal("expected non-nil package")
	}
	if pkg.Layers[0].Content != "padded" {
		t.Errorf("expected trimmed content %q, got %q", "padded", pkg.Layers[0].Content)
	}
}

// ---------------------------------------------------------------------------
// 4. File size cap (256KB) & total size cap (1MB)
// ---------------------------------------------------------------------------

func TestReadFileIfExists_NonExistent(t *testing.T) {
	content, ok := readFileIfExists("/nonexistent/path/CLAUDE.md")
	if ok {
		t.Error("expected ok=false for non-existent file")
	}
	if content != "" {
		t.Errorf("expected empty content, got %q", content)
	}
}

func TestReadFileIfExists_Directory(t *testing.T) {
	dir := t.TempDir()
	content, ok := readFileIfExists(dir)
	if ok {
		t.Error("expected ok=false for directory path")
	}
	if content != "" {
		t.Errorf("expected empty content for directory, got %q", content)
	}
}

func TestReadFileIfExists_TooLarge(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "CLAUDE.md")

	// Create a file that exceeds maxFileSize (256KB)
	bigContent := strings.Repeat("x", maxFileSize+1)
	if err := os.WriteFile(fp, []byte(bigContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	content, ok := readFileIfExists(fp)
	if ok {
		t.Error("expected ok=false for oversized file")
	}
	if content != "" {
		t.Errorf("expected empty content for oversized file, got len=%d", len(content))
	}
}

func TestReadFileIfExists_ExactlyAtLimit(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "CLAUDE.md")

	// Create a file exactly at maxFileSize
	exactContent := strings.Repeat("y", maxFileSize)
	if err := os.WriteFile(fp, []byte(exactContent), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	content, ok := readFileIfExists(fp)
	if !ok {
		t.Error("expected ok=true for file exactly at size limit")
	}
	if len(content) != maxFileSize {
		t.Errorf("expected content length %d, got %d", maxFileSize, len(content))
	}
}

func TestReadFileIfExists_Normal(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "CLAUDE.md")
	expected := "# My Instructions\n\nDo good things."
	if err := os.WriteFile(fp, []byte(expected), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	content, ok := readFileIfExists(fp)
	if !ok {
		t.Error("expected ok=true for normal file")
	}
	if content != expected {
		t.Errorf("content mismatch: got %q, want %q", content, expected)
	}
}

func TestToPackage_TotalSizeCap(t *testing.T) {
	// Create layers whose combined trimmed content exceeds maxTotalSize (1MB).
	// Each layer has ~600KB of content; the second layer should be truncated.
	hugeContent1 := strings.Repeat("A", 600*1024)
	hugeContent2 := strings.Repeat("B", 600*1024)

	r := &MergedResult{
		Content: "merged",
		Layers: []Layer{
			{Scope: ScopeManaged, FilePath: "/m.md", Content: hugeContent1, Priority: 0, Position: 0},
			{Scope: ScopeProject, FilePath: "/p.md", Content: hugeContent2, Priority: 20, Position: 1},
		},
	}

	pkg := r.ToPackage()
	if pkg == nil {
		t.Fatal("expected non-nil package (first layer fits)")
	}
	if len(pkg.Layers) != 1 {
		t.Errorf("expected only 1 layer (second exceeds 1MB cap), got %d", len(pkg.Layers))
	}
	if pkg.Layers[0].Scope != "managed" {
		t.Errorf("expected first layer scope %q, got %q", "managed", pkg.Layers[0].Scope)
	}
}

func TestToPackage_TotalSizeCap_AllExceed(t *testing.T) {
	// Single layer that exceeds maxTotalSize on its own.
	hugeContent := strings.Repeat("Z", maxTotalSize+1)

	r := &MergedResult{
		Content: "merged",
		Layers: []Layer{
			{Scope: ScopeManaged, FilePath: "/m.md", Content: hugeContent, Priority: 0, Position: 0},
		},
	}

	pkg := r.ToPackage()
	if pkg != nil {
		t.Errorf("expected nil package when all layers exceed total cap, got %+v", pkg)
	}
}

func TestToPackage_TotalSizeCap_ExactFit(t *testing.T) {
	// Content exactly at maxTotalSize should be included.
	exactContent := strings.Repeat("E", maxTotalSize)

	r := &MergedResult{
		Content: "merged",
		Layers: []Layer{
			{Scope: ScopeProject, FilePath: "/p.md", Content: exactContent, Priority: 20, Position: 0},
		},
	}

	pkg := r.ToPackage()
	if pkg == nil {
		t.Fatal("expected non-nil package when content is exactly at total cap")
	}
	if len(pkg.Layers) != 1 {
		t.Errorf("expected 1 layer, got %d", len(pkg.Layers))
	}
}

// ---------------------------------------------------------------------------
// 5. Integration: Load with env overrides (file discovery end-to-end)
// ---------------------------------------------------------------------------

func TestLoad_NoFilesFound(t *testing.T) {
	isolateEnv(t)
	// Point managed and user dirs to empty temp dirs
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	emptyWorkspace := t.TempDir()
	result, err := Load(emptyWorkspace)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(result.Layers) != 0 {
		t.Errorf("expected 0 layers, got %d", len(result.Layers))
	}
	if result.Content != "" {
		t.Errorf("expected empty content, got %q", result.Content)
	}
}

func TestLoad_ProjectLayer(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	writeTempFile(t, ws, "CLAUDE.md", "# Project Instructions\nUse Go.")

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(result.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(result.Layers))
	}
	if result.Layers[0].Scope != ScopeProject {
		t.Errorf("expected scope %q, got %q", ScopeProject, result.Layers[0].Scope)
	}
	if result.Layers[0].Priority != 20 {
		t.Errorf("expected priority 20, got %d", result.Layers[0].Priority)
	}
	if !strings.Contains(result.Content, "# Project Instructions") {
		t.Error("merged content missing project text")
	}
}

func TestLoad_LocalLayer(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	writeTempFile(t, ws, "CLAUDE.local.md", "# Local Override\nIgnore tests.")

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(result.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(result.Layers))
	}
	if result.Layers[0].Scope != ScopeLocal {
		t.Errorf("expected scope %q, got %q", ScopeLocal, result.Layers[0].Scope)
	}
	if result.Layers[0].Priority != 25 {
		t.Errorf("expected priority 25, got %d", result.Layers[0].Priority)
	}
}

func TestLoad_AgentMdDiscovered(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	writeTempFile(t, ws, "AGENT.md", "# Agent Config\nBe concise.")

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(result.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(result.Layers))
	}
	if !strings.Contains(result.Layers[0].FilePath, "AGENT.md") {
		t.Errorf("expected AGENT.md in path, got %q", result.Layers[0].FilePath)
	}
}

func TestLoad_CopilotInstructionsDiscovered(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	writeTempFile(t, ws, ".github/copilot-instructions.md", "# Copilot Config\nUse Go idioms.")

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(result.Layers) != 1 {
		t.Fatalf("expected 1 layer, got %d", len(result.Layers))
	}
	if !strings.Contains(result.Layers[0].FilePath, ".github/copilot-instructions.md") {
		t.Errorf("expected .github/copilot-instructions.md in path, got %q", result.Layers[0].FilePath)
	}
	if result.Layers[0].Scope != ScopeProject {
		t.Errorf("expected scope %q, got %q", ScopeProject, result.Layers[0].Scope)
	}
	if !strings.Contains(result.Content, "Use Go idioms") {
		t.Error("merged content missing copilot instructions text")
	}
}

func TestLoad_MultiLayerPriorityOrder(t *testing.T) {
	isolateEnv(t)

	managedDir := t.TempDir()
	userDir := t.TempDir()
	ws := t.TempDir()

	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", managedDir)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", userDir)

	writeTempFile(t, managedDir, "CLAUDE.md", "MANAGED")
	writeTempFile(t, userDir, "CLAUDE.md", "USER")
	writeTempFile(t, ws, "CLAUDE.md", "PROJECT")
	writeTempFile(t, ws, "CLAUDE.local.md", "LOCAL")

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(result.Layers) != 4 {
		t.Fatalf("expected 4 layers, got %d", len(result.Layers))
	}

	// Verify priority order: managed(0) < user(10) < project(20) < local(25)
	expectedPriorities := []int{0, 10, 20, 25}
	for i, expected := range expectedPriorities {
		if result.Layers[i].Priority != expected {
			t.Errorf("layer[%d].Priority = %d, want %d", i, result.Layers[i].Priority, expected)
		}
	}

	// Verify text order in merged content
	idxManaged := strings.Index(result.Content, "MANAGED")
	idxUser := strings.Index(result.Content, "USER")
	idxProject := strings.Index(result.Content, "PROJECT")
	idxLocal := strings.Index(result.Content, "LOCAL")

	if idxManaged < 0 || idxUser < 0 || idxProject < 0 || idxLocal < 0 {
		t.Fatalf("missing content in merged result:\n%s", result.Content)
	}
	if !(idxManaged < idxUser && idxUser < idxProject && idxProject < idxLocal) {
		t.Errorf("content order wrong: managed@%d user@%d project@%d local@%d",
			idxManaged, idxUser, idxProject, idxLocal)
	}
}

func TestLoad_ManagedAndUserEnvOverrides(t *testing.T) {
	isolateEnv(t)

	managedDir := t.TempDir()
	userDir := t.TempDir()
	ws := t.TempDir()

	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", managedDir)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", userDir)

	writeTempFile(t, managedDir, "AGENT.md", "enterprise policy")
	writeTempFile(t, userDir, "CLAUDE.md", "personal prefs")

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(result.Layers) != 2 {
		t.Fatalf("expected 2 layers, got %d", len(result.Layers))
	}
	if result.Layers[0].Scope != ScopeManaged {
		t.Errorf("layer[0] scope = %q, want managed", result.Layers[0].Scope)
	}
	if result.Layers[1].Scope != ScopeUser {
		t.Errorf("layer[1] scope = %q, want user", result.Layers[1].Scope)
	}
}

func TestLoad_DirectoryAsInstructionFileSkipped(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	// Create a directory named CLAUDE.md — should be skipped
	claudeDir := filepath.Join(ws, "CLAUDE.md")
	if err := os.Mkdir(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(result.Layers) != 0 {
		t.Errorf("expected 0 layers (directory skipped), got %d", len(result.Layers))
	}
}

func TestLoad_OversizedFileSkipped(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	bigContent := strings.Repeat("X", maxFileSize+100)
	writeTempFile(t, ws, "CLAUDE.md", bigContent)

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(result.Layers) != 0 {
		t.Errorf("expected 0 layers (oversized file skipped), got %d", len(result.Layers))
	}
}

func TestLoad_EmptyWorkspaceDir(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	// Pass empty string as workspace dir — should not panic
	result, err := Load("")
	if err != nil {
		t.Fatalf("Load with empty workspace failed: %v", err)
	}
	if len(result.Layers) != 0 {
		t.Errorf("expected 0 layers, got %d", len(result.Layers))
	}
}

// ---------------------------------------------------------------------------
// 6. FormatSummary
// ---------------------------------------------------------------------------

func TestFormatSummary_NoLayers(t *testing.T) {
	r := &MergedResult{}
	summary := r.FormatSummary()
	if summary != "No instruction files found." {
		t.Errorf("unexpected summary: %q", summary)
	}
}

func TestFormatSummary_WithLayers(t *testing.T) {
	r := &MergedResult{
		Layers: []Layer{
			{Scope: ScopeManaged, FilePath: "/m.md", Priority: 0},
			{Scope: ScopeUser, FilePath: "/u.md", Priority: 10},
			{Scope: ScopeProject, FilePath: "/p.md", Priority: 20},
		},
	}
	summary := r.FormatSummary()

	if !strings.Contains(summary, "3 instruction layer(s)") {
		t.Errorf("summary should mention 3 layers: %q", summary)
	}
	if !strings.Contains(summary, "[managed]") {
		t.Errorf("summary should mention [managed]: %q", summary)
	}
	if !strings.Contains(summary, "[user]") {
		t.Errorf("summary should mention [user]: %q", summary)
	}
	if !strings.Contains(summary, "[project]") {
		t.Errorf("summary should mention [project]: %q", summary)
	}
}

// ---------------------------------------------------------------------------
// 7. KnownFileNames
// ---------------------------------------------------------------------------

func TestKnownFileNames_ContainsClaudeAndAgent(t *testing.T) {
	found := map[string]bool{}
	for _, name := range KnownFileNames {
		found[name] = true
	}
	if !found["CLAUDE.md"] {
		t.Error("KnownFileNames should contain CLAUDE.md")
	}
	if !found["AGENT.md"] {
		t.Error("KnownFileNames should contain AGENT.md")
	}
	if !found[".github/copilot-instructions.md"] {
		t.Error("KnownFileNames should contain .github/copilot-instructions.md")
	}
}

// ---------------------------------------------------------------------------
// 8. resolveManagedDir / resolveUserDir env behavior
// ---------------------------------------------------------------------------

func TestResolveManagedDir_EnvOverride(t *testing.T) {
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", "/custom/managed")
	got := resolveManagedDir()
	if got != "/custom/managed" {
		t.Errorf("resolveManagedDir() = %q, want %q", got, "/custom/managed")
	}
}

func TestResolveUserDir_EnvOverride(t *testing.T) {
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", "/custom/user")
	got := resolveUserDir()
	if got != "/custom/user" {
		t.Errorf("resolveUserDir() = %q, want %q", got, "/custom/user")
	}
}

func TestResolveUserDirs_EnvOverride(t *testing.T) {
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", "/custom/user")
	dirs := resolveUserDirs()
	if len(dirs) != 1 {
		t.Fatalf("expected 1 user dir with env override, got %d", len(dirs))
	}
	if dirs[0].dir != "/custom/user" {
		t.Errorf("dirs[0].dir = %q, want %q", dirs[0].dir, "/custom/user")
	}
	if dirs[0].priority != 10 {
		t.Errorf("dirs[0].priority = %d, want 10", dirs[0].priority)
	}
}

func TestResolveUserDirs_DefaultThreeDirs(t *testing.T) {
	unsetEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR")
	dirs := resolveUserDirs()
	if len(dirs) != 3 {
		t.Fatalf("expected 3 default user dirs, got %d", len(dirs))
	}

	home, _ := os.UserHomeDir()
	expected := []struct {
		suffix   string
		priority int
	}{
		{".snz1dp", 10},
		{".claude", 11},
		{".codex", 12},
	}
	for i, e := range expected {
		wantDir := filepath.Join(home, e.suffix)
		if dirs[i].dir != wantDir {
			t.Errorf("dirs[%d].dir = %q, want %q", i, dirs[i].dir, wantDir)
		}
		if dirs[i].priority != e.priority {
			t.Errorf("dirs[%d].priority = %d, want %d", i, dirs[i].priority, e.priority)
		}
	}
}

// ---------------------------------------------------------------------------
// 9. Both CLAUDE.md and AGENT.md in same directory
// ---------------------------------------------------------------------------

func TestLoad_BothClaudeAndAgentInProject(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	writeTempFile(t, ws, "CLAUDE.md", "claude rules")
	writeTempFile(t, ws, "AGENT.md", "agent rules")

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Both files should be discovered as project layers
	projectLayers := 0
	for _, l := range result.Layers {
		if l.Scope == ScopeProject {
			projectLayers++
		}
	}
	if projectLayers != 2 {
		t.Errorf("expected 2 project layers, got %d", projectLayers)
	}

	if !strings.Contains(result.Content, "claude rules") {
		t.Error("missing claude rules in merged content")
	}
	if !strings.Contains(result.Content, "agent rules") {
		t.Error("missing agent rules in merged content")
	}
}

// ---------------------------------------------------------------------------
// 10. End-to-end: Load → ToPackage → JSON
// ---------------------------------------------------------------------------

func TestEndToEnd_LoadToPackageJSON(t *testing.T) {
	isolateEnv(t)

	managedDir := t.TempDir()
	userDir := t.TempDir()
	ws := t.TempDir()

	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", managedDir)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", userDir)

	writeTempFile(t, managedDir, "CLAUDE.md", "# Enterprise\nAll code must pass review.")
	writeTempFile(t, userDir, "AGENT.md", "# Personal\nPrefer tabs.")
	writeTempFile(t, ws, "CLAUDE.md", "# Project\nUse Go 1.25.")
	writeTempFile(t, ws, "CLAUDE.local.md", "# Local\nDebug mode on.")

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	pkg := result.ToPackage()
	if pkg == nil {
		t.Fatal("expected non-nil package")
	}

	// Verify layer count
	if len(pkg.Layers) != 4 {
		t.Fatalf("expected 4 layers, got %d", len(pkg.Layers))
	}

	// Verify priority ordering in serialized layers
	for i := 1; i < len(pkg.Layers); i++ {
		if pkg.Layers[i].Priority < pkg.Layers[i-1].Priority {
			t.Errorf("layers not sorted by priority: [%d]=%d < [%d]=%d",
				i, pkg.Layers[i].Priority, i-1, pkg.Layers[i-1].Priority)
		}
	}

	// JSON round-trip
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var decoded InstructionPackage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if len(decoded.Layers) != 4 {
		t.Errorf("decoded layer count = %d, want 4", len(decoded.Layers))
	}
	if decoded.Merged == "" {
		t.Error("decoded merged should not be empty")
	}

	// Verify all content present
	for _, text := range []string{"All code must pass review", "Prefer tabs", "Use Go 1.25", "Debug mode on"} {
		if !strings.Contains(decoded.Merged, text) {
			t.Errorf("merged content missing %q", text)
		}
	}
}

// ---------------------------------------------------------------------------
// 11. Symlink behavior (T3)
// ---------------------------------------------------------------------------

func TestLoad_SymlinkedProjectFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks may require special privileges on Windows")
	}
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	// Create a real file in a separate directory, then symlink it into the workspace.
	realDir := t.TempDir()
	writeTempFile(t, realDir, "CLAUDE.md", "# Real Instructions\nFrom symlink target.")

	ws := t.TempDir()
	target := filepath.Join(realDir, "CLAUDE.md")
	link := filepath.Join(ws, "CLAUDE.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(result.Layers) != 1 {
		t.Fatalf("expected 1 layer from symlinked file, got %d", len(result.Layers))
	}
	if !strings.Contains(result.Content, "From symlink target") {
		t.Error("merged content missing symlinked file content")
	}
}

func TestLoad_SymlinkedWorkspaceDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks may require special privileges on Windows")
	}
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	// Create a real workspace with an instruction file, then symlink the entire dir.
	realWs := t.TempDir()
	writeTempFile(t, realWs, "CLAUDE.md", "# Workspace Instructions\nSymlinked workspace.")

	linkWs := filepath.Join(t.TempDir(), "ws-link")
	if err := os.Symlink(realWs, linkWs); err != nil {
		t.Fatalf("symlink workspace: %v", err)
	}

	result, err := Load(linkWs)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(result.Layers) != 1 {
		t.Fatalf("expected 1 layer from symlinked workspace, got %d", len(result.Layers))
	}
	if !strings.Contains(result.Content, "Symlinked workspace") {
		t.Error("merged content missing symlinked workspace content")
	}
}

func TestLoad_BrokenSymlinkSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks may require special privileges on Windows")
	}
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	// Create a broken symlink pointing to a non-existent file.
	if err := os.Symlink("/nonexistent/CLAUDE.md", filepath.Join(ws, "CLAUDE.md")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	result, err := Load(ws)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if len(result.Layers) != 0 {
		t.Errorf("expected 0 layers (broken symlink skipped), got %d", len(result.Layers))
	}
}

// ---------------------------------------------------------------------------
// 12. Subdirectory traversal with explicit cwd (T4)
// ---------------------------------------------------------------------------

func TestLoadWithCwd_SubdirLayers(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	// Create workspace structure:
	//   ws/CLAUDE.md
	//   ws/pkg/CLAUDE.md
	//   ws/pkg/internal/CLAUDE.md
	ws := t.TempDir()
	writeTempFile(t, ws, "CLAUDE.md", "PROJECT ROOT")
	writeTempFile(t, ws, filepath.Join("pkg", "CLAUDE.md"), "PKG LAYER")
	writeTempFile(t, ws, filepath.Join("pkg", "internal", "CLAUDE.md"), "INTERNAL LAYER")

	cwd := filepath.Join(ws, "pkg", "internal")
	result, err := LoadWithCwd(ws, cwd)
	if err != nil {
		t.Fatalf("LoadWithCwd failed: %v", err)
	}

	// Should have: project(20) + subdir:internal(30) + subdir:pkg(31)
	subdirCount := 0
	for _, l := range result.Layers {
		if l.Scope == ScopeSubdir {
			subdirCount++
		}
	}
	if subdirCount != 2 {
		t.Errorf("expected 2 subdir layers, got %d", subdirCount)
	}

	// Verify all content present
	for _, text := range []string{"PROJECT ROOT", "PKG LAYER", "INTERNAL LAYER"} {
		if !strings.Contains(result.Content, text) {
			t.Errorf("merged content missing %q", text)
		}
	}
}

func TestLoadWithCwd_CwdEqualsWorkspace(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	writeTempFile(t, ws, "CLAUDE.md", "PROJECT ONLY")

	// cwd == workspaceDir → no subdir layers
	result, err := LoadWithCwd(ws, ws)
	if err != nil {
		t.Fatalf("LoadWithCwd failed: %v", err)
	}

	for _, l := range result.Layers {
		if l.Scope == ScopeSubdir {
			t.Errorf("unexpected subdir layer when cwd == workspace: %+v", l)
		}
	}
	if len(result.Layers) != 1 {
		t.Errorf("expected 1 layer (project only), got %d", len(result.Layers))
	}
}

func TestLoadWithCwd_CwdOutsideWorkspace(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	writeTempFile(t, ws, "CLAUDE.md", "PROJECT")

	// cwd is completely outside workspace → no subdir layers
	outsideDir := t.TempDir()
	result, err := LoadWithCwd(ws, outsideDir)
	if err != nil {
		t.Fatalf("LoadWithCwd failed: %v", err)
	}

	for _, l := range result.Layers {
		if l.Scope == ScopeSubdir {
			t.Errorf("unexpected subdir layer when cwd is outside workspace: %+v", l)
		}
	}
}

func TestLoadWithCwd_EmptyCwdFallsBackToGetwd(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	writeTempFile(t, ws, "CLAUDE.md", "PROJECT")

	// Empty cwd should fall back to os.Getwd() and not crash.
	result, err := LoadWithCwd(ws, "")
	if err != nil {
		t.Fatalf("LoadWithCwd with empty cwd failed: %v", err)
	}
	// At minimum, the project layer should be found.
	found := false
	for _, l := range result.Layers {
		if l.Scope == ScopeProject {
			found = true
		}
	}
	if !found {
		t.Error("expected project layer even with empty cwd")
	}
}

func TestLoadWithCwd_SingleSubdirLevel(t *testing.T) {
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	ws := t.TempDir()
	writeTempFile(t, ws, "CLAUDE.md", "ROOT")
	writeTempFile(t, ws, filepath.Join("src", "CLAUDE.md"), "SRC LAYER")

	cwd := filepath.Join(ws, "src")
	result, err := LoadWithCwd(ws, cwd)
	if err != nil {
		t.Fatalf("LoadWithCwd failed: %v", err)
	}

	subdirCount := 0
	for _, l := range result.Layers {
		if l.Scope == ScopeSubdir {
			subdirCount++
			if l.Priority < 30 {
				t.Errorf("subdir layer priority should be >= 30, got %d", l.Priority)
			}
		}
	}
	if subdirCount != 1 {
		t.Errorf("expected 1 subdir layer, got %d", subdirCount)
	}
}

func TestLoadWithCwd_PrefixPathBoundary(t *testing.T) {
	// Regression test for B1: workspace-other should NOT be treated as a subdir of workspace.
	isolateEnv(t)
	emptyManaged := t.TempDir()
	emptyUser := t.TempDir()
	setEnvForTest(t, "TAIYIFLOW_MANAGED_INSTRUCTIONS_DIR", emptyManaged)
	setEnvForTest(t, "TAIYIFLOW_USER_INSTRUCTIONS_DIR", emptyUser)

	// Create two sibling directories with similar names.
	base := t.TempDir()
	ws := filepath.Join(base, "workspace")
	other := filepath.Join(base, "workspace-other")
	os.MkdirAll(ws, 0o755)
	os.MkdirAll(other, 0o755)

	writeTempFile(t, ws, "CLAUDE.md", "WORKSPACE ROOT")
	writeTempFile(t, other, "CLAUDE.md", "OTHER DIR")

	// cwd is in workspace-other, workspace is workspace → should NOT produce subdir layers
	result, err := LoadWithCwd(ws, other)
	if err != nil {
		t.Fatalf("LoadWithCwd failed: %v", err)
	}

	for _, l := range result.Layers {
		if l.Scope == ScopeSubdir {
			t.Errorf("workspace-other should not produce subdir layers, got: %+v", l)
		}
	}
}
