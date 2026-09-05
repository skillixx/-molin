package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

var (
	ErrVideoImportUnavailable = errors.New("图片来源导入暂不可用")
	ErrVideoImportConflict    = errors.New("导入命令与已冻结来源不一致")
	ErrVideoImportInvalid     = errors.New("来源图片内容或规范化结果无效")
)

// 对象引用仅由已鉴权的数据库记录构造；读取必须限长并返回不可变副本，删除只限本次目标且封堵迟到写入。
type VideoImportObject struct{ Bucket, Key string }
type VideoInputImportStore interface {
	Read(context.Context, VideoImportObject, int64) ([]byte, error)
	Put(context.Context, VideoImportObject, []byte, string) error
	Discard(context.Context, VideoImportObject) error
}
type VideoInputImportOptions struct {
	Store                                     VideoInputImportStore
	Safety                                    *video.VideoSafetyPipeline
	NormalizedBucket, ModerationPolicyVersion string
	MaxUserReservedBytes                      uint64
}
type VideoInputImportCommand struct {
	Caller         VideoCaller
	IdempotencyKey string `json:"-"`
	SourceAssetID  string
}
type VideoInputImportReply struct {
	ImportID            string    `json:"import_id"`
	Status              string    `json:"status"`
	InputAssetID        *string   `json:"input_asset_id"`
	ProcessingExpiresAt time.Time `json:"processing_expires_at"`
	Idempotent          bool      `json:"idempotent"`
}
type videoInputImportRecord struct {
	InputAssetID, UserID, ProjectID                                             uint64
	PublicID, InputPublicID                                                     string
	APIKeyID                                                                    *uint64
	CommandKeyHash, CommandFingerprint                                          string
	SourceAssetID, SourceVersion                                                uint64
	SourcePublicID, SourceSHA256, SourceBucket, SourceObjectKey, SourceMIMEType string
	SourceSizeBytes                                                             uint64
	SourceWidth, SourceHeight                                                   uint32
	NormalizedBucket, NormalizedKey                                             string
	ReservedBytes, VersionNo                                                    uint64
	Status                                                                      string
	LeaseUntil                                                                  *time.Time
	ExpiresAt                                                                   time.Time
	CleanupPending                                                              bool
	CleanedAt                                                                   *time.Time
	LastSafeError                                                               string
	CreatedAt                                                                   time.Time
}

func (videoInputImportRecord) TableName() string { return "ai_video_input_imports" }
func (r videoInputImportRecord) target() VideoImportObject {
	return VideoImportObject{r.NormalizedBucket, r.NormalizedKey}
}
func (r videoInputImportRecord) reply(replay bool) *VideoInputImportReply {
	rp := &VideoInputImportReply{ImportID: r.PublicID, Status: r.Status, ProcessingExpiresAt: r.ExpiresAt, Idempotent: replay}
	if r.Status == "completed" {
		id := r.InputPublicID
		rp.InputAssetID = &id
	}
	return rp
}

type VideoInputImportService struct {
	db         *gorm.DB
	access     *VideoAccessService
	options    VideoInputImportOptions
	normalizer *video.ReferenceImageNormalizer
	now        func() time.Time
}

func NewVideoInputImportService(db *gorm.DB, options VideoInputImportOptions) (*VideoInputImportService, error) {
	if db == nil || options.Store == nil || options.Safety == nil || options.MaxUserReservedBytes == 0 || options.MaxUserReservedBytes > 1<<40 || !videoBillingPublicID.MatchString(options.NormalizedBucket) || !videoIntentPolicyCode.MatchString(options.ModerationPolicyVersion) {
		return nil, ErrVideoImportUnavailable
	}
	v := reflect.ValueOf(options.Store)
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return nil, ErrVideoImportUnavailable
	}
	n, err := video.NewReferenceImageNormalizer(video.ReferenceImageLimits{MaxSourceBytes: videoUploadMaxBytes, MaxNormalizedBytes: videoUploadMaxBytes, MaxPixels: 16777216, MaxWidth: 4096, MaxHeight: 4096, MinAspectRatio: 0.5, MaxAspectRatio: 2, MaxEXIFBytes: 1 << 20, MaxICCBytes: 1 << 20, MaxDecodeDuration: 30 * time.Second, MaxTempDiskBytes: 128 << 20})
	if err != nil {
		return nil, err
	}
	return &VideoInputImportService{db: db, access: NewVideoAccessService(db), options: options, normalizer: n, now: time.Now}, nil
}

