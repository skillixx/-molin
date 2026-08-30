package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	billingmodel "molin/server/internal/modules/billing/model"
	billingrepo "molin/server/internal/modules/billing/repository"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

var videoG5FixtureSequence atomic.Uint64

// TestVideoG5ReserveMySQLOutboxDispatcherOff 验证共享发布器仍可领取旧事件，但绝不能发送G5的MySQL财务事实。
func TestVideoG5ReserveMySQLOutboxDispatcherOff(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	control := model.AIOutboxEvent{EventID: "control_" + f.command.RequestID, AggregateType: "ai_request", AggregateID: f.command.RequestID, EventType: "request.held", PayloadJSON: json.RawMessage(`{}`), Status: model.AIOutboxPending, NextRetryAt: now}
	if err := db.Create(&control).Error; err != nil {
		t.Fatal(err)
	}
	events, err := repository.NewG3OutboxRepository(db).ClaimBatch(context.Background(), now.Add(time.Second), now.Add(-time.Minute), 10000)
	if err != nil {
		t.Fatal(err)
	}
	foundControl := false
	for _, event := range events {
		if event.AggregateType == "video_request" {
			t.Fatal("G5 Dispatcher为OFF时不得领取视频财务事件")
		}
		foundControl = foundControl || event.ID == control.ID
	}
	if !foundControl {
		t.Fatal("隔离视频事件不得阻断旧Chat/Image事件")
	}
	var held model.AIOutboxEvent
	if err := db.Where("aggregate_type='video_request' AND aggregate_id=?", f.command.RequestID).First(&held).Error; err != nil || held.Status != model.AIOutboxPending || held.LockedAt != nil {
		t.Fatalf("视频Outbox必须仍为未领取的数据库事实: %v", err)
	}
}

// TestVideoG5ReserveMySQLExpiryDuringTransaction 模拟验权、报价锁和钱包锁等待期间过期，不能沿用入口旧时钟放行。
func TestVideoG5ReserveMySQLExpiryDuringTransaction(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, step := range []string{"request", "quote", "hold", "held_outbox"} {
		t.Run(step, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			now := time.Now().UTC()
			f.service.now = func() time.Time { return now }
			f.service.fault = func(at string) error {
				if at == step {
					now = f.quote.ExpiresAt.Add(time.Second)
				}
				return nil
			}
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); !errors.Is(err, repository.ErrVideoQuoteExpired) {
				t.Fatalf("事务中报价过期应整体拒绝: %v", err)
			}
			var count int64
			if err := db.Model(&billingmodel.WalletHold{}).Where("user_id=?", f.owner.UserID).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("过期不得留下预占: %d %v", count, err)
			}
			var quote model.AIGatewayQuote
			if err := db.First(&quote, f.quote.ID).Error; err != nil || quote.ConsumedRequestID != nil {
				t.Fatalf("过期必须回滚Quote消费: %v", err)
			}
		})
	}
}

type videoG5ReservationFixture struct {
	db      *gorm.DB
	service *VideoBillingService
	command VideoReservationCommand
	owner   repository.VideoOwner
	quote   *model.AIGatewayQuote
	quotes  *VideoQuoteService
}

