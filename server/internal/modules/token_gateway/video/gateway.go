package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
)

type VideoGatewayDependencies struct {
	Ledger      VideoTaskLedger
	Provider    VideoProviderAdapter
	Verifier    ProviderCallbackVerifier
	Probe       *VideoMediaProbe
	Safety      *VideoSafetyPipeline
	Labeler     VideoAILabeler
	Store       VideoObjectStore
	FetchPolicy LocalOnlyMediaFetchPolicy
}

type VideoGateway struct {
	deps VideoGatewayDependencies
}

func NewVideoGateway(dependencies VideoGatewayDependencies) *VideoGateway {
	return &VideoGateway{deps: dependencies}
}

func (g *VideoGateway) Query(ctx context.Context, taskID string) (GatewayTask, error) {
	if g == nil || g.deps.Ledger == nil {
		return GatewayTask{}, ErrGatewayTaskNotFound
	}
	return g.deps.Ledger.Load(ctx, taskID)
}

func (g *VideoGateway) Submit(ctx context.Context, taskID string) (GatewayTask, error) {
	task, err := g.Query(ctx, taskID)
	if err != nil {
		return GatewayTask{}, err
	}
	if taskSafeTerminal(task.Status) {
		return g.deps.Ledger.ReleaseLeaseOnce(ctx, task.TaskID)
	}
	if taskStatusRank(task.Status) >= taskStatusRank(TaskSubmitted) {
		return task, nil
	}
	if task.Status == TaskSubmitting {
		return g.advance(ctx, task, TaskPendingReconcile, "worker", "submitting_recovery_unknown", nil)
	}
	if err := validateGatewayTask(task); err != nil {
		return g.failTask(ctx, task, "input_invalid", err)
	}
	if err := g.deps.Safety.Preflight(ctx, VideoSafetyRequest{Operation: task.Operation, Prompt: task.Prompt, Reference: task.Reference}); err != nil {
		return g.failTask(ctx, task, "input_moderation_failed", err)
	}
	var remaining []TaskStatus
	switch task.Status {
	case TaskCreated:
		remaining = []TaskStatus{TaskReserved, TaskQueued, TaskSubmitting}
	case TaskReserved:
		remaining = []TaskStatus{TaskQueued, TaskSubmitting}
	case TaskQueued:
		remaining = []TaskStatus{TaskSubmitting}
	default:
		return task, ErrGatewayTaskTransition
	}
	for _, status := range remaining {
		task, err = g.advance(ctx, task, status, "worker", "state_advanced", nil)
		if err != nil {
			return task, err
		}
	}
	result, submitErr := g.deps.Provider.Submit(ctx, SubmitRequest{
		RequestID: task.RequestID, Operation: task.Operation, Prompt: task.Prompt, Input: task.Input, Spec: task.Spec,
	})
	if errors.Is(submitErr, ErrSubmitAcknowledgementLost) {
		if result.ProviderTaskID == "" {
			task, _ = g.advance(ctx, task, TaskPendingReconcile, "worker", "ack_lost_unknown", nil)
			return task, submitErr
		}
		task, err = g.advance(ctx, task, TaskSubmitted, "worker", "ack_lost_known", func(next *GatewayTask) error {
			next.ProviderCode, next.ProviderTaskID = result.ProviderCode, result.ProviderTaskID
			return nil
		})
		if err != nil {
			return task, err
		}
		return task, submitErr
	}
	if submitErr != nil {
		if errors.Is(submitErr, ErrProviderTimeout) || errors.Is(submitErr, ErrProviderResultUnknown) {
			task, _ = g.advance(ctx, task, TaskPendingReconcile, "worker", "submit_unknown", nil)
			return task, submitErr
		}
		return g.failTask(ctx, task, "submit_failed", submitErr)
	}
	return g.advance(ctx, task, TaskSubmitted, "worker", "submitted", func(next *GatewayTask) error {
		next.ProviderCode, next.ProviderTaskID = result.ProviderCode, result.ProviderTaskID
		return nil
	})
}

