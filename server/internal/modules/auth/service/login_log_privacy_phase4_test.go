package service

import "testing"

func TestPhase4LoginAccountIsSanitizedBeforePersistence(t *testing.T) {
	phone := "138" + "0000" + "0001"
	if got := sanitizeLoginAccount("phone", phone); got == phone || got != "138****0001" {
		t.Fatalf("手机号登录账号必须在落库前脱敏: match_raw=%v", got == phone)
	}
	emailCases := []string{
		"phase4-user" + "@" + "example.invalid",
		"a" + "@" + "example.invalid",
		"ab" + "@" + "example.invalid",
		"用户" + "@" + "example.invalid",
	}
	for _, email := range emailCases {
		if got := sanitizeLoginAccount("email", email); got == email || got == "" {
			t.Fatalf("邮箱登录账号必须在落库前脱敏: match_raw=%v", got == email)
		}
	}
	for _, invalid := range []string{"short", "@domain.invalid", "local@", "a b@domain.invalid"} {
		if got := sanitizeLoginAccount("email", invalid); got != "***" {
			t.Fatalf("异常邮箱必须失败关闭为占位: input_length=%d", len(invalid))
		}
	}
	for _, invalidPhone := range []string{"short", "128" + "0000" + "0001", "1380000000x"} {
		if got := sanitizeLoginAccount("phone", invalidPhone); got != "***" {
			t.Fatalf("异常手机号必须失败关闭为占位: input_length=%d", len(invalidPhone))
		}
	}
	if got := sanitizeLoginAccount("unknown", "private-account"); got != "***" {
		t.Fatal("未知登录类型必须失败关闭为不可识别占位")
	}
}
