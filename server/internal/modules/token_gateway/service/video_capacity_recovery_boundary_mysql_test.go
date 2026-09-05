package service

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 数值上界使用独立新库，不能让耗尽后的全局门闩污染其他合同测试。
// 夹具恢复全部SQL守卫后才调用公开Repository；不把DDL准备冒称业务恢复。
func TestVideoG7CapacityRecoveryBoundaryMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	repo := repository.NewVideoCapacityRecoveryRepository(db)
	initial := currentVideoCapacity(t, repo)
	if initial.Epoch != 0 || initial.State != "uninitialized" || !videoG7WorkerLeaseDDLTarget(db) {
		t.Fatal("上界测试仅允许runner绑定的全新隔离库")
	}
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	seedVideoCapacityUpperBoundary(t, db, hash)
	before := currentVideoCapacity(t, repo)
	if before.Epoch != math.MaxUint64-1 || before.Version != math.MaxUint64-3 || before.State != "blocked" {
		t.Fatal("公开查询必须无损读取大整数夹具及完整审计")
	}
	last, err := repo.Begin(ctx, math.MaxUint64-1, "boundary-worker", hash, strings.Repeat("b", 40))
	if err != nil || last == nil || last.Epoch() != math.MaxUint64 {
		t.Fatalf("最后一次合法认领必须成功且不能浮点舍入: %v", err)
	}
	if err := repo.Validate(ctx, last); err != nil {
		t.Fatal(err)
	}
	renewed, err := repo.Renew(ctx, last)
	if err != nil || renewed.Epoch() != math.MaxUint64 {
		t.Fatalf("最大epoch仍能续期且不能因无关epoch加法溢出拒绝: %v", err)
	}
	if err := repo.Block(ctx, renewed); err != nil {
		t.Fatalf("最后版本必须能完成阻断并保存审计: %v", err)
	}
	final := currentVideoCapacity(t, repo)
	if final.Epoch != math.MaxUint64 || final.Version != math.MaxUint64 || final.State != "blocked" {
		t.Fatal("阻断后代次和版本应精确到达uint64上界")
	}
	facts := captureVideoCapacityDB(t, db)
	if proof, err := repo.Begin(ctx, math.MaxUint64, "overflow-worker", hash, strings.Repeat("c", 40)); proof != nil || !errors.Is(err, repository.ErrVideoCapacityRecoveryExhausted) {
		t.Fatalf("代次/版本耗尽必须给出受控错误并且不返回许可: %v", err)
	}
	if err := repo.Block(ctx, renewed); err != nil {
		t.Fatalf("版本耗尽不能破坏同证明已阻断的只读重放: %v", err)
	}
	if err := repo.Validate(ctx, renewed); !errors.Is(err, repository.ErrVideoCapacityRecoveryLost) {
		t.Fatalf("已阻断证明不能作为有效恢复许可: %v", err)
	}
	rollback := errors.New("数值上界负例回滚")
	for _, test := range []struct{ name, sql string }{
		{"version_wrap", "UPDATE ai_video_queue_admission_guard SET version_no=0 WHERE id=1"},
		{"epoch_wrap", "UPDATE ai_video_queue_admission_guard SET capacity_epoch=0 WHERE id=1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var rejected error
			err := db.Transaction(func(tx *gorm.DB) error { rejected = tx.Exec(test.sql).Error; return rollback })
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			var sqlErr *drivermysql.MySQLError
			if !errors.As(rejected, &sqlErr) || sqlErr.Number != 1644 {
				t.Fatalf("直接SQL回绕必须由恢复守卫拒绝: %v", rejected)
			}
		})
	}
	if !reflect.DeepEqual(facts, captureVideoCapacityDB(t, db)) {
		t.Fatal("耗尽、只读重放和直接SQL拒绝不得修改门闩或审计")
	}
}

// 准备距离上界一步的合成事实，避免执行2^64次认领；不删除或覆盖已有业务事实。
// 只短暂移除本阶段UPDATE触发器，CHECK、审计INSERT及追加守卫始终开启。
func seedVideoCapacityUpperBoundary(t *testing.T, db *gorm.DB, policy string) {
	t.Helper()
	if !videoG7WorkerLeaseDDLTarget(db) {
		t.Fatal("隔离实例绑定失效，禁止调整触发器")
	}
	var body string
	if err := db.Raw("SELECT ACTION_STATEMENT FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name='trg_video_capacity_epoch_update'").Scan(&body).Error; err != nil || body == "" {
		t.Fatalf("无法保存本轮SQL守卫: %v", err)
	}
	if err := db.Exec("DROP TRIGGER trg_video_capacity_epoch_update").Error; err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() error {
		return db.Exec("CREATE TRIGGER trg_video_capacity_epoch_update BEFORE UPDATE ON ai_video_queue_admission_guard FOR EACH ROW " + body).Error
	}
	defer func() {
		if !restored {
			if err := restore(); err != nil {
				t.Errorf("恢复临时库触发器失败: %v", err)
			}
		}
	}()
	err := db.Transaction(func(tx *gorm.DB) error {
		result := tx.Exec("UPDATE ai_video_queue_admission_guard SET version_no=18446744073709551612,capacity_epoch=18446744073709551614,capacity_state='recovering',capacity_policy_sha256=?,capacity_redis_run_id=?,capacity_recovery_owner='boundary-seed',capacity_token_sha256=?,capacity_heartbeat_at=UTC_TIMESTAMP(6),capacity_lease_until=UTC_TIMESTAMP(6)+INTERVAL 30 SECOND WHERE id=1 AND capacity_epoch=0 AND capacity_state='uninitialized'", policy, strings.Repeat("a", 40), strings.Repeat("d", 64))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("极限夹具禁止覆盖已初始化事实")
		}
		for _, outcome := range []string{"claimed", "blocked"} {
			if outcome == "blocked" {
				if err := tx.Exec("UPDATE ai_video_queue_admission_guard SET capacity_state='blocked' WHERE id=1").Error; err != nil {
					return err
				}
			}
			if err := tx.Exec("INSERT INTO audit_logs(module,action,target_type,target_id,request_summary,created_at) SELECT 'token_gateway',?,'video_capacity_domain',CONCAT('video-capacity:',capacity_epoch),JSON_OBJECT('schema',1,'epoch',CAST(capacity_epoch AS CHAR),'owner',capacity_recovery_owner,'policy_sha256',capacity_policy_sha256,'redis_run_id',capacity_redis_run_id,'token_sha256',capacity_token_sha256,'result',?),UTC_TIMESTAMP() FROM ai_video_queue_admission_guard WHERE id=1", "video_capacity_recovery_"+outcome, outcome).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	restored = true
}
