// 后端丙 PR#151 缺陷修复连真库回归测试（service/repository 层）。
// 直接连本地开发库（127.0.0.1:13306），验证 C-FIX-1 / C-FIX-2a / C-FIX-5 / C-FIX-6。
// 用法：go run ./tests/cfix
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"gorm.io/gorm"

	pkgdb "molin/server/pkg/db"

	assetmodel "molin/server/internal/modules/asset/model"
	assetservice "molin/server/internal/modules/asset/service"
	contentmodel "molin/server/internal/modules/content/model"
	contentrepo "molin/server/internal/modules/content/repository"
	membermodel "molin/server/internal/modules/membership/model"
	memberrepo "molin/server/internal/modules/membership/repository"
	memberservice "molin/server/internal/modules/membership/service"
)

var (
	pass   int
	fail   int
	failed []string
)

func check(name string, cond bool, detail string) {
	if cond {
		pass++
		fmt.Printf("  [PASS] %s\n", name)
	} else {
		fail++
		failed = append(failed, name+" -> "+detail)
		fmt.Printf("  [FAIL] %s\n     证据: %s\n", name, detail)
	}
}

func mustExec(db *gorm.DB, sql string, args ...interface{}) {
	if err := db.Exec(sql, args...).Error; err != nil {
		fmt.Printf("     [setup error] %s : %v\n", sql, err)
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	host := env("MYSQL_HOST", "127.0.0.1")
	port := env("MYSQL_PORT", "13306")
	user := env("MYSQL_USER", "molin")
	pwd := env("MYSQL_PASSWORD", "molin_password")
	dbname := env("MYSQL_DATABASE", "molin")

	db, err := pkgdb.New(host, port, user, pwd, dbname)
	if err != nil {
		fmt.Printf("连接数据库失败: %v\n", err)
		os.Exit(2)
	}
	ctx := context.Background()

	// 测试用 ID 前缀，避免与现有数据冲突。使用高位 user_id。
	const tUser1 = uint64(990001)
	const tUser2 = uint64(990002)
	const tUser3 = uint64(990003)

	cleanup(db, []uint64{tUser1, tUser2, tUser3})
	defer cleanup(db, []uint64{tUser1, tUser2, tUser3})

	// 准备一个 active 会员等级
	levelID := ensureLevel(db, "cfix_test_level")
	levelID2 := ensureLevel(db, "cfix_test_level2")
	inactiveLevelID := ensureInactiveLevel(db, "cfix_inactive_level")

	fmt.Println("\n========== C-FIX-1 会员续期 ==========")
	testCFix1(ctx, db, tUser1, tUser2, tUser3, levelID, levelID2, inactiveLevelID)

	fmt.Println("\n========== C-FIX-2a 资产取消（service 层） ==========")
	testCFix2a(ctx, db)

	fmt.Println("\n========== C-FIX-5 会员到期任务 BatchExpire ==========")
	testCFix5(ctx, db, tUser1, levelID2)

	fmt.Println("\n========== C-FIX-6 公告可见性 + 分页（真库 JSON_CONTAINS） ==========")
	testCFix6(ctx, db)

	fmt.Printf("\n========== service 层小结：PASS=%d FAIL=%d ==========\n", pass, fail)
	if fail > 0 {
		fmt.Println("失败项：")
		for _, f := range failed {
			fmt.Println("  -", f)
		}
		os.Exit(1)
	}
}

func cleanup(db *gorm.DB, users []uint64) {
	for _, u := range users {
		db.Exec("DELETE FROM user_memberships WHERE user_id = ?", u)
	}
	db.Exec("DELETE FROM membership_levels WHERE level_code LIKE 'cfix_%'")
	// 资产测试数据
	db.Exec("DELETE FROM asset_events WHERE user_id = 991000")
	db.Exec("DELETE FROM user_entitlements WHERE user_id = 991000")
	db.Exec("DELETE FROM user_assets WHERE user_id = 991000")
	// 公告测试数据
	db.Exec("DELETE FROM announcements WHERE title LIKE 'CFIX6_%'")
}

func ensureLevel(db *gorm.DB, code string) uint64 {
	var lvl membermodel.MembershipLevel
	if err := db.Where("level_code = ?", code).First(&lvl).Error; err == nil {
		db.Model(&lvl).Update("status", "active")
		return lvl.ID
	}
	lvl = membermodel.MembershipLevel{LevelCode: code, Name: code, Status: "active"}
	db.Create(&lvl)
	return lvl.ID
}

func ensureInactiveLevel(db *gorm.DB, code string) uint64 {
	lvl := membermodel.MembershipLevel{LevelCode: code, Name: code, Status: "inactive"}
	db.Create(&lvl)
	return lvl.ID
}

func countActive(db *gorm.DB, userID, levelID uint64) int64 {
	var c int64
	db.Model(&membermodel.UserMembership{}).
		Where("user_id = ? AND level_id = ? AND status = 'active'", userID, levelID).Count(&c)
	return c
}

func testCFix1(ctx context.Context, db *gorm.DB, u1, u2, u3, levelID, levelID2, inactiveLevelID uint64) {
	svc := memberservice.NewMembershipService(db)
	repo := memberrepo.NewUserMembershipRepository(db)

	// 1) 首次开通 30 天
	d30 := 30
	if err := svc.CreateOrRenewMembership(ctx, u1, levelID, 0, &d30); err != nil {
		check("首次开通会员成功", false, "err="+err.Error())
	} else {
		check("首次开通会员成功", true, "")
	}
	check("首次开通后仅 1 条 active", countActive(db, u1, levelID) == 1,
		fmt.Sprintf("active 数=%d", countActive(db, u1, levelID)))

	var m1 membermodel.UserMembership
	db.Where("user_id=? AND level_id=? AND status='active'", u1, levelID).First(&m1)
	firstExpiry := m1.ExpiresAt

	// 2) 同 user 同 level 再续 30 天 -> 应叠加到约 60 天，仍仅 1 条 active
	if err := svc.CreateOrRenewMembership(ctx, u1, levelID, 0, &d30); err != nil {
		check("续期会员成功", false, "err="+err.Error())
	} else {
		check("续期会员成功", true, "")
	}
	check("续期后仍仅 1 条 active（不新增第二条）", countActive(db, u1, levelID) == 1,
		fmt.Sprintf("active 数=%d", countActive(db, u1, levelID)))

	var m2 membermodel.UserMembership
	db.Where("user_id=? AND level_id=? AND status='active'", u1, levelID).First(&m2)
	if firstExpiry != nil && m2.ExpiresAt != nil {
		diff := m2.ExpiresAt.Sub(*firstExpiry)
		// 叠加约 30 天（允许少量误差）
		check("续期 expires_at 在原基础上叠加 ~30 天",
			diff > 29*24*time.Hour && diff < 31*24*time.Hour,
			fmt.Sprintf("叠加间隔=%v (期望~720h)", diff))
	} else {
		check("续期 expires_at 在原基础上叠加 ~30 天", false, "expires_at 为 nil")
	}
	check("续期复用同一条记录 id", m1.ID == m2.ID,
		fmt.Sprintf("first.id=%d renew.id=%d", m1.ID, m2.ID))

	// 3) durationDays=nil -> 升级永久（expires_at = NULL）
	if err := svc.CreateOrRenewMembership(ctx, u1, levelID, 0, nil); err != nil {
		check("升级永久会员成功", false, "err="+err.Error())
	} else {
		check("升级永久会员成功", true, "")
	}
	var m3 membermodel.UserMembership
	db.Where("user_id=? AND level_id=? AND status='active'", u1, levelID).First(&m3)
	check("永久会员 expires_at = NULL", m3.ExpiresAt == nil,
		fmt.Sprintf("expires_at=%v", m3.ExpiresAt))
	check("升级永久仍仅 1 条 active", countActive(db, u1, levelID) == 1,
		fmt.Sprintf("active 数=%d", countActive(db, u1, levelID)))

	// 4) 旧到期记录不影响新建：先插一条 expired 记录，再开通，应新建 active 而非碰旧 expired
	exp := time.Now().Add(-48 * time.Hour)
	mustExec(db, "INSERT INTO user_memberships (user_id, level_id, status, started_at, expires_at, created_at, updated_at) VALUES (?,?,?,?,?,NOW(),NOW())",
		u2, levelID, "expired", time.Now().Add(-72*time.Hour), exp)
	if err := svc.CreateOrRenewMembership(ctx, u2, levelID, 0, &d30); err != nil {
		check("旧到期记录存在时新开通成功", false, "err="+err.Error())
	} else {
		check("旧到期记录存在时新开通成功", true, "")
	}
	check("新开通生成 1 条 active（旧 expired 不被复用）", countActive(db, u2, levelID) == 1,
		fmt.Sprintf("active 数=%d", countActive(db, u2, levelID)))
	var expiredCnt int64
	db.Model(&membermodel.UserMembership{}).Where("user_id=? AND level_id=? AND status='expired'", u2, levelID).Count(&expiredCnt)
	check("旧 expired 记录保持 expired", expiredCnt == 1, fmt.Sprintf("expired 数=%d", expiredCnt))

	// 5) FindActive 排序：永久优先、最晚到期优先
	// 给 u3 在两个等级各建一条：level2 带到期(later)、level 永久
	d10 := 10
	svc.CreateOrRenewMembership(ctx, u3, levelID2, 0, &d10)   // 带到期
	svc.CreateOrRenewMembership(ctx, u3, levelID, 0, nil)     // 永久
	active, err := repo.FindActive(ctx, u3)
	if err != nil || active == nil {
		check("FindActive 永久优先", false, fmt.Sprintf("err=%v active=%v", err, active))
	} else {
		check("FindActive 永久优先返回 expires_at=NULL 记录", active.ExpiresAt == nil,
			fmt.Sprintf("返回 level=%d expires_at=%v", active.LevelID, active.ExpiresAt))
	}

	// 6) 等级停用时拒绝开通
	err = svc.CreateOrRenewMembership(ctx, u1, inactiveLevelID, 0, &d30)
	check("停用等级开通被拒绝", err != nil, fmt.Sprintf("err=%v", err))
}

func testCFix2a(ctx context.Context, db *gorm.DB) {
	const u = uint64(991000)
	svc := assetservice.NewAssetService(db)

	mkAsset := func(status string) uint64 {
		a := assetmodel.UserAsset{UserID: u, AssetType: "instance", ProductID: 1, Status: status}
		db.Create(&a)
		ent := assetmodel.UserEntitlement{UserID: u, AssetID: a.ID, EntitlementType: "quota", ProductID: 1, Status: "active"}
		db.Create(&ent)
		return a.ID
	}

	// active -> cancelled
	aid := mkAsset("active")
	if err := svc.CancelAsset(ctx, aid, 1, "test-cancel"); err != nil {
		check("active 资产取消成功", false, "err="+err.Error())
	} else {
		check("active 资产取消成功", true, "")
	}
	var a assetmodel.UserAsset
	db.First(&a, aid)
	check("资产状态变为 cancelled", a.Status == "cancelled", "status="+a.Status)
	var entCnt int64
	db.Model(&assetmodel.UserEntitlement{}).Where("asset_id=? AND status='cancelled'", aid).Count(&entCnt)
	check("关联权益同步 cancelled", entCnt == 1, fmt.Sprintf("cancelled 权益数=%d", entCnt))
	var evCnt int64
	db.Model(&assetmodel.AssetEvent{}).Where("asset_id=? AND event_type='cancelled'", aid).Count(&evCnt)
	check("写入 asset_events event_type=cancelled", evCnt == 1, fmt.Sprintf("事件数=%d", evCnt))
	var ev assetmodel.AssetEvent
	db.Where("asset_id=? AND event_type='cancelled'", aid).First(&ev)
	check("事件 before=active after=cancelled remark 正确",
		ev.BeforeStatus != nil && *ev.BeforeStatus == "active" && ev.AfterStatus != nil && *ev.AfterStatus == "cancelled" && ev.Remark != nil && *ev.Remark == "test-cancel",
		fmt.Sprintf("before=%v after=%v remark=%v", ev.BeforeStatus, ev.AfterStatus, ev.Remark))

	// suspended -> cancelled
	sid := mkAsset("suspended")
	if err := svc.CancelAsset(ctx, sid, 1, ""); err != nil {
		check("suspended 资产取消成功", false, "err="+err.Error())
	} else {
		check("suspended 资产取消成功", true, "")
	}
	var s assetmodel.UserAsset
	db.First(&s, sid)
	check("suspended->cancelled", s.Status == "cancelled", "status="+s.Status)

	// expired -> 拒绝
	eid := mkAsset("expired")
	err := svc.CancelAsset(ctx, eid, 1, "")
	check("expired 资产取消被拒绝", err != nil, fmt.Sprintf("err=%v", err))
	// cancelled -> 拒绝（已取消再取消）
	err2 := svc.CancelAsset(ctx, aid, 1, "")
	check("cancelled 资产再次取消被拒绝", err2 != nil, fmt.Sprintf("err=%v", err2))
}

func testCFix5(ctx context.Context, db *gorm.DB, userID, levelID uint64) {
	repo := memberrepo.NewUserMembershipRepository(db)
	// 用专用干净用户，避免与其他用例数据互相干扰。
	const u = uint64(992000)
	db.Exec("DELETE FROM user_memberships WHERE user_id = ?", u)
	// 用 SQL 相对时间构造 active 且 expires_at < NOW()（避免 Go time.Now 与 DB session 时区不一致）。
	mustExec(db, "INSERT INTO user_memberships (user_id, level_id, status, started_at, expires_at, created_at, updated_at) VALUES (?,?,?,DATE_SUB(NOW(), INTERVAL 3 DAY),DATE_SUB(NOW(), INTERVAL 1 HOUR),NOW(),NOW())",
		u, levelID, "active")
	// 同时构造一条未到期 active，验证不会被误伤。
	mustExec(db, "INSERT INTO user_memberships (user_id, level_id, status, started_at, expires_at, created_at, updated_at) VALUES (?,?,?,NOW(),DATE_ADD(NOW(), INTERVAL 30 DAY),NOW(),NOW())",
		u, levelID, "active")

	var expiredRowID uint64
	db.Raw("SELECT id FROM user_memberships WHERE user_id=? AND status='active' AND expires_at < NOW() LIMIT 1", u).Scan(&expiredRowID)
	check("构造到期 active 记录成功", expiredRowID > 0, fmt.Sprintf("到期 active id=%d", expiredRowID))

	affected, err := repo.BatchExpire(ctx, 1000)
	if err != nil {
		check("BatchExpire 执行成功", false, "err="+err.Error())
	} else {
		check("BatchExpire 执行成功", true, fmt.Sprintf("affected=%d", affected))
	}

	var st string
	db.Raw("SELECT status FROM user_memberships WHERE id=?", expiredRowID).Scan(&st)
	check("到期 active 记录被置为 expired", st == "expired", "status="+st)

	var futureActive int64
	db.Raw("SELECT COUNT(*) FROM user_memberships WHERE user_id=? AND status='active' AND expires_at > NOW()", u).Scan(&futureActive)
	check("未到期 active 不被误伤（仍 active）", futureActive == 1, fmt.Sprintf("未到期 active 数=%d", futureActive))

	db.Exec("DELETE FROM user_memberships WHERE user_id = ?", u)
}

func testCFix6(ctx context.Context, db *gorm.DB) {
	repo := contentrepo.NewAnnouncementRepository(db)

	mk := func(title, scope string, rolesJSON *string) {
		a := contentmodel.Announcement{Title: title, Content: "x", VisibleScope: scope, TargetRolesJSON: rolesJSON, Status: "published", CreatedBy: 1}
		db.Create(&a)
	}
	rolesAB := `["role_a","role_b"]`
	rolesC := `["role_c"]`
	mk("CFIX6_all", "all", nil)
	mk("CFIX6_members", "members", nil)
	mk("CFIX6_roles_ab", "roles", &rolesAB)
	mk("CFIX6_roles_c", "roles", &rolesC)
	mk("CFIX6_admins", "admins", nil)

	has := func(list []*contentmodel.Announcement, title string) bool {
		for _, a := range list {
			if a.Title == title {
				return true
			}
		}
		return false
	}
	onlyCfix := func(list []*contentmodel.Announcement) []*contentmodel.Announcement {
		var out []*contentmodel.Announcement
		for _, a := range list {
			if len(a.Title) >= 5 && a.Title[:5] == "CFIX6" {
				out = append(out, a)
			}
		}
		return out
	}

	// 用户 role_a，非会员
	list, _, err := repo.ListVisible(ctx, []string{"role_a"}, false, 0, 100)
	if err != nil {
		check("ListVisible role_a 查询成功", false, "err="+err.Error())
		return
	}
	list = onlyCfix(list)
	check("role_a 可见 all", has(list, "CFIX6_all"), "")
	check("role_a 可见含 role_a 的 roles 公告 (JSON_CONTAINS 命中)", has(list, "CFIX6_roles_ab"), "")
	check("role_a 不可见仅含 role_c 的 roles 公告 (JSON_CONTAINS 不命中)", !has(list, "CFIX6_roles_c"), "")
	check("role_a 非会员不可见 members 公告", !has(list, "CFIX6_members"), "")
	check("role_a 不可见 admins 公告（用户端永不可见）", !has(list, "CFIX6_admins"), "")

	// 用户 role_c，会员
	list2, _, err := repo.ListVisible(ctx, []string{"role_c"}, true, 0, 100)
	if err != nil {
		check("ListVisible role_c 会员查询成功", false, "err="+err.Error())
		return
	}
	list2 = onlyCfix(list2)
	check("会员可见 members 公告", has(list2, "CFIX6_members"), "")
	check("role_c 可见含 role_c 的 roles 公告", has(list2, "CFIX6_roles_c"), "")
	check("role_c 不可见仅含 role_a/b 的 roles 公告", !has(list2, "CFIX6_roles_ab"), "")
	check("会员仍不可见 admins 公告", !has(list2, "CFIX6_admins"), "")

	// 无角色非会员：仅 all 可见
	list3, _, _ := repo.ListVisible(ctx, nil, false, 0, 100)
	list3 = onlyCfix(list3)
	check("无角色非会员仅可见 all", has(list3, "CFIX6_all") && !has(list3, "CFIX6_members") && !has(list3, "CFIX6_roles_ab") && !has(list3, "CFIX6_roles_c") && !has(list3, "CFIX6_admins"),
		fmt.Sprintf("可见数=%d", len(list3)))

	// 分页：limit=2 应只返回 2 条，total >= 真实可见数
	pageList, total, _ := repo.ListVisible(ctx, []string{"role_a"}, true, 0, 2)
	check("分页 limit=2 返回不超过 2 条", len(pageList) <= 2, fmt.Sprintf("返回=%d", len(pageList)))
	check("分页 total 统计 > 0", total > 0, fmt.Sprintf("total=%d", total))
}
