package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"reflect"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

var (
	ErrVideoUploadUnavailable = errors.New("受控上传服务暂不可用")
	ErrVideoUploadConflict    = errors.New("上传会话状态或幂等命令冲突")
	ErrVideoUploadInvalid     = errors.New("上传输入参数或内容无效")
	ErrVideoUploadCapacity    = errors.New("输入存储容量不足")
	ErrVideoUploadConcurrency = errors.New("同时上传数量超过限制")
)

const videoUploadMaxBytes int64 = 10 << 20

// Target只由服务器从归属记录构造，不接受客户端提供的bucket、object_key或任意URL。
type VideoUploadTarget struct {
	SessionID, InputAssetID                                                                        string
	UserID, ProjectID                                                                              uint64
	SourceType, SourceBucket, SourceKey, NormalizedBucket, NormalizedKey, MIMEType, ExpectedSHA256 string
	SizeBytes                                                                                      uint64
	UploadExpiresAt                                                                                time.Time
}
type VideoUploadGrant struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}
type VideoSealedUpload struct {
	Bytes                     []byte `json:"-"`
	MIMEType, ETag, VersionID string
}

// Store必须封存同一版本或复制到不可变位置；Discard需封堵迟到写入，不能仅删除后允许复活。
// 普通S3删除不能自动满足这个合同；缺少具备围栏能力的适配器时入口必须关闭，不能降级Fake。
type VideoUploadStore interface {
	Issue(context.Context, VideoUploadTarget) (*VideoUploadGrant, error)
	Seal(context.Context, VideoUploadTarget, int64) (*VideoSealedUpload, error)
	PutNormalized(context.Context, VideoUploadTarget, []byte, string) error
	ReadNormalized(context.Context, string, string, int64) ([]byte, error)
	Discard(context.Context, VideoUploadTarget) error
}

// VideoInlineUploadStore是OpenAI multipart的显式服务端写入能力。
// PutOriginal必须执行条件不可变写：同一Target、size、SHA256和正文重复写视为成功，不得创建新对象身份或版本；
// 任一正文/长度/hash不一致必须返回ErrVideoUploadConflict。即使Seal已成功但回包丢失，同内容重放仍须安全成功。
// 未装配该能力时inline入口必须503，不能让Handler拼接对象位置或向预签URL自调用。
type VideoInlineUploadStore interface {
	PutOriginal(context.Context, VideoUploadTarget, io.Reader, uint64, string) error
}

type VideoUploadOptions struct {
	Store                                                   VideoUploadStore
	Safety                                                  *video.VideoSafetyPipeline
	SourceBucket, NormalizedBucket, ModerationPolicyVersion string
	MaxUserReservedBytes                                    uint64
}
type VideoUploadCreateCommand struct {
	Caller             VideoCaller
	IdempotencyKey     string `json:"-"`
	Filename, MIMEType string
	SizeBytes          uint64
	SHA256             string
	SourceType         string `json:"-"`
}
type VideoUploadReply struct {
	SessionID      string            `json:"session_id"`
	Status         string            `json:"status"`
	ExpiresAt      time.Time         `json:"expires_at"`
	VersionNo      uint64            `json:"version_no"`
	InputAssetID   *string           `json:"input_asset_id"`
	Upload         *VideoUploadGrant `json:"upload"`
	CleanupPending bool              `json:"cleanup_pending"`
	Idempotent     bool              `json:"idempotent"`
}

type videoUploadControl struct {
	SessionID                                                     uint64 `gorm:"primaryKey"`
	UserID, ProjectID                                             uint64
	CreateKeyHash, CreateFingerprint                              string
	ExpectedSHA256                                                string `gorm:"column:expected_sha256"`
	FileExtension, InputPublicID, NormalizedBucket, NormalizedKey string
	UploadExpiresAt                                               time.Time
	ReservedBytes, VersionNo                                      uint64
	CompleteKeyHash, CancelKeyHash                                *string
	LeaseUntil                                                    *time.Time
	CleanupPending                                                bool
	CleanedAt                                                     *time.Time
	LastSafeError                                                 string
	CreatedAt                                                     time.Time
}

