package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrResourceUnavailable = errors.New("资源治理服务不可用")
	ErrConcurrencyExceeded = errors.New("并发达到上限")
	ErrRateLimitExceeded   = errors.New("请求速率达到上限")
)

type ResourceLimitError struct {
	Cause      error
	LimitScope string
	LimitType  string
	RetryAfter time.Duration
}

func (e *ResourceLimitError) Error() string { return e.Cause.Error() }
func (e *ResourceLimitError) Unwrap() error { return e.Cause }

type ResourceLimits struct {
	Concurrency uint64
	RPM         uint64
	TPM         uint64
}

type ResourceDefaults struct {
	User    ResourceLimits
	Project ResourceLimits
	APIKey  ResourceLimits
	Model   ResourceLimits
}

type resourcePolicyReader interface {
	LoadResourcePolicies(ctx context.Context, scopeKeys map[string]string) (map[string]model.AIResourcePolicy, error)
}

type ResourceTicket struct {
	LeaseID     string
	Scopes      []string
	Keys        []string
	ReservedTPM uint64
}

// ResourceLimiter 使用单个 Redis Lua 脚本原子检查四层并发、RPM 和 TPM，任何不确定结果都失败关闭。
type ResourceLimiter struct {
	redis      redis.UniversalClient
	policies   resourcePolicyReader
	defaults   ResourceDefaults
	leaseTTL   time.Duration
	windowTTL  time.Duration
	acquireLua *redis.Script
	renewLua   *redis.Script
	releaseLua *redis.Script
	reconcile  *redis.Script
}

func NewResourceLimiter(client redis.UniversalClient, policies resourcePolicyReader, defaults ResourceDefaults) *ResourceLimiter {
	return &ResourceLimiter{
		redis: client, policies: policies, defaults: defaults, leaseTTL: 90 * time.Second, windowTTL: 60 * time.Second,
		acquireLua: redis.NewScript(resourceAcquireLua), renewLua: redis.NewScript(resourceRenewLua),
		releaseLua: redis.NewScript(resourceReleaseLua), reconcile: redis.NewScript(resourceReconcileLua),
	}
}

func (s *ResourceLimiter) Acquire(ctx context.Context, requestID string, userID, projectID, apiKeyID uint64, logicalModel string, reservedTokens uint64) (*ResourceTicket, error) {
	if s == nil || s.redis == nil || s.policies == nil || requestID == "" || reservedTokens == 0 {
		return nil, ErrResourceUnavailable
	}
	scopeKeys := map[string]string{
		"user": strconv.FormatUint(userID, 10), "project": strconv.FormatUint(projectID, 10),
		"api_key": strconv.FormatUint(apiKeyID, 10), "model": logicalModel,
	}
	policies, err := s.policies.LoadResourcePolicies(ctx, scopeKeys)
	if err != nil {
		return nil, ErrResourceUnavailable
	}
	scopes := []string{"user", "project", "api_key", "model"}
	limits := []ResourceLimits{s.defaults.User, s.defaults.Project, s.defaults.APIKey, s.defaults.Model}
	keys := make([]string, 0, 16)
	for index, scope := range scopes {
		if policy, ok := policies[scope]; ok {
			limits[index] = ResourceLimits{Concurrency: policy.ConcurrencyLimit, RPM: policy.RPMLimit, TPM: policy.TPMLimit}
		}
		if limits[index].Concurrency == 0 || limits[index].RPM == 0 || limits[index].TPM == 0 {
			return nil, ErrResourceUnavailable
		}
		prefix := "molin:{ai-g4}:" + scope + ":" + scopeKeys[scope]
		keys = append(keys, prefix+":concurrency", prefix+":rpm", prefix+":tpm:time", prefix+":tpm:value")
	}
	now := time.Now()
	args := []interface{}{requestID, now.UnixMilli(), now.Add(s.leaseTTL).UnixMilli(), now.Add(-s.windowTTL).UnixMilli(), reservedTokens, s.leaseTTL.Milliseconds(), (2 * s.windowTTL).Milliseconds(), s.windowTTL.Milliseconds()}
	for _, limit := range limits {
		args = append(args, limit.Concurrency, limit.RPM, limit.TPM)
	}
	result, err := s.acquireLua.Run(ctx, s.redis, keys, args...).Slice()
	if err != nil || len(result) < 1 {
		return nil, ErrResourceUnavailable
	}
	allowed, _ := toInt64(result[0])
	if allowed != 1 {
		if len(result) < 4 {
			return nil, ErrResourceUnavailable
		}
		scopeIndex, _ := toInt64(result[1])
		reason, _ := toInt64(result[2])
		retryMS, _ := toInt64(result[3])
		if scopeIndex < 1 || scopeIndex > int64(len(scopes)) {
			return nil, ErrResourceUnavailable
		}
		cause := ErrRateLimitExceeded
		limitType := "rpm"
		if reason == 1 {
			cause = ErrConcurrencyExceeded
			limitType = "concurrency"
		} else if reason == 3 {
			limitType = "tpm"
		}
		return nil, &ResourceLimitError{Cause: cause, LimitScope: scopes[scopeIndex-1], LimitType: limitType, RetryAfter: time.Duration(max(retryMS, 1000)) * time.Millisecond}
	}
	return &ResourceTicket{LeaseID: requestID, Scopes: scopes, Keys: keys, ReservedTPM: reservedTokens}, nil
}

