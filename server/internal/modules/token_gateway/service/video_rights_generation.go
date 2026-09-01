package service

import (
	"context"
	"errors"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/repository"
)

// 私有证明只能由实际政策/接受读取构造；客户端不能指定来源、接受ID或已核验状态。
type videoRightsProof struct {
	owner                            repository.VideoOwner
	policyID                         uint64
	version, bodyHash, source, trace string
	policyExpires, confirmedAt       time.Time
	acceptanceID                     *uint64
}

type videoRightsDeclaration struct {
	ID                      uint64
	CommandKind             string
	QuoteID                 uint64
	RequestID               *string
	UserID, ProjectID       uint64
	APIKeyID                *uint64
	PolicyID                uint64
	PolicyVersion           string
	PolicyBodySHA256        string `gorm:"column:policy_body_sha256"`
	PolicyExpiresAt         time.Time
	AcceptanceID            *uint64
	Source                  string
	HTTPRequestID           string `gorm:"column:http_request_id"`
	ConfirmedAt, VerifiedAt time.Time
}

func (videoRightsDeclaration) TableName() string { return "ai_video_rights_declarations" }

func (s *VideoRightsService) prepareGeneration(ctx context.Context, owner repository.VideoOwner, c VideoCommand) (*videoRightsProof, error) {
	if s == nil || s.db == nil {
		return nil, ErrVideoRightsUnavailable
	}
	var proof *videoRightsProof
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := s.now().UTC()
		if err := s.access.AuthorizeSubjectTx(ctx, tx, owner, now); err != nil {
			return err
		}
		p, err := loadVideoRightsPolicyTx(tx, now)
		if err != nil {
			return err
		}
		proof = &videoRightsProof{owner: owner, policyID: p.ID, version: p.PolicyVersion, bodyHash: p.BodySHA256, policyExpires: p.ExpiresAt, trace: c.HTTPRequestID, confirmedAt: now.Truncate(time.Microsecond)}
		if proof.trace == "" {
			proof.trace, err = newVideoHTTPID("vrchk_")
			if err != nil {
				return err
			}
		}
		if !videoBillingPublicID.MatchString(proof.trace) {
			return ErrVideoGenerationIntent
		}
		if owner.APIKeyID == nil {
			if !c.RightsConfirmed || c.RightsPolicyVersion != p.PolicyVersion || c.Facade == "openai" {
				return ErrVideoRightsRequired
			}
			proof.source = "jwt_per_request"
			return nil
		}
		if c.Facade != "" && c.Facade != "platform" && c.Facade != "openai" {
			return ErrVideoGenerationIntent
		}
		if (c.Facade != "openai" && !c.RightsAttestation) || (c.RightsPolicyVersion != "" && c.RightsPolicyVersion != p.PolicyVersion) {
			return ErrVideoRightsRequired
		}
		var a videoRightsAcceptanceRow
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("user_id=? AND project_id=? AND policy_id=? AND policy_version=? AND policy_body_sha256=? AND expires_at>?", owner.UserID, owner.ProjectID, p.ID, p.PolicyVersion, p.BodySHA256, now).Order("accepted_at DESC,id DESC").Take(&a).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrVideoRightsRequired
			}
			return ErrVideoRightsUnavailable
		}
		proof.acceptanceID, proof.confirmedAt = &a.ID, a.AcceptedAt
		proof.source = "project_sk_attestation"
		if c.Facade == "openai" {
			proof.source = "project_sk_multipart"
		}
		return nil
	})
	return proof, err
}

