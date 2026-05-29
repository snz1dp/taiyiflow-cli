package browserbridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

const (
	NativeMessagingHostName         = "cn.snz1.taiyiflow_cli.browser_host"
	ChromeExtensionID               = "agijaddhpaelilloojogabbiejogibdo"
	NativeMessagingProtocolVersion  = 1
	nativeMessagingStateStaleWindow = 2 * time.Minute
)

const ChromeExtensionManifestKey = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAlZMW9e4h4HVDDAcUFSKv19ChmEs6kShuYMmi+Bd2XoCahR1wvH9H8aD8Vg0VgPfd21v1xkOFyawUJU8MKhh3wKVZfXdonMoS5Byd89hNnYPx9N0pIKNMkpcX/5b2m8V1xSW3clZCZl8by3KoZ6/295koPnDnuVoOyMMBvz9CM9CFgqS41+vihuZeiHq90fAI49U6+AUgzgJBFVTYbx+phhSaoTC+FaWRzllDjYdlkpbpfpmXwBmyyZ9uyneeRwtznC1teTBWfNE/T1K/yBqhIJzGUAbVXaR30r0NzVPv0373a0L0+hG1VoInKAo9CVQqMWYWoKwDndIJPRGgE6TIIwIDAQAB"

type NativeMessagingManifest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Path           string   `json:"path"`
	Type           string   `json:"type"`
	AllowedOrigins []string `json:"allowed_origins"`
}

type NativeMessagingPaths struct {
	RootDir           string
	ManifestDir       string
	ManifestPath      string
	LauncherDir       string
	LauncherPath      string
	StateDir          string
	StatePath         string
	SocketDir         string
	SocketPath        string
	HostName          string
	ExtensionID       string
	ExtensionOrigin   string
	ExtensionBuildDir string
	ExtensionDir      string
	BinaryPath        string
}

type InstallResult struct {
	Channel           string
	HostName          string
	ManifestPath      string
	ManifestPaths     []string
	LauncherPath      string
	StatePath         string
	SocketPath        string
	ExtensionID       string
	ExtensionOrigin   string
	ExtensionDir      string
	ExtensionBuildDir string
	BinaryPath        string
}

type NativeMessagingStatus struct {
	Channel             string
	HostName            string
	ManifestPath        string
	ManifestInstalled   bool
	LauncherPath        string
	LauncherInstalled   bool
	StatePath           string
	StateDetected       bool
	SocketPath          string
	SocketReachable     bool
	ExtensionConnected  bool
	LastHelloAt         string
	ExtensionVersion    string
	ExtensionOrigin     string
	ExtensionDir        string
	ExtensionBuildDir   string
	ExtensionBuildFound bool
	Message             string
}

type nativeHostState struct {
	HostName          string   `json:"host_name"`
	ProtocolVersion   int      `json:"protocol_version"`
	ExtensionID       string   `json:"extension_id,omitempty"`
	ExtensionVersion  string   `json:"extension_version,omitempty"`
	Capabilities      []string `json:"capabilities,omitempty"`
	Connected         bool     `json:"connected"`
	LastHelloAt       string   `json:"last_hello_at,omitempty"`
	LastDisconnectAt  string   `json:"last_disconnect_at,omitempty"`
	UpdatedAt         string   `json:"updated_at,omitempty"`
	LastError         string   `json:"last_error,omitempty"`
	SocketNetwork     string   `json:"socket_network,omitempty"`
	SocketAddress     string   `json:"socket_address,omitempty"`
	SocketReady       bool     `json:"socket_ready,omitempty"`
	ManifestInstalled bool     `json:"manifest_installed,omitempty"`
	LauncherInstalled bool     `json:"launcher_installed,omitempty"`
}

type nativeMessage struct {
	ID               string         `json:"id,omitempty"`
	Type             string         `json:"type,omitempty"`
	OK               bool           `json:"ok,omitempty"`
	Error            string         `json:"error,omitempty"`
	HostName         string         `json:"host_name,omitempty"`
	ProtocolVersion  int            `json:"protocol_version,omitempty"`
	ExtensionID      string         `json:"extension_id,omitempty"`
	ExtensionVersion string         `json:"extension_version,omitempty"`
	Capabilities     []string       `json:"capabilities,omitempty"`
	Payload          map[string]any `json:"payload,omitempty"`
	Timestamp        string         `json:"timestamp,omitempty"`
}

