package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"molin/server/internal/config"
	"molin/server/internal/modules/auth/repository"
)

// 只替换SQL驱动边界，验证真实AuthService的错误传播与旧bool兼容，不启用任何发码依赖。
func TestAdminVerifyReadErrorCompatibility(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	auth := NewAuthService(repository.NewUserRepository(db), nil, nil, nil, config.Config{AdminVerifyExpireHours: 24}, nil, nil, nil, db)
	failure := errors.New("合成MFA数据库故障")
	mock.ExpectQuery("SELECT .*users.*").WillReturnError(failure)
	valid, err := auth.CheckAdminVerified(context.Background(), 77)
	if valid || !errors.Is(err, failure) {
		t.Fatal("新入口必须保留数据库错误，而不是返回未认证")
	}
	mock.ExpectQuery("SELECT .*users.*").WillReturnError(failure)
	if auth.IsAdminVerified(context.Background(), 77) {
		t.Fatal("旧bool入口仍必须失败关闭")
	}
	past := time.Now().Add(-time.Hour)
	mock.ExpectQuery("SELECT .*users.*").WillReturnRows(sqlmock.NewRows([]string{"id", "admin_phone_verified_at", "admin_email_verified_at"}).AddRow(77, past, past))
	if valid, err := auth.CheckAdminVerified(context.Background(), 77); err != nil || !valid {
		t.Fatal("有效双MFA规则保持不变")
	}
	mock.ExpectQuery("SELECT .*users.*").WillReturnRows(sqlmock.NewRows([]string{"id", "admin_phone_verified_at", "admin_email_verified_at"}).AddRow(77, past, nil))
	if valid, err := auth.CheckAdminVerified(context.Background(), 77); err != nil || valid {
		t.Fatal("缺失MFA仍是普通未认证，不是数据库错误")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
