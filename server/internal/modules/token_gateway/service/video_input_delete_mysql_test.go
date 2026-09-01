package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 已有I2V任务申请删除输入时必须保留冻结绑定，新报价拒绝且直接清理不能越过执行租约。
func TestVideoG6InputDeferredDeleteMySQL(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	ctx := context.Background()
	if _, err := f.app.AcceptProjectRights(ctx, VideoRightsAcceptCommand{Caller: VideoCaller{UserID: f.legacy.owner.UserID, ProjectID: f.legacy.owner.ProjectID}, PolicyVersion: f.policyVersion, Confirmed: true, IdempotencyKey: "g6-delete-rights-0001", RequestID: "g6-delete-rights-request"}); err != nil {
		t.Fatal(err)
	}
	c := f.command
	c.IdempotencyKey = "g6-delete-generation-0001"
	created, err := f.app.Create(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	inputs := repository.NewVideoInputAssetRepository(f.legacy.db)
	deferred, ok := any(inputs).(interface {
		RequestDeferredDelete(context.Context, string, repository.VideoOwner, uint64, string, time.Time) (*model.AIGatewayInputAsset, bool, error)
	})
	if !ok {
		t.Fatal("缺少保留原绑定的幂等删除申请入口")
	}
	before, err := repository.NewVideoTaskInputRepository(f.legacy.db).ListForOwner(ctx, created.Job.ID, f.legacy.owner)
	if err != nil || len(before) != 1 {
		t.Fatalf("必须有真实G5预占建立的唯一绑定：%v", err)
	}
	key := videoBillingDigest("g6-input-delete-command-0001")
	now := time.Now().UTC().Truncate(time.Second)
	asset, replay, err := deferred.RequestDeferredDelete(ctx, f.asset.PublicID, f.legacy.owner, f.asset.VersionNo, key, now)
	if err != nil || replay || asset.LifecycleState != "pending_delete" || asset.VersionNo != f.asset.VersionNo+1 {
		t.Fatalf("在途删除只应建立pending_delete：%+v replay=%v err=%v", asset, replay, err)
	}
	// 在删除已提交前建立RR快照的幂等重放，由独立用例覆盖；此处先检查凭据读取故障不能伪装漂移。
	readFailure := errors.New("合成删除凭据读取故障")
	const readHook = "g6_delete_receipt_read_failure"
	if err := f.legacy.db.Callback().Query().Before("gorm:query").Register(readHook, func(tx *gorm.DB) {
		if tx.Statement.Table == "ai_video_input_deletion_requests" {
			tx.AddError(readFailure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, observedFailure := inputs.ValidateTaskInputForProvider(ctx, created.Job.ID, f.legacy.owner, now)
	f.legacy.db.Callback().Query().Remove(readHook)
	if !errors.Is(observedFailure, readFailure) {
		t.Errorf("依赖读取错误不能被吞成快照漂移：%v", observedFailure)
	}
	again, replay, err := deferred.RequestDeferredDelete(ctx, f.asset.PublicID, f.legacy.owner, f.asset.VersionNo, key, now.Add(time.Second))
	if err != nil || !replay || again.VersionNo != asset.VersionNo || !again.PendingDeleteAt.Equal(*asset.PendingDeleteAt) {
		t.Fatalf("原键重放不能递增版本或续期：%v", err)
	}
	if _, _, err := deferred.RequestDeferredDelete(ctx, f.asset.PublicID, f.legacy.owner, f.asset.VersionNo+1, key, now); !errors.Is(err, repository.ErrVideoInputConflict) {
		t.Fatalf("同键更换CAS意图必须冲突：%v", err)
	}
	if _, err := inputs.ValidateTaskInputForProvider(ctx, created.Job.ID, f.legacy.owner, now); err != nil {
		t.Fatalf("已冻结任务仍应通过专用快照复验：%v", err)
	}
	after, err := repository.NewVideoTaskInputRepository(f.legacy.db).ListForOwner(ctx, created.Job.ID, f.legacy.owner)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("删除申请不得改写绑定版本/hash或释放租约")
	}
	c.IdempotencyKey = "g6-delete-new-quote-0001"
	if _, err := f.app.Quote(ctx, c); err == nil {
		t.Fatal("pending_delete不得形成新Quote")
	}
	if err := f.legacy.db.Model(&model.AIGatewayInputAsset{}).Where("id=?", asset.ID).Updates(map[string]any{"lifecycle_state": "deleting", "version_no": asset.VersionNo + 1}).Error; err == nil {
		t.Fatal("直接SQL清理也不能越过活跃执行租约")
	}
	if err := f.legacy.db.Transaction(func(tx *gorm.DB) error {
		var snapshot model.AIGatewayInputAsset
		if err := tx.Where("id=?", asset.ID).Take(&snapshot).Error; err != nil {
			return err
		}
		if err := f.legacy.db.Model(&model.AIGatewayInputAsset{}).Where("id=?", asset.ID).Update("version_no", asset.VersionNo+1).Error; err != nil {
			return err
		}
		if _, err := repository.NewVideoInputAssetRepository(tx).ValidateTaskInputForProvider(ctx, created.Job.ID, f.legacy.owner, now); !errors.Is(err, repository.ErrVideoInputSnapshotDrift) {
			t.Errorf("已有RR快照也必须看到独立连接提交的版本漂移：%v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := deferred.RequestDeferredDelete(ctx, f.asset.PublicID, f.legacy.owner, f.asset.VersionNo, key, now); !errors.Is(err, repository.ErrVideoInputConflict) {
		t.Errorf("额外版本漂移后原键不能返回变化后的删除回执：%v", err)
	}
}

// 删除赢家提交后，已有RR快照的原键调用必须读取原凭据，而不是看到pending_delete就误报冲突。
func TestVideoG6InputDeleteReplayRRMySQL(t *testing.T) {
	f := newVideoG6I2VFixture(t)
	ctx := context.Background()
	key := videoBillingDigest("g6-delete-rr-replay")
	now := time.Now().UTC().Truncate(time.Second)
	if err := f.legacy.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&repository.VideoInputDeletionRequest{}).Count(&count).Error; err != nil {
			return err
		}
		first, replay, err := repository.NewVideoInputAssetRepository(f.legacy.db).RequestDeferredDelete(ctx, f.asset.PublicID, f.legacy.owner, f.asset.VersionNo, key, now)
		if err != nil || replay {
			return fmt.Errorf("删除赢家失败：%v", err)
		}
		second, replay, err := repository.NewVideoInputAssetRepository(tx).RequestDeferredDelete(ctx, f.asset.PublicID, f.legacy.owner, f.asset.VersionNo, key, now.Add(time.Second))
		if err != nil || !replay || second == nil || second.VersionNo != first.VersionNo {
			t.Errorf("旧RR必须返回原删除凭据：%+v replay=%v err=%v", second, replay, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
