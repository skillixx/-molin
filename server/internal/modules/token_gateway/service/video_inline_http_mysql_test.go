package service_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/dto"
	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
	"molin/server/internal/modules/token_gateway/service"
	"molin/server/pkg/crypto"
)

type videoInlineLostCommitPool struct {
	gorm.ConnPool
	armed atomic.Bool
	lost  atomic.Bool
}

func (p *videoInlineLostCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &videoInlineLostCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type videoInlineLostCommitTx struct {
	gorm.ConnPool
	tx              *sql.Tx
	pool            *videoInlineLostCommitPool
	generationWrite bool
}

func (t *videoInlineLostCommitTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	lower := strings.ToLower(query)
	if strings.Contains(lower, "`ai_requests`") && (strings.Contains(lower, "insert") || strings.Contains(lower, "update")) {
		t.generationWrite = true
	}
	return t.tx.ExecContext(ctx, query, args...)
}

func (t *videoInlineLostCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.generationWrite && t.pool.armed.Load() && t.pool.lost.CompareAndSwap(false, true) {
		return errors.New("合成inline生成COMMIT确认丢失")
	}
	return nil
}
func (t *videoInlineLostCommitTx) Rollback() error { return t.tx.Rollback() }

// TestVideoG6OpenAIInlineTCPReadLimitAndDisconnectMySQL 使用真实TCP阻塞请求验证内存准入和读取中断零事实。
func TestVideoG6OpenAIInlineTCPReadLimitAndDisconnectMySQL(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	store := &videoG6InlineStore{entries: map[string]*videoG6InlineEntry{}}
	app := f.WithUploads(store)
	if _, err := app.AcceptProjectRights(t.Context(), service.VideoRightsAcceptCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID}, PolicyVersion: f.Policy, Confirmed: true, IdempotencyKey: "g6-inline-tcp-rights-0001", RequestID: "g6-inline-tcp-rights-request"}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, app, f.Keys, true, f.JWT)
	entered := make(chan struct{}, 8)
	finished := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		defer func() { finished <- struct{}{} }()
		mux.ServeHTTP(w, r)
	}))
	defer srv.Close()
	address := srv.Listener.Addr().String()
	const blocked = 6
	connections := make([]net.Conn, 0, blocked)
	writeHeader := func(conn net.Conn, key string) {
		t.Helper()
		_, err := fmt.Fprintf(conn, "POST /v1/videos HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\nIdempotency-Key: %s\r\nContent-Type: multipart/form-data; boundary=g6-boundary\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", address, f.Key, key, 10<<20)
		if err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < blocked; index++ {
		conn, err := net.Dial("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
		writeHeader(conn, fmt.Sprintf("g6-inline-tcp-block-%04d", index))
	}
	for index := 0; index < blocked; index++ {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("阻塞上传未进入真实HTTP Handler")
		}
	}
	// 让前六个请求完成鉴权并在multipart读取处阻塞；第七个不得读取正文。
	time.Sleep(300 * time.Millisecond)
	seventh, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer seventh.Close()
	writeHeader(seventh, "g6-inline-tcp-seventh-0001")
	_ = seventh.SetReadDeadline(time.Now().Add(10 * time.Second))
	response, err := http.ReadResponse(bufio.NewReader(seventh), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatalf("第七个请求必须在读取正文前得到429：%v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("第七个近10MiB请求必须429，实际%d", response.StatusCode)
	}
	for _, conn := range connections {
		_ = conn.Close()
	}
	for index := 0; index < blocked; index++ {
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Fatal("断开multipart后Handler未退出")
		}
	}
	for table, where := range map[string]string{
		"ai_upload_sessions":      "user_id=? AND source_type='openai_inline_multipart'",
		"ai_gateway_input_assets": "user_id=? AND source_type='openai_inline_multipart'",
		"ai_gateway_tasks":        "user_id=? AND capability='video.generate'",
	} {
		var count int64
		if err := f.DB.Table(table).Where(where, f.ProjectID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("读取中断不得留下事实：table=%s count=%d err=%v", table, count, err)
		}
	}
}

type videoG6InlineEntry struct {
	target                  service.VideoUploadTarget
	raw, frozen, normalized []byte
	mime                    string
	sealed, discarded       bool
}

