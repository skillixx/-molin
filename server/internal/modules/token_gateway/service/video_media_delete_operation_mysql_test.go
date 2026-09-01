package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// 真实COMMIT返回之后才用另一连接提交发布变更，不改写事务结果或准入服务。
type mediaDeleteCommitBoundary struct {
	gorm.ConnPool
	commits    atomic.Int64
	afterFirst func() error
}

func (p *mediaDeleteCommitBoundary) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &mediaDeleteCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type mediaDeleteCommitTx struct {
	gorm.ConnPool
	tx   *sql.Tx
	pool *mediaDeleteCommitBoundary
}

func (t *mediaDeleteCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.pool.commits.Add(1) == 1 {
		return t.pool.afterFirst()
	}
	return nil
}
func (t *mediaDeleteCommitTx) Rollback() error { return t.tx.Rollback() }

func TestVideoG6MediaDeleteOperationAfterPrepareMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	id := f.CreateCompletedForKey(f.ProjectID)
	before := f.FinancialSnapshot()
	var changed atomic.Bool
	pool := &mediaDeleteCommitBoundary{ConnPool: f.DB.ConnPool, afterFirst: func() error {
		err := f.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("UPDATE ai_model_release_versions SET status='retired' WHERE model_id=? AND status='active'", f.ProjectID).Error; err != nil {
				return err
			}
			if err := tx.Exec("INSERT INTO ai_model_release_versions(model_id,version_no,status,snapshot_json,reason,created_by,published_at) SELECT model_id,2,'active',JSON_SET(snapshot_json,'$.video_contract.supported_operations',CAST(? AS JSON)),'合成删除操作撤销',created_by,UTC_TIMESTAMP() FROM ai_model_release_versions WHERE model_id=? AND version_no=1", `["image_to_video"]`, f.ProjectID).Error; err != nil {
				return err
			}
			return tx.Exec("UPDATE token_models SET release_version_no=2 WHERE id=?", f.ProjectID).Error
		})
		changed.Store(err == nil)
		return err
	}}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	f.App.db = db
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	result, err := f.App.DeleteMedia(context.Background(), caller, id, "g6-operation-after-prepare")
	if !changed.Load() || pool.commits.Load() != 1 {
		t.Fatal("必须在准备真实提交后成功发布撤销操作")
	}
	if !errors.Is(err, ErrVideoOptionUnsupported) || result != nil || f.MediaDeleteCalls() != 0 {
		t.Errorf("已撤下原操作必须先拒绝再接触对象：expected_error=%t deletes=%d", errors.Is(err, ErrVideoOptionUnsupported), f.MediaDeleteCalls())
	}
	for role, fact := range f.InspectMedia(id) {
		if !fact.Present || !fact.HashMatches || fact.Deleted {
			t.Errorf("撤下操作后必须保留对象：%s", role)
		}
	}
	if !bytes.Equal(before, f.FinancialSnapshot()) {
		t.Fatal("操作撤销不得改写原生成财务")
	}
}
