package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	assetmodel "molin/server/internal/modules/asset/model"
	assetrepo "molin/server/internal/modules/asset/repository"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

type videoLegacySaveFixture struct {
	app                          *VideoHTTPService
	op                           videoAssetSave
	videoID, rootID, key, status string
}

// 从真实G5已交付任务和旧列约束构造迁移历史，不运行新版本的尝试选择，也不关闭旧触发器。
func seedVideoLegacySave(t *testing.T, status string) videoLegacySaveFixture {
	t.Helper()
	f := NewVideoContentHTTPFixture(t)
	entID := f.EnableAssetSaving()
	f.EnableAssetDownloads()
	id := f.CreateCompletedForKey(f.ProjectID)
	ctx := context.Background()
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	var op videoAssetSave
	rootID := ""
	key := "g6-legacy-save-" + status
	if err := f.DB.Transaction(func(tx *gorm.DB) error {
		task, owner, err := f.App.saveTaskTx(ctx, tx, caller, id)
		if err != nil {
			return err
		}
		assets, err := f.App.saveSourceTx(ctx, tx, task, owner)
		if err != nil {
			return err
		}
		op = videoAssetSave{TaskID: task.ID, PublicID: fmt.Sprintf("vsave_legacy_%d", f.ProjectID), RequestID: task.RequestID, UserID: owner.UserID, ProjectID: owner.ProjectID, APIKeyID: owner.APIKeyID, Status: "copying", VersionNo: 1, StorageEntitlementID: entID, StorageProductID: f.App.savePolicy.StorageProductID, QuotaUnit: "bytes", PolicyVersion: f.App.savePolicy.Version, CreatedAt: time.Now().UTC()}
		var plan []videoAssetSaveItem
		for _, a := range assets {
			if a.AssetRole == "moderation_copy" {
				continue
			}
			if a.AssetRole == "content" {
				rootID = a.PublicID
			}
			op.TotalBytes += *a.SizeBytes
			plan = append(plan, videoAssetSaveItem{AssetID: a.ID, PublicID: a.PublicID, Role: a.AssetRole, VersionNo: a.VersionNo, SHA256: *a.SHA256, Size: *a.SizeBytes, SourceBucket: *a.Bucket, SourceKey: *a.ObjectKey, TargetBucket: "ai-user-assets", TargetKey: op.PublicID + "/" + a.PublicID + "/" + a.AssetRole + ".bin", MetadataSHA256: mediaMetadataSHA(a)})
		}
		if len(plan) != 5 || rootID == "" {
			return ErrVideoSaveConflict
		}
		op.PlanJSON, err = json.Marshal(plan)
		if err != nil {
			return err
		}
		op.PlanSHA256 = videoPayloadSHA256(op.PlanJSON)
		op.QuotaAmount, err = videoSaveQuota(op.TotalBytes, op.QuotaUnit)
		if err != nil {
			return err
		}
		if _, err = f.App.reserveSaveCapacityTx(tx, owner.UserID, owner.ProjectID, op.TotalBytes); err != nil {
			return err
		}
		if err = assetrepo.NewEntitlementRepository(tx).ReserveQuota(ctx, tx, entID, op.QuotaAmount); err != nil {
			return err
		}
		// 这些列在89版尚不存在；省略列不是绕过约束，实际旧INSERT与全部旧守卫仍执行。
		if err = tx.Omit("AttemptNo", "PreviousSaveID", "StorageEntitlementType").Create(&op).Error; err != nil {
			return err
		}
		command := videoAssetSaveCommand{UserID: owner.UserID, ProjectID: owner.ProjectID, TaskID: task.ID, APIKeyID: owner.APIKeyID, CommandKeyHash: videoBillingDigest(fmt.Sprintf("video-save:%d:%d:%s", owner.UserID, owner.ProjectID, key)), CreatedAt: time.Now().UTC()}
		return tx.Omit("SavePublicID").Create(&command).Error
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := decodeVideoSavePlan(&op)
	if err != nil {
		t.Fatal(err)
	}
	if status != "copying" {
		for i, p := range plan {
			if status != "completed" && i > 0 {
				break
			}
			if _, err := f.App.saveStore.CopyImmutable(ctx, video.VideoObjectRef{Bucket: p.SourceBucket, ObjectKey: p.SourceKey}, video.VideoObjectRef{Bucket: p.TargetBucket, ObjectKey: p.TargetKey}, p.SHA256, p.Size); err != nil {
				t.Fatal(err)
			}
		}
	}
	if status == "copy_failed" || status == "aborted" {
		if err := transitionVideoSave(f.DB, &op, "copy_failed", nil); err != nil {
			t.Fatal(err)
		}
	}
	if status == "completed" {
		// 按旧存储合同发布真实UserAsset、事件及容量结转，不能由新代码猜测缺失的历史权益类型。
		if err := f.DB.Transaction(func(tx *gorm.DB) error {
			now := time.Now().UTC().Truncate(time.Second)
			a := assetmodel.UserAsset{UserID: op.UserID, ProductID: op.StorageProductID, AssetType: "video_file", BusinessInstanceID: &op.PublicID, Status: "active", StartedAt: &now}
			if err := assetrepo.NewAssetRepository(tx).Create(ctx, &a); err != nil {
				return err
			}
			after, remark, operator := "active", "旧版合成保存完成", op.UserID
			if err := assetrepo.NewEventRepository(tx).Create(ctx, &assetmodel.AssetEvent{AssetID: a.ID, UserID: op.UserID, EventType: "created", AfterStatus: &after, OperatorID: &operator, Remark: &remark, CreatedAt: now}); err != nil {
				return err
			}
			if err := assetrepo.NewEntitlementRepository(tx).SettleQuota(ctx, tx, entID, op.QuotaAmount, op.QuotaAmount); err != nil {
				return err
			}
			return transitionVideoSave(tx, &op, "completed", &a.ID)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if status == "aborted" {
		if err := f.DB.Model(&assetmodel.UserEntitlement{}).Where("id=?", entID).Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
			t.Fatal(err)
		}
		owner := repository.VideoOwner{UserID: op.UserID, ProjectID: op.ProjectID, APIKeyID: op.APIKeyID}
		if _, err := f.App.CleanupVideoAssetSave(ctx, op.PublicID, owner, VideoSaveCleanupPolicy{Purpose: "non_commercial_test_fixture", Version: "legacy-cleanup-fixture-v1"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.DB.Where("public_id=?", op.PublicID).Take(&op).Error; err != nil || op.Status != status {
		t.Fatal("旧保存历史状态构造失败")
	}
	return videoLegacySaveFixture{app: f.App, op: op, videoID: id, rootID: rootID, key: key, status: status}
}

// 该入口由专门运行器从89版启动；普通最新结构回归不得把跳过此项当作历史迁移通过。
func TestVideoMigration91HistoryMySQL(t *testing.T) {
	if os.Getenv("MOLIN_VIDEO_G6_MIGRATION91") != "YES" {
		t.Skip("仅在89版历史迁移专用隔离运行器执行")
	}
	db := openVideoG6MySQL(t)
	var newer int64
	if err := db.Raw("SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_asset_saves' AND column_name IN ('attempt_no','storage_entitlement_type')").Scan(&newer).Error; err != nil || newer != 0 {
		t.Fatal("历史测试必须从真实89版结构开始")
	}
	var fixtures []videoLegacySaveFixture
	for _, state := range []string{"copying", "copy_failed", "completed", "aborted"} {
		var fixture videoLegacySaveFixture
		if !t.Run("旧历史_"+state, func(t *testing.T) { fixture = seedVideoLegacySave(t, state) }) {
			return
		}
		// 子测试按既有Cleanup退休自己的条款并关闭自己的连接；新服务仅复用外部Fake存储和合成密钥材料。
		old := fixture.app
		var err error
		fixture.app, err = NewVideoHTTPService(db, VideoBillingOptions{QuoteSecret: old.billing.quoteSecret, PromptSecret: old.billing.promptSecret, IntentSecret: old.billing.intentSecret, Protector: old.billing.protector, Safety: old.billing.safety}, VideoHTTPOptions{ContentStore: old.contentStore, MediaDeleteStore: old.mediaDeleteStore, DownloadSigningSecret: old.downloadSecret, AssetSave: &VideoAssetSaveOptions{Store: old.saveStore, Policy: *old.savePolicy}})
		if err != nil {
			t.Fatal(err)
		}
		fixtures = append(fixtures, fixture)
	}
	columns := map[string][]string{}
	for _, table := range []string{"wallets", "wallet_holds", "wallet_transactions", "ai_requests", "ai_gateway_quotes", "ai_usage_items", "ai_request_wallet_links", "ai_outbox_events", "ai_gateway_task_events", "ai_gateway_provider_callback_events", "ai_gateway_tasks", "ai_gateway_assets", "ai_video_media_deletions", "ai_video_media_delete_commands", "ai_video_asset_saves", "ai_video_asset_save_commands", "user_assets", "user_entitlements", "asset_events"} {
		cols, err := db.Migrator().ColumnTypes(table)
		if err != nil {
			t.Fatal("无法读取旧列定义")
		}
		for _, c := range cols {
			columns[table] = append(columns[table], c.Name())
		}
	}
	before := videoLegacyColumnsSnapshot(t, db, columns)
	if err := runVideoMigrationSQL(db, readVideoMigration(t, "000090_video_saved_entitlement_type.up.sql")); err != nil {
		t.Fatal(err)
	}
	source := readVideoMigration(t, "000091_video_asset_save_attempts.up.sql")
	ddl := regexp.MustCompile(`(?s)ALTER TABLE\s+.*?;`).FindAllStringIndex(source, -1)
	if len(ddl) != 9 {
		t.Fatal("结构DDL数已变化，必须显式更新中断验证范围")
	}
	for i, span := range ddl {
		if !t.Run(fmt.Sprintf("结构DDL_%02d后中断", i+1), func(t *testing.T) {
			// 真实ALTER先提交，再从同一存储过程抛出错误；下一轮从原脚本开头重入，不手工撤回结构。
			mutated := source[:span[1]] + "\n SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='video_migration91_test_interrupt';\n" + source[span[1]:]
			err := runVideoMigrationSQL(db, mutated)
			var sqlErr *mysqlDriver.MySQLError
			if !errors.As(err, &sqlErr) || sqlErr.Number != 1644 || sqlErr.Message != "video_migration91_test_interrupt" {
				t.Fatalf("必须在目标DDL实际落地后中断：%v", err)
			}
			if !bytes.Equal(before, videoLegacyColumnsSnapshot(t, db, columns)) {
				t.Fatal("中断重入不得改变任何旧业务列")
			}
			err = db.Table("ai_video_asset_save_commands").Where("task_id=?", fixtures[0].op.TaskID).Update("command_key_hash", strings.Repeat("0", 64)).Error
			if !errors.As(err, &sqlErr) || sqlErr.Number != 1644 {
				t.Fatal("迁移中断期间旧命令字段仍须受保护")
			}
		}) {
			return
		}
	}
	for _, script := range []string{source, readVideoMigration(t, "000091_video_asset_save_attempts.down.sql"), source} {
		if err := runVideoMigrationSQL(db, script); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(before, videoLegacyColumnsSnapshot(t, db, columns)) {
		t.Fatal("完整迁移及保留式down/up必须保持全部旧列")
	}
	verifyVideoMigration91Structure(t, db)
	var unknown int64
	if err := db.Table("ai_video_asset_saves").Where("storage_entitlement_type IS NULL").Count(&unknown).Error; err != nil || unknown != 4 {
		t.Fatal("不得用当前权益类型回填四份旧NULL历史")
	}
	for _, fixture := range fixtures {
		var op videoAssetSave
		var command videoAssetSaveCommand
		if err := db.Where("public_id=?", fixture.op.PublicID).Take(&op).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Where("task_id=?", op.TaskID).Take(&command).Error; err != nil {
			t.Fatal(err)
		}
		if op.AttemptNo != 1 || op.PreviousSaveID != nil || op.Status != fixture.status || command.SavePublicID != op.PublicID {
			t.Fatal("旧命令必须确定性绑定原首个尝试且不改状态")
		}
		caller := VideoCaller{UserID: op.UserID, ProjectID: op.ProjectID, APIKeyID: *op.APIKeyID}
		if op.Status == "completed" {
			if _, err := fixture.app.SavedVideoDownloadURL(context.Background(), caller, *op.SavedUserAssetID, "content"); !errors.Is(err, ErrVideoEntitlementDenied) {
				t.Fatal("缺原权益类型事实的旧完成资产必须失败关闭")
			}
		}
		if op.Status == "copying" || op.Status == "copy_failed" {
			for _, key := range []string{fixture.key, fixture.key + "-new"} {
				if reply, err := fixture.app.SaveVideoAsset(context.Background(), caller, fixture.rootID, key); err == nil || reply != nil {
					t.Fatal("缺原权益类型的旧未完成计划不得用当前配置继续发布")
				}
				if !bytes.Equal(before, videoLegacyColumnsSnapshot(t, db, columns)) {
					t.Fatal("旧NULL计划恢复被拒绝时，新旧键均不得追加命令或改变业务事实")
				}
			}
		}
	}
	if !bytes.Equal(before, videoLegacyColumnsSnapshot(t, db, columns)) {
		t.Fatal("旧NULL读取及恢复拒绝不得偷偷改变历史或容量")
	}
	verifyVideoMigration91Identity(t, db, fixtures)
	if !bytes.Equal(before, videoLegacyColumnsSnapshot(t, db, columns)) {
		t.Fatal("身份约束探针回滚后不得留下业务或容量变化")
	}
}

// 按合同直接核对结构，不把脚本未报错等同于关键约束实际存在。
func verifyVideoMigration91Structure(t *testing.T, db *gorm.DB) {
	t.Helper()
	for name, want := range map[string][]string{"PRIMARY": {"public_id"}, "uk_video_save_attempt_owner": {"public_id", "task_id", "user_id", "project_id"}, "uk_video_save_task_attempt": {"task_id", "attempt_no"}, "uk_video_save_previous": {"previous_save_id"}, "uk_video_save_live_task": {"live_task_id"}} {
		var rows []struct {
			ColumnName string
			NonUnique  int
		}
		if err := db.Raw("SELECT column_name,non_unique FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name='ai_video_asset_saves' AND index_name=? ORDER BY seq_in_index", name).Scan(&rows).Error; err != nil || len(rows) != len(want) {
			t.Fatal("关键保存唯一索引缺失或列数错误")
		}
		for i, row := range rows {
			if row.ColumnName != want[i] || row.NonUnique != 0 {
				t.Fatal("唯一索引列顺序或唯一性错误")
			}
		}
	}
	for name, spec := range map[string]struct {
		Table, Parent       string
		Columns, Referenced []string
	}{
		"fk_video_save_task":             {"ai_video_asset_saves", "ai_gateway_tasks", []string{"task_id", "request_id", "user_id", "project_id"}, []string{"id", "request_id", "user_id", "project_id"}},
		"fk_video_save_previous_attempt": {"ai_video_asset_saves", "ai_video_asset_saves", []string{"previous_save_id"}, []string{"public_id"}},
		"fk_video_save_command_attempt":  {"ai_video_asset_save_commands", "ai_video_asset_saves", []string{"save_public_id", "task_id", "user_id", "project_id"}, []string{"public_id", "task_id", "user_id", "project_id"}},
	} {
		var rows []struct{ ColumnName, ReferencedTableName, ReferencedColumnName string }
		if err := db.Raw("SELECT column_name,referenced_table_name,referenced_column_name FROM information_schema.key_column_usage WHERE constraint_schema=DATABASE() AND table_name=? AND constraint_name=? ORDER BY ordinal_position", spec.Table, name).Scan(&rows).Error; err != nil || len(rows) != len(spec.Columns) {
			t.Fatal("保存尝试外键缺失或列数错误")
		}
		for i, row := range rows {
			if row.ColumnName != spec.Columns[i] || row.ReferencedTableName != spec.Parent || row.ReferencedColumnName != spec.Referenced[i] {
				t.Fatal("保存尝试外键归属列不正确")
			}
		}
	}
	var nullable string
	if err := db.Raw("SELECT is_nullable FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='ai_video_asset_save_commands' AND column_name='save_public_id'").Scan(&nullable).Error; err != nil || nullable != "NO" {
		t.Fatal("迁移后命令不得缺少尝试引用")
	}
	var live []struct {
		TaskID     uint64
		Status     string
		LiveTaskID *uint64
	}
	if err := db.Table("ai_video_asset_saves").Select("task_id,status,live_task_id").Find(&live).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range live {
		if row.Status == "aborted" {
			if row.LiveTaskID != nil {
				t.Fatal("终止历史不得占有效位置")
			}
		} else if row.LiveTaskID == nil || *row.LiveTaskID != row.TaskID {
			t.Fatal("非终止历史必须占原Task唯一位置")
		}
	}
}

// 在回滚事务内验证新尝试与命令的身份守卫；不关闭触发器，不留下真实副本或额外容量。
func verifyVideoMigration91Identity(t *testing.T, db *gorm.DB, fixtures []videoLegacySaveFixture) {
	t.Helper()
	base := fixtures[3].op
	for _, kind := range []string{"合法后继回滚", "错误前驱", "跳号", "缺少前驱", "错误Key", "孤儿命令", "跨Task命令"} {
		if !t.Run(kind, func(t *testing.T) {
			rollback := errors.New("合成身份探针回滚")
			err := db.Transaction(func(tx *gorm.DB) error {
				ctx := context.Background()
				if err := tx.Model(&assetmodel.UserEntitlement{}).Where("id=?", base.StorageEntitlementID).Update("expires_at", time.Now().UTC().Add(time.Hour)).Error; err != nil {
					return err
				}
				if err := assetrepo.NewEntitlementRepository(tx).ReserveQuota(ctx, tx, base.StorageEntitlementID, base.QuotaAmount); err != nil {
					return err
				}
				op := base
				op.PublicID = fmt.Sprintf("vsave_identity_%d", base.TaskID)
				op.AttemptNo, op.PreviousSaveID = 2, &base.PublicID
				op.Status, op.VersionNo, op.CreatedAt = "copying", 1, time.Now().UTC()
				op.StorageEntitlementType = "storage_bytes"
				op.SavedUserAssetID, op.CompletedAt = nil, nil
				op.CleanupPolicyVersion, op.CleanupReason, op.CleanupEligibleAt, op.CleanupStartedAt, op.CleanupFinishedAt, op.CleanupProofSHA256 = nil, nil, nil, nil, nil, nil
				plan, err := decodeVideoSavePlan(&base)
				if err != nil {
					return err
				}
				for i := range plan {
					plan[i].TargetKey = op.PublicID + "/" + plan[i].PublicID + "/" + plan[i].Role + ".bin"
				}
				op.PlanJSON, err = json.Marshal(plan)
				if err != nil {
					return err
				}
				op.PlanSHA256 = videoPayloadSHA256(op.PlanJSON)
				switch kind {
				case "错误前驱":
					op.PreviousSaveID = &fixtures[2].op.PublicID
				case "跳号":
					op.AttemptNo = 3
				case "缺少前驱":
					op.PreviousSaveID = nil
				case "错误Key":
					other := base.UserID + 9000000
					op.APIKeyID = &other
				}
				err = tx.Create(&op).Error
				if kind == "合法后继回滚" || kind == "孤儿命令" || kind == "跨Task命令" {
					if err != nil {
						return err
					}
					if kind == "合法后继回滚" {
						return rollback
					}
					ref := "vsave_missing_fixture"
					if kind == "跨Task命令" {
						ref = fixtures[2].op.PublicID
					}
					command := videoAssetSaveCommand{UserID: base.UserID, ProjectID: base.ProjectID, TaskID: base.TaskID, APIKeyID: base.APIKeyID, SavePublicID: ref, CommandKeyHash: videoBillingDigest("migration-identity-" + kind), CreatedAt: time.Now().UTC()}
					err = tx.Create(&command).Error
				}
				var sqlErr *mysqlDriver.MySQLError
				if !errors.As(err, &sqlErr) || sqlErr.Number != 1644 {
					return fmt.Errorf("身份探针应被1644拒绝：%v", err)
				}
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		}) {
			return
		}
	}
}

func videoLegacyColumnsSnapshot(t *testing.T, db *gorm.DB, columns map[string][]string) []byte {
	t.Helper()
	facts := map[string][]string{}
	for table, cols := range columns {
		var rows []map[string]any
		if err := db.Table(table).Select(cols).Find(&rows).Error; err != nil {
			t.Fatal("无法读取旧业务列快照")
		}
		for _, row := range rows {
			body, err := json.Marshal(row)
			if err != nil {
				t.Fatal("无法编码旧列快照")
			}
			facts[table] = append(facts[table], string(body))
		}
		sort.Strings(facts[table])
	}
	body, err := json.Marshal(facts)
	if err != nil {
		t.Fatal("无法编码历史事实集合")
	}
	return body
}

func readVideoMigration(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "migrations", name))
	if err != nil {
		t.Fatal("无法读取冻结SQL迁移")
	}
	return strings.ReplaceAll(string(body), "\r\n", "\n")
}

// 仅执行本测试显式选定的仓库SQL，识别行末DELIMITER；不打印SQL、原始行或连接凭据。
func runVideoMigrationSQL(db *gorm.DB, source string) error {
	delimiter := ";"
	var pending strings.Builder
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		if strings.HasPrefix(line, "DELIMITER ") {
			if strings.TrimSpace(pending.String()) != "" {
				return errors.New("迁移分隔符之前存在未完成语句")
			}
			delimiter = strings.TrimSpace(strings.TrimPrefix(line, "DELIMITER "))
			continue
		}
		if strings.HasSuffix(line, delimiter) {
			pending.WriteString(strings.TrimSuffix(line, delimiter))
			if err := db.Exec(pending.String()).Error; err != nil {
				return err
			}
			pending.Reset()
		} else {
			pending.WriteString(line)
			pending.WriteByte('\n')
		}
	}
	if strings.TrimSpace(pending.String()) != "" {
		return errors.New("迁移末尾存在未完成语句")
	}
	return nil
}
