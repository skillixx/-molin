package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/handler"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	video "molin/server/internal/modules/token_gateway/video"
)

type videoModelPublicationRecoveryFixture struct {
	base     adminCancelErrorFixture
	admin    *service.VideoAdminService
	provider *video.FakeAsyncVideoAdapter
	server   *httptest.Server
	caller   service.VideoCaller
	models   map[string]string
}

func newVideoModelPublicationRecoveryFixture(t *testing.T) videoModelPublicationRecoveryFixture {
	return newVideoModelPublicationRecoveryFixtureWithDefault(t, false)
}

func newVideoModelPublicationRecoveryFixtureWithDefault(t *testing.T, defaultModel bool) videoModelPublicationRecoveryFixture {
	t.Helper()
	f := newAdminCancelErrorFixture(t)
	if err := f.f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:model_manage'", f.actor).Error; err != nil {
		t.Fatal(err)
	}
	protector, err := service.NewVideoAdminReasonProtector("model-publication-recovery-v1", f.secret)
	if err != nil {
		t.Fatal(err)
	}
	provider := video.NewFakeAsyncVideoAdapter(video.FakeVideoSuccess)
	models := map[string]string{f.f.Model: "runway:1@2"}
	admin, err := service.NewVideoAdminService(f.f.App, 24, service.VideoAdminWriteOptions{ReasonProtector: protector, ModelDrafts: &service.VideoModelDraftOptions{}, ModelPublishing: &service.VideoModelPublishOptions{Provider: provider, ConfigVersion: "runware-fixture-v1", Models: models}})
	if err != nil {
		t.Fatal(err)
	}
	caller, err := f.f.JWT.Authenticate(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	details, err := admin.GetModelDraft(context.Background(), caller, f.f.ProjectID)
	if err != nil || details.SourceSHA256 == nil {
		t.Fatal("发布恢复夹具必须读取原草稿摘要")
	}
	definition := details.Definition
	docs, quick := "https://docs.example.invalid/video", "https://docs.example.invalid/video/quick"
	definition.DisplayName, definition.DocsURL, definition.QuickStartURL = "发布恢复模型", &docs, &quick
	definition.DocsURLHealthStatus, definition.QuickStartURLHealthStatus = "healthy", "healthy"
	definition.VideoContract = json.RawMessage(fmt.Sprintf(`{"schema_version":1,"purpose":"non_commercial_test_fixture","supported_operations":["text_to_video"],"default_model":%t,"asset_required":false,"required_entitlement_type":null,"required_membership_levels":[]}`, defaultModel))
	if draft, err := admin.SaveModelDraft(context.Background(), service.VideoModelDraftCommand{Caller: caller, ModelID: f.f.ProjectID, VersionNo: 0, SourceSHA256: *details.SourceSHA256, IdempotencyKey: "video-publish-recovery-adopt-001", Reason: "发布恢复前受控接管", Definition: definition}); err != nil || draft.VersionNo != 1 {
		t.Fatalf("发布恢复夹具接管失败：draft=%+v err=%v", draft, err)
	}
	g5 := handler.NewG5AdminHandler(service.NewG5AdminService(repository.NewG5AdminRepository(f.f.DB), repository.NewG3PricingRepository(f.f.DB)), nil).WithVideoPublications(handler.NewVideoAdminHandler(admin, f.f.JWT, true))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/admin/token/models/{id}/publish", g5.PublishModel)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return videoModelPublicationRecoveryFixture{base: f, admin: admin, provider: provider, server: server, caller: caller, models: models}
}

func (f videoModelPublicationRecoveryFixture) publish(t *testing.T, key string) (int, service.VideoModelPublicationReply) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"version_no": 1, "reason": "合成模型发布恢复原因"})
	req, _ := http.NewRequest(http.MethodPost, f.server.URL+"/api/admin/token/models/"+fmt.Sprint(f.base.f.ProjectID)+"/publish", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+f.base.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data service.VideoModelPublicationReply `json:"data"`
	}
	_ = json.Unmarshal(body, &envelope)
	return resp.StatusCode, envelope.Data
}

