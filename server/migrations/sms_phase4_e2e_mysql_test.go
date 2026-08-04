package migrations_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"

	"molin/server/internal/config"
	"molin/server/internal/middleware"
	auditrepo "molin/server/internal/modules/audit/repository"
	auditsvc "molin/server/internal/modules/audit/service"
	authroute "molin/server/internal/modules/auth"
	authdto "molin/server/internal/modules/auth/dto"
	authmodel "molin/server/internal/modules/auth/model"
	authrepo "molin/server/internal/modules/auth/repository"
	authsvc "molin/server/internal/modules/auth/service"
	smsrepo "molin/server/internal/modules/sms/repository"
	"molin/server/internal/modules/sms/sender"
	smssvc "molin/server/internal/modules/sms/service"
	pkgcrypto "molin/server/pkg/crypto"
)

type phase4EmailTargetKeyer struct{ hash string }

func (k phase4EmailTargetKeyer) TargetKey(string) (string, error) { return k.hash, nil }

type phase4IAMChecker struct{ allow bool }

func (c *phase4IAMChecker) CheckPermission(context.Context, uint64, string) bool { return c.allow }

type phase4HTTPEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// TestSMSPhase4FiveSceneBusinessE2E 使用真实 MySQL 8、真实仓储与 AuthService，外部阿里云边界使用 Mock Sender。
// 测试覆盖五个业务入口的发码、哈希落库、发送日志、单次消费、重放拒绝和最终业务状态。
func TestSMSPhase4FiveSceneBusinessE2E(t *testing.T) {
	if os.Getenv("SMS_REDIS_INTEGRATION_TEST") != "1" {
		t.Skip("未开启 SMS_REDIS_INTEGRATION_TEST，跳过阶段 4 MySQL/Redis 业务 E2E")
	}
	dsn := strings.TrimSpace(os.Getenv("SMS_MIGRATION_TEST_DSN"))
	if dsn == "" {
		t.Skip("未提供 SMS_MIGRATION_TEST_DSN，跳过阶段 4 MySQL 业务 E2E")
	}
	dsnConfig, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("阶段 4 测试 DSN 不合法: %v", err)
	}
	if !strings.HasPrefix(dsnConfig.DBName, "molin_sms_test_") {
		t.Fatal("拒绝在非 molin_sms_test_ 隔离数据库执行阶段 4 E2E")
	}
	dsnConfig.MultiStatements = true
	dsnConfig.ParseTime = true

	sqlDB, err := sql.Open("mysql", dsnConfig.FormatDSN())
	if err != nil {
		t.Fatalf("打开阶段 4 隔离数据库失败: %v", err)
	}
	defer sqlDB.Close()
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("连接阶段 4 隔离数据库失败: %v", err)
	}
	assertMySQL8(t, sqlDB)
	resetSMSPhase2IsolatedSchema(t, sqlDB)
	defer resetSMSPhase2IsolatedSchema(t, sqlDB)
	applySMSMigrationsThroughPhase2(t, sqlDB)

	gormDB, err := gorm.Open(gormmysql.Open(dsnConfig.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开阶段 4 GORM 隔离连接失败: %v", err)
	}
	redisAddr := strings.TrimSpace(os.Getenv("REDIS_ADDR"))
	if redisAddr == "" {
		t.Fatal("阶段 4 业务 E2E 缺少隔离 REDIS_ADDR")
	}
	redisClient := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer redisClient.Close()
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("连接阶段 4 隔离 Redis 失败: %v", err)
	}
	assertPhase4IPRateLimit(t, redisClient)
	cleanupPhase4Redis(t, redisClient)
	defer cleanupPhase4Redis(t, redisClient)

	const signName = "阶段4固定测试签名"
	insertPhase4Templates(t, sqlDB, signName)
	authVerificationRepo := authrepo.NewVerificationRepository(gormDB)
	verifySvc := authsvc.NewVerificationService(authVerificationRepo)
	verifySvc.SetEmailTargetKeyer(phase4EmailTargetKeyer{hash: strings.Repeat("e", 64)})
	otpGuard := smssvc.NewRedisOTPGuard(redisClient, strings.Repeat("h", 32))
	verifySvc.SetSMSVerificationGuard(otpGuard)
	mockSender := sender.NewMockSender(sender.Result{ProviderRequestID: "phase4-provider-request", ProviderCode: "OK"}, nil)
	smsDispatcher := smssvc.NewDispatcher(phase4SMSConfig(signName), smsrepo.NewSMSRepository(gormDB), mockSender)
	verifySvc.SetSMSDispatcher(smsDispatcher)

	userRepo := authrepo.NewUserRepository(gormDB)
	sessionRepo := authrepo.NewSessionRepository(gormDB)
	loginLogRepo := authrepo.NewLoginLogRepository(gormDB)
	auditService := auditsvc.NewAuditService(auditrepo.NewAuditLogRepository(gormDB))
	phase4Config := config.Config{
		JWTSecret: strings.Repeat("j", 32), JWTExpireSeconds: 7200,
		RefreshTokenSecret: strings.Repeat("r", 32), RefreshTokenExpireDays: 30,
		AdminVerifyExpireHours: 24,
	}
	authService := authsvc.NewAuthService(userRepo, sessionRepo, verifySvc, loginLogRepo, phase4Config, redisClient, auditService, nil, gormDB)
	mux := http.NewServeMux()
	iamChecker := &phase4IAMChecker{allow: true}
	authroute.RegisterRoutes(mux, authService, verifySvc, nil, phase4Config, iamChecker, nil, redisClient, middleware.NewPublicSourceIPResolver(nil), nil)

	ctx := context.Background()
	initialPassword := phase4TestPassword(t.Name() + "-initial")
	newPassword := phase4TestPassword(t.Name() + "-changed")
	registerPhone := phase4TestPhone(1)
	registerCode := sendPhase4PhoneCodeHTTP(t, mux, mockSender, registerPhone, "register", "")
	assertPhase4PhoneSendRateLimitedHTTP(t, mux, registerPhone, "register")
	emailCode := phase4TestCode(t.Name() + "-email")
	createPhase4AcceptedEmailCode(t, ctx, authVerificationRepo, strings.Repeat("e", 64), "register", emailCode)
	registerRequest := authdto.RegisterReq{
		Username: "phase4_user", Phone: registerPhone, Email: "phase4@example.test",
		Password: initialPassword, PhoneCode: registerCode, EmailCode: emailCode,
	}
	wrongRegisterRequest := registerRequest
	wrongRegisterRequest.PhoneCode = phase4WrongCode(registerCode)
	phase4HTTPError(t, mux, http.MethodPost, "/api/auth/register", "", wrongRegisterRequest, http.StatusBadRequest, 40000)
	var registerResult authdto.LoginResp
	phase4HTTPJSON(t, mux, http.MethodPost, "/api/auth/register", "", registerRequest, http.StatusCreated, &registerResult)
	if registerResult.User.ID == 0 || registerResult.AccessToken == "" {
		t.Fatalf("register HTTP 全链路未返回有效用户和令牌: user_present=%v token_present=%v", registerResult.User.ID != 0, registerResult.AccessToken != "")
	}
	userID := registerResult.User.ID
	shortEmail := "a" + "@" + "phase4.invalid"
	phase4HTTPError(t, mux, http.MethodPost, "/api/auth/login/email", "", authdto.LoginEmailReq{Email: shortEmail, Password: initialPassword}, http.StatusNotFound, 40404)
	phase4HTTPError(t, mux, http.MethodPost, "/api/auth/register", "", registerRequest, http.StatusBadRequest, 40000)

	loginCode := sendPhase4PhoneCodeHTTP(t, mux, mockSender, registerPhone, "login", "")
	loginRequest := authdto.LoginPhoneReq{Phone: registerPhone, Code: loginCode}
	wrongLoginRequest := loginRequest
	wrongLoginRequest.Code = phase4WrongCode(loginCode)
	phase4HTTPError(t, mux, http.MethodPost, "/api/auth/login/phone", "", wrongLoginRequest, http.StatusBadRequest, 40000)
	var loginResult authdto.LoginResp
	phase4HTTPJSON(t, mux, http.MethodPost, "/api/auth/login/phone", "", loginRequest, http.StatusOK, &loginResult)
	if loginResult.AccessToken == "" {
		t.Fatal("login HTTP 全链路未返回访问令牌")
	}
	phase4HTTPError(t, mux, http.MethodPost, "/api/auth/login/phone", "", loginRequest, http.StatusBadRequest, 40000)

	newPhone := phase4TestPhone(2)
	phase4HTTPError(t, mux, http.MethodPost, "/api/me/verification-codes/phone", "", authdto.SendBindPhoneCodeReq{Phone: newPhone}, http.StatusUnauthorized, 40001)
	bindCode := sendPhase4PhoneCodeHTTP(t, mux, mockSender, newPhone, "bind_phone", loginResult.AccessToken)
	bindRequest := authdto.UpdatePhoneReq{Phone: newPhone, Code: bindCode}
	wrongBindRequest := bindRequest
	wrongBindRequest.Code = phase4WrongCode(bindCode)
	phase4HTTPError(t, mux, http.MethodPatch, "/api/me/phone", loginResult.AccessToken, wrongBindRequest, http.StatusBadRequest, 40000)
	phase4HTTPJSON(t, mux, http.MethodPatch, "/api/me/phone", loginResult.AccessToken, bindRequest, http.StatusOK, nil)
	phase4HTTPError(t, mux, http.MethodPatch, "/api/me/phone", loginResult.AccessToken, bindRequest, http.StatusBadRequest, 40000)
	assertPhase4PhoneChanged(t, sqlDB, userID, newPhone)

	iamChecker.allow = false
	phase4HTTPError(t, mux, http.MethodPost, "/api/admin/auth/verification-codes/phone", loginResult.AccessToken, nil, http.StatusForbidden, 40003)
	iamChecker.allow = true
	adminCode := sendPhase4PhoneCodeHTTP(t, mux, mockSender, newPhone, "admin_verify", loginResult.AccessToken)
	adminRequest := authdto.AdminVerifyReq{Code: adminCode}
	wrongAdminRequest := adminRequest
	wrongAdminRequest.Code = phase4WrongCode(adminCode)
	phase4HTTPError(t, mux, http.MethodPost, "/api/admin/auth/verify-phone", loginResult.AccessToken, wrongAdminRequest, http.StatusBadRequest, 40000)
	phase4HTTPJSON(t, mux, http.MethodPost, "/api/admin/auth/verify-phone", loginResult.AccessToken, adminRequest, http.StatusOK, nil)
	phase4HTTPError(t, mux, http.MethodPost, "/api/admin/auth/verify-phone", loginResult.AccessToken, adminRequest, http.StatusBadRequest, 40000)
	assertPhase4AdminPhoneVerified(t, sqlDB, userID)

	resetCode := sendPhase4PhoneCodeHTTP(t, mux, mockSender, newPhone, "reset_password", "")
	resetRequest := authdto.ResetPasswordReq{
		Target: newPhone, TargetType: "phone", Code: resetCode, NewPassword: newPassword,
	}
	wrongResetRequest := resetRequest
	wrongResetRequest.Code = phase4WrongCode(resetCode)
	phase4HTTPError(t, mux, http.MethodPost, "/api/auth/password/reset", "", wrongResetRequest, http.StatusBadRequest, 40000)
	phase4HTTPJSON(t, mux, http.MethodPost, "/api/auth/password/reset", "", resetRequest, http.StatusOK, nil)
	phase4HTTPError(t, mux, http.MethodPost, "/api/auth/password/reset", "", resetRequest, http.StatusBadRequest, 40000)
	assertPhase4PasswordAndSessions(t, sqlDB, userID, newPassword)

	if mockSender.CallCount() != 5 {
		t.Fatalf("五业务场景必须各调用一次外部 Sender，实际 %d", mockSender.CallCount())
	}
	assertPhase4DatabaseEvidence(t, sqlDB, userID, shortEmail)
	assertPhase4ConcurrentConsumption(t, authVerificationRepo, verifySvc, otpGuard)
}

