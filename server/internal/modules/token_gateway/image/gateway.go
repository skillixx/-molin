package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"molin/server/internal/modules/token_gateway/model"
)

type GatewayOutcome string

const (
	GatewaySucceeded    GatewayOutcome = "succeeded"
	GatewayPartial      GatewayOutcome = "partial"
	GatewayFailed       GatewayOutcome = "failed"
	GatewayTimeout      GatewayOutcome = "timeout"
	GatewayDisconnected GatewayOutcome = "disconnected"
	GatewayUnknown      GatewayOutcome = "unknown"
)

var safeRequestID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

var ErrAssetStorageFailed = errors.New("图片资产存储失败")

type GenerateImageCommand struct {
	RequestID    string
	ModelCode    string
	Prompt       string `json:"-"`
	Count        uint64
	Resolution   string
	AspectRatio  string
	Quality      string
	OutputFormat string
}

type GatewayAsset struct {
	ResultIndex        uint64
	AssetRole          string
	Source             string
	IsBillableOutput   bool
	LifecycleState     string
	ModerationStatus   string
	ExplicitLabelState string
	ImplicitLabelState string
	MIMEType           string
	Width              int
	Height             int
	StoredObject       StoredObject `json:"-"`
}

type GatewayResult struct {
	Outcome             GatewayOutcome
	RequestedCount      uint64
	ProviderResultCount uint64
	ProviderCostUSD     string
	DeliverableCount    uint64
	RejectedCount       uint64
	FailedCount         uint64
	Assets              []GatewayAsset
	ErrorClass          string
}

// ImageGateway 把Provider、解码、临时存储、审核和产物归一化隐藏在一个深模块内；本阶段不触碰钱包或数据库状态。
type ImageGateway struct {
	provider   ImageProviderAdapter
	moderation ImageModerationAdapter
	processor  *ImageProcessor
	store      ObjectStore
	cleanup    ObjectCleanupRecorder
	now        func() time.Time
}

func NewImageGateway(provider ImageProviderAdapter, moderation ImageModerationAdapter, processor *ImageProcessor, store ObjectStore, cleanup ObjectCleanupRecorder) (*ImageGateway, error) {
	if provider == nil || moderation == nil || processor == nil || store == nil || cleanup == nil {
		return nil, ErrImageResultInvalid
	}
	return &ImageGateway{provider: provider, moderation: moderation, processor: processor, store: store, cleanup: cleanup, now: time.Now}, nil
}

