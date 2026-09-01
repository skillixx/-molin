package token_gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	authrepo "molin/server/internal/modules/auth/repository"
	authservice "molin/server/internal/modules/auth/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
	"molin/server/pkg/crypto"
	pkgjwt "molin/server/pkg/jwt"
)

type g6HTTPUploadEntry struct {
	target       service.VideoUploadTarget
	cap          string
	raw, norm    []byte
	mime         string
	sealed, dead bool
}

// 对象HTTP是假外部系统，Molin鉴权、上传状态、规范化、Quote与钱包均执行真实服务。
type g6HTTPUploadStore struct {
	sync.Mutex
	server  *httptest.Server
	entries map[string]*g6HTTPUploadEntry
}

func (s *g6HTTPUploadStore) Issue(_ context.Context, target service.VideoUploadTarget) (*service.VideoUploadGrant, error) {
	s.Lock()
	defer s.Unlock()
	e := s.entries[target.SessionID]
	if e == nil {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		e = &g6HTTPUploadEntry{target: target, cap: hex.EncodeToString(b[:])}
		s.entries[target.SessionID] = e
	}
	if e.dead || e.sealed {
		return nil, service.ErrVideoUploadConflict
	}
	return &service.VideoUploadGrant{Method: "PUT", URL: s.server.URL + "/objects/" + target.SessionID + "?cap=" + e.cap, Headers: map[string]string{"Content-Type": target.MIMEType}, ExpiresAt: target.UploadExpiresAt}, nil
}
func (s *g6HTTPUploadStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" {
		w.WriteHeader(405)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20))
	if err != nil {
		w.WriteHeader(413)
		return
	}
	s.Lock()
	defer s.Unlock()
	e := s.entries[strings.TrimPrefix(r.URL.Path, "/objects/")]
	if e == nil || r.URL.Query().Get("cap") != e.cap || !e.target.UploadExpiresAt.After(time.Now()) {
		w.WriteHeader(403)
		return
	}
	if e.dead || e.sealed {
		w.WriteHeader(409)
		return
	}
	e.raw = append([]byte(nil), raw...)
	e.mime = r.Header.Get("Content-Type")
	w.WriteHeader(204)
}
func (s *g6HTTPUploadStore) Seal(_ context.Context, target service.VideoUploadTarget, max int64) (*service.VideoSealedUpload, error) {
	s.Lock()
	defer s.Unlock()
	e := s.entries[target.SessionID]
	if e == nil || e.dead || len(e.raw) == 0 || int64(len(e.raw)) > max {
		return nil, service.ErrVideoUploadUnavailable
	}
	e.sealed = true
	return &service.VideoSealedUpload{Bytes: append([]byte(nil), e.raw...), MIMEType: e.mime, ETag: crypto.SHA256Hex(string(e.raw)), VersionID: "sealed-http-fixture"}, nil
}
func (s *g6HTTPUploadStore) PutNormalized(_ context.Context, target service.VideoUploadTarget, raw []byte, sha string) error {
	s.Lock()
	defer s.Unlock()
	e := s.entries[target.SessionID]
	if e == nil || e.dead {
		return service.ErrVideoUploadConflict
	}
	if len(e.norm) > 0 && crypto.SHA256Hex(string(e.norm)) != sha {
		return service.ErrVideoUploadConflict
	}
	e.norm = append([]byte(nil), raw...)
	return nil
}
func (s *g6HTTPUploadStore) ReadNormalized(_ context.Context, bucket, key string, max int64) ([]byte, error) {
	s.Lock()
	defer s.Unlock()
	for _, e := range s.entries {
		if !e.dead && e.target.NormalizedBucket == bucket && e.target.NormalizedKey == key && len(e.norm) > 0 && int64(len(e.norm)) <= max {
			return append([]byte(nil), e.norm...), nil
		}
	}
	return nil, service.ErrVideoUploadUnavailable
}
func (s *g6HTTPUploadStore) Discard(_ context.Context, target service.VideoUploadTarget) error {
	s.Lock()
	defer s.Unlock()
	e := s.entries[target.SessionID]
	if e == nil {
		e = &g6HTTPUploadEntry{target: target}
		s.entries[target.SessionID] = e
	}
	e.dead = true
	e.raw = nil
	e.norm = nil
	return nil
}

