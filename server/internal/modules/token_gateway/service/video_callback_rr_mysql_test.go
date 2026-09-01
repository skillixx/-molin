package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

// 共享G4/G5账本可能处于已有RR快照；历史ignored事件必须当前读，不能借重放补出新的processing。
func TestVideoG6CallbackHistoricalIgnoredRRMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	job, err := f.App.Create(context.Background(), VideoCommand{Caller: caller, IdempotencyKey: "g6-callback-history-rr", Model: f.Model, Prompt: "仅用于历史回调隔离验证", Operation: model.AIVideoOperationTextToVideo})
	if err != nil {
		t.Fatal(err)
	}
	f.Submit(job.Job.ID)
	var task model.AIImageTask
	if err := f.DB.Where("public_id=?", job.Job.ID).Take(&task).Error; err != nil || task.ProviderTaskID == nil || task.ProviderCode == nil || task.Status != "submitted" {
		t.Fatal("必须有真实提交绑定")
	}
	raw := []byte(fmt.Sprintf(`{"provider_task_id":%q,"external_event_id":"evt-history-ignored","video_id":%q,"status":"succeeded","progress":100}`, *task.ProviderTaskID, task.PublicID))
	request := VideoCallbackRequest{ProviderCode: *task.ProviderCode, Method: "POST", Path: videoCallbackPath, Timestamp: strconv.FormatInt(time.Now().Unix(), 10), Nonce: strings.Repeat("b", 64), Body: raw}
	request.Signature = signCallbackFixture(request)
	verifier, err := NewVideoCallbackVerifier(callbackVectorKey())
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifier.Verify(context.Background(), request, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	owner := repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: &f.ProjectID}
	before := f.FinancialSnapshot()
	var eventsBefore int64
	if err := f.DB.Table("ai_gateway_task_events").Where("task_id=?", task.ID).Count(&eventsBefore).Error; err != nil {
		t.Fatal(err)
	}
	err = f.DB.Transaction(func(tx *gorm.DB) error {
		var oldCount int64
		if err := tx.Model(&model.AIGatewayProviderCallbackEvent{}).Where("provider_code=? AND provider_task_id=? AND external_event_id=?", verified.Event.ProviderCode, verified.Event.ProviderTaskID, verified.Event.ExternalEventID).Count(&oldCount).Error; err != nil {
			return err
		}
		if oldCount != 0 {
			t.Fatal("RR快照必须先确认不存在该事件")
		}
		// 另一真实连接按原G3矩阵记录历史ignored，不修改任务或篡改读取结果。
		outcome, err := repository.NewVideoProviderCallbackEventRepository(f.DB).RecordAndApply(context.Background(), repository.VideoProviderCallbackCommand{ProviderCode: verified.Event.ProviderCode, ProviderTaskID: verified.Event.ProviderTaskID, ExternalEventID: verified.Event.ExternalEventID, BodySHA256: verified.Event.BodySHA256, ExpectedTaskPublicID: task.PublicID, ExpectedOwner: owner, SignatureStatus: model.AIProviderCallbackSignatureValid, ToStatus: model.AIImageTaskFetching, EventID: "g6_history_ignored", SafeResultJSON: json.RawMessage(`{"result":"ignored"}`), ReceivedAt: time.Now().UTC()})
		if err != nil {
			return err
		}
		if outcome.Applied || outcome.Replayed || outcome.Event.ProcessStatus != "ignored" {
			t.Fatal("必须真实提交原矩阵不允许的历史ignored回调")
		}
		replayed, err := f.App.NewTaskLedger(owner, nil).withDB(tx).RecordCallback(context.Background(), task.PublicID, verified.Event)
		if err != nil {
			return err
		}
		if !replayed {
			t.Fatal("历史事件只能返回重放")
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	var after model.AIImageTask
	if err := f.DB.First(&after, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	var eventsAfter int64
	if err := f.DB.Table("ai_gateway_task_events").Where("task_id=?", task.ID).Count(&eventsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if after.Status != task.Status || after.VersionNo != task.VersionNo || eventsAfter != eventsBefore || !bytes.Equal(before, f.FinancialSnapshot()) {
		t.Fatalf("旧RR不能借历史ignored重放改变状态：before=%s after=%s event_delta=%d", task.Status, after.Status, eventsAfter-eventsBefore)
	}
}
