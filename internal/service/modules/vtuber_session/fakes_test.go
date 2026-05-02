package vtuber_session

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// fakePaymentSession captures every call so tests can assert sequence
// and counts. Behavior knobs (DebitErr, SufficientReturns, etc.) let
// tests script low-balance / failure paths.
//
// Concurrency-safe; the module's debit ticker runs on a separate
// goroutine and may overlap with assertion code in tests.
type fakePaymentSession struct {
	mu sync.Mutex

	// Behavior knobs.
	debitErr          error
	debitErrUntilCall int   // if >0, return debitErr until this many calls have been made
	balanceAfterDebit int64 // returned by Debit (after the units are subtracted; tests script directly)
	sufficientResults []bool
	sufficientErr     error
	closeErr          error

	// Recorded calls.
	debitCalls      []debitCall
	sufficientCalls []sufficientCall
	closeCalls      int
	sufficientIdx   int
}

type debitCall struct {
	units uint64
	seq   uint64
}

type sufficientCall struct {
	minUnits uint64
}

func newFakePaymentSession() *fakePaymentSession {
	return &fakePaymentSession{
		balanceAfterDebit: 1_000_000,
	}
}

func (f *fakePaymentSession) Debit(_ context.Context, units uint64, debitSeq uint64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.debitCalls = append(f.debitCalls, debitCall{units: units, seq: debitSeq})
	if f.debitErrUntilCall > 0 && len(f.debitCalls) <= f.debitErrUntilCall {
		return 0, f.debitErr
	}
	if f.debitErr != nil && f.debitErrUntilCall == 0 {
		return 0, f.debitErr
	}
	return f.balanceAfterDebit, nil
}

func (f *fakePaymentSession) Sufficient(_ context.Context, minUnits uint64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sufficientCalls = append(f.sufficientCalls, sufficientCall{minUnits: minUnits})
	if f.sufficientErr != nil {
		return false, f.sufficientErr
	}
	if f.sufficientIdx >= len(f.sufficientResults) {
		// Default: sufficient when no scripted results remain.
		return true, nil
	}
	r := f.sufficientResults[f.sufficientIdx]
	f.sufficientIdx++
	return r, nil
}

func (f *fakePaymentSession) Close(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCalls++
	return f.closeErr
}

func (f *fakePaymentSession) snapshotDebits() []debitCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]debitCall, len(f.debitCalls))
	copy(out, f.debitCalls)
	return out
}

func (f *fakePaymentSession) snapshotCloseCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCalls
}

// fakeBridge records outbound events and serves a scripted inbound
// queue. M3 acceptance test uses this to assert event emission
// sequence; bridge-inbound is scripted so tests can drive
// session.end / session.persona.update / etc. paths once those land.
type fakeBridge struct {
	mu sync.Mutex

	sentEvents []Event
	inbound    chan Event
	closed     bool
	sendErr    error
}

func newFakeBridge() *fakeBridge {
	return &fakeBridge{
		inbound: make(chan Event, 16),
	}
}

func (b *fakeBridge) Send(_ context.Context, ev Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrBridgeClosed
	}
	if b.sendErr != nil {
		return b.sendErr
	}
	b.sentEvents = append(b.sentEvents, ev)
	return nil
}

func (b *fakeBridge) Recv(ctx context.Context) (Event, error) {
	select {
	case ev, ok := <-b.inbound:
		if !ok {
			return Event{}, ErrBridgeClosed
		}
		return ev, nil
	case <-ctx.Done():
		return Event{}, ctx.Err()
	}
}

func (b *fakeBridge) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		close(b.inbound)
	}
	return nil
}

func (b *fakeBridge) snapshotSent() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Event, len(b.sentEvents))
	copy(out, b.sentEvents)
	return out
}

func (b *fakeBridge) sentTypes() []EventType {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]EventType, 0, len(b.sentEvents))
	for _, ev := range b.sentEvents {
		out = append(out, ev.Type)
	}
	return out
}

// fakeBackend records OpenSession / Close calls. By default
// OpenSession returns nil (success); set OpenErr to simulate
// session-runner-down.
type fakeBackend struct {
	mu sync.Mutex

	openErr  error
	closeErr error

	openCalls  int
	closeCalls int
}

func (b *fakeBackend) OpenSession(_ context.Context, _ *http.Request, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.openCalls++
	return b.openErr
}

func (b *fakeBackend) Close(_ context.Context, _ string, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closeCalls++
	return b.closeErr
}

// fakeClock is a deterministic clock for driving the debit ticker
// without sleeping. Advance() releases queued tick events; After()
// returns a channel that fires when an Advance crosses its deadline.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
	timers  []*fakeTimer
}

type fakeTicker struct {
	c        chan time.Time
	interval time.Duration
	last     time.Time
	stopped  bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.c }
func (t *fakeTicker) Stop()               { t.stopped = true }

type fakeTimer struct {
	c        chan time.Time
	deadline time.Time
	fired    bool
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Ticker(d time.Duration) Ticker {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTicker{
		c:        make(chan time.Time, 32),
		interval: d,
		last:     c.now,
	}
	c.tickers = append(c.tickers, t)
	return t
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{
		c:        make(chan time.Time, 1),
		deadline: c.now.Add(d),
	}
	c.timers = append(c.timers, t)
	return t.c
}

// Advance moves the clock forward by d, firing all tickers + timers
// whose next deadline is at or before the new "now". Returns once
// every fired event has been sent on its channel.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	target := c.now
	tickers := append([]*fakeTicker(nil), c.tickers...)
	timers := append([]*fakeTimer(nil), c.timers...)
	c.mu.Unlock()

	for _, t := range tickers {
		if t.stopped {
			continue
		}
		for !t.last.Add(t.interval).After(target) {
			t.last = t.last.Add(t.interval)
			select {
			case t.c <- t.last:
			default:
				// Channel buffer full — drop. Tests should size
				// their advances to drain ticks promptly.
			}
		}
	}
	for _, t := range timers {
		if t.fired {
			continue
		}
		if !t.deadline.After(target) {
			t.fired = true
			select {
			case t.c <- target:
			default:
			}
		}
	}
}

// fakeIDGen returns evt_001, evt_002, ... — deterministic for tests.
type fakeIDGen struct {
	mu sync.Mutex
	n  int
}

func (g *fakeIDGen) NextID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.n++
	return fakeIDFmt(g.n)
}

func fakeIDFmt(n int) string {
	const prefix = "evt_"
	const width = 3
	s := []byte(prefix)
	digits := make([]byte, 0, width)
	if n == 0 {
		digits = append(digits, '0')
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	for len(digits) < width {
		digits = append([]byte{'0'}, digits...)
	}
	return string(append(s, digits...))
}

// errBoom is a sentinel error used in test scripts.
var errBoom = errors.New("boom")
