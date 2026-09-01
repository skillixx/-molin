package service_test

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"molin/server/internal/middleware"
	auditmodel "molin/server/internal/modules/audit/model"
	auditrepo "molin/server/internal/modules/audit/repository"
	auditservice "molin/server/internal/modules/audit/service"
	authmodel "molin/server/internal/modules/auth/model"
	authrepo "molin/server/internal/modules/auth/repository"
	authservice "molin/server/internal/modules/auth/service"
	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	pkgjwt "molin/server/pkg/jwt"
)

func TestVideoG6ProjectKeyHTTPMySQL(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	defer func() {
		// 本用例会让 api_keys 使用 AUTO_INCREMENT；退出时推进共享显式编号，避免后续隔离夹具复用同一主键。
		var maxKeyID uint64
		if err := f.DB.Table("api_keys").Select("COALESCE(MAX(id),0)").Scan(&maxKeyID).Error; err == nil {
			service.ReserveVideoFixtureIDsThrough(maxKeyID)
		}
	}()
	// 公开目录要求完整已发布展示字段；只补本轮隔离快照，不改发布合同、价格或真实环境。
	if err := f.DB.Exec(`UPDATE ai_model_release_versions SET snapshot_json=JSON_SET(snapshot_json,'$.display_name','视频Key测试模型','$.provider_name','合成厂商') WHERE model_id=? AND version_no=1`, f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	secretBytes := func() []byte {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	hmacSecret, jwtSecret := hex.EncodeToString(secretBytes()), hex.EncodeToString(secretBytes())
	svc := service.NewProjectService(repository.NewG2Repository(f.DB), hmacSecret).
		WithVisibilityChecker(service.NewCatalogService(repository.NewTokenModelRepository(f.DB))).
		WithAuditRecorder(auditservice.NewAuditService(auditrepo.NewAuditLogRepository(f.DB)))
	h := handler.NewProjectHandler(svc)
	mux := http.NewServeMux()
	mux.Handle("POST /api/token/projects/{id}/keys", middleware.RequireAuth(jwtSecret, nil, http.HandlerFunc(h.IssueKey)))
	mux.Handle("GET /api/token/projects/{id}/keys", middleware.RequireAuth(jwtSecret, nil, http.HandlerFunc(h.ListKeys)))
	mux.Handle("POST /api/token/projects/{id}/keys/{key_id}/rotate", middleware.RequireAuth(jwtSecret, nil, http.HandlerFunc(h.RotateKey)))
	mux.Handle("DELETE /api/token/projects/{id}/keys/{key_id}", middleware.RequireAuth(jwtSecret, nil, http.HandlerFunc(h.RevokeKey)))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	token, err := pkgjwt.Generate(f.ProjectID, "", jwtSecret, 3600)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		ID              uint64 `json:"id"`
		Name            string `json:"name"`
		Secret          string `json:"secret_key"`
		Allowed         bool   `json:"video_generate_allowed"`
		Status          string `json:"status"`
		SecretAvailable bool   `json:"secret_available"`
		Idempotent      bool   `json:"idempotent"`
	}
	call := func(method, path string, body []byte) (int, result, []byte) {
		t.Helper()
		r, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		if method == "POST" || method == "DELETE" {
			key := "video-key-issue-idem"
			safePath := strings.ReplaceAll(path, "/", "-")
			if strings.Contains(path, "/rotate") {
				key = "video-key-rotate" + safePath
			}
			if method == "DELETE" {
				key = "video-key-revoke" + safePath
			}
			r.Header.Set("Idempotency-Key", key)
		}
		res, err := srv.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Data result `json:"data"`
		}
		_ = json.Unmarshal(raw, &envelope)
		return res.StatusCode, envelope.Data, raw
	}
	path := fmt.Sprintf("/api/token/projects/%d/keys", f.ProjectID)
	legacyID := service.NextVideoFixtureUserID()
	if err := f.DB.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status) VALUES(?,?,?,'legacy','legacy-video-disabled-hash','历史默认关闭Key','postpaid','allowlist','active')", legacyID, f.ProjectID, f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	body := func(allowed bool, scope string, unknown bool) []byte {
		fields := map[string]any{"name": "视频测试Key", "scope_mode": scope, "model_codes": []string{f.Model}, "expires_at": nil, "video_generate_allowed": allowed}
		if unknown {
			fields["VideoGenerateAllowed"] = true
		}
		raw, _ := json.Marshal(fields)
		return raw
	}
	status, _, _ := call("POST", path, body(false, "allowlist", false))
	if status != 400 {
		t.Fatal("未显式开启却接受视频scope")
	}
	status, _, _ = call("POST", path, body(true, "all", false))
	if status != 400 {
		t.Fatal("all模式获得视频能力")
	}
	status, _, _ = call("POST", path, body(true, "allowlist", true))
	if status != 400 {
		t.Fatal("大小写别名绕过严格字段")
	}
	counts := func() (int64, int64, int64) {
		var keys, scopes, audits int64
		_ = f.DB.Table("api_keys").Where("project_id=?", f.ProjectID).Count(&keys).Error
		_ = f.DB.Table("api_key_model_scopes").Where("project_id=?", f.ProjectID).Count(&scopes).Error
		_ = f.DB.Table("audit_logs").Where("operator_id=? AND action='create_project_key'", f.ProjectID).Count(&audits).Error
		return keys, scopes, audits
	}
	beforeKeys, beforeScopes, beforeAudits := counts()
	for _, statement := range []string{"UPDATE token_models SET published_at=DATE_ADD(UTC_TIMESTAMP(),INTERVAL 1 HOUR) WHERE id=?", "UPDATE ai_model_release_versions SET published_at=DATE_ADD(UTC_TIMESTAMP(),INTERVAL 1 HOUR) WHERE model_id=? AND version_no=1"} {
		if err := f.DB.Exec(statement, f.ProjectID).Error; err != nil {
			t.Fatal(err)
		}
		status, _, _ = call("POST", path, body(true, "allowlist", false))
		if status != 400 {
			t.Fatal("未来生效视频模型提前签发Key")
		}
		if err := f.DB.Exec("UPDATE token_models SET published_at=DATE_SUB(UTC_TIMESTAMP(),INTERVAL 1 SECOND) WHERE id=?", f.ProjectID).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.DB.Exec("UPDATE ai_model_release_versions SET published_at=DATE_SUB(UTC_TIMESTAMP(),INTERVAL 1 SECOND) WHERE model_id=? AND version_no=1", f.ProjectID).Error; err != nil {
			t.Fatal(err)
		}
	}
	afterKeys, afterScopes, afterAudits := counts()
	if beforeKeys != afterKeys || beforeScopes != afterScopes || beforeAudits != afterAudits {
		t.Fatal("未来发布拒绝后留下Key、scope或审计")
	}
	status, created, raw := call("POST", path, body(true, "allowlist", false))
	if status != 201 || created.ID == 0 || created.Secret == "" || !created.Allowed || !bytes.Contains(raw, []byte(`"video_generate_allowed":true`)) {
		t.Fatalf("视频Key签发失败 status=%d", status)
	}
	// HTTP签发使用AUTO_INCREMENT；立即推进共享显式编号，不能等到用例退出后才同步。
	service.ReserveVideoFixtureIDsThrough(created.ID)
	status, replayed, _ := call("POST", path, body(true, "allowlist", false))
	if status != 200 || replayed.ID != created.ID || replayed.Secret != "" || replayed.SecretAvailable || !replayed.Idempotent {
		t.Fatal("签发重放再次暴露Secret或新建Key")
	}
	changedBody := map[string]any{"name": "不同名称", "scope_mode": "allowlist", "model_codes": []string{f.Model}, "expires_at": nil, "video_generate_allowed": true}
	changedRaw, _ := json.Marshal(changedBody)
	status, _, _ = call("POST", path, changedRaw)
	if status != 409 {
		t.Fatal("同键异签发意图未冲突")
	}
	if auth := authservice.NewAPIKeyService(authrepo.NewAPIKeyRepository(f.DB), hmacSecret, nil); func() bool {
		u, k, ok := auth.ResolveKeyForAuth(t.Context(), created.Secret)
		return ok && u == f.ProjectID && k == created.ID
	}() == false {
		t.Fatal("签发Secret不能通过真实HMAC鉴权")
	}
	status, _, listRaw := call("GET", path, nil)
	if status != 200 || bytes.Contains(listRaw, []byte(created.Secret)) || !bytes.Contains(listRaw, []byte(`"video_generate_allowed":true`)) {
		t.Fatal("列表未回显能力或泄露完整Secret")
	}
	status, rotated, _ := call("POST", fmt.Sprintf("%s/%d/rotate", path, created.ID), nil)
	if status != 201 || !rotated.Allowed || rotated.Secret == "" || rotated.ID == created.ID {
		t.Fatalf("轮换未继承显式视频能力 status=%d allowed=%v secret_available=%v same_id=%v idempotent=%v", status, rotated.Allowed, rotated.SecretAvailable, rotated.ID == created.ID, rotated.Idempotent)
	}
	service.ReserveVideoFixtureIDsThrough(rotated.ID)
	status, rotationReplay, _ := call("POST", fmt.Sprintf("%s/%d/rotate", path, created.ID), nil)
	if status != 200 || rotationReplay.ID != rotated.ID || rotationReplay.Secret != "" || rotationReplay.SecretAvailable || !rotationReplay.Idempotent {
		t.Fatal("轮换重放再次暴露Secret或新建Key")
	}
	if err := f.DB.Exec("UPDATE ai_project_model_capability_grants SET status='revoked',version_no=version_no+1 WHERE project_id=? AND logical_model_code=?", f.ProjectID, f.Model).Error; err != nil {
		t.Fatal(err)
	}
	var before int64
	_ = f.DB.Table("api_keys").Where("project_id=?", f.ProjectID).Count(&before).Error
	status, _, _ = call("POST", fmt.Sprintf("%s/%d/rotate", path, rotated.ID), nil)
	if status != 400 {
		t.Fatalf("Project授权撤销后仍轮换视频Key status=%d", status)
	}
	var after int64
	_ = f.DB.Table("api_keys").Where("project_id=?", f.ProjectID).Count(&after).Error
	if after != before {
		t.Fatal("失败轮换留下新Key")
	}
	if err := f.DB.Exec("UPDATE ai_project_model_capability_grants SET status='active',version_no=version_no+1 WHERE project_id=? AND logical_model_code=?", f.ProjectID, f.Model).Error; err != nil {
		t.Fatal(err)
	}
	status, last, _ := call("POST", fmt.Sprintf("%s/%d/rotate", path, rotated.ID), nil)
	if status != 201 || !last.Allowed {
		t.Fatal("恢复授权后轮换失败")
	}
	service.ReserveVideoFixtureIDsThrough(last.ID)
	var oldDefault bool
	if err := f.DB.Table("api_keys").Select("video_generate_allowed").Where("id=?", legacyID).Scan(&oldDefault).Error; err != nil || oldDefault {
		t.Fatal("历史Key自动继承了视频能力")
	}
	// 仓储必须忽略事务外对象伪造的all、期限和scope，只从锁定旧Key重建。
	expires := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	if err := f.DB.Model(&authmodel.APIKey{}).Where("id=?", last.ID).Update("expires_at", expires).Error; err != nil {
		t.Fatal(err)
	}
	projectID := f.ProjectID
	maliciousOld := authmodel.APIKey{ID: last.ID, UserID: f.ProjectID, ProjectID: &projectID, ScopeMode: "all", VideoGenerateAllowed: false}
	maliciousNew := authmodel.APIKey{UserID: f.ProjectID, ProjectID: &projectID, KeyPrefix: "malicious", KeyHash: fmt.Sprintf("malicious-rotation-%d", last.ID), Name: "篡改名称", BillingMode: "prepaid", ScopeMode: "all", Status: "active", ExpiresAt: func() *time.Time { x := time.Now().UTC().Add(365 * 24 * time.Hour); return &x }(), VideoGenerateAllowed: false}
	repo := repository.NewG2Repository(f.DB)
	if err := repo.RotateProjectKey(t.Context(), &maliciousOld, &maliciousNew, nil, func(*gorm.DB, uint64, []string, *repository.ProjectKeyIdempotency) (uint64, error) { return 1, nil }); err != nil {
		t.Fatal("仓储未按锁定事实轮换")
	}
	service.ReserveVideoFixtureIDsThrough(maliciousNew.ID)
	var locked authmodel.APIKey
	if err := f.DB.First(&locked, maliciousNew.ID).Error; err != nil {
		t.Fatal(err)
	}
	var scopes []string
	if err := f.DB.Model(&authmodel.APIKeyModelScope{}).Where("api_key_id=?", maliciousNew.ID).Pluck("logical_model_code", &scopes).Error; err != nil {
		t.Fatal(err)
	}
	if locked.Name != last.Name || locked.ScopeMode != "allowlist" || !locked.VideoGenerateAllowed || locked.ExpiresAt == nil || !locked.ExpiresAt.Equal(expires) || len(scopes) != 1 || scopes[0] != f.Model {
		t.Fatal("轮换信任了事务外scope或期限")
	}
	status, _, _ = call("DELETE", fmt.Sprintf("%s/%d", path, maliciousNew.ID), nil)
	if status != 204 {
		t.Fatal("视频Key吊销失败")
	}
	status, _, _ = call("DELETE", fmt.Sprintf("%s/%d", path, maliciousNew.ID), nil)
	if status != 204 {
		t.Fatal("吊销重放失败")
	}
	plainID := service.NextVideoFixtureUserID()
	plain := authmodel.APIKey{ID: plainID, UserID: f.ProjectID, KeyPrefix: "plain", KeyHash: fmt.Sprintf("plain-key-%d", plainID), Name: "普通非Project Key", BillingMode: "postpaid", ScopeMode: "legacy_all", Status: "active"}
	if err := f.DB.Create(&plain).Error; err != nil {
		t.Fatal(err)
	}
	called := false
	badNew := authmodel.APIKey{KeyPrefix: "bad", KeyHash: fmt.Sprintf("bad-nil-project-%d", plainID)}
	if err := repo.RotateProjectKey(t.Context(), &plain, &badNew, nil, func(*gorm.DB, uint64, []string, *repository.ProjectKeyIdempotency) (uint64, error) {
		called = true
		return 1, nil
	}); err == nil || called || badNew.ID != 0 {
		t.Fatal("非Project旧Key未失败关闭")
	}
}

