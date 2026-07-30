package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeEmailBootstrapBodyIsStrict(t *testing.T) {
	valid := httptest.NewRequest("POST", "/", strings.NewReader(`{"provider_template_id":"00123456"}`))
	if got, err := decodeEmailBootstrapBody(httptest.NewRecorder(), valid); err != nil || got != "00123456" {
		t.Fatalf("合法 Body 解析失败: %q %v", got, err)
	}
	boundaryValue := "0" + strings.Repeat("1", 63)
	boundary := httptest.NewRequest("POST", "/", strings.NewReader(fmt.Sprintf(`{"provider_template_id":%q}`, boundaryValue)))
	if got, err := decodeEmailBootstrapBody(httptest.NewRecorder(), boundary); err != nil || got != boundaryValue {
		t.Fatalf("六十四字节边界值必须按原值保留: got=%q err=%v", got, err)
	}
	for _, body := range []string{
		`{}`, `[]`, `{"provider_template_id":""}`, `{"provider_template_id":" 1"}`,
		`{"provider_template_id":"0"}`, `{"provider_template_id":"000"}`,
		`{"provider_template_id":"abc"}`, `{"provider_template_id":"-1"}`,
		`{"provider_template_id":"+1"}`, `{"provider_template_id":"1.0"}`,
		`{"provider_template_id":"1e2"}`,
		fmt.Sprintf(`{"provider_template_id":"%s"}`, strings.Repeat("1", 65)),
		`{"provider_template_id":"a","extra":1}`,
		`{"provider_template_id":"a","provider_template_id":"b"}`,
		`{"provider_template_id":"a"}{"provider_template_id":"b"}`,
	} {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		if _, err := decodeEmailBootstrapBody(httptest.NewRecorder(), req); err == nil {
			t.Fatalf("非法 Body 必须拒绝: %s", body)
		}
	}
}

func TestConfigureAdminVerifyRejectsInvalidProviderTemplateIDBeforeService(t *testing.T) {
	// svc 故意保持为空；若任一非法编号越过请求校验，测试会因调用空服务而失败。
	handler := NewEmailBootstrapHandler(nil)
	invalidValues := []string{"", "0", "000", "abc", "-1", "+1", "1.0", "1e2", " 1", strings.Repeat("1", 65)}
	for _, value := range invalidValues {
		t.Run(fmt.Sprintf("value_%q", value), func(t *testing.T) {
			body := fmt.Sprintf(`{"provider_template_id":%q}`, value)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", "idempotency-key-0001")
			resp := httptest.NewRecorder()

			handler.ConfigureAdminVerify(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("非法模板编号必须返回 400: value=%q status=%d body=%s", value, resp.Code, resp.Body.String())
			}
		})
	}
}

func TestBootstrapContentTypeOnlyAcceptsJSONUTF8(t *testing.T) {
	for _, valid := range []string{"application/json", "application/json; charset=utf-8", "application/json;charset=UTF-8"} {
		if !validBootstrapContentType(valid) {
			t.Fatalf("合法媒体类型被拒绝: %s", valid)
		}
	}
	for _, invalid := range []string{"", "text/json", "application/json; charset=gbk", "application/json; profile=x"} {
		if validBootstrapContentType(invalid) {
			t.Fatalf("非法媒体类型被接受: %s", invalid)
		}
	}
}
