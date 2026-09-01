package service

import (
	"github.com/shopspring/decimal"
	"testing"
)

// 容量以实际字节计数，权益换算向上取六位，禁止把GB和GiB混为一谈。
func TestVideoG6SavedCapacityUnits(t *testing.T) {
	for _, tc := range []struct {
		unit  string
		bytes uint64
		want  string
	}{{"bytes", 1, "1"}, {"GB", 1000000000, "1"}, {"GiB", 1073741824, "1"}, {"GB", 1, "0.000001"}, {"GiB", 1073741825, "1.000001"}} {
		got, err := videoSaveQuota(tc.bytes, tc.unit)
		if err != nil || !got.Equal(decimal.RequireFromString(tc.want)) {
			t.Fatalf("%s/%d容量换算错误", tc.unit, tc.bytes)
		}
	}
	for _, unit := range []string{"", "gb", "GB/GiB", "TB"} {
		if _, err := videoSaveQuota(1, unit); err == nil {
			t.Fatal("未知或歧义单位必须拒绝")
		}
	}
	if _, err := videoSaveQuota(0, "bytes"); err == nil {
		t.Fatal("零字节不能形成保存预留")
	}
}
