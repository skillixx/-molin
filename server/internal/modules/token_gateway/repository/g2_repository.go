package repository

import (
	"context"
	"errors"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/model"
)

var (
	ErrProjectNotFound      = errors.New("Project 不存在")
	ErrProjectNameExists    = errors.New("Project 名称已存在")
	ErrProjectKeyNotFound   = errors.New("Project SK 不存在")
	ErrRequestNotFound      = errors.New("AI 请求不存在")
	ErrRequestStateConflict = errors.New("AI 请求状态冲突")
)

// G2Repository 集中承载 Project、Project SK 和正式请求账本事务，确保跨表状态一次提交。
type G2Repository struct {
	db *gorm.DB
}

// ProjectKeyAudit 在 Project SK 事务中写入审计事实；返回错误时业务变更必须一起回滚。
type ProjectKeyAudit func(tx *gorm.DB, keyID uint64) error

func NewG2Repository(db *gorm.DB) *G2Repository { return &G2Repository{db: db} }

func (r *G2Repository) CreateProject(ctx context.Context, project *model.AIProject) error {
	err := r.db.WithContext(ctx).Create(project).Error
	if isDuplicateKey(err) {
		return ErrProjectNameExists
	}
	return err
}

func (r *G2Repository) FindProject(ctx context.Context, userID, projectID uint64) (*model.AIProject, error) {
	var project model.AIProject
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ?", projectID, userID).First(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectNotFound
	}
	return &project, err
}

func (r *G2Repository) ListProjects(ctx context.Context, userID uint64, offset, limit int) ([]model.AIProject, int64, error) {
	query := r.db.WithContext(ctx).Model(&model.AIProject{}).Where("user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var projects []model.AIProject
	err := query.Order("id DESC").Offset(offset).Limit(limit).Find(&projects).Error
	return projects, total, err
}

func (r *G2Repository) UpdateProject(ctx context.Context, userID, projectID uint64, updates map[string]interface{}) error {
	result := r.db.WithContext(ctx).Model(&model.AIProject{}).
		Where("id = ? AND user_id = ?", projectID, userID).Updates(updates)
	if result.Error != nil {
		if isDuplicateKey(result.Error) {
			return ErrProjectNameExists
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrProjectNotFound
	}
	return nil
}

func isDuplicateKey(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var mysqlError *gomysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}

// IsDuplicateKeyForHandler 供治理管理接口把唯一冲突稳定映射为 409，不暴露数据库错误正文。
func IsDuplicateKeyForHandler(err error) bool { return isDuplicateKey(err) }

// CreateProjectKey 在一个事务内创建哈希密钥和 allowlist，避免出现可用密钥但权限未落库的窗口。
func (r *G2Repository) CreateProjectKey(ctx context.Context, key *authmodel.APIKey, scopes []authmodel.APIKeyModelScope, audit ProjectKeyAudit) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(key).Error; err != nil {
			return err
		}
		for i := range scopes {
			scopes[i].APIKeyID = key.ID
		}
		if len(scopes) > 0 {
			if err := tx.Create(&scopes).Error; err != nil {
				return err
			}
		}
		return audit(tx, key.ID)
	})
}

func (r *G2Repository) ListProjectKeys(ctx context.Context, userID, projectID uint64) ([]authmodel.APIKey, error) {
	var keys []authmodel.APIKey
	err := r.db.WithContext(ctx).Where("user_id = ? AND project_id = ?", userID, projectID).
		Order("id DESC").Find(&keys).Error
	return keys, err
}

func (r *G2Repository) FindProjectKey(ctx context.Context, userID, projectID, keyID uint64) (*authmodel.APIKey, error) {
	var key authmodel.APIKey
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ? AND project_id = ?", keyID, userID, projectID).First(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectKeyNotFound
	}
	return &key, err
}

func (r *G2Repository) FindProjectKeyByID(ctx context.Context, userID, keyID uint64) (*authmodel.APIKey, error) {
	var key authmodel.APIKey
	err := r.db.WithContext(ctx).Where("id = ? AND user_id = ? AND project_id IS NOT NULL", keyID, userID).First(&key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrProjectKeyNotFound
	}
	return &key, err
}

