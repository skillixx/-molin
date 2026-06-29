package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/app/dto"
	appmodel "molin/server/internal/modules/app/model"
	assetmodel "molin/server/internal/modules/asset/model"
	productmodel "molin/server/internal/modules/product/model"
)

// memTicketStore 内存版票据存储，GETDEL 用互斥锁保证「取出即删除」的原子性，
// 用于在无 Redis 的环境下验证一次性 / 防重放语义。
type memTicketStore struct {
	mu   sync.Mutex
	data map[string]memEntry
}

type memEntry struct {
	value    string
	expireAt time.Time
}

func newMemTicketStore() *memTicketStore {
	return &memTicketStore{data: make(map[string]memEntry)}
}

func (m *memTicketStore) Set(_ context.Context, key, value string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = memEntry{value: value, expireAt: time.Now().Add(ttl)}
	return nil
}

func (m *memTicketStore) GetDel(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[key]
	if !ok {
		return "", ErrTicketInvalid
	}
	delete(m.data, key) // 原子：取出即删除，杜绝二次消费
	if time.Now().After(e.expireAt) {
		return "", ErrTicketInvalid
	}
	return e.value, nil
}

// seedTicket 直接往内存存储写入一张绑定指定身份的票据，返回票据明文。
func seedTicket(store *memTicketStore, claims dto.LaunchClaims) string {
	ticket, _ := generateTicket()
	payload, _ := json.Marshal(claims)
	_ = store.Set(context.Background(), launchTicketKeyPrefix+ticket, string(payload), launchTicketTTL)
	return ticket
}

// TestVerifyTicket_SuccessThenReplayRejected 首次校验成功并返回正确身份，二次校验（重放）必被拒。
func TestVerifyTicket_SuccessThenReplayRejected(t *testing.T) {
	store := newMemTicketStore()
	svc := newLaunchServiceWithStore(nil, store) // VerifyTicket 不触碰 DB
	want := dto.LaunchClaims{UserID: 1001, AppID: 7, ProductID: 100}
	ticket := seedTicket(store, want)

	got, err := svc.VerifyTicket(context.Background(), ticket)
	if err != nil {
		t.Fatalf("首次校验应成功，却返回错误: %v", err)
	}
	if *got != want {
		t.Fatalf("身份不符：want %+v got %+v", want, *got)
	}

	// 二次校验：票据已被消费，必拒
	if _, err := svc.VerifyTicket(context.Background(), ticket); err != ErrTicketInvalid {
		t.Fatalf("重放应返回 ErrTicketInvalid，实际: %v", err)
	}
}

// TestVerifyTicket_MissingAndEmpty 不存在 / 空票据均判无效。
func TestVerifyTicket_MissingAndEmpty(t *testing.T) {
	svc := newLaunchServiceWithStore(nil, newMemTicketStore())
	if _, err := svc.VerifyTicket(context.Background(), ""); err != ErrTicketInvalid {
		t.Fatalf("空票据应 ErrTicketInvalid，实际: %v", err)
	}
	if _, err := svc.VerifyTicket(context.Background(), "lt_does_not_exist"); err != ErrTicketInvalid {
		t.Fatalf("不存在票据应 ErrTicketInvalid，实际: %v", err)
	}
}

// TestVerifyTicket_CorruptPayload 存储内容损坏时等同票据无效，不 panic。
func TestVerifyTicket_CorruptPayload(t *testing.T) {
	store := newMemTicketStore()
	svc := newLaunchServiceWithStore(nil, store)
	ticket, _ := generateTicket()
	_ = store.Set(context.Background(), launchTicketKeyPrefix+ticket, "not-json", launchTicketTTL)
	if _, err := svc.VerifyTicket(context.Background(), ticket); err != ErrTicketInvalid {
		t.Fatalf("损坏内容应 ErrTicketInvalid，实际: %v", err)
	}
}

