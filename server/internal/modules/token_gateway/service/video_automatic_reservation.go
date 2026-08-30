package service

import (
	"context"
	"database/sql"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/repository"
)

// CreateWithAutomaticQuote 把自动报价与预占置于同一授权事务，失败不遗留未经授权的Quote。
func (s *VideoBillingService) CreateWithAutomaticQuote(ctx context.Context, r VideoFacadeRequest, quotes *VideoQuoteService) (*VideoPreparedGeneration, error) {
	if s == nil || s.db == nil || quotes == nil {
		return nil, ErrVideoBillingState
	}
	c := VideoReservationCommand{Prompt: r.Prompt, RightsPolicyVersion: r.RightsPolicyVersion, IdempotencyKey: r.IdempotencyKey, RequestID: r.RequestID, TaskID: r.TaskID, FingerprintInput: r.FingerprintInput, QuoteCommandKind: VideoQuoteCommandKindCreate}
	p, err := s.prepareVideoReservationIntent(c)
	if err != nil {
		return nil, err
	}
	var result *VideoPreparedGeneration
	err = retryVideoBillingTransaction(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// 同Project的自动创建串行裁决；显式预占的权限共享锁也参与此围栏，避免别名Quote先冲突。
			var project struct{ ID uint64 }
			if err := tx.Table("ai_projects").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", p.owner.ProjectID, p.owner.UserID).First(&project).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrVideoBillingAccess
				}
				return err
			}
			local := *s
			local.db = tx
			old, found, err := local.lookupVideoReservation(ctx, p)
			if err != nil {
				return err
			}
			if found {
				result = old
				return nil
			}
			if err := s.injectVideoFault("automatic_quote"); err != nil {
				return err
			}
			if err := local.authorizeVideo(ctx, tx, p.owner, p.input.LogicalModelCode, s.now().UTC()); err != nil {
				return err
			}
			// 只复制装配，不修改共享报价器；SQL仓储必须使用同一事务，不能在回滚之外写一张Quote。
			localQuotes := *quotes
			localQuotes.store = repository.NewVideoQuoteRepository(tx)
			quote, _, err := localQuotes.CreateQuote(ctx, VideoCreateQuoteCommand{CommandKind: VideoQuoteCommandKindCreate, IdempotencyKey: r.IdempotencyKey, FingerprintInput: p.input})
			if err != nil {
				return err
			}
			c.QuotePublicID = quote.PublicID
			result, err = local.ReserveAndCreate(ctx, c)
			return err
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
