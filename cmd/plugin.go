package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/plugins"
)

// pluginCommandSynopsis 返回 plugin 子命令用法摘要（root usage 引用）。
func pluginCommandSynopsis() string {
	return "taiyiflow-cli plugin [init|validate|dev|pack|keygen|list-installed|help] [参数]  插件开发套件（DevKit）"
}

// printPluginUsage 输出 plugin 子命令帮助。
func (a *App) printPluginUsage() {
	fmt.Fprintln(a.stdout, "用法:")
	fmt.Fprintln(a.stdout, "  "+pluginCommandSynopsis())
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "子命令:")
	fmt.Fprintln(a.stdout, "  init <名称> [--dir 目录] [--display-name 名称] [--overwrite]")
	fmt.Fprintln(a.stdout, "                                在指定目录（默认当前目录）生成插件工程骨架")
	fmt.Fprintln(a.stdout, "                                （plugin.json 最小 contributions + ui/ + prompts/ + SKILL.md）")
	fmt.Fprintln(a.stdout, "  validate <目录或plugin.json路径>")
	fmt.Fprintln(a.stdout, "                                校验插件 manifest 是否符合共享 schema")
	fmt.Fprintln(a.stdout, "                                （与宿主 manifest-validator 引同一份，校验通过即可被宿主加载）")
	fmt.Fprintln(a.stdout, "  dev <插件目录> [--workspace 宿主仓库目录]")
	fmt.Fprintln(a.stdout, "                                把插件目录写入 .plugin-dev.json（默认 workspace 为当前目录）")
	fmt.Fprintln(a.stdout, "                                随后在宿主仓库执行 quasar dev 即可 dev-load 加载")
	fmt.Fprintln(a.stdout, "  pack <插件目录> [--out 输出目录] [--sign 私钥路径]")
	fmt.Fprintln(a.stdout, "                                打包插件为可分发产物（发布前强制 validate）")
	fmt.Fprintln(a.stdout, "                                产出 {id}-{version}.zip + 旁置 .sha256 校验值文件")
	fmt.Fprintln(a.stdout, "  keygen [--dir 输出目录] [--name 密钥名]")
	fmt.Fprintln(a.stdout, "                                生成 ed25519 发布者密钥对（私有分发签名链）")
	fmt.Fprintln(a.stdout, "                                产出 {name}-private.pem（保密）/ {name}-public.pem（可公开）")
	fmt.Fprintln(a.stdout, "  list-installed                列出已安装插件（读取 ~/.snz1dp/plugins，与 Electron 宿主共享目录）")
	fmt.Fprintln(a.stdout, "  help                           显示当前帮助")
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "示例:")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli plugin init demo-plugin")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli plugin validate ./demo-plugin")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli plugin dev ./demo-plugin --workspace ~/work/taiyiflow-electron")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli plugin keygen --name demo-publisher")
	fmt.Fprintln(a.stdout, "  taiyiflow-cli plugin pack ./demo-plugin --sign ./demo-publisher-private.pem")
}

// runPlugin 是 plugin 子命令入口（root.go 命令分发调用）。
func (a *App) runPlugin(ctx context.Context, args []string) error {
	if len(args) == 0 {
		a.printPluginUsage()
		return nil
	}
	switch strings.TrimSpace(args[0]) {
	case "help", "-h", "--help":
		a.printPluginUsage()
		return nil
	case "init":
		return a.runPluginInit(args[1:])
	case "validate":
		return a.runPluginValidate(args[1:])
	case "dev":
		return a.runPluginDev(args[1:])
	case "pack":
		return a.runPluginPack(args[1:])
	case "keygen":
		return a.runPluginKeygen(args[1:])
	case "list-installed", "list":
		return a.runPluginListInstalled(args[1:])
	default:
		return fmt.Errorf("未知 plugin 子命令 %q，执行 `taiyiflow-cli plugin help` 查看用法", args[0])
	}
}

