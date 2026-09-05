package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	auditmodel "molin/server/internal/modules/audit/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 独立入口用于快速RED；完整capacity_epoch通过主测试调用同一helper，避免重复污染单行门闩。
func TestVideoG7CapacityRecoveryAuditTypesMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	policy, err := video.NewVideoCapacityPolicy(video.DefaultVideoCapacityLimits())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	verifyVideoG7CapacityRecoveryAuditTypes(t, db, repository.NewVideoCapacityRecoveryRepository(db), hash, strings.Repeat("a", 40))
}

func verifyVideoG7CapacityRecoveryAuditTypes(t *testing.T, db *gorm.DB, repo *repository.VideoCapacityRecoveryRepository, hash, redisID string) {
	ctx := context.Background()
	state := currentVideoCapacity(t, repo)
	// 数字形状的合法owner使旧UNQUOTE规则会匹配JSON数字，避免值不等遮蔽类型漏洞。
	proof, err := repo.Begin(ctx, state.Epoch, "123", hash, redisID)
	if err != nil {
		t.Fatal(err)
	}
	target := fmt.Sprintf("video-capacity:%d", proof.Epoch())
	var claimed auditmodel.AuditLog
	if err := db.Where("module='token_gateway' AND action='video_capacity_recovery_claimed' AND target_id=?", target).Take(&claimed).Error; err != nil || claimed.RequestSummary == nil {
		t.Fatal("缺少原已提交认领审计")
	}
	before := captureVideoCapacityDB(t, db)
	fields := []string{"schema", "epoch", "owner", "policy_sha256", "redis_run_id", "token_sha256", "result"}
	cases := []struct{ name, field, kind string }{{name: "valid", kind: "valid"}, {name: "owner_number", kind: "owner_number"}, {name: "schema_string", kind: "schema_string"}}
	for _, field := range fields {
		cases = append(cases, struct{ name, field, kind string }{"missing_" + field, field, "missing"}, struct{ name, field, kind string }{"null_" + field, field, "null"})
	}
	rollback := errors.New("审计类型反例仅回滚本轮事务")
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var summary map[string]any
			if err := json.Unmarshal([]byte(*claimed.RequestSummary), &summary); err != nil {
				t.Fatal(err)
			}
			summary["result"] = "blocked"
			switch test.kind {
			case "missing":
				delete(summary, test.field)
				summary["extra"] = 1
			case "null":
				summary[test.field] = nil
			case "owner_number":
				summary["owner"] = 123
			case "schema_string":
				summary["schema"] = "1"
			}
			body, err := json.Marshal(summary)
			if err != nil {
				t.Fatal(err)
			}
			var observed error
			if err := db.Transaction(func(tx *gorm.DB) error {
				changed := tx.Exec("UPDATE ai_video_queue_admission_guard SET capacity_state='blocked',version_no=version_no+1,updated_at=UTC_TIMESTAMP(6) WHERE id=1 AND capacity_epoch=? AND capacity_state='recovering'", proof.Epoch())
				if changed.Error != nil {
					return changed.Error
				}
				if changed.RowsAffected != 1 {
					return errors.New("反例必须先合法转为blocked")
				}
				var count int64
				if err := tx.Model(&auditmodel.AuditLog{}).Where("module='token_gateway' AND action='video_capacity_recovery_blocked' AND target_id=?", target).Count(&count).Error; err != nil {
					return err
				}
				if count != 0 {
					return errors.New("禁止用已存在的blocked唯一键遮蔽绑定检查")
				}
				entry := claimed
				entry.ID = 0
				entry.Action = "video_capacity_recovery_blocked"
				encoded := string(body)
				entry.RequestSummary = &encoded
				entry.CreatedAt = time.Now().UTC().Truncate(time.Second)
				observed = tx.Create(&entry).Error
				return rollback
			}); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			if test.kind == "valid" {
				if observed != nil {
					t.Fatalf("同一真实状态的合法正文必须允许: %v", observed)
				}
			} else {
				var sqlError *drivermysql.MySQLError
				if !errors.As(observed, &sqlError) || sqlError.Number != 1644 || sqlError.Message != "video_capacity_audit_binding_invalid" {
					t.Fatalf("必须由绑定触发器拒绝，不能依赖唯一键或Go解码: %v", observed)
				}
			}
			if !reflect.DeepEqual(before, captureVideoCapacityDB(t, db)) {
				t.Fatal("类型反例不能留下门闩或审计修改")
			}
		})
	}
	if err := repo.Block(ctx, proof); err != nil {
		t.Fatal(err)
	}
}
