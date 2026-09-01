package service

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// 在真实IAM公开边界检查授权期限；只替换SQL驱动，不将权限服务替换成恒允许。
func TestPermissionFreshAuthorizationExpiry(t *testing.T) {
	for _, mode := range []string{"temporary", "role_alternative", "permanent", "deny", "expired"} {
		t.Run(mode, func(t *testing.T) {
			s, mock := newFreshIAMTest(t)
			reader, ok := any(s).(interface {
				CheckPermissionFreshWithExpiry(context.Context, uint64, string) (bool, *time.Time, error)
			})
			if !ok {
				t.Fatal("缺少与原判定共享规则的权限有效期接口")
			}
			future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
			var expiry any = future
			effect := "allow"
			if mode == "permanent" {
				expiry = nil
			}
			if mode == "deny" {
				effect = "deny"
			}
			if mode == "expired" {
				expiry = past
			}
			mock.ExpectQuery("SELECT .*user_permission_overrides.*expires_at").WillReturnRows(sqlmock.NewRows([]string{"user_id", "permission_code", "effect", "expires_at"}).AddRow(7, "video:generate", effect, expiry))
			if mode == "temporary" || mode == "role_alternative" || mode == "expired" {
				roles := sqlmock.NewRows([]string{"user_id", "role_id"})
				if mode == "role_alternative" {
					roles.AddRow(7, 9)
				}
				mock.ExpectQuery("SELECT .*user_roles").WillReturnRows(roles)
				if mode == "role_alternative" {
					mock.ExpectQuery("SELECT .*permissions.*role_permissions").WillReturnRows(sqlmock.NewRows([]string{"code"}).AddRow("video:generate"))
				}
				mock.ExpectQuery("SELECT .*user_group_members").WillReturnRows(sqlmock.NewRows([]string{"user_id", "group_id"}))
			}
			allowed, until, err := reader.CheckPermissionFreshWithExpiry(context.Background(), 7, "video:generate")
			want := mode != "deny" && mode != "expired"
			if err != nil || allowed != want {
				t.Fatalf("权限结果不符：allowed=%t err=%v", allowed, err)
			}
			if mode == "temporary" {
				if until == nil || !until.Equal(future) {
					t.Fatal("仅临时allow必须返回真实到期时间")
				}
			} else if until != nil {
				t.Fatal("无授权与永久授权由allowed区分，不能沿用无关临时allow的期限")
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
