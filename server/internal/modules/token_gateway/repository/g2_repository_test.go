package repository

import (
	"context"
	"regexp"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestG2AccessSnapshotRowKeepsRawScanFlat(t *testing.T) {
	parsed, err := schema.Parse(&g2AccessSnapshotRow{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("授权快照原始行必须能被 GORM 作为扁平结构解析: %v", err)
	}
	if len(parsed.Relationships.Relations) != 0 {
		t.Fatalf("授权快照原始行不得包含 GORM 关联关系: %+v", parsed.Relationships.Relations)
	}
	if parsed.LookUpField("TokenModel") != nil {
		t.Fatal("TokenModel 必须在授权 SQL 扫描完成后单独加载，不能回到原始扫描结构")
	}
}

func TestActiveScopedModelsExistAllowsPublishedChatAndImage(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	query := "SELECT COUNT(DISTINCT(`logical_model_code`)) FROM `token_models` WHERE logical_model_code IN (?,?) AND status = 'active' AND modality IN ('chat','image') AND release_version_no > 0 AND published_at IS NOT NULL"
	mock.ExpectQuery(regexp.QuoteMeta(query)).
		WithArgs("molin/qwen-turbo", "bytedance-seed/seedream-5-0-lite").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	ok, err := NewG2Repository(db).ActiveScopedModelsExist(context.Background(), []string{"molin/qwen-turbo", "bytedance-seed/seedream-5-0-lite"})
	if err != nil || !ok {
		t.Fatalf("已发布Chat和图片模型必须共同进入显式scope: ok=%t err=%v", ok, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
