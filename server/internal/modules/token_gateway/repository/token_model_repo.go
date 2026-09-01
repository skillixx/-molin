package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
)

// ErrTokenModelNotFound 模型目录记录不存在（RowsAffected==0 守卫）。
var ErrTokenModelNotFound = errors.New("模型不存在")

// 视频模型的写入必须经过专用幂等、CAS与加密审计命令，旧CRUD不能作为旁路。
var ErrTokenVideoModelManaged = errors.New("视频模型需要受控管理")

// TokenModelRepository 对外模型目录数据访问层。
type TokenModelRepository struct {
	db *gorm.DB
}

// NewTokenModelRepository 创建模型目录仓库实例。
func NewTokenModelRepository(db *gorm.DB) *TokenModelRepository {
	return &TokenModelRepository{db: db}
}

// Create 创建模型目录记录。
func (r *TokenModelRepository) Create(ctx context.Context, m *model.TokenModel) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// FindByID 按 ID 查询模型。
func (r *TokenModelRepository) FindByID(ctx context.Context, id uint64) (*model.TokenModel, error) {
	var m model.TokenModel
	if err := r.db.WithContext(ctx).First(&m, id).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// FindByCode 按逻辑模型名查询模型（chat 透传前的模型校验用）。
func (r *TokenModelRepository) FindByCode(ctx context.Context, code string) (*model.TokenModel, error) {
	var m model.TokenModel
	if err := r.db.WithContext(ctx).Where("logical_model_code = ?", code).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// ListPaged 分页查询模型目录，支持 status、modality 过滤（空字符串不过滤）。
// 返回扁平分页二元组 (items, total)，handler 后续包 {items,page,page_size,total}。
func (r *TokenModelRepository) ListPaged(ctx context.Context, status, modality string, offset, limit int) ([]model.TokenModel, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.TokenModel{})
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if modality != "" {
		query = query.Where("modality = ?", modality)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var items []model.TokenModel
	if err := query.Order("sort_order ASC, id ASC").Offset(offset).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// ListActiveCandidates 返回全部 active 模型（可选 modality 过滤），按展示顺序排序。
// 供用户端定向可见性过滤：模型目录规模小，全量加载候选后在应用层判可见性再分页（保证分页 total 准确）。
func (r *TokenModelRepository) ListActiveCandidates(ctx context.Context, modality string) ([]model.TokenModel, error) {
	query := r.db.WithContext(ctx).Model(&model.TokenModel{}).Where("status = ?", "active")
	if modality != "" {
		query = query.Where("modality = ?", modality)
	}
	var items []model.TokenModel
	if err := query.Order("sort_order ASC, id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// PublicModelCandidate把视频工作副本与当前发布快照一起读取，避免目录读取过程中跨版本拼接。
// 非视频继续使用既有目录语义；快照原文只在服务层内解析，不作为HTTP响应。
type PublicModelCandidate struct {
	model.TokenModel
	PublishedVideoSnapshot json.RawMessage `gorm:"column:published_video_snapshot" json:"-"`
	PublishedModality      string          `gorm:"column:published_modality" json:"-"`
}

func (r *TokenModelRepository) ListPublicCandidates(ctx context.Context, modality string) ([]PublicModelCandidate, error) {
	now := time.Now().UTC()
	query := r.db.WithContext(ctx).Table("token_models AS m").
		// 即使草稿被改成Chat/Image也读取发布身份，禁止借模态修改绕过视频快照保护。
		Select("m.*, COALESCE(JSON_UNQUOTE(JSON_EXTRACT(published.snapshot_json,'$.modality')),'') AS published_modality, CASE WHEN m.release_version_no>0 AND m.published_at<=? AND published.status='active' AND published.published_at<=? THEN published.snapshot_json ELSE NULL END AS published_video_snapshot", now, now).
		Joins("LEFT JOIN ai_model_release_versions AS published ON published.model_id=m.id AND published.version_no=m.release_version_no").
		Where("m.status='active'")
	if modality != "" {
		query = query.Where("m.modality=?", modality)
	}
	var items []PublicModelCandidate
	err := query.Order("m.sort_order ASC, m.id ASC").Scan(&items).Error
	return items, err
}

// Update 更新模型字段（map 方式支持零值更新）。
func (r *TokenModelRepository) Update(ctx context.Context, id uint64, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.TokenModel{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTokenModelNotFound
	}
	return nil
}

// Delete 删除模型目录记录。
func (r *TokenModelRepository) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.TokenModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", id).Take(&item).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTokenModelNotFound
			}
			return err
		}
		if item.Modality == "video" || len(item.VideoContractJSON) > 0 {
			return ErrTokenVideoModelManaged
		}
		var videoRelease int64
		if err := tx.Table("ai_model_release_versions").Where("model_id=? AND version_no=? AND JSON_UNQUOTE(JSON_EXTRACT(snapshot_json,'$.modality'))='video'", id, item.ReleaseVersionNo).Count(&videoRelease).Error; err != nil {
			return err
		}
		if videoRelease != 0 {
			return ErrTokenVideoModelManaged
		}
		result := tx.Delete(&item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrTokenModelNotFound
		}
		return nil
	})
}
