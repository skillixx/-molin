package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 使用原模型仓储证明新合同可持久化、读回并冻结；不用SQLite替代MySQL的JSON和CHECK约束。
func TestVideoG6ModelContractPersistenceMySQL(t *testing.T) {
	db := openVideoG6MySQL(t)
	id := NextVideoFixtureUserID()
	ctx := context.Background()
	repo := repository.NewTokenModelRepository(db)
	item := model.TokenModel{ID: id, LogicalModelCode: fmt.Sprintf("molin/video-model-draft-%d", id), DisplayName: "视频合同工作副本", Modality: "video", Status: "inactive", VideoContractJSON: json.RawMessage(videoG6NoEntitlementContract)}
	if err := repo.Create(ctx, &item); err != nil {
		t.Fatal("模型合同写入失败")
	}
	loaded, err := repo.FindByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := ParseVideoModelContract(loaded.VideoContractJSON, nil)
	if err != nil || len(contract.SupportedOperations) != 2 || contract.DefaultModel || contract.AssetRequired {
		t.Fatal("模型合同持久化丢失显式配置")
	}
	snapshot, err := loaded.MarshalReleaseSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	var frozen model.TokenModelReleaseSnapshot
	if json.Unmarshal(snapshot, &frozen) != nil || len(frozen.VideoContract) == 0 {
		t.Fatal("发布快照未冻结原合同")
	}
	// 非对象及跨模态变更应被MySQL拒绝，失败后原合同仍可完整读取。
	for _, updates := range []map[string]any{{"video_contract_json": "null"}, {"video_contract_json": "[]"}, {"modality": "chat"}} {
		if err := repo.Update(ctx, id, updates); err == nil {
			t.Fatal("数据库放行非法视频合同或跨模态更新")
		}
	}
	unchanged, err := repo.FindByID(ctx, id)
	if err != nil || unchanged.Modality != "video" || string(unchanged.VideoContractJSON) != string(loaded.VideoContractJSON) {
		t.Fatal("失败更新改变原视频合同")
	}
	// 历史Chat模型仍可不带视频合同创建，不能为新能力自动填充任何授权。
	chatID := NextVideoFixtureUserID()
	chat := model.TokenModel{ID: chatID, LogicalModelCode: fmt.Sprintf("molin/chat-model-draft-%d", chatID), DisplayName: "旧文字合同", Modality: "chat", Status: "inactive"}
	if err := repo.Create(ctx, &chat); err != nil {
		t.Fatal("新增列破坏旧模型创建")
	}
	readChat, err := repo.FindByID(ctx, chatID)
	if err != nil || len(readChat.VideoContractJSON) != 0 {
		t.Fatal("旧模型被隐式授予视频合同")
	}
}
