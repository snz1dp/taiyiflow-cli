package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const daemonLabel = "cn.snz1dp.taiyiflow-cli.worker"
const daemonUnitName = "taiyiflow-cli-worker"

// runDaemon 守护进程管理子命令入口。
func (a *App) runDaemon(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printDaemonUsage()
		return nil
	}
	switch args[0] {
	case "install":
		return a.daemonInstall(ctx)
	case "uninstall":
		return a.daemonUninstall(ctx)
	case "start":
		return a.daemonStart(ctx)
	case "stop":
		return a.daemonStop(ctx)
	case "status":
		return a.daemonStatus()
	default:
		return fmt.Errorf("未知守护进程子命令: %s", args[0])
	}
}

func (a *App) printDaemonUsage() {
	fmt.Fprintf(a.stdout, `用法: taiyiflow-cli daemon <子命令>

子命令:
  install     安装并启动 worker 守护进程
  uninstall   停止并卸载 worker 守护进程
  start       启动已安装的守护进程
  stop        停止守护进程（不卸载，用于更新前停止）
  status      查看守护进程运行状态

平台支持:
  macOS   使用 launchd（用户级 LaunchAgent）
  Linux   使用 systemd（用户级 user service）

更新流程:
  taiyiflow-cli daemon stop     # 停止旧进程
  <替换二进制文件>
  taiyiflow-cli daemon start    # 启动新进程
`)
}

// daemonLogDir 返回日志输出目录。
func daemonLogDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户主目录失败: %w", err)
	}
	return filepath.Join(homeDir, ".snz1dp", "logs"), nil
}

// daemonExecutable 返回当前可执行文件的绝对路径。
func daemonExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("获取当前可执行文件路径失败: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("解析可执行文件符号链接失败: %w", err)
	}
	return executable, nil
}

// daemonInstall 安装并启动守护进程。
func (a *App) daemonInstall(ctx context.Context) error {
	switch runtime.GOOS {
	case "darwin":
		return a.daemonInstallDarwin(ctx)
	case "linux":
		return a.daemonInstallLinux(ctx)
	default:
		return fmt.Errorf("当前平台 %s 暂不支持守护进程安装", runtime.GOOS)
	}
}

// daemonUninstall 停止并卸载守护进程。
func (a *App) daemonUninstall(ctx context.Context) error {
	switch runtime.GOOS {
	case "darwin":
		return a.daemonUninstallDarwin(ctx)
	case "linux":
		return a.daemonUninstallLinux(ctx)
	default:
		return fmt.Errorf("当前平台 %s 暂不支持守护进程管理", runtime.GOOS)
	}
}

// daemonStart 启动已安装的守护进程（不重新安装）。
func (a *App) daemonStart(ctx context.Context) error {
	switch runtime.GOOS {
	case "darwin":
		return a.daemonStartDarwin(ctx)
	case "linux":
		return a.daemonStartLinux(ctx)
	default:
		return fmt.Errorf("当前平台 %s 暂不支持守护进程管理", runtime.GOOS)
	}
}

// daemonStop 停止守护进程（不卸载，用于更新前停止）。
func (a *App) daemonStop(ctx context.Context) error {
	switch runtime.GOOS {
	case "darwin":
		return a.daemonStopDarwin(ctx)
	case "linux":
		return a.daemonStopLinux(ctx)
	default:
		return fmt.Errorf("当前平台 %s 暂不支持守护进程管理", runtime.GOOS)
	}
}

// daemonStatus 查看守护进程运行状态。
func (a *App) daemonStatus() error {
	switch runtime.GOOS {
	case "darwin":
		return a.daemonStatusDarwin()
	case "linux":
		return a.daemonStatusLinux()
	default:
		return fmt.Errorf("当前平台 %s 暂不支持守护进程管理", runtime.GOOS)
	}
}

// ============================================================
// macOS launchd 实现
// ============================================================

// daemonPlistPath 返回 launchd plist 文件路径。
func daemonPlistPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户主目录失败: %w", err)
	}
	return filepath.Join(homeDir, "Library", "LaunchAgents", daemonLabel+".plist"), nil
}

