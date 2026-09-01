package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

func enableVideoG6Budget(t *testing.T, f *videoG5ReservationFixture) {
	t.Helper()
	f.service.budget = NewVideoBudgetAdmission(repository.NewG4GovernanceRepository(f.db))
	limit := decimal.RequireFromString("10.00000000")
	policy := model.AIBudgetPolicy{ScopeType: "project", ScopeID: f.owner.ProjectID, Mode: model.AIBudgetHard, DailyLimit: &limit, MonthlyLimit: &limit, VersionNo: 1, UpdatedBy: f.owner.UserID}
	if err := f.db.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
}

func TestVideoG6BudgetLifecycleSettleReleaseAndRollbackMySQL(t *testing.T) {
	t.Run("成功结算同步settled金额", func(t *testing.T) {
		f := newVideoG5ReservationFixture(t, openVideoG6MySQL(t), "10")
		enableVideoG6Budget(t, &f)
		if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
			t.Fatal(err)
		}
		runVideoG5ReadyFixture(t, f)
		result, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner)
		if err != nil || result.BillingStatus != model.AIBillingSettled {
			t.Fatalf("结算失败: result=%+v err=%v", result, err)
		}
		var budget model.AIBudgetReservation
		if err := f.db.Where("request_id=?", f.command.RequestID).Take(&budget).Error; err != nil || budget.Status != model.AIBudgetSettled || budget.SettledAmount == nil || !budget.SettledAmount.Equal(f.quote.QuotedAmount) {
			t.Fatalf("预算settled未与钱包同步: %+v err=%v", budget, err)
		}
	})

	t.Run("明确Provider失败同步released", func(t *testing.T) {
		f := newVideoG5ReservationFixture(t, openVideoG6MySQL(t), "10")
		enableVideoG6Budget(t, &f)
		if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
			t.Fatal(err)
		}
		adapter := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoExplicitFailure)
		ledger := NewVideoBillingTaskLedger(f.db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
		gateway := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: ledger, Provider: adapter, Probe: videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)), Labeler: videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1"), Store: videogateway.NewFakeVideoObjectStore()})
		if _, err := gateway.Submit(context.Background(), f.command.TaskID); err != nil {
			t.Fatal(err)
		}
		for index := 0; index < 2; index++ {
			_, _ = gateway.Poll(context.Background(), f.command.TaskID)
		}
		result, err := f.service.ReleaseUnserviceable(context.Background(), f.command.TaskID, f.owner)
		if err != nil || result.BillingStatus != model.AIBillingReleased {
			t.Fatalf("明确失败释放错误: result=%+v err=%v", result, err)
		}
		var budget model.AIBudgetReservation
		if err := f.db.Where("request_id=?", f.command.RequestID).Take(&budget).Error; err != nil || budget.Status != model.AIBudgetReleased {
			t.Fatalf("预算released未与钱包同步: %+v err=%v", budget, err)
		}
	})

	t.Run("预算后置故障整笔回滚", func(t *testing.T) {
		f := newVideoG5ReservationFixture(t, openVideoG6MySQL(t), "10")
		enableVideoG6Budget(t, &f)
		if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
			t.Fatal(err)
		}
		runVideoG5ReadyFixture(t, f)
		f.service.fault = func(step string) error {
			if step == "settle_budget" {
				return errors.New("合成预算后置故障")
			}
			return nil
		}
		if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err == nil {
			t.Fatal("预算后置故障必须返回失败")
		}
		var budget model.AIBudgetReservation
		if err := f.db.Where("request_id=?", f.command.RequestID).Take(&budget).Error; err != nil || budget.Status != model.AIBudgetHeld {
			t.Fatalf("失败结算必须保留held预算: %+v err=%v", budget, err)
		}
		var wallet billingmodel.Wallet
		if err := f.db.Where("user_id=?", f.owner.UserID).Take(&wallet).Error; err != nil || !wallet.BalanceAmount.Equal(decimal.RequireFromString("9.50000000")) || !wallet.FrozenAmount.Equal(f.quote.QuotedAmount) {
			t.Fatalf("预算故障后钱包未回滚: %+v err=%v", wallet, err)
		}
	})
}
