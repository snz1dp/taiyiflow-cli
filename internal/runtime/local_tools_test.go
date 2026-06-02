package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/computerbridge"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

type documentToolUploadCall struct {
	FlowID    string
	SessionID string
	LocalPath string
}

type documentToolProviderStub struct {
	provider.Provider
	serviceConfig    *provider.ServiceConfig
	createdSessionID string
	uploadCalls      []documentToolUploadCall
	runRequest       provider.DocumentUnstructuredRequest
	runResult        *provider.DocumentUnstructuredResult
	imageRunRequest  provider.ImageUnderstandingRequest
	imageRunResult   *provider.ImageUnderstandingResult
	imageRunErr      error
	callback         provider.ClientCallResult
}

func (s *documentToolProviderStub) GetServiceConfig(context.Context) (*provider.ServiceConfig, error) {
	return s.serviceConfig, nil
}

func (s *documentToolProviderStub) CreateSessionID(context.Context) (string, error) {
	if strings.TrimSpace(s.createdSessionID) != "" {
		return s.createdSessionID, nil
	}
	return "session-created", nil
}

func (s *documentToolProviderStub) UploadFile(_ context.Context, flowID string, sessionID string, localPath string, remotePath string) (*provider.UploadResult, error) {
	if remotePath != "" {
		return nil, nil
	}
	s.uploadCalls = append(s.uploadCalls, documentToolUploadCall{FlowID: flowID, SessionID: sessionID, LocalPath: localPath})
	return &provider.UploadResult{FlowID: flowID, FilePath: "/remote/" + filepath.Base(localPath)}, nil
}

func (s *documentToolProviderStub) RunDocumentUnstructured(_ context.Context, request provider.DocumentUnstructuredRequest) (*provider.DocumentUnstructuredResult, error) {
	s.runRequest = request
	return s.runResult, nil
}

func (s *documentToolProviderStub) RunImageUnderstanding(_ context.Context, request provider.ImageUnderstandingRequest) (*provider.ImageUnderstandingResult, error) {
	s.imageRunRequest = request
	if s.imageRunErr != nil {
		return nil, s.imageRunErr
	}
	return s.imageRunResult, nil
}

func (s *documentToolProviderStub) SubmitClientCallResult(_ context.Context, result provider.ClientCallResult) error {
	s.callback = result
	return nil
}

func assertScreenshotImageUnderstandingTweaks(t *testing.T, request provider.ImageUnderstandingRequest) {
	t.Helper()
	if len(request.Tweaks) == 0 {
		t.Fatalf("expected screenshot image understanding tweaks, got %#v", request)
	}
	systemPrompt, ok := request.Tweaks[imageUnderstandingSystemPromptComponent].(map[string]any)
	if !ok || strings.TrimSpace(stringValue(systemPrompt["template"])) != screenshotImageUnderstandingSystemTemplate {
		t.Fatalf("expected screenshot system prompt tweak, got %#v", request.Tweaks)
	}
	userPrompt, ok := request.Tweaks[imageUnderstandingUserPromptComponent].(map[string]any)
	if !ok || strings.TrimSpace(stringValue(userPrompt["template"])) != screenshotImageUnderstandingUserTemplate {
		t.Fatalf("expected screenshot user prompt tweak, got %#v", request.Tweaks)
	}
	if !strings.Contains(stringValue(userPrompt["template"]), "{question}") {
		t.Fatalf("expected screenshot user prompt template to preserve {question}, got %#v", userPrompt)
	}
	if !strings.Contains(stringValue(systemPrompt["template"]), "截图本地像素坐标") {
		t.Fatalf("expected screenshot system prompt to mention screenshot-local coordinates, got %#v", systemPrompt)
	}
	textOutput, ok := request.Tweaks[imageUnderstandingTextOutputComponent].(map[string]any)
	if !ok || strings.TrimSpace(stringValue(textOutput["output_format"])) != "text" {
		t.Fatalf("expected screenshot text output tweak, got %#v", request.Tweaks)
	}
}

func assertImageCompressionStrategy(t *testing.T, request provider.ImageUnderstandingRequest, expected string) {
	t.Helper()
	if request.Tweaks["image_compression_strategy"] != expected {
		t.Fatalf("expected image compression strategy %q under tweaks, got %#v", expected, request)
	}
}

func assertScreenshotImageUnderstandingInput(t *testing.T, request provider.ImageUnderstandingRequest, baseInput string, expectsMetadata bool) {
	t.Helper()
	input := strings.TrimSpace(request.Input)
	if !strings.Contains(input, strings.TrimSpace(baseInput)) {
		t.Fatalf("expected screenshot image understanding input to preserve base input %q, got %#v", baseInput, request)
	}
	if !strings.Contains(input, screenshotImageUnderstandingContextPrefix) {
		t.Fatalf("expected screenshot image understanding input to include coordinate context, got %#v", request)
	}
	if !strings.Contains(input, "截图本地像素坐标") {
		t.Fatalf("expected screenshot image understanding input to mention screenshot-local coordinates, got %#v", request)
	}
	if expectsMetadata && !strings.Contains(input, "当前截图尺寸") {
		t.Fatalf("expected screenshot image understanding input to include screenshot dimensions, got %#v", request)
	}
}

func mustEncodeTestPNG(t *testing.T, width int, height int) []byte {
	t.Helper()
	imageBuffer := bytes.Buffer{}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8((x + 1) * 7), G: uint8((y + 1) * 11), B: 0x66, A: 0xFF})
		}
	}
	if err := png.Encode(&imageBuffer, img); err != nil {
		t.Fatalf("encode test png failed: %v", err)
	}
	return imageBuffer.Bytes()
}

func TestSelectImageUnderstandingTextOnlyUsesTextOutputContract(t *testing.T) {
	testCases := []struct {
		name   string
		result *provider.ImageUnderstandingResult
		want   string
	}{
		{
			name: "prefer result text",
			result: &provider.ImageUnderstandingResult{
				Output: "原始文本",
				Text:   "提取后的纯文本",
			},
			want: "提取后的纯文本",
		},
		{
			name: "fallback raw output",
			result: &provider.ImageUnderstandingResult{
				Output: "直接文本输出",
				Text:   "",
			},
			want: "直接文本输出",
		},
		{
			name: "empty text returns empty string",
			result: &provider.ImageUnderstandingResult{
				Text: "",
			},
			want: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectImageUnderstandingText(tc.result); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

type browserToolCall struct {
	ToolName       string
	Arguments      map[string]any
	ApprovalMode   string
	BrowserMode    string
	PermissionMode string
}

type browserToolExecutorStub struct {
	result map[string]any
	err    error
	calls  []browserToolCall
}

type computerToolCall struct {
	ToolName       string
	Arguments      map[string]any
	ApprovalMode   string
	PermissionMode string
}

type computerToolExecutorStub struct {
	result map[string]any
	err    error
	calls  []computerToolCall
}

func (s *browserToolExecutorStub) ExecuteBrowserTool(_ context.Context, config internalconfig.Config, toolName string, arguments map[string]any) (map[string]any, error) {
	clonedArgs := map[string]any{}
	for key, value := range arguments {
		clonedArgs[key] = value
	}
	s.calls = append(s.calls, browserToolCall{
		ToolName:       toolName,
		Arguments:      clonedArgs,
		ApprovalMode:   config.ApprovalMode,
		BrowserMode:    config.Browser.Mode,
		PermissionMode: config.Browser.PermissionMode,
	})
	if s.err != nil {
		return nil, s.err
	}
	if s.result != nil {
		return s.result, nil
	}
	return map[string]any{"tool": toolName}, nil
}

func (s *computerToolExecutorStub) ExecuteComputerTool(_ context.Context, config internalconfig.Config, toolName string, arguments map[string]any) (map[string]any, error) {
	clonedArgs := map[string]any{}
	for key, value := range arguments {
		clonedArgs[key] = value
	}
	s.calls = append(s.calls, computerToolCall{
		ToolName:       toolName,
		Arguments:      clonedArgs,
		ApprovalMode:   config.ApprovalMode,
		PermissionMode: config.Computer.PermissionMode,
	})
	if s.err != nil {
		return nil, s.err
	}
	if s.result != nil {
		return s.result, nil
	}
	return map[string]any{"tool": toolName}, nil
}

func TestWriteAndReadFile(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}

	writeResult, err := runtimeClient.writeFile(config, map[string]any{
		"path":    "notes.txt",
		"content": "hello\nworld\n",
		"mode":    "create",
	})
	if err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	if writeResult["size"].(int) != len("hello\nworld\n") {
		t.Fatalf("unexpected size: %v", writeResult["size"])
	}

	readResult, err := runtimeClient.readFile(config, map[string]any{"path": "notes.txt", "start_line": 2, "end_line": 2})
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if readResult["content"].(string) != "world" {
		t.Fatalf("unexpected read content: %q", readResult["content"].(string))
	}
}

func TestResolveAllowedPathRejectsEscape(t *testing.T) {
	tempDir := t.TempDir()
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}
	outsidePath := filepath.Join(tempDir, "..", "outside.txt")
	if _, err := resolveAllowedPath(config, outsidePath, true); err == nil {
		t.Fatal("expected resolveAllowedPath to reject sandbox escape")
	}
}

func TestResolveAllowedPathExpandsTildeForRequestedPathAndSandboxRoot(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	t.Setenv("HOMEDRIVE", filepath.VolumeName(homeDir))
	t.Setenv("HOMEPATH", strings.TrimPrefix(homeDir, filepath.VolumeName(homeDir)))
	workdir := t.TempDir()
	targetDir := filepath.Join(homeDir, ".snz1dp", "skills")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	config := internalconfig.Config{
		Workdir:      workdir,
		ApprovalMode: "auto",
		SandboxRoots: []string{"~/.snz1dp/skills"},
	}
	resolved, err := resolveAllowedPath(config, "~/.snz1dp/skills/demo.txt", true)
	if err != nil {
		t.Fatalf("expected tilde path to resolve: %v", err)
	}
	expected, err := resolveWithExistingParent(filepath.Join(targetDir, "demo.txt"))
	if err != nil {
		t.Fatalf("resolve expected path failed: %v", err)
	}
	if resolved != expected {
		t.Fatalf("unexpected resolved path: got %s want %s", resolved, expected)
	}
}

func TestSandboxEscapePolicyDenyAllowConfirm(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")

	denyRuntime := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	denyConfig := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyDeny,
	}
	if _, err := denyRuntime.resolveAllowedPathWithPolicy(denyConfig, outsidePath, true); err == nil {
		t.Fatal("expected deny policy to reject sandbox escape")
	}

	allowRuntime := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	allowConfig := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyAllow,
	}
	if resolved, err := allowRuntime.resolveAllowedPathWithPolicy(allowConfig, outsidePath, true); err != nil {
		t.Fatalf("expected allow policy to pass: %v", err)
	} else if !strings.HasSuffix(resolved, "outside.txt") {
		t.Fatalf("unexpected resolved path: %s", resolved)
	}

	confirmApprovedRuntime := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	confirmConfig := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyConfirm,
	}
	if _, err := confirmApprovedRuntime.resolveAllowedPathWithPolicy(confirmConfig, outsidePath, true); err != nil {
		t.Fatalf("expected confirm policy with approval to pass: %v", err)
	}

	confirmRejectedRuntime := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	if _, err := confirmRejectedRuntime.resolveAllowedPathWithPolicy(confirmConfig, outsidePath, true); err == nil {
		t.Fatal("expected confirm policy rejection to fail")
	}

	confirmNoInputRuntime := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	if _, err := confirmNoInputRuntime.resolveAllowedPathWithPolicy(confirmConfig, outsidePath, true); err == nil || !strings.Contains(err.Error(), "--allow参数允许访问") {
		t.Fatalf("expected confirm policy without input to fail with guidance, got: %v", err)
	}
}

