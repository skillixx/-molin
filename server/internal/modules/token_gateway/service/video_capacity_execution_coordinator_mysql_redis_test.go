package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

type videoCapacityQueuedFixture struct {
	base    videoG5ReservationFixture
	ledger  *VideoRepositoryTaskLedger
	proof   *repository.VideoWorkerLease
	ctx     context.Context
	queued  videogateway.GatewayTask
	finance []byte
}

type videoCapacityProviderEntryCounter struct {
	videogateway.VideoProviderAdapter
	entries atomic.Int64
}

func (p *videoCapacityProviderEntryCounter) Submit(ctx context.Context, request videogateway.SubmitRequest) (videogateway.SubmitResult, error) {
	p.entries.Add(1)
	return p.VideoProviderAdapter.Submit(ctx, request)
}

func TestVideoG7CapacityExecutionCoordinatorMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	fixtures := []videoCapacityQueuedFixture{
		prepareVideoG7CapacityQueued(t, db, model.AIVideoOperationTextToVideo),
		prepareVideoG7CapacityQueued(t, db, model.AIVideoOperationImageToVideo),
		prepareVideoG7CapacityQueued(t, db, model.AIVideoOperationTextToVideo),
		prepareVideoG7CapacityQueued(t, db, model.AIVideoOperationImageToVideo),
	}
	policy, err := videogateway.NewVideoCapacityPolicy(videogateway.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	recoveryProof, err := recovery.Begin(ctx, 0, "execution-coordinator", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	nonceKey := mustVideoCapacityNonceKey(t)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	recoveryCoordinator := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, nonceKey), recovery, store)
	prepared, err := recoveryCoordinator.Prepare(ctx, recoveryProof, policy)
	if err != nil || prepared.Summary().Queued != 4 || prepared.Summary().Running != 0 {
		t.Fatalf("执行前必须从原账本恢复四个排队债务: %+v err=%v", prepared.Summary(), err)
	}
	if _, err := recoveryCoordinator.Complete(ctx, recoveryProof, prepared); err != nil {
		t.Fatal(err)
	}
	first, second, badFinance, fourth := &fixtures[0], &fixtures[1], &fixtures[2], &fixtures[3]
	firstCoordinator := NewVideoCapacityExecutionCoordinator(first.ledger, recovery, store, nonceKey)
	firstBefore := captureVideoG7TaskWrite(t, db, first.queued.TaskID, first.base.owner)
	cancelledCtx, cancelPromotion := context.WithCancel(first.ctx)
	firstCoordinator.fault = func(stage string) error {
		if stage == "after_capacity_event" {
			cancelPromotion()
			return context.Canceled
		}
		return nil
	}
	if _, err := firstCoordinator.PromoteAndPlan(cancelledCtx, first.queued.TaskID, first.queued.Version); err == nil {
		t.Fatal("调用方取消导致MySQL回滚时必须向上返回")
	}
	assertVideoCapacityQueuedAfterAbort(t, db, store, nonceKey, first, firstBefore)
	firstCoordinator.fault = nil

	secondCoordinator := NewVideoCapacityExecutionCoordinator(second.ledger, recovery, store, nonceKey)
	secondBefore := captureVideoG7TaskWrite(t, db, second.queued.TaskID, second.base.owner)
	if _, err := secondCoordinator.PromoteAndPlan(ctx, second.queued.TaskID, second.queued.Version); err == nil {
		t.Fatal("缺少Worker proof必须失败关闭")
	}
	assertVideoCapacityQueuedAfterAbort(t, db, store, nonceKey, second, secondBefore)

	// 金额仍相同但Hold已非holding时不能获得running；拒绝后恢复本轮合成夹具再继续容量竞争。
	var badHold struct {
		ID uint64
	}
	if err := db.Table("wallet_holds").Select("id").Where("id=(SELECT wallet_hold_id FROM ai_request_wallet_links WHERE request_id=?)", badFinance.base.command.RequestID).Take(&badHold).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("wallet_holds").Where("id=?", badHold.ID).Updates(map[string]any{"status": "released", "settled_amount": 0, "settled_at": time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	badFinanceBefore := captureVideoG7TaskWrite(t, db, badFinance.queued.TaskID, badFinance.base.owner)
	badFinanceCoordinator := NewVideoCapacityExecutionCoordinator(badFinance.ledger, recovery, store, nonceKey)
	if _, err := badFinanceCoordinator.PromoteAndPlan(badFinance.ctx, badFinance.queued.TaskID, badFinance.queued.Version); err == nil {
		t.Fatal("非holding资金事实不能取得running")
	}
	assertVideoCapacityQueuedAfterAbort(t, db, store, nonceKey, badFinance, badFinanceBefore)

	// 第一条把真实MySQL COMMIT后丢确认与Redis Confirm执行后丢回复叠加，必须查询原事实后返回成功。
	pool := &videoCapacityUnknownCommitPool{videoBudgetCommitPool: &videoBudgetCommitPool{ConnPool: db.ConnPool}}
	faultDB, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: db.Logger})
	if err != nil {
		t.Fatal(err)
	}
	faultLedger := NewVideoBillingTaskLedger(faultDB, first.base.owner, first.base.service.protector, videoG4TestLocationFactory{}, first.base.service.referenceLoader)
	lostClient, _ := openVideoG7CapacityRedis(t)
	confirmLost := &videoCapacityLostReplyHook{loseAt: 3}
	lostClient.AddHook(confirmLost)
	lostStore, err := NewRedisVideoCapacityStore(lostClient, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	firstCoordinator = NewVideoCapacityExecutionCoordinator(faultLedger, recovery, lostStore, nonceKey)
	firstResult, err := firstCoordinator.PromoteAndPlan(first.ctx, first.queued.TaskID, first.queued.Version)
	if err != nil || firstResult.Status != videogateway.TaskSubmitting || !pool.lost.Load() || confirmLost.evals.Load() != 4 {
		t.Fatalf("双侧未知回执必须查询后收敛: dbLost=%v redisEvals=%d result=%+v err=%v", pool.lost.Load(), confirmLost.evals.Load(), firstResult, err)
	}
	assertVideoCapacityCommitted(t, db, store, nonceKey, first)
	assertVideoCapacityExecutionSQLGuards(t, db, first)

	// 第二条在Confirm调用Redis前失败；DB计划保留，原prepared重试只补Redis，不重复写MySQL。
	failClient, _ := openVideoG7CapacityRedis(t)
	confirmBefore := &videoCapacityLostReplyHook{failBeforeAt: 3}
	failClient.AddHook(confirmBefore)
	failStore, err := NewRedisVideoCapacityStore(failClient, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	secondCoordinator = NewVideoCapacityExecutionCoordinator(second.ledger, recovery, failStore, nonceKey)
	if _, err := secondCoordinator.PromoteAndPlan(second.ctx, second.queued.TaskID, second.queued.Version); err == nil || confirmBefore.evals.Load() != 4 {
		t.Fatalf("Confirm执行前失败必须保留可恢复状态: evals=%d err=%v", confirmBefore.evals.Load(), err)
	}
	committedBeforeRetry := captureVideoG7TaskWrite(t, db, second.queued.TaskID, second.base.owner)
	secondResult, err := secondCoordinator.PromoteAndPlan(second.ctx, second.queued.TaskID, second.queued.Version)
	if err != nil || secondResult.Status != videogateway.TaskSubmitting || confirmBefore.evals.Load() != 6 || !reflect.DeepEqual(committedBeforeRetry, captureVideoG7TaskWrite(t, db, second.queued.TaskID, second.base.owner)) {
		t.Fatalf("同计划重试必须只补Redis Confirm: evals=%d result=%+v err=%v", confirmBefore.evals.Load(), secondResult, err)
	}
	assertVideoCapacityCommitted(t, db, store, nonceKey, second)

	// 两种operation已共同占满Provider=2；第三条保持queued，不得写计划或改变财务。
	fourthBefore := captureVideoG7TaskWrite(t, db, fourth.queued.TaskID, fourth.base.owner)
	fourthCoordinator := NewVideoCapacityExecutionCoordinator(fourth.ledger, recovery, store, nonceKey)
	if _, err := fourthCoordinator.PromoteAndPlan(fourth.ctx, fourth.queued.TaskID, fourth.queued.Version); !errors.Is(err, videogateway.ErrGatewayRunningCapacity) {
		t.Fatalf("T2V/I2V必须共用Provider hard cap: %v", err)
	}
	if !reflect.DeepEqual(fourthBefore, captureVideoG7TaskWrite(t, db, fourth.queued.TaskID, fourth.base.owner)) {
		t.Fatal("Provider容量输家不得写MySQL计划或财务")
	}

	// 先以单次真实Gateway链证明计划后的当前版本与原claim版本分别使用且回执能够绑定。
	firstCapacityLedger := &VideoCapacityTaskLedger{VideoRepositoryTaskLedger: faultLedger, execution: firstCoordinator}
	firstProvider := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
	firstGateway := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: firstCapacityLedger, Provider: firstProvider, Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess))})
	firstSubmitted, err := firstGateway.Submit(first.ctx, first.queued.TaskID)
	if err != nil || firstProvider.SubmitCalls() != 1 || firstSubmitted.Status != videogateway.TaskSubmitted || firstSubmitted.SubmissionClaimVersion == 0 {
		t.Fatalf("PromoteAndPlan到Provider回执必须完整通过一次: calls=%d task=%+v err=%v", firstProvider.SubmitCalls(), firstSubmitted, err)
	}

	// 第二条由100个同证明竞争恢复；Fake Provider按冻结request_id/taskUUID合同只能形成一个任务。
	capacityLedger := &VideoCapacityTaskLedger{VideoRepositoryTaskLedger: second.ledger, execution: secondCoordinator}
	providerBase := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
	provider := &videoCapacityProviderEntryCounter{VideoProviderAdapter: providerBase}
	gateway := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: capacityLedger, Provider: provider, Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess))})
	start := make(chan struct{})
	results := make(chan error, 100)
	var workers sync.WaitGroup
	for index := 0; index < 100; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := gateway.Submit(second.ctx, second.queued.TaskID)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, videogateway.ErrDuplicateSubmitForbidden) && !errors.Is(err, ErrVideoGovernanceUnavailable) {
			t.Fatalf("并发恢复只能成功、命中同Provider任务或安全返回可重试治理错误: %v", err)
		}
	}
	if successes == 0 || provider.entries.Load() != 1 || providerBase.SubmitCalls() != 1 {
		t.Fatalf("100并发必须至少一个成功、网关主动Submit入口一次且只创建一个Fake任务: successes=%d entries=%d tasks=%d", successes, provider.entries.Load(), providerBase.SubmitCalls())
	}
	finalTask, err := capacityLedger.Load(second.ctx, second.queued.TaskID)
	if err != nil || finalTask.Status != videogateway.TaskSubmitted || finalTask.ProviderTaskID == "" || finalTask.SubmissionClaimVersion == 0 {
		t.Fatalf("原claim版本必须完成回执绑定: %+v err=%v", finalTask, err)
	}
}

