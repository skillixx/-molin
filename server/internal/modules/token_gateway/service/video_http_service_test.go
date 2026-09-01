package service

import "testing"

func TestVideoG6HTTPServiceRequiresRealDependencies(t *testing.T) {
	if _, err := NewVideoHTTPService(nil, VideoBillingOptions{}); err == nil {
		t.Fatal("缺少真实数据库和财务依赖时不得装配HTTP应用")
	}
}
