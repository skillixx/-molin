package service

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

// 归档执行器只有读取原内容和生成安全产物的边界，不持有Submit/Query能力。
type videoArchiveContentReader interface {
	Name() string
	OpenContent(context.Context, video.ControlledContentRef) (video.StreamContent, error)
}
type videoArchiveExecutionOptions struct {
	content    videoArchiveContentReader
	store      video.VideoArchiveObjectStore
	probe      *video.VideoMediaProbe
	safety     *video.VideoSafetyPipeline
	labeler    video.VideoAILabeler
	locator    repository.VideoObjectLocationFactory
	completeTx func(context.Context, *gorm.DB, *repository.VideoTaskRecord) error
	failureTx  func(context.Context, *gorm.DB, *repository.VideoTaskRecord) error
}

type videoArchiveExecutionLedger struct {
	*VideoRepositoryTaskLedger
	admin      *VideoAdminService
	caller     VideoCaller
	taskID     string
	proof      *repository.VideoArchiveFenceProof
	physical   video.VideoObjectStore
	completed  atomic.Bool
	completeTx func(context.Context, *gorm.DB, *repository.VideoTaskRecord) error
	failureTx  func(context.Context, *gorm.DB, *repository.VideoTaskRecord) error
}

// 上层必须先持久化管理员命令/前审计并认领围栏；本函数不是可公开调用的HTTP写入口。
func runVideoArchiveRecovery(ctx context.Context, admin *VideoAdminService, caller VideoCaller, id string, owner repository.VideoOwner, proof *repository.VideoArchiveFenceProof, o videoArchiveExecutionOptions) error {
	if admin == nil || admin.app == nil || proof == nil || o.content == nil || o.content.Name() != "fake-native-async" || o.store == nil || o.probe == nil || o.safety == nil || o.labeler == nil || o.locator == nil {
		return ErrVideoAccessUnavailable
	}
	base := admin.app.NewTaskLedger(owner, o.locator)
	base.recoveryTaskID = id
	l := &videoArchiveExecutionLedger{VideoRepositoryTaskLedger: base, admin: admin, caller: caller, taskID: id, proof: proof, physical: o.store, completeTx: o.completeTx, failureTx: o.failureTx}
	if _, err := l.Load(ctx, id); err != nil {
		return err
	}
	claim, err := repository.NewVideoTaskRepository(l.db).FindForOwner(ctx, id, owner)
	if err != nil {
		return err
	}
	if claim.ArchiveLeaseUntil == nil {
		return repository.ErrVideoTaskConflict
	}
	deadline := *claim.ArchiveLeaseUntil
	if caller.credential != nil && caller.credential.expiresAt.Before(deadline) {
		deadline = caller.credential.expiresAt
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if err := o.store.AdvanceArchiveFence(ctx, id, proof.Generation()); err != nil {
		return err
	}
	ctx = video.WithArchiveWriteGeneration(ctx, id, proof.Generation())
	provider := &videoArchiveProviderBoundary{reader: o.content, ledger: l}
	store := &videoArchiveStoreBoundary{VideoArchiveObjectStore: o.store, ledger: l}
	g := video.NewVideoGateway(video.VideoGatewayDependencies{Ledger: l, Provider: provider, Store: store, Probe: o.probe, Safety: o.safety, Labeler: o.labeler})
	result, err := g.FetchAndFinalize(ctx, id)
	if err != nil {
		return err
	}
	if result.Status != video.TaskSucceeded || !l.completed.Load() {
		return ErrVideoReconciliation
	}
	return nil
}

// 每次读都锁原Task/Request并重验管理员、围栏、确认成本和当前资产保护状态。
func (l *videoArchiveExecutionLedger) loadTx(ctx context.Context, tx *gorm.DB) (*repository.VideoTaskRecord, video.GatewayTask, error) {
	r, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, l.taskID, l.owner)
	if err != nil {
		return nil, video.GatewayTask{}, err
	}
	if err := l.admin.authorizeTx(ctx, tx, l.caller, "ai_gateway:task_manage"); err != nil {
		return nil, video.GatewayTask{}, err
	}
	if !l.completed.Load() {
		if err := repository.CheckVideoArchiveFence(r, l.proof, time.Now().UTC()); err != nil {
			return nil, video.GatewayTask{}, err
		}
	}
	var quote model.AIGatewayQuote
	if err := tx.Where("id=?", r.QuoteID).Take(&quote).Error; err != nil {
		return nil, video.GatewayTask{}, err
	}
	if _, err := loadVideoConfirmedCostTx(tx, r, &quote); err != nil {
		return nil, video.GatewayTask{}, err
	}
	for _, table := range []string{"ai_video_media_deletions", "ai_video_asset_deletions"} {
		var count int64
		if err := tx.Table(table).Where("task_id=?", r.ID).Count(&count).Error; err != nil {
			return nil, video.GatewayTask{}, err
		}
		if count != 0 {
			return nil, video.GatewayTask{}, ErrVideoMediaProtected
		}
	}
	base := l.VideoRepositoryTaskLedger.withDB(tx)
	task, err := base.loadRecoveryMetadata(ctx, l.taskID)
	if err != nil {
		return nil, task, err
	}
	if l.completed.Load() {
		if r.Status != "succeeded" || r.ArchiveTokenHash != nil {
			return nil, task, ErrVideoReconciliation
		}
	} else {
		if r.ArchivePhase == nil {
			return nil, task, ErrVideoReconciliation
		}
		task.Status = video.TaskStatus(*r.ArchivePhase)
		if *r.ArchivePhase == "verified" {
			return nil, task, ErrVideoReconciliation
		}
	}
	task.Content = &video.ControlledContentRef{ProviderTaskID: task.ProviderTaskID, ContentID: "content-" + task.ProviderTaskID, MediaType: "video/mp4"}
	var assets []model.AIImageAsset
	if err := tx.Where("task_id=?", r.ID).Order("id").Find(&assets).Error; err != nil {
		return nil, task, err
	}
	for _, a := range assets {
		if a.RequestID != r.RequestID || a.UserID != r.UserID || a.ProjectID != r.ProjectID || a.Modality != "video" || a.LifecycleState != "temporary" || a.LegalHold || a.DisputeStatus == model.AIImageDisputeOpen || a.DeletedAt != nil || a.MediaDeletedAt != nil || !a.ExpiresAt.After(time.Now().UTC()) || a.ModerationStatus == "rejected" || a.ModerationStatus == "error" || a.ExplicitLabelStatus == "failed" || a.ImplicitLabelStatus == "failed" {
			return nil, task, ErrVideoMediaProtected
		}
		if a.AssetRole == "content" {
			if task.Asset != nil || a.ParentAssetID != nil {
				return nil, task, ErrVideoReconciliation
			}
			task.Asset = mapVideoG4Asset(&a)
			if task.Asset == nil {
				return nil, task, ErrVideoReconciliation
			}
		}
	}
	if len(assets) != 0 && task.Asset == nil {
		return nil, task, ErrVideoReconciliation
	}
	for _, a := range assets {
		if a.AssetRole == "content" {
			continue
		}
		if a.ParentAssetID == nil {
			return nil, task, ErrVideoReconciliation
		}
		var rootID uint64
		for _, r := range assets {
			if r.AssetRole == "content" {
				rootID = r.ID
			}
		}
		if *a.ParentAssetID != rootID {
			return nil, task, ErrVideoReconciliation
		}
		child := mapVideoG4Asset(&a)
		if child == nil {
			return nil, task, ErrVideoReconciliation
		}
		child.ParentAssetID = task.Asset.AssetID
		task.Asset.Children = append(task.Asset.Children, *child)
	}
	if len(assets) != 0 && len(assets) != 1 && len(assets) != 6 {
		return nil, task, ErrVideoReconciliation
	}
	if (task.Status == video.TaskModerating || task.Status == video.TaskLabeling) && task.Asset == nil {
		return nil, task, ErrVideoReconciliation
	}
	if task.Status == video.TaskLabeling && task.Asset.ModerationStatus != video.AssetModerationPassed {
		return nil, task, ErrVideoReconciliation
	}
	return r, task, nil
}

