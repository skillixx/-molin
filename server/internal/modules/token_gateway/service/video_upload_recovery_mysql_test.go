package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
	"molin/server/internal/modules/token_gateway/model"
)

func TestVideoG6UploadMySQLRecoveryFences(t *testing.T) {
	t.Run("新租约仍处理中时旧失败不得清理", func(t *testing.T) {
		_, s, store, c, data := newVideoUploadFixture(t)
		ctx := context.Background()
		var clock atomic.Int64
		clock.Store(time.Now().UTC().Truncate(time.Second).Unix())
		s.now = func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
		created, err := s.Create(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.write(created.SessionID, data, "image/png"); err != nil {
			t.Fatal(err)
		}
		enteredA, enteredB := make(chan struct{}), make(chan struct{})
		releaseA, releaseB := make(chan struct{}), make(chan struct{})
		var onceA, onceB sync.Once
		unblockA := func() { onceA.Do(func() { close(releaseA) }) }
		unblockB := func() { onceB.Do(func() { close(releaseB) }) }
		t.Cleanup(unblockA)
		t.Cleanup(unblockB)
		var attempts atomic.Int32
		store.beforePut = func() {
			switch attempts.Add(1) {
			case 1:
				close(enteredA)
				<-releaseA
			case 2:
				close(enteredB)
				<-releaseB
			}
		}
		wait := func(ch <-chan struct{}) {
			t.Helper()
			select {
			case <-ch:
			case <-time.After(10 * time.Second):
				t.Fatal("执行者未进入受控IO边界")
			}
		}
		const key = "g6-upload-verifying-fence-0001"
		oldResult, newResult := make(chan error, 1), make(chan error, 1)
		go func() { _, err := s.Complete(ctx, c.Caller, created.SessionID, key); oldResult <- err }()
		wait(enteredA)
		clock.Add(180)
		go func() { _, err := s.Complete(ctx, c.Caller, created.SessionID, key); newResult <- err }()
		wait(enteredB)
		before, err := s.Get(ctx, c.Caller, created.SessionID)
		if err != nil || before.Status != "verifying" {
			t.Fatal("新执行者应持有仍处理中租约")
		}
		unblockA()
		select {
		case err := <-oldResult:
			if !errors.Is(err, ErrVideoUploadConflict) {
				t.Fatalf("旧执行者应因版本冲突失败：%v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("旧执行者未退出")
		}
		after, err := s.Get(ctx, c.Caller, created.SessionID)
		if err != nil || after.Status != "verifying" || after.VersionNo != before.VersionNo || after.CleanupPending || after.InputAssetID != nil {
			t.Fatalf("旧失败不得拒绝、清理或终止新租约：%+v %v", after, err)
		}
		unblockB()
		select {
		case err := <-newResult:
			if err != nil {
				t.Fatalf("新执行者必须仍能安全完成：%v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("新执行者未退出")
		}
		done, err := s.Get(ctx, c.Caller, created.SessionID)
		if err != nil || done.Status != "completed" || done.InputAssetID == nil || done.CleanupPending {
			t.Fatal("新租约应发布唯一输入")
		}
	})
	for _, number := range []uint16{1213, 1205} {
		t.Run(fmt.Sprintf("发布写入临时错误%d保留原对象", number), func(t *testing.T) {
			db, s, store, c, data := newVideoUploadFixture(t)
			ctx := context.Background()
			created, err := s.Create(ctx, c)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.write(created.SessionID, data, "image/png"); err != nil {
				t.Fatal(err)
			}
			// 在真实事务的INSERT边界只注入一次驱动错误，不替换仓储或事务结果。
			var injected atomic.Bool
			const callback = "g6_upload_insert_transient"
			if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "ai_gateway_input_assets" && injected.CompareAndSwap(false, true) {
					tx.AddError(&mysqlDriver.MySQLError{Number: number, Message: "isolated publication write fault"})
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
			const key = "g6-upload-insert-retry-0001"
			if _, err := s.Complete(ctx, c.Caller, created.SessionID, key); !errors.Is(err, ErrVideoUploadUnavailable) || !injected.Load() {
				t.Fatalf("未命中真实发布路径的临时错误：%v", err)
			}
			state, err := s.Get(ctx, c.Caller, created.SessionID)
			if err != nil || state.Status != "verifying" || state.InputAssetID != nil || state.CleanupPending {
				t.Fatalf("暂时写故障不能拒绝或清理：%+v %v", state, err)
			}
			var count int64
			if err := db.Model(&model.AIGatewayInputAsset{}).Where("user_id=?", c.Caller.UserID).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("失败事务不能发布输入：%d %v", count, err)
			}
			done, err := s.Complete(ctx, c.Caller, created.SessionID, key)
			if err != nil || done.Status != "completed" || done.InputAssetID == nil {
				t.Fatalf("同键恢复失败：%v", err)
			}
			if err := db.Model(&model.AIGatewayInputAsset{}).Where("user_id=?", c.Caller.UserID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("恢复只能发布一个输入：%d %v", count, err)
			}
			store.Lock()
			defer store.Unlock()
			if store.puts != 1 || store.seals != 1 || store.entries[created.SessionID].discarded {
				t.Fatal("恢复不能删除或另建规范化对象")
			}
		})
	}
	t.Run("租约接管后迟到旧执行者不得破坏赢家", func(t *testing.T) {
		db, s, store, c, data := newVideoUploadFixture(t)
		ctx := context.Background()
		var clock atomic.Int64
		clock.Store(time.Now().UTC().Truncate(time.Second).Unix())
		s.now = func() time.Time { return time.Unix(clock.Load(), 0).UTC() }
		created, err := s.Create(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.write(created.SessionID, data, "image/png"); err != nil {
			t.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		var first atomic.Bool
		var releaseOnce sync.Once
		unblock := func() { releaseOnce.Do(func() { close(release) }) }
		t.Cleanup(unblock)
		store.beforePut = func() {
			if first.CompareAndSwap(false, true) {
				close(entered)
				<-release
			}
		}
		const key = "g6-upload-lease-takeover-0001"
		oldResult := make(chan error, 1)
		go func() {
			_, err := s.Complete(ctx, c.Caller, created.SessionID, key)
			oldResult <- err
		}()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("旧执行者未进入规范化写入边界")
		}
		// 只推进可注入的业务时钟，不等待两分钟，也不改数据库租约事实。
		clock.Add(int64((3 * time.Minute) / time.Second))
		winner, err := s.Complete(ctx, c.Caller, created.SessionID, key)
		if err != nil || winner.Status != "completed" || winner.InputAssetID == nil {
			t.Fatalf("新租约执行者必须完成原输入：%v", err)
		}
		unblock()
		select {
		case err := <-oldResult:
			if !errors.Is(err, ErrVideoUploadConflict) {
				t.Fatalf("旧执行者必须被版本围栏拒绝：%v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("旧执行者没有退出")
		}
		state, err := s.Get(ctx, c.Caller, created.SessionID)
		if err != nil || state.Status != "completed" || state.InputAssetID == nil || *state.InputAssetID != *winner.InputAssetID || state.VersionNo != winner.VersionNo || state.CleanupPending {
			t.Fatalf("迟到失败不能修改赢家或安排清理：%+v %v", state, err)
		}
		var count int64
		if err := db.Model(&model.AIGatewayInputAsset{}).Where("user_id=?", c.Caller.UserID).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("接管竞争只能有一个输入：%d %v", count, err)
		}
		store.Lock()
		defer store.Unlock()
		if store.entries[created.SessionID].discarded || store.puts != 1 || store.seals != 1 {
			t.Fatal("迟到执行不能复活、丢弃或重复创建对象")
		}
	})
}
