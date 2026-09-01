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
	assetmodel "molin/server/internal/modules/asset/model"
)

// 先让真实MySQL完成COMMIT，再丢失应用层确认；不是普通UPDATE失败或事务回滚的替代测试。
type videoCleanupCommitPool struct {
	gorm.ConnPool
	commits atomic.Int64
	lost    error
}

func (p *videoCleanupCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &videoCleanupCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type videoCleanupCommitTx struct {
	gorm.ConnPool
	tx   *sql.Tx
	pool *videoCleanupCommitPool
}

func (t *videoCleanupCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.pool.commits.Add(1) == 2 {
		return t.pool.lost
	}
	return nil
}
func (t *videoCleanupCommitTx) Rollback() error { return t.tx.Rollback() }

func TestVideoG6AssetSaveCleanupCommitUnknownMySQL(t *testing.T) {
	f, op, owner, policy := expiredVideoSaveFixture(t)
	before := f.FinancialSnapshot()
	lost := errors.New("合成COMMIT已成功但确认丢失")
	pool := &videoCleanupCommitPool{ConnPool: f.DB.ConnPool, lost: lost}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	f.App.db = db
	if result, err := f.App.CleanupVideoAssetSave(context.Background(), op.PublicID, owner, policy); !errors.Is(err, lost) || result != nil {
		t.Fatalf("必须实际丢失第二阶段COMMIT确认：%v", err)
	}
	if pool.commits.Load() != 2 {
		t.Fatal("必须完成两个真实事务提交")
	}
	var observed videoAssetSave
	if err := f.DB.Where("task_id=?", op.TaskID).Take(&observed).Error; err != nil {
		t.Fatal(err)
	}
	if observed.Status != "aborted" || observed.CleanupFinishedAt == nil {
		t.Fatal("确认虽丢失，真实数据库仍须已提交完成事实")
	}
	var ent assetmodel.UserEntitlement
	if err := f.DB.First(&ent, op.StorageEntitlementID).Error; err != nil {
		t.Fatal(err)
	}
	if !ent.QuotaReserved.IsZero() || !ent.QuotaUsed.IsZero() {
		t.Fatal("真实COMMIT已精确释放一次预占")
	}
	result, err := f.App.CleanupVideoAssetSave(context.Background(), op.PublicID, owner, policy)
	if err != nil || result == nil || !result.Idempotent {
		t.Fatalf("应从持久化完成事实恢复，不再次释放：%v", err)
	}
	if err := f.DB.First(&ent, op.StorageEntitlementID).Error; err != nil {
		t.Fatal(err)
	}
	if !ent.QuotaReserved.IsZero() || !ent.QuotaUsed.IsZero() || pool.commits.Load() != 3 {
		t.Fatal("重放只能读回完成状态，不得再次释放或进入第二完成事务")
	}
	var events int64
	if err := f.DB.Table("asset_events").Where("user_id=? AND event_type='video_save_aborted' AND remark=?", owner.UserID, videoSaveCleanupRemark(&op)).Count(&events).Error; err != nil || events != 1 {
		t.Fatal("COMMIT未知重放后仍只能存在一条清理事件")
	}
	if !bytes.Equal(before, f.FinancialSnapshot()) {
		t.Fatal("COMMIT未知恢复不能修改原生成财务")
	}
}
