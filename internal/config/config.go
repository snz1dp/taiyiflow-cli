package config

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultRootDirName                  = ".snz1dp"
	defaultConfigRel                    = "config/taiyiflow.yaml"
	rootDirEnvName                      = "SNZ1DP_HOME"
	DefaultBackendAPIPrefix             = "/taiyi/composer/api/v1"
	DefaultAuthAPIPrefix                = "/xeai"
	SkillInstallScopeUser               = "user"
	SkillInstallScopeWorkspace          = "workspace"
	DefaultHTTPTimeoutSeconds           = 600
	DefaultCommandTimeoutSeconds        = 3600
	MaxHTTPTimeoutSeconds               = 2000
	MaxCommandTimeoutSeconds            = 6000
	DefaultSubtaskTimeoutSeconds        = 3600
	MaxSubtaskTimeoutSeconds            = 6000
	DefaultStreamIdleTimeoutSeconds     = 1200
	DefaultQRWaitTimeoutSeconds         = 120
	DefaultToolConfirmTimeoutSeconds    = 120
	MinToolConfirmTimeoutSeconds        = 30
	MaxToolConfirmTimeoutSeconds        = 300
	DefaultAgentAutoLoopMaxRounds       = 0
	MaxAgentAutoLoopMaxRounds           = 10000
	DefaultMaxParallelismSubtask        = 3
	MaxMaxParallelismSubtask            = 32
	encryptedConfigValuePrefix          = "enc:v1:"
	SandboxEscapePolicyConfirm          = "confirm"
	SandboxEscapePolicyDeny             = "deny"
	SandboxEscapePolicyAllow            = "allow"
	SkillReviewPolicyConfirm            = "confirm"
	SkillReviewPolicyIgnore             = "ignore"
	WriteConfirmationPolicyConfirm      = "confirm"
	WriteConfirmationPolicyDirect       = "direct"
	ModeSwitchConfirmationPolicyConfirm = "confirm"
	ModeSwitchConfirmationPolicySkip    = "skip"
	SkillAutoInstallPolicyOff           = "off"
	SkillAutoInstallPolicyConfirm       = "confirm"
	SkillAutoInstallPolicyAllow         = "allow"
	BrowserModeDisabled                 = "disabled"
	BrowserModeChromeSession            = "chrome_session"
	BrowserModeIsolated                 = "isolated"
	BrowserPermissionModeAsk            = "ask"
	BrowserPermissionModeFollowPlan     = "follow_a_plan"
	BrowserPermissionModeSkipAll        = "skip_all_permission_checks"
	ComputerPermissionModeAsk           = "ask"
	ComputerPermissionModeFollowPlan    = "follow_a_plan"
	ComputerPermissionModeSkipAll       = "skip_all_permission_checks"
	ShellSandboxModeOff                 = "off"
	ShellSandboxModeAuto                = "auto"
	ShellSandboxModeEnforce             = "enforce"
	StreamProtocolAuto                  = "auto"
	StreamProtocolSSE                   = "sse"
	StreamProtocolWebSocket             = "websocket"
	DefaultRequestModeAgent             = "agent"
	DefaultRequestModeAsk               = "ask"
	DefaultRequestModePlan              = "plan"
	BrowserChannelChrome                = "chrome"
	BrowserChannelChromium              = "chromium"
	BrowserChannelEdge                  = "edge"
	BrowserChannelBrave                 = "brave"
	BrowserExtensionSetupNative         = "native_messaging"
	BrowserExtensionSetupManual         = "manual"
	BrowserExtensionSetupNone           = "none"
	DefaultBrowserConnectTimeout        = 15
	MaxBrowserConnectTimeout            = 120
	DefaultBrowserHostIdleTimeout       = 30
	MinBrowserHostIdleTimeout           = 1
	MaxBrowserHostIdleTimeout           = 3600
)

var defaultDocUnstructuredExts = []string{
	"mp3",
	"wav",
	"ogg",
	"flac",
	"aif",
	"m4a",
	"aac",
	"mp4",
	"webm",
	"avi",
	"mov",
	"flv",
	"wmv",
	"rmvb",
	"mkv",
	"pdf",
	"doc",
	"docx",
	"xls",
	"xlsx",
	"ppt",
	"pptx",
}