// 每次资格读取都是当前读；候选不是授权凭证，来源在IO前后均重新校验。
func (s *VideoInputImportService) source(tx *gorm.DB, c VideoCaller, id string) (*model.AIImageAsset, error) {
	var source model.AIImageAsset
	if err := videoSourceImagesQuery(tx, c, s.now().UTC()).Select("a.*").Where("a.public_id=?", id).Clauses(clause.Locking{Strength: "SHARE"}).Take(&source).Error; err != nil {
		return nil, videoAccessReadError(err, repository.ErrVideoInputNotFound)
	}
	if source.SHA256 == nil || !lowerHex64.MatchString(*source.SHA256) {
		return nil, ErrVideoImportInvalid
	}
	if c.APIKeyID != 0 {
		// 外层FOR SHARE不保证EXISTS子查询也是当前读；单独锁定源模型授权直到本事务结束。
		var task struct{ LogicalModelCode string }
		if err := tx.Table("ai_gateway_tasks").Select("logical_model_code").Clauses(clause.Locking{Strength: "SHARE"}).Where("id=? AND user_id=? AND project_id=?", source.TaskID, c.UserID, c.ProjectID).Take(&task).Error; err != nil {
			return nil, videoAccessReadError(err, repository.ErrVideoInputNotFound)
		}
		var scope struct{ APIKeyID uint64 }
		if err := tx.Table("api_key_model_scopes").Select("api_key_id").Clauses(clause.Locking{Strength: "SHARE"}).Where("api_key_id=? AND user_id=? AND project_id=? AND logical_model_code=?", c.APIKeyID, c.UserID, c.ProjectID, task.LogicalModelCode).Take(&scope).Error; err != nil {
			return nil, videoAccessReadError(err, repository.ErrVideoInputNotFound)
		}
	}
	return &source, nil
}
func importSourceMatches(r videoInputImportRecord, a *model.AIImageAsset) bool {
	return a != nil && a.ID == r.SourceAssetID && a.VersionNo == r.SourceVersion && a.SHA256 != nil && *a.SHA256 == r.SourceSHA256 && a.Bucket != nil && *a.Bucket == r.SourceBucket && a.ObjectKey != nil && *a.ObjectKey == r.SourceObjectKey && a.MIMEType != nil && *a.MIMEType == r.SourceMIMEType && a.SizeBytes != nil && *a.SizeBytes == r.SourceSizeBytes && a.Width != nil && *a.Width == r.SourceWidth && a.Height != nil && *a.Height == r.SourceHeight
}
func (s *VideoInputImportService) update(tx *gorm.DB, r *videoInputImportRecord, updates map[string]any) error {
	updates["version_no"] = r.VersionNo + 1
	changed := tx.Model(&videoInputImportRecord{}).Where("input_asset_id=? AND version_no=?", r.InputAssetID, r.VersionNo).Updates(updates)
	if changed.Error != nil {
		return changed.Error
	}
	if changed.RowsAffected != 1 {
		return ErrVideoImportConflict
	}
	r.VersionNo++
	return nil
}