// openVideoG5MySQL 只允许明确标记的一次性测试库，禁止回落到应用配置或共享数据库。
func openVideoG5MySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("MOLIN_VIDEO_G5_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置VID-G5隔离MySQL DSN")
	}
	if os.Getenv("MOLIN_VIDEO_G5_ISOLATED") != "YES" || !strings.Contains(dsn, "/molin_video_g5_contract?") {
		t.Fatal("仅允许VID-G5专用临时数据库")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("打开隔离MySQL失败")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(140)
	sqlDB.SetMaxIdleConns(140)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func newVideoG5ReservationFixture(t *testing.T, db *gorm.DB, balance string) videoG5ReservationFixture {
	t.Helper()
	id := uint64(990000) + videoG5FixtureSequence.Add(1)
	now := time.Now().UTC().Truncate(time.Second)
	code := fmt.Sprintf("molin/video-g5-%d", id)
	for _, statement := range []struct {
		sql  string
		args []interface{}
	}{
		{"INSERT INTO users(id,password_hash,real_name_status,status) VALUES(?,'fixture','verified','active')", []interface{}{id}},
		{"INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(?,?,'G5隔离项目','active','disabled','Asia/Shanghai')", []interface{}{id, id}},
		{"INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status) VALUES(?,?,?,'g5',?,'G5合成密钥','postpaid','allowlist','active')", []interface{}{id, id, id, fmt.Sprintf("fixture-g5-hash-%d", id)}},
		{"INSERT INTO token_models(id,logical_model_code,display_name,modality,status,capabilities_json,release_version_no,published_at) VALUES(?,?,'G5合成模型','video','active',JSON_ARRAY('video.generate'),1,?)", []interface{}{id, code, now}},
		{"INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(?,?,?,?)", []interface{}{id, id, id, code}},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&billingmodel.Wallet{UserID: id, BalanceAmount: decimal.RequireFromString(balance), Currency: "CNY"}).Error; err != nil {
		t.Fatal(err)
	}
	reader, variant := videoPriceFixture(t, now)
	reader.version.ID = id
	reader.version.LogicalModelCode = code
	for i := range reader.skus {
		reader.skus[i].PriceVersionID = id
		var v VideoPriceVariant
		if err := json.Unmarshal(reader.skus[i].VariantJSON, &v); err != nil {
			t.Fatal(err)
		}
		if v.Operation == model.AIVideoOperationTextToVideo {
			reader.skus[i].CostUnitPrice = decimal.RequireFromString("0.04")
		} else {
			reader.skus[i].SaleUnitPrice = decimal.RequireFromString("0.15")
		}
	}
	if err := db.Exec(`INSERT INTO ai_price_versions(id,logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by)
VALUES(?,?,'video.generate','video_seconds',1,'CNY',1,'active',0.2,NULL,NULL,?,0.1,'non_commercial_test_fixture','g5-fixture','non_commercial_test_fixture','confirmed_usage','ceil_8',?,?,?,?)`, id, code, reader.version.LimitsJSON, now, now.Add(time.Hour), now, id).Error; err != nil {
		t.Fatal(err)
	}
	quoteSecret, promptSecret, intentSecret := []byte(strings.Repeat("q", 32)), []byte(strings.Repeat("p", 32)), []byte(strings.Repeat("i", 32))
	protector, err := NewVideoTaskPayloadProtector("g5-fixture-v1", []byte(strings.Repeat("c", 32)))
	if err != nil {
		t.Fatal(err)
	}
	holds := billingservice.NewWalletHoldService(db, billingrepo.NewWalletRepository(db), billingrepo.NewTransactionRepository(db), billingrepo.NewWalletHoldRepository(db))
	s, err := NewVideoBillingService(db, holds, VideoBillingOptions{QuoteSecret: quoteSecret, PromptSecret: promptSecret, IntentSecret: intentSecret, Protector: protector, Visibility: imageG6AllowVisibility{}, Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), nil)})
	if err != nil {
		t.Fatal(err)
	}
	command := VideoReservationCommand{RequestID: fmt.Sprintf("vid_req_g5_%d", id), TaskID: fmt.Sprintf("vid_task_g5_%d", id), IdempotencyKey: "create-test", QuoteCommandKind: "quote", Prompt: "非商业视频测试", RightsPolicyVersion: "rights-fixture-v1"}
	promptHash, err := VideoGenerationPromptHMAC(promptSecret, command.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	command.FingerprintInput = VideoQuoteFingerprintInput{UserID: id, ProjectID: id, APIKeyID: id, LogicalModelCode: code, PromptHash: promptHash, Variant: variant}
	quotes := NewVideoQuoteService(NewVideoPricingService(reader), repository.NewVideoQuoteRepository(db), quoteSecret)
	quote, _, err := quotes.CreateQuote(context.Background(), VideoCreateQuoteCommand{CommandKind: "quote", IdempotencyKey: "quote-test", FingerprintInput: command.FingerprintInput})
	if err != nil {
		t.Fatal(err)
	}
	command.QuotePublicID = quote.PublicID
	return videoG5ReservationFixture{db: db, service: s, command: command, owner: repository.VideoOwner{UserID: id, ProjectID: id, APIKeyID: &id}, quote: quote, quotes: quotes}
}