// videoG6InlineStore只替换对象存储边界；服务端Target、MySQL状态、规范化、审核和财务均走生产实现。
type videoG6InlineStore struct {
	sync.Mutex
	entries            map[string]*videoG6InlineEntry
	writes             int
	failAfterWriteOnce bool
	failSealOnce       bool
	failAfterSealOnce  bool
	afterNormalized    func()
}

func (s *videoG6InlineStore) Issue(context.Context, service.VideoUploadTarget) (*service.VideoUploadGrant, error) {
	return nil, errors.New("inline路径不得申请预签上传能力")
}
func (s *videoG6InlineStore) PutOriginal(ctx context.Context, target service.VideoUploadTarget, body io.Reader, size uint64, sha string) error {
	if err := ctx.Err(); err != nil || target.SourceType != "openai_inline_multipart" || target.SizeBytes != size {
		return service.ErrVideoUploadConflict
	}
	raw, err := io.ReadAll(io.LimitReader(body, int64(size)+1))
	if err != nil || uint64(len(raw)) != size || crypto.SHA256Hex(string(raw)) != sha {
		return service.ErrVideoUploadInvalid
	}
	s.Lock()
	defer s.Unlock()
	e := s.entries[target.SessionID]
	if e == nil {
		e = &videoG6InlineEntry{target: target}
		s.entries[target.SessionID] = e
	}
	if e.discarded || (e.sealed && !bytes.Equal(e.frozen, raw)) || (len(e.raw) != 0 && !bytes.Equal(e.raw, raw)) {
		return service.ErrVideoUploadConflict
	}
	if len(e.raw) == 0 && !e.sealed {
		e.raw, e.mime = append([]byte(nil), raw...), target.MIMEType
		s.writes++
	}
	if s.failAfterWriteOnce {
		s.failAfterWriteOnce = false
		return service.ErrVideoUploadUnavailable
	}
	return nil
}
func (s *videoG6InlineStore) Seal(_ context.Context, target service.VideoUploadTarget, max int64) (*service.VideoSealedUpload, error) {
	s.Lock()
	defer s.Unlock()
	e := s.entries[target.SessionID]
	if e == nil || e.discarded || len(e.raw) == 0 || int64(len(e.raw)) > max {
		return nil, service.ErrVideoUploadUnavailable
	}
	if s.failSealOnce {
		s.failSealOnce = false
		return nil, service.ErrVideoUploadUnavailable
	}
	if !e.sealed {
		e.frozen, e.sealed = append([]byte(nil), e.raw...), true
	}
	if s.failAfterSealOnce {
		s.failAfterSealOnce = false
		return nil, service.ErrVideoUploadUnavailable
	}
	return &service.VideoSealedUpload{Bytes: append([]byte(nil), e.frozen...), MIMEType: e.mime, ETag: crypto.SHA256Hex(string(e.frozen)), VersionID: "inline-fixture-v1"}, nil
}

func TestVideoG6OpenAIInlineFailureRecoveryMySQL(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(*videoG6InlineStore)
	}{
		{name: "原件写入结果未知", setup: func(store *videoG6InlineStore) { store.failAfterWriteOnce = true }},
		{name: "Seal临时失败后接管", setup: func(store *videoG6InlineStore) { store.failSealOnce = true }},
		{name: "Seal成功回包丢失", setup: func(store *videoG6InlineStore) { store.failAfterSealOnce = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := service.NewVideoImportHTTPFixture(t)
			store := &videoG6InlineStore{entries: map[string]*videoG6InlineEntry{}}
			test.setup(store)
			app := f.WithUploads(store)
			if _, err := app.AcceptProjectRights(t.Context(), service.VideoRightsAcceptCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID}, PolicyVersion: f.Policy, Confirmed: true, IdempotencyKey: "g6-inline-recovery-rights-0001", RequestID: "g6-inline-recovery-rights-request"}); err != nil {
				t.Fatal(err)
			}
			command := service.VideoCommand{Caller: service.VideoCaller{UserID: f.ProjectID, APIKeyID: f.ProjectID}, IdempotencyKey: "g6-inline-recovery-create-0001", Model: f.Model, Prompt: "合成inline恢复", Operation: "image_to_video", Facade: "openai"}
			input := service.OpenAIVideoInlineInput{Filename: "reference.png", ContentType: "image/png", Body: f.Reference}
			if result, err := app.CreateOpenAIInlineVideo(t.Context(), command, input); err == nil || result != nil {
				t.Fatal("首次外部未知/临时失败必须返回503语义且不虚构任务")
			}
			var sessions, inputs, tasks int64
			_ = f.DB.Table("ai_upload_sessions").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&sessions).Error
			_ = f.DB.Table("ai_gateway_input_assets").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&inputs).Error
			_ = f.DB.Table("ai_gateway_tasks").Where("user_id=? AND capability='video.generate'", f.ProjectID).Count(&tasks).Error
			if sessions != 1 || inputs != 0 || tasks != 0 {
				t.Fatalf("首次失败事实不安全: sessions=%d inputs=%d tasks=%d", sessions, inputs, tasks)
			}
			result, err := app.CreateOpenAIInlineVideo(t.Context(), command, input)
			if err != nil || result == nil || result.Job.ID == "" {
				t.Fatalf("原键未恢复同一inline会话: result=%+v err=%v", result, err)
			}
			_ = f.DB.Table("ai_upload_sessions").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&sessions).Error
			_ = f.DB.Table("ai_gateway_input_assets").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&inputs).Error
			_ = f.DB.Table("ai_gateway_tasks").Where("user_id=? AND capability='video.generate'", f.ProjectID).Count(&tasks).Error
			store.Lock()
			writes := store.writes
			store.Unlock()
			if sessions != 1 || inputs != 1 || tasks != 1 || writes != 1 {
				t.Fatalf("恢复产生重复事实: sessions=%d inputs=%d tasks=%d writes=%d", sessions, inputs, tasks, writes)
			}
		})
	}
}

