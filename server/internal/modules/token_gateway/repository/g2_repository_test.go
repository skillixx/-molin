package repository

import (
	"sync"
	"testing"

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