// prepareVideoG5I2V 通过真实上传会话和InputAsset建立测试输入，不跳过G3的来源与归属约束。
func prepareVideoG5I2V(t *testing.T, f *videoG5ReservationFixture) *model.AIGatewayInputAsset {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	data := videoG4TestPNG(t)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	upload := model.AIUploadSession{PublicID: fmt.Sprintf("vup_g5_%d", f.owner.UserID), UserID: f.owner.UserID, ProjectID: f.owner.ProjectID, APIKeyID: f.owner.APIKeyID, Purpose: model.AIUploadPurposeVideoReferenceImage, SourceType: model.AIUploadSourcePlatformPresigned, Status: model.AIUploadSessionVerifying, MIMEType: model.AIInputMIMEPNG, SizeBytes: uint64(len(data)), Bucket: "video-temp", ObjectKey: fmt.Sprintf("fixture/%d.png", f.owner.UserID), ExpiresAt: now.Add(time.Hour)}
	if err := f.db.Create(&upload).Error; err != nil {
		t.Fatal(err)
	}
	mime, size, width, height, policy := model.AIInputMIMEPNG, uint64(len(data)), uint32(16), uint32(9), "fake-input-v1"
	asset := model.AIGatewayInputAsset{PublicID: fmt.Sprintf("vin_g5_%d", f.owner.UserID), UserID: f.owner.UserID, ProjectID: f.owner.ProjectID, SourceType: model.AIUploadSourcePlatformPresigned, UploadSessionID: &upload.ID, OriginalSHA256: hash, NormalizedSHA256: &hash, Bucket: &upload.Bucket, ObjectKey: &upload.ObjectKey, MIMEType: &mime, SizeBytes: &size, Width: &width, Height: &height, ModerationPolicyVersion: &policy, ModerationStatus: model.AIModerationPassed, VersionNo: 1, LifecycleState: model.AIInputAssetReady, ExpiresAt: now.Add(time.Hour)}
	if err := f.db.Create(&asset).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.AIUploadSession{}).Where("id=?", upload.ID).Updates(map[string]interface{}{"status": "completed", "final_input_asset_id": asset.ID, "source_etag": "fixture-etag", "completed_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	f.service.referenceLoader = func(context.Context, model.AIGatewayInputAsset) (*videogateway.NormalizedReferenceImage, error) {
		return &videogateway.NormalizedReferenceImage{Bytes: append([]byte(nil), data...), MIMEType: mime, Width: 16, Height: 9, SizeBytes: size, OriginalSHA256: hash, NormalizedSHA256: hash}, nil
	}
	f.command.FingerprintInput.Variant.Operation = model.AIVideoOperationImageToVideo
	f.command.FingerprintInput.Input = &VideoQuoteInputBinding{InternalID: asset.ID, InputAssetID: asset.PublicID, NormalizedSHA256: hash, Version: 1}
	f.quotes.WithInputSnapshotResolver(&fakeVideoInputResolver{items: map[string]VideoQuoteInputBinding{asset.PublicID: *f.command.FingerprintInput.Input}})
	quote, _, err := f.quotes.CreateQuote(context.Background(), VideoCreateQuoteCommand{CommandKind: "quote", IdempotencyKey: "i2v-quote", FingerprintInput: f.command.FingerprintInput})
	if err != nil {
		t.Fatal(err)
	}
	f.quote = quote
	f.command.QuotePublicID = quote.PublicID
	return &asset
}

// TestVideoG5ReserveMySQLI2VLeasesAndFailureClosed 验证I2V的唯一绑定、回滚、过期/隔离与快照漂移不能穿透Hold。
func TestVideoG5ReserveMySQLI2VLeasesAndFailureClosed(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, mode := range []string{"success", "input_fault", "expired", "quarantined", "pending_delete", "version_drift", "moderation_rejected"} {
		t.Run(mode, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			asset := prepareVideoG5I2V(t, &f)
			switch mode {
			case "input_fault":
				f.service.fault = func(step string) error {
					if step == "input" {
						return errors.New("合成输入绑定故障")
					}
					return nil
				}
			case "expired":
				if err := db.Model(asset).Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
					t.Fatal(err)
				}
			case "quarantined":
				if err := db.Model(asset).Update("lifecycle_state", "quarantined").Error; err != nil {
					t.Fatal(err)
				}
			case "pending_delete":
				if _, err := repository.NewVideoInputAssetRepository(db).RequestDelete(context.Background(), asset.PublicID, f.owner, 1, time.Now()); err != nil {
					t.Fatal(err)
				}
			case "version_drift":
				f.command.FingerprintInput.Input.Version = 2
			case "moderation_rejected":
				f.service.safety = videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationRejectReference), nil)
			}
			r, err := f.service.ReserveAndCreate(context.Background(), f.command)
			if mode != "success" {
				if err == nil {
					t.Fatal("不安全输入不得预占")
				}
				for _, table := range []string{"wallet_holds", "ai_gateway_tasks", "ai_gateway_task_inputs"} {
					var n int64
					if e := db.Table(table).Where("user_id=?", f.owner.UserID).Count(&n).Error; e != nil || n != 0 {
						t.Fatalf("拒绝后不应有%s: %d %v", table, n, e)
					}
				}
				return
			}
			if err != nil || r.HeldAmount.StringFixed(8) != "0.75000000" {
				t.Fatalf("I2V预占失败: %v", err)
			}
			bindings, err := repository.NewVideoTaskInputRepository(db).ListForOwner(context.Background(), r.TaskID, f.owner)
			if err != nil || len(bindings) != 1 || bindings[0].LeaseReleasedAt != nil || bindings[0].NormalizedSHA256 != *asset.NormalizedSHA256 {
				t.Fatalf("输入冻结或执行租约错误: %v", err)
			}
			if _, err := repository.NewVideoInputAssetRepository(db).RequestDelete(context.Background(), asset.PublicID, f.owner, 1, time.Now()); !errors.Is(err, repository.ErrVideoInputLeaseActive) {
				t.Fatalf("活跃输入租约应阻止清理: %v", err)
			}
		})
	}
}