func TestVideoG7CapacityExecutionHistoricalPlanMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	good := prepareVideoG7Plan(t, db, model.AIVideoOperationTextToVideo)
	bad := prepareVideoG7Plan(t, db, model.AIVideoOperationImageToVideo)
	for _, fixture := range []*videoG7PlanFixture{&good, &bad} {
		if err := fixture.ledger.RecordSubmissionPlan(fixture.owned, fixture.claim.TaskID, fixture.claim.Version, "fake-native-async"); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := videogateway.NewVideoCapacityPolicy(videogateway.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := policy.Fingerprint()
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	proof, err := recovery.Begin(ctx, 0, "historical-plan", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	key := mustVideoCapacityNonceKey(t)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	rebuild := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, key), recovery, store)
	prepared, err := rebuild.Prepare(ctx, proof, policy)
	if err != nil || prepared.Summary().Running != 2 || prepared.Summary().Queued != 0 {
		t.Fatalf("历史计划必须保守恢复为running债务: %+v err=%v", prepared.Summary(), err)
	}
	if _, err := rebuild.Complete(ctx, proof, prepared); err != nil {
		t.Fatal(err)
	}

	// 模拟旧代码只补Task epoch却未追加容量事件；恢复入口必须在触碰Redis前拒绝。
	badRecord, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, bad.claim.TaskID, bad.reservation.owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIImageTask{}).Where("id=? AND version_no=?", badRecord.ID, badRecord.VersionNo).Updates(map[string]any{"submission_capacity_epoch": 1, "version_no": gorm.Expr("version_no+1")}).Error; err != nil {
		t.Fatalf("构造缺容量事件的部分提交失败: %v", err)
	}
	var redisState map[string]any
	if err := json.Unmarshal([]byte(videoG7CapacityRaw(t, client)), &redisState); err != nil {
		t.Fatal(err)
	}
	records := redisState["records"].(map[string]any)
	row := records[bad.claim.TaskID].(map[string]any)
	row["phase"] = "promoting"
	changed, _ := json.Marshal(redisState)
	if err := client.Set(ctx, videoCapacityStateKey, changed, 0).Err(); err != nil {
		t.Fatal(err)
	}
	badBeforeDB, badBeforeRedis := captureVideoG7TaskWrite(t, db, bad.claim.TaskID, bad.reservation.owner), videoG7CapacityRaw(t, client)
	badCoordinator := NewVideoCapacityExecutionCoordinator(bad.ledger, recovery, store, key)
	if _, err := badCoordinator.PromoteAndPlan(bad.owned, bad.claim.TaskID, bad.claim.Version); err == nil {
		t.Fatal("缺少容量绑定事件的Task不能把Redis确认为running")
	}
	if !reflect.DeepEqual(badBeforeDB, captureVideoG7TaskWrite(t, db, bad.claim.TaskID, bad.reservation.owner)) || badBeforeRedis != videoG7CapacityRaw(t, client) {
		t.Fatal("坏历史计划拒绝必须MySQL/Redis双侧零写")
	}

	// 合法旧计划在当前Worker、资金和ready证明下只补epoch与唯一事件；Redis已由恢复快照保守计running。
	pool := &videoCapacityUnknownCommitPool{videoBudgetCommitPool: &videoBudgetCommitPool{ConnPool: db.ConnPool}}
	faultDB, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: db.Logger})
	if err != nil {
		t.Fatal(err)
	}
	faultLedger := NewVideoBillingTaskLedger(faultDB, good.reservation.owner, good.reservation.service.protector, videoG4TestLocationFactory{}, good.reservation.service.referenceLoader)
	goodCoordinator := NewVideoCapacityExecutionCoordinator(faultLedger, recovery, store, key)
	result, err := goodCoordinator.PromoteAndPlan(good.owned, good.claim.TaskID, good.claim.Version)
	if err != nil || !pool.lost.Load() || result.Status != videogateway.TaskSubmitting || result.SubmissionClaimVersion != good.claim.Version {
		t.Fatalf("历史计划COMMIT回执丢失必须查明并保留原claim: lost=%v result=%+v err=%v", pool.lost.Load(), result, err)
	}
	goodRecord, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, good.claim.TaskID, good.reservation.owner)
	if err != nil || goodRecord.SubmissionCapacityEpoch == nil || *goodRecord.SubmissionCapacityEpoch != 1 {
		t.Fatalf("历史计划必须只补当前容量epoch: %+v err=%v", goodRecord, err)
	}
	if err := validateVideoCapacityPlanEventsTx(db, goodRecord); err != nil {
		t.Fatalf("历史计划必须形成完整两事件证明: %v", err)
	}
}

