//go:build linux

package computerbridge

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	linuxInputSessionX11     = "x11"
	linuxInputSessionWayland = "wayland"
)

type linuxKeyboardBackend struct {
	Session string
	Tool    string
}

type linuxClipboardBackend struct {
	Name      string
	ReadArgs  []string
	WriteArgs []string
}

func linuxKeyPressAvailable() (bool, string) {
	if _, err := linuxKeyboardBackendForCurrentHost(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func linuxTypeTextAvailable() (bool, string) {
	backend, err := linuxKeyboardBackendForCurrentHost()
	if err != nil {
		return false, err.Error()
	}
	if backend.Session == linuxInputSessionWayland {
		return true, ""
	}
	if _, err := linuxClipboardBackendForCurrentHost(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func linuxTypeText(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	text := stringValue(arguments["text"])
	if text == "" {
		return nil, fmt.Errorf("type_text 缺少 text")
	}
	pressEnter := boolValue(arguments["press_enter"])
	keyboardBackend, err := linuxKeyboardBackendForCurrentHost()
	if err != nil {
		return nil, err
	}
	if keyboardBackend.Session == linuxInputSessionWayland {
		if err := linuxWaylandTypeText(ctx, text); err != nil {
			return nil, err
		}
		if pressEnter {
			if err := linuxWaylandKeyPress(ctx, "Return", nil); err != nil {
				return nil, err
			}
		}
		return map[string]any{
			"platform":                "linux",
			"text_length":             utf8.RuneCountInString(text),
			"line_count":              len(splitTypedText(text)),
			"press_enter":             pressEnter,
			"input_method":            "virtual_keyboard",
			"clipboard_restore_scope": "not_used",
			"session_type":            keyboardBackend.Session,
			"keyboard_tool":           keyboardBackend.Tool,
		}, nil
	}

	clipboardBackend, err := linuxClipboardBackendForCurrentHost()
	if err != nil {
		return nil, err
	}
	previous, err := linuxReadClipboardText(ctx, clipboardBackend)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := linuxWriteClipboardText(ctx, clipboardBackend, text); err != nil {
		return nil, err
	}
	pasteErr := linuxTriggerX11Key(ctx, "ctrl+v")
	if pasteErr == nil {
		time.Sleep(50 * time.Millisecond)
	}
	restoreErr := linuxWriteClipboardText(ctx, clipboardBackend, previous)
	if pasteErr != nil {
		return nil, pasteErr
	}
	if restoreErr != nil {
		return nil, restoreErr
	}
	if pressEnter {
		if err := linuxTriggerX11Key(ctx, "Return"); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"platform":                "linux",
		"text_length":             utf8.RuneCountInString(text),
		"line_count":              len(splitTypedText(text)),
		"press_enter":             pressEnter,
		"input_method":            "clipboard_paste",
		"clipboard_backend":       clipboardBackend.Name,
		"clipboard_restore_scope": "text_only",
		"session_type":            keyboardBackend.Session,
		"keyboard_tool":           keyboardBackend.Tool,
	}, nil
}

func linuxKeyboardBackendForCurrentHost() (linuxKeyboardBackend, error) {
	if linuxWaylandSession() {
		if _, err := exec.LookPath("wtype"); err != nil {
			return linuxKeyboardBackend{}, fmt.Errorf("当前 Linux Wayland 键盘输入依赖 wtype")
		}
		return linuxKeyboardBackend{Session: linuxInputSessionWayland, Tool: "wtype"}, nil
	}
	if strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
		return linuxKeyboardBackend{}, fmt.Errorf("未检测到 DISPLAY，当前 Linux 键盘输入暂仅支持 X11 或 Wayland")
	}
	if _, err := exec.LookPath("xdotool"); err != nil {
		return linuxKeyboardBackend{}, fmt.Errorf("未找到 xdotool")
	}
	return linuxKeyboardBackend{Session: linuxInputSessionX11, Tool: "xdotool"}, nil
}

func linuxWaylandSession() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), "wayland") {
		return true
	}
	return strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != ""
}

func linuxClipboardBackendForCurrentHost() (linuxClipboardBackend, error) {
	if _, err := exec.LookPath("xclip"); err == nil {
		return linuxClipboardBackend{
			Name:      "xclip",
			ReadArgs:  []string{"xclip", "-selection", "clipboard", "-out"},
			WriteArgs: []string{"xclip", "-selection", "clipboard", "-in"},
		}, nil
	}
	if _, err := exec.LookPath("xsel"); err == nil {
		return linuxClipboardBackend{
			Name:      "xsel",
			ReadArgs:  []string{"xsel", "--clipboard", "--output"},
			WriteArgs: []string{"xsel", "--clipboard", "--input"},
		}, nil
	}
	return linuxClipboardBackend{}, fmt.Errorf("未找到 xclip 或 xsel")
}

func linuxReadClipboardText(ctx context.Context, backend linuxClipboardBackend) (string, error) {
	return linuxRunCommand(ctx, backend.ReadArgs, "读取 Linux 剪贴板失败", "")
}

func linuxWriteClipboardText(ctx context.Context, backend linuxClipboardBackend, text string) error {
	_, err := linuxRunCommand(ctx, backend.WriteArgs, "写入 Linux 剪贴板失败", text)
	return err
}

