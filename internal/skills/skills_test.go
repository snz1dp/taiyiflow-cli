package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestDiscoverSkillsAndEnableState(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "reviewer"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "reviewer", "SKILL.md"), []byte("---\nname: Code Review\ndescription: 审查变更\ncategory: engineering\nprerequisites:\n  - repo access\n---\n\n检查回归风险。\n"), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tempDir, "writer"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "writer", "SKILL.md"), []byte("---\nname: Draft Writer\ndescription: 生成初稿\ncategory: docs\n---\n\n输出规划草稿。\n"), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	config := internalconfig.Config{}
	config.Skills.Directory = tempDir

	items, directory, err := Discover(config)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if !strings.Contains(directory, tempDir) {
		t.Fatalf("expected discovered directories to include custom dir %q, got %q", tempDir, directory)
	}
	if len(items) < 2 {
		t.Fatalf("expected at least 2 skills, got %d", len(items))
	}
	if !items[0].Enabled {
		t.Fatalf("expected enabled skill to be sorted first")
	}
	foundCodeReview := false
	for _, item := range items {
		if item.Name == "Code Review" {
			foundCodeReview = true
			if !item.Enabled {
				t.Fatal("expected Code Review enabled")
			}
		}
	}
	if !foundCodeReview {
		t.Fatal("expected custom Code Review skill in discovery result")
	}
	foundDraftWriter := false
	for _, item := range items {
		if item.Name == "Draft Writer" {
			foundDraftWriter = true
			if !item.Enabled {
				t.Fatal("expected Draft Writer enabled by default")
			}
		}
	}
	if !foundDraftWriter {
		t.Fatal("expected Draft Writer skill in discovery result")
	}

	config.Skills.Disabled = []string{"draft writer"}
	item, _, err := LoadByName(config, "draft writer")
	if err != nil {
		t.Fatalf("load by name failed: %v", err)
	}
	if item.Name != "Draft Writer" {
		t.Fatalf("unexpected loaded skill: %s", item.Name)
	}
	if item.Enabled {
		t.Fatalf("expected Draft Writer disabled after explicit deny")
	}
}

func TestLoadByNameMatchesRelativeDirectoryAlias(t *testing.T) {
	tempDir := t.TempDir()
	skillDir := filepath.Join(tempDir, "qq-email-summary")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	content := "---\nname: email-summary\ndescription: 邮件摘要\n---\n\n运行邮件摘要。\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	config := internalconfig.Config{}
	config.Skills.Directory = tempDir

	item, _, err := LoadByName(config, "qq-email-summary")
	if err != nil {
		t.Fatalf("load by relative dir failed: %v", err)
	}
	if item.Name != "email-summary" {
		t.Fatalf("unexpected loaded skill: %s", item.Name)
	}
	if item.RelativeDir != "qq-email-summary" {
		t.Fatalf("unexpected relative dir: %s", item.RelativeDir)
	}
}

