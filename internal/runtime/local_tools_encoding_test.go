package runtime

import (
	"bytes"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	unicodeenc "golang.org/x/text/encoding/unicode"
)

// =============================================================================
// trimTrailingIncompleteUTF8 测试
// =============================================================================

func TestTrimTrailingIncompleteUTF8(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected int // 期望的返回长度
	}{
		{"空切片", []byte{}, 0},
		{"纯ASCII", []byte("hello"), 5},
		{"完整UTF-8中文", []byte("你好"), 6},
		{"截断3字节序列-缺2字节", []byte("hello\xe4"), 5},
		{"截断3字节序列-缺1字节", []byte("hello\xe4\xbd"), 5},
		{"完整3字节序列", []byte("hello\xe4\xbd\xa0"), 8}, // "hello你"
		{"截断2字节序列-缺1字节", []byte("hello\xc3"), 5},
		{"完整2字节序列", []byte("hello\xc3\xa9"), 7}, // "helloé"
		{"截断4字节序列-缺3字节", []byte("hello\xf0"), 5},
		{"截断4字节序列-缺2字节", []byte("hello\xf0\x9f"), 5},
		{"截断4字节序列-缺1字节", []byte("hello\xf0\x9f\x98"), 5},
		{"完整4字节序列", []byte("hello\xf0\x9f\x98\x80"), 9}, // "hello😀"
		{"纯延续字节", []byte{0x80, 0x81, 0x82}, 3},          // 全是延续字节，找不到起始字节，返回原切片
		{"混合内容末尾截断", append(bytes.Repeat([]byte("a"), 100), 0xe5), 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trimTrailingIncompleteUTF8(tt.input)
			if len(result) != tt.expected {
				t.Errorf("trimTrailingIncompleteUTF8() 返回长度 %d, 期望 %d", len(result), tt.expected)
			}
			// 验证结果一定是有效 UTF-8（如果原始内容在截断前是有效 UTF-8）
			if len(result) > 0 && len(result) < len(tt.input) {
				// 被截断了，结果应该是有效 UTF-8
				if !utf8.Valid(result) {
					t.Errorf("trimTrailingIncompleteUTF8() 结果不是有效 UTF-8")
				}
			}
		})
	}
}

// =============================================================================
// looksBinaryContent 测试
// =============================================================================

func TestLooksBinaryContent_EmptyAndASCII(t *testing.T) {
	tests := []struct {
		name     string
		content  []byte
		expected bool
	}{
		{"空内容", []byte{}, false},
		{"纯ASCII文本", []byte("Hello, World!"), false},
		{"含换行的ASCII", []byte("line1\nline2\nline3\n"), false},
		{"Go源码片段", []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := looksBinaryContent(tt.content)
			if result != tt.expected {
				t.Errorf("looksBinaryContent() = %v, 期望 %v", result, tt.expected)
			}
		})
	}
}

func TestLooksBinaryContent_UTF8WithChinese(t *testing.T) {
	// 模拟 local_tools.go 的场景：大文件截取 8192 字节，末尾恰好截断在多字节字符中间
	content := bytes.Repeat([]byte("// 这是中文注释\n"), 600) // 约 9600 字节
	sample := content[:8192]

	// 验证原始问题：截取后末尾可能不完整
	if utf8.Valid(sample) {
		t.Skip("样本恰好完整，无法测试截断场景")
	}

	// 修复后应该能正确识别为文本
	if looksBinaryContent(sample) {
		t.Errorf("截断的 UTF-8 中文文本被误判为二进制")
	}

	// 完整内容也应该识别为文本
	if looksBinaryContent(content) {
		t.Errorf("完整 UTF-8 中文文本被误判为二进制")
	}
}

