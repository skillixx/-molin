package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/auth/model"
	"molin/server/pkg/crypto"
)

// fakeAPIKeyRepo 内存实现，用于 service 单测，无需真实 DB。
type fakeAPIKeyRepo struct {
	rows   map[uint64]*model.APIKey
	nextID uint64
}

func newFakeAPIKeyRepo() *fakeAPIKeyRepo {
	return &fakeAPIKeyRepo{rows: map[uint64]*model.APIKey{}, nextID: 1}
}

func (f *fakeAPIKeyRepo) Create(_ context.Context, key *model.APIKey) error {
	key.ID = f.nextID
	f.nextID++
	key.CreatedAt = time.Now()
	cp := *key
	f.rows[key.ID] = &cp
	return nil
}

func (f *fakeAPIKeyRepo) FindByHash(_ context.Context, hash string) (*model.APIKey, error) {
	for _, k := range f.rows {
		if k.KeyHash == hash {
			cp := *k
			return &cp, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeAPIKeyRepo) FindByID(_ context.Context, id uint64) (*model.APIKey, error) {
	k, ok := f.rows[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *k
	return &cp, nil
}

func (f *fakeAPIKeyRepo) Revoke(_ context.Context, userID, keyID uint64) (int64, error) {
	k, ok := f.rows[keyID]
	if !ok || k.UserID != userID || k.Status != "active" {
		return 0, nil
	}
	k.Status = "revoked"
	return 1, nil
}

func (f *fakeAPIKeyRepo) TouchLastUsed(_ context.Context, keyID uint64, t time.Time) error {
	if k, ok := f.rows[keyID]; ok && k.Status == "active" {
		k.LastUsedAt = &t
	}
	return nil
}

func (f *fakeAPIKeyRepo) ListByUser(_ context.Context, userID uint64, offset, limit int) ([]model.APIKey, int64, error) {
	var all []model.APIKey
	for _, k := range f.rows {
		if k.UserID == userID {
			all = append(all, *k)
		}
	}
	total := int64(len(all))
	if offset >= len(all) {
		return []model.APIKey{}, total, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], total, nil
}

// stubBanChecker 可控的封禁查询桩。
type stubBanChecker struct{ blocked map[uint64]bool }

func (s *stubBanChecker) IsUserBlocked(_ context.Context, userID uint64) bool {
	return s.blocked[userID]
}

const testSecret = "test-api-key-hmac-secret"

// TestIssueKey_PlaintextFormatAndPrefix 验证明文 sk 格式、key_prefix 派生、HMAC 落库、明文不入库。
func TestIssueKey_PlaintextFormatAndPrefix(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	svc := NewAPIKeyService(repo, testSecret, nil)

	plaintext, view, err := svc.IssueKey(context.Background(), IssueKeyInput{
		UserID:      7,
		Name:        "my-agent",
		BillingMode: billingModePostpaid,
	})
	if err != nil {
		t.Fatalf("IssueKey 出错: %v", err)
	}

	// 明文格式：sk-molin-<32 字符随机段>。
	if !strings.HasPrefix(plaintext, skPrefix) {
		t.Fatalf("明文前缀错误: %s", plaintext)
	}
	randomSeg := strings.TrimPrefix(plaintext, skPrefix)
	if len(randomSeg) != skRandomBytes {
		t.Fatalf("随机段长度应为 %d，实际 %d", skRandomBytes, len(randomSeg))
	}

	// key_prefix = sk-molin- + 随机段前 4 位。
	wantPrefix := skPrefix + randomSeg[:skPrefixRandomLen]
	if view.KeyPrefix != wantPrefix {
		t.Fatalf("key_prefix 期望 %s，实际 %s", wantPrefix, view.KeyPrefix)
	}

	// postpaid → source_id 必为 nil。
	stored := repo.rows[view.ID]
	if stored.SourceID != nil {
		t.Fatalf("postpaid 的 source_id 应为 nil")
	}

	// DB 只存 HMAC，且等于 HMAC256(明文, secret)；绝不等于明文。
	if stored.KeyHash != crypto.HMAC256(plaintext, testSecret) {
		t.Fatalf("DB 中 key_hash 不是明文的 HMAC")
	}
	if strings.Contains(stored.KeyHash, plaintext) || stored.KeyHash == plaintext {
		t.Fatalf("DB 中不得出现明文")
	}
}

// TestIssueKey_PrepaidKeepsSourceID 验证 prepaid 模式保留 entitlement_id。
func TestIssueKey_PrepaidKeepsSourceID(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	svc := NewAPIKeyService(repo, testSecret, nil)

	ent := uint64(99)
	_, view, err := svc.IssueKey(context.Background(), IssueKeyInput{
		UserID:      7,
		BillingMode: billingModePrepaid,
		SourceID:    &ent,
		ModelScope:  []string{"gpt-4o", "claude-3"},
	})
	if err != nil {
		t.Fatalf("IssueKey 出错: %v", err)
	}
	stored := repo.rows[view.ID]
	if stored.SourceID == nil || *stored.SourceID != ent {
		t.Fatalf("prepaid 的 source_id 应为 %d", ent)
	}
	if stored.ModelScope != "gpt-4o,claude-3" {
		t.Fatalf("model_scope 应逗号分隔存储，实际 %q", stored.ModelScope)
	}
	if len(view.ModelScope) != 2 {
		t.Fatalf("视图 model_scope 应转回切片")
	}
}

// TestResolveKey_HitRevokedBanned 覆盖命中、吊销失效、封禁联动失效、无效 sk。
func TestResolveKey_HitRevokedBanned(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	ban := &stubBanChecker{blocked: map[uint64]bool{}}
	svc := NewAPIKeyService(repo, testSecret, ban)

	plaintext, view, err := svc.IssueKey(context.Background(), IssueKeyInput{UserID: 7, BillingMode: billingModePostpaid})
	if err != nil {
		t.Fatalf("IssueKey 出错: %v", err)
	}

	// 1. 命中：返回正确 userID/apiKeyID。
	auth, err := svc.ResolveKey(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("ResolveKey 应命中: %v", err)
	}
	if auth.UserID != 7 || auth.APIKeyID != view.ID {
		t.Fatalf("ResolveKey 返回不匹配: %+v", auth)
	}

	// 2. 无效 sk → ErrKeyInvalid。
	if _, err := svc.ResolveKey(context.Background(), "sk-molin-doesnotexist"); err != ErrKeyInvalid {
		t.Fatalf("无效 sk 应返回 ErrKeyInvalid，实际 %v", err)
	}

	// 3. 封禁联动：用户被封 → ErrKeyInvalid。
	ban.blocked[7] = true
	if _, err := svc.ResolveKey(context.Background(), plaintext); err != ErrKeyInvalid {
		t.Fatalf("封禁用户 sk 应返回 ErrKeyInvalid，实际 %v", err)
	}
	// 解封 → 自动恢复。
	ban.blocked[7] = false
	if _, err := svc.ResolveKey(context.Background(), plaintext); err != nil {
		t.Fatalf("解封后 sk 应恢复可用: %v", err)
	}

	// 4. 吊销后 → ErrKeyInvalid。
	if err := svc.RevokeKey(context.Background(), 7, view.ID); err != nil {
		t.Fatalf("RevokeKey 出错: %v", err)
	}
	if _, err := svc.ResolveKey(context.Background(), plaintext); err != ErrKeyInvalid {
		t.Fatalf("吊销后 sk 应返回 ErrKeyInvalid，实际 %v", err)
	}
}

// TestRevokeKey_Forbidden 验证越权吊销他人 sk 返回 ErrKeyForbidden（对应 40003）。
func TestRevokeKey_Forbidden(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	svc := NewAPIKeyService(repo, testSecret, nil)

	_, view, err := svc.IssueKey(context.Background(), IssueKeyInput{UserID: 7})
	if err != nil {
		t.Fatalf("IssueKey 出错: %v", err)
	}

	// 用户 8 试图吊销用户 7 的 sk → 越权。
	if err := svc.RevokeKey(context.Background(), 8, view.ID); err != ErrKeyForbidden {
		t.Fatalf("越权吊销应返回 ErrKeyForbidden，实际 %v", err)
	}
	// 原 sk 仍为 active。
	if repo.rows[view.ID].Status != "active" {
		t.Fatalf("越权操作不应改变 sk 状态")
	}

	// 吊销不存在的 sk → ErrKeyInvalid。
	if err := svc.RevokeKey(context.Background(), 7, 99999); err != ErrKeyInvalid {
		t.Fatalf("吊销不存在的 sk 应返回 ErrKeyInvalid，实际 %v", err)
	}
}

// TestListKeys_NoSecretLeak 验证列表只回 prefix，不含 hash/明文。
func TestListKeys_NoSecretLeak(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	svc := NewAPIKeyService(repo, testSecret, nil)

	for i := 0; i < 3; i++ {
		if _, _, err := svc.IssueKey(context.Background(), IssueKeyInput{UserID: 7}); err != nil {
			t.Fatalf("IssueKey 出错: %v", err)
		}
	}
	// 另一个用户的 sk 不应被列出。
	if _, _, err := svc.IssueKey(context.Background(), IssueKeyInput{UserID: 8}); err != nil {
		t.Fatalf("IssueKey 出错: %v", err)
	}

	views, total, err := svc.ListKeys(context.Background(), 7, 0, 20)
	if err != nil {
		t.Fatalf("ListKeys 出错: %v", err)
	}
	if total != 3 || len(views) != 3 {
		t.Fatalf("用户 7 应有 3 条 sk，实际 total=%d len=%d", total, len(views))
	}
	for _, v := range views {
		if v.KeyPrefix == "" || !strings.HasPrefix(v.KeyPrefix, skPrefix) {
			t.Fatalf("视图必须含 key_prefix，实际 %q", v.KeyPrefix)
		}
		// APIKeyView 结构本身不含 KeyHash 字段（编译期保证），此处再校验 prefix 长度可读。
		if len(v.KeyPrefix) != len(skPrefix)+skPrefixRandomLen {
			t.Fatalf("key_prefix 长度异常: %q", v.KeyPrefix)
		}
	}
}
