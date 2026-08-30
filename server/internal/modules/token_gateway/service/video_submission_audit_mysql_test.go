package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

// 直接SQL也须满足专用审计形状；合法对照使用不同摘要，排除唯一键冲突造成的假阳性。
func TestVideoG5SubmissionMySQLAuditSQLConstraints(t *testing.T) {
	db := openVideoG5MySQL(t)
	f, l, claim, receipt, _ := videoG5ClaimFixture(t, db)
	if _, err := l.RecordSubmissionReceipt(context.Background(), claim.TaskID, claim.Version, receipt); err != nil {
		t.Fatal(err)
	}
	task, err := repository.NewVideoTaskRepository(db).FindForOwner(context.Background(), claim.TaskID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"missing_hash", "wrong_source", "from_status", "to_status", "wrong_owner", "unsafe_detail", "wrong_key", "valid_control"} {
		hash := videoBillingDigest("non-commercial-audit-fixture:" + change)
		row := map[string]interface{}{"event_id": "vg5_" + videoBillingDigest(task.RequestID+":submission_rejected:"+hash), "task_id": task.ID, "user_id": task.UserID, "project_id": task.ProjectID, "event_type": "submission_receipt_rejected", "source": "worker", "fact_sha256": hash, "safe_detail_json": `{"reason":"state_advanced"}`, "created_at": time.Now().UTC()}
		switch change {
		case "missing_hash":
			row["fact_sha256"] = nil
		case "wrong_source":
			row["source"] = "api"
		case "from_status":
			row["from_status"] = "submitted"
		case "to_status":
			row["to_status"] = "pending_reconcile"
		case "wrong_owner":
			row["user_id"] = task.UserID + 1
		case "unsafe_detail":
			row["safe_detail_json"] = `{"reason":"state_advanced","provider_body":"forbidden"}`
		case "wrong_key":
			row["event_id"] = "forged_" + hash
		}
		err := db.Table("ai_gateway_task_events").Create(row).Error
		if change == "valid_control" && err != nil {
			t.Fatalf("合法独立摘要对照必须能追加: %v", err)
		}
		if change != "valid_control" && err == nil {
			t.Fatalf("数据库未拒绝%s", change)
		}
	}
}

// 已有财务终态之后，原提交回执仍可只读重放；异回执只追加审计，不改钱包和三轴。
func TestVideoG5SubmissionMySQLTerminalReceiptPreservesFinance(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, terminal := range []string{model.AIBillingSettled, model.AIBillingReleased} {
		t.Run(terminal, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
				t.Fatal(err)
			}
			l := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, nil)
			if terminal == model.AIBillingSettled {
				runVideoG5ReadyFixture(t, f)
				if _, err := f.service.SettleReady(context.Background(), f.command.TaskID, f.owner); err != nil {
					t.Fatal(err)
				}
			} else {
				g := videogateway.NewVideoGateway(videogateway.VideoGatewayDependencies{Ledger: l, Provider: videogateway.NewFakeAsyncVideoAdapter(videogateway.FakeVideoSuccess), Safety: videogateway.NewVideoSafetyPipeline(videogateway.NewFakeVideoModerationAdapter(videogateway.FakeVideoModerationAllow), videogateway.NewFakeVideoSampler(videogateway.FakeVideoSampleSuccess))})
				if _, err := g.Submit(context.Background(), f.command.TaskID); err != nil {
					t.Fatal(err)
				}
				if _, err := g.Cancel(context.Background(), f.command.TaskID); err != nil {
					t.Fatal(err)
				}
				if _, err := f.service.ReleaseUnserviceable(context.Background(), f.command.TaskID, f.owner); err != nil {
					t.Fatal(err)
				}
			}
			tasks := repository.NewVideoTaskRepository(db)
			before, err := tasks.FindForOwner(context.Background(), f.command.TaskID, f.owner)
			if err != nil || before.BillingStatus != terminal {
				t.Fatal("夹具必须先形成真实合成钱包终态")
			}
			var claim model.AIGatewayTaskEvent
			if err := db.Where("task_id=? AND event_type='execution_status_changed' AND to_status='submitting'", before.ID).First(&claim).Error; err != nil {
				t.Fatal(err)
			}
			v, err := strconv.ParseUint(claim.EventID[strings.LastIndex(claim.EventID, "_")+1:], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			r := videogateway.SubmitResult{RequestID: before.RequestID, ProviderCode: *before.ProviderCode, ProviderTaskID: *before.ProviderTaskID, Status: videogateway.ProviderTaskQueued}
			var walletRowsBefore int64
			if err := db.Table("wallet_transactions").Where("user_id=?", f.owner.UserID).Count(&walletRowsBefore).Error; err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := l.RecordSubmissionReceipt(context.Background(), before.PublicID, v+1, r); err != nil {
					t.Fatalf("已终态原回执应幂等: %v", err)
				}
				wrong := r
				wrong.Status = videogateway.ProviderTaskUnknown
				if _, err := l.RecordSubmissionReceipt(context.Background(), before.PublicID, v+1, wrong); !errors.Is(err, ErrVideoBillingConflict) {
					t.Fatalf("终态异回执须拒绝并留审计: %v", err)
				}
			}
			after, err := tasks.FindForOwner(context.Background(), before.PublicID, f.owner)
			if err != nil || after.VersionNo != before.VersionNo || after.Status != before.Status || after.BillingStatus != before.BillingStatus || after.DeliveryStatus != before.DeliveryStatus {
				t.Fatal("审计不得改变既有三轴终态")
			}
			var walletRowsAfter int64
			if err := db.Table("wallet_transactions").Where("user_id=?", f.owner.UserID).Count(&walletRowsAfter).Error; err != nil || walletRowsAfter != walletRowsBefore {
				t.Fatal("拒绝与重放不得发生新钱包动作")
			}
		})
	}
}
