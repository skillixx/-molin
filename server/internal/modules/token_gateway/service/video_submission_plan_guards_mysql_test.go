package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type videoG7PlanFixture struct {
	reservation videoG5ReservationFixture
	ledger      *VideoRepositoryTaskLedger
	claim       video.GatewayTask
	proof       *repository.VideoWorkerLease
	owned       context.Context
}

// 所有用例均从真实Quote/Hold/Task及原submitting事件进入；不创建Provider或伪造恒成功事务。
func prepareVideoG7Plan(t *testing.T, db *gorm.DB, operation string) videoG7PlanFixture {
	t.Helper()
	ctx := context.Background()
	f := newVideoG5ReservationFixture(t, db, "10")
	if operation == model.AIVideoOperationImageToVideo {
		prepareVideoG5I2V(t, &f)
	}
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	leases := repository.NewVideoWorkerLeaseRepository(db)
	proof, err := leases.Claim(ctx, f.command.TaskID, f.owner, "plan-guard-worker", "submit")
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
	return videoG7PlanFixture{reservation: f, ledger: ledger, claim: claim, proof: proof, owned: owned}
}

func TestVideoG7SubmissionPlanConcurrentMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(operation, func(t *testing.T) {
			f := prepareVideoG7Plan(t, db, operation)
			before := captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)
			start := make(chan struct{})
			results := make(chan error, 100)
			var workers sync.WaitGroup
			for i := 0; i < 100; i++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					<-start
					results <- f.ledger.RecordSubmissionPlan(f.owned, f.claim.TaskID, f.claim.Version, "fake-native-async")
				}()
			}
			close(start)
			workers.Wait()
			close(results)
			for err := range results {
				if err != nil {
					t.Fatalf("100个同意图调用必须创建一次或只读重放: %v", err)
				}
			}
			after := captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)
			if after.task.VersionNo != before.task.VersionNo+1 || after.task.ProviderTaskID != nil || after.task.AttemptCount != 0 || after.task.SubmissionCapacityEpoch != nil || len(after.events) != len(before.events)+1 {
				t.Fatal("100并发不能生成多份计划、伪造接受或借出容量")
			}
			plans := 0
			for _, e := range after.events {
				if e.EventType == "video_submission_planned" {
					plans++
				}
			}
			if plans != 1 || !reflect.DeepEqual(before.inputs, after.inputs) || !reflect.DeepEqual(before.finance, after.finance) {
				t.Fatal("只允许唯一计划事件，输入及财务事实必须不变")
			}
		})
	}
}

// SQL是实际数据库约束的公共边界；负例均回滚，不留下篡改后的Task或审计事实。
func TestVideoG7SubmissionPlanSQLMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(operation, func(t *testing.T) {
			f := prepareVideoG7Plan(t, db, operation)
			if err := f.ledger.RecordSubmissionPlan(f.owned, f.claim.TaskID, f.claim.Version, "fake-native-async"); err != nil {
				t.Fatal(err)
			}
			before := captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)
			for _, test := range []struct {
				name, statement, message string
				args                     []any
			}{
				{"public_identity", "UPDATE ai_gateway_tasks SET public_id=CONCAT(public_id,'_changed') WHERE id=?", "video_submission_plan_identity_immutable", []any{before.task.ID}},
				{"provider", "UPDATE ai_gateway_tasks SET planned_provider_code='other' WHERE id=?", "video_submission_plan_immutable", []any{before.task.ID}},
				{"claim", "UPDATE ai_gateway_tasks SET submission_claim_version=submission_claim_version+1 WHERE id=?", "video_submission_plan_immutable", []any{before.task.ID}},
				{"worker", "UPDATE ai_gateway_tasks SET submission_worker_version=submission_worker_version+1 WHERE id=?", "video_submission_plan_immutable", []any{before.task.ID}},
				{"clear", "UPDATE ai_gateway_tasks SET planned_provider_code=NULL,submission_intent_id=NULL,submission_claim_version=NULL,submission_worker_version=NULL WHERE id=?", "video_submission_plan_immutable", []any{before.task.ID}},
				{"capacity", "UPDATE ai_gateway_tasks SET submission_capacity_epoch=1 WHERE id=?", "video_submission_capacity_not_authorized", []any{before.task.ID}},
				{"second_event", "INSERT INTO ai_gateway_task_events(event_id,task_id,user_id,project_id,event_type,source,safe_detail_json,created_at) SELECT CONCAT(event_id,'_second'),task_id,user_id,project_id,event_type,source,safe_detail_json,created_at FROM ai_gateway_task_events WHERE task_id=? AND event_type='video_submission_planned'", "video_submission_plan_event_invalid", []any{before.task.ID}},
			} {
				t.Run(test.name, func(t *testing.T) {
					rollback := errors.New("计划SQL负例回滚")
					var observed error
					err := db.Transaction(func(tx *gorm.DB) error { observed = tx.Exec(test.statement, test.args...).Error; return rollback })
					if !errors.Is(err, rollback) {
						t.Fatal(err)
					}
					var sqlErr *drivermysql.MySQLError
					if !errors.As(observed, &sqlErr) || sqlErr.Number != 1644 || sqlErr.Message != test.message {
						t.Fatalf("必须由对应计划守卫拒绝，不能由其他错误遮蔽: %v", observed)
					}
				})
			}
			if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)) {
				t.Fatal("SQL负例留下了Task、输入、事件或财务变化")
			}
		})
	}
}