func (l *videoArchiveExecutionLedger) Load(ctx context.Context, id string) (video.GatewayTask, error) {
	if id != l.taskID {
		return video.GatewayTask{}, repository.ErrVideoTaskNotFound
	}
	var task video.GatewayTask
	err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, t, err := l.loadTx(ctx, tx)
		if err != nil {
			return err
		}
		task = t
		if err := l.admin.authorizeTx(ctx, tx, l.caller, "ai_gateway:task_manage"); err != nil {
			return err
		}
		if !l.completed.Load() {
			return repository.CheckVideoArchiveFence(r, l.proof, time.Now().UTC())
		}
		return nil
	})
	return task, err
}

// 技术推进、原资产事实与原Task允许迁移同事务；pending不回退，成功必须重新验证完整六资产和计量。
func (l *videoArchiveExecutionLedger) Advance(ctx context.Context, id string, version uint64, to video.TaskStatus, source, reason string, mutate video.TaskMutation) (video.GatewayTask, error) {
	if id != l.taskID || l.completed.Load() {
		return video.GatewayTask{}, repository.ErrVideoTaskConflict
	}
	if to == video.TaskFailed || to == video.TaskPendingReconcile {
		return l.failStage(ctx, id, version, reason, mutate)
	}
	var output video.GatewayTask
	completed := false
	err := retryVideoBillingTransaction(ctx, func() error {
		return l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			r, current, err := l.loadTx(ctx, tx)
			if err != nil {
				return err
			}
			if r.VersionNo != version {
				return repository.ErrVideoTaskConflict
			}
			leaseUntil := *r.ArchiveLeaseUntil
			var mediaUntil time.Time
			next := current
			if current.Asset != nil {
				copy := *current.Asset
				copy.Children = append([]video.GatewayAsset(nil), current.Asset.Children...)
				next.Asset = &copy
			}
			if mutate != nil {
				if err := mutate(&next); err != nil {
					return err
				}
			}
			targetPhase := string(to)
			if to == video.TaskSucceeded {
				targetPhase = "verified"
			}
			allowed := map[string]string{"fetching": "storing", "storing": "moderating", "moderating": "labeling", "labeling": "verified"}
			if r.ArchivePhase == nil || allowed[*r.ArchivePhase] != targetPhase {
				return repository.ErrVideoTaskTransition
			}
			applyCtx := context.WithValue(ctx, videoBillingOuterTransactionKey{}, true)
			base := l.VideoRepositoryTaskLedger.withDB(tx.WithContext(applyCtx))
			if err := base.persistAssetMutation(applyCtx, current, next, time.Now().UTC()); err != nil {
				return err
			}
			if to == video.TaskSucceeded {
				root, err := loadVideoSettlementMediaTx(tx, r, true, time.Now().UTC())
				if err != nil {
					return err
				}
				var quote model.AIGatewayQuote
				if err := tx.Where("id=?", r.QuoteID).Take(&quote).Error; err != nil {
					return err
				}
				cost, err := loadVideoConfirmedCostTx(tx, r, &quote)
				if err != nil {
					return err
				}
				if root.DurationSeconds == nil || !cost.Quantity.Equal(*root.DurationSeconds) {
					return ErrVideoReconciliation
				}
				// 只核验六份已归档对象的元数据，不在最终事务内重新抓取媒体正文。
				var all []model.AIImageAsset
				if err := tx.Where("task_id=?", r.ID).Find(&all).Error; err != nil {
					return err
				}
				for _, a := range all {
					if mediaUntil.IsZero() || a.ExpiresAt.Before(mediaUntil) {
						mediaUntil = a.ExpiresAt
					}
					meta, err := l.physical.Head(ctx, video.VideoObjectRef{Bucket: *a.Bucket, ObjectKey: *a.ObjectKey})
					if err != nil || meta.Ref.Bucket != *a.Bucket || meta.Ref.ObjectKey != *a.ObjectKey || meta.SHA256 != *a.SHA256 || meta.SizeBytes != *a.SizeBytes {
						return ErrVideoReconciliation
					}
				}
			}
			repo := repository.NewVideoTaskRepository(tx)
			if r.Status != "pending_reconcile" || to == video.TaskSucceeded {
				r, err = repo.TransitionExecution(applyCtx, repository.VideoStateTransition{TaskPublicID: id, Owner: l.owner, ExpectedVersion: r.VersionNo, ToStatus: string(to), Progress: videoG4Progress(to), EventID: "vg6_archive_state_" + videoBillingDigest(fmt.Sprintf("%s:%d:%s", id, r.VersionNo, to)), Source: "worker", Now: time.Now().UTC(), ArchiveFence: l.proof})
				if err != nil {
					return err
				}
			}
			r, err = repo.AdvanceArchivePhase(applyCtx, id, l.owner, r.VersionNo, l.proof, targetPhase, time.Now().UTC())
			if err != nil {
				return err
			}
			if to == video.TaskSucceeded {
				root, err := loadVideoSettlementMediaTx(tx, r, true, time.Now().UTC())
				if err != nil || root == nil {
					return ErrVideoReconciliation
				}
				r, err = repo.ReleaseArchiveFence(applyCtx, id, l.owner, r.VersionNo, l.proof, time.Now().UTC())
				if err != nil {
					return err
				}
				completed = true
				if l.completeTx != nil {
					if err := l.completeTx(ctx, tx, r); err != nil {
						return err
					}
				}
			}
			// 清围栏及最终读取也可能等待；所有数据库动作之后才做最终授权，并核对保存的原期限。
			if err := l.admin.authorizeTx(ctx, tx, l.caller, "ai_gateway:task_manage"); err != nil {
				return err
			}
			now := time.Now().UTC()
			if !leaseUntil.After(now) || (!mediaUntil.IsZero() && !mediaUntil.After(now)) {
				return repository.ErrVideoTaskConflict
			}
			next.Status, next.Version = to, r.VersionNo
			output = next
			return nil
		})
	})
	if err == nil && completed {
		l.completed.Store(true)
	}
	return output, err
}

