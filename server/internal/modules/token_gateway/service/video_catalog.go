package service

import (
	"encoding/json"
	"strings"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// publishedCatalogCandidate只把视频发布快照投影为可见目录；缺项或损坏的发布不得被草稿补齐。
// 目录展示并不授予生成资格，生成仍执行实名、Project、Key、权益、权利声明和财务门禁。
func publishedCatalogCandidate(candidate repository.PublicModelCandidate) (model.TokenModel, *VideoModelContract, bool) {
	item := candidate.TokenModel
	if item.Modality != "video" {
		// 当前发布仍属视频时，工作副本改模态不能把草稿送入旧Chat/Image目录分支。
		if candidate.PublishedModality == "video" {
			return item, nil, false
		}
		return item, nil, true
	}
	var snapshot struct {
		model.TokenModelReleaseSnapshot
		VideoContract json.RawMessage `json:"video_contract"`
	}
	if json.Unmarshal(candidate.PublishedVideoSnapshot, &snapshot) != nil || snapshot.LogicalModelCode != item.LogicalModelCode || snapshot.Modality != "video" || strings.TrimSpace(snapshot.DisplayName) == "" || !capabilityEnabled(snapshot.Capabilities, model.AIVideoCapability) {
		return item, nil, false
	}
	if snapshot.VisibleScope != scopeAll && snapshot.VisibleScope != scopeGroups && snapshot.VisibleScope != scopeRoles {
		return item, nil, false
	}
	contract, err := ParseVideoModelContract(snapshot.VideoContract, snapshot.ProductID)
	if err != nil {
		return item, nil, false
	}
	// 同一公开条目的展示、文档和可见范围全部来自同一发布版本，不能混入未发布修改。
	item.DisplayName, item.ProviderName, item.Description = snapshot.DisplayName, snapshot.ProviderName, snapshot.Description
	item.CapabilitiesJSON, item.ContextWindow = snapshot.Capabilities, snapshot.ContextWindow
	item.IntroURL, item.DocsURL, item.QuickStartURL = snapshot.IntroURL, snapshot.DocsURL, snapshot.QuickStartURL
	item.IntroURLHealthStatus, item.DocsURLHealthStatus, item.QuickStartURLHealthStatus = snapshot.IntroURLHealthStatus, snapshot.DocsURLHealthStatus, snapshot.QuickStartURLHealthStatus
	item.VisibleScope, item.TargetAudience, item.ProductID = snapshot.VisibleScope, snapshot.TargetAudience, snapshot.ProductID
	return item, &contract, true
}