func TestVideoG6OpenAIInlineDisconnectAfterCompleteMySQL(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	store := &videoG6InlineStore{entries: map[string]*videoG6InlineEntry{}}
	app := f.WithUploads(store)
	if _, err := app.AcceptProjectRights(t.Context(), service.VideoRightsAcceptCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID}, PolicyVersion: f.Policy, Confirmed: true, IdempotencyKey: "g6-inline-disconnect-rights-0001", RequestID: "g6-inline-disconnect-rights-request"}); err != nil {
		t.Fatal(err)
	}
	command := service.VideoCommand{Caller: service.VideoCaller{UserID: f.ProjectID, APIKeyID: f.ProjectID}, IdempotencyKey: "g6-inline-disconnect-create-0001", Model: f.Model, Prompt: "合成Complete后断连恢复", Operation: model.AIVideoOperationImageToVideo, Facade: "openai"}
	input := service.OpenAIVideoInlineInput{Filename: "reference.png", ContentType: "image/png", Body: f.Reference}
	var holdsBefore int64
	if err := f.DB.Table("wallet_holds").Where("user_id=?", f.ProjectID).Count(&holdsBefore).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	restore := service.UseVideoHTTPBillingFault(app, func(step string) error {
		if step == "request" {
			cancel()
			return context.Canceled
		}
		return nil
	})
	result, err := app.CreateOpenAIInlineVideo(ctx, command, input)
	restore()
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Complete后断连不能虚构生成成功：result=%+v err=%v", result, err)
	}
	var sessions, inputs, tasks, holds, deletions int64
	for table, check := range map[string]struct {
		where  string
		target *int64
	}{
		"ai_upload_sessions":               {"user_id=? AND source_type='openai_inline_multipart'", &sessions},
		"ai_gateway_input_assets":          {"user_id=? AND source_type='openai_inline_multipart'", &inputs},
		"ai_gateway_tasks":                 {"user_id=? AND capability='video.generate'", &tasks},
		"wallet_holds":                     {"user_id=?", &holds},
		"ai_video_input_deletion_requests": {"user_id=?", &deletions},
	} {
		if err := f.DB.Table(table).Where(check.where, f.ProjectID).Count(check.target).Error; err != nil {
			t.Fatal(err)
		}
	}
	if sessions != 1 || inputs != 1 || tasks != 0 || holds != holdsBefore || deletions != 0 {
		t.Fatalf("断连必须保留可恢复输入且无生成/删除事实：sessions=%d inputs=%d tasks=%d holds=%d deletions=%d", sessions, inputs, tasks, holds, deletions)
	}
	result, err = app.CreateOpenAIInlineVideo(t.Context(), command, input)
	if err != nil || result == nil || result.Job.ID == "" {
		t.Fatalf("原键必须复用完成输入恢复生成：result=%+v err=%v", result, err)
	}
	_ = f.DB.Table("ai_upload_sessions").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&sessions).Error
	_ = f.DB.Table("ai_gateway_input_assets").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&inputs).Error
	_ = f.DB.Table("ai_gateway_tasks").Where("user_id=? AND capability='video.generate'", f.ProjectID).Count(&tasks).Error
	_ = f.DB.Table("wallet_holds").Where("user_id=?", f.ProjectID).Count(&holds).Error
	store.Lock()
	writes := store.writes
	store.Unlock()
	if sessions != 1 || inputs != 1 || tasks != 1 || holds != holdsBefore+1 || writes != 1 {
		t.Fatalf("恢复不得复制会话/输入/对象或财务事实：sessions=%d inputs=%d tasks=%d holds=%d writes=%d", sessions, inputs, tasks, holds, writes)
	}
}