func (r *G2Repository) ListKeyScopes(ctx context.Context, keyID uint64) ([]string, error) {
	var scopes []string
	err := r.db.WithContext(ctx).Model(&authmodel.APIKeyModelScope{}).
		Where("api_key_id = ?", keyID).Order("logical_model_code ASC").
		Pluck("logical_model_code", &scopes).Error
	return scopes, err
}

func (r *G2Repository) RevokeProjectKey(ctx context.Context, userID, projectID, keyID uint64, audit ProjectKeyAudit) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&authmodel.APIKey{}).
			Where("id = ? AND user_id = ? AND project_id = ? AND status = 'active'", keyID, userID, projectID).
			Update("status", "revoked")
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var key authmodel.APIKey
			if err := tx.Where("id = ? AND user_id = ? AND project_id = ?", keyID, userID, projectID).First(&key).Error; err != nil {
				return ErrProjectKeyNotFound
			}
		}
		return audit(tx, keyID)
	})
}

// RotateProjectKey 把新密钥写入和旧密钥吊销放进同一事务，保证轮换不存在双活窗口。
func (r *G2Repository) RotateProjectKey(ctx context.Context, oldKey *authmodel.APIKey, newKey *authmodel.APIKey, scopes []authmodel.APIKeyModelScope, audit ProjectKeyAudit) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked authmodel.APIKey
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ? AND project_id = ?", oldKey.ID, oldKey.UserID, oldKey.ProjectID).
			First(&locked).Error; err != nil {
			return ErrProjectKeyNotFound
		}
		if locked.Status != "active" {
			return ErrProjectKeyNotFound
		}
		if err := tx.Create(newKey).Error; err != nil {
			return err
		}
		for i := range scopes {
			scopes[i].APIKeyID = newKey.ID
		}
		if len(scopes) > 0 {
			if err := tx.Create(&scopes).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&authmodel.APIKey{}).Where("id = ? AND status = 'active'", oldKey.ID).
			Update("status", "revoked").Error; err != nil {
			return err
		}
		return audit(tx, newKey.ID)
	})
}

func (r *G2Repository) ActiveChatModelsExist(ctx context.Context, codes []string) (bool, error) {
	if len(codes) == 0 {
		return true, nil
	}
	var count int64
	err := r.db.WithContext(ctx).Model(&model.TokenModel{}).
		Where("logical_model_code IN ? AND status = 'active' AND modality = 'chat' AND release_version_no > 0 AND published_at IS NOT NULL", codes).
		Distinct("logical_model_code").Count(&count).Error
	return count == int64(len(codes)), err
}

// G2AccessSnapshot 是上游调用前在数据库重新确认的授权快照。
type G2AccessSnapshot struct {
	UserStatus     string
	RealNameStatus string
	ProjectStatus  string
	KeyStatus      string
	ScopeMode      string
	KeyExpiresAt   *time.Time
	Timezone       string
	ModelAllowed   bool
	TokenModel     model.TokenModel
}

// g2AccessSnapshotRow 只承接授权 SQL 的扁平字段，避免 GORM 将 TokenModel 误判为未声明的关联关系。
// 模型实体在授权行确认存在后单独查询，再由仓储层组装为对外快照。
type g2AccessSnapshotRow struct {
	UserStatus     string
	RealNameStatus string
	ProjectStatus  string
	KeyStatus      string
	ScopeMode      string
	KeyExpiresAt   *time.Time
	Timezone       string
	ModelAllowed   bool
}

