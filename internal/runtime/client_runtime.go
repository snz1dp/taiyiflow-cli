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
	defaultCommandTimeout = 30
	maxCommandTimeout     = 300
	localToolServerName   = "local"
)

type ClientRuntime struct {
	stdin                       io.Reader
	stdout                      io.Writer
	stderr                      io.Writer
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
	case "task_list":
		result, err = r.taskList(config)
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
	var copyGroup sync.WaitGroup
	copyGroup.Add(2)
	go func() {
		defer copyGroup.Done()
		_, _ = io.Copy(io.MultiWriter(&stdoutBuffer, r.stdout), stdoutPipe)
	}()
	go func() {
		defer copyGroup.Done()
		_, _ = io.Copy(io.MultiWriter(&stderrBuffer, r.stderr), stderrPipe)
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

	if stdout != "" && !strings.HasSuffix(stdout, "\n") {
		fmt.Fprintln(r.stdout)
	}
	if stderr != "" && !strings.HasSuffix(stderr, "\n") {
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

func detectSearchEngine() (string, error) {
	if _, err := exec.LookPath("rg"); err == nil {
		return "rg", nil
	}
	if _, err := exec.LookPath("grep"); err == nil {
		return "grep", nil
	}
	return "", fmt.Errorf("文件搜索需要可用的搜索工具：系统未找到 rg 或 grep")
}

func runRipgrep(
	ctx context.Context,
	query string,
	baseDir string,
	glob string,
	maxResults int,
) ([]byte, error) {
	commandArgs := []string{"-n", "--no-heading", "--color", "never", "--max-count", strconv.Itoa(maxResults), query}
	if glob != "" {
		commandArgs = append(commandArgs, "-g", glob)
	}
	commandArgs = append(commandArgs, baseDir)

	command := exec.CommandContext(ctx, "rg", commandArgs...)
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("执行 rg 搜索失败: %w", err)
	}
	return output, nil
}

func runGrep(ctx context.Context, query string, baseDir string) ([]byte, error) {
	commandArgs := []string{"-R", "-n", "-E", query, baseDir}
	command := exec.CommandContext(ctx, "grep", commandArgs...)
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("执行 grep 搜索失败: %w", err)
	}
	return output, nil
}

func parseSearchMatches(output []byte, baseDir string, glob string, maxResults int) []map[string]any {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return []map[string]any{}
	}

	lines := strings.Split(trimmed, "\n")
	matches := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		if !matchesSearchGlob(parts[0], baseDir, glob) {
			continue
		}
		lineNumber, _ := strconv.Atoi(parts[1])
		matches = append(matches, map[string]any{
			"path": parts[0],
			"line": lineNumber,
			"text": parts[2],
		})
		if len(matches) >= maxResults {
			break
		}
	}
	return matches
}

func matchesSearchGlob(path string, baseDir string, glob string) bool {
	if strings.TrimSpace(glob) == "" {
		return true
	}

	cleanPath := filepath.Clean(path)
	relativePath, err := filepath.Rel(baseDir, cleanPath)
	if err != nil {
		relativePath = cleanPath
	}

	normalizedPath := filepath.ToSlash(relativePath)
	normalizedGlob := filepath.ToSlash(strings.TrimSpace(glob))

	matched, err := filepath.Match(normalizedGlob, normalizedPath)
	if err == nil && matched {
		return true
	}

	if !strings.Contains(normalizedGlob, "/") {
		matched, err = filepath.Match(normalizedGlob, filepath.Base(normalizedPath))
		return err == nil && matched
	}

	return false
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
	timeout := intValue(arguments["timeout"], defaultCommandTimeout)
	if timeout < 1 {
		timeout = 1
	}
	if timeout > maxCommandTimeout {
		timeout = maxCommandTimeout
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
