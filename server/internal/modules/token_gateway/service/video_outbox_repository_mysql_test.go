package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG7OutboxScopeMySQL 使用真实预占事务生成视频事实，验证视频领取不会抢占或回写旧事件。
func TestVideoG7OutboxScopeMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	ctx := context.Background()
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	control := model.AIOutboxEvent{EventID: "g7_control_" + f.command.RequestID, AggregateType: "image_request", AggregateID: f.command.RequestID, EventType: "image.completed", PayloadJSON: json.RawMessage(`{}`), Status: model.AIOutboxPending, NextRetryAt: now}
	if err := db.Create(&control).Error; err != nil {
		t.Fatal(err)
	}
	videoRepo := repository.NewVideoOutboxRepository(db)
	legacyRepo := repository.NewG3OutboxRepository(db)
	videoEvents, err := videoRepo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1000)
	if err != nil {
		t.Fatal(err)
	}
	var held model.AIOutboxEvent
	for _, event := range videoEvents {
		if event.AggregateType != "video_request" || event.ID == control.ID {
			t.Fatal("视频入口不得领取图片事件")
		}
		if event.AggregateID == f.command.RequestID {
			held = event
		}
	}
	if held.ID == 0 || held.LockedAt == nil {
		t.Fatal("视频预占事件必须可显式领取")
	}
	legacyEvents, err := legacyRepo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1000)
	if err != nil {
		t.Fatal(err)
	}
	var claimedControl model.AIOutboxEvent
	for _, event := range legacyEvents {
		if event.AggregateType == "video_request" {
			t.Fatal("旧发布器仍须排除视频事件")
		}
		if event.ID == control.ID {
			claimedControl = event
		}
	}
	if claimedControl.LockedAt == nil {
		t.Fatal("视频领取不能阻断图片事件")
	}
	if err := videoRepo.MarkPublished(ctx, control.ID, *claimedControl.LockedAt, now); !errors.Is(err, repository.ErrOutboxLeaseLost) {
		t.Fatalf("不得跨范围标记发布: %v", err)
	}
	if err := videoRepo.MarkRetry(ctx, control.ID, *claimedControl.LockedAt, now, "publish_failed", true); !errors.Is(err, repository.ErrOutboxLeaseLost) {
		t.Fatalf("不得跨范围重试: %v", err)
	}
	if err := legacyRepo.MarkRetry(ctx, control.ID, *claimedControl.LockedAt, now, "publish_failed", true); err != nil {
		t.Fatal(err)
	}
	if err := videoRepo.RequeueDead(ctx, control.EventID, now); !errors.Is(err, repository.ErrOutboxLeaseLost) {
		t.Fatalf("不得跨范围重排: %v", err)
	}
	if err := legacyRepo.RequeueDead(ctx, control.EventID, now); err != nil {
		t.Fatal(err)
	}
	if err := videoRepo.MarkRetry(ctx, held.ID, *held.LockedAt, now, "publish_failed", true); err != nil {
		t.Fatal(err)
	}
	if err := videoRepo.RequeueDead(ctx, held.EventID, now); err != nil {
		t.Fatal(err)
	}
	if err := videoRepo.MarkPublished(ctx, held.ID, *held.LockedAt, now); !errors.Is(err, repository.ErrOutboxLeaseLost) {
		t.Fatal("重排后的旧租约不能写入")
	}
}

