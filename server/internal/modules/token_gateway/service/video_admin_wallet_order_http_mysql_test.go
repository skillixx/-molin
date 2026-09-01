package service_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	authmodel "molin/server/internal/modules/auth/model"
	gateway "molin/server/internal/modules/token_gateway"
	"molin/server/internal/modules/token_gateway/service"
)

type adminWalletPageKey struct{}

// 只控制真实数据库连接获得首个钱包锁的时序，不替换鉴权、SQL结果或财务服务。
type adminWalletOrderPool struct {
	gorm.ConnPool
	mu           sync.Mutex
	first        map[string]string
	selected     chan struct{}
	acquired     chan struct{}
	acquireCount int
	deadlocks    atomic.Int64
}

func (p *adminWalletOrderPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	tx, err := p.ConnPool.(gorm.TxBeginner).BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &adminWalletOrderTx{ConnPool: tx, tx: tx, p: p}, nil
}

type adminWalletOrderTx struct {
	gorm.ConnPool
	tx   *sql.Tx
	p    *adminWalletOrderPool
	seen bool
}

func (t *adminWalletOrderTx) Commit() error   { return t.tx.Commit() }
func (t *adminWalletOrderTx) Rollback() error { return t.tx.Rollback() }
func (t *adminWalletOrderTx) QueryContext(ctx context.Context, q string, args ...interface{}) (*sql.Rows, error) {
	page, _ := ctx.Value(adminWalletPageKey{}).(string)
	first := page != "" && !t.seen && strings.Contains(q, "FROM `wallets`") && strings.Contains(q, "FOR UPDATE")
	different := false
	if first {
		t.seen = true
		t.p.mu.Lock()
		t.p.first[page] = fmt.Sprintf("%v", args)
		if len(t.p.first) == 2 {
			close(t.p.selected)
		}
		t.p.mu.Unlock()
		select {
		case <-t.p.selected:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		t.p.mu.Lock()
		different = t.p.first["1"] != t.p.first["2"]
		t.p.mu.Unlock()
	}
	rows, err := t.ConnPool.QueryContext(ctx, q, args...)
	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) && mysqlError.Number == 1213 {
		t.p.deadlocks.Add(1)
	}
	// 首锁相异时使两连接同时持锁再继续，旧的反向锁序会稳定死锁；相同首锁必须允许先行者完成。
	if first && different && err == nil {
		t.p.mu.Lock()
		t.p.acquireCount++
		if t.p.acquireCount == 2 {
			close(t.p.acquired)
		}
		t.p.mu.Unlock()
		select {
		case <-t.p.acquired:
		case <-ctx.Done():
			_ = rows.Close()
			return nil, ctx.Err()
		}
	}
	return rows, err
}

