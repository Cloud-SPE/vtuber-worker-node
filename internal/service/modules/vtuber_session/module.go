package vtuber_session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Cloud-SPE/vtuber-worker-node/internal/service/modules"
	"github.com/Cloud-SPE/vtuber-worker-node/internal/types"
)

// Capability is the canonical capability string this module serves.
// Matches the example in
// livepeer-modules-project/service-registry-daemon/docs/design-docs/workload-agnostic-strings.md
// and is what worker.yaml advertises.
const Capability types.CapabilityID = "livepeer:vtuber-session"

// HTTPMethod and HTTPPath are the worker-facing endpoint the bridge
// POSTs session-open to.
const (
	HTTPMethod = http.MethodPost
	HTTPPath   = "/api/sessions/start"
)

// Default tuning values; overridable via Config. These match the
// reference numbers in livepeer-vtuber-project/docs/design-docs/
// streaming-session-module.md.
const (
	DefaultDebitCadence       = 5 * time.Second
	DefaultRunwayMinUnits     = uint64(30) // 30 seconds of runway
	DefaultGraceWindow        = 60 * time.Second
	DefaultDebitRetryBudget   = 3
	DefaultDebitRetryInterval = 1 * time.Second
)

// Config carries the dependencies the module needs to run a session.
// Production wires:
//
//	BackendURL: from worker.yaml's models[].backend_url
//	Bridge:    a real WebSocket-backed BridgeControlPlane (TODO: lands
//	           with vtuber-livepeer-bridge-mvp's session-open contract)
//	Backend:   NewHTTPBackend(nil)
//	Clock:     SystemClock{}
//	IDGen:     NewCounterIDGen(sessionID)
//
// Tests inject fakes for everything.
type Config struct {
	BackendURL string

	// DebitCadence overrides DefaultDebitCadence when non-zero.
	DebitCadence time.Duration
	// RunwayMinUnits overrides DefaultRunwayMinUnits when non-zero.
	RunwayMinUnits uint64
	// GraceWindow overrides DefaultGraceWindow when non-zero.
	GraceWindow time.Duration
	// DebitRetryBudget overrides DefaultDebitRetryBudget when > 0.
	DebitRetryBudget int
	// DebitRetryInterval overrides DefaultDebitRetryInterval when > 0.
	DebitRetryInterval time.Duration

	Bridge  BridgeControlPlane
	Backend BackendForward
	Clock   Clock
	IDGen   IDGenerator
	Logger  *slog.Logger
}