func TestVideoG7CapacityExecutionNextEpochRecoveryMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	fixture := prepareVideoG7CapacityQueued(t, db, model.AIVideoOperationImageToVideo)
	policy, err := videogateway.NewVideoCapacityPolicy(videogateway.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := policy.Fingerprint()
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	firstProof, err := recovery.Begin(ctx, 0, "next-epoch-first", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	key := mustVideoCapacityNonceKey(t)
	firstStore, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	firstRecovery := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, key), recovery, firstStore)
	prepared, err := firstRecovery.Prepare(ctx, firstProof, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := firstRecovery.Complete(ctx, firstProof, prepared); err != nil {
		t.Fatal(err)
	}
	execution := NewVideoCapacityExecutionCoordinator(fixture.ledger, recovery, firstStore, key)
	if _, err := execution.PromoteAndPlan(fixture.ctx, fixture.queued.TaskID, fixture.queued.Version); err != nil {
		t.Fatal(err)
	}
	before := captureVideoG7TaskWrite(t, db, fixture.queued.TaskID, fixture.base.owner)
	secondProof, err := recovery.Begin(ctx, 1, "next-epoch-second", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := NewRedisVideoCapacityStore(client, 2, policy)
	if err != nil {
		t.Fatal(err)
	}
	secondRecovery := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, key), recovery, secondStore)
	prepared2, err := secondRecovery.Prepare(ctx, secondProof, policy)
	if err != nil || prepared2.Summary().Running != 1 || prepared2.Summary().Queued != 0 {
		t.Fatalf("旧容量epoch计划必须进入新epoch完整快照: %+v err=%v", prepared2.Summary(), err)
	}
	if _, err := secondRecovery.Complete(ctx, secondProof, prepared2); err != nil {
		t.Fatal(err)
	}
	after := captureVideoG7TaskWrite(t, db, fixture.queued.TaskID, fixture.base.owner)
	if !reflect.DeepEqual(before, after) || after.task.SubmissionCapacityEpoch == nil || *after.task.SubmissionCapacityEpoch != 1 {
		t.Fatal("新恢复epoch不能覆盖原始提交授权epoch或追加业务事件")
	}
	identity, err := videoCapacityIdentityForTask(after.task)
	if err != nil {
		t.Fatal(err)
	}
	currentAttempt, _ := key.Attempt(2, identity)
	if view, err := secondStore.Read(ctx, currentAttempt); err != nil || view.Phase != "running" {
		t.Fatalf("新epoch必须以新nonce恢复原running债务: %+v err=%v", view, err)
	}
	oldAttempt, _ := key.Attempt(1, identity)
	if _, err := firstStore.Read(ctx, oldAttempt); !errors.Is(err, ErrVideoCapacityUnavailable) {
		t.Fatalf("旧epoch容量能力不能操作新快照: %v", err)
	}
}

func TestVideoG7CapacitySendPermitCrashWindowMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	fixture := prepareVideoG7CapacityQueued(t, db, model.AIVideoOperationTextToVideo)
	policy, err := videogateway.NewVideoCapacityPolicy(videogateway.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := policy.Fingerprint()
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	proof, err := recovery.Begin(ctx, 0, "send-crash-window", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	key := mustVideoCapacityNonceKey(t)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	rebuild := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, key), recovery, store)
	prepared, err := rebuild.Prepare(ctx, proof, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rebuild.Complete(ctx, proof, prepared); err != nil {
		t.Fatal(err)
	}
	ledger, err := NewVideoCapacityTaskLedger(fixture.ledger, recovery, store, key)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := ledger.ClaimRunning(fixture.ctx, fixture.queued.TaskID, fixture.queued.Version)
	if err != nil || planned.Status != videogateway.TaskSubmitting || planned.SubmissionClaimVersion == 0 {
		t.Fatalf("发送权消费前必须完成原计划: %+v err=%v", planned, err)
	}
	deadline, err := ledger.ValidateSubmissionClaim(fixture.ctx, planned.TaskID, planned.SubmissionClaimVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.ValidateProviderSubmission(fixture.ctx, planned.TaskID, planned.Version); err != nil {
		t.Fatalf("原进程必须能消费一次发送权: %v", err)
	}
	if err := ledger.ValidateProviderSubmission(fixture.ctx, planned.TaskID, planned.Version); !errors.Is(err, ErrVideoGovernanceUnavailable) {
		t.Fatalf("同进程不能第二次进入Provider: %v", err)
	}
	// 新进程可读取同计划并继续Query收口，但没有明文permit，绝不能重新create。
	restarted, err := NewVideoCapacityTaskLedger(fixture.ledger, recovery, store, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.ResumePlannedSubmission(fixture.ctx, planned.TaskID, planned.Version); err != nil {
		t.Fatalf("重启进程必须能识别原在途计划: %v", err)
	}
	if err := restarted.ValidateProviderSubmission(fixture.ctx, planned.TaskID, planned.Version); !errors.Is(err, ErrVideoGovernanceUnavailable) {
		t.Fatalf("重启后丢失permit只能查询，不能重新Submit: %v", err)
	}
	// 模拟permit消费后、Provider入口前进程退出；两分钟观察窗后只进入pending_reconcile，保留Hold和容量债务。
	fixture.base.service.now = func() time.Time { return deadline.Add(3 * time.Minute) }
	if _, err := fixture.base.service.RecoverExpiredSubmission(ctx, planned.TaskID, fixture.base.owner); err != nil {
		t.Fatal(err)
	}
	record, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, planned.TaskID, fixture.base.owner)
	if err != nil || record.Status != model.AIImageTaskPendingReconcile || record.ProviderTaskID != nil || record.AttemptCount != 0 {
		t.Fatalf("未知发送窗口必须收口pending_reconcile且不伪造Provider回执: %+v err=%v", record, err)
	}
	identity, _ := videoCapacityIdentityForTask(record)
	attempt, _ := key.Attempt(1, identity)
	if view, err := store.Read(ctx, attempt); err != nil || view.Phase != "running" {
		t.Fatalf("pending_reconcile必须继续占Provider容量: %+v err=%v", view, err)
	}
	var hold billingmodel.WalletHold
	if err := db.Where("id=(SELECT wallet_hold_id FROM ai_request_wallet_links WHERE request_id=?)", record.RequestID).Take(&hold).Error; err != nil || hold.Status != billingmodel.HoldStatusHolding {
		t.Fatalf("未知发送窗口不能释放真实Hold: status=%s err=%v", hold.Status, err)
	}
	beforePendingRelease := videoG7CapacityRaw(t, client)
	if err := ledger.execution.ReleaseTerminal(ctx, planned.TaskID); err == nil || beforePendingRelease != videoG7CapacityRaw(t, client) {
		t.Fatal("pending_reconcile不能释放Provider容量")
	}
}

func TestVideoG7CapacityTerminalReleaseMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	fixture := prepareVideoG7CapacityQueued(t, db, model.AIVideoOperationImageToVideo)
	policy, err := videogateway.NewVideoCapacityPolicy(videogateway.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := policy.Fingerprint()
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	proof, err := recovery.Begin(ctx, 0, "terminal-release", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	key := mustVideoCapacityNonceKey(t)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	rebuild := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, key), recovery, store)
	prepared, err := rebuild.Prepare(ctx, proof, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rebuild.Complete(ctx, proof, prepared); err != nil {
		t.Fatal(err)
	}
	queuedRecord, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, fixture.queued.TaskID, fixture.base.owner)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := videoCapacityIdentityForTask(queuedRecord)
	attempt, _ := key.Attempt(1, identity)
	if view, err := store.Read(ctx, attempt); err != nil || view.Phase != "queued" {
		t.Fatalf("取消前必须存在queued容量: %+v err=%v", view, err)
	}
	if _, err := fixture.base.service.CancelBeforeSubmit(ctx, fixture.queued.TaskID, fixture.base.owner); err != nil {
		t.Fatal(err)
	}
	wrongOwner := repository.VideoOwner{UserID: fixture.base.owner.UserID + 1, ProjectID: fixture.base.owner.ProjectID + 1, APIKeyID: fixture.base.owner.APIKeyID}
	wrongLedger := NewVideoBillingTaskLedger(db, wrongOwner, fixture.base.service.protector, videoG4TestLocationFactory{}, fixture.base.service.referenceLoader)
	wrong := NewVideoCapacityExecutionCoordinator(wrongLedger, recovery, store, key)
	beforeWrong := videoG7CapacityRaw(t, client)
	if err := wrong.ReleaseTerminal(ctx, fixture.queued.TaskID); err == nil || beforeWrong != videoG7CapacityRaw(t, client) {
		t.Fatal("跨用户/Project释放必须404语义边界内失败且不改容量")
	}
	execution := NewVideoCapacityExecutionCoordinator(fixture.ledger, recovery, store, key)
	if err := execution.ReleaseTerminal(ctx, fixture.queued.TaskID); err != nil {
		t.Fatalf("安全取消终态必须释放queued容量: %v", err)
	}
	if _, err := store.Read(ctx, attempt); !errors.Is(err, ErrVideoCapacityLeaseLost) {
		t.Fatalf("释放后旧容量能力必须不存在: %v", err)
	}
	beforeReplay := captureVideoG7TaskWrite(t, db, fixture.queued.TaskID, fixture.base.owner)
	beforeRedis := videoG7CapacityRaw(t, client)
	if err := execution.ReleaseTerminal(ctx, fixture.queued.TaskID); err != nil || !reflect.DeepEqual(beforeReplay, captureVideoG7TaskWrite(t, db, fixture.queued.TaskID, fixture.base.owner)) || beforeRedis != videoG7CapacityRaw(t, client) {
		t.Fatalf("终态释放重放必须MySQL/Redis双侧零写: %v", err)
	}
}

