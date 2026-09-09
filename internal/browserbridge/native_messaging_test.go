package browserbridge

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestInstallNativeMessagingHostWritesLauncherAndManifest(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())
	binaryPath := filepath.Join(t.TempDir(), "taiyiflow-cli")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write binary failed: %v", err)
	}
	extensionDir := filepath.Join(t.TempDir(), "browser-extension", "chrome")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatalf("mkdir extension dir failed: %v", err)
	}
	result, err := InstallNativeMessagingHost(internalconfig.BrowserConfig{Channel: internalconfig.BrowserChannelChrome}, binaryPath, extensionDir)
	if err != nil {
		t.Fatalf("InstallNativeMessagingHost failed: %v", err)
	}
	if _, err := os.Stat(result.LauncherPath); err != nil {
		t.Fatalf("expected launcher to exist, got %v", err)
	}
	expectedManifestPaths, err := resolveNativeMessagingManifestPaths(internalconfig.BrowserChannelChrome)
	if err != nil {
		t.Fatalf("resolveNativeMessagingManifestPaths failed: %v", err)
	}
	if len(result.ManifestPaths) != len(expectedManifestPaths) {
		t.Fatalf("expected %d manifest paths, got %#v", len(expectedManifestPaths), result.ManifestPaths)
	}
	seenManifestPaths := make(map[string]struct{}, len(result.ManifestPaths))
	for _, manifestPath := range result.ManifestPaths {
		seenManifestPaths[manifestPath] = struct{}{}
	}
	for _, manifestPath := range expectedManifestPaths {
		if _, ok := seenManifestPaths[manifestPath]; !ok {
			t.Fatalf("expected manifest path %q in install result, got %#v", manifestPath, result.ManifestPaths)
		}
		manifestBytes, readErr := os.ReadFile(manifestPath)
		if readErr != nil {
			t.Fatalf("read manifest %q failed: %v", manifestPath, readErr)
		}
		manifest := NativeMessagingManifest{}
		if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
			t.Fatalf("unmarshal manifest %q failed: %v", manifestPath, err)
		}
		if manifest.Name != NativeMessagingHostName {
			t.Fatalf("expected host name %q in %q, got %q", NativeMessagingHostName, manifestPath, manifest.Name)
		}
		if manifest.Path != result.LauncherPath {
			t.Fatalf("expected manifest launcher path %q in %q, got %q", result.LauncherPath, manifestPath, manifest.Path)
		}
		if len(manifest.AllowedOrigins) != 1 || manifest.AllowedOrigins[0] != "chrome-extension://"+ChromeExtensionID+"/" {
			t.Fatalf("unexpected allowed origins in %q: %#v", manifestPath, manifest.AllowedOrigins)
		}
	}
	status := DiagnoseNativeMessaging(internalconfig.BrowserConfig{Channel: internalconfig.BrowserChannelChrome}, extensionDir)
	if !status.ManifestInstalled || !status.LauncherInstalled {
		t.Fatalf("expected manifest and launcher installed, got %#v", status)
	}
	if !strings.Contains(status.Message, "清单: 已安装") {
		t.Fatalf("expected status message to mention manifest installation, got %q", status.Message)
	}
	if !strings.Contains(status.Message, "宿主: "+NativeMessagingHostName) {
		t.Fatalf("expected status message to mention host name, got %q", status.Message)
	}
	if result.SocketPath == "" {
		t.Fatalf("expected socket path to be populated, got %#v", result)
	}
}

func TestRunNativeMessagingHostRespondsToHello(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())
	stdin := &bytes.Buffer{}
	stdout := &bytes.Buffer{}
	request := nativeMessage{
		ID:               "hello-1",
		Type:             "hello",
		ExtensionID:      ChromeExtensionID,
		ExtensionVersion: "0.1.0",
		Capabilities:     []string{"tabs_context", "navigate"},
	}
	if err := writeNativeMessage(stdin, request); err != nil {
		t.Fatalf("write request failed: %v", err)
	}
	if err := RunNativeMessagingHost(context.Background(), internalconfig.BrowserConfig{Channel: internalconfig.BrowserChannelChrome}, stdin, stdout, ioDiscard{}); err != nil {
		t.Fatalf("RunNativeMessagingHost failed: %v", err)
	}
	response, err := readNativeMessage(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		t.Fatalf("read response failed: %v", err)
	}
	if response.Type != "hello_ack" || !response.OK {
		t.Fatalf("unexpected response: %#v", response)
	}
	paths, err := ResolveNativeMessagingPaths(internalconfig.BrowserConfig{Channel: internalconfig.BrowserChannelChrome}, "", "")
	if err != nil {
		t.Fatalf("resolve paths failed: %v", err)
	}
	state, ok := readNativeHostState(paths.StatePath)
	if !ok {
		t.Fatal("expected native host state to be written")
	}
	if strings.TrimSpace(state.ExtensionVersion) != "0.1.0" {
		t.Fatalf("expected extension version 0.1.0, got %#v", state)
	}
	if state.Connected {
		t.Fatalf("expected deferred disconnect state to be persisted, got %#v", state)
	}
	if strings.TrimSpace(state.LastHelloAt) == "" {
		t.Fatalf("expected last hello timestamp, got %#v", state)
	}
	if state.SocketReady {
		t.Fatalf("expected socket readiness to be cleared on shutdown, got %#v", state)
	}
}

