package cmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/browserbridge"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

var browserInstallLookupPath = exec.LookPath

var browserInstallRunCommand = func(ctx context.Context, dir string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

func (a *App) runBrowser(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printBrowserUsage()
		return nil
	}
	switch strings.TrimSpace(args[0]) {
	case "help", "-h", "--help":
		a.printBrowserUsage()
		return nil
	case "install":
		return a.runBrowserInstall(args[1:])
	case "host":
		return a.runBrowserHost(ctx, args[1:])
	default:
		return fmt.Errorf("不支持的 browser 子命令: %s，可使用 taiyiflow-cli browser help 查看用法", args[0])
	}
}

func (a *App) printBrowserUsage() {
	lines := []string{
		"浏览器命令用于安装当前浏览器会话扩展链路与本地消息宿主。",
		"",
		"用法:",
		"  taiyiflow-cli browser install [--channel chrome|chromium|edge|brave] [--build] [--extension-dir PATH] [--use-embedded-extension]",
		"  taiyiflow-cli browser help",
		"",
		"说明:",
		"  install 会生成本地消息宿主启动脚本与清单文件；若缺少工作区扩展目录，会自动抽取 CLI 内置的可加载扩展产物。",
		"  仅在显式传入 --build 时，才会执行 npm install && npm run build。",
		"  仓库内的 browser-extension/chrome 仍主要用于扩展开发、调试与手工重建产物。",
		"  host 为内部模式，供浏览器通过本地消息协议启动；通常不需要手动执行。",
	}
	_, _ = fmt.Fprintln(a.stdout, strings.Join(lines, "\n"))
}

func (a *App) runBrowserInstall(args []string) error {
	flagSet := flag.NewFlagSet("browser install", flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	channel := ""
	extensionDirFlag := ""
	buildExtension := false
	useEmbeddedExtension := false
	flagSet.StringVar(&channel, "channel", "", "浏览器通道（chrome|chromium|edge|brave）；默认使用当前配置")
	flagSet.StringVar(&extensionDirFlag, "extension-dir", "", "扩展项目目录；默认自动从当前工作区向上探测 browser-extension/chrome")
	flagSet.BoolVar(&buildExtension, "build", false, "安装本地消息宿主后自动执行 npm install && npm run build")
	flagSet.BoolVar(&useEmbeddedExtension, "use-embedded-extension", false, "忽略工作区扩展目录，直接抽取并使用 CLI 内置扩展资源")
	flagSet.BoolVar(&useEmbeddedExtension, "use-bundled-extension", false, "兼容旧参数；等价于 --use-embedded-extension")
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	config := internalconfig.Default()
	if a != nil && a.configManager != nil {
		loaded, err := a.configManager.Load()
		if err != nil {
			return err
		}
		config = loaded
	}
	if strings.TrimSpace(channel) != "" {
		normalized := internalconfig.NormalizeBrowserChannel(channel)
		if normalized != strings.ToLower(strings.TrimSpace(channel)) {
			return fmt.Errorf("浏览器通道仅支持 chrome、chromium、edge、brave")
		}
		config.Browser.Channel = normalized
	}
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("解析当前 CLI 可执行文件路径失败: %w", err)
	}
	currentDir, _ := os.Getwd()
	extensionDir, extensionSource, err := resolveBrowserExtensionProject(currentDir, extensionDirFlag, useEmbeddedExtension)
	if err != nil {
		return err
	}
	result, err := browserbridge.InstallNativeMessagingHost(config.Browser, binaryPath, extensionDir)
	if err != nil {
		return err
	}
	extensionStatus := browserbridge.InspectBrowserExtensionProject(result.ExtensionDir)
	buildSummary := ""
	if buildExtension {
		buildSummary, err = buildBrowserExtensionProject(context.Background(), extensionStatus)
		if err != nil {
			return err
		}
		extensionStatus = browserbridge.InspectBrowserExtensionProject(result.ExtensionDir)
	}
	if a == nil || a.stdout == nil {
		return nil
	}
	fmt.Fprintln(a.stdout, "已安装浏览器本地消息宿主。")
	fmt.Fprintf(a.stdout, "- 浏览器通道: %s\n", result.Channel)
	fmt.Fprintf(a.stdout, "- 宿主名称: %s\n", result.HostName)
	fmt.Fprintf(a.stdout, "- 清单文件路径: %s\n", result.ManifestPath)
	for _, manifestPath := range result.ManifestPaths {
		if manifestPath == result.ManifestPath {
			continue
		}
		fmt.Fprintf(a.stdout, "- 兼容清单文件: %s\n", manifestPath)
	}
	fmt.Fprintf(a.stdout, "- 启动脚本路径: %s\n", result.LauncherPath)
	if runtime.GOOS == "windows" {
		if result.RegistryRegistered {
			fmt.Fprintln(a.stdout, "- 注册表: 已注册")
		} else {
			fmt.Fprintln(a.stdout, "- 注册表: 未注册")
		}
	}
	fmt.Fprintf(a.stdout, "- 状态文件: %s\n", result.StatePath)
	fmt.Fprintf(a.stdout, "- 扩展标识: %s\n", result.ExtensionID)
	if strings.TrimSpace(result.SocketPath) != "" {
		fmt.Fprintf(a.stdout, "- 本地桥接套接字: %s\n", result.SocketPath)
	}
	if strings.TrimSpace(result.ExtensionDir) != "" {
		fmt.Fprintf(a.stdout, "- 扩展来源: %s\n", extensionSource)
		fmt.Fprintf(a.stdout, "- 扩展项目: %s\n", result.ExtensionDir)
		fmt.Fprintf(a.stdout, "- 扩展构建目录: %s\n", result.ExtensionBuildDir)
		fmt.Fprintf(a.stdout, "- 扩展源码状态: %s\n", formatBrowserExtensionSourceStatus(extensionStatus))
		fmt.Fprintf(a.stdout, "- 构建工具状态: %s\n", formatBrowserExtensionNPMStatus(extensionStatus))
		fmt.Fprintf(a.stdout, "- 构建产物: %s\n", formatBrowserExtensionBuildStatus(extensionStatus))
		if strings.TrimSpace(buildSummary) != "" {
			fmt.Fprintf(a.stdout, "- 构建验证: %s\n", buildSummary)
		} else if extensionStatus.SourceReady && !extensionStatus.DistReady {
			fmt.Fprintf(a.stdout, "- 自动构建命令: taiyiflow-cli browser install --build\n")
			fmt.Fprintf(a.stdout, "- 手动构建命令: cd %s && npm install && npm run build\n", result.ExtensionDir)
		}
		printBrowserFirstRunGuide(a.stdout, result.ExtensionBuildDir, extensionStatus)
	} else {
		fmt.Fprintln(a.stdout, "- 未找到可用的浏览器扩展项目，也未能抽取内置扩展资源。")
	}
	fmt.Fprintf(a.stdout, "- 安装完成后可执行: taiyiflow-cli doctor\n")
	return nil
}