// withDefaults returns a Config with zero-valued fields populated by
// the package defaults. Mutates a copy; the input is left untouched.
func (c Config) withDefaults() Config {
	if c.DebitCadence == 0 {
		c.DebitCadence = DefaultDebitCadence
	}
	if c.RunwayMinUnits == 0 {
		c.RunwayMinUnits = DefaultRunwayMinUnits
	}
	if c.GraceWindow == 0 {
		c.GraceWindow = DefaultGraceWindow
	}
	if c.DebitRetryBudget <= 0 {
		c.DebitRetryBudget = DefaultDebitRetryBudget
	}
	if c.DebitRetryInterval <= 0 {
		c.DebitRetryInterval = DefaultDebitRetryInterval
	}
	if c.Bridge == nil {
		c.Bridge = NewStubBridge()
	}
	if c.Backend == nil {
		c.Backend = NewHTTPBackend(nil)
	}
	if c.Clock == nil {
		c.Clock = SystemClock{}
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// Module implements modules.StreamingModule.
type Module struct {
	cfg Config
}

// New constructs a Module with the given config. Required: BackendURL.
// Everything else falls back to package defaults if zero-valued.
func New(cfg Config) *Module {
	return &Module{cfg: cfg.withDefaults()}
}

// Capability returns the canonical capability string.
func (m *Module) Capability() types.CapabilityID { return Capability }

// HTTPMethod is always POST.
func (m *Module) HTTPMethod() string { return HTTPMethod }

// HTTPPath returns the worker-facing endpoint.
func (m *Module) HTTPPath() string { return HTTPPath }

// EstimateWorkUnits returns the upfront pre-credit (1 second of work-
// units). The real billing happens in Serve's debit ticker.
func (m *Module) EstimateWorkUnits(req *http.Request) (int64, error) {
	return 1, nil
}

// Verify Module satisfies the StreamingModule interface at compile time.
var _ modules.StreamingModule = (*Module)(nil)

// session captures the per-Serve mutable state. Each Serve call gets
// its own session value; the Module struct is shared (read-only) across
// concurrent sessions.
type session struct {
	cfg       Config
	sessionID string
	ps        modules.PaymentSession

	// Lifecycle state. Updated only from the main Serve goroutine.
	cumulativeUnits uint64
	debitSeq        uint64
	consecutiveDebitFailures int

	// Low-balance state. Once entered, stays low until a Sufficient
	// returns true (refilled) or grace expires (fatal).
	inLowBalance   bool
	graceTimerCh   <-chan time.Time

	// Final reason, set by the goroutine that triggers shutdown.
	endReasonMu sync.Mutex
	endReason   EndReason
}

// Serve runs one session's lifetime. Returns when the session ends —
// graceful (bridge sent session.end), forced (balance exhausted past
// grace), context-cancelled (worker shutdown), or fatal error.
//
// The PaymentSession.Close call is deferred at the top so every
// termination path — including panics — releases payment-daemon state.
//
// Concurrency model (6 goroutines per session):
//
//  1. main (this function) — drives the state machine and the debit
//     ticker; blocks on the event channel.
//  2. bridgeReader — reads inbound events from the bridge and forwards
//     to backend or to the main loop.
//  3. backendReader — reads outbound events from the backend (e.g.
//     session-runner's transcript / heartbeat / error events) and
//     pushes to the bridge writer.
//  4. bridgeWriter — serializes outbound events to the bridge ws.
//  5. (M7) backend lifecycle watcher — detects backend forward closing
//     unexpectedly. For M3, the backend is fire-and-forget at
//     session-open; the watcher lands when session-runner exposes a
//     teardown signal.
//  6. (M3 only sketched) supervisor for goroutine errors — for M3 we
//     use a small errgroup-equivalent local pattern.
//
// M3 lands goroutines (1) and the debit ticker on the main loop.
// (2)/(3)/(4) are present as goroutines but Recv stubs out via
// NewStubBridge() until the bridge contract lands.
func (m *Module) Serve(ctx context.Context, req *http.Request, ps modules.PaymentSession) (retErr error) {
	if ps == nil {
		return errors.New("vtuber_session: nil PaymentSession")
	}
	if m.cfg.BackendURL == "" {
		return errors.New("vtuber_session: empty BackendURL in config")
	}

	sessionID := extractSessionID(req)
	idGen := m.cfg.IDGen
	if idGen == nil {
		idGen = NewCounterIDGen(sessionID)
	}
	cfg := m.cfg
	cfg.IDGen = idGen
	logger := cfg.Logger.With(slog.String("session_id", sessionID))
	logger.Info("vtuber_session: starting", "backend_url", cfg.BackendURL)

	s := &session{
		cfg:       cfg,
		sessionID: sessionID,
		ps:        ps,
	}

	// Close the PaymentSession on every exit path, including panic.
	// Best-effort: log but don't surface a Close error since Serve's
	// retErr is the more meaningful failure to the caller.
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("vtuber_session: panic: %v", r)
			s.recordEnd(EndReasonFatalError)
			s.emitErrorBest(ctx, ErrCodeFatal, fmt.Sprintf("panic: %v", r), false)
		}
		// Emit session.ended BEFORE closing the bridge so the event
		// reaches the bridge. Then close the bridge transport. Then
		// release the PaymentSession (last so its closeCtx isn't
		// affected by other failures above).
		s.emitEndedBest(ctx)
		_ = cfg.Bridge.Close()
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := ps.Close(closeCtx); err != nil {
			logger.Warn("PaymentSession.Close failed", "err", err)
		}
	}()

	// 1. Forward session-open to the backend. If this fails we don't
	// even reach the running state — emit error, mark fatal, return.
	openCtx, openCancel := context.WithTimeout(ctx, 30*time.Second)
	if err := cfg.Backend.OpenSession(openCtx, req, cfg.BackendURL); err != nil {
		openCancel()
		s.recordEnd(EndReasonFatalError)
		s.emitErrorBest(ctx, ErrCodeBackendDown, err.Error(), false)
		logger.Error("backend OpenSession failed", "err", err)
		return fmt.Errorf("backend OpenSession: %w", err)
	}
	openCancel()

	// 2. Backend accepted. Emit session.ready.
	s.emit(ctx, EventSessionReady, nil)
	logger.Info("vtuber_session: ready")

	// 3. Run the lifecycle. RunLoop returns when the session ends.
	if err := s.runLoop(ctx); err != nil {
		// Fatal errors during the run loop already emitted session.error;
		// here we just propagate.
		return err
	}
	return nil
}

