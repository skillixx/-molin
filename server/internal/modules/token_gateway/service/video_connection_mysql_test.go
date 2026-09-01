package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/repository"
)

// 单连接最小反例精确验证自动报价持有事务后不能再借另一连接，不靠扩大连接池通过。
func TestVideoG6AutoQuoteSingleConnectionMySQL(t *testing.T) {
	db := openVideoG6MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	reader, ok := f.quotes.pricing.repo.(*fakeActivePriceReader)
	if !ok {
		t.Fatal("测试夹具读取器不匹配")
	}
	for _, sku := range reader.skus {
		if err := db.Create(&sku).Error; err != nil {
			t.Fatal(err)
		}
	}
	id := f.owner.UserID
	code := f.command.FingerprintInput.LogicalModelCode
	contract := json.RawMessage(videoG6NoEntitlementContract)
	snapshot, err := json.Marshal(map[string]any{"logical_model_code": code, "modality": "video", "capabilities": []string{"video.generate"}, "visible_scope": "all", "video_contract": contract})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{"UPDATE api_keys SET video_generate_allowed=1 WHERE id=?", []any{id}},
		{"INSERT INTO ai_project_model_capability_grants(user_id,project_id,logical_model_code,capability,status,granted_by,created_at,updated_at) VALUES(?,?,?,'video.generate','active',?,UTC_TIMESTAMP(),UTC_TIMESTAMP())", []any{id, id, code, id}},
		{"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='video:generate'", []any{id}},
		{"INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) VALUES(?,1,'active',?,'单连接隔离验证',?,UTC_TIMESTAMP())", []any{id, string(snapshot), id}},
	} {
		if err := db.Exec(statement.sql, statement.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.service.access = NewVideoAccessService(db)
	f.quotes.pricing.repo = repository.NewG3PricingRepository(db)
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := f.service.CreateWithAutomaticQuote(ctx, VideoFacadeRequest{Prompt: f.command.Prompt, RightsPolicyVersion: f.command.RightsPolicyVersion, IdempotencyKey: "g6-single-connection-create", RequestID: f.command.RequestID, TaskID: f.command.TaskID, FingerprintInput: f.command.FingerprintInput}, f.quotes)
	if err != nil || result == nil {
		t.Fatalf("自动报价不能依赖第二连接：%v", err)
	}
	var holds int64
	if err := db.Table("wallet_holds").Where("user_id=?", id).Count(&holds).Error; err != nil || holds != 1 {
		t.Fatal("单连接未形成唯一预占")
	}
}