func TestToolErrorDoesNotExposeBracketCode(t *testing.T) {
	err := newToolError("PERMISSION_DENIED", "路径被拒绝")
	if strings.Contains(err.Error(), "[") || strings.Contains(err.Error(), "]") {
		t.Fatalf("expected user-facing error without bracket code, got: %s", err.Error())
	}
}

func TestFileEditToolDefinitionMentionsRetryHintReuse(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := runtimeClient.clientToolDefinitionsForMode("agent")
	for _, item := range definitions {
		name, _ := item["name"].(string)
		if name != "file_edit" {
			continue
		}
		description, _ := item["description"].(string)
		if !strings.Contains(description, "prefetched_read") || !strings.Contains(description, "retry_hint.recommended_args") {
			t.Fatalf("expected file_edit description to mention retry reuse, got %q", description)
		}
		inputSchema, _ := item["inputSchema"].(map[string]any)
		properties, _ := inputSchema["properties"].(map[string]any)
		oldStringSchema, _ := properties["old_string"].(map[string]any)
		expectedHashSchema, _ := properties["expected_sha256_before"].(map[string]any)
		if !strings.Contains(stringValue(oldStringSchema["description"]), "retry_hint.recommended_args.old_string") {
			t.Fatalf("expected old_string description to mention retry reuse, got %#v", oldStringSchema)
		}
		if !strings.Contains(stringValue(expectedHashSchema["description"]), "retry_hint.recommended_args.expected_sha256_before") {
			t.Fatalf("expected expected_sha256_before description to mention retry reuse, got %#v", expectedHashSchema)
		}
		return
	}
	t.Fatal("expected file_edit tool definition")
}

func TestShellExecToolDefinitionIncludesPlatformGuidanceAndExamples(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := runtimeClient.clientToolDefinitionsForMode("agent")
	for _, item := range definitions {
		name, _ := item["name"].(string)
		if name != "shell_exec" {
			continue
		}
		description, _ := item["description"].(string)
		if !strings.Contains(description, "前台执行") || !strings.Contains(description, "后台任务句柄") {
			t.Fatalf("expected shell_exec description to keep basic execution summary, got %q", description)
		}
		for _, expected := range []string{"当前 OS:", "按当前 OS 选择命令", "command + args", "rawCommand", CurrentShellExecCommandArgsExample(), CurrentShellExecRawCommandExample()} {
			if !strings.Contains(description, expected) {
				t.Fatalf("expected shell_exec description to contain %q, got %q", expected, description)
			}
		}
		inputSchema, _ := item["inputSchema"].(map[string]any)
		properties, _ := inputSchema["properties"].(map[string]any)
		commandSchema, _ := properties["command"].(map[string]any)
		commandDescription, _ := commandSchema["description"].(string)
		if !strings.Contains(commandDescription, "不要把整条命令塞进 command") || !strings.Contains(commandDescription, CurrentShellExecCommandArgsExample()) {
			t.Fatalf("expected command schema to include structured example, got %q", commandDescription)
		}
		rawCommandSchema, _ := properties["rawCommand"].(map[string]any)
		rawCommandDescription, _ := rawCommandSchema["description"].(string)
		if !strings.Contains(rawCommandDescription, CurrentShellExecRawCommandExample()) {
			t.Fatalf("expected rawCommand schema to include shell example, got %q", rawCommandDescription)
		}
		return
	}
	t.Fatal("expected shell_exec tool definition")
}

func TestAskUserToolDefinitionAvailableForAskMode(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := runtimeClient.clientToolDefinitionsForMode("ask")
	for _, item := range definitions {
		name, _ := item["name"].(string)
		if name != "ask_user" {
			continue
		}
		description, _ := item["description"].(string)
		if !strings.Contains(description, "multi_select") {
			t.Fatalf("expected ask_user description to mention multi_select, got %q", description)
		}
		inputSchema, _ := item["inputSchema"].(map[string]any)
		properties, _ := inputSchema["properties"].(map[string]any)
		questionsSchema, _ := properties["questions"].(map[string]any)
		questionItems, _ := questionsSchema["items"].(map[string]any)
		questionProperties, _ := questionItems["properties"].(map[string]any)
		for _, key := range []string{"name", "label", "input_format", "options_list", "multi_select"} {
			if _, ok := questionProperties[key]; !ok {
				t.Fatalf("expected ask_user question property %q, got %#v", key, questionProperties)
			}
		}
		return
	}
	t.Fatal("expected ask_user tool definition in ask mode")
}

func TestAskUserToolDefinitionHiddenWhenInteractivePromptsDisabled(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	runtimeClient.SetInteractivePromptsEnabled(false)
	for _, mode := range []string{provider.RequestTypeAgent, provider.RequestTypeAsk, provider.RequestTypePlan} {
		definitions := runtimeClient.clientToolDefinitionsForMode(mode)
		for _, item := range definitions {
			name, _ := item["name"].(string)
			if name == "ask_user" {
				t.Fatalf("expected ask_user to be hidden when prompts disabled, mode=%s definitions=%#v", mode, definitions)
			}
		}
	}
}

func TestComputerMouseToolDefinitionsAcceptScreenshotDimensions(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := runtimeClient.clientToolDefinitionsForMode(provider.RequestTypeAgent)
	checked := map[string]bool{}
	for _, item := range definitions {
		name := strings.TrimSpace(stringValue(item["name"]))
		switch name {
		case computerToolMouseMove, computerToolMouseClick, computerToolMouseDrag:
			description := stringValue(item["description"])
			if !strings.Contains(description, "screenshot_width_px") || !strings.Contains(description, "screenshot_height_px") {
				t.Fatalf("expected mouse tool description to mention screenshot dimensions, tool=%s description=%q", name, description)
			}
			inputSchema, _ := item["inputSchema"].(map[string]any)
			properties, _ := inputSchema["properties"].(map[string]any)
			if _, ok := properties["screenshot_width_px"]; !ok {
				t.Fatalf("expected %s schema to accept screenshot_width_px, got %#v", name, properties)
			}
			if _, ok := properties["screenshot_height_px"]; !ok {
				t.Fatalf("expected %s schema to accept screenshot_height_px, got %#v", name, properties)
			}
			checked[name] = true
		}
	}
	for _, name := range []string{computerToolMouseMove, computerToolMouseClick, computerToolMouseDrag} {
		if !checked[name] {
			t.Fatalf("expected to inspect computer mouse tool definition %s", name)
		}
	}
}

func TestComputerKeyPressToolDefinitionRedirectsTextInputToTypeText(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := runtimeClient.clientToolDefinitionsForMode(provider.RequestTypeAgent)
	for _, item := range definitions {
		if strings.TrimSpace(stringValue(item["name"])) != computerToolKeyPress {
			continue
		}
		description := stringValue(item["description"])
		if !strings.Contains(description, "type_text") || !strings.Contains(description, "非 ASCII") || !strings.Contains(description, "多字符字符串") {
			t.Fatalf("expected key_press description to redirect text input to type_text, got %q", description)
		}
		inputSchema, _ := item["inputSchema"].(map[string]any)
		properties, _ := inputSchema["properties"].(map[string]any)
		keySchema, _ := properties["key"].(map[string]any)
		keyDescription := stringValue(keySchema["description"])
		if !strings.Contains(keyDescription, "type_text") || !strings.Contains(keyDescription, "多字符字符串") {
			t.Fatalf("expected key field description to redirect text input to type_text, got %#v", keySchema)
		}
		return
	}
	t.Fatal("expected key_press tool definition")
}

func TestClientMCPServersForModeHidesComputerToolsWhenDisabled(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	enabled := false
	servers := runtimeClient.ClientMCPServersForMode(provider.RequestTypeAgent, internalconfig.Config{
		Computer: internalconfig.ComputerConfig{Enabled: &enabled},
	})
	definitions, ok := servers[localToolServerName].([]map[string]any)
	if !ok {
		t.Fatalf("expected local tool definitions, got %#v", servers)
	}
	for _, item := range definitions {
		if isComputerTool(strings.TrimSpace(stringValue(item["name"]))) {
			t.Fatalf("expected computer tool to be hidden when disabled, got %#v", item)
		}
	}
}

func TestFilterComputerToolDefinitionsByCapabilitiesHidesUnsupportedMouseTools(t *testing.T) {
	definitions := []map[string]any{
		{"name": computerToolDisplaysContext},
		{"name": computerToolMouseMove},
		{"name": computerToolMouseClick},
		{"name": computerToolMouseDrag},
		{"name": computerToolScroll},
		{"name": computerToolKeyPress},
		{"name": "read_file"},
	}
	filtered := filterComputerToolDefinitionsByCapabilities(definitions, []computerbridge.Capability{
		{ToolName: computerToolDisplaysContext, Available: true},
		{ToolName: computerToolKeyPress, Available: true},
		{ToolName: computerToolMouseMove, Available: false},
		{ToolName: computerToolMouseClick, Available: false},
		{ToolName: computerToolMouseDrag, Available: false},
		{ToolName: computerToolScroll, Available: false},
	})
	gotNames := make([]string, 0, len(filtered))
	for _, item := range filtered {
		gotNames = append(gotNames, stringValue(item["name"]))
	}
	wantNames := []string{computerToolDisplaysContext, computerToolKeyPress, "read_file"}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("unexpected filtered computer tool definitions: got %#v want %#v", gotNames, wantNames)
	}
}

