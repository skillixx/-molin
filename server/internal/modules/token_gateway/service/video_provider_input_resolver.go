package service

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	video "molin/server/internal/modules/token_gateway/video"
)

// NewVideoProviderInputResolver只按已冻结InputAsset身份读取规范化副本；不读取原multipart或外部URL。
func NewVideoProviderInputResolver(db *gorm.DB, store VideoUploadStore) video.NativeInputResolver {
	return func(ctx context.Context, input video.ControlledInputRef) (video.NativeInputPayload, error) {
		if db == nil || store == nil || !videoBillingPublicID.MatchString(input.AssetID) || !lowerHex64.MatchString(input.SHA256) || input.Version == 0 {
			return video.NativeInputPayload{}, ErrVideoUploadUnavailable
		}
		var asset model.AIGatewayInputAsset
		err := db.WithContext(ctx).Where("public_id=? AND normalized_sha256=? AND version_no=? AND bucket='ai-result' AND object_key IS NOT NULL AND mime_type='image/png' AND size_bytes IS NOT NULL AND lifecycle_state IN ('ready','pending_delete')", input.AssetID, input.SHA256, input.Version).Take(&asset).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return video.NativeInputPayload{}, ErrVideoUploadConflict
		}
		if err != nil || asset.Bucket == nil || asset.ObjectKey == nil || asset.SizeBytes == nil || *asset.SizeBytes == 0 || *asset.SizeBytes > uint64(videoUploadMaxBytes) {
			return video.NativeInputPayload{}, ErrVideoUploadUnavailable
		}
		raw, err := store.ReadNormalized(ctx, *asset.Bucket, *asset.ObjectKey, videoUploadMaxBytes)
		if err != nil || len(raw) == 0 || uint64(len(raw)) != *asset.SizeBytes || videoPayloadSHA256(raw) != input.SHA256 {
			return video.NativeInputPayload{}, ErrVideoUploadConflict
		}
		return video.NativeInputPayload{Bytes: raw, MIMEType: "image/png"}, nil
	}
}
