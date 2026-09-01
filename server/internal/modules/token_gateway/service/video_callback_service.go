package service

import (
	"context"
	"database/sql"
	"errors"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	video "molin/server/internal/modules/token_gateway/video"
)

var ErrVideoCallbackConflict = errors.New("内部视频回调重放冲突")
var ErrVideoCallbackNotFound = errors.New("内部视频回调目标不存在")

type VideoCallbackOptions struct {
	FakeOnlyEnabled bool   `json:"-"`
	SigningSecret   []byte `json:"-"`
}

// ACK只表示事件已持久化处理；applied描述原事件是否曾推进状态，不表示财务或交付完成。
type VideoCallbackACK struct {
	Accepted bool `json:"accepted"`
	Applied  bool `json:"applied"`
	Replayed bool `json:"replayed"`
}

type videoCallbackNonce struct {
	ProviderCode    string    `gorm:"primaryKey" json:"-"`
	NonceSHA256     string    `gorm:"primaryKey" json:"-"`
	RequestSHA256   string    `json:"-"`
	CallbackEventID uint64    `json:"-"`
	SignedAt        time.Time `json:"-"`
	CreatedAt       time.Time `json:"-"`
}

func (videoCallbackNonce) TableName() string { return "ai_video_callback_nonces" }

type VideoCallbackService struct {
	app      *VideoHTTPService
	verifier *VideoCallbackVerifier
}

func NewVideoCallbackService(app *VideoHTTPService, options VideoCallbackOptions) (*VideoCallbackService, error) {
	if app == nil || app.db == nil || app.billing == nil || !options.FakeOnlyEnabled {
		return nil, ErrVideoCallbackUnavailable
	}
	verifier, err := NewVideoCallbackVerifier(options.SigningSecret)
	if err != nil {
		return nil, err
	}
	return &VideoCallbackService{app: app, verifier: verifier}, nil
}

// 回调、Task/Event及nonce同事务；只调用原账本桥接，不再调用Gateway二次推进或联系Provider。
func (s *VideoCallbackService) Receive(ctx context.Context, request VideoCallbackRequest) (*VideoCallbackACK, error) {
	if s == nil || s.app == nil || s.verifier == nil {
		return nil, ErrVideoCallbackUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	received := time.Now().UTC()
	verified, err := s.verifier.Verify(ctx, request, received)
	if err != nil {
		return nil, err
	}
	var ack *VideoCallbackACK
	err = retryVideoBillingTransaction(ctx, func() error {
		return s.app.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var identity model.AIImageTask
			if err := tx.Where("public_id=? AND provider_code=? AND provider_task_id=? AND capability=?", verified.VideoID, verified.Event.ProviderCode, verified.Event.ProviderTaskID, model.AIVideoCapability).Take(&identity).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrVideoCallbackNotFound
				}
				return err
			}
			owner := repository.VideoOwner{UserID: identity.UserID, ProjectID: identity.ProjectID, APIKeyID: identity.APIKeyID}
			task, err := repository.NewVideoTaskRepository(tx).LockForOwnerTx(tx, verified.VideoID, owner)
			if err != nil {
				return err
			}
			if task.ProviderCode == nil || task.ProviderTaskID == nil || *task.ProviderCode != verified.Event.ProviderCode || *task.ProviderTaskID != verified.Event.ProviderTaskID {
				return ErrVideoCallbackNotFound
			}
			var prior videoCallbackNonce
			loadErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("provider_code=? AND nonce_sha256=?", verified.Event.ProviderCode, verified.NonceSHA256).Take(&prior).Error
			found := loadErr == nil
			if loadErr != nil && !errors.Is(loadErr, gorm.ErrRecordNotFound) {
				return loadErr
			}
			if found && prior.RequestSHA256 != verified.RequestSHA256 {
				return ErrVideoCallbackConflict
			}
			ledger := s.app.NewTaskLedger(owner, nil).withDB(tx)
			ledger.now = func() time.Time { return received }
			replayed, err := ledger.RecordCallback(ctx, verified.VideoID, verified.Event)
			if err != nil {
				return err
			}
			var event model.AIGatewayProviderCallbackEvent
			if err := tx.Where("provider_code=? AND provider_task_id=? AND external_event_id=?", verified.Event.ProviderCode, verified.Event.ProviderTaskID, verified.Event.ExternalEventID).Take(&event).Error; err != nil {
				return err
			}
			if event.BodySHA256 != verified.Event.BodySHA256 || event.SignatureStatus != model.AIProviderCallbackSignatureValid || event.TaskID == nil || *event.TaskID != task.ID || event.UserID == nil || *event.UserID != owner.UserID || event.ProjectID == nil || *event.ProjectID != owner.ProjectID || event.ProcessedAt == nil || (event.ProcessStatus != model.AIProviderCallbackApplied && event.ProcessStatus != model.AIProviderCallbackIgnored) {
				return ErrVideoCallbackConflict
			}
			if found {
				if prior.CallbackEventID != event.ID {
					return ErrVideoCallbackConflict
				}
			} else {
				nonce := videoCallbackNonce{ProviderCode: verified.Event.ProviderCode, NonceSHA256: verified.NonceSHA256, RequestSHA256: verified.RequestSHA256, CallbackEventID: event.ID, SignedAt: verified.SignedAt, CreatedAt: received}
				if err := tx.Create(&nonce).Error; err != nil {
					var duplicate *drivermysql.MySQLError
					if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
						return err
					}
					if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("provider_code=? AND nonce_sha256=?", nonce.ProviderCode, nonce.NonceSHA256).Take(&prior).Error; err != nil {
						return err
					}
					if prior.RequestSHA256 != nonce.RequestSHA256 || prior.CallbackEventID != event.ID {
						return ErrVideoCallbackConflict
					}
				}
			}
			ack = &VideoCallbackACK{Accepted: true, Applied: event.ProcessStatus == model.AIProviderCallbackApplied, Replayed: replayed}
			return nil
		}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	})
	if errors.Is(err, repository.ErrVideoCallbackBodyConflict) || errors.Is(err, video.ErrCallbackBodyConflict) {
		err = ErrVideoCallbackConflict
	}
	if err != nil {
		return nil, err
	}
	return ack, nil
}
