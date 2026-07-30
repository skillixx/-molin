package service

import (
	"testing"
	"time"
)

func TestIsAdminVerifyValidFailsClosedForInvalidTimeConfiguration(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	old := now.Add(-25 * time.Hour)

	tests := []struct {
		name        string
		verifiedAt  *time.Time
		expireHours int
		want        bool
	}{
		{name: "空时间无效", verifiedAt: nil, expireHours: 24},
		{name: "负有效期失败关闭", verifiedAt: &past, expireHours: -1},
		{name: "未来时间戳无效", verifiedAt: &future, expireHours: 24},
		{name: "零有效期允许过去时间", verifiedAt: &past, expireHours: 0, want: true},
		{name: "零有效期仍拒绝未来时间", verifiedAt: &future, expireHours: 0},
		{name: "有效期内有效", verifiedAt: &past, expireHours: 24, want: true},
		{name: "过期后无效", verifiedAt: &old, expireHours: 24},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAdminVerifyValid(tc.verifiedAt, tc.expireHours); got != tc.want {
				t.Fatalf("管理员认证有效性异常: got=%v want=%v", got, tc.want)
			}
		})
	}
}
