package tui

import (
	"bytes"
	"io"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

const (
	// 使用 kitty 键盘协议的 push/pop 机制（CSI > 和 CSI <），
	// 而非直接设置机制（CSI =），因为部分终端（如 VSCode 的 xterm.js）
	// 仅支持 push/pop 形式。flags 1=DISAMBIGUATE_ESCAPE_CODES，mode 2=按位设置。
	vscodeKittyKeyboardEnable  = "\x1b[>1;2u"
	vscodeKittyKeyboardDisable = "\x1b[<u"
)

var vscodeKittySequenceReplacements = []struct {
	from []byte
	to   []byte
}{
	{from: []byte("\x1b[13;2u"), to: []byte("\x1b\r")},
	{from: []byte("\x1b[13;3u"), to: []byte("\x1b\r")},
	{from: []byte("\x1b[13;4u"), to: []byte("\x1b\r")},
	{from: []byte("\x1b[9;2u"), to: []byte("\x1b[Z")},
	{from: []byte("\x1b[127;3u"), to: []byte("\x1b\x7f")},
	{from: []byte("\x1b[27;3u"), to: []byte("\x1b\x1b")},
}

type terminalInputReader struct {
	input                     term.File
	pending                   bytes.Buffer
	remainder                 []byte
	translateVSCodeKittyInput bool
}

func programKeyboardOptions(input term.File, output io.Writer, environ []string) ([]tea.ProgramOption, func()) {
	if input == nil || runtime.GOOS == "windows" {
		return nil, nil
	}
	enableVSCodeKeyboard := output != nil && shouldEnableVSCodeKeyboardProtocol(environ)
	if enableVSCodeKeyboard {
		_, _ = io.WriteString(output, vscodeKittyKeyboardEnable)
	}
	return []tea.ProgramOption{tea.WithInput(newTerminalInputReader(input, enableVSCodeKeyboard))}, func() {
		if enableVSCodeKeyboard {
			_, _ = io.WriteString(output, vscodeKittyKeyboardDisable)
		}
	}
}

func shouldEnableVSCodeKeyboardProtocol(environ []string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return lookupEnv(environ, "TERM_PROGRAM") == "vscode"
}

func lookupEnv(environ []string, key string) string {
	prefix := key + "="
	for _, entry := range environ {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}

func multilineInputPlaceholder(environ []string) string {
	if shouldEnableVSCodeKeyboardProtocol(environ) {
		return "回车发送，Shift+Enter、Alt+Enter 或 Ctrl+J 换行或发出执行引导"
	}
	return "回车发送，Alt+Enter 或 Ctrl+J 换行或发出执行引导"
}

func newTerminalInputReader(input term.File, translateVSCodeKittyInput bool) *terminalInputReader {
	return &terminalInputReader{input: input, translateVSCodeKittyInput: translateVSCodeKittyInput}
}

func newVSCodeKittyKeyboardReader(input term.File) *terminalInputReader {
	return newTerminalInputReader(input, true)
}

func (r *terminalInputReader) Read(buffer []byte) (int, error) {
	for {
		if r.pending.Len() > 0 {
			return r.pending.Read(buffer)
		}

		readBufferSize := len(buffer)
		if readBufferSize < 32 {
			readBufferSize = 32
		}
		readBuffer := make([]byte, readBufferSize)
		readCount, readErr := r.input.Read(readBuffer)
		if readCount > 0 {
			payload := append(append([]byte(nil), r.remainder...), readBuffer[:readCount]...)
			translated, remainder := translateTerminalInput(payload, r.translateVSCodeKittyInput)
			r.remainder = append(r.remainder[:0], remainder...)
			if len(translated) > 0 {
				_, _ = r.pending.Write(translated)
				return r.pending.Read(buffer)
			}
		}

		if readErr != nil {
			if len(r.remainder) > 0 {
				_, _ = r.pending.Write(r.remainder)
				r.remainder = nil
				return r.pending.Read(buffer)
			}
			return 0, readErr
		}
	}
}

func (r *terminalInputReader) Write(buffer []byte) (int, error) {
	return r.input.Write(buffer)
}

func (r *terminalInputReader) Close() error {
	return nil
}

func (r *terminalInputReader) Fd() uintptr {
	return r.input.Fd()
}

func translateVSCodeKittyKeyboardInput(input []byte) ([]byte, []byte) {
	return translateTerminalInput(input, true)
}

func translateTerminalInput(input []byte, translateVSCodeKittyInput bool) ([]byte, []byte) {
	if len(input) == 0 {
		return nil, nil
	}

	translated := make([]byte, 0, len(input))
	for index := 0; index < len(input); {
		if translateVSCodeKittyInput {
			matched := false
			for _, replacement := range vscodeKittySequenceReplacements {
				if len(input[index:]) >= len(replacement.from) && bytes.Equal(input[index:index+len(replacement.from)], replacement.from) {
					translated = append(translated, replacement.to...)
					index += len(replacement.from)
					matched = true
					break
				}
			}
			if matched {
				continue
			}

			if isVSCodeKittySequencePrefix(input[index:]) {
				return translated, append([]byte(nil), input[index:]...)
			}
		}

		translated = append(translated, input[index])
		index++
	}

	return splitTrailingANSIEscapeRemainder(translated)
}

func isVSCodeKittySequencePrefix(input []byte) bool {
	for _, replacement := range vscodeKittySequenceReplacements {
		if len(input) < len(replacement.from) && bytes.HasPrefix(replacement.from, input) {
			return true
		}
	}
	return false
}

func splitTrailingANSIEscapeRemainder(input []byte) ([]byte, []byte) {
	if len(input) == 0 {
		return input, nil
	}
	escapeIndex := bytes.LastIndexByte(input, '\x1b')
	if escapeIndex < 0 {
		return input, nil
	}
	sequence := input[escapeIndex:]
	if !hasIncompleteANSIEscape(sequence) {
		return input, nil
	}
	return input[:escapeIndex], append([]byte(nil), sequence...)
}

func hasIncompleteANSIEscape(sequence []byte) bool {
	if len(sequence) == 1 && sequence[0] == '\x1b' {
		return true
	}
	if len(sequence) < 2 || sequence[0] != '\x1b' {
		return false
	}
	switch sequence[1] {
	case '[':
		if len(sequence) >= 3 && sequence[2] == 'M' {
			return len(sequence) < 6
		}
		return !hasANSIFinalByte(sequence[2:])
	case 'O':
		return !hasANSIFinalByte(sequence[2:])
	default:
		return false
	}
}

func hasANSIFinalByte(sequence []byte) bool {
	if len(sequence) == 0 {
		return false
	}
	lastByte := sequence[len(sequence)-1]
	return lastByte >= 0x40 && lastByte <= 0x7e
}