// TestVerifyTicket_ConcurrentReplay 同一票据并发校验，必须恰好只有一个成功（防重放核心保证）。
func TestVerifyTicket_ConcurrentReplay(t *testing.T) {
	store := newMemTicketStore()
	svc := newLaunchServiceWithStore(nil, store)
	ticket := seedTicket(store, dto.LaunchClaims{UserID: 1, AppID: 2, ProductID: 3})

	const n = 64
	var success int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := svc.VerifyTicket(context.Background(), ticket); err == nil {
				atomic.AddInt64(&success, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if success != 1 {
		t.Fatalf("并发重放下应恰好 1 次成功，实际 %d 次", success)
	}
}

// TestGenerateTicket 票据带 lt_ 前缀、足够长、且不重复。
func TestGenerateTicket(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		tk, err := generateTicket()
		if err != nil {
			t.Fatalf("生成票据失败: %v", err)
		}
		if !strings.HasPrefix(tk, launchTicketPrefix) {
			t.Fatalf("票据应以 %q 开头: %s", launchTicketPrefix, tk)
		}
		if len(tk) != len(launchTicketPrefix)+32 {
			t.Fatalf("票据长度异常: %s", tk)
		}
		if seen[tk] {
			t.Fatalf("票据重复: %s", tk)
		}
		seen[tk] = true
	}
}

// ============ DB 集成测试（IssueTicket 使用权链路，需真实 MySQL）============

// 测试专用高位 ID，避免与真实业务数据冲突。
const (
	ltTestUserOwner   uint64 = 9_900_100_001 // 持有 active 资产
	ltTestUserNoRight uint64 = 9_900_100_002 // 无资产
	ltTestAppID       uint64 = 9_900_100_010
	ltTestProductID   uint64 = 9_900_100_020
	ltTestOtherAppID     uint64 = 9_900_100_011 // 「他应用」用例：另一应用 ID
	ltTestOtherProductID uint64 = 9_900_100_021 // 「他应用」商品（business_ref_id 指向 ltTestOtherAppID）
)

// setupLaunchDBTest 连本地 molin 开发库，按测试专用高位 ID 清理自己写入的行。
// 仅在 RUN_DB_TESTS=1 时运行（默认 SKIP，保持 CI 无需 MySQL）。
func setupLaunchDBTest(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	if os.Getenv("RUN_DB_TESTS") != "1" {
		t.Skip("跳过 DB 集成测试（设置 RUN_DB_TESTS=1 且本地 MySQL 13306 可用时运行）")
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		envOrLaunch("TEST_MYSQL_USER", "molin"),
		envOrLaunch("TEST_MYSQL_PASSWORD", "molin_password"),
		envOrLaunch("TEST_MYSQL_HOST", "127.0.0.1"),
		envOrLaunch("TEST_MYSQL_PORT", "13306"),
		envOrLaunch("TEST_MYSQL_DATABASE", "molin"),
	)
	gdb, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("GORM 连接 molin 库失败: %v", err)
	}
	clean := func() {
		gdb.Where("user_id IN ?", []uint64{ltTestUserOwner, ltTestUserNoRight}).Delete(&assetmodel.UserAsset{})
		gdb.Where("id = ?", ltTestProductID).Delete(&productmodel.Product{})
		gdb.Where("id = ?", ltTestAppID).Delete(&appmodel.Application{})
	}
	clean()
	return gdb, clean
}

