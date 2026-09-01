package service

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// 仅按发布合同的显式等级集合校验，不按等级排序猜测继承，也不把购买时会员价格变成调用规则。
func videoMembershipAccess(tx *gorm.DB, userID uint64, levels []uint64, now time.Time, proofs ...*videoAccessExpiry) error {
	if len(levels) == 0 {
		return nil
	}
	var configured []struct{ ID uint64 }
	if err := tx.Table("membership_levels").Clauses(clause.Locking{Strength: "SHARE"}).Select("id").Where("id IN ? AND status='active'", levels).Find(&configured).Error; err != nil {
		return ErrVideoAccessUnavailable
	}
	if len(configured) != len(levels) {
		return ErrVideoAccessUnavailable
	}
	var matches []struct {
		ID                         uint64
		ExpiresAt, ParentExpiresAt *time.Time
	}
	if err := tx.Table("user_memberships m").Clauses(clause.Locking{Strength: "SHARE"}).Select("m.id,m.expires_at,a.expires_at AS parent_expires_at").
		Joins("LEFT JOIN user_assets a ON a.id=m.asset_id AND a.user_id=m.user_id").
		Where("m.user_id=? AND m.level_id IN ? AND m.status='active' AND m.started_at<=? AND (m.expires_at IS NULL OR m.expires_at>?)", userID, levels, now, now).
		Where("m.asset_id IS NULL OR (a.id IS NOT NULL AND a.status='active' AND (a.started_at IS NULL OR a.started_at<=?) AND (a.expires_at IS NULL OR a.expires_at>?))", now, now).Find(&matches).Error; err != nil {
		return ErrVideoAccessUnavailable
	}
	if len(matches) == 0 {
		return ErrVideoEntitlementDenied
	}
	paths := make([][]*time.Time, 0, len(matches))
	for _, m := range matches {
		paths = append(paths, []*time.Time{m.ExpiresAt, m.ParentExpiresAt})
	}
	firstVideoAccessExpiry(proofs).alternatives(paths, ErrVideoEntitlementDenied)
	return nil
}
