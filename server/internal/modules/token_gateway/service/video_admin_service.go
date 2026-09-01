package service

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/config"
	authmodel "molin/server/internal/modules/auth/model"
	authrepo "molin/server/internal/modules/auth/repository"
	authservice "molin/server/internal/modules/auth/service"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrVideoAdminForbidden = errors.New("无管理权限")
	ErrVideoAdminMFA       = errors.New("请先完成管理员双重认证（手机+邮箱）")
)

type VideoAdminService struct {
	app                *VideoHTTPService
	verifyHours        int
	reasons            *VideoAdminReasonProtector
	pollProvider       VideoAdminPollProvider
	archive            *VideoAdminArchiveOptions
	adjustmentsEnabled bool
	modelDrafts        *VideoModelDraftOptions
	modelPublishing    *VideoModelPublishOptions
}

// 管理写依赖必须显式传入；只读构造保持兼容，不从Prompt或供应商密钥推导管理原因密钥。
type VideoAdminWriteOptions struct {
	ReasonProtector    *VideoAdminReasonProtector
	PollProvider       VideoAdminPollProvider
	Archive            *VideoAdminArchiveOptions
	AdjustmentsEnabled bool
	ModelDrafts        *VideoModelDraftOptions
	ModelPublishing    *VideoModelPublishOptions
}
type VideoAdminTaskDetails struct {
	*VideoTaskDetails
	UserID    uint64  `json:"user_id"`
	ProjectID uint64  `json:"project_id"`
	APIKeyID  *uint64 `json:"api_key_id"`
}

// 管理MFA沿用auth权威规则，0仍表示既有不过期语义；配置必须显式传入且不能发生时长溢出。
func NewVideoAdminService(app *VideoHTTPService, verifyHours int, writes ...VideoAdminWriteOptions) (*VideoAdminService, error) {
	if app == nil || app.db == nil || verifyHours < 0 || int64(verifyHours) > math.MaxInt64/int64(time.Hour) {
		return nil, ErrVideoAccessUnavailable
	}
	if len(writes) > 1 {
		return nil, ErrVideoAccessUnavailable
	}
	s := &VideoAdminService{app: app, verifyHours: verifyHours}
	if len(writes) == 1 {
		p := writes[0].ReasonProtector
		if p == nil || len(p.key) != 32 || len(p.digestKey) != 32 {
			return nil, ErrVideoAccessUnavailable
		}
		s.reasons = p
		s.pollProvider = writes[0].PollProvider
		s.adjustmentsEnabled = writes[0].AdjustmentsEnabled
		if writes[0].ModelDrafts != nil {
			copy := *writes[0].ModelDrafts
			s.modelDrafts = &copy
		}
		if writes[0].ModelPublishing != nil {
			copy, err := copyVideoModelPublishOptions(*writes[0].ModelPublishing)
			if err != nil {
				return nil, err
			}
			s.modelPublishing = copy
		}
		if writes[0].Archive != nil {
			copy := *writes[0].Archive
			s.archive = &copy
		}
	}
	return s, nil
}

func (s *VideoAdminService) WritesReady() bool {
	return s != nil && s.app != nil && s.app.db != nil && s.app.billing != nil && s.reasons != nil
}

// 仅用于已验证不可变解除申请中的发起者，复核持久化资格，不制造历史发起者JWT。
// 当前HTTP操作者仍必须调用authorizeTx；这里不是可公开调用的替代认证入口。
type videoReleaseMakerDeadline struct {
	permissionUntil *time.Time
	mfaUntil        *time.Time
}

func (s *VideoAdminService) authorizeReleaseMakerTx(ctx context.Context, tx *gorm.DB, makerID uint64) (*videoReleaseMakerDeadline, error) {
	return s.authorizeApprovalMakerTx(ctx, tx, makerID, "ai_gateway:safety_review")
}

func (s *VideoAdminService) authorizeApprovalMakerTx(ctx context.Context, tx *gorm.DB, makerID uint64, permission string) (*videoReleaseMakerDeadline, error) {
	tx = tx.WithContext(ctx)
	var user authmodel.User
	if makerID == 0 {
		return nil, ErrVideoAdminForbidden
	}
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id,status,admin_phone_verified_at,admin_email_verified_at").Where("id=?", makerID).Take(&user).Error; err != nil {
		return nil, videoAccessReadError(err, ErrVideoAdminForbidden)
	}
	if user.Status != "active" {
		return nil, ErrVideoAdminForbidden
	}
	allowed, until, err := newVideoFreshIAM(tx).CheckPermissionFreshWithExpiry(ctx, makerID, permission)
	if err != nil {
		return nil, errors.Join(ErrVideoAccessUnavailable, err)
	}
	if !allowed {
		return nil, ErrVideoAdminForbidden
	}
	auth := authservice.NewAuthService(authrepo.NewUserRepository(tx.Clauses(clause.Locking{Strength: "SHARE"})), nil, nil, nil, config.Config{AdminVerifyExpireHours: s.verifyHours}, nil, nil, nil, tx)
	valid, err := auth.CheckAdminVerified(ctx, makerID)
	if err != nil {
		return nil, errors.Join(ErrVideoAccessUnavailable, err)
	}
	if !valid {
		return nil, ErrVideoAdminMFA
	}
	now := time.Now().UTC()
	if until != nil && !until.After(now) {
		return nil, ErrVideoAdminForbidden
	}
	var mfaUntil *time.Time
	for _, verified := range []*time.Time{user.AdminPhoneVerifiedAt, user.AdminEmailVerifiedAt} {
		if verified == nil || verified.After(now) {
			return nil, ErrVideoAdminMFA
		}
		if s.verifyHours > 0 {
			expires := verified.Add(time.Duration(s.verifyHours) * time.Hour)
			if !expires.After(now) {
				return nil, ErrVideoAdminMFA
			}
			if mfaUntil == nil || expires.Before(*mfaUntil) {
				mfaUntil = &expires
			}
		}
	}
	return &videoReleaseMakerDeadline{permissionUntil: until, mfaUntil: mfaUntil}, ctx.Err()
}

