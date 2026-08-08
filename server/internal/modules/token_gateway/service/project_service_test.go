package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	authmodel "molin/server/internal/modules/auth/model"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/pkg/crypto"
)

func TestNormalizeProjectTimezone(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback string
		want     string
		wantErr  bool
	}{
		{name: "使用默认时区", fallback: "Asia/Shanghai", want: "Asia/Shanghai"},
		{name: "允许标准 IANA 时区", value: "America/New_York", want: "America/New_York"},
		{name: "拒绝本机动态时区", value: "Local", wantErr: true},
		{name: "拒绝无效时区", value: "China/NotFound", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := normalizeProjectTimezone(test.value, test.fallback)
			if test.wantErr {
				if !errors.Is(err, ErrProjectInvalid) {
					t.Fatalf("应返回项目参数错误，实际 %v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("时区归一化结果错误: got=%s want=%s err=%v", got, test.want, err)
			}
		})
	}
}

type memoryProjectStore struct {
	project        model.AIProject
	keys           map[uint64]authmodel.APIKey
	scopes         map[uint64][]string
	nextID         uint64
	modelsOK       bool
	realNameStatus string
}

type memoryProjectAudit struct {
	actions   []string
	summaries []interface{}
	err       error
}

func (a *memoryProjectAudit) RecordWithTx(_ context.Context, _ *gorm.DB, _ *uint64, _, action string, _, _ *string, _ string, summary any) error {
	if a.err != nil {
		return a.err
	}
	a.actions = append(a.actions, action)
	a.summaries = append(a.summaries, summary)
	return nil
}

func newMemoryProjectStore() *memoryProjectStore {
	return &memoryProjectStore{
		project: model.AIProject{ID: 9, UserID: 5, Name: "演示项目", Status: ProjectStatusActive},
		keys:    map[uint64]authmodel.APIKey{}, scopes: map[uint64][]string{}, nextID: 10, modelsOK: true, realNameStatus: "verified",
	}
}
func (s *memoryProjectStore) CreateProject(_ context.Context, project *model.AIProject) error {
	project.ID = s.project.ID
	s.project = *project
	return nil
}
func (s *memoryProjectStore) FindProject(_ context.Context, userID, projectID uint64) (*model.AIProject, error) {
	if s.project.UserID != userID || s.project.ID != projectID {
		return nil, repository.ErrProjectNotFound
	}
	copy := s.project
	return &copy, nil
}
func (s *memoryProjectStore) ListProjects(_ context.Context, userID uint64, _, _ int) ([]model.AIProject, int64, error) {
	if userID != s.project.UserID {
		return nil, 0, nil
	}
	return []model.AIProject{s.project}, 1, nil
}
func (s *memoryProjectStore) UpdateProject(_ context.Context, userID, projectID uint64, updates map[string]interface{}) error {
	if userID != s.project.UserID || projectID != s.project.ID {
		return repository.ErrProjectNotFound
	}
	if name, ok := updates["name"].(string); ok {
		s.project.Name = name
	}
	if status, ok := updates["status"].(string); ok {
		s.project.Status = status
	}
	return nil
}
func (s *memoryProjectStore) CreateProjectKey(_ context.Context, key *authmodel.APIKey, scopes []authmodel.APIKeyModelScope, audit repository.ProjectKeyAudit) error {
	key.ID = s.nextID
	if err := audit(nil, key.ID); err != nil {
		return err
	}
	s.nextID++
	key.CreatedAt = time.Now()
	s.keys[key.ID] = *key
	for _, scope := range scopes {
		s.scopes[key.ID] = append(s.scopes[key.ID], scope.LogicalModelCode)
	}
	return nil
}
func (s *memoryProjectStore) ListProjectKeys(_ context.Context, userID, projectID uint64) ([]authmodel.APIKey, error) {
	result := []authmodel.APIKey{}
	for _, key := range s.keys {
		if key.UserID == userID && key.ProjectID != nil && *key.ProjectID == projectID {
			result = append(result, key)
		}
	}
	return result, nil
}
func (s *memoryProjectStore) FindProjectKey(_ context.Context, userID, projectID, keyID uint64) (*authmodel.APIKey, error) {
	key, ok := s.keys[keyID]
	if !ok || key.UserID != userID || key.ProjectID == nil || *key.ProjectID != projectID {
		return nil, repository.ErrProjectKeyNotFound
	}
	copy := key
	return &copy, nil
}
func (s *memoryProjectStore) ListKeyScopes(_ context.Context, keyID uint64) ([]string, error) {
	return append([]string(nil), s.scopes[keyID]...), nil
}
func (s *memoryProjectStore) RevokeProjectKey(_ context.Context, userID, projectID, keyID uint64, audit repository.ProjectKeyAudit) error {
	key, err := s.FindProjectKey(context.Background(), userID, projectID, keyID)
	if err != nil {
		return err
	}
	if err := audit(nil, keyID); err != nil {
		return err
	}
	key.Status = "revoked"
	s.keys[keyID] = *key
	return nil
}
func (s *memoryProjectStore) RotateProjectKey(_ context.Context, oldKey *authmodel.APIKey, newKey *authmodel.APIKey, scopes []authmodel.APIKeyModelScope, audit repository.ProjectKeyAudit) error {
	current, ok := s.keys[oldKey.ID]
	if !ok || current.Status != "active" {
		return repository.ErrProjectKeyNotFound
	}
	newKey.ID = s.nextID
	if err := audit(nil, newKey.ID); err != nil {
		return err
	}
	s.nextID++
	newKey.CreatedAt = time.Now()
	s.keys[newKey.ID] = *newKey
	for _, scope := range scopes {
		s.scopes[newKey.ID] = append(s.scopes[newKey.ID], scope.LogicalModelCode)
	}
	current.Status = "revoked"
	s.keys[oldKey.ID] = current
	return nil
}
func (s *memoryProjectStore) ActiveChatModelsExist(_ context.Context, _ []string) (bool, error) {
	return s.modelsOK, nil
}
func (s *memoryProjectStore) UserRealNameStatus(_ context.Context, _ uint64) (string, error) {
	return s.realNameStatus, nil
}