func TestVideoG7CapacityReservationCoordinatorMySQLRedis(t *testing.T) {
	db := openVideoG5MySQL(t)
	client, runID := openVideoG7CapacityRedis(t)
	ctx := context.Background()
	first := newVideoG5ReservationFixture(t, db, "10")
	second := newVideoG5ReservationFixture(t, db, "10")
	third := newVideoG5ReservationFixture(t, db, "10")
	fourth := newVideoG5ReservationFixture(t, db, "10")
	limits := videogateway.DefaultVideoCapacityLimits()
	limits.Queued.Global = 1
	policy, err := videogateway.NewVideoCapacityPolicy(limits)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := policy.Fingerprint()
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	proof, err := recovery.Begin(ctx, 0, "reservation-coordinator", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, videoCapacityStateKey).Err(); err != nil {
		t.Fatal(err)
	}
	key := mustVideoCapacityNonceKey(t)
	store, err := NewRedisVideoCapacityStore(client, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	rebuild := NewVideoCapacityRecoveryCoordinator(NewVideoCapacitySnapshotBuilder(db, recovery, key), recovery, store)
	prepared, err := rebuild.Prepare(ctx, proof, policy)
	if err != nil || prepared.Summary().Total != 0 {
		t.Fatalf("新流量前必须从空Task账本发布ready: %+v err=%v", prepared.Summary(), err)
	}
	if _, err := rebuild.Complete(ctx, proof, prepared); err != nil {
		t.Fatal(err)
	}
	lostClient, _ := openVideoG7CapacityRedis(t)
	reserveLost := &videoCapacityLostReplyHook{}
	lostClient.AddHook(reserveLost)
	lostStore, err := NewRedisVideoCapacityStore(lostClient, 1, policy)
	if err != nil {
		t.Fatal(err)
	}
	lostCoordinator, err := NewVideoCapacityReservationCoordinator(first.service, recovery, lostStore, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lostCoordinator.ReserveAndCreate(ctx, first.command); err == nil || reserveLost.evals.Load() != 2 {
		t.Fatalf("Redis queued执行成功但回复丢失必须回滚MySQL并按原attempt清理: evals=%d err=%v", reserveLost.evals.Load(), err)
	}
	if _, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, first.command.TaskID, first.owner); !errors.Is(err, repository.ErrVideoTaskNotFound) {
		t.Fatalf("Redis回执未知不得提交Task: %v", err)
	}
	assertVideoCapacityRecordCount(t, client, 0)
	firstCoordinator, err := NewVideoCapacityReservationCoordinator(first.service, recovery, store, key)
	if err != nil {
		t.Fatal(err)
	}
	firstCoordinator.base.fault = func(stage string) error {
		if stage == "queue_admission" {
			return errors.New("合成Redis预留后MySQL明确回滚")
		}
		return nil
	}
	if _, err := firstCoordinator.ReserveAndCreate(ctx, first.command); err == nil {
		t.Fatal("队列预留后的MySQL故障必须返回")
	}
	if _, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, first.command.TaskID, first.owner); !errors.Is(err, repository.ErrVideoTaskNotFound) {
		t.Fatalf("明确回滚不得留下Task: %v", err)
	}
	assertVideoCapacityRecordCount(t, client, 0)
	firstCoordinator.base.fault = nil
	created, err := firstCoordinator.ReserveAndCreate(ctx, first.command)
	if err != nil || created.Existing || created.TaskID != first.command.TaskID {
		t.Fatalf("容量允许时必须提交原财务事务: %+v err=%v", created, err)
	}
	firstRecord, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, first.command.TaskID, first.owner)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := videoCapacityIdentityForTask(firstRecord)
	attempt, _ := key.Attempt(1, identity)
	if view, err := store.Read(ctx, attempt); err != nil || view.Phase != "queued" {
		t.Fatalf("成功创建必须原子保留queued: %+v err=%v", view, err)
	}
	beforeReplay := videoG7CapacityRaw(t, client)
	replay, err := firstCoordinator.ReserveAndCreate(ctx, first.command)
	if err != nil || !replay.Existing || beforeReplay != videoG7CapacityRaw(t, client) {
		t.Fatalf("生成幂等重放不能新增容量或资金: %+v err=%v", replay, err)
	}
	secondCoordinator, err := NewVideoCapacityReservationCoordinator(second.service, recovery, store, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondCoordinator.ReserveAndCreate(ctx, second.command); !errors.Is(err, ErrVideoQueueFull) {
		t.Fatalf("全局queued=1必须拒绝第二个任务: %v", err)
	}
	for table, where := range map[string]string{
		"ai_requests":      "request_id=?",
		"ai_gateway_tasks": "public_id=?",
		"wallet_holds":     "user_id=?",
		"ai_outbox_events": "aggregate_id=?",
	} {
		argument := any(second.command.RequestID)
		if table == "ai_gateway_tasks" {
			argument = second.command.TaskID
		}
		if table == "wallet_holds" {
			argument = second.owner.UserID
		}
		var count int64
		if err := db.Table(table).Where(where, argument).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("容量拒绝不得提交%s事实: count=%d err=%v", table, count, err)
		}
	}
	assertVideoCapacityRecordCount(t, client, 1)

	// 释放第一条安全取消任务后，同一容量协调器必须支持/v1自动Quote事务，不能只覆盖显式Quote门面。
	if _, err := first.service.CancelBeforeSubmit(ctx, first.command.TaskID, first.owner); err != nil {
		t.Fatal(err)
	}
	firstLedger := NewVideoBillingTaskLedger(db, first.owner, first.service.protector, videoG4TestLocationFactory{}, first.service.referenceLoader)
	if err := NewVideoCapacityExecutionCoordinator(firstLedger, recovery, store, key).ReleaseTerminal(ctx, first.command.TaskID); err != nil {
		t.Fatal(err)
	}
	assertVideoCapacityRecordCount(t, client, 0)
	automatic := VideoFacadeRequest{Prompt: second.command.Prompt, RightsPolicyVersion: second.command.RightsPolicyVersion, IdempotencyKey: second.command.IdempotencyKey, RequestID: second.command.RequestID, TaskID: second.command.TaskID, FingerprintInput: second.command.FingerprintInput}
	automaticResult, err := secondCoordinator.CreateWithAutomaticQuote(ctx, automatic, second.quotes)
	if err != nil || automaticResult.Existing || automaticResult.TaskID != second.command.TaskID || automaticResult.Quote == nil {
		t.Fatalf("OpenAI自动Quote也必须经过同一queued协调: %+v err=%v", automaticResult, err)
	}
	assertVideoCapacityRecordCount(t, client, 1)
	if _, err := second.service.CancelBeforeSubmit(ctx, second.command.TaskID, second.owner); err != nil {
		t.Fatal(err)
	}
	secondLedger := NewVideoBillingTaskLedger(db, second.owner, second.service.protector, videoG4TestLocationFactory{}, second.service.referenceLoader)
	if err := NewVideoCapacityExecutionCoordinator(secondLedger, recovery, store, key).ReleaseTerminal(ctx, second.command.TaskID); err != nil {
		t.Fatal(err)
	}
	assertVideoCapacityRecordCount(t, client, 0)

	pool := &videoCapacityUnknownCommitPool{videoBudgetCommitPool: &videoBudgetCommitPool{ConnPool: db.ConnPool}}
	faultDB, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: db.Logger})
	if err != nil {
		t.Fatal(err)
	}
	faultBase := *third.service
	faultBase.db = faultDB
	thirdCoordinator, err := NewVideoCapacityReservationCoordinator(&faultBase, recovery, store, key)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := thirdCoordinator.ReserveAndCreate(ctx, third.command)
	if err != nil || !pool.lost.Load() || committed.TaskID != third.command.TaskID {
		t.Fatalf("MySQL COMMIT回执丢失必须回读原生成事实: lost=%v result=%+v err=%v", pool.lost.Load(), committed, err)
	}
	for table, where := range map[string]string{"ai_requests": "request_id=?", "ai_gateway_tasks": "public_id=?", "wallet_holds": "user_id=?"} {
		argument := any(third.command.RequestID)
		if table == "ai_gateway_tasks" {
			argument = third.command.TaskID
		}
		if table == "wallet_holds" {
			argument = third.owner.UserID
		}
		var count int64
		if err := db.Table(table).Where(where, argument).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("COMMIT未知不得重复%s事实: count=%d err=%v", table, count, err)
		}
	}
	assertVideoCapacityRecordCount(t, client, 1)
	if _, err := third.service.CancelBeforeSubmit(ctx, third.command.TaskID, third.owner); err != nil {
		t.Fatal(err)
	}
	thirdLedger := NewVideoBillingTaskLedger(db, third.owner, third.service.protector, videoG4TestLocationFactory{}, third.service.referenceLoader)
	if err := NewVideoCapacityExecutionCoordinator(thirdLedger, recovery, store, key).ReleaseTerminal(ctx, third.command.TaskID); err != nil {
		t.Fatal(err)
	}
	assertVideoCapacityRecordCount(t, client, 0)

	fourthCoordinator, err := NewVideoCapacityReservationCoordinator(fourth.service, recovery, store, key)
	if err != nil {
		t.Fatal(err)
	}
	type reservationResult struct {
		value *VideoPreparedGeneration
		err   error
	}
	start := make(chan struct{})
	results := make(chan reservationResult, 100)
	var workers sync.WaitGroup
	for index := 0; index < 100; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			value, err := fourthCoordinator.ReserveAndCreate(ctx, fourth.command)
			results <- reservationResult{value: value, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for result := range results {
		if result.err != nil || result.value == nil || result.value.TaskID != fourth.command.TaskID {
			t.Fatalf("100并发同生成意图必须返回唯一原任务: %+v err=%v", result.value, result.err)
		}
	}
	for table, where := range map[string]string{"ai_requests": "request_id=?", "ai_gateway_tasks": "public_id=?", "wallet_holds": "user_id=?"} {
		argument := any(fourth.command.RequestID)
		if table == "ai_gateway_tasks" {
			argument = fourth.command.TaskID
		}
		if table == "wallet_holds" {
			argument = fourth.owner.UserID
		}
		var count int64
		if err := db.Table(table).Where(where, argument).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("100并发只能提交一个%s事实: count=%d err=%v", table, count, err)
		}
	}
	assertVideoCapacityRecordCount(t, client, 1)
}

