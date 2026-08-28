package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"molin/server/internal/modules/token_gateway/model"
)

var normalizedVideoInputSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// VideoSchemaTransactor 定义VID-G1唯一需要的事务边界；具体数据库实现留给后续Repository阶段。
type VideoSchemaTransactor interface {
	WithinTransaction(ctx context.Context, fn func(VideoSchemaTransaction) error) error
}

// VideoSchemaTransaction 只声明同一事务内写入请求、任务和输入绑定的最小能力。
type VideoSchemaTransaction interface {
	InsertRequest(ctx context.Context, request *model.AIRequest) error
	InsertTask(ctx context.Context, task *model.AIImageTask) error
	InsertTaskInput(ctx context.Context, input *model.AIGatewayTaskInput) error
}

// VideoSchemaCreateCommand 聚合同一视频创建事务需要的事实，不包含报价、钱包、队列或Provider行为。
type VideoSchemaCreateCommand struct {
	Operation string
	Request   model.AIRequest
	Task      model.AIImageTask
	Inputs    []model.AIGatewayTaskInput
}

// VideoInputDeleteTransactor 定义输入资产删除请求的独立事务边界，防止租约检查与状态写入之间发生竞态。
type VideoInputDeleteTransactor interface {
	WithinTransaction(ctx context.Context, fn func(VideoInputDeleteTransaction) error) error
}

// VideoInputDeleteTransaction 只声明加锁读取输入、加锁统计活跃租约和标记待删除所需的最小能力。
type VideoInputDeleteTransaction interface {
	LoadInputForUpdate(ctx context.Context, inputAssetID, userID, projectID uint64) (*model.AIGatewayInputAsset, error)
	CountActiveLeasesForUpdate(ctx context.Context, inputAssetID, userID, projectID uint64) (uint64, error)
	MarkPendingDelete(ctx context.Context, inputAssetID, userID, projectID uint64, deleteRequestedAt, pendingDeleteAt time.Time) error
}

// VideoInputDeleteCommand 是请求输入资产进入pending_delete所需的最小命令。
type VideoInputDeleteCommand struct {
	InputAssetID uint64
	UserID       uint64
	ProjectID    uint64
	Now          time.Time
}

// VideoUploadCompletionTransactor 定义上传会话完成与输入快照创建的原子事务边界。
type VideoUploadCompletionTransactor interface {
	WithinTransaction(ctx context.Context, fn func(VideoUploadCompletionTransaction) error) error
}

// VideoUploadCompletionTransaction 只声明锁定会话、插入快照和完成会话所需的最小能力。
type VideoUploadCompletionTransaction interface {
	LoadUploadSessionForUpdate(ctx context.Context, sessionID, userID, projectID uint64) (*model.AIUploadSession, error)
	InsertInputAsset(ctx context.Context, input *model.AIGatewayInputAsset) error
	MarkUploadCompleted(ctx context.Context, sessionID, inputAssetID uint64, completedAt time.Time) error
}

// VideoUploadCompletionCommand 聚合完成上传会话所需的归属、时间和输入快照。
type VideoUploadCompletionCommand struct {
	SessionID  uint64
	UserID     uint64
	ProjectID  uint64
	Now        time.Time
	InputAsset model.AIGatewayInputAsset
}

// VideoInputLeaseReleaseTransactor 定义任务、请求和输入租约同时加锁与释放的事务边界。
type VideoInputLeaseReleaseTransactor interface {
	WithinTransaction(ctx context.Context, fn func(VideoInputLeaseReleaseTransaction) error) error
}

// VideoInputLeaseReleaseTransaction 只声明安全释放输入租约所需的加锁读取与批量更新时间能力。
type VideoInputLeaseReleaseTransaction interface {
	LoadTaskForUpdate(ctx context.Context, taskID, userID, projectID uint64) (*model.AIImageTask, error)
	LoadRequestForUpdate(ctx context.Context, requestID string, userID, projectID uint64) (*model.AIRequest, error)
	LoadTaskInputsForUpdate(ctx context.Context, taskID, userID, projectID uint64) ([]model.AIGatewayTaskInput, error)
	ReleaseTaskInputLeases(ctx context.Context, taskInputIDs []uint64, releasedAt time.Time) error
}