func TestClientMCPServersForModeHidesBrowserToolsWhenDisabled(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	servers := runtimeClient.ClientMCPServersForMode(provider.RequestTypeAgent, internalconfig.Config{
		Browser: internalconfig.BrowserConfig{Mode: internalconfig.BrowserModeDisabled},
	})
	definitions, ok := servers[localToolServerName].([]map[string]any)
	if !ok {
		t.Fatalf("expected local tool definitions, got %#v", servers)
	}
	for _, item := range definitions {
		if isBrowserTool(strings.TrimSpace(stringValue(item["name"]))) {
			t.Fatalf("expected browser tool to be hidden when disabled, got %#v", item)
		}
	}
}

func TestExecuteClientToolPayloadRejectsComputerToolWhenDisabled(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	computerStub := &computerToolExecutorStub{}
	runtimeClient.SetComputerToolExecutor(computerStub)
	enabled := false
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "auto",
		Computer: internalconfig.ComputerConfig{
			Enabled:        &enabled,
			PermissionMode: internalconfig.ComputerPermissionModeAsk,
		},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name":      computerToolDisplaysContext,
			"arguments": map[string]any{},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "已在配置中禁用") {
		t.Fatalf("expected disabled computer tool execution to fail, got %v", err)
	}
	if len(computerStub.calls) != 0 {
		t.Fatalf("expected disabled computer tool not to reach executor, got %#v", computerStub.calls)
	}
}

func TestExecuteClientToolPayloadRejectsBrowserToolWhenDisabled(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	browserStub := &browserToolExecutorStub{}
	runtimeClient.SetBrowserToolExecutor(browserStub)
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "auto",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeDisabled,
			PermissionMode: internalconfig.BrowserPermissionModeSkipAll,
		},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name":      browserToolNavigate,
			"arguments": map[string]any{"url": "https://example.com"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "已在配置中禁用") {
		t.Fatalf("expected disabled browser tool execution to fail, got %v", err)
	}
	if len(browserStub.calls) != 0 {
		t.Fatalf("expected disabled browser tool not to reach executor, got %#v", browserStub.calls)
	}
}

func TestSandboxEscapeAlwaysApproveSkipsSubsequentConfirmations(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	confirmCalls := 0
	runtimeClient.SetSandboxEscapeConfirmer(func(path string) (bool, error) {
		confirmCalls++
		runtimeClient.SetSandboxEscapePolicyOverride(internalconfig.SandboxEscapePolicyAllow)
		return true, nil
	})
	config := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyConfirm,
	}
	if _, err := runtimeClient.resolveAllowedPathWithPolicy(config, outsidePath, true); err != nil {
		t.Fatalf("expected first confirm to pass: %v", err)
	}
	if _, err := runtimeClient.resolveAllowedPathWithPolicy(config, outsidePath, true); err != nil {
		t.Fatalf("expected second path resolution to reuse always-approve override: %v", err)
	}
	if confirmCalls != 1 {
		t.Fatalf("expected confirmer to be called once, got %d", confirmCalls)
	}
}

func TestSandboxEscapePolicyUsesInjectedConfirmer(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")
	canonicalOutsideDir, err := filepath.EvalSymlinks(outsideDir)
	if err != nil {
		t.Fatalf("eval symlink failed: %v", err)
	}
	expectedPath := filepath.Join(canonicalOutsideDir, "outside.txt")
	stdout := &bytes.Buffer{}
	runtimeClient := NewClientRuntime(strings.NewReader(""), stdout, io.Discard)
	called := false
	runtimeClient.SetSandboxEscapeConfirmer(func(path string) (bool, error) {
		called = true
		if path != expectedPath {
			t.Fatalf("unexpected path: %s", path)
		}
		return true, nil
	})
	config := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyConfirm,
	}
	if _, err := runtimeClient.resolveAllowedPathWithPolicy(config, outsidePath, true); err != nil {
		t.Fatalf("expected injected confirmer to approve path: %v", err)
	}
	if !called {
		t.Fatal("expected injected confirmer to be called")
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no terminal prompt output when confirmer injected, got: %q", stdout.String())
	}
}

func TestSearchFiles(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tempDir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "docs", "a.md"), []byte("alpha\nbeta\nalpha again\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader("y\n"), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}

	searchResult, err := runtimeClient.searchFiles(context.Background(), config, map[string]any{
		"pattern":         "alpha",
		"path":            "docs",
		"includePatterns": []any{"*.md"},
		"maxResults":      1,
	})
	if err != nil {
		t.Fatalf("search files failed: %v", err)
	}
	if searchResult["count"].(int) != 1 {
		t.Fatalf("unexpected search count: %v", searchResult["count"])
	}
}

func TestMatchGlobSupportsDoublestar(t *testing.T) {
	testCases := []struct {
		pattern string
		path    string
		matched bool
	}{
		{pattern: "cmd/*.go", path: "cmd/main.go", matched: true},
		{pattern: "**/*.go", path: "cmd/nested/main.go", matched: true},
		{pattern: "**/review/**", path: "skills/review/scripts/run.js", matched: true},
		{pattern: "**/review/**", path: "skills/reviewer/scripts/run.js", matched: false},
	}
	for _, testCase := range testCases {
		if got := matchGlob(testCase.pattern, testCase.path); got != testCase.matched {
			t.Fatalf("matchGlob(%q, %q) = %v, want %v", testCase.pattern, testCase.path, got, testCase.matched)
		}
	}
}

func TestCanonicalClientToolNameUsesSnakeCaseNamesOnly(t *testing.T) {
	if got := canonicalClientToolName("ask_user"); got != "ask_user" {
		t.Fatalf("unexpected canonical ask_user name: %s", got)
	}
	if got := canonicalClientToolName("lsp_query"); got != "lsp_query" {
		t.Fatalf("unexpected canonical lsp_query name: %s", got)
	}
	if got := canonicalClientToolName("glob_files"); got != "glob_files" {
		t.Fatalf("unexpected canonical glob_files name: %s", got)
	}
	if got := canonicalClientToolName("search_files"); got != "search_files" {
		t.Fatalf("unexpected canonical search_files name: %s", got)
	}
	if got := canonicalClientToolName("shell_exec"); got != "shell_exec" {
		t.Fatalf("unexpected canonical shell_exec name: %s", got)
	}
	if got := canonicalClientToolName("skill"); got != "skill" {
		t.Fatalf("unexpected canonical skill name: %s", got)
	}
	if got := canonicalClientToolName("LSP"); got != "LSP" {
		t.Fatalf("legacy alias should not be normalized: %s", got)
	}
	if got := canonicalClientToolName("Glob"); got != "Glob" {
		t.Fatalf("legacy alias should not be normalized: %s", got)
	}
	if got := canonicalClientToolName("Grep"); got != "Grep" {
		t.Fatalf("legacy alias should not be normalized: %s", got)
	}
	if got := canonicalClientToolName("Bash"); got != "Bash" {
		t.Fatalf("legacy alias should not be normalized: %s", got)
	}
	if got := canonicalClientToolName("Skill"); got != "Skill" {
		t.Fatalf("legacy alias should not be normalized: %s", got)
	}
}

func TestRequiresClientToolConfirmationRespectsSkillAndWritePolicies(t *testing.T) {
	skillPayload := map[string]any{
		"function": map[string]any{
			"name":      "skill",
			"arguments": map[string]any{"name": "demo"},
		},
	}
	writePayload := map[string]any{
		"function": map[string]any{
			"name":      "write_file",
			"arguments": map[string]any{"path": "notes.txt", "content": "hello"},
		},
	}

	confirmConfig := internalconfig.Config{
		ApprovalMode:            "auto",
		SkillReviewPolicy:       internalconfig.SkillReviewPolicyConfirm,
		WriteConfirmationPolicy: internalconfig.WriteConfirmationPolicyConfirm,
	}
	if !RequiresClientToolConfirmation(confirmConfig, skillPayload) {
		t.Fatal("expected skill payload to require confirmation")
	}
	if !RequiresClientToolConfirmation(confirmConfig, writePayload) {
		t.Fatal("expected write payload to require confirmation")
	}

	ignoreConfig := confirmConfig
	ignoreConfig.SkillReviewPolicy = internalconfig.SkillReviewPolicyIgnore
	if RequiresClientToolConfirmation(ignoreConfig, skillPayload) {
		t.Fatal("expected ignored skill review policy to skip confirmation")
	}

	directConfig := confirmConfig
	directConfig.WriteConfirmationPolicy = internalconfig.WriteConfirmationPolicyDirect
	if RequiresClientToolConfirmation(directConfig, writePayload) {
		t.Fatal("expected direct write policy to skip confirmation")
	}

	askRuleConfig := confirmConfig
	askRuleConfig.PermissionRules = []internalconfig.PermissionRule{{Action: "ask", ToolName: "write_file"}}
	if !RequiresClientToolConfirmation(askRuleConfig, writePayload) {
		t.Fatal("expected write confirmation policy to keep write review despite ask rule")
	}

	allowRuleConfig := confirmConfig
	allowRuleConfig.PermissionRules = []internalconfig.PermissionRule{{Action: "allow", ToolName: "write_file"}}
	if !RequiresClientToolConfirmation(allowRuleConfig, writePayload) {
		t.Fatal("expected write confirmation policy to keep write review despite allow rule")
	}

	directAskRuleConfig := directConfig
	directAskRuleConfig.PermissionRules = []internalconfig.PermissionRule{{Action: "ask", ToolName: "write_file"}}
	if RequiresClientToolConfirmation(directAskRuleConfig, writePayload) {
		t.Fatal("expected direct write policy to bypass write review despite ask rule")
	}
}

func TestRequiresClientToolConfirmationDocumentUnstructuredFollowsApprovalMode(t *testing.T) {
	payload := map[string]any{
		"function": map[string]any{
			"name":      "document_unstructured",
			"arguments": map[string]any{"path": "report.pdf"},
		},
	}
	if !RequiresClientToolConfirmation(internalconfig.Config{ApprovalMode: "confirm"}, payload) {
		t.Fatal("expected document_unstructured to require confirmation in confirm mode")
	}
	if RequiresClientToolConfirmation(internalconfig.Config{ApprovalMode: "auto"}, payload) {
		t.Fatal("expected document_unstructured to skip confirmation in auto mode")
	}
}

func TestDocumentUnstructuredToolDefinitionAvailableInAgentMode(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := runtimeClient.clientToolDefinitionsForMode("agent")
	for _, item := range definitions {
		name, _ := item["name"].(string)
		if name != "document_unstructured" {
			continue
		}
		description, _ := item["description"].(string)
		if !strings.Contains(description, "doc_unstructured_flow_id") {
			t.Fatalf("expected document_unstructured description to mention flow id, got %q", description)
		}
		if strings.Contains(description, "doc_unstructured_exts") {
			t.Fatalf("expected document_unstructured description to leave extension guidance to skills, got %q", description)
		}
		return
	}
	t.Fatal("expected document_unstructured tool definition in agent mode")
}

