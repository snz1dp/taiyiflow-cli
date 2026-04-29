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
	SkillInstallScopeUser           = "user"
	SkillInstallScopeWorkspace      = "workspace"
	DefaultCommandTimeoutSeconds    = 30
	MaxCommandTimeoutSeconds        = 300
	DefaultStreamIdleTimeoutSeconds = 300
	DefaultAgentAutoLoopMaxRounds   = 3
	SandboxEscapePolicyConfirm      = "confirm"
	SandboxEscapePolicyDeny         = "deny"
	SandboxEscapePolicyAllow        = "allow"
	SkillReviewPolicyConfirm        = "confirm"
	SkillReviewPolicyIgnore         = "ignore"
	WriteConfirmationPolicyConfirm  = "confirm"
	WriteConfirmationPolicyDirect   = "direct"
	SkillAutoInstallPolicyOff       = "off"
	SkillAutoInstallPolicyConfirm   = "confirm"
	SkillAutoInstallPolicyAllow     = "allow"
)

type Config struct {
	BackendBaseURL           string           `yaml:"backend_base_url"`
	APIKey                   string           `yaml:"api_key,omitempty"`
	DefaultFlow              string           `yaml:"default_flow,omitempty"`
	ApprovalMode             string           `yaml:"approval_mode"`
	ReasoningEnabled         bool             `yaml:"reasoning_enabled,omitempty"`
	Workdir                  string           `yaml:"workdir,omitempty"`
	SandboxEscapePolicy      string           `yaml:"sandbox_escape_policy,omitempty"`
	CommandTimeoutSeconds    int              `yaml:"command_timeout_seconds,omitempty"`
	StreamIdleTimeoutSeconds int              `yaml:"stream_idle_timeout_seconds,omitempty"`
	AgentAutoLoopEnabled     bool             `yaml:"agent_auto_loop_enabled,omitempty"`
	AgentAutoLoopMaxRounds   int              `yaml:"agent_auto_loop_max_rounds,omitempty"`
	OutputLanguage           string           `yaml:"output_language"`
	AuditEnabled             bool             `yaml:"audit_enabled,omitempty"`
	SkillReviewPolicy        string           `yaml:"skill_review_policy,omitempty"`
	WriteConfirmationPolicy  string           `yaml:"write_confirmation_policy,omitempty"`
	SandboxRoots             []string         `yaml:"sandbox_roots,omitempty"`
	PermissionRules          []PermissionRule `yaml:"permission_rules,omitempty"`
	Skills                   SkillConfig      `yaml:"skills,omitempty"`
}

type PermissionRule struct {
	Action        string `yaml:"action"`
	ToolName      string `yaml:"tool_name,omitempty"`
	CommandPrefix string `yaml:"command_prefix,omitempty"`
}

type SkillConfig struct {
	InstallScope string `yaml:"install_scope,omitempty"`
	// AutoInstallPolicy 控制请求前自动补装策略：off 关闭，confirm 请求确认，allow 允许自动安装。
	AutoInstallPolicy string `yaml:"auto_install_policy,omitempty"`
	// FreeTextSearchEnabled 控制在未命中显式缺失技能时，是否按任务文本搜索远程技能候选。
	FreeTextSearchEnabled bool `yaml:"free_text_search_enabled,omitempty"`
	// Directory 指向全局技能目录；发现顺序始终是“当前工作区 .snz1dp/skills 优先，
	// 然后再到这里配置的全局目录”，留空时回退到默认 ~/.snz1dp/skills。
	// 该字段仅保留兼容读取历史自定义路径配置，不再作为新的用户可编辑项。
	Directory   string `yaml:"directory,omitempty"`
	RegistryURL string `yaml:"registry_url,omitempty"`
	// Enabled 保留为显式放行/兼容配置。当前默认策略不是白名单启用，未列入 enabled
	// 的技能只要没有被 disabled 命中，仍会纳入渐进式披露候选集合。
	Enabled []string `yaml:"enabled,omitempty"`
	// Disabled 是显式禁用列表，优先级高于 enabled。命中后该技能不会纳入候选集合。
	Disabled []string `yaml:"disabled,omitempty"`
}

type Manager struct {
	path string
}

