package dto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"molin/server/internal/modules/app/model"
)

// strPtr 返回字符串指针的小工具。
func strPtr(s string) *string { return &s }

// TestMarketplaceAppResponse_Serialize 验证用户向白名单 DTO 的序列化结果：
//   - 不含 callback_url / adapter_config_json / updated_at（敏感/内部字段）
//   - 含 id / code / name / type / icon_url / status 等展示字段
func TestMarketplaceAppResponse_Serialize(t *testing.T) {
	src := &model.Application{
		ID:                42,
		Code:              "netdisk-basic",
		Name:              "基础网盘",
		Type:              "netdisk",
		Description:       strPtr("基础版网盘应用"),
		IconURL:           strPtr("https://cdn.example.com/icon.png"),
		CallbackURL:       strPtr("http://10.0.0.5/internal/callback"),
		AdapterConfigJSON: strPtr(`{"secret":"should-not-leak","internal_host":"10.0.0.5"}`),
		Status:            "active",
		CreatedAt:         time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
	}

	out := MapMarketplaceApp(src)
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	js := string(b)

	// 不应包含的敏感/内部字段。
	forbidden := []string{"callback_url", "adapter_config_json", "updated_at"}
	for _, f := range forbidden {
		if strings.Contains(js, "\""+f+"\"") {
			t.Errorf("白名单 DTO 不应包含字段 %q，实际序列化结果: %s", f, js)
		}
	}
	// 同时确保敏感值本身不泄露。
	for _, leak := range []string{"should-not-leak", "10.0.0.5", "internal/callback"} {
		if strings.Contains(js, leak) {
			t.Errorf("白名单 DTO 不应泄露敏感值 %q，实际序列化结果: %s", leak, js)
		}
	}

	// 应包含的展示字段。
	required := []string{"id", "code", "name", "type", "icon_url", "status", "created_at"}
	for _, f := range required {
		if !strings.Contains(js, "\""+f+"\"") {
			t.Errorf("白名单 DTO 应包含字段 %q，实际序列化结果: %s", f, js)
		}
	}

	// 校验字段值映射正确。
	var decoded map[string]interface{}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if decoded["code"] != "netdisk-basic" {
		t.Errorf("code 映射错误，期望 netdisk-basic，实际 %v", decoded["code"])
	}
	if decoded["status"] != "active" {
		t.Errorf("status 映射错误，期望 active，实际 %v", decoded["status"])
	}
}