func TestProjectKeyRequiresVerifiedRealName(t *testing.T) {
	store := newMemoryProjectStore()
	store.realNameStatus = "pending"
	projectService := NewProjectService(store, "secret").WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithAuditRecorder(&memoryProjectAudit{})
	_, _, err := projectService.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "未实名密钥"})
	if !errors.Is(err, ErrRealNameRequired) {
		t.Fatalf("未实名用户不得签发可调用 SK: %v", err)
	}
}

func TestProjectKeyDefaultsToDenyAllAllowlistAndStoresOnlyHash(t *testing.T) {
	store := newMemoryProjectStore()
	service := NewProjectService(store, "project-key-test-secret").WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithAuditRecorder(&memoryProjectAudit{})
	plaintext, view, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "默认密钥"})
	if err != nil {
		t.Fatal(err)
	}
	stored := store.keys[view.ID]
	if view.ScopeMode != ScopeModeAllowlist || len(view.ModelCodes) != 0 || len(store.scopes[view.ID]) != 0 {
		t.Fatalf("新 Project SK 必须默认空 allowlist: %+v", view)
	}
	if stored.KeyHash == "" || stored.KeyHash != crypto.HMAC256(plaintext, "project-key-test-secret") || stored.KeyHash == plaintext {
		t.Fatal("数据库必须只保存 HMAC，不能保存可恢复明文")
	}
}

func TestProjectKeyAllModeRequiresExplicitEmptyModelList(t *testing.T) {
	store := newMemoryProjectStore()
	service := NewProjectService(store, "secret").WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithAuditRecorder(&memoryProjectAudit{})
	_, _, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "全部模型密钥", ScopeMode: ScopeModeAll, ModelCodes: []string{"molin/qwen-turbo"}})
	if !errors.Is(err, ErrScopeModeInvalid) {
		t.Fatalf("all 必须由用户明确选择且不能混用 allowlist: %v", err)
	}
	_, view, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "全部模型密钥", ScopeMode: ScopeModeAll})
	if err != nil || view.ScopeMode != ScopeModeAll {
		t.Fatalf("显式 all 应签发成功: view=%+v err=%v", view, err)
	}
}

func TestProjectKeyRotateRevokesOldKeyAndReturnsSecretOnce(t *testing.T) {
	store := newMemoryProjectStore()
	service := NewProjectService(store, "rotate-secret").WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithAuditRecorder(&memoryProjectAudit{})
	oldPlaintext, oldView, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "轮换密钥", ModelCodes: []string{"molin/qwen-turbo"}})
	if err != nil {
		t.Fatal(err)
	}
	newPlaintext, newView, err := service.RotateKey(context.Background(), 5, 9, oldView.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if oldPlaintext == newPlaintext || store.keys[oldView.ID].Status != "revoked" || store.keys[newView.ID].Status != "active" {
		t.Fatal("轮换必须原子地产生新密钥并吊销旧密钥")
	}
	if newView.ScopeMode != oldView.ScopeMode || len(newView.ModelCodes) != 1 || newView.ModelCodes[0] != "molin/qwen-turbo" {
		t.Fatalf("轮换必须保留原权限: %+v", newView)
	}
}

