package tui

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"testing"
)

type stubTermFile struct {
	reader bytes.Buffer
	writer bytes.Buffer
}

func (f *stubTermFile) Read(buffer []byte) (int, error) {
	return f.reader.Read(buffer)
}

func (f *stubTermFile) Write(buffer []byte) (int, error) {
	return f.writer.Write(buffer)
}

func (f *stubTermFile) Close() error {
	return nil
}

func (f *stubTermFile) Fd() uintptr {
	return 0
}

type chunkedTermFile struct {
	chunks [][]byte
	index  int
}

func (f *chunkedTermFile) Read(buffer []byte) (int, error) {
	if f.index >= len(f.chunks) {
		return 0, io.EOF
	}
	count := copy(buffer, f.chunks[f.index])
	f.index++
	return count, nil
}

func (f *chunkedTermFile) Write(buffer []byte) (int, error) {
	return len(buffer), nil
}

func (f *chunkedTermFile) Close() error {
	return nil
}

func (f *chunkedTermFile) Fd() uintptr {
	return 0
}

func TestTranslateVSCodeKittyKeyboardInputMapsModifiedEnterAndShiftTab(t *testing.T) {
	translated, remainder := translateVSCodeKittyKeyboardInput([]byte("a\x1b[13;2ub\x1b[13;3u\x1b[9;2u"))
	if len(remainder) != 0 {
		t.Fatalf("expected no remainder, got %q", string(remainder))
	}
	if got, want := string(translated), "a\x1b\rb\x1b\r\x1b[Z"; got != want {
		t.Fatalf("expected translated bytes %q, got %q", want, got)
	}
}

func TestTranslateVSCodeKittyKeyboardInputKeepsPartialSequence(t *testing.T) {
	translated, remainder := translateVSCodeKittyKeyboardInput([]byte("前缀\x1b[13;"))
	if got, want := string(translated), "前缀"; got != want {
		t.Fatalf("expected translated prefix %q, got %q", want, got)
	}
	if got, want := string(remainder), "\x1b[13;"; got != want {
		t.Fatalf("expected partial sequence remainder %q, got %q", want, got)
	}
}

func TestTranslateVSCodeKittyKeyboardInputKeepsTrailingEscapeByte(t *testing.T) {
	translated, remainder := translateVSCodeKittyKeyboardInput([]byte("前缀\x1b"))
	if got, want := string(translated), "前缀"; got != want {
		t.Fatalf("expected translated prefix %q, got %q", want, got)
	}
	if got, want := string(remainder), "\x1b"; got != want {
		t.Fatalf("expected lone escape byte remainder %q, got %q", want, got)
	}
}

func TestTranslateVSCodeKittyKeyboardInputKeepsPartialMouseSGRSequence(t *testing.T) {
	translated, remainder := translateVSCodeKittyKeyboardInput([]byte("前缀\x1b[<64;12;7"))
	if got, want := string(translated), "前缀"; got != want {
		t.Fatalf("expected translated prefix %q, got %q", want, got)
	}
	if got, want := string(remainder), "\x1b[<64;12;7"; got != want {
		t.Fatalf("expected partial mouse remainder %q, got %q", want, got)
	}
}

func TestVSCodeKittyKeyboardReaderBuffersPartialMouseSequence(t *testing.T) {
	input := &chunkedTermFile{chunks: [][]byte{
		[]byte("前缀\x1b[<64;12;7"),
		[]byte("M后缀"),
	}}
	reader := newVSCodeKittyKeyboardReader(input)
	buffer := make([]byte, 64)

	count, err := reader.Read(buffer)
	if err != nil {
		t.Fatalf("expected first read to succeed, got %v", err)
	}
	if got, want := string(buffer[:count]), "前缀"; got != want {
		t.Fatalf("expected first read to exclude partial mouse sequence, got %q want %q", got, want)
	}

	count, err = reader.Read(buffer)
	if err != nil {
		t.Fatalf("expected second read to succeed, got %v", err)
	}
	if got, want := string(buffer[:count]), "\x1b[<64;12;7M后缀"; got != want {
		t.Fatalf("expected second read to flush completed mouse sequence, got %q want %q", got, want)
	}
}

