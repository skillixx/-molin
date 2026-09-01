package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 仅在第二轮外部审核边界写入并发保全，确保HTTP预检及预占前读取已经完成。
type videoInputHoldRaceModerator struct {
	video.VideoModerationAdapter
	calls  atomic.Int32
	change func() error
}

func (m *videoInputHoldRaceModerator) ModeratePrompt(ctx context.Context, prompt string) error {
	if m.calls.Add(1) == 2 {
		if err := m.change(); err != nil {
			return err
		}
	}
	return m.VideoModerationAdapter.ModeratePrompt(ctx, prompt)
}

func TestVideoG6InputMetadataMySQL(t *testing.T) {
	t.Run("预检后保全在预占事务内再次拒绝", func(t *testing.T) {
		f := newVideoG6I2VFixture(t)
		ctx, c := context.Background(), f.command
		if _, err := f.app.AcceptProjectRights(ctx, VideoRightsAcceptCommand{Caller: VideoCaller{UserID: c.Caller.UserID, ProjectID: c.Caller.ProjectID}, PolicyVersion: f.policyVersion, Confirmed: true, IdempotencyKey: "g6-input-hold-race-rights", RequestID: "g6-input-hold-race-http"}); err != nil {
			t.Fatal(err)
		}
		quote, err := f.app.Quote(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		gate := &videoInputHoldRaceModerator{VideoModerationAdapter: video.NewFakeVideoModerationAdapter(video.FakeVideoModerationAllow), change: func() error {
			return f.legacy.db.Exec("UPDATE ai_gateway_input_assets SET legal_hold=1,version_no=version_no+1 WHERE id=?", f.asset.ID).Error
		}}
		f.app.billing.safety = video.NewVideoSafetyPipeline(gate, nil)
		c.QuoteID, c.IdempotencyKey = quote.QuoteID, "g6-input-hold-race-generation"
		if _, err := f.app.Create(ctx, c); !errors.Is(err, repository.ErrVideoInputUnavailable) || gate.calls.Load() != 2 {
			t.Fatalf("必须在第二轮审核之后由事务内输入守卫拒绝：calls=%d err=%v", gate.calls.Load(), err)
		}
		for _, table := range []string{"ai_requests", "ai_gateway_tasks", "wallet_holds", "ai_gateway_task_inputs"} {
			var count int64
			if err := f.legacy.db.Table(table).Where("user_id=?", c.Caller.UserID).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("保全竞争不能留下请求、预占或绑定：%s %d %v", table, count, err)
			}
		}
		var after model.AIGatewayInputAsset
		if err := f.legacy.db.First(&after, f.asset.ID).Error; err != nil || !after.LegalHold {
			t.Fatal("拒绝生成不能回滚另一事务已经提交的保全事实")
		}
	})
	t.Run("旧来源未规范化字段保持null且禁止引用", func(t *testing.T) {
		f := newVideoG6I2VFixture(t)
		db, ctx := f.legacy.db, context.Background()
		var upload model.AIUploadSession
		if err := db.First(&upload, *f.asset.UploadSessionID).Error; err != nil {
			t.Fatal(err)
		}
		upload.ID, upload.FinalInputAssetID, upload.CompletedAt = 0, nil, nil
		upload.PublicID += "_incomplete"
		upload.ObjectKey += "/incomplete"
		upload.Status = model.AIUploadSessionVerifying
		if err := db.Create(&upload).Error; err != nil {
			t.Fatal(err)
		}
		asset := f.asset
		asset.ID, asset.PublicID, asset.UploadSessionID = 0, f.asset.PublicID+"_incomplete", &upload.ID
		asset.Bucket, asset.ObjectKey, asset.NormalizedSHA256 = nil, nil, nil
		asset.MIMEType, asset.SizeBytes, asset.Width, asset.Height, asset.ModerationPolicyVersion = nil, nil, nil, nil, nil
		asset.LifecycleState, asset.ModerationStatus = model.AIInputAssetNormalizing, model.AIModerationPending
		if err := db.Create(&asset).Error; err != nil {
			t.Fatal(err)
		}
		// 防御旧schema能够保存的未完成快照；不代表G6的complete允许发布这种记录。
		if err := db.Model(&model.AIUploadSession{}).Where("id=?", upload.ID).Updates(map[string]any{"status": "completed", "final_input_asset_id": asset.ID, "completed_at": time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
		detail, err := f.app.GetInput(ctx, f.command.Caller, asset.PublicID)
		if err != nil || detail.CanReference || detail.LifecycleState != "normalizing" {
			t.Fatalf("未完成快照只允许低敏状态查询：%+v %v", detail, err)
		}
		raw, err := json.Marshal(detail)
		var fields map[string]json.RawMessage
		if err != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 10 {
			t.Fatal("低敏DTO形状错误")
		}
		for _, name := range []string{"mime_type", "size_bytes", "width", "height"} {
			if string(fields[name]) != "null" {
				t.Fatalf("未规范化字段必须显式null：%s", name)
			}
		}
	})
	t.Run("归属分页与隔离元数据", func(t *testing.T) {
		f := newVideoG6I2VFixture(t)
		ctx, c, app := context.Background(), f.command.Caller, f.app
		detail, err := app.GetInput(ctx, c, f.asset.PublicID)
		if err != nil || detail.InputAssetID != f.asset.PublicID || detail.Width == nil || *detail.Width != 640 || !detail.CanReference {
			t.Fatalf("原主体应读到可引用规范化输入：%+v %v", detail, err)
		}
		raw, err := json.Marshal(detail)
		var fields map[string]json.RawMessage
		if err != nil || json.Unmarshal(raw, &fields) != nil || len(fields) != 10 {
			t.Fatal("详情只能返回冻结的10个低敏字段")
		}
		for _, key := range []string{"input_asset_id", "source_type", "lifecycle_state", "mime_type", "size_bytes", "width", "height", "expires_at", "version_no", "can_reference"} {
			if _, ok := fields[key]; !ok {
				t.Fatalf("必需字段缺失：%s", key)
			}
		}
		page, err := app.ListInputs(ctx, c, 1, 1)
		if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].InputAssetID != detail.InputAssetID || page.Page != 1 || page.PageSize != 1 {
			t.Fatalf("D-95归属计数和分页不一致：%+v %v", page, err)
		}
		empty, err := app.ListInputs(ctx, c, 2, 1)
		if err != nil || empty.Total != 1 || empty.Items == nil || len(empty.Items) != 0 {
			t.Fatal("越过末页仍须空数组和原主体总数")
		}
		for _, foreign := range []VideoCaller{
			{UserID: c.UserID, ProjectID: c.ProjectID},
			{UserID: c.UserID, ProjectID: c.ProjectID + 1, APIKeyID: c.APIKeyID},
			{UserID: c.UserID + 1, ProjectID: c.ProjectID, APIKeyID: c.APIKeyID},
			{UserID: c.UserID, ProjectID: c.ProjectID, APIKeyID: c.APIKeyID + 1},
			{UserID: c.UserID},
		} {
			if _, err := app.GetInput(ctx, foreign, f.asset.PublicID); !errors.Is(err, repository.ErrVideoInputNotFound) {
				t.Fatalf("跨User/Project/Key以及JWT不能读取SK来源：%v", err)
			}
		}
		if _, err := app.GetInput(ctx, c, "vin_unknown_fixture"); !errors.Is(err, repository.ErrVideoInputNotFound) {
			t.Fatal("未知与越权必须相同不存在语义")
		}
		// 驱动读取故障必须保持不可用，不能被详情伪装为404或被列表伪装为空数据。
		var armed atomic.Bool
		const callback = "g6_input_metadata_read_fault"
		if err := f.legacy.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "inputs" && armed.CompareAndSwap(true, false) {
				tx.AddError(errors.New("isolated input metadata read failure"))
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.legacy.db.Callback().Query().Remove(callback) })
		armed.Store(true)
		if _, err := app.ListInputs(ctx, c, 1, 20); !errors.Is(err, ErrVideoAccessUnavailable) || armed.Load() {
			t.Fatalf("列表必须实际命中驱动故障且失败关闭：%v", err)
		}
		armed.Store(true)
		if _, err := app.GetInput(ctx, c, f.asset.PublicID); !errors.Is(err, ErrVideoAccessUnavailable) || armed.Load() {
			t.Fatalf("详情必须实际命中驱动故障且失败关闭：%v", err)
		}
		if err := f.legacy.db.Exec("UPDATE ai_gateway_input_assets SET lifecycle_state='quarantined',version_no=version_no+1 WHERE id=?", f.asset.ID).Error; err != nil {
			t.Fatal(err)
		}
		history, err := app.GetInput(ctx, c, f.asset.PublicID)
		if err != nil || history.LifecycleState != "quarantined" || history.CanReference {
			t.Fatalf("合法历史元数据可查但隔离输入不得引用：%+v %v", history, err)
		}
		page, err = app.ListInputs(ctx, c, 1, 20)
		if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].CanReference {
			t.Fatal("列表与详情必须使用同一可引用条件")
		}
		if err := f.legacy.db.Exec("UPDATE api_keys SET video_generate_allowed=0 WHERE id=?", c.APIKeyID).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := app.GetInput(ctx, c, f.asset.PublicID); !errors.Is(err, ErrVideoCapabilityDenied) {
			t.Fatalf("撤权后不能读取旧ID：%v", err)
		}
		if _, err := app.ListInputs(ctx, c, 1, 20); !errors.Is(err, ErrVideoCapabilityDenied) {
			t.Fatalf("撤权后不能读取旧列表或数量：%v", err)
		}
	})
	t.Run("保全输入元数据不构成报价授权", func(t *testing.T) {
		f := newVideoG6I2VFixture(t)
		ctx, c := context.Background(), f.command
		if _, err := f.app.AcceptProjectRights(ctx, VideoRightsAcceptCommand{Caller: VideoCaller{UserID: c.Caller.UserID, ProjectID: c.Caller.ProjectID}, PolicyVersion: f.policyVersion, Confirmed: true, IdempotencyKey: "g6-input-read-rights-0001", RequestID: "g6-input-read-http-0001"}); err != nil {
			t.Fatal(err)
		}
		if err := f.legacy.db.Exec("UPDATE ai_gateway_input_assets SET legal_hold=1,version_no=version_no+1 WHERE id=?", f.asset.ID).Error; err != nil {
			t.Fatal(err)
		}
		detail, err := f.app.GetInput(ctx, c.Caller, f.asset.PublicID)
		if err != nil || detail.CanReference {
			t.Fatal("保全输入只可读低敏元数据")
		}
		// G5夹具已有一条T2V报价；要求本次拒绝零新增，而不是误将夹具事实计为泄漏。
		var before int64
		if err := f.legacy.db.Table("ai_gateway_quotes").Where("user_id=?", c.Caller.UserID).Count(&before).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := f.app.Quote(ctx, c); !errors.Is(err, repository.ErrVideoInputUnavailable) {
			t.Fatalf("元数据和报价必须共用输入禁用前置条件：%v", err)
		}
		var count int64
		if err := f.legacy.db.Table("ai_gateway_quotes").Where("user_id=?", c.Caller.UserID).Count(&count).Error; err != nil || count != before {
			t.Fatal("保全输入不能创建Quote")
		}
		// 到期边界只能收紧可引用性，不删除历史事实或释放任务租约。
		if err := f.legacy.db.Exec("UPDATE ai_gateway_input_assets SET legal_hold=0,expires_at=?,version_no=version_no+1 WHERE id=?", time.Now().UTC().Add(-time.Second), f.asset.ID).Error; err != nil {
			t.Fatal(err)
		}
		detail, err = f.app.GetInput(ctx, c.Caller, f.asset.PublicID)
		if err != nil || detail.CanReference {
			t.Fatal("到期输入不得引用，历史元数据仍可查")
		}
	})
}