func (g *VideoGateway) Poll(ctx context.Context, taskID string) (GatewayTask, error) {
	task, err := g.Query(ctx, taskID)
	if err != nil {
		return GatewayTask{}, err
	}
	if taskSafeTerminal(task.Status) {
		return g.deps.Ledger.ReleaseLeaseOnce(ctx, task.TaskID)
	}
	if task.Status != TaskSubmitted && task.Status != TaskProcessing {
		return task, nil
	}
	result, queryErr := g.deps.Provider.Query(ctx, QueryRequest{ProviderTaskID: task.ProviderTaskID})
	if queryErr != nil && !errors.Is(queryErr, ErrProviderExplicitFailure) {
		if errors.Is(queryErr, ErrProviderTimeout) || errors.Is(queryErr, ErrProviderResultUnknown) {
			return g.advance(ctx, task, TaskPendingReconcile, "worker", "query_unknown", nil)
		}
		return g.failTask(ctx, task, "query_failed", queryErr)
	}
	switch result.Status {
	case ProviderTaskProcessing:
		if task.Status == TaskProcessing {
			return task, nil
		}
		return g.advance(ctx, task, TaskProcessing, "worker", "provider_processing", nil)
	case ProviderTaskSucceeded:
		return g.advance(ctx, task, TaskFetching, "worker", "provider_succeeded", func(next *GatewayTask) error {
			next.Content = result.Content
			return nil
		})
	case ProviderTaskFailed:
		return g.failTask(ctx, task, "provider_failed", queryErr)
	case ProviderTaskCancelled:
		return g.cancelTerminal(ctx, task, "provider_cancelled")
	case ProviderTaskUnknown:
		return g.advance(ctx, task, TaskPendingReconcile, "worker", "provider_unknown", nil)
	default:
		return g.failTask(ctx, task, "provider_invalid_status", ErrProviderResultUnknown)
	}
}

