package browserbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

type bridgeToolRequest struct {
	ID        string         `json:"id,omitempty"`
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type bridgeToolResponse struct {
	ID     string         `json:"id,omitempty"`
	OK     bool           `json:"ok"`
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type lockedNativeMessageWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *lockedNativeMessageWriter) write(message nativeMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return writeNativeMessage(w.writer, message)
}

type nativeHostBridge struct {
	config   internalconfig.BrowserConfig
	paths    NativeMessagingPaths
	writer   *lockedNativeMessageWriter
	listener net.Listener

	stateMu sync.Mutex
	state   nativeHostState

	pendingMu sync.Mutex
	pending   map[string]chan bridgeToolResponse
	seq       atomic.Uint64
	closeOnce sync.Once
	cleanup   func()

	connMu       sync.Mutex
	activeConns  int
	connChangeCh chan struct{}
}

func newNativeHostBridge(config internalconfig.BrowserConfig, paths NativeMessagingPaths, state nativeHostState, writer *lockedNativeMessageWriter) *nativeHostBridge {
	return &nativeHostBridge{
		config:       internalconfig.NormalizeBrowserConfig(config),
		paths:        paths,
		writer:       writer,
		state:        state,
		pending:      map[string]chan bridgeToolResponse{},
		connChangeCh: make(chan struct{}, 1),
	}
}

func ExecuteNativeMessagingTool(ctx context.Context, config internalconfig.BrowserConfig, toolName string, arguments map[string]any) (map[string]any, error) {
	normalized := internalconfig.NormalizeBrowserConfig(config)
	if normalized.Mode != internalconfig.BrowserModeChromeSession {
		return nil, fmt.Errorf("本地消息浏览器桥仅支持当前会话模式")
	}
	if normalized.ExtensionSetup == internalconfig.BrowserExtensionSetupNone {
		return nil, fmt.Errorf("当前会话模式依赖本地消息扩展接入，当前扩展接入方式为不接扩展（none）")
	}
	paths, err := ResolveNativeMessagingPaths(normalized, "", "")
	if err != nil {
		return nil, err
	}
	state, _ := readNativeHostState(paths.StatePath)
	network, address, ok := nativeBridgeDialTarget(paths, state)
	if !ok {
		return nil, fmt.Errorf("未检测到可用的浏览器桥接套接字，请先加载扩展并保持后台连接")
	}
	dialer := &net.Dialer{Timeout: browserTimeout(normalized)}
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("连接浏览器桥接套接字失败: %w", err)
	}
	defer conn.Close()
	setConnDeadline(conn, ctx, browserTimeout(normalized))
	request := bridgeToolRequest{
		ID:        nextBridgeRequestID(),
		ToolName:  strings.TrimSpace(toolName),
		Arguments: cloneMap(arguments),
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		return nil, fmt.Errorf("发送浏览器桥接请求失败: %w", err)
	}
	response := bridgeToolResponse{}
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		return nil, fmt.Errorf("读取浏览器桥接响应失败: %w", err)
	}
	if !response.OK {
		if strings.TrimSpace(response.Error) == "" {
			return nil, fmt.Errorf("浏览器桥接返回失败")
		}
		return nil, errors.New(strings.TrimSpace(response.Error))
	}
	if response.Result == nil {
		return map[string]any{}, nil
	}
	return response.Result, nil
}

func (b *nativeHostBridge) start(ctx context.Context) error {
	listener, network, address, cleanup, err := listenNativeBridge(b.config, b.paths)
	if err != nil {
		return err
	}
	b.listener = listener
	b.cleanup = cleanup
	b.updateState(func(state *nativeHostState) {
		state.SocketNetwork = network
		state.SocketAddress = address
		state.SocketReady = true
		state.LastError = ""
	})
	go b.acceptLoop(ctx)
	return nil
}

func (b *nativeHostBridge) close() {
	b.closeOnce.Do(func() {
		if b.listener != nil {
			_ = b.listener.Close()
		}
		if b.cleanup != nil {
			b.cleanup()
		}
		b.failPending(fmt.Errorf("浏览器桥接服务已停止"))
		b.updateState(func(state *nativeHostState) {
			state.SocketReady = false
		})
	})
}

func (b *nativeHostBridge) trackConnStart() {
	b.connMu.Lock()
	b.activeConns++
	b.connMu.Unlock()
	b.notifyConnChange()
}

