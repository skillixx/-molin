package service

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func TestVideoG6TaskReadMySQLSettlementSnapshot(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	ctx := context.Background()
	created, err := f.app.Create(ctx, VideoCommand{Caller: f.command.Caller, Model: f.command.Model, Operation: "text_to_video", Prompt: "合成任务查询一致性", IdempotencyKey: "g6-task-read-snapshot-0001"})
	if err != nil {
		t.Fatal(err)
	}
	legacy := f.legacy
	legacy.command.TaskID = created.Job.ID
	legacy.command.RequestID = created.RequestID
	_, adapter := runVideoG5ReadyFixture(t, legacy)
	var injected atomic.Bool
	const hook = "g6_task_read_settle_after_identity"
	if err := legacy.db.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table != "t" || !injected.CompareAndSwap(false, true) {
			return
		}
		if _, err := legacy.service.SettleReady(ctx, created.Job.ID, legacy.owner); err != nil {
			tx.AddError(err)
			return
		}
		if _, err := legacy.service.DeliverReady(ctx, created.Job.ID, legacy.owner); err != nil {
			tx.AddError(err)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer legacy.db.Callback().Query().Remove(hook)
	detail, err := f.app.GetPlatformTask(ctx, f.command.Caller, created.Job.ID, false)
	if err != nil || !injected.Load() || detail.ExecutionStatus != "succeeded" || detail.BillingStatus != "settled" || detail.DeliveryStatus != "available" || detail.SettledAmount == nil || *detail.SettledAmount != "0.50000000" || detail.CurrentFrozenAmount == nil || *detail.CurrentFrozenAmount != "0.00000000" || !detail.CanDeliver {
		t.Fatalf("身份读取后已真实结算/交付，详情不能混合旧RR事实：%+v err=%v", detail, err)
	}
	if adapter.SubmitCalls() != 1 {
		t.Fatal("查询不能重新提交Provider")
	}
}

// 查询必须区分尚未结算的null、取消后零结算，以及删除正文记录后仍保留的原财务事实。
func TestVideoG6TaskReadMySQLFinancialLifecycle(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	ctx := context.Background()
	create := func(key string) *VideoHTTPGeneration {
		t.Helper()
		result, err := f.app.Create(ctx, VideoCommand{Caller: f.command.Caller, Model: f.command.Model, Operation: "text_to_video", Prompt: "合成任务财务查询", IdempotencyKey: key})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	cancelled := create("g6-task-read-cancel-0001")
	held, err := f.app.GetPlatformTask(ctx, f.command.Caller, cancelled.Job.ID, false)
	if err != nil || held.BillingStatus != "held" || held.SettledAmount != nil || held.CurrentFrozenAmount == nil || *held.CurrentFrozenAmount != "0.50000000" || held.CanDeliver {
		t.Fatalf("预占尚未结算不能伪造零结算：%+v err=%v", held, err)
	}
	t.Run("已预占却丢失Link", func(t *testing.T) {
		assertVideoG6TaskReadRejectsDBResult(t, f, cancelled.Job.ID, func(tx *gorm.DB) bool {
			if _, ok := tx.Statement.Dest.(*model.AIRequestWalletLink); !ok {
				return false
			}
			tx.AddError(gorm.ErrRecordNotFound)
			return true
		})
	})
	t.Run("同owner错Hold幂等身份", func(t *testing.T) {
		assertVideoG6TaskReadRejectsDBResult(t, f, cancelled.Job.ID, func(tx *gorm.DB) bool {
			hold, ok := tx.Statement.Dest.(*billingmodel.WalletHold)
			if ok {
				hold.IdempotencyKey = "other_request:video-hold"
			}
			return ok
		})
	})
	// 100个事件读取与实际取消事务竞争，每个分页必须完整看到追加前或追加后的同一组事实。
	start := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 100; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			events, err := f.app.ListPlatformTaskEvents(ctx, f.command.Caller, cancelled.Job.ID, 1, 100)
			if err != nil || events == nil || events.Total < 1 || int64(len(events.Items)) != events.Total {
				t.Errorf("事件total/items不得跨追加事务混合：%+v err=%v", events, err)
			}
		}()
	}
	readers.Add(1)
	go func() {
		defer readers.Done()
		<-start
		if _, err := f.legacy.service.CancelBeforeSubmit(ctx, cancelled.Job.ID, f.legacy.owner); err != nil {
			t.Error(err)
		}
	}()
	close(start)
	readers.Wait()
	released, err := f.app.GetPlatformTask(ctx, f.command.Caller, cancelled.RequestID, true)
	if err != nil || released.ExecutionStatus != "cancelled" || released.BillingStatus != "released" || released.DeliveryStatus != "rejected" || released.SettledAmount == nil || *released.SettledAmount != "0.00000000" || released.NetReleasedAmount == nil || *released.NetReleasedAmount != "0.50000000" || released.CurrentFrozenAmount == nil || *released.CurrentFrozenAmount != "0.00000000" || released.CompletedAt == nil || released.CanDeliver {
		t.Fatalf("取消后必须展示零结算与原预占全额净释放：%+v err=%v", released, err)
	}
	completed := create("g6-task-read-completed-0001")
	legacy := f.legacy
	legacy.command.TaskID, legacy.command.RequestID = completed.Job.ID, completed.RequestID
	_, adapter := runVideoG5ReadyFixture(t, legacy)
	if _, err := legacy.service.SettleReady(ctx, completed.Job.ID, legacy.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.service.DeliverReady(ctx, completed.Job.ID, legacy.owner); err != nil {
		t.Fatal(err)
	}
	before, err := f.app.GetPlatformTask(ctx, f.command.Caller, completed.Job.ID, false)
	if err != nil || !before.CanDeliver {
		t.Fatalf("只有实际完成和结算后的查询才能可交付：%+v err=%v", before, err)
	}
	for _, c := range []struct {
		name   string
		mutate func(*billingmodel.WalletHold)
	}{
		{"Hold与Link结算金额不一致", func(h *billingmodel.WalletHold) { wrong := decimal.RequireFromString("0.40"); h.SettledAmount = &wrong }},
		{"settled缺结算金额", func(h *billingmodel.WalletHold) { h.SettledAmount = nil }},
		{"Hold未知状态", func(h *billingmodel.WalletHold) { h.Status = "unknown" }},
		{"holding不能包含结算金额", func(h *billingmodel.WalletHold) { h.Status = "holding" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			assertVideoG6TaskReadRejectsDBResult(t, f, completed.Job.ID, func(tx *gorm.DB) bool {
				hold, ok := tx.Statement.Dest.(*billingmodel.WalletHold)
				if ok {
					c.mutate(hold)
				}
				return ok
			})
		})
	}
	// 本用例只验证已有删除元数据下的查询保留合同，不声称执行了对象正文删除。
	var asset model.AIImageAsset
	if err := legacy.db.Where("request_id=? AND asset_role='content'", completed.RequestID).Take(&asset).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.NewVideoOutputAssetRepository(legacy.db, nil).MarkMediaDeleted(ctx, asset.PublicID, legacy.owner, asset.VersionNo, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	after, err := f.app.GetPlatformTask(ctx, f.command.Caller, completed.RequestID, true)
	if err != nil || !after.MediaDeleted || after.CanDeliver {
		t.Fatalf("媒体已删除记录不得继续授予交付：%+v err=%v", after, err)
	}
	expected := *before
	expected.MediaDeleted, expected.CanDeliver = true, false
	if !reflect.DeepEqual(expected, *after) {
		t.Fatalf("删除媒体记录不得改写原请求/报价/金额/状态事实：before=%+v after=%+v", before, after)
	}
	if _, err := f.app.GetVideo(ctx, f.command.Caller, completed.Job.ID); !errors.Is(err, repository.ErrVideoTaskNotFound) {
		t.Fatalf("兼容门面应隐藏媒体已删除Job：%v", err)
	}
	page, err := f.app.ListPlatformTasks(ctx, f.command.Caller, 1, 20)
	if err != nil || page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("平台账单列表保留取消与删除记录：%+v err=%v", page, err)
	}
	if adapter.SubmitCalls() != 1 {
		t.Fatal("查询与记录保留不能再次提交Provider")
	}
}

// 仅注入数据库读取边界的损坏返回值，不改写或删除原资金事实，也不Mock业务服务。
func assertVideoG6TaskReadRejectsDBResult(t *testing.T, f videoG6I2VFixture, taskID string, mutate func(*gorm.DB) bool) {
	t.Helper()
	const hook = "g6_task_read_corrupt_db_result"
	var injected atomic.Bool
	if err := f.legacy.db.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		if !injected.Load() && mutate(tx) {
			injected.Store(true)
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer f.legacy.db.Callback().Query().Remove(hook)
	_, err := f.app.GetPlatformTask(context.Background(), f.command.Caller, taskID, false)
	if !injected.Load() || !errors.Is(err, ErrVideoAccessUnavailable) {
		t.Fatalf("损坏财务事实必须失败关闭：injected=%v err=%v", injected.Load(), err)
	}
}
