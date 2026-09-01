package service

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/model"
)

// 在真实列表成员SELECT返回后、任务锁之前插入另一连接操作，不伪造SELECT结果或状态。
type adminListSelectPool struct {
	gorm.ConnPool
	once        atomic.Bool
	afterSelect func() error
}

func (p *adminListSelectPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &adminListSelectTx{ConnPool: tx, tx: tx, p: p}, nil
}

type adminListSelectTx struct {
	gorm.ConnPool
	tx *sql.Tx
	p  *adminListSelectPool
}

func (t *adminListSelectTx) Commit() error   { return t.tx.Commit() }
func (t *adminListSelectTx) Rollback() error { return t.tx.Rollback() }
func (t *adminListSelectTx) QueryContext(ctx context.Context, q string, args ...interface{}) (*sql.Rows, error) {
	rows, err := t.ConnPool.QueryContext(ctx, q, args...)
	if err == nil && strings.Contains(q, "selected_tasks") && t.p.once.CompareAndSwap(false, true) {
		if e := t.p.afterSelect(); e != nil {
			_ = rows.Close()
			return nil, e
		}
	}
	return rows, err
}

func TestVideoG6AdminListStatusRaceMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	owner := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	job, err := f.App.Create(context.Background(), VideoCommand{Caller: owner, IdempotencyKey: "g6-admin-list-race-create", Model: f.Model, Prompt: "仅用于列表筛选竞争", Operation: model.AIVideoOperationTextToVideo})
	if err != nil {
		t.Fatal(err)
	}
	verified := time.Now().UTC().Truncate(time.Second).Add(-time.Minute)
	admin := authmodel.User{ID: NextVideoFixtureUserID(), PasswordHash: "synthetic-only", Status: "active", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
	if err := f.DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:view'", admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	actor, err := f.JWT.Authenticate(context.Background(), f.TokenForUser(admin.ID))
	if err != nil {
		t.Fatal(err)
	}
	app, err := NewVideoAdminService(f.App, 24)
	if err != nil {
		t.Fatal(err)
	}
	var cancelledFacts []byte
	pool := &adminListSelectPool{ConnPool: f.DB.ConnPool, afterSelect: func() error {
		result, err := f.App.CancelTask(context.Background(), owner, job.Job.ID, "g6-admin-list-race-cancel")
		if err != nil {
			return err
		}
		if result.CancellationResult != "cancelled" {
			t.Fatal("必须真实取消所选reserved任务")
		}
		cancelledFacts = f.FinancialSnapshot()
		return nil
	}}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.UseApplicationDB(db))
	page, err := app.ListTasks(context.Background(), actor, VideoAdminTaskFilter{Page: 1, PageSize: 20, UserID: f.ProjectID, Status: "reserved"})
	if err != nil {
		t.Fatal(err)
	}
	if !pool.once.Load() {
		t.Fatal("必须命中选择成员与锁任务之间的窗口")
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("状态漂移必须重算整页，不能返回不匹配项：total=%d items=%d", page.Total, len(page.Items))
	}
	if !bytes.Equal(cancelledFacts, f.FinancialSnapshot()) {
		t.Fatal("列表重试不得再次取消或改写财务")
	}
}
