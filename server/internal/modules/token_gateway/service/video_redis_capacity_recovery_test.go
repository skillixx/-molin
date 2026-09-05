package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7RedisCapacityRecoveryStage(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	// 固定键只属于本轮隔离Redis；显式清空并确认，测试不依赖注册顺序或前例残留。
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	if count, err := client.Exists(ctx, videoCapacityStateKey).Result(); err != nil || count != 0 {
		t.Fatal("恢复Stage必须从已确认的空键开始")
	}
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateRunID(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateRunID(context.Background(), strings.Repeat("0", 40)); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatal("错误run_id不能绑定实际Redis")
	}
	queued := videoG7CapacityAttempt(t, 701, 71, "text_to_video")
	running := videoG7CapacityAttempt(t, 702, 72, "image_to_video")
	snapshot, err := newVideoCapacityRecoverySnapshot(1, policy, []VideoCapacityRecoveryRecord{{Attempt: queued, Phase: "queued"}, {Attempt: running, Phase: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{snapshot, *snapshot} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, rendered := range []string{string(body), fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(rendered, queued.nonce) || strings.Contains(rendered, queued.identity) || strings.Contains(rendered, running.nonce) {
				t.Fatal("恢复快照不得进入普通JSON或格式化日志")
			}
		}
	}
	view, err := store.StageRecovery(ctx, snapshot)
	if err != nil || view.Status != "rebuilding" || view.Count != 2 {
		t.Fatalf("空库应原子写入未开放快照: %+v err=%v", view, err)
	}
	if inspected, err := store.InspectRecovery(ctx, snapshot); err != nil || !reflect.DeepEqual(inspected, view) {
		t.Fatalf("stage只能用同快照只读确认: %+v err=%v", inspected, err)
	}
	if _, err := store.ValidateReadyState(ctx, runID); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatalf("rebuilding不能被非领导实例当作ready: %v", err)
	}
	if _, err := store.Read(ctx, queued); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatalf("stage不得开放读取或准入: %v", err)
	}
	staged := videoG7CapacityRaw(t, client)
	if replay, err := store.StageRecovery(ctx, snapshot); err != nil || !reflect.DeepEqual(replay, view) || staged != videoG7CapacityRaw(t, client) {
		t.Fatalf("同一stage重放必须只读: %+v err=%v", replay, err)
	}
	other := videoG7CapacityAttempt(t, 703, 73, "text_to_video")
	different, err := newVideoCapacityRecoverySnapshot(1, policy, []VideoCapacityRecoveryRecord{{Attempt: other, Phase: "queued"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StageRecovery(ctx, different); !errors.Is(err, ErrVideoCapacityConflict) || staged != videoG7CapacityRaw(t, client) {
		t.Fatalf("同epoch异快照必须零写冲突: %v", err)
	}
	activated, err := store.ActivateRecovery(ctx, snapshot)
	if err != nil || activated.Status != "ready" || activated.Count != 2 {
		t.Fatalf("只有原stage快照可激活: %+v err=%v", activated, err)
	}
	if inspected, err := store.InspectRecovery(ctx, snapshot); err != nil || !reflect.DeepEqual(inspected, activated) {
		t.Fatalf("activate结果只能用同快照只读确认: %+v err=%v", inspected, err)
	}
	if observed, err := ReadVideoRedisRunID(ctx, client); err != nil || observed != runID {
		t.Fatalf("必须读取实际Redis进程身份: observed=%s err=%v", observed, err)
	}
	if count, err := store.ValidateReadyState(ctx, runID); err != nil || count != 2 {
		t.Fatalf("非领导实例必须原子确认完整ready头: count=%d err=%v", count, err)
	}
	if _, err := store.ValidateReadyState(ctx, strings.Repeat("0", 40)); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatalf("错误run_id不能加入ready运行时: %v", err)
	}
	for attempt, phase := range map[*VideoCapacityAttempt]string{queued: "queued", running: "running"} {
		if current, err := store.Read(ctx, attempt); err != nil || current.Phase != phase || current.Expired {
			t.Fatalf("激活后必须恢复原阶段: %+v err=%v", current, err)
		}
	}
	ready := videoG7CapacityRaw(t, client)
	if replay, err := store.ActivateRecovery(ctx, snapshot); err != nil || !reflect.DeepEqual(replay, activated) || ready != videoG7CapacityRaw(t, client) {
		t.Fatalf("激活重放必须只读: %+v err=%v", replay, err)
	}
	if _, err := store.ActivateRecovery(ctx, different); !errors.Is(err, ErrVideoCapacityConflict) || ready != videoG7CapacityRaw(t, client) {
		t.Fatalf("异快照不能激活或覆盖ready: %v", err)
	}
	// 新epoch可覆盖完整旧状态进入rebuilding，但旧store不能再读取；这是恢复替换，不是释放业务债务。
	newStore, err := NewRedisVideoCapacityStore(client, 2, policy)
	if err != nil {
		t.Fatal(err)
	}
	next, err := newVideoCapacityRecoverySnapshot(2, policy, []VideoCapacityRecoveryRecord{{Attempt: queued, Phase: "running"}})
	if err != nil {
		t.Fatal(err)
	}
	if replaced, err := newStore.StageRecovery(ctx, next); err != nil || replaced.Status != "rebuilding" || replaced.Count != 1 {
		t.Fatalf("新恢复epoch须完整替换为未开放态: %+v err=%v", replaced, err)
	}
	if _, err := store.Read(ctx, queued); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatal("旧epoch不得读取新恢复状态")
	}
	if _, err := newStore.Read(ctx, queued); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatal("新epoch也须等待activate")
	}
}

