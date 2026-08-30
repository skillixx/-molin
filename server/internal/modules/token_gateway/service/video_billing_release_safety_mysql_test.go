package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 只在明确外部边界制造故障，不模拟钱包、Repository或对账器。
type videoG5ReleaseTestLabeler struct {
	mode  string
	calls int
}

func (l *videoG5ReleaseTestLabeler) Apply(ctx context.Context, r videogateway.LabelRequest) (videogateway.LabelResult, error) {
	l.calls++
	if l.mode == "label_unknown" {
		return videogateway.LabelResult{Version: "fake-label-v1", ExplicitStatus: videogateway.LabelPending, ImplicitStatus: videogateway.LabelPending}, context.DeadlineExceeded
	}
	if l.mode == "derived_failed" && l.calls > 1 {
		return videogateway.LabelResult{}, videogateway.ErrVideoLabelFailed
	}
	return videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelSuccess, "fake-label-v1").Apply(ctx, r)
}

type videoG5ReleaseTestStore struct{ videogateway.VideoObjectStore }

func (s videoG5ReleaseTestStore) Put(context.Context, videogateway.PutVideoObjectRequest) (videogateway.StoredVideoObject, error) {
	return videogateway.StoredVideoObject{}, errors.New("合成归档失败")
}

func videoG5ReleaseFailureFixture(t *testing.T, db *gorm.DB, mode string) (videoG5ReservationFixture, *videogateway.FakeAsyncVideoAdapter) {
	t.Helper()
	f := newVideoG5ReservationFixture(t, db, "10")
	prepareVideoG5I2V(t, &f)
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	a := videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess)
	var labeler videogateway.VideoAILabeler = videogateway.NewFakeVideoAILabeler(videogateway.FakeVideoLabelExplicitFailure, "fake-label-v1")
	if mode == "label_unknown" || mode == "derived_failed" {
		labeler = &videoG5ReleaseTestLabeler{mode: mode}
	}
	var store videogateway.VideoObjectStore = videogateway.NewFakeVideoObjectStore()
	if mode == "archive_failed" {
		store = videoG5ReleaseTestStore{store}
	}
	l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
	g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: l, Provider: a, Probe: videogateway.NewVideoMediaProbe(videoG4TestProbeLimits()), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess)), Labeler: labeler, Store: store})
	if _, err := g.Submit(context.Background(), f.command.TaskID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := g.Poll(context.Background(), f.command.TaskID); err != nil {
			t.Fatal(err)
		}
	}
	_, fetchErr := g.FetchAndFinalize(context.Background(), f.command.TaskID)
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil || task.Status != model.AIImageTaskFailed {
		t.Fatalf("合成失败必须落库: %v %v", err, fetchErr)
	}
	return f, a
}

// 未知标识、派生或归档失败不能仅因Task failed或资产隔离就释放；后补marker也不能改变原原因。
func TestVideoG5ReleaseMySQLRejectsUnprovenFailures(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"label_unknown", "derived_failed", "archive_failed"} {
		t.Run(mode, func(t *testing.T) {
			f, a := videoG5ReleaseFailureFixture(t, db, mode)
			if _, err := f.service.ReleaseUnserviceable(context.Background(), f.command.TaskID, f.owner); err == nil {
				t.Fatal("证据不足却释放了Hold")
			}
			e := model.AIGatewayTaskEvent{EventID: "vg5_" + videoBillingDigest(f.command.RequestID+":video_release_label_failed"), EventType: "video_release_label_failed", Source: "worker", CreatedAt: time.Now()}
			if err := repository.NewVideoTaskEventRepository(db).Append(context.Background(), f.command.TaskID, f.owner, e); err == nil {
				t.Fatal("通用Append允许伪造释放依据")
			}
			// 模拟带数据库写权限的错误补记；旧marker即使存在，也不构成原失败原因证明。
			task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			e.TaskID, e.UserID, e.ProjectID = task.ID, task.UserID, task.ProjectID
			if err := db.Create(&e).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.ReleaseUnserviceable(context.Background(), f.command.TaskID, f.owner); err == nil {
				t.Fatal("补记旧marker不能替代原始失败原因")
			}
			var origins []model.VideoExecutionFailureEvent
			if err := db.Where("task_id=? AND failure_origin IS NOT NULL", task.ID).Find(&origins).Error; err != nil || len(origins) != 1 {
				t.Fatalf("原始失败事实必须唯一: %v", err)
			}
			if err := db.Model(&model.VideoExecutionFailureEvent{}).Where("id=?", origins[0].ID).Update("failure_origin", "label_failed").Error; err == nil {
				t.Fatal("不得改写原始失败原因")
			}
			for _, from := range []*string{nil, &task.Status} {
				to := "failed"
				bad := model.VideoExecutionFailureEvent{AIGatewayTaskEvent: model.AIGatewayTaskEvent{EventID: f.command.RequestID + "_invalid_origin", TaskID: task.ID, UserID: task.UserID, ProjectID: task.ProjectID, EventType: "execution_status_changed", Source: "worker", FromStatus: from, ToStatus: &to, CreatedAt: time.Now()}, FailureOrigin: "label_failed"}
				if err := db.Create(&bad).Error; err == nil {
					t.Fatal("NULL或错误前状态不能携带失败原因")
				}
			}
			assertVideoG5ReleaseStillHeld(t, f)
			if a.SubmitCalls() != 1 {
				t.Fatal("失败财务路径不得重新Submit")
			}
		})
	}
}

