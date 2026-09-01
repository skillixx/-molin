package service

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/iam/repository"
)

func TestVideoG6IAMMySQLFreshPermissions(t *testing.T) {
	dsn := os.Getenv("MOLIN_VIDEO_G6_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未配置G6一次性MySQL")
	}
	config, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("隔离DSN无效")
	}
	host, port, err := net.SplitHostPort(config.Addr)
	if err != nil || os.Getenv("MOLIN_VIDEO_G6_ISOLATED") != "YES" || config.DBName != "molin_video_g6_contract" || config.Net != "tcp" || port != "3306" || (host != "mysql" && host != "127.0.0.1") {
		t.Fatal("只允许本轮一次性MySQL")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("隔离MySQL不可用")
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	s := NewIAMService(repository.NewRoleRepository(db), repository.NewPermissionRepository(db), repository.NewUserRoleRepository(db), repository.NewOverrideRepository(db), repository.NewGroupRepository(db), nil, nil)
	const userID = uint64(996001)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if db.Exec(query, args...).Error != nil {
			t.Fatal("合成权限夹具写入失败")
		}
	}
	exec("INSERT INTO users(id,password_hash,status,real_name_status) VALUES(?,'fixture','active','verified')", userID)
	var permissionID uint64
	if err := db.Table("permissions").Select("id").Where("code=?", "video:generate").Scan(&permissionID).Error; err != nil || permissionID == 0 {
		t.Fatal("视频生成权限seed不存在")
	}
	assertPermission := func(want bool) {
		t.Helper()
		allowed, err := s.CheckPermissionFresh(ctx, userID, "video:generate")
		if err != nil || allowed != want {
			t.Fatalf("实时权限=%v，预期%v，错误=%v", allowed, want, err)
		}
	}
	assertPermission(false)
	exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) VALUES(?,?,?,'allow')", userID, permissionID, "video:generate")
	assertPermission(true)
	exec("UPDATE user_permission_overrides SET effect='deny' WHERE user_id=? AND permission_id=?", userID, permissionID)
	assertPermission(false)
	exec("UPDATE user_permission_overrides SET effect='allow',expires_at=? WHERE user_id=? AND permission_id=?", time.Now().UTC().Add(-time.Minute), userID, permissionID)
	assertPermission(false)
	exec("INSERT INTO roles(id,code,name) VALUES(996001,'vid_g6_fixture','视频隔离角色')")
	exec("INSERT INTO user_roles(user_id,role_id) VALUES(?,996001)", userID)
	exec("INSERT INTO role_permissions(role_id,permission_id) VALUES(996001,?)", permissionID)
	assertPermission(true)
	exec("UPDATE user_permission_overrides SET effect='deny',expires_at=NULL WHERE user_id=? AND permission_id=?", userID, permissionID)
	assertPermission(false)
	// 每个并发查询仍实际访问MySQL；不使用Redis，也不把权限服务替换为测试常量。
	results := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		go func() {
			allowed, err := s.CheckPermissionFresh(ctx, userID, "video:generate")
			results <- err == nil && !allowed
		}()
	}
	for i := 0; i < 100; i++ {
		if !<-results {
			t.Fatal("并发deny被绕过")
		}
	}
}
