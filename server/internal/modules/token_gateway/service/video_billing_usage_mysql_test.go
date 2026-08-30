package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG5UsageMySQLNoSubmitProof 必须排除共享执行尝试和submitting历史，不能仅凭任务未绑定Provider就写零成本。
func TestVideoG5UsageMySQLNoSubmitProof(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"execution_attempt", "submitting_history"} {
		t.Run(mode, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			tasks := repository.NewVideoTaskRepository(db)
			task, err := tasks.FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "execution_attempt" {
				attempt := model.AIExecutionAttempt{RequestID: f.command.RequestID, AttemptNo: 1, ExecutionDriver: "native", ProviderCode: "fake-native-async", ExecutionModelCode: f.command.FingerprintInput.LogicalModelCode, Status: "running", StartedAt: time.Now()}
				if err := repository.NewG2Repository(db).StartRequest(context.Background(), f.command.RequestID, &attempt); err != nil {
					t.Fatal(err)
				}
			} else {
				for _, status := range []string{model.AIImageTaskQueued, model.AIImageTaskSubmitting} {
					task, err = tasks.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: task.PublicID, Owner: f.owner, ExpectedVersion: task.VersionNo, ToStatus: status, Progress: task.Progress, EventID: f.command.RequestID + "_" + status, Source: "worker", Now: time.Now()})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			task, err = tasks.TransitionExecution(context.Background(), repository.VideoStateTransition{TaskPublicID: task.PublicID, Owner: f.owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskCancelled, Progress: task.Progress, EventID: f.command.RequestID + "_cancelled", Source: "worker", Now: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			zero, currency := decimal.Zero, "CNY"
			fact := model.AIUsageItem{RecordKind: model.AIUsageCostLine, Source: "gateway", Quantity: zero, UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &zero, Currency: &currency}
			var appendErr error
			rollback := errors.New("仅回滚反例插入")
			err = db.Transaction(func(tx *gorm.DB) error {
				_, _, appendErr = repository.NewVideoUsageRepository(db).AppendTx(tx, task.PublicID, f.owner, fact, time.Now())
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			if appendErr == nil {
				t.Error("存在执行证据时仓储不得确认未提交零成本")
			}
			fact.RequestID, fact.Operation, fact.PriceVersionID = task.RequestID, task.Operation, &f.quote.PriceVersionID
			fact.MeterType, fact.UsageUnit, fact.VariantHash, fact.VariantJSON = VideoMeterSeconds, "seconds", f.quote.RequestVariantHash, task.InputJSON
			direct := model.VideoUsageItem{AIUsageItem: fact, TaskID: task.ID, QuoteID: task.QuoteID, UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID, LogicalModelCode: task.LogicalModelCode, Capability: model.AIVideoCapability}
			if err := db.Create(&direct).Error; err == nil {
				t.Error("直接SQL也必须拒绝没有未提交证明的零成本")
			}
		})
	}
}

// TestVideoG5UsageMySQLAppendOwnershipAndReplay 验证共享Usage按任务补全归属，同事实重放不追加，异值冲突不覆盖。
func TestVideoG5UsageMySQLAppendOwnershipAndReplay(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	r := repository.NewVideoUsageRepository(db)
	zero, currency := decimal.Zero, "CNY"
	fact := model.AIUsageItem{RecordKind: model.AIUsageFact, Source: "gateway", Quantity: zero, UnitSize: decimal.NewFromInt(1), UnitPrice: &zero, Amount: &zero, Currency: &currency}
	appendFact := func(item model.AIUsageItem) (bool, error) {
		var replay bool
		err := db.Transaction(func(tx *gorm.DB) error {
			_, old, err := r.AppendTx(tx, f.command.TaskID, f.owner, item, time.Now().UTC())
			replay = old
			return err
		})
		return replay, err
	}
	if old, err := appendFact(fact); err != nil || old {
		t.Fatalf("首次追加失败: %v", err)
	}
	if old, err := appendFact(fact); err != nil || !old {
		t.Fatalf("重放应读取原事实: %v", err)
	}
	changed := fact
	changed.Quantity = decimal.NewFromInt(1)
	if _, err := appendFact(changed); !errors.Is(err, repository.ErrVideoUsageConflict) {
		t.Fatalf("同序号异值必须拒绝: %v", err)
	}
	items, err := r.ListForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || len(items) != 1 {
		t.Fatalf("只应存在一条事实: %v", err)
	}
	item := items[0]
	if item.RequestID != f.command.RequestID || item.TaskID == 0 || item.QuoteID != f.quote.ID || item.UserID != f.owner.UserID || item.ProjectID != f.owner.ProjectID || item.APIKeyID == nil || *item.APIKeyID != *f.owner.APIKeyID || item.LogicalModelCode != f.command.FingerprintInput.LogicalModelCode || item.Capability != model.AIVideoCapability || item.Operation == nil || *item.Operation != model.AIVideoOperationTextToVideo || item.PriceVersionID == nil || *item.PriceVersionID != f.quote.PriceVersionID {
		t.Fatal("Usage归属/模型/报价事实缺失")
	}
	wrong := f.owner
	wrong.APIKeyID = nil
	if _, err := r.ListForTask(context.Background(), f.command.TaskID, wrong); !errors.Is(err, repository.ErrVideoTaskNotFound) {
		t.Fatalf("换Key不得读取Usage: %v", err)
	}
	if err := db.Model(&model.VideoUsageItem{}).Where("id=?", item.ID).Update("quantity", 5).Error; err == nil {
		t.Fatal("数据库不得更新视频Usage")
	}
	if err := db.Delete(&model.VideoUsageItem{}, item.ID).Error; err == nil {
		t.Fatal("数据库不得删除视频Usage")
	}
}
