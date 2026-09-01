package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

// 自洽计划摘要不能替代目标绑定：校验缩略图后绝不能删除计划中伪造的父对象。
func TestVideoG6MediaDeletePlanTargetBindingMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	id := f.CreateCompletedForKey(f.ProjectID)
	var root, thumb model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Where("task_id=? AND asset_role='thumbnail'", root.TaskID).Take(&thumb).Error; err != nil {
		t.Fatal(err)
	}
	if root.ObjectKey == nil || thumb.ObjectKey == nil || *root.ObjectKey == *thumb.ObjectKey {
		t.Fatal("反例必须有不同的父子对象")
	}
	var injected atomic.Bool
	const name = "g6_single_delete_poison_ref"
	if err := f.DB.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table != "ai_video_asset_deletions" {
			return
		}
		op, ok := tx.Statement.Dest.(*videoAssetDeletion)
		if !ok || !injected.CompareAndSwap(false, true) {
			return
		}
		var p videoMediaDeleteItem
		if err := json.Unmarshal(op.PlanJSON, &p); err != nil {
			tx.AddError(err)
			return
		}
		p.Bucket, p.ObjectKey = *root.Bucket, *root.ObjectKey
		raw, err := json.Marshal(p)
		if err != nil {
			tx.AddError(err)
			return
		}
		op.PlanJSON, op.PlanSHA256 = raw, videoPayloadSHA256(raw)
	}); err != nil {
		t.Fatal(err)
	}
	defer f.DB.Callback().Create().Remove(name)
	before := f.FinancialSnapshot()
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	result, err := f.App.DeleteVideoAsset(context.Background(), caller, thumb.PublicID, thumb.VersionNo, "g6-delete-poison-ref")
	if !injected.Load() {
		t.Fatal("必须实际注入计划目标漂移")
	}
	if err == nil || result != nil || f.MediaDeleteCalls() != 0 {
		t.Fatalf("伪造目标必须在Delete前拒绝：failed=%t deletes=%d", err != nil, f.MediaDeleteCalls())
	}
	for _, fact := range f.InspectMedia(id) {
		if !fact.Present || !fact.HashMatches {
			t.Fatal("错误删除计划不得损坏任何原对象")
		}
	}
	if !bytes.Equal(before, f.FinancialSnapshot()) {
		t.Fatal("目标绑定失败不能改生成财务")
	}
}

// 已持久化的合法失败记录在读取边界遭到篡改，也必须由运行时证明拒绝，而不只依靠INSERT守卫。
func TestVideoG6MediaDeleteReadTargetBindingMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	id := f.CreateCompletedForKey(f.ProjectID)
	var root, thumb model.AIImageAsset
	if err := f.DB.Where("task_id=(SELECT id FROM ai_gateway_tasks WHERE public_id=?) AND asset_role='content'", id).Take(&root).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Where("task_id=? AND asset_role='thumbnail'", root.TaskID).Take(&thumb).Error; err != nil {
		t.Fatal(err)
	}
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	f.FailMediaDelete(true)
	if _, err := f.App.DeleteVideoAsset(context.Background(), caller, thumb.PublicID, thumb.VersionNo, "g6-delete-read-poison"); !errors.Is(err, ErrVideoMediaDeleteUnavailable) {
		t.Fatalf("必须先形成真实删除失败：%v", err)
	}
	f.FailMediaDelete(false)
	var stored videoAssetDeletion
	if err := f.DB.Where("asset_id=?", thumb.ID).Take(&stored).Error; err != nil || stored.Status != "delete_failed" {
		t.Fatal("必须有合法的失败见证")
	}
	var injected atomic.Bool
	const name = "g6_single_delete_read_poison"
	if err := f.DB.Callback().Query().After("gorm:query").Register(name, func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Table != "ai_video_asset_deletions" || !strings.Contains(tx.Statement.SQL.String(), "request_id") {
			return
		}
		op, ok := tx.Statement.Dest.(*videoAssetDeletion)
		if !ok || op.AssetID != thumb.ID || !injected.CompareAndSwap(false, true) {
			return
		}
		var p videoMediaDeleteItem
		if err := json.Unmarshal(op.PlanJSON, &p); err != nil {
			tx.AddError(err)
			return
		}
		p.Bucket, p.ObjectKey = *root.Bucket, *root.ObjectKey
		raw, err := json.Marshal(p)
		if err != nil {
			tx.AddError(err)
			return
		}
		op.PlanJSON, op.PlanSHA256 = raw, videoPayloadSHA256(raw)
	}); err != nil {
		t.Fatal(err)
	}
	defer f.DB.Callback().Query().Remove(name)
	before, deletes := f.FinancialSnapshot(), f.MediaDeleteCalls()
	result, err := f.App.DeleteVideoAsset(context.Background(), caller, thumb.PublicID, thumb.VersionNo, "g6-delete-read-poison")
	if !injected.Load() || !errors.Is(err, ErrVideoMediaProtected) || result != nil || f.MediaDeleteCalls() != deletes {
		t.Fatalf("读回错误目标须在Delete前拒绝：injected=%t err=%v", injected.Load(), err)
	}
	for _, fact := range f.InspectMedia(id) {
		if !fact.Present || !fact.HashMatches {
			t.Fatal("恢复拒绝不得损坏父、兄弟或目标正文")
		}
	}
	var after videoAssetDeletion
	if err := f.DB.Where("asset_id=?", thumb.ID).Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Status != stored.Status || after.VersionNo != stored.VersionNo || after.PlanSHA256 != stored.PlanSHA256 || !bytes.Equal(before, f.FinancialSnapshot()) {
		t.Fatal("只读返回篡改不能改变真实见证或原财务")
	}
}