// revalidateTx只作当前读，不发起另一事务或连接；政策升级、原接受过期均不能靠预检证明绕过。
func (s *VideoRightsService) revalidateTx(tx *gorm.DB, owner repository.VideoOwner, proof *videoRightsProof, now time.Time) error {
	if proof == nil || proof.owner.UserID != owner.UserID || proof.owner.ProjectID != owner.ProjectID || !sameVideoRightsKey(proof.owner.APIKeyID, owner.APIKeyID) {
		return ErrVideoRightsRequired
	}
	p, err := loadVideoRightsPolicyTx(tx, now)
	if err != nil {
		return err
	}
	if p.ID != proof.policyID || p.PolicyVersion != proof.version || p.BodySHA256 != proof.bodyHash || !p.ExpiresAt.Equal(proof.policyExpires) {
		return ErrVideoRightsRequired
	}
	if owner.APIKeyID == nil {
		if proof.source != "jwt_per_request" || proof.acceptanceID != nil {
			return ErrVideoRightsRequired
		}
		return nil
	}
	if proof.acceptanceID == nil || (proof.source != "project_sk_attestation" && proof.source != "project_sk_multipart") {
		return ErrVideoRightsRequired
	}
	var a videoRightsAcceptanceRow
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND project_id=?", *proof.acceptanceID, owner.UserID, owner.ProjectID).Take(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrVideoRightsRequired
		}
		return ErrVideoRightsUnavailable
	}
	if a.PolicyID != p.ID || a.PolicyVersion != p.PolicyVersion || a.PolicyBodySHA256 != p.BodySHA256 || !a.ExpiresAt.After(now) || !a.AcceptedAt.Equal(proof.confirmedAt) {
		return ErrVideoRightsRequired
	}
	return nil
}

func sameVideoRightsKey(a, b *uint64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func checkVideoRightsDeclarationTx(tx *gorm.DB, kind string, quoteID uint64, requestID string, proof *videoRightsProof) error {
	var row videoRightsDeclaration
	q := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("quote_id=? AND command_kind=?", quoteID, kind)
	if kind == "generation" {
		q = q.Where("request_id=?", requestID)
	}
	if err := q.Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrVideoRightsRequired
		}
		return ErrVideoRightsUnavailable
	}
	if proof == nil || row.UserID != proof.owner.UserID || row.ProjectID != proof.owner.ProjectID || !sameVideoRightsKey(row.APIKeyID, proof.owner.APIKeyID) || row.PolicyID != proof.policyID || row.PolicyVersion != proof.version || row.PolicyBodySHA256 != proof.bodyHash {
		return ErrVideoRightsRequired
	}
	return nil
}

// 声明和对应Quote/Request由调用方同事务提交；同一事实不改写来源、确认时刻或有效期。
func recordVideoRightsDeclarationTx(tx *gorm.DB, kind string, quoteID uint64, requestID string, proof *videoRightsProof, now time.Time) error {
	if proof == nil {
		return ErrVideoRightsRequired
	}
	var request *string
	if kind == "generation" {
		request = &requestID
	}
	row := videoRightsDeclaration{CommandKind: kind, QuoteID: quoteID, RequestID: request, UserID: proof.owner.UserID, ProjectID: proof.owner.ProjectID, APIKeyID: proof.owner.APIKeyID, PolicyID: proof.policyID, PolicyVersion: proof.version, PolicyBodySHA256: proof.bodyHash, PolicyExpiresAt: proof.policyExpires, AcceptanceID: proof.acceptanceID, Source: proof.source, HTTPRequestID: proof.trace, ConfirmedAt: proof.confirmedAt, VerifiedAt: now.Truncate(time.Microsecond)}
	if err := tx.Create(&row).Error; err != nil {
		var duplicate *drivermysql.MySQLError
		if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
			return err
		}
		return checkVideoRightsDeclarationTx(tx, kind, quoteID, requestID, proof)
	}
	return nil
}

// T2V严格不访问权利表；G5历史协调器未启用G6准入时保留原合同，不补造声明。
func (s *VideoBillingService) revalidateGenerationRightsTx(tx *gorm.DB, p *videoReservationIntent, now time.Time) error {
	if p.input.Variant.Operation != "image_to_video" {
		return nil
	}
	// 只有完全未启用G6准入的历史协调器沿用G5合同；部分装配不能静默降级。
	if s.rights == nil && s.access == nil {
		return nil
	}
	if s.rights == nil || s.access == nil {
		return ErrVideoRightsUnavailable
	}
	return s.rights.revalidateTx(tx, p.owner, p.rights, now)
}
