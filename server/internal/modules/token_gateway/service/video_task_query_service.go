package service

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 平台详情保留原三轴及客户金额事实；can_deliver才代表通过交付门禁，执行完成时间不等于交付完成。
type VideoTaskDetails struct {
	TaskID                string     `json:"task_id"`
	VideoID               string     `json:"video_id"`
	RequestID             string     `json:"request_id"`
	QuoteID               string     `json:"quote_id"`
	Model                 string     `json:"model"`
	Operation             string     `json:"operation"`
	ExecutionStatus       string     `json:"execution_status"`
	BillingStatus         string     `json:"billing_status"`
	DeliveryStatus        string     `json:"delivery_status"`
	Progress              uint8      `json:"progress"`
	VersionNo             uint64     `json:"version_no"`
	RequestVersionNo      uint64     `json:"request_version_no"`
	QuotedAmount          string     `json:"quoted_amount"`
	HeldAmount            *string    `json:"held_amount"`
	CurrentFrozenAmount   *string    `json:"current_frozen_amount"`
	SettledAmount         *string    `json:"settled_amount"`
	NetReleasedAmount     *string    `json:"net_released_amount"`
	HoldStatus            *string    `json:"hold_status"`
	Currency              string     `json:"currency"`
	CreatedAt             time.Time  `json:"created_at"`
	CompletedAt           *time.Time `json:"completed_at"`
	MediaDeleted          bool       `json:"media_deleted"`
	MediaPartiallyDeleted bool       `json:"media_partially_deleted"`
	MediaDeletionPending  bool       `json:"media_deletion_pending"`
	CanDeliver            bool       `json:"can_deliver"`
}
type VideoTaskPage struct {
	Items    []VideoTaskDetails `json:"items"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
	Total    int64              `json:"total"`
}

func videoTaskOwnerQuery(tx *gorm.DB, caller VideoCaller) *gorm.DB {
	q := tx.Table("ai_gateway_tasks t").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id AND r.logical_model_code=t.logical_model_code AND r.operation=t.operation").
		Where("t.user_id=? AND t.capability='video.generate' AND t.operation IN ('text_to_video','image_to_video') AND r.modality='video' AND r.capability='video.generate'", caller.UserID)
	if caller.ProjectID != 0 {
		q = q.Where("t.project_id=?", caller.ProjectID)
	}
	if caller.APIKeyID == 0 {
		return q.Where("t.api_key_id IS NULL")
	}
	return q.Where("t.api_key_id=?", caller.APIKeyID)
}

// 只在认证用户/Key范围内解析公开ID，再按G5既有Task→Request锁序读取三轴，避免旧RR混合新状态。
func (s *VideoHTTPService) taskForPlatformTx(ctx context.Context, tx *gorm.DB, caller VideoCaller, id string, byRequest bool) (*repository.VideoTaskRecord, repository.VideoOwner, error) {
	var empty repository.VideoOwner
	if !videoBillingPublicID.MatchString(id) {
		return nil, empty, repository.ErrVideoTaskNotFound
	}
	q := videoTaskOwnerQuery(tx, caller).Select("t.public_id,t.project_id")
	if byRequest {
		q = q.Where("t.request_id=?", id)
	} else {
		q = q.Where("t.public_id=?", id)
	}
	var identity struct {
		PublicID  string
		ProjectID uint64
	}
	if err := q.Take(&identity).Error; err != nil {
		return nil, empty, videoAccessReadError(err, repository.ErrVideoTaskNotFound)
	}
	caller.ProjectID = identity.ProjectID
	owner, err := s.access.ResolveSubjectTx(ctx, tx, caller, time.Now().UTC())
	if err != nil {
		return nil, owner, err
	}
	task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, identity.PublicID, owner)
	if err != nil {
		return nil, owner, err
	}
	if err := s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC()); err != nil {
		return nil, owner, err
	}
	return task, owner, nil
}

func videoAmount(value decimal.Decimal) *string { s := value.StringFixed(8); return &s }
func (s *VideoHTTPService) taskDetailsTx(ctx context.Context, tx *gorm.DB, task *repository.VideoTaskRecord, owner repository.VideoOwner) (*VideoTaskDetails, error) {
	var quote struct {
		PublicID, Currency, LogicalModelCode string
		Operation, ConsumedRequestID         *string
		QuotedAmount                         decimal.Decimal
	}
	q := tx.Table("ai_gateway_quotes").Select("public_id,currency,quoted_amount,logical_model_code,operation,consumed_request_id").Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND project_id=? AND capability='video.generate'", task.QuoteID, owner.UserID, owner.ProjectID)
	if owner.APIKeyID == nil {
		q = q.Where("api_key_id IS NULL")
	} else {
		q = q.Where("api_key_id=?", *owner.APIKeyID)
	}
	if err := q.Take(&quote).Error; err != nil {
		return nil, ErrVideoAccessUnavailable
	}
	if quote.Currency != "CNY" || quote.QuotedAmount.IsNegative() || task.Operation == nil || quote.Operation == nil || *quote.Operation != *task.Operation || quote.LogicalModelCode != task.LogicalModelCode {
		return nil, ErrVideoAccessUnavailable
	}
	d := &VideoTaskDetails{TaskID: task.PublicID, VideoID: task.PublicID, RequestID: task.RequestID, QuoteID: quote.PublicID, Model: task.LogicalModelCode, Operation: *task.Operation, ExecutionStatus: task.Status, BillingStatus: task.BillingStatus, DeliveryStatus: task.DeliveryStatus, Progress: task.Progress, VersionNo: task.VersionNo, RequestVersionNo: task.RequestVersionNo, QuotedAmount: quote.QuotedAmount.StringFixed(8), Currency: quote.Currency, CreatedAt: task.CreatedAt, CompletedAt: task.CompletedAt}
	var link model.AIRequestWalletLink
	err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("request_id=?", task.RequestID).Take(&link).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrVideoAccessUnavailable
	}
	if errors.Is(err, gorm.ErrRecordNotFound) && task.BillingStatus != model.AIBillingUnquoted && task.BillingStatus != model.AIBillingQuoted {
		return nil, ErrVideoAccessUnavailable
	}
	if err == nil {
		if quote.ConsumedRequestID == nil || *quote.ConsumedRequestID != task.RequestID || !link.QuotedAmount.Equal(quote.QuotedAmount) || !link.HeldAmount.Equal(quote.QuotedAmount) {
			return nil, ErrVideoAccessUnavailable
		}
		d.HeldAmount = videoAmount(link.HeldAmount)
		if link.SettledAmount != nil {
			d.SettledAmount = videoAmount(*link.SettledAmount)
		}
		var hold billingmodel.WalletHold
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND wallet_id=?", link.WalletHoldID, owner.UserID, link.WalletID).Take(&hold).Error; err != nil {
			return nil, ErrVideoAccessUnavailable
		}
		if !hold.HoldAmount.Equal(link.HeldAmount) || hold.IdempotencyKey != task.RequestID+":video-hold" || hold.FreezeTxnID == nil || *hold.FreezeTxnID != link.HoldTransactionID {
			return nil, ErrVideoAccessUnavailable
		}
		// 两份原结算事实必须同时为空或金额完全一致，不能展示Link金额却用另一Hold金额计算净释放。
		if (hold.SettledAmount == nil) != (link.SettledAmount == nil) ||
			(hold.SettledAmount != nil && !hold.SettledAmount.Equal(*link.SettledAmount)) {
			return nil, ErrVideoAccessUnavailable
		}
		d.HoldStatus = &hold.Status
		// 原始预占、当前冻结与原Hold净释放分别展示，不把解冻流水总额误当退款。
		switch hold.Status {
		case billingmodel.HoldStatusHolding:
			if hold.SettledAmount != nil {
				return nil, ErrVideoAccessUnavailable
			}
			d.CurrentFrozenAmount = videoAmount(hold.HoldAmount)
			d.NetReleasedAmount = videoAmount(decimal.Zero)
		case billingmodel.HoldStatusReleased:
			if hold.SettledAmount == nil || !hold.SettledAmount.IsZero() {
				return nil, ErrVideoAccessUnavailable
			}
			d.CurrentFrozenAmount = videoAmount(decimal.Zero)
			d.NetReleasedAmount = videoAmount(hold.HoldAmount)
		case billingmodel.HoldStatusSettled:
			if hold.SettledAmount == nil || hold.SettledAmount.IsNegative() || hold.SettledAmount.GreaterThan(hold.HoldAmount) {
				return nil, ErrVideoAccessUnavailable
			}
			d.CurrentFrozenAmount = videoAmount(decimal.Zero)
			d.NetReleasedAmount = videoAmount(hold.HoldAmount.Sub(*hold.SettledAmount))
		default:
			return nil, ErrVideoAccessUnavailable
		}
	}
	var assets []struct {
		LifecycleState            string
		DeletedAt, MediaDeletedAt *time.Time
	}
	if err := tx.Table("ai_gateway_assets").Select("lifecycle_state,deleted_at,media_deleted_at").Clauses(clause.Locking{Strength: "SHARE"}).Where("request_id=? AND user_id=? AND project_id=? AND modality='video' AND asset_role='content'", task.RequestID, owner.UserID, owner.ProjectID).Find(&assets).Error; err != nil {
		return nil, ErrVideoAccessUnavailable
	}
	for _, a := range assets {
		d.MediaDeleted = d.MediaDeleted || a.LifecycleState == "deleted" || a.DeletedAt != nil || a.MediaDeletedAt != nil
	}
	var completedDeletes int64
	if err := tx.Table("ai_video_media_deletions").Where("task_id=? AND user_id=? AND project_id=? AND status='completed'", task.ID, owner.UserID, owner.ProjectID).Count(&completedDeletes).Error; err != nil {
		return nil, ErrVideoAccessUnavailable
	}
	d.MediaDeleted = d.MediaDeleted || completedDeletes != 0
	var partial, pending, wholePending int64
	if err := tx.Table("ai_video_asset_deletions").Where("task_id=? AND status='completed'", task.ID).Count(&partial).Error; err != nil {
		return nil, ErrVideoAccessUnavailable
	}
	if err := tx.Table("ai_video_asset_deletions").Where("task_id=? AND status<>'completed'", task.ID).Count(&pending).Error; err != nil {
		return nil, ErrVideoAccessUnavailable
	}
	if err := tx.Table("ai_video_media_deletions").Where("task_id=? AND status<>'completed'", task.ID).Count(&wholePending).Error; err != nil {
		return nil, ErrVideoAccessUnavailable
	}
	d.MediaPartiallyDeleted = !d.MediaDeleted && partial > 0
	d.MediaDeletionPending = !d.MediaDeleted && (pending > 0 || wholePending > 0)
	if !d.MediaDeleted && partial == 0 && pending == 0 && wholePending == 0 && task.Status == model.AIImageTaskSucceeded && task.BillingStatus == model.AIBillingSettled && task.DeliveryStatus == model.AIDeliveryAvailable {
		report, err := NewVideoReconciliationService(tx).Reconcile(ctx, task.PublicID, owner)
		if err != nil {
			return nil, err
		}
		d.CanDeliver = report.Passed
	}
	return d, nil
}

func (s *VideoHTTPService) GetPlatformTask(ctx context.Context, caller VideoCaller, id string, byRequest bool) (*VideoTaskDetails, error) {
	if s == nil || s.db == nil || s.access == nil {
		return nil, ErrVideoAccessUnavailable
	}
	var result *VideoTaskDetails
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		task, owner, err := s.taskForPlatformTx(ctx, tx, caller, id, byRequest)
		if err != nil {
			return err
		}
		result, err = s.taskDetailsTx(ctx, tx, task, owner)
		if err != nil {
			return err
		}
		return s.access.AuthorizeTx(ctx, tx, owner, task.LogicalModelCode, time.Now().UTC())
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return result, err
}

func (s *VideoHTTPService) ListPlatformTasks(ctx context.Context, caller VideoCaller, page, size int) (*VideoTaskPage, error) {
	if s == nil || s.db == nil || s.access == nil {
		return nil, ErrVideoAccessUnavailable
	}
	if page < 1 || page > 10000 || size < 1 || size > 100 {
		return nil, ErrVideoListParameters
	}
	result := &VideoTaskPage{Items: []VideoTaskDetails{}, Page: page, PageSize: size}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		local := *s
		local.db, local.access = tx, NewVideoAccessService(tx)
		owner, codes, err := local.videoListScope(ctx, caller)
		if err != nil {
			return err
		}
		caller.ProjectID = owner.ProjectID
		base := func() *gorm.DB { return videoTaskOwnerQuery(tx, caller).Where("t.logical_model_code IN ?", codes) }
		// 与G5对账一致使用RC；总数和页内ID合并为一条一致性读取，避免两条RC查询看到不同新增任务。
		countQuery := base().Select("count(*) AS total")
		pageQuery := base().Select("t.public_id,t.created_at").Order("t.created_at DESC").Order("t.public_id DESC").Limit(size).Offset((page - 1) * size)
		var rows []struct {
			PublicID *string
			Total    int64
		}
		if err := tx.Table("(?) AS totals", countQuery).Joins("LEFT JOIN (?) AS selected_tasks ON 1=1", pageQuery).Select("selected_tasks.public_id,totals.total").Order("selected_tasks.created_at DESC,selected_tasks.public_id DESC").Scan(&rows).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		var ids []string
		for _, row := range rows {
			result.Total = row.Total
			if row.PublicID != nil {
				ids = append(ids, *row.PublicID)
			}
		}
		// 多任务详情可能继续对账并锁同钱包，先按稳定ID次序锁全部Task，不能依展示顺序交错锁钱包。
		locked := append([]string(nil), ids...)
		sort.Strings(locked)
		records := map[string]*repository.VideoTaskRecord{}
		for _, id := range locked {
			record, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, id, owner)
			if err != nil {
				return err
			}
			records[id] = record
		}
		for _, id := range ids {
			task := records[id]
			d, err := local.taskDetailsTx(ctx, tx, task, owner)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, *d)
		}
		for _, code := range codes {
			if err := local.access.AuthorizeTx(ctx, tx, owner, code, time.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	return result, err
}
