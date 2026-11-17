package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/agentloop"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/skills"
)

const (
	localToolServerName = "local"
)

// MCPRegistry 是外部 MCP 服务器注册表接口。
// 由 internal/mcp.Registry 实现，用于将后端配置的 MCP 服务器工具
// 合并到 client_mcp_servers 请求中，并路由非 local 的工具调用。
type MCPRegistry interface {
	// ClientMCPServers 返回 { serverName: [{name, description, input_schema}] } 格式的工具定义。
	ClientMCPServers() map[string]any
	// CallTool 路由工具调用到对应的 MCP 服务器。
	CallTool(ctx context.Context, serverName, toolName string, toolArgs map[string]any) (any, error)
	// HasServer 检查是否存在指定名称的 MCP 服务器。
	HasServer(serverName string) bool
}

// DetachedCallbackContext 创建一个脱离父上下文生命周期的独立上下文，
// 仅保留父上下文中携带的 Value（如认证信息），不受父上下文的取消或超时影响。
// 用于回调结果提交、协同任务回报等关键操作，确保即使流处理已结束或 WebSocket 断连，
// 结果仍能通过 HTTP 提交到服务端。timeout 为超时时间，若 <= 0 则使用系统默认 HTTP 超时配置。
// 调用方必须在使用完毕后调用返回的 cancel 函数。
func DetachedCallbackContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx := context.WithoutCancel(parent)
	if timeout <= 0 {
		timeout = time.Duration(internalconfig.DefaultHTTPTimeoutSeconds) * time.Second
	}
	return context.WithTimeout(ctx, timeout)
}

var windowsShellBuiltins = map[string]struct{}{
	"assoc": {}, "break": {}, "call": {}, "cd": {}, "chdir": {}, "cls": {}, "color": {},
	"copy": {}, "date": {}, "del": {}, "dir": {}, "echo": {}, "endlocal": {}, "erase": {},
	"exit": {}, "for": {}, "ftype": {}, "goto": {}, "if": {}, "md": {}, "mkdir": {},
	"mklink": {}, "move": {}, "path": {}, "pause": {}, "popd": {}, "prompt": {},
	"pushd": {}, "rd": {}, "ren": {}, "rename": {}, "rmdir": {}, "set": {},
	"setlocal": {}, "shift": {}, "start": {}, "time": {}, "title": {}, "type": {},
	"ver": {}, "verify": {}, "vol": {},
}

var posixShellOnlyBuiltins = map[string]struct{}{
	"alias": {}, "bg": {}, "bind": {}, "builtin": {}, "cd": {}, "command": {},
	"compgen": {}, "complete": {}, "declare": {}, "dirs": {}, "disown": {}, "enable": {},
	"eval": {}, "exec": {}, "export": {}, "fc": {}, "fg": {}, "getopts": {}, "hash": {},
	"help": {}, "history": {}, "jobs": {}, "local": {}, "logout": {}, "popd": {},
	"pushd": {}, "read": {}, "readonly": {}, "return": {}, "set": {}, "shift": {},
	"shopt": {}, "source": {}, "suspend": {}, "times": {}, "trap": {}, "typeset": {},
	"ulimit": {}, "umask": {}, "unalias": {}, "unset": {}, "wait": {},
}

type AskUserOption struct {
	Label string
	Value string
}

type AskUserQuestion struct {
	Name        string
	Label       string
	InputFormat string
	OptionsList []AskUserOption
	MultiSelect bool
}

type AskUserRequest struct {
	Questions []AskUserQuestion
	// TimeoutSeconds 为 ask_user 弹窗倒计时秒数；<=0 表示不启用倒计时
	TimeoutSeconds int
}

type AskUserResponse struct {
	Answers map[string]any
}

type AskUserPrompter func(ctx context.Context, request AskUserRequest) (AskUserResponse, error)

type ClientRuntime struct {
	stdin                       io.Reader
	stdout                      io.Writer
	stderr                      io.Writer
	askUserPrompter             AskUserPrompter
	browserToolExecutor         BrowserToolExecutor
	computerToolExecutor        ComputerToolExecutor
	sandboxEscapeConfirmer      func(path string) (bool, error)
	sandboxEscapePolicyOverride string
	// toolConfirmTimeoutSeconds 为 stdio 模式下确认弹窗的超时秒数；<=0 表示不启用
	toolConfirmTimeoutSeconds int
	streamEventLogsEnabled    bool
	interactivePromptsEnabled bool
	hookHandlers              []HookHandler
	// streamCallback 非 nil 时，runCommand 会将 stdout/stderr 实时转发给回调，
	// 同时跳过 [执行客户端命令] 等事件日志头，适用于 TUI 流式输出场景。
	streamCallback func(text string, isError bool)
	// callbackRetryDelay 控制回调提交失败后的重试间隔。
	// 默认 3 秒，测试中可通过 SetCallbackRetryDelay 缩短。
	callbackRetryDelay time.Duration
	// callbackMaxRetries 控制回调提交失败后的最大重试次数，默认 3。
	callbackMaxRetries int
	// 子任务能力继承字段：由 cmd 层在发起 Stream 前注入，子任务执行时复用。
	subtaskSkillPackages       any
	subtaskRequestedSkillNames []string
	subtaskReasoningEnabled    bool
	subtaskInternetSearch      bool
	subtaskImageGeneration     bool
	subtaskCrossSessionMemory  *bool
	subtaskKnowledgeScopes     []string
	subtaskKnowledgeDocs       []string
	// subtaskClientMCPServerNames 记录发布端使用的 MCP server 名称列表。
	// 跨客户端协同时由 features payload 注入，执行者端据此过滤本地 MCP servers。
	subtaskClientMCPServerNames []string
	// subtaskEnabled 控制是否向模型暴露 run_subtask 工具。
	// 非交互式模式默认 false，交互式模式和 TUI 默认 true。
	subtaskEnabled bool
	// maxParallelismSubtask 控制子智能体的最大并发数。
	// 由 cmd 层从本地配置注入，用于工具定义的 maximum 约束和实际执行时的并发上限。
	maxParallelismSubtask int
	// pendingModeSwitch 存储 switch_to_agent 工具执行后待消费的模式切换请求。
	// cmd 层在 HandleClientToolEvent 返回后通过 ConsumePendingModeSwitch 读取并更新会话状态。
	// 非线程安全：仅限单 goroutine 事件循环内写入和消费（switchToAgent 写入、ConsumePendingModeSwitch 读取）。
	// 如果未来引入并发事件处理，需改为 sync.Mutex 或 atomic 保护。
	pendingModeSwitch string
	// currentRequestMode 记录当前请求模式（ask/plan/agent），由 cmd 层在发起 Stream 前注入。
	// additionalAgentToolDescriptions 使用此字段确定基准模式，计算 agent 模式相比当前模式新增的工具。
	currentRequestMode string
	// nonArtifactFiles 追踪 is_artifact=false 的文件路径，在 on_complete 时自动清理。
	nonArtifactFiles []string
	nonArtifactMu    sync.Mutex
	// guidanceText 存储用户执行引导文本，在 submitCallback 中前置到回调结果的 Result 字段中，使用后清空。
	guidanceText string
	// guidanceCh 是从 TUI 接收用户执行引导文本的 channel。
	// 在 submitCallback 中排空，确保工具执行期间用户输入的引导能在回调提交前被消费。
	guidanceCh chan string
	// mcpRegistry 是外部 MCP 服务器注册表，用于将后端配置的 MCP 服务器工具
	// 合并到 client_mcp_servers 请求中，并路由非 local 的工具调用。
	// 与 taiyiflow-chatui 的 mcp-store + mcpClient 功能对齐。
	mcpRegistry    MCPRegistry
	agentLoopStore *agentloop.Store
}

func NewClientRuntime(stdin io.Reader, stdout io.Writer, stderr io.Writer) *ClientRuntime {
	// maxParallelismSubtask 显式初始化为默认值：0 是"禁用子智能体"的合法配置值，
	// 不能让 Go 零值与显式禁用语义混淆。cmd 层随后会用本地配置覆盖该值。
	r := &ClientRuntime{stdin: stdin, stdout: stdout, stderr: stderr, browserToolExecutor: defaultBrowserToolExecutor{}, computerToolExecutor: defaultComputerToolExecutor{}, streamEventLogsEnabled: true, interactivePromptsEnabled: true, maxParallelismSubtask: internalconfig.DefaultMaxParallelismSubtask, hookHandlers: []HookHandler{auditHookHandler}}
	r.RegisterHookHandler(r.nonArtifactCleanupHandler)
	return r
}

func (r *ClientRuntime) SetSandboxEscapeConfirmer(confirmer func(path string) (bool, error)) {
	r.sandboxEscapeConfirmer = confirmer
}

func (r *ClientRuntime) SetAskUserPrompter(prompter AskUserPrompter) {
	r.askUserPrompter = prompter
}

func (r *ClientRuntime) SetSandboxEscapePolicyOverride(policy string) {
	r.sandboxEscapePolicyOverride = strings.TrimSpace(policy)
}

func (r *ClientRuntime) SetToolConfirmTimeoutSeconds(seconds int) {
	r.toolConfirmTimeoutSeconds = seconds
}

// SetMCPRegistry 注入外部 MCP 服务器注册表。
// 注入后，ClientMCPServersForMode 会将注册表中的工具定义合并到 client_mcp_servers，
// executeClientToolPayload 会将非 local 服务器的工具调用路由到注册表。
func (r *ClientRuntime) SetMCPRegistry(registry MCPRegistry) {
	r.mcpRegistry = registry
}

func (r *ClientRuntime) SetAgentLoopStore(store *agentloop.Store) {
	r.agentLoopStore = store
}

// ReadStdinWithTimeout 从 stdin 读取一行，超时后返回空字符串和 nil error。
// timeoutSeconds <= 0 表示不启用超时。公开方法，供 cmd 层复用。
func (r *ClientRuntime) ReadStdinWithTimeout() (string, error) {
	return r.readStdinWithTimeout(r.toolConfirmTimeoutSeconds)
}