func TestVideoG6AdminListDisjointWalletsHTTPMySQL(t *testing.T) {
	a := service.NewVideoContentHTTPFixture(t)
	// 当前合同只允许一个生效权利版本；退出本测试第一套合成I2V政策后再建第二套。
	// 两用户均只创建T2V，不依赖I2V协议；不删除或改写政策正文，也不触及共享环境。
	if err := a.DB.Exec("UPDATE ai_video_rights_policies SET status='retired',version_no=version_no+1 WHERE policy_version=? AND status='active'", a.Policy).Error; err != nil {
		t.Fatal(err)
	}
	b := service.NewVideoContentHTTPFixture(t)
	create := func(f service.VideoContentHTTPFixture, key string) string {
		t.Helper()
		job, err := f.App.Create(context.Background(), service.VideoCommand{Caller: service.VideoCaller{UserID: f.ProjectID, ProjectID: f.ProjectID, APIKeyID: f.ProjectID}, IdempotencyKey: key, Model: f.Model, Prompt: "仅用于两钱包分页竞争", Operation: "text_to_video"})
		if err != nil {
			t.Fatal(err)
		}
		f.Execute(job.Job.ID)
		f.Settle(job.Job.ID)
		f.Deliver(job.Job.ID)
		// 数据库时间列为秒精度；通过真实创建时间形成确定的跨页反向展示，不能篡改历史任务时间。
		time.Sleep(1100 * time.Millisecond)
		return job.Job.ID
	}
	a1, b2 := create(a, "g6-disjoint-wallet-a1"), create(b, "g6-disjoint-wallet-b2")
	b1, a2 := create(b, "g6-disjoint-wallet-b1"), create(a, "g6-disjoint-wallet-a2")
	verified := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	admin := authmodel.User{ID: service.NextVideoFixtureUserID(), PasswordHash: "synthetic-only", Status: "active", AdminPhoneVerifiedAt: &verified, AdminEmailVerifiedAt: &verified}
	if err := a.DB.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.DB.Exec("INSERT INTO user_permission_overrides(user_id,permission_id,permission_code,effect) SELECT ?,id,code,'allow' FROM permissions WHERE code='ai_gateway:view'", admin.ID).Error; err != nil {
		t.Fatal(err)
	}
	app, err := service.NewVideoAdminService(a.App, 24)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gateway.RegisterVideoAdminRoutes(mux, app, a.JWT, true)
	var armed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if armed.Load() {
			r = r.WithContext(context.WithValue(r.Context(), adminWalletPageKey{}, r.URL.Query().Get("page")))
		}
		mux.ServeHTTP(w, r)
	}))
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 35 * time.Second
	token := a.TokenForUser(admin.ID)
	read := func(page int) (*service.VideoAdminTaskPage, error) {
		r, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/admin/token/video-tasks?page=%d&page_size=2", srv.URL, page), nil)
		r.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(r)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != 200 {
			return nil, fmt.Errorf("分页HTTP=%d err=%v", resp.StatusCode, err)
		}
		var e struct {
			Code int                        `json:"code"`
			Data service.VideoAdminTaskPage `json:"data"`
		}
		if json.Unmarshal(raw, &e) != nil || e.Code != 0 {
			return nil, fmt.Errorf("分页响应无效")
		}
		return &e.Data, nil
	}
	want := [][]string{{a2, b1}, {b2, a1}}
	assertPage := func(n int, page *service.VideoAdminTaskPage) error {
		if page == nil || len(page.Items) != 2 || page.Items[0].TaskID != want[n-1][0] || page.Items[1].TaskID != want[n-1][1] || !page.Items[0].CanDeliver || !page.Items[1].CanDeliver {
			return fmt.Errorf("第%d页必须保留两条已结算可交付任务和原展示顺序", n)
		}
		return nil
	}
	for n := 1; n <= 2; n++ {
		page, err := read(n)
		if err != nil {
			t.Fatal(err)
		}
		if err := assertPage(n, page); err != nil {
			t.Fatal(err)
		}
	}
	beforeA, beforeB := a.FinancialSnapshot(), b.FinancialSnapshot()
	pool := &adminWalletOrderPool{ConnPool: a.DB.ConnPool, first: map[string]string{}, selected: make(chan struct{}), acquired: make(chan struct{})}
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{Logger: a.DB.Logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.UseApplicationDB(db))
	armed.Store(true)
	errors := make(chan error, 2)
	for n := 1; n <= 2; n++ {
		go func(n int) {
			page, err := read(n)
			if err == nil {
				err = assertPage(n, page)
			}
			errors <- err
		}(n)
	}
	for n := 0; n < 2; n++ {
		if err := <-errors; err != nil {
			t.Error(err)
		}
	}
	pool.mu.Lock()
	firstCount, acquired := len(pool.first), pool.acquireCount
	firstOne, firstTwo := pool.first["1"], pool.first["2"]
	pool.mu.Unlock()
	t.Logf("合成分页首锁：page1=%s page2=%s distinct_locks_acquired=%d deadlocks=%d", firstOne, firstTwo, acquired, pool.deadlocks.Load())
	if firstCount != 2 {
		t.Error("必须实际执行两个分页事务的钱包锁查询")
	}
	if pool.deadlocks.Load() != 0 {
		t.Errorf("真实数据库发生%d次钱包死锁", pool.deadlocks.Load())
	}
	if !bytes.Equal(beforeA, a.FinancialSnapshot()) || !bytes.Equal(beforeB, b.FinancialSnapshot()) {
		t.Fatal("只读管理员列表不得改写任一钱包")
	}
}
