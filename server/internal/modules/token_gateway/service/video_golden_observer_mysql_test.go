package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 仅输出低敏金样观察值。空指针明确表示没有对应事实，不能把未知成本写成0。
type videoGoldenObservation struct {
	CaseID                  string         `json:"case_id"`
	Purpose                 string         `json:"purpose"`
	Operation               string         `json:"operation"`
	RequestID               string         `json:"request_id"`
	TaskID                  string         `json:"task_id"`
	QuoteID                 string         `json:"quote_id"`
	QuoteAmount             string         `json:"quote_amount"`
	HoldAmount              string         `json:"hold_amount"`
	UserQuantity            *string        `json:"user_usage_quantity"`
	SaleAmount              *string        `json:"sale_amount"`
	ProviderQuantity        *string        `json:"provider_usage_quantity"`
	CostAmount              *string        `json:"recorded_cost_amount"`
	CostSource              string         `json:"cost_source"`
	SettledAmount           *string        `json:"settled_amount"`
	ReleasedAmount          string         `json:"net_released_amount"`
	BalanceBefore           string         `json:"wallet_balance_before"`
	BalanceAfter            string         `json:"wallet_balance_after"`
	FrozenBefore            string         `json:"frozen_before"`
	FrozenAfter             string         `json:"frozen_after"`
	Execution               string         `json:"execution_status"`
	Billing                 string         `json:"billing_status"`
	Delivery                string         `json:"delivery_status"`
	Compensation            string         `json:"compensation_status"`
	CompensationReason      string         `json:"compensation_origin"`
	Assets                  map[string]int `json:"asset_lifecycles"`
	MediaQuantity           *string        `json:"media_seconds"`
	Outbox                  []string       `json:"outbox_events"`
	Reconciled              bool           `json:"reconciliation_passed"`
	Differences             []string       `json:"reconciliation_differences"`
	SubmitCalls             int            `json:"fake_submit_calls"`
	FactCounts              map[string]int `json:"usage_fact_counts"`
	WalletTransactionCounts map[string]int `json:"wallet_transaction_counts"`
	WalletFreeze            string         `json:"wallet_freeze_amount"`
	WalletUnfreeze          string         `json:"wallet_unfreeze_amount"`
	WalletConsume           string         `json:"wallet_consume_amount"`
}

func readVideoGoldenWallet(t *testing.T, f videoG5ReservationFixture) billingmodel.Wallet {
	t.Helper()
	var w billingmodel.Wallet
	if err := f.db.Where("user_id=?", f.owner.UserID).First(&w).Error; err != nil {
		t.Fatal(err)
	}
	return w
}

func videoGoldenMoney(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.StringFixed(8)
	return &s
}
func videoGoldenQuantity(d decimal.Decimal) *string { s := d.String(); return &s }

