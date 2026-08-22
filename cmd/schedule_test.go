package cmd

import (
	"strings"
	"testing"
)

// --- truncateRunes 测试 ---

func TestTruncateRunesShortString(t *testing.T) {
	result := truncateRunes("hello", 10)
	if result != "hello" {
		t.Fatalf("expected unchanged string, got %q", result)
	}
}

func TestTruncateRunesExactLength(t *testing.T) {
	result := truncateRunes("hello", 5)
	if result != "hello" {
		t.Fatalf("expected unchanged string at exact length, got %q", result)
	}
}

func TestTruncateRunesLongASCII(t *testing.T) {
	result := truncateRunes("hello world", 5)
	if result != "hello…" {
		t.Fatalf("expected 'hello…', got %q", result)
	}
}

func TestTruncateRunesChinese(t *testing.T) {
	// 中文字符每个占 3 字节，按字节截断会产生乱码
	input := "这是一个很长的中文提示词内容"
	result := truncateRunes(input, 5)
	if result != "这是一个很…" {
		t.Fatalf("expected '这是一个很…', got %q", result)
	}
	// 验证结果是有效 UTF-8
	if !isValidUTF8(result) {
		t.Fatalf("expected valid UTF-8, got %q", result)
	}
}

func TestTruncateRunesMixedContent(t *testing.T) {
	input := "abc中文def"
	result := truncateRunes(input, 5)
	if result != "abc中文…" {
		t.Fatalf("expected 'abc中文…', got %q", result)
	}
}

func TestTruncateRunesEmpty(t *testing.T) {
	result := truncateRunes("", 5)
	if result != "" {
		t.Fatalf("expected empty string, got %q", result)
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '\uFFFD' {
			return false
		}
	}
	return true
}

// --- isScheduleSlashCommand 测试 ---

func TestIsScheduleSlashCommand(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"/schedule", true},
		{"/schedule list", true},
		{"/SCHEDULE", true},
		{"/schedule runs abc", true},
		{"/schedules", false},
		{"schedule", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isScheduleSlashCommand(tt.input)
		if got != tt.expected {
			t.Errorf("isScheduleSlashCommand(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

// --- runScheduleEdit 互斥校验测试 ---

func TestRunScheduleEditRejectsCronAndIntervalTogether(t *testing.T) {
	app := &App{stderr: &strings.Builder{}, stdout: &strings.Builder{}}
	err := app.runScheduleEdit(nil, []string{"--cron", "0 9 * * *", "--interval", "3600", "sched-1"})
	if err == nil {
		t.Fatal("expected error for --cron and --interval together")
	}
	if !strings.Contains(err.Error(), "不能同时使用") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunScheduleEditRequiresScheduleID(t *testing.T) {
	app := &App{stderr: &strings.Builder{}, stdout: &strings.Builder{}}
	err := app.runScheduleEdit(nil, []string{"--name", "new"})
	if err == nil {
		t.Fatal("expected error for missing schedule_id")
	}
}

// --- runScheduleDelete 确认流程测试 ---

func TestRunScheduleDeleteRequiresScheduleID(t *testing.T) {
	app := &App{stderr: &strings.Builder{}, stdout: &strings.Builder{}}
	err := app.runScheduleDelete(nil, []string{})
	if err == nil {
		t.Fatal("expected error for missing schedule_id")
	}
}

func TestRunScheduleDeleteCancelledOnReadError(t *testing.T) {
	// stdin 为空（非交互式），读取确认输入会失败，应视为取消
	var stdout strings.Builder
	app := &App{
		stderr: &strings.Builder{},
		stdout: &stdout,
		stdin:  strings.NewReader(""),
	}
	err := app.runScheduleDelete(nil, []string{"sched-1"})
	if err != nil {
		t.Fatalf("expected nil error (cancelled), got %v", err)
	}
	if !strings.Contains(stdout.String(), "已取消") {
		t.Fatalf("expected cancellation message, got %q", stdout.String())
	}
}

// --- runSchedule 路由测试 ---

func TestRunScheduleUnknownSubcommand(t *testing.T) {
	app := &App{stderr: &strings.Builder{}, stdout: &strings.Builder{}}
	err := app.runSchedule(nil, []string{"unknown"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "未知的 schedule 子命令") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunScheduleHelp(t *testing.T) {
	var stdout strings.Builder
	app := &App{stderr: &strings.Builder{}, stdout: &stdout}
	err := app.runSchedule(nil, []string{"help"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "定时任务管理") {
		t.Fatalf("expected usage output, got %q", stdout.String())
	}
}

func TestRunScheduleNoArgs(t *testing.T) {
	var stdout strings.Builder
	app := &App{stderr: &strings.Builder{}, stdout: &stdout}
	err := app.runSchedule(nil, []string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "用法") {
		t.Fatalf("expected usage output, got %q", stdout.String())
	}
}
