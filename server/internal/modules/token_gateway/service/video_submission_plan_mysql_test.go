package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 只记录持久计划，不构造或调用Provider；服务接口不得返回新的执行许可。
type videoSubmissionPlanRecorder interface {
	RecordSubmissionPlan(context.Context, string, uint64, string) error
}

func TestVideoG7SubmissionPlanMySQL(t *testing.T) {
	if _, ok := any((*VideoRepositoryTaskLedger)(nil)).(videoSubmissionPlanRecorder); !ok {
		t.Fatal("尚未提供持久提交计划服务")
	}
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(operation, func(t *testing.T) {
			f := newVideoG5ReservationFixture(t, db, "10")
			if operation == model.AIVideoOperationImageToVideo {
				prepareVideoG5I2V(t, &f)
			}
			// 原RequestID合同允许128字符；65字符已足以证明新增列不能缩短为64。
			length := 65
			if operation == model.AIVideoOperationImageToVideo {
				length = 128
			}
			f.command.RequestID += strings.Repeat("x", length-len(f.command.RequestID))
			if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
				t.Fatal(err)
			}
			leases := repository.NewVideoWorkerLeaseRepository(db)
			proof, err := leases.Claim(ctx, f.command.TaskID, f.owner, "plan-writer", "submit")
			if err != nil {
				t.Fatal(err)
			}
			owned := repository.WithVideoWorkerLease(ctx, proof)
			ledger := NewVideoBillingTaskLedger(db, f.owner, f.service.protector, videoG4TestLocationFactory{}, f.service.referenceLoader)
			claim, err := ledger.Load(owned, f.command.TaskID)
			if err != nil {
				t.Fatal(err)
			}
			for _, state := range []video.TaskStatus{video.TaskQueued, video.TaskSubmitting} {
				claim, err = ledger.Advance(owned, claim.TaskID, claim.Version, state, "worker", "state_advanced", nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			recorder := any(ledger).(videoSubmissionPlanRecorder)
			before := captureVideoG7TaskWrite(t, db, claim.TaskID, f.owner)
			if err := recorder.RecordSubmissionPlan(ctx, claim.TaskID, claim.Version, "fake-native-async"); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
				t.Fatalf("无执行证明不能记录首次计划: %v", err)
			}
			if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, claim.TaskID, f.owner)) {
				t.Fatal("拒绝无证明必须零写")
			}
			finance := mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)
			inputs := repository.NewVideoTaskInputRepository(db)
			originalInputs, err := inputs.ListForOwner(ctx, claim.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			// 注入在事件写入之后，整笔计划和事件都必须回滚，不能留下半份提交身份。
			injected := errors.New("计划事件后故障")
			ledger.financialFault = func(stage string) error {
				if stage == "submission_plan" {
					return injected
				}
				return nil
			}
			if err := recorder.RecordSubmissionPlan(owned, claim.TaskID, claim.Version, "fake-native-async"); !errors.Is(err, injected) {
				ledger.financialFault = nil
				t.Fatalf("计划后故障必须透传并回滚: %v", err)
			}
			ledger.financialFault = nil
			if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, claim.TaskID, f.owner)) {
				t.Fatal("计划事件后故障留下了部分事实")
			}
			if err := recorder.RecordSubmissionPlan(owned, claim.TaskID, claim.Version, "fake-native-async"); err != nil {
				t.Fatal(err)
			}
			tasks := repository.NewVideoTaskRepository(db)
			recorded, err := tasks.FindForOwner(ctx, claim.TaskID, f.owner)
			if err != nil || recorded.VersionNo != claim.Version+1 || recorded.Status != model.AIImageTaskSubmitting || recorded.ProviderCode != nil || recorded.ProviderTaskID != nil || recorded.AttemptCount != 0 {
				t.Fatalf("计划只能新增一个业务版本，不能伪造Provider已接受: %v", err)
			}
			if recorded.PlannedProviderCode == nil || *recorded.PlannedProviderCode != "fake-native-async" || recorded.SubmissionIntentID == nil || !videoProviderTaskUUIDPattern.MatchString(*recorded.SubmissionIntentID) || recorded.SubmissionClaimVersion == nil || *recorded.SubmissionClaimVersion != claim.Version || recorded.SubmissionWorkerVersion == nil || *recorded.SubmissionWorkerVersion != proof.Version() || recorded.SubmissionCapacityEpoch != nil {
				t.Fatal("计划必须冻结原身份和原claim，不能伪造容量代次")
			}
			events, err := repository.NewVideoTaskEventRepository(db).ListForOwner(ctx, claim.TaskID, f.owner)
			if err != nil {
				t.Fatal(err)
			}
			plans := 0
			for _, event := range events {
				if event.EventType == "video_submission_planned" {
					plans++
				}
			}
			if plans != 1 {
				t.Fatal("首次计划必须同事务追加唯一事件")
			}
			body, err := json.Marshal(recorded)
			if err != nil || strings.Contains(string(body), "fake-native-async") || strings.Contains(string(body), "submission_intent") {
				t.Fatal("计划路由和内部提交字段不能进入普通JSON")
			}
			frozen := captureVideoG7TaskWrite(t, db, claim.TaskID, f.owner)
			if err := recorder.RecordSubmissionPlan(owned, claim.TaskID, claim.Version, "fake-native-async"); err != nil {
				t.Fatalf("相同计划应只读重放: %v", err)
			}
			if err := recorder.RecordSubmissionPlan(owned, claim.TaskID, claim.Version+1, "fake-native-async"); err == nil {
				t.Fatal("不能把计划后的版本冒充原submitting版本")
			}
			if err := recorder.RecordSubmissionPlan(owned, claim.TaskID, claim.Version, "other-provider"); err == nil {
				t.Fatal("不能改写原Provider计划")
			}
			if !reflect.DeepEqual(frozen, captureVideoG7TaskWrite(t, db, claim.TaskID, f.owner)) || !reflect.DeepEqual(finance, mediaDeleteFinanceSnapshot(t, db, f.owner.UserID)) {
				t.Fatal("计划重放/冲突不得修改原事实或财务")
			}
			lastInputs, err := inputs.ListForOwner(ctx, claim.TaskID, f.owner)
			if err != nil || !reflect.DeepEqual(originalInputs, lastInputs) {
				t.Fatal("计划不能释放或替换输入")
			}
			if _, err := ledger.ValidateSubmissionClaim(owned, claim.TaskID, claim.Version); err != nil {
				t.Fatalf("计划后的原claim仍须可验证: %v", err)
			}
			if _, err := ledger.ValidateSubmissionClaim(owned, claim.TaskID, claim.Version+1); err == nil {
				t.Fatal("计划的新版本不能替代原claim")
			}
			expectedStatus := video.TaskSubmitted
			if operation == model.AIVideoOperationImageToVideo {
				if _, err := ledger.Advance(owned, claim.TaskID, recorded.VersionNo, video.TaskPendingReconcile, "worker", "submit_unknown", nil); err != nil {
					t.Fatal(err)
				}
				expectedStatus = video.TaskPendingReconcile
			}
			// 使用受控回执夹具验证原c版本兼容；此处没有实际Provider或Fake HTTP请求。
			receipt := video.SubmitResult{RequestID: claim.RequestID, ProviderCode: "fake-native-async", ProviderTaskID: *recorded.SubmissionIntentID, Status: video.ProviderTaskQueued}
			wrong := receipt
			wrong.ProviderTaskID = "taskUUID-ffffffff-ffff-4fff-afff-ffffffffffff"
			if _, err := ledger.RecordSubmissionReceipt(owned, claim.TaskID, claim.Version, wrong); !errors.Is(err, ErrVideoBillingConflict) {
				t.Fatalf("Provider返回非预存taskUUID必须拒绝: %v", err)
			}
			accepted, err := ledger.RecordSubmissionReceipt(owned, claim.TaskID, claim.Version, receipt)
			if err != nil || accepted.Status != expectedStatus || accepted.ProviderTaskID != receipt.ProviderTaskID {
				t.Fatalf("计划后原回执必须继续绑定且pending不回退: %v", err)
			}
			if err := leases.Release(ctx, proof); err != nil {
				t.Fatal(err)
			}
		})
	}
}
