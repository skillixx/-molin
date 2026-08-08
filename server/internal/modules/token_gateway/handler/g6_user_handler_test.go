package handler

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

type g6AuditEvent struct {
	action     string
	targetType string
	targetID   string
	summary    any
}

type g6AuditCapture struct {
	events           []g6AuditEvent
	failSecretRecord bool
}

func (c *g6AuditCapture) Record(_ context.Context, _ *uint64, _, action string, targetType, targetID *string, _ string, summary any) error {
	if action == "secret_leak_detected" && c.failSecretRecord {
		return errors.New("审计存储不可用")
	}
	event := g6AuditEvent{action: action, summary: summary}
	if targetType != nil {
		event.targetType = *targetType
	}
	if targetID != nil {
		event.targetID = *targetID
	}
	c.events = append(c.events, event)
	return nil
}

func TestCSVSafeBlocksFormulaInjection(t *testing.T) {
	tests := map[string]string{
		"=HYPERLINK(\"https://invalid\")": "'=HYPERLINK(\"https://invalid\")",
		" +SUM(1,1)":                      "' +SUM(1,1)",
		"@cmd":                            "'@cmd",
		"req_123":                         "req_123",
	}
	for input, want := range tests {
		if got := csvSafe(input); got != want {
			t.Fatalf("CSV 单元格防护错误 input=%q got=%q want=%q", input, got, want)
		}
	}
}

func TestExportAuditSummaryContainsFilters(t *testing.T) {
	projectID, keyID := uint64(12), uint64(34)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	end := start.Add(24 * time.Hour)
	summary := exportAuditSummary(repository.G6RequestFilter{
		ProjectID: &projectID, APIKeyID: &keyID, LogicalModelCode: "molin/test", Status: "settled", Start: &start, End: &end,
	}, 8)
	if summary["count"] != 8 || summary["project_id"] != projectID || summary["api_key_id"] != keyID || summary["model_filter_set"] != true || summary["model_filter_sha256"] == "" || summary["status"] != "settled" {
		t.Fatalf("导出审计筛选摘要不完整: %+v", summary)
	}
	if _, exists := summary["model"]; exists {
		t.Fatalf("审计摘要不得保存用户提交的模型筛选原文: %+v", summary)
	}
	if summary["start"] != "2026-07-31T16:00:00Z" || summary["end"] != "2026-08-01T16:00:00Z" {
		t.Fatalf("导出审计时间范围必须统一为 UTC: %+v", summary)
	}
}

func TestCreateDisputePersistsRedactedSecretFinding(t *testing.T) {
	audit := &g6AuditCapture{}
	handler := NewG6UserHandler(nil, audit)
	handler.createDispute = func(context.Context, uint64, string, string) (*dto.BillingDisputeResp, error) {
		return nil, &service.ConfirmedCredentialLeakError{APIKeyID: 701}
	}
	request := httptest.NewRequest("POST", "/api/token/customer/requests/req-secret/disputes", strings.NewReader(`{"reason":"账单异常，api_key=supersecretvalue，请协助核查"}`))
	request.SetPathValue("request_id", "req-secret")
	recorder := httptest.NewRecorder()

	handler.CreateDispute(recorder, request)

	if recorder.Code != 400 {
		t.Fatalf("包含密钥的申诉必须被拒绝: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if len(audit.events) != 2 || audit.events[0].action != "billing_dispute.submit_attempt" || audit.events[1].action != "secret_leak_detected" {
		t.Fatalf("密钥发现必须写入独立审计事件: %+v", audit.events)
	}
	summary, ok := audit.events[1].summary.(map[string]any)
	if !ok || audit.events[1].targetType != "api_key" || audit.events[1].targetID != "701" || summary["source"] != "billing_dispute" || summary["blocked"] != true || summary["confirmed"] != true || len(summary) != 3 {
		t.Fatalf("确认泄漏审计只能保存脱敏固定字段: %#v", audit.events[1].summary)
	}
	for _, event := range audit.events {
		if strings.Contains(strings.ToLower(event.action+" "+strings.TrimSpace(recorder.Body.String())), "supersecretvalue") {
			t.Fatalf("响应或审计摘要不得包含密钥原文: action=%s summary=%#v", event.action, event.summary)
		}
		if summary, ok := event.summary.(map[string]any); ok {
			for _, value := range summary {
				if text, ok := value.(string); ok && strings.Contains(strings.ToLower(text), "supersecretvalue") {
					t.Fatalf("审计摘要不得包含密钥原文: action=%s summary=%#v", event.action, event.summary)
				}
			}
		}
	}
}

func TestCreateDisputeFailsClosedWhenSecretAuditUnavailable(t *testing.T) {
	audit := &g6AuditCapture{failSecretRecord: true}
	handler := NewG6UserHandler(nil, audit)
	handler.createDispute = func(context.Context, uint64, string, string) (*dto.BillingDisputeResp, error) {
		return nil, &service.ConfirmedCredentialLeakError{APIKeyID: 701}
	}
	request := httptest.NewRequest("POST", "/api/token/customer/requests/req-secret/disputes", strings.NewReader(`{"reason":"账单异常，api_key=supersecretvalue，请协助核查"}`))
	request.SetPathValue("request_id", "req-secret")
	recorder := httptest.NewRecorder()

	handler.CreateDispute(recorder, request)

	if recorder.Code != 500 || !strings.Contains(recorder.Body.String(), "安全审计失败") {
		t.Fatalf("密钥审计失败时必须失败关闭: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateDisputeDoesNotEscalateUnverifiedSecretText(t *testing.T) {
	audit := &g6AuditCapture{}
	handler := NewG6UserHandler(nil, audit)
	handler.createDispute = func(context.Context, uint64, string, string) (*dto.BillingDisputeResp, error) {
		return nil, service.ErrDisputeContainsUnverifiedSecret
	}
	request := httptest.NewRequest("POST", "/api/token/customer/requests/req-secret/disputes", strings.NewReader(`{"reason":"账单异常，api_key=fabricatedvalue，请协助核查"}`))
	request.SetPathValue("request_id", "req-secret")
	recorder := httptest.NewRecorder()

	handler.CreateDispute(recorder, request)

	if recorder.Code != 400 || len(audit.events) != 1 || audit.events[0].action != "billing_dispute.submit_attempt" {
		t.Fatalf("未确认文本必须拒绝但不得污染 P0 数据源: status=%d events=%+v", recorder.Code, audit.events)
	}
}
