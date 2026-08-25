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

	"molin/server/internal/modules/token_gateway/model"
)

func TestImageQuoteRepositoryMySQLConcurrentConsume(t *testing.T) {
	dsn := os.Getenv("MOLIN_IMAGE_G2_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置 IMG-G2 隔离 MySQL DSN")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(120)
	sqlDB.SetMaxIdleConns(120)

	const (
		userID    = uint64(91001)
		projectID = uint64(91001)
		apiKeyID  = uint64(91001)
		modelCode = "molin/image-g2-mysql"
		quoteID   = uint64(91001)
	)
	cleanupImageQuoteMySQLFixture(t, db, userID, projectID, apiKeyID, modelCode, quoteID)
	t.Cleanup(func() { cleanupImageQuoteMySQLFixture(t, db, userID, projectID, apiKeyID, modelCode, quoteID) })

	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?, 'fixture', 'verified', 'active')", userID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ai_projects(id,user_id,name,status,budget_mode,monthly_budget,timezone) VALUES(?,?,?,'active','disabled',NULL,'Asia/Shanghai')", projectID, userID, "图片G2隔离项目").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status) VALUES(?,?,?,'fixture','fixture-image-g2-hash','隔离密钥','postpaid','','allowlist','active')", apiKeyID, userID, projectID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(?,?,?,?,?)", quoteID, modelCode, "图片G2隔离模型", "image", "inactive").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	limits := json.RawMessage(`{"max_count":1,"variants":[{"resolution":"2K","aspect_ratio":"1:1","quality":"standard","output_format":"provider_default","delivery":"url"}]}`)
	if err := db.Exec(`INSERT INTO ai_price_versions(
id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,
max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,
failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by)
VALUES(?,?, 'image.generate','image_variant',1,'CNY',1,'active',0.2,NULL,NULL,?,0.01,'test_fixture','g2-mysql','test_fixture','confirmed_usage','ceil_8',?,?,?,?)`,
		quoteID, modelCode, limits, now.Add(-time.Hour), now.Add(time.Hour), now.Add(-time.Hour), userID).Error; err != nil {
		t.Fatal(err)
	}
	fixturePriceID := quoteID + 1
	if err := db.Exec(`INSERT INTO ai_price_versions(
id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,
max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,
failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by,approved_by,approved_at)
VALUES(?,?, 'image.generate','image_variant',2,'CNY',1,'approved',0.2,NULL,NULL,?,0.01,'test_fixture','g2-publish-deny','test_fixture','confirmed_usage','ceil_8',?,?,?,?,?,?)`,
		fixturePriceID, modelCode, limits, now.Add(-time.Hour), now.Add(time.Hour), now.Add(-time.Hour), userID, userID, now).Error; err != nil {
		t.Fatal(err)
	}
	pricingRepo := NewG3PricingRepository(db)
	if err := pricingRepo.PublishApprovedVersion(context.Background(), fixturePriceID, now); !errors.Is(err, ErrPriceVersionNotPublishable) {
		t.Fatalf("非商业测试价格不得由正式发布入口发布: %v", err)
	}
	var fixtureStatus string
	if err := db.Raw("SELECT status FROM ai_price_versions WHERE id = ?", fixturePriceID).Scan(&fixtureStatus).Error; err != nil || fixtureStatus != model.AIPriceApproved {
		t.Fatalf("发布拒绝后测试价格状态必须保持approved: status=%s err=%v", fixtureStatus, err)
	}

	requestIDs := make([]string, 100)
	for index := range requestIDs {
		requestIDs[index] = fmt.Sprintf("img-g2-concurrent-%03d", index)
		if err := db.Exec(`INSERT INTO ai_requests(request_id,user_id,project_id,api_key_id,logical_model_code,modality,capability,delivery_status,is_stream)
VALUES(?,?,?,?,?,'image','image.generate','pending',0)`, requestIDs[index], userID, projectID, apiKeyID, modelCode).Error; err != nil {
			t.Fatal(err)
		}
	}
	fingerprint := fmt.Sprintf("%064x", quoteID)
	variantHash := fmt.Sprintf("%064x", quoteID+1)
	quote := &model.AIGatewayQuote{
		ID: quoteID, PublicID: "quote_img_g2_mysql", UserID: userID, ProjectID: projectID, APIKeyID: uint64Ptr(apiKeyID),
		LogicalModelCode: modelCode, Capability: model.AIImageCapability, RequestFingerprint: fingerprint,
		RequestVariantHash: variantHash, PriceVersionID: quoteID, PriceSnapshotJSON: json.RawMessage(`{"schema_version":2}`),
		QuotedAmount: decimal.RequireFromString("0.50000000"), Currency: "CNY", ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now,
	}
	repo := NewImageQuoteRepository(db)
	if err := repo.Create(context.Background(), quote); err != nil {
		t.Fatal(err)
	}

	var winners atomic.Int64
	var losers atomic.Int64
	winnerChannel := make(chan string, 1)
	var wg sync.WaitGroup
	for _, requestID := range requestIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, idempotent, consumeErr := repo.Consume(context.Background(), quote.PublicID, userID, projectID, uint64Ptr(apiKeyID), fingerprint, id, now)
			switch {
			case consumeErr == nil && !idempotent:
				winners.Add(1)
				winnerChannel <- id
			case errors.Is(consumeErr, ErrImageQuoteConsumed):
				losers.Add(1)
			default:
				t.Errorf("并发消费异常: request=%s idempotent=%t err=%v", id, idempotent, consumeErr)
			}
		}(requestID)
	}
	wg.Wait()
	close(winnerChannel)
	if winners.Load() != 1 || losers.Load() != 99 {
		t.Fatalf("真实 MySQL Quote 并发必须只有一个胜者: winners=%d losers=%d", winners.Load(), losers.Load())
	}
	winner := <-winnerChannel
	_, idempotent, err := repo.Consume(context.Background(), quote.PublicID, userID, projectID, uint64Ptr(apiKeyID), fingerprint, winner, now)
	if err != nil || !idempotent {
		t.Fatalf("真实 MySQL 相同请求重放必须幂等: idempotent=%t err=%v", idempotent, err)
	}
	_, idempotent, err = repo.Consume(context.Background(), quote.PublicID, userID, projectID, uint64Ptr(apiKeyID), fingerprint, winner, now.Add(10*time.Minute))
	if err != nil || !idempotent {
		t.Fatalf("真实 MySQL 已消费Quote过期后仍须返回原绑定: idempotent=%t err=%v", idempotent, err)
	}
}

