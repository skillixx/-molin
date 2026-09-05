package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7ObjectObservationMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	repo := repository.NewVideoObjectReconciliationRepository(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	ref := video.VideoObjectRef{Bucket: "ai-result", ObjectKey: "video_orphan_fixture/vasset_fixture/content.bin"}
	digest := strings.Repeat("a", 64)
	first, err := repo.Observe(ctx, repository.VideoObjectUnreferenced, ref, digest, 1024, now, 5*time.Minute)
	if err != nil || first.Status != "observing" || first.ObservationCount != 1 || first.VersionNo != 1 {
		t.Fatalf("首次观察只能进入静默窗: observation=%+v err=%v", first, err)
	}
	early, err := repo.Observe(ctx, repository.VideoObjectUnreferenced, ref, digest, 1024, now.Add(4*time.Minute), 5*time.Minute)
	if err != nil || early.ID != first.ID || early.VersionNo != 1 || early.ObservationCount != 1 {
		t.Fatalf("静默窗内重扫必须零写: observation=%+v err=%v", early, err)
	}
	if _, err := repo.Observe(ctx, repository.VideoObjectUnreferenced, ref, strings.Repeat("b", 64), 1024, now.Add(4*time.Minute), 5*time.Minute); !errors.Is(err, repository.ErrVideoObjectObservationConflict) {
		t.Fatalf("同一活动观察的元数据漂移必须失败关闭: %v", err)
	}
	confirmed, err := repo.Observe(ctx, repository.VideoObjectUnreferenced, ref, digest, 1024, now.Add(5*time.Minute), 5*time.Minute)
	if err != nil || confirmed.Status != "confirmed" || confirmed.ObservationCount != 2 || confirmed.VersionNo != 2 {
		t.Fatalf("跨过静默窗的第二次观察必须确认: observation=%+v err=%v", confirmed, err)
	}
	var tasks []model.AICompensationTask
	if err := db.Where("task_type='video_orphan_cleanup' AND aggregate_id=?", confirmed.ID).Find(&tasks).Error; err != nil || len(tasks) != 1 || tasks[0].Status != "pending" {
		t.Fatalf("确认后必须形成唯一可追溯补偿任务: tasks=%+v err=%v", tasks, err)
	}
	replayed, err := repo.Observe(ctx, repository.VideoObjectUnreferenced, ref, digest, 1024, now.Add(20*time.Minute), 5*time.Minute)
	if err != nil || replayed.VersionNo != 2 || replayed.ObservationCount != 2 {
		t.Fatal("confirmed重扫不得重复创建补偿事实")
	}
	if err := repo.Resolve(ctx, repository.VideoObjectUnreferenced, ref, now.Add(21*time.Minute)); err != nil {
		t.Fatal(err)
	}
	var resolved repository.VideoObjectObservation
	if err := db.Where("id=?", confirmed.ID).Take(&resolved).Error; err != nil || resolved.Status != "resolved" || resolved.ResolvedAt == nil || resolved.VersionNo != 3 {
		t.Fatalf("恢复后必须保留resolved历史: %+v err=%v", resolved, err)
	}
	var resolvedTask struct {
		Status      string
		CompletedAt *time.Time
	}
	if err := db.Table("ai_compensation_tasks").Where("task_type='video_orphan_cleanup' AND aggregate_id=?", confirmed.ID).Take(&resolvedTask).Error; err != nil || resolvedTask.Status != "completed" || resolvedTask.CompletedAt == nil {
		t.Fatalf("恢复观察必须原子关闭未领取补偿，不能阻塞后续队列: task=%+v err=%v", resolvedTask, err)
	}
	next, err := repo.Observe(ctx, repository.VideoObjectUnreferenced, ref, digest, 1024, now.Add(22*time.Minute), 5*time.Minute)
	if err != nil || next.ID == first.ID || next.Status != "observing" {
		t.Fatalf("同键未来再次异常必须创建新episode: %+v err=%v", next, err)
	}
	if err := db.Model(&repository.VideoObjectObservation{}).Where("id=?", first.ID).Update("bucket", "ai-quarantine").Error; err == nil {
		t.Fatal("观察身份不得直接SQL改写")
	}
	if err := db.Delete(&repository.VideoObjectObservation{}, first.ID).Error; err == nil {
		t.Fatal("观察历史不得删除")
	}
	if _, err := repo.Observe(ctx, repository.VideoObjectUnreferenced, video.VideoObjectRef{Bucket: "ai-result", ObjectKey: "image/not-video.png"}, digest, 1, now, time.Minute); !errors.Is(err, repository.ErrVideoObjectObservationInvalid) {
		t.Fatalf("非视频用途前缀必须拒绝: %v", err)
	}
}

func TestVideoG7ObjectCleanupRetryBoundedMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	repo := repository.NewVideoObjectReconciliationRepository(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	ref := video.VideoObjectRef{Bucket: "ai-result", ObjectKey: "video_retry_bound/vasset_retry/content.bin"}
	digest := strings.Repeat("c", 64)
	if _, err := repo.Observe(ctx, repository.VideoObjectUnreferenced, ref, digest, 2048, now, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Observe(ctx, repository.VideoObjectUnreferenced, ref, digest, 2048, now.Add(5*time.Minute), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	cursor := now.Add(5 * time.Minute)
	for attempt := uint64(1); attempt <= 9; attempt++ {
		lease, err := repo.ClaimCleanup(ctx, "retry-bound-worker", cursor)
		if err != nil {
			t.Fatalf("第%d次必须可领取: %v", attempt, err)
		}
		if err := repo.FailCleanup(ctx, lease, cursor, "object_read_failed", false); err != nil {
			t.Fatal(err)
		}
		var task model.AICompensationTask
		if err := db.Where("id=?", lease.TaskID).Take(&task).Error; err != nil || task.RetryCount != attempt {
			t.Fatalf("重试次数必须精确递增: task=%+v err=%v", task, err)
		}
		if attempt < 9 {
			if task.Status != "retry" || !task.NextRetryAt.After(cursor) {
				t.Fatalf("未耗尽时必须按退避重试: task=%+v", task)
			}
			cursor = task.NextRetryAt
		} else if task.Status != "dead" || !task.NextRetryAt.Equal(cursor) {
			t.Fatalf("第9次失败必须进入dead且停止调度: task=%+v", task)
		}
	}
	if _, err := repo.ClaimCleanup(ctx, "retry-bound-worker", cursor.Add(24*time.Hour)); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("dead补偿不得再次自动领取: %v", err)
	}
}

type videoMissingInventoryFixture struct {
	item *video.VideoObjectInventoryItem
}

func (f *videoMissingInventoryFixture) ListPrefix(context.Context, string, string, string, int) (video.VideoObjectInventoryPage, error) {
	return video.VideoObjectInventoryPage{Done: true}, nil
}
func (f *videoMissingInventoryFixture) InspectObject(context.Context, video.VideoObjectRef) (video.VideoObjectInventoryItem, error) {
	if f.item == nil {
		return video.VideoObjectInventoryItem{}, video.ErrVideoObjectNotFound
	}
	return *f.item, nil
}
func (*videoMissingInventoryFixture) DeleteObservedObject(context.Context, video.VideoObjectRef, string, uint64) error {
	return errors.New("缺失恢复方向禁止删除")
}

func TestVideoG7ObjectMissingWorkerRecoveryMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	repo := repository.NewVideoObjectReconciliationRepository(db)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	ref := video.VideoObjectRef{Bucket: "ai-result", ObjectKey: "vid_missing_repair/vasset_missing/content.bin"}
	digest := strings.Repeat("d", 64)
	if _, err := repo.Observe(ctx, repository.VideoObjectDBMissing, ref, digest, 4096, base, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Observe(ctx, repository.VideoObjectDBMissing, ref, digest, 4096, base.Add(5*time.Minute), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	inventory := &videoMissingInventoryFixture{}
	worker, err := NewVideoObjectMissingWorker(db, inventory, "missing-repair-worker")
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return base.Add(5 * time.Minute) }
	if ran, err := worker.RunOnce(ctx); !ran || !errors.Is(err, video.ErrVideoObjectNotFound) {
		t.Fatalf("对象持续缺失必须有界重试: ran=%t err=%v", ran, err)
	}
	var task model.AICompensationTask
	if err := db.Where("task_type='video_object_missing_reconcile'").Take(&task).Error; err != nil || task.Status != "retry" || task.RetryCount != 1 {
		t.Fatalf("首次缺失必须保留补偿事实: task=%+v err=%v", task, err)
	}
	inventory.item = &video.VideoObjectInventoryItem{Ref: ref, SHA256: digest, SizeBytes: 4096, CreatedAt: base}
	worker.now = func() time.Time { return task.NextRetryAt }
	if ran, err := worker.RunOnce(ctx); !ran || err != nil {
		t.Fatalf("对象按原摘要重现后必须收口: ran=%t err=%v", ran, err)
	}
	var observation repository.VideoObjectObservation
	if err := db.Where("direction='db_missing_object' AND bucket=? AND object_key=?", ref.Bucket, ref.ObjectKey).Take(&observation).Error; err != nil || observation.Status != "resolved" {
		t.Fatalf("恢复后必须解除confirmed围栏: observation=%+v err=%v", observation, err)
	}
	if err := db.Where("id=?", task.ID).Take(&task).Error; err != nil || task.Status != "completed" {
		t.Fatalf("恢复任务必须完成且保留历史: task=%+v err=%v", task, err)
	}
}