func (r *ClientRuntime) SetStreamEventLogsEnabled(enabled bool) {
	r.streamEventLogsEnabled = enabled
}

func (r *ClientRuntime) SetStreamCallback(cb func(text string, isError bool)) {
	r.streamCallback = cb
}

// SetGuidanceText 设置用户执行引导文本。
// 该文本会在下一次 submitCallback 调用时前置到 Result 字段中，然后自动清空。
// 用于在工具执行期间将用户通过 shift+enter 输入的引导文本注入到工具回调结果中。
func (r *ClientRuntime) SetGuidanceText(text string) {
	if r == nil {
		return
	}
	r.guidanceText = strings.TrimSpace(text)
}

// SetGuidanceChannel 设置用户执行引导文本的 channel。
// submitCallback 会在提交回调前排空此 channel，确保工具执行期间用户输入的引导能被及时消费。
func (r *ClientRuntime) SetGuidanceChannel(ch chan string) {
	if r == nil {
		return
	}
	r.guidanceCh = ch
}

func (r *ClientRuntime) SetInteractivePromptsEnabled(enabled bool) {
	r.interactivePromptsEnabled = enabled
}

// SetCallbackRetryDelay 设置回调提交失败后的重试间隔和最大重试次数。
// 默认行为：重试 3 次，每次间隔 3 秒。测试中可缩短延迟避免等待。
func (r *ClientRuntime) SetCallbackRetryDelay(delay time.Duration, maxRetries int) {
	r.callbackRetryDelay = delay
	r.callbackMaxRetries = maxRetries
}

func (r *ClientRuntime) InteractivePromptsEnabled() bool {
	if r == nil {
		return false
	}
	return r.interactivePromptsEnabled
}

// SetSubtaskEnabled 控制是否向模型暴露 run_subtask 工具。
// 非交互式模式默认 false，交互式模式和 TUI 默认 true。
func (r *ClientRuntime) SetSubtaskEnabled(enabled bool) {
	if r == nil {
		return
	}
	r.subtaskEnabled = enabled
}

// SetMaxParallelismSubtask 设置子智能体的最大并发数。
// 由 cmd 层从本地配置注入，用于工具定义的 maximum 约束和实际执行时的并发上限。
func (r *ClientRuntime) SetMaxParallelismSubtask(maxParallelism int) {
	if r == nil {
		return
	}
	r.maxParallelismSubtask = maxParallelism
}

// MaxParallelismSubtask 返回子智能体的最大并发数。
// 返回 0 表示禁用子智能体（不暴露 run_subtask 工具）。
func (r *ClientRuntime) MaxParallelismSubtask() int {
	if r == nil || r.maxParallelismSubtask < 0 {
		return internalconfig.DefaultMaxParallelismSubtask
	}
	return r.maxParallelismSubtask
}

func (r *ClientRuntime) SubtaskEnabled() bool {
	if r == nil {
		return false
	}
	return r.subtaskEnabled
}

// ConsumePendingModeSwitch 读取并清除 switch_to_agent 工具设置的模式切换请求。
// 返回非空字符串表示需要将会话模式切换到该值；返回空字符串表示无待处理的切换。
func (r *ClientRuntime) ConsumePendingModeSwitch() string {
	if r == nil {
		return ""
	}
	mode := r.pendingModeSwitch
	r.pendingModeSwitch = ""
	return mode
}

// SetCurrentRequestMode 注入当前请求模式（ask/plan/agent）。
// 由 cmd 层在发起 Stream 前调用，用于 additionalAgentToolDescriptions 计算基准模式。
func (r *ClientRuntime) SetCurrentRequestMode(mode string) {
	r.currentRequestMode = provider.NormalizeRequestType(mode)
}

// SetSubtaskCapabilities 注入子任务执行时需要继承的能力字段。
// 由 cmd 层在发起 Stream 前调用，确保子任务拥有与主任务相同的 skills、MCP、reasoning、联网搜索、知识库等能力。
func (r *ClientRuntime) SetSubtaskCapabilities(skillPackages any, requestedSkillNames []string, reasoningEnabled bool, internetSearch bool, imageGeneration bool, crossSessionMemory *bool, knowledgeScopes []string, knowledgeDocs []string) {
	r.subtaskSkillPackages = skillPackages
	r.subtaskRequestedSkillNames = requestedSkillNames
	r.subtaskReasoningEnabled = reasoningEnabled
	r.subtaskInternetSearch = internetSearch
	r.subtaskImageGeneration = imageGeneration
	r.subtaskCrossSessionMemory = crossSessionMemory
	r.subtaskKnowledgeScopes = knowledgeScopes
	r.subtaskKnowledgeDocs = knowledgeDocs
}

func (r *ClientRuntime) cloneRuntime() *ClientRuntime {
	if r == nil {
		return NewClientRuntime(strings.NewReader(""), io.Discard, io.Discard)
	}
	return &ClientRuntime{
		stdin:                       r.stdin,
		stdout:                      r.stdout,
		stderr:                      r.stderr,
		askUserPrompter:             r.askUserPrompter,
		browserToolExecutor:         r.browserToolExecutor,
		computerToolExecutor:        r.computerToolExecutor,
		sandboxEscapeConfirmer:      r.sandboxEscapeConfirmer,
		sandboxEscapePolicyOverride: r.sandboxEscapePolicyOverride,
		toolConfirmTimeoutSeconds:   r.toolConfirmTimeoutSeconds,
		streamEventLogsEnabled:      r.streamEventLogsEnabled,
		interactivePromptsEnabled:   r.interactivePromptsEnabled,
		hookHandlers:                append([]HookHandler(nil), r.hookHandlers...),
		streamCallback:              r.streamCallback,
		subtaskSkillPackages:        r.subtaskSkillPackages,
		subtaskRequestedSkillNames:  append([]string(nil), r.subtaskRequestedSkillNames...),
		subtaskReasoningEnabled:     r.subtaskReasoningEnabled,
		subtaskInternetSearch:       r.subtaskInternetSearch,
		subtaskImageGeneration:      r.subtaskImageGeneration,
		subtaskCrossSessionMemory:   r.subtaskCrossSessionMemory,
		subtaskKnowledgeScopes:      append([]string(nil), r.subtaskKnowledgeScopes...),
		subtaskKnowledgeDocs:        append([]string(nil), r.subtaskKnowledgeDocs...),
		subtaskClientMCPServerNames: append([]string(nil), r.subtaskClientMCPServerNames...),
		subtaskEnabled:              r.subtaskEnabled,
		maxParallelismSubtask:       r.maxParallelismSubtask,
		// pendingModeSwitch 被有意忽略：克隆运行时不应继承父运行时的模式切换请求，
		// 模式切换仅由主运行时上的 switch_to_agent 工具触发，由 cmd 层消费主运行时的 pendingModeSwitch。
		currentRequestMode: r.currentRequestMode,
		nonArtifactFiles:   append([]string(nil), r.nonArtifactFiles...),
		agentLoopStore:     r.agentLoopStore,
	}
}

func (r *ClientRuntime) writeStreamEventf(writer io.Writer, format string, args ...any) {
	if !r.streamEventLogsEnabled {
		return
	}
	fmt.Fprintf(writer, format, args...)
}

func (r *ClientRuntime) ClientMCPServers() map[string]any {
	return r.ClientMCPServersForMode(provider.RequestTypeAgent, internalconfig.Default())
}

func (r *ClientRuntime) ClientMCPServersForMode(mode string, config internalconfig.Config) map[string]any {
	definitions := r.clientToolDefinitionsForMode(mode)
	hideBrowserTools := !internalconfig.BrowserEnabled(config.Browser)
	hideComputerTools := !internalconfig.ComputerEnabled(config.Computer)
	if hideBrowserTools || hideComputerTools {
		filtered := definitions[:0]
		for _, definition := range definitions {
			name := strings.TrimSpace(stringValue(definition["name"]))
			if hideBrowserTools && isBrowserTool(name) {
				continue
			}
			if hideComputerTools && isComputerTool(name) {
				continue
			}
			filtered = append(filtered, definition)
		}
		definitions = filtered
	}
	if !hideComputerTools {
		status := sharedComputerToolManager.Diagnose(config.Computer)
		definitions = filterComputerToolDefinitionsByCapabilities(definitions, status.Capabilities)
	}
	result := map[string]any{
		localToolServerName: definitions,
	}
	// 合并外部 MCP 服务器工具定义（与 taiyiflow-chatui 的 getFilteredToolDefinitions 对齐）
	if r.mcpRegistry != nil {
		for serverName, tools := range r.mcpRegistry.ClientMCPServers() {
			if serverName == localToolServerName {
				continue
			}
			result[serverName] = tools
		}
	}
	return result
}

func (r *ClientRuntime) HandleBuiltInEvent(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
) (returnErr error) {
	serverName := nestedString(payload, "server", "name")
	commandName := nestedString(payload, "command", "name")
	arguments := nestedMap(payload, "command", "arguments")
	preview := commandPreview(arguments)
	r.emitHook(config, buildPreToolHookEvent("built_in", commandName, preview, payload, nil))
	var toolResult any
	var toolErr error
	defer func() {
		r.emitHook(config, buildPostToolHookEvent("built_in", commandName, preview, payload, toolErr == nil, toolErr, toolResult, nil))
	}()
	if serverName != "built_in_command" || commandName != "run_command_via_client" {
		toolErr = fmt.Errorf("不支持的内置调用: %s/%s", serverName, commandName)
		returnErr = r.submitCallback(ctx, providerClient, config, payload, false, nil, toolErr.Error())
		return returnErr
	}
	if err := r.checkAgentLoopToolDispatch(payload); err != nil {
		toolErr = err
		returnErr = r.submitCallback(ctx, providerClient, config, payload, false, nil, err.Error())
		return returnErr
	}

	policy := nestedMap(payload, "command", "policy")
	if policyConfirmationRequired(policy) {
		approved, err := r.confirmCommand(arguments, config)
		if err != nil {
			toolErr = err
			returnErr = r.submitCallback(ctx, providerClient, config, payload, false, nil, err.Error())
			return returnErr
		}
		if !approved {
			toolErr = fmt.Errorf("用户拒绝执行客户端命令")
			returnErr = r.submitCallback(ctx, providerClient, config, payload, false, nil, "用户拒绝执行客户端命令。")
			return returnErr
		}
	}

	toolResult, toolErr = r.runCommand(ctx, config, arguments)
	if toolErr != nil {
		returnErr = r.submitCallback(ctx, providerClient, config, payload, false, nil, toolErr.Error())
		return returnErr
	}
	returnErr = r.submitCallback(ctx, providerClient, config, payload, true, toolResult, "")
	return returnErr
}

