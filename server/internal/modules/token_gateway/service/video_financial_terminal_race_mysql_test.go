package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 同一持久化请求上同时争抢相反财务入口；合法胜方由执行事实决定，不能由调度顺序改变。
func TestVideoG5SettleMySQLOppositeFinancialTerminalRace(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, op := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, outcome := range []string{"succeeded", "failed", "queued"} {
			t.Run(op+"/"+outcome, func(t *testing.T) {
				var f videoG5ReservationFixture
				var adapter *videogateway.FakeAsyncVideoAdapter
				if outcome == "failed" {
					var gateway *videogateway.VideoGateway
					f, gateway, adapter = videoG5CancellationFixture(t, db, op, videogateway.FakeVideoExplicitFailure)
					for i := 0; i < 2; i++ {
						// 明确失败是此夹具预期的Provider结果；其他错误不能当作有效退款依据。
						if _, err := gateway.Poll(ctx, f.command.TaskID); err != nil && !errors.Is(err, videogateway.ErrProviderExplicitFailure) {
							t.Fatal(err)
						}
					}
					failed, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.command.TaskID, f.owner)
					if err != nil || failed.Status != model.AIImageTaskFailed {
						t.Fatalf("财务竞争前必须已落库明确失败: %v", err)
					}
				} else {
					f = newVideoG5ReservationFixture(t, db, "10")
					if op == model.AIVideoOperationImageToVideo {
						prepareVideoG5I2V(t, &f)
					}
					if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
						t.Fatal(err)
					}
					if outcome == "succeeded" {
						_, adapter = runVideoG5ReadyFixture(t, f)
					} else {
						if _, err := repository.NewVideoTaskRepository(db).TransitionExecution(ctx, repository.VideoStateTransition{TaskPublicID: f.command.TaskID, Owner: f.owner, ExpectedVersion: 1, ToStatus: model.AIImageTaskQueued, Progress: 10, EventID: f.command.RequestID + "_queued", Source: "worker", Now: time.Now()}); err != nil {
							t.Fatal(err)
						}
					}
				}
				start := make(chan struct{})
				var wg sync.WaitGroup
				var applied, replayed, rejected atomic.Int64
				for i := 0; i < 100; i++ {
					settle := i%2 == 0
					wg.Add(1)
					go func() {
						defer wg.Done()
						<-start
						var result *VideoFinancialResult
						var err error
						if settle {
							result, err = f.service.SettleReady(ctx, f.command.TaskID, f.owner)
						} else if outcome == "queued" {
							result, err = f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner)
						} else {
							result, err = f.service.ReleaseUnserviceable(ctx, f.command.TaskID, f.owner)
						}
						legal := settle == (outcome == "succeeded")
						if !legal {
							if !errors.Is(err, ErrVideoBillingState) {
								t.Errorf("相反入口必须以财务状态冲突拒绝，不能误认数据库故障: %v", err)
							} else {
								rejected.Add(1)
							}
							return
						}
						if err != nil || result == nil {
							t.Errorf("合法财务入口被竞争阻断: %v", err)
							return
						}
						if result.Existing {
							replayed.Add(1)
						} else {
							applied.Add(1)
						}
					}()
				}
				close(start)
				wg.Wait()
				if applied.Load() != 1 || replayed.Load() != 49 || rejected.Load() != 50 {
					t.Fatalf("竞争必须只有一次合法写入: applied=%d replay=%d rejected=%d", applied.Load(), replayed.Load(), rejected.Load())
				}
				wantBilling, wantFinal, wantBalance, wantConsume := model.AIBillingReleased, "video_billing_released", decimal.NewFromInt(10), int64(0)
				if outcome == "succeeded" {
					wantBilling, wantFinal, wantBalance, wantConsume = model.AIBillingSettled, "video_billing_settled", decimal.NewFromInt(10).Sub(f.quote.QuotedAmount), 1
					if _, err := f.service.DeliverReady(ctx, f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
				}
				task, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || task.BillingStatus != wantBilling {
					t.Fatalf("持久化财务终态错误: %v", err)
				}
				wallet := readVideoGoldenWallet(t, f)
				if !wallet.BalanceAmount.Equal(wantBalance) || !wallet.FrozenAmount.IsZero() {
					t.Fatal("相反入口竞争破坏资金守恒")
				}
				for kind, want := range map[string]int64{"freeze": 1, "unfreeze": 1, "consume": wantConsume} {
					var count int64
					if err := db.Model(&billingmodel.WalletTransaction{}).Where("user_id=? AND type=?", f.owner.UserID, kind).Count(&count).Error; err != nil || count != want {
						t.Fatalf("流水%s必须唯一且金额由对账复核: count=%d err=%v", kind, count, err)
					}
				}
				var finals []model.AIOutboxEvent
				if err := db.Where("aggregate_id=? AND event_type IN ?", f.command.RequestID, []string{"video_billing_settled", "video_billing_released"}).Find(&finals).Error; err != nil || len(finals) != 1 || finals[0].EventType != wantFinal {
					t.Fatalf("相反终态Outbox不互斥: %v", err)
				}
				report, err := NewVideoReconciliationService(db).Reconcile(ctx, f.command.TaskID, f.owner)
				if err != nil || !report.Passed || len(report.Checks) != 17 || len(report.Differences) != 0 {
					t.Fatalf("竞争后必须逐项零差异: %+v %v", report, err)
				}
				if outcome != "queued" && adapter.SubmitCalls() != 1 {
					t.Fatal("财务竞争不得重调Provider")
				}
			})
		}
	}
}
