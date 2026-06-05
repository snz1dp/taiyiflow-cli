package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider"
	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/workspaceinstance"
)

const (
	primaryClientMessageType     = "primary_cli"
	primaryClientConnectionRole  = "primary_cli"
	primaryClientEventReady      = "connection.ready"
	primaryClientEventError      = "connection.error"
	primaryClientEventPing       = "connection.ping"
	primaryClientEventPong       = "connection.pong"
	primaryClientEventSession    = "session.update"
	primaryClientEventDispatch   = "primary_task.dispatch"
	defaultPrimaryReconnectDelay = 3 * time.Second
	defaultPrimaryPingInterval   = 45 * time.Second
)

type primaryClientEnvelope struct {
	Type    string          `json:"type"`
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload"`
}

type primaryTaskDispatch struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
}

type primaryClientConfigRef struct {
	mu    sync.RWMutex
	value internalconfig.Config
}

func newPrimaryClientConfigRef(config internalconfig.Config) *primaryClientConfigRef {
	return &primaryClientConfigRef{value: config}
}

func (r *primaryClientConfigRef) Load() internalconfig.Config {
	if r == nil {
		return internalconfig.Default()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.value
}

func (r *primaryClientConfigRef) Store(config internalconfig.Config) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.value = config
	r.mu.Unlock()
}

type primaryClientOptions struct {
	ReconnectDelay time.Duration
	PingInterval   time.Duration
	OnDispatch     func(primaryTaskDispatch)
	Logf           func(format string, args ...any)
}

type primaryClientManager struct {
	configRef      *primaryClientConfigRef
	lease          *workspaceinstance.Lease
	reconnectDelay time.Duration
	pingInterval   time.Duration
	onDispatch     func(primaryTaskDispatch)
	logf           func(format string, args ...any)
	writeMu        sync.Mutex
	sessionMu      sync.RWMutex
	sessionID      string
	sessionUpdates chan string
}

func newPrimaryClientManager(configRef *primaryClientConfigRef, lease *workspaceinstance.Lease, sessionID string, options primaryClientOptions) *primaryClientManager {
	reconnectDelay := options.ReconnectDelay
	if reconnectDelay <= 0 {
		reconnectDelay = defaultPrimaryReconnectDelay
	}
	pingInterval := options.PingInterval
	if pingInterval <= 0 {
		pingInterval = defaultPrimaryPingInterval
	}
	logf := options.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &primaryClientManager{
		configRef:      configRef,
		lease:          lease,
		reconnectDelay: reconnectDelay,
		pingInterval:   pingInterval,
		onDispatch:     options.OnDispatch,
		logf:           logf,
		sessionID:      strings.TrimSpace(sessionID),
		sessionUpdates: make(chan string, 1),
	}
}

func (m *primaryClientManager) CurrentSessionID() string {
	if m == nil {
		return ""
	}
	m.sessionMu.RLock()
	defer m.sessionMu.RUnlock()
	return m.sessionID
}

func (m *primaryClientManager) UpdateSession(sessionID string) {
	if m == nil {
		return
	}
	trimmedSessionID := strings.TrimSpace(sessionID)
	m.sessionMu.Lock()
	if m.sessionID == trimmedSessionID {
		m.sessionMu.Unlock()
		return
	}
	m.sessionID = trimmedSessionID
	m.sessionMu.Unlock()
	select {
	case m.sessionUpdates <- trimmedSessionID:
	default:
		select {
		case <-m.sessionUpdates:
		default:
		}
		select {
		case m.sessionUpdates <- trimmedSessionID:
		default:
		}
	}
}

func (m *primaryClientManager) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := m.runConnection(ctx)
		if err == nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
		} else if ctx.Err() != nil {
			return ctx.Err()
		} else {
			m.logf("主 CLI 长连接中断: %v", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.reconnectDelay):
		}
	}
}

func (m *primaryClientManager) runConnection(ctx context.Context) error {
	if m == nil || m.lease == nil {
		return fmt.Errorf("主 CLI 实例信息不可用")
	}
	config := m.configRef.Load()
	wsURL, err := buildPrimaryClientWebSocketURL(config, m.lease.InstanceID, m.CurrentSessionID())
	if err != nil {
		return err
	}
	headers := http.Header{}
	if token := strings.TrimSpace(config.AccessToken); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	dialer := ws.Dialer{Header: ws.HandshakeHeaderHTTP(headers)}
	conn, _, _, err := dialer.Dial(ctx, wsURL)
	if err != nil {
		return fmt.Errorf("连接主 CLI 长连接失败: %w", err)
	}
	defer conn.Close()
	stopInterruptRead := interruptPrimaryConnectionOnCancel(ctx, conn)
	defer stopInterruptRead()

	pingCtx, pingCancel := context.WithCancel(ctx)
	defer pingCancel()
	go m.pingLoop(pingCtx, conn)
	go m.sessionUpdateLoop(pingCtx, conn)

	for {
		payload, opCode, readErr := wsutil.ReadServerData(conn)
		if readErr != nil {
			return readErr
		}
		if opCode != ws.OpText {
			continue
		}
		if handleErr := m.handleServerEnvelope(conn, payload); handleErr != nil {
			return handleErr
		}
	}
}

