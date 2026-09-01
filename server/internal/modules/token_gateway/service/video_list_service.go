package service

import (
	"context"
	"errors"
	"sort"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var ErrVideoListParameters = errors.New("视频列表参数无效")

// VideoListQuery只接受冻结游标合同；游标为原公开ID，不编码Provider或数据库内部ID。
type VideoListQuery struct {
	After string
	Limit int
	Order string
}

// ListVideos在一个一致快照内查询共享任务账本，权限使用当前读并保持到响应材料生成结束。
// 仅返回当前调用人、Project和Key可见模型；不查询Provider，也不产生报价或资金写入。
func (s *VideoHTTPService) ListVideos(ctx context.Context, caller VideoCaller, query VideoListQuery) (*dto.VideoList, error) {
	if s == nil || s.db == nil || caller.UserID == 0 {
		return nil, ErrVideoBillingAccess
	}
	if query.Limit == 0 {
		query.Limit = 20
	}
	if query.Order == "" {
		query.Order = "desc"
	}
	if query.Limit < 1 || query.Limit > 100 || (query.Order != "asc" && query.Order != "desc") || (query.After != "" && !videoHTTPPublicID.MatchString(query.After)) {
		return nil, ErrVideoListParameters
	}
	result := &dto.VideoList{Object: "list", Data: []dto.VideoJob{}}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 复用应用查询与实时权限边界并绑定同一事务，避免每条任务借用额外连接。
		local := *s
		local.db, local.access = tx, NewVideoAccessService(tx)
		owner, codes, err := local.videoListScope(ctx, caller)
		if err != nil {
			return err
		}
		caller.ProjectID = owner.ProjectID
		baseQuery := func() *gorm.DB {
			q := tx.Model(&model.AIImageTask{}).Where("user_id=? AND project_id=? AND capability=? AND logical_model_code IN ?", owner.UserID, owner.ProjectID, model.AIVideoCapability, codes)
			if owner.APIKeyID == nil {
				return q.Where("api_key_id IS NULL")
			}
			return q.Where("api_key_id=?", *owner.APIKeyID)
		}
		pageQuery := baseQuery()
		if query.After != "" {
			var cursor model.AIImageTask
			// 被删除媒体的任务事实仍保留，原拥有者可继续分页；跨主体cursor统一404。
			if err := baseQuery().Select("public_id,created_at").Where("public_id=?", query.After).Take(&cursor).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
			}
			comparison := "<"
			if query.Order == "asc" {
				comparison = ">"
			}
			pageQuery = pageQuery.Where("(created_at "+comparison+" ? OR (created_at=? AND public_id "+comparison+" ?))", cursor.CreatedAt, cursor.CreatedAt, cursor.PublicID)
		}
		// 公开列表隐藏已删除媒体，但保留共享Task/Request和财务事实作为游标及审计依据。
		removed := tx.Table("ai_gateway_assets").Select("request_id").Where("user_id=? AND project_id=? AND modality='video' AND asset_role='content' AND (media_deleted_at IS NOT NULL OR deleted_at IS NOT NULL OR lifecycle_state='deleted')", owner.UserID, owner.ProjectID)
		pageQuery = pageQuery.Where("request_id NOT IN (?)", removed)
		pageQuery = pageQuery.Where("id NOT IN (?)", tx.Table("ai_video_media_deletions").Select("task_id").Where("user_id=? AND project_id=?", owner.UserID, owner.ProjectID))
		pageQuery = pageQuery.Where("id NOT IN (?)", tx.Table("ai_video_asset_deletions").Select("task_id").Where("user_id=? AND project_id=?", owner.UserID, owner.ProjectID))
		var tasks []model.AIImageTask
		if err := pageQuery.Select("public_id,created_at").Order("created_at " + query.Order).Order("public_id " + query.Order).Limit(query.Limit + 1).Find(&tasks).Error; err != nil {
			return ErrVideoAccessUnavailable
		}
		result.HasMore = len(tasks) > query.Limit
		if result.HasMore {
			tasks = tasks[:query.Limit]
		}
		// 对账会继续锁定共同钱包；不能按展示顺序逐个Task→Wallet→下一Task，否则
		// asc/desc两页会分别持有相反首Task并在共同Wallet处成环。先统一锁定全部Task。
		lockIDs := make([]string, len(tasks))
		for index := range tasks {
			lockIDs[index] = tasks[index].PublicID
		}
		sort.Strings(lockIDs)
		for _, id := range lockIDs {
			if _, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, id, owner); err != nil {
				return err
			}
		}
		for _, task := range tasks {
			job, err := local.GetVideo(ctx, caller, task.PublicID)
			if err != nil {
				return err
			}
			result.Data = append(result.Data, *job)
		}
		// 长查询不得冻结授权有效期；共享锁已阻止并行撤权，这里补查时间自然过期。
		for _, code := range codes {
			if err := local.access.AuthorizeTx(ctx, tx, owner, code, time.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(result.Data) > 0 {
		first, last := result.Data[0].ID, result.Data[len(result.Data)-1].ID
		result.FirstID, result.LastID = &first, &last
	}
	return result, nil
}

// 空页也必须完成实时业务准入，不能以无任务为由跳过实名或已吊销视频能力。
func (s *VideoHTTPService) videoListScope(ctx context.Context, caller VideoCaller) (repository.VideoOwner, []string, error) {
	owner := repository.VideoOwner{UserID: caller.UserID, ProjectID: caller.ProjectID}
	if caller.APIKeyID != 0 {
		var key struct{ ProjectID *uint64 }
		if err := s.db.Table("api_keys").Select("project_id").Where("id=? AND user_id=?", caller.APIKeyID, caller.UserID).Take(&key).Error; err != nil {
			return owner, nil, videoAccessReadError(err, ErrVideoBillingAccess)
		}
		if key.ProjectID == nil || (caller.ProjectID != 0 && caller.ProjectID != *key.ProjectID) {
			return owner, nil, ErrVideoBillingAccess
		}
		owner.ProjectID, owner.APIKeyID = *key.ProjectID, optionalUint64(caller.APIKeyID)
	}
	if owner.ProjectID == 0 {
		return owner, nil, ErrVideoBillingAccess
	}
	if err := s.access.AuthorizeSubjectTx(ctx, s.db, owner, time.Now().UTC()); err != nil {
		return owner, nil, err
	}
	var candidates []string
	if err := s.db.Table("ai_project_model_capability_grants").Where("user_id=? AND project_id=? AND capability=? AND status='active'", owner.UserID, owner.ProjectID, model.AIVideoCapability).Order("logical_model_code ASC").Pluck("logical_model_code", &candidates).Error; err != nil {
		return owner, nil, ErrVideoAccessUnavailable
	}
	allowed := make([]string, 0, len(candidates))
	for _, code := range candidates {
		if err := s.access.AuthorizeTx(ctx, s.db, owner, code, time.Now().UTC()); err != nil {
			if errors.Is(err, ErrVideoCapabilityDenied) || errors.Is(err, ErrVideoEntitlementDenied) {
				continue
			}
			return owner, nil, err
		}
		allowed = append(allowed, code)
	}
	if len(allowed) == 0 {
		return owner, nil, ErrVideoCapabilityDenied
	}
	return owner, allowed, nil
}
