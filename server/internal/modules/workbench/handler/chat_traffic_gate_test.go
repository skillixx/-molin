package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	tokengatewaysvc "molin/server/internal/modules/token_gateway/service"
)

func TestWriteChatErrorTrafficClosed(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeChatError(recorder, tokengatewaysvc.ErrTrafficClosed)

	if recorder.Code != 503 || !strings.Contains(recorder.Body.String(), `"code":50330`) {
		t.Fatalf("商业总闸错误应映射为 503/50330，实际 status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
