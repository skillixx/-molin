package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	iamrepo "molin/server/internal/modules/iam/repository"
	iamservice "molin/server/internal/modules/iam/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrVideoCapabilityDenied  = errors.New("视频能力未授权")
	ErrVideoAccessUnavailable = errors.New("视频准入服务不可用")
	ErrVideoEntitlementDenied = errors.New("视频模型所需权益不可用")
)

// VideoCaller仅由认证上下文构造；请求只能指定Project，不能覆盖认证用户或Key。
type VideoCaller struct {
	UserID, APIKeyID, ProjectID uint64
	credential                  *videoReadCredential
}

// VideoAccessService只校验持久化的身份、权限、模型与权益，不创建报价或修改钱包。
// 预算、队列、权利声明和钱包由后续原子创建链校验，不能把本入口当作完整生成授权。
type VideoAccessService struct{ db *gorm.DB }

func NewVideoAccessService(db *gorm.DB) *VideoAccessService { return &VideoAccessService{db: db} }

func (s *VideoAccessService) Resolve(ctx context.Context, caller VideoCaller, code string) (repository.VideoOwner, error) {
	owner := repository.VideoOwner{UserID: caller.UserID, ProjectID: caller.ProjectID}
	if s == nil || s.db == nil || caller.UserID == 0 {
		return owner, ErrVideoBillingAccess
	}
	if caller.APIKeyID != 0 {
		var key struct{ ProjectID *uint64 }
		if err := s.db.WithContext(ctx).Table("api_keys").Select("project_id").Where("id=? AND user_id=?", caller.APIKeyID, caller.UserID).Take(&key).Error; err != nil {
			return owner, videoAccessReadError(err, ErrVideoBillingAccess)
		}
		if key.ProjectID == nil {
			return owner, ErrVideoBillingAccess
		}
		if caller.ProjectID != 0 && caller.ProjectID != *key.ProjectID {
			return owner, ErrVideoBillingAccess
		}
		owner.ProjectID = *key.ProjectID
		id := caller.APIKeyID
		owner.APIKeyID = &id
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return s.AuthorizeTx(ctx, tx, owner, code, time.Now().UTC()) })
	return owner, err
}

// AuthorizeSubjectTx执行不依赖模型的基础准入；即使列表没有授权模型也不能跳过实名和主体状态。
func (s *VideoAccessService) AuthorizeSubjectTx(ctx context.Context, tx *gorm.DB, owner repository.VideoOwner, now time.Time) error {
	return s.authorizeSubjectTx(ctx, tx, owner, now, nil)
}

