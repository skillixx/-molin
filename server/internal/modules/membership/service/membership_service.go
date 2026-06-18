package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/membership/dto"
	"molin/server/internal/modules/membership/model"
	"molin/server/internal/modules/membership/repository"
)

// ErrLevelNotFound 表示会员等级不存在或未上架（status != active）。
// 公开权益端点据此返回 404，避免泄露未上架等级的存在。
var ErrLevelNotFound = errors.New("会员等级不存在")

// MembershipService 会员服务，处理会员等级管理和用户会员查询。
// 注意：不导入 product/service 包，避免循环导入。
// bootstrap/app.go 中使用 adapter 将本服务适配为 productservice.MembershipService 接口。
type MembershipService struct {
	db          *gorm.DB
	levelRepo   *repository.LevelRepository
	benefitRepo *repository.BenefitRepository
	memberRepo  *repository.UserMembershipRepository
}

// NewMembershipService 创建会员服务实例。
func NewMembershipService(db *gorm.DB) *MembershipService {
	return &MembershipService{
		db:          db,
		levelRepo:   repository.NewLevelRepository(db),
		benefitRepo: repository.NewBenefitRepository(db),
		memberRepo:  repository.NewUserMembershipRepository(db),
	}
}

// GetActiveMembership 查询用户当前有效会员。
// 查询条件：status = active AND (expires_at IS NULL OR expires_at > NOW())
// 返回 nil 表示用户无有效会员。
func (s *MembershipService) GetActiveMembership(ctx context.Context, userID uint64) (*model.UserMembership, error) {
	return s.memberRepo.FindActive(ctx, userID)
}

// HasActiveLevelIn 校验用户是否拥有满足条件的有效会员资格（用于"会员专属商品"购买门槛校验）。
// levelIDs 为商品/套餐配置的会员专属价所对应的会员等级 ID 集合：
// 只要用户的当前有效会员等级命中其中任意一个，即视为满足购买门槛。
// 由 product 模块通过 MembershipService 接口适配调用，不直接访问 membership 模块的 repository。
func (s *MembershipService) HasActiveLevelIn(ctx context.Context, userID uint64, levelIDs []uint64) (bool, error) {
	return s.memberRepo.HasActiveLevelIn(ctx, userID, levelIDs)
}

// CreateOrRenewMembership 开通或续期用户会员（由 provision 模块在会员商品开通成功后调用）。
// C-FIX-1：同一用户在同一等级下已有有效会员时执行「续期」——在原有效期基础上叠加 durationDays，
// 而非新增一条 active 记录（避免重复 active 导致定价/权益判定不确定）。
// durationDays 为 nil 表示永久会员（expires_at 置 NULL）。
// 事务内对同等级有效记录加行锁（FOR UPDATE），防止并发重复开通。
func (s *MembershipService) CreateOrRenewMembership(ctx context.Context, userID, levelID, assetID uint64, durationDays *int) error {
	// 校验等级是否存在且激活
	level, err := s.levelRepo.FindByID(ctx, levelID)
	if err != nil {
		return fmt.Errorf("会员等级不存在: %w", err)
	}
	if level.Status != "active" {
		return fmt.Errorf("会员等级 %s 已停用", level.LevelCode)
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		existing, err := s.memberRepo.FindActiveByLevelForUpdate(ctx, tx, userID, levelID)
		if err != nil {
			return err
		}

		if existing != nil {
			// 续期：在原有效期（若尚未到期）或当前时间基础上叠加 durationDays。
			var newExpiry *time.Time
			if durationDays != nil {
				base := now
				if existing.ExpiresAt != nil && existing.ExpiresAt.After(now) {
					base = *existing.ExpiresAt
				}
				t := base.AddDate(0, 0, *durationDays)
				newExpiry = &t
			}
			// durationDays 为 nil → 升级为永久会员（expires_at 置 NULL）。
			return tx.Model(&model.UserMembership{}).Where("id = ?", existing.ID).
				Updates(map[string]interface{}{"expires_at": newExpiry, "status": "active"}).Error
		}

		// 新建有效会员记录。
		var expiresAt *time.Time
		if durationDays != nil {
			t := now.AddDate(0, 0, *durationDays)
			expiresAt = &t
		}
		m := &model.UserMembership{
			UserID:    userID,
			LevelID:   levelID,
			Status:    "active",
			StartedAt: now,
			ExpiresAt: expiresAt,
		}
		if assetID > 0 {
			m.AssetID = &assetID
		}
		return tx.Create(m).Error
	})
}