// TestVideoG7OutboxConcurrentClaimMySQL 验证百路认领只有一名持有者，过期接管拒绝旧令牌且保留同一事件。
func TestVideoG7OutboxConcurrentClaimMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	ctx := context.Background()
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	// 只限定本夹具聚合，所有并发调用仍经过真实行锁与共享表，不把存储替换为内存。
	scopedDB := db.Where("aggregate_id = ?", f.command.RequestID)
	repo := repository.NewVideoOutboxRepository(scopedDB)
	now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	start := make(chan struct{})
	winners := make(chan model.AIOutboxEvent, 100)
	errs := make(chan error, 100)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
			if err != nil {
				errs <- err
				return
			}
			for _, event := range events {
				winners <- event
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
	first := <-winners
	later := now.Add(3 * time.Minute)
	events, err := repo.ClaimBatch(ctx, later, later.Add(-2*time.Minute), 1)
	if err != nil || len(events) != 1 || events[0].ID != first.ID {
		t.Fatalf("接管必须复用原事件: %v", err)
	}
	if err := repo.MarkPublished(ctx, first.ID, *first.LockedAt, later); !errors.Is(err, repository.ErrOutboxLeaseLost) {
		t.Fatalf("旧持有者不得覆盖接管结果: %v", err)
	}
	if err := repo.MarkPublished(ctx, events[0].ID, *events[0].LockedAt, later); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkPublished(ctx, events[0].ID, *events[0].LockedAt, later); !errors.Is(err, repository.ErrOutboxLeaseLost) {
		t.Fatal("重复确认不能改变已发布事实")
	}
}

// TestVideoG7OutboxSameSecondRequeueMySQL 防止同秒重排重新取得相同时间令牌，让迟到旧持有者误认新租约。
func TestVideoG7OutboxSameSecondRequeueMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	ctx := context.Background()
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id = ?", f.command.RequestID))
	now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	first, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("首次领取失败: %v", err)
	}
	if err := repo.MarkRetry(ctx, first[0].ID, *first[0].LockedAt, now, "publish_failed", true); err != nil {
		t.Fatal(err)
	}
	// 既有管理员入口仍能重排同一事件，不能清掉视频最后租约的防重用信息。
	if err := repository.NewG3OutboxRepository(db).RequeueDead(ctx, first[0].EventID, now); err != nil {
		t.Fatal(err)
	}
	second, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
	if err != nil || len(second) != 1 || second[0].ID != first[0].ID {
		t.Fatalf("同事件重领失败: %v", err)
	}
	if err := repo.MarkPublished(ctx, first[0].ID, *first[0].LockedAt, now); !errors.Is(err, repository.ErrOutboxLeaseLost) {
		t.Fatalf("同秒重排不得复用旧租约令牌: %v", err)
	}
	if err := repo.MarkPublished(ctx, second[0].ID, *second[0].LockedAt, now); err != nil {
		t.Fatal(err)
	}
}

// TestVideoG7OutboxAggregateOrderMySQL 通过真实取消链形成多个财务事件，前序失败或死亡时后序不能抢跑。
func TestVideoG7OutboxAggregateOrderMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	ctx := context.Background()
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CancelBeforeSubmit(ctx, f.command.TaskID, f.owner); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id = ?", f.command.RequestID))
	now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	first, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 10)
	if err != nil || len(first) != 1 || first[0].EventType != "video_billing_held" {
		t.Fatalf("只能先领取预占事件: %v", err)
	}
	if err := repo.MarkRetry(ctx, first[0].ID, *first[0].LockedAt, now, "publish_failed", true); err != nil {
		t.Fatal(err)
	}
	blocked, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 10)
	if err != nil || len(blocked) != 0 {
		t.Fatalf("前序死亡必须阻止后序: %v", err)
	}
	if err := repo.RequeueDead(ctx, first[0].EventID, now); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"video_billing_held", "video_billing_released", "video_delivery_rejected"} {
		events, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 10)
		if err != nil || len(events) != 1 || events[0].EventType != kind {
			t.Fatalf("聚合事件顺序错误，期望%s: %v", kind, err)
		}
		if err := repo.MarkPublished(ctx, events[0].ID, *events[0].LockedAt, now); err != nil {
			t.Fatal(err)
		}
	}
	done, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 10)
	if err != nil || len(done) != 0 {
		t.Fatalf("已发布事件不得再次领取: %v", err)
	}
}

