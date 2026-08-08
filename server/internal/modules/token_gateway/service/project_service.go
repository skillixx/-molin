package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/pkg/crypto"
)

const (
	ProjectStatusActive    = "active"
	ProjectStatusSuspended = "suspended"
	ProjectStatusArchived  = "archived"
	ScopeModeAll           = "all"
	ScopeModeAllowlist     = "allowlist"
	ScopeModeLegacyAll     = "legacy_all"
)

var (
	ErrProjectInvalid           = errors.New("Project 参数错误")
	ErrProjectInactive          = errors.New("Project 已停用")
	ErrScopeModeInvalid         = errors.New("模型权限模式无效")
	ErrScopeModelInvalid        = errors.New("授权模型不存在、未发布或不是文字模型")
	ErrKeyExpiresAtInvalid      = errors.New("SK 过期时间必须晚于当前时间")
	ErrKeyNameInvalid           = errors.New("SK 名称不能为空且不能超过 191 个字符")
	ErrSecurityAuditUnavailable = errors.New("安全审计服务暂不可用")
)

// ProjectService 管理单用户 Project。G2 不引入组织成员、共享钱包和预算硬限制。
type ProjectService struct {
	repo          projectStore
	hmacSecret    string
	visibility    modelVisibilityChecker
	auditRecorder projectAuditRecorder
}

type projectAuditRecorder interface {
	RecordWithTx(ctx context.Context, tx *gorm.DB, operatorID *uint64, module, action string, targetType, targetID *string, ip string, requestSummary any) error
}

type projectStore interface {
	CreateProject(ctx context.Context, project *model.AIProject) error
	FindProject(ctx context.Context, userID, projectID uint64) (*model.AIProject, error)
	ListProjects(ctx context.Context, userID uint64, offset, limit int) ([]model.AIProject, int64, error)
	UpdateProject(ctx context.Context, userID, projectID uint64, updates map[string]interface{}) error
	CreateProjectKey(ctx context.Context, key *authmodel.APIKey, scopes []authmodel.APIKeyModelScope, audit repository.ProjectKeyAudit) error
	ListProjectKeys(ctx context.Context, userID, projectID uint64) ([]authmodel.APIKey, error)
	FindProjectKey(ctx context.Context, userID, projectID, keyID uint64) (*authmodel.APIKey, error)
	ListKeyScopes(ctx context.Context, keyID uint64) ([]string, error)
	RevokeProjectKey(ctx context.Context, userID, projectID, keyID uint64, audit repository.ProjectKeyAudit) error
	RotateProjectKey(ctx context.Context, oldKey *authmodel.APIKey, newKey *authmodel.APIKey, scopes []authmodel.APIKeyModelScope, audit repository.ProjectKeyAudit) error
	ActiveChatModelsExist(ctx context.Context, codes []string) (bool, error)
	UserRealNameStatus(ctx context.Context, userID uint64) (string, error)
}

func NewProjectService(repo projectStore, hmacSecret string) *ProjectService {
	return &ProjectService{repo: repo, hmacSecret: hmacSecret}
}

func (s *ProjectService) WithVisibilityChecker(checker modelVisibilityChecker) *ProjectService {
	s.visibility = checker
	return s
}

func (s *ProjectService) WithAuditRecorder(recorder projectAuditRecorder) *ProjectService {
	s.auditRecorder = recorder
	return s
}

type CreateProjectInput struct {
	UserID   uint64
	Name     string
	Timezone string
}

type UpdateProjectInput struct {
	UserID    uint64
	ProjectID uint64
	Name      *string
	Status    *string
	Timezone  *string
}

func (s *ProjectService) Create(ctx context.Context, in CreateProjectInput) (*model.AIProject, error) {
	name := strings.TrimSpace(in.Name)
	if in.UserID == 0 || name == "" || len(name) > 191 {
		return nil, ErrProjectInvalid
	}
	timezone, err := normalizeProjectTimezone(in.Timezone, "Asia/Shanghai")
	if err != nil {
		return nil, err
	}
	project := &model.AIProject{UserID: in.UserID, Name: name, Status: ProjectStatusActive, BudgetMode: "disabled", Timezone: timezone}
	if err := s.repo.CreateProject(ctx, project); err != nil {
		return nil, err
	}
	return project, nil
}

func (s *ProjectService) Get(ctx context.Context, userID, projectID uint64) (*model.AIProject, error) {
	return s.repo.FindProject(ctx, userID, projectID)
}

func (s *ProjectService) List(ctx context.Context, userID uint64, offset, limit int) ([]model.AIProject, int64, error) {
	return s.repo.ListProjects(ctx, userID, offset, limit)
}

