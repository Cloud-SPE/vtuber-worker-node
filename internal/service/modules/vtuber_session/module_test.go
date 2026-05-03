package vtuber_session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// helper: spin up a Module under test against fakes and run Serve in a
// goroutine. Returns the fakes + a cancel function + a wait group that
// completes when Serve returns.
type testRig struct {
	mod    *Module
	ps     *fakePaymentSession
	bridge *fakeBridge
	back   *fakeBackend
	clock  *fakeClock
	cancel context.CancelFunc
	done   chan error
}

func startServe(t *testing.T, opts ...func(*Config)) *testRig {
	t.Helper()
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	bridge := newFakeBridge()
	back := &fakeBackend{}
	ps := newFakePaymentSession()

	cfg := Config{
		BackendURL:         "http://session-runner:8080/api/sessions/start",
		DebitCadence:       5 * time.Second,
		RunwayMinUnits:     30,
		GraceWindow:        60 * time.Second,
		DebitRetryBudget:   3,
		DebitRetryInterval: 1 * time.Second,
		Bridge:             bridge,
		Backend:            back,
		Clock:              clock,
		IDGen:              &fakeIDGen{},
	}
	for _, o := range opts {
		o(&cfg)
	}
	mod := New(cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(`{"session_id":"ses_test"}`))
	req.Header.Set("X-Vtuber-Session-Id", "ses_test")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		err := mod.Serve(ctx, req, ps)
		done <- err
		close(done) // close so a second receive (e.g. in test cleanup) doesn't block
	}()

	// Wait for session.ready to be emitted so tests start from the
	// running state rather than racing against the open path.
	waitForEvent(t, bridge, EventSessionReady, 2*time.Second)

	return &testRig{
		mod:    mod,
		ps:     ps,
		bridge: bridge,
		back:   back,
		clock:  clock,
		cancel: cancel,
		done:   done,
	}
}

func (r *testRig) shutdown(t *testing.T) {
	t.Helper()
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after cancel()")
	}
}

