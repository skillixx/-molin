package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCheckAndMarkUsedRequiresAcceptedForPhone(t *testing.T) {
	repo, mock, closeDB := newVerificationRepositoryMock(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `verification_codes` SET `used_at`=\\? WHERE .*send_status = \\?").
		WithArgs(sqlmock.AnyArg(), "phone", "register", "hash-value", "accepted", sqlmock.AnyArg(), "phone-test-value").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.CheckAndMarkUsed(context.Background(), "phone", "phone-test-value", "register", "hash-value"); err != nil {
		t.Fatalf("accepted 手机验证码原子消费失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("手机号校验 SQL 未包含 accepted 状态约束: %v", err)
	}
}

func TestCheckAndMarkUsedKeepsEmailCompatible(t *testing.T) {
	repo, mock, closeDB := newVerificationRepositoryMock(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `verification_codes` SET `used_at`=\\? WHERE .*target_hash = \\?").
		WithArgs(sqlmock.AnyArg(), "email", "register", "hash-value", "accepted", sqlmock.AnyArg(), "email-test-value").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.CheckAndMarkUsed(context.Background(), "email", "email-test-value", "register", "hash-value"); err != nil {
		t.Fatalf("邮箱验证码兼容消费失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("邮箱校验必须沿用 accepted 状态: %v", err)
	}
}

func TestUpdateSMSSendStateOnlyTransitionsPending(t *testing.T) {
	repo, mock, closeDB := newVerificationRepositoryMock(t)
	defer closeDB()
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `verification_codes` SET .* WHERE id = .* AND send_status = .*").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.UpdateSMSSendState(context.Background(), 1, "accepted", &now, "aliyun", "provider-request"); err != nil {
		t.Fatalf("pending 转 accepted 失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("状态更新 SQL 未锁定 pending 前态: %v", err)
	}
}

func newVerificationRepositoryMock(t *testing.T) (*VerificationRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("创建 SQL Mock 失败: %v", err)
	}
	gormDB, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		_ = sqlDB.Close()
		t.Fatalf("创建 GORM Mock 失败: %v", err)
	}
	return NewVerificationRepository(gormDB), mock, func() { _ = sqlDB.Close() }
}