func (g *ImageGateway) Generate(ctx context.Context, command GenerateImageCommand) (GatewayResult, error) {
	result := GatewayResult{RequestedCount: command.Count}
	if g == nil || !safeRequestID.MatchString(command.RequestID) || command.ModelCode == "" || command.Prompt == "" || command.Count == 0 {
		result.Outcome, result.ErrorClass = GatewayFailed, "invalid_request"
		return result, ErrImageResultInvalid
	}
	decision, err := g.moderation.ModeratePrompt(ctx, command.Prompt)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			result.Outcome, result.ErrorClass = GatewayDisconnected, "client_disconnected"
			return result, err
		}
		result.Outcome, result.ErrorClass = GatewayFailed, "moderation_unavailable"
		return result, ErrModerationFailed
	}
	if decision != ModerationAllowed {
		result.Outcome, result.ErrorClass = GatewayFailed, "content_policy_violation"
		return result, ErrModerationRejected
	}
	providerResult, err := g.provider.Generate(ctx, ProviderImageRequest{
		RequestID: command.RequestID, ModelCode: command.ModelCode, Prompt: command.Prompt, Count: command.Count,
		Resolution: command.Resolution, AspectRatio: command.AspectRatio, Quality: command.Quality, OutputFormat: command.OutputFormat,
	})
	if err != nil {
		return classifyProviderFailure(result, providerResult, err)
	}
	if providerResult.ResultUnknown {
		result.ProviderResultCount = uint64(len(providerResult.Images))
		result.ProviderCostUSD = providerResult.ProviderCostUSD
		result.Outcome, result.ErrorClass = GatewayUnknown, "result_unknown"
		return result, ErrProviderUnknown
	}
	if len(providerResult.Images) == 0 || uint64(len(providerResult.Images)) > command.Count {
		result.Outcome, result.ErrorClass = GatewayFailed, "provider_result_invalid"
		return result, ErrImageResultInvalid
	}
	result.ProviderResultCount = uint64(len(providerResult.Images))
	result.ProviderCostUSD = providerResult.ProviderCostUSD

	seen := make(map[uint64]struct{}, len(providerResult.Images))
	lastErrorClass := ""
	for _, providerImage := range providerResult.Images {
		if providerImage.Index >= command.Count {
			result.FailedCount++
			continue
		}
		if _, exists := seen[providerImage.Index]; exists {
			result.FailedCount++
			continue
		}
		seen[providerImage.Index] = struct{}{}
		assets, rejected, processErr := g.processOne(ctx, command, providerImage)
		if len(assets) > 0 {
			result.Assets = append(result.Assets, assets...)
			if rejected {
				result.RejectedCount++
			} else {
				result.DeliverableCount++
			}
		}
		if processErr != nil {
			if errors.Is(processErr, ErrObjectCleanupUnrecorded) {
				result.Outcome, result.ErrorClass = GatewayUnknown, "asset_cleanup_unrecorded"
				result.FailedCount = command.Count - result.DeliverableCount - result.RejectedCount
				return result, processErr
			}
			if errors.Is(processErr, ErrModerationFailed) {
				result.Outcome, result.ErrorClass = GatewayFailed, "moderation_unavailable"
				return result, ErrModerationFailed
			}
			if errors.Is(processErr, ErrAssetStorageFailed) {
				lastErrorClass = "asset_storage_failed"
			} else {
				lastErrorClass = "result_invalid"
			}
			result.FailedCount++
			continue
		}
	}
	missing := command.Count - uint64(len(seen))
	result.FailedCount += missing
	switch {
	case result.DeliverableCount == command.Count:
		result.Outcome = GatewaySucceeded
	case result.DeliverableCount > 0:
		result.Outcome = GatewayPartial
	default:
		result.Outcome = GatewayFailed
	}
	if result.Outcome == GatewayFailed {
		result.ErrorClass = lastErrorClass
		if result.ErrorClass == "" {
			result.ErrorClass = "no_deliverable_image"
		}
		return result, ErrImageResultInvalid
	}
	return result, nil
}

