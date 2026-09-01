package repository

import (
	"context"
	"errors"
	"math"
	"regexp"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
)

// 删除凭据冻结原版本与唯一删除后版本；不改写TaskInput，也不承诺媒体已经删除。
type VideoInputDeletionRequest struct {
	InputAssetID            uint64    `json:"-"`
	UserID                  uint64    `json:"-"`
	ProjectID               uint64    `json:"-"`
	APIKeyID                *uint64   `json:"-"`
	CommandKeyHash          string    `json:"-"`
	OriginalVersion         uint64    `json:"-"`
	DeletionVersion         uint64    `json:"-"`
	NormalizedSHA256        string    `json:"-"`
	ModerationPolicyVersion string    `json:"-"`
	InputExpiresAt          time.Time `json:"-"`
	RequestedAt             time.Time `json:"-"`
}

func (VideoInputDeletionRequest) TableName() string { return "ai_video_input_deletion_requests" }

var videoDeletionHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

// RequestDeferredDelete 原子保存命令凭据并进入pending_delete，实际清理必须另外取得安全终态与留存证明。
func (r *VideoInputAssetRepository) RequestDeferredDelete(ctx context.Context, publicID string, owner VideoOwner, expectedVersion uint64, commandKeyHash string, now time.Time) (*model.AIGatewayInputAsset, bool, error) {
	if r == nil || r.db == nil || !validVideoOwner(owner) || expectedVersion == 0 || expectedVersion == math.MaxUint64 || !videoDeletionHash.MatchString(commandKeyHash) || now.IsZero() {
		return nil, false, ErrVideoInputConflict
	}
	now = now.UTC().Truncate(time.Second)
	var result *model.AIGatewayInputAsset
	var replay bool
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		asset, err := findVideoInputForOwner(tx, publicID, owner, true)
		if err != nil {
			return err
		}
		var old VideoInputDeletionRequest
		err = tx.Clauses(clause.Locking{Strength: "SHARE"}).Where("input_asset_id=?", asset.ID).Take(&old).Error
		if err == nil {
			if old.UserID != owner.UserID || old.ProjectID != owner.ProjectID || !sameVideoDeletionKey(old.APIKeyID, owner.APIKeyID) {
				return ErrVideoInputNotFound
			}
			if old.CommandKeyHash != commandKeyHash || old.OriginalVersion != expectedVersion {
				return ErrVideoInputConflict
			}
			if asset.LifecycleState == model.AIInputAssetPendingDelete && !VideoPendingDeletionMatches(asset, old) {
				return ErrVideoInputConflict
			}
			result, replay = asset, true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if asset.VersionNo != expectedVersion || asset.LifecycleState != model.AIInputAssetReady || asset.ModerationStatus != model.AIModerationPassed || asset.LegalHold || asset.NormalizedSHA256 == nil || asset.ModerationPolicyVersion == nil || !asset.ExpiresAt.After(now) || asset.DeleteRequestedAt != nil || asset.PendingDeleteAt != nil || asset.DeletedAt != nil {
			return ErrVideoInputConflict
		}
		request := VideoInputDeletionRequest{InputAssetID: asset.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, CommandKeyHash: commandKeyHash, OriginalVersion: expectedVersion, DeletionVersion: expectedVersion + 1, NormalizedSHA256: *asset.NormalizedSHA256, ModerationPolicyVersion: *asset.ModerationPolicyVersion, InputExpiresAt: asset.ExpiresAt, RequestedAt: now}
		if err := tx.Create(&request).Error; err != nil {
			return err
		}
		changed := tx.Model(&model.AIGatewayInputAsset{}).Where("id=? AND version_no=? AND lifecycle_state='ready'", asset.ID, expectedVersion).Updates(map[string]any{"lifecycle_state": model.AIInputAssetPendingDelete, "delete_requested_at": now, "pending_delete_at": now, "version_no": expectedVersion + 1, "updated_at": now})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoInputConflict
		}
		result, err = findVideoInputForOwner(tx, publicID, owner, false)
		return err
	})
	return result, replay, err
}

func sameVideoDeletionKey(a, b *uint64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// 尚未进入实际清理时，原申请只允许唯一的删除版本；额外漂移不能被原键重放洗成新回执。
func VideoPendingDeletionMatches(asset *model.AIGatewayInputAsset, d VideoInputDeletionRequest) bool {
	return asset != nil && asset.ID == d.InputAssetID && asset.UserID == d.UserID && asset.ProjectID == d.ProjectID &&
		asset.LifecycleState == model.AIInputAssetPendingDelete && asset.VersionNo == d.DeletionVersion && d.DeletionVersion == d.OriginalVersion+1 &&
		!asset.LegalHold && asset.ModerationStatus == model.AIModerationPassed && asset.DeletedAt == nil &&
		asset.NormalizedSHA256 != nil && *asset.NormalizedSHA256 == d.NormalizedSHA256 && asset.ModerationPolicyVersion != nil && *asset.ModerationPolicyVersion == d.ModerationPolicyVersion &&
		asset.ExpiresAt.Equal(d.InputExpiresAt) && asset.DeleteRequestedAt != nil && asset.PendingDeleteAt != nil &&
		asset.DeleteRequestedAt.Equal(d.RequestedAt) && asset.PendingDeleteAt.Equal(d.RequestedAt)
}

// 仅已有且未释放的绑定可使用删除凭据；额外版本漂移、审核变化、到期或保全继续拒绝。
func videoBoundInputSnapshotValid(db *gorm.DB, asset *model.AIGatewayInputAsset, binding *model.AIGatewayTaskInput, owner VideoOwner, now time.Time) (bool, error) {
	if asset == nil || binding == nil || binding.LeaseReleasedAt != nil || binding.InputAssetID != asset.ID || binding.UserID != owner.UserID || binding.ProjectID != owner.ProjectID || asset.LegalHold || asset.ModerationStatus != model.AIModerationPassed || asset.NormalizedSHA256 == nil || *asset.NormalizedSHA256 != binding.NormalizedSHA256 || !asset.ExpiresAt.After(now) || asset.DeletedAt != nil {
		return false, nil
	}
	if asset.LifecycleState == model.AIInputAssetReady {
		return asset.VersionNo == binding.InputVersion && asset.DeleteRequestedAt == nil && asset.PendingDeleteAt == nil, nil
	}
	if asset.LifecycleState != model.AIInputAssetPendingDelete || asset.DeleteRequestedAt == nil || asset.PendingDeleteAt == nil || asset.ModerationPolicyVersion == nil {
		return false, nil
	}
	var d VideoInputDeletionRequest
	if err := db.Clauses(clause.Locking{Strength: "SHARE"}).Where("input_asset_id=? AND user_id=? AND project_id=?", asset.ID, owner.UserID, owner.ProjectID).Take(&d).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return sameVideoDeletionKey(d.APIKeyID, owner.APIKeyID) && d.OriginalVersion == binding.InputVersion && d.NormalizedSHA256 == binding.NormalizedSHA256 && VideoPendingDeletionMatches(asset, d), nil
}
