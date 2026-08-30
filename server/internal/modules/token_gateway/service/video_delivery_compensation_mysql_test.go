package service

import (
	"context"
	"errors"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG5DeliveryMySQLCompensationCompletesPublication 财务恢复、独立发布和completed最终闭合，不允许标记一半。
func TestVideoG5DeliveryMySQLCompensationCompletesPublication(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := videoG5PendingFixture(t, db)
	worker, err := NewVideoCompensationWorker(f.service, "publication-worker")
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.RunOne(context.Background(), f.command.RequestID)
	if err != nil || result.Status != "completed" {
		t.Fatalf("补偿应完成财务与交付: %+v %v", result, err)
	}
	report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
	if err != nil || !report.Passed {
		t.Fatalf("补偿后完整对账应通过: %+v %v", report, err)
	}
	again, err := worker.RunOne(context.Background(), f.command.RequestID)
	if err != nil || !again.Existing || again.Status != "completed" {
		t.Fatalf("已完成Worker重放应无副作用: %+v %v", again, err)
	}
}

// TestVideoG5DeliveryMySQLReadGateRechecksFacts 已发布后持久化事实变化不能绕过读取门禁。
func TestVideoG5DeliveryMySQLReadGateRechecksFacts(t *testing.T) {
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
	if err := db.Exec("UPDATE ai_outbox_events SET payload_json=JSON_SET(payload_json,'$.amount','999.00000000') WHERE aggregate_id=? AND event_type='video_billing_held'", f.command.RequestID).Error; err != nil {
		t.Fatal(err)
	}
	reader, err := gateway.ReadContent(context.Background(), f.command.TaskID, 0, 1)
	if reader != nil {
		reader.Close()
	}
	if err == nil {
		t.Fatal("对账已损坏时不得继续读取视频")
	}
}

// TestVideoG5DeliveryMySQLFailureBecomesCompensation 发布失败不撤销已结算事实，创建唯一交付补偿后安全恢复。
func TestVideoG5DeliveryMySQLFailureBecomesCompensation(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	_, adapter := runVideoG5ReadyFixture(t, f)
	if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	f.service.fault = func(at string) error {
		if at == "delivery_request" {
			return errors.New("合成交付故障")
		}
		return nil
	}
	if _, err := f.service.DeliverReady(context.Background(), f.command.TaskID, f.owner); err == nil {
		t.Fatal("交付注入应失败")
	}
	job, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || job.Status != "pending" {
		t.Fatalf("应有交付补偿: %v", err)
	}
	var assets int64
	if err := db.Model(&model.AIImageAsset{}).Where("request_id=? AND lifecycle_state='available'", f.command.RequestID).Count(&assets).Error; err != nil || assets != 0 {
		t.Fatal("发布失败必须回滚全部available")
	}
	f.service.fault = nil
	worker, err := NewVideoCompensationWorker(f.service, "delivery-recovery")
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.RunOne(context.Background(), f.command.RequestID)
	if err != nil || result.Status != "completed" {
		t.Fatalf("应补偿发布: %+v %v", result, err)
	}
	if adapter.SubmitCalls() != 1 {
		t.Fatal("补偿交付不得再次提交Provider")
	}
}
