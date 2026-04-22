package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultRootDirName              = ".snz1dp"
	defaultConfigRel                = "config/taiyiflow.yaml"
	rootDirEnvName                  = "SNZ1DP_HOME"
	DefaultStreamIdleTimeoutSeconds = 300
	SandboxEscapePolicyConfirm      = "confirm"
	SandboxEscapePolicyDeny         = "deny"
	SandboxEscapePolicyAllow        = "allow"
)

type Config struct {
	BackendBaseURL           string      `yaml:"backend_base_url"`
	APIKey                   string      `yaml:"api_key,omitempty"`
	DefaultFlow              string      `yaml:"default_flow,omitempty"`
	ApprovalMode             string      `yaml:"approval_mode"`
	Workdir                  string      `yaml:"workdir,omitempty"`
	SandboxEscapePolicy      string      `yaml:"sandbox_escape_policy,omitempty"`
	StreamIdleTimeoutSeconds int         `yaml:"stream_idle_timeout_seconds,omitempty"`
	OutputLanguage           string      `yaml:"output_language"`
	AuditEnabled             bool        `yaml:"audit_enabled,omitempty"`
	SandboxRoots             []string    `yaml:"sandbox_roots,omitempty"`
	Skills                   SkillConfig `yaml:"skills,omitempty"`
}

type SkillConfig struct {
	Directory string   `yaml:"directory,omitempty"`
	Enabled   []string `yaml:"enabled,omitempty"`
}

type Manager struct {
	path string
}

func Default() Config {
	return Config{
		BackendBaseURL:           "http://localhost:7860",
		ApprovalMode:             "confirm",
		SandboxEscapePolicy:      SandboxEscapePolicyConfirm,
		StreamIdleTimeoutSeconds: DefaultStreamIdleTimeoutSeconds,
		OutputLanguage:           "zh-CN",
		AuditEnabled:             false,
	}
}

func NormalizeSandboxEscapePolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SandboxEscapePolicyDeny:
		return SandboxEscapePolicyDeny
	case SandboxEscapePolicyAllow:
		return SandboxEscapePolicyAllow
	default:
		return SandboxEscapePolicyConfirm
	}
}

func NormalizeStreamIdleTimeoutSeconds(value int) int {
	if value < 0 {
		return DefaultStreamIdleTimeoutSeconds
	}
	return value
}

func NewManager() (*Manager, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	return &Manager{path: path}, nil
}

func DefaultRootDir() (string, error) {
	if configured := filepath.Clean(strings.TrimSpace(os.Getenv(rootDirEnvName))); configured != "." && configured != "" {
		if filepath.IsAbs(configured) {
			return configured, nil
		}
		absPath, err := filepath.Abs(configured)
		if err != nil {
			return "", fmt.Errorf("解析 %s 失败: %w", rootDirEnvName, err)
		}
		return absPath, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}
	return filepath.Join(homeDir, defaultRootDirName), nil
}

func RootDirEnvName() string {
	return rootDirEnvName
}

func DefaultConfigPath() (string, error) {
	rootDir, err := DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(rootDir, defaultConfigRel), nil
}

func DefaultSkillsDir() (string, error) {
	rootDir, err := DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(rootDir, "skills"), nil
}

func DefaultAuditLogPath() (string, error) {
	rootDir, err := DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(rootDir, "logs", "audit.log"), nil
}

func (m *Manager) Path() string {
	return m.path
}

func (m *Manager) Exists() bool {
	_, err := os.Stat(m.path)
	return err == nil
}

func (m *Manager) Load() (Config, error) {
	config := Default()

	content, err := os.ReadFile(m.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return config, nil
		}
		return Config{}, fmt.Errorf("读取配置失败: %w", err)
	}

	if err := yaml.Unmarshal(content, &config); err != nil {
		return Config{}, fmt.Errorf("解析配置失败: %w", err)
	}

	if config.BackendBaseURL == "" {
		config.BackendBaseURL = Default().BackendBaseURL
	}
	if config.ApprovalMode == "" {
		config.ApprovalMode = Default().ApprovalMode
	}
	config.SandboxEscapePolicy = NormalizeSandboxEscapePolicy(config.SandboxEscapePolicy)
	config.StreamIdleTimeoutSeconds = NormalizeStreamIdleTimeoutSeconds(config.StreamIdleTimeoutSeconds)
	if config.OutputLanguage == "" {
		config.OutputLanguage = Default().OutputLanguage
	}
	return config, nil
}

func (m *Manager) Save(config Config) error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	content, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	if err := os.WriteFile(m.path, content, 0o644); err != nil {
		return fmt.Errorf("写入配置失败: %w", err)
	}

	return nil
}

func (m *Manager) InitDefault() (Config, error) {
	config := Default()
	if err := m.Save(config); err != nil {
		return Config{}, err
	}
	return config, nil
}
