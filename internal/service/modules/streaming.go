package modules

import (
	"context"
	"net/http"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// StreamingModule is the worker-side contract for long-lived streaming
// capabilities. Distinct from the request/response Module interface in
// module.go: a StreamingModule owns one session's lifetime, debits
// incrementally against a PaymentSession (default 5s cadence), and is
// expected to run for minutes-to-hours.
//
// Both interfaces can coexist on one worker behind capability-keyed
// routing — vtuber-worker-node ships StreamingModule only;
// openai-worker-node ships Module only; a hypothetical worker that
// hosts both classes uses both.
//
// Canonical reference:
//
//	livepeer-vtuber-project/docs/design-docs/streaming-session-module.md
//
// Underlying primitives (ProcessPayment, DebitBalance, SufficientBalance,
// CloseSession) live in livepeer-payment-library — see its
// streaming-session-pattern.md for the recipe this interface composes.
type StreamingModule interface {
	// Capability returns the canonical capability string this module
	// serves (e.g. "livepeer:vtuber-session"). Static for the module's
	// lifetime.
	Capability() types.CapabilityID

	// HTTPMethod is the HTTP verb the mux registers. "POST" for
	// session-open semantics. Symmetric with Module.HTTPMethod so
	// future capabilities that want a different verb stay flexible.
	HTTPMethod() string

	// HTTPPath is the URI path the mux registers (e.g.
	// "/api/sessions/start"). Stable across the module's lifetime —
	// the mux registers once at startup.
	HTTPPath() string

	// EstimateWorkUnits returns the upfront work-unit estimate used to
	// build the session-open Payment header. For streaming workloads
	// this is a small pre-credit (e.g. 1 second of work-units) — the
	// real billing happens in periodic Debit calls inside Serve.
	//
	// Conservative overestimation is fine; the worker operates under
	// an over-debit-accepted policy.
	EstimateWorkUnits(req *http.Request) (int64, error)

	// Serve owns the lifetime of one streaming session. Returns when
	// the session ends — gracefully (Close on the session control
	// plane), forced (balance exhausted, fatal error), or context-
	// cancelled (worker shutdown). Implementations are responsible for:
	//
	//   - opening the WebSocket back to the bridge for control-plane
	//     events,
	//   - opening the forward to the local backend (e.g. session-runner
	//     over localhost HTTP),
	//   - calling ps.Debit periodically (default 5s cadence),
	//   - calling ps.Sufficient after each Debit to detect low balance
	//     and emitting a session.balance.low event on the bridge ws,
	//   - calling ps.Close exactly once before returning. Defer it.
	//
	// The middleware constructs the PaymentSession at session-open and
	// tears it down when Serve returns. It does NOT call ps.Close on
	// the module's behalf — that responsibility is the module's so
	// every termination path (graceful, fatal, panic-recovered) is
	// handled uniformly.
	Serve(ctx context.Context, req *http.Request, ps PaymentSession) error
}

// PaymentSession is the per-session handle a StreamingModule uses to
// debit incrementally and check headroom. The middleware constructs
// one PaymentSession per session-open and passes it into Serve.
//
// PaymentSession wraps the receiver-side gRPC client of
// livepeer-payment-library (see internal/providers/payeedaemon for the
// underlying client). Modules MUST NOT bypass PaymentSession to talk
// to the daemon directly — the wrapper is the seam that the
// payment-middleware-check linter enforces.
type PaymentSession interface {
	// Debit charges units against this session's balance. Returns the
	// remaining balance. A negative balance is a fatal over-debit
	// (the prior Sufficient check should have prevented it; if it
	// happens, the module should emit session.error and close).
	//
	// Idempotent by debitSeq within a session: replaying the same
	// (sender, work_id, debitSeq) tuple is a no-op. Modules should
	// monotonically increment debitSeq per Debit; do not reuse across
	// sessions.
	Debit(ctx context.Context, units uint64, debitSeq uint64) (balance int64, err error)

	// Sufficient reports whether the session has at least minUnits of
	// remaining balance. Cheap; does not modify balance.
	//
	// Modules use this AFTER each Debit to detect a low-balance state
	// and emit session.balance.low (once per low-state transition,
	// not every tick). On false-then-true transitions, emit
	// session.balance.refilled and clear the grace timer.
	Sufficient(ctx context.Context, minUnits uint64) (ok bool, err error)

	// Close releases payment-daemon state for this session. MUST be
	// called exactly once by the module before Serve returns —
	// including on panic-recovered paths. Idempotent: subsequent
	// calls return nil after the first.
	Close(ctx context.Context) error
}