// extractSessionID picks the session_id from the request — header
// preferred, then a path-tail fallback. Real shape lands when the
// bridge's session-open contract is finalized; for M3 the header is
// the simplest stable extraction.
func extractSessionID(req *http.Request) string {
	if req == nil {
		return "unknown"
	}
	if id := req.Header.Get("X-Vtuber-Session-Id"); id != "" {
		return id
	}
	return "unknown"
}

// runLoop drives the debit ticker and reacts to bridge inbound events
// + grace timer firings. Returns nil on graceful close, error on fatal.
func (s *session) runLoop(ctx context.Context) error {
	ticker := s.cfg.Clock.Ticker(s.cfg.DebitCadence)
	defer ticker.Stop()
	tickC := ticker.C()

	// Bridge inbound events. For M3 with a stub bridge, Recv returns
	// immediately with ErrBridgeNotConfigured; we close inboundCh so
	// the select doesn't loop on that one branch forever.
	inboundCh := make(chan Event, 16)
	inboundErrCh := make(chan error, 1)
	go s.bridgeReader(ctx, inboundCh, inboundErrCh)

	for {
		select {
		case <-ctx.Done():
			s.recordEnd(EndReasonShutdown)
			return ctx.Err()

		case <-tickC:
			if err := s.tick(ctx); err != nil {
				return err
			}

		case <-s.graceTimerCh:
			// Grace window expired. Re-check Sufficient one last time —
			// a top-up may have landed between the timer firing and now.
			if !s.checkSufficientNoEmit(ctx) {
				s.recordEnd(EndReasonBalanceExhausted)
				s.emit(ctx, EventSessionError, ErrorData{
					Code:        ErrCodeBalanceExhausted,
					Message:     "grace window expired with insufficient balance",
					Recoverable: false,
				})
				return errors.New("vtuber_session: balance exhausted past grace")
			}
			// Balance refilled in the meantime; clear low state.
			s.exitLowBalance(ctx)

		case ev, ok := <-inboundCh:
			if !ok {
				// Bridge reader closed cleanly. M3 stub: this happens
				// immediately. Real behavior: this is the bridge
				// disconnecting; treat as fatal-after-retries (M7).
				continue
			}
			if ev.Type == "session.end" {
				s.recordEnd(EndReasonUserEnd)
				s.cfg.Logger.Info("vtuber_session: bridge requested end")
				return nil
			}
			// Other inbound events (user.chat.send, persona.update,
			// interrupt) are forwarded to the backend in M7. For M3
			// we log and ignore.
			s.cfg.Logger.Debug("inbound bridge event ignored (M3)", "type", string(ev.Type))

		case err := <-inboundErrCh:
			if errors.Is(err, ErrBridgeNotConfigured) || errors.Is(err, ErrBridgeClosed) {
				// Stub bridge or clean close. Don't loop on the error
				// channel; null it so this branch never re-fires.
				inboundErrCh = nil
				continue
			}
			s.cfg.Logger.Warn("bridge reader error", "err", err)
			inboundErrCh = nil
		}
	}
}