// runPluginInit 处理 plugin init：生成插件工程骨架。
func (a *App) runPluginInit(args []string) error {
	var name, dir, displayName string
	var overwrite bool
	rest := args
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--dir":
			if i+1 >= len(rest) {
				return fmt.Errorf("--dir 缺少参数值")
			}
			i++
			dir = rest[i]
		case "--display-name":
			if i+1 >= len(rest) {
				return fmt.Errorf("--display-name 缺少参数值")
			}
			i++
			displayName = rest[i]
		case "--overwrite":
			overwrite = true
		default:
			if name == "" && !strings.HasPrefix(rest[i], "-") {
				name = rest[i]
			} else {
				return fmt.Errorf("未知参数 %q", rest[i])
			}
		}
	}
	if name == "" {
		return fmt.Errorf("用法: taiyiflow-cli plugin init <名称> [--dir 目录] [--display-name 名称] [--overwrite]")
	}
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("获取当前目录失败: %w", err)
		}
		dir = cwd
	}

	result, err := plugins.ScaffoldPlugin(dir, name, displayName, overwrite)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "插件工程已创建: %s\n", result.PluginDir)
	fmt.Fprintf(a.stdout, "插件 id: %s\n", result.PluginID)
	fmt.Fprintf(a.stdout, "生成文件:\n")
	for _, file := range result.Files {
		fmt.Fprintf(a.stdout, "  %s\n", file)
	}
	fmt.Fprintf(a.stdout, "\n后续步骤:\n")
	fmt.Fprintf(a.stdout, "  1. taiyiflow-cli plugin validate %s\n", result.PluginDir)
	fmt.Fprintf(a.stdout, "  2. taiyiflow-cli plugin dev %s --workspace <宿主仓库目录>\n", result.PluginDir)
	fmt.Fprintf(a.stdout, "  3. 在宿主仓库执行 quasar dev，dev 插件自动加载（DEV 角标）\n")
	return nil
}

// runPluginValidate 处理 plugin validate：校验 manifest。
func (a *App) runPluginValidate(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: taiyiflow-cli plugin validate <目录或plugin.json路径>")
	}
	target := args[0]
	manifestPath := target
	// 目录参数：拼接 plugin.json
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		manifestPath = filepath.Join(target, "plugin.json")
	}

	result, err := plugins.ValidateManifestFile(manifestPath)
	if err != nil {
		return err
	}
	if result.Valid {
		fmt.Fprintf(a.stdout, "✓ 校验通过: %s\n", manifestPath)
		fmt.Fprintf(a.stdout, "  （与宿主共享同一份 schema，可直接被 dev-load 加载）\n")
		return nil
	}
	fmt.Fprintf(a.stdout, "✗ 校验失败: %s\n", manifestPath)
	for _, validationErr := range result.Errors {
		fmt.Fprintf(a.stdout, "  %s\n", validationErr.Error())
	}
	return fmt.Errorf("manifest 校验失败（%d 处错误）", len(result.Errors))
}

// runPluginDev 处理 plugin dev：写入 .plugin-dev.json。
func (a *App) runPluginDev(args []string) error {
	var pluginDir, workspace string
	rest := args
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--workspace":
			if i+1 >= len(rest) {
				return fmt.Errorf("--workspace 缺少参数值")
			}
			i++
			workspace = rest[i]
		default:
			if pluginDir == "" && !strings.HasPrefix(rest[i], "-") {
				pluginDir = rest[i]
			} else {
				return fmt.Errorf("未知参数 %q", rest[i])
			}
		}
	}
	if pluginDir == "" {
		return fmt.Errorf("用法: taiyiflow-cli plugin dev <插件目录> [--workspace 宿主仓库目录]")
	}
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("获取当前目录失败: %w", err)
		}
		workspace = cwd
	}

	pluginList, err := plugins.RegisterDevPlugin(workspace, pluginDir)
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "已写入 %s\n", filepath.Join(workspace, plugins.DevConfigFileName))
	fmt.Fprintf(a.stdout, "当前 dev 插件列表:\n")
	for _, entry := range pluginList {
		fmt.Fprintf(a.stdout, "  %s\n", entry)
	}
	fmt.Fprintf(a.stdout, "\n下一步: 在宿主仓库（%s）执行 quasar dev，dev 插件自动加载\n", workspace)
	return nil
}