// TestVideoG5ReserveMySQLSharedWalletAndProjectScopes 保证多请求共享钱包不透支，Project键空间不污染旧请求幂等。
func TestVideoG5ReserveMySQLSharedWalletAndProjectScopes(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "100")
	commands := make([]VideoReservationCommand, 100)
	for i := range commands {
		c := f.command
		c.RequestID = fmt.Sprintf("vg5_many_%d_%d", f.owner.UserID, i)
		c.TaskID = "task_" + c.RequestID
		c.IdempotencyKey = fmt.Sprintf("many-%d", i)
		q, _, err := f.quotes.CreateQuote(context.Background(), VideoCreateQuoteCommand{CommandKind: "quote", IdempotencyKey: fmt.Sprintf("many-q-%d", i), FingerprintInput: c.FingerprintInput})
		if err != nil {
			t.Fatal(err)
		}
		c.QuotePublicID = q.PublicID
		commands[i] = c
	}
	var wg sync.WaitGroup
	var successes atomic.Int64
	for _, c := range commands {
		wg.Add(1)
		go func(c VideoReservationCommand) {
			defer wg.Done()
			if _, err := f.service.ReserveAndCreate(context.Background(), c); err != nil {
				t.Errorf("共享钱包预占失败: %v", err)
			} else {
				successes.Add(1)
			}
		}(c)
	}
	wg.Wait()
	if successes.Load() != 100 {
		t.Fatalf("应有100个独立预占: %d", successes.Load())
	}
	var w billingmodel.Wallet
	if err := db.Where("user_id=?", f.owner.UserID).First(&w).Error; err != nil {
		t.Fatal(err)
	}
	if !w.BalanceAmount.Equal(decimal.NewFromInt(50)) || !w.FrozenAmount.Equal(decimal.NewFromInt(50)) {
		t.Fatal("钱包守恒失败")
	}
	// 同一用户的另一Project可复用相同幂等键；新请求不使用旧idempotency_key列。
	otherProject := f.owner.ProjectID + 1000000
	if err := db.Exec("INSERT INTO ai_projects(id,user_id,name,status,budget_mode,timezone) VALUES(?,?,'另一G5项目','active','disabled','Asia/Shanghai')", otherProject, f.owner.UserID).Error; err != nil {
		t.Fatal(err)
	}
	c := commands[0]
	c.RequestID = "vg5_other_project"
	c.TaskID = "task_vg5_other_project"
	c.FingerprintInput.ProjectID = otherProject
	c.FingerprintInput.APIKeyID = 0
	q, _, err := f.quotes.CreateQuote(context.Background(), VideoCreateQuoteCommand{CommandKind: "quote", IdempotencyKey: "other-project-q", FingerprintInput: c.FingerprintInput})
	if err != nil {
		t.Fatal(err)
	}
	c.QuotePublicID = q.PublicID
	if result, err := f.service.ReserveAndCreate(context.Background(), c); err != nil || result.Existing {
		t.Fatalf("跨Project应独立幂等: %v", err)
	}
	// 同Project换成JWT不能接管属于SK的原任务，即便规范化意图完全相同。
	c = commands[0]
	c.FingerprintInput.APIKeyID = 0
	if _, err := f.service.ReserveAndCreate(context.Background(), c); !errors.Is(err, ErrVideoBillingAccess) {
		t.Fatalf("JWT不能接管原SK归属: %v", err)
	}
}