func (m *primaryClientManager) handleServerEnvelope(conn net.Conn, raw []byte) error {
	var envelope primaryClientEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("解析主 CLI 长连接消息失败: %w", err)
	}
	if strings.TrimSpace(envelope.Type) != primaryClientMessageType {
		return nil
	}
	switch strings.TrimSpace(envelope.Event) {
	case primaryClientEventReady:
		return nil
	case primaryClientEventError:
		message := "服务端返回主 CLI 连接错误"
		if len(envelope.Payload) > 0 {
			payload := map[string]any{}
			if err := json.Unmarshal(envelope.Payload, &payload); err == nil {
				if typedMessage, ok := payload["message"].(string); ok && strings.TrimSpace(typedMessage) != "" {
					message = strings.TrimSpace(typedMessage)
				}
			}
		}
		return fmt.Errorf("%s", message)
	case primaryClientEventPing:
		return m.sendEnvelope(conn, primaryClientEventPong, map[string]any{})
	case primaryClientEventPong:
		return nil
	case primaryClientEventDispatch:
		payload := primaryTaskDispatch{}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
			return fmt.Errorf("解析主任务下发消息失败: %w", err)
		}
		if strings.TrimSpace(payload.Prompt) == "" {
			return nil
		}
		if m.onDispatch != nil {
			m.onDispatch(payload)
		}
	}
	return nil
}

func (m *primaryClientManager) pingLoop(ctx context.Context, conn net.Conn) {
	ticker := time.NewTicker(m.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.sendEnvelope(conn, primaryClientEventPing, map[string]any{}); err != nil {
				m.logf("主 CLI 心跳发送失败: %v", err)
				return
			}
		}
	}
}

func (m *primaryClientManager) sessionUpdateLoop(ctx context.Context, conn net.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		case sessionID := <-m.sessionUpdates:
			if err := m.sendEnvelope(conn, primaryClientEventSession, map[string]any{
				"instance_id": m.lease.InstanceID,
				"session_id":  strings.TrimSpace(sessionID),
			}); err != nil {
				m.logf("主 CLI session 同步失败: %v", err)
				return
			}
		}
	}
}

func (m *primaryClientManager) sendEnvelope(conn net.Conn, event string, payload map[string]any) error {
	raw, err := json.Marshal(map[string]any{
		"type":    primaryClientMessageType,
		"event":   strings.TrimSpace(event),
		"payload": payload,
	})
	if err != nil {
		return fmt.Errorf("编码主 CLI 长连接消息失败: %w", err)
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	if err := wsutil.WriteClientText(conn, raw); err != nil {
		return fmt.Errorf("发送主 CLI 长连接消息失败: %w", err)
	}
	return nil
}

func buildPrimaryClientWebSocketURL(config internalconfig.Config, instanceID string, sessionID string) (string, error) {
	baseURL, err := url.Parse(strings.TrimSpace(config.BackendBaseURL))
	if err != nil {
		return "", fmt.Errorf("解析后台服务地址失败: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(baseURL.Scheme)) {
	case "https":
		baseURL.Scheme = "wss"
	case "http":
		baseURL.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", fmt.Errorf("不支持的后台服务地址协议: %s", baseURL.Scheme)
	}
	baseURL.Path = joinPrimaryClientURLPath(baseURL.Path, internalconfig.NormalizeBackendAPIPrefix(config.BackendAPIPrefix), "/agents/client/websocket")
	queryValues := baseURL.Query()
	queryValues.Set("client_id", strings.TrimSpace(config.ClientID))
	queryValues.Set("device_id", strings.TrimSpace(config.DeviceID))
	queryValues.Set("client_type", provider.ClientTypeCLI)
	queryValues.Set("device_type", goruntime.GOOS)
	queryValues.Set("connection_role", primaryClientConnectionRole)
	queryValues.Set("instance_id", strings.TrimSpace(instanceID))
	if strings.TrimSpace(sessionID) != "" {
		queryValues.Set("session_id", strings.TrimSpace(sessionID))
	}
	baseURL.RawQuery = queryValues.Encode()
	return baseURL.String(), nil
}

func joinPrimaryClientURLPath(parts ...string) string {
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		segments = append(segments, strings.Trim(trimmed, "/"))
	}
	joined := strings.Join(segments, "/")
	if joined == "" {
		return "/"
	}
	return "/" + joined
}

func interruptPrimaryConnectionOnCancel(ctx context.Context, conn net.Conn) func() {
	if ctx == nil || conn == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	return func() {
		close(done)
	}
}