func ResolveNativeMessagingPaths(config internalconfig.BrowserConfig, extensionProjectDir string, binaryPath string) (NativeMessagingPaths, error) {
	rootDir, err := internalconfig.DefaultRootDir()
	if err != nil {
		return NativeMessagingPaths{}, err
	}
	manifestDir, err := resolveNativeMessagingManifestDir(config.Channel)
	if err != nil {
		return NativeMessagingPaths{}, err
	}
	launcherDir := filepath.Join(rootDir, "browser", "native-messaging")
	launcherPath := filepath.Join(launcherDir, nativeMessagingLauncherName())
	stateDir := filepath.Join(rootDir, "browser", "native-messaging")
	statePath := filepath.Join(stateDir, "chrome-extension-state.json")
	socketDir := strings.TrimSpace(config.SocketDir)
	if socketDir == "" {
		socketDir = filepath.Join(rootDir, "browser", "socket")
	} else if !filepath.IsAbs(socketDir) {
		socketDir = filepath.Join(rootDir, socketDir)
	}
	absBinaryPath := strings.TrimSpace(binaryPath)
	if absBinaryPath != "" {
		resolvedBinaryPath, resolveErr := filepath.Abs(absBinaryPath)
		if resolveErr != nil {
			return NativeMessagingPaths{}, resolveErr
		}
		absBinaryPath = resolvedBinaryPath
	}
	result := NativeMessagingPaths{
		RootDir:         rootDir,
		ManifestDir:     manifestDir,
		ManifestPath:    filepath.Join(manifestDir, NativeMessagingHostName+".json"),
		LauncherDir:     launcherDir,
		LauncherPath:    launcherPath,
		StateDir:        stateDir,
		StatePath:       statePath,
		SocketDir:       socketDir,
		SocketPath:      filepath.Join(socketDir, nativeMessagingSocketName(config.Channel)),
		HostName:        NativeMessagingHostName,
		ExtensionID:     ChromeExtensionID,
		ExtensionOrigin: "chrome-extension://" + ChromeExtensionID + "/",
		ExtensionDir:    strings.TrimSpace(extensionProjectDir),
		BinaryPath:      absBinaryPath,
	}
	if result.ExtensionDir != "" {
		result.ExtensionBuildDir = filepath.Join(result.ExtensionDir, "dist")
	}
	return result, nil
}

func InstallNativeMessagingHost(config internalconfig.BrowserConfig, binaryPath string, extensionProjectDir string) (InstallResult, error) {
	paths, err := ResolveNativeMessagingPaths(config, extensionProjectDir, binaryPath)
	if err != nil {
		return InstallResult{}, err
	}
	manifestPaths, err := resolveNativeMessagingManifestPaths(config.Channel)
	if err != nil {
		return InstallResult{}, err
	}
	if strings.TrimSpace(paths.BinaryPath) == "" {
		return InstallResult{}, fmt.Errorf("无法解析当前 CLI 可执行文件路径")
	}
	if err := os.MkdirAll(paths.LauncherDir, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("创建本地消息宿主启动脚本目录失败: %w", err)
	}
	for _, manifestPath := range manifestPaths {
		if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
			return InstallResult{}, fmt.Errorf("创建本地消息宿主清单目录失败: %w", err)
		}
	}
	if err := os.MkdirAll(paths.StateDir, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("创建本地消息宿主状态目录失败: %w", err)
	}
	if err := os.MkdirAll(paths.SocketDir, 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("创建浏览器桥接 socket 目录失败: %w", err)
	}
	if err := writeNativeMessagingLauncher(paths.LauncherPath, paths.BinaryPath); err != nil {
		return InstallResult{}, err
	}
	manifest := NativeMessagingManifest{
		Name:           paths.HostName,
		Description:    "TaiyiFlow CLI Browser Message Host",
		Path:           paths.LauncherPath,
		Type:           "stdio",
		AllowedOrigins: []string{paths.ExtensionOrigin},
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return InstallResult{}, fmt.Errorf("序列化本地消息宿主清单失败: %w", err)
	}
	manifestBytes = append(manifestBytes, '\n')
	if err := os.WriteFile(paths.ManifestPath, manifestBytes, 0o644); err != nil {
		return InstallResult{}, fmt.Errorf("写入本地消息宿主清单失败: %w", err)
	}
	for _, manifestPath := range manifestPaths {
		if manifestPath == paths.ManifestPath {
			continue
		}
		if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
			return InstallResult{}, fmt.Errorf("写入本地消息宿主清单失败: %w", err)
		}
	}
	state := nativeHostState{
		HostName:          paths.HostName,
		ProtocolVersion:   NativeMessagingProtocolVersion,
		SocketAddress:     paths.SocketPath,
		ManifestInstalled: true,
		LauncherInstalled: true,
		UpdatedAt:         time.Now().Format(time.RFC3339Nano),
	}
	_ = writeNativeHostState(paths.StatePath, state)
	return InstallResult{
		Channel:           normalizedBrowserChannel(config.Channel),
		HostName:          paths.HostName,
		ManifestPath:      paths.ManifestPath,
		ManifestPaths:     append([]string(nil), manifestPaths...),
		LauncherPath:      paths.LauncherPath,
		StatePath:         paths.StatePath,
		SocketPath:        paths.SocketPath,
		ExtensionID:       paths.ExtensionID,
		ExtensionOrigin:   paths.ExtensionOrigin,
		ExtensionDir:      paths.ExtensionDir,
		ExtensionBuildDir: paths.ExtensionBuildDir,
		BinaryPath:        paths.BinaryPath,
	}, nil
}

