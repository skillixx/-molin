package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrVideoObjectInvalid  = errors.New("视频对象参数无效")
	ErrVideoObjectTooLarge = errors.New("视频对象超过大小上限")
	ErrVideoObjectNotFound = errors.New("视频对象不存在")
	ErrVideoObjectConflict = errors.New("视频对象键内容冲突")
)

type VideoObjectZone string

const (
	VideoObjectTemporary  VideoObjectZone = "temporary"
	VideoObjectResult     VideoObjectZone = "result"
	VideoObjectQuarantine VideoObjectZone = "quarantine"
	VideoObjectSaved      VideoObjectZone = "saved"
)

type VideoObjectRef struct {
	Bucket    string `json:"-"`
	ObjectKey string `json:"-"`
}

type PutVideoObjectRequest struct {
	Zone     VideoObjectZone
	TaskID   string
	AssetID  string
	Role     string
	Body     io.Reader
	MaxBytes int64
}

type StoredVideoObject struct {
	Ref       VideoObjectRef `json:"-"`
	SizeBytes uint64
	SHA256    string
	CreatedAt time.Time
}

type VideoObjectStore interface {
	Put(ctx context.Context, request PutVideoObjectRequest) (StoredVideoObject, error)
	GetRange(ctx context.Context, ref VideoObjectRef, offset, length int64) (io.ReadCloser, error)
	Head(ctx context.Context, ref VideoObjectRef) (StoredVideoObject, error)
	MoveToQuarantine(ctx context.Context, ref VideoObjectRef) (StoredVideoObject, error)
	PromoteToResult(ctx context.Context, ref VideoObjectRef) (StoredVideoObject, error)
	Delete(ctx context.Context, ref VideoObjectRef) error
}

type fakeVideoObject struct {
	meta   StoredVideoObject
	chunks [][]byte
}

const (
	fakeVideoObjectChunkBytes = 64 << 10
	fakeVideoMaxRangeBytes    = 1 << 20
)

// FakeVideoObjectStore 只保存本地测试字节，所有对象位置均由网关根据受控标识生成。
type FakeVideoObjectStore struct {
	mu                 sync.RWMutex
	objects            map[VideoObjectRef]fakeVideoObject
	tombstones         map[VideoObjectRef]bool
	now                func() time.Time
	archiveGenerations map[string]uint64
}

func NewFakeVideoObjectStore() *FakeVideoObjectStore {
	return &FakeVideoObjectStore{objects: make(map[VideoObjectRef]fakeVideoObject), tombstones: make(map[VideoObjectRef]bool), now: time.Now}
}

func (s *FakeVideoObjectStore) Put(ctx context.Context, request PutVideoObjectRequest) (StoredVideoObject, error) {
	if err := ctx.Err(); err != nil {
		return StoredVideoObject{}, err
	}
	ref, err := generateVideoObjectRef(request)
	if err != nil {
		return StoredVideoObject{}, err
	}
	s.mu.RLock()
	allowed := s.archiveWriteAllowedLocked(ctx, request.TaskID)
	s.mu.RUnlock()
	if !allowed {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	hasher := sha256.New()
	chunks := make([][]byte, 0)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return StoredVideoObject{}, err
		}
		buffer := make([]byte, fakeVideoObjectChunkBytes)
		count, readErr := request.Body.Read(buffer)
		if count > 0 {
			size += int64(count)
			if size > request.MaxBytes {
				return StoredVideoObject{}, ErrVideoObjectTooLarge
			}
			chunk := append([]byte(nil), buffer[:count]...)
			chunks = append(chunks, chunk)
			_, _ = hasher.Write(chunk)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return StoredVideoObject{}, readErr
		}
		if count == 0 {
			return StoredVideoObject{}, io.ErrNoProgress
		}
	}
	meta := StoredVideoObject{Ref: ref, SizeBytes: uint64(size), SHA256: hex.EncodeToString(hasher.Sum(nil)), CreatedAt: s.now().UTC()}

	s.mu.Lock()
	defer s.mu.Unlock()
	// 读体可能跨过接管时刻；临界区内再次检查才能拒绝迟到的旧写入。
	if err := ctx.Err(); err != nil {
		return StoredVideoObject{}, err
	}
	if !s.archiveWriteAllowedLocked(ctx, request.TaskID) {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	if s.tombstones[ref] {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	if existing, ok := s.objects[ref]; ok {
		if existing.meta.SHA256 != meta.SHA256 || existing.meta.SizeBytes != meta.SizeBytes || !equalVideoChunks(existing.chunks, chunks) {
			return StoredVideoObject{}, ErrVideoObjectConflict
		}
		return existing.meta, nil
	}
	s.objects[ref] = fakeVideoObject{meta: meta, chunks: chunks}
	return meta, nil
}

func (s *FakeVideoObjectStore) GetRange(ctx context.Context, ref VideoObjectRef, offset, length int64) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offset < 0 || length <= 0 || length > fakeVideoMaxRangeBytes {
		return nil, ErrVideoObjectInvalid
	}
	s.mu.RLock()
	object, ok := s.objects[ref]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrVideoObjectNotFound
	}
	if offset > int64(object.meta.SizeBytes) || length > int64(object.meta.SizeBytes)-offset {
		return nil, ErrVideoObjectInvalid
	}
	result := make([]byte, length)
	written := int64(0)
	for chunkIndex, chunk := range object.chunks {
		chunkStart := int64(chunkIndex * fakeVideoObjectChunkBytes)
		chunkEnd := chunkStart + int64(len(chunk))
		if chunkEnd <= offset || chunkStart >= offset+length {
			continue
		}
		start := maxInt64(offset, chunkStart) - chunkStart
		end := minInt64(offset+length, chunkEnd) - chunkStart
		written += int64(copy(result[written:], chunk[start:end]))
	}
	if written != length {
		return nil, ErrVideoObjectInvalid
	}
	return io.NopCloser(bytes.NewReader(result)), nil
}