func assertPhase4IPRateLimit(t *testing.T, client *redis.Client) {
	t.Helper()
	const (
		action = "phase4_sms_send"
		ip     = "192.0.2.44"
	)
	key := "ratelimit:ip:" + ip + ":" + action
	if err := client.Del(context.Background(), key).Err(); err != nil {
		t.Fatalf("清理阶段 4 IP 限流测试键失败: %v", err)
	}
	t.Cleanup(func() { _ = client.Del(context.Background(), key).Err() })

	var passed int
	handler := middleware.RateLimitVerificationByIP(client, middleware.NewPublicSourceIPResolver(nil), action, 10, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		passed++
		w.WriteHeader(http.StatusNoContent)
	}))
	for attempt := 1; attempt <= 11; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/phase4-ip-limit", nil)
		req.RemoteAddr = ip + ":41000"
		// 轮换伪造转发头，验证非可信直连始终只使用 RemoteAddr 形成单一限流桶。
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", attempt))
		req.Header.Set("X-Real-IP", fmt.Sprintf("203.0.113.%d", attempt))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		if attempt <= 10 && recorder.Code != http.StatusNoContent {
			t.Fatalf("同一 IP 前十次请求必须放行: attempt=%d status=%d", attempt, recorder.Code)
		}
		if attempt == 11 && recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("同一 IP 第十一次请求必须返回 429: status=%d", recorder.Code)
		}
	}
	if passed != 10 {
		t.Fatalf("IP 限流不得把第十一次请求传给业务处理器: passed=%d", passed)
	}
}