func (r *ClientRuntime) HandleClientToolEvent(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
) error {
	_, result, err := r.ExecuteClientToolPayloadWithProvider(ctx, providerClient, config, payload)
	return r.SubmitClientToolCallback(ctx, providerClient, config, payload, result, err)
}

func (r *ClientRuntime) ExecuteClientToolPayload(
	ctx context.Context,
	config internalconfig.Config,
	payload map[string]any,
) (string, any, error) {
	return r.executeClientToolPayload(ctx, nil, config, payload)
}

func (r *ClientRuntime) ExecuteClientToolPayloadWithProvider(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
) (string, any, error) {
	return r.executeClientToolPayload(ctx, providerClient, config, payload)
}

func (r *ClientRuntime) executeClientToolPayload(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
) (toolName string, result any, err error) {
	if err := r.checkAgentLoopToolDispatch(payload); err != nil {
		return canonicalClientToolName(nestedString(payload, "function", "name")), nil, err
	}
	serverName := nestedString(payload, "server", "name")
	toolName = canonicalClientToolName(nestedString(payload, "function", "name"))
	arguments := nestedMap(payload, "function", "arguments")
	arguments = enrichToolArgumentsWithContext(arguments, payload)

	if serverName != localToolServerName {
		// 非 local 服务器：路由到外部 MCP 服务器注册表（与 chatui toolHandlerMappings 对齐）
		if r.mcpRegistry != nil && r.mcpRegistry.HasServer(serverName) {
			preview := commandPreview(arguments)
			r.emitHook(config, buildPreToolHookEvent("mcp_server", toolName, preview, payload, arguments))
			result, err = r.mcpRegistry.CallTool(ctx, serverName, toolName, arguments)
			r.emitHook(config, buildPostToolHookEvent("mcp_server", toolName, preview, payload, err == nil, err, result, arguments))
			return toolName, result, err
		}
		return toolName, nil, fmt.Errorf("不支持的客户端工具服务: %s", serverName)
	}
	preview := previewClientTool(toolName, arguments)
	r.emitHook(config, buildPreToolHookEvent("client_tool", toolName, preview, payload, arguments))
	defer func() {
		r.emitHook(config, buildPostToolHookEvent("client_tool", toolName, preview, payload, err == nil, err, result, arguments))
	}()
	if err = r.authorizeClientTool(toolName, arguments, config); err != nil {
		return toolName, nil, err
	}

	switch toolName {
	case "ask_user":
		result, err = r.askUser(ctx, arguments)
	case "lsp_query":
		result, err = r.lspQuery(config, arguments)
	case "glob_files":
		result, err = r.globFiles(config, arguments)
	case "todo_write":
		result, err = r.todoWrite(config, payload, arguments)
	case "file_edit":
		result, err = r.fileEdit(config, arguments)
	case "shell_exec":
		result, err = r.shellExec(ctx, config, arguments)
	case "task_output":
		result, err = r.taskOutput(config, arguments)
	case "task_stop":
		result, err = r.taskStop(config, arguments)
	case "task_delete":
		result, err = r.taskDelete(config, arguments)
	case "task_cleanup":
		result, err = r.taskCleanup(config, arguments)
	case "task_list":
		result, err = r.taskList(config)
	case "terminal_capture":
		result, err = r.terminalCapture(config, arguments)
	case "run_subtask":
		result, err = r.runSubtask(ctx, providerClient, config, payload, arguments)
	case "search_files":
		result, err = r.searchFiles(ctx, config, arguments)
	case "read_file":
		result, err = r.readFile(config, arguments)
	case "image_understanding":
		result, err = r.imageUnderstanding(ctx, providerClient, config, arguments)
	case "document_unstructured":
		result, err = r.documentUnstructured(ctx, providerClient, config, arguments)
	case "write_file":
		result, err = r.writeFile(config, arguments)
	case "skill_fetch":
		result, err = r.skill(ctx, providerClient, config, arguments)
	case "skill_create":
		result, err = r.skillCreate(config, arguments)
	case "skill_upload":
		result, err = r.skillUpload(ctx, providerClient, config, arguments)
	case "skill_delete":
		result, err = r.skillDelete(config, arguments)
	case "switch_to_agent":
		result, err = r.switchToAgent(arguments)
	case "get_sso_token":
		result, err = r.ssoToken(config)
	default:
		if isScheduleTool(toolName) {
			result, err = r.scheduleTool(ctx, providerClient, config, toolName, arguments)
		} else if isMcpManageTool(toolName) {
			result, err = r.mcpManageTool(ctx, providerClient, config, toolName, arguments)
		} else if isBrowserTool(toolName) {
			result, err = r.browserTool(ctx, providerClient, config, toolName, arguments)
		} else if isComputerTool(toolName) {
			result, err = r.computerTool(ctx, providerClient, config, toolName, arguments)
		} else {
			err = fmt.Errorf("不支持的客户端工具: %s", toolName)
		}
	}
	return toolName, result, err
}

func enrichToolArgumentsWithContext(arguments map[string]any, payload map[string]any) map[string]any {
	if len(arguments) == 0 {
		arguments = map[string]any{}
	}
	cloned := map[string]any{}
	for key, value := range arguments {
		cloned[key] = value
	}
	if strings.TrimSpace(stringValue(cloned["flow_id"])) == "" {
		if flowID := strings.TrimSpace(stringValue(payload["flow_id"])); flowID != "" {
			cloned["flow_id"] = flowID
		}
	}
	if strings.TrimSpace(stringValue(cloned["session_id"])) == "" {
		if sessionID := strings.TrimSpace(nestedString(payload, "context", "session_id")); sessionID != "" {
			cloned["session_id"] = sessionID
		}
	}
	if strings.TrimSpace(stringValue(cloned["run_id"])) == "" {
		if runID := strings.TrimSpace(nestedString(payload, "context", "run_id")); runID != "" {
			cloned["run_id"] = runID
		}
	}
	if strings.TrimSpace(argumentString(cloned, "workingDirectory", "working_directory")) == "" {
		clientContextDir := strings.TrimSpace(stringValue(payload["client_context_dir"]))
		if clientContextDir == "" {
			clientContextDir = strings.TrimSpace(nestedString(payload, "context", "client_context_dir"))
		}
		if clientContextDir == "" {
			clientContextDir = strings.TrimSpace(nestedString(payload, "context", "context_dir"))
		}
		if clientContextDir != "" {
			cloned["working_directory"] = clientContextDir
		}
	}
	if strings.TrimSpace(argumentString(cloned, "source_skill", "sourceSkill")) == "" {
		if sourceSkill := strings.TrimSpace(stringValue(payload["source_skill"])); sourceSkill != "" {
			cloned["source_skill"] = sourceSkill
		} else if sourceSkill := strings.TrimSpace(nestedString(payload, "context", "source_skill")); sourceSkill != "" {
			cloned["source_skill"] = sourceSkill
		} else if sourceSkill := strings.TrimSpace(nestedString(payload, "context", "sourceSkill")); sourceSkill != "" {
			cloned["source_skill"] = sourceSkill
		}
	}
	return cloned
}

func (r *ClientRuntime) checkAgentLoopToolDispatch(payload map[string]any) error {
	if r.agentLoopStore == nil {
		return nil
	}
	sessionID := resolveContextString(payload, "session_id")
	runID := resolveContextString(payload, "run_id")
	if sessionID == "" {
		// 载荷无法解析出会话标识时沿用历史宽松语义，交由服务端身份校验兜底；
		// 真正的状态存储错误（损坏/版本不符）仍在下方 fail-closed。
		return nil
	}
	state, err := r.agentLoopStore.LoadRecovery(sessionID)
	if err != nil {
		return fmt.Errorf("无法确认 AgentLoop 用户控制状态，拒绝派发工具: %w", err)
	}
	if state.FlowID == "" && state.RunID == "" {
		return nil
	}
	if state.RunID == "" || state.RunID != runID {
		return fmt.Errorf("AgentLoop 执行身份已变化，拒绝派发旧工具调用")
	}
	if state.Intent == agentloop.IntentStopped || state.Intent == agentloop.IntentUserPause {
		return fmt.Errorf("用户已暂停或停止当前执行，拒绝派发新的本地工具")
	}
	return nil
}

func (r *ClientRuntime) SubmitClientToolCallback(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
	result any,
	err error,
) error {
	if err != nil {
		return r.submitCallback(ctx, providerClient, config, payload, false, result, err.Error())
	}
	return r.submitCallback(ctx, providerClient, config, payload, true, result, "")
}

func (r *ClientRuntime) ExecuteShellCommand(
	ctx context.Context,
	config internalconfig.Config,
	rawCommand string,
) (map[string]any, error) {
	if strings.TrimSpace(rawCommand) == "" {
		return nil, fmt.Errorf("! 用法: !<shell 命令>")
	}
	if strings.TrimSpace(config.ApprovalMode) == "readonly" {
		return nil, fmt.Errorf("当前审批模式为只读，不能执行本地命令")
	}
	return r.runCommand(ctx, config, map[string]any{"raw_command": strings.TrimSpace(rawCommand)})
}

func (r *ClientRuntime) askUser(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	request, err := parseAskUserRequest(arguments)
	if err != nil {
		return nil, err
	}
	response, err := r.promptAskUser(ctx, request)
	if err != nil {
		return nil, err
	}
	if response.Answers == nil {
		return map[string]any{}, nil
	}
	return response.Answers, nil
}