func (g *VideoGateway) FetchAndFinalize(ctx context.Context, taskID string) (GatewayTask, error) {
	task, err := g.Query(ctx, taskID)
	if err != nil {
		return GatewayTask{}, err
	}
	for attempt := 0; attempt < 12; attempt++ {
		switch task.Status {
		case TaskFetching:
			content, media, openErr := g.openAndProbe(ctx, task)
			_ = content
			if openErr != nil {
				return g.failTask(ctx, task, "fetch_or_probe_failed", openErr)
			}
			task, err = g.advance(ctx, task, TaskStoring, "worker", "media_probed", func(next *GatewayTask) error {
				next.Media = &media
				return nil
			})
		case TaskStoring:
			content, media, openErr := g.openAndProbe(ctx, task)
			if openErr != nil {
				return g.failTask(ctx, task, "fetch_or_probe_failed", openErr)
			}
			stored, storeErr := g.deps.Store.Put(ctx, PutVideoObjectRequest{
				Zone: VideoObjectTemporary, TaskID: task.TaskID, AssetID: "vasset-" + task.TaskID, Role: "content",
				Body: io.NewSectionReader(content.ReaderAt, 0, content.SizeBytes), MaxBytes: g.deps.Probe.limits.MaxBytes,
			})
			if storeErr != nil {
				return g.failTask(ctx, task, "store_failed", storeErr)
			}
			asset := &GatewayAsset{AssetID: "vasset-" + task.TaskID, Role: "content", Object: stored, MIMEType: media.MIMEType,
				SizeBytes: stored.SizeBytes, SHA256: stored.SHA256, Width: media.Width, Height: media.Height,
				DurationMillis: media.DurationMillis, FrameRate: media.FrameRate, VideoCodec: media.VideoCodec,
				AudioCodec: media.AudioCodec, HasAudio: media.HasAudio, Lifecycle: AssetTemporary,
				ExplicitLabelStatus: LabelPending, ImplicitLabelStatus: LabelPending, ModerationStatus: AssetModerationPending}
			task, err = g.advance(ctx, task, TaskModerating, "worker", "stored", func(next *GatewayTask) error {
				next.Asset = asset
				return nil
			})
		case TaskModerating:
			if task.Asset == nil {
				return g.advance(ctx, task, TaskPendingReconcile, "worker", "asset_missing", nil)
			}
			media := mediaFromGatewayAsset(task.Asset)
			if _, assessErr := g.deps.Safety.AssessOutput(ctx, media); assessErr != nil {
				return g.quarantineAndFail(ctx, task, task.Asset.Object.Ref, "moderation_failed", assessErr, func(next *GatewayTask, quarantined StoredVideoObject) {
					next.Asset.Object = quarantined
					if errors.Is(assessErr, ErrVideoModerationRejected) {
						next.Asset.ModerationStatus = AssetModerationRejected
					} else {
						next.Asset.ModerationStatus = AssetModerationError
					}
				})
			}
			task, err = g.advance(ctx, task, TaskLabeling, "worker", "moderation_passed", func(next *GatewayTask) error {
				next.Asset.ModerationPassed, next.Asset.ModerationStatus = true, AssetModerationPassed
				return nil
			})
		case TaskLabeling:
			if task.Asset == nil {
				return g.advance(ctx, task, TaskPendingReconcile, "worker", "asset_missing", nil)
			}
			labels, labelErr := g.deps.Labeler.Apply(ctx, LabelRequest{TaskID: task.TaskID, AssetID: task.Asset.AssetID, SHA256: task.Asset.SHA256})
			if labelErr != nil {
				labels = failedLabelResult(labels, "fake-label-failure-v1")
				return g.quarantineAndFail(ctx, task, task.Asset.Object.Ref, "label_failed", labelErr, func(next *GatewayTask, quarantined StoredVideoObject) {
					next.Asset.Object, next.Asset.ExplicitLabelStatus, next.Asset.ImplicitLabelStatus, next.Asset.LabelVersion = quarantined, labels.ExplicitStatus, labels.ImplicitStatus, labels.Version
				})
			}
			promoted, promoteErr := g.ensureObjectZone(ctx, task.Asset.Object.Ref, VideoObjectResult)
			if promoteErr != nil {
				updated, transitionErr := g.advance(ctx, task, TaskPendingReconcile, "worker", "result_promotion_unknown", nil)
				if transitionErr != nil {
					return updated, transitionErr
				}
				return updated, promoteErr
			}
			content, _, openErr := g.openAndProbe(ctx, task)
			if openErr != nil {
				return g.quarantineAndFail(ctx, task, promoted.Ref, "derived_source_failed", openErr, markDerivedDeliveryFailed)
			}
			children, derivedErr := g.createDerivedAssets(ctx, task, content)
			if derivedErr != nil {
				return g.quarantineAndFail(ctx, task, promoted.Ref, "derived_failed", derivedErr, markDerivedDeliveryFailed)
			}
			task, err = g.advance(ctx, task, TaskSucceeded, "worker", "available", func(next *GatewayTask) error {
				next.Asset.ExplicitLabelStatus, next.Asset.ImplicitLabelStatus, next.Asset.LabelVersion = labels.ExplicitStatus, labels.ImplicitStatus, labels.Version
				next.Asset.Lifecycle, next.Asset.Object, next.Asset.Children = AssetAvailable, promoted, children
				return nil
			})
			if err == nil {
				return g.deps.Ledger.ReleaseLeaseOnce(ctx, task.TaskID)
			}
		case TaskSucceeded:
			return g.deps.Ledger.ReleaseLeaseOnce(ctx, task.TaskID)
		case TaskFailed, TaskCancelled, TaskExpired:
			return g.deps.Ledger.ReleaseLeaseOnce(ctx, task.TaskID)
		default:
			return task, nil
		}
		if err != nil {
			return task, err
		}
	}
	return task, ErrGatewayTaskConflict
}