func linuxTriggerX11Key(ctx context.Context, key string) error {
	_, err := linuxRunCommand(ctx, []string{"xdotool", "key", "--clearmodifiers", key}, "发送 Linux 键盘事件失败", "")
	return err
}

func linuxKeyPress(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	key := strings.TrimSpace(firstString(arguments, "key", "keys"))
	if key == "" {
		return nil, fmt.Errorf("key_press 缺少 key")
	}
	repeatCount := intValue(arguments["repeat"], 1)
	if repeatCount < 1 {
		repeatCount = 1
	}
	modifiers := stringSlice(arguments["modifiers"])
	backend, err := linuxKeyboardBackendForCurrentHost()
	if err != nil {
		return nil, err
	}
	for current := 0; current < repeatCount; current++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if backend.Session == linuxInputSessionWayland {
			if err := linuxWaylandKeyPress(ctx, key, modifiers); err != nil {
				return nil, err
			}
			continue
		}
		sequence, err := linuxX11KeySequence(key, modifiers)
		if err != nil {
			return nil, err
		}
		if err := linuxTriggerX11Key(ctx, sequence); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"platform":      "linux",
		"key":           key,
		"repeat":        repeatCount,
		"modifiers":     modifiers,
		"session_type":  backend.Session,
		"keyboard_tool": backend.Tool,
	}, nil
}

func linuxWaylandTypeText(ctx context.Context, text string) error {
	_, err := linuxRunCommand(ctx, []string{"wtype", "-"}, "发送 Linux Wayland 文本输入失败", text)
	return err
}

func linuxWaylandKeyPress(ctx context.Context, key string, modifiers []string) error {
	args := []string{"wtype"}
	modifierTokens, err := linuxWaylandModifierTokens(modifiers)
	if err != nil {
		return err
	}
	for _, item := range modifierTokens {
		args = append(args, "-M", item)
	}
	keyToken, err := linuxWaylandKeyToken(key)
	if err != nil {
		return err
	}
	args = append(args, "-k", keyToken)
	for index := len(modifierTokens) - 1; index >= 0; index-- {
		args = append(args, "-m", modifierTokens[index])
	}
	_, err = linuxRunCommand(ctx, args, "发送 Wayland 键盘事件失败", "")
	return err
}

func linuxX11KeySequence(key string, modifiers []string) (string, error) {
	modifierTokens, err := linuxModifierTokens(modifiers)
	if err != nil {
		return "", err
	}
	keyToken, err := linuxX11KeyToken(key)
	if err != nil {
		return "", err
	}
	if len(modifierTokens) == 0 {
		return keyToken, nil
	}
	return strings.Join(append(modifierTokens, keyToken), "+"), nil
}

func linuxModifierTokens(modifiers []string) ([]string, error) {
	if len(modifiers) == 0 {
		return nil, nil
	}
	mapped := make([]string, 0, len(modifiers))
	for _, item := range modifiers {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "control", "ctrl":
			mapped = append(mapped, "ctrl")
		case "shift":
			mapped = append(mapped, "shift")
		case "alt", "option":
			mapped = append(mapped, "alt")
		case "command", "cmd", "meta":
			mapped = append(mapped, "super")
		default:
			return nil, fmt.Errorf("不支持的修饰键: %s", item)
		}
	}
	return mapped, nil
}

func linuxWaylandModifierTokens(modifiers []string) ([]string, error) {
	if len(modifiers) == 0 {
		return nil, nil
	}
	mapped := make([]string, 0, len(modifiers))
	for _, item := range modifiers {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "control", "ctrl":
			mapped = append(mapped, "ctrl")
		case "shift":
			mapped = append(mapped, "shift")
		case "alt", "option":
			mapped = append(mapped, "alt")
		case "command", "cmd", "meta":
			mapped = append(mapped, "logo")
		default:
			return nil, fmt.Errorf("不支持的修饰键: %s", item)
		}
	}
	return mapped, nil
}

func linuxX11KeyToken(key string) (string, error) {
	return linuxCommonKeyToken(key)
}

func linuxWaylandKeyToken(key string) (string, error) {
	return linuxCommonKeyToken(key)
}

func linuxCommonKeyToken(key string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "enter", "return":
		return "Return", nil
	case "tab":
		return "Tab", nil
	case "escape", "esc":
		return "Escape", nil
	case "space":
		return "space", nil
	case "backspace":
		return "BackSpace", nil
	case "delete":
		return "Delete", nil
	case "left":
		return "Left", nil
	case "right":
		return "Right", nil
	case "up":
		return "Up", nil
	case "down":
		return "Down", nil
	case "home":
		return "Home", nil
	case "end":
		return "End", nil
	case "pageup":
		return "Page_Up", nil
	case "pagedown":
		return "Page_Down", nil
	}
	if strings.HasPrefix(normalized, "f") {
		if len(normalized) >= 2 && len(normalized) <= 3 {
			return strings.ToUpper(normalized), nil
		}
	}
	if utf8.RuneCountInString(key) != 1 {
		return "", fmt.Errorf("key_press 暂仅支持单字符或常见特殊键")
	}
	return key, nil
}

func linuxRunCommand(ctx context.Context, args []string, failurePrefix string, stdin string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("%s: 缺少命令", failurePrefix)
	}
	commandCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, args[0], args[1:]...)
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", failurePrefix, summarizeCommandError(err, output))
	}
	return string(output), nil
}
