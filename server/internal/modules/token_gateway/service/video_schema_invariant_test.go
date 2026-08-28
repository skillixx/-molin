package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
)

func TestValidateVideoTaskInputCount(t *testing.T) {
	tests := []struct {
		name       string
		operation  string
		inputCount int
		wantErr    string
	}{
		{name: "文生视频零输入", operation: "text_to_video", inputCount: 0},
		{name: "文生视频拒绝输入", operation: "text_to_video", inputCount: 1, wantErr: "不能绑定参考图"},
		{name: "图生视频单输入", operation: "image_to_video", inputCount: 1},
		{name: "图生视频拒绝零输入", operation: "image_to_video", inputCount: 0, wantErr: "必须绑定且只能绑定一张参考图"},
		{name: "图生视频拒绝多输入", operation: "image_to_video", inputCount: 2, wantErr: "必须绑定且只能绑定一张参考图"},
		{name: "拒绝未知操作", operation: "video_to_video", inputCount: 0, wantErr: "不支持的视频操作"},
		{name: "拒绝负数计数", operation: "text_to_video", inputCount: -1, wantErr: "输入数量不能为负数"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateVideoTaskInputCount(tt.operation, tt.inputCount)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("预期通过，实际失败: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("错误不符合预期: got=%v want包含=%s", err, tt.wantErr)
			}
		})
	}
}

func TestCreateVideoSchemaFactsCommitsTextAndImageOperationsAtomically(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		inputs    []model.AIGatewayTaskInput
		wantSteps []string
	}{
		{
			name:      "文生视频只写请求和任务",
			operation: model.AIVideoOperationTextToVideo,
			wantSteps: []string{"request", "task"},
		},
		{
			name:      "图生视频在同一事务追加唯一输入",
			operation: model.AIVideoOperationImageToVideo,
			inputs: []model.AIGatewayTaskInput{{
				InputAssetID:     31,
				UserID:           7,
				ProjectID:        11,
				Role:             model.AITaskInputReferenceImage,
				Ordinal:          0,
				NormalizedSHA256: strings.Repeat("a", 64),
				InputVersion:     1,
			}},
			wantSteps: []string{"request", "task", "input"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transactor := &stagingVideoSchemaTransactor{}
			command := validVideoSchemaCommand(tt.operation, tt.inputs)
			if err := CreateVideoSchemaFacts(context.Background(), transactor, command); err != nil {
				t.Fatalf("创建视频Schema事实失败: %v", err)
			}
			if transactor.starts != 1 || strings.Join(transactor.committed, ",") != strings.Join(tt.wantSteps, ",") {
				t.Fatalf("事实必须一次原子提交: starts=%d committed=%v", transactor.starts, transactor.committed)
			}
		})
	}
}