// 明确拒绝保留实际安全事实；pending不能伪装为moderating/labeling以制造G5退款证明。
func (l *videoArchiveExecutionLedger) failStage(ctx context.Context, id string, version uint64, reason string, mutate video.TaskMutation) (video.GatewayTask, error) {
	var output video.GatewayTask
	err := retryVideoBillingTransaction(ctx, func() error {
		return l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			r, current, err := l.loadTx(ctx, tx)
			if err != nil {
				return err
			}
			if r.VersionNo != version {
				return repository.ErrVideoTaskConflict
			}
			leaseUntil := *r.ArchiveLeaseUntil
			next := current
			if current.Asset != nil {
				copy := *current.Asset
				copy.Children = append([]video.GatewayAsset(nil), current.Asset.Children...)
				next.Asset = &copy
			}
			if mutate != nil {
				if err := mutate(&next); err != nil {
					return err
				}
			}
			inner := context.WithValue(ctx, videoBillingOuterTransactionKey{}, true)
			origin := ""
			if next.Asset != nil && (next.Asset.ModerationStatus == video.AssetModerationRejected || next.Asset.ModerationStatus == video.AssetModerationError || next.Asset.ExplicitLabelStatus == video.LabelFailed || next.Asset.ImplicitLabelStatus == video.LabelFailed) {
				if err := l.VideoRepositoryTaskLedger.withDB(tx.WithContext(inner)).persistAssetMutation(inner, current, next, time.Now().UTC()); err != nil {
					return err
				}
				if reason == "moderation_failed" && next.Asset.ModerationStatus == video.AssetModerationRejected && r.Status == "moderating" {
					origin = "moderation_rejected"
				}
				if reason == "label_failed" && r.Status == "labeling" {
					origin = "label_failed"
				}
			}
			repo := repository.NewVideoTaskRepository(tx)
			if origin != "" {
				r, err = repo.TransitionExecution(inner, repository.VideoStateTransition{TaskPublicID: id, Owner: l.owner, ExpectedVersion: r.VersionNo, ToStatus: "failed", Progress: r.Progress, EventID: "vg6_archive_rejected_" + videoBillingDigest(fmt.Sprintf("%s:%d", id, r.VersionNo)), Source: "worker", Now: time.Now().UTC(), FailureOrigin: origin, ArchiveFence: l.proof})
				if err != nil {
					return err
				}
			} else if r.Status != "pending_reconcile" {
				r, err = repo.TransitionExecution(inner, repository.VideoStateTransition{TaskPublicID: id, Owner: l.owner, ExpectedVersion: r.VersionNo, ToStatus: "pending_reconcile", Progress: r.Progress, EventID: "vg6_archive_uncertain_" + videoBillingDigest(fmt.Sprintf("%s:%d", id, r.VersionNo)), Source: "worker", Now: time.Now().UTC(), ArchiveFence: l.proof})
				if err != nil {
					return err
				}
			}
			if _, err := l.admin.app.billing.reconcileVideoExecutionTx(inner, tx, r, l.owner); err != nil {
				return err
			}
			r, err = repo.ReleaseArchiveFence(inner, id, l.owner, r.VersionNo, l.proof, time.Now().UTC())
			if err != nil {
				return err
			}
			if l.failureTx != nil {
				if err := l.failureTx(ctx, tx, r); err != nil {
					return err
				}
			}
			if err := l.admin.authorizeTx(ctx, tx, l.caller, "ai_gateway:task_manage"); err != nil {
				return err
			}
			if !leaseUntil.After(time.Now().UTC()) {
				return repository.ErrVideoTaskConflict
			}
			output = next
			output.Status, output.Version = video.TaskStatus(r.Status), r.VersionNo
			return nil
		})
	})
	return output, err
}

