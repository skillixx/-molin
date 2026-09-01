package service

import (
	"context"
	"errors"
	"fmt"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// G5任一末尾写失败仍须回滚外层取消命令，不允许已释放而无回执或有回执但未释放。
func TestVideoG6TaskCancelRollbackMySQL(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	c := f.command
	c.Operation = model.AIVideoOperationTextToVideo
	c.InputAssetID = ""
	job, err := f.app.Create(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	f.app.billing.fault = func(at string) error {
		if at == "cancel_rejected_outbox" {
			return errors.New("合成末尾失败")
		}
		return nil
	}
	if got, err := f.app.CancelTask(context.Background(), c.Caller, job.Job.ID, "g6-cancel-rollback"); err == nil || got != nil {
		t.Fatal("注入失败不能返回取消成功")
	}
	task, err := repository.NewVideoTaskRepository(f.legacy.db).FindForOwner(context.Background(), job.Job.ID, f.legacy.owner)
	if err != nil || task.Status != "reserved" || task.BillingStatus != "held" || task.CancelRequestedAt != nil {
		t.Fatal("原Task/Request必须回滚")
	}
	for table, where := range map[string]string{"ai_video_cancellation_commands": "task_id=?", "ai_usage_items": "task_id=?", "ai_gateway_task_events": "task_id=? AND event_type='cancel_requested'"} {
		var n int64
		if err := f.legacy.db.Table(table).Where(where, task.ID).Count(&n).Error; err != nil || n != 0 {
			t.Fatalf("失败后不能保留取消/用量事实：%s %d %v", table, n, err)
		}
	}
	var wallet struct{ BalanceAmount, FrozenAmount string }
	if err := f.legacy.db.Table("wallets").Select("balance_amount,frozen_amount").Where("user_id=?", c.Caller.UserID).Take(&wallet).Error; err != nil || wallet.BalanceAmount != "9.50000000" || wallet.FrozenAmount != "0.50000000" {
		t.Fatalf("释放必须随外层失败回滚至原可用9.5/冻结0.5：balance=%s frozen=%s err=%v", wallet.BalanceAmount, wallet.FrozenAmount, err)
	}
	f.app.billing.fault = nil
	if got, err := f.app.CancelTask(context.Background(), c.Caller, job.Job.ID, "g6-cancel-rollback"); err != nil || got == nil || got.Idempotent {
		t.Fatalf("原键恢复应首次成功：%v", err)
	}
	if err := f.legacy.db.Table("ai_video_cancellation_commands").Where("task_id=?", task.ID).Update("initial_result", "already_terminal").Error; err == nil {
		t.Fatal("回执不得更新")
	}
	if err := f.legacy.db.Exec("DELETE FROM ai_video_cancellation_commands WHERE task_id=?", task.ID).Error; err == nil {
		t.Fatal("回执不得删除")
	}
	if err := f.legacy.db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status,video_generate_allowed) SELECT id+9000000,user_id,project_id,'g6',SHA2(CONCAT('g6-cancel-other-',id),256),'合成另一Key','postpaid','allowlist','active',1 FROM api_keys WHERE id=?", c.Caller.APIKeyID).Error; err != nil {
		t.Fatal(err)
	}
	wrongKey := c.Caller.APIKeyID + 9000000
	// 该高位Key与公共HTTP夹具共享同一张表，必须同步编号上界，防止后续显式Key复用已占用主键。
	ReserveVideoFixtureIDsThrough(wrongKey)
	err = f.legacy.db.Exec("INSERT INTO ai_video_cancellation_commands(user_id,project_id,command_kind,command_key_hash,task_id,request_id,api_key_id,initial_result,created_at) VALUES(?,?,'cancel',?,?,?,?, 'already_terminal',UTC_TIMESTAMP())", c.Caller.UserID, c.Caller.ProjectID, videoBillingDigest("g6-cancel-wrong-key"), task.ID, task.RequestID, wrongKey).Error
	var dbError *mysqlDriver.MySQLError
	if !errors.As(err, &dbError) || dbError.Number != 1644 {
		t.Fatalf("SQL必须精确拒绝同用户/Project但错Key回执：%v", err)
	}
}

