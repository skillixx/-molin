package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

type videoG5UsageMismatchAdapter struct {
	videogateway.VideoProviderAdapter
}

func TestVideoG5UnknownMySQLCallbackAtomic(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, _, _ := videoG5CancellationFixture(t, db, model.AIVideoOperationTextToVideo, videogateway.FakeVideoSuccess)
	l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil)
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	c := videogateway.VerifiedCallback{ProviderCode: *task.ProviderCode, ProviderTaskID: *task.ProviderTaskID, ExternalEventID: f.command.RequestID + "_unknown", BodySHA256: strings.Repeat("d", 64), Status: videogateway.ProviderTaskUnknown}
	l.financialFault = func(at string) error {
		if at == "execution_pending_outbox" {
			return errors.New("合成回调编排故障")
		}
		return nil
	}
	if _, err := l.RecordCallback(context.Background(), f.command.TaskID, c); err == nil {
		t.Fatal("回调与待核对事实必须同事务")
	}
	after, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil || after.Status != model.AIImageTaskSubmitted || after.BillingStatus != model.AIBillingHeld {
		t.Fatal("回调及状态必须一起回滚")
	}
	l.financialFault = nil
	if _, err := l.RecordCallback(context.Background(), f.command.TaskID, c); err != nil {
		t.Fatal(err)
	}
	if old, err := l.RecordCallback(context.Background(), f.command.TaskID, c); err != nil || !old {
		t.Fatalf("重放须相同ACK: %v", err)
	}
	after, err = repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil || after.Status != model.AIImageTaskPendingReconcile || after.BillingStatus != model.AIBillingSettlementPending {
		t.Fatal("未知回调需完整待核对")
	}
}

func (a videoG5UsageMismatchAdapter) Query(ctx context.Context, r videogateway.QueryRequest) (videogateway.QueryResult, error) {
	q, err := a.VideoProviderAdapter.Query(ctx, r)
	if q.Confirmation != nil && q.Status == videogateway.ProviderTaskSucceeded {
		q.Confirmation.Quantity = decimal.NewFromInt(6)
		q.Confirmation.Amount = q.Confirmation.UnitPrice.Mul(q.Confirmation.Quantity)
	}
	return q, err
}

type videoG5DisconnectedSubmit struct {
	videogateway.VideoProviderAdapter
	cancel       context.CancelFunc
	beforeCancel func() error
}

func (a videoG5DisconnectedSubmit) Submit(ctx context.Context, r videogateway.SubmitRequest) (videogateway.SubmitResult, error) {
	_, err := a.VideoProviderAdapter.Submit(ctx, r)
	if err != nil {
		return videogateway.SubmitResult{}, err
	}
	if a.beforeCancel != nil {
		if err := a.beforeCancel(); err != nil {
			return videogateway.SubmitResult{}, err
		}
	}
	a.cancel()
	return videogateway.SubmitResult{}, context.Canceled
}

func TestVideoG5UnknownMySQLDisconnectAfterSubmit(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"base", "cancel_intent", "input_loader_error"} {
		t.Run(mode, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if mode == "input_loader_error" {
				prepareVideoG5I2V(t, &f)
			}
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
			l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
			beforeCancel := func() error {
				if mode == "cancel_intent" {
					_, err := l.RequestCancellation(context.Background(), f.command.TaskID)
					return err
				}
				if mode == "input_loader_error" {
					l.referenceLoader = func(context.Context, model.AIGatewayInputAsset) (*videogateway.NormalizedReferenceImage, error) {
						return nil, errors.New("合成输入读取故障")
					}
				}
				return nil
			}
			g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: l, Provider: videoG5DisconnectedSubmit{VideoProviderAdapter: a, cancel: cancel, beforeCancel: beforeCancel}, Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess))})
			if _, err := g.Submit(ctx, f.command.TaskID); err == nil {
				t.Fatal("断连应返回错误")
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || task.Status != model.AIImageTaskPendingReconcile || task.BillingStatus != model.AIBillingSettlementPending {
				t.Fatalf("请求上下文结束不能丢掉待核对事实: %v", err)
			}
			if _, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner); err != nil {
				t.Fatal(err)
			}
			if a.SubmitCalls() != 1 {
				t.Fatal("补记不能重新Submit")
			}
		})
	}
}

func TestVideoG5UnknownMySQLCancelledCallbackWithoutCost(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, _, _ := videoG5CancellationFixture(t, db, model.AIVideoOperationTextToVideo, videogateway.FakeVideoSuccess)
	l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil)
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	c := videogateway.VerifiedCallback{ProviderCode: *task.ProviderCode, ProviderTaskID: *task.ProviderTaskID, ExternalEventID: f.command.RequestID + "_cancelled", BodySHA256: strings.Repeat("e", 64), Status: videogateway.ProviderTaskCancelled}
	if _, err := l.RecordCallback(context.Background(), f.command.TaskID, c); err != nil {
		t.Fatal(err)
	}
	after, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil || after.Status != model.AIImageTaskCancelled || after.BillingStatus != model.AIBillingSettlementPending {
		t.Fatal("缺少成本证明的取消回调必须安排核对")
	}
	if _, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
}

func TestVideoG5UnknownMySQLUsageMismatchPreservesBothFacts(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, g0, a := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, videogateway.FakeVideoSuccess)
	_ = g0
	g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader), Provider: videoG5UsageMismatchAdapter{a}, Probe: videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)), Labeler: videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1"), Store: videogateway.NewFakeVideoObjectStore()})
	for i := 0; i < 2; i++ {
		if _, err := g.Poll(context.Background(), f.command.TaskID); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = g.FetchAndFinalize(context.Background(), f.command.TaskID)
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil || task.Status != model.AIImageTaskPendingReconcile || task.BillingStatus != model.AIBillingSettlementPending {
		t.Fatalf("规格冲突必须先于执行成功进入待核对: %v", err)
	}
	var asset model.AIImageAsset
	if err := db.Where("task_id=? AND asset_role='content'", task.ID).First(&asset).Error; err != nil {
		t.Fatal(err)
	}
	if asset.DurationSeconds == nil || asset.DurationSeconds.String() != "5" || asset.LifecycleState != model.AIImageAssetTemporary {
		t.Fatal("实际5秒媒体必须保留且不交付")
	}
	facts, err := repository.NewVideoUsageRepository(db).ListForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || len(facts) != 2 {
		t.Fatalf("只保留Provider确认，不得猜测销售: %v", err)
	}
	for _, item := range facts {
		if item.Quantity.String() != "6" {
			t.Fatal("Provider的6秒确认不能改成5秒")
		}
	}
	job, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || job.OriginErrorCode != "facts_conflict" {
		t.Fatalf("冲突应留下对应补偿: %v", err)
	}
	assertVideoG5ReleaseStillHeld(t, f)
	if a.SubmitCalls() != 1 {
		t.Fatal("计量冲突不能重新Submit")
	}
}
