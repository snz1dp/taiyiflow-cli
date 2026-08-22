package cmd

import (
	"strings"
	"testing"
)

// --- daemon 常量测试 ---

func TestDaemonLabelAndUnitName(t *testing.T) {
	if daemonLabel != "cn.snz1dp.taiyiflow-cli.worker" {
		t.Fatalf("unexpected daemon label: %q", daemonLabel)
	}
	if daemonUnitName != "taiyiflow-cli-worker" {
		t.Fatalf("unexpected daemon unit name: %q", daemonUnitName)
	}
}

// --- daemonLogDir 测试 ---

func TestDaemonLogDir(t *testing.T) {
	dir, err := daemonLogDir()
	if err != nil {
		t.Fatalf("daemonLogDir failed: %v", err)
	}
	if !strings.Contains(dir, ".snz1dp") {
		t.Fatalf("expected .snz1dp in log dir, got %q", dir)
	}
	if !strings.Contains(dir, "logs") {
		t.Fatalf("expected logs in log dir, got %q", dir)
	}
}

// --- daemonExecutable 测试 ---

func TestDaemonExecutable(t *testing.T) {
	executable, err := daemonExecutable()
	if err != nil {
		t.Fatalf("daemonExecutable failed: %v", err)
	}
	if executable == "" {
		t.Fatal("expected non-empty executable path")
	}
}

// --- daemonPlistPath 测试 ---

func TestDaemonPlistPath(t *testing.T) {
	path, err := daemonPlistPath()
	if err != nil {
		t.Fatalf("daemonPlistPath failed: %v", err)
	}
	if !strings.Contains(path, "LaunchAgents") {
		t.Fatalf("expected LaunchAgents in plist path, got %q", path)
	}
	if !strings.Contains(path, daemonLabel+".plist") {
		t.Fatalf("expected label in plist path, got %q", path)
	}
}

// --- daemonUnitPath 测试 ---

func TestDaemonUnitPath(t *testing.T) {
	path, err := daemonUnitPath()
	if err != nil {
		t.Fatalf("daemonUnitPath failed: %v", err)
	}
	if !strings.Contains(path, "systemd") {
		t.Fatalf("expected systemd in unit path, got %q", path)
	}
	if !strings.Contains(path, daemonUnitName+".service") {
		t.Fatalf("expected unit name in path, got %q", path)
	}
}

// --- runDaemon 路由测试 ---

func TestRunDaemonUnknownSubcommand(t *testing.T) {
	var stdout strings.Builder
	app := &App{stderr: &strings.Builder{}, stdout: &stdout}
	err := app.runDaemon(nil, []string{"unknown"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "未知守护进程子命令") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunDaemonNoArgsShowsUsage(t *testing.T) {
	var stdout strings.Builder
	app := &App{stderr: &strings.Builder{}, stdout: &stdout}
	err := app.runDaemon(nil, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "守护进程") {
		t.Fatalf("expected usage output, got %q", stdout.String())
	}
}