// runPluginPack 处理 plugin pack：打包插件为可分发产物（zip + sha256，可选 --sign 签名）。
func (a *App) runPluginPack(args []string) error {
	var pluginDir, outDir, signKeyPath string
	rest := args
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--out":
			if i+1 >= len(rest) {
				return fmt.Errorf("--out 缺少参数值")
			}
			i++
			outDir = rest[i]
		case "--sign":
			if i+1 >= len(rest) {
				return fmt.Errorf("--sign 缺少参数值")
			}
			i++
			signKeyPath = rest[i]
		default:
			if pluginDir == "" && !strings.HasPrefix(rest[i], "-") {
				pluginDir = rest[i]
			} else {
				return fmt.Errorf("未知参数 %q", rest[i])
			}
		}
	}
	if pluginDir == "" {
		return fmt.Errorf("用法: taiyiflow-cli plugin pack <插件目录> [--out 输出目录] [--sign 私钥路径]")
	}

	result, err := plugins.PackPlugin(pluginDir, plugins.PackOptions{OutputDir: outDir})
	if err != nil {
		return err
	}

	fmt.Fprintf(a.stdout, "✓ 打包完成: %s\n", result.ZipPath)
	fmt.Fprintf(a.stdout, "  插件: %s@%s（%d 个文件）\n", result.PluginID, result.Version, result.FileCount)
	fmt.Fprintf(a.stdout, "  校验值（SHA-256）: %s\n", result.Hash)
	fmt.Fprintf(a.stdout, "  校验值文件: %s\n", result.HashPath)

	// P5-B 签名链：pack --sign 对 zip 签名写旁置 .sig（宿主 install 验签）
	if signKeyPath != "" {
		signResult, signErr := plugins.SignPluginZip(result.ZipPath, signKeyPath)
		if signErr != nil {
			return fmt.Errorf("打包成功但签名失败: %w", signErr)
		}
		fmt.Fprintf(a.stdout, "  签名文件: %s\n", signResult.SignaturePath)
		fmt.Fprintf(a.stdout, "  发布者公钥: %s…\n", signResult.PublicKey[:24])
	}

	fmt.Fprintf(a.stdout, "\n私有分发: zip + sha256 校验值（+ .sig 签名）交付给用户本地安装（plugin:install 校验哈希与签名）\n")
	return nil
}

// runPluginListInstalled 处理 plugin list-installed：列出已安装插件
// （读取 {SNZ1DP_HOME}/plugins，与 Electron 宿主共享目录，CLI 只读）。
func (a *App) runPluginListInstalled(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("list-installed 不接受参数，收到 %q", args[0])
	}
	records, err := plugins.ReadInstallRecord()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Fprintln(a.stdout, "暂无已安装插件（安装目录为空）")
		fmt.Fprintf(a.stdout, "安装目录: %s\n", mustInstallRootDir())
		return nil
	}
	fmt.Fprintf(a.stdout, "已安装 %d 个插件:\n", len(records))
	for _, record := range records {
		signMark := "未签名"
		if record.Signed {
			signMark = "已签名"
		}
		fmt.Fprintf(a.stdout, "  %s@%s（%s，安装于 %s）\n", record.ID, record.Version, signMark, record.InstalledAt)
	}
	fmt.Fprintf(a.stdout, "\n安装目录: %s\n", mustInstallRootDir())
	return nil
}

// mustInstallRootDir 获取安装根目录，失败时返回错误说明（展示用）。
func mustInstallRootDir() string {
	rootDir, err := plugins.InstallRootDir()
	if err != nil {
		return fmt.Sprintf("<解析失败: %v>", err)
	}
	return rootDir
}

// runPluginKeygen 处理 plugin keygen：生成 ed25519 发布者密钥对（P5-B 签名链）。
func (a *App) runPluginKeygen(args []string) error {
	var keyDir, name string
	rest := args
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--dir":
			if i+1 >= len(rest) {
				return fmt.Errorf("--dir 缺少参数值")
			}
			i++
			keyDir = rest[i]
		case "--name":
			if i+1 >= len(rest) {
				return fmt.Errorf("--name 缺少参数值")
			}
			i++
			name = rest[i]
		default:
			return fmt.Errorf("未知参数 %q", rest[i])
		}
	}
	result, err := plugins.GeneratePluginKeyPair(keyDir, name)
	if err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "✓ 密钥对生成完成\n")
	fmt.Fprintf(a.stdout, "  私钥（保密）: %s\n", result.PrivateKeyPath)
	fmt.Fprintf(a.stdout, "  公钥（可公开）: %s\n", result.PublicKeyPath)
	fmt.Fprintf(a.stdout, "  公钥指纹: %s\n", result.Fingerprint)
	fmt.Fprintf(a.stdout, "\n用法: taiyiflow-cli plugin pack <插件目录> --sign %s\n", result.PrivateKeyPath)
	fmt.Fprintf(a.stdout, "私钥泄露即失去防劫持能力，请妥善保管（已设 0600 权限）\n")
	return nil
}