func TestVideoG6OpenAIInlineGenerationCommitUnknownMySQL(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	pool := &videoInlineLostCommitPool{ConnPool: f.DB.ConnPool}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	store := &videoG6InlineStore{entries: map[string]*videoG6InlineEntry{}}
	app := f.WithUploadsOnDB(db, store)
	if _, err := app.AcceptProjectRights(t.Context(), service.VideoRightsAcceptCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID}, PolicyVersion: f.Policy, Confirmed: true, IdempotencyKey: "g6-inline-commit-rights-0001", RequestID: "g6-inline-commit-rights-request"}); err != nil {
		t.Fatal(err)
	}
	command := service.VideoCommand{Caller: service.VideoCaller{UserID: f.ProjectID, APIKeyID: f.ProjectID}, IdempotencyKey: "g6-inline-commit-create-0001", Model: f.Model, Prompt: "合成inline提交确认未知", Operation: model.AIVideoOperationImageToVideo, Facade: "openai"}
	input := service.OpenAIVideoInlineInput{Filename: "reference.png", ContentType: "image/png", Body: f.Reference}
	ctx, cancel := context.WithCancel(t.Context())
	restore := service.UseVideoHTTPBillingFault(app, func(step string) error {
		if step == "request" {
			cancel()
			return context.Canceled
		}
		return nil
	})
	if result, err := app.CreateOpenAIInlineVideo(ctx, command, input); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("预备断连必须只留下completed输入：result=%+v err=%v", result, err)
	}
	restore()
	var holdsBefore int64
	if err := f.DB.Table("wallet_holds").Where("user_id=?", f.ProjectID).Count(&holdsBefore).Error; err != nil {
		t.Fatal(err)
	}
	pool.armed.Store(true)
	result, commitErr := app.CreateOpenAIInlineVideo(t.Context(), command, input)
	if result != nil || commitErr == nil || !pool.lost.Load() {
		t.Fatalf("必须真实提交生成事实后丢失确认：result=%+v err=%v lost=%t", result, commitErr, pool.lost.Load())
	}
	type counts struct{ sessions, inputs, requests, tasks, holds, deletions int64 }
	read := func() counts {
		var got counts
		for table, check := range map[string]struct {
			where  string
			target *int64
		}{
			"ai_upload_sessions":               {"user_id=? AND source_type='openai_inline_multipart'", &got.sessions},
			"ai_gateway_input_assets":          {"user_id=? AND source_type='openai_inline_multipart'", &got.inputs},
			"ai_requests":                      {"user_id=? AND modality='video'", &got.requests},
			"ai_gateway_tasks":                 {"user_id=? AND capability='video.generate'", &got.tasks},
			"wallet_holds":                     {"user_id=?", &got.holds},
			"ai_video_input_deletion_requests": {"user_id=?", &got.deletions},
		} {
			if err := f.DB.Table(table).Where(check.where, f.ProjectID).Count(check.target).Error; err != nil {
				t.Fatal(err)
			}
		}
		return got
	}
	afterUnknown := read()
	store.Lock()
	writes := store.writes
	store.Unlock()
	if afterUnknown.sessions != 1 || afterUnknown.inputs != 1 || afterUnknown.requests != 1 || afterUnknown.tasks != 1 || afterUnknown.holds != holdsBefore+1 || afterUnknown.deletions != 0 || writes != 1 {
		t.Fatalf("提交未知必须保留唯一可恢复事实：%+v holds_before=%d writes=%d", afterUnknown, holdsBefore, writes)
	}
	result, err = app.CreateOpenAIInlineVideo(t.Context(), command, input)
	if err != nil || result == nil || result.Job.ID == "" || !result.Existing {
		t.Fatalf("提交未知重放必须恢复原Job：result=%+v err=%v", result, err)
	}
	afterReplay := read()
	store.Lock()
	writes = store.writes
	store.Unlock()
	if afterReplay != afterUnknown || writes != 1 {
		t.Fatalf("提交未知重放不得重复写入：unknown=%+v replay=%+v writes=%d", afterUnknown, afterReplay, writes)
	}
}

