package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

// shellSandboxConfig carries sandbox settings resolved from Config.ShellSandboxMode.
type shellSandboxConfig struct {
	Mode       string   // off | auto | enforce
	WorkDir    string   // working directory (always allowed for read/write)
	AllowRoots []string // additional allowed roots (temp dir, sandbox_roots, skill dirs)
}

// buildSandboxedCommand wraps the original command with a platform-specific sandbox.
// On macOS it uses sandbox-exec (Seatbelt); on Linux it uses bwrap (bubblewrap);
// on Windows it falls back to running without an OS-level sandbox.
//
// In "auto" mode the function silently falls back to no sandbox when the
// platform tool is unavailable. In "enforce" mode it returns an error.
func buildSandboxedCommand(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	sb := payload.Sandbox
	if sb == nil || sb.Mode == internalconfig.ShellSandboxModeOff {
		return buildRawCommand(ctx, payload)
	}

	wrapped, err := buildSandboxedCommandPlatform(ctx, payload)
	if err != nil {
		if sb.Mode == internalconfig.ShellSandboxModeAuto {
			// Auto mode: fall back to no sandbox.
			return buildRawCommand(ctx, payload)
		}
		// Enforce mode: surface the error.
		return nil, fmt.Errorf("shell sandbox (enforce) 不可用: %w", err)
	}
	configureProcessTree(wrapped)
	return wrapped, nil
}

// buildRawCommand creates the *exec.Cmd without any sandbox wrapping.
// This is the original buildCommand logic, extracted for reuse.
func buildRawCommand(ctx context.Context, payload commandArguments) (*exec.Cmd, error) {
	if payload.Mode == "shell" {
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, "cmd", "/C", payload.RawCommand)
		} else {
			cmd = exec.CommandContext(ctx, "sh", "-lc", payload.RawCommand)
		}
		configureProcessTree(cmd)
		return cmd, nil
	}
	// 结构化命令模式：返回命令名由 PATH 解析（CLI 目录已追加到 PATH 末尾）
	resolvedCommand := resolveCommandInCLIDir(payload.Command)
	cmd := exec.CommandContext(ctx, resolvedCommand, payload.Args...)
	configureProcessTree(cmd)
	return cmd, nil
}

// resolveShellSandbox resolves the sandbox configuration from Config.
// Returns nil when sandbox is disabled.
func resolveShellSandbox(config internalconfig.Config, workDir string) *shellSandboxConfig {
	mode := internalconfig.NormalizeShellSandboxMode(config.ShellSandboxMode)
	if mode == internalconfig.ShellSandboxModeOff {
		return nil
	}
	roots, err := resolveSandboxRoots(config, workDir)
	if err != nil {
		// If we can't resolve roots, still enable sandbox with just workdir + temp.
		roots = []string{workDir}
		if tempDir := strings.TrimSpace(os.TempDir()); tempDir != "" {
			roots = append(roots, tempDir)
		}
	}
	// Deduplicate and exclude the workdir (it's always in AllowRoots but we
	// also pass it separately for clarity in profile generation).
	allowRoots := make([]string, 0, len(roots))
	seen := map[string]bool{workDir: true}
	for _, root := range roots {
		cleaned := filepath.Clean(root)
		if !seen[cleaned] {
			seen[cleaned] = true
			allowRoots = append(allowRoots, cleaned)
		}
	}
	return &shellSandboxConfig{
		Mode:       mode,
		WorkDir:    workDir,
		AllowRoots: allowRoots,
	}
}

// isSandboxRuntimeError checks whether an error message indicates a
// sandbox runtime failure (e.g., sandbox-exec failed to apply the profile,
// or bwrap failed to set up the namespace). This is used to trigger auto
// mode runtime degradation: if the sandbox fails at execution time (not at
// command construction time), auto mode retries without the sandbox.
func isSandboxRuntimeError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// macOS sandbox-exec runtime errors.
	if strings.Contains(msg, "sandbox_apply") ||
		strings.Contains(msg, "sandbox-exec") ||
		strings.Contains(msg, "operation not permitted") {
		return true
	}
	// Linux bwrap runtime errors.
	if strings.Contains(msg, "bwrap") ||
		strings.Contains(msg, "bubblewrap") ||
		strings.Contains(msg, "failed to create namespace") {
		return true
	}
	return false
}

