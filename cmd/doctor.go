package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func (a *App) runDoctor(args []string) error {
	config, err := a.configManager.Load()
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	backendSourcePath := filepath.Clean(filepath.Join(cwd, "..", "taiyiflow", "src", "backend", "base"))
	backendExists := pathExists(backendSourcePath)

	fmt.Fprintln(a.stdout, "太乙智启命令行（CLI）自检结果")
	fmt.Fprintf(a.stdout, "- Go 运行时: %s\n", runtime.Version())
	fmt.Fprintf(a.stdout, "- 当前工作目录: %s\n", cwd)
	fmt.Fprintf(a.stdout, "- 配置文件路径: %s\n", a.configManager.Path())
	fmt.Fprintf(a.stdout, "- 配置文件是否存在: %t\n", a.configManager.Exists())
	fmt.Fprintf(a.stdout, "- 后端服务地址: %s\n", config.BackendBaseURL)
	fmt.Fprintf(a.stdout, "- 默认 flow: %s\n", emptyAs(config.DefaultFlow, "<未设置>"))
	fmt.Fprintf(a.stdout, "- sibling 后端源码路径: %s\n", backendSourcePath)
	fmt.Fprintf(a.stdout, "- sibling 后端源码是否存在: %t\n", backendExists)

	if !backendExists {
		fmt.Fprintln(a.stdout, "提示: 未在当前工作目录的 sibling 路径发现 taiyiflow 后端源码。")
	}

	return nil
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func emptyAs(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