func TestImageUnderstandingToolDefinitionAvailableInAgentMode(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	definitions := runtimeClient.clientToolDefinitionsForMode("agent")
	for _, item := range definitions {
		name, _ := item["name"].(string)
		if name != "image_understanding" {
			continue
		}
		description, _ := item["description"].(string)
		if !strings.Contains(description, "image_flow_id") {
			t.Fatalf("expected image_understanding description to mention flow id, got %q", description)
		}
		if !strings.Contains(description, "Browser 自动化") || !strings.Contains(description, "ComputerUse") {
			t.Fatalf("expected image_understanding description to mention screenshot reuse, got %q", description)
		}
		return
	}
	t.Fatal("expected image_understanding tool definition in agent mode")
}

func TestExecuteClientToolPayloadWithProviderImageUnderstanding(t *testing.T) {
	tempDir := t.TempDir()
	localPath := filepath.Join(tempDir, "shot.png")
	if err := os.WriteFile(localPath, []byte("fake-png"), 0o644); err != nil {
		t.Fatalf("write test image failed: %v", err)
	}
	providerStub := &documentToolProviderStub{
		serviceConfig: &provider.ServiceConfig{ImageFlowID: "image-flow"},
		imageRunResult: &provider.ImageUnderstandingResult{
			SessionID: "session-image-2",
			MediaType: "text/plain",
			Output:    "截图中央是登录表单",
			Text:      "截图中央是登录表单",
		},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
	}, map[string]any{
		"server":             map[string]any{"name": "local"},
		"flow_id":            "chat-flow",
		"client_context_dir": tempDir,
		"context":            map[string]any{"session_id": "session-1"},
		"function": map[string]any{
			"name": "image_understanding",
			"arguments": map[string]any{
				"path":  "shot.png",
				"input": "请概述截图内容",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected image_understanding to succeed, got %v", err)
	}
	if toolName != "image_understanding" {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if len(providerStub.uploadCalls) != 1 {
		t.Fatalf("expected one upload call, got %#v", providerStub.uploadCalls)
	}
	if providerStub.uploadCalls[0].FlowID != "chat-flow" || providerStub.uploadCalls[0].SessionID != "session-1" {
		t.Fatalf("unexpected upload call context: %#v", providerStub.uploadCalls[0])
	}
	if providerStub.imageRunRequest.FlowID != "image-flow" {
		t.Fatalf("expected image flow id, got %#v", providerStub.imageRunRequest)
	}
	if len(providerStub.imageRunRequest.InputFiles) != 1 || providerStub.imageRunRequest.InputFiles[0] != "/remote/shot.png" {
		t.Fatalf("unexpected uploaded remote files: %#v", providerStub.imageRunRequest.InputFiles)
	}
	if strings.TrimSpace(providerStub.imageRunRequest.Input) != "请概述截图内容" {
		t.Fatalf("expected image prompt to be forwarded, got %#v", providerStub.imageRunRequest)
	}
	assertImageCompressionStrategy(t, providerStub.imageRunRequest, provider.ImageCompressionStrategyFollowSystem)
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected image_understanding result map, got %#v", resultAny)
	}
	if result["text"] != "截图中央是登录表单" {
		t.Fatalf("unexpected image text: %#v", result)
	}
	if result["session_id"] != "session-image-2" {
		t.Fatalf("expected returned session id, got %#v", result["session_id"])
	}
	textOutput, ok := providerStub.imageRunRequest.Tweaks[imageUnderstandingTextOutputComponent].(map[string]any)
	if !ok || strings.TrimSpace(stringValue(textOutput["output_format"])) != "text" {
		t.Fatalf("expected text output tweak for image_understanding, got %#v", providerStub.imageRunRequest.Tweaks)
	}
	if _, exists := providerStub.imageRunRequest.Tweaks[imageUnderstandingSystemPromptComponent]; exists {
		t.Fatalf("expected generic image_understanding not to force screenshot system prompt, got %#v", providerStub.imageRunRequest.Tweaks)
	}
}

func TestExecuteClientToolPayloadWithProviderDocumentUnstructured(t *testing.T) {
	tempDir := t.TempDir()
	localPath := filepath.Join(tempDir, "report.pdf")
	if err := os.WriteFile(localPath, []byte("fake-pdf"), 0o644); err != nil {
		t.Fatalf("write test file failed: %v", err)
	}
	providerStub := &documentToolProviderStub{
		serviceConfig: &provider.ServiceConfig{DocUnstructuredFlowID: "doc-flow", DocExts: []string{"pdf"}},
		runResult:     &provider.DocumentUnstructuredResult{SessionID: "session-2", MediaType: "application/json", Output: `{"items":[{"text":"parsed content"}]}`},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		DocUnstructuredExts: []string{"pdf"},
	}, map[string]any{
		"server":             map[string]any{"name": "local"},
		"flow_id":            "chat-flow",
		"client_context_dir": tempDir,
		"context":            map[string]any{"session_id": "session-1"},
		"function": map[string]any{
			"name": "document_unstructured",
			"arguments": map[string]any{
				"path":  "report.pdf",
				"input": "请提取正文",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected document_unstructured to succeed, got %v", err)
	}
	if toolName != "document_unstructured" {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if len(providerStub.uploadCalls) != 1 {
		t.Fatalf("expected one upload call, got %#v", providerStub.uploadCalls)
	}
	if providerStub.uploadCalls[0].FlowID != "chat-flow" || providerStub.uploadCalls[0].SessionID != "session-1" {
		t.Fatalf("unexpected upload call context: %#v", providerStub.uploadCalls[0])
	}
	if providerStub.runRequest.FlowID != "doc-flow" {
		t.Fatalf("expected doc flow id, got %#v", providerStub.runRequest)
	}
	if len(providerStub.runRequest.InputFiles) != 1 || providerStub.runRequest.InputFiles[0] != "/remote/report.pdf" {
		t.Fatalf("unexpected uploaded remote files: %#v", providerStub.runRequest.InputFiles)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected document_unstructured result map, got %#v", resultAny)
	}
	if result["count"] != 1 {
		t.Fatalf("unexpected count: %#v", result)
	}
	items, ok := result["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("unexpected items: %#v", result["items"])
	}
	if result["session_id"] != "session-2" {
		t.Fatalf("expected returned session id, got %#v", result["session_id"])
	}
}

func TestExecuteClientToolPayloadWithProviderDocumentUnstructuredCreatesSessionWhenMissing(t *testing.T) {
	tempDir := t.TempDir()
	localPath := filepath.Join(tempDir, "report.pdf")
	if err := os.WriteFile(localPath, []byte("fake-pdf"), 0o644); err != nil {
		t.Fatalf("write test file failed: %v", err)
	}
	providerStub := &documentToolProviderStub{
		serviceConfig:    &provider.ServiceConfig{DocUnstructuredFlowID: "doc-flow", DocExts: []string{"pdf"}},
		createdSessionID: "session-created",
		runResult:        &provider.DocumentUnstructuredResult{MediaType: "application/json", Output: `{"items":[{"text":"parsed content"}]}`},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	_, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		DocUnstructuredExts: []string{"pdf"},
	}, map[string]any{
		"server":             map[string]any{"name": "local"},
		"flow_id":            "chat-flow",
		"client_context_dir": tempDir,
		"function": map[string]any{
			"name": "document_unstructured",
			"arguments": map[string]any{
				"path":  "report.pdf",
				"input": "请提取正文",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected document_unstructured to succeed, got %v", err)
	}
	if len(providerStub.uploadCalls) != 1 || providerStub.uploadCalls[0].SessionID != "session-created" {
		t.Fatalf("expected generated session_id to be used for upload, got %#v", providerStub.uploadCalls)
	}
	if providerStub.runRequest.SessionID != "session-created" {
		t.Fatalf("expected generated session_id to be reused by run request, got %#v", providerStub.runRequest)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected document_unstructured result map, got %#v", resultAny)
	}
	if result["session_id"] != "session-created" {
		t.Fatalf("expected generated session id in result fallback, got %#v", result["session_id"])
	}
}

func TestExecuteClientToolPayloadWithProviderDocumentUnstructuredRejectsDisabledExtension(t *testing.T) {
	tempDir := t.TempDir()
	localPath := filepath.Join(tempDir, "report.pdf")
	if err := os.WriteFile(localPath, []byte("fake-pdf"), 0o644); err != nil {
		t.Fatalf("write test file failed: %v", err)
	}
	providerStub := &documentToolProviderStub{
		serviceConfig: &provider.ServiceConfig{DocUnstructuredFlowID: "doc-flow", DocExts: []string{"pdf"}},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	_, _, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		DocUnstructuredExts: []string{"docx"},
	}, map[string]any{
		"server":             map[string]any{"name": "local"},
		"flow_id":            "chat-flow",
		"client_context_dir": tempDir,
		"function": map[string]any{
			"name":      "document_unstructured",
			"arguments": map[string]any{"path": "report.pdf"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "doc_unstructured_exts") {
		t.Fatalf("expected disabled extension error, got %v", err)
	}
}

func TestExecuteClientToolPayloadWithProviderDocumentUnstructuredRedirectsImageToImageUnderstanding(t *testing.T) {
	tempDir := t.TempDir()
	localPath := filepath.Join(tempDir, "shot.png")
	if err := os.WriteFile(localPath, []byte("fake-png"), 0o644); err != nil {
		t.Fatalf("write test image failed: %v", err)
	}
	providerStub := &documentToolProviderStub{
		serviceConfig: &provider.ServiceConfig{DocUnstructuredFlowID: "doc-flow", DocExts: []string{"pdf"}, ImageFlowID: "image-flow"},
		imageRunResult: &provider.ImageUnderstandingResult{
			SessionID: "session-image-redirect",
			MediaType: "text/plain",
			Output:    "图片里是流程图",
			Text:      "图片里是流程图",
		},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		DocUnstructuredExts: []string{"pdf"},
	}, map[string]any{
		"server":             map[string]any{"name": "local"},
		"flow_id":            "chat-flow",
		"client_context_dir": tempDir,
		"context":            map[string]any{"session_id": "session-1"},
		"function": map[string]any{
			"name": "document_unstructured",
			"arguments": map[string]any{
				"path":  "shot.png",
				"input": "请描述图片",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected image redirect to succeed, got %v", err)
	}
	if toolName != "document_unstructured" {
		t.Fatalf("unexpected tool name after redirect: %s", toolName)
	}
	if providerStub.runRequest.FlowID != "" {
		t.Fatalf("expected document flow not to run for image redirect, got %#v", providerStub.runRequest)
	}
	if providerStub.imageRunRequest.FlowID != "image-flow" {
		t.Fatalf("expected image flow to run after redirect, got %#v", providerStub.imageRunRequest)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected redirect result map, got %#v", resultAny)
	}
	if result["text"] != "图片里是流程图" {
		t.Fatalf("unexpected redirect text: %#v", result)
	}
}

func TestHandleClientToolEventSubmitsDocumentUnstructuredCallback(t *testing.T) {
	tempDir := t.TempDir()
	localPath := filepath.Join(tempDir, "report.pdf")
	if err := os.WriteFile(localPath, []byte("fake-pdf"), 0o644); err != nil {
		t.Fatalf("write test file failed: %v", err)
	}
	providerStub := &documentToolProviderStub{
		serviceConfig: &provider.ServiceConfig{DocUnstructuredFlowID: "doc-flow", DocExts: []string{"pdf"}},
		runResult:     &provider.DocumentUnstructuredResult{SessionID: "session-1", MediaType: "text/plain", Output: "plain text"},
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	err := runtimeClient.HandleClientToolEvent(context.Background(), providerStub, internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		DocUnstructuredExts: []string{"pdf"},
	}, map[string]any{
		"flow_id": "chat-flow",
		"server":  map[string]any{"name": "local"},
		"context": map[string]any{"session_id": "session-1", "run_id": "run-1", "message_id": "msg-1"},
		"function": map[string]any{
			"id":   "call-1",
			"name": "document_unstructured",
			"arguments": map[string]any{
				"path": localPath,
			},
		},
	})
	if err != nil {
		t.Fatalf("expected callback submission to succeed, got %v", err)
	}
	if !providerStub.callback.Success {
		t.Fatalf("expected successful callback, got %#v", providerStub.callback)
	}
	result, ok := providerStub.callback.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected callback result map, got %#v", providerStub.callback.Result)
	}
	if result["raw_output"] != "plain text" {
		t.Fatalf("expected raw output in callback result, got %#v", result)
	}
}

func TestExecuteClientToolPayloadAskUserUsesStdioFallback(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader("项目 Alpha\n2\n"), io.Discard, io.Discard)
	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), internalconfig.Config{ApprovalMode: "auto"}, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "ask_user",
			"arguments": map[string]any{
				"questions": []any{
					map[string]any{"name": "project", "label": "项目名称", "input_format": "text"},
					map[string]any{"name": "priority", "label": "优先级", "input_format": "select", "options_list": []any{"low", "high"}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected ask_user fallback to succeed, got %v", err)
	}
	if toolName != "ask_user" {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected ask_user result map, got %#v", resultAny)
	}
	if result["project"] != "项目 Alpha" {
		t.Fatalf("unexpected text answer: %#v", result)
	}
	if result["priority"] != "high" {
		t.Fatalf("unexpected select answer: %#v", result)
	}
}

func TestExecuteClientToolPayloadAskUserParsesMultiSelect(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader("1, beta\n"), io.Discard, io.Discard)
	_, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), internalconfig.Config{ApprovalMode: "auto"}, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "ask_user",
			"arguments": map[string]any{
				"questions": []any{
					map[string]any{
						"name":         "targets",
						"label":        "选择目标",
						"input_format": "select",
						"multi_select": true,
						"options_list": []any{"alpha", "beta", map[string]any{"label": "Gamma", "value": "gamma"}},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("expected ask_user multi-select to succeed, got %v", err)
	}
	result, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected ask_user result map, got %#v", resultAny)
	}
	values, ok := result["targets"].([]string)
	if !ok {
		t.Fatalf("expected multi-select values, got %#v", result["targets"])
	}
	if len(values) != 2 || values[0] != "alpha" || values[1] != "beta" {
		t.Fatalf("unexpected multi-select values: %#v", values)
	}
}

func TestExecuteClientToolPayloadAskUserCancelReturnsError(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader("/cancel\n"), io.Discard, io.Discard)
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), internalconfig.Config{ApprovalMode: "auto"}, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "ask_user",
			"arguments": map[string]any{
				"questions": []any{
					map[string]any{"name": "project", "label": "项目名称", "input_format": "text"},
				},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "取消 ask_user") {
		t.Fatalf("expected ask_user cancel error, got %v", err)
	}
}

func TestSetStreamEventLogsEnabledSuppressesBracketLogs(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "notes.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	stdout := &bytes.Buffer{}
	runtimeClient := NewClientRuntime(strings.NewReader(""), stdout, io.Discard)
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "auto"}

	runtimeClient.SetStreamEventLogsEnabled(false)
	if _, err := runtimeClient.readFile(config, map[string]any{"path": "notes.txt", "start_line": 1, "end_line": 1}); err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	result, err := runtimeClient.runCommand(context.Background(), config, map[string]any{"raw_command": "echo ok"})
	if err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	if output := stdout.String(); strings.Contains(output, "[本地工具]") || strings.Contains(output, "[本地命令]") || strings.Contains(output, "[执行客户端命令]") {
		t.Fatalf("expected bracket logs to be suppressed, got %q", output)
	}
	if strings.Contains(stdout.String(), "ok") {
		t.Fatalf("expected command stdout to stay hidden when stream logs disabled, got %q", stdout.String())
	}
	if stringValue(result["stdout"]) != "ok\n" {
		t.Fatalf("expected command stdout retained in result, got %#v", result["stdout"])
	}

	stdout.Reset()
	runtimeClient.SetStreamEventLogsEnabled(true)
	if _, err := runtimeClient.readFile(config, map[string]any{"path": "notes.txt", "start_line": 1, "end_line": 1}); err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if _, err := runtimeClient.runCommand(context.Background(), config, map[string]any{"raw_command": "echo ok"}); err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "[本地工具] read_file") {
		t.Fatalf("expected read_file stream log, got %q", output)
	}
	if !strings.Contains(output, "[本地命令]") {
		t.Fatalf("expected run command stream log, got %q", output)
	}
}

func TestRunCommandForwardsStdinToChildProcess(t *testing.T) {
	if os.Getenv("TAIYIFLOW_HELPER_PROCESS") == "stdin-echo" {
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			_, _ = os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(content)
		os.Exit(0)
	}

	runtimeClient := NewClientRuntime(strings.NewReader("hello from stdin\n"), io.Discard, io.Discard)
	config := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.runCommand(context.Background(), config, map[string]any{
		"command": os.Args[0],
		"args":    []string{"-test.run=TestRunCommandForwardsStdinToChildProcess$"},
		"env": map[string]string{
			"TAIYIFLOW_HELPER_PROCESS": "stdin-echo",
		},
	})
	if err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	if got := stringValue(result["stdout"]); got != "hello from stdin\n" {
		t.Fatalf("expected child process to receive runtime stdin, got %q", got)
	}
}

func TestRunCommandUsesPromptedStdinToChildProcess(t *testing.T) {
	if os.Getenv("TAIYIFLOW_HELPER_PROCESS") == "stdin-echo" {
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			_, _ = os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(content)
		os.Exit(0)
	}

	runtimeClient := NewClientRuntime(strings.NewReader("host stdin should not be used\n"), io.Discard, io.Discard)
	runtimeClient.SetAskUserPrompter(func(ctx context.Context, request AskUserRequest) (AskUserResponse, error) {
		if len(request.Questions) != 1 {
			t.Fatalf("expected one stdin question, got %#v", request.Questions)
		}
		if got := request.Questions[0].Label; got != "请输入命令所需输入" {
			t.Fatalf("unexpected stdin prompt label: %q", got)
		}
		return AskUserResponse{Answers: map[string]any{"stdin": "input from overlay"}}, nil
	})
	config := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.runCommand(context.Background(), config, map[string]any{
		"command":      os.Args[0],
		"args":         []string{"-test.run=TestRunCommandUsesPromptedStdinToChildProcess$"},
		"stdin_prompt": "请输入命令所需输入",
		"env": map[string]string{
			"TAIYIFLOW_HELPER_PROCESS": "stdin-echo",
		},
	})
	if err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	if got := stringValue(result["stdout"]); got != "input from overlay\n" {
		t.Fatalf("expected child process to receive prompted stdin, got %q", got)
	}
}

func TestRunCommandRejectsPromptedStdinWhenInteractivePromptsDisabled(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader("host stdin should not be used\n"), io.Discard, io.Discard)
	runtimeClient.SetInteractivePromptsEnabled(false)
	config := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	_, err := runtimeClient.runCommand(context.Background(), config, map[string]any{
		"command":      "echo",
		"args":         []string{"hello"},
		"stdin_prompt": "请输入命令所需输入",
	})
	if err == nil || !strings.Contains(err.Error(), "task 模式不支持 stdin_prompt") {
		t.Fatalf("expected stdin_prompt rejection when prompts disabled, got %v", err)
	}
}

func TestRunCommandDoesNotForwardHostStdinWhenInteractivePromptsDisabled(t *testing.T) {
	if os.Getenv("TAIYIFLOW_HELPER_PROCESS") == "stdin-echo" {
		content, err := io.ReadAll(os.Stdin)
		if err != nil {
			_, _ = os.Stderr.WriteString(err.Error())
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(content)
		os.Exit(0)
	}

	runtimeClient := NewClientRuntime(strings.NewReader("host stdin should not be forwarded\n"), io.Discard, io.Discard)
	runtimeClient.SetInteractivePromptsEnabled(false)
	config := internalconfig.Config{Workdir: t.TempDir(), ApprovalMode: "auto"}

	result, err := runtimeClient.runCommand(context.Background(), config, map[string]any{
		"command": os.Args[0],
		"args":    []string{"-test.run=TestRunCommandDoesNotForwardHostStdinWhenInteractivePromptsDisabled$"},
		"env": map[string]string{
			"TAIYIFLOW_HELPER_PROCESS": "stdin-echo",
		},
	})
	if err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	if got := stringValue(result["stdout"]); got != "" {
		t.Fatalf("expected child process not to receive host stdin, got %q", got)
	}
}

func TestPermissionRuleDeniesMutatingClientTool(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		PermissionRules: []internalconfig.PermissionRule{{
			Action:   "deny",
			ToolName: "write_file",
		}},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"path":    "notes.txt",
				"content": "blocked",
				"mode":    "create",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "命中权限规则") {
		t.Fatalf("expected permission rule denial, got %v", err)
	}
}

func TestExecuteClientToolPayloadRejectsConfirmationWhenInteractivePromptsDisabled(t *testing.T) {
	tempDir := t.TempDir()
	stdout := &bytes.Buffer{}
	runtimeClient := NewClientRuntime(strings.NewReader("y\n"), stdout, io.Discard)
	runtimeClient.SetInteractivePromptsEnabled(false)
	config := internalconfig.Config{Workdir: tempDir, ApprovalMode: "confirm"}

	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"path":    "notes.txt",
				"content": "blocked",
				"mode":    "create",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "task 模式不支持交互式工具确认") {
		t.Fatalf("expected non-interactive confirmation rejection, got %v", err)
	}
	if strings.Contains(stdout.String(), "[客户端工具]") || strings.Contains(stdout.String(), "是否执行？") {
		t.Fatalf("expected no tool confirmation prompt output, got %q", stdout.String())
	}
	if _, statErr := os.Stat(filepath.Join(tempDir, "notes.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("expected file not to be created, got %v", statErr)
	}
}

func TestSandboxEscapeConfirmSkipsPromptWhenInteractivePromptsDisabled(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.txt")
	stdout := &bytes.Buffer{}
	runtimeClient := NewClientRuntime(strings.NewReader("y\n"), stdout, io.Discard)
	runtimeClient.SetInteractivePromptsEnabled(false)
	config := internalconfig.Config{
		Workdir:             tempDir,
		ApprovalMode:        "auto",
		SandboxEscapePolicy: internalconfig.SandboxEscapePolicyConfirm,
	}

	if _, err := runtimeClient.resolveAllowedPathWithPolicy(config, outsidePath, true); err == nil || !strings.Contains(err.Error(), "--allow参数允许访问") {
		t.Fatalf("expected non-interactive sandbox escape denial with guidance, got %v", err)
	}
	if strings.Contains(stdout.String(), "[路径越界确认]") || strings.Contains(stdout.String(), "是否允许本次访问") {
		t.Fatalf("expected no sandbox confirmation prompt output, got %q", stdout.String())
	}
}

func TestPermissionRuleAskAndAllowShellExec(t *testing.T) {
	tempDir := t.TempDir()
	askRuntime := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	askConfig := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		PermissionRules: []internalconfig.PermissionRule{{
			Action:        "ask",
			CommandPrefix: "echo",
		}},
	}
	if _, err := askRuntime.ExecuteShellCommand(context.Background(), askConfig, "echo blocked"); err == nil || !strings.Contains(err.Error(), "用户拒绝执行") {
		t.Fatalf("expected ask rule rejection, got %v", err)
	}

	allowRuntime := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	allowConfig := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "confirm",
		PermissionRules: []internalconfig.PermissionRule{{
			Action:        "allow",
			CommandPrefix: "echo",
		}},
	}
	result, err := allowRuntime.ExecuteShellCommand(context.Background(), allowConfig, "echo allowed")
	if err != nil {
		t.Fatalf("expected allow rule to bypass confirmation, got %v", err)
	}
	if !strings.Contains(stringValue(result["stdout"]), "allowed") {
		t.Fatalf("unexpected allow rule stdout: %#v", result)
	}
}

func TestExecuteClientToolPayloadAutoModeBypassesAskRuleForFileEdit(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "notes.txt")
	if err := os.WriteFile(filePath, []byte("before\n"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		PermissionRules: []internalconfig.PermissionRule{{
			Action:   "ask",
			ToolName: "file_edit",
		}},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "file_edit",
			"arguments": map[string]any{
				"path":       "notes.txt",
				"old_string": "before",
				"new_string": "after",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected auto mode to bypass ask rule, got %v", err)
	}
	content, readErr := os.ReadFile(filePath)
	if readErr != nil {
		t.Fatalf("read file failed: %v", readErr)
	}
	if !strings.Contains(string(content), "after") {
		t.Fatalf("expected file_edit to be applied, got %q", string(content))
	}
}

func TestExecuteClientToolPayloadConfirmModeIgnoresAllowRuleForWriteReview(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	config := internalconfig.Config{
		Workdir:                 tempDir,
		ApprovalMode:            "confirm",
		WriteConfirmationPolicy: internalconfig.WriteConfirmationPolicyConfirm,
		PermissionRules:         []internalconfig.PermissionRule{{Action: "allow", ToolName: "write_file"}},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "write_file",
			"arguments": map[string]any{
				"path":    "notes.txt",
				"content": "blocked",
				"mode":    "create",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "用户拒绝执行") {
		t.Fatalf("expected write review confirmation to remain active despite allow rule, got %v", err)
	}
}

func TestExecuteClientToolPayloadAutoModeBypassesSkillReview(t *testing.T) {
	tempDir := t.TempDir()
	skillDir := filepath.Join(tempDir, ".snz1dp", "skills", "context-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: Context Review\ndescription: 审查工作区技能\n---\n\n检查工作区上下文。\n"), 0o644); err != nil {
		t.Fatalf("write skill file failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	config := internalconfig.Config{
		Workdir:           tempDir,
		ApprovalMode:      "auto",
		SkillReviewPolicy: internalconfig.SkillReviewPolicyConfirm,
	}
	_, result, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": "skill",
			"arguments": map[string]any{
				"name": "Context Review",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected auto mode to bypass skill review confirmation, got %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok || strings.TrimSpace(stringValue(resultMap["name"])) != "Context Review" {
		t.Fatalf("unexpected skill result: %#v", result)
	}
}

func TestRequiresClientToolConfirmationSkipsReadonlyMutatingTools(t *testing.T) {
	writePayload := map[string]any{
		"function": map[string]any{
			"name":      "write_file",
			"arguments": map[string]any{"path": "notes.txt", "content": "hello"},
		},
	}
	config := internalconfig.Config{
		ApprovalMode:            "readonly",
		WriteConfirmationPolicy: internalconfig.WriteConfirmationPolicyConfirm,
	}
	if RequiresClientToolConfirmation(config, writePayload) {
		t.Fatal("expected readonly mutating tool to be denied directly without extra confirmation")
	}
}

func TestExecuteClientToolPayloadDispatchesBrowserTool(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	browserStub := &browserToolExecutorStub{result: map[string]any{"tab_id": "tab-1", "url": "https://example.com"}}
	runtimeClient.SetBrowserToolExecutor(browserStub)
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeIsolated,
			PermissionMode: internalconfig.BrowserPermissionModeSkipAll,
		},
	}
	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": browserToolNavigate,
			"arguments": map[string]any{
				"url": "https://example.com",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected browser tool execution to succeed, got %v", err)
	}
	if toolName != browserToolNavigate {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if len(browserStub.calls) != 1 || browserStub.calls[0].ToolName != browserToolNavigate {
		t.Fatalf("expected browser executor to receive navigate call, got %#v", browserStub.calls)
	}
	resultMap, ok := resultAny.(map[string]any)
	if !ok || strings.TrimSpace(stringValue(resultMap["tab_id"])) != "tab-1" {
		t.Fatalf("unexpected browser tool result: %#v", resultAny)
	}
}

func TestBrowserPermissionModeAskOverridesAuto(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	runtimeClient.SetBrowserToolExecutor(&browserToolExecutorStub{})
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "auto",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeIsolated,
			PermissionMode: internalconfig.BrowserPermissionModeAsk,
		},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": browserToolClick,
			"arguments": map[string]any{
				"selector": "button.submit",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "用户拒绝执行") {
		t.Fatalf("expected browser ask mode to require confirmation, got %v", err)
	}
}

func TestBrowserPermissionModeSkipAllBypassesConfirm(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	browserStub := &browserToolExecutorStub{result: map[string]any{"tab_id": "tab-2"}}
	runtimeClient.SetBrowserToolExecutor(browserStub)
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "confirm",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeIsolated,
			PermissionMode: internalconfig.BrowserPermissionModeSkipAll,
		},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": browserToolType,
			"arguments": map[string]any{
				"selector": "input[name=q]",
				"text":     "taiyiflow",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected skip_all_permission_checks to bypass confirm, got %v", err)
	}
	if len(browserStub.calls) != 1 || browserStub.calls[0].ToolName != browserToolType {
		t.Fatalf("expected browser executor call, got %#v", browserStub.calls)
	}
}

func TestBrowserPermissionModeSkipAllBypassesReadonly(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	browserStub := &browserToolExecutorStub{result: map[string]any{"tab_id": "tab-readonly"}}
	runtimeClient.SetBrowserToolExecutor(browserStub)
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "readonly",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeChromeSession,
			PermissionMode: internalconfig.BrowserPermissionModeSkipAll,
		},
	}
	toolName, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": browserToolNavigate,
			"arguments": map[string]any{
				"url": "https://example.com/read-only-browser",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected browser permission mode to bypass readonly for browser tool, got %v", err)
	}
	if toolName != browserToolNavigate {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if len(browserStub.calls) != 1 || browserStub.calls[0].ToolName != browserToolNavigate {
		t.Fatalf("expected browser executor call, got %#v", browserStub.calls)
	}
}

func TestRequiresClientToolConfirmationUsesBrowserPermissionModeInReadonly(t *testing.T) {
	payload := map[string]any{
		"function": map[string]any{
			"name": browserToolClick,
			"arguments": map[string]any{
				"selector": "button.submit",
			},
		},
	}
	config := internalconfig.Config{
		ApprovalMode: "readonly",
		Browser: internalconfig.BrowserConfig{
			PermissionMode: internalconfig.BrowserPermissionModeAsk,
		},
	}
	if !RequiresClientToolConfirmation(config, payload) {
		t.Fatal("expected readonly browser tool to keep browser-specific confirmation behavior")
	}
}

func TestBrowserScreenshotAutoPathUsesWorkspaceScopedFile(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	browserStub := &browserToolExecutorStub{result: map[string]any{"tab_id": "tab-3"}}
	runtimeClient.SetBrowserToolExecutor(browserStub)
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeIsolated,
			PermissionMode: internalconfig.BrowserPermissionModeSkipAll,
		},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name":      browserToolScreenshot,
			"arguments": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("expected screenshot path auto generation to succeed, got %v", err)
	}
	if len(browserStub.calls) != 1 {
		t.Fatalf("expected one browser executor call, got %#v", browserStub.calls)
	}
	pathValue := strings.TrimSpace(stringValue(browserStub.calls[0].Arguments["path"]))
	if pathValue == "" {
		t.Fatal("expected screenshot tool to receive auto-generated path")
	}
	resolvedTempDir, err := resolveWithExistingParent(tempDir)
	if err != nil {
		t.Fatalf("resolve temp dir failed: %v", err)
	}
	if !strings.HasPrefix(pathValue, resolvedTempDir) {
		t.Fatalf("expected screenshot path under resolved workdir %s, got %s", resolvedTempDir, pathValue)
	}
	if !strings.HasSuffix(pathValue, ".png") {
		t.Fatalf("expected png screenshot path, got %s", pathValue)
	}
}