func TestClientPackagesIncludeMarkdownAndContent(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "reviewer"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	content := "---\nname: Code Review\ndescription: 审查变更\ncategory: engineering\nprerequisites:\n  - repo access\n---\n\n检查回归风险。\n"
	if err := os.WriteFile(filepath.Join(tempDir, "reviewer", "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	config := internalconfig.Config{}
	config.Skills.Directory = tempDir

	items, directory, err := Discover(config)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}

	payload, ok := ClientPackages(items, directory).([]map[string]any)
	if !ok {
		t.Fatalf("unexpected payload type: %T", ClientPackages(items, directory))
	}
	if len(payload) < 1 {
		t.Fatalf("expected at least 1 package, got %d", len(payload))
	}
	var skillPayload map[string]any
	for _, pkg := range payload {
		skillsPayload, ok := pkg["skills"].([]map[string]any)
		if !ok {
			continue
		}
		for _, candidate := range skillsPayload {
			if candidate["name"] == "Code Review" {
				skillPayload = candidate
				break
			}
		}
		if skillPayload != nil {
			break
		}
	}
	if skillPayload == nil {
		t.Fatalf("expected Code Review payload in %#v", payload)
	}
	if got := skillPayload["content"]; got != "检查回归风险。" {
		t.Fatalf("unexpected content: %#v", got)
	}
	markdown, ok := skillPayload["markdown"].(string)
	if !ok {
		t.Fatalf("unexpected markdown payload: %#v", skillPayload["markdown"])
	}
	if !strings.Contains(markdown, "name: Code Review") {
		t.Fatalf("markdown missing manifest: %s", markdown)
	}
	if !strings.Contains(markdown, "检查回归风险。") {
		t.Fatalf("markdown missing body: %s", markdown)
	}
}

func TestDiscoverIncludesBundledSkillsByDefault(t *testing.T) {
	config := internalconfig.Config{}

	items, _, err := Discover(config)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("expected bundled skills")
	}

	item, _, err := LoadByName(config, "update-config")
	if err != nil {
		t.Fatalf("load bundled skill failed: %v", err)
	}
	if item.Source != "bundled" {
		t.Fatalf("expected bundled source, got %s", item.Source)
	}
	if !item.Enabled {
		t.Fatal("expected bundled skill enabled by default")
	}
	if item.RootDir != "bundled" {
		t.Fatalf("expected bundled root dir, got %s", item.RootDir)
	}
	if strings.TrimSpace(item.Body) == "" {
		t.Fatal("expected bundled body content")
	}
	if !bundledSkillDefaultEnabled(item.Name) {
		t.Fatalf("expected default enabled bundled skill: %s", item.Name)
	}
}

