package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func TestImageObjectCleanupWorkerCompletesDeletedAndMissingObjects(t *testing.T) {
	now := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)
	for _, deleteErr := range []error{nil, imagegateway.ErrObjectNotFound} {
		repo := newMemoryImageObjectCleanupTaskStore(now, 0)
		store := &recordingObjectDeleteStore{ObjectStore: imagegateway.NewFakeObjectStore(), err: deleteErr}
		worker, err := NewImageObjectCleanupWorker(repo, store)
		if err != nil {
			t.Fatal(err)
		}
		worker.now = func() time.Time { return now }
		result, err := worker.RunBatch(context.Background(), 10)
		if err != nil || result.Scanned != 1 || result.Completed != 1 || result.Retried != 0 || len(repo.completed) != 1 || len(store.refs) != 1 {
			t.Fatalf("删除成功或对象已不存在都应完成: result=%+v completed=%v refs=%v err=%v", result, repo.completed, store.refs, err)
		}
		if store.refs[0] != repo.ref {
			t.Fatalf("Worker只能删除Recorder重建的固定临时路径: got=%+v want=%+v", store.refs[0], repo.ref)
		}
	}
}

func TestImageObjectCleanupWorkerProtectsReferencedAssets(t *testing.T) {
	now := time.Date(2026, 8, 26, 9, 15, 0, 0, time.UTC)
	repo := newMemoryImageObjectCleanupTaskStore(now, 0)
	repo.referenced = true
	store := &recordingObjectDeleteStore{ObjectStore: imagegateway.NewFakeObjectStore()}
	worker, err := NewImageObjectCleanupWorker(repo, store)
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return now }
	result, err := worker.RunBatch(context.Background(), 10)
	if err != nil || result.Scanned != 1 || result.Protected != 1 || result.Completed != 1 || result.Retried != 0 || len(store.refs) != 0 || len(repo.completed) != 1 {
		t.Fatalf("已有任意资产引用时必须安全完成且不删除: result=%+v refs=%v completed=%v err=%v", result, store.refs, repo.completed, err)
	}
}

func TestImageObjectCleanupWorkerFailsClosedWhenReferenceQueryFails(t *testing.T) {
	now := time.Date(2026, 8, 26, 9, 20, 0, 0, time.UTC)
	repo := newMemoryImageObjectCleanupTaskStore(now, 0)
	repo.referenceErr = errors.New("注入资产引用查询失败")
	store := &recordingObjectDeleteStore{ObjectStore: imagegateway.NewFakeObjectStore()}
	worker, err := NewImageObjectCleanupWorker(repo, store)
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return now }
	result, err := worker.RunBatch(context.Background(), 10)
	if err != nil || result.Retried != 1 || result.Completed != 0 || len(store.refs) != 0 || len(repo.failures) != 1 || repo.failures[0].errorClass != "object_reference_check_failed" {
		t.Fatalf("引用查询未知时必须保守重试且不得删除: result=%+v refs=%v failures=%+v err=%v", result, store.refs, repo.failures, err)
	}
}

func TestImageObjectCleanupWorkerBoundsReferenceQuery(t *testing.T) {
	now := time.Date(2026, 8, 26, 9, 25, 0, 0, time.UTC)
	repo := newMemoryImageObjectCleanupTaskStore(now, 0)
	repo.blockReference = true
	store := &recordingObjectDeleteStore{ObjectStore: imagegateway.NewFakeObjectStore()}
	worker, err := NewImageObjectCleanupWorker(repo, store)
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return now }
	worker.referenceTimeout = 10 * time.Millisecond
	started := time.Now()
	result, err := worker.RunBatch(context.Background(), 10)
	if err != nil || time.Since(started) > time.Second || result.Retried != 1 || len(store.refs) != 0 || len(repo.failures) != 1 {
		t.Fatalf("资产引用查询挂起必须有界失败关闭: elapsed=%s result=%+v refs=%v failures=%+v err=%v", time.Since(started), result, store.refs, repo.failures, err)
	}
}

