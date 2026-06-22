package service

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"
)

// 本测试文件聚焦 D-M2-03 / D-M2-02 修复的核心：RetryOnVersionConflict 重试封装的正确性，
// 以及在「乐观锁冲突」高并发下能否做到：扣费总额正确不丢、无负余额、可重试错误不被伪装成 60001。
//
// 由于本仓库测试环境无离线 sqlite 驱动、CI 不连真实 MySQL，这里用一个内存版「乐观锁钱包」
// （simWallet）精确模拟生产 SQL 语义：
//   - 读取 version 与 balance（对应 SELECT ... 拿到快照 version）；
//   - 提交时校验 WHERE version=? AND balance>=amount（对应乐观锁 UPDATE 的原子条件）：
//     version 不匹配 -> 返回 ErrConcurrentUpdate（行影响数 0）；余额不足 -> 返回 ErrInsufficientBalance；
//     成功 -> balance-=amount, version++（对应 UPDATE ... version=version+1）。
// 这样可在不依赖 DB 的前提下，验证重试策略「该重试的重试、不该重试的立即返回、绝不透支为负」。

// simWallet 内存乐观锁钱包，模拟生产 wallets 行的 version + balance 语义。
type simWallet struct {
	mu      sync.Mutex
	balance decimal.Decimal
	version int64
}

// snapshot 读取当前 (balance, version) 快照，对应事务内 SELECT 读到的值。
func (w *simWallet) snapshot() (decimal.Decimal, int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.balance, w.version
}

// commitDeduct 用乐观锁条件提交一次扣费：
//   - expectVersion 与当前 version 不符 -> ErrConcurrentUpdate（其他事务已抢先提交）；
//   - 余额不足 -> ErrInsufficientBalance（真实业务失败，不可重试）；
//   - 成功 -> 扣减并 version++，恒不透支为负。
func (w *simWallet) commitDeduct(expectVersion int64, amount decimal.Decimal) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.version != expectVersion {
		return ErrConcurrentUpdate
	}
	if w.balance.LessThan(amount) {
		return ErrInsufficientBalance
	}
	w.balance = w.balance.Sub(amount)
	w.version++
	return nil
}

// deductOnceSim 模拟一次完整的 read-modify-write 扣费尝试（对应 deductOnce 单次事务）。
// 关键：每次调用都重新 snapshot 最新 version，绝不复用旧 version（避免死重试）。
func (w *simWallet) deductOnceSim(amount decimal.Decimal) error {
	_, ver := w.snapshot()
	return w.commitDeduct(ver, amount)
}

// TestRetryOnVersionConflict_RetrySucceeds 版本冲突重试后成功：前 N-1 次返回冲突，第 N 次成功。
func TestRetryOnVersionConflict_RetrySucceeds(t *testing.T) {
	var calls int32
	err := RetryOnVersionConflict(func() error {
		// 前 3 次模拟乐观锁冲突，第 4 次成功。
		if atomic.AddInt32(&calls, 1) <= 3 {
			return ErrConcurrentUpdate
		}
		return nil
	})
	if err != nil {
		t.Fatalf("重试后应成功，实际返回 %v", err)
	}
	if calls != 4 {
		t.Fatalf("应在第 4 次成功（共 4 次调用），实际 %d 次", calls)
	}
}

// TestRetryOnVersionConflict_ExhaustReturnsConcurrent 重试耗尽必须返回可重试错误（ErrConcurrentUpdate），
// 绝不能伪装成余额不足（ErrInsufficientBalance / 60001）——这是 D-M2-02 错误语义的关键。
func TestRetryOnVersionConflict_ExhaustReturnsConcurrent(t *testing.T) {
	var calls int32
	err := RetryOnVersionConflict(func() error {
		atomic.AddInt32(&calls, 1)
		return ErrConcurrentUpdate // 永远冲突
	})
	if !errors.Is(err, ErrConcurrentUpdate) {
		t.Fatalf("重试耗尽应返回 ErrConcurrentUpdate（可重试），实际 %v", err)
	}
	if errors.Is(err, ErrInsufficientBalance) {
		t.Fatal("乐观锁冲突绝不能被映射为 ErrInsufficientBalance（60001）")
	}
	if calls != deductMaxRetries {
		t.Fatalf("应尝试 deductMaxRetries=%d 次，实际 %d 次", deductMaxRetries, calls)
	}
}

// TestRetryOnVersionConflict_BusinessErrorNoRetry 真实业务失败（余额不足）必须立即返回、不重试。
func TestRetryOnVersionConflict_BusinessErrorNoRetry(t *testing.T) {
	var calls int32
	err := RetryOnVersionConflict(func() error {
		atomic.AddInt32(&calls, 1)
		return ErrInsufficientBalance
	})
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("余额不足应原样返回，实际 %v", err)
	}
	if calls != 1 {
		t.Fatalf("业务失败不得重试，应只调用 1 次，实际 %d 次", calls)
	}
}