func (g *VideoGateway) openAndProbe(ctx context.Context, task GatewayTask) (StreamContent, VideoMediaMetadata, error) {
	if task.Content == nil {
		return StreamContent{}, VideoMediaMetadata{}, ErrProviderTaskNotFound
	}
	if err := g.deps.FetchPolicy.Validate(MediaFetchAttempt{ControlledRef: task.Content}); err != nil {
		return StreamContent{}, VideoMediaMetadata{}, err
	}
	content, err := g.deps.Provider.OpenContent(ctx, *task.Content)
	if err != nil {
		return StreamContent{}, VideoMediaMetadata{}, err
	}
	media, err := g.deps.Probe.Probe(ctx, content)
	return content, media, err
}

func mediaFromGatewayAsset(asset *GatewayAsset) VideoMediaMetadata {
	return VideoMediaMetadata{MIMEType: asset.MIMEType, Container: "mp4", Width: asset.Width, Height: asset.Height,
		DurationMillis: asset.DurationMillis, FrameRate: asset.FrameRate, VideoCodec: asset.VideoCodec,
		AudioCodec: asset.AudioCodec, HasAudio: asset.HasAudio, SizeBytes: asset.SizeBytes, SHA256: asset.SHA256}
}

func failedLabelResult(result LabelResult, fallbackVersion string) LabelResult {
	if strings.TrimSpace(result.Version) == "" {
		result.Version = fallbackVersion
	}
	if result.ExplicitStatus == LabelPending {
		result.ExplicitStatus = LabelFailed
	}
	if result.ImplicitStatus == LabelPending {
		result.ImplicitStatus = LabelFailed
	}
	return result
}

func markDerivedDeliveryFailed(next *GatewayTask, quarantined StoredVideoObject) {
	if next.Asset == nil {
		return
	}
	next.Asset.Object = quarantined
	next.Asset.ExplicitLabelStatus = LabelFailed
	next.Asset.ImplicitLabelStatus = LabelFailed
	next.Asset.LabelVersion = "fake-derived-safety-v1"
}

func (g *VideoGateway) createDerivedAssets(ctx context.Context, task GatewayTask, content StreamContent) ([]GatewayAsset, error) {
	roles := []string{"cover", "preview", "thumbnail", "moderation_copy", "derived"}
	children := make([]GatewayAsset, 0, len(roles))
	for _, role := range roles {
		assetID := "vasset-" + task.TaskID + "-" + role
		mimeType := "image/png"
		var payload []byte
		child := GatewayAsset{
			AssetID: assetID, Role: role, ParentAssetID: task.Asset.AssetID,
			Width: 1, Height: 1, ModerationStatus: AssetModerationPending,
			ExplicitLabelStatus: LabelPending, ImplicitLabelStatus: LabelPending, Lifecycle: AssetTemporary,
		}
		if role == "preview" || role == "derived" {
			mimeType = "video/mp4"
			child.Width, child.Height = task.Asset.Width, task.Asset.Height
			child.DurationMillis, child.FrameRate = task.Asset.DurationMillis, task.Asset.FrameRate
			child.VideoCodec, child.AudioCodec, child.HasAudio = task.Asset.VideoCodec, task.Asset.AudioCodec, task.Asset.HasAudio
			if _, err := g.deps.Probe.Probe(ctx, content); err != nil {
				return nil, err
			}
			payload = []byte{1}
			child.SHA256 = task.Asset.SHA256
			child.SizeBytes = uint64(content.SizeBytes)
		} else {
			payload = fakeDerivedPNG()
			digest := sha256.Sum256(payload)
			child.SHA256 = hex.EncodeToString(digest[:])
			child.SizeBytes = uint64(len(payload))
		}
		child.MIMEType = mimeType
		if err := g.deps.Safety.AssessDerived(ctx, child, payload); err != nil {
			return nil, err
		}
		child.ModerationPassed, child.ModerationStatus = true, AssetModerationPassed
		childLabels, err := g.deps.Labeler.Apply(ctx, LabelRequest{TaskID: task.TaskID, AssetID: child.AssetID, SHA256: child.SHA256})
		if err != nil {
			return nil, err
		}
		child.ExplicitLabelStatus, child.ImplicitLabelStatus = childLabels.ExplicitStatus, childLabels.ImplicitStatus
		child.LabelVersion = childLabels.Version
		child.Object = StoredVideoObject{}
		children = append(children, child)
	}
	for index := range children {
		child := &children[index]
		var body io.Reader
		if child.MIMEType == "video/mp4" {
			body = io.NewSectionReader(content.ReaderAt, 0, content.SizeBytes)
		} else {
			body = bytes.NewReader(fakeDerivedPNG())
		}
		stored, err := g.deps.Store.Put(ctx, PutVideoObjectRequest{
			Zone: VideoObjectTemporary, TaskID: task.TaskID, AssetID: child.AssetID, Role: child.Role,
			Body: body, MaxBytes: g.deps.Probe.limits.MaxBytes,
		})
		if err != nil {
			g.cleanupDerivedObjects(ctx, task.TaskID, children)
			return nil, err
		}
		child.Object, child.SizeBytes, child.SHA256 = stored, stored.SizeBytes, stored.SHA256
	}
	for index := range children {
		promoted, err := g.ensureObjectZone(ctx, children[index].Object.Ref, VideoObjectResult)
		if err != nil {
			g.cleanupDerivedObjects(ctx, task.TaskID, children)
			return nil, err
		}
		children[index].Object, children[index].Lifecycle = promoted, AssetAvailable
	}
	return children, nil
}

