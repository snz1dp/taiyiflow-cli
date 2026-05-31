//go:build windows

package computerbridge

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

func windowsTypeTextAvailable() (bool, string) {
	if _, err := windowsPowerShellBinary(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func windowsKeyPressAvailable() (bool, string) {
	if _, err := windowsPowerShellBinary(); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func windowsTypeText(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	text := stringValue(arguments["text"])
	if text == "" {
		return nil, fmt.Errorf("type_text 缺少 text")
	}
	pressEnter := boolValue(arguments["press_enter"])
	previous, err := windowsCaptureClipboard(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := windowsWriteClipboardText(ctx, text); err != nil {
		return nil, err
	}
	pasteErr := windowsSendKeys(ctx, "^v")
	if pasteErr == nil {
		time.Sleep(50 * time.Millisecond)
	}
	restoreErr := windowsRestoreClipboard(ctx, previous)
	if pasteErr != nil {
		return nil, pasteErr
	}
	if restoreErr != nil {
		return nil, restoreErr
	}
	if pressEnter {
		if err := windowsSendKeys(ctx, "{ENTER}"); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"platform":                 "windows",
		"text_length":              utf8.RuneCountInString(text),
		"line_count":               len(splitTypedText(text)),
		"press_enter":              pressEnter,
		"input_method":             "clipboard_paste",
		"clipboard_backend":        "powershell_dataobject",
		"clipboard_restore_scope":  "common_multi_format",
		"clipboard_restore_policy": "fail_on_unsupported_format",
	}, nil
}

func windowsPowerShellBinary() (string, error) {
	for _, candidate := range []string{"powershell", "powershell.exe"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("未找到 powershell")
}

func windowsWriteClipboardText(ctx context.Context, text string) error {
	_, err := windowsRunPowerShell(ctx, `[Console]::InputEncoding = [System.Text.UTF8Encoding]::UTF8; Set-Clipboard -Value ([Console]::In.ReadToEnd())`, "写入 Windows 剪贴板失败", text)
	return err
}

func windowsSendKeys(ctx context.Context, keys string) error {
	command := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.SendKeys]::SendWait('%s')`, windowsPowerShellQuotedLiteral(keys))
	_, err := windowsRunPowerShell(ctx, command, "发送 Windows 键盘事件失败", "")
	return err
}

func windowsKeyPress(ctx context.Context, arguments map[string]any) (map[string]any, error) {
	key := strings.TrimSpace(firstString(arguments, "key", "keys"))
	if key == "" {
		return nil, fmt.Errorf("key_press 缺少 key")
	}
	repeatCount := intValue(arguments["repeat"], 1)
	if repeatCount < 1 {
		repeatCount = 1
	}
	sequence, err := windowsKeySequence(key, stringSlice(arguments["modifiers"]))
	if err != nil {
		return nil, err
	}
	for current := 0; current < repeatCount; current++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := windowsSendKeys(ctx, sequence); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"platform":  "windows",
		"key":       key,
		"repeat":    repeatCount,
		"modifiers": stringSlice(arguments["modifiers"]),
	}, nil
}

func windowsKeySequence(key string, modifiers []string) (string, error) {
	modifierPrefix, err := windowsModifierPrefix(modifiers)
	if err != nil {
		return "", err
	}
	keyToken, err := windowsKeyToken(key)
	if err != nil {
		return "", err
	}
	return modifierPrefix + keyToken, nil
}

func windowsModifierPrefix(modifiers []string) (string, error) {
	if len(modifiers) == 0 {
		return "", nil
	}
	var builder strings.Builder
	for _, item := range modifiers {
		switch strings.ToLower(strings.TrimSpace(item)) {
		case "control", "ctrl":
			builder.WriteString("^")
		case "shift":
			builder.WriteString("+")
		case "alt", "option":
			builder.WriteString("%")
		case "command", "cmd", "meta":
			return "", fmt.Errorf("Windows key_press 暂不支持 command/meta 修饰键")
		default:
			return "", fmt.Errorf("不支持的修饰键: %s", item)
		}
	}
	return builder.String(), nil
}

func windowsKeyToken(key string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "enter", "return":
		return "{ENTER}", nil
	case "tab":
		return "{TAB}", nil
	case "escape", "esc":
		return "{ESC}", nil
	case "space":
		return " ", nil
	case "backspace":
		return "{BACKSPACE}", nil
	case "delete":
		return "{DELETE}", nil
	case "left":
		return "{LEFT}", nil
	case "right":
		return "{RIGHT}", nil
	case "up":
		return "{UP}", nil
	case "down":
		return "{DOWN}", nil
	case "home":
		return "{HOME}", nil
	case "end":
		return "{END}", nil
	case "pageup":
		return "{PGUP}", nil
	case "pagedown":
		return "{PGDN}", nil
	}
	if strings.HasPrefix(normalized, "f") {
		if len(normalized) >= 2 && len(normalized) <= 3 {
			return "{" + strings.ToUpper(normalized) + "}", nil
		}
	}
	if utf8.RuneCountInString(key) != 1 {
		return "", fmt.Errorf("key_press 暂仅支持单字符或常见特殊键")
	}
	switch key {
	case "+", "^", "%", "~", "(", ")", "{", "}", "[", "]":
		return "{" + key + "}", nil
	default:
		return key, nil
	}
}

func windowsRunPowerShell(ctx context.Context, script string, failurePrefix string, stdin string) (string, error) {
	binaryPath, err := windowsPowerShellBinary()
	if err != nil {
		return "", err
	}
	commandCtx, cancel := context.WithTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, binaryPath, "-NoProfile", "-NonInteractive", "-Sta", "-Command", script)
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", failurePrefix, summarizeCommandError(err, output))
	}
	return string(output), nil
}

func windowsPowerShellQuotedLiteral(value string) string {
	return strings.ReplaceAll(value, `'`, `''`)
}
