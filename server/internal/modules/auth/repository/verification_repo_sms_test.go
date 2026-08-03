package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCheckAndMarkUsedRequiresSentForPhone(t *testing.T) {
	repo, mock, closeDB := newVerificationRepositoryMock(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `verification_codes` SET `used_at`=\\? WHERE .*send_status = \\?").
		WithArgs(sqlmock.AnyArg(), "phone", "phone-test-value", "register", "hash-value", sqlmock.AnyArg(), "sent").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.CheckAndMarkUsed(context.Background(), "phone", "phone-test-value", "register", "hash-value"); err != nil {
		t.Fatalf("sent 手机验证码原子消费失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("手机号校验 SQL 未包含 sent 状态约束: %v", err)
	}
}

func TestCheckAndMarkUsedKeepsEmailCompatible(t *testing.T) {
	repo, mock, closeDB := newVerificationRepositoryMock(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `verification_codes` SET `used_at`=? WHERE target_type = ? AND target_value = ? AND scene = ? AND code = ? AND used_at IS NULL AND expires_at > ?")).
		WithArgs(sqlmock.AnyArg(), "email", "email-test-value", "register", "hash-value", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.CheckAndMarkUsed(context.Background(), "email", "email-test-value", "register", "hash-value"); err != nil {
		t.Fatalf("邮箱验证码兼容消费失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("邮箱校验不应要求 sent 状态: %v", err)
	}
}

func TestUpdateSendStateOnlyTransitionsPending(t *testing.T) {
	repo, mock, closeDB := newVerificationRepositoryMock(t)
	defer closeDB()
	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `verification_codes` SET .* WHERE id = .* AND send_status = .*").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.UpdateSendState(context.Background(), 1, "sent", &now, "aliyun", "provider-request"); err != nil {
		t.Fatalf("pending 转 sent 失败: %v", err)
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
