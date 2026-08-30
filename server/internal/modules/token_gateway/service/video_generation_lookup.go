package service

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"strings"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 准备生成语义不触发报价、输入读取或钱包动作；两个门面与原子预占复用同一规范。
type videoReservationIntent struct {
	input       VideoQuoteFingerprintInput
	owner       repository.VideoOwner
	prompt      string
	fingerprint string
	keyHash     string
}

func (s *VideoBillingService) prepareVideoReservationIntent(c VideoReservationCommand) (*videoReservationIntent, error) {
	c.IdempotencyKey = strings.TrimSpace(c.IdempotencyKey)
	if c.IdempotencyKey == "" || len(c.IdempotencyKey) > 128 || strings.ContainsRune(c.IdempotencyKey, 0) || !videoBillingPublicID.MatchString(c.RequestID) || !videoBillingPublicID.MatchString(c.TaskID) || (c.QuoteCommandKind != VideoQuoteCommandKindExplicit && c.QuoteCommandKind != VideoQuoteCommandKindCreate) {
		return nil, ErrVideoGenerationIntent
	}
	input := c.FingerprintInput
	input.LogicalModelCode = strings.TrimSpace(input.LogicalModelCode)
	if input.Input != nil {
		copied := *input.Input
		copied.InputAssetID = strings.TrimSpace(copied.InputAssetID)
		input.Input = &copied
		if copied.InputAssetID == "" {
			return nil, ErrVideoGenerationIntent
		}
	}
	prompt, err := NormalizeVideoGenerationPrompt(c.Prompt)
	if err != nil {
		return nil, err
	}
	hash, err := VideoGenerationPromptHMAC(s.promptSecret, prompt)
	if err != nil || subtle.ConstantTimeCompare([]byte(hash), []byte(input.PromptHash)) != 1 {
		return nil, ErrVideoGenerationIntent
	}
	fingerprint, err := BuildVideoGenerationIntentFingerprint(s.intentSecret, VideoGenerationIntent{LogicalModelCode: input.LogicalModelCode, PromptHMAC: hash, RightsPolicyVersion: c.RightsPolicyVersion, Variant: input.Variant, Input: input.Input})
	if err != nil {
		return nil, err
	}
	return &videoReservationIntent{input: input, owner: repository.VideoOwner{UserID: input.UserID, ProjectID: input.ProjectID, APIKeyID: optionalUint64(input.APIKeyID)}, prompt: prompt, fingerprint: fingerprint, keyHash: videoBillingDigest("create_video\x00" + c.IdempotencyKey)}, nil
}

// LookupVideoGeneration 在报价前验证当前权限并查原生成意图；不读取媒体或重新绑定输入。
func (s *VideoBillingService) LookupVideoGeneration(ctx context.Context, r VideoFacadeRequest) (*VideoPreparedGeneration, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, ErrVideoBillingState
	}
	p, err := s.prepareVideoReservationIntent(VideoReservationCommand{Prompt: r.Prompt, RightsPolicyVersion: r.RightsPolicyVersion, IdempotencyKey: r.IdempotencyKey, RequestID: r.RequestID, TaskID: r.TaskID, FingerprintInput: r.FingerprintInput, QuoteCommandKind: VideoQuoteCommandKindCreate})
	if err != nil {
		return nil, false, err
	}
	return s.lookupVideoReservation(ctx, p)
}

func (s *VideoBillingService) lookupVideoReservation(ctx context.Context, p *videoReservationIntent) (*VideoPreparedGeneration, bool, error) {
	var result *VideoPreparedGeneration
	var found bool
	// 只读一致快照避免在多次查询之间拼出两个时刻的Task与Request；权限共享锁保持到查询结束。
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.authorizeVideo(ctx, tx, p.owner, p.input.LogicalModelCode, s.now().UTC()); err != nil {
			return err
		}
		var err error
		result, found, err = s.findVideoReservation(tx, p.owner, p.keyHash, p.fingerprint)
		if err != nil {
			return err
		}
		if found {
			if err := s.validateVideoReplayInput(tx, p, result); err != nil {
				return err
			}
		}
		return s.authorizeVideo(ctx, tx, p.owner, p.input.LogicalModelCode, s.now().UTC())
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, found, err
	}
	return result, found, nil
}

// 原输入可在安全终态后删除，重放只验证冻结绑定与保留的归属；别名则必须独立验证当前可用性。
func (s *VideoBillingService) validateVideoReplayInput(tx *gorm.DB, p *videoReservationIntent, result *VideoPreparedGeneration) error {
	bindings, err := repository.NewVideoTaskInputRepository(tx).ListForOwner(tx.Statement.Context, result.TaskID, p.owner)
	if err != nil {
		return err
	}
	if p.input.Input == nil {
		if len(bindings) != 0 {
			return ErrVideoBillingState
		}
		return nil
	}
	if len(bindings) != 1 || bindings[0].Role != model.AITaskInputReferenceImage || bindings[0].Ordinal != 0 || bindings[0].NormalizedSHA256 != p.input.Input.NormalizedSHA256 || bindings[0].InputVersion != p.input.Input.Version {
		return ErrVideoBillingState
	}
	var original model.AIGatewayInputAsset
	if err := tx.Where("id=? AND user_id=? AND project_id=?", bindings[0].InputAssetID, p.owner.UserID, p.owner.ProjectID).First(&original).Error; err != nil {
		return repository.ErrVideoInputNotFound
	}
	if original.PublicID == p.input.Input.InputAssetID {
		return nil
	}
	alias, err := repository.NewVideoInputAssetRepository(tx).FindReadyForBinding(tx.Statement.Context, p.input.Input.InputAssetID, p.owner, s.now().UTC())
	if err != nil {
		return err
	}
	if alias.NormalizedSHA256 == nil || *alias.NormalizedSHA256 != p.input.Input.NormalizedSHA256 || alias.VersionNo != p.input.Input.Version {
		return repository.ErrVideoInputSnapshotDrift
	}
	return nil
}