func applySMSMigrationsThroughPhase2(t *testing.T, db *sql.DB) {
	t.Helper()
	files, err := filepath.Glob("*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, file := range files {
		content, readErr := os.ReadFile(file)
		if readErr != nil {
			t.Fatalf("读取迁移 %s 失败: %v", file, readErr)
		}
		if _, execErr := db.Exec(string(content)); execErr != nil {
			t.Fatalf("阶段 4 完整安装执行 %s 失败: %v", filepath.Base(file), execErr)
		}
		if filepath.Base(file) == "000059_add_sms_phase2_management.up.sql" {
			return
		}
	}
	t.Fatal("未找到 000059_add_sms_phase2_management.up.sql")
}

func insertPhase4Templates(t *testing.T, db *sql.DB, signName string) {
	t.Helper()
	scenes := []string{"register", "login", "reset_password", "bind_phone", "admin_verify"}
	for index, scene := range scenes {
		result, err := db.Exec(`INSERT INTO sms_templates(provider,template_code,template_name,template_type,provider_audit_status,content,variables_json,local_enabled,last_synced_at)
VALUES('aliyun',?,?, 'verification','approved','验证码 ${code}',JSON_ARRAY('code'),1,NOW())`, "SMS_PHASE4_"+strings.ToUpper(scene), "阶段4"+scene)
		if err != nil {
			t.Fatalf("写入 %s 独立模板失败: %v", scene, err)
		}
		templateID, _ := result.LastInsertId()
		if _, err := db.Exec("INSERT INTO sms_scene_bindings(scene,template_id,sign_name,enabled,version) VALUES(?,?,?,1,?)", scene, templateID, signName, index+1); err != nil {
			t.Fatalf("写入 %s 场景绑定失败: %v", scene, err)
		}
	}
}