func parseAskUserRequest(arguments map[string]any) (AskUserRequest, error) {
	rawQuestions, ok := arguments["questions"]
	if !ok {
		return AskUserRequest{}, newToolError("PARAM_ERROR", "ask_user 缺少 questions")
	}
	items, ok := rawQuestions.([]any)
	if !ok || len(items) == 0 {
		return AskUserRequest{}, newToolError("PARAM_ERROR", "ask_user 至少需要一个问题")
	}
	questions := make([]AskUserQuestion, 0, len(items))
	seenNames := map[string]bool{}
	for index, item := range items {
		questionMap, ok := item.(map[string]any)
		if !ok {
			return AskUserRequest{}, newToolError("PARAM_ERROR", "ask_user questions[%d] 不是有效对象", index)
		}
		name := strings.TrimSpace(stringValue(questionMap["name"]))
		if name == "" {
			name = strings.TrimSpace(stringValue(questionMap["key"]))
		}
		if name == "" {
			return AskUserRequest{}, newToolError("PARAM_ERROR", "ask_user questions[%d] 缺少 name", index)
		}
		nameKey := strings.ToLower(name)
		if seenNames[nameKey] {
			return AskUserRequest{}, newToolError("PARAM_ERROR", "ask_user questions 中存在重复 name: %s", name)
		}
		seenNames[nameKey] = true
		label := strings.TrimSpace(stringValue(questionMap["label"]))
		if label == "" {
			label = name
		}
		options := parseAskUserOptions(questionMap["options_list"])
		inputFormat := normalizeAskUserInputFormat(stringValue(questionMap["input_format"]), len(options) > 0)
		multiSelect := boolValue(questionMap["multi_select"])
		if inputFormat == "select" && len(options) == 0 {
			return AskUserRequest{}, newToolError("PARAM_ERROR", "ask_user questions[%d] 使用 select 时必须提供 options_list", index)
		}
		if inputFormat == "text" {
			multiSelect = false
		}
		questions = append(questions, AskUserQuestion{
			Name:        name,
			Label:       label,
			InputFormat: inputFormat,
			OptionsList: options,
			MultiSelect: multiSelect,
		})
	}
	return AskUserRequest{Questions: questions}, nil
}

func parseAskUserOptions(value any) []AskUserOption {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	options := make([]AskUserOption, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed == "" {
				continue
			}
			options = append(options, AskUserOption{Label: trimmed, Value: trimmed})
		case map[string]any:
			label := strings.TrimSpace(stringValue(typed["label"]))
			value := strings.TrimSpace(stringValue(typed["value"]))
			if label == "" {
				label = strings.TrimSpace(stringValue(typed["name"]))
			}
			if value == "" {
				value = label
			}
			if label == "" || value == "" {
				continue
			}
			options = append(options, AskUserOption{Label: label, Value: value})
		}
	}
	return options
}

func normalizeAskUserInputFormat(value string, hasOptions bool) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "text", "input", "string":
		if hasOptions {
			return "select"
		}
		return "text"
	case "select", "single_select", "single-choice", "choice", "radio", "options":
		return "select"
	default:
		if hasOptions {
			return "select"
		}
		return "text"
	}
}

func (r *ClientRuntime) promptAskUser(ctx context.Context, request AskUserRequest) (AskUserResponse, error) {
	if !r.interactivePromptsEnabled {
		return AskUserResponse{}, newToolError("PERMISSION_DENIED", "当前 task 模式不支持 ask_user 交互，请改为在工具参数中提供明确输入")
	}
	if r.askUserPrompter != nil {
		return r.askUserPrompter(ctx, request)
	}
	return r.promptAskUserStdio(ctx, request)
}

func (r *ClientRuntime) promptAskUserStdio(ctx context.Context, request AskUserRequest) (AskUserResponse, error) {
	reader := bufio.NewReader(r.stdin)
	answers := make(map[string]any, len(request.Questions))
	if len(request.Questions) > 0 {
		fmt.Fprintln(r.stdout, "\n[ask_user] 请输入以下信息；输入 /cancel 可取消。")
	}
	for index, question := range request.Questions {
		select {
		case <-ctx.Done():
			return AskUserResponse{}, ctx.Err()
		default:
		}
		if index > 0 {
			fmt.Fprintln(r.stdout)
		}
		answer, err := r.promptAskUserQuestion(ctx, reader, index, question)
		if err != nil {
			return AskUserResponse{}, err
		}
		answers[question.Name] = answer
	}
	return AskUserResponse{Answers: answers}, nil
}

func (r *ClientRuntime) promptAskUserQuestion(ctx context.Context, reader *bufio.Reader, index int, question AskUserQuestion) (any, error) {
	fmt.Fprintf(r.stdout, "%d. %s\n", index+1, strings.TrimSpace(question.Label))
	if question.InputFormat != "select" {
		return r.promptAskUserText(ctx, reader)
	}
	for optionIndex, option := range question.OptionsList {
		fmt.Fprintf(r.stdout, "  %d) %s\n", optionIndex+1, option.Label)
	}
	if question.MultiSelect {
		return r.promptAskUserMultiSelect(ctx, reader, question.OptionsList)
	}
	return r.promptAskUserSingleSelect(ctx, reader, question.OptionsList)
}

func (r *ClientRuntime) promptAskUserText(ctx context.Context, reader *bufio.Reader) (string, error) {
	return r.promptAskUserLine(ctx, reader, "请输入答案: ")
}

func (r *ClientRuntime) promptAskUserSingleSelect(ctx context.Context, reader *bufio.Reader, options []AskUserOption) (string, error) {
	for {
		answer, err := r.promptAskUserLine(ctx, reader, "请选择序号或候选值: ")
		if err != nil {
			return "", err
		}
		matched, ok := matchAskUserOption(answer, options)
		if ok {
			return matched.Value, nil
		}
		fmt.Fprintln(r.stdout, "输入无效，请输入序号或候选值。")
	}
}

func (r *ClientRuntime) promptAskUserMultiSelect(ctx context.Context, reader *bufio.Reader, options []AskUserOption) ([]string, error) {
	for {
		answer, err := r.promptAskUserLine(ctx, reader, "请选择一个或多个值，多个项用逗号分隔: ")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(answer) == "" {
			return []string{}, nil
		}
		parts := splitAskUserAnswers(answer)
		values := make([]string, 0, len(parts))
		seen := map[string]bool{}
		valid := true
		for _, part := range parts {
			matched, ok := matchAskUserOption(part, options)
			if !ok {
				valid = false
				break
			}
			if !seen[matched.Value] {
				seen[matched.Value] = true
				values = append(values, matched.Value)
			}
		}
		if valid {
			return values, nil
		}
		fmt.Fprintln(r.stdout, "输入无效，请输入逗号分隔的序号或候选值。")
	}
}

func (r *ClientRuntime) promptAskUserLine(ctx context.Context, reader *bufio.Reader, prompt string) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	fmt.Fprint(r.stdout, prompt)
	var answerLine string
	var err error
	if r.toolConfirmTimeoutSeconds > 0 {
		// 启用超时时使用 readStdinWithTimeout（内部创建独立 reader）
		answerLine, err = r.readStdinWithTimeout(r.toolConfirmTimeoutSeconds)
	} else {
		// 未启用超时时复用共享 reader，避免 bufio 缓冲导致多问题场景丢数据
		answerLine, err = reader.ReadString('\n')
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
		return "", fmt.Errorf("读取 ask_user 输入失败: %w", err)
	}
	answer := strings.TrimSpace(answerLine)
	if strings.EqualFold(answer, "/cancel") || strings.EqualFold(answer, "cancel") || strings.EqualFold(answer, "quit") {
		return "", newToolError("USER_ABORTED", "用户取消 ask_user")
	}
	if errors.Is(err, io.EOF) && answer == "" {
		return "", newToolError("USER_ABORTED", "用户取消 ask_user")
	}
	// 超时返回空字符串且无 error，此处将其转为超时错误
	if answer == "" && err == nil && r.toolConfirmTimeoutSeconds > 0 {
		return "", newToolError("USER_ABORTED", "等待用户输入超时，已自动取消 ask_user")
	}
	return answer, nil
}

