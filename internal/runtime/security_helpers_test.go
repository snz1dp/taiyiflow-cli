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