func observeVideoGolden(t *testing.T, f videoG5ReservationFixture, a *videogateway.FakeAsyncVideoAdapter, before billingmodel.Wallet, c videoGoldenCase) videoGoldenObservation {
	t.Helper()
	ctx := context.Background()
	task, err := repository.NewVideoTaskRepository(f.db).FindForOwner(ctx, f.command.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	var request model.VideoBillingRequest
	var hold billingmodel.WalletHold
	var link model.AIRequestWalletLink
	var quote model.AIGatewayQuote
	if err := f.db.Where("request_id=?", task.RequestID).First(&request).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Where("request_id=?", task.RequestID).First(&link).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&hold, link.WalletHoldID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&quote, task.QuoteID).Error; err != nil {
		t.Fatal(err)
	}
	wallet := readVideoGoldenWallet(t, f)
	o := videoGoldenObservation{CaseID: c.id, Purpose: VideoPricePurposeNonCommercialFixture, Operation: *task.Operation, RequestID: task.RequestID, TaskID: task.PublicID, QuoteID: quote.PublicID, QuoteAmount: quote.QuotedAmount.StringFixed(8), HoldAmount: hold.HoldAmount.StringFixed(8), SettledAmount: videoGoldenMoney(request.SettledAmount), BalanceBefore: before.BalanceAmount.StringFixed(8), BalanceAfter: wallet.BalanceAmount.StringFixed(8), FrozenBefore: before.FrozenAmount.StringFixed(8), FrozenAfter: wallet.FrozenAmount.StringFixed(8), Execution: task.Status, Billing: task.BillingStatus, Delivery: task.DeliveryStatus, Compensation: "none", Assets: map[string]int{}, Outbox: []string{}, SubmitCalls: a.SubmitCalls()}
	var facts []model.VideoUsageItem
	if err := f.db.Where("request_id=?", task.RequestID).Find(&facts).Error; err != nil {
		t.Fatal(err)
	}
	o.FactCounts = map[string]int{}
	expectedFacts := map[string]int{}
	if c.userQuantity != "-" {
		expectedFacts["gateway/usage_fact"] = 1
	}
	if c.sale != "-" {
		expectedFacts["gateway/sale_line"] = 1
	}
	if c.providerQuantity != "-" {
		expectedFacts["provider/usage_fact"] = 1
	}
	if c.cost != "-" {
		source := "provider_cost"
		if c.id == "F07" {
			source = "gateway"
		}
		expectedFacts[source+"/cost_line"] = 1
	}
	for _, v := range facts {
		key := v.Source + "/" + v.RecordKind
		o.FactCounts[key]++
		if expectedFacts[key] != 1 || o.FactCounts[key] != 1 || v.SequenceNo != 0 {
			t.Errorf("金样不能有额外/重复/异序号Usage: %s seq=%d", key, v.SequenceNo)
		}
		if v.TaskID != task.ID || v.QuoteID != quote.ID || v.UserID != f.owner.UserID || v.ProjectID != f.owner.ProjectID || !equalOptionalUint64(v.APIKeyID, f.owner.APIKeyID) || v.Operation == nil || *v.Operation != c.operation {
			t.Fatal("金样Usage归属错误")
		}
		switch {
		case v.Source == "gateway" && v.RecordKind == model.AIUsageFact:
			o.UserQuantity = videoGoldenQuantity(v.Quantity)
		case v.Source == "gateway" && v.RecordKind == model.AIUsageSaleLine:
			if o.SaleAmount != nil {
				t.Fatal("重复销售")
			}
			o.SaleAmount = videoGoldenMoney(v.Amount)
		case v.Source == "provider" && v.RecordKind == model.AIUsageFact:
			o.ProviderQuantity = videoGoldenQuantity(v.Quantity)
		case v.RecordKind == model.AIUsageCostLine:
			if o.CostAmount != nil {
				t.Fatal("重复成本")
			}
			o.CostAmount = videoGoldenMoney(v.Amount)
			o.CostSource = v.Source
		}
	}
	for key, count := range expectedFacts {
		if o.FactCounts[key] != count {
			t.Errorf("金样缺少指定来源事实: %s", key)
		}
	}
	var transactions []billingmodel.WalletTransaction
	if err := f.db.Where("wallet_id=?", wallet.ID).Order("id").Find(&transactions).Error; err != nil {
		t.Fatal(err)
	}
	freeze, unfreeze, consume := decimal.Zero, decimal.Zero, decimal.Zero
	o.WalletTransactionCounts = map[string]int{}
	for _, v := range transactions {
		o.WalletTransactionCounts[v.Type]++
		wantDirection := "out"
		if v.Type == "unfreeze" {
			wantDirection = "in"
		}
		if v.UserID != f.owner.UserID || v.Direction != wantDirection {
			t.Error("金样资金归属或方向错误")
		}
		switch v.Type {
		case "freeze":
			freeze = freeze.Add(v.Amount)
		case "unfreeze":
			unfreeze = unfreeze.Add(v.Amount)
		case "consume":
			consume = consume.Add(v.Amount)
		default:
			t.Fatalf("金样出现意外资金动作: %s", v.Type)
		}
	}
	o.WalletFreeze = freeze.StringFixed(8)
	o.WalletUnfreeze = unfreeze.StringFixed(8)
	o.WalletConsume = consume.StringFixed(8)
	o.ReleasedAmount = unfreeze.Sub(consume).StringFixed(8)
	if !before.BalanceAmount.Sub(freeze).Add(unfreeze).Sub(consume).Equal(wallet.BalanceAmount) {
		t.Fatal("金样余额必须由实际流水守恒")
	}
	if !freeze.Equal(hold.HoldAmount) {
		t.Fatal("冻结流水必须匹配Hold")
	}
	wantConsume := decimal.Zero
	if c.settled != "-" {
		wantConsume = decimal.RequireFromString(c.settled)
	}
	wantUnfreeze := decimal.Zero
	if c.billing == "settled" || c.billing == "released" {
		wantUnfreeze = decimal.RequireFromString(c.quote)
	}
	if !consume.Equal(wantConsume) || !unfreeze.Equal(wantUnfreeze) {
		t.Error("金样消费/全额解冻流水总额与冻结合同不一致")
	}
	unfreezeCount, consumeCount := 0, 0
	if wantUnfreeze.IsPositive() {
		unfreezeCount = 1
	}
	if wantConsume.IsPositive() {
		consumeCount = 1
	}
	if o.WalletTransactionCounts["freeze"] != 1 || o.WalletTransactionCounts["unfreeze"] != unfreezeCount || o.WalletTransactionCounts["consume"] != consumeCount || len(transactions) != 1+unfreezeCount+consumeCount {
		t.Error("零金额额外流水也不能混过金样")
	}
	var assets []model.AIImageAsset
	if err := f.db.Where("request_id=?", task.RequestID).Find(&assets).Error; err != nil {
		t.Fatal(err)
	}
	for _, v := range assets {
		if v.UserID != f.owner.UserID || v.ProjectID != f.owner.ProjectID || v.TaskID != task.ID || v.RequestID != task.RequestID {
			t.Error("金样资产归属错误")
		}
		o.Assets[v.LifecycleState]++
		if v.AssetRole == "content" && v.DurationSeconds != nil {
			o.MediaQuantity = videoGoldenQuantity(*v.DurationSeconds)
		}
	}
	if len(assets) > 0 {
		roles := map[string]uint64{}
		var rootID uint64
		for _, asset := range assets {
			if roles[asset.AssetRole] != 0 {
				t.Error("金样资产角色重复")
			}
			roles[asset.AssetRole] = asset.ID
			if asset.AssetRole == "content" {
				rootID = asset.ID
				if asset.ParentAssetID != nil {
					t.Error("主产物不能有父资产")
				}
			}
		}
		if rootID == 0 {
			t.Error("金样缺少主产物")
		}
		if len(assets) == 6 {
			for _, role := range []string{"content", "cover", "preview", "thumbnail", "moderation_copy", "derived"} {
				if roles[role] == 0 {
					t.Errorf("缺少角色%s", role)
				}
			}
		}
		for _, asset := range assets {
			if asset.AssetRole != "content" && (asset.ParentAssetID == nil || *asset.ParentAssetID != rootID) {
				t.Error("派生资产父子关系错误")
			}
		}
	}
	var jobs []model.VideoCompensationTask
	if err := f.db.Where("task_type='video_reconcile' AND aggregate_id=?", task.RequestID).Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) > 1 {
		t.Fatal("金样补偿必须唯一")
	}
	if len(jobs) == 1 {
		o.Compensation = jobs[0].Status
		o.CompensationReason = jobs[0].OriginErrorCode
	}
	var events []model.AIOutboxEvent
	if err := f.db.Where("aggregate_id=?", task.RequestID).Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{"video_billing_held": "H", "video_billing_settled": "S", "video_billing_released": "R", "video_settlement_pending": "P", "video_delivery_available": "A", "video_delivery_rejected": "J", "video_compensation_required": "C"}
	var short []string
	for _, e := range events {
		code, ok := codes[e.EventType]
		if !ok || e.AggregateType != "video_request" || e.Status != model.AIOutboxPending || e.LockedAt != nil {
			t.Fatal("金样Outbox集合或领取状态错误")
		}
		short = append(short, code)
		o.Outbox = append(o.Outbox, e.EventType)
		var body map[string]json.RawMessage
		if json.Unmarshal(e.PayloadJSON, &body) != nil || len(body) != 6 {
			t.Fatal("金样Outbox必须只有六个低敏合同字段")
		}
		status := map[string]string{"H": "held", "S": "settled", "R": "released", "P": "settlement_pending", "A": "available", "J": "rejected", "C": "pending"}[code]
		amount := c.quote
		if code == "S" || code == "A" {
			amount = c.sale
		}
		if code == "J" {
			amount = "0"
		}
		for key, want := range map[string]string{"request_id": task.RequestID, "status": status, "currency": "CNY", "operation": c.operation} {
			var got string
			if json.Unmarshal(body[key], &got) != nil || got != want {
				t.Errorf("Outbox %s字段%s错误", code, key)
			}
		}
		var gotAmount string
		var version int
		if json.Unmarshal(body["amount"], &gotAmount) != nil || gotAmount != decimal.RequireFromString(amount).StringFixed(8) || json.Unmarshal(body["version"], &version) != nil || version != 1 {
			t.Error("金样Outbox金额或版本错误")
		}
	}
	sort.Strings(short)
	sort.Strings(o.Outbox)
	report, err := NewVideoReconciliationService(f.db).Reconcile(ctx, task.PublicID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	o.Reconciled = report.Passed
	o.Differences = report.Differences
	sort.Strings(o.Differences)
	for label, pair := range map[string][2]string{"quote": {o.QuoteAmount, c.quote}, "hold": {o.HoldAmount, c.quote}, "released": {o.ReleasedAmount, c.released}, "balance": {o.BalanceAfter, c.balance}, "frozen": {o.FrozenAfter, c.frozen}} {
		if !decimal.RequireFromString(pair[0]).Equal(decimal.RequireFromString(pair[1])) {
			t.Errorf("%s实际%s预期%s", label, pair[0], pair[1])
		}
	}
	for _, check := range []struct {
		name string
		got  *string
		want string
	}{{"user_quantity", o.UserQuantity, c.userQuantity}, {"sale", o.SaleAmount, c.sale}, {"provider_quantity", o.ProviderQuantity, c.providerQuantity}, {"cost", o.CostAmount, c.cost}, {"settled", o.SettledAmount, c.settled}, {"hold_settled", videoGoldenMoney(hold.SettledAmount), c.settled}} {
		if check.want == "-" {
			if check.got != nil {
				t.Errorf("%s应无事实，实际%s", check.name, *check.got)
			}
		} else if check.got == nil || !decimal.RequireFromString(*check.got).Equal(decimal.RequireFromString(check.want)) {
			t.Errorf("%s实际%v预期%s", check.name, check.got, check.want)
		}
	}
	if o.Execution != c.execution || o.Billing != c.billing || o.Delivery != c.delivery || o.Compensation != c.compensation || strings.Join(short, ",") != c.events || len(assets) != c.assets || (c.assets > 0 && o.Assets[c.lifecycle] != c.assets) || o.Reconciled != c.closed || o.SubmitCalls != c.submits {
		t.Errorf("金样状态不符合字面预期: %+v", o)
	}
	if c.closed && len(o.Differences) != 0 || !c.closed && len(o.Differences) == 0 {
		t.Error("未闭合金样必须保留非零差异，闭合金样全部为0")
	}
	if c.id == "F12" && (o.MediaQuantity == nil || *o.MediaQuantity != "5") {
		t.Error("冲突金样必须保留实际5秒媒体")
	}
	if c.id == "F07" && o.CostSource != "gateway" {
		t.Error("未提交成本0必须注明来自网关事实，而非虚构Provider确认")
	}
	if o.Compensation != "none" {
		want := map[string]string{"store_failed": "media_unavailable", "settlement_compensated": "settlement_failed", "unknown": "provider_unknown", "usage_conflict": "facts_conflict"}[c.scenario]
		if o.CompensationReason != want {
			t.Errorf("补偿来源实际%s预期%s", o.CompensationReason, want)
		}
	}
	encoded, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VID_G5_GOLDEN=%s", encoded)
	return o
}