func phase4SMSConfig(signName string) config.Config {
	return config.Config{
		SMSEnabled: true, SMSProvider: "aliyun", SMSAliyunAccessKeyID: strings.Repeat("i", 16),
		SMSAliyunAccessKeySecret: strings.Repeat("s", 32), SMSAliyunSignName: signName,
		SMSAliyunEndpoint: "dysmsapi.aliyuncs.com", SMSPhoneHMACSecret: strings.Repeat("h", 32),
	}
}

func cleanupPhase4Redis(t *testing.T, client *redis.Client) {
	t.Helper()
	secret := strings.Repeat("h", 32)
	keys := []string{
		"ratelimit:ip:127.0.0.1:send_code",
		"ratelimit:user:1:send_bind_phone_code",
		"ratelimit:user:1:update_phone",
	}
	phoneScenes := []struct{ phone, scene string }{
		{phase4TestPhone(1), "register"}, {phase4TestPhone(1), "login"},
		{phase4TestPhone(2), "bind_phone"}, {phase4TestPhone(2), "admin_verify"},
		{phase4TestPhone(2), "reset_password"}, {phase4TestPhone(3), "login"},
	}
	for _, item := range phoneScenes {
		digest := smssvc.SMSPhoneHMAC(item.phone, secret)
		keys = append(keys, "sms:otp:send:"+item.scene+":"+digest, "sms:otp:failure:"+item.scene+":"+digest)
	}
	if err := client.Del(context.Background(), keys...).Err(); err != nil {
		t.Fatalf("清理阶段 4 隔离 Redis 测试键失败: %v", err)
	}
}

