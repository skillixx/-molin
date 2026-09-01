package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"molin/server/internal/modules/token_gateway/repository"
)

// 两个真实连接固定发生顺序：输家先读到不存在，赢家提交，输家再插入并读取原Quote。
// 该边界与平台Quote外层RR事务一致，不用随机休眠碰运气，也不Mock报价仓储。
func TestVideoG6QuoteRepeatableReadWinnerMySQL(t *testing.T) {
	db := openVideoG6MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key, kind := "g6-quote-snapshot-race-0001", VideoQuoteCommandKindExplicit
	quote := *f.quote
	quote.ID, quote.PublicID = 0, "vid_quote_g6_snapshot_winner"
	quote.IdempotencyKey, quote.CommandKind = &key, &kind
	tx := db.WithContext(ctx).Begin(&sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	loser := repository.NewVideoQuoteRepository(tx)
	if _, found, err := loser.FindIdempotent(ctx, quote.UserID, quote.ProjectID, kind, key, quote.RequestFingerprint); err != nil || found {
		t.Fatalf("初始快照应没有目标Quote：found=%v err=%v", found, err)
	}
	winner, existing, err := repository.NewVideoQuoteRepository(db).CreateIdempotent(ctx, &quote)
	if err != nil || existing {
		t.Fatalf("另一连接创建赢家失败：%v", err)
	}
	quote.PublicID = "vid_quote_g6_snapshot_loser"
	got, existing, err := loser.CreateIdempotent(ctx, &quote)
	if err != nil || !existing || got == nil || got.PublicID != winner.PublicID {
		t.Fatalf("旧RR快照必须返回已提交赢家：existing=%v err=%v", existing, err)
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("ai_gateway_quotes").Where("user_id=? AND project_id=? AND command_kind=? AND idempotency_key=?", quote.UserID, quote.ProjectID, kind, key).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("双连接竞争必须只有一个报价事实：count=%d err=%v", count, err)
	}
}
