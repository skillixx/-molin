package handler

import (
	"net/http/httptest"
	"strings"
	"testing"
)

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