func (videoUploadControl) TableName() string { return "ai_video_upload_controls" }

type videoUploadRecord struct {
	session model.AIUploadSession
	control videoUploadControl
}

func (r videoUploadRecord) target() VideoUploadTarget {
	return VideoUploadTarget{SessionID: r.session.PublicID, InputAssetID: r.control.InputPublicID, UserID: r.session.UserID, ProjectID: r.session.ProjectID, SourceType: r.session.SourceType, SourceBucket: r.session.Bucket, SourceKey: r.session.ObjectKey, NormalizedBucket: r.control.NormalizedBucket, NormalizedKey: r.control.NormalizedKey, MIMEType: r.session.MIMEType, ExpectedSHA256: r.control.ExpectedSHA256, SizeBytes: r.session.SizeBytes, UploadExpiresAt: r.control.UploadExpiresAt}
}
func (r videoUploadRecord) reply(now time.Time, replay bool) *VideoUploadReply {
	p := &VideoUploadReply{SessionID: r.session.PublicID, Status: r.session.Status, ExpiresAt: r.session.ExpiresAt, VersionNo: r.control.VersionNo, CleanupPending: r.control.CleanupPending, Idempotent: replay}
	if r.session.Status == "completed" {
		id := r.control.InputPublicID
		p.InputAssetID = &id
	} else if !r.session.ExpiresAt.After(now) && videoUploadActive(r.session.Status) {
		p.Status = "expired"
	}
	return p
}
func videoUploadActive(status string) bool {
	return status == "created" || status == "uploading" || status == "verifying"
}

type VideoUploadService struct {
	db         *gorm.DB
	access     *VideoAccessService
	options    VideoUploadOptions
	normalizer *video.ReferenceImageNormalizer
	now        func() time.Time
}

func NewVideoUploadService(db *gorm.DB, options VideoUploadOptions) (*VideoUploadService, error) {
	if db == nil || options.Store == nil || options.Safety == nil || options.MaxUserReservedBytes == 0 || options.MaxUserReservedBytes > 1<<40 || !videoBillingPublicID.MatchString(options.SourceBucket) || !videoBillingPublicID.MatchString(options.NormalizedBucket) || options.SourceBucket == options.NormalizedBucket || !videoIntentPolicyCode.MatchString(options.ModerationPolicyVersion) {
		return nil, ErrVideoUploadUnavailable
	}
	v := reflect.ValueOf(options.Store)
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return nil, ErrVideoUploadUnavailable
	}
	n, err := video.NewReferenceImageNormalizer(video.ReferenceImageLimits{MaxSourceBytes: videoUploadMaxBytes, MaxNormalizedBytes: videoUploadMaxBytes, MaxPixels: 16777216, MaxWidth: 4096, MaxHeight: 4096, MinAspectRatio: 0.5, MaxAspectRatio: 2, MaxEXIFBytes: 1 << 20, MaxICCBytes: 1 << 20, MaxDecodeDuration: 30 * time.Second, MaxTempDiskBytes: 128 << 20})
	if err != nil {
		return nil, err
	}
	return &VideoUploadService{db: db, access: NewVideoAccessService(db), options: options, normalizer: n, now: time.Now}, nil
}