func TestVideoG6OpenAIInlineOwnerIsolationMySQL(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	store := &videoG6InlineStore{entries: map[string]*videoG6InlineEntry{}}
	app := f.WithUploads(store)
	if _, err := app.AcceptProjectRights(t.Context(), service.VideoRightsAcceptCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID}, PolicyVersion: f.Policy, Confirmed: true, IdempotencyKey: "g6-inline-owner-rights-0001", RequestID: "g6-inline-owner-rights-request"}); err != nil {
		t.Fatal(err)
	}
	command := service.VideoCommand{Caller: service.VideoCaller{UserID: f.ProjectID, APIKeyID: f.ProjectID}, IdempotencyKey: "g6-inline-owner-create-0001", Model: f.Model, Prompt: "合成inline归属隔离", Operation: model.AIVideoOperationImageToVideo, Facade: "openai"}
	input := service.OpenAIVideoInlineInput{Filename: "reference.png", ContentType: "image/png", Body: f.Reference}
	created, err := app.CreateOpenAIInlineVideo(t.Context(), command, input)
	if err != nil || created == nil || created.Job.ID == "" {
		t.Fatalf("归属基线创建失败：result=%+v err=%v", created, err)
	}
	var sessionsBefore, inputsBefore, tasksBefore int64
	_ = f.DB.Table("ai_upload_sessions").Where("source_type='openai_inline_multipart'").Count(&sessionsBefore).Error
	_ = f.DB.Table("ai_gateway_input_assets").Where("source_type='openai_inline_multipart'").Count(&inputsBefore).Error
	_ = f.DB.Table("ai_gateway_tasks").Where("capability='video.generate'").Count(&tasksBefore).Error
	for _, test := range []struct {
		name   string
		caller service.VideoCaller
	}{
		{"跨用户", service.VideoCaller{UserID: f.ProjectID + 1, APIKeyID: f.ProjectID}},
		{"跨Project", service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID + 1, APIKeyID: f.ProjectID}},
		{"跨Key", service.VideoCaller{UserID: f.ProjectID, APIKeyID: f.OtherKeyID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			probe := command
			probe.Caller = test.caller
			result, err := app.CreateOpenAIInlineVideo(t.Context(), probe, input)
			if result != nil || err == nil {
				t.Fatalf("越权不能复用或新建inline任务：result=%+v err=%v", result, err)
			}
		})
	}
	var sessionsAfter, inputsAfter, tasksAfter int64
	_ = f.DB.Table("ai_upload_sessions").Where("source_type='openai_inline_multipart'").Count(&sessionsAfter).Error
	_ = f.DB.Table("ai_gateway_input_assets").Where("source_type='openai_inline_multipart'").Count(&inputsAfter).Error
	_ = f.DB.Table("ai_gateway_tasks").Where("capability='video.generate'").Count(&tasksAfter).Error
	if sessionsAfter != sessionsBefore || inputsAfter != inputsBefore || tasksAfter != tasksBefore {
		t.Fatalf("越权不能形成新事实：sessions=%d/%d inputs=%d/%d tasks=%d/%d", sessionsBefore, sessionsAfter, inputsBefore, inputsAfter, tasksBefore, tasksAfter)
	}
}
func (s *videoG6InlineStore) PutNormalized(_ context.Context, target service.VideoUploadTarget, raw []byte, sha string) error {
	s.Lock()
	e := s.entries[target.SessionID]
	if e == nil || e.discarded || crypto.SHA256Hex(string(raw)) != sha {
		s.Unlock()
		return service.ErrVideoUploadConflict
	}
	if len(e.normalized) != 0 && !bytes.Equal(e.normalized, raw) {
		s.Unlock()
		return service.ErrVideoUploadConflict
	}
	e.normalized = append([]byte(nil), raw...)
	hook := s.afterNormalized
	s.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func TestVideoG6OpenAIInlineFinalAdmissionCleanupMySQL(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	store := &videoG6InlineStore{entries: map[string]*videoG6InlineEntry{}}
	app := f.WithUploads(store)
	if _, err := app.AcceptProjectRights(t.Context(), service.VideoRightsAcceptCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID}, PolicyVersion: f.Policy, Confirmed: true, IdempotencyKey: "g6-inline-cleanup-rights-0001", RequestID: "g6-inline-cleanup-rights-request"}); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	var hookErr error
	nextPolicy := f.Policy + "-next"
	t.Cleanup(func() {
		_ = f.DB.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE policy_version=? AND status='active'", nextPolicy).Error
	})
	store.afterNormalized = func() {
		once.Do(func() {
			hookErr = f.DB.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE policy_version=? AND status='active'", f.Policy).Error; err != nil {
					return err
				}
				return tx.Exec("INSERT INTO ai_video_rights_policies(policy_version,purpose,title,body,body_sha256,status,effective_at,expires_at,acceptance_ttl_seconds,version_no) SELECT ?,purpose,title,body,body_sha256,'active',UTC_TIMESTAMP()-INTERVAL 1 HOUR,UTC_TIMESTAMP()+INTERVAL 1 HOUR,acceptance_ttl_seconds,1 FROM ai_video_rights_policies WHERE policy_version=?", nextPolicy, f.Policy).Error
			})
		})
	}
	command := service.VideoCommand{Caller: service.VideoCaller{UserID: f.ProjectID, APIKeyID: f.ProjectID}, IdempotencyKey: "g6-inline-cleanup-create-0001", Model: f.Model, Prompt: "合成inline尾部权利变化", Operation: "image_to_video", Facade: "openai"}
	input := service.OpenAIVideoInlineInput{Filename: "reference.png", ContentType: "image/png", Body: f.Reference}
	var holdsBefore int64
	if err := f.DB.Table("wallet_holds").Where("user_id=?", f.ProjectID).Count(&holdsBefore).Error; err != nil {
		t.Fatal(err)
	}
	result, createErr := app.CreateOpenAIInlineVideo(t.Context(), command, input)
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	if !errors.Is(createErr, service.ErrVideoRightsRequired) || result != nil {
		t.Fatalf("尾部权利失效必须拒绝生成: result=%+v err=%v", result, createErr)
	}
	var state string
	if err := f.DB.Table("ai_gateway_input_assets").Select("lifecycle_state").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Take(&state).Error; err != nil || state != model.AIInputAssetPendingDelete {
		t.Fatalf("尾部拒绝未把inline输入收口到pending_delete: state=%s err=%v", state, err)
	}
	for table, where := range map[string]string{"ai_video_input_deletion_requests": "user_id=?", "ai_requests": "user_id=? AND modality='video'", "ai_gateway_tasks": "user_id=? AND capability='video.generate'", "wallet_holds": "user_id=?"} {
		var count int64
		if err := f.DB.Table(table).Where(where, f.ProjectID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		want := int64(0)
		if table == "ai_video_input_deletion_requests" {
			want = 1
		}
		if table == "wallet_holds" {
			want = holdsBefore
		}
		if count != want {
			t.Fatalf("尾部拒绝事实错误: table=%s count=%d want=%d", table, count, want)
		}
	}
}