// TestVideoG5ReserveMySQLConcurrentOneHold 同一生成意图100次竞争只创建一次请求、Hold、任务和held事件。
func TestVideoG5ReserveMySQLConcurrentOneHold(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	var wg sync.WaitGroup
	var created, replayed atomic.Int64
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c := f.command
			c.RequestID = fmt.Sprintf("vid_req_race_%d", i)
			c.TaskID = fmt.Sprintf("vid_task_race_%d", i)
			r, err := f.service.ReserveAndCreate(context.Background(), c)
			if err != nil {
				t.Errorf("并发预占失败: %v", err)
				return
			}
			if r.Existing {
				replayed.Add(1)
			} else {
				created.Add(1)
			}
			if r.HeldAmount.StringFixed(8) != "0.50000000" {
				t.Error("Hold错误")
			}
		}(i)
	}
	wg.Wait()
	if created.Load() != 1 || replayed.Load() != 99 {
		t.Fatalf("赢家错误: %d/%d", created.Load(), replayed.Load())
	}
	var request model.VideoBillingRequest
	if err := db.Where("user_id=?", f.owner.UserID).First(&request).Error; err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"wallet_holds", "ai_gateway_tasks", "ai_requests"} {
		var n int64
		if err := db.Table(table).Where("user_id=?", f.owner.UserID).Count(&n).Error; err != nil || n != 1 {
			t.Fatalf("重复事实%s: %d %v", table, n, err)
		}
	}
	var outbox model.AIOutboxEvent
	if err := db.Where("aggregate_id=?", request.RequestID).First(&outbox).Error; err != nil {
		t.Fatal(err)
	}
	if outbox.EventType != "video_billing_held" || strings.Contains(string(outbox.PayloadJSON), f.command.Prompt) {
		t.Fatal("事件错误或包含明文")
	}
	var wallet billingmodel.Wallet
	db.Where("user_id=?", f.owner.UserID).First(&wallet)
	if !wallet.BalanceAmount.Equal(decimal.RequireFromString("9.5")) || !wallet.FrozenAmount.Equal(decimal.RequireFromString("0.5")) {
		t.Fatal("并发钱包守恒失败")
	}
	var inputCount int64
	db.Model(&model.AIGatewayTaskInput{}).Where("user_id=?", f.owner.UserID).Count(&inputCount)
	if inputCount != 0 {
		t.Fatal("T2V不得绑定输入")
	}
	var payload model.AIGatewayTaskPayload
	if err := db.Where("user_id=?", f.owner.UserID).First(&payload).Error; err != nil {
		t.Fatal(err)
	}
	plain, err := f.service.protector.Open(&payload)
	if err != nil || string(plain) != f.command.Prompt {
		t.Fatal("Prompt密文无法还原")
	}
	serialized, _ := json.Marshal(request)
	if strings.Contains(string(serialized), f.command.Prompt) {
		t.Fatal("普通请求JSON泄漏Prompt")
	}
	changed := f.command
	changed.Prompt = "另一个提示词"
	changed.FingerprintInput.PromptHash, _ = VideoGenerationPromptHMAC(f.service.promptSecret, changed.Prompt)
	if _, err := f.service.ReserveAndCreate(context.Background(), changed); !errors.Is(err, ErrVideoBillingConflict) {
		t.Fatalf("同键异意图必须冲突: %v", err)
	}
	if err := db.Exec("UPDATE api_keys SET status='revoked' WHERE id=?", *f.owner.APIKeyID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); !errors.Is(err, ErrVideoBillingAccess) {
		t.Fatalf("撤销权限不能读取幂等结果: %v", err)
	}
}

// TestVideoG5ReserveMySQLRollbackEveryWrite 每个事务写入点失败都不得留下冻结或已消费报价。
func TestVideoG5ReserveMySQLRollbackEveryWrite(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, step := range []string{"request", "quote", "hold", "wallet_link", "task", "payload", "held_state", "held_outbox"} {
		t.Run(step, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			f.service.fault = func(at string) error {
				if at == step {
					return errors.New("合成故障注入")
				}
				return nil
			}
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err == nil {
				t.Fatal("注入失败必须返回错误")
			}
			for _, table := range []string{"ai_requests", "ai_gateway_tasks", "wallet_holds", "wallet_transactions"} {
				var n int64
				db.Table(table).Where("user_id=?", f.owner.UserID).Count(&n)
				if n != 0 {
					t.Fatalf("事务未整体回滚: %s %d", table, n)
				}
			}
			var q model.AIGatewayQuote
			db.First(&q, f.quote.ID)
			if q.ConsumedRequestID != nil {
				t.Fatal("Quote消费未回滚")
			}
			var w billingmodel.Wallet
			db.Where("user_id=?", f.owner.UserID).First(&w)
			if !w.BalanceAmount.Equal(decimal.NewFromInt(10)) || !w.FrozenAmount.IsZero() {
				t.Fatal("冻结未回滚")
			}
		})
	}
	f := newVideoG5ReservationFixture(t, db, "0.49")
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); !errors.Is(err, billingservice.ErrInsufficientBalance) {
		t.Fatalf("余额不足应拒绝: %v", err)
	}
}