func TestCreateVideoSchemaFactsRejectsInvalidCommandBeforeTransaction(t *testing.T) {
	validInput := model.AIGatewayTaskInput{
		InputAssetID:     31,
		UserID:           7,
		ProjectID:        11,
		Role:             model.AITaskInputReferenceImage,
		Ordinal:          0,
		NormalizedSHA256: strings.Repeat("a", 64),
		InputVersion:     1,
	}
	tests := []struct {
		name   string
		mutate func(*VideoSchemaCreateCommand)
	}{
		{name: "未知operation", mutate: func(command *VideoSchemaCreateCommand) { command.Operation = "video_to_video" }},
		{name: "文生视频携带输入", mutate: func(command *VideoSchemaCreateCommand) { command.Inputs = []model.AIGatewayTaskInput{validInput} }},
		{name: "图生视频缺输入", mutate: func(command *VideoSchemaCreateCommand) {
			command.Operation = model.AIVideoOperationImageToVideo
			command.Request.Operation = stringPointerForVideoSchemaTest(command.Operation)
			command.Task.Operation = stringPointerForVideoSchemaTest(command.Operation)
		}},
		{name: "请求operation漂移", mutate: func(command *VideoSchemaCreateCommand) {
			command.Request.Operation = stringPointerForVideoSchemaTest(model.AIVideoOperationImageToVideo)
		}},
		{name: "请求modality不是video", mutate: func(command *VideoSchemaCreateCommand) { command.Request.Modality = "image" }},
		{name: "请求capability不是video", mutate: func(command *VideoSchemaCreateCommand) { command.Request.Capability = "image.generate" }},
		{name: "任务capability不是video", mutate: func(command *VideoSchemaCreateCommand) { command.Task.Capability = "image.generate" }},
		{name: "请求ID为空", mutate: func(command *VideoSchemaCreateCommand) { command.Request.RequestID = "" }},
		{name: "任务请求ID漂移", mutate: func(command *VideoSchemaCreateCommand) { command.Task.RequestID = "request-other" }},
		{name: "任务公开ID为空", mutate: func(command *VideoSchemaCreateCommand) { command.Task.PublicID = "" }},
		{name: "报价ID为空", mutate: func(command *VideoSchemaCreateCommand) { command.Task.QuoteID = 0 }},
		{name: "请求模型为空", mutate: func(command *VideoSchemaCreateCommand) { command.Request.LogicalModelCode = "" }},
		{name: "任务模型漂移", mutate: func(command *VideoSchemaCreateCommand) { command.Task.LogicalModelCode = "video-other" }},
		{name: "任务owner漂移", mutate: func(command *VideoSchemaCreateCommand) { command.Task.ProjectID++ }},
		{name: "输入owner漂移", mutate: func(command *VideoSchemaCreateCommand) {
			command.Operation = model.AIVideoOperationImageToVideo
			command.Request.Operation = stringPointerForVideoSchemaTest(command.Operation)
			command.Task.Operation = stringPointerForVideoSchemaTest(command.Operation)
			input := validInput
			input.UserID++
			command.Inputs = []model.AIGatewayTaskInput{input}
		}},
		{name: "输入SHA长度错误", mutate: func(command *VideoSchemaCreateCommand) {
			command.Operation = model.AIVideoOperationImageToVideo
			command.Request.Operation = stringPointerForVideoSchemaTest(command.Operation)
			command.Task.Operation = stringPointerForVideoSchemaTest(command.Operation)
			input := validInput
			input.NormalizedSHA256 = "abc"
			command.Inputs = []model.AIGatewayTaskInput{input}
		}},
		{name: "输入SHA不是小写十六进制", mutate: func(command *VideoSchemaCreateCommand) {
			command.Operation = model.AIVideoOperationImageToVideo
			command.Request.Operation = stringPointerForVideoSchemaTest(command.Operation)
			command.Task.Operation = stringPointerForVideoSchemaTest(command.Operation)
			input := validInput
			input.NormalizedSHA256 = strings.Repeat("Z", 64)
			command.Inputs = []model.AIGatewayTaskInput{input}
		}},
		{name: "输入version为零", mutate: func(command *VideoSchemaCreateCommand) {
			command.Operation = model.AIVideoOperationImageToVideo
			command.Request.Operation = stringPointerForVideoSchemaTest(command.Operation)
			command.Task.Operation = stringPointerForVideoSchemaTest(command.Operation)
			input := validInput
			input.InputVersion = 0
			command.Inputs = []model.AIGatewayTaskInput{input}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transactor := &stagingVideoSchemaTransactor{}
			command := validVideoSchemaCommand(model.AIVideoOperationTextToVideo, nil)
			tt.mutate(&command)
			if err := CreateVideoSchemaFacts(context.Background(), transactor, command); err == nil {
				t.Fatal("无效命令必须失败")
			}
			if transactor.starts != 0 || len(transactor.committed) != 0 {
				t.Fatalf("无效命令不得开始事务: starts=%d committed=%v", transactor.starts, transactor.committed)
			}
		})
	}
}

func TestCreateVideoSchemaFactsRollsBackEveryWriteFailure(t *testing.T) {
	for _, failedStep := range []string{"request", "task", "input"} {
		t.Run(failedStep, func(t *testing.T) {
			transactor := &stagingVideoSchemaTransactor{failStep: failedStep}
			command := validVideoSchemaCommand(model.AIVideoOperationImageToVideo, []model.AIGatewayTaskInput{{
				InputAssetID:     31,
				UserID:           7,
				ProjectID:        11,
				Role:             model.AITaskInputReferenceImage,
				Ordinal:          0,
				NormalizedSHA256: strings.Repeat("b", 64),
				InputVersion:     2,
			}})
			if err := CreateVideoSchemaFacts(context.Background(), transactor, command); err == nil {
				t.Fatalf("注入%s失败后必须返回错误", failedStep)
			}
			if transactor.starts != 1 || len(transactor.committed) != 0 {
				t.Fatalf("任一步失败均不得部分提交: starts=%d committed=%v", transactor.starts, transactor.committed)
			}
		})
	}
}

