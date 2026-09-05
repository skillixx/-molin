package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7CapacitySnapshotMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	reserved := newVideoG5ReservationFixture(t, db, "10")
	if _, err := reserved.service.ReserveAndCreate(ctx, reserved.command); err != nil {
		t.Fatal(err)
	}
	queued := newVideoG5ReservationFixture(t, db, "10")
	prepareVideoG5I2V(t, &queued)
	if _, err := queued.service.ReserveAndCreate(ctx, queued.command); err != nil {
		t.Fatal(err)
	}
	qLease, err := repository.NewVideoWorkerLeaseRepository(db).Claim(ctx, queued.command.TaskID, queued.owner, "snapshot-queued", "submit")
	if err != nil {
		t.Fatal(err)
	}
	qOwned := repository.WithVideoWorkerLease(ctx, qLease)
	qLedger := NewVideoBillingTaskLedger(db, queued.owner, queued.service.protector, videoG4TestLocationFactory{}, queued.service.referenceLoader)
	qTask, err := qLedger.Load(qOwned, queued.command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = qLedger.Advance(qOwned, qTask.TaskID, qTask.Version, video.TaskQueued, "worker", "state_advanced", nil); err != nil {
		t.Fatal(err)
	}
	running := prepareVideoG7Plan(t, db, model.AIVideoOperationTextToVideo)
	if err := running.ledger.RecordSubmissionPlan(running.owned, running.claim.TaskID, running.claim.Version, "fake-native-async"); err != nil {
		t.Fatal(err)
	}
	runningRecord, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, running.claim.TaskID, running.reservation.owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := running.ledger.Advance(running.owned, running.claim.TaskID, runningRecord.VersionNo, video.TaskPendingReconcile, "worker", "submit_unknown", nil); err != nil {
		t.Fatal(err)
	}
	bound := prepareVideoG7Plan(t, db, model.AIVideoOperationImageToVideo)
	if err := bound.ledger.RecordSubmissionPlan(bound.owned, bound.claim.TaskID, bound.claim.Version, "fake-native-async"); err != nil {
		t.Fatal(err)
	}
	// 回执必须逐字节绑定提交计划在Provider调用前持久化的UUIDv4，不能继续使用历史Molin任务ID拼接值。
	boundPlan, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, bound.claim.TaskID, bound.reservation.owner)
	if err != nil || boundPlan.SubmissionIntentID == nil {
		t.Fatalf("必须先读取已冻结的Provider taskUUID: %v", err)
	}
	if _, err := bound.ledger.RecordSubmissionReceipt(bound.owned, bound.claim.TaskID, bound.claim.Version, video.SubmitResult{RequestID: bound.claim.RequestID, ProviderCode: "fake-native-async", ProviderTaskID: *boundPlan.SubmissionIntentID, Status: video.ProviderTaskQueued}); err != nil {
		t.Fatal(err)
	}
	cancelled := newVideoG5ReservationFixture(t, db, "10")
	if _, err := cancelled.service.ReserveAndCreate(ctx, cancelled.command); err != nil {
		t.Fatal(err)
	}
	if _, err := cancelled.service.CancelBeforeSubmit(ctx, cancelled.command.TaskID, cancelled.owner); err != nil {
		t.Fatal(err)
	}
	// 超过Redis活动上限的安全历史终态必须逐条验证后排除，不能因累计Task数永久阻断ready。
	for i := 0; i < 103; i++ {
		historical := newVideoG5ReservationFixture(t, db, "10")
		if _, err := historical.service.ReserveAndCreate(ctx, historical.command); err != nil {
			t.Fatal(err)
		}
		if _, err := historical.service.CancelBeforeSubmit(ctx, historical.command.TaskID, historical.owner); err != nil {
			t.Fatal(err)
		}
	}
	succeeded := newVideoG5ReservationFixture(t, db, "10")
	if _, err := succeeded.service.ReserveAndCreate(ctx, succeeded.command); err != nil {
		t.Fatal(err)
	}
	runVideoG5ReadyFixture(t, succeeded)
	if _, err := succeeded.service.SettleReady(ctx, succeeded.command.TaskID, succeeded.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := succeeded.service.DeliverReady(ctx, succeeded.command.TaskID, succeeded.owner); err != nil {
		t.Fatal(err)
	}
	checker := succeeded.owner.UserID + 900000
	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", checker).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := succeeded.service.ApplyAdjustment(ctx, succeeded.command.TaskID, succeeded.owner, VideoAdjustmentCommand{Direction: "credit", Reason: "service_credit", Amount: decimal.RequireFromString("0.10"), MakerID: succeeded.owner.UserID, CheckerID: checker, SequenceNo: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := succeeded.service.ApplyAdjustment(ctx, succeeded.command.TaskID, succeeded.owner, VideoAdjustmentCommand{Direction: "debit", Reason: "billing_correction", Amount: decimal.RequireFromString("0.05"), MakerID: succeeded.owner.UserID, CheckerID: checker, SequenceNo: 2}); err != nil {
		t.Fatal(err)
	}
	failed := newVideoG5ReservationFixture(t, db, "10")
	if _, err := failed.service.ReserveAndCreate(ctx, failed.command); err != nil {
		t.Fatal(err)
	}
	failLedger := NewVideoBillingTaskLedger(db, failed.owner, failed.service.protector, videoG4TestLocationFactory{}, failed.service.referenceLoader)
	failAdapter := video.NewFakeAsyncVideoAdapter(video.FakeVideoExplicitFailure)
	failGateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: failLedger, Provider: failAdapter, Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), nil)})
	if _, err := failGateway.Submit(ctx, failed.command.TaskID); err != nil {
		t.Fatal(err)
	}
	_, _ = failGateway.Poll(ctx, failed.command.TaskID)
	_, _ = failGateway.Poll(ctx, failed.command.TaskID)
	_, _ = failGateway.FetchAndFinalize(ctx, failed.command.TaskID)
	if _, err := failed.service.ReleaseUnserviceable(ctx, failed.command.TaskID, failed.owner); err != nil {
		t.Fatal(err)
	}
	failedChecker := failed.owner.UserID + 900000
	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", failedChecker).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := failed.service.ApplyAdjustment(ctx, failed.command.TaskID, failed.owner, VideoAdjustmentCommand{Direction: "credit", Reason: "service_credit", Amount: decimal.RequireFromString("0.10"), MakerID: failed.owner.UserID, CheckerID: failedChecker, SequenceNo: 1}); err != nil {
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
	proof, err := recovery.Begin(ctx, 0, "snapshot-builder", hash, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	before := []videoG7TaskWriteSnapshot{captureVideoG7TaskWrite(t, db, reserved.command.TaskID, reserved.owner), captureVideoG7TaskWrite(t, db, queued.command.TaskID, queued.owner), captureVideoG7TaskWrite(t, db, running.claim.TaskID, running.reservation.owner), captureVideoG7TaskWrite(t, db, bound.claim.TaskID, bound.reservation.owner), captureVideoG7TaskWrite(t, db, cancelled.command.TaskID, cancelled.owner), captureVideoG7TaskWrite(t, db, succeeded.command.TaskID, succeeded.owner), captureVideoG7TaskWrite(t, db, failed.command.TaskID, failed.owner)}
	builder := NewVideoCapacitySnapshotBuilder(db, recovery, mustVideoCapacityNonceKey(t))
	snapshot, summary, err := builder.BuildSnapshot(ctx, proof, policy)
	if err != nil || snapshot == nil || summary.Epoch != 1 || summary.Total != 4 || summary.Queued != 2 || summary.Running != 2 || snapshot.Count() != 4 || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(snapshot.Digest()) {
		t.Fatalf("必须形成完整低敏快照摘要: snapshot=%v summary=%+v err=%v", snapshot != nil, summary, err)
	}
	again, againSummary, err := builder.BuildSnapshot(ctx, proof, policy)
	if err != nil || again.Digest() != snapshot.Digest() || !reflect.DeepEqual(againSummary, summary) {
		t.Fatalf("同一proof重建必须稳定以核对Stage未知: %v", err)
	}
	var deliveryEvent model.AIOutboxEvent
	if err := db.Where("aggregate_id=? AND event_type='video_delivery_available'", succeeded.command.RequestID).Take(&deliveryEvent).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIOutboxEvent{}).Where("id=?", deliveryEvent.ID).UpdateColumns(map[string]any{"payload_json": json.RawMessage(`{}`), "updated_at": deliveryEvent.UpdatedAt}).Error; err != nil {
		t.Fatal(err)
	}
	if got, _, err := builder.BuildSnapshot(ctx, proof, policy); got != nil || !errors.Is(err, ErrVideoGovernanceUnavailable) {
		t.Fatalf("终态交付Outbox损坏必须阻断ready: %v", err)
	}
	if err := db.Model(&model.AIOutboxEvent{}).Where("id=?", deliveryEvent.ID).UpdateColumns(map[string]any{"payload_json": deliveryEvent.PayloadJSON, "updated_at": deliveryEvent.UpdatedAt}).Error; err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{snapshot, *snapshot} {
		body, err := json.Marshal(value)
		if err != nil || string(body) != `{"redacted":true}` || strings.Contains(fmt.Sprintf("%#v", value), "vid_task") {
			t.Fatal("快照不能通过普通JSON或格式化泄露任务/nonce")
		}
	}
	after := []videoG7TaskWriteSnapshot{captureVideoG7TaskWrite(t, db, reserved.command.TaskID, reserved.owner), captureVideoG7TaskWrite(t, db, queued.command.TaskID, queued.owner), captureVideoG7TaskWrite(t, db, running.claim.TaskID, running.reservation.owner), captureVideoG7TaskWrite(t, db, bound.claim.TaskID, bound.reservation.owner), captureVideoG7TaskWrite(t, db, cancelled.command.TaskID, cancelled.owner), captureVideoG7TaskWrite(t, db, succeeded.command.TaskID, succeeded.owner), captureVideoG7TaskWrite(t, db, failed.command.TaskID, failed.owner)}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("RR快照构建不得修改Task、输入、事件或财务")
	}
	if err := recovery.Validate(ctx, proof); err != nil {
		t.Fatal("快照不得消耗或阻断恢复proof")
	}
	if err := recovery.Block(ctx, proof); err != nil {
		t.Fatal(err)
	}
	// 通过测试专用的底层夹具构造历史无计划submitting；生产旧入口已由cutoff禁止。
	unknown := newVideoG5ReservationFixture(t, db, "10")
	if _, err := unknown.service.ReserveAndCreate(ctx, unknown.command); err != nil {
		t.Fatal(err)
	}
	uLease, err := repository.NewVideoWorkerLeaseRepository(db).Claim(ctx, unknown.command.TaskID, unknown.owner, "snapshot-unknown", "submit")
	if err != nil {
		t.Fatal(err)
	}
	uOwned := repository.WithVideoWorkerLease(ctx, uLease)
	uLedger := NewVideoBillingTaskLedger(db, unknown.owner, unknown.service.protector, videoG4TestLocationFactory{}, unknown.service.referenceLoader)
	uTask, err := uLedger.Load(uOwned, unknown.command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []video.TaskStatus{video.TaskQueued, video.TaskSubmitting} {
		uTask, err = uLedger.Advance(uOwned, uTask.TaskID, uTask.Version, state, "worker", "state_advanced", nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	if uTask, err = uLedger.Advance(uOwned, uTask.TaskID, uTask.Version, video.TaskFailed, "worker", "submit_failed", nil); err != nil {
		t.Fatal(err)
	}
	second, err := recovery.Begin(ctx, 1, "snapshot-unknown", hash, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	unknownBefore := captureVideoG7TaskWrite(t, db, unknown.command.TaskID, unknown.owner)
	if got, _, err := builder.BuildSnapshot(ctx, second, policy); got != nil || !errors.Is(err, ErrVideoGovernanceUnavailable) {
		t.Fatalf("没有可靠Provider结束或从未提交证明的failed必须阻断整个ready: %v", err)
	}
	if !reflect.DeepEqual(unknownBefore, captureVideoG7TaskWrite(t, db, unknown.command.TaskID, unknown.owner)) {
		t.Fatal("阻断快照不得修改未知任务或财务")
	}
	if err := recovery.Block(ctx, second); err != nil {
		t.Fatal(err)
	}
}
