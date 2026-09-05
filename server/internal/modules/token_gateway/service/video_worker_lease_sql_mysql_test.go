package service

import (
	"context"
	"errors"
	"net"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// TestVideoG7WorkerLeaseSQLMySQL SQL不能绕过空租约初态，也不能在同代次伪造任意ID的审计副本。
func TestVideoG7WorkerLeaseSQLMySQL(t *testing.T) {
	db := openVideoG5MySQL(t)
	ctx := context.Background()
	t.Run("ddl_target_binding", func(t *testing.T) {
		expected := os.Getenv("MOLIN_VIDEO_G7_LEASE_MYSQL_SERVER_UUID")
		if !videoG7WorkerLeaseDDLTarget(db) {
			t.Fatal("极限DDL测试必须由G7隔离runner绑定当前临时MySQL实例")
		}
		for _, invalid := range []string{"", "00000000-0000-0000-0000-000000000000"} {
			t.Setenv("MOLIN_VIDEO_G7_LEASE_MYSQL_SERVER_UUID", invalid)
			if videoG7WorkerLeaseDDLTarget(db) {
				t.Fatal("缺少或错误实例绑定不能授权DDL夹具")
			}
		}
		t.Setenv("MOLIN_VIDEO_G7_LEASE_MYSQL_SERVER_UUID", expected)
	})
	t.Run("insert_must_start_empty", func(t *testing.T) {
		f := newVideoG5ReservationFixture(t, db, "10")
		now := time.Now().UTC()
		request := model.AIRequest{RequestID: f.command.RequestID, UserID: f.owner.UserID, ProjectID: &f.owner.ProjectID, APIKeyID: f.owner.APIKeyID, LogicalModelCode: f.command.FingerprintInput.LogicalModelCode, Modality: "video", Capability: model.AIVideoCapability, Operation: f.quote.Operation, ModerationStatus: model.AIModerationPending, ExecutionStatus: model.AIExecutionPending, BillingStatus: model.AIBillingUnquoted, DeliveryStatus: model.AIDeliveryPending, VersionNo: 1, CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&request).Error; err != nil {
			t.Fatal(err)
		}
		snapshot, err := DecodeVideoPriceSnapshot(f.quote.PriceSnapshotJSON)
		if err != nil {
			t.Fatal(err)
		}
		row := map[string]interface{}{"public_id": f.command.TaskID, "request_id": request.RequestID, "quote_id": f.quote.ID, "user_id": f.owner.UserID, "project_id": f.owner.ProjectID, "api_key_id": f.owner.APIKeyID, "logical_model_code": request.LogicalModelCode, "capability": model.AIVideoCapability, "operation": *f.quote.Operation, "status": "created", "input_json": string(snapshot.SelectedLines[0].VariantJSON), "version_no": 1, "lease_owner": "sql-worker", "lease_version": 1, "heartbeat_at": gorm.Expr("UTC_TIMESTAMP(6)"), "lease_until": gorm.Expr("UTC_TIMESTAMP(6)+INTERVAL 30 SECOND"), "worker_stage": "submit", "worker_lease_active": 1}
		err = db.Table("ai_gateway_tasks").Create(row).Error
		var sqlErr *drivermysql.MySQLError
		if !errors.As(err, &sqlErr) || sqlErr.Number != 1644 {
			t.Fatalf("SQL不得直接INSERT已持有的执行租约: %v", err)
		}
		row["lease_owner"] = nil
		row["lease_version"] = 0
		row["heartbeat_at"] = nil
		row["lease_until"] = nil
		row["worker_stage"] = nil
		row["worker_lease_active"] = 0
		if err := db.Table("ai_gateway_tasks").Create(row).Error; err != nil {
			t.Fatalf("相同业务字段空租约正例必须允许: %v", err)
		}
	})
	t.Run("event_id_must_bind_generation", func(t *testing.T) {
		f := newVideoG5ReservationFixture(t, db, "10")
		if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
			t.Fatal(err)
		}
		lease, err := repository.NewVideoWorkerLeaseRepository(db).Claim(ctx, f.command.TaskID, f.owner, "audit-worker", "submit")
		if err != nil {
			t.Fatal(err)
		}
		task, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.command.TaskID, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		err = db.Table("ai_gateway_task_events").Create(map[string]interface{}{"event_id": "forged_" + f.command.RequestID, "task_id": task.ID, "user_id": f.owner.UserID, "project_id": f.owner.ProjectID, "event_type": "video_worker_lease_claimed", "source": "worker", "safe_detail_json": "{}", "created_at": time.Now().UTC(), "worker_lease_version": lease.Version(), "worker_lease_owner": "audit-worker", "worker_lease_stage": "submit"}).Error
		var sqlErr *drivermysql.MySQLError
		if !errors.As(err, &sqlErr) || sqlErr.Number != 1644 {
			t.Fatalf("同一代次不得另造任意事件ID: %v", err)
		}
	})
	t.Run("maximum_generation_can_renew_and_release", func(t *testing.T) {
		f := newVideoG5ReservationFixture(t, db, "10")
		if _, err := f.service.ReserveAndCreate(ctx, f.command); err != nil {
			t.Fatal(err)
		}
		task, err := repository.NewVideoTaskRepository(db).FindForOwner(ctx, f.command.TaskID, f.owner)
		if err != nil {
			t.Fatal(err)
		}
		seedVideoG7LastGeneration(t, db, task)
		repo := repository.NewVideoWorkerLeaseRepository(db)
		last, err := repo.Claim(ctx, f.command.TaskID, f.owner, "last-worker", "submit")
		if err != nil || last.Version() != ^uint64(0) {
			t.Fatalf("最后可用代次仍应允许认领: %v", err)
		}
		renewed, err := repo.Renew(ctx, last)
		if err != nil {
			t.Fatalf("最大代次合法续期不得发生加法溢出: %v", err)
		}
		if err := repo.Release(ctx, renewed); err != nil {
			t.Fatalf("最大代次合法释放不得发生加法溢出: %v", err)
		}
		if proof, err := repo.Claim(ctx, f.command.TaskID, f.owner, "overflow-worker", "submit"); proof != nil || !errors.Is(err, repository.ErrVideoWorkerLeaseLost) {
			t.Fatalf("代次耗尽必须失败关闭而非回绕: %v", err)
		}
	})
}

