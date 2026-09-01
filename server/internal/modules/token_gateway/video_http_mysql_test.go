package token_gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
	"molin/server/pkg/crypto"
	pkgjwt "molin/server/pkg/jwt"
)

// 仅替换Redis存储边界；真实JWT验签、用户查询和业务权限仍使用生产服务。
type videoG6RevocationMemory struct {
	sync.RWMutex
	values      map[string]bool
	unavailable bool
}

func (s *videoG6RevocationMemory) IsRevoked(_ context.Context, digest string) (bool, error) {
	s.RLock()
	defer s.RUnlock()
	if s.unavailable {
		return false, fmt.Errorf("合成吊销存储故障")
	}
	return s.values[digest], nil
}

// 审核是外部边界；在其返回前切换已发布合同，复现HTTP预检和G5事务之间的真实竞态。
type videoG6PublicationModerator struct {
	video.VideoModerationAdapter
	publish func() error
	once    sync.Once
	err     error
}

func (m *videoG6PublicationModerator) ModeratePrompt(ctx context.Context, prompt string) error {
	m.once.Do(func() { m.err = m.publish() })
	if m.err != nil {
		return m.err
	}
	return m.VideoModerationAdapter.ModeratePrompt(ctx, prompt)
}

func TestVideoG6HTTPMySQLCreateRetrieveReplay(t *testing.T) {
	dsn := os.Getenv("MOLIN_VIDEO_G6_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置G6临时MySQL")
	}
	c, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("隔离DSN错误")
	}
	host, port, err := net.SplitHostPort(c.Addr)
	if err != nil || c.DBName != "molin_video_g6_contract" || c.Net != "tcp" || port != "3306" || (host != "mysql" && host != "127.0.0.1") || os.Getenv("MOLIN_VIDEO_G6_ISOLATED") != "YES" {
		t.Fatal("仅允许本轮临时数据库")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("数据库不可用")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(140)
	sqlDB.SetMaxIdleConns(140)
	secret := func() []byte {
		t.Helper()
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	hmacSecret := hex.EncodeToString(secret())
	rawSK := "sk-molin-" + hex.EncodeToString(secret())
	const code = "molin/video-g6-http"
	for _, q := range []string{
		"INSERT INTO users(id,password_hash,status,real_name_status) VALUES(996300,'fixture','active','verified')",
		"INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(996300,996300,'G6 HTTP','active','disabled','UTC')",
		"INSERT INTO wallets(user_id,balance_amount,frozen_amount,currency) VALUES(996300,10,0,'CNY')",
		"INSERT INTO token_models(id,logical_model_code,display_name,modality,status,capabilities_json,release_version_no,published_at) VALUES(996300,'molin/video-g6-http','合成HTTP模型','video','active',JSON_ARRAY('video.generate'),1,UTC_TIMESTAMP())",
		`INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) VALUES(996300,1,'active','{"logical_model_code":"molin/video-g6-http","modality":"video","capabilities":["video.generate"],"visible_scope":"all","video_contract":{"schema_version":1,"purpose":"non_commercial_test_fixture","supported_operations":["text_to_video","image_to_video"],"default_model":false,"asset_required":false,"required_entitlement_type":null,"required_membership_levels":[]}}','隔离测试',996300,UTC_TIMESTAMP())`,
		"INSERT INTO ai_project_model_capability_grants(user_id,project_id,logical_model_code,capability,status,granted_by,created_at,updated_at) VALUES(996300,996300,'molin/video-g6-http','video.generate','active',996300,UTC_TIMESTAMP(),UTC_TIMESTAMP())",
		"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT 996300,id,code,'allow' FROM permissions WHERE code='video:generate'",
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	if db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status,video_generate_allowed) VALUES(996300,996300,996300,'g6',?,'合成HTTP Key','postpaid','allowlist','active',1)", crypto.HMAC256(rawSK, hmacSecret)).Error != nil {
		t.Fatal("合成Key写入失败")
	}
	if err := db.Exec("INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(996300,996300,996300,?)", code).Error; err != nil {
		t.Fatal(err)
	}
	variants := []service.VideoPriceVariant{{Operation: "text_to_video", Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24}, {Operation: "image_to_video", Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24}}
	limits, _ := json.Marshal(service.VideoPricingLimits{MeterType: "video_seconds", Variants: variants})
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.Exec(`INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by) VALUES(996300,?,'video.generate','video_seconds',1,'CNY',1,'active',0.2,NULL,NULL,?,0.1,'non_commercial_test_fixture','g6-fixture','non_commercial_test_fixture','confirmed_usage','ceil_8',?,?,?,996300)`, code, string(limits), now, now.Add(time.Hour), now).Error; err != nil {
		t.Fatal(err)
	}
	for _, variant := range variants {
		raw, hash, err := service.CanonicalVideoPriceVariant(variant)
		if err != nil {
			t.Fatal(err)
		}
		sku := model.AIPriceSKU{PriceVersionID: 996300, MeterType: "video_seconds", VariantJSON: raw, VariantHash: hash, CostUnitPrice: decimal.RequireFromString("0.04"), SaleUnitPrice: decimal.RequireFromString("0.10"), Scale: decimal.NewFromInt(1), Currency: "CNY"}
		if err := db.Create(&sku).Error; err != nil {
			t.Fatal(err)
		}
	}
	protector, err := service.NewVideoTaskPayloadProtector("g6-fixture", secret())
	if err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoHTTPService(db, service.VideoBillingOptions{QuoteSecret: secret(), PromptSecret: secret(), IntentSecret: secret(), Protector: protector, Safety: video.NewVideoSafetyPipeline(video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), nil)})
	if err != nil {
		t.Fatal(err)
	}
	keys := authservice.NewAPIKeyService(authrepo.NewAPIKeyRepository(db), hmacSecret, nil)
	jwtSecret := hex.EncodeToString(secret())
	revocations := &videoG6RevocationMemory{values: map[string]bool{}}
	jwtAuth, err := service.NewVideoJWTAuthenticator(db, jwtSecret, revocations)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	RegisterVideoUserRoutes(mux, app, keys, true, jwtAuth)
	server := httptest.NewServer(mux)
	defer server.Close()
	create := func(prompt, key string, overrides ...map[string]string) (dto.VideoJob, string, int, error) {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		fields := map[string]string{"model": code, "prompt": prompt, "seconds": "5", "size": "1280x720"}
		for _, override := range overrides {
			for name, value := range override {
				if value == "__OMIT__" {
					delete(fields, name)
				} else {
					fields[name] = value
				}
			}
		}
		for name, value := range fields {
			if err := writer.WriteField(name, value); err != nil {
				return dto.VideoJob{}, "", 0, err
			}
		}
		if err := writer.Close(); err != nil {
			return dto.VideoJob{}, "", 0, err
		}
		req, err := http.NewRequest("POST", server.URL+"/v1/videos", &body)
		if err != nil {
			return dto.VideoJob{}, "", 0, err
		}
		req.Header.Set("Authorization", "Bearer "+rawSK)
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		res, err := server.Client().Do(req)
		if err != nil {
			return dto.VideoJob{}, "", 0, err
		}
		defer res.Body.Close()
		var job dto.VideoJob
		if res.StatusCode == 200 {
			err = json.NewDecoder(res.Body).Decode(&job)
			if res.Header.Get("X-Request-ID") == "" || res.Header.Get("X-Request-ID") == res.Header.Get("X-Molin-Request-ID") {
				err = fmt.Errorf("HTTP和业务追踪ID未分离")
			}
		} else {
			_, _ = io.Copy(io.Discard, res.Body)
		}
		return job, res.Header.Get("X-Molin-Request-ID"), res.StatusCode, err
	}
	// 空账本起步的100并发与后续100重放分别验证，不能用已有任务重放替代首次创建竞争。
	type created struct {
		job    dto.VideoJob
		id     string
		status int
		err    error
	}
	start := make(chan struct{})
	initial := make(chan created, 100)
	for i := 0; i < 100; i++ {
		go func() {
			<-start
			job, id, status, err := create("合成视频合同测试", "video-g6-http-idempotency-0001")
			initial <- created{job, id, status, err}
		}()
	}
	close(start)
	var first dto.VideoJob
	business := ""
	for i := 0; i < 100; i++ {
		got := <-initial
		if got.err != nil || got.status != 200 || got.job.Status != "queued" || got.id == "" {
			t.Fatalf("首次并发创建失败：HTTP=%d error=%v", got.status, got.err)
		}
		if i == 0 {
			first, business = got.job, got.id
		} else if got.job.ID != first.ID || got.id != business {
			t.Fatal("首次并发生成了不同任务")
		}
	}
	results := make(chan error, 100)
	for i := 0; i < 100; i++ {
		go func() {
			job, id, status, err := create("合成视频合同测试", "video-g6-http-idempotency-0001")
			if err == nil && (status != 200 || job.ID != first.ID || id != business) {
				err = fmt.Errorf("重放不一致HTTP=%d", status)
			}
			results <- err
		}()
	}
	for i := 0; i < 100; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	_, _, status, err := create("另一种生成意图", "video-g6-http-idempotency-0001")
	if err != nil || status != 409 {
		t.Fatalf("异意图必须409：%d %v", status, err)
	}
	req, _ := http.NewRequest("GET", server.URL+"/v1/videos/"+first.ID, nil)
	req.Header.Set("Authorization", "Bearer "+rawSK)
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("查询原Job失败")
	}
	for _, field := range []string{"model", "seconds", "size", "prompt"} {
		_, _, status, err := create("合成视频合同测试", "video-g6-http-idempotency-0001", map[string]string{field: ""})
		if err != nil || status != 400 {
			t.Fatalf("显式空%s应拒绝：HTTP=%d error=%v", field, status, err)
		}
	}
	if err := db.Exec("UPDATE ai_model_release_versions SET snapshot_json=JSON_SET(snapshot_json,'$.video_contract.default_model',CAST('true' AS JSON)) WHERE model_id=996300").Error; err != nil {
		t.Fatal(err)
	}
	job, id, status, err := create("合成视频合同测试", "video-g6-http-idempotency-0001", map[string]string{"model": "__OMIT__", "seconds": "__OMIT__", "size": "__OMIT__"})
	if err != nil || status != 200 || job.ID != first.ID || id != business {
		t.Fatalf("省略默认字段应重放原任务：%d %v", status, err)
	}
	if err := db.Exec("UPDATE wallets SET balance_amount=0 WHERE user_id=996300").Error; err != nil {
		t.Fatal(err)
	}
	_, _, status, err = create("合成余额不足测试", "video-g6-http-insufficient-0001")
	if err != nil || status != 402 {
		t.Fatalf("余额不足应402：HTTP=%d error=%v", status, err)
	}
	if err := db.Exec("UPDATE wallets SET balance_amount=9.5 WHERE user_id=996300").Error; err != nil {
		t.Fatal(err)
	}
	t.Run("预检后发布移除T2V不能预占", func(t *testing.T) {
		moderator := &videoG6PublicationModerator{VideoModerationAdapter: video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), publish: func() error {
			return db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec("UPDATE ai_model_release_versions SET status='retired' WHERE model_id=996300 AND version_no=1").Error; err != nil {
					return err
				}
				if err := tx.Exec("INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) SELECT model_id,2,'active',JSON_SET(snapshot_json,'$.video_contract.supported_operations',JSON_ARRAY('image_to_video')),'合成发布竞态',created_by,UTC_TIMESTAMP() FROM ai_model_release_versions WHERE model_id=996300 AND version_no=1").Error; err != nil {
					return err
				}
				return tx.Exec("UPDATE token_models SET release_version_no=2,published_at=UTC_TIMESTAMP() WHERE id=996300").Error
			})
		}}
		competing, err := service.NewVideoHTTPService(db, service.VideoBillingOptions{QuoteSecret: secret(), PromptSecret: secret(), IntentSecret: secret(), Protector: protector, Safety: video.NewVideoSafetyPipeline(moderator, nil)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = competing.Create(context.Background(), service.VideoCommand{Caller: service.VideoCaller{UserID: 996300, APIKeyID: 996300}, IdempotencyKey: "video-g6-publish-race-0001", Model: code, Prompt: "合成发布变更测试", Operation: "text_to_video"})
		if !errors.Is(err, service.ErrVideoOptionUnsupported) {
			t.Errorf("新发布合同移除操作后仍可创建：%v", err)
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("UPDATE ai_model_release_versions SET status='retired' WHERE model_id=996300 AND version_no=2").Error; err != nil {
				return err
			}
			if err := tx.Exec("UPDATE ai_model_release_versions SET status='active' WHERE model_id=996300 AND version_no=1").Error; err != nil {
				return err
			}
			return tx.Exec("UPDATE token_models SET release_version_no=1 WHERE id=996300").Error
		}); err != nil {
			t.Fatal(err)
		}
	})
	for _, table := range []string{"ai_requests", "ai_gateway_quotes", "ai_gateway_tasks", "wallet_holds"} {
		var count int64
		if err := db.Table(table).Where("user_id=996300").Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("%s不是唯一事实：%d %v", table, count, err)
		}
	}
	var balance struct{ BalanceAmount, FrozenAmount decimal.Decimal }
	if err := db.Table("wallets").Where("user_id=996300").Take(&balance).Error; err != nil || balance.BalanceAmount.StringFixed(8) != "9.50000000" || balance.FrozenAmount.StringFixed(8) != "0.50000000" {
		t.Fatal("合成钱包预占不符合固定金额金样")
	}
	postPlatform := func(path, credential, key string, payload map[string]any, expectedError ...string) (int, int, json.RawMessage, error) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, 0, nil, err
		}
		request, err := http.NewRequest("POST", server.URL+path, bytes.NewReader(raw))
		if err != nil {
			return 0, 0, nil, err
		}
		request.Header.Set("Authorization", "Bearer "+credential)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		result, err := server.Client().Do(request)
		if err != nil {
			return 0, 0, nil, err
		}
		defer result.Body.Close()
		var envelope struct {
			Code      int             `json:"code"`
			ErrorType string          `json:"error"`
			Data      json.RawMessage `json:"data"`
			RequestID string          `json:"request_id"`
		}
		err = json.NewDecoder(result.Body).Decode(&envelope)
		if envelope.RequestID == "" {
			return result.StatusCode, envelope.Code, envelope.Data, fmt.Errorf("平台缺少HTTP追踪ID")
		}
		if len(expectedError) == 1 && envelope.ErrorType != expectedError[0] {
			return result.StatusCode, envelope.Code, envelope.Data, fmt.Errorf("平台错误类型不符：got=%s want=%s", envelope.ErrorType, expectedError[0])
		}
		return result.StatusCode, envelope.Code, envelope.Data, err
	}
	var originalQuote string
	if err := db.Table("ai_gateway_quotes").Select("public_id").Where("user_id=996300 AND consumed_request_id=?", business).Scan(&originalQuote).Error; err != nil {
		t.Fatal(err)
	}
	platformBody := map[string]any{"model": code, "prompt": "合成视频合同测试", "operation": "text_to_video", "quote_id": originalQuote}
	httpStatus, numeric, data, err := postPlatform("/api/token/videos/generations", rawSK, "video-g6-http-idempotency-0001", platformBody)
	var platformGeneration service.VideoHTTPGeneration
	if err != nil || httpStatus != 202 || numeric != 0 || json.Unmarshal(data, &platformGeneration) != nil || platformGeneration.RequestID != business || platformGeneration.Job.ID != first.ID {
		t.Fatalf("两门面未返回同一任务：HTTP=%d code=%d err=%v", httpStatus, numeric, err)
	}
	jwt, err := pkgjwt.Generate(996300, "", jwtSecret, 3600)
	if err != nil {
		t.Fatal(err)
	}
	jwtBody := map[string]any{"project_id": 996300, "model": code, "prompt": "合成JWT视频测试", "operation": "text_to_video"}
	httpStatus, numeric, data, err = postPlatform("/api/token/videos/quotes", jwt, "video-g6-jwt-quote-0001", jwtBody)
	var jwtQuote service.VideoHTTPQuote
	if err != nil || httpStatus != 201 || numeric != 0 || json.Unmarshal(data, &jwtQuote) != nil || jwtQuote.QuotedAmount != "0.50000000" {
		t.Fatalf("JWT报价失败：HTTP=%d code=%d err=%v", httpStatus, numeric, err)
	}
	jwtBody["quote_id"] = jwtQuote.QuoteID
	httpStatus, numeric, data, err = postPlatform("/api/token/videos/generations", jwt, "video-g6-jwt-create-0001", jwtBody)
	var jwtGeneration service.VideoHTTPGeneration
	if err != nil || httpStatus != 202 || numeric != 0 || json.Unmarshal(data, &jwtGeneration) != nil || jwtGeneration.Job.ID == "" {
		t.Fatalf("JWT生成失败：HTTP=%d code=%d err=%v", httpStatus, numeric, err)
	}
	// 所有负例经过真实认证、G5事务和HTTP错误转换；不能把业务拒绝伪装成500。
	for _, tc := range []struct {
		name, path, credential, key, prompt, quote, errorType string
		status, numeric                                       int
	}{
		{"同键异报价意图", "/api/token/videos/quotes", jwt, "video-g6-jwt-quote-0001", "不同的合成意图", "", "idempotency_conflict", 409, 40901},
		{"未知报价", "/api/token/videos/generations", jwt, "video-g6-unknown-quote-0001", "合成JWT视频测试", "vid_quote_not_found_fixture", "quote_not_found", 404, 40420},
		{"跨Key报价", "/api/token/videos/generations", rawSK, "video-g6-cross-key-quote-0001", "合成JWT视频测试", jwtQuote.QuoteID, "quote_not_found", 404, 40420},
		{"报价已被其他生成消费", "/api/token/videos/generations", jwt, "video-g6-consumed-quote-0001", "合成JWT视频测试", jwtQuote.QuoteID, "idempotency_conflict", 409, 40901},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"project_id": 996300, "model": code, "prompt": tc.prompt, "operation": "text_to_video"}
			if tc.quote != "" {
				body["quote_id"] = tc.quote
			}
			status, numeric, _, err := postPlatform(tc.path, tc.credential, tc.key, body, tc.errorType)
			if err != nil || status != tc.status || numeric != tc.numeric {
				t.Fatalf("报价拒绝语义错误：HTTP=%d code=%d err=%v", status, numeric, err)
			}
		})
	}
	// 100个首次报价请求同时开始，不能先创建赢家再用重放冒充首次竞争。
	quoteBody := map[string]any{"project_id": 996300, "model": code, "prompt": "合成报价并发测试", "operation": "text_to_video"}
	quoteStart := make(chan struct{})
	type quoteResult struct {
		quote service.VideoHTTPQuote
		err   error
	}
	quoteResults := make(chan quoteResult, 100)
	for i := 0; i < 100; i++ {
		go func() {
			<-quoteStart
			status, numeric, data, err := postPlatform("/api/token/videos/quotes", jwt, "video-g6-quote-concurrent-0001", quoteBody)
			var quote service.VideoHTTPQuote
			if err == nil && (status != 201 || numeric != 0 || json.Unmarshal(data, &quote) != nil || quote.QuoteID == "") {
				err = fmt.Errorf("首次报价竞争HTTP=%d code=%d", status, numeric)
			}
			quoteResults <- quoteResult{quote, err}
		}()
	}
	close(quoteStart)
	concurrentQuoteID := ""
	for i := 0; i < 100; i++ {
		got := <-quoteResults
		if got.err != nil {
			t.Error(got.err)
			continue
		}
		if concurrentQuoteID == "" {
			concurrentQuoteID = got.quote.QuoteID
		} else if got.quote.QuoteID != concurrentQuoteID {
			t.Error("首次报价竞争产生不同Quote")
		}
	}
	if concurrentQuoteID == "" {
		t.Fatal("首次报价无成功结果")
	}
	t.Run("过期报价不能生成", func(t *testing.T) {
		// 只推进本次合成Quote有效期，不删除、消费或替换任何既有账务事实。
		if err := db.Exec("UPDATE ai_gateway_quotes SET expires_at=UTC_TIMESTAMP()-INTERVAL 1 SECOND WHERE public_id=? AND user_id=996300", concurrentQuoteID).Error; err != nil {
			t.Fatal(err)
		}
		body := map[string]any{"project_id": 996300, "model": code, "prompt": "合成报价并发测试", "operation": "text_to_video", "quote_id": concurrentQuoteID}
		status, numeric, _, err := postPlatform("/api/token/videos/generations", jwt, "video-g6-expired-quote-0001", body, "quote_expired")
		if err != nil || status != 409 || numeric != 40920 {
			t.Fatalf("过期报价应稳定冲突：HTTP=%d code=%d err=%v", status, numeric, err)
		}
	})
	request, _ := http.NewRequest("POST", server.URL+"/v1/videos", nil)
	t.Run("权利政策与所有者接受HTTP合同", func(t *testing.T) {
		body := "非商业隔离测试条款，不代表任何真实用户的法律同意。"
		if err := db.Exec("INSERT INTO ai_video_rights_policies(id,policy_version,purpose,title,body,body_sha256,status,effective_at,expires_at,acceptance_ttl_seconds,version_no) VALUES(996300,'rights-g6-http-v1','non_commercial_test_fixture','合成权利政策',?,?,'active',UTC_TIMESTAMP()-INTERVAL 1 HOUR,UTC_TIMESTAMP()+INTERVAL 1 HOUR,300,1)", body, crypto.SHA256Hex(body)).Error; err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := db.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE id=996300 AND status='active'").Error; err != nil {
				t.Error(err)
			}
		}()
		getRights := func(path, credential string, target any) int {
			t.Helper()
			req, _ := http.NewRequest("GET", server.URL+path, nil)
			req.Header.Set("Authorization", "Bearer "+credential)
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode == 200 {
				var envelope struct {
					Code int             `json:"code"`
					Data json.RawMessage `json:"data"`
				}
				if json.NewDecoder(res.Body).Decode(&envelope) != nil || envelope.Code != 0 || json.Unmarshal(envelope.Data, target) != nil {
					t.Fatal("权利响应必须使用平台Envelope")
				}
			}
			return res.StatusCode
		}
		var policy service.VideoRightsPolicy
		if getRights("/api/token/video-rights-policy", jwt, &policy) != 200 || policy.PolicyVersion != "rights-g6-http-v1" || policy.Body != body || policy.Scope != "non_commercial_test_fixture" {
			t.Fatal("政策读取失败")
		}
		path := "/api/token/projects/996300/video-rights-acceptance"
		var receipt service.VideoRightsAcceptance
		if getRights(path, rawSK, &receipt) != 200 || receipt.Valid || receipt.AcceptanceID != nil {
			t.Fatal("未接受不能返回有效同意")
		}
		payload := map[string]any{"rights_policy_version": policy.PolicyVersion, "rights_confirmed": true}
		status, numeric, data, err := postPlatform(path, jwt, "video-g6-jwt-create-0001", payload)
		if err != nil || status != 201 || numeric != 0 || json.Unmarshal(data, &receipt) != nil || !receipt.Valid || receipt.AcceptanceID == nil || receipt.Idempotent {
			t.Fatalf("所有者接受失败：%d/%d %v", status, numeric, err)
		}
		original := receipt
		status, numeric, data, err = postPlatform(path, jwt, "video-g6-jwt-create-0001", payload)
		if err != nil || status != 200 || numeric != 0 || json.Unmarshal(data, &receipt) != nil || !receipt.Idempotent || receipt.AcceptanceID == nil || *receipt.AcceptanceID != *original.AcceptanceID || receipt.ExpiresAt == nil || !receipt.ExpiresAt.Equal(*original.ExpiresAt) {
			t.Fatal("接受重放必须使用原回执与期限")
		}
		if getRights(path, rawSK, &receipt) != 200 || !receipt.Valid {
			t.Fatal("同Project SK应能读取所有者接受结果")
		}
		status, numeric, _, err = postPlatform(path, rawSK, "rights-g6-sk-accept-0001", payload, "video_rights_owner_jwt_required")
		if err != nil || status != 403 || numeric != 40003 {
			t.Fatalf("SK不能代签：%d/%d %v", status, numeric, err)
		}
		payload["accepted_by"] = 996401
		status, _, _, err = postPlatform(path, jwt, "rights-g6-forged-actor-0001", payload, "invalid_request_error")
		if err != nil || status != 400 {
			t.Fatalf("不能指定签署主体：%d %v", status, err)
		}
		if getRights("/api/token/projects/996401/video-rights-acceptance", jwt, &receipt) != 404 {
			t.Fatal("跨Project权利查询应404")
		}
		var count int64
		if err := db.Table("ai_project_video_rights_acceptances").Where("user_id=996300").Count(&count).Error; err != nil || count != 1 {
			t.Fatal("拒绝和重放不得形成第二条接受事实")
		}
		if err := db.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE id=996300").Error; err != nil {
			t.Fatal(err)
		}
		if getRights(path, rawSK, &receipt) != 200 || receipt.Valid || receipt.CurrentPolicyVersion != nil || receipt.AcceptedPolicyVersion == nil || *receipt.AcceptedPolicyVersion != policy.PolicyVersion || receipt.InvalidReason != "policy_unavailable" || receipt.AcceptanceID == nil || *receipt.AcceptanceID != *original.AcceptanceID || !receipt.AcceptedAt.Equal(*original.AcceptedAt) || !receipt.ExpiresAt.Equal(*original.ExpiresAt) {
			t.Fatal("退役后HTTP必须保留原回执并明确无当前政策")
		}
		delete(payload, "accepted_by")
		status, numeric, data, err = postPlatform(path, jwt, "video-g6-jwt-create-0001", payload)
		if err != nil || status != 200 || numeric != 0 || json.Unmarshal(data, &receipt) != nil || receipt.Valid || !receipt.Idempotent || receipt.CurrentPolicyVersion != nil || !receipt.ExpiresAt.Equal(*original.ExpiresAt) {
			t.Fatal("政策退役后原键HTTP重放不能续期")
		}
		status, numeric, _, err = postPlatform(path, jwt, "rights-g6-no-policy-new-0001", payload, "video_rights_unavailable")
		if err != nil || status != 503 || numeric != 50300 {
			t.Fatalf("无政策新键必须拒绝：%d/%d %v", status, numeric, err)
		}
		if getRights("/api/token/video-rights-policy", jwt, &policy) != 503 {
			t.Fatal("无当前政策阅读必须关闭")
		}
		if err := db.Table("ai_project_video_rights_acceptances").Where("user_id=996300").Count(&count).Error; err != nil || count != 1 {
			t.Fatal("政策退役后不能增加接受事实")
		}
		job, id, status, err := create("合成视频合同测试", "video-g6-http-idempotency-0001")
		if err != nil || status != 200 || job.ID != first.ID || id != business {
			t.Fatal("I2V政策不可用不能阻断T2V重放")
		}
	})
	request.Header.Set("Authorization", "Bearer "+jwt)
	denied, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = denied.Body.Close()
	if denied.StatusCode != 401 {
		t.Fatal("兼容门面接受了JWT")
	}
	revocations.Lock()
	revocations.values[crypto.SHA256Hex(jwt)] = true
	revocations.Unlock()
	httpStatus, numeric, _, err = postPlatform("/api/token/videos/generations", jwt, "video-g6-jwt-create-0001", jwtBody)
	if err != nil || httpStatus != 401 || numeric != 40001 {
		t.Fatalf("吊销后JWT重放未拒绝：%d/%d %v", httpStatus, numeric, err)
	}
	revocations.Lock()
	revocations.unavailable = true
	revocations.Unlock()
	httpStatus, numeric, _, err = postPlatform("/api/token/videos/generations", jwt, "video-g6-jwt-create-0001", jwtBody)
	if err != nil || httpStatus != 503 || numeric != 50300 {
		t.Fatalf("吊销依赖故障未失败关闭：%d/%d %v", httpStatus, numeric, err)
	}
	for _, table := range []string{"ai_requests", "ai_gateway_quotes", "ai_gateway_tasks", "wallet_holds"} {
		var count int64
		want := int64(2)
		if table == "ai_gateway_quotes" {
			want = 3
		}
		if err := db.Table(table).Where("user_id=996300").Count(&count).Error; err != nil || count != want {
			t.Fatalf("跨门面/JWT拒绝产生重复%s：%d %v", table, count, err)
		}
	}
	if err := db.Table("wallets").Where("user_id=996300").Take(&balance).Error; err != nil || balance.BalanceAmount.StringFixed(8) != "9.00000000" || balance.FrozenAmount.StringFixed(8) != "1.00000000" {
		t.Fatal("JWT和SK两个独立意图的合成资金不守恒")
	}
	// G6队列容量只统计created/reserved/queued；把不参与列表断言的JWT任务按真实状态机推进到submitting，保留原Hold和归属事实。
	jwtOwner := repository.VideoOwner{UserID: 996300, ProjectID: 996300}
	jwtTask, err := repository.NewVideoTaskRepository(db).FindForOwner(t.Context(), jwtGeneration.Job.ID, jwtOwner)
	if err != nil {
		t.Fatal(err)
	}
	jwtTask, err = repository.NewVideoTaskRepository(db).TransitionExecution(t.Context(), repository.VideoStateTransition{TaskPublicID: jwtTask.PublicID, Owner: jwtOwner, ExpectedVersion: jwtTask.VersionNo, ToStatus: model.AIImageTaskQueued, Progress: 10, EventID: jwtTask.RequestID + "_queue_capacity", Source: "worker", Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.NewVideoTaskRepository(db).TransitionExecution(t.Context(), repository.VideoStateTransition{TaskPublicID: jwtTask.PublicID, Owner: jwtOwner, ExpectedVersion: jwtTask.VersionNo, ToStatus: model.AIImageTaskSubmitting, Progress: 20, EventID: jwtTask.RequestID + "_submitting_capacity", Source: "worker", Now: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	t.Run("兼容列表游标与Key隔离", func(t *testing.T) {
		newer, _, status, err := create("合成列表第二个视频", "video-g6-list-second-0001")
		if err != nil || status != 200 {
			t.Fatalf("列表夹具创建失败：HTTP=%d err=%v", status, err)
		}
		readList := func(query string, expectedError ...string) (dto.VideoList, int, error) {
			req, _ := http.NewRequest("GET", server.URL+"/v1/videos"+query, nil)
			req.Header.Set("Authorization", "Bearer "+rawSK)
			res, err := server.Client().Do(req)
			if err != nil {
				return dto.VideoList{}, 0, err
			}
			defer res.Body.Close()
			var list dto.VideoList
			if res.StatusCode == 200 {
				err = json.NewDecoder(res.Body).Decode(&list)
			} else if len(expectedError) == 1 {
				var failure struct {
					Error struct{ Code, Message string } `json:"error"`
				}
				if json.NewDecoder(res.Body).Decode(&failure) != nil || failure.Error.Code != expectedError[0] || failure.Error.Message == "" {
					err = fmt.Errorf("列表错误码缺失或不符：want=%s", expectedError[0])
				}
			}
			return list, res.StatusCode, err
		}
		// 同秒任务以公开ID打破平局；不根据数据库自增ID推导游标。
		earlier, later := first, newer
		if earlier.CreatedAt > later.CreatedAt || (earlier.CreatedAt == later.CreatedAt && earlier.ID > later.ID) {
			earlier, later = later, earlier
		}
		page, status, err := readList("?limit=1&order=asc")
		if err != nil || status != 200 || page.Object != "list" || len(page.Data) != 1 || page.Data[0].ID != earlier.ID || !page.HasMore || page.FirstID == nil || *page.FirstID != earlier.ID || page.LastID == nil || *page.LastID != earlier.ID {
			t.Fatalf("第一页游标合同错误：HTTP=%d err=%v", status, err)
		}
		page, status, err = readList("?limit=1&order=asc&after=" + earlier.ID)
		if err != nil || status != 200 || len(page.Data) != 1 || page.Data[0].ID != later.ID || page.HasMore {
			t.Fatalf("第二页不能混入同Project其他凭据的任务：HTTP=%d err=%v", status, err)
		}
		page, status, err = readList("?after=" + earlier.ID)
		if err != nil || status != 200 || page.Data == nil || len(page.Data) != 0 || page.HasMore || page.FirstID != nil || page.LastID != nil {
			t.Fatalf("降序尾部空页必须返回空数组和null游标：HTTP=%d err=%v", status, err)
		}
		for _, query := range []string{"?limit=0", "?limit=101", "?limit=+1", "?limit=1&limit=2", "?limit=", "?order=sideways", "?order=", "?after=", "?after=%", "?extra=1"} {
			_, status, err := readList(query)
			if err != nil || status != 400 {
				t.Errorf("非法列表参数应400：query=%s HTTP=%d err=%v", query, status, err)
			}
		}
		var jwtTaskID string
		if err := db.Table("ai_gateway_tasks").Select("public_id").Where("user_id=996300 AND api_key_id IS NULL").Scan(&jwtTaskID).Error; err != nil {
			t.Fatal(err)
		}
		_, status, err = readList("?after=" + jwtTaskID)
		if err != nil || status != 404 {
			t.Fatalf("跨Key cursor必须不泄露存在性：HTTP=%d err=%v", status, err)
		}
		t.Run("无模型授权也必须先校验实名", func(t *testing.T) {
			if err := db.Exec("UPDATE users SET real_name_status='unverified' WHERE id=996300").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec("UPDATE ai_project_model_capability_grants SET status='revoked' WHERE user_id=996300").Error; err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Exec("UPDATE users SET real_name_status='verified' WHERE id=996300").Error; err != nil {
					t.Error(err)
				}
				if err := db.Exec("UPDATE ai_project_model_capability_grants SET status='active' WHERE user_id=996300").Error; err != nil {
					t.Error(err)
				}
			}()
			_, status, err := readList("", "70001")
			if err != nil || status != 400 {
				t.Fatalf("无grant不得改变未实名400合同：HTTP=%d err=%v", status, err)
			}
		})
	})
	if err := db.Exec("UPDATE api_keys SET video_generate_allowed=0 WHERE id=996300").Error; err != nil {
		t.Fatal(err)
	}
	_, _, status, err = create("合成视频合同测试", "video-g6-http-idempotency-0001")
	if err != nil || status != 403 {
		t.Fatalf("撤权后重放应拒绝：%d %v", status, err)
	}
}
