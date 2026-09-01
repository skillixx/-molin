package service_test

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	billingmodel "molin/server/internal/modules/billing/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

func TestVideoG6AdminCancelI2VLeaseMySQL(t *testing.T) {
	// 原G5夹具对I2V冻结0.15元/秒，固定5秒为0.75；不能误用T2V的0.50预占。
	const expectedI2VHold = "0.75000000"
	for _, submitted := range []bool{false, true} {
		name := "未提交安全释放"
		if submitted {
			name = "已提交只记录意图"
		}
		t.Run(name, func(t *testing.T) {
			f := newAdminCancelErrorFixture(t)
			ctx := context.Background()
			owner := service.VideoCaller{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID, APIKeyID: f.f.ProjectID}
			if _, err := f.f.App.AcceptProjectRights(ctx, service.VideoRightsAcceptCommand{Caller: service.VideoCaller{UserID: f.f.ProjectID, ProjectID: f.f.ProjectID}, PolicyVersion: f.f.Policy, Confirmed: true, IdempotencyKey: "g6-admin-i2v-rights", RequestID: "g6-admin-i2v-rights-trace"}); err != nil {
				t.Fatal(err)
			}
			imported, err := f.f.App.ImportImageInput(ctx, service.VideoInputImportCommand{Caller: owner, IdempotencyKey: "g6-admin-i2v-import", SourceAssetID: f.f.SourceID})
			if err != nil || imported.InputAssetID == nil {
				t.Fatalf("必须实际导入规范化参考图：%v", err)
			}
			created, err := f.f.App.Create(ctx, service.VideoCommand{Caller: owner, IdempotencyKey: "g6-admin-i2v-create", Model: f.f.Model, Prompt: "仅用于合成图生视频取消", Operation: "image_to_video", InputAssetID: *imported.InputAssetID, RightsAttestation: true})
			if err != nil {
				t.Fatal(err)
			}
			if created.HeldAmount != expectedI2VHold {
				t.Fatalf("原I2V报价预占应%s实际%s", expectedI2VHold, created.HeldAmount)
			}
			originalT2V := f.task
			f.task = created.Job.ID
			f.requestID = created.RequestID
			if submitted {
				f.f.Submit(f.task)
			}
			if err := f.f.DB.Table("ai_gateway_tasks").Select("version_no").Where("public_id=?", f.task).Scan(&f.version).Error; err != nil {
				t.Fatal(err)
			}
			binding := func() model.AIGatewayTaskInput {
				t.Helper()
				var rows []model.AIGatewayTaskInput
				if err := f.f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?)", f.task).Find(&rows).Error; err != nil || len(rows) != 1 {
					t.Fatal("I2V必须且只能有一份真实TaskInput")
				}
				return rows[0]
			}
			before := binding()
			if before.LeaseReleasedAt != nil || !f.f.InputPresent(*imported.InputAssetID) {
				t.Fatal("取消前必须仍有原输入正文和执行租约")
			}
			finance := f.f.FinancialSnapshot()
			var beforeReleases int64
			if err := f.f.DB.Table("wallet_transactions").Where("user_id=? AND type='unfreeze'", f.f.ProjectID).Count(&beforeReleases).Error; err != nil {
				t.Fatal(err)
			}
			submits := f.f.SubmitCalls()
			srv := f.server(t, "g6-admin-i2v-v1", f.secret)
			status := 200
			if submitted {
				status = 202
			}
			body := []byte(fmt.Sprintf(`{"reason":"合成I2V取消","version_no":%d}`, f.version))
			reply := f.call(t, srv, body, status)
			after := binding()
			if submitted {
				if reply.CancellationResult != "cancel_requested" || after.LeaseReleasedAt != nil || !bytes.Equal(finance, f.f.FinancialSnapshot()) {
					t.Fatal("已提交I2V不可释放租约或改写资金")
				}
			} else {
				if after.LeaseReleasedAt == nil || reply.CancellationResult != "cancelled" || reply.BillingStatus != "released" || reply.NetReleasedAmount == nil || *reply.NetReleasedAmount != expectedI2VHold {
					t.Fatal("安全取消必须释放原0.75预占和输入租约")
				}
				var link model.AIRequestWalletLink
				if err := f.f.DB.Where("request_id=?", f.requestID).Take(&link).Error; err != nil || link.ReleaseTransactionID == nil {
					t.Fatal("原Request必须指向释放流水")
				}
				var hold billingmodel.WalletHold
				var release billingmodel.WalletTransaction
				if err := f.f.DB.First(&hold, link.WalletHoldID).Error; err != nil || hold.Status != "released" || hold.SettledAmount == nil || !hold.SettledAmount.IsZero() || hold.HoldAmount.StringFixed(8) != expectedI2VHold {
					t.Fatal("原Hold必须真实全额释放")
				}
				if err := f.f.DB.First(&release, *link.ReleaseTransactionID).Error; err != nil || release.Type != "unfreeze" || release.Direction != "in" || release.WalletID != hold.WalletID || release.UserID != f.f.ProjectID || release.Amount.StringFixed(8) != expectedI2VHold {
					t.Fatal("原释放流水必须归属正确且金额0.75")
				}
				var afterReleases int64
				if err := f.f.DB.Table("wallet_transactions").Where("user_id=? AND type='unfreeze'", f.f.ProjectID).Count(&afterReleases).Error; err != nil || afterReleases != beforeReleases+1 {
					t.Fatal("本次I2V取消只能新增唯一解冻流水")
				}
			}
			// 唯一允许变化是安全终态释放时间，冻结归属/hash/version及角色不得改变。
			after.LeaseReleasedAt = nil
			if !reflect.DeepEqual(before, after) {
				t.Fatal("取消不得替换或重绑冻结参考图")
			}
			var state struct {
				Status            string
				CancelRequestedAt *time.Time
			}
			if err := f.f.DB.Table("ai_gateway_tasks").Select("status,cancel_requested_at").Where("public_id=?", f.task).Take(&state).Error; err != nil || state.CancelRequestedAt == nil {
				t.Fatal("必须保存原任务取消意图")
			}
			if (submitted && state.Status != "submitted") || (!submitted && state.Status != "cancelled") {
				t.Fatal("取消结果必须与实际提交边界一致")
			}
			if !f.f.InputPresent(*imported.InputAssetID) || submits != f.f.SubmitCalls() {
				t.Fatal("取消不能删除输入正文或提交Provider")
			}
			other, err := f.f.App.GetPlatformTask(ctx, owner, originalT2V, false)
			if err != nil || other.ExecutionStatus != "reserved" || other.BillingStatus != "held" {
				t.Fatal("不得波及同用户另一T2V任务")
			}
			commands, audits := f.counts(t)
			if commands != 1 || audits != 2 {
				t.Fatal("图生取消也必须使用同一管理命令和审计体系")
			}
		})
	}
}
