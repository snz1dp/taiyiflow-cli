package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestRunBrowserInstallWritesManifestAndMentionsExtensionProject(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	if err := manager.Save(internalconfig.Default()); err != nil {
		t.Fatalf("save default config failed: %v", err)
	}
	workspace := t.TempDir()
	extensionDir := filepath.Join(workspace, "browser-extension", "chrome")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatalf("mkdir extension dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "manifest.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write manifest.json failed: %v", err)
	}
	previousDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(previousDir) }()
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace failed: %v", err)
	}
	stdout := &bytes.Buffer{}
	app := &App{configManager: manager, stdout: stdout, stderr: &bytes.Buffer{}}
	if err := app.runBrowserInstall(nil); err != nil {
		t.Fatalf("runBrowserInstall failed: %v", err)
	}
	output := stdout.String()
	for _, expected := range []string{"已安装浏览器本地消息宿主", "清单文件路径:", "启动脚本路径:", "扩展标识:", extensionDir, "扩展来源: 工作区项目", "自动构建命令", "首次使用路径"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got %q", expected, output)
		}
	}
	statusPath, err := internalconfig.DefaultRootDir()
	if err != nil {
		t.Fatalf("default root dir failed: %v", err)
	}
	manifestPath := filepath.Join(t.TempDir(), "ignore")
	_ = manifestPath
	if _, err := os.Stat(filepath.Join(statusPath, "browser", "native-messaging", "chrome-extension-state.json")); err != nil {
		t.Fatalf("expected native messaging state file to exist, got %v", err)
	}
}

func TestRunBrowserInstallFallsBackToEmbeddedExtensionProject(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	if err := manager.Save(internalconfig.Default()); err != nil {
		t.Fatalf("save default config failed: %v", err)
	}
	workspace := t.TempDir()
	previousDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(previousDir) }()
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace failed: %v", err)
	}
	stdout := &bytes.Buffer{}
	app := &App{configManager: manager, stdout: stdout, stderr: &bytes.Buffer{}}
	if err := app.runBrowserInstall(nil); err != nil {
		t.Fatalf("runBrowserInstall failed: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "扩展来源: 内置运行产物") {
		t.Fatalf("expected output to mention embedded extension source, got %q", output)
	}
	if strings.Contains(output, "自动构建命令") {
		t.Fatalf("expected embedded bundled extension to avoid build hint, got %q", output)
	}
	rootDir, err := internalconfig.DefaultRootDir()
	if err != nil {
		t.Fatalf("default root dir failed: %v", err)
	}
	embeddedDir := filepath.Join(rootDir, "browser", "extension", "chrome")
	for _, required := range []string{"manifest.json", "background.js", "content.js"} {
		if _, err := os.Stat(filepath.Join(embeddedDir, required)); err != nil {
			t.Fatalf("expected embedded extension asset %s to exist, got %v", required, err)
		}
	}
	for _, unexpected := range []string{"package.json", filepath.Join("src", "background.ts"), filepath.Join("dist", "manifest.json")} {
		if _, err := os.Stat(filepath.Join(embeddedDir, unexpected)); err == nil {
			t.Fatalf("did not expect embedded extension asset %s", unexpected)
		}
	}
}

func TestRunBrowserInstallBuildsExtensionWhenRequested(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())
	manager, err := internalconfig.NewManager()
	if err != nil {
		t.Fatalf("new manager failed: %v", err)
	}
	if err := manager.Save(internalconfig.Default()); err != nil {
		t.Fatalf("save default config failed: %v", err)
	}
	workspace := t.TempDir()
	extensionDir := filepath.Join(workspace, "browser-extension", "chrome")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatalf("mkdir extension dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "manifest.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write manifest.json failed: %v", err)
	}
	previousDir, _ := os.Getwd()
	defer func() { _ = os.Chdir(previousDir) }()
	if err := os.Chdir(workspace); err != nil {
		t.Fatalf("chdir workspace failed: %v", err)
	}
	originalLookupPath := browserInstallLookupPath
	originalRunCommand := browserInstallRunCommand
	defer func() {
		browserInstallLookupPath = originalLookupPath
		browserInstallRunCommand = originalRunCommand
	}()
	commands := []string{}
	browserInstallLookupPath = func(name string) (string, error) {
		return "/usr/bin/npm", nil
	}
	browserInstallRunCommand = func(ctx context.Context, dir string, name string, args ...string) (string, error) {
		commands = append(commands, name+" "+strings.Join(args, " "))
		if len(args) >= 2 && args[0] == "run" && args[1] == "build" {
			if err := os.MkdirAll(filepath.Join(dir, "dist"), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(dir, "dist", "manifest.json"), []byte("{}\n"), 0o644); err != nil {
				return "", err
			}
		}
		return "ok", nil
	}
	stdout := &bytes.Buffer{}
	app := &App{configManager: manager, stdout: stdout, stderr: &bytes.Buffer{}}
	if err := app.runBrowserInstall([]string{"--build"}); err != nil {
		t.Fatalf("runBrowserInstall failed: %v", err)
	}
	if len(commands) != 2 {
		t.Fatalf("expected 2 npm commands, got %#v", commands)
	}
	if !strings.Contains(commands[0], "install") || !strings.Contains(commands[1], "run build") {
		t.Fatalf("expected npm install and npm run build, got %#v", commands)
	}
	output := stdout.String()
	if !strings.Contains(output, "构建验证: 已执行 npm install && npm run build") {
		t.Fatalf("expected build summary in output, got %q", output)
	}
	if !strings.Contains(output, filepath.Join(extensionDir, "dist")) {
		t.Fatalf("expected build dir in output, got %q", output)
	}
}

func TestDetectChromeExtensionProjectDirSearchesParents(t *testing.T) {
	workspace := t.TempDir()
	extensionDir := filepath.Join(workspace, "browser-extension", "chrome")
	nestedDir := filepath.Join(workspace, "internal", "browserbridge")
	if err := os.MkdirAll(extensionDir, 0o755); err != nil {
		t.Fatalf("mkdir extension dir failed: %v", err)
	}
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("mkdir nested dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(extensionDir, "manifest.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write manifest.json failed: %v", err)
	}
	if got := detectChromeExtensionProjectDir(nestedDir); got != extensionDir {
		t.Fatalf("expected extension dir %s, got %s", extensionDir, got)
	}
}