// load在同一个当前读中锁定会话与控制行，避免等待锁后读取旧RR状态；旧会话不会自动补控制记录。
func (s *VideoUploadService) load(tx *gorm.DB, owner repository.VideoOwner, id string, locked bool) (videoUploadRecord, error) {
	var r videoUploadRecord
	q := tx.Table("ai_video_upload_controls AS c").Select("c.*").Joins("JOIN ai_upload_sessions u ON u.id=c.session_id AND u.user_id=c.user_id AND u.project_id=c.project_id").Where("u.public_id=? AND u.user_id=? AND u.project_id=?", id, owner.UserID, owner.ProjectID)
	if owner.APIKeyID == nil {
		q = q.Where("u.api_key_id IS NULL")
	} else {
		q = q.Where("u.api_key_id=?", *owner.APIKeyID)
	}
	if locked {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.Take(&r.control).Error; err != nil {
		return r, videoAccessReadError(err, repository.ErrVideoUploadNotFound)
	}
	parent := tx.Where("id=?", r.control.SessionID)
	if locked {
		parent = parent.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := parent.Take(&r.session).Error; err != nil {
		return r, ErrVideoUploadUnavailable
	}
	return r, nil
}

func (s *VideoUploadService) Get(ctx context.Context, caller VideoCaller, id string) (*VideoUploadReply, error) {
	if s == nil {
		return nil, ErrVideoUploadUnavailable
	}
	var result *VideoUploadReply
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner, err := s.ownerForSession(ctx, tx, caller, id)
		if err != nil {
			return err
		}
		r, err := s.load(tx, owner, id, false)
		if err != nil {
			return err
		}
		result = r.reply(s.now().UTC(), false)
		return nil
	})
	return result, err
}