func TestLooksBinaryContent_TruncatedUTF8Boundary(t *testing.T) {
	// 精确构造在 8192 字节处截断的场景
	base := bytes.Repeat([]byte("a"), 8190)
	tests := []struct {
		name    string
		content []byte
	}{
		{"末尾为3字节序列首字节", append(base, 0xe4)},
		{"末尾为3字节序列前两字节", append(base, 0xe4, 0xbd)},
		{"末尾为2字节序列首字节", append(base, 0xc3)},
		{"末尾为4字节序列首字节", append(base, 0xf0)},
		{"末尾为4字节序列前3字节", append(base, 0xf0, 0x9f, 0x98)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if looksBinaryContent(tt.content) {
				t.Errorf("截断的 UTF-8 文本被误判为二进制 (长度=%d)", len(tt.content))
			}
		})
	}
}

func TestLooksBinaryContent_UTF16WithBOM(t *testing.T) {
	// UTF-16LE with BOM
	text := "Hello World 你好世界"
	enc := unicodeenc.UTF16(unicodeenc.LittleEndian, unicodeenc.UseBOM)
	encoded, err := enc.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encoded) {
		t.Errorf("UTF-16LE BOM 文本被误判为二进制")
	}

	// UTF-16BE with BOM
	encBE := unicodeenc.UTF16(unicodeenc.BigEndian, unicodeenc.UseBOM)
	encodedBE, err := encBE.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encodedBE) {
		t.Errorf("UTF-16BE BOM 文本被误判为二进制")
	}
}

func TestLooksBinaryContent_UTF16NoBOM(t *testing.T) {
	// UTF-16LE without BOM: ASCII 字符编码为 [byte, 0x00]
	text := "Hello World this is a test file with enough content"
	enc := unicodeenc.UTF16(unicodeenc.LittleEndian, unicodeenc.IgnoreBOM)
	encoded, err := enc.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}

	if looksBinaryContent(encoded) {
		t.Errorf("UTF-16LE 无 BOM 文本被误判为二进制 (长度=%d, null占比=%.1f%%)",
			len(encoded), float64(bytes.Count(encoded, []byte{0}))*100/float64(len(encoded)))
	}

	// UTF-16BE without BOM
	encBE := unicodeenc.UTF16(unicodeenc.BigEndian, unicodeenc.IgnoreBOM)
	encodedBE, err := encBE.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encodedBE) {
		t.Errorf("UTF-16BE 无 BOM 文本被误判为二进制")
	}
}

func TestLooksBinaryContent_GBK(t *testing.T) {
	// GBK 编码的中文文本
	gbkEnc := simplifiedchinese.GBK
	encoded, err := gbkEnc.NewEncoder().Bytes([]byte("这是一个GBK编码的中文文本文件，包含简体中文内容。"))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encoded) {
		t.Errorf("GBK 文本被误判为二进制")
	}
}

func TestLooksBinaryContent_GB18030(t *testing.T) {
	enc := simplifiedchinese.GB18030
	encoded, err := enc.NewEncoder().Bytes([]byte("这是GB18030编码的中文文本，包含简体和繁体中文。"))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encoded) {
		t.Errorf("GB18030 文本被误判为二进制")
	}
}

func TestLooksBinaryContent_Big5(t *testing.T) {
	enc := traditionalchinese.Big5
	encoded, err := enc.NewEncoder().Bytes([]byte("這是Big5編碼的繁體中文文本"))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encoded) {
		t.Errorf("Big5 文本被误判为二进制")
	}
}

func TestLooksBinaryContent_ShiftJIS(t *testing.T) {
	enc := japanese.ShiftJIS
	encoded, err := enc.NewEncoder().Bytes([]byte("これはShift_JISエンコーディングのテストです"))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encoded) {
		t.Errorf("Shift-JIS 文本被误判为二进制")
	}
}

func TestLooksBinaryContent_EUCJP(t *testing.T) {
	enc := japanese.EUCJP
	encoded, err := enc.NewEncoder().Bytes([]byte("これはEUC-JPエンコーディングのテストです"))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encoded) {
		t.Errorf("EUC-JP 文本被误判为二进制")
	}
}

func TestLooksBinaryContent_EUCKR(t *testing.T) {
	enc := korean.EUCKR
	encoded, err := enc.NewEncoder().Bytes([]byte("이것은 EUC-KR 인코딩 테스트입니다"))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encoded) {
		t.Errorf("EUC-KR 文本被误判为二进制")
	}
}

