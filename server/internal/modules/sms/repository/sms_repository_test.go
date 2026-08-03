package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"molin/server/internal/modules/sms/model"
)

func TestFindActiveBindingReturnsApprovedDatabaseSnapshot(t *testing.T) {
	repo, mock, closeDB := newSMSRepositoryMock(t)
	defer closeDB()
	now := time.Now()
	mock.ExpectQuery("SELECT \\* FROM `sms_scene_bindings` WHERE scene = \\? AND enabled = \\? ORDER BY `sms_scene_bindings`.`id` LIMIT \\?").
		WithArgs("register", true, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "scene", "template_id", "sign_name", "enabled", "version", "updated_by", "created_at", "updated_at"}).
			AddRow(1, "register", 9, "test-sign", true, 1, nil, now, now))
	mock.ExpectQuery("SELECT \\* FROM `sms_templates` WHERE `sms_templates`.`id` = \\?").
		WithArgs(9).
		WillReturnRows(sqlmock.NewRows([]string{"id", "provider", "template_code", "template_name", "provider_audit_status", "content", "local_enabled", "version", "last_synced_at", "created_at", "updated_at"}).
			AddRow(9, "aliyun", "SMS_TEST", "测试模板", "approved", "测试内容", true, 1, now, now, now))

	binding, err := repo.FindActiveBinding(context.Background(), "register")
	if err != nil {
		t.Fatalf("查询数据库场景绑定失败: %v", err)
	}
	if binding.Template.TemplateCode != "SMS_TEST" || binding.SignName != "test-sign" {
		t.Fatalf("返回的数据库绑定快照不正确: %#v", binding)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("场景绑定仓储查询不符合预期: %v", err)
	}
}

func TestCreateSendLogPersistsOnlySanitizedModel(t *testing.T) {
	repo, mock, closeDB := newSMSRepositoryMock(t)
	defer closeDB()
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sms_send_logs`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.CreateSendLog(context.Background(), &model.SendLog{
		Scene:             "register",
		PhoneMasked:       "pho****0000",
		PhoneHMAC:         "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TemplateCode:      "SMS_TEST",
		SignName:          "test-sign",
		Provider:          "aliyun",
		BusinessRequestID: "business-request",
		SubmitStatus:      "accepted",
	})
	if err != nil {
		t.Fatalf("写入脱敏发送日志失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("发送日志仓储写入不符合预期: %v", err)
	}
}

func newSMSRepositoryMock(t *testing.T) (*SMSRepository, sqlmock.Sqlmock, func()) {
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
	return NewSMSRepository(gormDB), mock, func() { _ = sqlDB.Close() }
}
