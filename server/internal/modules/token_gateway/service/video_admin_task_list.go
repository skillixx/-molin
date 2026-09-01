package service

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sort"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
)

var ErrVideoAdminQuery = errors.New("视频管理查询参数无效")
var errVideoAdminPageChanged = errors.New("视频管理列表成员状态已变化")
var videoAdminModelCode = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9/._:-]{0,190}$`)

type VideoAdminTaskFilter struct {
	Page, PageSize           int
	UserID, ProjectID        uint64
	Status, Model, Operation string
}
type VideoAdminTaskPage struct {
	Items    []VideoAdminTaskDetails `json:"items"`
	Page     int                     `json:"page"`
	PageSize int                     `json:"page_size"`
	Total    int64                   `json:"total"`
}

func validVideoAdminTaskFilter(f VideoAdminTaskFilter) bool {
	if f.Page < 1 || f.Page > 10000 || f.PageSize < 1 || f.PageSize > 100 {
		return false
	}
	if f.Model != "" && !videoAdminModelCode.MatchString(f.Model) {
		return false
	}
	if f.Operation != "" && f.Operation != "text_to_video" && f.Operation != "image_to_video" {
		return false
	}
	switch f.Status {
	case "", "created", "reserved", "queued", "submitting", "submitted", "processing", "fetching", "storing", "moderating", "labeling", "succeeded", "failed", "cancelled", "expired", "pending_reconcile":
		return true
	}
	return false
}

// 管理列表保留删除/隔离/停用主体的管理事实；过滤原执行轴，不复用v1的公开状态映射。
func (s *VideoAdminService) ListTasks(ctx context.Context, caller VideoCaller, f VideoAdminTaskFilter) (*VideoAdminTaskPage, error) {
	if s == nil || s.app == nil || s.app.db == nil {
		return nil, ErrVideoAccessUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 选页快照与后续当前读之间可能发生合法取消；整页重算保持筛选、条目和total一致。
	// 三次仍不能获得稳定页时失败关闭，不无限重试，也不返回删减条目后的旧total。
	for attempt := 0; attempt < 3; attempt++ {
		result := &VideoAdminTaskPage{Items: []VideoAdminTaskDetails{}, Page: f.Page, PageSize: f.PageSize}
		err := s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := s.authorizeTx(ctx, tx, caller, "ai_gateway:view"); err != nil {
				return err
			}
			if !validVideoAdminTaskFilter(f) {
				return ErrVideoAdminQuery
			}
			base := func() *gorm.DB {
				q := tx.Table("ai_gateway_tasks t").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id AND r.logical_model_code=t.logical_model_code AND r.operation=t.operation").Where("t.capability='video.generate' AND t.operation IN ('text_to_video','image_to_video') AND r.modality='video' AND r.capability='video.generate'")
				if f.UserID != 0 {
					q = q.Where("t.user_id=?", f.UserID)
				}
				if f.ProjectID != 0 {
					q = q.Where("t.project_id=?", f.ProjectID)
				}
				if f.Status != "" {
					q = q.Where("t.status=?", f.Status)
				}
				if f.Model != "" {
					q = q.Where("t.logical_model_code=?", f.Model)
				}
				if f.Operation != "" {
					q = q.Where("t.operation=?", f.Operation)
				}
				return q
			}
			countQuery := base().Select("COUNT(*) AS total")
			pageQuery := base().Select("t.public_id,t.created_at,t.user_id,t.project_id,t.api_key_id").Order("t.created_at DESC,t.public_id DESC").Limit(f.PageSize).Offset((f.Page - 1) * f.PageSize)
			var rows []struct {
				PublicID          *string
				UserID, ProjectID uint64
				APIKeyID          *uint64
				Total             int64
			}
			// 同一语句取得total和页内ID，空页仍有total，避免RC下分别COUNT/SELECT看到不同成员集合。
			if err := tx.Table("(?) AS totals", countQuery).Joins("LEFT JOIN (?) AS selected_tasks ON 1=1", pageQuery).Select("selected_tasks.public_id,selected_tasks.user_id,selected_tasks.project_id,selected_tasks.api_key_id,totals.total").Order("selected_tasks.created_at DESC,selected_tasks.public_id DESC").Scan(&rows).Error; err != nil {
				return ErrVideoAccessUnavailable
			}
			owners := map[string]repository.VideoOwner{}
			var ids []string
			for _, row := range rows {
				result.Total = row.Total
				if row.PublicID != nil {
					ids = append(ids, *row.PublicID)
					owners[*row.PublicID] = repository.VideoOwner{UserID: row.UserID, ProjectID: row.ProjectID, APIKeyID: row.APIKeyID}
				}
			}
			lockIDs := append([]string(nil), ids...)
			sort.Strings(lockIDs)
			records := map[string]*repository.VideoTaskRecord{}
			// 与v1/用户平台列表使用同一公开ID任务锁序，先固定整页任务，再读取原财务事实。
			for _, id := range lockIDs {
				record, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, id, owners[id])
				if err != nil {
					return err
				}
				if f.Status != "" && record.Status != f.Status {
					return errVideoAdminPageChanged
				}
				records[id] = record
			}
			// 两个无共同任务的分页也可能包含同样的钱包；按用户统一读取顺序，避免A→B与B→A死锁。
			// 一个用户仅有一个原钱包，用户内继续按公开任务ID排序；不改变原Quote/Link/Hold/钱包检查。
			financialIDs := append([]string(nil), lockIDs...)
			sort.Slice(financialIDs, func(i, j int) bool {
				left, right := owners[financialIDs[i]].UserID, owners[financialIDs[j]].UserID
				if left == right {
					return financialIDs[i] < financialIDs[j]
				}
				return left < right
			})
			details := make(map[string]VideoAdminTaskDetails, len(ids))
			for _, id := range financialIDs {
				detail, err := s.app.taskDetailsTx(ctx, tx, records[id], owners[id])
				if err != nil {
					return err
				}
				owner := owners[id]
				details[id] = VideoAdminTaskDetails{VideoTaskDetails: detail, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID}
			}
			// 仅恢复API约定的展示顺序，不能让客户端排序决定财务锁顺序。
			for _, id := range ids {
				result.Items = append(result.Items, details[id])
			}
			return s.authorizeTx(ctx, tx, caller, "ai_gateway:view")
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if errors.Is(err, errVideoAdminPageChanged) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	return nil, ErrVideoAccessUnavailable
}
