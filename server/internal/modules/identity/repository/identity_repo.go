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
// Submit 提交时用于重复检查：pending/verified 时不允许再次提交；rejected 允许重新提交。
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

// FindLatestByUser 查询用户最新一条认证记录（不过滤 status），按 created_at DESC 取第一条。
// D-05：GetMyVerification 改用此方法，确保被拒绝的用户也能查到拒绝原因及记录。
func (r *IdentityRepository) FindLatestByUser(ctx context.Context, userID uint64) (*model.IdentityVerification, error) {
	var v model.IdentityVerification
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
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

// UpdateStatus 更新认证记录状态。
// D-10：只有审核拒绝（status == "rejected"）且 rejectReason 非空时才写入 reject_reason 列，
// 审核通过（status == "verified"）时不写该列，避免误把驳回理由写入通过记录。
// D-11：UPDATE 语句增加 "AND status = 'pending'" 条件，防止并发审核请求重复提交；
// 返回 rowsAffected，调用方据此判断记录是否已被并发请求修改（rowsAffected == 0 表示已审核）。
func (r *IdentityRepository) UpdateStatus(db *gorm.DB, id uint64, status, rejectReason string) (int64, error) {
	updates := map[string]interface{}{"status": status}
	if status == "rejected" && rejectReason != "" {
		updates["reject_reason"] = rejectReason
	}
	result := db.Model(&model.IdentityVerification{}).
		Where("id = ? AND status = 'pending'", id).
		Updates(updates)
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func (r *IdentityRepository) CreateLog(db *gorm.DB, log *model.IdentityVerificationLog) error {
	return db.Create(log).Error
}

// ListPaged 管理员分页查看认证记录，status 非空时按状态过滤，空字符串时查全部状态。
// 替代原先硬编码 pending 的 ListPendingPaged，保持向下兼容：调用方传 "pending" 即为原行为。
func (r *IdentityRepository) ListPaged(ctx context.Context, status string, offset, limit int) ([]model.IdentityVerification, int64, error) {
	var list []model.IdentityVerification
	var total int64
	db := r.db.WithContext(ctx).Model(&model.IdentityVerification{})
	// status 非空时追加过滤条件；空字符串表示查询全部状态
	if status != "" {
		db = db.Where("status = ?", status)
	}
	// 先查总数
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	// 再查分页数据，按提交时间升序排列
	if err := db.Order("created_at ASC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
