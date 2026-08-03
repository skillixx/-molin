package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"molin/server/internal/modules/auth/model"
	"molin/server/pkg/crypto"
)

// fakeAPIKeyRepo 内存实现，用于 service 单测，无需真实 DB。
// 加 mu 保护 rows：ResolveKey 命中后会异步 TouchLastUsed（独立 goroutine），
// 与测试主协程的 Revoke/FindByID 并发访问同一 map，必须加锁，否则 go test -race 报数据竞争。
type fakeAPIKeyRepo struct {
	mu     sync.Mutex
	rows   map[uint64]*model.APIKey
	nextID uint64
}

// TestResolveKey_ProjectKeyExpired 验证 Project SK 到期后立即失效，且不会泄露具体失效原因。
func TestResolveKey_ProjectKeyExpired(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	svc := NewAPIKeyService(repo, "expiry-test-secret", nil)
	plaintext, view, err := svc.IssueKey(context.Background(), IssueKeyInput{UserID: 7})
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Minute)
	repo.mu.Lock()
	repo.rows[view.ID].ExpiresAt = &past
	repo.mu.Unlock()
	if _, err := svc.ResolveKey(context.Background(), plaintext); err != ErrKeyInvalid {
		t.Fatalf("过期 Project SK 必须统一返回 ErrKeyInvalid，实际: %v", err)
	}
}

func newFakeAPIKeyRepo() *fakeAPIKeyRepo {
	return &fakeAPIKeyRepo{rows: map[uint64]*model.APIKey{}, nextID: 1}
}

func (f *fakeAPIKeyRepo) Create(_ context.Context, key *model.APIKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key.ID = f.nextID
	f.nextID++
	key.CreatedAt = time.Now()
	cp := *key
	f.rows[key.ID] = &cp
	return nil
}

