package service_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

// 真正的回环HTTP验签与原G5创建/提交链；只把外部Provider和存储替换为现有Fake。
func TestVideoG6CallbackHTTPMySQL(t *testing.T) {
	f := service.NewVideoContentHTTPFixture(t)
	caller := service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	firstSubmission := true
	create := func(key string) model.AIImageTask {
		t.Helper()
		created, err := f.App.Create(context.Background(), service.VideoCommand{Caller: caller, IdempotencyKey: key, Model: f.Model, Prompt: "仅用于内部回调隔离验证", Operation: model.AIVideoOperationTextToVideo})
		if err != nil {
			t.Fatal(err)
		}
		if firstSubmission {
			// 首个任务必须证明生产G6运行准入后仍能进入真实Provider提交链。
			f.Submit(created.Job.ID)
			firstSubmission = false
		} else {
			// 其余任务只为并行回调矩阵保留submitted前置；容量裁决由独立100并发专测覆盖。
			f.SubmitCallbackFixture(created.Job.ID)
		}
		var task model.AIImageTask
		if err := f.DB.Where("public_id=?", created.Job.ID).Take(&task).Error; err != nil || task.ProviderTaskID == nil || task.ProviderCode == nil || task.Status != "submitted" {
			t.Fatal("回调必须从真实已提交且绑定Provider任务开始")
		}
		return task
	}
	task, other := create("g6-callback-http-original"), create("g6-callback-http-other")
	maxTask := create("g6-callback-http-max-event")
	directTask := create("g6-callback-http-direct-success")
	pendingTask := create("g6-callback-http-pending")
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoCallbackService(f.App, service.VideoCallbackOptions{FakeOnlyEnabled: true, SigningSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoInternalRoutes(mux, app, true)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	path := "/api/internal/ai/provider-callbacks/fake-native-async"
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	body := func(id, event, status string, progress int) []byte {
		return []byte(fmt.Sprintf(`{"provider_task_id":%q,"external_event_id":%q,"video_id":%q,"status":%q,"progress":%d}`, *task.ProviderTaskID, event, id, status, progress))
	}
	request := func(raw []byte, nonce, ts, override string, adjust ...func(*http.Request)) (int, *service.VideoCallbackACK, error) {
		digest := sha256.Sum256(raw)
		canonical := fmt.Sprintf("molin-video-callback-v1\nPOST\n%s\n%s\n%s\n%x", path, ts, nonce, digest)
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write([]byte(canonical))
		signature := hex.EncodeToString(mac.Sum(nil))
		if override != "" {
			signature = override
		}
		r, err := http.NewRequest("POST", srv.URL+path, bytes.NewReader(raw))
		if err != nil {
			return 0, nil, err
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Molin-Callback-Timestamp", ts)
		r.Header.Set("X-Molin-Callback-Nonce", nonce)
		r.Header.Set("X-Molin-Callback-Signature", signature)
		for _, change := range adjust {
			change(r)
		}
		resp, err := client.Do(r)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, nil, err
		}
		if resp.StatusCode != 200 {
			return resp.StatusCode, nil, nil
		}
		var fields map[string]json.RawMessage
		var ack service.VideoCallbackACK
		if json.Unmarshal(data, &fields) != nil || len(fields) != 3 || json.Unmarshal(data, &ack) != nil {
			return 200, nil, fmt.Errorf("ACK必须恰好三个布尔字段")
		}
		for _, name := range []string{"accepted", "applied", "replayed"} {
			if string(fields[name]) != "true" && string(fields[name]) != "false" {
				return 200, nil, fmt.Errorf("ACK字段必须显式bool")
			}
		}
		return 200, &ack, nil
	}
	call := func(raw []byte, nonce, ts, signature string, want int) *service.VideoCallbackACK {
		t.Helper()
		status, ack, err := request(raw, nonce, ts, signature)
		if err != nil || status != want {
			t.Fatalf("内部回调应%d实际%d，err=%v", want, status, err)
		}
		return ack
	}
	counts := func() (int64, int64) {
		t.Helper()
		var events, nonces int64
		if err := f.DB.Table("ai_gateway_provider_callback_events").Where("task_id=?", task.ID).Count(&events).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.DB.Table("ai_video_callback_nonces").Where("callback_event_id IN (SELECT id FROM ai_gateway_provider_callback_events WHERE task_id=?)", task.ID).Count(&nonces).Error; err != nil {
			t.Fatal(err)
		}
		return events, nonces
	}
	finance := func() []byte {
		t.Helper()
		var tables map[string]json.RawMessage
		if json.Unmarshal(f.FinancialSnapshot(), &tables) != nil {
			t.Fatal("财务快照无效")
		}
		delete(tables, "ai_requests")
		raw, err := json.Marshal(tables)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := finance()
	submits := f.SubmitCalls()
	firstBody := body(task.PublicID, "evt-http-processing", "processing", 15)
	nonce1, nonce2 := strings.Repeat("1", 64), strings.Repeat("2", 64)
	for _, tc := range []struct {
		name   string
		status int
		change func(*http.Request)
	}{
		{"重复时间头", 400, func(r *http.Request) { r.Header.Add("X-Molin-Callback-Timestamp", stamp) }},
		{"重复nonce头", 400, func(r *http.Request) { r.Header.Add("X-Molin-Callback-Nonce", nonce1) }},
		{"重复签名头", 400, func(r *http.Request) { r.Header.Add("X-Molin-Callback-Signature", "other") }},
		{"缺失签名", 401, func(r *http.Request) { r.Header.Del("X-Molin-Callback-Signature") }},
		{"SK不能替代签名", 401, func(r *http.Request) {
			r.Header.Del("X-Molin-Callback-Signature")
			r.Header.Set("Authorization", "Bearer "+f.Key)
		}},
		{"查询参数", 400, func(r *http.Request) { r.URL.RawQuery = "owner=1" }},
		{"编码路径别名", 400, func(r *http.Request) { r.URL.RawPath = "/api/internal/ai/provider-callbacks/%66ake-native-async" }},
		{"未知Provider", 404, func(r *http.Request) { r.URL.Path = "/api/internal/ai/provider-callbacks/other" }},
		{"缺失内容类型", 415, func(r *http.Request) { r.Header.Del("Content-Type") }},
		{"重复内容类型", 415, func(r *http.Request) { r.Header.Add("Content-Type", "application/json") }},
		{"未知内容参数", 415, func(r *http.Request) { r.Header.Set("Content-Type", "application/json; boundary=unused") }},
		{"不支持压缩", 415, func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }},
	} {
		status, _, err := request(firstBody, nonce1, stamp, "", tc.change)
		if err != nil || status != tc.status {
			t.Errorf("%s应%d实际%d err=%v", tc.name, tc.status, status, err)
		}
	}
	if events, nonces := counts(); events != 0 || nonces != 0 {
		t.Fatal("非法HTTP请求不得产生Callback或nonce")
	}
	call(firstBody, nonce1, stamp, strings.Repeat("0", 64), 401)
	if events, nonces := counts(); events != 0 || nonces != 0 {
		t.Fatal("无效签名不能提前占位合法事件或nonce")
	}
	first := call(firstBody, nonce1, stamp, "", 200)
	if !first.Accepted || !first.Applied || first.Replayed {
		t.Fatal("首次合法回调必须实际应用一次")
	}
	var processing model.AIImageTask
	if err := f.DB.First(&processing, task.ID).Error; err != nil || processing.Status != "processing" || processing.VersionNo != task.VersionNo+1 {
		t.Fatal("回调必须通过原Task CAS仅推进一次")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, ack, err := request(firstBody, nonce1, stamp, "")
			if err != nil || status != 200 || ack == nil || !ack.Accepted || !ack.Applied || !ack.Replayed {
				failures <- fmt.Errorf("并发重放异常：status=%d err=%v", status, err)
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if events, nonces := counts(); events != 1 || nonces != 1 {
		t.Fatal("100并发重放只能保留原事件和nonce")
	}
	call(body(task.PublicID, "evt-http-processing", "processing", 16), nonce2, stamp, "", 409)
	newStamp := strconv.FormatInt(time.Now().Unix()+1, 10)
	call(firstBody, nonce1, newStamp, "", 409)
	if ack := call(firstBody, nonce2, newStamp, "", 200); !ack.Replayed || !ack.Applied {
		t.Fatal("原事件允许新nonce重试，但不能再次应用")
	}
	call(body(other.PublicID, "evt-wrong-binding", "processing", 15), strings.Repeat("3", 64), stamp, "", 404)
	call(body("vid-unknown", "evt-unknown", "processing", 15), strings.Repeat("4", 64), stamp, "", 404)
	if events, nonces := counts(); events != 1 || nonces != 2 {
		t.Fatal("未知和错绑任务不得占用合法回调事实")
	}
	// 另一任务首次回调直接由100个请求争用，不能只验证已经存在记录的快路径。
	raceBody := []byte(fmt.Sprintf(`{"provider_task_id":%q,"external_event_id":"evt-http-processing","video_id":%q,"status":"processing","progress":15}`, *other.ProviderTaskID, other.PublicID))
	var winners atomic.Int64
	raceFailures := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status, ack, err := request(raceBody, fmt.Sprintf("%064x", 60), stamp, "")
			if err != nil || status != 200 || ack == nil || !ack.Accepted || !ack.Applied {
				raceFailures <- fmt.Errorf("首次并发回调失败：status=%d err=%v", status, err)
				return
			}
			if !ack.Replayed {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	close(raceFailures)
	for err := range raceFailures {
		t.Error(err)
	}
	if winners.Load() != 1 {
		t.Errorf("首次100并发只能有一名应用赢家：%d", winners.Load())
	}
	var raceTask model.AIImageTask
	if err := f.DB.First(&raceTask, other.ID).Error; err != nil || raceTask.Status != "processing" || raceTask.VersionNo != other.VersionNo+1 {
		t.Fatal("首次100并发必须只推进原Task一次")
	}
	// 外部事件号只在Provider任务内唯一，内部TaskEvent不能以裸外部ID作全局键。
	for i, target := range []model.AIImageTask{other, maxTask} {
		eventID := "evt-http-processing"
		if i == 1 {
			eventID = strings.Repeat("e", 128)
		}
		raw := []byte(fmt.Sprintf(`{"provider_task_id":%q,"external_event_id":%q,"video_id":%q,"status":"processing","progress":15}`, *target.ProviderTaskID, eventID, target.PublicID))
		status, ack, err := request(raw, fmt.Sprintf("%064x", i+30), stamp, "")
		if err != nil || status != 200 || ack == nil || !ack.Applied {
			t.Errorf("独立任务事件必须成功且不受外部ID长度/重复影响：case=%d status=%d err=%v", i, status, err)
		}
	}
	// 同Provider的nonce跨Task仍只能绑定一个完整请求；失败者的Callback也必须随外层事务回滚。
	type nonceResult struct {
		status int
		err    error
	}
	start := make(chan struct{})
	nonceResults := make(chan nonceResult, 2)
	for i, target := range []model.AIImageTask{task, other} {
		raw := []byte(fmt.Sprintf(`{"provider_task_id":%q,"external_event_id":%q,"video_id":%q,"status":"processing","progress":15}`, *target.ProviderTaskID, fmt.Sprintf("evt-cross-nonce-%d", i), target.PublicID))
		go func(raw []byte) {
			<-start
			status, _, err := request(raw, fmt.Sprintf("%064x", 80), stamp, "")
			nonceResults <- nonceResult{status, err}
		}(raw)
	}
	close(start)
	won, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		result := <-nonceResults
		if result.err != nil {
			t.Error(result.err)
		}
		switch result.status {
		case 200:
			won++
		case 409:
			conflicts++
		default:
			t.Errorf("跨Task nonce竞争异常状态：%d", result.status)
		}
	}
	if won != 1 || conflicts != 1 {
		t.Fatalf("同nonce只能一胜一冲突：won=%d conflict=%d", won, conflicts)
	}
	var crossEvents int64
	if err := f.DB.Table("ai_gateway_provider_callback_events").Where("task_id IN ? AND external_event_id IN ?", []uint64{task.ID, other.ID}, []string{"evt-cross-nonce-0", "evt-cross-nonce-1"}).Count(&crossEvents).Error; err != nil || crossEvents != 1 {
		t.Fatal("跨Task nonce失败者不得遗留已提交Callback")
	}
	directBody := []byte(fmt.Sprintf(`{"provider_task_id":%q,"external_event_id":"evt-direct-success","video_id":%q,"status":"succeeded","progress":100}`, *directTask.ProviderTaskID, directTask.PublicID))
	if ack := call(directBody, fmt.Sprintf("%064x", 40), stamp, "", 200); !ack.Applied {
		t.Error("首个回调直接成功也必须沿合法状态路径处理，不能静默忽略")
	}
	var directAfter model.AIImageTask
	if err := f.DB.First(&directAfter, directTask.ID).Error; err != nil || directAfter.Status != "fetching" || directAfter.VersionNo != directTask.VersionNo+2 {
		t.Error("直接成功必须原子经过processing再到fetching，不放宽单步矩阵")
	}
	if ack := call(body(task.PublicID, "evt-http-success", "succeeded", 100), strings.Repeat("5", 64), stamp, "", 200); !ack.Applied || ack.Replayed {
		t.Fatal("Provider成功只能推进至后续抓取阶段")
	}
	var fetching model.AIImageTask
	if err := f.DB.First(&fetching, task.ID).Error; err != nil || fetching.Status != "fetching" {
		t.Fatal("不能将Provider成功直接写为最终成功")
	}
	for i, status := range []string{"processing", "failed", "cancelled", "unknown"} {
		ack := call(body(task.PublicID, "evt-late-"+status, status, 20), fmt.Sprintf("%064x", i+10), stamp, "", 200)
		if !ack.Accepted || ack.Applied {
			t.Errorf("已确认Provider成功后的迟到%s只能记录安全忽略", status)
		}
	}
	var final model.AIImageTask
	if err := f.DB.First(&final, task.ID).Error; err != nil || final.Status != "fetching" || final.VersionNo != fetching.VersionNo {
		t.Fatal("迟到相反Provider终态不得破坏已开始归档的任务")
	}
	var req model.AIRequest
	if err := f.DB.Where("request_id=?", task.RequestID).Take(&req).Error; err != nil || req.BillingStatus != "held" || req.DeliveryStatus != "pending" {
		t.Fatal("回调不得擅自结算或交付")
	}
	if !bytes.Equal(before, finance()) || f.SubmitCalls() != submits {
		t.Fatal("回调与重放不得修改生成财务或重新Submit")
	}
	// 第一次未知结果允许原G5安排对账；之后的回调不能代替对账确认或再次安排资金操作。
	pendingBody := func(event, status string) []byte {
		return []byte(fmt.Sprintf(`{"provider_task_id":%q,"external_event_id":%q,"video_id":%q,"status":%q,"progress":20}`, *pendingTask.ProviderTaskID, event, pendingTask.PublicID, status))
	}
	if ack := call(pendingBody("evt-pending", "unknown"), fmt.Sprintf("%064x", 50), stamp, "", 200); !ack.Applied {
		t.Fatal("首次未知必须进入原待对账流程")
	}
	// 使用原G5租约/CAS把合成恢复任务交给人工核对，不伪造审批、金额或真实管理员。
	compensations := repository.NewVideoCompensationRepository(f.DB)
	lease, err := compensations.Claim(context.Background(), pendingTask.RequestID, "g6-callback-test-worker")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Transaction(func(tx *gorm.DB) error { return compensations.FinishTx(tx, *lease, "manual_review", "manual_review") }); err != nil {
		t.Fatal(err)
	}
	var compensationBefore model.VideoCompensationTask
	if err := f.DB.Where("aggregate_id=? AND task_type='video_reconcile'", pendingTask.RequestID).Take(&compensationBefore).Error; err != nil || compensationBefore.Status != "manual_review" {
		t.Fatal("必须真实进入待人工核对")
	}
	compensationRaw, err := json.Marshal(compensationBefore)
	if err != nil {
		t.Fatal(err)
	}
	var eventCountBefore int64
	if err := f.DB.Table("ai_gateway_task_events").Where("task_id=?", pendingTask.ID).Count(&eventCountBefore).Error; err != nil {
		t.Fatal(err)
	}
	var pendingBefore model.AIImageTask
	if err := f.DB.First(&pendingBefore, pendingTask.ID).Error; err != nil || pendingBefore.Status != "pending_reconcile" {
		t.Fatal("必须真实进入待对账")
	}
	pendingSnapshot, err := json.Marshal(pendingBefore)
	if err != nil {
		t.Fatal(err)
	}
	pendingFinance := f.FinancialSnapshot()
	for i, status := range []string{"failed", "cancelled", "succeeded"} {
		if ack := call(pendingBody("evt-pending-late-"+status, status), fmt.Sprintf("%064x", i+51), stamp, "", 200); ack.Applied {
			t.Errorf("待对账不得由迟到%s擅自终结", status)
		}
	}
	if ack := call(pendingBody("evt-pending", "unknown"), fmt.Sprintf("%064x", 50), stamp, "", 200); !ack.Replayed || !ack.Applied {
		t.Fatal("已应用原事件重放应恢复ACK而非重新执行对账")
	}
	var pendingAfter model.AIImageTask
	if err := f.DB.First(&pendingAfter, pendingTask.ID).Error; err != nil {
		t.Fatal(err)
	}
	pendingAfterRaw, err := json.Marshal(pendingAfter)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pendingSnapshot, pendingAfterRaw) || !bytes.Equal(pendingFinance, f.FinancialSnapshot()) {
		t.Fatal("待对账后的迟到事件只能新增回调事实，不能改任务/请求/财务/Outbox")
	}
	var eventCountAfter int64
	if err := f.DB.Table("ai_gateway_task_events").Where("task_id=?", pendingTask.ID).Count(&eventCountAfter).Error; err != nil {
		t.Fatal(err)
	}
	var compensationAfter model.VideoCompensationTask
	if err := f.DB.First(&compensationAfter, compensationBefore.ID).Error; err != nil {
		t.Fatal(err)
	}
	compensationAfterRaw, err := json.Marshal(compensationAfter)
	if err != nil {
		t.Fatal(err)
	}
	if eventCountAfter != eventCountBefore || !bytes.Equal(compensationRaw, compensationAfterRaw) {
		t.Fatal("已应用事件重放不得重新触发人工核对事件或补偿状态")
	}
}