func TestVideoG6UploadHTTPMySQLRoundtripAndI2V(t *testing.T) {
	dsn := os.Getenv("MOLIN_VIDEO_G6_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置G6临时MySQL")
	}
	cfg, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("隔离DSN无效")
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || cfg.DBName != "molin_video_g6_contract" || cfg.Net != "tcp" || port != "3306" || (host != "mysql" && host != "127.0.0.1") || os.Getenv("MOLIN_VIDEO_G6_ISOLATED") != "YES" {
		t.Fatal("只允许本轮隔离数据库")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("隔离数据库不可用")
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	pool.SetMaxOpenConns(20)
	pool.SetMaxIdleConns(20)
	secret := func() []byte {
		t.Helper()
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	hmacSecret, jwtSecret := hex.EncodeToString(secret()), hex.EncodeToString(secret())
	rawSK := "sk-molin-" + hex.EncodeToString(secret())
	const modelCode = "molin/video-g6-upload-http"
	for _, sql := range []string{
		"INSERT INTO users(id,password_hash,status,real_name_status) VALUES(996900,'fixture','active','verified')",
		"INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(996900,996900,'上传HTTP隔离项目','active','disabled','UTC')",
		"INSERT INTO wallets(user_id,balance_amount,frozen_amount,currency) VALUES(996900,10,0,'CNY')",
		"INSERT INTO token_models(id,logical_model_code,display_name,modality,status,capabilities_json,release_version_no,published_at) VALUES(996900,'molin/video-g6-upload-http','合成图生HTTP模型','video','active',JSON_ARRAY('video.generate'),1,UTC_TIMESTAMP())",
		`INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) VALUES(996900,1,'active','{"logical_model_code":"molin/video-g6-upload-http","modality":"video","capabilities":["video.generate"],"visible_scope":"all","video_contract":{"schema_version":1,"purpose":"non_commercial_test_fixture","supported_operations":["text_to_video","image_to_video"],"default_model":false,"asset_required":false,"required_entitlement_type":null,"required_membership_levels":[]}}','隔离测试',996900,UTC_TIMESTAMP())`,
		"INSERT INTO ai_project_model_capability_grants(user_id,project_id,logical_model_code,capability,status,granted_by,created_at,updated_at) VALUES(996900,996900,'molin/video-g6-upload-http','video.generate','active',996900,UTC_TIMESTAMP(),UTC_TIMESTAMP())",
		"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT 996900,id,code,'allow' FROM permissions WHERE code='video:generate'",
	} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status,video_generate_allowed) VALUES(996900,996900,996900,'g6',?,'合成HTTP凭据','postpaid','allowlist','active',1)", crypto.HMAC256(rawSK, hmacSecret)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(996900,996900,996900,?)", modelCode).Error; err != nil {
		t.Fatal(err)
	}
	variants := []service.VideoPriceVariant{{Operation: "text_to_video", Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24}, {Operation: "image_to_video", Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24}}
	limits, _ := json.Marshal(service.VideoPricingLimits{MeterType: "video_seconds", Variants: variants})
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.Exec("INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by) VALUES(996900,?,'video.generate','video_seconds',1,'CNY',1,'active',0.2,NULL,NULL,?,0.1,'non_commercial_test_fixture','upload-http-fixture','non_commercial_test_fixture','confirmed_usage','ceil_8',?,?,?,996900)", modelCode, string(limits), now, now.Add(time.Hour), now).Error; err != nil {
		t.Fatal(err)
	}
	for _, v := range variants {
		raw, sha, err := service.CanonicalVideoPriceVariant(v)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.AIPriceSKU{PriceVersionID: 996900, MeterType: "video_seconds", VariantJSON: raw, VariantHash: sha, CostUnitPrice: decimal.RequireFromString("0.04"), SaleUnitPrice: decimal.RequireFromString("0.10"), Scale: decimal.NewFromInt(1), Currency: "CNY"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	policyBody := "合成图生HTTP条款，不构成真实法律同意。"
	if err := db.Exec("INSERT INTO ai_video_rights_policies(policy_version,purpose,title,body,body_sha256,status,effective_at,expires_at,acceptance_ttl_seconds,version_no) VALUES('rights-g6-upload-http-v1','non_commercial_test_fixture','合成条款',?,?,'active',?,?,300,1)", policyBody, crypto.SHA256Hex(policyBody), now.Add(-time.Hour), now.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	defer db.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE policy_version='rights-g6-upload-http-v1' AND status='active'")
	store := &g6HTTPUploadStore{entries: map[string]*g6HTTPUploadEntry{}}
	store.server = httptest.NewServer(store)
	defer store.server.Close()
	protector, err := service.NewVideoTaskPayloadProtector("g6-upload-http", secret())
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoHTTPService(db, service.VideoBillingOptions{QuoteSecret: secret(), PromptSecret: secret(), IntentSecret: secret(), Protector: protector, Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), nil)}, service.VideoHTTPOptions{Uploads: &service.VideoUploadOptions{Store: store, SourceBucket: "g6-upload-raw", NormalizedBucket: "g6-upload-norm", ModerationPolicyVersion: "g6-upload-http-v1", MaxUserReservedBytes: 128 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	keys := authservice.NewAPIKeyService(authrepo.NewAPIKeyRepository(db), hmacSecret, nil)
	jwtAuth, err := service.NewVideoJWTAuthenticator(db, jwtSecret, &videoG6RevocationMemory{values: map[string]bool{}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, app, keys, true, jwtAuth)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	jwt, err := pkgjwt.Generate(996900, "", jwtSecret, 3600)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, key, credential string, body any, target any, wantError ...string) int {
		t.Helper()
		var encoded []byte
		if body != nil {
			encoded, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req, err := http.NewRequest(method, server.URL+path, bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+credential)
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("回环HTTP请求失败")
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"\"bucket\"", "\"object_key\"", "\"normalized_key\"", "\"key_hash\""} {
			if bytes.Contains(raw, []byte(field)) {
				t.Fatal("响应泄露内部字段")
			}
		}
		if res.StatusCode < 300 {
			var envelope struct {
				Code      int             `json:"code"`
				Data      json.RawMessage `json:"data"`
				RequestID string          `json:"request_id"`
			}
			if json.Unmarshal(raw, &envelope) != nil || envelope.Code != 0 || envelope.RequestID == "" {
				t.Fatal("平台Envelope错误")
			}
			if upload, ok := target.(*service.VideoUploadReply); ok {
				// 每次从空对象解码，并独立核对必需键；缺字段不能沿用上次成功响应。
				*upload = service.VideoUploadReply{}
				var fields map[string]json.RawMessage
				if json.Unmarshal(envelope.Data, &fields) != nil || len(fields) != 8 {
					t.Fatal("上传DTO字段集合错误")
				}
				for _, name := range []string{"session_id", "status", "expires_at", "version_no", "input_asset_id", "upload", "cleanup_pending", "idempotent"} {
					value, exists := fields[name]
					if !exists || (name != "input_asset_id" && name != "upload" && bytes.Equal(value, []byte("null"))) {
						t.Fatalf("上传DTO必需字段缺失或错误null：%s", name)
					}
				}
			}
			if target != nil && json.Unmarshal(envelope.Data, target) != nil {
				t.Fatal("DTO解码失败")
			}
		} else {
			var failure struct {
				Code      int             `json:"code"`
				Error     string          `json:"error"`
				RequestID string          `json:"request_id"`
				Data      json.RawMessage `json:"data"`
			}
			codes := map[string]int{"video_input_not_found": 40400, "invalid_request_error": 40000, "video_upload_conflict": 40900}
			if len(wantError) != 1 || codes[wantError[0]] == 0 || json.Unmarshal(raw, &failure) != nil || failure.Code != codes[wantError[0]] || failure.Error != wantError[0] || failure.RequestID == "" || !bytes.Equal(failure.Data, []byte("null")) {
				t.Fatalf("HTTP错误必须符合数字码、稳定类型、追踪ID及null数据合同：status=%d", res.StatusCode)
			}
		}
		return res.StatusCode
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 640, 640))); err != nil {
		t.Fatal(err)
	}
	createBody := map[string]any{"filename": "reference.png", "mime_type": "image/png", "size_bytes": len(raw.Bytes()), "sha256": crypto.SHA256Hex(raw.String())}
	var upload service.VideoUploadReply
	if call("POST", "/api/token/video-inputs/upload-sessions", "upload-http-create-0001", rawSK, createBody, &upload) != 201 || upload.Upload == nil || upload.Status != "uploading" {
		t.Fatal("HTTP创建上传会话失败")
	}
	path := "/api/token/video-inputs/upload-sessions/" + upload.SessionID
	if call("GET", path, "", jwt, nil, nil, "video_input_not_found") != 404 {
		t.Fatal("JWT不能读取另一Key会话")
	}
	put := func(grant *service.VideoUploadGrant) int {
		t.Helper()
		if !strings.HasPrefix(grant.URL, store.server.URL+"/") {
			t.Fatal("禁止非本轮回环存储URL")
		}
		req, _ := http.NewRequest("PUT", grant.URL, bytes.NewReader(raw.Bytes()))
		for k, v := range grant.Headers {
			req.Header.Set(k, v)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("隔离对象上传失败")
		}
		res.Body.Close()
		return res.StatusCode
	}
	if put(upload.Upload) != 204 {
		t.Fatal("对象HTTP PUT失败")
	}
	var completed service.VideoUploadReply
	if call("POST", path+"/complete", "upload-http-complete-0001", rawSK, map[string]any{}, &completed) != 200 || completed.InputAssetID == nil || completed.Status != "completed" {
		t.Fatal("HTTP完成封存失败")
	}
	inputID := *completed.InputAssetID
	var sourceImages service.VideoSourceImagePage
	if call("GET", "/api/token/video-input-source-images?page=1&page_size=20", "", rawSK, nil, &sourceImages) != 200 || sourceImages.Items == nil || len(sourceImages.Items) != 0 || sourceImages.Total != 0 {
		t.Fatal("没有已结算来源图时必须返回D-95空数组和零计数")
	}
	var inputDetail struct {
		InputAssetID   string  `json:"input_asset_id"`
		LifecycleState string  `json:"lifecycle_state"`
		Width          *uint32 `json:"width"`
		CanReference   bool    `json:"can_reference"`
	}
	if call("GET", "/api/token/video-inputs/"+inputID, "", rawSK, nil, &inputDetail) != 200 || inputDetail.InputAssetID != inputID || inputDetail.LifecycleState != "ready" || inputDetail.Width == nil || *inputDetail.Width != 640 || !inputDetail.CanReference {
		t.Fatal("原上传主体必须可查询规范化输入元数据")
	}
	var inputList struct {
		Items []struct {
			InputAssetID string `json:"input_asset_id"`
		} `json:"items"`
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
		Total    int64 `json:"total"`
	}
	if call("GET", "/api/token/video-inputs?page=1&page_size=1", "", rawSK, nil, &inputList) != 200 || inputList.Page != 1 || inputList.PageSize != 1 || inputList.Total != 1 || len(inputList.Items) != 1 || inputList.Items[0].InputAssetID != inputID {
		t.Fatal("输入列表必须使用归属受限D-95分页")
	}
	if call("GET", "/api/token/video-inputs/"+inputID, "", jwt, nil, nil, "video_input_not_found") != 404 {
		t.Fatal("JWT不能读取Key来源输入")
	}
	if call("GET", "/api/token/video-inputs?page_size=101", "", rawSK, nil, nil, "invalid_request_error") != 400 {
		t.Fatal("输入列表不能静默放宽分页上限")
	}
	if call("POST", path+"/complete", "upload-http-complete-0001", rawSK, map[string]any{}, &completed) != 200 || !completed.Idempotent || completed.InputAssetID == nil || *completed.InputAssetID != inputID || completed.Upload != nil {
		t.Fatal("完成重放不一致")
	}
	if put(upload.Upload) != 409 {
		t.Fatal("旧上传能力没有失效")
	}
	if call("POST", path+"/complete", "upload-http-forgery-0001", rawSK, map[string]any{"bucket": "forged"}, nil, "invalid_request_error") != 400 {
		t.Fatal("客户端不得提供对象位置")
	}
	if call("DELETE", path, "upload-http-cancel-done-0001", rawSK, nil, nil, "video_upload_conflict") != 409 {
		t.Fatal("已发布输入不能通过取消会话删除")
	}
	var unused service.VideoUploadReply
	if call("POST", "/api/token/video-inputs/upload-sessions", "upload-http-create-cancel-0001", rawSK, createBody, &unused) != 201 {
		t.Fatal("取消夹具创建失败")
	}
	if call("DELETE", "/api/token/video-inputs/upload-sessions/"+unused.SessionID, "upload-http-cancel-0001", rawSK, nil, &completed) != 200 || completed.Status != "cancelled" || completed.CleanupPending || completed.InputAssetID != nil || completed.Upload != nil {
		t.Fatal("取消与清理未闭合")
	}
	if put(unused.Upload) != 409 {
		t.Fatal("取消后旧能力可复活对象")
	}
	// JWT自己的无Key输入可查询，但不能借此看到同Project下SK创建的输入或数量。
	jwtCreateBody := map[string]any{"project_id": 996900, "filename": "reference.png", "mime_type": "image/png", "size_bytes": len(raw.Bytes()), "sha256": crypto.SHA256Hex(raw.String())}
	var jwtUpload service.VideoUploadReply
	if call("POST", "/api/token/video-inputs/upload-sessions", "upload-http-jwt-create-0001", jwt, jwtCreateBody, &jwtUpload) != 201 || jwtUpload.Upload == nil {
		t.Fatal("JWT上传会话创建失败")
	}
	if put(jwtUpload.Upload) != 204 {
		t.Fatal("JWT对象PUT失败")
	}
	var jwtCompleted service.VideoUploadReply
	if call("POST", "/api/token/video-inputs/upload-sessions/"+jwtUpload.SessionID+"/complete", "upload-http-jwt-complete-0001", jwt, map[string]any{}, &jwtCompleted) != 200 || jwtCompleted.InputAssetID == nil {
		t.Fatal("JWT上传完成失败")
	}
	var jwtInput service.VideoInputDetails
	if call("GET", "/api/token/video-inputs/"+*jwtCompleted.InputAssetID, "", jwt, nil, &jwtInput) != 200 || jwtInput.InputAssetID != *jwtCompleted.InputAssetID || !jwtInput.CanReference {
		t.Fatal("JWT必须能够派生自己的Project并读取输入")
	}
	var jwtInputs service.VideoInputPage
	if call("GET", "/api/token/video-inputs?project_id=996900", "", jwt, nil, &jwtInputs) != 200 || jwtInputs.Total != 1 || len(jwtInputs.Items) != 1 || jwtInputs.Items[0].InputAssetID != jwtInput.InputAssetID {
		t.Fatal("JWT列表及total不能包含SK来源输入")
	}
	if call("GET", "/api/token/video-inputs/"+jwtInput.InputAssetID, "", rawSK, nil, nil, "video_input_not_found") != 404 {
		t.Fatal("SK不能读取JWT来源输入")
	}
	for _, table := range []string{"ai_requests", "ai_gateway_quotes", "ai_gateway_tasks", "wallet_holds"} {
		var n int64
		if err := db.Table(table).Where("user_id=996900").Count(&n).Error; err != nil || n != 0 {
			t.Fatal("上传不得生成财务或任务事实")
		}
	}
	if call("POST", "/api/token/projects/996900/video-rights-acceptance", "upload-http-rights-0001", jwt, map[string]any{"rights_policy_version": "rights-g6-upload-http-v1", "rights_confirmed": true}, nil) != 201 {
		t.Fatal("所有者接受失败")
	}
	i2v := map[string]any{"model": modelCode, "prompt": "合成上传后图生视频", "operation": "image_to_video", "input_asset_id": inputID, "rights_attestation": true}
	var quote service.VideoHTTPQuote
	if call("POST", "/api/token/videos/quotes", "upload-http-i2v-quote-0001", rawSK, i2v, &quote) != 201 || quote.QuotedAmount != "0.50000000" {
		t.Fatal("真实上传输入未进入I2V报价")
	}
	i2v["quote_id"] = quote.QuoteID
	var generation service.VideoHTTPGeneration
	if call("POST", "/api/token/videos/generations", "upload-http-i2v-create-0001", rawSK, i2v, &generation) != 202 || generation.Job.Status != "queued" || generation.HeldAmount != "0.50000000" {
		t.Fatal("真实HTTP I2V未进入原G5预占")
	}
	delete(i2v, "quote_id")
	delete(i2v, "rights_attestation")
	i2v["project_id"] = 996900
	i2v["rights_confirmed"] = true
	i2v["rights_policy_version"] = "rights-g6-upload-http-v1"
	if call("POST", "/api/token/videos/quotes", "upload-http-cross-key-0001", jwt, i2v, nil, "video_input_not_found") != 404 {
		t.Fatal("更换JWT不得引用另一Key上传的输入")
	}
	for _, table := range []string{"ai_requests", "ai_gateway_quotes", "ai_gateway_tasks", "wallet_holds"} {
		var n int64
		if err := db.Table(table).Where("user_id=996900").Count(&n).Error; err != nil || n != 1 {
			t.Fatal("I2V必须形成一组原财务/任务事实")
		}
	}
}
