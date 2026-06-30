package cmd

import (
	"strings"
	"unicode/utf8"
)

// stripInvisibleFormatChars 移除文本中不可见的 Unicode 格式控制字符。
//
// 这些字符属于 Unicode 类别 Cf（Format），在终端中不可见，
// 但会影响文本渲染顺序（尤其是双向格式控制字符），
// 导致 TUI 输出出现文字乱序、重叠等问题。
//
// 移除范围：
//   - U+200E/U+200F: LRM/RLM（双向标记）
//   - U+202A-U+202E: LRE/RLE/PDF/LRO/RLO（双向嵌入与覆盖）
//   - U+2066-U+2069: LRI/RLI/FSI/PDI（双向隔离）
//   - U+206A-U+206F: 已废弃的格式控制字符（ISS/ASS/IAFS/AAFS/NADS/NODS）
//   - U+2060-U+2064: WJ/FUNCTION APPLICATION/INVISIBLE TIMES/INVISIBLE SEPARATOR/INVISIBLE PLUS
//
// 不移除 U+200B（ZWSP）、U+200C（ZWNJ）、U+200D（ZWJ），
// 因为它们在 emoji 组合和某些文字系统中有实际语义。
func stripInvisibleFormatChars(s string) string {
	if !strings.ContainsAny(s, invisibleFormatCharRange) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isInvisibleFormatChar(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// invisibleFormatCharRange 用于快速检测字符串中是否包含任何目标字符。
// 使用字符串而非 rune 切片，配合 strings.ContainsAny 做一次快速路径判断。
var invisibleFormatCharRange = string([]rune{
	0x200E, 0x200F,
	0x202A, 0x202B, 0x202C, 0x202D, 0x202E,
	0x2060, 0x2061, 0x2062, 0x2063, 0x2064,
	0x2066, 0x2067, 0x2068, 0x2069,
	0x206A, 0x206B, 0x206C, 0x206D, 0x206E, 0x206F,
})

func isInvisibleFormatChar(r rune) bool {
	switch {
	case r == 0x200E || r == 0x200F: // LRM, RLM
		return true
	case r >= 0x202A && r <= 0x202E: // LRE, RLE, PDF, LRO, RLO
		return true
	case r >= 0x2060 && r <= 0x2064: // WJ, FUNCTION APPLICATION, INVISIBLE TIMES, INVISIBLE SEPARATOR, INVISIBLE PLUS
		return true
	case r >= 0x2066 && r <= 0x2069: // LRI, RLI, FSI, PDI
		return true
	case r >= 0x206A && r <= 0x206F: // 已废弃: ISS, ASS, IAFS, AAFS, NADS, NODS
		return true
	default:
		return false
	}
}

// trailingIncompleteUTF8 返回字符串末尾不完整的 UTF-8 字节序列。
//
// 当服务端 SSE 分帧在水印字符的 UTF-8 编码中间截断时（例如 U+202E = 0xE2 0x80 0xAE，
// chunk 在 0xE2 后截断），逐 chunk 清洗无法识别不完整的水印字符。
// 本函数用于在清洗前检测并暂存尾部不完整字节，等下一个 chunk 到来后拼接成完整字符再处理。
func trailingIncompleteUTF8(s string) string {
	if len(s) == 0 {
		return ""
	}
	// 从末尾向前跳过续字节（0x80-0xBF）
	i := len(s)
	for i > 0 && (s[i-1]&0xC0) == 0x80 {
		i--
	}
	if i == 0 {
		// 整个字符串都是续字节，无前导字节
		return s
	}
	leading := s[i-1]
	if leading < 0x80 {
		// ASCII 字节，尾部无不完整序列
		return ""
	}
	var expected int
	switch {
	case leading < 0xC0:
		// 0x80-0xBF：孤立的续字节，非有效前导
		return ""
	case leading < 0xE0:
		expected = 2
	case leading < 0xF0:
		expected = 3
	case leading < 0xF8:
		expected = 4
	default:
		// 无效前导字节
		return ""
	}
	actual := len(s) - (i - 1)
	if actual < expected {
		return s[i-1:]
	}
	return ""
}

// stripInvalidUTF8 移除字符串中所有无效 UTF-8 字节。
//
// 在逐 chunk 清洗场景中，被拆分的水印字符可能留下孤立的续字节
// （如 0x80 0xAE），这些字节无法被 StripWatermarkCharacters 识别，
// 也无法被 trailingIncompleteUTF8 暂存（因为它们不是末尾的不完整序列）。
// 本函数作为最后一道防线，确保输出到终端的文本始终是合法 UTF-8。
func stripInvalidUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// 跳过无效字节
			i++
			continue
		}
		b.WriteString(s[i : i+size])
		i += size
	}
	return b.String()
}
