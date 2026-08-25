package service

import (
	"testing"

	"molin/server/internal/modules/token_gateway/model"
)

func TestResourceLimiterConcurrencyCeilingsCannotBeRelaxed(t *testing.T) {
	defaults := ResourceDefaults{
		User: ResourceLimits{Concurrency: 100, RPM: 1000, TPM: 1000}, Project: ResourceLimits{Concurrency: 100, RPM: 1000, TPM: 1000},
		APIKey: ResourceLimits{Concurrency: 50, RPM: 1000, TPM: 1000}, Model: ResourceLimits{Concurrency: 500, RPM: 1000, TPM: 1000},
	}
	ceilings := &ResourceDefaults{
		User: ResourceLimits{Concurrency: 1}, Project: ResourceLimits{Concurrency: 2},
		APIKey: ResourceLimits{Concurrency: 1}, Model: ResourceLimits{Concurrency: 4},
	}
	policies := map[string]model.AIResourcePolicy{
		"user": {ConcurrencyLimit: 999, RPMLimit: 900, TPMLimit: 900}, "project": {ConcurrencyLimit: 999, RPMLimit: 900, TPMLimit: 900},
		"api_key": {ConcurrencyLimit: 999, RPMLimit: 900, TPMLimit: 900}, "model": {ConcurrencyLimit: 999, RPMLimit: 900, TPMLimit: 900},
	}
	limits, err := effectiveResourceLimits(defaults, ceilings, policies)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{1, 2, 1, 4}
	for index, expected := range want {
		if limits[index].Concurrency != expected {
			t.Fatalf("第%d层高默认值和高数据库策略必须被硬上限收紧: got=%d want=%d", index, limits[index].Concurrency, expected)
		}
	}

	policies["project"] = model.AIResourcePolicy{ConcurrencyLimit: 1, RPMLimit: 900, TPMLimit: 900}
	limits, err = effectiveResourceLimits(defaults, ceilings, policies)
	if err != nil || limits[1].Concurrency != 1 {
		t.Fatalf("数据库策略允许继续收紧但不能放宽: limits=%+v err=%v", limits, err)
	}
}

func TestResourceLimiterRestoreTicketUsesFourFrozenScopes(t *testing.T) {
	limiter := &ResourceLimiter{}
	ticket, err := limiter.RestoreTicket("img_req_restore_0001", 11, 22, 33, "molin/image")
	if err != nil {
		t.Fatal(err)
	}
	if ticket.LeaseID != "img_req_restore_0001" || len(ticket.Scopes) != 4 || len(ticket.Keys) != 20 {
		t.Fatalf("恢复票据结构错误: %+v", ticket)
	}
	wantScopes := []string{"user", "project", "api_key", "model"}
	wantPrefixes := []string{
		"molin:{ai-g4}:user:11",
		"molin:{ai-g4}:project:22",
		"molin:{ai-g4}:api_key:33",
		"molin:{ai-g4}:model:molin/image",
	}
	for index, scope := range wantScopes {
		if ticket.Scopes[index] != scope || ticket.Keys[index*4] != wantPrefixes[index]+":concurrency" {
			t.Fatalf("第%d层票据错误: scopes=%v keys=%v", index, ticket.Scopes, ticket.Keys)
		}
	}
}

func TestResourceLimiterRestoreTicketRejectsIncompleteSubject(t *testing.T) {
	limiter := &ResourceLimiter{}
	tests := []struct {
		name      string
		requestID string
		userID    uint64
		projectID uint64
		apiKeyID  uint64
		modelCode string
	}{
		{name: "缺少请求", userID: 1, projectID: 2, apiKeyID: 3, modelCode: "molin/image"},
		{name: "缺少用户", requestID: "img_req_1", projectID: 2, apiKeyID: 3, modelCode: "molin/image"},
		{name: "缺少项目", requestID: "img_req_1", userID: 1, apiKeyID: 3, modelCode: "molin/image"},
		{name: "缺少密钥作用域", requestID: "img_req_1", userID: 1, projectID: 2, modelCode: "molin/image"},
		{name: "缺少模型", requestID: "img_req_1", userID: 1, projectID: 2, apiKeyID: 3},
		{name: "请求含空白", requestID: " img_req_1", userID: 1, projectID: 2, apiKeyID: 3, modelCode: "molin/image"},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			if _, err := limiter.RestoreTicket(item.requestID, item.userID, item.projectID, item.apiKeyID, item.modelCode); err == nil {
				t.Fatal("不完整或非规范主体不得恢复租约")
			}
		})
	}
}