func (a *App) daemonInstallDarwin(ctx context.Context) error {
	executable, err := daemonExecutable()
	if err != nil {
		return err
	}

	plistPath, err := daemonPlistPath()
	if err != nil {
		return err
	}

	logDir, err := daemonLogDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}

	// BUG-4 修复：不再忽略 UserHomeDir 错误，复用上方已获取的 homeDir。
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("获取用户主目录失败: %w", err)
	}
	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>worker</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>HOME</key>
		<string>%s</string>
	</dict>
</dict>
</plist>
`, daemonLabel, executable,
		filepath.Join(logDir, "worker-stdout.log"),
		filepath.Join(logDir, "worker-stderr.log"),
		homeDir)

	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return fmt.Errorf("创建 LaunchAgents 目录失败: %w", err)
	}
	// RISK-5 修复：权限收紧为 0600，避免在多用户服务器上暴露路径信息。
	if err := os.WriteFile(plistPath, []byte(plistContent), 0o600); err != nil {
		return fmt.Errorf("写入 plist 文件失败: %w", err)
	}

	// 先尝试卸载旧实例（忽略错误）
	_ = exec.Command("launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), plistPath).Run()

	// 加载新实例
	loadCmd := exec.CommandContext(ctx, "launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plistPath)
	loadCmd.Stdout = a.stdout
	loadCmd.Stderr = a.stderr
	if err := loadCmd.Run(); err != nil {
		return fmt.Errorf("加载守护进程失败: %w", err)
	}

	fmt.Fprintf(a.stdout, "守护进程已安装并启动（macOS launchd）。\n")
	fmt.Fprintf(a.stdout, "  plist: %s\n", plistPath)
	fmt.Fprintf(a.stdout, "  日志:  %s/\n", logDir)
	fmt.Fprintf(a.stdout, "  停止:  taiyiflow-cli daemon uninstall\n")
	return nil
}

func (a *App) daemonUninstallDarwin(ctx context.Context) error {
	plistPath, err := daemonPlistPath()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(plistPath); os.IsNotExist(statErr) {
		fmt.Fprintln(a.stdout, "守护进程未安装，无需卸载。")
		return nil
	}

	unloadCmd := exec.CommandContext(ctx, "launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), plistPath)
	unloadCmd.Stdout = a.stdout
	unloadCmd.Stderr = a.stderr
	if err := unloadCmd.Run(); err != nil {
		fmt.Fprintf(a.stderr, "卸载守护进程时出现警告: %v（可能已停止）\n", err)
	}

	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 plist 文件失败: %w", err)
	}

	fmt.Fprintln(a.stdout, "守护进程已停止并卸载。")
	return nil
}

func (a *App) daemonStatusDarwin() error {
	plistPath, err := daemonPlistPath()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(plistPath); os.IsNotExist(statErr) {
		fmt.Fprintln(a.stdout, "守护进程未安装。")
		fmt.Fprintln(a.stdout, "安装命令: taiyiflow-cli daemon install")
		return nil
	}

	printCmd := exec.Command("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), daemonLabel))
	output, err := printCmd.CombinedOutput()
	if err != nil {
		fmt.Fprintln(a.stdout, "守护进程已安装但未在运行。")
		fmt.Fprintf(a.stdout, "  plist: %s\n", plistPath)
		return nil
	}

	fmt.Fprintln(a.stdout, "守护进程运行状态:")
	for _, line := range strings.Split(string(output), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "state") || strings.HasPrefix(trimmed, "pid") ||
			strings.HasPrefix(trimmed, "last exit") || strings.HasPrefix(trimmed, "program") {
			fmt.Fprintf(a.stdout, "  %s\n", trimmed)
		}
	}
	fmt.Fprintf(a.stdout, "  plist: %s\n", plistPath)
	return nil
}

func (a *App) daemonStartDarwin(ctx context.Context) error {
	plistPath, err := daemonPlistPath()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(plistPath); os.IsNotExist(statErr) {
		return fmt.Errorf("守护进程未安装，请先执行: taiyiflow-cli daemon install")
	}

	// 使用 kickstart 启动已加载的服务；若未加载则先 bootstrap
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), daemonLabel)
	kickCmd := exec.CommandContext(ctx, "launchctl", "kickstart", "-k", target)
	if output, err := kickCmd.CombinedOutput(); err != nil {
		// kickstart 失败可能是服务未加载，尝试 bootstrap
		loadCmd := exec.CommandContext(ctx, "launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plistPath)
		if loadOutput, loadErr := loadCmd.CombinedOutput(); loadErr != nil {
			return fmt.Errorf("启动守护进程失败: kickstart: %v (%s), bootstrap: %v (%s)",
				err, strings.TrimSpace(string(output)), loadErr, strings.TrimSpace(string(loadOutput)))
		}
	}

	fmt.Fprintln(a.stdout, "守护进程已启动。")
	return nil
}

func (a *App) daemonStopDarwin(ctx context.Context) error {
	plistPath, err := daemonPlistPath()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(plistPath); os.IsNotExist(statErr) {
		fmt.Fprintln(a.stdout, "守护进程未安装，无需停止。")
		return nil
	}

	// 使用 bootout 停止并卸载服务实例（plist 文件保留）。
	// 不能用 kill TERM，因为 KeepAlive=true 时 launchd 会立即重新拉起进程。
	// daemon start 会重新 bootstrap 加载。
	bootoutCmd := exec.CommandContext(ctx, "launchctl", "bootout", fmt.Sprintf("gui/%d", os.Getuid()), plistPath)
	if output, bootoutErr := bootoutCmd.CombinedOutput(); bootoutErr != nil {
		// 服务可能已经停止（未加载），不算致命错误
		outputStr := strings.TrimSpace(string(output))
		if !strings.Contains(outputStr, "No such process") && !strings.Contains(outputStr, "Could not find") {
			fmt.Fprintf(a.stderr, "停止守护进程时出现警告: %v (%s)\n", bootoutErr, outputStr)
		}
	}

	fmt.Fprintln(a.stdout, "守护进程已停止。")
	fmt.Fprintln(a.stdout, "重新启动: taiyiflow-cli daemon start")
	return nil
}

// ============================================================
// Linux systemd 用户级服务实现
// ============================================================

// daemonUnitPath 返回 systemd 用户级 unit 文件路径。
func daemonUnitPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户主目录失败: %w", err)
	}
	return filepath.Join(homeDir, ".config", "systemd", "user", daemonUnitName+".service"), nil
}

func (a *App) daemonInstallLinux(ctx context.Context) error {
	executable, err := daemonExecutable()
	if err != nil {
		return err
	}

	unitPath, err := daemonUnitPath()
	if err != nil {
		return err
	}

	logDir, err := daemonLogDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}

	unitContent := fmt.Sprintf(`[Unit]
