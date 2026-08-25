package service

import (
	"context"
	"database/sql"
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

	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/dto"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func TestImageHTTPServiceMySQLContract(t *testing.T) {
	if os.Getenv("MOLIN_IMAGE_G6_ISOLATED") != "YES" {
		t.Skip("IMG-G6只允许隔离MySQL门禁执行")
	}
	dsn := os.Getenv("MOLIN_IMAGE_G6_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置IMG-G6隔离MySQL DSN")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	var databaseName string
	if err := db.Raw("SELECT DATABASE()").Scan(&databaseName).Error; err != nil || databaseName != "molin_image_g6_contract" {
		t.Fatalf("IMG-G6拒绝连接非隔离数据库: database=%s err=%v", databaseName, err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(150)
	sqlDB.SetMaxIdleConns(150)
	now := time.Date(2026, 8, 25, 6, 0, 0, 0, time.UTC)
	setupImageG5Base(t, db, now)
	if err := db.Exec("UPDATE token_models SET status='active', release_version_no=1, published_at=?, capabilities_json=JSON_ARRAY('image.generate') WHERE logical_model_code=?", now, imageG5ModelCode).Error; err != nil {
		t.Fatal(err)
	}

	t.Run("管理端创建图片测试价格且禁止正式发布", func(t *testing.T) {
		admin := NewG5AdminService(repository.NewG5AdminRepository(db), repository.NewG3PricingRepository(db))
		limits := json.RawMessage(`{"max_count":1,"variants":[{"resolution":"2K","aspect_ratio":"1:1","quality":"standard","output_format":"provider_default","delivery":"url"}]}`)
		variant := json.RawMessage(`{"resolution":"2K","aspect_ratio":"1:1","quality":"standard","output_format":"provider_default","delivery":"url"}`)
		created, err := admin.CreatePrice(context.Background(), imageG5OperatorID, dto.CreatePriceReq{
			LogicalModelCode: imageG5ModelCode, Capability: model.AIImageCapability, PricingTemplate: "image_variant",
			MinMarginRate: "0.20", Limits: limits, MinimumCharge: "0.01", CostSource: "test_fixture",
			CostSourceVersion: "g6-admin-fixture", PricePurpose: "test_fixture", CostUpdatedAt: now,
			CostExpiresAt: now.Add(24 * time.Hour), EffectiveAt: now.Add(time.Hour),
			SKUs: []dto.PriceSKUReq{{MeterType: "image_count", Variant: variant, CostUnitPrice: "0.3", SaleUnitPrice: "0.5", Scale: "1"}},
		})
		if err != nil || created.Version.PricingTemplate != "image_variant" || len(created.SKUs) != 1 {
			t.Fatalf("图片测试价格创建失败: created=%+v err=%v", created, err)
		}
		var maxInput, maxOutput sql.NullInt64
		if err := db.Raw("SELECT max_input_tokens,max_output_tokens FROM ai_price_versions WHERE id=?", created.Version.ID).Row().Scan(&maxInput, &maxOutput); err != nil || maxInput.Valid || maxOutput.Valid {
			t.Fatalf("图片价格Token上限必须为NULL: input=%+v output=%+v err=%v", maxInput, maxOutput, err)
		}
		if err := admin.ApprovePrice(context.Background(), created.Version.ID, imageG5ReviewerID); err != nil {
			t.Fatal(err)
		}
		if err := admin.PublishPrice(context.Background(), created.Version.ID); !errors.Is(err, repository.ErrPriceVersionNotPublishable) {
			t.Fatalf("测试夹具不得正式发布: %v", err)
		}
	})

	t.Run("ProjectSK报价同步生成下载与100次幂等重放", func(t *testing.T) {
		userID := uint64(97401)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "success")
		caller := ImageCaller{UserID: userID, APIKeyID: userID}
		quote := mustImageG6Quote(t, api, caller, userID, "测试生成")
		input := ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-success-0001", RequireSK: true, ExecuteNow: true,
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "测试生成", N: 1, Size: "2K", Quality: "standard", OutputFormat: "url", QuoteID: quote.QuoteID},
		}
		result, err := api.Generate(context.Background(), input)
		if err != nil || result.ExecutionErr != nil || result.Task.BillingStatus != model.AIBillingSettled || len(result.Task.Assets) != 2 {
			t.Fatalf("同步生成失败: result=%+v err=%v", result, err)
		}
		openAI, err := api.OpenAIResponse(context.Background(), caller, result.Task)
		if err != nil || len(openAI.Data) != 1 || openAI.MolinRequestID != result.Task.RequestID || openAI.Data[0].URL == "" {
			t.Fatalf("短效下载响应错误: result=%+v err=%v", openAI, err)
		}
		var wg sync.WaitGroup
		var replayFailures atomic.Int64
		for index := 0; index < 100; index++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				replay, replayErr := api.Generate(context.Background(), input)
				if replayErr != nil || replay.Task.RequestID != result.Task.RequestID || !replay.Task.Existing {
					replayFailures.Add(1)
				}
			}()
		}
		wg.Wait()
		if replayFailures.Load() != 0 || adapter.Calls() != 1 {
			t.Fatalf("幂等重放不得再次调用Provider: failures=%d calls=%d", replayFailures.Load(), adapter.Calls())
		}
		limiter := api.limiter.(*fakeImageResourceLimiter)
		limiter.mu.Lock()
		if limiter.acquireCalls != 1 || limiter.heartbeatCalls != 1 || limiter.releaseCalls != 1 || limiter.acquiredSubject.APIKeyID != userID {
			limiter.mu.Unlock()
			t.Fatalf("同步图片四维租约必须只随首次赢家执行一次: acquire=%d heartbeat=%d release=%d subject=%+v", limiter.acquireCalls, limiter.heartbeatCalls, limiter.releaseCalls, limiter.acquiredSubject)
		}
		limiter.mu.Unlock()
		var sensitiveRows int64
		if err := db.Raw(`SELECT
(SELECT COUNT(*) FROM ai_gateway_tasks WHERE request_id=? AND CAST(input_json AS CHAR) LIKE '%测试生成%') +
(SELECT COUNT(*) FROM ai_outbox_events WHERE aggregate_id=? AND CAST(payload_json AS CHAR) LIKE '%测试生成%')`, result.Task.RequestID, result.Task.RequestID).Scan(&sensitiveRows).Error; err != nil || sensitiveRows != 0 {
			t.Fatalf("Prompt不得进入任务或Outbox: rows=%d err=%v", sensitiveRows, err)
		}
		changed := mustImageG6Quote(t, api, caller, userID, "不同提示")
		input.Request.Prompt = "不同提示"
		input.Request.QuoteID = changed.QuoteID
		if _, err := api.Generate(context.Background(), input); !errors.Is(err, ErrImageQuoteConflict) {
			t.Fatalf("同幂等键不同指纹必须冲突: %v", err)
		}
	})

	t.Run("平台任务异步创建后取消释放", func(t *testing.T) {
		userID := uint64(97402)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "cancel")
		caller := ImageCaller{UserID: userID, APIKeyID: userID}
		quote := mustImageG6Quote(t, api, caller, userID, "取消测试")
		result, err := api.Generate(context.Background(), ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-cancel-0001",
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "取消测试", N: 1, Size: "2K", Quality: "standard", OutputFormat: "url", QuoteID: quote.QuoteID},
		})
		if err != nil || result.Task.Status != model.AIImageTaskReserved || adapter.Calls() != 0 {
			t.Fatalf("异步任务创建错误: result=%+v calls=%d err=%v", result, adapter.Calls(), err)
		}
		cancelled, err := api.CancelTask(context.Background(), caller, userID, result.Task.TaskID)
		if err != nil || cancelled.Status != model.AIImageTaskCancelled || cancelled.BillingStatus != model.AIBillingReleased {
			t.Fatalf("取消释放错误: task=%+v err=%v", cancelled, err)
		}
		assertWalletAmounts(t, db, userID, "10.00000000", "0.00000000")
		if report, err := api.billing.ReconcileRequest(context.Background(), cancelled.RequestID); err != nil || !report.ZeroDifference() {
			t.Fatalf("取消释放后必须零差异: report=%+v err=%v", report, err)
		}
	})

	t.Run("空map无Rabbit消息恢复陈旧reserved任务", func(t *testing.T) {
		userID := uint64(97415)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "stale-reserved")
		caller := ImageCaller{UserID: userID, APIKeyID: userID}
		quote := mustImageG6Quote(t, api, caller, userID, "陈旧预占恢复")
		result, err := api.Generate(context.Background(), ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-stale-reserved-0001",
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "陈旧预占恢复", QuoteID: quote.QuoteID},
		})
		if err != nil || result.Task.Status != model.AIImageTaskReserved || adapter.Calls() != 0 {
			t.Fatalf("构造陈旧reserved任务失败: result=%+v calls=%d err=%v", result, adapter.Calls(), err)
		}
		staleAt := now.Add(-6 * time.Minute)
		if err := db.Model(&model.AIImageTask{}).Where("request_id=?", result.Task.RequestID).Update("created_at", staleAt).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.AIRequest{}).Where("request_id=?", result.Task.RequestID).Update("created_at", staleAt).Error; err != nil {
			t.Fatal(err)
		}
		dispatcher, err := NewImageTaskDispatcher(&fakeImageTaskPublisher{}, api.billing, &fakeImageResourceLimiter{})
		if err != nil {
			t.Fatal(err)
		}
		dispatcher.now = func() time.Time { return now }
		workerCtx, stopWorker := context.WithCancel(context.Background())
		workerStopped := make(chan struct{})
		go func() {
			dispatcher.StartExpiryWorker(workerCtx, 10*time.Millisecond)
			close(workerStopped)
		}()
		deadline := time.After(2 * time.Second)
		for {
			var task model.AIImageTask
			if err := db.Where("request_id=?", result.Task.RequestID).First(&task).Error; err == nil && task.Status == model.AIImageTaskCancelled {
				break
			}
			select {
			case <-deadline:
				t.Fatalf("陈旧reserved任务未被数据库恢复worker取消: task=%+v", task)
			case <-time.After(10 * time.Millisecond):
			}
		}
		stopWorker()
		select {
		case <-workerStopped:
		case <-time.After(time.Second):
			t.Fatal("陈旧reserved恢复worker未停止")
		}
		if adapter.Calls() != 0 {
			t.Fatalf("陈旧reserved恢复不得调用Provider: %d", adapter.Calls())
		}
		assertWalletAmounts(t, db, userID, "10.00000000", "0.00000000")
		assertImageG5Count(t, db, "wallet_transactions", "user_id=? AND type='unfreeze'", userID, 1)
		if report, err := api.billing.ReconcileRequest(context.Background(), result.Task.RequestID); err != nil || !report.ZeroDifference() {
			t.Fatalf("陈旧reserved恢复后必须零差异: report=%+v err=%v", report, err)
		}
	})

	t.Run("同步Hold后claim前取消释放资金和租约", func(t *testing.T) {
		userID := uint64(97412)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "claim-cancel")
		caller := ImageCaller{UserID: userID, APIKeyID: userID}
		quote := mustImageG6Quote(t, api, caller, userID, "claim前取消")
		executionCtx, cancelExecution := context.WithCancel(context.Background())
		limiter := &fakeImageResourceLimiter{onAcquire: cancelExecution}
		api.WithResourceLimiter(limiter)
		_, err := api.Generate(executionCtx, ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-claim-cancel-0001", RequireSK: true, ExecuteNow: true,
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "claim前取消", QuoteID: quote.QuoteID},
		})
		if err == nil || adapter.Calls() != 0 {
			t.Fatalf("claim前取消不得调用Provider: calls=%d err=%v", adapter.Calls(), err)
		}
		assertWalletAmounts(t, db, userID, "10.00000000", "0.00000000")
		limiter.mu.Lock()
		if limiter.acquireCalls != 1 || limiter.releaseCalls != 1 {
			limiter.mu.Unlock()
			t.Fatalf("claim前取消必须释放唯一租约: acquire=%d release=%d", limiter.acquireCalls, limiter.releaseCalls)
		}
		limiter.mu.Unlock()
		var task model.AIImageTask
		if err := db.Where("user_id=?", userID).First(&task).Error; err != nil || task.Status != model.AIImageTaskCancelled {
			t.Fatalf("claim前取消必须释放Hold并进入取消终态: task=%+v err=%v", task, err)
		}
	})

	t.Run("同步普通claim数据库错误释放资金和租约", func(t *testing.T) {
		userID := uint64(97414)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "claim-db-error")
		caller := ImageCaller{UserID: userID, APIKeyID: userID}
		quote := mustImageG6Quote(t, api, caller, userID, "claim数据库错误")
		claimErr := errors.New("注入claim数据库查询错误")
		callbackName := "molin:test:image-claim-db-error"
		var injected atomic.Bool
		var registrationErr error
		limiter := &fakeImageResourceLimiter{onAcquire: func() {
			registrationErr = db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement != nil && tx.Statement.Table == "ai_requests" && injected.CompareAndSwap(false, true) {
					tx.AddError(claimErr)
				}
			})
		}}
		api.WithResourceLimiter(limiter)
		result, err := api.Generate(context.Background(), ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-claim-db-error-0001", RequireSK: true, ExecuteNow: true,
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "claim数据库错误", QuoteID: quote.QuoteID},
		})
		_ = db.Callback().Query().Remove(callbackName)
		if registrationErr != nil {
			t.Fatal(registrationErr)
		}
		if err != nil || result == nil || !errors.Is(result.ExecutionErr, claimErr) || adapter.Calls() != 0 {
			t.Fatalf("claim数据库错误不得调用Provider: result=%+v calls=%d err=%v", result, adapter.Calls(), err)
		}
		assertWalletAmounts(t, db, userID, "10.00000000", "0.00000000")
		limiter.mu.Lock()
		if limiter.releaseCalls != 1 {
			limiter.mu.Unlock()
			t.Fatalf("claim数据库错误必须释放租约: %d", limiter.releaseCalls)
		}
		limiter.mu.Unlock()
		var task model.AIImageTask
		if err := db.Where("user_id=?", userID).First(&task).Error; err != nil || task.Status != model.AIImageTaskCancelled {
			t.Fatalf("claim数据库错误必须释放Hold并取消任务: task=%+v err=%v", task, err)
		}
	})

	t.Run("同步心跳立即失败在Provider前释放", func(t *testing.T) {
		userID := uint64(97413)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "heartbeat-fail")
		caller := ImageCaller{UserID: userID, APIKeyID: userID}
		quote := mustImageG6Quote(t, api, caller, userID, "心跳立即失败")
		failure := make(chan error, 1)
		failure <- ErrResourceUnavailable
		limiter := &fakeImageResourceLimiter{heartbeatFailure: failure}
		api.WithResourceLimiter(limiter)
		result, err := api.Generate(context.Background(), ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-heartbeat-fail-0001", RequireSK: true, ExecuteNow: true,
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "心跳立即失败", QuoteID: quote.QuoteID},
		})
		if err != nil || result == nil || !errors.Is(result.ExecutionErr, ErrResourceUnavailable) || adapter.Calls() != 0 {
			t.Fatalf("心跳立即失败不得调用Provider: result=%+v calls=%d err=%v", result, adapter.Calls(), err)
		}
		assertWalletAmounts(t, db, userID, "10.00000000", "0.00000000")
		limiter.mu.Lock()
		if limiter.releaseCalls != 1 {
			limiter.mu.Unlock()
			t.Fatalf("心跳立即失败必须释放租约: %d", limiter.releaseCalls)
		}
		limiter.mu.Unlock()
		var task model.AIImageTask
		if err := db.Where("user_id=?", userID).First(&task).Error; err != nil || task.Status != model.AIImageTaskCancelled {
			t.Fatalf("心跳立即失败必须释放Hold并取消任务: task=%+v err=%v", task, err)
		}
	})

	t.Run("JWT平台任务绑定本人Project且不伪造SK归属", func(t *testing.T) {
		userID := uint64(97409)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "jwt")
		caller := ImageCaller{UserID: userID, RequestedProjectID: userID}
		quote := mustImageG6Quote(t, api, caller, userID, "JWT平台任务")
		result, err := api.Generate(context.Background(), ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-jwt-0001",
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "JWT平台任务", ProjectID: userID, QuoteID: quote.QuoteID},
		})
		if err != nil || result.Task.Status != model.AIImageTaskReserved || adapter.Calls() != 0 {
			t.Fatalf("JWT平台任务错误: result=%+v calls=%d err=%v", result, adapter.Calls(), err)
		}
		var task model.AIImageTask
		if err := db.Where("public_id=?", result.Task.TaskID).First(&task).Error; err != nil || task.APIKeyID != nil || task.ProjectID != userID {
			t.Fatalf("JWT任务归属错误: task=%+v err=%v", task, err)
		}
		if _, err := api.GetTask(context.Background(), ImageCaller{UserID: userID, RequestedProjectID: userID + 1}, result.Task.TaskID, userID+1); !errors.Is(err, ErrProjectAccessDenied) {
			t.Fatalf("JWT跨Project查询必须拒绝: %v", err)
		}
	})

	t.Run("首次100并发同幂等键只创建一个任务和hold", func(t *testing.T) {
		userID := uint64(97403)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "concurrent")
		caller := ImageCaller{UserID: userID, APIKeyID: userID}
		quote := mustImageG6Quote(t, api, caller, userID, "并发测试")
		input := ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-concurrent-0001", RequireSK: true, ExecuteNow: true,
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "并发测试", N: 1, Size: "2K", Quality: "standard", OutputFormat: "url", QuoteID: quote.QuoteID},
		}
		var wg sync.WaitGroup
		var failures atomic.Int64
		var newWinners atomic.Int64
		var executionFailures atomic.Int64
		var executionErrOnce sync.Once
		var firstExecutionErr error
		requestIDs := sync.Map{}
		for index := 0; index < 100; index++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				result, generateErr := api.Generate(context.Background(), input)
				if generateErr != nil {
					failures.Add(1)
					return
				}
				if !result.Task.Existing {
					newWinners.Add(1)
				}
				if result.ExecutionErr != nil {
					executionFailures.Add(1)
					executionErrOnce.Do(func() { firstExecutionErr = result.ExecutionErr })
				}
				requestIDs.Store(result.Task.RequestID, true)
			}()
		}
		wg.Wait()
		var requestCount int
		requestIDs.Range(func(_, _ interface{}) bool { requestCount++; return true })
		if failures.Load() != 0 || requestCount != 1 || adapter.Calls() != 1 {
			var request model.AIRequest
			var task model.AIImageTask
			_ = db.Where("user_id=? AND idempotency_key=?", userID, input.IdempotencyKey).First(&request).Error
			_ = db.Where("request_id=?", request.RequestID).First(&task).Error
			t.Fatalf("首次并发幂等错误: failures=%d request_ids=%d provider_calls=%d new_winners=%d execution_failures=%d first_execution_err=%v request_state=%s/%s task_state=%s",
				failures.Load(), requestCount, adapter.Calls(), newWinners.Load(), executionFailures.Load(), firstExecutionErr, request.ExecutionStatus, request.BillingStatus, task.Status)
		}
		assertImageG5Count(t, db, "ai_gateway_tasks", "user_id=?", userID, 1)
		assertImageG5Count(t, db, "wallet_holds", "user_id=?", userID, 1)
		var final model.AIRequest
		if err := db.Where("user_id=? AND idempotency_key=?", userID, input.IdempotencyKey).First(&final).Error; err != nil || final.BillingStatus != model.AIBillingSettled {
			t.Fatalf("首次并发赢家必须形成唯一结算终态: request=%+v err=%v", final, err)
		}
	})

	t.Run("横向越权和未结算下载失败关闭", func(t *testing.T) {
		ownerID := uint64(97404)
		otherID := uint64(97405)
		seedImageG6Owner(t, db, ownerID, decimal.NewFromInt(10))
		seedImageG6Owner(t, db, otherID, decimal.NewFromInt(10))
		api, _, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "isolation")
		owner := ImageCaller{UserID: ownerID, APIKeyID: ownerID}
		quote := mustImageG6Quote(t, api, owner, ownerID, "隔离测试")
		result, err := api.Generate(context.Background(), ImageGenerationInput{
			Caller: owner, IdempotencyKey: "idem-image-isolation-0001",
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "隔离测试", QuoteID: quote.QuoteID},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := api.GetTask(context.Background(), ImageCaller{UserID: otherID, APIKeyID: otherID}, result.Task.TaskID, otherID); !errors.Is(err, repository.ErrImageTaskNotFound) {
			t.Fatalf("跨用户任务查询必须统一不存在: %v", err)
		}
		if _, err := api.DownloadURL(context.Background(), owner, ownerID, "unknown-asset"); !errors.Is(err, ErrImageDownloadUnavailable) {
			t.Fatalf("未结算/不存在资产不得下载: %v", err)
		}
	})

	t.Run("历史all密钥没有显式图片scope时拒绝能力", func(t *testing.T) {
		userID := uint64(97408)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		if err := db.Exec("DELETE FROM api_key_model_scopes WHERE api_key_id=?", userID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("UPDATE api_keys SET scope_mode='all' WHERE id=?", userID).Error; err != nil {
			t.Fatal(err)
		}
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "legacy")
		_, err := api.CreateQuote(context.Background(), ImageCaller{UserID: userID, APIKeyID: userID}, dto.ImageQuoteReq{
			Model: imageG5ModelCode, Prompt: "历史密钥", N: 1, Size: "2K", Quality: "standard", OutputFormat: "url",
		})
		if !errors.Is(err, ErrImageCapabilityNotAllowed) || adapter.Calls() != 0 {
			t.Fatalf("历史all密钥不得继承图片能力: calls=%d err=%v", adapter.Calls(), err)
		}
	})

	t.Run("定向不可见模型不能绕过目录直接报价", func(t *testing.T) {
		userID := uint64(97411)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "visibility")
		api.WithVisibilityChecker(imageG6DenyVisibility{})
		_, err := api.CreateQuote(context.Background(), ImageCaller{UserID: userID, APIKeyID: userID}, dto.ImageQuoteReq{
			Model: imageG5ModelCode, Prompt: "不可见模型", N: 1, Size: "2K", Quality: "standard", OutputFormat: "url",
		})
		if !errors.Is(err, ErrImageModelUnavailable) || adapter.Calls() != 0 {
			t.Fatalf("不可见模型必须失败关闭: calls=%d err=%v", adapter.Calls(), err)
		}
	})

	t.Run("管理端D95查询和CAS隔离立即关闭下载", func(t *testing.T) {
		userID := uint64(97410)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, _, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "admin")
		caller := ImageCaller{UserID: userID, APIKeyID: userID}
		result, err := api.Generate(context.Background(), ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-admin-0001", RequireSK: true, ExecuteNow: true,
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "管理测试"},
		})
		if err != nil || result.ExecutionErr != nil {
			t.Fatalf("管理测试生成失败: result=%+v err=%v", result, err)
		}
		tasks, total, err := api.ListAdminTasks(context.Background(), ImageAdminTaskListInput{UserID: userID, Page: 1, PageSize: 20})
		if err != nil || total != 1 || len(tasks) != 1 || tasks[0].TaskID != result.Task.TaskID {
			t.Fatalf("管理任务查询错误: tasks=%+v total=%d err=%v", tasks, total, err)
		}
		assets, total, err := api.ListAdminAssets(context.Background(), ImageAdminAssetListInput{UserID: userID, Page: 1, PageSize: 20})
		if err != nil || total != 2 || len(assets) != 2 {
			t.Fatalf("管理资产查询错误: assets=%+v total=%d err=%v", assets, total, err)
		}
		var primary dto.ImageAdminAssetResp
		for _, asset := range assets {
			if asset.Role == model.AIImageAssetPrimaryOutput {
				primary = asset
			}
		}
		if primary.AssetID == "" {
			t.Fatal("缺少主图资产")
		}
		quarantined, err := api.QuarantineAsset(context.Background(), primary.AssetID, primary.VersionNo)
		if err != nil || quarantined.LifecycleState != model.AIImageAssetQuarantined {
			t.Fatalf("CAS隔离错误: asset=%+v err=%v", quarantined, err)
		}
		if _, err := api.QuarantineAsset(context.Background(), primary.AssetID, primary.VersionNo); !errors.Is(err, repository.ErrImageAssetConflict) {
			t.Fatalf("旧版本隔离重放必须冲突: %v", err)
		}
		if _, err := api.DownloadURL(context.Background(), caller, userID, primary.AssetID); !errors.Is(err, ErrImageDownloadUnavailable) {
			t.Fatalf("隔离后必须立即关闭下载: %v", err)
		}
	})

	t.Run("结果未知返回安全查询状态且不重试", func(t *testing.T) {
		userID := uint64(97406)
		seedImageG6Owner(t, db, userID, decimal.NewFromInt(10))
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageUnknown, imagegateway.FakeModerationAllow, now, "unknown")
		caller := ImageCaller{UserID: userID, APIKeyID: userID}
		result, err := api.Generate(context.Background(), ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-unknown-0001", RequireSK: true, ExecuteNow: true,
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "未知测试"},
		})
		if err != nil || !errors.Is(result.ExecutionErr, ErrImagePendingReconcile) || result.Task.BillingStatus != model.AIBillingSettlementPending || adapter.Calls() != 1 {
			t.Fatalf("结果未知错误: result=%+v calls=%d err=%v", result, adapter.Calls(), err)
		}
		queried, err := api.GetTaskByRequest(context.Background(), caller, result.Task.RequestID, userID)
		if err != nil || queried.RequestID != result.Task.RequestID || queried.BillingStatus != model.AIBillingSettlementPending {
			t.Fatalf("504后查询合同错误: task=%+v err=%v", queried, err)
		}
		if _, err := api.Generate(context.Background(), ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-unknown-0001", RequireSK: true, ExecuteNow: true,
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "未知测试"},
		}); err != nil || adapter.Calls() != 1 {
			t.Fatalf("unknown重放不得再次调用Provider: calls=%d err=%v", adapter.Calls(), err)
		}
		summary, err := api.ReconciliationSummary(context.Background())
		if err != nil || summary.SettlementPending < 1 || summary.ActiveCompensations < 1 || summary.UnreleasedHoldAmount == "0.00000000" {
			t.Fatalf("管理对账汇总必须显示待结算风险: summary=%+v err=%v", summary, err)
		}
	})

	t.Run("余额不足回滚请求任务和Quote消费", func(t *testing.T) {
		userID := uint64(97407)
		seedImageG6Owner(t, db, userID, decimal.Zero)
		api, adapter, _ := newImageG6API(t, db, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, now, "insufficient")
		caller := ImageCaller{UserID: userID, APIKeyID: userID}
		quote := mustImageG6Quote(t, api, caller, userID, "余额不足")
		_, err := api.Generate(context.Background(), ImageGenerationInput{
			Caller: caller, IdempotencyKey: "idem-image-insufficient-0001",
			Request: dto.ImageGenerationReq{Model: imageG5ModelCode, Prompt: "余额不足", QuoteID: quote.QuoteID},
		})
		if !errors.Is(err, billingservice.ErrInsufficientBalance) || adapter.Calls() != 0 {
			t.Fatalf("余额不足门禁错误: calls=%d err=%v", adapter.Calls(), err)
		}
		assertImageG5Count(t, db, "ai_requests", "user_id=?", userID, 0)
		assertImageG5Count(t, db, "ai_gateway_tasks", "user_id=?", userID, 0)
		var consumed *string
		if err := db.Table("ai_gateway_quotes").Select("consumed_request_id").Where("public_id=?", quote.QuoteID).Scan(&consumed).Error; err != nil || consumed != nil {
			t.Fatalf("余额不足必须回滚Quote消费: consumed=%v err=%v", consumed, err)
		}
	})
}

