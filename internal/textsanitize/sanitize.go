package textsanitize

import "strings"

var watermarkCharReplacer = strings.NewReplacer(
	"\u200b", "",
	"\u200c", "",
	"\u200d", "",
	"\u2060", "",
	"\ufeff", "",
	"\u2061", "",
	"\u2062", "",
	"\u2063", "",
	"\u2064", "",
	"\u180e", "",
	"\u200e", "",
	"\u200f", "",
	"\u202a", "",
	"\u202b", "",
	"\u202c", "",
	"\u202d", "",
	"\u206a", "",
	"\u206b", "",
)

// StripWatermarkCharacters 移除模型输出中常见的零宽与水印字符。
func StripWatermarkCharacters(content string) string {
	if content == "" {
		return ""
	}
	return watermarkCharReplacer.Replace(content)
}