var defaultFileSearchExcludeDirs = []string{
	// 版本控制
	".git", ".svn", ".hg",
	// 依赖 / 包管理
	"node_modules", "vendor", "bower_components", ".pnpm-store",
	"venv", ".venv", "env", "__pypackages__", ".bundle", "Pods",
	// 构建产物
	"dist", "build", "out", "target", "bin", "obj",
	".next", ".nuxt", ".output", ".turbo", ".quasar",
	// IDE / 编辑器
	".idea", ".vscode", ".vs", ".eclipse", ".settings",
	// 缓存 / 临时
	".cache", "__pycache__", ".parcel-cache", ".pytest_cache",
	".mypy_cache", ".ruff_cache", ".tox", ".nox",
	"tmp", "temp", ".tmp",
	// 测试 / 覆盖率
	"coverage", ".nyc_output", "htmlcov",
	// 容器 / 基础设施
	".docker", ".terraform", ".serverless",
	// 操作系统
	".DS_Store", "Thumbs.db", "desktop.ini",
	// Java / JVM
	".gradle", ".m2",
	// iOS / macOS
	".cocoapods",
	// Dart / Flutter
	".dart_tool", ".pub-cache",
	// 前端框架
	".angular", ".svelte-kit", ".astro",
	// 工具 / 平台
	".snz1dp", ".serverless",
	// Python
	".eggs", "*.egg-info",
	// Go
	".gotestcache",
	// Node.js
	".tap", ".nyc_output",
}

var imageUnderstandingExtSet = map[string]struct{}{
	"jpg":  {},
	"jpeg": {},
	"png":  {},
	"gif":  {},
	"svg":  {},
	"webp": {},
	"bmp":  {},
	"tif":  {},
	"tiff": {},
}

type Config struct {
	BackendBaseURL               string           `yaml:"backend_base_url"`
	BackendAPIPrefix             string           `yaml:"backend_api_prefix,omitempty"`
	AuthAPIPrefix                string           `yaml:"auth_api_prefix,omitempty"`
	AccessToken                  string           `yaml:"access_token,omitempty"`
	PersistentToken              string           `yaml:"persistent_token,omitempty"`
	ClientID                     string           `yaml:"client_id,omitempty"`
	DeviceID                     string           `yaml:"device_id,omitempty"`
	AuthUpdatedAt                string           `yaml:"auth_updated_at,omitempty"`
	DefaultFlow                  string           `yaml:"default_flow,omitempty"`
	ApprovalMode                 string           `yaml:"approval_mode"`
	ReasoningEnabled             bool             `yaml:"reasoning_enabled,omitempty"`
	Workdir                      string           `yaml:"workdir,omitempty"`
	DocUnstructuredExts          []string         `yaml:"doc_unstructured_exts,omitempty"`
	HTTPTimeoutSeconds           int              `yaml:"http_timeout_seconds,omitempty"`
	SandboxEscapePolicy          string           `yaml:"sandbox_escape_policy,omitempty"`
	CommandTimeoutSeconds        int              `yaml:"command_timeout_seconds,omitempty"`
	SubtaskTimeoutSeconds        int              `yaml:"subtask_timeout_seconds,omitempty"`
	StreamIdleTimeoutSeconds     int              `yaml:"stream_idle_timeout_seconds,omitempty"`
	StreamProtocol               string           `yaml:"stream_protocol,omitempty"`
	QRWaitTimeoutSeconds         int              `yaml:"qr_wait_timeout_seconds,omitempty"`
	ToolConfirmTimeoutSeconds    int              `yaml:"tool_confirm_timeout_seconds,omitempty"`
	AgentAutoLoopEnabled         bool             `yaml:"agent_auto_loop_enabled,omitempty"`
	AgentAutoLoopMaxRounds       int              `yaml:"agent_auto_loop_max_rounds,omitempty"`
	MaxParallelismSubtask        int              `yaml:"max_parallelism_subtask,omitempty"`
	LoopDecideFlowID             string           `yaml:"loop_decide_flow_id,omitempty"`
	CrossSessionMemoryEnabled    bool             `yaml:"cross_session_memory_enabled,omitempty"`
	OutputLanguage               string           `yaml:"output_language"`
	AuditEnabled                 bool             `yaml:"audit_enabled,omitempty"`
	SkillReviewPolicy            string           `yaml:"skill_review_policy,omitempty"`
	WriteConfirmationPolicy      string           `yaml:"write_confirmation_policy,omitempty"`
	ModeSwitchConfirmationPolicy string           `yaml:"mode_switch_confirmation_policy,omitempty"`
	DefaultRequestMode           string           `yaml:"default_request_mode,omitempty"`
	RequestModeAutoSwitch        bool             `yaml:"request_mode_auto_switch,omitempty"`
	SandboxRoots                 []string         `yaml:"sandbox_roots,omitempty"`
	FileSearchExcludeDirs        []string         `yaml:"file_search_exclude_dirs,omitempty"`
	PermissionRules              []PermissionRule `yaml:"permission_rules,omitempty"`
	ShellSandboxMode             string           `yaml:"shell_sandbox_mode,omitempty"`
	Browser                      BrowserConfig    `yaml:"browser,omitempty"`
	Computer                     ComputerConfig   `yaml:"computer,omitempty"`
	Skills                       SkillConfig      `yaml:"skills,omitempty"`
}

