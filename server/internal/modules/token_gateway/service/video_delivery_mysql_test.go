package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG5DeliveryMySQLPublishesOnceAfterReconciliation 已结算的六类媒体须经一致性检查和独立事务，100并发只交付一次。
func TestVideoG5DeliveryMySQLPublishesOnceAfterReconciliation(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, op := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(op, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if op == model.AIVideoOperationImageToVideo {
				prepareVideoG5I2V(t, &f)
			}
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			gateway, adapter := runVideoG5ReadyFixture(t, f)
			if _, err := f.service.DeliverReady(context.Background(), f.command.TaskID, f.owner); err == nil {
				t.Fatal("未结算不能交付")
			}
			if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			var applied, replayed atomic.Int64
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					result, err := f.service.DeliverReady(context.Background(), f.command.TaskID, f.owner)
					if err != nil {
						t.Errorf("交付失败: %v", err)
						return
					}
					if result.Existing {
						replayed.Add(1)
					} else {
						applied.Add(1)
					}
				}()
			}
			wg.Wait()
			if applied.Load() != 1 || replayed.Load() != 99 {
				t.Fatalf("交付不唯一: %d/%d", applied.Load(), replayed.Load())
			}
			report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), f.command.TaskID, f.owner)
			if err != nil || !report.Passed || len(report.Checks) != 17 || len(report.Differences) != 0 {
				t.Fatalf("完整请求检查未通过: %+v %v", report, err)
			}
			ready, err := gateway.Query(context.Background(), f.command.TaskID)
			if err != nil || ready.Asset == nil || string(ready.Asset.Lifecycle) != "available" {
				t.Fatalf("交付后资产应可见: %v", err)
			}
			if adapter.SubmitCalls() != 1 {
				t.Fatal("交付不得再次提交Provider")
			}
			var count int64
			if err := db.Model(&model.AIImageAsset{}).Where("request_id=? AND lifecycle_state='available'", f.command.RequestID).Count(&count).Error; err != nil || count != 6 {
				t.Fatalf("必须原子交付六资产: %d %v", count, err)
			}
		})
	}
}

// TestVideoG5ReconciliationMySQLRejectsExtraEvidence 不改当前余额/状态的额外矛盾事实也必须阻断零差异结论。
func TestVideoG5ReconciliationMySQLRejectsExtraEvidence(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"billing_event", "execution_attempt", "callback_binding"} {
		t.Run(mode, func(t *testing.T) {
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
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "billing_event":
				from, to := model.AIBillingSettled, model.AIBillingReleased
				err = repository.NewVideoTaskEventRepository(db).Append(context.Background(), task.PublicID, f.owner, model.AIGatewayTaskEvent{EventID: f.command.RequestID + "_wrong_bill", EventType: "billing_status_changed", FromStatus: &from, ToStatus: &to, Source: "system", CreatedAt: time.Now()})
			case "execution_attempt":
				err = db.Create(&model.AIExecutionAttempt{RequestID: task.RequestID, AttemptNo: 1, ExecutionDriver: "native", ProviderCode: "fake-native-async", ExecutionModelCode: task.LogicalModelCode, Status: "unknown", ResultUnknown: true, StartedAt: time.Now()}).Error
			case "callback_binding":
				now := time.Now().UTC()
				err = db.Create(&model.AIGatewayProviderCallbackEvent{TaskID: &task.ID, UserID: &task.UserID, ProjectID: &task.ProjectID, ProviderCode: *task.ProviderCode, ProviderTaskID: "taskUUID-other", ExternalEventID: f.command.RequestID + "_wrong_cb", BodySHA256: strings.Repeat("1", 64), SignatureStatus: "valid", ProcessStatus: "ignored", ApplicationResultJSON: json.RawMessage(`{"result":"ignored","reason":"provider_task_mismatch"}`), ReceivedAt: now, ProcessedAt: &now}).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			report, err := NewVideoReconciliationService(db).Reconcile(context.Background(), task.PublicID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			if report.Passed {
				t.Fatalf("%s矛盾事实被漏检", mode)
			}
		})
	}
}
