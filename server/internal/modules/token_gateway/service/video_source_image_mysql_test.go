package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	imagegateway "molin/server/internal/modules/token_gateway/image"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 只替代上游图片边界，真实处理器、归档、预占、结算与对账仍按IMG-G5执行。
type videoSourceImageFake struct {
	raw   []byte
	calls atomic.Int32
}

func (f *videoSourceImageFake) Name() string { return "fake" }
func (f *videoSourceImageFake) Generate(ctx context.Context, r imagegateway.ProviderImageRequest) (imagegateway.ProviderImageResult, error) {
	if err := ctx.Err(); err != nil {
		return imagegateway.ProviderImageResult{}, err
	}
	f.calls.Add(1)
	return imagegateway.ProviderImageResult{ProviderRequestID: "fake-source-" + r.RequestID, Images: []imagegateway.ProviderImage{{Index: 0, Base64: base64.StdEncoding.EncodeToString(f.raw), MediaType: "image/png"}}}, nil
}

func TestVideoG6SourceImagesMySQL(t *testing.T) {
	db := openVideoG6MySQL(t)
	now, ctx := time.Now().UTC(), context.Background()
	ensureVideoG6ImageBase(t, db, now)
	fixture := seedImageBillingFixture(t, db, 998101, "g6-source-image", 1, imagegateway.FakeImageSuccess, imagegateway.FakeModerationAllow, decimal.NewFromInt(10), now, nil)
	for _, statement := range []string{
		"UPDATE api_keys SET video_generate_allowed=1 WHERE id=998101",
		"INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT 998101,id,code,'allow' FROM permissions WHERE code='video:generate'",
		"INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(998101,998101,998101,'molin/image-g5-mysql')",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 640, 640))); err != nil {
		t.Fatal(err)
	}
	provider := &videoSourceImageFake{raw: raw.Bytes()}
	adapter, err := NewAttemptRecordingImageAdapter(provider, db)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := imagegateway.NewImageProcessor(imagegateway.ImageProcessingLimits{MaxSourceBytes: 10 << 20, MaxNormalizedBytes: 10 << 20, MaxPixels: 16777216, MaxWidth: 4096, MaxHeight: 4096, ExpectedAspectRatio: 1, AspectTolerance: 0.01, ThumbnailMaxEdge: 32}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := repository.NewImageObjectCleanupRepository(db)
	gateway, err := imagegateway.NewImageGateway(adapter, imagegateway.NewFakeModerationAdapter(imagegateway.FakeModerationAllow), processor, fixture.store, cleanup)
	if err != nil {
		t.Fatal(err)
	}
	fixture.service.gateway = gateway
	app := &VideoHTTPService{db: db, access: NewVideoAccessService(db)}
	caller := VideoCaller{UserID: 998101, ProjectID: 998101, APIKeyID: 998101}
	page, err := app.ListInputSourceImages(ctx, caller, 1, 20)
	if err != nil || page.Total != 0 || page.Items == nil {
		t.Fatalf("未执行图片不能成为来源：%+v %v", page, err)
	}
	observedUnsettled := false
	var windowError error
	// 成功路径的结算前边界始终执行；资产提交响应异常可恢复，不能把可跳过的回执钩子当作唯一观察点。
	fixture.service.beforeFinalize = func() (resultError error) {
		defer func() { windowError = resultError }()
		var assets int64
		if err := db.Table("ai_gateway_assets").Where("request_id=? AND asset_role='primary_output' AND lifecycle_state='temporary'", fixture.requestID).Count(&assets).Error; err != nil {
			return err
		}
		var request model.AIRequest
		if err := db.Where("request_id=?", fixture.requestID).Take(&request).Error; err != nil {
			return err
		}
		page, err := app.ListInputSourceImages(ctx, caller, 1, 20)
		if err != nil {
			return err
		}
		if assets != 1 || request.BillingStatus != model.AIBillingHeld || request.DeliveryStatus != model.AIDeliveryPending || page.Total != 0 || len(page.Items) != 0 {
			return fmt.Errorf("合成媒体入库窗口不匹配：temporary主图=%d 计费=%s 交付=%s 候选=%d 页长度=%d", assets, request.BillingStatus, request.DeliveryStatus, page.Total, len(page.Items))
		}
		observedUnsettled = true
		return nil
	}
	mustReserveImageG5(t, fixture)
	execution, err := fixture.service.Execute(ctx, fixture.requestID, fixture.command)
	if err != nil || execution.BillingStatus != model.AIBillingSettled || execution.DeliveryStatus != model.AIDeliveryAvailable || provider.calls.Load() != 1 {
		t.Fatalf("合成图片必须真实结算：%v；结算前窗口=%v", err, windowError)
	}
	if !observedUnsettled {
		t.Fatal("必须实际经过媒体已入库但尚未结算的窗口")
	}
	report, err := fixture.service.ReconcileRequest(ctx, fixture.requestID)
	if err != nil || !report.ZeroDifference() {
		t.Fatal("原图片必须零差异对账")
	}
	page, err = app.ListInputSourceImages(ctx, caller, 1, 20)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].Width != 640 || page.Items[0].Height != 640 {
		t.Fatalf("只允许已结算640px主图，不包含派生图：%+v %v", page, err)
	}
	assetID := page.Items[0].AssetID
	exerciseVideoInputImport(t, db, fixture.store, caller, assetID)
	jwt := caller
	jwt.APIKeyID = 0
	if page, err := app.ListInputSourceImages(ctx, jwt, 1, 20); err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("JWT不能读取SK来源或数量")
	}
	if err := db.Exec("DELETE FROM api_key_model_scopes WHERE api_key_id=998101").Error; err != nil {
		t.Fatal(err)
	}
	if page, err := app.ListInputSourceImages(ctx, caller, 1, 20); err != nil || page.Total != 0 {
		t.Fatal("当前Key模型scope撤销必须隐藏来源")
	}
	if err := db.Exec("UPDATE api_keys SET scope_mode='all' WHERE id=998101").Error; err != nil {
		t.Fatal(err)
	}
	if page, err := app.ListInputSourceImages(ctx, caller, 1, 20); err != nil || page.Total != 0 {
		t.Fatal("all模式也不能绕过图片模型显式scope")
	}
	if err := db.Exec("INSERT INTO api_key_model_scopes(api_key_id,project_id,user_id,logical_model_code) VALUES(998101,998101,998101,'molin/image-g5-mysql')").Error; err != nil {
		t.Fatal(err)
	}
	// 只修改临时合成资产的保全事实，原结算和钱包不可回写。
	if err := db.Exec("UPDATE ai_gateway_assets SET legal_hold=1,version_no=version_no+1 WHERE public_id=?", assetID).Error; err != nil {
		t.Fatal(err)
	}
	if page, err := app.ListInputSourceImages(ctx, caller, 1, 20); err != nil || page.Total != 0 {
		t.Fatal("保全图片不得作为新输入来源")
	}
	if provider.calls.Load() != 1 {
		t.Fatal("列表查询不能重新调用Provider")
	}
}

