package service

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

	billingmodel "molin/server/internal/modules/billing/model"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func TestVideoReservationServiceMySQLAtomicQuoteHoldAndTask(t *testing.T) {
	dsn := os.Getenv("MOLIN_VIDEO_G2_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置VID-G2隔离MySQL DSN")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	const fixtureID = uint64(94001)
	const modelCode = "molin/video-g2-reservation"
	// 本测试只允许运行在脚本创建的一次性数据库；G3开始TaskEvent和TaskInput均为不可删除事实，禁止清理式验收。
	if err := db.Exec("INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(?,?,?,'active','disabled','Asia/Shanghai')", fixtureID, fixtureID, "视频G2原子预占项目").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,model_scope,scope_mode,status) VALUES
(?,?,?,'fixture','vid-g2-reserve-hash','视频G2预占密钥','postpaid','','allowlist','active'),
(?,?,?,'fixture-b','vid-g2-reserve-hash-b','视频G2第二密钥','postpaid','','allowlist','active')`, fixtureID, fixtureID, fixtureID, fixtureID+1, fixtureID, fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO token_models(id,logical_model_code,display_name,modality,status) VALUES(?,?,?,?,?)", fixtureID, modelCode, "视频G2预占模型", "video", "inactive").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&billingmodel.Wallet{ID: fixtureID, UserID: fixtureID, BalanceAmount: decimal.NewFromInt(10), FrozenAmount: decimal.Zero, Currency: "CNY"}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	variants := []VideoPriceVariant{
		{Operation: model.AIVideoOperationTextToVideo, Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24},
		{Operation: model.AIVideoOperationImageToVideo, Resolution: "1280x720", DurationSeconds: 5, AspectRatio: "16:9", FrameRate: 24},
	}
	limits, _ := json.Marshal(VideoPricingLimits{MeterType: VideoMeterSeconds, Variants: variants})
	if err := db.Exec(`INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by)
