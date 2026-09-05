package service

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"

	imagegateway "molin/server/internal/modules/token_gateway/image"
)

// VideoMinIOImportStore复用图片网关已验收的私有ObjectStore；源对象只读，目标限定为视频import规范化前缀。
type VideoMinIOImportStore struct {
	store            imagegateway.ObjectStore
	normalizedBucket string
}

func NewVideoMinIOImportStore(store imagegateway.ObjectStore, normalizedBucket string) (*VideoMinIOImportStore, error) {
	if store == nil || normalizedBucket != "ai-result" {
		return nil, ErrVideoImportUnavailable
	}
	return &VideoMinIOImportStore{store: store, normalizedBucket: normalizedBucket}, nil
}

func (s *VideoMinIOImportStore) Read(ctx context.Context, object VideoImportObject, maxBytes int64) ([]byte, error) {
	if s == nil || object.Bucket != "ai-result" || object.Key == "" || strings.HasPrefix(object.Key, "import/") || maxBytes <= 0 || maxBytes > videoUploadMaxBytes {
		return nil, ErrVideoImportUnavailable
	}
	raw, err := s.store.Get(ctx, imagegateway.ObjectRef{Bucket: object.Bucket, Key: object.Key})
	if err != nil || len(raw) == 0 || int64(len(raw)) > maxBytes {
		return nil, ErrVideoImportUnavailable
	}
	return raw, nil
}

func (s *VideoMinIOImportStore) Put(ctx context.Context, object VideoImportObject, raw []byte, digest string) error {
	if s == nil || object.Bucket != s.normalizedBucket || !validVideoImportKey(object.Key) || len(raw) == 0 || int64(len(raw)) > videoUploadMaxBytes || videoPayloadSHA256(raw) != digest {
		return ErrVideoImportConflict
	}
	stored, err := s.store.Put(ctx, imagegateway.ObjectRef{Bucket: object.Bucket, Key: object.Key}, bytes.NewReader(raw), videoUploadMaxBytes)
	if err != nil {
		if errors.Is(err, imagegateway.ErrObjectConflict) {
			return ErrVideoImportConflict
		}
		return ErrVideoImportUnavailable
	}
	if stored.SHA256 != digest || stored.SizeBytes != uint64(len(raw)) {
		return ErrVideoImportConflict
	}
	return nil
}

func (s *VideoMinIOImportStore) Discard(ctx context.Context, object VideoImportObject) error {
	if s == nil || object.Bucket != s.normalizedBucket || !validVideoImportKey(object.Key) {
		return ErrVideoImportConflict
	}
	if err := s.store.Delete(ctx, imagegateway.ObjectRef{Bucket: object.Bucket, Key: object.Key}); err != nil {
		return ErrVideoImportUnavailable
	}
	return nil
}

func (s *VideoMinIOImportStore) SupportsSynchronousDeletion() bool { return true }

func (s *VideoMinIOImportStore) VerifyDiscarded(ctx context.Context, object VideoImportObject) (bool, error) {
	if s == nil || object.Bucket != s.normalizedBucket || !validVideoImportKey(object.Key) {
		return false, ErrVideoImportConflict
	}
	_, err := s.store.Head(ctx, imagegateway.ObjectRef{Bucket: object.Bucket, Key: object.Key})
	if errors.Is(err, imagegateway.ErrObjectNotFound) {
		return true, nil
	}
	if err != nil {
		return false, ErrVideoImportUnavailable
	}
	return false, nil
}

func validVideoImportKey(key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) != 4 || parts[0] != "import" || !strings.HasPrefix(parts[3], "vin_") || !strings.HasSuffix(parts[3], ".png") || strings.ContainsAny(key, "\\\x00\r\n") {
		return false
	}
	user, userErr := strconv.ParseUint(parts[1], 10, 64)
	project, projectErr := strconv.ParseUint(parts[2], 10, 64)
	id := strings.TrimSuffix(parts[3], ".png")
	return userErr == nil && projectErr == nil && user > 0 && project > 0 && strconv.FormatUint(user, 10) == parts[1] && strconv.FormatUint(project, 10) == parts[2] && videoBillingPublicID.MatchString(id)
}

var _ VideoInputImportStore = (*VideoMinIOImportStore)(nil)
