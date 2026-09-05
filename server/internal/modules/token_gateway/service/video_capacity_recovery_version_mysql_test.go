package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

func TestVideoG7CapacityRecoveryVersionMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	repo := repository.NewVideoCapacityRecoveryRepository(db)
	state := currentVideoCapacity(t, repo)
	var owned *repository.VideoCapacityRecoveryLease
	if state.Epoch == 0 {
		policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
		if err != nil {
			t.Fatal(err)
		}
		hash, err := policy.Fingerprint()
		if err != nil {
			t.Fatal(err)
		}
		owned, err = repo.Begin(ctx, 0, "version-test", hash, strings.Repeat("a", 40))
		if err != nil {
			t.Fatal(err)
		}
		state = currentVideoCapacity(t, repo)
	}
	if state.Version < 2 || state.State != "recovering" {
		t.Fatal("必须先有真实已提交的恢复认领")
	}
	target := fmt.Sprintf("video-capacity:%d", state.Epoch)
	before := captureVideoCapacityDB(t, db)
	rollback := errors.New("恢复SQL负例只回滚本轮事务")
	cases := []struct {
		name, statement string
		args            []any
		code            uint16
	}{
		{"version_rollback", "UPDATE ai_video_queue_admission_guard SET version_no=1 WHERE id=1", nil, 1644},
		{"version_skip", "UPDATE ai_video_queue_admission_guard SET version_no=version_no+2 WHERE id=1", nil, 1644},
		{"audit_update", "UPDATE audit_logs SET request_summary=JSON_OBJECT() WHERE module='token_gateway' AND action='video_capacity_recovery_claimed' AND target_id=?", []any{target}, 1644},
		{"audit_delete", "DELETE FROM audit_logs WHERE module='token_gateway' AND action='video_capacity_recovery_claimed' AND target_id=?", []any{target}, 1644},
		{"audit_duplicate", "INSERT INTO audit_logs(operator_id,module,action,target_type,target_id,ip,request_summary,created_at) SELECT operator_id,module,action,target_type,target_id,ip,request_summary,created_at FROM audit_logs WHERE module='token_gateway' AND action='video_capacity_recovery_claimed' AND target_id=?", []any{target}, 1062},
		{"audit_case_alias", "INSERT INTO audit_logs(operator_id,module,action,target_type,target_id,ip,request_summary,created_at) SELECT operator_id,UPPER(module),action,target_type,target_id,ip,request_summary,created_at FROM audit_logs WHERE module='token_gateway' AND action='video_capacity_recovery_claimed' AND target_id=?", []any{target}, 1644},
		{"audit_wrong_owner", "INSERT INTO audit_logs(operator_id,module,action,target_type,target_id,ip,request_summary,created_at) SELECT operator_id,module,action,target_type,target_id,ip,JSON_SET(request_summary,'$.owner','wrong-owner'),created_at FROM audit_logs WHERE module='token_gateway' AND action='video_capacity_recovery_claimed' AND target_id=?", []any{target}, 1644},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var observed error
			if err := db.Transaction(func(tx *gorm.DB) error { observed = tx.Exec(test.statement, test.args...).Error; return rollback }); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			var sqlError *drivermysql.MySQLError
			if !errors.As(observed, &sqlError) || sqlError.Number != test.code {
				t.Fatalf("必须由SQL守卫拒绝且不是其他约束遮蔽: %v", observed)
			}
		})
	}
	if !reflect.DeepEqual(before, captureVideoCapacityDB(t, db)) {
		t.Fatal("SQL负例不可留下元数据或审计变化")
	}
	// 新守卫只保留本模块的两类恢复事实，不把其他模块审计的历史行为一并改写。
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("INSERT INTO audit_logs(module,action,target_type,target_id,request_summary,created_at) VALUES('synthetic_other','test','test','capacity-compat',JSON_OBJECT(),UTC_TIMESTAMP())").Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE audit_logs SET action='updated' WHERE module='synthetic_other' AND target_id='capacity-compat'").Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM audit_logs WHERE module='synthetic_other' AND target_id='capacity-compat'").Error; err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("不能改变其他审计模块语义: %v", err)
	}
	if owned != nil {
		if err := repo.Block(ctx, owned); err != nil {
			t.Fatal(err)
		}
	}
}
