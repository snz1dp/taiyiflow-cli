package cmd

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
	providerclient "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/provider/taiyiflow"
)

var (
	deviceIDHostname     = os.Hostname
	deviceIDInterfaces   = net.Interfaces
	deviceIDReadFile     = os.ReadFile
	deviceIDRandomRead   = cryptorand.Read
	clientIDRandomRead   = cryptorand.Read
	deviceIDPlatformUUID func() string // 测试用 mock；生产环境由平台文件实现

	// 进程级缓存：确保同一进程内多次调用 readPlatformUUIDSeed / readMachineIDSeed
	// 返回完全一致的结果，防止 ioreg 间歇性失败等瞬态问题导致 seed 变化。
	deviceIDPlatformUUIDOnce  sync.Once
	deviceIDPlatformUUIDCache string
	deviceIDMachineIDOnce     sync.Once
	deviceIDMachineIDCache    string
)

// resetDeviceIDCaches 清空设备标识相关的进程级缓存，仅供测试使用。
func resetDeviceIDCaches() {
	deviceIDPlatformUUIDOnce = sync.Once{}
	deviceIDPlatformUUIDCache = ""
	deviceIDMachineIDOnce = sync.Once{}
	deviceIDMachineIDCache = ""
}

var deviceIDMachineIDPaths = []string{
	"/etc/machine-id",
	"/var/lib/dbus/machine-id",
	"/sys/class/dmi/id/product_uuid",
}

const deviceIDNamespace = "taiyiflow-cli/device-id/v1"

func (a *App) ensureClientIdentity(config internalconfig.Config) (internalconfig.Config, bool, error) {
	changed := false
	if strings.TrimSpace(config.DeviceID) == "" {
		deviceID, err := generateDeviceID()
		if err != nil {
			return config, false, err
		}
		config.DeviceID = deviceID
		changed = true
	}
	if strings.TrimSpace(config.ClientID) == "" {
		clientID, err := generateClientID()
		if err != nil {
			return config, changed, err
		}
		config.ClientID = clientID
		changed = true
	}
	if !changed || a == nil || a.configManager == nil {
		return config, changed, nil
	}
	if err := a.configManager.Save(config); err != nil {
		return config, true, err
	}
	return config, true, nil
}

func (a *App) ensureProviderAuthenticated(
	ctx context.Context,
	providerClient *providerclient.Client,
	config internalconfig.Config,
) (internalconfig.Config, bool, error) {
	if _, err := providerClient.GetCurrentUserInfo(ctx); err == nil {
		return config, false, nil
	} else if !shouldAttemptPersistentLogin(config, err) {
		return config, false, nil
	}

	updatedConfig, changed, err := a.tryPersistentLogin(ctx, providerClient, config)
	if err != nil {
		return updatedConfig, changed, err
	}
	return updatedConfig, changed, nil
}

func (a *App) tryPersistentLogin(
	ctx context.Context,
	providerClient *providerclient.Client,
	config internalconfig.Config,
) (internalconfig.Config, bool, error) {
	persistentToken := strings.TrimSpace(config.PersistentToken)
	if persistentToken == "" {
		return config, false, nil
	}

	changed := false
	deviceID := strings.TrimSpace(config.DeviceID)
	if deviceID == "" {
		generatedDeviceID, err := generateDeviceID()
		if err != nil {
			return config, false, err
		}
		config.DeviceID = generatedDeviceID
		deviceID = generatedDeviceID
		changed = true
	}

	authClient := buildAuthClient(config)
	session, err := authClient.ExchangePersistentToken(ctx, persistentToken, deviceID)
	if err != nil {
		if shouldClearPersistentLogin(err) {
			config, changed = clearAuthenticationState(config, false)
			if changed {
				if saveErr := a.configManager.Save(config); saveErr != nil {
					return config, true, saveErr
				}
			}
			providerClient.SetAPIKey("")
			return config, changed, nil
		}
		return config, changed, err
	}

	if updatedToken := strings.TrimSpace(session.AccessToken); updatedToken != strings.TrimSpace(config.AccessToken) {
		config.AccessToken = updatedToken
		providerClient.SetAPIKey(updatedToken)
		changed = true
	}
	if rotatedPersistentToken := strings.TrimSpace(session.PersistentToken); rotatedPersistentToken != "" && rotatedPersistentToken != persistentToken {
		config.PersistentToken = rotatedPersistentToken
		changed = true
	}
	authUpdatedAt := time.Now().UTC().Format(time.RFC3339)
	if config.AuthUpdatedAt != authUpdatedAt {
		config.AuthUpdatedAt = authUpdatedAt
		changed = true
	}

	if changed {
		if err := a.configManager.Save(config); err != nil {
			return config, true, err
		}
	}
	return config, changed, nil
}