func cleanupImageQuoteMySQLFixture(t *testing.T, db *gorm.DB, userID, projectID, apiKeyID uint64, modelCode string, quoteID uint64) {
	t.Helper()
	statements := []struct {
		query string
		args  []interface{}
	}{
		{"DELETE FROM ai_gateway_quotes WHERE id = ?", []interface{}{quoteID}},
		{"DELETE FROM ai_requests WHERE user_id = ? AND request_id LIKE 'img-g2-concurrent-%'", []interface{}{userID}},
		{"DELETE FROM ai_price_versions WHERE id = ?", []interface{}{quoteID}},
		{"DELETE FROM ai_price_versions WHERE id = ?", []interface{}{quoteID + 1}},
		{"DELETE FROM api_keys WHERE id = ?", []interface{}{apiKeyID}},
		{"DELETE FROM ai_projects WHERE id = ?", []interface{}{projectID}},
		{"DELETE FROM token_models WHERE logical_model_code = ?", []interface{}{modelCode}},
		{"DELETE FROM users WHERE id = ?", []interface{}{userID}},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.query, statement.args...).Error; err != nil {
			t.Fatalf("清理 IMG-G2 MySQL 夹具失败: %v", err)
		}
	}
}

func uint64Ptr(value uint64) *uint64 { return &value }
