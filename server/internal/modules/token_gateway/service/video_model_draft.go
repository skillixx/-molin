package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	auditmodel "molin/server/internal/modules/audit/model"
	auditrepo "molin/server/internal/modules/audit/repository"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 显式注入只开放视频草稿；没有Provider、发布、价格或钱包写入能力。
type VideoModelDraftOptions struct {
	Groups GroupResolver
	Roles  RoleResolver
}

// video_definition整体替换工作副本；客户端必须重送完整七键合同，不把缺项补成授权。
type VideoModelDraftDefinition struct {
	LogicalModelCode          string          `json:"logical_model_code"`
	DisplayName               string          `json:"display_name"`
	ProviderName              string          `json:"provider_name"`
	Description               *string         `json:"description"`
	VideoContract             json.RawMessage `json:"video_contract"`
	ProductID                 *uint64         `json:"product_id"`
	IntroURL                  *string         `json:"intro_url"`
	DocsURL                   *string         `json:"docs_url"`
	QuickStartURL             *string         `json:"quick_start_url"`
	DocsURLHealthStatus       string          `json:"docs_url_health_status"`
	QuickStartURLHealthStatus string          `json:"quick_start_url_health_status"`
	VisibleScope              string          `json:"visible_scope"`
	GroupIDs                  []uint64        `json:"group_ids"`
	GroupRoles                []string        `json:"group_roles"`
	RoleCodes                 []string        `json:"role_codes"`
}

type VideoModelDraftCommand struct {
	Caller                 VideoCaller               `json:"-"`
	ModelID, VersionNo     uint64                    `json:"-"`
	IdempotencyKey, Reason string                    `json:"-"`
	Definition             VideoModelDraftDefinition `json:"-"`
	SourceSHA256           string                    `json:"-"`
}

type VideoModelDraftReply struct {
	ModelID          uint64                    `json:"model_id"`
	VersionNo        uint64                    `json:"version_no"`
	ReleaseVersionNo uint64                    `json:"release_version_no"`
	Definition       VideoModelDraftDefinition `json:"video_definition"`
	Idempotent       bool                      `json:"idempotent"`
}

type videoModelDraftState struct {
	ModelID        uint64 `gorm:"primaryKey"`
	VersionNo      uint64
	SnapshotSHA256 string `gorm:"column:snapshot_sha256"`
	UpdatedBy      uint64
	UpdatedAt      time.Time
}

func (videoModelDraftState) TableName() string { return "ai_video_model_draft_states" }

type videoModelDraftRecord struct {
	ID                                                  uint64 `gorm:"primaryKey"`
	PublicID, Action, CommandKeyHash                    string
	ActorUserID, ModelID, InitialVersion, ResultVersion uint64
	ModelCode                                           string
	InputSHA256                                         string          `gorm:"column:input_sha256"`
	SourceSHA256                                        *string         `gorm:"column:source_sha256"`
	ResultJSON                                          json.RawMessage `gorm:"column:result_json"`
	ResultSHA256                                        string          `gorm:"column:result_sha256"`
	VideoAdminReasonEnvelope                            `gorm:"embedded"`
	BeforeAuditID, AfterAuditID                         uint64
	CreatedAt                                           time.Time
}

func (videoModelDraftRecord) TableName() string { return "ai_video_model_draft_commands" }

func (s *VideoAdminService) ModelDraftsReady() bool { return s.WritesReady() && s.modelDrafts != nil }

func modelDraftReasonID(c videoModelDraftRecord) VideoAdminReasonIdentity {
	return VideoAdminReasonIdentity{ActorID: c.ActorUserID, ModelCode: c.ModelCode, ModelAction: c.Action, CommandKeyHash: c.CommandKeyHash, VersionNo: c.InitialVersion + 1}
}

func modelDraftSnapshotHash(m model.TokenModel) (string, error) {
	snapshot, err := m.MarshalReleaseSnapshot()
	if err != nil {
		return "", err
	}
	b, err := json.Marshal([]any{json.RawMessage(snapshot), m.Status, m.ReleaseVersionNo, m.PublishedAt})
	return videoPayloadSHA256(b), err
}