func (s *ResourceLimiter) Renew(ctx context.Context, ticket *ResourceTicket) error {
	if s == nil || ticket == nil || len(ticket.Keys) != 16 {
		return ErrResourceUnavailable
	}
	result, err := s.renewLua.Run(ctx, s.redis, ticket.Keys, ticket.LeaseID, time.Now().Add(s.leaseTTL).UnixMilli(), s.leaseTTL.Milliseconds()).Int64()
	if err != nil || result != 1 {
		return ErrResourceUnavailable
	}
	return nil
}

func (s *ResourceLimiter) Release(ctx context.Context, ticket *ResourceTicket) error {
	if s == nil || ticket == nil || len(ticket.Keys) != 16 {
		return nil
	}
	if _, err := s.releaseLua.Run(ctx, s.redis, ticket.Keys, ticket.LeaseID).Result(); err != nil {
		return ErrResourceUnavailable
	}
	return nil
}

func (s *ResourceLimiter) ReconcileTokens(ctx context.Context, ticket *ResourceTicket, actual uint64) error {
	if s == nil || ticket == nil || len(ticket.Keys) != 16 {
		return nil
	}
	if _, err := s.reconcile.Run(ctx, s.redis, ticket.Keys, ticket.LeaseID, actual).Result(); err != nil {
		return ErrResourceUnavailable
	}
	return nil
}

func (s *ResourceLimiter) StartHeartbeat(ctx context.Context, ticket *ResourceTicket) <-chan error {
	result := make(chan error, 1)
	go func() {
		defer close(result)
		ticker := time.NewTicker(s.leaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := s.Renew(context.WithoutCancel(ctx), ticket); err != nil {
					result <- err
					return
				}
			}
		}
	}()
	return result
}

func conservativeTokenReservation(body map[string]interface{}, maxOutput uint64) (uint64, error) {
	text := extractRequestText(body)
	if text == "" || maxOutput == 0 {
		return 0, ErrResourceUnavailable
	}
	// UTF-8 字节数是文字模型输入 Token 的保守上界，宁可多预留也不能低估后继续收费调用。
	input := uint64(len([]byte(text)))
	if ^uint64(0)-input < maxOutput {
		return 0, ErrResourceUnavailable
	}
	return input + maxOutput, nil
}

func toInt64(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	default:
		return 0, fmt.Errorf("Redis Lua 返回类型异常: %T", value)
	}
}