func TestImageObjectCleanupWorkerKeepsPutUnknownReasonWhenReferenceCheckFailsDuringQuietWindow(t *testing.T) {
	createdAt := time.Date(2026, 8, 26, 9, 27, 0, 0, time.UTC)
	repo := newMemoryImageObjectCleanupTaskStore(createdAt, 0)
	reason := string(imagegateway.ObjectCleanupAfterResultPutUnknown)
	repo.tasks[0].LastErrorClass, repo.tasks[0].CreatedAt = &reason, createdAt
	repo.referenceErr = errors.New("注入资产引用瞬时失败")
	worker, err := NewImageObjectCleanupWorker(repo, &recordingObjectDeleteStore{ObjectStore: imagegateway.NewFakeObjectStore()})
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return createdAt.Add(time.Minute) }
	result, err := worker.RunBatch(context.Background(), 10)
	if err != nil || result.Quiescing != 1 || result.Retried != 0 || len(repo.failures) != 0 || len(repo.reschedules) != 1 || repo.tasks[0].LastErrorClass == nil || *repo.tasks[0].LastErrorClass != reason {
		t.Fatalf("静默窗引用查询失败必须保留PutUnknown原因: result=%+v task=%+v failures=%v reschedules=%v err=%v", result, repo.tasks[0], repo.failures, repo.reschedules, err)
	}
}

func TestImageObjectCleanupWorkerKeepsPutUnknownReasonWhenHeadFailsDuringQuietWindow(t *testing.T) {
	createdAt := time.Date(2026, 8, 26, 9, 28, 0, 0, time.UTC)
	repo := newMemoryImageObjectCleanupTaskStore(createdAt, 0)
	reason := string(imagegateway.ObjectCleanupAfterThumbnailPutUnknown)
	repo.tasks[0].LastErrorClass, repo.tasks[0].CreatedAt = &reason, createdAt
	store := &headErrorObjectStore{ObjectStore: imagegateway.NewFakeObjectStore(), err: errors.New("注入Head瞬时失败")}
	worker, err := NewImageObjectCleanupWorker(repo, store)
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return createdAt.Add(2 * time.Minute) }
	result, err := worker.RunBatch(context.Background(), 10)
	if err != nil || result.Quiescing != 1 || result.Retried != 0 || len(repo.failures) != 0 || len(repo.reschedules) != 1 || repo.tasks[0].LastErrorClass == nil || *repo.tasks[0].LastErrorClass != reason {
		t.Fatalf("静默窗Head失败必须保留PutUnknown原因: result=%+v task=%+v failures=%v reschedules=%v err=%v", result, repo.tasks[0], repo.failures, repo.reschedules, err)
	}
}

func TestImageObjectCleanupWorkerRetriesAndStopsAtEightFailures(t *testing.T) {
	now := time.Date(2026, 8, 26, 9, 30, 0, 0, time.UTC)
	for _, test := range []struct {
		retryCount uint64
		wantStatus string
		wantDelay  time.Duration
	}{
		{retryCount: 0, wantStatus: "retry", wantDelay: time.Minute},
		{retryCount: 7, wantStatus: "dead", wantDelay: time.Hour},
	} {
		repo := newMemoryImageObjectCleanupTaskStore(now, test.retryCount)
		store := &recordingObjectDeleteStore{ObjectStore: imagegateway.NewFakeObjectStore(), err: errors.New("注入对象删除失败")}
		worker, err := NewImageObjectCleanupWorker(repo, store)
		if err != nil {
			t.Fatal(err)
		}
		worker.now = func() time.Time { return now }
		result, err := worker.RunBatch(context.Background(), 10)
		if err != nil || result.Retried != 1 || len(repo.failures) != 1 {
			t.Fatalf("删除失败必须进入有界重试: result=%+v failures=%+v err=%v", result, repo.failures, err)
		}
		failure := repo.failures[0]
		if failure.errorClass != "object_delete_failed" || failure.next != now.Add(test.wantDelay) || repo.tasks[0].Status != test.wantStatus || repo.tasks[0].RetryCount != test.retryCount+1 {
			t.Fatalf("第八次失败边界错误: failure=%+v task=%+v", failure, repo.tasks[0])
		}
	}
}

func TestImageObjectCleanupWorkerRejectsForgedDescriptorWithoutDelete(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	repo := newMemoryImageObjectCleanupTaskStore(now, 0)
	repo.resolveErr = repository.ErrImageObjectCleanupInvalid
	store := &recordingObjectDeleteStore{ObjectStore: imagegateway.NewFakeObjectStore()}
	worker, err := NewImageObjectCleanupWorker(repo, store)
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return now }
	result, err := worker.RunBatch(context.Background(), 10)
	if err != nil || result.Retried != 1 || len(store.refs) != 0 || len(repo.failures) != 1 || repo.failures[0].errorClass != "object_cleanup_descriptor_invalid" {
		t.Fatalf("伪造描述符不得触发对象删除: result=%+v refs=%v failures=%+v err=%v", result, store.refs, repo.failures, err)
	}
}

