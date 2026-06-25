package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	appmodel "molin/server/internal/modules/app/model"
	assetmodel "molin/server/internal/modules/asset/model"
	productmodel "molin/server/internal/modules/product/model"
)

// DBAccessChecker 基于 DB 的 entitlement 闸门实现：
//
//	presenton 应用(code) → applications.id
//	→ products(product_type=application, business_ref_id=应用id).id
//	→ user_assets(user_id, product_id, status=active, 未过期) 存在即放行。
type DBAccessChecker struct {
	db      *gorm.DB
	appCode string // presenton 应用 code，如 "presenton-ppt"
}

// NewDBAccessChecker 构造闸门。appCode 为 presenton 应用在 applications 表的 code。
func NewDBAccessChecker(db *gorm.DB, appCode string) *DBAccessChecker {
	return &DBAccessChecker{db: db, appCode: appCode}
}

// HasActiveAccess 判定用户是否对 presenton 应用有有效开通。
func (c *DBAccessChecker) HasActiveAccess(ctx context.Context, userID uint64) (bool, error) {
	// ① 应用 code → 应用 ID。
	var app appmodel.Application
	if err := c.db.WithContext(ctx).
		Where("code = ?", c.appCode).
		First(&app).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 应用未注册：视为不可访问（而非报错），闸门 fail-safe。
			return false, nil
		}
		return false, err
	}

	// ② 应用 ID → 商品 ID（product_type=application 且 business_ref_id 指向该应用）。
	var product productmodel.Product
	if err := c.db.WithContext(ctx).
		Where("product_type = ? AND business_ref_id = ?", "application", app.ID).
		First(&product).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil // 未上架为商品：不可访问。
		}
		return false, err
	}

	// ③ 该用户是否有 active 且未过期的开通记录。
	now := time.Now()
	var count int64
	if err := c.db.WithContext(ctx).
		Model(&assetmodel.UserAsset{}).
		Where("user_id = ? AND product_id = ? AND status = ?", userID, product.ID, "active").
		Where("expires_at IS NULL OR expires_at > ?", now).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