func envOrLaunch(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// seedLaunchFixtures 建一个 active 应用（配 access_url）+ 对应 application 商品 + owner 的 active 资产。
func seedLaunchFixtures(t *testing.T, gdb *gorm.DB, appStatus string, accessURL *string) {
	t.Helper()
	if err := gdb.Create(&appmodel.Application{
		ID: ltTestAppID, Code: fmt.Sprintf("lt-test-app-%d", ltTestAppID),
		Name: "Launch 测试应用", Type: "saas", AccessURL: accessURL, Status: appStatus,
	}).Error; err != nil {
		t.Fatalf("建应用失败: %v", err)
	}
	ref := ltTestAppID
	if err := gdb.Create(&productmodel.Product{
		ID: ltTestProductID, ProductType: "application",
		ProductCode: fmt.Sprintf("lt-test-prod-%d", ltTestProductID),
		Name:        "Launch 测试商品", Status: "active", BusinessRefID: &ref,
	}).Error; err != nil {
		t.Fatalf("建商品失败: %v", err)
	}
	if err := gdb.Create(&assetmodel.UserAsset{
		UserID: ltTestUserOwner, AssetType: "application", ProductID: ltTestProductID, Status: "active",
	}).Error; err != nil {
		t.Fatalf("建资产失败: %v", err)
	}
}

// TestIssueTicket_DBRoundTrip 持有 active 资产的用户可签发票据并校验换回正确身份；无资产用户被拒。
func TestIssueTicket_DBRoundTrip(t *testing.T) {
	gdb, clean := setupLaunchDBTest(t)
	defer clean()
	access := "https://ppt.example.com"
	seedLaunchFixtures(t, gdb, AppStatusActive, &access)

	svc := newLaunchServiceWithStore(gdb, newMemTicketStore())
	ctx := context.Background()

	// owner：未指定 entitlement_id（兼容路径）签发成功，claims.EntitlementID 应为 0
	res, err := svc.IssueTicket(ctx, ltTestUserOwner, ltTestAppID, 0)
	if err != nil {
		t.Fatalf("owner 应能签发票据，实际错误: %v", err)
	}
	if res.AccessURL != access || res.ExpiresIn != 60 || !strings.HasPrefix(res.LaunchTicket, launchTicketPrefix) {
		t.Fatalf("票据响应字段异常: %+v", res)
	}
	// 校验换回身份
	claims, err := svc.VerifyTicket(ctx, res.LaunchTicket)
	if err != nil {
		t.Fatalf("校验票据失败: %v", err)
	}
	if claims.UserID != ltTestUserOwner || claims.AppID != ltTestAppID || claims.ProductID != ltTestProductID || claims.EntitlementID != 0 {
		t.Fatalf("身份不符: %+v", claims)
	}

	// 无资产用户：拒签
	if _, err := svc.IssueTicket(ctx, ltTestUserNoRight, ltTestAppID, 0); err != ErrNoUseRight {
		t.Fatalf("无资产用户应 ErrNoUseRight，实际: %v", err)
	}
}

// TestIssueTicket_SelectedEntitlement 用户显式选择某权益（多套餐）：
// 合法 entitlement_id 精确绑定并透传；他人/他应用/父资产冻结/不存在的 entitlement_id 一律拒签。
func TestIssueTicket_SelectedEntitlement(t *testing.T) {
	gdb, clean := setupLaunchDBTest(t)
	defer clean()
	access := "https://ppt.example.com"
	seedLaunchFixtures(t, gdb, AppStatusActive, &access)

	// mkEnt 为 owner 建一条父资产（指定状态）+ 挂一条 active 权益，返回 entitlement id。
	mkEnt := func(t *testing.T, productID uint64, assetStatus string) uint64 {
		t.Helper()
		asset := assetmodel.UserAsset{UserID: ltTestUserOwner, AssetType: "application", ProductID: productID, Status: assetStatus}
		if err := gdb.Create(&asset).Error; err != nil {
			t.Fatalf("建资产失败: %v", err)
		}
		ent := assetmodel.UserEntitlement{UserID: ltTestUserOwner, AssetID: asset.ID, ProductID: productID, EntitlementType: "token", Status: "active"}
		if err := gdb.Create(&ent).Error; err != nil {
			t.Fatalf("建权益失败: %v", err)
		}
		t.Cleanup(func() { gdb.Where("id = ?", ent.ID).Delete(&assetmodel.UserEntitlement{}) })
		return ent.ID
	}

	// 另建一个「他应用」商品（business_ref_id 指向别的 app），用于跨应用越权用例。
	otherRef := ltTestOtherAppID
	if err := gdb.Create(&productmodel.Product{
		ID: ltTestOtherProductID, ProductType: "application",
		ProductCode: fmt.Sprintf("lt-test-other-prod-%d", ltTestOtherProductID),
		Name:        "Launch 测试他应用商品", Status: "active", BusinessRefID: &otherRef,
	}).Error; err != nil {
		t.Fatalf("建他应用商品失败: %v", err)
	}
	t.Cleanup(func() { gdb.Where("id = ?", ltTestOtherProductID).Delete(&productmodel.Product{}) })

	entOK := mkEnt(t, ltTestProductID, "active")             // 合法：本应用 + 父资产 active
	entOtherApp := mkEnt(t, ltTestOtherProductID, "active")  // 本人持有、active，但商品挂在别的 app
	entFrozen := mkEnt(t, ltTestProductID, "suspended")      // 本应用，但父资产被冻结

	svc := newLaunchServiceWithStore(gdb, newMemTicketStore())
	ctx := context.Background()

	// 合法选择：票据带回 entitlement_id 与反推的 product_id
	res, err := svc.IssueTicket(ctx, ltTestUserOwner, ltTestAppID, entOK)
	if err != nil {
		t.Fatalf("合法选择应能签发，实际错误: %v", err)
	}
	claims, err := svc.VerifyTicket(ctx, res.LaunchTicket)
	if err != nil {
		t.Fatalf("校验票据失败: %v", err)
	}
	if claims.EntitlementID != entOK || claims.ProductID != ltTestProductID {
		t.Fatalf("应精确绑定所选权益，实际: %+v", claims)
	}

	// 他应用的权益（本人持有、active，但商品挂在别的 app）→ 拒签
	if _, err := svc.IssueTicket(ctx, ltTestUserOwner, ltTestAppID, entOtherApp); err != ErrEntitlementInvalid {
		t.Fatalf("他应用权益应 ErrEntitlementInvalid，实际: %v", err)
	}

	// 父资产被冻结（suspended，未级联到 entitlement）→ 拒签，与 fallback 口径对齐，防绕过冻结
	if _, err := svc.IssueTicket(ctx, ltTestUserOwner, ltTestAppID, entFrozen); err != ErrEntitlementInvalid {
		t.Fatalf("父资产冻结应 ErrEntitlementInvalid，实际: %v", err)
	}

	// 越权：他人持有的 entitlement_id（无资产用户带 owner 的权益）→ 拒签
	if _, err := svc.IssueTicket(ctx, ltTestUserNoRight, ltTestAppID, entOK); err != ErrEntitlementInvalid {
		t.Fatalf("越权携带他人权益应 ErrEntitlementInvalid，实际: %v", err)
	}

	// 不存在的 entitlement_id → 拒签
	if _, err := svc.IssueTicket(ctx, ltTestUserOwner, ltTestAppID, 9_900_199_999); err != ErrEntitlementInvalid {
		t.Fatalf("不存在权益应 ErrEntitlementInvalid，实际: %v", err)
	}
}

// TestIssueTicket_NotLaunchable 应用未上架或未配 access_url 时拒签。
func TestIssueTicket_NotLaunchable(t *testing.T) {
	gdb, clean := setupLaunchDBTest(t)
	defer clean()

	// 情形一：应用 inactive（即便有 access_url）
	access := "https://ppt.example.com"
	seedLaunchFixtures(t, gdb, AppStatusInactive, &access)
	svc := newLaunchServiceWithStore(gdb, newMemTicketStore())
	if _, err := svc.IssueTicket(context.Background(), ltTestUserOwner, ltTestAppID, 0); err != ErrAppNotLaunchable {
		t.Fatalf("未上架应用应 ErrAppNotLaunchable，实际: %v", err)
	}
	clean()

	// 情形二：应用 active 但未配 access_url
	seedLaunchFixtures(t, gdb, AppStatusActive, nil)
	if _, err := svc.IssueTicket(context.Background(), ltTestUserOwner, ltTestAppID, 0); err != ErrAppNotLaunchable {
		t.Fatalf("未配入口应用应 ErrAppNotLaunchable，实际: %v", err)
	}
}