func DiagnoseNativeMessaging(config internalconfig.BrowserConfig, extensionProjectDir string) NativeMessagingStatus {
	paths, err := ResolveNativeMessagingPaths(config, extensionProjectDir, "")
	if err != nil {
		return NativeMessagingStatus{Channel: normalizedBrowserChannel(config.Channel), HostName: NativeMessagingHostName, Message: err.Error()}
	}
	status := NativeMessagingStatus{
		Channel:           normalizedBrowserChannel(config.Channel),
		HostName:          paths.HostName,
		ManifestPath:      paths.ManifestPath,
		LauncherPath:      paths.LauncherPath,
		StatePath:         paths.StatePath,
		SocketPath:        paths.SocketPath,
		ExtensionOrigin:   paths.ExtensionOrigin,
		ExtensionDir:      paths.ExtensionDir,
		ExtensionBuildDir: paths.ExtensionBuildDir,
	}
	if info, statErr := os.Stat(paths.ManifestPath); statErr == nil && !info.IsDir() {
		status.ManifestInstalled = true
	}
	if info, statErr := os.Stat(paths.LauncherPath); statErr == nil && !info.IsDir() {
		status.LauncherInstalled = true
	}
	if strings.TrimSpace(paths.ExtensionBuildDir) != "" {
		if info, statErr := os.Stat(filepath.Join(paths.ExtensionBuildDir, "manifest.json")); statErr == nil && !info.IsDir() {
			status.ExtensionBuildFound = true
		}
	}
	state, stateDetected := readNativeHostState(paths.StatePath)
	status.StateDetected = stateDetected
	if stateDetected {
		status.LastHelloAt = strings.TrimSpace(state.LastHelloAt)
		status.ExtensionVersion = strings.TrimSpace(state.ExtensionVersion)
		if strings.TrimSpace(state.SocketAddress) != "" {
			status.SocketPath = strings.TrimSpace(state.SocketAddress)
		}
		status.SocketReachable = probeNativeBridge(config, paths, state)
		if state.Connected && state.LastHelloAt != "" {
			if parsedTime, parseErr := time.Parse(time.RFC3339Nano, state.LastHelloAt); parseErr == nil && time.Since(parsedTime) <= nativeMessagingStateStaleWindow {
				status.ExtensionConnected = true
			}
		}
	}
	status.Message = buildNativeMessagingStatusMessage(status)
	return status
}