// 仅复用相同的隔离图片基线，避免外部HTTP测试与内部服务测试依赖执行顺序；绝不覆盖不匹配数据。
func ensureVideoG6ImageBase(t *testing.T, db *gorm.DB, now time.Time) {
	t.Helper()
	var base struct{ LogicalModelCode, Capability, Status, PricePurpose string }
	err := db.Table("ai_price_versions").Where("id=?", imageG5PriceVersionID).Take(&base).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		setupImageG5Base(t, db, now)
		return
	}
	if err != nil || base.LogicalModelCode != imageG5ModelCode || base.Capability != "image.generate" || base.Status != "active" || base.PricePurpose != "test_fixture" {
		t.Fatal("隔离图片价格基线不匹配，不能覆盖既有事实")
	}
	_, variantHash, err := canonicalImageVariant(ImagePriceVariant{Resolution: "2K", AspectRatio: "1:1", Quality: "standard", OutputFormat: "provider_default", Delivery: "url"})
	if err != nil {
		t.Fatal(err)
	}
	var matching, total int64
	if err := db.Table("ai_price_skus").Where("price_version_id=?", imageG5PriceVersionID).Count(&total).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("ai_price_skus").Where("price_version_id=? AND meter_type='image_count' AND variant_hash=? AND cost_unit_price=0.3 AND sale_unit_price=0.5 AND scale=1 AND currency='CNY'", imageG5PriceVersionID, variantHash).Count(&matching).Error; err != nil || total != 1 || matching != 1 {
		t.Fatal("隔离图片SKU必须仍为原精确价格及规格")
	}
}