// MySQL会调整JSON空格与键顺序；摘要使用保留整数精度的规范JSON，不能直接哈希读回原字节。
func modelDraftResultHash(raw []byte) (string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	return videoPayloadSHA256(canonical), err
}

func (s *VideoAdminService) normalizeModelDraft(ctx context.Context, d VideoModelDraftDefinition) (VideoModelDraftDefinition, *model.TokenModel, error) {
	invalid := func() (VideoModelDraftDefinition, *model.TokenModel, error) {
		return d, nil, ErrVideoAdminCommandInvalid
	}
	if d.ProductID != nil && *d.ProductID == 0 {
		return invalid()
	}
	// 完整定义替换不能把遗漏的定向配置默认为all；显式空数组与明确范围都是合同的一部分。
	if d.GroupIDs == nil || d.GroupRoles == nil || d.RoleCodes == nil || len(d.GroupIDs) > 64 || len(d.GroupRoles) > 2 || len(d.RoleCodes) > 64 || (d.VisibleScope != scopeAll && d.VisibleScope != scopeGroups && d.VisibleScope != scopeRoles) || d.DocsURLHealthStatus == "" || d.QuickStartURLHealthStatus == "" {
		return invalid()
	}
	if (d.VisibleScope == scopeGroups && len(d.RoleCodes) > 0) || (d.VisibleScope == scopeRoles && (len(d.GroupIDs) > 0 || len(d.GroupRoles) > 0)) {
		return invalid()
	}
	if !videoAdminModelCode.MatchString(d.LogicalModelCode) || len(d.LogicalModelCode) > 128 {
		return invalid()
	}
	d.DisplayName = strings.TrimSpace(d.DisplayName)
	d.ProviderName = strings.TrimSpace(d.ProviderName)
	if d.DisplayName == "" || !utf8.ValidString(d.DisplayName) || len(d.DisplayName) > 191 || strings.IndexFunc(d.DisplayName, unicode.IsControl) >= 0 || !utf8.ValidString(d.ProviderName) || len(d.ProviderName) > 191 || strings.IndexFunc(d.ProviderName, unicode.IsControl) >= 0 {
		return invalid()
	}
	if d.Description != nil {
		v := strings.TrimSpace(*d.Description)
		if !utf8.ValidString(v) || len(v) > 4096 {
			return invalid()
		}
		d.Description = &v
	}
	contract, err := ParseVideoModelContract(d.VideoContract, d.ProductID)
	if err != nil {
		return invalid()
	}
	d.VideoContract, _ = json.Marshal(contract)
	// 文档是静态说明入口，不接受带凭据、签名参数或片段的媒体能力URL。
	for _, value := range []*string{d.IntroURL, d.DocsURL, d.QuickStartURL} {
		if value != nil {
			u, e := url.Parse(*value)
			if e != nil || !validDocumentationURL(*value) || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return invalid()
			}
		}
	}
	d.DocsURLHealthStatus = documentHealthStatus(d.DocsURL, d.DocsURLHealthStatus)
	d.QuickStartURLHealthStatus = documentHealthStatus(d.QuickStartURL, d.QuickStartURLHealthStatus)
	if validateDocumentHealth(d.DocsURL, d.DocsURLHealthStatus, "API文档") != nil || validateDocumentHealth(d.QuickStartURL, d.QuickStartURLHealthStatus, "快速入门") != nil {
		return invalid()
	}
	if (d.VisibleScope == "" || d.VisibleScope == scopeAll) && (len(d.GroupIDs) > 0 || len(d.GroupRoles) > 0 || len(d.RoleCodes) > 0) {
		return invalid()
	}
	scope, audience, err := buildVisibility(ctx, visibilityInput{Scope: d.VisibleScope, GroupIDs: d.GroupIDs, GroupRoles: d.GroupRoles, RoleCodes: d.RoleCodes}, s.modelDrafts.Groups, s.modelDrafts.Roles)
	if err != nil {
		return invalid()
	}
	d.VisibleScope = scope
	m := &model.TokenModel{LogicalModelCode: d.LogicalModelCode, DisplayName: d.DisplayName, ProviderName: d.ProviderName, Description: d.Description, Modality: "video", Status: "inactive", CapabilitiesJSON: json.RawMessage(`["video.generate"]`), VideoContractJSON: d.VideoContract, ProductID: d.ProductID, IntroURL: d.IntroURL, IntroURLHealthStatus: documentHealthStatus(d.IntroURL, "unknown"), DocsURL: d.DocsURL, DocsURLHealthStatus: d.DocsURLHealthStatus, QuickStartURL: d.QuickStartURL, QuickStartURLHealthStatus: d.QuickStartURLHealthStatus, VisibleScope: scope, TargetAudience: audience}
	return d, m, nil
}

