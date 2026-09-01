package service

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

var ErrVideoContentUnavailable = errors.New("视频私有内容暂不可用")

// 读取边界只提供私有不可变对象，不包含上传、删除、签名URL或Provider能力。
// 实现必须按Ref读取同一不可变对象，Head的hash/大小必须对应GetRange的实际字节。
type VideoContentStore interface {
	Head(context.Context, video.VideoObjectRef) (video.StoredVideoObject, error)
	GetRange(context.Context, video.VideoObjectRef, int64, int64) (io.ReadCloser, error)
}

// 仅向传输层提供有界读取能力；内部位置与快照不进入JSON。
type VideoContent struct {
	MIMEType    string                                                     `json:"-"`
	Size        int64                                                      `json:"-"`
	SHA256      string                                                     `json:"-"`
	OpenRange   func(context.Context, int64, int64) (io.ReadCloser, error) `json:"-"`
	BeforeWrite func(context.Context) (time.Time, error)                   `json:"-"`
	Close       func() error                                               `json:"-"`
}

// GetContent不能以公开completed替代鉴权；能力绑定原资产版本，分片会再次读取真实账本。
func (s *VideoHTTPService) GetContent(ctx context.Context, caller VideoCaller, id string) (*VideoContent, error) {
	return s.getAssetContent(ctx, caller, id, "", nil)
}

// 两个门面共用真实鉴权、财务、资产快照和下载租约，平台不能另建读取旁路。
func (s *VideoHTTPService) getAssetContent(ctx context.Context, caller VideoCaller, id, assetID string, validate func(context.Context, *model.AIImageAsset) error) (*VideoContent, error) {
	if s == nil || s.db == nil || s.access == nil || s.contentStore == nil {
		return nil, ErrVideoContentUnavailable
	}
	if !videoHTTPPublicID.MatchString(id) || caller.UserID == 0 {
		return nil, repository.ErrVideoTaskNotFound
	}
	readTx := func(ctx context.Context, read func(context.Context, *gorm.DB, *model.AIImageAsset) error) error {
		return s.withAssetContentTx(ctx, caller, id, assetID, read)
	}
	return s.videoContentCapability(ctx, s.contentStore, readTx, validate)
}

