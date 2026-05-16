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

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
)

const (
	localToolServerName = "local"
)

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
	sandboxEscapeConfirmer      func(path string) (bool, error)
	sandboxEscapePolicyOverride string
	streamEventLogsEnabled      bool
}

func NewClientRuntime(stdin io.Reader, stdout io.Writer, stderr io.Writer) *ClientRuntime {
	return &ClientRuntime{stdin: stdin, stdout: stdout, stderr: stderr, streamEventLogsEnabled: true}
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

func (r *ClientRuntime) SetStreamEventLogsEnabled(enabled bool) {
	r.streamEventLogsEnabled = enabled
}

func (r *ClientRuntime) writeStreamEventf(writer io.Writer, format string, args ...any) {
	if !r.streamEventLogsEnabled {
		return
	}
	fmt.Fprintf(writer, format, args...)
}

func (r *ClientRuntime) ClientMCPServers() map[string]any {
	return r.ClientMCPServersForMode(provider.RequestTypeAgent)
}

func (r *ClientRuntime) ClientMCPServersForMode(mode string) map[string]any {
	return map[string]any{
		localToolServerName: r.clientToolDefinitionsForMode(mode),
	}
}

func (r *ClientRuntime) HandleBuiltInEvent(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
) error {
	serverName := nestedString(payload, "server", "name")
	commandName := nestedString(payload, "command", "name")
	if serverName != "built_in_command" || commandName != "run_command_via_client" {
		return r.submitCallback(ctx, providerClient, payload, false, nil, fmt.Sprintf("不支持的内置调用: %s/%s", serverName, commandName))
	}

	arguments := nestedMap(payload, "command", "arguments")
	policy := nestedMap(payload, "command", "policy")
	if policyConfirmationRequired(policy) {
		approved, err := r.confirmCommand(arguments, config)
		if err != nil {
			return r.submitCallback(ctx, providerClient, payload, false, nil, err.Error())
		}
		if !approved {
			return r.submitCallback(ctx, providerClient, payload, false, nil, "用户拒绝执行客户端命令。")
		}
	}

	result, err := r.runCommand(ctx, config, arguments)
	if err != nil {
		return r.submitCallback(ctx, providerClient, payload, false, nil, err.Error())
	}
	return r.submitCallback(ctx, providerClient, payload, true, result, "")
}

func (r *ClientRuntime) HandleClientToolEvent(
	ctx context.Context,
	providerClient provider.Provider,
	config internalconfig.Config,
	payload map[string]any,
) error {
	_, result, err := r.ExecuteClientToolPayload(ctx, config, payload)
	return r.SubmitClientToolCallback(ctx, providerClient, payload, result, err)
}

func (r *ClientRuntime) ExecuteClientToolPayload(
	ctx context.Context,
	config internalconfig.Config,
	payload map[string]any,
) (string, any, error) {
	serverName := nestedString(payload, "server", "name")
	toolName := canonicalClientToolName(nestedString(payload, "function", "name"))
	arguments := nestedMap(payload, "function", "arguments")
	arguments = enrichToolArgumentsWithContext(arguments, payload)

	if serverName != localToolServerName {
		return toolName, nil, fmt.Errorf("不支持的客户端工具服务: %s", serverName)
	}
	if err := r.authorizeClientTool(toolName, arguments, config); err != nil {
		return toolName, nil, err
	}

	var (
		result any
		err    error
	)
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
	case "search_files":
		result, err = r.searchFiles(ctx, config, arguments)
	case "read_file":
		result, err = r.readFile(config, arguments)
	case "write_file":
		result, err = r.writeFile(config, arguments)
	case "skill":
		result, err = r.skill(config, arguments)
	default:
		err = fmt.Errorf("不支持的客户端工具: %s", toolName)
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
	return cloned
}

func (r *ClientRuntime) SubmitClientToolCallback(
	ctx context.Context,
	providerClient provider.Provider,
	payload map[string]any,
	result any,
	err error,
) error {
	if err != nil {
		return r.submitCallback(ctx, providerClient, payload, false, result, err.Error())
	}
	return r.submitCallback(ctx, providerClient, payload, true, result, "")
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
	answerLine, err := reader.ReadString('\n')
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
	payload map[string]any,
	success bool,
	result any,
	errorMessage string,
) error {
	callback := provider.ClientCallResult{
		FlowID:       stringValue(payload["flow_id"]),
		SessionID:    nestedString(payload, "context", "session_id"),
		MessageID:    nestedString(payload, "context", "message_id"),
		RunID:        nestedString(payload, "context", "run_id"),
		CallID:       nestedString(payload, "function", "id"),
		Success:      success,
		Result:       result,
		ErrorMessage: errorMessage,
	}
	if callback.FlowID == "" || callback.RunID == "" || callback.CallID == "" {
		return fmt.Errorf("客户端回调缺少关键字段")
	}
	return providerClient.SubmitClientCallResult(ctx, callback)
}

func (r *ClientRuntime) confirmCommand(arguments map[string]any, config internalconfig.Config) (bool, error) {
	switch strings.TrimSpace(config.ApprovalMode) {
	case "auto":
		return true, nil
	case "readonly":
		return false, nil
	}

	commandPreview := commandPreview(arguments)
	fmt.Fprintf(r.stdout, "\n[客户端命令]\n%s\n是否执行？[y/N]: ", commandPreview)
	reader := bufio.NewReader(r.stdin)
	answerLine, err := reader.ReadString('\n')
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

	command, err := buildCommand(timeoutCtx, payload)
	if err != nil {
		return nil, err
	}

	command.Dir = payload.WorkingDirectory
	command.Env = mergeEnv(payload.EnvOverrides)
	_ = appendAuditLog(config, "run_command", map[string]any{
		"command": commandPreview(arguments),
		"cwd":     payload.WorkingDirectory,
	})

	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("创建命令标准输出失败: %w", err)
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("创建命令错误输出失败: %w", err)
	}

	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("启动命令失败: %w", err)
	}

	r.writeStreamEventf(r.stdout, "\n[执行客户端命令] %s\n", commandPreview(arguments))
	startedAt := time.Now()

	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	stdoutTarget := io.Writer(&stdoutBuffer)
	stderrTarget := io.Writer(&stderrBuffer)
	if r.streamEventLogsEnabled {
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
	copyGroup.Wait()

	stdout := stdoutBuffer.String()
	stderr := stderrBuffer.String()
	result := map[string]any{
		"stdout":    stdout,
		"stderr":    stderr,
		"exit_code": command.ProcessState.ExitCode(),
		"cwd":       payload.WorkingDirectory,
	}

	if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
		r.writeStreamEventf(r.stdout, "[本地命令] %s（超时，>%ds）\n", commandPreview(arguments), payload.Timeout)
		return nil, fmt.Errorf("命令执行超时，超过 %d 秒", payload.Timeout)
	}
	if waitErr != nil {
		message := strings.TrimSpace(stderr)
		if message == "" {
			message = strings.TrimSpace(stdout)
		}
		if message == "" {
			message = waitErr.Error()
		}
		_ = appendAuditLog(config, "run_command_failed", map[string]any{
			"command":   commandPreview(arguments),
			"cwd":       payload.WorkingDirectory,
			"exit_code": command.ProcessState.ExitCode(),
			"error":     message,
		})
		r.writeStreamEventf(r.stdout, "[本地命令] %s（失败，%s，退出码 %d）\n", commandPreview(arguments), formatCommandDuration(time.Since(startedAt)), command.ProcessState.ExitCode())
		return nil, fmt.Errorf("命令执行失败: %s", message)
	}

	if payload.ExpectJSON {
		var decoded any
		if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
			return nil, fmt.Errorf("命令输出了不合法的JSON数据")
		}
		result["json"] = decoded
	}

	if r.streamEventLogsEnabled && stdout != "" && !strings.HasSuffix(stdout, "\n") {
		fmt.Fprintln(r.stdout)
	}
	if r.streamEventLogsEnabled && stderr != "" && !strings.HasSuffix(stderr, "\n") {
		fmt.Fprintln(r.stderr)
	}
	_ = appendAuditLog(config, "run_command_succeeded", map[string]any{
		"command":   commandPreview(arguments),
		"cwd":       payload.WorkingDirectory,
		"exit_code": command.ProcessState.ExitCode(),
	})
	r.writeStreamEventf(r.stdout, "[本地命令] %s（成功，%s，退出码 %d）\n", commandPreview(arguments), formatCommandDuration(time.Since(startedAt)), command.ProcessState.ExitCode())

	return result, nil
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
	WorkingDirectory string
	Timeout          int
	EnvOverrides     map[string]string
	ExpectJSON       bool
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

	rawCommand := argumentString(arguments, "rawCommand", "raw_command")
	if rawCommand != "" {
		return commandArguments{
			Mode:             "shell",
			RawCommand:       rawCommand,
			WorkingDirectory: workingDirectory,
			Timeout:          timeout,
			EnvOverrides:     envOverrides,
			ExpectJSON:       argumentBool(arguments, "expectJson", "expect_json"),
		}, nil
	}

	commandName := strings.TrimSpace(stringValue(arguments["command"]))
	if commandName == "" {
		return commandArguments{}, fmt.Errorf("command 或 raw_command 至少需要提供一个")
	}

	args := stringSlice(arguments["args"])
	return commandArguments{
		Mode:             "exec",
		Command:          commandName,
		Args:             args,
		WorkingDirectory: workingDirectory,
		Timeout:          timeout,
		EnvOverrides:     envOverrides,
		ExpectJSON:       argumentBool(arguments, "expectJson", "expect_json"),
	}, nil
}

func defaultCommandTimeoutSeconds(config internalconfig.Config) int {
	return internalconfig.NormalizeCommandTimeoutSeconds(config.CommandTimeoutSeconds)
}

func buildCommand(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	if payload.Mode == "shell" {
		if runtime.GOOS == "windows" {
			return exec.CommandContext(ctx, "cmd", "/C", payload.RawCommand), nil
		}
		return exec.CommandContext(ctx, "sh", "-lc", payload.RawCommand), nil
	}
	return exec.CommandContext(ctx, payload.Command, payload.Args...), nil
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
	result := make([]string, 0, len(merged))
	for key, value := range merged {
		result = append(result, key+"="+value)
	}
	sort.Strings(result)
	return result
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
