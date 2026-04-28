package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallSkillDependenciesRunsNpmCIWhenPackageLockExists(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake npm script test is only configured for unix-like environments")
	}

	skillDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(skillDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "package-lock.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write package-lock.json failed: %v", err)
	}

	fakeBinDir := t.TempDir()
	logPath := filepath.Join(fakeBinDir, "npm.log")
	nodeModulesDir := filepath.Join(skillDir, "node_modules")
	scriptPath := filepath.Join(fakeBinDir, "npm")
	script := "#!/bin/sh\nset -e\nprintf '%s\\n' \"$@\" > \"$FAKE_NPM_LOG\"\nmkdir -p \"$FAKE_NODE_MODULES\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake npm failed: %v", err)
	}
	t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_NPM_LOG", logPath)
	t.Setenv("FAKE_NODE_MODULES", nodeModulesDir)

	if err := installSkillDependencies(skillDir); err != nil {
		t.Fatalf("install dependencies failed: %v", err)
	}

	loggedArgs, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake npm log failed: %v", err)
	}
	argsText := string(loggedArgs)
	if !strings.Contains(argsText, "ci") {
		t.Fatalf("expected npm ci, got %q", argsText)
	}
	if !strings.Contains(argsText, "--omit=dev") {
		t.Fatalf("expected --omit=dev, got %q", argsText)
	}
	if _, err := os.Stat(nodeModulesDir); err != nil {
		t.Fatalf("expected fake npm to prepare node_modules: %v", err)
	}
}

func TestRepairInstalledSkillDependenciesReinstallsWhenNodeModulesMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake npm script test is only configured for unix-like environments")
	}

	skillDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(skillDir, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write package.json failed: %v", err)
	}

	fakeBinDir := t.TempDir()
	logPath := filepath.Join(fakeBinDir, "npm.log")
	nodeModulesDir := filepath.Join(skillDir, "node_modules")
	scriptPath := filepath.Join(fakeBinDir, "npm")
	script := "#!/bin/sh\nset -e\nprintf '%s\\n' \"$@\" > \"$FAKE_NPM_LOG\"\nmkdir -p \"$FAKE_NODE_MODULES\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake npm failed: %v", err)
	}
	t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_NPM_LOG", logPath)
	t.Setenv("FAKE_NODE_MODULES", nodeModulesDir)

	repaired, err := repairInstalledSkillDependencies(skillDir)
	if err != nil {
		t.Fatalf("repair dependencies failed: %v", err)
	}
	if !repaired {
		t.Fatal("expected repairInstalledSkillDependencies to report repaired=true")
	}
	if _, err := os.Stat(nodeModulesDir); err != nil {
		t.Fatalf("expected fake npm to recreate node_modules: %v", err)
	}
}