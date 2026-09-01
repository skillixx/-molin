package service

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"molin/server/internal/modules/iam/repository"
)

// 使用数据库驱动边界验证真实IAM服务，不Mock权限判断为恒允许，也不连接Redis。
func TestPermissionFreshOverrideAndDatabaseFailure(t *testing.T) {
	for _, tc := range []struct {
		name, effect       string
		dbFailure, allowed bool
	}{
		{"显式禁止", "deny", false, false},
		{"显式允许", "allow", false, true},
		{"查询失败关闭", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := newFreshIAMTest(t)
			q := mock.ExpectQuery("SELECT .*user_permission_overrides.*expires_at").WithArgs(uint64(7), sqlmock.AnyArg())
			if tc.dbFailure {
				q.WillReturnError(errors.New("数据库不可用"))
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"user_id", "permission_code", "effect"}).AddRow(7, "video:generate", tc.effect))
			}
			allowed, err := s.CheckPermissionFresh(context.Background(), 7, "video:generate")
			if allowed != tc.allowed || (err != nil) != tc.dbFailure {
				t.Fatalf("权限=%v 错误=%v", allowed, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func newFreshIAMTest(t *testing.T) (*IAMService, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	return NewIAMService(repository.NewRoleRepository(db), repository.NewPermissionRepository(db), repository.NewUserRoleRepository(db), repository.NewOverrideRepository(db), repository.NewGroupRepository(db), nil, nil), mock
}

// 视频商品使用权依赖完整角色集合；任一组查询失败不能只返回已读到的直接角色。
func TestPermissionFreshRoleIDsRejectPartialDatabaseResult(t *testing.T) {
	for _, stage := range []string{"members", "group_roles"} {
		t.Run(stage, func(t *testing.T) {
			s, mock := newFreshIAMTest(t)
			mock.ExpectQuery("SELECT .*user_roles").WillReturnRows(sqlmock.NewRows([]string{"user_id", "role_id"}).AddRow(7, 9))
			q := mock.ExpectQuery("SELECT .*user_group_members")
			if stage == "members" {
				q.WillReturnError(errors.New("分组不可用"))
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"user_id", "group_id"}).AddRow(7, 11))
				mock.ExpectQuery("SELECT .*group_roles").WillReturnError(errors.New("组角色不可用"))
			}
			ids, err := s.GetUserRoleIDs(context.Background(), 7)
			if err == nil || len(ids) != 0 {
				t.Fatalf("不得返回部分角色：数量=%d，error=%v", len(ids), err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPermissionFreshRoleGroupAndFailureClosed(t *testing.T) {
	for _, tc := range []struct {
		name, failure     string
		role, group, want bool
	}{
		{"默认拒绝", "", false, false, false},
		{"角色授权", "", true, false, true},
		{"组授权", "", false, true, true},
		{"角色查询失败", "roles", false, true, false},
		{"权限查询失败", "permissions", true, true, false},
		{"组查询失败不复用角色授权", "groups", true, false, false},
		{"组权限读取失败", "group_permissions", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := newFreshIAMTest(t)
			mock.ExpectQuery("SELECT .*user_permission_overrides").WillReturnRows(sqlmock.NewRows([]string{"user_id", "permission_code", "effect"}))
			roleQuery := mock.ExpectQuery("SELECT .*user_roles")
			failure := errors.New("权限事实查询失败")
			if tc.failure == "roles" {
				roleQuery.WillReturnError(failure)
			} else {
				roles := sqlmock.NewRows([]string{"user_id", "role_id"})
				if tc.role {
					roles.AddRow(7, 9)
				}
				roleQuery.WillReturnRows(roles)
				if tc.role {
					permQuery := mock.ExpectQuery("SELECT .*permissions.*role_permissions")
					if tc.failure == "permissions" {
						permQuery.WillReturnError(failure)
					} else {
						permQuery.WillReturnRows(sqlmock.NewRows([]string{"code"}).AddRow("video:generate"))
					}
				}
				if tc.failure != "permissions" {
					groupQuery := mock.ExpectQuery("SELECT .*user_group_members")
					if tc.failure == "groups" {
						groupQuery.WillReturnError(failure)
					} else {
						groups := sqlmock.NewRows([]string{"user_id", "group_id"})
						if tc.group {
							groups.AddRow(7, 11)
						}
						groupQuery.WillReturnRows(groups)
						if tc.group {
							q := mock.ExpectQuery("SELECT .*group_permissions")
							if tc.failure == "group_permissions" {
								q.WillReturnError(failure)
							} else {
								q.WillReturnRows(sqlmock.NewRows([]string{"permission_code"}).AddRow("video:generate"))
							}
						}
					}
				}
			}
			allowed, err := s.CheckPermissionFresh(context.Background(), 7, "video:generate")
			if allowed != tc.want || (err != nil) != (tc.failure != "") {
				t.Fatalf("权限=%v 错误=%v", allowed, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