func TestCreateVideoSchemaFactsReturnsTransactionCommitFailure(t *testing.T) {
	transactor := &stagingVideoSchemaTransactor{commitErr: true}
	command := validVideoSchemaCommand(model.AIVideoOperationTextToVideo, nil)
	if err := CreateVideoSchemaFacts(context.Background(), transactor, command); err == nil || !strings.Contains(err.Error(), "提交失败") {
		t.Fatalf("事务提交失败必须原样向上返回并附带中文上下文: %v", err)
	}
	if transactor.starts != 1 || len(transactor.committed) != 0 {
		t.Fatalf("事务提交失败不得留下部分事实: starts=%d committed=%v", transactor.starts, transactor.committed)
	}
}

func TestRequestVideoInputPendingDeleteCommitsEligibleStates(t *testing.T) {
	for _, lifecycleState := range []string{
		model.AIInputAssetReady,
		model.AIInputAssetRejected,
		model.AIInputAssetQuarantined,
	} {
		t.Run(lifecycleState, func(t *testing.T) {
			now := time.Date(2026, 8, 28, 10, 30, 0, 0, time.UTC)
			transactor := &stagingVideoInputDeleteTransactor{
				asset: validVideoInputDeleteAsset(lifecycleState),
			}
			if err := RequestVideoInputPendingDelete(context.Background(), transactor, VideoInputDeleteCommand{
				InputAssetID: 41,
				UserID:       7,
				ProjectID:    11,
				Now:          now,
			}); err != nil {
				t.Fatalf("请求输入资产pending_delete失败: %v", err)
			}
			if transactor.starts != 1 || len(transactor.committed) != 1 || transactor.committed[0] != now {
				t.Fatalf("删除请求必须在唯一事务内提交同一时间: starts=%d committed=%v", transactor.starts, transactor.committed)
			}
		})
	}
}

func TestRequestVideoInputPendingDeleteRejectsUnsafeStateWithoutCommit(t *testing.T) {
	tests := []struct {
		name         string
		asset        model.AIGatewayInputAsset
		activeLeases uint64
		failStep     string
		commitErr    bool
	}{
		{name: "法律保全", asset: func() model.AIGatewayInputAsset {
			asset := validVideoInputDeleteAsset(model.AIInputAssetReady)
			asset.LegalHold = true
			return asset
		}()},
		{name: "存在活跃租约", asset: validVideoInputDeleteAsset(model.AIInputAssetReady), activeLeases: 1},
		{name: "错误生命周期", asset: validVideoInputDeleteAsset(model.AIInputAssetNormalizing)},
		{name: "loader返回错误user", asset: func() model.AIGatewayInputAsset {
			asset := validVideoInputDeleteAsset(model.AIInputAssetReady)
			asset.UserID = 8
			return asset
		}()},
		{name: "loader返回错误project", asset: func() model.AIGatewayInputAsset {
			asset := validVideoInputDeleteAsset(model.AIInputAssetReady)
			asset.ProjectID = 12
			return asset
		}()},
		{name: "读取失败", asset: validVideoInputDeleteAsset(model.AIInputAssetReady), failStep: "load"},
		{name: "租约计数失败", asset: validVideoInputDeleteAsset(model.AIInputAssetReady), failStep: "count"},
		{name: "写入失败", asset: validVideoInputDeleteAsset(model.AIInputAssetReady), failStep: "mark"},
		{name: "事务提交失败", asset: validVideoInputDeleteAsset(model.AIInputAssetReady), commitErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transactor := &stagingVideoInputDeleteTransactor{
				asset:        tt.asset,
				activeLeases: tt.activeLeases,
				failStep:     tt.failStep,
				commitErr:    tt.commitErr,
			}
			err := RequestVideoInputPendingDelete(context.Background(), transactor, VideoInputDeleteCommand{
				InputAssetID: 41,
				UserID:       7,
				ProjectID:    11,
				Now:          time.Date(2026, 8, 28, 10, 30, 0, 0, time.UTC),
			})
			if err == nil {
				t.Fatal("不安全状态或事务故障必须拒绝删除请求")
			}
			if transactor.starts != 1 || len(transactor.committed) != 0 {
				t.Fatalf("失败路径不得部分提交pending_delete: starts=%d committed=%v", transactor.starts, transactor.committed)
			}
		})
	}
}