func TestImageObjectCleanupWorkerBoundsEachDeleteCall(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 15, 0, 0, time.UTC)
	repo := newMemoryImageObjectCleanupTaskStore(now, 0)
	store := &blockingObjectDeleteStore{ObjectStore: imagegateway.NewFakeObjectStore()}
	worker, err := NewImageObjectCleanupWorker(repo, store)
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return now }
	worker.deleteTimeout = 10 * time.Millisecond
	started := time.Now()
	result, err := worker.RunBatch(context.Background(), 10)
	if err != nil || time.Since(started) > time.Second || result.Retried != 1 || len(repo.failures) != 1 || repo.failures[0].errorClass != "object_delete_failed" {
		t.Fatalf("MinIO挂起必须在单对象超时后进入重试: elapsed=%s result=%+v failures=%+v err=%v", time.Since(started), result, repo.failures, err)
	}
}

func TestImageObjectCleanupWorkerDeletesPutThatArrivesAfterInitialNotFound(t *testing.T) {
	createdAt := time.Date(2026, 8, 26, 10, 20, 0, 0, time.UTC)
	repo := newMemoryImageObjectCleanupTaskStore(createdAt, 0)
	reason := string(imagegateway.ObjectCleanupAfterResultPutUnknown)
	repo.tasks[0].LastErrorClass = &reason
	repo.tasks[0].CreatedAt = createdAt
	baseStore := imagegateway.NewFakeObjectStore()
	store := &recordingObjectDeleteStore{ObjectStore: baseStore}
	worker, err := NewImageObjectCleanupWorker(repo, store)
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return createdAt.Add(time.Minute) }
	result, err := worker.RunBatch(context.Background(), 10)
	if err != nil || result.Quiescing != 1 || result.Completed != 0 || len(repo.reschedules) != 1 || repo.tasks[0].RetryCount != 0 || len(store.refs) != 0 {
		t.Fatalf("Put未知首次NotFound必须保留tombstone且不消耗失败次数: result=%+v reschedules=%v task=%+v err=%v", result, repo.reschedules, repo.tasks[0], err)
	}
	if _, err := baseStore.Put(context.Background(), repo.ref, strings.NewReader("late-provider-object"), 1024); err != nil {
		t.Fatal(err)
	}
	secondLease := createdAt.Add(2 * time.Minute)
	repo.tasks[0].Status, repo.tasks[0].LockedAt = "running", &secondLease
	worker.now = func() time.Time { return secondLease }
	result, err = worker.RunBatch(context.Background(), 10)
	if err != nil || result.Completed != 1 || len(store.refs) != 1 || len(repo.completed) != 1 {
		t.Fatalf("迟到对象出现后必须幂等删除并完成: result=%+v refs=%v completed=%v err=%v", result, store.refs, repo.completed, err)
	}
	if _, err := baseStore.Head(context.Background(), repo.ref); !errors.Is(err, imagegateway.ErrObjectNotFound) {
		t.Fatalf("迟到对象最终必须被删除: %v", err)
	}
}

func TestImageObjectCleanupWorkerCompletesPutUnknownAfterQuietWindowWithoutRetryPenalty(t *testing.T) {
	createdAt := time.Date(2026, 8, 26, 10, 40, 0, 0, time.UTC)
	repo := newMemoryImageObjectCleanupTaskStore(createdAt, 0)
	reason := string(imagegateway.ObjectCleanupAfterThumbnailPutUnknown)
	repo.tasks[0].LastErrorClass = &reason
	repo.tasks[0].CreatedAt = createdAt
	worker, err := NewImageObjectCleanupWorker(repo, &recordingObjectDeleteStore{ObjectStore: imagegateway.NewFakeObjectStore()})
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return createdAt.Add(time.Minute) }
	if result, err := worker.RunBatch(context.Background(), 10); err != nil || result.Quiescing != 1 {
		t.Fatalf("静默窗内必须继续观察: result=%+v err=%v", result, err)
	}
	finalLease := createdAt.Add(5 * time.Minute)
	repo.tasks[0].Status, repo.tasks[0].LockedAt = "running", &finalLease
	worker.now = func() time.Time { return finalLease }
	if result, err := worker.RunBatch(context.Background(), 10); err != nil || result.Completed != 1 || repo.tasks[0].RetryCount != 0 {
		t.Fatalf("五分钟连续NotFound应安全完成且不进入dead计数: result=%+v task=%+v err=%v", result, repo.tasks[0], err)
	}
}

