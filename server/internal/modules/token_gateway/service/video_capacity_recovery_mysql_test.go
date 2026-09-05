package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7CapacityRecoveryEpochMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	f := newVideoG5ReservationFixture(t, db, "10")
	prepareVideoG5I2V(t, &f)
	if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
		t.Fatal(err)
	}
	before := captureVideoG7TaskWrite(t, db, f.command.TaskID, f.owner)
	repo := repository.NewVideoCapacityRecoveryRepository(db)
	initial, err := repo.Current(ctx)
	if err != nil || initial.Epoch != 0 || initial.State != "uninitialized" {
		t.Fatalf("原门闩新增字段须关闭初态: %v", err)
	}
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	redisID := strings.Repeat("a", 40)
	var won, conflicts, unexpected atomic.Int64
	proofs := make(chan *repository.VideoCapacityRecoveryLease, 100)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			p, err := repo.Begin(ctx, 0, fmt.Sprintf("recovery-%d", i), hash, redisID)
			switch {
			case err == nil:
				won.Add(1)
				proofs <- p
			case errors.Is(err, repository.ErrVideoCapacityRecoveryConflict):
				conflicts.Add(1)
			default:
				unexpected.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(proofs)
	if won.Load() != 1 || conflicts.Load() != 99 || unexpected.Load() != 0 {
		t.Fatalf("100个CAS必须只有一个持久恢复赢家: won=%d conflict=%d other=%d", won.Load(), conflicts.Load(), unexpected.Load())
	}
	proof := <-proofs
	if proof == nil || proof.Epoch() != 1 {
		t.Fatal("成功提交后应返回第一代私有证明")
	}
	if _, err := repo.Begin(ctx, 1, "other-recovery", hash, redisID); !errors.Is(err, repository.ErrVideoCapacityRecoveryBusy) {
		t.Fatalf("已知当前代次也不得抢占有效持有者: %v", err)
	}
	if err := repo.Validate(ctx, proof); err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{proof, *proof} {
		raw, err := json.Marshal(v)
		if err != nil || string(raw) != `{"redacted":true}` || strings.Contains(fmt.Sprintf("%#v", v), "nonce:") {
			t.Fatal("恢复证明不得进入普通JSON或日志")
		}
	}
	time.Sleep(20 * time.Millisecond)
	renewed, err := repo.Renew(ctx, proof)
	if err != nil || !renewed.Deadline().After(proof.Deadline()) {
		t.Fatalf("有效证明应按DB时间续期: %v", err)
	}
	if err := repo.Block(ctx, renewed); err != nil {
		t.Fatal(err)
	}
	blocked, err := repo.Current(ctx)
	if err != nil || blocked.State != "blocked" || blocked.Epoch != 1 {
		t.Fatalf("失败结束只阻断，不发布ready: %v", err)
	}
	if err := repo.Block(ctx, proof); err != nil {
		t.Fatalf("相同结束重放应只读: %v", err)
	}
	again, _ := repo.Current(ctx)
	if !reflect.DeepEqual(blocked, again) {
		t.Fatal("阻断重放不能增加版本或改变时间")
	}
	if err := repo.Validate(ctx, proof); !errors.Is(err, repository.ErrVideoCapacityRecoveryLost) {
		t.Fatalf("已阻断证明不能继续授权恢复: %v", err)
	}
	second, err := repo.Begin(ctx, 1, "second-recovery", hash, redisID)
	if err != nil || second.Epoch() != 2 {
		t.Fatalf("重新恢复须递增，不复用旧代次: %v", err)
	}
	if err := repo.Block(ctx, proof); !errors.Is(err, repository.ErrVideoCapacityRecoveryLost) {
		t.Fatalf("旧证明不得阻断新代次: %v", err)
	}
	time.Sleep(time.Until(second.Deadline()) + 50*time.Millisecond)
	if _, err := repo.Renew(ctx, second); !errors.Is(err, repository.ErrVideoCapacityRecoveryLost) {
		t.Fatalf("过期证明不得续期: %v", err)
	}
	third, err := repo.Begin(ctx, 2, "takeover-recovery", hash, redisID)
	if err != nil || third.Epoch() != 3 {
		t.Fatalf("实际30秒到期应可接管: %v", err)
	}
	if err := repo.Block(ctx, second); !errors.Is(err, repository.ErrVideoCapacityRecoveryLost) {
		t.Fatalf("过期持有者不能影响接管: %v", err)
	}
	if err := repo.Block(ctx, third); err != nil {
		t.Fatal(err)
	}
	// 内部savepoint成功不等于外层COMMIT；不能从现有事务借出提前可用的证明。
	if err := db.Transaction(func(tx *gorm.DB) error {
		_, err := repository.NewVideoCapacityRecoveryRepository(tx).Begin(ctx, 3, "nested-recovery", hash, redisID)
		if !errors.Is(err, repository.ErrVideoCapacityRecoveryUnavailable) {
			t.Fatalf("嵌套事务不得授予持久证明: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, f.command.TaskID, f.owner)) {
		t.Fatal("恢复元数据不得改变Task、输入或任何资金事实")
	}
	t.Run("sql_and_legacy_guard", func(t *testing.T) { verifyVideoG7CapacityRecoverySQL(t, db, repo, hash, redisID, f) })
	t.Run("audit_and_transaction_boundaries", func(t *testing.T) { verifyVideoG7CapacityRecoveryFailures(t, db, repo, hash, redisID) })
	t.Run("block_tail_expiry", func(t *testing.T) { verifyVideoG7CapacityRecoveryTail(t, db, repo, hash, redisID) })
	t.Run("audit_json_types", func(t *testing.T) { verifyVideoG7CapacityRecoveryAuditTypes(t, db, repo, hash, redisID) })
	t.Run("committed_but_ack_lost", func(t *testing.T) { verifyVideoG7CapacityRecoveryCommitUnknown(t, db, repo, hash, redisID) })
	if !reflect.DeepEqual(before, captureVideoG7TaskWrite(t, db, f.command.TaskID, f.owner)) {
		t.Fatal("失败注入和恢复不能改变原I2V输入与财务事实")
	}
}
