package service

import (
	"gorm.io/gorm/schema"
	"sync"
	"testing"
)

func TestVideoG6AdminArchiveCommandSchema(t *testing.T) {
	s, err := schema.Parse(&videoAdminArchiveRecord{}, &sync.Map{}, schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"id", "public_id", "actor_user_id", "task_id", "request_id", "initial_version", "archive_generation", "initial_phase", "ciphertext", "before_audit_id", "after_audit_id"} {
		if s.LookUpField(column) == nil {
			t.Fatalf("归档持久化映射缺少%s", column)
		}
	}
}
