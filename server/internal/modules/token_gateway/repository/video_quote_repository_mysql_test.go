package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/token_gateway/model"
)

func TestVideoQuoteRepositoryMySQLConcurrentCreateAndConsume(t *testing.T) {
	dsn := os.Getenv("MOLIN_VIDEO_G2_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置 VID-G2 隔离 MySQL DSN")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(140)
	sqlDB.SetMaxIdleConns(140)
	// 先注册连接池关闭，后注册的夹具清理会按LIFO先执行，避免连接泄漏污染同库后续100并发测试。
	t.Cleanup(func() { _ = sqlDB.Close() })

	const (
		userID      = uint64(93001)
		projectID   = uint64(93001)
		apiKeyID    = uint64(93001)
		priceID     = uint64(93001)
		modelCode   = "molin/video-g2-mysql"
		fingerprint = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		variantHash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	cleanupVideoQuoteMySQLFixture(t, db, userID, projectID, apiKeyID, priceID, modelCode)
	t.Cleanup(func() { cleanupVideoQuoteMySQLFixture(t, db, userID, projectID, apiKeyID, priceID, modelCode) })
	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", userID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(?,?,?,'active','disabled','Asia/Shanghai')", projectID, userID, "视频G2隔离项目").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status) VALUES(?,?,?,'fixture','fixture-video-g2-hash','视频隔离密钥','postpaid','','allowlist','active')", apiKeyID, userID, projectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(?,?,?,?,?)", priceID, modelCode, "视频G2隔离模型", "video", "inactive").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	limits := json.RawMessage(`{"meter_type":"video_seconds","variants":[{"operation":"text_to_video","resolution":"1280x720","duration_seconds":5,"aspect_ratio":"16:9","frame_rate":24,"audio":false},{"operation":"image_to_video","resolution":"1280x720","duration_seconds":5,"aspect_ratio":"16:9","frame_rate":24,"audio":false}]}`)
	if err := db.Exec(`INSERT INTO ai_price_versions(
id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,
max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,
failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by)
VALUES(?,?,'video.generate','video_seconds',1,'CNY',1,'active',0.2,NULL,NULL,?,0.1,
'non_commercial_test_fixture','vid-g2-mysql','non_commercial_test_fixture','confirmed_usage','ceil_8',?,?,?,?)`,
		priceID, modelCode, limits, now.Add(-time.Hour), now.Add(time.Hour), now.Add(-time.Hour), userID).Error; err != nil {
		t.Fatal(err)
	}

	repo := NewVideoQuoteRepository(db)
	commandKind := "quote"
	idempotencyKey := "vid-g2-concurrent-create"
	operation := model.AIVideoOperationTextToVideo
	var created atomic.Int64
	var replayed atomic.Int64
	var createGroup sync.WaitGroup
	for index := 0; index < 100; index++ {
		createGroup.Add(1)
		go func(index int) {
			defer createGroup.Done()
			quote := &model.AIGatewayQuote{
				PublicID: fmt.Sprintf("vid_quote_create_%03d", index), UserID: userID, ProjectID: projectID, APIKeyID: uint64Ptr(apiKeyID),
				LogicalModelCode: modelCode, Capability: model.AIVideoCapability, Operation: &operation, CommandKind: &commandKind, IdempotencyKey: &idempotencyKey,
				RequestFingerprint: fingerprint, RequestVariantHash: variantHash, PriceVersionID: priceID,
				PriceSnapshotJSON: json.RawMessage(`{"schema_version":3}`), QuotedAmount: decimal.RequireFromString("0.50000000"),
				Currency: "CNY", ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now,
			}
			_, existing, createErr := repo.CreateIdempotent(context.Background(), quote)
			switch {
			case createErr == nil && !existing:
				created.Add(1)
			case createErr == nil && existing:
				replayed.Add(1)
			default:
				t.Errorf("并发创建Quote异常: index=%d err=%v", index, createErr)
			}
		}(index)
	}
	createGroup.Wait()
	if created.Load() != 1 || replayed.Load() != 99 {
		t.Fatalf("100并发幂等创建必须一条新事实: created=%d replayed=%d", created.Load(), replayed.Load())
	}
	var quote model.AIGatewayQuote
	if err := db.Where("user_id=? AND project_id=? AND command_kind=? AND idempotency_key=?", userID, projectID, commandKind, idempotencyKey).First(&quote).Error; err != nil {
		t.Fatal(err)
	}

	requestIDs := make([]string, 100)
	for index := range requestIDs {
		requestIDs[index] = fmt.Sprintf("vid-g2-concurrent-%03d", index)
		if err := db.Exec(`INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,modality,capability,operation,delivery_status,is_stream)
VALUES(?,?,?,?,?,'video','video.generate','text_to_video','pending',0)`, requestIDs[index], userID, projectID, apiKeyID, modelCode).Error; err != nil {
			t.Fatal(err)
		}
	}
	var winners atomic.Int64
	var losers atomic.Int64
	winnerChannel := make(chan string, 1)
	var consumeGroup sync.WaitGroup
	for _, requestID := range requestIDs {
		consumeGroup.Add(1)
		go func(id string) {
			defer consumeGroup.Done()
			_, replay, consumeErr := repo.Consume(context.Background(), quote.PublicID, userID, projectID, uint64Ptr(apiKeyID), operation, fingerprint, id, now)
			switch {
			case consumeErr == nil && !replay:
				winners.Add(1)
				winnerChannel <- id
			case errors.Is(consumeErr, ErrVideoQuoteConsumed):
				losers.Add(1)
			default:
				t.Errorf("并发消费Quote异常: request=%s replay=%t err=%v", id, replay, consumeErr)
			}
		}(requestID)
	}
	consumeGroup.Wait()
	close(winnerChannel)
	if winners.Load() != 1 || losers.Load() != 99 {
		t.Fatalf("100并发消费必须只有一个赢家: winners=%d losers=%d", winners.Load(), losers.Load())
	}
	winner := <-winnerChannel
	if _, replay, err := repo.Consume(context.Background(), quote.PublicID, userID, projectID, uint64Ptr(apiKeyID), operation, fingerprint, winner, now.Add(10*time.Minute)); err != nil || !replay {
		t.Fatalf("已消费Quote过期后相同request_id仍应幂等: replay=%t err=%v", replay, err)
	}
}

func cleanupVideoQuoteMySQLFixture(t *testing.T, db *gorm.DB, userID, projectID, apiKeyID, priceID uint64, modelCode string) {
	t.Helper()
	statements := []struct {
		query string
		args  []interface{}
	}{
		{"DELETE FROM ai_gateway_quotes WHERE user_id=? AND project_id=?", []interface{}{userID, projectID}},
		{"DELETE FROM ai_requests WHERE user_id=? AND request_id LIKE 'vid-g2-concurrent-%'", []interface{}{userID}},
		{"DELETE FROM ai_price_skus WHERE price_version_id=?", []interface{}{priceID}},
		{"DELETE FROM ai_price_versions WHERE id=?", []interface{}{priceID}},
		{"DELETE FROM api_keys WHERE id=?", []interface{}{apiKeyID}},
		{"DELETE FROM ai_projects WHERE id=?", []interface{}{projectID}},
		{"DELETE FROM token_models WHERE logical_model_code=?", []interface{}{modelCode}},
		{"DELETE FROM users WHERE id=?", []interface{}{userID}},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.query, statement.args...).Error; err != nil {
			t.Fatalf("清理VID-G2 MySQL夹具失败: %v", err)
		}
	}
}