// GetActiveMembershipResponse 查询用户当前有效会员并内联等级名（M2 用）。
// 返回 nil 表示用户无有效会员。内联 level_code / level_name，前端无需再映射等级列表。
func (s *MembershipService) GetActiveMembershipResponse(ctx context.Context, userID uint64) (*dto.MembershipResponse, error) {
	m, err := s.memberRepo.FindActive(ctx, userID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, nil
	}
	// 加载对应等级填充等级名；等级查询失败不阻断会员信息返回（等级名留空）。
	var level *model.MembershipLevel
	if lv, err := s.levelRepo.FindByID(ctx, m.LevelID); err == nil {
		level = lv
	}
	return mapMembershipResponse(m, level), nil
}

// mapMembershipResponse 将单条会员记录映射为 M2 响应并内联等级名（level 为 nil 时等级名留空）。
// 抽为纯函数便于单元测试。
func mapMembershipResponse(m *model.UserMembership, level *model.MembershipLevel) *dto.MembershipResponse {
	resp := &dto.MembershipResponse{
		ID:        m.ID,
		UserID:    m.UserID,
		LevelID:   m.LevelID,
		AssetID:   m.AssetID,
		Status:    m.Status,
		StartedAt: m.StartedAt,
		ExpiresAt: m.ExpiresAt,
	}
	if level != nil {
		resp.LevelCode = level.LevelCode
		resp.LevelName = level.Name
	}
	return resp
}

// ListPublicLevelBenefits 查询某会员等级的公开权益（仅 active 权益）。
// 等级不存在或未上架（status != active）时返回 ErrLevelNotFound，避免泄露未上架等级。
func (s *MembershipService) ListPublicLevelBenefits(ctx context.Context, levelID uint64) ([]*model.MembershipBenefit, error) {
	level, err := s.levelRepo.FindByID(ctx, levelID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrLevelNotFound
		}
		return nil, err
	}
	if level.Status != "active" {
		return nil, ErrLevelNotFound
	}
	return s.benefitRepo.FindActiveByLevelID(ctx, levelID)
}

// ListLevels 查询所有 active 会员等级（用户端）。
func (s *MembershipService) ListLevels(ctx context.Context) ([]*model.MembershipLevel, error) {
	return s.levelRepo.ListActive(ctx)
}

// AdminListLevels 查询所有会员等级（管理端，不过滤状态）。
func (s *MembershipService) AdminListLevels(ctx context.Context) ([]*model.MembershipLevel, error) {
	return s.levelRepo.ListAll(ctx)
}

// CreateLevel 创建会员等级（管理端）。
func (s *MembershipService) CreateLevel(ctx context.Context, levelCode, name string, description *string, sortOrder int) (*model.MembershipLevel, error) {
	level := &model.MembershipLevel{
		LevelCode:   levelCode,
		Name:        name,
		Description: description,
		SortOrder:   sortOrder,
		Status:      "active",
	}
	if err := s.levelRepo.Create(ctx, level); err != nil {
		return nil, fmt.Errorf("创建会员等级失败: %w", err)
	}
	return level, nil
}

// UpdateLevel 修改会员等级（管理端）。
func (s *MembershipService) UpdateLevel(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return s.levelRepo.Update(ctx, id, updates)
}

// ListBenefits 查询权益列表（支持按 levelID 过滤，管理端）。
func (s *MembershipService) ListBenefits(ctx context.Context, levelID uint64) ([]*model.MembershipBenefit, error) {
	return s.benefitRepo.FindByLevelID(ctx, levelID)
}

// CreateBenefit 创建权益（管理端）。
func (s *MembershipService) CreateBenefit(ctx context.Context, levelID uint64, benefitType, benefitValue string) (*model.MembershipBenefit, error) {
	// 校验等级存在
	if _, err := s.levelRepo.FindByID(ctx, levelID); err != nil {
		return nil, fmt.Errorf("会员等级不存在: %w", err)
	}

	benefit := &model.MembershipBenefit{
		LevelID:      levelID,
		BenefitType:  benefitType,
		BenefitValue: benefitValue,
		Status:       "active",
	}
	if err := s.benefitRepo.Create(ctx, benefit); err != nil {
		return nil, fmt.Errorf("创建权益失败: %w", err)
	}
	return benefit, nil
}

// UpdateBenefit 修改权益（管理端）。
func (s *MembershipService) UpdateBenefit(ctx context.Context, id uint64, updates map[string]interface{}) error {
	return s.benefitRepo.Update(ctx, id, updates)
}