func TestRequestVideoInputPendingDeleteRejectsInvalidCommandBeforeTransaction(t *testing.T) {
	tests := []VideoInputDeleteCommand{
		{InputAssetID: 0, UserID: 7, ProjectID: 11, Now: time.Now()},
		{InputAssetID: 41, ProjectID: 11, Now: time.Now()},
		{InputAssetID: 41, UserID: 7, Now: time.Now()},
		{InputAssetID: 41, UserID: 7, ProjectID: 11},
	}
	for _, command := range tests {
		transactor := &stagingVideoInputDeleteTransactor{asset: validVideoInputDeleteAsset(model.AIInputAssetReady)}
		if err := RequestVideoInputPendingDelete(context.Background(), transactor, command); err == nil {
			t.Fatal("缺少资产ID或时间的命令必须失败")
		}
		if transactor.starts != 0 || len(transactor.committed) != 0 {
			t.Fatalf("无效删除命令不得开始事务: starts=%d committed=%v", transactor.starts, transactor.committed)
		}
	}
}

func TestCompleteVideoUploadSessionCommitsSnapshotAndSessionAtomically(t *testing.T) {
	now := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)
	for _, sourceVersion := range []struct {
		name      string
		etag      *string
		versionID *string
	}{
		{name: "ETag", etag: stringPointerForVideoSchemaTest("etag-v1")},
		{name: "VersionID", versionID: stringPointerForVideoSchemaTest("version-v1")},
	} {
		t.Run(sourceVersion.name, func(t *testing.T) {
			transactor := validVideoUploadCompletionTransactor(now)
			transactor.session.SourceETag = sourceVersion.etag
			transactor.session.SourceVersionID = sourceVersion.versionID
			command := validVideoUploadCompletionCommand(now)
			if err := CompleteVideoUploadSession(context.Background(), transactor, command); err != nil {
				t.Fatalf("完成视频上传会话失败: %v", err)
			}
			if transactor.starts != 1 || strings.Join(transactor.committed, ",") != "input,session" || transactor.completedInputAssetID != 501 {
				t.Fatalf("输入快照和会话必须一次提交且使用回填ID: starts=%d committed=%v inputID=%d", transactor.starts, transactor.committed, transactor.completedInputAssetID)
			}
		})
	}
}

func TestCompleteVideoUploadSessionRejectsInvalidOrStaleFactsWithoutCommit(t *testing.T) {
	now := time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		mutate    func(*stagingVideoUploadCompletionTransactor, *VideoUploadCompletionCommand)
		failStep  string
		commitErr bool
	}{
		{name: "会话已过期", mutate: func(tx *stagingVideoUploadCompletionTransactor, _ *VideoUploadCompletionCommand) {
			tx.session.ExpiresAt = now.Add(-time.Second)
		}},
		{name: "跨owner会话", mutate: func(tx *stagingVideoUploadCompletionTransactor, _ *VideoUploadCompletionCommand) { tx.session.UserID++ }},
		{name: "缺少ETag和VersionID", mutate: func(tx *stagingVideoUploadCompletionTransactor, _ *VideoUploadCompletionCommand) {
			tx.session.SourceETag = nil
			tx.session.SourceVersionID = nil
		}},
		{name: "ETag和VersionID仅空白", mutate: func(tx *stagingVideoUploadCompletionTransactor, _ *VideoUploadCompletionCommand) {
			tx.session.SourceETag = stringPointerForVideoSchemaTest("   ")
			tx.session.SourceVersionID = stringPointerForVideoSchemaTest("\t")
		}},
		{name: "已有最终输入资产", mutate: func(tx *stagingVideoUploadCompletionTransactor, _ *VideoUploadCompletionCommand) {
			tx.session.FinalInputAssetID = uint64PointerForVideoSchemaTest(99)
		}},
		{name: "输入owner漂移", mutate: func(_ *stagingVideoUploadCompletionTransactor, command *VideoUploadCompletionCommand) {
			command.InputAsset.ProjectID++
		}},
		{name: "输入携带既有内部ID", mutate: func(_ *stagingVideoUploadCompletionTransactor, command *VideoUploadCompletionCommand) {
			command.InputAsset.ID = 99
		}},
		{name: "输入source_type漂移", mutate: func(_ *stagingVideoUploadCompletionTransactor, command *VideoUploadCompletionCommand) {
			command.InputAsset.SourceType = model.AIUploadSourceOpenAIInlineMultipart
		}},
		{name: "输入upload_session漂移", mutate: func(_ *stagingVideoUploadCompletionTransactor, command *VideoUploadCompletionCommand) {
			command.InputAsset.UploadSessionID = uint64PointerForVideoSchemaTest(88)
		}},
		{name: "读取失败", failStep: "load"},
		{name: "插入失败", failStep: "insert"},
		{name: "完成标记失败", failStep: "mark"},
		{name: "提交失败", commitErr: true},
	}
	for _, status := range []string{
		model.AIUploadSessionCreated, model.AIUploadSessionUploading, model.AIUploadSessionCompleted,
		model.AIUploadSessionRejected, model.AIUploadSessionCancelled, model.AIUploadSessionExpired,
	} {
		status := status
		tests = append(tests, struct {
			name      string
			mutate    func(*stagingVideoUploadCompletionTransactor, *VideoUploadCompletionCommand)
			failStep  string
			commitErr bool
		}{name: "拒绝状态_" + status, mutate: func(tx *stagingVideoUploadCompletionTransactor, _ *VideoUploadCompletionCommand) {
			tx.session.Status = status
		}})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transactor := validVideoUploadCompletionTransactor(now)
			transactor.failStep = tt.failStep
			transactor.commitErr = tt.commitErr
			command := validVideoUploadCompletionCommand(now)
			if tt.mutate != nil {
				tt.mutate(transactor, &command)
			}
			if err := CompleteVideoUploadSession(context.Background(), transactor, command); err == nil {
				t.Fatal("无效、过期或事务故障必须拒绝完成上传")
			}
			if transactor.starts != 1 || len(transactor.committed) != 0 {
				t.Fatalf("上传完成失败不得部分提交: starts=%d committed=%v", transactor.starts, transactor.committed)
			}
		})
	}
}

