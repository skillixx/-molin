package video

import "context"

// 存储必须在提交不可变写入/移动的同一临界区检查代次，而不是只在数据库读前检查。
type VideoArchiveObjectStore interface {
	VideoObjectStore
	AdvanceArchiveFence(context.Context, string, uint64) error
}

type archiveWriteScopeKey struct{}
type archiveWriteScope struct {
	taskID     string
	generation uint64
}

// 仅归档协调器在验证原Task的不透明围栏证明后构造，不接受HTTP提供的代次。
func WithArchiveWriteGeneration(ctx context.Context, taskID string, generation uint64) context.Context {
	return context.WithValue(ctx, archiveWriteScopeKey{}, archiveWriteScope{taskID: taskID, generation: generation})
}

func (s *FakeVideoObjectStore) AdvanceArchiveFence(ctx context.Context, taskID string, generation uint64) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if !videoObjectIDPattern.MatchString(taskID) || generation == 0 {
		return ErrVideoObjectInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.archiveGenerations == nil {
		s.archiveGenerations = map[string]uint64{}
	}
	if generation < s.archiveGenerations[taskID] {
		return ErrVideoObjectConflict
	}
	s.archiveGenerations[taskID] = generation
	return nil
}

func (s *FakeVideoObjectStore) archiveWriteAllowedLocked(ctx context.Context, taskID string) bool {
	generation := s.archiveGenerations[taskID]
	scope, provided := ctx.Value(archiveWriteScopeKey{}).(archiveWriteScope)
	if !provided {
		return generation == 0
	}
	return scope.taskID == taskID && scope.generation == generation && scope.generation > 0
}