func (s *VideoUploadService) Create(ctx context.Context, c VideoUploadCreateCommand) (*VideoUploadReply, error) {
	if s == nil {
		return nil, ErrVideoUploadUnavailable
	}
	ext := strings.ToLower(path.Ext(c.Filename))
	validExt := (c.MIMEType == "image/png" && ext == ".png") || (c.MIMEType == "image/jpeg" && (ext == ".jpg" || ext == ".jpeg"))
	if !videoHTTPIdempotency.MatchString(c.IdempotencyKey) || !validExt || len(c.Filename) > 255 || strings.ContainsAny(c.Filename, "/\\\x00\r\n") || c.SizeBytes == 0 || c.SizeBytes > uint64(videoUploadMaxBytes) || !lowerHex64.MatchString(c.SHA256) {
		return nil, ErrVideoUploadInvalid
	}
	if c.MIMEType == "image/jpeg" {
		ext = ".jpg"
	}
	sourceType := c.SourceType
	if sourceType == "" {
		sourceType = model.AIUploadSourcePlatformPresigned
	}
	if sourceType != model.AIUploadSourcePlatformPresigned && sourceType != model.AIUploadSourceOpenAIInlineMultipart {
		return nil, ErrVideoUploadInvalid
	}
	keyHash := videoBillingDigest("upload_create\x00" + c.IdempotencyKey)
	var r videoUploadRecord
	existing := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 用户行先锁，跨Project的同用户创建也不能越过并发或存储预留限额。
		var user struct{ ID uint64 }
		if err := tx.Table("users").Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id=?", c.Caller.UserID).Take(&user).Error; err != nil {
			return videoAccessReadError(err, ErrVideoBillingAccess)
		}
		owner, err := s.access.ResolveSubjectTx(ctx, tx, c.Caller, s.now().UTC())
		if err != nil {
			return err
		}
		keyID := uint64(0)
		if owner.APIKeyID != nil {
			keyID = *owner.APIKeyID
		}
		fingerprintInput := []any{1, owner.UserID, owner.ProjectID, keyID, c.MIMEType, c.SizeBytes, c.SHA256, ext}
		if sourceType == model.AIUploadSourceOpenAIInlineMultipart {
			fingerprintInput = []any{2, owner.UserID, owner.ProjectID, keyID, sourceType, c.MIMEType, c.SizeBytes, c.SHA256, ext}
		}
		raw, _ := json.Marshal(fingerprintInput)
		fingerprint := videoBillingDigest(string(raw))
		err = tx.Where("user_id=? AND project_id=? AND create_key_hash=?", owner.UserID, owner.ProjectID, keyHash).Take(&r.control).Error
		if err == nil {
			if err := tx.Where("id=?", r.control.SessionID).Take(&r.session).Error; err != nil {
				return err
			}
			if !sameVideoRightsKey(r.session.APIKeyID, owner.APIKeyID) {
				return repository.ErrVideoUploadNotFound
			}
			if r.control.CreateFingerprint != fingerprint {
				return ErrVideoUploadConflict
			}
			existing = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := s.now().UTC().Truncate(time.Second)
		reserve := c.SizeBytes + uint64(videoUploadMaxBytes)
		if err := checkVideoInputCapacity(tx, owner, now, reserve, s.options.MaxUserReservedBytes); err != nil {
			return err
		}
		sid, err := newVideoHTTPID("vup_")
		if err != nil {
			return err
		}
		aid, err := newVideoHTTPID("vin_")
		if err != nil {
			return err
		}
		prefix := "original"
		if sourceType == model.AIUploadSourceOpenAIInlineMultipart {
			prefix = "inline"
		}
		r.session = model.AIUploadSession{PublicID: sid, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, Purpose: model.AIUploadPurposeVideoReferenceImage, SourceType: sourceType, MIMEType: c.MIMEType, SizeBytes: c.SizeBytes, Bucket: s.options.SourceBucket, ObjectKey: fmt.Sprintf("%s/%d/%d/%s", prefix, owner.UserID, owner.ProjectID, sid), Status: "created", ExpiresAt: now.Add(currentVideoRetentionPolicy.UploadSession), CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&r.session).Error; err != nil {
			return err
		}
		r.control = videoUploadControl{SessionID: r.session.ID, UserID: owner.UserID, ProjectID: owner.ProjectID, CreateKeyHash: keyHash, CreateFingerprint: fingerprint, ExpectedSHA256: c.SHA256, FileExtension: ext, InputPublicID: aid, NormalizedBucket: s.options.NormalizedBucket, NormalizedKey: fmt.Sprintf("normalized/%d/%d/%s.png", owner.UserID, owner.ProjectID, aid), UploadExpiresAt: now.Add(15 * time.Minute), ReservedBytes: reserve, VersionNo: 1, CreatedAt: now}
		return tx.Create(&r.control).Error
	})
	if err != nil {
		return nil, err
	}
	if (r.session.Status != "created" && r.session.Status != "uploading") || !r.control.UploadExpiresAt.After(s.now()) {
		return r.reply(s.now(), existing), nil
	}
	if sourceType == model.AIUploadSourceOpenAIInlineMultipart {
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			owner, err := s.access.ResolveSubjectTx(ctx, tx, c.Caller, s.now().UTC())
			if err != nil {
				return err
			}
			current, err := s.load(tx, owner, r.session.PublicID, true)
			if err != nil {
				return err
			}
			r = current
			if r.session.SourceType != model.AIUploadSourceOpenAIInlineMultipart {
				return ErrVideoUploadConflict
			}
			if r.session.Status == "created" {
				return s.advance(tx, &r, "uploading", nil)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		return r.reply(s.now(), existing), nil
	}
	grant, err := s.options.Store.Issue(ctx, r.target())
	if err != nil {
		return nil, ErrVideoUploadUnavailable
	}
	if grant == nil || grant.Method != "PUT" || !grant.ExpiresAt.Equal(r.control.UploadExpiresAt) {
		return nil, ErrVideoUploadUnavailable
	}
	u, err := url.Parse(grant.URL)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, ErrVideoUploadUnavailable
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner, err := s.access.ResolveSubjectTx(ctx, tx, c.Caller, s.now().UTC())
		if err != nil {
			return err
		}
		current, err := s.load(tx, owner, r.session.PublicID, true)
		if err != nil {
			return err
		}
		r = current
		if r.session.Status == "created" {
			if err := s.advance(tx, &r, "uploading", nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := r.reply(s.now(), existing)
	if r.session.Status == "uploading" && r.control.UploadExpiresAt.After(s.now()) {
		result.Upload = grant
	}
	return result, nil
}

// 状态和控制版本在调用方事务一起推进，任一CAS失败会整体回滚。
func (s *VideoUploadService) advance(tx *gorm.DB, r *videoUploadRecord, status string, control map[string]any) error {
	updates := map[string]any{"version_no": r.control.VersionNo + 1}
	for k, v := range control {
		updates[k] = v
	}
	changed := tx.Model(&videoUploadControl{}).Where("session_id=? AND version_no=?", r.session.ID, r.control.VersionNo).Updates(updates)
	if changed.Error != nil {
		return changed.Error
	}
	if changed.RowsAffected != 1 {
		return ErrVideoUploadConflict
	}
	if status != r.session.Status {
		parent := map[string]any{"status": status, "updated_at": s.now().UTC()}
		switch status {
		case "cancelled":
			parent["cancelled_at"] = s.now().UTC()
		case "rejected":
			parent["rejected_at"] = s.now().UTC()
		case "expired":
			parent["expired_at"] = s.now().UTC()
		}
		changed = tx.Model(&model.AIUploadSession{}).Where("id=? AND status=?", r.session.ID, r.session.Status).Updates(parent)
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrVideoUploadConflict
		}
	}
	r.control.VersionNo++
	r.session.Status = status
	return nil
}

func (s *VideoUploadService) Cancel(ctx context.Context, caller VideoCaller, id, key string) (*VideoUploadReply, error) {
	if s == nil {
		return nil, ErrVideoUploadUnavailable
	}
	if !videoHTTPIdempotency.MatchString(key) {
		return nil, ErrVideoUploadInvalid
	}
	hash := videoBillingDigest("upload_cancel\x00" + key)
	var record videoUploadRecord
	replay := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner, err := s.ownerForSession(ctx, tx, caller, id)
		if err != nil {
			return err
		}
		record, err = s.load(tx, owner, id, true)
		if err != nil {
			return err
		}
		if record.session.Status == "completed" || record.session.FinalInputAssetID != nil {
			return ErrVideoUploadConflict
		}
		if record.control.CancelKeyHash != nil {
			if *record.control.CancelKeyHash != hash {
				return ErrVideoUploadConflict
			}
			replay = true
			return nil
		}
		status := record.session.Status
		if videoUploadActive(status) {
			status = "cancelled"
			if !record.session.ExpiresAt.After(s.now()) {
				status = "expired"
			}
		}
		return s.advance(tx, &record, status, map[string]any{"cancel_key_hash": hash, "lease_until": nil, "cleanup_pending": true})
	})
	if err != nil {
		return nil, err
	}
	if err := s.cleanup(ctx, caller, record); err != nil {
		return nil, err
	}
	reply, err := s.Get(ctx, caller, id)
	if reply != nil {
		reply.Idempotent = replay
	}
	return reply, err
}

