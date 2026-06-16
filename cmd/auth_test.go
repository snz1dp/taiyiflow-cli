package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

func TestEnsureProviderAuthenticatedUsesPersistentToken(t *testing.T) {
	backendPrefix := internalconfig.DefaultBackendAPIPrefix
	authPrefix := "/custom-auth"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case backendPrefix + "/users/whoami/userinfo":
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(writer, `{"code":401,"message":"unauthorized"}`)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case authPrefix + "/sso/salt":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"salt-ticket","value":"salt-value"}}`)
		case authPrefix + "/sso":
			if got := request.URL.Query().Get("persistent_token"); got != "persist-1" {
				t.Fatalf("unexpected persistent token: %q", got)
			}
			if got := request.URL.Query().Get("salt_ticket"); got != "salt-ticket" {
				t.Fatalf("unexpected salt ticket: %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"userid":"u-1","access_token":"access-1","persistent_token":"persist-2","idle_duration":3600,"scope":"employee"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	config.AuthAPIPrefix = authPrefix
	config.PersistentToken = "persist-1"
	config.DeviceID = "device-1"
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	providerClient := buildProviderClient(config)
	updatedConfig, changed, err := app.ensureProviderAuthenticated(context.Background(), providerClient, config)
	if err != nil {
		t.Fatalf("ensureProviderAuthenticated failed: %v", err)
	}
	if !changed {
		t.Fatal("expected auth helper to update config")
	}
	if updatedConfig.AccessToken != "access-1" {
		t.Fatalf("expected api key to be refreshed, got %q", updatedConfig.AccessToken)
	}
	if updatedConfig.PersistentToken != "persist-2" {
		t.Fatalf("expected persistent token to rotate, got %q", updatedConfig.PersistentToken)
	}

	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.AccessToken != "access-1" || reloaded.PersistentToken != "persist-2" {
		t.Fatalf("expected auth state to persist, got %#v", reloaded)
	}

	currentUser, err := providerClient.GetCurrentUserInfo(context.Background())
	if err != nil {
		t.Fatalf("expected provider client to use refreshed api key, got %v", err)
	}
	if currentUser == nil || currentUser.Name != "张三" {
		t.Fatalf("unexpected current user after relogin: %#v", currentUser)
	}
}

func TestGenerateDeviceIDUsesMachineFingerprintSeed(t *testing.T) {
	originalHostname := deviceIDHostname
	originalInterfaces := deviceIDInterfaces
	originalReadFile := deviceIDReadFile
	originalRandomRead := deviceIDRandomRead
	originalPlatformUUID := deviceIDPlatformUUID
	resetDeviceIDCaches()
	t.Cleanup(func() {
		deviceIDHostname = originalHostname
		deviceIDInterfaces = originalInterfaces
		deviceIDReadFile = originalReadFile
		deviceIDRandomRead = originalRandomRead
		deviceIDPlatformUUID = originalPlatformUUID
		resetDeviceIDCaches()
	})

	deviceIDPlatformUUID = func() string { return "" }
	deviceIDHostname = func() (string, error) {
		return "taiyi-host", nil
	}
	deviceIDInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Flags: net.FlagUp, HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}},
			{Flags: net.FlagLoopback | net.FlagUp, HardwareAddr: net.HardwareAddr{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}},
		}, nil
	}
	deviceIDReadFile = func(path string) ([]byte, error) {
		if path == deviceIDMachineIDPaths[0] {
			return []byte("machine-123\n"), nil
		}
		return nil, os.ErrNotExist
	}
	deviceIDRandomRead = func(_ []byte) (int, error) {
		t.Fatal("expected machine fingerprint path without random fallback")
		return 0, nil
	}

	got, err := generateDeviceID()
	if err != nil {
		t.Fatalf("generateDeviceID failed: %v", err)
	}
	// machine_id 可用时，只使用 machine_id + goos/goarch，不混入 hostname 和 MAC。
	wantSeed := strings.Join([]string{
		"machine_id=machine-123",
		"goos=" + runtime.GOOS,
		"goarch=" + runtime.GOARCH,
	}, "|")
	want := buildDeviceIDFromSeed(wantSeed)
	if got != want {
		t.Fatalf("expected deterministic device id %q, got %q", want, got)
	}
}

func TestGenerateDeviceIDPrefersPlatformUUIDOverMachineID(t *testing.T) {
	originalHostname := deviceIDHostname
	originalInterfaces := deviceIDInterfaces
	originalReadFile := deviceIDReadFile
	originalRandomRead := deviceIDRandomRead
	originalPlatformUUID := deviceIDPlatformUUID
	resetDeviceIDCaches()
	t.Cleanup(func() {
		deviceIDHostname = originalHostname
		deviceIDInterfaces = originalInterfaces
		deviceIDReadFile = originalReadFile
		deviceIDRandomRead = originalRandomRead
		deviceIDPlatformUUID = originalPlatformUUID
		resetDeviceIDCaches()
	})

	deviceIDPlatformUUID = func() string { return "12345678-ABCD-EFGH-1234-123456789ABC" }
	deviceIDHostname = func() (string, error) {
		return "mac-host", nil
	}
	deviceIDInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Flags: net.FlagUp, HardwareAddr: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}},
		}, nil
	}
	deviceIDReadFile = func(path string) ([]byte, error) {
		if path == deviceIDMachineIDPaths[0] {
			return []byte("machine-123\n"), nil
		}
		return nil, os.ErrNotExist
	}
	deviceIDRandomRead = func(_ []byte) (int, error) {
		t.Fatal("expected platform UUID path without random fallback")
		return 0, nil
	}

	got, err := generateDeviceID()
	if err != nil {
		t.Fatalf("generateDeviceID failed: %v", err)
	}
	// platform_uuid 可用时，只使用 platform_uuid + goos/goarch，忽略 machine_id / hostname / macs。
	wantSeed := strings.Join([]string{
		"platform_uuid=12345678-ABCD-EFGH-1234-123456789ABC",
		"goos=" + runtime.GOOS,
		"goarch=" + runtime.GOARCH,
	}, "|")
	want := buildDeviceIDFromSeed(wantSeed)
	if got != want {
		t.Fatalf("expected deterministic device id %q, got %q", want, got)
	}
}

func TestGenerateDeviceIDUsesPlatformUUIDAloneOnMacOS(t *testing.T) {
	originalHostname := deviceIDHostname
	originalInterfaces := deviceIDInterfaces
	originalReadFile := deviceIDReadFile
	originalRandomRead := deviceIDRandomRead
	originalPlatformUUID := deviceIDPlatformUUID
	resetDeviceIDCaches()
	t.Cleanup(func() {
		deviceIDHostname = originalHostname
		deviceIDInterfaces = originalInterfaces
		deviceIDReadFile = originalReadFile
		deviceIDRandomRead = originalRandomRead
		deviceIDPlatformUUID = originalPlatformUUID
		resetDeviceIDCaches()
	})

	deviceIDPlatformUUID = func() string { return "MACOS-UUID-1234" }
	deviceIDHostname = func() (string, error) {
		return "", fmt.Errorf("hostname unavailable")
	}
	deviceIDInterfaces = func() ([]net.Interface, error) {
		return nil, fmt.Errorf("interfaces unavailable")
	}
	deviceIDReadFile = func(path string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	deviceIDRandomRead = func(_ []byte) (int, error) {
		t.Fatal("expected platform UUID path without random fallback")
		return 0, nil
	}

	got, err := generateDeviceID()
	if err != nil {
		t.Fatalf("generateDeviceID failed: %v", err)
	}
	wantSeed := strings.Join([]string{
		"platform_uuid=MACOS-UUID-1234",
		"goos=" + runtime.GOOS,
		"goarch=" + runtime.GOARCH,
	}, "|")
	want := buildDeviceIDFromSeed(wantSeed)
	if got != want {
		t.Fatalf("expected deterministic device id %q, got %q", want, got)
	}
}

func TestGenerateDeviceIDIsStableAcrossHostnameAndMACChanges(t *testing.T) {
	originalHostname := deviceIDHostname
	originalInterfaces := deviceIDInterfaces
	originalReadFile := deviceIDReadFile
	originalRandomRead := deviceIDRandomRead
	originalPlatformUUID := deviceIDPlatformUUID
	t.Cleanup(func() {
		deviceIDHostname = originalHostname
		deviceIDInterfaces = originalInterfaces
		deviceIDReadFile = originalReadFile
		deviceIDRandomRead = originalRandomRead
		deviceIDPlatformUUID = originalPlatformUUID
	})

	deviceIDPlatformUUID = func() string { return "STABLE-UUID-9999" }
	deviceIDReadFile = func(_ string) ([]byte, error) { return nil, os.ErrNotExist }
	deviceIDRandomRead = func(_ []byte) (int, error) {
		t.Fatal("expected platform UUID path without random fallback")
		return 0, nil
	}

	// 第一次：原始 hostname 和 MAC
	deviceIDHostname = func() (string, error) { return "old-host", nil }
	deviceIDInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Flags: net.FlagUp, HardwareAddr: net.HardwareAddr{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF}},
		}, nil
	}
	first, err := generateDeviceID()
	if err != nil {
		t.Fatalf("first generateDeviceID failed: %v", err)
	}

	// 第二次：hostname 和 MAC 都变了
	deviceIDHostname = func() (string, error) { return "new-host", nil }
	deviceIDInterfaces = func() ([]net.Interface, error) {
		return []net.Interface{
			{Flags: net.FlagUp, HardwareAddr: net.HardwareAddr{0x11, 0x22, 0x33, 0x44, 0x55, 0x66}},
			{Flags: net.FlagUp, HardwareAddr: net.HardwareAddr{0x77, 0x88, 0x99, 0xAA, 0xBB, 0xCC}},
		}, nil
	}
	second, err := generateDeviceID()
	if err != nil {
		t.Fatalf("second generateDeviceID failed: %v", err)
	}

	if first != second {
		t.Fatalf("device_id must be stable when platform_uuid is available: first=%q second=%q", first, second)
	}
}

func TestPersistAuthenticatedSessionGeneratesDeviceIDWhenMissing(t *testing.T) {
	var capturedDeviceID string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case authAPIPath("/logged/persistent_token"):
			capturedDeviceID = strings.TrimSpace(request.URL.Query().Get("device_id"))
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"persist-1"}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	config.BackendBaseURL = server.URL
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	providerClient := buildProviderClient(config)
	updatedConfig, err := app.persistAuthenticatedSession(context.Background(), providerClient, config, &providerclient.AuthSession{AccessToken: "access-1"}, true)
	if err != nil {
		t.Fatalf("persistAuthenticatedSession failed: %v", err)
	}
	if strings.TrimSpace(updatedConfig.DeviceID) == "" {
		t.Fatalf("expected generated device id, got %#v", updatedConfig)
	}
	if capturedDeviceID == "" || capturedDeviceID != updatedConfig.DeviceID {
		t.Fatalf("expected device id %q to be sent when creating persistent token, got %q", updatedConfig.DeviceID, capturedDeviceID)
	}
	if updatedConfig.PersistentToken != "persist-1" {
		t.Fatalf("expected persistent token to persist, got %#v", updatedConfig)
	}
	if reloaded, err := manager.Load(); err != nil {
		t.Fatalf("reload config failed: %v", err)
	} else if reloaded.DeviceID != updatedConfig.DeviceID {
		t.Fatalf("expected saved device id %q, got %#v", updatedConfig.DeviceID, reloaded)
	}
}

func TestEnsureClientIdentityGeneratesAndPersistsClientAndDeviceID(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	config := internalconfig.Default()
	if err := manager.Save(config); err != nil {
		t.Fatalf("save config failed: %v", err)
	}

	app := &App{configManager: manager}
	updatedConfig, changed, err := app.ensureClientIdentity(config)
	if err != nil {
		t.Fatalf("ensureClientIdentity failed: %v", err)
	}
	if !changed {
		t.Fatal("expected client identity helper to persist generated ids")
	}
	if strings.TrimSpace(updatedConfig.ClientID) == "" {
		t.Fatalf("expected generated client id, got %#v", updatedConfig)
	}
	if strings.TrimSpace(updatedConfig.DeviceID) == "" {
		t.Fatalf("expected generated device id, got %#v", updatedConfig)
	}

	reloaded, err := manager.Load()
	if err != nil {
		t.Fatalf("reload config failed: %v", err)
	}
	if reloaded.ClientID != updatedConfig.ClientID || reloaded.DeviceID != updatedConfig.DeviceID {
		t.Fatalf("expected generated client identity to persist, got %#v", reloaded)
	}

	againConfig, changed, err := app.ensureClientIdentity(reloaded)
	if err != nil {
		t.Fatalf("ensureClientIdentity second call failed: %v", err)
	}
	if changed {
		t.Fatalf("expected second ensureClientIdentity call to be idempotent, got %#v", againConfig)
	}
}
