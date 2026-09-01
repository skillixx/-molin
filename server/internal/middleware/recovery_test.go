package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 普通业务panic继续返回低敏500；流式中断哨兵必须由net/http接管，不能追加JSON。
func TestRecoveryPreservesStreamingAbortAndOrdinaryFailure(t *testing.T) {
	t.Run("普通异常保持原错误合同", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		Recovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("内部敏感异常") })).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
		var body struct {
			Code    int
			Message string
		}
		if json.Unmarshal(recorder.Body.Bytes(), &body) != nil || recorder.Code != 500 || body.Code != 50000 || body.Message != "internal error" {
			t.Fatal("普通恢复合同变化")
		}
	})
	t.Run("中断哨兵保持原值", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		defer func() {
			if recover() != http.ErrAbortHandler {
				t.Error("流式中断哨兵被吞掉")
			}
			if recorder.Body.Len() != 0 {
				t.Error("中断后写入了JSON")
			}
		}()
		Recovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	})
}
