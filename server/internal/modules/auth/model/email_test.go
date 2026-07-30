package model

import "testing"

func TestEmailTestRecipientAllowlistTableName(t *testing.T) {
	if got := (EmailTestRecipientAllowlist{}).TableName(); got != "email_test_recipient_allowlist" {
		t.Fatalf("测试邮箱白名单模型表名错误: %s", got)
	}
}