func TestReleaseVideoInputLeasesCommitsFourTerminalPoliciesAndT2VNoop(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		taskStatus    string
		billingStatus string
		inputs        []model.AIGatewayTaskInput
		wantReleased  []uint64
	}{
		{name: "成功已结算", taskStatus: model.AIImageTaskSucceeded, billingStatus: model.AIBillingSettled, inputs: leaseInputsForVideoTest(), wantReleased: []uint64{61}},
		{name: "失败已释放", taskStatus: model.AIImageTaskFailed, billingStatus: model.AIBillingReleased, inputs: leaseInputsForVideoTest(), wantReleased: []uint64{61}},
		{name: "取消已结算", taskStatus: model.AIImageTaskCancelled, billingStatus: model.AIBillingSettled, inputs: leaseInputsForVideoTest(), wantReleased: []uint64{61}},
		{name: "过期已释放", taskStatus: model.AIImageTaskExpired, billingStatus: model.AIBillingReleased, inputs: leaseInputsForVideoTest(), wantReleased: []uint64{61}},
		{name: "图生视频已释放租约幂等跳过", taskStatus: model.AIImageTaskSucceeded, billingStatus: model.AIBillingSettled, inputs: leaseInputsForVideoTest()[1:]},
		{name: "文生视频零输入幂等成功", taskStatus: model.AIImageTaskSucceeded, billingStatus: model.AIBillingSettled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transactor := validVideoLeaseReleaseTransactor(now)
			transactor.task.Status = tt.taskStatus
			transactor.request.BillingStatus = tt.billingStatus
			transactor.inputs = tt.inputs
			if err := ReleaseVideoInputLeases(context.Background(), transactor, VideoInputLeaseReleaseCommand{TaskID: 51, UserID: 7, ProjectID: 11, Now: now}); err != nil {
				t.Fatalf("释放视频输入租约失败: %v", err)
			}
			if transactor.starts != 1 || !equalUint64Slices(transactor.committed, tt.wantReleased) {
				t.Fatalf("只允许一次释放未释放租约: starts=%d got=%v want=%v", transactor.starts, transactor.committed, tt.wantReleased)
			}
		})
	}
}

