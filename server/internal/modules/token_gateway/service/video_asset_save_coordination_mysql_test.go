package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	assetmodel "molin/server/internal/modules/asset/model"
	productmodel "molin/server/internal/modules/product/model"
	"molin/server/internal/modules/token_gateway/model"
)

// 直接写协调表检验真实触发器；建表成功不能替代已结算/已交付请求的INSERT正反例。
func TestVideoG6SavedCoordinationMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	id := f.CreateCompletedForKey(f.ProjectID)
	var task model.AIImageTask
	if err := f.DB.Where("public_id=?", id).Take(&task).Error; err != nil {
		t.Fatal(err)
	}
	p := productmodel.Product{ProductType: "storage", ProductCode: fmt.Sprintf("video-save-storage-%d", f.ProjectID), Name: "合成存储商品", Status: "active"}
	if err := f.DB.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	a := assetmodel.UserAsset{UserID: f.ProjectID, ProductID: p.ID, AssetType: "storage", Status: "active"}
	if err := f.DB.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	quota := decimal.NewFromInt(1 << 30)
	unit := "bytes"
	e := assetmodel.UserEntitlement{UserID: f.ProjectID, AssetID: a.ID, ProductID: p.ID, EntitlementType: "storage_bytes", QuotaTotal: &quota, QuotaUnit: &unit, Status: "active"}
	if err := f.DB.Create(&e).Error; err != nil {
		t.Fatal(err)
	}
	var assets []model.AIImageAsset
	if err := f.DB.Where("task_id=? AND asset_role<>'moderation_copy'", task.ID).Order("id").Find(&assets).Error; err != nil {
		t.Fatal(err)
	}
	var total uint64
	plan := []map[string]any{}
	for _, a := range assets {
		total += *a.SizeBytes
		plan = append(plan, map[string]any{"source_asset_id": a.ID, "role": a.AssetRole})
	}
	if len(plan) != 5 {
		t.Fatal("必须使用真实五个交付对象")
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"task_id": task.ID, "public_id": fmt.Sprintf("vsave-fixture-%d", task.ID), "request_id": task.RequestID, "user_id": f.ProjectID, "project_id": f.ProjectID, "api_key_id": f.ProjectID, "status": "copying", "version_no": 1, "storage_entitlement_id": e.ID, "storage_product_id": p.ID, "quota_unit": unit, "quota_amount": total, "total_bytes": total, "policy_version": "fixture-only-v1", "plan_json": string(raw), "plan_sha256": videoPayloadSHA256(raw), "created_at": time.Now().UTC()}
	base["storage_entitlement_type"] = e.EntitlementType
	rollback := errors.New("保留隔离测试基线")
	if err := f.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Table("ai_video_asset_saves").Create(base).Error; err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatalf("真实已结算已交付Task的协调写入失败：%v", err)
	}
	for _, change := range []map[string]any{{"api_key_id": f.ProjectID + 9000000}, {"storage_product_id": p.ID + 1}, {"status": "completed"}, {"storage_entitlement_type": nil}, {"storage_entitlement_type": "api_calls"}, {"storage_entitlement_type": "STORAGE_BYTES"}} {
		row := map[string]any{}
		for key, value := range base {
			row[key] = value
		}
		for key, value := range change {
			row[key] = value
		}
		err := f.DB.Table("ai_video_asset_saves").Create(row).Error
		var sqlErr *mysqlDriver.MySQLError
		if !errors.As(err, &sqlErr) || sqlErr.Number != 1644 {
			t.Fatalf("错误身份或提前完成必须由触发器1644拒绝：%v", err)
		}
	}
	// 保存时冻结的类型不可在后续更新中替换，也不能利用数据库不区分大小写的默认排序规则。
	for _, changedType := range []any{nil, "api_calls", "STORAGE_BYTES"} {
		if err := f.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Table("ai_video_asset_saves").Create(base).Error; err != nil {
				return err
			}
			err := tx.Table("ai_video_asset_saves").Where("task_id=?", task.ID).Updates(map[string]any{"storage_entitlement_type": changedType, "version_no": 2}).Error
			var sqlErr *mysqlDriver.MySQLError
			if !errors.As(err, &sqlErr) || sqlErr.Number != 1644 {
				t.Fatal("冻结权益类型UPDATE必须由SQL拒绝")
			}
			return rollback
		}); !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	}
}
