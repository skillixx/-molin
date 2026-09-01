package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	authmodel "molin/server/internal/modules/auth/model"
)

type ProjectKeyIdempotency struct{ Action, CommandKeyHash, Fingerprint string }
type projectKeyCommandRecord struct {
	ID                                  uint64 `gorm:"primaryKey"`
	UserID, ProjectID                   uint64
	Action, CommandKeyHash, Fingerprint string
	SourceKeyID                         *uint64
	ResultKeyID                         uint64
	ResultJSON                          json.RawMessage
	ResultSHA256                        string
	AuditID                             uint64
	AuditSHA256                         string
	CreatedAt                           time.Time
}

func (projectKeyCommandRecord) TableName() string { return "ai_project_key_commands" }

type projectKeyCommandResult struct {
	KeyID  uint64 `json:"key_id"`
	Status string `json:"status"`
}

func projectKeyCommandDigest(raw []byte) (string, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return sha256Hex(canonical), nil
}
func sha256Hex(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func decodeProjectKeyCommandResult(raw []byte) (projectKeyCommandResult, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || len(fields) != 2 {
		return projectKeyCommandResult{}, ErrRequestStateConflict
	}
	if _, ok := fields["key_id"]; !ok {
		return projectKeyCommandResult{}, ErrRequestStateConflict
	}
	if _, ok := fields["status"]; !ok {
		return projectKeyCommandResult{}, ErrRequestStateConflict
	}
	var result projectKeyCommandResult
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || result.KeyID == 0 || result.Status != "completed" {
		return projectKeyCommandResult{}, ErrRequestStateConflict
	}
	return result, nil
}

func projectKeySummaryUint(value any) (uint64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseUint(string(number), 10, 64)
	return parsed, err == nil
}

func projectKeySummaryStrings(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	values := make([]string, len(items))
	for index, item := range items {
		values[index], ok = item.(string)
		if !ok {
			return nil, false
		}
	}
	return values, true
}

func sameProjectKeyScopes(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func verifyProjectKeyAuditSummary(c projectKeyCommandRecord, key authmodel.APIKey, scopes []string, raw string) bool {
	var summary map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&summary); err != nil {
		return false
	}
	wantFields := map[string]int{"issue": 8, "rotate": 8, "revoke": 4}
	if len(summary) != wantFields[c.Action] {
		return false
	}
	project, ok := projectKeySummaryUint(summary["project_id"])
	if !ok || project != c.ProjectID || summary["idempotency_action"] != c.Action || summary["command_key_hash"] != c.CommandKeyHash || summary["fingerprint"] != c.Fingerprint {
		return false
	}
	if c.Action == "revoke" {
		return true
	}
	modelCodes, ok := projectKeySummaryStrings(summary["model_codes"])
	if !ok || !sameProjectKeyScopes(modelCodes, scopes) || summary["scope_mode"] != key.ScopeMode || summary["video_generate_allowed"] != key.VideoGenerateAllowed {
		return false
	}
	if c.Action == "rotate" {
		rotatedFrom, ok := projectKeySummaryUint(summary["rotated_from_id"])
		return ok && c.SourceKeyID != nil && rotatedFrom == *c.SourceKeyID
	}
	expiresAt, exists := summary["expires_at"]
	if !exists {
		return false
	}
	if key.ExpiresAt == nil {
		return expiresAt == nil
	}
	encoded, ok := expiresAt.(string)
	if !ok {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, encoded)
	return err == nil && parsed.Equal(*key.ExpiresAt)
}

func verifyProjectKeyCommand(tx *gorm.DB, c projectKeyCommandRecord, userID, projectID uint64) (*authmodel.APIKey, []string, error) {
	var stored projectKeyCommandRecord
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("id=?", c.ID).Take(&stored).Error; err != nil {
		return nil, nil, err
	}
	if stored.UserID != c.UserID || stored.ProjectID != c.ProjectID || stored.Action != c.Action || stored.CommandKeyHash != c.CommandKeyHash || stored.Fingerprint != c.Fingerprint || (stored.SourceKeyID == nil) != (c.SourceKeyID == nil) || (stored.SourceKeyID != nil && *stored.SourceKeyID != *c.SourceKeyID) || stored.ResultKeyID != c.ResultKeyID || stored.ResultSHA256 != c.ResultSHA256 || stored.AuditID != c.AuditID || stored.AuditSHA256 != c.AuditSHA256 {
		return nil, nil, ErrRequestStateConflict
	}
	c = stored
	if c.UserID != userID || c.ProjectID != projectID {
		return nil, nil, ErrRequestStateConflict
	}
	hash, err := projectKeyCommandDigest(c.ResultJSON)
	if err != nil || hash != c.ResultSHA256 {
		return nil, nil, ErrRequestStateConflict
	}
	result, err := decodeProjectKeyCommandResult(c.ResultJSON)
	if err != nil || result.KeyID != c.ResultKeyID {
		return nil, nil, ErrRequestStateConflict
	}
	var audit struct {
		OperatorID           *uint64
		Module, Action       string
		TargetType, TargetID *string
		RequestSummary       *string
	}
	if err := tx.Table("audit_logs").Clauses(clause.Locking{Strength: "SHARE"}).Select("operator_id,module,action,target_type,target_id,request_summary").Where("id=?", c.AuditID).Take(&audit).Error; err != nil {
		return nil, nil, err
	}
	wantAction := map[string]string{"issue": "create_project_key", "rotate": "rotate_project_key", "revoke": "revoke_project_key"}[c.Action]
	wantType, wantID := "api_key", strconv.FormatUint(c.ResultKeyID, 10)
	if audit.OperatorID == nil || *audit.OperatorID != userID || audit.Module != "token_gateway" || audit.Action != wantAction || audit.TargetType == nil || *audit.TargetType != wantType || audit.TargetID == nil || *audit.TargetID != wantID || audit.RequestSummary == nil {
		return nil, nil, ErrRequestStateConflict
	}
	auditHash, err := projectKeyCommandDigest([]byte(*audit.RequestSummary))
	if err != nil || auditHash != c.AuditSHA256 {
		return nil, nil, ErrRequestStateConflict
	}
	var key authmodel.APIKey
	if err := tx.Where("id=? AND user_id=? AND project_id=?", c.ResultKeyID, userID, projectID).Take(&key).Error; err != nil {
		return nil, nil, err
	}
	var scopes []string
	if err := tx.Model(&authmodel.APIKeyModelScope{}).Where("api_key_id=?", key.ID).Order("logical_model_code").Pluck("logical_model_code", &scopes).Error; err != nil {
		return nil, nil, err
	}
	if !key.VideoGenerateAllowed {
		return nil, nil, ErrRequestStateConflict
	}
	switch c.Action {
	case "issue":
		if c.SourceKeyID != nil || key.Status != "active" || key.RotatedFromID != nil {
			return nil, nil, ErrRequestStateConflict
		}
	case "rotate":
		if c.SourceKeyID == nil || key.Status != "active" || key.RotatedFromID == nil || *key.RotatedFromID != *c.SourceKeyID {
			return nil, nil, ErrRequestStateConflict
		}
		var source authmodel.APIKey
		if err := tx.Where("id=? AND user_id=? AND project_id=?", *c.SourceKeyID, userID, projectID).Take(&source).Error; err != nil || source.Status != "revoked" || !source.VideoGenerateAllowed {
			return nil, nil, ErrRequestStateConflict
		}
	case "revoke":
		if c.SourceKeyID == nil || c.ResultKeyID != *c.SourceKeyID || key.Status != "revoked" {
			return nil, nil, ErrRequestStateConflict
		}
	default:
		return nil, nil, ErrRequestStateConflict
	}
	if !verifyProjectKeyAuditSummary(c, key, scopes, *audit.RequestSummary) {
		return nil, nil, ErrRequestStateConflict
	}
	return &key, scopes, nil
}

var projectKeyHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validKeyIdempotency(i ProjectKeyIdempotency, action string) bool {
	return i.Action == action && projectKeyHashPattern.MatchString(i.CommandKeyHash) && projectKeyHashPattern.MatchString(i.Fingerprint)
}

func (r *G2Repository) findKeyCommandTx(tx *gorm.DB, userID, projectID uint64, i ProjectKeyIdempotency) (*projectKeyCommandRecord, error) {
	var c projectKeyCommandRecord
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND project_id=? AND action=? AND command_key_hash=?", userID, projectID, i.Action, i.CommandKeyHash).Take(&c).Error
	if err != nil {
		return nil, err
	}
	if c.Fingerprint != i.Fingerprint {
		return nil, ErrRequestStateConflict
	}
	return &c, nil
}

func (r *G2Repository) saveKeyCommandTx(tx *gorm.DB, userID, projectID uint64, i ProjectKeyIdempotency, source *uint64, keyID, auditID uint64) error {
	result := projectKeyCommandResult{KeyID: keyID, Status: "completed"}
	raw, _ := json.Marshal(result)
	hash, _ := projectKeyCommandDigest(raw)
	var audit struct{ RequestSummary *string }
	if auditID == 0 || tx.Table("audit_logs").Select("request_summary").Where("id=?", auditID).Take(&audit).Error != nil || audit.RequestSummary == nil {
		return ErrRequestStateConflict
	}
	auditHash, err := projectKeyCommandDigest([]byte(*audit.RequestSummary))
	if err != nil {
		return ErrRequestStateConflict
	}
	c := projectKeyCommandRecord{UserID: userID, ProjectID: projectID, Action: i.Action, CommandKeyHash: i.CommandKeyHash, Fingerprint: i.Fingerprint, SourceKeyID: source, ResultKeyID: keyID, ResultJSON: raw, ResultSHA256: hash, AuditID: auditID, AuditSHA256: auditHash, CreatedAt: time.Now().UTC()}
	if err := tx.Create(&c).Error; err != nil {
		return err
	}
	_, _, err = verifyProjectKeyCommand(tx, c, userID, projectID)
	return err
}

func (r *G2Repository) CreateProjectKeyIdempotent(ctx context.Context, key *authmodel.APIKey, scopes []authmodel.APIKeyModelScope, audit ProjectKeyAudit, i ProjectKeyIdempotency) (*authmodel.APIKey, []string, bool, error) {
	if key == nil || key.ProjectID == nil || !validKeyIdempotency(i, "issue") {
		return nil, nil, false, ErrRequestStateConflict
	}
	var result *authmodel.APIKey
	var codes []string
	existing := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user struct{ ID uint64 }
		if err := tx.Table("users").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id=?", key.UserID).Take(&user).Error; err != nil {
			return err
		}
		c, err := r.findKeyCommandTx(tx, key.UserID, *key.ProjectID, i)
		if err == nil {
			result, codes, err = verifyProjectKeyCommand(tx, *c, key.UserID, *key.ProjectID)
			existing = true
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var auditID uint64
		wrapped := func(inner *gorm.DB, id uint64, s []string, _ *ProjectKeyIdempotency) (uint64, error) {
			value, e := audit(inner, id, s, &i)
			auditID = value
			return value, e
		}
		if err := NewG2Repository(tx).CreateProjectKey(ctx, key, scopes, wrapped); err != nil {
			return err
		}
		if err := r.saveKeyCommandTx(tx, key.UserID, *key.ProjectID, i, nil, key.ID, auditID); err != nil {
			return err
		}
		result = key
		for _, s := range scopes {
			codes = append(codes, s.LogicalModelCode)
		}
		return nil
	})
	return result, codes, existing, err
}

