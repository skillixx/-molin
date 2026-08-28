package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

var (
	ErrVideoReservationState = errors.New("视频预占状态不允许")
	ErrVideoQuotaExceeded    = errors.New("视频项目额度不足")
)

type videoQuotaGuard interface {
	CheckTx(tx *gorm.DB, userID, projectID uint64, amount decimal.Decimal, now time.Time) error
}

// VideoProjectQuotaGuard 对hard月预算执行事务内额度检查；soft只告警、disabled不限制。
type VideoProjectQuotaGuard struct{}

func (VideoProjectQuotaGuard) CheckTx(tx *gorm.DB, userID, projectID uint64, amount decimal.Decimal, now time.Time) error {
	var project model.AIProject
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", projectID, userID).First(&project).Error; err != nil {
		return err
	}
	if project.Status != "active" || amount.LessThanOrEqual(decimal.Zero) {
		return ErrVideoReservationState
	}
	if project.BudgetMode != "hard" {
		return nil
	}
	if project.MonthlyBudget == nil || project.MonthlyBudget.LessThanOrEqual(decimal.Zero) {
		return ErrVideoQuotaExceeded
	}
	monthStart, err := projectMonthStartUTC(project.Timezone, now)
	if err != nil {
		return err
	}
	var rows []model.AIRequest
	// Project行锁串行化同项目预算；随后用锁定读取得最新已提交金额，不能使用事务旧快照的普通SUM。
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id,quoted_amount,held_amount,settled_amount").Where("project_id=? AND created_at>=? AND billing_status IN ?", projectID, monthStart, []string{model.AIBillingHeld, model.AIBillingSettlementPending, model.AIBillingSettled}).Find(&rows).Error; err != nil {
		return err
	}
	used := decimal.Zero
	for index := range rows {
		switch {
		case rows[index].SettledAmount != nil:
			used = used.Add(*rows[index].SettledAmount)
		case rows[index].HeldAmount != nil:
			used = used.Add(*rows[index].HeldAmount)
		case rows[index].QuotedAmount != nil:
			used = used.Add(*rows[index].QuotedAmount)
		}
	}
	if used.Add(amount).GreaterThan(*project.MonthlyBudget) {
		return ErrVideoQuotaExceeded
	}
	return nil
}

func projectMonthStartUTC(timezone string, now time.Time) (time.Time, error) {
	location, err := time.LoadLocation(strings.TrimSpace(timezone))
	if err != nil {
		return time.Time{}, ErrVideoReservationState
	}
	localNow := now.In(location)
	return time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, location).UTC(), nil
}

// VideoReservationService 在真实事务内完成可信输入复核、额度检查、Quote消费、钱包Hold、请求和任务创建。
// 本服务只形成reserved事实，不写MQ、不调用Provider，也不实现VID-G3事件与执行生命周期。
type VideoReservationService struct {
	db           *gorm.DB
	holds        imageWalletHoldService
	quota        videoQuotaGuard
	quotes       *repository.VideoQuoteRepository
	quoteService *VideoQuoteService
	now          func() time.Time
}

func NewVideoReservationService(db *gorm.DB, holds imageWalletHoldService, quoteService *VideoQuoteService) (*VideoReservationService, error) {
	if db == nil || holds == nil || quoteService == nil || quoteService.store == nil || len(quoteService.fingerprint) < 32 {
		return nil, ErrVideoReservationState
	}
	return &VideoReservationService{db: db, holds: holds, quota: VideoProjectQuotaGuard{}, quotes: repository.NewVideoQuoteRepository(db), quoteService: quoteService, now: time.Now}, nil
}

