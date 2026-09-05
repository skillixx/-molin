package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type videoCutoffRaceLedger struct {
	video.VideoTaskLedger
	running  video.VideoRunningAdmissionLedger
	gate     video.VideoProviderSubmissionGate
	recovery *repository.VideoCapacityRecoveryRepository
	policy   string
	proof    *repository.VideoCapacityRecoveryLease
}

func (l *videoCutoffRaceLedger) Load(ctx context.Context, taskID string) (video.GatewayTask, error) {
	task, err := l.VideoTaskLedger.Load(ctx, taskID)
	task.DeferDelivery = false
	return task, err
}
func (l *videoCutoffRaceLedger) ClaimRunning(ctx context.Context, taskID string, version uint64) (video.GatewayTask, error) {
	task, err := l.running.ClaimRunning(ctx, taskID, version)
	if err != nil {
		return task, err
	}
	task.DeferDelivery = false
	l.proof, err = l.recovery.Begin(ctx, 0, "cutoff-race", l.policy, strings.Repeat("b", 40))
	return task, err
}
func (l *videoCutoffRaceLedger) ValidateProviderSubmission(ctx context.Context, taskID string, version uint64) error {
	return l.gate.ValidateProviderSubmission(ctx, taskID, version)
}

// TestVideoG7CapacityCutoffMySQL 证明Begin提交后旧G6创建、运行认领和Provider前校验全部停止。
func TestVideoG7CapacityCutoffMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	queued := prepareVideoG7Plan(t, db, model.AIVideoOperationTextToVideo)
	// prepare helper已进入submitting；单独准备真正queued任务验证ClaimRunning保持原状态。
	q := newVideoG5ReservationFixture(t, db, "10")
	if _, err := q.service.ReserveAndCreate(ctx, q.command); err != nil {
		t.Fatal(err)
	}
	qLease, err := repository.NewVideoWorkerLeaseRepository(db).Claim(ctx, q.command.TaskID, q.owner, "cutoff-queued", "submit")
	if err != nil {
		t.Fatal(err)
	}
	qOwned := repository.WithVideoWorkerLease(ctx, qLease)
	qLedger := NewVideoBillingTaskLedger(db, q.owner, q.service.protector, videoG4TestLocationFactory{}, q.service.referenceLoader)
	qTask, err := qLedger.Load(qOwned, q.command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	qTask, err = qLedger.Advance(qOwned, qTask.TaskID, qTask.Version, video.TaskQueued, "worker", "state_advanced", nil)
	if err != nil {
		t.Fatal(err)
	}
	qLedger.runningAdmission = true
	qLedger.runningLimits = videoG6RunningLimits()
	// 另一条submitting任务已持久计划，Begin后同样不得从计划成功推导RPC许可。
	planned := prepareVideoG7Plan(t, db, model.AIVideoOperationImageToVideo)
	if err := planned.ledger.RecordSubmissionPlan(planned.owned, planned.claim.TaskID, planned.claim.Version, "fake-native-async"); err != nil {
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
	// 把同一个Begin精确插在另一Task的Claim COMMIT之后、Provider紧前；随后复用该恢复代次验证全部门禁。
	race := newVideoG5ReservationFixture(t, db, "10")
	if _, err := race.service.ReserveAndCreate(ctx, race.command); err != nil {
		t.Fatal(err)
	}
	raceLease, err := repository.NewVideoWorkerLeaseRepository(db).Claim(ctx, race.command.TaskID, race.owner, "cutoff-race-worker", "submit")
	if err != nil {
		t.Fatal(err)
	}
	raceOwned := repository.WithVideoWorkerLease(ctx, raceLease)
	raceLedger := NewVideoBillingTaskLedger(db, race.owner, race.service.protector, videoG4TestLocationFactory{}, race.service.referenceLoader)
	raceTask, err := raceLedger.Load(raceOwned, race.command.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	raceTask, err = raceLedger.Advance(raceOwned, raceTask.TaskID, raceTask.Version, video.TaskQueued, "worker", "state_advanced", nil)
	if err != nil {
		t.Fatal(err)
	}
	raceLedger.runningAdmission, raceLedger.runningLimits = true, videoG6RunningLimits()
	recovery := repository.NewVideoCapacityRecoveryRepository(db)
	wrapped := &videoCutoffRaceLedger{VideoTaskLedger: raceLedger, running: raceLedger, gate: raceLedger, recovery: recovery, policy: hash}
	provider := video.NewFakeAsyncVideoAdapter(video.FakeVideoSuccess)
	gateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: wrapped, Provider: provider, Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), nil)})
	raceClaim, err := gateway.Submit(raceOwned, raceTask.TaskID)
	if !errors.Is(err, ErrVideoGovernanceUnavailable) || raceClaim.Status != video.TaskSubmitting || provider.SubmitCalls() != 0 || wrapped.proof == nil {
		t.Fatalf("Claim后恢复cutoff必须在RPC紧前阻断: status=%s provider=%d proof=%v err=%v", raceClaim.Status, provider.SubmitCalls(), wrapped.proof != nil, err)
	}
	proof := wrapped.proof
	t.Cleanup(func() { _ = recovery.Block(ctx, proof) })
	qBefore := captureVideoG7TaskWrite(t, db, qTask.TaskID, q.owner)
	if _, err := qLedger.ClaimRunning(qOwned, qTask.TaskID, qTask.Version); !errors.Is(err, ErrVideoGovernanceUnavailable) || errors.Is(err, video.ErrGatewayRunningCapacity) {
		t.Fatalf("恢复期间旧ClaimRunning必须保持queued: %v", err)
	}
	if !reflect.DeepEqual(qBefore, captureVideoG7TaskWrite(t, db, qTask.TaskID, q.owner)) {
		t.Fatal("拒绝运行认领不能修改Task、事件、输入或财务")
	}
	raceCandidate := videoG7PlanFixture{reservation: race, ledger: raceLedger, claim: raceClaim, proof: raceLease, owned: raceOwned}
	for _, candidate := range []videoG7PlanFixture{queued, planned, raceCandidate} {
		before := captureVideoG7TaskWrite(t, db, candidate.claim.TaskID, candidate.reservation.owner)
		if _, err := candidate.ledger.ValidateSubmissionClaim(candidate.owned, candidate.claim.TaskID, candidate.claim.Version); !errors.Is(err, ErrVideoGovernanceUnavailable) {
			t.Fatalf("恢复期间Provider前校验必须失败关闭: %v", err)
		}
		if err := candidate.ledger.RecordSubmissionPlan(candidate.owned, candidate.claim.TaskID, candidate.claim.Version, "fake-native-async"); !errors.Is(err, ErrVideoGovernanceUnavailable) {
			t.Fatalf("恢复期间不能新建或以计划重放冒充提交许可: %v", err)
		}
		if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, candidate.claim.TaskID, candidate.reservation.owner)) {
			t.Fatal("提交截止门禁拒绝必须零写")
		}
	}
	newTask := newVideoG5ReservationFixture(t, db, "10")
	// G5底层夹具默认不装队列门；生产HTTP会显式装配，这里保持同一真实路径。
	newTask.service.queue = NewMySQLVideoQueueAdmission()
	beforeFinance := mediaDeleteFinanceSnapshot(t, db, newTask.owner.UserID)
	if _, err := newTask.service.ReserveAndCreate(ctx, newTask.command); !errors.Is(err, ErrVideoGovernanceUnavailable) {
		t.Fatalf("恢复期间旧创建事务必须整笔回滚: %v", err)
	}
	var requests, tasks, holds int64
	_ = db.Table("ai_requests").Where("request_id=?", newTask.command.RequestID).Count(&requests).Error
	_ = db.Table("ai_gateway_tasks").Where("public_id=?", newTask.command.TaskID).Count(&tasks).Error
	_ = db.Table("wallet_holds").Where("user_id=? AND business_key=?", newTask.owner.UserID, newTask.command.RequestID+":video-hold").Count(&holds).Error
	if requests != 0 || tasks != 0 || holds != 0 || !reflect.DeepEqual(beforeFinance, mediaDeleteFinanceSnapshot(t, db, newTask.owner.UserID)) {
		t.Fatal("截止后的创建不能留下Request、Task、Hold或金融变化")
	}
}