// 临时与长期对象共享分片、租约和快照防替换实现，只由各自权威事务选择对象及存储。
func (s *VideoHTTPService) videoContentCapability(ctx context.Context, store VideoContentStore, readTx func(context.Context, func(context.Context, *gorm.DB, *model.AIImageAsset) error) error, validate func(context.Context, *model.AIImageAsset) error) (*VideoContent, error) {
	var pinned model.AIImageAsset
	var lease *videoDownloadLease
	err := readTx(ctx, func(ctx context.Context, tx *gorm.DB, asset *model.AIImageAsset) error {
		if validate != nil {
			if err := validate(ctx, asset); err != nil {
				return err
			}
		}
		var err error
		lease, err = acquireVideoDownloadTx(tx, asset, s.downloadLimits)
		if err != nil {
			return err
		}
		if err := checkVideoContentObject(ctx, store, asset); err != nil {
			return err
		}
		pinned = *asset
		return nil
	})
	if err != nil {
		// COMMIT可能已成功但确认丢失；只允许用本次随机lease_id精确恢复原租约，普通回滚仍失败关闭。
		if lease == nil {
			return nil, err
		}
		lease.db = s.db
		if recoverErr := lease.recoverCommitted(ctx); recoverErr != nil {
			return nil, err
		}
	}
	size := int64(*pinned.SizeBytes)
	lease.db = s.db
	result := &VideoContent{MIMEType: *pinned.MIMEType, Size: size, SHA256: *pinned.SHA256, BeforeWrite: lease.renew, Close: lease.close}
	if validate != nil {
		if err := validate(ctx, &pinned); err != nil {
			_ = lease.close()
			return nil, err
		}
		result.BeforeWrite = func(ctx context.Context) (time.Time, error) {
			until, err := lease.renew(ctx)
			if err != nil {
				return time.Time{}, err
			}
			if err := validate(ctx, &pinned); err != nil {
				return time.Time{}, err
			}
			return until, nil
		}
	}
	result.OpenRange = func(ctx context.Context, offset, length int64) (io.ReadCloser, error) {
		// 每片最多1MiB，事务只覆盖这一片的受控读取，不持有钱包锁等待慢客户端。
		if offset < 0 || length <= 0 || length > 1<<20 || offset >= size || length > size-offset {
			return nil, ErrVideoContentUnavailable
		}
		if _, err := lease.renew(ctx); err != nil {
			return nil, err
		}
		var body []byte
		err := readTx(ctx, func(ctx context.Context, tx *gorm.DB, asset *model.AIImageAsset) error {
			if validate != nil {
				if err := validate(ctx, asset); err != nil {
					return err
				}
			}
			if asset.ID != pinned.ID || asset.VersionNo != pinned.VersionNo || *asset.SHA256 != *pinned.SHA256 || *asset.SizeBytes != *pinned.SizeBytes || *asset.Bucket != *pinned.Bucket || *asset.ObjectKey != *pinned.ObjectKey {
				return repository.ErrVideoTaskNotFound
			}
			if err := checkVideoContentObject(ctx, store, asset); err != nil {
				return err
			}
			r, err := store.GetRange(ctx, video.VideoObjectRef{Bucket: *asset.Bucket, ObjectKey: *asset.ObjectKey}, offset, length)
			if err != nil || r == nil {
				if r != nil {
					_ = r.Close()
				}
				return ErrVideoContentUnavailable
			}
			data, readErr := io.ReadAll(io.LimitReader(r, length+1))
			closeErr := r.Close()
			if readErr != nil || closeErr != nil || int64(len(data)) != length || ctx.Err() != nil {
				return ErrVideoContentUnavailable
			}
			body = data
			return nil
		})
		if err != nil {
			return nil, err
		}
		if validate != nil {
			if err := validate(ctx, &pinned); err != nil {
				return nil, err
			}
		}
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	return result, nil
}

// 使用原G5 Task→Request→财务/六资产锁序；复验与对象读取在同一事务中，删除只能先于或后于本片。
func (s *VideoHTTPService) withContentTx(ctx context.Context, caller VideoCaller, id string, read func(context.Context, *gorm.DB, *model.AIImageAsset) error) error {
	return s.withAssetContentTx(ctx, caller, id, "", read)
}

func (s *VideoHTTPService) withAssetContentTx(ctx context.Context, caller VideoCaller, id, assetID string, read func(context.Context, *gorm.DB, *model.AIImageAsset) error) error {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.db.WithContext(bounded).Transaction(func(tx *gorm.DB) error {
		if assetID != "" {
			if err := revalidateVideoReadCredential(bounded, caller); err != nil {
				return err
			}
		}
		task, owner, err := s.taskForPlatformTx(bounded, tx, caller, id, false)
		if err != nil {
			return err
		}
		if task.Operation == nil || task.Status != model.AIImageTaskSucceeded || task.BillingStatus != model.AIBillingSettled || task.DeliveryStatus != model.AIDeliveryAvailable {
			return repository.ErrVideoTaskNotFound
		}
		var deleting int64
		if err := tx.Table("ai_video_media_deletions").Where("task_id=?", task.ID).Count(&deleting).Error; err != nil {
			return ErrVideoContentUnavailable
		}
		if deleting != 0 {
			return repository.ErrVideoTaskNotFound
		}
		if err := tx.Table("ai_video_asset_deletions").Where("task_id=?", task.ID).Count(&deleting).Error; err != nil {
			return ErrVideoContentUnavailable
		}
		if deleting != 0 {
			return repository.ErrVideoTaskNotFound
		}
		if err := s.access.AuthorizeTx(bounded, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
			return err
		}
		check := func() error {
			report, err := NewVideoReconciliationService(tx).Reconcile(bounded, id, owner)
			if err != nil {
				return err
			}
			if !report.Passed {
				return repository.ErrVideoTaskNotFound
			}
			return nil
		}
		if err := check(); err != nil {
			return err
		}
		asset, err := loadVideoSettlementMediaTx(tx, task, false, time.Now().UTC())
		if err != nil {
			if errors.Is(err, ErrVideoBillingState) {
				return repository.ErrVideoTaskNotFound
			}
			return ErrVideoContentUnavailable
		}
		if assetID != "" {
			var selected model.AIImageAsset
			if err := tx.Where("public_id=? AND task_id=? AND request_id=? AND user_id=? AND project_id=? AND modality='video'", assetID, task.ID, task.RequestID, owner.UserID, owner.ProjectID).Take(&selected).Error; err != nil {
				return videoAccessReadError(err, repository.ErrVideoTaskNotFound)
			}
			if !videoPublicDownloadAsset(&selected) {
				return repository.ErrVideoTaskNotFound
			}
			asset = &selected
		}
		if asset.MIMEType == nil || asset.SizeBytes == nil || *asset.SizeBytes == 0 || *asset.SizeBytes > 256<<20 || (assetID == "" && *asset.MIMEType != "video/mp4") {
			return repository.ErrVideoTaskNotFound
		}
		if err := read(bounded, tx, asset); err != nil {
			return err
		}
		// 存储等待可能跨越凭据、权益或媒体期限；只返回复验后仍可交付的已缓冲片段。
		if err := s.access.AuthorizeTx(bounded, tx, owner, task.LogicalModelCode, time.Now().UTC(), *task.Operation); err != nil {
			return err
		}
		if err := check(); err != nil {
			return err
		}
		if assetID != "" {
			return revalidateVideoReadCredential(bounded, caller)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
}

func (s *VideoHTTPService) checkContentObject(ctx context.Context, asset *model.AIImageAsset) error {
	return checkVideoContentObject(ctx, s.contentStore, asset)
}
func checkVideoContentObject(ctx context.Context, store VideoContentStore, asset *model.AIImageAsset) error {
	ref := video.VideoObjectRef{Bucket: *asset.Bucket, ObjectKey: *asset.ObjectKey}
	meta, err := store.Head(ctx, ref)
	if err != nil || meta.Ref != ref || meta.SHA256 != *asset.SHA256 || meta.SizeBytes != *asset.SizeBytes {
		return ErrVideoContentUnavailable
	}
	return nil
}