func assertVideoCapacityRecordCount(t *testing.T, client *redis.Client, want int) {
	t.Helper()
	var state struct {
		Count   int                        `json:"count"`
		Records map[string]json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal([]byte(videoG7CapacityRaw(t, client)), &state); err != nil || state.Count != want || len(state.Records) != want {
		t.Fatalf("Redis容量记录数错误: count=%d records=%d want=%d err=%v", state.Count, len(state.Records), want, err)
	}
}

func prepareVideoG7CapacityQueued(t *testing.T, db *gorm.DB, operation string) videoCapacityQueuedFixture {
	t.Helper()
	ctx := context.Background()
	fixture := newVideoG5ReservationFixture(t, db, "10")
	if operation == model.AIVideoOperationImageToVideo {
		prepareVideoG5I2V(t, &fixture)
	}
	if _, err := fixture.service.ReserveAndCreate(ctx, fixture.command); err != nil {
		t.Fatal(err)
	}
	proof, err := repository.NewVideoWorkerLeaseRepository(db).Claim(ctx, fixture.command.TaskID, fixture.owner, "capacity-execution-worker", "submit")
	if err != nil {
		t.Fatal(err)
	}
	owned := repository.WithVideoWorkerLease(ctx, proof)
	ledger := NewVideoBillingTaskLedger(db, fixture.owner, fixture.service.protector, videoG4TestLocationFactory{}, fixture.service.referenceLoader)
	current, err := ledger.Load(owned, fixture.command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := ledger.Advance(owned, current.TaskID, current.Version, videogateway.TaskQueued, "worker", "state_advanced", nil)
	if err != nil {
		t.Fatal(err)
	}
	return videoCapacityQueuedFixture{base: fixture, ledger: ledger, proof: proof, ctx: owned, queued: queued, finance: videoCapacityFinancialSnapshot(t, db, fixture.owner.UserID)}
}

func assertVideoCapacityQueuedAfterAbort(t *testing.T, db *gorm.DB, store *RedisVideoCapacityStore, key *VideoCapacityNonceKey, fixture *videoCapacityQueuedFixture, before videoG7TaskWriteSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, fixture.queued.TaskID, fixture.base.owner)) {
		t.Fatal("MySQL失败补偿不得留下Task、事件、输入或财务变化")
	}
	record, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), fixture.queued.TaskID, fixture.base.owner)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := videoCapacityIdentityForTask(record)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := key.Attempt(1, identity)
	if err != nil {
		t.Fatal(err)
	}
	if view, err := store.Read(context.Background(), attempt); err != nil || view.Phase != "queued" {
		t.Fatalf("MySQL明确未提交后必须恢复原queued债务: %+v err=%v", view, err)
	}
}

