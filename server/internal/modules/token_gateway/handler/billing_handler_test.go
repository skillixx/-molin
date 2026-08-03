package handler

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
)

type fakeBillingExceptionResolver struct {
	called bool
	usage  service.ExecutionUsage
	err    error
}

func (f *fakeBillingExceptionResolver) ResolveException(_ context.Context, _ string, _ string, usage service.ExecutionUsage) error {
	f.called, f.usage = true, usage
	return f.err
}

type fakeBillingAuditRecorder struct {
	err    error
	errors []error
	calls  int
}

func (f *fakeBillingAuditRecorder) Record(context.Context, *uint64, string, string, *string, *string, string, any) error {
	f.calls++
	if f.calls <= len(f.errors) {
		return f.errors[f.calls-1]
	}
	return f.err
}

func TestBillingHandlerFailsClosedWhenAuditUnavailable(t *testing.T) {
	resolver := &fakeBillingExceptionResolver{}
	audit := &fakeBillingAuditRecorder{err: errors.New("audit down")}
	handler := NewBillingHandler(resolver, audit)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/token/billing/exceptions/req-1/resolve", strings.NewReader(`{"resolution":"release"}`))
	req.SetPathValue("request_id", "req-1")
	recorder := httptest.NewRecorder()
	handler.ResolveException(recorder, req)
	if recorder.Code != http.StatusInternalServerError || resolver.called {
		t.Fatalf("审计失败必须在资金操作前拒绝: status=%d called=%t", recorder.Code, resolver.called)
	}
}

func TestBillingHandlerSettlesWithAuditedUsage(t *testing.T) {
	resolver := &fakeBillingExceptionResolver{}
	audit := &fakeBillingAuditRecorder{}
	handler := NewBillingHandler(resolver, audit)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/token/billing/exceptions/req-2/resolve", strings.NewReader(`{"resolution":"settle","prompt_tokens":10,"completion_tokens":5}`))
	req.SetPathValue("request_id", "req-2")
	recorder := httptest.NewRecorder()
	handler.ResolveException(recorder, req)
	if recorder.Code != http.StatusOK || !resolver.called || !resolver.usage.Present || resolver.usage.PromptTokens != 10 || audit.calls != 2 {
		t.Fatalf("人工结算入口未完成审计与核定: status=%d called=%t usage=%+v audits=%d", recorder.Code, resolver.called, resolver.usage, audit.calls)
	}
}

func TestBillingHandlerKeepsSuccessAndRedactsTerminalAuditFailure(t *testing.T) {
	resolver := &fakeBillingExceptionResolver{}
	audit := &fakeBillingAuditRecorder{errors: []error{nil, errors.New("数据库密码和 Usage 不得进入日志")}}
	handler := NewBillingHandler(resolver, audit)
	var logs bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(originalWriter) })
	req := httptest.NewRequest(http.MethodPost, "/api/admin/token/billing/exceptions/req-audit-warning/resolve", strings.NewReader(`{"resolution":"settle","prompt_tokens":10,"completion_tokens":5}`))
	req.SetPathValue("request_id", "req-audit-warning")
	recorder := httptest.NewRecorder()
	handler.ResolveException(recorder, req)
	if recorder.Code != http.StatusOK || !resolver.called || audit.calls != 2 {
		t.Fatalf("资金终态成功后审计故障不得伪装操作失败: status=%d called=%t audits=%d", recorder.Code, resolver.called, audit.calls)
	}
	output := logs.String()
	if !strings.Contains(output, "request_id=req-audit-warning") || strings.Contains(output, "数据库密码") || strings.Contains(output, "prompt_tokens") {
		t.Fatalf("终态审计告警必须可定位且保持脱敏: %s", output)
	}
}

func TestBillingHandlerRejectsZeroUsageSettle(t *testing.T) {
	resolver := &fakeBillingExceptionResolver{err: service.ErrBillingAmountException}
	audit := &fakeBillingAuditRecorder{}
	handler := NewBillingHandler(resolver, audit)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/token/billing/exceptions/req-zero/resolve", strings.NewReader(`{"resolution":"settle"}`))
	req.SetPathValue("request_id", "req-zero")
	recorder := httptest.NewRecorder()
	handler.ResolveException(recorder, req)
	if recorder.Code != http.StatusBadRequest || !resolver.called || audit.calls != 1 {
		t.Fatalf("零用量必须由锁定真实状态后的服务拒绝: status=%d called=%t audits=%d", recorder.Code, resolver.called, audit.calls)
	}
}

func TestBillingHandlerRejectsReleaseWithUsage(t *testing.T) {
	resolver := &fakeBillingExceptionResolver{err: service.ErrBillingAmountException}
	audit := &fakeBillingAuditRecorder{}
	handler := NewBillingHandler(resolver, audit)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/token/billing/exceptions/req-release/resolve", strings.NewReader(`{"resolution":"release","prompt_tokens":10}`))
	req.SetPathValue("request_id", "req-release")
	recorder := httptest.NewRecorder()
	handler.ResolveException(recorder, req)
	if recorder.Code != http.StatusBadRequest || !resolver.called || resolver.usage.PromptTokens != 10 || audit.calls != 1 {
		t.Fatalf("release 携带 Usage 必须保留审计事实并拒绝: status=%d called=%t usage=%+v audits=%d", recorder.Code, resolver.called, resolver.usage, audit.calls)
	}
}

func TestBillingHandlerKeepsTerminalConflictPriority(t *testing.T) {
	resolver := &fakeBillingExceptionResolver{err: repository.ErrRequestStateConflict}
	handler := NewBillingHandler(resolver, &fakeBillingAuditRecorder{})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/token/billing/exceptions/req-terminal/resolve", strings.NewReader(`{"resolution":"settle"}`))
	req.SetPathValue("request_id", "req-terminal")
	recorder := httptest.NewRecorder()
	handler.ResolveException(recorder, req)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("服务确认已有终态时 HTTP 必须返回 409: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