func TestVideoG7RedisCapacityRecoveryFaults(t *testing.T) {
	client, runID := openVideoG7CapacityRedis(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	newStore, err := NewRedisVideoCapacityStore(client, 2, policy)
	if err != nil {
		t.Fatal(err)
	}
	attempt := videoG7CapacityAttempt(t, 711, 81, "text_to_video")
	snapshot, err := newVideoCapacityRecoverySnapshot(2, policy, []VideoCapacityRecoveryRecord{{Attempt: attempt, Phase: "queued"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// 模拟Redis进程重启但AOF仍保留旧快照：新epoch可替换完整旧状态，不需要先删唯一业务键。
	seedVideoG7CapacityState(t, client, strings.Repeat("0", 40), policy)
	if view, err := newStore.StageRecovery(ctx, snapshot); err != nil || view.Status != "rebuilding" {
		t.Fatalf("新epoch必须能替换旧run_id完整状态: %+v err=%v", view, err)
	}
	for _, kind := range []string{"staged_ttl", "ready_ttl", "future_expiry", "provider_overflow"} {
		t.Run(kind, func(t *testing.T) {
			seedVideoG7CapacityState(t, client, runID, policy)
			if kind == "staged_ttl" {
				if _, err := newStore.StageRecovery(ctx, snapshot); err != nil {
					t.Fatal(err)
				}
			}
			var state map[string]any
			raw := videoG7CapacityRaw(t, client)
			if err := json.Unmarshal([]byte(raw), &state); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "staged_ttl", "ready_ttl":
				if err := client.PExpire(ctx, videoCapacityStateKey, time.Minute).Err(); err != nil {
					t.Fatal(err)
				}
			case "future_expiry":
				rowAttempt := videoG7CapacityAttempt(t, 712, 82, "text_to_video")
				state["records"].(map[string]any)[rowAttempt.task] = map[string]any{"identity": rowAttempt.identity, "attempt": rowAttempt.nonce, "phase": "queued", "expires_ms": float64(time.Now().Add(time.Minute).UnixMilli())}
				state["count"] = float64(1)
			case "provider_overflow":
				rows := state["records"].(map[string]any)
				for i := 0; i < 3; i++ {
					a := videoG7CapacityAttempt(t, 720+i, uint64(90+i), "text_to_video")
					rows[a.task] = map[string]any{"identity": a.identity, "attempt": a.nonce, "phase": "running", "expires_ms": float64(time.Now().Add(20 * time.Second).UnixMilli())}
				}
				state["count"] = float64(3)
			}
			if kind == "future_expiry" || kind == "provider_overflow" {
				body, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				if err := client.Set(ctx, videoCapacityStateKey, body, 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			before := videoG7CapacityRaw(t, client)
			store3, err := NewRedisVideoCapacityStore(client, 3, policy)
			if err != nil {
				t.Fatal(err)
			}
			next, err := newVideoCapacityRecoverySnapshot(3, policy, []VideoCapacityRecoveryRecord{{Attempt: attempt, Phase: "queued"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store3.StageRecovery(ctx, next); !errors.Is(err, ErrVideoCapacityUnavailable) {
				t.Fatalf("损坏/TTL旧状态必须失败关闭: %v", err)
			}
			if before != videoG7CapacityRaw(t, client) {
				t.Fatal("恢复拒绝不得覆盖原状态")
			}
		})
	}
}

func TestVideoG7RedisCapacityRecoveryLostReply(t *testing.T) {
	client, _ := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	attempt := videoG7CapacityAttempt(t, 731, 101, "image_to_video")
	snapshot, err := newVideoCapacityRecoverySnapshot(1, policy, []VideoCapacityRecoveryRecord{{Attempt: attempt, Phase: "queued"}})
	if err != nil {
		t.Fatal(err)
	}
	hook := &videoCapacityLostReplyHook{}
	client.AddHook(hook)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StageRecovery(ctx, snapshot); !errors.Is(err, ErrVideoCapacityUnavailable) || hook.evals.Load() != 1 {
		t.Fatalf("EVAL成功后丢返回必须报告未知且不重试: evals=%d err=%v", hook.evals.Load(), err)
	}
	client2, _ := openVideoG7CapacityRedis(t)
	clean, err := NewRedisVideoCapacityStore(client2, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	if view, err := clean.StageRecovery(ctx, snapshot); err != nil || view.Status != "rebuilding" {
		t.Fatalf("同快照须能核对已生效stage: %+v err=%v", view, err)
	}
	different := videoG7CapacityAttempt(t, 732, 102, "text_to_video")
	other, err := newVideoCapacityRecoverySnapshot(1, policy, []VideoCapacityRecoveryRecord{{Attempt: different, Phase: "queued"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clean.StageRecovery(ctx, other); !errors.Is(err, ErrVideoCapacityConflict) {
		t.Fatalf("未知后不得换nonce/身份覆盖: %v", err)
	}
}

func TestVideoG7RedisCapacityRecoveryConcurrent(t *testing.T) {
	client, _ := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	a := videoG7CapacityAttempt(t, 741, 111, "text_to_video")
	b := videoG7CapacityAttempt(t, 742, 112, "image_to_video")
	one, _ := newVideoCapacityRecoverySnapshot(1, policy, []VideoCapacityRecoveryRecord{{Attempt: a, Phase: "queued"}})
	two, _ := newVideoCapacityRecoverySnapshot(1, policy, []VideoCapacityRecoveryRecord{{Attempt: b, Phase: "queued"}})
	var success, conflict int
	var mu sync.Mutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			candidate := one
			if i%2 == 1 {
				candidate = two
			}
			_, err := store.StageRecovery(ctx, candidate)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				success++
			} else if errors.Is(err, ErrVideoCapacityConflict) {
				conflict++
			} else {
				t.Errorf("并发stage意外错误: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if success != 50 || conflict != 50 {
		t.Fatalf("只能有一个快照族胜出并幂等重放: success=%d conflict=%d", success, conflict)
	}
}
