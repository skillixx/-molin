package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7CapacityReadyMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	repo := repository.NewVideoCapacityRecoveryRepository(db)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	runID := strings.Repeat("a", 40)
	snapshot := strings.Repeat("b", 64)
	proof, err := repo.Begin(ctx, 0, "ready-publisher", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	// SQL层必须显式拒绝NULL租期；不能依赖CHECK的UNKNOWN语义或后续Go读取才发现损坏。
	beforeNullLease := captureVideoCapacityDB(t, db)
	if err := db.Exec("UPDATE ai_video_queue_admission_guard SET capacity_lease_until=NULL,version_no=version_no+1 WHERE id=1").Error; err == nil {
		t.Fatal("恢复租期NULL必须被数据库守卫拒绝")
	}
	if !reflect.DeepEqual(beforeNullLease, captureVideoCapacityDB(t, db)) {
		t.Fatal("NULL租期拒绝不得改写门闩或审计")
	}
	if err := repo.PublishReady(ctx, proof, snapshot, 3); err != nil {
		t.Fatal(err)
	}
	ready, err := repo.Current(ctx)
	if err != nil || ready.State != "ready" || ready.Epoch != 1 || ready.SnapshotHash != snapshot || ready.SnapshotCount != 3 || ready.ReadyAt.IsZero() {
		t.Fatalf("ready必须绑定原epoch和快照: %+v err=%v", ready, err)
	}
	if err := repo.ValidateReady(ctx, 1, hash, runID, snapshot, 3); err != nil {
		t.Fatal(err)
	}
	before := captureVideoCapacityDB(t, db)
	if err := repo.PublishReady(ctx, proof, snapshot, 3); err != nil {
		t.Fatalf("同ready重放必须只读: %v", err)
	}
	for _, test := range []struct {
		name, hash string
		count      uint32
	}{{"hash", strings.Repeat("c", 64), 3}, {"count", snapshot, 4}} {
		if err := repo.PublishReady(ctx, proof, test.hash, test.count); err == nil {
			t.Fatalf("%s异事实不得覆盖ready", test.name)
		}
	}
	if !reflect.DeepEqual(before, captureVideoCapacityDB(t, db)) {
		t.Fatal("ready重放和冲突不得改变门闩或审计")
	}
	if err := repo.Block(ctx, proof); !errors.Is(err, repository.ErrVideoCapacityRecoveryLost) {
		t.Fatalf("ready不能退回blocked: %v", err)
	}
	second, err := repo.Begin(ctx, 1, "ready-rebuild", hash, runID)
	if err != nil || second.Epoch() != 2 {
		t.Fatalf("ready异常恢复必须新开epoch: %v", err)
	}
	recovering, err := repo.Current(ctx)
	if err != nil || recovering.State != "recovering" || recovering.SnapshotHash != "" || recovering.SnapshotCount != 0 || !recovering.ReadyAt.IsZero() {
		t.Fatalf("新epoch必须清除旧快照绑定: %+v err=%v", recovering, err)
	}
	if err := repo.ValidateReady(ctx, 1, hash, runID, snapshot, 3); !errors.Is(err, repository.ErrVideoCapacityRecoveryLost) {
		t.Fatalf("旧ready不得跨epoch授权: %v", err)
	}
	if err := repo.Block(ctx, second); err != nil {
		t.Fatal(err)
	}
	third, err := repo.Begin(ctx, 2, "ready-commit-unknown", hash, runID)
	if err != nil {
		t.Fatal(err)
	}
	unknownSnapshot := strings.Repeat("d", 64)
	pool := &videoCapacityUnknownCommitPool{&videoBudgetCommitPool{ConnPool: db.ConnPool}}
	faultDB, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: db.Logger})
	if err != nil {
		t.Fatal(err)
	}
	err = repository.NewVideoCapacityRecoveryRepository(faultDB).PublishReady(ctx, third, unknownSnapshot, 2)
	if err == nil || !pool.lost.Load() {
		t.Fatalf("COMMIT回执丢失不能直接返回成功: lost=%v err=%v", pool.lost.Load(), err)
	}
	if err := repo.ValidateReady(ctx, 3, hash, runID, unknownSnapshot, 2); err != nil {
		t.Fatalf("正常连接必须能确认实际已提交ready: %v", err)
	}
	if err := repo.PublishReady(ctx, third, unknownSnapshot, 2); err != nil {
		t.Fatalf("查明后相同ready只读重放: %v", err)
	}
}