func TestReleaseVideoInputLeasesRejectsUnsafeFactsAndFailuresWithoutCommit(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		mutate    func(*stagingVideoLeaseReleaseTransactor)
		failStep  string
		commitErr bool
	}{
		{name: "pending_reconcile", mutate: func(tx *stagingVideoLeaseReleaseTransactor) { tx.task.Status = model.AIImageTaskPendingReconcile }},
		{name: "非终态", mutate: func(tx *stagingVideoLeaseReleaseTransactor) { tx.task.Status = model.AIImageTaskProcessing }},
		{name: "成功但未结算", mutate: func(tx *stagingVideoLeaseReleaseTransactor) {
			tx.task.Status = model.AIImageTaskSucceeded
			tx.request.BillingStatus = model.AIBillingReleased
		}},
		{name: "失败但账单未闭合", mutate: func(tx *stagingVideoLeaseReleaseTransactor) {
			tx.task.Status = model.AIImageTaskFailed
			tx.request.BillingStatus = model.AIBillingHeld
		}},
		{name: "任务未完成", mutate: func(tx *stagingVideoLeaseReleaseTransactor) { tx.task.CompletedAt = nil }},
		{name: "请求未完成", mutate: func(tx *stagingVideoLeaseReleaseTransactor) { tx.request.CompletedAt = nil }},
		{name: "任务owner漂移", mutate: func(tx *stagingVideoLeaseReleaseTransactor) { tx.task.UserID++ }},
		{name: "请求owner漂移", mutate: func(tx *stagingVideoLeaseReleaseTransactor) {
			tx.request.ProjectID = uint64PointerForVideoSchemaTest(12)
		}},
		{name: "请求ID漂移", mutate: func(tx *stagingVideoLeaseReleaseTransactor) { tx.request.RequestID = "request-other" }},
		{name: "输入owner漂移", mutate: func(tx *stagingVideoLeaseReleaseTransactor) { tx.inputs[0].UserID++ }},
		{name: "输入task漂移", mutate: func(tx *stagingVideoLeaseReleaseTransactor) { tx.inputs[0].TaskID++ }},
		{name: "任务读取失败", failStep: "task"},
		{name: "请求读取失败", failStep: "request"},
		{name: "输入读取失败", failStep: "inputs"},
		{name: "释放写入失败", failStep: "release"},
		{name: "提交失败", commitErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transactor := validVideoLeaseReleaseTransactor(now)
			transactor.failStep = tt.failStep
			transactor.commitErr = tt.commitErr
			if tt.mutate != nil {
				tt.mutate(transactor)
			}
			if err := ReleaseVideoInputLeases(context.Background(), transactor, VideoInputLeaseReleaseCommand{TaskID: 51, UserID: 7, ProjectID: 11, Now: now}); err == nil {
				t.Fatal("不安全事实或事务故障必须拒绝释放租约")
			}
			if transactor.starts != 1 || len(transactor.committed) != 0 {
				t.Fatalf("租约释放失败不得部分提交: starts=%d committed=%v", transactor.starts, transactor.committed)
			}
		})
	}
}

func validVideoSchemaCommand(operation string, inputs []model.AIGatewayTaskInput) VideoSchemaCreateCommand {
	return VideoSchemaCreateCommand{
		Operation: operation,
		Request: model.AIRequest{
			RequestID:        "request-test",
			UserID:           7,
			ProjectID:        uint64PointerForVideoSchemaTest(11),
			LogicalModelCode: "video-public-model",
			Modality:         "video",
			Capability:       model.AIVideoCapability,
			Operation:        stringPointerForVideoSchemaTest(operation),
		},
		Task: model.AIImageTask{
			PublicID:         "video_test",
			RequestID:        "request-test",
			QuoteID:          19,
			UserID:           7,
			ProjectID:        11,
			LogicalModelCode: "video-public-model",
			Capability:       model.AIVideoCapability,
			Operation:        stringPointerForVideoSchemaTest(operation),
		},
		Inputs: inputs,
	}
}

type stagingVideoSchemaTransactor struct {
	starts    int
	failStep  string
	commitErr bool
	committed []string
}

func (transactor *stagingVideoSchemaTransactor) WithinTransaction(ctx context.Context, fn func(VideoSchemaTransaction) error) error {
	transactor.starts++
	tx := &stagingVideoSchemaTransaction{failStep: transactor.failStep}
	if err := fn(tx); err != nil {
		return err
	}
	if transactor.commitErr {
		return errors.New("注入事务提交失败")
	}
	transactor.committed = append(transactor.committed, tx.staging...)
	return nil
}

type stagingVideoSchemaTransaction struct {
	failStep string
	staging  []string
}

func (tx *stagingVideoSchemaTransaction) InsertRequest(context.Context, *model.AIRequest) error {
	return tx.stage("request")
}

func (tx *stagingVideoSchemaTransaction) InsertTask(context.Context, *model.AIImageTask) error {
	return tx.stage("task")
}

func (tx *stagingVideoSchemaTransaction) InsertTaskInput(context.Context, *model.AIGatewayTaskInput) error {
	return tx.stage("input")
}

func (tx *stagingVideoSchemaTransaction) stage(step string) error {
	if tx.failStep == step {
		return errors.New("注入写入失败")
	}
	tx.staging = append(tx.staging, step)
	return nil
}

func stringPointerForVideoSchemaTest(value string) *string { return &value }

func uint64PointerForVideoSchemaTest(value uint64) *uint64 { return &value }

