package service

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 从同一真实账本构造不同根连接或归属；不替换认证、事务与财务服务为恒成功实现。
func planLedgerForDB(db *gorm.DB, f videoG7PlanFixture, owner repository.VideoOwner) *VideoRepositoryTaskLedger {
	return NewVideoBillingTaskLedger(db, owner, f.reservation.service.protector, videoG4TestLocationFactory{}, f.reservation.service.referenceLoader)
}

func TestVideoG7SubmissionPlanRootAndOwnerMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(operation, func(t *testing.T) {
			f := prepareVideoG7Plan(t, db, operation)
			before := captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)
			other := newVideoG5ReservationFixture(t, db, "10")
			for _, dimension := range []string{"user", "project", "key", "key_null"} {
				owner := f.reservation.owner
				switch dimension {
				case "user":
					owner.UserID = other.owner.UserID
				case "project":
					owner.ProjectID = other.owner.ProjectID
				case "key":
					owner.APIKeyID = other.owner.APIKeyID
				case "key_null":
					owner.APIKeyID = nil
				}
				if err := planLedgerForDB(db, f, owner).RecordSubmissionPlan(f.owned, f.claim.TaskID, f.claim.Version, "fake-native-async"); !errors.Is(err, repository.ErrVideoTaskNotFound) {
					t.Fatalf("%s越权须保持统一不存在语义: %v", dimension, err)
				}
			}
			for _, variant := range []string{"plain", "context", "session", "prepared"} {
				if err := db.Transaction(func(tx *gorm.DB) error {
					switch variant {
					case "context":
						tx = tx.WithContext(f.owned)
					case "session":
						tx = tx.Session(&gorm.Session{NewDB: true})
					case "prepared":
						tx = tx.Session(&gorm.Session{PrepareStmt: true})
					}
					if err := planLedgerForDB(tx, f, f.reservation.owner).RecordSubmissionPlan(f.owned, f.claim.TaskID, f.claim.Version, "fake-native-async"); !errors.Is(err, ErrVideoBillingState) {
						t.Fatalf("%s不能将savepoint成功冒充根COMMIT: %v", variant, err)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)) {
				t.Fatal("归属和嵌套拒绝必须零写入")
			}
			if err := planLedgerForDB(db.Session(&gorm.Session{PrepareStmt: true}), f, f.reservation.owner).RecordSubmissionPlan(f.owned, f.claim.TaskID, f.claim.Version, "fake-native-async"); err != nil {
				t.Fatalf("合法根PreparedStmt不能被误拒绝: %v", err)
			}
		})
	}
}

func TestVideoG7SubmissionPlanCommitUnknownMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(operation, func(t *testing.T) {
			f := prepareVideoG7Plan(t, db, operation)
			before := captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)
			// 沿用真实sql.Tx.Commit成功后丢返回值的边界包装；不声称这是TCP断连。
			pool := &videoCapacityUnknownCommitPool{&videoBudgetCommitPool{ConnPool: db.ConnPool}}
			faultDB, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: db.Logger})
			if err != nil {
				t.Fatal(err)
			}
			err = planLedgerForDB(faultDB, f, f.reservation.owner).RecordSubmissionPlan(f.owned, f.claim.TaskID, f.claim.Version, "fake-native-async")
			if err == nil || !pool.lost.Load() {
				t.Fatalf("真实COMMIT后丢确认不得返回持久化成功: lost=%v err=%v", pool.lost.Load(), err)
			}
			committed := captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)
			if committed.task.VersionNo != before.task.VersionNo+1 || committed.task.SubmissionIntentID == nil || !videoProviderTaskUUIDPattern.MatchString(*committed.task.SubmissionIntentID) || len(committed.events) != len(before.events)+1 || committed.task.ProviderTaskID != nil || committed.task.AttemptCount != 0 || committed.task.SubmissionCapacityEpoch != nil {
				t.Fatal("提交未知应保留已提交计划，但不能变成Provider接受或容量许可")
			}
			if !reflect.DeepEqual(before.inputs, committed.inputs) || !reflect.DeepEqual(before.finance, committed.finance) {
				t.Fatal("提交未知不能触发输入或资金补偿")
			}
			if err := f.ledger.RecordSubmissionPlan(f.owned, f.claim.TaskID, f.claim.Version, "fake-native-async"); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(committed, captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)) {
				t.Fatal("查明原计划后的重放必须零写，不新建计划")
			}
		})
	}
}

func TestVideoG7SubmissionPlanTailExpiryMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		t.Run(operation, func(t *testing.T) {
			f := prepareVideoG7Plan(t, db, operation)
			before := captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)
			// 先等到原30秒真实租约接近到期，再在5秒有界根事务内跨过它；不用请求超时替代围栏证据。
			time.Sleep(time.Until(f.proof.Deadline().Add(-time.Second)))
			hits := 0
			f.ledger.financialFault = func(stage string) error {
				if stage == "submission_plan" {
					hits++
					time.Sleep(time.Until(f.proof.Deadline()) + 100*time.Millisecond)
				}
				return nil
			}
			err := f.ledger.RecordSubmissionPlan(f.owned, f.claim.TaskID, f.claim.Version, "fake-native-async")
			f.ledger.financialFault = nil
			if hits != 1 || !errors.Is(err, repository.ErrVideoWorkerLeaseLost) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("必须实际写完计划事件后因Worker到期回滚: hits=%d err=%v", hits, err)
			}
			if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, f.claim.TaskID, f.reservation.owner)) {
				t.Fatal("事务尾失权必须撤销整份计划和事件，保留原Task、输入和资金事实")
			}
		})
	}
}