func (r *G2Repository) RotateProjectKeyIdempotent(ctx context.Context, oldKey, newKey *authmodel.APIKey, audit ProjectKeyAudit, i ProjectKeyIdempotency) (*authmodel.APIKey, []string, bool, error) {
	if oldKey == nil || oldKey.ProjectID == nil || !validKeyIdempotency(i, "rotate") {
		return nil, nil, false, ErrRequestStateConflict
	}
	var result *authmodel.APIKey
	var codes []string
	existing := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user struct{ ID uint64 }
		if err := tx.Table("users").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id=?", oldKey.UserID).Take(&user).Error; err != nil {
			return err
		}
		c, err := r.findKeyCommandTx(tx, oldKey.UserID, *oldKey.ProjectID, i)
		if err == nil {
			result, codes, err = verifyProjectKeyCommand(tx, *c, oldKey.UserID, *oldKey.ProjectID)
			existing = true
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var auditID uint64
		wrapped := func(inner *gorm.DB, id uint64, s []string, _ *ProjectKeyIdempotency) (uint64, error) {
			value, e := audit(inner, id, s, &i)
			auditID = value
			return value, e
		}
		if err := NewG2Repository(tx).RotateProjectKey(ctx, oldKey, newKey, nil, wrapped); err != nil {
			return err
		}
		if err := r.saveKeyCommandTx(tx, oldKey.UserID, *oldKey.ProjectID, i, &oldKey.ID, newKey.ID, auditID); err != nil {
			return err
		}
		result = newKey
		_ = tx.Model(&authmodel.APIKeyModelScope{}).Where("api_key_id=?", newKey.ID).Order("logical_model_code").Pluck("logical_model_code", &codes).Error
		return nil
	})
	return result, codes, existing, err
}

func (r *G2Repository) RevokeProjectKeyIdempotent(ctx context.Context, userID, projectID, keyID uint64, audit ProjectKeyAudit, i ProjectKeyIdempotency) (bool, error) {
	if !validKeyIdempotency(i, "revoke") {
		return false, ErrRequestStateConflict
	}
	existing := false
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user struct{ ID uint64 }
		if err := tx.Table("users").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id=?", userID).Take(&user).Error; err != nil {
			return err
		}
		c, err := r.findKeyCommandTx(tx, userID, projectID, i)
		if err == nil {
			_, _, err = verifyProjectKeyCommand(tx, *c, userID, projectID)
			existing = true
			return err
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var auditID uint64
		wrapped := func(inner *gorm.DB, id uint64, s []string, _ *ProjectKeyIdempotency) (uint64, error) {
			value, e := audit(inner, id, s, &i)
			auditID = value
			return value, e
		}
		if err := NewG2Repository(tx).RevokeProjectKey(ctx, userID, projectID, keyID, wrapped); err != nil {
			return err
		}
		return r.saveKeyCommandTx(tx, userID, projectID, i, &keyID, keyID, auditID)
	})
	return existing, err
}
