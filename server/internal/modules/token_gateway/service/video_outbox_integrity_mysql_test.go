package service

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// TestVideoG7OutboxIdentityMySQL 大小写不敏感查询不能代替取回后的精确身份核验。
func TestVideoG7OutboxIdentityMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, field := range []string{"aggregate_id", "event_type"} {
		t.Run(field, func(t *testing.T) {
			f, cmd := videoG5ClosedAdjustmentFixture(t, db, "10")
			if _, err := f.service.ApplyAdjustment(ctx, f.command.TaskID, f.owner, cmd); err != nil {
				t.Fatal(err)
			}
			kind := "video_billing_held"
			if field == "event_type" {
				kind = "video_adjustment_recorded"
			}
			value := strings.ToUpper(f.command.RequestID)
			if field == "event_type" {
				value = "VIDEO_adjustment_recorded"
			}
			r := db.Model(&model.AIOutboxEvent{}).Where("aggregate_id=? AND event_type=?", f.command.RequestID, kind).Update(field, value)
			if r.Error != nil || r.RowsAffected != 1 {
				t.Fatalf("未实际写入反例: %v", r.Error)
			}
			before := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
			if report, err := NewVideoReconciliationService(db).Reconcile(ctx, f.command.TaskID, f.owner); err == nil && report.Passed {
				t.Fatal("大小写漂移身份不得通过财务对账")
			}
			if !bytes.Equal(before, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
				t.Fatal("对账不得修写损坏事实")
			}
		})
	}
	t.Run("recovery_event_id", func(t *testing.T) {
		f, g, _ := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, videogateway.FakeVideoResultUnknown)
		if _, err := g.Poll(ctx, f.command.TaskID); err != nil {
			t.Fatal(err)
		}
		_, _ = g.Poll(ctx, f.command.TaskID)
		var event model.AIOutboxEvent
		if err := db.Where("aggregate_id=? AND event_type='video_compensation_required'", f.command.RequestID).Take(&event).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIOutboxEvent{}).Where("id=?", event.ID).Update("event_id", strings.ToUpper(event.EventID)).Error; err != nil {
			t.Fatal(err)
		}
		if result, err := f.service.ReconcileExecution(ctx, f.command.TaskID, f.owner); err == nil {
			t.Fatalf("恢复事件ID大小写漂移不得被当原事件: %s", result)
		}
	})
}

// TestVideoG7OutboxRecoveryTransportMySQL 已发布的P/C事实仍须支持结果未知恢复，不能新增事件或释放输入。
func TestVideoG7OutboxRecoveryTransportMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	f, g, provider := videoG5CancellationFixture(t, db, model.AIVideoOperationImageToVideo, videogateway.FakeVideoResultUnknown)
	if _, err := g.Poll(ctx, f.command.TaskID); err != nil {
		t.Fatal(err)
	}
	_, _ = g.Poll(ctx, f.command.TaskID)
	repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id=?", f.command.RequestID))
	now := time.Now().UTC().Add(10 * time.Second)
	for i := 0; i < 3; i++ {
		events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
		if err != nil || len(events) != 1 {
			t.Fatalf("应领取H/P/C原事件: %v", err)
		}
		for _, stage := range []string{"publishing", "published"} {
			if stage == "published" {
				if err := repo.MarkPublished(ctx, events[0].ID, *events[0].LockedAt, now); err != nil {
					t.Fatal(err)
				}
			}
			before := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
			if result, err := f.service.ReconcileExecution(ctx, f.command.TaskID, f.owner); err != nil || result != "existing_active" {
				t.Fatalf("运输状态变化后应沿原恢复事实: %s %v", result, err)
			}
			if !bytes.Equal(before, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
				t.Fatal("恢复重放不得新增财务或事件")
			}
		}
	}
	bindings, err := repository.NewVideoTaskInputRepository(db).ListForOwner(ctx, f.command.TaskID, f.owner)
	if err != nil || len(bindings) != 1 || bindings[0].LeaseReleasedAt != nil || provider.SubmitCalls() != 1 {
		t.Fatal("结果未知必须保留输入且不能重提Provider")
	}
}

// TestVideoG7OutboxMalformedTransportMySQL 放开合法运输四态不能放过矛盾元数据或损坏金额。
func TestVideoG7OutboxMalformedTransportMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	cases := []struct {
		name   string
		change map[string]interface{}
	}{
		{"publishing_without_lease", map[string]interface{}{"status": "publishing"}},
		{"published_without_time", map[string]interface{}{"status": "published"}},
		{"published_with_lease", map[string]interface{}{"status": "published", "processed_at": now, "locked_at": now}},
		{"pending_with_time", map[string]interface{}{"processed_at": now}},
		{"dead_with_time", map[string]interface{}{"status": "dead", "retry_count": 1, "last_error_class": "publish_failed", "processed_at": now, "locked_at": now}},
		{"dead_without_attempt", map[string]interface{}{"status": "dead", "last_error_class": "publish_failed", "locked_at": now}},
		{"dead_without_lease", map[string]interface{}{"status": "dead", "retry_count": 1, "last_error_class": "publish_failed"}},
		{"uppercase_state", map[string]interface{}{"status": "PUBLISHED", "processed_at": now}},
		{"published_wrong_amount", map[string]interface{}{"status": "published", "processed_at": now, "payload_json": gorm.Expr("JSON_SET(payload_json,'$.amount','999.00000000')")}},
		{"dead_wrong_currency", map[string]interface{}{"status": "dead", "retry_count": 1, "last_error_class": "publish_failed", "locked_at": now, "payload_json": gorm.Expr("JSON_SET(payload_json,'$.currency','USD')")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner); err != nil {
				t.Fatal(err)
			}
			r := db.Model(&model.AIOutboxEvent{}).Where("aggregate_id=? AND event_type='video_billing_held'", f.command.RequestID).Updates(c.change)
			if r.Error != nil || r.RowsAffected != 1 {
				t.Fatalf("反例未写入: %v", r.Error)
			}
			before := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
			if report, err := NewVideoReconciliationService(db).Reconcile(ctx, f.command.TaskID, f.owner); err == nil && report.Passed {
				t.Fatal("运输状态或财务载荷损坏必须拒绝")
			}
			if !bytes.Equal(before, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
				t.Fatal("失败对账不得修改财务或事件")
			}
		})
	}
}