func splitAskUserAnswers(value string) []string {
	replaced := strings.NewReplacer("，", ",", "、", ",").Replace(value)
	parts := strings.Split(replaced, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func matchAskUserOption(answer string, options []AskUserOption) (AskUserOption, bool) {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "" {
		return AskUserOption{}, false
	}
	if index, err := strconv.Atoi(trimmed); err == nil {
		if index >= 1 && index <= len(options) {
			return options[index-1], true
		}
	}
	for _, option := range options {
		if strings.EqualFold(trimmed, option.Value) || strings.EqualFold(trimmed, option.Label) {
			return option, true
		}
	}
	return AskUserOption{}, false
}

func (r *ClientRuntime) submitCallback(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
	success bool,
	result any,
	errorMessage string,
) error {
	// 排空用户执行引导 channel，确保工具执行期间用户输入的引导能在回调提交前被消费。
	if r.guidanceCh != nil {
		select {
		case text := <-r.guidanceCh:
			r.guidanceText = strings.TrimSpace(text)
		default:
		}
	}
	// 如果存在用户执行引导文本，前置到 Result 中，作为提示词附加方式传递给模型。
	// 对于成功和失败的回调都适用：模型在解析工具结果时会先看到引导文本，再看到原始结果。
	// 使用后立即清空，确保每次引导只被消费一次。
	if r.guidanceText != "" {
		guidancePrefix := "[用户执行引导]\n" + r.guidanceText + "\n\n[以下是工具执行结果]\n"
		if str, ok := result.(string); ok {
			result = guidancePrefix + str
		} else if m, ok := result.(map[string]any); ok {
			cloned := make(map[string]any, len(m)+1)
			for k, v := range m {
				cloned[k] = v
			}
			cloned["_user_guidance_prefix"] = guidancePrefix
			result = cloned
		} else if result == nil {
			result = guidancePrefix
		} else {
			result = guidancePrefix + fmt.Sprintf("%v", result)
		}
		r.guidanceText = ""
	}
	callback := provider.ClientCallResult{
		FlowID:       strings.TrimSpace(stringValue(payload["flow_id"])),
		SessionID:    resolveContextString(payload, "session_id"),
		MessageID:    resolveContextString(payload, "message_id"),
		RunID:        resolveContextString(payload, "run_id"),
		CallID:       strings.TrimSpace(nestedString(payload, "function", "id")),
		Success:      success,
		Result:       result,
		ErrorMessage: errorMessage,
	}
	if callback.FlowID == "" || callback.RunID == "" || callback.CallID == "" {
		return fmt.Errorf("客户端回调缺少关键字段")
	}
	// 仅在身份完整（含会话标识）时落盘补偿队列；缺会话标识的载荷沿用历史直发语义，
	// 避免因校验失败而连上报都不再发起。
	queued := false
	if r.agentLoopStore != nil && callback.SessionID != "" {
		if _, err := r.agentLoopStore.EnqueueToolCallback(callback, time.Now().UTC()); err != nil {
			return fmt.Errorf("持久化客户端工具结果失败，尚未发起上报: %w", err)
		}
		queued = true
	}

	retryDelay := r.callbackRetryDelay
	if retryDelay <= 0 {
		retryDelay = 3 * time.Second
	}
	maxRetries := r.callbackMaxRetries
	if maxRetries <= 0 {
		maxRetries = 3
	}

	// 使用独立上下文提交回调结果，避免父上下文已取消时出现 "context deadline exceeded"。
	// 超时时间使用系统配置中的 HTTP 请求超时设定。
	callbackCtx, cancel := DetachedCallbackContext(ctx, time.Duration(config.HTTPTimeoutSeconds)*time.Second)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = providerClient.SubmitClientCallResult(callbackCtx, callback)
		if lastErr == nil {
			if queued {
				r.removeQueuedCallback(callback)
			}
			return nil
		}
		if queued && (agentloop.IsLateToolResultRejected(lastErr) || agentloop.IsPermanentCallbackRejection(lastErr)) {
			r.removeQueuedCallback(callback)
			return nil
		}
		if attempt < maxRetries {
			timer := time.NewTimer(retryDelay)
			select {
			case <-callbackCtx.Done():
				timer.Stop()
				lastErr = callbackCtx.Err()
				attempt = maxRetries
			case <-timer.C:
			}
		}
	}
	if queued {
		// 内联重试只计一次队列尝试，避免快速耗尽补报上限；后续由补报循环按退避继续。
		if _, markErr := r.agentLoopStore.MarkToolCallbackAttempt(callback.FlowID, callback.SessionID, callback.RunID, callback.CallID); markErr != nil {
			r.warnCallbackQueue("登记补报尝试失败", callback.CallID, markErr)
		}
		r.warnCallbackQueue("客户端工具结果已持久化，等待网络恢复后继续补报", callback.CallID, lastErr)
		return nil
	}
	return fmt.Errorf("回调提交失败（已重试 %d 次）: %w", maxRetries, lastErr)
}

// removeQueuedCallback 服务端已受理或权威拒绝后清理队列记录；清理失败仅告警，
// 不能把磁盘错误当成回调失败返回（结果已被服务端消费）。
func (r *ClientRuntime) removeQueuedCallback(callback provider.ClientCallResult) {
	if err := r.agentLoopStore.RemoveToolCallback(callback.FlowID, callback.SessionID, callback.RunID, callback.CallID); err != nil {
		r.warnCallbackQueue("清理已上报工具结果记录失败（不影响服务端结果）", callback.CallID, err)
	}
}

func (r *ClientRuntime) warnCallbackQueue(message, callID string, err error) {
	if r.stderr != nil {
		fmt.Fprintf(r.stderr, "警告: %s: call_id=%s err=%v\n", message, callID, err)
	}
}

func (r *ClientRuntime) confirmCommand(arguments map[string]any, config internalconfig.Config) (bool, error) {
	if !r.interactivePromptsEnabled {
		return false, newToolError("PERMISSION_DENIED", "当前 task 模式不支持交互式命令确认，请将审批模式改为 auto 或预先放行命令")
	}
	switch strings.TrimSpace(config.ApprovalMode) {
	case "auto":
		return true, nil
	case "readonly":
		return false, nil
	}

	commandPreview := commandPreview(arguments)
	fmt.Fprintf(r.stdout, "\n[客户端命令]\n%s\n是否执行？[y/N]: ", commandPreview)
	answerLine, err := r.readStdinWithTimeout(r.toolConfirmTimeoutSeconds)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("读取命令确认输入失败: %w", err)
	}
	answer := strings.TrimSpace(strings.ToLower(answerLine))
	return answer == "y" || answer == "yes", nil
}

func (r *ClientRuntime) runCommand(
	ctx context.Context,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	if action, matched := evaluatePermissionAction(config, "shell_exec", commandPreview(arguments)); matched {
		switch action {
		case "deny":
			return nil, newToolError("PERMISSION_DENIED", "命中权限规则，拒绝执行 shell_exec")
		case "ask":
			confirmConfig := config
			confirmConfig.ApprovalMode = "confirm"
			approved, err := r.confirmCommand(arguments, confirmConfig)
			if err != nil {
				return nil, err
			}
			if !approved {
				return nil, newToolError("PERMISSION_DENIED", "用户拒绝执行 shell_exec")
			}
		}
	}
	payload, err := r.normalizeCommandArguments(arguments, config)
	if err != nil {
		return nil, err
	}
	if err := guardDangerousCommand(payload, config); err != nil {
		return nil, err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(payload.Timeout)*time.Second)
	defer cancel()

	// executeWithSandboxRetry attempts to run the command with the sandbox
	// enabled. If the sandbox fails at runtime (Start or Wait) and the mode
	// is "auto", it retries without the sandbox. In "enforce" mode, the
	// error is surfaced directly.
	//
	// The function returns the result map, the start error (if any), the
	// wait error (if any), and a flag indicating whether a sandbox retry
	// was performed.
	result, startErr, waitErr, _ := r.executeCommandWithSandboxRetry(timeoutCtx, payload, arguments, config)

	// If we got a result, handle success path.
	if result != nil && startErr == nil && waitErr == nil {
		return result, nil
	}

	// If start or wait failed and we already retried (or can't retry),
	// surface the error.
	if startErr != nil {
		return nil, startErr
	}
	if waitErr != nil {
		return nil, waitErr
	}

	// result is non-nil but had some other issue — return it.
	return result, nil
}

// executeCommandWithSandboxRetry runs the command, and if it fails due to a
// sandbox runtime error in "auto" mode, retries without the sandbox.
func (r *ClientRuntime) executeCommandWithSandboxRetry(
	timeoutCtx context.Context,
	payload commandArguments,
	arguments map[string]any,
	config internalconfig.Config,
) (map[string]any, error, error, bool) {
	command, err := buildCommand(timeoutCtx, payload)
	if err != nil {
		return nil, err, nil, false
	}

	// Pre-cache host stdin content if it will be used, so that sandbox
	// retry can reuse the same stdin data. Without this, the first
	// (sandboxed) execution consumes r.stdin, and the retry gets an empty
	// stdin, causing child processes to receive no input.
	var cachedStdin string
	needsStdinCache := !payload.HasStdin &&
		strings.TrimSpace(payload.StdinPrompt) == "" &&
		r.interactivePromptsEnabled && r.stdin != nil
	if needsStdinCache {
		content, readErr := io.ReadAll(r.stdin)
		if readErr != nil {
			return nil, fmt.Errorf("读取 stdin 缓存失败: %w", readErr), nil, false
		}
		cachedStdin = string(content)
		r.stdin = strings.NewReader(cachedStdin)
	}

	result, startErr, waitErr := r.runSingleCommand(timeoutCtx, command, payload, arguments, config)

	// Check if the failure is due to a sandbox runtime error.
	// In auto mode, retry without sandbox.
	sb := payload.Sandbox
	if sb != nil && sb.Mode == internalconfig.ShellSandboxModeAuto {
		sandboxFailed := false
		if startErr != nil && isSandboxRuntimeError(startErr) {
			sandboxFailed = true
		}
		if waitErr != nil {
			if isSandboxRuntimeError(waitErr) {
				sandboxFailed = true
			} else if result != nil {
				// 分别检查 stderr 和 stdout，避免短路遮蔽。
				// runSingleCommand 构建错误消息时优先取 stderr，
				// 如果 stderr 非空但不含沙盒关键词，会遮蔽
				// 可能出现在 stdout 或原始 waitErr 中的沙盒错误。
				if stderrStr, ok := result["stderr"].(string); ok && isSandboxRuntimeError(fmt.Errorf("%s", stderrStr)) {
					sandboxFailed = true
				} else if stdoutStr, ok := result["stdout"].(string); ok && isSandboxRuntimeError(fmt.Errorf("%s", stdoutStr)) {
					sandboxFailed = true
				}
			}
		}
		if sandboxFailed {
			// Retry without sandbox: build a raw command (no sandbox wrapping).
			rawPayload := payload
			rawPayload.Sandbox = nil
			rawCommand, rawErr := buildRawCommand(timeoutCtx, rawPayload)
			if rawErr != nil {
				return nil, rawErr, nil, false
			}
			// Restore cached stdin for retry if it was consumed by the
			// failed sandboxed execution.
			if needsStdinCache {
				r.stdin = strings.NewReader(cachedStdin)
			}
			result2, startErr2, waitErr2 := r.runSingleCommand(timeoutCtx, rawCommand, rawPayload, arguments, config)
			return result2, startErr2, waitErr2, true
		}
	}

	return result, startErr, waitErr, false
}

