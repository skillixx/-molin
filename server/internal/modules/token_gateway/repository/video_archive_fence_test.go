package repository

import (
	"testing"
	"time"
)

func TestVideoG6ArchiveFenceRejectsUnprovenWriter(t *testing.T) {
	now := time.Now().UTC()
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	task := &VideoTaskRecord{ArchiveTokenHash: &hash}
	if err := CheckVideoArchiveFence(task, nil, now); err == nil {
		t.Fatal("已有归档围栏时普通Worker不能继续写状态")
	}
	task.ArchiveTokenHash = nil
	if err := CheckVideoArchiveFence(task, nil, now); err != nil {
		t.Fatal("没有围栏的旧G3/G4任务必须保持兼容")
	}
}