func TestBrowserTabsCreateDefaultsURLAndNormalizesResult(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	browserStub := &browserToolExecutorStub{result: map[string]any{"tab_id": "tab-create"}}
	runtimeClient.SetBrowserToolExecutor(browserStub)
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "auto",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeChromeSession,
			PermissionMode: internalconfig.BrowserPermissionModeSkipAll,
		},
	}
	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name":      browserToolTabsCreate,
			"arguments": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("expected tabs_create to succeed, got %v", err)
	}
	if toolName != browserToolTabsCreate {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if len(browserStub.calls) != 1 {
		t.Fatalf("expected one browser executor call, got %#v", browserStub.calls)
	}
	if got := strings.TrimSpace(stringValue(browserStub.calls[0].Arguments["url"])); got != "about:blank" {
		t.Fatalf("expected default tabs_create url about:blank, got %q", got)
	}
	if active := boolValue(browserStub.calls[0].Arguments["activate"]); !active {
		t.Fatalf("expected tabs_create activate default true, got %#v", browserStub.calls[0].Arguments)
	}
	resultMap, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if got := strings.TrimSpace(stringValue(resultMap["url"])); got != "about:blank" {
		t.Fatalf("expected normalized tabs_create url, got %#v", resultMap)
	}
	if !boolValue(resultMap["created"]) {
		t.Fatalf("expected normalized created flag, got %#v", resultMap)
	}
	if !boolValue(resultMap["active"]) {
		t.Fatalf("expected normalized active flag, got %#v", resultMap)
	}
}

