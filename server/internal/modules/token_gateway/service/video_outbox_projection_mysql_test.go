package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// TestVideoG7OutboxProjectionMySQL 消息从原账本重建，不透传财务载荷，也不因投影改变任何业务事实。
func TestVideoG7OutboxProjectionMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, op := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, phase := range []string{"held", "released", "settled"} {
			t.Run(op+"/"+phase, func(t *testing.T) {
				f := newVideoG5ReservationFixture(t, db, "10")
				inputID := ""
				if op == model.AIVideoOperationImageToVideo {
					inputID = prepareVideoG5I2V(t, &f).PublicID
				}
				if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
					t.Fatal(err)
				}
				if phase == "released" {
					if _, err := f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
				}
				if phase == "settled" {
					runVideoG5ReadyFixture(t, f)
					if _, err := f.service.SettleReady(ctx, f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
					if _, err := f.service.DeliverReady(ctx, f.command.TaskID, f.owner); err != nil {
						t.Fatal(err)
					}
				}
				repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id=?", f.command.RequestID))
				projector := NewVideoOutboxProjector(db)
				now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
				count := 0
				for count < 4 {
					events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
					if err != nil {
						t.Fatal(err)
					}
					if len(events) == 0 {
						break
					}
					e := events[0]
					before := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
					message, err := projector.Project(ctx, e)
					if err != nil || message != (video.TaskMessage{TaskID: f.command.TaskID, RequestID: f.command.RequestID, InputAssetID: inputID, Attempt: 0}) {
						t.Fatalf("原事实投影失败: %v", err)
					}
					body, err := video.EncodeTaskMessage(message)
					if err != nil {
						t.Fatal(err)
					}
					var fields map[string]json.RawMessage
					if json.Unmarshal(body, &fields) != nil || len(fields) != 3+boolToVideoG7Int(inputID != "") || bytes.Contains(body, []byte(f.command.Prompt)) {
						t.Fatal("消息必须只含三/四个低敏引用键")
					}
					if !bytes.Equal(before, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
						t.Fatal("投影不得修改业务/财务或Outbox事实")
					}
					if err := repo.MarkPublished(ctx, e.ID, *e.LockedAt, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
					if msg, err := projector.Project(ctx, e); err == nil || msg != (video.TaskMessage{}) {
						t.Fatal("已失效租约不得再次投影")
					}
					count++
				}
				want := 3
				if phase == "held" {
					want = 1
				}
				if count != want {
					t.Fatalf("未投影全部%d条原事件", want)
				}
			})
		}
	}
}

func boolToVideoG7Int(value bool) int {
	if value {
		return 1
	}
	return 0
}

// TestVideoG7OutboxProjectionLeaseMySQL 伪造快照、过期及重领旧令牌失败关闭，运输重试计数不能变成业务attempt。
func TestVideoG7OutboxProjectionLeaseMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id=?", f.command.RequestID))
	now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
	if err != nil || len(events) != 1 {
		t.Fatal("领取失败")
	}
	e := events[0]
	projector := NewVideoOutboxProjector(db)
	for _, mutate := range []func(*model.AIOutboxEvent){func(x *model.AIOutboxEvent) { x.ID++ }, func(x *model.AIOutboxEvent) { x.EventID += "x" }, func(x *model.AIOutboxEvent) { x.AggregateID += "x" }, func(x *model.AIOutboxEvent) { x.AggregateType = "ai_request" }, func(x *model.AIOutboxEvent) { x.EventType = "video_unknown" }, func(x *model.AIOutboxEvent) {
		x.PayloadJSON = json.RawMessage(`{"prompt":"禁止进入消息的合成内容"}`)
	}, func(x *model.AIOutboxEvent) { x.LockedAt = nil }} {
		bad := e
		mutate(&bad)
		if message, err := projector.Project(ctx, bad); err == nil || message != (video.TaskMessage{}) {
			t.Fatal("伪造Outbox快照不得生成消息")
		}
	}
	if err := repo.MarkRetry(ctx, e.ID, *e.LockedAt, now, "publish_failed", false); err != nil {
		t.Fatal(err)
	}
	events, err = repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
	if err != nil || len(events) != 1 {
		t.Fatal("重领失败")
	}
	if message, err := projector.Project(ctx, e); err == nil || message != (video.TaskMessage{}) {
		t.Fatal("旧租约不得投影新工作")
	}
	if message, err := projector.Project(ctx, events[0]); err != nil || message.Attempt != 0 {
		t.Fatalf("发布重试仍从业务attempt零开始: %v", err)
	}
	old := time.Now().UTC().Add(-3 * time.Minute).Truncate(time.Second)
	if err := db.Model(&model.AIOutboxEvent{}).Where("id=?", e.ID).Update("locked_at", old).Error; err != nil {
		t.Fatal(err)
	}
	expired := events[0]
	expired.LockedAt = &old
	if message, err := projector.Project(ctx, expired); err == nil || message != (video.TaskMessage{}) {
		t.Fatal("过期持有者不得派发")
	}
}
