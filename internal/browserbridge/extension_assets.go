package browserbridge

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	browserextensionassets "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/browser-extension"
	internalconfig "snz1.cn/gitrepo/dp/ai/taiyi/taiyiflow-cli/internal/config"
)

type BrowserExtensionProjectStatus struct {
	Directory        string
	PackageJSONPath  string
	ManifestPath     string
	DistDir          string
	DistManifestPath string
	NPMPath          string
	NPMAvailable     bool
	SourceReady      bool
	DistReady        bool
}

func ExtractEmbeddedBrowserExtension() (string, error) {
	rootDir, err := internalconfig.DefaultRootDir()
	if err != nil {
		return "", err
	}
	targetRoot := filepath.Join(rootDir, "browser", "extension", "chrome")
	if err := extractEmbeddedBrowserExtensionTo(targetRoot); err != nil {
		return "", err
	}
	return targetRoot, nil
}

func extractEmbeddedBrowserExtensionTo(targetRoot string) error {
	subFS, err := browserextensionassets.ChromeSubFS()
	if err != nil {
		return fmt.Errorf("解析内置浏览器扩展资源失败: %w", err)
	}
	if err := fs.WalkDir(subFS, ".", func(relPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if relPath == "." {
			return os.MkdirAll(targetRoot, 0o755)
		}
		targetPath := filepath.Join(targetRoot, filepath.FromSlash(relPath))
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		content, err := fs.ReadFile(subFS, relPath)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(targetPath, content, 0o644)
	}); err != nil {
		return fmt.Errorf("写入内置浏览器扩展资源失败: %w", err)
	}
	return nil
}

func InspectBrowserExtensionProject(extensionDir string) BrowserExtensionProjectStatus {
	status := BrowserExtensionProjectStatus{Directory: strings.TrimSpace(extensionDir)}
	if status.Directory == "" {
		return status
	}
	status.PackageJSONPath = filepath.Join(status.Directory, "package.json")
	status.ManifestPath = filepath.Join(status.Directory, "manifest.json")
	status.DistDir = filepath.Join(status.Directory, "dist")
	status.DistManifestPath = filepath.Join(status.DistDir, "manifest.json")
	if fileExists(status.PackageJSONPath) && fileExists(status.ManifestPath) {
		status.SourceReady = true
	}
	if fileExists(status.DistManifestPath) {
		status.DistReady = true
	} else if fileExists(status.ManifestPath) && fileExists(filepath.Join(status.Directory, "background.js")) && fileExists(filepath.Join(status.Directory, "content.js")) {
		status.DistDir = status.Directory
		status.DistManifestPath = status.ManifestPath
		status.DistReady = true
	}
	if npmPath, err := exec.LookPath("npm"); err == nil {
		status.NPMPath = npmPath
		status.NPMAvailable = true
	}
	return status
}

func fileExists(pathValue string) bool {
	info, err := os.Stat(pathValue)
	return err == nil && !info.IsDir()
}
