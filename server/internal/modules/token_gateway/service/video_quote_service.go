package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

const (
	VideoQuoteCommandKindExplicit = "quote"
	VideoQuoteCommandKindCreate   = "create_video"
	videoQuoteTTL                 = 5 * time.Minute
)

var (
	ErrVideoInputMismatch = errors.New("视频输入与operation不匹配")
	ErrVideoQuoteSecret   = errors.New("视频报价指纹密钥无效")
	ErrVideoQuoteConflict = errors.New("视频报价请求指纹冲突")
	ErrVideoQuoteExpired  = errors.New("视频报价已过期")
	ErrVideoQuoteConsumed = errors.New("视频报价已被其他请求消费")
	ErrVideoQuoteNotFound = errors.New("视频报价不存在")
)

// VideoQuoteInputBinding 冻结图生视频引用资产的公开标识、规范化内容摘要和不可变版本。
type VideoQuoteInputBinding struct {
	InternalID       uint64 `json:"-"`
	InputAssetID     string `json:"input_asset_id"`
	NormalizedSHA256 string `json:"normalized_sha256"`
	Version          uint64 `json:"version"`
}

// VideoQuoteFingerprintInput 汇总必须进入HMAC的owner、模型、Prompt摘要、variant和输入快照。
type VideoQuoteFingerprintInput struct {
	UserID           uint64
	ProjectID        uint64
	APIKeyID         uint64
	LogicalModelCode string
	PromptHash       string
	Variant          VideoPriceVariant
	Input            *VideoQuoteInputBinding
}

// VideoCreateQuoteCommand 通过命令类型与幂等键区分显式报价和自动创建视频。
type VideoCreateQuoteCommand struct {
	CommandKind      string
	IdempotencyKey   string
	FingerprintInput VideoQuoteFingerprintInput
}

type videoQuoteStore interface {
	FindIdempotent(ctx context.Context, userID, projectID uint64, commandKind, idempotencyKey, fingerprint string) (*model.AIGatewayQuote, bool, error)
	CreateIdempotent(ctx context.Context, quote *model.AIGatewayQuote) (*model.AIGatewayQuote, bool, error)
	Consume(ctx context.Context, publicID string, userID, projectID uint64, apiKeyID *uint64, operation, fingerprint, requestID string, now time.Time) (*model.AIGatewayQuote, bool, error)
}

// VideoInputSnapshotResolver 必须从可信持久层读取当前ready输入快照，禁止相信客户端提交的SHA或版本。
type VideoInputSnapshotResolver interface {
	ResolveReadyInput(ctx context.Context, userID, projectID uint64, inputAssetID string) (*VideoQuoteInputBinding, error)
}

// VideoQuoteService 编排可信输入、请求HMAC、价格快照与Quote持久化，不触发钱包或Provider。
type VideoQuoteService struct {
	pricing     *VideoPricingService
	store       videoQuoteStore
	fingerprint []byte
	now         func() time.Time
	newPublicID func() (string, error)
	inputs      VideoInputSnapshotResolver
}

// WithInputSnapshotResolver 注入图生视频可信输入读取器；未注入时图生报价失败关闭。
func (s *VideoQuoteService) WithInputSnapshotResolver(resolver VideoInputSnapshotResolver) *VideoQuoteService {
	s.inputs = resolver
	return s
}

// NewVideoQuoteService 复制专用HMAC密钥，避免调用方随后修改原切片。
func NewVideoQuoteService(pricing *VideoPricingService, store videoQuoteStore, fingerprintSecret []byte) *VideoQuoteService {
	return &VideoQuoteService{pricing: pricing, store: store, fingerprint: append([]byte(nil), fingerprintSecret...), now: time.Now, newPublicID: newVideoQuotePublicID}
}