func (s *VideoAccessService) authorizeSubjectTx(ctx context.Context, tx *gorm.DB, owner repository.VideoOwner, now time.Time, proof *videoAccessExpiry) error {
	if tx == nil || owner.UserID == 0 || owner.ProjectID == 0 {
		return ErrVideoBillingAccess
	}
	// 所有后续查询继承调用方期限，不能沿外层补偿事务更长的context继续等待。
	tx = tx.WithContext(ctx)
	var account struct{ UserStatus, ProjectStatus, RealNameStatus string }
	if err := tx.WithContext(ctx).Raw("SELECT u.status AS user_status,p.status AS project_status,u.real_name_status FROM ai_projects p JOIN users u ON u.id=p.user_id WHERE p.id=? AND u.id=? FOR SHARE", owner.ProjectID, owner.UserID).Scan(&account).Error; err != nil {
		return ErrVideoAccessUnavailable
	}
	if account.UserStatus != "active" || account.ProjectStatus != "active" {
		return ErrVideoBillingAccess
	}
	if account.RealNameStatus != "verified" {
		return ErrRealNameRequired
	}
	if owner.APIKeyID != nil {
		var key struct {
			Status, ScopeMode, BillingMode string
			ExpiresAt                      *time.Time
			VideoGenerateAllowed           bool
		}
		if err := tx.Table("api_keys").Clauses(clause.Locking{Strength: "SHARE"}).Select("status,scope_mode,billing_mode,expires_at,video_generate_allowed").Where("id=? AND user_id=? AND project_id=?", *owner.APIKeyID, owner.UserID, owner.ProjectID).Take(&key).Error; err != nil {
			return videoAccessReadError(err, ErrVideoBillingAccess)
		}
		if key.Status != "active" || key.BillingMode != "postpaid" || key.ScopeMode == ScopeModeLegacyAll || (key.ExpiresAt != nil && !key.ExpiresAt.After(now)) {
			return ErrVideoBillingAccess
		}
		if !key.VideoGenerateAllowed {
			return ErrVideoCapabilityDenied
		}
		proof.require(key.ExpiresAt, ErrVideoBillingAccess)
	}
	var blocked []struct{ ID uint64 }
	query := tx.Table("ai_safety_subject_actions").Clauses(clause.Locking{Strength: "SHARE"}).Select("id").Where("status='active' AND action='suspend' AND (expires_at IS NULL OR expires_at>?)", now)
	if owner.APIKeyID == nil {
		query = query.Where("subject_type='user' AND subject_id=?", strconv.FormatUint(owner.UserID, 10))
	} else {
		query = query.Where("(subject_type='user' AND subject_id=?) OR (subject_type='api_key' AND subject_id=?)", strconv.FormatUint(owner.UserID, 10), strconv.FormatUint(*owner.APIKeyID, 10))
	}
	if err := query.Find(&blocked).Error; err != nil {
		return ErrVideoAccessUnavailable
	}
	if len(blocked) != 0 {
		return ErrVideoCapabilityDenied
	}
	iam := newVideoFreshIAM(tx)
	var allowed bool
	var err error
	if proof == nil {
		allowed, err = iam.CheckPermissionFresh(ctx, owner.UserID, "video:generate")
	} else {
		var end *time.Time
		allowed, end, err = iam.CheckPermissionFreshWithExpiry(ctx, owner.UserID, "video:generate")
		proof.require(end, ErrVideoCapabilityDenied)
	}
	if err != nil {
		return ErrVideoAccessUnavailable
	}
	if !allowed {
		return ErrVideoCapabilityDenied
	}
	return nil
}

// AuthorizeTx允许G5协调器在自身事务内再次校验，避免HTTP预检与实际写入形成两个权限时点。
func (s *VideoAccessService) AuthorizeTx(ctx context.Context, tx *gorm.DB, owner repository.VideoOwner, code string, now time.Time, operations ...string) error {
	return s.authorizeTx(ctx, tx, owner, code, now, operations, nil)
}

func (s *VideoAccessService) authorizeTx(ctx context.Context, tx *gorm.DB, owner repository.VideoOwner, code string, now time.Time, operations []string, proof *videoAccessExpiry) error {
	if code == "" {
		return ErrVideoBillingAccess
	}
	if tx != nil {
		tx = tx.WithContext(ctx)
	}
	if err := s.authorizeSubjectTx(ctx, tx, owner, now, proof); err != nil {
		return err
	}
	iam := newVideoFreshIAM(tx)
	item, err := videoPublishedModel(tx, code, now)
	if err != nil {
		return err
	}
	if len(operations) > 1 {
		return ErrVideoOptionUnsupported
	}
	if len(operations) == 1 {
		allowed := false
		for _, operation := range item.Contract.SupportedOperations {
			allowed = allowed || operation == operations[0]
		}
		if !allowed {
			return ErrVideoOptionUnsupported
		}
	}
	resolver := &videoSQLVisibility{db: tx.Clauses(clause.Locking{Strength: "SHARE"})}
	visible := modelVisibleTo(ctx, &item.TokenModel, owner.UserID, resolver, resolver)
	if resolver.failure != nil {
		return ErrVideoAccessUnavailable
	}
	if !visible {
		return ErrVideoCapabilityDenied
	}
	var grant struct{ ID uint64 }
	if err := tx.Table("ai_project_model_capability_grants").Clauses(clause.Locking{Strength: "SHARE"}).Select("id").Where("user_id=? AND project_id=? AND logical_model_code=? AND capability=? AND status='active'", owner.UserID, owner.ProjectID, code, model.AIVideoCapability).Take(&grant).Error; err != nil {
		return videoAccessReadError(err, ErrVideoCapabilityDenied)
	}
	if owner.APIKeyID != nil {
		var scope struct{ ID uint64 }
		if err := tx.Table("api_key_model_scopes").Clauses(clause.Locking{Strength: "SHARE"}).Select("id").Where("user_id=? AND project_id=? AND api_key_id=? AND logical_model_code=?", owner.UserID, owner.ProjectID, *owner.APIKeyID, code).Take(&scope).Error; err != nil {
			return videoAccessReadError(err, ErrVideoCapabilityDenied)
		}
	}
	if item.ProductID != nil {
		if err := videoProductAccess(ctx, tx, iam, owner.UserID, *item.ProductID, item.Contract, now, proof); err != nil {
			return err
		}
	}
	return videoMembershipAccess(tx, owner.UserID, item.Contract.RequiredMembershipLevels, now, proof)
}