func (a *App) runBrowserHost(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("browser host", flag.ContinueOnError)
	flagSet.SetOutput(ioDiscardWriter{})
	if err := flagSet.Parse(args); err != nil {
		return err
	}
	config := internalconfig.Default()
	if a != nil && a.configManager != nil {
		loaded, err := a.configManager.Load()
		if err != nil {
			return err
		}
		config = loaded
	}
	var stdinReader io.Reader = os.Stdin
	var stdoutWriter io.Writer = os.Stdout
	var stderrWriter io.Writer = os.Stderr
	if a != nil {
		if a.stdin != nil {
			stdinReader = a.stdin
		}
		if a.stdout != nil {
			stdoutWriter = a.stdout
		}
		if a.stderr != nil {
			stderrWriter = a.stderr
		}
	}
	return browserbridge.RunNativeMessagingHost(ctx, config.Browser, stdinReader, stdoutWriter, stderrWriter)
}

func detectChromeExtensionProjectDir(start string) string {
	trimmedStart := strings.TrimSpace(start)
	if trimmedStart == "" {
		return ""
	}
	current := trimmedStart
	for {
		candidate := filepath.Join(current, "browser-extension", "chrome")
		if info, err := os.Stat(filepath.Join(candidate, "package.json")); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func resolveBrowserExtensionProject(currentDir, explicitDir string, useEmbedded bool) (string, string, error) {
	resolvedExplicitDir := strings.TrimSpace(explicitDir)
	if resolvedExplicitDir != "" {
		absoluteDir, err := filepath.Abs(resolvedExplicitDir)
		if err != nil {
			return "", "", fmt.Errorf("解析扩展目录失败: %w", err)
		}
		status := browserbridge.InspectBrowserExtensionProject(absoluteDir)
		if !status.SourceReady && !status.DistReady && !useEmbedded {
			return "", "", fmt.Errorf("指定扩展目录既不是扩展源码目录，也不是可加载运行目录: %s", absoluteDir)
		}
		if (status.SourceReady || status.DistReady) && !useEmbedded {
			return absoluteDir, "指定目录", nil
		}
	}
	if !useEmbedded {
		detectedDir := detectChromeExtensionProjectDir(currentDir)
		if status := browserbridge.InspectBrowserExtensionProject(detectedDir); status.SourceReady || status.DistReady {
			return detectedDir, "工作区项目", nil
		}
	}
	embeddedDir, err := browserbridge.ExtractEmbeddedBrowserExtension()
	if err != nil {
		return "", "", err
	}
	status := browserbridge.InspectBrowserExtensionProject(embeddedDir)
	if !status.DistReady {
		return "", "", fmt.Errorf("抽取内置扩展资源后仍缺少可加载运行产物: %s", embeddedDir)
	}
	return embeddedDir, "内置运行产物", nil
}

func buildBrowserExtensionProject(ctx context.Context, status browserbridge.BrowserExtensionProjectStatus) (string, error) {
	if !status.SourceReady {
		if status.DistReady {
			return "", fmt.Errorf("当前目录仅包含可加载运行产物，无法执行构建: %s", status.Directory)
		}
		return "", fmt.Errorf("扩展目录缺少 package.json 或 manifest.json，无法执行构建: %s", status.Directory)
	}
	npmPath, err := browserInstallLookupPath("npm")
	if err != nil {
		return "", fmt.Errorf("当前环境未找到 npm，无法执行 --build；可手动安装 Node.js/npm 后重试")
	}
	installOutput, err := browserInstallRunCommand(ctx, status.Directory, npmPath, "install")
	if err != nil {
		return "", fmt.Errorf("执行 npm install 失败: %w\n%s", err, trimBrowserBuildOutput(installOutput))
	}
	buildOutput, err := browserInstallRunCommand(ctx, status.Directory, npmPath, "run", "build")
	if err != nil {
		return "", fmt.Errorf("执行 npm run build 失败: %w\n%s", err, trimBrowserBuildOutput(buildOutput))
	}
	updated := browserbridge.InspectBrowserExtensionProject(status.Directory)
	if !updated.DistReady {
		return "", fmt.Errorf("扩展构建命令已执行，但未找到构建产物: %s", updated.DistManifestPath)
	}
	return fmt.Sprintf("已执行 npm install && npm run build，构建产物位于 %s%s", updated.DistDir, summarizeCommandTail(installOutput, buildOutput)), nil
}

func formatBrowserExtensionSourceStatus(status browserbridge.BrowserExtensionProjectStatus) string {
	if status.SourceReady {
		return "已找到 package.json 与 manifest.json"
	}
	if status.DistReady {
		return "当前目录仅包含最小运行产物"
	}
	return "缺少 package.json 或 manifest.json"
}

func formatBrowserExtensionNPMStatus(status browserbridge.BrowserExtensionProjectStatus) string {
	if status.DistReady && !status.SourceReady {
		return "不适用（当前目录仅包含最小运行产物）"
	}
	if status.DistReady {
		if status.NPMAvailable {
			return fmt.Sprintf("可选重建可用 (%s)", status.NPMPath)
		}
		return "未找到 npm，但当前已存在可加载产物"
	}
	if status.NPMAvailable {
		return fmt.Sprintf("可用 (%s)", status.NPMPath)
	}
	return "未找到构建工具"
}

func formatBrowserExtensionBuildStatus(status browserbridge.BrowserExtensionProjectStatus) string {
	if status.DistReady {
		return fmt.Sprintf("已就绪 (%s)", status.DistManifestPath)
	}
	return fmt.Sprintf("未找到 %s", status.DistManifestPath)
}

func printBrowserFirstRunGuide(w io.Writer, extensionBuildDir string, status browserbridge.BrowserExtensionProjectStatus) {
	if w == nil {
		return
	}
	steps := []string{"首次使用路径:"}
	if !status.DistReady {
		steps = append(steps,
			fmt.Sprintf("1. 先执行 taiyiflow-cli browser install --build，或进入 %s 手动运行 npm install && npm run build。", status.Directory),
			"2. 构建完成后打开浏览器扩展管理页，并开启开发者模式。",
			fmt.Sprintf("3. 选择“加载已解压的扩展程序”，目录指向 %s。", extensionBuildDir),
			"4. 加载后点击一次扩展图标，触发扩展后台与本地消息宿主建立连接。",
			"5. 执行 taiyiflow-cli doctor，确认浏览器桥、本地消息宿主与本地套接字连通性均为通过。",
			"6. 在 TUI 配置中将浏览器模式切换为当前会话模式后，再调用浏览器工具验证真实页面操作。",
		)
	} else {
		steps = append(steps,
			"1. 打开浏览器扩展管理页，并开启开发者模式。",
			fmt.Sprintf("2. 选择“加载已解压的扩展程序”，目录指向 %s。", extensionBuildDir),
			"3. 加载后点击一次扩展图标，触发扩展后台与本地消息宿主建立连接。",
			"4. 执行 taiyiflow-cli doctor，确认浏览器桥、本地消息宿主与本地套接字连通性均为通过。",
			"5. 在 TUI 配置中将浏览器模式切换为当前会话模式后，再调用浏览器工具验证真实页面操作。",
		)
	}
	_, _ = fmt.Fprintln(w, strings.Join(steps, "\n"))
}

func trimBrowserBuildOutput(output string) string {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	return strings.Join(lines, "\n")
}

func summarizeCommandTail(installOutput, buildOutput string) string {
	tail := trimBrowserBuildOutput(buildOutput)
	if tail == "" {
		tail = trimBrowserBuildOutput(installOutput)
	}
	if tail == "" {
		return ""
	}
	firstLine := strings.SplitN(tail, "\n", 2)[0]
	return fmt.Sprintf("；命令输出摘要: %s", firstLine)
}

type ioDiscardWriter struct{}

func (ioDiscardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