func (s *ProjectService) Update(ctx context.Context, in UpdateProjectInput) (*model.AIProject, error) {
	updates := map[string]interface{}{}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || len(name) > 191 {
			return nil, ErrProjectInvalid
		}
		updates["name"] = name
	}
	if in.Status != nil {
		status := strings.TrimSpace(*in.Status)
		if status != ProjectStatusActive && status != ProjectStatusSuspended && status != ProjectStatusArchived {
			return nil, ErrProjectInvalid
		}
		updates["status"] = status
	}
	if in.Timezone != nil {
		timezone, err := normalizeProjectTimezone(*in.Timezone, "")
		if err != nil {
			return nil, err
		}
		updates["timezone"] = timezone
	}
	if len(updates) == 0 {
		return s.repo.FindProject(ctx, in.UserID, in.ProjectID)
	}
	if err := s.repo.UpdateProject(ctx, in.UserID, in.ProjectID, updates); err != nil {
		return nil, err
	}
	return s.repo.FindProject(ctx, in.UserID, in.ProjectID)
}

func normalizeProjectTimezone(value, fallback string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	if value == "" || len(value) > 64 || value == "Local" {
		return "", ErrProjectInvalid
	}
	if _, err := time.LoadLocation(value); err != nil {
		return "", ErrProjectInvalid
	}
	return value, nil
}

type IssueProjectKeyInput struct {
	UserID     uint64
	ProjectID  uint64
	Name       string
	ScopeMode  string
	ModelCodes []string
	ExpiresAt  *time.Time
	IP         string
}