func (g *VideoGateway) ensureObjectZone(ctx context.Context, ref VideoObjectRef, zone VideoObjectZone) (StoredVideoObject, error) {
	if ref.Bucket == bucketForVideoZone(zone) {
		return g.deps.Store.Head(ctx, ref)
	}
	var moved StoredVideoObject
	var err error
	if zone == VideoObjectQuarantine {
		moved, err = g.deps.Store.MoveToQuarantine(ctx, ref)
	} else {
		moved, err = g.deps.Store.PromoteToResult(ctx, ref)
	}
	if err == nil || !errors.Is(err, ErrVideoObjectNotFound) {
		return moved, err
	}
	return g.deps.Store.Head(ctx, VideoObjectRef{Bucket: bucketForVideoZone(zone), ObjectKey: ref.ObjectKey})
}

func (g *VideoGateway) cleanupDerivedObjects(ctx context.Context, taskID string, children []GatewayAsset) {
	for _, child := range children {
		for _, zone := range []VideoObjectZone{VideoObjectTemporary, VideoObjectResult, VideoObjectQuarantine} {
			_ = g.deps.Store.Delete(ctx, VideoObjectRef{Bucket: bucketForVideoZone(zone), ObjectKey: taskID + "/" + child.AssetID + "/" + child.Role + ".bin"})
		}
	}
}

func fakeDerivedPNG() []byte {
	canvas := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	canvas.SetNRGBA(0, 0, color.NRGBA{R: 32, G: 64, B: 96, A: 255})
	var output bytes.Buffer
	_ = png.Encode(&output, canvas)
	return output.Bytes()
}