func TestBrowserReadTextNormalizesArgumentAliasAndResultShape(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	browserStub := &browserToolExecutorStub{result: map[string]any{"tab_id": "tab-read", "text": "hello world"}}
	runtimeClient.SetBrowserToolExecutor(browserStub)
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "auto",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeChromeSession,
			PermissionMode: internalconfig.BrowserPermissionModeSkipAll,
		},
	}
	_, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": browserToolReadText,
			"arguments": map[string]any{
				"selector":  "main",
				"max_chars": 5,
			},
		},
	})
	if err != nil {
		t.Fatalf("expected read_text to succeed, got %v", err)
	}
	if len(browserStub.calls) != 1 {
		t.Fatalf("expected one browser executor call, got %#v", browserStub.calls)
	}
	if got := intValue(browserStub.calls[0].Arguments["max_length"], 0); got != 5 {
		t.Fatalf("expected max_length alias to be forwarded, got %#v", browserStub.calls[0].Arguments)
	}
	resultMap, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if got := strings.TrimSpace(stringValue(resultMap["selector"])); got != "main" {
		t.Fatalf("expected normalized selector, got %#v", resultMap)
	}
	if !boolValue(resultMap["found"]) {
		t.Fatalf("expected normalized found flag, got %#v", resultMap)
	}
	if got := intValue(resultMap["total_length"], 0); got != len("hello world") {
		t.Fatalf("expected normalized total_length, got %#v", resultMap)
	}
	if boolValue(resultMap["truncated"]) {
		t.Fatalf("expected fallback truncated=false without total_length drift, got %#v", resultMap)
	}
}