Description=太乙智启 CLI Worker 守护进程
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s worker
Restart=always
RestartSec=5
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, executable,
		filepath.Join(logDir, "worker-stdout.log"),
		filepath.Join(logDir, "worker-stderr.log"))

	if err := os.MkdirAll(filepath.Dir(unitPath), 0o755); err != nil {
		return fmt.Errorf("创建 systemd 用户目录失败: %w", err)
	}
	// RISK-5 修复：权限收紧为 0600。
	if err := os.WriteFile(unitPath, []byte(unitContent), 0o600); err != nil {
		return fmt.Errorf("写入 systemd unit 文件失败: %w", err)
	}

	// 重新加载 systemd 用户配置
	// RISK-1 修复：检测 linger/bus 相关错误，给出针对性提示。
	reloadCmd := exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload")
	if output, err := reloadCmd.CombinedOutput(); err != nil {
		outputStr := strings.TrimSpace(string(output))
		if strings.Contains(outputStr, "bus") || strings.Contains(outputStr, "linger") || strings.Contains(outputStr, "No medium found") {
			return fmt.Errorf("重新加载 systemd 配置失败（用户会话不可用，可能需要执行 `loginctl enable-linger %s`）: %w (%s)", os.Getenv("USER"), err, outputStr)
		}
		return fmt.Errorf("重新加载 systemd 配置失败: %w (%s)", err, outputStr)
	}

	// 启用并启动服务
	enableCmd := exec.CommandContext(ctx, "systemctl", "--user", "enable", "--now", daemonUnitName)
	enableCmd.Stdout = a.stdout
	enableCmd.Stderr = a.stderr
	if err := enableCmd.Run(); err != nil {
		return fmt.Errorf("启用并启动守护进程失败: %w", err)
	}

	fmt.Fprintf(a.stdout, "守护进程已安装并启动（Linux systemd user service）。\n")
	fmt.Fprintf(a.stdout, "  unit: %s\n", unitPath)
	fmt.Fprintf(a.stdout, "  日志: %s/\n", logDir)
	fmt.Fprintf(a.stdout, "  停止: taiyiflow-cli daemon uninstall\n")
	return nil
}