func (s *VideoInputImportService) Import(ctx context.Context, c VideoInputImportCommand) (*VideoInputImportReply, error) {
	if s == nil {
		return nil, ErrVideoImportUnavailable
	}
	if !videoHTTPIdempotency.MatchString(c.IdempotencyKey) || !videoBillingPublicID.MatchString(c.SourceAssetID) {
		return nil, ErrVideoImportInvalid
	}
	var r videoInputImportRecord
	claimed, replay, knownCommand := false, false, false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user struct{ ID uint64 }
		if err := tx.Table("users").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id=?", c.Caller.UserID).Take(&user).Error; err != nil {
			return videoAccessReadError(err, repository.ErrVideoInputNotFound)
		}
		owner, err := s.access.ResolveSubjectTx(ctx, tx, c.Caller, s.now().UTC())
		if err != nil {
			return videoInputSubjectError(err)
		}
		c.Caller.ProjectID = owner.ProjectID
		key := videoBillingDigest("input_import\x00" + c.IdempotencyKey)
		fingerprint := videoBillingDigest(fmt.Sprintf("1|%d|%d|%d|%s", owner.UserID, owner.ProjectID, c.Caller.APIKeyID, c.SourceAssetID))
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND project_id=? AND command_key_hash=?", owner.UserID, owner.ProjectID, key).Take(&r).Error
		if err == nil {
			if !sameVideoRightsKey(r.APIKeyID, owner.APIKeyID) {
				return repository.ErrVideoInputNotFound
			}
			if r.CommandFingerprint != fingerprint {
				return ErrVideoImportConflict
			}
			replay = true
			knownCommand = true
			if r.Status == "rejected" {
				return ErrVideoImportConflict
			}
			source, err := s.source(tx, c.Caller, c.SourceAssetID)
			if err != nil {
				return err
			}
			if !importSourceMatches(r, source) {
				return ErrVideoImportConflict
			}
			if r.Status == "completed" {
				return nil
			}
			now := s.now().UTC()
			if !r.ExpiresAt.After(now) {
				return ErrVideoImportConflict
			}
			if r.LeaseUntil != nil && r.LeaseUntil.After(now) {
				return nil
			}
			until := now.Add(2 * time.Minute)
			if err := s.update(tx, &r, map[string]any{"lease_until": until}); err != nil {
				return err
			}
			r.LeaseUntil = &until
			claimed = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		source, err := s.source(tx, c.Caller, c.SourceAssetID)
		if err != nil {
			return err
		}
		now := s.now().UTC().Truncate(time.Second)
		if err := checkVideoInputCapacity(tx, owner, now, uint64(videoUploadMaxBytes), s.options.MaxUserReservedBytes); err != nil {
			return err
		}
		inputID, err := newVideoHTTPID("vin_")
		if err != nil {
			return err
		}
		importID, err := newVideoHTTPID("vim_")
		if err != nil {
			return err
		}
		asset := model.AIGatewayInputAsset{PublicID: inputID, UserID: owner.UserID, ProjectID: owner.ProjectID, SourceType: "gateway_asset_snapshot", SourceGatewayAssetID: &source.ID, OriginalSHA256: *source.SHA256, ModerationStatus: model.AIModerationPending, VersionNo: 1, LifecycleState: model.AIInputAssetNormalizing, ExpiresAt: now.Add(currentVideoRetentionPolicy.InputBound), CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&asset).Error; err != nil {
			return err
		}
		until := now.Add(2 * time.Minute)
		r = videoInputImportRecord{InputAssetID: asset.ID, InputPublicID: inputID, PublicID: importID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, CommandKeyHash: key, CommandFingerprint: fingerprint, SourceAssetID: source.ID, SourcePublicID: source.PublicID, SourceVersion: source.VersionNo, SourceSHA256: *source.SHA256, SourceBucket: *source.Bucket, SourceObjectKey: *source.ObjectKey, SourceMIMEType: *source.MIMEType, SourceSizeBytes: *source.SizeBytes, SourceWidth: *source.Width, SourceHeight: *source.Height, NormalizedBucket: s.options.NormalizedBucket, NormalizedKey: fmt.Sprintf("import/%d/%d/%s.png", owner.UserID, owner.ProjectID, inputID), ReservedBytes: uint64(videoUploadMaxBytes), Status: "processing", VersionNo: 1, LeaseUntil: &until, ExpiresAt: now.Add(currentVideoRetentionPolicy.ImportProcessing), CreatedAt: now}
		if err := tx.Create(&r).Error; err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err != nil {
		if knownCommand && r.Status == "processing" {
			return nil, s.fail(ctx, r, err)
		}
		if knownCommand && r.Status == "rejected" && r.CleanupPending {
			if cleanupErr := s.cleanup(ctx, r); cleanupErr != nil {
				return nil, ErrVideoImportUnavailable
			}
		}
		if importRetryable(err) {
			return nil, ErrVideoImportUnavailable
		}
		return nil, err
	}
	if !claimed {
		return r.reply(replay), nil
	}
	work, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := s.options.Store.Read(work, VideoImportObject{r.SourceBucket, r.SourceObjectKey}, videoUploadMaxBytes)
	if err != nil {
		if errors.Is(err, ErrVideoImportInvalid) {
			return nil, s.fail(ctx, r, ErrVideoImportInvalid)
		}
		return nil, s.fail(ctx, r, ErrVideoImportUnavailable)
	}
	if len(raw) == 0 || int64(len(raw)) > videoUploadMaxBytes || uint64(len(raw)) != r.SourceSizeBytes || videoPayloadSHA256(raw) != r.SourceSHA256 {
		return nil, s.fail(ctx, r, ErrVideoImportInvalid)
	}
	ext := ".png"
	if r.SourceMIMEType == "image/jpeg" {
		ext = ".jpg"
	}
	image, err := s.normalizer.Normalize(work, video.ReferenceImageInput{Filename: "reference" + ext, DeclaredMIME: r.SourceMIMEType, Body: bytes.NewReader(raw)})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, video.ErrReferenceImageBusy) {
			return nil, s.fail(ctx, r, ErrVideoImportUnavailable)
		}
		return nil, s.fail(ctx, r, ErrVideoImportInvalid)
	}
	if image.Width < 640 || image.Height < 640 || image.Width != int(r.SourceWidth) || image.Height != int(r.SourceHeight) || image.OriginalSHA256 != r.SourceSHA256 {
		return nil, s.fail(ctx, r, ErrVideoImportInvalid)
	}
	if err := s.options.Safety.AssessReference(work, image); err != nil {
		return nil, s.fail(ctx, r, err)
	}
	if err := s.options.Store.Put(work, r.target(), image.Bytes, image.NormalizedSHA256); err != nil {
		if errors.Is(err, ErrVideoImportConflict) || errors.Is(err, ErrVideoImportInvalid) {
			return nil, s.fail(ctx, r, err)
		}
		return nil, s.fail(ctx, r, ErrVideoImportUnavailable)
	}
	if err := s.publish(work, c.Caller, r, image); err != nil {
		return nil, s.fail(ctx, r, err)
	}
	r.Status = "completed"
	return r.reply(replay), nil
}

func importRetryable(err error) bool {
	var dbError *mysqlDriver.MySQLError
	return errors.Is(err, ErrVideoImportUnavailable) || errors.Is(err, ErrVideoAccessUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, video.ErrVideoModerationFailed) || (errors.As(err, &dbError) && (dbError.Number == 1213 || dbError.Number == 1205))
}