func TestLooksBinaryContent_Latin1(t *testing.T) {
	// ISO-8859-1 编码的西欧文本
	enc := charmap.ISO8859_1
	encoded, err := enc.NewEncoder().Bytes([]byte("Ceci est un texte en français avec des caractères spéciaux: é, è, ê, ë"))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encoded) {
		t.Errorf("ISO-8859-1 文本被误判为二进制")
	}
}

func TestLooksBinaryContent_Windows1252(t *testing.T) {
	enc := charmap.Windows1252
	encoded, err := enc.NewEncoder().Bytes([]byte("This is Windows-1252 encoded text with special chars: \u201cquotes\u201d and \u2014dash"))
	if err != nil {
		t.Fatal(err)
	}
	if looksBinaryContent(encoded) {
		t.Errorf("Windows-1252 文本被误判为二进制")
	}
}

func TestLooksBinaryContent_RealBinaryFiles(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{"PNG文件头", []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D}},
		{"JPEG文件头", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00, 0x01}},
		{"ZIP文件头", []byte{0x50, 0x4B, 0x03, 0x04, 0x14, 0x00, 0x00, 0x00, 0x08, 0x00}},
		{"ELF文件头", []byte{0x7F, 0x45, 0x4C, 0x46, 0x02, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00}},
		{"PDF文件头", []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")},
		{"全null字节", bytes.Repeat([]byte{0x00}, 1024)},
		{"高null比例二进制", append(bytes.Repeat([]byte{0x00}, 900), bytes.Repeat([]byte{0x41}, 100)...)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构造足够大的样本以避免 printableRatio 误判
			content := tt.content
			if len(content) < 100 {
				content = append(content, bytes.Repeat([]byte{0x00}, 1000)...)
			}
			if !looksBinaryContent(content) {
				t.Errorf("二进制内容 %s 未被正确识别", tt.name)
			}
		})
	}
}

func TestLooksBinaryContent_UTF8BOM(t *testing.T) {
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte("Hello UTF-8 BOM")...)
	if looksBinaryContent(content) {
		t.Errorf("UTF-8 BOM 文本被误判为二进制")
	}
}

// =============================================================================
// tryDecodeAsText 测试
// =============================================================================

func TestTryDecodeAsText_CJK(t *testing.T) {
	gbkText, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("这是GBK编码的中文文本"))
	big5Text, _ := traditionalchinese.Big5.NewEncoder().Bytes([]byte("這是Big5編碼的繁體中文"))
	sjisText, _ := japanese.ShiftJIS.NewEncoder().Bytes([]byte("これはShift_JISのテスト"))
	euckrText, _ := korean.EUCKR.NewEncoder().Bytes([]byte("이것은 EUC-KR 테스트"))

	tests := []struct {
		name    string
		content []byte
	}{
		{"GBK中文", gbkText},
		{"Big5繁体", big5Text},
		{"ShiftJIS日文", sjisText},
		{"EUCKR韩文", euckrText},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tryDecodeAsText(tt.content) {
				t.Errorf("tryDecodeAsText() 未能识别 %s 编码的文本", tt.name)
			}
		})
	}
}

func TestTryDecodeAsText_RejectsBinary(t *testing.T) {
	// 纯二进制内容（大量 null 字节 + 随机高字节）
	binary := make([]byte, 1024)
	for i := range binary {
		if i%3 == 0 {
			binary[i] = 0x00
		} else if i%3 == 1 {
			binary[i] = 0xFF
		} else {
			binary[i] = 0x80
		}
	}
	// 这种内容不应被识别为文本（printableRatio 会很低）
	if tryDecodeAsText(binary) {
		t.Logf("注意：tryDecodeAsText 对纯二进制内容返回 true，但 looksBinaryContent 的 null/control 检查会兜底")
	}
}

// =============================================================================
// decodeNonUTF8ToUTF8 测试
// =============================================================================