// 模拟MySQL死锁已回滚整个事务与普通锁等待超时，要求最外层从取消回执开始完整重试。
func TestVideoG6TaskCancelDatabaseRetryMySQL(t *testing.T) {
	for _, number := range []uint16{1213, 1205} {
		t.Run(fmt.Sprint(number), func(t *testing.T) {
			f := newVideoG6I2VFixture(t)
			c := f.command
			c.Operation = model.AIVideoOperationTextToVideo
			c.InputAssetID = ""
			job, err := f.app.Create(context.Background(), c)
			if err != nil {
				t.Fatal(err)
			}
			var injected atomic.Bool
			var commandCreates atomic.Int64
			name := "g6_cancel_whole_transaction_retry"
			if err := f.legacy.db.Callback().Create().After("gorm:create").Register(name, func(tx *gorm.DB) {
				if tx.Error != nil {
					return
				}
				if tx.Statement.Table == "ai_video_cancellation_commands" {
					commandCreates.Add(1)
					return
				}
				e, ok := tx.Statement.Dest.(*model.AIOutboxEvent)
				if !ok || e.AggregateID != job.RequestID || e.EventType != "video_delivery_rejected" || !injected.CompareAndSwap(false, true) {
					return
				}
				if number == 1213 {
					// 只回滚当前合成请求所在的实际事务连接，复现InnoDB死锁受害者的失效保存点。
					if err := tx.Session(&gorm.Session{NewDB: true}).Exec("ROLLBACK").Error; err != nil {
						tx.AddError(err)
						return
					}
				}
				tx.AddError(&mysqlDriver.MySQLError{Number: number, Message: "合成事务重试故障"})
			}); err != nil {
				t.Fatal(err)
			}
			defer f.legacy.db.Callback().Create().Remove(name)
			result, err := f.app.CancelTask(context.Background(), c.Caller, job.Job.ID, fmt.Sprintf("g6-cancel-retry-%d", number))
			if err != nil || result == nil || result.CancellationResult != "cancelled" {
				t.Fatalf("应完整重试并成功：%v", err)
			}
			var commands int64
			if err := f.legacy.db.Table("ai_video_cancellation_commands").Where("request_id=?", job.RequestID).Count(&commands).Error; err != nil || commands != 1 || commandCreates.Load() != 2 || !injected.Load() {
				t.Fatalf("必须从外层重建且仅保留一个回执：rows=%d attempts=%d injected=%v err=%v", commands, commandCreates.Load(), injected.Load(), err)
			}
			if report, err := NewVideoReconciliationService(f.legacy.db).Reconcile(context.Background(), job.Job.ID, f.legacy.owner); err != nil || !report.Passed {
				t.Fatalf("重试后财务必须唯一且对账通过：%v", err)
			}
		})
	}
}

// 真正进入submitting后的在途RPC不能被HTTP意图退款；确认返回后仍保存原Provider绑定。
func TestVideoG6TaskCancelSubmittingMySQL(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	c := f.command
	c.Operation = model.AIVideoOperationTextToVideo
	c.InputAssetID = ""
	job, err := f.app.Create(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	base := video.NewFakeAsyncVideoAdapter(video.FakeVideoSuccess)
	adapter := &videoG5InflightSubmit{VideoProviderAdapter: base, entered: make(chan struct{}), release: make(chan struct{})}
	g := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: f.app.NewTaskLedger(f.legacy.owner, videoG4TestLocationFactory{}), Provider: adapter, Probe: video.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess)), Labeler: video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1"), Store: video.NewFakeVideoObjectStore()})
	done := make(chan error, 1)
	go func() { _, err := g.Submit(context.Background(), job.Job.ID); done <- err }()
	select {
	case <-adapter.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("未进入真实submitting窗口")
	}
	var releaseOnce bool
	defer func() {
		if !releaseOnce {
			close(adapter.release)
		}
	}()
	r, err := f.app.CancelTask(context.Background(), c.Caller, job.Job.ID, "g6-cancel-inflight")
	if err != nil || r == nil || r.CancellationResult != "cancel_requested" || r.ExecutionStatus != "submitting" || r.BillingStatus != "held" || r.CancelRequestedAt == nil {
		t.Fatalf("在途RPC只能记录意图：%v", err)
	}
	close(adapter.release)
	releaseOnce = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("原RPC未结束")
	}
	task, err := repository.NewVideoTaskRepository(f.legacy.db).FindForOwner(context.Background(), job.Job.ID, f.legacy.owner)
	if err != nil || task.ProviderTaskID == nil || task.CancelRequestedAt == nil || task.BillingStatus != "held" || base.SubmitCalls() != 1 {
		t.Fatal("必须保留原绑定和预占，不重提或释放")
	}
	if r, err := f.app.CancelTask(context.Background(), c.Caller, job.Job.ID, "g6-cancel-inflight"); err != nil || !r.Idempotent || r.CancellationResult != "cancel_requested" {
		t.Fatal("在途取消重放必须复用原命令")
	}
}
