package service

import (
	"context"
	"errors"
	"testing"
)

func TestVideoG6ProjectKeyExplicitCapabilityAndRotation(t *testing.T) {
	store := newMemoryProjectStore()
	store.videoGrant = true
	svc := NewProjectService(store, "video-key-test-secret").WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithAuditRecorder(&memoryProjectAudit{})
	_, view, err := svc.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "视频密钥", ScopeMode: ScopeModeAllowlist, ModelCodes: []string{"molin/video"}, VideoGenerateAllowed: true, IdempotencyKey: "video-key-unit-issue"})
	if err != nil || !view.VideoGenerateAllowed || !store.keys[view.ID].VideoGenerateAllowed {
		t.Fatalf("显式视频能力未写入: view=%+v err=%v", view, err)
	}
	_, rotated, err := svc.RotateKey(context.Background(), 5, 9, view.ID, "127.0.0.1", "video-key-unit-rotate")
	if err != nil || !rotated.VideoGenerateAllowed || !store.keys[rotated.ID].VideoGenerateAllowed || store.keys[view.ID].Status != "revoked" {
		t.Fatal("轮换没有原子继承已明确视频能力")
	}
	if _, _, err := svc.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "全模型视频密钥", ScopeMode: ScopeModeAll, VideoGenerateAllowed: true}); !errors.Is(err, ErrScopeModeInvalid) {
		t.Fatal("全模型Key不应获得视频能力")
	}
	store.videoGrant = false
	if _, _, err := svc.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "无Project授权", ModelCodes: []string{"molin/video"}, VideoGenerateAllowed: true}); !errors.Is(err, ErrScopeModelInvalid) {
		t.Fatal("缺Project模型授权仍签发视频Key")
	}
	_, legacy, err := svc.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "旧式Key", ModelCodes: []string{"molin/chat"}})
	if err != nil || legacy.VideoGenerateAllowed {
		t.Fatal("旧式Key不得自动继承视频能力")
	}
}