func (a *App) daemonUninstallLinux(ctx context.Context) error {
	unitPath, err := daemonUnitPath()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(unitPath); os.IsNotExist(statErr) {
		fmt.Fprintln(a.stdout, "守护进程未安装，无需卸载。")
		return nil
	}

	// 停止并禁用服务
	stopCmd := exec.CommandContext(ctx, "systemctl", "--user", "disable", "--now", daemonUnitName)
	stopCmd.Stdout = a.stdout
	stopCmd.Stderr = a.stderr
	if err := stopCmd.Run(); err != nil {
		fmt.Fprintf(a.stderr, "停止守护进程时出现警告: %v（可能已停止）\n", err)
	}

	// 删除 unit 文件
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 unit 文件失败: %w", err)
	}

	// 重新加载配置
	_ = exec.CommandContext(ctx, "systemctl", "--user", "daemon-reload").Run()

	fmt.Fprintln(a.stdout, "守护进程已停止并卸载。")
	return nil
}

func (a *App) daemonStartLinux(ctx context.Context) error {
	unitPath, err := daemonUnitPath()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(unitPath); os.IsNotExist(statErr) {
		return fmt.Errorf("守护进程未安装，请先执行: taiyiflow-cli daemon install")
	}

	startCmd := exec.CommandContext(ctx, "systemctl", "--user", "start", daemonUnitName)
	if output, err := startCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("启动守护进程失败: %w (%s)", err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintln(a.stdout, "守护进程已启动。")
	return nil
}

func (a *App) daemonStopLinux(ctx context.Context) error {
	unitPath, err := daemonUnitPath()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(unitPath); os.IsNotExist(statErr) {
		fmt.Fprintln(a.stdout, "守护进程未安装，无需停止。")
		return nil
	}

	stopCmd := exec.CommandContext(ctx, "systemctl", "--user", "stop", daemonUnitName)
	if output, err := stopCmd.CombinedOutput(); err != nil {
		fmt.Fprintf(a.stderr, "停止守护进程时出现警告: %v (%s)\n", err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintln(a.stdout, "守护进程已停止。")
	fmt.Fprintln(a.stdout, "重新启动: taiyiflow-cli daemon start")
	return nil
}

func (a *App) daemonStatusLinux() error {
	unitPath, err := daemonUnitPath()
	if err != nil {
		return err
	}

	if _, statErr := os.Stat(unitPath); os.IsNotExist(statErr) {
		fmt.Fprintln(a.stdout, "守护进程未安装。")
		fmt.Fprintln(a.stdout, "安装命令: taiyiflow-cli daemon install")
		return nil
	}

	statusCmd := exec.Command("systemctl", "--user", "status", daemonUnitName)
	output, err := statusCmd.CombinedOutput()
	if err != nil {
		fmt.Fprintln(a.stdout, "守护进程已安装但未在运行。")
		fmt.Fprintf(a.stdout, "  unit: %s\n", unitPath)
		return nil
	}

	fmt.Fprintln(a.stdout, "守护进程运行状态:")
	// 只输出前几行关键信息
	lines := strings.Split(string(output), "\n")
	for i, line := range lines {
		if i >= 6 {
			break
		}
		fmt.Fprintf(a.stdout, "  %s\n", strings.TrimSpace(line))
	}
	fmt.Fprintf(a.stdout, "  unit: %s\n", unitPath)
	return nil
}