func TestProjectKeyRejectsInactiveProjectInvalidModelAndExpiredTime(t *testing.T) {
	store := newMemoryProjectStore()
	service := NewProjectService(store, "validation-secret").WithVisibilityChecker(fakeVisibilityChecker{visible: true}).WithAuditRecorder(&memoryProjectAudit{})
	if _, _, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "  "}); !errors.Is(err, ErrKeyNameInvalid) {
		t.Fatalf("空 SK 名称必须被后端拒绝: %v", err)
	}
	store.project.Status = ProjectStatusSuspended
	if _, _, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "停用项目密钥"}); !errors.Is(err, ErrProjectInactive) {
		t.Fatalf("停用 Project 不得签发 SK: %v", err)
	}
	store.project.Status = ProjectStatusActive
	store.modelsOK = false
	if _, _, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "缺失模型密钥", ModelCodes: []string{"missing-model"}}); !errors.Is(err, ErrScopeModelInvalid) {
		t.Fatalf("不存在或非文字模型不得进入 allowlist: %v", err)
	}
	store.modelsOK = true
	service.WithVisibilityChecker(fakeVisibilityChecker{visible: false})
	if _, _, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "隐藏模型密钥", ModelCodes: []string{"hidden-model"}}); !errors.Is(err, ErrScopeModelInvalid) {
		t.Fatalf("当前用户不可见模型不得进入 allowlist: %v", err)
	}
	service.WithVisibilityChecker(fakeVisibilityChecker{visible: true})
	past := time.Now().Add(-time.Second)
	if _, _, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "过期密钥", ExpiresAt: &past}); !errors.Is(err, ErrKeyExpiresAtInvalid) {
		t.Fatalf("过期时间不得早于当前时间: %v", err)
	}
	if _, _, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 6, ProjectID: 9, Name: "越权密钥"}); !errors.Is(err, repository.ErrProjectNotFound) {
		t.Fatalf("跨用户访问 Project 必须按不存在处理: %v", err)
	}
}

func TestProjectKeyLifecycleWritesAuditWithoutPlaintext(t *testing.T) {
	store := newMemoryProjectStore()
	audit := &memoryProjectAudit{}
	service := NewProjectService(store, "audit-secret").
		WithVisibilityChecker(fakeVisibilityChecker{visible: true}).
		WithAuditRecorder(audit)
	plaintext, view, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "审计密钥", ModelCodes: []string{"molin/qwen-turbo"}, IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	rotatedPlaintext, rotated, err := service.RotateKey(context.Background(), 5, 9, view.ID, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeKey(context.Background(), 5, 9, rotated.ID, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(audit.actions, ",") != "create_project_key,rotate_project_key,revoke_project_key" {
		t.Fatalf("SK 生命周期审计不完整: %v", audit.actions)
	}
	summary := fmt.Sprint(audit.summaries)
	if strings.Contains(summary, plaintext) || strings.Contains(summary, rotatedPlaintext) || strings.Contains(summary, "audit-secret") {
		t.Fatal("审计摘要不得包含 SK 明文或 HMAC Secret")
	}
}

func TestProjectKeyLifecycleFailsClosedWhenAuditUnavailable(t *testing.T) {
	store := newMemoryProjectStore()
	service := NewProjectService(store, "audit-fail-secret").WithVisibilityChecker(fakeVisibilityChecker{visible: true})
	if _, _, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "缺少审计"}); !errors.Is(err, ErrSecurityAuditUnavailable) {
		t.Fatalf("缺少安全审计时不得签发 SK: %v", err)
	}
	if len(store.keys) != 0 {
		t.Fatal("审计不可用时不得留下 SK 事实")
	}

	failingAudit := &memoryProjectAudit{err: errors.New("审计数据库不可用")}
	service.WithAuditRecorder(failingAudit)
	if _, _, err := service.IssueKey(context.Background(), IssueProjectKeyInput{UserID: 5, ProjectID: 9, Name: "审计失败"}); !errors.Is(err, ErrSecurityAuditUnavailable) {
		t.Fatalf("审计写入失败时必须返回稳定错误: %v", err)
	}
	if len(store.keys) != 0 {
		t.Fatal("审计写入失败时 SK 事务必须回滚")
	}
}
