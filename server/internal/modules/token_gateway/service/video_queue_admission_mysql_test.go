package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

func videoQueueAdmissionSnapshot(t *testing.T, f videoG6I2VFixture) []byte {
	t.Helper()
	result := map[string]any{}
	for _, table := range []string{"ai_requests", "ai_gateway_quotes", "ai_gateway_tasks", "ai_gateway_task_inputs", "wallet_holds", "ai_budget_reservations", "ai_outbox_events", "ai_video_rights_declarations"} {
		query := f.legacy.db.Table(table)
		switch table {
		case "ai_gateway_task_inputs":
			query = query.Where("task_id IN (SELECT id FROM ai_gateway_tasks WHERE user_id=?)", f.command.Caller.UserID)
		case "ai_outbox_events":
			query = query.Where("aggregate_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)", f.command.Caller.UserID)
		default:
			query = query.Where("user_id=?", f.command.Caller.UserID)
		}
		var count int64
		if err := query.Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		result[table] = count
	}
	var wallet map[string]any
	if err := f.legacy.db.Table("wallets").Select("balance_amount,frozen_amount,version").Where("user_id=?", f.command.Caller.UserID).Take(&wallet).Error; err != nil {
		t.Fatal(err)
	}
	result["wallet"] = wallet
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVideoG6QueueAdmissionFailureZeroFactsMySQL(t *testing.T) {
	t.Run("用户容量拒绝", func(t *testing.T) {
		f := newVideoG6I2VFixture(t)
		for index := 0; index < 2; index++ {
			if _, err := f.app.Create(t.Context(), VideoCommand{Caller: f.command.Caller, IdempotencyKey: fmt.Sprintf("g6-queue-zero-winner-%04d", index), Model: f.command.Model, Prompt: fmt.Sprintf("合成队列零事实赢家%d", index), Operation: model.AIVideoOperationTextToVideo, Facade: "openai"}); err != nil {
				t.Fatal(err)
			}
		}
		before := videoQueueAdmissionSnapshot(t, f)
		_, err := f.app.Create(t.Context(), VideoCommand{Caller: f.command.Caller, IdempotencyKey: "g6-queue-zero-rejected-0001", Model: f.command.Model, Prompt: "合成队列完整回滚", Operation: model.AIVideoOperationTextToVideo, Facade: "openai"})
		var limit *VideoQueueLimitError
		if !errors.As(err, &limit) || limit.Scope != "user" {
			t.Fatalf("用户容量必须明确拒绝：%v", err)
		}
		if after := videoQueueAdmissionSnapshot(t, f); string(after) != string(before) {
			t.Fatalf("队列拒绝必须零事实变化：before=%s after=%s", before, after)
		}
	})

	t.Run("门闩损坏失败关闭", func(t *testing.T) {
		f := newVideoG6I2VFixture(t)
		var injected atomic.Bool
		const hook = "g6_queue_guard_read_failure"
		if err := f.legacy.db.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
			if tx.Error == nil && tx.Statement.Table == "ai_video_queue_admission_guard" && injected.CompareAndSwap(false, true) {
				tx.AddError(errors.New("合成队列门闩读取故障"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		defer f.legacy.db.Callback().Query().Remove(hook)
		before := videoQueueAdmissionSnapshot(t, f)
		_, err := f.app.Create(t.Context(), VideoCommand{Caller: f.command.Caller, IdempotencyKey: "g6-queue-guard-fault-0001", Model: f.command.Model, Prompt: "合成门闩损坏", Operation: model.AIVideoOperationTextToVideo, Facade: "openai"})
		if !injected.Load() || !errors.Is(err, ErrVideoGovernanceUnavailable) {
			t.Fatalf("门闩损坏必须治理503语义：%v", err)
		}
		if err := f.legacy.db.Callback().Query().Remove(hook); err != nil {
			t.Fatal(err)
		}
		if after := videoQueueAdmissionSnapshot(t, f); string(after) != string(before) {
			t.Fatalf("门闩损坏必须零事实变化：before=%s after=%s", before, after)
		}
	})

	t.Run("准入末尾故障整笔回滚", func(t *testing.T) {
		f := newVideoG6I2VFixture(t)
		f.app.billing.fault = func(step string) error {
			if step == "queue_admission" {
				return errors.New("合成queue_admission末尾故障")
			}
			return nil
		}
		before := videoQueueAdmissionSnapshot(t, f)
		if _, err := f.app.Create(t.Context(), VideoCommand{Caller: f.command.Caller, IdempotencyKey: "g6-queue-tail-fault-0001", Model: f.command.Model, Prompt: "合成队列末尾故障", Operation: model.AIVideoOperationTextToVideo, Facade: "openai"}); err == nil {
			t.Fatal("准入末尾故障必须返回失败")
		}
		if after := videoQueueAdmissionSnapshot(t, f); string(after) != string(before) {
			t.Fatalf("准入末尾故障必须零事实变化：before=%s after=%s", before, after)
		}
	})
}

func TestVideoG6QueueAdmissionUserLimitMySQL(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	before := func(table string) int64 {
		t.Helper()
		var count int64
		if err := f.legacy.db.Table(table).Where("user_id=?", f.command.Caller.UserID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		return count
	}
	requestsBefore, tasksBefore, holdsBefore := before("ai_requests"), before("ai_gateway_tasks"), before("wallet_holds")
	var unconsumedBefore int64
	if err := f.legacy.db.Table("ai_gateway_quotes").Where("user_id=? AND consumed_request_id IS NULL", f.command.Caller.UserID).Count(&unconsumedBefore).Error; err != nil {
		t.Fatal(err)
	}
	create := func(index int) error {
		_, err := f.app.Create(t.Context(), VideoCommand{
			Caller: f.command.Caller, IdempotencyKey: fmt.Sprintf("g6-queue-user-%04d", index),
			Model: f.command.Model, Prompt: fmt.Sprintf("合成排队容量%d", index),
			Operation: model.AIVideoOperationTextToVideo, Facade: "openai",
		})
		return err
	}
	if err := create(1); err != nil {
		t.Fatal(err)
	}
	if err := create(2); err != nil {
		t.Fatal(err)
	}
	var limit *VideoQueueLimitError
	if err := create(3); !errors.As(err, &limit) || limit.Scope != "user" {
		t.Fatalf("用户第3个排队任务必须按user范围429: %v", err)
	}
	if got := before("ai_requests"); got != requestsBefore+2 {
		t.Fatalf("队列拒绝留下Request: got=%d want=%d", got, requestsBefore+2)
	}
	if got := before("ai_gateway_tasks"); got != tasksBefore+2 {
		t.Fatalf("队列拒绝留下Task: got=%d want=%d", got, tasksBefore+2)
	}
	if got := before("wallet_holds"); got != holdsBefore+2 {
		t.Fatalf("队列拒绝留下Hold: got=%d want=%d", got, holdsBefore+2)
	}
	var unconsumed int64
	if err := f.legacy.db.Table("ai_gateway_quotes").Where("user_id=? AND consumed_request_id IS NULL", f.command.Caller.UserID).Count(&unconsumed).Error; err != nil || unconsumed != unconsumedBefore {
		t.Fatalf("队列拒绝必须回滚Quote消费: before=%d after=%d err=%v", unconsumedBefore, unconsumed, err)
	}
}

func TestVideoG6QueueAdmissionConcurrentMySQL(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	var winners, rejected atomic.Int32
	var group sync.WaitGroup
	start := make(chan struct{})
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, err := f.app.Create(t.Context(), VideoCommand{
				Caller: f.command.Caller, IdempotencyKey: fmt.Sprintf("g6-queue-race-%04d", index),
				Model: f.command.Model, Prompt: fmt.Sprintf("合成排队竞争%d", index),
				Operation: model.AIVideoOperationTextToVideo, Facade: "openai",
			})
			var limit *VideoQueueLimitError
			switch {
			case err == nil:
				winners.Add(1)
			case errors.As(err, &limit) && limit.Scope == "user":
				rejected.Add(1)
			default:
				t.Errorf("并发排队返回异常: %v", err)
			}
		}(index)
	}
	close(start)
	group.Wait()
	if winners.Load() != 2 || rejected.Load() != 98 {
		t.Fatalf("两个用户槽位竞争错误: winners=%d rejected=%d", winners.Load(), rejected.Load())
	}
	for _, table := range []string{"ai_requests", "ai_gateway_tasks", "wallet_holds"} {
		var count int64
		if err := f.legacy.db.Table(table).Where("user_id=?", f.command.Caller.UserID).Count(&count).Error; err != nil || count != 2 {
			t.Fatalf("%s必须只有两个赢家事实: count=%d err=%v", table, count, err)
		}
	}
}

func TestVideoG6QueueAdmissionProjectAndGlobalLimitsMySQL(t *testing.T) {
	for _, test := range []struct {
		name, scope       string
		limits            videoQueueLimits
		allowed, rejected int
	}{
		{name: "Global第3个成功第4个拒绝", scope: "global", limits: videoQueueLimits{User: 100, Project: 100, Global: 3}, allowed: 3, rejected: 4},
		{name: "Project第10个成功第11个拒绝", scope: "project", limits: videoQueueLimits{User: 100, Project: 10, Global: 100}, allowed: 10, rejected: 11},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newVideoG6I2VFixture(t)
			var baseline int64
			if err := f.legacy.db.Table("ai_gateway_tasks").Where("capability=? AND status IN ?", model.AIVideoCapability, []string{model.AIImageTaskCreated, model.AIImageTaskReserved, model.AIImageTaskQueued}).Count(&baseline).Error; err != nil {
				t.Fatal(err)
			}
			if test.scope == "global" {
				test.limits.Global = uint64(baseline) + uint64(test.allowed)
			} else {
				// 给第11个Project任务也保留全局空间，确保拒绝一定来自project分支。
				test.limits.Global = uint64(baseline) + uint64(test.rejected)
			}
			f.app.billing.queue = &MySQLVideoQueueAdmission{limits: test.limits}
			for index := 1; index <= test.allowed; index++ {
				if _, err := f.app.Create(t.Context(), VideoCommand{Caller: f.command.Caller, IdempotencyKey: fmt.Sprintf("g6-queue-%s-%04d", test.scope, index), Model: f.command.Model, Prompt: fmt.Sprintf("合成%s容量%d", test.scope, index), Operation: model.AIVideoOperationTextToVideo, Facade: "openai"}); err != nil {
					t.Fatalf("第%d个任务应成功: %v", index, err)
				}
			}
			_, err := f.app.Create(t.Context(), VideoCommand{Caller: f.command.Caller, IdempotencyKey: fmt.Sprintf("g6-queue-%s-%04d", test.scope, test.rejected), Model: f.command.Model, Prompt: fmt.Sprintf("合成%s容量拒绝", test.scope), Operation: model.AIVideoOperationTextToVideo, Facade: "openai"})
			var limit *VideoQueueLimitError
			if !errors.As(err, &limit) || limit.Scope != test.scope {
				t.Fatalf("目标层未稳定拒绝: scope=%s err=%v", test.scope, err)
			}
			var tasks int64
			if err := f.legacy.db.Table("ai_gateway_tasks").Where("user_id=? AND capability='video.generate'", f.command.Caller.UserID).Count(&tasks).Error; err != nil || tasks != int64(test.allowed) {
				t.Fatalf("拒绝留下额外Task: tasks=%d err=%v", tasks, err)
			}
		})
	}
}

func TestVideoG6QueueAdmissionFrozenDefaults(t *testing.T) {
	if videoG6QueueUserLimit != 2 || videoG6QueueProjectLimit != 10 || videoG6QueueGlobalLimit != 100 {
		t.Fatalf("G0冻结queued阈值漂移: %d/%d/%d", videoG6QueueUserLimit, videoG6QueueProjectLimit, videoG6QueueGlobalLimit)
	}
}
