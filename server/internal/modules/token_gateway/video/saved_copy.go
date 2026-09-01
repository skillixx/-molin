package video

import (
	"context"
)

// CopyImmutable模拟对象存储的服务端原子复制；不经过浏览器，不改变原对象及其删除事实。
// 长期目标由应用服务预先冻结，调用方必须持有原任务执行权并核对完整资产快照。
func (s *FakeVideoObjectStore) CopyImmutable(ctx context.Context, source, target VideoObjectRef, hash string, size uint64) (StoredVideoObject, error) {
	if err := ctx.Err(); err != nil {
		return StoredVideoObject{}, err
	}
	if source == target || !validVideoObjectRef(source) || !validVideoObjectRef(target) || target.Bucket != bucketForVideoZone(VideoObjectSaved) || size == 0 {
		return StoredVideoObject{}, ErrVideoObjectInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tombstones[target] {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	original, ok := s.objects[source]
	if !ok || s.tombstones[source] {
		return StoredVideoObject{}, ErrVideoObjectNotFound
	}
	if original.meta.SHA256 != hash || original.meta.SizeBytes != size {
		return StoredVideoObject{}, ErrVideoObjectConflict
	}
	if existing, ok := s.objects[target]; ok {
		if existing.meta.SHA256 != hash || existing.meta.SizeBytes != size || !equalVideoChunks(original.chunks, existing.chunks) {
			return StoredVideoObject{}, ErrVideoObjectConflict
		}
		return existing.meta, nil
	}
	chunks := make([][]byte, len(original.chunks))
	for i, chunk := range original.chunks {
		if err := ctx.Err(); err != nil {
			return StoredVideoObject{}, err
		}
		chunks[i] = append([]byte(nil), chunk...)
	}
	meta := StoredVideoObject{Ref: target, SHA256: hash, SizeBytes: size, CreatedAt: s.now().UTC()}
	s.objects[target] = fakeVideoObject{meta: meta, chunks: chunks}
	return meta, nil
}
