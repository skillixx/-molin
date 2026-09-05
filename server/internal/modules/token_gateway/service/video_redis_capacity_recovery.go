package service

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	video "molin/server/internal/modules/token_gateway/video"
)

type VideoCapacityRecoveryRecord struct {
	Attempt *VideoCapacityAttempt
	Phase   string
}

type videoCapacityRecoveryRecordJSON struct {
	Identity string `json:"identity"`
	Attempt  string `json:"attempt"`
	Phase    string `json:"phase"`
}

type videoCapacityRecoverySnapshotJSON struct {
	Schema  int                                        `json:"schema"`
	Epoch   string                                     `json:"epoch"`
	Policy  string                                     `json:"policy"`
	Count   int                                        `json:"count"`
	Records map[string]videoCapacityRecoveryRecordJSON `json:"records"`
}

// VideoCapacityRecoverySnapshot冻结一次完整重建输入；内部nonce不能通过JSON或格式化日志泄露。
type VideoCapacityRecoverySnapshot struct {
	epoch, policy, raw, digest string
	count                      int
}

func (VideoCapacityRecoverySnapshot) MarshalJSON() ([]byte, error) {
	return []byte(`{"redacted":true}`), nil
}
func (VideoCapacityRecoverySnapshot) String() string   { return "[video capacity recovery snapshot]" }
func (VideoCapacityRecoverySnapshot) GoString() string { return "[video capacity recovery snapshot]" }
func (s *VideoCapacityRecoverySnapshot) Digest() string {
	if s == nil {
		return ""
	}
	return s.digest
}
func (s *VideoCapacityRecoverySnapshot) Count() int {
	if s == nil {
		return 0
	}
	return s.count
}

func newVideoCapacityRecoverySnapshot(epoch uint64, policy *video.VideoCapacityPolicy, records []VideoCapacityRecoveryRecord) (*VideoCapacityRecoverySnapshot, error) {
	if epoch == 0 || len(records) > 102 {
		return nil, ErrVideoCapacityConflict
	}
	hash, err := policy.Fingerprint()
	if err != nil {
		return nil, ErrVideoCapacityConflict
	}
	body := videoCapacityRecoverySnapshotJSON{Schema: 1, Epoch: strconv.FormatUint(epoch, 10), Policy: hash, Count: len(records), Records: make(map[string]videoCapacityRecoveryRecordJSON, len(records))}
	requests := map[string]bool{}
	hard := map[string]int{}
	ceilings := []int{2, 10, 2, 100, 100, 1, 2, 1, 2, 2}
	for _, record := range records {
		a := record.Attempt
		if a == nil || a.task == "" || a.identity == "" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(a.nonce) || (record.Phase != "queued" && record.Phase != "running") {
			return nil, ErrVideoCapacityConflict
		}
		var id videoCapacityIdentityJSON
		if json.Unmarshal([]byte(a.identity), &id) != nil || id.Task != a.task || requests[id.Request] || body.Records[a.task].Identity != "" {
			return nil, ErrVideoCapacityConflict
		}
		requests[id.Request] = true
		scopes := []string{id.User, id.Project, id.Key, id.Model, "global", id.User, id.Project, id.Key, id.Model, id.Provider}
		for i, scope := range scopes {
			if (i < 5 && record.Phase != "running") || (i >= 5 && record.Phase != "queued") {
				bucket := strconv.Itoa(i) + ":" + scope
				hard[bucket]++
				if hard[bucket] > ceilings[i] {
					return nil, ErrVideoCapacityConflict
				}
			}
		}
		body.Records[a.task] = videoCapacityRecoveryRecordJSON{Identity: a.identity, Attempt: a.nonce, Phase: record.Phase}
	}
	raw, err := json.Marshal(body)
	if err != nil || len(raw) > 128*1024 {
		return nil, ErrVideoCapacityConflict
	}
	digest := sha256.Sum256(raw)
	return &VideoCapacityRecoverySnapshot{epoch: body.Epoch, policy: hash, raw: string(raw), digest: hex.EncodeToString(digest[:]), count: len(records)}, nil
}

type VideoCapacityRecoveryView struct {
	Status string
	Count  int
}