func (g *VideoGateway) HandleCallback(ctx context.Context, taskID string, envelope CallbackEnvelope) (GatewayTask, error) {
	callback, err := g.deps.Verifier.Verify(ctx, envelope)
	if err != nil {
		return GatewayTask{}, err
	}
	task, err := g.Query(ctx, taskID)
	if err != nil {
		_, _ = g.deps.Ledger.RecordCallback(ctx, taskID, callback)
		return GatewayTask{}, err
	}
	if callback.ProviderTaskID != task.ProviderTaskID || callback.ProviderCode != task.ProviderCode {
		_, _ = g.deps.Ledger.RecordCallback(ctx, taskID, callback)
		return task, ErrCallbackTaskMismatch
	}
	duplicate, err := g.deps.Ledger.RecordCallback(ctx, taskID, callback)
	if err != nil {
		return GatewayTask{}, err
	}
	if taskSafeTerminal(task.Status) {
		return g.deps.Ledger.ReleaseLeaseOnce(ctx, task.TaskID)
	}
	if duplicate || task.Status == TaskPendingReconcile || taskStatusRank(task.Status) >= taskStatusRank(TaskFetching) {
		return task, nil
	}
	switch callback.Status {
	case ProviderTaskProcessing:
		if task.Status == TaskProcessing {
			return task, nil
		}
		return g.advance(ctx, task, TaskProcessing, "provider_callback", "callback_processing", nil)
	case ProviderTaskSucceeded:
		return g.advance(ctx, task, TaskFetching, "provider_callback", "callback_succeeded", func(next *GatewayTask) error {
			next.Content = controlledContentForProviderTask(callback.ProviderTaskID)
			return nil
		})
	case ProviderTaskFailed:
		return g.failTask(ctx, task, "callback_failed", ErrProviderExplicitFailure)
	case ProviderTaskCancelled:
		return g.cancelTerminal(ctx, task, "callback_cancelled")
	case ProviderTaskUnknown:
		return g.advance(ctx, task, TaskPendingReconcile, "provider_callback", "callback_unknown", nil)
	default:
		return task, ErrCallbackInvalid
	}
}

func (g *VideoGateway) Cancel(ctx context.Context, taskID string) (GatewayTask, error) {
	task, err := g.Query(ctx, taskID)
	if err != nil {
		return GatewayTask{}, err
	}
	if taskSafeTerminal(task.Status) || task.Status == TaskPendingReconcile {
		return task, nil
	}
	if task.ProviderTaskID != "" {
		result, cancelErr := g.deps.Provider.Cancel(ctx, CancelRequest{ProviderTaskID: task.ProviderTaskID})
		if cancelErr != nil {
			return task, cancelErr
		}
		if result.Status == ProviderTaskSucceeded {
			return g.advance(ctx, task, TaskFetching, "worker", "late_success", func(next *GatewayTask) error {
				next.Content = controlledContentForProviderTask(task.ProviderTaskID)
				return nil
			})
		}
	}
	return g.cancelTerminal(ctx, task, "cancelled")
}

func (g *VideoGateway) ReadContent(ctx context.Context, taskID string, offset, length int64) (io.ReadCloser, error) {
	task, err := g.Query(ctx, taskID)
	if err != nil || task.Status != TaskSucceeded || task.Asset == nil || task.Asset.Lifecycle != AssetAvailable || task.Asset.MediaDeleted {
		return nil, ErrVideoObjectNotFound
	}
	return g.deps.Store.GetRange(ctx, task.Asset.Object.Ref, offset, length)
}

func (g *VideoGateway) DeleteContent(ctx context.Context, taskID string) error {
	task, err := g.deps.Ledger.PrepareMediaDelete(ctx, taskID)
	if err != nil || task.Asset == nil {
		if err != nil {
			return err
		}
		return ErrVideoObjectNotFound
	}
	var deleteErr error
	for index := range task.Asset.Children {
		if err := g.deps.Store.Delete(ctx, task.Asset.Children[index].Object.Ref); err != nil && deleteErr == nil {
			deleteErr = err
		}
	}
	if err := g.deps.Store.Delete(ctx, task.Asset.Object.Ref); err != nil && deleteErr == nil {
		deleteErr = err
	}
	if _, err := g.deps.Ledger.CompleteMediaDelete(ctx, taskID, deleteErr == nil); err != nil {
		return err
	}
	return deleteErr
}

func (g *VideoGateway) advance(ctx context.Context, task GatewayTask, to TaskStatus, source, reason string, mutate TaskMutation) (GatewayTask, error) {
	for attempt := 0; attempt < 16; attempt++ {
		updated, err := g.deps.Ledger.Advance(ctx, task.TaskID, task.Version, to, source, reason, mutate)
		if err == nil {
			return updated, nil
		}
		if !errors.Is(err, ErrGatewayTaskConflict) {
			return task, err
		}
		task, err = g.Query(ctx, task.TaskID)
		if err != nil {
			return GatewayTask{}, err
		}
		if task.Status == to || taskStatusRank(task.Status) > taskStatusRank(to) {
			return task, nil
		}
	}
	return task, ErrGatewayTaskConflict
}

