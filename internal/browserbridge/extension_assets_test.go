package browserbridge

import (
	"os"
	"path/filepath"
	"testing"

	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

func TestExtractEmbeddedBrowserExtensionWritesSourceFiles(t *testing.T) {
	t.Setenv(internalconfig.RootDirEnvName(), t.TempDir())
	t.Setenv("HOME", t.TempDir())

	targetDir, err := ExtractEmbeddedBrowserExtension()
	if err != nil {
		t.Fatalf("ExtractEmbeddedBrowserExtension failed: %v", err)
	}
	expectedFiles := []string{
		"manifest.json",
		"package.json",
		"tsconfig.json",
		filepath.Join("scripts", "build.mjs"),
		filepath.Join("src", "background.ts"),
		filepath.Join("src", "chrome.d.ts"),
		filepath.Join("src", "content.ts"),
	}
	for _, relPath := range expectedFiles {
		if _, err := os.Stat(filepath.Join(targetDir, relPath)); err != nil {
			t.Fatalf("expected extracted file %q, got %v", relPath, err)
		}
	}
	unexpectedFiles := []string{
		"README.md",
		"package-lock.json",
		filepath.Join("dist", "manifest.json"),
		filepath.Join("node_modules", ".package-lock.json"),
	}
	for _, relPath := range unexpectedFiles {
		if _, err := os.Stat(filepath.Join(targetDir, relPath)); err == nil {
			t.Fatalf("did not expect extracted file %q", relPath)
		}
	}
}