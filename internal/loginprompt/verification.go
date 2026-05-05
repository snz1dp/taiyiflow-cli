package loginprompt

import "strings"

const (
	VerifyTypeKaptcha = "kaptcha"
	VerifyTypeSMS     = "sms"
	VerifyTypeMail    = "mail"
	VerifyTypeTOTP    = "totp"
)

func NormalizeVerifyType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case VerifyTypeKaptcha:
		return VerifyTypeKaptcha
	case VerifyTypeSMS:
		return VerifyTypeSMS
	case VerifyTypeMail:
		return VerifyTypeMail
	case VerifyTypeTOTP:
		return VerifyTypeTOTP
	default:
		return ""
	}
}

func DetectVerifyType(message string, fallback string) string {
	if normalized := NormalizeVerifyType(fallback); normalized != "" {
		return normalized
	}
	lowerMessage := strings.ToLower(strings.TrimSpace(message))
	switch {
	case strings.Contains(lowerMessage, "kaptcha"), strings.Contains(lowerMessage, "captcha"), strings.Contains(lowerMessage, "图形"):
		return VerifyTypeKaptcha
	case strings.Contains(lowerMessage, "sms"), strings.Contains(lowerMessage, "短信"):
		return VerifyTypeSMS
	case strings.Contains(lowerMessage, "mail"), strings.Contains(lowerMessage, "email"), strings.Contains(lowerMessage, "邮箱"), strings.Contains(lowerMessage, "邮件"):
		return VerifyTypeMail
	case strings.Contains(lowerMessage, "totp"), strings.Contains(lowerMessage, "otp"), strings.Contains(lowerMessage, "口令"):
		return VerifyTypeTOTP
	case strings.Contains(lowerMessage, "验证码"):
		return VerifyTypeKaptcha
	default:
		return ""
	}
}

func HasVerificationSignal(message string) bool {
	return DetectVerifyType(message, "") != "" || strings.Contains(strings.ToLower(strings.TrimSpace(message)), "验证码")
}

func CodePlaceholder(verifyType string) string {
	if NormalizeVerifyType(verifyType) == VerifyTypeTOTP {
		return "请输入动态口令（TOTP）"
	}
	return "请输入验证码"
}

func TypeLabel(verifyType string) string {
	switch NormalizeVerifyType(verifyType) {
	case VerifyTypeKaptcha:
		return "图形验证码(image)"
	case VerifyTypeSMS:
		return "短信验证码(sms)"
	case VerifyTypeMail:
		return "邮箱验证码(mail)"
	case VerifyTypeTOTP:
		return "动态口令(totp)"
	default:
		return "验证码(verify_code)"
	}
}

func CodeLabel(verifyType string) string {
	switch NormalizeVerifyType(verifyType) {
	case VerifyTypeKaptcha:
		return "图形验证码(code)"
	case VerifyTypeSMS:
		return "短信验证码(sms_code)"
	case VerifyTypeMail:
		return "邮箱验证码(mail_code)"
	case VerifyTypeTOTP:
		return "动态口令(totp_code)"
	default:
		return "验证码(verify_code)"
	}
}

func Description(verifyType string, verifyHint string) string {
	if hint := strings.TrimSpace(verifyHint); hint != "" {
		return hint
	}
	switch NormalizeVerifyType(verifyType) {
	case VerifyTypeKaptcha:
		return "检测到需要图形验证码，请输入验证码后继续登录。"
	case VerifyTypeSMS:
		return "检测到需要短信验证码，请输入短信验证码后继续登录。"
	case VerifyTypeMail:
		return "检测到需要邮箱验证码，请输入邮箱验证码后继续登录。"
	case VerifyTypeTOTP:
		return "检测到需要动态口令（TOTP），请输入动态口令后继续登录。"
	default:
		return "检测到需要验证码，请输入验证码后继续登录。"
	}
}

func Notice(verifyType string, verifyHint string, verifyTarget string) string {
	if hint := strings.TrimSpace(verifyHint); hint != "" {
		return hint
	}
	message := "需要验证码，请输入验证码后继续登录。"
	switch NormalizeVerifyType(verifyType) {
	case VerifyTypeKaptcha:
		message = "需要图形验证码，请输入验证码后继续登录。"
	case VerifyTypeSMS:
		message = "需要短信验证码，请输入短信验证码后继续登录。"
	case VerifyTypeMail:
		message = "需要邮箱验证码，请输入邮箱验证码后继续登录。"
	case VerifyTypeTOTP:
		message = "需要动态口令（TOTP），请输入动态口令后继续登录。"
	}
	if target := strings.TrimSpace(verifyTarget); target != "" {
		message += " 验证目标: " + target + "。"
	}
	return message
}