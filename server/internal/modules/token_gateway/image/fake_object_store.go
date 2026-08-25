package image

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxFakeSignedURLTTL = 15 * time.Minute

type fakeStoredObject struct {
	meta StoredObject
	body []byte
}

// FakeObjectStore 为本地契约测试提供并发安全、幂等且有界的对象存储，不访问网络或真实 MinIO。
type FakeObjectStore struct {
	mu      sync.RWMutex
	objects map[ObjectRef]fakeStoredObject
	now     func() time.Time
}

func NewFakeObjectStore() *FakeObjectStore {
	return &FakeObjectStore{objects: make(map[ObjectRef]fakeStoredObject), now: time.Now}
}

func (s *FakeObjectStore) Put(ctx context.Context, ref ObjectRef, body io.Reader, maxBytes int64) (StoredObject, error) {
	if err := ctx.Err(); err != nil {
		return StoredObject{}, err
	}
	if !validObjectRef(ref) || body == nil || maxBytes <= 0 {
		return StoredObject{}, ErrObjectInvalid
	}
	limited := io.LimitReader(body, maxBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return StoredObject{}, err
	}
	if int64(len(raw)) > maxBytes {
		return StoredObject{}, ErrObjectTooLarge
	}
	sum := sha256.Sum256(raw)
	meta := StoredObject{Ref: ref, SizeBytes: uint64(len(raw)), SHA256: hex.EncodeToString(sum[:]), CreatedAt: s.now().UTC()}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.objects[ref]; ok {
		if existing.meta.SHA256 != meta.SHA256 || !bytes.Equal(existing.body, raw) {
			return StoredObject{}, ErrObjectConflict
		}
		return existing.meta, nil
	}
	s.objects[ref] = fakeStoredObject{meta: meta, body: append([]byte(nil), raw...)}
	return meta, nil
}

func (s *FakeObjectStore) Get(ctx context.Context, ref ObjectRef) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[ref]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return append([]byte(nil), object.body...), nil
}

func (s *FakeObjectStore) Head(ctx context.Context, ref ObjectRef) (StoredObject, error) {
	if err := ctx.Err(); err != nil {
		return StoredObject{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	object, ok := s.objects[ref]
	if !ok {
		return StoredObject{}, ErrObjectNotFound
	}
	return object.meta, nil
}

func (s *FakeObjectStore) Delete(ctx context.Context, ref ObjectRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validObjectRef(ref) {
		return ErrObjectInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, ref)
	return nil
}

func (s *FakeObjectStore) SignDownload(ctx context.Context, ref ObjectRef, expiresIn time.Duration) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if expiresIn <= 0 || expiresIn > maxFakeSignedURLTTL || !validObjectRef(ref) {
		return "", ErrObjectInvalid
	}
	object, err := s.Head(ctx, ref)
	if err != nil {
		return "", err
	}
	expiresAt := s.now().UTC().Add(expiresIn).Unix()
	return fmt.Sprintf("https://object.invalid/%s/%s?expires=%d&sig=%s", url.PathEscape(ref.Bucket), escapeObjectKey(ref.Key), expiresAt, object.SHA256[:16]), nil
}

func validObjectRef(ref ObjectRef) bool {
	if strings.TrimSpace(ref.Bucket) != ref.Bucket || strings.TrimSpace(ref.Key) != ref.Key || ref.Bucket == "" || ref.Key == "" {
		return false
	}
	if strings.Contains(ref.Bucket, "/") || strings.Contains(ref.Bucket, "\\") || strings.Contains(ref.Key, "\\") || strings.HasPrefix(ref.Key, "/") {
		return false
	}
	for _, segment := range strings.Split(ref.Key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func escapeObjectKey(key string) string {
	parts := strings.Split(key, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}