func TestRunNativeMessagingHostBridgesToolRequestsOverSocket(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	hostDone := make(chan error, 1)
	go func() {
		hostDone <- RunNativeMessagingHost(context.Background(), internalconfig.BrowserConfig{Channel: internalconfig.BrowserChannelChrome}, stdinReader, stdoutWriter, ioDiscard{})
	}()
	paths, err := ResolveNativeMessagingPaths(internalconfig.BrowserConfig{Channel: internalconfig.BrowserChannelChrome}, "", "")
	if err != nil {
		t.Fatalf("resolve paths failed: %v", err)
	}
	if err := waitForNativeBridge(paths.StatePath, 3*time.Second); err != nil {
		t.Fatalf("waitForNativeBridge failed: %v", err)
	}
	extensionDone := make(chan error, 1)
	go func() {
		if err := writeNativeMessage(stdinWriter, nativeMessage{ID: "hello-1", Type: "hello", ExtensionID: ChromeExtensionID, ExtensionVersion: "0.1.0", Capabilities: []string{"navigate"}}); err != nil {
			extensionDone <- err
			return
		}
		helloAck, err := readNativeMessage(stdoutReader)
		if err != nil {
			extensionDone <- err
			return
		}
		if helloAck.Type != "hello_ack" || !helloAck.OK {
			extensionDone <- fmt.Errorf("unexpected hello ack: %#v", helloAck)
			return
		}
		request, err := readNativeMessage(stdoutReader)
		if err != nil {
			extensionDone <- err
			return
		}
		if request.Type != "tool_request" {
			extensionDone <- fmt.Errorf("unexpected host request: %#v", request)
			return
		}
		payload := request.Payload
		if strings.TrimSpace(stringValue(payload["tool"])) != "taiyiflow-in-chrome__navigate" {
			extensionDone <- fmt.Errorf("unexpected tool payload: %#v", payload)
			return
		}
		if err := writeNativeMessage(stdinWriter, nativeMessage{ID: request.ID, Type: "tool_result", OK: true, Payload: map[string]any{"result": map[string]any{"tab_id": "tab-1", "url": "https://example.com"}}}); err != nil {
			extensionDone <- err
			return
		}
		_ = stdinWriter.Close()
		extensionDone <- nil
	}()
	result, err := ExecuteNativeMessagingTool(context.Background(), internalconfig.BrowserConfig{Mode: internalconfig.BrowserModeChromeSession, Channel: internalconfig.BrowserChannelChrome}, "taiyiflow-in-chrome__navigate", map[string]any{"url": "https://example.com"})
	if err != nil {
		t.Fatalf("ExecuteNativeMessagingTool failed: %v", err)
	}
	if got := strings.TrimSpace(stringValue(result["tab_id"])); got != "tab-1" {
		t.Fatalf("expected tab_id tab-1, got %#v", result)
	}
	if err := <-extensionDone; err != nil {
		t.Fatalf("extension simulation failed: %v", err)
	}
	if err := <-hostDone; err != nil {
		t.Fatalf("host exited with error: %v", err)
	}
}

func TestRunNativeMessagingHostStopsOnContextCancel(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())
	stdinReader, _ := io.Pipe()
	stdout := &bytes.Buffer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hostDone := make(chan error, 1)
	go func() {
		hostDone <- RunNativeMessagingHost(ctx, internalconfig.BrowserConfig{Channel: internalconfig.BrowserChannelChrome}, stdinReader, stdout, ioDiscard{})
	}()
	paths, err := ResolveNativeMessagingPaths(internalconfig.BrowserConfig{Channel: internalconfig.BrowserChannelChrome}, "", "")
	if err != nil {
		t.Fatalf("resolve paths failed: %v", err)
	}
	if err := waitForNativeBridge(paths.StatePath, 3*time.Second); err != nil {
		t.Fatalf("waitForNativeBridge failed: %v", err)
	}
	cancel()
	select {
	case err := <-hostDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context cancellation, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected native host to exit after context cancellation")
	}
}

