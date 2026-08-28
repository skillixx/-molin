package service

import (
	"context"
	"errors"
	"strings"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/token_gateway/model"
)

var ErrVideoFacadeInvalid = errors.New("视频报价门面参数无效")

// VideoFacadeRequest 是两个协议门面进入共享视频报价器前的规范化命令。
type VideoFacadeRequest struct {
	IdempotencyKey   string
	RequestID        string
	TaskID           string
	FingerprintInput VideoQuoteFingerprintInput
}

// VideoExplicitQuoteResult 返回显式报价及其是否来自同一幂等事实。
type VideoExplicitQuoteResult struct {
	Quote    *model.AIGatewayQuote
	Existing bool
}

// VideoReservationCommand 把已选Quote和生成命令交给唯一原子预占边界。
type VideoReservationCommand struct {
	QuotePublicID    string
	QuoteCommandKind string
	IdempotencyKey   string
	RequestID        string
	TaskID           string
	FingerprintInput VideoQuoteFingerprintInput
}

// VideoPreparedGeneration 返回已预占的请求、任务和冻结金额，不包含Provider执行结果。
type VideoPreparedGeneration struct {
	Quote      *model.AIGatewayQuote
	RequestID  string
	TaskID     string
	HeldAmount decimal.Decimal
	Existing   bool
}

// VideoReservationCoordinator 的实现必须在一个事务内完成Quote消费、余额/额度检查、Hold与Task创建。
// VID-G2只调用这个原子边界；任何实现都不得在该边界之前触发Provider或消息队列。
type VideoReservationCoordinator interface {
	ReserveAndCreate(ctx context.Context, command VideoReservationCommand) (*VideoPreparedGeneration, error)
}

// VideoQuoteFacade 固定两个协议的差异：/api/token先显式Quote，/v1/videos在服务端自动Quote。
type VideoQuoteFacade struct {
	quotes      *VideoQuoteService
	reservation VideoReservationCoordinator
}

// NewVideoQuoteFacade 装配共享报价器与原子预占协调器。
func NewVideoQuoteFacade(quotes *VideoQuoteService, reservation VideoReservationCoordinator) *VideoQuoteFacade {
	return &VideoQuoteFacade{quotes: quotes, reservation: reservation}
}

// CreateTokenQuote 对应POST /api/token/videos/quotes，只形成价格快照，不预占钱包、不创建任务。
func (s *VideoQuoteFacade) CreateTokenQuote(ctx context.Context, request VideoFacadeRequest) (*VideoExplicitQuoteResult, error) {
	if s == nil || s.quotes == nil || strings.TrimSpace(request.IdempotencyKey) == "" {
		return nil, ErrVideoFacadeInvalid
	}
	quote, existing, err := s.quotes.CreateQuote(ctx, VideoCreateQuoteCommand{
		CommandKind: VideoQuoteCommandKindExplicit, IdempotencyKey: request.IdempotencyKey, FingerprintInput: request.FingerprintInput,
	})
	if err != nil {
		return nil, err
	}
	return &VideoExplicitQuoteResult{Quote: quote, Existing: existing}, nil
}

// GenerateWithTokenQuote 对应/api/token/videos/generations，消费控制台已展示的显式Quote。
func (s *VideoQuoteFacade) GenerateWithTokenQuote(ctx context.Context, request VideoFacadeRequest, quotePublicID string) (*VideoPreparedGeneration, error) {
	if s == nil || s.reservation == nil || strings.TrimSpace(quotePublicID) == "" || strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.TaskID) == "" {
		return nil, ErrVideoFacadeInvalid
	}
	return s.reservation.ReserveAndCreate(ctx, VideoReservationCommand{QuotePublicID: strings.TrimSpace(quotePublicID), QuoteCommandKind: VideoQuoteCommandKindExplicit, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), RequestID: strings.TrimSpace(request.RequestID), TaskID: strings.TrimSpace(request.TaskID), FingerprintInput: request.FingerprintInput})
}

// CreateOpenAIVideo 对应POST /v1/videos，在同一服务端编排中自动Quote后进入原子Hold与Task边界。
func (s *VideoQuoteFacade) CreateOpenAIVideo(ctx context.Context, request VideoFacadeRequest) (*VideoPreparedGeneration, error) {
	if s == nil || s.quotes == nil || s.reservation == nil || strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.TaskID) == "" {
		return nil, ErrVideoFacadeInvalid
	}
	quote, _, err := s.quotes.CreateQuote(ctx, VideoCreateQuoteCommand{
		CommandKind: VideoQuoteCommandKindCreate, IdempotencyKey: request.IdempotencyKey, FingerprintInput: request.FingerprintInput,
	})
	if err != nil {
		return nil, err
	}
	return s.reservation.ReserveAndCreate(ctx, VideoReservationCommand{QuotePublicID: quote.PublicID, QuoteCommandKind: VideoQuoteCommandKindCreate, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), RequestID: strings.TrimSpace(request.RequestID), TaskID: strings.TrimSpace(request.TaskID), FingerprintInput: request.FingerprintInput})
}
