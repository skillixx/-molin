package service

import (
	"context"
	"errors"
	"testing"

	"molin/server/internal/modules/token_gateway/model"
	"molin/server/internal/modules/token_gateway/repository"
)

func videoG5FacadeRequest(f videoG5ReservationFixture) VideoFacadeRequest {
	return VideoFacadeRequest{Prompt: f.command.Prompt, RightsPolicyVersion: f.command.RightsPolicyVersion, IdempotencyKey: f.command.IdempotencyKey, RequestID: f.command.RequestID, TaskID: f.command.TaskID, FingerprintInput: f.command.FingerprintInput}
}

// 指纹排除资产ID不能变成归属豁免：另一用户相同图片的ID也必须拒绝。
func TestVideoG5ReserveMySQLFacadeRejectsForeignInputAlias(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	prepareVideoG5I2V(t, &f)
	if _, err := f.service.ReserveAndCreate(context.Background(), f.command); err != nil {
		t.Fatal(err)
	}
	other := newVideoG5ReservationFixture(t, db, "10")
	asset := prepareVideoG5I2V(t, &other)
	c := f.command
	binding := *c.FingerprintInput.Input
	binding.InputAssetID = asset.PublicID
	c.FingerprintInput.Input = &binding
	if _, err := f.service.ReserveAndCreate(context.Background(), c); !errors.Is(err, repository.ErrVideoInputNotFound) {
		t.Fatalf("跨用户别名不得用同SHA绕过归属: %v", err)
	}
}

// 两个门面的生成命名空间相同，重放不应先创建另一门面的报价，更不能再次读取已释放输入。
func TestVideoG5ReserveMySQLCrossFacadeReplayBeforeQuote(t *testing.T) {
	db := openVideoG5MySQL(t)
	for _, i2v := range []bool{false, true} {
		f := newVideoG5ReservationFixture(t, db, "10")
		if i2v {
			prepareVideoG5I2V(t, &f)
		}
		facade := NewVideoQuoteFacade(f.quotes, f.service)
		r := videoG5FacadeRequest(f)
		first, err := facade.GenerateWithTokenQuote(context.Background(), r, f.command.QuotePublicID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.service.CancelBeforeSubmit(context.Background(), first.TaskID, f.owner); err != nil {
			t.Fatal(err)
		}
		var before int64
		if err := db.Model(&model.AIGatewayQuote{}).Where("user_id=?", f.owner.UserID).Count(&before).Error; err != nil {
			t.Fatal(err)
		}
		// 已有任务的重放只读冻结事实，不依赖报价器重新读取输入。
		f.quotes.WithInputSnapshotResolver(nil)
		r.RequestID += "_replay"
		r.TaskID += "_replay"
		got, err := facade.CreateOpenAIVideo(context.Background(), r)
		if err != nil || !got.Existing || got.RequestID != first.RequestID || got.TaskID != first.TaskID || got.Quote.PublicID != first.Quote.PublicID {
			t.Fatalf("跨门面应返回原已取消请求: %+v %v", got, err)
		}
		var after int64
		if err := db.Model(&model.AIGatewayQuote{}).Where("user_id=?", f.owner.UserID).Count(&after).Error; err != nil || after != before {
			t.Fatalf("重放不得多建自动报价: %d -> %d %v", before, after, err)
		}
	}
}

func TestVideoG5ReserveMySQLAutoQuoteAfterAuthorization(t *testing.T) {
	db := openVideoG5MySQL(t)
	f := newVideoG5ReservationFixture(t, db, "10")
	if err := db.Exec("UPDATE api_keys SET status='revoked' WHERE id=?", *f.owner.APIKeyID).Error; err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := db.Model(&model.AIGatewayQuote{}).Where("user_id=?", f.owner.UserID).Count(&before).Error; err != nil {
		t.Fatal(err)
	}
	_, err := NewVideoQuoteFacade(f.quotes, f.service).CreateOpenAIVideo(context.Background(), videoG5FacadeRequest(f))
	if !errors.Is(err, ErrVideoBillingAccess) {
		t.Fatalf("撤销Key必须拒绝: %v", err)
	}
	var after int64
	if err := db.Model(&model.AIGatewayQuote{}).Where("user_id=?", f.owner.UserID).Count(&after).Error; err != nil || after != before {
		t.Fatalf("权限拒绝之前不能写Quote: %d -> %d %v", before, after, err)
	}
}
