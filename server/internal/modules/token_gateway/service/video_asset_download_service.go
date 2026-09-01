package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 短效地址仍需原Bearer认证；签名不含可见内部身份、对象位置或可匿名使用的存储能力。
type VideoAssetDownloadURL struct {
	AssetID     string    `json:"asset_id"`
	DownloadURL string    `json:"download_url"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func videoPublicDownloadAsset(a *model.AIImageAsset) bool {
	if a.MIMEType == nil {
		return false
	}
	switch *a.MIMEType {
	case "video/mp4", "image/png", "image/jpeg", "image/webp":
	default:
		return false
	}
	switch a.AssetRole {
	case "content", "cover", "preview", "thumbnail":
		return true
	case "derived":
		return a.Source == "derived"
	default:
		return false
	}
}

func (s *VideoHTTPService) videoForDownload(ctx context.Context, caller VideoCaller, assetID string) (string, error) {
	if s == nil || s.db == nil || s.access == nil || s.contentStore == nil || len(s.downloadSecret) != 32 {
		return "", ErrVideoContentUnavailable
	}
	if caller.UserID == 0 || !videoBillingPublicID.MatchString(assetID) {
		return "", repository.ErrVideoTaskNotFound
	}
	if err := revalidateVideoReadCredential(ctx, caller); err != nil {
		return "", err
	}
	var row struct{ PublicID string }
	// 此处只解析已限定归属的Task；后续原G5事务会重新锁定身份与整个资产树。
	q := videoTaskOwnerQuery(s.db.WithContext(ctx), caller).Joins("JOIN ai_gateway_assets a ON a.task_id=t.id AND a.request_id=t.request_id AND a.user_id=t.user_id AND a.project_id=t.project_id")
	err := q.Select("t.public_id").Where("a.public_id=? AND a.modality='video' AND a.asset_role IN ('content','cover','preview','thumbnail','derived') AND (a.asset_role<>'derived' OR a.source='derived')", assetID).Take(&row).Error
	if err != nil {
		return "", videoAccessReadError(err, repository.ErrVideoTaskNotFound)
	}
	return row.PublicID, nil
}

// 签名域包含HTTP方法和规范路径，绑定原主体、精确来源Key及不可变媒体版本。
func (s *VideoHTTPService) signVideoDownload(caller VideoCaller, a *model.AIImageAsset, expires int64) string {
	mac := hmac.New(sha256.New, s.downloadSecret)
	fmt.Fprintf(mac, "molin-video-download-v1\nGET\n/api/token/video-assets/%s/content\n%d\n%d\n%d\n%d\n%s\n%d\n%s\n%d", a.PublicID, a.UserID, a.ProjectID, caller.APIKeyID, a.VersionNo, *a.SHA256, *a.SizeBytes, a.AssetRole, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *VideoHTTPService) AssetDownloadURL(ctx context.Context, caller VideoCaller, assetID string) (*VideoAssetDownloadURL, error) {
	videoID, err := s.videoForDownload(ctx, caller, assetID)
	if err != nil {
		return nil, err
	}
	var asset model.AIImageAsset
	var expires time.Time
	err = s.withAssetContentTx(ctx, caller, videoID, assetID, func(ctx context.Context, tx *gorm.DB, a *model.AIImageAsset) error {
		if err := s.checkContentObject(ctx, a); err != nil {
			return err
		}
		var deadlines []struct{ ExpiresAt time.Time }
		if err := tx.Model(&model.AIImageAsset{}).Select("expires_at").Where("request_id=?", a.RequestID).Find(&deadlines).Error; err != nil {
			return ErrVideoContentUnavailable
		}
		if len(deadlines) != 6 {
			return ErrVideoContentUnavailable
		}
		expires = time.Now().UTC().Add(15 * time.Minute)
		if caller.credential != nil && caller.credential.expiresAt.Before(expires) {
			expires = caller.credential.expiresAt
		}
		for _, d := range deadlines {
			if d.ExpiresAt.Before(expires) {
				expires = d.ExpiresAt
			}
		}
		expires = expires.Truncate(time.Second)
		asset = *a
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !expires.After(time.Now().UTC()) {
		return nil, repository.ErrVideoTaskNotFound
	}
	value := strconv.FormatInt(expires.Unix(), 10)
	return &VideoAssetDownloadURL{AssetID: assetID, ExpiresAt: expires, DownloadURL: "/api/token/video-assets/" + assetID + "/content?expires=" + value + "&signature=" + s.signVideoDownload(caller, &asset, expires.Unix())}, nil
}

func (s *VideoHTTPService) GetSignedAssetContent(ctx context.Context, caller VideoCaller, assetID, expiry, signature string) (*VideoContent, error) {
	videoID, err := s.videoForDownload(ctx, caller, assetID)
	if err != nil {
		return nil, err
	}
	seconds, err := strconv.ParseInt(expiry, 10, 64)
	if err != nil || strconv.FormatInt(seconds, 10) != expiry || !lowerHex64.MatchString(signature) {
		return nil, repository.ErrVideoTaskNotFound
	}
	expires := time.Unix(seconds, 0).UTC()
	validate := func(ctx context.Context, a *model.AIImageAsset) error {
		if err := revalidateVideoReadCredential(ctx, caller); err != nil {
			return err
		}
		if !expires.After(time.Now().UTC()) || expires.After(time.Now().UTC().Add(15*time.Minute)) || !hmac.Equal([]byte(signature), []byte(s.signVideoDownload(caller, a, seconds))) {
			return repository.ErrVideoTaskNotFound
		}
		return nil
	}
	content, err := s.getAssetContent(ctx, caller, videoID, assetID, validate)
	if err != nil {
		return nil, err
	}
	renew := content.BeforeWrite
	content.BeforeWrite = func(ctx context.Context) (time.Time, error) {
		until, err := renew(ctx)
		if err != nil {
			return time.Time{}, err
		}
		if expires.Before(until) {
			until = expires
		}
		if caller.credential != nil && caller.credential.expiresAt.Before(until) {
			until = caller.credential.expiresAt
		}
		return until, nil
	}
	return content, nil
}