// ReserveAndCreate 让唯一键决定并发赢家，并对MySQL死锁做有界重试；任一步失败都会回滚全部事实。
func (s *VideoReservationService) ReserveAndCreate(ctx context.Context, command VideoReservationCommand) (*VideoPreparedGeneration, error) {
	if s == nil || strings.TrimSpace(command.QuotePublicID) == "" || (command.QuoteCommandKind != VideoQuoteCommandKindExplicit && command.QuoteCommandKind != VideoQuoteCommandKindCreate) || strings.TrimSpace(command.IdempotencyKey) == "" || strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.TaskID) == "" {
		return nil, ErrVideoReservationState
	}
	rawFingerprint, err := BuildVideoQuoteFingerprint(s.quoteService.fingerprint, command.FingerprintInput)
	if err != nil {
		return nil, err
	}
	if existing, found, lookupErr := s.findExistingReservation(ctx, command, command.FingerprintInput, rawFingerprint); lookupErr != nil {
		return nil, lookupErr
	} else if found {
		return existing, nil
	}
	trusted, err := s.quoteService.resolveTrustedFingerprintInput(ctx, command.FingerprintInput)
	if err != nil {
		// 响应恢复优先返回已经提交的幂等事实；输入后续过期或删除不能抹掉成功结果。
		if existing, found, lookupErr := s.findExistingReservation(ctx, command, command.FingerprintInput, rawFingerprint); lookupErr == nil && found {
			return existing, nil
		}
		return nil, err
	}
	fingerprint, err := BuildVideoQuoteFingerprint(s.quoteService.fingerprint, trusted)
	if err != nil {
		return nil, err
	}
	now := s.now().UTC()
	for attempt := 0; attempt < 7; attempt++ {
		var prepared *VideoPreparedGeneration
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var txErr error
			prepared, txErr = s.reserveAndCreateTx(tx, command, trusted, fingerprint, now)
			return txErr
		})
		if err == nil || !isRetryableVideoReservation(err) || attempt == 6 {
			return prepared, err
		}
		timer := time.NewTimer(time.Duration(10*(1<<attempt)) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, ErrVideoReservationState
}

func (s *VideoReservationService) findExistingReservation(ctx context.Context, command VideoReservationCommand, input VideoQuoteFingerprintInput, fingerprint string) (*VideoPreparedGeneration, bool, error) {
	var prepared *VideoPreparedGeneration
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var request model.AIRequest
		err := tx.Where("user_id=? AND idempotency_key=?", input.UserID, strings.TrimSpace(command.IdempotencyKey)).First(&request).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		var loadErr error
		prepared, loadErr = loadExistingVideoReservationTx(tx, command, input, fingerprint)
		return loadErr
	})
	return prepared, prepared != nil, err
}

