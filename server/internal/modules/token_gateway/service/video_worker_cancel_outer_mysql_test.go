package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	auditmodel "molin/server/internal/modules/audit/model"
	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	pkgjwt "molin/server/pkg/jwt"
)

// 分别比较原命令和审计事实，不能只验证内层Task/资金回滚却遗留外层成功回执。
type videoG7OuterCancelSnapshot struct {
	business videoG7TaskWriteSnapshot
	user     []videoCancellationCommand
	admin    []videoAdminCancellationRecord
	audits   []auditmodel.AuditLog
}

func captureVideoG7OuterCancel(t *testing.T, db *gorm.DB, id string, owner repository.VideoOwner) videoG7OuterCancelSnapshot {
	t.Helper()
	s := videoG7OuterCancelSnapshot{business: captureVideoG7TaskWrite(t, db, id, owner)}
	for _, query := range []struct {
		dest  any
		where string
		arg   any
	}{
		{&s.user, "task_id=?", s.business.task.ID},
		{&s.admin, "task_id=?", s.business.task.ID},
		{&s.audits, "target_id=? AND module='token_gateway'", id},
	} {
		if err := db.Where(query.where, query.arg).Find(query.dest).Error; err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// 公开取消服务经过真实G6准入和G5资金事务；仅在数据库响应边界注入延迟。
func TestVideoG7WorkerCancelOuterTransactionMySQL(t *testing.T) {
	for _, operation := range []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo} {
		for _, mode := range []string{"user", "admin"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				// 独立GORM连接拥有独立回调；政策创建与任务生成先串行，再并行等待真实租约过期。
				db := openVideoG5MySQL(t)
				f := newVideoG6I2VFixtureWithDB(t, db)
				ctx := context.Background()
				c := f.command
				c.Operation = operation
				if operation == model.AIVideoOperationTextToVideo {
					c.InputAssetID, c.RightsAttestation = "", false
				} else {
					if _, err := f.app.AcceptProjectRights(ctx, VideoRightsAcceptCommand{Caller: VideoCaller{UserID: c.Caller.UserID, ProjectID: c.Caller.ProjectID}, PolicyVersion: f.policyVersion, Confirmed: true, IdempotencyKey: "g7-outer-rights-0001", RequestID: "g7-outer-rights-request-0001"}); err != nil {
						t.Fatal(err)
					}
				}
				created, err := f.app.Create(ctx, c)
				if err != nil {
					t.Fatal(err)
				}
				id, owner := created.Job.ID, f.legacy.owner
				positiveCommand := c
				positiveCommand.IdempotencyKey = "g7-outer-current-worker-create"
				positive, err := f.app.Create(ctx, positiveCommand)
				if err != nil {
					t.Fatal(err)
				}
				initial := captureVideoG7OuterCancel(t, db, id, owner)
				var admin *VideoAdminService
				var command VideoAdminCancelCommand
				if mode == "admin" {
					verified := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
					actor := authmodel.User{ID: NextVideoFixtureUserID(), Status: "active", PasswordHash: "synthetic-only", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
					if err := db.Create(&actor).Error; err != nil {
						t.Fatal(err)
					}
					secret, reasonKey := make([]byte, 32), make([]byte, 32)
					if _, err := rand.Read(secret); err != nil {
						t.Fatal(err)
					}
					if _, err := rand.Read(reasonKey); err != nil {
						t.Fatal(err)
					}
					jwtSecret := hex.EncodeToString(secret)
					auth, err := NewVideoJWTAuthenticator(db, jwtSecret, &videoTestRevocations{revoked: map[string]bool{}})
					if err != nil {
						t.Fatal(err)
					}
					token, err := pkgjwt.Generate(actor.ID, "", jwtSecret, 3600)
					if err != nil {
						t.Fatal(err)
					}
					caller, err := auth.Authenticate(ctx, token)
					if err != nil {
						t.Fatal(err)
					}
					protector, err := NewVideoAdminReasonProtector("g7-outer-cancel", reasonKey)
					if err != nil {
						t.Fatal(err)
					}
					admin, err = NewVideoAdminService(f.app, 24, VideoAdminWriteOptions{ReasonProtector: protector})
					if err != nil {
						t.Fatal(err)
					}
					command = VideoAdminCancelCommand{Caller: caller, TaskID: id, VersionNo: initial.business.task.VersionNo, IdempotencyKey: "g7-outer-admin-cancel", Reason: "合成外层取消围栏验证"}
					if _, err := admin.CancelTask(ctx, command); !errors.Is(err, ErrVideoAdminForbidden) {
						t.Fatalf("未授权管理员不得取消: %v", err)
					}
					if !reflect.DeepEqual(initial, captureVideoG7OuterCancel(t, db, id, owner)) {
						t.Fatal("权限拒绝必须零业务写入")
					}
					grant := db.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:task_manage'", actor.ID)
					if grant.Error != nil || grant.RowsAffected != 1 {
						t.Fatalf("必须建立真实管理权限: %v", grant.Error)
					}
					if err := db.Model(&authmodel.User{}).Where("id=?", actor.ID).Update("admin_email_verified_at", nil).Error; err != nil {
						t.Fatal(err)
					}
					if _, err := admin.CancelTask(ctx, command); !errors.Is(err, ErrVideoAdminMFA) {
						t.Fatalf("有管理权限但缺少MFA仍须拒绝: %v", err)
					}
					if !reflect.DeepEqual(initial, captureVideoG7OuterCancel(t, db, id, owner)) {
						t.Fatal("MFA拒绝必须零业务写入")
					}
					if err := db.Model(&authmodel.User{}).Where("id=?", actor.ID).Update("admin_email_verified_at", verified).Error; err != nil {
						t.Fatal(err)
					}
				}
				// 创建已绑定原政策快照；取消不依赖当前政策，精确退役本例合成政策后才允许下一例创建。
				retired := db.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE policy_version=? AND status='active'", f.policyVersion)
				if retired.Error != nil || retired.RowsAffected != 1 {
					t.Fatalf("必须精确退役本例政策: %v", retired.Error)
				}
				t.Parallel()
				leases := repository.NewVideoWorkerLeaseRepository(db)
				proof, err := leases.Claim(ctx, id, owner, "g7-outer-cancel-worker", "submit")
				if err != nil {
					t.Fatal(err)
				}
				before := captureVideoG7OuterCancel(t, db, id, owner)
				deadline, hits := proof.Deadline(), 0
				const hook = "g7:cancel_outer_detail_delay"
				// 此详情查询在内层取消完整返回后发生；不修改SQL结果、业务方法或认证决定。
				if err := db.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
					if hits == 0 && tx.Error == nil && tx.Statement.Table == "ai_gateway_quotes" && strings.Join(tx.Statement.Selects, ",") == "public_id,currency,quoted_amount,logical_model_code,operation,consumed_request_id" {
						hits++
						time.Sleep(time.Until(deadline) + 200*time.Millisecond)
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := db.Callback().Query().Remove(hook); err != nil {
						t.Error(err)
					}
				})
				if time.Until(deadline) < 3*time.Second {
					t.Fatal("准备阶段耗尽租约观察窗")
				}
				time.Sleep(time.Until(deadline) - 2*time.Second)
				cancelTask := func(callCtx context.Context) (bool, error) {
					if mode == "admin" {
						r, err := admin.CancelTask(callCtx, command)
						if err != nil {
							if r != nil {
								t.Fatal("失败不能返回成功回执")
							}
							return false, err
						}
						if r == nil || r.CancellationResult != "cancelled" || r.BillingStatus != model.AIBillingReleased {
							t.Fatal("管理取消成功回执不完整")
						}
						return r.Idempotent, nil
					}
					r, err := f.app.CancelTask(callCtx, c.Caller, id, "g7-outer-user-cancel")
					if err != nil {
						if r != nil {
							t.Fatal("失败不能返回成功回执")
						}
						return false, err
					}
					if r == nil || r.CancellationResult != "cancelled" || r.BillingStatus != model.AIBillingReleased {
						t.Fatal("用户取消成功回执不完整")
					}
					return r.Idempotent, nil
				}
				owned, cancel := context.WithTimeout(repository.WithVideoWorkerLease(ctx, proof), 8*time.Second)
				defer cancel()
				_, err = cancelTask(owned)
				if !errors.Is(err, repository.ErrVideoWorkerLeaseLost) || errors.Is(err, context.DeadlineExceeded) || hits != 1 {
					t.Fatalf("外层必须识别真实到期而非超时: hits=%d err=%v", hits, err)
				}
				if !reflect.DeepEqual(before, captureVideoG7OuterCancel(t, db, id, owner)) {
					t.Fatal("外层失权必须撤销命令、审计、Task、资金、事件和输入释放")
				}
				if _, err := cancelTask(repository.WithVideoWorkerLease(ctx, proof)); !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
					t.Fatalf("已经到期的Worker必须在新命令入口拒绝: %v", err)
				}
				if !reflect.DeepEqual(before, captureVideoG7OuterCancel(t, db, id, owner)) {
					t.Fatal("入口失权必须零业务写入")
				}
				if replay, err := cancelTask(ctx); err != nil || replay {
					t.Fatalf("原控制面授权不依赖Worker租约: %v", err)
				}
				replayBefore := captureVideoG7OuterCancel(t, db, id, owner)
				if replay, err := cancelTask(repository.WithVideoWorkerLease(ctx, proof)); err != nil || !replay {
					t.Fatalf("旧证明允许只读重放: %v", err)
				}
				if !reflect.DeepEqual(replayBefore, captureVideoG7OuterCancel(t, db, id, owner)) {
					t.Fatal("幂等重放不能追加写入")
				}
				// 独立原任务证明有效Worker的首次命令也能提交，避免把全部带证明请求拒绝当成修复。
				current, err := leases.Claim(ctx, positive.Job.ID, owner, "g7-current-worker", "submit")
				if err != nil {
					t.Fatal(err)
				}
				currentCtx := repository.WithVideoWorkerLease(ctx, current)
				if mode == "admin" {
					currentCommand := command
					currentCommand.TaskID, currentCommand.IdempotencyKey = positive.Job.ID, "g7-current-admin-cancel"
					currentCommand.VersionNo = captureVideoG7OuterCancel(t, db, positive.Job.ID, owner).business.task.VersionNo
					r, err := admin.CancelTask(currentCtx, currentCommand)
					if err != nil || r == nil || r.Idempotent || r.CancellationResult != "cancelled" || r.BillingStatus != model.AIBillingReleased {
						t.Fatalf("有效Worker管理取消必须成功: %v", err)
					}
				} else {
					r, err := f.app.CancelTask(currentCtx, c.Caller, positive.Job.ID, "g7-current-user-cancel")
					if err != nil || r == nil || r.Idempotent || r.CancellationResult != "cancelled" || r.BillingStatus != model.AIBillingReleased {
						t.Fatalf("有效Worker用户取消必须成功: %v", err)
					}
				}
				if err := leases.Release(ctx, current); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
