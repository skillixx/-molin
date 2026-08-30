package service

import (
	"context"
	"errors"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
)

// 对账必须观察请求下的财务事件全集，不能预先过滤坏类型而把矛盾事实隐藏。
func TestVideoG5ReconciliationMySQLRejectsForeignFinancialOutbox(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, op := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, terminal := range []string{"settled", "released"} {
			for _, eventKind := range []string{"held", "final"} {
				for _, mutation := range []string{"additional", "replaced"} {
					t.Run(op+"/"+terminal+"/"+eventKind+"/"+mutation, func(t *testing.T) {
						f := newVideoG5ReservationFixture(t, db, "10")
						if op == model.AIVideoOperationImageToVideo {
							prepareVideoG5I2V(t, &f)
						}
						if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
							t.Fatal(err)
						}
						if terminal == "settled" {
							runVideoG5ReadyFixture(t, f)
							if _, err := f.service.SettleReady(ctx, f.command.TaskID, f.owner); err != nil {
								t.Fatal(err)
							}
							if _, err := f.service.DeliverReady(ctx, f.command.TaskID, f.owner); err != nil {
								t.Fatal(err)
							}
						} else if _, err := f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner); err != nil {
							t.Fatal(err)
						}
						before := readVideoGoldenWallet(t, f)
						report, err := NewVideoReconciliationService(db).Reconcile(ctx, f.command.TaskID, f.owner)
						if err != nil || !report.Passed {
							t.Fatalf("反例注入前应完全闭合: %+v %v", report, err)
						}
						eventType := "video_billing_held"
						if eventKind == "final" {
							eventType = "video_billing_" + terminal
						}
						var event model.AIOutboxEvent
						if err := db.Where("aggregate_id=? AND event_type=?", f.command.RequestID, eventType).First(&event).Error; err != nil {
							t.Fatal(err)
						}
						// 只污染该临时请求的聚合归属，正文和金额仍合法，排除其他失败原因。
						if mutation == "additional" {
							event.ID = 0
							event.EventID += "_foreign"
							event.AggregateType = "foreign_request"
							if err := db.Create(&event).Error; err != nil {
								t.Fatal(err)
							}
						} else if err := db.Model(&event).Update("aggregate_type", "foreign_request").Error; err != nil {
							t.Fatal(err)
						}
						report, err = NewVideoReconciliationService(db).Reconcile(ctx, f.command.TaskID, f.owner)
						if err != nil || report.Passed {
							t.Errorf("不能忽略错误聚合类型的财务事件: %+v %v", report, err)
						}
						if terminal == "settled" {
							_, err = f.service.SettleReady(ctx, f.command.TaskID, f.owner)
						} else {
							_, err = f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner)
						}
						if !errors.Is(err, ErrVideoBillingState) {
							t.Errorf("财务终态重放必须失败关闭: %v", err)
						}
						after := readVideoGoldenWallet(t, f)
						if before.Version != after.Version || !before.BalanceAmount.Equal(after.BalanceAmount) || !before.FrozenAmount.Equal(after.FrozenAmount) {
							t.Fatal("错误Outbox不能触发资金修正")
						}
					})
				}
			}
		}
	}
}
