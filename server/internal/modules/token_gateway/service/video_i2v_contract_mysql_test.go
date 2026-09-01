package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"image"
	"image/png"
	"sync"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type videoG6I2VMutationModerator struct {
	video.VideoModerationAdapter
	once   sync.Once
	change func() error
	err    error
}

func (m *videoG6I2VMutationModerator) ModeratePrompt(ctx context.Context, prompt string) error {
	m.once.Do(func() { m.err = m.change() })
	if m.err != nil {
		return m.err
	}
	return m.VideoModerationAdapter.ModeratePrompt(ctx, prompt)
}

type videoG6I2VFixture struct {
	legacy        videoG5ReservationFixture
	app           *VideoHTTPService
	command       VideoCommand
	asset         model.AIGatewayInputAsset
	reference     video.NormalizedReferenceImage
	policyVersion string
}

// 从真实规范化PNG、G3输入事实及G5钱包构建独立I2V场景，不以业务Mock替代事务。
func newVideoG6I2VFixture(t *testing.T) videoG6I2VFixture {
	t.Helper()
	db := openVideoG6MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	id, code := f.owner.UserID, f.command.FingerprintInput.LogicalModelCode
	price := f.quotes.pricing.repo.(*fakeActivePriceReader)
	for _, sku := range price.skus {
		if err := db.Create(&sku).Error; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, _ := json.Marshal(map[string]any{"logical_model_code": code, "modality": "video", "capabilities": []string{"video.generate"}, "visible_scope": "all", "video_contract": json.RawMessage(videoG6NoEntitlementContract)})
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{"UPDATE api_keys SET video_generate_allowed=1 WHERE id=?", []any{id}},
		{"INSERT INTO ai_project_model_capability_grants(user_id,project_id,logical_model_code,capability,status,granted_by,created_at,updated_at) VALUES(?,?,?,'video.generate','active',?,UTC_TIMESTAMP(),UTC_TIMESTAMP())", []any{id, id, code, id}},
		{"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='video:generate'", []any{id}},
		{"INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) VALUES(?,1,'active',?,'I2V合成事务',?,UTC_TIMESTAMP())", []any{id, string(snapshot), id}},
	} {
		if err := db.Exec(stmt.sql, stmt.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	body := "仅用于图生视频隔离事务验证的合成条款。"
	policyVersion := fmt.Sprintf("rights-g6-i2v-%d", id)
	if err := db.Exec("INSERT INTO ai_video_rights_policies(policy_version,purpose,title,body,body_sha256,status,effective_at,expires_at,acceptance_ttl_seconds,version_no) VALUES(?,'non_commercial_test_fixture','合成I2V条款',?,?,'active',?,?,300,1)", policyVersion, body, videoBillingDigest(body), now.Add(-time.Hour), now.Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE policy_version=? AND status='active'", policyVersion).Error; err != nil {
			t.Error(err)
		}
	})
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 640, 640))); err != nil {
		t.Fatal(err)
	}
	normalizer, err := video.NewReferenceImageNormalizer(video.ReferenceImageLimits{MaxSourceBytes: 10 << 20, MaxNormalizedBytes: 10 << 20, MaxPixels: 16777216, MaxWidth: 4096, MaxHeight: 4096, MinAspectRatio: 0.5, MaxAspectRatio: 2, MaxEXIFBytes: 1 << 20, MaxICCBytes: 1 << 20, MaxDecodeDuration: 30 * time.Second, MaxTempDiskBytes: 128 << 20})
	if err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizer.Normalize(context.Background(), video.ReferenceImageInput{Filename: "fixture.png", DeclaredMIME: "image/png", Body: bytes.NewReader(raw.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	upload := model.AIUploadSession{PublicID: fmt.Sprintf("vup_g6_%d", id), UserID: id, ProjectID: id, APIKeyID: &id, Purpose: model.AIUploadPurposeVideoReferenceImage, SourceType: model.AIUploadSourcePlatformPresigned, Status: model.AIUploadSessionVerifying, MIMEType: "image/png", SizeBytes: normalized.SizeBytes, Bucket: "video-test", ObjectKey: fmt.Sprintf("fixture/%d.png", id), ExpiresAt: now.Add(time.Hour)}
	if err := db.Create(&upload).Error; err != nil {
		t.Fatal(err)
	}
	mime, size, width, height, moderation := "image/png", normalized.SizeBytes, uint32(normalized.Width), uint32(normalized.Height), "g6-fixture"
	asset := model.AIGatewayInputAsset{PublicID: fmt.Sprintf("vin_g6_%d", id), UserID: id, ProjectID: id, SourceType: upload.SourceType, UploadSessionID: &upload.ID, OriginalSHA256: normalized.OriginalSHA256, NormalizedSHA256: &normalized.NormalizedSHA256, Bucket: &upload.Bucket, ObjectKey: &upload.ObjectKey, MIMEType: &mime, SizeBytes: &size, Width: &width, Height: &height, ModerationPolicyVersion: &moderation, ModerationStatus: model.AIModerationPassed, VersionNo: 1, LifecycleState: model.AIInputAssetReady, ExpiresAt: now.Add(time.Hour)}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.AIUploadSession{}).Where("id=?", upload.ID).Updates(map[string]any{"status": "completed", "final_input_asset_id": asset.ID, "source_etag": "sealed-fixture", "completed_at": now}).Error; err != nil {
		t.Fatal(err)
	}
	loader := func(_ context.Context, a model.AIGatewayInputAsset) (*video.NormalizedReferenceImage, error) {
		if a.PublicID != asset.PublicID {
			return nil, repository.ErrVideoInputNotFound
		}
		copy := normalized
		copy.Bytes = append([]byte(nil), normalized.Bytes...)
		return &copy, nil
	}
	app, err := NewVideoHTTPService(db, VideoBillingOptions{QuoteSecret: f.service.quoteSecret, PromptSecret: f.service.promptSecret, IntentSecret: f.service.intentSecret, Protector: f.service.protector, Safety: f.service.safety, ReferenceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	c := VideoCommand{Caller: VideoCaller{UserID: id, ProjectID: id, APIKeyID: id}, IdempotencyKey: "g6-i2v-quote-0001", Model: code, Prompt: "合成图生视频", Operation: "image_to_video", InputAssetID: asset.PublicID, RightsAttestation: true}
	return videoG6I2VFixture{legacy: f, app: app, command: c, asset: asset, reference: normalized, policyVersion: policyVersion}
}

func TestVideoG6I2VMySQLRightsQuoteHold(t *testing.T) {
	fixture := newVideoG6I2VFixture(t)
	f, app, c, normalized, policyVersion := fixture.legacy, fixture.app, fixture.command, fixture.reference, fixture.policyVersion
	db, id := f.db, f.owner.UserID
	if _, err := app.Quote(context.Background(), c); !errors.Is(err, ErrVideoRightsRequired) {
		t.Fatalf("没有项目接受不得创建I2V报价：%v", err)
	}
	if _, err := app.AcceptProjectRights(context.Background(), VideoRightsAcceptCommand{Caller: VideoCaller{UserID: id, ProjectID: id}, PolicyVersion: policyVersion, Confirmed: true, IdempotencyKey: "g6-i2v-owner-accept-0001", RequestID: "g6-i2v-rights-http-0001"}); err != nil {
		t.Fatal(err)
	}
	quote, err := app.Quote(context.Background(), c)
	if err != nil || quote.QuotedAmount != "0.75000000" {
		t.Fatalf("合法I2V报价应0.75：%v", err)
	}
	c.QuoteID, c.IdempotencyKey = quote.QuoteID, "g6-i2v-create-0001"
	created, err := app.Create(context.Background(), c)
	if err != nil || created.Job.Status != "queued" {
		t.Fatalf("合法I2V必须进入同一G5任务：%v", err)
	}
	bindings, err := repository.NewVideoTaskInputRepository(db).ListForOwner(context.Background(), created.Job.ID, f.owner)
	if err != nil || len(bindings) != 1 || bindings[0].NormalizedSHA256 != normalized.NormalizedSHA256 || bindings[0].LeaseReleasedAt != nil {
		t.Fatal("I2V必须建立唯一冻结输入及执行租约")
	}
	for _, table := range []string{"ai_requests", "ai_gateway_tasks", "wallet_holds"} {
		var n int64
		if err := db.Table(table).Where("user_id=?", id).Count(&n).Error; err != nil || n != 1 {
			t.Fatalf("唯一事实不满足：%s=%d %v", table, n, err)
		}
	}
	var declarations int64
	if err := db.Table("ai_video_rights_declarations").Where("user_id=?", id).Count(&declarations).Error; err != nil || declarations != 2 {
		t.Fatalf("Quote与生成都须绑定原子权利声明：%d %v", declarations, err)
	}
	without := c
	without.RightsAttestation = false
	if _, err := app.Create(context.Background(), without); !errors.Is(err, ErrVideoRightsRequired) {
		t.Fatalf("幂等重放不能绕过本次声明：%v", err)
	}
	t.Run("启用G6却缺rights不能回退旧合同", func(t *testing.T) {
		r, err := app.prepareCommand(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		partial := *app.billing
		partial.rights = nil
		_, err = partial.ReserveAndCreate(context.Background(), VideoReservationCommand{Rights: r.Rights, Prompt: r.Prompt, RightsPolicyVersion: r.RightsPolicyVersion, QuotePublicID: c.QuoteID, QuoteCommandKind: "quote", IdempotencyKey: c.IdempotencyKey, RequestID: r.RequestID, TaskID: r.TaskID, FingerprintInput: r.FingerprintInput})
		if !errors.Is(err, ErrVideoRightsUnavailable) {
			t.Fatalf("缺权利依赖必须失败关闭：%v", err)
		}
	})
	t.Run("同owner错Quote不能关联声明", func(t *testing.T) {
		other := c
		other.QuoteID = ""
		other.IdempotencyKey = "g6-i2v-other-quote-0001"
		q, err := app.Quote(context.Background(), other)
		if err != nil {
			t.Fatal(err)
		}
		var quoteRow model.AIGatewayQuote
		if err := db.Where("public_id=?", q.QuoteID).Take(&quoteRow).Error; err != nil {
			t.Fatal(err)
		}
		var declaration videoRightsDeclaration
		if err := db.Where("request_id=?", created.RequestID).Take(&declaration).Error; err != nil {
			t.Fatal(err)
		}
		rollback := errors.New("回滚同主体错绑反例")
		var observed error
		if err := db.Transaction(func(tx *gorm.DB) error {
			op, requestID, fp := "image_to_video", fmt.Sprintf("vid_req_g6_shadow_%d", id), videoBillingDigest("shadow")
			r := model.VideoBillingRequest{AIRequest: model.AIRequest{RequestID: requestID, UserID: id, ProjectID: &id, APIKeyID: &id, LogicalModelCode: c.Model, Modality: "video", Capability: model.AIVideoCapability, Operation: &op, RequestFingerprint: &fp, ModerationStatus: model.AIModerationPassed, ExecutionStatus: model.AIExecutionPending, BillingStatus: model.AIBillingUnquoted, DeliveryStatus: model.AIDeliveryPending, VersionNo: 1}, CommandKind: "create_video", IntentKeyHash: videoBillingDigest("shadow-key"), IntentVersion: VideoGenerationIntentVersion, RightsPolicyVersion: policyVersion}
			if err := tx.Create(&r).Error; err != nil {
				return err
			}
			declaration.ID = 0
			declaration.QuoteID = quoteRow.ID
			declaration.RequestID = &requestID
			observed = tx.Create(&declaration).Error
			return rollback
		}); !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		var failure *drivermysql.MySQLError
		if !errors.As(observed, &failure) || failure.Number != 1644 {
			t.Fatalf("声明必须绑定原消费关联：%v", observed)
		}
	})
	// 事务外安全审核返回前切换政策，写入时必须观察新事实且不产生第二个Quote/Hold/Task。
	app.billing.safety = video.NewVideoSafetyPipeline(&videoG6I2VMutationModerator{VideoModerationAdapter: video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), change: func() error {
		return db.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE policy_version=?", policyVersion).Error
	}}, nil)
	changed := c
	changed.QuoteID = ""
	changed.IdempotencyKey = "g6-i2v-race-policy-0001"
	if _, err := app.Create(context.Background(), changed); !errors.Is(err, ErrVideoRightsUnavailable) && !errors.Is(err, ErrVideoRightsRequired) {
		t.Fatalf("预检后退役必须在写入前拒绝：%v", err)
	}
	for _, table := range []string{"ai_requests", "ai_gateway_tasks", "wallet_holds"} {
		var n int64
		if err := db.Table(table).Where("user_id=?", id).Count(&n).Error; err != nil || n != 1 {
			t.Fatalf("拒绝不得形成部分事实：%s=%d %v", table, n, err)
		}
	}
}

func TestVideoG6I2VMySQLReplayAfterInputDeletion(t *testing.T) {
	x := newVideoG6I2VFixture(t)
	app, c, f := x.app, x.command, x.legacy
	if _, err := app.AcceptProjectRights(context.Background(), VideoRightsAcceptCommand{Caller: VideoCaller{UserID: f.owner.UserID, ProjectID: f.owner.ProjectID}, PolicyVersion: x.policyVersion, Confirmed: true, IdempotencyKey: "g6-i2v-delete-rights-0001", RequestID: "g6-i2v-delete-http-0001"}); err != nil {
		t.Fatal(err)
	}
	quote, err := app.Quote(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	c.QuoteID, c.IdempotencyKey = quote.QuoteID, "g6-i2v-delete-create-0001"
	result, err := app.Create(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	f.service = app.billing
	f.command.RequestID = result.RequestID
	f.command.TaskID = result.Job.ID
	_, adapter := runVideoG5ReadyFixture(t, f)
	if _, err := app.billing.SettleReady(context.Background(), result.Job.ID, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := app.billing.DeliverReady(context.Background(), result.Job.ID, f.owner); err != nil {
		t.Fatal(err)
	}
	inputs := repository.NewVideoInputAssetRepository(f.db)
	asset, err := inputs.RequestDelete(context.Background(), x.asset.PublicID, f.owner, 1, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"deleting", "deleted"} {
		asset, err = inputs.TransitionLifecycle(context.Background(), asset.PublicID, f.owner, asset.VersionNo, state, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	reads := 0
	app.billing.referenceLoader = func(context.Context, model.AIGatewayInputAsset) (*video.NormalizedReferenceImage, error) {
		reads++
		return nil, errors.New("已删除正文不可读取")
	}
	got, err := app.Create(context.Background(), c)
	if err != nil || got == nil || !got.Existing || got.RequestID != result.RequestID || got.Job.ID != result.Job.ID || got.Job.Status != "completed" || reads != 0 || adapter.SubmitCalls() != 1 {
		t.Fatalf("安全终态原输入删除后须纯账本重放：reads=%d err=%v", reads, err)
	}
}