func (s *VideoAdminService) authorizeTx(ctx context.Context, tx *gorm.DB, caller VideoCaller, permission string) error {
	if caller.APIKeyID != 0 || caller.UserID == 0 {
		return ErrVideoAdminForbidden
	}
	if err := revalidateVideoReadCredential(ctx, caller); err != nil {
		return err
	}
	// 保留可克隆的干净事务基准；不能把用户SELECT/WHERE带入后续IAM及MFA仓储查询。
	tx = tx.WithContext(ctx)
	var user authmodel.User
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Select("id,status,admin_phone_verified_at,admin_email_verified_at").Where("id=?", caller.UserID).Take(&user).Error; err != nil {
		return videoAccessReadError(err, ErrVideoAdminForbidden)
	}
	if user.Status != "active" {
		return ErrVideoAdminForbidden
	}
	allowed, permissionUntil, err := newVideoFreshIAM(tx).CheckPermissionFreshWithExpiry(ctx, caller.UserID, permission)
	if err != nil {
		return ErrVideoAccessUnavailable
	}
	if !allowed {
		return ErrVideoAdminForbidden
	}
	// 真实AuthService每次读取同事务的用户事实，不用恒true的MFA替身或目标用户身份。
	auth := authservice.NewAuthService(authrepo.NewUserRepository(tx.Clauses(clause.Locking{Strength: "SHARE"})), nil, nil, nil, config.Config{AdminVerifyExpireHours: s.verifyHours}, nil, nil, nil, tx)
	mfaValid, mfaErr := auth.CheckAdminVerified(ctx, caller.UserID)
	if mfaErr != nil {
		return ErrVideoAccessUnavailable
	}
	if !mfaValid {
		return ErrVideoAdminMFA
	}
	if err := revalidateVideoReadCredential(ctx, caller); err != nil {
		return err
	}
	now := time.Now().UTC()
	if permissionUntil != nil && !permissionUntil.After(now) {
		return ErrVideoAdminForbidden
	}
	// 最后的吊销读取也可能等待；复核已锁定的两个MFA截止时间，不扩展原auth定义。
	for _, verified := range []*time.Time{user.AdminPhoneVerifiedAt, user.AdminEmailVerifiedAt} {
		if verified == nil || verified.After(now) || (s.verifyHours > 0 && !verified.Add(time.Duration(s.verifyHours)*time.Hour).After(now)) {
			return ErrVideoAdminMFA
		}
	}
	return ctx.Err()
}

// 管理员读目标归属事实，不冒充目标用户JWT，也不要求目标用户当前仍有调用模型的资格。
func (s *VideoAdminService) GetTask(ctx context.Context, caller VideoCaller, id string) (*VideoAdminTaskDetails, error) {
	if s == nil || s.app == nil || s.app.db == nil {
		return nil, ErrVideoAccessUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var result *VideoAdminTaskDetails
	err := s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.authorizeTx(ctx, tx, caller, "ai_gateway:view"); err != nil {
			return err
		}
		if !videoBillingPublicID.MatchString(id) {
			return repository.ErrVideoTaskNotFound
		}
		var identity struct {
			UserID, ProjectID uint64
			APIKeyID          *uint64
		}
		q := tx.Table("ai_gateway_tasks t").Select("t.user_id,t.project_id,t.api_key_id").Joins("JOIN ai_requests r ON r.request_id=t.request_id AND r.user_id=t.user_id AND r.project_id=t.project_id AND r.api_key_id <=> t.api_key_id AND r.logical_model_code=t.logical_model_code AND r.operation=t.operation").Where("t.public_id=? AND t.capability='video.generate' AND t.operation IN ('text_to_video','image_to_video') AND r.modality='video' AND r.capability='video.generate'", id)
		if err := q.Take(&identity).Error; err != nil {
			return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
		}
		owner := repository.VideoOwner{UserID: identity.UserID, ProjectID: identity.ProjectID, APIKeyID: identity.APIKeyID}
		task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, id, owner)
		if err != nil {
			return err
		}
		details, err := s.app.taskDetailsTx(ctx, tx, task, owner)
		if err != nil {
			return err
		}
		if err := s.authorizeTx(ctx, tx, caller, "ai_gateway:view"); err != nil {
			return err
		}
		result = &VideoAdminTaskDetails{VideoTaskDetails: details, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	return result, nil
}