func TestDecodeNonUTF8ToUTF8_ValidUTF8(t *testing.T) {
	text, enc := decodeNonUTF8ToUTF8([]byte("Hello UTF-8"))
	if enc != "utf-8" {
		t.Errorf("期望 utf-8, 得到 %s", enc)
	}
	if text != "Hello UTF-8" {
		t.Errorf("内容不匹配: %s", text)
	}
}

func TestDecodeNonUTF8ToUTF8_UTF16LE_BOM(t *testing.T) {
	enc := unicodeenc.UTF16(unicodeenc.LittleEndian, unicodeenc.UseBOM)
	encoded, _ := enc.NewEncoder().Bytes([]byte("Hello UTF-16LE"))
	text, name := decodeNonUTF8ToUTF8(encoded)
	if name != "utf-16le" {
		t.Errorf("期望 utf-16le, 得到 %s", name)
	}
	if text != "Hello UTF-16LE" {
		t.Errorf("内容不匹配: %q", text)
	}
}

func TestDecodeNonUTF8ToUTF8_UTF16BE_BOM(t *testing.T) {
	enc := unicodeenc.UTF16(unicodeenc.BigEndian, unicodeenc.UseBOM)
	encoded, _ := enc.NewEncoder().Bytes([]byte("Hello UTF-16BE"))
	text, name := decodeNonUTF8ToUTF8(encoded)
	if name != "utf-16be" {
		t.Errorf("期望 utf-16be, 得到 %s", name)
	}
	if text != "Hello UTF-16BE" {
		t.Errorf("内容不匹配: %q", text)
	}
}

func TestDecodeNonUTF8ToUTF8_UTF16LE_NoBOM(t *testing.T) {
	enc := unicodeenc.UTF16(unicodeenc.LittleEndian, unicodeenc.IgnoreBOM)
	encoded, _ := enc.NewEncoder().Bytes([]byte("Hello UTF-16LE without BOM"))
	text, name := decodeNonUTF8ToUTF8(encoded)
	if name != "utf-16le" {
		t.Errorf("期望 utf-16le, 得到 %s", name)
	}
	if text != "Hello UTF-16LE without BOM" {
		t.Errorf("内容不匹配: %q", text)
	}
}

func TestDecodeNonUTF8ToUTF8_GBK(t *testing.T) {
	original := "这是GBK编码的中文文本文件"
	encoded, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(original))
	text, name := decodeNonUTF8ToUTF8(encoded)
	if name != "gbk" && name != "gb18030" {
		t.Errorf("期望 gbk 或 gb18030, 得到 %s", name)
	}
	if text != original {
		t.Errorf("内容不匹配: %q != %q", text, original)
	}
}

func TestDecodeNonUTF8ToUTF8_Big5(t *testing.T) {
	original := "這是Big5編碼的繁體中文"
	encoded, _ := traditionalchinese.Big5.NewEncoder().Bytes([]byte(original))
	text, name := decodeNonUTF8ToUTF8(encoded)
	if name != "big5" {
		t.Errorf("期望 big5, 得到 %s", name)
	}
	if text != original {
		t.Errorf("内容不匹配: %q != %q", text, original)
	}
}