func seedImageG6Owner(t *testing.T, db *gorm.DB, userID uint64, balance decimal.Decimal) {
	t.Helper()
	setupImageG5Owner(t, db, userID, balance)
	if err := db.Exec("INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(?,?,?,?)", userID, userID, userID, imageG5ModelCode).Error; err != nil {
		t.Fatal(err)
	}
}

func newImageG6API(t *testing.T, db *gorm.DB, providerMode imagegateway.FakeImageMode, moderationMode imagegateway.FakeModerationMode, now time.Time, prefix string) (*ImageAPIService, *imagegateway.FakeImageAdapter, imagegateway.ObjectStore) {
	t.Helper()
	adapter := imagegateway.NewFakeImageAdapter(providerMode)
	store := imagegateway.NewFakeObjectStore()
	billing := newImageG5Service(t, db, adapter, imagegateway.NewFakeModerationAdapter(moderationMode), store, now)
	pricing := NewImagePricingService(repository.NewG3PricingRepository(db))
	pricing.now = func() time.Time { return now }
	api, err := NewImageAPIService(db, billing, pricing, store, ImageAPISecrets{
		QuoteFingerprint: []byte("0123456789abcdef0123456789abcdef"), PromptHMAC: []byte("abcdef0123456789abcdef0123456789"),
	})
	if err != nil {
		t.Fatal(err)
	}
	api.WithVisibilityChecker(imageG6AllowVisibility{})
	api.WithResourceLimiter(&fakeImageResourceLimiter{})
	api.now = func() time.Time { return now }
	api.quotes.now = func() time.Time { return now }
	var sequence atomic.Uint64
	api.newID = func(kind string) (string, error) {
		return fmt.Sprintf("%s_%s_%06d", kind, prefix, sequence.Add(1)), nil
	}
	return api, adapter, store
}

type imageG6AllowVisibility struct{}

func (imageG6AllowVisibility) VisibleToUser(context.Context, uint64, string) (bool, error) {
	return true, nil
}

type imageG6DenyVisibility struct{}

func (imageG6DenyVisibility) VisibleToUser(context.Context, uint64, string) (bool, error) {
	return false, nil
}

func mustImageG6Quote(t *testing.T, api *ImageAPIService, caller ImageCaller, projectID uint64, prompt string) *dto.ImageQuoteResp {
	t.Helper()
	quote, err := api.CreateQuote(context.Background(), caller, dto.ImageQuoteReq{
		Model: imageG5ModelCode, Prompt: prompt, N: 1, Size: "2K", Quality: "standard", OutputFormat: "url", ProjectID: projectID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return quote
}
