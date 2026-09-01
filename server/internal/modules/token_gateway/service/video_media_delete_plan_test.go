package service

import (
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"testing"
)

// 完成标记不能代替实际资产集合，成功任务尤其不能通过空计划伪造删除。
func TestVideoG6MediaDeletePlanShape(t *testing.T) {
	task := &repository.VideoTaskRecord{AIImageTask: model.AIImageTask{ID: 1, UserID: 2, ProjectID: 3, Status: model.AIImageTaskSucceeded}}
	assets := []model.AIImageAsset{{ID: 1, TaskID: 1, UserID: 2, ProjectID: 3, AssetRole: "content", Modality: "video"}}
	if validateMediaDeletePlanShape(task, assets, nil) == nil {
		t.Fatal("有资产不能以空计划完成")
	}
	if validateMediaDeletePlanShape(task, nil, nil) == nil {
		t.Fatal("成功任务缺产物不能以空计划完成")
	}
	task.Status = model.AIImageTaskCancelled
	if err := validateMediaDeletePlanShape(task, nil, []videoMediaDeleteItem{}); err != nil {
		t.Fatal("已取消无产物可以接受空计划，但仍须独立金融门禁")
	}
}