func RunNativeMessagingHost(ctx context.Context, config internalconfig.BrowserConfig, stdin io.Reader, stdout io.Writer, _ io.Writer) error {
	paths, err := ResolveNativeMessagingPaths(config, "", "")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.StateDir, 0o755); err != nil {
		return fmt.Errorf("创建本地消息宿主状态目录失败: %w", err)
	}
	state, _ := readNativeHostState(paths.StatePath)
	state.HostName = paths.HostName
	state.ProtocolVersion = NativeMessagingProtocolVersion
	state.Connected = false
	state.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	writer := &lockedNativeMessageWriter{writer: stdout}
	bridge := newNativeHostBridge(config, paths, state, writer)
	if err := bridge.start(ctx); err != nil {
		return err
	}
	defer bridge.close()
	defer func() {
		bridge.markExtensionDisconnected()
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		message, err := readNativeMessage(stdin)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			bridge.markError(err.Error())
			return err
		}
		response := nativeMessage{
			ID:              message.ID,
			HostName:        paths.HostName,
			ProtocolVersion: NativeMessagingProtocolVersion,
			Timestamp:       time.Now().Format(time.RFC3339Nano),
		}
		switch strings.TrimSpace(message.Type) {
		case "hello":
			bridge.markExtensionHello(message.ExtensionID, message.ExtensionVersion, message.Capabilities)
			response.Type = "hello_ack"
			response.OK = true
			response.ExtensionID = ChromeExtensionID
			response.Payload = map[string]any{
				"extension_origin": paths.ExtensionOrigin,
				"state_path":       paths.StatePath,
				"channel":          normalizedBrowserChannel(config.Channel),
				"socket_path":      paths.SocketPath,
			}
		case "ping":
			response.Type = "pong"
			response.OK = true
		case "tool_request":
			response.Type = "tool_result"
			response.Error = "当前本地消息宿主仅提供安装链路与握手诊断，浏览器工具执行协议待后续接线"
		case "tool_result":
			bridge.resolveToolResult(message)
			continue
		default:
			response.Type = "error"
			response.Error = fmt.Sprintf("不支持的本地消息协议类型: %s", strings.TrimSpace(message.Type))
		}
		if err := writer.write(response); err != nil {
			bridge.markError(err.Error())
			return err
		}
	}
}

func readNativeMessage(reader io.Reader) (nativeMessage, error) {
	var length uint32
	if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
		return nativeMessage{}, err
	}
	if length == 0 {
		return nativeMessage{}, fmt.Errorf("读取本地消息协议数据失败: 空 payload")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nativeMessage{}, err
	}
	message := nativeMessage{}
	if err := json.Unmarshal(payload, &message); err != nil {
		return nativeMessage{}, fmt.Errorf("解析本地消息协议数据失败: %w", err)
	}
	return message, nil
}

func writeNativeMessage(writer io.Writer, message nativeMessage) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("序列化本地消息协议响应失败: %w", err)
	}
	if err := binary.Write(writer, binary.LittleEndian, uint32(len(payload))); err != nil {
		return fmt.Errorf("写入本地消息协议响应长度失败: %w", err)
	}
	if _, err := writer.Write(payload); err != nil {
		return fmt.Errorf("写入本地消息协议响应内容失败: %w", err)
	}
	return nil
}

func writeNativeMessagingLauncher(path string, binaryPath string) error {
	content := ""
	mode := os.FileMode(0o755)
	switch runtime.GOOS {
	case "windows":
		content = "@echo off\r\n\"" + strings.ReplaceAll(binaryPath, "\"", "\"\"") + "\" browser host\r\n"
		mode = 0o644
	default:
		content = "#!/bin/sh\nset -eu\nexec \"" + strings.ReplaceAll(binaryPath, "\"", `\"`) + "\" browser host\n"
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("写入本地消息宿主启动脚本失败: %w", err)
	}
	return nil
}

func writeNativeHostState(path string, state nativeHostState) error {
	stateBytes, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	stateBytes = append(stateBytes, '\n')
	return os.WriteFile(path, stateBytes, 0o644)
}

func readNativeHostState(path string) (nativeHostState, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nativeHostState{}, false
	}
	state := nativeHostState{}
	if err := json.Unmarshal(content, &state); err != nil {
		return nativeHostState{}, false
	}
	return state, true
}

func buildNativeMessagingStatusMessage(status NativeMessagingStatus) string {
	parts := []string{fmt.Sprintf("宿主: %s", status.HostName)}
	if status.ManifestInstalled {
		parts = append(parts, "清单: 已安装")
	} else {
		parts = append(parts, "清单: 未安装")
	}
	if status.LauncherInstalled {
		parts = append(parts, "启动脚本: 已安装")
	} else {
		parts = append(parts, "启动脚本: 未安装")
	}
	if status.ExtensionConnected {
		parts = append(parts, "扩展连接: 已连接")
	} else {
		parts = append(parts, "扩展连接: 未连接")
	}
	if status.SocketReachable {
		parts = append(parts, "套接字: 可达")
	} else if strings.TrimSpace(status.SocketPath) != "" {
		parts = append(parts, "套接字: 不可达")
	}
	if status.ExtensionBuildFound {
		parts = append(parts, "构建产物: 已生成")
	} else if strings.TrimSpace(status.ExtensionBuildDir) != "" {
		parts = append(parts, "构建产物: 未生成")
	}
	if strings.TrimSpace(status.LastHelloAt) != "" {
		parts = append(parts, "最近握手: "+status.LastHelloAt)
	}
	return strings.Join(parts, " | ")
}