func (s *VideoReservationService) reserveAndCreateTx(tx *gorm.DB, command VideoReservationCommand, trusted VideoQuoteFingerprintInput, fingerprint string, now time.Time) (*VideoPreparedGeneration, error) {
	quote, err := loadVideoQuoteForReservationTx(tx, command, trusted)
	if err != nil {
		return nil, err
	}
	if err := validateVideoQuoteSnapshot(quote); err != nil {
		return nil, err
	}
	if err := revalidateVideoInputTx(tx, trusted, now); err != nil {
		return nil, err
	}
	// 普通一致性读不会对空范围加gap lock；并发输家在唯一键冲突后开启新事务即可看到赢家事实。
	var existing model.AIRequest
	existingErr := tx.Where("user_id=? AND idempotency_key=?", trusted.UserID, strings.TrimSpace(command.IdempotencyKey)).First(&existing).Error
	if existingErr == nil {
		return loadExistingVideoReservationTx(tx, command, trusted, fingerprint)
	}
	if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
		return nil, existingErr
	}
	projectID, operation := trusted.ProjectID, trusted.Variant.Operation
	idempotencyKey, requestFingerprint := strings.TrimSpace(command.IdempotencyKey), fingerprint
	request := model.AIRequest{RequestID: strings.TrimSpace(command.RequestID), IdempotencyKey: &idempotencyKey, RequestFingerprint: &requestFingerprint, UserID: trusted.UserID, ProjectID: &projectID, APIKeyID: optionalUint64(trusted.APIKeyID), LogicalModelCode: trusted.LogicalModelCode, Modality: "video", Capability: model.AIVideoCapability, Operation: &operation, ModerationStatus: model.AIModerationPending, ExecutionStatus: model.AIExecutionPending, BillingStatus: model.AIBillingUnquoted, DeliveryStatus: model.AIDeliveryPending}
	if createErr := tx.Create(&request).Error; createErr != nil {
		return nil, createErr
	}
	if err := s.quota.CheckTx(tx, trusted.UserID, projectID, quote.QuotedAmount, now); err != nil {
		return nil, err
	}
	inputJSON, err := json.Marshal(map[string]interface{}{"operation": operation, "resolution": trusted.Variant.Resolution, "duration_seconds": trusted.Variant.DurationSeconds, "aspect_ratio": trusted.Variant.AspectRatio, "frame_rate": trusted.Variant.FrameRate, "audio": trusted.Variant.Audio})
	if err != nil {
		return nil, err
	}
	task := model.AIImageTask{PublicID: strings.TrimSpace(command.TaskID), RequestID: request.RequestID, QuoteID: quote.ID, UserID: trusted.UserID, ProjectID: projectID, APIKeyID: optionalUint64(trusted.APIKeyID), LogicalModelCode: trusted.LogicalModelCode, Capability: model.AIVideoCapability, Operation: &operation, Status: model.AIImageTaskCreated, InputJSON: inputJSON}
	if err := tx.Create(&task).Error; err != nil {
		return nil, err
	}
	if operation == model.AIVideoOperationImageToVideo {
		if trusted.Input == nil || trusted.Input.InternalID == 0 {
			return nil, ErrVideoInputMismatch
		}
		binding := model.AIGatewayTaskInput{TaskID: task.ID, InputAssetID: trusted.Input.InternalID, UserID: trusted.UserID, ProjectID: projectID, Role: model.AITaskInputReferenceImage, Ordinal: 0, NormalizedSHA256: trusted.Input.NormalizedSHA256, InputVersion: trusted.Input.Version}
		if err := tx.Create(&binding).Error; err != nil {
			return nil, err
		}
	}
	consumed, _, err := s.quotes.ConsumeTx(tx, quote.PublicID, trusted.UserID, projectID, optionalUint64(trusted.APIKeyID), operation, fingerprint, request.RequestID, now)
	if err != nil {
		return nil, err
	}
	// G3三轴状态机要求计费轴完整经过quoted；即使quoted与held处于同一原子预占事务，也不得跳过中间事实。
	if result := tx.Model(&model.AIRequest{}).Where("id=? AND billing_status=?", request.ID, model.AIBillingUnquoted).Updates(map[string]interface{}{
		"price_snapshot_json": consumed.PriceSnapshotJSON, "quoted_amount": consumed.QuotedAmount,
		"billing_status": model.AIBillingQuoted, "version_no": gorm.Expr("version_no+1"), "updated_at": now,
	}); result.Error != nil || result.RowsAffected != 1 {
		return nil, ErrVideoReservationState
	}
	quotedFrom, quotedTo := model.AIBillingUnquoted, model.AIBillingQuoted
	if err := tx.Create(&model.AIGatewayTaskEvent{
		EventID: task.PublicID + ":billing:quoted", TaskID: task.ID, UserID: trusted.UserID, ProjectID: projectID,
		EventType: "billing_status_changed", FromStatus: &quotedFrom, ToStatus: &quotedTo, Source: "api", CreatedAt: now,
	}).Error; err != nil {
		return nil, err
	}
	hold, err := s.holds.CreateHoldTx(tx, trusted.UserID, consumed.QuotedAmount, request.RequestID+":reserve", "视频生成预占")
	if err != nil {
		return nil, err
	}
	link := model.AIRequestWalletLink{RequestID: request.RequestID, WalletID: hold.WalletID, WalletHoldID: hold.HoldID, HoldTransactionID: hold.FreezeTransaction, QuotedAmount: consumed.QuotedAmount, HeldAmount: consumed.QuotedAmount}
	if err := tx.Create(&link).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&model.AIGatewayQuote{}).Where("id=?", consumed.ID).Update("held_amount", consumed.QuotedAmount).Error; err != nil {
		return nil, err
	}
	if result := tx.Model(&model.AIRequest{}).Where("id=? AND billing_status=?", request.ID, model.AIBillingQuoted).Updates(map[string]interface{}{
		"held_amount": consumed.QuotedAmount, "billing_status": model.AIBillingHeld,
		"version_no": gorm.Expr("version_no+1"), "updated_at": now,
	}); result.Error != nil || result.RowsAffected != 1 {
		return nil, ErrVideoReservationState
	}
	heldFrom, heldTo := model.AIBillingQuoted, model.AIBillingHeld
	if err := tx.Create(&model.AIGatewayTaskEvent{
		EventID: task.PublicID + ":billing:held", TaskID: task.ID, UserID: trusted.UserID, ProjectID: projectID,
		EventType: "billing_status_changed", FromStatus: &heldFrom, ToStatus: &heldTo, Source: "api", CreatedAt: now,
	}).Error; err != nil {
		return nil, err
	}
	if result := tx.Model(&model.AIImageTask{}).Where("id=? AND status=?", task.ID, model.AIImageTaskCreated).Updates(map[string]interface{}{"status": model.AIImageTaskReserved, "version_no": gorm.Expr("version_no+1")}); result.Error != nil || result.RowsAffected != 1 {
		return nil, ErrVideoReservationState
	}
	executionFrom, executionTo := model.AIImageTaskCreated, model.AIImageTaskReserved
	if err := tx.Create(&model.AIGatewayTaskEvent{
		EventID: task.PublicID + ":execution:reserved", TaskID: task.ID, UserID: trusted.UserID, ProjectID: projectID,
		EventType: "execution_status_changed", FromStatus: &executionFrom, ToStatus: &executionTo, Source: "api", CreatedAt: now,
	}).Error; err != nil {
		return nil, err
	}
	copyQuote := *consumed
	copyQuote.HeldAmount = &consumed.QuotedAmount
	return &VideoPreparedGeneration{Quote: &copyQuote, RequestID: request.RequestID, TaskID: task.PublicID, HeldAmount: consumed.QuotedAmount}, nil
}

