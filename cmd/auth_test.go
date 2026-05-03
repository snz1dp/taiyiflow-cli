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
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/taiyi/composer/api/v1/users/whoami/userinfo":
			if got := request.Header.Get("Authorization"); got != "Bearer access-1" {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(writer, `{"code":401,"message":"unauthorized"}`)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"name":"张三","user_id":"u-1","user_name":"zhangsan"}`)
		case "/xeai/sso/salt":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"code":0,"message":"OK","data":{"ticket":"salt-ticket","value":"salt-value"}}`)
		case "/xeai/sso":
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
	t.Cleanup(func() {
		deviceIDHostname = originalHostname
		deviceIDInterfaces = originalInterfaces
		deviceIDReadFile = originalReadFile
		deviceIDRandomRead = originalRandomRead
	})

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
	wantSeed := strings.Join([]string{
		"machine_id=machine-123",
		"hostname=taiyi-host",
		"macs=00:11:22:33:44:55",
		"goos=" + runtime.GOOS,
		"goarch=" + runtime.GOARCH,
	}, "|")
	want := buildDeviceIDFromSeed(wantSeed)
	if got != want {
		t.Fatalf("expected deterministic device id %q, got %q", want, got)
	}
}

func TestPersistAuthenticatedSessionGeneratesDeviceIDWhenMissing(t *testing.T) {
	var capturedDeviceID string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/xeai/logged/persistent_token":
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