// BuildVideoQuoteFingerprint 以HMAC绑定定价意图；图生视频额外绑定输入资产ID、规范化SHA-256与版本。
func BuildVideoQuoteFingerprint(secret []byte, input VideoQuoteFingerprintInput) (string, error) {
	if len(secret) < 32 || input.UserID == 0 || input.ProjectID == 0 || strings.TrimSpace(input.LogicalModelCode) == "" || !lowerHex64.MatchString(input.PromptHash) {
		return "", ErrVideoQuoteSecret
	}
	_, variantHash, err := CanonicalVideoPriceVariant(input.Variant)
	if err != nil {
		return "", err
	}
	var binding *VideoQuoteInputBinding
	switch input.Variant.Operation {
	case model.AIVideoOperationTextToVideo:
		if input.Input != nil {
			return "", ErrVideoInputMismatch
		}
	case model.AIVideoOperationImageToVideo:
		if input.Input == nil || strings.TrimSpace(input.Input.InputAssetID) == "" || !lowerHex64.MatchString(input.Input.NormalizedSHA256) || input.Input.Version == 0 {
			return "", ErrVideoInputMismatch
		}
		copyBinding := *input.Input
		copyBinding.InputAssetID = strings.TrimSpace(copyBinding.InputAssetID)
		binding = &copyBinding
	default:
		return "", ErrVideoInputMismatch
	}
	payload := struct {
		Capability       string                  `json:"capability"`
		Operation        string                  `json:"operation"`
		UserID           uint64                  `json:"user_id"`
		ProjectID        uint64                  `json:"project_id"`
		APIKeyID         uint64                  `json:"api_key_id"`
		LogicalModelCode string                  `json:"logical_model_code"`
		PromptHash       string                  `json:"prompt_hash"`
		VariantHash      string                  `json:"variant_hash"`
		Input            *VideoQuoteInputBinding `json:"input,omitempty"`
	}{
		Capability: model.AIVideoCapability, Operation: input.Variant.Operation, UserID: input.UserID, ProjectID: input.ProjectID, APIKeyID: input.APIKeyID,
		LogicalModelCode: strings.TrimSpace(input.LogicalModelCode), PromptHash: input.PromptHash, VariantHash: variantHash, Input: binding,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// CreateQuote 让显式Quote和OpenAI自动Quote共用同一报价器，只以command_kind隔离幂等命令空间。
func (s *VideoQuoteService) CreateQuote(ctx context.Context, command VideoCreateQuoteCommand) (*model.AIGatewayQuote, bool, error) {
	if s == nil || s.pricing == nil || s.store == nil || len(s.fingerprint) < 32 {
		return nil, false, ErrVideoPriceUnavailable
	}
	commandKind := strings.TrimSpace(command.CommandKind)
	idempotencyKey := strings.TrimSpace(command.IdempotencyKey)
	if (commandKind != VideoQuoteCommandKindExplicit && commandKind != VideoQuoteCommandKindCreate) || idempotencyKey == "" || len(idempotencyKey) > 128 {
		return nil, false, ErrVideoQuoteConflict
	}
	trustedInput, err := s.resolveTrustedFingerprintInput(ctx, command.FingerprintInput)
	if err != nil {
		return nil, false, err
	}
	fingerprint, err := BuildVideoQuoteFingerprint(s.fingerprint, trustedInput)
	if err != nil {
		return nil, false, err
	}
	// 幂等重放必须先返回已冻结Quote；后续调价、停价或成本过期都不能污染旧事实。
	if existing, found, findErr := s.store.FindIdempotent(ctx, trustedInput.UserID, trustedInput.ProjectID, commandKind, idempotencyKey, fingerprint); findErr != nil {
		if errors.Is(findErr, repository.ErrVideoQuoteConflict) {
			return nil, false, ErrVideoQuoteConflict
		}
		return nil, false, findErr
	} else if found {
		return existing, true, nil
	}
	priceQuote, err := s.pricing.QuoteVideo(ctx, VideoQuoteCommand{LogicalModelCode: trustedInput.LogicalModelCode, Variant: trustedInput.Variant})
	if err != nil {
		return nil, false, err
	}
	publicID, err := s.newPublicID()
	if err != nil {
		return nil, false, err
	}
	now := s.now().UTC()
	operation := trustedInput.Variant.Operation
	quote := &model.AIGatewayQuote{
		PublicID: publicID, UserID: trustedInput.UserID, ProjectID: trustedInput.ProjectID,
		APIKeyID:         optionalUint64(trustedInput.APIKeyID),
		LogicalModelCode: strings.TrimSpace(trustedInput.LogicalModelCode), Capability: model.AIVideoCapability,
		Operation: &operation, CommandKind: &commandKind, IdempotencyKey: &idempotencyKey,
		RequestFingerprint: fingerprint, RequestVariantHash: priceQuote.VariantHash, PriceVersionID: priceQuote.Snapshot.PriceVersionID,
		PriceSnapshotJSON: priceQuote.SnapshotJSON, QuotedAmount: priceQuote.QuotedAmount, Currency: "CNY",
		ExpiresAt: now.Add(videoQuoteTTL), CreatedAt: now,
	}
	created, existing, err := s.store.CreateIdempotent(ctx, quote)
	if errors.Is(err, repository.ErrVideoQuoteConflict) {
		return nil, false, ErrVideoQuoteConflict
	}
	if errors.Is(err, repository.ErrVideoQuoteNotFound) {
		return nil, false, ErrVideoQuoteNotFound
	}
	return created, existing, err
}

// ConsumeQuote 原子消费一次Quote；相同request_id重放返回原事实，其他请求稳定失败。
func (s *VideoQuoteService) ConsumeQuote(ctx context.Context, publicID string, input VideoQuoteFingerprintInput, requestID string) (*model.AIGatewayQuote, bool, error) {
	if s == nil || s.store == nil || strings.TrimSpace(publicID) == "" || strings.TrimSpace(requestID) == "" {
		return nil, false, ErrVideoQuoteConflict
	}
	trustedInput, err := s.resolveTrustedFingerprintInput(ctx, input)
	if err != nil {
		return nil, false, err
	}
	fingerprint, err := BuildVideoQuoteFingerprint(s.fingerprint, trustedInput)
	if err != nil {
		return nil, false, err
	}
	quote, replay, err := s.store.Consume(ctx, strings.TrimSpace(publicID), trustedInput.UserID, trustedInput.ProjectID, optionalUint64(trustedInput.APIKeyID), trustedInput.Variant.Operation, fingerprint, strings.TrimSpace(requestID), s.now().UTC())
	if err == nil {
		return quote, replay, nil
	}
	switch {
	case errors.Is(err, repository.ErrVideoQuoteNotFound):
		return nil, false, ErrVideoQuoteNotFound
	case errors.Is(err, repository.ErrVideoQuoteConflict):
		return nil, false, ErrVideoQuoteConflict
	case errors.Is(err, repository.ErrVideoQuoteExpired):
		return nil, false, ErrVideoQuoteExpired
	case errors.Is(err, repository.ErrVideoQuoteConsumed):
		return nil, false, ErrVideoQuoteConsumed
	default:
		return nil, false, err
	}
}

func (s *VideoQuoteService) resolveTrustedFingerprintInput(ctx context.Context, input VideoQuoteFingerprintInput) (VideoQuoteFingerprintInput, error) {
	if input.Variant.Operation == model.AIVideoOperationTextToVideo {
		if input.Input != nil {
			return VideoQuoteFingerprintInput{}, ErrVideoInputMismatch
		}
		return input, nil
	}
	if input.Variant.Operation != model.AIVideoOperationImageToVideo || input.Input == nil || s.inputs == nil {
		return VideoQuoteFingerprintInput{}, ErrVideoInputMismatch
	}
	trusted, err := s.inputs.ResolveReadyInput(ctx, input.UserID, input.ProjectID, strings.TrimSpace(input.Input.InputAssetID))
	if err != nil || trusted == nil || strings.TrimSpace(trusted.InputAssetID) != strings.TrimSpace(input.Input.InputAssetID) ||
		trusted.NormalizedSHA256 != input.Input.NormalizedSHA256 || trusted.Version != input.Input.Version {
		return VideoQuoteFingerprintInput{}, ErrVideoInputMismatch
	}
	resolved := input
	copyBinding := *trusted
	resolved.Input = &copyBinding
	return resolved, nil
}

func newVideoQuotePublicID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "vid_quote_" + hex.EncodeToString(raw), nil
}