// TestVideoG7OutboxRetryHighWaterMySQL 覆盖普通重试、旧入口回写、连续同秒租约及未来高水位接管边界。
func TestVideoG7OutboxRetryHighWaterMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	ctx := context.Background()
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id = ?", f.command.RequestID))
	legacy := repository.NewG3OutboxRepository(db)
	now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	current, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
	if err != nil || len(current) != 1 {
		t.Fatalf("初次领取失败: %v", err)
	}
	for i := 0; i < 6; i++ {
		old := current[0]
		writer := repo
		if i%2 == 1 {
			writer = legacy
		}
		if err := writer.MarkRetry(ctx, old.ID, *old.LockedAt, now, "publish_failed", false); err != nil {
			t.Fatal(err)
		}
		current, err = repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 1)
		if err != nil || len(current) != 1 || current[0].ID != old.ID || !current[0].LockedAt.After(*old.LockedAt) {
			t.Fatalf("普通重试令牌必须严格递增: %v", err)
		}
		if err := writer.MarkRetry(ctx, old.ID, *old.LockedAt, now, "publish_failed", true); !errors.Is(err, repository.ErrOutboxLeaseLost) {
			t.Fatalf("旧令牌不得将新租约写死: %v", err)
		}
		if err := repo.MarkPublished(ctx, old.ID, *old.LockedAt, now); !errors.Is(err, repository.ErrOutboxLeaseLost) {
			t.Fatal("旧令牌不得确认新发布")
		}
	}
	// 以数据库保存的高水位而非原墙钟计算两分钟接管边界，恰到边界仍不抢占。
	boundary := current[0].LockedAt.Add(2 * time.Minute)
	early, err := repo.ClaimBatch(ctx, boundary, boundary.Add(-2*time.Minute), 1)
	if err != nil || len(early) != 0 {
		t.Fatalf("高水位边界不能提前接管: %v", err)
	}
	late, err := repo.ClaimBatch(ctx, boundary.Add(time.Second), boundary.Add(-2*time.Minute).Add(time.Second), 1)
	if err != nil || len(late) != 1 || late[0].ID != current[0].ID {
		t.Fatalf("超过高水位边界应接管: %v", err)
	}
	if err := repo.MarkPublished(ctx, current[0].ID, *current[0].LockedAt, boundary); !errors.Is(err, repository.ErrOutboxLeaseLost) {
		t.Fatal("接管后高水位旧令牌必须失效")
	}
	if err := repo.MarkPublished(ctx, late[0].ID, *late[0].LockedAt, boundary.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

// TestVideoG7OutboxBatchTokensMySQL 验证同批不同历史高水位各自返回正确令牌，没有循环指针复用或批量错写。
func TestVideoG7OutboxBatchTokensMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	a := newVideoG5ReservationFixture(t, db, "10")
	b := newVideoG5ReservationFixture(t, db, "10")
	ctx := context.Background()
	for _, f := range []videoG5ReservationFixture{a, b} {
		if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	aRepo := repository.NewVideoOutboxRepository(db.Where("aggregate_id = ?", a.command.RequestID))
	first, err := aRepo.ClaimBatch(ctx, now.Add(time.Minute), now.Add(-time.Minute), 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("预置未来租约失败: %v", err)
	}
	if err := aRepo.MarkRetry(ctx, first[0].ID, *first[0].LockedAt, now, "publish_failed", false); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewVideoOutboxRepository(db.Where("aggregate_id IN ?", []string{a.command.RequestID, b.command.RequestID}))
	batch, err := repo.ClaimBatch(ctx, now, now.Add(-2*time.Minute), 2)
	if err != nil || len(batch) != 2 {
		t.Fatalf("批量领取失败: %v", err)
	}
	seen := make(map[string]bool)
	for _, event := range batch {
		want := now
		if event.AggregateID == a.command.RequestID {
			want = now.Add(time.Minute + time.Second)
		} else if event.AggregateID != b.command.RequestID {
			t.Fatal("批量领取混入其他聚合")
		}
		if seen[event.AggregateID] || event.LockedAt == nil || !event.LockedAt.Equal(want) {
			t.Fatal("每个聚合必须返回自己的高水位令牌")
		}
		seen[event.AggregateID] = true
		if err := repo.MarkPublished(ctx, event.ID, *event.LockedAt, now); err != nil {
			t.Fatalf("返回令牌必须与数据库完全一致: %v", err)
		}
	}
}