func TestVSCodeKittyKeyboardReaderBuffersLoneEscapeBeforeMouseSequence(t *testing.T) {
	input := &chunkedTermFile{chunks: [][]byte{
		[]byte("前缀\x1b"),
		[]byte("[<65;30;38M后缀"),
	}}
	reader := newVSCodeKittyKeyboardReader(input)
	buffer := make([]byte, 64)

	count, err := reader.Read(buffer)
	if err != nil {
		t.Fatalf("expected first read to succeed, got %v", err)
	}
	if got, want := string(buffer[:count]), "前缀"; got != want {
		t.Fatalf("expected first read to exclude trailing escape byte, got %q want %q", got, want)
	}

	count, err = reader.Read(buffer)
	if err != nil {
		t.Fatalf("expected second read to succeed, got %v", err)
	}
	if got, want := string(buffer[:count]), "\x1b[<65;30;38M后缀"; got != want {
		t.Fatalf("expected second read to preserve full mouse sequence after lone escape, got %q want %q", got, want)
	}
}

func TestTerminalInputReaderBuffersLoneEscapeOutsideVSCode(t *testing.T) {
	input := &chunkedTermFile{chunks: [][]byte{
		[]byte("前缀\x1b"),
		[]byte("[<65;49;20M后缀"),
	}}
	reader := newTerminalInputReader(input, false)
	buffer := make([]byte, 64)

	count, err := reader.Read(buffer)
	if err != nil {
		t.Fatalf("expected first read to succeed, got %v", err)
	}
	if got, want := string(buffer[:count]), "前缀"; got != want {
		t.Fatalf("expected first read to exclude trailing escape byte, got %q want %q", got, want)
	}

	count, err = reader.Read(buffer)
	if err != nil {
		t.Fatalf("expected second read to succeed, got %v", err)
	}
	if got, want := string(buffer[:count]), "\x1b[<65;49;20M后缀"; got != want {
		t.Fatalf("expected second read to preserve full mouse sequence outside VS Code, got %q want %q", got, want)
	}
}

func TestProgramKeyboardOptionsEnableVSCodeProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows uses a different terminal input path")
	}

	input := &stubTermFile{}
	var output bytes.Buffer

	options, cleanup := programKeyboardOptions(input, &output, []string{"TERM_PROGRAM=vscode"})
	if len(options) != 1 {
		t.Fatalf("expected one program option, got %d", len(options))
	}
	if cleanup == nil {
		t.Fatal("expected cleanup callback")
	}
	if got := output.String(); got != vscodeKittyKeyboardEnable {
		t.Fatalf("expected enable sequence %q, got %q", vscodeKittyKeyboardEnable, got)
	}

	cleanup()
	if got := output.String(); got != vscodeKittyKeyboardEnable+vscodeKittyKeyboardDisable {
		t.Fatalf("expected enable and disable sequences, got %q", got)
	}
}

func TestProgramKeyboardOptionsWrapsInputOutsideVSCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows uses a different terminal input path")
	}

	input := &stubTermFile{}
	var output bytes.Buffer

	options, cleanup := programKeyboardOptions(input, &output, []string{"TERM_PROGRAM=Apple_Terminal"})
	if len(options) != 1 {
		t.Fatalf("expected one input wrapper option outside VS Code, got %d", len(options))
	}
	if cleanup == nil {
		t.Fatal("expected cleanup callback")
	}
	if got := output.String(); got != "" {
		t.Fatalf("expected no VS Code keyboard enable sequence outside VS Code, got %q", got)
	}

	cleanup()
	if got := output.String(); got != "" {
		t.Fatalf("expected no VS Code keyboard cleanup sequence outside VS Code, got %q", got)
	}
}

func TestMultilineInputPlaceholderUsesShiftEnterInVSCode(t *testing.T) {
	placeholder := multilineInputPlaceholder([]string{"TERM_PROGRAM=vscode"})
	if runtime.GOOS != "windows" && !strings.Contains(placeholder, "Shift+Enter") {
		t.Fatalf("expected VS Code placeholder to mention Shift+Enter, got %q", placeholder)
	}

	placeholder = multilineInputPlaceholder(nil)
	if strings.Contains(placeholder, "Shift+Enter") {
		t.Fatalf("expected default placeholder not to mention Shift+Enter, got %q", placeholder)
	}
}