type BrowserConfig struct {
	Mode                   string `yaml:"mode,omitempty"`
	PermissionMode         string `yaml:"permission_mode,omitempty"`
	Channel                string `yaml:"channel,omitempty"`
	ExecutablePath         string `yaml:"executable_path,omitempty"`
	ProfileDir             string `yaml:"profile_dir,omitempty"`
	ConnectTimeoutSeconds  int    `yaml:"connect_timeout_seconds,omitempty"`
	ExtensionSetup         string `yaml:"extension_setup,omitempty"`
	SocketDir              string `yaml:"socket_dir,omitempty"`
	RemoteDebuggingURL     string `yaml:"remote_debugging_url,omitempty"`
	HostIdleTimeoutSeconds int    `yaml:"host_idle_timeout_seconds,omitempty"`
	Headless               bool   `yaml:"headless,omitempty"`
}

type ComputerConfig struct {
	Enabled        *bool  `yaml:"enabled"`
	PermissionMode string `yaml:"permission_mode,omitempty"`
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
	Directory string `yaml:"directory,omitempty"`
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
		BackendBaseURL:               "https://snz1.cn",
		BackendAPIPrefix:             DefaultBackendAPIPrefix,
		AuthAPIPrefix:                DefaultAuthAPIPrefix,
		ApprovalMode:                 "confirm",
		ReasoningEnabled:             false,
		SandboxEscapePolicy:          SandboxEscapePolicyConfirm,
		HTTPTimeoutSeconds:           DefaultHTTPTimeoutSeconds,
		CommandTimeoutSeconds:        DefaultCommandTimeoutSeconds,
		DocUnstructuredExts:          append([]string(nil), defaultDocUnstructuredExts...),
		SubtaskTimeoutSeconds:        DefaultSubtaskTimeoutSeconds,
		StreamIdleTimeoutSeconds:     DefaultStreamIdleTimeoutSeconds,
		QRWaitTimeoutSeconds:         DefaultQRWaitTimeoutSeconds,
		ToolConfirmTimeoutSeconds:    DefaultToolConfirmTimeoutSeconds,
		AgentAutoLoopEnabled:         false,
		AgentAutoLoopMaxRounds:       DefaultAgentAutoLoopMaxRounds,
		MaxParallelismSubtask:        DefaultMaxParallelismSubtask,
		CrossSessionMemoryEnabled:    true,
		OutputLanguage:               "zh-CN",
		AuditEnabled:                 false,
		SkillReviewPolicy:            SkillReviewPolicyConfirm,
		WriteConfirmationPolicy:      WriteConfirmationPolicyConfirm,
		ModeSwitchConfirmationPolicy: ModeSwitchConfirmationPolicyConfirm,
		DefaultRequestMode:           DefaultRequestModeAgent,
		RequestModeAutoSwitch:        true,
		ShellSandboxMode:             ShellSandboxModeAuto,
		FileSearchExcludeDirs:        append([]string(nil), defaultFileSearchExcludeDirs...),
		Browser: BrowserConfig{
			Mode:                  BrowserModeChromeSession,
			PermissionMode:        BrowserPermissionModeAsk,
			Channel:               BrowserChannelChrome,
			ConnectTimeoutSeconds: DefaultBrowserConnectTimeout,
			ExtensionSetup:        BrowserExtensionSetupNative,
		},
		Computer: ComputerConfig{
			Enabled:        configBoolPtr(true),
			PermissionMode: ComputerPermissionModeAsk,
		},
		Skills: SkillConfig{
			InstallScope:          SkillInstallScopeUser,
			AutoInstallPolicy:     SkillAutoInstallPolicyOff,
			FreeTextSearchEnabled: false,
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

func NormalizeBackendAPIPrefix(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return DefaultBackendAPIPrefix
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	trimmed = strings.TrimRight(trimmed, "/")
	if trimmed == "/api/v1" {
		return DefaultBackendAPIPrefix
	}
	if trimmed == "" {
		return DefaultBackendAPIPrefix
	}
	return trimmed
}

func NormalizeAuthAPIPrefix(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return DefaultAuthAPIPrefix
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	trimmed = strings.TrimRight(trimmed, "/")
	if trimmed == "" {
		return DefaultAuthAPIPrefix
	}
	return trimmed
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

func NormalizeHTTPTimeoutSeconds(value int) int {
	if value < 1 {
		return DefaultHTTPTimeoutSeconds
	}
	if value > MaxHTTPTimeoutSeconds {
		return MaxHTTPTimeoutSeconds
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

func NormalizeBrowserMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case BrowserModeDisabled:
		return BrowserModeDisabled
	case BrowserModeIsolated:
		return BrowserModeIsolated
	default:
		return BrowserModeChromeSession
	}
}

func BrowserEnabled(value BrowserConfig) bool {
	return NormalizeBrowserMode(value.Mode) != BrowserModeDisabled
}

func NormalizeBrowserPermissionMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case BrowserPermissionModeFollowPlan:
		return BrowserPermissionModeFollowPlan
	case BrowserPermissionModeSkipAll:
		return BrowserPermissionModeSkipAll
	default:
		return BrowserPermissionModeAsk
	}
}

func NormalizeComputerPermissionMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ComputerPermissionModeFollowPlan:
		return ComputerPermissionModeFollowPlan
	case ComputerPermissionModeSkipAll:
		return ComputerPermissionModeSkipAll
	default:
		return ComputerPermissionModeAsk
	}
}