VALUES(?,?,'video.generate','video_seconds',1,'CNY',1,'active',0.2,NULL,NULL,?,0.10,'non_commercial_test_fixture','vid-g2-reserve','non_commercial_test_fixture','confirmed_usage','ceil_8',?,?,?,?)`, fixtureID, modelCode, limits, now, now.Add(time.Hour), now, fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	for _, variant := range variants {
		raw, hash, err := CanonicalVideoPriceVariant(variant)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.AIPriceSKU{PriceVersionID: fixtureID, MeterType: VideoMeterSeconds, VariantJSON: raw, VariantHash: hash, CostUnitPrice: decimal.RequireFromString("0.06"), SaleUnitPrice: decimal.RequireFromString("0.10"), Scale: decimal.NewFromInt(1), Currency: "CNY"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO ai_upload_sessions(id,public_id,user_id,project_id,api_key_id,purpose,source_type,mime_type,size_bytes,bucket,object_key,status,expires_at)
VALUES(?, 'vid_upload_reservation', ?, ?, ?, 'video_reference_image','platform_presigned','image/png',1024,'vid-g2-input','source.png','verifying',?)`, fixtureID, fixtureID, fixtureID, fixtureID, now.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	normalizedHash := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if err := db.Exec(`INSERT INTO ai_gateway_input_assets(id,public_id,user_id,project_id,source_type,upload_session_id,original_sha256,normalized_sha256,bucket,object_key,mime_type,size_bytes,width,height,moderation_policy_version,moderation_status,version_no,lifecycle_state,expires_at,legal_hold)
VALUES(?, 'vin_asset_reservation', ?, ?, 'platform_presigned', ?, ?, ?, 'vid-g2-input','normalized.png','image/png',1024,1280,720,'vid-g2-policy','passed',1,'ready',?,0)`, fixtureID, fixtureID, fixtureID, fixtureID, normalizedHash, normalizedHash, now.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE ai_upload_sessions SET status='completed',final_input_asset_id=?,source_etag='fixture-etag',completed_at=? WHERE id=?", fixtureID, now, fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ai_upload_sessions(id,public_id,user_id,project_id,api_key_id,purpose,source_type,mime_type,size_bytes,bucket,object_key,status,expires_at)
VALUES(?, 'vid_upload_reservation_key_b', ?, ?, ?, 'video_reference_image','platform_presigned','image/png',1024,'vid-g2-input','source-b.png','verifying',?)`, fixtureID+1, fixtureID, fixtureID, fixtureID+1, now.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	otherKeyHash := "edededededededededededededededededededededededededededededededed"
	if err := db.Exec(`INSERT INTO ai_gateway_input_assets(id,public_id,user_id,project_id,source_type,upload_session_id,original_sha256,normalized_sha256,bucket,object_key,mime_type,size_bytes,width,height,moderation_policy_version,moderation_status,version_no,lifecycle_state,expires_at,legal_hold)
VALUES(?, 'vin_asset_reservation_key_b', ?, ?, 'platform_presigned', ?, ?, ?, 'vid-g2-input','normalized-b.png','image/png',1024,1280,720,'vid-g2-policy','passed',1,'ready',?,0)`, fixtureID+1, fixtureID, fixtureID, fixtureID+1, otherKeyHash, otherKeyHash, now.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE ai_upload_sessions SET status='completed',final_input_asset_id=?,source_etag='fixture-etag-b',completed_at=? WHERE id=?", fixtureID+1, now, fixtureID+1).Error; err != nil {
		t.Fatal(err)
	}
	pricing := NewVideoPricingService(repository.NewG3PricingRepository(db))
	pricing.now = func() time.Time { return now }
	quoteService := NewVideoQuoteService(pricing, repository.NewVideoQuoteRepository(db), []byte("vid-g2-quote-fingerprint-secret-32bytes"))
	inputResolver, err := NewGORMVideoInputSnapshotResolver(db)
	if err != nil {
		t.Fatal(err)
	}
	inputResolver.now = func() time.Time { return now }
	quoteService.WithInputSnapshotResolver(inputResolver)
	quoteService.now = func() time.Time { return now }
	var quoteSequence atomic.Int32
	quoteService.newPublicID = func() (string, error) { return fmt.Sprintf("vid_quote_reservation_%d", quoteSequence.Add(1)), nil }
	walletHolds := billingservice.NewWalletHoldService(db, billingrepo.NewWalletRepository(db), billingrepo.NewTransactionRepository(db), billingrepo.NewWalletHoldRepository(db))
	reservation, err := NewVideoReservationService(db, walletHolds, quoteService)
	if err != nil {
		t.Fatal(err)
	}
	reservation.now = func() time.Time { return now }
	facade := NewVideoQuoteFacade(quoteService, reservation)
	for _, denied := range []VideoFacadeRequest{
		{IdempotencyKey: "vid-g3-cross-key-quote", RequestID: "vid_req_cross_key", TaskID: "vid_task_cross_key", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "bdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbdbd", Variant: variants[1], Input: &VideoQuoteInputBinding{InputAssetID: "vin_asset_reservation_key_b", NormalizedSHA256: otherKeyHash, Version: 1}}},
		{IdempotencyKey: "vid-g3-jwt-key-quote", RequestID: "vid_req_jwt_key", TaskID: "vid_task_jwt_key", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: 0, LogicalModelCode: modelCode, PromptHash: "bebebebebebebebebebebebebebebebebebebebebebebebebebebebebebebebe", Variant: variants[1], Input: &VideoQuoteInputBinding{InputAssetID: "vin_asset_reservation", NormalizedSHA256: normalizedHash, Version: 1}}},
	} {
		if _, err := facade.CreateTokenQuote(context.Background(), denied); !errors.Is(err, ErrVideoInputMismatch) {
			t.Fatalf("跨API Key或JWT引用输入必须在Quote主链失败关闭: key=%d err=%v", denied.FingerprintInput.APIKeyID, err)
		}
	}
	request := VideoFacadeRequest{IdempotencyKey: "vid-g2-reserve-idem", RequestID: "vid_req_reservation", TaskID: "vid_task_reservation", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", Variant: variants[0]}}
	explicit, err := facade.CreateTokenQuote(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var replays atomic.Int32
	preparedChannel := make(chan *VideoPreparedGeneration, 100)
	var group sync.WaitGroup
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			item, generateErr := facade.GenerateWithTokenQuote(context.Background(), request, explicit.Quote.PublicID)
			if generateErr != nil {
				t.Errorf("100并发原子预占异常: %v", generateErr)
				return
			}
			if item.Existing {
				replays.Add(1)
			} else {
				winners.Add(1)
			}
			preparedChannel <- item
		}()
	}
	group.Wait()
	close(preparedChannel)
	if winners.Load() != 1 || replays.Load() != 99 {
		t.Fatalf("100并发生成必须只有一个原子赢家: winners=%d replays=%d", winners.Load(), replays.Load())
	}
	prepared := <-preparedChannel
	if prepared.HeldAmount.StringFixed(8) != "0.50000000" {
		t.Fatalf("原子预占金额错误: %+v", prepared)
	}
	assertVideoG2Count(t, db, "ai_requests", "request_id=? AND billing_status='held'", prepared.RequestID, 1)
	assertVideoG2Count(t, db, "ai_gateway_tasks", "public_id=? AND status='reserved'", prepared.TaskID, 1)
	assertVideoG2Count(t, db, "ai_request_wallet_links", "request_id=?", prepared.RequestID, 1)
	assertVideoG2Count(t, db, "wallet_holds", "idempotency_key=?", prepared.RequestID+":reserve", 1)
	imageRequest := VideoFacadeRequest{IdempotencyKey: "vid-g2-reserve-i2v", RequestID: "vid_req_reservation_i2v", TaskID: "vid_task_reservation_i2v", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Variant: variants[1], Input: &VideoQuoteInputBinding{InputAssetID: "vin_asset_reservation", NormalizedSHA256: normalizedHash, Version: 1}}}
	imageQuote, err := facade.CreateTokenQuote(context.Background(), imageRequest)
	if err != nil {
		t.Fatal(err)
	}
	imagePrepared, err := facade.GenerateWithTokenQuote(context.Background(), imageRequest, imageQuote.Quote.PublicID)
	if err != nil || imagePrepared.HeldAmount.StringFixed(8) != "0.50000000" {
		t.Fatalf("图生视频原子预占失败: prepared=%+v err=%v", imagePrepared, err)
	}
	assertVideoG2Count(t, db, "ai_gateway_task_inputs", "input_asset_id=?", fixtureID, 1)
	var persistedTask model.AIImageTask
	if err := db.Where("public_id=?", imagePrepared.TaskID).First(&persistedTask).Error; err != nil {
		t.Fatal(err)
	}
	protector, err := NewVideoTaskPayloadProtector("vid-g3-key-v1", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := protector.Seal(persistedTask.ID, fixtureID, fixtureID, "prompt", []byte("VID-G3隔离测试Prompt"))
	if err != nil {
		t.Fatal(err)
	}
	payloadRepo := repository.NewVideoTaskPayloadRepository(db, protector)
	payloadOwner := repository.VideoOwner{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: uint64PointerForVideoSchemaTest(fixtureID)}
	if err := payloadRepo.Create(context.Background(), persistedTask.PublicID, payloadOwner, payload); err != nil {
		t.Fatalf("Protector认证信封应可持久化: %v", err)
	}
	if _, err := payloadRepo.FindForOwner(context.Background(), persistedTask.PublicID, "prompt", payloadOwner); err != nil {
		t.Fatalf("认证密文应可按归属读取: %v", err)
	}
	forged, err := protector.Seal(persistedTask.ID, fixtureID, fixtureID, "provider_request", []byte("受保护Provider请求"))
	if err != nil {
		t.Fatal(err)
	}
	forged.Ciphertext[0] ^= 0xff
	forged.CiphertextSHA256 = videoPayloadSHA256(forged.Ciphertext)
	if err := payloadRepo.Create(context.Background(), persistedTask.PublicID, payloadOwner, forged); !errors.Is(err, repository.ErrVideoPayloadNotFound) {
		t.Fatalf("摘要自洽但GCM认证失败的伪造信封必须拒绝: %v", err)
	}
	if err := db.Model(&model.AIGatewayTaskPayload{}).Where("id=?", payload.ID).Update("key_version", "tampered").Error; err == nil {
		t.Fatal("TaskPayload UPDATE必须被触发器拒绝")
	}
	if err := db.Delete(&model.AIGatewayTaskPayload{}, payload.ID).Error; err == nil {
		t.Fatal("TaskPayload DELETE必须被触发器拒绝")
	}
	staleRequest := VideoFacadeRequest{IdempotencyKey: "vid-g2-input-stale", RequestID: "vid_req_input_stale", TaskID: "vid_task_input_stale", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "cececececececececececececececececececececececececececececececece", Variant: variants[1], Input: &VideoQuoteInputBinding{InputAssetID: "vin_asset_reservation", NormalizedSHA256: normalizedHash, Version: 1}}}
	staleQuote, err := facade.CreateTokenQuote(context.Background(), staleRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE ai_gateway_input_assets SET lifecycle_state='pending_delete',delete_requested_at=?,pending_delete_at=? WHERE id=?", now, now, fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	// 首次成功后的同幂等重放不依赖输入继续ready；新生成则必须拒绝。
	imageReplay, err := facade.GenerateWithTokenQuote(context.Background(), imageRequest, imageQuote.Quote.PublicID)
	if err != nil || !imageReplay.Existing {
		t.Fatalf("输入状态变化后原生成仍须幂等恢复: replay=%+v err=%v", imageReplay, err)
	}
	if _, err := facade.GenerateWithTokenQuote(context.Background(), staleRequest, staleQuote.Quote.PublicID); !errors.Is(err, ErrVideoInputMismatch) {
		t.Fatalf("输入状态变化后新生成必须在预占前拒绝: %v", err)
	}
	assertVideoG2Count(t, db, "ai_gateway_quotes", "public_id=? AND consumed_request_id IS NULL", staleQuote.Quote.PublicID, 1)
	if err := db.Exec("UPDATE ai_gateway_input_assets SET lifecycle_state='ready',delete_requested_at=NULL,pending_delete_at=NULL WHERE id=?", fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	// OpenAI-compatible路径不要求先显式Quote，但必须进入同一真实预占事务。
	autoRequest := VideoFacadeRequest{IdempotencyKey: "vid-g2-reserve-auto", RequestID: "vid_req_reservation_auto", TaskID: "vid_task_reservation_auto", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd", Variant: variants[0]}}
	autoPrepared, err := facade.CreateOpenAIVideo(context.Background(), autoRequest)
	if err != nil || autoPrepared.HeldAmount.StringFixed(8) != "0.50000000" {
		t.Fatalf("OpenAI自动Quote原子预占失败: prepared=%+v err=%v", autoPrepared, err)
	}
	assertVideoG2Count(t, db, "ai_gateway_quotes", "public_id=? AND command_kind='create_video'", autoPrepared.Quote.PublicID, 1)
	// 临界hard预算并发必须由Project行锁串行化，只允许一个0.5元请求进入2.0元上限。
	budgetRequests := []VideoFacadeRequest{
		{IdempotencyKey: "vid-g2-budget-race-a", RequestID: "vid_req_budget_race_a", TaskID: "vid_task_budget_race_a", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "babababababababababababababababababababababababababababababababa", Variant: variants[0]}},
		{IdempotencyKey: "vid-g2-budget-race-b", RequestID: "vid_req_budget_race_b", TaskID: "vid_task_budget_race_b", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "bcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbcbc", Variant: variants[0]}},
	}
	budgetQuotes := make([]*VideoExplicitQuoteResult, len(budgetRequests))
	for index := range budgetRequests {
		budgetQuotes[index], err = facade.CreateTokenQuote(context.Background(), budgetRequests[index])
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("UPDATE ai_projects SET budget_mode='hard',monthly_budget=2.00 WHERE id=?", fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	var budgetWinners atomic.Int32
	var budgetRejected atomic.Int32
	group = sync.WaitGroup{}
	for index := range budgetRequests {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, generateErr := facade.GenerateWithTokenQuote(context.Background(), budgetRequests[index], budgetQuotes[index].Quote.PublicID)
			switch {
			case generateErr == nil:
				budgetWinners.Add(1)
			case errors.Is(generateErr, ErrVideoQuotaExceeded):
				budgetRejected.Add(1)
			default:
				t.Errorf("hard预算并发返回异常: %v", generateErr)
			}
		}(index)
	}
	group.Wait()
	if budgetWinners.Load() != 1 || budgetRejected.Load() != 1 {
		t.Fatalf("hard预算临界并发必须一胜一拒: winners=%d rejected=%d", budgetWinners.Load(), budgetRejected.Load())
	}
	// hard月预算不足必须在Hold前回滚Request、Task、Link，Quote保持未消费。
	if err := db.Exec("UPDATE ai_projects SET budget_mode='hard',monthly_budget=2.10 WHERE id=?", fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	quotaRequest := VideoFacadeRequest{IdempotencyKey: "vid-g2-quota-fail", RequestID: "vid_req_quota_fail", TaskID: "vid_task_quota_fail", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", Variant: variants[0]}}
	quotaQuote, err := facade.CreateTokenQuote(context.Background(), quotaRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facade.GenerateWithTokenQuote(context.Background(), quotaRequest, quotaQuote.Quote.PublicID); !errors.Is(err, ErrVideoQuotaExceeded) {
		t.Fatalf("hard月预算不足必须失败关闭: %v", err)
	}
	assertVideoG2Count(t, db, "ai_requests", "request_id=?", quotaRequest.RequestID, 0)
	assertVideoG2Count(t, db, "ai_gateway_tasks", "public_id=?", quotaRequest.TaskID, 0)
	assertVideoG2Count(t, db, "ai_request_wallet_links", "request_id=?", quotaRequest.RequestID, 0)
	assertVideoG2Count(t, db, "ai_gateway_quotes", "public_id=? AND consumed_request_id IS NULL", quotaQuote.Quote.PublicID, 1)

	// Quote过期和TaskID冲突都必须回滚生成事实，且不消费Quote或写Hold。
	if err := db.Exec("UPDATE ai_projects SET budget_mode='disabled',monthly_budget=NULL WHERE id=?", fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	expiredRequest := VideoFacadeRequest{IdempotencyKey: "vid-g2-expired-fail", RequestID: "vid_req_expired_fail", TaskID: "vid_task_expired_fail", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "acacacacacacacacacacacacacacacacacacacacacacacacacacacacacacacac", Variant: variants[0]}}
	expiredQuote, err := facade.CreateTokenQuote(context.Background(), expiredRequest)
	if err != nil {
		t.Fatal(err)
	}
	reservation.now = func() time.Time { return now.Add(videoQuoteTTL) }
	if _, err := facade.GenerateWithTokenQuote(context.Background(), expiredRequest, expiredQuote.Quote.PublicID); !errors.Is(err, repository.ErrVideoQuoteExpired) {
		t.Fatalf("过期Quote必须失败关闭: %v", err)
	}
	reservation.now = func() time.Time { return now }
	assertVideoG2Count(t, db, "ai_requests", "request_id=?", expiredRequest.RequestID, 0)
	assertVideoG2Count(t, db, "ai_gateway_quotes", "public_id=? AND consumed_request_id IS NULL", expiredQuote.Quote.PublicID, 1)

	conflictRequest := VideoFacadeRequest{IdempotencyKey: "vid-g2-task-conflict", RequestID: "vid_req_task_conflict", TaskID: request.TaskID, FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "adadadadadadadadadadadadadadadadadadadadadadadadadadadadadadadad", Variant: variants[0]}}
	conflictQuote, err := facade.CreateTokenQuote(context.Background(), conflictRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facade.GenerateWithTokenQuote(context.Background(), conflictRequest, conflictQuote.Quote.PublicID); err == nil {
		t.Fatal("重复TaskID必须失败关闭")
	}
	assertVideoG2Count(t, db, "ai_requests", "request_id=?", conflictRequest.RequestID, 0)
	assertVideoG2Count(t, db, "ai_gateway_quotes", "public_id=? AND consumed_request_id IS NULL", conflictQuote.Quote.PublicID, 1)

	// 钱包余额不足同样回滚全部业务事实；隔离钱包只保留前三次成功预占。
	if err := db.Exec("UPDATE wallets SET balance_amount=0 WHERE user_id=?", fixtureID).Error; err != nil {
		t.Fatal(err)
	}
	walletRequest := VideoFacadeRequest{IdempotencyKey: "vid-g2-wallet-fail", RequestID: "vid_req_wallet_fail", TaskID: "vid_task_wallet_fail", FingerprintInput: VideoQuoteFingerprintInput{UserID: fixtureID, ProjectID: fixtureID, APIKeyID: fixtureID, LogicalModelCode: modelCode, PromptHash: "abababababababababababababababababababababababababababababababab", Variant: variants[0]}}
	walletQuote, err := facade.CreateTokenQuote(context.Background(), walletRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facade.GenerateWithTokenQuote(context.Background(), walletRequest, walletQuote.Quote.PublicID); err == nil {
		t.Fatal("钱包余额不足必须失败关闭")
	}
	assertVideoG2Count(t, db, "ai_requests", "request_id=?", walletRequest.RequestID, 0)
	assertVideoG2Count(t, db, "ai_gateway_tasks", "public_id=?", walletRequest.TaskID, 0)
	assertVideoG2Count(t, db, "ai_request_wallet_links", "request_id=?", walletRequest.RequestID, 0)
	assertVideoG2Count(t, db, "ai_gateway_quotes", "public_id=? AND consumed_request_id IS NULL", walletQuote.Quote.PublicID, 1)
	var wallet billingmodel.Wallet
	if err := db.Where("user_id=?", fixtureID).First(&wallet).Error; err != nil || !wallet.BalanceAmount.IsZero() || wallet.FrozenAmount.StringFixed(8) != "2.00000000" {
		t.Fatalf("隔离钱包余额与冻结金额金样错误: wallet=%+v err=%v", wallet, err)
	}
}

func assertVideoG2Count(t *testing.T, db *gorm.DB, table, where string, value interface{}, expected int64) {
	t.Helper()
	var count int64
	if err := db.Table(table).Where(where, value).Count(&count).Error; err != nil || count != expected {
		t.Fatalf("VID-G2事实数量错误: table=%s count=%d expected=%d err=%v", table, count, expected, err)
	}
}

func cleanupVideoReservationFixture(t *testing.T, db *gorm.DB, fixtureID uint64, modelCode string) {
	t.Helper()
	statements := []struct {
		query string
		args  []interface{}
	}{
		{"DELETE FROM ai_gateway_task_inputs WHERE user_id=?", []interface{}{fixtureID}},
		{"DELETE links FROM ai_request_wallet_links AS links INNER JOIN ai_requests AS requests ON requests.request_id=links.request_id WHERE requests.user_id=?", []interface{}{fixtureID}},
		{"DELETE FROM ai_gateway_tasks WHERE user_id=?", []interface{}{fixtureID}},
		{"DELETE FROM ai_gateway_quotes WHERE user_id=?", []interface{}{fixtureID}},
		{"DELETE FROM ai_requests WHERE user_id=?", []interface{}{fixtureID}},
		{"DELETE FROM wallet_holds WHERE user_id=?", []interface{}{fixtureID}},
		{"DELETE FROM wallet_transactions WHERE wallet_id=?", []interface{}{fixtureID}},
		{"DELETE FROM wallets WHERE user_id=?", []interface{}{fixtureID}},
		{"UPDATE ai_upload_sessions SET status='verifying',final_input_asset_id=NULL,completed_at=NULL,source_etag=NULL WHERE user_id=?", []interface{}{fixtureID}},
		{"DELETE FROM ai_gateway_input_assets WHERE user_id=?", []interface{}{fixtureID}},
		{"DELETE FROM ai_upload_sessions WHERE user_id=?", []interface{}{fixtureID}},
		{"DELETE FROM ai_price_skus WHERE price_version_id=?", []interface{}{fixtureID}},
		{"DELETE FROM ai_price_versions WHERE id=?", []interface{}{fixtureID}},
		{"DELETE FROM api_keys WHERE id=?", []interface{}{fixtureID}},
		{"DELETE FROM ai_projects WHERE id=?", []interface{}{fixtureID}},
		{"DELETE FROM token_models WHERE logical_model_code=?", []interface{}{modelCode}},
		{"DELETE FROM users WHERE id=?", []interface{}{fixtureID}},
	}
	for _, statement := range statements {
		if err := db.Exec(statement.query, statement.args...).Error; err != nil {
			t.Fatalf("清理VID-G2原子预占夹具失败: %v", err)
		}
	}
}