// runSingleCommand executes a single *exec.Cmd and returns the result,
// along with separate start and wait errors for sandbox retry detection.
func (r *ClientRuntime) runSingleCommand(
	timeoutCtx context.Context,
	command *exec.Cmd,
	payload commandArguments,
	arguments map[string]any,
	config internalconfig.Config,
) (map[string]any, error, error) {
	command.Dir = payload.WorkingDirectory
	command.Env = mergeEnv(payload.EnvOverrides)
	stdin, err := r.resolveCommandStdin(timeoutCtx, payload)
	if err != nil {
		return nil, err, nil
	}
	if stdin != nil {
		command.Stdin = stdin
	}
	auditPayload := map[string]any{
		"command": commandPreview(arguments),
		"cwd":     payload.WorkingDirectory,
	}
	if payload.SourceSkill != "" {
		auditPayload["source_skill"] = payload.SourceSkill
	}
	if len(payload.InjectedEnvKeys) > 0 {
		auditPayload["injected_env_keys"] = append([]string(nil), payload.InjectedEnvKeys...)
	}
	_ = appendAuditLog(config, "run_command", auditPayload)

	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("创建命令标准输出失败: %w", err), nil
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("创建命令错误输出失败: %w", err), nil
	}

	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("启动命令失败: %w", err), nil
	}
	assignProcessTree(command)

	if r.streamCallback == nil {
		r.writeStreamEventf(r.stdout, "\n[执行客户端命令] %s\n", commandPreview(arguments))
	}
	startedAt := time.Now()

	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	stdoutTarget := io.Writer(&stdoutBuffer)
	stderrTarget := io.Writer(&stderrBuffer)
	if r.streamCallback != nil {
		stdoutTarget = io.MultiWriter(&stdoutBuffer, &streamCallbackWriter{callback: r.streamCallback, isError: false})
		stderrTarget = io.MultiWriter(&stderrBuffer, &streamCallbackWriter{callback: r.streamCallback, isError: true})
	} else if r.streamEventLogsEnabled {
		stdoutTarget = io.MultiWriter(&stdoutBuffer, r.stdout)
		stderrTarget = io.MultiWriter(&stderrBuffer, r.stderr)
	}
	var copyGroup sync.WaitGroup
	copyGroup.Add(2)
	go func() {
		defer copyGroup.Done()
		_, _ = io.Copy(stdoutTarget, stdoutPipe)
	}()
	go func() {
		defer copyGroup.Done()
		_, _ = io.Copy(stderrTarget, stderrPipe)
	}()

	waitErr := command.Wait()
	releaseProcessTree(command)
	// 有界等待：进程已退出但后台子进程（如 `&` 后台化）可能仍持有 stdout/stderr pipe 写端，
	// 导致 io.Copy 无法收到 EOF，copyGroup.Wait() 永久阻塞。
	// 设置 5 秒兜底超时，超时后使用已收集的输出继续处理。
	done := make(chan struct{})
	go func() {
		copyGroup.Wait()
		close(done)
	}()
	select {
	case <-done:
		// 正常完成
	case <-time.After(5 * time.Second):
		// 超时兜底：后台进程持有 pipe fd，使用已收集的输出
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
	}

	stdout := stdoutBuffer.String()
	stderr := stderrBuffer.String()
	result := map[string]any{
		"stdout":    stdout,
		"stderr":    stderr,
		"exit_code": command.ProcessState.ExitCode(),
		"cwd":       payload.WorkingDirectory,
	}
	if payload.SourceSkill != "" {
		result["source_skill"] = payload.SourceSkill
	}
	if len(payload.InjectedEnvKeys) > 0 {
		result["injected_env_keys"] = append([]string(nil), payload.InjectedEnvKeys...)
	}

	if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
		if r.streamCallback == nil {
			r.writeStreamEventf(r.stdout, "[本地命令] %s（超时，>%ds）\n", commandPreview(arguments), payload.Timeout)
		}
		return nil, nil, fmt.Errorf("命令执行超时，超过 %d 秒", payload.Timeout)
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr)
		if message == "" {
			message = strings.TrimSpace(stdout)
		}
		if message == "" {
			message = waitErr.Error()
		}
		failureAudit := map[string]any{
			"command":   commandPreview(arguments),
			"cwd":       payload.WorkingDirectory,
			"exit_code": command.ProcessState.ExitCode(),
			"error":     message,
		}
		if payload.SourceSkill != "" {
			failureAudit["source_skill"] = payload.SourceSkill
		}
		if len(payload.InjectedEnvKeys) > 0 {
			failureAudit["injected_env_keys"] = append([]string(nil), payload.InjectedEnvKeys...)
		}
		_ = appendAuditLog(config, "run_command_failed", failureAudit)
		if r.streamCallback == nil {
			r.writeStreamEventf(r.stdout, "[本地命令] %s（失败，%s，退出码 %d）\n", commandPreview(arguments), formatCommandDuration(time.Since(startedAt)), command.ProcessState.ExitCode())
		}
		return result, nil, fmt.Errorf("命令执行失败: %s", message)
	}

	if payload.ExpectJSON {
		var decoded any
		if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
			return nil, nil, fmt.Errorf("命令输出了不合法的JSON数据")
		}
		result["json"] = decoded
	}

	if r.streamCallback == nil && r.streamEventLogsEnabled && stdout != "" && !strings.HasSuffix(stdout, "\n") {
		fmt.Fprintln(r.stdout)
	}
	if r.streamCallback == nil && r.streamEventLogsEnabled && stderr != "" && !strings.HasSuffix(stderr, "\n") {
		fmt.Fprintln(r.stderr)
	}
	successAudit := map[string]any{
		"command":   commandPreview(arguments),
		"cwd":       payload.WorkingDirectory,
		"exit_code": command.ProcessState.ExitCode(),
	}
	if payload.SourceSkill != "" {
		successAudit["source_skill"] = payload.SourceSkill
	}
	if len(payload.InjectedEnvKeys) > 0 {
		successAudit["injected_env_keys"] = append([]string(nil), payload.InjectedEnvKeys...)
	}
	_ = appendAuditLog(config, "run_command_succeeded", successAudit)
	if r.streamCallback == nil {
		r.writeStreamEventf(r.stdout, "[本地命令] %s（成功，%s，退出码 %d）\n", commandPreview(arguments), formatCommandDuration(time.Since(startedAt)), command.ProcessState.ExitCode())
	}

	return result, nil, nil
}

