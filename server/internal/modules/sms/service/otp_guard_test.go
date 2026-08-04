package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestOTPGuardKeyUsesPhoneHMACAndSceneIsolation(t *testing.T) {
	phone := fmt.Sprintf("%s%08d", "138", 12345678)
	guard := NewRedisOTPGuard(nil, strings.Repeat("s", 32))
	registerKey := guard.key("send", phone, "register")
	loginKey := guard.key("send", phone, "login")

	if strings.Contains(registerKey, phone) {
		t.Fatal("Redis 门禁键不得包含完整手机号")
	}
	if registerKey == loginKey {
		t.Fatal("同一手机号的不同业务场景必须使用独立限流桶")
	}
	if !strings.HasPrefix(registerKey, "sms:otp:send:register:") {
		t.Fatalf("Redis 门禁键必须保留固定用途和场景维度: %s", registerKey)
	}
}

func TestOTPGuardFailsClosedWithoutRedisOrStrongSecret(t *testing.T) {
	guards := []*RedisOTPGuard{
		NewRedisOTPGuard(nil, strings.Repeat("s", 32)),
		NewRedisOTPGuard(nil, "short"),
	}
	for _, guard := range guards {
		if allowed, err := guard.AllowSend(context.Background(), "phone-test-value", "register"); err == nil || allowed {
			t.Fatal("Redis 或强 HMAC 密钥未就绪时必须关闭失败")
		}
		if allowed, err := guard.AllowCheckAttempt(context.Background(), "phone-test-value", "register"); err == nil || allowed {
			t.Fatal("校验门禁未就绪时必须按已锁定处理")
		}
	}
}