func sendPhase4PhoneCodeHTTP(t *testing.T, mux http.Handler, mock *sender.MockSender, phone, scene, accessToken string) string {
	t.Helper()
	path := "/api/auth/verification-codes/phone"
	body := any(authdto.SendPhoneCodeReq{Phone: phone, Scene: scene})
	if scene == "bind_phone" {
		path = "/api/me/verification-codes/phone"
		body = authdto.SendBindPhoneCodeReq{Phone: phone}
	} else if scene == "admin_verify" {
		path = "/api/admin/auth/verification-codes/phone"
		body = nil
	}
	phase4HTTPJSON(t, mux, http.MethodPost, path, accessToken, body, http.StatusOK, nil)
	var params map[string]string
	if err := json.Unmarshal([]byte(mock.LastRequest().TemplateParamJSON), &params); err != nil || params["code"] == "" {
		t.Fatalf("%s Mock Sender 验证码变量无效: code_present=%v err=%v", scene, params["code"] != "", err)
	}
	return params["code"]
}

func assertPhase4PhoneSendRateLimitedHTTP(t *testing.T, mux http.Handler, phone, scene string) {
	t.Helper()
	phase4HTTPError(t, mux, http.MethodPost, "/api/auth/verification-codes/phone", "", authdto.SendPhoneCodeReq{Phone: phone, Scene: scene}, http.StatusTooManyRequests, 42900)
}

func phase4HTTPJSON(t *testing.T, handler http.Handler, method, path, accessToken string, body any, expectedStatus int, dataOut any) {
	t.Helper()
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化阶段 4 HTTP 请求失败: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.RemoteAddr = "127.0.0.1:41000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "phase4-http-e2e")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	var envelope phase4HTTPEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("%s %s 返回非 JSON 响应: status=%d body_length=%d err=%v", method, path, recorder.Code, recorder.Body.Len(), err)
	}
	if recorder.Code != expectedStatus || envelope.Code != 0 {
		t.Fatalf("%s %s HTTP 验收失败: status=%d code=%d", method, path, recorder.Code, envelope.Code)
	}
	if dataOut != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, dataOut); err != nil {
			t.Fatalf("解析 %s %s data 失败: %v", method, path, err)
		}
	}
}