func formatCommandDuration(duration time.Duration) string {
	if duration < time.Second {
		return fmt.Sprintf("%dms", duration.Milliseconds())
	}
	if duration < time.Minute {
		return fmt.Sprintf("%.1fs", duration.Seconds())
	}
	minutes := int(duration / time.Minute)
	seconds := int((duration % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

func (r *ClientRuntime) searchFiles(
	ctx context.Context,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	return r.searchFilesLocal(ctx, config, arguments)
}

type commandArguments struct {
	Mode             string
	RawCommand       string
	Command          string
	Args             []string
	HasStdin         bool
	Stdin            string
	StdinPrompt      string
	WorkingDirectory string
	Timeout          int
	EnvOverrides     map[string]string
	SourceSkill      string
	InjectedEnvKeys  []string
	ExpectJSON       bool
	Sandbox          *shellSandboxConfig
}

func (r *ClientRuntime) normalizeCommandArguments(arguments map[string]any, config internalconfig.Config) (commandArguments, error) {
	timeout := intValue(arguments["timeout"], defaultCommandTimeoutSeconds(config))
	if timeout < 1 {
		timeout = 1
	}
	if timeout > internalconfig.MaxCommandTimeoutSeconds {
		timeout = internalconfig.MaxCommandTimeoutSeconds
	}

	workingDirectory, err := r.resolveAllowedDirectoryWithPolicy(config, argumentString(arguments, "workingDirectory", "working_directory"))
	if err != nil {
		return commandArguments{}, err
	}

	// Resolve OS-level shell sandbox configuration.
	sandbox := resolveShellSandbox(config, workingDirectory)

	envOverrides := map[string]string{}
	if rawEnv, ok := firstArgument(arguments, "env", "env_overrides"); ok {
		if rawEnvMap, ok := rawEnv.(map[string]any); ok {
			for key, value := range rawEnvMap {
				envOverrides[strings.TrimSpace(key)] = stringValue(value)
			}
		} else if rawEnvMap, ok := rawEnv.(map[string]string); ok {
			for key, value := range rawEnvMap {
				envOverrides[strings.TrimSpace(key)] = value
			}
		}
	}
	envOverrides = sanitizeEnvOverrides(envOverrides)
	sourceSkill, envOverrides, injectedEnvKeys, err := resolveSourceSkillEnv(config, argumentString(arguments, "source_skill", "sourceSkill"), envOverrides)
	if err != nil {
		return commandArguments{}, err
	}
	rawStdin, hasStdin := firstArgument(arguments, "stdin")
	stdin := ""
	if hasStdin {
		// 对 stdin 应用传输层转义归一化，将字面 \n（反斜杠+n）转换为真实换行符。
		// 与 rawCommand、args 的处理保持一致，修复 LLM JSON 双重转义导致的字面 \n 问题。
		stdin = normalizeTransportEscapes(stringValue(rawStdin))
	}
	stdinPrompt := argumentString(arguments, "stdinPrompt", "stdin_prompt")

	rawCommand := normalizeTransportEscapes(argumentString(arguments, "rawCommand", "raw_command"))
	if rawCommand != "" {
		return commandArguments{
			Mode:             "shell",
			RawCommand:       rawCommand,
			HasStdin:         hasStdin,
			Stdin:            stdin,
			StdinPrompt:      stdinPrompt,
			WorkingDirectory: workingDirectory,
			Timeout:          timeout,
			EnvOverrides:     envOverrides,
			SourceSkill:      sourceSkill,
			InjectedEnvKeys:  injectedEnvKeys,
			ExpectJSON:       argumentBool(arguments, "expectJson", "expect_json"),
			Sandbox:          sandbox,
		}, nil
	}

	commandName := strings.TrimSpace(normalizeTransportEscapes(stringValue(arguments["command"])))
	if commandName == "" {
		return commandArguments{}, fmt.Errorf("command 或 raw_command 至少需要提供一个")
	}

	args := normalizeTransportEscapedArgs(stringSlice(arguments["args"]))
	commandName, args, rawCommand = normalizeLegacyCommandField(commandName, args, workingDirectory, runtime.GOOS)
	if rawCommand == "" {
		commandName, args, rawCommand = normalizeStructuredCommandField(commandName, args, runtime.GOOS)
	}
	if rawCommand != "" {
		return commandArguments{
			Mode:             "shell",
			RawCommand:       rawCommand,
			HasStdin:         hasStdin,
			Stdin:            stdin,
			StdinPrompt:      stdinPrompt,
			WorkingDirectory: workingDirectory,
			Timeout:          timeout,
			EnvOverrides:     envOverrides,
			SourceSkill:      sourceSkill,
			InjectedEnvKeys:  injectedEnvKeys,
			ExpectJSON:       argumentBool(arguments, "expectJson", "expect_json"),
			Sandbox:          sandbox,
		}, nil
	}
	return commandArguments{
		Mode:             "exec",
		Command:          commandName,
		Args:             args,
		HasStdin:         hasStdin,
		Stdin:            stdin,
		StdinPrompt:      stdinPrompt,
		WorkingDirectory: workingDirectory,
		Timeout:          timeout,
		EnvOverrides:     envOverrides,
		SourceSkill:      sourceSkill,
		InjectedEnvKeys:  injectedEnvKeys,
		ExpectJSON:       argumentBool(arguments, "expectJson", "expect_json"),
		Sandbox:          sandbox,
	}, nil
}

func resolveSourceSkillEnv(config internalconfig.Config, sourceSkill string, envOverrides map[string]string) (string, map[string]string, []string, error) {
	trimmed := strings.TrimSpace(sourceSkill)
	if trimmed == "" {
		return "", envOverrides, nil, nil
	}
	item, _, err := skills.LoadByName(config, trimmed)
	if err != nil {
		return "", nil, nil, newToolError("PARAM_ERROR", "source_skill 无效: %s", trimmed)
	}
	requiredEnvKeys := skills.RequiredEnvKeys(*item)
	if len(requiredEnvKeys) == 0 {
		return item.Name, envOverrides, nil, nil
	}
	merged := map[string]string{}
	for key, value := range envOverrides {
		merged[key] = value
	}
	injectedEnvKeys := make([]string, 0, len(requiredEnvKeys))
	missingEnvKeys := make([]string, 0, len(requiredEnvKeys))
	for _, key := range requiredEnvKeys {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		if _, ok := merged[trimmedKey]; ok {
			continue
		}
		value, ok := os.LookupEnv(trimmedKey)
		if !ok {
			missingEnvKeys = append(missingEnvKeys, trimmedKey)
			continue
		}
		merged[trimmedKey] = value
		injectedEnvKeys = append(injectedEnvKeys, trimmedKey)
	}
	if len(missingEnvKeys) > 0 {
		sort.Strings(missingEnvKeys)
		return "", nil, nil, newToolError("MISSING_ENV", "skill %s 缺少必需环境变量: %s", item.Name, strings.Join(missingEnvKeys, ", "))
	}
	sort.Strings(injectedEnvKeys)
	return item.Name, merged, injectedEnvKeys, nil
}

func (r *ClientRuntime) resolveCommandStdin(ctx context.Context, payload commandArguments) (io.Reader, error) {
	if payload.HasStdin {
		return strings.NewReader(payload.Stdin), nil
	}
	if strings.TrimSpace(payload.StdinPrompt) != "" {
		if !r.interactivePromptsEnabled {
			return nil, newToolError("PERMISSION_DENIED", "当前 task 模式不支持 stdin_prompt，请显式提供 stdin")
		}
		response, err := r.promptAskUser(ctx, AskUserRequest{
			Questions: []AskUserQuestion{{
				Name:        "stdin",
				Label:       payload.StdinPrompt,
				InputFormat: "text",
			}},
		})
		if err != nil {
			return nil, err
		}
		stdin := stringValue(response.Answers["stdin"])
		if !strings.HasSuffix(stdin, "\n") {
			stdin += "\n"
		}
		return strings.NewReader(stdin), nil
	}
	if !r.interactivePromptsEnabled {
		return nil, nil
	}
	if r.stdin != nil {
		return r.stdin, nil
	}
	return nil, nil
}

func normalizeTransportEscapedArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	normalized := make([]string, 0, len(args))
	for _, arg := range args {
		normalized = append(normalized, normalizeTransportEscapes(arg))
	}
	return normalized
}

func normalizeTransportEscapes(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}

	var builder strings.Builder
	builder.Grow(len(value))
	changed := false
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char != '\\' || index+1 >= len(value) {
			builder.WriteByte(char)
			continue
		}

		next := value[index+1]
		switch next {
		case '\\', '"', '\'':
			builder.WriteByte(next)
			index++
			changed = true
		case 'n':
			builder.WriteByte('\n')
			index++
			changed = true
		case 'r':
			builder.WriteByte('\r')
			index++
			changed = true
		case 't':
			builder.WriteByte('\t')
			index++
			changed = true
		default:
			// 保留 \$ 等非传输层转义，避免改变 shell 语义。
			builder.WriteByte(char)
		}
	}
	if !changed {
		return value
	}
	return builder.String()
}

func normalizeStructuredCommandField(commandName string, args []string, goos string) (string, []string, string) {
	trimmed := strings.TrimSpace(commandName)
	if trimmed == "" {
		return "", args, ""
	}
	if !isLikelyEnvAssignment(trimmed) && !isShellBuiltinOnly(trimmed, goos) {
		if argsContainShellSyntax(args, goos) {
			return "", nil, joinShellCommand(trimmed, args, goos)
		}
		return trimmed, args, ""
	}
	return "", nil, joinShellCommand(trimmed, args, goos)
}

// argsContainShellSyntax 检测 args 中是否包含明确的 shell 操作符。
// 只检测 |、&、;、<、> 等作为独立参数时几乎不可能是程序参数的字符，
// 避免将 (、)、$、* 等可能是合法程序参数（如 Python 代码）的字符误判为 shell 语法。
func argsContainShellSyntax(args []string, goos string) bool {
	for _, arg := range args {
		if isShellOperatorArg(arg, goos) {
			return true
		}
	}
	return false
}

// isShellOperatorArg 判断一个参数是否是 shell 操作符。
// 匹配独立的管道符、逻辑运算符、命令分隔符和重定向符号。
func isShellOperatorArg(arg string, goos string) bool {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return false
	}
	switch trimmed {
	case "|", "||", "&&", "&", ";", ";;", "|&":
		return true
	}
	if goos == "windows" {
		return false
	}
	// POSIX: 检测重定向操作符（<, >, >>, <<, <<<, 2>, 2>>, &>, etc.）
	if containsRedirection(trimmed) {
		return true
	}
	return false
}

func containsRedirection(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '<' || c == '>' {
			return true
		}
	}
	return false
}

func defaultCommandTimeoutSeconds(config internalconfig.Config) int {
	return internalconfig.NormalizeCommandTimeoutSeconds(config.CommandTimeoutSeconds)
}

func normalizeLegacyCommandField(commandName string, args []string, workingDirectory string, goos string) (string, []string, string) {
	trimmed := strings.TrimSpace(commandName)
	if trimmed == "" || len(args) != 0 {
		return trimmed, args, ""
	}
	if looksLikeExecutablePath(trimmed, workingDirectory) {
		return trimmed, nil, ""
	}
	if containsShellSyntax(trimmed, goos) {
		return "", nil, trimmed
	}
	parsed, err := splitCommandLine(trimmed, goos)
	if err != nil || len(parsed) == 0 {
		return "", nil, trimmed
	}
	if isLikelyEnvAssignment(parsed[0]) || isShellBuiltinOnly(parsed[0], goos) {
		return "", nil, trimmed
	}
	return parsed[0], parsed[1:], ""
}

