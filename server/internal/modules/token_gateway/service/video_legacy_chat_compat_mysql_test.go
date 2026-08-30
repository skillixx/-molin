package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"
	billingservice "molin/server/internal/modules/billing/service"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 复用原Chat账务整套断言，隔离网络仅有临时MySQL；不执行旧脚本的RabbitMQ或公网部分。
func TestVideoG5CompatibilityMySQLLegacyChatBilling(t *testing.T) {
	db := openVideoG5MySQL(t)
	// 先创建真实G5合成钱包/预占，证明旧Chat夹具不能假设钱包主键与user_id相等。
	coexisting := newVideoG5ReservationFixture(t, db, "10")
	if _, err := coexisting.service.ReserveAndCreate(context.Background(), coexisting.command); err != nil {
		t.Fatal(err)
	}
	before := readVideoGoldenWallet(t, coexisting)
	// 人为推进旧Chat扫描时钟，视频持有款不能成为Chat的超时释放/异常候选。
	pricingRepo := repository.NewG3PricingRepository(db)
	chatBilling := NewAIBillingService(db, NewPricingService(pricingRepo), pricingRepo, coexisting.service.holds.(*billingservice.WalletHoldService))
	chatBilling.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	if changed, err := chatBilling.ReconcileInterrupted(context.Background(), 10000); err != nil || changed != 0 {
		t.Fatalf("旧Chat对账不得领取视频请求: changed=%d err=%v", changed, err)
	}
	if err := chatBilling.ResolveException(context.Background(), coexisting.command.RequestID, ManualResolutionRelease, ExecutionUsage{}); err == nil {
		t.Fatal("旧Chat人工入口不得终结视频Hold")
	}
	for i := 1; i <= 18; i++ {
		if err := db.Exec("INSERT INTO users(id,password_hash,status,real_name_status) VALUES(?,'fixture','active','verified')", i).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO ai_projects(id,user_id,name) VALUES(?,?,?)", i, i, fmt.Sprintf("G3-%d", i)).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,model_scope,scope_mode,status) VALUES(?,?,?,?,?,?,'','all','active')", i, i, i, fmt.Sprintf("sk-g3-%d", i), fmt.Sprintf("hash-%d", i), fmt.Sprintf("G3-%d", i)).Error; err != nil {
			t.Fatal(err)
		}
		balance := "1"
		if i == 1 {
			balance = "0"
		}
		if i == 2 {
			balance = "0.14"
		}
		if err := db.Exec("INSERT INTO wallets(user_id,balance_amount,frozen_amount,currency) VALUES(?,?,0,'CNY')", i, balance).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO token_models(id,logical_model_code,display_name,status,modality) VALUES(1,'qwen-plus','Chat兼容夹具','active','chat'),(2,'qwen-concurrent','Chat并发夹具','active','chat')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ai_price_versions(id,logical_model_code,version_no,currency,exchange_rate,status,min_margin_rate,max_input_tokens,max_output_tokens,failure_charge_policy,rounding_mode,cost_updated_at,cost_expires_at,effective_at,created_by,approved_by,approved_at,published_at)
VALUES(1,'qwen-plus',1,'CNY',1,'active',0.2,1000,100,'confirmed_usage','ceil_8','2026-08-01 00:00:00','2030-01-01 00:00:00','2026-08-01 00:00:00',1,1,NOW(),NOW())`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO ai_price_skus(price_version_id,meter_type,variant_hash,cost_unit_price,sale_unit_price,scale,currency) VALUES
(1,'input_tokens',SHA2('input',256),5,10,1000000,'CNY'),
(1,'cached_tokens',SHA2('cached',256),1,2,1000000,'CNY'),
(1,'output_tokens',SHA2('output',256),10,20,1000000,'CNY'),
(1,'reasoning_tokens',SHA2('reasoning',256),20,40,1000000,'CNY')`).Error; err != nil {
		t.Fatal(err)
	}
	t.Setenv("G3_MYSQL_DSN", os.Getenv("MOLIN_VIDEO_G5_MYSQL_DSN"))
	quote, err := chatBilling.QuoteRequest(context.Background(), "qwen-plus", map[string]interface{}{"max_tokens": 10})
	if err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("撤销跨模态预占反例")
	err = db.Transaction(func(tx *gorm.DB) error {
		local := *chatBilling
		local.db = tx
		for _, mode := range []string{"image", "video"} {
			for _, entry := range []string{"prepare", "quoted", "retry", "retry_unquoted"} {
				r := integrationRequest("compat-invalid-"+mode+entry, 16)
				r.Modality = mode
				r.Capability = mode + ".generate"
				key := "compat-invalid"
				r.IdempotencyKey = &key
				if mode == "video" {
					op := model.AIVideoOperationTextToVideo
					r.Operation = &op
				}
				var callErr error
				switch entry {
				case "prepare":
					_, callErr = local.PrepareRequest(context.Background(), r, map[string]interface{}{"max_tokens": 10})
				case "quoted":
					_, callErr = local.PrepareQuotedRequest(context.Background(), r, quote)
				case "retry":
					_, callErr = local.PrepareRetryQuotedRequest(context.Background(), coexisting.command.RequestID, r, quote)
				case "retry_unquoted":
					_, callErr = local.PrepareRetryRequest(context.Background(), coexisting.command.RequestID, r, map[string]interface{}{"max_tokens": 10})
				}
				if !errors.Is(callErr, ErrUnquotableRequest) {
					t.Errorf("非Chat%s入口必须在资金操作前拒绝: %v", entry, callErr)
				}
				var n int64
				if err := tx.Model(&model.AIRequest{}).Where("request_id=?", r.RequestID).Count(&n).Error; err != nil {
					return err
				}
				if n != 0 {
					t.Error("非Chat请求不得由旧账务入口创建")
				}
			}
		}
		r := integrationRequest("compat-foreign-previous", 16)
		key := "compat-previous"
		r.IdempotencyKey = &key
		if _, err := local.PrepareRetryQuotedRequest(context.Background(), coexisting.command.RequestID, r, quote); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("旧重试入口不得读取video原请求: %v", err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	TestG3MySQLBillingIntegration(t)
	after := readVideoGoldenWallet(t, coexisting)
	if !before.BalanceAmount.Equal(after.BalanceAmount) || !before.FrozenAmount.Equal(after.FrozenAmount) || before.Version != after.Version {
		t.Fatal("Chat兼容回归不得改变已有G5钱包")
	}
}