func newVideoFreshIAM(db *gorm.DB) *iamservice.IAMService {
	// 原G5外层可能已建立RR快照；显式当前读才能看到另一连接已提交的deny/角色撤销。
	db = db.Clauses(clause.Locking{Strength: "SHARE"})
	return iamservice.NewIAMService(iamrepo.NewRoleRepository(db), iamrepo.NewPermissionRepository(db), iamrepo.NewUserRoleRepository(db), iamrepo.NewOverrideRepository(db), iamrepo.NewGroupRepository(db), nil, nil)
}

func videoAccessReadError(err, errorIfMissing error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errorIfMissing
	}
	return ErrVideoAccessUnavailable
}

// 发布快照是对外模型合同的真相源；后台工作副本不能提前改变可见范围或商品要求。
type videoPublishedModelRecord struct {
	model.TokenModel
	Contract VideoModelContract
}

func videoPublishedModel(tx *gorm.DB, code string, now time.Time) (*videoPublishedModelRecord, error) {
	var item model.TokenModel
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("logical_model_code=?", code).Take(&item).Error; err != nil {
		return nil, videoAccessReadError(err, ErrVideoCapabilityDenied)
	}
	if item.Status != "active" || item.Modality != "video" || item.ReleaseVersionNo == 0 || item.PublishedAt == nil || item.PublishedAt.After(now) {
		return nil, ErrVideoCapabilityDenied
	}
	var release model.AIModelReleaseVersion
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("model_id=? AND version_no=? AND status='active'", item.ID, item.ReleaseVersionNo).Take(&release).Error; err != nil {
		return nil, videoAccessReadError(err, ErrVideoCapabilityDenied)
	}
	var snapshot struct {
		model.TokenModelReleaseSnapshot
		VideoContract json.RawMessage `json:"video_contract"`
	}
	if json.Unmarshal(release.SnapshotJSON, &snapshot) != nil || snapshot.LogicalModelCode != code || snapshot.Modality != "video" || !capabilityEnabled(snapshot.Capabilities, model.AIVideoCapability) || release.PublishedAt.After(now) {
		return nil, ErrVideoCapabilityDenied
	}
	item.VisibleScope, item.TargetAudience, item.ProductID = snapshot.VisibleScope, snapshot.TargetAudience, snapshot.ProductID
	item.CapabilitiesJSON = snapshot.Capabilities
	contract, err := ParseVideoModelContract(snapshot.VideoContract, snapshot.ProductID)
	if err != nil {
		return nil, err
	}
	return &videoPublishedModelRecord{TokenModel: item, Contract: contract}, nil
}

