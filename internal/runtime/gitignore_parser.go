package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// gitignoreRule 表示一条 .gitignore 规则。
type gitignoreRule struct {
	pattern  string         // 原始模式（已去除前导 ! 和尾 /）
	negation  bool          // 是否为取反规则（! 前缀）
	dirOnly   bool          // 是否仅匹配目录（尾 / 限定）
	regex     *regexp.Regexp // 编译后的正则表达式
	baseDir   string        // 规则所属的 .gitignore 文件所在目录
}

// gitignoreCacheEntry 缓存条目：仅在 mtime 或 size 变化时重新解析。
type gitignoreCacheEntry struct {
	rules    []gitignoreRule
	mtime    time.Time
	size     int64
}

// gitignoreCache 全局缓存：key=目录绝对路径。
// 通过 mtime+size 判断文件是否变更，未变更则直接返回缓存结果。
var gitignoreCache sync.Map // map[string]gitignoreCacheEntry

// loadGitignoreRules 加载目录中的 .gitignore 并合并到已有规则。
// 支持嵌套 .gitignore：子目录的规则继承父目录的规则。
func loadGitignoreRules(dir string, parentRules []gitignoreRule) []gitignoreRule {
	localRules := parseGitignoreFile(dir)
	if len(localRules) == 0 {
		return parentRules
	}
	// 为本地规则记录基准目录
	for i := range localRules {
		localRules[i].baseDir = dir
	}
	return append(parentRules, localRules...)
}

// parseGitignoreFile 解析指定目录下的 .gitignore 文件（带缓存）。
// 通过 mtime+size 判断文件是否变更，未变更则直接返回缓存结果。
// 支持: 注释、negation(!)、dir-only(尾/)、glob(*, **, ?)。
func parseGitignoreFile(dirPath string) []gitignoreRule {
	gitignorePath := filepath.Join(dirPath, ".gitignore")
	stat, err := os.Stat(gitignorePath)
	if err != nil {
		// 不存在 .gitignore，清理可能过期的缓存
		gitignoreCache.Delete(dirPath)
		return nil
	}

	// 检查缓存：mtime 和 size 均未变化则直接复用
	if cached, ok := gitignoreCache.Load(dirPath); ok {
		entry := cached.(gitignoreCacheEntry)
		if entry.mtime.Equal(stat.ModTime()) && entry.size == stat.Size() {
			return entry.rules
		}
	}

	// 缓存失效或首次加载，读取并解析
	content, err := os.ReadFile(gitignorePath)
	if err != nil {
		return nil
	}

	rules := parseGitignoreContent(string(content))

	// 写入缓存
	gitignoreCache.Store(dirPath, gitignoreCacheEntry{
		rules: rules,
		mtime: stat.ModTime(),
		size:  stat.Size(),
	})
	return rules
}

// parseGitignoreContent 将 .gitignore 文本内容解析为结构化规则列表。
func parseGitignoreContent(content string) []gitignoreRule {
	var rules []gitignoreRule
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		rule := gitignoreRule{}
		pattern := line

		// negation: ! 前缀
		if strings.HasPrefix(pattern, "!") {
			rule.negation = true
			pattern = pattern[1:]
		}

		// dir-only: 尾 / 限定
		if strings.HasSuffix(pattern, "/") {
			rule.dirOnly = true
			pattern = strings.TrimRight(pattern, "/")
		}

		// 根路径限定: 前导 / 表示从 .gitignore 所在目录开始匹配
		if strings.HasPrefix(pattern, "/") {
			pattern = pattern[1:]
		}

		rule.pattern = pattern
		rule.regex = gitignorePatternToRegex(pattern)
		rules = append(rules, rule)
	}
	return rules
}

// gitignorePatternToRegex 将 gitignore glob 模式转换为正则表达式。
// 支持 * (匹配除 / 外的任意字符)、** (匹配任意路径段)、? (匹配除 / 外的单个字符)。
func gitignorePatternToRegex(pattern string) *regexp.Regexp {
	p := pattern
	// 转义正则特殊字符（保留 * ? /）
	p = regexp.QuoteMeta(p)
	// QuoteMeta 会把 * 转义为 \*，需要还原
	p = strings.ReplaceAll(p, "\\*", "*")
	p = strings.ReplaceAll(p, "\\?", "?")

	// ** 占位
	p = strings.ReplaceAll(p, "**", "{{DOUBLE_STAR}}")
	// * → 匹配除 / 外的任意字符
	p = strings.ReplaceAll(p, "*", "[^/]*")
	// ? → 匹配除 / 外的单个字符
	p = strings.ReplaceAll(p, "?", "[^/]")
	// 还原 ** → .*
	p = strings.ReplaceAll(p, "{{DOUBLE_STAR}}", ".*")

	// 判断是否含 /（不含 / 的模式可匹配任意层级）
	if strings.Contains(pattern, "/") {
		// 含 / 的模式：从 .gitignore 所在目录开始匹配完整路径
		return regexp.MustCompile("^" + p + "$")
	}
	// 不含 / 的模式：可匹配任意层级的 basename 或相对路径
	return regexp.MustCompile("(^|/)" + p + "$")
}

// isGitignored 检查路径是否应被 .gitignore 规则忽略。
// 遍历规则列表，最后一条匹配的规则决定结果（支持 negation 取反）。
// 每条规则根据自身 baseDir 计算相对路径，支持嵌套 .gitignore。
func isGitignored(rules []gitignoreRule, absPath string, searchRoot string, isDir bool) bool {
	ignored := false
	for _, rule := range rules {
		if rule.dirOnly && !isDir {
			continue
		}
		// 每条规则的路径相对于其 .gitignore 所在目录
		base := rule.baseDir
		if base == "" {
			base = searchRoot
		}
		rel, relErr := filepath.Rel(base, absPath)
		if relErr != nil {
			continue
		}
		ruleRel := "/" + filepath.ToSlash(rel)
		// 嵌套 .gitignore 的规则不影响其目录之外的文件
		if strings.HasPrefix(ruleRel, "/..") {
			continue
		}
		if rule.regex.MatchString(ruleRel) {
			ignored = !rule.negation
		}
	}
	return ignored
}

// clearGitignoreCache 清除所有 .gitignore 缓存。
func clearGitignoreCache() {
	gitignoreCache.Range(func(key, _ any) bool {
		gitignoreCache.Delete(key)
		return true
	})
}