// 每个释放写入点故障均整体回滚；只留下原确认成本、Hold和唯一补偿，随后恢复且不重复释放。
func TestVideoG5ReleaseMySQLRollbackAndRecovery(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, step := range []string{"release_pending", "release_hold", "release_link", "release_usage_fact", "release_sale_line", "release_state", "release_lease", "release_outbox", "release_rejected_outbox", "release_checked"} {
		t.Run(step, func(t *testing.T) {
			f, a := videoG5ReleaseFailureFixture(t, db, "label_failed")
			f.service.fault = func(at string) error {
				if at == step {
					return errors.New("合成释放故障")
				}
				return nil
			}
			if _, err := f.service.ReleaseUnserviceable(context.Background(), f.command.TaskID, f.owner); err == nil {
				t.Fatal("释放故障应返回错误")
			}
			assertVideoG5ReleaseStillHeld(t, f)
			job, err := repository.NewVideoCompensationRepository(db).GetForTask(context.Background(), f.command.TaskID, f.owner)
			if err != nil || job.Status != "pending" || job.OriginErrorCode != "release_failed" {
				t.Fatalf("缺少唯一释放补偿: %v", err)
			}
			f.service.fault = nil
			w, err := NewVideoCompensationWorker(f.service, "release-recovery")
			if err != nil {
				t.Fatal(err)
			}
			r, err := w.RunOne(context.Background(), f.command.RequestID)
			if err != nil || r.Status != "completed" || r.Financial == nil || r.Financial.BillingStatus != model.AIBillingReleased {
				t.Fatalf("释放补偿未闭合: result=%+v err=%v", r, err)
			}
			for i := 0; i < 2; i++ {
				replay, err := w.RunOne(context.Background(), f.command.RequestID)
				if err != nil || !replay.Existing {
					t.Fatalf("已完成补偿重放不应重复释放: %v", err)
				}
			}
			if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err == nil {
				t.Fatal("released不能被settled覆盖")
			}
			if a.SubmitCalls() != 1 {
				t.Fatal("补偿不得重新Submit")
			}
		})
	}
}

func assertVideoG5ReleaseStillHeld(t *testing.T, f videoG5ReservationFixture) {
	t.Helper()
	var wallet billingmodel.Wallet
	if err := f.db.Where("user_id=?", f.owner.UserID).First(&wallet).Error; err != nil {
		t.Fatal(err)
	}
	if wallet.BalanceAmount.StringFixed(8) != "9.25000000" || wallet.FrozenAmount.StringFixed(8) != "0.75000000" {
		t.Fatal("不应改变合成I2V钱包的余额和冻结额")
	}
	facts, err := repository.NewVideoUsageRepository(f.db).ListForTask(context.Background(), f.command.TaskID, f.owner)
	if err != nil || len(facts) != 2 {
		t.Fatalf("确认成本保留，用户销售不应提前写入: %v", err)
	}
	bindings, err := repository.NewVideoTaskInputRepository(f.db).ListForOwner(context.Background(), f.command.TaskID, f.owner)
	if err != nil || len(bindings) != 1 || bindings[0].LeaseReleasedAt != nil {
		t.Fatalf("Hold未终结前仍须保护输入: %v", err)
	}
	var n int64
	if err := f.db.Model(&billingmodel.WalletTransaction{}).Where("user_id=?", f.owner.UserID).Count(&n).Error; err != nil || n != 1 {
		t.Fatalf("仅可保留原冻结流水: %d %v", n, err)
	}
}