func TestBrowserScreenshotDecodesExtensionResultToPath(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	expectedBytes := mustEncodeTestPNG(t, 9, 5)
	browserStub := &browserToolExecutorStub{result: map[string]any{
		"tab_id":       "tab-shot",
		"image_base64": base64.StdEncoding.EncodeToString(expectedBytes),
		"format":       "png",
	}}
	runtimeClient.SetBrowserToolExecutor(browserStub)
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeChromeSession,
			PermissionMode: internalconfig.BrowserPermissionModeSkipAll,
		},
	}
	_, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name":      browserToolScreenshot,
			"arguments": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("expected screenshot to succeed, got %v", err)
	}
	resultMap, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	pathValue := strings.TrimSpace(stringValue(resultMap["path"]))
	if pathValue == "" {
		t.Fatalf("expected normalized screenshot path, got %#v", resultMap)
	}
	actualBytes, readErr := os.ReadFile(pathValue)
	if readErr != nil {
		t.Fatalf("expected screenshot file to be written, got %v", readErr)
	}
	if !bytes.Equal(actualBytes, expectedBytes) {
		t.Fatalf("expected screenshot bytes %q, got %q", expectedBytes, actualBytes)
	}
	if got := intValue(resultMap["size"], 0); got != len(expectedBytes) {
		t.Fatalf("expected screenshot size %d, got %#v", len(expectedBytes), resultMap)
	}
	if got := intValue(resultMap["screenshot_width_px"], 0); got != 9 {
		t.Fatalf("expected screenshot width 9, got %#v", resultMap)
	}
	if got := intValue(resultMap["screenshot_height_px"], 0); got != 5 {
		t.Fatalf("expected screenshot height 5, got %#v", resultMap)
	}
	if got := strings.TrimSpace(stringValue(resultMap["content_type"])); got != "image/png" {
		t.Fatalf("expected screenshot content_type image/png, got %#v", resultMap)
	}
	if got := strings.TrimSpace(stringValue(resultMap["image_base64"])); got != "" {
		t.Fatalf("expected image_base64 to be removed after normalization, got %#v", resultMap)
	}
}

func TestBrowserScreenshotAddsImageUnderstandingWhenConfigured(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	expectedBytes := mustEncodeTestPNG(t, 13, 7)
	browserStub := &browserToolExecutorStub{result: map[string]any{
		"tab_id":       "tab-shot",
		"image_base64": base64.StdEncoding.EncodeToString(expectedBytes),
		"format":       "png",
	}}
	runtimeClient.SetBrowserToolExecutor(browserStub)
	providerStub := &documentToolProviderStub{
		serviceConfig: &provider.ServiceConfig{ImageFlowID: "image-flow"},
		imageRunResult: &provider.ImageUnderstandingResult{
			SessionID: "session-image-2",
			Output:    `{"catalog":"网页截图","scene":"控制台界面","human":"否","face":"否","summary":"包含浏览器截图","text_as_html":"<p>Hello</p>","text":"Hello"}`,
			MediaType: "application/json",
			Text:      "Hello",
		},
	}
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeChromeSession,
			PermissionMode: internalconfig.BrowserPermissionModeSkipAll,
		},
	}
	_, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, config, map[string]any{
		"flow_id": "chat-flow-1",
		"context": map[string]any{"session_id": "session-browser-1"},
		"server":  map[string]any{"name": "local"},
		"function": map[string]any{
			"name": browserToolScreenshot,
			"arguments": map[string]any{
				"input_value": "请提取图像内容",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected screenshot with image understanding to succeed, got %v", err)
	}
	if len(providerStub.uploadCalls) != 1 {
		t.Fatalf("expected one uploaded screenshot, got %#v", providerStub.uploadCalls)
	}
	resultMap, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	pathValue := strings.TrimSpace(stringValue(resultMap["path"]))
	if pathValue == "" || providerStub.uploadCalls[0].LocalPath != pathValue {
		t.Fatalf("expected uploaded local screenshot path %q, got %#v", pathValue, providerStub.uploadCalls)
	}
	if providerStub.uploadCalls[0].FlowID != "chat-flow-1" || providerStub.uploadCalls[0].SessionID != "session-browser-1" {
		t.Fatalf("expected upload context to reuse current flow/session, got %#v", providerStub.uploadCalls[0])
	}
	if providerStub.imageRunRequest.FlowID != "image-flow" || providerStub.imageRunRequest.SessionID != "session-browser-1" {
		t.Fatalf("unexpected image flow request metadata: %#v", providerStub.imageRunRequest)
	}
	assertScreenshotImageUnderstandingInput(t, providerStub.imageRunRequest, "请提取图像内容", true)
	assertScreenshotImageUnderstandingTweaks(t, providerStub.imageRunRequest)
	assertImageCompressionStrategy(t, providerStub.imageRunRequest, provider.ImageCompressionStrategyPassthrough)
	imageUnderstanding := strings.TrimSpace(stringValue(resultMap["image_understanding"]))
	if imageUnderstanding == "" {
		t.Fatalf("expected image_understanding result, got %#v", resultMap)
	}
	if imageUnderstanding != "Hello" {
		t.Fatalf("unexpected image understanding payload: %#v", resultMap)
	}
	if strings.TrimSpace(stringValue(resultMap["image_understanding_error"])) != "" {
		t.Fatalf("expected no image understanding error, got %#v", resultMap)
	}
}

func TestBrowserScreenshotAddsDefaultImageUnderstandingInputWhenMissing(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	expectedBytes := mustEncodeTestPNG(t, 13, 7)
	browserStub := &browserToolExecutorStub{result: map[string]any{
		"tab_id":       "tab-shot",
		"image_base64": base64.StdEncoding.EncodeToString(expectedBytes),
		"format":       "png",
	}}
	runtimeClient.SetBrowserToolExecutor(browserStub)
	providerStub := &documentToolProviderStub{
		serviceConfig: &provider.ServiceConfig{ImageFlowID: "image-flow"},
		imageRunResult: &provider.ImageUnderstandingResult{
			SessionID: "session-image-2",
			Output:    "Hello",
			MediaType: "text/plain",
			Text:      "Hello",
		},
	}
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		Browser: internalconfig.BrowserConfig{
			Mode:           internalconfig.BrowserModeChromeSession,
			PermissionMode: internalconfig.BrowserPermissionModeSkipAll,
		},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, config, map[string]any{
		"flow_id": "chat-flow-1",
		"context": map[string]any{"session_id": "session-browser-1"},
		"server":  map[string]any{"name": "local"},
		"function": map[string]any{
			"name":      browserToolScreenshot,
			"arguments": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("expected screenshot with default image understanding input to succeed, got %v", err)
	}
	assertScreenshotImageUnderstandingInput(t, providerStub.imageRunRequest, screenshotImageUnderstandingDefaultInput, true)
	assertScreenshotImageUnderstandingTweaks(t, providerStub.imageRunRequest)
	assertImageCompressionStrategy(t, providerStub.imageRunRequest, provider.ImageCompressionStrategyPassthrough)
}

func TestExecuteClientToolPayloadDispatchesComputerTool(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	computerStub := &computerToolExecutorStub{result: map[string]any{"count": 2, "displays": []any{map[string]any{"display_id": "1"}, map[string]any{"display_id": "2"}}}}
	runtimeClient.SetComputerToolExecutor(computerStub)
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "auto",
		Computer: internalconfig.ComputerConfig{
			PermissionMode: internalconfig.ComputerPermissionModeAsk,
		},
	}
	toolName, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name":      computerToolDisplaysContext,
			"arguments": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("expected computer tool execution to succeed, got %v", err)
	}
	if toolName != computerToolDisplaysContext {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if len(computerStub.calls) != 1 || computerStub.calls[0].ToolName != computerToolDisplaysContext {
		t.Fatalf("expected computer executor call, got %#v", computerStub.calls)
	}
	resultMap, ok := resultAny.(map[string]any)
	if !ok || intValue(resultMap["count"], 0) != 2 {
		t.Fatalf("unexpected computer tool result: %#v", resultAny)
	}
}

func TestComputerPermissionModeAskOverridesAuto(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	runtimeClient.SetComputerToolExecutor(&computerToolExecutorStub{})
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "auto",
		Computer: internalconfig.ComputerConfig{
			PermissionMode: internalconfig.ComputerPermissionModeAsk,
		},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": computerToolTypeText,
			"arguments": map[string]any{
				"text": "taiyiflow",
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "用户拒绝执行") {
		t.Fatalf("expected computer ask mode to require confirmation, got %v", err)
	}
}

func TestComputerPermissionModeSkipAllBypassesConfirm(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	computerStub := &computerToolExecutorStub{result: map[string]any{"ok": true}}
	runtimeClient.SetComputerToolExecutor(computerStub)
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "confirm",
		Computer: internalconfig.ComputerConfig{
			PermissionMode: internalconfig.ComputerPermissionModeSkipAll,
		},
	}
	toolName, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": computerToolTypeText,
			"arguments": map[string]any{
				"text": "taiyiflow",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected computer skip_all_permission_checks to bypass confirm, got %v", err)
	}
	if toolName != computerToolTypeText {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if len(computerStub.calls) != 1 || computerStub.calls[0].ToolName != computerToolTypeText {
		t.Fatalf("expected computer executor call, got %#v", computerStub.calls)
	}
}

func TestComputerPermissionModeSkipAllBypassesReadonly(t *testing.T) {
	runtimeClient := NewClientRuntime(strings.NewReader("n\n"), io.Discard, io.Discard)
	computerStub := &computerToolExecutorStub{result: map[string]any{"window_id": "win-1"}}
	runtimeClient.SetComputerToolExecutor(computerStub)
	config := internalconfig.Config{
		Workdir:      t.TempDir(),
		ApprovalMode: "readonly",
		Computer: internalconfig.ComputerConfig{
			PermissionMode: internalconfig.ComputerPermissionModeSkipAll,
		},
	}
	toolName, _, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name": computerToolFocusWindow,
			"arguments": map[string]any{
				"window_id": "win-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected computer permission mode to bypass readonly for desktop tool, got %v", err)
	}
	if toolName != computerToolFocusWindow {
		t.Fatalf("unexpected tool name: %s", toolName)
	}
	if len(computerStub.calls) != 1 || computerStub.calls[0].ToolName != computerToolFocusWindow {
		t.Fatalf("expected computer executor call, got %#v", computerStub.calls)
	}
}

