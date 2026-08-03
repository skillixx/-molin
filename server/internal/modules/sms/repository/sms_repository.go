package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"molin/server/internal/modules/sms/model"
)

var ErrBindingNotFound = errors.New("短信场景未绑定可用模板")

// SMSRepository 提供阶段 1 发送链路所需的最小数据访问能力。
type SMSRepository struct {
	db *gorm.DB
}

func NewSMSRepository(db *gorm.DB) *SMSRepository { return &SMSRepository{db: db} }

// FindActiveBinding 只返回启用绑定且模板已审核、已本地启用的数据库快照。
func (r *SMSRepository) FindActiveBinding(ctx context.Context, scene string) (*model.SceneBinding, error) {
	var binding model.SceneBinding
	err := r.db.WithContext(ctx).
		Preload("Template").
		Where("scene = ? AND enabled = ?", scene, true).
		First(&binding).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrBindingNotFound
		}
		return nil, err
	}
	if binding.Template.ID == 0 || !binding.Template.LocalEnabled || binding.Template.ProviderAuditStatus != "approved" {
		return nil, ErrBindingNotFound
	}
	return &binding, nil
}

// CreateSendLog 写入最终提交状态；模型本身不包含完整手机号和验证码字段。
func (r *SMSRepository) CreateSendLog(ctx context.Context, log *model.SendLog) error {
	return r.db.WithContext(ctx).Create(log).Error
}
