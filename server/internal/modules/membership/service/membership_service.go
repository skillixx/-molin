package service

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/membership/model"
	"molin/server/internal/modules/membership/repository"
)

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
func (s *MembershipService) AdminListUserMemberships(ctx context.Context, userID uint64, page, pageSize int) ([]*model.UserMembership, int64, error) {
	offset := (page - 1) * pageSize
	return s.memberRepo.ListByUserID(ctx, userID, offset, pageSize)
}
