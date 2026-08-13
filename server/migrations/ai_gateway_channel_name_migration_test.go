package migrations_test

import (
	"os"
	"strings"
	"testing"
)

// TestAIGatewayBailianChannelNameRepairContract 固定百炼渠道名称的数据修复边界。
// 迁移只能修复已确认的乱码字节，不能覆盖管理员后续设置的合法渠道名称。
func TestAIGatewayBailianChannelNameRepairContract(t *testing.T) {
	up, err := os.ReadFile("000067_fix_bailian_channel_name_utf8.up.sql")
	if err != nil {
		t.Fatalf("读取百炼渠道名称修复迁移失败: %v", err)
	}
	down, err := os.ReadFile("000067_fix_bailian_channel_name_utf8.down.sql")
	if err != nil {
		t.Fatalf("读取百炼渠道名称修复回滚迁移失败: %v", err)
	}

	upSQL := string(up)
	for _, required := range []string{
		"code = 'bailian'",
		"HEX(name) = 'C3A7E284A2C2BEC3A7E2809AC2BC20426966726F7374'",
		"CONVERT(0xE799BEE782BC20426966726F7374 USING utf8mb4)",
	} {
		if !strings.Contains(upSQL, required) {
			t.Fatalf("百炼渠道名称修复迁移缺少安全约束 %q", required)
		}
	}

	// 回滚不能重新写入乱码；数据修复在应用版本回退时仍应保留。
	if strings.Contains(strings.ToUpper(string(down)), "UPDATE TOKEN_CHANNELS") {
		t.Fatal("百炼渠道名称修复 down 不得重新写入乱码数据")
	}
}