type ProjectKeyView struct {
	ID         uint64     `json:"id"`
	ProjectID  uint64     `json:"project_id"`
	Name       string     `json:"name"`
	KeyPrefix  string     `json:"key_prefix"`
	ScopeMode  string     `json:"scope_mode"`
	ModelCodes []string   `json:"model_codes"`
	Status     string     `json:"status"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (s *ProjectService) IssueKey(ctx context.Context, in IssueProjectKeyInput) (string, ProjectKeyView, error) {
	if err := s.requireVerifiedUser(ctx, in.UserID); err != nil {
		return "", ProjectKeyView{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 191 {
		return "", ProjectKeyView{}, ErrKeyNameInvalid
	}
	project, err := s.repo.FindProject(ctx, in.UserID, in.ProjectID)
	if err != nil {
		return "", ProjectKeyView{}, err
	}
	if project.Status != ProjectStatusActive {
		return "", ProjectKeyView{}, ErrProjectInactive
	}
	mode, models, err := s.validateScope(ctx, in.UserID, in.ScopeMode, in.ModelCodes)
	if err != nil {
		return "", ProjectKeyView{}, err
	}
	if in.ExpiresAt != nil && !in.ExpiresAt.After(time.Now()) {
		return "", ProjectKeyView{}, ErrKeyExpiresAtInvalid
	}
	plaintext, prefix, hash, err := s.generateKey()
	if err != nil {
		return "", ProjectKeyView{}, err
	}
	key := &authmodel.APIKey{
		UserID: in.UserID, ProjectID: &in.ProjectID, KeyPrefix: prefix, KeyHash: hash,
		Name: name, BillingMode: "postpaid", ScopeMode: mode,
		Status: "active", ExpiresAt: in.ExpiresAt,
	}
	scopes := buildScopeModels(in.UserID, in.ProjectID, models)
	audit, err := s.keyAudit(ctx, in.UserID, "create_project_key", in.ProjectID, in.IP, map[string]interface{}{"scope_mode": mode, "model_codes": models, "expires_at": in.ExpiresAt})
	if err != nil {
		return "", ProjectKeyView{}, err
	}
	if err := s.repo.CreateProjectKey(ctx, key, scopes, audit); err != nil {
		return "", ProjectKeyView{}, err
	}
	return plaintext, projectKeyView(key, models), nil
}

func (s *ProjectService) ListKeys(ctx context.Context, userID, projectID uint64) ([]ProjectKeyView, error) {
	if _, err := s.repo.FindProject(ctx, userID, projectID); err != nil {
		return nil, err
	}
	keys, err := s.repo.ListProjectKeys(ctx, userID, projectID)
	if err != nil {
		return nil, err
	}
	views := make([]ProjectKeyView, 0, len(keys))
	for i := range keys {
		models, scopeErr := s.repo.ListKeyScopes(ctx, keys[i].ID)
		if scopeErr != nil {
			return nil, scopeErr
		}
		views = append(views, projectKeyView(&keys[i], models))
	}
	return views, nil
}

func (s *ProjectService) RevokeKey(ctx context.Context, userID, projectID, keyID uint64, ip string) error {
	audit, err := s.keyAudit(ctx, userID, "revoke_project_key", projectID, ip, nil)
	if err != nil {
		return err
	}
	if err := s.repo.RevokeProjectKey(ctx, userID, projectID, keyID, audit); err != nil {
		return err
	}
	return nil
}

func (s *ProjectService) RotateKey(ctx context.Context, userID, projectID, keyID uint64, ip string) (string, ProjectKeyView, error) {
	if err := s.requireVerifiedUser(ctx, userID); err != nil {
		return "", ProjectKeyView{}, err
	}
	oldKey, err := s.repo.FindProjectKey(ctx, userID, projectID, keyID)
	if err != nil {
		return "", ProjectKeyView{}, err
	}
	if oldKey.Status != "active" || (oldKey.ExpiresAt != nil && !oldKey.ExpiresAt.After(time.Now())) {
		return "", ProjectKeyView{}, repository.ErrProjectKeyNotFound
	}
	models, err := s.repo.ListKeyScopes(ctx, oldKey.ID)
	if err != nil {
		return "", ProjectKeyView{}, err
	}
	plaintext, prefix, hash, err := s.generateKey()
	if err != nil {
		return "", ProjectKeyView{}, err
	}
	newKey := &authmodel.APIKey{
		UserID: userID, ProjectID: &projectID, KeyPrefix: prefix, KeyHash: hash,
		Name: oldKey.Name, BillingMode: "postpaid", ScopeMode: oldKey.ScopeMode,
		Status: "active", ExpiresAt: oldKey.ExpiresAt, RotatedFromID: &oldKey.ID,
	}
	audit, err := s.keyAudit(ctx, userID, "rotate_project_key", projectID, ip, map[string]interface{}{"rotated_from_id": oldKey.ID, "scope_mode": oldKey.ScopeMode, "model_codes": models})
	if err != nil {
		return "", ProjectKeyView{}, err
	}
	if err := s.repo.RotateProjectKey(ctx, oldKey, newKey, buildScopeModels(userID, projectID, models), audit); err != nil {
		return "", ProjectKeyView{}, err
	}
	return plaintext, projectKeyView(newKey, models), nil
}

// requireVerifiedUser 在签发或轮换可调用 SK 前重新读取数据库实名状态，不能信任前端路由守卫。
func (s *ProjectService) requireVerifiedUser(ctx context.Context, userID uint64) error {
	status, err := s.repo.UserRealNameStatus(ctx, userID)
	if err != nil {
		return err
	}
	if status != "verified" {
		return ErrRealNameRequired
	}
	return nil
}

func (s *ProjectService) validateScope(ctx context.Context, userID uint64, mode string, modelCodes []string) (string, []string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = ScopeModeAllowlist
	}
	if mode != ScopeModeAll && mode != ScopeModeAllowlist {
		return "", nil, ErrScopeModeInvalid
	}
	models := uniqueNonEmpty(modelCodes)
	if mode == ScopeModeAll {
		if len(models) != 0 {
			return "", nil, ErrScopeModeInvalid
		}
		return mode, nil, nil
	}
	ok, err := s.repo.ActiveChatModelsExist(ctx, models)
	if err != nil {
		return "", nil, err
	}
	if !ok {
		return "", nil, ErrScopeModelInvalid
	}
	if s.visibility == nil {
		return "", nil, ErrScopeModelInvalid
	}
	for _, code := range models {
		visible, visibilityErr := s.visibility.VisibleToUser(ctx, userID, code)
		if visibilityErr != nil || !visible {
			return "", nil, ErrScopeModelInvalid
		}
	}
	return mode, models, nil
}

func (s *ProjectService) keyAudit(ctx context.Context, userID uint64, action string, projectID uint64, ip string, summary map[string]interface{}) (repository.ProjectKeyAudit, error) {
	if s.auditRecorder == nil {
		return nil, ErrSecurityAuditUnavailable
	}
	if summary == nil {
		summary = map[string]interface{}{}
	}
	summary["project_id"] = projectID
	targetType := "api_key"
	return func(tx *gorm.DB, keyID uint64) error {
		targetID := strconv.FormatUint(keyID, 10)
		if err := s.auditRecorder.RecordWithTx(ctx, tx, &userID, "token_gateway", action, &targetType, &targetID, ip, summary); err != nil {
			return ErrSecurityAuditUnavailable
		}
		return nil
	}, nil
}

func (s *ProjectService) generateKey() (string, string, string, error) {
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", "", "", err
	}
	segment := base64.RawURLEncoding.EncodeToString(randomBytes)
	plaintext := "sk-molin-" + segment
	prefix := "sk-molin-" + segment[:4]
	return plaintext, prefix, crypto.HMAC256(plaintext, s.hmacSecret), nil
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func buildScopeModels(userID, projectID uint64, models []string) []authmodel.APIKeyModelScope {
	result := make([]authmodel.APIKeyModelScope, 0, len(models))
	for _, code := range models {
		result = append(result, authmodel.APIKeyModelScope{UserID: userID, ProjectID: projectID, LogicalModelCode: code})
	}
	return result
}

func projectKeyView(key *authmodel.APIKey, models []string) ProjectKeyView {
	projectID := uint64(0)
	if key.ProjectID != nil {
		projectID = *key.ProjectID
	}
	return ProjectKeyView{
		ID: key.ID, ProjectID: projectID, Name: key.Name, KeyPrefix: key.KeyPrefix,
		ScopeMode: key.ScopeMode, ModelCodes: models, Status: key.Status,
		ExpiresAt: key.ExpiresAt, LastUsedAt: key.LastUsedAt, CreatedAt: key.CreatedAt,
	}
}
