package service

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// VideoServerObjectLocationFactory与VideoObjectStore使用同一服务端确定性位置，不接收客户端bucket或key。
type VideoServerObjectLocationFactory struct{}

func (VideoServerObjectLocationFactory) NewVideoObjectLocation(_ context.Context, owner repository.VideoOwner, taskID, assetID, role string, _ uint32) (repository.VideoObjectLocation, error) {
	if owner.UserID == 0 || owner.ProjectID == 0 || !videoBillingPublicID.MatchString(taskID) || !videoBillingPublicID.MatchString(assetID) {
		return repository.VideoObjectLocation{}, repository.ErrVideoAssetNotFound
	}
	bucket := "ai-result"
	if role == model.AIImageAssetContent {
		bucket = "ai-upload-temp"
	}
	switch role {
	case model.AIImageAssetContent, "cover", "preview", "thumbnail", "moderation_copy", "derived":
	default:
		return repository.VideoObjectLocation{}, repository.ErrVideoAssetNotFound
	}
	return repository.VideoObjectLocation{Bucket: bucket, ObjectKey: taskID + "/" + assetID + "/" + role + ".bin"}, nil
}

// EnableVideoCapacityReservation把HTTP显式Quote与自动Quote同时切换到同一Redis准入协调器。
func (s *VideoHTTPService) EnableVideoCapacityReservation(recovery *repository.VideoCapacityRecoveryRepository, store *RedisVideoCapacityStore, key *VideoCapacityNonceKey) error {
	if s == nil || s.billing == nil || s.quotes == nil {
		return ErrVideoGovernanceUnavailable
	}
	coordinator, err := NewVideoCapacityReservationCoordinator(s.billing, recovery, store, key)
	if err != nil {
		return err
	}
	s.facade = NewVideoQuoteFacade(s.quotes, coordinator)
	return nil
}

type VideoRuntimeGatewayDependencies struct {
	DB       *gorm.DB
	App      *VideoHTTPService
	Recovery *repository.VideoCapacityRecoveryRepository
	Capacity *RedisVideoCapacityStore
	NonceKey *VideoCapacityNonceKey
	Provider video.VideoProviderAdapter
	Store    video.VideoObjectStore
}

// NewVideoRuntimeGatewayFactory统一构造T2V/I2V Worker网关，防止不同消费者绕过容量账本或使用平行资产定位。
func NewVideoRuntimeGatewayFactory(deps VideoRuntimeGatewayDependencies) (VideoGatewayFactory, error) {
	if deps.DB == nil || deps.App == nil || deps.Recovery == nil || deps.Capacity == nil || deps.NonceKey == nil || deps.Provider == nil || deps.Store == nil {
		return nil, ErrVideoGovernanceUnavailable
	}
	probe := video.NewVideoMediaProbe(video.VideoProbeLimits{
		MaxBytes: 512 << 20, MaxBoxBytes: 4 << 20, MaxDurationMillis: 60_000,
		MaxWidth: 4096, MaxHeight: 4096, MinFrameRate: 1, MaxFrameRate: 120,
		AllowedVideoCodecs: map[string]bool{"avc1": true}, AllowedAudioCodecs: map[string]bool{"mp4a": true},
		MaxProbeDuration: 15 * time.Second, MaxTopLevelBoxes: 32,
	})
	safety := video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), video.NewFakeVideoSampler(video.FakeVideoSampleSuccess))
	labeler := video.NewFakeVideoAILabeler(video.FakeVideoLabelSuccess, "fake-label-v1")
	return func(owner repository.VideoOwner) (*video.VideoGateway, error) {
		if owner.UserID == 0 || owner.ProjectID == 0 {
			return nil, ErrVideoGovernanceUnavailable
		}
		base := deps.App.NewTaskLedger(owner, VideoServerObjectLocationFactory{})
		ledger, err := NewVideoCapacityTaskLedger(base, deps.Recovery, deps.Capacity, deps.NonceKey)
		if err != nil {
			return nil, err
		}
		gateway := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: ledger, Provider: deps.Provider, Store: deps.Store, Probe: probe, Safety: safety, Labeler: labeler})
		if gateway == nil {
			return nil, errors.New("视频运行时网关装配失败")
		}
		return gateway, nil
	}, nil
}

var _ repository.VideoObjectLocationFactory = VideoServerObjectLocationFactory{}
