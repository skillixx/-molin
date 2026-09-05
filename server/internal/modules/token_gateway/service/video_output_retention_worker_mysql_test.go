package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7OutputRetentionWorkerMySQL(t *testing.T) {
	if os.Getenv("MOLIN_VIDEO_G7_RUNTIME_ISOLATED") != "YES" {
		t.Skip("VID-G7输出留存只允许完整隔离运行时门禁执行")
	}
	f := NewVideoContentHTTPFixture(t, true)
	client := redis.NewClient(&redis.Options{Addr: "redis:6379", Password: os.Getenv("MOLIN_VIDEO_G7_RUNTIME_REDIS_PASSWORD"), MaxRetries: -1, ContextTimeoutEnabled: true})
	defer client.Close()
	capacity, err := PrepareVideoCapacityRuntime(context.Background(), f.DB, client, mustVideoCapacityNonceKey(t), "output-retention-fixture")
	if err != nil {
		t.Fatalf("加入G7容量恢复epoch失败: %v", err)
	}
	if err := f.App.EnableVideoCapacityReservation(capacity.Recovery, capacity.Store, mustVideoCapacityNonceKey(t)); err != nil {
		t.Fatalf("装配G7容量协调器失败: %v", err)
	}
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	objectStore, ok := f.App.mediaDeleteStore.(video.VideoObjectStore)
	if !ok {
		t.Fatal("测试媒体Store必须实现完整对象接口")
	}
	nonceKey := mustVideoCapacityNonceKey(t)
	factory, err := NewVideoRuntimeGatewayFactory(VideoRuntimeGatewayDependencies{DB: f.DB, App: f.App, Recovery: capacity.Recovery, Capacity: capacity.Store, NonceKey: nonceKey, Provider: video.NewFakeAsyncVideoAdapter(video.FakeVideoSuccess), Store: objectStore})
	if err != nil {
		t.Fatal(err)
	}
	owner := repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: &f.ProjectID}
	runner, err := NewVideoWorkerLeaseRunner(f.DB)
	if err != nil {
		t.Fatal(err)
	}
	completeJob := func(key, prompt string) string {
		t.Helper()
		job, err := f.App.Create(context.Background(), VideoCommand{Caller: caller, IdempotencyKey: key, Model: f.Model, Prompt: prompt, Operation: model.AIVideoOperationTextToVideo})
		if err != nil {
			t.Fatal(err)
		}
		gateway, err := factory(owner)
		if err != nil {
			t.Fatal(err)
		}
		taskID := job.Job.ID
		runStage := func(stage video.TaskStage, action func(context.Context) error) {
			t.Helper()
			if err := runner.Execute(context.Background(), VideoWorkerExecution{TaskID: taskID, Owner: owner, WorkerID: "output-retention-" + string(stage) + "-" + key[len(key)-4:], Stage: stage}, action); err != nil {
				t.Fatal(err)
			}
		}
		runStage(video.TaskSubmit, func(owned context.Context) error {
			_, err := video.NewSubmitWorker(gateway).Run(owned, taskID)
			return err
		})
		runStage(video.TaskPoll, func(owned context.Context) error {
			_, err := video.NewPollWorker(gateway).Run(owned, taskID)
			return err
		})
		runStage(video.TaskPoll, func(owned context.Context) error {
			_, err := video.NewPollWorker(gateway).Run(owned, taskID)
			return err
		})
		runStage(video.TaskFetch, func(owned context.Context) error {
			_, err := video.NewAssetFetchWorker(gateway).Run(owned, taskID)
			if errors.Is(err, ErrVideoGovernanceUnavailable) {
				current, loadErr := repository.NewVideoTaskRepository(f.DB).FindForOwner(owned, taskID, owner)
				if loadErr == nil && current.Status == model.AIImageTaskSucceeded {
					err = nil
				}
			}
			if err != nil {
				return err
			}
			if _, err := f.App.billing.SettleReady(owned, taskID, owner); err != nil {
				return err
			}
			if _, err := f.App.billing.DeliverReady(owned, taskID, owner); err != nil {
				return err
			}
			terminal := NewVideoCapacityExecutionCoordinator(f.App.NewTaskLedger(owner, VideoServerObjectLocationFactory{}), capacity.Recovery, capacity.Store, nonceKey)
			return terminal.ReleaseTerminal(owned, taskID)
		})
		return taskID
	}
	taskID := completeJob("g7-output-retention-create", "仅用于输出到期清理验证")
	beforeFinance := mediaDeleteFinanceSnapshot(t, f.DB, f.ProjectID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	expires := now.Add(-time.Minute)
	if err := f.DB.Model(&model.AIImageAsset{}).Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND modality='video'", taskID).Update("expires_at", expires).Error; err != nil {
		t.Fatalf("准备到期父子资产失败: %v", err)
	}
	var expectedEligible time.Time
	if err := f.DB.Model(&model.AIImageAsset{}).Select("MAX(expires_at)").Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND modality='video'", taskID).Scan(&expectedEligible).Error; err != nil || expectedEligible.IsZero() {
		t.Fatalf("读取数据库权威到期时间失败: value=%s err=%v", expectedEligible, err)
	}
	worker, err := NewVideoOutputRetentionWorker(f.App)
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return now }
	if count, err := worker.RunOnce(context.Background(), 10); err != nil || count != 1 {
		t.Fatalf("到期视频父子树必须复用媒体删除账本收口: count=%d err=%v", count, err)
	}
	var deleted, retained int64
	if err := f.DB.Model(&model.AIImageAsset{}).Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND modality='video' AND asset_role<>'moderation_copy' AND lifecycle_state='deleted' AND media_deleted_at IS NOT NULL", taskID).Count(&deleted).Error; err != nil || deleted != 5 {
		t.Fatalf("五个交付对象必须删除并保留元数据: count=%d err=%v", deleted, err)
	}
	if err := f.DB.Model(&model.AIImageAsset{}).Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='moderation_copy' AND media_deleted_at IS NULL", taskID).Count(&retained).Error; err != nil || retained != 1 {
		t.Fatalf("审核副本必须继续保留: count=%d err=%v", retained, err)
	}
	var fact videoOutputRetentionFact
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", taskID).Take(&fact).Error; err != nil || fact.PolicyVersion != "vid-g7-output-retention-v1" || !fact.EligibleAt.Equal(expectedEligible) || !fact.CompletedAt.Equal(now) {
		t.Fatalf("必须追加唯一输出留存事实: fact=%+v expected_eligible=%s expected_completed=%s err=%v", fact, expectedEligible, now, err)
	}
	if !bytes.Equal(beforeFinance, mediaDeleteFinanceSnapshot(t, f.DB, f.ProjectID)) {
		t.Fatal("输出到期清理不得改写钱包、Quote、Usage或结算事实")
	}
	if count, err := worker.RunOnce(context.Background(), 10); err != nil || count != 0 {
		t.Fatalf("完成事实重跑必须零写: count=%d err=%v", count, err)
	}
	// 用limit=1制造长期受保护前缀；第二轮必须越过前缀清理尾部，不能永久盯住最小Task ID。
	protectedTaskID := completeJob("g7-output-retention-protected", "仅用于受保护前缀公平性验证")
	tailTaskID := completeJob("g7-output-retention-tail", "仅用于尾部清理公平性验证")
	if err := f.DB.Model(&model.AIImageAsset{}).Where("task_id IN (SELECT id FROM ai_gateway_tasks WHERE public_id IN ?) AND modality='video'", []string{protectedTaskID, tailTaskID}).Update("expires_at", expires).Error; err != nil {
		t.Fatalf("准备公平性到期资产失败: %v", err)
	}
	if err := f.DB.Model(&model.AIImageAsset{}).Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='thumbnail'", protectedTaskID).Update("legal_hold", true).Error; err != nil {
		t.Fatalf("准备受保护前缀失败: %v", err)
	}
	if count, err := worker.RunOnce(context.Background(), 1); err != nil || count != 0 {
		t.Fatalf("首轮只能安全跳过受保护前缀: count=%d err=%v", count, err)
	}
	if count, err := worker.RunOnce(context.Background(), 1); err != nil || count != 1 {
		t.Fatalf("第二轮必须通过持久游标清理尾部任务: count=%d err=%v", count, err)
	}
	var tailFacts, protectedFacts int64
	if err := f.DB.Table("ai_video_output_retention_facts").Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", tailTaskID).Count(&tailFacts).Error; err != nil || tailFacts != 1 {
		t.Fatalf("尾部任务必须形成唯一留存事实: count=%d err=%v", tailFacts, err)
	}
	if err := f.DB.Table("ai_video_output_retention_facts").Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", protectedTaskID).Count(&protectedFacts).Error; err != nil || protectedFacts != 0 {
		t.Fatalf("受保护任务不得形成删除事实: count=%d err=%v", protectedFacts, err)
	}
	if err := f.DB.Model(&model.AIImageAsset{}).Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='thumbnail'", protectedTaskID).Update("legal_hold", false).Error; err != nil {
		t.Fatalf("解除公平性夹具保护失败: %v", err)
	}
	if count, err := worker.RunOnce(context.Background(), 1); err != nil || count != 1 {
		t.Fatalf("游标回卷后必须最终清理已解除保护的任务: count=%d err=%v", count, err)
	}
	// 为后续应用/Schema回滚保留两类真实在途任务；不发布Rabbit消息，避免当前测试消费者抢占。
	leaveInFlight := func(key string, mode video.FakeVideoMode, poll bool) {
		t.Helper()
		created, err := f.App.Create(context.Background(), VideoCommand{Caller: caller, IdempotencyKey: key, Model: f.Model, Prompt: "仅用于回滚在途事实验证", Operation: model.AIVideoOperationTextToVideo})
		if err != nil {
			t.Fatal(err)
		}
		inflightFactory, err := NewVideoRuntimeGatewayFactory(VideoRuntimeGatewayDependencies{DB: f.DB, App: f.App, Recovery: capacity.Recovery, Capacity: capacity.Store, NonceKey: nonceKey, Provider: video.NewFakeAsyncVideoAdapter(mode), Store: objectStore})
		if err != nil {
			t.Fatal(err)
		}
		inflight, err := inflightFactory(owner)
		if err != nil {
			t.Fatal(err)
		}
		execute := func(stage video.TaskStage, action func(context.Context) error) {
			if err := runner.Execute(context.Background(), VideoWorkerExecution{TaskID: created.Job.ID, Owner: owner, WorkerID: "rollback-" + string(stage) + "-" + key[len(key)-4:], Stage: stage}, action); err != nil {
				t.Fatal(err)
			}
		}
		execute(video.TaskSubmit, func(owned context.Context) error {
			_, err := video.NewSubmitWorker(inflight).Run(owned, created.Job.ID)
			return err
		})
		if poll {
			execute(video.TaskPoll, func(owned context.Context) error {
				ledger := f.App.NewTaskLedger(owner, VideoServerObjectLocationFactory{})
				current, err := ledger.Load(owned, created.Job.ID)
				if err != nil {
					return err
				}
				_, err = ledger.Advance(owned, current.TaskID, current.Version, video.TaskPendingReconcile, "worker", "query_unknown", nil)
				return err
			})
		}
	}
	leaveInFlight("g7-rollback-pending-0001", video.FakeVideoSuccess, true)
	leaveInFlight("g7-rollback-submitted-0001", video.FakeVideoSuccess, false)
	for status, want := range map[string]int64{model.AIImageTaskQueued: 1, model.AIImageTaskPendingReconcile: 1} {
		var count int64
		if err := f.DB.Table("ai_gateway_tasks").Where("user_id=? AND status=?", f.ProjectID, status).Count(&count).Error; err != nil || count != want {
			t.Fatalf("必须保留回滚在途状态%s: count=%d err=%v", status, count, err)
		}
	}
}