func assertVideoGoldenTotals(t *testing.T, rows []videoGoldenObservation) {
	t.Helper()
	if len(rows) != 12 {
		t.Fatalf("十二种金样必须全部执行: %d", len(rows))
	}
	for _, scope := range []string{"text_to_video", "image_to_video", "all"} {
		quote, sale, cost, released, balance, frozen := decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero, decimal.Zero
		count, unknown := 0, 0
		allClosed := true
		for _, r := range rows {
			if scope != "all" && r.Operation != scope {
				continue
			}
			count++
			allClosed = allClosed && r.Reconciled
			quote = quote.Add(decimal.RequireFromString(r.QuoteAmount))
			released = released.Add(decimal.RequireFromString(r.ReleasedAmount))
			balance = balance.Add(decimal.RequireFromString(r.BalanceAfter))
			frozen = frozen.Add(decimal.RequireFromString(r.FrozenAfter))
			if r.SaleAmount != nil {
				sale = sale.Add(decimal.RequireFromString(*r.SaleAmount))
			}
			if r.CostAmount == nil {
				unknown++
			} else {
				cost = cost.Add(decimal.RequireFromString(*r.CostAmount))
			}
		}
		want := map[string][]string{"text_to_video": {"5.50", "1.50", "1.24", "2.00", "106.50", "2.00"}, "image_to_video": {"0.75", "0.75", "0.30", "0", "9.25", "0"}, "all": {"6.25", "2.25", "1.54", "2.00", "115.75", "2.00"}}[scope]
		for i, actual := range []decimal.Decimal{quote, sale, cost, released, balance, frozen} {
			if !actual.Equal(decimal.RequireFromString(want[i])) {
				t.Errorf("汇总%s第%d项实际%s预期%s", scope, i, actual, want[i])
			}
		}
		wantUnknown := 2
		if scope == "image_to_video" {
			wantUnknown = 0
		}
		if unknown != wantUnknown {
			t.Error("未知成本数量不可在汇总中消失")
		}
		if allClosed != (scope == "image_to_video") {
			t.Error("汇总不能把未闭合请求视为最终对账通过")
		}
		if !quote.Equal(sale.Add(released).Add(frozen)) {
			t.Error("汇总Hold必须等于销售+净释放+仍冻结")
		}
		encoded, _ := json.Marshal(map[string]interface{}{"scope": scope, "requests": count, "quote_hold": quote.StringFixed(8), "posted_sale": sale.StringFixed(8), "known_cost_subtotal": cost.StringFixed(8), "unknown_cost_requests": unknown, "net_released": released.StringFixed(8), "wallet_balance_after": balance.StringFixed(8), "frozen_after": frozen.StringFixed(8), "conservation_difference": quote.Sub(sale.Add(released).Add(frozen)).StringFixed(8), "all_requests_finally_reconciled": allClosed})
		t.Logf("VID_G5_GOLDEN_TOTAL=%s", encoded)
	}
}
