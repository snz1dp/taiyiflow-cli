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
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func NewClientRuntime(stdin io.Reader, stdout io.Writer, stderr io.Writer) *ClientRuntime {
	return &ClientRuntime{stdin: stdin, stdout: stdout, stderr: stderr}
}

func (r *ClientRuntime) ClientMCPServers() map[string]any {
	return map[string]any{
		localToolServerName: []map[string]any{
			{
				"name":                 "search_files",
				"description":          "在当前工作目录搜索文本，优先使用 ripgrep，不可用时回退 grep，返回匹配文件、行号和片段。",
				"waiting_for_response": true,
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "要搜索的文本或正则表达式。",
						},
						"path": map[string]any{
							"type":        "string",
							"description": "搜索根目录，可为相对或绝对路径。默认当前工作目录。",
						},
						"glob": map[string]any{
							"type":        "string",
							"description": "可选 glob 过滤，例如 *.go 或 cmd/*.go。",
						},
						"max_results": map[string]any{
							"type":        "integer",
							"minimum":     1,
							"maximum":     200,
							"description": "最大返回条数，默认 50。",
						},
					},
					"required":             []string{"query"},
					"additionalProperties": false,
				},
			},
		},
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
		return r.submitCallback(ctx, providerClient, payload, false, nil, fmt.Sprintf("不支持的 built_in 调用: %s/%s", serverName, commandName))
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
	serverName := nestedString(payload, "server", "name")
	toolName := nestedString(payload, "function", "name")
	arguments := nestedMap(payload, "function", "arguments")

	if serverName != localToolServerName {
		return r.submitCallback(ctx, providerClient, payload, false, nil, fmt.Sprintf("不支持的客户端工具服务: %s", serverName))
	}

	var (
		result any
		err    error
	)
	if toolName == "search_files" {
		result, err = r.searchFiles(ctx, config, arguments)
	} else {
		err = fmt.Errorf("不支持的客户端工具: %s", toolName)
	}

	if err != nil {
		return r.submitCallback(ctx, providerClient, payload, false, nil, err.Error())
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
	payload, err := normalizeCommandArguments(arguments, config.Workdir)
	if err != nil {
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

	fmt.Fprintf(r.stdout, "\n[执行客户端命令] %s\n", commandPreview(arguments))

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

	return result, nil
}

func (r *ClientRuntime) searchFiles(
	ctx context.Context,
	config internalconfig.Config,
	arguments map[string]any,
) (map[string]any, error) {
	query := strings.TrimSpace(stringValue(arguments["query"]))
	if query == "" {
		return nil, fmt.Errorf("文件搜索缺少关键词")
	}

	baseDir, err := resolveBaseDirectory(config.Workdir, stringValue(arguments["path"]))
	if err != nil {
		return nil, err
	}

	maxResults := intValue(arguments["max_results"], 50)
	if maxResults < 1 {
		maxResults = 1
	}
	if maxResults > 200 {
		maxResults = 200
	}

	glob := strings.TrimSpace(stringValue(arguments["glob"]))

	searchEngine, err := detectSearchEngine()
	if err != nil {
		return nil, err
	}

	var output []byte
	switch searchEngine {
	case "rg":
		output, err = runRipgrep(ctx, query, baseDir, glob, maxResults)
	case "grep":
		output, err = runGrep(ctx, query, baseDir)
	default:
		return nil, fmt.Errorf("不支持的搜索引擎: %s", searchEngine)
	}
	if err != nil {
		return nil, err
	}

	matches := parseSearchMatches(output, baseDir, glob, maxResults)

	fmt.Fprintf(r.stdout, "\n[本地搜索] engine=%s query=%s 命中 %d 条\n", searchEngine, query, len(matches))
	return map[string]any{
		"matches": matches,
		"count":   len(matches),
		"root":    baseDir,
		"engine":  searchEngine,
	}, nil
}

func detectSearchEngine() (string, error) {
	if _, err := exec.LookPath("rg"); err == nil {
		return "rg", nil
	}
	if _, err := exec.LookPath("grep"); err == nil {
		return "grep", nil
	}
	return "", fmt.Errorf("search_files 需要可用的搜索工具：未找到 rg 或 grep")
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

func normalizeCommandArguments(arguments map[string]any, defaultWorkdir string) (commandArguments, error) {
	timeout := intValue(arguments["timeout"], defaultCommandTimeout)
	if timeout < 1 {
		timeout = 1
	}
	if timeout > maxCommandTimeout {
		timeout = maxCommandTimeout
	}

	workingDirectory, err := resolveBaseDirectory(defaultWorkdir, stringValue(arguments["working_directory"]))
	if err != nil {
		return commandArguments{}, err
	}

	envOverrides := map[string]string{}
	if rawEnv, ok := arguments["env_overrides"].(map[string]any); ok {
		for key, value := range rawEnv {
			envOverrides[strings.TrimSpace(key)] = stringValue(value)
		}
	} else if rawEnv, ok := arguments["env_overrides"].(map[string]string); ok {
		for key, value := range rawEnv {
			envOverrides[strings.TrimSpace(key)] = value
		}
	}

	rawCommand := strings.TrimSpace(stringValue(arguments["raw_command"]))
	if rawCommand != "" {
		return commandArguments{
			Mode:             "shell",
			RawCommand:       rawCommand,
			WorkingDirectory: workingDirectory,
			Timeout:          timeout,
			EnvOverrides:     envOverrides,
			ExpectJSON:       boolValue(arguments["expect_json"]),
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
		ExpectJSON:       boolValue(arguments["expect_json"]),
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
	if len(overrides) == 0 {
		return base
	}
	merged := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) == 2 {
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
	return result
}

func commandPreview(arguments map[string]any) string {
	rawCommand := strings.TrimSpace(stringValue(arguments["raw_command"]))
	if rawCommand != "" {
		return rawCommand
	}
	parts := append([]string{strings.TrimSpace(stringValue(arguments["command"]))}, stringSlice(arguments["args"])...)
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
