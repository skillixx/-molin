package service

import (
	"context"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	assetmodel "molin/server/internal/modules/asset/model"
)

// 只有存储商品的当前使用授权、同用户父资产与有效有限额权益同时成立，才能预占或确认容量。
func (s *VideoHTTPService) saveEntitlementTx(ctx context.Context, tx *gorm.DB, userID, existingID uint64, amount decimal.Decimal) (*assetmodel.UserEntitlement, error) {
	p := s.savePolicy
	now := time.Now().UTC()
	if err := videoProductAccess(ctx, tx, newVideoFreshIAM(tx), userID, p.StorageProductID, VideoModelContract{}, now); err != nil {
		return nil, err
	}
	var products int64
	if err := tx.Table("products").Where("id=? AND product_type='storage' AND status='active'", p.StorageProductID).Count(&products).Error; err != nil {
		return nil, ErrVideoSaveUnavailable
	}
	if products != 1 {
		return nil, ErrVideoEntitlementDenied
	}
	q := tx.Table("user_entitlements e").Select("e.id,e.asset_id").Joins("JOIN user_assets a ON a.id=e.asset_id AND a.user_id=e.user_id AND a.product_id=e.product_id").
		Where("e.user_id=? AND e.product_id=? AND e.entitlement_type=? AND e.quota_unit=? AND e.status='active' AND a.status='active'", userID, p.StorageProductID, p.EntitlementType, p.QuotaUnit).
		Where("(e.started_at IS NULL OR e.started_at<=?) AND (e.expires_at IS NULL OR e.expires_at>?) AND (a.started_at IS NULL OR a.started_at<=?) AND (a.expires_at IS NULL OR a.expires_at>?)", now, now, now, now).
		Where("e.quota_total IS NOT NULL AND e.quota_used>=0 AND e.quota_reserved>=0")
	if existingID != 0 {
		q = q.Where("e.id=?", existingID)
	} else {
		q = q.Where("e.quota_total-e.quota_used-e.quota_reserved>=?", amount)
	}
	var identity struct{ ID, AssetID uint64 }
	if err := q.Order("e.id").Take(&identity).Error; err != nil {
		return nil, videoAccessReadError(err, ErrVideoSaveCapacity)
	}
	// 与资产冻结/过期逻辑一致，先锁父资产，再锁权益，不能倒序形成跨模块死锁。
	var parent assetmodel.UserAsset
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND product_id=?", identity.AssetID, userID, p.StorageProductID).Take(&parent).Error; err != nil {
		return nil, ErrVideoSaveUnavailable
	}
	var ent assetmodel.UserEntitlement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=? AND asset_id=? AND product_id=?", identity.ID, userID, parent.ID, p.StorageProductID).Take(&ent).Error; err != nil {
		return nil, ErrVideoSaveUnavailable
	}
	now = time.Now().UTC()
	if parent.Status != "active" || ent.Status != "active" || (parent.StartedAt != nil && parent.StartedAt.After(now)) || (ent.StartedAt != nil && ent.StartedAt.After(now)) || (parent.ExpiresAt != nil && !parent.ExpiresAt.After(now)) || (ent.ExpiresAt != nil && !ent.ExpiresAt.After(now)) || ent.QuotaTotal == nil || ent.QuotaUsed.IsNegative() || ent.QuotaReserved.IsNegative() || ent.QuotaUnit == nil || *ent.QuotaUnit != p.QuotaUnit || ent.EntitlementType != p.EntitlementType {
		return nil, ErrVideoEntitlementDenied
	}
	if ent.QuotaUsed.Add(ent.QuotaReserved).GreaterThan(*ent.QuotaTotal) || (existingID == 0 && ent.QuotaUsed.Add(ent.QuotaReserved).Add(amount).GreaterThan(*ent.QuotaTotal)) {
		return nil, ErrVideoSaveCapacity
	}
	return &ent, nil
}

// 三层容量都按实际字节统计，失败尚未清理的计划继续占用；不靠进程内计数器跨实例限额。
func (s *VideoHTTPService) reserveSaveCapacityTx(tx *gorm.DB, userID, projectID, total uint64) (bool, error) {
	p := s.savePolicy
	alert := false
	for _, scope := range []struct {
		kind      string
		id, limit uint64
	}{{"global", 0, p.MaxGlobalBytes}, {"user", userID, p.MaxUserBytes}, {"project", projectID, p.MaxProjectBytes}} {
		if err := tx.Exec("INSERT INTO ai_video_asset_save_scopes(scope_type,scope_id) VALUES(?,?) ON DUPLICATE KEY UPDATE scope_id=VALUES(scope_id)", scope.kind, scope.id).Error; err != nil {
			return false, err
		}
		q := tx.Table("ai_video_asset_saves").Select("COALESCE(SUM(total_bytes),0) AS occupied").Where("status<>'aborted'")
		if scope.kind == "user" {
			q = q.Where("user_id=?", userID)
		}
		if scope.kind == "project" {
			q = q.Where("project_id=?", projectID)
		}
		var usage struct{ Occupied decimal.Decimal }
		if err := q.Scan(&usage).Error; err != nil {
			return false, err
		}
		next := usage.Occupied.Add(decimal.NewFromUint64(total))
		if next.GreaterThan(decimal.NewFromUint64(scope.limit)) {
			return false, ErrVideoSaveCapacity
		}
		if scope.kind == "global" {
			alert = next.GreaterThanOrEqual(decimal.NewFromUint64(p.GlobalAlertBytes))
		}
	}
	return alert, nil
}