func TestWorkspaceSkillOverridesBundledDefaultAndCanBeDisabled(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "update-config"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	content := "---\nname: update-config\ndescription: 工作区覆盖版\ncategory: workspace\n---\n\n优先使用工作区 skill。\n"
	if err := os.WriteFile(filepath.Join(tempDir, "update-config", "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	config := internalconfig.Config{}
	config.Skills.Directory = tempDir

	item, _, err := LoadByName(config, "update-config")
	if err != nil {
		t.Fatalf("load skill failed: %v", err)
	}
	if item.Source != "custom" {
		t.Fatalf("expected custom source, got %s", item.Source)
	}
	if !item.Enabled {
		t.Fatal("expected overriding skill enabled because bundled default exists")
	}

	config.Skills.Disabled = []string{"update-config"}
	item, _, err = LoadByName(config, "update-config")
	if err != nil {
		t.Fatalf("load disabled skill failed: %v", err)
	}
	if item.Enabled {
		t.Fatal("expected disabled skill to be off")
	}
}

func TestBundledSkillExtractsReferenceFiles(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Config{}

	item, _, err := LoadByName(config, "taiyiflow-api")
	if err != nil {
		t.Fatalf("load bundled skill failed: %v", err)
	}
	if item.RootDir == "bundled" || strings.TrimSpace(item.RootDir) == "" {
		t.Fatalf("expected extracted root dir, got %q", item.RootDir)
	}
	if len(item.Files) != 1 {
		t.Fatalf("expected extracted files metadata, got %#v", item.Files)
	}
	referencePath := filepath.Join(item.RootDir, filepath.FromSlash(item.Files[0].Path))
	content, err := os.ReadFile(referencePath)
	if err != nil {
		t.Fatalf("read extracted reference failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(content)), "bundled skill") {
		t.Fatalf("unexpected extracted content: %s", string(content))
	}
}

func TestLoadBundledTaiyiFlowComputerSkill(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Config{}

	item, _, err := LoadByName(config, "taiyiflow-computer")
	if err != nil {
		t.Fatalf("load bundled skill failed: %v", err)
	}
	if item.Source != "bundled" {
		t.Fatalf("expected bundled source, got %s", item.Source)
	}
	if !item.Enabled {
		t.Fatal("expected bundled desktop computer skill enabled by default")
	}
	if !strings.Contains(item.Body, "taiyiflow-computer__") {
		t.Fatalf("expected desktop computer skill body to mention tool namespace, got %q", item.Body)
	}
}

func TestLoadFileFallsBackToMeaningfulSummary(t *testing.T) {
	tempDir := t.TempDir()
	skillDir := filepath.Join(tempDir, "summarized")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	content := "---\nname: Summarized Skill\n---\n\n# Summarized Skill\n\n>\n> 用于验证默认摘要提取不会退化成单独的 markdown 符号。\n\n后续正文。\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	item, err := LoadFile(tempDir, filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("load file failed: %v", err)
	}
	if item.Description != "用于验证默认摘要提取不会退化成单独的 markdown 符号。" {
		t.Fatalf("unexpected description: %q", item.Description)
	}
	if item.Summary != item.Description {
		t.Fatalf("expected summary fallback to meaningful paragraph, got %#v", item)
	}
}

func TestLoadFileSupportsMetadataWithoutOpeningFrontmatterMarker(t *testing.T) {
	tempDir := t.TempDir()
	skillDir := filepath.Join(tempDir, "wan-video")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	content := "name: alibaba-ai-video-wan-video\n" +
		"description: Alibaba Cloud Wanx Video Generation - Text to Video, Image to Video, Video Editing\n" +
		"homepage: https://help.aliyun.com/zh/model-studio/video-generation\n" +
		"metadata:\n" +
		"  {\n" +
		"    \"openclaw\":\n" +
		"      {\n" +
		"        \"emoji\": \"🎬\",\n" +
		"        \"requires\": { \"env\": [\"DASHSCOPE_API_KEY\"] },\n" +
		"        \"install\": [],\n" +
		"      },\n" +
		"  }\n" +
		"---\n\n" +
		"# 阿里云万相视频生成 (Wanx Video)\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill failed: %v", err)
	}

	item, err := LoadFile(tempDir, filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("load file failed: %v", err)
	}
	if item.Name != "alibaba-ai-video-wan-video" {
		t.Fatalf("unexpected name: %q", item.Name)
	}
	if item.Homepage != "https://help.aliyun.com/zh/model-studio/video-generation" {
		t.Fatalf("unexpected homepage: %q", item.Homepage)
	}
	metadata, ok := item.Metadata["openclaw"].(map[string]any)
	if !ok {
		t.Fatalf("expected openclaw metadata map, got %#v", item.Metadata)
	}
	if metadata["emoji"] != "🎬" {
		t.Fatalf("unexpected metadata emoji: %#v", metadata)
	}
	requires, ok := metadata["requires"].(map[string]any)
	if !ok {
		t.Fatalf("expected requires metadata map, got %#v", metadata)
	}
	envList, ok := requires["env"].([]any)
	if !ok || len(envList) != 1 || envList[0] != "DASHSCOPE_API_KEY" {
		t.Fatalf("unexpected env requirements: %#v", requires)
	}
	if item.Body != "# 阿里云万相视频生成 (Wanx Video)" {
		t.Fatalf("unexpected body: %q", item.Body)
	}
	markdown := Markdown(item)
	if !strings.HasPrefix(markdown, "---\n") {
		t.Fatalf("expected markdown to render canonical frontmatter, got %q", markdown)
	}
	if !strings.Contains(markdown, "homepage: https://help.aliyun.com/zh/model-studio/video-generation") {
		t.Fatalf("expected markdown to include homepage, got %q", markdown)
	}
	if !strings.Contains(markdown, "DASHSCOPE_API_KEY") {
		t.Fatalf("expected markdown to include metadata, got %q", markdown)
	}
	item.Enabled = true
	packages, ok := ClientPackages([]Skill{item}, tempDir).([]map[string]any)
	if !ok || len(packages) != 1 {
		t.Fatalf("unexpected client packages payload: %#v", packages)
	}
	skillsPayload, ok := packages[0]["skills"].([]map[string]any)
	if !ok || len(skillsPayload) != 1 {
		t.Fatalf("unexpected nested skills payload: %#v", packages[0])
	}
	metadataPayload, ok := skillsPayload[0]["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata in payload, got %#v", skillsPayload[0])
	}
	if _, ok := metadataPayload["openclaw"]; !ok {
		t.Fatalf("expected openclaw metadata in payload, got %#v", metadataPayload)
	}
	if got := skillsPayload[0]["homepage"]; got != item.Homepage {
		t.Fatalf("unexpected homepage payload: %s", fmt.Sprintf("%#v", got))
	}
}