func Default() Config {
	return Config{
		BackendBaseURL:           "http://localhost:7860",
		ApprovalMode:             "confirm",
		ReasoningEnabled:         false,
		SandboxEscapePolicy:      SandboxEscapePolicyConfirm,
		CommandTimeoutSeconds:    DefaultCommandTimeoutSeconds,
		StreamIdleTimeoutSeconds: DefaultStreamIdleTimeoutSeconds,
		AgentAutoLoopEnabled:     false,
		AgentAutoLoopMaxRounds:   DefaultAgentAutoLoopMaxRounds,
		OutputLanguage:           "zh-CN",
		AuditEnabled:             false,
		SkillReviewPolicy:        SkillReviewPolicyConfirm,
		WriteConfirmationPolicy:  WriteConfirmationPolicyConfirm,
		Skills: SkillConfig{
			InstallScope:          SkillInstallScopeUser,
			AutoInstallPolicy:     SkillAutoInstallPolicyOff,
			FreeTextSearchEnabled: false,
			RegistryURL:           "https://mirror-cn.clawhub.com",
		},
	}
}

func NormalizeSkillInstallScope(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SkillInstallScopeWorkspace:
		return SkillInstallScopeWorkspace
	default:
		return SkillInstallScopeUser
	}
}

func NormalizeCommandTimeoutSeconds(value int) int {
	if value < 1 {
		return DefaultCommandTimeoutSeconds
	}
	if value > MaxCommandTimeoutSeconds {
		return MaxCommandTimeoutSeconds
	}
	return value
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

func NormalizeSkillReviewPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SkillReviewPolicyIgnore:
		return SkillReviewPolicyIgnore
	default:
		return SkillReviewPolicyConfirm
	}
}

func NormalizeSkillAutoInstallPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case SkillAutoInstallPolicyAllow:
		return SkillAutoInstallPolicyAllow
	case SkillAutoInstallPolicyConfirm:
		return SkillAutoInstallPolicyConfirm
	default:
		return SkillAutoInstallPolicyOff
	}
}

func NormalizeWriteConfirmationPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case WriteConfirmationPolicyDirect:
		return WriteConfirmationPolicyDirect
	default:
		return WriteConfirmationPolicyConfirm
	}
}

func NormalizePermissionAction(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "allow":
		return "allow"
	case "deny":
		return "deny"
	default:
		return "ask"
	}
}

func NormalizeStreamIdleTimeoutSeconds(value int) int {
	if value < 0 {
		return DefaultStreamIdleTimeoutSeconds
	}
	return value
}

func NormalizeAgentAutoLoopMaxRounds(value int) int {
	if value < 1 {
		return DefaultAgentAutoLoopMaxRounds
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

func DefaultBundledSkillsDir() (string, error) {
	rootDir, err := DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(rootDir, "bundled-skills"), nil
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
	config.SkillReviewPolicy = NormalizeSkillReviewPolicy(config.SkillReviewPolicy)
	config.Skills.AutoInstallPolicy = NormalizeSkillAutoInstallPolicy(config.Skills.AutoInstallPolicy)
	config.WriteConfirmationPolicy = NormalizeWriteConfirmationPolicy(config.WriteConfirmationPolicy)
	config.CommandTimeoutSeconds = NormalizeCommandTimeoutSeconds(config.CommandTimeoutSeconds)
	config.StreamIdleTimeoutSeconds = NormalizeStreamIdleTimeoutSeconds(config.StreamIdleTimeoutSeconds)
	config.AgentAutoLoopMaxRounds = NormalizeAgentAutoLoopMaxRounds(config.AgentAutoLoopMaxRounds)
	if config.OutputLanguage == "" {
		config.OutputLanguage = Default().OutputLanguage
	}
	if strings.TrimSpace(config.Skills.RegistryURL) == "" {
		config.Skills.RegistryURL = Default().Skills.RegistryURL
	}
	config.PermissionRules = NormalizePermissionRules(config.PermissionRules)
	return config, nil
}

func (m *Manager) Save(config Config) error {
	config.PermissionRules = NormalizePermissionRules(config.PermissionRules)
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

func NormalizePermissionRules(items []PermissionRule) []PermissionRule {
	result := make([]PermissionRule, 0, len(items))
	for _, item := range items {
		rule := PermissionRule{
			Action:        NormalizePermissionAction(item.Action),
			ToolName:      strings.TrimSpace(item.ToolName),
			CommandPrefix: strings.TrimSpace(item.CommandPrefix),
		}
		if rule.ToolName == "" && rule.CommandPrefix == "" {
			continue
		}
		result = append(result, rule)
	}
	return result
}