// 查询路径不要求客户端再提供Project；JWT仅从自己的无Key会话解析，不能借JWT读取其他Key的会话。
func (s *VideoUploadService) ownerForSession(ctx context.Context, tx *gorm.DB, caller VideoCaller, id string) (repository.VideoOwner, error) {
	if caller.APIKeyID == 0 && caller.ProjectID == 0 {
		var row struct{ ProjectID uint64 }
		if err := tx.Table("ai_upload_sessions").Select("project_id").Where("public_id=? AND user_id=? AND api_key_id IS NULL", id, caller.UserID).Take(&row).Error; err != nil {
			return repository.VideoOwner{}, videoAccessReadError(err, repository.ErrVideoUploadNotFound)
		}
		caller.ProjectID = row.ProjectID
	}
	return s.access.ResolveSubjectTx(ctx, tx, caller, s.now().UTC())
}

// 清理只作用于已登记且终止的会话；围栏Store阻止旧worker在清理后复活对象。
func (s *VideoUploadService) cleanup(ctx context.Context, caller VideoCaller, record videoUploadRecord) error {
	if err := s.options.Store.Discard(ctx, record.target()); err != nil {
		return ErrVideoUploadUnavailable
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		owner := repository.VideoOwner{UserID: record.session.UserID, ProjectID: record.session.ProjectID, APIKeyID: record.session.APIKeyID}
		r, err := s.load(tx, owner, record.session.PublicID, true)
		if err != nil {
			return err
		}
		if videoUploadActive(r.session.Status) || r.session.Status == "completed" {
			return ErrVideoUploadConflict
		}
		if r.control.CleanedAt != nil && !r.control.CleanupPending {
			return nil
		}
		return s.advance(tx, &r, r.session.Status, map[string]any{"cleanup_pending": false, "cleaned_at": s.now().UTC()})
	})
}
