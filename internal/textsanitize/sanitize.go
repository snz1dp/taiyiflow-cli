package textsanitize

import "strings"

var watermarkCharReplacer = strings.NewReplacer(
	// 零宽字符（对应 Python watermark service.py 中的 _ZW_CHARS 编码表）
	"\u200b", "", // ZWSP - ZERO WIDTH SPACE
	"\u200c", "", // ZWNJ - ZERO WIDTH NON-JOINER
	"\u200d", "", // ZWJ  - ZERO WIDTH JOINER
	"\u2060", "", // WJ   - WORD JOINER
	"\ufeff", "", // BOM  - ZERO WIDTH NO-BREAK SPACE
	"\u2061", "", // FUNCTION APPLICATION
	"\u2062", "", // INVISIBLE TIMES
	"\u2063", "", // INVISIBLE SEPARATOR
	"\u2064", "", // INVISIBLE PLUS
	"\u180e", "", // MONGOLIAN VOWEL SEPARATOR
	"\u200e", "", // LRM  - LEFT-TO-RIGHT MARK
	"\u200f", "", // RLM  - RIGHT-TO-LEFT MARK
	// 双向格式控制（对应 _ZW_CHARS 中的 C-F 以及额外的 RLO）
	"\u202a", "", // LRE  - LEFT-TO-RIGHT EMBEDDING
	"\u202b", "", // RLE  - RIGHT-TO-LEFT EMBEDDING
	"\u202c", "", // PDF  - POP DIRECTIONAL FORMATTING
	"\u202d", "", // LRO  - LEFT-TO-RIGHT OVERRIDE
	"\u202e", "", // RLO  - RIGHT-TO-LEFT OVERRIDE
	// 水印起止标记
	"\u206a", "", // ISS  - INHIBIT SYMMETRIC SWAPING (水印起始标记)
	"\u206b", "", // ASS  - ACTIVATE SYMMETRIC SWAPING (水印结束标记)
	// 双向隔离符（Unicode 6.3 新增，不在 Python 水印编码表中但同样会导致渲染乱序）
	"\u2066", "", // LRI  - LEFT-TO-RIGHT ISOLATE
	"\u2067", "", // RLI  - RIGHT-TO-LEFT ISOLATE
	"\u2068", "", // FSI  - FIRST STRONG ISOLATE
	"\u2069", "", // PDI  - POP DIRECTIONAL ISOLATE
	// 废弃格式控制符
	"\u206c", "", // IAFS - INHIBIT ARABIC FORM SHAPING
	"\u206d", "", // AAFS - ACTIVATE ARABIC FORM SHAPING
	"\u206e", "", // NADS - NATIONAL DIGIT SHAPES
	"\u206f", "", // NODS - NOMINAL DIGIT SHAPES
)

// StripWatermarkCharacters 移除模型输出中常见的零宽与水印字符。
func StripWatermarkCharacters(content string) string {
	if content == "" {
		return ""
	}
	return watermarkCharReplacer.Replace(content)
}