const resourceAcquireLua = `
local lease = ARGV[1]
local now = tonumber(ARGV[2])
local lease_expire = tonumber(ARGV[3])
local window_start = tonumber(ARGV[4])
local tokens = tonumber(ARGV[5])
local lease_ttl = tonumber(ARGV[6])
local window_ttl = tonumber(ARGV[7])
local window_duration = tonumber(ARGV[8])
for i = 0, 3 do
  local base = i * 4
  local limit_base = 9 + i * 3
  local concurrency_limit = tonumber(ARGV[limit_base])
  local rpm_limit = tonumber(ARGV[limit_base + 1])
  local tpm_limit = tonumber(ARGV[limit_base + 2])
  redis.call('ZREMRANGEBYSCORE', KEYS[base + 1], 0, now)
  if redis.call('ZCARD', KEYS[base + 1]) >= concurrency_limit then
    local oldest = redis.call('ZRANGE', KEYS[base + 1], 0, 0, 'WITHSCORES')
    local retry = 1000
    if #oldest == 2 then retry = math.max(1000, tonumber(oldest[2]) - now) end
    return {0, i + 1, 1, retry}
  end
  redis.call('ZREMRANGEBYSCORE', KEYS[base + 2], 0, window_start)
  if redis.call('ZCARD', KEYS[base + 2]) >= rpm_limit then
    local oldest = redis.call('ZRANGE', KEYS[base + 2], 0, 0, 'WITHSCORES')
    local retry = 1000
    if #oldest == 2 then retry = math.max(1000, tonumber(oldest[2]) + window_duration - now) end
    return {0, i + 1, 2, retry}
  end
  local expired = redis.call('ZRANGEBYSCORE', KEYS[base + 3], 0, window_start)
  for _, member in ipairs(expired) do redis.call('HDEL', KEYS[base + 4], member) end
  redis.call('ZREMRANGEBYSCORE', KEYS[base + 3], 0, window_start)
  local values = redis.call('HVALS', KEYS[base + 4])
  local total = 0
  for _, value in ipairs(values) do total = total + tonumber(value) end
  if total + tokens > tpm_limit then
    local oldest = redis.call('ZRANGE', KEYS[base + 3], 0, 0, 'WITHSCORES')
    local retry = 1000
    if #oldest == 2 then retry = math.max(1000, tonumber(oldest[2]) + window_duration - now) end
    return {0, i + 1, 3, retry}
  end
end
for i = 0, 3 do
  local base = i * 4
  redis.call('ZADD', KEYS[base + 1], lease_expire, lease)
  redis.call('PEXPIRE', KEYS[base + 1], lease_ttl * 2)
  redis.call('ZADD', KEYS[base + 2], now, lease)
  redis.call('PEXPIRE', KEYS[base + 2], window_ttl)
  redis.call('ZADD', KEYS[base + 3], now, lease)
  redis.call('HSET', KEYS[base + 4], lease, tokens)
  redis.call('PEXPIRE', KEYS[base + 3], window_ttl)
  redis.call('PEXPIRE', KEYS[base + 4], window_ttl)
end
return {1}
`

const resourceRenewLua = `
for i = 0, 3 do
  local key = KEYS[i * 4 + 1]
  if redis.call('ZSCORE', key, ARGV[1]) == false then return 0 end
end
for i = 0, 3 do
  local key = KEYS[i * 4 + 1]
  redis.call('ZADD', key, 'XX', tonumber(ARGV[2]), ARGV[1])
  redis.call('PEXPIRE', key, tonumber(ARGV[3]) * 2)
end
return 1
`

const resourceReleaseLua = `
for i = 0, 3 do redis.call('ZREM', KEYS[i * 4 + 1], ARGV[1]) end
return 1
`

const resourceReconcileLua = `
for i = 0, 3 do
  local key = KEYS[i * 4 + 4]
  if redis.call('HEXISTS', key, ARGV[1]) == 1 then redis.call('HSET', key, ARGV[1], tonumber(ARGV[2])) end
end
return 1
`