func loadVideoQuoteForReservationTx(tx *gorm.DB, command VideoReservationCommand, input VideoQuoteFingerprintInput) (*model.AIGatewayQuote, error) {
	query := tx.Where("public_id=? AND user_id=? AND project_id=? AND capability=? AND operation=? AND command_kind=?", strings.TrimSpace(command.QuotePublicID), input.UserID, input.ProjectID, model.AIVideoCapability, input.Variant.Operation, command.QuoteCommandKind)
	if input.APIKeyID == 0 {
		query = query.Where("api_key_id IS NULL")
	} else {
		query = query.Where("api_key_id=?", input.APIKeyID)
	}
	var quote model.AIGatewayQuote
	if err := query.First(&quote).Error; err != nil {
		return nil, err
	}
	return &quote, nil
}

func validateVideoQuoteSnapshot(quote *model.AIGatewayQuote) error {
	if quote == nil || quote.Operation == nil {
		return ErrVideoReservationState
	}
	snapshot, err := DecodeVideoPriceSnapshot(quote.PriceSnapshotJSON)
	if err != nil || snapshot.PriceVersionID != quote.PriceVersionID || snapshot.Operation != *quote.Operation || len(snapshot.SelectedLines) != 1 || snapshot.SelectedLines[0].VariantHash != quote.RequestVariantHash {
		return ErrVideoPriceUnavailable
	}
	quoted, err := decimal.NewFromString(snapshot.QuotedAmount)
	if err != nil || !quoted.Equal(quote.QuotedAmount) {
		return ErrVideoPriceUnavailable
	}
	return nil
}

