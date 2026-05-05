package loginprompt

import "testing"

func TestNormalizeVerifyType(t *testing.T) {
	tests := map[string]string{
		"kaptcha": VerifyTypeKaptcha,
		" SMS ":   VerifyTypeSMS,
		"mail":    VerifyTypeMail,
		"ToTp":    VerifyTypeTOTP,
		"unknown": "",
	}

	for input, want := range tests {
		if got := NormalizeVerifyType(input); got != want {
			t.Fatalf("NormalizeVerifyType(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDetectVerifyType(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		fallback string
		want     string
	}{
		{name: "fallback wins", message: "ignored", fallback: VerifyTypeMail, want: VerifyTypeMail},
		{name: "captcha keyword", message: "需要图形验证码", want: VerifyTypeKaptcha},
		{name: "sms keyword", message: "需要短信验证码", want: VerifyTypeSMS},
		{name: "mail keyword", message: "请查收企业邮箱验证码", want: VerifyTypeMail},
		{name: "totp keyword", message: "请输入动态口令 totp", want: VerifyTypeTOTP},
		{name: "generic verify code", message: "需要验证码", want: VerifyTypeKaptcha},
		{name: "unknown", message: "用户名或密码错误", want: ""},
	}

	for _, testCase := range tests {
		if got := DetectVerifyType(testCase.message, testCase.fallback); got != testCase.want {
			t.Fatalf("%s: DetectVerifyType(%q, %q) = %q, want %q", testCase.name, testCase.message, testCase.fallback, got, testCase.want)
		}
	}
}

func TestHasVerificationSignal(t *testing.T) {
	if !HasVerificationSignal("需要短信验证码") {
		t.Fatal("expected sms challenge message to be recognized")
	}
	if !HasVerificationSignal("需要验证码") {
		t.Fatal("expected generic verification message to be recognized")
	}
	if HasVerificationSignal("用户名或密码错误") {
		t.Fatal("expected non-verification message to be ignored")
	}
}

func TestVerificationCopyHelpers(t *testing.T) {
	if got := CodePlaceholder(VerifyTypeTOTP); got != "请输入动态口令（TOTP）" {
		t.Fatalf("unexpected totp placeholder: %q", got)
	}
	if got := TypeLabel(VerifyTypeMail); got != "邮箱验证码(mail)" {
		t.Fatalf("unexpected mail type label: %q", got)
	}
	if got := CodeLabel(VerifyTypeSMS); got != "短信验证码(sms_code)" {
		t.Fatalf("unexpected sms code label: %q", got)
	}
	if got := Description(VerifyTypeTOTP, ""); got != "检测到需要动态口令（TOTP），请输入动态口令后继续登录。" {
		t.Fatalf("unexpected totp description: %q", got)
	}
	if got := Description(VerifyTypeSMS, "请输入短信验证码"); got != "请输入短信验证码" {
		t.Fatalf("expected explicit verify hint to win, got %q", got)
	}
	if got := Notice(VerifyTypeMail, "", "zhangsan@example.com"); got != "需要邮箱验证码，请输入邮箱验证码后继续登录。 验证目标: zhangsan@example.com。" {
		t.Fatalf("unexpected mail notice: %q", got)
	}
	if got := Notice(VerifyTypeSMS, "请先查收短信", "138****0000"); got != "请先查收短信" {
		t.Fatalf("expected explicit notice hint to win, got %q", got)
	}
}
