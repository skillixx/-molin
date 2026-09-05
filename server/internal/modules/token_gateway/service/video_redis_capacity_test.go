package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	video "molin/server/internal/modules/token_gateway/video"
)

func openVideoG7CapacityRedis(t *testing.T) (*redis.Client, string) {
	t.Helper()
	if os.Getenv("MOLIN_VIDEO_G7_REDIS_ISOLATED") != "YES" {
		t.Skip("仅显式本机隔离Redis运行")
	}
	addr := os.Getenv("MOLIN_VIDEO_G7_REDIS_ADDR")
	host, _, err := net.SplitHostPort(addr)
	if err != nil || (host != "127.0.0.1" && !regexp.MustCompile(`^molin-vidg7-capacity-redis-[0-9a-f]{12}$`).MatchString(host)) {
		t.Fatal("拒绝非本轮隔离Redis地址")
	}
	expected := os.Getenv("MOLIN_VIDEO_G7_REDIS_RUN_ID")
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(expected) || os.Getenv("MOLIN_VIDEO_G7_REDIS_PASSWORD") == "" {
		t.Fatal("必须绑定本轮实际Redis身份及合成密码")
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("MOLIN_VIDEO_G7_REDIS_PASSWORD"), MaxRetries: -1, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	info, err := client.Info(context.Background(), "server").Result()
	if err != nil {
		t.Fatal("本轮Redis不可用")
	}
	match := regexp.MustCompile(`(?m)^run_id:([0-9a-f]{40})\r?$`).FindStringSubmatch(info)
	if len(match) != 2 || match[1] != expected {
		t.Fatal("Redis身份与runner创建证据不符")
	}
	return client, expected
}

// 100个真实Redis请求竞争同一主体；不是2/4/8个Go进程的阶段验收替身。
func TestVideoG7RedisCapacityConcurrent(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	seedVideoG7CapacityState(t, client, runID, policy)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	var won, limited, unexpected atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < 100; index++ {
		attempt := videoG7CapacityAttempt(t, 1000+index, 7, []string{"text_to_video", "image_to_video"}[index%2])
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.ReserveQueued(context.Background(), attempt)
			switch {
			case err == nil:
				won.Add(1)
			case errors.Is(err, ErrVideoCapacityFull):
				limited.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if won.Load() != 2 || limited.Load() != 98 || unexpected.Load() != 0 {
		t.Fatalf("必须原子裁决2个赢家和98个容量拒绝: won=%d limited=%d other=%d", won.Load(), limited.Load(), unexpected.Load())
	}
}

func TestVideoG7RedisCapacityMalformed(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, kind := range []string{"json", "oversized", "unknown_field", "count", "count_overflow", "epoch", "policy", "run_id", "record_phase", "missing_nonce", "wrong_identity", "duplicate_request", "wrong_type", "key_ttl"} {
		t.Run(kind, func(t *testing.T) {
			seedVideoG7CapacityState(t, client, runID, policy)
			first := videoG7CapacityAttempt(t, 1, 1, "text_to_video")
			if _, err := store.ReserveQueued(ctx, first); err != nil {
				t.Fatal(err)
			}
			raw, err := client.Get(ctx, videoCapacityStateKey).Result()
			if err != nil {
				t.Fatal("快照读取失败")
			}
			var state map[string]any
			if err := json.Unmarshal([]byte(raw), &state); err != nil {
				t.Fatal(err)
			}
			row := state["records"].(map[string]any)[first.task].(map[string]any)
			switch kind {
			case "json":
				raw = "{"
			case "oversized":
				raw = strings.Repeat(" ", 131073)
			case "unknown_field":
				state["unexpected"] = true
			case "count":
				state["count"] = 0
			case "count_overflow":
				state["count"] = 103
			case "epoch":
				state["epoch"] = "2"
			case "policy":
				state["policy"] = strings.Repeat("0", 64)
			case "run_id":
				state["run_id"] = strings.Repeat("0", 40)
			case "record_phase":
				row["phase"] = "released_without_proof"
			case "missing_nonce":
				delete(row, "attempt")
			case "wrong_identity":
				row["identity"] = strings.ReplaceAll(row["identity"].(string), "fake-native-async", "other-provider")
			case "duplicate_request":
				var copiedIdentity map[string]any
				if err := json.Unmarshal([]byte(row["identity"].(string)), &copiedIdentity); err != nil {
					t.Fatal(err)
				}
				copiedIdentity["task"] = "vid_duplicate_state"
				body, err := json.Marshal(copiedIdentity)
				if err != nil {
					t.Fatal(err)
				}
				state["records"].(map[string]any)["vid_duplicate_state"] = map[string]any{"identity": string(body), "attempt": row["attempt"], "phase": row["phase"], "expires_ms": row["expires_ms"]}
				state["count"] = 2
			}
			if kind != "json" && kind != "oversized" {
				body, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				raw = string(body)
			}
			if err := client.Set(ctx, videoCapacityStateKey, raw, 0).Err(); err != nil {
				t.Fatal("快照故障注入失败")
			}
			if kind == "wrong_type" {
				if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
					t.Fatal(err)
				}
				if err := client.RPush(ctx, videoCapacityStateKey, "synthetic-invalid").Err(); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "key_ttl" {
				if err := client.PExpire(ctx, videoCapacityStateKey, time.Minute).Err(); err != nil {
					t.Fatal(err)
				}
			}
			before, err := client.Dump(ctx, videoCapacityStateKey).Result()
			if err != nil {
				t.Fatal("故障快照读取失败")
			}
			if _, err := store.ReserveQueued(ctx, videoG7CapacityAttempt(t, 2, 2, "image_to_video")); !errors.Is(err, ErrVideoCapacityUnavailable) {
				t.Fatalf("损坏/未恢复状态不得视为空闲: %v", err)
			}
			after, err := client.Dump(ctx, videoCapacityStateKey).Result()
			if err != nil || before != after {
				t.Fatal("任何校验失败不得部分覆盖Redis状态")
			}
		})
	}
}

func TestVideoG7RedisCapacityPromoting(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	seedVideoG7CapacityState(t, client, runID, policy)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, second := videoG7CapacityAttempt(t, 1, 1, "text_to_video"), videoG7CapacityAttempt(t, 2, 1, "image_to_video")
	for _, attempt := range []*VideoCapacityAttempt{first, second} {
		if _, err := store.ReserveQueued(ctx, attempt); err != nil {
			t.Fatal(err)
		}
	}
	view, err := store.PrepareRunning(ctx, first)
	if err != nil || view.Phase != "promoting" {
		t.Fatalf("取得全部running预留后须保留queued保护: %v", err)
	}
	before := videoG7CapacityRaw(t, client)
	if replay, err := store.PrepareRunning(ctx, first); err != nil || !replay.ExpiresAt.Equal(view.ExpiresAt) {
		t.Fatalf("相同准备重放不能延长租期: %v", err)
	}
	if _, err := store.PrepareRunning(ctx, second); !errors.Is(err, ErrVideoCapacityFull) {
		t.Fatalf("同用户第二个running必须拒绝: %v", err)
	}
	if _, err := store.ReserveQueued(ctx, videoG7CapacityAttempt(t, 3, 1, "text_to_video")); !errors.Is(err, ErrVideoCapacityFull) {
		t.Fatalf("promoting在MySQL确认前仍计queued: %v", err)
	}
	after := videoG7CapacityRaw(t, client)
	if before != after {
		t.Fatal("重放与失败迁移都必须零修改")
	}
	if queued, err := store.Read(ctx, second); err != nil || queued.Phase != "queued" {
		t.Fatalf("输家不得丢失排队预留: %v", err)
	}
	third := videoG7CapacityAttempt(t, 4, 2, "image_to_video")
	if _, err := store.ReserveQueued(ctx, third); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareRunning(ctx, third); err != nil {
		t.Fatal(err)
	}
	key := uint64(3)
	fourth, err := NewVideoCapacityAttempt(VideoCapacityIdentity{TaskID: "vid_capacity_5", RequestID: "req_capacity_5", UserID: 3, ProjectID: 3, APIKeyID: &key, Model: "molin/other-model", Provider: "fake-native-async", Operation: "text_to_video"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveQueued(ctx, fourth); err != nil {
		t.Fatal(err)
	}
	_, err = store.PrepareRunning(ctx, fourth)
	var limited *VideoCapacityLimitError
	if !errors.As(err, &limited) || limited.Scope != "provider" {
		t.Fatalf("不同模型/operation共用Provider=2: %v", err)
	}
	// 真实等到30秒失效，不能通过快照改时钟冒充过期；过期债务仍占满Provider容量。
	time.Sleep(20 * time.Millisecond)
	renewed, err := store.Renew(ctx, first)
	if err != nil || !renewed.ExpiresAt.After(view.ExpiresAt) {
		t.Fatalf("合法持有者必须能续期: %v", err)
	}
	time.Sleep(time.Until(renewed.ExpiresAt) + 50*time.Millisecond)
	expired, err := store.Read(ctx, first)
	if err != nil || !expired.Expired || expired.Phase != "promoting" {
		t.Fatalf("允许只读观察过期占用: %v", err)
	}
	if _, err := store.Renew(ctx, first); !errors.Is(err, ErrVideoCapacityLeaseLost) {
		t.Fatalf("过期持有者不得续期: %v", err)
	}
	if _, err := store.PrepareRunning(ctx, fourth); !errors.Is(err, ErrVideoCapacityLeaseLost) {
		t.Fatalf("其他排队许可也已过期，不能恢复执行权: %v", err)
	}
	freshKey := uint64(4)
	fresh, err := NewVideoCapacityAttempt(VideoCapacityIdentity{TaskID: "vid_capacity_6", RequestID: "req_capacity_6", UserID: 4, ProjectID: 4, APIKeyID: &freshKey, Model: "molin/after-expiry", Provider: "fake-native-async", Operation: "image_to_video"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveQueued(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	_, err = store.PrepareRunning(ctx, fresh)
	if !errors.As(err, &limited) || limited.Scope != "provider" {
		t.Fatalf("过期不是Provider结束证据: %v", err)
	}
}

func TestVideoG7RedisCapacityConfirmRelease(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	seedVideoG7CapacityState(t, client, runID, policy)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	attempt := videoG7CapacityAttempt(t, 41, 41, "image_to_video")
	second := videoG7CapacityAttempt(t, 42, 41, "text_to_video")
	for _, queued := range []*VideoCapacityAttempt{attempt, second} {
		if _, err := store.ReserveQueued(ctx, queued); err != nil {
			t.Fatal(err)
		}
	}
	promoting, err := store.PrepareRunning(ctx, attempt)
	if err != nil || promoting.Phase != "promoting" {
		t.Fatalf("确认前必须保留双重容量: %+v err=%v", promoting, err)
	}
	third := videoG7CapacityAttempt(t, 43, 41, "image_to_video")
	if _, err := store.ReserveQueued(ctx, third); !errors.Is(err, ErrVideoCapacityFull) {
		t.Fatalf("promoting确认前仍占queued，同用户第三个任务必须拒绝: %v", err)
	}
	// EVAL实际完成后丢回执，原尝试只能查询并重放，不能另造容量记录。
	confirmLost := &videoCapacityLostReplyHook{}
	client.AddHook(confirmLost)
	if _, err := store.confirmRunning(ctx, attempt); !errors.Is(err, ErrVideoCapacityUnavailable) || confirmLost.evals.Load() != 1 {
		t.Fatalf("running确认回执未知必须上抛: evals=%d err=%v", confirmLost.evals.Load(), err)
	}
	running, err := store.Read(ctx, attempt)
	if err != nil || running.Phase != "running" || running.ExpiresAt.Before(promoting.ExpiresAt) {
		t.Fatalf("查询必须查明实际running: %+v err=%v", running, err)
	}
	if _, err := store.ReserveQueued(ctx, third); err != nil {
		t.Fatalf("confirm移除promoting的queued计数后必须恢复同用户名额: %v", err)
	}
	beforeReplay := videoG7CapacityRaw(t, client)
	if replay, err := store.confirmRunning(ctx, attempt); err != nil || replay.Phase != "running" || !replay.ExpiresAt.Equal(running.ExpiresAt) || beforeReplay != videoG7CapacityRaw(t, client) {
		t.Fatalf("running确认重放必须零写: %+v err=%v", replay, err)
	}
	wrong := videoG7CapacityAttempt(t, 41, 41, "image_to_video")
	if _, err := store.releaseCapacity(ctx, wrong); !errors.Is(err, ErrVideoCapacityConflict) || beforeReplay != videoG7CapacityRaw(t, client) {
		t.Fatalf("不同nonce不得释放当前容量: %v", err)
	}
	releaseLost := &videoCapacityLostReplyHook{}
	client.AddHook(releaseLost)
	if _, err := store.releaseCapacity(ctx, attempt); !errors.Is(err, ErrVideoCapacityUnavailable) || releaseLost.evals.Load() != 1 {
		t.Fatalf("释放回执未知必须上抛: evals=%d err=%v", releaseLost.evals.Load(), err)
	}
	releasedRaw := videoG7CapacityRaw(t, client)
	if _, err := store.Read(ctx, attempt); !errors.Is(err, ErrVideoCapacityLeaseLost) {
		t.Fatalf("实际释放后不能继续读取旧许可: %v", err)
	}
	if replay, err := store.releaseCapacity(ctx, attempt); err != nil || replay.Phase != "released" || releasedRaw != videoG7CapacityRaw(t, client) {
		t.Fatalf("释放重放必须只读成功: %+v err=%v", replay, err)
	}
	for _, queued := range []*VideoCapacityAttempt{second, third} {
		if _, err := store.releaseCapacity(ctx, queued); err != nil {
			t.Fatalf("测试清理排队容量失败: %v", err)
		}
	}
	finalRaw := videoG7CapacityRaw(t, client)
	var state struct {
		Count   int                        `json:"count"`
		Records map[string]json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal([]byte(finalRaw), &state); err != nil || state.Count != 0 || len(state.Records) != 0 {
		t.Fatalf("释放必须原子移除唯一记录: count=%d records=%d err=%v", state.Count, len(state.Records), err)
	}
}

func TestVideoG7RedisCapacityExpiredRelease(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	for index, phase := range []string{"promoting", "running"} {
		t.Run(phase, func(t *testing.T) {
			seedVideoG7CapacityState(t, client, runID, policy)
			attempt := videoG7CapacityAttempt(t, 50+index, 50+uint64(index), "text_to_video")
			if _, err := store.ReserveQueued(context.Background(), attempt); err != nil {
				t.Fatal(err)
			}
			if _, err := store.PrepareRunning(context.Background(), attempt); err != nil {
				t.Fatal(err)
			}
			if phase == "running" {
				if _, err := store.confirmRunning(context.Background(), attempt); err != nil {
					t.Fatal(err)
				}
			}
			var state map[string]any
			if err := json.Unmarshal([]byte(videoG7CapacityRaw(t, client)), &state); err != nil {
				t.Fatal(err)
			}
			records := state["records"].(map[string]any)
			record := records[attempt.task].(map[string]any)
			record["expires_ms"] = float64(1)
			raw, err := json.Marshal(state)
			if err != nil || client.Set(context.Background(), videoCapacityStateKey, raw, 0).Err() != nil {
				t.Fatal("注入过期容量债务失败")
			}
			before := videoG7CapacityRaw(t, client)
			if _, err := store.confirmRunning(context.Background(), attempt); !errors.Is(err, ErrVideoCapacityLeaseLost) || before != videoG7CapacityRaw(t, client) {
				t.Fatalf("过期%s不得确认或改写: %v", phase, err)
			}
			wrong := videoG7CapacityAttempt(t, 50+index, 50+uint64(index), "text_to_video")
			if _, err := store.releaseCapacity(context.Background(), wrong); !errors.Is(err, ErrVideoCapacityConflict) || before != videoG7CapacityRaw(t, client) {
				t.Fatalf("过期债务仍须拒绝不同nonce释放: %v", err)
			}
			if released, err := store.releaseCapacity(context.Background(), attempt); err != nil || released.Phase != "released" {
				t.Fatalf("上层已证明终态后必须允许exact清理过期%s: %+v err=%v", phase, released, err)
			}
			after := videoG7CapacityRaw(t, client)
			if absent, err := store.releaseCapacity(context.Background(), wrong); err != nil || absent.Phase != "released" || after != videoG7CapacityRaw(t, client) {
				t.Fatalf("记录缺失只表达当前无占用且保持零写，不证明调用者授权: %+v err=%v", absent, err)
			}
		})
	}
}

func TestVideoG7RedisCapacityAbortPromotion(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	seedVideoG7CapacityState(t, client, runID, policy)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	attempt := videoG7CapacityAttempt(t, 60, 60, "text_to_video")
	if _, err := store.ReserveQueued(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	promoting, err := store.PrepareRunning(ctx, attempt)
	if err != nil || promoting.Phase != "promoting" {
		t.Fatalf("必须先取得running预留: %+v err=%v", promoting, err)
	}
	queued, err := store.abortPromotion(ctx, attempt)
	if err != nil || queued.Phase != "queued" || !queued.ExpiresAt.Equal(promoting.ExpiresAt) {
		t.Fatalf("明确MySQL失败只能退回原queued且不续期: %+v err=%v", queued, err)
	}
	before := videoG7CapacityRaw(t, client)
	if replay, err := store.abortPromotion(ctx, attempt); err != nil || replay.Phase != "queued" || before != videoG7CapacityRaw(t, client) {
		t.Fatalf("abort重放必须零写: %+v err=%v", replay, err)
	}
	if _, err := store.confirmRunning(ctx, attempt); !errors.Is(err, ErrVideoCapacityLeaseLost) || before != videoG7CapacityRaw(t, client) {
		t.Fatalf("queued不能跳过prepare直接confirm: %v", err)
	}
	wrong := videoG7CapacityAttempt(t, 60, 60, "text_to_video")
	if _, err := store.abortPromotion(ctx, wrong); !errors.Is(err, ErrVideoCapacityConflict) || before != videoG7CapacityRaw(t, client) {
		t.Fatalf("不同nonce不能撤销原running预留: %v", err)
	}
}

// 这是存储组件的初始数据夹具，不是MySQL恢复器，也不将手工快照当成运行时恢复证据。
func seedVideoG7CapacityState(t *testing.T, client *redis.Client, runID string, policy *video.VideoCapacityPolicy) {
	t.Helper()
	hash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"schema": 1, "epoch": "1", "policy": hash, "run_id": runID, "status": "ready", "count": 0, "records": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(context.Background(), videoCapacityStateKey, body, 0).Err(); err != nil {
		t.Fatal("无法写入本轮Redis夹具")
	}
}

func videoG7CapacityAttempt(t *testing.T, n int, user uint64, operation string) *VideoCapacityAttempt {
	t.Helper()
	key := user
	attempt, err := NewVideoCapacityAttempt(VideoCapacityIdentity{TaskID: fmt.Sprintf("vid_capacity_%d", n), RequestID: fmt.Sprintf("req_capacity_%d", n), UserID: user, ProjectID: user, APIKeyID: &key, Model: "molin/video-fixture", Provider: "fake-native-async", Operation: operation})
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func TestVideoG7RedisCapacityQueued(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := videoG7CapacityAttempt(t, 1, 1, "text_to_video")
	// 仅在已验证身份的本轮临时Redis模拟整键丢失，不能假定其他用例没有留下容量快照。
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal("空库故障注入失败")
	}
	if _, err := store.ReserveQueued(ctx, first); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatalf("空库不能自动开放: %v", err)
	}
	seedVideoG7CapacityState(t, client, runID, policy)
	view, err := store.ReserveQueued(ctx, first)
	if err != nil || view == nil || view.Phase != "queued" || view.Expired {
		t.Fatalf("真实Redis首次合法预留应成功: %v", err)
	}
	before, err := client.Get(ctx, videoCapacityStateKey).Result()
	if err != nil {
		t.Fatal("快照读取失败")
	}
	replay, err := store.ReserveQueued(ctx, first)
	if err != nil || replay == nil || !replay.ExpiresAt.Equal(view.ExpiresAt) {
		t.Fatalf("同尝试重放不得续期或重复预留: %v", err)
	}
	after := videoG7CapacityRaw(t, client)
	if before != after {
		t.Fatal("只读重放不得改写快照")
	}
	if _, err := store.ReserveQueued(ctx, videoG7CapacityAttempt(t, 1, 2, "image_to_video")); !errors.Is(err, ErrVideoCapacityConflict) {
		t.Fatalf("同Task异身份不得覆盖: %v", err)
	}
	if _, err := store.ReserveQueued(ctx, videoG7CapacityAttempt(t, 2, 1, "image_to_video")); err != nil {
		t.Fatalf("第二个混合operation预留应成功: %v", err)
	}
	before = videoG7CapacityRaw(t, client)
	_, err = store.ReserveQueued(ctx, videoG7CapacityAttempt(t, 3, 1, "text_to_video"))
	var limited *VideoCapacityLimitError
	if !errors.As(err, &limited) || limited.Scope != "user" || limited.RetryAfter != time.Second {
		t.Fatalf("两个operation共用用户queued=2: %v", err)
	}
	after = videoG7CapacityRaw(t, client)
	if before != after {
		t.Fatal("容量拒绝不得部分写入")
	}
	current, err := store.Read(ctx, first)
	if err != nil || current.Phase != "queued" {
		t.Fatalf("原任务仍保持原预留: %v", err)
	}
}

// 原子性对照必须实际读到状态，不能把两个读取错误产生的空串当作零修改。
func videoG7CapacityRaw(t *testing.T, client *redis.Client) string {
	t.Helper()
	raw, err := client.Get(context.Background(), videoCapacityStateKey).Result()
	if err != nil {
		t.Fatal("容量快照读取失败")
	}
	return raw
}

func TestVideoG7RedisCapacityRequestBinding(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	seedVideoG7CapacityState(t, client, runID, policy)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := videoG7CapacityAttempt(t, 1, 1, "text_to_video")
	if _, err := store.ReserveQueued(ctx, first); err != nil {
		t.Fatal(err)
	}
	before := videoG7CapacityRaw(t, client)
	key := uint64(1)
	duplicate, err := NewVideoCapacityAttempt(VideoCapacityIdentity{TaskID: "vid_duplicate_request", RequestID: "req_capacity_1", UserID: 1, ProjectID: 1, APIKeyID: &key, Model: "molin/video-fixture", Provider: "fake-native-async", Operation: "text_to_video"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveQueued(ctx, duplicate); !errors.Is(err, ErrVideoCapacityConflict) {
		t.Fatalf("原Request不能再绑定第二个Task预留: %v", err)
	}
	if before != videoG7CapacityRaw(t, client) {
		t.Fatal("错绑Request拒绝必须零写入")
	}
}

func TestVideoG7RedisCapacityScopes(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	for _, scope := range []string{"project", "api_key", "model", "global"} {
		t.Run(scope, func(t *testing.T) {
			limits := video.DefaultVideoCapacityLimits()
			switch scope {
			case "project":
				limits.Queued.Project = 1
			case "api_key":
				limits.Queued.APIKey = 1
			case "model":
				limits.Queued.Model = 1
			case "global":
				limits.Queued.Global = 1
			}
			policy, err := video.NewVideoCapacityPolicy(limits)
			if err != nil {
				t.Fatal(err)
			}
			seedVideoG7CapacityState(t, client, runID, policy)
			store, err := NewRedisVideoCapacityStore(client, 1, policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReserveQueued(ctx, videoG7CapacityAttempt(t, 1, 1, "text_to_video")); err != nil {
				t.Fatal(err)
			}
			user := uint64(1)
			if scope == "model" || scope == "global" {
				user = 2
			}
			before := videoG7CapacityRaw(t, client)
			_, err = store.ReserveQueued(ctx, videoG7CapacityAttempt(t, 2, user, "image_to_video"))
			var limited *VideoCapacityLimitError
			if !errors.As(err, &limited) || limited.Scope != scope {
				t.Fatalf("收紧目标轴必须独立命中: %v", err)
			}
			if before != videoG7CapacityRaw(t, client) {
				t.Fatal("任一轴拒绝都不能部分写入")
			}
		})
	}
	t.Run("uint64精度与JWT主体", func(t *testing.T) {
		limits := video.DefaultVideoCapacityLimits()
		limits.Queued.APIKey = 1
		policy, err := video.NewVideoCapacityPolicy(limits)
		if err != nil {
			t.Fatal(err)
		}
		seedVideoG7CapacityState(t, client, runID, policy)
		store, err := NewRedisVideoCapacityStore(client, 1, policy)
		if err != nil {
			t.Fatal(err)
		}
		makeJWT := func(n int, user uint64) *VideoCapacityAttempt {
			a, err := NewVideoCapacityAttempt(VideoCapacityIdentity{TaskID: fmt.Sprintf("vid_jwt_%d", n), RequestID: fmt.Sprintf("req_jwt_%d", n), UserID: user, ProjectID: user, Model: "molin/video-fixture", Provider: "fake-native-async", Operation: "text_to_video"})
			if err != nil {
				t.Fatal(err)
			}
			return a
		}
		largest := ^uint64(0)
		for index, user := range []uint64{largest - 1, largest} {
			if _, err := store.ReserveQueued(ctx, makeJWT(index, user)); err != nil {
				t.Fatalf("相邻uint64不能合并，JWT不能全归入key0: %v", err)
			}
		}
		_, err = store.ReserveQueued(ctx, makeJWT(3, largest))
		var limited *VideoCapacityLimitError
		if !errors.As(err, &limited) || limited.Scope != "api_key" {
			t.Fatalf("同JWT主体跨请求仍共享稳定Key桶: %v", err)
		}
	})
}

type videoCapacityLostReplyHook struct {
	lost         atomic.Bool
	evals        atomic.Int64
	loseAt       int64
	failBeforeAt int64
}

func (h *videoCapacityLostReplyHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *videoCapacityLostReplyHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *videoCapacityLostReplyHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "eval" {
			current := h.evals.Add(1)
			if current == h.failBeforeAt && h.lost.CompareAndSwap(false, true) {
				return io.ErrUnexpectedEOF
			}
			err := next(ctx, cmd)
			target := h.loseAt
			if target == 0 && h.failBeforeAt == 0 {
				target = 1
			}
			if err == nil && target > 0 && current == target && h.lost.CompareAndSwap(false, true) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		return next(ctx, cmd)
	}
}

// 在真实EVAL完成后丢弃客户端结果；这是响应边界注入，不声称真实TCP断连或MySQL提交未知已验收。
func TestVideoG7RedisCapacityLostReply(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	seedVideoG7CapacityState(t, client, runID, policy)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	hook := &videoCapacityLostReplyHook{}
	client.AddHook(hook)
	ctx := context.Background()
	attempt := videoG7CapacityAttempt(t, 1, 1, "text_to_video")
	if _, err := store.ReserveQueued(ctx, attempt); !errors.Is(err, ErrVideoCapacityUnavailable) || hook.evals.Load() != 1 {
		t.Fatalf("响应未知必须上抛且禁自动重试: evals=%d err=%v", hook.evals.Load(), err)
	}
	before := videoG7CapacityRaw(t, client)
	if _, err := store.ReserveQueued(ctx, attempt); err != nil {
		t.Fatalf("原尝试应能恢复原预留: %v", err)
	}
	if hook.evals.Load() != 2 || before != videoG7CapacityRaw(t, client) {
		t.Fatal("明确重放不得增加占用或刷新期限")
	}
	if _, err := store.ReserveQueued(ctx, videoG7CapacityAttempt(t, 1, 1, "text_to_video")); !errors.Is(err, ErrVideoCapacityConflict) {
		t.Fatalf("新nonce不能夺取结果未知的旧预留: %v", err)
	}
	if _, err := store.ReserveQueued(ctx, videoG7CapacityAttempt(t, 2, 1, "image_to_video")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReserveQueued(ctx, videoG7CapacityAttempt(t, 3, 1, "text_to_video")); !errors.Is(err, ErrVideoCapacityFull) {
		t.Fatalf("未知响应仍实际占用一份容量: %v", err)
	}
}

func TestVideoG7RedisCapacityAttemptBoundary(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	seedVideoG7CapacityState(t, client, runID, policy)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	key := uint64(7)
	identity := VideoCapacityIdentity{TaskID: "vid_boundary_1", RequestID: "req_boundary_1", UserID: 7, ProjectID: 7, APIKeyID: &key, Model: "molin/video-fixture", Provider: "fake-native-async", Operation: "text_to_video"}
	attempt, err := NewVideoCapacityAttempt(identity)
	if err != nil {
		t.Fatal(err)
	}
	key = 8
	if strings.Contains(attempt.identity, `"key":"sk:8"`) {
		t.Fatal("输入指针变化不能替换已冻结Key主体")
	}
	for _, value := range []any{attempt, *attempt} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, rendered := range []string{string(body), fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(rendered, attempt.nonce) || strings.Contains(rendered, attempt.task) || strings.Contains(rendered, "sk:7") {
				t.Fatal("普通JSON/日志不得携带内部尝试能力或主体")
			}
		}
	}
	before := videoG7CapacityRaw(t, client)
	if _, err := store.ReserveQueued(nil, attempt); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatal("nil context应失败关闭")
	}
	if _, err := store.ReserveQueued(ctx, &VideoCapacityAttempt{}); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatal("零值能力应失败关闭")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := store.ReserveQueued(cancelled, attempt); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatal("已取消调用不得新增容量")
	}
	if before != videoG7CapacityRaw(t, client) {
		t.Fatal("入口拒绝必须保持整个Redis快照")
	}
	unsafeClient := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = unsafeClient.Close() })
	if _, err := NewRedisVideoCapacityStore(unsafeClient, 1, policy); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatal("自动重试或不遵守context的客户端不得接线")
	}
	// 最大epoch与主体一样只按字符串传输比较，不进入Lua浮点数。
	var state map[string]any
	if err := json.Unmarshal([]byte(before), &state); err != nil {
		t.Fatal(err)
	}
	state["epoch"] = "18446744073709551615"
	body, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, videoCapacityStateKey, body, 0).Err(); err != nil {
		t.Fatal(err)
	}
	last, err := NewRedisVideoCapacityStore(client, ^uint64(0), policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := last.ReserveQueued(ctx, attempt); err != nil {
		t.Fatalf("最后uint64代次仍能准确匹配: %v", err)
	}
	if _, err := store.Read(ctx, attempt); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatal("旧epoch不能读取为当前许可")
	}
}