func (f videoModelPublicationRecoveryFixture) snapshot(t *testing.T) []byte {
	t.Helper()
	facts := map[string]any{"finance": json.RawMessage(f.base.f.FinancialSnapshot())}
	for _, table := range []string{"token_models", "ai_model_release_versions", "ai_video_model_draft_states", "ai_video_model_draft_commands", "audit_logs"} {
		query := f.base.f.DB.Table(table)
		switch table {
		case "token_models", "ai_model_release_versions", "ai_video_model_draft_states":
			query = query.Where("model_id=?", f.base.f.ProjectID)
			if table == "token_models" {
				query = f.base.f.DB.Table(table).Where("id=?", f.base.f.ProjectID)
			}
		case "ai_video_model_draft_commands":
			query = query.Where("model_id=?", f.base.f.ProjectID)
		case "audit_logs":
			query = query.Where("operator_id=? AND action LIKE 'video_model_%'", f.base.actor)
		}
		order := "id"
		if table == "ai_video_model_draft_states" {
			order = "model_id"
		}
		var rows []map[string]any
		if err := query.Order(order).Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		facts[table] = rows
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVideoG6ModelPublicationCommitUnknownMySQL(t *testing.T) {
	f := newVideoModelPublicationRecoveryFixture(t)
	pool := &adminCancelAckPool{ConnPool: f.base.f.DB.ConnPool}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.base.f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.base.f.UseApplicationDB(db))
	before := f.snapshot(t)
	status, _ := f.publish(t, "video-model-publish-commit-001")
	if status < 500 || !pool.lost.Load() {
		t.Fatalf("真实发布提交确认丢失必须返回5xx：status=%d lost=%t", status, pool.lost.Load())
	}
	afterUnknown := f.snapshot(t)
	if bytes.Equal(before, afterUnknown) {
		t.Fatal("确认丢失前必须已真实提交发布、命令和审计")
	}
	status, replay := f.publish(t, "video-model-publish-commit-001")
	if status != http.StatusOK || !replay.Idempotent || replay.PublicationStatus != "active" {
		t.Fatalf("发布确认未知重放必须恢复原结果：status=%d reply=%+v", status, replay)
	}
	if !bytes.Equal(afterUnknown, f.snapshot(t)) || f.provider.SubmitCalls() != 0 {
		t.Fatal("发布确认未知重放不得重复发布、审计或调用Provider")
	}
}

func TestVideoG6ModelPublicationSQLFailureRollbackMySQL(t *testing.T) {
	f := newVideoModelPublicationRecoveryFixture(t)
	before := f.snapshot(t)
	injected := false
	const hook = "g6_model_publication_command_failure"
	if err := f.base.f.DB.Callback().Create().After("gorm:create").Register(hook, func(tx *gorm.DB) {
		if tx.Error == nil && tx.Statement.Table == "ai_video_model_draft_commands" && !injected {
			injected = true
			tx.AddError(errors.New("合成发布命令末尾写失败"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	status, _ := f.publish(t, "video-model-publish-sql-fault-001")
	_ = f.base.f.DB.Callback().Create().Remove(hook)
	if status < 500 || !injected || !bytes.Equal(before, f.snapshot(t)) {
		t.Fatalf("发布末尾写失败必须全量回滚：status=%d injected=%t", status, injected)
	}
	status, reply := f.publish(t, "video-model-publish-sql-fault-001")
	if status != http.StatusCreated || reply.Idempotent || reply.PublicationStatus != "active" {
		t.Fatalf("移除故障后原键必须首次发布：status=%d reply=%+v", status, reply)
	}
}

func TestVideoG6ModelPublicationPermissionAndMFAExpiryMySQL(t *testing.T) {
	f := newVideoModelPublicationRecoveryFixture(t)
	before := f.snapshot(t)
	if err := f.base.f.DB.Exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=? AND permission_code='ai_gateway:model_manage'", f.base.actor).Error; err != nil {
		t.Fatal(err)
	}
	if status, _ := f.publish(t, "video-model-publish-deny-001"); status != http.StatusForbidden || !bytes.Equal(before, f.snapshot(t)) {
		t.Fatalf("权限deny必须403且零写入：status=%d", status)
	}
	if err := f.base.f.DB.Exec("UPDATE user_permission_overrides SET effect='allow' WHERE user_id=? AND permission_code='ai_gateway:model_manage'", f.base.actor).Error; err != nil {
		t.Fatal(err)
	}
	expired := time.Now().UTC().Add(-25 * time.Hour)
	if err := f.base.f.DB.Exec("UPDATE users SET admin_phone_verified_at=?,admin_email_verified_at=? WHERE id=?", expired, expired, f.base.actor).Error; err != nil {
		t.Fatal(err)
	}
	if status, _ := f.publish(t, "video-model-publish-mfa-expired-001"); status != http.StatusForbidden || !bytes.Equal(before, f.snapshot(t)) {
		t.Fatalf("MFA过期必须403且零写入：status=%d", status)
	}
	valid := time.Now().UTC().Add(-time.Minute)
	if err := f.base.f.DB.Exec("UPDATE users SET admin_phone_verified_at=?,admin_email_verified_at=? WHERE id=?", valid, valid, f.base.actor).Error; err != nil {
		t.Fatal(err)
	}
	if status, reply := f.publish(t, "video-model-publish-after-expiry-001"); status != http.StatusCreated || reply.PublicationStatus != "active" {
		t.Fatalf("恢复权限和MFA后必须发布：status=%d reply=%+v", status, reply)
	}
}

func cloneVideoDefaultPublicationModel(t *testing.T, f *videoModelPublicationRecoveryFixture) (uint64, string) {
	t.Helper()
	secondID := service.NextVideoFixtureUserID()
	secondCode := fmt.Sprintf("%s-default-%d", f.base.f.Model, secondID)
	var source model.TokenModel
	if err := f.base.f.DB.First(&source, f.base.f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	source.ID, source.LogicalModelCode, source.DisplayName = secondID, secondCode, "第二默认视频模型"
	source.CreatedAt, source.UpdatedAt = time.Time{}, time.Time{}
	if err := f.base.f.DB.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	var release model.AIModelReleaseVersion
	if err := f.base.f.DB.Where("model_id=? AND version_no=1", f.base.f.ProjectID).Take(&release).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if json.Unmarshal(release.SnapshotJSON, &snapshot) != nil {
		t.Fatal("原发布快照必须可解码")
	}
	snapshot["logical_model_code"], snapshot["display_name"] = secondCode, source.DisplayName
	release.ID, release.ModelID, release.CreatedAt, release.RetiredAt = 0, secondID, time.Time{}, nil
	release.SnapshotJSON, _ = json.Marshal(snapshot)
	if err := f.base.f.DB.Create(&release).Error; err != nil {
		t.Fatal(err)
	}
	var price model.AIPriceVersion
	if err := f.base.f.DB.Where("logical_model_code=? AND status='active'", f.base.f.Model).Order("version_no DESC").Take(&price).Error; err != nil {
		t.Fatal(err)
	}
	oldPriceID := price.ID
	// 视频价格的token上限为SQL NULL；使用INSERT SELECT保留NULL，不能让Go零值重写为0触发约束。
	insertPrice := f.base.f.DB.Exec(`INSERT INTO ai_price_versions(logical_model_code,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,expires_at,created_by,approved_by,approved_at,published_at,suspended_reason,created_at,updated_at)
SELECT ?,capability,pricing_template,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,limits_json,minimum_charge,cost_source,cost_source_version,price_purpose,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,expires_at,created_by,approved_by,approved_at,published_at,suspended_reason,UTC_TIMESTAMP(),UTC_TIMESTAMP() FROM ai_price_versions WHERE id=?`, secondCode, oldPriceID)
	if insertPrice.Error != nil || insertPrice.RowsAffected != 1 {
		t.Fatalf("第二模型价格复制必须影响一行：source_id=%d rows=%d err=%v", oldPriceID, insertPrice.RowsAffected, insertPrice.Error)
	}
	price = model.AIPriceVersion{}
	if err := f.base.f.DB.Where("logical_model_code=? AND status='active'", secondCode).Take(&price).Error; err != nil {
		var candidates []map[string]any
		_ = f.base.f.DB.Table("ai_price_versions").Select("id,logical_model_code,status,version_no").Where("logical_model_code=? OR id=?", secondCode, oldPriceID).Order("id").Find(&candidates).Error
		t.Fatalf("第二模型active价格复制后不可读：source_id=%d candidates=%v err=%v", oldPriceID, candidates, err)
	}
	var skus []model.AIPriceSKU
	if err := f.base.f.DB.Where("price_version_id=?", oldPriceID).Order("id").Find(&skus).Error; err != nil {
		t.Fatal(err)
	}
	for index := range skus {
		skus[index].ID, skus[index].PriceVersionID, skus[index].CreatedAt = 0, price.ID, time.Time{}
		if err := f.base.f.DB.Create(&skus[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.models[secondCode] = "runway:1@2"
	if !service.AddVideoModelPublicationMappingForTest(f.admin, secondCode, "runway:1@2") {
		t.Fatal("第二模型测试映射未进入冻结发布配置")
	}
	details, err := f.admin.GetModelDraft(context.Background(), f.caller, secondID)
	if err != nil || details.SourceSHA256 == nil {
		t.Fatalf("第二模型草稿读取失败：%v", err)
	}
	definition := details.Definition
	definition.VideoContract = json.RawMessage(`{"schema_version":1,"purpose":"non_commercial_test_fixture","supported_operations":["text_to_video"],"default_model":true,"asset_required":false,"required_entitlement_type":null,"required_membership_levels":[]}`)
	if draft, err := f.admin.SaveModelDraft(context.Background(), service.VideoModelDraftCommand{Caller: f.caller, ModelID: secondID, VersionNo: 0, SourceSHA256: *details.SourceSHA256, IdempotencyKey: "video-publish-second-default-adopt", Reason: "第二默认模型受控接管", Definition: definition}); err != nil || draft.VersionNo != 1 {
		t.Fatalf("第二模型草稿接管失败：draft=%+v err=%v", draft, err)
	}
	return secondID, secondCode
}

func TestVideoG6ModelPublicationConcurrentDefaultMySQL(t *testing.T) {
	f := newVideoModelPublicationRecoveryFixtureWithDefault(t, true)
	secondID, _ := cloneVideoDefaultPublicationModel(t, &f)
	// 全包复用同一临时数据库，较早用例可能已留下自己的合成默认模型。
	// 本用例先可恢复地停用这些既有默认，才能只裁决当前两个候选且不依赖测试顺序。
	var priorDefaults []uint64
	if err := f.base.f.DB.Table("token_models AS m").
		Select("m.id").
		Joins("JOIN ai_model_release_versions r ON r.model_id=m.id AND r.version_no=m.release_version_no AND r.status='active'").
		Where("m.id NOT IN ? AND m.modality='video' AND m.status='active' AND JSON_UNQUOTE(JSON_EXTRACT(r.snapshot_json,'$.video_contract.default_model'))='true'", []uint64{f.base.f.ProjectID, secondID}).
		Pluck("m.id", &priorDefaults).Error; err != nil {
		t.Fatal(err)
	}
	if len(priorDefaults) > 0 {
		if err := f.base.f.DB.Model(&model.TokenModel{}).Where("id IN ?", priorDefaults).Update("status", "inactive").Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		// 先停用本用例候选，再恢复旧默认，避免清理阶段制造两个active默认模型。
		_ = f.base.f.DB.Model(&model.TokenModel{}).Where("id IN ?", []uint64{f.base.f.ProjectID, secondID}).Update("status", "inactive").Error
		if len(priorDefaults) > 0 {
			_ = f.base.f.DB.Model(&model.TokenModel{}).Where("id IN ?", priorDefaults).Update("status", "active").Error
		}
	})
	finance := f.base.f.FinancialSnapshot()
	commands := []service.VideoModelPublicationCommand{
		{Caller: f.caller, ModelID: f.base.f.ProjectID, VersionNo: 1, Action: "publish", IdempotencyKey: "video-default-race-first", Reason: "第一默认模型并发发布"},
		{Caller: f.caller, ModelID: secondID, VersionNo: 1, Action: "publish", IdempotencyKey: "video-default-race-second", Reason: "第二默认模型并发发布"},
	}
	start := make(chan struct{})
	type answer struct {
		reply *service.VideoModelPublicationReply
		err   error
	}
	answers := make(chan answer, len(commands))
	var wg sync.WaitGroup
	for _, command := range commands {
		wg.Add(1)
		go func(c service.VideoModelPublicationCommand) {
			defer wg.Done()
			<-start
			reply, err := f.admin.ManageModelPublication(context.Background(), c)
			answers <- answer{reply: reply, err: err}
		}(command)
	}
	close(start)
	wg.Wait()
	close(answers)
	var succeeded, conflicted int
	for result := range answers {
		switch {
		case result.err == nil && result.reply != nil && result.reply.PublicationStatus == "active":
			succeeded++
		case errors.Is(result.err, service.ErrVideoAdminCommandConflict) && result.reply == nil:
			conflicted++
		default:
			t.Fatalf("默认模型并发返回异常：reply=%+v err=%v", result.reply, result.err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("默认模型并发必须一胜一冲突：success=%d conflict=%d", succeeded, conflicted)
	}
	var activeDefaults int64
	if err := f.base.f.DB.Table("token_models AS m").Joins("JOIN ai_model_release_versions r ON r.model_id=m.id AND r.version_no=m.release_version_no AND r.status='active'").Where("m.modality='video' AND m.status='active' AND JSON_UNQUOTE(JSON_EXTRACT(r.snapshot_json,'$.video_contract.default_model'))='true'").Count(&activeDefaults).Error; err != nil || activeDefaults != 1 {
		t.Fatalf("数据库只能有一个active默认视频模型：count=%d err=%v", activeDefaults, err)
	}
	var versions []uint64
	if err := f.base.f.DB.Table("ai_video_model_draft_states").Where("model_id IN ?", []uint64{f.base.f.ProjectID, secondID}).Order("model_id").Pluck("version_no", &versions).Error; err != nil || len(versions) != 2 || versions[0]+versions[1] != 3 {
		t.Fatalf("赢家版本2、输家版本1必须保留：versions=%v err=%v", versions, err)
	}
	if f.provider.SubmitCalls() != 0 || !bytes.Equal(finance, f.base.f.FinancialSnapshot()) {
		t.Fatal("默认模型并发不能调用Provider或改变财务")
	}
}
