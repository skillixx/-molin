package service

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG7WorkerHeartbeatMySQL 实际经过10秒心跳和初始30秒期限，验证同代次仍可持证写入并有序退出。
func TestVideoG7WorkerHeartbeatMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	finance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
	tasks := repository.NewVideoTaskRepository(db)
	runner, err := NewVideoWorkerLeaseRunner(db)
	if err != nil {
		t.Fatal(err)
	}
	command := VideoWorkerExecution{TaskID: f.command.TaskID, Owner: f.owner, WorkerID: "heartbeat-worker", Stage: "submit"}
	var firstDeadline time.Time
	var transitionAt time.Time
	var firstRequestVersion uint64
	err = runner.Execute(ctx, command, func(owned context.Context) error {
		first, err := tasks.FindForOwner(owned, f.command.TaskID, f.owner)
		if err != nil {
			return err
		}
		if !first.WorkerLeaseActive || first.WorkerLeaseVersion != 1 || first.WorkerHeartbeatAt == nil || first.WorkerLeaseUntil == nil {
			t.Error("工作开始前必须持有真实数据库租约")
			return repository.ErrVideoWorkerLeaseLost
		}
		firstDeadline = *first.WorkerLeaseUntil
		firstRequestVersion = first.RequestVersionNo
		// 不注入虚拟时间；12秒观察窗要求第一轮10秒自动心跳已持久化。
		timer := time.NewTimer(12 * time.Second)
		defer timer.Stop()
		select {
		case <-owned.Done():
			return owned.Err()
		case <-timer.C:
		}
		middle, err := tasks.FindForOwner(owned, f.command.TaskID, f.owner)
		if err != nil {
			return err
		}
		if middle.WorkerHeartbeatAt == nil || middle.WorkerLeaseUntil == nil || !middle.WorkerHeartbeatAt.After(*first.WorkerHeartbeatAt) || middle.WorkerLeaseVersion != 1 || !middle.WorkerLeaseUntil.After(firstDeadline) {
			t.Error("自动心跳必须续期且保持原代次")
			return repository.ErrVideoWorkerLeaseLost
		}
		timer.Reset(time.Until(firstDeadline) + 200*time.Millisecond)
		select {
		case <-owned.Done():
			return owned.Err()
		case <-timer.C:
		}
		if !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
			t.Error("业务CAS前只有心跳发生，八表包括Request应完全不变")
			return repository.ErrVideoWorkerLeaseLost
		}
		// 原30秒截止之后仍用当前执行context，通过真实Task CAS证明续期不是仅更新内存。
		transitionAt = time.Now().UTC().Truncate(time.Second)
		_, err = tasks.TransitionExecution(owned, repository.VideoStateTransition{TaskPublicID: f.command.TaskID, Owner: f.owner, ExpectedVersion: 1, ToStatus: model.AIImageTaskQueued, Progress: 10, EventID: f.command.RequestID + "_heartbeat_queued", Source: "worker", Now: transitionAt})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := tasks.FindForOwner(ctx, f.command.TaskID, f.owner)
	if err != nil || after.WorkerLeaseActive || after.WorkerLeaseVersion != 1 || after.Status != model.AIImageTaskQueued || after.VersionNo != 2 {
		t.Fatalf("工作退出后停止心跳并保留已完成业务事实: %v", err)
	}
	if after.RequestVersionNo != firstRequestVersion+1 || after.RequestExecutionStatus != model.AIExecutionRunning {
		t.Fatal("Request只能发生对应queued的单次执行轴推进")
	}
	var requestClock struct{ UpdatedAt time.Time }
	if err := db.Table("ai_requests").Select("updated_at").Where("request_id=?", f.command.RequestID).Take(&requestClock).Error; err != nil || !requestClock.UpdatedAt.Equal(transitionAt) {
		t.Fatalf("Request更新时间只能来自该次业务CAS: %v", err)
	}
	assertVideoG7HeartbeatFinance(t, finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID))
	// 已退出的执行器不能占住租约，新持有者可以立即认领下一代。
	leases := repository.NewVideoWorkerLeaseRepository(db)
	next, err := leases.Claim(ctx, f.command.TaskID, f.owner, "next-worker", "submit")
	if err != nil || next.Version() != 2 {
		t.Fatalf("下一代认领失败: %v", err)
	}
	if err := leases.Release(ctx, next); err != nil {
		t.Fatal(err)
	}
}

// 七张资金/Quote/Usage/Outbox表完整比较；Request只排除上方已独立精确验证的三个执行轴字段。
// 不删除整张Request的校验，未列出的金额、计费、归属及扩展字段都必须原样保留。
func assertVideoG7HeartbeatFinance(t *testing.T, before, after []byte) {
	t.Helper()
	var first, last map[string][]string
	if json.Unmarshal(before, &first) != nil || json.Unmarshal(after, &last) != nil {
		t.Fatal("必须读取完整八表快照")
	}
	// 原快照函数对空表不生成JSON键；逐一核对固定八表，不能以键数把合法空Usage误判为遗漏。
	tables := []string{"wallets", "wallet_holds", "wallet_transactions", "ai_requests", "ai_gateway_quotes", "ai_usage_items", "ai_request_wallet_links", "ai_outbox_events"}
	allowed := make(map[string]bool, len(tables))
	for _, table := range tables {
		allowed[table] = true
	}
	for _, snapshot := range []map[string][]string{first, last} {
		for table := range snapshot {
			if !allowed[table] {
				t.Fatal("不接受合同外的快照表")
			}
		}
	}
	for _, table := range tables {
		rows := first[table]
		if table != "ai_requests" {
			if !reflect.DeepEqual(rows, last[table]) {
				t.Fatalf("心跳和业务执行CAS不得改变资金事实表: %s", table)
			}
			continue
		}
		if len(rows) != 1 || len(last[table]) != 1 {
			t.Fatal("原Request数量不得改变")
		}
		var oldRow, newRow map[string]json.RawMessage
		if json.Unmarshal([]byte(rows[0]), &oldRow) != nil || json.Unmarshal([]byte(last[table][0]), &newRow) != nil {
			t.Fatal("Request完整行快照无法解析")
		}
		for _, field := range []string{"execution_status", "version_no", "updated_at"} {
			if oldRow[field] == nil || newRow[field] == nil {
				t.Fatal("不能遗漏执行轴字段")
			}
			delete(oldRow, field)
			delete(newRow, field)
		}
		if !reflect.DeepEqual(oldRow, newRow) {
			t.Fatal("Request除已验证的执行轴字段外必须完全不变")
		}
	}
}
