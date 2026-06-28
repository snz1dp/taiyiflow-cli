package cmd

import "strings"

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