func writeModelDraftAudit(ctx context.Context, tx *gorm.DB, c videoModelDraftRecord, before bool) (uint64, error) {
	action := "video_model_" + c.Action + "_after"
	if before {
		action = "video_model_" + c.Action + "_before"
	}
	raw, err := json.Marshal(modelDraftAuditFields(c))
	if err != nil {
		return 0, err
	}
	target, summary := "video_model", string(raw)
	a := auditmodel.AuditLog{OperatorID: &c.ActorUserID, Module: "token_gateway", Action: action, TargetType: &target, TargetID: &c.ModelCode, RequestSummary: &summary, CreatedAt: time.Now().UTC()}
	err = auditrepo.NewAuditLogRepository(tx).CreateWithTx(ctx, tx, &a)
	return a.ID, err
}

func modelDraftAuditFields(c videoModelDraftRecord) map[string]any {
	fields := map[string]any{"command_id": c.PublicID, "command_key_hash": c.CommandKeyHash, "initial_version": c.InitialVersion, "input_sha256": c.InputSHA256, "reason_hmac": c.ReasonHMAC}
	if c.SourceSHA256 != nil {
		fields["source_sha256"] = *c.SourceSHA256
	}
	return fields
}

func verifyModelDraftAudits(tx *gorm.DB, c videoModelDraftRecord) error {
	for _, before := range []bool{true, false} {
		id, action := c.AfterAuditID, "video_model_"+c.Action+"_after"
		if before {
			id, action = c.BeforeAuditID, "video_model_"+c.Action+"_before"
		}
		var a auditmodel.AuditLog
		if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=?", id).Take(&a).Error; err != nil {
			return errors.Join(ErrVideoAccessUnavailable, err)
		}
		want, _ := json.Marshal(modelDraftAuditFields(c))
		if a.OperatorID == nil || *a.OperatorID != c.ActorUserID || a.Module != "token_gateway" || a.Action != action || a.TargetType == nil || *a.TargetType != "video_model" || a.TargetID == nil || *a.TargetID != c.ModelCode || a.RequestSummary == nil {
			return ErrVideoAccessUnavailable
		}
		// 保留整数精度并计入额外键，不把大版本号转换为float64。
		gotHash, gotErr := modelDraftResultHash([]byte(*a.RequestSummary))
		wantHash, wantErr := modelDraftResultHash(want)
		if gotErr != nil || wantErr != nil || gotHash != wantHash {
			return ErrVideoAccessUnavailable
		}
	}
	return nil
}