func (s *FakeVideoObjectStore) Head(ctx context.Context, ref VideoObjectRef) (StoredVideoObject, error) {
	if err := ctx.Err(); err != nil {
		return StoredVideoObject{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[ref]
	if !ok {
		return StoredVideoObject{}, ErrVideoObjectNotFound
	}
	return object.meta, nil
}

func (s *FakeVideoObjectStore) MoveToQuarantine(ctx context.Context, ref VideoObjectRef) (StoredVideoObject, error) {
	return s.move(ctx, ref, VideoObjectQuarantine)
}

func (s *FakeVideoObjectStore) PromoteToResult(ctx context.Context, ref VideoObjectRef) (StoredVideoObject, error) {
	return s.move(ctx, ref, VideoObjectResult)
}

func (s *FakeVideoObjectStore) move(ctx context.Context, ref VideoObjectRef, zone VideoObjectZone) (StoredVideoObject, error) {
	if err := ctx.Err(); err != nil {
		return StoredVideoObject{}, err
	}
	target := VideoObjectRef{Bucket: bucketForVideoZone(zone), ObjectKey: ref.ObjectKey}
	s.mu.Lock()
	defer s.mu.Unlock()
	taskID, _, ok := strings.Cut(ref.ObjectKey, "/")
	if err := ctx.Err(); err != nil {
		return StoredVideoObject{}, err
	}
	if !ok || !s.archiveWriteAllowedLocked(ctx, taskID) {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	source, ok := s.objects[ref]
	if !ok {
		return StoredVideoObject{}, ErrVideoObjectNotFound
	}
	if target == ref {
		return source.meta, nil
	}
	if s.tombstones[target] {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	if existing, exists := s.objects[target]; exists && (existing.meta.SHA256 != source.meta.SHA256 || !equalVideoChunks(existing.chunks, source.chunks)) {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	source.meta.Ref = target
	s.objects[target] = source
	delete(s.objects, ref)
	return source.meta, nil
}

func equalVideoChunks(left, right [][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !bytes.Equal(left[index], right[index]) {
			return false
		}
	}
	return true
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func (s *FakeVideoObjectStore) Delete(ctx context.Context, ref VideoObjectRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validVideoObjectRef(ref) {
		return ErrVideoObjectInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 归档执行上下文不能用旧代次删除对象；普通用户删除仍由既有生命周期/财务门禁授权。
	if _, scoped := ctx.Value(archiveWriteScopeKey{}).(archiveWriteScope); scoped {
		taskID, _, ok := strings.Cut(ref.ObjectKey, "/")
		if !ok || !s.archiveWriteAllowedLocked(ctx, taskID) {
			return ErrVideoObjectConflict
		}
	}
	delete(s.objects, ref)
	if s.tombstones == nil {
		s.tombstones = make(map[VideoObjectRef]bool)
	}
	s.tombstones[ref] = true
	return nil
}

// Fake删除确认必须同时有原目标墓碑且正文不存在，不能用一般Head错误冒充已删除。
func (s *FakeVideoObjectStore) SupportsSynchronousDeletion() bool { return true }
func (s *FakeVideoObjectStore) VerifyDeleted(ctx context.Context, ref VideoObjectRef) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.objects[ref]
	return s.tombstones[ref] && !exists, nil
}

var videoObjectIDPattern = regexp.MustCompile("^[A-Za-z0-9_-]{1,128}$")

func generateVideoObjectRef(request PutVideoObjectRequest) (VideoObjectRef, error) {
	if request.Body == nil || request.MaxBytes <= 0 || !videoObjectIDPattern.MatchString(request.TaskID) || !videoObjectIDPattern.MatchString(request.AssetID) || !validVideoObjectRole(request.Role) {
		return VideoObjectRef{}, ErrVideoObjectInvalid
	}
	bucket := bucketForVideoZone(request.Zone)
	if bucket == "" {
		return VideoObjectRef{}, ErrVideoObjectInvalid
	}
	return VideoObjectRef{Bucket: bucket, ObjectKey: request.TaskID + "/" + request.AssetID + "/" + request.Role + ".bin"}, nil
}

func bucketForVideoZone(zone VideoObjectZone) string {
	switch zone {
	case VideoObjectTemporary:
		return "video-temp"
	case VideoObjectResult:
		return "video-result"
	case VideoObjectQuarantine:
		return "video-quarantine"
	case VideoObjectSaved:
		return "ai-user-assets"
	default:
		return ""
	}
}

func validVideoObjectRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "content", "cover", "preview", "thumbnail", "moderation_copy", "derived":
		return true
	default:
		return false
	}
}

func validVideoObjectRef(ref VideoObjectRef) bool {
	if ref.Bucket != bucketForVideoZone(VideoObjectTemporary) && ref.Bucket != bucketForVideoZone(VideoObjectResult) && ref.Bucket != bucketForVideoZone(VideoObjectQuarantine) && ref.Bucket != bucketForVideoZone(VideoObjectSaved) {
		return false
	}
	parts := strings.Split(ref.ObjectKey, "/")
	return len(parts) == 3 && videoObjectIDPattern.MatchString(parts[0]) && videoObjectIDPattern.MatchString(parts[1]) && strings.HasSuffix(parts[2], ".bin")
}