func ComputerEnabled(value ComputerConfig) bool {
	if value.Enabled == nil {
		return true
	}
	return *value.Enabled
}

func NormalizeBrowserChannel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case BrowserChannelChromium:
		return BrowserChannelChromium
	case BrowserChannelEdge:
		return BrowserChannelEdge
	case BrowserChannelBrave:
		return BrowserChannelBrave
	default:
		return BrowserChannelChrome
	}
}

func NormalizeBrowserExtensionSetup(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case BrowserExtensionSetupManual:
		return BrowserExtensionSetupManual
	case BrowserExtensionSetupNone:
		return BrowserExtensionSetupNone
	default:
		return BrowserExtensionSetupNative
	}
}

func NormalizeBrowserConnectTimeoutSeconds(value int) int {
	if value < 1 {
		return DefaultBrowserConnectTimeout
	}
	if value > MaxBrowserConnectTimeout {
		return MaxBrowserConnectTimeout
	}
	return value
}

func NormalizeBrowserHostIdleTimeoutSeconds(value int) int {
	if value < 0 {
		return DefaultBrowserHostIdleTimeout
	}
	if value > 0 && value < MinBrowserHostIdleTimeout {
		return MinBrowserHostIdleTimeout
	}
	if value > MaxBrowserHostIdleTimeout {
		return MaxBrowserHostIdleTimeout
	}
	return value
}

func NormalizeBrowserConfig(value BrowserConfig) BrowserConfig {
	value.Mode = NormalizeBrowserMode(value.Mode)
	value.PermissionMode = NormalizeBrowserPermissionMode(value.PermissionMode)
	value.Channel = NormalizeBrowserChannel(value.Channel)
	value.ExecutablePath = strings.TrimSpace(value.ExecutablePath)
	value.ProfileDir = strings.TrimSpace(value.ProfileDir)
	value.ConnectTimeoutSeconds = NormalizeBrowserConnectTimeoutSeconds(value.ConnectTimeoutSeconds)
	value.ExtensionSetup = NormalizeBrowserExtensionSetup(value.ExtensionSetup)
	value.SocketDir = strings.TrimSpace(value.SocketDir)
	value.RemoteDebuggingURL = strings.TrimSpace(value.RemoteDebuggingURL)
	value.HostIdleTimeoutSeconds = NormalizeBrowserHostIdleTimeoutSeconds(value.HostIdleTimeoutSeconds)
	return value
}