// SaveModelDraft只变更原模型工作副本：不发布、不改价格、不创建授权和财务事实。
func (s *VideoAdminService) SaveModelDraft(ctx context.Context, c VideoModelDraftCommand) (*VideoModelDraftReply, error) {
	if !s.ModelDraftsReady() {
		return nil, ErrVideoAccessUnavailable
	}
	action := "create"
	if c.ModelID != 0 {
		action = "update"
	}
	adopting := c.ModelID != 0 && c.VersionNo == 0
	if (c.ModelID == 0 && c.VersionNo != 0) || (adopting && !lowerHex64.MatchString(c.SourceSHA256)) || (!adopting && c.SourceSHA256 != "") || c.VersionNo >= math.MaxUint64-1 || !videoHTTPIdempotency.MatchString(c.IdempotencyKey) {
		return nil, ErrVideoAdminCommandInvalid
	}
	reason := strings.TrimSpace(c.Reason)
	if reason == "" || !utf8.ValidString(reason) || len(reason) > 1024 || utf8.RuneCountInString(reason) > 256 || strings.IndexFunc(reason, unicode.IsControl) >= 0 {
		return nil, ErrVideoAdminCommandInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// 先鉴权再解析受控配置，避免无权请求触发分组/角色资源查询。
	if err := s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage") }); err != nil {
		return nil, err
	}
	definition, newModel, err := s.normalizeModelDraft(ctx, c.Definition)
	if err != nil {
		return nil, err
	}
	input, _ := json.Marshal([]any{action, c.ModelID, c.VersionNo, definition})
	// 保持原创建/更新指纹逐字兼容；只有历史接管显式绑定读取时的原始状态摘要。
	if adopting {
		input, _ = json.Marshal([]any{action, c.ModelID, c.VersionNo, definition, c.SourceSHA256})
	}
	keyHash, inputHash := videoBillingDigest("video_model_"+action+"\n"+c.IdempotencyKey), videoPayloadSHA256(input)
	var reply *VideoModelDraftReply
	err = retryVideoBillingTransaction(ctx, func() error {
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// 同操作者命令先锁用户行，避免并发从SHARE升级到UPDATE形成死锁；权限仍由真实IAM复核。
			var actor struct{ ID uint64 }
			if e := tx.Table("users").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id=?", c.Caller.UserID).Take(&actor).Error; e != nil {
				if errors.Is(e, gorm.ErrRecordNotFound) {
					return ErrVideoAdminForbidden
				}
				return errors.Join(ErrVideoAccessUnavailable, e)
			}
			if e := s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage"); e != nil {
				return e
			}
			var existing videoModelDraftRecord
			e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("actor_user_id=? AND action=? AND command_key_hash=?", c.Caller.UserID, action, keyHash).Take(&existing).Error
			if e == nil {
				plain, e := s.reasons.Open(modelDraftReasonID(existing), existing.VideoAdminReasonEnvelope)
				if e != nil {
					return ErrVideoAccessUnavailable
				}
				hash, hashErr := modelDraftResultHash(existing.ResultJSON)
				if e := verifyModelDraftAudits(tx, existing); e != nil {
					return e
				}
				if hashErr != nil || hash != existing.ResultSHA256 {
					return ErrVideoAccessUnavailable
				}
				if existing.InputSHA256 != inputHash || string(plain) != reason {
					return ErrVideoAdminCommandConflict
				}
				var result VideoModelDraftReply
				if json.Unmarshal(existing.ResultJSON, &result) != nil || result.ModelID != existing.ModelID || result.VersionNo != existing.ResultVersion {
					return ErrVideoAccessUnavailable
				}
				result.Idempotent = true
				reply = &result
				return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage")
			}
			if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
			m := *newModel
			newState := c.ModelID == 0
			if c.ModelID != 0 {
				var current model.TokenModel
				if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", c.ModelID).Take(&current).Error; e != nil {
					if errors.Is(e, gorm.ErrRecordNotFound) {
						return repository.ErrTokenModelNotFound
					}
					return errors.Join(ErrVideoAccessUnavailable, e)
				}
				var state videoModelDraftState
				stateErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("model_id=?", c.ModelID).Take(&state).Error
				if current.Modality != "video" || current.LogicalModelCode != m.LogicalModelCode {
					return ErrVideoAdminCommandConflict
				}
				if errors.Is(stateErr, gorm.ErrRecordNotFound) && adopting {
					hash, e := modelDraftAdoptionHash(current)
					if e != nil || hash != c.SourceSHA256 {
						return ErrVideoAdminCommandConflict
					}
					var history []struct{ ID uint64 }
					if e := tx.Model(&videoModelDraftRecord{}).Clauses(clause.Locking{Strength: "SHARE"}).Select("id").Where("model_id=?", c.ModelID).Limit(1).Find(&history).Error; e != nil {
						return errors.Join(ErrVideoAccessUnavailable, e)
					}
					if len(history) != 0 {
						return ErrVideoAdminCommandConflict
					}
					newState = true
				} else if stateErr != nil {
					if errors.Is(stateErr, gorm.ErrRecordNotFound) {
						return ErrVideoAdminCommandConflict
					}
					return errors.Join(ErrVideoAccessUnavailable, stateErr)
				} else {
					hash, e := modelDraftSnapshotHash(current)
					if e != nil || adopting || state.VersionNo != c.VersionNo || state.SnapshotSHA256 != hash {
						return ErrVideoAdminCommandConflict
					}
				}
				if current.LogicalModelCode != m.LogicalModelCode {
					return ErrVideoAdminCommandConflict
				}
				m.ID, m.Status, m.ReleaseVersionNo, m.PublishedAt, m.CreatedAt = current.ID, current.Status, current.ReleaseVersionNo, current.PublishedAt, current.CreatedAt
				m.SortOrder = current.SortOrder
			}
			publicID, e := newVideoHTTPID("vmd_")
			if e != nil {
				return e
			}
			command := videoModelDraftRecord{PublicID: publicID, Action: action, CommandKeyHash: keyHash, ActorUserID: c.Caller.UserID, ModelCode: m.LogicalModelCode, InitialVersion: c.VersionNo, ResultVersion: c.VersionNo + 1, InputSHA256: inputHash, CreatedAt: time.Now().UTC()}
			if adopting {
				source := c.SourceSHA256
				command.SourceSHA256 = &source
			}
			env, e := s.reasons.Seal(modelDraftReasonID(command), []byte(reason))
			if e != nil {
				return e
			}
			command.VideoAdminReasonEnvelope = *env
			command.BeforeAuditID, e = writeModelDraftAudit(ctx, tx, command, true)
			if e != nil {
				return e
			}
			m.UpdatedBy = &c.Caller.UserID
			if c.ModelID == 0 {
				if e := repository.NewTokenModelRepository(tx).Create(ctx, &m); e != nil {
					if repository.IsDuplicateKeyForHandler(e) {
						return ErrVideoAdminCommandConflict
					}
					return e
				}
			} else {
				updates := map[string]any{"display_name": m.DisplayName, "provider_name": m.ProviderName, "description": m.Description, "capabilities_json": m.CapabilitiesJSON, "video_contract_json": m.VideoContractJSON, "product_id": m.ProductID, "intro_url": m.IntroURL, "intro_url_health_status": m.IntroURLHealthStatus, "docs_url": m.DocsURL, "docs_url_health_status": m.DocsURLHealthStatus, "quick_start_url": m.QuickStartURL, "quick_start_url_health_status": m.QuickStartURLHealthStatus, "visible_scope": m.VisibleScope, "target_audience_json": m.TargetAudience, "updated_by": m.UpdatedBy}
				if e := tx.Model(&model.TokenModel{}).Where("id=? AND modality='video'", m.ID).Updates(updates).Error; e != nil {
					return e
				}
			}
			// 按数据库实际写入后的状态计算围栏，避免字符集/JSON规范化差异隐藏写入漂移。
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", m.ID).Take(&m).Error; e != nil {
				return e
			}
			hash, e := modelDraftSnapshotHash(m)
			if e != nil {
				return e
			}
			state := videoModelDraftState{ModelID: m.ID, VersionNo: command.ResultVersion, SnapshotSHA256: hash, UpdatedBy: c.Caller.UserID, UpdatedAt: time.Now().UTC()}
			if newState {
				if e := tx.Create(&state).Error; e != nil {
					return e
				}
			} else {
				r := tx.Model(&videoModelDraftState{}).Where("model_id=? AND version_no=?", m.ID, c.VersionNo).Updates(map[string]any{"version_no": state.VersionNo, "snapshot_sha256": hash, "updated_by": state.UpdatedBy, "updated_at": state.UpdatedAt})
				if r.Error != nil {
					return r.Error
				}
				if r.RowsAffected != 1 {
					return ErrVideoAdminCommandConflict
				}
			}
			reply = &VideoModelDraftReply{ModelID: m.ID, VersionNo: state.VersionNo, ReleaseVersionNo: m.ReleaseVersionNo, Definition: definition}
			command.ModelID = m.ID
			command.ResultJSON, e = json.Marshal(reply)
			if e != nil {
				return e
			}
			command.ResultSHA256, e = modelDraftResultHash(command.ResultJSON)
			if e != nil {
				return e
			}
			command.AfterAuditID, e = writeModelDraftAudit(ctx, tx, command, false)
			if e != nil {
				return e
			}
			if e := tx.Create(&command).Error; e != nil {
				return e
			}
			if e := verifyModelDraftAudits(tx, command); e != nil {
				return e
			}
			return s.authorizeTx(ctx, tx, c.Caller, "ai_gateway:model_manage")
		})
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}