func assertVideoCapacityCommitted(t *testing.T, db *gorm.DB, store *RedisVideoCapacityStore, key *VideoCapacityNonceKey, fixture *videoCapacityQueuedFixture) {
	t.Helper()
	record, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), fixture.queued.TaskID, fixture.base.owner)
	if err != nil || record.SubmissionCapacityEpoch == nil || *record.SubmissionCapacityEpoch != 1 || record.PlannedProviderCode == nil || *record.PlannedProviderCode != "fake-native-async" || record.SubmissionSendTokenHash == nil || !lowerHex64.MatchString(*record.SubmissionSendTokenHash) || record.SubmissionSendWorker == nil || record.SubmissionSendStartedAt == nil || record.ProviderTaskID != nil || record.AttemptCount != 0 {
		t.Fatalf("MySQL必须只保存计划和原始容量epoch: %+v err=%v", record, err)
	}
	identity, err := videoCapacityIdentityForTask(record)
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := key.Attempt(1, identity)
	if err != nil {
		t.Fatal(err)
	}
	if view, err := store.Read(context.Background(), attempt); err != nil || view.Phase != "running" {
		t.Fatalf("MySQL提交后Redis必须为running: %+v err=%v", view, err)
	}
	var planEvents, capacityEvents, sendEvents int
	for _, event := range captureVideoG7TaskWrite(t, db, fixture.queued.TaskID, fixture.base.owner).events {
		switch event.EventType {
		case "video_submission_planned":
			planEvents++
		case "video_submission_capacity_bound":
			capacityEvents++
		case "video_submission_send_claimed":
			sendEvents++
		}
	}
	if planEvents != 1 || capacityEvents != 1 || sendEvents != 1 || !reflect.DeepEqual(fixture.finance, videoCapacityFinancialSnapshot(t, db, fixture.base.owner.UserID)) {
		t.Fatal("协调只能追加唯一计划/容量/发送事件，不能改变七张财务事实")
	}
}

