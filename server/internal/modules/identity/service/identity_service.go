package service

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"regexp"
	"strconv"
	"strings"
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
	// D-06：拒绝审核时必须填写驳回理由
	ErrReasonRequired = errors.New("驳回时必须填写理由")
	// D-40：批准审核时发现同一身份证号已被其他用户 verified，返回 409 冲突错误
	ErrIDCardAlreadyVerified = errors.New("该身份证号已被其他账号实名认证，无法重复绑定")
	// D-77：输入参数校验错误（姓名/身份证格式）—— handler 层映射为 400
	ErrInvalidRealName = errors.New("真实姓名不能为空")
	ErrRealNameTooLong = errors.New("真实姓名不能超过 50 个字符")
	ErrInvalidIDCardNo = errors.New("身份证号格式不正确")
	// D-80：附件校验错误 —— handler 层映射为 400
	ErrTooManyAttachments   = errors.New("附件数量不能超过 5 个")
	ErrAttachmentNotHTTPS   = errors.New("附件 URL 必须以 https:// 开头")
	// D-81：驳回理由过长 —— handler 层映射为 400
	ErrReasonTooLong = errors.New("驳回理由不能超过 500 字")
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

// idCardRegexp 身份证号正则：18 位，最后一位允许大写或小写 X。
var idCardRegexp = regexp.MustCompile(`^\d{17}[\dXx]$`)

// maxRealNameLen 真实姓名最大字符数（Unicode 字符计，非字节数）。
const maxRealNameLen = 50

// maxAttachments 附件 URL 最大数量。
const maxAttachments = 5

// Submit 用户提交实名认证。身份证号仅在内存处理，不持久化明文。
// 返回新建记录的 ID，供 handler 响应给客户端。
func (s *IdentityService) Submit(ctx context.Context, userID uint64, req dto.SubmitReq) (uint64, error) {
	// D-77：真实姓名非空校验
	if strings.TrimSpace(req.RealName) == "" {
		return 0, ErrInvalidRealName
	}
	// D-77：真实姓名长度上限（Unicode 字符数，防止超长字符串写入 VARCHAR(128)）
	if len([]rune(req.RealName)) > maxRealNameLen {
		return 0, ErrRealNameTooLong
	}
	// D-77：身份证号格式校验：18 位，最后一位允许大写或小写 X
	if !idCardRegexp.MatchString(req.IDCardNo) {
		return 0, ErrInvalidIDCardNo
	}
	// D-80：附件数量上限校验，防止海量 URL 占用存储
	if len(req.Attachments) > maxAttachments {
		return 0, ErrTooManyAttachments
	}
	// D-80：附件 URL 必须以 https:// 开头，防止 http:// 内网地址 SSRF
	for _, u := range req.Attachments {
		if !strings.HasPrefix(u, "https://") {
			return 0, ErrAttachmentNotHTTPS
		}
	}
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
	// D-43：原局部变量命名为 s，与接收者 s *IdentityService 同名导致遮蔽，改为 attachmentsStr
	attachmentsJSON := (*string)(nil)
	if len(req.Attachments) > 0 {
		b, _ := json.Marshal(req.Attachments)
		attachmentsStr := string(b)
		attachmentsJSON = &attachmentsStr
	}
	// D-78：将 repo.Create 和 userRepo.UpdateRealNameStatus 纳入同一事务，
	// 确保 identity_verifications.status='pending' 与 users.real_name_status 保持一致。
	var newID uint64
	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		v := &model.IdentityVerification{
			UserID:          userID,
			RealName:        req.RealName,
			IDCardNoHash:    hmacHash,
			IDCardNoMasked:  maskIDCard(req.IDCardNo),
			AttachmentsJSON: attachmentsJSON,
			Status:          "pending",
		}
		if err := s.repo.Create(ctx, v); err != nil {
			return err
		}
		newID = v.ID
		// 同步更新 users.real_name_status='pending'，失败时回滚
		if err := s.userRepo.UpdateRealNameStatus(tx, userID, "pending", ""); err != nil {
			log.Printf("[identity] Submit: 同步 real_name_status 失败 userID=%d err=%v", userID, err)
			return err
		}
		return nil
	})
	if txErr != nil {
		return 0, txErr
	}
	return newID, nil
}