type stagingVideoInputDeleteTransactor struct {
	starts       int
	asset        model.AIGatewayInputAsset
	activeLeases uint64
	failStep     string
	commitErr    bool
	committed    []time.Time
}

func (transactor *stagingVideoInputDeleteTransactor) WithinTransaction(ctx context.Context, fn func(VideoInputDeleteTransaction) error) error {
	transactor.starts++
	tx := &stagingVideoInputDeleteTransaction{
		asset:        transactor.asset,
		activeLeases: transactor.activeLeases,
		failStep:     transactor.failStep,
	}
	if err := fn(tx); err != nil {
		return err
	}
	if transactor.commitErr {
		return errors.New("注入输入删除事务提交失败")
	}
	transactor.committed = append(transactor.committed, tx.staging...)
	return nil
}

type stagingVideoInputDeleteTransaction struct {
	asset        model.AIGatewayInputAsset
	activeLeases uint64
	failStep     string
	staging      []time.Time
}

func (tx *stagingVideoInputDeleteTransaction) LoadInputForUpdate(context.Context, uint64, uint64, uint64) (*model.AIGatewayInputAsset, error) {
	if tx.failStep == "load" {
		return nil, errors.New("注入输入资产读取失败")
	}
	asset := tx.asset
	return &asset, nil
}

func (tx *stagingVideoInputDeleteTransaction) CountActiveLeasesForUpdate(context.Context, uint64, uint64, uint64) (uint64, error) {
	if tx.failStep == "count" {
		return 0, errors.New("注入活跃租约计数失败")
	}
	return tx.activeLeases, nil
}

func (tx *stagingVideoInputDeleteTransaction) MarkPendingDelete(_ context.Context, _, _, _ uint64, deleteRequestedAt, pendingDeleteAt time.Time) error {
	if tx.failStep == "mark" {
		return errors.New("注入pending_delete写入失败")
	}
	if deleteRequestedAt != pendingDeleteAt {
		return errors.New("删除请求时间与pending_delete时间必须一致")
	}
	tx.staging = append(tx.staging, deleteRequestedAt)
	return nil
}

func validVideoInputDeleteAsset(lifecycleState string) model.AIGatewayInputAsset {
	return model.AIGatewayInputAsset{ID: 41, UserID: 7, ProjectID: 11, LifecycleState: lifecycleState}
}

func validVideoUploadCompletionCommand(now time.Time) VideoUploadCompletionCommand {
	return VideoUploadCompletionCommand{
		SessionID: 71,
		UserID:    7,
		ProjectID: 11,
		Now:       now,
		InputAsset: model.AIGatewayInputAsset{
			UserID:           7,
			ProjectID:        11,
			SourceType:       model.AIUploadSourcePlatformPresigned,
			UploadSessionID:  uint64PointerForVideoSchemaTest(71),
			OriginalSHA256:   strings.Repeat("c", 64),
			LifecycleState:   model.AIInputAssetNormalizing,
			ModerationStatus: model.AIModerationPending,
		},
	}
}

func validVideoUploadCompletionTransactor(now time.Time) *stagingVideoUploadCompletionTransactor {
	return &stagingVideoUploadCompletionTransactor{
		session: model.AIUploadSession{
			ID:         71,
			UserID:     7,
			ProjectID:  11,
			SourceType: model.AIUploadSourcePlatformPresigned,
			Status:     model.AIUploadSessionVerifying,
			SourceETag: stringPointerForVideoSchemaTest("etag-v1"),
			ExpiresAt:  now.Add(time.Minute),
		},
	}
}

type stagingVideoUploadCompletionTransactor struct {
	starts                int
	session               model.AIUploadSession
	failStep              string
	commitErr             bool
	committed             []string
	completedInputAssetID uint64
}

func (transactor *stagingVideoUploadCompletionTransactor) WithinTransaction(ctx context.Context, fn func(VideoUploadCompletionTransaction) error) error {
	transactor.starts++
	tx := &stagingVideoUploadCompletionTransaction{session: transactor.session, failStep: transactor.failStep}
	if err := fn(tx); err != nil {
		return err
	}
	if transactor.commitErr {
		return errors.New("注入上传完成事务提交失败")
	}
	transactor.committed = append(transactor.committed, tx.staging...)
	transactor.completedInputAssetID = tx.completedInputAssetID
	return nil
}

type stagingVideoUploadCompletionTransaction struct {
	session               model.AIUploadSession
	failStep              string
	staging               []string
	completedInputAssetID uint64
}

