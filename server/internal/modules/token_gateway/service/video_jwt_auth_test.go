package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"molin/server/pkg/crypto"
	pkgjwt "molin/server/pkg/jwt"
)

// 吊销存储是外部边界，测试保留真实JWT验签与GORM用户读取，不替换认证结果。
type videoTestRevocations struct {
	mu          sync.RWMutex
	revoked     map[string]bool
	unavailable bool
}

func (v *videoTestRevocations) IsRevoked(_ context.Context, digest string) (bool, error) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.unavailable {
		return false, errors.New("吊销存储不可用")
	}
	return v.revoked[digest], nil
}

func TestVideoG6JWTAuthenticationFailsClosed(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: database, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("j", 32)
	token, err := pkgjwt.Generate(7, "", secret, 60)
	if err != nil {
		t.Fatal(err)
	}
	store := &videoTestRevocations{revoked: map[string]bool{}}
	auth, err := NewVideoJWTAuthenticator(db, secret, store)
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT .*users").WithArgs(uint64(7), 1).WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("active"))
	caller, err := auth.Authenticate(context.Background(), token)
	if err != nil || caller.UserID != 7 || caller.APIKeyID != 0 {
		t.Fatalf("合法JWT失败：%v", err)
	}
	rawCaller, err := json.Marshal(caller)
	if err != nil {
		t.Fatal(err)
	}
	var callerFields map[string]json.RawMessage
	if json.Unmarshal(rawCaller, &callerFields) != nil || len(callerFields) != 3 || strings.Contains(string(rawCaller), token) || strings.Contains(string(rawCaller), crypto.SHA256Hex(token)) {
		t.Fatal("内存凭据复验能力和摘要不得进入普通JSON")
	}
	store.unavailable = true
	if _, err := auth.Authenticate(context.Background(), token); !errors.Is(err, ErrVideoAccessUnavailable) {
		t.Fatal("吊销存储故障不能放行")
	}
	if _, err := auth.Authenticate(context.Background(), token+"broken"); !errors.Is(err, ErrVideoJWTInvalid) {
		t.Fatal("篡改JWT未拒绝")
	}
	store.unavailable = false
	store.revoked[crypto.SHA256Hex(token)] = true
	if _, err := auth.Authenticate(context.Background(), token); !errors.Is(err, ErrVideoJWTInvalid) {
		t.Fatal("已吊销JWT仍有效")
	}
	expired, err := pkgjwt.Generate(7, "", secret, -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(context.Background(), expired); !errors.Is(err, ErrVideoJWTInvalid) {
		t.Fatal("过期JWT仍有效")
	}
	if _, err := NewVideoJWTAuthenticator(db, secret, nil); err == nil {
		t.Fatal("缺少吊销依赖不能装配")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// 外部依赖只观察上下文期限并失败，不替换JWT验签，也不执行数据库认证旁路。
type videoJWTDeadlineProbe struct {
	deadline time.Time
	bounded  bool
}

func (p *videoJWTDeadlineProbe) IsRevoked(ctx context.Context, _ string) (bool, error) {
	p.deadline, p.bounded = ctx.Deadline()
	return false, errors.New("合成吊销依赖故障")
}

func TestVideoG6JWTInitialDeadline(t *testing.T) {
	database, _, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: database, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("d", 32)
	for _, seconds := range []int64{5, 3600} {
		token, err := pkgjwt.Generate(7, "", secret, seconds)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := pkgjwt.Parse(token, secret)
		if err != nil {
			t.Fatal(err)
		}
		probe := &videoJWTDeadlineProbe{}
		auth, err := NewVideoJWTAuthenticator(db, secret, probe)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := auth.Authenticate(context.Background(), token); !errors.Is(err, ErrVideoAccessUnavailable) {
			t.Fatal("初次吊销依赖故障必须失败关闭")
		}
		if !probe.bounded || probe.deadline.After(claims.ExpiresAt.Time) || probe.deadline.After(time.Now().Add(30*time.Second)) {
			t.Fatal("初次依赖查询期限必须同时不晚于JWT实际过期和内部30秒上界")
		}
	}
}