// tick performs one Debit + Sufficient cycle.
func (s *session) tick(ctx context.Context) error {
	units := uint64(s.cfg.DebitCadence / time.Second)
	if units == 0 {
		units = 1
	}
	balance, err := s.debitWithRetry(ctx, units)
	if err != nil {
		// Already-emitted payment_unreachable; if we're here, the
		// retry budget is exhausted.
		s.recordEnd(EndReasonFatalError)
		s.emit(ctx, EventSessionError, ErrorData{
			Code:        ErrCodePaymentUnreachable,
			Message:     err.Error(),
			Recoverable: false,
		})
		return fmt.Errorf("debit: %w", err)
	}
	if balance < 0 {
		s.recordEnd(EndReasonFatalError)
		s.emit(ctx, EventSessionError, ErrorData{
			Code:        ErrCodeBalanceExhausted,
			Message:     fmt.Sprintf("over-debit: balance %d", balance),
			Recoverable: false,
		})
		return errors.New("vtuber_session: over-debit")
	}
	s.cumulativeUnits += units
	s.emit(ctx, EventSessionUsageTick, UsageTickData{
		WorkUnits:           units,
		WorkUnitKind:        "second",
		CumulativeWorkUnits: s.cumulativeUnits,
	})

	// Sufficient check. Only emit balance.low on transition false →
	// false (entry); only emit balance.refilled on transition true →
	// true (exit).
	ok, err := s.ps.Sufficient(ctx, s.cfg.RunwayMinUnits)
	if err != nil {
		// Treat Sufficient errors as transient (don't escalate to
		// fatal here; the next Debit will surface payment-daemon
		// failures via the retry budget).
		s.cfg.Logger.Warn("Sufficient check failed", "err", err)
		return nil
	}
	if !ok && !s.inLowBalance {
		s.enterLowBalance(ctx, balance)
	} else if ok && s.inLowBalance {
		s.exitLowBalance(ctx)
	}
	return nil
}

// debitWithRetry runs Debit with the configured retry budget. Returns
// the final balance on success; an error if the retry budget is
// exhausted. Emits one warn log per failure and one
// recoverable-error event on the FIRST failure.
func (s *session) debitWithRetry(ctx context.Context, units uint64) (int64, error) {
	for attempt := 1; attempt <= s.cfg.DebitRetryBudget; attempt++ {
		s.debitSeq++
		balance, err := s.ps.Debit(ctx, units, s.debitSeq)
		if err == nil {
			if s.consecutiveDebitFailures > 0 {
				s.cfg.Logger.Info("Debit recovered after transient failures",
					"recovered_after", s.consecutiveDebitFailures)
			}
			s.consecutiveDebitFailures = 0
			return balance, nil
		}
		s.consecutiveDebitFailures++
		s.cfg.Logger.Warn("Debit failed; will retry",
			"attempt", attempt, "budget", s.cfg.DebitRetryBudget, "err", err)
		if attempt == 1 {
			// Surface a recoverable warning to the bridge on the first
			// failure (don't spam on every retry).
			s.emit(ctx, EventSessionError, ErrorData{
				Code:        ErrCodePaymentUnreachable,
				Message:     err.Error(),
				Recoverable: true,
			})
		}
		if attempt < s.cfg.DebitRetryBudget {
			// Real time.After here on purpose: the retry interval is a
			// small operational delay (defaults to 1s; tests use ms).
			// The Clock interface is for the cadence ticker and grace
			// timer where deterministic Advance matters; gating the
			// retry sleep on Clock.After would force tests to race
			// goroutine entry against fakeClock.Advance.
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(s.cfg.DebitRetryInterval):
			}
		}
	}
	return 0, fmt.Errorf("Debit failed after %d attempts", s.cfg.DebitRetryBudget)
}

