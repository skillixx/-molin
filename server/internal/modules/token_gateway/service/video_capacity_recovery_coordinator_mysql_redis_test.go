package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7CapacityRecoveryCoordinatorMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	reserved := newVideoG5ReservationFixture(t, db, "10")
	if _, err := reserved.service.ReserveAndCreate(ctx, reserved.command); err != nil {
		t.Fatal(err)
	}
	running := prepareVideoG7Plan(t, db, model.AIVideoOperationTextToVideo)
	if err := running.ledger.RecordSubmissionPlan(running.owned, running.claim.TaskID, running.claim.Version, "fake-native-async"); err != nil {
		t.Fatal(err)
	}
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	proof, err := recovery.Begin(ctx, 0, "coordinator-first", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	stageHook := &videoCapacityLostReplyHook{}
	client.AddHook(stageHook)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	nonceKey := mustVideoCapacityNonceKey(t)
	builder := NewVideoCapacitySnapshotBuilder(db, recovery, nonceKey)
	coordinator := NewVideoCapacityRecoveryCoordinator(builder, recovery, store)
	prepared, err := coordinator.Prepare(ctx, proof, policy)
	if err != nil || prepared == nil || stageHook.evals.Load() != 2 || prepared.Summary().Total != 2 || prepared.Summary().Queued != 1 || prepared.Summary().Running != 1 {
		t.Fatalf("Stage丢响应后须Inspect确认同快照: evals=%d summary=%+v err=%v", stageHook.evals.Load(), prepared.Summary(), err)
	}
	for _, value := range []any{prepared, *prepared} {
		body, err := json.Marshal(value)
		if err != nil || string(body) != `{"redacted":true}` || strings.Contains(fmt.Sprintf("%#v", value), reserved.command.TaskID) {
			t.Fatal("prepared对象不能泄露任务或nonce")
		}
	}
	if view, err := store.InspectRecovery(ctx, prepared.snapshot); err != nil || view.Status != "rebuilding" {
		t.Fatalf("MySQL ready前Redis必须保持rebuilding: %+v err=%v", view, err)
	}
	if _, err := store.Read(ctx, newVideoCapacityAttemptMust(t, nonceKey, proof.Epoch(), reserved)); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatal("rebuilding不能授权普通容量读取")
	}
	summary, err := coordinator.Complete(ctx, proof, prepared)
	if err != nil || !reflect.DeepEqual(summary, prepared.Summary()) {
		t.Fatalf("首次协调完成失败: %+v err=%v", summary, err)
	}
	if err := recovery.ValidateReady(ctx, 1, hash, runID, prepared.snapshot.Digest(), 2); err != nil {
		t.Fatal(err)
	}
	beforeDB, beforeRedis := captureVideoCapacityDB(t, db), videoG7CapacityRaw(t, client)
	if replay, err := coordinator.Complete(ctx, proof, prepared); err != nil || !reflect.DeepEqual(replay, summary) || !reflect.DeepEqual(beforeDB, captureVideoCapacityDB(t, db)) || beforeRedis != videoG7CapacityRaw(t, client) {
		t.Fatalf("Complete重放必须双侧零写: %v", err)
	}
	second, err := recovery.Begin(ctx, 1, "coordinator-second", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	cleanClient, _ := openVideoG7CapacityRedis(t)
	cleanStore, err := NewRedisVideoCapacityStore(cleanClient, 2, policy)
	if err != nil {
		t.Fatal(err)
	}
	cleanCoordinator := NewVideoCapacityRecoveryCoordinator(builder, recovery, cleanStore)
	// 旧快照绝不能借用新代次证明发布；拒绝必须发生在MySQL和Redis任何写入之前。
	mismatchDB, mismatchRedis := captureVideoCapacityDB(t, db), videoG7CapacityRaw(t, cleanClient)
	if _, err := cleanCoordinator.Complete(ctx, second, prepared); err == nil {
		t.Fatal("旧prepared配新proof必须失败关闭")
	}
	if !reflect.DeepEqual(mismatchDB, captureVideoCapacityDB(t, db)) || mismatchRedis != videoG7CapacityRaw(t, cleanClient) {
		t.Fatal("代次错配拒绝不得改写MySQL或Redis")
	}
	prepared2, err := cleanCoordinator.Prepare(ctx, second, policy)
	if err != nil {
		t.Fatal(err)
	}
	pool := &videoCapacityUnknownCommitPool{&videoBudgetCommitPool{ConnPool: db.ConnPool}}
	faultDB, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: db.Logger})
	if err != nil {
		t.Fatal(err)
	}
	faultRecovery := repository.NewVideoCapacityRecoveryRepository(faultDB)
	lossClient, _ := openVideoG7CapacityRedis(t)
	// Complete先Inspect再Activate，因此只丢第二次EVAL的激活回执。
	activateHook := &videoCapacityLostReplyHook{loseAt: 2}
	lossClient.AddHook(activateHook)
	lossStore, err := NewRedisVideoCapacityStore(lossClient, 2, policy)
	if err != nil {
		t.Fatal(err)
	}
	faultCoordinator := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(faultDB, faultRecovery, nonceKey), faultRecovery, lossStore)
	result, err := faultCoordinator.Complete(ctx, second, prepared2)
	if err != nil || !pool.lost.Load() || activateHook.evals.Load() != 3 || !reflect.DeepEqual(result, prepared2.Summary()) {
		t.Fatalf("双侧返回未知必须只读查明: dbLost=%v redisEvals=%d result=%+v err=%v", pool.lost.Load(), activateHook.evals.Load(), result, err)
	}
	if err := recovery.ValidateReady(ctx, 2, hash, runID, prepared2.snapshot.Digest(), 2); err != nil {
		t.Fatal(err)
	}
	third, err := recovery.Begin(ctx, 2, "coordinator-third", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	retryClient, _ := openVideoG7CapacityRedis(t)
	retryStore, err := NewRedisVideoCapacityStore(retryClient, 3, policy)
	if err != nil {
		t.Fatal(err)
	}
	retryCoordinator := NewVideoCapacityRecoveryCoordinator(builder, recovery, retryStore)
	prepared3, err := retryCoordinator.Prepare(ctx, third, policy)
	if err != nil {
		t.Fatal(err)
	}
	// 第一次Complete在Activate执行前确定失败，留下合法DB ready和Redis rebuilding；同prepared重试必须收敛。
	failBeforeActivate := &videoCapacityLostReplyHook{failBeforeAt: 2}
	retryClient.AddHook(failBeforeActivate)
	if _, err := retryCoordinator.Complete(ctx, third, prepared3); err == nil || failBeforeActivate.evals.Load() != 3 {
		t.Fatalf("Activate执行前失败必须保留可恢复窗口: evals=%d err=%v", failBeforeActivate.evals.Load(), err)
	}
	if err := recovery.ValidateReady(ctx, 3, hash, runID, prepared3.snapshot.Digest(), 2); err != nil {
		t.Fatalf("Activate前失败时MySQL ready应已提交: %v", err)
	}
	if view, err := retryStore.InspectRecovery(ctx, prepared3.snapshot); err != nil || view.Status != "rebuilding" {
		t.Fatalf("Activate未执行时Redis必须保持原rebuilding: %+v err=%v", view, err)
	}
	retryDB := captureVideoCapacityDB(t, db)
	if result, err := retryCoordinator.Complete(ctx, third, prepared3); err != nil || !reflect.DeepEqual(result, prepared3.Summary()) {
		t.Fatalf("同prepared必须补做Activate并收敛: result=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(retryDB, captureVideoCapacityDB(t, db)) {
		t.Fatal("补做Redis Activate不得重复写MySQL ready或审计")
	}
	fourth, err := recovery.Begin(ctx, 3, "coordinator-fourth", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	driftClient, _ := openVideoG7CapacityRedis(t)
	driftStore, err := NewRedisVideoCapacityStore(driftClient, 4, policy)
	if err != nil {
		t.Fatal(err)
	}
	driftCoordinator := NewVideoCapacityRecoveryCoordinator(builder, recovery, driftStore)
	prepared4, err := driftCoordinator.Prepare(ctx, fourth, policy)
	if err != nil {
		t.Fatal(err)
	}
	// 固定业务键禁止TTL；Prepare后出现TTL代表快照已漂移，Complete必须在发布MySQL ready前拒绝。
	if ok, err := driftClient.Expire(ctx, videoCapacityStateKey, time.Hour).Result(); err != nil || !ok {
		t.Fatalf("注入Redis TTL漂移失败: ok=%v err=%v", ok, err)
	}
	driftDB, driftRedis := captureVideoCapacityDB(t, db), videoG7CapacityRaw(t, driftClient)
	if _, err := driftCoordinator.Complete(ctx, fourth, prepared4); err == nil {
		t.Fatal("Prepare后Redis漂移必须失败关闭")
	}
	if !reflect.DeepEqual(driftDB, captureVideoCapacityDB(t, db)) || driftRedis != videoG7CapacityRaw(t, driftClient) {
		t.Fatal("Redis漂移拒绝不得产生协调器附加写入")
	}
	if ttl, err := driftClient.PTTL(ctx, videoCapacityStateKey).Result(); err != nil || ttl <= 0 {
		t.Fatalf("拒绝不能清除或重建漂移键: ttl=%v err=%v", ttl, err)
	}
}

func newVideoCapacityAttemptMust(t *testing.T, key *VideoCapacityNonceKey, epoch uint64, f videoG5ReservationFixture) *VideoCapacityAttempt {
	t.Helper()
	attempt, err := key.Attempt(epoch, VideoCapacityIdentity{TaskID: f.command.TaskID, RequestID: f.command.RequestID, UserID: f.owner.UserID, ProjectID: f.owner.ProjectID, APIKeyID: f.owner.APIKeyID, Model: f.command.FingerprintInput.LogicalModelCode, Provider: "fake-native-async", Operation: model.AIVideoOperationTextToVideo})
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}