func assertVideoCapacityExecutionSQLGuards(t *testing.T, db *gorm.DB, fixture *videoCapacityQueuedFixture) {
	t.Helper()
	before := captureVideoG7TaskWrite(t, db, fixture.queued.TaskID, fixture.base.owner)
	statements := []struct {
		sql  string
		args []any
	}{
		{"UPDATE ai_gateway_tasks SET submission_capacity_epoch=2,version_no=version_no+1 WHERE public_id=?", []any{fixture.queued.TaskID}},
		{"UPDATE ai_gateway_tasks SET submission_capacity_epoch=NULL,version_no=version_no+1 WHERE public_id=?", []any{fixture.queued.TaskID}},
		{"UPDATE ai_gateway_tasks SET planned_provider_code='other',version_no=version_no+1 WHERE public_id=?", []any{fixture.queued.TaskID}},
		{"UPDATE ai_gateway_tasks SET submission_send_token_sha256=REPEAT('f',64),version_no=version_no+1 WHERE public_id=?", []any{fixture.queued.TaskID}},
		{"UPDATE ai_gateway_tasks SET submission_send_token_sha256=NULL,submission_send_worker_version=NULL,submission_send_started_at=NULL,version_no=version_no+1 WHERE public_id=?", []any{fixture.queued.TaskID}},
		{"INSERT INTO ai_gateway_task_events(event_id,task_id,user_id,project_id,event_type,source,safe_detail_json,created_at) SELECT 'forged_capacity_event',id,user_id,project_id,'video_submission_capacity_bound','worker',JSON_OBJECT(),UTC_TIMESTAMP(6) FROM ai_gateway_tasks WHERE public_id=?", []any{fixture.queued.TaskID}},
		{"INSERT INTO ai_gateway_task_events(event_id,task_id,user_id,project_id,event_type,source,safe_detail_json,created_at) SELECT 'forged_send_event',id,user_id,project_id,'video_submission_send_claimed','worker',JSON_OBJECT(),UTC_TIMESTAMP(6) FROM ai_gateway_tasks WHERE public_id=?", []any{fixture.queued.TaskID}},
	}
	for index, statement := range statements {
		if err := db.Exec(statement.sql, statement.args...).Error; err == nil {
			t.Fatalf("容量SQL负例%d必须被数据库拒绝", index)
		}
	}
	if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, fixture.queued.TaskID, fixture.base.owner)) {
		t.Fatal("SQL拒绝不得改变Task、事件、输入或财务事实")
	}
}

func videoCapacityFinancialSnapshot(t *testing.T, db *gorm.DB, userID uint64) []byte {
	t.Helper()
	var tables map[string]json.RawMessage
	if err := json.Unmarshal(mediaDeleteFinanceSnapshot(t, db, userID), &tables); err != nil {
		t.Fatal(err)
	}
	// Task推进会按原Repository同步Request执行轴；这里只冻结其余七张资金事实。
	delete(tables, "ai_requests")
	raw, err := json.Marshal(tables)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