func phase4HTTPError(t *testing.T, handler http.Handler, method, path, accessToken string, body any, expectedStatus, expectedCode int) {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化阶段 4 负向 HTTP 请求失败: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.RemoteAddr = "127.0.0.1:41000"
	req.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	var envelope phase4HTTPEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("%s %s 负向响应不是 JSON: status=%d body_length=%d", method, path, recorder.Code, recorder.Body.Len())
	}
	if recorder.Code != expectedStatus || envelope.Code != expectedCode {
		t.Fatalf("%s %s 负向 HTTP 契约不符: status=%d code=%d", method, path, recorder.Code, envelope.Code)
	}
}

func createPhase4AcceptedEmailCode(t *testing.T, ctx context.Context, repo *authrepo.VerificationRepository, targetHash, scene, code string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	targetMasked := "p***@example.test"
	if err := repo.Create(ctx, &authmodel.VerificationCode{
		TargetType: "email", TargetHash: &targetHash, TargetMasked: &targetMasked, CodeHash: pkgcrypto.SHA256Hex(code), Scene: scene,
		SendStatus: "accepted", AcceptedAt: &now, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("创建阶段 4 邮箱注册验证码夹具失败: %v", err)
	}
}

func assertPhase4ReplayRejected(t *testing.T, svc *authsvc.VerificationService, phone, scene, code string) {
	t.Helper()
	if err := svc.Check(context.Background(), "phone", phone, scene, code); !errors.Is(err, authsvc.ErrInvalidCode) {
		t.Fatalf("%s 已消费验证码重放必须拒绝，实际 %v", scene, err)
	}
}

func assertPhase4PasswordAndSessions(t *testing.T, db *sql.DB, userID uint64, newPassword string) {
	t.Helper()
	var passwordHash string
	if err := db.QueryRow("SELECT password_hash FROM users WHERE id=?", userID).Scan(&passwordHash); err != nil || !pkgcrypto.CheckPassword(newPassword, passwordHash) {
		t.Fatalf("重置密码后密码哈希不正确: err=%v", err)
	}
	var activeSessions int
	if err := db.QueryRow("SELECT COUNT(*) FROM user_sessions WHERE user_id=? AND revoked_at IS NULL", userID).Scan(&activeSessions); err != nil || activeSessions != 0 {
		t.Fatalf("重置密码后必须吊销全部会话: active=%d err=%v", activeSessions, err)
	}
}

func assertPhase4PhoneChanged(t *testing.T, db *sql.DB, userID uint64, phone string) {
	t.Helper()
	var storedPhone string
	var verified bool
	var adminMFA sql.NullTime
	if err := db.QueryRow("SELECT phone,phone_verified,admin_phone_verified_at FROM users WHERE id=?", userID).Scan(&storedPhone, &verified, &adminMFA); err != nil || storedPhone != phone || !verified || adminMFA.Valid {
		t.Fatalf("换绑手机最终状态错误: phone_match=%v verified=%v admin_mfa_valid=%v err=%v", storedPhone == phone, verified, adminMFA.Valid, err)
	}
}

func assertPhase4AdminPhoneVerified(t *testing.T, db *sql.DB, userID uint64) {
	t.Helper()
	var verifiedAt sql.NullTime
	if err := db.QueryRow("SELECT admin_phone_verified_at FROM users WHERE id=?", userID).Scan(&verifiedAt); err != nil || !verifiedAt.Valid {
		t.Fatalf("管理员手机 MFA 最终状态错误: verified_at=%v err=%v", verifiedAt, err)
	}
}

func assertPhase4DatabaseEvidence(t *testing.T, db *sql.DB, userID uint64, shortEmail string) {
	t.Helper()
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM sms_send_logs WHERE purpose='otp' AND submit_status='accepted'", nil, 5)
	assertSchemaCount(t, db, "SELECT COUNT(DISTINCT template_id) FROM sms_send_logs WHERE purpose='otp' AND submit_status='accepted'", nil, 5)
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM verification_codes WHERE target_type='phone' AND business_request_no IS NOT NULL AND send_status='accepted' AND used_at IS NOT NULL", nil, 5)
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM verification_codes WHERE target_type='phone' AND business_request_no IS NOT NULL AND CHAR_LENGTH(code_hash)=64", nil, 5)
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM sms_send_logs WHERE CHAR_LENGTH(phone_masked)=11 AND SUBSTRING(phone_masked,4,4)='****'", nil, 5)
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM audit_logs WHERE module='auth' AND action IN ('update_phone','admin_verify_phone','reset_password') AND request_summary IS NULL", nil, 3)
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM user_login_logs WHERE user_id=? AND login_type='phone' AND status='success' AND login_account=?", []any{userID, authdto.MaskPhone(phase4TestPhone(1))}, 1)
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM user_login_logs WHERE login_account=?", []any{phase4TestPhone(1)}, 0)
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM user_login_logs WHERE login_type='email' AND status='failed' AND login_account=?", []any{sanitizePhase4EmailForAssertion(shortEmail)}, 1)
	assertSchemaCount(t, db, "SELECT COUNT(*) FROM user_login_logs WHERE login_account=?", []any{shortEmail}, 0)
}

func sanitizePhase4EmailForAssertion(email string) string {
	parts := strings.SplitN(email, "@", 2)
	return parts[0][:1] + "***@" + parts[1]
}

func assertPhase4ConcurrentConsumption(t *testing.T, repo *authrepo.VerificationRepository, svc *authsvc.VerificationService, guard *smssvc.RedisOTPGuard) {
	t.Helper()
	phone := phase4TestPhone(3)
	code := phase4TestCode(t.Name() + "-concurrent")
	now := time.Now().UTC().Truncate(time.Second)
	acceptedAt := now
	if err := repo.Create(context.Background(), &authmodel.VerificationCode{
		TargetType: "phone", TargetValue: stringPointerForPhase4(phone), CodeHash: pkgcrypto.SHA256Hex(code),
		Scene: "login", SendStatus: "accepted", AcceptedAt: &acceptedAt, ExpiresAt: now.Add(10 * time.Minute),
	}); err != nil {
		t.Fatalf("创建并发消费夹具失败: %v", err)
	}

	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			<-start
			results <- svc.Check(context.Background(), "phone", phone, "login", code)
		}()
	}
	close(start)
	successes := 0
	for i := 0; i < workers; i++ {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, authsvc.ErrInvalidCode) {
			t.Fatalf("并发消费返回非预期错误: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("同一验证码并发消费必须恰好一次成功，实际 %d", successes)
	}
	if _, err := svc.SendDetailed(context.Background(), "phone", phone, "login"); err != nil {
		t.Fatalf("并发重放后新验证码受理必须清除旧批次错误次数: %v", err)
	}
	if allowed, err := guard.AllowCheckAttempt(context.Background(), phone, "login"); err != nil || !allowed {
		t.Fatalf("新验证码受理后首次校验必须恢复可用: allowed=%v err=%v", allowed, err)
	}
}

func stringPointerForPhase4(value string) *string { return &value }

// phase4TestPhone 在运行时构造隔离测试号码，避免在源码和提交记录中保存完整手机号。
func phase4TestPhone(sequence int) string {
	return fmt.Sprintf("%s%08d", "138", sequence)
}

// phase4TestCode 在运行时生成六位测试码，测试源码和提交记录不保存固定验证码。
func phase4TestCode(seed string) string {
	return fmt.Sprintf("%06d", crc32.ChecksumIEEE([]byte(seed))%1000000)
}

func phase4WrongCode(correct string) string {
	candidate := phase4TestCode("wrong-" + correct)
	if candidate == correct {
		return phase4TestCode("second-wrong-" + correct)
	}
	return candidate
}

// phase4TestPassword 在运行时生成满足长度要求的测试口令，避免持久化任何可直接使用的口令。
func phase4TestPassword(seed string) string {
	return "T!" + pkgcrypto.SHA256Hex(seed)[:16]
}
