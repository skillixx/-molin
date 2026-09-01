package service_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	billingmodel "molin/server/internal/modules/billing/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6ImportHTTPMySQL(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, f.App, f.Keys, true, f.JWT)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	type reply struct {
		status int
		header http.Header
		code   int
		kind   string
		data   json.RawMessage
		err    error
	}
	call := func(method, path, key, credential, body string) reply {
		r, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			return reply{err: err}
		}
		r.Header.Set("Authorization", "Bearer "+credential)
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		res, err := client.Do(r)
		if err != nil {
			return reply{err: err}
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			return reply{err: err}
		}
		for _, forbidden := range []string{"\"bucket\"", "\"object_key\"", "\"key_hash\"", "\"source_sha256\"", "导入HTTP合成视频Prompt"} {
			if bytes.Contains(raw, []byte(forbidden)) {
				return reply{err: fmt.Errorf("响应泄露禁止字段")}
			}
		}
		var env struct {
			Code      int             `json:"code"`
			Kind      string          `json:"error"`
			Data      json.RawMessage `json:"data"`
			RequestID string          `json:"request_id"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			return reply{err: err}
		}
		if env.RequestID == "" || env.RequestID != res.Header.Get("X-Request-ID") {
			return reply{err: fmt.Errorf("HTTP追踪ID不一致")}
		}
		return reply{status: res.StatusCode, header: res.Header, code: env.Code, kind: env.Kind, data: env.Data}
	}
	check := func(r reply, status, code int, kind string) {
		t.Helper()
		if r.err != nil || r.status != status || r.code != code || r.kind != kind {
			t.Fatalf("HTTP合同不符：status=%d code=%d kind=%s err=%v", r.status, r.code, r.kind, r.err)
		}
		if status >= 400 && string(r.data) != "null" {
			t.Fatal("错误data必须null")
		}
	}
	decode := func(r reply) service.VideoInputImportReply {
		t.Helper()
		var fields map[string]json.RawMessage
		var d service.VideoInputImportReply
		if json.Unmarshal(r.data, &fields) != nil || len(fields) != 5 || json.Unmarshal(r.data, &d) != nil {
			t.Fatal("导入DTO必须固定五键")
		}
		for _, k := range []string{"import_id", "status", "input_asset_id", "processing_expires_at", "idempotent"} {
			if value, ok := fields[k]; !ok || (k != "input_asset_id" && bytes.Equal(value, []byte("null"))) {
				t.Fatalf("导入DTO缺字段%s", k)
			}
		}
		if d.ImportID == "" || d.ProcessingExpiresAt.IsZero() || !d.ProcessingExpiresAt.After(time.Now()) {
			t.Fatal("导入关联ID与处理期限必须有效，不能以null/零值冒充重放一致")
		}
		return d
	}
	path := "/api/token/video-inputs/from-image-asset"
	body := fmt.Sprintf(`{"source_asset_id":%q}`, f.SourceID)
	key := "g6-import-http-create-0001"
	counts := func() map[string]int64 {
		t.Helper()
		result := map[string]int64{}
		for _, table := range []string{"ai_requests", "ai_gateway_quotes", "ai_gateway_tasks", "wallet_holds", "wallet_transactions"} {
			var n int64
			if err := f.DB.Table(table).Where("user_id=?", f.ProjectID).Count(&n).Error; err != nil {
				t.Fatal(err)
			}
			result[table] = n
		}
		for _, table := range []string{"ai_usage_items", "ai_request_wallet_links"} {
			var n int64
			if err := f.DB.Table(table).Where("request_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)", f.ProjectID).Count(&n).Error; err != nil {
				t.Fatal(err)
			}
			result[table] = n
		}
		var outbox int64
		if err := f.DB.Table("ai_outbox_events").Where("aggregate_id IN (SELECT request_id FROM ai_requests WHERE user_id=?)", f.ProjectID).Count(&outbox).Error; err != nil {
			t.Fatal(err)
		}
		result["ai_outbox_events"] = outbox
		return result
	}
	before := counts()
	listed := call("GET", "/api/token/video-input-source-images?page=1&page_size=20", "", f.Key, "")
	check(listed, 200, 0, "")
	var candidates service.VideoSourceImagePage
	if json.Unmarshal(listed.data, &candidates) != nil || candidates.Total != 1 || len(candidates.Items) != 1 || candidates.Items[0].AssetID != f.SourceID || candidates.Items[0].Width != 640 || candidates.Items[0].Height != 640 {
		t.Fatal("实际HTTP必须列出已结算且归属正确的来源主图")
	}
	var candidateFields struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if json.Unmarshal(listed.data, &candidateFields) != nil || len(candidateFields.Items) != 1 || len(candidateFields.Items[0]) != 7 {
		t.Fatal("来源候选只允许七个公开字段")
	}
	for _, key := range []string{"asset_id", "mime_type", "size_bytes", "width", "height", "version_no", "expires_at"} {
		v, ok := candidateFields.Items[0][key]
		if !ok || bytes.Equal(v, []byte("null")) {
			t.Fatalf("来源字段缺失或为空：%s", key)
		}
	}
	otherList := call("GET", "/api/token/video-input-source-images", "", f.OtherKey, "")
	check(otherList, 200, 0, "")
	var invisible service.VideoSourceImagePage
	if json.Unmarshal(otherList.data, &invisible) != nil || invisible.Total != 0 || invisible.Items == nil || len(invisible.Items) != 0 {
		t.Fatal("来源列表与total不能泄露其他Key图片")
	}
	var allOutboxBefore int64
	if err := f.DB.Table("ai_outbox_events").Count(&allOutboxBefore).Error; err != nil {
		t.Fatal(err)
	}
	walletAmounts := func() string {
		t.Helper()
		var row struct{ BalanceAmount, FrozenAmount string }
		if err := f.DB.Table("wallets").Select("balance_amount,frozen_amount").Where("user_id=?", f.ProjectID).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row.BalanceAmount + "/" + row.FrozenAmount
	}
	walletBefore := walletAmounts()
	check(call("POST", path, key, f.OtherKey, body), 404, 40400, "video_input_not_found")
	check(call("POST", path, key, f.Token, fmt.Sprintf(`{"project_id":%d,"source_asset_id":%q}`, f.ProjectID, f.SourceID)), 404, 40400, "video_input_not_found")
	check(call("POST", path, key, f.Key, fmt.Sprintf(`{"source_asset_id":%q,"bucket":"forged"}`, f.SourceID)), 400, 40000, "invalid_request_error")
	entered, release := f.PauseRead()
	defer release()
	first := make(chan reply, 1)
	go func() { first <- call("POST", path, key, f.Key, body) }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("HTTP导入未进入真实存储边界")
	}
	processing := call("POST", path, key, f.Key, body)
	check(processing, 202, 0, "")
	pending := decode(processing)
	if pending.Status != "processing" || pending.InputAssetID != nil || !pending.Idempotent || pending.ImportID == "" || processing.header.Get("Retry-After") != "1" {
		t.Fatal("处理中合同错误")
	}
	release()
	var created reply
	select {
	case created = <-first:
	case <-time.After(15 * time.Second):
		t.Fatal("导入未结束")
	}
	check(created, 201, 0, "")
	done := decode(created)
	if done.Status != "completed" || done.InputAssetID == nil || done.ImportID != pending.ImportID || done.Idempotent {
		t.Fatal("首次完成未绑定原命令")
	}
	replay := call("POST", path, key, f.Key, body)
	check(replay, 200, 0, "")
	same := decode(replay)
	if !same.Idempotent || same.InputAssetID == nil || *same.InputAssetID != *done.InputAssetID || !same.ProcessingExpiresAt.Equal(done.ProcessingExpiresAt) {
		t.Fatal("重放不能另建输入或续期")
	}
	check(call("POST", path, key, f.Key, `{"source_asset_id":"image_other_fixture"}`), 409, 40900, "video_input_import_conflict")
	if !reflect.DeepEqual(before, counts()) || walletAmounts() != walletBefore {
		t.Fatal("导入不能写入财务、用量、Outbox或任务事实")
	}
	var allOutboxAfter int64
	if err := f.DB.Table("ai_outbox_events").Count(&allOutboxAfter).Error; err != nil || allOutboxAfter != allOutboxBefore {
		t.Fatal("导入不能新建任何Outbox事件，包含无原request归属的事件")
	}
	check(call("POST", fmt.Sprintf("/api/token/projects/%d/video-rights-acceptance", f.ProjectID), "g6-import-http-rights-0001", f.Token, fmt.Sprintf(`{"rights_policy_version":%q,"rights_confirmed":true}`, f.Policy)), 201, 0, "")
	i2v := fmt.Sprintf(`{"model":%q,"operation":"image_to_video","prompt":"导入HTTP合成视频Prompt","input_asset_id":%q,"rights_attestation":true}`, f.Model, *done.InputAssetID)
	quoted := call("POST", "/api/token/videos/quotes", "g6-import-http-quote-0001", f.Key, i2v)
	check(quoted, 201, 0, "")
	var quote service.VideoHTTPQuote
	if json.Unmarshal(quoted.data, &quote) != nil || quote.QuoteID == "" || quote.QuotedAmount != "0.75000000" || quote.Currency != "CNY" {
		t.Fatal("导入输入不能报价")
	}
	gen := strings.TrimSuffix(i2v, "}") + fmt.Sprintf(`,"quote_id":%q}`, quote.QuoteID)
	generated := call("POST", "/api/token/videos/generations", "g6-import-http-generation", f.Key, gen)
	check(generated, 202, 0, "")
	var task service.VideoHTTPGeneration
	if json.Unmarshal(generated.data, &task) != nil || task.Job.Status != "queued" || task.HeldAmount != "0.75000000" || task.RequestID == "" || generated.header.Get("X-Molin-Request-ID") != task.RequestID {
		t.Fatal("导入输入未进入原G5任务")
	}
	after := counts()
	for _, table := range []string{"ai_requests", "ai_gateway_quotes", "ai_gateway_tasks", "wallet_holds", "wallet_transactions"} {
		if after[table] != before[table]+1 {
			t.Fatalf("生成必须恰好新增一组原账本事实：%s", table)
		}
	}
	if after["ai_request_wallet_links"] != before["ai_request_wallet_links"]+1 || after["ai_outbox_events"] != before["ai_outbox_events"]+1 || after["ai_usage_items"] != before["ai_usage_items"] {
		t.Fatal("预占必须仅增加原钱包关联与held Outbox，不提前结算Usage")
	}
	var link model.AIRequestWalletLink
	if err := f.DB.Where("request_id=?", task.RequestID).Take(&link).Error; err != nil || link.WalletHoldID == 0 || link.HoldTransactionID == 0 || link.HeldAmount.StringFixed(8) != "0.75000000" {
		t.Fatal("生成必须关联真实0.75预占与冻结流水")
	}
	var hold billingmodel.WalletHold
	if err := f.DB.First(&hold, link.WalletHoldID).Error; err != nil || hold.Status != billingmodel.HoldStatusHolding || hold.UserID != f.ProjectID || hold.HoldAmount.StringFixed(8) != "0.75000000" || hold.FreezeTxnID == nil || *hold.FreezeTxnID != link.HoldTransactionID {
		t.Fatal("真实Hold状态、归属、金额和冻结流水必须一致")
	}
	var freeze billingmodel.WalletTransaction
	if err := f.DB.First(&freeze, link.HoldTransactionID).Error; err != nil || freeze.Type != "freeze" || freeze.Direction != "out" || freeze.UserID != f.ProjectID || freeze.WalletID != hold.WalletID || freeze.Amount.StringFixed(8) != "0.75000000" {
		t.Fatal("必须实际创建0.75元freeze/out流水，不能只增加计数")
	}
	var event model.AIOutboxEvent
	if err := f.DB.Where("aggregate_id=? AND event_type='video_billing_held'", task.RequestID).Take(&event).Error; err != nil || event.AggregateType != "video_request" || event.Status != "pending" {
		t.Fatal("必须持久化原请求的待发送held事件")
	}
	var payload struct {
		RequestID string `json:"request_id"`
		Status    string `json:"status"`
		Amount    string `json:"amount"`
		Currency  string `json:"currency"`
		Operation string `json:"operation"`
		Version   int    `json:"version"`
	}
	if json.Unmarshal(event.PayloadJSON, &payload) != nil || payload.RequestID != task.RequestID || payload.Status != "held" || payload.Amount != "0.75000000" || payload.Currency != "CNY" || payload.Operation != "image_to_video" || payload.Version != 1 {
		t.Fatal("held Outbox低敏载荷必须与真实预占一致")
	}
	if f.ProviderCalls() != 1 {
		t.Fatal("导入、报价和预占不能重新调用图片Provider")
	}
	readBaseline := counts()
	readWallet := walletAmounts()
	var canonical service.VideoTaskDetails
	for index, path := range []string{"/api/token/video-tasks/" + task.Job.ID, "/api/token/videos/requests/" + task.RequestID, "/api/token/videos/requests/by-video/" + task.Job.ID} {
		response := call("GET", path, "", f.Key, "")
		check(response, 200, 0, "")
		var fields map[string]json.RawMessage
		if json.Unmarshal(response.data, &fields) != nil || len(fields) != 25 {
			t.Fatal("任务响应必须恰好25字段，不得混入内部正文或位置")
		}
		for _, key := range []string{"task_id", "video_id", "request_id", "quote_id", "model", "operation", "execution_status", "billing_status", "delivery_status", "progress", "version_no", "request_version_no", "quoted_amount", "held_amount", "current_frozen_amount", "settled_amount", "net_released_amount", "hold_status", "currency", "created_at", "completed_at", "media_deleted", "media_partially_deleted", "media_deletion_pending", "can_deliver"} {
			if _, ok := fields[key]; !ok {
				t.Fatalf("任务响应缺少必需字段：%s", key)
			}
		}
		if string(fields["settled_amount"]) != "null" || string(fields["completed_at"]) != "null" {
			t.Fatal("未完成任务必须显式保留null字段")
		}
		if string(fields["media_partially_deleted"]) != "false" || string(fields["media_deletion_pending"]) != "false" {
			t.Fatal("未执行删除的I2V任务必须显式返回false，不能缺失或使用null")
		}
		var detail service.VideoTaskDetails
		if json.Unmarshal(response.data, &detail) != nil || detail.TaskID != task.Job.ID || detail.VideoID != task.Job.ID || detail.RequestID != task.RequestID || detail.QuoteID != quote.QuoteID || detail.ExecutionStatus != "reserved" || detail.BillingStatus != "held" || detail.DeliveryStatus != "pending" || detail.HeldAmount == nil || *detail.HeldAmount != "0.75000000" || detail.CurrentFrozenAmount == nil || *detail.CurrentFrozenAmount != "0.75000000" || detail.SettledAmount != nil || detail.CanDeliver || detail.MediaDeleted {
			t.Fatal("平台三种ID查询必须保持同一任务和原三轴/金额事实")
		}
		if index == 0 {
			canonical = detail
		} else if !reflect.DeepEqual(canonical, detail) {
			t.Fatal("Task/request/video查询不能拼接不同快照")
		}
		check(call("GET", path, "", f.OtherKey, ""), 404, 40400, "video_not_found")
		check(call("GET", path, "", f.Token, ""), 404, 40400, "video_not_found")
	}
	list := call("GET", "/api/token/video-tasks?page=1&page_size=20", "", f.Key, "")
	check(list, 200, 0, "")
	var tasks service.VideoTaskPage
	if json.Unmarshal(list.data, &tasks) != nil || tasks.Page != 1 || tasks.PageSize != 20 || tasks.Total != 1 || len(tasks.Items) != 1 || !reflect.DeepEqual(tasks.Items[0], canonical) {
		t.Fatal("平台任务列表应为D-95且与详情一致")
	}
	events := call("GET", "/api/token/video-tasks/"+task.Job.ID+"/events", "", f.Key, "")
	check(events, 200, 0, "")
	var history service.VideoTaskEventPage
	if json.Unmarshal(events.data, &history) != nil || history.Total != 1 || len(history.Items) != 1 || history.Items[0].Axis != "execution" || history.Items[0].EventType != "execution_status_changed" || !strings.HasPrefix(history.Items[0].EventID, "vevt_") {
		t.Fatal("事件需归一化状态轴和公开ID")
	}
	var storedTask model.AIImageTask
	if err := f.DB.Where("public_id=?", task.Job.ID).Take(&storedTask).Error; err != nil {
		t.Fatal(err)
	}
	from, to := "quoted", "held"
	// 数据库首先拒绝任意诊断正文；查询层仅测试数据库允许落库的低敏事件，不绕过既有防线。
	invalidEvent := model.AIGatewayTaskEvent{EventID: fmt.Sprintf("invalid_diagnostic_%d", f.ProjectID), TaskID: storedTask.ID, UserID: f.ProjectID, ProjectID: f.ProjectID, EventType: "unknown_diagnostic", Source: "system", CreatedAt: canonical.CreatedAt, SafeDetailJSON: json.RawMessage(`{"diagnostic":"fixture_internal_only"}`)}
	if err := f.DB.Create(&invalidEvent).Error; err == nil {
		t.Fatal("普通TaskEvent不得持久化任意诊断正文")
	}
	for index, kind := range []string{"provider_cost_succeeded", "unknown_diagnostic", "BILLING_STATUS_CHANGED", "execution_status_changed", "billing_status_changed"} {
		e := model.AIGatewayTaskEvent{EventID: fmt.Sprintf("internal_event_marker_%d_%d", f.ProjectID, index), TaskID: storedTask.ID, UserID: f.ProjectID, ProjectID: f.ProjectID, EventType: kind, Source: "system", FromStatus: &from, ToStatus: &to, CreatedAt: canonical.CreatedAt, SafeDetailJSON: json.RawMessage(`{"reason":"state_advanced"}`)}
		if err := f.DB.Create(&e).Error; err != nil {
			t.Fatal(err)
		}
	}
	legacyFrom, legacyTo := "held", "exception"
	legacyEvent := model.AIGatewayTaskEvent{EventID: fmt.Sprintf("legacy_exception_%d", f.ProjectID), TaskID: storedTask.ID, UserID: f.ProjectID, ProjectID: f.ProjectID, EventType: "billing_status_changed", Source: "system", FromStatus: &legacyFrom, ToStatus: &legacyTo, CreatedAt: canonical.CreatedAt}
	if err := f.DB.Create(&legacyEvent).Error; err != nil {
		t.Fatal(err)
	}
	secondEvents := call("GET", "/api/token/video-tasks/"+task.Job.ID+"/events?page=2&page_size=1", "", f.Key, "")
	check(secondEvents, 200, 0, "")
	var second service.VideoTaskEventPage
	if json.Unmarshal(secondEvents.data, &second) != nil || second.Total != 2 || len(second.Items) != 1 || second.Items[0].Axis != "billing" || second.Items[0].EventType != "billing_status_changed" || second.Items[0].FromStatus == nil || *second.Items[0].FromStatus != "quoted" || bytes.Contains(secondEvents.data, []byte("internal_event_marker")) || bytes.Contains(secondEvents.data, []byte("state_advanced")) {
		t.Fatal("成本、未知、大小写及错误轴事件须隐藏，不能泄露内部ID或详情")
	}
	if again := call("GET", "/api/token/video-tasks/"+task.Job.ID+"/events?page=2&page_size=1", "", f.Key, ""); again.err != nil || !bytes.Equal(again.data, secondEvents.data) {
		t.Fatal("同时间事件分页与公开ID必须稳定")
	}
	var eventCount int64
	if err := f.DB.Model(&model.AIGatewayTaskEvent{}).Where("task_id=?", storedTask.ID).Count(&eventCount).Error; err != nil || eventCount != 7 {
		t.Fatal("事件查询不能修改或删除原始事实")
	}
	check(call("GET", "/api/token/video-tasks/"+task.Job.ID+"/events", "", f.OtherKey, ""), 404, 40400, "video_not_found")
	for _, path := range []string{
		"/api/token/video-tasks?page_size=101", "/api/token/video-tasks?page=10001",
		"/api/token/video-tasks?page=0", "/api/token/video-tasks?page=-1",
		"/api/token/video-tasks?page=1&page=2", "/api/token/video-tasks?page_size=",
		"/api/token/video-tasks?unknown=1", "/api/token/video-tasks?page=1.5",
		"/api/token/video-tasks?project_id=1&project_id=2",
		"/api/token/video-tasks/" + task.Job.ID + "?project_id=1",
		"/api/token/video-tasks/" + task.Job.ID + "/events?project_id=1",
		"/api/token/videos/requests/" + task.RequestID + "?page=1",
		"/api/token/videos/requests/by-video/" + task.Job.ID + "?unknown=1",
	} {
		check(call("GET", path, "", f.Key, ""), 400, 40000, "invalid_request_error")
	}
	check(call("GET", "/api/token/video-tasks", "", f.Token, ""), 400, 40000, "project_required")
	check(call("GET", "/api/token/videos/requests/"+task.Job.ID, "", f.Key, ""), 404, 40400, "video_not_found")
	check(call("GET", "/api/token/video-tasks/"+task.RequestID, "", f.Key, ""), 404, 40400, "video_not_found")
	if !reflect.DeepEqual(readBaseline, counts()) || readWallet != walletAmounts() || f.ProviderCalls() != 1 {
		t.Fatal("平台只读查询不能推进财务/Outbox或调用Provider")
	}
	// 删除申请在真实HTTP中形成原输入的pending_delete，仍保留正在预占的I2V绑定和全部财务事实。
	var deletingInput model.AIGatewayInputAsset
	if err := f.DB.Where("public_id=?", *done.InputAssetID).Take(&deletingInput).Error; err != nil {
		t.Fatal(err)
	}
	deletePath := "/api/token/video-inputs/" + *done.InputAssetID
	deleteKey := "g6-import-input-delete-0001"
	deleteBody := fmt.Sprintf(`{"version_no":%d}`, deletingInput.VersionNo)
	check(call("DELETE", deletePath, deleteKey, f.OtherKey, deleteBody), 404, 40400, "video_input_not_found")
	check(call("DELETE", deletePath, deleteKey, f.Token, deleteBody), 404, 40400, "video_input_not_found")
	for _, invalid := range []string{`{}`, `{"version_no":null}`, `{"version_no":0}`, `{"version_no":-1}`, `{"version_no":1.5}`, `{"version_no":3,"version_no":3}`, `{"version_no":3,"project_id":1}`, `{"version_no":3,"object_key":"forged"}`, `{"VERSION_NO":3}`, `{"version_no":999,"VERSION_NO":3}`} {
		check(call("DELETE", deletePath, deleteKey, f.Key, invalid), 400, 40000, "invalid_request_error")
	}
	check(call("DELETE", deletePath+"?project_id=1", deleteKey, f.Key, deleteBody), 400, 40000, "invalid_request_error")
	if err := f.DB.Model(&model.AIGatewayInputAsset{}).Where("id=?", deletingInput.ID).Update("legal_hold", true).Error; err != nil {
		t.Fatal(err)
	}
	check(call("DELETE", deletePath, deleteKey, f.Key, deleteBody), 409, 40900, "video_input_delete_conflict")
	if err := f.DB.Model(&model.AIGatewayInputAsset{}).Where("id=?", deletingInput.ID).Update("legal_hold", false).Error; err != nil {
		t.Fatal(err)
	}
	deleted := call("DELETE", deletePath, deleteKey, f.Key, deleteBody)
	check(deleted, 202, 0, "")
	var deletion service.VideoInputDeletionReply
	var deletionFields map[string]json.RawMessage
	if json.Unmarshal(deleted.data, &deletion) != nil || json.Unmarshal(deleted.data, &deletionFields) != nil || len(deletionFields) != 6 || deletion.InputAssetID != *done.InputAssetID || deletion.LifecycleState != "pending_delete" || deletion.VersionNo != deletingInput.VersionNo+1 || deletion.DeleteRequestedAt.IsZero() || deletion.MediaDeleted || deletion.Idempotent {
		t.Fatal("删除只返回六字段申请回执，不能声称正文已删除")
	}
	for _, field := range []string{"input_asset_id", "lifecycle_state", "version_no", "delete_requested_at", "media_deleted", "idempotent"} {
		if v, ok := deletionFields[field]; !ok || bytes.Equal(v, []byte("null")) {
			t.Fatalf("删除回执缺字段或null：%s", field)
		}
	}
	repeatedDelete := call("DELETE", deletePath, deleteKey, f.Key, deleteBody)
	check(repeatedDelete, 202, 0, "")
	var repeated service.VideoInputDeletionReply
	if json.Unmarshal(repeatedDelete.data, &repeated) != nil || !repeated.Idempotent || repeated.VersionNo != deletion.VersionNo || !repeated.DeleteRequestedAt.Equal(deletion.DeleteRequestedAt) {
		t.Fatal("原删除键必须复用原申请和版本，不得续期")
	}
	check(call("DELETE", deletePath, deleteKey, f.Key, fmt.Sprintf(`{"version_no":%d}`, deletion.VersionNo)), 409, 40900, "video_input_delete_conflict")
	metadata := call("GET", deletePath, "", f.Key, "")
	check(metadata, 200, 0, "")
	var inputView service.VideoInputDetails
	if json.Unmarshal(metadata.data, &inputView) != nil || inputView.CanReference || inputView.LifecycleState != "pending_delete" || !inputView.ExpiresAt.Equal(deletingInput.ExpiresAt) {
		t.Fatal("删除申请立即阻止新引用且不改变原留存期限")
	}
	var binding model.AIGatewayTaskInput
	if err := f.DB.Where("task_id=?", storedTask.ID).Take(&binding).Error; err != nil || binding.InputAssetID != deletingInput.ID || binding.InputVersion != deletingInput.VersionNo || binding.LeaseReleasedAt != nil {
		t.Fatal("HTTP删除不能改写或释放已有TaskInput")
	}
	if !reflect.DeepEqual(readBaseline, counts()) || readWallet != walletAmounts() || f.ProviderCalls() != 1 {
		t.Fatal("HTTP删除申请不能写钱包/用量/Outbox或调用Provider")
	}
	if err := f.DB.Model(&model.AIGatewayInputAsset{}).Where("id=?", deletingInput.ID).Update("version_no", deletion.VersionNo+1).Error; err != nil {
		t.Fatal(err)
	}
	check(call("DELETE", deletePath, deleteKey, f.Key, deleteBody), 409, 40900, "video_input_delete_conflict")
}
