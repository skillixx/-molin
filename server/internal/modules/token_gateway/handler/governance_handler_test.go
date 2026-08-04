package handler

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/service"
)

type governanceAuditCapture struct {
	summary any
}

func (c *governanceAuditCapture) Record(_ context.Context, _ *uint64, _, _ string, _, _ *string, _ string, summary any) error {
	c.summary = summary
	return nil
}

type governanceOutboxRequeuer struct{}

func (governanceOutboxRequeuer) RequeueDead(context.Context, string, time.Time) error { return nil }

func TestGovernanceHandlerAuditsDeadOutboxReason(t *testing.T) {
	audit := &governanceAuditCapture{}
	admin := service.NewGovernanceAdminService(nil).WithOutboxDeadRequeuer(governanceOutboxRequeuer{})
	handler := NewGovernanceHandler(admin, audit)
	req := httptest.NewRequest("POST", "/api/admin/token/outbox-events/event-1/requeue", strings.NewReader(`{"reason":"  已核对钱包终态  "}`))
	req.SetPathValue("event_id", "event-1")
	recorder := httptest.NewRecorder()
	handler.RequeueDeadOutbox(recorder, req)
	if recorder.Code != 200 {
		t.Fatalf("死信重试失败: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	summary, ok := audit.summary.(map[string]interface{})
	if !ok || summary["reason"] != "已核对钱包终态" {
		t.Fatalf("审计必须保留受控重试原因: %#v", audit.summary)
	}
}

func TestDecodeGovernanceJSONRejectsTrailingDocument(t *testing.T) {
	req := httptest.NewRequest("POST", "/", strings.NewReader(`{"version_no":1}{"version_no":2}`))
	recorder := httptest.NewRecorder()
	var target versionRequest
	if decodeGovernanceJSON(recorder, req, &target) {
		t.Fatal("治理写接口不得接受尾随 JSON 文档")
	}
	if recorder.Code != 400 {
		t.Fatalf("尾随 JSON 应返回 400，实际 %d", recorder.Code)
	}
}
