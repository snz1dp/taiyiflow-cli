package tui

import (
	"strings"
	"testing"
)

// TestSanitizeTerminalTextPreservesOrder 验证 sanitizeTerminalText 不会重排字符顺序。
// 构造包含 BiDi 控制字符的文本，清洗后检查可见字符的相对顺序保持不变。
func TestSanitizeTerminalTextPreservesOrder(t *testing.T) {
	// 构造一段包含各种 BiDi 控制字符的文本
	// U+202A (LRE), U+202E (RLO), U+202C (PDF), U+200E (LRM), U+200F (RLM)
	// U+2066 (LRI), U+2067 (RLI), U+2069 (PDI)
	input := "abc\u202adef\u202eghi\u202cjkl\u200emno\u200fpqr\u2066stu\u2067vwx\u2069yz"
	expected := "abcdefghijklmnopqrstuvwxyz"

	got := sanitizeTerminalText(input)
	if got != expected {
		t.Errorf("sanitizeTerminalText changed character order:\n  input  = %q\n  got    = %q\n  expect = %q", input, got, expected)
	}
}

// TestRewriteRetrievalCitationMarkersPreservesOrder 验证 rewriteRetrievalCitationMarkers 不会重排文本顺序。
func TestRewriteRetrievalCitationMarkersPreservesOrder(t *testing.T) {
	input := "前面文字[^1.3^]中间文字^2.5^后面文字"
	got := rewriteRetrievalCitationMarkers(input)

	// 检查可见文字的顺序保持不变（引用标记被替换为 [联网#3] 和 [共享#5]）
	if !strings.Contains(got, "前面文字") || !strings.Contains(got, "中间文字") || !strings.Contains(got, "后面文字") {
		t.Errorf("rewriteRetrievalCitationMarkers lost text segments:\n  input = %q\n  got   = %q", input, got)
	}

	// 验证顺序：前面文字 在 中间文字 之前，中间文字 在 后面文字 之前
	idx1 := strings.Index(got, "前面文字")
	idx2 := strings.Index(got, "中间文字")
	idx3 := strings.Index(got, "后面文字")
	if idx1 < 0 || idx2 < 0 || idx3 < 0 || idx1 >= idx2 || idx2 >= idx3 {
		t.Errorf("rewriteRetrievalCitationMarkers reordered text:\n  input = %q\n  got   = %q\n  idx1=%d idx2=%d idx3=%d", input, got, idx1, idx2, idx3)
	}
}

// TestSanitizeTerminalTextWithChineseAndBiDi 验证中文字符与 BiDi 控制字符混合时顺序不变。
func TestSanitizeTerminalTextWithChineseAndBiDi(t *testing.T) {
	// 模拟实际场景：中文文本中嵌入 BiDi 控制字符
	input := "完成了三\u202a处\u202eoverflow hidden\u202c的移除"
	expected := "完成了三处overflow hidden的移除"

	got := sanitizeTerminalText(input)
	if got != expected {
		t.Errorf("sanitizeTerminalText reordered Chinese+BiDi text:\n  input  = %q\n  got    = %q\n  expect = %q", input, got, expected)
	}
}

// TestSanitizeTerminalTextDoesNotReorderMixedContent 验证包含 ANSI 转义序列和中文混合时顺序不变。
func TestSanitizeTerminalTextDoesNotReorderMixedContent(t *testing.T) {
	// ANSI CSI 序列 + 中文 + BiDi 控制字符
	input := "\x1b[32m绿色文字\u202e\x1b[0m更多文字"
	got := sanitizeTerminalText(input)

	// 清洗后应该只剩可见文字，顺序不变
	expected := "绿色文字更多文字"
	if got != expected {
		t.Errorf("sanitizeTerminalText reordered mixed content:\n  input  = %q\n  got    = %q\n  expect = %q", input, got, expected)
	}
}
