package service

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG5ReconciliationMySQLRefreshesReadClock 读取过程可跨过有效期，不能沿用入口旧时钟返回可读。
func TestVideoG5ReconciliationMySQLRefreshesReadClock(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	runVideoG5ReadyFixture(t, f)
	if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DeliverReady(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	var asset model.AIImageAsset
	if err := db.Where("request_id=? AND asset_role='content'", f.command.RequestID).First(&asset).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC()
	calls := 0
	ledger := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil)
	ledger.now = func() time.Time {
		calls++
		if calls == 1 {
			return start
		}
		return asset.ExpiresAt.Add(time.Second)
	}
	if _, err := ledger.Load(context.Background(), f.command.TaskID); err == nil {
		t.Fatal("读取结束时已过期不能沿用旧时钟通过")
	}
}

// TestVideoG5DeliveryMySQLLateExpiryRollback 最后复核后跨过媒体或租约期限，也必须撤销available和completed。
func TestVideoG5DeliveryMySQLLateExpiryRollback(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"asset_expiry", "lease_expiry"} {
		t.Run(mode, func(t *testing.T) {
			f := videoG5PendingFixture(t, db)
			now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
			f.service.now = func() time.Time { return now }
			f.service.fault = func(at string) error {
				if at == "delivery_checked" {
					if mode == "asset_expiry" {
						now = now.Add(25 * time.Hour)
					} else {
						now = now.Add(repository.VideoCompensationLeaseDuration + time.Second)
					}
				}
				return nil
			}
			worker, err := NewVideoCompensationWorker(f.service, "deadline-publication")
			if err != nil {
				t.Fatal(err)
			}
			if result, err := worker.RunOne(context.Background(), f.command.RequestID); err == nil && result.Status == "completed" {
				t.Fatal("过期事务不得提交交付")
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || task.DeliveryStatus != model.AIDeliveryPending {
				t.Fatalf("交付状态必须回滚: %v", err)
			}
			var count int64
			if err := db.Model(&model.AIImageAsset{}).Where("request_id=? AND lifecycle_state='available'", f.command.RequestID).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("过期后不得留available")
			}
			job, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
			if err != nil || job.CompletedAt != nil || job.DeliveryPreparedAt != nil {
				t.Fatalf("完成和标记必须回滚: %v", err)
			}
		})
	}
}

// TestVideoG5DeliveryMySQLChildHoldBlocksRead 派生资产保全/争议也必须阻断根视频的后续读取。
func TestVideoG5DeliveryMySQLChildHoldBlocksRead(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	gateway, _ := runVideoG5ReadyFixture(t, f)
	if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DeliverReady(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIImageAsset{}).Where("request_id=? AND asset_role='thumbnail'", f.command.RequestID).Update("legal_hold", true).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.Query(context.Background(), f.command.TaskID); err == nil {
		t.Fatal("子资产保全不能被根资产available绕过")
	}
}

// TestVideoG5DeliveryMySQLLegacyLedgerCannotBypassGate G5身份来自数据库，不能换旧构造器绕过最终对账。
func TestVideoG5DeliveryMySQLLegacyLedgerCannotBypassGate(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	runVideoG5ReadyFixture(t, f)
	if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.DeliverReady(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	from, to := model.AIBillingSettled, model.AIBillingReleased
	if err := repository.NewVideoTaskEventRepository(db).Append(context.Background(), f.command.TaskID, f.owner, model.AIGatewayTaskEvent{EventID: f.command.RequestID + "_bad_history", EventType: "billing_status_changed", FromStatus: &from, ToStatus: &to, Source: "system", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	legacy := NewVideoRepositoryTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil)
	if _, err := legacy.Load(context.Background(), f.command.TaskID); err == nil {
		t.Fatal("旧Ledger不能读取需G5门禁的已交付任务")
	}
}

// TestVideoG5DeliveryMySQLPublicationFaults 撤销任何发布步骤，也必须撤销临时围栏、available和completed，不撤销已完成结算。
func TestVideoG5DeliveryMySQLPublicationFaults(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, point := range []string{"delivery_prepared", "delivery_outbox", "delivery_request", "delivery_content", "delivery_cover", "delivery_preview", "delivery_thumbnail", "delivery_moderation_copy", "delivery_derived", "delivery_completed", "delivery_checked"} {
		t.Run(point, func(t *testing.T) {
			f := videoG5PendingFixture(t, db)
			f.service.fault = func(at string) error {
				if at == point {
					return errors.New("合成发布故障")
				}
				return nil
			}
			worker, err := NewVideoCompensationWorker(f.service, "fault-publication")
			if err != nil {
				t.Fatal(err)
			}
			result, err := worker.RunOne(context.Background(), f.command.RequestID)
			if err != nil || result.Status != "retry" {
				t.Fatalf("发布故障应可重试: %+v %v", result, err)
			}
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || task.BillingStatus != model.AIBillingSettled || task.DeliveryStatus != model.AIDeliveryPending {
				t.Fatalf("财务必须保留但交付回滚: %v", err)
			}
			job, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
			if err != nil || job.CompletedAt != nil || job.DeliveryRequestVersion != nil || job.DeliveryPreparedAt != nil {
				t.Fatalf("发布标记和完成必须回滚: %v", err)
			}
			var count int64
			if err := db.Model(&model.AIImageAsset{}).Where("request_id=? AND lifecycle_state='available'", f.command.RequestID).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("不得遗留部分available")
			}
			f.service.fault = nil
			now := job.NextRetryAt
			f.service.now = func() time.Time { return now }
			result, err = worker.RunOne(context.Background(), f.command.RequestID)
			if err != nil || result.Status != "completed" {
				t.Fatalf("重试应原子闭合: %+v %v", result, err)
			}
		})
	}
}

// TestVideoG5DeliveryMySQLLegacyG4Compatibility 在相同一次性数据库复跑既有G4闭环，确认旧事实仍兼容。
func TestVideoG5DeliveryMySQLLegacyG4Compatibility(t *testing.T) {
	_ = openVideoG5MySQL(t)
	t.Setenv("MOLIN_VIDEO_G3_MYSQL_DSN", os.Getenv("MOLIN_VIDEO_G5_MYSQL_DSN"))
	TestVideoG4RepositoryLedgerMySQLRunsFakeT2VClosure(t)
}
