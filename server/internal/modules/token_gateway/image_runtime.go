package token_gateway

import (
	"context"
	"gorm.io/gorm"

	billingservice "molin/server/internal/modules/billing/service"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

type ImageRuntime struct {
	API                 *service.ImageAPIService
	Billing             *service.ImageBillingService
	CleanupWorker       *service.ImageCleanupWorker
	ObjectCleanupWorker *service.ImageObjectCleanupWorker
	CompensationWorker  *service.ImageCompensationWorker
	TaskDispatcher      *service.ImageTaskDispatcher
	TaskWorker          *service.ImageTaskWorker
	Queue               *imagegateway.ImageTaskQueue
}

type ImageRuntimeDeps struct {
	DB                   *gorm.DB
	WalletHolds          *billingservice.WalletHoldService
	Provider             imagegateway.ImageProviderAdapter
	Metrics              *service.AIGatewayMetrics
	Moderation           imagegateway.ImageModerationAdapter
	Store                imagegateway.ObjectStore
	Queue                *imagegateway.ImageTaskQueue
	ImageResourceLimiter service.ImageResourceLimiter
	Secrets              service.ImageAPISecrets
	Visibility           interface {
		VisibleToUser(ctx context.Context, userID uint64, code string) (bool, error)
	}
}

// NewImageRuntime 统一装配图片深模块；调用方必须先完成配置、Secret、bucket和queue拓扑门禁。
func NewImageRuntime(deps ImageRuntimeDeps) (*ImageRuntime, error) {
	provider := deps.Provider
	if deps.Metrics != nil {
		observed, err := service.NewObservedImageAdapter(provider, deps.Metrics)
		if err != nil {
			return nil, err
		}
		provider = observed
	}
	processor, err := imagegateway.NewImageProcessor(imagegateway.ImageProcessingLimits{
		MaxSourceBytes: 32 << 20, MaxNormalizedBytes: 32 << 20, MaxPixels: 5308416,
		MaxWidth: 2304, MaxHeight: 2304, ExpectedAspectRatio: 1, AspectTolerance: 0.01, ThumbnailMaxEdge: 512,
	}, nil)
	if err != nil {
		return nil, err
	}
	objectCleanupRepository := repository.NewImageObjectCleanupRepository(deps.DB)
	gateway, err := imagegateway.NewImageGateway(provider, deps.Moderation, processor, deps.Store, objectCleanupRepository)
	if err != nil {
		return nil, err
	}
	pricing := service.NewImagePricingService(repository.NewG3PricingRepository(deps.DB))
	billing, err := service.NewImageBillingService(deps.DB, deps.WalletHolds, pricing, gateway, objectCleanupRepository)
	if err != nil {
		return nil, err
	}
	api, err := service.NewImageAPIService(deps.DB, billing, pricing, deps.Store, deps.Secrets)
	if err != nil {
		return nil, err
	}
	api.WithVisibilityChecker(deps.Visibility)
	api.WithResourceLimiter(deps.ImageResourceLimiter)
	dispatcher, err := service.NewImageTaskDispatcher(deps.Queue, billing, deps.ImageResourceLimiter)
	if err != nil {
		return nil, err
	}
	api.WithAsyncDispatcher(dispatcher)
	taskWorker, err := service.NewImageTaskWorker(deps.Queue, dispatcher)
	if err != nil {
		return nil, err
	}
	cleanup, err := service.NewImageCleanupWorker(deps.DB, deps.Store)
	if err != nil {
		return nil, err
	}
	objectCleanup, err := service.NewImageObjectCleanupWorker(objectCleanupRepository, deps.Store)
	if err != nil {
		return nil, err
	}
	return &ImageRuntime{
		API: api, Billing: billing, CleanupWorker: cleanup, ObjectCleanupWorker: objectCleanup,
		CompensationWorker: service.NewImageCompensationWorker(repository.NewImageCompensationRepository(deps.DB), billing),
		TaskDispatcher:     dispatcher, TaskWorker: taskWorker, Queue: deps.Queue,
	}, nil
}
