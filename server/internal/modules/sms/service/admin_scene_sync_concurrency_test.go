package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/sms/model"
	"molin/server/internal/modules/sms/repository"
	"molin/server/internal/modules/sms/sender"
)

type concurrentSceneRepository struct {
	mu          sync.Mutex
	template    model.Template
	bindings    map[string]model.SceneBinding
	updateCalls atomic.Int32
}

func newConcurrentSceneRepository() *concurrentSceneRepository {
	return &concurrentSceneRepository{
		template: model.Template{ID: 7, Provider: "aliyun", TemplateCode: "SMS_SAFE", TemplateType: "verification", ProviderAuditStatus: "approved", Content: "验证码 ${code}", LocalEnabled: true, Version: 1},
		bindings: make(map[string]model.SceneBinding),
	}
}

func (r *concurrentSceneRepository) GetAdminTemplate(context.Context, uint64) (*model.Template, error) {
	copyTemplate := r.template
	return &copyTemplate, nil
}

func (r *concurrentSceneRepository) UpdateAdminTemplateStatus(context.Context, uint64, uint64, bool) error {
	return nil
}

func (r *concurrentSceneRepository) ListAdminTemplates(context.Context, model.TemplateListFilter) ([]model.Template, int64, error) {
	return []model.Template{r.template}, 1, nil
}

func (r *concurrentSceneRepository) ListAdminSceneBindings(context.Context) ([]model.SceneBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	items := make([]model.SceneBinding, 0, len(r.bindings))
	for _, binding := range r.bindings {
		copyBinding := binding
		copyBinding.Template = r.template
		items = append(items, copyBinding)
	}
	return items, nil
}

func (r *concurrentSceneRepository) UpsertAdminSceneBinding(_ context.Context, scene, signName string, templateID, version, operatorID uint64, enabled bool) (*model.SceneBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if enabled {
		for boundScene, binding := range r.bindings {
			if boundScene != scene && binding.Enabled && binding.TemplateID == templateID {
				return nil, repository.ErrAdminSceneTemplateInUse
			}
		}
	}
	existing, exists := r.bindings[scene]
	if !exists {
		if version != 0 {
			return nil, repository.ErrAdminSceneConflict
		}
		now := time.Now().UTC()
		binding := model.SceneBinding{ID: uint64(len(r.bindings) + 1), Scene: scene, TemplateID: templateID, SignName: signName, Enabled: enabled, Version: 1, CreatedBy: &operatorID, UpdatedBy: &operatorID, UpdatedAt: now}
		r.bindings[scene] = binding
		r.updateCalls.Add(1)
		return &binding, nil
	}
	if existing.Version != version {
		return nil, repository.ErrAdminSceneConflict
	}
	existing.TemplateID, existing.SignName, existing.Enabled, existing.Version, existing.UpdatedBy = templateID, signName, enabled, version+1, &operatorID
	existing.UpdatedAt = time.Now().UTC()
	r.bindings[scene] = existing
	r.updateCalls.Add(1)
	return &existing, nil
}

func TestSMSAdminSceneConcurrentBindingRejectsSharedTemplate(t *testing.T) {
	repo := newConcurrentSceneRepository()
	svc := NewSMSAdminService(repo)
	svc.ConfigureTemplateSync(nil, "固定签名")

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, scene := range []string{"register", "login"} {
		go func(scene string) {
			<-start
			_, err := svc.SetScene(context.Background(), scene, 7, 0, 10, true)
			results <- err
		}(scene)
	}
	close(start)
	success, inUse := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrSMSSceneTemplateInUse):
			inUse++
		default:
			t.Fatalf("并发争用同一模板返回意外错误: %v", err)
		}
	}
	if success != 1 || inUse != 1 || repo.updateCalls.Load() != 1 {
		t.Fatalf("同一模板只能绑定一个启用场景: success=%d in_use=%d calls=%d", success, inUse, repo.updateCalls.Load())
	}
}

func TestSMSAdminSceneAllowsDisablingLegacySharedBinding(t *testing.T) {
	repo := newConcurrentSceneRepository()
	repo.bindings["register"] = model.SceneBinding{ID: 1, Scene: "register", TemplateID: 7, SignName: "固定签名", Enabled: true, Version: 1}
	repo.bindings["login"] = model.SceneBinding{ID: 2, Scene: "login", TemplateID: 7, SignName: "固定签名", Enabled: true, Version: 1}
	svc := NewSMSAdminService(repo)
	svc.ConfigureTemplateSync(nil, "固定签名")

	binding, err := svc.SetScene(context.Background(), "login", 7, 1, 10, false)
	if err != nil || binding.Enabled || binding.Version != 2 || repo.updateCalls.Load() != 1 {
		t.Fatalf("历史共用模板必须允许先停用以便整改: binding=%#v err=%v calls=%d", binding, err, repo.updateCalls.Load())
	}
}

func (r *concurrentSceneRepository) ListAdminSendLogs(context.Context, model.SendLogListFilter) ([]model.SendLog, int64, error) {
	return []model.SendLog{}, 0, nil
}

