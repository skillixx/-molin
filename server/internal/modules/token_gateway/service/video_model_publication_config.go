package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

const videoRunwarePublicationContract = "RUNWARE_RUNWAY_GEN4_5_TASKUUID_5S"

// 显式Fake指针限制本阶段装配范围；发布器只读取名称，不调用Submit/Query或任何媒体能力。
type VideoModelPublishOptions struct {
	Provider      *video.FakeAsyncVideoAdapter
	ConfigVersion string
	Models        map[string]string
}

func copyVideoModelPublishOptions(in VideoModelPublishOptions) (*VideoModelPublishOptions, error) {
	if in.Provider == nil || in.Provider.Name() != "fake-native-async" || !videoAdminModelCode.MatchString(in.ConfigVersion) || len(in.ConfigVersion) > 64 || len(in.Models) == 0 {
		return nil, ErrVideoAccessUnavailable
	}
	out := in
	out.Models = make(map[string]string, len(in.Models))
	for code, providerModel := range in.Models {
		if !videoAdminModelCode.MatchString(code) || len(code) > 128 || providerModel != "runway:1@2" {
			return nil, ErrVideoAccessUnavailable
		}
		out.Models[code] = providerModel
	}
	return &out, nil
}

type videoModelExecutionBinding struct {
	SchemaVersion       int               `json:"schema_version"`
	Purpose             string            `json:"purpose"`
	ExecutionDriver     string            `json:"execution_driver"`
	ProviderContract    string            `json:"provider_contract"`
	ProviderModel       string            `json:"provider_model"`
	ConfigVersion       string            `json:"config_version"`
	PriceVersionID      uint64            `json:"price_version_id"`
	PriceSnapshotHashes map[string]string `json:"price_snapshot_hashes"`
}

type videoModelPublicationPrices struct {
	tx      *gorm.DB
	version *model.AIPriceVersion
	failure error
}

// 使用原价格事实和计算器，但查询保持当前锁定读，不让长事务沿用旧的RR快照。
func (p *videoModelPublicationPrices) FindActiveVersion(ctx context.Context, code string, at time.Time) (*model.AIPriceVersion, []model.AIPriceSKU, error) {
	var versions []model.AIPriceVersion
	if err := p.tx.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("logical_model_code=? AND status='active' AND effective_at<=? AND (expires_at IS NULL OR expires_at>?)", code, at, at).Order("id").Limit(2).Find(&versions).Error; err != nil {
		p.failure = err
		return nil, nil, err
	}
	if len(versions) != 1 {
		return nil, nil, ErrVideoPriceUnavailable
	}
	var skus []model.AIPriceSKU
	if err := p.tx.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("price_version_id=?", versions[0].ID).Order("id").Find(&skus).Error; err != nil {
		p.failure = err
		return nil, nil, err
	}
	p.version = &versions[0]
	return p.version, skus, nil
}

func (s *VideoAdminService) modelPublicationSnapshot(ctx context.Context, tx *gorm.DB, m model.TokenModel) (json.RawMessage, *model.AIPriceVersion, error) {
	if s.modelPublishing == nil || s.modelPublishing.Provider == nil || s.modelPublishing.Models[m.LogicalModelCode] != "runway:1@2" || s.app.contentStore == nil {
		return nil, nil, ErrVideoAccessUnavailable
	}
	contract, err := ParseVideoModelContract(m.VideoContractJSON, m.ProductID)
	if err != nil {
		return nil, nil, err
	}
	d, redacted, err := modelDraftReadDefinition(m)
	if err != nil || len(redacted) != 0 {
		return nil, nil, ErrVideoAccessUnavailable
	}
	validator := *s
	options := *s.modelDrafts
	visibility := &videoSQLVisibility{db: tx.Clauses(clause.Locking{Strength: "SHARE"})}
	options.Groups, options.Roles = visibility, visibility
	validator.modelDrafts = &options
	if _, _, err := validator.normalizeModelDraft(ctx, d); err != nil {
		return nil, nil, err
	}
	if m.DocsURL == nil || m.QuickStartURL == nil || m.DocsURLHealthStatus != "healthy" || m.QuickStartURLHealthStatus != "healthy" {
		return nil, nil, ErrVideoAdminCommandConflict
	}
	prices := &videoModelPublicationPrices{tx: tx}
	pricing := NewVideoPricingService(prices)
	binding := videoModelExecutionBinding{SchemaVersion: 1, Purpose: VideoPricePurposeNonCommercialFixture, ExecutionDriver: s.modelPublishing.Provider.Name(), ProviderContract: videoRunwarePublicationContract, ProviderModel: "runway:1@2", ConfigVersion: s.modelPublishing.ConfigVersion, PriceSnapshotHashes: map[string]string{}}
	for _, operation := range contract.SupportedOperations {
		if operation == model.AIVideoOperationImageToVideo && s.app.billing.referenceLoader == nil {
			return nil, nil, ErrVideoAccessUnavailable
		}
		quote, err := pricing.QuoteVideo(ctx, VideoQuoteCommand{LogicalModelCode: m.LogicalModelCode, Variant: VideoPriceVariant{Operation: operation, Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24}})
		if err != nil {
			if prices.failure != nil {
				return nil, nil, errors.Join(ErrVideoAccessUnavailable, prices.failure)
			}
			return nil, nil, err
		}
		if binding.PriceVersionID != 0 && binding.PriceVersionID != quote.Snapshot.PriceVersionID {
			return nil, nil, ErrVideoPriceUnavailable
		}
		binding.PriceVersionID = quote.Snapshot.PriceVersionID
		binding.PriceSnapshotHashes[operation] = videoPayloadSHA256(quote.SnapshotJSON)
	}
	// 视频快照明确使用native映射，不把历史Bifrost渠道字段当作执行依据。
	m.ChannelID, m.UpstreamModel = nil, nil
	raw, err := m.MarshalReleaseSnapshot()
	if err != nil {
		return nil, nil, err
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, nil, ErrVideoAccessUnavailable
	}
	fields["video_execution"], err = json.Marshal(binding)
	if err != nil {
		return nil, nil, err
	}
	raw, err = json.Marshal(fields)
	return raw, prices.version, err
}
