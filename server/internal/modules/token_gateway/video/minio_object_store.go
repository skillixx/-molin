package video

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const minIOVideoRangeLimit = int64(1 << 20)

// VideoArchiveFenceVerifier在每次对象变更临界区核对数据库权威归档代次。
// generation为0表示普通执行路径，此时实现方必须确认当前没有生效中的归档接管。
type VideoArchiveFenceVerifier func(context.Context, string, uint64) error

type MinIOVideoObjectStoreConfig struct {
	Endpoint, AccessKey, SecretKey, Region string
	UseSSL                                 bool
	Buckets                                []string
	TempDirectory                          string
	VerifyArchiveFence                     VideoArchiveFenceVerifier
}

// MinIOVideoObjectStore把物理对象限制在视频专用bucket，并复用既有任务、资产和删除账本。
type MinIOVideoObjectStore struct {
	client             *minio.Client
	buckets            map[string]struct{}
	tempDirectory      string
	verifyArchiveFence VideoArchiveFenceVerifier
}

type VideoObjectInventoryItem struct {
	Ref       VideoObjectRef
	SizeBytes uint64
	SHA256    string
	CreatedAt time.Time
	Discarded bool
}

type VideoObjectInventoryPage struct {
	Items          []VideoObjectInventoryItem
	NextStartAfter string
	Done           bool
}

// VideoObjectInventory只供内部孤儿对账按冻结用途前缀分页读取，不得接到用户列表接口。
type VideoObjectInventory interface {
	ListPrefix(context.Context, string, string, string, int) (VideoObjectInventoryPage, error)
	InspectObject(context.Context, VideoObjectRef) (VideoObjectInventoryItem, error)
	DeleteObservedObject(context.Context, VideoObjectRef, string, uint64) error
}

// NewMinIOVideoObjectStore只构造运行时客户端，不创建bucket或修改匿名策略。
func NewMinIOVideoObjectStore(config MinIOVideoObjectStoreConfig) (*MinIOVideoObjectStore, error) {
	endpoint := strings.TrimSpace(config.Endpoint)
	accessKey := strings.TrimSpace(config.AccessKey)
	secretKey := strings.TrimSpace(config.SecretKey)
	if endpoint == "" || endpoint != config.Endpoint || accessKey == "" || accessKey != config.AccessKey || secretKey == "" || secretKey != config.SecretKey || accessKey == secretKey || strings.Contains(endpoint, "://") || strings.ContainsAny(endpoint, "@/\\ \t\r\n") || config.VerifyArchiveFence == nil {
		return nil, ErrVideoObjectInvalid
	}
	allowed := make(map[string]struct{}, len(config.Buckets))
	for _, bucket := range config.Buckets {
		if bucket == "" || bucket != strings.TrimSpace(bucket) || strings.ContainsAny(bucket, "/\\") {
			return nil, ErrVideoObjectInvalid
		}
		if _, duplicate := allowed[bucket]; duplicate {
			return nil, ErrVideoObjectInvalid
		}
		allowed[bucket] = struct{}{}
	}
	for _, zone := range []VideoObjectZone{VideoObjectTemporary, VideoObjectResult, VideoObjectQuarantine, VideoObjectSaved} {
		if _, ok := allowed[bucketForVideoZone(zone)]; !ok {
			return nil, ErrVideoObjectInvalid
		}
	}
	tempDirectory := config.TempDirectory
	if tempDirectory == "" {
		tempDirectory = os.TempDir()
	}
	absolute, err := filepath.Abs(tempDirectory)
	if err != nil || absolute != tempDirectory {
		return nil, ErrVideoObjectInvalid
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrVideoObjectInvalid
	}
	region := strings.TrimSpace(config.Region)
	if region == "" {
		region = "us-east-1"
	}
	client, err := minio.New(endpoint, &minio.Options{Creds: credentials.NewStaticV4(accessKey, secretKey, ""), Secure: config.UseSSL, Region: region})
	if err != nil {
		return nil, ErrVideoObjectInvalid
	}
	return &MinIOVideoObjectStore{client: client, buckets: allowed, tempDirectory: absolute, verifyArchiveFence: config.VerifyArchiveFence}, nil
}