func TestComputerScreenshotAutoPathUsesWorkspaceScopedFile(t *testing.T) {
	tempDir := t.TempDir()
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	computerStub := &computerToolExecutorStub{result: map[string]any{"path": "", "size": 12}}
	runtimeClient.SetComputerToolExecutor(computerStub)
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		Computer: internalconfig.ComputerConfig{
			PermissionMode: internalconfig.ComputerPermissionModeAsk,
		},
	}
	_, resultAny, err := runtimeClient.ExecuteClientToolPayload(context.Background(), config, map[string]any{
		"server": map[string]any{"name": "local"},
		"function": map[string]any{
			"name":      computerToolScreenshot,
			"arguments": map[string]any{},
		},
	})
	if err != nil {
		t.Fatalf("expected screenshot path auto generation to succeed, got %v", err)
	}
	if len(computerStub.calls) != 1 {
		t.Fatalf("expected one computer executor call, got %#v", computerStub.calls)
	}
	pathValue := strings.TrimSpace(stringValue(computerStub.calls[0].Arguments["path"]))
	if pathValue == "" {
		t.Fatal("expected screenshot tool to receive auto-generated path")
	}
	resolvedTempDir, err := resolveWithExistingParent(tempDir)
	if err != nil {
		t.Fatalf("resolve temp dir failed: %v", err)
	}
	if !strings.HasPrefix(pathValue, resolvedTempDir) {
		t.Fatalf("expected screenshot path under resolved workdir %s, got %s", resolvedTempDir, pathValue)
	}
	if !strings.HasSuffix(pathValue, ".png") {
		t.Fatalf("expected png screenshot path, got %s", pathValue)
	}
	resultMap, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %#v", resultAny)
	}
	if got := strings.TrimSpace(stringValue(resultMap["path"])); got != pathValue {
		t.Fatalf("expected normalized screenshot path %s, got %#v", pathValue, resultMap)
	}
}

func TestComputerScreenshotAddsImageUnderstandingWhenConfigured(t *testing.T) {
	tempDir := t.TempDir()
	explicitPath := filepath.Join(tempDir, "computer-shot.png")
	if err := os.WriteFile(explicitPath, []byte("fake-computer-png"), 0o644); err != nil {
		t.Fatalf("write screenshot fixture failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	computerStub := &computerToolExecutorStub{result: map[string]any{"path": "", "content_type": "image/png", "screenshot_width_px": 1440, "screenshot_height_px": 900, "display_index": 2, "display_origin_x": 2560, "display_origin_y": -180, "display_scale_factor": 2.0}}
	runtimeClient.SetComputerToolExecutor(computerStub)
	providerStub := &documentToolProviderStub{
		serviceConfig: &provider.ServiceConfig{ImageFlowID: "image-flow"},
		imageRunResult: &provider.ImageUnderstandingResult{
			SessionID: "session-image-3",
			Output:    `{"catalog":"桌面截图","scene":"桌面应用","human":"否","face":"否","summary":"包含桌面截图","text_as_html":"<p>Status</p>","text":"Status"}`,
			MediaType: "application/json",
			Text:      "Status",
		},
	}
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		Computer: internalconfig.ComputerConfig{
			PermissionMode: internalconfig.ComputerPermissionModeSkipAll,
		},
	}
	_, resultAny, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, config, map[string]any{
		"flow_id": "chat-flow-2",
		"context": map[string]any{"session_id": "session-computer-1"},
		"server":  map[string]any{"name": "local"},
		"function": map[string]any{
			"name": computerToolScreenshot,
			"arguments": map[string]any{
				"path":        explicitPath,
				"input_value": "请提取状态栏内容",
			},
		},
	})
	if err != nil {
		t.Fatalf("expected desktop screenshot with image understanding to succeed, got %v", err)
	}
	resolvedExplicitPath, err := resolveWithExistingParent(explicitPath)
	if err != nil {
		t.Fatalf("resolve explicit screenshot path failed: %v", err)
	}
	if len(providerStub.uploadCalls) != 1 || providerStub.uploadCalls[0].LocalPath != resolvedExplicitPath {
		t.Fatalf("expected uploaded explicit screenshot path, got %#v", providerStub.uploadCalls)
	}
	if providerStub.imageRunRequest.FlowID != "image-flow" {
		t.Fatalf("unexpected image flow request metadata: %#v", providerStub.imageRunRequest)
	}
	assertScreenshotImageUnderstandingInput(t, providerStub.imageRunRequest, "请提取状态栏内容", true)
	assertScreenshotImageUnderstandingTweaks(t, providerStub.imageRunRequest)
	assertImageCompressionStrategy(t, providerStub.imageRunRequest, provider.ImageCompressionStrategyPassthrough)
	resultMap, ok := resultAny.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", resultAny)
	}
	if got := strings.TrimSpace(stringValue(resultMap["path"])); got != resolvedExplicitPath {
		t.Fatalf("expected normalized screenshot path %s, got %#v", resolvedExplicitPath, resultMap)
	}
	imageUnderstanding := strings.TrimSpace(stringValue(resultMap["image_understanding"]))
	if imageUnderstanding == "" {
		t.Fatalf("expected image_understanding result, got %#v", resultMap)
	}
	if imageUnderstanding != "Status" {
		t.Fatalf("unexpected desktop image understanding payload: %#v", resultMap)
	}
}

func TestComputerScreenshotAddsDefaultImageUnderstandingInputWhenMissing(t *testing.T) {
	tempDir := t.TempDir()
	explicitPath := filepath.Join(tempDir, "computer-shot.png")
	if err := os.WriteFile(explicitPath, []byte("fake-computer-png"), 0o644); err != nil {
		t.Fatalf("write screenshot fixture failed: %v", err)
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	computerStub := &computerToolExecutorStub{result: map[string]any{"path": "", "content_type": "image/png", "screenshot_width_px": 1440, "screenshot_height_px": 900, "display_index": 2, "display_origin_x": 2560, "display_origin_y": -180, "display_scale_factor": 2.0}}
	runtimeClient.SetComputerToolExecutor(computerStub)
	providerStub := &documentToolProviderStub{
		serviceConfig: &provider.ServiceConfig{ImageFlowID: "image-flow"},
		imageRunResult: &provider.ImageUnderstandingResult{
			SessionID: "session-image-3",
			Output:    "Status",
			MediaType: "text/plain",
			Text:      "Status",
		},
	}
	config := internalconfig.Config{
		Workdir:      tempDir,
		ApprovalMode: "auto",
		Computer: internalconfig.ComputerConfig{
			PermissionMode: internalconfig.ComputerPermissionModeSkipAll,
		},
	}
	_, _, err := runtimeClient.ExecuteClientToolPayloadWithProvider(context.Background(), providerStub, config, map[string]any{
		"flow_id": "chat-flow-2",
		"context": map[string]any{"session_id": "session-computer-1"},
		"server":  map[string]any{"name": "local"},
		"function": map[string]any{
			"name": computerToolScreenshot,
			"arguments": map[string]any{
				"path": explicitPath,
			},
		},
	})
	if err != nil {
		t.Fatalf("expected desktop screenshot with default image understanding input to succeed, got %v", err)
	}
	assertScreenshotImageUnderstandingInput(t, providerStub.imageRunRequest, screenshotImageUnderstandingDefaultInput, true)
	assertScreenshotImageUnderstandingTweaks(t, providerStub.imageRunRequest)
	assertImageCompressionStrategy(t, providerStub.imageRunRequest, provider.ImageCompressionStrategyPassthrough)
}
