package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// TestVideoG7WorkerHeartbeatFailureMySQL 真实数据库拒绝首次续期，执行器必须取消工作且不能因work返回nil而成功。
func TestVideoG7WorkerHeartbeatFailureMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	if !videoG7WorkerLeaseDDLTarget(db) {
		t.Fatal("心跳故障触发器仅允许本轮绑定的临时MySQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	tasks := repository.NewVideoTaskRepository(db)
	task, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	finance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
	trigger := fmt.Sprintf("trg_vidg7_heartbeat_fault_%d", task.ID)
	// 只阻止本次Task同代次心跳变化，不阻止首次认领或保留心跳历史的释放。
	ddl := fmt.Sprintf("CREATE TRIGGER %s BEFORE UPDATE ON ai_gateway_tasks FOR EACH ROW BEGIN IF OLD.id=%d AND NEW.lease_version=OLD.lease_version AND NOT(NEW.heartbeat_at<=>OLD.heartbeat_at) THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic_heartbeat_failure'; END IF; END", trigger, task.ID)
	if err := db.Exec(ddl).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Exec("DROP TRIGGER " + trigger).Error; err != nil {
			t.Errorf("清理本轮心跳故障触发器失败: %v", err)
		}
	})
	runner, err := NewVideoWorkerLeaseRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	var initialHeartbeat time.Time
	started := time.Now()
	err = runner.Execute(ctx, VideoWorkerExecution{TaskID: f.command.TaskID, Owner: f.owner, WorkerID: "heartbeat-fault", Stage: video.TaskSubmit}, func(owned context.Context) error {
		ownedTask, err := tasks.FindForOwner(owned, f.command.TaskID, f.owner)
		if err != nil {
			return err
		}
		if ownedTask.WorkerHeartbeatAt == nil {
			return repository.ErrVideoWorkerLeaseLost
		}
		initialHeartbeat = *ownedTask.WorkerHeartbeatAt
		<-owned.Done()
		// 故意返回nil：上层仍必须传播续期失败与取消，不能ACK未确认的工作。
		return nil
	})
	if !errors.Is(err, repository.ErrVideoWorkerLeaseUnavailable) || !errors.Is(err, context.Canceled) {
		t.Fatalf("续期错误必须取消工作并失败关闭: %v", err)
	}
	if elapsed := time.Since(started); elapsed < 9*time.Second || elapsed > 25*time.Second {
		t.Fatalf("必须由第一轮10秒心跳触发取消，而非等到父context超时: %s", elapsed)
	}
	after, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
	if err != nil || after.WorkerLeaseActive || after.WorkerLeaseVersion != 1 || after.WorkerHeartbeatAt == nil || !after.WorkerHeartbeatAt.Equal(initialHeartbeat) {
		t.Fatalf("工作退出后释放技术租约，失败心跳不能写入半份记录: %v", err)
	}
	if !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
		t.Fatal("心跳失败不能改变冻结、Request、Quote、Usage或Outbox")
	}
}