// EnsureBuckets只供隔离安装或明确授权的部署步骤使用；普通服务启动应调用VerifyBuckets。
func (s *MinIOVideoObjectStore) EnsureBuckets(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrVideoObjectInvalid
	}
	for bucket := range s.buckets {
		exists, err := s.client.BucketExists(ctx, bucket)
		if err != nil {
			return err
		}
		if !exists {
			if err := s.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
				return err
			}
		}
		// 空策略显式清除匿名访问；此动作只允许在安装边界执行。
		if err := s.client.SetBucketPolicy(ctx, bucket, ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *MinIOVideoObjectStore) VerifyBuckets(ctx context.Context) error {
	if s == nil || s.client == nil {
		return ErrVideoObjectInvalid
	}
	for bucket := range s.buckets {
		exists, err := s.client.BucketExists(ctx, bucket)
		if err != nil || !exists {
			return ErrVideoObjectNotFound
		}
		policy, err := s.client.GetBucketPolicy(ctx, bucket)
		if err != nil {
			response := minio.ToErrorResponse(err)
			if response.Code != "NoSuchBucketPolicy" && response.Code != "NoSuchPolicy" {
				return ErrVideoObjectConflict
			}
		} else if strings.TrimSpace(policy) != "" {
			return ErrVideoObjectConflict
		}
	}
	return nil
}

func (s *MinIOVideoObjectStore) Put(ctx context.Context, request PutVideoObjectRequest) (StoredVideoObject, error) {
	ref, err := generateVideoObjectRef(request)
	if err != nil || !s.allowedRef(ref) {
		return StoredVideoObject{}, ErrVideoObjectInvalid
	}
	if err := s.verifyWrite(ctx, request.TaskID); err != nil {
		return StoredVideoObject{}, err
	}
	file, size, digest, err := s.spool(ctx, request.Body, request.MaxBytes)
	if err != nil {
		return StoredVideoObject{}, err
	}
	defer func() {
		name := file.Name()
		_ = file.Close()
		_ = os.Remove(name)
	}()
	if err := s.verifyWrite(ctx, request.TaskID); err != nil {
		return StoredVideoObject{}, err
	}
	return s.putImmutable(ctx, ref, file, size, digest)
}

func (s *MinIOVideoObjectStore) spool(ctx context.Context, body io.Reader, maxBytes int64) (*os.File, int64, string, error) {
	if s == nil || body == nil || maxBytes <= 0 {
		return nil, 0, "", ErrVideoObjectInvalid
	}
	file, err := os.CreateTemp(s.tempDirectory, "molin-video-*.sealed")
	if err != nil {
		return nil, 0, "", err
	}
	fail := func(problem error) (*os.File, int64, string, error) {
		name := file.Name()
		_ = file.Close()
		_ = os.Remove(name)
		return nil, 0, "", problem
	}
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(&contextReader{ctx: ctx, reader: body}, maxBytes+1))
	if err != nil {
		return fail(err)
	}
	if written == 0 || written > maxBytes {
		return fail(ErrVideoObjectTooLarge)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fail(err)
	}
	return file, written, hex.EncodeToString(hasher.Sum(nil)), nil
}

