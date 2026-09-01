package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"molin/server/internal/modules/token_gateway/service"
)

// 应用层Head失败必须只输出一次兼容错误，不能在503正文后追加默认500。
func TestVideoG6ContentHTTPApplicationErrorSingleEnvelope(t *testing.T) {
	r := httptest.NewRequest("GET", "/v1/videos/video_fixture/content", nil)
	w := httptest.NewRecorder()
	writeVideoAPIError(w, r, service.ErrVideoContentUnavailable)
	if w.Code != 503 {
		t.Fatalf("应为503，实际%d", w.Code)
	}
	decoder := json.NewDecoder(w.Body)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := decoder.Decode(&body); err != nil || body.Error.Code != "video_content_unavailable" {
		t.Fatal("缺少稳定低敏错误")
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		t.Fatal("错误正文后不能追加第二段JSON")
	}
}
