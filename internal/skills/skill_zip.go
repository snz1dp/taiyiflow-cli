package skills

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PackSkillToZip 将本地技能目录打包为内存 ZIP。
//
// ZIP 结构：顶层目录（dirName）下包含 SKILL.md 及技能目录中的所有附属文件。
// 例如：
//
//	my-skill/SKILL.md
//	my-skill/references/notes.md
//	my-skill/scripts/helper.py
//
// 这与 taiyiflow-chatui 的 packSkillToZip 格式一致，后端期望 ZIP 内
// 顶层有一个与技能 slug 同名的目录，该目录下必须存在 SKILL.md。
func PackSkillToZip(item Skill, dirName string) ([]byte, error) {
	if strings.TrimSpace(dirName) == "" {
		return nil, fmt.Errorf("ZIP 顶层目录名不能为空")
	}

	skillDir := SkillDir(item)
	if skillDir == "" {
		// 没有磁盘目录（如内置技能），仅打包 SKILL.md 内容
		return packSkillContentOnly(dirName, Markdown(item))
	}

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	hasSkillMD := false

	// 遍历技能目录，将所有文件写入 ZIP
	walkErr := filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// 计算相对于技能目录的相对路径
		relPath, err := filepath.Rel(skillDir, path)
		if err != nil {
			return fmt.Errorf("计算相对路径失败: %w", err)
		}
		relPath = filepath.ToSlash(relPath)
		// 跳过隐藏文件
		if strings.HasPrefix(relPath, ".") || strings.Contains(relPath, "/.") {
			return nil
		}
		if relPath == "SKILL.md" {
			hasSkillMD = true
		}

		data, err := readFileBytes(path)
		if err != nil {
			return fmt.Errorf("读取文件 %s 失败: %w", path, err)
		}

		zipPath := dirName + "/" + relPath
		entry, err := writer.Create(zipPath)
		if err != nil {
			return fmt.Errorf("创建 ZIP 条目 %s 失败: %w", zipPath, err)
		}
		if _, err := entry.Write(data); err != nil {
			return fmt.Errorf("写入 ZIP 条目 %s 失败: %w", zipPath, err)
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("遍历技能目录失败: %w", walkErr)
	}

	// 如果目录中没有 SKILL.md，补充一个
	if !hasSkillMD {
		zipPath := dirName + "/SKILL.md"
		entry, err := writer.Create(zipPath)
		if err != nil {
			return nil, fmt.Errorf("创建 SKILL.md ZIP 条目失败: %w", err)
		}
		if _, err := entry.Write([]byte(Markdown(item))); err != nil {
			return nil, fmt.Errorf("写入 SKILL.md ZIP 条目失败: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("关闭 ZIP 写入器失败: %w", err)
	}

	return buf.Bytes(), nil
}

// packSkillContentOnly 仅打包 SKILL.md 内容为 ZIP（无附属文件）。
func packSkillContentOnly(dirName, skillMdContent string) ([]byte, error) {
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	zipPath := dirName + "/SKILL.md"
	entry, err := writer.Create(zipPath)
	if err != nil {
		return nil, fmt.Errorf("创建 SKILL.md ZIP 条目失败: %w", err)
	}
	if _, err := entry.Write([]byte(skillMdContent)); err != nil {
		return nil, fmt.Errorf("写入 SKILL.md ZIP 条目失败: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("关闭 ZIP 写入器失败: %w", err)
	}
	return buf.Bytes(), nil
}

// readFileBytes 读取文件内容为字节切片。
func readFileBytes(path string) ([]byte, error) {
	return os.ReadFile(path)
}
