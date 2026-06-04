package repository

import (
	"context"

	"gorm.io/gorm"
	"molin/server/internal/modules/identity/model"
)

// IdentityRepository 实名认证数据访问层。
type IdentityRepository struct {
	db *gorm.DB
}

func NewIdentityRepository(db *gorm.DB) *IdentityRepository {
	return &IdentityRepository{db: db}
}

func (r *IdentityRepository) Create(ctx context.Context, v *model.IdentityVerification) error {
	return r.db.WithContext(ctx).Create(v).Error
}

func (r *IdentityRepository) FindByID(ctx context.Context, id uint64) (*model.IdentityVerification, error) {
	var v model.IdentityVerification
	err := r.db.WithContext(ctx).First(&v, id).Error
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// FindActiveByUser 查询用户当前有效的认证记录（pending 或 verified）。
func (r *IdentityRepository) FindActiveByUser(ctx context.Context, userID uint64) (*model.IdentityVerification, error) {
	var v model.IdentityVerification
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status IN ?", userID, []string{"pending", "verified"}).
		Order("created_at DESC").
		First(&v).Error
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// ExistsByHMAC 查重：身份证号 hash 是否已被其他用户绑定。
func (r *IdentityRepository) ExistsByHMAC(ctx context.Context, hmacHash string, excludeUserID uint64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.IdentityVerification{}).
		Where("id_card_no_hash = ? AND user_id != ? AND status = 'verified'", hmacHash, excludeUserID).
		Count(&count).Error
	return count > 0, err
}

func (r *IdentityRepository) UpdateStatus(db *gorm.DB, id uint64, status, rejectReason string) error {
	updates := map[string]interface{}{"status": status}
	if rejectReason != "" {
		updates["reject_reason"] = rejectReason
	}
	return db.Model(&model.IdentityVerification{}).Where("id = ?", id).Updates(updates).Error
}

func (r *IdentityRepository) CreateLog(db *gorm.DB, log *model.IdentityVerificationLog) error {
	return db.Create(log).Error
}

// ListPending 管理员查看待审核列表。
func (r *IdentityRepository) ListPending(ctx context.Context) ([]model.IdentityVerification, error) {
	var list []model.IdentityVerification
	err := r.db.WithContext(ctx).Where("status = 'pending'").Order("created_at ASC").Find(&list).Error
	return list, err
}