func (a *App) persistAuthenticatedSession(
	ctx context.Context,
	providerClient *providerclient.Client,
	config internalconfig.Config,
	session *providerclient.AuthSession,
	remember bool,
) (internalconfig.Config, error) {
	if session == nil || strings.TrimSpace(session.AccessToken) == "" {
		return config, fmt.Errorf("登录成功但未返回 access_token")
	}

	config.AccessToken = strings.TrimSpace(session.AccessToken)
	config.AuthUpdatedAt = time.Now().UTC().Format(time.RFC3339)

	if remember {
		deviceID := strings.TrimSpace(config.DeviceID)
		if deviceID == "" {
			generatedDeviceID, err := generateDeviceID()
			if err != nil {
				return config, err
			}
			deviceID = generatedDeviceID
			config.DeviceID = generatedDeviceID
		}

		persistentToken := strings.TrimSpace(session.PersistentToken)
		if persistentToken == "" {
			authClient := buildAuthClient(config)
			createdToken, err := authClient.CreatePersistentToken(ctx, config.AccessToken, deviceID)
			if err != nil {
				return config, err
			}
			persistentToken = strings.TrimSpace(createdToken.Ticket)
		}
		config.PersistentToken = persistentToken
	} else {
		config, _ = clearAuthenticationState(config, false)
		config.AccessToken = strings.TrimSpace(session.AccessToken)
		config.AuthUpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	if err := a.configManager.Save(config); err != nil {
		return config, err
	}
	providerClient.SetAPIKey(config.AccessToken)
	return config, nil
}

func clearAuthenticationState(config internalconfig.Config, clearDeviceID bool) (internalconfig.Config, bool) {
	changed := false
	if strings.TrimSpace(config.AccessToken) != "" {
		config.AccessToken = ""
		changed = true
	}
	if strings.TrimSpace(config.PersistentToken) != "" {
		config.PersistentToken = ""
		changed = true
	}
	if strings.TrimSpace(config.AuthUpdatedAt) != "" {
		config.AuthUpdatedAt = ""
		changed = true
	}
	if clearDeviceID && strings.TrimSpace(config.DeviceID) != "" {
		config.DeviceID = ""
		changed = true
	}
	return config, changed
}

func generateDeviceID() (string, error) {
	seed, err := collectDeviceIDSeed()
	if err != nil {
		return "", err
	}
	return buildDeviceIDFromSeed(seed), nil
}

func collectDeviceIDSeed() (string, error) {
	goParts := []string{"goos=" + runtime.GOOS, "goarch=" + runtime.GOARCH}

	// 优先级 1：平台硬件标识（IOPlatformUUID / DMI Product UUID / MachineGuid）。
	// 这些是主板或系统级别的唯一标识，不会因网络环境、主机名变更而改变。
	// 一旦可用，直接使用它 + goos/goarch，不再混入易变的 hostname 和 MAC 地址。
	if platformUUID := readPlatformUUIDSeed(); platformUUID != "" {
		return strings.Join(append([]string{"platform_uuid=" + platformUUID}, goParts...), "|"), nil
	}

	// 优先级 2：machine-id（Linux 的 /etc/machine-id 等）。
	// 由系统在首次安装时生成，重装前不变，同样独立于网络环境。
	if machineID := readMachineIDSeed(); machineID != "" {
		return strings.Join(append([]string{"machine_id=" + machineID}, goParts...), "|"), nil
	}

	// 优先级 3：hostname + MAC 地址组合（降级方案）。
	// 这些标识可能变化，但在没有更稳定标识时作为最佳可用指纹。
	seedParts := make([]string, 0, 6)
	if hostname, err := deviceIDHostname(); err == nil {
		trimmedHostname := strings.TrimSpace(hostname)
		if trimmedHostname != "" {
			seedParts = append(seedParts, "hostname="+trimmedHostname)
		}
	}
	if macAddresses, err := readMACAddressSeed(); err == nil && len(macAddresses) > 0 {
		seedParts = append(seedParts, "macs="+strings.Join(macAddresses, ","))
	}
	seedParts = append(seedParts, goParts...)

	// 优先级 4：完全无标识可用时，使用随机值（每次不同，但会被持久化到配置文件）。
	if len(seedParts) == 2 {
		raw := make([]byte, 16)
		if _, err := deviceIDRandomRead(raw); err != nil {
			return "", fmt.Errorf("生成设备标识失败: %w", err)
		}
		seedParts = append([]string{"random=" + hex.EncodeToString(raw)}, seedParts...)
	}
	return strings.Join(seedParts, "|"), nil
}

func readMachineIDSeed() string {
	deviceIDMachineIDOnce.Do(func() {
		for _, path := range deviceIDMachineIDPaths {
			payload, err := deviceIDReadFile(path)
			if err != nil {
				continue
			}
			trimmedPayload := strings.TrimSpace(string(payload))
			if trimmedPayload != "" {
				deviceIDMachineIDCache = trimmedPayload
				return
			}
		}
	})
	return deviceIDMachineIDCache
}

func readMACAddressSeed() ([]string, error) {
	interfaces, err := deviceIDInterfaces()
	if err != nil {
		return nil, err
	}
	addresses := make([]string, 0, len(interfaces))
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if len(iface.HardwareAddr) == 0 {
			continue
		}
		addresses = append(addresses, strings.ToLower(iface.HardwareAddr.String()))
	}
	sort.Strings(addresses)
	return addresses, nil
}

