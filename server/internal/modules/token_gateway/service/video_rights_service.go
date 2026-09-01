package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrVideoRightsUnavailable      = errors.New("视频权利政策暂不可用")
	ErrVideoRightsRequired         = errors.New("请先确认当前图生视频权利政策")
	ErrVideoRightsConflict         = errors.New("权利接受幂等请求冲突")
	ErrVideoRightsOwnerJWTRequired = errors.New("仅Project所有者登录身份可以接受政策")
)

// 政策与接受表只承载非商业工程事实；不可直接序列化内部ID及命令指纹。
type videoRightsPolicyRow struct {
	ID                   uint64
	PolicyVersion        string
	Purpose              string
	Title                string
	Body                 string
	BodySHA256           string `gorm:"column:body_sha256"`
	Status               string
	EffectiveAt          time.Time
	ExpiresAt            time.Time
	AcceptanceTTLSeconds uint32 `gorm:"column:acceptance_ttl_seconds"`
	VersionNo            uint64
}

func (videoRightsPolicyRow) TableName() string { return "ai_video_rights_policies" }

type videoRightsAcceptanceRow struct {
	ID                 uint64
	PublicID           string
	UserID             uint64
	ProjectID          uint64
	AcceptedBy         uint64
	PolicyID           uint64
	PolicyVersion      string
	PolicyBodySHA256   string `gorm:"column:policy_body_sha256"`
	CommandKind        string
	IdempotencyKeyHash string
	RequestFingerprint string
	RequestID          string
	AcceptedAt         time.Time
	ExpiresAt          time.Time
}

func (videoRightsAcceptanceRow) TableName() string { return "ai_project_video_rights_acceptances" }

type VideoRightsPolicy struct {
	PolicyVersion                  string    `json:"rights_policy_version"`
	Scope                          string    `json:"scope"`
	Title                          string    `json:"title"`
	Body                           string    `json:"body"`
	EffectiveAt                    time.Time `json:"effective_at"`
	ExpiresAt                      time.Time `json:"expires_at"`
	ProjectOwnerAcceptanceRequired bool      `json:"project_owner_acceptance_required"`
}

// 回执区分历史接受和当前有效；null表示没有历史，不把失效回执误称为有效同意。
type VideoRightsAcceptance struct {
	AcceptanceID          *string    `json:"acceptance_id"`
	ProjectID             uint64     `json:"project_id"`
	CurrentPolicyVersion  *string    `json:"rights_policy_version"`
	AcceptedPolicyVersion *string    `json:"accepted_policy_version"`
	AcceptedAt            *time.Time `json:"accepted_at"`
	ExpiresAt             *time.Time `json:"expires_at"`
	Valid                 bool       `json:"valid"`
	InvalidReason         string     `json:"invalid_reason"`
	Idempotent            bool       `json:"idempotent"`
}

type VideoRightsAcceptCommand struct {
	Caller         VideoCaller
	PolicyVersion  string
	Confirmed      bool
	IdempotencyKey string `json:"-"`
	RequestID      string
}

type VideoRightsService struct {
	db     *gorm.DB
	access *VideoAccessService
	now    func() time.Time
}

func NewVideoRightsService(db *gorm.DB) *VideoRightsService {
	return &VideoRightsService{db: db, access: NewVideoAccessService(db), now: time.Now}
}

// CurrentPolicy仅供认证后的条款阅读，不表示调用者已经具有模型或生成权限。
func (s *VideoRightsService) CurrentPolicy(ctx context.Context, caller VideoCaller) (*VideoRightsPolicy, error) {
	if s == nil || s.db == nil || caller.UserID == 0 {
		return nil, ErrVideoRightsUnavailable
	}
	var policy *videoRightsPolicyRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user struct{ Status string }
		if err := tx.Table("users").Clauses(clause.Locking{Strength: "SHARE"}).Select("status").Where("id=?", caller.UserID).Take(&user).Error; err != nil {
			return videoAccessReadError(err, ErrVideoBillingAccess)
		}
		if user.Status != "active" {
			return ErrVideoBillingAccess
		}
		if caller.APIKeyID != 0 {
			var key struct {
				Status    string
				ExpiresAt *time.Time
			}
			if err := tx.Table("api_keys").Clauses(clause.Locking{Strength: "SHARE"}).Select("status,expires_at").Where("id=? AND user_id=?", caller.APIKeyID, caller.UserID).Take(&key).Error; err != nil {
				return videoAccessReadError(err, ErrVideoBillingAccess)
			}
			if key.Status != "active" || (key.ExpiresAt != nil && !key.ExpiresAt.After(s.now())) {
				return ErrVideoBillingAccess
			}
		}
		var err error
		policy, err = loadVideoRightsPolicyTx(tx, s.now().UTC())
		return err
	})
	if err != nil {
		return nil, err
	}
	return &VideoRightsPolicy{PolicyVersion: policy.PolicyVersion, Scope: policy.Purpose, Title: policy.Title, Body: policy.Body, EffectiveAt: policy.EffectiveAt, ExpiresAt: policy.ExpiresAt, ProjectOwnerAcceptanceRequired: true}, nil
}

