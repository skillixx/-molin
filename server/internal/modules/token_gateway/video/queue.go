package video

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

var ErrQueueLeaseInvalid = errors.New("视频任务队列租约无效")

type TaskQueueLease struct {
	TaskID    string
	WorkerID  string
	Attempt   uint32
	ExpiresAt time.Time
}

type deterministicQueueItem struct {
	taskID    string
	workerID  string
	attempt   uint32
	expiresAt time.Time
	acked     bool
}

// DeterministicTaskQueue 是进程内Fake队列，只用于G4确定性测试，不替代RabbitMQ或Outbox。
type DeterministicTaskQueue struct {
	mu    sync.Mutex
	now   func() time.Time
	order []string
	items map[string]*deterministicQueueItem
}

func NewDeterministicTaskQueue(now func() time.Time) *DeterministicTaskQueue {
	if now == nil {
		now = time.Now
	}
	return &DeterministicTaskQueue{now: now, items: make(map[string]*deterministicQueueItem)}
}

func (q *DeterministicTaskQueue) Enqueue(taskID string) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ErrQueueLeaseInvalid
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.items[taskID]; exists {
		return nil
	}
	q.items[taskID] = &deterministicQueueItem{taskID: taskID}
	q.order = append(q.order, taskID)
	return nil
}

func (q *DeterministicTaskQueue) Claim(ctx context.Context, workerID string, leaseDuration time.Duration) (TaskQueueLease, bool) {
	if err := ctx.Err(); err != nil || strings.TrimSpace(workerID) == "" || leaseDuration <= 0 {
		return TaskQueueLease{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now().UTC()
	for _, taskID := range q.order {
		item := q.items[taskID]
		if item == nil || item.acked || item.workerID != "" && item.expiresAt.After(now) {
			continue
		}
		item.workerID = workerID
		item.attempt++
		item.expiresAt = now.Add(leaseDuration)
		return TaskQueueLease{TaskID: taskID, WorkerID: workerID, Attempt: item.attempt, ExpiresAt: item.expiresAt}, true
	}
	return TaskQueueLease{}, false
}

func (q *DeterministicTaskQueue) Ack(lease TaskQueueLease) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	item := q.items[lease.TaskID]
	if item == nil || item.acked || item.workerID != lease.WorkerID || item.attempt != lease.Attempt || !item.expiresAt.Equal(lease.ExpiresAt) {
		return ErrQueueLeaseInvalid
	}
	item.acked = true
	item.workerID = ""
	return nil
}

func (q *DeterministicTaskQueue) Nack(lease TaskQueueLease) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	item := q.items[lease.TaskID]
	if item == nil || item.acked || item.workerID != lease.WorkerID || item.attempt != lease.Attempt {
		return ErrQueueLeaseInvalid
	}
	item.workerID = ""
	item.expiresAt = time.Time{}
	return nil
}

func (q *DeterministicTaskQueue) RecoverExpired() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now().UTC()
	recovered := 0
	for _, item := range q.items {
		if item != nil && !item.acked && item.workerID != "" && !item.expiresAt.After(now) {
			item.workerID = ""
			item.expiresAt = time.Time{}
			recovered++
		}
	}
	return recovered
}

func (q *DeterministicTaskQueue) Pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	pending := 0
	for _, item := range q.items {
		if item != nil && !item.acked {
			pending++
		}
	}
	return pending
}