func resolveNativeMessagingManifestDir(channel string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户目录失败: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		switch normalizedBrowserChannel(channel) {
		case internalconfig.BrowserChannelChromium:
			return filepath.Join(homeDir, "Library", "Application Support", "Chromium", "NativeMessagingHosts"), nil
		case internalconfig.BrowserChannelEdge:
			return filepath.Join(homeDir, "Library", "Application Support", "Microsoft Edge", "NativeMessagingHosts"), nil
		case internalconfig.BrowserChannelBrave:
			return filepath.Join(homeDir, "Library", "Application Support", "BraveSoftware", "Brave-Browser", "NativeMessagingHosts"), nil
		default:
			return filepath.Join(homeDir, "Library", "Application Support", "Google", "Chrome", "NativeMessagingHosts"), nil
		}
	case "windows":
		appData := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if appData == "" {
			return "", fmt.Errorf("缺少 LOCALAPPDATA，无法解析本地消息宿主清单目录")
		}
		switch normalizedBrowserChannel(channel) {
		case internalconfig.BrowserChannelChromium:
			return filepath.Join(appData, "Chromium", "User Data", "NativeMessagingHosts"), nil
		case internalconfig.BrowserChannelEdge:
			return filepath.Join(appData, "Microsoft", "Edge", "User Data", "NativeMessagingHosts"), nil
		case internalconfig.BrowserChannelBrave:
			return filepath.Join(appData, "BraveSoftware", "Brave-Browser", "User Data", "NativeMessagingHosts"), nil
		default:
			return filepath.Join(appData, "Google", "Chrome", "User Data", "NativeMessagingHosts"), nil
		}
	default:
		switch normalizedBrowserChannel(channel) {
		case internalconfig.BrowserChannelChromium:
			return filepath.Join(homeDir, ".config", "chromium", "NativeMessagingHosts"), nil
		case internalconfig.BrowserChannelEdge:
			return filepath.Join(homeDir, ".config", "microsoft-edge", "NativeMessagingHosts"), nil
		case internalconfig.BrowserChannelBrave:
			return filepath.Join(homeDir, ".config", "BraveSoftware", "Brave-Browser", "NativeMessagingHosts"), nil
		default:
			return filepath.Join(homeDir, ".config", "google-chrome", "NativeMessagingHosts"), nil
		}
	}
}

func resolveNativeMessagingManifestPaths(channel string) ([]string, error) {
	channels := nativeMessagingManifestChannels(channel)
	paths := make([]string, 0, len(channels))
	for _, currentChannel := range channels {
		manifestDir, err := resolveNativeMessagingManifestDir(currentChannel)
		if err != nil {
			return nil, err
		}
		paths = append(paths, filepath.Join(manifestDir, NativeMessagingHostName+".json"))
	}
	return paths, nil
}

func nativeMessagingManifestChannels(channel string) []string {
	ordered := []string{
		normalizedBrowserChannel(channel),
		internalconfig.BrowserChannelChrome,
		internalconfig.BrowserChannelChromium,
		internalconfig.BrowserChannelEdge,
		internalconfig.BrowserChannelBrave,
	}
	result := make([]string, 0, len(ordered))
	seen := make(map[string]struct{}, len(ordered))
	for _, item := range ordered {
		normalized := normalizedBrowserChannel(item)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func nativeMessagingLauncherName() string {
	if runtime.GOOS == "windows" {
		return "taiyiflow-chrome-host.cmd"
	}
	return "taiyiflow-chrome-host.sh"
}

func nativeMessagingSocketName(channel string) string {
	name := "taiyiflow-" + normalizedBrowserChannel(channel) + "-bridge"
	if runtime.GOOS == "windows" {
		return name + ".tcp"
	}
	return name + ".sock"
}

func normalizedBrowserChannel(value string) string {
	return internalconfig.NormalizeBrowserChannel(value)
}