func TestResolveInstallDirectoryDefaultsToGlobalSkillsDir(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	config := internalconfig.Default()
	config.Workdir = t.TempDir()

	resolved, err := ResolveInstallDirectory(config)
	if err != nil {
		t.Fatalf("resolve install directory failed: %v", err)
	}
	defaultDir, err := internalconfig.DefaultSkillsDir()
	if err != nil {
		t.Fatalf("default skills dir failed: %v", err)
	}
	if resolved != defaultDir {
		t.Fatalf("expected global default skills dir %q, got %q", defaultDir, resolved)
	}
}

func TestResolveInstallDirectoryUsesWorkspaceScope(t *testing.T) {
	workspaceDir := t.TempDir()
	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.InstallScope = internalconfig.SkillInstallScopeWorkspace

	resolved, err := ResolveInstallDirectory(config)
	if err != nil {
		t.Fatalf("resolve install directory failed: %v", err)
	}
	expected := filepath.Join(workspaceDir, ".snz1dp", "skills")
	if resolved != expected {
		t.Fatalf("expected workspace skills dir %q, got %q", expected, resolved)
	}
}

func TestResolveDirectoriesPreferWorkspaceThenGlobalConfig(t *testing.T) {
	workspaceDir := t.TempDir()
	globalDir := t.TempDir()
	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.Directory = globalDir

	directories, err := ResolveDirectories(config)
	if err != nil {
		t.Fatalf("resolve directories failed: %v", err)
	}
	expectedWorkspace := filepath.Join(workspaceDir, ".snz1dp", "skills")
	if len(directories) != 2 {
		t.Fatalf("expected workspace and global directories, got %#v", directories)
	}
	if directories[0] != expectedWorkspace {
		t.Fatalf("expected workspace directory first, got %#v", directories)
	}
	if directories[1] != globalDir {
		t.Fatalf("expected configured global directory second, got %#v", directories)
	}
}

func TestWorkspaceSkillOverridesConfiguredGlobalSkill(t *testing.T) {
	workspaceDir := t.TempDir()
	globalDir := t.TempDir()
	workspaceSkillDir := filepath.Join(workspaceDir, ".snz1dp", "skills", "shared-skill")
	globalSkillDir := filepath.Join(globalDir, "shared-skill")
	if err := os.MkdirAll(workspaceSkillDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace skill failed: %v", err)
	}
	if err := os.MkdirAll(globalSkillDir, 0o755); err != nil {
		t.Fatalf("mkdir global skill failed: %v", err)
	}
	workspaceContent := "---\nname: Shared Skill\ndescription: 工作区版本\ncategory: workspace\n---\n\n优先使用工作区版本。\n"
	globalContent := "---\nname: Shared Skill\ndescription: 全局版本\ncategory: global\n---\n\n作为全局回退。\n"
	if err := os.WriteFile(filepath.Join(workspaceSkillDir, "SKILL.md"), []byte(workspaceContent), 0o644); err != nil {
		t.Fatalf("write workspace skill failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(globalSkillDir, "SKILL.md"), []byte(globalContent), 0o644); err != nil {
		t.Fatalf("write global skill failed: %v", err)
	}

	config := internalconfig.Default()
	config.Workdir = workspaceDir
	config.Skills.Directory = globalDir

	item, directory, err := LoadByName(config, "Shared Skill")
	if err != nil {
		t.Fatalf("load shared skill failed: %v", err)
	}
	if item.Source != "workspace" {
		t.Fatalf("expected workspace source, got %#v", item)
	}
	if item.Description != "工作区版本" {
		t.Fatalf("expected workspace description, got %#v", item)
	}
	if !strings.Contains(directory, workspaceDir) || !strings.Contains(directory, globalDir) {
		t.Fatalf("expected discovery chain to include workspace and global dirs, got %q", directory)
	}
}
