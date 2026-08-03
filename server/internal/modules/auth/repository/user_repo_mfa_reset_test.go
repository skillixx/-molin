package repository

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestUpdatePhoneAndVerifiedClearsAdminPhoneMFA(t *testing.T) {
	repo, mock, closeDB := newUserRepositoryMock(t)
	defer closeDB()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE `users` SET `admin_phone_verified_at`=?,`phone`=?,`phone_verified`=?,`updated_at`=? WHERE id = ?")).
		WithArgs(nil, "13900000000", true, sqlmock.AnyArg(), uint64(259)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.UpdatePhoneAndVerified(context.Background(), 259, "13900000000"); err != nil {
		t.Fatalf("换绑手机号并清空管理员手机 MFA 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("换绑手机号必须在同一条 SQL 中清空旧管理员手机 MFA: %v", err)
	}
}

func TestUpdateEmailAndVerifiedClearsAdminEmailMFA(t *testing.T) {
	repo, mock, closeDB := newUserRepositoryMock(t)
	defer closeDB()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE `users` SET `admin_email_verified_at`=?,`email`=?,`email_verified`=?,`updated_at`=? WHERE id = ?")).
		WithArgs(nil, "new@example.com", true, sqlmock.AnyArg(), uint64(259)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.UpdateEmailAndVerified(context.Background(), 259, "new@example.com"); err != nil {
		t.Fatalf("换绑邮箱并清空管理员邮箱 MFA 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("换绑邮箱必须在同一条 SQL 中清空旧管理员邮箱 MFA: %v", err)
	}
}

func TestUpdateAdminUserClearsChangedContactMFA(t *testing.T) {
	tests := []struct {
		name   string
		fields map[string]interface{}
		query  string
		args   []driver.Value
	}{
		{
			name:   "管理员修改手机号",
			fields: map[string]interface{}{"phone": "13900000000", "phone_verified": true},
			query:  "UPDATE `users` SET `admin_phone_verified_at`=?,`phone`=?,`phone_verified`=?,`updated_at`=? WHERE id = ?",
			args:   []driver.Value{nil, "13900000000", true, sqlmock.AnyArg(), uint64(259)},
		},
		{
			name:   "管理员修改邮箱",
			fields: map[string]interface{}{"email": "new@example.com", "email_verified": true},
			query:  "UPDATE `users` SET `admin_email_verified_at`=?,`email`=?,`email_verified`=?,`updated_at`=? WHERE id = ?",
			args:   []driver.Value{nil, "new@example.com", true, sqlmock.AnyArg(), uint64(259)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo, mock, closeDB := newUserRepositoryMock(t)
			defer closeDB()

			mock.ExpectExec(regexp.QuoteMeta(test.query)).
				WithArgs(test.args...).
				WillReturnResult(sqlmock.NewResult(0, 1))

			if err := repo.UpdateAdminUser(context.Background(), 259, test.fields); err != nil {
				t.Fatalf("管理员修改联系方式失败: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("管理员修改联系方式必须清空对应 MFA: %v", err)
			}
		})
	}
}

func TestUpdateAdminUserStatusDoesNotClearContactMFA(t *testing.T) {
	repo, mock, closeDB := newUserRepositoryMock(t)
	defer closeDB()

	mock.ExpectExec(regexp.QuoteMeta("UPDATE `users` SET `status`=?,`updated_at`=? WHERE id = ?")).
		WithArgs("active", sqlmock.AnyArg(), uint64(259)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	fields := map[string]interface{}{"status": "active"}
	if err := repo.UpdateAdminUser(context.Background(), 259, fields); err != nil {
		t.Fatalf("管理员仅修改状态失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("仅修改状态时不得清空联系方式 MFA: %v", err)
	}
}

func newUserRepositoryMock(t *testing.T) (*UserRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("创建用户仓储 SQL Mock 失败: %v", err)
	}
	gormDB, err := gorm.Open(
		mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent), SkipDefaultTransaction: true},
	)
	if err != nil {
		_ = sqlDB.Close()
		t.Fatalf("创建用户仓储 GORM Mock 失败: %v", err)
	}
	return NewUserRepository(gormDB), mock, func() { _ = sqlDB.Close() }
}