func NormalizeComputerConfig(value ComputerConfig) ComputerConfig {
	value.Enabled = configBoolPtr(ComputerEnabled(value))
	value.PermissionMode = NormalizeComputerPermissionMode(value.PermissionMode)
	return value
}

func configBoolPtr(value bool) *bool {
	result := value
	return &result
}

func NormalizeWriteConfirmationPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case WriteConfirmationPolicyDirect:
		return WriteConfirmationPolicyDirect
	default:
		return WriteConfirmationPolicyConfirm
	}
}

func NormalizeModeSwitchConfirmationPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ModeSwitchConfirmationPolicySkip:
		return ModeSwitchConfirmationPolicySkip
	default:
		return ModeSwitchConfirmationPolicyConfirm
	}
}

func NormalizeDefaultRequestMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case DefaultRequestModeAsk:
		return DefaultRequestModeAsk
	case DefaultRequestModePlan:
		return DefaultRequestModePlan
	default:
		return DefaultRequestModeAgent
	}
}

// DefaultFileSearchExcludeDirs 返回文件搜索排除目录的默认值副本。
func DefaultFileSearchExcludeDirs() []string {
	return append([]string(nil), defaultFileSearchExcludeDirs...)
}

func NormalizeFileSearchExcludeDirs(dirs []string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, dir := range dirs {
		trimmed := strings.TrimSpace(dir)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func NormalizeDocUnstructuredExts(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.Trim(strings.ToLower(strings.TrimSpace(value)), ".")
		if trimmed == "" {
			continue
		}
		if IsImageUnderstandingExtension(trimmed) {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func IsImageUnderstandingExtension(value string) bool {
	trimmed := strings.Trim(strings.ToLower(strings.TrimSpace(value)), ".")
	if trimmed == "" {
		return false
	}
	_, ok := imageUnderstandingExtSet[trimmed]
	return ok
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

func NormalizeShellSandboxMode(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ShellSandboxModeOff:
		return ShellSandboxModeOff
	case ShellSandboxModeEnforce:
		return ShellSandboxModeEnforce
	default:
		return ShellSandboxModeAuto
	}
}

func NormalizeSubtaskTimeoutSeconds(value int) int {
	if value < 0 {
		return 0
	}
	if value > MaxSubtaskTimeoutSeconds {
		return MaxSubtaskTimeoutSeconds
	}
	return value
}

func NormalizeStreamIdleTimeoutSeconds(value int) int {
	if value < 0 {
		return DefaultStreamIdleTimeoutSeconds
	}
	return value
}

// NormalizeStreamProtocol 规范化流式输出协议配置。
// 支持 auto（跟随服务端配置）、sse、websocket；无法识别的值回退为 auto。
func NormalizeStreamProtocol(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case StreamProtocolSSE:
		return StreamProtocolSSE
	case StreamProtocolWebSocket, "ws":
		return StreamProtocolWebSocket
	default:
		return StreamProtocolAuto
	}
}

func NormalizeQRWaitTimeoutSeconds(value int) int {
	if value < 0 {
		return DefaultQRWaitTimeoutSeconds
	}
	return value
}

// NormalizeToolConfirmTimeoutSeconds 规范化工具确认超时秒数。
// 若 value <= 0，返回 0，表示禁止倒计时，必须等待用户确认。
// 若 value > 0，在 [Min, Max] 范围内取值。
func NormalizeToolConfirmTimeoutSeconds(value int) int {
	if value <= 0 {
		return 0
	}
	if value < MinToolConfirmTimeoutSeconds {
		return MinToolConfirmTimeoutSeconds
	}
	if value > MaxToolConfirmTimeoutSeconds {
		return MaxToolConfirmTimeoutSeconds
	}
	return value
}

func NormalizeAgentAutoLoopMaxRounds(value int) int {
	if value < 0 {
		return DefaultAgentAutoLoopMaxRounds
	}
	if value > MaxAgentAutoLoopMaxRounds {
		return MaxAgentAutoLoopMaxRounds
	}
	return value
}

func NormalizeMaxParallelismSubtask(value int) int {
	// 0 是合法值，表示禁用子智能体（不暴露 run_subtask 工具）。
	if value == 0 {
		return 0
	}
	if value < 1 {
		return DefaultMaxParallelismSubtask
	}
	if value > MaxMaxParallelismSubtask {
		return MaxMaxParallelismSubtask
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

// DefaultSkillsDirLabel 返回用户级技能目录的显示标签。
// 当 SNZ1DP_HOME 已设置时，标签反映实际路径而非硬编码的 ~/.snz1dp/skills。
func DefaultSkillsDirLabel() string {
	dir, err := DefaultSkillsDir()
	if err != nil {
		return "用户目录 (~/.snz1dp/skills)"
	}
	return fmt.Sprintf("用户目录 (%s)", dir)
}

// DefaultAgentSkillsDir 返回兼容其他 agent 的用户级技能目录 ~/.agent/skills。
// 该目录用于加载第三方 agent（如 Claude Code 等约定 .agent/skills 的工具）安装的技能包。
func DefaultAgentSkillsDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}
	return filepath.Join(homeDir, ".agent", "skills"), nil
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

	decryptedAccessToken, err := decryptConfigSecret(config.AccessToken)
	if err != nil {
		// P2 修复：解密失败时降级为空令牌（视为未登录），而非阻断整个 Load 流程。
		// 场景：用户更改主机名、迁移 SNZ1DP_HOME、从备份恢复配置等。
		decryptedAccessToken = ""
	}
	decryptedPersistentToken, err := decryptConfigSecret(config.PersistentToken)
	if err != nil {
		decryptedPersistentToken = ""
	}
	config.AccessToken = decryptedAccessToken
	config.PersistentToken = decryptedPersistentToken

	if config.BackendBaseURL == "" {
		config.BackendBaseURL = Default().BackendBaseURL
	}
	config.BackendAPIPrefix = NormalizeBackendAPIPrefix(config.BackendAPIPrefix)
	config.AuthAPIPrefix = NormalizeAuthAPIPrefix(config.AuthAPIPrefix)
	config.AccessToken = strings.TrimSpace(config.AccessToken)
	config.PersistentToken = strings.TrimSpace(config.PersistentToken)
	config.ClientID = strings.TrimSpace(config.ClientID)
	config.DeviceID = strings.TrimSpace(config.DeviceID)
	config.AuthUpdatedAt = strings.TrimSpace(config.AuthUpdatedAt)
	config.DocUnstructuredExts = NormalizeDocUnstructuredExts(config.DocUnstructuredExts)
	if config.ApprovalMode == "" {
		config.ApprovalMode = Default().ApprovalMode
	}
	config.SandboxEscapePolicy = NormalizeSandboxEscapePolicy(config.SandboxEscapePolicy)
	config.SkillReviewPolicy = NormalizeSkillReviewPolicy(config.SkillReviewPolicy)
	config.Skills.AutoInstallPolicy = NormalizeSkillAutoInstallPolicy(config.Skills.AutoInstallPolicy)
	config.WriteConfirmationPolicy = NormalizeWriteConfirmationPolicy(config.WriteConfirmationPolicy)
	config.ModeSwitchConfirmationPolicy = NormalizeModeSwitchConfirmationPolicy(config.ModeSwitchConfirmationPolicy)
	config.DefaultRequestMode = NormalizeDefaultRequestMode(config.DefaultRequestMode)
	config.ShellSandboxMode = NormalizeShellSandboxMode(config.ShellSandboxMode)
	config.HTTPTimeoutSeconds = NormalizeHTTPTimeoutSeconds(config.HTTPTimeoutSeconds)
	config.CommandTimeoutSeconds = NormalizeCommandTimeoutSeconds(config.CommandTimeoutSeconds)
	config.SubtaskTimeoutSeconds = NormalizeSubtaskTimeoutSeconds(config.SubtaskTimeoutSeconds)
	config.StreamIdleTimeoutSeconds = NormalizeStreamIdleTimeoutSeconds(config.StreamIdleTimeoutSeconds)
	config.StreamProtocol = NormalizeStreamProtocol(config.StreamProtocol)
	config.QRWaitTimeoutSeconds = NormalizeQRWaitTimeoutSeconds(config.QRWaitTimeoutSeconds)
	config.ToolConfirmTimeoutSeconds = NormalizeToolConfirmTimeoutSeconds(config.ToolConfirmTimeoutSeconds)
	config.AgentAutoLoopMaxRounds = NormalizeAgentAutoLoopMaxRounds(config.AgentAutoLoopMaxRounds)
	config.MaxParallelismSubtask = NormalizeMaxParallelismSubtask(config.MaxParallelismSubtask)
	if config.OutputLanguage == "" {
		config.OutputLanguage = Default().OutputLanguage
	}
	config.Browser = NormalizeBrowserConfig(config.Browser)
	config.Computer = NormalizeComputerConfig(config.Computer)
	config.FileSearchExcludeDirs = NormalizeFileSearchExcludeDirs(config.FileSearchExcludeDirs)
	config.PermissionRules = NormalizePermissionRules(config.PermissionRules)
	return config, nil
}

func (m *Manager) Save(config Config) error {
	config.DocUnstructuredExts = NormalizeDocUnstructuredExts(config.DocUnstructuredExts)
	config.FileSearchExcludeDirs = NormalizeFileSearchExcludeDirs(config.FileSearchExcludeDirs)
	config.PermissionRules = NormalizePermissionRules(config.PermissionRules)
	config.Browser = NormalizeBrowserConfig(config.Browser)
	config.Computer = NormalizeComputerConfig(config.Computer)
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败: %w", err)
	}

	serializedConfig := config
	// P2⑩ 修复：确保随机种子存在，使加密密钥包含不可预测的随机成分
	if err := ensureConfigSecretSeed(); err != nil {
		return fmt.Errorf("初始化配置加密种子失败: %w", err)
	}
	encryptedAccessToken, err := encryptConfigSecret(config.AccessToken)
	if err != nil {
		return fmt.Errorf("加密 access_token 失败: %w", err)
	}
	encryptedPersistentToken, err := encryptConfigSecret(config.PersistentToken)
	if err != nil {
		return fmt.Errorf("加密 persistent_token 失败: %w", err)
	}
	serializedConfig.AccessToken = encryptedAccessToken
	serializedConfig.PersistentToken = encryptedPersistentToken

	content, err := yaml.Marshal(serializedConfig)
	if err != nil {
		return fmt.Errorf("序列化配置失败: %w", err)
	}

	// P0①+P1④ 修复：原子写入 + 权限收紧为 0600
	tmpPath := m.path + ".tmp"
	if err := os.WriteFile(tmpPath, content, 0o600); err != nil {
		return fmt.Errorf("写入配置临时文件失败: %w", err)
	}
	if err := os.Rename(tmpPath, m.path); err != nil {
		os.Remove(tmpPath)
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

func encryptConfigSecret(value string) (string, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return "", nil
	}
	if strings.HasPrefix(trimmedValue, encryptedConfigValuePrefix) {
		return trimmedValue, nil
	}
	block, err := aes.NewCipher(configSecretKey())
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nil, nonce, []byte(trimmedValue), nil)
	payload := append(nonce, sealed...)
	return encryptedConfigValuePrefix + base64.RawStdEncoding.EncodeToString(payload), nil
}

func decryptConfigSecret(value string) (string, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return "", nil
	}
	if !strings.HasPrefix(trimmedValue, encryptedConfigValuePrefix) {
		return trimmedValue, nil
	}
	encodedPayload := strings.TrimPrefix(trimmedValue, encryptedConfigValuePrefix)
	payload, err := base64.RawStdEncoding.DecodeString(encodedPayload)
	if err != nil {
		return "", err
	}
	// P2⑩ 修复：优先使用新密钥（含随机种子）解密，失败时回退到旧密钥（向后兼容）
	// v3 修复：密钥派生去除 hostname（macOS 上会随网络切换/睡眠唤醒漂移，导致密文永久解不开），
	// 回退链为 v3（seed+home+rootDir）→ v2（seed+home+hostname+rootDir）→ v1（home+hostname+rootDir）
	keys := [][]byte{configSecretKey(), configSecretKeyV2Legacy(), configSecretKeyLegacy()}
	for _, key := range keys {
		block, err := aes.NewCipher(key)
		if err != nil {
			continue
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			continue
		}
		if len(payload) < aead.NonceSize() {
			continue
		}
		nonce := payload[:aead.NonceSize()]
		ciphertext := payload[aead.NonceSize():]
		plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
		if err == nil {
			return strings.TrimSpace(string(plaintext)), nil
		}
	}
	return "", fmt.Errorf("解密配置密文失败")
}

// configSecretKey 返回当前版本（v3）密钥。
// v3 修复：派生因子中移除 os.Hostname()。macOS 上主机名会随网络切换、睡眠唤醒、
// DHCP 续租等发生变化（HostName / LocalHostName / ComputerName 三者还可能互不一致），
// 一旦变化，用旧主机名加密的密文将永久无法解密，表现为
// "Unsupported state or unable to authenticate data"。
// 随机性由 config-secret-seed 文件保证，不再依赖主机名。
func configSecretKey() []byte {
	seedParts := []string{"taiyiflow-cli/config-secret/v3"}
	// P2⑩ 修复：引入随机种子文件，使密钥不再仅由公开信息推导
	if seed, err := readConfigSecretSeed(); err == nil && seed != "" {
		seedParts = append(seedParts, seed)
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		seedParts = append(seedParts, strings.TrimSpace(homeDir))
	}
	if rootDir, err := DefaultRootDir(); err == nil {
		seedParts = append(seedParts, strings.TrimSpace(rootDir))
	}
	sum := sha256.Sum256([]byte(strings.Join(seedParts, "|")))
	return sum[:]
}

// configSecretKeyV2Legacy 返回 v2 密钥（含随机种子 + 主机名），用于向后兼容解密。
func configSecretKeyV2Legacy() []byte {
	seedParts := []string{"taiyiflow-cli/config-secret/v2"}
	if seed, err := readConfigSecretSeed(); err == nil && seed != "" {
		seedParts = append(seedParts, seed)
	}
	if homeDir, err := os.UserHomeDir(); err == nil {
		seedParts = append(seedParts, strings.TrimSpace(homeDir))
	}
	if hostName, err := os.Hostname(); err == nil {
		seedParts = append(seedParts, strings.TrimSpace(hostName))
	}
	if rootDir, err := DefaultRootDir(); err == nil {
		seedParts = append(seedParts, strings.TrimSpace(rootDir))
	}
	sum := sha256.Sum256([]byte(strings.Join(seedParts, "|")))
	return sum[:]
}

// configSecretKeyLegacy 返回 v1 密钥（不含随机种子），用于向后兼容解密。
func configSecretKeyLegacy() []byte {
	seedParts := []string{"taiyiflow-cli/config-secret/v1"}
	if homeDir, err := os.UserHomeDir(); err == nil {
		seedParts = append(seedParts, strings.TrimSpace(homeDir))
	}
	if hostName, err := os.Hostname(); err == nil {
		seedParts = append(seedParts, strings.TrimSpace(hostName))
	}
	if rootDir, err := DefaultRootDir(); err == nil {
		seedParts = append(seedParts, strings.TrimSpace(rootDir))
	}
	sum := sha256.Sum256([]byte(strings.Join(seedParts, "|")))
	return sum[:]
}

// configSecretSeedPath 返回随机种子文件路径。
func configSecretSeedPath() (string, error) {
	rootDir, err := DefaultRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(rootDir, "config-secret-seed"), nil
}

// readConfigSecretSeed 读取随机种子文件内容。
func readConfigSecretSeed() (string, error) {
	seedPath, err := configSecretSeedPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(seedPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// ensureConfigSecretSeed 确保随机种子文件存在；不存在时生成 32 字节随机种子。
func ensureConfigSecretSeed() error {
	seedPath, err := configSecretSeedPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(seedPath); err == nil {
		return nil // 已存在
	}
	seed := make([]byte, 32)
	if _, err := io.ReadFull(cryptorand.Reader, seed); err != nil {
		return fmt.Errorf("生成配置加密种子失败: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(seedPath), 0o700); err != nil {
		return fmt.Errorf("创建配置加密种子目录失败: %w", err)
	}
	if err := os.WriteFile(seedPath, []byte(base64.RawStdEncoding.EncodeToString(seed)), 0o600); err != nil {
		return fmt.Errorf("写入配置加密种子失败: %w", err)
	}
	return nil
}
