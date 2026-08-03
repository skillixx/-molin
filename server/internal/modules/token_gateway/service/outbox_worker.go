package service

import (
	"context"
	"errors"
	"log"
	"time"

	"molin/server/internal/modules/token_gateway/model"
)

type outboxStore interface {
	ClaimBatch(ctx context.Context, now, lockBefore time.Time, limit int) ([]model.AIOutboxEvent, error)
	MarkPublished(ctx context.Context, id uint64, lease time.Time, now time.Time) error
	MarkRetry(ctx context.Context, id uint64, lease time.Time, next time.Time, errorClass string, dead bool) error
}

// OutboxPublisher 只负责发布事件，不拥有任何钱包或请求状态修改权限。
type OutboxPublisher interface {
	Publish(ctx context.Context, event model.AIOutboxEvent) error
}

type OutboxWorker struct {
	store          outboxStore
	publisher      OutboxPublisher
	now            func() time.Time
	maxAttempts    uint32
	publishTimeout time.Duration
	publishSlots   chan struct{}
}

func NewOutboxWorker(store outboxStore, publisher OutboxPublisher) *OutboxWorker {
	// 第 18 次失败前累计退避超过 2.5 小时，覆盖两小时 Broker 中断验收窗口。
	return &OutboxWorker{store: store, publisher: publisher, now: time.Now, maxAttempts: 18, publishTimeout: 30 * time.Second, publishSlots: make(chan struct{}, 1)}
}

func (w *OutboxWorker) RunOnce(ctx context.Context, limit int) (int, error) {
	if w == nil || w.store == nil {
		return 0, errors.New("Outbox Worker 未装配")
	}
	now := w.now()
	events, err := w.store.ClaimBatch(ctx, now, now.Add(-2*time.Minute), limit)
	if err != nil {
		return 0, err
	}
	published := 0
	blockedAggregates := make(map[string]struct{})
	for _, event := range events {
		if _, blocked := blockedAggregates[event.AggregateType+":"+event.AggregateID]; blocked {
			continue
		}
		if event.LockedAt == nil {
			return published, errors.New("Outbox 事件缺少租约")
		}
		publishErr := errors.New("RabbitMQ 发布器未配置")
		if w.publisher != nil {
			publishCtx, cancel := context.WithTimeout(ctx, w.publishTimeout)
			result := make(chan error, 1)
			select {
			case w.publishSlots <- struct{}{}:
				go func() {
					defer func() { <-w.publishSlots }()
					result <- w.publisher.Publish(publishCtx, event)
				}()
				select {
				case publishErr = <-result:
				case <-publishCtx.Done():
					publishErr = publishCtx.Err()
				}
			case <-publishCtx.Done():
				publishErr = publishCtx.Err()
			}
			cancel()
		}
		if publishErr == nil {
			if err := w.store.MarkPublished(ctx, event.ID, *event.LockedAt, w.now()); err != nil {
				return published, err
			}
			published++
			continue
		}
		attempt := event.RetryCount + 1
		dead := attempt >= w.maxAttempts
		if err := w.store.MarkRetry(ctx, event.ID, *event.LockedAt, w.now().Add(outboxBackoff(attempt)), "publish_failed", dead); err != nil {
			return published, err
		}
		// 同一聚合的后续事件必须等待当前事件成功，避免消费者状态倒退。
		blockedAggregates[event.AggregateType+":"+event.AggregateID] = struct{}{}
	}
	return published, nil
}

func (w *OutboxWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := w.RunOnce(ctx, 50); err != nil {
			log.Printf("[token_gateway] G3 Outbox 扫描失败: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func outboxBackoff(attempt uint32) time.Duration {
	if attempt > 10 {
		attempt = 10
	}
	delay := time.Second * time.Duration(1<<attempt)
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

// SettlementWorker 只调用统一 G3 对账入口，禁止直接写钱包余额。
type SettlementWorker struct {
	billing *AIBillingService
}

func NewSettlementWorker(billing *AIBillingService) *SettlementWorker {
	return &SettlementWorker{billing: billing}
}

func (w *SettlementWorker) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if w != nil && w.billing != nil {
			if _, err := w.billing.ReconcileInterrupted(ctx, 100); err != nil {
				log.Printf("[token_gateway] G3 结算对账扫描失败: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
