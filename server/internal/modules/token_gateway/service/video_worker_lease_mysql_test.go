package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG7WorkerLeaseMySQL 使用原G5任务验证百路唯一租约、续期、释放和旧证明失效，不建立影子任务。
func TestVideoG7WorkerLeaseMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	ctx := context.Background()
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	before := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
	repo := repository.NewVideoWorkerLeaseRepository(db)
	start := make(chan struct{})
	winners := make(chan *repository.VideoWorkerLease, 100)
	errs := make(chan error, 100)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			p, err := repo.Claim(ctx, f.command.TaskID, f.owner, "worker-one", "submit")
			if err == nil {
				winners <- p
			} else if !errors.Is(err, repository.ErrVideoWorkerLeaseBusy) {
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(winners)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if len(winners) != 1 {
		t.Fatalf("百路认领必须只有一个持有者，实际%d", len(winners))
	}
	proof := <-winners
	if proof.Version() != 1 {
		t.Fatal("首次租约版本必须为1")
	}
	if err := repo.Validate(ctx, proof); err != nil {
		t.Fatal(err)
	}
	renewed, err := repo.Renew(ctx, proof)
	if err != nil || renewed.Version() != 1 || renewed.Deadline().Before(proof.Deadline()) {
		t.Fatalf("续期必须保留代次并延长期限: %v", err)
	}
	if err := repo.Release(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	if err := repo.Validate(ctx, proof); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
		t.Fatal("释放后旧证明必须失效")
	}
	next, err := repo.Claim(ctx, f.command.TaskID, f.owner, "worker-one", "submit")
	if err != nil || next.Version() != 2 {
		t.Fatalf("同worker名称重领也必须换代: %v", err)
	}
	if _, err := repo.Renew(ctx, proof); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
		t.Fatal("旧证明不得续期新租约")
	}
	if err := repo.Release(ctx, proof); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
		t.Fatal("旧证明不得释放新租约")
	}
	wrong := f.owner
	wrong.ProjectID++
	if _, err := repo.Claim(ctx, f.command.TaskID, wrong, "other", "submit"); !errors.Is(err, repository.ErrVideoTaskNotFound) {
		t.Fatal("跨Project必须统一不存在")
	}
	if err := repo.Release(ctx, next); err != nil {
		t.Fatal(err)
	}
	if string(before) != string(mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
		t.Fatal("操作性租约不得改变财务/Quote/Outbox")
	}
	var task model.AIImageTask
	if err := db.Where("public_id=?", f.command.TaskID).Take(&task).Error; err != nil || task.VersionNo != 1 || task.Status != model.AIImageTaskReserved {
		t.Fatalf("技术租约不得改业务状态版本: %v", err)
	}
	var count int64
	if err := db.Table("ai_gateway_task_events").Where("task_id=? AND event_type IN ('video_worker_lease_claimed','video_worker_lease_released')", task.ID).Count(&count).Error; err != nil || count != 4 {
		t.Fatalf("两次认领释放须追加四条事实: %d %v", count, err)
	}
}

// TestVideoG7WorkerLeaseExpiryMySQL 实际等待30秒租约到期，旧执行者不能续约或释放，新持有者必须递增代次。
func TestVideoG7WorkerLeaseExpiryMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	ctx := context.Background()
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewVideoWorkerLeaseRepository(db)
	first, err := repo.Claim(ctx, f.command.TaskID, f.owner, "before-crash", "submit")
	if err != nil {
		t.Fatal(err)
	}
	// 不修改数据库时间或期限，期限来自真实数据库时钟；这不是进程kill测试。
	time.Sleep(time.Until(first.Deadline()) + 150*time.Millisecond)
	if _, err := repo.Renew(ctx, first); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
		t.Fatal("过期证明不得复活")
	}
	if err := repo.Release(ctx, first); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
		t.Fatal("过期证明不得清掉原租约事实")
	}
	second, err := repo.Claim(ctx, f.command.TaskID, f.owner, "after-crash", "submit")
	if err != nil || second.Version() != 2 {
		t.Fatalf("过期接管失败: %v", err)
	}
	if err := repo.Validate(ctx, first); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
		t.Fatal("接管后旧证明仍须失效")
	}
	if err := repo.Release(ctx, second); err != nil {
		t.Fatal(err)
	}
}

// TestVideoG7WorkerLeaseFencesTaskMySQL 进入租约管理后，普通和迟到Worker不能凭新的业务version冒充当前持有者。
func TestVideoG7WorkerLeaseFencesTaskMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	leases := repository.NewVideoWorkerLeaseRepository(db)
	proof, err := leases.Claim(ctx, f.command.TaskID, f.owner, "submit-worker", "submit")
	if err != nil {
		t.Fatal(err)
	}
	tasks := repository.NewVideoTaskRepository(db)
	command := repository.VideoStateTransition{TaskPublicID: f.command.TaskID, Owner: f.owner, ExpectedVersion: 1, ToStatus: model.AIImageTaskQueued, Progress: 10, EventID: f.command.RequestID + "_queued", Source: "worker", Now: time.Now().UTC()}
	if _, err := tasks.TransitionExecution(ctx, command); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
		t.Fatalf("没有租约证明的Worker必须拒绝: %v", err)
	}
	owned := repository.WithVideoWorkerLease(ctx, proof)
	queued, err := tasks.TransitionExecution(owned, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := leases.Release(ctx, proof); err != nil {
		t.Fatal(err)
	}
	second, err := leases.Claim(ctx, f.command.TaskID, f.owner, "submit-worker", "submit")
	if err != nil {
		t.Fatal(err)
	}
	command.ExpectedVersion = queued.VersionNo
	command.ToStatus = model.AIImageTaskSubmitting
	command.Progress = 15
	command.EventID = f.command.RequestID + "_submitting"
	if _, err := tasks.TransitionExecution(owned, command); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
		t.Fatalf("旧租约即使知道新业务版本也不能写入: %v", err)
	}
	current := repository.WithVideoWorkerLease(ctx, second)
	submitting, err := tasks.TransitionExecution(current, command)
	if err != nil {
		t.Fatal(err)
	}
	binding := repository.VideoProviderBinding{TaskPublicID: f.command.TaskID, Owner: f.owner, ExpectedVersion: submitting.VersionNo, ProviderCode: "fake-native-async", ProviderTaskID: "taskUUID-" + f.command.TaskID, EventID: f.command.RequestID + "_bound", Now: time.Now().UTC()}
	if _, err := tasks.BindProviderTask(ctx, binding); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
		t.Fatalf("绑定也必须验证租约: %v", err)
	}
	if _, err := tasks.BindProviderTask(current, binding); err != nil {
		t.Fatal(err)
	}
	if err := leases.Release(ctx, second); err != nil {
		t.Fatal(err)
	}
}