// TestVideoG6ProjectKeyCommandCorruptionFailsClosedMySQL 从真实 MySQL 重放入口验证：
// 命令、审计和 Key 状态只要有一处不能互相证明，就必须失败关闭且不能补写第二把 Key。
func TestVideoG6ProjectKeyCommandCorruptionFailsClosedMySQL(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	repo := repository.NewG2Repository(f.DB)
	projectID := f.ProjectID
	ctx := t.Context()

	newKey := func(status string, rotatedFrom *uint64, suffix string) *authmodel.APIKey {
		t.Helper()
		key := &authmodel.APIKey{
			UserID: f.ProjectID, ProjectID: &projectID, KeyPrefix: "corrupt-" + suffix,
			KeyHash: strings.Repeat(suffix, 64/len(suffix)+1)[:64], Name: "损坏事实测试Key",
			BillingMode: "postpaid", ScopeMode: "allowlist", Status: status,
			RotatedFromID: rotatedFrom, VideoGenerateAllowed: true,
		}
		if err := f.DB.Create(key).Error; err != nil {
			t.Fatal(err)
		}
		return key
	}
	digest := func(raw []byte) string {
		t.Helper()
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(canonical)
		return hex.EncodeToString(sum[:])
	}
	createAudit := func(action string, keyID uint64, summary map[string]any) uint64 {
		t.Helper()
		raw, err := json.Marshal(summary)
		if err != nil {
			t.Fatal(err)
		}
		targetType, targetID, summaryJSON := "api_key", strconv.FormatUint(keyID, 10), string(raw)
		entry := auditmodel.AuditLog{OperatorID: &f.ProjectID, Module: "token_gateway", Action: action, TargetType: &targetType, TargetID: &targetID, RequestSummary: &summaryJSON}
		if err := f.DB.Create(&entry).Error; err != nil {
			t.Fatal(err)
		}
		return entry.ID
	}
	insertCommand := func(action, hash string, source *uint64, result *authmodel.APIKey, auditID uint64, auditSummary map[string]any) {
		t.Helper()
		resultJSON, _ := json.Marshal(map[string]any{"key_id": result.ID, "status": "completed"})
		auditJSON, _ := json.Marshal(auditSummary)
		row := map[string]any{
			"user_id": f.ProjectID, "project_id": f.ProjectID, "action": action,
			"command_key_hash": hash, "fingerprint": strings.Repeat("f", 64),
			"source_key_id": source, "result_key_id": result.ID,
			"result_json": resultJSON, "result_sha256": digest(resultJSON),
			"audit_id": auditID, "audit_sha256": digest(auditJSON), "created_at": time.Now().UTC(),
		}
		if err := f.DB.Table("ai_project_key_commands").Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	idem := func(action, char string) repository.ProjectKeyIdempotency {
		return repository.ProjectKeyIdempotency{Action: action, CommandKeyHash: strings.Repeat(char, 64), Fingerprint: strings.Repeat("f", 64)}
	}
	unusedAudit := func(*gorm.DB, uint64, []string, *repository.ProjectKeyIdempotency) (uint64, error) {
		t.Fatal("损坏命令重放不得再次写审计")
		return 0, nil
	}

	t.Run("首写审计摘要缺少命令绑定时整笔回滚", func(t *testing.T) {
		key := &authmodel.APIKey{UserID: f.ProjectID, ProjectID: &projectID, KeyPrefix: "first-write", KeyHash: strings.Repeat("1", 64), Name: "首写回滚", BillingMode: "postpaid", ScopeMode: "allowlist", Status: "active", VideoGenerateAllowed: true}
		command := idem("issue", "1")
		badAudit := func(tx *gorm.DB, keyID uint64, _ []string, _ *repository.ProjectKeyIdempotency) (uint64, error) {
			targetType, targetID, summary := "api_key", strconv.FormatUint(keyID, 10), `{"project_id":1}`
			entry := auditmodel.AuditLog{OperatorID: &f.ProjectID, Module: "token_gateway", Action: "create_project_key", TargetType: &targetType, TargetID: &targetID, RequestSummary: &summary}
			return entry.ID, tx.Create(&entry).Error
		}
		_, _, _, err := repo.CreateProjectKeyIdempotent(ctx, key, nil, badAudit, command)
		if !errors.Is(err, repository.ErrRequestStateConflict) {
			t.Fatalf("损坏审计未失败关闭: %v", err)
		}
		var keys, commands int64
		_ = f.DB.Model(&authmodel.APIKey{}).Where("key_hash=?", key.KeyHash).Count(&keys).Error
		_ = f.DB.Table("ai_project_key_commands").Where("command_key_hash=?", command.CommandKeyHash).Count(&commands).Error
		if keys != 0 || commands != 0 {
			t.Fatalf("首写复核失败后仍留下事实: keys=%d commands=%d", keys, commands)
		}
	})

	t.Run("签发结果Key已吊销时拒绝重放", func(t *testing.T) {
		result := newKey("revoked", nil, "2")
		command := idem("issue", "2")
		summary := map[string]any{"project_id": f.ProjectID, "scope_mode": "allowlist", "model_codes": []string{}, "expires_at": nil, "video_generate_allowed": true, "idempotency_action": "issue", "command_key_hash": command.CommandKeyHash, "fingerprint": command.Fingerprint}
		auditID := createAudit("create_project_key", result.ID, summary)
		insertCommand("issue", command.CommandKeyHash, nil, result, auditID, summary)
		probe := &authmodel.APIKey{UserID: f.ProjectID, ProjectID: &projectID}
		if _, _, _, err := repo.CreateProjectKeyIdempotent(ctx, probe, nil, unusedAudit, command); !errors.Is(err, repository.ErrRequestStateConflict) {
			t.Fatalf("已吊销签发结果未失败关闭: %v", err)
		}
	})

	t.Run("轮换结果未关联来源时拒绝重放", func(t *testing.T) {
		source := newKey("revoked", nil, "3")
		result := newKey("active", nil, "4")
		command := idem("rotate", "3")
		summary := map[string]any{"project_id": f.ProjectID, "rotated_from_id": source.ID, "scope_mode": "allowlist", "model_codes": []string{}, "video_generate_allowed": true, "idempotency_action": "rotate", "command_key_hash": command.CommandKeyHash, "fingerprint": command.Fingerprint}
		auditID := createAudit("rotate_project_key", result.ID, summary)
		insertCommand("rotate", command.CommandKeyHash, &source.ID, result, auditID, summary)
		probe := &authmodel.APIKey{ID: source.ID, UserID: f.ProjectID, ProjectID: &projectID}
		if _, _, _, err := repo.RotateProjectKeyIdempotent(ctx, probe, &authmodel.APIKey{}, unusedAudit, command); !errors.Is(err, repository.ErrRequestStateConflict) {
			t.Fatalf("错绑轮换结果未失败关闭: %v", err)
		}
	})

	t.Run("吊销结果仍为active时拒绝重放", func(t *testing.T) {
		result := newKey("active", nil, "5")
		command := idem("revoke", "4")
		summary := map[string]any{"project_id": f.ProjectID, "idempotency_action": "revoke", "command_key_hash": command.CommandKeyHash, "fingerprint": command.Fingerprint}
		auditID := createAudit("revoke_project_key", result.ID, summary)
		insertCommand("revoke", command.CommandKeyHash, &result.ID, result, auditID, summary)
		if _, err := repo.RevokeProjectKeyIdempotent(ctx, f.ProjectID, f.ProjectID, result.ID, unusedAudit, command); !errors.Is(err, repository.ErrRequestStateConflict) {
			t.Fatalf("未吊销结果未失败关闭: %v", err)
		}
	})
}
