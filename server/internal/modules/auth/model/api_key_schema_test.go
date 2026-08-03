package model

import (
	"sync"
	"testing"

	"gorm.io/gorm/schema"
)

func TestAPIKeyGORMCompositeOwnerIndex(t *testing.T) {
	parsed, err := schema.Parse(&APIKey{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatalf("解析 APIKey GORM Schema 失败: %v", err)
	}
	var index *schema.Index
	for _, candidate := range parsed.ParseIndexes() {
		if candidate.Name == "uk_api_keys_id_user" {
			index = candidate
			break
		}
	}
	if index == nil || index.Class != "UNIQUE" || len(index.Fields) != 2 || index.Fields[0].DBName != "id" || index.Fields[1].DBName != "user_id" {
		t.Fatalf("APIKey GORM 复合归属索引与 Migration 不一致: %+v", index)
	}
}