func (g *VideoGateway) failTask(ctx context.Context, task GatewayTask, reason string, cause error) (GatewayTask, error) {
	return g.failTaskWithMutation(ctx, task, reason, cause, nil)
}

func (g *VideoGateway) failTaskWithMutation(ctx context.Context, task GatewayTask, reason string, cause error, mutate TaskMutation) (GatewayTask, error) {
	if taskSafeTerminal(task.Status) || task.Status == TaskPendingReconcile {
		if taskSafeTerminal(task.Status) {
			updated, releaseErr := g.deps.Ledger.ReleaseLeaseOnce(ctx, task.TaskID)
			if releaseErr != nil {
				return updated, releaseErr
			}
			return updated, cause
		}
		return task, cause
	}
	updated, err := g.advance(ctx, task, TaskFailed, "worker", reason, func(next *GatewayTask) error {
		if next.Asset != nil {
			next.Asset.Lifecycle = AssetQuarantined
		}
		if mutate != nil {
			return mutate(next)
		}
		return nil
	})
	if err != nil {
		return updated, err
	}
	updated, err = g.deps.Ledger.ReleaseLeaseOnce(ctx, task.TaskID)
	if err != nil {
		return updated, err
	}
	if cause != nil {
		return updated, cause
	}
	return updated, err
}

func (g *VideoGateway) quarantineAndFail(ctx context.Context, task GatewayTask, ref VideoObjectRef, reason string, cause error, mutate func(*GatewayTask, StoredVideoObject)) (GatewayTask, error) {
	quarantined, err := g.ensureObjectZone(ctx, ref, VideoObjectQuarantine)
	if err != nil {
		updated, transitionErr := g.advance(ctx, task, TaskPendingReconcile, "worker", "quarantine_unknown", nil)
		if transitionErr != nil {
			return updated, transitionErr
		}
		return updated, err
	}
	return g.failTaskWithMutation(ctx, task, reason, cause, func(next *GatewayTask) error {
		if mutate != nil {
			mutate(next, quarantined)
		}
		return nil
	})
}

func (g *VideoGateway) cancelTerminal(ctx context.Context, task GatewayTask, reason string) (GatewayTask, error) {
	if taskSafeTerminal(task.Status) {
		return g.deps.Ledger.ReleaseLeaseOnce(ctx, task.TaskID)
	}
	updated, err := g.advance(ctx, task, TaskCancelled, "worker", reason, nil)
	if err != nil {
		if errors.Is(err, ErrGatewayTaskTransition) {
			return g.Query(ctx, task.TaskID)
		}
		return updated, err
	}
	return g.deps.Ledger.ReleaseLeaseOnce(ctx, task.TaskID)
}

func validateGatewayTask(task GatewayTask) error {
	if strings.TrimSpace(task.TaskID) == "" || strings.TrimSpace(task.RequestID) == "" || strings.TrimSpace(task.Prompt) == "" {
		return ErrVideoRequestInvalid
	}
	if err := validateSubmitRequest(SubmitRequest{RequestID: task.RequestID, Operation: task.Operation, Prompt: task.Prompt, Input: task.Input, Spec: task.Spec}); err != nil {
		return err
	}
	if task.Operation == OperationTextToVideo {
		if task.Reference != nil {
			return ErrVideoRequestInvalid
		}
		return nil
	}
	return ValidateNormalizedReference(*task.Input, task.Reference)
}

func controlledContentForProviderTask(providerTaskID string) *ControlledContentRef {
	return &ControlledContentRef{ProviderTaskID: providerTaskID, ContentID: "content-" + providerTaskID, MediaType: "video/mp4"}
}