// maxReasonLen 驳回理由最大字符数（Unicode 字符计），与 DB 列 reject_reason VARCHAR(512) 留余量。
const maxReasonLen = 500

// Review 管理员审核：approve=true 通过，false 拒绝。
func (s *IdentityService) Review(ctx context.Context, verificationID, operatorID uint64, approve bool, reason string) error {
	// D-06：拒绝审核时必须填写驳回理由（去除首尾空格后非空）
	if !approve && strings.TrimSpace(reason) == "" {
		return ErrReasonRequired
	}
	// D-81：驳回理由长度校验，超过 500 字符时返回 400，而非 MySQL Data too long 导致 500
	if len([]rune(reason)) > maxReasonLen {
		return ErrReasonTooLong
	}
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
		// D-40：批准路径在事务内再次检查同一身份证号是否已有其他用户的 verified 记录，
		// 防止多条 pending 记录被逐一批准导致一证多号竟态
		if approve {
			conflict, err := s.repo.ExistsByHMACVerified(tx, verification.IDCardNoHash, verification.UserID)
			if err != nil {
				return err
			}
			if conflict {
				return ErrIDCardAlreadyVerified
			}
		}
		// D-11：UpdateStatus 内部带 "AND status = 'pending'" 条件，rowsAffected == 0
		// 说明记录已被并发请求审核完成，回滚事务并返回已审核错误
		rowsAffected, err := s.repo.UpdateStatus(tx, verificationID, newStatus, reason)
		if err != nil {
			return err
		}
		if rowsAffected == 0 {
			return ErrVerificationAlreadyReviewed
		}
		// D-02：统一更新 verified_at（审核时间），不再仅在通过时写入
		if err := tx.Model(&model.IdentityVerification{}).
			Where("id = ?", verificationID).
			Update("verified_at", &now).Error; err != nil {
			return err
		}
		// D-09：CreateLog 错误需检查，失败时回滚事务
		if err := s.repo.CreateLog(tx, &model.IdentityVerificationLog{
			VerificationID: verificationID,
			UserID:         verification.UserID,
			Action:         newStatus,
			OperatorID:     &operatorID,
			Remark:         &reason,
		}); err != nil {
			return err
		}
		if approve {
			// 批准：同步 users.real_name_status='verified'
			return s.userRepo.UpdateRealNameStatus(tx, verification.UserID, "verified", verification.RealName)
		}
		// D-42：拒绝时同步更新 users.real_name_status='rejected'，
		// 确保 GET /api/me 实名状态与 identity_verifications.status 一致
		return s.userRepo.UpdateRealNameStatus(tx, verification.UserID, "rejected", "")
	})
	if txErr != nil {
		return txErr
	}
	// D-44：审计 action 改为"动词+结果"命名风格，与 iam/auth 模块保持一致
	// D-45：auditSvc nil 守卫，防止未注入时 panic（与 auth 模块 recordBanAudit 风格一致）
	if s.auditSvc == nil {
		return nil
	}
	auditAction := "reject_verification"
	if approve {
		auditAction = "approve_verification"
	}
	// D-84：审计日志中不记录 reason 原文，防止驳回理由含 PII 持久化并通过 API 泄露。
	// 仅保留操作元数据（approve 布尔值、verification_id），满足合规审计需求。
	_ = s.auditSvc.Record(ctx, &operatorID, "identity", auditAction,
		strPtr("identity_verification"), strPtr(strconv.FormatUint(verificationID, 10)), "",
		map[string]any{
			"approve":         approve,
			"verification_id": verificationID,
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

// GetByUserID A-31：管理员查看指定用户的最新实名认证记录（用户详情页身份卡片）。
// 无记录时返回 nil, nil（由 handler 转换为 404）。
func (s *IdentityService) GetByUserID(ctx context.Context, userID uint64) (*dto.VerificationResp, error) {
	v, err := s.repo.FindLatestByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return toResp(v), nil
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
	// D-41：将 AttachmentsJSON 反序列化后填入响应，管理员审核时可查看证件照片 URL 列表
	if v.AttachmentsJSON != nil && *v.AttachmentsJSON != "" {
		var attachments []string
		if err := json.Unmarshal([]byte(*v.AttachmentsJSON), &attachments); err == nil {
			resp.Attachments = attachments
		}
	}
	return resp
}