func (s *videoG6InlineStore) ReadNormalized(_ context.Context, bucket, key string, max int64) ([]byte, error) {
	s.Lock()
	defer s.Unlock()
	for _, e := range s.entries {
		if !e.discarded && e.target.NormalizedBucket == bucket && e.target.NormalizedKey == key && int64(len(e.normalized)) <= max {
			return append([]byte(nil), e.normalized...), nil
		}
	}
	return nil, service.ErrVideoUploadUnavailable
}
func (s *videoG6InlineStore) Discard(_ context.Context, target service.VideoUploadTarget) error {
	s.Lock()
	defer s.Unlock()
	e := s.entries[target.SessionID]
	if e == nil {
		e = &videoG6InlineEntry{target: target}
		s.entries[target.SessionID] = e
	}
	e.discarded, e.raw, e.frozen, e.normalized = true, nil, nil, nil
	return nil
}

func TestVideoG6OpenAIInlineMultipartI2VMySQL(t *testing.T) {
	f := service.NewVideoImportHTTPFixture(t)
	store := &videoG6InlineStore{entries: map[string]*videoG6InlineEntry{}}
	app := f.WithUploads(store)
	mux := http.NewServeMux()
	gateway.RegisterVideoUserRoutes(mux, app, f.Keys, true, f.JWT)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	call := func(key, prompt, filename string, image []byte) (int, dto.VideoJob, []byte) {
		t.Helper()
		var payload bytes.Buffer
		writer := multipart.NewWriter(&payload)
		for name, value := range map[string]string{"model": f.Model, "prompt": prompt, "seconds": "5", "size": "1280x720"} {
			if err := writer.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
		if filename != "" {
			part, err := writer.CreateFormFile("input_reference", filename)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(image); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/videos", &payload)
		req.Header.Set("Authorization", "Bearer "+f.Key)
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var job dto.VideoJob
		if res.StatusCode == 200 {
			_ = json.Unmarshal(raw, &job)
		}
		return res.StatusCode, job, raw
	}

	var sessions, inputs, bindings int64
	status, _, _ := call("g6-inline-no-rights-0001", "合成缺权利输入", "reference.png", f.Reference)
	_ = f.DB.Table("ai_upload_sessions").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&sessions).Error
	if status != 403 || sessions != 0 {
		t.Fatalf("缺权利必须在输入持久化前拒绝: HTTP=%d sessions=%d", status, sessions)
	}
	status, textFirst, _ := call("g6-inline-cross-mode-0001", "合成跨模式文生", "", nil)
	if status != 200 || textFirst.ID == "" {
		t.Fatalf("跨模式前置T2V失败: HTTP=%d", status)
	}
	if _, err := app.AcceptProjectRights(t.Context(), service.VideoRightsAcceptCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID}, PolicyVersion: f.Policy, Confirmed: true, IdempotencyKey: "g6-inline-rights-accept-0001", RequestID: "g6-inline-rights-request-0001"}); err != nil {
		t.Fatal(err)
	}
	status, _, _ = call("g6-inline-cross-mode-0001", "合成跨模式文生", "reference.png", f.Reference)
	_ = f.DB.Table("ai_upload_sessions").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&sessions).Error
	if status != 409 || sessions != 0 {
		t.Fatalf("T2V到I2V同键必须在输入前冲突: HTTP=%d sessions=%d", status, sessions)
	}

	status, first, raw := call("g6-inline-create-video-0001", "合成inline图生视频", "reference.png", f.Reference)
	if status != 200 || first.ID == "" || first.Status != "queued" || bytes.Contains(raw, []byte("vin_")) || bytes.Contains(raw, []byte("vup_")) {
		t.Fatalf("inline I2V首次创建失败或泄露内部输入: HTTP=%d body=%s", status, raw)
	}
	status, replay, _ := call("g6-inline-create-video-0001", "合成inline图生视频", "reference.png", f.Reference)
	if status != 200 || replay.ID != first.ID {
		t.Fatalf("inline I2V重放未返回原任务: HTTP=%d", status)
	}
	changed := append([]byte(nil), f.Reference...)
	changed[len(changed)-1] ^= 1
	status, _, _ = call("g6-inline-create-video-0001", "合成inline图生视频", "reference.png", changed)
	if status != 409 {
		t.Fatalf("同键异文件必须409: HTTP=%d", status)
	}
	_ = f.DB.Table("ai_upload_sessions").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&sessions).Error
	_ = f.DB.Table("ai_gateway_input_assets").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&inputs).Error
	_ = f.DB.Table("ai_gateway_task_inputs AS b").Joins("JOIN ai_gateway_input_assets i ON i.id=b.input_asset_id").Where("i.user_id=? AND i.source_type='openai_inline_multipart'", f.ProjectID).Count(&bindings).Error
	store.Lock()
	writes := store.writes
	store.Unlock()
	if sessions != 1 || inputs != 1 || bindings != 1 || writes != 1 {
		t.Fatalf("inline事实不唯一: sessions=%d inputs=%d bindings=%d writes=%d", sessions, inputs, bindings, writes)
	}
	// 前两个独立意图已完成各自断言；按真实状态机推进到submitting，避免把它们占用的queued槽位混入下一组首次并发。
	keyID := f.ProjectID
	owner := repository.VideoOwner{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: &keyID}
	for _, taskID := range []string{textFirst.ID, first.ID} {
		task, err := repository.NewVideoTaskRepository(f.DB).FindForOwner(t.Context(), taskID, owner)
		if err != nil {
			t.Fatal(err)
		}
		task, err = repository.NewVideoTaskRepository(f.DB).TransitionExecution(t.Context(), repository.VideoStateTransition{TaskPublicID: task.PublicID, Owner: owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskQueued, Progress: 10, EventID: task.RequestID + "_inline_queue_capacity", Source: "worker", Now: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repository.NewVideoTaskRepository(f.DB).TransitionExecution(t.Context(), repository.VideoStateTransition{TaskPublicID: task.PublicID, Owner: owner, ExpectedVersion: task.VersionNo, ToStatus: model.AIImageTaskSubmitting, Progress: 20, EventID: task.RequestID + "_inline_submitting_capacity", Source: "worker", Now: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}

	// 100个请求从空命令同时进入，必须共同等待唯一上传赢家和唯一生成事务。
	type concurrentResult struct {
		status int
		job    dto.VideoJob
	}
	start := make(chan struct{})
	results := make(chan concurrentResult, 100)
	for index := 0; index < 100; index++ {
		go func() {
			<-start
			status, job, _ := call("g6-inline-concurrent-0001", "合成inline并发", "reference.png", f.Reference)
			results <- concurrentResult{status: status, job: job}
		}()
	}
	close(start)
	concurrentID := ""
	for index := 0; index < 100; index++ {
		got := <-results
		if got.status != 200 || got.job.ID == "" {
			t.Fatalf("inline并发请求失败: HTTP=%d", got.status)
		}
		if concurrentID == "" {
			concurrentID = got.job.ID
		} else if got.job.ID != concurrentID {
			t.Fatal("inline并发产生了不同任务")
		}
	}
	_ = f.DB.Table("ai_upload_sessions").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&sessions).Error
	_ = f.DB.Table("ai_gateway_input_assets").Where("user_id=? AND source_type='openai_inline_multipart'", f.ProjectID).Count(&inputs).Error
	_ = f.DB.Table("ai_gateway_task_inputs AS b").Joins("JOIN ai_gateway_input_assets i ON i.id=b.input_asset_id").Where("i.user_id=? AND i.source_type='openai_inline_multipart'", f.ProjectID).Count(&bindings).Error
	store.Lock()
	writes = store.writes
	store.Unlock()
	if sessions != 2 || inputs != 2 || bindings != 2 || writes != 2 {
		t.Fatalf("inline并发事实不唯一: sessions=%d inputs=%d bindings=%d writes=%d", sessions, inputs, bindings, writes)
	}

	// 无文件仍走T2V；不允许未知引用字段、重复文件、空文件或伪造扩展名。
	status, textJob, _ := call("g6-inline-t2v-without-file-0001", "合成无文件文生视频", "", nil)
	if status != 200 || textJob.ID == "" {
		t.Fatalf("无文件T2V被inline权利链阻断: HTTP=%d", status)
	}
	status, _, _ = call("g6-inline-empty-file-0001", "合成空文件", "reference.png", nil)
	if status != 422 {
		t.Fatalf("空input_reference必须422: HTTP=%d", status)
	}
	status, _, _ = call("g6-inline-wrong-extension-0001", "合成伪扩展", "reference.gif", f.Reference)
	if status != 422 {
		t.Fatalf("伪造扩展名必须422: HTTP=%d", status)
	}
	strictCall := func(key string, build func(*multipart.Writer)) int {
		t.Helper()
		var payload bytes.Buffer
		writer := multipart.NewWriter(&payload)
		_ = writer.WriteField("model", f.Model)
		_ = writer.WriteField("prompt", "合成严格multipart")
		build(writer)
		_ = writer.Close()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/videos", &payload)
		req.Header.Set("Authorization", "Bearer "+f.Key)
		req.Header.Set("Idempotency-Key", key)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		_, _ = io.Copy(io.Discard, res.Body)
		return res.StatusCode
	}
	if status := strictCall("g6-inline-unknown-field-0001", func(writer *multipart.Writer) { _ = writer.WriteField("image_url", "https://example.invalid/a.png") }); status != 400 {
		t.Fatalf("image_url必须400: HTTP=%d", status)
	}
	if status := strictCall("g6-inline-duplicate-file-0001", func(writer *multipart.Writer) {
		for index := 0; index < 2; index++ {
			part, _ := writer.CreateFormFile("input_reference", "reference.png")
			_, _ = part.Write(f.Reference)
		}
	}); status != 400 {
		t.Fatalf("重复input_reference必须400: HTTP=%d", status)
	}
}