// ValidateRunID把MySQL proof冻结的实例身份与实际执行连接核对；不相信环境缓存或调用方字符串。
func (s *RedisVideoCapacityStore) ValidateRunID(ctx context.Context, expected string) error {
	if s == nil || s.client == nil || ctx == nil || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(expected) {
		return ErrVideoCapacityUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	info, err := s.client.Info(bounded, "server").Result()
	if err != nil {
		return ErrVideoCapacityUnavailable
	}
	match := regexp.MustCompile(`(?m)^run_id:([0-9a-f]{40})\r?$`).FindStringSubmatch(info)
	if len(match) != 2 || match[1] != expected {
		return ErrVideoCapacityUnavailable
	}
	return nil
}

// ReadVideoRedisRunID从实际连接读取本次Redis进程身份，不接受配置缓存代替。
func ReadVideoRedisRunID(ctx context.Context, client *redis.Client) (string, error) {
	if ctx == nil || client == nil {
		return "", ErrVideoCapacityUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	info, err := client.Info(bounded, "server").Result()
	if err != nil {
		return "", ErrVideoCapacityUnavailable
	}
	match := regexp.MustCompile(`(?m)^run_id:([0-9a-f]{40})\r?$`).FindStringSubmatch(info)
	if len(match) != 2 {
		return "", ErrVideoCapacityUnavailable
	}
	return match[1], nil
}

// ValidateReadyState在单次Lua执行中复核无TTL、完整结构、实际run_id及ready头，供非领导实例安全加入。
func (s *RedisVideoCapacityStore) ValidateReadyState(ctx context.Context, expectedRunID string) (int, error) {
	if s == nil || s.client == nil || ctx == nil || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(expectedRunID) {
		return 0, ErrVideoCapacityUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	values, err := s.client.Eval(bounded, videoCapacityRecoveryLua, []string{videoCapacityStateKey}, "header", s.epoch, s.policy, expectedRunID).Slice()
	if err != nil || len(values) != 3 {
		return 0, ErrVideoCapacityUnavailable
	}
	code, codeOK := values[0].(int64)
	status, statusOK := values[1].(string)
	count, countOK := values[2].(int64)
	if !codeOK || code != 1 || !statusOK || status != "ready" || !countOK || count < 0 || count > 102 {
		return 0, ErrVideoCapacityUnavailable
	}
	return int(count), nil
}

func (s *RedisVideoCapacityStore) CollectCapacityCounts(ctx context.Context, expectedRunID string) (map[string]uint64, error) {
	if s == nil || s.client == nil || ctx == nil || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(expectedRunID) {
		return nil, ErrVideoCapacityUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	values, err := s.client.Eval(bounded, videoCapacityRecoveryLua, []string{videoCapacityStateKey}, "metrics", s.epoch, s.policy, expectedRunID).Slice()
	if err != nil || len(values) != 4 {
		return nil, ErrVideoCapacityUnavailable
	}
	code, ok := values[0].(int64)
	if !ok || code != 1 {
		return nil, ErrVideoCapacityUnavailable
	}
	result := map[string]uint64{}
	for index, phase := range []string{"queued", "promoting", "running"} {
		value, ok := values[index+1].(int64)
		if !ok || value < 0 || value > 102 {
			return nil, ErrVideoCapacityUnavailable
		}
		result[phase] = uint64(value)
	}
	return result, nil
}

func (s *RedisVideoCapacityStore) StageRecovery(ctx context.Context, snapshot *VideoCapacityRecoverySnapshot) (*VideoCapacityRecoveryView, error) {
	return s.recover(ctx, "stage", snapshot)
}
func (s *RedisVideoCapacityStore) ActivateRecovery(ctx context.Context, snapshot *VideoCapacityRecoverySnapshot) (*VideoCapacityRecoveryView, error) {
	return s.recover(ctx, "activate", snapshot)
}
func (s *RedisVideoCapacityStore) InspectRecovery(ctx context.Context, snapshot *VideoCapacityRecoverySnapshot) (*VideoCapacityRecoveryView, error) {
	return s.recover(ctx, "inspect", snapshot)
}

func (s *RedisVideoCapacityStore) recover(ctx context.Context, action string, snapshot *VideoCapacityRecoverySnapshot) (*VideoCapacityRecoveryView, error) {
	if s == nil || s.client == nil || ctx == nil || snapshot == nil || snapshot.epoch != s.epoch || snapshot.policy != s.policy || snapshot.raw == "" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(snapshot.digest) {
		return nil, ErrVideoCapacityUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	values, err := s.client.Eval(bounded, videoCapacityRecoveryLua, []string{videoCapacityStateKey}, action, s.epoch, s.policy, snapshot.raw).Slice()
	if err != nil || len(values) < 1 {
		return nil, ErrVideoCapacityUnavailable
	}
	code, ok := values[0].(int64)
	if !ok {
		return nil, ErrVideoCapacityUnavailable
	}
	if code == 2 {
		return nil, ErrVideoCapacityConflict
	}
	if code != 1 || len(values) != 3 {
		return nil, ErrVideoCapacityUnavailable
	}
	status, statusOK := values[1].(string)
	count, countOK := values[2].(int64)
	if !statusOK || !countOK || (status != "rebuilding" && status != "ready") || count < 0 || count > 102 {
		return nil, ErrVideoCapacityUnavailable
	}
	return &VideoCapacityRecoveryView{Status: status, Count: int(count)}, nil
}

//go:embed video_redis_capacity_recovery.lua
var videoCapacityRecoveryLua string