// AdminListUserMemberships 查询用户会员列表（管理端，支持 user_id 过滤）。
// 内联 level_code / level_name：收集本页所有 level_id 一次性批量查 levels 建 map 后填充，避免 N+1。
func (s *MembershipService) AdminListUserMemberships(ctx context.Context, userID uint64, page, pageSize int) ([]*dto.AdminUserMembershipResponse, int64, error) {
	offset := (page - 1) * pageSize
	list, total, err := s.memberRepo.ListByUserID(ctx, userID, offset, pageSize)
	if err != nil {
		return nil, 0, err
	}

	// 收集本页去重后的 level_id，一次性批量查等级（避免 N+1）。
	levels, err := s.levelRepo.FindByIDs(ctx, collectLevelIDs(list))
	if err != nil {
		return nil, 0, err
	}
	return mapAdminUserMembershipItems(list, buildLevelMap(levels)), total, nil
}

// collectLevelIDs 收集会员记录中去重后的 level_id 集合（供批量查等级用）。
func collectLevelIDs(list []*model.UserMembership) []uint64 {
	idSet := make(map[uint64]struct{}, len(list))
	for _, m := range list {
		idSet[m.LevelID] = struct{}{}
	}
	ids := make([]uint64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	return ids
}

// buildLevelMap 将等级切片构建为 id→level 映射。
func buildLevelMap(levels []*model.MembershipLevel) map[uint64]*model.MembershipLevel {
	m := make(map[uint64]*model.MembershipLevel, len(levels))
	for _, lv := range levels {
		m[lv.ID] = lv
	}
	return m
}

// mapAdminUserMembershipItems 将会员记录列表映射为 M9 响应并按 levelMap 内联等级名。
// 抽为纯函数便于单元测试（验证无 N+1、等级名填充、缺失等级兜底）。
func mapAdminUserMembershipItems(list []*model.UserMembership, levelMap map[uint64]*model.MembershipLevel) []*dto.AdminUserMembershipResponse {
	items := make([]*dto.AdminUserMembershipResponse, 0, len(list))
	for _, m := range list {
		item := &dto.AdminUserMembershipResponse{
			ID:        m.ID,
			UserID:    m.UserID,
			LevelID:   m.LevelID,
			AssetID:   m.AssetID,
			Status:    m.Status,
			StartedAt: m.StartedAt,
			ExpiresAt: m.ExpiresAt,
			CreatedAt: m.CreatedAt,
			UpdatedAt: m.UpdatedAt,
		}
		if lv, ok := levelMap[m.LevelID]; ok {
			item.LevelCode = lv.LevelCode
			item.LevelName = lv.Name
		}
		items = append(items, item)
	}
	return items
}

// AdminGrantMembership 管理端手动开通/续期用户会员（复用 CreateOrRenewMembership 的续期叠加语义）。
// 不关联资产（assetID=0），适用于客服补偿、人工纠错等场景。
func (s *MembershipService) AdminGrantMembership(ctx context.Context, userID, levelID uint64, durationDays *int) error {
	if userID == 0 || levelID == 0 {
		return fmt.Errorf("user_id 和 level_id 为必填项")
	}
	// BUG-M10-01：补 user_id 存在性校验，与 level_id 存在性校验对称，
	// 避免传入不存在的 user_id 时写入孤儿会员记录（脏数据并污染 ExpireMembershipsJob 等后续处理）。
	exists, err := s.memberRepo.UserExists(ctx, userID)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("用户不存在")
	}
	return s.CreateOrRenewMembership(ctx, userID, levelID, 0, durationDays)
}

// AdminUpdateUserMembership 管理端调整/取消用户会员。
// action=cancel 时将 status 置 cancelled；expiresAt 非 nil 时直接覆盖到期时间。
func (s *MembershipService) AdminUpdateUserMembership(ctx context.Context, id uint64, action *string, expiresAt *time.Time) error {
	m, err := s.memberRepo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("会员记录不存在: %w", err)
	}

	updates := map[string]interface{}{}
	if action != nil {
		switch *action {
		case "cancel":
			if m.Status == "cancelled" {
				return fmt.Errorf("会员记录已是 cancelled 状态")
			}
			updates["status"] = "cancelled"
		default:
			return fmt.Errorf("无效 action，支持：cancel")
		}
	}
	if expiresAt != nil {
		updates["expires_at"] = *expiresAt
	}
	if len(updates) == 0 {
		return fmt.Errorf("无可更新字段（需提供 action 或 expires_at）")
	}
	return s.memberRepo.UpdateByID(ctx, id, updates)
}
