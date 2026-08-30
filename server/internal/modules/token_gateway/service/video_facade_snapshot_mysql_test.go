package service

import (
	"context"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

type videoG5ReplaySnapshotContext struct{}

// 在原Request读出后原子提交完整pending/HPC，返回不得拼接两个时刻的执行/财务状态。
func TestVideoG5ReserveMySQLFacadeAutomaticReplaySnapshot(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, ledger, claim, receipt, _ := videoG5ClaimFixture(t, db)
	bound, err := ledger.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, receipt)
	if err != nil {
		t.Fatal(err)
	}
	var fired atomic.Bool
	callbackName := "g5_replay_snapshot_" + claim.TaskID
	if err := db.Callback().Query().After("gorm:query").Register(callbackName, func(query *gorm.DB) {
		if query.Statement.Context.Value(videoG5ReplaySnapshotContext{}) != true {
			return
		}
		if _, ok := query.Statement.Dest.(*model.VideoBillingRequest); !ok {
			return
		}
		if !fired.CompareAndSwap(false, true) {
			return
		}
		_, err := ledger.Advance(context.Background(), bound.TaskID, bound.Version, videogateway.TaskPendingReconcile, "worker", "submit_unknown", nil)
		if err != nil {
			query.AddError(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })
	ctx := context.WithValue(context.Background(), videoG5ReplaySnapshotContext{}, true)
	got, err := NewVideoQuoteFacade(f.quotes, f.service).CreateOpenAIVideo(ctx, videoG5FacadeRequest(f))
	if err != nil {
		t.Fatal(err)
	}
	if !fired.Load() {
		t.Fatal("未触发交错读取，不能作为快照证明")
	}
	if got.ExecutionStatus == model.AIImageTaskPendingReconcile && got.BillingStatus != model.AIBillingSettlementPending {
		t.Fatalf("返回了从未存在过的混合三轴: %s/%s", got.ExecutionStatus, got.BillingStatus)
	}
}

// 仅转发旧预占接口的包装器不能静默降级到报价先于鉴权的自动创建路径。
type videoG5ReservationOnlyWrapper struct{ VideoReservationCoordinator }

func TestVideoG5ReserveMySQLFacadeWrapperFailsClosed(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if err := db.Exec("UPDATE api_keys SET status='revoked' WHERE id=?", *f.owner.APIKeyID).Error; err != nil {
		t.Fatal(err)
	}
	_, err := NewVideoQuoteFacade(f.quotes, videoG5ReservationOnlyWrapper{f.service}).CreateOpenAIVideo(context.Background(), videoG5FacadeRequest(f))
	if err == nil {
		t.Fatal("自动协调能力缺失必须失败关闭")
	}
	var count int64
	if err := db.Model(&model.AIGatewayQuote{}).Where("user_id=?", f.owner.UserID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("包装器不得先写自动Quote: %d %v", count, err)
	}
}