func loadVideoRightsPolicyTx(tx *gorm.DB, now time.Time) (*videoRightsPolicyRow, error) {
	policy, err := findVideoRightsPolicyTx(tx)
	if err != nil || !videoRightsPolicyEffective(policy, now) {
		return nil, ErrVideoRightsUnavailable
	}
	return policy, nil
}

// 当前政策缺失或自然失效不抹掉历史回执；配置损坏和数据库故障仍必须报错，不能伪装未接受。
func findVideoRightsPolicyTx(tx *gorm.DB) (*videoRightsPolicyRow, error) {
	var rows []videoRightsPolicyRow
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("status='active'").Limit(2).Find(&rows).Error; err != nil || len(rows) > 1 {
		return nil, ErrVideoRightsUnavailable
	}
	if len(rows) == 0 {
		return nil, nil
	}
	p := rows[0]
	hash := sha256.Sum256([]byte(p.Body))
	if p.Purpose != "non_commercial_test_fixture" || !videoIntentPolicyCode.MatchString(p.PolicyVersion) || p.Title == "" || !utf8.ValidString(p.Body) || len(p.Body) == 0 || len(p.Body) > 16384 || p.BodySHA256 != hex.EncodeToString(hash[:]) || p.AcceptanceTTLSeconds == 0 || !p.ExpiresAt.After(p.EffectiveAt) {
		return nil, ErrVideoRightsUnavailable
	}
	return &p, nil
}

func videoRightsPolicyEffective(policy *videoRightsPolicyRow, now time.Time) bool {
	return policy != nil && !policy.EffectiveAt.After(now) && policy.ExpiresAt.After(now)
}

// ProjectAcceptance不要求具体模型grant，但仍复验主体权限和Project归属；SK不能窥探另一Key所属Project。
func (s *VideoRightsService) ProjectAcceptance(ctx context.Context, caller VideoCaller) (*VideoRightsAcceptance, error) {
	if s == nil || s.db == nil {
		return nil, ErrVideoRightsUnavailable
	}
	var result *VideoRightsAcceptance
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner, err := s.access.ResolveSubjectTx(ctx, tx, caller, s.now().UTC())
		if err != nil {
			return err
		}
		policy, err := findVideoRightsPolicyTx(tx)
		if err != nil {
			return err
		}
		var row videoRightsAcceptanceRow
		err = tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("user_id=? AND project_id=?", owner.UserID, owner.ProjectID).Order("accepted_at DESC,id DESC").Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			result = videoRightsReceipt(owner.ProjectID, nil, policy, s.now().UTC(), false)
			return nil
		}
		if err != nil {
			return ErrVideoRightsUnavailable
		}
		result = videoRightsReceipt(owner.ProjectID, &row, policy, s.now().UTC(), false)
		return nil
	})
	return result, err
}