func (b *nativeHostBridge) trackConnEnd() {
	b.connMu.Lock()
	if b.activeConns > 0 {
		b.activeConns--
	}
	b.connMu.Unlock()
	b.notifyConnChange()
}

func (b *nativeHostBridge) notifyConnChange() {
	select {
	case b.connChangeCh <- struct{}{}:
	default:
	}
}

func (b *nativeHostBridge) ActiveConnectionCount() int {
	b.connMu.Lock()
	defer b.connMu.Unlock()
	return b.activeConns
}

func (b *nativeHostBridge) acceptLoop(ctx context.Context) {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			b.updateState(func(state *nativeHostState) {
				state.LastError = fmt.Sprintf("接受浏览器桥接连接失败: %v", err)
			})
			continue
		}
		go b.handleConn(conn)
	}
}

func (b *nativeHostBridge) handleConn(conn net.Conn) {
	b.trackConnStart()
	defer b.trackConnEnd()
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(browserTimeout(b.config)))
	request := bridgeToolRequest{}
	if err := json.NewDecoder(conn).Decode(&request); err != nil {
		if errors.Is(err, io.EOF) {
			return
		}
		_ = json.NewEncoder(conn).Encode(bridgeToolResponse{OK: false, Error: fmt.Sprintf("解析浏览器桥接请求失败: %v", err)})
		return
	}
	request.ID = strings.TrimSpace(request.ID)
	if request.ID == "" {
		request.ID = nextBridgeRequestID()
	}
	if !b.isExtensionConnected() {
		_ = json.NewEncoder(conn).Encode(bridgeToolResponse{ID: request.ID, OK: false, Error: "浏览器扩展尚未连接，请先加载扩展并保持后台连接"})
		return
	}
	responseCh := make(chan bridgeToolResponse, 1)
	b.registerPending(request.ID, responseCh)
	defer b.unregisterPending(request.ID)
	message := nativeMessage{
		ID:              request.ID,
		Type:            "tool_request",
		HostName:        b.paths.HostName,
		ProtocolVersion: NativeMessagingProtocolVersion,
		Timestamp:       time.Now().Format(time.RFC3339Nano),
		Payload: map[string]any{
			"tool":      request.ToolName,
			"arguments": request.Arguments,
		},
	}
	if err := b.writer.write(message); err != nil {
		_ = json.NewEncoder(conn).Encode(bridgeToolResponse{ID: request.ID, OK: false, Error: fmt.Sprintf("向浏览器扩展发送工具请求失败: %v", err)})
		return
	}
	timer := time.NewTimer(browserTimeout(b.config))
	defer timer.Stop()
	select {
	case response := <-responseCh:
		if err := json.NewEncoder(conn).Encode(response); err != nil {
			b.updateState(func(state *nativeHostState) {
				state.LastError = fmt.Sprintf("写回浏览器桥接响应失败: %v", err)
			})
		}
	case <-timer.C:
		b.updateState(func(state *nativeHostState) {
			state.LastError = "等待浏览器扩展工具响应超时"
		})
		_ = json.NewEncoder(conn).Encode(bridgeToolResponse{ID: request.ID, OK: false, Error: "等待浏览器扩展工具响应超时"})
	}
}

func (b *nativeHostBridge) resolveToolResult(message nativeMessage) bool {
	requestID := strings.TrimSpace(message.ID)
	if requestID == "" {
		return false
	}
	b.pendingMu.Lock()
	responseCh, ok := b.pending[requestID]
	b.pendingMu.Unlock()
	if !ok {
		return false
	}
	response := bridgeToolResponse{ID: requestID, OK: message.OK}
	if message.OK {
		if result, ok := mapValue(message.Payload, "result"); ok {
			response.Result = result
		} else {
			response.Result = map[string]any{}
		}
	} else {
		response.Error = strings.TrimSpace(message.Error)
		if response.Error == "" {
			response.Error = "浏览器扩展返回失败"
		}
	}
	select {
	case responseCh <- response:
	default:
	}
	return true
}

