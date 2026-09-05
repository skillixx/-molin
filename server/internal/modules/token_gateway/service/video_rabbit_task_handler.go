package service

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	videogateway "molin/server/internal/modules/token_gateway/video"
)

type VideoGatewayFactory func(repository.VideoOwner) (*videogateway.VideoGateway, error)
type VideoRabbitTaskFinalizer func(context.Context, string, repository.VideoOwner) error

// VideoRabbitTaskHandler从原账本解析低敏消息并通过执行租约调用同一VideoGateway。
type VideoRabbitTaskHandler struct {
	db        *gorm.DB
	leases    *VideoWorkerLeaseRunner
	publisher *videogateway.TaskPublisher
	factory   VideoGatewayFactory
	finalize  VideoRabbitTaskFinalizer
	workerID  string
}

func NewVideoRabbitTaskHandler(db *gorm.DB, publisher *videogateway.TaskPublisher, factory VideoGatewayFactory, finalizer VideoRabbitTaskFinalizer, workerID string) (*VideoRabbitTaskHandler, error) {
	if db == nil || publisher == nil || factory == nil || finalizer == nil || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,47}$`).MatchString(workerID) {
		return nil, videogateway.ErrTaskHandlerUncertain
	}
	leases, err := NewVideoWorkerLeaseRunner(db)
	if err != nil {
		return nil, err
	}
	return &VideoRabbitTaskHandler{db: db, leases: leases, publisher: publisher, factory: factory, finalize: finalizer, workerID: workerID}, nil
}

// NewVideoRabbitTaskFinalizer复用原财务、交付和容量账本；调用方必须在TaskFetch执行租约内使用。
func NewVideoRabbitTaskFinalizer(app *VideoHTTPService, recovery *repository.VideoCapacityRecoveryRepository, capacity *RedisVideoCapacityStore, key *VideoCapacityNonceKey) (VideoRabbitTaskFinalizer, error) {
	if app == nil || app.billing == nil || recovery == nil || capacity == nil || key == nil {
		return nil, ErrVideoGovernanceUnavailable
	}
	return func(ctx context.Context, taskID string, owner repository.VideoOwner) error {
		if _, err := app.billing.SettleReady(ctx, taskID, owner); err != nil {
			return err
		}
		if _, err := app.billing.DeliverReady(ctx, taskID, owner); err != nil {
			return err
		}
		ledger := NewVideoBillingTaskLedger(app.db, owner, app.billing.protector, VideoServerObjectLocationFactory{}, app.billing.referenceLoader)
		return NewVideoCapacityExecutionCoordinator(ledger, recovery, capacity, key).ReleaseTerminal(ctx, taskID)
	}, nil
}

func (h *VideoRabbitTaskHandler) HandleTask(ctx context.Context, stage videogateway.TaskStage, message videogateway.TaskMessage) (videogateway.TaskDisposition, error) {
	if h == nil || ctx == nil || (stage != videogateway.TaskSubmit && stage != videogateway.TaskPoll && stage != videogateway.TaskFetch) {
		return 0, videogateway.ErrTaskHandlerUncertain
	}
	task, owner, err := h.resolveMessage(ctx, message)
	if err != nil {
		if errors.Is(err, videogateway.ErrTaskMessageInvalid) {
			return videogateway.TaskReject, nil
		}
		return 0, err
	}
	gateway, err := h.factory(owner)
	if err != nil || gateway == nil {
		return 0, videogateway.ErrTaskHandlerUncertain
	}
	result := videogateway.GatewayTask{}
	workErr := h.leases.Execute(ctx, VideoWorkerExecution{TaskID: task.PublicID, Owner: owner, WorkerID: h.workerID + "-" + string(stage), Stage: stage}, func(owned context.Context) error {
		switch stage {
		case videogateway.TaskSubmit:
			result, err = videogateway.NewSubmitWorker(gateway).Run(owned, task.PublicID)
		case videogateway.TaskPoll:
			result, err = videogateway.NewPollWorker(gateway).Run(owned, task.PublicID)
		case videogateway.TaskFetch:
			result, err = videogateway.NewAssetFetchWorker(gateway).Run(owned, task.PublicID)
			if err == nil && result.Status == videogateway.TaskSucceeded {
				err = h.finalize(owned, task.PublicID, owner)
			}
		}
		return err
	})
	if errors.Is(workErr, videogateway.ErrGatewayRunningCapacity) {
		return videogateway.TaskRetry, nil
	}
	if result.TaskID == "" {
		if current, loadErr := gateway.Query(ctx, task.PublicID); loadErr == nil {
			result = current
		}
	}
	disposition, scheduleErr := h.routeNext(ctx, stage, message, result)
	if scheduleErr != nil {
		return 0, scheduleErr
	}
	if workErr != nil {
		// succeeded只代表Provider与资产阶段完成；财务、交付或容量释放失败时必须保留原fetch消息重试。
		if stage == videogateway.TaskFetch && result.Status == videogateway.TaskSucceeded {
			return 0, workErr
		}
		// 已形成submitted/pending/终态时按持久事实调度；其他错误保留原消息未ACK。
		switch result.Status {
		case videogateway.TaskSubmitted, videogateway.TaskProcessing, videogateway.TaskFetching, videogateway.TaskStoring, videogateway.TaskModerating, videogateway.TaskLabeling, videogateway.TaskPendingReconcile, videogateway.TaskSucceeded, videogateway.TaskFailed, videogateway.TaskCancelled, videogateway.TaskExpired:
			return disposition, nil
		default:
			return 0, workErr
		}
	}
	return disposition, nil
}

func (h *VideoRabbitTaskHandler) routeNext(ctx context.Context, stage videogateway.TaskStage, message videogateway.TaskMessage, task videogateway.GatewayTask) (videogateway.TaskDisposition, error) {
	next := videogateway.TaskMessage{TaskID: message.TaskID, RequestID: message.RequestID, InputAssetID: message.InputAssetID, Attempt: 0}
	switch task.Status {
	case videogateway.TaskCreated, videogateway.TaskReserved, videogateway.TaskQueued:
		if stage != videogateway.TaskSubmit {
			return videogateway.TaskReject, nil
		}
		return videogateway.TaskRetry, nil
	case videogateway.TaskSubmitting:
		// 发送权已丢失或提交未知由持久恢复器收口，不重复发布Submit。
		return videogateway.TaskHandled, nil
	case videogateway.TaskSubmitted, videogateway.TaskProcessing:
		if err := h.publisher.PublishDelayed(ctx, videogateway.TaskPoll, next, 0); err != nil {
			return 0, err
		}
		return videogateway.TaskHandled, nil
	case videogateway.TaskFetching, videogateway.TaskStoring, videogateway.TaskModerating, videogateway.TaskLabeling:
		if err := h.publisher.Publish(ctx, videogateway.TaskFetch, next); err != nil {
			return 0, err
		}
		return videogateway.TaskHandled, nil
	case videogateway.TaskPendingReconcile, videogateway.TaskSucceeded, videogateway.TaskFailed, videogateway.TaskCancelled, videogateway.TaskExpired:
		return videogateway.TaskHandled, nil
	default:
		return videogateway.TaskReject, nil
	}
}

func (h *VideoRabbitTaskHandler) resolveMessage(ctx context.Context, message videogateway.TaskMessage) (*repository.VideoTaskRecord, repository.VideoOwner, error) {
	var task repository.VideoTaskRecord
	err := h.db.WithContext(ctx).Table("ai_gateway_tasks AS tasks").Select(`tasks.*,requests.execution_status AS request_execution_status,requests.billing_status,requests.delivery_status,requests.version_no AS request_version_no`).Joins("JOIN ai_requests AS requests ON requests.request_id=tasks.request_id AND requests.user_id=tasks.user_id AND requests.project_id=tasks.project_id").Where("tasks.public_id=? AND BINARY tasks.request_id=BINARY ? AND tasks.capability=? AND tasks.operation IN ?", message.TaskID, message.RequestID, model.AIVideoCapability, []string{model.AIVideoOperationTextToVideo, model.AIVideoOperationImageToVideo}).Take(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && task.Operation == nil) {
		return nil, repository.VideoOwner{}, videogateway.ErrTaskMessageInvalid
	}
	if err != nil {
		return nil, repository.VideoOwner{}, videogateway.ErrTaskHandlerUncertain
	}
	owner := repository.VideoOwner{UserID: task.UserID, ProjectID: task.ProjectID, APIKeyID: task.APIKeyID}
	var inputs []struct{ PublicID string }
	if err := h.db.WithContext(ctx).Table("ai_gateway_task_inputs AS bindings").Select("assets.public_id").Joins("JOIN ai_gateway_input_assets AS assets ON assets.id=bindings.input_asset_id AND assets.user_id=bindings.user_id AND assets.project_id=bindings.project_id").Where("bindings.task_id=?", task.ID).Scan(&inputs).Error; err != nil {
		return nil, repository.VideoOwner{}, videogateway.ErrTaskMessageInvalid
	}
	if *task.Operation == model.AIVideoOperationTextToVideo {
		if len(inputs) != 0 || message.InputAssetID != "" {
			return nil, repository.VideoOwner{}, videogateway.ErrTaskMessageInvalid
		}
	} else if len(inputs) != 1 || !strings.EqualFold(inputs[0].PublicID, message.InputAssetID) || inputs[0].PublicID != message.InputAssetID {
		return nil, repository.VideoOwner{}, videogateway.ErrTaskMessageInvalid
	}
	return &task, owner, nil
}

var _ videogateway.TaskMessageHandler = (*VideoRabbitTaskHandler)(nil)