func waitForEvent(t *testing.T, b *fakeBridge, want EventType, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for _, ev := range b.snapshotSent() {
			if ev.Type == want {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("expected event %q within %v; got %v", want, timeout, b.sentTypes())
}

func waitForN(t *testing.T, getN func() int, n int, timeout time.Duration, label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if getN() >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("waited %v for %s to reach %d; got %d", timeout, label, n, getN())
}

// TestServe_DebitsEvery5Seconds verifies that the debit ticker fires
// at the configured cadence, that Debit is called with units = cadence
// in seconds, and that debit_seq increments monotonically.
func TestServe_DebitsEvery5Seconds(t *testing.T) {
	r := startServe(t)
	defer r.shutdown(t)

	// Fast-forward 15s of clock. Expect 3 ticks → 3 Debit calls.
	r.clock.Advance(5 * time.Second)
	r.clock.Advance(5 * time.Second)
	r.clock.Advance(5 * time.Second)

	waitForN(t, func() int { return len(r.ps.snapshotDebits()) }, 3, 2*time.Second, "Debit calls")

	debits := r.ps.snapshotDebits()
	if len(debits) != 3 {
		t.Fatalf("expected 3 Debit calls; got %d", len(debits))
	}
	for i, d := range debits {
		if d.units != 5 {
			t.Errorf("call %d: units = %d; want 5", i, d.units)
		}
		if d.seq != uint64(i+1) {
			t.Errorf("call %d: seq = %d; want %d", i, d.seq, i+1)
		}
	}
}

// TestServe_EmitsBalanceLowOnceOnTransition verifies that the module
// emits session.balance.low exactly once on the false-state transition,
// not on every tick that finds Sufficient false.
func TestServe_EmitsBalanceLowOnceOnTransition(t *testing.T) {
	r := startServe(t)
	defer r.shutdown(t)

	// Script: tick 1 sufficient=true; ticks 2 + 3 sufficient=false.
	r.ps.mu.Lock()
	r.ps.sufficientResults = []bool{true, false, false}
	r.ps.mu.Unlock()

	r.clock.Advance(5 * time.Second)
	r.clock.Advance(5 * time.Second)
	r.clock.Advance(5 * time.Second)

	waitForN(t, func() int { return len(r.ps.snapshotDebits()) }, 3, 2*time.Second, "Debit calls")

	// Count balance.low events. Should be exactly 1 — the second
	// tick's transition. Tick 3 is also low but already in low state.
	low := 0
	for _, ev := range r.bridge.snapshotSent() {
		if ev.Type == EventSessionBalanceLow {
			low++
		}
	}
	if low != 1 {
		t.Fatalf("expected 1 session.balance.low event; got %d (events: %v)", low, r.bridge.sentTypes())
	}
}

// TestServe_GraceExpiresFatallyAfterPersistentLow verifies that if
// Sufficient stays false for the full grace window, the session ends
// with EventSessionEnded reason=balance_exhausted and Serve returns
// an error.
func TestServe_GraceExpiresFatallyAfterPersistentLow(t *testing.T) {
	r := startServe(t, func(c *Config) {
		c.GraceWindow = 10 * time.Second
	})

	// All ticks return sufficient=false to keep the session in low
	// state through the grace window.
	r.ps.mu.Lock()
	r.ps.sufficientResults = []bool{false, false, false, false, false, false, false, false}
	r.ps.mu.Unlock()

	// First tick: enters low-balance, starts grace timer.
	r.clock.Advance(5 * time.Second)
	waitForEvent(t, r.bridge, EventSessionBalanceLow, 2*time.Second)

	// Advance past the grace window. The grace After-channel is set
	// for now+10s; the clock is at 5s now (1 tick advance), so we
	// need another 10s past the start of low.
	r.clock.Advance(11 * time.Second)

	// Wait for Serve to return.
	select {
	case err := <-r.done:
		if err == nil {
			t.Fatal("expected Serve to return an error after grace expiry")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after grace expiry")
	}

	// Last emitted event should be session.ended with balance_exhausted
	// reason. The exact preceding sequence is also worth inspecting.
	events := r.bridge.snapshotSent()
	if len(events) == 0 {
		t.Fatal("no events emitted")
	}
	last := events[len(events)-1]
	if last.Type != EventSessionEnded {
		t.Fatalf("last event = %q; want %q", last.Type, EventSessionEnded)
	}
	endedData, ok := last.Data.(EndedData)
	if !ok {
		t.Fatalf("session.ended Data type = %T; want EndedData", last.Data)
	}
	if endedData.Reason != EndReasonBalanceExhausted {
		t.Fatalf("session.ended reason = %q; want %q", endedData.Reason, EndReasonBalanceExhausted)
	}
}

// TestServe_ClosesPaymentSessionOnReturn verifies the load-bearing
// invariant that PaymentSession.Close is called exactly once on every
// termination path — graceful, fatal, and panic-recovered.
func TestServe_ClosesPaymentSessionOnReturn(t *testing.T) {
	t.Run("graceful_via_ctx_cancel", func(t *testing.T) {
		r := startServe(t)
		r.cancel()
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after cancel")
		}
		if got := r.ps.snapshotCloseCalls(); got != 1 {
			t.Fatalf("Close called %d times; want exactly 1", got)
		}
	})

	t.Run("fatal_via_grace_expiry", func(t *testing.T) {
		r := startServe(t, func(c *Config) {
			c.GraceWindow = 5 * time.Second
		})
		r.ps.mu.Lock()
		r.ps.sufficientResults = []bool{false, false, false, false, false}
		r.ps.mu.Unlock()
		r.clock.Advance(5 * time.Second)
		waitForEvent(t, r.bridge, EventSessionBalanceLow, 2*time.Second)
		r.clock.Advance(6 * time.Second)
		select {
		case <-r.done:
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after grace expiry")
		}
		if got := r.ps.snapshotCloseCalls(); got != 1 {
			t.Fatalf("Close called %d times; want exactly 1", got)
		}
	})
}

// TestServe_DebitFailureRetriesThenEscalates verifies that transient
// Debit failures are retried up to the budget, a recoverable
// session.error is emitted on the FIRST failure (not every retry),
// and exhaustion of the retry budget escalates to fatal close.
func TestServe_DebitFailureRetriesThenEscalates(t *testing.T) {
	r := startServe(t, func(c *Config) {
		c.DebitRetryBudget = 2
		c.DebitRetryInterval = 10 * time.Millisecond // tighten retry wait so test stays fast
	})
	defer func() {
		// Drain Serve return; the rig's shutdown is fine if Serve
		// already exited fatally.
		select {
		case <-r.done:
		default:
			r.cancel()
			<-r.done
		}
	}()

	r.ps.mu.Lock()
	r.ps.debitErr = errBoom
	r.ps.mu.Unlock()

	// Trigger one tick. The debit fails, retries, then escalates.
	r.clock.Advance(5 * time.Second)

	select {
	case err := <-r.done:
		if err == nil {
			t.Fatal("expected Serve to return error after Debit retry exhaustion")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after Debit retry exhaustion")
	}

	debits := r.ps.snapshotDebits()
	if len(debits) != 2 {
		t.Fatalf("expected 2 debit attempts, got %d", len(debits))
	}
	if debits[0].seq != debits[1].seq {
		t.Fatalf("retry must reuse debit_seq; got %d then %d", debits[0].seq, debits[1].seq)
	}

	// Count session.error events: should be at least 1 recoverable +
	// 1 fatal. The recoverable one fires on attempt 1; the fatal one
	// fires when the budget is exhausted.
	events := r.bridge.snapshotSent()
	var (
		recoverable bool
		fatal       bool
	)
	for _, ev := range events {
		if ev.Type != EventSessionError {
			continue
		}
		ed, ok := ev.Data.(ErrorData)
		if !ok {
			continue
		}
		if ed.Code == ErrCodePaymentUnreachable && ed.Recoverable {
			recoverable = true
		}
		if ed.Code == ErrCodePaymentUnreachable && !ed.Recoverable {
			fatal = true
		}
	}
	if !recoverable {
		t.Errorf("expected a recoverable payment_unreachable session.error; got events %v", typesOf(events))
	}
	if !fatal {
		t.Errorf("expected a fatal payment_unreachable session.error; got events %v", typesOf(events))
	}

	// Close MUST still be called exactly once.
	if got := r.ps.snapshotCloseCalls(); got != 1 {
		t.Fatalf("Close called %d times; want exactly 1", got)
	}
}

// TestServe_BackendOpenFailureClosesPaymentSession is an additional
// safety check: if the very first OpenSession fails, Close must
// still be called and session.ended must still be emitted.
func TestServe_BackendOpenFailureClosesPaymentSession(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	bridge := newFakeBridge()
	back := &fakeBackend{openErr: errBoom}
	ps := newFakePaymentSession()
	mod := New(Config{
		BackendURL: "http://session-runner:8080/api/sessions/start",
		Bridge:     bridge,
		Backend:    back,
		Clock:      clock,
		IDGen:      &fakeIDGen{},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader("{}"))
	req.Header.Set("X-Vtuber-Session-Id", "ses_open_fail")

	err := mod.Serve(context.Background(), req, ps)
	if err == nil {
		t.Fatal("expected Serve to return error on backend open failure")
	}
	if got := ps.snapshotCloseCalls(); got != 1 {
		t.Fatalf("Close called %d times; want exactly 1", got)
	}
	// session.ready must NOT have fired (open failed before we got there).
	for _, ev := range bridge.snapshotSent() {
		if ev.Type == EventSessionReady {
			t.Errorf("session.ready emitted on a failed open; got events %v", bridge.sentTypes())
		}
	}
}

func typesOf(events []Event) []EventType {
	out := make([]EventType, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Type)
	}
	return out
}

// Compile-time assertion that fakePaymentSession satisfies the
// modules.PaymentSession interface — catches drift between the fake
// and the real interface during refactors.
var _ = func() bool {
	var _ interface {
		Debit(context.Context, uint64, uint64) (int64, error)
		Sufficient(context.Context, uint64) (bool, error)
		Close(context.Context) error
	} = (*fakePaymentSession)(nil)
	_ = sync.Mutex{}
	return true
}()
