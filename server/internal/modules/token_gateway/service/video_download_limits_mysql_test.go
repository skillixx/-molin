package service

import (
	"context"
	"database/sql"
	"errors"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/model"
)

type videoDownloadCommitPool struct {
	gorm.ConnPool
	lost atomic.Bool
}

func (p *videoDownloadCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &videoDownloadCommitTx{ConnPool: tx, tx: tx, pool: p}, nil
}

type videoDownloadCommitTx struct {
	gorm.ConnPool
	tx         *sql.Tx
	pool       *videoDownloadCommitPool
	leaseWrite bool
}

func (t *videoDownloadCommitTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.Contains(strings.ToLower(query), "insert into ai_video_download_leases") {
		t.leaseWrite = true
	}
	return t.tx.ExecContext(ctx, query, args...)
}
func (t *videoDownloadCommitTx) Commit() error {
	if err := t.tx.Commit(); err != nil {
		return err
	}
	if t.leaseWrite && t.pool.lost.CompareAndSwap(false, true) {
		return errors.New("合成下载租约COMMIT确认丢失")
	}
	return nil
}
func (t *videoDownloadCommitTx) Rollback() error { return t.tx.Rollback() }

// 同一用户100次请求只能取得两个共享名额，关闭与过期后旧连接不能恢复占用。
func TestVideoG6DownloadLimitsMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	ctx := context.Background()
	c := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	job, err := f.App.Create(ctx, VideoCommand{Caller: c, IdempotencyKey: "g6-download-limits-create", Model: f.Model, Prompt: "仅隔离下载限额测试", Operation: model.AIVideoOperationTextToVideo})
	if err != nil {
		t.Fatal(err)
	}
	f.Execute(job.Job.ID)
	f.Settle(job.Job.ID)
	f.Deliver(job.Job.ID)
	secondTask := f.CreateCompletedForKey(f.ProjectID)
	var mu sync.Mutex
	var winners []*VideoContent
	var limited int
	var wg sync.WaitGroup
	start := make(chan struct{})
	otherInstance := *f.App
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			app := f.App
			if i%2 == 1 {
				app = &otherInstance
			}
			taskID := job.Job.ID
			if i%2 == 1 {
				taskID = secondTask
			}
			got, err := app.GetContent(ctx, c, taskID)
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, ErrVideoDownloadLimited) && got == nil {
				limited++
				return
			}
			if err != nil || got == nil {
				t.Errorf("下载准入失败：%v", err)
				return
			}
			winners = append(winners, got)
		}()
	}
	close(start)
	wg.Wait()
	defer func() {
		for _, v := range winners {
			_ = v.Close()
		}
	}()
	if len(winners) != 2 || limited != 98 {
		t.Fatalf("必须恰好2成功/98限流，实际%d/%d", len(winners), limited)
	}
	if err := winners[0].Close(); err != nil {
		t.Fatal(err)
	}
	if err := winners[0].Close(); err != nil {
		t.Fatal("重复释放必须安全")
	}
	replacement, err := f.App.GetContent(ctx, c, job.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if _, err := winners[0].BeforeWrite(ctx); err == nil {
		t.Fatal("旧连接不能续约")
	}
	if got, err := f.App.GetContent(ctx, c, job.Job.ID); !errors.Is(err, ErrVideoDownloadLimited) || got != nil {
		t.Fatal("重复释放旧连接不能释放新名额")
	}
	// 只让本合成主体的操作性租约过期，不改Task或任何财务事实。
	if err := f.DB.Exec("UPDATE ai_video_download_leases SET lease_until=UTC_TIMESTAMP(6) WHERE user_id=? AND released_at IS NULL", f.ProjectID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := replacement.BeforeWrite(ctx); err == nil {
		t.Fatal("过期租约不得复活")
	}
	again, err := f.App.GetContent(ctx, c, job.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
}

func TestVideoG6DownloadProjectFifthLeaseMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	// 生产固定用户2会先于Project4触发；测试只放宽被遮蔽作用域，HTTP无法注入该值。
	f.App.downloadLimits = videoDownloadLimits{User: 100, Project: videoDownloadProjectLimit}
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	contents := make([]*VideoContent, 0, videoDownloadProjectLimit)
	defer func() {
		for _, content := range contents {
			_ = content.Close()
		}
	}()
	for index := 0; index < videoDownloadProjectLimit+1; index++ {
		id := f.CreateCompletedForKey(f.ProjectID)
		beforeHeads := f.HeadCalls()
		content, err := f.App.GetContent(context.Background(), caller, id)
		if index < videoDownloadProjectLimit {
			if err != nil || content == nil {
				t.Fatalf("Project第%d路必须取得租约：%v", index+1, err)
			}
			contents = append(contents, content)
			continue
		}
		if !errors.Is(err, ErrVideoDownloadLimited) || content != nil || f.HeadCalls() != beforeHeads {
			t.Fatalf("Project第5路必须在Store前限流：content=%v err=%v heads=%d/%d", content, err, beforeHeads, f.HeadCalls())
		}
	}
	var active int64
	if err := f.DB.Table("ai_video_download_leases").Where("project_id=? AND released_at IS NULL AND lease_until>UTC_TIMESTAMP(6)", f.ProjectID).Count(&active).Error; err != nil || active != videoDownloadProjectLimit {
		t.Fatalf("Project必须只有4个有效租约：active=%d err=%v", active, err)
	}
}

func TestVideoG6DownloadLeaseCommitUnknownMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	id := f.CreateCompletedForKey(f.ProjectID)
	pool := &videoDownloadCommitPool{ConnPool: f.DB.ConnPool}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: f.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	app := *f.App
	app.db = db
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	content, err := app.GetContent(context.Background(), caller, id)
	if err != nil || content == nil || !pool.lost.Load() {
		t.Fatalf("真实提交后确认丢失必须恢复原租约：content=%v err=%v lost=%t", content, err, pool.lost.Load())
	}
	var active int64
	if err := f.DB.Table("ai_video_download_leases").Where("user_id=? AND released_at IS NULL AND lease_until>UTC_TIMESTAMP(6)", f.ProjectID).Count(&active).Error; err != nil || active != 1 {
		t.Fatalf("确认丢失只能保留一个有效租约：active=%d err=%v", active, err)
	}
	if err := content.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Table("ai_video_download_leases").Where("user_id=? AND released_at IS NULL AND lease_until>UTC_TIMESTAMP(6)", f.ProjectID).Count(&active).Error; err != nil || active != 0 {
		t.Fatalf("恢复租约必须正常释放：active=%d err=%v", active, err)
	}
}

