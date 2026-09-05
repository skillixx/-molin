package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG7OutboxFinancialReplayMySQL 真实财务事实不因运输状态改变而失效，重放也不能重新扣费或退款。
func TestVideoG7OutboxFinancialReplayMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, ending := range []string{"settled", "released", "adjusted"} {
			t.Run(operation+"/"+ending, func(t *testing.T) {
				f := newVideoG5ReservationFixture(t, db, "10")
				if operation == model.AIVideoOperationImageToVideo {
					prepareVideoG5I2V(t, &f)
				}
				if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
					t.Fatal(err)
				}
				var adjustment *VideoAdjustmentCommand
				if ending == "settled" {
					runVideoG5ReadyFixture(t, f)
					if _, err := f.service.SettleReady(ctx, f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
					if _, err := f.service.DeliverReady(ctx, f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
					if ending == "adjusted" {
						checker := f.owner.UserID + 900000
						if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", checker).Error; err != nil {
							t.Fatal(err)
						}
						cmd := VideoAdjustmentCommand{Direction: "credit", Reason: "billing_correction", Amount: f.quote.QuotedAmount, MakerID: f.owner.UserID, CheckerID: checker, SequenceNo: 1}
						adjustment = &cmd
						if _, err := f.service.ApplyAdjustment(ctx, f.command.TaskID, f.owner, cmd); err != nil {
							t.Fatal(err)
						}
					}
				}
				verify := func() {
					before := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
					if ending == "settled" {
						if r, err := f.service.SettleReady(ctx, f.command.TaskID, f.owner); err != nil || !r.Existing {
							t.Fatalf("运输状态不能破坏结算重放: %v", err)
						}
						if r, err := f.service.DeliverReady(ctx, f.command.TaskID, f.owner); err != nil || !r.Existing {
							t.Fatalf("运输状态不能破坏交付重放: %v", err)
						}
					} else {
						if r, err := f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner); err != nil || !r.Existing {
							t.Fatalf("运输状态不能破坏退款重放: %v", err)
						}
					}
					if adjustment != nil {
						if r, err := f.service.ApplyAdjustment(ctx, f.command.TaskID, f.owner, *adjustment); err != nil || !r.Existing {
							t.Fatalf("运输状态不能破坏调整重放: %v", err)
						}
					}
					if r, err := NewVideoReconciliationService(db).Reconcile(ctx, f.command.TaskID, f.owner); err != nil || !r.Passed || len(r.Checks) != 17 || len(r.Differences) != 0 {
						t.Fatalf("运输变化后必须仍然17项零差异: %+v %v", r, err)
					}
					if !bytes.Equal(before, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
						t.Fatal("财务重放/查询不能修改八表事实或重排事件")
					}
				}
				verify()
				repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id=?", f.command.RequestID))
				now := time.Now().UTC().Truncate(time.Second).Add(10 * time.Second)
				published := 0
				for published < 8 {
					events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
					if err != nil {
						t.Fatal(err)
					}
					if len(events) == 0 {
						break
					}
					e := events[0]
					verify()
					if err := repo.MarkRetry(ctx, e.ID, *e.LockedAt, now, "publish_failed", false); err != nil {
						t.Fatal(err)
					}
					verify()
					events, err = repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
					if err != nil || len(events) != 1 || events[0].ID != e.ID {
						t.Fatalf("重试应重领同一事件: %v", err)
					}
					e = events[0]
					if err := repo.MarkRetry(ctx, e.ID, *e.LockedAt, now, "publish_failed", true); err != nil {
						t.Fatal(err)
					}
					verify()
					if err := repo.RequeueDead(ctx, e.EventID, now); err != nil {
						t.Fatal(err)
					}
					verify()
					events, err = repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
					if err != nil || len(events) != 1 || events[0].ID != e.ID {
						t.Fatalf("死信重排应重领同一事件: %v", err)
					}
					e = events[0]
					if err := repo.MarkPublished(ctx, e.ID, *e.LockedAt, now); err != nil {
						t.Fatal(err)
					}
					verify()
					published++
				}
				want := 3
				if adjustment != nil {
					want = 4
				}
				if published != want {
					t.Fatalf("应验证全部%d个事件，实际%d", want, published)
				}
			})
		}
	}
}

// TestVideoG7OutboxBeforeFinancialTerminalMySQL 首次财务终结也必须容忍原预占事件已被发布器认领。
func TestVideoG7OutboxBeforeFinancialTerminalMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, ending := range []string{"settled", "released"} {
		t.Run(ending, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
				t.Fatal(err)
			}
			repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id=?", f.command.RequestID))
			now := time.Now().UTC().Add(time.Second)
			events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
			if err != nil || len(events) != 1 {
				t.Fatalf("预占事件未领取: %v", err)
			}
			if ending == "settled" {
				runVideoG5ReadyFixture(t, f)
				if _, err := f.service.SettleReady(ctx, f.command.TaskID, f.owner); err != nil {
					t.Fatalf("领取不能阻断首次结算: %v", err)
				}
				if _, err := f.service.DeliverReady(ctx, f.command.TaskID, f.owner); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner); err != nil {
				t.Fatalf("领取不能阻断首次退款: %v", err)
			}
			if r, err := NewVideoReconciliationService(db).Reconcile(ctx, f.command.TaskID, f.owner); err != nil || !r.Passed {
				t.Fatalf("首次终结必须零差异: %+v %v", r, err)
			}
		})
	}
}
