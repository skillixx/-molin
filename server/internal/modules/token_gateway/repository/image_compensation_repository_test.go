package repository

import (
	"errors"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
)

func TestPlanImageCompensationRequestClaim(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	activeLease := now.Add(-time.Minute)
	staleLease := now.Add(-3 * time.Minute)
	cases := []struct {
		name          string
		task          model.AICompensationTask
		wantClaim     bool
		wantCompleted bool
		wantRestore   string
		wantErr       error
	}{
		{name: "pending可领取", task: model.AICompensationTask{Status: "pending"}, wantClaim: true, wantRestore: "pending"},
		{name: "retry可领取", task: model.AICompensationTask{Status: "retry"}, wantClaim: true, wantRestore: "retry"},
		{name: "dead可领取", task: model.AICompensationTask{Status: "dead"}, wantClaim: true, wantRestore: "dead"},
		{name: "manual_review可领取", task: model.AICompensationTask{Status: "manual_review"}, wantClaim: true, wantRestore: "manual_review"},
		{name: "completed无需重复领取", task: model.AICompensationTask{Status: "completed"}, wantCompleted: true},
		{name: "活跃running保持busy", task: model.AICompensationTask{Status: "running", LockedAt: &activeLease}, wantErr: ErrImageCompensationBusy},
		{name: "过期running可接管", task: model.AICompensationTask{Status: "running", LockedAt: &staleLease}, wantClaim: true, wantRestore: "retry"},
		{name: "无租约running按过期处理", task: model.AICompensationTask{Status: "running"}, wantClaim: true, wantRestore: "retry"},
		{name: "非法状态失败关闭", task: model.AICompensationTask{Status: "invalid"}, wantErr: ErrImageCompensationLeaseLost},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			plan, err := planImageCompensationRequestClaim(item.task, now.Add(-2*time.Minute))
			if !errors.Is(err, item.wantErr) {
				t.Fatalf("领取规划错误: err=%v want=%v", err, item.wantErr)
			}
			if plan.Claim != item.wantClaim || plan.Completed != item.wantCompleted || plan.RestoreStatus != item.wantRestore {
				t.Fatalf("领取规划不符: plan=%+v", plan)
			}
		})
	}
}
