package service

import (
	"context"
	"database/sql"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// 对账运行汇总仅报告原持久化积压与冻结金额；全零不代表请求已逐项对账通过。
type VideoAdminReconciliationSummary struct {
	SettlementPending    int64  `json:"settlement_pending"`
	ActiveCompensations  int64  `json:"active_compensations"`
	DeadCompensations    int64  `json:"dead_compensations"`
	OutboxPending        int64  `json:"outbox_pending"`
	OutboxDead           int64  `json:"outbox_dead"`
	UnreleasedHoldAmount string `json:"unreleased_hold_amount"`
}

func (s *VideoAdminService) ReconciliationSummary(ctx context.Context, caller VideoCaller) (*VideoAdminReconciliationSummary, error) {
	if s == nil || s.app == nil || s.app.db == nil {
		return nil, ErrVideoAccessUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := &VideoAdminReconciliationSummary{UnreleasedHoldAmount: "0.00000000"}
	err := s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.authorizeTx(ctx, tx, caller, "ai_gateway:reconcile_manage"); err != nil {
			return err
		}
		// 不按目标用户当前启停过滤历史；所有计数保持同一RR快照，不运行任何恢复或结算协调器。
		if err := tx.Table("ai_requests").Where("modality='video' AND capability='video.generate' AND billing_status='settlement_pending'").Count(&result.SettlementPending).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		for _, q := range []struct {
			table, where string
			target       *int64
		}{
			{"ai_compensation_tasks", "task_type='video_reconcile' AND status IN ('pending','running','retry','manual_review')", &result.ActiveCompensations},
			{"ai_compensation_tasks", "task_type='video_reconcile' AND status='dead'", &result.DeadCompensations},
			{"ai_outbox_events", "aggregate_type='video_request' AND status IN ('pending','publishing')", &result.OutboxPending},
			{"ai_outbox_events", "aggregate_type='video_request' AND status='dead'", &result.OutboxDead},
		} {
			if err := tx.Table(q.table).Where(q.where).Count(q.target).Error; err != nil {
				return ErrVideoAccessUnavailable
			}
		}
		// 不能因已预占请求的Link/Hold缺失、归属错绑或金额漂移，返回看似正常的零冻结金额。
		var invalid int64
		if err := tx.Raw(`SELECT COUNT(*) FROM ai_requests r
LEFT JOIN ai_request_wallet_links l ON l.request_id=r.request_id
LEFT JOIN wallet_holds h ON h.id=l.wallet_hold_id
LEFT JOIN wallets w ON w.id=l.wallet_id
WHERE r.modality='video' AND r.capability='video.generate' AND (
 (r.billing_status NOT IN ('unquoted','quoted') AND l.id IS NULL) OR
 (l.id IS NOT NULL AND (h.id IS NULL OR w.id IS NULL OR h.user_id<>r.user_id OR w.user_id<>r.user_id OR h.wallet_id<>l.wallet_id OR h.hold_amount<>l.held_amount OR w.currency<>'CNY' OR h.status NOT IN ('holding','settled','released')))
)`).Scan(&invalid).Error; err != nil || invalid != 0 {
			return ErrVideoAccessUnavailable
		}
		// 每份原Hold只能被一个视频请求关联；发现异常时失败关闭，不能用DISTINCT隐藏重复关联。
		if err := tx.Raw(`SELECT COUNT(*) FROM (
 SELECT l.wallet_hold_id FROM ai_request_wallet_links l JOIN ai_requests r ON r.request_id=l.request_id
 WHERE r.modality='video' AND r.capability='video.generate'
 GROUP BY l.wallet_hold_id HAVING COUNT(*)<>1
) duplicate_holds`).Scan(&invalid).Error; err != nil || invalid != 0 {
			return ErrVideoAccessUnavailable
		}
		var amount decimal.Decimal
		if err := tx.Raw(`SELECT COALESCE(SUM(h.hold_amount),0)
FROM wallet_holds h JOIN ai_request_wallet_links l ON l.wallet_hold_id=h.id
JOIN ai_requests r ON r.request_id=l.request_id
WHERE r.modality='video' AND r.capability='video.generate' AND h.status='holding'`).Scan(&amount).Error; err != nil || amount.IsNegative() {
			return ErrVideoAccessUnavailable
		}
		result.UnreleasedHoldAmount = amount.StringFixed(8)
		return s.authorizeTx(ctx, tx, caller, "ai_gateway:reconcile_manage")
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	return result, nil
}
