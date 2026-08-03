package repository

import (
	"context"
	"errors"
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

func TestGetAdminSummaryUsesSingleDatabaseAggregate(t *testing.T) {
	repo, mock, closeDB := newSMSRepositoryMock(t)
	defer closeDB()
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery("SELECT .*template_total.*approved_total.*enabled_total.*bound_scene_total.*last_synced_at").
		WillReturnRows(sqlmock.NewRows([]string{"template_total", "approved_total", "enabled_total", "bound_scene_total", "last_synced_at"}).
			AddRow(7, 5, 4, 3, now))

	got, err := repo.GetAdminSummary(context.Background())
	if err != nil {
		t.Fatalf("聚合短信管理概览失败: %v", err)
	}
	if got.TemplateTotal != 7 || got.ApprovedTotal != 5 || got.EnabledTotal != 4 || got.BoundSceneTotal != 3 {
		t.Fatalf("短信管理概览统计错误: %#v", got)
	}
	if got.LastSyncedAt == nil || !got.LastSyncedAt.Equal(now) {
		t.Fatalf("最后同步时间错误: %#v", got.LastSyncedAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("概览聚合查询不符合预期: %v", err)
	}
}

func TestUpdateAdminTemplateStatusUsesVersionAndActiveBindingGuard(t *testing.T) {
	repo, mock, closeDB := newSMSRepositoryMock(t)
	defer closeDB()
	mock.ExpectExec("UPDATE sms_templates SET local_enabled = \\?, version = version \\+ 1 .*NOT EXISTS").
		WithArgs(false, uint64(7), uint64(2), false, uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.UpdateAdminTemplateStatus(context.Background(), 7, 2, false); err != nil {
		t.Fatalf("按版本停用未绑定模板失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("模板状态 CAS 查询不符合预期: %v", err)
	}
}

func TestUpdateAdminTemplateStatusReturnsConflictWhenCASMisses(t *testing.T) {
	repo, mock, closeDB := newSMSRepositoryMock(t)
	defer closeDB()
	mock.ExpectExec("UPDATE sms_templates SET local_enabled = \\?, version = version \\+ 1 .*NOT EXISTS").
		WithArgs(true, uint64(7), uint64(2), true, uint64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.UpdateAdminTemplateStatus(context.Background(), 7, 2, true)
	if !errors.Is(err, ErrAdminTemplateConflict) {
		t.Fatalf("CAS 未命中应返回冲突，实际: %v", err)
	}
}

func TestReserveTestSendRejectsSameAdminKeyWithChangedRequest(t *testing.T) {
	repo, mock, closeDB := newSMSRepositoryMock(t)
	defer closeDB()
	ownerKey, oldFingerprint, newFingerprint := "owner-key-hash", "old-fingerprint", "new-fingerprint"
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sms_send_logs`").WillReturnError(errors.New("duplicate key"))
	mock.ExpectRollback()
	mock.ExpectQuery("SELECT \\* FROM `sms_send_logs` WHERE idempotency_owner_key_hash = \\? ORDER BY `sms_send_logs`.`id` LIMIT \\?").
		WithArgs(ownerKey, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "idempotency_owner_key_hash", "request_fingerprint", "submit_status"}).AddRow(8, ownerKey, oldFingerprint, "accepted"))

	_, _, err := repo.ReserveTestSend(context.Background(), &model.SendLog{IdempotencyOwnerKeyHash: &ownerKey, RequestFingerprint: &newFingerprint})
	if !errors.Is(err, ErrTestSendIdempotencyConflict) {
		t.Fatalf("同管理员相同幂等键修改参数必须冲突，实际: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("幂等冲突查询不符合预期: %v", err)
	}
}

func TestCompleteTestSendPersistsRetryAfterForIdempotentReplay(t *testing.T) {
	repo, mock, closeDB := newSMSRepositoryMock(t)
	defer closeDB()
	retryAfter := int64(27)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `sms_send_logs` SET .*`retry_after_seconds`=\\?.* WHERE id = \\? AND submit_status = \\?").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.CompleteTestSend(context.Background(), 8, "failed", nil, nil, stringPointerForRepositoryTest("测试发送频率超限"), &retryAfter, time.Now().UTC()); err != nil {
		t.Fatalf("持久化限流恢复秒数失败: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("限流恢复秒数仓储写入不符合预期: %v", err)
	}
}

func stringPointerForRepositoryTest(value string) *string { return &value }

func TestListAdminSendLogsNeverPublishesPendingRows(t *testing.T) {
	repo, mock, closeDB := newSMSRepositoryMock(t)
	defer closeDB()
	now := time.Now().UTC()
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `sms_send_logs` WHERE submit_status <> \\?").
		WithArgs("pending").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("SELECT \\* FROM `sms_send_logs` WHERE submit_status <> \\? ORDER BY id DESC LIMIT \\?").
		WithArgs("pending", 20).
		WillReturnRows(sqlmock.NewRows([]string{"id", "purpose", "scene", "phone_masked", "template_code", "sign_name", "provider", "business_request_id", "submit_status", "submitted_at", "created_at"}).
			AddRow(1, "test", "register", "pho****st-a", "SMS_SAFE", "固定签名", "aliyun", "sms_safe", "accepted", now, now))

	items, total, err := repo.ListAdminSendLogs(context.Background(), model.SendLogListFilter{Limit: 20})
	if err != nil || total != 1 || len(items) != 1 || items[0].SubmitStatus != "accepted" {
		t.Fatalf("发送记录终态查询错误: total=%d items=%#v err=%v", total, items, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("发送记录查询必须固定排除 pending: %v", err)
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