func TestRunNativeMessagingHostExitsOnIdleTimeout(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())
	stdinReader, stdinWriter := io.Pipe()
	stdout := &bytes.Buffer{}
	config := internalconfig.BrowserConfig{
		Channel:                internalconfig.BrowserChannelChrome,
		HostIdleTimeoutSeconds: 1,
	}
	hostDone := make(chan error, 1)
	go func() {
		hostDone <- RunNativeMessagingHost(context.Background(), config, stdinReader, stdout, ioDiscard{})
	}()
	paths, err := ResolveNativeMessagingPaths(config, "", "")
	if err != nil {
		t.Fatalf("resolve paths failed: %v", err)
	}
	if err := waitForNativeBridge(paths.StatePath, 3*time.Second); err != nil {
		t.Fatalf("waitForNativeBridge failed: %v", err)
	}
	// 无 socket 连接，等待空闲超时退出
	select {
	case err := <-hostDone:
		if err != nil {
			t.Fatalf("expected nil error on idle timeout, got %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = stdinWriter.Close()
		t.Fatal("expected native host to exit on idle timeout (no socket connections)")
	}
	_ = stdinWriter.Close()
	state, ok := readNativeHostState(paths.StatePath)
	if !ok {
		t.Fatal("expected native host state to be written after idle timeout exit")
	}
	if state.SocketReady {
		t.Fatalf("expected socket readiness to be cleared on idle timeout exit, got %#v", state)
	}
	if !strings.Contains(state.LastError, "空闲超时") {
		t.Fatalf("expected last error to mention idle timeout, got %q", state.LastError)
	}
}

func TestRunNativeMessagingHostKeepsAliveWithSocketConnection(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	config := internalconfig.BrowserConfig{
		Channel:                internalconfig.BrowserChannelChrome,
		HostIdleTimeoutSeconds: 2,
	}
	hostDone := make(chan error, 1)
	go func() {
		hostDone <- RunNativeMessagingHost(context.Background(), config, stdinReader, stdoutWriter, ioDiscard{})
	}()
	paths, err := ResolveNativeMessagingPaths(config, "", "")
	if err != nil {
		t.Fatalf("resolve paths failed: %v", err)
	}
	if err := waitForNativeBridge(paths.StatePath, 3*time.Second); err != nil {
		t.Fatalf("waitForNativeBridge failed: %v", err)
	}
	// Send hello so extension is marked connected (required for tool requests)
	if err := writeNativeMessage(stdinWriter, nativeMessage{
		ID: "hello-1", Type: "hello", ExtensionID: ChromeExtensionID, ExtensionVersion: "0.1.0",
	}); err != nil {
		t.Fatalf("write hello failed: %v", err)
	}
	helloAck, err := readNativeMessage(stdoutReader)
	if err != nil {
		t.Fatalf("read hello ack failed: %v", err)
	}
	if helloAck.Type != "hello_ack" || !helloAck.OK {
		t.Fatalf("unexpected hello ack: %#v", helloAck)
	}
	// Establish a socket connection (simulating a CLI client) and keep it open.
	// This should prevent the idle timer from firing.
	state, _ := readNativeHostState(paths.StatePath)
	network, address, ok := nativeBridgeDialTarget(paths, state)
	if !ok {
		t.Fatal("expected native bridge dial target to be available")
	}
	conn, err := net.DialTimeout(network, address, 3*time.Second)
	if err != nil {
		t.Fatalf("dial native bridge failed: %v", err)
	}
	defer conn.Close()
	// Wait beyond the 2s idle timeout — host should still be alive because a socket connection exists
	time.Sleep(2500 * time.Millisecond)
	select {
	case err := <-hostDone:
		t.Fatalf("host exited prematurely with socket connection active: %v", err)
	default:
	}
	// Close the socket connection — now no connections remain, idle timer should fire
	conn.Close()
	select {
	case err := <-hostDone:
		if err != nil {
			t.Fatalf("expected nil error after idle timeout, got %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = stdinWriter.Close()
		t.Fatal("expected host to exit after socket disconnect and idle timeout")
	}
	_ = stdinWriter.Close()
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}

func TestWriteAndReadNativeMessageRoundTrip(t *testing.T) {
	buffer := &bytes.Buffer{}
	message := nativeMessage{ID: "ping-1", Type: "ping", OK: true}
	if err := writeNativeMessage(buffer, message); err != nil {
		t.Fatalf("writeNativeMessage failed: %v", err)
	}
	if buffer.Len() < 4 {
		t.Fatalf("expected framed message, got %d bytes", buffer.Len())
	}
	payloadSize := binary.LittleEndian.Uint32(buffer.Bytes()[:4])
	if payloadSize == 0 {
		t.Fatal("expected non-empty payload size")
	}
	decoded, err := readNativeMessage(bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatalf("readNativeMessage failed: %v", err)
	}
	if decoded.ID != message.ID || decoded.Type != message.Type {
		t.Fatalf("unexpected decoded message: %#v", decoded)
	}
}

func waitForNativeBridge(statePath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, ok := readNativeHostState(statePath)
		if ok && state.SocketReady && strings.TrimSpace(state.SocketAddress) != "" {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("socket listener 未在 %s 内就绪", timeout)
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