func (s *session) enterLowBalance(ctx context.Context, balance int64) {
	s.inLowBalance = true
	s.graceTimerCh = s.cfg.Clock.After(s.cfg.GraceWindow)
	s.emit(ctx, EventSessionBalanceLow, BalanceLowData{
		RunwayUnitsRemaining: balance,
	})
}

func (s *session) exitLowBalance(ctx context.Context) {
	s.inLowBalance = false
	s.graceTimerCh = nil
	// Read current balance via Sufficient (cheap; doesn't modify
	// balance). Emitting balance.refilled with a numeric runway is
	// nicer for the bridge but a Sufficient call doesn't return the
	// exact balance — that's fine, the bridge translates units to
	// USD on its side and uses its own threshold.
	s.emit(ctx, EventSessionBalanceRefilled, BalanceRefilledData{
		NewBalanceUnits: int64(s.cfg.RunwayMinUnits),
	})
}

// checkSufficientNoEmit re-checks the Sufficient state without
// emitting any event — used when the grace timer fires to give the
// session one last chance to recover.
func (s *session) checkSufficientNoEmit(ctx context.Context) bool {
	ok, err := s.ps.Sufficient(ctx, s.cfg.RunwayMinUnits)
	if err != nil {
		return false
	}
	return ok
}

// bridgeReader drains the bridge inbound channel until the bridge
// closes or ctx is cancelled. M3 with a stub bridge: returns
// immediately with ErrBridgeNotConfigured.
func (s *session) bridgeReader(ctx context.Context, ch chan<- Event, errCh chan<- error) {
	defer close(ch)
	for {
		ev, err := s.cfg.Bridge.Recv(ctx)
		if err != nil {
			select {
			case errCh <- err:
			case <-ctx.Done():
			}
			return
		}
		select {
		case ch <- ev:
		case <-ctx.Done():
			return
		}
	}
}

// emit sends an outbound event over the bridge. Best-effort: a stub
// bridge or a transient send error is logged but does not fail the
// session. Real bridge contracts in M7 may upgrade send-failure
// handling to the disconnect/retry path.
func (s *session) emit(ctx context.Context, typ EventType, data any) {
	ev := Event{
		EventID:   s.cfg.IDGen.NextID(),
		Type:      typ,
		SessionID: s.sessionID,
		TsMs:      s.cfg.Clock.Now().UnixMilli(),
		Data:      data,
	}
	if err := s.cfg.Bridge.Send(ctx, ev); err != nil {
		// Don't escalate stub/closed-bridge errors during M3.
		if errors.Is(err, ErrBridgeNotConfigured) || errors.Is(err, ErrBridgeClosed) {
			return
		}
		s.cfg.Logger.Warn("bridge.Send failed", "type", string(typ), "err", err)
	}
}

// emitErrorBest is a deferred-friendly emit that swallows any failure.
func (s *session) emitErrorBest(ctx context.Context, code ErrorCode, msg string, recoverable bool) {
	s.emit(ctx, EventSessionError, ErrorData{
		Code:        code,
		Message:     msg,
		Recoverable: recoverable,
	})
}

// emitEndedBest emits the final session.ended event with the recorded
// reason. Best-effort: bridge may already be closed by this point.
func (s *session) emitEndedBest(ctx context.Context) {
	s.endReasonMu.Lock()
	reason := s.endReason
	if reason == "" {
		reason = EndReasonUserEnd // default to graceful if Serve returned cleanly
	}
	s.endReasonMu.Unlock()
	s.emit(ctx, EventSessionEnded, EndedData{
		Reason:               reason,
		FinalCumulativeUnits: s.cumulativeUnits,
	})
}

// recordEnd captures the termination reason for emitEndedBest. Safe
// to call from any goroutine.
func (s *session) recordEnd(reason EndReason) {
	s.endReasonMu.Lock()
	if s.endReason == "" {
		s.endReason = reason
	}
	s.endReasonMu.Unlock()
}