// 续约UPDATE未提交跨过旧TTL时，新申请必须等待同范围锁，不能先补位再形成第三个有效租约。
func TestVideoG6DownloadRenewalRaceMySQL(t *testing.T) {
	f := NewVideoContentHTTPFixture(t)
	id := f.CreateCompletedForKey(f.ProjectID)
	caller := VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}
	first, err := f.App.GetContent(context.Background(), caller, id)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	var row struct {
		LeaseID    string
		LeaseUntil time.Time
	}
	if err := f.DB.Table("ai_video_download_leases").Where("user_id=? AND released_at IS NULL", f.ProjectID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	second, err := f.App.GetContent(context.Background(), caller, id)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := f.DB.Exec("UPDATE ai_video_download_leases SET lease_until=DATE_ADD(UTC_TIMESTAMP(6),INTERVAL 1 SECOND) WHERE lease_id=?", row.LeaseID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.DB.Table("ai_video_download_leases").Where("lease_id=?", row.LeaseID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	defer resume()
	var paused atomic.Bool
	var renewConnection uint64
	name := "g6_download_renew_commit_window"
	if err := f.DB.Callback().Raw().After("gorm:raw").Register(name, func(tx *gorm.DB) {
		if tx.Error != nil || tx.RowsAffected != 1 || !strings.HasPrefix(tx.Statement.SQL.String(), "UPDATE ai_video_download_leases SET version_no=version_no+1,lease_until=") {
			return
		}
		matched := false
		for _, v := range tx.Statement.Vars {
			if s, ok := v.(string); ok && s == row.LeaseID {
				matched = true
			}
		}
		if matched && paused.CompareAndSwap(false, true) {
			if err := tx.Session(&gorm.Session{NewDB: true}).Raw("SELECT CONNECTION_ID()").Scan(&renewConnection).Error; err != nil {
				tx.AddError(err)
				return
			}
			close(entered)
			select {
			case <-release:
			case <-tx.Statement.Context.Done():
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer f.DB.Callback().Raw().Remove(name)
	renewed := make(chan error, 1)
	go func() { _, err := first.BeforeWrite(context.Background()); renewed <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("未进入续约提交窗口")
	}
	// 数据库时钟构造一秒短租约，仅验证操作性回收，不改变媒体或财务期限。
	clockDeadline := time.Now().Add(2 * time.Second)
	for {
		var expired bool
		if err := f.DB.Raw("SELECT UTC_TIMESTAMP(6)>=?", row.LeaseUntil).Scan(&expired).Error; err != nil {
			t.Fatal(err)
		}
		if expired {
			break
		}
		if time.Now().After(clockDeadline) {
			t.Fatal("数据库时钟未跨过旧TTL")
		}
		timer := time.NewTimer(10 * time.Millisecond)
		<-timer.C
	}
	type answer struct {
		content *VideoContent
		err     error
	}
	result := make(chan answer, 1)
	go func() { v, err := f.App.GetContent(context.Background(), caller, id); result <- answer{v, err} }()
	var early *answer
	// 修复前必须观察到提前完成，修复后必须观察到实际scope行锁等待；不靠固定sleep制造偶然绿。
	waitDeadline := time.Now().Add(2 * time.Second)
	for early == nil {
		select {
		case r := <-result:
			early = &r
		default:
		}
		if early != nil {
			break
		}
		var waits int64
		if err := f.DB.Raw("SELECT COUNT(*) FROM performance_schema.data_lock_waits w JOIN performance_schema.data_locks d ON d.ENGINE_LOCK_ID=w.REQUESTING_ENGINE_LOCK_ID AND d.ENGINE=w.ENGINE JOIN performance_schema.threads th ON th.THREAD_ID=w.BLOCKING_THREAD_ID WHERE d.OBJECT_SCHEMA=DATABASE() AND d.OBJECT_NAME='ai_video_download_scopes' AND th.PROCESSLIST_ID=?", renewConnection).Scan(&waits).Error; err != nil {
			t.Fatal(err)
		}
		if waits > 0 {
			break
		}
		if time.Now().After(waitDeadline) {
			t.Fatal("未观察到新申请完成或等待续约持有的scope锁")
		}
		timer := time.NewTimer(10 * time.Millisecond)
		<-timer.C
	}
	resume()
	if err := <-renewed; err != nil {
		t.Fatalf("原持有者应能提交有效续约：%v", err)
	}
	var got answer
	if early != nil {
		got = *early
	} else {
		select {
		case got = <-result:
		case <-time.After(5 * time.Second):
			t.Fatal("续约提交后新申请未结束")
		}
	}
	if got.content != nil {
		defer got.content.Close()
	}
	var active int64
	if err := f.DB.Table("ai_video_download_leases").Where("user_id=? AND released_at IS NULL AND lease_until>UTC_TIMESTAMP(6)", f.ProjectID).Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(got.err, ErrVideoDownloadLimited) || got.content != nil {
		t.Fatalf("续约提交跨旧TTL不得令新请求补出第三个下载名额：active=%d", active)
	}
	if active != 2 {
		t.Fatalf("窗口结束必须恰好2个有效名额，实际%d", active)
	}
}
