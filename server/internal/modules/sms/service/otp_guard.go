package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// 同一手机号同一场景 60 秒内只允许一次供应商提交，和前端倒计时保持一致。
	defaultOTPSendLimit = 1
	// 同一手机号同一场景在验证码有效期内最多允许五次错误尝试。
	defaultOTPCheckFailureLimit = 5
)

var otpGuardIncrementScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return count
`)

// RedisOTPGuard 使用手机号 HMAC 和固定场景构造低敏 Redis 键，实现跨实例原子门禁。
type RedisOTPGuard struct {
	client      *redis.Client
	phoneSecret string
	sendWindow  time.Duration
	checkWindow time.Duration
}

// NewRedisOTPGuard 创建短信 OTP 门禁。调用方只应在短信配置通过 ValidateSMS 后注入。
func NewRedisOTPGuard(client *redis.Client, phoneSecret string) *RedisOTPGuard {
	return &RedisOTPGuard{
		client: client, phoneSecret: phoneSecret,
		sendWindow: time.Minute, checkWindow: 10 * time.Minute,
	}
}

// AllowSend 原子占用手机号+场景发码窗口，超过一次返回 false。
func (g *RedisOTPGuard) AllowSend(ctx context.Context, phone, scene string) (bool, error) {
	count, err := g.increment(ctx, g.key("send", phone, scene), g.sendWindow)
	if err != nil {
		return false, err
	}
	return count <= defaultOTPSendLimit, err
}

// AllowCheckAttempt 在数据库校验前原子取得一次尝试资格，确保并发请求也不能越过五次硬边界。
func (g *RedisOTPGuard) AllowCheckAttempt(ctx context.Context, phone, scene string) (bool, error) {
	count, err := g.increment(ctx, g.key("failure", phone, scene), g.checkWindow)
	if err != nil {
		return false, err
	}
	return count <= defaultOTPCheckFailureLimit, nil
}

// ClearCheckFailures 在验证码成功消费后清除对应错误次数。
func (g *RedisOTPGuard) ClearCheckFailures(ctx context.Context, phone, scene string) error {
	if err := g.validate(); err != nil {
		return err
	}
	return g.client.Del(ctx, g.key("failure", phone, scene)).Err()
}

func (g *RedisOTPGuard) increment(ctx context.Context, key string, window time.Duration) (int64, error) {
	if err := g.validate(); err != nil {
		return 0, err
	}
	return otpGuardIncrementScript.Run(ctx, g.client, []string{key}, window.Milliseconds()).Int64()
}

func (g *RedisOTPGuard) validate() error {
	if g == nil || g.client == nil || len(g.phoneSecret) < 32 {
		return errors.New("短信验证码门禁未就绪")
	}
	return nil
}

func (g *RedisOTPGuard) key(kind, phone, scene string) string {
	// 场景已经由 Dispatcher 的固定枚举校验；手机号只以不可逆 HMAC 进入键空间。
	return fmt.Sprintf("sms:otp:%s:%s:%s", kind, scene, phoneHMAC(phone, g.phoneSecret))
}
