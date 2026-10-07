package plugins

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PackResult 表示 plugin pack 的产物信息。
type PackResult struct {
	PluginID  string // 插件 id（来自 manifest.id）
	Version   string // 插件版本（来自 manifest.version）
	ZipPath   string // 产物 zip 绝对路径（{plugin_id}-{version}.zip）
	HashPath  string // 旁置 hash 文件绝对路径（{plugin_id}-{version}.zip.sha256）
	Hash      string // zip 的 SHA-256 十六进制小写摘要
	FileCount int    // zip 内文件条目数
}

// PackOptions 控制 plugin pack 行为。
type PackOptions struct {
	OutputDir string // 产物输出目录（默认插件目录同级，即插件父目录）
}

// PackPlugin 打包插件工程为可分发产物（DevKit 批次 3，plugin pack 命令核心实现）。
//
// 流程（对齐架构文档 11.1 组件 6 与实施计划 5.1）：
//  1. 读取并 schema 校验 plugin.json（发布前强制 validate，校验失败拒绝打包）；
//  2. 遍历插件目录全部文件（跳过隐藏文件/目录，如 .DS_Store、.git），
//     以顶层目录（插件目录名）为前缀写入 ZIP（对齐 skill_zip 格式，
//     后端/宿主解包后顶层有与插件目录同名的目录）；
//  3. 计算 ZIP 字节流的 SHA-256，写旁置 .sha256 文件（内容为「hex  文件名」格式，
//     与 sha256sum 工具输出一致，便于安装侧校验）。
//
// 参数：
//   - pluginDir: 插件工程根目录（须含 plugin.json）
//   - opts: 打包选项（OutputDir 为空时输出到插件父目录）
func PackPlugin(pluginDir string, opts PackOptions) (PackResult, error) {
	absDir, err := filepath.Abs(pluginDir)
	if err != nil {
		return PackResult{}, fmt.Errorf("解析插件目录失败: %w", err)
	}
	manifestPath := filepath.Join(absDir, "plugin.json")

	// 步骤 1：发布前强制 validate（校验失败拒绝打包）
	result, err := ValidateManifestFile(manifestPath)
	if err != nil {
		return PackResult{}, err
	}
	if !result.Valid {
		var sb strings.Builder
		for _, validationErr := range result.Errors {
			sb.WriteString("\n  " + validationErr.Error())
		}
		return PackResult{}, fmt.Errorf("manifest 校验失败，拒绝打包:%s", sb.String())
	}

	// 读取 id / version 用于产物命名
	manifestInfo, err := readManifestIdentity(manifestPath)
	if err != nil {
		return PackResult{}, err
	}
	if manifestInfo.ID == "" {
		return PackResult{}, fmt.Errorf("manifest 缺少 id 字段: %s", manifestPath)
	}
	if manifestInfo.Version == "" {
		return PackResult{}, fmt.Errorf("manifest 缺少 version 字段: %s", manifestPath)
	}

	// 步骤 2：打包为 ZIP（顶层目录 = 插件目录名）
	zipBytes, fileCount, err := packPluginToZip(absDir)
	if err != nil {
		return PackResult{}, err
	}

	// 步骤 3：计算 SHA-256 并落盘产物
	outputDir := opts.OutputDir
	if outputDir == "" {
		outputDir = filepath.Dir(absDir)
	}
	absOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return PackResult{}, fmt.Errorf("解析输出目录失败: %w", err)
	}
	if err := os.MkdirAll(absOutput, 0o755); err != nil {
		return PackResult{}, fmt.Errorf("创建输出目录 %s 失败: %w", absOutput, err)
	}

	zipName := fmt.Sprintf("%s-%s.zip", manifestInfo.ID, manifestInfo.Version)
	zipPath := filepath.Join(absOutput, zipName)
	if err := os.WriteFile(zipPath, zipBytes, 0o644); err != nil {
		return PackResult{}, fmt.Errorf("写入 %s 失败: %w", zipPath, err)
	}

	sum := sha256.Sum256(zipBytes)
	hashHex := hex.EncodeToString(sum[:])
	hashPath := zipPath + ".sha256"
	if err := os.WriteFile(hashPath, []byte(hashHex+"  "+zipName+"\n"), 0o644); err != nil {
		return PackResult{}, fmt.Errorf("写入 %s 失败: %w", hashPath, err)
	}

	return PackResult{
		PluginID:  manifestInfo.ID,
		Version:   manifestInfo.Version,
		ZipPath:   zipPath,
		HashPath:  hashPath,
		Hash:      hashHex,
		FileCount: fileCount,
	}, nil
}

// manifestIdentity 表示从 manifest 提取的产物命名所需字段。
type manifestIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// readManifestIdentity 读取 manifest 的 id / version 字段。
func readManifestIdentity(manifestPath string) (manifestIdentity, error) {
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return manifestIdentity{}, fmt.Errorf("读取 %s 失败: %w", manifestPath, err)
	}
	var identity manifestIdentity
	if err := json.Unmarshal(content, &identity); err != nil {
		return manifestIdentity{}, fmt.Errorf("解析 %s 失败: %w", manifestPath, err)
	}
	return identity, nil
}

// packPluginToZip 把插件目录打包为内存 ZIP，返回字节流与文件条目数。
// ZIP 结构：顶层目录（插件目录名）下包含 plugin.json 及全部附属文件，例如：
//
//	hello-plugin/plugin.json
//	hello-plugin/ui/icon.svg
//	hello-plugin/prompts/hello.json
//
// 跳过隐藏文件与隐藏目录（.DS_Store、.git、.plugin-dev.json 不入包）。
// 条目按路径排序写入，保证同内容目录产出确定性 ZIP（hash 可复现）。
func packPluginToZip(pluginDir string) ([]byte, int, error) {
	dirName := filepath.Base(pluginDir)
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	type entry struct {
		relPath string
		data    []byte
	}
	var entries []entry

	walkErr := filepath.WalkDir(pluginDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(pluginDir, path)
		if err != nil {
			return fmt.Errorf("计算相对路径失败: %w", err)
		}
		relPath = filepath.ToSlash(relPath)
		// 跳过隐藏文件（含 .DS_Store、.plugin-dev.json 等）
		if strings.HasPrefix(relPath, ".") || strings.Contains(relPath, "/.") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取文件 %s 失败: %w", path, err)
		}
		entries = append(entries, entry{relPath: relPath, data: data})
		return nil
	})
	if walkErr != nil {
		return nil, 0, fmt.Errorf("遍历插件目录失败: %w", walkErr)
	}

	// 排序保证确定性输出（同内容目录两次打包 hash 一致）
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relPath < entries[j].relPath
	})

	for _, e := range entries {
		zipPath := dirName + "/" + e.relPath
		zipEntry, err := writer.Create(zipPath)
		if err != nil {
			return nil, 0, fmt.Errorf("创建 ZIP 条目 %s 失败: %w", zipPath, err)
		}
		if _, err := zipEntry.Write(e.data); err != nil {
			return nil, 0, fmt.Errorf("写入 ZIP 条目 %s 失败: %w", zipPath, err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, 0, fmt.Errorf("关闭 ZIP 写入器失败: %w", err)
	}
	return buf.Bytes(), len(entries), nil
}
