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
	vscodeKittyKeyboardEnable  = "\x1b[=1;2u"
	vscodeKittyKeyboardDisable = "\x1b[=1;3u"
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

type vscodeKittyKeyboardReader struct {
	input     term.File
	pending   bytes.Buffer
	remainder []byte
}

func programKeyboardOptions(input term.File, output io.Writer, environ []string) ([]tea.ProgramOption, func()) {
	if input == nil || output == nil || !shouldEnableVSCodeKeyboardProtocol(environ) {
		return nil, nil
	}
	_, _ = io.WriteString(output, vscodeKittyKeyboardEnable)
	return []tea.ProgramOption{tea.WithInput(newVSCodeKittyKeyboardReader(input))}, func() {
		_, _ = io.WriteString(output, vscodeKittyKeyboardDisable)
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
		return "回车发送，Shift+Enter、Alt+Enter 或 Ctrl+J 换行"
	}
	return "回车发送，Alt+Enter 或 Ctrl+J 换行"
}

func newVSCodeKittyKeyboardReader(input term.File) *vscodeKittyKeyboardReader {
	return &vscodeKittyKeyboardReader{input: input}
}

func (r *vscodeKittyKeyboardReader) Read(buffer []byte) (int, error) {
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
			translated, remainder := translateVSCodeKittyKeyboardInput(payload)
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

func (r *vscodeKittyKeyboardReader) Write(buffer []byte) (int, error) {
	return r.input.Write(buffer)
}

func (r *vscodeKittyKeyboardReader) Close() error {
	return nil
}

func (r *vscodeKittyKeyboardReader) Fd() uintptr {
	return r.input.Fd()
}

func translateVSCodeKittyKeyboardInput(input []byte) ([]byte, []byte) {
	if len(input) == 0 {
		return nil, nil
	}

	translated := make([]byte, 0, len(input))
	for index := 0; index < len(input); {
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

		translated = append(translated, input[index])
		index++
	}

	return translated, nil
}

func isVSCodeKittySequencePrefix(input []byte) bool {
	for _, replacement := range vscodeKittySequenceReplacements {
		if len(input) < len(replacement.from) && bytes.HasPrefix(replacement.from, input) {
			return true
		}
	}
	return false
}