// 使用真实存在的同归属Key/Quote作为替代目标，不能用外键不存在错误冒称计划身份被保护。
func TestVideoG7SubmissionPlanIdentityMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(operation, func(t *testing.T) {
			f := prepareVideoG7Plan(t, db, operation)
			other := newVideoG5ReservationFixture(t, db, "10")
			otherKey := f.reservation.owner.UserID + 5000000
			if err := db.Exec("INSERT INTO api_keys(id,user_id,project_id,key_prefix,key_hash,name,billing_mode,scope_mode,status) VALUES(?,?,?,'plan',?,'计划同归属替代Key','postpaid','allowlist','active')", otherKey, f.reservation.owner.UserID, f.reservation.owner.ProjectID, fmt.Sprintf("plan-key-fixture-%d", otherKey)).Error; err != nil {
				t.Fatal(err)
			}
			otherQuote, _, err := f.reservation.quotes.CreateQuote(ctx, VideoCreateQuoteCommand{CommandKind: "quote", IdempotencyKey: "plan-other-quote", FingerprintInput: f.reservation.command.FingerprintInput})
			if err != nil || otherQuote.ID == f.reservation.quote.ID {
				t.Fatalf("必须准备真实同归属不同Quote: %v", err)
			}
			opposite := model.AIVideoOperationImageToVideo
			if operation == opposite {
				opposite = model.AIVideoOperationTextToVideo
			}
			mutations := []struct {
				name, assignment string
				args             []any
			}{
				{"id", "id=id+1000000", nil},
				{"public", "public_id=CONCAT(public_id,'_changed')", nil},
				{"public_case", "public_id=UPPER(public_id)", nil},
				{"public_space", "public_id=CONCAT(public_id,' ')", nil},
				{"request_case", "request_id=UPPER(request_id)", nil},
				{"user", "user_id=?", []any{other.owner.UserID}},
				{"project", "project_id=?", []any{other.owner.ProjectID}},
				{"same_owner_key", "api_key_id=?", []any{otherKey}},
				{"key_null", "api_key_id=NULL", nil},
				{"same_owner_quote", "quote_id=?", []any{otherQuote.ID}},
				{"model_case", "logical_model_code=?", []any{strings.ToUpper(f.reservation.command.FingerprintInput.LogicalModelCode)}},
				{"capability_escape", "capability='image.generate'", nil},
				{"operation", "operation=?,input_json=JSON_SET(input_json,'$.operation',?)", []any{opposite, opposite}},
				{"spec", "input_json=JSON_SET(input_json,'$.duration_seconds',10)", nil},
			}
			for _, phase := range []string{"first_plan", "after_plan"} {
				if phase == "after_plan" {
					if err := f.ledger.RecordSubmissionPlan(f.owned, f.claim.TaskID, f.claim.Version, "fake-native-async"); err != nil {
						t.Fatal(err)
					}
				}
				before := captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)
				for _, mutation := range mutations {
					t.Run(phase+"/"+mutation.name, func(t *testing.T) {
						assignment := mutation.assignment
						args := append([]any(nil), mutation.args...)
						if phase == "first_plan" {
							assignment += ",planned_provider_code='fake-native-async',submission_intent_id=?,submission_claim_version=?,submission_worker_version=?,version_no=version_no+1"
							args = append(args, f.claim.RequestID, f.claim.Version, f.proof.Version())
						}
						args = append(args, before.task.ID)
						rollback := errors.New("计划身份负例只回滚")
						var rejected error
						if err := db.Transaction(func(tx *gorm.DB) error {
							rejected = tx.Exec("UPDATE ai_gateway_tasks SET "+assignment+" WHERE id=?", args...).Error
							return rollback
						}); !errors.Is(err, rollback) {
							t.Fatal(err)
						}
						var sqlErr *drivermysql.MySQLError
						if !errors.As(rejected, &sqlErr) || sqlErr.Number != 1644 || sqlErr.Message != "video_submission_plan_identity_immutable" {
							t.Fatalf("必须命中计划身份守卫，不依赖外键或其他拒绝: %v", rejected)
						}
					})
				}
				if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)) {
					t.Fatal("身份负例不能留下改变后的任务或金融事实")
				}
			}
			// 身份冻结不能挡住合法Worker心跳和回执；两者都保持原计划身份不变。
			if _, err := repository.NewVideoWorkerLeaseRepository(db).Renew(ctx, f.proof); err != nil {
				t.Fatal(err)
			}
			planned, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.claim.TaskID, f.reservation.owner)
			if err != nil || planned.SubmissionIntentID == nil {
				t.Fatal("计划必须保存Provider taskUUID")
			}
			result, err := f.ledger.RecordSubmissionReceipt(f.owned, f.claim.TaskID, f.claim.Version, video.SubmitResult{RequestID: f.claim.RequestID, ProviderCode: "fake-native-async", ProviderTaskID: *planned.SubmissionIntentID, Status: video.ProviderTaskQueued})
			if err != nil || result.Status != video.TaskSubmitted {
				t.Fatalf("不能阻止合法心跳后的原回执绑定: %v", err)
			}
		})
	}
}