func (g *ImageGateway) processOne(ctx context.Context, command GenerateImageCommand, providerImage ProviderImage) ([]GatewayAsset, bool, error) {
	source := providerImageSource(providerImage)
	if source == "" {
		return nil, false, ErrImageResultInvalid
	}
	contentID := contentIdentifier(command.RequestID, providerImage.Index)
	normalized, err := g.processor.ProcessProviderImage(ctx, providerImage, contentID)
	if err != nil {
		return nil, false, err
	}
	namespace := requestNamespace(command.RequestID)
	tempRef := ObjectRef{Bucket: TemporaryObjectBucket, Key: fmt.Sprintf("%s/%d/primary.png", namespace, providerImage.Index)}
	if _, err := g.store.Put(ctx, tempRef, bytes.NewReader(normalized.Bytes), int64(normalized.SizeBytes)); err != nil {
		cleanupErr := g.cleanupObject(ctx, command.RequestID, tempRef, ObjectCleanupAfterTempPutUnknown)
		return nil, false, errors.Join(fmt.Errorf("%w: %v", ErrAssetStorageFailed, err), cleanupErr)
	}
	decision, moderationErr := g.moderation.ModerateImage(ctx, normalized)
	if moderationErr != nil {
		cleanupErr := g.cleanupObject(ctx, command.RequestID, tempRef, ObjectCleanupAfterModerationFailure)
		return nil, false, errors.Join(moderationErr, cleanupErr)
	}
	if decision != ModerationAllowed {
		quarantineRef := ObjectRef{Bucket: QuarantineObjectBucket, Key: fmt.Sprintf("%s/%d/primary.png", namespace, providerImage.Index)}
		stored, storeErr := g.store.Put(ctx, quarantineRef, bytes.NewReader(normalized.Bytes), int64(normalized.SizeBytes))
		if storeErr != nil {
			targetCleanupErr := g.cleanupObject(ctx, command.RequestID, quarantineRef, ObjectCleanupAfterQuarantinePutUnknown)
			tempCleanupErr := g.cleanupObject(ctx, command.RequestID, tempRef, ObjectCleanupAfterQuarantineStoreFailure)
			return nil, false, errors.Join(fmt.Errorf("%w: %v", ErrAssetStorageFailed, storeErr), targetCleanupErr, tempCleanupErr)
		}
		assets := []GatewayAsset{{
			ResultIndex: providerImage.Index, AssetRole: model.AIImageAssetPrimaryOutput, Source: source,
			LifecycleState: model.AIImageAssetQuarantined, ModerationStatus: model.AIModerationRejected,
			ExplicitLabelState: model.AIImageLabelApplied, ImplicitLabelState: model.AIImageLabelApplied,
			MIMEType: normalized.MIMEType, Width: normalized.Width, Height: normalized.Height,
			StoredObject: stored,
		}}
		cleanupErr := g.cleanupObject(ctx, command.RequestID, tempRef, ObjectCleanupAfterQuarantineStored)
		return assets, true, cleanupErr
	}
	resultRef := ObjectRef{Bucket: ResultObjectBucket, Key: fmt.Sprintf("%s/%d/primary.png", namespace, providerImage.Index)}
	stored, err := g.store.Put(ctx, resultRef, bytes.NewReader(normalized.Bytes), int64(normalized.SizeBytes))
	if err != nil {
		targetCleanupErr := g.cleanupObject(ctx, command.RequestID, resultRef, ObjectCleanupAfterResultPutUnknown)
		tempCleanupErr := g.cleanupObject(ctx, command.RequestID, tempRef, ObjectCleanupAfterResultStoreFailure)
		return nil, false, errors.Join(fmt.Errorf("%w: %v", ErrAssetStorageFailed, err), targetCleanupErr, tempCleanupErr)
	}
	assets := []GatewayAsset{{
		ResultIndex: providerImage.Index, AssetRole: model.AIImageAssetPrimaryOutput, Source: source, IsBillableOutput: true,
		LifecycleState: model.AIImageAssetTemporary, ModerationStatus: model.AIModerationPassed,
		ExplicitLabelState: model.AIImageLabelApplied, ImplicitLabelState: model.AIImageLabelApplied,
		MIMEType: normalized.MIMEType, Width: normalized.Width, Height: normalized.Height,
		StoredObject: stored,
	}}
	if cleanupErr := g.cleanupObject(ctx, command.RequestID, tempRef, ObjectCleanupAfterResultStored); cleanupErr != nil {
		// 主产物已经成功写入，必须把引用返回给持久层，避免错误处理再制造一个不可追踪对象。
		return assets, false, cleanupErr
	}
	thumbnail, err := g.processor.CreateThumbnail(ctx, normalized, contentID+"-thumbnail")
	if err != nil {
		return assets, false, nil
	}
	thumbnailRef := ObjectRef{Bucket: ResultObjectBucket, Key: fmt.Sprintf("%s/%d/thumbnail.png", namespace, providerImage.Index)}
	thumbnailStored, err := g.store.Put(ctx, thumbnailRef, bytes.NewReader(thumbnail.Bytes), int64(thumbnail.SizeBytes))
	if err != nil {
		cleanupErr := g.cleanupObject(ctx, command.RequestID, thumbnailRef, ObjectCleanupAfterThumbnailPutUnknown)
		return assets, false, cleanupErr
	}
	assets = append(assets, GatewayAsset{
		ResultIndex: providerImage.Index, AssetRole: model.AIImageAssetThumbnail, Source: "derived",
		LifecycleState: model.AIImageAssetTemporary, ModerationStatus: model.AIModerationPassed,
		ExplicitLabelState: model.AIImageLabelApplied, ImplicitLabelState: model.AIImageLabelApplied,
		MIMEType: thumbnail.MIMEType, Width: thumbnail.Width, Height: thumbnail.Height,
		StoredObject: thumbnailStored,
	})
	return assets, false, nil
}

