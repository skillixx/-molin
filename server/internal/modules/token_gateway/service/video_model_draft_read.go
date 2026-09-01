package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

type VideoModelDraftDetails struct {
	ModelID          uint64                    `json:"model_id"`
	VersionNo        uint64                    `json:"version_no"`
	ReleaseVersionNo uint64                    `json:"release_version_no"`
	Managed          bool                      `json:"managed"`
	SourceSHA256     *string                   `json:"source_sha256"`
	Definition       VideoModelDraftDefinition `json:"video_definition"`
	RedactedFields   []string                  `json:"redacted_fields"`
}

// 接管摘要绑定原模型ID、全部快照字段、状态、排序、发布指针与修改时刻；未配置合同也可以安全读取摘要。
func modelDraftAdoptionHash(m model.TokenModel) (string, error) {
	raw, err := m.MarshalDraftSnapshot()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal([]any{"video_model_adoption_v1", m.ID, json.RawMessage(raw), m.Status, m.SortOrder, m.ReleaseVersionNo, m.PublishedAt, m.CreatedAt, m.UpdatedAt, m.UpdatedBy})
	if err != nil {
		return "", err
	}
	return modelDraftResultHash(b)
}

func modelDraftReadDefinition(m model.TokenModel) (VideoModelDraftDefinition, []string, error) {
	d := VideoModelDraftDefinition{LogicalModelCode: m.LogicalModelCode, DisplayName: m.DisplayName, ProviderName: m.ProviderName, Description: m.Description, ProductID: m.ProductID, IntroURL: m.IntroURL, DocsURL: m.DocsURL, QuickStartURL: m.QuickStartURL, DocsURLHealthStatus: m.DocsURLHealthStatus, QuickStartURLHealthStatus: m.QuickStartURLHealthStatus, VisibleScope: m.VisibleScope, GroupIDs: []uint64{}, GroupRoles: []string{}, RoleCodes: []string{}}
	redacted := []string{}
	if len(m.VideoContractJSON) != 0 {
		contract, err := ParseVideoModelContract(m.VideoContractJSON, m.ProductID)
		if err == nil {
			d.VideoContract, _ = json.Marshal(contract)
		} else {
			redacted = append(redacted, "video_contract")
		}
	}
	audience, err := parseTargetAudience(m.TargetAudience)
	if err != nil {
		return d, nil, ErrVideoAccessUnavailable
	}
	d.GroupIDs = append(d.GroupIDs, audience.GroupIDs...)
	d.GroupRoles = append(d.GroupRoles, audience.GroupRoles...)
	d.RoleCodes = append(d.RoleCodes, audience.RoleCodes...)
	// 历史文档字段可能带凭据。只回传安全静态URL，原值仍由接管摘要绑定，不改变数据库事实。
	for _, entry := range []struct {
		name   string
		value  **string
		health *string
	}{{"intro_url", &d.IntroURL, nil}, {"docs_url", &d.DocsURL, &d.DocsURLHealthStatus}, {"quick_start_url", &d.QuickStartURL, &d.QuickStartURLHealthStatus}} {
		if *entry.value == nil {
			continue
		}
		u, e := url.Parse(**entry.value)
		if e != nil || !validDocumentationURL(**entry.value) || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			*entry.value = nil
			if entry.health != nil {
				*entry.health = "unpublished"
			}
			redacted = append(redacted, entry.name)
		}
	}
	return d, redacted, nil
}

func (s *VideoAdminService) GetModelDraft(ctx context.Context, caller VideoCaller, id uint64) (*VideoModelDraftDetails, error) {
	if !s.ModelDraftsReady() {
		return nil, ErrVideoAccessUnavailable
	}
	if id == 0 {
		return nil, ErrVideoAdminQuery
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var result *VideoModelDraftDetails
	err := retryVideoBillingTransaction(ctx, func() error {
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if e := s.authorizeTx(ctx, tx, caller, "ai_gateway:model_manage"); e != nil {
				return e
			}
			var m model.TokenModel
			if e := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND modality='video'", id).Take(&m).Error; e != nil {
				if errors.Is(e, gorm.ErrRecordNotFound) {
					return repository.ErrTokenModelNotFound
				}
				return errors.Join(ErrVideoAccessUnavailable, e)
			}
			var state videoModelDraftState
			stateErr := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("model_id=?", id).Take(&state).Error
			if stateErr != nil && !errors.Is(stateErr, gorm.ErrRecordNotFound) {
				return errors.Join(ErrVideoAccessUnavailable, stateErr)
			}
			managed := stateErr == nil
			var source *string
			if managed {
				hash, e := modelDraftSnapshotHash(m)
				if e != nil || hash != state.SnapshotSHA256 {
					return ErrVideoAdminCommandConflict
				}
			} else {
				hash, e := modelDraftAdoptionHash(m)
				if e != nil {
					return ErrVideoAccessUnavailable
				}
				source = &hash
				var history []struct{ ID uint64 }
				if e := tx.Model(&videoModelDraftRecord{}).Clauses(clause.Locking{Strength: "SHARE"}).Select("id").Where("model_id=?", id).Limit(1).Find(&history).Error; e != nil {
					return errors.Join(ErrVideoAccessUnavailable, e)
				}
				if len(history) > 0 {
					return ErrVideoAdminCommandConflict
				}
			}
			definition, redacted, e := modelDraftReadDefinition(m)
			if e != nil {
				return e
			}
			result = &VideoModelDraftDetails{ModelID: id, VersionNo: state.VersionNo, ReleaseVersionNo: m.ReleaseVersionNo, Managed: managed, SourceSHA256: source, Definition: definition, RedactedFields: redacted}
			return s.authorizeTx(ctx, tx, caller, "ai_gateway:model_manage")
		})
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