// 归档只完成媒体与执行事实；预占中的输入租约继续由原G5结算/交付逻辑管理。
func (l *videoArchiveExecutionLedger) ReleaseLeaseOnce(ctx context.Context, id string) (video.GatewayTask, error) {
	return l.Load(ctx, id)
}

func (l *videoArchiveExecutionLedger) checkIO(ctx context.Context) error {
	if l.completed.Load() {
		return repository.ErrVideoTaskConflict
	}
	_, err := l.Load(ctx, l.taskID)
	return err
}

// 不允许归档适配器重新提交、轮询、取消或删除原Provider任务。
type videoArchiveProviderBoundary struct {
	reader videoArchiveContentReader
	ledger *videoArchiveExecutionLedger
}

func (p *videoArchiveProviderBoundary) Name() string { return p.reader.Name() }
func (p *videoArchiveProviderBoundary) Submit(context.Context, video.SubmitRequest) (video.SubmitResult, error) {
	return video.SubmitResult{}, video.ErrDuplicateSubmitForbidden
}
func (p *videoArchiveProviderBoundary) Query(context.Context, video.QueryRequest) (video.QueryResult, error) {
	return video.QueryResult{}, video.ErrVideoRequestInvalid
}
func (p *videoArchiveProviderBoundary) Cancel(context.Context, video.CancelRequest) (video.QueryResult, error) {
	return video.QueryResult{}, video.ErrVideoRequestInvalid
}
func (p *videoArchiveProviderBoundary) Delete(context.Context, video.ControlledContentRef) error {
	return video.ErrVideoRequestInvalid
}
func (p *videoArchiveProviderBoundary) OpenContent(ctx context.Context, ref video.ControlledContentRef) (video.StreamContent, error) {
	if err := p.ledger.checkIO(ctx); err != nil {
		return video.StreamContent{}, err
	}
	content, err := p.reader.OpenContent(ctx, ref)
	if err != nil {
		return content, err
	}
	if content.Ref != ref || content.ReaderAt == nil || content.SizeBytes <= 0 {
		return video.StreamContent{}, video.ErrProviderResultCorrupt
	}
	if err := p.ledger.checkIO(ctx); err != nil {
		return video.StreamContent{}, err
	}
	return content, nil
}

