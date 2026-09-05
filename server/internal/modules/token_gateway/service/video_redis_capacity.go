package service

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	video "molin/server/internal/modules/token_gateway/video"
)

var (
	ErrVideoCapacityUnavailable = errors.New("视频容量协调暂不可用")
	ErrVideoCapacityConflict    = errors.New("视频容量意图或尝试代次冲突")
	ErrVideoCapacityLeaseLost   = errors.New("视频容量持有者已失效")
	ErrVideoCapacityFull        = errors.New("视频容量已满")
)

const videoCapacityStateKey = "molin:{video-g7}:capacity:active-v1"

// 本组件只操作Redis容量快照，不负责用户授权、MySQL提交证明或Provider调用。
type VideoCapacityIdentity struct {
	TaskID, RequestID          string
	UserID, ProjectID          uint64
	APIKeyID                   *uint64
	Model, Provider, Operation string
}

// 主体ID必须是十进制字符串，防止Lua/cjson把uint64舍入到同一主体。
type videoCapacityIdentityJSON struct {
	Task      string `json:"task"`
	Request   string `json:"request"`
	User      string `json:"user"`
	Project   string `json:"project"`
	Key       string `json:"key"`
	Model     string `json:"model"`
	Provider  string `json:"provider"`
	Operation string `json:"operation"`
}

type VideoCapacityAttempt struct {
	identity string
	task     string
	nonce    string
}

// 同一网络尝试保留这个不可变对象；不能在结果未知时另造nonce覆盖旧预留。
func NewVideoCapacityAttempt(identity VideoCapacityIdentity) (*VideoCapacityAttempt, error) {
	return newVideoCapacityAttempt(identity, "")
}

func newVideoCapacityAttempt(identity VideoCapacityIdentity, fixedNonce string) (*VideoCapacityAttempt, error) {
	body, err := canonicalVideoCapacityIdentity(identity)
	if err != nil {
		return nil, err
	}
	nonce := fixedNonce
	if nonce == "" {
		secret := make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, ErrVideoCapacityUnavailable
		}
		nonce = hex.EncodeToString(secret)
	} else if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(nonce) {
		return nil, ErrVideoCapacityConflict
	}
	return &VideoCapacityAttempt{identity: body, task: identity.TaskID, nonce: nonce}, nil
}

