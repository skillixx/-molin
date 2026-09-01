package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
)

// VideoModelContract随发布快照冻结，只表达已配置的资格要求，不赋予真实用户商业权益。
// 显式false/null/空数组表示已配置为无需该条件，整个字段缺失则失败关闭。
type VideoModelContract struct {
	SchemaVersion            int      `json:"schema_version"`
	Purpose                  string   `json:"purpose"`
	SupportedOperations      []string `json:"supported_operations"`
	DefaultModel             bool     `json:"default_model"`
	AssetRequired            bool     `json:"asset_required"`
	RequiredEntitlementType  *string  `json:"required_entitlement_type"`
	RequiredMembershipLevels []uint64 `json:"required_membership_levels"`
}

var ErrVideoModelContractInvalid = errors.New("视频模型合同无效")

var videoEntitlementType = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ParseVideoModelContract拒绝缺项、重复键、未知字段、非法组合及非本阶段测试配置。
func ParseVideoModelContract(raw []byte, productID *uint64) (VideoModelContract, error) {
	var result VideoModelContract
	if len(raw) == 0 || len(raw) > 4096 {
		return result, ErrVideoModelContractInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return result, ErrVideoModelContractInvalid
	}
	allowed := map[string]bool{"schema_version": true, "purpose": true, "supported_operations": true, "default_model": true, "asset_required": true, "required_entitlement_type": true, "required_membership_levels": true}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return result, ErrVideoModelContractInvalid
		}
		name, ok := key.(string)
		if !ok || !allowed[name] || seen[name] {
			return result, ErrVideoModelContractInvalid
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || (name != "required_entitlement_type" && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return result, ErrVideoModelContractInvalid
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') || len(seen) != len(allowed) {
		return result, ErrVideoModelContractInvalid
	}
	if _, err = decoder.Token(); err != io.EOF {
		return result, ErrVideoModelContractInvalid
	}
	if json.Unmarshal(raw, &result) != nil || result.SchemaVersion != 1 || result.Purpose != "non_commercial_test_fixture" || len(result.SupportedOperations) == 0 || len(result.SupportedOperations) > 2 || result.RequiredMembershipLevels == nil || len(result.RequiredMembershipLevels) > 64 {
		return result, ErrVideoModelContractInvalid
	}
	operations := map[string]bool{}
	for _, op := range result.SupportedOperations {
		if operations[op] || (op != AIVideoOperationTextToVideo && op != AIVideoOperationImageToVideo) {
			return result, ErrVideoModelContractInvalid
		}
		operations[op] = true
	}
	levels := map[uint64]bool{}
	for _, level := range result.RequiredMembershipLevels {
		if level == 0 || levels[level] {
			return result, ErrVideoModelContractInvalid
		}
		levels[level] = true
	}
	if result.AssetRequired && (productID == nil || *productID == 0) {
		return result, ErrVideoModelContractInvalid
	}
	if result.RequiredEntitlementType != nil && (!result.AssetRequired || !videoEntitlementType.MatchString(*result.RequiredEntitlementType)) {
		return result, ErrVideoModelContractInvalid
	}
	return result, nil
}