// TestConcurrentDeduct_TotalCorrectNoNegative 高并发 N 次扣费：
//   - 全部成功（余额充足时），扣费总额精确、无丢失（修 D-M2-03 漏收费）；
//   - 任意时刻不透支为负；
//   - 借助 RetryOnVersionConflict，乐观锁冲突被重试吸收，无一笔因冲突被丢弃。
func TestConcurrentDeduct_TotalCorrectNoNegative(t *testing.T) {
	const n = 64
	unit := decimal.RequireFromString("1.00")
	w := &simWallet{balance: unit.Mul(decimal.NewFromInt(n))} // 恰好够扣 n 次

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = RetryOnVersionConflict(func() error {
				return w.deductOnceSim(unit)
			})
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Fatalf("第 %d 笔扣费在余额充足下不应失败（漏收费/丢扣），实际 %v", i, e)
		}
	}
	// 全部扣完，余额应精确为 0，且全程从未透支为负（commitDeduct 的余额校验保证）。
	if !w.balance.Equal(decimal.Zero) {
		t.Fatalf("并发扣费后余额应为 0（无丢失、无重复），实际 %s", w.balance)
	}
	if w.balance.LessThan(decimal.Zero) {
		t.Fatalf("出现负余额，违反资金不变量：%s", w.balance)
	}
}

// simHold 内存版 hold，模拟 wallet_holds 行的 status + 乐观锁结算语义。
type simHold struct {
	mu      sync.Mutex
	status  string // holding / settled / released
	version int64  // 与所属钱包共用一套版本竞争模型
}

// settleOnceSim 模拟一次 hold 结算尝试：读快照 version，提交时若 version 漂移则冲突。
// 幂等：非 holding 状态直接成功返回（对应 SettleHold 的 status 守卫）。
func (h *simHold) settleOnceSim(walletVer *int64, walletMu *sync.Mutex) error {
	h.mu.Lock()
	if h.status != "holding" {
		h.mu.Unlock()
		return nil // 幂等：已结算/释放
	}
	h.mu.Unlock()

	// 读取钱包 version 快照（对应 FOR UPDATE 前的读）。
	walletMu.Lock()
	expect := *walletVer
	walletMu.Unlock()

	// 提交：乐观锁条件。若期间钱包 version 被其他事务推进，则冲突重试。
	walletMu.Lock()
	defer walletMu.Unlock()
	if *walletVer != expect {
		return ErrConcurrentUpdate
	}
	*walletVer++ // 解冻 + 实扣推进钱包 version
	h.mu.Lock()
	h.status = "settled"
	h.mu.Unlock()
	return nil
}

// TestConcurrentSettle_NoHoldLeak 并发结算多笔 hold（共享同一钱包的 version 竞争）：
// 借助 RetryOnVersionConflict，每笔 hold 最终都被结算（status=settled），不会因冲突卡在
// holding 永久泄漏 frozen（修 D-M2-03 的保证金泄漏）。
func TestConcurrentSettle_NoHoldLeak(t *testing.T) {
	const n = 40
	var walletVer int64
	var walletMu sync.Mutex
	holds := make([]*simHold, n)
	for i := range holds {
		holds[i] = &simHold{status: "holding"}
	}

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(h *simHold) {
			defer wg.Done()
			if err := RetryOnVersionConflict(func() error {
				return h.settleOnceSim(&walletVer, &walletMu)
			}); err != nil {
				t.Errorf("结算应重试到成功，实际 %v", err)
			}
		}(holds[i])
	}
	wg.Wait()

	for i, h := range holds {
		if h.status != "settled" {
			t.Fatalf("hold[%d] 状态应为 settled（无泄漏），实际 %s", i, h.status)
		}
	}
}

// TestConcurrentDeduct_OverdraftStopsAtZero 并发扣费总需求超过余额时：
//   - 恰好扣到余额耗尽（无负余额）；
//   - 超出的请求返回 ErrInsufficientBalance（而非 ErrConcurrentUpdate），错误语义正确。
func TestConcurrentDeduct_OverdraftStopsAtZero(t *testing.T) {
	const n = 50
	const funded = 30 // 只够 30 笔
	unit := decimal.RequireFromString("1.00")
	w := &simWallet{balance: unit.Mul(decimal.NewFromInt(funded))}

	var wg sync.WaitGroup
	var success, insufficient int32
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := RetryOnVersionConflict(func() error {
				return w.deductOnceSim(unit)
			})
			switch {
			case err == nil:
				atomic.AddInt32(&success, 1)
			case errors.Is(err, ErrInsufficientBalance):
				atomic.AddInt32(&insufficient, 1)
			default:
				t.Errorf("非预期错误（应为余额不足或成功）：%v", err)
			}
		}()
	}
	wg.Wait()

	if success != funded {
		t.Fatalf("应恰好成功 %d 笔，实际 %d 笔", funded, success)
	}
	if insufficient != n-funded {
		t.Fatalf("应有 %d 笔余额不足，实际 %d 笔", n-funded, insufficient)
	}
	if !w.balance.Equal(decimal.Zero) {
		t.Fatalf("余额应恰好耗尽为 0，实际 %s", w.balance)
	}
	if w.balance.LessThan(decimal.Zero) {
		t.Fatalf("出现负余额，违反资金不变量：%s", w.balance)
	}
}