// TestVideoG7WorkerHeartbeatExitMySQL 覆盖panic低敏收口，以及取消后仍未退出的旧工作被真实到期接管。
func TestVideoG7WorkerHeartbeatExitMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"panic", "cancel_then_takeover"} {
		t.Run(mode, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if mode == "cancel_then_takeover" {
				prepareVideoG5I2V(t, &f)
			}
			ctx := context.Background()
			if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
				t.Fatal(err)
			}
			finance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
			runner, err := NewVideoWorkerLeaseRunner(db)
			if err != nil {
				t.Fatal(err)
			}
			tasks := repository.NewVideoTaskRepository(db)
			leases := repository.NewVideoWorkerLeaseRepository(db)
			command := VideoWorkerExecution{TaskID: f.command.TaskID, Owner: f.owner, WorkerID: "old-worker", Stage: video.TaskSubmit}
			if mode == "panic" {
				marker := "合成受保护载荷不得出现在错误中"
				err := runner.Execute(ctx, command, func(context.Context) error { panic(marker) })
				if err == nil || !errors.Is(err, video.ErrTaskHandlerUncertain) || strings.Contains(err.Error(), marker) {
					t.Fatal("panic必须返回低敏不确定结果，不得传播正文")
				}
				after, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || after.WorkerLeaseActive || after.WorkerLeaseVersion != 1 {
					t.Fatalf("panic退出后应停止心跳并释放原技术租约: %v", err)
				}
			} else {
				inputs := repository.NewVideoTaskInputRepository(db)
				beforeInput, err := inputs.ListForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || len(beforeInput) != 1 || beforeInput[0].LeaseReleasedAt != nil {
					t.Fatalf("I2V在途输入应已绑定且受保护: %v", err)
				}
				parent, cancel := context.WithCancel(ctx)
				entered := make(chan *repository.VideoTaskRecord, 1)
				cancelObserved := make(chan struct{})
				allowReturn := make(chan struct{})
				finished := make(chan error, 1)
				oldWriteResult := make(chan error, 1)
				var releaseWork sync.Once
				defer func() {
					cancel()
					releaseWork.Do(func() { close(allowReturn) })
					select {
					case <-finished:
					case <-time.After(15 * time.Second):
						t.Error("测试工作未退出，不能遗留后台执行")
					}
				}()
				go func() {
					defer close(finished)
					finished <- runner.Execute(parent, command, func(owned context.Context) error {
						first, err := tasks.FindForOwner(owned, f.command.TaskID, f.owner)
						if err != nil {
							return err
						}
						entered <- first
						<-owned.Done()
						close(cancelObserved)
						// 合成不合作的工作，故意等测试放行；执行器不能假装已强杀此Go代码。
						<-allowReturn
						// 移除原取消但保留旧proof，再给这次拒绝性写入独立期限，防止异常数据库拖住测试。
						writeContext, cancelWrite := context.WithTimeout(context.WithoutCancel(owned), 5*time.Second)
						defer cancelWrite()
						_, err = tasks.TransitionExecution(writeContext, repository.VideoStateTransition{TaskPublicID: f.command.TaskID, Owner: f.owner, ExpectedVersion: 1, ToStatus: model.AIImageTaskQueued, Progress: 10, EventID: f.command.RequestID + "_stale_worker", Source: "worker", Now: time.Now().UTC()})
						oldWriteResult <- err
						return err
					})
				}()
				var first *repository.VideoTaskRecord
				select {
				case first = <-entered:
				case err := <-finished:
					t.Fatalf("工作尚未开始便退出: %v", err)
				case <-time.After(5 * time.Second):
					t.Fatal("工作未按时进入")
				}
				if first.WorkerLeaseUntil == nil {
					t.Fatal("缺少实际数据库租约截止")
				}
				cancel()
				select {
				case <-cancelObserved:
				case <-time.After(5 * time.Second):
					t.Fatal("取消没有传给工作")
				}
				if proof, err := leases.Claim(ctx, f.command.TaskID, f.owner, "too-early", "submit"); proof != nil || !errors.Is(err, repository.ErrVideoWorkerLeaseBusy) {
					t.Fatalf("工作未退出且租约未到期，不能提前释放: %v", err)
				}
				select {
				case <-finished:
					t.Fatal("工作未退出时Execute不得宣称完成")
				default:
				}
				// 心跳已随取消停止；真实等待原30秒到期，不改数据库时钟或租期。
				time.Sleep(time.Until(*first.WorkerLeaseUntil) + 200*time.Millisecond)
				next, err := leases.Claim(ctx, f.command.TaskID, f.owner, "new-worker", "submit")
				if err != nil || next.Version() != 2 {
					t.Fatalf("取消后的过期租约应可被新代次接管: %v", err)
				}
				releaseWork.Do(func() { close(allowReturn) })
				select {
				case err := <-finished:
					if !errors.Is(err, repository.ErrVideoWorkerLeaseLost) || !errors.Is(err, context.Canceled) {
						t.Fatalf("旧代码即使移除context取消，也不能写回或清理新租约: %v", err)
					}
				case <-time.After(15 * time.Second):
					t.Fatal("旧工作未按时退出")
				}
				// Execute合并工作与清理错误；这里单独验证CAS，不能把Release的LeaseLost当作写入拒绝证据。
				select {
				case writeErr := <-oldWriteResult:
					if !errors.Is(writeErr, repository.ErrVideoWorkerLeaseLost) {
						t.Fatalf("旧Task CAS本身必须被围栏拒绝: %v", writeErr)
					}
				default:
					t.Fatal("未观察到旧Task CAS执行结果")
				}
				unchanged, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || unchanged.Status != model.AIImageTaskReserved || unchanged.VersionNo != 1 || unchanged.WorkerLeaseVersion != 2 || !unchanged.WorkerLeaseActive {
					t.Fatalf("旧代码不得改变Task业务状态或新租约: %v", err)
				}
				if err := leases.Validate(ctx, next); err != nil {
					t.Fatalf("旧工作清理不能释放新持有者: %v", err)
				}
				if err := leases.Release(ctx, next); err != nil {
					t.Fatal(err)
				}
				afterInput, err := inputs.ListForOwner(ctx, f.command.TaskID, f.owner)
				if err != nil || !reflect.DeepEqual(beforeInput, afterInput) {
					t.Fatalf("取消和接管不能释放在途I2V输入: %v", err)
				}
			}
			if !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
				t.Fatal("异常退出或接管不得改变八表财务事实")
			}
		})
	}
}