// sensitivePaths returns absolute paths that should always be denied
// regardless of sandbox roots. Includes both user-level and system-level
// sensitive paths, with platform-specific entries. 清单与桌面端
// taiyiflow-electron src/utils/sandbox/path-sandbox.ts 的 sensitivePaths()
// 保持对齐，是 isSensitivePath 与 OS 沙盒拒绝规则的统一数据源。
func sensitivePaths() []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	paths := []string{
		// SSH/GPG/AWS 凭据目录
		filepath.Join(homeDir, ".ssh"),
		filepath.Join(homeDir, ".gnupg"),
		filepath.Join(homeDir, ".aws"),
		// Shell 配置文件（可能含环境变量、别名、API Key 等）
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".bashrc"),
		filepath.Join(homeDir, ".bash_profile"),
		filepath.Join(homeDir, ".zprofile"),
		filepath.Join(homeDir, ".profile"),
		filepath.Join(homeDir, ".env"),
		filepath.Join(homeDir, ".gitconfig"),
		// Shell 历史记录文件（证据文件，清空操作属于证据销毁）
		filepath.Join(homeDir, ".bash_history"),
		filepath.Join(homeDir, ".zsh_history"),
		filepath.Join(homeDir, ".python_history"),
		// 云平台凭证目录
		filepath.Join(homeDir, ".kube"),
		filepath.Join(homeDir, ".docker"),
		filepath.Join(homeDir, ".azure"),
		filepath.Join(homeDir, ".config", "gcloud"),
		filepath.Join(homeDir, ".config", "azure"),
		filepath.Join(homeDir, ".oci"),
		// Git 与包管理器凭证文件
		filepath.Join(homeDir, ".git-credentials"),
		filepath.Join(homeDir, ".netrc"),
		filepath.Join(homeDir, ".npmrc"),
		filepath.Join(homeDir, ".pypirc"),
		filepath.Join(homeDir, ".gem"),
		// 基础设施即代码凭据
		filepath.Join(homeDir, ".terraform.d"),
		filepath.Join(homeDir, ".ansible"),
		filepath.Join(homeDir, ".vault-token"),
		// PKI 与本地密钥环
		filepath.Join(homeDir, ".pki"),
		filepath.Join(homeDir, ".local", "share", "keyring"),
		// 浏览器密码库（可能存储已保存的密码、Cookie、会话令牌）
		filepath.Join(homeDir, ".mozilla"),
		filepath.Join(homeDir, ".thunderbird"),
		filepath.Join(homeDir, ".config", "google-chrome"),
		filepath.Join(homeDir, ".config", "chromium"),
	}
	if runtime.GOOS != "windows" {
		// 系统级敏感文件（用户账户、认证数据库、系统 SSH 配置与私钥、关键配置）
		paths = append(paths,
			"/etc/passwd",
			"/etc/passwd-",
			"/etc/shadow",
			"/etc/shadow-",
			"/etc/gshadow",
			"/etc/gshadow-",
			"/etc/sudoers",
			"/etc/ssh",
			"/etc/ssl/private",
			"/etc/letsencrypt",
			"/etc/hosts",
			"/etc/fstab",
		)
	}
	switch runtime.GOOS {
	case "darwin":
		paths = append(paths,
			filepath.Join(homeDir, "Library", "Keychains"),
			filepath.Join(homeDir, "Library", "Application Support", "Google", "Chrome"),
			filepath.Join(homeDir, "Library", "Application Support", "Chromium"),
			filepath.Join(homeDir, "Library", "Application Support", "Firefox"),
			"/etc/master.passwd",
			"/private/etc/passwd",
			"/private/etc/master.passwd",
			"/private/etc/sudoers",
		)
	case "linux":
		// /etc/* 已在上方统一追加
	case "windows":
		systemRoot := os.Getenv("SystemRoot")
		if systemRoot == "" {
			systemRoot = `C:\Windows`
		}
		systemDrive := os.Getenv("SystemDrive")
		if systemDrive == "" {
			systemDrive = `C:`
		}
		paths = append(paths,
			// Windows hosts 文件（域名解析劫持）
			filepath.Join(systemRoot, "System32", "drivers", "etc", "hosts"),
			// 注册表蜂巢文件（密码哈希、系统配置）
			filepath.Join(systemRoot, "System32", "config", "SAM"),
			filepath.Join(systemRoot, "System32", "config", "SYSTEM"),
			filepath.Join(systemRoot, "System32", "config", "SECURITY"),
			filepath.Join(systemRoot, "System32", "config", "SOFTWARE"),
			filepath.Join(systemRoot, "System32", "config", "DEFAULT"),
			// Windows 修复目录（含 SAM 备份）
			filepath.Join(systemRoot, "repair"),
			// 事件日志目录（证据文件）
			filepath.Join(systemRoot, "System32", "winevt", "Logs"),
			// 启动管理器配置
			filepath.Join(systemDrive, "Boot", "BCD"),
			// Windows 凭据管理器（凭据、DPAPI 主密钥）
			filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Credentials"),
			filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Crypto"),
			filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Protect"),
			filepath.Join(homeDir, "AppData", "Local", "Microsoft", "Credentials"),
			// PowerShell 历史记录（证据文件）
			filepath.Join(homeDir, "AppData", "Roaming", "Microsoft", "Windows", "PowerShell", "PSReadLine"),
		)
	}
	return paths
}
