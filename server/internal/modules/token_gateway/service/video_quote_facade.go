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
	Rights              *videoRightsProof `json:"-"`
	Prompt              string            `json:"-"`
	RightsPolicyVersion string            `json:"-"`
	IdempotencyKey      string
	RequestID           string
	TaskID              string
	FingerprintInput    VideoQuoteFingerprintInput
}

// VideoExplicitQuoteResult 返回显式报价及其是否来自同一幂等事实。
type VideoExplicitQuoteResult struct {
	Quote    *model.AIGatewayQuote
	Existing bool
}

// VideoReservationCommand 把已选Quote和生成命令交给唯一原子预占边界。
type VideoReservationCommand struct {
	Rights              *videoRightsProof `json:"-"`
	Prompt              string            `json:"-"`
	RightsPolicyVersion string            `json:"-"`
	QuotePublicID       string
	QuoteCommandKind    string
	IdempotencyKey      string
	RequestID           string
	TaskID              string
	FingerprintInput    VideoQuoteFingerprintInput
}

// VideoPreparedGeneration 返回已预占的请求、任务和冻结金额，不包含Provider执行结果。
type VideoPreparedGeneration struct {
	Quote           *model.AIGatewayQuote
	RequestID       string
	TaskID          string
	HeldAmount      decimal.Decimal
	Existing        bool
	ExecutionStatus string
	BillingStatus   string
	DeliveryStatus  string
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
	return s.reservation.ReserveAndCreate(ctx, VideoReservationCommand{Rights: request.Rights, Prompt: request.Prompt, RightsPolicyVersion: request.RightsPolicyVersion, QuotePublicID: strings.TrimSpace(quotePublicID), QuoteCommandKind: VideoQuoteCommandKindExplicit, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), RequestID: strings.TrimSpace(request.RequestID), TaskID: strings.TrimSpace(request.TaskID), FingerprintInput: request.FingerprintInput})
}

// CreateOpenAIVideo 对应POST /v1/videos，在同一服务端编排中自动Quote后进入原子Hold与Task边界。
func (s *VideoQuoteFacade) CreateOpenAIVideo(ctx context.Context, request VideoFacadeRequest) (*VideoPreparedGeneration, error) {
	if s == nil || s.quotes == nil || s.reservation == nil || strings.TrimSpace(request.IdempotencyKey) == "" || strings.TrimSpace(request.RequestID) == "" || strings.TrimSpace(request.TaskID) == "" {
		return nil, ErrVideoFacadeInvalid
	}
	// G5由财务协调器持有生成幂等与报价事务；G2测试/旧协调器仍保持原接口契约。
	if coordinator, ok := s.reservation.(interface {
		CreateWithAutomaticQuote(context.Context, VideoFacadeRequest, *VideoQuoteService) (*VideoPreparedGeneration, error)
	}); ok {
		return coordinator.CreateWithAutomaticQuote(ctx, request, s.quotes)
	}
	// 包装器若丢失自动协调能力必须失败关闭，不能静默退回报价先于鉴权的旧路径。
	return nil, ErrVideoFacadeInvalid
}

// 旧阶段协调器显式选择旧报价合同；G5不调用此路径，也不把它作为接口能力缺失的默认行为。
func (s *VideoReservationService) CreateWithAutomaticQuote(ctx context.Context, request VideoFacadeRequest, quotes *VideoQuoteService) (*VideoPreparedGeneration, error) {
	return createLegacyAutomaticVideo(ctx, request, quotes, s)
}

func createLegacyAutomaticVideo(ctx context.Context, request VideoFacadeRequest, quotes *VideoQuoteService, reservation VideoReservationCoordinator) (*VideoPreparedGeneration, error) {
	if quotes == nil || reservation == nil {
		return nil, ErrVideoFacadeInvalid
	}
	quote, _, err := quotes.CreateQuote(ctx, VideoCreateQuoteCommand{
		CommandKind: VideoQuoteCommandKindCreate, IdempotencyKey: request.IdempotencyKey, FingerprintInput: request.FingerprintInput,
	})
	if err != nil {
		return nil, err
	}
	return reservation.ReserveAndCreate(ctx, VideoReservationCommand{Prompt: request.Prompt, RightsPolicyVersion: request.RightsPolicyVersion, QuotePublicID: quote.PublicID, QuoteCommandKind: VideoQuoteCommandKindCreate, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), RequestID: strings.TrimSpace(request.RequestID), TaskID: strings.TrimSpace(request.TaskID), FingerprintInput: request.FingerprintInput})
}
