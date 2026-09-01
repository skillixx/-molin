package service

import (
	"strings"
	"testing"
)

const videoG6NoEntitlementContract = `{"schema_version":1,"purpose":"non_commercial_test_fixture","supported_operations":["text_to_video","image_to_video"],"default_model":false,"asset_required":false,"required_entitlement_type":null,"required_membership_levels":[]}`

func TestVideoG6ModelContractExplicitRequirements(t *testing.T) {
	if _, err := ParseVideoModelContract([]byte(videoG6NoEntitlementContract), nil); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"", "null", "{}",
		strings.Replace(videoG6NoEntitlementContract, `"asset_required":false,`, "", 1),
		strings.Replace(videoG6NoEntitlementContract, `"asset_required":false`, `"asset_required":null`, 1),
		strings.Replace(videoG6NoEntitlementContract, `"schema_version":1`, `"schema_version":2`, 1),
		strings.Replace(videoG6NoEntitlementContract, `"default_model":false`, `"default_model":false,"default_model":true`, 1),
		strings.Replace(videoG6NoEntitlementContract, `"required_membership_levels":[]`, `"required_membership_levels":[0]`, 1),
		strings.Replace(videoG6NoEntitlementContract, `"required_membership_levels":[]`, `"required_membership_levels":[1,1]`, 1),
		strings.Replace(videoG6NoEntitlementContract, `"required_membership_levels":[]`, `"required_membership_levels":null`, 1),
		strings.Replace(videoG6NoEntitlementContract, `"required_entitlement_type":null`, `"required_entitlement_type":"storage_gb"`, 1),
		strings.Replace(videoG6NoEntitlementContract, `"asset_required":false`, `"asset_required":true`, 1),
		strings.Replace(videoG6NoEntitlementContract, `"image_to_video"`, `"other"`, 1),
		strings.Replace(videoG6NoEntitlementContract, `"purpose":"non_commercial_test_fixture"`, `"purpose":"commercial"`, 1),
		videoG6NoEntitlementContract + "{}",
	} {
		if _, err := ParseVideoModelContract([]byte(raw), nil); err == nil {
			t.Fatal("无效或未配置要求被当作授权")
		}
	}
	product := uint64(7)
	assetOnly := strings.Replace(videoG6NoEntitlementContract, `"asset_required":false`, `"asset_required":true`, 1)
	if _, err := ParseVideoModelContract([]byte(assetOnly), &product); err != nil {
		t.Fatal("只有资产不应被要求拥有配额")
	}
	typed := strings.Replace(assetOnly, `"required_entitlement_type":null`, `"required_entitlement_type":"video_access"`, 1)
	if _, err := ParseVideoModelContract([]byte(typed), &product); err != nil {
		t.Fatal(err)
	}
}
