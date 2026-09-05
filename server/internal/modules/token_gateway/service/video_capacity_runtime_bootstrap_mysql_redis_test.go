package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestVideoG7CapacityRuntimeBootstrapMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, _ := openVideoG7CapacityRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	key := mustVideoCapacityNonceKey(t)
	results := make([]*VideoCapacityRuntime, 8)
	errorsSeen := make([]error, 8)
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results[index], errorsSeen[index] = PrepareVideoCapacityRuntime(ctx, db, client, key, "runtime-worker-"+string(rune('a'+index)))
		}(index)
	}
	wait.Wait()
	for index, result := range results {
		if errorsSeen[index] != nil || result == nil || result.Summary.Epoch != 1 || result.Summary.Total != 0 {
			t.Fatalf("并发实例必须加入同一ready代次: index=%d result=%+v err=%v", index, result, errorsSeen[index])
		}
	}
	state, err := results[0].Recovery.Current(ctx)
	if err != nil || state.State != "ready" || state.Epoch != 1 || state.SnapshotCount != 0 {
		t.Fatalf("领导实例必须发布唯一ready: state=%+v err=%v", state, err)
	}
	version := state.Version
	replayed, err := PrepareVideoCapacityRuntime(ctx, db, client, key, "runtime-replay")
	if err != nil || replayed.Summary.Epoch != 1 {
		t.Fatalf("ready实例加入必须成功: runtime=%+v err=%v", replayed, err)
	}
	afterReplay, _ := replayed.Recovery.Current(ctx)
	if afterReplay.Version != version {
		t.Fatal("ready加入必须只读，不能每个实例都启动新恢复")
	}
	// 模拟Redis进程状态丢失：MySQL ready不能冒充运行可用，必须形成下一完整epoch。
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	recovered, err := PrepareVideoCapacityRuntime(ctx, db, client, key, "runtime-recovery")
	if err != nil || recovered.Summary.Epoch != 2 || recovered.Summary.Total != 0 {
		t.Fatalf("Redis丢失必须完整重建到新代次: runtime=%+v err=%v", recovered, err)
	}
	if count, err := recovered.Store.ValidateReadyState(ctx, state.RedisRunID); err != nil || count != 0 {
		t.Fatalf("重建后ready头必须原子有效: count=%d err=%v", count, err)
	}
}