// Accept只允许所有者JWT明确提交；唯一键决定100并发赢家，重复命令读取原时间而非续期。
func (s *VideoRightsService) Accept(ctx context.Context, c VideoRightsAcceptCommand) (*VideoRightsAcceptance, error) {
	if s == nil || s.db == nil {
		return nil, ErrVideoRightsUnavailable
	}
	if c.Caller.APIKeyID != 0 {
		return nil, ErrVideoRightsOwnerJWTRequired
	}
	if !c.Confirmed || !videoIntentPolicyCode.MatchString(c.PolicyVersion) {
		return nil, ErrVideoRightsRequired
	}
	if !videoHTTPIdempotency.MatchString(c.IdempotencyKey) || !videoBillingPublicID.MatchString(c.RequestID) {
		return nil, ErrVideoGenerationIntent
	}
	keyHash := videoBillingDigest("rights_accept\x00" + c.IdempotencyKey)
	fingerprint := videoBillingDigest(fmt.Sprintf("rights_accept:v1\x00%d\x00%d\x00%s\x00true", c.Caller.UserID, c.Caller.ProjectID, c.PolicyVersion))
	var result *VideoRightsAcceptance
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner, err := s.access.ResolveSubjectTx(ctx, tx, c.Caller, s.now().UTC())
		if err != nil {
			return err
		}
		lookup := func(current bool) *gorm.DB {
			q := tx.Where("user_id=? AND project_id=? AND command_kind='rights_accept' AND idempotency_key_hash=?", owner.UserID, owner.ProjectID, keyHash)
			if current {
				q = q.Clauses(clause.Locking{Strength: "SHARE"})
			}
			return q
		}
		var row videoRightsAcceptanceRow
		err = lookup(false).Take(&row).Error
		existing := err == nil
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrVideoRightsUnavailable
		}
		if existing && row.RequestFingerprint != fingerprint {
			return ErrVideoRightsConflict
		}
		policy, err := findVideoRightsPolicyTx(tx)
		if err != nil {
			return err
		}
		if !existing {
			if !videoRightsPolicyEffective(policy, s.now().UTC()) {
				return ErrVideoRightsUnavailable
			}
			if c.PolicyVersion != policy.PolicyVersion {
				return ErrVideoRightsRequired
			}
			now := s.now().UTC().Truncate(time.Microsecond)
			expires := now.Add(time.Duration(policy.AcceptanceTTLSeconds) * time.Second)
			if expires.After(policy.ExpiresAt) {
				expires = policy.ExpiresAt
			}
			publicID, err := newVideoHTTPID("vrights_")
			if err != nil {
				return err
			}
			row = videoRightsAcceptanceRow{PublicID: publicID, UserID: owner.UserID, ProjectID: owner.ProjectID, AcceptedBy: owner.UserID, PolicyID: policy.ID, PolicyVersion: policy.PolicyVersion, PolicyBodySHA256: policy.BodySHA256, CommandKind: "rights_accept", IdempotencyKeyHash: keyHash, RequestFingerprint: fingerprint, RequestID: c.RequestID, AcceptedAt: now, ExpiresAt: expires}
			if err := tx.Create(&row).Error; err != nil {
				var duplicate *drivermysql.MySQLError
				if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
					return ErrVideoRightsUnavailable
				}
				// 重复插入后使用当前读，不能复用外层RR的“不存在”快照。
				row = videoRightsAcceptanceRow{}
				if err := lookup(true).Take(&row).Error; err != nil {
					return ErrVideoRightsUnavailable
				}
				if row.RequestFingerprint != fingerprint {
					return ErrVideoRightsConflict
				}
				existing = true
			}
		}
		// 等待唯一键/主体锁后重查期限，失效只影响valid，不改写历史接受事实。
		if err := s.access.AuthorizeSubjectTx(ctx, tx, owner, s.now().UTC()); err != nil {
			return err
		}
		policy, err = findVideoRightsPolicyTx(tx)
		if err != nil {
			return err
		}
		if !existing && (!videoRightsPolicyEffective(policy, s.now().UTC()) || !row.ExpiresAt.After(s.now().UTC())) {
			return ErrVideoRightsUnavailable
		}
		result = videoRightsReceipt(owner.ProjectID, &row, policy, s.now().UTC(), existing)
		return nil
	})
	return result, err
}

func videoRightsReceipt(projectID uint64, row *videoRightsAcceptanceRow, policy *videoRightsPolicyRow, now time.Time, existing bool) *VideoRightsAcceptance {
	result := &VideoRightsAcceptance{ProjectID: projectID, InvalidReason: "not_accepted", Idempotent: existing}
	if policy != nil {
		result.CurrentPolicyVersion = &policy.PolicyVersion
	}
	if row == nil {
		return result
	}
	result.AcceptanceID, result.AcceptedPolicyVersion, result.AcceptedAt, result.ExpiresAt = &row.PublicID, &row.PolicyVersion, &row.AcceptedAt, &row.ExpiresAt
	switch {
	case policy == nil:
		result.InvalidReason = "policy_unavailable"
	case !videoRightsPolicyEffective(policy, now):
		result.InvalidReason = "policy_expired_or_inactive"
	case row.PolicyID != policy.ID || row.PolicyVersion != policy.PolicyVersion || row.PolicyBodySHA256 != policy.BodySHA256:
		result.InvalidReason = "policy_changed"
	case !row.ExpiresAt.After(now):
		result.InvalidReason = "acceptance_expired"
	default:
		result.Valid, result.InvalidReason = true, ""
	}
	return result
}

// ResolveSubjectTx为权利、上传等无具体模型入口解析归属，复用相同基础准入而非另建鉴权链。
func (s *VideoAccessService) ResolveSubjectTx(ctx context.Context, tx *gorm.DB, caller VideoCaller, now time.Time) (repository.VideoOwner, error) {
	owner := repository.VideoOwner{UserID: caller.UserID, ProjectID: caller.ProjectID}
	if tx == nil || caller.UserID == 0 {
		return owner, ErrVideoBillingAccess
	}
	if caller.APIKeyID != 0 {
		var key struct{ ProjectID *uint64 }
		if err := tx.WithContext(ctx).Table("api_keys").Select("project_id").Where("id=? AND user_id=?", caller.APIKeyID, caller.UserID).Take(&key).Error; err != nil {
			return owner, videoAccessReadError(err, ErrVideoBillingAccess)
		}
		if key.ProjectID == nil || (caller.ProjectID != 0 && caller.ProjectID != *key.ProjectID) {
			return owner, ErrVideoBillingAccess
		}
		owner.ProjectID, owner.APIKeyID = *key.ProjectID, optionalUint64(caller.APIKeyID)
	}
	return owner, s.AuthorizeSubjectTx(ctx, tx, owner, now)
}
