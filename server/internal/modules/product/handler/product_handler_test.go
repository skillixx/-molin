package handler

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"molin/server/internal/modules/product/service"
)

// TestResolveUserPrice_Unconfigured 验证：取价失败（含 ErrNoPriceConfigured）时统一返回 -1。
func TestResolveUserPrice_Unconfigured(t *testing.T) {
	got := resolveUserPrice(decimal.Zero, service.ErrNoPriceConfigured)
	if !got.Equal(decimal.NewFromInt(-1)) {
		t.Fatalf("未配置价格应返回 -1，实际得到 %s", got.String())
	}

	// 任意其他错误也应归一化为 -1（未配置/不可购买）
	got = resolveUserPrice(decimal.Zero, errors.New("数据库错误"))
	if !got.Equal(decimal.NewFromInt(-1)) {
		t.Fatalf("取价出错应返回 -1，实际得到 %s", got.String())
	}
}

// TestResolveUserPrice_FreeIsZero 验证：合法的免费价 0 必须原样保留，不能被当成"未配置"。
func TestResolveUserPrice_FreeIsZero(t *testing.T) {
	got := resolveUserPrice(decimal.Zero, nil)
	if !got.Equal(decimal.Zero) {
		t.Fatalf("免费价 0 应原样返回 0，实际得到 %s", got.String())
	}
}

// TestResolveUserPrice_Normal 验证：正常价格原样返回，精度不变。
func TestResolveUserPrice_Normal(t *testing.T) {
	price := decimal.RequireFromString("19.990000")
	got := resolveUserPrice(price, nil)
	if !got.Equal(price) {
		t.Fatalf("正常价格应原样返回 %s，实际得到 %s", price.String(), got.String())
	}
}
