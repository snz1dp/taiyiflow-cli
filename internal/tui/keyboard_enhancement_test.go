package tui

import (
	"bytes"
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