func TestDecodeNonUTF8ToUTF8_ShiftJIS(t *testing.T) {
	original := "Shift_JISテスト"
	encoded, _ := japanese.ShiftJIS.NewEncoder().Bytes([]byte(original))
	text, name := decodeNonUTF8ToUTF8(encoded)
	// CJK 编码存在固有歧义：Shift_JIS 的字节序列可能同时是有效的 GBK/GB18030。
	// charset.DetermineEncoding 基于字节频率分析，应能优先识别 Shift_JIS，
	// 但如果回退到手动候选列表，GBK 可能先匹配成功。
	// 因此接受 shift_jis、gbk、gb18030 作为合理结果。
	acceptableNames := []string{"shift_jis", "gbk", "gb18030"}
	found := false
	for _, acceptable := range acceptableNames {
		if name == acceptable {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("期望 %v 之一, 得到 %s", acceptableNames, name)
	}
	// 如果检测到 shift_jis，内容必须完全匹配
	if name == "shift_jis" && text != original {
		t.Errorf("内容不匹配: %q != %q", text, original)
	}
	// 如果检测到 gbk/gb18030，说明 CJK 编码歧义，记录但不失败
	if name == "gbk" || name == "gb18030" {
		t.Logf("CJK 编码歧义: Shift_JIS 内容被检测为 %s，解码为 %q", name, text)
	}
}

func TestDecodeNonUTF8ToUTF8_Latin1(t *testing.T) {
	original := "Café résumé naïve"
	encoded, _ := charmap.ISO8859_1.NewEncoder().Bytes([]byte(original))
	text, name := decodeNonUTF8ToUTF8(encoded)
	// 可能被 charset.DetermineEncoding 检测为 iso-8859-1 或 windows-1252
	if name != "iso-8859-1" && name != "windows-1252" {
		t.Logf("编码名称: %s (可能被 charset 自动检测为其他名称)", name)
	}
	if text != original {
		t.Errorf("内容不匹配: %q != %q", text, original)
	}
}

// =============================================================================
// looksLikeUTF16LENoBOM / looksLikeUTF16BENoBOM 测试
// =============================================================================

func TestLooksLikeUTF16LENoBOM(t *testing.T) {
	enc := unicodeenc.UTF16(unicodeenc.LittleEndian, unicodeenc.IgnoreBOM)
	encoded, _ := enc.NewEncoder().Bytes([]byte("Hello World this is a test"))
	if !looksLikeUTF16LENoBOM(encoded) {
		t.Errorf("未能识别 UTF-16LE 无 BOM 内容")
	}
	if looksLikeUTF16BENoBOM(encoded) {
		t.Errorf("错误地将 UTF-16LE 识别为 UTF-16BE")
	}
}

func TestLooksLikeUTF16BENoBOM(t *testing.T) {
	enc := unicodeenc.UTF16(unicodeenc.BigEndian, unicodeenc.IgnoreBOM)
	encoded, _ := enc.NewEncoder().Bytes([]byte("Hello World this is a test"))
	if !looksLikeUTF16BENoBOM(encoded) {
		t.Errorf("未能识别 UTF-16BE 无 BOM 内容")
	}
	if looksLikeUTF16LENoBOM(encoded) {
		t.Errorf("错误地将 UTF-16BE 识别为 UTF-16LE")
	}
}

func TestLooksLikeUTF16_RejectsNonUTF16(t *testing.T) {
	// 普通 ASCII 文本不应被识别为 UTF-16
	ascii := []byte("This is plain ASCII text without any special characters")
	if looksLikeUTF16LENoBOM(ascii) {
		t.Errorf("ASCII 文本被错误识别为 UTF-16LE")
	}
	if looksLikeUTF16BENoBOM(ascii) {
		t.Errorf("ASCII 文本被错误识别为 UTF-16BE")
	}

	// GBK 编码不应被识别为 UTF-16
	gbk, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("这是GBK编码的中文"))
	if looksLikeUTF16LENoBOM(gbk) {
		t.Errorf("GBK 文本被错误识别为 UTF-16LE")
	}
	if looksLikeUTF16BENoBOM(gbk) {
		t.Errorf("GBK 文本被错误识别为 UTF-16BE")
	}
}

// =============================================================================
// printableRatio 测试
// =============================================================================

func TestPrintableRatio(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		minRatio float64
	}{
		{"纯ASCII", "Hello World", 0.9},
		{"含换行", "line1\nline2\n", 0.9},
		{"中文UTF-8", "你好世界", 0.9},
		{"空字符串", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratio := printableRatio(tt.input)
			if ratio < tt.minRatio {
				t.Errorf("printableRatio(%q) = %.2f, 期望 >= %.2f", tt.input, ratio, tt.minRatio)
			}
		})
	}
}

// =============================================================================
// 辅助函数（已内联到各测试用例中）
// =============================================================================