func (s *MinIOVideoObjectStore) putImmutable(ctx context.Context, ref VideoObjectRef, body io.Reader, size int64, digest string) (StoredVideoObject, error) {
	if existing, err := s.Head(ctx, ref); err == nil {
		if existing.SizeBytes == uint64(size) && existing.SHA256 == digest {
			return existing, nil
		}
		return StoredVideoObject{}, ErrVideoObjectConflict
	} else if !errors.Is(err, ErrVideoObjectNotFound) {
		return StoredVideoObject{}, err
	}
	options := minio.PutObjectOptions{ContentType: "application/octet-stream", UserMetadata: map[string]string{"sha256": digest}}
	options.SetMatchETagExcept("*")
	_, err := s.client.PutObject(ctx, ref.Bucket, ref.ObjectKey, body, size, options)
	if err != nil {
		if current, headErr := s.Head(ctx, ref); headErr == nil {
			if current.SizeBytes == uint64(size) && current.SHA256 == digest {
				return current, nil
			}
			return StoredVideoObject{}, ErrVideoObjectConflict
		}
		return StoredVideoObject{}, normalizeVideoMinIOError(err)
	}
	// 首次写与幂等重放都返回MinIO权威元数据，避免本机时钟与对象时间产生两个事实。
	stored, err := s.Head(ctx, ref)
	if err != nil || stored.SizeBytes != uint64(size) || stored.SHA256 != digest {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	return stored, nil
}

func (s *MinIOVideoObjectStore) GetRange(ctx context.Context, ref VideoObjectRef, offset, length int64) (io.ReadCloser, error) {
	if !s.allowedRef(ref) || offset < 0 || length <= 0 || length > minIOVideoRangeLimit {
		return nil, ErrVideoObjectInvalid
	}
	meta, err := s.Head(ctx, ref)
	if err != nil {
		return nil, err
	}
	if uint64(offset) > meta.SizeBytes || uint64(length) > meta.SizeBytes-uint64(offset) {
		return nil, ErrVideoObjectInvalid
	}
	options := minio.GetObjectOptions{}
	if err := options.SetRange(offset, offset+length-1); err != nil {
		return nil, ErrVideoObjectInvalid
	}
	object, err := s.client.GetObject(ctx, ref.Bucket, ref.ObjectKey, options)
	if err != nil {
		return nil, normalizeVideoMinIOError(err)
	}
	return object, nil
}

func (s *MinIOVideoObjectStore) Head(ctx context.Context, ref VideoObjectRef) (StoredVideoObject, error) {
	if !s.allowedRef(ref) {
		return StoredVideoObject{}, ErrVideoObjectInvalid
	}
	info, err := s.client.StatObject(ctx, ref.Bucket, ref.ObjectKey, minio.StatObjectOptions{})
	if err != nil {
		return StoredVideoObject{}, normalizeVideoMinIOError(err)
	}
	digest := strings.ToLower(info.Metadata.Get("X-Amz-Meta-Sha256"))
	if info.Size <= 0 || !regexpLowerHex64.MatchString(digest) {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	return StoredVideoObject{Ref: ref, SizeBytes: uint64(info.Size), SHA256: digest, CreatedAt: info.LastModified.UTC()}, nil
}

func (s *MinIOVideoObjectStore) MoveToQuarantine(ctx context.Context, ref VideoObjectRef) (StoredVideoObject, error) {
	return s.move(ctx, ref, VideoObjectQuarantine)
}

func (s *MinIOVideoObjectStore) PromoteToResult(ctx context.Context, ref VideoObjectRef) (StoredVideoObject, error) {
	return s.move(ctx, ref, VideoObjectResult)
}

func (s *MinIOVideoObjectStore) move(ctx context.Context, source VideoObjectRef, zone VideoObjectZone) (StoredVideoObject, error) {
	if !s.allowedRef(source) {
		return StoredVideoObject{}, ErrVideoObjectInvalid
	}
	taskID, err := taskIDFromVideoRef(source)
	if err != nil {
		return StoredVideoObject{}, err
	}
	if err := s.verifyWrite(ctx, taskID); err != nil {
		return StoredVideoObject{}, err
	}
	target := VideoObjectRef{Bucket: bucketForVideoZone(zone), ObjectKey: source.ObjectKey}
	if !s.allowedRef(target) {
		return StoredVideoObject{}, ErrVideoObjectInvalid
	}
	if source == target {
		return s.Head(ctx, source)
	}
	meta, err := s.copyImmutable(ctx, source, target, "", 0)
	if err != nil {
		return StoredVideoObject{}, err
	}
	if err := s.verifyWrite(ctx, taskID); err != nil {
		return StoredVideoObject{}, err
	}
	if err := s.client.RemoveObject(ctx, source.Bucket, source.ObjectKey, minio.RemoveObjectOptions{}); err != nil {
		return StoredVideoObject{}, normalizeVideoMinIOError(err)
	}
	deleted, err := s.VerifyDeleted(ctx, source)
	if err != nil || !deleted {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	return meta, nil
}

// CopyImmutable为保存到用户资产执行服务器侧受控流式复制，不接触浏览器。
func (s *MinIOVideoObjectStore) CopyImmutable(ctx context.Context, source, target VideoObjectRef, digest string, size uint64) (StoredVideoObject, error) {
	if source == target || !s.allowedRef(source) || !s.allowedRef(target) || target.Bucket != bucketForVideoZone(VideoObjectSaved) || size == 0 || !regexpLowerHex64.MatchString(digest) {
		return StoredVideoObject{}, ErrVideoObjectInvalid
	}
	taskID, err := taskIDFromVideoRef(source)
	if err != nil {
		return StoredVideoObject{}, err
	}
	if err := s.verifyWrite(ctx, taskID); err != nil {
		return StoredVideoObject{}, err
	}
	return s.copyImmutable(ctx, source, target, digest, size)
}

func (s *MinIOVideoObjectStore) copyImmutable(ctx context.Context, source, target VideoObjectRef, expectedDigest string, expectedSize uint64) (StoredVideoObject, error) {
	meta, err := s.Head(ctx, source)
	if err != nil {
		return StoredVideoObject{}, err
	}
	if expectedDigest != "" && (meta.SHA256 != expectedDigest || meta.SizeBytes != expectedSize) {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	options := minio.GetObjectOptions{}
	stat, err := s.client.StatObject(ctx, source.Bucket, source.ObjectKey, minio.StatObjectOptions{})
	if err != nil {
		return StoredVideoObject{}, normalizeVideoMinIOError(err)
	}
	if err := options.SetMatchETag(stat.ETag); err != nil {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	reader, err := s.client.GetObject(ctx, source.Bucket, source.ObjectKey, options)
	if err != nil {
		return StoredVideoObject{}, normalizeVideoMinIOError(err)
	}
	defer reader.Close()
	return s.putImmutable(ctx, target, reader, int64(meta.SizeBytes), meta.SHA256)
}

func (s *MinIOVideoObjectStore) Delete(ctx context.Context, ref VideoObjectRef) error {
	if !s.allowedRef(ref) {
		return ErrVideoObjectInvalid
	}
	// 长期保存对象由独立保存账本授权；其首段是保存操作ID，不得误当原视频Task查询归档围栏。
	if ref.Bucket == bucketForVideoZone(VideoObjectSaved) {
		if _, archiveScoped := ctx.Value(archiveWriteScopeKey{}).(archiveWriteScope); archiveScoped {
			return ErrVideoObjectConflict
		}
		return normalizeVideoMinIOError(s.client.RemoveObject(ctx, ref.Bucket, ref.ObjectKey, minio.RemoveObjectOptions{}))
	}
	taskID, err := taskIDFromVideoRef(ref)
	if err != nil {
		return err
	}
	if err := s.verifyWrite(ctx, taskID); err != nil {
		return err
	}
	return normalizeVideoMinIOError(s.client.RemoveObject(ctx, ref.Bucket, ref.ObjectKey, minio.RemoveObjectOptions{}))
}

func (s *MinIOVideoObjectStore) SupportsSynchronousDeletion() bool { return true }

func (s *MinIOVideoObjectStore) VerifyDeleted(ctx context.Context, ref VideoObjectRef) (bool, error) {
	if !s.allowedRef(ref) {
		return false, ErrVideoObjectInvalid
	}
	_, err := s.client.StatObject(ctx, ref.Bucket, ref.ObjectKey, minio.StatObjectOptions{})
	if errors.Is(normalizeVideoMinIOError(err), ErrVideoObjectNotFound) {
		return true, nil
	}
	return false, normalizeVideoMinIOError(err)
}

func (s *MinIOVideoObjectStore) ListPrefix(ctx context.Context, bucket, prefix, startAfter string, limit int) (VideoObjectInventoryPage, error) {
	if s == nil || s.client == nil || limit < 1 || limit > 1000 || !s.allowedInventoryPrefix(bucket, prefix) || (startAfter != "" && (!strings.HasPrefix(startAfter, prefix) || len(startAfter) > 191 || strings.ContainsAny(startAfter, "\x00\r\n\\"))) {
		return VideoObjectInventoryPage{}, ErrVideoObjectInvalid
	}
	listCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	items := make([]VideoObjectInventoryItem, 0, limit+1)
	for object := range s.client.ListObjects(listCtx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true, StartAfter: startAfter, MaxKeys: 1000}) {
		if object.Err != nil {
			return VideoObjectInventoryPage{}, normalizeVideoMinIOError(object.Err)
		}
		if len(items) == limit+1 {
			break
		}
		// List的自定义metadata在不同MinIO版本并不稳定；每个候选再Stat，不能把缺字段误判为孤儿。
		inspected, err := s.InspectObject(ctx, VideoObjectRef{Bucket: bucket, ObjectKey: object.Key})
		if err != nil {
			return VideoObjectInventoryPage{}, err
		}
		items = append(items, inspected)
	}
	done := len(items) <= limit
	if !done {
		items = items[:limit]
	}
	next := ""
	if !done && len(items) > 0 {
		next = items[len(items)-1].Ref.ObjectKey
	}
	return VideoObjectInventoryPage{Items: items, NextStartAfter: next, Done: done}, nil
}

func (s *MinIOVideoObjectStore) InspectObject(ctx context.Context, ref VideoObjectRef) (VideoObjectInventoryItem, error) {
	if s == nil || s.client == nil || !s.allowedInventoryRef(ref) {
		return VideoObjectInventoryItem{}, ErrVideoObjectInvalid
	}
	object, err := s.client.StatObject(ctx, ref.Bucket, ref.ObjectKey, minio.StatObjectOptions{})
	if err != nil {
		return VideoObjectInventoryItem{}, normalizeVideoMinIOError(err)
	}
	digest := strings.ToLower(object.Metadata.Get("X-Amz-Meta-Sha256"))
	discarded := object.Metadata.Get("X-Amz-Meta-Molin-Discarded") == "1"
	if object.Size < 0 || (!discarded && !regexpLowerHex64.MatchString(digest)) {
		return VideoObjectInventoryItem{}, ErrVideoObjectConflict
	}
	return VideoObjectInventoryItem{Ref: ref, SizeBytes: uint64(object.Size), SHA256: digest, CreatedAt: object.LastModified.UTC(), Discarded: discarded}, nil
}

// DeleteObservedObject只删除与持久观察摘要完全一致的对象；不存在视为幂等成功，漂移对象失败关闭。
func (s *MinIOVideoObjectStore) DeleteObservedObject(ctx context.Context, ref VideoObjectRef, digest string, size uint64) error {
	if !regexpLowerHex64.MatchString(digest) || size == 0 {
		return ErrVideoObjectInvalid
	}
	item, err := s.InspectObject(ctx, ref)
	if errors.Is(err, ErrVideoObjectNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	actualDigest := item.SHA256
	if item.Discarded {
		sum := sha256.Sum256([]byte{0})
		actualDigest = hex.EncodeToString(sum[:])
	}
	if actualDigest != digest || item.SizeBytes != size {
		return ErrVideoObjectConflict
	}
	if err := s.client.RemoveObject(ctx, ref.Bucket, ref.ObjectKey, minio.RemoveObjectOptions{}); err != nil {
		return normalizeVideoMinIOError(err)
	}
	if _, err := s.InspectObject(ctx, ref); !errors.Is(err, ErrVideoObjectNotFound) {
		if err != nil {
			return err
		}
		return ErrVideoObjectConflict
	}
	return nil
}

func (s *MinIOVideoObjectStore) allowedInventoryRef(ref VideoObjectRef) bool {
	for _, prefix := range []string{"vid_", "video_", "original/", "inline/", "normalized/", "vsave_"} {
		if strings.HasPrefix(ref.ObjectKey, prefix) {
			return s.allowedInventoryPrefix(ref.Bucket, prefix)
		}
	}
	return false
}

func (s *MinIOVideoObjectStore) allowedInventoryPrefix(bucket, prefix string) bool {
	if _, ok := s.buckets[bucket]; !ok {
		return false
	}
	switch bucket {
	case "ai-upload-temp":
		return prefix == "vid_" || prefix == "video_" || prefix == "original/" || prefix == "inline/"
	case "ai-result":
		return prefix == "vid_" || prefix == "video_" || prefix == "normalized/"
	case "ai-quarantine":
		return prefix == "vid_" || prefix == "video_"
	case "ai-user-assets":
		return prefix == "vsave_"
	default:
		return false
	}
}

func (s *MinIOVideoObjectStore) AdvanceArchiveFence(ctx context.Context, taskID string, generation uint64) error {
	if s == nil || !videoObjectIDPattern.MatchString(taskID) || generation == 0 {
		return ErrVideoObjectInvalid
	}
	return s.verifyArchiveFence(ctx, taskID, generation)
}

func (s *MinIOVideoObjectStore) verifyWrite(ctx context.Context, taskID string) error {
	if s == nil || s.verifyArchiveFence == nil || !videoObjectIDPattern.MatchString(taskID) {
		return ErrVideoObjectInvalid
	}
	generation := uint64(0)
	if scope, ok := ctx.Value(archiveWriteScopeKey{}).(archiveWriteScope); ok {
		if scope.taskID != taskID || scope.generation == 0 {
			return ErrVideoObjectConflict
		}
		generation = scope.generation
	}
	if err := s.verifyArchiveFence(ctx, taskID, generation); err != nil {
		return ErrVideoObjectConflict
	}
	return nil
}

func (s *MinIOVideoObjectStore) allowedRef(ref VideoObjectRef) bool {
	if s == nil || s.client == nil || !validVideoObjectRef(ref) {
		return false
	}
	_, ok := s.buckets[ref.Bucket]
	return ok
}

func (s *MinIOVideoObjectStore) spoolTempCount() (int, error) {
	entries, err := filepath.Glob(filepath.Join(s.tempDirectory, "molin-video-*.sealed"))
	return len(entries), err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func taskIDFromVideoRef(ref VideoObjectRef) (string, error) {
	parts := strings.Split(ref.ObjectKey, "/")
	if len(parts) != 3 || !videoObjectIDPattern.MatchString(parts[0]) {
		return "", ErrVideoObjectInvalid
	}
	return parts[0], nil
}

func normalizeVideoMinIOError(err error) error {
	if err == nil {
		return nil
	}
	response := minio.ToErrorResponse(err)
	switch response.Code {
	case "NoSuchKey", "NoSuchObject", "NoSuchBucket", "NotFound":
		return ErrVideoObjectNotFound
	case "PreconditionFailed":
		return ErrVideoObjectConflict
	default:
		return err
	}
}

var regexpLowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

var _ VideoArchiveObjectStore = (*MinIOVideoObjectStore)(nil)
var _ VideoObjectInventory = (*MinIOVideoObjectStore)(nil)