func buildDeviceIDFromSeed(seed string) string {
	sum := sha256.Sum256([]byte(deviceIDNamespace + "|" + strings.TrimSpace(seed)))
	return hex.EncodeToString(sum[:16])
}

func generateClientID() (string, error) {
	raw := make([]byte, 16)
	if _, err := clientIDRandomRead(raw); err != nil {
		return "", fmt.Errorf("生成客户端标识失败: %w", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("cli-%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

func shouldAttemptPersistentLogin(config internalconfig.Config, err error) bool {
	if strings.TrimSpace(config.PersistentToken) == "" {
		return false
	}
	if err == nil {
		return false
	}
	return isAuthenticationError(err)
}

func shouldClearPersistentLogin(err error) bool {
	var authErr *providerclient.AuthError
	if errors.As(err, &authErr) {
		if authErr.StatusCode == httpStatusUnauthorized || authErr.StatusCode == httpStatusForbidden {
			return true
		}
		if authErr.Code != 0 {
			return true
		}
	}
	return isAuthenticationError(err)
}

const (
	httpStatusUnauthorized = 401
	httpStatusForbidden    = 403
)

func isAuthenticationError(err error) bool {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if message == "" {
		return false
	}
	switch {
	case strings.Contains(message, "401"), strings.Contains(message, "403"):
		return true
	case strings.Contains(message, "unauthorized"), strings.Contains(message, "forbidden"):
		return true
	case strings.Contains(message, "请重新登录"), strings.Contains(message, "需要登录"):
		return true
	case strings.Contains(message, "登录"), strings.Contains(message, "token") && strings.Contains(message, "失效"):
		return true
	default:
		return false
	}
}
