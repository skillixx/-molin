package repository

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

// 无证明路径不访问数据库，也不是完整授权；畸形私有值必须在数据库操作前失败关闭。
func TestVideoG7WorkerContextLeaseInvalid(t *testing.T) {
	tx, task := &gorm.DB{}, &VideoTaskRecord{}
	if err := CheckVideoWorkerContextLeaseTx(context.Background(), tx, task); err != nil {
		t.Fatalf("控制面已有授权的无证明路径不应被隐式改写: %v", err)
	}
	for _, invalid := range []any{"forged", (*VideoWorkerLease)(nil), &VideoWorkerLease{}, &VideoWorkerLease{version: 1}} {
		ctx := context.WithValue(context.Background(), videoWorkerLeaseContextKey{}, invalid)
		if err := CheckVideoWorkerContextLeaseTx(ctx, tx, task); !errors.Is(err, ErrVideoWorkerLeaseLost) {
			t.Fatalf("无效私有证明不得当作缺省控制面: %v", err)
		}
	}
	for _, err := range []error{
		CheckVideoWorkerContextLeaseTx(nil, tx, task),
		CheckVideoWorkerContextLeaseTx(context.Background(), nil, task),
		CheckVideoWorkerContextLeaseTx(context.Background(), tx, nil),
	} {
		if !errors.Is(err, ErrVideoWorkerLeaseLost) {
			t.Fatal("缺少上下文或已锁定记录必须拒绝")
		}
	}
}