func (f *fakeAPIKeyRepo) FindByHash(_ context.Context, hash string) (*model.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range f.rows {
		if k.KeyHash == hash {
			cp := *k
			return &cp, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func (f *fakeAPIKeyRepo) FindByID(_ context.Context, id uint64) (*model.APIKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.rows[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	cp := *k
	return &cp, nil
}

func (f *fakeAPIKeyRepo) Revoke(_ context.Context, userID, keyID uint64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.rows[keyID]
	if !ok || k.UserID != userID || k.Status != "active" {
		return 0, nil
	}
	k.Status = "revoked"
	return 1, nil
}

func (f *fakeAPIKeyRepo) TouchLastUsed(_ context.Context, keyID uint64, t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if k, ok := f.rows[keyID]; ok && k.Status == "active" {
		k.LastUsedAt = &t
	}
	return nil
}

func (f *fakeAPIKeyRepo) ListByUser(_ context.Context, userID uint64, offset, limit int) ([]model.APIKey, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

// stubEntitlementChecker 可控的套餐权益只读校验桩（S2-甲6）。
// usable[userID] 是该用户名下可用（active + token_quota）的 entitlement_id 集合。
type stubEntitlementChecker struct {
	usable map[uint64]map[uint64]bool
}

func (s *stubEntitlementChecker) IsTokenQuotaUsable(_ context.Context, userID, entitlementID uint64) bool {
	set, ok := s.usable[userID]
	return ok && set[entitlementID]
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

// TestIssueKey_PrepaidKeepsSourceID 验证 prepaid 模式：校验通过后保留 entitlement_id 并回显 source_id。
func TestIssueKey_PrepaidKeepsSourceID(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	ent := uint64(99)
	// 用户 7 名下 entitlement 99 可用。
	checker := &stubEntitlementChecker{usable: map[uint64]map[uint64]bool{7: {99: true}}}
	svc := NewAPIKeyService(repo, testSecret, nil)
	svc.SetEntitlementChecker(checker)

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
	// 视图也应回显 source_id（前端创建后展示绑定的权益）。
	if view.SourceID == nil || *view.SourceID != ent {
		t.Fatalf("prepaid 视图 source_id 应回显 %d", ent)
	}
	if view.BillingMode != billingModePrepaid {
		t.Fatalf("视图 billing_mode 应为 prepaid，实际 %q", view.BillingMode)
	}
	if stored.ModelScope != "gpt-4o,claude-3" {
		t.Fatalf("model_scope 应逗号分隔存储，实际 %q", stored.ModelScope)
	}
	if len(view.ModelScope) != 2 {
		t.Fatalf("视图 model_scope 应转回切片")
	}
}

// TestIssueKey_PostpaidForcesNilSourceID 验证 postpaid 强制忽略传入的 source_id（防脏数据）。
func TestIssueKey_PostpaidForcesNilSourceID(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	svc := NewAPIKeyService(repo, testSecret, nil)

	ent := uint64(99)
	_, view, err := svc.IssueKey(context.Background(), IssueKeyInput{
		UserID:      7,
		BillingMode: billingModePostpaid,
		SourceID:    &ent, // 即使传入也应被强制丢弃
	})
	if err != nil {
		t.Fatalf("IssueKey 出错: %v", err)
	}
	if repo.rows[view.ID].SourceID != nil {
		t.Fatalf("postpaid 应强制 source_id=nil，实际 %v", *repo.rows[view.ID].SourceID)
	}
	if view.SourceID != nil {
		t.Fatalf("postpaid 视图 source_id 应为 nil")
	}
}

// TestIssueKey_DefaultPostpaid 验证不传 billing_mode 时默认 postpaid（M1 兼容）。
func TestIssueKey_DefaultPostpaid(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	svc := NewAPIKeyService(repo, testSecret, nil)

	_, view, err := svc.IssueKey(context.Background(), IssueKeyInput{UserID: 7})
	if err != nil {
		t.Fatalf("IssueKey 出错: %v", err)
	}
	if view.BillingMode != billingModePostpaid {
		t.Fatalf("不传 billing_mode 应默认 postpaid，实际 %q", view.BillingMode)
	}
	if repo.rows[view.ID].SourceID != nil {
		t.Fatalf("默认 postpaid 的 source_id 应为 nil")
	}
}

// TestIssueKey_InvalidBillingMode 验证非法 billing_mode 被拒绝。
func TestIssueKey_InvalidBillingMode(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	svc := NewAPIKeyService(repo, testSecret, nil)

	_, _, err := svc.IssueKey(context.Background(), IssueKeyInput{
		UserID:      7,
		BillingMode: "free", // 非法
	})
	if err != ErrInvalidBillingMode {
		t.Fatalf("非法 billing_mode 应返回 ErrInvalidBillingMode，实际 %v", err)
	}
	if len(repo.rows) != 0 {
		t.Fatalf("非法入参不应落库任何 sk")
	}
}

// TestIssueKey_PrepaidSourceIDRequired 验证 prepaid 缺 source_id（nil 或 0）被拒绝。
func TestIssueKey_PrepaidSourceIDRequired(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	checker := &stubEntitlementChecker{usable: map[uint64]map[uint64]bool{7: {99: true}}}
	svc := NewAPIKeyService(repo, testSecret, nil)
	svc.SetEntitlementChecker(checker)

	// source_id = nil。
	if _, _, err := svc.IssueKey(context.Background(), IssueKeyInput{
		UserID:      7,
		BillingMode: billingModePrepaid,
	}); err != ErrSourceIDRequired {
		t.Fatalf("prepaid 缺 source_id 应返回 ErrSourceIDRequired，实际 %v", err)
	}
	// source_id = 0（也视为未提供）。
	zero := uint64(0)
	if _, _, err := svc.IssueKey(context.Background(), IssueKeyInput{
		UserID:      7,
		BillingMode: billingModePrepaid,
		SourceID:    &zero,
	}); err != ErrSourceIDRequired {
		t.Fatalf("prepaid source_id=0 应返回 ErrSourceIDRequired，实际 %v", err)
	}
	if len(repo.rows) != 0 {
		t.Fatalf("校验失败不应落库")
	}
}

// TestIssueKey_PrepaidEntitlementNotOwned 验证 prepaid 绑定他人/不存在/不可用权益被拒绝（越权）。
func TestIssueKey_PrepaidEntitlementNotOwned(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	// 用户 7 名下只有 entitlement 99 可用；用户 8 名下有 200。
	checker := &stubEntitlementChecker{usable: map[uint64]map[uint64]bool{
		7: {99: true},
		8: {200: true},
	}}
	svc := NewAPIKeyService(repo, testSecret, nil)
	svc.SetEntitlementChecker(checker)

	// 用户 7 试图绑定用户 8 的 entitlement 200 → 越权拒绝。
	other := uint64(200)
	if _, _, err := svc.IssueKey(context.Background(), IssueKeyInput{
		UserID:      7,
		BillingMode: billingModePrepaid,
		SourceID:    &other,
	}); err != ErrEntitlementNotOwned {
		t.Fatalf("绑定他人权益应返回 ErrEntitlementNotOwned，实际 %v", err)
	}

	// 绑定不存在的 entitlement → 拒绝。
	ghost := uint64(99999)
	if _, _, err := svc.IssueKey(context.Background(), IssueKeyInput{
		UserID:      7,
		BillingMode: billingModePrepaid,
		SourceID:    &ghost,
	}); err != ErrEntitlementNotOwned {
		t.Fatalf("绑定不存在权益应返回 ErrEntitlementNotOwned，实际 %v", err)
	}
	if len(repo.rows) != 0 {
		t.Fatalf("校验失败不应落库")
	}
}

// TestIssueKey_PrepaidWithoutCheckerRejected 验证未注入校验器时 prepaid 安全失败（不放行无法核验的 sk）。
func TestIssueKey_PrepaidWithoutCheckerRejected(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	svc := NewAPIKeyService(repo, testSecret, nil) // 未 SetEntitlementChecker

	ent := uint64(99)
	if _, _, err := svc.IssueKey(context.Background(), IssueKeyInput{
		UserID:      7,
		BillingMode: billingModePrepaid,
		SourceID:    &ent,
	}); err != ErrEntitlementNotOwned {
		t.Fatalf("无校验器时 prepaid 应安全失败返回 ErrEntitlementNotOwned，实际 %v", err)
	}
}

// TestResolveKey_CarriesBillingModeAndSourceID 验证 ResolveKey 结果带出 billing_mode 和 source_id（门面丁5 用）。
func TestResolveKey_CarriesBillingModeAndSourceID(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	ent := uint64(99)
	checker := &stubEntitlementChecker{usable: map[uint64]map[uint64]bool{7: {99: true}}}
	svc := NewAPIKeyService(repo, testSecret, nil)
	svc.SetEntitlementChecker(checker)

	plaintext, _, err := svc.IssueKey(context.Background(), IssueKeyInput{
		UserID:      7,
		BillingMode: billingModePrepaid,
		SourceID:    &ent,
	})
	if err != nil {
		t.Fatalf("IssueKey 出错: %v", err)
	}
	auth, err := svc.ResolveKey(context.Background(), plaintext)
	if err != nil {
		t.Fatalf("ResolveKey 应命中: %v", err)
	}
	if auth.BillingMode != billingModePrepaid {
		t.Fatalf("ResolveKey 应带出 billing_mode=prepaid，实际 %q", auth.BillingMode)
	}
	if auth.SourceID == nil || *auth.SourceID != ent {
		t.Fatalf("ResolveKey 应带出 source_id=%d，实际 %v", ent, auth.SourceID)
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

// TestBillingByID 覆盖只读访问器在四种场景下的返回（S2-甲6b）：
// prepaid 带 source_id / postpaid（source_id=0）/ 已吊销（ok=false）/ apiKeyID=0（ok=false）。
func TestBillingByID(t *testing.T) {
	repo := newFakeAPIKeyRepo()
	svc := NewAPIKeyService(repo, testSecret, nil)
	ctx := context.Background()

	src := uint64(77)
	// prepaid + active，source_id=77。
	repo.rows[10] = &model.APIKey{ID: 10, UserID: 7, Status: "active", BillingMode: "prepaid", SourceID: &src}
	// postpaid + active，source_id=NULL。
	repo.rows[20] = &model.APIKey{ID: 20, UserID: 7, Status: "active", BillingMode: "postpaid", SourceID: nil}
	// prepaid 但已吊销 → 视为失效。
	repo.rows[30] = &model.APIKey{ID: 30, UserID: 7, Status: "revoked", BillingMode: "prepaid", SourceID: &src}

	cases := []struct {
		name     string
		id       uint64
		wantMode string
		wantSrc  uint64
		wantOK   bool
	}{
		{"prepaid 命中带 source_id", 10, "prepaid", 77, true},
		{"postpaid 命中 source_id=0", 20, "postpaid", 0, true},
		{"已吊销 ok=false", 30, "", 0, false},
		{"不存在 ok=false", 999, "", 0, false},
		{"apiKeyID=0 ok=false", 0, "", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mode, srcID, ok := svc.BillingByID(ctx, c.id)
			if ok != c.wantOK || mode != c.wantMode || srcID != c.wantSrc {
				t.Fatalf("BillingByID(%d) = (%q,%d,%v)，期望 (%q,%d,%v)",
					c.id, mode, srcID, ok, c.wantMode, c.wantSrc, c.wantOK)
			}
		})
	}
}