const objectCleanupTimeout = 5 * time.Second

// cleanupObject 脱离已取消的请求上下文执行有限时回收；直接删除失败后必须先持久化补偿事实才可返回成功。
func (g *ImageGateway) cleanupObject(ctx context.Context, requestID string, ref ObjectRef, reason ObjectCleanupReason) error {
	deleteCtx, cancelDelete := context.WithTimeout(context.WithoutCancel(ctx), objectCleanupTimeout)
	deleteErr := g.store.Delete(deleteCtx, ref)
	cancelDelete()
	// Put返回错误时服务端仍可能在Delete之后迟到提交，因此无论即时Delete结果如何都必须留下延迟tombstone。
	forceTombstone := IsPutUnknownCleanupReason(reason)
	if !forceTombstone && (deleteErr == nil || errors.Is(deleteErr, ErrObjectNotFound)) {
		return nil
	}
	recordCtx, cancelRecord := context.WithTimeout(context.WithoutCancel(ctx), objectCleanupTimeout)
	recordErr := g.cleanup.RecordObjectCleanup(recordCtx, ObjectCleanupTask{RequestID: requestID, Ref: ref, Reason: reason})
	cancelRecord()
	if recordErr != nil {
		return ErrObjectCleanupUnrecorded
	}
	return nil
}

// IsPutUnknownCleanupReason 标识必须跨Provider迟到窗口持续保留tombstone的写入未知原因。
func IsPutUnknownCleanupReason(reason ObjectCleanupReason) bool {
	switch reason {
	case ObjectCleanupAfterTempPutUnknown,
		ObjectCleanupAfterQuarantinePutUnknown,
		ObjectCleanupAfterResultPutUnknown,
		ObjectCleanupAfterThumbnailPutUnknown:
		return true
	default:
		return false
	}
}

func providerImageSource(providerImage ProviderImage) string {
	switch {
	case providerImage.Base64 != "" && providerImage.URL == "":
		return "provider_base64"
	case providerImage.URL != "" && providerImage.Base64 == "":
		return "provider_url"
	default:
		return ""
	}
}

func classifyProviderFailure(result GatewayResult, providerResult ProviderImageResult, err error) (GatewayResult, error) {
	result.ProviderResultCount = uint64(len(providerResult.Images))
	result.ProviderCostUSD = providerResult.ProviderCostUSD
	switch {
	case errors.Is(err, ErrProviderTimeout):
		result.Outcome, result.ErrorClass = GatewayTimeout, "provider_timeout"
	case errors.Is(err, ErrProviderDisconnected):
		result.Outcome, result.ErrorClass = GatewayDisconnected, "provider_disconnected"
	case errors.Is(err, ErrProviderUnknown) || providerResult.ResultUnknown:
		result.Outcome, result.ErrorClass = GatewayUnknown, "result_unknown"
	default:
		result.Outcome, result.ErrorClass = GatewayFailed, "provider_failed"
	}
	return result, err
}

func requestNamespace(requestID string) string {
	sum := sha256.Sum256([]byte(requestID))
	return hex.EncodeToString(sum[:16])
}

func contentIdentifier(requestID string, index uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", requestID, index)))
	return hex.EncodeToString(sum[:])
}