func TestImageObjectCleanupWorkerStartStopsWithContext(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 30, 0, 0, time.UTC)
	repo := newMemoryImageObjectCleanupTaskStore(now, 0)
	repo.tasks = nil
	worker, err := NewImageObjectCleanupWorker(repo, &recordingObjectDeleteStore{ObjectStore: imagegateway.NewFakeObjectStore()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		worker.Start(ctx, time.Hour)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("对象回收Worker必须响应运行时停止信号")
	}
}

type objectCleanupFailure struct {
	next       time.Time
	errorClass string
}

type memoryImageObjectCleanupTaskStore struct {
	mu             sync.Mutex
	tasks          []model.AICompensationTask
	ref            imagegateway.ObjectRef
	resolveErr     error
	referenced     bool
	referenceErr   error
	blockReference bool
	completed      []uint64
	reschedules    []time.Time
	failures       []objectCleanupFailure
}

func newMemoryImageObjectCleanupTaskStore(now time.Time, retryCount uint64) *memoryImageObjectCleanupTaskStore {
	lease := now
	return &memoryImageObjectCleanupTaskStore{
		tasks: []model.AICompensationTask{{
			ID: 1, TaskType: "image_object_cleanup", Status: "running", RetryCount: retryCount,
			LockedAt: &lease, AggregateID: "d9b66751d38772a1e518f3e9a2ad11cb:7",
		}},
		ref: imagegateway.ObjectRef{Bucket: imagegateway.TemporaryObjectBucket, Key: "d9b66751d38772a1e518f3e9a2ad11cb/7/primary.png"},
	}
}

func (r *memoryImageObjectCleanupTaskStore) ClaimBatch(context.Context, time.Time, time.Time, int) ([]model.AICompensationTask, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]model.AICompensationTask(nil), r.tasks...), nil
}

func (r *memoryImageObjectCleanupTaskStore) ResolveObjectRef(model.AICompensationTask) (imagegateway.ObjectRef, error) {
	if r.resolveErr != nil {
		return imagegateway.ObjectRef{}, r.resolveErr
	}
	return r.ref, nil
}

func (r *memoryImageObjectCleanupTaskStore) HasAssetReference(ctx context.Context, _ imagegateway.ObjectRef) (bool, error) {
	if r.blockReference {
		<-ctx.Done()
		return false, ctx.Err()
	}
	if r.referenceErr != nil {
		return false, r.referenceErr
	}
	return r.referenced, nil
}

func (r *memoryImageObjectCleanupTaskStore) MarkCompleted(_ context.Context, id uint64, _ time.Time, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed = append(r.completed, id)
	r.tasks[0].Status = "completed"
	return nil
}

func (r *memoryImageObjectCleanupTaskStore) RescheduleQuiescence(_ context.Context, _ uint64, _ time.Time, next time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reschedules = append(r.reschedules, next)
	r.tasks[0].Status = "retry"
	r.tasks[0].LockedAt = nil
	return nil
}

func (r *memoryImageObjectCleanupTaskStore) MarkFailure(_ context.Context, _ uint64, _ time.Time, next time.Time, errorClass string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, objectCleanupFailure{next: next, errorClass: errorClass})
	r.tasks[0].RetryCount++
	if r.tasks[0].RetryCount >= 8 {
		r.tasks[0].Status = "dead"
	} else {
		r.tasks[0].Status = "retry"
	}
	return nil
}

type recordingObjectDeleteStore struct {
	imagegateway.ObjectStore
	err  error
	refs []imagegateway.ObjectRef
}

func (s *recordingObjectDeleteStore) Delete(ctx context.Context, ref imagegateway.ObjectRef) error {
	s.refs = append(s.refs, ref)
	if s.err != nil {
		return s.err
	}
	return s.ObjectStore.Delete(ctx, ref)
}

type blockingObjectDeleteStore struct {
	imagegateway.ObjectStore
}

type headErrorObjectStore struct {
	imagegateway.ObjectStore
	err error
}

func (s *headErrorObjectStore) Head(context.Context, imagegateway.ObjectRef) (imagegateway.StoredObject, error) {
	return imagegateway.StoredObject{}, s.err
}

func (s *blockingObjectDeleteStore) Delete(ctx context.Context, _ imagegateway.ObjectRef) error {
	<-ctx.Done()
	return ctx.Err()
}
