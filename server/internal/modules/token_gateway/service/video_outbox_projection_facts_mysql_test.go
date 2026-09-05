package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// TestVideoG7OutboxProjectionFabricatedFactsMySQL 正确格式、摘要ID和金额不能替代实际结算、退款或补偿依据。
func TestVideoG7OutboxProjectionFabricatedFactsMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, item := range []struct {
		kind, status, amount string
		release              bool
	}{
		{"video_billing_released", "released", "0.50000000", false},
		{"video_delivery_rejected", "rejected", "0.00000000", false},
		{"video_settlement_pending", "settlement_pending", "0.50000000", false},
		{"video_compensation_required", "pending", "0.50000000", false},
		{"video_billing_settled", "settled", "0.00000000", true},
		{"video_delivery_available", "available", "0.00000000", true},
	} {
		t.Run(item.kind, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
				t.Fatal(err)
			}
			if item.release {
				if _, err := f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner); err != nil {
					t.Fatal(err)
				}
			}
			repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id=?", f.command.RequestID))
			now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
			for i := 0; i < 3; i++ {
				events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
				if err != nil {
					t.Fatal(err)
				}
				if len(events) == 0 {
					break
				}
				if err := repo.MarkPublished(ctx, events[0].ID, *events[0].LockedAt, now); err != nil {
					t.Fatal(err)
				}
			}
			payload, _ := json.Marshal(map[string]interface{}{"request_id": f.command.RequestID, "status": item.status, "amount": item.amount, "currency": "CNY", "operation": "text_to_video", "version": 1})
			fake := model.AIOutboxEvent{EventID: "vg5_" + videoBillingDigest(f.command.RequestID+":"+item.kind), AggregateType: "video_request", AggregateID: f.command.RequestID, EventType: item.kind, PayloadJSON: payload, Status: model.AIOutboxPending, NextRetryAt: now}
			if err := db.Create(&fake).Error; err != nil {
				t.Fatalf("反例必须真实插入共享表: %v", err)
			}
			events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
			if err != nil || len(events) != 1 || events[0].ID != fake.ID {
				t.Fatal("未实际领取伪造后序事件")
			}
			if message, err := NewVideoOutboxProjector(db).Project(ctx, events[0]); err == nil || message != (video.TaskMessage{}) {
				t.Fatal("无原业务依据的事件不得生成唤醒消息")
			}
		})
	}
}

// TestVideoG7OutboxProjectionRecoveryAndAdjustmentMySQL 覆盖原P/C和独立调整，不把当前终态误当作历史事件失效。
func TestVideoG7OutboxProjectionRecoveryAndAdjustmentMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	t.Run("pending_reconcile", func(t *testing.T) {
		f, g, provider := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, video.FakeVideoResultUnknown)
		if _, err := g.Poll(ctx, f.command.TaskID); err != nil {
			t.Fatal(err)
		}
		_, _ = g.Poll(ctx, f.command.TaskID)
		projectVideoG7OriginalEvents(t, f, 3)
		if provider.SubmitCalls() != 1 {
			t.Fatal("投影不能重提Provider")
		}
	})
	t.Run("adjustment", func(t *testing.T) {
		f, cmd := videoG5ClosedAdjustmentFixture(t, db, "10")
		if _, err := f.service.ApplyAdjustment(ctx, f.command.TaskID, f.owner, cmd); err != nil {
			t.Fatal(err)
		}
		projectVideoG7OriginalEvents(t, f, 4)
	})
}

func projectVideoG7OriginalEvents(t *testing.T, f videoG5ReservationFixture, want int) {
	t.Helper()
	ctx := context.Background()
	repo := repository.NewVideoOutboxRepository(f.db.Where("aggregate_id=?", f.command.RequestID))
	now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	for i := 0; i < want; i++ {
		events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
		if err != nil || len(events) != 1 {
			t.Fatalf("原事件未领取: %v", err)
		}
		message, err := NewVideoOutboxProjector(f.db).Project(ctx, events[0])
		if err != nil || message.TaskID != f.command.TaskID || message.RequestID != f.command.RequestID {
			t.Fatalf("原业务事实投影失败: %v", err)
		}
		if err := repo.MarkPublished(ctx, events[0].ID, *events[0].LockedAt, now); err != nil {
			t.Fatal(err)
		}
	}
	events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
	if err != nil || len(events) != 0 {
		t.Fatal("必须遍历全部原事件")
	}
}

// TestVideoG7OutboxProjectionRunningMySQL 请求粗粒度running必须与多个合法任务执行阶段兼容。
func TestVideoG7OutboxProjectionRunningMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	f, g, provider := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, video.FakeVideoSuccess)
	repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id=?", f.command.RequestID))
	now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
	if err != nil || len(events) != 1 {
		t.Fatal("领取失败")
	}
	for i := 0; i < 3; i++ {
		message, err := NewVideoOutboxProjector(db).Project(ctx, events[0])
		if err != nil || message.TaskID != f.command.TaskID {
			t.Fatalf("执行中投影应沿原映射: %v", err)
		}
		if i < 2 {
			if _, err := g.Poll(ctx, f.command.TaskID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if provider.SubmitCalls() != 1 {
		t.Fatal("执行中投影不能重提")
	}
}