func (tx *stagingVideoUploadCompletionTransaction) LoadUploadSessionForUpdate(_ context.Context, _, _, _ uint64) (*model.AIUploadSession, error) {
	if tx.failStep == "load" {
		return nil, errors.New("注入上传会话读取失败")
	}
	session := tx.session
	return &session, nil
}

func (tx *stagingVideoUploadCompletionTransaction) InsertInputAsset(_ context.Context, input *model.AIGatewayInputAsset) error {
	if tx.failStep == "insert" {
		return errors.New("注入输入快照插入失败")
	}
	input.ID = 501
	tx.staging = append(tx.staging, "input")
	return nil
}

func (tx *stagingVideoUploadCompletionTransaction) MarkUploadCompleted(_ context.Context, _, inputAssetID uint64, _ time.Time) error {
	if tx.failStep == "mark" {
		return errors.New("注入上传会话完成标记失败")
	}
	tx.completedInputAssetID = inputAssetID
	tx.staging = append(tx.staging, "session")
	return nil
}

func validVideoLeaseReleaseTransactor(now time.Time) *stagingVideoLeaseReleaseTransactor {
	return &stagingVideoLeaseReleaseTransactor{
		task: model.AIImageTask{
			ID:          51,
			RequestID:   "request-lease",
			UserID:      7,
			ProjectID:   11,
			Status:      model.AIImageTaskSucceeded,
			CompletedAt: &now,
		},
		request: model.AIRequest{
			RequestID:     "request-lease",
			UserID:        7,
			ProjectID:     uint64PointerForVideoSchemaTest(11),
			BillingStatus: model.AIBillingSettled,
			CompletedAt:   &now,
		},
		inputs: leaseInputsForVideoTest(),
	}
}

func leaseInputsForVideoTest() []model.AIGatewayTaskInput {
	releasedAt := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	return []model.AIGatewayTaskInput{
		{ID: 61, TaskID: 51, UserID: 7, ProjectID: 11},
		{ID: 62, TaskID: 51, UserID: 7, ProjectID: 11, LeaseReleasedAt: &releasedAt},
	}
}

type stagingVideoLeaseReleaseTransactor struct {
	starts    int
	task      model.AIImageTask
	request   model.AIRequest
	inputs    []model.AIGatewayTaskInput
	failStep  string
	commitErr bool
	committed []uint64
}

func (transactor *stagingVideoLeaseReleaseTransactor) WithinTransaction(ctx context.Context, fn func(VideoInputLeaseReleaseTransaction) error) error {
	transactor.starts++
	tx := &stagingVideoLeaseReleaseTransaction{
		task:     transactor.task,
		request:  transactor.request,
		inputs:   append([]model.AIGatewayTaskInput(nil), transactor.inputs...),
		failStep: transactor.failStep,
	}
	if err := fn(tx); err != nil {
		return err
	}
	if transactor.commitErr {
		return errors.New("注入租约释放事务提交失败")
	}
	transactor.committed = append(transactor.committed, tx.staging...)
	return nil
}

type stagingVideoLeaseReleaseTransaction struct {
	task     model.AIImageTask
	request  model.AIRequest
	inputs   []model.AIGatewayTaskInput
	failStep string
	staging  []uint64
}

func (tx *stagingVideoLeaseReleaseTransaction) LoadTaskForUpdate(context.Context, uint64, uint64, uint64) (*model.AIImageTask, error) {
	if tx.failStep == "task" {
		return nil, errors.New("注入任务读取失败")
	}
	task := tx.task
	return &task, nil
}

func (tx *stagingVideoLeaseReleaseTransaction) LoadRequestForUpdate(context.Context, string, uint64, uint64) (*model.AIRequest, error) {
	if tx.failStep == "request" {
		return nil, errors.New("注入请求读取失败")
	}
	request := tx.request
	return &request, nil
}

func (tx *stagingVideoLeaseReleaseTransaction) LoadTaskInputsForUpdate(context.Context, uint64, uint64, uint64) ([]model.AIGatewayTaskInput, error) {
	if tx.failStep == "inputs" {
		return nil, errors.New("注入任务输入读取失败")
	}
	return append([]model.AIGatewayTaskInput(nil), tx.inputs...), nil
}

func (tx *stagingVideoLeaseReleaseTransaction) ReleaseTaskInputLeases(_ context.Context, taskInputIDs []uint64, _ time.Time) error {
	if tx.failStep == "release" {
		return errors.New("注入租约释放写入失败")
	}
	tx.staging = append(tx.staging, taskInputIDs...)
	return nil
}

func equalUint64Slices(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
