package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/response"
)

func TestVideoG6QueueAdmissionErrorEnvelopes(t *testing.T) {
	for _, test := range []struct {
		name, path string
		platform   bool
	}{
		{name: "OpenAI兼容", path: "/v1/videos"},
		{name: "平台", path: "/api/token/videos/generations", platform: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest("POST", test.path, nil)
			writeVideoAPIError(recorder, request, &service.VideoQueueLimitError{Scope: "user"})
			if recorder.Code != 429 || recorder.Header().Get("Retry-After") != "1" {
				t.Fatalf("排队错误HTTP合同不符: status=%d retry=%s", recorder.Code, recorder.Header().Get("Retry-After"))
			}
			if test.platform {
				var body response.Body
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != 42922 || body.ErrorType != "concurrency_limit_exceeded" {
					t.Fatalf("平台排队错误不符: %+v err=%v", body, err)
				}
				data, ok := body.Data.(map[string]any)
				if !ok || data["limit_scope"] != "user" {
					t.Fatalf("平台排队scope缺失: %#v", body.Data)
				}
				return
			}
			var body struct {
				Error map[string]string `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Error["code"] != "concurrency_limit_exceeded" {
				t.Fatalf("兼容排队错误不符: %#v err=%v", body, err)
			}
		})
	}
}

func TestVideoG6BudgetAdmissionErrorEnvelopes(t *testing.T) {
	for _, test := range []struct {
		path string
		code int
	}{
		{path: "/v1/videos", code: 0},
		{path: "/api/token/videos/generations", code: 42920},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", test.path, nil)
		writeVideoAPIError(recorder, request, service.ErrBudgetExceeded)
		if recorder.Code != 429 || recorder.Header().Get("Retry-After") != "1" {
			t.Fatalf("预算错误HTTP合同不符: path=%s status=%d", test.path, recorder.Code)
		}
		if test.code != 0 {
			var body response.Body
			if json.Unmarshal(recorder.Body.Bytes(), &body) != nil || body.Code != test.code || body.ErrorType != "budget_limit_exceeded" {
				t.Fatalf("平台预算错误不符: %+v", body)
			}
		}
	}
}
