package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/repository"
)

// 完成态列表实际执行17类财务对账；两个任务共享合成钱包，不能用queued页面替代锁竞争验证。
func TestVideoG6CompletedListsMySQLConcurrency(t *testing.T) {
	db := openVideoG6MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	reader := f.quotes.pricing.repo.(*fakeActivePriceReader)
	for _, sku := range reader.skus {
		if err := db.Create(&sku).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.quotes.pricing.repo = repository.NewG3PricingRepository(db)
	id, code := f.owner.UserID, f.command.FingerprintInput.LogicalModelCode
	snapshot, err := json.Marshal(map[string]any{"logical_model_code": code, "modality": "video", "capabilities": []string{"video.generate"}, "visible_scope": "all", "video_contract": json.RawMessage(videoG6NoEntitlementContract)})
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{"UPDATE api_keys SET video_generate_allowed=1 WHERE id=?", []any{id}},
		{"INSERT INTO ai_project_model_capability_grants(user_id,project_id,logical_model_code,capability,status,granted_by,created_at,updated_at) VALUES(?,?,?,'video.generate','active',?,UTC_TIMESTAMP(),UTC_TIMESTAMP())", []any{id, id, code, id}},
		{"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='video:generate'", []any{id}},
		{"INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) VALUES(?,1,'active',?,'完成态列表合成测试',?,UTC_TIMESTAMP())", []any{id, string(snapshot), id}},
	} {
		if err := db.Exec(stmt.sql, stmt.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.service.access = NewVideoAccessService(db)
	for i := 0; i < 2; i++ {
		item := f
		item.command.RequestID = fmt.Sprintf("vid_req_g6_list_%d_%d", id, i)
		item.command.TaskID = fmt.Sprintf("video_g6_list_%d_%d", id, i)
		item.command.IdempotencyKey = fmt.Sprintf("g6-list-generation-%d", i)
		quote, _, err := f.quotes.CreateQuote(context.Background(), VideoCreateQuoteCommand{CommandKind: "quote", IdempotencyKey: fmt.Sprintf("g6-list-quote-%d", i), FingerprintInput: item.command.FingerprintInput})
		if err != nil {
			t.Fatal(err)
		}
		item.command.QuotePublicID, item.quote = quote.PublicID, quote
		if _, err := item.service.ReserveAndCreate(context.Background(), item.command); err != nil {
			t.Fatal(err)
		}
		_, adapter := runVideoG5ReadyFixture(t, item)
		if _, err := item.service.SettleReady(context.Background(), item.command.TaskID, item.owner); err != nil {
			t.Fatal(err)
		}
		if _, err := item.service.DeliverReady(context.Background(), item.command.TaskID, item.owner); err != nil {
			t.Fatal(err)
		}
		if adapter.SubmitCalls() != 1 {
			t.Fatal("建立完成态夹具不得重复提交Fake Provider")
		}
	}
	app, err := NewVideoHTTPService(db, VideoBillingOptions{QuoteSecret: f.service.quoteSecret, PromptSecret: f.service.promptSecret, IntentSecret: f.service.intentSecret, Protector: f.service.protector, Safety: f.service.safety})
	if err != nil {
		t.Fatal(err)
	}
	caller := VideoCaller{UserID: id, ProjectID: id, APIKeyID: id}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	baseline, err := app.ListVideos(ctx, caller, VideoListQuery{})
	if err != nil || len(baseline.Data) != 2 || baseline.Data[0].Status != "completed" || baseline.Data[1].Status != "completed" {
		t.Fatalf("先证明夹具为真实可交付completed页面：%v", err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			order := "asc"
			if index%2 == 1 {
				order = "desc"
			}
			result, err := app.ListVideos(ctx, caller, VideoListQuery{Order: order})
			if err != nil || result == nil || len(result.Data) != 2 || result.Data[0].Status != "completed" || result.Data[1].Status != "completed" {
				t.Errorf("相反排序并发不能使完整页面失败：order=%s err=%v", order, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	// 平台详情列表同样执行真实对账；独立期限避免前一轮压力耗尽本轮上下文。
	platformCtx, platformCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer platformCancel()
	platformStart := make(chan struct{})
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-platformStart
			page, err := app.ListPlatformTasks(platformCtx, caller, 1, 20)
			if err != nil || page == nil || page.Total != 2 || len(page.Items) != 2 {
				t.Errorf("平台完成态列表100并发必须返回同一两条事实：%+v err=%v", page, err)
				return
			}
			for _, item := range page.Items {
				if item.ExecutionStatus != "succeeded" || item.BillingStatus != "settled" || item.DeliveryStatus != "available" || !item.CanDeliver || item.HeldAmount == nil || *item.HeldAmount != "0.50000000" || item.SettledAmount == nil || *item.SettledAmount != "0.50000000" || item.CurrentFrozenAmount == nil || *item.CurrentFrozenAmount != "0.00000000" || item.NetReleasedAmount == nil || *item.NetReleasedAmount != "0.00000000" {
					t.Errorf("平台列表不得混用公开状态或误把全额解冻显示为退款：%+v", item)
				}
			}
		}()
	}
	close(platformStart)
	wg.Wait()
	page, err := app.ListPlatformTasks(context.Background(), caller, 2, 2)
	if err != nil || page.Total != 2 || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("超出末页仍须保留总数及空数组：%+v err=%v", page, err)
	}
}
