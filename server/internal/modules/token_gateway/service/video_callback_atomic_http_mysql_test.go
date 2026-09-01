package service_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/service"
)

type callbackLostCommitPool struct {
	gorm.ConnPool
	commits atomic.Int64
}

func (p *callbackLostCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &callbackLostCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type callbackLostCommitTx struct {
	gorm.ConnPool
	tx   *sql.Tx
	pool *callbackLostCommitPool
}

func (t *callbackLostCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.pool.commits.Add(1) == 1 {
		return errors.New("合成回调COMMIT确认丢失")
	}
	return nil
}
func (t *callbackLostCommitTx) Rollback() error { return t.tx.Rollback() }

// nonce落库在Callback及原G5对账之后，失败必须回滚全部；真实提交后确认丢失则重试仅恢复ACK。
func TestVideoG6CallbackAtomicHTTPMySQL(t *testing.T) {
	for _, mode := range []string{"nonce_write", "commit_unknown"} {
		t.Run(mode, func(t *testing.T) {
			f := service.NewVideoContentHTTPFixture(t)
			caller := service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
			job, err := f.App.Create(context.Background(), service.VideoCommand{Caller: caller, IdempotencyKey: "g6-callback-atomic-" + mode, Model: f.Model, Prompt: "仅用于回调事务隔离测试", Operation: model.AIVideoOperationTextToVideo})
			if err != nil {
				t.Fatal(err)
			}
			f.Submit(job.Job.ID)
			var task model.AIImageTask
			if err := f.DB.Where("public_id=?", job.Job.ID).Take(&task).Error; err != nil || task.ProviderTaskID == nil {
				t.Fatal("必须已有真实Provider绑定")
			}
			beforeSubmits := f.SubmitCalls()
			secret := make([]byte, 32)
			if _, err := rand.Read(secret); err != nil {
				t.Fatal(err)
			}
			app, err := service.NewVideoCallbackService(f.App, service.VideoCallbackOptions{FakeOnlyEnabled: true, SigningSecret: secret})
			if err != nil {
				t.Fatal(err)
			}
			var pool *callbackLostCommitPool
			var injected atomic.Bool
			const hook = "g6_callback_nonce_insert_failure"
			if mode == "nonce_write" {
				if err := f.DB.Callback().Create().After("gorm:create").Register(hook, func(tx *gorm.DB) {
					if tx.Error == nil && tx.Statement.Table == "ai_video_callback_nonces" && injected.CompareAndSwap(false, true) {
						tx.AddError(errors.New("合成nonce写入后回滚"))
					}
				}); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = f.DB.Callback().Create().Remove(hook) })
			} else {
				pool = &callbackLostCommitPool{ConnPool: f.DB.ConnPool}
				db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.DB.Logger})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(f.UseApplicationDB(db))
			}
			mux := http.NewServeMux()
			gateway.RegisterVideoInternalRoutes(mux, app, true)
			srv := httptest.NewServer(mux)
			defer srv.Close()
			transport := &http.Transport{Proxy: nil}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 40 * time.Second}
			path := "/api/internal/ai/provider-callbacks/fake-native-async"
			raw := []byte(fmt.Sprintf(`{"provider_task_id":%q,"external_event_id":"evt-atomic","video_id":%q,"status":"unknown","progress":0}`, *task.ProviderTaskID, task.PublicID))
			// nonce按Provider全局去重；不同独立夹具也不能复用，否则会正确409而到不了COMMIT故障点。
			nonceBytes := make([]byte, 32)
			if _, err := rand.Read(nonceBytes); err != nil {
				t.Fatal(err)
			}
			stamp, nonce := strconv.FormatInt(time.Now().Unix(), 10), hex.EncodeToString(nonceBytes)
			digest := sha256.Sum256(raw)
			canonical := fmt.Sprintf("molin-video-callback-v1\nPOST\n%s\n%s\n%s\n%x", path, stamp, nonce, digest)
			mac := hmac.New(sha256.New, secret)
			_, _ = mac.Write([]byte(canonical))
			signature := hex.EncodeToString(mac.Sum(nil))
			call := func(want int) *service.VideoCallbackACK {
				t.Helper()
				r, err := http.NewRequest("POST", srv.URL+path, bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("X-Molin-Callback-Timestamp", stamp)
				r.Header.Set("X-Molin-Callback-Nonce", nonce)
				r.Header.Set("X-Molin-Callback-Signature", signature)
				resp, err := client.Do(r)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				data, err := io.ReadAll(resp.Body)
				if err != nil || resp.StatusCode != want {
					t.Fatalf("回调事务响应应%d实际%d", want, resp.StatusCode)
				}
				if want != 200 {
					return nil
				}
				var ack service.VideoCallbackACK
				if json.Unmarshal(data, &ack) != nil {
					t.Fatal("ACK解码失败")
				}
				return &ack
			}
			snapshot := func() []byte {
				t.Helper()
				tables := map[string]any{"finance": json.RawMessage(f.FinancialSnapshot())}
				for _, name := range []string{"ai_gateway_tasks", "ai_gateway_task_events", "ai_gateway_provider_callback_events", "ai_video_callback_nonces", "ai_compensation_tasks", "ai_gateway_task_inputs", "ai_gateway_assets"} {
					q := f.DB.Table(name)
					switch name {
					case "ai_gateway_tasks":
						q = q.Where("id=?", task.ID)
					case "ai_video_callback_nonces":
						q = q.Where("callback_event_id IN (SELECT id FROM ai_gateway_provider_callback_events WHERE task_id=?)", task.ID).Order("provider_code,nonce_sha256")
					case "ai_compensation_tasks":
						q = q.Where("aggregate_id=? AND task_type='video_reconcile'", task.RequestID)
					default:
						q = q.Where("task_id=?", task.ID)
					}
					if name != "ai_video_callback_nonces" {
						q = q.Order("id")
					}
					var rows []map[string]any
					if err := q.Find(&rows).Error; err != nil {
						t.Fatal(err)
					}
					tables[name] = rows
				}
				result, err := json.Marshal(tables)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			before := snapshot()
			call(503)
			failed := snapshot()
			if mode == "nonce_write" {
				if !injected.Load() || !bytes.Equal(before, failed) {
					t.Fatal("nonce写入失败必须回滚Task/Event、Callback、补偿和财务")
				}
				if err := f.DB.Callback().Create().Remove(hook); err != nil {
					t.Fatal(err)
				}
			} else if pool.commits.Load() != 1 || bytes.Equal(before, failed) {
				t.Fatal("必须真实提交全部事实后丢失确认")
			}
			ack := call(200)
			if !ack.Accepted || !ack.Applied || ack.Replayed != (mode == "commit_unknown") {
				t.Fatal("重试必须区分原事务回滚和已提交确认丢失")
			}
			after := snapshot()
			if mode == "commit_unknown" && (!bytes.Equal(failed, after) || pool.commits.Load() != 2) {
				t.Fatal("提交未知重试不得再次写入对账或事件")
			}
			if f.SubmitCalls() != beforeSubmits {
				t.Fatal("回调恢复不得重新Submit")
			}
			var final model.AIImageTask
			if err := f.DB.First(&final, task.ID).Error; err != nil || final.Status != "pending_reconcile" {
				t.Fatal("原未知结果必须通过G5进入待对账")
			}
			for _, table := range []string{"ai_gateway_provider_callback_events", "ai_video_callback_nonces", "ai_compensation_tasks"} {
				q := f.DB.Table(table)
				switch table {
				case "ai_gateway_provider_callback_events":
					q = q.Where("task_id=?", task.ID)
				case "ai_video_callback_nonces":
					q = q.Where("callback_event_id IN (SELECT id FROM ai_gateway_provider_callback_events WHERE task_id=?)", task.ID)
				default:
					q = q.Where("aggregate_id=? AND task_type='video_reconcile'", task.RequestID)
				}
				var n int64
				if err := q.Count(&n).Error; err != nil || n != 1 {
					t.Fatalf("恢复后必须唯一：%s count=%d", table, n)
				}
			}
		})
	}
}
