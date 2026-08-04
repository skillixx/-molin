package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type permissionSet map[string]bool

func (p permissionSet) CheckPermission(_ context.Context, _ uint64, code string) bool { return p[code] }

func TestRequireAnyPermAllowsEitherPermission(t *testing.T) {
	for _, permissions := range []permissionSet{{"ai_gateway:view": true}, {"token:manage": true}} {
		called := false
		handler := RequireAnyPerm(permissions, []string{"ai_gateway:view", "token:manage"}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		if !called {
			t.Fatal("任一候选权限命中时都应放行")
		}
	}
}

func TestRequireAnyPermRejectsWithoutPermission(t *testing.T) {
	recorder := httptest.NewRecorder()
	RequireAnyPerm(permissionSet{}, []string{"ai_gateway:view", "token:manage"}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("无权限时不得调用后续处理器")
	})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("无权限应返回 403，实际为 %d", recorder.Code)
	}
}
