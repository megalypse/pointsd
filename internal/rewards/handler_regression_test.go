package rewards_test

import (
	"testing"

	"github.com/rentpoints/pointsd/internal/ledger"
	"github.com/rentpoints/pointsd/internal/rewards"
)

func TestReversal_ConcurrentRedeliveryAcrossHandlersIsReversedOnce(t *testing.T) {
	store := ledger.NewMemoryStore(ledger.WithLatency(dbLatency))
	handlers := []*rewards.Handler{newHandler(store), newHandler(store)}
	deliver(t, handlers[0], settled("pay_1", "mbr_1", 2000))
	deliver(t, handlers[0], settled("pay_2", "mbr_1", 1500))

	var fns []func()
	for i := 0; i < 8; i++ {
		h := handlers[i%2]
		fns = append(fns, func() { deliver(t, h, reversed("pay_1", "mbr_1", 2000)) })
	}
	concurrently(fns...)

	if got := balance(t, store, "mbr_1"); got != 1500 {
		t.Fatalf("balance = %d, want 1500 (payment reversed more than once)", got)
	}
}

func TestReversal_DoesNotRestoreMonthlyCapOrAllowAnotherAward(t *testing.T) {
	store := ledger.NewMemoryStore()
	h := newHandler(store)
	deliver(t, h, settled("pay_1", "mbr_1", 10000))
	deliver(t, h, reversed("pay_1", "mbr_1", 10000))
	deliver(t, h, settled("pay_1", "mbr_1", 10000))
	deliver(t, h, settled("pay_2", "mbr_1", 1000))
	deliver(t, h, reversed("pay_2", "mbr_1", 1000))

	if got := balance(t, store, "mbr_1"); got != 0 {
		t.Fatalf("balance = %d, want 0 (reversal must preserve cap usage and award identity)", got)
	}
}
