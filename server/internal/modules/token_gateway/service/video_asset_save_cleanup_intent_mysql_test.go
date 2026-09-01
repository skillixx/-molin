package service

import (
	"context"
	"errors"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"molin/server/internal/modules/token_gateway/model"
)

// cleanup_pending是后续删除的持久化授权，必须拒绝与真实到期实体无关的伪过去时间。
func TestVideoG6AssetSaveCleanupIntentMySQL(t *testing.T) {
	for _, reason := range []string{"source_expired", "entitlement_expired"} {
		t.Run(reason, func(t *testing.T) {
			f := NewVideoContentHTTPFixture(t)
			f.EnableAssetSaving()
			id := f.CreateCompletedForKey(f.ProjectID)
			var root model.AIImageAsset
			if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
				t.Fatal(err)
			}
			f.FailSaveAfterOne(true)
			if _, err := f.App.SaveVideoAsset(context.Background(), VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}, root.PublicID, "g6-cleanup-forged-intent"); !errors.Is(err, ErrVideoSaveUnavailable) {
				t.Fatal("必须先产生未发布部分保存")
			}
			var op videoAssetSave
			if err := f.DB.Where("task_id=?", root.TaskID).Take(&op).Error; err != nil {
				t.Fatal(err)
			}
			err := f.DB.Model(&videoAssetSave{}).Where("task_id=?", op.TaskID).Updates(map[string]any{"status": "cleanup_pending", "version_no": op.VersionNo + 1, "cleanup_policy_version": "fixture-cleanup-v1", "cleanup_reason": reason, "cleanup_eligible_at": time.Now().UTC().Add(-time.Hour), "cleanup_started_at": time.Now().UTC()}).Error
			var sqlErr *mysqlDriver.MySQLError
			if !errors.As(err, &sqlErr) || sqlErr.Number != 1644 {
				t.Fatalf("伪到期意图必须被真实SQL守卫拒绝1644，实际%v", err)
			}
		})
	}
}
