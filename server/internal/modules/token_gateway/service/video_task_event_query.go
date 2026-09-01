package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
)

type VideoTaskEventView struct {
	EventID    string    `json:"event_id"`
	EventType  string    `json:"event_type"`
	Axis       string    `json:"axis"`
	FromStatus *string   `json:"from_status"`
	ToStatus   *string   `json:"to_status"`
	CreatedAt  time.Time `json:"created_at"`
}
type VideoTaskEventPage struct {
	Items    []VideoTaskEventView `json:"items"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
	Total    int64                `json:"total"`
}

// 先在SQL过滤客户可见类别及对应轴的状态，total也不能暴露隐藏的Provider成本/诊断事件。
func visibleVideoEvents(tx *gorm.DB, taskID, userID, projectID uint64) *gorm.DB {
	execution := []string{"created", "reserved", "queued", "submitting", "submitted", "processing", "fetching", "storing", "moderating", "labeling", "succeeded", "failed", "cancelled", "expired", "pending_reconcile"}
	billing := []string{"unquoted", "quoted", "held", "settlement_pending", "settled", "released", "adjusted"}
	delivery := []string{"pending", "available", "rejected", "expired"}
	return tx.Table("ai_gateway_task_events").Where("task_id=? AND user_id=? AND project_id=?", taskID, userID, projectID).Where(`(
(BINARY event_type IN ('execution_status_changed','provider_task_bound','video_billing_held') AND (from_status IS NULL OR BINARY from_status IN ?) AND (to_status IS NULL OR BINARY to_status IN ?)) OR
(BINARY event_type='billing_status_changed' AND (from_status IS NULL OR BINARY from_status IN ?) AND (to_status IS NULL OR BINARY to_status IN ?)) OR
(BINARY event_type='delivery_status_changed' AND (from_status IS NULL OR BINARY from_status IN ?) AND (to_status IS NULL OR BINARY to_status IN ?)) OR
BINARY event_type IN ('cancel_requested','video_reconciliation_review_required'))`, execution, execution, billing, billing, delivery, delivery)
}

func (s *VideoHTTPService) ListPlatformTaskEvents(ctx context.Context, caller VideoCaller, id string, page, size int) (*VideoTaskEventPage, error) {
	if s == nil || s.db == nil || s.access == nil {
		return nil, ErrVideoAccessUnavailable
	}
	if page < 1 || page > 10000 || size < 1 || size > 100 {
		return nil, ErrVideoListParameters
	}
	result := &VideoTaskEventPage{Items: []VideoTaskEventView{}, Page: page, PageSize: size}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, owner, err := s.taskForPlatformTx(ctx, tx, caller, id, false)
		if err != nil {
			return err
		}
		query := func() *gorm.DB {
			return visibleVideoEvents(tx, task.ID, owner.UserID, owner.ProjectID).Clauses(clause.Locking{Strength: "SHARE"})
		}
		if err := query().Select("count(*)").Count(&result.Total).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		var events []model.AIGatewayTaskEvent
		// 不读取原event_id、SafeDetailJSON或source，避免任何内部正文进入普通响应路径。
		if err := query().Select("id,event_type,from_status,to_status,created_at").Order("id ASC").Limit(size).Offset((page - 1) * size).Find(&events).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		for _, event := range events {
			v := VideoTaskEventView{EventID: "vevt_" + videoBillingDigest(fmt.Sprintf("%s/%d", task.PublicID, event.ID)), Axis: "execution", EventType: "execution_status_changed", FromStatus: event.FromStatus, ToStatus: event.ToStatus, CreatedAt: event.CreatedAt}
			switch event.EventType {
			case "billing_status_changed":
				v.Axis = "billing"
				v.EventType = event.EventType
			case "delivery_status_changed":
				v.Axis = "delivery"
				v.EventType = event.EventType
			case "cancel_requested":
				v.EventType = "cancel_requested"
				v.FromStatus = nil
				v.ToStatus = nil
			case "video_reconciliation_review_required":
				v.EventType = "reconciliation_required"
				v.FromStatus = nil
				v.ToStatus = nil
			}
			result.Items = append(result.Items, v)
		}
		return s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC())
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return result, err
}
