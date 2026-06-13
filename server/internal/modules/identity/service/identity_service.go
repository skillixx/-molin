package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
	auditservice "molin/server/internal/modules/audit/service"
	"molin/server/internal/config"
	"molin/server/internal/modules/identity/dto"
	"molin/server/internal/modules/identity/model"
	"molin/server/internal/modules/identity/repository"
	"molin/server/pkg/crypto"
)

var (
	ErrAlreadySubmitted         = errors.New("已有待审核或已通过的认证，不允许重复提交")
	ErrIDCardAlreadyBound       = errors.New("该身份证号已绑定其他账号")
	// D-01：新增错误，用于标识重复审核已完结记录
	ErrVerificationAlreadyReviewed = errors.New("该记录已审核，不可重复操作")
)

// UserUpdater 跨模块接口，由 auth.UserRepository 实现，identity 只注入 interface 避免循环导入。
type UserUpdater interface {
	UpdateRealNameStatus(db *gorm.DB, userID uint64, status, realName string) error
}

// IdentityService 负责实名认证提交、审核、状态写回。
type IdentityService struct {
	repo     *repository.IdentityRepository
	userRepo UserUpdater
	db       *gorm.DB
	cfg      config.Config
	// D-04：注入审计日志服务，用于审核操作写全局审计日志
	auditSvc *auditservice.AuditService
}

func NewIdentityService(
	repo *repository.IdentityRepository,
	userRepo UserUpdater,
	db *gorm.DB,
	cfg config.Config,
	auditSvc *auditservice.AuditService,
) *IdentityService {
	return &IdentityService{repo: repo, userRepo: userRepo, db: db, cfg: cfg, auditSvc: auditSvc}
}

// Submit 用户提交实名认证。身份证号仅在内存处理，不持久化明文。
// 返回新建记录的 ID，供 handler 响应给客户端。
func (s *IdentityService) Submit(ctx context.Context, userID uint64, req dto.SubmitReq) (uint64, error) {
	// 已有 pending/verified 记录时不允许重复提交
	existing, _ := s.repo.FindActiveByUser(ctx, userID)
	if existing != nil {
		return 0, ErrAlreadySubmitted
	}
	// 身份证号 HMAC 查重
	hmacHash := hashIDCard(req.IDCardNo, s.cfg.IDCardHMACSecret)
	if conflict, _ := s.repo.ExistsByHMAC(ctx, hmacHash, userID); conflict {
		return 0, ErrIDCardAlreadyBound
	}
	attachmentsJSON := (*string)(nil)
	if len(req.Attachments) > 0 {
		b, _ := json.Marshal(req.Attachments)
		s := string(b)
		attachmentsJSON = &s
	}
	v := &model.IdentityVerification{
		UserID:          userID,
		RealName:        req.RealName,
		IDCardNoHash:    hmacHash,
		IDCardNoMasked:  maskIDCard(req.IDCardNo),
		AttachmentsJSON: attachmentsJSON,
		Status:          "pending",
	}
	if err := s.repo.Create(ctx, v); err != nil {
		return 0, err
	}
	return v.ID, nil
}

// Review 管理员审核：approve=true 通过，false 拒绝。
func (s *IdentityService) Review(ctx context.Context, verificationID, operatorID uint64, approve bool, reason string) error {
	verification, err := s.repo.FindByID(ctx, verificationID)
	if err != nil {
		return err
	}
	// D-01：只允许对 pending 状态的记录进行审核，已完结记录返回错误
	if verification.Status != "pending" {
		return ErrVerificationAlreadyReviewed
	}
	newStatus := "rejected"
	if approve {
		newStatus = "verified"
	}
	// D-02：无论通过或拒绝，都记录审核时间
	now := time.Now()
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		if err := s.repo.UpdateStatus(tx, verificationID, newStatus, reason); err != nil {
			return err
		}
		// D-02：统一更新 verified_at（审核时间），不再仅在通过时写入
		if err := tx.Model(&model.IdentityVerification{}).
			Where("id = ?", verificationID).
			Update("verified_at", &now).Error; err != nil {
			return err
		}
		s.repo.CreateLog(tx, &model.IdentityVerificationLog{
			VerificationID: verificationID,
			UserID:         verification.UserID,
			Action:         newStatus,
			OperatorID:     &operatorID,
			Remark:         &reason,
		})
		if approve {
			return s.userRepo.UpdateRealNameStatus(tx, verification.UserID, "verified", verification.RealName)
		}
		return nil
	})
	if txErr != nil {
		return txErr
	}
	// D-04：审核操作完成后写入全局审计日志（失败不阻断主流程）
	_ = s.auditSvc.Record(ctx, &operatorID, "identity", "review",
		strPtr("identity_verification"), strPtr(strconv.FormatUint(verificationID, 10)), "",
		map[string]any{
			"approve": approve,
			"reason":  reason,
		})
	return nil
}

// strPtr 返回字符串的指针，用于构造审计日志中的 targetType/targetID 字段。
func strPtr(s string) *string {
	return &s
}

// GetMyVerification 用户查自己的认证状态。
// D-05：改用 FindLatestByUser，确保被拒绝的用户也能查到拒绝原因，不再返回 404。
func (s *IdentityService) GetMyVerification(ctx context.Context, userID uint64) (*dto.VerificationResp, error) {
	v, err := s.repo.FindLatestByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return toResp(v), nil
}

// ListPending 管理员查看待审核列表（不分页，兼容旧调用）。
func (s *IdentityService) ListPending(ctx context.Context) ([]dto.VerificationResp, error) {
	list, err := s.repo.ListPending(ctx)
	if err != nil {
		return nil, err
	}
	resp := make([]dto.VerificationResp, len(list))
	for i, v := range list {
		resp[i] = *toResp(&v)
	}
	return resp, nil
}

// ListPaged 管理员分页查看认证记录，status 非空时按状态过滤，空字符串时查全部状态。
// 原方法名 ListPendingPaged 已直接重命名，调用链均在本模块内，无外部依赖。
func (s *IdentityService) ListPaged(ctx context.Context, status string, offset, limit int) ([]dto.VerificationResp, int64, error) {
	list, total, err := s.repo.ListPaged(ctx, status, offset, limit)
	if err != nil {
		return nil, 0, err
	}
	resp := make([]dto.VerificationResp, len(list))
	for i, v := range list {
		resp[i] = *toResp(&v)
	}
	return resp, total, nil
}

// GetVerification 管理员查认证详情。
func (s *IdentityService) GetVerification(ctx context.Context, id uint64) (*dto.VerificationResp, error) {
	v, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return toResp(v), nil
}

func hashIDCard(idCardNo, secret string) string {
	return crypto.HMAC256(idCardNo, secret)
}

func maskIDCard(idCardNo string) string {
	if len(idCardNo) != 18 {
		return "**"
	}
	return idCardNo[:6] + "********" + idCardNo[14:]
}

func toResp(v *model.IdentityVerification) *dto.VerificationResp {
	resp := &dto.VerificationResp{
		ID:             v.ID,
		UserID:         v.UserID,
		RealName:       v.RealName,
		IDCardNoMasked: v.IDCardNoMasked,
		Status:         v.Status,
		RejectReason:   v.RejectReason,
		SubmittedAt:    v.SubmittedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	// VerifiedAt 字段在审核通过或拒绝后记录，对应 reviewed_at
	if v.VerifiedAt != nil {
		t := v.VerifiedAt.Format("2006-01-02T15:04:05Z07:00")
		resp.ReviewedAt = &t
	}
	return resp
}