func TestSMSAdminScenesAlwaysReturnFiveFixedEntries(t *testing.T) {
	repo := newConcurrentSceneRepository()
	repo.bindings["register"] = model.SceneBinding{ID: 1, Scene: "register", TemplateID: 7, SignName: "固定签名", Enabled: true, Version: 2, Template: repo.template}
	svc := NewSMSAdminService(repo)

	items, err := svc.ListScenes(context.Background())
	if err != nil || len(items) != 5 {
		t.Fatalf("场景列表必须固定返回五项: items=%#v err=%v", items, err)
	}
	want := []string{"register", "login", "reset_password", "bind_phone", "admin_verify"}
	for index, scene := range want {
		if items[index].Scene != scene {
			t.Fatalf("固定场景顺序错误: got=%s want=%s", items[index].Scene, scene)
		}
		if scene != "register" && (items[index].TemplateID != nil || items[index].Enabled || items[index].Version != 0) {
			t.Fatalf("未绑定场景必须返回空模板、关闭态和零版本: %#v", items[index])
		}
	}
}

func TestSMSAdminSceneRejectsUnapprovedTemplateBeforeWrite(t *testing.T) {
	repo := newConcurrentSceneRepository()
	repo.template.ProviderAuditStatus = "rejected"
	svc := NewSMSAdminService(repo)
	svc.ConfigureTemplateSync(nil, "固定签名")

	_, err := svc.SetScene(context.Background(), "register", 7, 0, 10, true)
	if !errors.Is(err, ErrSMSSceneTemplateInvalid) || repo.updateCalls.Load() != 0 {
		t.Fatalf("未审核模板不得绑定: err=%v calls=%d", err, repo.updateCalls.Load())
	}
}

func TestSMSAdminSceneConcurrentCASAllowsOneWinner(t *testing.T) {
	repo := newConcurrentSceneRepository()
	repo.bindings["register"] = model.SceneBinding{ID: 1, Scene: "register", TemplateID: 7, SignName: "固定签名", Enabled: true, Version: 1}
	svc := NewSMSAdminService(repo)
	svc.ConfigureTemplateSync(nil, "固定签名")

	start := make(chan struct{})
	results := make(chan error, 2)
	for _, operatorID := range []uint64{10, 11} {
		go func(operatorID uint64) {
			<-start
			_, err := svc.SetScene(context.Background(), "register", 7, 1, operatorID, true)
			results <- err
		}(operatorID)
	}
	close(start)
	success, conflict := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			success++
		case errors.Is(err, ErrSMSSceneVersionConflict):
			conflict++
		default:
			t.Fatalf("并发绑定返回意外错误: %v", err)
		}
	}
	if success != 1 || conflict != 1 || repo.updateCalls.Load() != 1 {
		t.Fatalf("同版本并发更新必须一胜一冲突: success=%d conflict=%d calls=%d", success, conflict, repo.updateCalls.Load())
	}
}

type idempotentSyncRepository struct {
	mu      sync.Mutex
	items   map[string]model.TemplateSnapshot
	created int64
}

func (r *idempotentSyncRepository) ApplyTemplateSnapshots(_ context.Context, snapshots []model.TemplateSnapshot, syncedAt time.Time) (model.TemplateSyncResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := model.TemplateSyncResult{TotalCount: int64(len(snapshots)), LastSyncedAt: syncedAt}
	for _, snapshot := range snapshots {
		key := snapshot.Provider + "|" + snapshot.TemplateCode
		if _, exists := r.items[key]; exists {
			result.UnchangedCount++
			continue
		}
		r.items[key] = snapshot
		result.CreatedCount++
		r.created++
	}
	return result, nil
}

type countingTemplateProvider struct{ calls atomic.Int32 }

func (p *countingTemplateProvider) ListTemplates(context.Context) ([]sender.TemplateSnapshot, error) {
	p.calls.Add(1)
	return []sender.TemplateSnapshot{{Provider: "aliyun", TemplateCode: "SMS_SAFE", TemplateName: "验证码", TemplateType: "verification", Content: "验证码 ${code}", Variables: []string{"code"}, AuditStatus: "approved", SignName: "固定签名"}}, nil
}

func TestSMSAdminConcurrentSyncRemainsIdempotent(t *testing.T) {
	repo := &idempotentSyncRepository{items: make(map[string]model.TemplateSnapshot)}
	provider := &countingTemplateProvider{}
	svc := NewSMSAdminService(repo)
	svc.ConfigureTemplateSync(provider, "固定签名")

	const workers = 20
	var wait sync.WaitGroup
	wait.Add(workers)
	errorsSeen := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wait.Done()
			_, err := svc.SyncTemplates(context.Background())
			errorsSeen <- err
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("并发同步失败: %v", err)
		}
	}
	if len(repo.items) != 1 || repo.created != 1 || provider.calls.Load() != workers {
		t.Fatalf("并发同步必须只创建一个权威快照: items=%d created=%d provider=%d", len(repo.items), repo.created, provider.calls.Load())
	}
}