func (b *nativeHostBridge) markExtensionHello(extensionID string, extensionVersion string, capabilities []string) {
	b.updateState(func(state *nativeHostState) {
		state.Connected = true
		state.ExtensionID = strings.TrimSpace(extensionID)
		state.ExtensionVersion = strings.TrimSpace(extensionVersion)
		state.Capabilities = append([]string(nil), capabilities...)
		state.LastHelloAt = time.Now().Format(time.RFC3339Nano)
		state.LastError = ""
	})
}

func (b *nativeHostBridge) markExtensionDisconnected() {
	b.updateState(func(state *nativeHostState) {
		state.Connected = false
		state.LastDisconnectAt = time.Now().Format(time.RFC3339Nano)
	})
}

func (b *nativeHostBridge) markError(message string) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return
	}
	b.updateState(func(state *nativeHostState) {
		state.LastError = trimmed
	})
}

func (b *nativeHostBridge) isExtensionConnected() bool {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	return b.state.Connected
}

func (b *nativeHostBridge) updateState(mutate func(state *nativeHostState)) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	mutate(&b.state)
	b.state.HostName = b.paths.HostName
	b.state.ProtocolVersion = NativeMessagingProtocolVersion
	b.state.UpdatedAt = time.Now().Format(time.RFC3339Nano)
	_ = writeNativeHostState(b.paths.StatePath, b.state)
}

func (b *nativeHostBridge) registerPending(id string, responseCh chan bridgeToolResponse) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	b.pending[id] = responseCh
}

func (b *nativeHostBridge) unregisterPending(id string) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	delete(b.pending, id)
}

func (b *nativeHostBridge) failPending(err error) {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	for id, responseCh := range b.pending {
		select {
		case responseCh <- bridgeToolResponse{ID: id, OK: false, Error: err.Error()}:
		default:
		}
	}
	b.pending = map[string]chan bridgeToolResponse{}
}

func listenNativeBridge(config internalconfig.BrowserConfig, paths NativeMessagingPaths) (net.Listener, string, string, func(), error) {
	if runtime.GOOS == "windows" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, "", "", nil, fmt.Errorf("启动浏览器桥接 TCP 监听失败: %w", err)
		}
		return listener, "tcp", listener.Addr().String(), func() {}, nil
	}
	if err := os.MkdirAll(paths.SocketDir, 0o755); err != nil {
		return nil, "", "", nil, fmt.Errorf("创建浏览器桥接 socket 目录失败: %w", err)
	}
	socketPath := compactUnixSocketPath(paths)
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, "", "", nil, fmt.Errorf("启动浏览器桥接 socket 失败: %w", err)
	}
	return listener, "unix", socketPath, func() { _ = os.Remove(socketPath) }, nil
}

func nativeBridgeDialTarget(paths NativeMessagingPaths, state nativeHostState) (string, string, bool) {
	network := strings.TrimSpace(state.SocketNetwork)
	address := strings.TrimSpace(state.SocketAddress)
	if network != "" && address != "" {
		return network, address, true
	}
	if runtime.GOOS == "windows" {
		return "", "", false
	}
	if strings.TrimSpace(paths.SocketPath) == "" {
		return "", "", false
	}
	return "unix", paths.SocketPath, true
}

func probeNativeBridge(config internalconfig.BrowserConfig, paths NativeMessagingPaths, state nativeHostState) bool {
	network, address, ok := nativeBridgeDialTarget(paths, state)
	if !ok {
		return false
	}
	conn, err := net.DialTimeout(network, address, browserTimeout(config))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func setConnDeadline(conn net.Conn, ctx context.Context, fallback time.Duration) {
	deadline := time.Now().Add(fallback)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
}

func nextBridgeRequestID() string {
	return "bridge-" + strconv.FormatInt(time.Now().UnixNano(), 10)
}

func compactUnixSocketPath(paths NativeMessagingPaths) string {
	if runtime.GOOS == "windows" {
		return paths.SocketPath
	}
	if len(paths.SocketPath) < 96 {
		return paths.SocketPath
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(paths.SocketPath))
	fileName := fmt.Sprintf("tflow-%x.sock", hasher.Sum64())
	return filepath.Join(os.TempDir(), fileName)
}

func cloneMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func mapValue(value map[string]any, key string) (map[string]any, bool) {
	if len(value) == 0 {
		return nil, false
	}
	raw, ok := value[key]
	if !ok || raw == nil {
		return nil, false
	}
	result, ok := raw.(map[string]any)
	if ok {
		return result, true
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, false
	}
	return decoded, true
}