// VideoInputLeaseReleaseCommand 聚合安全终结后释放输入执行租约的定位与时间。
type VideoInputLeaseReleaseCommand struct {
	TaskID    uint64
	UserID    uint64
	ProjectID uint64
	Now       time.Time
}

// ValidateVideoTaskInputCount 在进入报价、预占、消息队列或Provider前校验视频输入数量。
// 该校验必须与创建任务及绑定输入处于同一事务边界，数据库唯一键只负责阻止重复序号，不能替代跨行计数。
func ValidateVideoTaskInputCount(operation string, inputCount int) error {
	if inputCount < 0 {
		return fmt.Errorf("视频输入数量不能为负数: %d", inputCount)
	}

	switch operation {
	case model.AIVideoOperationTextToVideo:
		if inputCount != 0 {
			return fmt.Errorf("文生视频不能绑定参考图，当前输入数量为%d", inputCount)
		}
		return nil
	case model.AIVideoOperationImageToVideo:
		if inputCount != 1 {
			return fmt.Errorf("图生视频必须绑定且只能绑定一张参考图，当前输入数量为%d", inputCount)
		}
		return nil
	default:
		return fmt.Errorf("不支持的视频操作: %s", operation)
	}
}

// CreateVideoSchemaFacts 先完成全部纯校验，再在唯一事务回调内依次写入请求、任务和可选输入。
// 事务提交或回滚由VideoSchemaTransactor保证，本函数不实现Repository、CAS、报价或Provider编排。
func CreateVideoSchemaFacts(ctx context.Context, transactor VideoSchemaTransactor, command VideoSchemaCreateCommand) error {
	if err := validateVideoSchemaCreateCommand(command); err != nil {
		return err
	}
	if transactor == nil {
		return fmt.Errorf("视频Schema事务执行器不能为空")
	}

	request := command.Request
	task := command.Task
	inputs := append([]model.AIGatewayTaskInput(nil), command.Inputs...)
	if err := transactor.WithinTransaction(ctx, func(tx VideoSchemaTransaction) error {
		if tx == nil {
			return fmt.Errorf("视频Schema事务不能为空")
		}
		if err := tx.InsertRequest(ctx, &request); err != nil {
			return fmt.Errorf("写入视频请求事实失败: %w", err)
		}
		if err := tx.InsertTask(ctx, &task); err != nil {
			return fmt.Errorf("写入视频任务事实失败: %w", err)
		}
		if command.Operation == model.AIVideoOperationImageToVideo {
			// 任务插入后可能由持久化实现回填自增ID，输入绑定必须引用同一任务。
			inputs[0].TaskID = task.ID
			if err := tx.InsertTaskInput(ctx, &inputs[0]); err != nil {
				return fmt.Errorf("写入视频任务输入失败: %w", err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("创建视频Schema事实事务失败: %w", err)
	}
	return nil
}

func validateVideoSchemaCreateCommand(command VideoSchemaCreateCommand) error {
	if err := ValidateVideoTaskInputCount(command.Operation, len(command.Inputs)); err != nil {
		return err
	}
	if command.Request.Operation == nil || *command.Request.Operation != command.Operation {
		return fmt.Errorf("视频请求operation与创建命令不一致")
	}
	if command.Task.Operation == nil || *command.Task.Operation != command.Operation {
		return fmt.Errorf("视频任务operation与创建命令不一致")
	}
	if command.Request.Modality != "video" || command.Request.Capability != model.AIVideoCapability {
		return fmt.Errorf("视频请求必须使用video模态和video.generate能力")
	}
	if command.Task.Capability != model.AIVideoCapability {
		return fmt.Errorf("视频任务必须使用video.generate能力")
	}
	if strings.TrimSpace(command.Request.RequestID) == "" || command.Task.RequestID != command.Request.RequestID {
		return fmt.Errorf("视频任务必须绑定同一非空请求ID")
	}
	if strings.TrimSpace(command.Task.PublicID) == "" {
		return fmt.Errorf("视频任务公开ID不能为空")
	}
	if command.Task.QuoteID == 0 {
		return fmt.Errorf("视频任务报价ID不能为空")
	}
	if strings.TrimSpace(command.Request.LogicalModelCode) == "" ||
		command.Task.LogicalModelCode != command.Request.LogicalModelCode {
		return fmt.Errorf("视频请求和任务必须使用同一非空逻辑模型")
	}
	if command.Request.UserID == 0 || command.Request.ProjectID == nil || *command.Request.ProjectID == 0 {
		return fmt.Errorf("视频请求用户和Project归属不能为空")
	}
	if command.Task.UserID != command.Request.UserID || command.Task.ProjectID != *command.Request.ProjectID {
		return fmt.Errorf("视频任务必须与请求属于同一用户和Project")
	}

	for index := range command.Inputs {
		input := command.Inputs[index]
		if input.UserID != command.Task.UserID || input.ProjectID != command.Task.ProjectID {
			return fmt.Errorf("视频任务输入必须与任务属于同一用户和Project")
		}
		if input.InputAssetID == 0 || input.Role != model.AITaskInputReferenceImage || input.Ordinal != 0 {
			return fmt.Errorf("图生视频输入必须绑定唯一reference_image资产")
		}
		if !normalizedVideoInputSHA256Pattern.MatchString(input.NormalizedSHA256) {
			return fmt.Errorf("图生视频输入normalized SHA-256必须为64位小写十六进制")
		}
		if input.InputVersion == 0 {
			return fmt.Errorf("图生视频输入version必须大于0")
		}
	}
	return nil
}

// RequestVideoInputPendingDelete 在同一事务内锁定输入资产与执行租约，再写入两个删除时间。
// 本函数只冻结跨表事务不变量，不实现Repository、对象存储删除或后台清理Worker。
func RequestVideoInputPendingDelete(ctx context.Context, transactor VideoInputDeleteTransactor, command VideoInputDeleteCommand) error {
	if command.InputAssetID == 0 || command.UserID == 0 || command.ProjectID == 0 {
		return fmt.Errorf("输入资产及用户Project归属不能为空")
	}
	if command.Now.IsZero() {
		return fmt.Errorf("输入资产删除请求时间不能为空")
	}
	if transactor == nil {
		return fmt.Errorf("输入资产删除事务执行器不能为空")
	}

	if err := transactor.WithinTransaction(ctx, func(tx VideoInputDeleteTransaction) error {
		if tx == nil {
			return fmt.Errorf("输入资产删除事务不能为空")
		}
		asset, err := tx.LoadInputForUpdate(ctx, command.InputAssetID, command.UserID, command.ProjectID)
		if err != nil {
			return fmt.Errorf("加锁读取输入资产失败: %w", err)
		}
		if asset == nil || asset.ID != command.InputAssetID || asset.UserID != command.UserID || asset.ProjectID != command.ProjectID {
			return fmt.Errorf("输入资产不存在或租户归属不匹配")
		}
		if !videoInputLifecycleAllowsDelete(asset.LifecycleState) {
			return fmt.Errorf("输入资产当前状态不允许请求删除: %s", asset.LifecycleState)
		}
		if asset.LegalHold {
			return fmt.Errorf("输入资产处于法律保全状态，不能请求删除")
		}
		activeLeases, err := tx.CountActiveLeasesForUpdate(ctx, command.InputAssetID, command.UserID, command.ProjectID)
		if err != nil {
			return fmt.Errorf("加锁统计输入资产活跃租约失败: %w", err)
		}
		if activeLeases != 0 {
			return fmt.Errorf("输入资产仍有%d个活跃执行租约，不能请求删除", activeLeases)
		}
		if err := tx.MarkPendingDelete(ctx, command.InputAssetID, command.UserID, command.ProjectID, command.Now, command.Now); err != nil {
			return fmt.Errorf("标记输入资产pending_delete失败: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("请求输入资产pending_delete事务失败: %w", err)
	}
	return nil
}

func videoInputLifecycleAllowsDelete(lifecycleState string) bool {
	switch lifecycleState {
	case model.AIInputAssetReady, model.AIInputAssetRejected, model.AIInputAssetQuarantined:
		return true
	default:
		return false
	}
}

// CompleteVideoUploadSession 在同一事务内锁定verifying会话、创建不可变输入快照并以回填ID完成会话。
// 重复完成或任一事实漂移均返回冲突类错误，不实现对象上传、图片规范化或Repository。
func CompleteVideoUploadSession(ctx context.Context, transactor VideoUploadCompletionTransactor, command VideoUploadCompletionCommand) error {
	if command.SessionID == 0 || command.UserID == 0 || command.ProjectID == 0 {
		return fmt.Errorf("上传会话及用户Project归属不能为空")
	}
	if command.Now.IsZero() {
		return fmt.Errorf("上传会话完成时间不能为空")
	}
	if transactor == nil {
		return fmt.Errorf("上传完成事务执行器不能为空")
	}

	inputAsset := command.InputAsset
	if err := transactor.WithinTransaction(ctx, func(tx VideoUploadCompletionTransaction) error {
		if tx == nil {
			return fmt.Errorf("上传完成事务不能为空")
		}
		session, err := tx.LoadUploadSessionForUpdate(ctx, command.SessionID, command.UserID, command.ProjectID)
		if err != nil {
			return fmt.Errorf("加锁读取上传会话失败: %w", err)
		}
		if session == nil || session.ID != command.SessionID || session.UserID != command.UserID || session.ProjectID != command.ProjectID {
			return fmt.Errorf("上传会话不存在或归属不匹配")
		}
		if session.Status != model.AIUploadSessionVerifying {
			return fmt.Errorf("上传会话状态冲突，只有verifying可以完成: %s", session.Status)
		}
		if command.Now.After(session.ExpiresAt) {
			return fmt.Errorf("上传会话已经过期")
		}
		if !nonEmptyStringPointer(session.SourceETag) && !nonEmptyStringPointer(session.SourceVersionID) {
			return fmt.Errorf("上传会话缺少source_etag或source_version_id完整性事实")
		}
		if session.FinalInputAssetID != nil {
			return fmt.Errorf("上传会话已经绑定最终输入资产，不能重复完成")
		}
		if inputAsset.UserID != session.UserID || inputAsset.ProjectID != session.ProjectID {
			return fmt.Errorf("输入快照必须与上传会话属于同一用户和Project")
		}
		if inputAsset.ID != 0 {
			return fmt.Errorf("待插入输入快照不能携带既有内部ID")
		}
		if inputAsset.SourceType != session.SourceType || inputAsset.UploadSessionID == nil || *inputAsset.UploadSessionID != session.ID || inputAsset.SourceGatewayAssetID != nil {
			return fmt.Errorf("输入快照来源必须唯一绑定当前上传会话")
		}
		if err := tx.InsertInputAsset(ctx, &inputAsset); err != nil {
			return fmt.Errorf("插入规范化输入快照失败: %w", err)
		}
		if inputAsset.ID == 0 {
			return fmt.Errorf("插入输入快照后未回填内部ID")
		}
		if err := tx.MarkUploadCompleted(ctx, session.ID, inputAsset.ID, command.Now); err != nil {
			return fmt.Errorf("标记上传会话完成失败: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("完成视频上传会话事务失败: %w", err)
	}
	return nil
}

// ReleaseVideoInputLeases 在同一事务内锁定任务、请求和输入，只释放尚未释放的输入租约。
// 文生视频零输入和已全部释放均为幂等成功，不实现任务Repository或清理Worker。
func ReleaseVideoInputLeases(ctx context.Context, transactor VideoInputLeaseReleaseTransactor, command VideoInputLeaseReleaseCommand) error {
	if command.TaskID == 0 || command.UserID == 0 || command.ProjectID == 0 {
		return fmt.Errorf("任务及用户Project归属不能为空")
	}
	if command.Now.IsZero() {
		return fmt.Errorf("输入租约释放时间不能为空")
	}
	if transactor == nil {
		return fmt.Errorf("输入租约释放事务执行器不能为空")
	}

	if err := transactor.WithinTransaction(ctx, func(tx VideoInputLeaseReleaseTransaction) error {
		if tx == nil {
			return fmt.Errorf("输入租约释放事务不能为空")
		}
		task, err := tx.LoadTaskForUpdate(ctx, command.TaskID, command.UserID, command.ProjectID)
		if err != nil {
			return fmt.Errorf("加锁读取视频任务失败: %w", err)
		}
		if task == nil || task.ID != command.TaskID || task.UserID != command.UserID || task.ProjectID != command.ProjectID {
			return fmt.Errorf("视频任务不存在或归属不匹配")
		}
		if task.CompletedAt == nil || strings.TrimSpace(task.RequestID) == "" {
			return fmt.Errorf("视频任务尚未形成完整终态事实")
		}

		request, err := tx.LoadRequestForUpdate(ctx, task.RequestID, command.UserID, command.ProjectID)
		if err != nil {
			return fmt.Errorf("加锁读取视频请求失败: %w", err)
		}
		if request == nil || request.RequestID != task.RequestID || request.UserID != command.UserID || request.ProjectID == nil || *request.ProjectID != command.ProjectID {
			return fmt.Errorf("视频请求与任务归属或request_id不一致")
		}
		if request.CompletedAt == nil {
			return fmt.Errorf("视频请求尚未形成completed_at终态事实")
		}
		if err := validateVideoLeaseReleaseTerminalPolicy(task.Status, request.BillingStatus); err != nil {
			return err
		}

		inputs, err := tx.LoadTaskInputsForUpdate(ctx, task.ID, command.UserID, command.ProjectID)
		if err != nil {
			return fmt.Errorf("加锁读取视频任务输入失败: %w", err)
		}
		unreleasedIDs := make([]uint64, 0, len(inputs))
		for index := range inputs {
			input := inputs[index]
			if input.ID == 0 || input.TaskID != task.ID || input.UserID != command.UserID || input.ProjectID != command.ProjectID {
				return fmt.Errorf("视频任务输入与任务归属不一致")
			}
			if input.LeaseReleasedAt == nil {
				unreleasedIDs = append(unreleasedIDs, input.ID)
			}
		}
		if len(unreleasedIDs) == 0 {
			return nil
		}
		if err := tx.ReleaseTaskInputLeases(ctx, unreleasedIDs, command.Now); err != nil {
			return fmt.Errorf("释放视频任务输入租约失败: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("释放视频输入租约事务失败: %w", err)
	}
	return nil
}

func nonEmptyStringPointer(value *string) bool {
	return value != nil && strings.TrimSpace(*value) != ""
}

func validateVideoLeaseReleaseTerminalPolicy(taskStatus, billingStatus string) error {
	switch taskStatus {
	case model.AIImageTaskSucceeded:
		if billingStatus != model.AIBillingSettled {
			return fmt.Errorf("成功视频任务只有账单settled后才能释放输入租约")
		}
	case model.AIImageTaskFailed, model.AIImageTaskCancelled, model.AIImageTaskExpired:
		if billingStatus != model.AIBillingReleased && billingStatus != model.AIBillingSettled {
			return fmt.Errorf("失败、取消或过期视频任务只有账单released或settled后才能释放输入租约")
		}
	default:
		return fmt.Errorf("视频任务未安全终结，不能释放输入租约: %s", taskStatus)
	}
	return nil
}