// 商品使用权不等于购买权；权益必须属于同一商品和用户且父资产可用。
func videoProductAccess(ctx context.Context, tx *gorm.DB, iam *iamservice.IAMService, userID, productID uint64, contract VideoModelContract, now time.Time, proofs ...*videoAccessExpiry) error {
	proof := firstVideoAccessExpiry(proofs)
	roles, err := iam.GetUserRoleIDs(ctx, userID)
	if err != nil {
		return ErrVideoAccessUnavailable
	}
	if len(roles) == 0 {
		return ErrVideoEntitlementDenied
	}
	var usable []struct{ ID uint64 }
	if err := tx.Table("products AS p").Clauses(clause.Locking{Strength: "SHARE"}).Select("p.id").Joins("JOIN product_role_access a ON a.product_id=p.id").Where("p.id=? AND p.status='active' AND a.role_id IN ? AND a.can_use=1", productID, roles).Find(&usable).Error; err != nil {
		return ErrVideoAccessUnavailable
	}
	if len(usable) == 0 {
		return ErrVideoEntitlementDenied
	}
	if !contract.AssetRequired {
		return nil
	}
	var assets []struct {
		ID        uint64
		ExpiresAt *time.Time
	}
	if err := tx.Table("user_assets").Clauses(clause.Locking{Strength: "SHARE"}).Select("id,expires_at").Where("user_id=? AND product_id=? AND status='active' AND (started_at IS NULL OR started_at<=?) AND (expires_at IS NULL OR expires_at>?)", userID, productID, now, now).Find(&assets).Error; err != nil {
		return ErrVideoAccessUnavailable
	}
	if len(assets) == 0 {
		return ErrVideoEntitlementDenied
	}
	// 只有显式配置指定类型时才要求配额；只有资产的商品不被错误要求存在权益行。
	if contract.RequiredEntitlementType == nil {
		paths := make([][]*time.Time, 0, len(assets))
		for _, a := range assets {
			paths = append(paths, []*time.Time{a.ExpiresAt})
		}
		proof.alternatives(paths, ErrVideoEntitlementDenied)
		return nil
	}
	var entitlements []struct {
		ID                         uint64
		ExpiresAt, ParentExpiresAt *time.Time
	}
	if err := tx.Table("user_entitlements AS e").Clauses(clause.Locking{Strength: "SHARE"}).Select("e.id,e.expires_at,a.expires_at AS parent_expires_at").Joins("JOIN user_assets a ON a.id=e.asset_id AND a.user_id=e.user_id AND a.product_id=e.product_id").Where("e.user_id=? AND e.product_id=? AND e.status='active' AND a.status='active'", userID, productID).
		Where("(e.started_at IS NULL OR e.started_at<=?) AND (a.started_at IS NULL OR a.started_at<=?)", now, now).
		Where("(e.expires_at IS NULL OR e.expires_at>?) AND (a.expires_at IS NULL OR a.expires_at>?)", now, now).
		Where("e.entitlement_type=?", *contract.RequiredEntitlementType).
		Where("e.quota_total IS NULL OR e.quota_total-e.quota_used-e.quota_reserved>0").Find(&entitlements).Error; err != nil {
		return ErrVideoAccessUnavailable
	}
	if len(entitlements) == 0 {
		return ErrVideoEntitlementDenied
	}
	paths := make([][]*time.Time, 0, len(entitlements))
	for _, e := range entitlements {
		paths = append(paths, []*time.Time{e.ExpiresAt, e.ParentExpiresAt})
	}
	proof.alternatives(paths, ErrVideoEntitlementDenied)
	return nil
}

// 可见性仍调用既有modelVisibleTo规则，解析器只从当前真实数据库读取分组与角色。
type videoSQLVisibility struct {
	db      *gorm.DB
	failure error
}

func (v *videoSQLVisibility) UserGroupRoles(ctx context.Context, userID uint64) (map[uint64]string, error) {
	rows, err := iamrepo.NewGroupRepository(v.db).GetUserGroups(ctx, userID)
	if err != nil {
		v.failure = err
		return nil, err
	}
	result := make(map[uint64]string, len(rows))
	for _, row := range rows {
		result[row.GroupID] = row.GroupRole
	}
	return result, nil
}
func (v *videoSQLVisibility) ExistingGroupIDs(ctx context.Context, ids []uint64) (map[uint64]struct{}, error) {
	var found []uint64
	err := v.db.WithContext(ctx).Table("user_groups").Where("id IN ?", ids).Pluck("id", &found).Error
	v.failure = err
	result := make(map[uint64]struct{}, len(found))
	for _, id := range found {
		result[id] = struct{}{}
	}
	return result, err
}
func (v *videoSQLVisibility) GetUserRoleCodes(ctx context.Context, userID uint64) ([]string, error) {
	var codes []string
	err := v.db.WithContext(ctx).Table("roles r").Joins("JOIN user_roles u ON u.role_id=r.id").Where("u.user_id=?", userID).Pluck("r.code", &codes).Error
	v.failure = err
	return codes, err
}
func (v *videoSQLVisibility) ExistingRoleCodes(ctx context.Context, codes []string) (map[string]struct{}, error) {
	var found []string
	err := v.db.WithContext(ctx).Table("roles").Where("code IN ?", codes).Pluck("code", &found).Error
	v.failure = err
	result := make(map[string]struct{}, len(found))
	for _, code := range found {
		result[code] = struct{}{}
	}
	return result, err
}
