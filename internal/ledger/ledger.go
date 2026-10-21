// Package ledger stores points entries.
//
// In production the Store is backed by Postgres (table ledger_entries).
// MemoryStore is the stand-in used for local runs and tests.
package ledger

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Kind string

const (
	KindAward    Kind = "award"
	KindReversal Kind = "reversal"
)

// Entry is one line of a member's points ledger.
type Entry struct {
	MemberID  string
	PaymentID string
	Kind      Kind
	Points    int64  // positive for awards, negative for reversals
	Month     string // settlement month of the payment, "2006-01" in UTC
}

// ErrUnavailable is returned when the ledger cannot be reached. Retrying may succeed.
var ErrUnavailable = errors.New("ledger: unavailable")

// ErrUnknownPayment means there is no award to reverse.
var ErrUnknownPayment = errors.New("ledger: reversal for unknown payment")

type Store interface {
	// Award atomically deduplicates the payment, applies the cap and appends the entry.
	Award(ctx context.Context, e Entry, monthlyCap int64) error
	// Reverse atomically reverses the stored award at most once.
	Reverse(ctx context.Context, paymentID string) error
	HasEntry(ctx context.Context, paymentID string, kind Kind) (bool, error)
	AwardedInMonth(ctx context.Context, memberID, month string) (int64, error)
	Append(ctx context.Context, e Entry) error
	Balance(ctx context.Context, memberID string) (int64, error)
}

type Option func(*MemoryStore)

// WithLatency delays every call, approximating a database round trip.
func WithLatency(d time.Duration) Option {
	return func(s *MemoryStore) { s.latency = d }
}

// WithFaults installs a hook run at the start of every call (after the
// round trip). A non-nil error is returned to the caller and the call has no effect.
func WithFaults(f func(op string) error) Option {
	return func(s *MemoryStore) { s.faults = f }
}

type MemoryStore struct {
	latency time.Duration
	faults  func(op string) error

	mu      sync.Mutex
	entries []Entry
}

func NewMemoryStore(opts ...Option) *MemoryStore {
	s := &MemoryStore{}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *MemoryStore) roundTrip(ctx context.Context, op string) error {
	if s.latency > 0 {
		select {
		case <-time.After(s.latency):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.faults != nil {
		return s.faults(op)
	}
	return nil
}

func (s *MemoryStore) HasEntry(ctx context.Context, paymentID string, kind Kind) (bool, error) {
	if err := s.roundTrip(ctx, "has_entry"); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.PaymentID == paymentID && e.Kind == kind {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) AwardedInMonth(ctx context.Context, memberID, month string) (int64, error) {
	if err := s.roundTrip(ctx, "awarded_in_month"); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, e := range s.entries {
		if e.MemberID == memberID && e.Month == month && e.Kind == KindAward {
			total += e.Points
		}
	}
	return total, nil
}

func (s *MemoryStore) Append(ctx context.Context, e Entry) error {
	if err := s.roundTrip(ctx, "append"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

func (s *MemoryStore) Award(ctx context.Context, e Entry, monthlyCap int64) error {
	if err := s.roundTrip(ctx, "award"); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var used int64
	for _, entry := range s.entries {
		if entry.Kind != KindAward {
			continue
		}
		if entry.PaymentID == e.PaymentID {
			return nil
		}
		if entry.MemberID == e.MemberID && entry.Month == e.Month {
			used += entry.Points
		}
	}
	e.Kind = KindAward
	e.Points = min(e.Points, max(monthlyCap-used, 0))
	s.entries = append(s.entries, e)
	return nil
}

func (s *MemoryStore) Reverse(ctx context.Context, paymentID string) error {
	if err := s.roundTrip(ctx, "reverse"); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var award Entry
	found := false
	for _, entry := range s.entries {
		if entry.PaymentID != paymentID {
			continue
		}
		if entry.Kind == KindReversal {
			return nil
		}
		if entry.Kind == KindAward {
			award = entry
			found = true
		}
	}
	if !found {
		return ErrUnknownPayment
	}
	award.Kind = KindReversal
	award.Points = -award.Points
	s.entries = append(s.entries, award)
	return nil
}

func (s *MemoryStore) Balance(ctx context.Context, memberID string) (int64, error) {
	if err := s.roundTrip(ctx, "balance"); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, e := range s.entries {
		if e.MemberID == memberID {
			total += e.Points
		}
	}
	return total, nil
}
