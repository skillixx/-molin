package main

import (
	"strings"
	"testing"
)

func TestRunRequiresExplicitApprovalAndVersion(t *testing.T) {
	t.Setenv("AI_PRICE_PUBLISH_APPROVED", "")
	if err := run([]string{"-version-id", "1"}); err == nil || !strings.Contains(err.Error(), "显式设置") {
		t.Fatalf("缺少批准门时必须在连接数据库前拒绝: %v", err)
	}
	t.Setenv("AI_PRICE_PUBLISH_APPROVED", "YES")
	if err := run([]string{"-version-id", "0"}); err == nil || !strings.Contains(err.Error(), "正整数") {
		t.Fatalf("零版本 ID 必须在连接数据库前拒绝: %v", err)
	}
	t.Setenv("APP_ENV", "")
	if err := run([]string{"-version-id", "1"}); err == nil || !strings.Contains(err.Error(), "显式设置") {
		t.Fatalf("APP_ENV 缺失或空值必须失败关闭: %v", err)
	}
	t.Setenv("APP_ENV", "production")
	if err := run([]string{"-version-id", "1"}); err == nil || !strings.Contains(err.Error(), "非生产") {
		t.Fatalf("生产环境必须在连接数据库前拒绝: %v", err)
	}
	t.Setenv("APP_ENV", "unknown")
	if err := run([]string{"-version-id", "1"}); err == nil || !strings.Contains(err.Error(), "非生产") {
		t.Fatalf("未知环境必须失败关闭: %v", err)
	}
}