func looksLikeExecutablePath(commandName string, workingDirectory string) bool {
	trimmed := strings.TrimSpace(commandName)
	if trimmed == "" {
		return false
	}

	checkPath := ""
	if filepath.IsAbs(trimmed) {
		checkPath = trimmed
	} else if strings.Contains(trimmed, string(filepath.Separator)) || strings.Contains(trimmed, "/") || strings.Contains(trimmed, `\\`) {
		checkPath = filepath.Join(workingDirectory, trimmed)
	}
	if checkPath == "" {
		return false
	}

	info, err := os.Stat(checkPath)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func containsShellSyntax(command string, goos string) bool {
	inSingleQuotes := false
	inDoubleQuotes := false
	escaped := false
	for _, char := range command {
		if escaped {
			escaped = false
			continue
		}
		if inSingleQuotes {
			if char == '\'' {
				inSingleQuotes = false
			}
			continue
		}
		if inDoubleQuotes {
			switch char {
			case '"':
				inDoubleQuotes = false
			case '\\':
				if goos != "windows" {
					escaped = true
				}
			}
			continue
		}

		switch char {
		case '\'':
			inSingleQuotes = true
		case '"':
			inDoubleQuotes = true
		case '|', '&', ';', '<', '>', '(', ')', '$', '`', '*', '?', '[', ']', '{', '}', '!', '\n', '\r':
			return true
		case '%', '^':
			if goos == "windows" {
				return true
			}
		}
	}
	return false
}

func splitCommandLine(command string, goos string) ([]string, error) {
	var fields []string
	var current strings.Builder
	tokenStarted := false
	inSingleQuotes := false
	inDoubleQuotes := false
	runes := []rune(command)

	flush := func() {
		if !tokenStarted {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
		tokenStarted = false
	}

	for index := 0; index < len(runes); index++ {
		char := runes[index]
		if inSingleQuotes {
			if char == '\'' {
				inSingleQuotes = false
				continue
			}
			current.WriteRune(char)
			tokenStarted = true
			continue
		}
		if inDoubleQuotes {
			switch char {
			case '"':
				inDoubleQuotes = false
			case '\\':
				if goos == "windows" {
					if index+1 < len(runes) && (runes[index+1] == '"' || runes[index+1] == '\\') {
						current.WriteRune(runes[index+1])
						index++
					} else {
						current.WriteRune(char)
					}
				} else if index+1 < len(runes) {
					current.WriteRune(runes[index+1])
					index++
				} else {
					current.WriteRune(char)
				}
			default:
				current.WriteRune(char)
			}
			tokenStarted = true
			continue
		}

		switch char {
		case '\'', '"':
			tokenStarted = true
			if char == '\'' {
				inSingleQuotes = true
			} else {
				inDoubleQuotes = true
			}
		case ' ', '\t', '\n', '\r':
			flush()
		case '\\':
			tokenStarted = true
			if goos != "windows" && index+1 < len(runes) {
				current.WriteRune(runes[index+1])
				index++
			} else {
				current.WriteRune(char)
			}
		default:
			current.WriteRune(char)
			tokenStarted = true
		}
	}

	if inSingleQuotes || inDoubleQuotes {
		return nil, fmt.Errorf("命令包含未闭合的引号")
	}
	flush()
	return fields, nil
}

func isShellBuiltinOnly(commandName string, goos string) bool {
	normalized := strings.ToLower(strings.TrimSpace(commandName))
	if normalized == "" {
		return false
	}
	if goos == "windows" {
		_, exists := windowsShellBuiltins[normalized]
		return exists
	}
	_, exists := posixShellOnlyBuiltins[normalized]
	return exists
}

func isLikelyEnvAssignment(token string) bool {
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	separator := strings.IndexRune(token, '=')
	if separator <= 0 {
		return false
	}
	for index, char := range token[:separator] {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || char == '_' {
			continue
		}
		if index > 0 && char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}

func joinShellCommand(commandName string, args []string, goos string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteShellToken(commandName, goos))
	for _, arg := range args {
		if isShellOperatorArg(arg, goos) {
			parts = append(parts, arg)
		} else {
			parts = append(parts, quoteShellToken(arg, goos))
		}
	}
	return strings.Join(parts, " ")
}

func quoteShellToken(token string, goos string) string {
	if goos == "windows" {
		return quoteWindowsCommandToken(token)
	}
	return quotePOSIXShellToken(token)
}

func quotePOSIXShellToken(token string) string {
	if isLikelyEnvAssignment(token) {
		separator := strings.IndexRune(token, '=')
		return token[:separator+1] + quotePOSIXShellText(token[separator+1:])
	}
	return quotePOSIXShellText(token)
}

func quotePOSIXShellText(token string) string {
	if token == "" {
		return "''"
	}
	if isPOSIXShellSafeToken(token) {
		return token
	}
	return "'" + strings.ReplaceAll(token, "'", `'"'"'`) + "'"
}

func isPOSIXShellSafeToken(token string) bool {
	if token == "" {
		return false
	}
	for _, char := range token {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case strings.ContainsRune("@%_+=:,./-", char):
		default:
			return false
		}
	}
	return true
}

func quoteWindowsCommandToken(token string) string {
	if token == "" {
		return `""`
	}
	if isWindowsCommandSafeToken(token) {
		return token
	}
	return `"` + strings.ReplaceAll(token, `"`, `\"`) + `"`
}

func isWindowsCommandSafeToken(token string) bool {
	if token == "" {
		return false
	}
	for _, char := range token {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case strings.ContainsRune("@%_+=:,./-", char):
		default:
			return false
		}
	}
	return true
}

// buildCommand 构建 *exec.Cmd，根据 payload.Sandbox 决定是否包装 OS 级沙盒。
// 沙盒逻辑见 shell_sandbox.go 和平台特定文件（_darwin/_linux/_windows）。
func buildCommand(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	return buildSandboxedCommand(ctx, payload)
}

func resolveBaseDirectory(defaultWorkdir string, requested string) (string, error) {
	base := strings.TrimSpace(defaultWorkdir)
	if base == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("获取当前工作目录失败: %w", err)
		}
		base = cwd
	}
	if !filepath.IsAbs(base) {
		absoluteBase, err := filepath.Abs(base)
		if err != nil {
			return "", fmt.Errorf("解析默认工作目录失败: %w", err)
		}
		base = absoluteBase
	}

	target := strings.TrimSpace(requested)
	if target == "" {
		target = base
	} else if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}

	resolved, err := filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("解析工作目录失败: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("工作目录不存在: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("working_directory 必须是目录")
	}
	return resolved, nil
}

func mergeEnv(overrides map[string]string) []string {
	base := os.Environ()
	merged := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
			if isSensitiveEnvKey(parts[0]) {
				continue
			}
			merged[parts[0]] = parts[1]
		}
	}
	for key, value := range overrides {
		if strings.TrimSpace(key) == "" {
			continue
		}
		merged[key] = value
	}
	// 将 CLI 二进制文件所在目录追加到 PATH 末尾，使操作系统内置工具优先，
	// CLI 捆绑工具仅在系统 PATH 中不存在时作为回退
	appendCLIDirToPathInEnv(merged)
	result := make([]string, 0, len(merged))
	for key, value := range merged {
		result = append(result, key+"="+value)
	}
	sort.Strings(result)
	return result
}

// appendCLIDirToPathInEnv 将 CLI 二进制文件所在目录追加到环境变量 map 中的 PATH 值末尾。
// 兼容 Windows 下 PATH 变量名大小写差异。
func appendCLIDirToPathInEnv(envMap map[string]string) {
	// 查找 PATH 键（兼容 Windows 大小写）
	pathKey := ""
	for k := range envMap {
		if strings.EqualFold(k, "PATH") {
			pathKey = k
			break
		}
	}
	if pathKey == "" {
		pathKey = "PATH"
		envMap[pathKey] = ""
	}
	envMap[pathKey] = appendCLIDirToPath(envMap[pathKey])
}

func commandPreview(arguments map[string]any) string {
	rawCommand := argumentString(arguments, "rawCommand", "raw_command")
	if rawCommand != "" {
		return rawCommand
	}
	parts := append([]string{strings.TrimSpace(stringValue(arguments["command"]))}, argumentStringSlice(arguments, "args")...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func policyConfirmationRequired(policy map[string]any) bool {
	if value, ok := policy["confirmation_required"]; ok {
		return boolValue(value)
	}
	return true
}

func nestedMap(payload map[string]any, keys ...string) map[string]any {
	current := payload
	for _, key := range keys {
		value, ok := current[key]
		if !ok {
			return map[string]any{}
		}
		next, ok := value.(map[string]any)
		if !ok {
			return map[string]any{}
		}
		current = next
	}
	return current
}

func nestedString(payload map[string]any, keys ...string) string {
	if len(keys) == 0 {
		return ""
	}
	if len(keys) == 1 {
		return stringValue(payload[keys[0]])
	}
	current := payload
	for index, key := range keys {
		value, ok := current[key]
		if !ok {
			return ""
		}
		if index == len(keys)-1 {
			return stringValue(value)
		}
		next, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

// resolveContextString 兼容两种回调载荷形态：优先取 context.<key>，回落到顶层 <key>。
// 早期实现仅读 context.*，导致只带顶层 session_id/run_id 的载荷被判空，误伤派发与上报。
func resolveContextString(payload map[string]any, key string) string {
	if value := strings.TrimSpace(nestedString(payload, "context", key)); value != "" {
		return value
	}
	return strings.TrimSpace(stringValue(payload[key]))
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

// stringSliceValue 将 any 类型转换为 []string。
// 支持 []any（JSON 反序列化结果）和 []string 两种输入。
func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if s := stringValue(item); s != "" {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

func intValue(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			items = append(items, stringValue(item))
		}
		return items
	default:
		return nil
	}
}

// switchToAgent 处理 switch_to_agent 工具调用。
// 设置 pendingModeSwitch 标记，由 cmd 层在工具回调完成后消费并更新会话状态。
// 返回值包含 additional_tools 字段，列出 agent 模式相比 ask/plan 模式新增的工具完整定义
// （name、description、inputSchema、waiting_for_response），便于模型了解切换后可用的工具及其参数。
func (r *ClientRuntime) switchToAgent(arguments map[string]any) (map[string]any, error) {
	r.pendingModeSwitch = provider.RequestTypeAgent
	reason := argumentString(arguments, "reason")
	message := "已切换到执行模式（agent），后续对话将使用 agent 模式。"
	if reason != "" {
		message = fmt.Sprintf("已切换到执行模式（agent），原因: %s", reason)
	}
	return map[string]any{
		"switched_to":      "agent",
		"message":          message,
		"additional_tools": r.additionalAgentToolDescriptions(),
	}, nil
}

// additionalAgentToolDescriptions 计算 agent 模式相比当前模式（ask 或 plan）新增的工具完整定义。
// 基准模式由 r.currentRequestMode 决定，如果未设置则回退到 ask。
// 通过对比 clientToolDefinitionsForMode(agent) 和 clientToolDefinitionsForMode(基准模式) 的差集得到。
// 返回每个工具的 name、description、inputSchema 和 waiting_for_response，确保模型知道切换后可用工具的参数格式。
func (r *ClientRuntime) additionalAgentToolDescriptions() []map[string]any {
	baseMode := r.currentRequestMode
	if baseMode == "" || baseMode == provider.RequestTypeAgent {
		baseMode = provider.RequestTypeAsk
	}
	agentDefs := r.clientToolDefinitionsForMode(provider.RequestTypeAgent)
	baseDefs := r.clientToolDefinitionsForMode(baseMode)

	baseNames := make(map[string]bool, len(baseDefs))
	for _, def := range baseDefs {
		name := strings.TrimSpace(stringValue(def["name"]))
		if name != "" {
			baseNames[name] = true
		}
	}

	additional := make([]map[string]any, 0)
	for _, def := range agentDefs {
		name := strings.TrimSpace(stringValue(def["name"]))
		if name == "" || baseNames[name] {
			continue
		}
		entry := map[string]any{
			"name":        name,
			"description": def["description"],
		}
		if schema, ok := def["inputSchema"]; ok {
			entry["inputSchema"] = schema
		}
		if wfr, ok := def["waiting_for_response"]; ok {
			entry["waiting_for_response"] = wfr
		}
		additional = append(additional, entry)
	}
	return additional
}

// streamCallbackWriter 将写入的数据实时转发给 streamCallback。
type streamCallbackWriter struct {
	callback func(text string, isError bool)
	isError  bool
}

func (w *streamCallbackWriter) Write(p []byte) (int, error) {
	w.callback(string(p), w.isError)
	return len(p), nil
}