func canonicalVideoCapacityIdentity(identity VideoCapacityIdentity) (string, error) {
	if identity.UserID == 0 || identity.ProjectID == 0 || !videoBillingPublicID.MatchString(identity.TaskID) || !videoBillingPublicID.MatchString(identity.RequestID) || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,190}$`).MatchString(identity.Model) || identity.Provider != "fake-native-async" || (identity.Operation != "text_to_video" && identity.Operation != "image_to_video") {
		return "", ErrVideoCapacityConflict
	}
	user, project := strconv.FormatUint(identity.UserID, 10), strconv.FormatUint(identity.ProjectID, 10)
	key := "jwt:" + user + ":" + project
	if identity.APIKeyID != nil {
		if *identity.APIKeyID == 0 {
			return "", ErrVideoCapacityConflict
		}
		key = "sk:" + strconv.FormatUint(*identity.APIKeyID, 10)
	}
	body, err := json.Marshal(videoCapacityIdentityJSON{Task: identity.TaskID, Request: identity.RequestID, User: user, Project: project, Key: key, Model: identity.Model, Provider: identity.Provider, Operation: identity.Operation})
	if err != nil {
		return "", ErrVideoCapacityConflict
	}
	return string(body), nil
}

// 值接收者同时保护指针与解引用副本，内部尝试能力不进入普通JSON或格式化日志。
func (VideoCapacityAttempt) MarshalJSON() ([]byte, error) { return []byte(`{"redacted":true}`), nil }
func (VideoCapacityAttempt) String() string               { return "[video capacity attempt]" }
func (VideoCapacityAttempt) GoString() string             { return "[video capacity attempt]" }

type VideoCapacityView struct {
	Phase                 string
	ExpiresAt, ObservedAt time.Time
	Expired               bool
}

type VideoCapacityLimitError struct {
	Scope      string
	RetryAfter time.Duration
}

func (*VideoCapacityLimitError) Error() string { return ErrVideoCapacityFull.Error() }
func (*VideoCapacityLimitError) Unwrap() error { return ErrVideoCapacityFull }

// 构造器不会初始化空Redis，也不会宣布恢复完成。epoch必须来自后续持久化协调器。
type RedisVideoCapacityStore struct {
	client *redis.Client
	epoch  string
	policy string
	limits [10]uint32
}

func NewRedisVideoCapacityStore(client *redis.Client, epoch uint64, policy *video.VideoCapacityPolicy) (*RedisVideoCapacityStore, error) {
	if client == nil || epoch == 0 || client.Options().MaxRetries != 0 || !client.Options().ContextTimeoutEnabled {
		return nil, ErrVideoCapacityUnavailable
	}
	limits, err := policy.Limits()
	if err != nil {
		return nil, err
	}
	hash, err := policy.Fingerprint()
	if err != nil {
		return nil, err
	}
	return &RedisVideoCapacityStore{client: client, epoch: strconv.FormatUint(epoch, 10), policy: hash, limits: [10]uint32{limits.Queued.User, limits.Queued.Project, limits.Queued.APIKey, limits.Queued.Model, limits.Queued.Global, limits.Running.User, limits.Running.Project, limits.Running.APIKey, limits.Running.Model, limits.Running.Provider}}, nil
}

func (s *RedisVideoCapacityStore) ReserveQueued(ctx context.Context, attempt *VideoCapacityAttempt) (*VideoCapacityView, error) {
	return s.execute(ctx, "reserve", attempt)
}

func (s *RedisVideoCapacityStore) Read(ctx context.Context, attempt *VideoCapacityAttempt) (*VideoCapacityView, error) {
	return s.execute(ctx, "read", attempt)
}

// 只预留running并保留queued；后续数据库提交协调器尚未接线，不能据此调用Provider。
func (s *RedisVideoCapacityStore) PrepareRunning(ctx context.Context, attempt *VideoCapacityAttempt) (*VideoCapacityView, error) {
	return s.execute(ctx, "prepare", attempt)
}

func (s *RedisVideoCapacityStore) Renew(ctx context.Context, attempt *VideoCapacityAttempt) (*VideoCapacityView, error) {
	return s.execute(ctx, "renew", attempt)
}

// confirmRunning只完成已预留promoting到running的原子切换；只能由同包持久协调器在MySQL提交后调用。
func (s *RedisVideoCapacityStore) confirmRunning(ctx context.Context, attempt *VideoCapacityAttempt) (*VideoCapacityView, error) {
	return s.execute(ctx, "confirm", attempt)
}

// releaseCapacity只移除完全匹配的容量记录；只能由同包协调器先证明安全终态、Provider结束和财务事实。
func (s *RedisVideoCapacityStore) releaseCapacity(ctx context.Context, attempt *VideoCapacityAttempt) (*VideoCapacityView, error) {
	return s.execute(ctx, "release", attempt)
}

// abortPromotion只用于MySQL明确未提交时把同一promoting退回queued；提交结果未知时禁止调用。
func (s *RedisVideoCapacityStore) abortPromotion(ctx context.Context, attempt *VideoCapacityAttempt) (*VideoCapacityView, error) {
	return s.execute(ctx, "abort", attempt)
}

// Lua仅返回低敏观察；该结果不能代替MySQL提交证明、Worker围栏或Provider授权。
func (s *RedisVideoCapacityStore) execute(ctx context.Context, action string, attempt *VideoCapacityAttempt) (*VideoCapacityView, error) {
	if s == nil || s.client == nil || ctx == nil || attempt == nil || attempt.identity == "" || attempt.task == "" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(attempt.nonce) {
		return nil, ErrVideoCapacityUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	limits, err := json.Marshal(s.limits)
	if err != nil {
		return nil, ErrVideoCapacityUnavailable
	}
	// 客户端构造时已禁用自动重试。结果未知只留给原尝试查询，不更换nonce猜测重新预留。
	values, err := s.client.Eval(bounded, videoCapacityLua, []string{videoCapacityStateKey}, action, s.epoch, s.policy, attempt.task, attempt.identity, attempt.nonce, string(limits)).Slice()
	if err != nil || len(values) < 1 {
		return nil, ErrVideoCapacityUnavailable
	}
	code, ok := values[0].(int64)
	if !ok {
		return nil, ErrVideoCapacityUnavailable
	}
	switch code {
	case 0:
		return nil, ErrVideoCapacityUnavailable
	case 2:
		return nil, ErrVideoCapacityConflict
	case 3:
		return nil, ErrVideoCapacityLeaseLost
	case 4:
		if len(values) != 2 {
			return nil, ErrVideoCapacityUnavailable
		}
		scope, ok := values[1].(string)
		if !ok || (scope != "user" && scope != "project" && scope != "api_key" && scope != "model" && scope != "global" && scope != "provider") {
			return nil, ErrVideoCapacityUnavailable
		}
		return nil, &VideoCapacityLimitError{Scope: scope, RetryAfter: time.Second}
	case 1:
		if len(values) != 4 {
			return nil, ErrVideoCapacityUnavailable
		}
		phase, phaseOK := values[1].(string)
		expires, expiresOK := values[2].(int64)
		now, nowOK := values[3].(int64)
		if !phaseOK || !expiresOK || !nowOK || expires <= 0 || now <= 0 || (phase != "queued" && phase != "promoting" && phase != "running" && phase != "released") {
			return nil, ErrVideoCapacityUnavailable
		}
		return &VideoCapacityView{Phase: phase, ExpiresAt: time.UnixMilli(expires).UTC(), ObservedAt: time.UnixMilli(now).UTC(), Expired: expires <= now}, nil
	default:
		return nil, ErrVideoCapacityUnavailable
	}
}

// 脚本与Go源码一同冻结和计算哈希，便于审查所有读取检查均先于唯一SET。
//
//go:embed video_redis_capacity.lua
var videoCapacityLua string