// 仅在G7 runner明确绑定的一次性数据库准备数值极限夹具，避免循环2^64次认领。
// 暂时移除的只有本轮新增UPDATE守卫；原样恢复成功后才运行被测公开Repository。
// 该准备不是正常业务迁移或崩溃证据，不允许用于共享数据库。
func seedVideoG7LastGeneration(t *testing.T, db *gorm.DB, task *repository.VideoTaskRecord) {
	t.Helper()
	if !videoG7WorkerLeaseDDLTarget(db) {
		t.Fatal("禁止在未绑定本轮临时实例的数据库调整租约守卫")
	}
	var body string
	if err := db.Raw("SELECT ACTION_STATEMENT FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name='trg_video_worker_lease_update'").Scan(&body).Error; err != nil || body == "" {
		t.Fatalf("读取隔离库原守卫失败: %v", err)
	}
	if err := db.Exec("DROP TRIGGER trg_video_worker_lease_update").Error; err != nil {
		t.Fatal(err)
	}
	restore := func() error {
		return db.Exec("CREATE TRIGGER trg_video_worker_lease_update BEFORE UPDATE ON ai_gateway_tasks FOR EACH ROW " + body).Error
	}
	restored := false
	defer func() {
		if !restored {
			if err := restore(); err != nil {
				t.Errorf("恢复隔离库守卫失败: %v", err)
			}
		}
	}()
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE ai_gateway_tasks SET lease_version=18446744073709551614,lease_owner='boundary-seed',worker_stage='submit',heartbeat_at=UTC_TIMESTAMP(6),lease_until=UTC_TIMESTAMP(6)+INTERVAL 30 SECOND,worker_lease_active=1 WHERE id=?", task.ID).Error; err != nil {
			return err
		}
		for _, kind := range []string{"video_worker_lease_claimed", "video_worker_lease_released"} {
			if kind == "video_worker_lease_released" {
				if err := tx.Exec("UPDATE ai_gateway_tasks SET worker_lease_active=0 WHERE id=?", task.ID).Error; err != nil {
					return err
				}
			}
			if err := tx.Exec("INSERT INTO ai_gateway_task_events(event_id,task_id,user_id,project_id,event_type,source,safe_detail_json,created_at,worker_lease_version,worker_lease_owner,worker_lease_stage) SELECT CONCAT('vg7_worker_',SHA2(CONCAT(public_id,'|',lease_version,'|',?),256)),id,user_id,project_id,?,'worker','{}',UTC_TIMESTAMP(6),lease_version,lease_owner,worker_stage FROM ai_gateway_tasks WHERE id=?", kind, kind, task.ID).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	restored = true
}

// 旧G5环境标记不能单独授权DROP触发器；同时核对解析后DSN、实际库名及本轮容器server_uuid。
// UUID由runner从新建临时实例读取，不从应用配置继承；错误不回显连接串或凭据。
func videoG7WorkerLeaseDDLTarget(db *gorm.DB) bool {
	cfg, err := drivermysql.ParseDSN(os.Getenv("MOLIN_VIDEO_G5_MYSQL_DSN"))
	expected := os.Getenv("MOLIN_VIDEO_G7_LEASE_MYSQL_SERVER_UUID")
	if err != nil || cfg.Net != "tcp" || cfg.DBName != "molin_video_g5_contract" || os.Getenv("MOLIN_VIDEO_G5_ISOLATED") != "YES" || !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(expected) {
		return false
	}
	host, port, err := net.SplitHostPort(cfg.Addr)
	portNumber, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || (host != "127.0.0.1" && !regexp.MustCompile(`^molin-vidg7-outbox-mysql-[0-9a-f]{12}$`).MatchString(host)) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var actual struct{ ServerUUID, DatabaseName string }
	return db.WithContext(ctx).Raw("SELECT @@server_uuid AS server_uuid,DATABASE() AS database_name").Scan(&actual).Error == nil && actual.ServerUUID == expected && actual.DatabaseName == cfg.DBName
}