func (r *G2Repository) LoadAccessSnapshot(ctx context.Context, userID, projectID, keyID uint64, modelCode string) (*G2AccessSnapshot, error) {
	var row g2AccessSnapshotRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT u.status AS user_status, u.real_name_status,
		       p.status AS project_status, k.status AS key_status,
		       k.scope_mode, k.expires_at, p.timezone,
		       CASE
		         WHEN k.scope_mode = 'all' THEN 1
		         WHEN k.scope_mode = 'allowlist' AND s.id IS NOT NULL THEN 1
		         WHEN k.scope_mode = 'legacy_all' AND (k.model_scope = '' OR FIND_IN_SET(?, k.model_scope) > 0) THEN 1
		         ELSE 0
		       END AS model_allowed
		FROM users u
		JOIN ai_projects p ON p.user_id = u.id AND p.id = ?
		JOIN api_keys k ON k.user_id = u.id AND k.project_id = p.id AND k.id = ?
		LEFT JOIN api_key_model_scopes s ON s.api_key_id = k.id AND s.logical_model_code = ?
		WHERE u.id = ?`, modelCode, projectID, keyID, modelCode, userID).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	if row.KeyStatus == "" {
		return nil, ErrProjectKeyNotFound
	}
	var tokenModel model.TokenModel
	if err := r.db.WithContext(ctx).Where("logical_model_code = ?", modelCode).First(&tokenModel).Error; err != nil {
		return nil, err
	}
	return &G2AccessSnapshot{
		UserStatus: row.UserStatus, RealNameStatus: row.RealNameStatus,
		ProjectStatus: row.ProjectStatus, KeyStatus: row.KeyStatus,
		ScopeMode: row.ScopeMode, KeyExpiresAt: row.KeyExpiresAt,
		Timezone: row.Timezone, ModelAllowed: row.ModelAllowed,
		TokenModel: tokenModel,
	}, nil
}

func (r *G2Repository) FindRequestByIdentity(ctx context.Context, requestID string) (*model.AIRequest, error) {
	var request model.AIRequest
	err := r.db.WithContext(ctx).Where("request_id = ?", requestID).First(&request).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRequestNotFound
	}
	return &request, err
}

func (r *G2Repository) FindRequestByIdempotency(ctx context.Context, userID uint64, key string) (*model.AIRequest, error) {
	var request model.AIRequest
	err := r.db.WithContext(ctx).Where("user_id = ? AND idempotency_key = ?", userID, key).First(&request).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrRequestNotFound
	}
	return &request, err
}

func (r *G2Repository) CreateRequest(ctx context.Context, request *model.AIRequest) error {
	return r.db.WithContext(ctx).Create(request).Error
}

// StartRequest 用旧状态和版本号条件推进 pending -> running，并同时创建唯一 attempt=1。
func (r *G2Repository) StartRequest(ctx context.Context, requestID string, attempt *model.AIExecutionAttempt) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.AIRequest{}).
			Where("request_id = ? AND execution_status = ? AND billing_status IN ?", requestID, model.AIExecutionPending, []string{model.AIBillingUnquoted, model.AIBillingHeld}).
			Updates(map[string]interface{}{"execution_status": model.AIExecutionRunning, "started_at": attempt.StartedAt, "version_no": gorm.Expr("version_no + 1")})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRequestStateConflict
		}
		return tx.Create(attempt).Error
	})
}

// FinalizeRequest 在一个事务内写终态尝试、Usage 和请求终态；重复 Finalize 不产生第二份计量。
func (r *G2Repository) FinalizeRequest(ctx context.Context, requestID string, attempt model.AIExecutionAttempt, usage []model.AIUsageItem, clientDisconnected bool, errorClass, errorCode *string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.AIExecutionAttempt{}).
			Where("request_id = ? AND attempt_no = ? AND status = 'running'", requestID, attempt.AttemptNo).
			Updates(map[string]interface{}{
				"status": attempt.Status, "result_unknown": attempt.ResultUnknown, "latency_ms": attempt.LatencyMS,
				"prompt_tokens": attempt.PromptTokens, "completion_tokens": attempt.CompletionTokens,
				"reasoning_tokens": attempt.ReasoningTokens, "cached_tokens": attempt.CachedTokens,
				"error_class": attempt.ErrorClass, "finished_at": attempt.FinishedAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var existing model.AIExecutionAttempt
			if err := tx.Where("request_id = ? AND attempt_no = ?", requestID, attempt.AttemptNo).First(&existing).Error; err != nil {
				return ErrRequestStateConflict
			}
			if existing.Status == attempt.Status {
				return nil
			}
			return ErrRequestStateConflict
		}
		if len(usage) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&usage).Error; err != nil {
				return err
			}
		}
		completed := time.Now()
		result = tx.Model(&model.AIRequest{}).
			Where("request_id = ? AND execution_status = ? AND billing_status = ?", requestID, model.AIExecutionRunning, model.AIBillingUnquoted).
			Updates(map[string]interface{}{
				"execution_status": requestStatusFromAttempt(attempt), "billing_status": model.AIBillingUnquoted,
				"client_disconnected": clientDisconnected, "error_class": errorClass, "error_code": errorCode,
				"execution_model_code": attempt.ExecutionModelCode, "completed_at": completed,
				"version_no": gorm.Expr("version_no + 1"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRequestStateConflict
		}
		return nil
	})
}

func requestStatusFromAttempt(attempt model.AIExecutionAttempt) string {
	if attempt.ResultUnknown || attempt.Status == "timeout" || attempt.Status == "unknown" {
		return model.AIExecutionUnknown
	}
	if attempt.Status == "succeeded" {
		return model.AIExecutionSucceeded
	}
	return model.AIExecutionFailed
}

func (r *G2Repository) MarkPendingOrRunningUnknown(ctx context.Context, requestID string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Model(&model.AIExecutionAttempt{}).
			Where("request_id = ? AND status = 'running'", requestID).
			Updates(map[string]interface{}{"status": "unknown", "result_unknown": true, "error_class": "reconcile_required", "finished_at": now}).Error; err != nil {
			return err
		}
		result := tx.Model(&model.AIRequest{}).
			Where("request_id = ? AND execution_status IN ? AND billing_status = ?", requestID, []string{model.AIExecutionPending, model.AIExecutionRunning}, model.AIBillingUnquoted).
			Updates(map[string]interface{}{"execution_status": model.AIExecutionUnknown, "error_class": "reconcile_required", "error_code": "execution_interrupted", "completed_at": now, "version_no": gorm.Expr("version_no + 1")})
		return result.Error
	})
}

// ListRecoverableRequestIDs 只返回越过安全窗口的遗留请求，避免把仍在其他节点执行的长流误判为中断。
func (r *G2Repository) ListRecoverableRequestIDs(ctx context.Context, pendingBefore, runningBefore time.Time, limit int) ([]string, error) {
	var requestIDs []string
	err := r.db.WithContext(ctx).Model(&model.AIRequest{}).
		Where("billing_status = ? AND ((execution_status = ? AND updated_at < ?) OR (execution_status = ? AND updated_at < ?))",
			model.AIBillingUnquoted, model.AIExecutionPending, pendingBefore, model.AIExecutionRunning, runningBefore).
		Order("updated_at ASC").Limit(limit).Pluck("request_id", &requestIDs).Error
	return requestIDs, err
}

// MarkRecoverableUnknown 在同一事务内重新锁定并校验截止时间，关闭扫描与 StartRequest 之间的竞态窗口。
func (r *G2Repository) MarkRecoverableUnknown(ctx context.Context, requestID string, pendingBefore, runningBefore time.Time) (bool, error) {
	changed := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request model.AIRequest
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("request_id = ? AND billing_status = ? AND ((execution_status = ? AND updated_at < ?) OR (execution_status = ? AND updated_at < ?))",
				requestID, model.AIBillingUnquoted, model.AIExecutionPending, pendingBefore, model.AIExecutionRunning, runningBefore).
			First(&request).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&model.AIExecutionAttempt{}).
			Where("request_id = ? AND status = 'running'", requestID).
			Updates(map[string]interface{}{"status": "unknown", "result_unknown": true, "error_class": "reconcile_required", "finished_at": now}).Error; err != nil {
			return err
		}
		result := tx.Model(&model.AIRequest{}).
			Where("request_id = ? AND execution_status = ? AND billing_status = ?", requestID, request.ExecutionStatus, model.AIBillingUnquoted).
			Updates(map[string]interface{}{"execution_status": model.AIExecutionUnknown, "error_class": "reconcile_required", "error_code": "execution_interrupted", "completed_at": now, "version_no": gorm.Expr("version_no + 1")})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRequestStateConflict
		}
		changed = true
		return nil
	})
	return changed, err
}

// MarkClientDisconnected 只补充传输事实，不改变已经形成的执行和计费终态。
func (r *G2Repository) MarkClientDisconnected(ctx context.Context, requestID string) error {
	return r.db.WithContext(ctx).Model(&model.AIRequest{}).
		Where("request_id = ?", requestID).Update("client_disconnected", true).Error
}
