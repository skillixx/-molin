package service

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 旧G4门面也必须兼容已应用状态的SQL账本，不能先提交回调再以旧版本做第二次CAS并返回错误。
func TestVideoG6CallbackLegacyGatewayMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	job, err := f.App.Create(context.Background(), VideoCommand{Caller: caller, IdempotencyKey: "g6-callback-legacy-gateway", Model: f.Model, Prompt: "仅用于旧回调门面隔离验证", Operation: model.AIVideoOperationTextToVideo})
	if err != nil {
		t.Fatal(err)
	}
	f.Submit(job.Job.ID)
	var task model.AIImageTask
	if err := f.DB.Where("public_id=?", job.Job.ID).Take(&task).Error; err != nil || task.ProviderTaskID == nil {
		t.Fatal("必须有真实提交绑定")
	}
	owner := repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: &f.ProjectID}
	verifier := video.NewFakeProviderCallbackVerifier(callbackVectorKey())
	// 回调和Query只需要原账本及验签器；不装Provider可额外防止该路径偷偷发出上游请求。
	g := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: f.App.NewTaskLedger(owner, nil), Verifier: verifier})
	var envelope video.CallbackEnvelope
	for _, status := range []string{"processing", "succeeded"} {
		body := []byte(fmt.Sprintf(`{"status":%q,"progress":50}`, status))
		envelope = video.CallbackEnvelope{ProviderCode: *task.ProviderCode, ProviderTaskID: *task.ProviderTaskID, ExternalEventID: "evt-legacy-" + status, Body: body, Signature: verifier.Sign(body)}
		result, err := g.HandleCallback(context.Background(), task.PublicID, envelope)
		want := video.TaskProcessing
		if status == "succeeded" {
			want = video.TaskFetching
		}
		if err != nil || result.Status != want {
			t.Fatalf("旧门面应返回已提交的新状态，不得二次CAS：status=%s err=%v", result.Status, err)
		}
		if status == "succeeded" && result.Content == nil {
			t.Fatal("旧门面成功回调仍需提供受控内容句柄")
		}
	}
	before := f.FinancialSnapshot()
	var state model.AIImageTask
	if err := f.DB.First(&state, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if state.Status != "fetching" || state.VersionNo != task.VersionNo+2 {
		t.Fatal("两次新事件只能各应用一次")
	}
	if _, err := g.HandleCallback(context.Background(), task.PublicID, envelope); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, f.FinancialSnapshot()) || f.SubmitCalls() != 1 {
		t.Fatal("旧门面重放不得写财务或重新Submit")
	}
}