// revalidateVideoInputTx 在预占事务内锁定ready输入，关闭Quote复核与TaskInput写入之间的状态竞态。
func revalidateVideoInputTx(tx *gorm.DB, input VideoQuoteFingerprintInput, now time.Time) error {
	if input.Variant.Operation == model.AIVideoOperationTextToVideo {
		return nil
	}
	if input.Input == nil || input.Input.InternalID == 0 {
		return ErrVideoInputMismatch
	}
	var asset model.AIGatewayInputAsset
	query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Table("ai_gateway_input_assets AS inputs").Select("inputs.*").
		Where("inputs.id=? AND inputs.public_id=? AND inputs.user_id=? AND inputs.project_id=? AND inputs.lifecycle_state=? AND inputs.moderation_status=? AND inputs.expires_at>? AND inputs.delete_requested_at IS NULL AND inputs.pending_delete_at IS NULL AND inputs.deleted_at IS NULL",
			input.Input.InternalID, input.Input.InputAssetID, input.UserID, input.ProjectID, model.AIInputAssetReady, model.AIModerationPassed, now)
	query = scopeTrustedVideoInputSource(query, input.APIKeyID, now)
	if err := query.First(&asset).Error; err != nil {
		return ErrVideoInputMismatch
	}
	if asset.NormalizedSHA256 == nil || *asset.NormalizedSHA256 != input.Input.NormalizedSHA256 || asset.VersionNo != input.Input.Version {
		return ErrVideoInputMismatch
	}
	return nil
}

func loadExistingVideoReservationTx(tx *gorm.DB, command VideoReservationCommand, input VideoQuoteFingerprintInput, fingerprint string) (*VideoPreparedGeneration, error) {
	var existing model.AIRequest
	if err := tx.Where("user_id=? AND idempotency_key=?", input.UserID, strings.TrimSpace(command.IdempotencyKey)).First(&existing).Error; err != nil {
		return nil, err
	}
	if existing.ProjectID == nil || *existing.ProjectID != input.ProjectID || existing.RequestFingerprint == nil || subtle.ConstantTimeCompare([]byte(*existing.RequestFingerprint), []byte(fingerprint)) != 1 || !equalOptionalUint64(existing.APIKeyID, optionalUint64(input.APIKeyID)) || existing.Modality != "video" || existing.Capability != model.AIVideoCapability || existing.Operation == nil || *existing.Operation != input.Variant.Operation {
		return nil, ErrVideoQuoteConflict
	}
	var task model.AIImageTask
	if err := tx.Where("request_id=? AND capability=? AND operation=?", existing.RequestID, model.AIVideoCapability, input.Variant.Operation).First(&task).Error; err != nil {
		return nil, err
	}
	var quote model.AIGatewayQuote
	if err := tx.First(&quote, task.QuoteID).Error; err != nil {
		return nil, err
	}
	if quote.PublicID != strings.TrimSpace(command.QuotePublicID) || quote.CommandKind == nil || *quote.CommandKind != command.QuoteCommandKind {
		return nil, ErrVideoQuoteConflict
	}
	var link model.AIRequestWalletLink
	if err := tx.Where("request_id=?", existing.RequestID).First(&link).Error; err != nil {
		return nil, err
	}
	return &VideoPreparedGeneration{Quote: &quote, RequestID: existing.RequestID, TaskID: task.PublicID, HeldAmount: link.HeldAmount, Existing: true}, nil
}

func isRetryableVideoReservation(err error) bool {
	var mysqlErr *drivermysql.MySQLError
	return errors.As(err, &mysqlErr) && (mysqlErr.Number == 1062 || mysqlErr.Number == 1213 || mysqlErr.Number == 1205)
}