type videoArchiveStoreBoundary struct {
	video.VideoArchiveObjectStore
	ledger *videoArchiveExecutionLedger
}

func (s *videoArchiveStoreBoundary) Put(ctx context.Context, r video.PutVideoObjectRequest) (video.StoredVideoObject, error) {
	if r.TaskID != s.ledger.taskID {
		return video.StoredVideoObject{}, video.ErrVideoObjectInvalid
	}
	if err := s.ledger.checkIO(ctx); err != nil {
		return video.StoredVideoObject{}, err
	}
	o, err := s.VideoArchiveObjectStore.Put(ctx, r)
	if err != nil {
		return o, err
	}
	return o, s.ledger.checkIO(ctx)
}
func (s *videoArchiveStoreBoundary) PromoteToResult(ctx context.Context, r video.VideoObjectRef) (video.StoredVideoObject, error) {
	if err := s.ledger.checkIO(ctx); err != nil {
		return video.StoredVideoObject{}, err
	}
	o, err := s.VideoArchiveObjectStore.PromoteToResult(ctx, r)
	if err != nil {
		return o, err
	}
	return o, s.ledger.checkIO(ctx)
}
func (s *videoArchiveStoreBoundary) MoveToQuarantine(ctx context.Context, r video.VideoObjectRef) (video.StoredVideoObject, error) {
	if err := s.ledger.checkIO(ctx); err != nil {
		return video.StoredVideoObject{}, err
	}
	o, err := s.VideoArchiveObjectStore.MoveToQuarantine(ctx, r)
	if err != nil {
		return o, err
	}
	return o, s.ledger.checkIO(ctx)
}
func (s *videoArchiveStoreBoundary) Delete(context.Context, video.VideoObjectRef) error {
	return errors.New("归档恢复不能删除媒体")
}
